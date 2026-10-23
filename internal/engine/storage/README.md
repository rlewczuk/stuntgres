# internal/engine/storage

The engine's physical data layer: committed relation storage and row
versions, deletion roots, logical B-tree and hash index roots, sequence
generator state, and the `Store` that owns them. The package sits at layer
L4 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports only `catalog`, `expr`, `ids`, `sqltype` and the standard library,
and never the engine.

## What it owns

- **`Store`** (`store.go`): the published maps `Relations`, `Sequences` and
  `IndexRoots`, and the row and version identity allocators. The engine
  holds one store by value (`Engine.storage`) and never replaces it whole.
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
- **Lineage tokens** (`lineage.go`): `TruncateLineage` and
  `MaterializedLineage`. A token is a nonzero-size allocation compared by
  pointer; it retains no storage or rows. `SameTruncateLineage` is nil-safe.
- **Index roots** (`index.go`, `hash.go`): `IndexRoot` and `IndexEntry`
  (B-tree keys or GiST membership), `HashRoot`, `HashEntry` and the bound
  key `HashBinding`, and `IndexOutput`, an index membership/key a private
  row already produced. `CaptureRowEntries` builds the per-row hash entry
  index for exclusion indexes with runtime domain key conversion.
- **Sequence generators** (`sequence.go`): `SequenceGenerator`.

## Invariants

- **One lock authority.** The package has no mutex. `Engine.mu` guards every
  `Store` field and every lineage token. Mutating store methods carry the
  `Locked` suffix; the caller holds `Engine.mu`.
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
  store's maps together with the catalog state under `Engine.mu`. Snapshots
  retain roots by pointer and never copy row values.
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
  remain unknown until engine initialization against the canonical safe cutoff.
  Remint initializes current Live from committed visible capture cardinality,
  Dead 0, and independently resets scan/vacuum/cumulative history; transport is
  not counted as native committed INSERT activity.
- **VACUUM roots.** `InspectVacuum` counts and chooses exact committed matching
  end/version retirement; `MaterializeVacuum` copies headers only when changed,
  preserving survivor payloads and old roots. The engine owns the conservative
  snapshot gate and final fence. `RemapVacuumIndex` preserves keys and existing
  index state, remaps positions/membership/RowEntries and rebuilds hash pointers
  against new buckets. Hash prefix coverage is preserved, never upgraded to a
  complete index; freeze-only work rebinds hash generation without needless
  B-tree remapping. No-change passes retain roots; no row/version IDs are minted.

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

Package units pin the allocators, `IndexRootsCurrent`, index identity and
truncate lineage. Engine witnesses cover storage-map publication
(materialized views, generated replacement, unique and concurrent index
publication, TRUNCATE generations, TEMP cleanup, commit-time publication) and
VACUUM roots; public sequence, control and hash-partition replacement
witnesses are in `reference/`.

```sh
make check RUN='^Test(Store|Relation(IndexIdentity|SameTruncateLineage))'
make check RACE=1 RUN='^Test(ConcurrentIndexU1RootPhasesAndVersions|ConcurrentIndexU6ReadyCancellationReleasesSUE|UniqueIndexPublication(Streaming|Cancellation)|GeneratedReplacement(StaleAuthority|LateCancellation)|TemporaryTableCleanupPublication)$$'
make check RUN='^Test(MaterializedViewSnapshotImport|MaterializedViewPublication|MaterializedRefreshPublicationFences|ControlTransactionsReplacementCancellationBoundary|CommitTimePublication|TruncateGeneration|TruncateIndexes)$$'
make check RUN='^TestVacuum'
make check RUN='^TestReference(ControlSnapshotSurvivesCatalogReplacement|ControlFailedRestorePreservesCatalog|HashPartition(Controls|FinalRemap))$$'
```
