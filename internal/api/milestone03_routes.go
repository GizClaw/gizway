package api

import "net/http"

func (s *Server) registerMilestone03Routes(mux *http.ServeMux) {
	for _, route := range []string{
		"POST /webhooks/v1/zitadel/user-authenticated",
		"GET /account/v1/accounts",
		"GET /account/v1/accounts/{account_id}/balance",
		"GET /account/v1/accounts/{account_id}/transactions",
		"GET /account/v1/accounts/{account_id}/charges",
		"GET /account/v1/accounts/{account_id}/topups",
		"POST /account/v1/accounts/{account_id}/topups",
		"GET /account/v1/service-accounts",
		"POST /account/v1/service-accounts",
		"DELETE /account/v1/service-accounts/{service_account_id}",
		"GET /account/v1/merchants",
		"POST /account/v1/merchants",
		"GET /account/v1/merchants/{merchant_id}",
		"PATCH /account/v1/merchants/{merchant_id}",
		"GET /account/v1/merchants/{merchant_id}/products",
		"POST /account/v1/merchants/{merchant_id}/products",
		"GET /account/v1/products",
		"GET /account/v1/products/{product_id}",
		"PATCH /account/v1/products/{product_id}",
		"POST /account/v1/products/{product_id}/subscriptions",
		"GET /account/v1/subscriptions",
		"GET /account/v1/subscriptions/{subscription_id}",
		"PATCH /account/v1/subscriptions/{subscription_id}",
		"GET /account/v1/subscriptions/{subscription_id}/keys",
		"POST /account/v1/subscriptions/{subscription_id}/keys",
		"GET /account/v1/subscriptions/{subscription_id}/keys/{subscription_key_id}",
		"POST /account/v1/subscriptions/{subscription_id}/keys/{subscription_key_id}/revoke",
		"POST /service/v1/subscription-credit-checks",
		"POST /service/v1/payg-charges",
		"GET /service/v1/payg-charges/{external_order_id}",
		"POST /user/v1/providers/{provider_id}/keys",
		"PUT /user/v1/provider-keys/{provider_key_id}/prices",
		"POST /user/v1/provider-keys/{provider_key_id}/disable",
		"GET /auth/catalog-token",
		"GET /auth/runtime-config",
		"GET /v1/models",
		"POST /v1/chat/completions",
		"POST /v1/messages",
		"POST /v1beta/models/{operation}",
		"POST /v1/realtime/client_secrets",
		"GET /v1/realtime",
	} {
		mux.Handle(route, http.HandlerFunc(s.milestone03NotImplemented))
	}
}

func (s Surface) String() string {
	if s == SurfaceGizPay {
		return "gizpay"
	}
	return "gizway"
}
