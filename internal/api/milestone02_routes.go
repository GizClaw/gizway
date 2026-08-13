package api

import (
	"net/http"
)

func (s *Server) registerMilestone02Routes(mux *http.ServeMux) {
	mux.Handle("GET /account/v1/accounts", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("GET /account/v1/accounts/{account_id}/balance", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("GET /account/v1/accounts/{account_id}/transactions", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("GET /account/v1/accounts/{account_id}/charges", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("GET /account/v1/service-accounts", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("POST /account/v1/service-accounts", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("DELETE /account/v1/service-accounts/{service_account_id}", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("GET /account/v1/merchants", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("POST /account/v1/merchants", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("GET /account/v1/merchants/{merchant_id}", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("PATCH /account/v1/merchants/{merchant_id}", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("GET /account/v1/merchants/{merchant_id}/products", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("POST /account/v1/merchants/{merchant_id}/products", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("GET /account/v1/products/{product_id}", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("PATCH /account/v1/products/{product_id}", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("POST /account/v1/products/{product_id}/subscriptions", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("GET /account/v1/subscriptions", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("GET /account/v1/subscriptions/{subscription_id}", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("PATCH /account/v1/subscriptions/{subscription_id}", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("GET /account/v1/subscriptions/{subscription_id}/api-keys", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("POST /account/v1/subscriptions/{subscription_id}/api-keys", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("GET /account/v1/subscriptions/{subscription_id}/api-keys/{api_key_id}", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("POST /account/v1/subscriptions/{subscription_id}/api-keys/{api_key_id}/revoke", http.HandlerFunc(s.milestone02NotImplemented))

	mux.Handle("POST /service/v1/subscription-credit-checks", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("POST /service/v1/payg-charges", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("GET /service/v1/payg-charges/{external_order_id}", http.HandlerFunc(s.milestone02NotImplemented))

	mux.Handle("GET /admin/v1/models", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("POST /admin/v1/models", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("GET /admin/v1/models/{model_id}", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("PATCH /admin/v1/models/{model_id}", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("GET /admin/v1/models/{model_id}/prices", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("PUT /admin/v1/models/{model_id}/prices", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("GET /admin/v1/providers", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("POST /admin/v1/providers", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("GET /admin/v1/providers/{provider_id}", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("PATCH /admin/v1/providers/{provider_id}", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("GET /admin/v1/providers/{provider_id}/api-keys", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("POST /admin/v1/providers/{provider_id}/api-keys", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("GET /admin/v1/provider-api-keys/{bifrost_key_id}", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("PATCH /admin/v1/provider-api-keys/{bifrost_key_id}", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("POST /admin/v1/provider-api-keys/{bifrost_key_id}/disable", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("GET /admin/v1/provider-api-keys/{bifrost_key_id}/billing", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("PUT /admin/v1/provider-api-keys/{bifrost_key_id}/billing", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("GET /admin/v1/provider-api-keys/{bifrost_key_id}/prices", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("PUT /admin/v1/provider-api-keys/{bifrost_key_id}/prices", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("GET /admin/v1/ai-orders", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("GET /admin/v1/charge-outbox", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("GET /admin/v1/bifrost-logs", http.HandlerFunc(s.milestone02NotImplemented))

	mux.Handle("GET /v1/models", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("POST /v1/chat/completions", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("POST /v1/messages", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("POST /v1beta/models/{operation}", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("POST /v1/realtime/client_secrets", http.HandlerFunc(s.milestone02NotImplemented))
	mux.Handle("GET /v1/realtime", http.HandlerFunc(s.milestone02NotImplemented))
}

func (s Surface) String() string {
	if s == SurfaceGizPay {
		return "gizpay"
	}
	return "gizway"
}
