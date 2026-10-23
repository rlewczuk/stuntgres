# reference/views

Views and rewrite rules: persistent view definitions, rows, catalog links and
replay; CREATE OR REPLACE VIEW; automatically updatable views and WITH CHECK
OPTION; recursive and temporary views; DROP VIEW wrong-kind errors; `CREATE
RULE` rewrite rules; materialized views (create, REFRESH including CONCURRENTLY,
ALTER, extension edges); `pg_get_*def` deparse of constraints, relations and
indexes. Related families: ALTER TABLE effects on dependent views in
`reference/alter`. `^TestReferenceView` also selects the `pg_locks` view tests
(`ViewLocks*`) in `reference/sessions`.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses default options.

- Helpers: `ruleRoutes`/`ruleRun` (`rule_helpers_test.go`), `viewCheckRoutes`/
  `viewCheckRun` (`view_check_helpers_test.go`), `recursiveViewReference`
  (`recursive_view_helpers_test.go`) and `deparseReviewNullableColumns`
  (`deparse_constraints_metadata_helpers_test.go`). The route helpers pass a
  `held` flag: direct Query and a held prepared Query can expose different result
  descriptions for rule actions, so keep both routes and never relabel a
  repeated direct Query as prepared.
- `*ConsumerRoutes` tests exercise the plain (non-Context) `database/sql` methods
  (`Step.NonContext`, `fixture.DeleteReferenceConsume`) for behavior that a
  Context test also covers; change both together.
- `TestReferenceDeparseConstraintsReviewIdentityControls` gives each identity
  case its own numbered subtest and scenario so no case outgrows the 30-second
  scenario context; keep new cases in that shape.
- Concurrent-refresh unique violations on PostgreSQL carry an SPI-generated
  Context. `concurrentRuntimeExecutor` (`materialized_refresh_concurrent_oracle_test.go`)
  validates its exact shape and clears only that field.
- Raw cells differ by client: OID is native `int64`, pq bytes; DATE and TIME are
  native strings, pq `time.Time` (±infinity DATE as bytes). pq cannot build the
  `time.Time` for TIMETZ displacement endpoints, so those tests read TEXT.
- Files tagged `//go:build reference` hold legacy oracle-only tests (run only with
  `REFERENCE=1`); migrate them to untagged dual-mode tests when working there.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Persistent views (all `View*` families): `RUN='^TestReferenceView'`
- CREATE OR REPLACE VIEW: `RUN='^TestReferenceViewReplace'`
- Updatable views: `RUN='^TestReferenceViewUpdatable'`
- WITH CHECK OPTION: `RUN='^TestReferenceViewCheck'`
- <a id="recursive-view-contract"></a>Recursive views: `RUN='^TestReferenceRecursiveView'`
- Temporary views: `RUN='^TestReferenceTemporaryView'`
- DROP VIEW/INDEX wrong kind: `RUN='^TestReferenceDropView'`
- <a id="rewrite-rules"></a>Rewrite rules: `RUN='^TestReferenceRule'`
- Materialized views (create, refresh, ALTER, extensions): `RUN='^TestReferenceMaterialized'`
- Deparse (`pg_get_constraintdef`, `pg_get_viewdef`, ...): `RUN='^TestReferenceDeparse'`

Engine units: `make check RUN='^Test(Rule|ViewWrite|ViewCheck|Materialized|TemporaryView|CreateTemporaryView)'`.
