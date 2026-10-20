package main

import (
	"errors"
	"net"
	"os"
	"testing"

	"file-converter/backend/internal/jobs"
	"file-converter/backend/internal/pdfops"
	"file-converter/backend/internal/server"
)

func TestIsClosedRecognizesListenerCloseErrors(t *testing.T) {
	if !isClosed(net.ErrClosed) {
		t.Fatal("expected net.ErrClosed to be recognized")
	}
	if !isClosed(errors.New("accept: use of closed network connection")) {
		t.Fatal("expected closed network connection error to be recognized")
	}
	if isClosed(errors.New("permission denied")) {
		t.Fatal("unexpectedly recognized unrelated error")
	}
}

func TestEngineRegistryIncludesSVG(t *testing.T) {
	engines := newEngineRegistry()
	for _, registered := range engines.Engines() {
		if registered.ID() == "svg" {
			return
		}
	}
	t.Fatal("daemon engine registry does not include svg")
}

func TestEngineRegistryIncludesPDF(t *testing.T) {
	engines := newEngineRegistry()
	for _, registered := range engines.Engines() {
		if registered.ID() == "pdf" {
			return
		}
	}
	t.Fatal("daemon engine registry does not include pdf")
}

func TestEngineRegistryIncludesFFmpeg(t *testing.T) {
	engines := newEngineRegistry()
	var found bool
	for _, registered := range engines.Engines() {
		if registered.ID() == "ffmpeg" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("daemon engine registry does not include ffmpeg")
	}
}

func TestNewEngineRegistryReportsPDFCPU(t *testing.T) {
	// The cmd/fcd wiring must surface pdfcpu to clients even though it is not a
	// conversion engine the registry tracks.
	backend := pdfops.NewPDFCPUBackend()
	manager := jobs.NewManagerWithRegistry(newEngineRegistry())
	defer manager.Close()
	manager.SetPDFToolbox(pdfops.Toolbox{PDFCPU: backend})
	deps := server.Dependencies{Jobs: manager, Registry: newEngineRegistry(), PDFToolbox: pdfops.Toolbox{PDFCPU: backend}}
	srv := server.New(deps)
	status := srv.EngineStatus()
	found := false
	for _, entry := range status {
		if entry.ID == "pdfcpu" {
			found = true
			if !entry.Available {
				t.Fatalf("pdfcpu report = %+v, want available", entry)
			}
		}
	}
	if !found {
		t.Fatalf("server status = %+v, want pdfcpu present", status)
	}
}

func TestNewPDFCPUBackendDoesNotCreateConfigDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	backend := pdfops.NewPDFCPUBackend()
	if err := backend.Available(); err != nil {
		t.Fatalf("Available: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == "" {
			continue
		}
		t.Fatalf("config directory created: %s", entry.Name())
	}
}
