# Milestone 03 API

The normative HTTP behavior and ownership rules are encoded by the four current
executable contracts in this directory and their API acceptance tests:

- `account.yaml`: GizPay human APIs, including idempotent initialization,
  Accounts, Merchants, Products, Subscriptions, plaintext Subscription Keys,
  Top-ups, Charges, transactions, and Service Accounts.
- `internal-gizpay.yaml`: machine Credit Check and Charge APIs.
- `gizway-user.yaml`: regional Provider Key command APIs.
- `gizway-public.yaml`: Subscription Key authenticated OpenAI, Anthropic,
  Gemini, Streaming, and Realtime compatibility routes.

GizPay is the central financial service. CN and Global are independent GizWay
deployments with independent regional databases and embedded Bifrost stores.
User query data is delivered by the three owner-filtered PowerSync services.
