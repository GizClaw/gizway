package gateway

import (
	"bytes"
	"encoding/json"
)

// MarshalPublicJSON removes Bifrost-only observability envelopes before bytes
// cross a provider-compatible API boundary. RoutingInfo remains available to
// settlement code before this call, but private provider model/key metadata is
// not part of OpenAI, Anthropic, or Gemini public schemas.
func MarshalPublicJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var public any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&public); err != nil {
		return nil, err
	}
	stripBifrostFields(public)
	return json.Marshal(public)
}

func stripBifrostFields(value any) {
	switch typed := value.(type) {
	case map[string]any:
		delete(typed, "extra_fields")
		for _, child := range typed {
			stripBifrostFields(child)
		}
	case []any:
		for _, child := range typed {
			stripBifrostFields(child)
		}
	}
}
