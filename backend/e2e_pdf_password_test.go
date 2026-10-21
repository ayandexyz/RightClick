package backend_test

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A password-protected document is opened, thumbnailed and edited through a
// real daemon. The password is a recognisable sentinel so the test can search
// for it in everything the daemon wrote or reported.
const passwordSentinel = "Sentinel-Passw0rd-7431"

func TestDaemonEndToEndLockedDocument(t *testing.T) {
	requireTools(t, "pdftotext", "pdftocairo", "qpdf")
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)
	_, socket, stop := startDaemonForE2E(t)
	t.Cleanup(stop)
	runtimeDir := filepath.Dir(filepath.Dir(socket))

	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.pdf")
	if err := os.WriteFile(plain, pdfWithText("SECRETPAGE"), 0o600); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(dir, "locked.pdf")
	if out, err := exec.Command("qpdf", "--encrypt", passwordSentinel, "owner-"+passwordSentinel, "256", "--", plain, locked).CombinedOutput(); err != nil {
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 3 {
			t.Fatalf("qpdf --encrypt: %v\n%s", err, out)
		}
	}

	conn, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	connClosed := false
	t.Cleanup(func() {
		if !connClosed {
			conn.Close()
		}
	})
	stream := newE2EStream(conn)
	if hello := request(t, stream, map[string]any{"id": 1, "method": "hello", "params": map[string]any{"client": "e2e", "protocol": 1}}); hello["error"] != nil {
		t.Fatalf("hello: %#v", hello)
	}
	code := func(resp wireMessage) string {
		errValue, _ := resp["error"].(map[string]any)
		c, _ := errValue["code"].(string)
		return c
	}

	if got := code(request(t, stream, map[string]any{"id": 2, "method": "pdf_info", "params": map[string]any{"file": locked}})); got != "password_required" {
		t.Fatalf("no password = %q, want password_required", got)
	}
	if got := code(request(t, stream, map[string]any{"id": 3, "method": "pdf_info", "params": map[string]any{"file": locked, "password": "wrong"}})); got != "password_incorrect" {
		t.Fatalf("wrong password = %q, want password_incorrect", got)
	}
	info := request(t, stream, map[string]any{"id": 4, "method": "pdf_info", "params": map[string]any{"file": locked, "password": passwordSentinel}})
	if info["error"] != nil || info["result"].(map[string]any)["pages"] != float64(1) {
		t.Fatalf("pdf_info with the password = %#v", info)
	}

	// Thumbnails of the locked document render, and land in this connection's
	// private directory, not the persistent cache.
	resp := request(t, stream, map[string]any{"id": 5, "method": "pdf_thumbnails", "params": map[string]any{"file": locked, "pages": []int{1}, "width": 120, "password": passwordSentinel}})
	if resp["error"] != nil {
		t.Fatalf("pdf_thumbnails with the password = %#v", resp)
	}
	thumb := resp["result"].(map[string]any)["pages"].([]any)[0].(map[string]any)["path"].(string)
	if !strings.HasPrefix(thumb, filepath.Join(runtimeDir, "file-converter", "locked-")) {
		t.Fatalf("locked thumbnail at %s, want it under the connection's private directory", thumb)
	}
	if st, err := os.Stat(thumb); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("thumbnail stat = %v, %v, want a 0600 file", st, err)
	}
	privateDir := filepath.Dir(filepath.Dir(filepath.Dir(thumb)))
	if st, err := os.Stat(privateDir); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("private directory %s = %v, %v, want 0700", privateDir, st, err)
	}
	_ = filepath.WalkDir(cacheDir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".png") {
			t.Errorf("a locked document's thumbnail reached the persistent cache: %s", path)
		}
		return nil
	})

	// An operation with the password writes a copy that needs none.
	rows := runJobForFiles(t, stream, map[string]any{"op": "pdf-extract", "files": []string{locked}, "output_dir": dir, "params": map[string]any{"pages": "1", "password": passwordSentinel}})
	row := finishedFile(t, rows)
	outputs := outputsOf(row)
	if len(outputs) != 1 {
		t.Fatalf("extract from a locked file = %#v", row)
	}
	if got := pageTexts(t, outputs[0]); len(got) != 1 || got[0] != "SECRETPAGE" {
		t.Fatalf("extracted text = %q", got)
	}
	reported, _ := json.Marshal(rows)
	if bytes.Contains(reported, []byte(passwordSentinel)) {
		t.Fatalf("the password appears in the job result: %s", reported)
	}

	// The password is in no file the daemon wrote.
	for _, root := range []string{runtimeDir, cacheDir} {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || d.Type()&fs.ModeSocket != 0 {
				return nil
			}
			if data, err := os.ReadFile(path); err == nil && bytes.Contains(data, []byte(passwordSentinel)) {
				t.Errorf("the password was written to %s", path)
			}
			return nil
		})
	}

	// Closing the connection removes the unlocked copy and its thumbnails.
	conn.Close()
	connClosed = true
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(privateDir); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s still exists after the connection closed", privateDir)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
