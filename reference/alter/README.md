# reference/alter

ALTER TABLE actions: ADD/DROP COLUMN, ADD CONSTRAINT (including NOT VALID,
replacement and mixed RESTRICT/CASCADE drops), SET/DROP DEFAULT (with inheritance
recursion), SET/DROP NOT NULL, identity column options, column TYPE changes
(including identity columns under savepoints), RENAME, SET SCHEMA, SET TABLESPACE
and constraint rename, plus foreign-key callers of DROP COLUMN and SET SCHEMA.
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
`reference/catalogs` and `reference/locking_transactions`.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses default options.

- Scenario helpers: `addConstraintScenario` and the catalog/backing/Dump
  snapshot helpers (`alter_add_constraint_helpers_test.go`),
  `alterTablespaceScenario` (`alter_tablespace_helpers_test.go`),
  `constraintRenameScenario` (`constraint_rename_helpers_test.go`),
  `referenceDDLNativeDump`/`referenceDDLPositionError`
  (`alter_ddl_controls_helpers_test.go`) and `referenceSchemaSequenceState`
  (`alter_schema_helpers_test.go`).
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

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- ADD COLUMN: `RUN='^TestReferenceAlterAddColumn'`
- ADD CONSTRAINT: `RUN='^TestReferenceAlterAddConstraint'`
- SET/DROP DEFAULT: `RUN='^TestReferenceAlterDefault'`
- DROP COLUMN: `RUN='^TestReferenceAlterDropColumn'`
- Identity options: `RUN='^TestReferenceAlterIdentity'`
- SET/DROP NOT NULL: `RUN='^TestReferenceAlterNullability'`
- RENAME: `RUN='^TestReference(Smoke)?AlterRename'`
- SET SCHEMA: `RUN='^TestReference(Smoke)?AlterSchema'`
- SET TABLESPACE: `RUN='^TestReferenceAlterTablespace'`
- Column TYPE: `RUN='^TestReference(Smoke)?AlterType'`
- Constraint rename: `RUN='^TestReferenceConstraintRename'`
- Foreign-key callers: `RUN='^TestReferenceForeignKey[A-Za-z]*Caller$'`
- Transaction-private ALTER chains/actions: `RUN='^TestReferenceTransactionalSchema(Alter|Actions)$'`
- Transaction-private lifecycle and rollback: `RUN='^TestReferenceTransactionalSchema(Flyway|Lifecycle|Savepoints)$'`
- Dependency closure and DROP routes: `RUN='^TestReferenceTransactionalSchema(Dependencies|DependencyDropRoutes)$'`
- Diagnostics, utility boundaries and held consumers: `RUN='^TestReferenceTransactionalSchema(Diagnostics|UtilityBoundaries|Prepared)$'`
- DDL conflict scheduling and captured admission: `RUN='^TestReferenceTransactionalSchema(Locks|NamespaceConflicts|QuotedNameConflicts|CompanionConflicts|DropAdmission)$'`
- Native controls and physical lifecycle: `RUN='^TestReferenceTransactionalSchemaNativeControls$'`

Engine and parser units: `make check RUN='^TestAlter(AddConstraint|Tablespace)'`.
