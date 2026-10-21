package pdfops

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LibreOffice converts Office documents using an isolated, disposable user
// profile so concurrent jobs never touch the user's LibreOffice session.
type LibreOffice struct {
	runner Runner
	// isolated runs soffice in a network namespace of its own, so a document
	// that links a remote image cannot reach anything. Only the real runner is
	// wrapped; tests with a fake runner see the plain soffice command.
	isolated bool
}

func NewLibreOffice(runner Runner) *LibreOffice {
	if runner == nil {
		return &LibreOffice{runner: defaultRunTool, isolated: networkIsolation()}
	}
	return &LibreOffice{runner: runner}
}

func (l *LibreOffice) ID() string { return EngineLibreOffice }

func findLibreOfficeBinary(lookPath func(string) (string, error)) (string, error) {
	for _, name := range []string{"soffice", "libreoffice"} {
		if _, err := lookPath(name); err == nil {
			return name, nil
		}
	}
	return "", fmt.Errorf("libreoffice is not installed")
}

func (l *LibreOffice) Available() error {
	_, err := findLibreOfficeBinary(exec.LookPath)
	return err
}

const libreOfficeTimeout = 120 * time.Second

func (l *LibreOffice) Convert(ctx context.Context, input, output string) error {
	binary, err := findLibreOfficeBinary(exec.LookPath)
	if err != nil {
		return err
	}
	convertCtx, cancel := context.WithTimeout(ctx, libreOfficeTimeout)
	defer cancel()
	if err := convertCtx.Err(); err != nil {
		return err
	}
	profile, err := os.MkdirTemp("", "file-converter-lo-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(profile)
	if err := writeProfileSettings(profile); err != nil {
		return err
	}
	outDir, err := os.MkdirTemp("", "file-converter-lo-out-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(outDir)
	if err := checkOfficeContent(input); err != nil {
		return err
	}
	// soffice rejects "--", so the input must not look like an option: an
	// absolute path always starts with a slash.
	if input, err = filepath.Abs(input); err != nil {
		return err
	}
	args := []string{"--headless", "--norestore", "--convert-to", "pdf", "--outdir", outDir,
		"-env:UserInstallation=file://" + profile, input}
	if l.isolated {
		binary, args = "unshare", append([]string{"--map-root-user", "--net", "--", binary}, args...)
	}
	if err := l.runner(convertCtx, binary, args); err != nil {
		if convertCtx.Err() != nil {
			return convertCtx.Err()
		}
		return fmt.Errorf("soffice convert: %w", err)
	}
	generated := filepath.Join(outDir, strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))+".pdf")
	data, err := os.ReadFile(generated)
	if err != nil {
		return fmt.Errorf("soffice produced no PDF: %w", err)
	}
	if len(data) < len("%PDF-") || string(data[:len("%PDF-")]) != "%PDF-" {
		return fmt.Errorf("soffice produced an invalid PDF")
	}
	if err := os.WriteFile(output, data, 0o600); err != nil {
		return fmt.Errorf("publish soffice PDF: %w", err)
	}
	return nil
}

// checkOfficeContent refuses a file whose bytes do not match its extension.
// LibreOffice would otherwise import a damaged ".docx" as plain text and
// produce a PDF of the garbage, which looks like success.
func checkOfficeContent(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, 8)
	n, _ := f.Read(head)
	head = head[:n]
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	var want []byte
	switch ext {
	case "docx", "xlsx", "pptx", "odt", "ods", "odp":
		want = []byte("PK\x03\x04")
	case "doc", "xls", "ppt":
		want = []byte{0xD0, 0xCF, 0x11, 0xE0}
	case "rtf":
		want = []byte("{\\rtf")
	default:
		return nil
	}
	if !bytes.HasPrefix(head, want) {
		return fmt.Errorf("%s is not a valid .%s document", filepath.Base(path), ext)
	}
	return nil
}

// profileSettings is written into the throwaway profile. Macros are set to the
// strictest level, which is also the default for a fresh profile; stating it
// keeps that from depending on a default.
const profileSettings = `<?xml version="1.0" encoding="UTF-8"?>
<oor:items xmlns:oor="http://openoffice.org/2001/registry" xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
<item oor:path="/org.openoffice.Office.Common/Security/Scripting"><prop oor:name="MacroSecurityLevel" oor:op="fuse"><value>3</value></prop></item>
</oor:items>
`

var (
	isolationOnce   sync.Once
	isolationAvails bool
)

// networkIsolation reports whether soffice can be run without a network. A
// document can link an image on a web server, and LibreOffice fetches some of
// those links while converting (a DOCX did, although an ODT did not), which
// tells the sender when and from where the file was opened. Proxy settings and
// the link-update options did not stop it; an empty network namespace does. It
// needs unprivileged user namespaces, which some distributions restrict, in
// which case conversion still works and the link can be fetched.
func networkIsolation() bool {
	isolationOnce.Do(func() {
		isolationAvails = exec.Command("unshare", "--map-root-user", "--net", "--", "true").Run() == nil
	})
	return isolationAvails
}

func writeProfileSettings(profile string) error {
	dir := filepath.Join(profile, "user")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "registrymodifications.xcu"), []byte(profileSettings), 0o600)
}
