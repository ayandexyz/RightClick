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
