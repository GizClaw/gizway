-- Milestone 03 is an empty-database breaking schema. There is deliberately no
-- upgrade migration, compatibility view, or legacy Key encryption surface.

CREATE TABLE users (
    id TEXT PRIMARY KEY,
    identity_issuer TEXT NOT NULL,
    identity_subject TEXT NOT NULL,
    email TEXT NOT NULL DEFAULT '',
    display_name TEXT NOT NULL CHECK (length(trim(display_name)) > 0),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (identity_issuer, identity_subject)
);

CREATE TABLE accounts (
    id TEXT PRIMARY KEY,
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (owner_user_id)
);

CREATE TABLE service_principals (
    id TEXT PRIMARY KEY,
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    identity_issuer TEXT NOT NULL,
    identity_subject TEXT NOT NULL,
    credential_key_id TEXT,
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    roles JSONB NOT NULL DEFAULT '[]'::jsonb,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','revoked')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ,
    UNIQUE (identity_issuer, identity_subject),
    CHECK ((status='active' AND revoked_at IS NULL) OR (status='revoked' AND revoked_at IS NOT NULL))
);

CREATE TABLE ledger_accounts (
    id TEXT PRIMARY KEY,
    owner_account_id TEXT REFERENCES accounts(id) ON DELETE RESTRICT,
    asset_code TEXT NOT NULL DEFAULT 'credit',
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
    UNIQUE (owner_account_id, asset_code)
);

INSERT INTO ledger_accounts(id,owner_account_id,asset_code,status) VALUES
    ('led_clearing',NULL,'clearing','active'),
    ('led_platform_fee',NULL,'platform_fee','active');

CREATE TABLE ledger_transactions (
    id TEXT PRIMARY KEY,
    transaction_type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','posted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE ledger_entries (
    id TEXT PRIMARY KEY,
    transaction_id TEXT NOT NULL REFERENCES ledger_transactions(id) ON DELETE RESTRICT,
    ledger_account_id TEXT NOT NULL REFERENCES ledger_accounts(id) ON DELETE RESTRICT,
    direction TEXT NOT NULL CHECK (direction IN ('debit','credit')),
    amount_microcredits BIGINT NOT NULL CHECK (amount_microcredits > 0)
);

CREATE VIEW account_balances AS
SELECT a.id AS account_id,
       COALESCE(SUM(CASE WHEN t.status='posted' AND e.direction='credit' THEN e.amount_microcredits
                         WHEN t.status='posted' AND e.direction='debit' THEN -e.amount_microcredits
                         ELSE 0 END),0)::BIGINT AS balance_microcredits
FROM accounts a
LEFT JOIN ledger_accounts la ON la.owner_account_id=a.id AND la.asset_code='credit'
LEFT JOIN ledger_entries e ON e.ledger_account_id=la.id
LEFT JOIN ledger_transactions t ON t.id=e.transaction_id
GROUP BY a.id;

CREATE TABLE merchants (
    id TEXT PRIMARY KEY,
    settlement_account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    legal_name TEXT NOT NULL CHECK (length(trim(legal_name)) > 0),
    public_name TEXT NOT NULL CHECK (length(trim(public_name)) > 0),
    is_default BOOLEAN NOT NULL DEFAULT false,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX merchants_one_default_per_account
    ON merchants(settlement_account_id) WHERE is_default=true;

CREATE TABLE products (
    id TEXT PRIMARY KEY,
    merchant_id TEXT NOT NULL REFERENCES merchants(id) ON DELETE RESTRICT,
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    billing_mode TEXT NOT NULL DEFAULT 'pay_as_you_go' CHECK (billing_mode IN ('pay_as_you_go')),
    published BOOLEAN NOT NULL DEFAULT true,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
    terms_version TEXT NOT NULL CHECK (length(trim(terms_version)) > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE subscriptions (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    product_id TEXT NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','paused','inactive')),
    terms_version TEXT NOT NULL DEFAULT 'current' CHECK (length(trim(terms_version)) > 0),
    accepted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    canceled_at TIMESTAMPTZ,
    UNIQUE (account_id, product_id)
);

CREATE TABLE subscription_keys (
    id TEXT PRIMARY KEY,
    subscription_id TEXT NOT NULL REFERENCES subscriptions(id) ON DELETE RESTRICT,
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    key TEXT NOT NULL UNIQUE CHECK (length(key) > 0),
    subscription_key_hmac TEXT NOT NULL UNIQUE CHECK (length(subscription_key_hmac) > 0),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','revoked')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    CHECK ((status='active' AND revoked_at IS NULL) OR (status='revoked' AND revoked_at IS NOT NULL))
);

CREATE TABLE payg_charges (
    id TEXT PRIMARY KEY,
    external_order_id TEXT NOT NULL UNIQUE,
    subscription_id TEXT NOT NULL REFERENCES subscriptions(id) ON DELETE RESTRICT,
    service_principal_id TEXT NOT NULL REFERENCES service_principals(id) ON DELETE RESTRICT,
    gross_microcredits BIGINT NOT NULL CHECK (gross_microcredits > 0),
    platform_fee_microcredits BIGINT NOT NULL CHECK (platform_fee_microcredits >= 0),
    main_merchant_net_microcredits BIGINT NOT NULL,
    order_snapshot JSONB NOT NULL,
    ledger_transaction_id TEXT NOT NULL UNIQUE REFERENCES ledger_transactions(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE charge_commissions (
    charge_id TEXT NOT NULL REFERENCES payg_charges(id) ON DELETE RESTRICT,
    merchant_id TEXT NOT NULL REFERENCES merchants(id) ON DELETE RESTRICT,
    settlement_account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    amount_microcredits BIGINT NOT NULL CHECK (amount_microcredits >= 0),
    PRIMARY KEY (charge_id, merchant_id)
);

CREATE TABLE topups (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    channel TEXT NOT NULL CHECK (channel IN ('fake')),
    external_reference TEXT NOT NULL CHECK (length(trim(external_reference)) > 0),
    amount_microcredits BIGINT NOT NULL CHECK (amount_microcredits > 0),
    status TEXT NOT NULL CHECK (status IN ('succeeded')),
    ledger_transaction_id TEXT NOT NULL UNIQUE REFERENCES ledger_transactions(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    credited_at TIMESTAMPTZ NOT NULL,
    UNIQUE (channel, external_reference)
);

CREATE TABLE product_listings (
    id TEXT PRIMARY KEY,
    product_id TEXT NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
    site TEXT NOT NULL CHECK (length(trim(site)) > 0),
    title TEXT NOT NULL CHECK (length(trim(title)) > 0),
    description TEXT NOT NULL DEFAULT '',
    billing_mode TEXT NOT NULL DEFAULT 'pay_as_you_go' CHECK (billing_mode IN ('pay_as_you_go')),
    price_text TEXT NOT NULL DEFAULT '',
    display_order INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (product_id, site)
);

CREATE SCHEMA client_sync;
CREATE TABLE client_sync.user_profiles (
    id TEXT PRIMARY KEY,
    owner_identity_issuer TEXT NOT NULL,
    owner_identity_subject TEXT NOT NULL,
    email TEXT NOT NULL,
    display_name TEXT NOT NULL,
    merchant_id TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (owner_identity_issuer, owner_identity_subject)
);
CREATE TABLE client_sync.account_balances (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL UNIQUE,
    owner_identity_issuer TEXT NOT NULL,
    owner_identity_subject TEXT NOT NULL,
    balance_microcredits BIGINT NOT NULL
);
CREATE TABLE client_sync.transactions (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    owner_identity_issuer TEXT NOT NULL,
    owner_identity_subject TEXT NOT NULL,
    transaction_type TEXT NOT NULL,
    amount_microcredits BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE client_sync.charges (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    subscription_id TEXT NOT NULL,
    owner_identity_issuer TEXT NOT NULL,
    owner_identity_subject TEXT NOT NULL,
    external_order_id TEXT NOT NULL,
    gross_microcredits BIGINT NOT NULL,
    order_snapshot JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE client_sync.commissions (
    id TEXT PRIMARY KEY,
    merchant_id TEXT NOT NULL,
    charge_id TEXT NOT NULL,
    owner_identity_issuer TEXT NOT NULL,
    owner_identity_subject TEXT NOT NULL,
    amount_microcredits BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE FUNCTION protect_service_principal() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN RAISE EXCEPTION 'service principal audit history cannot be deleted'; END IF;
    IF OLD.status='revoked' THEN RAISE EXCEPTION 'revoked service principal cannot be changed'; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER service_principals_audit_history BEFORE UPDATE OR DELETE ON service_principals
FOR EACH ROW EXECUTE FUNCTION protect_service_principal();

CREATE FUNCTION protect_subscription_key() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN RAISE EXCEPTION 'Subscription Key cannot be deleted'; END IF;
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.subscription_id IS DISTINCT FROM OLD.subscription_id
       OR NEW.name IS DISTINCT FROM OLD.name
       OR NEW.key IS DISTINCT FROM OLD.key OR NEW.subscription_key_hmac IS DISTINCT FROM OLD.subscription_key_hmac
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'Subscription Key identity and material are immutable';
    END IF;
    IF NEW.status=OLD.status AND NEW.revoked_at IS NOT DISTINCT FROM OLD.revoked_at THEN
        IF OLD.last_used_at IS NOT NULL AND (NEW.last_used_at IS NULL OR NEW.last_used_at < OLD.last_used_at) THEN
            RAISE EXCEPTION 'Subscription Key last_used_at cannot move backwards';
        END IF;
        RETURN NEW;
    END IF;
    IF OLD.status='revoked' THEN RAISE EXCEPTION 'revoked Subscription Key cannot be changed'; END IF;
    IF NOT (OLD.status='active' AND NEW.status='revoked' AND NEW.revoked_at IS NOT NULL) THEN
        RAISE EXCEPTION 'only active to revoked Subscription Key transition is allowed';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER subscription_keys_immutable BEFORE UPDATE OR DELETE ON subscription_keys
FOR EACH ROW EXECUTE FUNCTION protect_subscription_key();

CREATE FUNCTION assert_ledger_transaction_balanced() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE target_id TEXT; target_ids TEXT[]; net BIGINT;
BEGIN
    IF TG_TABLE_NAME='ledger_transactions' THEN
        target_ids := ARRAY[COALESCE(NEW.id,OLD.id)];
    ELSIF TG_OP='INSERT' THEN
        target_ids := ARRAY[NEW.transaction_id];
    ELSIF TG_OP='DELETE' THEN
        target_ids := ARRAY[OLD.transaction_id];
    ELSE
        target_ids := ARRAY[OLD.transaction_id,NEW.transaction_id];
    END IF;
    FOREACH target_id IN ARRAY target_ids LOOP
        IF EXISTS (SELECT 1 FROM ledger_transactions WHERE id=target_id AND status='posted') THEN
            SELECT COALESCE(SUM(CASE direction WHEN 'debit' THEN amount_microcredits ELSE -amount_microcredits END),0)
            INTO net FROM ledger_entries WHERE transaction_id=target_id;
            IF net<>0 THEN RAISE EXCEPTION 'ledger transaction % is not balanced',target_id; END IF;
        END IF;
    END LOOP;
    RETURN COALESCE(NEW,OLD);
END;
$$;
CREATE CONSTRAINT TRIGGER ledger_transaction_balance AFTER INSERT OR UPDATE ON ledger_transactions
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION assert_ledger_transaction_balanced();
CREATE CONSTRAINT TRIGGER ledger_entry_balance AFTER INSERT OR UPDATE OR DELETE ON ledger_entries
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION assert_ledger_transaction_balanced();
