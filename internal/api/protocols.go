package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	bfanthropic "github.com/maximhq/bifrost/core/providers/anthropic"
	bfgemini "github.com/maximhq/bifrost/core/providers/gemini"
	bfopenai "github.com/maximhq/bifrost/core/providers/openai"
	"github.com/maximhq/bifrost/core/schemas"

	"github.com/idy/gizway/internal/service/gateway"
	"github.com/idy/gizway/internal/store"
)

func gatewayPrincipal(r *http.Request) store.GatewayPrincipal {
	return store.GatewayPrincipal{
		UserID: contextString(r.Context(), userIDKey), AccountID: contextString(r.Context(), accountIDKey),
		APIKeyID: contextString(r.Context(), apiKeyIDKey),
	}
}

func (s *Server) openAIResponses(w http.ResponseWriter, r *http.Request) {
	if !s.requireAICommand(w, r) {
		return
	}
	var request bfopenai.OpenAIResponsesRequest
	if err := decodeJSON(r, &request); err != nil || request.Model == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "a valid model and input are required")
		return
	}
	codecContext := schemas.NewBifrostContext(r.Context(), time.Now().Add(30*time.Second))
	defer codecContext.Cancel()
	canonical := request.ToBifrostResponsesRequest(codecContext)
	if request.IsStreamingRequested() {
		s.streamResponses(w, r, "responses", request.Model, request, canonical,
			func(_ context.Context, response *schemas.BifrostResponsesStreamResponse, _ string) ([][]byte, error) {
				public, err := gateway.MarshalPublicJSON(response.WithDefaults())
				if err != nil {
					return nil, err
				}
				return [][]byte{[]byte("event: " + string(response.Type) + "\ndata: " + string(public) + "\n\n")}, nil
			})
		return
	}
	response, err := s.gateway.ExecuteResponses(r.Context(), gatewayPrincipal(r), "responses", r.Header.Get("Idempotency-Key"), request.Model, request, canonical,
		func(_ context.Context, response *schemas.BifrostResponsesResponse, _ string) ([]byte, error) {
			return gateway.MarshalPublicJSON(response)
		})
	if err != nil {
		s.writeGatewayExecutionError(w, err)
		return
	}
	writeProtocolJSON(w, response)
}

func (s *Server) openAIEmbeddings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAICommand(w, r) {
		return
	}
	var request bfopenai.OpenAIEmbeddingRequest
	if err := decodeJSON(r, &request); err != nil || request.Model == "" || request.Input == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "model and embedding input are required")
		return
	}
	codecContext := schemas.NewBifrostContext(r.Context(), time.Now().Add(30*time.Second))
	defer codecContext.Cancel()
	response, err := s.gateway.ExecuteEmbedding(r.Context(), gatewayPrincipal(r), r.Header.Get("Idempotency-Key"), request.Model, request, request.ToBifrostEmbeddingRequest(codecContext))
	if err != nil {
		s.writeGatewayExecutionError(w, err)
		return
	}
	writeProtocolJSON(w, response)
}

func (s *Server) openAISpeech(w http.ResponseWriter, r *http.Request) {
	if !s.requireAICommand(w, r) {
		return
	}
	var request bfopenai.OpenAISpeechRequest
	if err := decodeJSON(r, &request); err != nil || request.Model == "" || request.Input == "" || request.IsStreamingRequested() {
		writeError(w, http.StatusBadRequest, "invalid_request", "non-streaming model, input, and voice are required")
		return
	}
	codecContext := schemas.NewBifrostContext(r.Context(), time.Now().Add(30*time.Second))
	defer codecContext.Cancel()
	responseJSON, err := s.gateway.ExecuteSpeech(r.Context(), gatewayPrincipal(r), r.Header.Get("Idempotency-Key"), request.Model, request, request.ToBifrostSpeechRequest(codecContext))
	if err != nil {
		s.writeGatewayExecutionError(w, err)
		return
	}
	var response schemas.BifrostSpeechResponse
	if err := json.Unmarshal(responseJSON, &response); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "stored speech response is invalid")
		return
	}
	contentType := "audio/mpeg"
	if request.ResponseFormat == "wav" {
		contentType = "audio/wav"
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(response.Audio)
}

func (s *Server) openAITranscription(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	if s.gateway == nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "AI execution is not configured")
		return
	}
	const maximumAudio = 16 << 20
	// ParseMultipartForm's argument is only the in-memory threshold; without a
	// MaxBytesReader an attacker can spill unbounded extra parts to disk before
	// the selected file is checked. Allow one MiB for multipart headers/fields
	// while bounding the entire authenticated request.
	r.Body = http.MaxBytesReader(w, r.Body, maximumAudio+(1<<20))
	if err := r.ParseMultipartForm(maximumAudio); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "multipart request exceeds the 17 MiB transport limit")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_request", "valid multipart audio is required")
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "audio file is required")
		return
	}
	defer file.Close()
	audio, err := io.ReadAll(io.LimitReader(file, maximumAudio+1))
	if err != nil || len(audio) == 0 || len(audio) > maximumAudio || r.FormValue("model") == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "model and audio up to 16 MiB are required")
		return
	}
	request := bfopenai.OpenAITranscriptionRequest{Model: r.FormValue("model"), File: audio, Filename: header.Filename}
	if format := r.FormValue("response_format"); format != "" {
		request.ResponseFormat = &format
	}
	codecContext := schemas.NewBifrostContext(r.Context(), time.Now().Add(30*time.Second))
	defer codecContext.Cancel()
	response, err := s.gateway.ExecuteTranscription(r.Context(), gatewayPrincipal(r), r.Header.Get("Idempotency-Key"), request.Model, request, request.ToBifrostTranscriptionRequest(codecContext))
	if err != nil {
		s.writeGatewayExecutionError(w, err)
		return
	}
	writeProtocolJSON(w, response)
}

func (s *Server) openAIImageGeneration(w http.ResponseWriter, r *http.Request) {
	if !s.requireAICommand(w, r) {
		return
	}
	var request bfopenai.OpenAIImageGenerationRequest
	if err := decodeJSON(r, &request); err != nil || request.Model == "" || request.Prompt == "" || request.IsStreamingRequested() {
		writeError(w, http.StatusBadRequest, "invalid_request", "non-streaming model and prompt are required")
		return
	}
	codecContext := schemas.NewBifrostContext(r.Context(), time.Now().Add(30*time.Second))
	defer codecContext.Cancel()
	response, err := s.gateway.ExecuteImageGeneration(r.Context(), gatewayPrincipal(r), r.Header.Get("Idempotency-Key"), request.Model, request, request.ToBifrostImageGenerationRequest(codecContext))
	if err != nil {
		s.writeGatewayExecutionError(w, err)
		return
	}
	writeProtocolJSON(w, response)
}

func (s *Server) anthropicMessages(w http.ResponseWriter, r *http.Request) {
	if !s.requireAICommand(w, r) {
		return
	}
	var request bfanthropic.AnthropicMessageRequest
	if err := decodeJSON(r, &request); err != nil || request.Model == "" || len(request.Messages) == 0 || request.MaxTokens <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "model, messages, and positive max_tokens are required")
		return
	}
	codecContext := schemas.NewBifrostContext(r.Context(), time.Now().Add(30*time.Second))
	defer codecContext.Cancel()
	canonical := request.ToBifrostResponsesRequest(codecContext)
	if request.Stream != nil && *request.Stream {
		s.streamResponses(w, r, "anthropic.messages", request.Model, request, canonical,
			func(ctx context.Context, response *schemas.BifrostResponsesStreamResponse, _ string) ([][]byte, error) {
				bfctx := schemas.NewBifrostContext(ctx, time.Now().Add(30*time.Second))
				defer bfctx.Cancel()
				events := bfanthropic.ToAnthropicResponsesStreamResponse(bfctx, response)
				frames := make([][]byte, 0, len(events))
				for _, event := range events {
					public, err := json.Marshal(event)
					if err != nil {
						return nil, err
					}
					frames = append(frames, []byte("event: "+string(event.Type)+"\ndata: "+string(public)+"\n\n"))
				}
				return frames, nil
			})
		return
	}
	response, err := s.gateway.ExecuteResponses(r.Context(), gatewayPrincipal(r), "anthropic.messages", r.Header.Get("Idempotency-Key"), request.Model, request, canonical,
		func(ctx context.Context, response *schemas.BifrostResponsesResponse, _ string) ([]byte, error) {
			bfctx := schemas.NewBifrostContext(ctx, time.Now().Add(30*time.Second))
			defer bfctx.Cancel()
			return json.Marshal(bfanthropic.ToAnthropicResponsesResponse(bfctx, response))
		})
	if err != nil {
		s.writeGatewayExecutionError(w, err)
		return
	}
	writeProtocolJSON(w, response)
}

func (s *Server) geminiGenerateContent(w http.ResponseWriter, r *http.Request) {
	if !s.requireAICommand(w, r) {
		return
	}
	model, action, ok := strings.Cut(r.PathValue("operation"), ":")
	if !ok || model == "" {
		writeError(w, http.StatusNotFound, "not_found", "Gemini operation is not supported")
		return
	}
	if action == "embedContent" || action == "batchEmbedContents" {
		s.geminiEmbeddings(w, r, model, action)
		return
	}
	if action != "generateContent" && action != "streamGenerateContent" {
		writeError(w, http.StatusNotImplemented, "unsupported_operation", "Gemini operation is explicitly unsupported by this provider contract")
		return
	}
	var request bfgemini.GeminiGenerationRequest
	if err := decodeJSON(r, &request); err != nil || len(request.Contents) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "Gemini contents are required")
		return
	}
	request.Model = model
	codecContext := schemas.NewBifrostContext(r.Context(), time.Now().Add(30*time.Second))
	defer codecContext.Cancel()
	canonical := request.ToBifrostResponsesRequest(codecContext)
	if action == "streamGenerateContent" {
		state := bfgemini.NewBifrostToGeminiStreamState()
		s.streamResponses(w, r, "gemini.streamGenerateContent", model, request, canonical,
			func(_ context.Context, response *schemas.BifrostResponsesStreamResponse, _ string) ([][]byte, error) {
				converted := bfgemini.ToGeminiResponsesStreamResponse(response, state)
				if converted == nil {
					return nil, nil
				}
				public, err := json.Marshal(converted)
				if err != nil {
					return nil, err
				}
				return [][]byte{[]byte("data: " + string(public) + "\n\n")}, nil
			})
		return
	}
	response, err := s.gateway.ExecuteResponses(r.Context(), gatewayPrincipal(r), "gemini.generateContent", r.Header.Get("Idempotency-Key"), model, request, canonical,
		func(_ context.Context, response *schemas.BifrostResponsesResponse, _ string) ([]byte, error) {
			return json.Marshal(bfgemini.ToGeminiResponsesResponse(response))
		})
	if err != nil {
		s.writeGatewayExecutionError(w, err)
		return
	}
	writeProtocolJSON(w, response)
}

func (s *Server) geminiEmbeddings(w http.ResponseWriter, r *http.Request, model, action string) {
	var request bfgemini.GeminiGenerationRequest
	if action == "embedContent" {
		var single struct {
			Content              *bfgemini.Content `json:"content"`
			TaskType             *string           `json:"taskType,omitempty"`
			Title                *string           `json:"title,omitempty"`
			OutputDimensionality *int              `json:"outputDimensionality,omitempty"`
		}
		if err := decodeJSON(r, &single); err != nil || single.Content == nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "Gemini embedding content is required")
			return
		}
		request.Requests = []bfgemini.GeminiEmbeddingRequest{{Content: single.Content, TaskType: single.TaskType, Title: single.Title, OutputDimensionality: single.OutputDimensionality}}
	} else {
		if err := decodeJSON(r, &request); err != nil || len(request.Requests) == 0 {
			writeError(w, http.StatusBadRequest, "invalid_request", "Gemini embedding requests are required")
			return
		}
	}
	request.Model = model
	codecContext := schemas.NewBifrostContext(r.Context(), time.Now().Add(30*time.Second))
	defer codecContext.Cancel()
	responseJSON, err := s.gateway.ExecuteEmbedding(r.Context(), gatewayPrincipal(r), r.Header.Get("Idempotency-Key"), model, request, request.ToBifrostEmbeddingRequest(codecContext))
	if err != nil {
		s.writeGatewayExecutionError(w, err)
		return
	}
	var response schemas.BifrostEmbeddingResponse
	if err := json.Unmarshal(responseJSON, &response); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "stored Gemini embedding response is invalid")
		return
	}
	var public any = bfgemini.ToGeminiEmbeddingResponse(&response)
	if action == "embedContent" {
		public = bfgemini.ToGeminiEmbedContentResponse(&response)
	}
	writeJSON(w, http.StatusOK, public)
}

func (s *Server) unsupportedAICommand(w http.ResponseWriter, r *http.Request) {
	if !s.requireAICommand(w, r) {
		return
	}
	writeError(w, http.StatusNotImplemented, "unsupported_operation", "operation is explicitly unsupported by the configured provider contract")
}

func (s *Server) unsupportedAIRead(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotImplemented, "unsupported_operation", "operation is explicitly unsupported by the configured provider contract")
}

func (s *Server) requireAICommand(w http.ResponseWriter, r *http.Request) bool {
	if !requireIdempotencyKey(w, r) {
		return false
	}
	if s.gateway == nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "AI execution is not configured")
		return false
	}
	return true
}

func (s *Server) writeGatewayExecutionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, gateway.ErrInvalidRequest):
		writeError(w, http.StatusBadRequest, "invalid_request", "AI request parameters are invalid")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "model_not_found", "model is not available")
	case errors.Is(err, store.ErrInsufficientBalance):
		writeError(w, http.StatusPaymentRequired, "insufficient_balance", "available balance is insufficient")
	case errors.Is(err, store.ErrAccountFrozen):
		writeError(w, http.StatusLocked, "account_frozen", "account balance is frozen")
	case errors.Is(err, store.ErrIdempotencyConflict):
		writeError(w, http.StatusConflict, "idempotency_conflict", "idempotency key was used with a different payload")
	case errors.Is(err, store.ErrCommandInProgress):
		writeError(w, http.StatusConflict, "idempotency_in_progress", "idempotent command is already being executed")
	default:
		writeError(w, http.StatusBadGateway, "provider_error", "AI request failed")
	}
}

func writeProtocolJSON(w http.ResponseWriter, response []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(response)
}

func (s *Server) streamResponses(w http.ResponseWriter, r *http.Request, operation, publicModel string, fingerprint any, request *schemas.BifrostResponsesRequest, render gateway.ProtocolStreamRenderer) {
	wroteHeader := false
	emit := func(frame []byte) error {
		if !wroteHeader {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
			wroteHeader = true
		}
		if _, err := w.Write(frame); err != nil {
			return err
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		return nil
	}
	err := s.gateway.ExecuteResponsesStream(r.Context(), gatewayPrincipal(r), operation, r.Header.Get("Idempotency-Key"), publicModel, fingerprint, request, render, emit)
	if err != nil && !wroteHeader {
		s.writeGatewayExecutionError(w, err)
	}
}
