# OpenAPI contracts

This directory contains only versioned, Gizway-owned API specifications:

- `common.yaml` for shared authentication, errors, pagination, and Credit amounts.
- `payment.yaml` for merchant payment intents, transactions, and webhooks.
- `account.yaml` for profile, accounts, API keys, balances, top-ups,
  original-route refunds, transfers, usage, merchants, and transaction history.
- `admin.yaml` for audited operations over users, merchant reviews, providers,
  model variants, effective pricing, usage, payments, ledger, and webhooks.

Provider-owned protocol schemas remain upstream-owned. Gizway should test its
compatibility against those schemas instead of maintaining divergent copies.

Bifrost's native HTTP API is an internal implementation detail and is not a
public Gizway surface. OpenAI, Anthropic, and Gemini clients use their respective
compatible endpoints and official SDKs; their schemas are intentionally absent
from this directory.

The Admin API intentionally does not expose raw table CRUD. Historical prices
are append-only, provider credentials are write-only, and posted ledger changes
use balanced adjustments or compensating reversals.

Administration uses a flat account model rather than RBAC. Every active
administrator has the same authority and may authenticate with a short-lived
web session or a separately stored Admin API Key. Customer API keys cannot call
Admin endpoints.
