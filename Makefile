.DEFAULT_GOAL := help

GO ?= go
HURLFMT ?= hurlfmt
ACTIONLINT ?= actionlint
NPM_DIST_TAG ?=
export NPM_DIST_TAG

.NOTPARALLEL: fmt lint test test-unit

.PHONY: help \
	fmt fmt-go fmt-hurl fmt-module \
	lint lint-go lint-actions \
	verify-modules build \
	build-images publish-npm test-release-images test-package-gizway-sdk \
	test test-unit test-unit-go test-unit-go-race test-unit-api test-unit-postgresql \
	test-unit-sdk test-unit-powersync test-unit-gizway-sdk test-e2e test-e2e-sdk test-e2e-powersync test-e2e-gizway-sdk

help:
	@printf '%s\n' \
		'Gizway' \
		'' \
		'Usage: make <target>' \
		'' \
		'Formatting:' \
		'  fmt                 run every source-format check' \
		'  fmt-go              require gofmt-clean Go source' \
		'  fmt-hurl            require hurlfmt-clean executable API stories' \
		'  fmt-module          require go.mod and go.sum to be tidy' \
		'' \
		'Static analysis:' \
		'  lint                run every language and workflow lint check' \
		'  lint-go             run vet, modernize, staticcheck, and govulncheck' \
		'  lint-actions        validate GitHub Actions workflows' \
		'' \
		'Build and tests:' \
		'  build               compile every Go package and command' \
		'  test                run the complete local unit-test gate' \
		'  test-unit           run every local test without real external services' \
		'  test-unit-go        run Go tests and enforce the current instrumented coverage floor' \
		'  test-unit-go-race   run all Go tests with the race detector' \
		'  test-unit-api       validate contracts and run fake-provider Hurl stories' \
		'  test-unit-postgresql run production-dialect tests on local PostgreSQL' \
		'  test-unit-sdk       compile the pinned official SDK test module' \
		'  test-unit-powersync typecheck and run offline PowerSync client contracts' \
		'  test-unit-gizway-sdk run GizWay SDK type, lint, unit, build, and package gates' \
		'  test-e2e            run Milestone 03 Compose acceptance scenarios' \
		'  test-e2e-sdk        run the official SDK compatibility matrix' \
		'  test-e2e-powersync  run the PowerSync Client SDK acceptance matrix' \
		'  test-e2e-gizway-sdk run real Global/CN GizWay SDK acceptance' \
		'  build-images        build six linux/amd64 production OCI layouts' \
		'  publish-npm         publish @gizclaw/gizway to GitHub Packages' \
		'  test-package-gizway-sdk validate direct gizway package and OCI release separation' \
		'  test-release-images validate tags and inspect/smoke the built OCI layouts'

fmt: fmt-go fmt-hurl fmt-module

fmt-go:
	@unformatted="$$(find . \
		\( -path './.git' -o -path './.coverage' -o -path './tmp' \) -prune -o \
		-type f -name '*.go' -exec gofmt -l {} +)"; \
	if [ -n "$$unformatted" ]; then \
		printf 'Go files require gofmt:\n%s\n' "$$unformatted" >&2; \
		exit 1; \
	fi

fmt-hurl:
	@find tests -type f -name '*.hurl' -print0 | xargs -0 $(HURLFMT) --check

fmt-module:
	@$(GO) mod tidy -diff

lint: lint-go lint-actions

lint-go:
	@GO='$(GO)' ./scripts/lint/lint-go.sh

lint-actions:
	@$(ACTIONLINT)

verify-modules:
	@$(GO) mod verify

build:
	@$(GO) build ./...

test: test-unit

test-unit:
	@GO='$(GO)' ./scripts/test-unit/test-unit.sh

test-unit-go:
	@GO='$(GO)' ./scripts/test-unit/test-unit-go.sh

test-unit-go-race:
	@GO='$(GO)' ./scripts/test-unit/test-unit-go-race.sh

test-unit-api:
	@GO='$(GO)' ./scripts/test-unit/test-unit-api.sh

test-unit-postgresql:
	@GO='$(GO)' ./scripts/test-unit/test-unit-postgresql.sh

test-unit-sdk:
	@cd tests/sdk && $(GO) test ./...

test-unit-powersync:
	@cd tests/powersync && npm run typecheck && npm test

test-unit-gizway-sdk:
	@./scripts/test-unit/test-unit-browser-sdk.sh

test-e2e:
	@./tests/e2e/run.sh all

test-e2e-sdk:
	@./tests/e2e/run.sh sdk

test-e2e-powersync:
	@./tests/e2e/run.sh powersync

test-e2e-gizway-sdk:
	@./tests/e2e/run.sh gizway-sdk

build-images:
	@./scripts/release/build-images.sh "$(RELEASE_VERSION)"

publish-npm:
	@cd sdk/web && \
		tag="$${NPM_DIST_TAG:-}"; \
		version="$$(node -p "require('./package.json').version")"; \
		case "$$tag" in \
			'') \
				case "$$version" in *-*) printf 'NPM_DIST_TAG must be a non-latest tag for prerelease version %s\n' "$$version" >&2; exit 2;; esac; \
				exec npm publish \
				;; \
			[A-Za-z]*) \
				case "$$tag" in *[!A-Za-z0-9._-]*) printf 'invalid NPM_DIST_TAG: %s\n' "$$tag" >&2; exit 2;; esac; \
				if [ "$$tag" = latest ]; then \
					case "$$version" in *-*) printf 'NPM_DIST_TAG must not be latest for prerelease version %s\n' "$$version" >&2; exit 2;; esac; \
				fi; \
				exec npm publish --tag "$$tag" \
				;; \
			*) printf 'invalid NPM_DIST_TAG: %s\n' "$$tag" >&2; exit 2;; \
		esac

test-release-images:
	@./tests/release/tag-validation.sh
	@./tests/release/build-images.sh
	@./tests/release/publish-images.sh
	@./scripts/release/verify-images.sh
	@./tests/release/smoke.sh

test-package-gizway-sdk:
	@./tests/release/web-sdk.sh
