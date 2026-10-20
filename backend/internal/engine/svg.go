package engine

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"strings"

	"file-converter/backend/internal/formats"
)

// ValidateSVG checks that path is a bounded, self-contained SVG document.
func ValidateSVG(path string, maxBytes int64) error {
	if err := formats.ValidateInput(path); err != nil {
		return err
	}
	if maxBytes <= 0 {
		return fmt.Errorf("invalid SVG size limit: %d", maxBytes)
	}

	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open SVG: %w", err)
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return fmt.Errorf("read SVG: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return fmt.Errorf("SVG exceeds size limit of %d bytes", maxBytes)
	}

	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	depth := 0
	rootSeen := false
	rootClosed := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			if !rootSeen || depth != 0 {
				return fmt.Errorf("invalid SVG XML structure")
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("invalid SVG XML: %w", err)
		}

		switch value := token.(type) {
		case xml.Directive:
			if forbiddenDirective(string(value)) {
				return fmt.Errorf("SVG contains forbidden DTD or entity declaration")
			}
		case xml.StartElement:
			if rootClosed {
				return fmt.Errorf("multiple SVG root elements")
			}
			if !rootSeen {
				rootSeen = true
				if value.Name.Local != "svg" {
					return fmt.Errorf("SVG root element is %q, want svg", value.Name.Local)
				}
			}
			if strings.EqualFold(value.Name.Local, "script") {
				return fmt.Errorf("SVG scripts are not allowed")
			}
			for _, attr := range value.Attr {
				name := strings.ToLower(attr.Name.Local)
				if strings.HasPrefix(name, "on") || (name != "xmlns" && unsafeSVGReference(attr.Value)) {
					return fmt.Errorf("unsafe SVG attribute %q", attr.Name.Local)
				}
			}
			depth++
		case xml.EndElement:
			depth--
			if depth < 0 {
				return fmt.Errorf("invalid SVG XML structure")
			}
			if depth == 0 {
				rootClosed = true
			}
		}
	}
}

func forbiddenDirective(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(value, "doctype") || strings.HasPrefix(value, "entity")
}

func unsafeSVGReference(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, prefix := range []string{"file:", "http:", "https:", "data:"} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return strings.Contains(value, "../")
}
