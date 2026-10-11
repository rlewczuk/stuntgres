.PHONY: generate build quickcheck racecheck finalcheck refcheck check bench deps

# Only check accepts options. Require a selector to avoid accidental broad runs.
RUN ?=
REFERENCE ?= 0
RACE ?= 0

# Regenerate checked-in parser and Unicode tables after generator/input changes.
generate:
	go generate ./internal/parser
	go run ./internal/sqltype/unicode16/generate -source ./internal/sqltype/unicode16/data -output ./internal/sqltype/unicode16/tables_generated.go -case-output ./internal/sqltype/unicode16/case_tables_generated.go -assigned-output ./internal/sqltype/unicode16/assigned_tables_generated.go -regex-output ./internal/sqltype/unicode16/regex_tables_generated.go

# Compile production packages.
build:
	go build ./...

# Full-project verification: uncached units, legacy smoke and quick reference tests.
# Fixed quick mode; use check for development selections, not this target.
# -timeout=0 disables Go's implicit deadline; callers own command timeouts.
quickcheck:
	go generate ./internal/parser
	go run ./internal/sqltype/unicode16/generate -source ./internal/sqltype/unicode16/data -output ./internal/sqltype/unicode16/tables_generated.go -case-output ./internal/sqltype/unicode16/case_tables_generated.go -assigned-output ./internal/sqltype/unicode16/assigned_tables_generated.go -regex-output ./internal/sqltype/unicode16/regex_tables_generated.go
	go test -count=1 -timeout=0 ./...

# Full quick-mode suite with the race detector; no PostgreSQL container.
racecheck:
	go generate ./internal/parser
	go run ./internal/sqltype/unicode16/generate -source ./internal/sqltype/unicode16/data -output ./internal/sqltype/unicode16/tables_generated.go -case-output ./internal/sqltype/unicode16/case_tables_generated.go -assigned-output ./internal/sqltype/unicode16/assigned_tables_generated.go -regex-output ./internal/sqltype/unicode16/regex_tables_generated.go
	go test -count=1 -timeout=0 -race ./...

# Post-integration gates: five startup benchmarks, full quick-mode race suite, deps.
finalcheck:
	go generate ./internal/parser
	go run ./internal/sqltype/unicode16/generate -source ./internal/sqltype/unicode16/data -output ./internal/sqltype/unicode16/tables_generated.go -case-output ./internal/sqltype/unicode16/case_tables_generated.go -assigned-output ./internal/sqltype/unicode16/assigned_tables_generated.go -regex-output ./internal/sqltype/unicode16/regex_tables_generated.go
	go test -timeout=0 -run '^$$' -bench '^BenchmarkOpenDatabase$$' -benchmem -count=5 .
	go test -count=1 -timeout=0 -race ./...
	go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./...

# Human-only complete real PostgreSQL reference suite. Agents MUST NEVER call this
# target under any circumstances: it is too slow. Use selected check REFERENCE=1.
refcheck:
	go generate ./internal/parser
	go run ./internal/sqltype/unicode16/generate -source ./internal/sqltype/unicode16/data -output ./internal/sqltype/unicode16/tables_generated.go -case-output ./internal/sqltype/unicode16/case_tables_generated.go -assigned-output ./internal/sqltype/unicode16/assigned_tables_generated.go -regex-output ./internal/sqltype/unicode16/regex_tables_generated.go
	go test -count=1 -timeout=0 -tags reference ./reference -args -reference=true

# Development ONLY: individual tests or small selected sets, never broad/package
# checks. RUN is required; REFERENCE=1 enables real PostgreSQL, RACE=1 enables race.
check:
	$(if $(strip $(RUN)),,$(error check requires RUN selecting individual tests or a small test set))
	go generate ./internal/parser
	go run ./internal/sqltype/unicode16/generate -source ./internal/sqltype/unicode16/data -output ./internal/sqltype/unicode16/tables_generated.go -case-output ./internal/sqltype/unicode16/case_tables_generated.go -assigned-output ./internal/sqltype/unicode16/assigned_tables_generated.go -regex-output ./internal/sqltype/unicode16/regex_tables_generated.go
	go test -count=1 -timeout=0 -run '$(RUN)' $(if $(filter 1,$(RACE)),-race) $(if $(filter 1,$(REFERENCE)),-tags reference) $(if $(filter 1,$(REFERENCE)),./reference,./...) $(if $(filter 1,$(REFERENCE)),-args -reference=true)

# Measure fresh-engine Ping/Close startup: five runs with allocation counts.
bench:
	go test -timeout=0 -run '^$$' -bench '^BenchmarkOpenDatabase$$' -benchmem -count=5 .

# List non-standard production dependencies; only this module's paths may appear.
deps:
	go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./...
