// Package pdfops implements structural PDF operations: page selection,
// merging, deleting, rotating and reversing. It owns the page-spec grammar and
// the catalogue of operations; the actual PDF work is delegated to a Backend.
package pdfops

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// maxPages bounds how many pages a single document may have and how many a
// spec may expand to. It matches the daemon's existing PDF page ceiling.
const maxPages = 1000

// ParsePages turns a page spec into the pages it names, 1-based and in the
// order written. Duplicates and overlaps are kept, because Select-style
// operations need them; use PagesSet to collapse them.
//
// The grammar is comma-separated items, each being N, N-M, N-, -M, or z (the
// last page). Whitespace is ignored. pageCount bounds every page number and
// may be 0 to skip the bound while still enforcing the size limit.
func ParsePages(spec string, pageCount int) ([]int, error) {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" {
		return nil, fmt.Errorf("page spec is empty")
	}
	var pages []int
	for _, item := range strings.Split(trimmed, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			return nil, fmt.Errorf("empty page range in spec %q", spec)
		}
		expanded, err := expandItem(item, pageCount, spec)
		if err != nil {
			return nil, err
		}
		if len(pages)+len(expanded) > maxPages {
			return nil, fmt.Errorf("page spec %q selects more than the maximum of %d pages", spec, maxPages)
		}
		pages = append(pages, expanded...)
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("page spec %q selects no pages", spec)
	}
	return pages, nil
}

// expandItem resolves one comma-separated item to the pages it names.
func expandItem(item string, pageCount int, spec string) ([]int, error) {
	invalid := func(reason string) error {
		return fmt.Errorf("invalid page range %q in spec %q: %s", item, spec, reason)
	}
	if strings.EqualFold(item, "z") {
		if pageCount < 1 {
			return nil, invalid("z needs a known page count")
		}
		return []int{pageCount}, nil
	}
	if strings.Count(item, "-") > 1 {
		return nil, invalid("too many hyphens")
	}
	from, to, hasRange := strings.Cut(item, "-")
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	// An open start means "from page 1"; an open end means "to the last page".
	if hasRange && from == "" {
		from = "1"
	}
	lower, err := parseBound(from, item, pageCount, spec)
	if err != nil {
		return nil, err
	}
	if !hasRange {
		return []int{lower}, nil
	}
	if to == "" {
		return rangeTo(lower, pageCount), nil
	}
	upper, err := parseBound(to, item, pageCount, spec)
	if err != nil {
		return nil, err
	}
	if upper < lower {
		return nil, invalid("range runs backwards")
	}
	return rangeTo(lower, upper), nil
}

// rangeTo returns lower..upper inclusive, or nothing when the document is
// shorter than the range's start (an open range past the end is empty, not an
// error, but ParsePages rejects an empty overall selection).
func rangeTo(lower, upper int) []int {
	if upper < lower {
		return nil
	}
	pages := make([]int, 0, upper-lower+1)
	for page := lower; page <= upper; page++ {
		pages = append(pages, page)
	}
	return pages
}

// parseBound reads one end of a range. An empty end yields 1 (for "-M") or
// pageCount (for "N-"); z is the last page. Every result is checked against
// pageCount and against page 1.
func parseBound(text, item string, pageCount int, spec string) (int, error) {
	invalid := func(reason string) error {
		return fmt.Errorf("invalid page range %q in spec %q: %s", item, spec, reason)
	}
	if text == "" {
		return 0, invalid("missing page number")
	}
	if strings.EqualFold(text, "z") {
		if pageCount < 1 {
			return 0, invalid("z needs a known page count")
		}
		return pageCount, nil
	}
	if !allDigits(text) {
		return 0, invalid("page number must be a positive integer")
	}
	number, err := strconv.Atoi(text)
	if err != nil {
		return 0, invalid("page number is not a number")
	}
	if number < 1 {
		return 0, invalid("page numbers start at 1")
	}
	if pageCount > 0 && number > pageCount {
		return 0, invalid(fmt.Sprintf("page %d is past the last page (%d)", number, pageCount))
	}
	return number, nil
}

func allDigits(text string) bool {
	if text == "" {
		return false
	}
	for _, char := range text {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

// PagesSet returns pages sorted and de-duplicated.
func PagesSet(pages []int) []int {
	if len(pages) == 0 {
		return nil
	}
	set := append([]int(nil), pages...)
	sort.Ints(set)
	out := set[:0]
	for i, page := range set {
		if i == 0 || page != set[i-1] {
			out = append(out, page)
		}
	}
	return out
}

// Complement returns every page of a pageCount-page document that pages does
// not name, sorted ascending.
func Complement(pages []int, pageCount int) []int {
	removed := make(map[int]struct{}, len(pages))
	for _, page := range pages {
		removed[page] = struct{}{}
	}
	out := make([]int, 0, pageCount)
	for page := 1; page <= pageCount; page++ {
		if _, ok := removed[page]; !ok {
			out = append(out, page)
		}
	}
	return out
}

// Format collapses pages back into the canonical spec form, collapsing runs of
// three or more consecutive pages into a range.
func Format(pages []int) string {
	sorted := PagesSet(pages)
	var parts []string
	for i := 0; i < len(sorted); {
		start := sorted[i]
		end := start
		for i+1 < len(sorted) && sorted[i+1] == end+1 {
			i++
			end = sorted[i]
		}
		// A one- or two-page run reads better written out than as "1-2".
		if end-start >= 2 {
			parts = append(parts, strconv.Itoa(start)+"-"+strconv.Itoa(end))
		} else {
			for page := start; page <= end; page++ {
				parts = append(parts, strconv.Itoa(page))
			}
		}
		i++
	}
	return strings.Join(parts, ",")
}
