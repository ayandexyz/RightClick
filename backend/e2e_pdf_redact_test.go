package backend_test

import (
	"strings"
	"testing"
)

// Search and redaction through a real daemon: pdf_find reports where a term
// is, and redacting by that term removes the page's text and nothing else.
func TestDaemonEndToEndFindAndRedact(t *testing.T) {
	stream, dir, doc := studioFixture(t)

	found := request(t, stream, map[string]any{
		"id": 70, "method": "pdf_find", "params": map[string]any{"file": doc, "terms": []string{"page3"}},
	})
	if found["error"] != nil {
		t.Fatalf("pdf_find: %#v", found)
	}
	matches, _ := found["result"].(map[string]any)["matches"].([]any)
	if len(matches) != 1 {
		t.Fatalf("matches = %#v, want one", matches)
	}
	match := matches[0].(map[string]any)
	if match["page"] != float64(3) || match["term"] != "page3" || match["w"].(float64) <= 0 || match["h"].(float64) <= 0 {
		t.Fatalf("match = %#v, want a box on page 3 with lower-case json keys", match)
	}

	out := runJob(t, stream, map[string]any{
		"op": "pdf-redact", "files": []string{doc}, "output_dir": dir,
		"params": map[string]any{"redact": map[string]any{"terms": []string{"PAGE3"}}},
	})
	pages := pageTexts(t, out)
	if len(pages) != 4 {
		t.Fatalf("redacted file has %d pages, want 4", len(pages))
	}
	if pages[2] != "" {
		t.Errorf("page 3 still has text %q", pages[2])
	}
	for _, i := range []int{0, 1, 3} {
		if !strings.Contains(pages[i], "PAGE") {
			t.Errorf("page %d lost its text: %q", i+1, pages[i])
		}
	}
}
