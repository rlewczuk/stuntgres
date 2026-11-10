# reference/indexes

Dual-mode scenarios for index definitions, index-backed constraints, maintenance
and installed verification. Partition hierarchies belong to
[partitions](../partitions/README.md#testing); table constraint actions to
[alter](../alter/README.md#testing).

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
- `heapScenario` (`amcheck_heap_helpers_test.go`) owns amcheck registration cleanup
  independently of role-changing borrowers. Heap scenarios assert empty-result
  metadata, direct/prepared execution and explicit native control phases.
  Forced remint assertions first prove an actual retained TEMP numeric OID
  collision on a pinned owner, then check unchanged TEMP identities and data.
  Independent engines may legitimately reuse equal numeric OIDs otherwise.
- Rediscover user OIDs (classes, families, methods) by name after Restore or
  replay; never assume they are stable.
- Timeout witnesses (`HashIndexBuildAdmission`, `BtreeIndexBuildTimeouts`,
  `IndexMaintenanceLocks`) prepare the held CREATE INDEX/REINDEX/DROP, and any
  timer-reset `SET`, at timeout 0, then arm a 10–20 ms lock or 20–50 ms statement
  timer. The 55P03/57014 diagnostic is expected at Execute, never Prepare; the
  failed utility leaves no effects, and the same physical owner later runs it
  successfully.
- Query results do not prove that an index is used. Index structure, lookup and
  native lock-queue order are asserted by engine units:
  `make check RUN='^Test(AccessMethod|OperatorClass|OperatorFamily|HashIndex|ConcurrentIndex|PartialIndex|UsingIndex|PrimaryKey|Exclusion)'`.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Access methods: `RUN='^TestReferenceAccessMethod'`
- Operator classes: `RUN='^TestReferenceOperatorClass'`
- Operator families: `RUN='^TestReferenceOperatorFamily'`
- B-tree/hash: `RUN='^TestReference(Btree|HashIndex)'`
- Other methods: `RUN='^TestReference(Bloom|BrinIndex|GiST|SPGiST|GIN)'`
- Constraints: `RUN='^TestReference(Unique|PrimaryKey|Exclusion|UsingIndex)'`
- Maintenance: `RUN='^TestReferenceIndexMaintenance'`
- Transactional creation: `RUN='^TestReferenceTransactionalIndex'`
- Installed verification: `RUN='^TestReferenceAmcheck'`

Native logical replay preserves objects and data, not C ABI, pages/WAL, planner
state or live resources. Provision global roles before unchanged emitted-SQL
replay. A real retained TEMP OID collision establishes remapping; independent
engines can otherwise reuse the same numeric OIDs legitimately.
