package protocol

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

func TestValidateRequest(t *testing.T) {
	tests := []struct {
		name     string
		req      Request
		wantCode string
		wantMsg  string
		wantNil  bool
	}{
		{
			name: "valid hello",
			req: Request{
				ID:     1,
				Method: "hello",
				Params: mustMarshal(map[string]interface{}{
					"client":   "test-client",
					"protocol": 1,
				}),
			},
			wantNil: true,
		},
		{
			name: "hello missing client",
			req: Request{
				ID:     1,
				Method: "hello",
				Params: mustMarshal(map[string]interface{}{
					"protocol": 1,
				}),
			},
			wantCode: "bad_request",
			wantMsg:  "client name is required",
		},
		{
			name: "hello unsupported protocol",
			req: Request{
				ID:     1,
				Method: "hello",
				Params: mustMarshal(map[string]interface{}{
					"client":   "test",
					"protocol": 2,
				}),
			},
			wantCode: "unsupported",
			wantMsg:  "protocol 2 not supported",
		},
		{
			name: "valid targets",
			req: Request{
				ID:     2,
				Method: "targets",
				Params: mustMarshal(map[string]interface{}{
					"files": []string{absolutePath("/tmp/test.png")},
				}),
			},
			wantNil: true,
		},
		{
			name: "targets empty files",
			req: Request{
				ID:     2,
				Method: "targets",
				Params: mustMarshal(map[string]interface{}{
					"files": []string{},
				}),
			},
			wantCode: "bad_request",
			wantMsg:  "files list cannot be empty",
		},
		{
			name: "targets relative path",
			req: Request{
				ID:     2,
				Method: "targets",
				Params: mustMarshal(map[string]interface{}{
					"files": []string{"relative/path.png"},
				}),
			},
			wantCode: "bad_request",
			wantMsg:  "path must be absolute",
		},
		{
			name: "valid convert",
			req: Request{
				ID:     3,
				Method: "convert",
				Params: mustMarshal(map[string]interface{}{
					"files":  []string{absolutePath("/tmp/test.png")},
					"preset": "image-to-jpeg",
				}),
			},
			wantNil: true,
		},
		{
			name: "convert missing preset",
			req: Request{
				ID:     3,
				Method: "convert",
				Params: mustMarshal(map[string]interface{}{
					"files": []string{absolutePath("/tmp/test.png")},
				}),
			},
			wantCode: "bad_request",
			wantMsg:  "preset is required",
		},
		{
			name: "convert unsupported preset",
			req: Request{
				ID:     3,
				Method: "convert",
				Params: mustMarshal(map[string]interface{}{
					"files":  []string{absolutePath("/tmp/test.png")},
					"preset": "unknown",
				}),
			},
			wantCode: "unsupported",
			wantMsg:  "preset not supported",
		},
		{
			name: "convert invalid conflict policy",
			req: Request{
				ID:     3,
				Method: "convert",
				Params: mustMarshal(map[string]interface{}{
					"files":       []string{absolutePath("/tmp/test.png")},
					"preset":      "image-to-jpeg",
					"on_conflict": "invalid",
				}),
			},
			wantCode: "bad_request",
			wantMsg:  "invalid on_conflict",
		},
		{
			name:     "convert invalid quality",
			req:      Request{ID: 3, Method: "convert", Params: mustMarshal(map[string]interface{}{"files": []string{absolutePath("/tmp/test.png")}, "preset": "image-to-jpeg", "quality": 101})},
			wantCode: "bad_request",
			wantMsg:  "quality must be between",
		},
		{
			name:     "convert invalid max size",
			req:      Request{ID: 3, Method: "convert", Params: mustMarshal(map[string]interface{}{"files": []string{absolutePath("/tmp/test.png")}, "preset": "image-to-jpeg", "max_size": 0})},
			wantCode: "bad_request",
			wantMsg:  "max_size must be between",
		},
		{
			name:     "convert invalid background",
			req:      Request{ID: 3, Method: "convert", Params: mustMarshal(map[string]interface{}{"files": []string{absolutePath("/tmp/test.png")}, "preset": "image-to-jpeg", "background": "@file"})},
			wantCode: "bad_request",
			wantMsg:  "background must be",
		},
		{
			name:     "convert rejects rgba shorthand background",
			req:      Request{ID: 3, Method: "convert", Params: mustMarshal(map[string]interface{}{"files": []string{absolutePath("/tmp/test.png")}, "preset": "image-to-png", "background": "#abcd"})},
			wantCode: "bad_request",
			wantMsg:  "background must be",
		},
		{
			name:     "convert transparent jpeg background rejected",
			req:      Request{ID: 3, Method: "convert", Params: mustMarshal(map[string]interface{}{"files": []string{absolutePath("/tmp/test.png")}, "preset": "image-to-jpeg", "background": "transparent"})},
			wantCode: "bad_request",
			wantMsg:  "transparent background",
		},
		{
			name:    "convert valid options",
			req:     Request{ID: 3, Method: "convert", Params: mustMarshal(map[string]interface{}{"files": []string{absolutePath("/tmp/test.png")}, "preset": "image-to-png", "quality": 100, "max_size": 32768, "background": "#AbC"})},
			wantNil: true,
		},
		{
			name: "convert valid conflict policies",
			req: Request{
				ID:     3,
				Method: "convert",
				Params: mustMarshal(map[string]interface{}{
					"files":       []string{absolutePath("/tmp/test.png")},
					"preset":      "image-to-jpeg",
					"on_conflict": "rename",
				}),
			},
			wantNil: true,
		},
		{
			name: "convert relative output_dir",
			req: Request{
				ID:     3,
				Method: "convert",
				Params: mustMarshal(map[string]interface{}{
					"files":      []string{absolutePath("/tmp/test.png")},
					"preset":     "image-to-jpeg",
					"output_dir": "relative/path",
				}),
			},
			wantCode: "bad_request",
			wantMsg:  "output_dir must be absolute",
		},
		{
			name: "valid cancel",
			req: Request{
				ID:     4,
				Method: "cancel",
				Params: mustMarshal(map[string]interface{}{
					"job": "job-123",
				}),
			},
			wantNil: true,
		},
		{
			name: "cancel missing job",
			req: Request{
				ID:     4,
				Method: "cancel",
				Params: mustMarshal(map[string]interface{}{}),
			},
			wantCode: "bad_request",
			wantMsg:  "job id is required",
		},
		{
			name: "unknown method",
			req: Request{
				ID:     99,
				Method: "unknown",
				Params: json.RawMessage(`{}`),
			},
			wantCode: "bad_request",
			wantMsg:  "unknown method",
		},
		{
			name: "empty method",
			req: Request{
				ID:     99,
				Method: "",
				Params: json.RawMessage(`{}`),
			},
			wantCode: "bad_request",
			wantMsg:  "method is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRequest(tt.req)
			if tt.wantNil {
				if err != nil {
					t.Errorf("ValidateRequest() = %+v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateRequest() = nil, want error with code %q", tt.wantCode)
			}
			if err.Error.Code != tt.wantCode {
				t.Errorf("ValidateRequest() code = %q, want %q", err.Error.Code, tt.wantCode)
			}
			if tt.wantMsg != "" && !contains(err.Error.Message, tt.wantMsg) {
				t.Errorf("ValidateRequest() msg = %q, want to contain %q", err.Error.Message, tt.wantMsg)
			}
		})
	}
}

func TestSupportedPDFPresets(t *testing.T) {
	want := map[string]Target{
		"pdf-to-png":  {Preset: "pdf-to-png", Label: "PDF to PNG", Format: "png", Category: "image", Enabled: true},
		"pdf-to-jpeg": {Preset: "pdf-to-jpeg", Label: "PDF to JPEG", Format: "jpeg", Category: "image", Enabled: true},
		"pdf-to-webp": {Preset: "pdf-to-webp", Label: "PDF to WebP", Format: "webp", Category: "image", Enabled: true},
	}
	for _, target := range SupportedPresets() {
		if expected, ok := want[target.Preset]; ok {
			if target != expected {
				t.Errorf("preset %q = %+v, want %+v", target.Preset, target, expected)
			}
			delete(want, target.Preset)
		}
	}
	for preset := range want {
		t.Errorf("SupportedPresets() missing %q", preset)
	}
}

func TestSupportedSVGPresets(t *testing.T) {
	want := map[string]Target{
		"svg-to-png":  {Preset: "svg-to-png", Label: "SVG to PNG", Format: "png", Category: "image", Enabled: true},
		"svg-to-jpeg": {Preset: "svg-to-jpeg", Label: "SVG to JPEG", Format: "jpeg", Category: "image", Enabled: true},
		"svg-to-webp": {Preset: "svg-to-webp", Label: "SVG to WebP", Format: "webp", Category: "image", Enabled: true},
	}
	for _, target := range SupportedPresets() {
		if expected, ok := want[target.Preset]; ok {
			if target != expected {
				t.Errorf("preset %q = %+v, want %+v", target.Preset, target, expected)
			}
			delete(want, target.Preset)
		}
	}
	for preset := range want {
		t.Errorf("SupportedPresets() missing %q", preset)
	}
}

func TestSupportedMediaPresets(t *testing.T) {
	wantCategories := map[string]string{
		"video-to-mp4": "video", "video-to-mov": "video", "video-to-webm": "video",
		"video-to-mkv": "video", "video-to-avi": "video", "video-to-gif": "video", "audio-to-mp3": "audio",
		"audio-to-wav": "audio", "audio-to-flac": "audio", "audio-to-aac": "audio",
		"audio-to-m4a": "audio", "audio-to-ogg": "audio", "audio-to-opus": "audio",
		"audio-to-aiff": "audio", "audio-to-alac": "audio", "audio-to-ac3": "audio",
		"audio-to-amr": "audio", "audio-to-wma": "audio",
	}
	for _, target := range SupportedPresets() {
		want, ok := wantCategories[target.Preset]
		if !ok {
			continue
		}
		if target.Format == "" || target.Category == "" || !target.Enabled {
			t.Errorf("media preset %q has incomplete target: %+v", target.Preset, target)
		}
		if target.Category != want {
			t.Errorf("media preset %q category = %q, want %q", target.Preset, target.Category, want)
		}
		delete(wantCategories, target.Preset)
	}
	for preset := range wantCategories {
		t.Errorf("SupportedPresets() missing media preset %q", preset)
	}
}

func TestSupportedPresets(t *testing.T) {
	presets := SupportedPresets()
	if len(presets) == 0 {
		t.Error("SupportedPresets() returned empty list")
	}

	// Check that common formats are present and have proper Target structure
	required := []string{"image-to-png", "image-to-jpeg", "image-to-webp", "image-to-jxl"}
	for _, r := range required {
		found := false
		for _, p := range presets {
			if p.Preset == r {
				found = true
				// Verify Target fields are populated
				if p.Label == "" {
					t.Errorf("preset %q has empty Label", r)
				}
				if p.Format == "" {
					t.Errorf("preset %q has empty Format", r)
				}
				if p.Category == "" {
					t.Errorf("preset %q has empty Category", r)
				}
				break
			}
		}
		if !found {
			t.Errorf("SupportedPresets() missing required preset: %s", r)
		}
	}
}

func mustMarshal(v interface{}) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && s[:len(substr)] == substr)
}

func absolutePath(path string) string {
	if runtime.GOOS == "windows" {
		return "C:" + path
	}
	return path
}

func TestValidateEnginesAcceptsEmptyAndNullParams(t *testing.T) {
	for _, params := range []string{``, `null`, `{}`} {
		if err := ValidateRequest(Request{ID: 1, Method: "engines", Params: json.RawMessage(params)}); err != nil {
			t.Fatalf("params %q rejected: %+v", params, err)
		}
	}
}

func TestValidateTargetsAcceptsMenuFlag(t *testing.T) {
	params := json.RawMessage(`{"files":["/tmp/a.png"],"menu":true}`)
	if err := ValidateRequest(Request{ID: 1, Method: "targets", Params: params}); err != nil {
		t.Fatalf("rejected: %+v", err)
	}
}

func TestValidateMenuRequests(t *testing.T) {
	cases := map[string]bool{
		`{"action":"show"}`: true,
		`{"action":"set","category":"video","formats":["mp4"]}`: true,
		`{"action":"set","category":"video","formats":[]}`:      true,
		`{"action":"reset"}`:                    true,
		`{"action":"reset","category":"audio"}`: true,
		`{}`:                                    false,
		`{"action":"explode"}`:                  false,
		`{"action":"set","formats":["mp4"]}`:    false,
		`{"action":"set","category":"video"}`:   false,
		`{"action":"show","category":"video","formats":["mp4"]}`: false,
		`{"action":"reset","formats":["mp4"]}`:                   false,
	}
	for params, ok := range cases {
		err := ValidateRequest(Request{ID: 1, Method: "menu", Params: json.RawMessage(params)})
		if ok && err != nil {
			t.Errorf("%s rejected: %+v", params, err)
		}
		if !ok && err == nil {
			t.Errorf("%s accepted", params)
		}
	}
}

func TestValidateConvertAcceptsRecursiveFlag(t *testing.T) {
	params := json.RawMessage(`{"files":["/tmp/folder"],"preset":"image-to-jpeg","recursive":true}`)
	if err := ValidateRequest(Request{ID: 1, Method: "convert", Params: params}); err != nil {
		t.Fatalf("rejected: %+v", err)
	}
	bad := json.RawMessage(`{"files":["/tmp/folder"],"preset":"image-to-jpeg","recursive":"yes"}`)
	if err := ValidateRequest(Request{ID: 1, Method: "convert", Params: bad}); err == nil {
		t.Fatal("non-boolean recursive accepted")
	}
}

func TestValidateOperations(t *testing.T) {
	tests := []struct {
		name     string
		params   string
		wantCode string
		wantMsg  string
	}{
		{name: "valid", params: `{"files":["/tmp/a.pdf"]}`},
		{name: "several files", params: `{"files":["/tmp/a.pdf","/tmp/b.pdf"]}`},
		{name: "empty files", params: `{"files":[]}`, wantCode: "bad_request", wantMsg: "files list cannot be empty"},
		{name: "missing files", params: `{}`, wantCode: "bad_request", wantMsg: "files list cannot be empty"},
		{name: "relative path", params: `{"files":["a.pdf"]}`, wantCode: "bad_request", wantMsg: "must be absolute"},
		{name: "invalid json", params: `{`, wantCode: "bad_request", wantMsg: "invalid operations params"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ValidateRequest(Request{ID: 1, Method: "operations", Params: json.RawMessage(tc.params)})
			if tc.wantCode == "" {
				if got != nil {
					t.Fatalf("ValidateRequest = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("ValidateRequest = nil, want an error")
			}
			if got.Error.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", got.Error.Code, tc.wantCode)
			}
			if !strings.Contains(got.Error.Message, tc.wantMsg) {
				t.Fatalf("message = %q, want it to contain %q", got.Error.Message, tc.wantMsg)
			}
		})
	}
}

func TestValidateRunOperation(t *testing.T) {
	tests := []struct {
		name     string
		params   string
		wantCode string
		wantMsg  string
	}{
		{name: "valid", params: `{"op":"pdf-merge","files":["/tmp/a.pdf","/tmp/b.pdf"]}`},
		{name: "with params", params: `{"op":"pdf-extract","files":["/tmp/a.pdf"],"params":{"pages":"1-3"}}`},
		{name: "with angle", params: `{"op":"pdf-rotate","files":["/tmp/a.pdf"],"params":{"angle":90}}`},
		{name: "with output dir", params: `{"op":"pdf-merge","files":["/tmp/a.pdf","/tmp/b.pdf"],"output_dir":"/tmp/out"}`},
		{name: "with conflict", params: `{"op":"pdf-merge","files":["/tmp/a.pdf","/tmp/b.pdf"],"on_conflict":"overwrite"}`},
		{name: "missing op", params: `{"files":["/tmp/a.pdf"]}`, wantCode: "bad_request", wantMsg: "op is required"},
		{name: "empty op", params: `{"op":"","files":["/tmp/a.pdf"]}`, wantCode: "bad_request", wantMsg: "op is required"},
		{name: "empty files", params: `{"op":"pdf-merge","files":[]}`, wantCode: "bad_request", wantMsg: "files list cannot be empty"},
		{name: "relative file", params: `{"op":"pdf-merge","files":["a.pdf","b.pdf"]}`, wantCode: "bad_request", wantMsg: "must be absolute"},
		{name: "relative output dir", params: `{"op":"pdf-merge","files":["/tmp/a.pdf","/tmp/b.pdf"],"output_dir":"out"}`, wantCode: "bad_request", wantMsg: "output_dir must be absolute"},
		{name: "bad conflict", params: `{"op":"pdf-merge","files":["/tmp/a.pdf","/tmp/b.pdf"],"on_conflict":"clobber"}`, wantCode: "bad_request", wantMsg: "invalid on_conflict"},
		{name: "angle not an integer", params: `{"op":"pdf-rotate","files":["/tmp/a.pdf"],"params":{"angle":"90"}}`, wantCode: "bad_request", wantMsg: "invalid run_operation params"},
		{name: "invalid json", params: `{`, wantCode: "bad_request", wantMsg: "invalid run_operation params"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ValidateRequest(Request{ID: 1, Method: "run_operation", Params: json.RawMessage(tc.params)})
			if tc.wantCode == "" {
				if got != nil {
					t.Fatalf("ValidateRequest = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("ValidateRequest = nil, want an error")
			}
			if got.Error.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", got.Error.Code, tc.wantCode)
			}
			if !strings.Contains(got.Error.Message, tc.wantMsg) {
				t.Fatalf("message = %q, want it to contain %q", got.Error.Message, tc.wantMsg)
			}
		})
	}
}

// The new methods must not swallow genuinely unknown ones.
func TestValidateRequestStillRejectsUnknownMethods(t *testing.T) {
	got := ValidateRequest(Request{ID: 1, Method: "run_operations", Params: json.RawMessage(`{}`)})
	if got == nil {
		t.Fatal("a misspelled method should be rejected")
	}
	if got.Error.Code != "bad_request" {
		t.Fatalf("code = %q, want bad_request", got.Error.Code)
	}
}

func TestValidatePDFInfo(t *testing.T) {
	if code, msg := validatePDFInfo(json.RawMessage(`{"file":"/tmp/a.pdf"}`)); code != "" {
		t.Fatalf("absolute file = %q %q, want accepted", code, msg)
	}
	for _, tc := range []struct{ name, params string }{
		{"not json", `{`},
		{"missing file", `{}`},
		{"relative path", `{"file":"a.pdf"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := validatePDFInfo(json.RawMessage(tc.params)); code == "" {
				t.Fatal("expected rejection")
			}
		})
	}
}

// An unknown method must still be rejected, so adding pdf_info must not have
// loosened the allowlist.
func TestUnknownMethodIsStillRejected(t *testing.T) {
	if err := ValidateRequest(Request{ID: 1, Method: "pdf_invented", Params: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("an unknown method should be rejected")
	}
}

func TestValidatePDFThumbnails(t *testing.T) {
	if code, msg := validatePDFThumbnails(json.RawMessage(`{"file":"/tmp/a.pdf","pages":[1,2]}`)); code != "" {
		t.Fatalf("valid request = %q %q, want accepted", code, msg)
	}
	for _, tc := range []struct{ name, params string }{
		{"not json", `{`},
		{"missing file", `{"pages":[1]}`},
		{"relative path", `{"file":"a.pdf","pages":[1]}`},
		{"no pages", `{"file":"/tmp/a.pdf"}`},
		{"empty pages", `{"file":"/tmp/a.pdf","pages":[]}`},
		{"page zero", `{"file":"/tmp/a.pdf","pages":[0]}`},
		{"negative page", `{"file":"/tmp/a.pdf","pages":[-1]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := validatePDFThumbnails(json.RawMessage(tc.params)); code == "" {
				t.Fatal("expected rejection")
			}
		})
	}
}

// cache_clear takes no params, so anything a client sends is accepted; the
// method removes everything or nothing.
func TestValidateCacheClear(t *testing.T) {
	if code, msg := validateCacheClear(json.RawMessage(`{}`)); code != "" {
		t.Fatalf("cache_clear = %q %q, want accepted", code, msg)
	}
	if code, msg := validateCacheClear(json.RawMessage(`{"anything":true}`)); code != "" {
		t.Fatalf("cache_clear with params = %q %q, want accepted", code, msg)
	}
}

func TestValidatePDFEdit(t *testing.T) {
	valid := `{"op":"pdf-edit","files":["/tmp/a.pdf"],"params":{"edit":[{"src":3},{"src":0},{"src":1,"rotate":90}]}}`
	if code, msg := validateRunOperation(json.RawMessage(valid)); code != "" {
		t.Fatalf("valid pdf-edit = %q %q", code, msg)
	}
	for _, tc := range []struct{ name, body string }{
		{"missing edit", `{"op":"pdf-edit","files":["/tmp/a.pdf"],"params":{}}`},
		{"negative source", `{"op":"pdf-edit","files":["/tmp/a.pdf"],"params":{"edit":[{"src":-1}]}}`},
		{"bad rotation", `{"op":"pdf-edit","files":["/tmp/a.pdf"],"params":{"edit":[{"src":1,"rotate":45}]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := validateRunOperation(json.RawMessage(tc.body)); code != "bad_request" {
				t.Fatalf("code = %q, want bad_request", code)
			}
		})
	}
}

func TestValidateStampPresets(t *testing.T) {
	run := func(op, stamp string) string {
		body := `{"op":"` + op + `","files":["/tmp/a.pdf"],"params":{"stamp":` + stamp + `}}`
		code, _ := validateRunOperation(json.RawMessage(body))
		return code
	}
	for name, tc := range map[string]struct{ op, stamp, want string }{
		"page numbers":                {"pdf-page-numbers", `{"format":"of-total","start":5}`, ""},
		"page numbers bad format":     {"pdf-page-numbers", `{"format":"roman"}`, "bad_request"},
		"bates":                       {"pdf-bates", `{"prefix":"ABC-","start":1}`, ""},
		"bates needs a start":         {"pdf-bates", `{"prefix":"ABC-"}`, "bad_request"},
		"bates start too large":       {"pdf-bates", `{"start":1000000000}`, "bad_request"},
		"header":                      {"pdf-header-footer", `{"left":"Draft","edge":"bottom"}`, ""},
		"header needs a zone":         {"pdf-header-footer", `{}`, "bad_request"},
		"header bad edge":             {"pdf-header-footer", `{"left":"x","edge":"middle"}`, "bad_request"},
		"zone with a newline":         {"pdf-header-footer", `{"left":"a\nb"}`, "bad_request"},
		"text stamp needs text":       {"pdf-stamp-text", `{}`, "bad_request"},
		"image stamp needs a path":    {"pdf-stamp-image", `{}`, "bad_request"},
		"image path must be absolute": {"pdf-stamp-image", `{"image":"a.png"}`, "bad_request"},
	} {
		if got := run(tc.op, tc.stamp); got != tc.want {
			t.Errorf("%s: code = %q, want %q", name, got, tc.want)
		}
	}
}
