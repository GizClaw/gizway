#!/bin/sh
set -eu

root="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
business="${root}/tests/e2e/resources/business.go"
web_test="${root}/web/apps/gizway/e2e/real-auth-and-sync.spec.ts"

if rg -n 'jmoiron/sqlx|lib/pq|gorm\.io|internal/adapter/bifrost|gizpay-dsn|cn-dsn|global-dsn|\b(psql|INSERT|UPDATE|DELETE|CREATE TABLE|ALTER TABLE)\b' "${business}"; then
    echo "E2E Business Seed must use HTTP APIs only" >&2
    exit 1
fi

if rg -n '\bpsql\b' "${web_test}"; then
    echo "real Web E2E must mutate Model Listings through Admin API" >&2
    exit 1
fi

if rg -n -- '--(gizpay|cn|global)-dsn' "${root}/tests/e2e/compose.yaml" "${root}/tests/e2e/resources"; then
    echo "E2E Business Seed must not accept business database DSNs" >&2
    exit 1
fi
