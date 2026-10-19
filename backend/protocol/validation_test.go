package protocol

import (
	"encoding/json"
	"runtime"
	"testing"
)

func TestValidateRequest(t *testing.T) {
	tests := []struct {
		name     string
		req      *Request
		wantCode string
		wantMsg  string
	}{
		{
			name: "valid hello",
			req: &Request{
				ID:     1,
				Method: "hello",
				Params: mustMarshal(map[string]interface{}{
					"client":   "test-client",
					"protocol": 1,
				}),
			},
			wantCode: "",
		},
		{
			name: "hello missing client",
			req: &Request{
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
			req: &Request{
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
			req: &Request{
				ID:     2,
				Method: "targets",
				Params: mustMarshal(map[string]interface{}{
					"files": []string{absolutePath("/tmp/test.png")},
				}),
			},
			wantCode: "",
		},
		{
			name: "targets empty files",
			req: &Request{
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
			req: &Request{
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
			req: &Request{
				ID:     3,
				Method: "convert",
				Params: mustMarshal(map[string]interface{}{
					"files":  []string{absolutePath("/tmp/test.png")},
					"preset": "jpg",
				}),
			},
			wantCode: "",
		},
		{
			name: "convert missing preset",
			req: &Request{
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
			req: &Request{
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
			req: &Request{
				ID:     3,
				Method: "convert",
				Params: mustMarshal(map[string]interface{}{
					"files":       []string{absolutePath("/tmp/test.png")},
					"preset":      "jpg",
					"on_conflict": "invalid",
				}),
			},
			wantCode: "bad_request",
			wantMsg:  "invalid on_conflict",
		},
		{
			name: "convert valid conflict policies",
			req: &Request{
				ID:     3,
				Method: "convert",
				Params: mustMarshal(map[string]interface{}{
					"files":       []string{absolutePath("/tmp/test.png")},
					"preset":      "jpg",
					"on_conflict": "rename",
				}),
			},
			wantCode: "",
		},
		{
			name: "convert relative output_dir",
			req: &Request{
				ID:     3,
				Method: "convert",
				Params: mustMarshal(map[string]interface{}{
					"files":      []string{absolutePath("/tmp/test.png")},
					"preset":     "jpg",
					"output_dir": "relative/path",
				}),
			},
			wantCode: "bad_request",
			wantMsg:  "output_dir must be absolute",
		},
		{
			name: "valid cancel",
			req: &Request{
				ID:     4,
				Method: "cancel",
				Params: mustMarshal(map[string]interface{}{
					"job": "job-123",
				}),
			},
			wantCode: "",
		},
		{
			name: "cancel missing job",
			req: &Request{
				ID:     4,
				Method: "cancel",
				Params: mustMarshal(map[string]interface{}{}),
			},
			wantCode: "bad_request",
			wantMsg:  "job id is required",
		},
		{
			name: "valid jobs request",
			req: &Request{
				ID:     5,
				Method: "jobs",
				Params: json.RawMessage(`{}`),
			},
			wantCode: "",
		},
		{
			name: "valid subscribe request",
			req: &Request{
				ID:     6,
				Method: "subscribe",
				Params: json.RawMessage(`{}`),
			},
			wantCode: "",
		},
		{
			name: "unknown method",
			req: &Request{
				ID:     99,
				Method: "unknown",
				Params: json.RawMessage(`{}`),
			},
			wantCode: "bad_request",
			wantMsg:  "unknown method",
		},
		{
			name: "empty method",
			req: &Request{
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
			code, msg := ValidateRequest(tt.req)
			if code != tt.wantCode {
				t.Errorf("ValidateRequest() code = %q, want %q", code, tt.wantCode)
			}
			if tt.wantMsg != "" && msg != tt.wantMsg {
				if !contains(msg, tt.wantMsg) {
					t.Errorf("ValidateRequest() msg = %q, want to contain %q", msg, tt.wantMsg)
				}
			}
		})
	}
}

func TestSupportedPresets(t *testing.T) {
	presets := SupportedPresets()
	if len(presets) == 0 {
		t.Error("SupportedPresets() returned empty list")
	}

	// Check that common formats are present
	required := []string{"png", "jpg", "pdf", "mp4", "mp3"}
	for _, r := range required {
		found := false
		for _, p := range presets {
			if p == r {
				found = true
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
