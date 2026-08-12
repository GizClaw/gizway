# Story 03 — Anthropic and Gemini wire contracts share the regional Catalog.
# protocol-case: anthropic.messages.nonstream native-json
# protocol-case: anthropic.messages.streaming sse
# protocol-case: gemini.generate-content.nonstream native-json
# protocol-case: gemini.stream-generate-content.streaming sse
# protocol-case: gemini.embed-content native-json
# protocol covers: Anthropic POST /v1/messages
# protocol covers: Anthropic streaming POST /v1/messages
# protocol covers: Gemini POST /v1beta/models/{model}:generateContent
# protocol covers: Gemini streaming POST /v1beta/models/{model}:streamGenerateContent
# protocol covers: Gemini POST /v1beta/models/{model}:embedContent

POST {{way_url}}/v1/messages
x-api-key: {{gateway_api_key}}
anthropic-version: 2023-06-01
Content-Type: application/json
{"model":"story-text","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}

HTTP 200
[Asserts]
jsonpath "$.type" == "message"
jsonpath "$.model" == "story-text"


POST {{way_url}}/v1/messages
x-api-key: {{gateway_api_key}}
anthropic-version: 2023-06-01
Content-Type: application/json
{"model":"story-text","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"stream"}]}

HTTP 200
[Asserts]
header "Content-Type" startsWith "text/event-stream"
body contains "event: message_stop"


POST {{way_url}}/v1beta/models/story-text:generateContent
x-goog-api-key: {{gateway_api_key}}
Content-Type: application/json
{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":64}}

HTTP 200
[Asserts]
jsonpath "$.candidates[0].content.role" == "model"
jsonpath "$.modelVersion" == "story-text"


POST {{way_url}}/v1beta/models/story-text:streamGenerateContent?alt=sse
x-goog-api-key: {{gateway_api_key}}
Content-Type: application/json
{"contents":[{"role":"user","parts":[{"text":"stream"}]}],"generationConfig":{"maxOutputTokens":64}}

HTTP 200
[Asserts]
header "Content-Type" startsWith "text/event-stream"
body contains "deterministic responses output"


POST {{way_url}}/v1beta/models/story-text:embedContent
x-goog-api-key: {{gateway_api_key}}
Content-Type: application/json
{"content":{"parts":[{"text":"embed"}]},"outputDimensionality":3}

HTTP 200
[Asserts]
jsonpath "$.embedding.values" count == 3
