// Package quotaexchange contains the financial rules shared by GizPay's
// internal Quota endpoint and its PostgreSQL transaction boundary.
package quotaexchange

import (
	"errors"
	"fmt"
	"math"
	"math/big"
)

// Price is one immutable rate-publication entry. UnitSize raw metric units cost
// Microcredits. Both values are integers so billing never depends on floats.
type Price struct {
	UnitSize     int64
	Microcredits int64
}

// UsageRecord is the identity-free metering envelope sent by GizWay. Account
// and API-key attribution are intentionally absent: GizPay derives both from
// the raw key carried once by the enclosing Exchange request.
type UsageRecord struct {
	UCGID             string           `json:"ucgid"`
	OperationID       string           `json:"operation_id"`
	PublicModel       string           `json:"public_model"`
	ModelVariantID    string           `json:"model_variant_id"`
	RatePublicationID string           `json:"rate_publication_id"`
	Metrics           map[string]int64 `json:"metrics"`
	StartedAt         string           `json:"started_at"`
	CompletedAt       string           `json:"completed_at"`
}

// PriceMetrics recomputes the charge from raw Usage metrics using ceiling
// division for each metric. It rejects missing prices and every value that
// cannot be represented safely as an int64 financial amount.
func PriceMetrics(metrics map[string]int64, prices map[string]Price) (int64, error) {
	var total int64
	for metric, quantity := range metrics {
		price, ok := prices[metric]
		if !ok {
			return 0, fmt.Errorf("unknown metric %q", metric)
		}
		if quantity < 0 || price.UnitSize <= 0 || price.Microcredits < 0 {
			return 0, errors.New("invalid metric quantity or price")
		}
		charge, err := ceilingProductRatio(quantity, price.Microcredits, price.UnitSize)
		if err != nil {
			return 0, fmt.Errorf("price metric %q: %w", metric, err)
		}
		if charge > math.MaxInt64-total {
			return 0, errors.New("total charge overflow")
		}
		total += charge
	}
	return total, nil
}

func ceilingProductRatio(left, right, divisor int64) (int64, error) {
	if left == 0 || right == 0 {
		return 0, nil
	}
	product := new(big.Int).Mul(big.NewInt(left), big.NewInt(right))
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(product, big.NewInt(divisor), remainder)
	if remainder.Sign() != 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return 0, errors.New("charge overflow")
	}
	return quotient.Int64(), nil
}

// CurrentQuota returns the entire currently spendable AI balance. It does not
// reserve, lease, freeze, or mutate that balance. Debt and real payment holds
// clamp the wire quota to zero, while the posted ledger balance may stay
// negative for a later top-up to repay.
func CurrentQuota(postedMicrocredits, paymentHoldsMicrocredits int64) (int64, bool) {
	if postedMicrocredits <= 0 || paymentHoldsMicrocredits >= postedMicrocredits {
		return 0, false
	}
	if paymentHoldsMicrocredits < 0 {
		paymentHoldsMicrocredits = 0
	}
	spendable := postedMicrocredits - paymentHoldsMicrocredits
	return spendable, spendable > 0
}
