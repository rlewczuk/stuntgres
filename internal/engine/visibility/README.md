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
- **The private deletion overlay:** `PrivateDeletes` with `DeleteCandidate`
  rows, and `LiveVersion`, the version-liveness rule over a frontier, end
  markers and the overlay.
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
  `transactionOwner.Mu`; this package takes no part in that ordering.

## What stays in the engine

`transactionOwner` (the per-session state machine and its CHAIN and
characteristic rules), savepoints, publication of the catalog journal (the
journal itself is `session.Journal`), prepared transactions (2PC),
`StatementScope`, write staging and `mergeDeletions`.
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
(`Isolation`, `stableIsolation`) are engine types.

## Testing

Package seam tests:

```sh
make check RACE=1 RUN='^Test(Registry|NextFullXID|LiveVersion|SafeSnapshot|View|SerializableGraph|SerializablePrecommit)'
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
