# reference/indexes

Indexes and index-backed constraints: access-method definitions (`CREATE ACCESS
METHOD`), operator classes and families, B-tree/hash/GiST/SP-GiST/GIN/BRIN/bloom indexes, partial,
concurrent and UNIQUE indexes, PRIMARY KEY, index clauses (expressions, INCLUDE,
collation/class, ordering), index property inquiry, maintenance (REINDEX, ALTER
INDEX, DROP INDEX and their locks), `ADD CONSTRAINT ... USING INDEX`,
exclusion constraints (including GiST), and installed amcheck heap, B-tree and GIN
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

- Installed heap/B-tree/GIN verification: `RUN='^TestReferenceAmcheck'`; corrupt actual
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
  The GIN family (`RUN='^TestReferenceAmcheckGIN'`) covers the slot-eight
  declaration, typed NULL/VOID and occurrence bags, maintained array/tsvector
  dictionaries, full diagnostics, EXECUTE/default ACL provenance, real queue
  admission/cancellation and stored-expression control replay. Native
  `RUN='^TestAmcheckGIN'` owner proofs inspect exact typed posting identities,
  damage retained structures, and assert immutable private/capture authority.
  Contents also proves verification leaves pending work for actual cleanup, then
  checks cleaned/main and renewed mixed roots with fastupdate-off siblings.
  `TestAmcheckGINSplitPostings` retains overlapping-token main/pending roots;
  logical-corruption and cancellation proofs cover both genuine portions,
  including cross-portion duplicates and inconsistent column placement. Private
  authority proofs consume mandatory produced GIN outputs, never synthetic
  row-backed substitutes.
  The physical failed-concurrent-partial-build phase is PostgreSQL-only:
  native concurrent partial DDL does not retain that representation. Native
  `TestAmcheckGINTargetDiagnostics` instead publishes an actual retained-invalid
  GIN descriptor and checks the installed common admission diagnostic, unchanged
  retained data and healthy recovery. Both backends still check the subsequent
  healthy partial index. Installation-default ACL replay removes the independent
  default declaration only after proving its installed member grants, then
  rechecks grantors, initial ACLs and executable authority across real native
  Dump/Reset/Restore.

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
- Bloom installation, signatures' logical equality/recheck outcomes, options,
  diagnostics, fallback, maintenance and controls: `RUN='^TestReferenceBloom'`.
  The shared scenario owns global extension/method cleanup; alternate schemas
  and roles use the common fixture cleanup. Public collision witnesses use
  seventeen distinct values in a one-bit, sixteen-effective-bit signature and
  independently assert every equality's multiplicity; engine tests must also
  prove actual candidate consumption and recheck. Controls use recorded
  independent Driver destinations and PostgreSQL logical replay. Only native
  Dump/Reset/Restore and eager-handle phases are native-specific; physical
  pages/WAL, native AM ABI and live session resources are not replay artifacts.
- BRIN min/max indexes, diagnostics and controls (including callable/partial CREATE-plus-ALTER replay and later writes): `RUN='^TestReferenceBrinIndex'`
- GiST classes, exact predicates, options and lifecycle: `RUN='^TestReferenceGiST'`.
  Text-search predicate rows assert scalar match behavior independently of index use.
- SP-GiST classes, exact predicates, bytewise text providers and lifecycle: `RUN='^TestReferenceSPGiST'`
- GIN arrays/tsvector/jsonb, exact released catalogs, diagnostics, typed/lexical
  options, publication and replay: `RUN='^TestReferenceGIN'`. JSONB adds the
  default jsonb_ops and named jsonb_path_ops predicate bags, catalog metadata,
  DDL diagnostics, amcheck structure and the coder migration replay under
  `RUN='^TestReferenceGINJSONB'`. Public predicates retain the
  scalar array/FTS authority independently of index use; internal `TestGIN` units
  prove substantive typed main/pending postings, immutable batch merge,
  destination ownership, every transport, vacuum and cancellation.
  `RUN='^TestReferenceGINMaintenance'` covers typed cleanup, exact diagnostics and
  privileges, fastupdate routing, read-only/nonrollback/cross-session transfer,
  and actual native split replay. Positive counts are not physical-page goldens.
  Internal `TestGINMaintenance` witnesses inspect actual split, produced placement,
  source receipts and revision-fenced active/prepared/stage races; the ordinary
  `TestGINNonmaterializingMerge` family includes large main and pending payloads.
  `TestReferenceGINMaintenanceBinding` and `Returning` cover coercion, current
  identity/namespace authority and genuine pre-RETURNING production.
  `TestReferenceGINMaintenanceMaterialized` independently covers concurrent
  matched/new placement and actual native replay of populated duplicate-valued,
  empty and unpopulated parents with multiple indexes.
  `TestReferenceGINMaintenanceInstalledScalar` combines actual installed
  bit/varbit/enum/money GIN classes with array/vector keys, independently checks
  every scalar strategy and NULL result, and proves residual positive→0 cleanup,
  fastupdate-off writes, Amcheck no-drain and native split replay.
  `TestGINMaintenanceScalarCandidates` owns exact physical both-portion probes,
  pending→clean invariance, cancellation and scalar-token corruption rejection.
  `TestReferenceGINPresentationChanges` independently covers populated array/
  vector class and family rename/owner/schema changes, indexed writes, inquiry,
  private rollback and native replay. Builtin identities are restored on every
  exit. Internal `TestGINPresentationRebinding` retains strict detached rejection
  and actual shared backing; `TestGINNonmaterializingMerge` checks required
  validation/cancellation and bounded append/delete/no-change allocation.
  Installed scalar families and their SQL-callable enum comparator use
  `RUN='^TestReferenceBtreeGINCompound'`, including independent released
  signatures, direct/held values and descriptors, full diagnostics, presentation
  and real native Dump replay. Matching private `TestBtreeGINCompound` units
  assert selective physical posting triples and installation authority rather
  than inferring GIN use from successful SQL queries.
  Network/UUID/OID scalar classes use `RUN='^TestReferenceBtreeGINNetwork'` for
  independently specified graphs, slot-dependent dependency ownership, raw
  direct/held values, diagnostics, presentation and persistent replay. Owning
  `TestBtreeGINNetwork` definitions prove actual unsigned/prefix dictionary
  ordering, exact physical postings, cancellation and destination authority.
  Independent-driver and PostgreSQL Dump replay provision referenced global
  roles separately before executing unchanged emitted SQL; role definitions
  are not database Dump contents, while ownership and schema ACLs remain checked.
  Integer/float/boolean scalar classes use `RUN='^TestReferenceBtreeGINScalar'`
  for independent released catalogs/dependencies, exact boundary and special
  values, direct/fresh/held descriptions, diagnostics and actual Dump replay.
  Owning `TestBtreeGINScalar` proofs independently check sorted typed keys,
  exact row/version/slot candidate postings (not scan parity), immutable
  publication/presentation backing and destination-owned replacement authority.
  Temporal classes use `RUN='^TestReferenceBtreeGINTemporal'` for independent
  released graphs, finite/infinite boundaries, exact diagnostics, raw typed
  metadata, presentation and actual emitted native Dump replay on PostgreSQL.
  Owning `TestBtreeGINTemporal` proofs assert typed equivalence groups, exact
  selective posting triples, immutable publication/presentation and independent
  destination generations and sealed authority; public SQL alone is not index-use
  evidence.
  Text/varchar/bpchar/internal-char/name/bytea fixtures use
  `RUN='^TestReferenceBtreeGINText'`: independently expected five-strategy
  boundaries, empty/NULL/raw values and metadata, directional binary-compatible
  admission, full diagnostics/atomicity, shared text/varchar helper graphs,
  presentation and actual native emitted SQL replay. `TestBtreeGINText` owner
  proofs assert real typed dictionaries and selective posting triples rather
  than inferring index execution from SQL scan parity. Native controls replay
  logical objects/data, never C shared-library ABI, disk pages/WAL/pending lists,
  network/server planner state or live session resources.
  Native controls distinguish successful builtin-signature/datum held rebinding
  from the ordinary lifetime fence of a captured enum literal and the exact
  `0A000` concrete-enum parameter/result fences. Fresh enum-literal preparation
  succeeds after Restore while old eager values and metadata remain readable.
  `TestReferenceBtreeGINCompoundHelperOwnerACL` adds the explicit-ACL helper
  owner projection with an existing dependent index, rollback, grant options,
  direct/fresh/held allowed and denied calls, and indexed writes by a caller
  denied helper EXECUTE. Installer-role expectations retain the fixture's native
  postgres versus pinned-PostgreSQL stuntgres distinction; transferred ownership
  has the same independently specified role/grantor projection on both backends.
- Partial indexes: `RUN='^TestReferencePartialIndex'`
- Concurrent indexes: `RUN='^TestReferenceConcurrentIndex'`
- UNIQUE constraints and indexes: `RUN='^TestReferenceUnique'`. Standalone
  UNIQUE indexes admit expression keys mixed with columns and partial
  predicates and create no `pg_constraint` row; expression indexes remain
  ineligible for concurrent refresh and replica identity.
- PRIMARY KEY: `RUN='^TestReferencePrimaryKey'`
- Deferred UNIQUE/PK timing and concurrency: `RUN='^TestReferenceDeferredUnique'`
- Index clauses: `RUN='^TestReferenceIndexClauses'`
- Index properties: `RUN='^TestReferenceIndexProperties'`
- Index maintenance and locks: `RUN='^TestReferenceIndexMaintenance'`
- Logical index catalog metadata, lifecycle, diagnostics and controls: `RUN='^TestReferencePGIndex'`
- `USING INDEX`: `RUN='^TestReferenceUsingIndex'`
- Exclusion constraints: `RUN='^TestReferenceExclusion'`
- Heap verification: `RUN='^TestReferenceAmcheckHeap'`
- TEMP heap admission/accounting: `RUN='^TestReferenceAmcheckHeapTemporary$'`
- Heap ACL provenance/replay: `RUN='^TestReferenceAmcheckHeapACLProvenance$'`
- B-tree ACL provenance/replay: `RUN='^TestReferenceAmcheckBtreeACLProvenance$'`
