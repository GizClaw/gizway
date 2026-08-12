package gateway

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"

	"github.com/idy/gizway/internal/store"
)

func TestServiceConfigurationAndEarlyRejections(t *testing.T) {
	service := NewWithRealtimeProviderCallback(nil, nil, "", "")
	service.ConfigureRealtimeSessionTimeout(2 * time.Second)
	service.ConfigureRealtimeSessionTimeout(0)
	if service.realtimeSessionTimeout != 2*time.Second {
		t.Fatalf("Realtime timeout=%s", service.realtimeSessionTimeout)
	}
	if err := service.CheckQuota(context.Background(), CustomerCredential{RawAPIKey: "key"}); err == nil {
		t.Fatal("CheckQuota without a regional runtime succeeded")
	}
	if _, err := service.CreateRealtimeSession(context.Background(), CustomerCredential{}, RealtimeRequest{}); err == nil {
		t.Fatal("invalid Realtime request succeeded")
	}
	if _, err := service.Chat(context.Background(), CustomerCredential{}, ChatRequest{}); err == nil {
		t.Fatal("Chat without executor succeeded")
	}
	if err := service.StreamChat(context.Background(), CustomerCredential{}, ChatRequest{}, func([]byte) error { return nil }); err == nil {
		t.Fatal("StreamChat without executor succeeded")
	}
	if _, _, err := service.CompleteRealtimeProviderEvent(context.Background(), nil, ""); err == nil {
		t.Fatal("unsigned Realtime event succeeded")
	}
}

func TestCandidateResolutionAndChatMessageValidation(t *testing.T) {
	candidates := []store.GatewayCandidate{
		{VariantID: "variant-a", ProviderModel: "wire-a", ProviderEndpoint: "https://a.test", ProviderCredential: "secret-a"},
		{VariantID: "variant-b", ProviderModel: "wire-b", ProviderEndpoint: "https://b.test", ProviderCredential: "secret-b"},
	}
	targets := candidateTargets(candidates)
	if len(targets) != 2 || targets[0].RouteKey != "variant-a" || targets[1].Model != "wire-b" {
		t.Fatalf("candidate targets=%+v", targets)
	}
	resolved, err := resolvedCandidate(candidates, schemas.ModelProvider("gizway-variant-a"), "wire-a")
	if err != nil || resolved.VariantID != "variant-a" {
		t.Fatalf("private winner=%+v err=%v", resolved, err)
	}
	for name, resolve := range map[string]func() error{
		"private model mismatch": func() error {
			_, err := resolvedCandidate(candidates, schemas.ModelProvider("gizway-variant-a"), "attacker-model")
			return err
		},
		"missing metadata": func() error { _, err := resolvedCandidate(candidates, "", ""); return err },
		"unknown winner": func() error {
			_, err := resolvedCandidate(candidates, schemas.ModelProvider("openai"), "missing")
			return err
		},
		"ambiguous model": func() error {
			ambiguous := append(candidates, store.GatewayCandidate{VariantID: "variant-c", ProviderModel: "wire-b"})
			_, err := resolvedCandidate(ambiguous, schemas.ModelProvider("openai"), "wire-b")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := resolve(); err == nil {
				t.Fatal("invalid winner succeeded")
			}
		})
	}
	if winner, err := resolvedCandidate(candidates[:1], "", ""); err != nil || winner.VariantID != "variant-a" {
		t.Fatalf("single winner=%+v err=%v", winner, err)
	}
	if winner, err := resolvedCandidate(candidates, schemas.ModelProvider("openai"), "wire-b"); err != nil || winner.VariantID != "variant-b" {
		t.Fatalf("model winner=%+v err=%v", winner, err)
	}

	messages, err := providerMessages([]ChatMessage{{Role: "system", Content: "s"}, {Role: "user", Content: "u"}, {Role: "assistant", Content: "a"}})
	if err != nil || len(messages) != 3 {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	for _, role := range []string{"tool", "", "unknown"} {
		if _, err := providerMessages([]ChatMessage{{Role: role, Content: "x"}}); err == nil {
			t.Fatalf("unsupported role %q succeeded", role)
		}
	}
	for role, expected := range map[string]schemas.ChatMessageRole{
		"system": schemas.ChatMessageRoleSystem, "user": schemas.ChatMessageRoleUser, "assistant": schemas.ChatMessageRoleAssistant,
	} {
		if got, err := chatRole(role); err != nil || got != expected {
			t.Fatalf("chatRole(%q)=%s err=%v", role, got, err)
		}
	}
	if _, err := chatRole("tool"); err == nil {
		t.Fatal("unsupported tool role succeeded")
	}
}

func TestChatPricingValidationBoundaries(t *testing.T) {
	prices := testPrices()
	reserved, err := quotaCommitmentUpperBound(prices, 4096, 4096)
	if err != nil || reserved != 22_120 {
		t.Fatalf("reservation=%d err=%v", reserved, err)
	}
	for _, mutate := range []func(map[string]store.GatewayPrice){
		func(value map[string]store.GatewayPrice) { delete(value, "input_token") },
		func(value map[string]store.GatewayPrice) {
			value["input_token"] = store.GatewayPrice{Metric: "input_token", UnitSize: 0, EffectivePrice: 1}
		},
		func(value map[string]store.GatewayPrice) {
			value["input_token"] = store.GatewayPrice{Metric: "input_token", UnitSize: 1, EffectivePrice: math.MaxInt64}
		},
	} {
		invalid := testPrices()
		mutate(invalid)
		if _, err := quotaCommitmentUpperBound(invalid, 4096, 4096); err == nil {
			t.Fatal("invalid price set succeeded")
		}
	}
	missing := testPrices()
	delete(missing, "output_token")
	if _, err := quotaCommitmentUpperBound(missing, 1, 1); err == nil {
		t.Fatal("missing output price succeeded")
	}
	if _, err := tokenDistributionUpperBound(prices, 1, "missing"); err == nil {
		t.Fatal("missing distribution price succeeded")
	}
}
