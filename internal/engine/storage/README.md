# internal/engine/storage

The engine's physical data layer: committed relation storage and row
versions, deletion roots, logical B-tree/hash/GiST/BRIN index roots, sequence
generator state, and the `Store` that owns them. The package sits at layer
L4 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports only `catalog`, `expr`, `ids`, `sqltype` and the standard library,
and never the engine.

## What it owns

- **`Store`** (`store.go`): the published maps `Relations`, `RelationMetadata`,
  `Sequences` and `IndexRoots`, and the row and version identity allocators.
  The engine holds one store by value (`Engine.Storage`) and never replaces it whole.
  The counters are private: read them with `RowFrontier`/`VersionFrontier`,
  reserve with `AllocateRowVersionsLocked` (one row and version per row, in
  order, with the exhaustion diagnostics), `AllocateRowLocked` and
  `AllocateVersionLocked` (one step; the caller has checked exhaustion with
  its own diagnostic), and install both with `SetFrontierLocked` after
  Restore or foreign replacement reminted storage. `IndexRootsCurrent`
  compares captured roots with the published ones.
  Map writes use `state_write.go`: `InstallRelationLocked`,
  `RemoveRelationLocked`, `ReplaceRelationsLocked`, and the corresponding
  sequence and index-root methods. Reads may use the exported maps directly.
- **Relation storage** (`relation.go`): `Relation` (the version vector, the
  end root, the generation, the index-build origin and the lineage tokens),
  `RowVersion`, `RowEnd` and `DeletionRoot`. `SameVersionRoot` compares
  two version roots by identity. `IndexIdentity` names the
  storage an index root was built from. `RowByID` finds a row by its stable
  `RowID`, trying a captured position first.
  `Generation` advances on ordinary DML publication as well as physical changes;
  it is a tuple-root revision, not an immutable heap-incarnation token. The heap
  pointer identifies the storage incarnation; rewrites install a new pointer.
- **Logical heap verification** (`verify_heapam.go`): `HeapVerification` borrows
  live column usages/stable attributes and immutable committed/own version/end
  roots. `VerifyHeapam` checks exact typed admission and bounded identity,
  birth, private-predecessor, width and end-sequence facts without visibility
  filtering, codec I/O, constraints or mutation. Findings retain exact RowID and
  VersionID; Attribute zero is tuple-wide. Stopping completes every finding for
  the first invalid version. Captured committed order precedes own order.
- **Lineage tokens** (`lineage.go`): `TruncateLineage` and
  `MaterializedLineage`. A token is a nonzero-size allocation compared by
  pointer; it retains no storage or rows. `SameTruncateLineage` is nil-safe.
- **Index roots** (`index.go`, `hash.go`): `IndexRoot` and `IndexEntry`
  (B-tree keys or GiST membership), `HashRoot`, `HashEntry` and the bound
  key `HashBinding`, and `IndexOutput`, an index membership/key a private
  row already produced. `CaptureRowEntries` builds the per-row hash entry
  index for exclusion indexes with runtime domain key conversion.
  `brin.go` owns the immutable `BrinRoot`, per-key bindings and range summaries.
  It retains effective width and storage/generation/prefix/end captures, not
  a row-key list. Logical pages are authoritative version-vector slots; datum
  comparison, summary publication and candidate consistency belong to `index`.
- **Sequence generators** (`sequence.go`): `SequenceGenerator`.
- **Catalog publication values** (`row_shape.go`, `publication.go`): a
  stage's planned heap replacement `RowShape`, whose `Matches` fences store,
  generation and row count and `MatchesWithEnds` also the deletion ends;
  `Publication` (initial tables, row shapes, refreshed store) and its
  `CandidateRelations`, the relation map a catalog candidate installs;
  `Relation.StampPublishedBirth` and `CountPublishedInserts` for privately
  built stores; and `Store.ReplacementCapacityLocked`, the identity and
  commit-sequence capacity check of a catalog replacement.
- **ANALYZE facts** (`relation_metadata.go`, `statistics.go`,
  `statistics_collect.go`): `Store.RelationMetadata` is the one canonical owner
  for immutable ANALYZE records across all relation kinds. Partitioned parents
  retain metadata without any synthetic physical `Relation`. Each record owns
  the sole committed `Statistics` root, keyed by stable attribute and
  local/inherited flavor, plus successful scan cardinality/count/time facts.
  `CollectStatistics` consumes caller-owned visible/layout-mapped borrowed rows
  outside engine locks. Actual grouping/hash/ordering bind once per column.
  Population, NULLs, supported non-NULL distinct and MCV counts are exact
  full-data facts. Unknown distinct is not measured zero. Each StatisticsColumn
  carries the admitted target (1..10000): at most target repeated MCV groups and
  target+1 weighted residual quantile endpoints are retained. Target1 still
  collects statistics. Deterministic membership/ties and residual summaries are
  approximations,
  not PostgreSQL physical samples. Frequencies use all rows as denominator.
  Missing equality keeps NULL facts; missing ordering keeps grouping facts.
  Empty collection returns no replacements, preserving prior distributions.
  `MergeStatistics` replaces selected columns without mutating older roots or
  unselected payloads; `CompatibleStatistics` filters dropped/type/collation
  incompatible attributes. Callers own transactional column deltas, authority
  revalidation, lifecycle retention and record replacement under `Engine.mu`.
  Successful heap ANALYZE also updates existing physical maintenance scan facts
  and resets only the since-analyze modification counter, retaining cumulative
  DML/VACUUM and since-vacuum counters. Physical `Relation.Maintenance` does not
  duplicate typed statistics or ANALYZE count/time/inherited publications.
  Extended facts share this same root, keyed by generation-qualified statistics
  definition and inherited flavor. `ExtendedCollector` reuses the ordinary scalar
  collector for typed projected expression values and shares those values with
  ndistinct and dependency collectors. Typed NOT DISTINCT keys are bound once
  per dimension and prepared/hashed once per row for both kinds; every hash
  collision is equality-checked. Ndistinct covers subsets of widths2..8.
  Dependencies cover every nonempty proper determinant subset and each other
  dependent dimension (at most1016 directions). A group contributes all its rows
  only if every dependent value agrees; conflicting groups contribute zero.
  Positive row-weighted degrees are retained; all-zero analysis yields typed
  SQL NULL in an existing analyzed row. This deterministic logical-population
  estimate does not emulate PostgreSQL sampling or physical TOAST-width filtering.
  NULLs form real groups. Simple keys retain positive attribute identities;
  ordered expression dimensions use -1,-2,... . Serialized members preserve
  positive simple keys followed by expression order, matching PostgreSQL.
  Single-expression, automatic-summary-only (`e`-only) facts have genuine
  scalar summaries but no joint Value. All-expression multivariate definitions
  can also collect ndistinct and dependencies. `MergeExtendedStatistics`
  preserves unselected backing; compatibility
  checks retained recipe identity, stable input/result usages, actual external
  provider revisions and current hierarchy authority, not display names or DML.
  AverageWidth retains the actual result type's available fixed datum width,
  including all-NULL populations. Non-NULL text/bytea use datum byte lengths,
  never formatted wire size; all-NULL/unsupported variable-width models remain0.
  The strict `stuntgres-ndistinct-v1` channel now transports checked extended
  facts, including optional typed expression summaries and semantic recipes.
  Non-f records retain their existing byte format. A dependencies array is
  mandatory exactly for f records, including an empty array for analyzed NULL.
  Snapshot import validates canonical signed direction ordering, positive finite
  fractional degrees, signed ndistinct subsets, integral counts, typed MCV/histogram
  bounds and destination recipe/usages. Stale dependency facts are reminted,
  never recomputed from restored rows. The composing engine supplies existing
  control literal rendering/input; storage never binds expressions or executes SQL.
  Genuine destination TEMP reownership uses the existing retention authority,
  preserving old immutable facts while reminting their recipe/type generation.

### Logical allocation estimates

`relation_size.go` owns checked INT8 arithmetic over captured immutable roots;
`core` captures descriptors, vector headers, private overlays and index roots
under the cluster mutex, then releases it before counting. No datum payload is
copied, rendered or measured through SQL output; predicates and index expressions
are not reevaluated, and SELECT/RLS visibility does not filter allocations.
Current-command staged rows are included alongside prior transaction writes.
Complete projected private roots override the prior private map; replacement
staging supersedes prior private rows by stable RowID without copying payloads.
Nested routine activation may expose a staged version through both borrowed
vectors; matching RowID/VersionID pairs are charged once in heaps and indexes.
Deletion markers alone do not reclaim allocated physical versions.

These counters are deterministic technical estimates, not PostgreSQL physical
heap, index, TOAST or map-file bytes. Let `A8(x)=8*ceil(x/8)`, with `A8(0)=0`.
A NULL cell costs zero; a non-NULL fixed-width datum costs `A8(typlen)`, using
the type descriptor's PostgreSQL length (including domain base types); any other
non-NULL datum costs one 32-byte slot, independent of payload length.
A row costs `24+A8(ceil(cells/8))+sum(cell costs)`. An allocated heap's main
component is `64+sum(row costs)` over every retained physical version, including
ended versions. Its `fsm=8*ceil(versions/256)` and `vm=ceil(versions/8)`.
Empty allocated heaps cost 64; absent roots and storage-less relations cost zero.
TRUNCATE charges the current root, not retained predecessors; reclamation removes
reclaimed versions from later estimates. Supported native heaps/indexes have no
initialization allocation, so their `init` component is zero.

An allocated index's main component is `64+sum(entry costs)`. B-tree/GiST entries
cost `16+sum(stored key cell costs)+sum(included cell costs)`; included values
come from stable attributes and matching parent row/version identity. Private
entries consume their already-held row directly, without searching the published
prefix. Captured index-entry lookups validate both RowID and VersionID, use the
position hint when valid, and poll cancellation during any fallback scan.
Hash counts each bucket entry once at `16+cell(stored key)`, never its duplicate
lookup maps.
BRIN counts only summarized ranges: `16+sum(8+cell(min)+cell(max))`, with extrema
charged only for keys having non-NULL values; unsummarized ranges cost zero.
Partial/expression indexes charge actual stored memberships/keys, including
already produced private row outputs exactly once. Pending BRIN suffixes do not
own summaries until publication. Index `fsm` and `vm` are zero. An allocated
sequence generator costs main 64, with init 64 only when its actual stored
sequence options specify unlogged persistence. Native variable datums are inline:
external TOAST heap and TOAST-index components are both zero, not synthetic roots.

`pg_relation_size` selects one fork (one argument means main); `pg_table_size`
sums the object's forks and external TOAST components, not attached ordinary
indexes. `pg_indexes_size` sums every directly attached index's forks once;
`pg_total_relation_size=pg_table_size+pg_indexes_size`. An index passed to
`pg_indexes_size` has no attached ordinary indexes. Parents do not recursively
charge partition or inheritance descendants. Database size sums each owned stored
relation once, counting indexes separately and excluding shared pg_global
allocations. Tablespace size sums assigned allocations across registered database
runtimes once: default storage belongs to pg_default, shared storage to pg_global.
Virtual catalog rows do not create physical allocations.

For `(INT4,TEXT,BOOL)` rows `(1,'x',TRUE),(2,NULL,FALSE)`, row costs are 80 and
48, main 192, fsm 8, vm 1, table 201. An ordinary index on the integer costs
112, giving total 313. Native Dump/Restore reconstructs supported typed contents
and index metadata through ordinary SQL replay. Clean-state allocations survive
that round trip; dead-version bloat, stale BRIN extrema, sequence advancement and
nonserialized auxiliary allocation history are not a replay identity contract.



## Invariants

- **One lock authority.** The package has no mutex. `Engine.Mu` guards every
  `Store` field and every lineage token. Mutating store methods carry the
  `Locked` suffix; the caller holds `Engine.Mu`.
  This includes bootstrap and isolated replacement engines; standalone storage
  unit fixtures instead own their store exclusively.
- **Minimal map writes.** Install retains the supplied pointer, remove is an
  ordinary delete, and replace installs the supplied map container (including
  nil) in one assignment. No method allocates, copies, validates, initializes
  a nil map or acquires a lock. Installing into a nil map still panics.
  The engine builds detached candidates directly and publishes through these
  methods without changing preflight, allocator or catalog publication order.
- **Immutable published roots.** Published `Relation` vectors, end roots and
  index roots are immutable and shared copy-on-write. Publication swaps the
  store's maps together with the catalog state under `Engine.Mu`. Snapshots
  retain roots by pointer and never copy row values.
- **Publication values take no lock.** `RowShape`, `Publication` and the
  stamping methods are value code the engine runs under `Engine.Mu` inside
  `publishCatalog`; the visibility calls, retry loop and installation stay
  in the engine.
- **Allocation order.** Row and version identities are globally nonreused
  and allocated in order; failed work may leave gaps.
- **Sequence generators.** Generator state is nontransactional and distinct
  from the transactional sequence options in the catalog and from the
  per-session currval/lastval memories of [`sequence`](../sequence/README.md).
  Never add live catalog undo or per-CACHE arrays for it.
- **Maintenance provenance.** `RowVersion.InsertFullXID` is actual canonical
  committed publisher identity, independent of Born CommitSeq. Frozen headers
  retain it. `ImportFrozen` explicitly denotes an atomically committed detached
  foreign import without fictional source/target XID; ordinary self replacement
  preserves provenance. `Relation.Maintenance` holds coherent logical facts.
  Same-object storage rewrites copy cumulative maintenance from the validated
  current predecessor inside publication, not stale prepare-time facts.
  `Maintenance.RecordBirth` clamps an existing full-width bound against each
  actual unfrozen publisher without rescanning old versions. Unknown bounds
  remain unknown until `visibility.Registry.InitializeMaintenanceLocked` seeds
  them against the canonical safe cutoff. `Maintenance.RecordVacuum` records an
  installed VACUUM pass.
  Remint initializes current Live from committed visible capture cardinality,
  Dead 0, and independently resets scan/vacuum/cumulative history; transport is
  not counted as native committed INSERT activity.
- **VACUUM roots.** `InspectVacuum` counts and chooses exact committed matching
  end/version retirement; `MaterializeVacuum` copies headers only when changed,
  preserving survivor payloads and old roots. The engine owns the conservative
  snapshot gate and final fence. `RemapVacuumIndex` preserves keys and existing
  index state, remaps positions/membership/RowEntries and rebuilds hash pointers
  against new buckets. Hash prefix coverage is preserved, never upgraded to a
  complete index; freeze-only work rebinds hash/BRIN generation without needless
  B-tree remapping. BRIN compaction dispatches through the index owner's
  conservative destination-range reconstruction, never generic entry remapping.
  No-change passes retain roots; no row/version IDs are minted.

## What stays in the engine

Write staging (`WriteStage`), the overlays that consult the transaction
catalog journal (`privateStorageLocked`,
`sequenceGeneratorLocked`, `domainStageIndexRootLocked`), DML candidate
building, index builds and maintenance orchestration, Restore, Dump and
replacement, and sequence DDL and transactional sequence metadata. Session
sequence memories belong to [`sequence`](../sequence/README.md). Index key
comparison (`index.OutputMatches`) belongs to [`index`](../index/README.md). The
transaction-private deletion overlay and the single concrete
`HeapOperations.LiveVersion` rule belong to
[`visibility`](../visibility/README.md). Registered heap operations consume these
version vectors and end roots; they do not wrap or replace `storage.Relation`.

## Testing

Signed-dimension collection invariants that require real `plan` expression
recipes live in external `enginetests/`, avoiding a storage→plan→storage test
variant import cycle. Pure collector and snapshot-codec units remain local.

Package units pin the allocators, `IndexRootsCurrent`, index identity and
truncate lineage. Engine witnesses cover storage-map publication
(materialized views, generated replacement, unique and concurrent index
publication, TRUNCATE generations, TEMP cleanup, commit-time publication) and
VACUUM roots; public sequence, control and hash-partition replacement
witnesses are in `reference/`.

```sh
make check RUN='^Test(Store|Relation(IndexIdentity|SameTruncateLineage)|RowShape|StampPublishedBirth|ReplacementCapacityLocked)'
make check RACE=1 RUN='^Test(ConcurrentIndexU1RootPhasesAndVersions|ConcurrentIndexU6ReadyCancellationReleasesSUE|UniqueIndexPublication(Streaming|Cancellation)|GeneratedReplacement(StaleAuthority|LateCancellation)|TemporaryTableCleanupPublication)$$'
make check RUN='^Test(MaterializedViewSnapshotImport|MaterializedViewPublication|MaterializedRefreshPublicationFences|ControlTransactionsReplacementCancellationBoundary|CommitTimePublication|TruncateGeneration|TruncateIndexes)$$'
make check RUN='^TestVacuum'
make check RUN='^TestVerifyHeapamLogicalRows$'
make check RUN='^TestReference(ControlSnapshotSurvivesCatalogReplacement|ControlFailedRestorePreservesCatalog|HashPartition(Controls|FinalRemap))$$'
make check RUN='^TestStatisticsNDistinct'
make check RUN='^TestStatisticsExpression(CollectionAllNullWidth|PublicationInheritedAuthority|Snapshot(AllNullWidth|InheritedState))$'
make check RUN='^TestPlannerOtherStatisticsTarget$'
make check RUN='^TestRelationSize'
```
