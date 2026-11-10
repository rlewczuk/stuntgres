# internal/engine/logical

Native logical event stream below core. The package has no lock or live session
state; see [logical-decoding-boundary.md](../../../docs/design/logical-decoding-boundary.md).

## Ownership

- `Change` owns immutable typed values and historic relation/type snapshots,
  identity/rewrite provenance and capture-time decoded-memory charge.
- `History` owns ordered row, catalog/invalidation, completion and prepared
  lifecycle records. Reconstruction anchors are independent of successful
  effects; rollback can remove effects without retiring an open/prepared anchor.
  Append/trim/reset never mutate retained snapshots or their descriptors.
- `Plugin` supplies ordinary callbacks and explicit optional capabilities.
  Factories create fresh contexts; output owns emitted bytes and callback stamps.
- `Decode` owns one fresh reorder pass. Confirmed positions, memory budgeting,
  complete event/block boundaries and filtering are independent of formatting.
  No reorder state survives in a slot or prepared statement. Callback, formatting
  and cancellation failure return no successful partial result.

Core owns actual staging, canonical XID assignment, lifecycle publication,
coherent status/frontier capture, slots, locks and confirmation. Memory charges
model decoded events, not physical WAL/heap bytes or Go allocation sizes.

## Testing

Pure history/reorder/plugin units live here. Composed real staging and lifecycle
tests live in `core/enginetests/logical`; stock formatting belongs to
`logical/testdecoding`. Public SQL belongs to reference packages.
