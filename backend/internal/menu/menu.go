// Package menu stores which conversion targets the file-manager menus offer.
//
// The policy lives in $XDG_CONFIG_HOME/file-converter/menu.toml (falling back
// to $HOME/.config/file-converter/menu.toml) as one array of target formats
// per source category:
//
//	video = ["mp4", "webm"]
//
// Categories or formats this build does not know are ignored so a newer
// config still loads. An empty list, or one with no reachable target, falls
// back to the category defaults so a menu never silently disappears.
package menu

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/protocol"
)

// Categories are the valid policy keys, in display order.
var Categories = []string{"image", "video", "audio", "pdf", "document", "presentation", "spreadsheet"}

var defaults = map[string][]string{
	"image":        {"jpeg", "png", "webp"},
	"video":        {"mp4", "mov", "webm", "gif"},
	"audio":        {"mp3", "m4a", "wav"},
	"pdf":          {"png", "jpeg", "webp"},
	"document":     {"pdf"},
	"presentation": {"pdf"},
	"spreadsheet":  {"pdf"},
}

var knownFormats = map[string]bool{}

func init() {
	for _, f := range []formats.Format{
		formats.PDF, formats.PNG, formats.JPEG, formats.WebP, formats.JXL, formats.AVIF, formats.GIF, formats.BMP,
		formats.TIFF, formats.ICO, formats.HEIC, formats.MP4, formats.MOV, formats.WebM, formats.MKV, formats.AVI,
		formats.MP3, formats.WAV, formats.FLAC, formats.AAC, formats.M4A, formats.OGG, formats.Opus, formats.AIFF,
		formats.ALAC, formats.AC3, formats.AMR, formats.WMA,
	} {
		knownFormats[string(f)] = true
	}
}

// Policy holds only the user's customised categories; missing ones use defaults.
type Policy map[string][]string

// Defaults returns a copy of the built-in menu targets per category.
func Defaults() map[string][]string {
	out := make(map[string][]string, len(defaults))
	for k, v := range defaults {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// Category maps a detected source format to its policy category.
func Category(format formats.Format) string {
	switch {
	case formats.IsVideoFormat(format):
		return "video"
	case formats.IsAudioFormat(format):
		return "audio"
	case formats.IsPDFFormat(format):
		return "pdf"
	case formats.IsImageFormat(format), formats.IsSVGFormat(format):
		return "image"
	default:
		return ""
	}
}

func knownCategory(category string) bool {
	_, ok := defaults[category]
	return ok
}

// Path returns the policy file location.
func Path() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "file-converter", "menu.toml"), nil
	}
	home := os.Getenv("HOME")
	if home == "" {
		return "", errors.New("neither XDG_CONFIG_HOME nor HOME is set")
	}
	return filepath.Join(home, ".config", "file-converter", "menu.toml"), nil
}

// Load reads the policy at path. A missing file is an empty policy.
func Load(path string) (Policy, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Policy{}, nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Save atomically replaces the policy file at path.
func Save(path string, p Policy) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".menu-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(p.Encode()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Encode renders the customised categories as TOML.
func (p Policy) Encode() []byte {
	var b strings.Builder
	for _, category := range Categories {
		list, ok := p[category]
		if !ok {
			continue
		}
		quoted := make([]string, len(list))
		for i, f := range list {
			quoted[i] = `"` + f + `"`
		}
		fmt.Fprintf(&b, "%s = [%s]\n", category, strings.Join(quoted, ", "))
	}
	return []byte(b.String())
}

// Set returns a copy of p with category's list replaced. Unknown categories
// and formats are rejected; duplicates are dropped.
func (p Policy) Set(category string, list []string) (Policy, error) {
	if !knownCategory(category) {
		return nil, fmt.Errorf("unknown menu category %q", category)
	}
	clean := make([]string, 0, len(list))
	seen := map[string]bool{}
	for _, f := range list {
		if !knownFormats[f] {
			return nil, fmt.Errorf("unknown format %q", f)
		}
		if !seen[f] {
			seen[f] = true
			clean = append(clean, f)
		}
	}
	out := p.clone()
	out[category] = clean
	return out, nil
}

// Reset returns a copy of p with category restored to its default. An empty
// category resets every category.
func (p Policy) Reset(category string) (Policy, error) {
	if category == "" {
		return Policy{}, nil
	}
	if !knownCategory(category) {
		return nil, fmt.Errorf("unknown menu category %q", category)
	}
	out := p.clone()
	delete(out, category)
	return out, nil
}

func (p Policy) clone() Policy {
	out := make(Policy, len(p)+1)
	for k, v := range p {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// Effective returns the menu formats per category, with defaults filling any
// category that is unset or empty.
func (p Policy) Effective() map[string][]string {
	out := Defaults()
	for category, list := range p {
		if knownCategory(category) && len(list) > 0 {
			out[category] = append([]string(nil), list...)
		}
	}
	return out
}

// TargetsForSource narrows the reachable targets of a selection to the ones
// the menu offers. categories are the source categories present in the
// selection; the offered formats are the union of their lists, in list order.
// If nothing remains it falls back to the category defaults, and finally to
// every reachable target.
func (p Policy) TargetsForSource(categories []string, reachable []protocol.Target) []protocol.Target {
	if len(reachable) == 0 {
		return reachable
	}
	pick := func(lists map[string][]string) []protocol.Target {
		var order []string
		seen := map[string]bool{}
		sorted := append([]string(nil), categories...)
		sort.Strings(sorted)
		for _, category := range sorted {
			for _, f := range lists[category] {
				if !seen[f] {
					seen[f] = true
					order = append(order, f)
				}
			}
		}
		var out []protocol.Target
		for _, f := range order {
			for _, target := range reachable {
				if target.Format == f {
					out = append(out, target)
				}
			}
		}
		return out
	}
	if out := pick(p.Effective()); len(out) > 0 {
		return out
	}
	if out := pick(defaults); len(out) > 0 {
		return out
	}
	return reachable
}
