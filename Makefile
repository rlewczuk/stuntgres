.PHONY: generate build test reference smoke race test-all bench deps

# Test deadlines are fixed. GNU timeout bounds the whole invocation, including
# compilation and all packages; Go's timeout supplies per-package diagnostics.
RUN ?=
PACKAGES ?= ./...
VERBOSE ?= 0
TEST_FLAGS = -count=1 -timeout=60s $(if $(filter 1,$(VERBOSE)),-v) $(if $(RUN),-run '$(RUN)')
TEST_COMMAND = timeout --signal=KILL 60s go test $(TEST_FLAGS)

generate:
	go generate ./internal/parser

build:
	go build ./...

test:
	@$(TEST_COMMAND) $(PACKAGES)

reference:
	@$(TEST_COMMAND) -tags reference $(if $(RUN),,-run TestReference) ./reference

smoke:
	@$(TEST_COMMAND) ./smoke

race:
	@$(TEST_COMMAND) -race $(PACKAGES)

# One invocation includes smoke and PostgreSQL coverage, all under the detector.
test-all:
	@$(TEST_COMMAND) -race -tags reference ./...

bench:
	@timeout --signal=KILL 60s go test -timeout=60s -run '^$$' -bench '^BenchmarkOpenDatabase$$' -benchmem -count=5 .

deps:
	go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./...
