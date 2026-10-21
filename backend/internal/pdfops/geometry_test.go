package pdfops

import (
	"context"
	"math"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var boxLine = regexp.MustCompile(`Page\s+(\d+)\s+(\w+Box):\s+([-\d.]+)\s+([-\d.]+)\s+([-\d.]+)\s+([-\d.]+)`)
var sizeLine = regexp.MustCompile(`Page\s+(\d+)\s+size:\s+([\d.]+) x ([\d.]+) pts`)

// cropBoxes returns each page's CropBox as x1, y1, x2, y2.
func cropBoxes(t *testing.T, path string, pages int) map[int][4]float64 {
	t.Helper()
	out, err := exec.Command("pdfinfo", "-box", "-f", "1", "-l", strconv.Itoa(pages), path).Output()
	if err != nil {
		t.Fatalf("pdfinfo -box: %v", err)
	}
	boxes := map[int][4]float64{}
	for _, m := range boxLine.FindAllStringSubmatch(string(out), -1) {
		if m[2] != "CropBox" {
			continue
		}
		page, _ := strconv.Atoi(m[1])
		var box [4]float64
		for i := range box {
			box[i], _ = strconv.ParseFloat(m[3+i], 64)
		}
		boxes[page] = box
	}
	return boxes
}

// pageDims returns each page's size as the reader sees it.
func pageDims(t *testing.T, path string, pages int) map[int][2]float64 {
	t.Helper()
	out, err := exec.Command("pdfinfo", "-f", "1", "-l", strconv.Itoa(pages), path).Output()
	if err != nil {
		t.Fatalf("pdfinfo: %v", err)
	}
	dims := map[int][2]float64{}
	for _, m := range sizeLine.FindAllStringSubmatch(string(out), -1) {
		page, _ := strconv.Atoi(m[1])
		w, _ := strconv.ParseFloat(m[2], 64)
		h, _ := strconv.ParseFloat(m[3], 64)
		dims[page] = [2]float64{w, h}
	}
	return dims
}

func near(a, b float64) bool { return math.Abs(a-b) <= 1 }

func TestCropSetsTheCropBoxToThePageLessTheMargins(t *testing.T) {
	requireTool(t, "pdfinfo")
	in := buildTextPDF(t, 3) // Letter, 612 x 792
	be := NewPDFCPUBackend()
	mm := func(v float64) float64 { return v * 72 / 25.4 }
	for _, tc := range []struct {
		name   string
		params CropParams
		want   [4]float64 // x1, y1, x2, y2
	}{
		{"mm", CropParams{Unit: "mm", Top: 10, Right: 20, Bottom: 30, Left: 40}, [4]float64{mm(40), mm(30), 612 - mm(20), 792 - mm(10)}},
		{"pt", CropParams{Unit: "pt", Top: 10, Right: 20, Bottom: 30, Left: 40}, [4]float64{40, 30, 592, 782}},
		{"percent", CropParams{Unit: "percent", Top: 10, Right: 10, Bottom: 10, Left: 10}, [4]float64{61.2, 79.2, 550.8, 712.8}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out.pdf")
			if err := be.Crop(context.Background(), in, out, tc.params); err != nil {
				t.Fatal(err)
			}
			boxes := cropBoxes(t, out, 3)
			if len(boxes) != 3 {
				t.Fatalf("read %d crop boxes, want 3", len(boxes))
			}
			for page, got := range boxes {
				for i := range got {
					if !near(got[i], tc.want[i]) {
						t.Fatalf("page %d crop box = %v, want %v", page, got, tc.want)
					}
				}
			}
		})
	}
}

func TestCropOnlyTouchesTheSelectedPages(t *testing.T) {
	requireTool(t, "pdfinfo")
	in := buildTextPDF(t, 3)
	out := filepath.Join(t.TempDir(), "out.pdf")
	if err := NewPDFCPUBackend().Crop(context.Background(), in, out, CropParams{Unit: "pt", Top: 50, Pages: "2"}); err != nil {
		t.Fatal(err)
	}
	dims := pageDims(t, out, 3)
	if !near(dims[1][1], 792) || !near(dims[2][1], 742) || !near(dims[3][1], 792) {
		t.Fatalf("page heights = %v, want only page 2 cropped to 742", dims)
	}
}

func TestCropRefusesMarginsThatLeaveNoPageAndNamesThePage(t *testing.T) {
	requireTool(t, "pdfinfo")
	in := buildTextPDF(t, 2)
	be := NewPDFCPUBackend()
	err := be.Crop(context.Background(), in, filepath.Join(t.TempDir(), "o.pdf"), CropParams{Unit: "pt", Top: 400, Bottom: 400})
	if err == nil || !strings.Contains(err.Error(), "page 1") {
		t.Fatalf("err = %v, want a refusal naming page 1", err)
	}
	for name, p := range map[string]CropParams{
		"negative":   {Unit: "mm", Top: -1},
		"no margins": {Unit: "mm"},
		"bad unit":   {Unit: "cm", Top: 1},
		"percent":    {Unit: "percent", Left: 60, Right: 40},
	} {
		if err := be.Crop(context.Background(), in, filepath.Join(t.TempDir(), "o.pdf"), p); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// On a page that carries /Rotate the margins apply to the page the reader
// sees: the "top" is the visual top, whatever the stored orientation.
func TestCropOnARotatedPageUsesTheVisualTop(t *testing.T) {
	requireTool(t, "pdfinfo")
	requireTool(t, "qpdf")
	in := buildTextPDF(t, 1)
	dir := t.TempDir()
	rotated, out := filepath.Join(dir, "rot.pdf"), filepath.Join(dir, "out.pdf")
	be := NewPDFCPUBackend()
	if err := be.Rotate(context.Background(), in, rotated, []int{1}, 90); err != nil {
		t.Fatal(err)
	}
	if err := be.Crop(context.Background(), rotated, out, CropParams{Unit: "pt", Top: 100}); err != nil {
		t.Fatal(err)
	}
	// A visually landscape 792 x 612 page loses 100 points of height.
	if got := pageDims(t, out, 1)[1]; !near(got[0], 792) || !near(got[1], 512) {
		t.Fatalf("size = %v, want 792 x 512", got)
	}
}

func TestResizeFitsPagesInsideTheTarget(t *testing.T) {
	requireTool(t, "pdfinfo")
	in := buildTextPDF(t, 2)
	be := NewPDFCPUBackend()
	for to, want := range map[string][2]float64{"A4": {595, 842}, "A5": {420, 595}, "Letter": {612, 792}, "Legal": {612, 1008}} {
		out := filepath.Join(t.TempDir(), "out.pdf")
		if err := be.Resize(context.Background(), in, out, ResizeParams{To: to, Fit: "scale"}); err != nil {
			t.Fatalf("%s: %v", to, err)
		}
		dims := pageDims(t, out, 2)
		if len(dims) != 2 {
			t.Fatalf("read %d page sizes, want 2", len(dims))
		}
		for page, got := range dims {
			if !near(got[0], want[0]) || !near(got[1], want[1]) {
				t.Fatalf("%s page %d = %v, want %v", to, page, got, want)
			}
		}
	}
}

func TestResizeOnlyTouchesTheSelectedPagesAndKeepsText(t *testing.T) {
	requireTool(t, "pdfinfo")
	requireTool(t, "pdftotext")
	in := buildTextPDF(t, 2)
	out := filepath.Join(t.TempDir(), "out.pdf")
	if err := NewPDFCPUBackend().Resize(context.Background(), in, out, ResizeParams{To: "A5", Pages: "2"}); err != nil {
		t.Fatal(err)
	}
	dims := pageDims(t, out, 2)
	if !near(dims[1][0], 612) || !near(dims[2][0], 420) {
		t.Fatalf("sizes = %v, want only page 2 resized", dims)
	}
	if got := stampPageText(t, out, 2); !strings.Contains(strings.ReplaceAll(got, " ", ""), "PAGE2") {
		t.Fatalf("page 2 text = %q", got)
	}
}

func TestResizeRejectsModesPDFCPUCannotDo(t *testing.T) {
	in := buildTextPDF(t, 1)
	for _, p := range []ResizeParams{{To: "A4", Fit: "crop"}, {To: "A4", Fit: "pad"}, {To: "B5"}, {}} {
		if err := NewPDFCPUBackend().Resize(context.Background(), in, filepath.Join(t.TempDir(), "o.pdf"), p); err == nil {
			t.Errorf("%+v was accepted", p)
		}
	}
}
