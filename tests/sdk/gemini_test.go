package sdk_test

import (
	"strings"
	"testing"

	"github.com/GizClaw/gizway/tests/sdk/internal/assertions"
	"github.com/GizClaw/gizway/tests/sdk/internal/fixture"
	"google.golang.org/genai"
)

func geminiClient(t *testing.T, baseURL, key string) *genai.Client {
	t.Helper()
	client, err := genai.NewClient(t.Context(), &genai.ClientConfig{
		APIKey: key, Backend: genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{BaseURL: baseURL + "/genai", APIVersion: "v1beta"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestGeminiOfficialSDKGenerateContent(t *testing.T) {
	env := fixture.Load(t)
	if env.GlobalDSN == "" || env.PayDSN == "" || env.ProviderURL == "" || env.ProviderKeyID == "" {
		t.Skip("billing assertion environment is not configured")
	}
	before := assertions.CaptureBillingSnapshot(t, env.GlobalDSN, env.PayDSN, env.ProviderURL)
	response, err := geminiClient(t, env.GlobalURL, env.SubscriptionKey).Models.GenerateContent(
		t.Context(), env.GlobalModel, genai.Text("return the m03-gemini marker"), &genai.GenerateContentConfig{MaxOutputTokens: 41},
	)
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}
	if response.Text() != "deterministic story response" {
		t.Fatalf("unexpected provider response: %q", response.Text())
	}
	if response.UsageMetadata == nil {
		t.Fatal("Gemini response lacks usage metadata")
	}
	assertions.AssertBilledCallEventually(t, before, env.GlobalDSN, env.PayDSN, env.ProviderURL, env.ProviderKeyID, "non_streaming")
	if got := assertions.ProviderStats(t, env.ProviderURL)["last_max_tokens"]; got != float64(41) {
		t.Fatalf("Gemini maxOutputTokens did not reach Provider: %v", got)
	}
}

func TestGeminiOfficialSDKStreaming(t *testing.T) {
	env := fixture.Load(t)
	if env.GlobalDSN == "" || env.PayDSN == "" || env.ProviderURL == "" || env.ProviderKeyID == "" {
		t.Skip("billing assertion environment is not configured")
	}
	before := assertions.CaptureBillingSnapshot(t, env.GlobalDSN, env.PayDSN, env.ProviderURL)
	var text strings.Builder
	chunks := 0
	for response, err := range geminiClient(t, env.GlobalURL, env.SubscriptionKey).Models.GenerateContentStream(
		t.Context(), env.GlobalModel, genai.Text("stream the m03-gemini marker"), nil,
	) {
		if err != nil {
			t.Fatalf("GenerateContentStream: %v", err)
		}
		chunks++
		text.WriteString(response.Text())
	}
	if chunks < 2 || text.String() != "deterministic stream response" {
		t.Fatalf("Gemini stream chunks=%d text=%q", chunks, text.String())
	}
	assertions.AssertBilledCallEventually(t, before, env.GlobalDSN, env.PayDSN, env.ProviderURL, env.ProviderKeyID, "streaming")
}

func TestGeminiOfficialSDKFailureBillingBoundaries(t *testing.T) {
	env := fixture.Load(t)
	if env.GlobalDSN == "" || env.PayDSN == "" || env.ProviderURL == "" || env.ProviderKeyID == "" || env.RevokedKey == "" || env.InactiveModel == "" {
		t.Skip("failure billing assertion environment is not configured")
	}
	for _, test := range []struct {
		name, key, model, prompt string
		providerDelta, logDelta  int64
	}{
		{name: "revoked Key", key: env.RevokedKey, model: env.GlobalModel, prompt: "must not reach Provider"},
		{name: "inactive Model", key: env.SubscriptionKey, model: env.InactiveModel, prompt: "must not reach Provider"},
		{name: "Provider failure", key: env.SubscriptionKey, model: env.GlobalModel, prompt: "provider-error", providerDelta: 1, logDelta: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := assertions.CaptureBillingSnapshot(t, env.GlobalDSN, env.PayDSN, env.ProviderURL)
			_, err := geminiClient(t, env.GlobalURL, test.key).Models.GenerateContent(
				t.Context(), test.model, genai.Text(test.prompt), nil,
			)
			if err == nil {
				t.Fatal("request unexpectedly succeeded")
			}
			assertions.AssertNoFinancialChange(t, before, env.GlobalDSN, env.PayDSN, env.ProviderURL, test.providerDelta, test.logDelta)
			if test.logDelta != 0 {
				assertions.AssertLatestErrorLog(t, env.GlobalDSN, env.ProviderKeyID, "non_streaming")
			}
		})
	}
}

func TestGeminiOfficialSDKRejectsInvalidKeyAndModel(t *testing.T) {
	env := fixture.Load(t)
	for name, tc := range map[string]struct{ key, model string }{
		"invalid key":   {"m03-invalid-subscription-key", env.GlobalModel},
		"missing model": {env.SubscriptionKey, "m03-missing-model"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := geminiClient(t, env.GlobalURL, tc.key).Models.GenerateContent(
				t.Context(), tc.model, genai.Text("must not reach provider"), nil,
			)
			if err == nil {
				t.Fatal("official SDK accepted a request that must fail")
			}
		})
	}
}
