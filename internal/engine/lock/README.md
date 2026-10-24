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
- **Resources and modes.** A `Resource` is a real relation, type or namespace
  (`ids.ObjectRef`), an exact namespace/relation/type destination binding
  (`Name`, with its real domain/epoch and namespace), a logical row (`Row`),
  an interned unique key (`PinUniqueKey`), transaction completion
  (`CompletionOf`) or an advisory key (`Kind`, `High`, `Low`). Schema-name
  bindings use the database-wide zero namespace address, never a synthetic OID.
  Names share ordinary ticket queues/checkpoints/end/fence; distinct keys remain
  independent. `Mode` uses PostgreSQL's table and row names; modes accumulate as
  bits and owners never conflict with themselves. Advisory grants are counted
  (`Hold.Shared`, `Hold.Exclusive`).
- **Queues and waits.** `StartAcquire` grants at once or returns the queued
  `Request`, which the caller waits on (`Wait`) after releasing its own
  locks. `Policy` carries NOWAIT, the lock timeout, the statement deadline,
  the caller's source context and the session request's provenance. Each queued
  acquisition gets its own fresh lock-timeout deadline; immediate grants are
  untimed. `WaitError` decides a wait's outcome from absolute SQL deadlines, not
  from callback scheduling: the earlier deadline wins and an exact tie favors the
  lock timeout. Queue settlement checks the deadline before granting, so a late
  release fails rather than extending the timeout. `TimeoutSlot` lets the
  session observe a pending SQL timeout without the manager mutex. Only the
  manager grants or removes requests.
- **Deadlock detection.** Every new wait runs detection over the live queues.
  Cancellation and deadlines are normalized before graph decisions. Soft cycles
  are resolved by reordering queues when a proposal removes every cycle;
  otherwise one request gets `40P01` with an exact witness.
- **Savepoints and unique-key candidates.** `Checkpoint`/`Restore` and the
  unique-key variants restore held contributions, never pending requests.
  `BeginUniqueDelta`, `RestoreUniqueDelta` and `EndUniqueDelta` journal the
  claims of one conflict-update candidate. `UniqueJournal` binds the
  manager to one ticket for these (`Checkpoint`, `Restore`, `BeginDelta`,
  `EndDelta`, `RestoreDelta`); the engine's DML code uses it.
- **MultiXact history.** The manager owns immutable MultiXact records, the
  nonzero modular counter and the association with exact row versions
  (`PhysicalEvents`, `MultiXactMembers`, `MultiAge`). Arbitration uses
  `ObjectRef` and `RowID`; history adds `VersionID`, so a successor copies the
  predecessor's original raw xmax ID and full member set, including ended or
  mixed-mode members. A batch is prepared completely before any counter or
  history change is published. Member-set reuse is a transaction-local LRU of
  at most 256 IDs, filled by creation and by member inquiry, never universal
  interning. Same-XID strength merges keep action versus lock kind.
  Savepoints restore status contributions separately from grant modes; grant
  intersection only bounds a stale checkpoint and never invents provenance.
  Inherited singleton tuple headers are separately KEYSHR|LOCK_ONLY, without
  rewriting immutable original membership. A subsequent genuine lock changes
  the effective header; checkpoints preserve earlier inherited provenance.
  Successor assignment retains the updater's original per-mode contribution
  mask before singleton promotion clears predecessor modes. Only those saved
  contributions can survive on its successor; matching owner pointers cannot
  resurrect an action retained solely in an immutable rolled-back raw bag.
  A prior singleton FOR UPDATE followed by a non-key UPDATE remains an
  `Update` action, while a fresh non-key UPDATE is `No Key Update`.
- **Inquiries.** `BlockingPids` (for `pg_blocking_pids`), `PGLocks` (the
  manager's half of `pg_locks`, as owned `Record` values) and the grant queries
  the engine uses to validate its own admission (`Holds`, `MissingRowGrant`,
  `ReplacementGrants`, `ValidSession`, `TransactionTicket`, `IndexWriters`,
  `AppendRelationNameClaims`).
  Namespace existence/removal holds project as real object tags. Exact catalog
  binding claims are internal, not physical relation/object tags; their real
  queues remain visible to blocking inquiry and deadlock arbitration.
  Generated ordinary index naming snapshots only other owners' granted relation
  names for the exact namespace/domain/epoch into caller-owned scratch. Pending
  requests alone reserve no generated name. Capture takes only manager metadata,
  outside engine/session mutexes, and creates no second reservation registry.
- **pgrowlocks capture.** `Pgrowlocks` accepts only an MVCC-selected prefix of
  exact ObjectRef/RowID/VersionID identities. It applies current HTSU-style
  eligibility separately from raw membership, preserves full aligned bags,
  excludes pending requests and projects current top-level PID ownership
  (ended/detached owners are zero). The output contains owned scalars only,
  never tickets or queues. Capture assigns nothing, settles nothing and runs
  no callbacks. The engine releases Engine.mu before entering this manager.
  `HasAccessShare`/`ReleaseAddedAccessShare` remove only the function-added
  exact-relation contribution, preserving earlier SELECT grants.

## Mutex ownership and lock order

`Manager.mu` guards every map, queue, journal and counter above. Engine code
reaches it only through `Manager` methods; no method runs a caller callback
under it.

- `mu` is innermost. It may be taken while the engine holds `replacementMu`
  (admission reservation) or a `session.TransactionOwner.Mu` (savepoint
  checkpoint and restore, finish).
- It is never taken while holding `Engine.mu`, `Session.Mu`,
  `SettingsStore.Mu` or `Engine.sessionMu`. Callers release `Engine.mu`
  before calling the manager.
- While `mu` is held, no other mutex is taken, no callback runs and nothing
  blocks. Unique-key comparers run outside it (`PinUniqueKey` releases `mu`
  around every comparison).

For reference, the surrounding engine order takes `replacementMu` and
`TransactionOwner.Mu` before `Engine.mu`, and `Engine.mu` before `Session.Mu`,
`SettingsStore.Mu` and `Engine.sessionMu`; `Session.Mu` comes before
`Engine.sessionMu`. Session code reaches the engine only through
`session.Host`: `Unregister` runs under `Session.Mu` and takes only
`Engine.sessionMu`; `EndMaintenance` runs with no session lock held, outside
every `SessionResource` callback, and may take `Engine.mu`; `NewOperation` only
allocates and takes no lock. The session-layer mutexes are exported fields of
`session` types (`Session.Mu`, `TransactionOwner.Mu` and `Admission`,
`SettingsStore.Mu`). The LISTEN/NOTIFY registry and listener fields
(`session.NotificationRegistry`, `session.NotificationListener`) belong to
`Engine.mu`; only root code takes it around their `…Locked` methods, and the
root `notificationSessionEnd` resource callback takes it to end a listener.

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

**Frame names matched by tests.** The defining type of `Request` is
`relationRequest`, and `Wait` blocks in `(*relationRequest).wait`: the reference
suite recognizes a parked lock wait by that frame. Keep both names.

## What stays in the engine

Query lock admission, `LOCK TABLE` statement handling and relation scopes
(they combine locks with the catalog), the `pg_locks` projection and engine
adapters for advisory and blocking-inquiry SQL, predicate-lock (SSI) records,
safe-snapshot blocking, the exclusion projection registry instance,
unique-key pins, and XID assignment. The registry type
(`index.ExclusionRegistry`) and the unique key the pins lock
(`index.UniqueKeyValues`, `index.UniqueKeyHash`) are
[`index`](../index/README.md) code. The advisory/blocking immutable nodes
and family bind/evaluation recipes live in `functions`; engine adapters retain
resource construction, ticket choice and lock ordering.

## Testing

Unit tests run on `lockFixture` (`fixture_test.go`), which mints tickets and
resources from an `ids.Domain` without an engine; drive deadlines with
`ExpireWait` and explicit instants, never sleeps. Run them with the race
detector. Engine tests that need an engine stay in `internal/engine` and read
lock state only through the diagnostic seam.

```sh
make check RACE=1 RUN='^Test(RelationDeadlock|RelationLockWaits(DelayedDeadlines|TimeoutPerAcquisition)|TypeLock|Advisory(Counted|Expired)|BlockingPids(AdvisoryAuthority|Lifecycle)|MultiXact|PGLocks(ResourceRecords|ConcurrentAssignment)|UniqueJournal)'
make check RACE=1 RUN='^TestTransactionalSchemaLockResources$'
make check RUN='^TestPgrowlocks'   # pgrowlocks capture through a real engine
```
