package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"

	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
)

type protocolExecutor struct {
	responses      *schemas.BifrostResponsesResponse
	embedding      *schemas.BifrostEmbeddingResponse
	speech         *schemas.BifrostSpeechResponse
	transcription  *schemas.BifrostTranscriptionResponse
	image          *schemas.BifrostImageGenerationResponse
	stream         []*schemas.BifrostStreamChunk
	responsesCalls int
	streamCalls    int
}

func (e *protocolExecutor) Responses(context.Context, store.ProviderExecutionTarget, *schemas.BifrostResponsesRequest) (*schemas.BifrostResponsesResponse, error) {
	e.responsesCalls++
	return e.responses, nil
}

func (e *protocolExecutor) ResponsesCandidates(ctx context.Context, targets []store.ProviderExecutionTarget, request *schemas.BifrostResponsesRequest) (*schemas.BifrostResponsesResponse, error) {
	response, err := e.Responses(ctx, targets[0], request)
	if response != nil {
		response.ExtraFields.RoutingInfo = protocolRoutingInfo(targets[0])
	}
	return response, err
}

func (e *protocolExecutor) ResponsesStream(context.Context, store.ProviderExecutionTarget, *schemas.BifrostResponsesRequest) (<-chan *schemas.BifrostStreamChunk, context.CancelFunc, error) {
	e.streamCalls++
	channel := make(chan *schemas.BifrostStreamChunk, len(e.stream))
	for _, chunk := range e.stream {
		channel <- chunk
	}
	close(channel)
	return channel, func() {}, nil
}

func (e *protocolExecutor) ResponsesStreamCandidates(ctx context.Context, targets []store.ProviderExecutionTarget, request *schemas.BifrostResponsesRequest) (<-chan *schemas.BifrostStreamChunk, context.CancelFunc, error) {
	for _, chunk := range e.stream {
		if chunk != nil && chunk.BifrostResponsesStreamResponse != nil {
			chunk.BifrostResponsesStreamResponse.ExtraFields.RoutingInfo = protocolRoutingInfo(targets[0])
		}
	}
	return e.ResponsesStream(ctx, targets[0], request)
}

func (e *protocolExecutor) Embedding(context.Context, store.ProviderExecutionTarget, *schemas.BifrostEmbeddingRequest) (*schemas.BifrostEmbeddingResponse, error) {
	return e.embedding, nil
}

func (e *protocolExecutor) EmbeddingCandidates(ctx context.Context, targets []store.ProviderExecutionTarget, request *schemas.BifrostEmbeddingRequest) (*schemas.BifrostEmbeddingResponse, error) {
	response, err := e.Embedding(ctx, targets[0], request)
	markProtocolWinner(response, targets)
	return response, err
}

func (e *protocolExecutor) Speech(context.Context, store.ProviderExecutionTarget, *schemas.BifrostSpeechRequest) (*schemas.BifrostSpeechResponse, error) {
	return e.speech, nil
}

func (e *protocolExecutor) SpeechCandidates(ctx context.Context, targets []store.ProviderExecutionTarget, request *schemas.BifrostSpeechRequest) (*schemas.BifrostSpeechResponse, error) {
	response, err := e.Speech(ctx, targets[0], request)
	markProtocolWinner(response, targets)
	return response, err
}

func (e *protocolExecutor) Transcription(context.Context, store.ProviderExecutionTarget, *schemas.BifrostTranscriptionRequest) (*schemas.BifrostTranscriptionResponse, error) {
	return e.transcription, nil
}

func (e *protocolExecutor) TranscriptionCandidates(ctx context.Context, targets []store.ProviderExecutionTarget, request *schemas.BifrostTranscriptionRequest) (*schemas.BifrostTranscriptionResponse, error) {
	response, err := e.Transcription(ctx, targets[0], request)
	markProtocolWinner(response, targets)
	return response, err
}

func (e *protocolExecutor) ImageGeneration(context.Context, store.ProviderExecutionTarget, *schemas.BifrostImageGenerationRequest) (*schemas.BifrostImageGenerationResponse, error) {
	return e.image, nil
}

func (e *protocolExecutor) ImageGenerationCandidates(ctx context.Context, targets []store.ProviderExecutionTarget, request *schemas.BifrostImageGenerationRequest) (*schemas.BifrostImageGenerationResponse, error) {
	response, err := e.ImageGeneration(ctx, targets[0], request)
	markProtocolWinner(response, targets)
	return response, err
}

func markProtocolWinner(response any, targets []store.ProviderExecutionTarget) {
	if len(targets) == 0 {
		return
	}
	routing := protocolRoutingInfo(targets[0])
	switch value := response.(type) {
	case *schemas.BifrostEmbeddingResponse:
		if value != nil {
			value.ExtraFields.RoutingInfo = routing
		}
	case *schemas.BifrostSpeechResponse:
		if value != nil {
			value.ExtraFields.RoutingInfo = routing
		}
	case *schemas.BifrostTranscriptionResponse:
		if value != nil {
			value.ExtraFields.RoutingInfo = routing
		}
	case *schemas.BifrostImageGenerationResponse:
		if value != nil {
			value.ExtraFields.RoutingInfo = routing
		}
	}
}

func protocolRoutingInfo(target store.ProviderExecutionTarget) schemas.RoutingInfo {
	return schemas.RoutingInfo{Provider: schemas.ModelProvider("gizway-" + target.RouteKey), Model: target.Model}
}

func TestResponsesMaxOutputDistinguishesOmittedFromInvalid(t *testing.T) {
	if got := responsesMaxOutput(nil); got != 0 {
		t.Fatalf("omitted max output = %d", got)
	}
	zero := 0
	if got := responsesMaxOutput(&schemas.BifrostResponsesRequest{Params: &schemas.ResponsesParameters{MaxOutputTokens: &zero}}); got >= 0 {
		t.Fatalf("explicit zero max output = %d", got)
	}
}

func (*protocolExecutor) ChatCompletion(context.Context, store.ProviderExecutionTarget, []schemas.ChatMessage, *schemas.ChatParameters) (*schemas.BifrostChatResponse, error) {
	return nil, errors.New("unused")
}

func (*protocolExecutor) ChatCompletionCandidates(context.Context, []store.ProviderExecutionTarget, []schemas.ChatMessage, *schemas.ChatParameters) (*schemas.BifrostChatResponse, error) {
	return nil, errors.New("unused")
}

func (*protocolExecutor) ChatCompletionStream(context.Context, store.ProviderExecutionTarget, []schemas.ChatMessage, *schemas.ChatParameters) (<-chan *schemas.BifrostStreamChunk, context.CancelFunc, error) {
	return nil, nil, errors.New("unused")
}

func (e *protocolExecutor) ChatCompletionStreamCandidates(ctx context.Context, targets []store.ProviderExecutionTarget, messages []schemas.ChatMessage, params *schemas.ChatParameters) (<-chan *schemas.BifrostStreamChunk, context.CancelFunc, error) {
	return e.ChatCompletionStream(ctx, targets[0], messages, params)
}

func (*protocolExecutor) RealtimeWebSocketRoute(context.Context, store.ProviderExecutionTarget) (string, http.Header, error) {
	return "", nil, errors.New("unused")
}

func (*protocolExecutor) RealtimeClientEvent(context.Context, store.ProviderExecutionTarget, []byte) ([]byte, error) {
	return nil, errors.New("unused")
}

func (*protocolExecutor) RealtimeProviderEvent(context.Context, store.ProviderExecutionTarget, []byte) ([]byte, *schemas.BifrostLLMUsage, bool, error) {
	return nil, nil, false, errors.New("unused")
}

func (*protocolExecutor) ExchangeRealtimeWebRTCSDP(context.Context, store.ProviderExecutionTarget, string, json.RawMessage) (string, error) {
	return "", errors.New("unused")
}

func protocolTestService(t *testing.T, executor *protocolExecutor) (*Service, store.GatewayPrincipal) {
	t.Helper()
	database := testdb.OpenStory(t)
	t.Cleanup(func() { _ = database.Close() })
	repository := store.New(database.SQL)
	hash := sha256.Sum256([]byte("giz_story_user_active_1"))
	principal, err := repository.AuthenticateGatewayKey(t.Context(), hash[:], "2026-08-10T01:00:00.000000000Z")
	if err != nil {
		t.Fatal(err)
	}
	return New(repository, executor), principal
}

func protocolUsage() *schemas.ResponsesResponseUsage {
	return &schemas.ResponsesResponseUsage{
		InputTokens: 11, OutputTokens: 6, TotalTokens: 17,
		InputTokensDetails: &schemas.ResponsesResponseInputTokens{CachedReadTokens: 2},
	}
}

func assertNoActiveGatewayWork(t *testing.T, service *Service) {
	t.Helper()
	activeReservations, startedRequests, err := service.store.GatewayActiveWorkCounts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if activeReservations != 0 || startedRequests != 0 {
		t.Fatalf("active reservations=%d started requests=%d", activeReservations, startedRequests)
	}
}

func TestProtocolOperationsSettleAndReplay(t *testing.T) {
	id := "provider-response"
	seconds := 2.4
	executor := &protocolExecutor{
		responses:     &schemas.BifrostResponsesResponse{ID: &id, Model: "private-model", Usage: protocolUsage()},
		embedding:     &schemas.BifrostEmbeddingResponse{Model: "private-model", Usage: &schemas.BifrostLLMUsage{PromptTokens: 8, TotalTokens: 8}},
		speech:        &schemas.BifrostSpeechResponse{Audio: []byte("audio")},
		transcription: &schemas.BifrostTranscriptionResponse{Text: "words", Usage: &schemas.TranscriptionUsage{Type: "duration", Seconds: &seconds}},
		image:         &schemas.BifrostImageGenerationResponse{ID: "image-id", Data: []schemas.ImageData{{B64JSON: "image"}}, Usage: &schemas.ImageUsage{InputTokens: 4, OutputTokens: 3}},
	}
	service, principal := protocolTestService(t, executor)
	render := func(_ context.Context, response *schemas.BifrostResponsesResponse, _ string) ([]byte, error) {
		return json.Marshal(response)
	}
	request := &schemas.BifrostResponsesRequest{}
	first, err := service.ExecuteResponses(t.Context(), principal, "responses", "responses-key", "story-text", map[string]any{"input": "x"}, request, render)
	if err != nil || !json.Valid(first) {
		t.Fatalf("ExecuteResponses = %s, %v", first, err)
	}
	replayed, err := service.ExecuteResponses(t.Context(), principal, "responses", "responses-key", "story-text", map[string]any{"input": "x"}, request, render)
	if err != nil || string(replayed) != string(first) || executor.responsesCalls != 1 {
		t.Fatalf("Responses replay = %s, calls=%d, err=%v", replayed, executor.responsesCalls, err)
	}
	if _, err := service.ExecuteEmbedding(t.Context(), principal, "embedding-key", "story-text", "embed", &schemas.BifrostEmbeddingRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ExecuteSpeech(t.Context(), principal, "speech-key", "story-text", "speech", &schemas.BifrostSpeechRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ExecuteTranscription(t.Context(), principal, "transcription-key", "story-text", "audio", &schemas.BifrostTranscriptionRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ExecuteImageGeneration(t.Context(), principal, "image-key", "story-text", "image", &schemas.BifrostImageGenerationRequest{}); err != nil {
		t.Fatal(err)
	}
}

func TestProtocolStreamSettlesAndReplaysFrames(t *testing.T) {
	id := "stream-response"
	completed := &schemas.BifrostResponsesStreamResponse{
		Type:     schemas.ResponsesStreamResponseTypeCompleted,
		Response: &schemas.BifrostResponsesResponse{ID: &id, Model: "private-model", Usage: protocolUsage()},
	}
	executor := &protocolExecutor{stream: []*schemas.BifrostStreamChunk{{BifrostResponsesStreamResponse: completed}}}
	service, principal := protocolTestService(t, executor)
	render := func(_ context.Context, response *schemas.BifrostResponsesStreamResponse, _ string) ([][]byte, error) {
		encoded, err := json.Marshal(response)
		return [][]byte{encoded}, err
	}
	var first [][]byte
	err := service.ExecuteResponsesStream(t.Context(), principal, "responses", "stream-key", "story-text", "stream", &schemas.BifrostResponsesRequest{}, render, func(frame []byte) error {
		first = append(first, append([]byte(nil), frame...))
		return nil
	})
	if err != nil || len(first) != 1 {
		t.Fatalf("ExecuteResponsesStream frames=%d err=%v", len(first), err)
	}
	var replay [][]byte
	err = service.ExecuteResponsesStream(t.Context(), principal, "responses", "stream-key", "story-text", "stream", &schemas.BifrostResponsesRequest{}, render, func(frame []byte) error {
		replay = append(replay, append([]byte(nil), frame...))
		return nil
	})
	if err != nil || len(replay) != 1 || string(replay[0]) != string(first[0]) || executor.streamCalls != 1 {
		t.Fatalf("stream replay=%q calls=%d err=%v", replay, executor.streamCalls, err)
	}
}

func TestProtocolUsageAndPricingFailures(t *testing.T) {
	prices := map[string]store.GatewayPrice{
		"input_token": {ID: "input", Metric: "input_token", UnitSize: 1000, EffectivePrice: 1800},
		"request":     {ID: "request", Metric: "request", UnitSize: 1, EffectivePrice: 9},
	}
	metrics, err := pricedQuantities(prices, map[string]int64{"input_token": 8, "request": 1, "zero": 0})
	if err != nil || len(metrics) != 2 || metrics[0].Metric != "input_token" || metrics[1].Metric != "request" {
		t.Fatalf("pricedQuantities = %+v, %v", metrics, err)
	}
	for name, quantities := range map[string]map[string]int64{
		"negative": {"input_token": -1},
		"missing":  {"output_token": 1},
		"empty":    {"input_token": 0},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := pricedQuantities(prices, quantities); err == nil {
				t.Fatal("invalid quantities succeeded")
			}
		})
	}
	candidate := store.GatewayCandidate{ContextWindow: 4096, MaxOutputTokens: 4096, Prices: map[string]store.GatewayPrice{"unknown": {UnitSize: 1, EffectivePrice: 1}}}
	if _, err := protocolReservationUpperBound("responses", candidate, 0); err == nil {
		t.Fatal("unsupported reservation metric succeeded")
	}
	overflow := map[string]store.GatewayPrice{"request": {UnitSize: 1, EffectivePrice: 1}, "input_token": {UnitSize: 1, EffectivePrice: ^int64(0)}}
	candidate.Prices = overflow
	if _, err := protocolReservationUpperBound("responses", candidate, 0); err == nil {
		t.Fatal("overflow reservation succeeded")
	}
}

func TestProtocolReservationAllowsCompleteZeroPriceSet(t *testing.T) {
	prices := testPrices()
	for metric, price := range prices {
		price.EffectivePrice = 0
		price.DiscountBPS = 10_000
		prices[metric] = price
	}
	candidate := store.GatewayCandidate{ContextWindow: 4096, MaxOutputTokens: 4096, Prices: prices}
	if reserved, err := protocolReservationUpperBound("responses", candidate, 0); err != nil || reserved != 0 {
		t.Fatalf("zero-price protocol reservation = %d, %v", reserved, err)
	}
}

func TestMarshalPublicJSONRemovesNestedBifrostMetadata(t *testing.T) {
	public, err := MarshalPublicJSON(map[string]any{
		"model": "story-text", "extra_fields": map[string]any{"resolved_model_used": "private"},
		"items": []any{map[string]any{"text": "ok", "extra_fields": map[string]any{"provider": "private"}}},
		"large": int64(9_007_199_254_740_993),
	})
	if err != nil || string(public) != `{"items":[{"text":"ok"}],"large":9007199254740993,"model":"story-text"}` {
		t.Fatalf("MarshalPublicJSON=%s err=%v", public, err)
	}
	if _, err := MarshalPublicJSON(make(chan int)); err == nil {
		t.Fatal("MarshalPublicJSON accepted an unsupported value")
	}
}

func TestProtocolProviderResponsesFailClosed(t *testing.T) {
	t.Run("responses missing usage", func(t *testing.T) {
		executor := &protocolExecutor{responses: &schemas.BifrostResponsesResponse{}}
		service, principal := protocolTestService(t, executor)
		_, err := service.ExecuteResponses(t.Context(), principal, "responses", "key", "story-text", "x", &schemas.BifrostResponsesRequest{}, func(context.Context, *schemas.BifrostResponsesResponse, string) ([]byte, error) {
			return []byte(`{}`), nil
		})
		if err == nil {
			t.Fatal("missing Responses usage succeeded")
		}
	})
	t.Run("responses invalid cache", func(t *testing.T) {
		usage := protocolUsage()
		usage.InputTokensDetails.CachedReadTokens = 12
		executor := &protocolExecutor{responses: &schemas.BifrostResponsesResponse{Usage: usage}}
		service, principal := protocolTestService(t, executor)
		_, err := service.ExecuteResponses(t.Context(), principal, "responses", "key", "story-text", "x", &schemas.BifrostResponsesRequest{}, func(context.Context, *schemas.BifrostResponsesResponse, string) ([]byte, error) {
			return []byte(`{}`), nil
		})
		if err == nil {
			t.Fatal("cached input above total succeeded")
		}
	})
	t.Run("responses invalid public JSON", func(t *testing.T) {
		executor := &protocolExecutor{responses: &schemas.BifrostResponsesResponse{Usage: protocolUsage()}}
		service, principal := protocolTestService(t, executor)
		_, err := service.ExecuteResponses(t.Context(), principal, "responses", "key", "story-text", "x", &schemas.BifrostResponsesRequest{}, func(context.Context, *schemas.BifrostResponsesResponse, string) ([]byte, error) {
			return []byte(`not-json`), nil
		})
		if err == nil {
			t.Fatal("invalid public JSON succeeded")
		}
	})
	t.Run("operation shapes", func(t *testing.T) {
		executor := &protocolExecutor{
			embedding:     &schemas.BifrostEmbeddingResponse{},
			speech:        &schemas.BifrostSpeechResponse{},
			transcription: &schemas.BifrostTranscriptionResponse{},
			image:         &schemas.BifrostImageGenerationResponse{},
		}
		service, principal := protocolTestService(t, executor)
		if _, err := service.ExecuteEmbedding(t.Context(), principal, "embedding", "story-text", "x", &schemas.BifrostEmbeddingRequest{}); err == nil {
			t.Fatal("embedding without usage succeeded")
		}
		if _, err := service.ExecuteSpeech(t.Context(), principal, "speech", "story-text", "x", &schemas.BifrostSpeechRequest{}); err == nil {
			t.Fatal("speech without audio succeeded")
		}
		if _, err := service.ExecuteTranscription(t.Context(), principal, "transcription", "story-text", "x", &schemas.BifrostTranscriptionRequest{}); err == nil {
			t.Fatal("transcription without usage succeeded")
		}
		if _, err := service.ExecuteImageGeneration(t.Context(), principal, "image", "story-text", "x", &schemas.BifrostImageGenerationRequest{}); err == nil {
			t.Fatal("image without result succeeded")
		}
	})
	t.Run("embedding invalid cache", func(t *testing.T) {
		executor := &protocolExecutor{embedding: &schemas.BifrostEmbeddingResponse{Usage: &schemas.BifrostLLMUsage{
			PromptTokens: 2, PromptTokensDetails: &schemas.ChatPromptTokensDetails{CachedReadTokens: 3}, TotalTokens: 2,
		}}}
		service, principal := protocolTestService(t, executor)
		if _, err := service.ExecuteEmbedding(t.Context(), principal, "embedding-invalid-cache", "story-text", "x", &schemas.BifrostEmbeddingRequest{}); err == nil {
			t.Fatal("embedding cached input above total succeeded")
		}
		assertNoActiveGatewayWork(t, service)
	})
	for name, duration := range map[string]float64{"negative": -1, "nan": math.NaN(), "infinite": math.Inf(1)} {
		t.Run("transcription "+name, func(t *testing.T) {
			executor := &protocolExecutor{transcription: &schemas.BifrostTranscriptionResponse{Usage: &schemas.TranscriptionUsage{Seconds: &duration}}}
			service, principal := protocolTestService(t, executor)
			if _, err := service.ExecuteTranscription(t.Context(), principal, "key", "story-text", "x", &schemas.BifrostTranscriptionRequest{}); err == nil {
				t.Fatal("invalid duration succeeded")
			}
		})
	}
}

func TestProtocolStreamFailuresReleaseReservation(t *testing.T) {
	t.Run("missing terminal usage", func(t *testing.T) {
		executor := &protocolExecutor{stream: []*schemas.BifrostStreamChunk{{BifrostResponsesStreamResponse: &schemas.BifrostResponsesStreamResponse{Type: schemas.ResponsesStreamResponseTypeCreated}}}}
		service, principal := protocolTestService(t, executor)
		err := service.ExecuteResponsesStream(t.Context(), principal, "responses", "key", "story-text", "x", &schemas.BifrostResponsesRequest{}, func(context.Context, *schemas.BifrostResponsesStreamResponse, string) ([][]byte, error) {
			return [][]byte{[]byte(`{}`)}, nil
		}, func([]byte) error { return nil })
		if err == nil {
			t.Fatal("stream without usage succeeded")
		}
		assertNoActiveGatewayWork(t, service)
	})
	t.Run("render failure", func(t *testing.T) {
		executor := &protocolExecutor{stream: []*schemas.BifrostStreamChunk{{BifrostResponsesStreamResponse: &schemas.BifrostResponsesStreamResponse{Type: schemas.ResponsesStreamResponseTypeCreated}}}}
		service, principal := protocolTestService(t, executor)
		err := service.ExecuteResponsesStream(t.Context(), principal, "responses", "key", "story-text", "x", &schemas.BifrostResponsesRequest{}, func(context.Context, *schemas.BifrostResponsesStreamResponse, string) ([][]byte, error) {
			return nil, errors.New("codec")
		}, func([]byte) error { return nil })
		if err == nil {
			t.Fatal("stream render failure succeeded")
		}
		assertNoActiveGatewayWork(t, service)
	})
	t.Run("client disconnect", func(t *testing.T) {
		executor := &protocolExecutor{stream: []*schemas.BifrostStreamChunk{{BifrostResponsesStreamResponse: &schemas.BifrostResponsesStreamResponse{Type: schemas.ResponsesStreamResponseTypeCreated}}}}
		service, principal := protocolTestService(t, executor)
		err := service.ExecuteResponsesStream(t.Context(), principal, "responses", "key", "story-text", "x", &schemas.BifrostResponsesRequest{}, func(context.Context, *schemas.BifrostResponsesStreamResponse, string) ([][]byte, error) {
			return [][]byte{[]byte(`{}`)}, nil
		}, func([]byte) error { return errors.New("disconnect") })
		if err == nil {
			t.Fatal("stream disconnect succeeded")
		}
		assertNoActiveGatewayWork(t, service)
	})
}
