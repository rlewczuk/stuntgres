# reference/ddl

Dual-mode tests for catalog-publication contracts that span multiple DDL
statements in one transaction: journal fills, catalog-overlay reowning and type
identity replacement interacting with column metadata.

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

Family selector: `make check RUN='^TestReferenceEnumAlterFillReown'` (add
`REFERENCE=1` for the oracle).