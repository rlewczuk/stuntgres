# reference/alter

ALTER TABLE actions: ADD/DROP COLUMN, ADD CONSTRAINT (including NOT VALID,
replacement and mixed RESTRICT/CASCADE drops), SET/DROP DEFAULT (with inheritance
recursion), SET/DROP NOT NULL, VALIDATE CONSTRAINT, identity column options, column TYPE changes
(including identity columns under savepoints), RENAME, SET SCHEMA, SET TABLESPACE,
SET ACCESS METHOD, clustering preferences, OID-removal compatibility syntax and
constraint rename, plus foreign-key callers of DROP COLUMN and SET SCHEMA.
CHECK/FK declaration enforcement, FK enforcement toggles and atomic CHECK
replacement share exact flags, diagnostics, historical scans, logical trigger
lifecycle, dependencies, locking and native persistence scenarios. Physical
PostgreSQL pages, WAL/crash durability and the internal PostgreSQL trigger
executor are outside the logical engine/API contract.
ALTER CONSTRAINT scenarios cover guarded bare requests, persistent FK timing
and enforcement, NN inheritability's direct-child localization versus recursive
construction, exact diagnostics, current authority, atomic failures, held
metadata/DML, deterministic cancellation and native replay. Invalid
noninheritable NN replay uses a real atomic post-data declaration, not a
privileged catalog UPDATE.
Column statistics scenarios cover nullable targets, ordered distinctness options
with SQL-integer semantic coercion and preserved quoted/Float spelling,
exact subnormal acceptance versus ERANGE, whole-list transformation precedence,
ANALYZE/planner refresh, measured MCV ceilings, actual inherited consumption,
hierarchy locality, transactional ownership, cancellation, held consumers and
native logical replay through public APIs.
Transactional table/schema integration covers CREATE/ALTER/DROP lifecycle,
private catalog/dependency visibility and atomic DDL/DML publication, savepoints,
held prepared consumers, utility boundaries, conflict scheduling and native
controls. Dependency, grouped and mixed TEMP/permanent DROP routes retain both
SQL and Go controls, direct and held execution, and commit/rollback outcomes;
schema-drop admission retains captured names and identities across waits.
Related families: CHECK, NOT NULL and standalone table DDL in `reference/tables`;
identity, defaults and `SET/DROP EXPRESSION` in `reference/sequences`;
query-derived table creation in `reference/query_core`; wire-protocol routes in
`reference/sessions`; standalone catalog and transaction families in
`reference/catalogs` (and its sibling catalog packages) and `reference/locking_transactions`.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses default options.

- Scenario helpers: `addConstraintScenario` and the catalog/backing/Dump
  snapshot helpers (`alter_add_constraint_helpers_test.go`),
  reused by the validation scenarios with held commands, full historical
  diagnostics, hierarchy validity/provenance, lock admission and native controls;
  `alterTablespaceScenario` (`alter_tablespace_helpers_test.go`),
  `constraintRenameScenario` (`constraint_rename_helpers_test.go`),
  `referenceDDLNativeDump`/`referenceDDLPositionError`
  (`alter_ddl_controls_helpers_test.go`) and `referenceSchemaSequenceState`
  (`alter_schema_helpers_test.go`).
- Access-method scenarios own database-global AM cleanup as well as pinned
  sessions. Emitted-SQL replay on PostgreSQL deliberately leaves native snapshot
  comments uninterpreted: assert unpopulated MVs before normal REFRESH of the
  originally populated recipe. Native Restore instead preserves captured rows
  and population flags without REFRESH. Replay cleanup runs on failure too,
  before the next backend setup.
- `dcrScenario` and `dcr*` helpers (`alter_drop_column_routine_helpers_test.go`)
  share notice-enabled direct/held routine dependency scenarios, exact attribute
  edge bags and native-control phases. Each column action owns its notice group;
  failure of a later action does not erase an already reported group.
  Precision retains a held survivor call across leading/trailing and depended
  column compaction. The survivor has both a body and a default edge to the
  remaining attribute: omitted/default calls still return 8, explicit 17 returns
  25, and both physical dependency rows remain. The shared cascade survivor
  assertions carry this witness through transaction/savepoint and native-control
  phases without changing the retired closure or notice expectations.
  `TestReferenceAlterDropColumnRoutineOwnedSequence` independently covers the
  sequence-only body/view path for both an ordinary OWNED BY/default sequence
  and an identity sequence. Direct/held RESTRICT preserves full identity/edge
  bags and stable attributes after leading compaction; rollback/commit CASCADE
  retires the recursive closure once while other-attribute and other-table
  sequence owners survive. Discover its full diagnostics/notices first with
  `make check REFERENCE=1 RUN='^TestReferenceAlterDropColumnRoutineOwnedSequence$'`,
  then run the same selector natively before review.
- `alterTypeIdentityOldRows` (`alter_type_identity_savepoints_test.go`) checks a
  retained pre-ALTER cursor (metadata, scan, exhaustion, `Rows.Err`, Close); it is
  shared by `alter_type_identity_test.go` and
  `alter_type_identity_source_routes_test.go`.
- `*SourceConsumerRoutes` tests rerun a Context test's corpus through plain
  `database/sql` methods (`Step.NonContext`). Both consume the same expected
  cases (for example `alter_type_source_cases_test.go`); change them together.
- `atsPhysical` (`alter_tablespace_physical_test.go`) prepares real tablespaces
  on the oracle: it creates their directories through `fixture.Container.Exec`,
  registers cleanup (drop, then `rmdir`), and only then creates the tablespaces.
- Transactional schema helpers stay with their lifecycle/control/concurrency
  scenarios: `transactionalSchemaRun`, catalog closure and rollback-route helpers,
  authority/notice/read-only assertions, physical identity and pending-lock
  observers, no-retarget, abandonment and failed-Restore witnesses. Restore
  reader failures reuse `addConstraintFailedReader`. Each serial route owns its
  scenario and schema/role cleanup; workers register cancellation and bounded
  joins before handles close. Assertions precede backend comparison.
- Clustering scenarios assert complete malformed-index/trailing-input diagnostics
  and direct/Prepare error stages, with unchanged preference and ordered data.
  Mixed inheritance failures combine recursive column work, root-only clustering
  and OID compatibility with descendant CHECK validation; both execution routes
  retain root/child columns, rows, constraints and complete index/preference bags.
  Native Restore rejects unqualified, conditional, ONLY, multi-action, clear and
  OID preference replay forms, preserving the destination dump preimage,
  sentinel, temporary rows and held permanent/temporary row snapshots per case.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- ADD COLUMN: `RUN='^TestReferenceAlterAddColumn'`
- ADD CONSTRAINT: `RUN='^TestReferenceAlterAddConstraint'`
- VALIDATE CONSTRAINT: `RUN='^TestReferenceAlterValidateConstraint'`
- Column statistics: `RUN='^TestReferenceAlterStatistics'`
- Constraint enforcement: `RUN='^TestReferenceConstraintEnforcement'`
  Review counterexamples are independently selected by the same prefix:
  `QueuedHistory`, `UnattachedDiagnostics`, `PropertyOrder`, `GeneratedNames`,
  `DropNotNullNames` and `UsingIndexDiagnostics`.
  They cover final-disabled historical obligations, utility attribute errors and
  IF NOT EXISTS skipping, supported nondeferred property orders, and authentic
  generated-name collision/ordering/locking outcomes. Run each changed definition
  in native and `REFERENCE=1` modes.
- Deferred constraint ALTER admission, FK event eligibility and timing: `RUN='^TestReferenceDeferred(ForeignKey(Alter|EventEligibility)|ConstraintAlterSafety)'`
- Constraint modes: `RUN='^TestReferenceAlterConstraint'`
- SET/DROP DEFAULT: `RUN='^TestReferenceAlterDefault'`
- DROP COLUMN: `RUN='^TestReferenceAlterDropColumn'`
- DROP COLUMN routine dependency closure, identity, rollback, admission and controls:
  `RUN='^TestReferenceAlterDropColumnRoutine'`
- Identity options: `RUN='^TestReferenceAlterIdentity'`
- SET/DROP NOT NULL: `RUN='^TestReferenceAlterNullability'`
- RENAME: `RUN='^TestReference(Smoke)?AlterRename'`
- SET SCHEMA: `RUN='^TestReference(Smoke)?AlterSchema'`
- SET TABLESPACE: `RUN='^TestReferenceAlterTablespace'`
- Logged/unlogged identity: `RUN='^TestReferenceAlterPersistence'`
- Relation access methods: `RUN='^TestReferenceAlterAccessMethod'`
- Clustering preferences: `RUN='^TestReferenceAlterCluster'`
- Column TYPE: `RUN='^TestReference(Smoke)?AlterType'`
- Constraint rename: `RUN='^TestReferenceConstraintRename'`
- Foreign-key callers: `RUN='^TestReferenceForeignKey[A-Za-z]*Caller$'`
- Transaction-private ALTER chains/actions: `RUN='^TestReferenceTransactionalSchema(Alter|Actions)$'`
- Transaction-private lifecycle and rollback: `RUN='^TestReferenceTransactionalSchema(Flyway|Lifecycle|Savepoints)$'`
- Dependency closure and DROP routes: `RUN='^TestReferenceTransactionalSchema(Dependencies|DependencyDropRoutes)$'`
- Diagnostics, utility boundaries and held consumers: `RUN='^TestReferenceTransactionalSchema(Diagnostics|UtilityBoundaries|Prepared)$'`
- DDL conflict scheduling and captured admission: `RUN='^TestReferenceTransactionalSchema(Locks|NamespaceConflicts|QuotedNameConflicts|CompanionConflicts|DropAdmission)$'`
- Native controls and physical lifecycle: `RUN='^TestReferenceTransactionalSchemaNativeControls$'`

Engine and parser units: `make check RUN='^TestAlter(AddConstraint|ValidateConstraint|Tablespace)'`.
