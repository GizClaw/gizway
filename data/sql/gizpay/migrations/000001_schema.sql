-- GizPay target schema. This project is pre-launch, so the first production
-- migration creates only the control-plane tables directly; no legacy AI
-- reservation or regional Catalog table is created and then transformed.


-- Dumped from database version 17.10 (Debian 17.10-1.pgdg12+1)
-- Dumped by pg_dump version 17.10 (Debian 17.10-1.pgdg12+1)

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET transaction_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Name: public; Type: SCHEMA; Schema: -; Owner: -
--



--
-- Name: SCHEMA public; Type: COMMENT; Schema: -; Owner: -
--



--
-- Name: utc_timestamp_text; Type: DOMAIN; Schema: public; Owner: -
--

CREATE DOMAIN utc_timestamp_text AS text
	CONSTRAINT utc_timestamp_text_check CHECK ((VALUE ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{9}Z$'::text));


--
-- Name: assert_ledger_transaction_balanced(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION assert_ledger_transaction_balanced() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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


--
-- Name: prevent_posted_ledger_entry_mutation(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION prevent_posted_ledger_entry_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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


--
-- Name: prevent_posted_ledger_transaction_mutation(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION prevent_posted_ledger_transaction_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF OLD.status = 'posted' THEN
        RAISE EXCEPTION 'posted ledger transactions are immutable';
    END IF;
    RETURN OLD;
END;
$$;


SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: ledger_accounts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE ledger_accounts (
    id text NOT NULL,
    owner_account_id text,
    code text NOT NULL,
    kind text NOT NULL,
    asset_code text DEFAULT 'GIZ_CREDIT'::text NOT NULL,
    normal_balance text NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    created_at utc_timestamp_text NOT NULL,
    updated_at utc_timestamp_text NOT NULL,
    CONSTRAINT ledger_accounts_normal_balance_check CHECK ((normal_balance = ANY (ARRAY['debit'::text, 'credit'::text]))),
    CONSTRAINT ledger_accounts_status_check CHECK ((status = ANY (ARRAY['active'::text, 'frozen'::text, 'closed'::text])))
);


--
-- Name: ledger_entries; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE ledger_entries (
    id text NOT NULL,
    transaction_id text NOT NULL,
    ledger_account_id text NOT NULL,
    sequence bigint NOT NULL,
    direction text NOT NULL,
    amount_microcredits bigint NOT NULL,
    created_at utc_timestamp_text NOT NULL,
    CONSTRAINT ledger_entries_amount_microcredits_check CHECK ((amount_microcredits > 0)),
    CONSTRAINT ledger_entries_direction_check CHECK ((direction = ANY (ARRAY['debit'::text, 'credit'::text])))
);


--
-- Name: ledger_transactions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE ledger_transactions (
    id text NOT NULL,
    transaction_type text NOT NULL,
    status text NOT NULL,
    idempotency_key text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    initiated_by_account_id text,
    reference_type text,
    reference_id text,
    created_at utc_timestamp_text NOT NULL,
    posted_at utc_timestamp_text,
    CONSTRAINT ledger_transactions_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'posted'::text, 'reversed'::text, 'failed'::text])))
);


--
-- Name: account_balances; Type: VIEW; Schema: public; Owner: -
--

CREATE VIEW account_balances AS
 SELECT la.owner_account_id AS account_id,
    la.asset_code,
    COALESCE(sum(
        CASE
            WHEN (lt.status <> 'posted'::text) THEN (0)::bigint
            WHEN (le.direction = la.normal_balance) THEN le.amount_microcredits
            ELSE (- le.amount_microcredits)
        END), (0)::numeric) AS balance_microcredits,
    max(
        CASE
            WHEN (lt.status = 'posted'::text) THEN (le.created_at)::text
            ELSE NULL::text
        END) AS updated_at
   FROM ((ledger_accounts la
     LEFT JOIN ledger_entries le ON ((le.ledger_account_id = la.id)))
     LEFT JOIN ledger_transactions lt ON ((lt.id = le.transaction_id)))
  WHERE (la.owner_account_id IS NOT NULL)
  GROUP BY la.owner_account_id, la.asset_code;


--
-- Name: accounts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE accounts (
    id text NOT NULL,
    owner_user_id text NOT NULL,
    kind text NOT NULL,
    name text NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    created_at utc_timestamp_text NOT NULL,
    updated_at utc_timestamp_text NOT NULL,
    CONSTRAINT accounts_kind_check CHECK ((kind = ANY (ARRAY['personal'::text, 'merchant'::text]))),
    CONSTRAINT accounts_status_check CHECK ((status = ANY (ARRAY['active'::text, 'suspended'::text, 'closed'::text])))
);


--
-- Name: admin_api_keys; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE admin_api_keys (
    id text NOT NULL,
    administrator_id text NOT NULL,
    name text NOT NULL,
    key_prefix text NOT NULL,
    secret_hash bytea NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    expires_at utc_timestamp_text,
    last_used_at utc_timestamp_text,
    created_at utc_timestamp_text NOT NULL,
    revoked_at utc_timestamp_text,
    idempotency_key text,
    payload_hash bytea,
    CONSTRAINT admin_api_keys_status_check CHECK ((status = ANY (ARRAY['active'::text, 'revoked'::text, 'expired'::text])))
);


--
-- Name: admin_sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE admin_sessions (
    id text NOT NULL,
    administrator_id text NOT NULL,
    secret_hash bytea NOT NULL,
    status text NOT NULL,
    expires_at utc_timestamp_text NOT NULL,
    created_at utc_timestamp_text NOT NULL,
    revoked_at utc_timestamp_text,
    CONSTRAINT admin_sessions_status_check CHECK ((status = ANY (ARRAY['active'::text, 'revoked'::text, 'expired'::text])))
);


--
-- Name: admin_webhook_retry_commands; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE admin_webhook_retry_commands (
    id text NOT NULL,
    administrator_id text NOT NULL,
    original_delivery_id text NOT NULL,
    result_delivery_id text NOT NULL,
    idempotency_key text NOT NULL,
    payload_hash bytea NOT NULL,
    created_at utc_timestamp_text NOT NULL
);


--
-- Name: administrators; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE administrators (
    id text NOT NULL,
    email text NOT NULL,
    display_name text DEFAULT ''::text NOT NULL,
    password_hash text,
    status text DEFAULT 'active'::text NOT NULL,
    last_login_at utc_timestamp_text,
    created_at utc_timestamp_text NOT NULL,
    updated_at utc_timestamp_text NOT NULL,
    closed_at utc_timestamp_text,
    CONSTRAINT administrators_status_check CHECK ((status = ANY (ARRAY['active'::text, 'suspended'::text, 'closed'::text])))
);


--
-- Name: api_idempotency_commands; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE api_idempotency_commands (
    id text NOT NULL,
    credential_hash bytea NOT NULL,
    operation text NOT NULL,
    idempotency_key text NOT NULL,
    payload_hash bytea NOT NULL,
    status text NOT NULL,
    response_status bigint,
    response_content_type text,
    response_body bytea,
    created_at utc_timestamp_text NOT NULL,
    expires_at utc_timestamp_text NOT NULL,
    completed_at utc_timestamp_text,
    CONSTRAINT api_idempotency_commands_status_check CHECK ((status = ANY (ARRAY['started'::text, 'completed'::text])))
);


--
-- Name: api_keys; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE api_keys (
    id text NOT NULL,
    account_id text NOT NULL,
    kind text NOT NULL,
    name text NOT NULL,
    key_prefix text NOT NULL,
    secret_hash bytea NOT NULL,
    scopes jsonb DEFAULT '[]'::jsonb NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    expires_at utc_timestamp_text,
    last_used_at utc_timestamp_text,
    created_at utc_timestamp_text NOT NULL,
    revoked_at utc_timestamp_text,
    idempotency_key text,
    payload_hash bytea,
    CONSTRAINT api_keys_kind_check CHECK ((kind = ANY (ARRAY['gateway'::text, 'payment'::text]))),
    CONSTRAINT api_keys_status_check CHECK ((status = ANY (ARRAY['active'::text, 'revoked'::text, 'expired'::text])))
);


--
-- Name: audit_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE audit_events (
    sequence bigint NOT NULL,
    id text NOT NULL,
    actor_type text NOT NULL,
    actor_id text NOT NULL,
    action text NOT NULL,
    resource_type text NOT NULL,
    resource_id text NOT NULL,
    reason text,
    request_id text,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at utc_timestamp_text NOT NULL,
    CONSTRAINT audit_events_actor_type_check CHECK ((actor_type = ANY (ARRAY['user'::text, 'administrator'::text, 'api_key'::text, 'system'::text])))
);


--
-- Name: audit_events_sequence_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE audit_events_sequence_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: audit_events_sequence_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE audit_events_sequence_seq OWNED BY audit_events.sequence;


--
-- Name: billing_rate_publications; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE billing_rate_publications (
    id text NOT NULL,
    node_id text NOT NULL,
    region text NOT NULL,
    source_publication_id text NOT NULL,
    revision bigint NOT NULL,
    payload_hash bytea NOT NULL,
    status text NOT NULL,
    effective_at utc_timestamp_text NOT NULL,
    created_at utc_timestamp_text NOT NULL,
    CONSTRAINT billing_rate_publications_region_check CHECK ((region = ANY (ARRAY['cn'::text, 'global'::text]))),
    CONSTRAINT billing_rate_publications_revision_check CHECK ((revision > 0)),
    CONSTRAINT billing_rate_publications_status_check CHECK ((status = ANY (ARRAY['active'::text, 'retired'::text, 'disabled'::text])))
);


--
-- Name: billing_rate_versions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE billing_rate_versions (
    id text NOT NULL,
    publication_id text NOT NULL,
    model_variant_id text NOT NULL,
    public_model text NOT NULL,
    metric text NOT NULL,
    unit_size bigint NOT NULL,
    base_price_microcredits bigint NOT NULL,
    customer_price_microcredits bigint NOT NULL,
    discount_bps integer NOT NULL,
    CONSTRAINT billing_rate_versions_base_price_microcredits_check CHECK ((base_price_microcredits >= 0)),
    CONSTRAINT billing_rate_versions_check CHECK ((customer_price_microcredits <= base_price_microcredits)),
    CONSTRAINT billing_rate_versions_customer_price_microcredits_check CHECK ((customer_price_microcredits >= 0)),
    CONSTRAINT billing_rate_versions_discount_bps_check CHECK (((discount_bps >= 0) AND (discount_bps <= 10000))),
    CONSTRAINT billing_rate_versions_metric_check CHECK ((metric = ANY (ARRAY['input_token'::text, 'output_token'::text, 'cached_input_token'::text, 'input_audio_token'::text, 'output_audio_token'::text, 'audio_second'::text, 'image'::text, 'video_second'::text, 'request'::text]))),
    CONSTRAINT billing_rate_versions_unit_size_check CHECK ((unit_size > 0))
);


--
-- Name: credit_holds; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE credit_holds (
    id text NOT NULL,
    account_id text NOT NULL,
    purpose text NOT NULL,
    reference_type text NOT NULL,
    reference_id text NOT NULL,
    amount_microcredits bigint NOT NULL,
    status text NOT NULL,
    expires_at utc_timestamp_text NOT NULL,
    completed_at utc_timestamp_text,
    created_at utc_timestamp_text NOT NULL,
    CONSTRAINT credit_holds_amount_microcredits_check CHECK ((amount_microcredits > 0)),
    CONSTRAINT credit_holds_purpose_check CHECK ((purpose = ANY (ARRAY['payment'::text, 'refund'::text, 'reversal'::text]))),
    CONSTRAINT credit_holds_status_check CHECK ((status = ANY (ARRAY['active'::text, 'captured'::text, 'released'::text, 'expired'::text])))
);


--
-- Name: credit_lots; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE credit_lots (
    id text NOT NULL,
    account_id text NOT NULL,
    topup_id text NOT NULL,
    original_microcredits bigint NOT NULL,
    remaining_microcredits bigint NOT NULL,
    created_at utc_timestamp_text NOT NULL,
    CONSTRAINT credit_lots_original_microcredits_check CHECK ((original_microcredits > 0)),
    CONSTRAINT credit_lots_remaining_microcredits_check CHECK ((remaining_microcredits >= 0))
);


--
-- Name: credit_transfers; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE credit_transfers (
    id text NOT NULL,
    sender_account_id text NOT NULL,
    recipient_account_id text NOT NULL,
    amount_microcredits bigint NOT NULL,
    status text NOT NULL,
    note text DEFAULT ''::text NOT NULL,
    idempotency_key text NOT NULL,
    payload_hash bytea NOT NULL,
    created_at utc_timestamp_text NOT NULL,
    completed_at utc_timestamp_text,
    CONSTRAINT credit_transfers_amount_microcredits_check CHECK ((amount_microcredits > 0)),
    CONSTRAINT credit_transfers_check CHECK ((sender_account_id <> recipient_account_id)),
    CONSTRAINT credit_transfers_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'succeeded'::text, 'failed'::text, 'cancelled'::text])))
);


--
-- Name: gateway_node_certificates; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE gateway_node_certificates (
    id text NOT NULL,
    node_id text NOT NULL,
    fingerprint_sha256 bytea NOT NULL,
    serial_number text NOT NULL,
    subject_dn text NOT NULL,
    san_uri text NOT NULL,
    status text NOT NULL,
    not_before utc_timestamp_text NOT NULL,
    not_after utc_timestamp_text NOT NULL,
    replaced_by_id text,
    created_at utc_timestamp_text NOT NULL,
    revoked_at utc_timestamp_text,
    CONSTRAINT gateway_node_certificates_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'active'::text, 'revoked'::text, 'expired'::text])))
);


--
-- Name: gateway_nodes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE gateway_nodes (
    id text NOT NULL,
    region text NOT NULL,
    name text NOT NULL,
    created_at utc_timestamp_text NOT NULL,
    updated_at utc_timestamp_text NOT NULL,
    CONSTRAINT gateway_nodes_region_check CHECK ((region = ANY (ARRAY['cn'::text, 'global'::text])))
);


--
-- Name: gateway_usage_metrics; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE gateway_usage_metrics (
    ucgid text NOT NULL,
    rate_version_id text NOT NULL,
    metric text NOT NULL,
    quantity bigint NOT NULL,
    unit_size bigint NOT NULL,
    price_microcredits bigint NOT NULL,
    charged_microcredits bigint NOT NULL,
    CONSTRAINT gateway_usage_metrics_charged_microcredits_check CHECK ((charged_microcredits >= 0)),
    CONSTRAINT gateway_usage_metrics_price_microcredits_check CHECK ((price_microcredits >= 0)),
    CONSTRAINT gateway_usage_metrics_quantity_check CHECK ((quantity >= 0)),
    CONSTRAINT gateway_usage_metrics_unit_size_check CHECK ((unit_size > 0))
);


--
-- Name: gateway_usage_records; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE gateway_usage_records (
    ucgid text NOT NULL,
    account_id text NOT NULL,
    node_id text NOT NULL,
    region text NOT NULL,
    operation_id text NOT NULL,
    public_model text NOT NULL,
    model_variant_id text NOT NULL,
    rate_publication_id text NOT NULL,
    canonical_payload_hash bytea NOT NULL,
    charged_microcredits bigint NOT NULL,
    ledger_transaction_id text NOT NULL,
    started_at utc_timestamp_text NOT NULL,
    completed_at utc_timestamp_text NOT NULL,
    received_at utc_timestamp_text NOT NULL,
    CONSTRAINT gateway_usage_records_charged_microcredits_check CHECK ((charged_microcredits >= 0)),
    CONSTRAINT gateway_usage_records_region_check CHECK ((region = ANY (ARRAY['cn'::text, 'global'::text])))
);


--
-- Name: invoices; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE invoices (
    id text NOT NULL,
    account_id text NOT NULL,
    topup_id text NOT NULL,
    invoice_number text NOT NULL,
    fiat_currency text NOT NULL,
    fiat_amount_minor bigint NOT NULL,
    issued_at utc_timestamp_text NOT NULL
);


--
-- Name: merchant_accounts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE merchant_accounts (
    account_id text NOT NULL,
    owner_user_id text NOT NULL,
    legal_name text NOT NULL,
    public_name text NOT NULL,
    review_level text DEFAULT 'basic'::text NOT NULL,
    merchant_status text DEFAULT 'pending'::text NOT NULL,
    country_code text,
    website_url text,
    created_at utc_timestamp_text NOT NULL,
    updated_at utc_timestamp_text NOT NULL,
    CONSTRAINT merchant_accounts_merchant_status_check CHECK ((merchant_status = ANY (ARRAY['pending'::text, 'approved'::text, 'rejected'::text, 'suspended'::text, 'closed'::text]))),
    CONSTRAINT merchant_accounts_review_level_check CHECK ((review_level = ANY (ARRAY['basic'::text, 'enhanced'::text])))
);


--
-- Name: merchant_payment_reversals; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE merchant_payment_reversals (
    id text NOT NULL,
    payment_intent_id text NOT NULL,
    merchant_account_id text NOT NULL,
    payer_account_id text NOT NULL,
    gross_microcredits bigint NOT NULL,
    platform_fee_microcredits bigint NOT NULL,
    net_microcredits bigint NOT NULL,
    status text NOT NULL,
    reason text NOT NULL,
    idempotency_key text NOT NULL,
    payload_hash bytea NOT NULL,
    ledger_transaction_id text NOT NULL,
    created_at utc_timestamp_text NOT NULL,
    CONSTRAINT merchant_payment_reversals_gross_microcredits_check CHECK ((gross_microcredits > 0)),
    CONSTRAINT merchant_payment_reversals_net_microcredits_check CHECK ((net_microcredits >= 0)),
    CONSTRAINT merchant_payment_reversals_platform_fee_microcredits_check CHECK ((platform_fee_microcredits >= 0)),
    CONSTRAINT merchant_payment_reversals_status_check CHECK ((status = ANY (ARRAY['succeeded'::text, 'failed'::text])))
);


--
-- Name: merchant_services; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE merchant_services (
    id text NOT NULL,
    merchant_account_id text NOT NULL,
    service_code text NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    interface_set jsonb DEFAULT '[]'::jsonb NOT NULL,
    status text NOT NULL,
    max_transaction_microcredits bigint NOT NULL,
    daily_limit_microcredits bigint NOT NULL,
    idempotency_key text NOT NULL,
    payload_hash bytea NOT NULL,
    created_at utc_timestamp_text NOT NULL,
    updated_at utc_timestamp_text NOT NULL,
    CONSTRAINT merchant_services_daily_limit_microcredits_check CHECK ((daily_limit_microcredits > 0)),
    CONSTRAINT merchant_services_max_transaction_microcredits_check CHECK ((max_transaction_microcredits > 0)),
    CONSTRAINT merchant_services_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'approved'::text, 'rejected'::text, 'suspended'::text])))
);


--
-- Name: merchant_transactions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE merchant_transactions (
    id text NOT NULL,
    payment_intent_id text NOT NULL,
    merchant_account_id text NOT NULL,
    gross_microcredits bigint NOT NULL,
    platform_fee_microcredits bigint NOT NULL,
    net_microcredits bigint NOT NULL,
    status text NOT NULL,
    created_at utc_timestamp_text NOT NULL,
    CONSTRAINT merchant_transactions_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'posted'::text, 'reversed'::text, 'failed'::text])))
);


--
-- Name: payment_intents; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE payment_intents (
    id text NOT NULL,
    merchant_account_id text NOT NULL,
    service_id text NOT NULL,
    payer_account_id text,
    external_order_id text NOT NULL,
    amount_microcredits bigint NOT NULL,
    platform_fee_microcredits bigint NOT NULL,
    net_microcredits bigint NOT NULL,
    fee_bps bigint NOT NULL,
    status text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    checkout_url text NOT NULL,
    expires_at utc_timestamp_text NOT NULL,
    created_at utc_timestamp_text NOT NULL,
    completed_at utc_timestamp_text,
    create_idempotency_key text NOT NULL,
    create_payload_hash bytea NOT NULL,
    confirm_idempotency_key text,
    CONSTRAINT payment_intents_amount_microcredits_check CHECK ((amount_microcredits > 0)),
    CONSTRAINT payment_intents_fee_bps_check CHECK (((fee_bps >= 0) AND (fee_bps <= 10000))),
    CONSTRAINT payment_intents_net_microcredits_check CHECK ((net_microcredits >= 0)),
    CONSTRAINT payment_intents_platform_fee_microcredits_check CHECK ((platform_fee_microcredits >= 0)),
    CONSTRAINT payment_intents_status_check CHECK ((status = ANY (ARRAY['created'::text, 'authorized'::text, 'succeeded'::text, 'expired'::text, 'cancelled'::text, 'failed'::text])))
);


--
-- Name: payment_provider_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE payment_provider_events (
    event_id text NOT NULL,
    event_type text NOT NULL,
    provider_reference text NOT NULL,
    payload_hash bytea NOT NULL,
    status text NOT NULL,
    error_code text,
    received_at utc_timestamp_text NOT NULL,
    CONSTRAINT payment_provider_events_status_check CHECK ((status = ANY (ARRAY['processed'::text, 'quarantined'::text])))
);


--
-- Name: powersync_account_balances; Type: VIEW; Schema: public; Owner: -
--

CREATE VIEW powersync_account_balances AS
 SELECT account_id,
    asset_code,
    balance_microcredits,
    updated_at
   FROM account_balances;


--
-- Name: powersync_credit_transfers; Type: VIEW; Schema: public; Owner: -
--

CREATE VIEW powersync_credit_transfers AS
 SELECT credit_transfers.sender_account_id AS account_id,
    credit_transfers.id,
    'outgoing'::text AS direction,
    credit_transfers.sender_account_id,
    credit_transfers.recipient_account_id,
    credit_transfers.amount_microcredits,
    credit_transfers.status,
    credit_transfers.note,
    credit_transfers.created_at,
    credit_transfers.completed_at
   FROM credit_transfers
UNION ALL
 SELECT credit_transfers.recipient_account_id AS account_id,
    credit_transfers.id,
    'incoming'::text AS direction,
    credit_transfers.sender_account_id,
    credit_transfers.recipient_account_id,
    credit_transfers.amount_microcredits,
    credit_transfers.status,
    credit_transfers.note,
    credit_transfers.created_at,
    credit_transfers.completed_at
   FROM credit_transfers;


--
-- Name: powersync_gateway_usage; Type: VIEW; Schema: public; Owner: -
--

CREATE VIEW powersync_gateway_usage AS
SELECT
    NULL::text AS account_id,
    NULL::text AS id,
    NULL::text AS ucgid,
    NULL::text AS node_id,
    NULL::text AS region,
    NULL::text AS public_model,
    NULL::text AS model_variant_id,
    NULL::text AS rate_publication_id,
    NULL::bigint AS input_tokens,
    NULL::bigint AS output_tokens,
    NULL::bigint AS cached_input_tokens,
    NULL::bigint AS input_audio_tokens,
    NULL::bigint AS output_audio_tokens,
    NULL::bigint AS charged_microcredits,
    NULL::utc_timestamp_text AS started_at,
    NULL::utc_timestamp_text AS completed_at,
    NULL::utc_timestamp_text AS received_at;


--
-- Name: powersync_merchant_orders; Type: VIEW; Schema: public; Owner: -
--

CREATE VIEW powersync_merchant_orders AS
 SELECT payment_intents.merchant_account_id AS account_id,
    payment_intents.id,
    payment_intents.merchant_account_id,
    payment_intents.service_id,
    payment_intents.payer_account_id,
    payment_intents.external_order_id,
    payment_intents.amount_microcredits,
    payment_intents.platform_fee_microcredits,
    payment_intents.net_microcredits,
    payment_intents.status,
    payment_intents.description,
    payment_intents.expires_at,
    payment_intents.created_at,
    payment_intents.completed_at
   FROM payment_intents
UNION ALL
 SELECT payment_intents.payer_account_id AS account_id,
    payment_intents.id,
    payment_intents.merchant_account_id,
    payment_intents.service_id,
    payment_intents.payer_account_id,
    payment_intents.external_order_id,
    payment_intents.amount_microcredits,
    payment_intents.platform_fee_microcredits,
    payment_intents.net_microcredits,
    payment_intents.status,
    payment_intents.description,
    payment_intents.expires_at,
    payment_intents.created_at,
    payment_intents.completed_at
   FROM payment_intents
  WHERE (payment_intents.payer_account_id IS NOT NULL);


--
-- Name: refunds; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE refunds (
    id text NOT NULL,
    topup_id text NOT NULL,
    account_id text NOT NULL,
    provider_refund_id text,
    credit_microcredits bigint NOT NULL,
    fiat_amount_minor bigint NOT NULL,
    status text NOT NULL,
    idempotency_key text NOT NULL,
    payload_hash bytea NOT NULL,
    created_at utc_timestamp_text NOT NULL,
    completed_at utc_timestamp_text,
    CONSTRAINT refunds_credit_microcredits_check CHECK ((credit_microcredits > 0)),
    CONSTRAINT refunds_fiat_amount_minor_check CHECK ((fiat_amount_minor > 0)),
    CONSTRAINT refunds_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'succeeded'::text, 'failed'::text])))
);


--
-- Name: topups; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE topups (
    id text NOT NULL,
    account_id text NOT NULL,
    payment_provider text NOT NULL,
    provider_reference text NOT NULL,
    fiat_currency text NOT NULL,
    fiat_amount_minor bigint NOT NULL,
    base_fiat_minor bigint NOT NULL,
    base_credit_microcredits bigint NOT NULL,
    effective_fiat_minor bigint NOT NULL,
    effective_credit_microcredits bigint NOT NULL,
    discount_bps bigint NOT NULL,
    credit_microcredits bigint NOT NULL,
    refundable_microcredits bigint DEFAULT 0 NOT NULL,
    status text NOT NULL,
    checkout_url text,
    idempotency_key text NOT NULL,
    payload_hash bytea NOT NULL,
    created_at utc_timestamp_text NOT NULL,
    completed_at utc_timestamp_text,
    CONSTRAINT topups_base_credit_microcredits_check CHECK ((base_credit_microcredits > 0)),
    CONSTRAINT topups_base_fiat_minor_check CHECK ((base_fiat_minor > 0)),
    CONSTRAINT topups_credit_microcredits_check CHECK ((credit_microcredits > 0)),
    CONSTRAINT topups_discount_bps_check CHECK (((discount_bps >= 0) AND (discount_bps <= 10000))),
    CONSTRAINT topups_effective_credit_microcredits_check CHECK ((effective_credit_microcredits > 0)),
    CONSTRAINT topups_effective_fiat_minor_check CHECK ((effective_fiat_minor > 0)),
    CONSTRAINT topups_fiat_amount_minor_check CHECK ((fiat_amount_minor > 0)),
    CONSTRAINT topups_fiat_currency_check CHECK ((length(fiat_currency) = 3)),
    CONSTRAINT topups_refundable_microcredits_check CHECK ((refundable_microcredits >= 0)),
    CONSTRAINT topups_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'succeeded'::text, 'partially_refunded'::text, 'refunded'::text, 'failed'::text])))
);


--
-- Name: powersync_payments; Type: VIEW; Schema: public; Owner: -
--

CREATE VIEW powersync_payments AS
 SELECT topups.account_id,
    topups.id,
    'topup'::text AS payment_type,
    topups.credit_microcredits AS amount_microcredits,
    topups.status,
    topups.created_at,
    topups.completed_at
   FROM topups
UNION ALL
 SELECT refunds.account_id,
    refunds.id,
    'refund'::text AS payment_type,
    refunds.credit_microcredits AS amount_microcredits,
    refunds.status,
    refunds.created_at,
    refunds.completed_at
   FROM refunds
UNION ALL
 SELECT payment_intents.merchant_account_id AS account_id,
    payment_intents.id,
    'merchant_payment'::text AS payment_type,
    payment_intents.amount_microcredits,
    payment_intents.status,
    payment_intents.created_at,
    payment_intents.completed_at
   FROM payment_intents;


--
-- Name: request_rate_limits; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE request_rate_limits (
    scope_key text NOT NULL,
    action text NOT NULL,
    window_started_at utc_timestamp_text NOT NULL,
    request_count bigint NOT NULL,
    updated_at utc_timestamp_text NOT NULL,
    CONSTRAINT request_rate_limits_request_count_check CHECK ((request_count > 0))
);


--
-- Name: risk_decisions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE risk_decisions (
    id text NOT NULL,
    merchant_account_id text NOT NULL,
    service_id text NOT NULL,
    provider_reference text NOT NULL,
    decision text NOT NULL,
    kyc_status text NOT NULL,
    kyb_status text NOT NULL,
    sanctions_status text NOT NULL,
    anomaly_score bigint NOT NULL,
    reason text NOT NULL,
    created_at utc_timestamp_text NOT NULL,
    CONSTRAINT risk_decisions_anomaly_score_check CHECK (((anomaly_score >= 0) AND (anomaly_score <= 100))),
    CONSTRAINT risk_decisions_decision_check CHECK ((decision = ANY (ARRAY['allow'::text, 'deny'::text, 'review'::text]))),
    CONSTRAINT risk_decisions_kyb_status_check CHECK ((kyb_status = ANY (ARRAY['verified'::text, 'failed'::text, 'pending'::text]))),
    CONSTRAINT risk_decisions_kyc_status_check CHECK ((kyc_status = ANY (ARRAY['verified'::text, 'failed'::text, 'pending'::text]))),
    CONSTRAINT risk_decisions_sanctions_status_check CHECK ((sanctions_status = ANY (ARRAY['clear'::text, 'match'::text, 'pending'::text])))
);


--
-- Name: user_sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE user_sessions (
    id text NOT NULL,
    user_id text NOT NULL,
    secret_hash bytea NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    expires_at utc_timestamp_text NOT NULL,
    created_at utc_timestamp_text NOT NULL,
    revoked_at utc_timestamp_text,
    CONSTRAINT user_sessions_status_check CHECK ((status = ANY (ARRAY['active'::text, 'revoked'::text, 'expired'::text])))
);


--
-- Name: users; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE users (
    id text NOT NULL,
    email text NOT NULL,
    display_name text DEFAULT ''::text NOT NULL,
    password_hash text DEFAULT ''::text NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    created_at utc_timestamp_text NOT NULL,
    updated_at utc_timestamp_text NOT NULL,
    CONSTRAINT users_status_check CHECK ((status = ANY (ARRAY['active'::text, 'suspended'::text, 'closed'::text])))
);


--
-- Name: webhook_deliveries; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE webhook_deliveries (
    id text NOT NULL,
    event_id text NOT NULL,
    endpoint_id text NOT NULL,
    signing_secret_snapshot text,
    attempt bigint NOT NULL,
    status text NOT NULL,
    response_status bigint,
    error text,
    claimed_at utc_timestamp_text,
    lease_until utc_timestamp_text,
    next_attempt_at utc_timestamp_text,
    created_at utc_timestamp_text NOT NULL,
    completed_at utc_timestamp_text,
    CONSTRAINT webhook_deliveries_attempt_check CHECK ((attempt > 0)),
    CONSTRAINT webhook_deliveries_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'delivering'::text, 'succeeded'::text, 'failed'::text, 'exhausted'::text])))
);


--
-- Name: webhook_endpoint_commands; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE webhook_endpoint_commands (
    id text NOT NULL,
    merchant_account_id text NOT NULL,
    endpoint_id text NOT NULL,
    operation text NOT NULL,
    idempotency_key text NOT NULL,
    payload_hash bytea NOT NULL,
    response_json text,
    secret_result text,
    created_at utc_timestamp_text NOT NULL,
    CONSTRAINT webhook_endpoint_commands_operation_check CHECK ((operation = ANY (ARRAY['status'::text, 'rotate_secret'::text, 'delete'::text]))),
    CONSTRAINT webhook_endpoint_commands_response_json_check CHECK (((response_json IS NULL) OR (jsonb_typeof((response_json)::jsonb) IS NOT NULL)))
);


--
-- Name: webhook_endpoints; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE webhook_endpoints (
    id text NOT NULL,
    merchant_account_id text NOT NULL,
    url text NOT NULL,
    events jsonb NOT NULL,
    signing_secret text NOT NULL,
    status text NOT NULL,
    idempotency_key text,
    payload_hash bytea,
    created_at utc_timestamp_text NOT NULL,
    updated_at utc_timestamp_text NOT NULL,
    deleted_at utc_timestamp_text,
    CONSTRAINT webhook_endpoints_status_check CHECK ((status = ANY (ARRAY['active'::text, 'disabled'::text])))
);


--
-- Name: webhook_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE webhook_events (
    id text NOT NULL,
    merchant_account_id text NOT NULL,
    event_type text NOT NULL,
    resource_id text NOT NULL,
    payload text NOT NULL,
    created_at utc_timestamp_text NOT NULL,
    CONSTRAINT webhook_events_payload_check CHECK ((jsonb_typeof((payload)::jsonb) IS NOT NULL))
);


--
-- Name: audit_events sequence; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY audit_events ALTER COLUMN sequence SET DEFAULT nextval('audit_events_sequence_seq'::regclass);


--
-- Name: accounts accounts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY accounts
    ADD CONSTRAINT accounts_pkey PRIMARY KEY (id);


--
-- Name: admin_api_keys admin_api_keys_administrator_id_idempotency_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY admin_api_keys
    ADD CONSTRAINT admin_api_keys_administrator_id_idempotency_key_key UNIQUE (administrator_id, idempotency_key);


--
-- Name: admin_api_keys admin_api_keys_key_prefix_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY admin_api_keys
    ADD CONSTRAINT admin_api_keys_key_prefix_key UNIQUE (key_prefix);


--
-- Name: admin_api_keys admin_api_keys_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY admin_api_keys
    ADD CONSTRAINT admin_api_keys_pkey PRIMARY KEY (id);


--
-- Name: admin_sessions admin_sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY admin_sessions
    ADD CONSTRAINT admin_sessions_pkey PRIMARY KEY (id);


--
-- Name: admin_sessions admin_sessions_secret_hash_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY admin_sessions
    ADD CONSTRAINT admin_sessions_secret_hash_key UNIQUE (secret_hash);


--
-- Name: admin_webhook_retry_commands admin_webhook_retry_commands_administrator_id_idempotency_k_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY admin_webhook_retry_commands
    ADD CONSTRAINT admin_webhook_retry_commands_administrator_id_idempotency_k_key UNIQUE (administrator_id, idempotency_key);


--
-- Name: admin_webhook_retry_commands admin_webhook_retry_commands_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY admin_webhook_retry_commands
    ADD CONSTRAINT admin_webhook_retry_commands_pkey PRIMARY KEY (id);


--
-- Name: administrators administrators_email_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY administrators
    ADD CONSTRAINT administrators_email_key UNIQUE (email);


--
-- Name: administrators administrators_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY administrators
    ADD CONSTRAINT administrators_pkey PRIMARY KEY (id);


--
-- Name: api_idempotency_commands api_idempotency_commands_credential_hash_operation_idempote_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY api_idempotency_commands
    ADD CONSTRAINT api_idempotency_commands_credential_hash_operation_idempote_key UNIQUE (credential_hash, operation, idempotency_key);


--
-- Name: api_idempotency_commands api_idempotency_commands_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY api_idempotency_commands
    ADD CONSTRAINT api_idempotency_commands_pkey PRIMARY KEY (id);


--
-- Name: api_keys api_keys_account_id_idempotency_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY api_keys
    ADD CONSTRAINT api_keys_account_id_idempotency_key_key UNIQUE (account_id, idempotency_key);


--
-- Name: api_keys api_keys_key_prefix_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY api_keys
    ADD CONSTRAINT api_keys_key_prefix_key UNIQUE (key_prefix);


--
-- Name: api_keys api_keys_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY api_keys
    ADD CONSTRAINT api_keys_pkey PRIMARY KEY (id);


--
-- Name: audit_events audit_events_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY audit_events
    ADD CONSTRAINT audit_events_id_key UNIQUE (id);


--
-- Name: audit_events audit_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY audit_events
    ADD CONSTRAINT audit_events_pkey PRIMARY KEY (sequence);


--
-- Name: billing_rate_publications billing_rate_publications_node_id_revision_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY billing_rate_publications
    ADD CONSTRAINT billing_rate_publications_node_id_revision_key UNIQUE (node_id, revision);


--
-- Name: billing_rate_publications billing_rate_publications_node_id_source_publication_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY billing_rate_publications
    ADD CONSTRAINT billing_rate_publications_node_id_source_publication_id_key UNIQUE (node_id, source_publication_id);


--
-- Name: billing_rate_publications billing_rate_publications_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY billing_rate_publications
    ADD CONSTRAINT billing_rate_publications_pkey PRIMARY KEY (id);


--
-- Name: billing_rate_versions billing_rate_versions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY billing_rate_versions
    ADD CONSTRAINT billing_rate_versions_pkey PRIMARY KEY (id);


--
-- Name: billing_rate_versions billing_rate_versions_publication_id_model_variant_id_metri_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY billing_rate_versions
    ADD CONSTRAINT billing_rate_versions_publication_id_model_variant_id_metri_key UNIQUE (publication_id, model_variant_id, metric);


--
-- Name: credit_holds credit_holds_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY credit_holds
    ADD CONSTRAINT credit_holds_pkey PRIMARY KEY (id);


--
-- Name: credit_holds credit_holds_purpose_reference_type_reference_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY credit_holds
    ADD CONSTRAINT credit_holds_purpose_reference_type_reference_id_key UNIQUE (purpose, reference_type, reference_id);


--
-- Name: credit_lots credit_lots_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY credit_lots
    ADD CONSTRAINT credit_lots_pkey PRIMARY KEY (id);


--
-- Name: credit_lots credit_lots_topup_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY credit_lots
    ADD CONSTRAINT credit_lots_topup_id_key UNIQUE (topup_id);


--
-- Name: credit_transfers credit_transfers_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY credit_transfers
    ADD CONSTRAINT credit_transfers_pkey PRIMARY KEY (id);


--
-- Name: credit_transfers credit_transfers_sender_account_id_idempotency_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY credit_transfers
    ADD CONSTRAINT credit_transfers_sender_account_id_idempotency_key_key UNIQUE (sender_account_id, idempotency_key);


--
-- Name: gateway_node_certificates gateway_node_certificates_fingerprint_sha256_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY gateway_node_certificates
    ADD CONSTRAINT gateway_node_certificates_fingerprint_sha256_key UNIQUE (fingerprint_sha256);


--
-- Name: gateway_node_certificates gateway_node_certificates_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY gateway_node_certificates
    ADD CONSTRAINT gateway_node_certificates_pkey PRIMARY KEY (id);


--
-- Name: gateway_nodes gateway_nodes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY gateway_nodes
    ADD CONSTRAINT gateway_nodes_pkey PRIMARY KEY (id);


--
-- Name: gateway_usage_metrics gateway_usage_metrics_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY gateway_usage_metrics
    ADD CONSTRAINT gateway_usage_metrics_pkey PRIMARY KEY (ucgid, metric, rate_version_id);


--
-- Name: gateway_usage_records gateway_usage_records_ledger_transaction_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY gateway_usage_records
    ADD CONSTRAINT gateway_usage_records_ledger_transaction_id_key UNIQUE (ledger_transaction_id);


--
-- Name: gateway_usage_records gateway_usage_records_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY gateway_usage_records
    ADD CONSTRAINT gateway_usage_records_pkey PRIMARY KEY (ucgid);


--
-- Name: invoices invoices_invoice_number_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY invoices
    ADD CONSTRAINT invoices_invoice_number_key UNIQUE (invoice_number);


--
-- Name: invoices invoices_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY invoices
    ADD CONSTRAINT invoices_pkey PRIMARY KEY (id);


--
-- Name: invoices invoices_topup_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY invoices
    ADD CONSTRAINT invoices_topup_id_key UNIQUE (topup_id);


--
-- Name: ledger_accounts ledger_accounts_code_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY ledger_accounts
    ADD CONSTRAINT ledger_accounts_code_key UNIQUE (code);


--
-- Name: ledger_accounts ledger_accounts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY ledger_accounts
    ADD CONSTRAINT ledger_accounts_pkey PRIMARY KEY (id);


--
-- Name: ledger_entries ledger_entries_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY ledger_entries
    ADD CONSTRAINT ledger_entries_pkey PRIMARY KEY (id);


--
-- Name: ledger_entries ledger_entries_transaction_id_sequence_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY ledger_entries
    ADD CONSTRAINT ledger_entries_transaction_id_sequence_key UNIQUE (transaction_id, sequence);


--
-- Name: ledger_transactions ledger_transactions_idempotency_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY ledger_transactions
    ADD CONSTRAINT ledger_transactions_idempotency_key_key UNIQUE (idempotency_key);


--
-- Name: ledger_transactions ledger_transactions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY ledger_transactions
    ADD CONSTRAINT ledger_transactions_pkey PRIMARY KEY (id);


--
-- Name: merchant_accounts merchant_accounts_owner_user_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY merchant_accounts
    ADD CONSTRAINT merchant_accounts_owner_user_id_key UNIQUE (owner_user_id);


--
-- Name: merchant_accounts merchant_accounts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY merchant_accounts
    ADD CONSTRAINT merchant_accounts_pkey PRIMARY KEY (account_id);


--
-- Name: merchant_payment_reversals merchant_payment_reversals_ledger_transaction_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY merchant_payment_reversals
    ADD CONSTRAINT merchant_payment_reversals_ledger_transaction_id_key UNIQUE (ledger_transaction_id);


--
-- Name: merchant_payment_reversals merchant_payment_reversals_merchant_account_id_idempotency__key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY merchant_payment_reversals
    ADD CONSTRAINT merchant_payment_reversals_merchant_account_id_idempotency__key UNIQUE (merchant_account_id, idempotency_key);


--
-- Name: merchant_payment_reversals merchant_payment_reversals_payment_intent_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY merchant_payment_reversals
    ADD CONSTRAINT merchant_payment_reversals_payment_intent_id_key UNIQUE (payment_intent_id);


--
-- Name: merchant_payment_reversals merchant_payment_reversals_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY merchant_payment_reversals
    ADD CONSTRAINT merchant_payment_reversals_pkey PRIMARY KEY (id);


--
-- Name: merchant_services merchant_services_merchant_account_id_idempotency_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY merchant_services
    ADD CONSTRAINT merchant_services_merchant_account_id_idempotency_key_key UNIQUE (merchant_account_id, idempotency_key);


--
-- Name: merchant_services merchant_services_merchant_account_id_service_code_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY merchant_services
    ADD CONSTRAINT merchant_services_merchant_account_id_service_code_key UNIQUE (merchant_account_id, service_code);


--
-- Name: merchant_services merchant_services_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY merchant_services
    ADD CONSTRAINT merchant_services_pkey PRIMARY KEY (id);


--
-- Name: merchant_transactions merchant_transactions_payment_intent_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY merchant_transactions
    ADD CONSTRAINT merchant_transactions_payment_intent_id_key UNIQUE (payment_intent_id);


--
-- Name: merchant_transactions merchant_transactions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY merchant_transactions
    ADD CONSTRAINT merchant_transactions_pkey PRIMARY KEY (id);


--
-- Name: payment_intents payment_intents_merchant_account_id_create_idempotency_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY payment_intents
    ADD CONSTRAINT payment_intents_merchant_account_id_create_idempotency_key_key UNIQUE (merchant_account_id, create_idempotency_key);


--
-- Name: payment_intents payment_intents_merchant_account_id_external_order_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY payment_intents
    ADD CONSTRAINT payment_intents_merchant_account_id_external_order_id_key UNIQUE (merchant_account_id, external_order_id);


--
-- Name: payment_intents payment_intents_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY payment_intents
    ADD CONSTRAINT payment_intents_pkey PRIMARY KEY (id);


--
-- Name: payment_provider_events payment_provider_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY payment_provider_events
    ADD CONSTRAINT payment_provider_events_pkey PRIMARY KEY (event_id);


--
-- Name: refunds refunds_account_id_idempotency_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY refunds
    ADD CONSTRAINT refunds_account_id_idempotency_key_key UNIQUE (account_id, idempotency_key);


--
-- Name: refunds refunds_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY refunds
    ADD CONSTRAINT refunds_pkey PRIMARY KEY (id);


--
-- Name: request_rate_limits request_rate_limits_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY request_rate_limits
    ADD CONSTRAINT request_rate_limits_pkey PRIMARY KEY (scope_key, action, window_started_at);


--
-- Name: risk_decisions risk_decisions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY risk_decisions
    ADD CONSTRAINT risk_decisions_pkey PRIMARY KEY (id);


--
-- Name: risk_decisions risk_decisions_provider_reference_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY risk_decisions
    ADD CONSTRAINT risk_decisions_provider_reference_key UNIQUE (provider_reference);


--
-- Name: topups topups_account_id_idempotency_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY topups
    ADD CONSTRAINT topups_account_id_idempotency_key_key UNIQUE (account_id, idempotency_key);


--
-- Name: topups topups_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY topups
    ADD CONSTRAINT topups_pkey PRIMARY KEY (id);


--
-- Name: topups topups_provider_reference_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY topups
    ADD CONSTRAINT topups_provider_reference_key UNIQUE (provider_reference);


--
-- Name: user_sessions user_sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY user_sessions
    ADD CONSTRAINT user_sessions_pkey PRIMARY KEY (id);


--
-- Name: user_sessions user_sessions_secret_hash_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY user_sessions
    ADD CONSTRAINT user_sessions_secret_hash_key UNIQUE (secret_hash);


--
-- Name: users users_email_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY users
    ADD CONSTRAINT users_email_key UNIQUE (email);


--
-- Name: users users_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);


--
-- Name: webhook_deliveries webhook_deliveries_event_id_endpoint_id_attempt_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY webhook_deliveries
    ADD CONSTRAINT webhook_deliveries_event_id_endpoint_id_attempt_key UNIQUE (event_id, endpoint_id, attempt);


--
-- Name: webhook_deliveries webhook_deliveries_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY webhook_deliveries
    ADD CONSTRAINT webhook_deliveries_pkey PRIMARY KEY (id);


--
-- Name: webhook_endpoint_commands webhook_endpoint_commands_merchant_account_id_operation_ide_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY webhook_endpoint_commands
    ADD CONSTRAINT webhook_endpoint_commands_merchant_account_id_operation_ide_key UNIQUE (merchant_account_id, operation, idempotency_key);


--
-- Name: webhook_endpoint_commands webhook_endpoint_commands_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY webhook_endpoint_commands
    ADD CONSTRAINT webhook_endpoint_commands_pkey PRIMARY KEY (id);


--
-- Name: webhook_endpoints webhook_endpoints_merchant_account_id_idempotency_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY webhook_endpoints
    ADD CONSTRAINT webhook_endpoints_merchant_account_id_idempotency_key_key UNIQUE (merchant_account_id, idempotency_key);


--
-- Name: webhook_endpoints webhook_endpoints_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY webhook_endpoints
    ADD CONSTRAINT webhook_endpoints_pkey PRIMARY KEY (id);


--
-- Name: webhook_events webhook_events_event_type_resource_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY webhook_events
    ADD CONSTRAINT webhook_events_event_type_resource_id_key UNIQUE (event_type, resource_id);


--
-- Name: webhook_events webhook_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY webhook_events
    ADD CONSTRAINT webhook_events_pkey PRIMARY KEY (id);


--
-- Name: accounts_one_personal_per_user; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX accounts_one_personal_per_user ON accounts USING btree (owner_user_id) WHERE (kind = 'personal'::text);


--
-- Name: administrators_email_casefold_unique; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX administrators_email_casefold_unique ON administrators USING btree (lower(email));

-- Administrator bearer credentials are identities, not reusable labels. The
-- digest lookup must return at most one row even under concurrent creation.
CREATE UNIQUE INDEX admin_api_keys_secret_hash_uidx ON admin_api_keys USING btree (secret_hash);

-- Quota Exchange authenticates by the SHA-256 digest on every refresh. The
-- digest is a credential identity, so duplicates must be impossible as well as
-- index-backed.
CREATE UNIQUE INDEX api_keys_secret_hash_uidx ON api_keys USING btree (secret_hash);

-- Store code assumes one balance-bearing ledger per customer account and
-- asset. System ledgers have no owner and remain governed by their code.
CREATE UNIQUE INDEX ledger_accounts_owner_asset_uidx ON ledger_accounts USING btree (owner_account_id, asset_code) WHERE (owner_account_id IS NOT NULL);

CREATE INDEX ledger_entries_ledger_account_idx ON ledger_entries USING btree (ledger_account_id);

CREATE INDEX gateway_usage_records_account_received_idx ON gateway_usage_records USING btree (account_id, received_at DESC, ucgid);

CREATE INDEX gateway_usage_records_node_received_idx ON gateway_usage_records USING btree (node_id, received_at DESC);


--
-- Name: audit_events_resource_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX audit_events_resource_idx ON audit_events USING btree (resource_type, resource_id, created_at);


--
-- Name: ledger_single_reversal; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ledger_single_reversal ON ledger_transactions USING btree (reference_id) WHERE ((transaction_type = 'reversal'::text) AND (reference_type = 'ledger_transaction'::text));


--
-- Name: users_email_casefold_unique; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX users_email_casefold_unique ON users USING btree (lower(email));


--
-- Name: webhook_single_active_attempt; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX webhook_single_active_attempt ON webhook_deliveries USING btree (event_id, endpoint_id) WHERE (status = ANY (ARRAY['pending'::text, 'delivering'::text]));


--
-- Name: powersync_gateway_usage _RETURN; Type: RULE; Schema: public; Owner: -
--

CREATE OR REPLACE VIEW powersync_gateway_usage AS
 SELECT u.account_id,
    u.ucgid AS id,
    u.ucgid,
    u.node_id,
    u.region,
    u.public_model,
    u.model_variant_id,
    u.rate_publication_id,
    (COALESCE(sum(m.quantity) FILTER (WHERE (m.metric = 'input_token'::text)), (0)::numeric))::bigint AS input_tokens,
    (COALESCE(sum(m.quantity) FILTER (WHERE (m.metric = 'output_token'::text)), (0)::numeric))::bigint AS output_tokens,
    (COALESCE(sum(m.quantity) FILTER (WHERE (m.metric = 'cached_input_token'::text)), (0)::numeric))::bigint AS cached_input_tokens,
    (COALESCE(sum(m.quantity) FILTER (WHERE (m.metric = 'input_audio_token'::text)), (0)::numeric))::bigint AS input_audio_tokens,
    (COALESCE(sum(m.quantity) FILTER (WHERE (m.metric = 'output_audio_token'::text)), (0)::numeric))::bigint AS output_audio_tokens,
    u.charged_microcredits,
    u.started_at,
    u.completed_at,
    u.received_at
   FROM (gateway_usage_records u
     LEFT JOIN gateway_usage_metrics m ON ((m.ucgid = u.ucgid)))
  GROUP BY u.ucgid;


--
-- Name: ledger_entries ledger_entries_balanced; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER ledger_entries_balanced AFTER INSERT OR DELETE OR UPDATE ON ledger_entries DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION assert_ledger_transaction_balanced();


--
-- Name: ledger_entries ledger_entries_no_update_posted; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER ledger_entries_no_update_posted BEFORE DELETE OR UPDATE ON ledger_entries FOR EACH ROW EXECUTE FUNCTION prevent_posted_ledger_entry_mutation();


--
-- Name: ledger_transactions ledger_transactions_balanced; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER ledger_transactions_balanced AFTER INSERT OR UPDATE ON ledger_transactions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION assert_ledger_transaction_balanced();


--
-- Name: ledger_transactions ledger_transactions_no_update_posted; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER ledger_transactions_no_update_posted BEFORE DELETE OR UPDATE ON ledger_transactions FOR EACH ROW EXECUTE FUNCTION prevent_posted_ledger_transaction_mutation();


--
-- Name: accounts accounts_owner_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY accounts
    ADD CONSTRAINT accounts_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES users(id);


--
-- Name: admin_api_keys admin_api_keys_administrator_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY admin_api_keys
    ADD CONSTRAINT admin_api_keys_administrator_id_fkey FOREIGN KEY (administrator_id) REFERENCES administrators(id);


--
-- Name: admin_sessions admin_sessions_administrator_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY admin_sessions
    ADD CONSTRAINT admin_sessions_administrator_id_fkey FOREIGN KEY (administrator_id) REFERENCES administrators(id);


--
-- Name: admin_webhook_retry_commands admin_webhook_retry_commands_administrator_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY admin_webhook_retry_commands
    ADD CONSTRAINT admin_webhook_retry_commands_administrator_id_fkey FOREIGN KEY (administrator_id) REFERENCES administrators(id);


--
-- Name: admin_webhook_retry_commands admin_webhook_retry_commands_original_delivery_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY admin_webhook_retry_commands
    ADD CONSTRAINT admin_webhook_retry_commands_original_delivery_id_fkey FOREIGN KEY (original_delivery_id) REFERENCES webhook_deliveries(id);


--
-- Name: admin_webhook_retry_commands admin_webhook_retry_commands_result_delivery_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY admin_webhook_retry_commands
    ADD CONSTRAINT admin_webhook_retry_commands_result_delivery_id_fkey FOREIGN KEY (result_delivery_id) REFERENCES webhook_deliveries(id);


--
-- Name: api_keys api_keys_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY api_keys
    ADD CONSTRAINT api_keys_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id);


--
-- Name: billing_rate_publications billing_rate_publications_node_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY billing_rate_publications
    ADD CONSTRAINT billing_rate_publications_node_fk FOREIGN KEY (node_id) REFERENCES gateway_nodes(id);


--
-- Name: billing_rate_versions billing_rate_versions_publication_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY billing_rate_versions
    ADD CONSTRAINT billing_rate_versions_publication_id_fkey FOREIGN KEY (publication_id) REFERENCES billing_rate_publications(id);


--
-- Name: credit_holds credit_holds_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY credit_holds
    ADD CONSTRAINT credit_holds_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id);


--
-- Name: credit_lots credit_lots_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY credit_lots
    ADD CONSTRAINT credit_lots_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id);


--
-- Name: credit_lots credit_lots_topup_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY credit_lots
    ADD CONSTRAINT credit_lots_topup_id_fkey FOREIGN KEY (topup_id) REFERENCES topups(id);


--
-- Name: credit_transfers credit_transfers_recipient_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY credit_transfers
    ADD CONSTRAINT credit_transfers_recipient_account_id_fkey FOREIGN KEY (recipient_account_id) REFERENCES accounts(id);


--
-- Name: credit_transfers credit_transfers_sender_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY credit_transfers
    ADD CONSTRAINT credit_transfers_sender_account_id_fkey FOREIGN KEY (sender_account_id) REFERENCES accounts(id);


--
-- Name: gateway_node_certificates gateway_node_certificates_node_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY gateway_node_certificates
    ADD CONSTRAINT gateway_node_certificates_node_id_fkey FOREIGN KEY (node_id) REFERENCES gateway_nodes(id);


--
-- Name: gateway_node_certificates gateway_node_certificates_replaced_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY gateway_node_certificates
    ADD CONSTRAINT gateway_node_certificates_replaced_by_id_fkey FOREIGN KEY (replaced_by_id) REFERENCES gateway_node_certificates(id);


--
-- Name: gateway_usage_metrics gateway_usage_metrics_rate_version_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY gateway_usage_metrics
    ADD CONSTRAINT gateway_usage_metrics_rate_version_id_fkey FOREIGN KEY (rate_version_id) REFERENCES billing_rate_versions(id);


--
-- Name: gateway_usage_metrics gateway_usage_metrics_ucgid_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY gateway_usage_metrics
    ADD CONSTRAINT gateway_usage_metrics_ucgid_fkey FOREIGN KEY (ucgid) REFERENCES gateway_usage_records(ucgid);


--
-- Name: gateway_usage_records gateway_usage_records_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY gateway_usage_records
    ADD CONSTRAINT gateway_usage_records_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id);


-- Name: gateway_usage_records gateway_usage_records_ledger_transaction_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY gateway_usage_records
    ADD CONSTRAINT gateway_usage_records_ledger_transaction_id_fkey FOREIGN KEY (ledger_transaction_id) REFERENCES ledger_transactions(id);


--
-- Name: gateway_usage_records gateway_usage_records_node_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY gateway_usage_records
    ADD CONSTRAINT gateway_usage_records_node_id_fkey FOREIGN KEY (node_id) REFERENCES gateway_nodes(id);


--
-- Name: gateway_usage_records gateway_usage_records_rate_publication_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY gateway_usage_records
    ADD CONSTRAINT gateway_usage_records_rate_publication_id_fkey FOREIGN KEY (rate_publication_id) REFERENCES billing_rate_publications(id);


--
-- Name: invoices invoices_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY invoices
    ADD CONSTRAINT invoices_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id);


--
-- Name: invoices invoices_topup_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY invoices
    ADD CONSTRAINT invoices_topup_id_fkey FOREIGN KEY (topup_id) REFERENCES topups(id);


--
-- Name: ledger_accounts ledger_accounts_owner_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY ledger_accounts
    ADD CONSTRAINT ledger_accounts_owner_account_id_fkey FOREIGN KEY (owner_account_id) REFERENCES accounts(id);


--
-- Name: ledger_entries ledger_entries_ledger_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY ledger_entries
    ADD CONSTRAINT ledger_entries_ledger_account_id_fkey FOREIGN KEY (ledger_account_id) REFERENCES ledger_accounts(id);


--
-- Name: ledger_entries ledger_entries_transaction_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY ledger_entries
    ADD CONSTRAINT ledger_entries_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES ledger_transactions(id);


--
-- Name: ledger_transactions ledger_transactions_initiated_by_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY ledger_transactions
    ADD CONSTRAINT ledger_transactions_initiated_by_account_id_fkey FOREIGN KEY (initiated_by_account_id) REFERENCES accounts(id);


--
-- Name: merchant_accounts merchant_accounts_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY merchant_accounts
    ADD CONSTRAINT merchant_accounts_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id);


--
-- Name: merchant_accounts merchant_accounts_owner_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY merchant_accounts
    ADD CONSTRAINT merchant_accounts_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES users(id);


--
-- Name: merchant_payment_reversals merchant_payment_reversals_ledger_transaction_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY merchant_payment_reversals
    ADD CONSTRAINT merchant_payment_reversals_ledger_transaction_id_fkey FOREIGN KEY (ledger_transaction_id) REFERENCES ledger_transactions(id);


--
-- Name: merchant_payment_reversals merchant_payment_reversals_merchant_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY merchant_payment_reversals
    ADD CONSTRAINT merchant_payment_reversals_merchant_account_id_fkey FOREIGN KEY (merchant_account_id) REFERENCES accounts(id);


--
-- Name: merchant_payment_reversals merchant_payment_reversals_payer_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY merchant_payment_reversals
    ADD CONSTRAINT merchant_payment_reversals_payer_account_id_fkey FOREIGN KEY (payer_account_id) REFERENCES accounts(id);


--
-- Name: merchant_payment_reversals merchant_payment_reversals_payment_intent_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY merchant_payment_reversals
    ADD CONSTRAINT merchant_payment_reversals_payment_intent_id_fkey FOREIGN KEY (payment_intent_id) REFERENCES payment_intents(id);


--
-- Name: merchant_services merchant_services_merchant_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY merchant_services
    ADD CONSTRAINT merchant_services_merchant_account_id_fkey FOREIGN KEY (merchant_account_id) REFERENCES merchant_accounts(account_id);


--
-- Name: merchant_transactions merchant_transactions_merchant_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY merchant_transactions
    ADD CONSTRAINT merchant_transactions_merchant_account_id_fkey FOREIGN KEY (merchant_account_id) REFERENCES accounts(id);


--
-- Name: merchant_transactions merchant_transactions_payment_intent_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY merchant_transactions
    ADD CONSTRAINT merchant_transactions_payment_intent_id_fkey FOREIGN KEY (payment_intent_id) REFERENCES payment_intents(id);


--
-- Name: payment_intents payment_intents_merchant_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY payment_intents
    ADD CONSTRAINT payment_intents_merchant_account_id_fkey FOREIGN KEY (merchant_account_id) REFERENCES merchant_accounts(account_id);


--
-- Name: payment_intents payment_intents_payer_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY payment_intents
    ADD CONSTRAINT payment_intents_payer_account_id_fkey FOREIGN KEY (payer_account_id) REFERENCES accounts(id);


--
-- Name: payment_intents payment_intents_service_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY payment_intents
    ADD CONSTRAINT payment_intents_service_id_fkey FOREIGN KEY (service_id) REFERENCES merchant_services(id);


--
-- Name: refunds refunds_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY refunds
    ADD CONSTRAINT refunds_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id);


--
-- Name: refunds refunds_topup_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY refunds
    ADD CONSTRAINT refunds_topup_id_fkey FOREIGN KEY (topup_id) REFERENCES topups(id);


--
-- Name: risk_decisions risk_decisions_merchant_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY risk_decisions
    ADD CONSTRAINT risk_decisions_merchant_account_id_fkey FOREIGN KEY (merchant_account_id) REFERENCES merchant_accounts(account_id);


--
-- Name: risk_decisions risk_decisions_service_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY risk_decisions
    ADD CONSTRAINT risk_decisions_service_id_fkey FOREIGN KEY (service_id) REFERENCES merchant_services(id);


--
-- Name: topups topups_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY topups
    ADD CONSTRAINT topups_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id);


--
-- Name: user_sessions user_sessions_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY user_sessions
    ADD CONSTRAINT user_sessions_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id);


--
-- Name: webhook_deliveries webhook_deliveries_endpoint_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY webhook_deliveries
    ADD CONSTRAINT webhook_deliveries_endpoint_id_fkey FOREIGN KEY (endpoint_id) REFERENCES webhook_endpoints(id);


--
-- Name: webhook_deliveries webhook_deliveries_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY webhook_deliveries
    ADD CONSTRAINT webhook_deliveries_event_id_fkey FOREIGN KEY (event_id) REFERENCES webhook_events(id);


--
-- Name: webhook_endpoint_commands webhook_endpoint_commands_endpoint_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY webhook_endpoint_commands
    ADD CONSTRAINT webhook_endpoint_commands_endpoint_id_fkey FOREIGN KEY (endpoint_id) REFERENCES webhook_endpoints(id);


--
-- Name: webhook_endpoint_commands webhook_endpoint_commands_merchant_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY webhook_endpoint_commands
    ADD CONSTRAINT webhook_endpoint_commands_merchant_account_id_fkey FOREIGN KEY (merchant_account_id) REFERENCES accounts(id);


--
-- Name: webhook_endpoints webhook_endpoints_merchant_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY webhook_endpoints
    ADD CONSTRAINT webhook_endpoints_merchant_account_id_fkey FOREIGN KEY (merchant_account_id) REFERENCES accounts(id);


--
-- Name: webhook_events webhook_events_merchant_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY webhook_events
    ADD CONSTRAINT webhook_events_merchant_account_id_fkey FOREIGN KEY (merchant_account_id) REFERENCES accounts(id);


--
-- PostgreSQL database dump complete
--
