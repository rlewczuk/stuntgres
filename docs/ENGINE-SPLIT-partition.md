# Engine split: `internal/engine/partition`, fifth round

Session prompt for linear item 157 of the
[status page](ENGINE-SPLIT-STATUS.md). The rules are in
[ENGINE-SPLIT.md](ENGINE-SPLIT.md) §2, §3 and §5, and the package contract
is in [the partition README](../internal/engine/partition/README.md). Sizes
were measured at `fe0683b68`; re-run `reach` on the move set first.

## Item 157 — `pg_partition_*` values

Needs —. About 0.18k lines move.

**Moves.** From `partition_info_runtime.go`: `partitionInquiryRelation`,
`partitionInquiryResult`, `partitionTreeValues`, `partitionAncestorValues`,
`partitionRootValue` and `partitionRegclassValue`. `reach` already reports
all six as clean: they read only a captured catalog and `sqltype` values.

**Cut.** None is needed. Export the six as `partition` functions over
`catalog.Read`.

**Stays root.** The three `statementHost` methods (about 40 lines), which
now share one helper.

**Callers and locks.** Nothing is held.

**Tests.**
- `partition_info_runtime_test.go` moves to `partition`.
- Quick and `REFERENCE=1` selection: `TestReferencePartitionInfo`.

**Docs.** Add the inquiry values to the partition README.

## Status

- 157: done (`cc1e5d46c`, then the docs commit). `partition/inquiry.go` holds
  `TreeValues`, `AncestorValues` and `RootValue`; root
  `partition_info_runtime.go` is 234 → 53 lines, its three methods sharing
  `statementHost.partitionInquiry`.
  - Deviation: `inquiryRelation`, `inquiryResult` and `regclassValue` stay
    unexported, because only the package calls them.
  - Tests: `TestPartitionInfoTraversal` and `TestPartitionInfoCancellation`
    moved to `partition/inquiry_test.go` over a fresh builtin type generation;
    the foreign-descriptor check became `TestPartitionInfoForeignResult`
    (also covering the tree shape checks). `TestPartitionInfoCaptureOwnership`
    stays in the root: it needs the engine-owned retained catalog.
