# Development and verification

[README](../README.md) · [SQL dialect and limits](DIALECT.md) · [SQL reference](SQL.md)

```sh
make generate   # pinned goyacc; checked-in parser generation
make build      # compile production packages
make test       # uncached Docker-free tests, including smoke
make reference  # real pinned PostgreSQL differential fixtures
make smoke      # uncached public database/sql and controls smoke tests
make race       # Docker-free tests with the race detector
make test-all   # native, smoke and reference tests with race detection in one run
make bench      # fresh engine + connector + pool + Ping + Close, five runs
make deps       # list non-standard production dependencies
```

Test targets enforce a fixed 60-second wall-clock deadline across all packages,
including compilation, plus a per-package timeout. `make test-all` runs all suites
together within that deadline; slow or hanging tests must be fixed, not exempted.
Output is quiet by default; use `VERBOSE=1` for test diagnostics, `RUN='^TestName$'`
for selection, and `PACKAGES=./internal/parser` to scope ordinary/race tests.
Read `Makefile` for non-standard actions. Only specialized, explicitly enabled
benchmarks may exceed the test budget; the default startup benchmark is bounded too.

Every nonblank production dependency entry must be `stuntgres` or start with `stuntgres/`. goyacc is a generation tool, not a runtime dependency. lib/pq, sqlmw and testcontainers are test-only. Benchmark startup target is below 1,000,000 ns/op; report observed allocations and timings, not a timing-sensitive test assertion.

The public smoke tests live in `smoke/*_test.go` and are also part of `make test`.
`make smoke` runs the entire package, uncached and quiet unless `VERBOSE=1`.
The baseline `TestSmoke` verifies rows, metadata, affected counts,
Dump/Reset/Restore and a second already-open pool. Run the catalog witness with
`make smoke RUN='^TestCatalogOwnersPublicWitness$'`.
Go test reports each test's pass or failure instead of standalone output labels.

The explicitly enabled reference suite requires working Docker and image/module access. It uses one PostgreSQL 18.0 container pinned to `postgres:18.0-bookworm@sha256:3f55f8895c4ed50603e2fbdfc72fffeeaba3173321fee5cb825bbbeb30d9d854`, UTF8/C locale, serialized fixtures and public-schema resets. sqlmw records both direct and prepared paths; native memory handles remain available for controls. Fixtures assert independently specified outcomes in addition to parity. Reference startup failures are errors, not silent skips or mock fallbacks.

The shared fixture provisions native bootstrap owner `postgres` as a separate
NOLOGIN superuser for the container lifetime, with bounded startup and cleanup.
PostgreSQL callers still authenticate as `stuntgres`; native callers retain
`postgres`. Dump replay executes the unchanged owner-bearing stream, including
its first explicit owner directive, so schema/table/sequence owners are not
silently remapped. Pooled administrative session state is discarded before
schema resets. Sequence queue witnesses observe the captured local OID and
actual blocker, not a hard-coded relation name or sleep-based timing.

Reference tests live in the dedicated `reference/` external test package and use only the public stuntgres API. Shared setup and helpers belong in `reference/fixture_test.go`; functional groups belong in separate test files of at most 750 lines each. `make reference` runs these fixtures uncached, with `VERBOSE=1` available for diagnostics.
The cohesive common fixture is exempt from the 750-line limit; it keeps container lifecycle, diagnostic assertions, notice connections and middleware recording together.

Implicit INSERT and UPDATE diagnostic fixtures independently specify preparation versus execution failure stages, not just backend agreement. Native engine tests also use test-owned contexts to deterministically cancel after row staging and immediately before publication, checking complete sentinel preservation and subsequent engine usability without sleeps or production hooks.

Authoritative references: [PostgreSQL 18 commands](https://www.postgresql.org/docs/18/sql-commands.html), [SQLSTATE codes](https://www.postgresql.org/docs/18/errcodes-appendix.html), [testcontainers PostgreSQL API](https://golang.testcontainers.org/modules/postgres/), and [pinned sqlmw APIs](https://github.com/ngrok/sqlmw/blob/97c9c04efc79/interceptor.go). Local PostgreSQL source is 20devel and useful for implementation research; the pinned PostgreSQL 18.0 runtime is the compatibility oracle.
