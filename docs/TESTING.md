# Adding and maintaining tests

Feature-specific contracts, selectors, and smoke instructions belong in the
owning package’s `README.md`, under `Testing`. This guide contains shared
test policy, fixture usage, and implementation workflow.

## Mandatory policy

Agents MUST read and follow this guide before adding, modifying or migrating tests.
This is the authoritative test-placement and implementation workflow.

- All tests exercising public APIs by issuing SQL and checking responses MUST live
  in `reference/` as **dual-mode reference tests**. This includes direct/prepared
  execution, rows, metadata, diagnostics, cancellation and SQL witnesses around
  native controls. Implementing these tests in root, `smoke/`, `internal/` or any
  other directory is forbidden.
- When working in an area containing offending tests, immediately migrate them
  to dual-mode tests in `reference/`. Preserve the complete behavioral assertion
  set and public execution routes, merge with existing reference coverage where
  appropriate, and remove the superseded tests. Do not leave native-only copies.
  A migration subagent MAY handle this independent slice to keep the current
  implementation context focused; give it the original assertions and this guide.
  The approved smoke migration retains only `smoke/catalog_test.go`:
  `TestCatalogOwnersPublicWitness` tests the native Driver's opaque-DSN attachment
  and physical lifetime. SQL supplies incidental state observations, not a
  portable PostgreSQL scenario. Do not expand this named retention exception.
- Internal unit tests that exercise package functions rather than public SQL APIs
  may remain beside their package. They supplement, never replace, reference
  coverage for PostgreSQL functionality.
- Every new PostgreSQL functionality implemented in stuntgres MUST add or modify
  dual-mode reference coverage. Every discovered compatibility bug MUST have a
  failing reference regression before its fix.
- The actual SQL/API exercise and assertions MUST share one body across modes.
  Never maintain near-duplicate PostgreSQL and quick-mode implementations.
  Mode selection changes backend availability, not the scenario.
- Use only public stuntgres APIs in `reference/`; do not import internal packages
  or root-private test helpers. Name tests `TestReference<Feature><Behavior>` and
  use untagged `*_test.go` files so ordinary `make quickcheck` discovers them.
- Assert behavior, boundaries, errors, atomicity and lifecycle transitions, not
  source text, wiring counts, mock echoes or parity alone. Keep functional files
  at most 750 lines; shared fixture code is exempt. Keep output quiet by default.
  PostgreSQL internal DEBUG trace wording, allocator identities, record counts
  and loader order are implementation details, not SQL notice expectations.
  Keep independently specified feature notices and their complete diagnostics
  exact; distinguish DEBUG tracing from unexpected NOTICE/WARNING delivery.
- Organize whole test files in feature-group packages under `reference/`, with
  common importable helpers in `reference/fixture/`. Keep at most 30 packages
  total including the fixture, and at most 1,000,000 bytes per package counting
  all files. Do not split or duplicate test bodies to meet these limits.
- Every added, modified or migrated dual-mode reference test MUST be selectively
  run in both real pinned PostgreSQL mode and Docker-free quick mode, using the
  same completed test definitions and a `RUN` selector covering all affected tests.
  Repeat both selective runs after changes to those tests or their implementation.
  Finalcheck does not replace this pre-review requirement.
- Concurrency tests MUST identify a deadlock victim from its error outcome, not
  worker completion order: a ready channel select can choose either result.
  Victim cleanup can also wake a successful survivor before the victim reports
  its result. Join both workers under a bounded deadline and retain the exact
  victim, survivor and side-effect assertions.
  `TestUniqueClaimDeadlock` collects both outcomes under one five-second deadline,
  requires exactly one error, and assigns victim/survivor assertions by that error;
  its worker cleanup cancels and joins on assertion-failure paths as well.
- Catalog discovery fixtures MUST use scenario-specific object names when
  asserting a complete database-wide candidate bag. The shared PostgreSQL
  fixture resets `public`, not every nonpublic schema; unrelated same-name
  objects can legitimately remain outside the scenario's search path.
- The pinned fixture retains one idle administrative PostgreSQL session, with
  unrestricted open connections for concurrent observers. Serialized resets
  require all borrowers, Rows and workers to return before the boundary. Each
  before-case and after-case reset borrows that session under the existing
  15-second context, executes standalone `ROLLBACK`, then standalone `DISCARD ALL`,
  then unchanged `DROP SCHEMA public CASCADE; CREATE SCHEMA public` on that same
  connection. Returning it preserves authentication without retaining dirty
  authorization, role, settings, temporary, prepared or advisory state.
  Case pools and native engines remain isolated. Startup failure and final
  teardown close the administrative pool; no separate reset connection is owned.

Native Reset/Dump/Restore have no PostgreSQL API equivalent. Keep their witnesses
in the same dual-mode reference scenario, with explicit native-only control
phases and independently specified PostgreSQL SQL outcomes where applicable.
Do not invent a PostgreSQL control API or duplicate the main SQL test body.

## Required implementation order

1. **Write the shared reference scenario and run real PostgreSQL first.** Use
   the pinned PostgreSQL 18.0 fixture, not a local server or newer release. Start
   with independently reasoned expectations; use observed PostgreSQL results to
   resolve unknown values, metadata and diagnostics.
2. **Record reference data from that run.** Put concrete expected rows, column
   names, database type names, affected counts and relevant diagnostics in the
   test. Review them as a contract; never derive expected values from stuntgres
   output, copy native failures into goldens, or assert only backend equality.
3. **Run quick mode in the second iteration.** Confirm the recorded contract
   exposes missing/incorrect stuntgres behavior. Observe the failing regression,
   implement the smallest fix, and rerun until it passes.
4. **Finish by selectively running both modes.** Run the same completed definitions
   in quick and real PostgreSQL modes, confirming independent expectations and
   consistency for every added, changed or migrated scenario. Then run ordinary
   `make quickcheck`; final `make finalcheck` runs after integration under the
   catalog-part workflow. Selected tests do not replace full verification.

### Commands and oracle-first discovery

Run from repository root with Go >=1.26. Real mode needs Docker and image/module access.

```sh
# Oracle-first discovery, then native proof; repeat both after affected edits.
make check RUN='^TestReferenceFeatureBehavior$' REFERENCE=1
make check RUN='^TestReferenceFeatureBehavior$'
# Whole-project verification after edits, before review/integration.
make quickcheck
# Same Docker-free suite with race detection.
make racecheck
# After integration: startup benchmark, socket-free native race suite, dependencies.
make finalcheck
```

`finalcheck` uses `noservertests` to exclude server checks that open real TCP
sockets. Those checks remain enabled in `quickcheck`, `racecheck` and selected
`make check RUN='...'` runs.

`check` requires `RUN` selecting individual tests or small sets; add `RACE=1` for
selected race proof. All test targets regenerate parser and Unicode data and keep
output quiet. The Makefile imposes no deadline and disables Go's implicit test
timeout (`-timeout=0`). Agents MUST set external command deadlines of 900 seconds
for check, quickcheck, racecheck and bench, or 1800 seconds for finalcheck.
`refcheck` runs the whole real reference suite for humans only; agents MUST NEVER
invoke it. Neither whole-project target replaces selected dual-mode pre-review proof.

`make check RUN='...' REFERENCE=1` enables `-reference=true` and the `reference`
build tag for `./reference/...`; it runs both native and PostgreSQL backends,
not PostgreSQL alone. Real-mode targets use `-p 1`: each package owns an
independent fixture/container/process lifecycle, with startup and teardown
serialized across packages. Explicit true eagerly starts every selected
package's oracle even when no tests match. The stdlib-only
`reference/cmd/checkpackages` command recursively discovers tagged test files
using `go list -json -tags reference ./reference/...` and parses top-level test
names without running tests. For selectors without `/`, only packages with
matching top-level tests run. Slash-containing subtest selectors conservatively
retain all test packages so Go performs exact test/subtest matching; invalid
regexes fail rather than narrowing selection. A no-match selector runs the
fixture test package to preserve eager startup and visible startup failures.
Packages with no test files are excluded. The at-most-30-package cap, including
the selector command and fixture, bounds conservative startup overhead.
The ordinary runner visits native first. During the initial oracle-discovery iteration, use the
shared `p.Backends()` loop and temporarily select only `backend.PG` (for example,
`if !backend.PG { continue }`) so an unimplemented native feature cannot prevent
PostgreSQL observations. Remove that discovery filter before quick validation
and final verification. Never commit a filter that bypasses either backend.

There is no automatic golden-file recorder or PostgreSQL-only make mode.
Use failure diagnostics or temporary verbose-only observation logging to inspect
unknown PostgreSQL results, then encode those results as explicit expectations
and remove discovery scaffolding. `p.Step` validates expectations; it does not
learn or update them. Do not weaken assertions to accommodate native failures.

## How the dual-mode fixture works

`fixture.NewScenario(t)` creates a fresh native engine, a middleware-wrapped
native pool and an unwrapped pool for native controls. In quick mode PostgreSQL
is absent. In real mode it also opens the pinned PostgreSQL pool. The fixture
owns a 30-second context and pool cleanup; real fixtures reset `public` before
and after each case. All real fixtures in one package share one bounded-lifecycle
container: **do not use `t.Parallel()`**. Each package's `TestMain` delegates to
`fixture.Main`; different package processes own independent containers,
administrative sessions and fixture state. Real-mode Makefile targets serialize
those processes with `-p 1`, including startup and final teardown.

Packages needing a startup tracking mode may opt their existing single oracle
in through `fixture.Main(m, fixture.WithCommitTimestampTracking())`.
The option is applied before eager startup; fixture initialization checks the
requested server setting, and native scenario engines inherit the same mode.
Other packages retain default-off tracking. This is not a session SET toggle
or permission to create a second package-owned container.

Change note: the pinned admin pool retains one idle physical session instead of
reconnecting at every fixture reset. Open connections remain unrestricted so
observers can run while a replay holds an admin handle. Every existing
15-second before/after reset borrows one connection, executes standalone
`ROLLBACK`, then standalone `DISCARD ALL`, then the existing public schema
DROP/CREATE on that same connection, and returns it on every exit.
`DISCARD ALL` must not share a multi-statement execution with ROLLBACK: it
cannot run inside a transaction block. It restores authenticated/session role
state, GUCs and temp/prepared/advisory state before schema ownership is assigned.
All borrowed admin handles, Rows and workers must be returned/closed/joined
before serial fixture cleanup. Case pools remain fresh; SCRAM authentication,
startup configuration, fixture contexts and verification deadlines are unchanged.

Every pair, including legacy `fixture.NewPair(t)`, owns a dedicated native
`Driver`. Its wrapped and native control pools share the exact fixture DSN;
closing and reopening a pool on that same Driver/DSN preserves its engine.
Legacy recorder provenance remains unchanged. Legacy notice helpers preserve
their caller-specific `reference/` + `t.Name()` DSNs on the pair's Driver:
equal caller names share state, different names remain isolated. Modern
scenario notice helpers retain the parent connector. Neither kind retains
finished fixture engines in the process-registered Driver. The explicit
registered-driver BYTEA ownership witness is intentionally unchanged.

Non-public schemas are not reset by the fixture. Register
`fixture.SchemaReferenceCleanup(t, "schema_name")` before constructing the scenario
when a case creates or moves objects into another schema; otherwise a later
case can fail with `42P06` even when it passes in isolation.
Dropping a moved view does not remove its destination schema. Prepared utility
and native replay cases must register that schema too, even when they drop every
view they created. Registering before the scenario lets fixture handles close
before schema teardown.

Queries of `pg_class` are database-wide across namespaces, not limited to the fixture's `public` reset.
Use case-specific relation names or constrain the intended namespaces when
asserting a complete candidate bag; common names such as `t` can match unrelated
objects outside the case even when its own schema cleanup is correct.
Preserve exact multiplicities and visibility assertions rather than ignoring

`p.Step(t, step)` executes one description on native and, when enabled,
PostgreSQL. Each backend must match the supplied expectations independently;
complete observed records are then compared for parity. SELECT checks include
column names, database type names, complete rows and owned middleware-recorded
rows/closure. Raw codec representations are checked before normalization.

Use `p.Step` for straightforward streams. Use `p.Backends()` and one shared
callback for transactions, pinned sessions, held statements, richer metadata,
concurrency or initial oracle discovery. Keep SQL session state on a borrowed
`*sql.Conn` or `*sql.Tx`, not an arbitrary pooled connection. Close rows before
statements, then return connections before fixture/schema cleanup. Register
cleanup immediately and cancel/join goroutines on all exits; use deterministic
synchronization, not sleeps.

Native eager retained cursors and detached Columns slices are separate contracts
from pq streaming cursors and aliased Columns slices. Shared metadata callbacks
run before iteration, including empty results; raw cells are asserted before
normalization. Native precanceled/expired routes retain ordinary context errors.
For pq user cancellation use observed server admission and joined completion,
then check the SAME physical owner and preheld consumers. Do not assert a
race-prone fast precanceled pq command must return nil outputs, or cancel its
Go context during a notice callback and invent stable transport health.

Original source methods are obligations, not interchangeable API labels.
For proven DB/Tx plain calls, set fixture.Step.NonContext=true on the existing
runner: actual Exec/Query/Prepare and returned Stmt.Exec/Query retain complete
count/error-stage/nil-output/cursor assertions. Metadata and specialized Scan
hooks inspect the actual source cursor before the truthful return preceding
Context-only recording. Keep a fresh recorded Context companion; never fabricate
its recorder key for a plain call. *sql.Conn has no plain methods: original
Conn.QueryContext and physical-owner adapters remain actual Conn routes.
Explicit QueryRow/Begin/native raw calls and SAME held objects stay explicit.
fixture.Prepared prepares anew; it does not prove quiet Prepare or held reuse.
Consumers such as fixture.DeleteReferenceConsume do not invoke step metadata/Scan
callbacks, so preserve those original checks at the callsite. Cleanup follows
rows, statements, rollback, borrowed connections, then fixture/schema/role reset;
worker joins and registered failure exits are part of the assertion union.



### Common helper reference

Import common helpers explicitly as `fixture` from
`"stuntgres/reference/fixture"`; do not use dot imports or package-local
compatibility aliases. Each feature-group package's `TestMain` must call
`os.Exit(fixture.Main(m))` to own its startup and teardown.

When moving or exporting helpers, update runtime-frame admission probes to the
actual new symbol name. `fixture.BinaryConversionActiveCancel` requires both the
provider and its worker frame before cancellation; preserve that gate, workload,
cleanup and assertions rather than weakening them to make a migration pass.
Native admission reads complete live goroutine stacks: the 32-PC
`runtime.GoroutineProfile` limit must not hide the worker after executor
decomposition. Both frames must belong to the same live stack; goroutine
creation metadata is not admission evidence.

| Helper | Contract and use |
| --- | --- |
| `fixture.NewScenario(t) *fixture.Pair` | Required entry point for new dual-mode tests; honors `-reference` and uses a fresh native engine. Do not use legacy `fixture.NewPair(t)`, which always starts Docker. |
| `fixture.NewFixture(t, withPostgres, nativeRaw) *fixture.Pair` | Low-level fixture seam for ownership/provenance tests. Every pair owns a dedicated native Driver. `withPostgres` controls oracle startup; `nativeRaw` selects native/PostgreSQL raw recorder provenance, not Driver ownership. False retains exact legacy raw semantics. Prefer `fixture.NewScenario` for feature tests. |
| `p.Step(t, s)` | Execute and independently assert the same step on available backends, then compare observations. |
| `p.Backends()` | Returns backend descriptors with `Name`, `DB` (`*sql.DB`), `Recorder`, and `PG` (true only for PostgreSQL). Loop once over these and share the callback. |
| `p.Ctx`, `p.Native`, `p.NativeConnector` | Bounded scenario context; unwrapped native control pool; fresh native connector for public connector controls. `p.Memory` and `p.Postgres` are the wrapped pools; `p.Postgres` is nil in quick mode. |
| `fixture.Exec(query, count, args...) fixture.Step` | Exec success with exact `RowsAffected` and unsupported `LastInsertId`; DDL generally expects zero. Arguments are bound normally. Error steps require nil published results. |
| `fixture.Select(query, columns, types, rows, args...) fixture.Step` | Query success with exact column names, `DatabaseTypeName` strings and normalized `[][]any` values. Supply metadata even for zero rows. |
| `fixture.Prepared(s) fixture.Step` | Return the same step with Prepare followed by statement execution. This prepares anew per step; it does not prove held-statement reuse. |
| `fixture.Error(query, state, args...) fixture.Step` | Exec failure with exact SQLSTATE, asserted through `SQLState() string`. Use structured diagnostics when testing message/field fidelity or failure stage. |
| `fixture.ErrorDiagnostic(query, want, args...) fixture.Step` | Exec failure with a complete `fixture.Diagnostic` expectation, including exact empty fields. |
| `fixture.ImplicitError(query, want, args...) fixture.Step` | As above, filling both severity fields with `ERROR`. |
| `fixture.Position(n) string` | Decimal diagnostic character position; zero becomes the absent-field empty string. PostgreSQL positions are character-based, not byte offsets. |
| `fixture.ControlsReferenceDiagnostic(code, message) fixture.Diagnostic` | Build a diagnostic with both severities `ERROR`, code and message; set any additional expected fields explicitly. |
| `fixture.RunStep(t, ctx, executor, recorder, s) fixture.Observed` | Run standard independent assertions on a chosen backend/session. Context steps accept DB/Conn/Tx; proven `s.NonContext=true` requires actual DB/Tx plain methods and cannot use Conn or a fake forwarding adapter. Actual cursor callbacks remain checked before returning without Context-only recording. Does not compare backends for you. |
| `fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}` | Executor wrapper for an already prepared `*sql.Stmt`, usable with `fixture.RunStep`; leave `s.Prepared` false, use the matching query, and close the held statement yourself. Other queries route through the embedded executor. |
| `fixture.State(t, err, want) string` | Check SQLSTATE through the interface; empty `want` requires success. Useful for custom public API calls. |
| `fixture.ExtractDiagnostic(t, err) fixture.Diagnostic` | Extract structured public native/pq diagnostics; validates diagnostic shape and excludes native claims of PostgreSQL C source locations. Useful while discovering oracle data. |
| `fixture.AssertDiagnostic(t, err, &want) fixture.Diagnostic` | Assert the complete structured diagnostic for custom calls. Does not accept a nil expectation. |
| `fixture.Normalize(value) any` | Converts `[]byte` to string and `time.Time` to UTC without monotonic/location identity; leaves other values unchanged. Standard steps apply this automatically. |
| `fixture.NoticeReferencePool(t, p, postgres, &records)` | Return a notice-enabled pool, pinned connection and recorder; append complete notice diagnostics to `records`. Select the backend using `backend.PG`; fixture registers cleanup. Native legacy provenance preserves the exact caller `reference/` + `t.Name()` DSN on the pair-owned Driver; scenario provenance shares the parent connector even for child names. The native pair must have an owning connector; there is no registered-driver fallback. |
| `fixture.SessionCancel(t, parent, conn, prepared, queryMode...)` | Observe real PostgreSQL pg_sleep admission, cancel through the server, join the operation and assert full57014. Optional true queryMode selects Query with nil error cursor; otherwise Exec. The caller asserts SAME-owner continuation; this is not a native precancel substitute. |
| `fixture.RoleReferenceCleanup(t, names)` | Register cleanup for real PostgreSQL roles created by the test **before constructing the scenario**, so pool closure and schema reset happen before role removal. Public-schema resets do not remove cluster roles. |

Register role cleanup on the same `testing.T` that owns the scenario, before
constructing its pair. A child subtest using a parent's pair must not register
role cleanup on the child: child cleanup runs while parent-owned tables/ACLs
still exist. The parent owns role removal after its physical handles and schema
have been cleaned up; consumer subtests retain all assertions and reset identity.

### Step fields and expected data

- `fixture.Step.Query`, `Args`, `Prepared`, `SelectRows`, `Affected`, `State`,
  `Columns`, `Types` and `Rows` are populated by the constructors above.
- Set `s.Bag = true` for unordered results: order is ignored but duplicate
  occurrences must match. Otherwise add SQL `ORDER BY` when ordering is part of
  the expected result; never depend on traversal order.
- Set `s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { ... }` for complete
  raw descriptor contracts on the actual result, before iteration even when
  empty. Retain independent backend ScanType/Nullable/Length/DecimalSize flags.
- Set `s.CheckScan = func(t *testing.T, rows *sql.Rows, rowIndex int) { ... }` to
  rescan the current row into specialized destinations. It must not issue SQL.
  Use a shared backend callback for metadata contracts beyond database type names.
- For Query errors, start with `fixture.ErrorDiagnostic`, then set
  `s.SelectRows = true`. Set `s.ErrorStage` to `"prepare"` or `"execute"` when
  stage matters; this field is checked only with a structured diagnostic.
- `s.PostgresDiagnostic` permits an independently specified PostgreSQL diagnostic
  for intentional differences. Both full records are asserted first; only
  Position/Context are normalized for subsequent parity. This is not permission
  to suppress unrelated differences.
- `fixture.Diagnostic` fields are `Severity`, `SeverityUnlocalized`, `Code`,
  `Message`, `Detail`, `Hint`, `Position`, `InternalPosition`, `InternalQuery`,
  `Context`, `Schema`, `Table`, `Column`, `DataType`, `Constraint`. Unspecified
  fields must actually be empty; source File/Line/Routine are not reference data.
- Integer cells normally use `int64`, floating cells `float64`, Boolean cells
  `bool`, NULL `nil`; normalized byte payloads use strings. Do not confuse
  `ColumnType.ScanType` with the actual raw scanned value. Temporal/OID and other
  codec differences can require independently captured backend expectations;
  branch on `backend.PG` for those expectations, not to duplicate the scenario.

## Self-contained example

Place this untagged file in `reference/query_core/feature_answer_test.go`, using
that group's existing external test package and `TestMain`. New packages need a
`TestMain` calling `os.Exit(fixture.Main(m))`; do not duplicate an existing one.
The literal expectations illustrate the final recorded form; when adding real
functionality, validate its own expectations against the pinned PostgreSQL run
first.

```go
package reference_test

import (
    "testing"

    "stuntgres/reference/fixture"
)

func TestReferenceFeatureAnswer(t *testing.T) {
    p := fixture.NewScenario(t)
    for _, backend := range p.Backends() {
        t.Run(backend.Name, func(t *testing.T) {
            // During oracle discovery only, temporarily select backend.PG
            // before this callback. The completed test runs every backend.
            conn, err := backend.DB.Conn(p.Ctx)
            if err != nil {
                t.Fatal(err)
            }
            t.Cleanup(func() {
                if err := conn.Close(); err != nil {
                    t.Error(err)
                }
            })
            run := func(s fixture.Step) {
                fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
            }

            run(fixture.Exec("CREATE TABLE answer (v INTEGER)", 0))
            run(fixture.Exec("INSERT INTO answer VALUES (41), (NULL)", 2))
            for _, prepared := range []bool{false, true} {
                s := fixture.Select(
                    "SELECT v + $1::integer AS result FROM answer ORDER BY v NULLS LAST",
                    []string{"result"}, []string{"INT4"},
                    [][]any{{int64(42)}, {nil}}, int64(1),
                )
                if prepared {
                    s = fixture.Prepared(s)
                }
                run(s)
            }
            run(fixture.Error("SELECT absent FROM answer", "42703"))
        })
    }
}
```

The shared callback creates independent state on each backend. A pinned
connection owns the session, and both direct and prepared queries check exact
values, NULL propagation and metadata through the same step description. The
SQLSTATE assertion is a separate failure witness. Standard steps also check
owned middleware rows and closure; the fixture closes its pools after the
borrowed connection is returned.

Run `make check REFERENCE=1 RUN='^TestReferenceFeatureAnswer$'` first, then
`make check RUN='^TestReferenceFeatureAnswer$'`. After any
test or implementation changes, rerun both selectively before review, then run
ordinary quickcheck and the post-integration finalcheck.

## Allocation checks

For package-function byte-allocation budgets, use calibrated
`testing.Benchmark` measurements and normalize `AllocedBytesPerOp` to one
provider evaluation when a benchmark iteration evaluates a batch.
Short process-wide `runtime.MemStats.TotalAlloc` samples can charge unrelated
runtime allocations to the provider, especially under the race detector.
Keep independently chosen payload-size and allocation-count bounds unchanged.

## Optional last-resort implementation materials

This guide is sufficient for ordinary dual-mode tests. Do **not** read other
feature tests to discover the basic convention. Only when a required advanced
fixture behavior is not covered above, optionally consult:

- `reference/fixture/fixture.go`: step runner, diagnostic, notice and control helpers.
- `reference/fixture/lifecycle.go`: mode selection, backend descriptors and
  container lifecycle.
- `Makefile`: non-standard command details.

These are last-resort implementation references, not prerequisites for each test.

## Package-specific testing guidance

- [aggregates](../reference/aggregates/README.md#testing)
- [alter](../reference/alter/README.md#testing)
- [binary_json_arrays](../reference/binary_json_arrays/README.md#testing)
- [catalogs](../reference/catalogs/README.md#testing)
- [datetime](../reference/datetime/README.md#testing)
- [dml](../reference/dml/README.md#testing)
- [fixture](../reference/fixture/README.md#testing)
- [indexes](../reference/indexes/README.md#testing)
- [locking_transactions](../reference/locking_transactions/README.md#testing)
- [numeric](../reference/numeric/README.md#testing)
- [parser](../internal/parser/README.md#testing)
- [patterns_encoding](../reference/patterns_encoding/README.md#testing)
- [query_core](../reference/query_core/README.md#testing)
- [query_order](../reference/query_order/README.md#testing)
- [roles](../reference/roles/README.md#testing)
- [sequences](../reference/sequences/README.md#testing)
- [sessions](../reference/sessions/README.md#testing)
- [strings](../reference/strings/README.md#testing)
- [tables](../reference/tables/README.md#testing)
- [type_resolution](../reference/type_resolution/README.md#testing)
- [types](../reference/types/README.md#testing)
- [views](../reference/views/README.md#testing)
