package gizwaypublic

import (
	"net/http"
	"strings"
)

// registerGeminiHandlers adapts OpenAPI's embedded path parameters to Go's
// whole-segment ServeMux wildcard syntax without widening the operation set.
func registerGeminiHandlers(m ServeMux, baseURL string, wrapper ServerInterfaceWrapper) {
	m.HandleFunc(http.MethodPost+" "+baseURL+"/genai/v1beta/models/{operation}", func(w http.ResponseWriter, r *http.Request) {
		operation := r.PathValue("operation")
		if model, ok := strings.CutSuffix(operation, ":generateContent"); ok && model != "" {
			r.SetPathValue("model", model)
			wrapper.GenerateGeminiContent(w, r)
			return
		}
		if model, ok := strings.CutSuffix(operation, ":streamGenerateContent"); ok && model != "" {
			r.SetPathValue("model", model)
			wrapper.StreamGeminiContent(w, r)
			return
		}
		http.NotFound(w, r)
	})
}
