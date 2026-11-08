# reference/ddl

Dual-mode tests for catalog-publication contracts that span multiple DDL
statements in one transaction: journal fills, catalog-overlay reowning, type
identity replacement interacting with column metadata, inline `PRIMARY KEY`
column constraints added by `ALTER TABLE ... ADD COLUMN`, ordinary
`ALTER TABLE ... IF EXISTS` actions, and session-owned query-derived tables.

Related families: enum values and `ALTER TYPE` coverage stay in
[reference/types](../types/README.md#testing); `ALTER TABLE` action coverage in
[reference/alter](../alter/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` is plain
`fixture.Main(m)`, so the pinned PostgreSQL 18.6 oracle runs only in
`REFERENCE=1`.

- `TestReferenceEnumAlterFillReown` runs every scenario on both backends and
  both execution routes (direct and prepared) from one body; route-suffixed
  object names let the two routes share one schema. Error steps assert the full
  execute-phase diagnostic and then roll back.
- `TestReferenceEnumAlterFillReownDriver` is the explicit native driver surface:
  `stuntgres.NewDriver().OpenConnector` plus `sql.OpenDB` and `db.BeginTx`, with
  direct and `tx.PrepareContext` routes. Completing the binary is the witness
  that the transaction no longer aborts the process.
- `TestReferenceEnumAlterFillReownFeatureSmoke` is the new-behavior smoke: the
  exact replacement repro, one `ADD VALUE` variant and the enum-array variant,
  direct route.
- `TestReferenceAlterAddColumnPrimaryKey` runs both backends and both execution
  routes from one body: catalog publication, same-column UNIQUE/PRIMARY KEY
  dedup and name transfer, skipped declarations, the coder migration forms,
  boundaries, inheritance, atomicity and the feature smoke.
- `TestReferenceAlterTableIfExists` runs both backends and both execution
  routes from one body: every implemented ordinary action under relation-level
  `IF EXISTS`, the standalone one-default and inheritance forms, the single
  missing-relation NOTICE (including absent schemas and execute-time prepared
  skips), a multi-action list compared against its plain spelling, existing
  relation renames and the round-2 feature smoke with the coder 000245 ALTER
  forms.
- `TestReferenceCreateTempTableAs` shares direct/prepared creation, isolation,
  commit actions, rollback, producer suppression, migration, destination
  admission/settings and restricted-maintenance scenarios.

Family selector: `make check RUN='^TestReferenceEnumAlterFillReown'` (add
`REFERENCE=1` for the oracle).

Family selector: `make check RUN='^TestReferenceAlterAddColumnPrimaryKey'` (add
`REFERENCE=1` for the oracle).

Family selector: `make check RUN='^TestReferenceAlterTableIfExists'` (add
`REFERENCE=1` for the oracle).

Family selector: `make check RUN='^TestReferenceCreateTempTableAs'` (add
`REFERENCE=1` for the oracle).
