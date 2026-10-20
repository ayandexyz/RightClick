package main

import (
	"errors"
	"net"
	"testing"
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
