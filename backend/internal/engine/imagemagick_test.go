package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/protocol"
)

func TestPlanOutput(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "photo.png")
	if err := os.WriteFile(source, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := PlanOutput(dir, source, "jpg", "rename")
	if err != nil {
		t.Fatal(err)
	}
	defer p.RemoveTemp()
	if filepath.Base(p.Destination) != "photo.jpg" || filepath.Dir(p.Temp) != dir {
		t.Fatalf("bad plan: %+v", p)
	}
	if _, err := os.Stat(p.Temp); err != nil {
		t.Fatal(err)
	}
}

func TestPlanOutputAcceptsMediaExtensions(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "input.source")
	if err := os.WriteFile(source, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, ext := range []string{"mp3", "aiff", "alac", "ac3", "amr", "wma"} {
		plan, err := PlanOutput(dir, source, ext, "rename")
		if err != nil {
			t.Fatalf("PlanOutput(%q) error: %v", ext, err)
		}
		plan.RemoveTemp()
	}
}

func TestPlanOutputConflictPolicies(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "photo.png")
	if err := os.WriteFile(source, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "photo.jpg")
	if err := os.WriteFile(dest, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	skip, err := PlanOutput(dir, source, "jpg", "skip")
	if err != nil || !skip.Skipped() {
		t.Fatalf("skip: %+v %v", skip, err)
	}
	rename, err := PlanOutput(dir, source, "jpg", "rename")
	if err != nil {
		t.Fatal(err)
	}
	defer rename.RemoveTemp()
	if filepath.Base(rename.Destination) != "photo (1).jpg" {
		t.Fatal(rename.Destination)
	}
	overwrite, err := PlanOutput(dir, source, "jpg", "overwrite")
	if err != nil {
		t.Fatal(err)
	}
	defer overwrite.RemoveTemp()
	if overwrite.Destination != dest {
		t.Fatal(overwrite.Destination)
	}
}

func TestJXLCommandUsesExplicitFormatSpecifiers(t *testing.T) {
	var gotArgs []string
	e := NewImageMagick(func(_ context.Context, _ string, args []string) error {
		gotArgs = args
		return nil
	})
	e.executable = "magick"
	if err := e.Convert(context.Background(), "/tmp/photo.png", formats.PNG, "/tmp/out.jxl", formats.JXL, protocol.ImageOptions{}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "png:/tmp/photo.png") || !strings.Contains(joined, "jxl:/tmp/out.jxl") {
		t.Fatalf("missing JPEG XL format specifiers: %v", gotArgs)
	}
}

func TestWebPCommandUsesFirstFrameInInputSpecifier(t *testing.T) {
	var gotArgs []string
	e := NewImageMagick(func(_ context.Context, _ string, args []string) error {
		gotArgs = args
		return nil
	})
	e.executable = "magick"
	if err := e.Convert(context.Background(), "/tmp/animation.webp", formats.WebP, "/tmp/out.jpg", formats.JPEG, protocol.ImageOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(gotArgs) < 2 {
		t.Fatalf("missing ImageMagick arguments: %v", gotArgs)
	}
	foundInput := false
	for _, arg := range gotArgs {
		if arg == "webp:/tmp/animation.webp[0]" {
			foundInput = true
		}
		if arg == "[0]" {
			t.Fatalf("unexpected standalone frame selector: %v", gotArgs)
		}
	}
	if !foundInput {
		t.Fatalf("missing first-frame input specifier: %v", gotArgs)
	}
}

func TestCommandOptionsOrderAndTargetBehavior(t *testing.T) {
	quality, maxSize := 82, 2048
	var gotArgs []string
	e := NewImageMagick(func(_ context.Context, _ string, args []string) error {
		gotArgs = args
		return nil
	})
	e.executable = "magick"
	if err := e.Convert(context.Background(), "/tmp/photo.png", formats.PNG, "/tmp/out.jpg", formats.JPEG, protocol.ImageOptions{Quality: &quality, MaxSize: &maxSize, Background: "#abc"}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{"-resize 2048x2048>", "-background #abc", "-alpha remove", "-alpha off", "-quality 82", "jpeg:/tmp/out.jpg"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %v", want, gotArgs)
		}
	}
	if strings.Index(joined, "-resize") > strings.Index(joined, "-quality") || strings.Index(joined, "-quality") > strings.Index(joined, "jpeg:/tmp/out.jpg") {
		t.Fatalf("options are out of order: %v", gotArgs)
	}

	gotArgs = nil
	if err := e.Convert(context.Background(), "/tmp/photo.png", formats.PNG, "/tmp/out.png", formats.PNG, protocol.ImageOptions{Quality: &quality, MaxSize: &maxSize, Background: "black"}); err != nil {
		t.Fatal(err)
	}
	joined = strings.Join(gotArgs, " ")
	if strings.Contains(joined, "-quality") || strings.Contains(joined, "-background") || strings.Contains(joined, "-alpha") {
		t.Fatalf("PNG should ignore lossy/background options: %v", gotArgs)
	}
}

func TestCommandConstruction(t *testing.T) {
	var gotExe string
	var gotArgs []string
	e := NewImageMagick(func(_ context.Context, exe string, args []string) error { gotExe = exe; gotArgs = args; return nil })
	e.executable = "magick"
	source := "/tmp/photo.png"
	destination := "/tmp/out.jpg"
	if err := e.Convert(context.Background(), source, formats.PNG, destination, formats.JPEG, protocol.ImageOptions{}); err != nil {
		t.Fatal(err)
	}
	if gotExe != "magick" || !strings.Contains(strings.Join(gotArgs, " "), "png:/tmp/photo.png") {
		t.Fatalf("%s %v", gotExe, gotArgs)
	}
}
