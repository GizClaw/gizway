package gizway

import (
	"testing"
	"time"
)

func TestHandlerCloseStopsBackgroundWorkers(t *testing.T) {
	handler := &Handler{stop: make(chan struct{}), done: make(chan struct{})}
	go handler.runBackgroundWorkers()
	closed := make(chan struct{})
	go func() {
		_ = handler.Close()
		_ = handler.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not stop the background workers")
	}
}
