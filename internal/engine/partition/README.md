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
  validation and containment; `InsertState.Route` is shared by INSERT, COPY and
  UPDATE and walks from the query-named target to the accepting leaf.
  `MissingPartition` discloses key values only to an actor without RLS who may
  SELECT them. Routing retains the admitted catalog, layouts, `access.View`
  and environment; per-leaf insertion groups belong only to INSERT/COPY.
  Captured physical BEFORE/AFTER ROW INSERT applicability is independent of
  statement definitions. Only row definitions clone to leaves; logical statement
  targets remain the query-named relation.
- **Key recipes** (`routine.go`): `EvaluateRecipe` evaluates a key expression
  through `RecipeHost` (the root `statementHost`); `Scope` is the statement's
  partition slot in the root `StatementScope`.
- **DDL helpers** (`create.go`, `attach.go`, `detach.go`, `exclusion.go`,
  `constraint_drop.go`) and **admission** (`locks.go`): `ConsumerBoundary`
  rejects statements hierarchies do not accept; `Ancestors` returns ancestors
  root first, for the caller to lock before the leaf. Ordinary GiST/SP-GiST CREATE INDEX,
  including aliases identified by the captured catalog's native method authority,
  uses the existing partitioned-index propagation path in `index`.
  `StageConstraintDrop` marks only the successfully selected constraint ORIGINAL,
  after inherited/backing-index validation and constraint-driven recursive
  retirement. Recursive constraints and backing indexes are not SQL roots.
  PARTITION OF constructs its own relation descriptor and retains the child's
  adjusted creation persistence before row-type/constraint staging; it does not
  inherit logged/unlogged identity from the partitioned parent.
- **Proofs** (`proof.go`, `retained_checks.go`): `ConstraintProven` lets ATTACH
  skip its scan; `RetainedChecks.ForRow` picks CHECKs for hash-remap repair.
  RANGE/LIST concurrent-detach CHECK reconstruction selects the saved opclass's
  actual family operators, typed datums and binary-compatible input adapters;
  the ordinary CHECK binder owns cooking, dependencies and recursive propagation.
  The same conservative implication engine validates restored pending bounds.
- **Dump and replay** (`dump*.go`, `*_dump.go`, `replacement.go`, `replay.go`):
  SQL builders, `DumpEvents` (over the root's `dump.Resolver`), hash-remap
  repair (`RepairRemappedHash` returns moves without changing storage) and the
  Restore replay records the root leases on its `restoreReplay` slot.
- **Inquiry values** (`inquiry.go`): `TreeValues`, `AncestorValues` and
  `RootValue` build the `pg_partition_tree`, `pg_partition_ancestors` and
  `pg_partition_root` results from the statement's captured `catalog.Read`.
  The result descriptor must belong to that catalog. Index trees follow only
  index attachment edges; a childless partitioned relation is not a leaf.
  Canonical pending membership remains a dependency edge. Fresh table inquiry
  traversal omits pending branches and treats a pending child as its own root.
  Scan/routing eligibility alone may retain the edge from an older stable
  snapshot; `EffectiveChildren` never resurrects a fully removed edge.
  Incomplete-detach ALTER admission guards the target's immediate edge, not all
  descendants or ATTACH candidates. Rename and namespace transfer are separate
  utility paths and preserve membership identity.
  Descriptor validation enforces the single-pending/no-DEFAULT invariant from
  either side of the edge, including a stage replacing only the child while its
  parent's descriptor remains unchanged.

## Host and lock contract

The package holds no engine state and no mutex; the root owns `Engine.Mu`, lock
waits and publication.

- `Host` is `table.Host` plus `BindKeyExpression`, implemented by
  `tablecmds.TableHost`. Every method runs with `Engine.Mu` held, never takes
  or releases it and never waits.
- `BoundBinder` is self-locking: callers hold nothing, it takes `Engine.Mu` for
  one window, and bound evaluation follows unlocked.
- Under `Engine.Mu`: `RejectForeignKeys`, `StageCreate`, `BindKeys`,
  `StageIndexChildren`, `StageExclusions` and `CaptureInsertLocked` (whose
  snapshot callbacks never wait).
- With nothing held: `EvaluateBounds`, `ValidateBounds`, `CheckOverlap`,
  `CheckCoverage`, `CheckDefaultRows`, `CheckAttachTarget`,
  `ValidateAttachRows`, `AttachDescriptors`, `StageDetach`,
  `StageConstraintDrop`, the Restore replay cores (`CheckReplayTarget`,
  `Capture*Replay`, `Matches`) and the inquiry values, which check the
  context between steps.
- `AttachFrontier` waits only in the caller's `lockChild` callback, which runs
  with nothing held and locks and rereads each DEFAULT descendant.

## Query branch refutation

`CapturePruner` borrows the admitted source's immutable catalog, topology and
physical layouts; `CapturedPruner.Select` runs unlocked through exec's
`QueryBranchPruner` interface and emits captured ordinal intervals. Unsupported
provider, identity, collation or probe contracts keep the branch possible.
`NewBranchProof`/`Possible` own the same bounded feasibility search for Select
and metadata-only EXPLAIN; ordinal construction stays in Select. The captured
constraint_exclusion mode enables CHECK/validated NOT NULL facts for all tables
under on, inherited child branches (not the named parent or a standalone child)
under partition, and neither under off. Declarative membership proof remains
independent. EXPLAIN uses no sources or row counts, runs proof comparisons
unlocked and omits proof details for row-security/row-lock guarded sources.
The Select membership argument is an admitted value from the executor, not a
live setting read. When false, RANGE/LIST/DEFAULT/HASH ancestor membership
facts are omitted; physical CHECK/NOT NULL facts remain governed independently
by constraint_exclusion, and constant-false query proofs remain active.
Captured topology, lock manifests and residual evaluation do
not change when membership pruning is disabled.

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
`CatalogStage`: `schemaPartitionTableLocked` and `PendingPartitionDefinition`
(checks in diagnostic order; both in `tablecmds`), ATTACH and DETACH PARTITION
and the Restore replays ([`partitioncmds`](../partitioncmds/README.md)), and,
in core, lock admission (`partition_locks.go`), authorization and Restore trust
checks (`partition*ReplayAllowed`), storage-read fences and shared DML routing
capture. `partition_host.go` in
`tablecmds` holds `TableHost.BindKeyExpression` and the `PartitionBoundBinder`
adapter; `partition_info_runtime.go` holds the
`statementHost` inquiry methods, which check the retained catalog's owner.

## Invariants

- **Storage ownership.** Virtual roots and intermediates own descriptors only;
  physical leaves own storage. One write stage routes and validates all
  destinations, so a late failure publishes no earlier leaf rows. Scans capture
  all descendant prefixes at one frontier.
- **UPDATE movement.** The executor evaluates source generation before routing
  and validates the actual destination rather than the departing source's local
  constraints. Direct physical targets never route to siblings. Both effects use
  the existing canonical write stage, retaining captured deletion provenance and
  immutable moved-source markers through private and committed publication.
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
  Enforcement compatibility is independent of validity: a false parent permits
  a true child, not the reverse. Unenforced checks cannot prove a bound or skip
  the ordinary partition-bound scan. Matched attached CHECKs become inherited
  nonlocal facts while retaining their real identities and enforcement.
- **Lock order.** ATTACH: the parent keeps SHARE UPDATE EXCLUSIVE; the
  candidate, its descendants and the incumbent DEFAULT take ACCESS EXCLUSIVE.
  Ordinary DETACH uses parent, incumbent DEFAULT, candidate and descendants,
  all ACCESS EXCLUSIVE. Concurrent phase one uses parent/child SUE; finalization
  uses parent SUE then child/descendant AX. Real waits run without `Engine.Mu`.
- **Constraint origins.** CHECK/NOT NULL descriptors own exact `PartitionOrigin`
  metadata (CREATE and ATTACH populate it, DETACH removes the top contribution);
  catalog output never infers locality from a matching parent name.
  Pending publication retains those identities and index ancestry. Its exact
  predecessor/revision/root stamp permits only that one CHECK/pending transition
  across admitted-reader fences. Later revisions fail automatically; finalization
  clears this edge's proof, and replacement/import never carries it forward.
- **Partitioned exclusions.** ATTACH clones a fresh inherited graph; DETACH
  makes crossing providers local; DROP follows inherited edges only. Coverage
  rules are in the [`index` README](../index/README.md).
- **Dump.** Output is byte-stable: the dump-event graph keeps its order and
  never iterates maps into output. Each local exclusion provider is declared
  once; inherited CHECKs and providers are not dumped.

## Testing

Package units cover pruning proofs, the shared search budget and the inquiry
values. Units that
build an engine live in `internal/engine/partitioncmds` (partition and
inheritance DDL) or `internal/engine/core` (`pg_partition_*` functions),
naming the code as `partition.X`,
and assert branch selection and unlocked provider callbacks without timing
thresholds. Run attach/detach overlay, lock-fence and LIST cancellation units
with `RACE=1`. Public SQL lives in
[reference/partitions](../../../reference/partitions/README.md#testing); add
`REFERENCE=1` for the oracle.

```sh
make check RUN='^TestPartitionPruning'
make check RUN='^TestPartitionInfo'
make check RUN='^Test(Partition|RangePartition|HashPartition|ListPartition)'
make check RUN='^TestReference(Range|Hash|List)?Partition'
```
