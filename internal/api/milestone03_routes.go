package api

import (
	"net/http"
	"strings"
)

func (s *Server) registerMilestone03Routes(mux *http.ServeMux) {
	routes := []string{
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
		"GET /openai/v1/models",
		"POST /openai/v1/chat/completions",
		"POST /anthropic/v1/messages",
		"POST /genai/v1beta/models/{model}:generateContent",
		"POST /genai/v1beta/models/{model}:streamGenerateContent",
		"POST /openai/v1/realtime/client_secrets",
		"GET /openai/v1/realtime",
	}
	if s.surface == SurfaceGizPay {
		routes = append(routes,
			"POST /admin/v1/products",
			"GET /admin/v1/products",
			"GET /admin/v1/products/{product_id}",
			"PATCH /admin/v1/products/{product_id}",
			"DELETE /admin/v1/products/{product_id}",
			"POST /admin/v1/product-listings",
			"GET /admin/v1/product-listings",
			"GET /admin/v1/product-listings/{product_listing_id}",
			"PATCH /admin/v1/product-listings/{product_listing_id}",
			"DELETE /admin/v1/product-listings/{product_listing_id}",
			"POST /admin/v1/service-principals",
			"GET /admin/v1/service-principals",
			"GET /admin/v1/service-principals/{service_principal_id}",
			"PATCH /admin/v1/service-principals/{service_principal_id}",
			"DELETE /admin/v1/service-principals/{service_principal_id}",
		)
	} else {
		routes = append(routes,
			"POST /admin/v1/providers",
			"GET /admin/v1/providers",
			"GET /admin/v1/providers/{provider_id}",
			"PATCH /admin/v1/providers/{provider_id}",
			"DELETE /admin/v1/providers/{provider_id}",
			"POST /admin/v1/models",
			"GET /admin/v1/models",
			"GET /admin/v1/models/{model_id}",
			"PATCH /admin/v1/models/{model_id}",
			"DELETE /admin/v1/models/{model_id}",
			"GET /admin/v1/models/{model_id}/customer-prices",
			"PUT /admin/v1/models/{model_id}/customer-prices",
			"POST /admin/v1/model-listings",
			"GET /admin/v1/model-listings",
			"GET /admin/v1/model-listings/{model_listing_id}",
			"PATCH /admin/v1/model-listings/{model_listing_id}",
			"DELETE /admin/v1/model-listings/{model_listing_id}",
			"POST /admin/v1/provider-keys",
			"GET /admin/v1/provider-keys",
			"GET /admin/v1/provider-keys/{provider_key_id}",
			"PATCH /admin/v1/provider-keys/{provider_key_id}",
			"DELETE /admin/v1/provider-keys/{provider_key_id}",
			"POST /admin/v1/provider-keys/{provider_key_id}/rotate-secret",
			"GET /admin/v1/provider-keys/{provider_key_id}/prices",
			"PUT /admin/v1/provider-keys/{provider_key_id}/prices",
		)
	}
	geminiRegistered := false
	for _, route := range routes {
		if strings.HasPrefix(route, "POST "+"/genai/") {
			if !geminiRegistered {
				// net/http patterns cannot embed a wildcard before a literal suffix in one segment.
				// Register one dispatch pattern; the protocol handler enforces the two exact operations.
				mux.Handle("POST "+"/genai/v1beta/models/{operation}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					operation := r.PathValue("operation")
					if model, generate := strings.CutSuffix(operation, ":generateContent"); generate && model != "" {
						s.milestone03NotImplemented(w, r)
						return
					}
					if model, stream := strings.CutSuffix(operation, ":streamGenerateContent"); stream && model != "" {
						s.milestone03NotImplemented(w, r)
						return
					}
					http.NotFound(w, r)
				}))
				geminiRegistered = true
			}
			continue
		}
		mux.Handle(route, http.HandlerFunc(s.milestone03NotImplemented))
	}
}

func (s Surface) String() string {
	if s == SurfaceGizPay {
		return "gizpay"
	}
	return "gizway"
}
