package pdfops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// Runner executes one external binary with the given arguments. It matches the
// seam engine.ImageMagick uses so tests can assert argument lists without the
// real tools installed.
type Runner func(ctx context.Context, name string, args []string) error

// defaultRunTool is the production runner. It calls the binary, captures its
// stderr into the returned error, and converts a non-zero exit code into a
// typed value so engines can inspect it. Stderr is drained in a goroutine so a
// process that fills the pipe does not block; ctx cancellation kills the whole
// process group.
func defaultRunTool(ctx context.Context, name string, args []string) error {
	path, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("look up %s: %w", name, err)
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	select {
	case err := <-wait:
		if err == nil {
			return nil
		}
		wrap := err
		if capture := stderrBuf.Bytes(); len(capture) > 0 {
			wrap = fmt.Errorf("%w: %s", err, capture)
		}
		return &exitCodeError{code: exitCode(cmd.ProcessState), err: wrap, name: name}
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-wait
		return ctx.Err()
	}
}

// exitCodeError lets engines tell "the program produced something, despite a
// non-zero exit" (qpdf's exit 3) apart from "the program failed outright" in a
// way errcodes.Is does not give them directly.
type exitCodeError struct {
	code int
	err  error
	name string
}

func (e *exitCodeError) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return fmt.Sprintf("%s exited %d", e.name, e.code)
}

func (e *exitCodeError) Unwrap() error { return e.err }

func (e *exitCodeError) ExitCode() int { return e.code }

func exitCode(state *os.ProcessState) int {
	if state == nil {
		return -1
	}
	if status, ok := state.Sys().(syscall.WaitStatus); ok {
		return status.ExitStatus()
	}
	return state.ExitCode()
}

// availableBinary is the cheap "is the binary here?" probe used by every
// engine's Available method.
func availableBinary(name string) error {
	_, err := exec.LookPath(name)
	if err == nil {
		return nil
	}
	var ee *exec.Error
	if errors.As(err, &ee) {
		return fmt.Errorf("%s is not installed", name)
	}
	return err
}

// nilRunner is a Runner that does nothing; used in tests that want to assert
// argument lists without spawning any process.
var nilRunner Runner = func(_ context.Context, _ string, _ []string) error { return nil }
