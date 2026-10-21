# internal/engine/partition

Partitioned-table records and decisions that need no engine: partition
bounds and routing helpers, partition-key recipe evaluation, CREATE,
ATTACH, DETACH and DROP helpers, partition-constraint proofs, retained
checks, the partition dump SQL and dump-event graph, and the Restore replay
records. Step 25 of the engine split created it (25b moved the clean set).
It is an upper feature package (L12 in
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture)),
above `index` and `table`: it imports `analyze` (types and pure functions
only), `authorization`, `catalog`, `dump`, `exec`, `expr`, `ids`, `index`,
`plan`, `regobj`, `stored`, `table`, `typedef`, `visibility`, `parser`,
`sqlstate`, `sqltype` and the standard library. It never imports the
engine, `view` or `dml`; only the engine imports it. It never builds a
binder. Partitioned indexes are `index` code. See
[ENGINE-SPLIT-partition.md](../../../docs/ENGINE-SPLIT-partition.md).

## What it owns

- **Bounds and routing** (`bounds.go`, `bind.go`, `hash_bounds.go`,
  `insert.go`, `query.go`): `ValidateDescriptor`, `CompareTuple`,
  `RowKey` (an `EvaluatedKey`), `Contains`, `RangeContains`, `Accepts`,
  `PhysicalDescendants`; bound preparation (`PrepareBounds`,
  `GeneratedError`); hash bounds (`ValidHashBound`, `HashOverlap`,
  `CompareHashBound`); the INSERT routing group `InsertGroup`; and
  `CaptureQueryPrefix`.
- **Key recipes** (`routine.go`): `EvaluateRecipe` evaluates a partition
  key expression under the statement's routine program. It reaches the
  statement through `RecipeHost`, recovered from the evaluation's live
  host; the root implements it on `statementHost`. `Scope` is the
  statement's partition slot (cached `Programs`, the attach
  `ValidationView`); the root's `StatementScope` holds it.
- **CREATE, ATTACH, DETACH, DROP helpers** (`create.go`, `attach.go`,
  `detach.go`, `drop.go`, `exclusion.go`, `locks.go`): `Boundary` and
  `TupleSQL` for overlap diagnostics; `AttachEligibility`,
  `AttachKindError`, `AttachSchema`, the attach scan jobs `AttachScan`
  and `AttachRowConstraint`; `DetachMembership`, `DetachKindError`;
  `DependsOn`; `ExclusionAddActions`; `AppendCompatible`,
  `KeyUsesAttribute`, `WriteExtensionBoundary`.
- **Proofs and retained checks** (`proof.go`, `retained_checks.go`):
  `ConstraintProven` proves a child's constraints imply its partition
  bound, so ATTACH can skip the scan; `ProofGoal`, `ProofTuple`.
  `RetainedChecks.ForRow` picks the CHECKs a retained row re-validates
  during hash-partition remap repair; `CheckRecipeAffected` decides
  whether a trusted catalog import changed a CHECK recipe's inputs.
- **Dump and replay** (`dump.go`, `dump_events.go`, `constraint_dump.go`,
  `invalid_dump.go`, `unique_dump.go`, `replacement.go`, `replay.go`):
  the SQL builders (`ChildSQL`, `KeySQL`, `AttachDumpSQL`,
  `DumpNeedsAttach`, `VirtualUniqueDefinitionSQL`,
  `IndexDumpAttachmentSQL`), `DumpPredecessors`, the dump-event graph
  (`DumpEvents`, `InvalidDumpEvents`), the constraint-name replay record
  `ConstraintReplay` (built by `ConstraintDumpCatalog`) and
  `InvalidReplay`; the remap repair rows (`HasHashPartition`,
  `ReplacementRow`); and the Restore replay records `UniqueReplay`,
  `UsingIndexReplay`, `ReplayTeardown` and `CheckReplayActions`, which the
  root leases on its `restoreReplay` slot. Dump code resolves
  predecessors through the `dump.Resolver` the root passes in.

## What stays in the engine

The handlers and everything that holds `Engine`, `StatementScope` or
`CatalogStage`: `attachPartitionLocked`, `detachPartitionLocked`,
`stagePartitionDetach`, `schemaPartitionTableLocked`,
`schemaPartitionChildLocked`, `pendingPartitionDefinition`,
`bindPartitionKeys`, `executePartitionExclusionAddLocked`, the attach scans
and captures, lock admission (`partition_locks.go`,
`partitionConsumerBoundary`), the authorization captures, the
`*ReplayAllowed` checks and `partitionConstraintReplayImport`, the Restore
executors (`begin*Replay`, `repairRemappedHashPartitions`, the teardown
capture), `dropProvider.removePartitionHierarchyLocked`, the storage-read
fences (`storage_read_fence.go`), and INSERT routing execution with
`partitionInsertState` (it holds an `AuthorizationView`; step 29 moves
it). 25c/25d move the handler decisions behind a `partition.Host`.

## Invariants

- Routing picks the same partition for every row; "no partition of
  relation found for row" keeps its SQLSTATE, message and DETAIL.
- ATTACH skips the scan only when `ConstraintProven` proves the bound.
- Dump output is byte-identical: the dump-event graph keeps its order and
  never iterates maps into output.
- The package holds no engine state and no mutex. Functions here never
  wait; callers in the root own `Engine.mu`, lock waits and publication.

## Testing

No package-local tests: every partition unit test builds an engine and
stays in the root package, naming the moved code as `partition.X`.

```sh
make check RUN='^Test(Partition|RangePartition|HashPartition)'
make check RACE=1 RUN='^(TestPartitionAttachTransactionOverlay|TestPartitionDetachTransactionOverlay|TestPartitionDetachLockFences)$'
```

`TestPartitionRoutineOriginalAuthority` exercises genuine bound partition-key
outer/body manifests, missing/empty/foreign/stale/retag rejection and sibling
isolation. Complete-success statement preparation preserves enclosing row/scope/
frame, actual actor/command/RI phases and graph-only post-wait readmission.
The simple-key path initializes no derived program. The retained-check fixture
uses its actual copied generation owner/attribute; missing-owner rejection stays.

```sh
make check RUN='^TestPartitionRoutineOriginalAuthority$'
make check RUN='^TestPartitionRoutineOriginalAuthority$' RACE=1
```

Public behavior is covered by the dual-mode reference suite:

```sh
make check RUN='^TestReference(RangePartition|HashPartition|PartitionConstraints|PartitionAttach|PartitionDetach|PartitionExclusion|DumpRoundTrip|InsertConflict)'
```
