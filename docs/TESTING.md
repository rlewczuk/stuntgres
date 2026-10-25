# Adding and maintaining tests

This guide holds the shared test policy, fixture usage and test-first workflow.
Package-specific helpers, rules and family selectors belong in the owning
package's `README.md` under `## Testing`.

## Placement policy

- Tests that exercise public APIs by issuing SQL and checking responses MUST live
  in `reference/` as **dual-mode reference tests**. This covers direct and prepared
  execution, rows, metadata, diagnostics, cancellation and SQL witnesses around
  native controls. Root, `smoke/` (legacy; only `smoke/catalog_test.go` remains),
  `internal/` and other directories are forbidden for them.
- When working in an area with offending tests, migrate them to `reference/`.
  Keep the complete assertion set and execution routes, merge with existing
  coverage, and delete the originals; leave no native-only copies.
- Files tagged `//go:build reference` hold legacy PostgreSQL-only tests that run
  only with `REFERENCE=1` (a quick-mode run never covers them). Migrate them to
  untagged dual-mode tests when working in their area.
- Internal unit tests of package functions stay beside their package. They
  supplement, never replace, reference coverage.
- Internal tests that need a composed engine are external tests
  (`package <owner>_test`) beside the package that owns the code under test,
  built with the [`enginetest`](../internal/engine/enginetest/README.md)
  helpers; statement runtime belongs to `internal/engine/core/`. New ones are
  never added to the root `internal/engine` package. A lower package with
  internal tests (the engine subpackages below core, `internal/sqltype`) keeps
  them in a test-only `enginetests/` subdirectory with a one-sentence `doc.go`:
  in the owner's own directory, `go test` would rebuild the root and every
  package in between against the owner's test variant. Internal tests cannot
  import `enginetest` (import cycle).
- Every new PostgreSQL feature needs new or changed reference coverage, and every
  compatibility bug needs a failing reference regression before its fix.
- One test body serves both modes. Mode changes which backends exist, never the
  scenario; do not maintain near-duplicate PostgreSQL and native versions.
- Use only public stuntgres APIs in `reference/` (no internal packages or
  root-private helpers). Name tests `TestReference<Feature><Behavior>`; use untagged
  `*_test.go` files so `make quickcheck` runs them.
- Assert behavior, boundaries, errors, atomicity and lifecycle, not source text,
  wiring counts, mock echoes or backend parity alone. Keep feature notices and
  their diagnostics exact; PostgreSQL DEBUG trace wording, allocator identities
  and loader order are not expectations. Keep output quiet.
- Organize tests in feature-group packages under `reference/`, with shared helpers
  in `reference/fixture`. At most 40 packages (including the fixture) and at most
  1,000,000 bytes per package; functional files at most 750 lines. Do not split or
  duplicate test bodies to meet these limits.
- Native Reset/Dump/Restore have no PostgreSQL equivalent: keep their witnesses as
  explicit native-only phases inside the same dual-mode scenario.

## Test-first workflow

1. **Write the shared scenario and run real PostgreSQL first**, using the pinned
   oracle ([fixture policy](../reference/fixture/README.md#postgresql-oracle-policy-and-provenance)),
   never a local or unpinned server. Start from independently reasoned
   expectations; use the oracle to resolve unknown values, metadata and diagnostics.
2. **Record the oracle's results as the contract**: rows, column names, database
   type names, affected counts and diagnostics. Never derive expectations from
   stuntgres output, copy native failures into goldens or assert only equality.
3. **Run quick mode** and watch the regression fail. Implement the smallest correct
   change and rerun until it passes.
4. **Run both modes selectively** for every added, changed or migrated test, again
   after any later edit to those tests or their implementation. Then run
   `make quickcheck`. Whole-project targets never replace the selected runs.

```sh
make check REFERENCE=1 RUN='^TestReferenceFeatureBehavior$'  # oracle (and native)
make check RUN='^TestReferenceFeatureBehavior$'              # quick mode
make quickcheck                                             # before review/merge
```

During discovery only, filter the shared `p.Backends()` loop to `backend.PG`
(`if !backend.PG { continue }`) so a missing native feature cannot block oracle
observation. Remove it before quick-mode validation; never commit a backend filter.
There is no golden recorder and no PostgreSQL-only mode: `p.Step` validates
expectations, it never learns them. Inspect unknown results through failure
diagnostics or temporary logging, encode them, then remove the scaffolding.

### Command details

- `check` requires `RUN` selecting individual tests or a small set; `RACE=1` adds
  the race detector. Targets impose no deadline (`-timeout=0`); agents use 900 s
  for `check` and 1800 s for `quickcheck`. `refcheck` is human-only.
- `REFERENCE=1` adds the `reference` build tag and `-reference=true`, runs **both**
  native and PostgreSQL backends, and serializes packages with `-p 1` (each package
  owns one container). `reference/cmd/checkpackages` selects packages with
  matching top-level test names; selectors containing `/` keep every package, and
  a selector that matches nothing still starts the fixture package's oracle.
  Startup failures fail visibly; they are never skipped.
- Make expands `$` in `RUN`. A `$` before `|`, `/` or `)` must be written `$$`
  (`RUN='^TestX$$/^postgres$$'`), or wrap alternatives once
  (`RUN='^(TestA|TestB)$'`). An internal `$|` silently selects nothing; a
  no-match run is not evidence.

## The dual-mode fixture

`fixture.NewScenario(t)` creates a fresh native engine with its own Driver, a
middleware-wrapped native pool, an unwrapped native control pool and, in real
mode, the pinned PostgreSQL pool. It owns a 60-second context and pool cleanup.
Real mode resets `public` before and after each case. All real fixtures in a
package share one container, so **never use `t.Parallel()`**. Each package's
`TestMain` is `os.Exit(fixture.Main(m))`; import helpers as
`"stuntgres/reference/fixture"` without dot imports or local aliases.

- **Shared catalog state.** Only `public` is reset. Register
  `fixture.SchemaReferenceCleanup(t, "name")` *before* creating the scenario when a
  case creates or moves objects into another schema (otherwise later cases fail
  with 42P06). Register `fixture.RoleReferenceCleanup(t, names)` before creating the
  scenario, on the `testing.T` that owns it, for cluster roles. Catalog queries such
  as `pg_class` are database-wide: use scenario-unique object names or constrain
  namespaces when asserting a complete candidate bag.
- **Steps vs callbacks.** Use `p.Step` for plain statement streams. Use
  `p.Backends()` with one shared callback for transactions, pinned sessions, held
  statements, richer metadata, concurrency and oracle discovery.
- **Sessions and cleanup.** Keep session state on a borrowed `*sql.Conn` or
  `*sql.Tx`. Close rows, then statements, then return connections before fixture,
  schema and role cleanup. Register cleanup immediately; cancel and join goroutines
  on every exit; synchronize deterministically, never with sleeps.
- **Long scenarios.** Never refresh or extend the 60-second scenario context. Give
  long inventories or heavy matrices one scenario per serial subtest, and run
  cleanup under its own bounded context after handles close.
- **Deadlocks.** Identify the victim from its error, not worker completion order.
  Join both workers under a bounded deadline and keep the victim, survivor and
  side-effect assertions.
- **Timeouts.** Prepare held statements and finish setup at timeout 0, then arm a
  short `statement_timeout`/`lock_timeout`. Once armed, even `SHOW` or `RESET` can
  time out: dispose of the armed owner instead of resetting it, and never raise the
  timer or retry.
- **Cancellation.** Native pre-canceled and expired routes return ordinary context
  errors. An operation can classify an elapsed source deadline before the source's
  Done notification: check the operation error independently, then wait for the
  owned context's Done and snapshot its terminal Err before cleanup cancellation.
  For an admitted pq deadline probe, require the exact `57014` user-request
  cancellation diagnostic before waiting for Done; reject unrelated SQL and
  transport errors. Validate any physical-tuple Context with an anchored grammar
  that varies only the heap coordinates, not the action, relation or cascaded SQL.
  Return context-interrupted pq borrowers instead of reusing their possibly closed
  transport. Use an independent admission/release witness rather than racing an
  observer against a 100ms interruption probe. For PostgreSQL user cancellation,
  observe real server admission, cancel, join, then check the same physical owner.
  Never assert that a fast pre-canceled pq command returns nil outputs.
- **Randomness and floats.** Test random functions through independent
  properties, never backend parity. Finite irrational FLOAT8 results may differ by
  at most 2 ULP; NaN, infinities, signs and errors stay exact.
- **Client differences.** lib/pq reports unknown nullability for every column,
  MERGE affected count 0, INT4 `ScanType` int32 (native int64), and OID/REGTYPE raw
  values as strings or bytes. Native reports known nullability for stored table and
  view columns and unknown for most computed results. Native rows are eager with
  detached `Columns` slices; pq streams. Branch on `backend.PG` only for such independently specified
  expectations, never to duplicate the scenario.
- **Throwaway programs.** Standalone smoke programs used during development are
  never committed.

### Common helpers

| Helper | Contract and use |
| --- | --- |
| `fixture.NewScenario(t) *fixture.Pair` | Entry point for new tests; honors `-reference`. Do not use legacy `fixture.NewPair(t)` (always starts Docker). |
| `fixture.NewFixture(t, withPostgres, nativeRaw)` | Low-level seam for ownership/provenance tests only. |
| `p.Step(t, s)` | Run and independently assert one step on every backend, then compare observations. |
| `p.Backends()` | Backend descriptors: `Name`, `DB`, `Recorder`, `PG`. Loop once with a shared callback. |
| `p.Ctx`, `p.Native`, `p.NativeConnector`, `p.Memory`, `p.Postgres` | Scenario context; unwrapped native control pool; native connector; wrapped pools (`p.Postgres` is nil in quick mode). |
| `fixture.Exec(query, count, args...)` | Exec success with exact `RowsAffected` (DDL usually 0); `LastInsertId` must be unsupported. |
| `fixture.Select(query, columns, types, rows, args...)` | Query success with exact column names, `DatabaseTypeName`s and normalized rows. Give metadata even for zero rows. |
| `fixture.Prepared(s)` | The same step via a fresh Prepare; does not prove held-statement reuse. |
| `fixture.Error(query, state, args...)` | Exec failure with exact SQLSTATE via `SQLState()`. |
| `fixture.ErrorDiagnostic(query, want, args...)`, `fixture.ImplicitError(...)` | Exec failure with a complete `fixture.Diagnostic` (`ImplicitError` fills both severities with `ERROR`). |
| `fixture.Position(n)` | Character (not byte) diagnostic position; 0 means absent. |
| `fixture.ControlsReferenceDiagnostic(code, message)` | Diagnostic with both severities `ERROR`. |
| `fixture.RunStep(t, ctx, executor, recorder, s)` | Run standard assertions on a chosen DB/Conn/Tx; does not compare backends. With `s.NonContext=true` it requires actual DB/Tx plain methods. |
| `fixture.HeldStatement{Executor, Statement, SQL}` | Run steps through an already prepared `*sql.Stmt` (leave `s.Prepared` false; close it yourself). The matching query uses the held statement; other queries use the underlying executor. |
| `fixture.State(t, err, want)` | Check SQLSTATE through the interface; empty `want` requires success. |
| `fixture.ExtractDiagnostic(t, err)`, `fixture.AssertDiagnostic(t, err, &want)` | Extract or assert complete structured diagnostics for custom calls. |
| `fixture.Normalize(value)` | `[]byte` → string, `time.Time` → UTC; applied automatically by steps. |
| `fixture.NoticeReferencePool(t, p, postgres, &records)` | Notice-enabled pool, pinned connection and recorder collecting complete notice diagnostics. |
| `fixture.SessionCancel(t, parent, conn, prepared, queryMode...)` | Observe PostgreSQL `pg_sleep` admission, cancel through the server, join and assert 57014. |
| `fixture.SchemaReferenceCleanup`, `fixture.RoleReferenceCleanup` | Register before the scenario (see above). |

More helpers (lock observers, active-cancel admission, role grants, COPY notices,
server fixture, driver configuration) are documented in the
[fixture README](../reference/fixture/README.md).

### Step fields

- Constructors fill `Query`, `Args`, `Prepared`, `SelectRows`, `Affected`, `State`,
  `Columns`, `Types` and `Rows`.
- `s.Bag = true` ignores row order but keeps duplicate counts; otherwise add
  `ORDER BY` when order matters. Never depend on traversal order.
- `s.CheckMetadata(t, rows)` checks raw column descriptors before iteration, even
  for empty results. `s.CheckScan(t, rows, i)` rescans the current row into
  specialized destinations and must not issue SQL.
- For Query errors use `fixture.ErrorDiagnostic` with `s.SelectRows = true`; set
  `s.ErrorStage` to `"prepare"` or `"execute"` when the stage matters.
- `s.PostgresDiagnostic` gives an independently specified PostgreSQL diagnostic
  where the oracle differs (for example a known oracle defect); both records are
  asserted in full.
- `fixture.Diagnostic` fields: `Severity`, `SeverityUnlocalized`, `Code`,
  `Message`, `Detail`, `Hint`, `Position`, `InternalPosition`, `InternalQuery`,
  `Context`, `Schema`, `Table`, `Column`, `DataType`, `Constraint`. Unset fields
  must be empty; source file/line/routine are never reference data.
- Normalized cells: integers `int64`, floats `float64`, booleans `bool`, NULL
  `nil`, byte payloads `string`. `ColumnType.ScanType` is not the scanned value.

## Self-contained example

Place an untagged file such as `reference/query_core/feature_answer_test.go` in an
existing package (new packages need their own `TestMain`):

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

Run `make check REFERENCE=1 RUN='^TestReferenceFeatureAnswer$'`, then
`make check RUN='^TestReferenceFeatureAnswer$'`.

## Allocation checks

For byte-allocation budgets of package functions, use calibrated
`testing.Benchmark` results and normalize `AllocedBytesPerOp` to one evaluation.
Short `runtime.MemStats.TotalAlloc` samples charge unrelated runtime allocations,
especially under the race detector.

## Package testing guides

Reference packages: [aggregates](../reference/aggregates/README.md#testing),
[alter](../reference/alter/README.md#testing),
[binary_json_arrays](../reference/binary_json_arrays/README.md#testing),
[catalog_objects](../reference/catalog_objects/README.md#testing),
[catalog_privileges](../reference/catalog_privileges/README.md#testing),
[catalogs](../reference/catalogs/README.md#testing),
[cursors](../reference/cursors/README.md#testing),
[datetime](../reference/datetime/README.md#testing),
[dml](../reference/dml/README.md#testing),
[domain_types](../reference/domain_types/README.md#testing),
[extension_types](../reference/extension_types/README.md#testing),
[extensions](../reference/extensions/README.md#testing),
[fixture](../reference/fixture/README.md#testing),
[geometry_network](../reference/geometry_network/README.md#testing),
[indexes](../reference/indexes/README.md#testing),
[inheritance](../reference/inheritance/README.md#testing),
[information_schema](../reference/information_schema/README.md#testing),
[locking_transactions](../reference/locking_transactions/README.md#testing),
[numeric](../reference/numeric/README.md#testing),
[object_address](../reference/object_address/README.md#testing),
[object_grants](../reference/object_grants/README.md#testing),
[operators](../reference/operators/README.md#testing),
[partitions](../reference/partitions/README.md#testing),
[patterns_encoding](../reference/patterns_encoding/README.md#testing),
[query_core](../reference/query_core/README.md#testing),
[query_order](../reference/query_order/README.md#testing),
[ranges_text_search](../reference/ranges_text_search/README.md#testing),
[roles](../reference/roles/README.md#testing),
[sequences](../reference/sequences/README.md#testing),
[session_state](../reference/session_state/README.md#testing),
[sessions](../reference/sessions/README.md#testing),
[strings](../reference/strings/README.md#testing),
[tables](../reference/tables/README.md#testing),
[temporal_types](../reference/temporal_types/README.md#testing),
[type_resolution](../reference/type_resolution/README.md#testing),
[types](../reference/types/README.md#testing),
[views](../reference/views/README.md#testing).
`datetime_formatting` and `query_outer` have no package guide. Internal packages
document unit tests in their own READMEs (for example
[parser](../internal/parser/README.md#testing)).
