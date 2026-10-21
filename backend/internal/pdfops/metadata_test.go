package pdfops

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestPDFCPUMetadataReadsInfoProperties(t *testing.T) {
	backend := NewPDFCPUBackend()
	dir := t.TempDir()
	input := writeMetadataPDF(t, dir, "input.pdf")
	if err := backend.SetMetadata(context.Background(), input, filepath.Join(dir, "with-info.pdf"), MetadataParams{
		Title: "Title", Author: "Author", Subject: "Subject", Keywords: "one, two",
	}); err != nil {
		t.Fatalf("seed metadata: %v", err)
	}
	metadata, err := backend.Metadata(context.Background(), filepath.Join(dir, "with-info.pdf"))
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if metadata.Title != "Title" || metadata.Author != "Author" || metadata.Subject != "Subject" || metadata.Keywords != "one, two" {
		t.Fatalf("metadata = %+v", metadata)
	}
}

func TestPDFCPUMetadataEditsStandardAndCustomProperties(t *testing.T) {
	backend := NewPDFCPUBackend()
	dir := t.TempDir()
	input := writeMetadataPDF(t, dir, "in.pdf")
	output := filepath.Join(dir, "out.pdf")
	err := backend.SetMetadata(context.Background(), input, output, MetadataParams{
		Title: "New title", Author: "New author", Subject: "New subject", Keywords: "one, two",
		Custom: map[string]string{"Department": "Research"},
	})
	if err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}
	metadata, err := backend.Metadata(context.Background(), output)
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if metadata.Title != "New title" || metadata.Custom["Department"] != "Research" {
		t.Fatalf("metadata = %+v", metadata)
	}
}

func writeMetadataPDF(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 10 10] /Contents 4 0 R /Resources << >> >>",
		"<< /Length 0 >>\nstream\n\nendstream",
	}
	data := []byte("%PDF-1.7\n")
	offsets := []int{0}
	for i, object := range objects {
		offsets = append(offsets, len(data))
		data = append(data, []byte(fmt.Sprintf("%d 0 obj\n%s\nendobj\n", i+1, object))...)
	}
	xref := len(data)
	data = append(data, []byte(fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", len(objects)+1))...)
	for _, offset := range offsets[1:] {
		data = append(data, []byte(fmt.Sprintf("%010d 00000 n \n", offset))...)
	}
	data = append(data, []byte(fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref))...)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMetadataOperationIsCatalogued(t *testing.T) {
	operation, ok := Lookup("pdf-metadata")
	if !ok {
		t.Fatal("pdf-metadata is not catalogued")
	}
	if err := operation.Validate([]string{"/tmp/input.pdf"}, Params{Metadata: MetadataParams{Title: "Title"}}); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestPDFCPUStripMetadataRemovesInfoFields(t *testing.T) {
	dir := t.TempDir()
	backend := NewPDFCPUBackend()
	input := writeMetadataPDF(t, dir, "input.pdf")
	withMetadata := filepath.Join(dir, "with-metadata.pdf")
	if err := backend.SetMetadata(context.Background(), input, withMetadata, MetadataParams{
		Title: "Title", Author: "Author", Subject: "Subject", Keywords: "one, two",
	}); err != nil {
		t.Fatalf("seed metadata: %v", err)
	}
	output := filepath.Join(dir, "clean.pdf")
	if err := backend.StripMetadata(context.Background(), withMetadata, output); err != nil {
		t.Fatalf("StripMetadata: %v", err)
	}
	metadata, err := backend.Metadata(context.Background(), output)
	if err != nil {
		t.Fatalf("read stripped metadata: %v", err)
	}
	for name, value := range map[string]string{
		"title": metadata.Title, "author": metadata.Author, "subject": metadata.Subject,
		"keywords": metadata.Keywords, "creator": metadata.Creator, "producer": metadata.Producer,
	} {
		if value != "" {
			t.Errorf("%s = %q, want empty", name, value)
		}
	}
}

func TestStripMetadataOperationIsCatalogued(t *testing.T) {
	op, ok := Lookup("pdf-strip-metadata")
	if !ok {
		t.Fatal("pdf-strip-metadata is not catalogued")
	}
	if err := op.Validate([]string{"/tmp/input.pdf"}, Params{}); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
