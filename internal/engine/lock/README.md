# internal/engine/lock

The heavyweight lock manager. It sits at layer L2 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture) and
imports only `ids`, `sqlstate` and the standard library. It never imports the
engine.

## What it owns

`Manager` holds all heavyweight lock state for one engine:

- **Tickets.** One `Ticket` per registered transaction (`Register`) and one
  persistent ticket per session for session-level advisory locks
  (`RegisterSession`). A ticket journals every resource it holds or waits on.
  Its transaction ID, phase and completion state are atomic mirrors that the
  engine publishes (`MirrorFullXID`, `MirrorCompletion`). A prepared ticket is
  detached from its session (`DetachPrepared`).
- **Resources and modes.** A `Resource` is a relation or type (embedded
  `ids.ObjectRef`), a logical row (`Row`), an interned unique key
  (`PinUniqueKey`), a transaction's completion (`CompletionOf`) or an advisory
  key (`Kind`, `High`, `Low`). `Mode` uses PostgreSQL's table and row lock
  names; `Conflicts` is the conflict table. Advisory grants are counted
  (`Hold.Shared`, `Hold.Exclusive`).
- **Queues and waits.** `StartAcquire` grants at once or returns the queued
  `Request`, which the caller waits on (`Wait`) after releasing its own
  locks. `Policy` carries NOWAIT, the lock timeout, the statement deadline,
  the caller's source context and the session request's provenance.
  `WaitError` decides a wait's outcome from absolute SQL deadlines, not from
  callback scheduling. `TimeoutSlot` lets the session observe a pending SQL
  timeout without the manager mutex.
- **Deadlock detection.** Every new wait runs detection over the live queues.
  Soft cycles are resolved by reordering queues when a proposal removes every
  cycle; otherwise one request gets `40P01` with an exact witness.
- **Savepoints and unique-key candidates.** `Checkpoint`/`Restore` and the
  unique-key variants restore held contributions, never pending requests.
  `BeginUniqueDelta`, `RestoreUniqueDelta` and `EndUniqueDelta` journal the
  claims of one conflict-update candidate.
- **MultiXact history.** The manager owns immutable MultiXact records, the
  nonzero modular counter and the association with exact row versions
  (`PhysicalEvents`, `MultiXactMembers`, `MultiAge`). Arbitration still uses
  `ObjectRef` and `RowID`; history adds `VersionID`, so a successor copies the
  predecessor's original raw xmax ID and full member set, including ended or
  mixed-mode members. A batch is prepared completely before any counter or
  history change is published. Member-set reuse is a transaction-local LRU of
  at most 256 IDs, filled by creation and by member inquiry, never universal
  interning. Same-XID strength merges keep action versus lock kind.
  Savepoints restore status contributions separately from grant modes; grant
  intersection only bounds a stale checkpoint and never invents provenance.
- **Inquiries.** `BlockingPids` (for `pg_blocking_pids`), `PGLocks` (the
  manager's half of `pg_locks`, as owned `Record` values) and the grant queries
  the engine uses to validate its own admission (`Holds`, `MissingRowGrant`,
  `ReplacementGrants`, `ValidSession`, `TransactionTicket`, `IndexWriters`).

## Mutex ownership and lock order

`Manager.mu` guards every map, queue, journal and counter above. Engine code
reaches it only through `Manager` methods; no method runs a caller callback
under it. The order below was audited in engine-split step 4a.

- `mu` is innermost. It may be taken while the engine holds `replacementMu`
  (admission reservation) or a `transactionOwner.mu` (savepoint checkpoint and
  restore, finish).
- It is never taken while holding `Engine.mu`, `Session.mu`,
  `sessionSettings.mu` or `Engine.sessionMu`. Callers release `Engine.mu`
  before calling the manager.
- While `mu` is held, no other mutex is taken, no callback runs and nothing
  blocks. Unique-key comparers run outside it (`PinUniqueKey` releases `mu`
  around every comparison).

For reference, the surrounding engine order takes `replacementMu` and
`transactionOwner.mu` before `Engine.mu`, and `Engine.mu` before `Session.mu`,
`sessionSettings.mu` and `Engine.sessionMu`; `Session.mu` comes before
`Engine.sessionMu`. Session code reaches the engine only through
`session.Host`: `Unregister` runs under `Session.mu` and takes only
`Engine.sessionMu`; `EndMaintenance` runs with no session lock held, outside
every `SessionResource` callback, and may take `Engine.mu`.

Ticket atomics (`AssignedPublicXID`, `OwnershipEnded`, `CompletionState`) are
readable without `mu`; the engine updates them under `Engine.mu`, which is why
`PGLocks` samples each ticket's XID once per capture.

## Cancellation and teardown

- A request becomes terminal exactly once, under `mu`: granted, failed by
  deadline, canceled by its context or source, ended with its ticket
  (`ErrOwnerEnded`), fenced (`ids.ErrCatalogReplaced`) or chosen as a deadlock
  victim. The same step stops its lock timer and context callback and retires
  its timeout slot.
- Waiters are notified only after `mu` is released. A queued request waits on
  its done channel with no mutex held.
- `End` releases a ticket's grants and terminates its pending requests,
  including session requests that carry its transaction as provenance. `Fence`
  retires every ticket and resource of older catalog epochs. Both settle the
  affected queues, so every waiter that can now run is granted and every
  ended waiter is woken.
- Engine transaction tickets end through the engine's `endLockOwner`, which
  first retires engine-held exclusion state.

## Diagnostics

`inspect.go` is a deliberate seam for tests and tooling. Each method takes only
`mu`, runs no callbacks and returns owned values that share only immutable
identity (ticket and request pointers, interned key values):

- `Inspect` returns a `Snapshot` of registrations, per-resource holders and
  waiters, ticket journals, unique-key buckets and MultiXact state.
- `ExpireWait` ends a queued request with the outcome its deadlines give at a
  chosen instant; `ExpireLockTimeout` ends it with the lock-timeout outcome.
  Both use the terminal path a firing timer uses.
- `Request.Terminal`, `Request.Released` (terminal, timer and callback
  disarmed) and `MutexHeld` let tests observe lifecycle without the mutex.

The defining type of `Request` keeps the name `relationRequest`, and `Wait`
blocks in `(*relationRequest).wait`: the reference suite recognizes a parked
lock wait by that frame. Keep both names.

## What stays in the engine

Query lock admission, `LOCK TABLE` statement handling and relation scopes
(they combine locks with the catalog), the `pg_locks` projection and engine
adapters for advisory and blocking-inquiry SQL, predicate-lock (SSI) records,
safe-snapshot blocking, `Engine.exclusionPending`, the session advisory
delegate and its `EndReason`, unique-key hashing and comparison
(`uniqueKeyValues`), and XID assignment. The advisory/blocking immutable nodes
and family bind/evaluation recipes live in `functions`, not in this manager;
engine adapters retain resource construction, ticket choice and lock ordering.

## Testing

Unit tests run on `lockFixture` (`fixture_test.go`), which mints tickets and
resources from an `ids.Domain` without an engine. They cover deadlock search,
review, publication, lifecycle and deadlines, type locks, advisory counts and
delayed callbacks, blocking-PID journal authority and lifecycle, MultiXact age,
cache and counter wrap, `pg_locks` records and capture against concurrent XID
assignment, and delayed deadline release. Run them with the race detector:

```sh
make check RACE=1 RUN='^Test(RelationDeadlock|RelationLockWaitsDelayedDeadlines|TypeLockLifecycle|Advisory(CountedGrantsAndOverflow|ExpiredSourceWinsDelayedCallback)|BlockingPids(AdvisoryAuthority|Lifecycle)|MultiXact|PGLocks(ResourceRecords|ConcurrentAssignmentCapture))'
```

`TestRelationLockWaitsTimeoutPerAcquisition` uses the real manager, one waiter
ticket and one policy across two different relation resources. It checks each
generated deadline against that acquisition's start, ages only the first
request's private deadline under `mu`, and supplies explicit instants to
`ExpireWait`. Each wait consumes three quarters of its own budget; the second
crosses the first deadline, and both requests transfer successfully. The total
modeled wait is therefore one and a half budgets. A final request must fail
exactly at its own deadline with `55P03` and the exact lock-timeout diagnostic.
Grant ownership, callback retirement and empty teardown are also asserted.
Reusing a request deadline or deducting an earlier wait from a later budget
violates the fresh-deadline bound or the second wait's successful outcome.

This replaces the engine's sleep-only duplicate: sleeping 120ms does not
guarantee release within a 200ms timeout. `StartAcquire` installs a fresh
absolute deadline, and queue settlement checks it before granting, even when
the timer callback is delayed. A late release must fail rather than extend the
timeout. No production clock, timeout or lock-order behavior changes.

Selected mechanism smoke (also run with `RACE=1`):

```sh
make check RUN='^TestRelationLockWaitsTimeoutPerAcquisition$'
```

Expected: two granted acquisitions despite cumulative modeled waits exceeding
one budget, exact-boundary timeout on the third, and no retained lock state.
This is private manager coverage, not a public SQL/API witness; it needs no
new reference definition or real-mode oracle run.

Engine tests that need an engine stay in `internal/engine` and read lock state
only through the diagnostic seam.
