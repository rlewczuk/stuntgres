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
- Handlerless foreign metadata is distinct from executable foreign sources.
  Metadata reads use independent identities and masks without implying a connection.
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
  Typed query helpers execute real independent source SQL. Async helpers advance
  actual source cursors over a test-only HTTP provider; PostgreSQL uses loopback
  servers. Observe real FETCH admission rather than cursor creation. Every gated
  worker is released/canceled/joined on exit, and cancellation joins before an
  external gate is released. Track live resources separately from cumulative
  reopen generations.
- Genuine external PostgreSQL scenarios use the single pgconn adapter in
  [fixture](../fixture/README.md#shared-genuine-external-sql-adapter), shared with
  dblink. Registration/contract phases are Docker-free; physical phases require
  `REFERENCE=1` and genuine external resources. Credentials and physical
  authentication observations remain separate from catalog/provider metadata.
  Copy borrowed descriptors/cells before advancing, including empty tuple results.
  Assertions inside active Go callbacks return errors to unwind; never call
  `testing.T.Fatal`/`FailNow` or capture a parent T for callback failure.
  Raw PostgreSQL BYTEA text is decoded once when exporting native driver values.
  Host establishment errors preserve errors.Is/As without fabricated SQLState.
  Explicit empty conninfo must not fall back to the fixture DSN.
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

- Table core: `RUN='^TestReferenceTableCore'`
- Storage options: `RUN='^TestReferenceRelationOptions'`
- Constraints: `RUN='^TestReference(Smoke)?(Check|NotNull)'`
- Foreign keys/actions: `RUN='^TestReferenceForeign(Key|Actions)'`
- Deferred constraints: `RUN='^TestReferenceDeferred(Constraint|ForeignKey)'`
- Foreign tables: `RUN='^TestReferenceFDW'`
- External provider: `RUN='^(TestReferenceExternalSQLProvider|TestExternalSQLProvider)'`
- Stock providers: `RUN='^TestReference(PostgresFDW|FileFDW)'`
- Foreign analyzed execution: `RUN='^TestReferenceExplainAnalyzeForeign'`
- User mappings: `RUN='^TestReferencePGUserMapping'`

Sampling helpers acquire real independent source records. Native bounded samples
and PostgreSQL physical samples have independently specified expectations.
