# Development and verification

[README](../README.md) · [SQL dialect and limits](DIALECT.md) · [SQL reference](SQL.md)

```sh
make generate   # pinned goyacc; checked-in parser generation
make build      # compile production packages
make test       # uncached Docker-free tests, including smoke and dual-mode reference
make reference  # real pinned PostgreSQL differential fixtures
make smoke      # uncached public database/sql and controls smoke tests
make race       # Docker-free tests with the race detector
make test-all   # quick + full reference race suites under one aggregate deadline
make bench      # fresh engine + connector + pool + Ping + Close, five runs
make deps       # list non-standard production dependencies
```

Test targets enforce a fixed 900-second wall-clock deadline across all packages,
including compilation, plus a per-package timeout. `make test-all` chains ordinary
native/smoke/quick race tests and the full tagged reference race package under one
outer deadline; slow or hanging tests must be fixed, not exempted.
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

### Dual-mode query reads and legacy transition

The ordinary external `reference/` binary discovers 80 untagged query-read
definitions migrated/merged from 67 original SQL-interaction tests. The families
are `TestReferenceNullCount*`, `TestReferenceQueryAggregate*`,
`TestReferenceCountExpression*`, `TestReferenceQueryCore*`,
`TestReferenceQueryJoins*`, `TestReferenceQueryLimit*` and
`TestReferenceQueryOrder*`. These include cohesive boundary, consumer, metadata
and lifecycle splits, plus the existing reference-only complements that belong
to the migrated definitions. Select the complete query-read family in both modes:

```sh
make test PACKAGES=./reference RUN='^TestReference(NullCount[A-Za-z0-9_]*|QueryAggregate[A-Za-z0-9_]*|CountExpression[A-Za-z0-9_]*|QueryCore[A-Za-z0-9_]*|QueryJoins[A-Za-z0-9_]*|QueryLimit[A-Za-z0-9_]*|QueryOrder[A-Za-z0-9_]*)$' VERBOSE=0
make reference RUN='^TestReference(NullCount[A-Za-z0-9_]*|QueryAggregate[A-Za-z0-9_]*|CountExpression[A-Za-z0-9_]*|QueryCore[A-Za-z0-9_]*|QueryJoins[A-Za-z0-9_]*|QueryLimit[A-Za-z0-9_]*|QueryOrder[A-Za-z0-9_]*)$' VERBOSE=0
```

The original query projection/bags/metadata and ORDER BY position pilots are
included in the query-read family. The BYTEA and short-INSERT pilots,
`TestReferenceByteaInputBindsMetadata` and
`TestReferenceInsertImplicitShortDiagnostics`, remain available alongside it.

It also discovers the migrated INSERT SELECT, UPDATE and DELETE/RETURNING
families. Their 61 former root interaction tests are merged with existing
reference assertions and split into cohesive definitions of at most 150 lines.
Original raw `Scan(&any)` representations are checked before value normalization,
including native `int64` and string outputs, SQL NULL, owned BYTEA and nonnil empty
bytes. PostgreSQL codec expectations are independent: pq returns actual `int64`
integer cells even when INT4 ScanType is `int32`, and NUMERIC/UUID cells are byte
slices rather than native strings. Retained cursors obey the same raw contract;
normalized complete rows and owned middleware recordings remain checked afterward.
Select every resulting DML definition, including retained tagged oracle edges:

```sh
make test PACKAGES=./reference RUN='^TestReference(InsertSelect[A-Za-z0-9_]*|UpdateCore[A-Za-z0-9_]*|DeleteCore[A-Za-z0-9_]*|DeleteReturning[A-Za-z0-9_]*)$' VERBOSE=0
make reference RUN='^TestReference(InsertSelect[A-Za-z0-9_]*|UpdateCore[A-Za-z0-9_]*|DeleteCore[A-Za-z0-9_]*|DeleteReturning[A-Za-z0-9_]*)$' VERBOSE=0
```

Their single definitions run the real public stuntgres `database/sql` adapter
against independently hardcoded expectations by default. The explicit boolean
test-binary flag adds real PostgreSQL; the build tag is not the mode selector:

```sh
go test ./reference
go test ./reference -args -reference=false
go test ./reference -args -reference=true
go test -tags reference ./reference -args -reference=true
```

Absent/false quick mode does not contact Docker, fetch an image, open PostgreSQL
or read server state. Explicit true starts the pinned oracle before test execution,
even if selection matches no tests; startup/configuration errors fail visibly,
never skip or fall back. Only `./reference` accepts this custom flag: do not pass
it to unrelated package binaries. `make test` and `make race` discover migrated
quick scenarios normally. `make reference` selects true and includes every
legacy tagged case. `make test-all` first runs `go test -race ./...`, then
`go test -race -tags reference ./reference -args -reference=true`, with uncached
test flags and one shared 900-second wall-clock bound.

The tag retains untouched legacy PostgreSQL-only definitions. A tagged selection
restricted to mode-aware migrated scenarios with `-reference=false` (flag after
`-args`) is still Docker-free; selecting actual legacy cases lazily starts the
same singleton even when the runtime flag is absent/false. The tag alone
therefore does not make the whole legacy suite quick. Other unmigrated families,
richer tagged ordering/lock complements, the standalone PostgreSQL count
column-ACL test and DML rules/triggers, privilege and exact blocker/server-
cancellation edges remain tagged. Root `queryCoreRows`, `nullCountError` and
`queryJoinsPublicFixture` remain for unmigrated callers, not as SQL test bodies.
Untagged migrated definitions use the mode-aware fixture; host Dump/Reset/Restore
and raw native cursor phases use its unwrapped native pool, with independently
specified SQL consumer phases on PostgreSQL.

For example, these tagged legacy join diagnostics explicitly own a real pair
even with the migrated runtime mode disabled. This command requires Docker:

```sh
go test -tags reference -count=1 -run '^TestReferenceQueryJoins(Diagnostics|DiagnosticsOracle|ScopeDiagnostics|ScopeDiagnosticsOracle)$' ./reference -args -reference=false
```

Their shared SQL seed is reused without making migrated quick fixtures start
PostgreSQL. Keep this legacy selection separate from Docker-unavailable quick
mode checks.

Each selected backend independently asserts exact rows, column labels/type names
including zero-row results, NULLs, duplicate-preserving bags versus ordered rows,
direct/prepared parameters and affected counts. Diagnostics retain full English
logical fields, UTF8 character positions and Prepare-versus-Execute stages.
Backend differences are asserted before parity: native parameter analysis and
PostgreSQL simple-protocol zero-argument errors are distinct, and the original
native `count(1/0)` Prepare-or-Execute tolerance does not impose PG stage parity.
Raw null-count payloads/ScanType/sums, LIMIT precision/length/nullability,
zero-column occurrence counts and merged-view typmods are checked before
normalization. Held statements are actually reused; sqlmw checks consumed
outputs and closure. Joined BYTEA scans retain independent owned bytes on both
backends; detached `Rows.Columns` slices are a native guarantee, not a lib/pq
copy promise. Native unwrapped Dump/Reset/Restore phases retain snapshots and
pristine restored sequence state; actual PostgreSQL identities/waiters prove
the PG-only lock/refill complements. No unsupported native success is invented.

The BYTEA/short-INSERT pilots retain their independent unnamed-portal parameter
Context and failed-INSERT sentinel checks. BYTEA also checks raw ScanType,
known/unknown Length/Nullable metadata, NULL versus nonnil empty values and owned
recorded arguments/consumed buffers before normalization; the focused byte-copy
regression and existing row comparator continue to exercise those helper seams.

For a single migrated lifecycle definition, use the same exact name in both modes:

```sh
make test PACKAGES=./reference RUN='^TestReferenceQueryJoinsExportAndSchemaOwnership$' VERBOSE=0
make reference RUN='^TestReferenceQueryJoinsExportAndSchemaOwnership$' VERBOSE=0
```

Mode-aware pools use a fresh native driver per fixture; wrapped and native control
pools share that driver/DSN without exposing controls through middleware. Cleanup
closes rows before statements and all pools before schema reset. PostgreSQL
lifecycle, owner provisioning and administrative-session safeguards below remain
shared by eager migrated and lazy legacy startup.
Failed eager or lazy startup releases any acquired administrative pool/container
before returning the cached error, with bounded termination and visible cleanup
errors. Legacy administrative probes require successful startup; registered
role/schema cleanup runs only after initialization succeeds.

Some researched cases cannot be forced into identical static row snapshots:

| Retained case | Concrete fit boundary |
| --- | --- |
| Physical sequence catalog/cache probes | Server `log_cnt=22` versus native logical `log_cnt=0` would assert nonexistent native WAL/cache durability. |
| Lock/blocker probes | OID/PID/`pg_locks`/`pg_blocking_pids` relationships require locally captured server identities and bounded concurrency, not portable fixed rows. |
| Random draws | Independent range/reseed witnesses fit selected backend scenarios; identical random streams are not a portable expectation. |
| Native Dump followed by PG replay | Requires an unwrapped native control phase and a real PostgreSQL replay phase, not a query-only snapshot. |
| PostgreSQL-only `INHERITS` | Outside the bounded native dialect; retain the real server assertion without inventing native success. |

These are concrete boundaries of static snapshot comparison, not blanket
exceptions to future shared scenarios. No extra migration or engine/oracle
surface change is implied.


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
