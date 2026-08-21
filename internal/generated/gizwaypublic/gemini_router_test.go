package gizwaypublic

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type routerProbe struct {
	operation string
	model     string
}

func (p *routerProbe) CreateAnthropicMessage(http.ResponseWriter, *http.Request, CreateAnthropicMessageParams) {
}
func (p *routerProbe) GetPublicCatalogToken(http.ResponseWriter, *http.Request)  {}
func (p *routerProbe) GetPublicRuntimeConfig(http.ResponseWriter, *http.Request) {}
func (p *routerProbe) GenerateGeminiContent(w http.ResponseWriter, _ *http.Request, model string) {
	p.operation, p.model = "generate", model
	w.WriteHeader(http.StatusNoContent)
}
func (p *routerProbe) StreamGeminiContent(w http.ResponseWriter, _ *http.Request, model string) {
	p.operation, p.model = "stream", model
	w.WriteHeader(http.StatusNoContent)
}
func (p *routerProbe) CreateChatCompletion(http.ResponseWriter, *http.Request) {}
func (p *routerProbe) ListModels(http.ResponseWriter, *http.Request)           {}
func (p *routerProbe) ConnectRealtimeWebSocket(http.ResponseWriter, *http.Request, ConnectRealtimeWebSocketParams) {
}
func (p *routerProbe) CreateRealtimeClientSecret(http.ResponseWriter, *http.Request) {}

func TestGeneratedGeminiServeMuxAdapter(t *testing.T) {
	probe := &routerProbe{}
	handler := Handler(probe)
	for _, test := range []struct {
		path, operation string
		status          int
	}{
		{"/genai/v1beta/models/gemini-2.5:generateContent", "generate", http.StatusNoContent},
		{"/genai/v1beta/models/gemini-2.5:streamGenerateContent", "stream", http.StatusNoContent},
		{"/genai/v1beta/models/gemini-2.5:unknown", "", http.StatusNotFound},
	} {
		probe.operation, probe.model = "", ""
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, test.path, nil))
		if response.Code != test.status || probe.operation != test.operation {
			t.Errorf("POST %s status=%d operation=%q, want status=%d operation=%q", test.path, response.Code, probe.operation, test.status, test.operation)
		}
		if test.operation != "" && probe.model != "gemini-2.5" {
			t.Errorf("POST %s model=%q, want gemini-2.5", test.path, probe.model)
		}
	}
}
