package gizway

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gorm.io/gorm"
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

func TestProviderKeyInternalCreationFailureIsRetryable(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want int
	}{
		{name: "unavailable model is deterministic", err: errUnavailableProviderModel, want: http.StatusBadRequest},
		{name: "database or Bifrost failure is temporary", err: errors.New("database unavailable"), want: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeProviderKeyCreationError(recorder, test.err)
			if recorder.Code != test.want {
				t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), test.want)
			}
		})
	}
}

func TestProviderKeyMutationLookupFailureIsRetryable(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want int
	}{
		{name: "missing SQL row", err: sql.ErrNoRows, want: http.StatusNotFound},
		{name: "missing Bifrost row", err: gorm.ErrRecordNotFound, want: http.StatusNotFound},
		{name: "database failure", err: errors.New("database unavailable"), want: http.StatusInternalServerError},
		{name: "Bifrost failure", err: errors.New("Bifrost unavailable"), want: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeProviderKeyLookupError(recorder, test.err)
			if recorder.Code != test.want {
				t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), test.want)
			}
		})
	}
}
