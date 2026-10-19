package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/protocol"
)

type Runner func(context.Context, string, []string) error

type ImageMagick struct {
	executable string
	runner     Runner
}

func NewImageMagick(runner Runner) *ImageMagick { return &ImageMagick{runner: runner} }

func (e *ImageMagick) ID() string { return "imagemagick" }

func (e *ImageMagick) Supports(source, target formats.Format) bool {
	return isImageFormat(source) && isImageFormat(target)
}

func isImageFormat(format formats.Format) bool {
	return format == formats.PNG || format == formats.JPEG || format == formats.WebP
}

func (e *ImageMagick) Available() (string, error) {
	if e.executable != "" {
		return e.executable, nil
	}
	for _, name := range []string{"magick", "convert"} {
		if path, err := exec.LookPath(name); err == nil {
			e.executable = path
			return path, nil
		}
	}
	return "", fmt.Errorf("imagemagick not found")
}

func (e *ImageMagick) Convert(ctx context.Context, source string, sourceFormat formats.Format, destination string, target formats.Format) error {
	executable, err := e.Available()
	if err != nil {
		return err
	}
	if strings.ContainsAny(filepath.Base(source), "[]:") || strings.HasPrefix(filepath.Base(source), "@") {
		return fmt.Errorf("unsafe image path")
	}
	input := string(sourceFormat) + ":" + source
	if sourceFormat == formats.WebP {
		input += "[0]"
	}
	args := []string{"-limit", "memory", "512MiB", "-limit", "map", "1GiB", "-limit", "disk", "2GiB", "-limit", "time", "300", input}
	if target == formats.JPEG {
		args = append(args, "-auto-orient", "-background", "white", "-alpha", "remove", "-alpha", "off")
	} else {
		args = append(args, "-auto-orient")
	}
	args = append(args, string(target)+":"+destination)
	if e.runner != nil {
		return e.runner(ctx, executable, args)
	}
	return exec.CommandContext(ctx, executable, args...).Run()
}

type OutputPlan struct {
	Temp, Destination string
	skip              bool
}

func PlanOutput(dir, source string, targetExt string, conflict protocol.ConflictPolicy) (OutputPlan, error) {
	if !filepath.IsAbs(source) {
		return OutputPlan{}, fmt.Errorf("source path must be absolute")
	}
	if unsafeBase(filepath.Base(source)) {
		return OutputPlan{}, fmt.Errorf("unsafe image path")
	}
	if targetExt != "png" && targetExt != "jpg" && targetExt != "webp" {
		return OutputPlan{}, fmt.Errorf("unsupported target extension: %s", targetExt)
	}
	if dir == "" {
		dir = filepath.Dir(source)
	}
	if !filepath.IsAbs(dir) {
		return OutputPlan{}, fmt.Errorf("output directory must be absolute")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return OutputPlan{}, err
	}
	base := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source)) + "." + targetExt
	destination := filepath.Join(dir, base)
	if filepath.Clean(destination) == filepath.Clean(source) {
		return OutputPlan{}, fmt.Errorf("output would overwrite source")
	}
	if _, err := os.Stat(destination); err == nil {
		switch conflict {
		case "skip":
			return OutputPlan{Destination: destination, skip: true}, nil
		case "overwrite":
		default:
			for i := 1; ; i++ {
				candidate := filepath.Join(dir, strings.TrimSuffix(base, "."+targetExt)+" ("+strconv.Itoa(i)+")."+targetExt)
				if _, err := os.Stat(candidate); os.IsNotExist(err) {
					destination = candidate
					break
				}
			}
		}
	} else if !os.IsNotExist(err) {
		return OutputPlan{}, err
	}
	tempFile, err := os.CreateTemp(dir, ".file-converter-*")
	if err != nil {
		return OutputPlan{}, err
	}
	temp := tempFile.Name()
	if err := tempFile.Close(); err != nil {
		return OutputPlan{}, err
	}
	return OutputPlan{Temp: temp, Destination: destination}, nil
}
func (p OutputPlan) Commit() (string, error) {
	if p.skip {
		return "", nil
	}
	if err := os.Rename(p.Temp, p.Destination); err != nil {
		return "", err
	}
	return p.Destination, nil
}
func (p OutputPlan) RemoveTemp() {
	if p.Temp != "" {
		_ = os.Remove(p.Temp)
	}
}
func (p OutputPlan) Skipped() bool { return p.skip }

func unsafeBase(base string) bool {
	return strings.ContainsAny(base, "[]:") || strings.HasPrefix(base, "@")
}
