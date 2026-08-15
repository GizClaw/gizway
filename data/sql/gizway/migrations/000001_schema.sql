-- Milestone 03 regional extension schema. Bifrost owns its Config and Log
-- schemas in the same PostgreSQL database; GizWay owns only these extensions.

CREATE TABLE gizway_user_merchants (
    owner_identity_issuer TEXT NOT NULL,
    owner_identity_subject TEXT NOT NULL,
    merchant_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_identity_issuer, owner_identity_subject)
);

CREATE TABLE model_customer_prices (
    model_id TEXT NOT NULL,
    metric TEXT NOT NULL,
    unit_size BIGINT NOT NULL CHECK (unit_size > 0),
    price_microcredits BIGINT NOT NULL CHECK (price_microcredits >= 0),
    PRIMARY KEY (model_id, metric)
);

CREATE TABLE provider_key_billing (
    provider_key_id TEXT PRIMARY KEY,
    owner_identity_issuer TEXT NOT NULL,
    owner_identity_subject TEXT NOT NULL,
    merchant_id TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE provider_key_prices (
    provider_key_id TEXT NOT NULL REFERENCES provider_key_billing(provider_key_id) ON DELETE RESTRICT,
    model_id TEXT NOT NULL,
    metric TEXT NOT NULL,
    unit_size BIGINT NOT NULL CHECK (unit_size > 0),
    microcredits_per_unit BIGINT NOT NULL CHECK (microcredits_per_unit >= 0),
    PRIMARY KEY (provider_key_id, model_id, metric)
);

CREATE TABLE ai_orders (
    id TEXT PRIMARY KEY,
    external_order_id TEXT NOT NULL UNIQUE,
    provider_key_id TEXT NOT NULL,
    subscription_key_hmac TEXT NOT NULL,
    account_id TEXT NOT NULL,
    subscription_id TEXT NOT NULL,
    product_id TEXT NOT NULL,
    owner_identity_issuer TEXT NOT NULL,
    owner_identity_subject TEXT NOT NULL,
    model_id TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    gross_microcredits BIGINT NOT NULL CHECK (gross_microcredits > 0),
    commission_microcredits BIGINT NOT NULL CHECK (commission_microcredits >= 0),
    pricing_snapshot JSONB NOT NULL,
    provider_snapshot JSONB NOT NULL,
    billing_error JSONB,
    status TEXT NOT NULL CHECK (status IN ('pending','charged','billing_failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ
);

CREATE TABLE charge_outbox (
    id TEXT PRIMARY KEY,
    external_order_id TEXT NOT NULL UNIQUE,
    ai_order_id TEXT NOT NULL REFERENCES ai_orders(id) ON DELETE RESTRICT,
    payload JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','sending','reported','abandoned')),
    recover_duplicate BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE SCHEMA client_sync;
CREATE TABLE client_sync.models (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL,
    name TEXT NOT NULL,
    provider_model TEXT NOT NULL,
    status TEXT NOT NULL
);
CREATE TABLE client_sync.providers (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    kind TEXT NOT NULL,
    status TEXT NOT NULL
);
CREATE TABLE client_sync.provider_keys (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL,
    key TEXT NOT NULL,
    merchant_id TEXT NOT NULL,
    owner_identity_issuer TEXT NOT NULL,
    owner_identity_subject TEXT NOT NULL,
    status TEXT NOT NULL,
    prices_json JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE client_sync.ai_usage (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    order_id TEXT,
    model_id TEXT NOT NULL,
    metric TEXT NOT NULL,
    quantity BIGINT NOT NULL CHECK (quantity >= 0),
    owner_identity_issuer TEXT NOT NULL,
    owner_identity_subject TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);
