#!/bin/sh

test_postgresql_container="${GIZWAY_TEST_POSTGRES_CONTAINER:-}"
test_postgresql_owned=false

start_test_postgresql() {
    if [ -n "${GIZWAY_TEST_POSTGRES_DSN:-}" ]; then
        return
    fi
    if ! command -v docker >/dev/null 2>&1; then
        echo "docker is required when GIZWAY_TEST_POSTGRES_DSN is not set" >&2
        exit 1
    fi

    test_postgresql_container="gizway-test-postgresql-$$"
    test_postgresql_owned=true
    GIZWAY_TEST_POSTGRES_CONTAINER="${test_postgresql_container}"
    export GIZWAY_TEST_POSTGRES_CONTAINER
    docker run --detach --rm \
        --name "${test_postgresql_container}" \
        --env POSTGRES_USER=postgres \
        --env POSTGRES_PASSWORD=postgres \
        --env POSTGRES_DB=gizway_test \
        --publish 127.0.0.1::5432 \
        postgres:17.10-bookworm >/dev/null

    test_postgresql_port="$(docker port "${test_postgresql_container}" 5432/tcp | sed -E 's/.*:([0-9]+)$/\1/')"
    if [ -z "${test_postgresql_port}" ]; then
        echo "could not resolve disposable PostgreSQL port" >&2
        stop_test_postgresql
        exit 1
    fi
    GIZWAY_TEST_POSTGRES_DSN="postgres://postgres:postgres@127.0.0.1:${test_postgresql_port}/gizway_test?sslmode=disable"
    export GIZWAY_TEST_POSTGRES_DSN

    attempt=0
    # The official image first starts a temporary PostgreSQL instance while it
    # initializes the data directory, then shuts that instance down before
    # starting the real server. pg_isready alone can observe that temporary
    # window and let a test race directly into "database system is shutting
    # down". Require the entrypoint completion marker as well as final
    # readiness so every caller receives a stable disposable database.
    until docker logs "${test_postgresql_container}" 2>&1 | rg -q 'PostgreSQL init process complete; ready for start up.' \
        && docker exec "${test_postgresql_container}" pg_isready --username postgres --dbname gizway_test >/dev/null 2>&1; do
        attempt=$((attempt + 1))
        # Cold Docker volumes on desktop hosts can spend well over 15 seconds
        # in initdb/fsync before the final server starts. Keep polling in small
        # intervals, but allow one minute before treating startup as failed.
        if [ "${attempt}" -ge 240 ]; then
            echo "disposable PostgreSQL did not become ready" >&2
            docker logs "${test_postgresql_container}" >&2 || true
            stop_test_postgresql
            exit 1
        fi
        sleep 0.25
    done
}

create_test_postgresql_schema() {
    schema="$1"
    if [ -n "${test_postgresql_container}" ]; then
        docker exec "${test_postgresql_container}" psql --username postgres --dbname gizway_test \
            --set ON_ERROR_STOP=1 --command "CREATE SCHEMA ${schema}" >/dev/null
    else
        if ! command -v psql >/dev/null 2>&1; then
            echo "psql is required with an externally supplied GIZWAY_TEST_POSTGRES_DSN" >&2
            exit 1
        fi
        psql "${GIZWAY_TEST_POSTGRES_DSN}" --set ON_ERROR_STOP=1 \
            --command "CREATE SCHEMA ${schema}" >/dev/null
    fi
}

drop_test_postgresql_schema() {
    schema="$1"
    if [ -n "${test_postgresql_container}" ]; then
        docker exec "${test_postgresql_container}" psql --username postgres --dbname gizway_test \
            --set ON_ERROR_STOP=1 --command "DROP SCHEMA IF EXISTS ${schema} CASCADE" >/dev/null
    else
        psql "${GIZWAY_TEST_POSTGRES_DSN}" --set ON_ERROR_STOP=1 \
            --command "DROP SCHEMA IF EXISTS ${schema} CASCADE" >/dev/null
    fi
}

test_postgresql_schema_dsn() {
    schema="$1"
    case "${GIZWAY_TEST_POSTGRES_DSN}" in
        postgres://*|postgresql://*)
            case "${GIZWAY_TEST_POSTGRES_DSN}" in
                *\?*) printf '%s&search_path=%s\n' "${GIZWAY_TEST_POSTGRES_DSN}" "${schema}" ;;
                *) printf '%s?search_path=%s\n' "${GIZWAY_TEST_POSTGRES_DSN}" "${schema}" ;;
            esac
            ;;
        *) printf '%s search_path=%s\n' "${GIZWAY_TEST_POSTGRES_DSN}" "${schema}" ;;
    esac
}

stop_test_postgresql() {
    if [ "${test_postgresql_owned}" = true ] && [ -n "${test_postgresql_container}" ]; then
        docker stop "${test_postgresql_container}" >/dev/null 2>&1 || true
        test_postgresql_container=""
        GIZWAY_TEST_POSTGRES_CONTAINER=""
        export GIZWAY_TEST_POSTGRES_CONTAINER
    fi
}
