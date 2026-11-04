# internal/engine/logical

The native logical change stream below the engine runtime. Layer L1 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture): it
imports only `sqltype`, `sqlstate` and the standard library, and holds no lock.
The design is [logical-decoding-boundary.md](../../../docs/design/logical-decoding-boundary.md).

## What it owns

- **Changes** (`change.go`): `Change` (INSERT, UPDATE, DELETE, TRUNCATE) with
  owned immutable values, `OldTuple` per replica identity, and the capture-time
  `Relation` snapshot shared by a catalog state's changes.
- **History** (`history.go`): `Allocator` hands out fixed-span positions on one
  cluster-wide stream from `Base`; `Record` is a commit, prepare or prepared
  finish of a `Transaction`; `History` is one database's append-only log whose
  `Snapshot` stays valid after appends and trims; `RestartLSN` is the oldest
  position a slot still needs.
- **Plugins** (`plugin.go`): `Plugin` (startup, begin, change, commit, shutdown)
  with the optional `TruncatePlugin`, `TwoPhasePlugin` and `OriginFilter`;
  `Factory` creates one instance per decoding context; `Output` collects rows
  stamped with the callback's position and XID.
- **Decoding** (`decode.go`): `Decode` is the get/peek loop of PostgreSQL's
  `pg_logical_slot_get_changes_guts` over a snapshot: limits after each record,
  whole transactions, cancellation after each record, and the 0A000 boundaries
  for rewrite changes and streaming.

The engine runtime (`core`) owns capture, publication, slots, locking and SQL.

## Testing

`make check RUN='^TestLogical(Decode|History)[A-Za-z]*$'` covers the pure loop and
retention. Engine scenarios through SQL use `RUN='^TestLogicalStream[A-Za-z]*$'`
in `core/enginetests/logical`.
