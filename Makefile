.PHONY: generate build quickcheck fullcheck bench deps

# Test deadlines are fixed. GNU timeout bounds the whole invocation, including
# compilation and all packages; Go's timeout supplies per-package diagnostics.
RUN ?=
PACKAGES ?= ./...
VERBOSE ?= 0
REFERENCE ?= 0
RACE ?= 0
TEST_FLAGS = -count=1 -timeout=900s $(if $(filter 1,$(VERBOSE)),-v) $(if $(RUN),-run '$(RUN)')
TEST_COMMAND = timeout --signal=KILL 900s go test $(TEST_FLAGS)

generate:
	go generate ./internal/parser

build:
	go build ./...

# Ordinary units, legacy smoke and untagged reference scenarios, without Docker.
# REFERENCE=1 enables the pinned PostgreSQL fixture for selective dual-mode checks.
quickcheck:
	@$(TEST_COMMAND) $(if $(filter 1,$(RACE)),-race) $(if $(filter 1,$(REFERENCE)),-tags reference) $(PACKAGES) $(if $(filter 1,$(REFERENCE)),-args -reference=true)

# Post-integration production gates share one 30-minute deadline.
# Clear selection overrides so final verification always covers the full suites.
fullcheck:
	@timeout --signal=KILL 1800s sh -c '$(MAKE) -j1 generate build quickcheck RUN= PACKAGES=./... REFERENCE=0 RACE=0 && $(MAKE) -j1 quickcheck RUN= PACKAGES=./... REFERENCE=0 RACE=1 && $(MAKE) -j1 quickcheck RUN= PACKAGES=./reference REFERENCE=1 RACE=1 && $(MAKE) -j1 bench deps && module=$$(go list -m) && dependencies=$$(go list -deps -f "{{if not .Standard}}{{.ImportPath}}{{end}}" ./...) && for dependency in $$dependencies; do case "$$dependency" in "$$module"|"$$module"/*) ;; *) printf "Non-standard production dependency: %s\n" "$$dependency" >&2; exit 1 ;; esac; done'

bench:
	@timeout --signal=KILL 900s go test -timeout=900s -run '^$$' -bench '^BenchmarkOpenDatabase$$' -benchmem -count=5 .

deps:
	go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./...
