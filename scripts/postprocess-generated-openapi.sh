#!/bin/sh
set -eu

file="${1:?generated gizway-public file is required}"
perl -0pi -e 's~\tm\.HandleFunc\(http\.MethodPost\+" "\+options\.BaseURL\+"/genai/v1beta/models/\{model\}:generateContent", wrapper\.GenerateGeminiContent\)\n\tm\.HandleFunc\(http\.MethodPost\+" "\+options\.BaseURL\+"/genai/v1beta/models/\{model\}:streamGenerateContent", wrapper\.StreamGeminiContent\)~\tregisterGeminiHandlers(m, options.BaseURL, wrapper)~' "$file"

grep -F 'registerGeminiHandlers(m, options.BaseURL, wrapper)' "$file" >/dev/null || {
  echo "failed to install the generated Gemini ServeMux adapter in $file" >&2
  exit 1
}
if grep -E 'm\.HandleFunc\(.*models/\{model\}:(generateContent|streamGenerateContent)' "$file" >/dev/null; then
  echo "invalid embedded-wildcard ServeMux route remains in $file" >&2
  exit 1
fi
