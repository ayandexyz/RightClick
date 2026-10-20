package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/protocol"
)

type Runner func(context.Context, string, []string) error

type ImageMagick struct {
	executable string
	runner     Runner
	readCaps   map[formats.Format]bool
	writeCaps  map[formats.Format]bool
}

func NewImageMagick(runner Runner) *ImageMagick { return &ImageMagick{runner: runner} }

func (e *ImageMagick) ID() string { return "imagemagick" }

func (e *ImageMagick) Supports(source, target formats.Format) bool {
	if !isImageFormat(source) || !isImageFormat(target) {
		return false
	}
	// Read capability is checked by Convert so recognized-but-unreadable inputs
	// produce a useful error instead of looking like an unknown route.
	return e.writeCaps == nil || e.writeCaps[target]
}

func (e *ImageMagick) Capability(format formats.Format) (bool, bool) {
	return e.readCaps[format], e.writeCaps[format]
}

func (e *ImageMagick) ProbeCapabilities() error {
	executable, err := e.Available()
	if err != nil {
		e.readCaps, e.writeCaps = map[formats.Format]bool{}, map[formats.Format]bool{}
		return err
	}
	cmd := exec.Command(executable, "-list", "format")
	out, err := cmd.Output()
	if err != nil {
		return err
	}
	readCaps, writeCaps, parsed := parseCapabilities(string(out))
	if !parsed {
		for _, format := range []formats.Format{formats.PNG, formats.JPEG, formats.WebP} {
			readCaps[format], writeCaps[format] = true, true
		}
	}
	e.readCaps, e.writeCaps = readCaps, writeCaps
	return nil
}

func parseCapabilities(output string) (map[formats.Format]bool, map[formats.Format]bool, bool) {
	readCaps, writeCaps := map[formats.Format]bool{}, map[formats.Format]bool{}
	parsed := false
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		name, mode := strings.ToLower(strings.TrimRight(fields[0], "*")), fields[2]
		if len(mode) < 2 || (mode[0] != 'r' && mode[0] != '-') || (mode[1] != 'w' && mode[1] != '-') {
			continue
		}
		parsed = true
		var format formats.Format
		switch name {
		case "png":
			format = formats.PNG
		case "jpeg", "jpg":
			format = formats.JPEG
		case "webp":
			format = formats.WebP
		case "jxl":
			format = formats.JXL
		case "avif":
			format = formats.AVIF
		case "gif":
			format = formats.GIF
		case "bmp":
			format = formats.BMP
		case "tiff":
			format = formats.TIFF
		case "ico":
			format = formats.ICO
		case "heic":
			format = formats.HEIC
		default:
			continue
		}
		if format == "" {
			continue
		}
		if mode[0] == 'r' {
			readCaps[format] = true
		}
		if mode[1] == 'w' {
			writeCaps[format] = true
		}
	}
	return readCaps, writeCaps, parsed
}

func isImageFormat(format formats.Format) bool {
	switch format {
	case formats.PNG, formats.JPEG, formats.WebP, formats.JXL, formats.AVIF, formats.GIF, formats.BMP, formats.TIFF, formats.ICO, formats.HEIC:
		return true
	default:
		return false
	}
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

func (e *ImageMagick) Convert(ctx context.Context, source string, sourceFormat formats.Format, destination string, target formats.Format, options protocol.ImageOptions) error {
	executable, err := e.Available()
	if err != nil {
		return err
	}
	if strings.ContainsAny(filepath.Base(source), "[]:") || strings.HasPrefix(filepath.Base(source), "@") {
		return fmt.Errorf("unsafe image path")
	}
	if e.readCaps != nil && !e.readCaps[sourceFormat] {
		return fmt.Errorf("%s is not supported by this ImageMagick build", strings.ToUpper(string(sourceFormat)))
	}
	if e.writeCaps != nil && !e.writeCaps[target] {
		return fmt.Errorf("%s is not supported by this ImageMagick build", strings.ToUpper(string(target)))
	}
	input := string(sourceFormat) + ":" + source
	if (sourceFormat == formats.WebP || sourceFormat == formats.GIF || sourceFormat == formats.TIFF || sourceFormat == formats.ICO) && target != formats.GIF && target != formats.WebP {
		input += "[0]"
	}
	args := []string{"-limit", "memory", "512MiB", "-limit", "map", "1GiB", "-limit", "disk", "2GiB", "-limit", "time", "300", input, "-auto-orient"}
	maxSize := 0
	if options.MaxSize != nil {
		maxSize = *options.MaxSize
	}
	if target == formats.ICO && (maxSize == 0 || maxSize > 256) {
		maxSize = 256
	}
	if maxSize > 0 {
		args = append(args, "-resize", fmt.Sprintf("%dx%d>", maxSize, maxSize))
	}
	if target == formats.JPEG || target == formats.BMP || target == formats.GIF {
		background := options.Background
		if background == "" {
			background = "white"
		}
		if sourceFormat == formats.GIF && target == formats.GIF {
			args = append(args, "-coalesce")
		}
		args = append(args, "-background", background, "-alpha", "remove", "-alpha", "off")
	}
	if options.Quality != nil && target != formats.PNG && target != formats.GIF && target != formats.BMP && target != formats.TIFF && target != formats.ICO {
		args = append(args, "-quality", strconv.Itoa(*options.Quality))
	}
	args = append(args, string(target)+":"+destination)
	if e.runner != nil {
		return e.runner(ctx, executable, args)
	}
	cmd := exec.Command(executable, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	select {
	case err := <-wait:
		return err
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-wait
		return ctx.Err()
	}
}

type OutputPlan struct {
	Temp, Destination string
	backup            string
	skip              bool
}

func PlanOutput(dir, source string, targetExt string, conflict protocol.ConflictPolicy) (OutputPlan, error) {
	return planOutput(dir, source, targetExt, "", conflict)
}

func PlanOutputPath(dir, source string, targetExt string, page int, conflict protocol.ConflictPolicy) (OutputPlan, error) {
	if page < 1 {
		return OutputPlan{}, fmt.Errorf("page number must be positive")
	}
	return planOutput(dir, source, targetExt, "-"+strconv.Itoa(page), conflict)
}

func planOutput(dir, source string, targetExt, suffix string, conflict protocol.ConflictPolicy) (OutputPlan, error) {
	if !filepath.IsAbs(source) {
		return OutputPlan{}, fmt.Errorf("source path must be absolute")
	}
	if unsafeBase(filepath.Base(source)) {
		return OutputPlan{}, fmt.Errorf("unsafe image path")
	}
	if !supportedOutputExtension(targetExt) {
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
	base := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source)) + suffix + "." + targetExt
	destination := filepath.Join(dir, base)
	if filepath.Clean(destination) == filepath.Clean(source) {
		return OutputPlan{}, fmt.Errorf("output would overwrite source")
	}
	existing := false
	if _, err := os.Stat(destination); err == nil {
		existing = true
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
	plan := OutputPlan{Temp: temp, Destination: destination}
	if existing && conflict == protocol.ConflictOverwrite {
		backup, err := os.CreateTemp(dir, ".file-converter-backup-*")
		if err != nil {
			_ = os.Remove(temp)
			return OutputPlan{}, err
		}
		backupPath := backup.Name()
		if err := backup.Close(); err != nil {
			_ = os.Remove(temp)
			_ = os.Remove(backupPath)
			return OutputPlan{}, err
		}
		if err := os.Remove(backupPath); err != nil {
			_ = os.Remove(temp)
			return OutputPlan{}, err
		}
		if err := os.Rename(destination, backupPath); err != nil {
			_ = os.Remove(temp)
			return OutputPlan{}, err
		}
		plan.backup = backupPath
	}
	return plan, nil
}
func (p OutputPlan) Commit() (string, error) {
	if p.skip {
		return "", nil
	}
	// A hard link makes the file appear as "created"; Nautilus ignores a rename from a hidden temp name until refresh.
	if err := os.Link(p.Temp, p.Destination); err == nil {
		_ = os.Remove(p.Temp)
		return p.Destination, nil
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
func (p OutputPlan) Rollback() {
	p.RemoveTemp()
	if p.Destination != "" {
		_ = os.Remove(p.Destination)
	}
	if p.backup != "" {
		_ = os.Rename(p.backup, p.Destination)
	}
}
func (p OutputPlan) Finalize() {
	if p.backup != "" {
		_ = os.Remove(p.backup)
	}
}
func (p OutputPlan) Skipped() bool { return p.skip }

func supportedOutputExtension(ext string) bool {
	switch ext {
	case "png", "jpg", "webp", "jxl", "avif", "gif", "bmp", "tiff", "ico", "heic",
		"mp4", "mov", "webm", "mkv", "avi", "mp3", "wav", "flac", "aac", "m4a", "ogg", "opus",
		"aiff", "alac", "ac3", "amr", "wma":
		return true
	default:
		return false
	}
}

func unsafeBase(base string) bool {
	return strings.ContainsAny(base, "[]:") || strings.HasPrefix(base, "@")
}
