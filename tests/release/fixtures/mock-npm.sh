#!/usr/bin/env bash
set -euo pipefail

printf '%s\n' "$*" >>"${MOCK_NPM_CALLS:?}"
case "${1:-}" in
  view)
    case "${MOCK_NPM_VIEW:-missing}" in
      existing)
        printf '"%s"\n' "${MOCK_NPM_INTEGRITY:?}"
        ;;
      missing)
        printf 'npm error code E404\nnpm error 404 Not Found\n' >&2
        exit 1
        ;;
      error)
        printf 'npm error code E500\nnpm error registry unavailable\n' >&2
        exit 1
        ;;
      *)
        printf 'unsupported MOCK_NPM_VIEW\n' >&2
        exit 2
        ;;
    esac
    ;;
  publish)
    ;;
  *)
    printf 'unexpected npm command: %s\n' "${1:-}" >&2
    exit 2
    ;;
esac
