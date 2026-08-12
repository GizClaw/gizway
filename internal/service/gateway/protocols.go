package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/maximhq/bifrost/core/schemas"

	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/timetext"
)

// ProtocolRenderer converts Bifrost's canonical Responses result back to the
// exact public protocol selected by the route (OpenAI, Anthropic, or Gemini).
// The rendered bytes are returned directly to the current caller; Refactor 01
// has no persisted response replay path.
type ProtocolRenderer func(context.Context, *schemas.BifrostResponsesResponse, string) ([]byte, error)

// ProtocolStreamRenderer converts one canonical Responses event into zero or
// more fully framed public chunks. Zero is valid because Anthropic/Gemini
// codecs may buffer canonical bookkeeping events until a public event exists.
type ProtocolStreamRenderer func(context.Context, *schemas.BifrostResponsesStreamResponse, string) ([][]byte, error)

type protocolExecution struct {
	providerRequestID string
	quantities        map[string]int64
	response          []byte
	winner            store.GatewayCandidate
}

// executeProtocol owns the shared reserve -> provider -> measured settlement
// state machine for every non-Realtime compatible protocol operation.
func (s *Service) executeProtocol(ctx context.Context, principal CustomerCredential, operation, publicModel string, requestedMaxOutput int64, invoke func(context.Context, []store.GatewayCandidate) (protocolExecution, error)) ([]byte, error) {
	if s.executor == nil {
		return nil, errors.New("AI executor is not configured")
	}
	now := s.now().UTC()
	plan, err := s.beginGatewayExecution(ctx, principal, operation, publicModel, now, requestedMaxOutput, func(candidate store.GatewayCandidate) (int64, error) {
		return protocolCommitmentUpperBound(operation, candidate, requestedMaxOutput)
	})
	if err != nil {
		return nil, err
	}
	requestID := plan.requestID
	candidates := plan.candidates
	candidate := candidates[0]
	result, err := invoke(ctx, candidates)
	if err != nil {
		s.releaseGatewayCommand(ctx, requestID, "provider_error")
		return nil, err
	}
	if len(result.response) == 0 || !json.Valid(result.response) {
		s.releaseGatewayCommand(ctx, requestID, "response_error")
		return nil, errors.New("provider-compatible response is not valid JSON")
	}
	if result.winner.VariantID == "" {
		result.winner = candidate
	}
	if _, ok := result.winner.Prices["request"]; ok {
		result.quantities["request"] = 1
	}
	if err := validateCandidateUsage(operation, result.winner, result.quantities); err != nil {
		s.releaseGatewayCommand(ctx, requestID, "invalid_usage")
		return nil, err
	}
	metrics, err := pricedQuantities(result.winner.Prices, result.quantities)
	if err != nil {
		s.releaseGatewayCommand(ctx, requestID, "invalid_usage")
		return nil, err
	}
	if err := s.settleGatewayCommandForVariant(ctx, requestID, result.providerRequestID, result.winner.VariantID, metrics, timetext.Format(s.now())); err != nil {
		return nil, err
	}
	return result.response, nil
}

// ExecuteResponses handles OpenAI Responses plus Anthropic/Gemini codecs that
// translate through Bifrost's canonical Responses model.
func (s *Service) ExecuteResponses(ctx context.Context, principal CustomerCredential, operation, publicModel string, request *schemas.BifrostResponsesRequest, render ProtocolRenderer) ([]byte, error) {
	return s.executeProtocol(ctx, principal, operation, publicModel, responsesMaxOutput(request), func(providerContext context.Context, candidates []store.GatewayCandidate) (protocolExecution, error) {
		response, err := s.executor.ResponsesCandidates(providerContext, candidateTargets(candidates), request)
		if err != nil {
			return protocolExecution{}, err
		}
		if response == nil || response.Usage == nil {
			return protocolExecution{}, errors.New("provider response did not include usage")
		}
		resolvedModel := resolvedResponseModel(response.ExtraFields, response.Model)
		winner, err := resolvedCandidate(candidates, response.ExtraFields.RoutingInfo.Provider, resolvedModel)
		if err != nil {
			return protocolExecution{}, err
		}
		response.Model = winner.PublicModel
		public, err := render(ctx, response, winner.PublicModel)
		if err != nil {
			return protocolExecution{}, err
		}
		cached := int64(0)
		if response.Usage.InputTokensDetails != nil {
			cached = int64(response.Usage.InputTokensDetails.CachedReadTokens)
		}
		input := int64(response.Usage.InputTokens) - cached
		if input < 0 {
			return protocolExecution{}, errors.New("provider cached input exceeds total input")
		}
		providerID := ""
		if response.ID != nil {
			providerID = *response.ID
		}
		return protocolExecution{providerRequestID: providerID, quantities: map[string]int64{
			"input_token": input, "cached_input_token": cached, "output_token": int64(response.Usage.OutputTokens),
		}, response: public, winner: winner}, nil
	})
}

// ExecuteResponsesStream provides one shared reserve/stream/settle state
// machine for OpenAI Responses, Anthropic Messages and Gemini GenerateContent.
// It settles only the terminal provider usage after the stream closes
// successfully; emitted frames are not retained for replay.
func (s *Service) ExecuteResponsesStream(ctx context.Context, principal CustomerCredential, operation, publicModel string, request *schemas.BifrostResponsesRequest, render ProtocolStreamRenderer, emit func([]byte) error) error {
	now := s.now().UTC()
	plan, err := s.beginGatewayExecution(ctx, principal, operation, publicModel, now, responsesMaxOutput(request), func(candidate store.GatewayCandidate) (int64, error) {
		return protocolCommitmentUpperBound(operation, candidate, responsesMaxOutput(request))
	})
	if err != nil {
		return err
	}
	requestID := plan.requestID
	candidates := plan.candidates
	candidate := candidates[0]
	chunks, cancel, err := s.executor.ResponsesStreamCandidates(ctx, candidateTargets(candidates), request)
	if err != nil {
		s.releaseGatewayCommand(ctx, requestID, "provider_error")
		return err
	}
	defer cancel()
	var usage *schemas.ResponsesResponseUsage
	var clientWriteErr error
	providerRequestID := ""
	resolved := candidate
	for chunk := range chunks {
		if chunk == nil {
			continue
		}
		if chunk.BifrostError != nil {
			s.releaseGatewayCommand(ctx, requestID, "provider_error")
			return fmt.Errorf("provider responses stream: %s", chunk.BifrostError.Error.Message)
		}
		response := chunk.BifrostResponsesStreamResponse
		if response == nil {
			continue
		}
		if response.Response != nil {
			resolvedModel := resolvedResponseModel(response.ExtraFields, response.Response.Model)
			winner, winnerErr := resolvedCandidate(candidates, response.ExtraFields.RoutingInfo.Provider, resolvedModel)
			if winnerErr != nil {
				s.releaseGatewayCommand(ctx, requestID, "unapproved_fallback")
				return winnerErr
			}
			resolved = winner
			response.Response.Model = resolved.PublicModel
			if response.Response.ID != nil {
				providerRequestID = *response.Response.ID
			}
			if response.Response.Usage != nil {
				usage = response.Response.Usage
			}
		}
		frames, err := render(ctx, response, resolved.PublicModel)
		if err != nil {
			s.releaseGatewayCommand(ctx, requestID, "response_error")
			return err
		}
		for _, frame := range frames {
			if len(frame) == 0 {
				continue
			}
			if clientWriteErr == nil {
				if err := emit(frame); err != nil {
					// The terminal Responses event can carry trustworthy usage.
					// Settle that usage even if the downstream socket breaks while
					// writing the same event; only usage-less disconnects release.
					if usage == nil {
						s.releaseGatewayCommand(ctx, requestID, "client_disconnect")
						return err
					}
					clientWriteErr = err
				}
			}
		}
	}
	if usage == nil {
		s.releaseGatewayCommand(ctx, requestID, "invalid_usage")
		return errors.New("provider responses stream did not include terminal usage")
	}
	cached := int64(0)
	if usage.InputTokensDetails != nil {
		cached = int64(usage.InputTokensDetails.CachedReadTokens)
	}
	input := int64(usage.InputTokens) - cached
	if input < 0 {
		s.releaseGatewayCommand(ctx, requestID, "invalid_usage")
		return errors.New("provider cached input exceeds total input")
	}
	quantities := map[string]int64{"input_token": input, "cached_input_token": cached, "output_token": int64(usage.OutputTokens)}
	if _, ok := resolved.Prices["request"]; ok {
		quantities["request"] = 1
	}
	metrics, err := pricedQuantities(resolved.Prices, quantities)
	if err != nil {
		s.releaseGatewayCommand(ctx, requestID, "pricing_error")
		return err
	}
	if err := validateCandidateUsage(operation, resolved, quantities); err != nil {
		s.releaseGatewayCommand(ctx, requestID, "invalid_usage")
		return err
	}
	if err := s.settleGatewayCommandForVariant(ctx, requestID, providerRequestID, resolved.VariantID, metrics, timetext.Format(s.now())); err != nil {
		return err
	}
	if clientWriteErr != nil {
		return clientWriteErr
	}
	return nil
}

func (s *Service) ExecuteEmbedding(ctx context.Context, principal CustomerCredential, publicModel string, request *schemas.BifrostEmbeddingRequest) ([]byte, error) {
	return s.executeProtocol(ctx, principal, "embeddings", publicModel, 0, func(providerContext context.Context, candidates []store.GatewayCandidate) (protocolExecution, error) {
		response, err := s.executor.EmbeddingCandidates(providerContext, candidateTargets(candidates), request)
		if err != nil {
			return protocolExecution{}, err
		}
		if response == nil || response.Usage == nil {
			return protocolExecution{}, errors.New("provider embedding response did not include usage")
		}
		winner, err := resolvedCandidate(candidates, response.ExtraFields.RoutingInfo.Provider, resolvedResponseModel(response.ExtraFields, response.Model))
		if err != nil {
			return protocolExecution{}, err
		}
		quantities, err := llmUsageQuantities(response.Usage)
		if err != nil {
			return protocolExecution{}, err
		}
		response.Model = winner.PublicModel
		public, err := MarshalPublicJSON(response)
		return protocolExecution{quantities: quantities, response: public, winner: winner}, err
	})
}

func (s *Service) ExecuteSpeech(ctx context.Context, principal CustomerCredential, publicModel string, request *schemas.BifrostSpeechRequest) ([]byte, error) {
	return s.executeProtocol(ctx, principal, "audio.speech", publicModel, 0, func(providerContext context.Context, candidates []store.GatewayCandidate) (protocolExecution, error) {
		response, err := s.executor.SpeechCandidates(providerContext, candidateTargets(candidates), request)
		if err != nil {
			return protocolExecution{}, err
		}
		if response == nil || len(response.Audio) == 0 {
			return protocolExecution{}, errors.New("provider speech response omitted audio")
		}
		// OpenAI's speech endpoint returns audio bytes without a usage object.
		// Bifrost backfills character usage when possible, but request-count
		// pricing remains the provider-independent measured quantity. Keeping an
		// empty usage object lets that legitimate response settle without
		// inventing token or duration usage.
		if response.Usage == nil {
			response.Usage = &schemas.SpeechUsage{}
		}
		winner, err := resolvedCandidate(candidates, response.ExtraFields.RoutingInfo.Provider, resolvedResponseModel(response.ExtraFields, ""))
		if err != nil {
			return protocolExecution{}, err
		}
		public, err := MarshalPublicJSON(response)
		return protocolExecution{quantities: map[string]int64{
			"input_token": int64(response.Usage.InputTokens), "output_token": int64(response.Usage.OutputTokens),
			"audio_second": int64(response.Usage.AudioSeconds),
		}, response: public, winner: winner}, err
	})
}

func (s *Service) ExecuteTranscription(ctx context.Context, principal CustomerCredential, publicModel string, request *schemas.BifrostTranscriptionRequest) ([]byte, error) {
	return s.executeProtocol(ctx, principal, "audio.transcriptions", publicModel, 0, func(providerContext context.Context, candidates []store.GatewayCandidate) (protocolExecution, error) {
		response, err := s.executor.TranscriptionCandidates(providerContext, candidateTargets(candidates), request)
		if err != nil {
			return protocolExecution{}, err
		}
		if response == nil || response.Usage == nil {
			return protocolExecution{}, errors.New("provider transcription response omitted usage")
		}
		winner, err := resolvedCandidate(candidates, response.ExtraFields.RoutingInfo.Provider, resolvedResponseModel(response.ExtraFields, ""))
		if err != nil {
			return protocolExecution{}, err
		}
		quantities := map[string]int64{}
		if response.Usage.InputTokens != nil {
			quantities["input_token"] = int64(*response.Usage.InputTokens)
		}
		if response.Usage.OutputTokens != nil {
			quantities["output_token"] = int64(*response.Usage.OutputTokens)
		}
		if response.Usage.Seconds != nil {
			if math.IsNaN(*response.Usage.Seconds) || math.IsInf(*response.Usage.Seconds, 0) || *response.Usage.Seconds < 0 {
				return protocolExecution{}, errors.New("provider transcription duration is invalid")
			}
			quantities["audio_second"] = int64(math.Ceil(*response.Usage.Seconds))
		}
		public, err := MarshalPublicJSON(response)
		return protocolExecution{quantities: quantities, response: public, winner: winner}, err
	})
}

func (s *Service) ExecuteImageGeneration(ctx context.Context, principal CustomerCredential, publicModel string, request *schemas.BifrostImageGenerationRequest) ([]byte, error) {
	return s.executeProtocol(ctx, principal, "images.generations", publicModel, 0, func(providerContext context.Context, candidates []store.GatewayCandidate) (protocolExecution, error) {
		response, err := s.executor.ImageGenerationCandidates(providerContext, candidateTargets(candidates), request)
		if err != nil {
			return protocolExecution{}, err
		}
		if response == nil || response.Usage == nil || len(response.Data) == 0 {
			return protocolExecution{}, errors.New("provider image response omitted result or usage")
		}
		winner, err := resolvedCandidate(candidates, response.ExtraFields.RoutingInfo.Provider, resolvedResponseModel(response.ExtraFields, response.Model))
		if err != nil {
			return protocolExecution{}, err
		}
		response.Model = winner.PublicModel
		quantities := map[string]int64{"image": int64(len(response.Data)), "input_token": int64(response.Usage.InputTokens), "output_token": int64(response.Usage.OutputTokens)}
		public, err := MarshalPublicJSON(response)
		return protocolExecution{providerRequestID: response.ID, quantities: quantities, response: public, winner: winner}, err
	})
}

func resolvedResponseModel(extra schemas.BifrostResponseExtraFields, fallback string) string {
	if alias := extra.RoutingInfo.ResolvedKeyAlias; alias != nil && alias.ModelID != "" {
		return alias.ModelID
	}
	if extra.RoutingInfo.Model != "" {
		return extra.RoutingInfo.Model
	}
	return fallback
}

func llmUsageQuantities(usage *schemas.BifrostLLMUsage) (map[string]int64, error) {
	cached := int64(0)
	if usage.PromptTokensDetails != nil {
		cached = int64(usage.PromptTokensDetails.CachedReadTokens)
	}
	input := int64(usage.PromptTokens) - cached
	if input < 0 {
		return nil, errors.New("provider cached input exceeds total input")
	}
	return map[string]int64{"input_token": input, "cached_input_token": cached, "output_token": int64(usage.CompletionTokens)}, nil
}

func pricedQuantities(prices map[string]store.GatewayPrice, quantities map[string]int64) ([]store.GatewayMetric, error) {
	names := make([]string, 0, len(quantities))
	for name, quantity := range quantities {
		if quantity < 0 {
			return nil, fmt.Errorf("negative provider usage for %s", name)
		}
		if quantity > 0 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, errors.New("provider response did not include measurable usage")
	}
	metrics := make([]store.GatewayMetric, 0, len(names))
	for _, name := range names {
		price, ok := prices[name]
		if !ok {
			return nil, fmt.Errorf("missing active %s price", name)
		}
		charge, err := store.CheckedCharge(quantities[name], price.EffectivePrice, price.UnitSize)
		if err != nil {
			return nil, err
		}
		metrics = append(metrics, store.GatewayMetric{Metric: name, Quantity: quantities[name], Price: price, Charge: charge})
	}
	return metrics, nil
}

func protocolCommitmentUpperBound(operation string, candidate store.GatewayCandidate, requestedMaxOutput int64) (int64, error) {
	for _, metric := range requiredProtocolPrices(operation) {
		if _, ok := candidate.Prices[metric]; !ok {
			return 0, fmt.Errorf("missing active %s price for %s", metric, operation)
		}
	}
	limits := map[string]int64{"request": 1}
	var total int64
	maxOutput := candidate.MaxOutputTokens
	if requestedMaxOutput > 0 {
		maxOutput = requestedMaxOutput
	}
	switch operation {
	case "audio.speech":
		limits["input_token"], limits["output_token"], limits["audio_second"] = candidate.ContextWindow, maxOutput, 3600
	case "audio.transcriptions":
		limits["input_token"], limits["output_token"], limits["audio_second"] = candidate.ContextWindow, maxOutput, 3600
	case "images.generations":
		limits["input_token"], limits["output_token"], limits["image"] = candidate.ContextWindow, maxOutput, 10
	case "embeddings":
		inputCommitment, err := inputTokenCommitmentUpperBound(candidate.Prices, candidate.ContextWindow)
		if err != nil {
			return 0, err
		}
		total = inputCommitment
	default:
		// Responses, Anthropic Messages and Gemini GenerateContent are
		// token-metered in the supported compatibility surface.
		inputCommitment, err := inputTokenCommitmentUpperBound(candidate.Prices, candidate.ContextWindow)
		if err != nil {
			return 0, err
		}
		total = inputCommitment
		limits["output_token"] = maxOutput
	}
	for metric, price := range candidate.Prices {
		quantity := limits[metric]
		if quantity == 0 {
			continue
		}
		charge, err := store.CheckedCharge(quantity, price.EffectivePrice, price.UnitSize)
		if err != nil || charge > math.MaxInt64-total {
			if err != nil {
				return 0, err
			}
			return 0, errors.New("quota commitment overflow")
		}
		total += charge
	}
	return total, nil
}

func inputTokenCommitmentUpperBound(prices map[string]store.GatewayPrice, contextWindow int64) (int64, error) {
	charges := make([]int64, 0, 2)
	for _, metric := range []string{"input_token", "cached_input_token"} {
		price, ok := prices[metric]
		if !ok {
			return 0, fmt.Errorf("missing active %s price", metric)
		}
		charge, err := store.CheckedCharge(contextWindow, price.EffectivePrice, price.UnitSize)
		if err != nil {
			return 0, err
		}
		charges = append(charges, charge)
	}
	upper := max(charges[0], charges[1])
	// A mixed input can incur both metric ceilings. The underlying unrounded
	// linear cost is maximized at one endpoint, and the second ceiling adds less
	// than one whole microcredit, so one extra integer unit is a safe bound.
	if contextWindow > 1 && charges[0] > 0 && charges[1] > 0 {
		if upper == math.MaxInt64 {
			return 0, errors.New("quota commitment overflow")
		}
		upper++
	}
	return upper, nil
}

func requiredProtocolPrices(operation string) []string {
	switch operation {
	case "embeddings":
		return []string{"input_token", "cached_input_token"}
	case "audio.speech", "audio.transcriptions":
		return []string{"request", "input_token", "output_token", "audio_second"}
	case "images.generations":
		return []string{"request", "input_token", "output_token", "image"}
	default:
		return []string{"input_token", "cached_input_token", "output_token"}
	}
}

func responsesMaxOutput(request *schemas.BifrostResponsesRequest) int64 {
	if request != nil && request.Params != nil && request.Params.MaxOutputTokens != nil {
		value := int64(*request.Params.MaxOutputTokens)
		// Zero is only the omitted sentinel inside Gizway. An explicitly supplied
		// zero must be rejected before local commitment/provider execution, matching
		// Chat Completions and preventing Bifrost from silently raising it.
		if value <= 0 {
			return -1
		}
		return value
	}
	return 0
}

func candidatesWithinOutputLimit(candidates []store.GatewayCandidate, requested int64) ([]store.GatewayCandidate, error) {
	if requested < 0 {
		return nil, fmt.Errorf("%w: maximum output tokens must be positive", ErrInvalidRequest)
	}
	if requested == 0 {
		return candidates, nil
	}
	allowed := make([]store.GatewayCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if requested <= candidate.MaxOutputTokens {
			allowed = append(allowed, candidate)
		}
	}
	if len(allowed) == 0 {
		return nil, fmt.Errorf("%w: requested output exceeds every authorized model variant limit", ErrInvalidRequest)
	}
	return allowed, nil
}

func validateCandidateUsage(operation string, candidate store.GatewayCandidate, quantities map[string]int64) error {
	input := quantities["input_token"] + quantities["cached_input_token"]
	if input < 0 || input > candidate.ContextWindow {
		return errors.New("provider input usage exceeds the authorized context window")
	}
	if output := quantities["output_token"]; output < 0 || output > candidate.MaxOutputTokens {
		return errors.New("provider output usage exceeds the authorized model limit")
	}
	if quantities["audio_second"] > 3600 || quantities["image"] > 10 {
		return fmt.Errorf("provider usage exceeds the supported %s request limit", operation)
	}
	return nil
}
