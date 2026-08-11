package timetext

import (
	"testing"
	"time"
)

func TestNormalizeMakesLexicalAndTemporalOrderIdentical(t *testing.T) {
	whole, err := Normalize("2026-08-10T00:00:00.000000000Z")
	if err != nil {
		t.Fatal(err)
	}
	half, err := Normalize("2026-08-10T08:00:00.5+08:00")
	if err != nil {
		t.Fatal(err)
	}
	if whole != "2026-08-10T00:00:00.000000000Z" || half != "2026-08-10T00:00:00.500000000Z" || !(whole < half) {
		t.Fatalf("canonical order whole=%q half=%q", whole, half)
	}
	if got := Format(time.Date(2026, 8, 10, 8, 0, 0, 1, time.FixedZone("CST", 8*60*60))); got != "2026-08-10T00:00:00.000000001Z" {
		t.Fatalf("Format = %q", got)
	}
}
