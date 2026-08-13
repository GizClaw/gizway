package gizpay

import (
	"bytes"
	"errors"
	"testing"
)

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("entropy unavailable") }

func TestRandomTokenUsesAllRequestedEntropy(t *testing.T) {
	token, err := randomToken(bytes.NewReader(make([]byte, 24)), 24)
	if err != nil {
		t.Fatal(err)
	}
	if token != "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" {
		t.Fatalf("token = %q", token)
	}
}

func TestRandomTokenPropagatesEntropyFailure(t *testing.T) {
	if _, err := randomToken(failingReader{}, 24); err == nil {
		t.Fatal("entropy failure was ignored")
	}
}
