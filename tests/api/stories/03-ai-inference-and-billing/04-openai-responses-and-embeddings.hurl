# Story 03 — OpenAI Responses and Embeddings use the same database-backed bill.
#
# Durable product requirements:
# - These are native compatible endpoints, not aliases that manufacture Chat
#   Completions. Gizway translates each public request into Bifrost's canonical
#   request, then Bifrost performs the external provider HTTP call.
# - The public model stays `story-text`; the private provider model and provider
#   credential are never exposed in compatible responses.
# - Responses cached tokens are separated from ordinary input tokens before
#   pricing. Exact charge: input ceil(9*1800/1000)=17, cached
#   ceil(2*900/1000)=2, output ceil(6*3600/1000)=22, request=9; total=50.
# - Embedding exact charge: input ceil(8*1800/1000)=15 plus request=9; total=24.
# - Idempotency is scoped by operation. Repeating Responses returns the stored
#   public response and cannot call the provider or ledger a second time.
#
# Fixture/isolation: a fresh PostgreSQL database and external OpenAI-compatible
# fake process are created for this Hurl file. The fake is only the supplier;
# all identity, catalog, reservation, pricing, ledger and replay state is real.
# protocol-case: openai.responses.nonstream exact-settlement cached-input replay
# protocol-case: openai.responses.tools-multimodal preserved-function-call
# protocol-case: openai.embeddings.nonstream exact-settlement
# protocol-case: openai.embeddings.batch dimensions-and-usage
# protocol covers: OpenAI POST /v1/responses
# protocol covers: OpenAI POST /v1/embeddings
# covers: getAccountBalance listGatewayUsage listAccountTransactions

# An explicit zero is invalid, rather than an omitted limit. Gizway must reject
# it before reserving Credit or calling Bifrost/provider.
POST {{base_url}}/v1/responses
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-responses-zero-output
Content-Type: application/json
{
  "model": "story-text",
  "input": "this must not reach the provider",
  "max_output_tokens": 0
}

HTTP 400

POST {{base_url}}/v1/responses
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-responses-one
Content-Type: application/json
{
  "model": "story-text",
  "input": "explain deterministic billing",
  "max_output_tokens": 64
}

HTTP 200
[Asserts]
jsonpath "$.id" == "fake-response-001"
jsonpath "$.object" == "response"
jsonpath "$.status" == "completed"
jsonpath "$.model" == "story-text"
jsonpath "$.output[0].content[0].text" == "deterministic responses output"
jsonpath "$.usage.input_tokens" == 11
jsonpath "$.usage.input_tokens_details.cached_tokens" == 2
jsonpath "$.usage.output_tokens" == 6
jsonpath "$.extra_fields" not exists


# Exact replay must return the same provider response ID without an external call.
POST {{base_url}}/v1/responses
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-responses-one
Content-Type: application/json
{
  "model": "story-text",
  "input": "explain deterministic billing",
  "max_output_tokens": 64
}

HTTP 200
[Asserts]
jsonpath "$.id" == "fake-response-001"


POST {{base_url}}/v1/responses
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-responses-tools-multimodal
Content-Type: application/json
{
  "model": "story-text",
  "input": [{
    "role": "user",
    "content": [
      {"type": "input_text", "text": "Use the weather tool after viewing this image"},
      {"type": "input_image", "image_url": "data:image/png;base64,iVBORw0KGgo="}
    ]
  }],
  "tools": [{
    "type": "function",
    "name": "get_weather",
    "description": "Return deterministic weather",
    "parameters": {
      "type": "object",
      "properties": {"city": {"type": "string"}},
      "required": ["city"],
      "additionalProperties": false
    },
    "strict": true
  }]
}

HTTP 200
[Asserts]
jsonpath "$.output[0].type" == "function_call"
jsonpath "$.output[0].call_id" == "call_weather_001"
jsonpath "$.output[0].name" == "get_weather"
jsonpath "$.output[0].arguments" == "{\"city\":\"Shanghai\"}"


POST {{base_url}}/v1/embeddings
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-embedding-one
Content-Type: application/json
{
  "model": "story-text",
  "input": "embed this deterministic sentence"
}

HTTP 200
[Asserts]
jsonpath "$.object" == "list"
jsonpath "$.model" == "story-text"
jsonpath "$.data" count == 1
jsonpath "$.data[0].object" == "embedding"
jsonpath "$.data[0].embedding" count == 3
jsonpath "$.usage.prompt_tokens" == 8
jsonpath "$.extra_fields" not exists


# Batch cardinality and requested vector dimensions must survive the typed
# OpenAI -> Bifrost path. It is a distinct command and usage charge.
POST {{base_url}}/v1/embeddings
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-embedding-batch
Content-Type: application/json
{
  "model": "story-text",
  "input": ["first", "second"],
  "dimensions": 2
}

HTTP 200
[Asserts]
jsonpath "$.data" count == 2
jsonpath "$.data[0].embedding" count == 2
jsonpath "$.data[1].embedding" count == 2
jsonpath "$.usage.prompt_tokens" == 8


GET {{fake_url}}/events

HTTP 200
[Asserts]
jsonpath "$.responses_calls" == 2
jsonpath "$.embedding_calls" == 2


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.microcredits" == 99999852


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/usage?from=2026-08-10T00%3A00%3A00Z&to=2027-08-11T00%3A00%3A00Z
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 4
jsonpath "$.data[0].charged_microcredits" == 24
jsonpath "$.data[0].input_tokens" == 8
jsonpath "$.data[1].charged_microcredits" == 24
jsonpath "$.data[1].input_tokens" == 8
jsonpath "$.data[2].charged_microcredits" == 50
jsonpath "$.data[2].input_tokens" == 9
jsonpath "$.data[2].cached_input_tokens" == 2
jsonpath "$.data[2].output_tokens" == 6
jsonpath "$.data[3].charged_microcredits" == 50


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/transactions
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 5
jsonpath "$.data[0].type" == "gateway_usage"
jsonpath "$.data[0].amount.microcredits" == 24
jsonpath "$.data[1].amount.microcredits" == 24
jsonpath "$.data[2].amount.microcredits" == 50
jsonpath "$.data[3].amount.microcredits" == 50
