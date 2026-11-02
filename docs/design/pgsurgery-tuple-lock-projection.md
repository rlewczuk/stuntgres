# Target-local tuple ownership after forced repair

## Status and boundary

This describes the implemented target-local tuple admission and raw-header
projection contract for PG18-EXT-PGSURGERY-FREEZE. Root's tests20 observed the
four corrected Concurrency leaves on pinned PostgreSQL18.6; tests21 recorded
native lock-only xmax0 instead of the actual assigned owner XID. Root removed
the discovery guard and authorized production implementation. The implementation
and new native units still require root-run verification; this document makes
no passing native, race, review or integration claim.

Repair changes only represented exact-version tuple-lock header projection and
genuinely later tuple admission. It does not end a ticket, release an existing
request, retire a transaction, touch relation/advisory ownership, or reset the
manager/database epoch. No physical page or WAL fidelity is claimed.

Root's tests24 subsequently observed all eight producer-recheck leaves on pinned
PostgreSQL18.6; tests25 recorded the native pretrigger successor's missing actual
owner xmax. Root authorized the selected producer boundaries below. A genuine
successor discovered during Stage readmission invalidates the old proposal: it
never becomes an index-stale retry, fabricated serialization failure, stale
success, or blanket statement restart. Native/race proof remains root-owned and
unexercised by the implementation agent.

## Release evidence and independent expectations

[REL_18_6 heap_surgery.c, heap_force_common](https://raw.githubusercontent.com/postgres/postgres/REL_18_6/contrib/pg_surgery/heap_surgery.c)
opens the relation with RowExclusiveLock, takes a buffer cleanup lock, and resets
only each selected normal tuple's self-link, xmin, xmax and visibility/lock bits.
It does not perform ordinary tuple arbitration or end the old transaction.
[REL_18_6 pgrowlocks.c](https://raw.githubusercontent.com/postgres/postgres/REL_18_6/contrib/pgrowlocks/pgrowlocks.c)
scans actual visible physical tuples, reads their current headers and reports
only HeapTupleSatisfiesUpdate's BeingModified result. Old transaction activity
alone cannot recreate a cleared header. The
[PostgreSQL 18 surgery manual](https://www.postgresql.org/docs/18/pgsurgery.html)
warns that this deliberately unsafe operation can break normal invariants.

Independent expected transitions, observed by root's pinned PostgreSQL18.6 run:

1. A committed target in `fs_part.heap` and an unrelated sentinel in a separate
   canonical heap relation, `fs_part.sentinel`, have real captured TIDs. A live
   owner acquires FOR UPDATE on both, an unrelated relation AX lock and a real
   transaction advisory lock. Both tuple headers expose that owner's actual
   assigned XID; installed pgrowlocks returns singleton For Update memberships
   with matching locker/XID/PID and multi=false. Every tuple lookup includes its
   owning relation: different heaps can legitimately assign identical TID text.
2. An actual UPDATE of the sentinel is observed waiting on the original owner
   before surgery. PostgreSQL uses the exact transactionid ShareLock/blocker
   witness; native uses the existing actual mutation-worker stack observer.
3. Repair only the target. It retains its values and TID, exposes xmin2/xmax0,
   and disappears from pgrowlocks even though the owner still runs. Sentinel
   header/membership, relation ownership, advisory ownership and the observed
   waiter remain unchanged. NOWAIT/try-lock probes still reject unrelated work.
4. Execute a statement held since before repair in a different transaction:
   FOR UPDATE NOWAIT succeeds on the target, or an actual UPDATE/DELETE affects
   exactly one target without waiting on the cleared owner. A 10ms lock_timeout
   bounds an erroneous actual mutation wait; elapsed time is not an admission
   witness. The newly assigned transaction, not the old owner, becomes the sole
   target pgrowlocks member: For Update, No Key Update, or Update respectively.
   During uncommitted mutation another reader still sees the original frozen
   target with the new raw xmax. UPDATE creates a distinct genuine successor
   with its own xmin and xmax0; DELETE removes the target only on commit.
5. Commit the new owner while the old owner remains active. The old target lock
   does not reappear. Unrelated probes still fail and the same sentinel UPDATE
   remains observed waiting. Only rollback of the original owner lets it finish
   exactly once. Relation/advisory/tuple probes then succeed; final visible bags
   contain A/B2, A2/B2, or only B2 for the three respective leaves.
6. The inherited-successor leaf admits a new actual KEY SHARE owner after repair.
   Two compatible non-key UPDATEs commit in turn while that owner stays active.
   Each successor has a new real TID, its updating transaction's xmin (not2),
   and the KEY SHARE owner's inherited singleton xmax/pgrowlocks membership.
   An independent held FOR UPDATE NOWAIT rejects both successors while that
   inherited owner lives. Ending only that owner permits acquisition of the
   final successor even though the original cleared owner remains active and
   still blocks the unrelated sentinel. Only ending the original owner admits
   the sentinel; the final visible bag is exactly A3/B.

The complete pgrowlocks metadata is TID, XID, BOOL, _XID, _TEXT, _INT4 with
released OUT names locked_row, locker, multi, xids, modes, pids, including on
empty results. Target NOWAIT row failure is ERROR/ERROR 55P03, `could not obtain
lock on row in relation "heap"`; the separate sentinel probe names `"sentinel"`.
Unrelated relation failure retains the already observed qualified spelling
`could not obtain lock on relation "fs_part.unrelated"`.
Every other portable diagnostic field is independently expected empty; the
fixture compares the full diagnostic. Generated TID/XID/PID values are related
to actual public capture, never pinned across backends.

### Page-independent waiter schedule

The first three leaves start a real UPDATE waiter against `fs_part.sentinel`,
not another tuple on the repaired target's page. The old owner locks A and B
with separate statements in the same transaction; the AX lock remains on a
third relation, `fs_part.unrelated`, so it cannot prevent the sentinel UPDATE
from reaching actual tuple arbitration. The exact transactionid wait must be
observed before repair and again after repair, after target admission, and after
the new target owner's commit. Only rollback of the old owner admits B2 once.
Each boundary also checks the sentinel's original payload/TID/xmin, old-owner
raw xmax and all six uncast pgrowlocks output fields, as well as AX/advisory and
tuple NOWAIT rejection. Final values and empty pgrowlocks results are asserted
on both heaps. Target operations remain prepared before repair.

This separation removes a physical dependency irrelevant to the intended tuple
ownership proof: surgery takes a buffer cleanup lock on the target page, while
a blocked UPDATE can retain a buffer pin. With A and B on that page, waiting
for B can prevent repair itself from reaching the header reset. Moving B to a
different relation rules out that same-buffer dependency without weakening the
real waiter, target bypass, or unrelated ownership requirements. No sleeps,
deadline-based success, context refresh or altered timeout is used.

Root's tests15 observed the original first three leaves fail after about30s
with `operation completed before required transaction lock wait`; the
inherited-successor leaf passed. The fixture's `LockOperation` applies its
30-second Liveness hang guard, and its pending observer reports completion at
any re-observation boundary. **[INFERENCE]** The shared-page pin/cleanup-lock
dependency explains repair resuming only once that worker is canceled; tests15
did not capture a server buffer-pin wait or distinguish the failing invocation
of the repeated observer. It is not evidence that unrelated ownership should
be released. Root's corrected separate-sentinel run supplied the subsequent
oracle observations and explicit production authorization.

The inherited-successor leaf has no pre-repair pending UPDATE and retains its
existing same-heap sentinel and two-successor schedule; only its shared
pgrowlocks helper now takes the explicit owning relation.

## Canonical representation and resource identity

`storage.HeapHeaderRoot` remains the immutable sparse exact-version authority.
`TupleLockVersion, TupleLockStamp` are both zero for ordinary RowID arbitration.
Repair assigns only the target's own actual VersionID and fresh repair stamp.
Re-repair replaces only that target pair. `ReplaceHeapHeaders` transports the
predecessor's real pair to an accepted UPDATE successor, including a successor
with its own RepairStamp0 and sequential successors within one accepted batch.
End/commit and immutable maintenance transport retain the pair; genuine fresh
import/retirement clears it. RowVersion has no extra anchor fields.

`lock.TupleResource(ref,row,anchor,stamp)` validates and packs the pair. The
ordinary zero-pair result is exactly the existing ObjectRef+RowID key. A distinct
repaired discriminator uses the existing Row slot for anchorVersionID and
High/Low for the full64-bit stamp; Resource size and ordinary grant allocation
remain unchanged. ObjectRef still carries the actual relation/domain/epoch.
`exec.TupleResource` is the sole exact-header-to-resource adapter shared by
core admission, physical inspection and maintenance.

## Admission and accepted physical events

Query and mutation row acquisition receive the actual RowVersion, not just
RowID. `core.acquireTuple` captures the exact current header/incarnation under
Engine.Mu, releases metadata locks, admits through the existing manager request,
then revalidates. If repair changed the key while waiting, it admits the new
key outside metadata locks without destroying the old genuine request/grant.
NOWAIT/SKIP policy, SQL deadlines, replacement-read release and ordinary ticket,
deadlock, savepoint and cleanup ownership remain the established paths.

`LatestQueryRow` uses the exact-version mutation successor reader, including
Additional and actual private history. Only genuine Header.Successor links can
select another version: a same-RowID visible sibling is never a replacement.
Stable isolation retains its canonical serialization checks and compatible
KEY SHARE snapshot-value exception.

The statement remembers repaired admission keys sparsely; ordinary admissions
need no side map. `lockTupleStage` inspects the actual admitted grants outside
metadata locks, takes Owner.Mu then Engine.Mu, and compares every current key
with its admitted key. Changed keys readmit outside both mutexes and retry.
The final comparison and replacement/deletion publication share that Engine.Mu
critical section. The predecessor header sampled there supplies genuine
full-XID/end/history authority, and successor publication inherits its pair.

Every MultiEvent carries the actually admitted Resource and a separate physical
VersionID. Stage queues that resource before unlocking; flushing does not derive
a newer key. Query rechecks similarly retain their admitted event key. No repair
routine calls the manager or resets its MultiXact history, queues or tickets.
Relation-only FK grants use the zero RowVersion; row FK grants use the same
exact admission path. Replacement grant inspectors consume complete Resource
values rather than synthesizing RowID-only keys.

The immediate DML API is `PhysicalRowLock(ref,row,lock.Mode)`: it can publish
only a genuine lock-only effect on the caller's already-admitted resource.
Publishing that effect never rederives a newer header key or waits again.
UPDATE, conflict UPDATE, RI and MERGE proposals do not publish an updating
member with successor0 before acceptance. Only the final accepted replacement
or deletion queues its actual updater event; replacement supplies its real
successor. A failed tuple proposal consequently cannot append another updater
to an old version a peer already updated. Genuine earlier prelocks and committed
updater/raw history remain unchanged, including after retry or predicate rejection.
Pre-Stage constraint/key/view rejection does not invent a physical allocation
or an updating header; genuine already-allocated failed versions retain their
existing disposal/history path.


## Selected producer recheck boundaries

`ReplacementStageResult` separates `IndexStale` from `RecheckIndex`; the latter
names the first changed/nonlive predecessor in an entirely unaccepted window.
The final exact-key and canonical-version checks share the acceptance Engine.Mu
critical section. `ReplaceRows` consumes index staleness internally and returns
only the distinct internal tuple-recheck control plus that index to its producer.
DELETE preserves its original captured source authority while separately
checking the expected current version. No unchecked RowID retarget is admitted.

[REL_18_6 nodeModifyTable.c](https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/backend/executor/nodeModifyTable.c)
supplies the selected boundaries. Initial UPDATE projection precedes genuine
BEFORE prelocking; ordinary logical grant admission retains its established
grant-before-demand boundary. External initial EPQ rechecks the predicate and
targetlist before running BEFORE once on the latest row. Late UPDATE rechecks only its selected
predicate/targetlist below the already-completed BEFORE prologue: volatile RHS
can run again, completed BEFORE side effects remain, BEFORE does not run again,
and its discarded NEW edits do not overwrite refreshed RHS. DELETE/USING retry
their predicate below completed BEFORE. Only actual acceptance advances AFTER,
transition sets, affected count and RETURNING. Routed UPDATE also retains its
completed source BEFORE DELETE when source acceptance readmits.
Each eligible FROM pair demands its source targetlist once before target
deduplication, including losing pairs. An already accepted own winner is not an
external EPQ event: its genuine current physical version is the acceptance
predecessor, while frozen-source values remain the projection input. Repair of
the same VersionID changes admission but does not replay predicate/RHS/BEFORE.

Same-version BEFORE UPDATE prelocking is real metadata-driven lock-only work,
with key-column targets selecting the existing ForUpdate/ForNoKeyUpdate mode.
It is not synthesized by raw projection. The normal physical action/successor
path then inherits actual own pretrigger and compatible external memberships.
Trigger-free action-only successors retain their distinct ordinary behavior.

ON CONFLICT retains the existing unique-discovery loop below completed
VALUES/EXCLUDED/BEFORE INSERT. Its late UPDATE recheck reuses that proposal and
reevaluates selected WHERE/SET without replaying a completed UPDATE BEFORE.
RI uses its original generated-command snapshot and ordinary producer boundary;
stable isolation retains its canonical crosscheck/40001 rather than refreshing
the transaction snapshot.

MERGE re-enters actual ON/class/WHEN/action projection and prologue for only the
changed source occurrence. Its existing source-completion and explicit
`AssembleMergeRows`/`StageMergeRows` boundary remains intact. The existing
execution-private MergeRows allocation embeds a noncopyable producer context:
owned materialized values/presence/provenance, immutable projected-version/raw
captures and the same statement State. No cursor, borrowed row scratch,
prepared authority or callback is retained. Terminal stage/error/cancellation
clears that context. Indexed failed-window replay discards only the failed
unaccepted occurrence, preserves accepted prefixes and unrelated pending
RHS/RETURNING/effects, and rebases real contiguous action/payload segments.
The ordinary deferred batched path acquires no replay payload copies; only rare
candidate replay splices its affected segment and combines pending unique proof.
Initial changed-version MERGE follow-chain admission uses the full all-arm
strong-mode union before reclassification/prologue, even when the new tuple
shares an ordinary resource key. A strong replay similarly upgrades the actual
grant rather than treating equal resource identity as equal mode. A deleted
BY SOURCE target retains its genuine absent/NULL-source NOT MATCHED opportunity;
that opportunity can still INSERT DEFAULT VALUES or raise its real constraints.
Inherited replacements may prove candidate authority through exact immutable
predecessor identities back to the captured prefix/additional root, never by
retargeting an arbitrary same-RowID head or later unrelated insertion.
Published heads clear private history and transport committed predecessors to
Relation.Retained. Their authority proof therefore captures the immutable
current header root under the same storage fence and follows exact increasing
Successor links from a captured physical candidate to the requested version.
A reset/self link after repair is not ancestry, and neither a same-RowID sibling
nor an unrelated later insertion acquires candidate authority from a row grant.

The native late-Stage UPDATE proof uses a real advisory owner, repair and peer
publication, then observes the actual repair-key tuple request before commit.
It asserts canonical predecessor/storage, selected RHS, one completed BEFORE,
success-only AFTER/count/RETURNING and immutable source capture. This is not a
claim that surgery can pass PostgreSQL's old-tuple buffer pin while paused in
BEFORE. The separate native middle-candidate MERGE proof preserves other
pending sequence-derived RHS/RETURNING cells across an indexed failed batch.
The advisory transaction wait is observed on the actual transaction ticket,
not the independent session-advisory ticket. Queue observation also detects
premature worker completion. Failure cleanup cancels, joins the real worker,
then releases producer/frame resources and finalizes the statement/operation
before session shutdown; a failed assertion cannot strand Owner.Admission.
The Stage-only fixture uses a plain heap: an immediate unique index would add
its independent, legitimate unchanged-key claim after BEFORE and before tuple
Stage admission. Removing that unrelated prerequisite from this fixture does
not bypass production key checks or replace the exact tuple-request witness.


## Detached raw xmax presentation

`lock.TupleMaintenance` carries detached Present/Xmax/Multi facts. One
manager-private exact-MultiTuple helper feeds both maintenance and pgrowlocks
before inspector eligibility filtering. Retained aborted/ended raw xmax is
independent of effective ownership: pgrowlocks can be empty while raw xmax
remains. Capture never assigns identity, settles requests, invokes callbacks,
or exposes manager-owned member backing.

`core.CaptureTupleXmax` captures exact storage/header keys under Engine.Mu,
releases it before the manager batch, then readmits the same storage/header
publication. A changed publication retries the capture. Captured raw facts
override canonical header xmax only for presentation; raw XID/MultiXact scalars
never enter DeleteFullXID, mutation candidates, private history, Stage, or end/
commit publication.

Executable xmax demand creates immutable source/frame-owned maps and detached
target/OLD scalars. ctid/xmin/tableoid-only and ordinary scans do not allocate a
raw batch. Query frames retain their immutable publication; genuine row-lock
events publish a new copy before EPQ/current-row projection. RETURNING captures
the actual accepted target/OLD header after physical events have flushed.
`EvalSystemAttribute` only reads captured values and never calls a manager.
Source.Close releases its map; detached completed frames retain their scalars.
Mutation qualification retains its source capture, separately from current
action and RETURNING projection.

Pgrowlocks and pgvisibility derive keys from the exact immutable header root
captured with physical membership and readmit that publication after manager
capture. Repair consequently cannot resurrect an old raw record merely because
its owner remains active. Later genuine owners and inherited successor KEY SHARE
membership use the same anchor and existing MultiXact algorithm.

## Root proof handoff

Shared Concurrency expectations remain in
`reference/extensions/pgsurgery_freeze_tuple_locks_test.go`; no discovery guard
or weakened expected value remains. Native real-owner units cover actual wait
readmission, Stage repair/readmission, retained unrelated pending-request
identity, actual event-key attribution, successor inheritance, detached raw
lock-only capture and pure old-frame lifetime. Lower-owner units independently
cover representation size, zero-allocation ordinary keys, exact history and
aborted/ended raw facts.

Root-selected commands (external900-second deadline each):

```sh
make check RUN='^TestPgsurgeryFreeze(Tuple(Readmission|Projection|Resource|History|Anchor|PretriggerInheritance)|ProducerStageRecheck|MergeStageRecheck)$'
make check RACE=1 RUN='^TestPgsurgeryFreeze(Tuple(Readmission|Projection|Resource|History|Anchor|PretriggerInheritance)|ProducerStageRecheck|MergeStageRecheck)$'
make check RUN='^TestReferencePgsurgeryFreezeConcurrency$'
make check REFERENCE=1 RUN='^TestReferencePgsurgeryFreezeConcurrency$'
make check RACE=1 RUN='^TestReferencePgsurgeryFreezeConcurrency$'
make check RUN='^TestReferencePgsurgeryFreezeProducerRecheck$'
make check REFERENCE=1 RUN='^TestReferencePgsurgeryFreezeProducerRecheck$'
make check RACE=1 RUN='^TestReferencePgsurgeryFreezeProducerRecheck$'
make check RUN='^TestReferenceRowLock(ReadCommitted|RepeatableRead|KeyControl|Independence|Lifetime)$'
make check REFERENCE=1 RUN='^TestReferenceRowLock(ReadCommitted|RepeatableRead|KeyControl|Independence|Lifetime)$'
make check RUN='^TestReferencePgrowlocks(ModesAndOwners|RawMultiAndPID|VisibilityAndVersions|DynamicSnapshot|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferencePgrowlocks(ModesAndOwners|RawMultiAndPID|VisibilityAndVersions|DynamicSnapshot|FeatureSmoke)$'
```

Feature smoke is the unchanged four `postrepair-` Concurrency leaves: exact raw
header/member metadata and new target-owner bypass, old unrelated pending waiter
and AX/advisory ownership until original-owner end, and inherited KEY SHARE
across two real successors. Root's native/reference/race observations, not this
contract, determine verification readiness.
