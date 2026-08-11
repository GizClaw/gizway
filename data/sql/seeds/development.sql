INSERT INTO users (id, email, display_name, status, created_at, updated_at) VALUES
('10000000-0000-4000-8000-000000000001', 'demo@gizway.dev', 'Demo User', 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z');

-- Development-only password: story-user-password.
UPDATE users SET password_hash='$2y$10$iAzJ1RTYRWDK8DJ33HhM8.qCkBLeI9pqEP4ArSvxyld9ir736xIwm';

INSERT INTO accounts (id, owner_user_id, kind, name, status, created_at, updated_at) VALUES
('20000000-0000-4000-8000-000000000001', '10000000-0000-4000-8000-000000000001', 'personal', 'Demo Account', 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z');

INSERT INTO api_keys (id, account_id, kind, name, key_prefix, secret_hash, scopes, status, created_at) VALUES
('30000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000001', 'gateway', 'Development Key', 'giz_dev_', decode('6f3d9039dfc12b5770c46c36e1af0ec952279f3c5fa382f1de1ed641c9cea3f8','hex'), '["account:self","gateway:invoke","gateway:usage:read"]', 'active', '2026-08-10T00:00:00.000000000Z');

INSERT INTO administrators (id, email, display_name, status, created_at, updated_at) VALUES
('40000000-0000-4000-8000-000000000001', 'admin@gizway.dev', 'Development Admin', 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z');

INSERT INTO admin_api_keys (id, administrator_id, name, key_prefix, secret_hash, status, created_at) VALUES
('50000000-0000-4000-8000-000000000001', '40000000-0000-4000-8000-000000000001', 'Development Admin Key', 'gizadm_dev_', decode('7d4ca26b8f4cac24842c078d81b77717bbdafe4a03ab1448bbd8f819885a7d01','hex'), 'active', '2026-08-10T00:00:00.000000000Z');

INSERT INTO providers (id, slug, name, status, created_at, updated_at) VALUES
('60000000-0000-4000-8000-000000000001', 'openai', 'OpenAI', 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('60000000-0000-4000-8000-000000000002', 'anthropic', 'Anthropic', 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('60000000-0000-4000-8000-000000000003', 'google', 'Google', 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z');

INSERT INTO provider_endpoints (id, provider_id, name, base_url, credential_ref, region, priority, weight, status, created_at, updated_at) VALUES
('70000000-0000-4000-8000-000000000001', '60000000-0000-4000-8000-000000000001', 'OpenAI Global', 'https://api.openai.com', 'dev/openai', NULL, 100, 100, 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('70000000-0000-4000-8000-000000000002', '60000000-0000-4000-8000-000000000002', 'Anthropic Global', 'https://api.anthropic.com', 'dev/anthropic', NULL, 100, 100, 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('70000000-0000-4000-8000-000000000003', '60000000-0000-4000-8000-000000000003', 'Gemini Global', 'https://generativelanguage.googleapis.com', 'dev/google', NULL, 100, 100, 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z');

INSERT INTO models (id, slug, name, modality, status, metadata, created_at, updated_at) VALUES
('80000000-0000-4000-8000-000000000001', 'gpt-5', 'GPT-5', '["text","image"]', 'active', '{}', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('80000000-0000-4000-8000-000000000002', 'claude-sonnet', 'Claude Sonnet', '["text","image"]', 'active', '{}', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('80000000-0000-4000-8000-000000000003', 'gemini-pro', 'Gemini Pro', '["text","image","audio","video"]', 'active', '{}', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z');

INSERT INTO model_variants (id, model_id, provider_endpoint_id, provider_model_name, variant_slug, capabilities, context_window, max_output_tokens, status, created_at, updated_at) VALUES
('90000000-0000-4000-8000-000000000001', '80000000-0000-4000-8000-000000000001', '70000000-0000-4000-8000-000000000001', 'gpt-5', 'openai', '{"chat":true,"responses":true}', 400000, 128000, 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('90000000-0000-4000-8000-000000000002', '80000000-0000-4000-8000-000000000002', '70000000-0000-4000-8000-000000000002', 'claude-sonnet', 'anthropic', '{"messages":true}', 200000, 64000, 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('90000000-0000-4000-8000-000000000003', '80000000-0000-4000-8000-000000000003', '70000000-0000-4000-8000-000000000003', 'gemini-pro', 'google', '{"generateContent":true}', 1000000, 64000, 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z');

INSERT INTO model_variant_prices (id, model_variant_id, metric, unit_size, upstream_cost_microcredits, base_customer_price_microcredits, customer_price_microcredits, discount_bps, valid_from, created_at) VALUES
('a0000000-0000-4000-8000-000000000001', '90000000-0000-4000-8000-000000000001', 'input_token', 1000000, 1000000, 1200000, 1080000, 1000, '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('a0000000-0000-4000-8000-000000000002', '90000000-0000-4000-8000-000000000001', 'output_token', 1000000, 8000000, 9600000, 8640000, 1000, '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('a0000000-0000-4000-8000-000000000005', '90000000-0000-4000-8000-000000000001', 'cached_input_token', 1000000, 500000, 600000, 540000, 1000, '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('a0000000-0000-4000-8000-000000000003', '90000000-0000-4000-8000-000000000002', 'input_token', 1000000, 3000000, 3600000, 3240000, 1000, '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('a0000000-0000-4000-8000-000000000004', '90000000-0000-4000-8000-000000000003', 'input_token', 1000000, 1250000, 1500000, 1350000, 1000, '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z');

INSERT INTO ledger_accounts (id, owner_account_id, code, kind, asset_code, normal_balance, status, created_at, updated_at) VALUES
('b0000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000001', 'USER:DEMO:GIZ_CREDIT', 'user_credit', 'GIZ_CREDIT', 'credit', 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('b0000000-0000-4000-8000-000000000002', NULL, 'SYSTEM:CREDIT_LIABILITY', 'system_credit_liability', 'GIZ_CREDIT', 'debit', 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z'),
('b0000000-0000-4000-8000-000000000003', NULL, 'SYSTEM:PLATFORM_FEE_REVENUE', 'platform_fee_revenue', 'GIZ_CREDIT', 'credit', 'active', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z');

INSERT INTO ledger_transactions (id, transaction_type, status, idempotency_key, description, created_at, posted_at) VALUES
('c0000000-0000-4000-8000-000000000001', 'topup', 'posted', 'seed-demo-balance', 'Development seed balance', '2026-08-10T00:00:00.000000000Z', '2026-08-10T00:00:00.000000000Z');

INSERT INTO ledger_entries (id, transaction_id, ledger_account_id, sequence, direction, amount_microcredits, created_at) VALUES
('d0000000-0000-4000-8000-000000000001', 'c0000000-0000-4000-8000-000000000001', 'b0000000-0000-4000-8000-000000000001', 1, 'credit', 100000000, '2026-08-10T00:00:00.000000000Z'),
('d0000000-0000-4000-8000-000000000002', 'c0000000-0000-4000-8000-000000000001', 'b0000000-0000-4000-8000-000000000002', 2, 'debit', 100000000, '2026-08-10T00:00:00.000000000Z');
