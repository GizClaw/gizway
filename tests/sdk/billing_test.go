package sdk_test

import (
	"testing"

	"github.com/GizClaw/gizway/tests/sdk/internal/assertions"
	"github.com/GizClaw/gizway/tests/sdk/internal/fixture"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

func TestAuthenticationModelAndProviderFailuresDoNotCharge(t *testing.T) {
	env := fixture.Load(t)
	if env.GlobalDSN == "" || env.PayDSN == "" || env.ProviderURL == "" || env.ProviderKeyID == "" || env.RevokedKey == "" || env.InactiveModel == "" {
		t.Skip("failure billing assertion environment is not configured")
	}
	for _, test := range []struct {
		name, key, model, prompt string
		providerDelta, logDelta  int64
	}{
		{name: "authentication", key: "m03-invalid-subscription-key", model: env.GlobalModel, prompt: "never reach provider"},
		{name: "revoked Key", key: env.RevokedKey, model: env.GlobalModel, prompt: "never reach provider"},
		{name: "missing Model", key: env.SubscriptionKey, model: "m03-missing-model", prompt: "never reach provider"},
		{name: "inactive Model", key: env.SubscriptionKey, model: env.InactiveModel, prompt: "never reach provider"},
		{name: "Provider failure", key: env.SubscriptionKey, model: env.GlobalModel, prompt: "provider-error", providerDelta: 1, logDelta: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := assertions.CaptureBillingSnapshot(t, env.GlobalDSN, env.PayDSN, env.ProviderURL)
			client := openai.NewClient(option.WithAPIKey(test.key), option.WithBaseURL(env.GlobalURL+"/openai/v1"), option.WithMaxRetries(0))
			_, err := client.Chat.Completions.New(t.Context(), openai.ChatCompletionNewParams{
				Model:    test.model,
				Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage(test.prompt)},
			})
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

func TestZeroPriceSDKCallCreatesNoFinancialRows(t *testing.T) {
	env := fixture.Load(t)
	if env.ZeroPriceModel == "" || env.GlobalDSN == "" || env.PayDSN == "" || env.ProviderURL == "" || env.ProviderKeyID == "" {
		t.Skip("zero-price assertion environment is not configured")
	}
	before := assertions.CaptureBillingSnapshot(t, env.GlobalDSN, env.PayDSN, env.ProviderURL)
	client := openai.NewClient(option.WithAPIKey(env.SubscriptionKey), option.WithBaseURL(env.GlobalURL+"/openai/v1"))
	response, err := client.Chat.Completions.New(t.Context(), openai.ChatCompletionNewParams{
		Model:    env.ZeroPriceModel,
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("return the m03-zero-price marker")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Choices) != 1 || response.Choices[0].Message.Content != "deterministic story response" {
		t.Fatalf("zero-price provider response was replaced: %+v", response.Choices)
	}
	assertions.AssertNoFinancialChange(t, before, env.GlobalDSN, env.PayDSN, env.ProviderURL, 1, 1)
	assertions.AssertLatestSuccessLog(t, env.GlobalDSN, env.ProviderKeyID, "non_streaming")
}
