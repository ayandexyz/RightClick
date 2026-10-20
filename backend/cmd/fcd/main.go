package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"file-converter/backend/internal/engine"
	"file-converter/backend/internal/jobs"
	"file-converter/backend/internal/registry"
	"file-converter/backend/internal/server"
	"file-converter/backend/internal/transport"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "serve" {
		fmt.Fprintln(os.Stderr, "usage: fcd serve [--idle-exit duration]")
		os.Exit(2)
	}
	flags := flag.NewFlagSet("serve", flag.ExitOnError)
	idleExit := flags.Duration("idle-exit", 5*time.Minute, "exit after this duration without clients or jobs (0 disables it)")
	_ = flags.Parse(os.Args[2:])
	path, err := transport.SocketPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	listener, cleanup, err := transport.Listen(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer cleanup()
	engines, enginePath := setupEngines()
	manager := jobs.NewManagerWithRegistry(engines)
	defer manager.Close()
	srv := server.New(server.Dependencies{Jobs: manager, Registry: engines, Engine: enginePath, Version: "0.1.0"})
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-stop; _ = listener.Close() }()
	if err := srv.ServeWithIdle(listener, *idleExit); err != nil {
		if !isClosed(err) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	srv.Wait()
}

func setupEngines() (*registry.Registry, string) {
	imageEngine := engine.NewImageMagick(nil)
	_ = imageEngine.ProbeCapabilities()
	enginePath, _ := imageEngine.Available()
	engines := registry.New()
	engines.Register(imageEngine)
	engines.Register(engine.NewFFmpeg(nil, nil))
	return engines, enginePath
}

func newEngineRegistry() *registry.Registry {
	engines, _ := setupEngines()
	return engines
}

func isClosed(err error) bool {
	return err != nil && (err == os.ErrClosed || strings.Contains(err.Error(), "use of closed network connection"))
}
