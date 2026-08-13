package gizpay

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateOrderMetadata(t *testing.T) {
	if err := validateOrderMetadata(json.RawMessage(`{"type":"ai_call","model":"story"}`), 1024); err != nil {
		t.Fatalf("valid order metadata: %v", err)
	}
	for name, raw := range map[string]string{
		"prompt":        `{"prompt":"secret input"}`,
		"nested secret": `{"provider":{"api_secret":"value"}}`,
		"nested arrays": `{"items":[[ [{"authorization":"Bearer secret"}] ]]}`,
		"array":         `[]`,
		"invalid":       `{`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateOrderMetadata(json.RawMessage(raw), 1024); err == nil {
				t.Fatalf("forbidden metadata %s was accepted", raw)
			}
		})
	}
	if err := validateOrderMetadata(json.RawMessage(`{"title":"`+strings.Repeat("x", 100)+`"}`), 32); err == nil {
		t.Fatal("oversized metadata was accepted")
	}
}

func TestPlatformFeeRoundsUpWithoutIntermediateOverflow(t *testing.T) {
	if got := platformFee(1, 1); got != 1 {
		t.Fatalf("platformFee(1,1) = %d, want 1", got)
	}
	const gross = int64(9_000_000_000_000_000_000)
	if got := platformFee(gross, 200); got != 180_000_000_000_000_000 {
		t.Fatalf("large platform fee = %d", got)
	}
}
