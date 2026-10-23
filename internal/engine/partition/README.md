# internal/engine/partition

Partitioned-table records and decisions that need no engine: bounds and
routing, key recipes, CREATE/ATTACH/DETACH/DROP helpers, constraint proofs,
query branch refutation, dump SQL and Restore replay records. Partitioned
indexes are [`index`](../index/README.md) code; recursive ALTER policy is in
`table/partition_alter*.go`.

## Imports

An upper feature package (L12 in
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture)) above
`index` and `table`. It imports lower engine packages (`analyze` for types and
pure functions only), `parser`, `sqlstate`, `sqltype` and the standard library,
never the engine, `view` or `dml`; only the engine, `control` and `dml` import
it. It never builds a binder.

## What it owns

- **Bounds and routing** (`bounds.go`, `*_bounds.go`, `insert.go`): bound
  validation and containment; `InsertState.Route` walks to the accepting leaf,
  and `MissingPartition` discloses key values only to an actor without RLS who
  may SELECT them. `InsertState` keeps its captured `access.View` and
  environment (Restore's trusted authority has its own `$user`).
- **Key recipes** (`routine.go`): `EvaluateRecipe` evaluates a key expression
  through `RecipeHost` (the root `statementHost`); `Scope` is the statement's
  partition slot in the root `StatementScope`.
- **DDL helpers** (`create.go`, `attach.go`, `detach.go`, `exclusion.go`,
  `constraint_drop.go`) and **admission** (`locks.go`): `ConsumerBoundary`
  rejects statements hierarchies do not accept; `Ancestors` returns ancestors
  root first, for the caller to lock before the leaf.
- **Proofs** (`proof.go`, `retained_checks.go`): `ConstraintProven` lets ATTACH
  skip its scan; `RetainedChecks.ForRow` picks CHECKs for hash-remap repair.
- **Dump and replay** (`dump*.go`, `*_dump.go`, `replacement.go`, `replay.go`):
  SQL builders, `DumpEvents` (over the root's `dump.Resolver`), hash-remap
  repair (`RepairRemappedHash` returns moves without changing storage) and the
  Restore replay records the root leases on its `restoreReplay` slot.

## Host and lock contract

The package holds no engine state and no mutex; the root owns `Engine.mu`, lock
waits and publication.

- `Host` is `table.Host` plus `BindKeyExpression`, implemented by the root
  `ddlHost`. Every method runs with `Engine.mu` held, never takes or releases
  it and never waits.
- `BoundBinder` is self-locking: callers hold nothing, it takes `Engine.mu` for
  one window, and bound evaluation follows unlocked.
- Under `Engine.mu`: `RejectForeignKeys`, `StageCreate`, `BindKeys`,
  `StageIndexChildren`, `StageExclusions` and `CaptureInsertLocked` (whose
  snapshot callbacks never wait).
- With nothing held: `EvaluateBounds`, `ValidateBounds`, `CheckOverlap`,
  `CheckCoverage`, `CheckDefaultRows`, `CheckAttachTarget`,
  `ValidateAttachRows`, `AttachDescriptors`, `StageDetach`,
  `StageConstraintDrop` and the Restore replay cores (`CheckReplayTarget`,
  `Capture*Replay`, `Matches`).
- `AttachFrontier` waits only in the caller's `lockChild` callback, which runs
  with nothing held and locks and rereads each DEFAULT descendant.

## Query branch refutation

`CapturePruner` borrows the admitted source's immutable catalog, topology and
physical layouts; `CapturedPruner.Select` runs unlocked through exec's
`QueryBranchPruner` interface and emits captured ordinal intervals. Unsupported
provider, identity, collation or probe contracts keep the branch possible.

- **Shared budget.** Boolean backtracking processes at most 4096 search entries
  across one entire Select, not per branch. Exhaustion retains the current and
  later branches; earlier proven omissions stay valid. Cancellation is checked
  before fallback; proof-provider errors propagate unchanged.
- **Ownership.** The engine owns admission (lock manifests include the whole
  hierarchy) and capture lifetime; exec owns interval skipping and residual
  evaluation. Selection is never cached in prepared analysis, retained
  candidates run the full residual predicate, and an empty selection
  suppresses no binding, privilege, RLS or provider error.

## What stays in the engine

Orchestration and everything that holds `Engine`, `StatementScope` or
`CatalogStage`: `schemaPartitionTableLocked`, `pendingPartitionDefinition`
(checks in diagnostic order), `attachPartitionLocked`, `detachPartitionLocked`,
lock admission (`partition_locks.go`), authorization and Restore trust checks
(`partition*ReplayAllowed`), the Restore executors, storage-read fences and
INSERT routing capture. `partition_host.go` holds `ddlHost.BindKeyExpression`
and the `partitionBoundBinder` adapter.

## Invariants

- **Storage ownership.** Virtual roots and intermediates own descriptors only;
  physical leaves own storage. One write stage routes and validates all
  destinations, so a late failure publishes no earlier leaf rows. Scans capture
  all descendant prefixes at one frontier.
- **Routing** picks the same partition for every row and allocates no
  rendering, sorting or lookup cache. HASH uses the extended scalar hash support
  functions and the NULL-skipping ordered uint64 row combiner (shared with
  pruning through `AccumulateHash`), never grouping or index hashes.
- **LIST** keeps one immutable typed Values slice per member; bound expressions
  execute once before overlap/default-row checks; sibling value/NULL ownership
  is exclusive and DEFAULT is the live complement. Metadata, dump literals and
  remap use key 0 for every value, never the list ordinal.
- **ATTACH** skips the scan only when `ConstraintProven` proves the bound.
  Compatibility matches columns by name and type, NOT NULL by mapped attribute
  and CHECK by name and cooked definition, never by physical slot.
- **Lock order.** ATTACH: the parent keeps SHARE UPDATE EXCLUSIVE; the
  candidate, its descendants and the incumbent DEFAULT take ACCESS EXCLUSIVE.
  DETACH: parent, incumbent DEFAULT, candidate, then descendants, all ACCESS
  EXCLUSIVE. Real waits run without `Engine.mu`.
- **Constraint origins.** CHECK/NOT NULL descriptors own exact `PartitionOrigin`
  metadata (CREATE and ATTACH populate it, DETACH removes the top contribution);
  catalog output never infers locality from a matching parent name.
- **Partitioned exclusions.** ATTACH clones a fresh inherited graph; DETACH
  makes crossing providers local; DROP follows inherited edges only. Coverage
  rules are in the [`index` README](../index/README.md).
- **Dump.** Output is byte-stable: the dump-event graph keeps its order and
  never iterates maps into output. Each local exclusion provider is declared
  once; inherited CHECKs and providers are not dumped.

## Testing

Package units cover pruning proofs and the shared search budget. Units that
build an engine stay in `internal/engine`, naming the code as `partition.X`,
and assert branch selection and unlocked provider callbacks without timing
thresholds. Run attach/detach overlay, lock-fence and LIST cancellation units
with `RACE=1`. Public SQL lives in
[reference/tables](../../../reference/tables/README.md#testing); add
`REFERENCE=1` for the oracle.

```sh
make check RUN='^TestPartitionPruning'
make check RUN='^Test(Partition|RangePartition|HashPartition|ListPartition)'
make check RUN='^TestReference(Range|Hash|List)?Partition'
```
