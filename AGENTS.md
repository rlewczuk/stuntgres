# Agent development guide

## Navigation and package seams

Read [README.md](README.md) for the consumer contract and [docs/DIALECT.md](docs/DIALECT.md)
for SQL support, limits and intentional compatibility differences before changing behavior.
Design notes live in `docs/design/`; catalog-part workflow lives in `.cswd/`.

- Root `stuntgres`: public `database/sql` adapters, driver/connector, session settings, notices and native Reset/Dump/Restore APIs.
- `internal/parser`: AST and parser entry points in `ast.go`/`parser.go`, lexer in `lexer.go`, keyword categories in `keywords.go`. Edit `grammar.y` for grammar changes, then `make generate`; never hand-edit `grammar_gen.go`.
- `internal/engine`: catalog, authorization, binding/execution, transactions, relation locks, settings, snapshots and lifecycle publication.
- Sequence DDL/functions use relation descriptors, central ACLs and the existing transaction/lock/query paths. Generator state and physical-session memories are distinct from transactional sequence metadata; never add live catalog undo or per-CACHE arrays. TEMP sequence lifetime follows the physical session, not pooled borrowing.
- Prepared sequence literals use a private description analysis cell, a distinct relation plan stamp and a physical-session effective namespace cache. Reanalyze the entire original literal group at Execute start after dependency/path changes; preserve immutable temporal/signature/result contracts. RX remains demand-driven after once-only argument evaluation; grant-time opening retains the in-flight OID, with no expression retarget/replay. Filtered/zero-row and strict NULL calls do not open their sequence. Generator state and descriptor revision are not plan invalidation tokens. Native replacement remains fenced except exact caller-owned live temporary provenance.
- `internal/sqltype`: SQL type descriptors, input/output codecs, coercion and value comparison. Reuse these rather than adding adapter-local conversions.
- `internal/sqlstate`: shared SQL errors; root `Error` aliases it. Assert the SQLState interface, not concrete `pq.Error`.
- Root and internal `*_test.go`: public adapter and package-level behavioral regression tests.
- `smoke/`: real public API scenarios, including native controls; included in the ordinary test suite.
- `reference/`: build-tagged external tests against pinned PostgreSQL 18, using only public stuntgres APIs. Shared fixtures belong in `fixture_test.go`.

Production dependencies must be standard library or this module only. Keep internal packages
independent of root adapters. goyacc is generation-only; pq, sqlmw and testcontainers are test-only.
Reuse existing package seams, binders and storage paths; do not introduce parallel implementations.

## Development rules

- Plan every change first: identify affected seams, compatibility evidence, invariants and verification. Keep scope explicit; do not silently widen the dialect.
- Use TDD: write the failing behavioral test, observe the failure, implement the smallest correct change, then refactor with tests passing. Every discovered bug needs a unit regression before its fix; every compatibility bug also needs a reference regression.
- Prefer clear, self-explanatory, maintainable code with separated concerns and testable boundaries. Keep functions focused and files short; split by responsibility, not arbitrary line counts. Grammar and generated files are exceptions. Reference functional test files have a hard 750-line limit; the cohesive shared fixture is exempt.
- Test consumer-visible outcomes, boundaries, errors, atomicity and lifecycle transitions with independently specified expectations. Cover direct/prepared behavior and relevant metadata/cancellation paths. Do not test source text, wiring counts or mock echoes in place of real behavior.
- Keep test output quiet: assertions report failures through the test runner, not printed success labels. Extra debug output requires an explicit verbosity flag, off by default (`VERBOSE=1` for make targets).
- All tests together must finish within 900 seconds. `make test-all` enforces one wall-clock deadline across native, smoke, reference and race coverage; individual test targets are bounded too. Fix slow or hanging tests rather than increasing deadlines, dropping coverage or skipping failures. Only explicitly enabled specialized benchmarks may exceed this limit; never include them in default targets.
- Pay particular attention to mutexes, goroutines and cancellation. Document lock ownership/order, avoid recursive acquisition and callbacks/external I/O under locks, release resources on every exit, and preserve immutable published snapshots and atomic publication. Use deterministic synchronization rather than sleep-based ordering; exercise cancellation, contention and teardown with the race detector.
- Transaction isolation uses one owner: RR retains all immutable relation/end roots before analysis waits; RC/RU execution views stay grant-time fresh. Never copy row values for snapshot retention, cache an owner on prepared statements, or add a second settings/lock authority. Current characteristic routes validate owner-first; healthy CHAIN preserves final modes, abort restores entry defaults before overrides, and aborted CHAIN carries restored modes. Every chain clears snapshot state.
- Read-only utility execution rejects supported modifying tags before object resolution, authorization or relation locks; DML retains analysis-before-read-only diagnostics. Do not apply execution rejection to Prepare. Selected ALTER TABLE nullability/named-NOT-NULL/DROP COLUMN lists and sequence DDL are the bounded explicit-transaction exceptions; do not enable unrelated utilities. Independently assert SHOW ALL built-in descriptions against PostgreSQL 18.0 short_desc, including current/default transaction settings, and registered custom descriptions without empty-string exemptions.
- Keep parser state/options per call, retain source positions and parameter indices in the AST, and consume the entire input. Never execute a valid prefix while ignoring a suffix.
- Update affected callers and documentation with behavior changes; remove obsolete paths instead of leaving compatibility shims. Regenerate checked-in parser output after grammar changes and verify it builds.

## PostgreSQL compatibility and reference tests

PostgreSQL 18 behavior grounds every change; reference tests are the golden standard.
Resolve conflicting design requirements against real PostgreSQL behavior and encode the result
in reference tests, including independently asserted native capability boundaries where applicable.
Never loosen, disable or skip reference tests to make a change pass: only genuine test bugfixes
and additions are allowed. Record every discovered compatibility bug in reference coverage.

- Use the PostgreSQL 18.0 image digest pinned in [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) and UTF8/C locale. Local newer PostgreSQL sources are research aids, not the release oracle.
- Keep reference tests independent of internal packages and root-private test helpers. Use one bounded-lifecycle container and serialized fixtures; reset public schema before/after cases and close handles/rows first.
- Preserve sqlmw coverage of direct/prepared execution and owned row buffers; retain native pools for controls. Assert expected results independently, not parity or implementation call counts alone.
- Docker/image failures in an explicitly enabled reference run must fail visibly, never be skipped or mocked.

Primary references: [PostgreSQL 18 SQL](https://www.postgresql.org/docs/18/sql-commands.html),
[SQLSTATE](https://www.postgresql.org/docs/18/errcodes-appendix.html),
[testcontainers PostgreSQL](https://golang.testcontainers.org/modules/postgres/),
[pinned sqlmw](https://github.com/ngrok/sqlmw/blob/97c9c04efc79/interceptor.go).

## Commands and proof

Run from repository root with Go >=1.26 and GNU `timeout` (reference targets also require Docker).
Always use make targets for standard actions:

```sh
make generate   # regenerate the checked-in parser with pinned goyacc
make build      # compile production packages
make test       # uncached Docker-free unit and public smoke tests
make smoke      # uncached public API scenarios only
make reference  # uncached tests against real pinned PostgreSQL 18
make race       # Docker-free suite with the race detector
make test-all   # all suites, including reference and race, within one 900s deadline
make bench      # fresh-engine startup benchmark, five runs with allocation counts
make deps       # list non-standard production dependencies
```

Read `Makefile` for non-standard actions. Use `RUN='^TestName$'` to select tests,
`PACKAGES=./internal/parser` to scope ordinary/race tests, and `VERBOSE=1` for diagnostics.
Do not replace full verification with selected tests. Before delivery, run relevant tests and
exercise the changed public path; report only observed results and name any blocked checks.
Dependency output may contain only this module's paths. Benchmark real fresh engines through
Ping and Close, not lazy Open alone; report ns/op and allocations against the <1ms startup target.
