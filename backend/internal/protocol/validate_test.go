package protocol

import (
	"encoding/json"
	"runtime"
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
