package transport

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

func SocketPath() (string, error) {
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if runtime == "" {
		return "", fmt.Errorf("XDG_RUNTIME_DIR is not set")
	}
	return filepath.Join(runtime, "file-converter", "fcd.sock"), nil
}

func PrepareSocket(path string) (func(), error) {
	if len(path) >= 108 {
		return nil, fmt.Errorf("unix socket path is too long (%d bytes; maximum is 107)", len(path))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	_ = os.Chmod(filepath.Dir(path), 0o700)
	if _, err := os.Stat(path); err == nil {
		conn, dialErr := net.DialTimeout("unix", path, 100*time.Millisecond)
		if dialErr == nil {
			conn.Close()
			return nil, fmt.Errorf("daemon already running")
		}
		if !isStaleSocket(path) {
			return nil, fmt.Errorf("cannot replace socket: %w", dialErr)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	return func() { _ = os.Remove(path) }, nil
}

func isStaleSocket(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode()&os.ModeSocket != 0
}

func Listen(path string) (net.Listener, func(), error) {
	cleanup, err := PrepareSocket(path)
	if err != nil {
		return nil, nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	_ = os.Chmod(path, 0o600)
	return listener, func() { listener.Close(); cleanup() }, nil
}
