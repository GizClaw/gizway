package gizpay

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHumanLookupFailureClassification(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want int
	}{
		{name: "unknown identity", err: sql.ErrNoRows, want: http.StatusNotFound},
		{name: "temporary database failure", err: errors.New("database unavailable"), want: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeHumanLookupError(recorder, test.err)
			if recorder.Code != test.want {
				t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), test.want)
			}
		})
	}
}
