# M03 stable wire checks complementing the official SDK E2E.
# covers: gizway-public.yaml#listModels gizway-public.yaml#createChatCompletion
# covers: gizway-public.yaml#createAnthropicMessage gizway-public.yaml#generateGeminiContent gizway-public.yaml#streamGeminiContent
# covers: gizway-public.yaml#createRealtimeClientSecret gizway-public.yaml#connectRealtimeWebSocket
# protocol-case: m03.openai.models
# protocol covers: OpenAI Models response shape and Subscription Key bearer authentication
# protocol-case: m03.openai.chat
# protocol covers: OpenAI Chat Completions response is returned from the selected Provider
# protocol-case: m03.anthropic.messages
# protocol covers: Anthropic x-api-key authentication and version forwarding
# protocol-case: m03.gemini.generate
# protocol covers: Gemini x-goog-api-key authentication and model operation mapping
# protocol-case: m03.ai.rejected-routes
# protocol covers: removed root APIs and unknown prefixed operations return 404
# protocol-case: m03.ai.invalid-subscription-key
# protocol covers: all SDK protocols preserve the invalid Subscription Key business rejection

POST {{way_url}}/openai/v1/chat/completions
Authorization: Bearer unknown-subscription-key
Content-Type: application/json
{"model":"{{seeded_model_name}}","messages":[{"role":"user","content":"must reject"}]}

HTTP 401
[Asserts]
jsonpath "$.error.code" == "invalid_subscription_key"


POST {{way_url}}/anthropic/v1/messages
x-api-key: unknown-subscription-key
anthropic-version: 2023-06-01
Content-Type: application/json
{"model":"{{seeded_model_name}}","max_tokens":32,"messages":[{"role":"user","content":"must reject"}]}

HTTP 401
[Asserts]
jsonpath "$.error.code" == "invalid_subscription_key"


POST {{way_url}}/genai/v1beta/models/{{seeded_model_name}}:generateContent
x-goog-api-key: unknown-subscription-key
Content-Type: application/json
{"contents":[{"role":"user","parts":[{"text":"must reject"}]}]}

HTTP 401
[Asserts]
jsonpath "$.error.code" == "invalid_subscription_key"

GET {{way_url}}/openai/v1/models
Authorization: Bearer unknown-subscription-key

HTTP 401
[Asserts]
jsonpath "$.error.code" == "invalid_subscription_key"


POST {{way_url}}/openai/v1/realtime/client_secrets
Authorization: Bearer unknown-subscription-key
Content-Type: application/json
{"model":"{{seeded_model_name}}","transport":"websocket"}

HTTP 401
[Asserts]
jsonpath "$.error.code" == "invalid_subscription_key"

GET {{way_url}}/openai/v1/models
Authorization: Bearer {{raw_subscription_key}}

HTTP 200
[Asserts]
jsonpath "$.object" == "list"
jsonpath "$.data[0].id" exists


POST {{way_url}}/openai/v1/chat/completions
Authorization: Bearer {{raw_subscription_key}}
Content-Type: application/json
{"model":"{{seeded_model_name}}","messages":[{"role":"user","content":"m03 marker"}],"stream":false}

HTTP 200
[Asserts]
jsonpath "$.choices[0].message.content" contains "deterministic story response"
jsonpath "$.usage" exists


POST {{way_url}}/anthropic/v1/messages
x-api-key: {{raw_subscription_key}}
anthropic-version: 2023-06-01
Content-Type: application/json
{"model":"{{seeded_model_name}}","max_tokens":32,"messages":[{"role":"user","content":"m03 marker"}]}

HTTP 200
[Asserts]
jsonpath "$.content[0].text" contains "deterministic story response"
jsonpath "$.usage" exists


POST {{way_url}}/genai/v1beta/models/{{seeded_model_name}}:generateContent
x-goog-api-key: {{raw_subscription_key}}
Content-Type: application/json
{"contents":[{"role":"user","parts":[{"text":"m03 marker"}]}]}

HTTP 200
[Asserts]
jsonpath "$.candidates[0].content.parts[0].text" contains "deterministic story response"
jsonpath "$.usageMetadata" exists


POST {{way_url}}/genai/v1beta/models/{{seeded_model_name}}:streamGenerateContent
x-goog-api-key: {{raw_subscription_key}}
Content-Type: application/json
{"contents":[{"role":"user","parts":[{"text":"m03 marker"}]}]}

HTTP 200
[Asserts]
header "Content-Type" startsWith "text/event-stream"
body contains "data:"
body contains "deterministic"


POST {{way_url}}/v1/chat/completions
Authorization: Bearer {{raw_subscription_key}}
Content-Type: application/json
{"model":"{{seeded_model_name}}","messages":[{"role":"user","content":"removed route"}]}

HTTP 404


POST {{way_url}}/v1beta/models/{{seeded_model_name}}:generateContent
x-goog-api-key: {{raw_subscription_key}}
Content-Type: application/json
{"contents":[{"role":"user","parts":[{"text":"removed route"}]}]}

HTTP 404


POST {{way_url}}/genai/v1beta/models/story-text:unknownOperation
x-goog-api-key: {{raw_subscription_key}}
Content-Type: application/json
{"contents":[{"role":"user","parts":[{"text":"unknown route"}]}]}

HTTP 404


POST {{way_url}}/openai/v1/realtime/client_secrets
Authorization: Bearer {{raw_subscription_key}}
Content-Type: application/json
{"model":"{{seeded_model_name}}","transport":"websocket"}

HTTP 201
[Captures]
realtime_secret: jsonpath "$.client_secret.value"
realtime_session_id: jsonpath "$.session.session_id"
[Asserts]
jsonpath "$.client_secret.value" exists
jsonpath "$.client_secret.expires_at" exists


# The fixture drives the real public WebSocket and waits for the terminal Usage event.
POST http://fake-provider-cn:19000/drive-realtime
Content-Type: application/json
{
  "api_url": "{{way_url}}",
  "secret": "{{realtime_secret}}",
  "session_id": "{{realtime_session_id}}",
  "scenario": "text_audio"
}

HTTP 200
[Asserts]
jsonpath "$.driven" == true


# The ephemeral Secret is one-purpose after a successful connection.
GET {{way_url}}/openai/v1/realtime?session_id={{realtime_session_id}}
Authorization: Bearer {{realtime_secret}}
Connection: Upgrade
Upgrade: websocket

HTTP 401


GET {{way_url}}/openai/v1/realtime
Authorization: Bearer invalid-realtime-secret
Connection: Upgrade
Upgrade: websocket

HTTP 401
