# internal/engine/partitioncmds

The partition and inheritance statement executors
([`docs/ENGINE-SPLIT.md`](../../../docs/ENGINE-SPLIT.md) §3, L16): ALTER TABLE
... ATTACH PARTITION and DETACH PARTITION, the partition Restore replays and
the inheritance Restore repairs. The decisions are
[`partition`](../partition/README.md)'s and [`table`](../table/README.md)'s;
this package orchestrates them over core's runtime.

## Layering

It imports [`core`](../core/README.md), [`tablecmds`](../tablecmds/README.md)
(the IF EXISTS skip, `PendingPartitionDefinition` and `PartitionBoundBinder`
for ATTACH bounds, `TableHost`, the validation views, and the Restore
foreign-key capture and repair publication) and the decision packages, never
the root. By rule 6 it may also import `indexcmds`. Only the root composition
imports it.

Stays elsewhere: CREATE TABLE ... PARTITION OF, ALTER TABLE INHERIT and NO
INHERIT, and the partition key UNIQUE replay executor are `tablecmds`'s.
Routing, pruning, partition lock admission (`AcquirePartitionLock`,
`AcquirePartitionDescendants`, `ObservePartitionRelationLocked`), the Restore
trust checks (`PartitionConstraintReplayImport`, `partition*ReplayAllowed`)
and the partition parts of the DML path are core's.

## Entry points

- Dispatch (`core.Statements.ExecuteLocked`): `AttachPartitionLocked` and
  `DetachPartitionLocked` are lock-domain continuations: entered with
  `Engine.Mu` held and the parent admitted, they release it on every return.
  DEFAULT, candidate and descendant waits run unlocked on the statement's
  ticket in the order the [`partition` README](../partition/README.md#invariants)
  states; after each wait they recheck names, ownership and the locked
  relation under `Engine.Mu`, then stage and validate in one catalog stage.
- Restore hooks (the root's `restore_*.go`): the partition replays
  `BeginPartitionCheckReplay`, `BeginPartitionUniqueReplay` and
  `BeginPartitionUsingIndexReplay` record on the private import operation
  what the ordinary ALTER TABLE then executes;
  `CapturePartitionReplayTeardown` and `ValidatePartitionReplayTeardown`
  pair a generated DETACH with its DROP TABLE. The inheritance repairs are
  `RestoreCheck`, `PairRestoredGeneration`, `CaptureRestoredGeneration`,
  `CloseRestoredRecipesForData`, `RestoreRetiredConstraint`,
  `CaptureInheritedForeignKey`, `CaptureInheritedOwner` and
  `RestoreInheritedOwner`. They are entered with nothing held and take
  `Engine.Mu` only for their checks of live state; each admission accepts
  only a trusted private import.

## Testing

Tests are external (`package partitioncmds_test`) and run on a composed
engine: the root's `engine.New`, `Reset`, `Dump` and `Restore` and the
[`enginetest`](../enginetest/README.md) helpers. `export_test.go` exports the
internals they call; add a name there only when the test cannot assert
through SQL or the exported API. Public SQL lives in
[reference/partitions](../../../reference/partitions/README.md#testing) and
[reference/inheritance](../../../reference/inheritance/README.md#testing).

```sh
make check RUN='^TestPartition(Attach|Detach)'             # ATTACH and DETACH, overlays, fences, Restore replays
make check RUN='^Test(RangePartition|ListPartition|HashPartition|PartitionExclusion|PartitionInherited|PartitionHost)'  # strategies, publication, remap
make check RUN='^TestInheritance(Merge|Graph|Guards|Dependency|UsingIndex|Retained|Generation|Prospective|Maint(Candidate|Ordinals|History|Review))'  # inheritance graphs, maintenance
make check RUN='^Test(RestoreInherited|RestoreConstraintHistory|InheritanceForeignKey|CastDefinition(Restore|PlainGeneration))'  # inheritance Restore repairs
make check RACE=1 RUN='^TestPartition(Attach|Detach)(TransactionOverlay|LockFences|PublicationFences|Growth)'  # overlays, lock fences
```
