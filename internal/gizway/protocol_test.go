package gizway

import (
	"math"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestCeilMulDiv(t *testing.T) {
	tests := []struct {
		name                 string
		left, right, divisor int64
		want                 int64
	}{
		{name: "exact", left: 10, right: 3, divisor: 5, want: 6},
		{name: "round up", left: 1, right: 1, divisor: 3, want: 1},
		{name: "large exact product", left: math.MaxInt64, right: 1, divisor: 1, want: math.MaxInt64},
		{name: "saturates", left: math.MaxInt64, right: math.MaxInt64, divisor: 1, want: math.MaxInt64},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ceilMulDiv(test.left, test.right, test.divisor); got != test.want {
				t.Fatalf("ceilMulDiv(%d, %d, %d) = %d, want %d", test.left, test.right, test.divisor, got, test.want)
			}
		})
	}
}

func TestRateRejectsUnknownMetrics(t *testing.T) {
	usage := &schemas.BifrostLLMUsage{PromptTokens: 11, CompletionTokens: 7}
	if _, err := rate(usage, []priceRow{{Metric: "cached_token", Unit: 1, Amount: 1}}); err == nil {
		t.Fatal("unknown metric was silently billed as input_token")
	}
}

func TestRateMapsInputAndOutputMetricsExplicitly(t *testing.T) {
	usage := &schemas.BifrostLLMUsage{PromptTokens: 11, CompletionTokens: 7}
	got, err := rate(usage, []priceRow{
		{Metric: "input_token", Unit: 1, Amount: 2},
		{Metric: "output_token", Unit: 1, Amount: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != 43 {
		t.Fatalf("rated usage = %d, want 43", got)
	}
}

func TestConsumeLocalCreditSaturatesWithoutOverflow(t *testing.T) {
	handler := &Handler{credits: map[string]*creditState{
		"normal":   {available: 10},
		"boundary": {available: math.MinInt64 + 2},
	}}

	handler.consumeLocalCredit("normal", 7)
	if got := handler.credits["normal"].available; got != 3 {
		t.Fatalf("normal local Credit = %d, want 3", got)
	}

	handler.consumeLocalCredit("boundary", 3)
	if got := handler.credits["boundary"].available; got != math.MinInt64 {
		t.Fatalf("overflowing local Credit = %d, want saturated %d", got, int64(math.MinInt64))
	}
}
