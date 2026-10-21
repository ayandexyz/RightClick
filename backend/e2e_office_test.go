package backend_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// officeFixtures lets LibreOffice write the documents the daemon will convert
// back, so every format is a real file made by a real producer. Names carry
// spaces and a non-ASCII letter on purpose.
func officeFixtures(t *testing.T, dir string) map[string]string {
	t.Helper()
	profile := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	convert := func(src, format string) (string, bool) {
		cmd := exec.Command("soffice", "--headless", "--norestore", "-env:UserInstallation=file://"+profile,
			"--convert-to", format, "--outdir", dir, src)
		if err := cmd.Run(); err != nil {
			return "", false
		}
		out := filepath.Join(dir, strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))+"."+format)
		_, err := os.Stat(out)
		return out, err == nil
	}
	text := write("mein Dokument é.txt", "Hello OFFICEWORD\n\nsecond paragraph\n")
	sheet := write("sheet 1.csv", "name,value\nOFFICEWORD,1\n")
	slides := write("deck.fodp", `<?xml version="1.0" encoding="UTF-8"?>
<office:document xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:draw="urn:oasis:names:tc:opendocument:xmlns:drawing:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" xmlns:svg="urn:oasis:names:tc:opendocument:xmlns:svg-compatible:1.0" office:version="1.3" office:mimetype="application/vnd.oasis.opendocument.presentation">
<office:body><office:presentation><draw:page draw:name="p1"><draw:frame svg:width="20cm" svg:height="5cm" svg:x="2cm" svg:y="2cm"><draw:text-box><text:p>OFFICEWORD slide</text:p></draw:text-box></draw:frame></draw:page></office:presentation></office:body></office:document>`)

	got := map[string]string{"txt": text, "csv": sheet}
	for ext, from := range map[string]string{"docx": text, "odt": text, "rtf": text, "xlsx": sheet, "ods": sheet, "pptx": slides, "odp": slides} {
		if path, ok := convert(from, ext); ok {
			got[ext] = path
		}
	}
	return got
}

func officeStream(t *testing.T) (*e2eStream, string) {
	t.Helper()
	requireTools(t, "soffice", "pdftotext")
	stream, dir, _ := studioFixture(t)
	return stream, dir
}

func TestDaemonEndToEndOfficeToPDF(t *testing.T) {
	stream, dir := officeStream(t)
	fixtures := officeFixtures(t, dir)
	for _, ext := range []string{"txt", "csv", "docx", "odt", "rtf", "xlsx", "ods", "pptx", "odp"} {
		path, ok := fixtures[ext]
		if !ok {
			t.Logf("no .%s fixture could be built here; not tested", ext)
			continue
		}
		t.Run(ext, func(t *testing.T) {
			out := runJob(t, stream, map[string]any{"op": "office-to-pdf", "files": []string{path}, "output_dir": dir})
			if !strings.HasSuffix(out, ".pdf") || filepath.Dir(out) != dir {
				t.Fatalf("output = %q", out)
			}
			if text := strings.Join(pageTexts(t, out), " "); !strings.Contains(text, "OFFICEWORD") {
				t.Fatalf("converted text = %q, want it to hold the document's word", text)
			}
		})
	}
	if len(fixtures) < 5 {
		t.Fatalf("only %d fixtures were built; LibreOffice looks broken here", len(fixtures))
	}
}

func TestDaemonEndToEndOfficeToPDFReportsACorruptFile(t *testing.T) {
	stream, dir := officeStream(t)
	bad := filepath.Join(dir, "broken.docx")
	if err := os.WriteFile(bad, []byte("this is not a zip archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	files := runJobForFiles(t, stream, map[string]any{"op": "office-to-pdf", "files": []string{bad}, "output_dir": dir})
	if len(files) != 1 || files[0]["state"] != "failed" {
		t.Fatalf("result = %#v, want one failed file", files)
	}
	if outputs, _ := files[0]["outputs"].([]any); len(outputs) != 0 {
		t.Fatalf("a failed conversion left outputs: %#v", outputs)
	}
	if _, err := os.Stat(filepath.Join(dir, "broken.pdf")); err == nil {
		t.Fatal("a failed conversion left a PDF behind")
	}
}

// Cancelling mid-conversion must stop LibreOffice itself, not only the wrapper
// the daemon started, and remove the throwaway profile.
func TestDaemonEndToEndOfficeToPDFCancelLeavesNoProcessBehind(t *testing.T) {
	stream, dir := officeStream(t)
	doc := officeFixtures(t, dir)["docx"]
	if doc == "" {
		t.Skip("no docx fixture")
	}
	ack := request(t, stream, map[string]any{"id": 90, "method": "run_operation", "params": map[string]any{"op": "office-to-pdf", "files": []string{doc}, "output_dir": dir}})
	job, _ := ack["result"].(map[string]any)["job"].(string)
	if job == "" {
		t.Fatalf("no job: %#v", ack)
	}
	// Wait until LibreOffice is running on a daemon profile, then cancel.
	if !eventually(5*time.Second, func() bool { return len(daemonOffice(t)) > 0 }) {
		t.Skip("conversion finished before it could be cancelled")
	}
	if c := request(t, stream, map[string]any{"id": 91, "method": "cancel", "params": map[string]any{"job": job}}); c["error"] != nil {
		t.Fatalf("cancel: %#v", c)
	}
	if !eventually(10*time.Second, func() bool { return len(daemonOffice(t)) == 0 }) {
		t.Fatalf("LibreOffice processes survive the cancel: %v", daemonOffice(t))
	}
	leftovers, _ := filepath.Glob(filepath.Join(os.TempDir(), "file-converter-lo-*"))
	if !eventually(5*time.Second, func() bool {
		leftovers, _ = filepath.Glob(filepath.Join(os.TempDir(), "file-converter-lo-*"))
		return len(leftovers) == 0
	}) {
		t.Fatalf("temporary profiles survive the cancel: %v", leftovers)
	}
}

func eventually(limit time.Duration, ok func() bool) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if ok() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return ok()
}

// daemonOffice lists LibreOffice processes started on one of the daemon's
// private profiles, which are the only ones this test should see.
func daemonOffice(t *testing.T) []string {
	t.Helper()
	out, _ := exec.Command("pgrep", "-af", "file-converter-lo-").Output()
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" && !strings.Contains(line, "pgrep") {
			lines = append(lines, line)
		}
	}
	return lines
}
