# reference/inheritance

Tables defined from other relations or types: inheritance (creation, queries,
DML, ALTER recursion, INHERIT/NO INHERIT maintenance), typed tables (`OF type`)
and CREATE TABLE LIKE. Related families: table core, constraints, foreign keys
and foreign tables in [reference/tables](../tables/README.md); partitioning in
[reference/partitions](../partitions/README.md); composite types in
`reference/types`; inherited row-lock rechecks in `reference/locking_transactions`.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md) and the shared table rules in
[reference/tables](../tables/README.md#testing). `TestMain` uses default options.

- Scenario helpers: `inheritanceScenario` (`inheritance_helpers_test.go`) and
  `typedTableScenario` (`typed_helpers_test.go`).
- Roles named by a replayed native Dump are passed as `inheritanceScenario`
  replay roles so they exist on PostgreSQL before the replay.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- CREATE TABLE LIKE: `RUN='^TestReferenceTableLike'`
- Typed tables: `RUN='^TestReferenceTypedTable'`
- Inheritance (creation and lifecycle; selects every `Inheritance*` family): `RUN='^TestReferenceInheritance'`
- Inherited queries: `RUN='^TestReferenceInheritanceQuery'`
- Inherited DML: `RUN='^TestReferenceInheritanceDML'`
- Inherited ALTER: `RUN='^TestReferenceInheritanceAlter'`
- INHERIT/NO INHERIT maintenance: `RUN='^TestReferenceInheritanceMaint'`
- Inherited ALTER CHECKs share native/PostgreSQL success for `NOT ENFORCED`,
  asserting false enforcement/validity on both parent and child while retaining
  enforced sibling checks and final rows: `RUN='^TestReferenceInheritanceAlterChecks$'`.

Engine and parser units: `make check RUN='^Test(Inheritance|Typed|TableLike)'`.
