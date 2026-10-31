# internal/engine/lock

The heavyweight lock manager. It sits at layer L2 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture) and
imports only `ids`, `sqlstate` and the standard library. It never imports the
engine.

## What it owns

`Manager` holds heavyweight lock state for one cluster's admitted databases:

- **Tickets.** One `Ticket` per registered transaction (`Register`) and one
  persistent ticket per session for session-level advisory locks
  (`RegisterSession`). A ticket journals every resource it holds or waits on.
  Its transaction ID, phase and completion state are atomic mirrors that the
  engine publishes (`MirrorFullXID`, `MirrorCompletion`). A prepared ticket is
  detached from its session (`DetachPrepared`).
- **Resources and modes.** A `Resource` is a real relation, type, namespace,
  collation, conversion, statistics, cast, transform, procedure, language, text-search parser/template/dictionary/configuration,
  access method, operator family/class or shared role (`ids.ObjectRef`),
  an exact catalog destination binding (`Name`),
  a role attribute tuple claim (`RoleTuple`), a logical row (`Row`), an interned
  unique key (`PinUniqueKey`), transaction completion (`CompletionOf`) or a
  database-scoped advisory key (`Database`, `Kind`, `High`, `Low`). Local resources
  must match their ticket's database domain and epoch; shared role resources use
  the cluster's stable role domain. Schema and role names use the zero namespace
  address, never a synthetic OID. All ordinary resources share one ticket,
  checkpoint and deadlock graph across databases. `Mode` uses PostgreSQL's table
  and row names; modes accumulate as bits and owners never conflict with
  themselves. Advisory grants are counted (`Hold.Shared`, `Hold.Exclusive`).
  Procedure/operator names include exact input signatures, including zero-arity
  procedures and NONE operator operands. Family/class names include their exact
  SQL method signature; method names are global and member names identify actual
  family/pair/slot keys. Other name classes forbid signatures.
  Cast and transform names share canonical nonzero uint32 decimal pair syntax,
  with zero namespace and no signature: source/target type for casts, SQL
  type/language for transforms. Ordered endpoints and catalog class remain
  independent keys; malformed numbers, namespaces and ticket epochs are rejected.
  There is no default-class absence lock. Procedures, transforms, languages, text-search parser/template/dictionary/configuration and
  nonpinned method/family/class providers use ordinary AS/AX object grants, not
  relation aliases. The engine acquires and waits outside `Engine.Mu`, then
  rechecks original identities;
  definition DROP holds AX before capturing its complete dependent closure.
  Catalog ACL writer claims use a real catalog-object address; default ACL
  claims use the actual creator ID, namespace/global scope and default class.
  Their distinct resource kinds share this manager's queues and ticket
  checkpoints, cancellation, end and epoch fencing. Internal exclusive claim
  mode is not a relation AX lock; named grants separately acquire relation AS.
  `DefaultACLWriter` encodes the creator in High/Low only for that distinct
  kind; `DefaultCreator` decodes it. The class fits beside Kind in existing
  padding. Keep Resource within its original compact layout and below Go's
  indirect map-key threshold; ordinary immediate/reentrant grants must remain
  zero-allocation, as the existing lock/timeout tests require.
  Persisted role-default writers use `RoleSettingResource` in the shared role
  domain: the full database address (zero for ALL) and principal ID in `High`
  identify one exact row, with no extra resource fields or registry. Principal
  retirement protection precedes this writer; grants live on the same transaction
  ticket and restore through ordinary checkpoints. These internal claims project
  blocker XIDs, never fabricated physical object tags.
  Subscription object and database-discriminated name claims use the same
  stable cluster domain, so cross-database REASSIGN and local ALTER arbitrate
  on the actual shared object identity. The core adapter retains its original
  local reference for post-wait catalog/epoch validation; object lock records
  expose the shared database-zero tag.
  Parameter canonical-name writers (including absent first grants) and retained
  parameter ACL-address claims also use the stable cluster domain. Parameter
  names have zero namespace and no signature. The adapter retains original local
  epoch and immutable parameter predecessor/incarnation fences across waits.
  These internal claims expose real blocker XIDs through existing inquiry,
  never synthetic physical tuple or index records.
  Replication-origin name writers likewise use the stable cluster domain, zero
  namespace and no signature. Their exact TEXT key may be empty; this exception
  does not admit empty SQL identifier names. They share ordinary transaction
  queues and expose blocker XIDs, not physical index tuples. Origin object
  grants use real class6000 IDs 1..65534 in that same shared domain, with AS/AX
  modes and database-zero object tags, never relation aliases. Subscription
  retirement and standalone retirement use this single object authority.
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
- **MultiXact history.** The manager owns database-partitioned immutable records,
  nonzero modular counters and associations with exact row versions
  (`PhysicalEvents`, `MultiXactMembers`, `MultiAge`).
  Arbitration uses `ObjectRef` and `RowID`; history adds `VersionID`, so a successor copies the
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
  `RelationHolders`, `AppendRelationNameClaims`). `RelationHolders` freezes a
  specified conflict-mode cohort without acquiring or queuing that mode.
  `RetireDetachPhase` captures the AX-conflicting cohort and releases the
  completed utility phase's SUE relation grants in one manager critical section;
  the transaction ticket, completion resource and unrelated grants remain live.
  Namespace, procedure, transform, language, text-search parser/template/dictionary/configuration, access-method,
  family/class and shared role holds project as real object tags; shared roles
  have SQL database zero. Exact name and
  role-tuple claims are internal, not physical
  object tags; pending claims project the actual blocker XID. Their queues join
  blocking inquiry and the same deadlock graph without exposing peer local locks.
  Catalog/default ACL claims likewise emit no invented relation, object or
  advisory holder tags. Their pending projection follows real blocker XIDs
  through the existing transaction dependency path.
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
  no callbacks. The engine releases Engine.Mu before entering this manager.
  `StartAcquireRelation` records a caller-owned `RelationContribution`, released
  by `ReleaseRelation` after its request is terminal. Scoped AccessShare/Share
  ownership is counted separately from idempotent ordinary transaction grants;
  their union supplies conflict and inquiry modes. Cleanup preserves ordinary
  same-mode grants acquired before or during verification and other nested
  scopes. Savepoint checkpoints never promote active scoped contributions into
  ordinary ownership. Canceled upgrades do not remove an earlier hold; retired
  ticket and entry identities fence delayed cleanup from a replacement grant.

Engine type mutation resource order is source TypeExclusive, actual composite
backing AX when required, then typed-table closure AX. Schema moves retain source
type authority without adding backing AX; scalar sources never fabricate a
relation resource. These are ordinary ticket contributions, restored by savepoint
checkpoints and released at owner end.

Definition admission uses real `pg_collation` relation ShareRowExclusive for
the protected phase of collation CREATE, AccessExclusive on mutated/retired
definition objects, and AccessShare on actual declaration providers. Namespace,
option, FROM and provider-specific preparation and serial same-encoding
duplicate/skip checks precede ShareRowExclusive; cross-encoding shadow checks,
remaining provider loading/version work and insertion follow it. Exact-key
insertion races retain `23505`, including IF NOT EXISTS. Definition addresses are
sorted by class/OID and deduplicated. Collation/conversion names use their real namespace;
cast names use the zero namespace and canonical directed endpoint key. Language
names likewise use global zero-namespace keys, and language declaration consumers
use ordinary AS grants against ALTER/DROP AX grants. No conversion DEFAULT-pair
lock is manufactured. These contributions share ordinary savepoint, cancellation,
owner-end and replacement lifetimes. Object holds project
as object tags; internal destination claims expose only actual blocker XIDs.
Publication names likewise use local zero-namespace keys. Publication definition
AS/AX grants retain the ticket's own database domain and epoch, and project as
non-shared class6104 object locks; mapping edges have no independent lock owner.

ALTER retains its original target even for no-op actions, independently of DROP
roots. After the exact object grant, current authority must still identify that
original name/address/namespace; a search-path fallback is never locked or adopted.
Native replacement failure clears explicit-transaction savepoints and requires
full rollback before starting a fresh transaction on the same physical owner.


## Mutex ownership and lock order

`Manager.mu` guards every map, queue, journal and counter above. Engine code
reaches it only through `Manager` methods; no method runs a caller callback
under it.

- `mu` is innermost. It may be taken while the engine holds `replacementMu`
  (admission reservation) or a `session.TransactionOwner.Mu` (savepoint
  checkpoint and restore, finish).
- It is never taken while holding `Engine.Mu`, `Session.Mu`,
  `SettingsStore.Mu` or `Engine.sessionMu`. Callers release `Engine.Mu`
  before calling the manager.
- While `mu` is held, no other mutex is taken, no callback runs and nothing
  blocks. Unique-key comparers run outside it (`PinUniqueKey` releases `mu`
  around every comparison).

For reference, the surrounding engine order takes `replacementMu` and
`TransactionOwner.Mu` before `Engine.Mu`, and `Engine.Mu` before `Session.Mu`,
`SettingsStore.Mu` and `Engine.sessionMu`; `Session.Mu` comes before
`Engine.sessionMu`. Session code reaches the engine only through
`session.Host`: `Unregister` runs under `Session.Mu`, removes registration under
`Engine.sessionMu`, drains the leaf statistics provider, and releases physical
admission. `EndMaintenance` runs without session/admission/owner locks, outside
every `SessionResource` callback, and may take metadata locks. Unconditional
post-resource `DrainStatistics` cancels and joins callbacks outside locks.
`StatisticsReadyLocked` runs under `Session.Mu` but only
tries admission/owner mutexes without waiting, then takes the leaf provider lock.
It cannot wait in the inverse owner/session order. `NewOperation` only allocates
and takes no lock. The session-layer mutexes are exported fields of
`session` types (`Session.Mu`, `TransactionOwner.Mu` and `Admission`,
`SettingsStore.Mu`). The LISTEN/NOTIFY registry and listener fields
(`session.NotificationRegistry`, `session.NotificationListener`) belong to
`Engine.Mu`; only root code takes it around their `…Locked` methods, and the
root `notificationSessionEnd` resource callback takes it to end a listener.

Ticket atomics (`AssignedPublicXID`, `OwnershipEnded`, `CompletionState`) are
readable without `mu`; the engine updates them under `Engine.Mu`, which is why
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
  including session requests that carry its transaction as provenance.
  `Fence(databaseDomain, epoch)` retires only that database's older tickets,
  resources and physical history, including its shared-role contributions.
  Peer database owners and holds survive. Both operations settle affected queues,
  granting eligible waiters and waking ended owners.
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
detector. Engine tests that need an engine live in `internal/engine/core` (relation
locks, waits and deadlocks) or `internal/engine` and read lock state only
through the diagnostic seam.
Publication name/definition resource admission and object-tag ownership use
`RUN='^TestPublicationDefinitionLockResources$'`.

```sh
make check RACE=1 RUN='^Test(RelationDeadlock|RelationLockWaits(DelayedDeadlines|TimeoutPerAcquisition)|TypeLock|Advisory(Counted|Expired)|BlockingPids(AdvisoryAuthority|Lifecycle)|MultiXact|PGLocks(ResourceRecords|ConcurrentAssignment)|UniqueJournal)'
make check RACE=1 RUN='^TestTransactionalSchemaLockResources$'
make check RACE=1 RUN='^(TestDictionaryLockResources|TestTransactionalDefinitionLockResources)$'
make check RUN='^TestPgrowlocks'   # pgrowlocks capture through a real engine
```
