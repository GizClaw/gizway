-- Milestone 02 regional schema. The embedded engine owns its separate stores;
-- this migration creates only GizWay-owned tables in the current schema.

CREATE TABLE administrators (
    id TEXT PRIMARY KEY,
    identity_issuer TEXT NOT NULL,
    identity_subject TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (identity_issuer, identity_subject)
);

CREATE TABLE models (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE CHECK (length(trim(name)) > 0),
    provider_id TEXT NOT NULL CHECK (length(trim(provider_id)) > 0),
    provider_model TEXT NOT NULL CHECK (length(trim(provider_model)) > 0),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE model_customer_prices (
    model_id TEXT NOT NULL REFERENCES models(id),
    metric TEXT NOT NULL,
    unit_size BIGINT NOT NULL CHECK (unit_size > 0),
    price_microcredits BIGINT NOT NULL CHECK (price_microcredits >= 0),
    PRIMARY KEY (model_id, metric)
);

CREATE TABLE provider_key_billing (
    bifrost_key_id TEXT PRIMARY KEY,
    beneficiary_merchant_id TEXT NOT NULL CHECK (length(trim(beneficiary_merchant_id)) > 0),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive'))
);

CREATE TABLE provider_key_prices (
    bifrost_key_id TEXT NOT NULL REFERENCES provider_key_billing(bifrost_key_id),
    model_id TEXT NOT NULL REFERENCES models(id),
    metric TEXT NOT NULL,
    unit_size BIGINT NOT NULL CHECK (unit_size > 0),
    commission_microcredits BIGINT NOT NULL CHECK (commission_microcredits >= 0),
    PRIMARY KEY (bifrost_key_id, model_id, metric)
);

CREATE TABLE ai_orders (
    id TEXT PRIMARY KEY,
    external_order_id TEXT NOT NULL UNIQUE,
    key_hmac TEXT NOT NULL,
    product_id TEXT NOT NULL,
    model_id TEXT NOT NULL REFERENCES models(id),
    provider_id TEXT NOT NULL,
    bifrost_key_id TEXT NOT NULL,
    gross_microcredits BIGINT NOT NULL CHECK (gross_microcredits >= 0),
    commission_microcredits BIGINT NOT NULL CHECK (commission_microcredits >= 0),
    pricing_snapshot JSONB NOT NULL,
    provider_snapshot JSONB NOT NULL,
    billing_error JSONB,
    status TEXT NOT NULL CHECK (status IN ('pending','charged','billing_failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE charge_outbox (
    id TEXT PRIMARY KEY,
    external_order_id TEXT NOT NULL UNIQUE,
    ai_order_id TEXT NOT NULL REFERENCES ai_orders(id),
    payload JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','sending','reported','abandoned')),
    recover_duplicate BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
