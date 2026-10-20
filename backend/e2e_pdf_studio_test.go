package backend_test

import (
	"image/png"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These tests drive the PDF Studio surface of a real daemon over its socket:
// pdf_info, pdf_thumbnails and the cache, pdf-edit, both splits, and merge with
// per-file ranges. Page order is checked through the text of each page, because
// a page count alone would not notice a reordering bug. Anything that needs a
// tool the machine lacks skips instead of failing.

// pageTexts returns the text of each page of a PDF, in order. A page with no
// text (a blank) is the empty string.
func pageTexts(t *testing.T, path string) []string {
	t.Helper()
	out, err := exec.Command("pdftotext", "-layout", path, "-").Output()
	if err != nil {
		t.Fatalf("pdftotext %s: %v", path, err)
	}
	pages := strings.Split(string(out), "\f")
	// pdftotext ends the output with a form feed, leaving one empty tail.
	pages = pages[:len(pages)-1]
	for i := range pages {
		pages[i] = strings.TrimSpace(pages[i])
	}
	return pages
}

var rotationLine = regexp.MustCompile(`Page\s+(\d+)\s+rot:\s+(\d+)`)

// pageRotations returns the /Rotate of each page, as pdfinfo reports it.
func pageRotations(t *testing.T, path string, pages int) []int {
	t.Helper()
	out, err := exec.Command("pdfinfo", "-f", "1", "-l", strconv.Itoa(pages), path).Output()
	if err != nil {
		t.Fatalf("pdfinfo %s: %v", path, err)
	}
	rotations := make([]int, pages)
	for _, match := range rotationLine.FindAllStringSubmatch(string(out), -1) {
		page, _ := strconv.Atoi(match[1])
		rotation, _ := strconv.Atoi(match[2])
		rotations[page-1] = rotation
	}
	return rotations
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func requireTools(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s is not installed", name)
		}
	}
}

// finishedFile returns the single file row of a finished job, failing the test
// if the job produced any other number.
func finishedFile(t *testing.T, rows []map[string]any) map[string]any {
	t.Helper()
	if len(rows) != 1 {
		t.Fatalf("job has %d files, want 1: %#v", len(rows), rows)
	}
	return rows[0]
}

func outputsOf(row map[string]any) []string {
	raw, _ := row["outputs"].([]any)
	outputs := make([]string, len(raw))
	for i, entry := range raw {
		outputs[i], _ = entry.(string)
	}
	return outputs
}

func errorCodeOf(row map[string]any) string {
	errValue, _ := row["error"].(map[string]any)
	code, _ := errValue["code"].(string)
	return code
}

// studioFixture starts a daemon and builds a four-page document whose pages read
// PAGE1 .. PAGE4.
func studioFixture(t *testing.T) (stream *e2eStream, dir, doc string) {
	t.Helper()
	requireTools(t, "pdftotext", "pdfinfo")
	_, socket, stop := startDaemonForE2E(t)
	t.Cleanup(stop)
	conn, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	stream = newE2EStream(conn)
	if hello := request(t, stream, map[string]any{
		"id": 1, "method": "hello", "params": map[string]any{"client": "e2e", "protocol": 1},
	}); hello["error"] != nil {
		t.Fatalf("hello: %#v", hello)
	}

	dir = t.TempDir()
	var pages []string
	for i := 1; i <= 4; i++ {
		page := filepath.Join(dir, "src"+strconv.Itoa(i)+".pdf")
		if err := os.WriteFile(page, pdfWithText("PAGE"+strconv.Itoa(i)), 0o600); err != nil {
			t.Fatal(err)
		}
		pages = append(pages, page)
	}
	built := runJob(t, stream, map[string]any{"op": "pdf-merge", "files": pages, "output_dir": dir})
	doc = filepath.Join(dir, "doc.pdf")
	if err := os.Rename(built, doc); err != nil {
		t.Fatal(err)
	}
	if got := pageTexts(t, doc); !equalStrings(got, []string{"PAGE1", "PAGE2", "PAGE3", "PAGE4"}) {
		t.Fatalf("fixture pages = %q", got)
	}
	return stream, dir, doc
}

func TestDaemonEndToEndPDFInfoAndErrors(t *testing.T) {
	stream, dir, doc := studioFixture(t)

	t.Run("info reports pages and sizes", func(t *testing.T) {
		resp := request(t, stream, map[string]any{"id": 10, "method": "pdf_info", "params": map[string]any{"file": doc}})
		if resp["error"] != nil {
			t.Fatalf("pdf_info: %#v", resp)
		}
		result := resp["result"].(map[string]any)
		if result["pages"] != float64(4) {
			t.Fatalf("pages = %v, want 4", result["pages"])
		}
		sizes, _ := result["sizes"].([]any)
		if len(sizes) != 4 {
			t.Fatalf("sizes = %v, want 4 entries", sizes)
		}
		first := sizes[0].(map[string]any)
		if first["w"] != float64(200) || first["h"] != float64(200) {
			t.Fatalf("first page size = %v, want 200x200", first)
		}
	})

	t.Run("a damaged file is invalid_pdf for info and thumbnails", func(t *testing.T) {
		damaged := filepath.Join(dir, "damaged.pdf")
		if err := os.WriteFile(damaged, []byte("%PDF-1.7\ngarbage that is not a document\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, method := range []struct {
			name   string
			params map[string]any
		}{
			{"pdf_info", map[string]any{"file": damaged}},
			{"pdf_thumbnails", map[string]any{"file": damaged, "pages": []int{1}}},
		} {
			resp := request(t, stream, map[string]any{"id": 11, "method": method.name, "params": method.params})
			errValue, _ := resp["error"].(map[string]any)
			if errValue["code"] != "invalid_pdf" {
				t.Fatalf("%s on a damaged file = %#v, want invalid_pdf", method.name, resp)
			}
		}
	})

	t.Run("a locked file is password_required for info and thumbnails", func(t *testing.T) {
		requireTools(t, "qpdf")
		locked := filepath.Join(dir, "locked.pdf")
		// qpdf exits 3 for "succeeded with warnings", which is fine here.
		if out, err := exec.Command("qpdf", "--encrypt", "user", "owner", "256", "--", doc, locked).CombinedOutput(); err != nil {
			if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 3 {
				t.Fatalf("qpdf --encrypt: %v\n%s", err, out)
			}
		}
		for _, method := range []struct {
			name   string
			params map[string]any
		}{
			{"pdf_info", map[string]any{"file": locked}},
			{"pdf_thumbnails", map[string]any{"file": locked, "pages": []int{1}}},
		} {
			resp := request(t, stream, map[string]any{"id": 12, "method": method.name, "params": method.params})
			errValue, _ := resp["error"].(map[string]any)
			if errValue["code"] != "password_required" {
				t.Fatalf("%s on a locked file = %#v, want password_required", method.name, resp)
			}
		}
	})
}

func TestDaemonEndToEndPDFThumbnailsAndCache(t *testing.T) {
	requireTools(t, "pdftocairo")
	stream, _, doc := studioFixture(t)

	thumbs := func(id int, pages []int, width int) []map[string]any {
		resp := request(t, stream, map[string]any{"id": id, "method": "pdf_thumbnails", "params": map[string]any{"file": doc, "pages": pages, "width": width}})
		if resp["error"] != nil {
			t.Fatalf("pdf_thumbnails: %#v", resp)
		}
		raw := resp["result"].(map[string]any)["pages"].([]any)
		out := make([]map[string]any, len(raw))
		for i, entry := range raw {
			out[i] = entry.(map[string]any)
		}
		return out
	}

	first := thumbs(20, []int{1, 2}, 120)
	if len(first) != 2 || first[0]["page"] != float64(1) || first[1]["page"] != float64(2) {
		t.Fatalf("thumbnails = %#v, want pages 1 and 2 in order", first)
	}

	times := map[string]time.Time{}
	for _, entry := range first {
		path := entry["path"].(string)
		file, err := os.Open(path)
		if err != nil {
			t.Fatalf("thumbnail missing: %v", err)
		}
		config, err := png.DecodeConfig(file)
		file.Close()
		if err != nil {
			t.Fatalf("thumbnail %s is not a PNG: %v", path, err)
		}
		if config.Width != 120 && config.Height != 120 {
			t.Fatalf("thumbnail is %dx%d, want the longest side to be 120", config.Width, config.Height)
		}
		if float64(config.Width) != entry["w"] || float64(config.Height) != entry["h"] {
			t.Fatalf("reported size %vx%v differs from the file's %dx%d", entry["w"], entry["h"], config.Width, config.Height)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("thumbnail mode = %o, want 600", info.Mode().Perm())
		}
		if dirInfo, err := os.Stat(filepath.Dir(path)); err != nil || dirInfo.Mode().Perm() != 0o700 {
			t.Fatalf("cache directory mode = %v (%v), want 700", dirInfo.Mode().Perm(), err)
		}
		times[path] = info.ModTime()
	}

	// The second request must come from the cache: same paths, files untouched.
	second := thumbs(21, []int{1, 2}, 120)
	for i, entry := range second {
		path := entry["path"].(string)
		if path != first[i]["path"] {
			t.Fatalf("cache miss: %s then %s", first[i]["path"], path)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(times[path]) {
			t.Fatalf("%s was rewritten on the second request", path)
		}
	}

	// A different width is a different cache entry.
	if wide := thumbs(22, []int{1}, 200); wide[0]["path"] == first[0]["path"] {
		t.Fatalf("a different width reused the same file: %v", wide[0]["path"])
	}

	t.Run("bounds are enforced", func(t *testing.T) {
		for name, params := range map[string]map[string]any{
			"width too small": {"file": doc, "pages": []int{1}, "width": 10},
			"width too large": {"file": doc, "pages": []int{1}, "width": 5000},
			"page past end":   {"file": doc, "pages": []int{9}, "width": 120},
		} {
			resp := request(t, stream, map[string]any{"id": 23, "method": "pdf_thumbnails", "params": params})
			errValue, _ := resp["error"].(map[string]any)
			if errValue["code"] != "bad_request" {
				t.Errorf("%s = %#v, want bad_request", name, resp)
			}
		}
	})

	t.Run("cache clear removes the files", func(t *testing.T) {
		resp := request(t, stream, map[string]any{"id": 24, "method": "cache_clear", "params": map[string]any{}})
		if resp["error"] != nil {
			t.Fatalf("cache_clear: %#v", resp)
		}
		if removed, _ := resp["result"].(map[string]any)["removed"].(float64); removed < 3 {
			t.Fatalf("removed = %v, want at least the 3 thumbnails made above", removed)
		}
		if _, err := os.Stat(first[0]["path"].(string)); !os.IsNotExist(err) {
			t.Fatalf("thumbnail still exists after cache_clear: %v", err)
		}
		// And it renders again afterwards.
		if again := thumbs(25, []int{1}, 120); len(again) != 1 {
			t.Fatalf("no thumbnail after clearing: %#v", again)
		}
	})
}

func TestDaemonEndToEndPDFEdit(t *testing.T) {
	stream, dir, doc := studioFixture(t)

	edit := func(items ...map[string]any) []map[string]any {
		return runJobForFiles(t, stream, map[string]any{
			"op": "pdf-edit", "files": []string{doc}, "output_dir": dir,
			"params": map[string]any{"edit": items},
		})
	}
	page := func(src, rotate int) map[string]any { return map[string]any{"src": src, "rotate": rotate} }

	t.Run("reorder, duplicate, rotate and insert a blank", func(t *testing.T) {
		row := finishedFile(t, edit(page(3, 0), page(1, 90), page(0, 0), page(3, 0)))
		if row["state"] != "done" {
			t.Fatalf("edit = %#v", row)
		}
		output := outputsOf(row)[0]
		if got, want := pageTexts(t, output), []string{"PAGE3", "PAGE1", "", "PAGE3"}; !equalStrings(got, want) {
			t.Fatalf("pages = %q, want %q", got, want)
		}
		rotations := pageRotations(t, output, 4)
		if want := []int{0, 90, 0, 0}; rotations[0] != want[0] || rotations[1] != want[1] || rotations[2] != want[2] || rotations[3] != want[3] {
			t.Fatalf("rotations = %v, want %v: only the one rotated copy turns", rotations, want)
		}
	})

	t.Run("deleting pages is just leaving them out", func(t *testing.T) {
		row := finishedFile(t, edit(page(1, 0), page(4, 0)))
		if got, want := pageTexts(t, outputsOf(row)[0]), []string{"PAGE1", "PAGE4"}; !equalStrings(got, want) {
			t.Fatalf("pages = %q, want %q", got, want)
		}
	})

	t.Run("rotation is relative to the page's current rotation", func(t *testing.T) {
		rotated := runJob(t, stream, map[string]any{"op": "pdf-rotate-cw", "files": []string{doc}, "output_dir": dir})
		row := finishedFile(t, runJobForFiles(t, stream, map[string]any{
			"op": "pdf-edit", "files": []string{rotated}, "output_dir": dir,
			"params": map[string]any{"edit": []map[string]any{page(1, 90)}},
		}))
		if got := pageRotations(t, outputsOf(row)[0], 1)[0]; got != 180 {
			t.Fatalf("rotation = %d, want 90 + 90 = 180", got)
		}
	})

	t.Run("bad edits fail the file and write nothing", func(t *testing.T) {
		before, _ := filepath.Glob(filepath.Join(dir, "*-edited*.pdf"))
		for name, items := range map[string][]map[string]any{
			"only blank pages":     {page(0, 0), page(0, 0)},
			"page past the end":    {page(1, 0), page(9, 0)},
			"rotation not 90s":     {page(1, 45)},
			"no items at all":      {},
			"more than 1000 items": manyPages(1001),
		} {
			ack := request(t, stream, map[string]any{"id": 30, "method": "run_operation", "params": map[string]any{
				"op": "pdf-edit", "files": []string{doc}, "output_dir": dir, "params": map[string]any{"edit": items},
			}})
			if ack["error"] != nil {
				continue // rejected up front, which is also correct
			}
			id, _ := ack["result"].(map[string]any)["job"].(string)
			if output := tryJobOutput(t, stream, id); output != "" {
				t.Errorf("%s produced %s, want a failure", name, output)
			}
		}
		after, _ := filepath.Glob(filepath.Join(dir, "*-edited*.pdf"))
		if len(after) != len(before) {
			t.Fatalf("failed edits left files behind: %v -> %v", before, after)
		}
	})
}

func manyPages(n int) []map[string]any {
	items := make([]map[string]any, n)
	for i := range items {
		items[i] = map[string]any{"src": 1, "rotate": 0}
	}
	return items
}

// tryJobOutput waits for a job and returns its first output, or "" when the
// job failed to produce one.
func tryJobOutput(t *testing.T, stream *e2eStream, id string) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		message, err := stream.read(5 * time.Second)
		if err != nil {
			t.Fatalf("waiting for job.finished: %v", err)
		}
		if event, _ := message["event"].(string); event != "job.finished" {
			continue
		}
		data, _ := message["data"].(map[string]any)
		if data["id"] != id {
			continue
		}
		files, _ := data["files"].([]any)
		for _, entry := range files {
			if outputs := outputsOf(entry.(map[string]any)); len(outputs) > 0 {
				return outputs[0]
			}
		}
		return ""
	}
	t.Fatal("timed out waiting for job.finished")
	return ""
}

func TestDaemonEndToEndPDFSplit(t *testing.T) {
	stream, dir, doc := studioFixture(t)

	split := func(op string, params map[string]any) []map[string]any {
		return runJobForFiles(t, stream, map[string]any{"op": op, "files": []string{doc}, "output_dir": dir, "params": params})
	}

	t.Run("by ranges", func(t *testing.T) {
		row := finishedFile(t, split("pdf-split-ranges", map[string]any{"ranges": "1-2;3-"}))
		outputs := outputsOf(row)
		if len(outputs) != 2 {
			t.Fatalf("outputs = %v, want 2 parts", outputs)
		}
		for i, want := range [][]string{{"PAGE1", "PAGE2"}, {"PAGE3", "PAGE4"}} {
			if got := pageTexts(t, outputs[i]); !equalStrings(got, want) {
				t.Errorf("part %d = %q, want %q", i+1, got, want)
			}
			if !strings.Contains(filepath.Base(outputs[i]), "-part-") {
				t.Errorf("part %d is named %s, want a -part- name", i+1, outputs[i])
			}
		}
	})

	t.Run("every N pages leaves a short last part", func(t *testing.T) {
		row := finishedFile(t, split("pdf-split-every", map[string]any{"every": 3}))
		outputs := outputsOf(row)
		if len(outputs) != 2 {
			t.Fatalf("outputs = %v, want 2 parts", outputs)
		}
		if got := pageTexts(t, outputs[0]); len(got) != 3 {
			t.Errorf("first part has %d pages, want 3", len(got))
		}
		if got := pageTexts(t, outputs[1]); !equalStrings(got, []string{"PAGE4"}) {
			t.Errorf("last part = %q, want [PAGE4]", got)
		}
	})

	t.Run("a repeated split renames instead of overwriting", func(t *testing.T) {
		existing, _ := filepath.Glob(filepath.Join(dir, "doc-part-*.pdf"))
		if len(existing) == 0 {
			t.Fatal("expected parts from the earlier splits")
		}
		stamps := map[string]time.Time{}
		for _, path := range existing {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			stamps[path] = info.ModTime()
		}
		row := finishedFile(t, split("pdf-split-ranges", map[string]any{"ranges": "1-2;3-"}))
		for _, output := range outputsOf(row) {
			if _, taken := stamps[output]; taken {
				t.Errorf("%s reused the name of an existing part", output)
			}
			if !strings.Contains(filepath.Base(output), "(") {
				t.Errorf("%s was not renamed on conflict", output)
			}
		}
		for path, stamp := range stamps {
			info, err := os.Stat(path)
			if err != nil || !info.ModTime().Equal(stamp) {
				t.Errorf("%s was touched by the repeated split (%v)", path, err)
			}
		}
	})

	t.Run("bad splits fail before writing", func(t *testing.T) {
		before, _ := filepath.Glob(filepath.Join(dir, "*-part-*"))
		for name, params := range map[string]map[string]any{
			"range past the end":   {"ranges": "1-2;9"},
			"every covers it all":  {"every": 4},
			"every of zero":        {"every": 0},
			"an empty range group": {"ranges": "1-2;;3"},
		} {
			op := "pdf-split-ranges"
			if _, ok := params["every"]; ok {
				op = "pdf-split-every"
			}
			ack := request(t, stream, map[string]any{"id": 40, "method": "run_operation", "params": map[string]any{
				"op": op, "files": []string{doc}, "output_dir": dir, "params": params,
			}})
			if ack["error"] != nil {
				continue
			}
			id, _ := ack["result"].(map[string]any)["job"].(string)
			if output := tryJobOutput(t, stream, id); output != "" {
				t.Errorf("%s produced %s, want a failure", name, output)
			}
		}
		after, _ := filepath.Glob(filepath.Join(dir, "*-part-*"))
		if len(after) != len(before) {
			t.Fatalf("failed splits left files behind: %v -> %v", before, after)
		}
	})
}

func TestDaemonEndToEndPDFMergeWithRanges(t *testing.T) {
	stream, dir, doc := studioFixture(t)
	single := filepath.Join(dir, "src1.pdf")

	t.Run("per-file ranges pick pages in file order", func(t *testing.T) {
		out := runJob(t, stream, map[string]any{
			"op": "pdf-merge", "files": []string{single, doc}, "output_dir": dir,
			"params": map[string]any{"file_pages": []string{"", "3-4"}},
		})
		if got, want := pageTexts(t, out), []string{"PAGE1", "PAGE3", "PAGE4"}; !equalStrings(got, want) {
			t.Fatalf("pages = %q, want %q", got, want)
		}
	})

	t.Run("file order is the merge order", func(t *testing.T) {
		out := runJob(t, stream, map[string]any{
			"op": "pdf-merge", "files": []string{doc, single}, "output_dir": dir,
			"params": map[string]any{"file_pages": []string{"2", ""}},
		})
		if got, want := pageTexts(t, out), []string{"PAGE2", "PAGE1"}; !equalStrings(got, want) {
			t.Fatalf("pages = %q, want %q", got, want)
		}
	})

	t.Run("no ranges merges everything as before", func(t *testing.T) {
		out := runJob(t, stream, map[string]any{"op": "pdf-merge", "files": []string{single, doc}, "output_dir": dir})
		if got := pageTexts(t, out); len(got) != 5 {
			t.Fatalf("pages = %q, want all 5", got)
		}
	})

	t.Run("a range naming a missing page fails the job", func(t *testing.T) {
		ack := request(t, stream, map[string]any{"id": 50, "method": "run_operation", "params": map[string]any{
			"op": "pdf-merge", "files": []string{single, doc}, "output_dir": dir,
			"params": map[string]any{"file_pages": []string{"", "9"}},
		}})
		if ack["error"] != nil {
			return
		}
		id, _ := ack["result"].(map[string]any)["job"].(string)
		if output := tryJobOutput(t, stream, id); output != "" {
			t.Fatalf("produced %s, want a failure", output)
		}
	})
}
