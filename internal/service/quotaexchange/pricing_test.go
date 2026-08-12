package quotaexchange

import (
	"math"
	"testing"
)

func TestPriceMetricsUsesCheckedIntegerCeiling(t *testing.T) {
	t.Parallel()

	// These are the canonical story rates. Charging is performed centrally from
	// raw quantities; a Gateway is never allowed to submit a final charge.
	prices := map[string]Price{
		"input_token":  {UnitSize: 1000, Microcredits: 1800},
		"output_token": {UnitSize: 1000, Microcredits: 3600},
	}
	got, err := PriceMetrics(map[string]int64{"input_token": 10, "output_token": 5}, prices)
	if err != nil {
		t.Fatalf("PriceMetrics: %v", err)
	}
	if got != 36 {
		t.Fatalf("PriceMetrics = %d, want 36", got)
	}
}

func TestPriceMetricsRejectsUnknownMetricsAndOverflow(t *testing.T) {
	t.Parallel()

	if _, err := PriceMetrics(map[string]int64{"invented": 1}, map[string]Price{}); err == nil {
		t.Fatal("PriceMetrics accepted an unknown metric")
	}
	if _, err := PriceMetrics(
		map[string]int64{"request": math.MaxInt64},
		map[string]Price{"request": {UnitSize: 1, Microcredits: 2}},
	); err == nil {
		t.Fatal("PriceMetrics accepted multiplication overflow")
	}
}

func TestCurrentQuotaClampsDebtAndPaymentHolds(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		posted  int64
		holds   int64
		want    int64
		allowed bool
	}{
		{name: "positive", posted: 100, want: 100, allowed: true},
		{name: "hold", posted: 100, holds: 30, want: 70, allowed: true},
		{name: "fully held", posted: 100, holds: 100, want: 0},
		{name: "debt", posted: -20, want: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, allowed := CurrentQuota(test.posted, test.holds)
			if got != test.want || allowed != test.allowed {
				t.Fatalf("CurrentQuota(%d, %d) = (%d, %v), want (%d, %v)", test.posted, test.holds, got, allowed, test.want, test.allowed)
			}
		})
	}
}
