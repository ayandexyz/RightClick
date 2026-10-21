package backend_test

import (
	"os/exec"
	"strings"
	"testing"
)

// The phase 4 tools through a real daemon: the request is the typed params a
// client sends, and the result is read back with pdftotext and pdfinfo.
func TestDaemonEndToEndPDFTools(t *testing.T) {
	stream, dir, doc := studioFixture(t)

	run := func(op string, params map[string]any) string {
		t.Helper()
		return runJob(t, stream, map[string]any{"op": op, "files": []string{doc}, "output_dir": dir, "params": params})
	}

	t.Run("page numbers", func(t *testing.T) {
		out := run("pdf-page-numbers", map[string]any{"stamp": map[string]any{"format": "of-total", "start": 10, "pages": "2-3"}})
		pages := pageTexts(t, out)
		for i, want := range map[int]string{1: "11 of 4", 2: "12 of 4"} {
			if !strings.Contains(strings.ReplaceAll(pages[i], "\n", " "), want) {
				t.Fatalf("page %d = %q, want %q", i+1, pages[i], want)
			}
		}
		if strings.Contains(pages[0], " of ") || strings.Contains(pages[3], " of ") {
			t.Fatalf("pages outside the selection were numbered: %q", pages)
		}
	})

	t.Run("bates", func(t *testing.T) {
		out := run("pdf-bates", map[string]any{"stamp": map[string]any{"prefix": "CASE-", "start": 99}})
		pages := pageTexts(t, out)
		for i, want := range []string{"CASE-000099", "CASE-000100", "CASE-000101", "CASE-000102"} {
			if !strings.Contains(strings.ReplaceAll(pages[i], " ", ""), want) {
				t.Fatalf("page %d = %q, want %q", i+1, pages[i], want)
			}
		}
	})

	t.Run("header and footer", func(t *testing.T) {
		out := run("pdf-header-footer", map[string]any{"stamp": map[string]any{"left": "Draft", "right": "Page %p", "edge": "bottom"}})
		page := strings.ReplaceAll(pageTexts(t, out)[1], " ", "")
		if !strings.Contains(page, "Draft") || !strings.Contains(page, "Page2") {
			t.Fatalf("page 2 = %q", page)
		}
	})

	t.Run("crop and resize", func(t *testing.T) {
		cropped := run("pdf-crop", map[string]any{"crop": map[string]any{"unit": "pt", "top": 20, "bottom": 20}})
		info, err := exec.Command("pdfinfo", cropped).Output()
		if err != nil || !strings.Contains(string(info), "200 x 160 pts") {
			t.Fatalf("cropped size: %v\n%s", err, info)
		}
		resized := run("pdf-resize", map[string]any{"resize": map[string]any{"to": "A4"}})
		info, err = exec.Command("pdfinfo", resized).Output()
		if err != nil || !strings.Contains(string(info), "595 x 842 pts") {
			t.Fatalf("resized size: %v\n%s", err, info)
		}
	})

	t.Run("a refused stamp writes nothing and says why", func(t *testing.T) {
		rows := runJobForFiles(t, stream, map[string]any{"op": "pdf-stamp-text", "files": []string{doc}, "output_dir": dir, "params": map[string]any{"stamp": map[string]any{"text": "আমি"}}})
		row := finishedFile(t, rows)
		if len(outputsOf(row)) != 0 || errorCodeOf(row) == "" {
			t.Fatalf("a non-Latin stamp = %#v, want a failure and no output", row)
		}
	})
}
