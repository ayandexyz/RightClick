package protocol

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestDecodeRequest(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		want    Request
		wantErr bool
	}{
		{
			name:  "valid hello request",
			input: []byte(`{"id":1,"method":"hello","params":{"client":"test","protocol":1}}`),
			want: Request{
				ID:     1,
				Method: "hello",
				Params: json.RawMessage(`{"client":"test","protocol":1}`),
			},
		},
		{
			name:  "valid targets request",
			input: []byte(`{"id":2,"method":"targets","params":{"files":["/tmp/test.png"]}}`),
			want: Request{
				ID:     2,
				Method: "targets",
				Params: json.RawMessage(`{"files":["/tmp/test.png"]}`),
			},
		},
		{
			name:    "invalid json",
			input:   []byte(`{broken json}`),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeRequest(tt.input)
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
		want []byte
	}{
		{
			name: "response",
			msg: Response{
				ID:     1,
				Result: map[string]string{"server": "fcd"},
			},
			want: []byte(`{"id":1,"result":{"server":"fcd"}}` + "\n"),
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
			want: []byte(`{"id":2,"error":{"code":"bad_request","message":"invalid params"}}` + "\n"),
		},
		{
			name: "event",
			msg: Event{
				Name: "job.updated",
				Data: map[string]string{
					"id": "job-123",
				},
			},
			want: []byte(`{"event":"job.updated","data":{"id":"job-123"}}` + "\n"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EncodeMessage(tt.msg)
			if err != nil {
				t.Fatalf("EncodeMessage() error = %v", err)
			}
			if !bytes.Equal(got, tt.want) {
				t.Errorf("EncodeMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCodecRoundTrip(t *testing.T) {
	req := Request{
		ID:     42,
		Method: "convert",
		Params: json.RawMessage(`{"files":["/tmp/a.png"],"preset":"jpg"}`),
	}

	resp := Response{ID: req.ID, Result: map[string]string{"job": "xyz"}}
	encoded, err := EncodeMessage(resp)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	// Verify we can read it back
	var decoded Response
	if err := json.Unmarshal(bytes.TrimSpace(encoded), &decoded); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if decoded.ID != resp.ID {
		t.Errorf("round trip ID mismatch: got %d, want %d", decoded.ID, resp.ID)
	}
}
