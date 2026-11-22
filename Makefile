.PHONY: build vet test test-gomod test-services test-smoke test-smoke-binary
build:
	go build ./...
vet:
	go vet ./...
test:
	go test -race ./...
# The go.sum, go.work and vendor/ refresh paths end to end (fake GitHub, local module proxy, real git).
test-gomod:
	go test -race ./internal/e2e -run 'TestMinorBump|TestMajor'
	go test -race ./internal/manager ./internal/platform/github ./internal/engine
# Starts Postgres and DynamoDB Local itself (Docker, or Apple `container` when Docker has no daemon),
# runs every test with RIPPLE_REQUIRE_SERVICES=1 so a skipped service test fails, then removes them.
test-services:
	scripts/test-services.sh
# Drives the built image over HTTP against a fake GitHub: one signed push event, exactly one PR.
# Needs a runtime with --network host (Docker on Linux): ripple accepts plain http only for loopback.
# `make test-smoke-binary` runs the same test against a native binary where that is unavailable.
test-smoke:
	docker build -t ripple:smoke .
	RIPPLE_SMOKE_IMAGE=ripple:smoke go test -count=1 -v ./internal/smoke/
test-smoke-binary:
	go build -o bin/ripple ./cmd/ripple
	RIPPLE_SMOKE_BINARY=$(CURDIR)/bin/ripple go test -count=1 -v ./internal/smoke/
