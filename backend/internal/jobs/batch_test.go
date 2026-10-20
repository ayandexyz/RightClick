package jobs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/protocol"
)

const pngBytes = "\x89PNG\r\n\x1a\nfixture"
const jpegBytes = "\xff\xd8\xff\xe0fixture"

func writeFixture(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func runBatch(t *testing.T, req SubmitRequest) protocol.Job {
	t.Helper()
	m := NewManager(&fakeConverter{})
	defer m.Close()
	events := make(chan protocol.Event, 64)
	req.Events = func(e protocol.Event) { events <- e }
	if req.Conflict == "" {
		req.Conflict = protocol.ConflictRename
	}
	if _, err := m.Submit(req); err != nil {
		t.Fatal(err)
	}
	return waitJob(t, events)
}

func fileStates(job protocol.Job) map[string]protocol.FileJob {
	out := map[string]protocol.FileJob{}
	for _, f := range job.Files {
		out[filepath.Base(f.Source)] = f
	}
	return out
}

func TestSubmitConvertsRecursiveFolderMirroringStructureIntoOutputDir(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "a.png"), pngBytes)
	writeFixture(t, filepath.Join(root, "sub", "b.png"), pngBytes)
	writeFixture(t, filepath.Join(root, "sub", "deep", "c.png"), pngBytes)
	out := t.TempDir()

	job := runBatch(t, SubmitRequest{Files: []string{root}, Preset: "image-to-jpeg", OutputDir: out, Recursive: true})
	if job.State != "completed" || len(job.Files) != 3 {
		t.Fatalf("job = %+v", job)
	}
	for _, want := range []string{"a.jpg", "sub/b.jpg", "sub/deep/c.jpg"} {
		if _, err := os.Stat(filepath.Join(out, want)); err != nil {
			t.Errorf("missing mirrored output %s: %v", want, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "a.jpg")); !os.IsNotExist(err) {
		t.Fatal("output leaked next to the source despite an output dir")
	}
}

func TestSubmitFolderWithoutRecursiveConvertsOnlyTopLevel(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "a.png"), pngBytes)
	writeFixture(t, filepath.Join(root, "sub", "b.png"), pngBytes)
	job := runBatch(t, SubmitRequest{Files: []string{root}, Preset: "image-to-jpeg"})
	if len(job.Files) != 1 || job.Files[0].State != "done" {
		t.Fatalf("job = %+v", job)
	}
	if _, err := os.Stat(filepath.Join(root, "a.jpg")); err != nil {
		t.Fatal("output should sit next to its source without an output dir:", err)
	}
}

func TestSubmitSkipsUnsupportedFilesFoundInFoldersWithAReason(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "a.png"), pngBytes)
	writeFixture(t, filepath.Join(root, "notes.txt"), "just text")
	writeFixture(t, filepath.Join(root, "already.jpg"), jpegBytes)
	writeFixture(t, filepath.Join(root, "[odd].png"), pngBytes)

	job := runBatch(t, SubmitRequest{Files: []string{root}, Preset: "image-to-jpeg"})
	states := fileStates(job)
	if job.State != "completed" {
		t.Fatalf("skipped folder entries must not fail the job: %+v", job)
	}
	if states["a.png"].State != "done" {
		t.Fatalf("a.png: %+v", states["a.png"])
	}
	for _, name := range []string{"notes.txt", "already.jpg", "[odd].png"} {
		f := states[name]
		if f.State != "skipped" || f.Notice == "" || f.Error != nil {
			t.Errorf("%s should be skipped with a notice: %+v", name, f)
		}
	}
}

func TestSubmitStillFailsExplicitUnsupportedFile(t *testing.T) {
	dir := t.TempDir()
	good := writeFixture(t, filepath.Join(dir, "a.png"), pngBytes)
	bad := writeFixture(t, filepath.Join(dir, "notes.txt"), "just text")
	job := runBatch(t, SubmitRequest{Files: []string{good, bad}, Preset: "image-to-jpeg"})
	states := fileStates(job)
	if states["notes.txt"].State != "failed" || job.State != "completed_with_errors" {
		t.Fatalf("job = %+v", job)
	}
}

func TestSubmitKeepsExplicitFileOrderAndSortsFolderContents(t *testing.T) {
	base := t.TempDir()
	late := writeFixture(t, filepath.Join(base, "z.png"), pngBytes)
	root := filepath.Join(base, "folder")
	writeFixture(t, filepath.Join(root, "b.png"), pngBytes)
	writeFixture(t, filepath.Join(root, "a.png"), pngBytes)
	job := runBatch(t, SubmitRequest{Files: []string{late, root}, Preset: "image-to-jpeg", OutputDir: t.TempDir()})
	var got []string
	for _, f := range job.Files {
		got = append(got, filepath.Base(f.Source))
	}
	if want := []string{"z.png", "a.png", "b.png"}; len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestSubmitRejectsFolderWithNothingToConvert(t *testing.T) {
	m := NewManager(&fakeConverter{})
	defer m.Close()
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, ".hidden.png"), pngBytes)
	_, err := m.Submit(SubmitRequest{Files: []string{root}, Preset: "image-to-jpeg"})
	if !errors.Is(err, formats.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestSubmitStillRejectsMissingExplicitFile(t *testing.T) {
	m := NewManager(&fakeConverter{})
	defer m.Close()
	_, err := m.Submit(SubmitRequest{Files: []string{filepath.Join(t.TempDir(), "missing.png")}, Preset: "image-to-jpeg"})
	if !errors.Is(err, formats.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}
