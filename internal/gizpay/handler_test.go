package gizpay

import (
	"math"
	"testing"
)

func TestPlatformFeeRoundsUpWithoutOverflow(t *testing.T) {
	tests := []struct {
		gross, bps, want int64
	}{
		{1, 200, 1},
		{100, 200, 2},
		{101, 200, 3},
		{math.MaxInt64, 10000, math.MaxInt64},
	}
	for _, test := range tests {
		if got := platformFee(test.gross, test.bps); got != test.want {
			t.Errorf("platformFee(%d, %d) = %d, want %d", test.gross, test.bps, got, test.want)
		}
	}
}
