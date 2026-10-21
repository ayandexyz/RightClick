package main

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"file-converter/backend/internal/pdfops"
)

func TestParseConvertAcceptsToAlias(t *testing.T) {
	command, err := parseArgs([]string{"convert", "photo.png", "--to", "jpeg", "--output-dir", "/tmp/out", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if command.preset != "image-to-jpeg" || command.outputDir != "/tmp/out" || !command.jsonOutput {
		t.Fatalf("unexpected command: %+v", command)
	}
	if len(command.files) != 1 || command.files[0] != "photo.png" {
		t.Fatalf("unexpected files: %#v", command.files)
	}
}

func TestParseConvertRejectsUnknownToTarget(t *testing.T) {
	if _, err := parseArgs([]string{"convert", "photo.png", "--to", "pdf"}); err == nil {
		t.Fatal("expected unknown target to be rejected")
	}
}

func TestFormatEventHumanReadable(t *testing.T) {
	message := wireEvent{Event: "job.updated", Data: jobData{ID: "job-1", State: "running", Progress: 0.5}}
	line := formatHumanEvent(message)
	if !strings.Contains(line, "job-1") || !strings.Contains(line, "running") || !strings.Contains(line, "50%") {
		t.Fatalf("unexpected output: %q", line)
	}
}

func TestFormatFailedEventIncludesPerFileError(t *testing.T) {
	message := wireEvent{Event: "job.finished", Data: jobData{ID: "job-1", State: "failed", Files: []fileData{{Source: "/tmp/missing.png", State: "failed", Error: &fileError{Code: "unsupported", Message: "file not found"}}}}}
	line := formatHumanEvent(message)
	if !strings.Contains(line, "file not found") || !strings.Contains(line, "/tmp/missing.png") {
		t.Fatalf("unexpected output: %q", line)
	}
}

func TestParseConvertImageOptions(t *testing.T) {
	cmd, err := parseArgs([]string{"convert", "/a.png", "--to", "jpeg", "--quality", "80", "--max-size", "2048", "--background", "#fff"})
	if err != nil {
		t.Fatal(err)
	}
	var params map[string]any
	if err := json.Unmarshal(commandRequest(cmd).Params, &params); err != nil {
		t.Fatal(err)
	}
	if params["quality"] != float64(80) || params["max_size"] != float64(2048) || params["background"] != "#fff" {
		t.Fatalf("params = %#v", params)
	}
}

func TestParseImageOptionsRejectBadValuesAndNonConvert(t *testing.T) {
	for _, args := range [][]string{
		{"convert", "/a.png", "--to", "jpeg", "--quality", "high"},
		{"convert", "/a.png", "--to", "jpeg", "--max-size"},
		{"targets", "/a.png", "--quality", "80"},
	} {
		if _, err := parseArgs(args); err == nil {
			t.Fatalf("parseArgs(%v) succeeded", args)
		}
	}
}

func TestFormatFinishedEventIncludesSkipNotice(t *testing.T) {
	message := wireEvent{Event: "job.finished", Data: jobData{ID: "job-1", State: "completed", Files: []fileData{{Source: "/p/notes.txt", State: "skipped", Notice: "unsupported image format"}}}}
	if line := formatHumanEvent(message); !strings.Contains(line, "skipped /p/notes.txt: unsupported image format") {
		t.Fatalf("line = %q", line)
	}
}

func TestParseEnginesCommand(t *testing.T) {
	cmd, err := parseArgs([]string{"engines", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.name != "engines" || !cmd.jsonOutput {
		t.Fatalf("unexpected command: %+v", cmd)
	}
	if req := commandRequest(cmd); req.Method != "engines" {
		t.Fatalf("method = %q", req.Method)
	}
	if _, err := parseArgs([]string{"engines", "file.png"}); err == nil {
		t.Fatal("engines must not accept files")
	}
}

func TestFormatEnginesHumanReadable(t *testing.T) {
	value := map[string]any{"result": map[string]any{"engines": []any{
		map[string]any{"id": "magick", "available": true, "reason": ""},
		map[string]any{"id": "ffmpeg", "available": false, "reason": "ffmpeg not found"},
	}}}
	got := formatHuman(value)
	for _, want := range []string{"magick", "available", "ffmpeg", "unavailable: ffmpeg not found"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output %q missing %q", got, want)
		}
	}
}

func TestParseTargetsMenuFlag(t *testing.T) {
	cmd, err := parseArgs([]string{"targets", "a.png", "--menu"})
	if err != nil || !cmd.menu {
		t.Fatalf("cmd=%+v err=%v", cmd, err)
	}
	var params map[string]any
	_ = json.Unmarshal(commandRequest(cmd).Params, &params)
	if params["menu"] != true {
		t.Fatalf("params: %#v", params)
	}
	if _, err := parseArgs([]string{"convert", "a.png", "--to", "png", "--menu"}); err == nil {
		t.Fatal("--menu only applies to targets")
	}
}

func TestParseMenuCommands(t *testing.T) {
	for args, want := range map[string]map[string]any{
		"menu show":               {"action": "show"},
		"menu set video mp4 webm": {"action": "set", "category": "video", "formats": []any{"mp4", "webm"}},
		"menu reset":              {"action": "reset"},
		"menu reset audio":        {"action": "reset", "category": "audio"},
	} {
		cmd, err := parseArgs(strings.Fields(args))
		if err != nil {
			t.Fatalf("%s: %v", args, err)
		}
		req := commandRequest(cmd)
		if req.Method != "menu" {
			t.Fatalf("%s: method %q", args, req.Method)
		}
		var params map[string]any
		_ = json.Unmarshal(req.Params, &params)
		if !reflect.DeepEqual(params, want) {
			t.Fatalf("%s: params %#v, want %#v", args, params, want)
		}
	}
	for _, bad := range []string{"menu", "menu bogus", "menu set", "menu set video", "menu show video"} {
		if _, err := parseArgs(strings.Fields(bad)); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestFormatMenuHumanReadable(t *testing.T) {
	value := map[string]any{"result": map[string]any{
		"path":   "/c/menu.toml",
		"custom": []any{"video"},
		"menu":   map[string]any{"image": []any{"jpeg", "png"}, "video": []any{"mp4"}},
	}}
	got := formatHuman(value)
	for _, want := range []string{"/c/menu.toml", "image: jpeg, png", "video: mp4 (custom)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output %q missing %q", got, want)
		}
	}
}

func TestParseConvertRecursiveAndOutDirAlias(t *testing.T) {
	cmd, err := parseArgs([]string{"convert", "/photos", "--recursive", "--to", "jpeg", "--out-dir", "/dest"})
	if err != nil {
		t.Fatal(err)
	}
	if !cmd.recursive || cmd.outputDir != "/dest" {
		t.Fatalf("cmd = %+v", cmd)
	}
	var params map[string]any
	_ = json.Unmarshal(commandRequest(cmd).Params, &params)
	if params["recursive"] != true || params["output_dir"] != "/dest" {
		t.Fatalf("params = %#v", params)
	}
	plain, _ := parseArgs([]string{"convert", "/a.png", "--to", "jpeg"})
	var plainParams map[string]any
	_ = json.Unmarshal(commandRequest(plain).Params, &plainParams)
	if _, ok := plainParams["recursive"]; ok {
		t.Fatalf("recursive sent without the flag: %#v", plainParams)
	}
	if _, err := parseArgs([]string{"targets", "/a", "--recursive"}); err == nil {
		t.Fatal("--recursive only applies to convert")
	}
}

func TestParseConvertMakesRelativeInputsAbsolute(t *testing.T) {
	cmd, err := parseArgs([]string{"convert", "photos/", "--to", "jpeg", "--out-dir", "dest"})
	if err != nil {
		t.Fatal(err)
	}
	var params struct {
		Files     []string `json:"files"`
		OutputDir string   `json:"output_dir"`
	}
	_ = json.Unmarshal(commandRequest(cmd).Params, &params)
	if !filepath.IsAbs(params.Files[0]) || !strings.HasSuffix(params.Files[0], "/photos") || !filepath.IsAbs(params.OutputDir) {
		t.Fatalf("params = %+v", params)
	}
}

func TestParsePDFSubcommand(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantCmd string
		wantErr string
	}{
		{name: "merge two files", args: []string{"pdf", "merge", "a.pdf", "b.pdf"}, wantCmd: "pdf-merge"},
		{name: "extract pages", args: []string{"pdf", "extract", "a.pdf", "--pages", "1-3"}, wantCmd: "pdf-extract"},
		{name: "delete pages", args: []string{"pdf", "delete", "a.pdf", "--pages", "2,4"}, wantCmd: "pdf-delete"},
		{name: "rotate all", args: []string{"pdf", "rotate", "a.pdf", "--angle", "90"}, wantCmd: "pdf-rotate"},
		{name: "rotate pages", args: []string{"pdf", "rotate", "a.pdf", "--angle", "270", "--pages", "1-3"}, wantCmd: "pdf-rotate"},
		{name: "reverse", args: []string{"pdf", "reverse", "a.pdf"}, wantCmd: "pdf-reverse"},
		{name: "list", args: []string{"pdf", "list", "a.pdf"}, wantCmd: "pdf-list"},
		{name: "unknown op", args: []string{"pdf", "stamp", "a.pdf"}, wantErr: "unknown pdf command"},
		{name: "extract without pages", args: []string{"pdf", "extract", "a.pdf"}, wantErr: "--pages"},
		{name: "rotate without angle", args: []string{"pdf", "rotate", "a.pdf"}, wantErr: "--angle"},
		{name: "bad angle", args: []string{"pdf", "rotate", "a.pdf", "--angle", "45"}, wantErr: "--angle"},
		{name: "merge one file", args: []string{"pdf", "merge", "a.pdf"}, wantErr: "at least 2"},
		{name: "no files", args: []string{"pdf", "reverse"}, wantErr: "file is required"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := parseArgs(tc.args)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("parseArgs: %v", err)
				}
				if cmd.op != tc.wantCmd {
					t.Fatalf("op = %q, want %q", cmd.op, tc.wantCmd)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q should contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestPDFRequestBodyForRunOperation(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "doc.pdf")
	cmd, err := parseArgs([]string{"pdf", "extract", source, "--pages", "1-3,7", "--output-dir", dir, "--on-conflict", "overwrite"})
	if err != nil {
		t.Fatal(err)
	}
	var params map[string]any
	_ = json.Unmarshal(commandRequest(cmd).Params, &params)
	if params["op"] != "pdf-extract" {
		t.Fatalf("op = %v", params["op"])
	}
	files, _ := params["files"].([]any)
	if len(files) != 1 || files[0] != source {
		t.Fatalf("files = %v", params["files"])
	}
	nested, _ := params["params"].(map[string]any)
	if nested["pages"] != "1-3,7" {
		t.Fatalf("pages = %v", nested["pages"])
	}
	if params["output_dir"] != dir {
		t.Fatalf("output_dir = %v", params["output_dir"])
	}
	if params["on_conflict"] != "overwrite" {
		t.Fatalf("on_conflict = %v", params["on_conflict"])
	}
}

func TestPDFRequestBodyForList(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "doc.pdf")
	cmd, err := parseArgs([]string{"pdf", "list", source})
	if err != nil {
		t.Fatal(err)
	}
	req := commandRequest(cmd)
	if req.Method != "operations" {
		t.Fatalf("method = %q, want operations", req.Method)
	}
	var params map[string]any
	_ = json.Unmarshal(req.Params, &params)
	files, _ := params["files"].([]any)
	if len(files) != 1 || files[0] != source {
		t.Fatalf("files = %v", params["files"])
	}
}

// TestEveryCatalogueOperationIsReachableFromTheCLI is what makes `fcctl pdf`
// table-driven: every instant operation in the catalogue must have a verb in
// pdfCommandByShortName, so adding an operation without a CLI verb is caught
// here. We check the dispatcher separately from full argument validation so
// callers learn about minimum-file requirements only when they actually pass
// files.
func TestEveryCatalogueOperationIsReachableFromTheCLI(t *testing.T) {
	for _, op := range pdfops.Catalogue() {
		name := pdfShortName(op.ID)
		opID, ok := pdfCommandByShortName(name)
		if !ok {
			t.Errorf("pdf %s (id %s): no CLI verb", name, op.ID)
			continue
		}
		if opID != op.ID {
			t.Errorf("pdf %s -> %s, want %s", name, opID, op.ID)
		}
	}
}

func TestFixedAngleVariantsNeedNoAngle(t *testing.T) {
	for _, name := range []string{"rotate-cw", "rotate-ccw", "rotate-180"} {
		cmd, err := parsePDFArgs([]string{"pdf", name, "/tmp/a.pdf"})
		if err != nil {
			t.Fatalf("pdf %s: %v", name, err)
		}
		if cmd.pdfAngle != nil {
			t.Fatalf("pdf %s should not require an angle", name)
		}
	}
	if _, err := parsePDFArgs([]string{"pdf", "rotate-cw", "/tmp/a.pdf", "--angle", "90"}); err == nil {
		t.Fatal("a fixed-angle variant should reject an explicit angle")
	}
}

func TestUnknownPDFCommandListsTheValidOnes(t *testing.T) {
	_, err := parsePDFArgs([]string{"pdf", "frobnicate", "/tmp/a.pdf"})
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"compress-balanced", "repair", "to-text", "extract-images", "list"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
}

func TestExtractImagesIsNumberedAndToTextIsTxt(t *testing.T) {
	images, _ := pdfops.Lookup("pdf-extract-images")
	if !images.Numbered || images.OutputExtension() != "png" {
		t.Fatalf("extract-images = %+v, want numbered png", images)
	}
	text, _ := pdfops.Lookup("pdf-to-text")
	if text.Numbered || text.OutputExtension() != "txt" {
		t.Fatalf("to-text = %+v, want a single txt", text)
	}
}

func TestParsePDFInfoTakesExactlyOneFile(t *testing.T) {
	cmd, err := parsePDFArgs([]string{"pdf", "info", "/tmp/a.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	if !cmd.pdfInfo || cmd.op != "pdf-info" {
		t.Fatalf("command = %+v, want the info query", cmd)
	}
	if _, err := parsePDFArgs([]string{"pdf", "info"}); err == nil {
		t.Fatal("pdf info with no file should be rejected")
	}
}

func TestPDFInfoRequestBody(t *testing.T) {
	cmd, err := parsePDFArgs([]string{"pdf", "info", "a.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	req := commandRequest(cmd)
	if req.Method != "pdf_info" {
		t.Fatalf("method = %q, want pdf_info", req.Method)
	}
}

func TestFormatPDFInfoOmitsSizesWhenAbsent(t *testing.T) {
	// A daemon without pdfinfo reports the count alone; showing zero-sized
	// pages would look like real measurements of a blank document.
	got := formatPDFInfo(map[string]any{"pages": 12}, 12.0)
	if got != "pages: 12" {
		t.Fatalf("got %q, want just the count", got)
	}
	got = formatPDFInfo(map[string]any{
		"pages": 2,
		"sizes": []any{
			map[string]any{"w": 595.0, "h": 842.0, "rot": 0.0},
			map[string]any{"w": 612.0, "h": 792.0, "rot": 90.0},
		},
	}, 2.0)
	if !strings.Contains(got, "595 x 842 pts") || !strings.Contains(got, "rotated 90") {
		t.Fatalf("got %q, want both pages with sizes and rotation", got)
	}
}

func TestParsePDFThumbsRequiresPagesAndOneFile(t *testing.T) {
	if _, err := parsePDFArgs([]string{"pdf", "thumbs", "--pages", "1-3", "/tmp/a.pdf"}); err != nil {
		t.Fatal(err)
	}
	if _, err := parsePDFArgs([]string{"pdf", "thumbs", "/tmp/a.pdf"}); err == nil {
		t.Fatal("pdf thumbs without --pages should be rejected")
	}
	if _, err := parsePDFArgs([]string{"pdf", "thumbs", "--pages", "1", "a.pdf", "b.pdf"}); err == nil {
		t.Fatal("pdf thumbs with two files should be rejected")
	}
	// A malformed spec is caught here, not on the daemon.
	if _, err := parsePDFArgs([]string{"pdf", "thumbs", "--pages", "9-3", "/tmp/a.pdf"}); err == nil {
		t.Fatal("a reversed range should be rejected")
	}
}

func TestPDFThumbsRequestBody(t *testing.T) {
	cmd, err := parsePDFArgs([]string{"pdf", "thumbs", "--pages", "1-3", "--width", "128", "a.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	req := commandRequest(cmd)
	if req.Method != "pdf_thumbnails" {
		t.Fatalf("method = %q, want pdf_thumbnails", req.Method)
	}
	var params struct {
		File  string `json:"file"`
		Pages []int  `json:"pages"`
		Width int    `json:"width"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatal(err)
	}
	if len(params.Pages) != 3 || params.Pages[0] != 1 || params.Pages[2] != 3 {
		t.Fatalf("pages = %v, want 1,2,3", params.Pages)
	}
	if params.Width != 128 {
		t.Fatalf("width = %d, want 128", params.Width)
	}
}

func TestParseCacheArgs(t *testing.T) {
	cmd, err := parseCacheArgs([]string{"cache", "clear"})
	if err != nil {
		t.Fatal(err)
	}
	if commandRequest(cmd).Method != "cache_clear" {
		t.Fatalf("method = %q, want cache_clear", commandRequest(cmd).Method)
	}
	for _, args := range [][]string{{"cache"}, {"cache", "list"}, {"cache", "clear", "--nope"}} {
		if _, err := parseCacheArgs(args); err == nil {
			t.Fatalf("%v should have been rejected", args)
		}
	}
}

func TestFormatThumbnailsPrintsOneLinePerPage(t *testing.T) {
	got := formatThumbnails([]any{
		map[string]any{"page": 1.0, "path": "/a.png", "w": 200.0, "h": 283.0},
		map[string]any{"page": 2.0, "path": "/b.png", "w": 200.0, "h": 283.0},
	})
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	if !strings.Contains(lines[0], "/a.png") || !strings.Contains(lines[1], "/b.png") {
		t.Fatalf("lines should carry the paths, got %q", got)
	}
}

func TestFormatResultDistinguishesThumbnailsFromPageCount(t *testing.T) {
	// pdf_info returns a count; pdf_thumbnails returns a list. Guessing wrong
	// would print thumbnails as if they were a page count.
	info := formatResult(map[string]any{"pages": 12.0})
	if info != "pages: 12" {
		t.Fatalf("pdf_info rendered as %q", info)
	}
	thumbs := formatResult(map[string]any{"pages": []any{
		map[string]any{"page": 1.0, "path": "/a.png", "w": 200.0, "h": 283.0},
	}})
	if !strings.Contains(thumbs, "/a.png") {
		t.Fatalf("pdf_thumbnails rendered as %q", thumbs)
	}
	if cleared := formatResult(map[string]any{"removed": 3.0}); cleared != "removed 3 cached thumbnails" {
		t.Fatalf("cache_clear rendered as %q", cleared)
	}
}

// The usage line is the only help fcctl has, and it once went stale when the
// edit and split commands were added. Every operation that needs its own
// arguments (the studio ones) and every flag the PDF parser accepts must be
// named in it; instant operations are covered by the generic NAME form.
func TestUsageNamesEveryPDFCommandAndFlag(t *testing.T) {
	for _, op := range pdfops.Catalogue() {
		// An operation that cannot run yet (OCR) is not worth advertising.
		if op.Mode != "studio" || op.Unimplemented != "" {
			continue
		}
		name := pdfShortName(op.ID)
		if !strings.Contains(usageText, name+" ") && !strings.Contains(usageText, "| "+name) {
			t.Errorf("usage does not mention the studio command %q", name)
		}
	}
	for _, word := range []string{"merge", "reverse", "list", "info", "thumbs", "NAME FILE...",
		"--edit", "--ranges", "--every", "--pages-for", "--pages", "--angle", "--width", "--out-dir", "--on-conflict"} {
		if !strings.Contains(usageText, word) {
			t.Errorf("usage does not mention %q", word)
		}
	}
}

func toolParams(t *testing.T, args ...string) map[string]any {
	t.Helper()
	cmd, err := parseArgs(append([]string{"pdf"}, args...))
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	var params map[string]any
	_ = json.Unmarshal(commandRequest(cmd).Params, &params)
	nested, _ := params["params"].(map[string]any)
	return nested
}

func TestStampToolFlagsBuildTheTypedParams(t *testing.T) {
	stamp, _ := toolParams(t, "page-numbers", "a.pdf", "--format", "of-total", "--start", "5", "--position", "bc", "--pages", "2-4")["stamp"].(map[string]any)
	if stamp["format"] != "of-total" || stamp["start"] != float64(5) || stamp["position"] != "bc" || stamp["pages"] != "2-4" {
		t.Fatalf("page-numbers stamp = %v", stamp)
	}
	stamp, _ = toolParams(t, "header-footer", "a.pdf", "--left", "Draft", "--right", "%p", "--edge", "bottom")["stamp"].(map[string]any)
	if stamp["left"] != "Draft" || stamp["right"] != "%p" || stamp["edge"] != "bottom" {
		t.Fatalf("header-footer stamp = %v", stamp)
	}
	stamp, _ = toolParams(t, "stamp-text", "a.pdf", "--text", "DRAFT", "--opacity", "40", "--on-top")["stamp"].(map[string]any)
	if stamp["text"] != "DRAFT" || stamp["opacity"] != float64(40) || stamp["on_top"] != true {
		t.Fatalf("text stamp = %v", stamp)
	}
}

func TestGeometryAndLayoutFlagsBuildTheTypedParams(t *testing.T) {
	crop, _ := toolParams(t, "crop", "a.pdf", "--unit", "mm", "--top", "10.5", "--right", "5")["crop"].(map[string]any)
	if crop["unit"] != "mm" || crop["top"] != 10.5 || crop["right"] != float64(5) {
		t.Fatalf("crop = %v", crop)
	}
	resize, _ := toolParams(t, "resize", "a.pdf", "--to", "A4")["resize"].(map[string]any)
	if resize["to"] != "A4" {
		t.Fatalf("resize = %v", resize)
	}
	layout, _ := toolParams(t, "nup", "a.pdf", "--per-sheet", "4", "--border")["layout"].(map[string]any)
	if layout["per_sheet"] != float64(4) || layout["border"] != true {
		t.Fatalf("layout = %v", layout)
	}
}

func TestToolFlagsAreRejectedWhereTheyDoNotApply(t *testing.T) {
	for name, args := range map[string][]string{
		"number not whole":           {"pdf", "bates", "a.pdf", "--start", "x"},
		"crop takes no text":         {"pdf", "crop", "a.pdf", "--text", "hi"},
		"reverse takes no tool flag": {"pdf", "reverse", "a.pdf", "--per-sheet", "2"},
		"layout takes no pages":      {"pdf", "nup", "a.pdf", "--per-sheet", "2", "--pages", "1-2"},
		"margin is not a stamp flag": {"pdf", "bates", "a.pdf", "--margin", "2"},
	} {
		if _, err := parseArgs(args); err == nil {
			t.Errorf("%s: accepted %v", name, args)
		}
	}
}
