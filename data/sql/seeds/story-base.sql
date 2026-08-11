-- Automated API acceptance fixture.
--
-- Mutable story resources (new keys, payments, transfers, and usage) are not
-- seeded. Every Hurl file receives a fresh copy of this database and creates
-- its own mutable state through the public API.

INSERT INTO users (id, email, display_name, status, created_at, updated_at) VALUES
('11000000-0000-4000-8000-000000000001', 'active-one@gizway.test', 'Active User One', 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('11000000-0000-4000-8000-000000000002', 'active-two@gizway.test', 'Active User Two', 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('11000000-0000-4000-8000-000000000003', 'suspended@gizway.test', 'Suspended User', 'suspended', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z');

-- Story user password: story-user-password. Authentication is real bcrypt;
-- the deterministic hash is fixture data, never a production credential.
UPDATE users SET password_hash='$2y$10$iAzJ1RTYRWDK8DJ33HhM8.qCkBLeI9pqEP4ArSvxyld9ir736xIwm';

INSERT INTO accounts (id, owner_user_id, kind, name, status, created_at, updated_at) VALUES
('21000000-0000-4000-8000-000000000001', '11000000-0000-4000-8000-000000000001', 'personal', 'Active One Personal', 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('21000000-0000-4000-8000-000000000002', '11000000-0000-4000-8000-000000000002', 'personal', 'Active Two Personal', 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('21000000-0000-4000-8000-000000000003', '11000000-0000-4000-8000-000000000003', 'personal', 'Suspended Personal', 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('22000000-0000-4000-8000-000000000002', '11000000-0000-4000-8000-000000000002', 'merchant', 'Approved Story Merchant', 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z');

INSERT INTO merchant_accounts (account_id, owner_user_id, legal_name, public_name, review_level, merchant_status, country_code, website_url, created_at, updated_at) VALUES
('22000000-0000-4000-8000-000000000002', '11000000-0000-4000-8000-000000000002', 'Approved Story Merchant LLC', 'Approved Story Merchant', 'enhanced', 'approved', 'US', 'https://merchant.gizway.test', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z');

INSERT INTO merchant_services (id,merchant_account_id,service_code,name,description,interface_set,status,max_transaction_microcredits,daily_limit_microcredits,idempotency_key,payload_hash,created_at,updated_at) VALUES
('23000000-0000-4000-8000-000000000001','22000000-0000-4000-8000-000000000002','vpn','Reviewed Story VPN','Deterministic approved digital service','["checkout","webhook"]','approved',10000000,50000000,'story-seeded-service',decode('00','hex'),'2026-08-10T00:00:00.000000000Z','2026-08-10T00:00:00.000000000Z');

INSERT INTO risk_decisions (id,merchant_account_id,service_id,provider_reference,decision,kyc_status,kyb_status,sanctions_status,anomaly_score,reason,created_at) VALUES
('24000000-0000-4000-8000-000000000001','22000000-0000-4000-8000-000000000002','23000000-0000-4000-8000-000000000001','risk_seed_vpn','allow','verified','verified','clear',5,'story fixture approved','2026-08-10T00:00:00.000000000Z');

INSERT INTO user_sessions (id, user_id, secret_hash, status, expires_at, created_at) VALUES
('12000000-0000-4000-8000-000000000001', '11000000-0000-4000-8000-000000000001', decode('2fa038181de4cab470fd8495334195a71923ef70521df4ba849202d7504507bd','hex'), 'active', '2027-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('12000000-0000-4000-8000-000000000002', '11000000-0000-4000-8000-000000000002', decode('7adf9d94d3f00ff29cd8c9f4bc13cdd58e407313b95636c69b2f270c27219499','hex'), 'active', '2027-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('12000000-0000-4000-8000-000000000003', '11000000-0000-4000-8000-000000000003', decode('7a39e89b915972a12ee01542d0f2baffe007b37a9c8f42061d9ec8b0e017e433','hex'), 'active', '2027-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z');

-- Plain fixture values are supplied only by tests. The database stores SHA-256.
INSERT INTO api_keys (id, account_id, kind, name, key_prefix, secret_hash, scopes, status, created_at) VALUES
('31000000-0000-4000-8000-000000000001', '21000000-0000-4000-8000-000000000001', 'gateway', 'Story bootstrap user one', 'giz_story_user_active_1', decode('e320c5b58c2c735d5716e6b5a2c3fc956a1533c3754f989b5623ae5a71185071','hex'), '["account:self","gateway:invoke","gateway:usage:read"]', 'active', '2026-08-10T00:00:00.000000000Z'),
('31000000-0000-4000-8000-000000000002', '21000000-0000-4000-8000-000000000002', 'gateway', 'Story bootstrap user two', 'giz_story_user_active_2', decode('af2b6487182e6cc77d741719174301178969426d7263733be6bb6e3033d8439e','hex'), '["account:self","gateway:invoke","gateway:usage:read"]', 'active', '2026-08-10T00:00:00.000000000Z'),
('31000000-0000-4000-8000-000000000003', '21000000-0000-4000-8000-000000000003', 'gateway', 'Story suspended user', 'giz_story_user_suspended', decode('92e44ba588e5ba60781b843f3190d163c6de014a757074249aa9b90a7444a089','hex'), '["account:self"]', 'active', '2026-08-10T00:00:00.000000000Z');

INSERT INTO administrators (id, email, display_name, password_hash, status, created_at, updated_at) VALUES
('41000000-0000-4000-8000-000000000001', 'admin@gizway.test', 'Story Administrator', '$2y$10$PO.gPoH/.5ICr0hdws7NYeZ5Iz7EWQbINiTr70nxWnla9MPiQOoHa', 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z');

INSERT INTO admin_api_keys (id, administrator_id, name, key_prefix, secret_hash, status, created_at) VALUES
('51000000-0000-4000-8000-000000000001', '41000000-0000-4000-8000-000000000001', 'Story Admin Key', 'gizadm_story_admin', decode('55c85a39d2ad9897f5f689558eac0d4ee496adbffc13171f0c21333bf37db943','hex'), 'active', '2026-08-10T00:00:00.000000000Z');

INSERT INTO providers (id, slug, name, status, created_at, updated_at) VALUES
('61000000-0000-4000-8000-000000000001', 'fake-openai', 'Fake OpenAI', 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z');

INSERT INTO provider_endpoints (id, provider_id, name, base_url, credential_ref, region, priority, weight, status, created_at, updated_at) VALUES
('71000000-0000-4000-8000-000000000001', '61000000-0000-4000-8000-000000000001', 'Story Fake Provider', 'http://127.0.0.1:1', 'story/fake-openai', NULL, 100, 100, 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z');

INSERT INTO models (id, slug, name, modality, status, metadata, created_at, updated_at) VALUES
('81000000-0000-4000-8000-000000000001', 'story-text', 'Story Text Model', '["text"]', 'active', '{}', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('81000000-0000-4000-8000-000000000002', 'story-disabled', 'Story Disabled Model', '["text"]', 'disabled', '{}', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z');

INSERT INTO model_variants (id, model_id, provider_endpoint_id, provider_model_name, variant_slug, capabilities, context_window, max_output_tokens, status, created_at, updated_at) VALUES
('91000000-0000-4000-8000-000000000001', '81000000-0000-4000-8000-000000000001', '71000000-0000-4000-8000-000000000001', 'fake-text-v1', 'fake-openai', '{"chat":true,"responses":true,"messages":true,"generateContent":true,"embeddings":true,"audio_speech":true,"audio_transcriptions":true,"image_generation":true,"realtime":true,"realtime_webrtc_callback":true}', 8192, 2048, 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('91000000-0000-4000-8000-000000000003', '81000000-0000-4000-8000-000000000001', '71000000-0000-4000-8000-000000000001', 'fake-text-v2', 'zz-fallback-openai', '{"chat":true}', 8192, 2048, 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('91000000-0000-4000-8000-000000000002', '81000000-0000-4000-8000-000000000002', '71000000-0000-4000-8000-000000000001', 'fake-disabled-v1', 'fake-disabled', '{"chat":true}', 8192, 2048, 'disabled', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z');

INSERT INTO model_variant_prices (id, model_variant_id, metric, unit_size, upstream_cost_microcredits, base_customer_price_microcredits, customer_price_microcredits, discount_bps, valid_from, valid_until, created_at) VALUES
('a1000000-0000-4000-8000-000000000001', '91000000-0000-4000-8000-000000000001', 'input_token', 1000, 1000, 2000, 1800, 1000, '2026-08-10T00:00:00.000000000Z', NULL, '2026-08-10T00:00:00.000000000Z'),
('a1000000-0000-4000-8000-000000000002', '91000000-0000-4000-8000-000000000001', 'output_token', 1000, 2000, 4000, 3600, 1000, '2026-08-10T00:00:00.000000000Z', NULL, '2026-08-10T00:00:00.000000000Z'),
('a1000000-0000-4000-8000-000000000003', '91000000-0000-4000-8000-000000000001', 'cached_input_token', 1000, 500, 1000, 900, 1000, '2026-08-10T00:00:00.000000000Z', NULL, '2026-08-10T00:00:00.000000000Z'),
('a1000000-0000-4000-8000-000000000004', '91000000-0000-4000-8000-000000000001', 'audio_second', 1, 10, 20, 18, 1000, '2026-08-10T00:00:00.000000000Z', NULL, '2026-08-10T00:00:00.000000000Z'),
('a1000000-0000-4000-8000-000000000005', '91000000-0000-4000-8000-000000000001', 'image', 1, 50, 100, 90, 1000, '2026-08-10T00:00:00.000000000Z', NULL, '2026-08-10T00:00:00.000000000Z'),
('a1000000-0000-4000-8000-000000000006', '91000000-0000-4000-8000-000000000001', 'request', 1, 5, 10, 9, 1000, '2026-08-10T00:00:00.000000000Z', NULL, '2026-08-10T00:00:00.000000000Z'),
('a1000000-0000-4000-8000-000000000007', '91000000-0000-4000-8000-000000000003', 'input_token', 1000, 1000, 2000, 1800, 1000, '2026-08-10T00:00:00.000000000Z', NULL, '2026-08-10T00:00:00.000000000Z'),
('a1000000-0000-4000-8000-000000000008', '91000000-0000-4000-8000-000000000003', 'output_token', 1000, 2000, 4000, 3600, 1000, '2026-08-10T00:00:00.000000000Z', NULL, '2026-08-10T00:00:00.000000000Z'),
('a1000000-0000-4000-8000-000000000009', '91000000-0000-4000-8000-000000000003', 'cached_input_token', 1000, 500, 1000, 900, 1000, '2026-08-10T00:00:00.000000000Z', NULL, '2026-08-10T00:00:00.000000000Z'),
-- Realtime audio is tokenized by the provider and priced separately from text.
-- The aggregate prompt/completion totals remain observable, while settlement
-- subtracts these subsets from text to avoid charging any token twice.
('a1000000-0000-4000-8000-000000000010', '91000000-0000-4000-8000-000000000001', 'input_audio_token', 1000, 1500, 3000, 2700, 1000, '2026-08-10T00:00:00.000000000Z', NULL, '2026-08-10T00:00:00.000000000Z'),
('a1000000-0000-4000-8000-000000000011', '91000000-0000-4000-8000-000000000001', 'output_audio_token', 1000, 3000, 6000, 5400, 1000, '2026-08-10T00:00:00.000000000Z', NULL, '2026-08-10T00:00:00.000000000Z');

INSERT INTO ledger_accounts (id, owner_account_id, code, kind, asset_code, normal_balance, status, created_at, updated_at) VALUES
('b1000000-0000-4000-8000-000000000001', '21000000-0000-4000-8000-000000000001', 'USER:STORY:ONE', 'user_credit', 'GIZ_CREDIT', 'credit', 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('b1000000-0000-4000-8000-000000000002', '21000000-0000-4000-8000-000000000002', 'USER:STORY:TWO', 'user_credit', 'GIZ_CREDIT', 'credit', 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('b1000000-0000-4000-8000-000000000003', '21000000-0000-4000-8000-000000000003', 'USER:STORY:SUSPENDED', 'user_credit', 'GIZ_CREDIT', 'credit', 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('b1000000-0000-4000-8000-000000000006', '22000000-0000-4000-8000-000000000002', 'MERCHANT:STORY:TWO', 'merchant_credit', 'GIZ_CREDIT', 'credit', 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('b1000000-0000-4000-8000-000000000004', NULL, 'SYSTEM:CREDIT_LIABILITY', 'system_credit_liability', 'GIZ_CREDIT', 'debit', 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('b1000000-0000-4000-8000-000000000005', NULL, 'SYSTEM:PLATFORM_FEE_REVENUE', 'platform_fee_revenue', 'GIZ_CREDIT', 'credit', 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z');

INSERT INTO ledger_transactions (id, transaction_type, status, idempotency_key, description, created_at, posted_at) VALUES
('c1000000-0000-4000-8000-000000000001', 'topup', 'posted', 'story-opening-one', 'Story opening balance one', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('c1000000-0000-4000-8000-000000000002', 'topup', 'posted', 'story-opening-two', 'Story opening balance two', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z');

INSERT INTO ledger_entries (id, transaction_id, ledger_account_id, sequence, direction, amount_microcredits, created_at) VALUES
('d1000000-0000-4000-8000-000000000001', 'c1000000-0000-4000-8000-000000000001', 'b1000000-0000-4000-8000-000000000001', 1, 'credit', 100000000, '2026-08-10T00:00:00.000000000Z'),
('d1000000-0000-4000-8000-000000000002', 'c1000000-0000-4000-8000-000000000001', 'b1000000-0000-4000-8000-000000000004', 2, 'debit', 100000000, '2026-08-10T00:00:00.000000000Z'),
('d1000000-0000-4000-8000-000000000003', 'c1000000-0000-4000-8000-000000000002', 'b1000000-0000-4000-8000-000000000002', 1, 'credit', 50000000, '2026-08-10T00:00:00.000000000Z'),
('d1000000-0000-4000-8000-000000000004', 'c1000000-0000-4000-8000-000000000002', 'b1000000-0000-4000-8000-000000000004', 2, 'debit', 50000000, '2026-08-10T00:00:00.000000000Z');

-- A durable failed delivery gives Admin Story 09 a real retryable outbox row.
INSERT INTO webhook_endpoints (id, merchant_account_id, url, events, signing_secret, status, created_at, updated_at) VALUES
('e1000000-0000-4000-8000-000000000001', '22000000-0000-4000-8000-000000000002', 'http://127.0.0.1:1/unreachable', '["payment_intent.succeeded"]', 'story-seeded-webhook-secret', 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z');
INSERT INTO webhook_events (id, merchant_account_id, event_type, resource_id, payload, created_at) VALUES
('e2000000-0000-4000-8000-000000000001', '22000000-0000-4000-8000-000000000002', 'payment_intent.succeeded', 'seeded-payment-intent', '{"id":"e2000000-0000-4000-8000-000000000001","type":"payment_intent.succeeded","data":{"object":{"id":"seeded-payment-intent"}}}', '2026-08-10T00:00:00.000000000Z');
INSERT INTO webhook_deliveries (id, event_id, endpoint_id, attempt, status, error, created_at, completed_at) VALUES
('e3000000-0000-4000-8000-000000000001', 'e2000000-0000-4000-8000-000000000001', 'e1000000-0000-4000-8000-000000000001', 1, 'failed', 'fixture connection failure', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:01.000000000Z');
