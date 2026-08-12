# Story 03 — OpenAI-compatible commands run in GizWay and report Usage later.
#
# Refactor 01 does not replay provider responses from an Idempotency-Key and it
# does not synchronously debit GizPay after each call. Every request below is a
# real provider call. Advancing past the Quota recheck deadline makes the next
# unmetered model read flush the accumulated identity-free Usage to GizPay.
# protocol-case: openai.chat.nonstream provider-roundtrip
# protocol-case: openai.chat.streaming sse
# protocol-case: openai.responses.nonstream provider-roundtrip
# protocol-case: openai.responses.streaming sse
# protocol-case: openai.embeddings.nonstream vector-shape
# protocol-case: openai.audio.speech binary-response
# protocol-case: openai.audio.transcriptions multipart
# protocol-case: openai.images.generations image-response
# protocol covers: OpenAI POST /v1/chat/completions
# protocol covers: OpenAI streaming POST /v1/chat/completions
# protocol covers: OpenAI POST /v1/responses
# protocol covers: OpenAI streaming POST /v1/responses
# protocol covers: OpenAI POST /v1/embeddings
# protocol covers: OpenAI POST /v1/audio/speech
# protocol covers: OpenAI POST /v1/audio/transcriptions
# protocol covers: OpenAI POST /v1/images/generations

POST {{way_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Content-Type: application/json
{"model":"story-text","messages":[{"role":"user","content":"hello"}],"stream":false}

HTTP 200
[Asserts]
jsonpath "$.object" == "chat.completion"
jsonpath "$.model" == "story-text"


POST {{way_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Content-Type: application/json
{"model":"story-text","messages":[{"role":"user","content":"stream"}],"stream":true,"max_tokens":64}

HTTP 200
[Asserts]
header "Content-Type" startsWith "text/event-stream"
body contains "data: [DONE]"


POST {{way_url}}/v1/responses
Authorization: Bearer {{gateway_api_key}}
Content-Type: application/json
{"model":"story-text","input":"respond","max_output_tokens":64}

HTTP 200
[Asserts]
jsonpath "$.object" == "response"
jsonpath "$.model" == "story-text"


POST {{way_url}}/v1/responses
Authorization: Bearer {{gateway_api_key}}
Content-Type: application/json
{"model":"story-text","input":"stream response","stream":true,"max_output_tokens":64}

HTTP 200
[Asserts]
header "Content-Type" startsWith "text/event-stream"
body contains "event: response.completed"


POST {{way_url}}/v1/embeddings
Authorization: Bearer {{gateway_api_key}}
Content-Type: application/json
{"model":"story-text","input":"embed this"}

HTTP 200
[Asserts]
jsonpath "$.object" == "list"
jsonpath "$.data[0].embedding" count == 3


POST {{way_url}}/v1/audio/speech
Authorization: Bearer {{gateway_api_key}}
Content-Type: application/json
{"model":"story-text","input":"speak","voice":"alloy","response_format":"mp3"}

HTTP 200
[Asserts]
header "Content-Type" startsWith "audio/mpeg"
body contains "GIZWAY-DETERMINISTIC-MP3"


POST {{way_url}}/v1/audio/transcriptions
Authorization: Bearer {{gateway_api_key}}
[Multipart]
model: story-text
response_format: verbose_json
file: file,fixtures/story-audio.txt; text/plain

HTTP 200
[Asserts]
jsonpath "$.text" == "deterministic transcript"


POST {{way_url}}/v1/images/generations
Authorization: Bearer {{gateway_api_key}}
Content-Type: application/json
{"model":"story-text","prompt":"a blue square","n":1,"response_format":"b64_json"}

HTTP 200
[Asserts]
jsonpath "$.data[0].b64_json" == "R0laV0FZLUlNQUdF"


POST {{way_url}}/test/v1/clock/advance
Content-Type: application/json
{"by":"5m"}

HTTP 200


GET {{way_url}}/v1/models
Authorization: Bearer {{gateway_api_key}}

HTTP 200


GET {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/usage?from=2026-08-10T00%3A00%3A00Z&to=2027-08-13T00%3A00%3A00Z
Authorization: Bearer {{active_user_one_key}}
[Options]
retry: 20
retry-interval: 25ms

HTTP 200
[Asserts]
jsonpath "$.data" count == 8
