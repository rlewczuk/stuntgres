# reference/tables

Table definitions and relationships: CREATE TABLE core, relation storage
options, CHECK and NOT NULL constraints, foreign keys and referential actions,
and foreign tables (SQL/MED metadata, scans, writes, IMPORT FOREIGN SCHEMA, user
mappings). Related families: inheritance, typed tables (`OF type`) and LIKE in
[reference/inheritance](../inheritance/README.md); partitioning in
[reference/partitions](../partitions/README.md); temporary tables, defaults,
identity and generated columns in `reference/sequences`; ALTER TABLE actions in
`reference/alter`; indexes and non-partitioned exclusion constraints in
`reference/indexes`.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses default options.
The rules below also apply to `reference/inheritance` and `reference/partitions`.

- Scenario helpers: `relationOptionsScenario` (`relation_options_helpers_test.go`).
- Foreign tables use no canned rows. Natively, `newFDWSQLReader`
  (`fdw_reader_test.go`) serves an independent in-process stuntgres database
  (DSN `fdw-independent/<test>`) through wrappers registered with
  `fixture.NewScenarioWithDriverConfig`; on PostgreSQL the same SQL runs against a
  `postgres_fdw` loopback server (`127.0.0.1:5432`, database
  `stuntgres_reference`), whose user mappings need `password_required 'false'`.
  Use `fdwScenario`/`fdwWriteScenario`/`fdwConfiguredScenario`
  (`fdw_fixture_test.go`) and `fdwHeld` (`fdw_lifecycle_helpers_test.go`).
  `fdwCleanup` runs on the case's own connection under a fresh 15-second
  context (ROLLBACK, RESET ROLE, then drops), so it still works after the case
  context is canceled.
  Typed query scenarios (`fdw_pushdown*_test.go`) deparse the provider's bound
  occurrence/slot tree into genuine SQL against these independent sources.
  Their quoted source identifiers differ from foreign names. Direct/prepared
  public results are independent expectations; selected-stage observations only
  supplement them. Absent/declined paths and unsafe semantic frontiers retain
  real base scans. Source cursor resources and fault/cancellation hooks reuse
  `fdwSQLScan`, rather than a canned composite-row implementation.
  Canceled direct/prepared INSERT SELECT consumes an admitted composite source,
  leaves its local target sentinel unchanged and publishes no affected result.
  Provider-level changed-parameter ReScan reopens a genuine source SQL cursor;
  owning `exec/enginetests` separately exercises engine Stage.Rewind and its
  current-context parameter export, selection/cache reset and error ownership.
  Async scenarios (`fdw_async_*_test.go`) use a test-only HTTP provider that
  advances independent real source SQL cursors. PostgreSQL uses two loopback
  servers and an advisory-lock gate in a volatile fetched target; admission
  observes blocked slow FETCH and completed ready FETCH, never cursor creation.
  Native HTTP response delivery is gated after selecting a real source row.
  Every gated worker is released/canceled/joined on exit, and cancellation must
  join before the external gate is released.
  Repeated-invocation scenarios release the external gate and inspect live
  resource high-water bounds, distinguishing optional async requests from
  synchronous HTTP reads and live cursors from cumulative ReScan generations.
- Native-control phases replay their actual Dump output, unmodified, on
  PostgreSQL. Roles named by the dump must already exist there: Dump serializes
  their object/schema ACLs, not role definitions or memberships. Register
  same-session `RESET ROLE`, `RESET SESSION AUTHORIZATION` and `RESET search_path`
  cleanup before replaying.
  Wrapper/server USAGE and foreign relation/column ACLs roundtrip as exact named
  raw ACL repairs with independent inquiry outcomes. Capture invokes no provider
  callbacks and replacement preserves the real catalog graph. Routine handler
  inquiries read the actual EXECUTE ACL even when the handler is metadata-only.
- BPCHAR cells scan as strings on both clients, but `ScanType` is `string`
  natively and `interface{}` on pq.
- Compare catalog OIDs relationally (joins, captured values); allocator numbers
  can repeat across Reset/Restore.
- On PostgreSQL the rebuilt `public` grants no USAGE to PUBLIC: grant it to
  restricted actors before asserting table- or column-level denial.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- CREATE TABLE core: `RUN='^TestReferenceTableCore'`
- Storage options: `RUN='^TestReferenceRelationOptions'`
- CHECK constraints: `RUN='^TestReference(Smoke)?Check'`
  `TestReferenceCheckTableoidBoundary` retains PostgreSQL's positive CHECK
  witness and the native direct/prepared rejection with table/constraint
  publication guards; ordinary query `tableoid` remains shared-positive.
- NOT NULL constraints: `RUN='^TestReference(Smoke)?(Drop)?NotNull'`
- Foreign keys (also selects alter/dml callers): `RUN='^TestReferenceForeignKey'`
- Referential actions: `RUN='^TestReferenceForeignActions'`
- Constraint timing (including nested utilities and procedural transaction lifetime), FK completion and native controls:
  `RUN='^TestReferenceDeferred(Constraint|ForeignKey)'`
- BEFORE statement foreign-operation boundaries (shared selector with the view
  scenarios in `reference/operators`):
  `RUN='^TestReferenceTriggerBeforeStatementViewForeign$'`.
- Foreign tables: `RUN='^TestReferenceFDW'`
  `TestReferenceFDWRoutineSourceCompaction` exercises six real foreign-target
  INSERT/UPDATE/DELETE source forms with direct/held routine calls, exact rows,
  dependency bags, RESTRICT, remote/local rollback and native controls.
  `TestReferenceFDWRoutineInputCompaction` drops only local FOREIGN TABLE metadata
  over unchanged real remote rows, preserving literal 7 through the frozen input
  bridge, genuine scan lifecycle and later progress. Both use real native SQL
  handlers and PostgreSQL `postgres_fdw`; select each exact name in native and
  `REFERENCE=1` modes. Registered-handler controls remain native-only, never
  translated into synthetic PostgreSQL replay.
  `TestReferenceFDWConversion` retains ordinary conversion cases and composes a
  frozen routine's original aliases with local-only foreign-column compaction.
  Direct/preheld calls retain 7; invalid remote text has two-line Context
  `column "picked" of foreign table "f"` followed by
  `SQL function "fdw_alias_conversion_run" statement 1`. Restore/progress checks
  retain the unchanged three-column remote row and balanced actual scan ownership.
- Async foreign scans: `RUN='^TestReferenceFDWAsync'`
- `pg_user_mapping`: `RUN='^TestReferencePGUserMapping'`

Also in this package: `make check RUN='^TestReferenceSeriesCheckRestrictions$'`
and the migrated smoke tests `make check RUN='^TestReferenceSmoke(Check|NotNull|Drop)'`.
Engine and parser units: `make check RUN='^Test(RelationOptions|ForeignKey|ForeignActions|FDW)'`.
