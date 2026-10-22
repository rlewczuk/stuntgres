# internal/engine/partition

Partitioned-table records and decisions that need no engine: partition
bounds and routing helpers, partition-key recipe evaluation, CREATE,
ATTACH, DETACH and DROP helpers, partition-constraint proofs, retained
checks, the partition dump SQL and dump-event graph, and the Restore replay
records. Step 25 of the engine split created it (25b moved the clean set).
It is an upper feature package (L12 in
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture)),
above `index` and `table`: it imports `access`, `analyze` (types and pure
functions only), `authorization`, `catalog`, `ddl`, `dump`, `exec`, `expr`, `ids`,
`index`, `plan`, `regobj`, `sequence`, `storage`, `stored`, `table`, `typedef`,
`visibility`, `parser`,
`sqlstate`, `sqltype` and the standard library. It never imports the
engine, `view` or `dml`; only the engine and `dml` import it. It never builds a
binder. Partitioned indexes are `index` code. See
[ENGINE-SPLIT-partition.md](../../../docs/ENGINE-SPLIT-partition.md).

## What it owns

- **Bounds and routing** (`bounds.go`, `bind.go`, `hash_bounds.go`,
  `list_bounds.go`, `insert.go`, `query.go`): `ValidateDescriptor`, `CompareTuple`,
  `RowKey` (an `EvaluatedKey`), `Contains`, `RangeContains`, `Accepts`,
  `PhysicalDescendants`; bound preparation (`PrepareBounds`,
  `GeneratedError`); hash bounds (`ValidHashBound`, `HashOverlap`,
  `CompareHashBound`); the INSERT routing state `InsertState` (`Route`
  walks to the accepting leaf, `RowFor` maps a target row to a level's
  layout, `MissingPartition` is the 23514 failure that discloses key values
  only to an actor without RLS who may SELECT them) with its per-leaf
  `InsertGroup`; and `CaptureQueryPrefix`. `InsertState` keeps the captured
  `access.View` and its environment: Restore's trusted insertion authority
  carries its own `$user`, so the environment cannot be rebuilt from the
  view.
- **Key recipes** (`routine.go`): `EvaluateRecipe` evaluates a partition
  key expression under the statement's routine program. It reaches the
  statement through `RecipeHost`, recovered from the evaluation's live
  host; the root implements it on `statementHost`. `Scope` is the
  statement's partition slot (cached `Programs`, the attach
  `ValidationView`); the root's `StatementScope` holds it.
- **CREATE, ATTACH, DETACH, DROP helpers** (`create.go`, `attach.go`,
  `detach.go`, `exclusion.go`, `locks.go`): `Boundary` and
  `TupleSQL` for overlap diagnostics; `AttachEligibility`,
  `AttachKindError`, `AttachSchema`, the attach scan jobs `AttachScan`
  and `AttachRowConstraint`; `DetachMembership`, `DetachKindError`;
  `ExclusionAddActions`; the key dependency check is
  `catalog.PartitionKeyDependsOn`; `AppendCompatible`,
  `KeyUsesAttribute`, `WriteExtensionBoundary`.
- **Admission decisions** (`locks.go`, 25e): `ConsumerBoundary` rejects the
  statements partition hierarchies do not support (0A000, and the 42P16
  DROP COLUMN errors); the caller's Restore trust check is one callback,
  consulted only for ALTER TABLE and ALTER ... DEFAULT at their old place
  in the check order. `Ancestors` returns a relation's ancestors root
  first, for the caller to lock before the leaf. `CaptureCatalog` puts the
  statement's admitted descriptors back over their append growth. None of
  them waits or locks.
- **Proofs and retained checks** (`proof.go`, `retained_checks.go`):
  `ConstraintProven` proves a child's constraints imply its partition
  bound, so ATTACH can skip the scan; `ProofGoal`, `ProofTuple`.
  `RetainedChecks.ForRow` picks the CHECKs a retained row re-validates
  during hash-partition remap repair; `CheckRecipeAffected` decides
  whether a trusted catalog import changed a CHECK recipe's inputs.
- **Query refutation** (`prune.go`, `prune_predicate.go`): `CapturePruner`
  borrows the admitted source's immutable catalog/topology and physical layouts.
  `CapturedPruner.Select` runs unlocked through the narrow exec-owned
  `QueryBranchPruner` interface. Typed interval-domain refutation combines
  WHERE TRUE with applicable CHECK TRUE-or-UNKNOWN and ancestor membership.
  It emits captured ordinal intervals, not copied rows or per-row candidates.
  HASH shares `AccumulateHash` with routing. Unsupported provider/identity/
  collation/probe contracts retain possibilities. The engine still owns full
  admission, locks, parameter import, preflight and capture lifetime; exec owns
  actual interval skipping, residual evaluation and stage reset/close.
  Boolean backtracking processes at most 4096 search entries across one entire
  Select, including terminal membership checks, not 4096 per physical branch.
  Exhaustion conservatively retains the current and later branches; earlier
  proven omissions remain valid. Cancellation is checked before fallback, and
  actual errors reached by proof providers propagate unchanged. Normalization,
  metadata traversal and each reached provider's work are input-sized costs,
  not a constant-time guarantee.
- **Dump and replay** (`dump.go`, `dump_events.go`, `constraint_dump.go`,
  `invalid_dump.go`, `unique_dump.go`, `replacement.go`, `replay.go`):
  the SQL builders (`ChildSQL`, `KeySQL`, `AttachDumpSQL`,
  `DumpNeedsAttach`, `VirtualUniqueDefinitionSQL`,
  `IndexDumpAttachmentSQL`), `DumpPredecessors`, the dump-event graph
  (`DumpEvents`, `InvalidDumpEvents`), the constraint-name replay record
  `ConstraintReplay` (built by `ConstraintDumpCatalog`) and
  `InvalidReplay`; the remap repair (`HasHashPartition`,
  `ReplacementRow`, and `RepairRemappedHash`, which routes and validates
  the live rows of remapped hash leaves and returns the `HashRepair` moves
  without changing storage); and the Restore replay records `UniqueReplay`,
  `UsingIndexReplay`, `ReplayTeardown` and `CheckReplayActions`, which the
  root leases on its `restoreReplay` slot. Dump code resolves
  predecessors through the `dump.Resolver` the root passes in.
- **Handler decisions** (`host.go`, `definition.go`, `create.go`,
  `bounds.go`, `hash_bounds.go`, `attach.go`, `detach.go`, `exclusion.go`,
  `replay.go`; 25d): `Host` is `table.Host` plus `BindKeyExpression`,
  implemented by the root `ddlHost`; every method runs with `Engine.mu`
  held, never takes or releases it and never waits. `BoundBinder` binds
  bound datums and is self-locking: callers hold nothing, it takes
  `Engine.mu` for one window and evaluation follows unlocked. `Definition`
  is the pending edge (parent, child, bound, raw syntax, default leaves,
  unsupported flag), with `WithChild`, `WithoutChild`,
  `CheckDefaultConflict` and `CheckSupported`. Under `Engine.mu`:
  `RejectForeignKeys`, `StageCreate` (the PARTITION OF child or the
  PARTITION BY table, keys, bound, default leaves), `BindKeys`,
  `StageIndexChildren` and `StageExclusions`. With nothing held:
  `EvaluateBounds`, `ValidateBounds`, `CheckOverlap`, `CheckCoverage`,
  `CheckDefaultRows`, `CheckAttachTarget`, `ValidateAttachRows`,
  `AttachDescriptors` and `StageDetach`. Restore replay cores:
  `CheckReplayTarget`, `CaptureUniqueReplay`, `CaptureUsingIndexReplay` and
  the `Matches` methods of `UniqueReplay` and `UsingIndexReplay`.

## What stays in the engine

The orchestration and everything that holds `Engine`, `StatementScope` or
`CatalogStage`: `schemaPartitionTableLocked` (the FOREIGN KEY rejection
first, the parent resolution, Restore parent authority, locked identity and
owner check, then the default leaves' storage-read fences and data views
after `StageCreate`); `pendingPartitionDefinition`, which embeds
`Definition` and whose `finish` sequences the moved checks in their
diagnostic order and takes `Engine.mu` for the index children;
`attachPartitionLocked` and `detachPartitionLocked` (`Engine.mu` windows,
lock waits, the attach frontier and capture, stage replacement, validation
and publication); `executePartitionExclusionAddLocked`'s windows; lock
admission (`partition_locks.go`: the lock waits and the observed
descriptors around `Ancestors`, with `Engine.mu` held for the walk); the
authorization captures and trust checks (`partitionConstraintReplayImport`,
`partitionParentAuthorization`, `partitionAttachAuthorization`,
`partitionDefaultReplayAllowed`, and `partitionReplayAllowed`, the
`ConsumerBoundary` callback over the four `*ReplayAllowed` checks); the `begin*Replay` and `*ReplayAllowed`
hook wrappers and the Restore executors
(`executePartitionUniqueReplayLocked`, `repairRemappedHashPartitions` with
the storage install loop under `Engine.mu`, the teardown capture); `dropProvider.removePartitionHierarchyLocked` (over
`WithoutChild`); the storage-read fences (`storage_read_fence.go`); and
INSERT routing execution around `InsertState` (the capture under
`Engine.mu`, the partition lock, physical validation, the target
constraint and the per-leaf groups; step 29 moves them to `dml`). `partition_host.go` holds
`ddlHost.BindKeyExpression` and the `partitionBoundBinder` adapter.

## Invariants

- Routing picks the same partition for every row; "no partition of
  relation found for row" keeps its SQLSTATE, message and DETAIL.
- ATTACH skips the scan only when `ConstraintProven` proves the bound.
- Dump output is byte-identical: the dump-event graph keeps its order and
  never iterates maps into output.
- The package holds no engine state and no mutex. Functions here never
  wait; callers in the root own `Engine.mu`, lock waits and publication.
- LIST has one key and one immutable typed Values slice per member. Bound
  expressions execute once before overlap/default-row checks. Same-list
  duplicates normalize with the key's admitted comparator; sibling value/NULL
  ownership is exclusive. Routing performs no rendering, sorting or lookup-cache
  allocation. DEFAULT stays the live sibling complement.
- LIST metadata, dump literals and logical remap use key0 for every value, never
  the list ordinal. Typed NULL preserves its descriptor without nonnull output.
  Private LIST replay uses full-row validation; public LIST ATTACH stays excluded.
- CHECK/NOT NULL descriptors own exact `PartitionOrigin` metadata. CREATE and
  ATTACH populate immediate origins; DETACH removes the top contribution.
  `ValidateConstraintOrigins` checks actual parent fact identities and NN column
  mapping, including valid local-only survivors after ONLY/DROP. Catalog output
  never infers locality from a currently matching parent name. Dump emits local
  NN declarations (including child-generated/different names), omits inherited
  CHECKs and uses the existing attachment fallback only when final shape needs it.
  Selected recursive ALTER policy belongs in `table/partition_alter*.go`; the
  engine reuses its ordinary one-stage orchestration and publication fences.

## Testing

Package-local `TestPartitionPruningProof` tests typed predicate refutation,
CHECK UNKNOWN, incompatible providers/identities and forbidden probe safety.
Engine `TestPartitionPruningProof` asserts actual captured physical branch
selections. `TestPartitionPruningExecution` observes actual residual demand by
captured row-buffer/physical identity, with duplicate and same-branch residual
controls. `TestPartitionPruningSafety` observes actual provider callbacks unlocked,
original callback errors/cancellation and release of every captured view handle.
`TestPartitionPruningSearchBudget` asserts exact shared search accounting,
terminal/error/cancellation boundaries, domain restoration and conservative
fallback. Engine `TestPartitionPruningSearchBound` counts actual proof checkpoints
through captured Select on empty/populated eight-branch sources, without timing
thresholds or synthetic cancellation. `TestReferencePartitionPruningComplexPredicate`
independently checks complex direct/prepared/held bags and empty metadata.

```sh
make check RUN='^TestPartitionPruningSearch(Budget|Bound)$'
make check RUN='^TestReferencePartitionPruningComplexPredicate$'
make check RUN='^TestReferencePartitionPruningComplexPredicate$' REFERENCE=1
```

Exec `TestQueryBranchPruning` covers interval/scan-range/raw-ordinal and rewind
behavior. Other partition units build an engine and stay in the root package,
naming the moved code as `partition.X`.
`TestPartitionHost*` checks the root `ddlHost.BindKeyExpression`.

Exact provenance clone/validation and parent-qualified remap units:

```sh
make check RUN='^TestPartitionInherited(Provenance|OriginRemap)$'
```

Public selected actions, controls, cancellation, replay and feature smoke:
[reference/tables](../../../reference/tables/README.md#inherited-partition-constraints).


LIST selected units and real production-phase cancellation proof:

```sh
make check RUN='^TestListPartitionGrammar$'
make check RUN='^TestListPartition(Descriptor|Comparison|LogicalRemap)$'
make check RUN='^TestListPartition(PublicationCancellation|CancellationLockRelease)$' RACE=1
```

`TestListPartitionLogicalRemap` pins actual destination builtin generations in
a live TEMP table before cross-engine import. This forces scalar/domain datum
reownership rather than demanding pointer inequality when unchanged immutable
codec sharing is valid. It asserts destination catalog authority, NULL/nonnull
routing and exact preservation of the TEMP descriptor, storage and values.

Public LIST behavior and native control/runtime-smoke instructions are in
[reference/tables](../../../reference/tables/README.md#list-partition-definitions).

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
