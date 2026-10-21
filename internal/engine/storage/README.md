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
  `RowVersion`, `RowEnd` and `DeletionRoot`. `IndexIdentity` names the
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
  per-session currval/lastval memories of [`sequence`](../sequence/README.md). Never add live catalog
  undo or per-CACHE arrays for it.

## What stays in the engine

Write staging (`WriteStage`), the overlays
that consult the transaction catalog journal (`privateStorageLocked`,
`sequenceGeneratorLocked`, `domainStageIndexRootLocked`), DML candidate
building, index builds and maintenance orchestration, Restore, Dump and
replacement, and sequence DDL and transactional sequence metadata. Session
sequence memories belong to [`sequence`](../sequence/README.md). Index key comparison
(`index.OutputMatches`) belongs to [`index`](../index/README.md). The transaction-private deletion overlay and
`LiveVersion` belong to [`visibility`](../visibility/README.md).

## Testing

Package seams and selected storage-map publication witnesses:

```sh
make check RUN='^Test(StoreAllocateRowVersionsInOrder|StoreAllocateRowVersionsExhaustion|StoreSingleAllocatorsAdvanceIndependently|StoreIndexRootsCurrent|RelationIndexIdentity|RelationSameTruncateLineage)$'
make check RUN='^Test(MaterializedViewSnapshotImport|MaterializedViewPublication|GeneratedReplacementStaleAuthority|GeneratedReplacementLateCancellation|ControlTransactionsReplacementCancellationBoundary|CommitTimePublication)$'
make check RUN='^Test(UniqueIndexPublicationStreaming|UniqueIndexPublicationCancellation|TruncateGeneration|TruncateIndexes|MaterializedRefreshPublicationFences|TemporaryTableCleanupPublication)$'
make check RACE=1 RUN='^Test(ConcurrentIndexU1RootPhasesAndVersions|ConcurrentIndexU6ReadyCancellationReleasesSUE|UniqueIndexPublicationCancellation|GeneratedReplacementStaleAuthority|GeneratedReplacementLateCancellation|TemporaryTableCleanupPublication)$'
```

Public sequence and replacement witnesses retain independent expectations:

```sh
make check RUN='^TestReferenceSequence(DefaultsPrepared|SessionsSetvalRollback|TransactionalRestartDDL)$'
make check REFERENCE=1 RUN='^TestReferenceSequence(DefaultsPrepared|SessionsSetvalRollback|TransactionalRestartDDL)$'
make check RUN='^TestReference(ControlSnapshotSurvivesCatalogReplacement|ControlFailedRestorePreservesCatalog|SequenceTemporaryPreparedIdentitySurvivesPersistentControls)$'
make check REFERENCE=1 RUN='^TestReference(ControlSnapshotSurvivesCatalogReplacement|ControlFailedRestorePreservesCatalog)$'
make check RUN='^TestReferenceHashPartition(Controls|FinalRemap)$'
make check REFERENCE=1 RUN='^TestReferenceHashPartition(Controls|FinalRemap)$'
make check RACE=1 RUN='^TestReferenceHashPartition(Controls|FinalRemap)$'
```

Held readers keep their old INT4 rows 1,2 across replacement; fresh native
reads see TEXT 99. Failed Restore retains sentinel 77 and no replacement.
The TEMP prepared-identity control witness is native-only, not PostgreSQL
control-API coverage. See [TESTING.md](../../../docs/TESTING.md) for pinned
PostgreSQL policy and the storage split's selected DML/index/TRUNCATE/materialized
replay evidence in [ENGINE-SPLIT-storage.md](../../../docs/ENGINE-SPLIT-storage.md).
