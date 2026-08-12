// Package merchant owns merchant payment policy, fee calculation and signed
// webhook delivery. SQL transactions are delegated to Store.
package merchant

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	riskadapter "github.com/idy/gizway/internal/adapter/risk"
	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/timetext"
)

type Service struct {
	store               *store.Store
	http                *http.Client
	now                 func() time.Time
	allowPrivateTargets bool
	risk                *riskadapter.Client
	checkoutBaseURL     string
}

var (
	// ErrInvalidRequest marks caller-controlled validation failures. HTTP
	// handlers may safely expose the wrapped explanation without ever exposing
	// a database driver, table name, provider response, or network detail.
	ErrInvalidRequest  = errors.New("invalid merchant request")
	ErrRiskUnavailable = errors.New("merchant risk provider unavailable")
)

func invalidRequest(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidRequest, message)
}

func NewConfigured(repository *store.Store, risk *riskadapter.Client, allowPrivateTargets bool, checkoutBaseURL string) *Service {
	service := &Service{store: repository, now: time.Now, risk: risk, allowPrivateTargets: allowPrivateTargets, checkoutBaseURL: strings.TrimRight(checkoutBaseURL, "/")}
	service.http = service.webhookClient()
	return service
}

func (s *Service) ConfigureClock(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

// NewForStoryTests permits loopback webhook receivers used by the executable
// Hurl stories. Production construction deliberately has no equivalent flag:
// merchant-controlled callbacks must never reach local or private networks.
func NewForStoryTests(repository *store.Store) *Service {
	return NewConfigured(repository, nil, true, "https://pay.gizway.test")
}

type CreateIntentRequest struct {
	ServiceID       string             `json:"service_id"`
	ExternalOrderID string             `json:"external_order_id"`
	Amount          store.CreditAmount `json:"amount"`
	Description     string             `json:"description"`
	ExpiresAt       string             `json:"expires_at"`
	Metadata        store.JSON         `json:"metadata"`
	ReturnURL       *string            `json:"return_url,omitempty"`
}

func (s *Service) CreateIntent(ctx context.Context, merchantAccountID, apiKeyID, idempotencyKey string, request CreateIntentRequest) (store.PaymentIntent, bool, error) {
	if request.ServiceID == "" || request.ExternalOrderID == "" || request.Amount.Asset != "GIZ_CREDIT" || request.Amount.Microcredits <= 0 || request.ExpiresAt == "" {
		return store.PaymentIntent{}, false, invalidRequest("service_id, external_order_id, positive GIZ_CREDIT amount, and expires_at are required")
	}
	expires, err := timetext.Parse(request.ExpiresAt)
	if err != nil || !expires.After(s.now()) {
		return store.PaymentIntent{}, false, invalidRequest("expires_at must be a future RFC3339 timestamp")
	}
	request.ExpiresAt = timetext.Format(expires)
	fee, err := store.CheckedCharge(request.Amount.Microcredits, 250, 10_000)
	if err != nil || fee >= request.Amount.Microcredits {
		return store.PaymentIntent{}, false, invalidRequest("amount is too small or too large for fee calculation")
	}
	if len(request.Metadata) == 0 {
		request.Metadata = store.JSON(`{}`)
	}
	encoded, _ := json.Marshal(request)
	hash := sha256.Sum256(encoded)
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(merchantAccountID+":"+idempotencyKey)).String()
	created := timetext.Format(s.now())
	intent := store.PaymentIntent{Object: "payment_intent", ID: id, MerchantAccountID: merchantAccountID, ServiceID: request.ServiceID, ExternalOrderID: request.ExternalOrderID, Amount: request.Amount, PlatformFee: store.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: fee}, NetAmount: store.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: request.Amount.Microcredits - fee}, FeeBPS: 250, Status: "created", Description: request.Description, Metadata: request.Metadata, CheckoutURL: s.checkoutBaseURL + "/checkout/" + id, ExpiresAt: request.ExpiresAt, CreatedAt: created}
	return s.store.CreatePaymentIntentForKey(ctx, merchantAccountID, apiKeyID, idempotencyKey, hash[:], intent)
}

type CreateMerchantServiceRequest struct {
	ServiceCode  string   `json:"service_code"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	InterfaceSet []string `json:"interface_set"`
}

func (s *Service) CreateMerchantService(ctx context.Context, userID, merchantAccountID, idempotencyKey string, request CreateMerchantServiceRequest) (store.MerchantService, store.RiskDecision, bool, error) {
	if request.ServiceCode == "" || request.Name == "" || len(request.InterfaceSet) == 0 {
		return store.MerchantService{}, store.RiskDecision{}, false, invalidRequest("service_code, name, and interface_set are required")
	}
	// Authorize before invoking the external risk provider. Store repeats the
	// ownership check in the final serializable transaction, so this is both
	// side-effect safe and race safe.
	if err := s.store.AuthorizeMerchantServiceCreation(ctx, userID, merchantAccountID); err != nil {
		return store.MerchantService{}, store.RiskDecision{}, false, err
	}
	allowedInterfaces := map[string]bool{"checkout": true, "webhook": true}
	seen := map[string]bool{}
	for _, item := range request.InterfaceSet {
		if !allowedInterfaces[item] || seen[item] {
			return store.MerchantService{}, store.RiskDecision{}, false, invalidRequest("interface_set contains an unapproved interface")
		}
		seen[item] = true
	}
	payload, _ := json.Marshal(request)
	hash := sha256.Sum256(payload)
	if service, decision, replayed, err := s.store.LookupMerchantServiceCommand(ctx, userID, merchantAccountID, idempotencyKey, hash[:]); err != nil || replayed {
		return service, decision, replayed, err
	}
	if s.risk == nil {
		return store.MerchantService{}, store.RiskDecision{}, false, ErrRiskUnavailable
	}
	serviceID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(merchantAccountID+":"+idempotencyKey)).String()
	assessmentID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(serviceID)).String()
	assessment, err := s.risk.Assess(ctx, riskadapter.AssessmentRequest{AssessmentID: assessmentID, MerchantAccountID: merchantAccountID, ServiceCode: request.ServiceCode, InterfaceSet: request.InterfaceSet})
	if err != nil {
		return store.MerchantService{}, store.RiskDecision{}, false, err
	}
	now := timetext.Format(s.now())
	interfaces, _ := json.Marshal(request.InterfaceSet)
	service := store.MerchantService{ID: serviceID, MerchantAccountID: merchantAccountID, ServiceCode: request.ServiceCode, Name: request.Name, Description: request.Description, InterfaceSet: store.JSON(interfaces), Status: "pending", MaxTransactionMicrocredits: assessment.MaxTransactionMicrocredits, DailyLimitMicrocredits: assessment.DailyLimitMicrocredits, CreatedAt: now, UpdatedAt: now}
	risk := store.RiskDecision{ID: assessmentID, MerchantAccountID: merchantAccountID, ServiceID: serviceID, ProviderReference: assessment.ProviderReference, Decision: assessment.Decision, KYCStatus: assessment.KYCStatus, KYBStatus: assessment.KYBStatus, SanctionsStatus: assessment.SanctionsStatus, AnomalyScore: assessment.AnomalyScore, Reason: assessment.Reason, CreatedAt: now}
	return s.store.CreateMerchantService(ctx, userID, idempotencyKey, hash[:], service, risk)
}

func (s *Service) Confirm(ctx context.Context, userID, intentID, idempotencyKey string) (store.PaymentIntent, bool, error) {
	intent, deliveries, replayed, err := s.store.ConfirmPaymentIntent(ctx, userID, intentID, idempotencyKey, timetext.Format(s.now()))
	if err != nil {
		return store.PaymentIntent{}, false, err
	}
	for _, id := range deliveries {
		_ = s.Deliver(ctx, id)
	}
	return intent, replayed, nil
}

// Reverse creates the explicit compensating workflow required after a payment
// has settled. Cancellation remains a pre-settlement operation only.
func (s *Service) Reverse(ctx context.Context, merchantAccountID, intentID, idempotencyKey, reason string) (store.MerchantPaymentReversal, bool, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 500 {
		return store.MerchantPaymentReversal{}, false, invalidRequest("reason is required and must not exceed 500 characters")
	}
	payload, _ := json.Marshal(struct {
		PaymentIntentID string `json:"payment_intent_id"`
		Reason          string `json:"reason"`
	}{intentID, reason})
	hash := sha256.Sum256(payload)
	reversal, deliveries, replayed, err := s.store.ReversePaymentIntent(ctx, merchantAccountID, intentID, idempotencyKey, hash[:], reason, timetext.Format(s.now()))
	if err != nil {
		return store.MerchantPaymentReversal{}, false, err
	}
	for _, deliveryID := range deliveries {
		_ = s.Deliver(ctx, deliveryID)
	}
	return reversal, replayed, nil
}

func (s *Service) CreateWebhookEndpoint(ctx context.Context, merchantAccountID, idempotencyKey, rawURL string, events []string) (store.WebhookEndpoint, string, bool, error) {
	if idempotencyKey == "" {
		return store.WebhookEndpoint{}, "", false, invalidRequest("Idempotency-Key is required")
	}
	if err := validateWebhookURL(rawURL, s.allowPrivateTargets); err != nil {
		return store.WebhookEndpoint{}, "", false, err
	}
	allowed := map[string]bool{"payment_intent.succeeded": true, "payment_intent.failed": true, "payment_intent.cancelled": true, "transaction.reversed": true}
	if len(events) == 0 {
		return store.WebhookEndpoint{}, "", false, invalidRequest("at least one event is required")
	}
	for _, event := range events {
		if !allowed[event] {
			return store.WebhookEndpoint{}, "", false, invalidRequest("unsupported webhook event")
		}
	}
	payload, _ := json.Marshal(struct {
		URL    string   `json:"url"`
		Events []string `json:"events"`
	}{rawURL, events})
	hash := sha256.Sum256(payload)
	secret, err := newWebhookSecret()
	if err != nil {
		return store.WebhookEndpoint{}, "", false, err
	}
	encoded, _ := json.Marshal(events)
	endpoint := store.WebhookEndpoint{ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte(merchantAccountID+":webhook:"+idempotencyKey)).String(), URL: rawURL, Events: store.JSON(encoded), SigningSecret: secret, Status: "active", CreatedAt: timetext.Format(s.now())}
	created, replayed, err := s.store.CreateWebhookEndpoint(ctx, merchantAccountID, idempotencyKey, hash[:], endpoint)
	if replayed {
		secret = ""
	}
	return created, secret, replayed, err
}

func newWebhookSecret() (string, error) {
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return "", err
	}
	return "whsec_" + base64.RawURLEncoding.EncodeToString(secretBytes), nil
}

func (s *Service) SetWebhookEndpointStatus(ctx context.Context, merchantAccountID, endpointID, idempotencyKey, status string) (store.WebhookEndpoint, bool, error) {
	if status != "active" && status != "disabled" {
		return store.WebhookEndpoint{}, false, invalidRequest("status must be active or disabled")
	}
	hash := sha256.Sum256([]byte(endpointID + "\x00" + status))
	return s.store.SetWebhookEndpointStatus(ctx, merchantAccountID, endpointID, status, idempotencyKey, hash[:], timetext.Format(s.now()))
}

func (s *Service) RotateWebhookEndpointSecret(ctx context.Context, merchantAccountID, endpointID, idempotencyKey string) (string, bool, error) {
	secret, err := newWebhookSecret()
	if err != nil {
		return "", false, err
	}
	hash := sha256.Sum256([]byte(endpointID))
	return s.store.RotateWebhookEndpointSecret(ctx, merchantAccountID, endpointID, idempotencyKey, hash[:], secret, timetext.Format(s.now()))
}

func (s *Service) DeleteWebhookEndpoint(ctx context.Context, merchantAccountID, endpointID, idempotencyKey string) (bool, error) {
	hash := sha256.Sum256([]byte(endpointID))
	return s.store.DeleteWebhookEndpoint(ctx, merchantAccountID, endpointID, idempotencyKey, hash[:], timetext.Format(s.now()))
}

func (s *Service) Deliver(ctx context.Context, deliveryID string) error {
	claimedAt := s.now().UTC()
	if err := s.store.ClaimWebhookDelivery(ctx, deliveryID, timetext.Format(claimedAt), timetext.Format(claimedAt.Add(30*time.Second))); err != nil {
		return err
	}
	target, err := s.store.GetDeliveryTarget(ctx, deliveryID)
	if err != nil {
		return err
	}
	if err := validateWebhookURL(target.URL, s.allowPrivateTargets); err != nil {
		message := err.Error()
		_ = s.store.FinishWebhookDelivery(context.WithoutCancel(ctx), deliveryID, 0, &message, timetext.Format(s.now()))
		return fmt.Errorf("validate merchant webhook target: %w", err)
	}
	mac := hmac.New(sha256.New, []byte(target.Secret))
	_, _ = mac.Write(target.Payload)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL, bytes.NewReader(target.Payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Gizway-Signature", "v1="+hex.EncodeToString(mac.Sum(nil)))
	response, err := s.http.Do(request)
	completed := timetext.Format(s.now())
	if err != nil {
		message := err.Error()
		_ = s.store.FinishWebhookDelivery(context.WithoutCancel(ctx), deliveryID, 0, &message, completed)
		return fmt.Errorf("deliver merchant webhook: %w", err)
	}
	defer response.Body.Close()
	var deliveryError *string
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := fmt.Sprintf("HTTP %d", response.StatusCode)
		deliveryError = &message
	}
	return s.store.FinishWebhookDelivery(context.WithoutCancel(ctx), deliveryID, response.StatusCode, deliveryError, completed)
}

// DispatchRecoverable delivers one bounded batch from durable storage. It is
// intentionally public for deterministic startup-recovery tests; RunDispatcher
// owns the recurring production loop.
func (s *Service) DispatchRecoverable(ctx context.Context, limit int) error {
	ids, err := s.store.RecoverableWebhookDeliveryIDs(ctx, timetext.Format(s.now()), limit)
	if err != nil {
		return err
	}
	var firstErr error
	for _, id := range ids {
		if err := s.Deliver(ctx, id); err != nil && !errors.Is(err, store.ErrNotFound) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// RunDispatcher immediately recovers committed work, then polls until the app
// context is cancelled. Failures schedule durable, bounded exponential retries;
// an operator may also create an explicit retry after failure/exhaustion.
func (s *Service) RunDispatcher(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}
	dispatch := func() {
		workerContext := store.WithAuditRequestID(ctx, "webhook-dispatch-"+uuid.NewString())
		_ = s.DispatchRecoverable(workerContext, 64)
	}
	dispatch()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			dispatch()
		}
	}
}

func (s *Service) webhookClient() *http.Client {
	client := &http.Client{Timeout: 5 * time.Second}
	client.CheckRedirect = func(request *http.Request, _ []*http.Request) error {
		return validateWebhookURL(request.URL.String(), s.allowPrivateTargets)
	}
	if !s.allowPrivateTargets {
		client.Transport = &http.Transport{DialContext: safeWebhookDialContext}
	}
	return client
}

func validateWebhookURL(rawURL string, allowPrivate bool) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return invalidRequest("absolute HTTP(S) webhook URL is required")
	}
	if parsed.User != nil {
		return invalidRequest("webhook URL cannot contain user information")
	}
	if !allowPrivate && parsed.Scheme != "https" {
		return invalidRequest("production webhook URL must use HTTPS")
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return invalidRequest("webhook URL cannot target localhost")
	}
	if address := net.ParseIP(host); address != nil && !allowPrivate && unsafeWebhookIP(address) {
		return invalidRequest("webhook URL cannot target a private or local address")
	}
	return nil
}

// safeWebhookDialContext resolves and validates the address immediately before
// dialing, then connects to the validated IP. This prevents DNS rebinding from
// turning an initially public hostname into an internal-network request.
func safeWebhookDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("split webhook address: %w", err)
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve webhook host: %w", err)
	}
	if len(addresses) == 0 {
		return nil, errors.New("webhook host resolved to no addresses")
	}
	for _, candidate := range addresses {
		if unsafeWebhookIP(candidate.IP) {
			return nil, errors.New("webhook host resolved to a private or local address")
		}
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	return dialer.DialContext(ctx, network, net.JoinHostPort(addresses[0].IP.String(), port))
}

func unsafeWebhookIP(address net.IP) bool {
	return address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() ||
		address.IsLinkLocalMulticast() || address.IsUnspecified() || address.IsMulticast()
}
