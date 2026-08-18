#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == manifest && "${2:-}" == fetch ]]; then
  case "${MOCK_ORAS_MODE:?}" in
    publish)
      ref="${!#}"
      repository="${ref%:*}"
      if [[ -f "${MOCK_ORAS_STATE:?}" ]] && grep -Fq "$repository:" "$MOCK_ORAS_STATE"; then
        printf '{"digest":"%s"}\n' "${MOCK_ORAS_DIGEST:?}"
      else
        printf 'Error response from registry: manifest not found\n' >&2
        exit 1
      fi
      ;;
    same)
      printf '{"digest":"%s"}\n' "${MOCK_ORAS_DIGEST:?}"
      ;;
    conflict)
      printf '{"digest":"sha256:%064d"}\n' 9
      ;;
    network)
      printf 'dial tcp: network unavailable\n' >&2
      exit 1
      ;;
  esac
  exit 0
fi

if [[ "${1:-}" == cp ]]; then
  printf '%s\n' "$*" >>"${MOCK_ORAS_LOG:?}"
  printf '%s\n' "${!#}" >>"${MOCK_ORAS_STATE:?}"
  exit 0
fi

printf 'unexpected mock ORAS invocation: %s\n' "$*" >&2
exit 2
