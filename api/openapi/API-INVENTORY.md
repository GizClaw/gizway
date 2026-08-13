
# Current implementation API inventory

Final retained operation count: **54**.

| OpenAPI file | Method | Path | operationId | Service | Status | Deletion reason | Hurl coverage |
|---|---|---|---|---|---|---|---|
| account.yaml | GET | `/account/v1/accounts` | listAccounts | GizPay | Keep | - | tests/api/stories/23-milestone-02/01-account-subscription-and-keys.hurl |
| account.yaml | GET | `/account/v1/accounts/{account_id}/balance` | getAccountBalance | GizPay | Keep | - | tests/api/stories/23-milestone-02/03-charge-commission-and-ledger.hurl |
| account.yaml | GET | `/account/v1/accounts/{account_id}/charges` | listAccountCharges | GizPay | Keep | - | tests/api/stories/23-milestone-02/03-charge-commission-and-ledger.hurl |
| account.yaml | GET | `/account/v1/accounts/{account_id}/transactions` | listAccountTransactions | GizPay | Keep | - | tests/api/stories/23-milestone-02/03-charge-commission-and-ledger.hurl |
| account.yaml | GET | `/account/v1/merchants` | listMerchants | GizPay | Keep | - | tests/api/stories/23-milestone-02/01-account-subscription-and-keys.hurl |
| account.yaml | POST | `/account/v1/merchants` | createMerchant | GizPay | Keep | - | tests/api/stories/23-milestone-02/01-account-subscription-and-keys.hurl |
| account.yaml | GET | `/account/v1/merchants/{merchant_id}` | getMerchant | GizPay | Keep | - | tests/api/stories/23-milestone-02/01-account-subscription-and-keys.hurl |
| account.yaml | PATCH | `/account/v1/merchants/{merchant_id}` | updateMerchant | GizPay | Keep | - | tests/api/stories/23-milestone-02/01-account-subscription-and-keys.hurl |
| account.yaml | GET | `/account/v1/merchants/{merchant_id}/products` | listMerchantProducts | GizPay | Keep | - | tests/api/stories/23-milestone-02/01-account-subscription-and-keys.hurl |
| account.yaml | POST | `/account/v1/merchants/{merchant_id}/products` | createMerchantProduct | GizPay | Keep | - | tests/api/stories/23-milestone-02/01-account-subscription-and-keys.hurl |
| account.yaml | GET | `/account/v1/products/{product_id}` | getProduct | GizPay | Keep | - | tests/api/stories/23-milestone-02/01-account-subscription-and-keys.hurl |
| account.yaml | PATCH | `/account/v1/products/{product_id}` | updateProduct | GizPay | Keep | - | tests/api/stories/23-milestone-02/01-account-subscription-and-keys.hurl |
| account.yaml | POST | `/account/v1/products/{product_id}/subscriptions` | createProductSubscription | GizPay | Keep | - | tests/api/stories/23-milestone-02/01-account-subscription-and-keys.hurl |
| account.yaml | GET | `/account/v1/service-accounts` | listServiceAccounts | GizPay | Keep | - | tests/api/stories/23-milestone-02/01-account-subscription-and-keys.hurl |
| account.yaml | POST | `/account/v1/service-accounts` | createServiceAccount | GizPay | Keep | - | tests/api/stories/23-milestone-02/01-account-subscription-and-keys.hurl |
| account.yaml | DELETE | `/account/v1/service-accounts/{service_account_id}` | revokeServiceAccount | GizPay | Keep | - | tests/api/stories/23-milestone-02/01-account-subscription-and-keys.hurl |
| account.yaml | GET | `/account/v1/subscriptions` | listSubscriptions | GizPay | Keep | - | tests/api/stories/23-milestone-02/01-account-subscription-and-keys.hurl |
| account.yaml | GET | `/account/v1/subscriptions/{subscription_id}` | getSubscription | GizPay | Keep | - | tests/api/stories/23-milestone-02/01-account-subscription-and-keys.hurl |
| account.yaml | PATCH | `/account/v1/subscriptions/{subscription_id}` | updateSubscription | GizPay | Keep | - | tests/api/stories/23-milestone-02/01-account-subscription-and-keys.hurl |
| account.yaml | GET | `/account/v1/subscriptions/{subscription_id}/api-keys` | listSubscriptionAPIKeys | GizPay | Keep | - | tests/api/stories/23-milestone-02/01-account-subscription-and-keys.hurl |
| account.yaml | POST | `/account/v1/subscriptions/{subscription_id}/api-keys` | createSubscriptionAPIKey | GizPay | Keep | - | tests/api/stories/23-milestone-02/01-account-subscription-and-keys.hurl |
| account.yaml | GET | `/account/v1/subscriptions/{subscription_id}/api-keys/{api_key_id}` | getSubscriptionAPIKey | GizPay | Keep | - | tests/api/stories/23-milestone-02/01-account-subscription-and-keys.hurl |
| account.yaml | POST | `/account/v1/subscriptions/{subscription_id}/api-keys/{api_key_id}/revoke` | revokeSubscriptionAPIKey | GizPay | Keep | - | tests/api/stories/23-milestone-02/01-account-subscription-and-keys.hurl |
| gizway-admin.yaml | GET | `/admin/v1/ai-orders` | listAIOrders | GizWay | Keep | - | tests/api/stories/23-milestone-02/05-ai-protocols-and-orders.hurl |
| gizway-admin.yaml | GET | `/admin/v1/bifrost-logs` | listBifrostLogs | GizWay | Keep | - | tests/api/stories/23-milestone-02/05-ai-protocols-and-orders.hurl |
| gizway-admin.yaml | GET | `/admin/v1/charge-outbox` | listChargeOutbox | GizWay | Keep | - | tests/api/stories/23-milestone-02/05-ai-protocols-and-orders.hurl |
| gizway-admin.yaml | GET | `/admin/v1/models` | listAdminModels | GizWay | Keep | - | tests/api/stories/23-milestone-02/04-regional-admin-and-bifrost.hurl |
| gizway-admin.yaml | POST | `/admin/v1/models` | createModel | GizWay | Keep | - | tests/api/stories/23-milestone-02/04-regional-admin-and-bifrost.hurl |
| gizway-admin.yaml | GET | `/admin/v1/models/{model_id}` | getModel | GizWay | Keep | - | tests/api/stories/23-milestone-02/04-regional-admin-and-bifrost.hurl |
| gizway-admin.yaml | PATCH | `/admin/v1/models/{model_id}` | updateModel | GizWay | Keep | - | tests/api/stories/23-milestone-02/04-regional-admin-and-bifrost.hurl |
| gizway-admin.yaml | GET | `/admin/v1/models/{model_id}/prices` | getModelPrices | GizWay | Keep | - | tests/api/stories/23-milestone-02/04-regional-admin-and-bifrost.hurl |
| gizway-admin.yaml | PUT | `/admin/v1/models/{model_id}/prices` | putModelPrices | GizWay | Keep | - | tests/api/stories/23-milestone-02/04-regional-admin-and-bifrost.hurl |
| gizway-admin.yaml | GET | `/admin/v1/provider-api-keys/{bifrost_key_id}` | getProviderAPIKey | GizWay | Keep | - | tests/api/stories/23-milestone-02/04-regional-admin-and-bifrost.hurl |
| gizway-admin.yaml | PATCH | `/admin/v1/provider-api-keys/{bifrost_key_id}` | updateProviderAPIKey | GizWay | Keep | - | tests/api/stories/23-milestone-02/04-regional-admin-and-bifrost.hurl |
| gizway-admin.yaml | GET | `/admin/v1/provider-api-keys/{bifrost_key_id}/billing` | getProviderAPIKeyBilling | GizWay | Keep | - | tests/api/stories/23-milestone-02/04-regional-admin-and-bifrost.hurl |
| gizway-admin.yaml | PUT | `/admin/v1/provider-api-keys/{bifrost_key_id}/billing` | putProviderAPIKeyBilling | GizWay | Keep | - | tests/api/stories/23-milestone-02/04-regional-admin-and-bifrost.hurl |
| gizway-admin.yaml | POST | `/admin/v1/provider-api-keys/{bifrost_key_id}/disable` | disableProviderAPIKey | GizWay | Keep | - | tests/api/stories/23-milestone-02/04-regional-admin-and-bifrost.hurl |
| gizway-admin.yaml | GET | `/admin/v1/provider-api-keys/{bifrost_key_id}/prices` | getProviderAPIKeyPrices | GizWay | Keep | - | tests/api/stories/23-milestone-02/04-regional-admin-and-bifrost.hurl |
| gizway-admin.yaml | PUT | `/admin/v1/provider-api-keys/{bifrost_key_id}/prices` | putProviderAPIKeyPrices | GizWay | Keep | - | tests/api/stories/23-milestone-02/04-regional-admin-and-bifrost.hurl |
| gizway-admin.yaml | GET | `/admin/v1/providers` | listProviders | GizWay | Keep | - | tests/api/stories/23-milestone-02/04-regional-admin-and-bifrost.hurl |
| gizway-admin.yaml | POST | `/admin/v1/providers` | createProvider | GizWay | Keep | - | tests/api/stories/23-milestone-02/04-regional-admin-and-bifrost.hurl |
| gizway-admin.yaml | GET | `/admin/v1/providers/{provider_id}` | getProvider | GizWay | Keep | - | tests/api/stories/23-milestone-02/04-regional-admin-and-bifrost.hurl |
| gizway-admin.yaml | PATCH | `/admin/v1/providers/{provider_id}` | updateProvider | GizWay | Keep | - | tests/api/stories/23-milestone-02/04-regional-admin-and-bifrost.hurl |
| gizway-admin.yaml | GET | `/admin/v1/providers/{provider_id}/api-keys` | listProviderAPIKeys | GizWay | Keep | - | tests/api/stories/23-milestone-02/04-regional-admin-and-bifrost.hurl |
| gizway-admin.yaml | POST | `/admin/v1/providers/{provider_id}/api-keys` | createProviderAPIKey | GizWay | Keep | - | tests/api/stories/23-milestone-02/04-regional-admin-and-bifrost.hurl |
| gizway-public.yaml | POST | `/v1/chat/completions` | createChatCompletion | GizWay | Keep | - | tests/api/stories/23-milestone-02/05-ai-protocols-and-orders.hurl |
| gizway-public.yaml | POST | `/v1/messages` | createAnthropicMessage | GizWay | Keep | - | tests/api/stories/23-milestone-02/05-ai-protocols-and-orders.hurl |
| gizway-public.yaml | GET | `/v1/models` | listModels | GizWay | Keep | - | tests/api/stories/23-milestone-02/05-ai-protocols-and-orders.hurl |
| gizway-public.yaml | GET | `/v1/realtime` | connectRealtimeWebSocket | GizWay | Keep | - | tests/api/stories/23-milestone-02/05-ai-protocols-and-orders.hurl |
| gizway-public.yaml | POST | `/v1/realtime/client_secrets` | createRealtimeClientSecret | GizWay | Keep | - | tests/api/stories/23-milestone-02/05-ai-protocols-and-orders.hurl |
| gizway-public.yaml | POST | `/v1beta/models/{operation}` | generateGeminiContent | GizWay | Keep | - | tests/api/stories/23-milestone-02/05-ai-protocols-and-orders.hurl |
| internal-gizpay.yaml | POST | `/service/v1/payg-charges` | createPAYGCharge | GizPay | Keep | - | tests/api/stories/23-milestone-02/03-charge-commission-and-ledger.hurl |
| internal-gizpay.yaml | GET | `/service/v1/payg-charges/{external_order_id}` | getPAYGCharge | GizPay | Keep | - | tests/api/stories/23-milestone-02/03-charge-commission-and-ledger.hurl |
| internal-gizpay.yaml | POST | `/service/v1/subscription-credit-checks` | checkSubscriptionCredit | GizPay | Keep | - | tests/api/stories/23-milestone-02/02-credit-check.hurl |
| account.yaml | GET | `/account/v1/accounts/{account_id}/models` | listAccountModels | GizPay | Delete | Regional Catalog belongs to GizWay; GizPay has no Model or Provider tables, so this cross-database projection is not part of the split architecture. | Removed with route, handler, Store implementation, and obsolete Hurl coverage. |
| gizpay-admin.yaml | PUT | `/admin/v1/accounts/{account_id}/model_entitlements/{model_id}` | setAccountModelEntitlement | GizPay | Delete | Account-to-Catalog entitlement would duplicate regional Model identity in GizPay and reintroduce the removed merged-database boundary. | Removed with route, handler, Store implementation, and obsolete Hurl coverage. |
