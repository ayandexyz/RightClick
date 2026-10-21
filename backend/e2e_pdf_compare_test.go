package backend_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Two different documents through a real daemon: the report is written next to
// the inputs, names the page that changed, and a document with no text is
// refused with a message that says why.
func TestDaemonEndToEndCompare(t *testing.T) {
	stream, dir, doc := studioFixture(t)

	// The same four pages with page 3 replaced.
	changed := runJob(t, stream, map[string]any{"op": "pdf-edit", "files": []string{doc}, "output_dir": dir,
		"params": map[string]any{"edit": []map[string]any{{"src": 1}, {"src": 2}, {"src": 0}, {"src": 4}}}})
	report := runJob(t, stream, map[string]any{"op": "pdf-compare", "files": []string{doc, changed}, "output_dir": dir})
	if filepath.Dir(report) != dir || !strings.HasSuffix(report, ".txt") || !strings.Contains(filepath.Base(report), "-compare-") {
		t.Fatalf("report path = %q", report)
	}
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "page 3 differs") || !strings.Contains(text, "-PAGE3") || strings.Contains(text, "page 1") || strings.Contains(text, "page 4") {
		t.Fatalf("report = %q, want only page 3 reported", text)
	}

	// A document with no text at all cannot be compared by its text.
	blank := filepath.Join(dir, "blank.pdf")
	if err := os.WriteFile(blank, pdfWithText(""), 0o600); err != nil {
		t.Fatal(err)
	}
	files := runJobForFiles(t, stream, map[string]any{"op": "pdf-compare", "files": []string{doc, blank}, "output_dir": dir})
	if len(files) != 2 {
		t.Fatalf("result = %#v, want one entry per input", files)
	}
	for _, f := range files {
		if f["state"] != "skipped" || !strings.Contains(asString(f["notice"]), "no text") {
			t.Errorf("result %#v, want skipped with a notice that there is no text to compare", f)
		}
		if outputs, _ := f["outputs"].([]any); len(outputs) != 0 {
			t.Errorf("a refused comparison wrote %v", outputs)
		}
	}
}

func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case map[string]any:
		s, _ := x["message"].(string)
		return s
	}
	return ""
}
