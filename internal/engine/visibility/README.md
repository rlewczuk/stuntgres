# internal/engine/visibility

Transaction identities, commit ordering/timestamps, snapshot data views,
repeatable-read conflict decisions and serializable tracking below core.
It never owns catalog admission or a session/transaction state machine.

## What it owns

`Registry` is held by value on Engine and never replaced whole. It owns canonical
commit/XID frontiers, live/completed identity records, retained snapshot evidence
and SSI graph state. `View` captures immutable relation/version roots and
visibility frontier; write-set merges operate over explicit private/committed
versions and end roots.

## Contract

- Registry reads/writes require caller-owned Engine.Mu, including pure-looking
  accessors. Methods never take another mutex, block or call application code.
- Transaction isolation has one owner in session/core. Catalog epoch and
  transaction provenance fences remain core's responsibility; Registry never
  looks up a catalog or authorizes a raw numeric identity.
- Full XIDs are monotonic, skip reserved low values and are distinct from
  CommitSeq. Read-only inquiry/snapshot capture does not invent assignment;
  assigned ownership and completion transfer through the actual transaction.
- Commit timestamps consume explicit completion time/origin from their real
  owner, not a later sampled clock. Abort/subabort is not committed history.
- Stable isolation retains admitted roots/frontier; ordinary RC selects its
  execution pair after relation admission. Later physical capture applies that
  same frontier, not a fresh snapshot. Retained roots/payloads remain immutable.
- Tuple conflict/merge checks use actual version chains and canonical birth/end
  facts. Same RowID or equal payloads do not prove successor ownership.
- Snapshot/export advertisements and tuple-retention horizons have explicit
  lifetime distinct from XID allocation and display. Maintenance never retires
  versions needed by genuine retained readers or active/prepared owners.
- SSI records real read/write evidence; safe read-only snapshots wait through
  core's unlocked completion path and install only the accepted coherent pair.
  Imported snapshot/SSI evidence retains genuine source lineage and does not
  weaken local catalog, owner or publication checks.

## What stays in the engine

Transaction admission and state transitions, lock registration/waits, captured
catalog/role pairing, snapshot/data-view construction, completion, safe-snapshot
waiting, epoch fencing and row/index publication. SQL inquiry recipes live in
functions and borrow current runtime authority through core.

## Testing

Pure Registry/view/conflict/SSI/merge units live here with controlled canonical
frontiers. Composed core tests exercise real lock, transaction and snapshot
ownership. Public isolation/visibility SQL belongs to reference packages under
[docs/TESTING.md](../../../docs/TESTING.md).
