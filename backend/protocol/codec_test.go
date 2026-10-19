package protocol

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestDecodeRequest(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *Request
		wantErr bool
	}{
		{
			name:  "valid hello request",
			input: `{"id":1,"method":"hello","params":{"client":"test","protocol":1}}` + "\n",
			want: &Request{
				ID:     1,
				Method: "hello",
				Params: json.RawMessage(`{"client":"test","protocol":1}`),
			},
		},
		{
			name:  "valid targets request",
			input: `{"id":2,"method":"targets","params":{"files":["/tmp/test.png"]}}` + "\n",
			want: &Request{
				ID:     2,
				Method: "targets",
				Params: json.RawMessage(`{"files":["/tmp/test.png"]}`),
			},
		},
		{
			name:    "invalid json",
			input:   `{broken json}` + "\n",
			wantErr: true,
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := strings.NewReader(tt.input)
			got, err := DecodeRequest(r)
			if (err != nil) != tt.wantErr {
				t.Fatalf("DecodeRequest() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.ID != tt.want.ID || got.Method != tt.want.Method {
				t.Errorf("DecodeRequest() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestEncodeMessage(t *testing.T) {
	tests := []struct {
		name string
		msg  interface{}
		want string
	}{
		{
			name: "response",
			msg: Response{
				ID:     1,
				Result: map[string]string{"server": "fcd"},
			},
			want: `{"id":1,"result":{"server":"fcd"}}` + "\n",
		},
		{
			name: "error",
			msg: Error{
				ID: 2,
				Error: ErrorValue{
					Code:    "bad_request",
					Message: "invalid params",
				},
			},
			want: `{"id":2,"error":{"code":"bad_request","message":"invalid params"}}` + "\n",
		},
		{
			name: "event",
			msg: Event{
				Name: "job.updated",
				Data: map[string]string{
					"id": "job-123",
				},
			},
			want: `{"event":"job.updated","data":{"id":"job-123"}}` + "\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := EncodeMessage(&buf, tt.msg); err != nil {
				t.Fatalf("EncodeMessage() error = %v", err)
			}
			if got := buf.String(); got != tt.want {
				t.Errorf("EncodeMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCodecRoundTrip(t *testing.T) {
	req := &Request{
		ID:     42,
		Method: "convert",
		Params: json.RawMessage(`{"files":["/tmp/a.png"],"preset":"jpg"}`),
	}

	var buf bytes.Buffer
	resp := Response{ID: req.ID, Result: map[string]string{"job": "xyz"}}
	if err := EncodeMessage(&buf, resp); err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	// Verify we can read it back
	scanner := bytes.NewReader(buf.Bytes())
	var decoded Response
	line, _ := io.ReadAll(scanner)
	if err := json.Unmarshal(bytes.TrimSpace(line), &decoded); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if decoded.ID != resp.ID {
		t.Errorf("round trip ID mismatch: got %d, want %d", decoded.ID, resp.ID)
	}
}
