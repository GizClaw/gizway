-- Milestone 02 starts from an empty GizPay database. There is deliberately no
-- compatibility schema, migration bridge, or legacy payment/quota surface.

CREATE TABLE users (
    id TEXT PRIMARY KEY,
    identity_issuer TEXT NOT NULL,
    identity_subject TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (identity_issuer, identity_subject)
);

CREATE TABLE accounts (
    id TEXT PRIMARY KEY,
    owner_user_id TEXT NOT NULL REFERENCES users(id),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (owner_user_id)
);

CREATE TABLE service_principals (
    id TEXT PRIMARY KEY,
    owner_user_id TEXT NOT NULL REFERENCES users(id),
    identity_issuer TEXT NOT NULL,
    identity_subject TEXT NOT NULL,
    credential_key_id TEXT,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','revoked')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ,
    UNIQUE (identity_issuer, identity_subject),
    CHECK ((status='active' AND revoked_at IS NULL) OR
           (status='revoked' AND revoked_at IS NOT NULL))
);

CREATE TABLE ledger_accounts (
    id TEXT PRIMARY KEY,
    owner_account_id TEXT REFERENCES accounts(id),
    asset_code TEXT NOT NULL DEFAULT 'credit',
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
    UNIQUE (owner_account_id, asset_code)
);

CREATE TABLE ledger_transactions (
    id TEXT PRIMARY KEY,
    transaction_type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','posted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE ledger_entries (
    id TEXT PRIMARY KEY,
    transaction_id TEXT NOT NULL REFERENCES ledger_transactions(id),
    ledger_account_id TEXT NOT NULL REFERENCES ledger_accounts(id),
    direction TEXT NOT NULL CHECK (direction IN ('debit','credit')),
    amount_microcredits BIGINT NOT NULL CHECK (amount_microcredits > 0)
);

CREATE VIEW account_balances AS
SELECT a.id AS account_id,
       COALESCE(SUM(CASE WHEN t.status<>'posted' OR t.status IS NULL THEN 0
                         WHEN e.direction='credit' THEN e.amount_microcredits
                         ELSE -e.amount_microcredits END), 0)::BIGINT AS balance_microcredits
FROM accounts a
LEFT JOIN ledger_accounts la ON la.owner_account_id=a.id AND la.asset_code='credit'
LEFT JOIN ledger_entries e ON e.ledger_account_id=la.id
LEFT JOIN ledger_transactions t ON t.id=e.transaction_id AND t.status='posted'
GROUP BY a.id;

CREATE TABLE merchants (
    id TEXT PRIMARY KEY,
    settlement_account_id TEXT NOT NULL REFERENCES accounts(id),
    legal_name TEXT NOT NULL CHECK (length(trim(legal_name)) > 0),
    public_name TEXT NOT NULL CHECK (length(trim(public_name)) > 0),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
    review_level TEXT NOT NULL DEFAULT 'basic',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE products (
    id TEXT PRIMARY KEY,
    merchant_id TEXT NOT NULL REFERENCES merchants(id),
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    billing_mode TEXT NOT NULL DEFAULT 'pay_as_you_go' CHECK (billing_mode IN ('pay_as_you_go')),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
    terms_version TEXT NOT NULL CHECK (length(trim(terms_version)) > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE subscriptions (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(id),
    product_id TEXT NOT NULL REFERENCES products(id),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','paused','inactive')),
    terms_version TEXT NOT NULL CHECK (length(trim(terms_version)) > 0),
    accepted_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    canceled_at TIMESTAMPTZ
);

CREATE TABLE subscription_api_keys (
    id TEXT PRIMARY KEY,
    subscription_id TEXT NOT NULL REFERENCES subscriptions(id) ON DELETE RESTRICT,
    key_hmac TEXT NOT NULL UNIQUE,
    encrypted_key TEXT NOT NULL CHECK (length(encrypted_key) > 0),
    encryption_version INTEGER NOT NULL CHECK (encryption_version > 0),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','revoked')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ,
    CHECK ((status='active' AND revoked_at IS NULL) OR
           (status='revoked' AND revoked_at IS NOT NULL))
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
    ledger_transaction_id TEXT NOT NULL UNIQUE REFERENCES ledger_transactions(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE charge_commissions (
    charge_id TEXT NOT NULL REFERENCES payg_charges(id),
    merchant_id TEXT NOT NULL REFERENCES merchants(id),
    settlement_account_id TEXT NOT NULL REFERENCES accounts(id),
    amount_microcredits BIGINT NOT NULL CHECK (amount_microcredits >= 0),
    PRIMARY KEY (charge_id, merchant_id)
);

CREATE FUNCTION protect_revoked_records() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN
        RAISE EXCEPTION '% records are audit history and cannot be deleted', TG_TABLE_NAME;
    END IF;
    IF OLD.status='revoked' AND (NEW.status<>OLD.status OR NEW.revoked_at IS DISTINCT FROM OLD.revoked_at) THEN
        RAISE EXCEPTION 'revoked % cannot be restored or changed', TG_TABLE_NAME;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER service_principals_audit_history
BEFORE UPDATE OR DELETE ON service_principals
FOR EACH ROW EXECUTE FUNCTION protect_revoked_records();

CREATE TRIGGER subscription_api_keys_audit_history
BEFORE UPDATE OR DELETE ON subscription_api_keys
FOR EACH ROW EXECUTE FUNCTION protect_revoked_records();

CREATE FUNCTION assert_ledger_transaction_balanced() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE target_id TEXT; old_target_id TEXT; net BIGINT;
BEGIN
    IF TG_TABLE_NAME='ledger_transactions' THEN
        target_id := COALESCE(NEW.id,OLD.id);
    ELSE
        target_id := COALESCE(NEW.transaction_id,OLD.transaction_id);
        IF TG_OP='UPDATE' AND OLD.transaction_id IS DISTINCT FROM NEW.transaction_id THEN
            old_target_id := OLD.transaction_id;
        END IF;
    END IF;
    IF EXISTS (SELECT 1 FROM ledger_transactions WHERE id=target_id AND status='posted') THEN
        SELECT COALESCE(SUM(CASE direction WHEN 'debit' THEN amount_microcredits
                                          ELSE -amount_microcredits END),0)
          INTO net FROM ledger_entries WHERE transaction_id=target_id;
        IF net<>0 THEN RAISE EXCEPTION 'ledger transaction % is not balanced', target_id; END IF;
    END IF;
    IF old_target_id IS NOT NULL AND EXISTS (SELECT 1 FROM ledger_transactions WHERE id=old_target_id AND status='posted') THEN
        SELECT COALESCE(SUM(CASE direction WHEN 'debit' THEN amount_microcredits
                                          ELSE -amount_microcredits END),0)
          INTO net FROM ledger_entries WHERE transaction_id=old_target_id;
        IF net<>0 THEN RAISE EXCEPTION 'ledger transaction % is not balanced', old_target_id; END IF;
    END IF;
    RETURN COALESCE(NEW,OLD);
END;
$$;

CREATE CONSTRAINT TRIGGER ledger_transaction_balance
AFTER INSERT OR UPDATE ON ledger_transactions
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW
EXECUTE FUNCTION assert_ledger_transaction_balanced();

CREATE CONSTRAINT TRIGGER ledger_entry_balance
AFTER INSERT OR UPDATE OR DELETE ON ledger_entries
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW
EXECUTE FUNCTION assert_ledger_transaction_balanced();
