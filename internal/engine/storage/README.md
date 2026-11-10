# internal/engine/storage

Physical data records below core: relation versions, end/header roots, indexes,
generators, logical lineage, publication values and ANALYZE metadata. Core owns
admission, staging and installation; this package is not a transaction manager.

## What it owns

`Store` contains committed Relations, RelationMetadata, Sequences, IndexRoots and
allocation frontiers. `Relation` owns immutable version/payload and physical
header/end roots; index method records own typed immutable key backing.
Publication/RowShape values describe checked candidate installation. Maintenance
and statistics helpers compute from explicit captured roots without live engine
lookup or callbacks.

### Logical allocation estimates

Size/model helpers perform checked arithmetic over actual captured immutable
values. Estimates are deterministic logical measurements, not PostgreSQL files,
physical heap/TOAST pages or Go/OS allocation reports. Core owns descriptor/
visibility/private-overlay capture and authorization before unlocked computation.

## Invariants

- No package mutex. Engine.Mu guards every Store field and lineage token;
  mutating methods are Locked. Exclusive standalone fixtures own their store.
- Store setters install supplied roots/maps without allocation, copying,
  validation or hidden initialization. Candidate preparation and validation
  happen before the engine's no-failure installation window.
- Published vectors, payloads, headers, end maps and index roots are immutable.
  Captures borrow pointers; COW transforms retain old backing and copy only
  changed metadata/payloads. Catalog/storage publish coherently.
- RowID, VersionID, canonical publisher XID and visibility CommitSeq are distinct.
  Identity allocation is nonreused and failures may leave gaps; equal values or
  same-row siblings never establish physical successor ancestry.
- Actual rewrite, preserving codec transport, concurrent compatible lineage and
  destructive replacement carry separate provenance. A copy cannot infer or
  refresh an earlier maintenance predecessor. Final publication fences genuine
  observations against their original storage owner.
- Sequence generators are nontransactional and distinct from catalog options and
  per-session sequence memories. Definition rollback is not counter undo.
- Maintenance copies retain genuine tuple publisher/provenance and survivor
  payloads. Imported estimates and collected typed metadata are independent of
  physical heap identity, allocator frontiers and visibility.

## What stays in the engine

WriteStage/journal overlays, snapshot selection, owner capture, lock windows,
private disposal, validator ordering, frontiers and final row/index/catalog
installation. Storage values never publish committed roots themselves.

## Testing

Pure root/COW/allocator/lineage/checked-model units live here. Composed core tests
cover real storage ownership, publication and replacement; type-dependent recipe
fixtures use `enginetests` to avoid dependency cycles. Public SQL belongs to
reference packages under [docs/TESTING.md](../../../docs/TESTING.md).
