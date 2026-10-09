# Adding and maintaining tests

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
- Every added, modified or migrated dual-mode reference test MUST be selectively
  run in both real pinned PostgreSQL mode and Docker-free quick mode, using the
  same completed test definitions and a `RUN` selector covering all affected tests.
  Repeat both selective runs after changes to those tests or their implementation.
  Fullcheck does not replace this pre-review requirement.
- Concurrency tests MUST identify a deadlock victim from its error outcome, not
  worker completion order. Victim cleanup can wake a successful survivor before
  the victim reports its result. Join both workers under a bounded deadline and
  retain the exact victim, survivor and side-effect assertions.
- Catalog discovery fixtures MUST use scenario-specific object names when
  asserting a complete database-wide candidate bag. The shared PostgreSQL
  fixture resets `public`, not every nonpublic schema; unrelated same-name
  objects can legitimately remain outside the scenario's search path.

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
   `make quickcheck`; final `make fullcheck` runs after integration under the
   catalog-part workflow. Selected tests do not replace full verification.

### Commands and oracle-first discovery

Run from repository root with Go >=1.26 and GNU `timeout`. Real mode also needs
working Docker and image/module access.

```sh
# First: real PostgreSQL discovery/validation.
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceFeatureBehavior$' VERBOSE=1
# Second: Docker-free validation of the recorded contract.
make quickcheck PACKAGES=./reference RUN='^TestReferenceFeatureBehavior$'
# Before review: both modes, using exactly the same completed test.
make quickcheck PACKAGES=./reference RUN='^TestReferenceFeatureBehavior$'
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceFeatureBehavior$'
# Ordinary full Docker-free verification.
make quickcheck
# After integration: all final gates under one 30-minute deadline.
make fullcheck
```

`make quickcheck REFERENCE=1 PACKAGES=./reference` enables `-reference=true` and the `reference` build tag; it runs
both native and PostgreSQL backends, not PostgreSQL alone. The ordinary runner
visits native first. During the initial oracle-discovery iteration, use the
shared `p.backends()` loop and temporarily select only `backend.pg` (for example,
`if !backend.pg { continue }`) so an unimplemented native feature cannot prevent
PostgreSQL observations. Remove that discovery filter before quick validation
and final verification. Never commit a filter that bypasses either backend.

There is no automatic golden-file recorder or PostgreSQL-only make mode.
Use failure diagnostics or temporary verbose-only observation logging to inspect
unknown PostgreSQL results, then encode those results as explicit expectations
and remove discovery scaffolding. `p.step` validates expectations; it does not
learn or update them. Do not weaken assertions to accommodate native failures.
Docker/image failures in an explicitly enabled real run MUST fail visibly, not
be skipped or mocked. Fix slow/hanging tests rather than increasing deadlines.

The index-clause feature smoke exercises mixed keys, an existing scalar expression
key with explicit owned collation/class and INCLUDE, prepared uppercase projection,
payload/derived-key maintenance, the total 32/33 boundary and native controls with
PostgreSQL replay of the actual emitted native SQL. Compact `pg_get_indexdef`
assertions cover default/provider spelling, the INCLUDE ordinal boundary and
post-TYPE state without changing canonical Dump/replay authority:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceIndexClausesFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceIndexClausesFeatureSmoke$' VERBOSE=0
```

This smoke does not replace the complete identical dual-mode family selection
documented in [the index-clause design](design/PG18-INDEX-CLAUSES.md).

The index-property smoke checks the eleven independently expected BOOL values
for B-tree AM/index capabilities, mixed DESC/NULLS ordering and INCLUDE roles,
plus prepared true/false/NULL and explicit native Reset/Restore/foreign restore:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceIndexPropertiesFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceIndexPropertiesFeatureSmoke$' VERBOSE=0
```

Run both with a900-second deadline. The full `^TestReferenceIndexProperties`
selector covers every new definition; see [the inquiry design](design/PG18-FUNC-INFO-INDEX-PROPERTIES.md)
for the shared-coercion consumer risk selectors. Smoke does not replace those
identical dual-mode selective runs, ordinary quickcheck or post-merge fullcheck.

## How the dual-mode fixture works

`newReferenceScenario(t)` creates a fresh native engine, a middleware-wrapped
native pool and an unwrapped pool for native controls. In quick mode PostgreSQL
is absent. In real mode it also opens the pinned PostgreSQL pool. The fixture
owns a 30-second context and pool cleanup; real fixtures reset `public` before
and after each case. All real fixtures share one bounded-lifecycle container:
**do not use `t.Parallel()`**.

Non-public schemas are not reset by the fixture. Register
`schemaReferenceCleanup(t, "schema_name")` before constructing the scenario
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
extra identities. The visibility source-route scenario keeps the complete
unfiltered two-peer bag and creates an unrelated `public.t` to exercise
isolation from the old generic basename.

`p.step(t, step)` executes one description on native and, when enabled,
PostgreSQL. Each backend must match the supplied expectations independently;
complete observed records are then compared for parity. SELECT checks include
column names, database type names, complete rows and owned middleware-recorded
rows/closure. Raw codec representations are checked before normalization.

Use `p.step` for straightforward streams. Use `p.backends()` and one shared
callback for transactions, pinned sessions, held statements, richer metadata,
concurrency or initial oracle discovery. Keep SQL session state on a borrowed
`*sql.Conn` or `*sql.Tx`, not an arbitrary pooled connection. Close rows before
statements, then return connections before fixture/schema cleanup. Register
cleanup immediately and cancel/join goroutines on all exits; use deterministic
synchronization, not sleeps.

The whole-root cutover is complete: root retains13 focused nonSQL units and its
startup benchmark, with no public SQL test bodies or obsolete SQL helpers.
The final driver/session/catalog ledger accounts for372 original sources and131
helper dispositions, with651 shared untagged definitions,39 genuine PostgreSQL-only
and five PostgreSQL-required differential companions.36 genuine-only and all five
differential definitions remain tagged; three genuine-only callers are untagged.
Use [family commands](DEVELOPMENT.md#driver-session-and-catalog-whole-root-cutover)
for exploration and the catalog's identical exact651-name RUN for native,
PostgreSQL and race acceptance. Select companions separately in real mode.

Native eager retained cursors and detached Columns slices are separate contracts
from pq streaming cursors and aliased Columns slices. Shared metadata callbacks
run before iteration, including empty results; raw cells are asserted before
normalization. Native precanceled/expired routes retain ordinary context errors.
For pq user cancellation use observed server admission and joined completion,
then check the SAME physical owner and preheld consumers. Do not assert a
race-prone fast precanceled pq command must return nil outputs, or cancel its
Go context during a notice callback and invent stable transport health.

Settings timeout witnesses live in `TestReferenceSettingsPublicTimeoutStages`
and `TestReferenceSettingsPublicTimeoutRollbackCleanup`. Once a physical owner
is armed with the genuine 1ms statement timer, even `SHOW` or `RESET` can
legitimately time out under scheduling pressure. The implicit-timeout scenario
therefore disposes of its armed owner without further SQL; transaction rollback
restores timeout 0 before checking SAME-owner and held-statement recovery.
`TestReferenceSettingsSQL` checks tiny-timeout encoding on the arming command
and then disposes of that isolated owner. Do not increase the timer, retry an
expired-owner reset, or suppress its diagnostic to make fixture cleanup pass.
The superseded `TestSettingsSmoke` is removed; its typed registry, numeric,
metadata, notice/reentry, parser capture, access and connection-lifecycle
obligations remain in the existing shared `TestReferenceSettings*` consumers.
The superseded `TestNullCountSmoke` is removed. Its literal/table counts, raw
INT4 metadata and argument-count sums, nested calls, held parameter reuse and
empty-result metadata, Exec counts and complete diagnostic/nil-output contracts
remain in the shared `TestReferenceNullCount*` families. Former no-FROM rejection
cases now assert shared success; `TestReferenceNullCountInsertConsumerRoutes`
retains the original INSERT Query/Prepare routes and exact resulting state.
Malformed suffixes retain whole-input/no-prefix-effects rejection coverage,
while actual unsupported array/function-provider forms remain boundaries.


Original source methods are obligations, not interchangeable API labels.
For proven DB/Tx plain calls, set referenceStep.nonContext=true on the existing
runner: actual Exec/Query/Prepare and returned Stmt.Exec/Query retain complete
count/error-stage/nil-output/cursor assertions. Metadata and specialized Scan
hooks inspect the actual source cursor before the truthful return preceding
Context-only recording. Keep a fresh recorded Context companion; never fabricate
its recorder key for a plain call. *sql.Conn has no plain methods: original
Conn.QueryContext and physical-owner adapters remain actual Conn routes.
Explicit QueryRow/Begin/native raw calls and SAME held objects stay explicit.
referencePrepared prepares anew; it does not prove quiet Prepare or held reuse.
Consumers such as deleteReferenceConsume do not invoke step metadata/Scan
callbacks, so preserve those original checks at the callsite. Cleanup follows
rows, statements, rollback, borrowed connections, then fixture/schema/role reset;
worker joins and registered failure exits are part of the assertion union.



### Common helper reference

These helpers are package-local and directly available to `reference_test` files.
No additional helper imports or fixture setup are needed.

| Helper | Contract and use |
| --- | --- |
| `newReferenceScenario(t) *referencePair` | Required entry point for new dual-mode tests; honors `-reference` and uses a fresh native engine. Do not use legacy `newReferencePair(t)`, which always starts Docker. |
| `p.step(t, s)` | Execute and independently assert the same step on available backends, then compare observations. |
| `p.backends()` | Returns backend descriptors with `name`, `db` (`*sql.DB`), `recorder`, and `pg` (true only for PostgreSQL). Loop once over these and share the callback. |
| `p.ctx`, `p.native`, `p.nativeConnector` | Bounded scenario context; unwrapped native control pool; fresh native connector for public connector controls. `p.memory` and `p.postgres` are the wrapped pools; `p.postgres` is nil in quick mode. |
| `referenceExec(query, count, args...) referenceStep` | Exec success with exact `RowsAffected` and unsupported `LastInsertId`; DDL generally expects zero. Arguments are bound normally. Error steps require nil published results. |
| `referenceSelect(query, columns, types, rows, args...) referenceStep` | Query success with exact column names, `DatabaseTypeName` strings and normalized `[][]any` values. Supply metadata even for zero rows. |
| `referencePrepared(s) referenceStep` | Return the same step with Prepare followed by statement execution. This prepares anew per step; it does not prove held-statement reuse. |
| `referenceError(query, state, args...) referenceStep` | Exec failure with exact SQLSTATE, asserted through `SQLState() string`. Use structured diagnostics when testing message/field fidelity or failure stage. |
| `referenceErrorDiagnostic(query, want, args...) referenceStep` | Exec failure with a complete `referenceDiagnostic` expectation, including exact empty fields. |
| `referenceImplicitError(query, want, args...) referenceStep` | As above, filling both severity fields with `ERROR`. |
| `referencePosition(n) string` | Decimal diagnostic character position; zero becomes the absent-field empty string. PostgreSQL positions are character-based, not byte offsets. |
| `controlsReferenceDiagnostic(code, message) referenceDiagnostic` | Build a diagnostic with both severities `ERROR`, code and message; set any additional expected fields explicitly. |
| `referenceRunStep(t, ctx, executor, recorder, s) referenceObserved` | Run standard independent assertions on a chosen backend/session. Context steps accept DB/Conn/Tx; proven `s.nonContext=true` requires actual DB/Tx plain methods and cannot use Conn or a fake forwarding adapter. Actual cursor callbacks remain checked before returning without Context-only recording. Does not compare backends for you. |
| `referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}` | Executor wrapper for an already prepared `*sql.Stmt`, usable with `referenceRunStep`; leave `s.prepared` false, use the matching query, and close the held statement yourself. Other queries route through the embedded executor. |
| `referenceState(t, err, want) string` | Check SQLSTATE through the interface; empty `want` requires success. Useful for custom public API calls. |
| `referenceExtractDiagnostic(t, err) referenceDiagnostic` | Extract structured public native/pq diagnostics; validates diagnostic shape and excludes native claims of PostgreSQL C source locations. Useful while discovering oracle data. |
| `referenceAssertDiagnostic(t, err, &want) referenceDiagnostic` | Assert the complete structured diagnostic for custom calls. Does not accept a nil expectation. |
| `referenceNormalize(value) any` | Converts `[]byte` to string and `time.Time` to UTC without monotonic/location identity; leaves other values unchanged. Standard steps apply this automatically. |
| `noticeReferencePool(t, p, postgres, &records)` | Return a notice-enabled pool, pinned connection and recorder; append complete notice diagnostics to `records`. Select the backend using `backend.pg`; fixture registers cleanup. |
| `referenceSessionCancel(t, parent, conn, prepared, queryMode...)` | Observe real PostgreSQL pg_sleep admission, cancel through the server, join the operation and assert full57014. Optional true queryMode selects Query with nil error cursor; otherwise Exec. The caller asserts SAME-owner continuation; this is not a native precancel substitute. |
| `roleReferenceCleanup(t, names)` | Register cleanup for real PostgreSQL roles created by the test **before constructing the scenario**, so pool closure and schema reset happen before role removal. Public-schema resets do not remove cluster roles. |

### Step fields and expected data

- `referenceStep.query`, `args`, `prepared`, `selectRows`, `affected`, `state`,
  `columns`, `types` and `rows` are populated by the constructors above.
- Set `s.bag = true` for unordered results: order is ignored but duplicate
  occurrences must match. Otherwise add SQL `ORDER BY` when ordering is part of
  the expected result; never depend on traversal order.
- Set `s.checkMetadata = func(t *testing.T, rows *sql.Rows) { ... }` for complete
  raw descriptor contracts on the actual result, before iteration even when
  empty. Retain independent backend ScanType/Nullable/Length/DecimalSize flags.
- Set `s.checkScan = func(t *testing.T, rows *sql.Rows, rowIndex int) { ... }` to
  rescan the current row into specialized destinations. It must not issue SQL.
  Use a shared backend callback for metadata contracts beyond database type names.
- For Query errors, start with `referenceErrorDiagnostic`, then set
  `s.selectRows = true`. Set `s.errorStage` to `"prepare"` or `"execute"` when
  stage matters; this field is checked only with a structured diagnostic.
- `s.postgresDiagnostic` permits an independently specified PostgreSQL diagnostic
  for intentional differences. Both full records are asserted first; only
  Position/Context are normalized for subsequent parity. This is not permission
  to suppress unrelated differences.
- `referenceDiagnostic` fields are `Severity`, `SeverityUnlocalized`, `Code`,
  `Message`, `Detail`, `Hint`, `Position`, `InternalPosition`, `InternalQuery`,
  `Context`, `Schema`, `Table`, `Column`, `DataType`, `Constraint`. Unspecified
  fields must actually be empty; source File/Line/Routine are not reference data.
- Integer cells normally use `int64`, floating cells `float64`, Boolean cells
  `bool`, NULL `nil`; normalized byte payloads use strings. Do not confuse
  `ColumnType.ScanType` with the actual raw scanned value. Temporal/OID and other
  codec differences can require independently captured backend expectations;
  branch on `backend.pg` for those expectations, not to duplicate the scenario.

## Self-contained example

Place this untagged file in `reference/feature_answer_test.go`. The literal
expectations illustrate the final recorded form; when adding real functionality,
validate its own expectations against the pinned PostgreSQL run first.

```go
package reference_test

import "testing"

func TestReferenceFeatureAnswer(t *testing.T) {
    p := newReferenceScenario(t)
    for _, backend := range p.backends() {
        t.Run(backend.name, func(t *testing.T) {
            // During oracle discovery only, temporarily select backend.pg
            // before this callback. The completed test runs every backend.
            conn, err := backend.db.Conn(p.ctx)
            if err != nil {
                t.Fatal(err)
            }
            t.Cleanup(func() {
                if err := conn.Close(); err != nil {
                    t.Error(err)
                }
            })
            run := func(s referenceStep) {
                referenceRunStep(t, p.ctx, conn, backend.recorder, s)
            }

            run(referenceExec("CREATE TABLE answer (v INTEGER)", 0))
            run(referenceExec("INSERT INTO answer VALUES (41), (NULL)", 2))
            for _, prepared := range []bool{false, true} {
                s := referenceSelect(
                    "SELECT v + $1::integer AS result FROM answer ORDER BY v NULLS LAST",
                    []string{"result"}, []string{"INT4"},
                    [][]any{{int64(42)}, {nil}}, int64(1),
                )
                if prepared {
                    s = referencePrepared(s)
                }
                run(s)
            }
            run(referenceError("SELECT absent FROM answer", "42703"))
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

Run `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceFeatureAnswer$' VERBOSE=1` first, then
`make quickcheck PACKAGES=./reference RUN='^TestReferenceFeatureAnswer$'`. After any
test or implementation changes, rerun both selectively before review, then run
ordinary quickcheck and the post-integration fullcheck.

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

- `reference/fixture_test.go`: step runner, diagnostic, notice and control helpers.
- `reference/fixture_lifecycle_test.go`: mode selection, backend descriptors and
  container lifecycle.
- `Makefile`: non-standard command details.

These are last-resort implementation references, not prerequisites for each test.

## Subquery regression and instrumentation coverage

`reference/query_subquery_*_test.go` contains the shared direct/prepared,
held-handle, mutation, metadata, admission, locking and stored-view scenarios.
Both backends execute the same callback without a discovery-only backend filter:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceQuerySubquery'
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQuerySubquery'
```

The integration handoff additionally supplies the exact selector for migrated
consumer/no-FROM families; those families are not replaced by the new prefix.
Each selective invocation uses a900-second deadline, in both modes before review.

Internal `TestQuerySubquery*` cases instrument actual bound plans, typed
comparison providers, lexical frame lookup, admission manifests and borrowed
stage cleanup. Sentinels prove ignored EXISTS targets versus required ORDER
keys/derived projections and normal target demand. They are not duplicate
public SQL feature fixtures and do not use fake database APIs. Parser
`TestQuerySubqueryAST` / `TestQuerySubquerySourceWalk` check structural syntax,
positions, source occurrence order and child-specific rowmark ownership.
Run those private units with
`make quickcheck PACKAGES='./internal/parser ./internal/engine' RUN='^TestQuery(Subquery|DerivedSubquerySyntax)'`.

