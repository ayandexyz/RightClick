package protocol

import (
	"encoding/json"
	"fmt"
)

// DecodeRequest reads a single NDJSON message and decodes it as a Request.
func DecodeRequest(data []byte) (Request, error) {
	var req Request
	if err := json.Unmarshal(data, &req); err != nil {
		return Request{}, fmt.Errorf("decode request: %w", err)
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
