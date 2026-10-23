# reference/indexes

Indexes and index-backed constraints: access-method definitions (`CREATE ACCESS
METHOD`), operator classes and families, B-tree and hash indexes, partial,
concurrent and UNIQUE indexes, PRIMARY KEY, index clauses (expressions, INCLUDE,
collation/class, ordering), index property inquiry, maintenance (REINDEX, ALTER
INDEX, DROP INDEX and their locks), `ADD CONSTRAINT ... USING INDEX` and
exclusion constraints (including GiST). Related families: partitioned-index and
partitioned-exclusion hierarchies in `reference/tables`; ALTER TABLE ADD
CONSTRAINT in `reference/alter`.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses default options.

- Scenario helpers: `amBackends` (`access_method_test.go`), `classBackends`/
  `classConn` (`operator_class_test.go`), `familyBackends`/`familyScenario`
  (`operator_family_test.go`), `hashIndexScenario` (`hash_index_test.go`),
  `partialIndexScenario` (`partial_index_test.go`), `indexClausesScenario`
  (`index_clauses_test.go`), `indexPropertiesScenario` (`index_properties_test.go`),
  `indexMaintenanceScenario` (`index_maintenance_lifecycle_test.go`) and
  `usingIndexScenario` (`using_index_eligibility_test.go`).
- `uniqueReference*(t, standalone)` helpers run one body for a UNIQUE constraint
  (`false`) and a standalone UNIQUE index (`true`).
- Access methods are database-global and survive the `public` reset: list every
  method a scenario creates in `amBackends`, which drops them through the admin
  pool before and after the case.
- Rediscover user OIDs (classes, families, methods) by name after Restore or
  replay; never assume they are stable.
- Timeout witnesses (`HashIndexBuildAdmission`, `BtreeIndexBuildTimeouts`,
  `IndexMaintenanceLocks`) prepare the held CREATE INDEX/REINDEX/DROP, and any
  timer-reset `SET`, at timeout 0, then arm the 20–50 ms timer. The 55P03/57014
  diagnostic is expected at Execute, never Prepare; the failed utility leaves no
  effects, and the same physical owner later runs it successfully.
- Query results do not prove that an index is used. Index structure, lookup and
  native lock-queue order are asserted by engine units:
  `make check RUN='^Test(AccessMethod|OperatorClass|OperatorFamily|HashIndex|ConcurrentIndex|PartialIndex|UsingIndex|PrimaryKey|Exclusion)'`.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- <a id="access-method-definitions"></a>Access-method definitions: `RUN='^TestReferenceAccessMethod'`
- <a id="operator-class-definitions"></a>Operator classes: `RUN='^TestReferenceOperatorClass'`
- Operator families and support routines: `RUN='^TestReferenceOperatorFamily'`
- B-tree indexes and build timeouts: `RUN='^TestReferenceBtreeIndex'`
- Hash indexes: `RUN='^TestReferenceHashIndex'`
- Partial indexes: `RUN='^TestReferencePartialIndex'`
- Concurrent indexes: `RUN='^TestReferenceConcurrentIndex'`
- UNIQUE constraints and indexes: `RUN='^TestReferenceUnique'`
- PRIMARY KEY: `RUN='^TestReferencePrimaryKey'`
- Index clauses: `RUN='^TestReferenceIndexClauses'`
- Index properties: `RUN='^TestReferenceIndexProperties'`
- Index maintenance and locks: `RUN='^TestReferenceIndexMaintenance'`
- `USING INDEX`: `RUN='^TestReferenceUsingIndex'`
- Exclusion constraints: `RUN='^TestReferenceExclusion'`
