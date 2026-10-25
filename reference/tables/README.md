# reference/tables

Table definitions and relationships: CREATE TABLE core and LIKE, typed tables
(`OF type`), relation storage options, CHECK and NOT NULL constraints, foreign
keys and referential actions, inheritance (creation, queries, DML, ALTER
recursion, INHERIT/NO INHERIT maintenance), partitioning (RANGE/LIST/HASH,
ATTACH/DETACH, pruning, partitioned UNIQUE/PRIMARY KEY and exclusion
constraints, inherited partition constraints) and foreign tables (SQL/MED
metadata, scans, writes, IMPORT FOREIGN SCHEMA, user mappings). Related
families: temporary tables, defaults, identity and generated columns in
`reference/sequences`; ALTER TABLE actions in `reference/alter`; indexes and
non-partitioned exclusion constraints in `reference/indexes`; composite types in
`reference/types`; inherited row-lock rechecks in `reference/locking_transactions`.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses default options.

- Scenario helpers: `relationOptionsScenario` (`relation_options_helpers_test.go`),
  `inheritanceScenario` (`inheritance_helpers_test.go`), `typedTableScenario`
  (`typed_helpers_test.go`), `referenceRangeScenario`
  (`range_partition_fixture_test.go`), `referencePruningScenario`
  (`partition_pruning_fixture_test.go`), `partitionConstraintCase`
  (`partition_constraints_fixture_test.go`), `partitionExclusionScenario`/`Case`
  (`partition_exclusion_fixture_test.go`) and `partitionInheritedScenario`
  (`partition_inherited_fixture_test.go`).
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
  PostgreSQL. Roles named by the dump must already exist there (pass them as
  `inheritanceScenario` replay roles) and need their schema USAGE regranted after
  each `public` reset: Dump serializes neither roles nor schema ACLs. Register
  same-session `RESET ROLE`, `RESET SESSION AUTHORIZATION` and `RESET search_path`
  cleanup before replaying.
  Wrapper/server USAGE ACLs roundtrip as exact named raw ACL repairs; foreign
  relation/column ACLs retain their no-output preflight refusal. Routine handler
  inquiries read the actual EXECUTE ACL even when the handler is metadata-only.
- BPCHAR cells scan as strings on both clients, but `ScanType` is `string`
  natively and `interface{}` on pq.
- Compare catalog OIDs relationally (joins, captured values); allocator numbers
  can repeat across Reset/Restore.
- On PostgreSQL the rebuilt `public` grants no USAGE to PUBLIC: grant it to
  restricted actors before asserting table- or column-level denial.
- Query results do not prove that pruned branches or ATTACH validation scans
  were skipped. Engine units assert that natively; on PostgreSQL, ATTACH scan
  elision is read from `pg_stat_user_tables.seq_scan` on the same session after
  `pg_stat_force_next_flush()` and `pg_stat_clear_snapshot()`, calibrated against
  an ATTACH without proof (`partition_attach_proof_test.go`).

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- CREATE TABLE core: `RUN='^TestReferenceTableCore'`
- CREATE TABLE LIKE: `RUN='^TestReferenceTableLike'`
- Typed tables: `RUN='^TestReferenceTypedTable'`
- Storage options: `RUN='^TestReferenceRelationOptions'`
- CHECK constraints: `RUN='^TestReference(Smoke)?Check'`
- NOT NULL constraints: `RUN='^TestReference(Smoke)?(Drop)?NotNull'`
- Foreign keys (also selects alter/dml callers): `RUN='^TestReferenceForeignKey'`
- Referential actions: `RUN='^TestReferenceForeignActions'`
- Inheritance (creation and lifecycle; selects every `Inheritance*` family): `RUN='^TestReferenceInheritance'`
- Inherited queries: `RUN='^TestReferenceInheritanceQuery'`
- Inherited DML: `RUN='^TestReferenceInheritanceDML'`
- Inherited ALTER: `RUN='^TestReferenceInheritanceAlter'`
- INHERIT/NO INHERIT maintenance: `RUN='^TestReferenceInheritanceMaint'`
- RANGE partitions: `RUN='^TestReferenceRangePartition'`
- <a id="list-partition-definitions"></a>LIST partitions: `RUN='^TestReferenceListPartition'`
- HASH partitions: `RUN='^TestReferenceHashPartition'`
- Partition DML routing: `RUN='^TestReferencePartitionRouting'`
- ATTACH PARTITION: `RUN='^TestReferencePartitionAttach'`
- DETACH PARTITION: `RUN='^TestReferencePartitionDetach'`
- Partition pruning: `RUN='^TestReferencePartitionPruning'`
- <a id="partitioned-constraints--selected-proof"></a>Partitioned UNIQUE/PRIMARY KEY: `RUN='^TestReferencePartitionConstraints'`
- Partitioned exclusion constraints: `RUN='^TestReferencePartitionExclusion'`
- <a id="inherited-partition-constraints"></a>Inherited partition CHECK/NOT NULL: `RUN='^TestReferencePartitionInherited'`
- Foreign tables: `RUN='^TestReferenceFDW'`
- Async foreign scans: `RUN='^TestReferenceFDWAsync'`
- `pg_user_mapping`: `RUN='^TestReferencePGUserMapping'`

Also in this package: `make check RUN='^TestReferenceSeriesCheckRestrictions$'`
and the migrated smoke tests `make check RUN='^TestReferenceSmoke(Check|NotNull|Drop)'`.
Engine and parser units: `make check RUN='^Test(RelationOptions|PartitionPruning|RangePartition|HashPartition|ListPartition|PartitionAttach|PartitionDetach|PartitionConstraints|Inheritance|Typed|TableLike|ForeignKey|ForeignActions|FDW)'`.
