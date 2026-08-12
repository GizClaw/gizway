-- GizWay target schema. CN and Global apply this same migration to separate
-- databases. It directly creates regional Catalog, execution, and transient
-- Usage state without customer Account/API-Key projection or payment tables.


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


SET default_tablespace = '';

SET default_table_access_method = heap;

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
-- Name: gateway_executions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE gateway_executions (
    id text NOT NULL,
    public_model text NOT NULL,
    model_variant_id text NOT NULL,
    provider_request_id text,
    protocol text NOT NULL,
    stream_mode text NOT NULL,
    rate_publication_id text NOT NULL,
    status text NOT NULL,
    estimated_microcredits bigint NOT NULL,
    actual_microcredits bigint,
    started_at utc_timestamp_text NOT NULL,
    completed_at utc_timestamp_text,
    CONSTRAINT gateway_executions_actual_microcredits_check CHECK ((actual_microcredits >= 0)),
    CONSTRAINT gateway_executions_estimated_microcredits_check CHECK ((estimated_microcredits >= 0)),
    CONSTRAINT gateway_executions_protocol_check CHECK ((protocol = ANY (ARRAY['https'::text, 'sse'::text, 'websocket'::text, 'webrtc'::text]))),
    CONSTRAINT gateway_executions_status_check CHECK ((status = ANY (ARRAY['admitted'::text, 'running'::text, 'completed'::text, 'failed'::text])))
);


--
-- Name: gateway_usage_metrics; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE gateway_usage_metrics (
    ucgid text NOT NULL,
    metric text NOT NULL,
    quantity bigint NOT NULL,
    unit_size bigint NOT NULL,
    rate_version_id text NOT NULL,
    CONSTRAINT gateway_usage_metrics_quantity_check CHECK ((quantity >= 0)),
    CONSTRAINT gateway_usage_metrics_unit_size_check CHECK ((unit_size > 0))
);


--
-- Name: gateway_usage_outbox; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE gateway_usage_outbox (
    ucgid text NOT NULL,
    process_epoch text NOT NULL,
    runtime_key_token text NOT NULL,
    operation_id text NOT NULL,
    public_model text NOT NULL,
    model_variant_id text NOT NULL,
    rate_publication_id text NOT NULL,
    period_started_at utc_timestamp_text NOT NULL,
    period_ended_at utc_timestamp_text NOT NULL,
    gateway_calculated_microcredits bigint NOT NULL,
    canonical_payload_hash bytea NOT NULL,
    payload jsonb NOT NULL,
    status text NOT NULL,
    attempt_count bigint DEFAULT 0 NOT NULL,
    next_attempt_at utc_timestamp_text NOT NULL,
    last_error text,
    created_at utc_timestamp_text NOT NULL,
    updated_at utc_timestamp_text NOT NULL,
    reported_at utc_timestamp_text,
    abandoned_at utc_timestamp_text,
    failed_at utc_timestamp_text,
    CONSTRAINT gateway_usage_outbox_attempt_count_check CHECK ((attempt_count >= 0)),
    CONSTRAINT gateway_usage_outbox_gateway_calculated_microcredits_check CHECK ((gateway_calculated_microcredits >= 0)),
    CONSTRAINT gateway_usage_outbox_payload_check CHECK ((NOT (payload ? 'api_key'::text))),
    CONSTRAINT gateway_usage_outbox_payload_check1 CHECK ((NOT (payload ? 'api_key_id'::text))),
    CONSTRAINT gateway_usage_outbox_payload_check2 CHECK ((NOT (payload ? 'account_id'::text))),
    CONSTRAINT gateway_usage_outbox_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'sending'::text, 'reported'::text, 'abandoned'::text, 'failed'::text])))
);


--
-- Name: model_variant_prices; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE model_variant_prices (
    id text NOT NULL,
    model_variant_id text NOT NULL,
    metric text NOT NULL,
    unit_size bigint DEFAULT 1 NOT NULL,
    upstream_cost_microcredits bigint NOT NULL,
    base_customer_price_microcredits bigint NOT NULL,
    customer_price_microcredits bigint NOT NULL,
    discount_bps bigint DEFAULT 0 NOT NULL,
    valid_from utc_timestamp_text NOT NULL,
    valid_until utc_timestamp_text,
    created_at utc_timestamp_text NOT NULL,
    CONSTRAINT model_variant_prices_base_customer_price_microcredits_check CHECK ((base_customer_price_microcredits >= 0)),
    CONSTRAINT model_variant_prices_check CHECK ((customer_price_microcredits <= base_customer_price_microcredits)),
    CONSTRAINT model_variant_prices_customer_price_microcredits_check CHECK ((customer_price_microcredits >= 0)),
    CONSTRAINT model_variant_prices_discount_bps_check CHECK (((discount_bps >= 0) AND (discount_bps <= 10000))),
    CONSTRAINT model_variant_prices_metric_check CHECK ((metric = ANY (ARRAY['input_token'::text, 'output_token'::text, 'cached_input_token'::text, 'input_audio_token'::text, 'output_audio_token'::text, 'audio_second'::text, 'image'::text, 'video_second'::text, 'request'::text]))),
    CONSTRAINT model_variant_prices_unit_size_check CHECK ((unit_size > 0)),
    CONSTRAINT model_variant_prices_upstream_cost_microcredits_check CHECK ((upstream_cost_microcredits >= 0))
);


--
-- Name: model_variants; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE model_variants (
    id text NOT NULL,
    model_id text NOT NULL,
    provider_endpoint_id text NOT NULL,
    provider_model_name text NOT NULL,
    variant_slug text NOT NULL,
    capabilities jsonb DEFAULT '{}'::jsonb NOT NULL,
    context_window bigint,
    max_output_tokens bigint,
    status text DEFAULT 'active'::text NOT NULL,
    created_at utc_timestamp_text NOT NULL,
    updated_at utc_timestamp_text NOT NULL,
    CONSTRAINT model_variants_status_check CHECK ((status = ANY (ARRAY['active'::text, 'degraded'::text, 'disabled'::text])))
);


--
-- Name: models; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE models (
    id text NOT NULL,
    slug text NOT NULL,
    name text NOT NULL,
    modality jsonb DEFAULT '["text"]'::jsonb NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at utc_timestamp_text NOT NULL,
    updated_at utc_timestamp_text NOT NULL,
    CONSTRAINT models_status_check CHECK ((status = ANY (ARRAY['active'::text, 'deprecated'::text, 'disabled'::text])))
);


--
-- Name: provider_endpoints; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE provider_endpoints (
    id text NOT NULL,
    provider_id text NOT NULL,
    name text NOT NULL,
    base_url text NOT NULL,
    credential_ref text NOT NULL,
    region text,
    priority bigint DEFAULT 100 NOT NULL,
    weight bigint DEFAULT 100 NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    created_at utc_timestamp_text NOT NULL,
    updated_at utc_timestamp_text NOT NULL,
    CONSTRAINT provider_endpoints_status_check CHECK ((status = ANY (ARRAY['active'::text, 'draining'::text, 'disabled'::text]))),
    CONSTRAINT provider_endpoints_weight_check CHECK ((weight > 0))
);


--
-- Name: providers; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE providers (
    id text NOT NULL,
    slug text NOT NULL,
    name text NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    created_at utc_timestamp_text NOT NULL,
    updated_at utc_timestamp_text NOT NULL,
    CONSTRAINT providers_status_check CHECK ((status = ANY (ARRAY['active'::text, 'disabled'::text])))
);


--
-- Name: rate_publication_versions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE rate_publication_versions (
    publication_id text NOT NULL,
    rate_version_id text NOT NULL,
    model_variant_id text NOT NULL,
    public_model text NOT NULL,
    metric text NOT NULL,
    unit_size bigint NOT NULL,
    base_price_microcredits bigint NOT NULL,
    customer_price_microcredits bigint NOT NULL,
    discount_bps integer NOT NULL,
    CONSTRAINT rate_publication_versions_base_price_microcredits_check CHECK ((base_price_microcredits >= 0)),
    CONSTRAINT rate_publication_versions_customer_price_microcredits_check CHECK ((customer_price_microcredits >= 0)),
    CONSTRAINT rate_publication_versions_discount_bps_check CHECK (((discount_bps >= 0) AND (discount_bps <= 10000))),
    CONSTRAINT rate_publication_versions_unit_size_check CHECK ((unit_size > 0))
);


--
-- Name: rate_publications; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE rate_publications (
    id text NOT NULL,
    region text NOT NULL,
    revision bigint NOT NULL,
    content_hash bytea NOT NULL,
    status text NOT NULL,
    gizpay_publication_id text,
    effective_at utc_timestamp_text,
    created_at utc_timestamp_text NOT NULL,
    updated_at utc_timestamp_text NOT NULL,
    CONSTRAINT rate_publications_revision_check CHECK ((revision > 0)),
    CONSTRAINT rate_publications_status_check CHECK ((status = ANY (ARRAY['draft'::text, 'publishing'::text, 'active'::text, 'failed'::text, 'retired'::text])))
);


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
-- Name: audit_events sequence; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY audit_events ALTER COLUMN sequence SET DEFAULT nextval('audit_events_sequence_seq'::regclass);


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
-- Name: gateway_executions gateway_executions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY gateway_executions
    ADD CONSTRAINT gateway_executions_pkey PRIMARY KEY (id);


--
-- Name: gateway_usage_metrics gateway_usage_metrics_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY gateway_usage_metrics
    ADD CONSTRAINT gateway_usage_metrics_pkey PRIMARY KEY (ucgid, metric, rate_version_id);


--
-- Name: gateway_usage_outbox gateway_usage_outbox_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY gateway_usage_outbox
    ADD CONSTRAINT gateway_usage_outbox_pkey PRIMARY KEY (ucgid);


--
-- Name: model_variant_prices model_variant_prices_model_variant_id_metric_valid_from_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY model_variant_prices
    ADD CONSTRAINT model_variant_prices_model_variant_id_metric_valid_from_key UNIQUE (model_variant_id, metric, valid_from);


--
-- Name: model_variant_prices model_variant_prices_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY model_variant_prices
    ADD CONSTRAINT model_variant_prices_pkey PRIMARY KEY (id);


--
-- Name: model_variants model_variants_model_id_variant_slug_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY model_variants
    ADD CONSTRAINT model_variants_model_id_variant_slug_key UNIQUE (model_id, variant_slug);


--
-- Name: model_variants model_variants_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY model_variants
    ADD CONSTRAINT model_variants_pkey PRIMARY KEY (id);


--
-- Name: model_variants model_variants_provider_endpoint_id_provider_model_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY model_variants
    ADD CONSTRAINT model_variants_provider_endpoint_id_provider_model_name_key UNIQUE (provider_endpoint_id, provider_model_name);


--
-- Name: models models_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY models
    ADD CONSTRAINT models_pkey PRIMARY KEY (id);


--
-- Name: models models_slug_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY models
    ADD CONSTRAINT models_slug_key UNIQUE (slug);


--
-- Name: provider_endpoints provider_endpoints_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY provider_endpoints
    ADD CONSTRAINT provider_endpoints_pkey PRIMARY KEY (id);


--
-- Name: provider_endpoints provider_endpoints_provider_id_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY provider_endpoints
    ADD CONSTRAINT provider_endpoints_provider_id_name_key UNIQUE (provider_id, name);


--
-- Name: providers providers_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY providers
    ADD CONSTRAINT providers_pkey PRIMARY KEY (id);


--
-- Name: providers providers_slug_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY providers
    ADD CONSTRAINT providers_slug_key UNIQUE (slug);


--
-- Name: rate_publication_versions rate_publication_versions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY rate_publication_versions
    ADD CONSTRAINT rate_publication_versions_pkey PRIMARY KEY (publication_id, model_variant_id, metric);


--
-- Name: rate_publications rate_publications_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY rate_publications
    ADD CONSTRAINT rate_publications_pkey PRIMARY KEY (id);


--
-- Name: rate_publications rate_publications_region_revision_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY rate_publications
    ADD CONSTRAINT rate_publications_region_revision_key UNIQUE (region, revision);


--
-- Name: request_rate_limits request_rate_limits_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY request_rate_limits
    ADD CONSTRAINT request_rate_limits_pkey PRIMARY KEY (scope_key, action, window_started_at);


--
-- Name: administrators_email_casefold_unique; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX administrators_email_casefold_unique ON administrators USING btree (lower(email));

-- Each regional administrator credential digest resolves to exactly one local
-- operator. CN and Global enforce this independently in their own databases.
CREATE UNIQUE INDEX admin_api_keys_secret_hash_uidx ON admin_api_keys USING btree (secret_hash);


--
-- Name: audit_events_resource_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX audit_events_resource_idx ON audit_events USING btree (resource_type, resource_id, created_at);

-- This partial index matches ClaimUsage exactly. Reported and abandoned rows
-- never participate in the current-process retry scan.
CREATE INDEX gateway_usage_outbox_claim_idx ON gateway_usage_outbox USING btree (process_epoch, runtime_key_token, next_attempt_at, created_at, ucgid) WHERE (status = ANY (ARRAY['pending'::text, 'sending'::text]));


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
-- Name: gateway_executions gateway_executions_model_variant_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY gateway_executions
    ADD CONSTRAINT gateway_executions_model_variant_id_fkey FOREIGN KEY (model_variant_id) REFERENCES model_variants(id);


--
-- Name: gateway_executions gateway_executions_rate_publication_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY gateway_executions
    ADD CONSTRAINT gateway_executions_rate_publication_id_fkey FOREIGN KEY (rate_publication_id) REFERENCES rate_publications(id);


--
-- Name: gateway_usage_outbox gateway_usage_outbox_operation_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY gateway_usage_outbox
    ADD CONSTRAINT gateway_usage_outbox_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES gateway_executions(id);


--
-- Name: gateway_usage_metrics gateway_usage_metrics_ucgid_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY gateway_usage_metrics
    ADD CONSTRAINT gateway_usage_metrics_ucgid_fkey FOREIGN KEY (ucgid) REFERENCES gateway_usage_outbox(ucgid);


--
-- Name: model_variant_prices model_variant_prices_model_variant_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY model_variant_prices
    ADD CONSTRAINT model_variant_prices_model_variant_id_fkey FOREIGN KEY (model_variant_id) REFERENCES model_variants(id);


--
-- Name: model_variants model_variants_model_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY model_variants
    ADD CONSTRAINT model_variants_model_id_fkey FOREIGN KEY (model_id) REFERENCES models(id);


--
-- Name: model_variants model_variants_provider_endpoint_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY model_variants
    ADD CONSTRAINT model_variants_provider_endpoint_id_fkey FOREIGN KEY (provider_endpoint_id) REFERENCES provider_endpoints(id);


--
-- Name: provider_endpoints provider_endpoints_provider_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY provider_endpoints
    ADD CONSTRAINT provider_endpoints_provider_id_fkey FOREIGN KEY (provider_id) REFERENCES providers(id);


--
-- Name: rate_publication_versions rate_publication_versions_model_variant_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY rate_publication_versions
    ADD CONSTRAINT rate_publication_versions_model_variant_id_fkey FOREIGN KEY (model_variant_id) REFERENCES model_variants(id);


--
-- Name: rate_publication_versions rate_publication_versions_publication_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY rate_publication_versions
    ADD CONSTRAINT rate_publication_versions_publication_id_fkey FOREIGN KEY (publication_id) REFERENCES rate_publications(id);


--
-- Name: rate_publication_versions rate_publication_versions_rate_version_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY rate_publication_versions
    ADD CONSTRAINT rate_publication_versions_rate_version_id_fkey FOREIGN KEY (rate_version_id) REFERENCES model_variant_prices(id);


--
-- PostgreSQL database dump complete
--
