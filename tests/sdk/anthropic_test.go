package sdk_test

import (
	"testing"

	"github.com/GizClaw/gizway/tests/sdk/internal/assertions"
	"github.com/GizClaw/gizway/tests/sdk/internal/fixture"
	"github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
)

func TestAnthropicOfficialSDKMessages(t *testing.T) {
	env := fixture.Load(t)
	if env.GlobalDSN == "" || env.PayDSN == "" || env.ProviderURL == "" || env.ProviderKeyID == "" {
		t.Skip("billing assertion environment is not configured")
	}
	before := assertions.CaptureBillingSnapshot(t, env.GlobalDSN, env.PayDSN, env.ProviderURL)
	client := anthropic.NewClient(anthropicoption.WithAPIKey(env.SubscriptionKey), anthropicoption.WithBaseURL(env.GlobalURL+"/anthropic"))
	message, err := client.Messages.New(t.Context(), anthropic.MessageNewParams{
		Model: anthropic.Model(env.GlobalModel), MaxTokens: 64,
		Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("return the m03-anthropic marker"))},
	})
	if err != nil {
		t.Fatalf("Messages.New: %v", err)
	}
	if len(message.Content) == 0 || message.Content[0].Text != "deterministic story response" {
		t.Fatalf("unexpected provider response: %+v", message.Content)
	}
	if message.Usage.InputTokens == 0 && message.Usage.OutputTokens == 0 {
		t.Fatal("Anthropic response lacks usage")
	}
	assertions.AssertBilledCallEventually(t, before, env.GlobalDSN, env.PayDSN, env.ProviderURL, env.ProviderKeyID, "non_streaming")
	if got := assertions.ProviderStats(t, env.ProviderURL)["last_max_tokens"]; got != float64(64) {
		t.Fatalf("Anthropic max_tokens did not reach Provider: %v", got)
	}
}

func TestAnthropicOfficialSDKStreaming(t *testing.T) {
	env := fixture.Load(t)
	if env.GlobalDSN == "" || env.PayDSN == "" || env.ProviderURL == "" || env.ProviderKeyID == "" {
		t.Skip("billing assertion environment is not configured")
	}
	before := assertions.CaptureBillingSnapshot(t, env.GlobalDSN, env.PayDSN, env.ProviderURL)
	client := anthropic.NewClient(anthropicoption.WithAPIKey(env.SubscriptionKey), anthropicoption.WithBaseURL(env.GlobalURL+"/anthropic"))
	stream := client.Messages.NewStreaming(t.Context(), anthropic.MessageNewParams{
		Model: anthropic.Model(env.GlobalModel), MaxTokens: 64,
		Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("stream the m03-anthropic marker"))},
	})
	message := anthropic.Message{}
	events := 0
	for stream.Next() {
		events++
		if err := message.Accumulate(stream.Current()); err != nil {
			t.Fatalf("accumulate stream: %v", err)
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("Anthropic stream: %v", err)
	}
	if events < 2 || len(message.Content) == 0 || message.Content[0].Text != "deterministic stream response" {
		t.Fatalf("Anthropic stream events=%d content=%+v", events, message.Content)
	}
	assertions.AssertBilledCallEventually(t, before, env.GlobalDSN, env.PayDSN, env.ProviderURL, env.ProviderKeyID, "streaming")
}

func TestAnthropicOfficialSDKRejectsInvalidKeyAndModel(t *testing.T) {
	env := fixture.Load(t)
	request := func(key, model string) error {
		client := anthropic.NewClient(anthropicoption.WithAPIKey(key), anthropicoption.WithBaseURL(env.GlobalURL+"/anthropic"), anthropicoption.WithMaxRetries(0))
		_, err := client.Messages.New(t.Context(), anthropic.MessageNewParams{
			Model: anthropic.Model(model), MaxTokens: 8,
			Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("must not reach provider"))},
		})
		return err
	}
	for name, tc := range map[string]struct{ key, model string }{
		"invalid key":   {"m03-invalid-subscription-key", env.GlobalModel},
		"missing model": {env.SubscriptionKey, "m03-missing-model"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := request(tc.key, tc.model); err == nil {
				t.Fatal("official SDK accepted a request that must fail")
			}
		})
	}
}

func TestAnthropicOfficialSDKFailureBillingBoundaries(t *testing.T) {
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
			client := anthropic.NewClient(anthropicoption.WithAPIKey(test.key), anthropicoption.WithBaseURL(env.GlobalURL+"/anthropic"), anthropicoption.WithMaxRetries(0))
			_, err := client.Messages.New(t.Context(), anthropic.MessageNewParams{
				Model: anthropic.Model(test.model), MaxTokens: 32,
				Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(test.prompt))},
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
