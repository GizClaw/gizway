# Story 03 — OpenAI speech, transcription, and image generation settle real usage.
#
# Durable product requirements:
# - Speech returns provider audio bytes, not JSON or a generated placeholder.
#   OpenAI speech has no provider usage object, so the independently measurable
#   request metric is billed once: 9 microcredits.
# - Transcription crosses both public and provider multipart boundaries. Its
#   fractional 2.4-second usage rounds quantity upward to 3 seconds, then bills
#   3*18 plus request 9 = 63 microcredits.
# - Image generation requires at least one actual provider image and provider
#   token usage. Exact charge: input ceil(4*1800/1000)=8, output
#   ceil(3*3600/1000)=11, one image=90, request=9; total=118.
# - Each command persists the provider-compatible public result for exact
#   idempotent replay, while raw credentials remain outside response/storage.
#
# Fixture/isolation: every file gets a fresh isolated PostgreSQL schema, seed, Gizway
# process, Bifrost instance, and external supplier fake. The tiny text fixture
# is only non-empty uploaded bytes; no Gizway database or settlement is mocked.
# protocol-case: openai.audio.speech binary-response request-price
# protocol-case: openai.audio.transcriptions multipart duration-rounding
# protocol-case: openai.images.generations token-and-image-settlement
# protocol covers: OpenAI POST /v1/audio/speech
# protocol covers: OpenAI POST /v1/audio/transcriptions
# protocol covers: OpenAI POST /v1/images/generations
# covers: getAccountBalance listGatewayUsage

POST {{base_url}}/v1/audio/speech
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-speech-one
Content-Type: application/json
{
  "model": "story-text",
  "input": "speak deterministic audio",
  "voice": "alloy",
  "response_format": "mp3"
}

HTTP 200
[Asserts]
header "Content-Type" startsWith "audio/mpeg"
body contains "GIZWAY-DETERMINISTIC-MP3"


POST {{base_url}}/v1/audio/transcriptions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-transcription-one
[Multipart]
model: story-text
response_format: verbose_json
file: file,fixtures/story-audio.txt; text/plain

HTTP 200
[Asserts]
jsonpath "$.text" == "deterministic transcript"
jsonpath "$.duration" == 2.4
jsonpath "$.language" == "english"
jsonpath "$.usage.type" == "duration"
jsonpath "$.usage.seconds" == 2.4


POST {{base_url}}/v1/images/generations
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-image-one
Content-Type: application/json
{
  "model": "story-text",
  "prompt": "a deterministic blue square",
  "n": 1,
  "response_format": "b64_json"
}

HTTP 200
[Asserts]
jsonpath "$.id" == "fake-image-001"
jsonpath "$.model" == "story-text"
jsonpath "$.data" count == 1
jsonpath "$.data[0].b64_json" == "R0laV0FZLUlNQUdF"
jsonpath "$.usage.input_tokens" == 4
jsonpath "$.usage.output_tokens" == 3


GET {{fake_url}}/events

HTTP 200
[Asserts]
jsonpath "$.speech_calls" == 1
jsonpath "$.transcription_calls" == 1
jsonpath "$.image_calls" == 1


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.microcredits" == 99999810


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/usage?from=2026-08-10T00%3A00%3A00Z&to=2027-08-11T00%3A00%3A00Z
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 3
jsonpath "$.data[0].charged_microcredits" == 118
jsonpath "$.data[0].input_tokens" == 4
jsonpath "$.data[0].output_tokens" == 3
jsonpath "$.data[1].charged_microcredits" == 63
jsonpath "$.data[2].charged_microcredits" == 9
