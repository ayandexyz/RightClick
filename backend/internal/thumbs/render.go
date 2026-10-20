package thumbs

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// PDFTool is the pdftocairo-backed Renderer. It renders into a directory the
// Cache hands it and reports the files it wrote.
type PDFTool struct {
	// run executes a tool. A nil run uses the real binary; tests inject one to
	// assert the argument list without pdftocairo installed.
	run func(ctx context.Context, name string, args []string) error
}

// NewPDFTool returns a renderer backed by the installed pdftocairo.
func NewPDFTool(run func(ctx context.Context, name string, args []string) error) *PDFTool {
	return &PDFTool{run: run}
}

// Available reports whether pdftocairo is installed. Without it the daemon
// disables thumbnail rendering but keeps serving every other method, so a
// missing optional tool never takes the editor down.
func (p *PDFTool) Available() error {
	if err := lookup("pdftocairo"); err != nil {
		return err
	}
	return nil
}

// Render draws the requested pages into dir and returns the PNG paths in the
// order the pages were asked for.
//
// pdftocairo names each output after the page with a dash, zero-padded to the
// digit width of the document's page count, so a 12-page document writes
// pg-09.png for page 9 while a 3-page one writes pg-3.png. The produced files
// are therefore listed and matched by their parsed number rather than
// predicted.
func (p *PDFTool) Render(ctx context.Context, file string, pages []int, width int, dir string) ([]string, error) {
	root := filepath.Join(dir, "page")
	// One pdftocairo per contiguous run keeps the argument list short and lets
	// a gap between requested pages skip work.
	for _, run := range contiguousRuns(pages) {
		args := []string{
			"-png",
			"-scale-to", strconv.Itoa(width),
			"-f", strconv.Itoa(run[0]),
			"-l", strconv.Itoa(run[1]),
			file, root,
		}
		if err := p.exec(ctx, "pdftocairo", args); err != nil {
			return nil, fmt.Errorf("pdftocairo: %w", err)
		}
	}

	produced, err := pagePNGs(root)
	if err != nil {
		return nil, err
	}
	byNumber := map[int]string{}
	for _, path := range produced {
		byNumber[cairoPageNumber(path)] = path
	}
	out := make([]string, 0, len(pages))
	for _, page := range pages {
		path, ok := byNumber[page]
		if !ok {
			return nil, fmt.Errorf("pdftocairo did not produce page %d", page)
		}
		out = append(out, path)
	}
	return out, nil
}

func (p *PDFTool) exec(ctx context.Context, name string, args []string) error {
	if p.run != nil {
		return p.run(ctx, name, args)
	}
	if err := lookup(name); err != nil {
		return err
	}
	return runProcess(ctx, name, args)
}

// contiguousRuns turns a sorted page list into inclusive [first,last] ranges,
// so pages 1,2,3,7,9,10 render as three pdftocairo calls rather than six.
func contiguousRuns(pages []int) [][2]int {
	var runs [][2]int
	for _, page := range pages {
		if n := len(runs); n > 0 && runs[n-1][1] == page-1 {
			runs[n-1][1] = page
			continue
		}
		runs = append(runs, [2]int{page, page})
	}
	return runs
}

// pagePNGs lists the PNGs pdftocairo wrote for a root prefix.
func pagePNGs(root string) ([]string, error) {
	dir, base := filepath.Split(root)
	matches, err := filepath.Glob(filepath.Join(dir, base+"-*.png"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

// cairoPageNumber reads the page number pdftocairo embedded in a file name,
// tolerating the zero padding it applies.
func cairoPageNumber(path string) int {
	base := filepath.Base(path)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	_, number, ok := strings.Cut(stem, "-")
	if !ok {
		return -1
	}
	n, err := strconv.Atoi(number)
	if err != nil {
		return -1
	}
	return n
}

// lookup reports whether a tool is installed.
func lookup(name string) error {
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("%s is not installed", name)
	}
	return nil
}

// runProcess executes a tool in its own process group so a cancelled context
// kills the child rather than orphaning it.
func runProcess(ctx context.Context, name string, args []string) error {
	path, err := exec.LookPath(name)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("%w: %s", err, stderr.Bytes())
		}
		return nil
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		return ctx.Err()
	}
}
