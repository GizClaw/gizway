package sdk_test

import (
	"strings"
	"testing"

	"github.com/idy/gizway/tests/sdk/internal/assertions"
	"github.com/idy/gizway/tests/sdk/internal/fixture"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
)

func TestOpenAIOfficialSDKModelsAndChat(t *testing.T) {
	env := fixture.Load(t)
	if env.GlobalDSN == "" || env.PayDSN == "" || env.ProviderURL == "" || env.ProviderKeyID == "" {
		t.Skip("billing assertion environment is not configured")
	}
	client := openai.NewClient(option.WithAPIKey(env.SubscriptionKey), option.WithBaseURL(env.GlobalURL+"/openai/v1"))
	models, err := client.Models.List(t.Context())
	if err != nil {
		t.Fatalf("Models.List: %v", err)
	}
	found := false
	for _, model := range models.Data {
		found = found || model.ID == env.GlobalModel
	}
	if !found {
		t.Fatalf("Models.List lacks %q", env.GlobalModel)
	}
	before := assertions.CaptureBillingSnapshot(t, env.GlobalDSN, env.PayDSN, env.ProviderURL)
	response, err := client.Chat.Completions.New(t.Context(), openai.ChatCompletionNewParams{
		Model:     env.GlobalModel,
		MaxTokens: param.NewOpt(int64(37)),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("return the m03-openai marker"),
		},
	})
	if err != nil {
		t.Fatalf("Chat.Completions.New: %v", err)
	}
	if len(response.Choices) != 1 || response.Choices[0].Message.Content != "deterministic story response" {
		t.Fatalf("unexpected provider response: %+v", response.Choices)
	}
	if response.Usage.TotalTokens == 0 {
		t.Fatal("OpenAI response lacks usage")
	}
	assertions.AssertBilledCallEventually(t, before, env.GlobalDSN, env.PayDSN, env.ProviderURL, env.ProviderKeyID, "non_streaming")
	if got := assertions.ProviderStats(t, env.ProviderURL)["last_max_tokens"]; got != float64(37) {
		t.Fatalf("OpenAI max_tokens did not reach Provider: %v", got)
	}
}

func TestOpenAIOfficialSDKStreaming(t *testing.T) {
	env := fixture.Load(t)
	if env.GlobalDSN == "" || env.PayDSN == "" || env.ProviderURL == "" || env.ProviderKeyID == "" {
		t.Skip("billing assertion environment is not configured")
	}
	client := openai.NewClient(option.WithAPIKey(env.SubscriptionKey), option.WithBaseURL(env.GlobalURL+"/openai/v1"))
	before := assertions.CaptureBillingSnapshot(t, env.GlobalDSN, env.PayDSN, env.ProviderURL)
	stream := client.Chat.Completions.NewStreaming(t.Context(), openai.ChatCompletionNewParams{
		Model:    env.GlobalModel,
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("stream the m03-openai marker")},
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: param.NewOpt(true), IncludeObfuscation: param.NewOpt(true),
		},
	})
	var text strings.Builder
	chunks := 0
	for stream.Next() {
		chunk := stream.Current()
		chunks++
		if len(chunk.Choices) > 0 {
			text.WriteString(chunk.Choices[0].Delta.Content)
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("OpenAI stream: %v", err)
	}
	if chunks < 2 || text.String() != "deterministic stream response" {
		t.Fatalf("OpenAI stream chunks=%d text=%q", chunks, text.String())
	}
	assertions.AssertBilledCallEventually(t, before, env.GlobalDSN, env.PayDSN, env.ProviderURL, env.ProviderKeyID, "streaming")
	stats := assertions.ProviderStats(t, env.ProviderURL)
	if stats["last_stream_include_usage"] != float64(1) || stats["last_stream_include_obfuscation"] != float64(1) {
		t.Fatalf("OpenAI stream_options did not reach Provider: %v", stats)
	}
}

func TestOpenAIOfficialSDKRejectsInvalidKeyAndModel(t *testing.T) {
	env := fixture.Load(t)
	request := func(key, model string) error {
		client := openai.NewClient(option.WithAPIKey(key), option.WithBaseURL(env.GlobalURL+"/openai/v1"))
		_, err := client.Chat.Completions.New(t.Context(), openai.ChatCompletionNewParams{
			Model:    model,
			Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("must not reach provider")},
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

func TestOpenAIOfficialSDKCNRegionSmoke(t *testing.T) {
	env := fixture.Load(t)
	if env.CNURL == "" || env.CNModel == "" {
		t.Skip("CN SDK E2E environment is not configured")
	}
	client := openai.NewClient(option.WithAPIKey(env.SubscriptionKey), option.WithBaseURL(env.CNURL+"/openai/v1"))
	response, err := client.Chat.Completions.New(t.Context(), openai.ChatCompletionNewParams{
		Model:    env.CNModel,
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("return the m03-cn marker")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Choices) != 1 || response.Choices[0].Message.Content != "deterministic story response" {
		t.Fatalf("unexpected CN provider response: %+v", response.Choices)
	}
}
