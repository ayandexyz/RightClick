package pdfops

import (
	"strings"
	"testing"
)

func TestParsePages(t *testing.T) {
	tests := []struct {
		name      string
		spec      string
		pageCount int
		want      []int
		wantErr   string
	}{
		{name: "single page", spec: "1", pageCount: 3, want: []int{1}},
		{name: "closed range", spec: "1-3", pageCount: 5, want: []int{1, 2, 3}},
		{name: "open end", spec: "2-", pageCount: 4, want: []int{2, 3, 4}},
		{name: "open start", spec: "-2", pageCount: 4, want: []int{1, 2}},
		{name: "open start to end", spec: "-", pageCount: 3, want: []int{1, 2, 3}},
		{name: "last page", spec: "z", pageCount: 4, want: []int{4}},
		{name: "z is one page", spec: "z", pageCount: 1, want: []int{1}},
		{name: "mixed", spec: "1-3,7,10-", pageCount: 12, want: []int{1, 2, 3, 7, 10, 11, 12}},
		{name: "uppercase z", spec: "Z", pageCount: 4, want: []int{4}},
		{name: "whitespace ignored", spec: " 1 - 3 , 7 ", pageCount: 8, want: []int{1, 2, 3, 7}},
		{name: "overlap preserved", spec: "1-3,2-4", pageCount: 6, want: []int{1, 2, 3, 2, 3, 4}},
		{name: "duplicates preserved", spec: "2,2,2", pageCount: 4, want: []int{2, 2, 2}},
		{name: "order preserved", spec: "5,1,3", pageCount: 6, want: []int{5, 1, 3}},
		{name: "last page of range", spec: "2-z", pageCount: 5, want: []int{2, 3, 4, 5}},

		{name: "zero", spec: "0", pageCount: 3, wantErr: "0"},
		{name: "zero in range", spec: "0-2", pageCount: 3, wantErr: "0"},
		{name: "reversed range", spec: "5-3", pageCount: 8, wantErr: "5-3"},
		{name: "garbage", spec: "abc", pageCount: 3, wantErr: "abc"},
		{name: "bare comma", spec: ",", pageCount: 3, wantErr: "empty"},
		{name: "double comma", spec: "1,,2", pageCount: 3, wantErr: "empty"},
		{name: "trailing comma", spec: "1,", pageCount: 3, wantErr: "empty"},
		{name: "out of range", spec: "9", pageCount: 3, wantErr: "9"},
		{name: "out of range in range", spec: "3-9", pageCount: 5, wantErr: "9"},
		{name: "empty spec", spec: "", pageCount: 3, wantErr: "empty"},
		{name: "whitespace only", spec: "   ", pageCount: 3, wantErr: "empty"},
		{name: "multiple hyphens", spec: "1-2-3", pageCount: 5, wantErr: "1-2-3"},
		{name: "trailing hyphen number", spec: "1-2x", pageCount: 5, wantErr: "1-2x"},
		{name: "too many pages", spec: "1-2000", pageCount: 0, wantErr: "1000"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParsePages(tc.spec, tc.pageCount)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("ParsePages(%q) = %v, want error containing %q", tc.spec, got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ParsePages(%q) error = %q, want it to contain %q", tc.spec, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePages(%q) unexpected error: %v", tc.spec, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("ParsePages(%q) = %v, want %v", tc.spec, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("ParsePages(%q) = %v, want %v", tc.spec, got, tc.want)
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
