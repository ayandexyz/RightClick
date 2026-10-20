package engine

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/protocol"
)

const defaultSVGMaxBytes int64 = 16 << 20

// SVG rasterizes validated SVG documents with rsvg-convert. JPEG and WebP
// outputs are converted from a renderer-owned PNG through ImageMagick.
type SVG struct {
	executable  string
	runner      Runner
	maxBytes    int64
	imageMagick *ImageMagick
}

func NewSVG(runner Runner) *SVG {
	return &SVG{runner: runner, maxBytes: defaultSVGMaxBytes}
}

func (e *SVG) ID() string { return "svg" }

func (e *SVG) Available() (string, error) {
	if e.executable != "" {
		return e.executable, nil
	}
	path, err := exec.LookPath("rsvg-convert")
	if err != nil {
		return "", fmt.Errorf("rsvg-convert not found: %w", err)
	}
	e.executable = path
	return path, nil
}

func (e *SVG) Supports(source, target formats.Format) bool {
	if !formats.IsSVGFormat(source) {
		return false
	}
	switch target {
	case formats.PNG, formats.JPEG, formats.WebP:
		return true
	default:
		return false
	}
}

func (e *SVG) Convert(ctx context.Context, source string, sourceFormat formats.Format, destination string, target formats.Format, options protocol.ImageOptions) error {
	if !e.Supports(sourceFormat, target) {
		return fmt.Errorf("unsupported SVG conversion: %s to %s", sourceFormat, target)
	}
	maxBytes := e.maxBytes
	if maxBytes == 0 {
		maxBytes = defaultSVGMaxBytes
	}
	if err := ValidateSVG(source, maxBytes); err != nil {
		return fmt.Errorf("validate SVG: %w", err)
	}
	if err := formats.ValidatePath(destination); err != nil {
		return fmt.Errorf("unsafe output path: %w", err)
	}

	executable := e.executable
	if e.runner == nil {
		var err error
		executable, err = e.Available()
		if err != nil {
			return err
		}
	}
	output := destination
	var cleanup string
	if target != formats.PNG {
		file, err := os.CreateTemp(filepath.Dir(destination), ".file-converter-svg-*.png")
		if err != nil {
			return fmt.Errorf("create SVG intermediate: %w", err)
		}
		output = file.Name()
		cleanup = output
		if err := file.Close(); err != nil {
			_ = os.Remove(cleanup)
			return fmt.Errorf("create SVG intermediate: %w", err)
		}
		defer os.Remove(cleanup)
	}

	args := []string{"--format", "png", "--output", output, source}
	if e.runner != nil {
		if err := e.runner(ctx, executable, args); err != nil {
			return fmt.Errorf("rsvg-convert failed: %w", err)
		}
	} else if err := runCommand(ctx, executable, args); err != nil {
		return fmt.Errorf("rsvg-convert failed: %w", err)
	}
	if target == formats.PNG {
		return nil
	}

	if e.imageMagick == nil {
		e.imageMagick = NewImageMagick(e.runner)
	}
	if err := e.imageMagick.Convert(ctx, output, formats.PNG, destination, target, options); err != nil {
		return fmt.Errorf("convert rendered PNG to %s: %w", target, err)
	}
	return nil
}

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
