VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

# The web UI is embedded from internal/ui/dist (ADR 0029). dist is not
# committed, so without it Go builds with the noui tag and a stub page, and a
# fresh clone needs no Node. Run `make ui-build` first to embed the real UI.
UI_DIST := internal/ui/dist/index.html
GOTAGS = $(shell test -f $(UI_DIST) || echo -tags noui)
# Not ./...: that would walk web/node_modules, where npm packages may ship Go.
PKGS := ./api/... ./cmd/... ./internal/...

.PHONY: build test test-race e2e lint check-imports check-openapi vet goldens ui-build ui-dev ui-check ui-gen

build:
	CGO_ENABLED=0 go build $(GOTAGS) -trimpath -ldflags "$(LDFLAGS)" -o bin/spyglass ./cmd/spyglass

vet:
	go vet $(GOTAGS) $(PKGS)

test:
	go test $(GOTAGS) $(PKGS)

# The same tests under the race detector. The concurrency tests (config swap,
# recorder, store) can only fail with it. Needs cgo and a C compiler.
test-race:
	go test -race $(GOTAGS) $(PKGS)

# The end-to-end tests (ADR 0038): the real wiring against a fake Anthropic
# upstream and a test Postgres. They need Docker or SPYGLASS_TEST_DATABASE_URL
# and skip without either. `make test` runs them too when Postgres is there.
e2e:
	go test $(GOTAGS) -count=1 -run TestE2E ./cmd/spyglass

lint:
	golangci-lint run $(if $(GOTAGS),--build-tags noui) $(PKGS)

check-imports:
	golangci-lint run $(if $(GOTAGS),--build-tags noui) --enable-only depguard $(PKGS)

# Build the web UI and copy it to internal/ui/dist, where `make build` embeds
# it. Needs Node 22 or later.
ui-build:
	cd web && npm ci && npm run build
	rm -rf internal/ui/dist
	cp -R web/dist internal/ui/dist

# Run the Vite dev server with hot reload. It proxies /admin to a local
# `spyglass serve` on localhost:9090.
ui-dev:
	cd web && npm ci && npm run dev

# Regenerate web/src/api/schema.gen.ts from api/openapi.yaml (ADR 0030). Commit
# the result. The container build does not run it: it copies only web/.
ui-gen:
	cd web && npm ci && npm run gen:api

# Lint, type-check and unit-test the web UI, and check that the committed
# generated API types match api/openapi.yaml. CI runs it.
ui-check:
	cd web && npm ci && npm run gen:api && git diff --exit-code -- src/api/schema.gen.ts
	cd web && npm run lint && npm run typecheck && npm test

# Lint the OpenAPI document with a pinned linter. Needs Node; CI runs it.
check-openapi:
	npx --yes @redocly/cli@1.34.5 lint --config api/redocly.yaml api/openapi.yaml

# Rewrite the golden call records under testdata/fixtures. Read the diff.
goldens:
	go test ./internal/family/anthropic -run 'TestFixtureGoldens|TestScannerGolden' -update
