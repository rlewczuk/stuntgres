# internal/engine/partition

Partitioned-table records and decisions that need no engine: partition bounds
and routing helpers, partition-key recipe evaluation, CREATE, ATTACH, DETACH and
DROP helpers, partition-constraint proofs, retained checks, query branch
refutation, the partition dump SQL and dump-event graph, and the Restore replay
records. Partitioned indexes are [`index`](../index/README.md) code; selected
recursive ALTER policy is in `table/partition_alter*.go`.

## Imports

An upper feature package (L12 in
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture); see
also [ENGINE-SPLIT-partition.md](../../../docs/ENGINE-SPLIT-partition.md)), above
`index` and `table`. It imports `access`, `analyze` (types and pure functions
only), `authorization`, `catalog`, `ddl`, `dump`, `exec`, `expr`, `ids`, `index`,
`plan`, `regobj`, `routine`, `sequence`, `session`, `storage`, `stored`, `table`,
`typedef`, `visibility`, `parser`, `sqlstate`, `sqltype` and the standard
library. It never imports the engine, `view` or `dml`; only the engine,
`control` and `dml` import it. It never builds a binder.

## What it owns

- **Bounds and routing** (`bounds.go`, `bind.go`, `hash_bounds.go`,
  `list_bounds.go`, `insert.go`, `query.go`): `ValidateDescriptor`,
  `CompareTuple`, `RowKey` (an `EvaluatedKey`), `Contains`, `RangeContains`,
  `Accepts`, `PhysicalDescendants`; bound preparation (`PrepareBounds`,
  `GeneratedError`); hash bounds (`ValidHashBound`, `HashOverlap`,
  `CompareHashBound`); the INSERT routing state `InsertState` (`Route` walks to
  the accepting leaf, `RowFor` maps a target row to a level's layout,
  `MissingPartition` is the 23514 failure that discloses key values only to an
  actor without RLS who may SELECT them) with its per-leaf `InsertGroup`; and
  `CaptureQueryPrefix`. `InsertState` keeps the captured `access.View` and its
  environment: Restore's trusted insertion authority carries its own `$user`,
  so the environment cannot be rebuilt from the view.
- **Key recipes** (`routine.go`): `EvaluateRecipe` evaluates a partition key
  expression under the statement's routine program. It reaches the statement
  through `RecipeHost`, recovered from the evaluation's live host; the root
  implements it on `statementHost`. `Scope` is the statement's partition slot
  (cached `Programs`, the attach `ValidationView`); the root's `StatementScope`
  holds it.
- **CREATE, ATTACH, DETACH, DROP helpers** (`create.go`, `attach.go`,
  `detach.go`, `exclusion.go`, `locks.go`): `Boundary` and `TupleSQL` for overlap
  diagnostics; `AttachEligibility`, `AttachKindError`, `AttachSchema`, the
  attach scan jobs `AttachScan` and `AttachRowConstraint`; `DetachMembership`,
  `DetachKindError`; `ExclusionAddActions`; `AppendCompatible`,
  `KeyUsesAttribute`, `WriteExtensionBoundary`. The key dependency check is
  `catalog.PartitionKeyDependsOn`.
- **Admission decisions** (`locks.go`): `ConsumerBoundary` rejects the
  statements partition hierarchies do not accept (0A000, and the 42P16 DROP
  COLUMN errors); the caller's Restore trust check is one callback, consulted
  only for ALTER TABLE and ALTER ... DEFAULT at their place in the check order.
  `Ancestors` returns a relation's ancestors root first, for the caller to lock
  before the leaf. `CaptureCatalog` puts the statement's admitted descriptors
  back over their append growth. None of them waits or locks.
- **Attach frontier, INSERT capture and constraint drop** (`attach.go`,
  `insert.go`, `constraint_drop.go`): `AttachFrontier` returns the leaves ATTACH
  must scan, skipping every relation whose constraints prove the bound; it holds
  nothing and waits only in the caller's `lockChild` callback, which locks and
  rereads each DEFAULT descendant (the candidate passes nil: its descendants are
  already admitted). `CaptureInsertLocked` builds an `InsertState` with the
  layouts of the target, its descendants and its ancestors; it runs under the
  caller's `Engine.mu`, and its `regTS` and `leaf` snapshot callbacks never
  wait. `StageConstraintDrop` stages one DROP CONSTRAINT action of a partition
  hierarchy (UNIQUE or EXCLUDE with its partitioned index, else the
  `table.NotNullCandidate` check) over `index.DropStage` with nothing held, and
  returns the stage candidate's removals sorted by object ID.
- **Proofs and retained checks** (`proof.go`, `retained_checks.go`):
  `ConstraintProven` proves a child's constraints imply its partition bound, so
  ATTACH can skip the scan; `ProofGoal`, `ProofTuple`. `RetainedChecks.ForRow`
  picks the CHECKs a retained row re-validates during hash-partition remap
  repair; `CheckRecipeAffected` decides whether a trusted catalog import changed
  a CHECK recipe's inputs.
- **Query refutation** (`prune.go`, `prune_predicate.go`): see below.
- **Dump and replay** (`dump.go`, `dump_events.go`, `constraint_dump.go`,
  `invalid_dump.go`, `unique_dump.go`, `replacement.go`, `replay.go`): the SQL
  builders (`ChildSQL`, `KeySQL`, `AttachDumpSQL`, `DumpNeedsAttach`,
  `VirtualUniqueDefinitionSQL`, `IndexDumpAttachmentSQL`), `DumpPredecessors`,
  the dump-event graph (`DumpEvents`, `InvalidDumpEvents`), the constraint-name
  replay record `ConstraintReplay` (built by `ConstraintDumpCatalog`) and
  `InvalidReplay`; the remap repair (`HasHashPartition`, `ReplacementRow`, and
  `RepairRemappedHash`, which routes and validates the live rows of remapped
  hash leaves and returns the `HashRepair` moves without changing storage); and
  the Restore replay records `UniqueReplay`, `UsingIndexReplay`,
  `ReplayTeardown` and `CheckReplayActions`, which the root leases on its
  `restoreReplay` slot. Dump code resolves predecessors through the
  `dump.Resolver` the root passes in.
- **Handler decisions** (`host.go`, `definition.go`, `create.go`, `bounds.go`,
  `hash_bounds.go`, `attach.go`, `detach.go`, `exclusion.go`, `replay.go`):
  `Host` is `table.Host` plus `BindKeyExpression`, implemented by the root
  `ddlHost`; every method runs with `Engine.mu` held, never takes or releases it
  and never waits. `BoundBinder` binds bound datums and is self-locking: callers
  hold nothing, it takes `Engine.mu` for one window and evaluation follows
  unlocked. `Definition` is the pending edge (parent, child, bound, raw syntax,
  default leaves, unsupported flag), with `WithChild`, `WithoutChild`,
  `CheckDefaultConflict` and `CheckSupported`.
  - Under `Engine.mu`: `RejectForeignKeys`, `StageCreate` (the PARTITION OF
    child or the PARTITION BY table, keys, bound, default leaves), `BindKeys`,
    `StageIndexChildren` and `StageExclusions`.
  - With nothing held: `EvaluateBounds`, `ValidateBounds`, `CheckOverlap`,
    `CheckCoverage`, `CheckDefaultRows`, `CheckAttachTarget`,
    `ValidateAttachRows`, `AttachDescriptors` and `StageDetach`.
  - Restore replay cores: `CheckReplayTarget`, `CaptureUniqueReplay`,
    `CaptureUsingIndexReplay` and the `Matches` methods of `UniqueReplay` and
    `UsingIndexReplay`.

## Query branch refutation

`CapturePruner` borrows the admitted source's immutable catalog/topology and
physical layouts. `CapturedPruner.Select` runs unlocked through the narrow
exec-owned `QueryBranchPruner` interface. Typed interval-domain refutation
combines WHERE TRUE with applicable CHECK TRUE-or-UNKNOWN and ancestor
membership, and emits captured ordinal intervals, not copied rows or per-row
candidates. HASH shares `AccumulateHash` with routing. Unsupported
provider/identity/collation/probe contracts keep the branch possible.

- **Shared budget.** Boolean backtracking processes at most 4096 search entries
  across one entire Select, including terminal membership checks, not 4096 per
  physical branch. Exhaustion conservatively retains the current and later
  branches; earlier proven omissions stay valid. Cancellation is checked before
  fallback, and actual errors reached by proof providers propagate unchanged.
  Normalization, metadata traversal and each reached provider's work are
  input-sized costs, not a constant-time guarantee.
- **Ownership.** The engine owns full admission (lock manifests still include
  the whole hierarchy), locks, parameter import, preflight and capture lifetime;
  exec owns interval skipping, residual evaluation and stage reset/close.
  Selection is private to the current execution and never cached in prepared
  analysis; every retained candidate still runs the complete residual
  predicate, and an empty selection suppresses no binding, privilege, RLS or
  provider error.

## What stays in the engine

The orchestration and everything that holds `Engine`, `StatementScope` or
`CatalogStage`: `schemaPartitionTableLocked` (the FOREIGN KEY rejection first,
the parent resolution, Restore parent authority, locked identity and owner
check, then the default leaves' storage-read fences and data views after
`StageCreate`); `pendingPartitionDefinition`, which embeds `Definition` and
whose `finish` sequences the checks in their diagnostic order and takes
`Engine.mu` for the index children; `attachPartitionLocked` and
`detachPartitionLocked` (`Engine.mu` windows, lock waits including
`lockPartitionAttachChild`, the `AttachFrontier` callback, the attach capture
`partitionAttachCapture`, stage replacement, validation and publication);
`executePartitionExclusionAddLocked`'s windows;
`executePartitionConstraintDropLocked` (the stage windows,
`stageExpressionDependents` over the roots `StageConstraintDrop` returns,
validation and the overlay); lock admission (`partition_locks.go` and
`acquirePartitionDropLocks`: the lock waits and the observed descriptors around
`Ancestors`, with `Engine.mu` held for the walk); the authorization captures and
trust checks (`partitionConstraintReplayImport`, `partitionParentAuthorization`,
`partitionAttachAuthorization`, `partitionDefaultReplayAllowed`, and
`partitionReplayAllowed`, the `ConsumerBoundary` callback over the four
`*ReplayAllowed` checks); the `begin*Replay` hook wrappers and the Restore
executors (`executePartitionUniqueReplayLocked`, `repairRemappedHashPartitions`
with the storage install loop under `Engine.mu`, the teardown capture);
`dropProvider.removePartitionHierarchyLocked` (over `WithoutChild`); the
storage-read fences (`storage_read_fence.go`); and INSERT routing capture
(`capturePartitionInsertLocked`, which passes the comment and CHECK snapshot
captures to `CaptureInsertLocked` under `Engine.mu`). `partition_host.go` holds
`ddlHost.BindKeyExpression` and the `partitionBoundBinder` adapter.

## Invariants

- The package holds no engine state and no mutex. Functions here never wait
  except through a caller's callback that runs with nothing held
  (`AttachFrontier`'s `lockChild`); callers in the root own `Engine.mu`, lock
  waits and publication.
- **Storage ownership.** Virtual roots and intermediates own descriptors only;
  physical leaves own storage and local enforcing roots. One write stage routes
  and validates all destinations, so a late failure publishes no earlier leaf
  rows. Defaults and disclosure authority belong to the named target; physical
  errors identify the actual leaf. Scans capture all descendant prefixes at one
  frontier.
- **Routing** picks the same partition for every row and performs no rendering,
  sorting or lookup-cache allocation; "no partition of relation found for row"
  keeps its SQLSTATE, message and DETAIL. HASH uses the extended scalar hash
  support functions and the NULL-skipping ordered uint64 row combiner, never
  grouping or index hashes.
- **LIST** has one key and one immutable typed Values slice per member. Bound
  expressions execute once before overlap/default-row checks. Same-list
  duplicates normalize with the key's admitted comparator; sibling value/NULL
  ownership is exclusive. DEFAULT stays the live sibling complement. LIST
  metadata, dump literals and logical remap use key 0 for every value, never the
  list ordinal. Typed NULL preserves its descriptor without nonnull output.
  Private LIST replay uses full-row validation.
- **ATTACH** skips the scan only when `ConstraintProven` proves the bound.
  Compatibility matches columns by name/type/typmod/collation, required NOT NULL
  by mapped attribute and CHECK by name and cooked definition, never by physical
  slot. Lock order: the parent keeps SHARE UPDATE EXCLUSIVE; the candidate, its
  descendants and the incumbent DEFAULT take ACCESS EXCLUSIVE. DETACH takes
  parent, incumbent DEFAULT, candidate, then descendant ACCESS EXCLUSIVE; real
  waits run without `Engine.mu`.
- **Constraint origins.** CHECK/NOT NULL descriptors own exact `PartitionOrigin`
  metadata. CREATE and ATTACH populate immediate origins; DETACH removes the top
  contribution, making matched inherited facts local without new identities.
  `ValidateConstraintOrigins` checks actual parent fact identities and NN column
  mapping, including valid local-only survivors after ONLY/DROP. Catalog output
  never infers locality from a currently matching parent name.
- **Partitioned exclusions.** RANGE ATTACH preserves independent exclusions and
  clones a fresh inherited graph; DETACH makes crossing providers local but
  keeps the detached subtree's internal edges; DROP follows inherited edges
  only. Coverage rules are in the [`index` README](../index/README.md).
- **Dump.** Output is byte-stable: the dump-event graph keeps its order and never
  iterates maps into output. Dump emits local NN declarations (including
  child-generated/different names), omits inherited CHECKs and uses the
  attachment fallback only when final shape needs it; each local exclusion
  provider is declared once and inherited providers are recreated automatically.

## Testing

Package units cover pruning proofs (typed refutation, CHECK UNKNOWN,
incompatible providers, probe safety) and the shared search budget. Engine units
assert captured branch selection, residual demand, unlocked provider callbacks
and proof checkpoint counts without timing thresholds; other partition units
build an engine and stay in `internal/engine`, naming the code as
`partition.X`. Run attach/detach overlay, lock-fence and LIST cancellation units
with `RACE=1`. Public SQL lives in
[reference/tables](../../../reference/tables/README.md#testing); add
`REFERENCE=1` for the oracle.

```sh
make check RUN='^TestPartitionPruning'
make check RUN='^Test(Partition|RangePartition|HashPartition|ListPartition)'
make check RUN='^TestReference(Range|Hash|List)Partition'
make check RUN='^TestReferencePartition'
```
