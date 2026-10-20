package pdfops

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// specCase is one shared page-spec vector. The Python Studio has its own parser
// for the same grammar and runs against the same file, so the two cannot drift.
type specCase struct {
	Name      string `json:"name"`
	Spec      string `json:"spec"`
	PageCount int    `json:"page_count"`
	Want      []int  `json:"want"`
	Error     string `json:"error"`
}

func loadSpecCases(t *testing.T) []specCase {
	t.Helper()
	data, err := os.ReadFile("testdata/pagespec_cases.json")
	if err != nil {
		t.Fatalf("read shared page-spec cases: %v", err)
	}
	var cases []specCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatalf("parse shared page-spec cases: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("shared page-spec cases file is empty")
	}
	return cases
}

func TestParsePages(t *testing.T) {
	for _, tc := range loadSpecCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			got, err := ParsePages(tc.Spec, tc.PageCount)
			if tc.Error != "" {
				if err == nil {
					t.Fatalf("ParsePages(%q) = %v, want error containing %q", tc.Spec, got, tc.Error)
				}
				if !strings.Contains(err.Error(), tc.Error) {
					t.Fatalf("ParsePages(%q) error = %q, want it to contain %q", tc.Spec, err, tc.Error)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePages(%q) unexpected error: %v", tc.Spec, err)
			}
			if len(got) != len(tc.Want) {
				t.Fatalf("ParsePages(%q) = %v, want %v", tc.Spec, got, tc.Want)
			}
			for i := range got {
				if got[i] != tc.Want[i] {
					t.Fatalf("ParsePages(%q) = %v, want %v", tc.Spec, got, tc.Want)
				}
			}
		})
	}
}

// A spec is checked against pageCount 0 in one error case above, so parse
// against a real page count to be sure bounds checking really uses it.
func TestParsePagesUsesPageCount(t *testing.T) {
	if _, err := ParsePages("3", 3); err != nil {
		t.Fatalf("page 3 of 3 should parse: %v", err)
	}
	if _, err := ParsePages("4", 3); err == nil {
		t.Fatal("page 4 of 3 should not parse")
	}
	if _, err := ParsePages("z", 1); err != nil {
		t.Fatalf("z on a 1-page document: %v", err)
	}
	if _, err := ParsePages("2", 1); err == nil {
		t.Fatal("page 2 of 1 should not parse")
	}
}

func TestParsePagesRejectsExpansionBeyondLimit(t *testing.T) {
	// 1000 pages is allowed; anything past the bound is refused before the
	// slice is built.
	if _, err := ParsePages("1-1000", maxPages); err != nil {
		t.Fatalf("1-1000 should be allowed: %v", err)
	}
	if _, err := ParsePages("1-1001", maxPages); err == nil {
		t.Fatal("1-1001 should be refused")
	}
	if _, err := ParsePages("1-600,1-600", maxPages); err == nil {
		t.Fatal("duplicates must still count against the bound")
	}
}

func TestPagesSet(t *testing.T) {
	got := PagesSet([]int{3, 1, 3, 2, 1})
	want := []int{1, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("PagesSet = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("PagesSet = %v, want %v", got, want)
		}
	}
	if got := PagesSet(nil); len(got) != 0 {
		t.Fatalf("PagesSet(nil) = %v, want empty", got)
	}
}

func TestComplement(t *testing.T) {
	got := Complement([]int{2}, 4)
	want := []int{1, 3, 4}
	if len(got) != len(want) {
		t.Fatalf("Complement = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("Complement = %v, want %v", got, want)
		}
	}
	if got := Complement([]int{1, 2, 3}, 3); len(got) != 0 {
		t.Fatalf("Complement of everything = %v, want empty", got)
	}
	if got := Complement(nil, 2); len(got) != 2 {
		t.Fatalf("Complement(nil) = %v, want both pages", got)
	}
}

func TestFormat(t *testing.T) {
	tests := []struct {
		pages []int
		want  string
	}{
		{pages: nil, want: ""},
		{pages: []int{1}, want: "1"},
		{pages: []int{1, 2, 3}, want: "1-3"},
		{pages: []int{1, 2, 3, 7, 10, 11, 12}, want: "1-3,7,10-12"},
		{pages: []int{3, 5}, want: "3,5"},
		{pages: []int{1, 3}, want: "1,3"},
	}
	for _, tc := range tests {
		if got := Format(tc.pages); got != tc.want {
			t.Fatalf("Format(%v) = %q, want %q", tc.pages, got, tc.want)
		}
	}
}

// Format and ParsePages must agree, so the canonical form always round-trips.
func TestFormatRoundTrips(t *testing.T) {
	for _, pages := range [][]int{{1}, {1, 2, 3}, {1, 2, 3, 7}, {4, 5, 6, 9}} {
		count := 9
		spec := Format(pages)
		got, err := ParsePages(spec, count)
		if err != nil {
			t.Fatalf("ParsePages(Format(%v)=%q) failed: %v", pages, spec, err)
		}
		if len(got) != len(pages) {
			t.Fatalf("ParsePages(%q) = %v, want %v", spec, got, pages)
		}
		for i := range got {
			if got[i] != pages[i] {
				t.Fatalf("ParsePages(%q) = %v, want %v", spec, got, pages)
			}
		}
	}
}
