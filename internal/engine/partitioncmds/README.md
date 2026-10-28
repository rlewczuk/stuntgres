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
  CONCURRENTLY instead owns parent/child SUE in phase one, publishes the pending
  edge and bound CHECK irreversibly, retires those phase grants, then waits a
  frozen AX-conflicting parent-holder cohort without requesting parent AX.
  Its second phase reopens the exact identities with parent SUE and child/
  descendant AX and publishes shared teardown. Cancellation after the first
  publication leaves pending state, never a successful or background completion.
  FINALIZE uses parent SUE and child/descendant AX, waits older snapshots through
  canonical virtual completion, and stages ordinary transactional teardown.
  Explicit rollback/savepoint rollback therefore preserves committed pending state.
  Between phases, saved refs—not old lexical names—select the original objects.
  Legal pending-child rename/schema transfer survives reopen; an old name reused
  by a replacement never redirects the operation. Pending ATPrepCmd alterations
  (including OWNER and nested ATTACH/DETACH) are refused on that immediate target;
  the separate rename/schema paths keep their ordinary authority checks.
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
  The owner-pair entry also covers fresh ordinary permanent tables without
  inheritance: administrator creation followed by the exact captured transfer
  preserves owners lacking schema CREATE. Creator, target reference and epoch,
  recipient role and private-import authority are checked without publishing
  anything; the ordinary ALTER executor performs the transfer.
  Partitioned roots and partition members retain their named-owner constructor
  replay, not this ordinary-table pair. A missing transfer remains ordinary SQL
  and gives no private authority.
  `ImportPendingDetaches` is the final isolated Restore hook: it validates all
  logical records and executable qualifications before one native pending-edge
  stage. Only the genuine validated bootstrap import with captured parent/child
  owners may use it; no public concurrent SQL or transient admission proof is replayed.

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
