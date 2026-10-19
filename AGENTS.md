# Agent development guide

## Navigation and package seams

Read [README.md](README.md) for the consumer contract and [docs/DIALECT.md](docs/DIALECT.md)
for native limits and intentional compatibility differences before changing behavior.
Use the [PostgreSQL 18 documentation](https://www.postgresql.org/docs/18/sql.html)
as the SQL syntax reference; do not maintain a duplicate syntax guide in `docs/`.
Design notes live in `docs/design/`; catalog-part workflow lives in `.cswd/`.

**MANDATORY FOR TEST WORK:** Before adding, modifying or migrating any test, read
and follow [docs/TESTING.md](docs/TESTING.md). It is the authoritative test-placement,
dual-mode fixture and oracle-first implementation guide. Public SQL/API tests MUST
be dual-mode tests in `reference/`, never in other directories. When working in an
area with offending tests, migrate them immediately as instructed there.
Keep `docs/TESTING.md` limited to common testing instructions and shared workflow.
Package-specific contracts, test selectors, proof notes and smoke instructions
MUST NOT be added there. Put them in the owning package's `README.md` under
`## Testing`; create the file or section if absent.

- Root `stuntgres`: public `database/sql` adapters, driver/connector, session settings, notices and native Reset/Dump/Restore APIs.
- `internal/parser`: AST and parser entry points in `ast.go`/`parser.go`, lexer in `lexer.go`, keyword categories in `keywords.go`. Edit `grammar.y` for grammar changes, then `make generate`; never hand-edit `grammar_gen.go`.
- `internal/engine`: authorization, binding/execution, transactions, settings, snapshots and lifecycle publication.
- `internal/engine/{ids,expr,authorization,functions,lock,catalog,storage,access}`: layered subsystem packages (layering in `docs/ENGINE-SPLIT.md` §3); each README states its contract; put new code in the lowest package that can own it (new builtins: the `functions` registry); subpackages never import the root engine.
- Sequence DDL/functions use relation descriptors, central ACLs and the existing transaction/lock/query paths. Physical-session memories are distinct from transactional sequence metadata and never use per-CACHE arrays; generator rules are in the `storage` README. TEMP sequence lifetime follows the physical session, not pooled borrowing.
- Prepared sequence literals use a private description analysis cell, a distinct relation plan stamp and a physical-session effective namespace cache. Reanalyze the entire original literal group at Execute start after dependency/path changes; preserve immutable temporal/signature/result contracts. RX remains demand-driven after once-only argument evaluation; grant-time opening retains the in-flight OID, with no expression retarget/replay. Filtered/zero-row and strict NULL calls do not open their sequence. Generator state and descriptor revision are not plan invalidation tokens. Native replacement remains fenced except exact caller-owned live temporary provenance.
- Ordinary prepared SQL uses one private analysis-identity cell, not an executable plan/data/permission cache. Preserve per-occurrence target/nested/view/unused-CTE identities through admission while valid; only dependency/effective-path/namespace/DISCARD invalidation reanalyzes original source. Existing-schema shadow creation alone is not a trigger. Reuse the effective namespace comparator and collect existing analyzed dependencies, never an all-DDL epoch. Full invalidation atomically publishes genuine temporal scalar/array plus sequence/regtype candidates and identity stamps after analysis/signature/provenance success; preserve native logical enum/domain/composite captures and arrays with catalog-owned leaves, including embedded temporal fields and existing lifetime diagnostics. Never re-input old enum labels or rescue replacement logical identities. Later importer/execution failure need not undo successful analysis. Fixed formals/results remain immutable, current data/ACL/compatible metadata stay fresh. Never retarget after lock admission or certify an older queued resolution under a newer environment. DML synthetic query roots are not stable AST owners; retain original table occurrences. Writable views retain their logical target identity, not the translated physical base.
- Prepared shadow retention applies to preexisting statement-boundary shadows, not changes during admission waits. Record actual lexical resolution independently of the chosen retained target/source and preserve native ErrCatalogReplaced when that observation changes during a wait on every route; no warmup exemption, retarget or replay. Preserve existing direct/new-prepare/held conflict tests unchanged. Schema SQL GRANT/REVOKE uses real ACL authority: establish reader USAGE with grants rather than transferring ownership as a permission workaround. Never assume ordinary private schemas supply reader USAGE by default.
- `internal/sqltype`: SQL type descriptors, input/output codecs, coercion and value comparison. Reuse these rather than adding adapter-local conversions.
- `internal/sqlstate`: shared SQL errors; root `Error` aliases it. Assert the SQLState interface, not concrete `pq.Error`.
- Root and internal `*_test.go`: non-SQL adapter and package-level unit tests only; public SQL/API witnesses belong in `reference/`. Migrate offending tests when touching their area, following [docs/TESTING.md](docs/TESTING.md).
- `smoke/`: legacy public API scenarios included in the ordinary suite; do not add public SQL/API tests here. Migrate relevant existing witnesses to dual-mode `reference/` tests when working in their area.
- `reference/`: public SQL/API behavioral tests using one shared body in Docker-free quick mode and real pinned PostgreSQL 18 mode. New tests are untagged; legacy tagged tests remain until migrated. Follow [docs/TESTING.md](docs/TESTING.md) for fixtures, helpers and implementation order.

Production dependencies must be standard library or this module only. Keep internal packages
independent of root adapters. goyacc is generation-only; pq, sqlmw and testcontainers are test-only.
Reuse existing package seams, binders and storage paths; do not introduce parallel implementations.

## Development rules

- Plan every change first: identify affected seams, compatibility evidence, invariants and verification. Keep scope explicit; do not silently widen the dialect.
- Use TDD: write the failing behavioral regression, observe the failure, implement the smallest correct change, then refactor with tests passing. Every new PostgreSQL functionality and compatibility bug requires new or modified dual-mode reference coverage. Follow [docs/TESTING.md](docs/TESTING.md): real PostgreSQL first, record oracle expectations, quick mode second, then verify both modes. Internal unit regressions may supplement this coverage; never duplicate public SQL/API tests outside `reference/`.
- Prefer clear, self-explanatory, maintainable code with separated concerns and testable boundaries. Keep functions focused and files short; split by responsibility, not arbitrary line counts. Grammar and generated files are exceptions. Reference functional test files have a hard 750-line limit; the cohesive shared fixture is exempt.
- Test consumer-visible outcomes, boundaries, errors, atomicity and lifecycle transitions with independently specified expectations. Cover direct/prepared behavior and relevant metadata/cancellation paths. Do not test source text, wiring counts or mock echoes in place of real behavior.
- Keep test output quiet: report failures through the test runner, not success labels; debug output must be opt-in.
- Agents own command deadlines: 900 seconds for `check`, `quickcheck`, `racecheck` and `bench`; 1800 seconds total for post-merge `finalcheck`. Targets impose no deadlines. Fix slow/hanging tests rather than increasing deadlines, dropping coverage or skipping failures; specialized opt-in benchmarks are exempt.
- Pay particular attention to mutexes, goroutines and cancellation. Document lock ownership/order, avoid recursive acquisition and callbacks/external I/O under locks, release resources on every exit, and preserve immutable published snapshots and atomic publication. Use deterministic synchronization rather than sleep-based ordering; exercise cancellation, contention and teardown with the race detector.
- Transaction isolation uses one owner: RR retains all immutable relation/end roots before analysis waits; RC/RU execution views stay grant-time fresh. Never copy row values for snapshot retention, cache an owner on prepared statements, or add a second settings/lock authority. Current characteristic routes validate owner-first; healthy CHAIN preserves final modes, abort restores entry defaults before overrides, and aborted CHAIN carries restored modes. Every chain clears snapshot state.
- Read-only utility execution rejects supported modifying tags before object resolution, authorization or relation locks; DML retains analysis-before-read-only diagnostics. Do not apply execution rejection to Prepare. Selected ALTER TABLE nullability/named-NOT-NULL/DROP COLUMN lists and sequence DDL are the bounded explicit-transaction exceptions; do not enable unrelated utilities. Independently assert SHOW ALL built-in descriptions against PostgreSQL 18.0 short_desc, including current/default transaction settings, and registered custom descriptions without empty-string exemptions.
- Keep parser state/options per call, retain source positions and parameter indices in the AST, and consume the entire input. Never execute a valid prefix while ignoring a suffix.
- Update affected callers and documentation with behavior changes; remove obsolete paths instead of leaving compatibility shims. Regenerate checked-in parser output after grammar changes and verify it builds.

## PostgreSQL compatibility and reference tests

**Read and obey [docs/TESTING.md](docs/TESTING.md) before implementing tests.**
Its mandatory placement, migration and shared-body dual-mode workflow applies to
all test work; existing tests in other directories are not a precedent.

PostgreSQL 18 behavior grounds every change; reference tests are the golden standard.
Resolve conflicting design requirements against real PostgreSQL behavior and encode the result
in reference tests, including independently asserted native capability boundaries where applicable.
Never loosen, disable or skip reference tests to make a change pass: only genuine test bugfixes
and additions are allowed. Record every discovered compatibility bug in reference coverage.

- Use the PostgreSQL 18.6 image digest pinned in [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) and UTF8/C locale. Local newer PostgreSQL sources are research aids, not the release oracle.
- Keep reference tests independent of internal packages and root-private test helpers. Use one bounded-lifecycle container and serialized fixtures; reset public schema before/after cases and close handles/rows first.
- Preserve sqlmw coverage of direct/prepared execution and owned row buffers; retain native pools for controls. Assert expected results independently, not parity or implementation call counts alone.
- Docker/image failures in an explicitly enabled reference run must fail visibly, never be skipped or mocked.

Primary references: [PostgreSQL 18 SQL](https://www.postgresql.org/docs/18/sql-commands.html),
[SQLSTATE](https://www.postgresql.org/docs/18/errcodes-appendix.html),
[testcontainers PostgreSQL](https://golang.testcontainers.org/modules/postgres/),
[pinned sqlmw](https://github.com/ngrok/sqlmw/blob/97c9c04efc79/interceptor.go).

## Commands and proof

Run from repository root with Go >=1.26; selected real-reference checks also require Docker.
Use make targets for standard actions:

```sh
make generate   # regenerate checked-in parser and Unicode tables
make build      # compile production packages
make check RUN='^TestName$' # development: individual tests or small selected sets only
make quickcheck # full-project Docker-free verification after edits/before review or merge
make racecheck  # full quick-mode suite with race detector
make finalcheck # after merge: five startup benchmarks, full quick-mode race suite, deps
make bench      # fresh-engine startup benchmark, five runs with allocation counts
make deps       # list non-standard production dependencies
```

Only `check` accepts options: required `RUN`, `RACE=1`, and `REFERENCE=1` (real pinned
PostgreSQL via `-tags reference` and `-args -reference=true`). Never use it for broad
or whole-package testing. Agents MUST NEVER invoke human-only `make refcheck`.
Every changed/new reference definition must pass `make check RUN='...'` and
`make check REFERENCE=1 RUN='...'` before review/integration; finalcheck is not a substitute.
Run full `quickcheck` after implementation/remedies and rebases before review/integration,
then `finalcheck` after merge. Commit post-merge remedies separately.
Exercise the changed public path before delivery; report observed results and blocked checks.
Dependency output may contain only this module's paths. Benchmark fresh engines through
Ping and Close, not lazy Open; report ns/op and allocations against the <1ms startup target.
