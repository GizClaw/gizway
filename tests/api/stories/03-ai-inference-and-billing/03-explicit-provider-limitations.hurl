# Story 03 — Implemented compatibility gaps fail explicitly and predictably.
# protocol-case: openai.images.edit explicit-not-implemented
# protocol-case: anthropic.batches explicit-not-implemented
# protocol-case: gemini.files explicit-not-implemented
# protocol covers: OpenAI POST /v1/images/edits
# protocol covers: Anthropic POST /v1/messages/batches
# protocol covers: Gemini GET /v1beta/files

POST {{way_url}}/v1/images/edits
Authorization: Bearer {{gateway_api_key}}
Content-Type: application/json
{}

HTTP 501


POST {{way_url}}/v1/messages/batches
x-api-key: {{gateway_api_key}}
anthropic-version: 2023-06-01
Content-Type: application/json
{}

HTTP 501


GET {{way_url}}/v1beta/files
x-goog-api-key: {{gateway_api_key}}

HTTP 501
