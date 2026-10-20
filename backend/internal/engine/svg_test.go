package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateSVGAcceptsSafeDocument(t *testing.T) {
	path := writeSVG(t, `<svg xmlns="http://www.w3.org/2000/svg"><rect width="10" height="10"/></svg>`)
	if err := ValidateSVG(path, 1024); err != nil {
		t.Fatalf("ValidateSVG() error = %v", err)
	}
}

func TestValidateSVGRejectsInvalidPaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.svg")
	if err := ValidateSVG(path, 1024); err == nil {
		t.Fatal("missing file accepted")
	}
	if err := ValidateSVG("relative.svg", 1024); err == nil {
		t.Fatal("relative path accepted")
	}
	if err := ValidateSVG(t.TempDir(), 1024); err == nil {
		t.Fatal("directory accepted")
	}
}

func TestValidateSVGRejectsFilesOverLimit(t *testing.T) {
	path := writeSVG(t, `<svg xmlns="http://www.w3.org/2000/svg"></svg>`)
	if err := ValidateSVG(path, 10); err == nil {
		t.Fatal("oversized SVG accepted")
	}
}

func TestValidateSVGRejectsMalformedXML(t *testing.T) {
	path := writeSVG(t, `<svg><rect></svg>`)
	if err := ValidateSVG(path, 1024); err == nil {
		t.Fatal("malformed SVG accepted")
	}
}

func TestValidateSVGRejectsUnsafeConstructs(t *testing.T) {
	cases := []string{
		`<!DOCTYPE svg><svg/>`,
		`<!DOCTYPE svg [<!ENTITY xxe "file:///etc/passwd">]><svg>&xxe;</svg>`,
		`<svg><script>alert(1)</script></svg>`,
		`<svg onload="alert(1)"/>`,
		`<svg><image href="file:///tmp/image.png"/></svg>`,
		`<svg><image href="http://example.com/image.png"/></svg>`,
		`<svg><image href="https://example.com/image.png"/></svg>`,
		`<svg><image href="data:image/png;base64,AAAA"/></svg>`,
		`<svg><image href="../image.png"/></svg>`,
	}
	for _, document := range cases {
		t.Run(strings.ReplaceAll(document[:min(len(document), 24)], " ", "_"), func(t *testing.T) {
			if err := ValidateSVG(writeSVG(t, document), 4096); err == nil {
				t.Fatalf("unsafe SVG accepted: %s", document)
			}
		})
	}
}

func writeSVG(t *testing.T, document string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.svg")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
