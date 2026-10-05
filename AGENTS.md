# Phase-one engineering contract

## Package seams

- Root `stuntgres`: database/sql driver, connectors, connections, statements, rows, results and native Reset/Dump/Restore APIs. `NewDriver() *Driver` implements DriverContext; default registration is `stuntgres`.
- `internal/parser`: handwritten PostgreSQL-oriented lexer, goyacc grammar/checked-in generated parser and closed CREATE/INSERT/SELECT/SET/RESET/SHOW/setting-call AST. Parse entire input, never execute a prefix while ignoring a suffix. Source positions and parameter indices belong in AST, not SQL substring counting. Ordinary-string mode and command context are per-call options, never globals.
- `internal/engine`: synchronized process-local catalog, immutable committed values, statement-atomic execution, parameter inference, snapshots and lifecycle publication.
- `internal/sqlstate`: shared error contract; root Error aliases it. SQLState is the compatibility interface, not concrete pq.Error.
- `smoke/*_test.go`: real public API proof; production imports are standard library/module only.

Production must have zero external dependencies. goyacc is generation-only; lib/pq, sqlmw and testcontainers stay in tests. Keep engine/parser independent of root adapters. Reuse these seams rather than introduce alternative binders or storage paths.

## Supported surface and limits

Only unqualified CREATE TABLE with INTEGER/INT/INT4, BIGINT/INT8, TEXT, BOOLEAN/BOOL and optional NULL/NOT NULL; INSERT VALUES with optional target columns/multiple rows; SELECT star or scalar literal/unqualified-column/parameter targets with its existing FROM, optional existing column=literal/parameter predicate or bare scalar WHERE in contextual BOOL, and one ORDER BY key ASC/DESC. No no-FROM scalar expansion or equality projection. Values are signed-64 integers, standard quoted strings, TRUE/FALSE/NULL and 1-based `$n`. Quoted identifiers, nested comments, doubled quotes/apostrophes and newline string concatenation follow README's C-locale UTF8 subset. Unresolved output parameter occurrences fall back to TEXT after filter analysis, retaining their initially known type: a target-first unknown occurrence conflicts with a later non-TEXT constraint (`42P08`), while TEXT target/filter reuse works. Same-context repeats work, unused gaps fail, mixed assignment destinations remain unsupported. Text/parameter inputs coerce through type input functions; INSERT assignment casts and WHERE operator checks differ. NULL equality is unknown and is not true; WHERE keeps only TRUE. ASC NULLS LAST/DESC NULLS FIRST remain.

Do not widen syntax beyond the documented selected surface: no SQL transaction controls/savepoints, aliases, general casts/joins/functions/expressions, relation defaults, serial, primary keys, UPDATE/DELETE/RETURNING or extra clauses. Driver BeginTx/Commit/Rollback supports READ COMMITTED baseline INSERT/SELECT only; explicit CREATE, role/session-authorization utilities and SET/RESET/SHOW/setting calls remain native unsupported boundaries. Session settings use the shared registry/binder and catalog-resolved sqltype descriptors/values. Driver.RegisterSetting publishes copied dotted typed definitions before first Engine construction, then freezes. Six honest builtins are application_name, client_min_messages with notice filtering, standard_conforming_strings, statement_timeout, read-only UTF8 server_encoding and deny-only log_statement_stats. Settings add no privileged authority, transaction-local restoration or file/process effects; preserve existing role/ACL capabilities. See README for units, bounds, diagnostics and physical-session behavior. Recognized unsupported features fail `0A000`; malformed supported syntax fails `42601`. Commands accept at most one statement except Restore. Caller context cancellation stays ordinary; only own setting timers return 57014. Failed multirow INSERT must not partially publish data.

DSNs are exact opaque process-local names, including empty string, scoped to a Driver. Different strings/Driver instances isolate data. Closing pools/connections does not erase an engine; no persistence or cross-process sharing. Reset keeps identity and atomically clears; Dump emits deterministic plain CREATE/INSERT SQL; Restore stages and atomically replaces without merging. Controls require native pools, reject wrapped/foreign drivers and nil inputs, and preserve data on errors. Snapshots remain usable after Reset/Restore. Never hold target locks during external reader/writer I/O; blocking I/O cannot be forcibly canceled.

## Commands and proof

Run from repository root with Go >=1.26:

```sh
make generate
make test
make reference
make smoke
make bench
go test -race ./...
go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./...
```

Make commands expand respectively to `go generate ./internal/parser`, `go test ./...`, `go test -tags reference -run TestReference -count=1 -v ./reference`, `go test -count=1 -v ./smoke`, and `go test -run '^$' -bench '^BenchmarkOpenDatabase$' -benchmem -count=5 .`. Generation uses `golang.org/x/tools/cmd/goyacc@v0.50.0`; checked-in output must compile. Production dependency output may contain only this module's paths. Benchmark actual fresh engines through Ping and Close, not merely lazy sql.Open; report ns/op and allocations against <1ms target.

Tests must specify expected outcomes independently, not parity alone. Cover consumer-visible boundaries, errors, atomicity, parameter direct/prepared behavior, cursor metadata/cancellation/EOF, shared pools and isolated instances, and destructive-control round trips/failures. Public smoke assertions run as unit tests in `smoke/*_test.go`, with failures and success reported by Go test rather than printed success labels. Do not replace actual behavior with mocks or test source text/wiring counts.

Reference tests live in the dedicated `reference/` external test package gated by `reference`, using only the public stuntgres API. `reference/fixture_test.go` owns shared setup and helpers; functional groups live in separate test files of at most 750 lines each. One bounded-lifecycle PostgreSQL 18.0 container serves serialized fixtures; pin README's image digest and UTF8/C locale. Reset public schema before/after fixtures with privileged unwrapped pq; close case handles/rows first. Use sqlmw wrapping memory and pq for execution, record through context on both direct/prepared hooks and own copied row buffers, retaining a native memory pool for controls. Do not compare implementation call counts. Docker/image failures in an explicitly enabled suite must be visible, never skipped or mocked.
The cohesive common `reference/fixture_test.go` is exempt from the 750-line limit. Keep reference tests independent of root-private test helpers and internal stuntgres packages; public driver, error, notice and native control APIs are allowed.

Authoritative references: [PostgreSQL 18 SQL](https://www.postgresql.org/docs/18/sql-commands.html), [SQLSTATE](https://www.postgresql.org/docs/18/errcodes-appendix.html), [testcontainers PostgreSQL](https://golang.testcontainers.org/modules/postgres/), [pinned sqlmw](https://github.com/ngrok/sqlmw/blob/97c9c04efc79/interceptor.go). Local 20devel parser/coercion sources are research aids, not the release oracle. See README for consumer contract and `.cswd/commands/verify-phase-one.md` for a plain-Markdown verification prompt.
