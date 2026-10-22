# internal/engine/visibility

Transaction identity, commit order, commit timestamps, snapshot data views
and the serializable (SSI) tracker. The package imports only `catalog`,
`ids`, `lock` (for `*lock.Ticket` identity), `storage`, `sqlstate` and the
standard library. It never imports the root engine. Layering is in
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).

## What it owns

- **`Registry`**, held by value as `Engine.visibility` and never replaced
  whole. It contains:
  - the commit frontier (`CommitSeq`);
  - writer ordering identities (visibility XIDs) and per-incarnation
    `Entry` records;
  - the canonical full-XID allocator with its `History` and `LowIDs`;
  - the TxRef diagnostic counter;
  - commit timestamps;
  - the serializable `Tracker`.

  `NewRegistry(track)` starts canonical assignment after the special XIDs.
  Its maps are allocated on first use, so a zero `Engine{}` still works.
- **Snapshot data:** `View`, the handle a statement reads through, and
  `Data`, its immutable payload. `Data` holds the frontier, the catalog
  read, captured storage `Generation`s, end roots, own writes and the
  private deletion overlay. `Root` is the repeatable-read retention of one
  relation.
- **The ordinary heap operation authority:** zero-state concrete
  `HeapOperations` owns the single `LiveVersion` implementation over real
  `storage.RowVersion`, frontier, end roots and `PrivateDeletes` overlays.
  `ResolveTableAccess` selects it only for the finite Heap capability.
  `ResolveHeapAccess` binds from an actual captured method descriptor and its
  installed, provider-validated handler3. It does not infer registration from
  a signature or marker. Default `visibility.LiveVersion` remains the legitimate
  ordinary-heap API and forwards to this same implementation.
  Source capture/reselection and catalog replacement bind builtin heap2 once
  outside their row loops, then pass concrete operations to `exec.SelectQuerySource`.
  This does not add custom TABLE selection, storage types or callback dispatch.
- **Repeatable-read fetch conflicts:** `RepeatableConflict` (`repeatable.go`),
  the 40001 "concurrent update"/"concurrent delete" rule for a row fetched
  under a stable snapshot, over its frontier and the end markers. The DML
  latest-row readers and the FK action admission call it.
- **Write-set merges** (`write_set.go`, 13g): `MergePrivateRows`,
  `MergeDeletions` and `PrepareOwnWrites` combine a statement's staged
  private rows and deletions with the transaction's earlier ones. They never
  mutate either input or write into a prefix an earlier view retains;
  insert-only merges append, and `PrepareOwnWrites` sizes the vector headers
  and capacity before the final preflight writes the tails.
- **SSI evidence:** `SerializableTx` nodes, `SerializableWrite` events and
  rw-conflict edges, finalization order, safe-snapshot candidates
  (`SafeSnapshotBlocker`) and SIRead holders (`PredicateLockHolder`, for
  `pg_locks`).

## Contract

- **One lock authority.** The registry has no mutex. The caller holds
  `Engine.mu` for every read and write, including the getters, so commit
  ordering and publication stay atomic with the catalog and storage.
  Mutating methods carry the `Locked` suffix or say that the caller holds
  `Engine.mu`.
- **Lock order.** Registry methods never take another mutex, never block
  and never call back into the engine. They touch only ticket atomics
  (`MirrorFullXID`, `MirrorCompletion`, `OwnershipEnded`,
  `AssignedFullXID`), never `lock.Manager` state, so the manager mutex is
  never taken under `Engine.mu`. The full order is in
  [lock/README.md](../lock/README.md#mutex-ownership-and-lock-order).
- **Fences stay in the engine.** Registry methods never read the catalog.
  Engine wrappers check the `TxRef` domain and catalog epoch first:
  `visibilityWriteLocked`, `visibilitySnapshotLocked`,
  `transactionAgeLocked`, `assignTransactionXIDLocked` and
  `enrollSerializableLocked`.
- **Counters.** The counters are private. `CommitSeq` and
  `AdvanceCommitSeqLocked` (one step) serve publication. Each publishing
  caller checks exhaustion first, with its own diagnostic, before entering
  its no-failure region. `SetCommitSeqLocked`, `SetLastAssignedXIDLocked`
  and `SetDiagnosticTxLocked` are seeding seams for tests only.
- **XID assignment.** Full XIDs are monotonic. Low 32-bit values below 3
  are skipped (`NextFullXID`). An assignment never reuses the low 32 bits
  of a transaction that is still in progress; the `LowIDs` index makes that
  check constant-time, including the gap between publication and manager
  release. An exact atomic full-XID mirror on the ticket keeps an old status
  from following a later index phase with the same low bits. Sparse retained
  status and optional timestamp reservations index the full identity. With
  tracking off, no timestamp map is allocated.
- **Snapshots and retention.** Repeatable read and serializable retain every
  immutable relation root and end root (`Root`) before analysis waits. Read
  committed and read uncommitted views stay fresh at grant time. Retention
  holds roots by pointer and never copies row values. A `View` handle has
  its own release authority: `Retain` returns an independent handle,
  `Release` ends only its own handle, and a forged or copied handle can
  neither retain, release nor read (`Data` panics; `Load` returns the raw
  pointer).
- **VACUUM retention and freezing.** The engine excludes its own canonical
  maintenance owner and conservatively gates current dead-head retirement on
  any other same-epoch registered snapshot/builder/writer/live ticket. It
  rechecks that gate before installation. Detached owned roots survive current
  compaction independently; old headers, values and end maps are never mutated.
  Freezing cutoff instead uses next canonical FullXID clamped by actual
  in-progress assigned FullXIDs. CommitSeq, writer order and low32 XIDs are not
  interchangeable ordering domains; special low32 values are skipped by the
  existing canonical allocator. Ticket liveness reads atomics only, not manager
  metadata under Engine.mu.
  The same safe cutoff initializes empty relation incarnations: an already
  assigned older READ COMMITTED publisher may later see and insert into a newly
  created relation. Ordinary/prepared and rewrite publication additionally
  retain each real full-width birth in its relation bound; no low32 comparison
  or read-only-BEGIN inference substitutes for canonical assignment.
  Database calculation also starts from that cutoff before minimizing installed
  relation bounds: future peer-created relations must already be protected
  during a database-only pass. Exhaustion and unknown relation bounds retain
  no-publication behavior; no repair pass is needed to justify the earlier seed.
- **Runtime-named lock observations.** Before the first data capture, the
  engine checks the outer analyzed dependencies and executable stored-view
  source manifests recursively for the installed pgrowlocks provider.
  Those statements retain all-relation immutable prefixes in `Data.DynamicRoots`
  at the same frontier as their ordinary sources. A later dynamic target uses
  those prefixes locally, without replacing the outer view, resolving targets
  early, merging stored routine ACL manifests, or weakening epoch, storage,
  object and lexical admission fences. Prepared and nested stored routes share
  this trigger; unrelated statements do not retain all-relation roots.
- **SSI.** Edge failure follows PostgreSQL REL_18_0
  `OnConflict_CheckForSerializationFailure`; finalized nodes are immune, and
  prepared finalization orders conflicts without resolving the committed
  outcome. Retained events hold identities, never row values. A tracker cohort is
  cleared once it has no live participant.
- **Safe snapshots.** `CaptureSerializableLocked` gives a DEFERRABLE
  read-only node the overlapping read-write writers it must wait out.
  Resolution closes the node's ready channel exactly once: when the last
  writer commits or aborts, on `EndTransactionLocked`, on `FenceLocked`, or
  on `AbandonSafeSnapshotLocked`. The engine's wait loop
  (`acquireSerializableSnapshot`) calls `SafeSnapshotWait` and
  `SafeSnapshotOutcome` and waits with `Engine.mu` released. That loop also
  drops and retakes `replacementMu` while its caller holds
  `session.TransactionOwner.Mu`; this package takes no part in that ordering.

## What stays in the engine

The transaction owner's orchestration (the per-session state machine's
transitions and its CHAIN and characteristic rules; the owner state itself
is `session.TransactionOwner`), savepoints, publication of the catalog journal (the
journal itself is `session.Journal`), prepared transactions (2PC),
`StatementScope` and write staging.
Also every entry point that combines visibility with the catalog, storage,
locks or sessions:

- `registerTransactionLocks`, the fence wrappers and `endStatementVisibility`;
- `waitOlderIndexSnapshots`;
- `AcquireDataView` and `captureDataViews*` (snapshot construction from
  private storage);
- `snapshotDataPhase` and `acquireSerializableSnapshot`;
- the SSI eligibility and authority checks (`serializableReadLocked`,
  `serializableWriteLocked`, `serializableEligibleLocked`) and rollback
  reconciliation;
- `endSerializableLocked` and `fenceSerializableLocked`, which also clear
  index build waits;
- the `pg_blocking_pids` and `pg_locks` projections.

The SQL functions (`age`, `pg_current_xact_id*`, `pg_xact_status`, the
commit-timestamp functions) stay in the engine. The publication sites
interleave `commitSeq` advances with catalog and storage installation and
stay inline (`catalog.go`, `transaction_publication.go`). Isolation levels
are `session.Isolation` (`Stable` reports a transaction snapshot).

## Testing

Package seam tests:

```sh
make check RACE=1 RUN='^Test(Registry|NextFullXID|LiveVersion|SafeSnapshot|View|SerializableGraph|SerializablePrecommit)'
make check RUN='^TestVacuum(SnapshotSafety|FreezeProvenance|ImportHorizons)$'
make check RUN='^TestVacuum(SnapshotSafety|PublicationFences)$' RACE=1
make check RUN='^TestWriteSetMerges'
```

Registered heap authority and real source-selection regression:

```sh
make check RUN='^TestAccessMethodHeapOperations$'
make check RUN='^TestAccessMethodHeapOperations$' RACE=1
make check RUN='^TestLiveVersionOverlayAndEnds$'
```

Engine tests exercise the registry through sessions, publication and SQL
functions. Use external 900-second deadlines. These actual-seam units seed
wrap, horizon and collision state:

```sh
make check RUN='^TestTransactionIdentity(AgeArithmetic|Allocation|History|Completion|MultiHistory|GrantActions|Savepoint|Binding)$'
make check RUN='^TestTransactionIdentity(MultiHistory|GrantActions|Savepoint|Completion)$' RACE=1
make check RUN='^TestCommitTime(Assignment|Publication|Replacement|Expression)$'
make check RACE=1 RUN='^Test(Serializable|PreparedTransaction|TransactionIsolation|TransactionVisibility|ConcurrentIndex|BlockingPids|PGLocks)'
```

Reference selections for isolation, SSI, safe snapshots, commit timestamps,
xid functions, 2PC and concurrent DML visibility (run each with and without
`REFERENCE=1`):

```sh
make check RUN='^TestReference(TransactionIsolation|Serializable|CommitTime|TransactionIdentity|PreparedTransaction|TransactionVisibility|TransactionControlsVisibility|ConcurrentInserts|DeleteCoreVisibility|UpdateCoreVisibility|InsertConflictSnapshotVisibility|RowLock(ReadCommitted|RepeatableRead)|BlockingPidsSafeSnapshot)'
```
