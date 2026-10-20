package registry

import (
	"context"
	"fmt"
	"strings"

	"file-converter/backend/internal/engine"
	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/protocol"
)

type Engine interface {
	ID() string
	Available() (string, error)
	Supports(source, target formats.Format) bool
	Convert(context.Context, string, formats.Format, string, formats.Format, protocol.ImageOptions) error
}

type Registry struct {
	engines []Engine
}

func New() *Registry { return &Registry{} }

func (r *Registry) Engines() []Engine {
	return append([]Engine(nil), r.engines...)
}

func (r *Registry) Status() []map[string]any {
	out := make([]map[string]any, 0, len(r.engines))
	for _, engine := range r.engines {
		_, err := engine.Available()
		out = append(out, map[string]any{"id": engine.ID(), "available": err == nil})
	}
	return out
}

func (r *Registry) Register(engine Engine) {
	r.engines = append(r.engines, engine)
}

func (r *Registry) Resolve(source, target formats.Format) (Engine, error) {
	var unavailable error
	for _, engine := range r.engines {
		_, err := engine.Available()
		if err != nil {
			if unavailable == nil {
				unavailable = err
			}
			continue
		}
		if engine.Supports(source, target) {
			return engine, nil
		}
	}
	if unavailable != nil {
		return nil, fmt.Errorf("no engine supports %s to %s: %w", source, target, unavailable)
	}
	return nil, fmt.Errorf("no engine supports %s to %s", source, target)
}

func (r *Registry) TargetsForFiles(paths []string) ([]protocol.Target, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("files list cannot be empty")
	}
	var common map[formats.Format]bool
	var hasVideoSource bool
	var hasImageSource bool
	var hasSVGSource bool
	var hasPDFSource bool
	var hasNonSVGSource bool
	for _, path := range paths {
		source, err := formats.Detect(path)
		if err != nil {
			source, err = r.mediaSource(context.Background(), path)
			if err != nil {
				return nil, err
			}
		}
		if formats.IsVideoFormat(source) {
			hasVideoSource = true
		}
		if formats.IsImageFormat(source) {
			hasImageSource = true
		}
		if formats.IsSVGFormat(source) {
			hasSVGSource = true
		} else {
			hasNonSVGSource = true
		}
		if formats.IsPDFFormat(source) {
			hasPDFSource = true
		}
		available := make(map[formats.Format]bool)
		for _, target := range allSupportedFormats() {
			if target == source || (formats.IsAudioFormat(source) && formats.IsVideoFormat(target)) {
				continue
			}
			if _, err := r.Resolve(source, target); err == nil {
				available[target] = true
			}
		}
		if common == nil {
			common = available
		} else {
			for target := range common {
				if !available[target] {
					delete(common, target)
				}
			}
		}
	}
	out := make([]protocol.Target, 0, len(common))
	for _, target := range protocol.SupportedPresets() {
		if strings.HasPrefix(target.Preset, "svg-to-") && (!hasSVGSource || hasNonSVGSource) {
			continue
		}
		if !strings.HasPrefix(target.Preset, "svg-to-") && hasSVGSource && !hasNonSVGSource {
			continue
		}
		if strings.HasPrefix(target.Preset, "pdf-to-") && !hasPDFSource {
			continue
		}
		format, ok := formats.PresetFormat(target.Preset)
		if target.Preset == "video-to-gif" && (!hasVideoSource || hasImageSource) {
			continue
		}
		if target.Preset == "image-to-gif" && (hasVideoSource || !hasImageSource) {
			continue
		}
		if ok && common[format] {
			out = append(out, target)
		}
	}
	return out, nil
}

type MediaProber interface {
	Probe(context.Context, string) (engine.MediaKind, error)
}

func (r *Registry) MediaSource(ctx context.Context, path string) (formats.Format, error) {
	return r.mediaSource(ctx, path)
}

func (r *Registry) mediaSource(ctx context.Context, path string) (formats.Format, error) {
	for _, registered := range r.engines {
		prober, ok := registered.(MediaProber)
		if !ok {
			continue
		}
		if _, err := registered.Available(); err != nil {
			continue
		}
		kind, err := prober.Probe(ctx, path)
		if err != nil {
			continue
		}
		switch kind {
		case engine.MediaAudio:
			if format, ok := formats.FormatFromExtension(path); ok && formats.IsAudioFormat(format) {
				return format, nil
			}
		case engine.MediaVideo:
			if format, ok := formats.FormatFromExtension(path); ok && formats.IsVideoFormat(format) {
				return format, nil
			}
		}
	}
	return "", formats.ErrUnsupported
}

func allSupportedFormats() []formats.Format {
	candidates := []formats.Format{
		formats.PNG, formats.JPEG, formats.WebP, formats.JXL, formats.AVIF, formats.BMP, formats.TIFF, formats.ICO, formats.HEIC,
		formats.MP4, formats.MOV, formats.WebM, formats.MKV, formats.AVI, formats.GIF,
		formats.MP3, formats.WAV, formats.FLAC, formats.AAC, formats.M4A, formats.OGG, formats.Opus,
		formats.AIFF, formats.ALAC, formats.AC3, formats.AMR, formats.WMA,
	}
	out := make([]formats.Format, 0, len(candidates))
	for _, format := range candidates {
		if formats.IsImageFormat(format) || formats.IsVideoFormat(format) || formats.IsAudioFormat(format) {
			out = append(out, format)
		}
	}
	return out
}

func (r *Registry) Targets(source formats.Format) []formats.Format {
	seen := make(map[formats.Format]bool)
	out := make([]formats.Format, 0, len(allSupportedFormats()))
	for _, target := range allSupportedFormats() {
		if target == source || seen[target] {
			continue
		}
		if _, err := r.Resolve(source, target); err == nil {
			seen[target] = true
			out = append(out, target)
		}
	}
	return out
}
