package protocol

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// DecodeRequest reads a single NDJSON message and decodes it as a Request.
func DecodeRequest(r io.Reader) (*Request, error) {
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}

	var req Request
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		return nil, fmt.Errorf("decode request: %w", err)
	}

	return &req, nil
}

// EncodeMessage encodes any message (Response, Error, or Event) as NDJSON.
func EncodeMessage(w io.Writer, msg interface{}) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("encode message: %w", err)
	}

	if _, err := w.Write(data); err != nil {
		return err
	}
	if _, err := w.Write([]byte("\n")); err != nil {
		return err
	}

	return nil
}
