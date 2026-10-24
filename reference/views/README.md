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

`TestReferenceMaterializedViewKeywordTypes` uses genuine enum DROP TYPE in both
backends, preserving direct/prepared unreserved-name and namespace legality.
WITH CHECK OPTION evaluates the final base-table BEFORE replacement. Shared
cases cover INSERT/UPDATE and held statements; the separate INSTEAD OF boundary
asserts native capability rejection and preserved check enforcement, without
depending on diagnostic wording or the phase in which rejection occurs.

View definitions and materialized-view definitions support ordinary autocommit,
SQL BEGIN and Go BeginTx through direct and held prepared handles. Earlier own
DDL and source writes are visible privately; definitions, dependencies and
materialized rows commit together or are restored together by rollback and
savepoints. CREATE OR REPLACE and metadata ALTER preserve object identity and
ACLs. WITH DATA captures stored results once; WITH NO DATA does not execute the
producer and leaves the materialized view unpopulated. Ordinary and concurrent
REFRESH use the same transaction/savepoint boundaries, including private source
writes, stored rows, indexes and population state. Ordinary refresh retains
AccessExclusive until transaction end; concurrent refresh retains Exclusive,
allowing peers to read the old committed result.

- Helpers: `ruleRoutes`/`ruleRun` (`rule_helpers_test.go`), `viewCheckRoutes`/
  `viewCheckRun` (`view_check_helpers_test.go`), `recursiveViewReference`
  (`recursive_view_helpers_test.go`) and `deparseReviewNullableColumns`
  (`deparse_constraints_metadata_helpers_test.go`). The route helpers pass a
  `held` flag: direct Query and a held prepared Query can expose different result
  descriptions for rule actions, so keep both routes and never relabel a
  repeated direct Query as prepared.
- `transactionalRulesRoutes` gives each autocommit/SQL BEGIN/Go BeginTx and
  direct/held cell its own fixture. `transactionalRulesHandles` prepares actual
  Conn-owned utility and DML handles before BEGIN, including utility targets
  created later. Keep those handles across definition changes and recovery;
  `fixture.Prepared` alone does not prove prepared consumer freshness.
- Transactional rule lock scenarios observe real pending requests before release
  or cancellation. Event DDL holds AX: peer catalog inspection remains committed,
  while subsequent peer DML waits for transaction end before applying its rules.
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

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Persistent views (all `View*` families): `RUN='^TestReferenceView'`
- CREATE OR REPLACE VIEW: `RUN='^TestReferenceViewReplace'`
- Transactional view/materialized definitions: `RUN='^TestReferenceTransactionalViews'`
  (wire coverage lives in [sessions](../sessions/README.md#testing)).
- Transactional materialized refresh: `RUN='^TestReferenceTransactionalMaterializedRefresh'`
  (wire coverage lives in [sessions](../sessions/README.md#testing)).
- Updatable views: `RUN='^TestReferenceViewUpdatable'`
- WITH CHECK OPTION: `RUN='^TestReferenceViewCheck'`
- <a id="recursive-view-contract"></a>Recursive views: `RUN='^TestReferenceRecursiveView'`
- Temporary views: `RUN='^TestReferenceTemporaryView'`
- DROP VIEW/INDEX wrong kind: `RUN='^TestReferenceDropView'`
- <a id="rewrite-rules"></a>Rewrite rules: `RUN='^TestReferenceRule'`
- Transactional rewrite-rule definitions: `RUN='^TestReferenceTransactionalRules'`
  (wire coverage lives in [sessions](../sessions/README.md#testing)).
- Materialized views (create, refresh, ALTER, extensions): `RUN='^TestReferenceMaterialized'`
- Deparse (`pg_get_constraintdef`, `pg_get_viewdef`, ...): `RUN='^TestReferenceDeparse'`

Engine units: `make check RUN='^Test(Rule|ViewWrite|ViewCheck|Materialized|TemporaryView|CreateTemporaryView|TransactionalSchemaAdmission|TransactionalMaterializedRefresh|CatalogSourceEmptyMaterializedRows)'`.
