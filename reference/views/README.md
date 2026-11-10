# reference/views

Dual-mode scenarios for views, materialized views, rewrite rules and definition
introspection. ALTER TABLE effects belong to [alter](../alter/README.md#testing);
wire routes to [sessions](../sessions/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses default options.

- Helpers: `ruleRoutes`/`ruleRun` (`rule_helpers_test.go`), `viewCheckRoutes`/
  `viewCheckRun` (`view_check_helpers_test.go`), `recursiveViewReference`
  (`recursive_view_helpers_test.go`) and `deparseReviewNullableColumns`
  (`deparse_constraints_metadata_helpers_test.go`). The route helpers pass a
  `held` flag: direct Query and a held prepared Query can expose different result
  descriptions for rule actions, so keep both routes and never relabel a
  repeated direct Query as prepared.
- `materializedStorageRoutes` / `materializedStorageRoutesMode` give each direct
  and held route its own scenario. The retained Conn-owned statement map and
  assertions share the actual scenario subtest owner. Storage metadata, row bags,
  transaction visibility and lock/cancellation witnesses use the common fixture;
  native controls retain genuine snapshots and replay the actual emitted SQL on
  PostgreSQL. Bulk fixtures use genuine server tablespaces for physical movement
  and explicit native builtin-boundary phases, never a synthetic placement store.
- `transactionalRulesRoutes` gives each autocommit/SQL BEGIN/Go BeginTx and
  direct/held cell its own fixture. `transactionalRulesHandles` prepares actual
  Conn-owned utility and DML handles before BEGIN, including utility targets
  created later. Keep those handles across definition changes and recovery;
  `fixture.Prepared` alone does not prove prepared consumer freshness.
- Transactional rule scenarios reuse the route helpers and observe pending lock
  requests before releasing or canceling workers.
- `*ConsumerRoutes` tests exercise the plain (non-Context) `database/sql` methods
  (`Step.NonContext`, `fixture.DeleteReferenceConsume`) for behavior that a
  Context test also covers; change both together.
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
- Recursive views: `RUN='^TestReferenceRecursiveView'`
- Temporary views: `RUN='^TestReferenceTemporaryView'`
- DROP VIEW/INDEX wrong kind: `RUN='^TestReferenceDropView'`
- Rewrite rules: `RUN='^TestReferenceRule'`
- Rewrite-rule enable modes: `RUN='^TestReferenceRuleMode'`
- Transactional rewrite-rule definitions: `RUN='^TestReferenceTransactionalRules'`
  (wire coverage lives in [sessions](../sessions/README.md#testing)).
- Materialized views (create, refresh, ALTER, extensions): `RUN='^TestReferenceMaterialized'`
- Deparse (`pg_get_constraintdef`, `pg_get_viewdef`, ...): `RUN='^TestReferenceDeparse'`
- Trigger/rule introspection: `RUN='^TestReferenceDeparseRewrite'`


Engine units: `make check RUN='^Test(Rule|ViewWrite|ViewCheck|Materialized|TemporaryView|CreateTemporaryView|TransactionalSchemaAdmission|TransactionalMaterializedRefresh|CatalogSourceEmptyMaterializedRows)'`.
