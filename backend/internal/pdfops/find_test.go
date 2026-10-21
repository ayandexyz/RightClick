package pdfops

import (
	"context"
	"errors"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildFindPDF makes one Letter page with a word, a differently cased word, a
// phrase split by a line break and a word with a suffix.
func buildFindPDF(t *testing.T) string {
	t.Helper()
	requireTool(t, "ps2pdf")
	requireTool(t, "pdftotext")
	dir := t.TempDir()
	script := "%!PS-Adobe-3.0\n/Helvetica findfont 24 scalefont setfont\n" +
		"72 700 moveto (alpha SECRET beta) show\n" +
		"72 650 moveto (the Secret Number is) show\n" +
		"72 600 moveto (long phrase end SEC) show\n" +
		"72 570 moveto (RET tail) show\n" +
		"72 500 moveto (numbers) show\nshowpage\n"
	source := filepath.Join(dir, "find.ps")
	if err := os.WriteFile(source, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	pdf := filepath.Join(dir, "find.pdf")
	runTool(t, "ps2pdf", source, pdf)
	return pdf
}

func find(t *testing.T, path string, terms ...string) []FindMatch {
	t.Helper()
	matches, err := NewPoppler(nil).Find(context.Background(), path, terms)
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

func TestFindMatchesIgnoringCaseWithBoxesInsideThePage(t *testing.T) {
	matches := find(t, buildFindPDF(t), "secret")
	if len(matches) != 2 {
		t.Fatalf("got %d matches %+v, want 2 (SECRET and Secret)", len(matches), matches)
	}
	for _, m := range matches {
		if m.Page != 1 || m.Term != "secret" || m.W <= 0 || m.H <= 0 || m.X < 0 || m.Y < 0 || m.X+m.W > 1 || m.Y+m.H > 1 {
			t.Errorf("bad match %+v", m)
		}
	}
	// SECRET sits at x=137..235 of a 612pt page, y=75..97 of 792.
	if got := matches[0]; math.Abs(got.X-137.4/612) > 0.01 || math.Abs(got.Y-74.8/792) > 0.01 {
		t.Errorf("first box %+v is not where SECRET was drawn", got)
	}
}

func TestFindMatchesAPhraseAndPartOfAWord(t *testing.T) {
	path := buildFindPDF(t)
	if got := find(t, path, "secret number"); len(got) != 1 {
		t.Errorf("phrase matched %d boxes %+v, want 1 on one line", len(got), got)
	}
	if got := find(t, path, "number"); len(got) != 2 {
		t.Errorf("number matched %d boxes, want 2 (Number, numbers)", len(got))
	}
	if got := find(t, path, "absent"); len(got) != 0 {
		t.Errorf("a missing term matched %+v", got)
	}
}

func TestFindRejectsInvalidTermsBeforeRunningPdftotext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.pdf")
	for name, terms := range map[string][]string{
		"empty":    {""},
		"overlong": {strings.Repeat("x", 101)},
		"too many": makeTerms(51),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewPoppler(nil).Find(context.Background(), path, terms); err == nil {
				t.Fatal("invalid terms were accepted")
			}
		})
	}
}

func makeTerms(n int) []string {
	terms := make([]string, n)
	for i := range terms {
		terms[i] = "term"
	}
	return terms
}

func TestFindReportsAPhraseSplitByALineBreakAsOneBoxPerLine(t *testing.T) {
	got := find(t, buildFindPDF(t), "end sec")
	if len(got) != 1 {
		t.Fatalf("got %+v, want one box", got)
	}
	// "phrase end SEC" then "RET": a phrase across the break needs two boxes.
	got = find(t, buildFindPDF(t), "sec ret")
	if len(got) != 2 || got[0].Y >= got[1].Y {
		t.Errorf("got %+v, want two boxes, the second below the first", got)
	}
}

func TestFindBoxesFollowARotatedPage(t *testing.T) {
	requireTool(t, "qpdf")
	requireTool(t, "pdfinfo")
	upright := buildFindPDF(t)
	turned := filepath.Join(t.TempDir(), "turned.pdf")
	if err := exec.Command("qpdf", upright, "--rotate=+90", "--", turned).Run(); err != nil {
		t.Skipf("qpdf cannot rotate the fixture: %v", err)
	}
	matches := find(t, turned, "secret")
	if len(matches) != 2 {
		t.Fatalf("got %d matches, want 2", len(matches))
	}
	for _, m := range matches {
		if m.X < 0 || m.Y < 0 || m.X+m.W > 1.0001 || m.Y+m.H > 1.0001 {
			t.Errorf("box %+v is outside the page: fractions must be of the page as turned", m)
		}
	}
	// Turned a quarter clockwise, a point at (x, y) of the page lands at
	// (1-y, x); SECRET started at x=137.4/612, y=74.8/792.
	if got := matches[0]; math.Abs(got.X-(1-(74.8+22.2)/792)) > 0.02 || math.Abs(got.Y-137.4/612) > 0.02 {
		t.Errorf("first box %+v is not where the turned page shows SECRET", got)
	}
}

// TestRedactByTermBlacksOutTheWordOnTheRenderedPage follows a search all the
// way to pixels: after redacting by term, the page as drawn is black where the
// word was, on an upright page and on a turned one.
func TestRedactByTermBlacksOutTheWordOnTheRenderedPage(t *testing.T) {
	requireTool(t, "pdftocairo")
	requireTool(t, "magick")
	requireTool(t, "qpdf")
	requireTool(t, "pdfinfo")
	upright := buildFindPDF(t)
	turned := filepath.Join(t.TempDir(), "turned.pdf")
	if err := exec.Command("qpdf", upright, "--rotate=+90", "--", turned).Run(); err != nil {
		t.Skipf("qpdf cannot rotate the fixture: %v", err)
	}
	for name, input := range map[string]string{"upright": upright, "turned": turned} {
		t.Run(name, func(t *testing.T) {
			want := find(t, input, "secret number")
			if len(want) != 1 {
				t.Fatalf("setup: %+v", want)
			}
			output := filepath.Join(t.TempDir(), "out.pdf")
			if err := redactPDF(context.Background(), input, output, RedactParams{Terms: []string{"secret number"}, DPI: 150}); err != nil {
				t.Fatal(err)
			}
			prefix := filepath.Join(t.TempDir(), "page")
			if err := exec.Command("pdftocairo", "-png", "-r", "72", "-singlefile", output, prefix).Run(); err != nil {
				t.Fatal(err)
			}
			f, err := os.Open(prefix + ".png")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			img, err := png.Decode(f)
			if err != nil {
				t.Fatal(err)
			}
			b := img.Bounds()
			cx := b.Min.X + int((want[0].X+want[0].W/2)*float64(b.Dx()))
			cy := b.Min.Y + int((want[0].Y+want[0].H/2)*float64(b.Dy()))
			if r, g, bl, _ := img.At(cx, cy).RGBA(); r+g+bl != 0 {
				t.Errorf("pixel (%d,%d) of the redacted page is not black: %d %d %d", cx, cy, r, g, bl)
			}
		})
	}
}

// TestFindWithAPasswordNeverPutsItOnACommandLine records every Poppler call
// through logging shims: any local user can read a process's arguments from
// /proc, so the password may only be used in memory.
func TestFindWithAPasswordNeverPutsItOnACommandLine(t *testing.T) {
	for _, tool := range []string{"qpdf", "pdftotext", "pdfinfo"} {
		requireTool(t, tool)
	}
	const password = "Sentinel-Find-Pw-20481"
	plain := buildFindPDF(t)
	locked := filepath.Join(t.TempDir(), "locked.pdf")
	// qpdf exits 3 for warnings it still succeeded with.
	if out, err := exec.Command("qpdf", "--encrypt", password, password, "256", "--", plain, locked).CombinedOutput(); err != nil {
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 3 {
			t.Fatalf("qpdf --encrypt: %v\n%s", err, out)
		}
	}

	shims, log := t.TempDir(), filepath.Join(t.TempDir(), "argv.log")
	for _, tool := range []string{"pdftotext", "pdfinfo"} {
		real, _ := exec.LookPath(tool)
		script := "#!/bin/sh\necho \"" + tool + " $*\" >> '" + log + "'\nexec " + real + " \"$@\"\n"
		if err := os.WriteFile(filepath.Join(shims, tool), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", shims+string(os.PathListSeparator)+os.Getenv("PATH"))

	matches, err := NewPoppler(nil).FindPassword(context.Background(), locked, []string{"secret"}, password)
	if err != nil || len(matches) != 2 {
		t.Fatalf("FindPassword = %+v, %v, want the 2 matches of the unlocked file", matches, err)
	}
	data, err := os.ReadFile(log)
	if err != nil || !strings.Contains(string(data), "pdftotext") || !strings.Contains(string(data), "pdfinfo") {
		t.Fatalf("the shims recorded %q (%v); the check below would prove nothing", data, err)
	}
	if strings.Contains(string(data), password) || strings.Contains(string(data), "-upw") {
		t.Errorf("the password reached a command line:\n%s", data)
	}

	if _, err := NewPoppler(nil).FindPassword(context.Background(), locked, []string{"secret"}, "wrong"); !errors.Is(err, ErrPasswordIncorrect) {
		t.Errorf("wrong password = %v, want ErrPasswordIncorrect", err)
	}
	if _, err := NewPoppler(nil).FindPassword(context.Background(), locked, []string{"secret"}, ""); err == nil {
		t.Error("a locked file with no password should not be searched")
	}
}
