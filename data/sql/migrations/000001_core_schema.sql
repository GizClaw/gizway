-- Canonical Gizway PostgreSQL schema. It uses
-- the connection's current schema so runtime queries need no session-local
-- search_path mutation and remain safe across the connection pool.
--
-- This migration is the database-structure source of truth. Relationships,
-- persisted states, constraints, indexes, triggers, and views are documented
-- beside the SQL that enforces them; do not copy them into a data-model
-- document. Executable API behavior remains in tests/api/stories/*.hurl.
--
-- Storage-wide invariants:
--   * one Credit is 1,000,000 microcredits and monetary values are integers;
--   * posted ledger history is immutable; corrections are compensating rows;
--   * prices and top-ups snapshot rates instead of rewriting history;
--   * metadata never owns money, access, state, idempotency, or retry decisions;
--   * PowerSync reads account-keyed, read-only, secret-free projections.

-- sqlx scans these values into stable Go string fields. A
-- fixed-width UTC domain makes lexical comparison temporally correct and
-- rejects RFC3339Nano's variable precision or non-UTC offsets at the database
-- boundary.
CREATE DOMAIN UTC_TIMESTAMP_TEXT AS TEXT
    CHECK (VALUE ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{9}Z$');

-- Customer identity and direct account ownership. Every user has at most one
-- personal account and may additionally own a merchant account. There is no
-- team or membership table.
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'closed')),
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    updated_at UTC_TIMESTAMP_TEXT NOT NULL
);

CREATE UNIQUE INDEX users_email_casefold_unique ON users(lower(email));

CREATE TABLE accounts (
    id TEXT PRIMARY KEY,
    owner_user_id TEXT NOT NULL REFERENCES users(id),
    kind TEXT NOT NULL CHECK (kind IN ('personal', 'merchant')),
    name TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'closed')),
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    updated_at UTC_TIMESTAMP_TEXT NOT NULL
);

CREATE UNIQUE INDEX accounts_one_personal_per_user
    ON accounts(owner_user_id) WHERE kind = 'personal';

CREATE TABLE user_sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id),
    secret_hash BYTEA NOT NULL UNIQUE,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked', 'expired')),
    expires_at UTC_TIMESTAMP_TEXT NOT NULL,
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    revoked_at UTC_TIMESTAMP_TEXT
);

CREATE TABLE merchant_accounts (
    account_id TEXT PRIMARY KEY REFERENCES accounts(id),
    owner_user_id TEXT NOT NULL UNIQUE REFERENCES users(id),
    legal_name TEXT NOT NULL,
    public_name TEXT NOT NULL,
    review_level TEXT NOT NULL DEFAULT 'basic' CHECK (review_level IN ('basic', 'enhanced')),
    merchant_status TEXT NOT NULL DEFAULT 'pending'
        CHECK (merchant_status IN ('pending', 'approved', 'rejected', 'suspended', 'closed')),
    country_code TEXT,
    website_url TEXT,
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    updated_at UTC_TIMESTAMP_TEXT NOT NULL
);

CREATE TABLE merchant_services (
    id TEXT PRIMARY KEY,
    merchant_account_id TEXT NOT NULL REFERENCES merchant_accounts(account_id),
    service_code TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    interface_set JSONB NOT NULL DEFAULT '[]'::jsonb,
    status TEXT NOT NULL CHECK (status IN ('pending','approved','rejected','suspended')),
    max_transaction_microcredits BIGINT NOT NULL CHECK (max_transaction_microcredits > 0),
    daily_limit_microcredits BIGINT NOT NULL CHECK (daily_limit_microcredits > 0),
    idempotency_key TEXT NOT NULL,
    payload_hash BYTEA NOT NULL,
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    updated_at UTC_TIMESTAMP_TEXT NOT NULL,
    UNIQUE(merchant_account_id, service_code),
    UNIQUE(merchant_account_id, idempotency_key)
);

CREATE TABLE risk_decisions (
    id TEXT PRIMARY KEY,
    merchant_account_id TEXT NOT NULL REFERENCES merchant_accounts(account_id),
    service_id TEXT NOT NULL REFERENCES merchant_services(id),
    provider_reference TEXT NOT NULL UNIQUE,
    decision TEXT NOT NULL CHECK (decision IN ('allow','deny','review')),
    kyc_status TEXT NOT NULL CHECK (kyc_status IN ('verified','failed','pending')),
    kyb_status TEXT NOT NULL CHECK (kyb_status IN ('verified','failed','pending')),
    sanctions_status TEXT NOT NULL CHECK (sanctions_status IN ('clear','match','pending')),
    anomaly_score BIGINT NOT NULL CHECK (anomaly_score BETWEEN 0 AND 100),
    reason TEXT NOT NULL,
    created_at UTC_TIMESTAMP_TEXT NOT NULL
);

CREATE TABLE api_keys (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(id),
    kind TEXT NOT NULL CHECK (kind IN ('gateway', 'payment')),
    name TEXT NOT NULL,
    key_prefix TEXT NOT NULL UNIQUE,
    secret_hash BYTEA NOT NULL,
    scopes JSONB NOT NULL DEFAULT '[]'::jsonb,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked', 'expired')),
    expires_at UTC_TIMESTAMP_TEXT,
    last_used_at UTC_TIMESTAMP_TEXT,
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    revoked_at UTC_TIMESTAMP_TEXT
    ,idempotency_key TEXT
    ,payload_hash BYTEA
    ,UNIQUE(account_id, idempotency_key)
);

-- Administrator credentials are isolated from customer credentials. Active
-- administrators share one authority level; no role/RBAC tables exist.
CREATE TABLE administrators (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL DEFAULT '',
    password_hash TEXT,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'closed')),
    last_login_at UTC_TIMESTAMP_TEXT,
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    updated_at UTC_TIMESTAMP_TEXT NOT NULL,
    closed_at UTC_TIMESTAMP_TEXT
);

CREATE UNIQUE INDEX administrators_email_casefold_unique ON administrators(lower(email));

CREATE TABLE admin_sessions (
    id TEXT PRIMARY KEY,
    administrator_id TEXT NOT NULL REFERENCES administrators(id),
    secret_hash BYTEA NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK (status IN ('active','revoked','expired')),
    expires_at UTC_TIMESTAMP_TEXT NOT NULL,
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    revoked_at UTC_TIMESTAMP_TEXT
);

CREATE TABLE admin_api_keys (
    id TEXT PRIMARY KEY,
    administrator_id TEXT NOT NULL REFERENCES administrators(id),
    name TEXT NOT NULL,
    key_prefix TEXT NOT NULL UNIQUE,
    secret_hash BYTEA NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked', 'expired')),
    expires_at UTC_TIMESTAMP_TEXT,
    last_used_at UTC_TIMESTAMP_TEXT,
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    revoked_at UTC_TIMESTAMP_TEXT
    ,idempotency_key TEXT
    ,payload_hash BYTEA
    ,UNIQUE(administrator_id, idempotency_key)
);

-- Provider catalog and routing. A public model may have multiple endpoint-
-- specific variants. credential_ref names externally managed secret material;
-- provider credential plaintext is not stored here.
CREATE TABLE providers (
    id TEXT PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    updated_at UTC_TIMESTAMP_TEXT NOT NULL
);

CREATE TABLE provider_endpoints (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL REFERENCES providers(id),
    name TEXT NOT NULL,
    base_url TEXT NOT NULL,
    credential_ref TEXT NOT NULL,
    region TEXT,
    priority BIGINT NOT NULL DEFAULT 100,
    weight BIGINT NOT NULL DEFAULT 100 CHECK (weight > 0),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'draining', 'disabled')),
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    updated_at UTC_TIMESTAMP_TEXT NOT NULL,
    UNIQUE(provider_id, name)
);

CREATE TABLE models (
    id TEXT PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    modality JSONB NOT NULL DEFAULT '["text"]'::jsonb,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'deprecated', 'disabled')),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    updated_at UTC_TIMESTAMP_TEXT NOT NULL
);

CREATE TABLE model_variants (
    id TEXT PRIMARY KEY,
    model_id TEXT NOT NULL REFERENCES models(id),
    provider_endpoint_id TEXT NOT NULL REFERENCES provider_endpoints(id),
    provider_model_name TEXT NOT NULL,
    variant_slug TEXT NOT NULL,
    capabilities JSONB NOT NULL DEFAULT '{}'::jsonb,
    context_window BIGINT,
    max_output_tokens BIGINT,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'degraded', 'disabled')),
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    updated_at UTC_TIMESTAMP_TEXT NOT NULL,
    UNIQUE(model_id, variant_slug),
    UNIQUE(provider_endpoint_id, provider_model_name)
);

-- Optional account policy overlays the global catalog. A deny always wins; as
-- soon as one allow exists the account enters allow-list mode. Keeping this as
-- normalized policy data makes discovery and execution share one SQL rule.
CREATE TABLE account_model_entitlements (
    account_id TEXT NOT NULL REFERENCES accounts(id),
    model_id TEXT NOT NULL REFERENCES models(id),
    effect TEXT NOT NULL CHECK (effect IN ('allow', 'deny')),
    reason TEXT NOT NULL,
    created_by_administrator_id TEXT NOT NULL REFERENCES administrators(id),
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    updated_at UTC_TIMESTAMP_TEXT NOT NULL,
    PRIMARY KEY (account_id, model_id)
);

-- Append-only, effective-dated billing snapshots. Each metric preserves
-- upstream cost, customer baseline, effective price, and discount so a later
-- catalog change cannot rewrite an already charged request.
CREATE TABLE model_variant_prices (
    id TEXT PRIMARY KEY,
    model_variant_id TEXT NOT NULL REFERENCES model_variants(id),
    metric TEXT NOT NULL CHECK (metric IN (
        'input_token', 'output_token', 'cached_input_token',
        'input_audio_token', 'output_audio_token',
        'audio_second', 'image', 'video_second', 'request'
    )),
    unit_size BIGINT NOT NULL DEFAULT 1 CHECK (unit_size > 0),
    upstream_cost_microcredits BIGINT NOT NULL CHECK (upstream_cost_microcredits >= 0),
    base_customer_price_microcredits BIGINT NOT NULL CHECK (base_customer_price_microcredits >= 0),
    customer_price_microcredits BIGINT NOT NULL CHECK (customer_price_microcredits >= 0),
    discount_bps BIGINT NOT NULL DEFAULT 0 CHECK (discount_bps BETWEEN 0 AND 10000),
    valid_from UTC_TIMESTAMP_TEXT NOT NULL,
    valid_until UTC_TIMESTAMP_TEXT,
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    CHECK (customer_price_microcredits <= base_customer_price_microcredits),
    UNIQUE(model_variant_id, metric, valid_from)
);

-- AI execution journal. Usage belongs to the exact API key, charges link to
-- the exact metric-price rows, and reservations protect available Credit until
-- settlement or release.
CREATE TABLE gateway_requests (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(id),
    api_key_id TEXT NOT NULL REFERENCES api_keys(id),
    model_id TEXT REFERENCES models(id),
    model_variant_id TEXT REFERENCES model_variants(id),
    operation TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    payload_hash BYTEA NOT NULL,
    provider_request_id TEXT,
    protocol TEXT NOT NULL DEFAULT 'https' CHECK (protocol IN ('https', 'websocket', 'webrtc')),
    status TEXT NOT NULL CHECK (status IN ('started', 'succeeded', 'failed', 'cancelled')),
    input_tokens BIGINT NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens BIGINT NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    cached_input_tokens BIGINT NOT NULL DEFAULT 0 CHECK (cached_input_tokens >= 0),
    input_audio_tokens BIGINT NOT NULL DEFAULT 0 CHECK (input_audio_tokens >= 0),
    output_audio_tokens BIGINT NOT NULL DEFAULT 0 CHECK (output_audio_tokens >= 0),
    charged_microcredits BIGINT NOT NULL DEFAULT 0 CHECK (charged_microcredits >= 0),
    response_json TEXT CHECK (response_json IS NULL OR jsonb_typeof(response_json::jsonb) IS NOT NULL),
    error_code TEXT,
    started_at UTC_TIMESTAMP_TEXT NOT NULL,
    execution_lease_until UTC_TIMESTAMP_TEXT,
    execution_attempts BIGINT NOT NULL DEFAULT 1 CHECK (execution_attempts > 0),
    -- Encrypted immutable provider/candidate/price plan. A retry after a
    -- process crash must stay in the original provider idempotency domain even
    -- when an administrator has since changed the live catalog.
    execution_snapshot BYTEA,
    -- Encrypted, secret-free public request envelope. An expired execution
    -- lease can be replayed by the recovery worker even when the original HTTP
    -- client never reconnects; bearer/API-key secrets are never persisted.
    recovery_request BYTEA,
    recovery_status TEXT CHECK (recovery_status IS NULL OR recovery_status IN ('pending', 'completed', 'reconciliation_required')),
    recovery_attempts BIGINT NOT NULL DEFAULT 0 CHECK (recovery_attempts >= 0),
    recovery_next_attempt_at UTC_TIMESTAMP_TEXT,
    recovery_last_error TEXT,
    completed_at UTC_TIMESTAMP_TEXT,
    UNIQUE(api_key_id, operation, idempotency_key)
);

CREATE TABLE gateway_request_charges (
    id TEXT PRIMARY KEY,
    gateway_request_id TEXT NOT NULL REFERENCES gateway_requests(id),
    model_variant_price_id TEXT NOT NULL REFERENCES model_variant_prices(id),
    metric TEXT NOT NULL,
    quantity BIGINT NOT NULL CHECK (quantity >= 0),
    unit_size BIGINT NOT NULL CHECK (unit_size > 0),
    base_price_microcredits BIGINT NOT NULL CHECK (base_price_microcredits >= 0),
    effective_price_microcredits BIGINT NOT NULL CHECK (effective_price_microcredits >= 0),
    discount_bps BIGINT NOT NULL CHECK (discount_bps BETWEEN 0 AND 10000),
    charged_microcredits BIGINT NOT NULL CHECK (charged_microcredits >= 0),
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    UNIQUE(gateway_request_id, metric)
);

CREATE TABLE credit_reservations (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(id),
    api_key_id TEXT NOT NULL REFERENCES api_keys(id),
    amount_microcredits BIGINT NOT NULL CHECK (amount_microcredits > 0),
    status TEXT NOT NULL CHECK (status IN ('active', 'settled', 'released')),
    idempotency_key TEXT NOT NULL,
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    completed_at UTC_TIMESTAMP_TEXT,
    UNIQUE(account_id, idempotency_key)
);

CREATE TABLE gateway_settlement_outbox (
    request_id TEXT PRIMARY KEY REFERENCES gateway_requests(id),
    provider_request_id TEXT NOT NULL,
    resolved_variant_id TEXT NOT NULL,
    metrics_json JSONB NOT NULL,
    response_json TEXT NOT NULL CHECK (jsonb_typeof(response_json::jsonb) IS NOT NULL),
    completed_at UTC_TIMESTAMP_TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','succeeded')),
    attempts BIGINT NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error TEXT,
    updated_at UTC_TIMESTAMP_TEXT NOT NULL
);

-- Realtime client secrets are single-session credentials. The plaintext value
-- is returned once; only SHA-256 is persisted. The linked gateway request owns
-- reservation and final usage settlement for the complete connection.
CREATE TABLE realtime_sessions (
    id TEXT PRIMARY KEY,
    gateway_request_id TEXT NOT NULL UNIQUE REFERENCES gateway_requests(id),
    account_id TEXT NOT NULL REFERENCES accounts(id),
    api_key_id TEXT NOT NULL REFERENCES api_keys(id),
    model_id TEXT NOT NULL REFERENCES models(id),
    model_variant_id TEXT NOT NULL REFERENCES model_variants(id),
    public_model TEXT NOT NULL,
    provider_model TEXT NOT NULL,
    client_secret_hash BYTEA NOT NULL UNIQUE,
    transport TEXT NOT NULL CHECK (transport IN ('websocket','webrtc')),
    status TEXT NOT NULL CHECK (status IN ('created','connected','succeeded','failed','cancelled','expired')),
    idempotency_key TEXT NOT NULL,
    payload_hash BYTEA NOT NULL,
    expires_at UTC_TIMESTAMP_TEXT NOT NULL,
    deadline_at UTC_TIMESTAMP_TEXT NOT NULL,
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    connected_at UTC_TIMESTAMP_TEXT,
    completed_at UTC_TIMESTAMP_TEXT,
    UNIQUE(api_key_id,idempotency_key)
);

-- WebRTC media flows directly between the client and provider, so exact
-- billing arrives through a signed server-to-server terminal usage event.
-- The event journal makes retries observable and rejects event-id reuse with
-- a different payload.
CREATE TABLE realtime_provider_events (
    event_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL UNIQUE REFERENCES realtime_sessions(id),
    payload_hash BYTEA NOT NULL,
	input_tokens BIGINT NOT NULL CHECK (input_tokens >= 0),
	output_tokens BIGINT NOT NULL CHECK (output_tokens >= 0),
	cached_input_tokens BIGINT NOT NULL DEFAULT 0 CHECK (cached_input_tokens >= 0),
	input_audio_tokens BIGINT NOT NULL DEFAULT 0 CHECK (input_audio_tokens >= 0),
	output_audio_tokens BIGINT NOT NULL DEFAULT 0 CHECK (output_audio_tokens >= 0),
    status TEXT NOT NULL CHECK (status IN ('received','processed')),
    received_at UTC_TIMESTAMP_TEXT NOT NULL,
    processed_at UTC_TIMESTAMP_TEXT
);

-- External-payment state machine with an immutable rate snapshot. Integer
-- conversion uses these columns for both issuance and original-route refund.
-- credit_lots track remaining purchased Credit, so only unused purchased
-- Credit can return to its original provider payment. Transferred and
-- merchant-earned Credit have no cash-refund route.
CREATE TABLE topups (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(id),
    payment_provider TEXT NOT NULL,
    provider_reference TEXT NOT NULL UNIQUE,
    fiat_currency TEXT NOT NULL CHECK (length(fiat_currency) = 3),
    fiat_amount_minor BIGINT NOT NULL CHECK (fiat_amount_minor > 0),
    base_fiat_minor BIGINT NOT NULL CHECK (base_fiat_minor > 0),
    base_credit_microcredits BIGINT NOT NULL CHECK (base_credit_microcredits > 0),
    effective_fiat_minor BIGINT NOT NULL CHECK (effective_fiat_minor > 0),
    effective_credit_microcredits BIGINT NOT NULL CHECK (effective_credit_microcredits > 0),
    discount_bps BIGINT NOT NULL CHECK (discount_bps BETWEEN 0 AND 10000),
    credit_microcredits BIGINT NOT NULL CHECK (credit_microcredits > 0),
    refundable_microcredits BIGINT NOT NULL DEFAULT 0 CHECK (refundable_microcredits >= 0),
    status TEXT NOT NULL CHECK (status IN ('pending','succeeded','partially_refunded','refunded','failed')),
    checkout_url TEXT,
    idempotency_key TEXT NOT NULL,
    payload_hash BYTEA NOT NULL,
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    completed_at UTC_TIMESTAMP_TEXT,
    UNIQUE(account_id, idempotency_key)
);

CREATE TABLE payment_provider_events (
    event_id TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    provider_reference TEXT NOT NULL,
    payload_hash BYTEA NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('processed','quarantined')),
    error_code TEXT,
    received_at UTC_TIMESTAMP_TEXT NOT NULL
);

CREATE TABLE credit_lots (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(id),
    topup_id TEXT NOT NULL UNIQUE REFERENCES topups(id),
    original_microcredits BIGINT NOT NULL CHECK (original_microcredits > 0),
    remaining_microcredits BIGINT NOT NULL CHECK (remaining_microcredits >= 0),
    created_at UTC_TIMESTAMP_TEXT NOT NULL
);

CREATE TABLE invoices (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(id),
    topup_id TEXT NOT NULL UNIQUE REFERENCES topups(id),
    invoice_number TEXT NOT NULL UNIQUE,
    fiat_currency TEXT NOT NULL,
    fiat_amount_minor BIGINT NOT NULL,
    issued_at UTC_TIMESTAMP_TEXT NOT NULL
);

CREATE TABLE refunds (
    id TEXT PRIMARY KEY,
    topup_id TEXT NOT NULL REFERENCES topups(id),
    account_id TEXT NOT NULL REFERENCES accounts(id),
    provider_refund_id TEXT,
    credit_microcredits BIGINT NOT NULL CHECK (credit_microcredits > 0),
    fiat_amount_minor BIGINT NOT NULL CHECK (fiat_amount_minor > 0),
    status TEXT NOT NULL CHECK (status IN ('pending','succeeded','failed')),
    idempotency_key TEXT NOT NULL,
    payload_hash BYTEA NOT NULL,
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    completed_at UTC_TIMESTAMP_TEXT,
    UNIQUE(account_id, idempotency_key)
);

-- Double-entry Credit ledger. Entries are authoritative monetary facts;
-- workflow tables only link to their postings. Deferred triggers below require
-- every posted PostgreSQL transaction to balance.
CREATE TABLE ledger_accounts (
    id TEXT PRIMARY KEY,
    owner_account_id TEXT REFERENCES accounts(id),
    code TEXT NOT NULL UNIQUE,
    kind TEXT NOT NULL,
    asset_code TEXT NOT NULL DEFAULT 'GIZ_CREDIT',
    normal_balance TEXT NOT NULL CHECK (normal_balance IN ('debit', 'credit')),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'frozen', 'closed')),
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    updated_at UTC_TIMESTAMP_TEXT NOT NULL
);

CREATE TABLE ledger_transactions (
    id TEXT PRIMARY KEY,
    transaction_type TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'posted', 'reversed', 'failed')),
    idempotency_key TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    initiated_by_account_id TEXT REFERENCES accounts(id),
    reference_type TEXT,
    reference_id TEXT,
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    posted_at UTC_TIMESTAMP_TEXT
);
CREATE UNIQUE INDEX ledger_single_reversal
    ON ledger_transactions(reference_id)
    WHERE transaction_type='reversal' AND reference_type='ledger_transaction';

CREATE TABLE credit_transfers (
    id TEXT PRIMARY KEY,
    sender_account_id TEXT NOT NULL REFERENCES accounts(id),
    recipient_account_id TEXT NOT NULL REFERENCES accounts(id),
    amount_microcredits BIGINT NOT NULL CHECK (amount_microcredits > 0),
    status TEXT NOT NULL CHECK (status IN ('pending', 'succeeded', 'failed', 'cancelled')),
    note TEXT NOT NULL DEFAULT '',
    idempotency_key TEXT NOT NULL,
    payload_hash BYTEA NOT NULL,
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    completed_at UTC_TIMESTAMP_TEXT,
    UNIQUE(sender_account_id, idempotency_key),
    CHECK(sender_account_id <> recipient_account_id)
);

CREATE TABLE ledger_entries (
    id TEXT PRIMARY KEY,
    transaction_id TEXT NOT NULL REFERENCES ledger_transactions(id),
    ledger_account_id TEXT NOT NULL REFERENCES ledger_accounts(id),
    sequence BIGINT NOT NULL,
    direction TEXT NOT NULL CHECK (direction IN ('debit', 'credit')),
    amount_microcredits BIGINT NOT NULL CHECK (amount_microcredits > 0),
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    UNIQUE(transaction_id, sequence)
);

-- Posted financial history is append-only. Corrections are represented by a
-- new compensating transaction, never by rewriting historical rows.
CREATE OR REPLACE FUNCTION prevent_posted_ledger_entry_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE transaction_status TEXT;
BEGIN
    SELECT status INTO transaction_status
      FROM ledger_transactions
     WHERE id = OLD.transaction_id;
    IF transaction_status = 'posted' THEN
        RAISE EXCEPTION 'posted ledger entries are immutable';
    END IF;
    RETURN OLD;
END;
$$;

CREATE TRIGGER ledger_entries_no_update_posted
BEFORE UPDATE OR DELETE ON ledger_entries
FOR EACH ROW EXECUTE FUNCTION prevent_posted_ledger_entry_mutation();

CREATE OR REPLACE FUNCTION prevent_posted_ledger_transaction_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status = 'posted' THEN
        RAISE EXCEPTION 'posted ledger transactions are immutable';
    END IF;
    RETURN OLD;
END;
$$;

CREATE TRIGGER ledger_transactions_no_update_posted
BEFORE UPDATE OR DELETE ON ledger_transactions
FOR EACH ROW EXECUTE FUNCTION prevent_posted_ledger_transaction_mutation();

-- A transaction is inserted as posted before its entries are appended in the
-- same SQL transaction. Deferrable constraint triggers validate the final
-- committed shape, not the intermediate statement order.
CREATE OR REPLACE FUNCTION assert_ledger_transaction_balanced()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    target_id TEXT;
    transaction_status TEXT;
    net_amount NUMERIC;
BEGIN
    IF TG_TABLE_NAME = 'ledger_transactions' THEN
        target_id := NEW.id;
    ELSE
        target_id := COALESCE(NEW.transaction_id, OLD.transaction_id);
    END IF;
    SELECT status INTO transaction_status FROM ledger_transactions WHERE id = target_id;
    IF transaction_status = 'posted' THEN
        SELECT COALESCE(SUM(CASE direction WHEN 'debit' THEN amount_microcredits ELSE -amount_microcredits END), 0)
          INTO net_amount FROM ledger_entries WHERE transaction_id = target_id;
        IF net_amount <> 0 THEN
            RAISE EXCEPTION 'ledger transaction % is not balanced', target_id;
        END IF;
    END IF;
    RETURN COALESCE(NEW, OLD);
END;
$$;

CREATE CONSTRAINT TRIGGER ledger_entries_balanced
AFTER INSERT OR UPDATE OR DELETE ON ledger_entries
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION assert_ledger_transaction_balanced();

CREATE CONSTRAINT TRIGGER ledger_transactions_balanced
AFTER INSERT OR UPDATE ON ledger_transactions
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION assert_ledger_transaction_balanced();

-- Merchant checkout and settlement. Gross, platform fee, and merchant net are
-- persisted independently. Completed payments are corrected only by a linked
-- compensating reversal.
CREATE TABLE payment_intents (
    id TEXT PRIMARY KEY,
    merchant_account_id TEXT NOT NULL REFERENCES merchant_accounts(account_id),
    service_id TEXT NOT NULL REFERENCES merchant_services(id),
    payer_account_id TEXT REFERENCES accounts(id),
    external_order_id TEXT NOT NULL,
    amount_microcredits BIGINT NOT NULL CHECK (amount_microcredits > 0),
    platform_fee_microcredits BIGINT NOT NULL CHECK (platform_fee_microcredits >= 0),
    net_microcredits BIGINT NOT NULL CHECK (net_microcredits >= 0),
    fee_bps BIGINT NOT NULL CHECK (fee_bps BETWEEN 0 AND 10000),
    status TEXT NOT NULL CHECK (status IN ('created','authorized','succeeded','expired','cancelled','failed')),
    description TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    checkout_url TEXT NOT NULL,
    expires_at UTC_TIMESTAMP_TEXT NOT NULL,
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    completed_at UTC_TIMESTAMP_TEXT,
    create_idempotency_key TEXT NOT NULL,
    create_payload_hash BYTEA NOT NULL,
    confirm_idempotency_key TEXT,
    UNIQUE(merchant_account_id, external_order_id),
    UNIQUE(merchant_account_id, create_idempotency_key)
);

CREATE TABLE merchant_transactions (
    id TEXT PRIMARY KEY,
    payment_intent_id TEXT NOT NULL UNIQUE REFERENCES payment_intents(id),
    merchant_account_id TEXT NOT NULL REFERENCES accounts(id),
    gross_microcredits BIGINT NOT NULL,
    platform_fee_microcredits BIGINT NOT NULL,
    net_microcredits BIGINT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending','posted','reversed','failed')),
    created_at UTC_TIMESTAMP_TEXT NOT NULL
);

-- A settled merchant payment is never edited back to a pre-settlement state.
-- A full reversal is a separately addressable, idempotent compensating
-- workflow linked to both the payment intent and its reverse ledger posting.
CREATE TABLE merchant_payment_reversals (
    id TEXT PRIMARY KEY,
    payment_intent_id TEXT NOT NULL UNIQUE REFERENCES payment_intents(id),
    merchant_account_id TEXT NOT NULL REFERENCES accounts(id),
    payer_account_id TEXT NOT NULL REFERENCES accounts(id),
    gross_microcredits BIGINT NOT NULL CHECK (gross_microcredits > 0),
    platform_fee_microcredits BIGINT NOT NULL CHECK (platform_fee_microcredits >= 0),
    net_microcredits BIGINT NOT NULL CHECK (net_microcredits >= 0),
    status TEXT NOT NULL CHECK (status IN ('succeeded','failed')),
    reason TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    payload_hash BYTEA NOT NULL,
    ledger_transaction_id TEXT NOT NULL UNIQUE REFERENCES ledger_transactions(id),
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    UNIQUE(merchant_account_id, idempotency_key)
);

-- Merchant webhook outbox. Events are immutable facts; deliveries are leased
-- retry attempts with a signing-secret snapshot, so endpoint changes cannot
-- alter an already queued delivery.
CREATE TABLE webhook_endpoints (
    id TEXT PRIMARY KEY,
    merchant_account_id TEXT NOT NULL REFERENCES accounts(id),
    url TEXT NOT NULL,
    events JSONB NOT NULL,
    signing_secret TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active','disabled')),
    idempotency_key TEXT,
    payload_hash BYTEA,
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    updated_at UTC_TIMESTAMP_TEXT NOT NULL,
    deleted_at UTC_TIMESTAMP_TEXT,
    UNIQUE(merchant_account_id, idempotency_key)
);

CREATE TABLE webhook_endpoint_commands (
    id TEXT PRIMARY KEY,
    merchant_account_id TEXT NOT NULL REFERENCES accounts(id),
    endpoint_id TEXT NOT NULL REFERENCES webhook_endpoints(id),
    operation TEXT NOT NULL CHECK (operation IN ('status','rotate_secret','delete')),
    idempotency_key TEXT NOT NULL,
    payload_hash BYTEA NOT NULL,
    response_json TEXT CHECK (response_json IS NULL OR jsonb_typeof(response_json::jsonb) IS NOT NULL),
    secret_result TEXT,
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    UNIQUE(merchant_account_id, operation, idempotency_key)
);

CREATE TABLE webhook_events (
    id TEXT PRIMARY KEY,
    merchant_account_id TEXT NOT NULL REFERENCES accounts(id),
    event_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    payload TEXT NOT NULL CHECK (jsonb_typeof(payload::jsonb) IS NOT NULL),
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    UNIQUE(event_type, resource_id)
);

CREATE TABLE webhook_deliveries (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL REFERENCES webhook_events(id),
    endpoint_id TEXT NOT NULL REFERENCES webhook_endpoints(id),
    signing_secret_snapshot TEXT,
    attempt BIGINT NOT NULL CHECK (attempt > 0),
    status TEXT NOT NULL CHECK (status IN ('pending','delivering','succeeded','failed','exhausted')),
    response_status BIGINT,
    error TEXT,
    claimed_at UTC_TIMESTAMP_TEXT,
    lease_until UTC_TIMESTAMP_TEXT,
    next_attempt_at UTC_TIMESTAMP_TEXT,
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    completed_at UTC_TIMESTAMP_TEXT,
    UNIQUE(event_id, endpoint_id, attempt)
);

-- A failed delivery may have exactly one automatic or administrator-created
-- successor. This database invariant closes the race between the worker and
-- an administrator retrying the same event/endpoint pair.
CREATE UNIQUE INDEX webhook_single_active_attempt
    ON webhook_deliveries(event_id, endpoint_id)
    WHERE status IN ('pending','delivering');

-- Durable administrator command record: retry is an externally observable
-- mutation and must replay the same delivery ID without duplicating its audit.
CREATE TABLE admin_webhook_retry_commands (
    id TEXT PRIMARY KEY,
    administrator_id TEXT NOT NULL REFERENCES administrators(id),
    original_delivery_id TEXT NOT NULL REFERENCES webhook_deliveries(id),
    result_delivery_id TEXT NOT NULL REFERENCES webhook_deliveries(id),
    idempotency_key TEXT NOT NULL,
    payload_hash BYTEA NOT NULL,
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    UNIQUE(administrator_id, idempotency_key)
);

CREATE TABLE api_idempotency_commands (
    id TEXT PRIMARY KEY,
    credential_hash BYTEA NOT NULL,
    operation TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    payload_hash BYTEA NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('started','completed')),
    response_status BIGINT,
    response_content_type TEXT,
    response_body BYTEA,
    created_at UTC_TIMESTAMP_TEXT NOT NULL,
    expires_at UTC_TIMESTAMP_TEXT NOT NULL,
    completed_at UTC_TIMESTAMP_TEXT,
    UNIQUE(credential_hash,operation,idempotency_key)
);

-- Shared fixed-window counters keep abuse controls consistent across process
-- replicas and PostgreSQL-backed behavior tests.
CREATE TABLE request_rate_limits (
    scope_key TEXT NOT NULL,
    action TEXT NOT NULL,
    window_started_at UTC_TIMESTAMP_TEXT NOT NULL,
    request_count BIGINT NOT NULL CHECK (request_count > 0),
    updated_at UTC_TIMESTAMP_TEXT NOT NULL,
    PRIMARY KEY (scope_key, action, window_started_at)
);

CREATE VIEW account_balances AS
SELECT
    la.owner_account_id AS account_id,
    la.asset_code,
    COALESCE(SUM(CASE
        WHEN lt.status <> 'posted' THEN 0
        WHEN le.direction = la.normal_balance THEN le.amount_microcredits
        ELSE -le.amount_microcredits
    END), 0) AS balance_microcredits,
    MAX(CASE WHEN lt.status = 'posted' THEN le.created_at END) AS updated_at
FROM ledger_accounts la
LEFT JOIN ledger_entries le ON le.ledger_account_id = la.id
LEFT JOIN ledger_transactions lt ON lt.id = le.transaction_id
WHERE la.owner_account_id IS NOT NULL
GROUP BY la.owner_account_id, la.asset_code;

-- PowerSync reads only these account-keyed, secret-free projections. Views are
-- intentionally non-updatable and carry account_id on every row so sync rules
-- can bind one authenticated claim before returning data.
CREATE VIEW powersync_account_balances AS
SELECT account_id, asset_code, balance_microcredits, updated_at
FROM account_balances;

CREATE VIEW powersync_gateway_usage AS
SELECT id, account_id, api_key_id, model_id, model_variant_id, protocol, status,
       input_tokens, output_tokens, cached_input_tokens, input_audio_tokens,
       output_audio_tokens, charged_microcredits, started_at, completed_at
FROM gateway_requests;

CREATE VIEW powersync_credit_transfers AS
SELECT sender_account_id AS account_id, id, 'outgoing'::TEXT AS direction,
       sender_account_id, recipient_account_id, amount_microcredits, status,
       note, created_at, completed_at
FROM credit_transfers
UNION ALL
SELECT recipient_account_id AS account_id, id, 'incoming'::TEXT AS direction,
       sender_account_id, recipient_account_id, amount_microcredits, status,
       note, created_at, completed_at
FROM credit_transfers;

CREATE VIEW powersync_payments AS
SELECT account_id, id, 'topup'::TEXT AS payment_type, credit_microcredits AS amount_microcredits, status, created_at, completed_at
FROM topups
UNION ALL
SELECT account_id, id, 'refund'::TEXT AS payment_type, credit_microcredits AS amount_microcredits, status, created_at, completed_at
FROM refunds
UNION ALL
SELECT merchant_account_id AS account_id, id, 'merchant_payment'::TEXT AS payment_type, amount_microcredits, status, created_at, completed_at
FROM payment_intents;

CREATE VIEW powersync_merchant_orders AS
SELECT merchant_account_id AS account_id, id, merchant_account_id, service_id,
       payer_account_id, external_order_id, amount_microcredits,
       platform_fee_microcredits, net_microcredits, status, description,
       expires_at, created_at, completed_at
FROM payment_intents
UNION ALL
SELECT payer_account_id AS account_id, id, merchant_account_id, service_id,
       payer_account_id, external_order_id, amount_microcredits,
       platform_fee_microcredits, net_microcredits, status, description,
       expires_at, created_at, completed_at
FROM payment_intents WHERE payer_account_id IS NOT NULL;

-- Audits are written in the same transaction as mutations. Metadata is kept as
-- JSON metadata must never contain credential plaintext or hashes.
CREATE TABLE audit_events (
    sequence BIGSERIAL PRIMARY KEY,
    id TEXT NOT NULL UNIQUE,
    actor_type TEXT NOT NULL CHECK (actor_type IN ('user', 'administrator', 'api_key', 'system')),
    actor_id TEXT NOT NULL,
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    reason TEXT,
    request_id TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at UTC_TIMESTAMP_TEXT NOT NULL
);

CREATE INDEX audit_events_resource_idx
    ON audit_events(resource_type, resource_id, created_at);
