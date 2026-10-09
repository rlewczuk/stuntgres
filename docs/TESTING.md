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
  use untagged `*_test.go` files so ordinary `make test` discovers them.
- Assert behavior, boundaries, errors, atomicity and lifecycle transitions, not
  source text, wiring counts, mock echoes or parity alone. Keep functional files
  at most 750 lines; shared fixture code is exempt. Keep output quiet by default.

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
4. **Finish by running both modes.** Run the same completed definitions in quick
   and real PostgreSQL modes, confirming independent expectations and consistency.
   Then run full applicable suites; selected tests do not replace full verification.

### Commands and oracle-first discovery

Run from repository root with Go >=1.26 and GNU `timeout`. Real mode also needs
working Docker and image/module access.

```sh
# First: real PostgreSQL discovery/validation.
make reference RUN='^TestReferenceFeatureBehavior$' VERBOSE=1
# Second: Docker-free validation of the recorded contract.
make test PACKAGES=./reference RUN='^TestReferenceFeatureBehavior$'
# Final: both modes, using exactly the same completed test.
make test PACKAGES=./reference RUN='^TestReferenceFeatureBehavior$'
make reference RUN='^TestReferenceFeatureBehavior$'
# Full verification, including race coverage, under one 900-second deadline.
make test-all
```

`make reference` enables `-reference=true` and the `reference` build tag; it runs
both native and PostgreSQL backends, not PostgreSQL alone. The ordinary runner
visits native first. During the initial oracle-discovery iteration, use the
shared `p.backends()` loop and temporarily select only `backend.pg` (for example,
`if !backend.pg { continue }`) so an unimplemented native feature cannot prevent
PostgreSQL observations. Remove that discovery filter before quick validation
and final verification. Never commit a filter that bypasses either backend.

There is no automatic golden-file recorder or PostgreSQL-only make target.
Use failure diagnostics or temporary verbose-only observation logging to inspect
unknown PostgreSQL results, then encode those results as explicit expectations
and remove discovery scaffolding. `p.step` validates expectations; it does not
learn or update them. Do not weaken assertions to accommodate native failures.
Docker/image failures in an explicitly enabled real run MUST fail visibly, not
be skipped or mocked. Fix slow/hanging tests rather than increasing deadlines.

## How the dual-mode fixture works

`newReferenceScenario(t)` creates a fresh native engine, a middleware-wrapped
native pool and an unwrapped pool for native controls. In quick mode PostgreSQL
is absent. In real mode it also opens the pinned PostgreSQL pool. The fixture
owns a 30-second context and pool cleanup; real fixtures reset `public` before
and after each case. All real fixtures share one bounded-lifecycle container:
**do not use `t.Parallel()`**.

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

### Common helper reference

These helpers are package-local and directly available to `reference_test` files.
No additional helper imports or fixture setup are needed.

| Helper | Contract and use |
| --- | --- |
| `newReferenceScenario(t) *referencePair` | Required entry point for new dual-mode tests; honors `-reference` and uses a fresh native engine. Do not use legacy `newReferencePair(t)`, which always starts Docker. |
| `p.step(t, s)` | Execute and independently assert the same step on available backends, then compare observations. |
| `p.backends()` | Returns backend descriptors with `name`, `db` (`*sql.DB`), `recorder`, and `pg` (true only for PostgreSQL). Loop once over these and share the callback. |
| `p.ctx`, `p.native`, `p.nativeConnector` | Bounded scenario context; unwrapped native control pool; fresh native connector for public connector controls. `p.memory` and `p.postgres` are the wrapped pools; `p.postgres` is nil in quick mode. |
| `referenceExec(query, count, args...) referenceStep` | Exec success with exact `RowsAffected`; DDL generally expects zero. Arguments are bound normally. |
| `referenceSelect(query, columns, types, rows, args...) referenceStep` | Query success with exact column names, `DatabaseTypeName` strings and normalized `[][]any` values. Supply metadata even for zero rows. |
| `referencePrepared(s) referenceStep` | Return the same step with Prepare followed by statement execution. This prepares anew per step; it does not prove held-statement reuse. |
| `referenceError(query, state, args...) referenceStep` | Exec failure with exact SQLSTATE, asserted through `SQLState() string`. Use structured diagnostics when testing message/field fidelity or failure stage. |
| `referenceErrorDiagnostic(query, want, args...) referenceStep` | Exec failure with a complete `referenceDiagnostic` expectation, including exact empty fields. |
| `referenceImplicitError(query, want, args...) referenceStep` | As above, filling both severity fields with `ERROR`. |
| `referencePosition(n) string` | Decimal diagnostic character position; zero becomes the absent-field empty string. PostgreSQL positions are character-based, not byte offsets. |
| `controlsReferenceDiagnostic(code, message) referenceDiagnostic` | Build a diagnostic with both severities `ERROR`, code and message; set any additional expected fields explicitly. |
| `referenceRunStep(t, ctx, executor, recorder, s) referenceObserved` | Run the standard assertions on a chosen backend/session. Executor accepts `*sql.DB`, `*sql.Conn` or `*sql.Tx`; pass that backend's recorder. Returns observed state, diagnostic, count, columns, types, rows and error stage. Does not compare backends for you. |
| `referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}` | Executor wrapper for an already prepared `*sql.Stmt`, usable with `referenceRunStep`; leave `s.prepared` false, use the matching query, and close the held statement yourself. Other queries route through the embedded executor. |
| `referenceState(t, err, want) string` | Check SQLSTATE through the interface; empty `want` requires success. Useful for custom public API calls. |
| `referenceExtractDiagnostic(t, err) referenceDiagnostic` | Extract structured public native/pq diagnostics; validates diagnostic shape and excludes native claims of PostgreSQL C source locations. Useful while discovering oracle data. |
| `referenceAssertDiagnostic(t, err, &want) referenceDiagnostic` | Assert the complete structured diagnostic for custom calls. Does not accept a nil expectation. |
| `referenceNormalize(value) any` | Converts `[]byte` to string and `time.Time` to UTC without monotonic/location identity; leaves other values unchanged. Standard steps apply this automatically. |
| `noticeReferencePool(t, p, postgres, &records)` | Return a notice-enabled pool, pinned connection and recorder; append complete notice diagnostics to `records`. Select the backend using `backend.pg`; fixture registers cleanup. |
| `roleReferenceCleanup(t, names)` | Register cleanup for real PostgreSQL roles created by the test **before constructing the scenario**, so pool closure and schema reset happen before role removal. Public-schema resets do not remove cluster roles. |

### Step fields and expected data

- `referenceStep.query`, `args`, `prepared`, `selectRows`, `affected`, `state`,
  `columns`, `types` and `rows` are populated by the constructors above.
- Set `s.bag = true` for unordered results: order is ignored but duplicate
  occurrences must match. Otherwise add SQL `ORDER BY` when ordering is part of
  the expected result; never depend on traversal order.
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

Run `make reference RUN='^TestReferenceFeatureAnswer$' VERBOSE=1` first, then
`make test PACKAGES=./reference RUN='^TestReferenceFeatureAnswer$'`. After any
implementation changes, rerun both and the full applicable suites.

## Optional last-resort implementation materials

This guide is sufficient for ordinary dual-mode tests. Do **not** read other
feature tests to discover the basic convention. Only when a required advanced
fixture behavior is not covered above, optionally consult:

- `reference/fixture_test.go`: step runner, diagnostic, notice and control helpers.
- `reference/fixture_lifecycle_test.go`: mode selection, backend descriptors and
  container lifecycle.
- `Makefile`: non-standard command details.

These are last-resort implementation references, not prerequisites for each test.
