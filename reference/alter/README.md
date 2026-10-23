# reference/alter

ALTER TABLE actions: ADD/DROP COLUMN, ADD CONSTRAINT (including NOT VALID,
replacement and mixed RESTRICT/CASCADE drops), SET/DROP DEFAULT (with inheritance
recursion), SET/DROP NOT NULL, identity column options, column TYPE changes
(including identity columns under savepoints), RENAME, SET SCHEMA, SET TABLESPACE
and constraint rename, plus foreign-key callers of DROP COLUMN and SET SCHEMA.
Related families: CHECK, NOT NULL and table DDL in `reference/tables`; identity,
defaults and `SET/DROP EXPRESSION` in `reference/sequences`.

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

Engine and parser units: `make check RUN='^TestAlter(AddConstraint|Tablespace)'`.
