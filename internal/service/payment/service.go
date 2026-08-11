// Package payment owns top-up/refund state transitions and provider boundary
// orchestration. All balance mutations remain atomic Store transactions.
package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	paymentadapter "github.com/idy/gizway/internal/adapter/payment"
	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/timetext"
)

type Service struct {
	store          *store.Store
	provider       *paymentadapter.Client
	callbackSecret []byte
	now            func() time.Time
}

var (
	ErrInvalidProviderEvent = errors.New("invalid payment provider event")
	ErrInvalidRequest       = errors.New("invalid payment request")
	ErrProviderUnavailable  = errors.New("payment provider unavailable")
)

func invalidRequest(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidRequest, message)
}

const providerReplayWindow = 5 * time.Minute

func New(repository *store.Store, provider *paymentadapter.Client, callbackSecret string) *Service {
	return &Service{store: repository, provider: provider, callbackSecret: []byte(callbackSecret), now: time.Now}
}

func (s *Service) ConfigureClock(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

type CreateTopupRequest struct {
	FiatCurrency    string  `json:"fiat_currency"`
	FiatAmountMinor int64   `json:"fiat_amount_minor"`
	ReturnURL       *string `json:"return_url,omitempty"`
}

func (s *Service) CreateTopup(ctx context.Context, userID, accountID, idempotencyKey string, request CreateTopupRequest) (store.Topup, bool, error) {
	if request.FiatCurrency != "USD" || request.FiatAmountMinor <= 0 {
		return store.Topup{}, false, invalidRequest("a supported positive USD amount is required")
	}
	encoded, _ := json.Marshal(request)
	hash := sha256.Sum256(encoded)
	if request.FiatAmountMinor > math.MaxInt64/1_000_000 {
		return store.Topup{}, false, invalidRequest("top-up conversion overflow")
	}
	// Published StoryPay price: baseline 100 cents / Credit, effective
	// 90 cents / Credit (10% discount). Issuance rounds down and can never
	// create more Credit than the recorded rational rate permits.
	credit := request.FiatAmountMinor * 1_000_000 / 90
	if credit <= 0 {
		return store.Topup{}, false, invalidRequest("top-up is below the minimum")
	}
	// Resolve a durable replay before provider I/O. The Store and payment
	// provider both repeat idempotency enforcement to close the concurrent and
	// ambiguous-commit windows without creating duplicate checkouts.
	if stored, replayed, err := s.store.LookupTopupCommand(ctx, userID, accountID, idempotencyKey, hash[:]); err != nil || replayed {
		return stored, replayed, err
	}
	if s.provider == nil {
		return store.Topup{}, false, ErrProviderUnavailable
	}
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(accountID+":"+idempotencyKey)).String()
	checkout, err := s.provider.CreateCheckout(ctx, id, request.FiatCurrency, request.FiatAmountMinor)
	if err != nil {
		return store.Topup{}, false, fmt.Errorf("%w: create checkout", ErrProviderUnavailable)
	}
	checkoutURL := checkout.CheckoutURL
	topup := store.Topup{ID: id, AccountID: accountID, PaymentProvider: s.provider.ProviderID(),
		ProviderReference: checkout.ProviderReference, FiatCurrency: request.FiatCurrency,
		FiatAmountMinor: request.FiatAmountMinor,
		Rate: store.TopupRateSnapshot{
			Base:      store.TopupRate{FiatMinor: 100, CreditMicrocredits: 1_000_000},
			Effective: store.TopupRate{FiatMinor: 90, CreditMicrocredits: 1_000_000}, DiscountBPS: 1000,
		},
		CreditAmount:     store.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: credit},
		RefundableAmount: store.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: 0},
		Status:           "pending", CheckoutURL: &checkoutURL, CreatedAt: timetext.Format(s.now())}
	return s.store.CreateTopup(ctx, userID, idempotencyKey, hash[:], topup)
}

type ProviderEvent struct {
	EventID           string `json:"event_id"`
	Type              string `json:"type"`
	ProviderReference string `json:"provider_reference"`
	Currency          string `json:"currency"`
	AmountMinor       int64  `json:"amount_minor"`
}

func (s *Service) CompleteProviderEvent(ctx context.Context, raw []byte, signature string) (store.Topup, bool, error) {
	if len(s.callbackSecret) == 0 {
		return store.Topup{}, false, ErrInvalidProviderEvent
	}
	var timestampText, digestText string
	for part := range strings.SplitSeq(signature, ",") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch name {
		case "t":
			timestampText = value
		case "v1":
			digestText = value
		}
	}
	timestamp, err := strconv.ParseInt(timestampText, 10, 64)
	provided, decodeErr := hex.DecodeString(digestText)
	if err != nil || decodeErr != nil {
		return store.Topup{}, false, ErrInvalidProviderEvent
	}
	delta := s.now().Sub(time.Unix(timestamp, 0))
	if delta < -providerReplayWindow || delta > providerReplayWindow {
		return store.Topup{}, false, ErrInvalidProviderEvent
	}
	mac := hmac.New(sha256.New, s.callbackSecret)
	_, _ = mac.Write([]byte(timestampText + "."))
	_, _ = mac.Write(raw)
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return store.Topup{}, false, ErrInvalidProviderEvent
	}
	var event ProviderEvent
	if err := json.Unmarshal(raw, &event); err != nil || event.EventID == "" || event.Type != "topup.succeeded" {
		return store.Topup{}, false, ErrInvalidProviderEvent
	}
	payloadHash := sha256.Sum256(raw)
	return s.store.CompleteTopupEvent(ctx, event.EventID, event.ProviderReference, event.Currency,
		event.AmountMinor, payloadHash[:], timetext.Format(s.now()))
}

func (s *Service) Refund(ctx context.Context, userID, accountID, topupID, idempotencyKey string, amount store.CreditAmount) (store.RefundRecord, bool, error) {
	if amount.Asset != "GIZ_CREDIT" || amount.Microcredits <= 0 {
		return store.RefundRecord{}, false, invalidRequest("positive GIZ_CREDIT amount is required")
	}
	encoded, _ := json.Marshal(amount)
	hash := sha256.Sum256(encoded)
	created, providerReference, currency, replayed, err := s.store.CreateRefund(ctx, userID, accountID, topupID, idempotencyKey, hash[:], amount.Microcredits, timetext.Format(s.now()))
	if err != nil || replayed && created.Status != "pending" {
		return created, replayed, err
	}
	completed, err := s.executeRefund(ctx, created, providerReference, currency)
	if err != nil {
		return store.RefundRecord{}, false, err
	}
	return completed, replayed, nil
}

// executeRefund maps the provider protocol onto the durable Refund state
// machine. A definitive rejection becomes failed and releases Credit at once;
// pending stays recoverable; transport/5xx ambiguity is returned to the caller
// while the persisted command remains available to the background reconciler.
func (s *Service) executeRefund(ctx context.Context, refund store.RefundRecord, providerReference, currency string) (store.RefundRecord, error) {
	if s.provider == nil {
		return store.RefundRecord{}, ErrProviderUnavailable
	}
	providerResult, err := s.provider.Refund(ctx, providerReference, refund.ID, currency, refund.FiatAmountMinor)
	if err != nil {
		return store.RefundRecord{}, fmt.Errorf("%w: original-route refund", ErrProviderUnavailable)
	}
	switch providerResult.Status {
	case "succeeded":
		return s.store.CompleteRefund(ctx, refund.ID, providerResult.ProviderRefundID, timetext.Format(s.now()))
	case "failed":
		return s.store.FailRefund(ctx, refund.ID, timetext.Format(s.now()), "provider definitively rejected original-route refund")
	case "pending":
		return refund, nil
	default:
		// The adapter rejects unknown statuses, but retaining a fail-closed
		// branch keeps this state machine safe if another adapter is added.
		return store.RefundRecord{}, errors.New("unknown payment provider refund state")
	}
}

// RecoverPendingRefunds reconciles durable pending commands after ambiguous
// network/process failures. It is safe to call repeatedly because each refund
// ID is the provider idempotency key and Store terminal transitions are
// conditional/idempotent.
func (s *Service) RecoverPendingRefunds(ctx context.Context, limit int) error {
	if s.provider == nil {
		return nil
	}
	commands, err := s.store.RecoverableRefunds(ctx, limit)
	if err != nil {
		return err
	}
	var firstErr error
	for _, command := range commands {
		if _, err := s.executeRefund(ctx, command.Refund, command.ProviderReference, command.Currency); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
