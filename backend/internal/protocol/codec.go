package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// DecodeRequest decodes one non-empty JSON object from an NDJSON line.
func DecodeRequest(data []byte) (Request, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return Request{}, fmt.Errorf("decode request: expected JSON object")
	}
	var req Request
	if err := json.Unmarshal(data, &req); err != nil {
		return Request{}, fmt.Errorf("decode request: %w", err)
	}
	if req.Method == "" {
		return Request{}, fmt.Errorf("decode request: method is required")
	}
	return req, nil
}

// EncodeMessage encodes any message (Response, Error, or Event) as NDJSON.
func EncodeMessage(msg interface{}) ([]byte, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("encode message: %w", err)
	}

	// Append newline for NDJSON
	data = append(data, '\n')
	return data, nil
}
