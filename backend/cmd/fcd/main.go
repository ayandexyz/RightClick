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
	"file-converter/backend/internal/pdfops"
	"file-converter/backend/internal/registry"
	"file-converter/backend/internal/server"
	"file-converter/backend/internal/thumbs"
	"file-converter/backend/internal/transport"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "serve" {
		fmt.Fprintln(os.Stderr, "usage: fcd serve [--idle-exit duration]")
		os.Exit(2)
	}
	flags := flag.NewFlagSet("serve", flag.ExitOnError)
	idleExit := flags.Duration("idle-exit", 5*time.Minute, "exit after this duration without clients or jobs (0 disables it)")
	maxWorkers := flags.Int("max-workers", jobs.DefaultWorkers(), "maximum conversions to run at once")
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
	manager.SetMaxWorkers(*maxWorkers)
	// The engines are built once at startup and shared by the manager and the
	// server. DisableConfigDir is already called inside NewPDFCPUBackend. Each
	// optional engine reports itself unavailable if its binary is missing, so
	// the menu only shows operations a user can actually run.
	pdfToolbox := pdfops.Toolbox{
		PDFCPU:  pdfops.NewPDFCPUBackend(),
		QPDF:    pdfops.NewQPDF(nil),
		GS:      pdfops.NewGhostscript(nil),
		Poppler: pdfops.NewPoppler(nil),
		OCR:     pdfops.NewOCR(nil),
	}
	manager.SetPDFToolbox(pdfToolbox)
	defer manager.Close()
	pdfInfo := pdfops.NewInfo(nil, pdfToolbox.PDFCPU)

	// Thumbnails are optional: without pdftocairo the daemon still serves every
	// other method and only thumbnail requests report it unavailable.
	var thumbCache *thumbs.Cache
	if err := (thumbs.NewPDFTool(nil)).Available(); err == nil {
		root, err := thumbs.CacheRoot()
		if err != nil {
			fmt.Fprintf(os.Stderr, "thumbnails: %v\n", err)
		} else {
			thumbCache = thumbs.NewCache(root, thumbs.Limits{}, thumbs.NewPDFTool(nil))
			if err := thumbCache.Ensure(); err != nil {
				fmt.Fprintf(os.Stderr, "thumbnail cache: %v\n", err)
				thumbCache = nil
			} else {
				// Prune once at start so a cache left over from a previous run
				// does not grow without bound.
				if err := thumbCache.Prune(time.Now()); err != nil {
					fmt.Fprintf(os.Stderr, "thumbnail cache prune: %v\n", err)
				}
			}
		}
	}
	srv := server.New(server.Dependencies{Jobs: manager, Registry: engines, Engine: enginePath, Version: "0.1.0", PDFToolbox: pdfToolbox, PDFInfo: pdfInfo, Thumbs: thumbCache})
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

	svgEngine := engine.NewSVG(nil)
	_, _ = svgEngine.Available()

	pdfEngine := engine.NewPDF(nil)
	_, _ = pdfEngine.Available()

	ffmpegEngine := engine.NewFFmpeg(nil, nil)
	// Probe independently. An unavailable media tool must not prevent image
	// conversions or daemon startup; the registry reports its status later.
	_, _ = ffmpegEngine.Available()

	engines := registry.New()
	engines.Register(imageEngine)
	engines.Register(svgEngine)
	engines.Register(pdfEngine)
	engines.Register(ffmpegEngine)
	return engines, enginePath
}

func newEngineRegistry() *registry.Registry {
	engines, _ := setupEngines()
	return engines
}

func isClosed(err error) bool {
	return err != nil && (err == os.ErrClosed || strings.Contains(err.Error(), "use of closed network connection"))
}
