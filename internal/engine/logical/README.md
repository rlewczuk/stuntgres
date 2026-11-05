# internal/engine/logical

The native logical event stream below the engine runtime. It imports `sqltype`,
`sqlstate` and the standard library and holds no lock. See
[logical-decoding-boundary.md](../../../docs/design/logical-decoding-boundary.md).

## Ownership

- `Change` owns immutable typed value vectors, historic relation/type snapshots,
  replica identity, rewrite provenance and a capture-time decoded-memory charge.
- `History` owns ordered staged-row, catalog/invalidation, top/child completion,
  prepare and prepared-finish records. Open/prepared anchors retain reconstruction
  prefixes independently of the successful-effect projection, including when
  child rollback removes every effect before PREPARE. Either prepared terminal
  retires the active anchor; consuming that terminal permits prefix trimming.
  Snapshots and their original anchors/prepared descriptors stay valid across
  append, trim, status transitions and Reset; trim replaces backing.
- `Plugin` has required ordinary callbacks and optional TruncatePlugin,
  TwoPhasePlugin, StreamingPlugin and OriginFilter. Factory creates a fresh
  context. Output owns emitted bytes and stamps callback LSN/XID, carrying the
  operation context through potentially long typed formatting.
- `Decode` and `stream.go` own one fresh reorder pass. Confirmed position gates
  stream triggers, not historical queued tuples. Buffered scheduling uses the
  total logical budget inclusively and drains the largest eligible top
  transaction. Queued DML/truncate/rewrite evidence makes that top eligible for
  the context, even after drains or child aborts; catalog work alone cannot.
  Catalog work still contributes memory and can form empty blocks after real
  streamable work. Immediate mode drains complete events, with catalog-liveness
  checks for aborts. No reorder state survives in a slot or prepared statement.
- Limits follow complete input events: ordinary commit replay is whole, while
  streaming may stop at a complete block. Callback/format/cancel failures return
  no successful result. Rewrite/origin filtering is independent of formatting.

The runtime owns actual staging, canonical XID assignment, lifecycle publication,
coherent frontier/status capture, locking, slot retention and confirmation.
Decoded-memory charges cover a fixed header, typed slots and canonical payload
bytes, not physical WAL/heap bytes or Go heap allocation sizes.

## Testing

`make check RUN='^TestLogical(Decode|History)[A-Za-z]*$'` covers reorder, immutable
snapshots, rollback-only prepared retention/retirement, cancellation and limits;
add `RACE=1` for shared snapshots.
Composed real staging and lifecycle owners use
`make check RUN='^TestLogicalStream[A-Za-z]*$'` in `core/enginetests/logical`.
Stock formatting and options use `make check RUN='^TestTestDecoding'`.
