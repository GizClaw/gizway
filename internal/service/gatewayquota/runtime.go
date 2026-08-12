// Package gatewayquota connects GizPay's report-and-query API to one process's
// ephemeral local admission counters.
package gatewayquota

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	gizpayclient "github.com/idy/gizway/internal/client/gizpay"
	"github.com/idy/gizway/internal/service/localadmission"
	"github.com/idy/gizway/internal/service/quotaexchange"
	"github.com/idy/gizway/internal/store"
)

type Exchanger interface {
	Exchange(context.Context, string, []quotaexchange.UsageRecord) (gizpayclient.ExchangeResponse, error)
}

type Runtime struct {
	exchanger  Exchanger
	local      *localadmission.Coordinator
	outbox     *store.Store
	processID  string
	tokenSalt  [32]byte
	contexts   map[[32]byte]runtimeContext
	now        func() time.Time
	exchangeMu sync.Mutex
	exchanges  map[[32]byte]*keyExchangeLock
	tokenMu    sync.Mutex
	retryMu    sync.Mutex
	retrying   map[[32]byte]string
	wake       chan struct{}
}

type runtimeContext struct {
	rawAPIKey string
	token     string
}

type keyExchangeLock struct {
	mu   sync.Mutex
	refs int
}

func New(exchanger Exchanger, local *localadmission.Coordinator, outbox *store.Store, now func() time.Time) *Runtime {
	if outbox == nil {
		panic("Gateway quota runtime requires the regional Usage outbox")
	}
	if now == nil {
		now = time.Now
	}
	runtime := &Runtime{
		exchanger: exchanger, local: local, outbox: outbox, now: now, processID: uuid.NewString(),
		contexts: make(map[[32]byte]runtimeContext), exchanges: make(map[[32]byte]*keyExchangeLock),
		retrying: make(map[[32]byte]string), wake: make(chan struct{}, 1),
	}
	if _, err := rand.Read(runtime.tokenSalt[:]); err != nil {
		panic("Gateway quota runtime requires process entropy: " + err.Error())
	}
	return runtime
}

// Admit checks a fresh local counter and performs an empty-Usage Exchange only
// when there is no usable answer or the counter is depleted. Multiple first
// requests are serialized so one process does not fan out redundant key checks.
func (r *Runtime) Admit(ctx context.Context, rawAPIKey string, commitment int64) (bool, error) {
	if rawAPIKey == "" || commitment <= 0 {
		return false, nil
	}
	r.ensureRuntimeToken(rawAPIKey)
	if admitted, refresh := r.local.TryAdmit(rawAPIKey, commitment); admitted || !refresh {
		return admitted, nil
	}
	unlock := r.lockExchange(rawAPIKey)
	defer unlock()
	if admitted, refresh := r.local.TryAdmit(rawAPIKey, commitment); admitted || !refresh {
		return admitted, nil
	}
	response, err := r.exchangeCurrent(ctx, rawAPIKey)
	if err != nil {
		if gizpayclient.IsPermanentExchangeError(err) {
			r.dropRuntimeContext(rawAPIKey)
		}
		return false, err
	}
	r.replaceLocal(rawAPIKey, response)
	admitted, _ := r.local.TryAdmit(rawAPIKey, commitment)
	return admitted, nil
}

// Check validates a customer key and current quota without consuming any of
// the returned allowance. It is used by unmetered protocol discovery routes:
// the one-microcredit local admission is immediately restored and no Usage is
// created or reported.
func (r *Runtime) Check(ctx context.Context, rawAPIKey string) (bool, error) {
	admitted, err := r.Admit(ctx, rawAPIKey, 1)
	if admitted {
		r.local.Complete(rawAPIKey, 1, 0)
	}
	return admitted, err
}

// lockExchange serializes only refreshes for the same raw key. The map key is
// a process-random HMAC, never the customer credential, and the entry is
// removed as soon as the last waiter leaves. This preserves the important
// recheck-after-lock behavior without letting one slow customer block all
// Gateway traffic.
func (r *Runtime) lockExchange(rawAPIKey string) func() {
	digest := r.tokenDigest(rawAPIKey)
	r.exchangeMu.Lock()
	lock := r.exchanges[digest]
	if lock == nil {
		lock = &keyExchangeLock{}
		r.exchanges[digest] = lock
	}
	lock.refs++
	r.exchangeMu.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		r.exchangeMu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(r.exchanges, digest)
		}
		r.exchangeMu.Unlock()
	}
}

// Complete replaces the estimate already consumed at admission with measured
// provider Usage, then queues the identity-free record in the current process's
// Usage outbox for the next required Exchange. It does not contact GizPay on
// every AI request.
func (r *Runtime) Complete(ctx context.Context, rawAPIKey string, committed, actual int64, providerRequestID string, usage quotaexchange.UsageRecord, metrics []store.GatewayMetric) error {
	token, ok := r.runtimeToken(rawAPIKey)
	if !ok {
		return errors.New("quota admission context is unavailable")
	}
	if err := r.outbox.CompleteRegionalExecution(ctx, r.processID, token, providerRequestID, usage, metrics, actual); err != nil {
		return err
	}
	r.local.Complete(rawAPIKey, committed, actual)
	return nil
}

// Run retries only Exchanges that this process already attempted and failed.
// Normal successful Usage remains batched until the next customer request hits
// its quota deadline or depletes the local counter. PostgreSQL retry timing
// prevents a failed Exchange from becoming a hot loop.
func (r *Runtime) Run(ctx context.Context, pollInterval time.Duration) {
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		case <-ticker.C:
		}
		r.flushRetries(ctx)
		r.cleanupIdleContexts(ctx)
	}
}

// cleanupIdleContexts bounds process memory even when a customer never sends
// another request after its quota answer expires. Taking the per-key Exchange
// lock prevents cleanup from racing a refresh that is about to install and
// consume a new answer. forgetContextIfIdle performs the final outbox check.
func (r *Runtime) cleanupIdleContexts(ctx context.Context) {
	r.tokenMu.Lock()
	keys := make([]string, 0, len(r.contexts))
	for _, runtimeContext := range r.contexts {
		keys = append(keys, runtimeContext.rawAPIKey)
	}
	r.tokenMu.Unlock()
	for _, rawAPIKey := range keys {
		if ctx.Err() != nil {
			return
		}
		unlock := r.lockExchange(rawAPIKey)
		r.forgetContextIfIdle(ctx, rawAPIKey)
		unlock()
	}
}

func (r *Runtime) scheduleRetry(rawAPIKey string) {
	digest := r.tokenDigest(rawAPIKey)
	r.retryMu.Lock()
	r.retrying[digest] = rawAPIKey
	r.retryMu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *Runtime) flushRetries(ctx context.Context) {
	r.retryMu.Lock()
	keys := make([]string, 0, len(r.retrying))
	for _, rawAPIKey := range r.retrying {
		keys = append(keys, rawAPIKey)
	}
	r.retryMu.Unlock()
	for _, rawAPIKey := range keys {
		if ctx.Err() != nil {
			return
		}
		r.flushKey(ctx, rawAPIKey)
	}
}

func (r *Runtime) flushKey(ctx context.Context, rawAPIKey string) {
	unlock := r.lockExchange(rawAPIKey)
	defer unlock()
	token, ok := r.runtimeToken(rawAPIKey)
	if !ok {
		r.forgetRetry(rawAPIKey)
		return
	}
	claimed, err := r.outbox.ClaimUsage(ctx, r.processID, token, 200, 256<<10)
	if err != nil {
		return
	}
	if len(claimed) != 0 {
		usage := make([]quotaexchange.UsageRecord, 0, len(claimed))
		ids := make([]string, 0, len(claimed))
		for _, row := range claimed {
			var record quotaexchange.UsageRecord
			if json.Unmarshal(row.Payload, &record) != nil {
				_ = r.outbox.MarkUsageFailed(ctx, []string{row.UCGID}, "stored Usage payload is invalid")
				continue
			}
			usage = append(usage, record)
			ids = append(ids, row.UCGID)
		}
		if len(usage) != 0 {
			response, err := r.exchanger.Exchange(ctx, rawAPIKey, usage)
			if err != nil {
				if !gizpayclient.IsPermanentExchangeError(err) {
					_ = r.outbox.ReturnUsagePending(ctx, ids, "quota exchange temporarily unavailable")
					return
				}
				_ = r.outbox.MarkUsageFailed(ctx, ids, permanentFailureReason(err))
				r.forgetRetry(rawAPIKey)
				if errors.Is(err, gizpayclient.ErrInvalidAPIKey) || errors.Is(err, gizpayclient.ErrInvalidNodeIdentity) {
					r.dropRuntimeContext(rawAPIKey)
				} else {
					r.forgetContextIfIdle(ctx, rawAPIKey)
				}
				return
			}
			if err := r.outbox.MarkUsageReported(ctx, ids); err != nil {
				return
			}
			r.replaceLocal(rawAPIKey, response)
		}
	}
	remaining, err := r.outbox.HasUnreportedUsage(ctx, r.processID, token)
	if err == nil && !remaining {
		r.forgetRetry(rawAPIKey)
		r.forgetContextIfIdle(ctx, rawAPIKey)
	}
}

func (r *Runtime) forgetRetry(rawAPIKey string) {
	digest := r.tokenDigest(rawAPIKey)
	r.retryMu.Lock()
	delete(r.retrying, digest)
	r.retryMu.Unlock()
}

// Release returns a failed provider request's whole local commitment. No Usage
// is queued because GizPay charges only Usage the Gateway actually reports.
func (r *Runtime) Release(rawAPIKey string, committed int64) {
	r.local.Complete(rawAPIKey, committed, 0)
}

func (r *Runtime) exchangeCurrent(ctx context.Context, rawAPIKey string) (gizpayclient.ExchangeResponse, error) {
	token, ok := r.runtimeToken(rawAPIKey)
	if !ok {
		return gizpayclient.ExchangeResponse{}, errors.New("quota admission context is unavailable")
	}
	claimed, err := r.outbox.ClaimUsage(ctx, r.processID, token, 200, 256<<10)
	if err != nil {
		return gizpayclient.ExchangeResponse{}, err
	}
	usage := make([]quotaexchange.UsageRecord, 0, len(claimed))
	ids := make([]string, 0, len(claimed))
	for _, row := range claimed {
		var record quotaexchange.UsageRecord
		if err := json.Unmarshal(row.Payload, &record); err != nil {
			_ = r.outbox.MarkUsageFailed(ctx, []string{row.UCGID}, "stored Usage payload is invalid")
			return gizpayclient.ExchangeResponse{}, errors.New("decode queued Usage")
		}
		usage = append(usage, record)
		ids = append(ids, row.UCGID)
	}
	response, err := r.exchanger.Exchange(ctx, rawAPIKey, usage)
	if err != nil {
		// A rejected or unavailable Usage batch does not make quota-only queries
		// illegal. Identity failures are the exception: the same raw key or node
		// certificate cannot become valid merely by omitting Usage, so they stop
		// immediately instead of issuing a redundant second request.
		if !gizpayclient.IsPermanentExchangeError(err) {
			_ = r.outbox.ReturnUsagePending(ctx, ids, "quota exchange temporarily unavailable")
			r.scheduleRetry(rawAPIKey)
		} else {
			_ = r.outbox.MarkUsageFailed(ctx, ids, permanentFailureReason(err))
			r.forgetRetry(rawAPIKey)
			if errors.Is(err, gizpayclient.ErrInvalidAPIKey) || errors.Is(err, gizpayclient.ErrInvalidNodeIdentity) {
				return gizpayclient.ExchangeResponse{}, err
			}
		}
		return r.exchanger.Exchange(ctx, rawAPIKey, nil)
	}
	if err := r.outbox.MarkUsageReported(ctx, ids); err != nil {
		r.scheduleRetry(rawAPIKey)
		return gizpayclient.ExchangeResponse{}, err
	}
	r.forgetRetry(rawAPIKey)
	return response, nil
}

func permanentFailureReason(err error) string {
	switch {
	case errors.Is(err, gizpayclient.ErrInvalidAPIKey):
		return "invalid API key"
	case errors.Is(err, gizpayclient.ErrInvalidNodeIdentity):
		return "invalid Gateway node identity"
	case errors.Is(err, gizpayclient.ErrUsageConflict):
		return "UCGID conflict"
	case errors.Is(err, gizpayclient.ErrUsageUnpriceable):
		return "usage unpriceable"
	default:
		return "invalid quota exchange payload"
	}
}

func (r *Runtime) forgetContextIfIdle(ctx context.Context, rawAPIKey string) {
	if !r.local.Forgettable(rawAPIKey) {
		return
	}
	token, ok := r.runtimeToken(rawAPIKey)
	if ok {
		pending, err := r.outbox.HasUnreportedUsage(ctx, r.processID, token)
		if err != nil || pending {
			return
		}
	}
	r.dropRuntimeContext(rawAPIKey)
}

func (r *Runtime) dropRuntimeContext(rawAPIKey string) {
	digest := r.tokenDigest(rawAPIKey)
	r.local.Delete(rawAPIKey)
	r.tokenMu.Lock()
	delete(r.contexts, digest)
	r.tokenMu.Unlock()
	r.retryMu.Lock()
	delete(r.retrying, digest)
	r.retryMu.Unlock()
}

func (r *Runtime) replaceLocal(rawAPIKey string, response gizpayclient.ExchangeResponse) {
	r.local.ReplaceResult(
		rawAPIKey,
		response.Quota.Microcredits,
		r.now().Add(time.Duration(response.RecheckAfterSeconds)*time.Second),
		response.Status == "denied",
	)
}

func (r *Runtime) ensureRuntimeToken(rawAPIKey string) string {
	digest := r.tokenDigest(rawAPIKey)
	r.tokenMu.Lock()
	defer r.tokenMu.Unlock()
	if runtimeContext, ok := r.contexts[digest]; ok {
		return runtimeContext.token
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		panic("Gateway quota runtime requires process entropy: " + err.Error())
	}
	token := hex.EncodeToString(random)
	r.contexts[digest] = runtimeContext{rawAPIKey: rawAPIKey, token: token}
	return token
}

func (r *Runtime) runtimeToken(rawAPIKey string) (string, bool) {
	digest := r.tokenDigest(rawAPIKey)
	r.tokenMu.Lock()
	defer r.tokenMu.Unlock()
	runtimeContext, ok := r.contexts[digest]
	return runtimeContext.token, ok
}

func (r *Runtime) tokenDigest(rawAPIKey string) [32]byte {
	mac := hmac.New(sha256.New, r.tokenSalt[:])
	_, _ = mac.Write([]byte(rawAPIKey))
	var result [32]byte
	copy(result[:], mac.Sum(nil))
	return result
}
