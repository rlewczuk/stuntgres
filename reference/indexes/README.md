# reference/indexes

Indexes and index-backed constraints: access-method definitions (`CREATE ACCESS
METHOD`), operator classes and families, B-tree/hash/GiST/SP-GiST/BRIN indexes, partial,
concurrent and UNIQUE indexes, PRIMARY KEY, index clauses (expressions, INCLUDE,
collation/class, ordering), index property inquiry, maintenance (REINDEX, ALTER
INDEX, DROP INDEX and their locks), `ADD CONSTRAINT ... USING INDEX`,
exclusion constraints (including GiST), and installed amcheck heap and B-tree
verification. Related families: partitioned-index and partitioned-exclusion
hierarchies in `reference/partitions`; ALTER TABLE ADD
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
- UNIQUE/PK and USING INDEX property witnesses assert persistent timing flags
  and canonical definitions on both backends, separately from transaction modes.
- Access methods are database-global and survive the `public` reset: list every
  method a scenario creates in `amBackends`, which drops them through the admin
  pool before and after the case.
- `heapScenario` (`amcheck_heap_helpers_test.go`) owns amcheck registration cleanup
  independently of role-changing borrowers. Heap scenarios assert empty-result
  metadata, direct/prepared execution and explicit native control phases.
  Forced remint assertions first prove an actual retained TEMP numeric OID
  collision on a pinned owner, then check unchanged TEMP identities and data.
  Independent engines may legitimately reuse equal numeric OIDs otherwise.
  `TestReferenceAmcheckHeapTemporary` pins numeric regclass calls across sessions:
  own TEMP tables/sequences are healthy but create a PREPARE footprint; foreign
  TEMP storage is rejected after EXECUTE admission without retaining added locks,
  but preserves PostgreSQL 18.6's sticky TEMP-attempt preparation footprint.
  EXECUTE denial precedes that footprint. Direct and prepared calls use the same
  public fixture. `TestReferenceAmcheckHeapACLProvenance` records real
  owner→grant-option-holder→third-role origins and options, then checks independent
  native role-domain replay, missing-role atomicity, cascading grant-option
  revocation, and an actual PUBLIC ACL Dump/Reset/Restore.
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
- Transactional ordinary creation uses `transactionalIndexRun` with actual
  pinned owners/held statements. `TransactionalIndexFlyway` retains the exact
  reported SQL across SQL BEGIN/BeginTx, existing/private full-table parents and
  commit/rollback. Lifecycle, UNIQUE, savepoints, diagnostics, current prepared
  authority, locks/cancellation, names and native controls have separate bounded
  definitions. `fixture.RelationLockPending` observes actual scheduler admission;
  native units additionally assert exact ticket/ref/mode and substantive typed
  root identities, physical positions, candidates and immutable prefixes.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Installed B-tree verification: `RUN='^TestReferenceAmcheck'`; corrupt actual
  roots/private outputs and invocation internals use `RUN='^TestAmcheck'` in the
  owning engine packages. Public witnesses use real maintained indexes, exact
  scalar diagnostics, scoped locks and native controls, not a SQL corruption API.
  Fresh installation defaults to owner-only EXECUTE with PUBLIC revoked; controls
  replay current arbitrary installed-member ACLs, including grant options,
  nonowner grantor provenance, PUBLIC and explicit-empty ACLs. The preserved heap
  provenance fixture covers slot zero. `TestReferenceAmcheckBtreeACLProvenance`
  exercises each of the seven B-tree overloads through actual Dump/Reset/Restore,
  independent destination role domains, missing-role atomicity and restored
  callable authority, while denying ungranted sibling overloads. ACL targets are
  schema-qualified across role/search-path changes. The controls lifetime fixture
  retains the stale held-literal fence, fresh parameterized inputs and stored views.
  The current pinned oracle is PostgreSQL 18.6. Initial restricted search-path
  behavior is covered natively through the invocation seam: the public SQL
  surface does not provide the required custom comparison callback recipe.

- <a id="access-method-definitions"></a>Access-method definitions: `RUN='^TestReferenceAccessMethod'`
- <a id="operator-class-definitions"></a>Operator classes: `RUN='^TestReferenceOperatorClass'`
- Operator families and support routines: `RUN='^TestReferenceOperatorFamily'`
- B-tree indexes and build timeouts: `RUN='^TestReferenceBtreeIndex'`
- Transactional ordinary creation (select small groups, add `REFERENCE=1`):
  `RUN='^(TestReferenceTransactionalIndexFlyway|TestReferenceTransactionalIndexLifecycle)$'`,
  `RUN='^(TestReferenceTransactionalIndexUnique|TestReferenceTransactionalIndexSavepoints)$'`,
  `RUN='^(TestReferenceTransactionalIndexDiagnostics|TestReferenceTransactionalIndexPrepared)$'`,
  `RUN='^(TestReferenceTransactionalIndexLocks|TestReferenceTransactionalIndexCancellation)$'`,
  `RUN='^(TestReferenceTransactionalIndexNames|TestReferenceTransactionalIndexControls)$'`.
  Focused native structure/authority:
  `RUN='^(TestTransactionalIndexPrivateRoots|TestTransactionalIndexSavepointRoots|TestTransactionalIndexAuthorityFences)$'`.
  Use `RACE=1` for the transactional lock/savepoint/cancellation/name selections
  and `TestTransactionalIndexLocks`/`TestTransactionalIndexReplacementFences`.
- Hash indexes: `RUN='^TestReferenceHashIndex'`
- BRIN min/max indexes, diagnostics and controls (including callable/partial CREATE-plus-ALTER replay and later writes): `RUN='^TestReferenceBrinIndex'`
- GiST classes, exact predicates, options and lifecycle: `RUN='^TestReferenceGiST'`
- SP-GiST classes, exact predicates, bytewise text providers and lifecycle: `RUN='^TestReferenceSPGiST'`
- Partial indexes: `RUN='^TestReferencePartialIndex'`
- Concurrent indexes: `RUN='^TestReferenceConcurrentIndex'`
- UNIQUE constraints and indexes: `RUN='^TestReferenceUnique'`
- PRIMARY KEY: `RUN='^TestReferencePrimaryKey'`
- Deferred UNIQUE/PK timing and concurrency: `RUN='^TestReferenceDeferredUnique'`
- Index clauses: `RUN='^TestReferenceIndexClauses'`
- Index properties: `RUN='^TestReferenceIndexProperties'`
- Index maintenance and locks: `RUN='^TestReferenceIndexMaintenance'`
- `USING INDEX`: `RUN='^TestReferenceUsingIndex'`
- Exclusion constraints: `RUN='^TestReferenceExclusion'`
- Heap verification: `RUN='^TestReferenceAmcheckHeap'`
- TEMP heap admission/accounting: `RUN='^TestReferenceAmcheckHeapTemporary$'`
- Heap ACL provenance/replay: `RUN='^TestReferenceAmcheckHeapACLProvenance$'`
- B-tree ACL provenance/replay: `RUN='^TestReferenceAmcheckBtreeACLProvenance$'`
