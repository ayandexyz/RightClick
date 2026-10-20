package pdfops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// writeFixture builds a small PDF whose pages carry distinct text, so tests can
// check page order as well as page count. It uses the backend itself, which
// keeps the tests self-contained.
func writeFixture(t *testing.T, dir, name string, pages int) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if pages < 1 {
		pages = 1
	}
	// Object layout: 1 catalog, 2 page tree, 3 font, then a page object and a
	// content-stream object per page, so each page carries distinct text.
	kids := make([]string, 0, pages)
	for i := 0; i < pages; i++ {
		kids = append(kids, strconv.Itoa(pageObject+2*i)+" 0 R")
	}
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [" + strings.Join(kids, " ") + "] /Count " + strconv.Itoa(pages) + " >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	for i := 0; i < pages; i++ {
		content := "BT /F1 18 Tf 20 100 Td (page" + strconv.Itoa(i+1) + ") Tj ET"
		objects = append(objects,
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << /Font << /F1 3 0 R >> >> /Contents "+
				strconv.Itoa(pageObject+2*i+1)+" 0 R >>",
			"<< /Length "+strconv.Itoa(len(content))+" >>\nstream\n"+content+"\nendstream",
		)
	}

	var body strings.Builder
	body.WriteString("%PDF-1.7\n")
	offsets := make([]int, 0, len(objects))
	for i, object := range objects {
		offsets = append(offsets, body.Len())
		fmt.Fprintf(&body, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := body.Len()
	fmt.Fprintf(&body, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&body, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&body, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	if err := os.WriteFile(path, []byte(body.String()), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// pageObject is the object number of the first page; each page occupies two
// consecutive objects (the page and its content stream).
const pageObject = 4

func TestPDFCPUBackendPageCount(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "four.pdf", 4)
	backend := NewPDFCPUBackend()
	if backend.ID() != "pdfcpu" {
		t.Fatalf("ID = %q, want pdfcpu", backend.ID())
	}
	if err := backend.Available(); err != nil {
		t.Fatalf("Available: %v", err)
	}
	count, err := backend.PageCount(context.Background(), path)
	if err != nil {
		t.Fatalf("PageCount: %v", err)
	}
	if count != 4 {
		t.Fatalf("PageCount = %d, want 4", count)
	}
}

func TestPDFCPUBackendCollect(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "four.pdf", 4)
	backend := NewPDFCPUBackend()
	out := filepath.Join(dir, "collected.pdf")
	if err := backend.Collect(context.Background(), path, out, []int{4, 3, 2, 1}); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	count, err := backend.PageCount(context.Background(), out)
	if err != nil {
		t.Fatalf("PageCount on output: %v", err)
	}
	if count != 4 {
		t.Fatalf("collected page count = %d, want 4", count)
	}
	if got := pageText(t, out); got != "page4,page3,page2,page1" {
		t.Fatalf("collected page order = %q, want page4,page3,page2,page1", got)
	}
}

func TestPDFCPUBackendCollectSubset(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "four.pdf", 4)
	backend := NewPDFCPUBackend()
	out := filepath.Join(dir, "subset.pdf")
	if err := backend.Collect(context.Background(), path, out, []int{3, 1}); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	count, err := backend.PageCount(context.Background(), out)
	if err != nil {
		t.Fatalf("PageCount on output: %v", err)
	}
	if count != 2 {
		t.Fatalf("subset page count = %d, want 2", count)
	}
	if got := pageText(t, out); got != "page3,page1" {
		t.Fatalf("subset page order = %q, want page3,page1", got)
	}
}

func TestPDFCPUBackendMerge(t *testing.T) {
	dir := t.TempDir()
	first := writeFixture(t, dir, "first.pdf", 2)
	second := writeFixture(t, dir, "second.pdf", 3)
	backend := NewPDFCPUBackend()
	out := filepath.Join(dir, "merged.pdf")
	if err := backend.Merge(context.Background(), []string{first, second}, out); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	count, err := backend.PageCount(context.Background(), out)
	if err != nil {
		t.Fatalf("PageCount on output: %v", err)
	}
	if count != 5 {
		t.Fatalf("merged page count = %d, want 5", count)
	}
	if got := pageText(t, out); got != "page1,page2,page1,page2,page3" {
		t.Fatalf("merged page order = %q, want page1,page2,page1,page2,page3", got)
	}
}

func TestPDFCPUBackendRotate(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "four.pdf", 4)
	backend := NewPDFCPUBackend()
	out := filepath.Join(dir, "rotated.pdf")
	if err := backend.Rotate(context.Background(), path, out, nil, 90); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	count, err := backend.PageCount(context.Background(), out)
	if err != nil {
		t.Fatalf("PageCount on output: %v", err)
	}
	if count != 4 {
		t.Fatalf("rotated page count = %d, want 4", count)
	}
	// Every page must now be rotated by 90 degrees.
	for page := 1; page <= count; page++ {
		if got := pageRotation(t, out, page); got != 90 {
			t.Fatalf("page %d rotation = %d, want 90", page, got)
		}
	}
}

func TestPDFCPUBackendRotateSelectedPages(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "four.pdf", 4)
	backend := NewPDFCPUBackend()
	out := filepath.Join(dir, "rotated.pdf")
	if err := backend.Rotate(context.Background(), path, out, []int{2}, 180); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	for page, want := range map[int]int{1: 0, 2: 180, 3: 0, 4: 0} {
		if got := pageRotation(t, out, page); got != want {
			t.Fatalf("page %d rotation = %d, want %d", page, got, want)
		}
	}
}

func TestPDFCPUBackendRejectsCorruptPDF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.7\nthis is not a PDF at all\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	backend := NewPDFCPUBackend()
	if _, err := backend.PageCount(context.Background(), path); err == nil {
		t.Fatal("PageCount on a corrupt PDF should fail")
	}
	out := filepath.Join(dir, "out.pdf")
	if err := backend.Collect(context.Background(), path, out, []int{1}); err == nil {
		t.Fatal("Collect on a corrupt PDF should fail")
	}
}

func TestPDFCPUBackendEncryptedFile(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "plain.pdf", 2)
	encrypted := filepath.Join(dir, "locked.pdf")
	conf := model.NewDefaultConfiguration()
	conf.UserPW = "secret"
	conf.OwnerPW = "owner"
	if err := api.EncryptFile(path, encrypted, conf); err != nil {
		t.Skipf("pdfcpu could not encrypt a fixture: %v", err)
	}
	backend := NewPDFCPUBackend()
	_, err := backend.PageCount(context.Background(), encrypted)
	if err == nil {
		t.Fatal("an encrypted PDF should fail")
	}
	if !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("error = %v, want ErrPasswordRequired", err)
	}
}

func TestPDFCPUBackendHonoursCancelledContext(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "four.pdf", 4)
	backend := NewPDFCPUBackend()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := backend.PageCount(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("PageCount with a cancelled context = %v, want context.Canceled", err)
	}
	if err := backend.Collect(ctx, path, filepath.Join(dir, "out.pdf"), []int{1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Collect with a cancelled context = %v, want context.Canceled", err)
	}
	if err := backend.Rotate(ctx, path, filepath.Join(dir, "out2.pdf"), nil, 90); !errors.Is(err, context.Canceled) {
		t.Fatalf("Rotate with a cancelled context = %v, want context.Canceled", err)
	}
	if err := backend.Merge(ctx, []string{path, path}, filepath.Join(dir, "out3.pdf")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Merge with a cancelled context = %v, want context.Canceled", err)
	}
}

// pageText returns the text of each page in order, joined by commas. It uses
// pdftotext per page, and skips the test when the binary is missing.
func pageText(t *testing.T, path string) string {
	t.Helper()
	binary, err := exec.LookPath("pdftotext")
	if err != nil {
		t.Skip("pdftotext not installed; skipping page-order assertion")
	}
	count, err := NewPDFCPUBackend().PageCount(context.Background(), path)
	if err != nil {
		t.Fatalf("PageCount: %v", err)
	}
	var parts []string
	for page := 1; page <= count; page++ {
		out, err := exec.Command(binary, "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), path, "-").Output()
		if err != nil {
			t.Fatalf("pdftotext page %d: %v", page, err)
		}
		parts = append(parts, strings.TrimSpace(string(out)))
	}
	return strings.Join(parts, ",")
}

// pageRotation reads one page's rotation with pdfinfo, skipping the test when
// the binary is missing. pdfcpu has no public API for reading a page rotation.
func pageRotation(t *testing.T, path string, page int) int {
	t.Helper()
	binary, err := exec.LookPath("pdfinfo")
	if err != nil {
		t.Skip("pdfinfo not installed; skipping rotation assertion")
	}
	out, err := exec.Command(binary, "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), path).Output()
	if err != nil {
		t.Fatalf("pdfinfo page %d: %v", page, err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "Page") || !strings.Contains(line, "rot:") {
			continue
		}
		value := line[strings.LastIndex(line, "rot:")+len("rot:"):]
		rotation, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			t.Fatalf("parse rotation from %q: %v", line, err)
		}
		if rotation < 0 {
			rotation += 360
		}
		return rotation
	}
	t.Fatalf("pdfinfo reported no rotation for page %d of %s:\n%s", page, path, out)
	return 0
}

// A file pdfcpu cannot read is reported as damaged, not as an unexplained
// failure: clients choose a different screen for the two. The cases are the
// real shapes damage takes, because pdfcpu words each one differently.
func TestPDFCPUPageCountClassifiesUnreadableFilesAsInvalid(t *testing.T) {
	dir := t.TempDir()
	good := writeFixture(t, dir, "good.pdf", 3)
	body, err := os.ReadFile(good)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"truncated":   body[:len(body)/2],
		"header only": []byte("%PDF-1.7\n"),
		"garbage":     []byte("%PDF-1.7\nthis is not a PDF at all\n"),
		"zero filled": append([]byte("%PDF-1.7\n"), make([]byte, 512)...),
	}
	backend := NewPDFCPUBackend()
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name+".pdf")
			if err := os.WriteFile(path, content, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := backend.PageCount(context.Background(), path)
			if err == nil {
				t.Skip("pdfcpu repaired this file; nothing to classify")
			}
			if !errors.Is(err, ErrInvalidPDF) {
				t.Fatalf("err = %v, want ErrInvalidPDF", err)
			}
			if ErrorCode(err) != "invalid_pdf" {
				t.Fatalf("ErrorCode = %q, want invalid_pdf", ErrorCode(err))
			}
			if err.Error() != ErrInvalidPDF.Error() {
				t.Fatalf("message = %q, want the short user-facing sentence", err.Error())
			}
		})
	}
}

// A request that is cancelled is not a verdict on the file.
func TestPDFCPUPageCountCancellationIsNotInvalid(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "good.pdf", 2)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewPDFCPUBackend().PageCount(ctx, path)
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrInvalidPDF) {
		t.Fatalf("err = %v, want context.Canceled and not ErrInvalidPDF", err)
	}
}

// A locked file stays locked: it must not be reclassified as damaged.
func TestPDFCPUPageCountKeepsLockedDistinctFromInvalid(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "plain.pdf", 2)
	locked := filepath.Join(dir, "locked.pdf")
	conf := model.NewDefaultConfiguration()
	conf.UserPW, conf.OwnerPW = "secret", "owner"
	if err := api.EncryptFile(path, locked, conf); err != nil {
		t.Skipf("pdfcpu could not encrypt a fixture: %v", err)
	}
	_, err := NewPDFCPUBackend().PageCount(context.Background(), locked)
	if !errors.Is(err, ErrPasswordRequired) || errors.Is(err, ErrInvalidPDF) {
		t.Fatalf("err = %v, want ErrPasswordRequired only", err)
	}
}
