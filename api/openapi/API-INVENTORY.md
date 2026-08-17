
# Current implementation API inventory

Final retained operation count: **41**.

| OpenAPI file | Method | Path | operationId | Service | Status | Deletion reason | Hurl coverage |
|---|---|---|---|---|---|---|---|
| account.yaml | GET | `/account/v1/accounts` | listAccounts | GizPay | Keep | - | tests/api/stories/24-milestone-03/01-initialize-and-account.hurl |
| account.yaml | GET | `/account/v1/accounts/{account_id}/balance` | getAccountBalance | GizPay | Keep | - | tests/api/stories/24-milestone-03/01-initialize-and-account.hurl |
| account.yaml | GET | `/account/v1/accounts/{account_id}/charges` | listAccountCharges | GizPay | Keep | - | tests/api/stories/24-milestone-03/01-initialize-and-account.hurl |
| account.yaml | GET | `/account/v1/accounts/{account_id}/topups` | listAccountTopups | GizPay | Keep | - | tests/api/stories/24-milestone-03/04-topups-and-ledger.hurl |
| account.yaml | POST | `/account/v1/accounts/{account_id}/topups` | createAccountTopup | GizPay | Keep | - | tests/api/stories/24-milestone-03/04-topups-and-ledger.hurl<br>tests/api/stories/25-milestone-04/01-idempotent-writes.hurl |
| account.yaml | GET | `/account/v1/accounts/{account_id}/transactions` | listAccountTransactions | GizPay | Keep | - | tests/api/stories/24-milestone-03/01-initialize-and-account.hurl |
| account.yaml | GET | `/account/v1/merchants` | listMerchants | GizPay | Keep | - | tests/api/stories/24-milestone-03/02-merchant-product-subscription.hurl |
| account.yaml | POST | `/account/v1/merchants` | createMerchant | GizPay | Keep | - | tests/api/stories/24-milestone-03/02-merchant-product-subscription.hurl |
| account.yaml | GET | `/account/v1/merchants/{merchant_id}` | getMerchant | GizPay | Keep | - | tests/api/stories/24-milestone-03/02-merchant-product-subscription.hurl |
| account.yaml | PATCH | `/account/v1/merchants/{merchant_id}` | updateMerchant | GizPay | Keep | - | tests/api/stories/24-milestone-03/02-merchant-product-subscription.hurl |
| account.yaml | GET | `/account/v1/merchants/{merchant_id}/products` | listMerchantProducts | GizPay | Keep | - | tests/api/stories/24-milestone-03/02-merchant-product-subscription.hurl |
| account.yaml | POST | `/account/v1/merchants/{merchant_id}/products` | createMerchantProduct | GizPay | Keep | - | tests/api/stories/24-milestone-03/02-merchant-product-subscription.hurl |
| account.yaml | GET | `/account/v1/products` | listProducts | GizPay | Keep | - | tests/api/stories/24-milestone-03/02-merchant-product-subscription.hurl |
| account.yaml | GET | `/account/v1/products/{product_id}` | getProduct | GizPay | Keep | - | tests/api/stories/24-milestone-03/02-merchant-product-subscription.hurl |
| account.yaml | PATCH | `/account/v1/products/{product_id}` | updateProduct | GizPay | Keep | - | tests/api/stories/24-milestone-03/02-merchant-product-subscription.hurl |
| account.yaml | POST | `/account/v1/products/{product_id}/subscriptions` | createProductSubscription | GizPay | Keep | - | tests/api/stories/24-milestone-03/02-merchant-product-subscription.hurl<br>tests/api/stories/25-milestone-04/01-idempotent-writes.hurl |
| account.yaml | GET | `/account/v1/service-accounts` | listServiceAccounts | GizPay | Keep | - | tests/api/stories/24-milestone-03/05-service-accounts-and-charge.hurl |
| account.yaml | POST | `/account/v1/service-accounts` | createServiceAccount | GizPay | Keep | - | tests/api/stories/24-milestone-03/05-service-accounts-and-charge.hurl |
| account.yaml | DELETE | `/account/v1/service-accounts/{service_account_id}` | revokeServiceAccount | GizPay | Keep | - | tests/api/stories/24-milestone-03/05-service-accounts-and-charge.hurl |
| account.yaml | GET | `/account/v1/subscriptions` | listSubscriptions | GizPay | Keep | - | tests/api/stories/24-milestone-03/02-merchant-product-subscription.hurl |
| account.yaml | GET | `/account/v1/subscriptions/{subscription_id}` | getSubscription | GizPay | Keep | - | tests/api/stories/24-milestone-03/02-merchant-product-subscription.hurl |
| account.yaml | PATCH | `/account/v1/subscriptions/{subscription_id}` | updateSubscription | GizPay | Keep | - | tests/api/stories/24-milestone-03/02-merchant-product-subscription.hurl |
| account.yaml | GET | `/account/v1/subscriptions/{subscription_id}/keys` | listSubscriptionKeys | GizPay | Keep | - | tests/api/stories/24-milestone-03/03-subscription-keys.hurl |
| account.yaml | POST | `/account/v1/subscriptions/{subscription_id}/keys` | createSubscriptionKey | GizPay | Keep | - | tests/api/stories/24-milestone-03/03-subscription-keys.hurl<br>tests/api/stories/25-milestone-04/01-idempotent-writes.hurl |
| account.yaml | GET | `/account/v1/subscriptions/{subscription_id}/keys/{subscription_key_id}` | getSubscriptionKey | GizPay | Keep | - | tests/api/stories/24-milestone-03/03-subscription-keys.hurl |
| account.yaml | POST | `/account/v1/subscriptions/{subscription_id}/keys/{subscription_key_id}/revoke` | revokeSubscriptionKey | GizPay | Keep | - | tests/api/stories/24-milestone-03/03-subscription-keys.hurl |
| gizpay-webhooks.yaml | POST | `/webhooks/v1/zitadel/user-authenticated` | initializeHumanFromZitadel | GizPay | Keep | - | tests/api/stories/25-milestone-04/02-auth-catalog-and-errors.hurl |
| gizway-public.yaml | GET | `/auth/catalog-token` | getPublicCatalogToken | GizWay | Keep | - | tests/api/stories/25-milestone-04/02-auth-catalog-and-errors.hurl |
| gizway-public.yaml | GET | `/auth/runtime-config` | getPublicRuntimeConfig | GizWay | Keep | - | tests/api/stories/25-milestone-04/02-auth-catalog-and-errors.hurl |
| gizway-public.yaml | POST | `/v1/chat/completions` | createChatCompletion | GizWay | Keep | - | tests/api/stories/24-milestone-03/08-ai-protocols.hurl |
| gizway-public.yaml | POST | `/v1/messages` | createAnthropicMessage | GizWay | Keep | - | tests/api/stories/24-milestone-03/08-ai-protocols.hurl |
| gizway-public.yaml | GET | `/v1/models` | listModels | GizWay | Keep | - | tests/api/stories/24-milestone-03/08-ai-protocols.hurl |
| gizway-public.yaml | GET | `/v1/realtime` | connectRealtimeWebSocket | GizWay | Keep | - | tests/api/stories/24-milestone-03/08-ai-protocols.hurl |
| gizway-public.yaml | POST | `/v1/realtime/client_secrets` | createRealtimeClientSecret | GizWay | Keep | - | tests/api/stories/24-milestone-03/08-ai-protocols.hurl |
| gizway-public.yaml | POST | `/v1beta/models/{operation}` | generateGeminiContent | GizWay | Keep | - | tests/api/stories/24-milestone-03/08-ai-protocols.hurl |
| gizway-user.yaml | POST | `/user/v1/provider-keys/{provider_key_id}/disable` | disableProviderKey | GizWay | Keep | - | tests/api/stories/24-milestone-03/06-provider-key-commands.hurl |
| gizway-user.yaml | PUT | `/user/v1/provider-keys/{provider_key_id}/prices` | putProviderKeyPrices | GizWay | Keep | - | tests/api/stories/24-milestone-03/06-provider-key-commands.hurl |
| gizway-user.yaml | POST | `/user/v1/providers/{provider_id}/keys` | createProviderKey | GizWay | Keep | - | tests/api/stories/24-milestone-03/06-provider-key-commands.hurl<br>tests/api/stories/25-milestone-04/01-idempotent-writes.hurl |
| internal-gizpay.yaml | POST | `/service/v1/payg-charges` | createPAYGCharge | GizPay | Keep | - | tests/api/stories/24-milestone-03/05-service-accounts-and-charge.hurl |
| internal-gizpay.yaml | GET | `/service/v1/payg-charges/{external_order_id}` | getPAYGCharge | GizPay | Keep | - | tests/api/stories/24-milestone-03/05-service-accounts-and-charge.hurl |
| internal-gizpay.yaml | POST | `/service/v1/subscription-credit-checks` | checkSubscriptionCredit | GizPay | Keep | - | tests/api/stories/24-milestone-03/05-service-accounts-and-charge.hurl |
| account.yaml | GET | `/account/v1/accounts/{account_id}/models` | listAccountModels | GizPay | Delete | Regional Catalog belongs to GizWay; GizPay has no Model or Provider tables, so this cross-database projection is not part of the split architecture. | Removed with route, handler, Store implementation, and obsolete Hurl coverage. |
| gizpay-admin.yaml | PUT | `/admin/v1/accounts/{account_id}/model_entitlements/{model_id}` | setAccountModelEntitlement | GizPay | Delete | Account-to-Catalog entitlement would duplicate regional Model identity in GizPay and reintroduce the removed merged-database boundary. | Removed with route, handler, Store implementation, and obsolete Hurl coverage. |
