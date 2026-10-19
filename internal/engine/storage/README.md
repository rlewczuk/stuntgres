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
- **Relation storage** (`relation.go`): `Relation` (the version vector, the
  end root, the generation, the index-build origin and the lineage tokens),
  `RowVersion`, `RowEnd` and `DeletionRoot`. `IndexIdentity` names the
  storage an index root was built from.
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
- **Immutable published roots.** Published `Relation` vectors, end roots and
  index roots are immutable and shared copy-on-write. Publication swaps the
  store's maps together with the catalog state under `Engine.mu`. Snapshots
  retain roots by pointer and never copy row values.
- **Allocation order.** Row and version identities are globally nonreused
  and allocated in order; failed work may leave gaps.
- **Sequence generators.** Generator state is nontransactional and distinct
  from the transactional sequence options in the catalog and from the
  engine's per-session currval/lastval memories. Never add live catalog
  undo or per-CACHE arrays for it.

## What stays in the engine

Write staging (`WriteStage`), the transaction-private deletion overlay
(`privateDeletionRoot`, `deleteCandidate`) and `liveVersion`, the overlays
that consult the transaction catalog journal (`privateStorageLocked`,
`sequenceGeneratorLocked`, `domainStageIndexRootLocked`), DML candidate
building, index builds and maintenance orchestration, Restore, Dump and
replacement, sequence DDL and transactional sequence metadata, and session
sequence memories. `indexOutputMatches` stays in the engine because it
compares index key semantics.

## Testing

Package seam tests (`store_test.go`):

```sh
make check RUN='^Test(Store|Relation)'
```

Engine tests exercise the storage paths through publication, DML, index
maintenance and Restore. `internal/engine/README.md` had no storage
selectors to move; these are the selections used for the split:

```sh
make check RUN='^Test(Truncate|MaterializedRefresh|ConcurrentIndex|HashIndex|Sequence|UpdateCore|Exclusion|InsertConflict|CommitTime|Restore|UniqueIndexPublication)'
make check RACE=1 RUN='^Test(ConcurrentIndex|UpdateCore|Exclusion|InsertConflict|Truncate|MaterializedRefresh|Sequence)'
```
