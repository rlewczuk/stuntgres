# internal/engine/session

Session-layer state of the engine, at layer L9 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture),
moved here in step 13d
([ENGINE-SPLIT-session.md](../../../docs/ENGINE-SPLIT-session.md)): sessions
and operations, the transaction owner's state, savepoint marks, the
per-session settings store, timeouts, notices, the transaction catalog
journal and the interfaces through which this state holds root and feature
state and calls back into the engine. State moves, orchestration stays: the
root engine still begins, publishes and ends transactions, applies settings
and dispatches statements. The engine, the root driver and `cmd/stuntgres`
name the session types directly (`session.Session`, `session.SessionStart`,
`session.TxOptions`, the end-reason, transaction-state and isolation
constants, `session.ProtocolCycle`); no aliases remain (13e).

The package imports only lower layers (`ids`, `authorization`, `catalog`,
`functions`, `lock`, `settings`, `storage`, `visibility`, `access`,
`sqltype`, `sqlstate`, `parser`). It never imports `analyze`, a feature
package (`routine`, `typedef`, `extension`, `sequence`, ...) or the root
engine: feature packages import `session` to implement its slot interfaces,
so either import would form a cycle.

## What it owns

- **Sessions** (`session.go`): `Session` (lifecycle `State`, leases,
  resources, the settings store, transaction owner, advisory and
  authorization delegates, RNG and UUID state, timeouts, the root payload),
  `SessionStart`, `EndReason`, `ResourceKind`, `ResourceHandle`,
  `SessionResource`, and the lifecycle (`Acquire`, `AcquireActivity`,
  `RequestEnd`, `WaitEnd`, `Reuse`, `Detach`, `Check`). `ValidateOperation`
  checks that an operation is live and belongs to the domain.
- **Operations** (`session.go`, `setting_admission.go`, `notices.go`,
  `protocol_input_context.go`): `Operation` (contexts, command budgets, lock
  timeouts, notices, the copy-input context, the statement lease and the
  replay and analysis slots), command admission (`Session.AcquireCommand`,
  `RefreshStatementBudget`, `CommandError`), notice levels (`NoticeLevel`,
  `LevelDebug5`…`LevelError`) and emission.
- **The transaction owner** (`owner.go`, `savepoint_marks.go`,
  `protocol_cycle.go`): `TransactionOwner` with its state, characteristics,
  admission and the journal it holds; `TxState`, `Isolation`, `TxOptions`;
  `Validate`, `AcquireAdmission`, `InBlock`, `RetainsTransaction`,
  `TruncateSavepoints`; `SavepointMark`; `ProtocolCycle`.
- **Settings store** (`settings_store.go`, `savepoint_marks.go`):
  `SettingsStore` (values, typed values, defaults, SET LOCAL actions, role
  setting actions and the owner's characteristic projections) and
  `SettingsMark` for savepoints.
- **Timeouts** (`timeout.go`): transaction, idle-in-transaction and idle
  session budgets and their timers; `TimeoutFatal`.
- **Records and delegates**: `Notification` and `NotificationAction`
  (`notification_records.go`), the advisory delegate (`advisory.go`),
  `AuthorizationState` (`authorization.go`), `RandomState` (`random.go`),
  the UUID clock (`uuid_clock.go`) and `StatementLease`
  (`statement_lease.go`).
- **The journal** (`journal.go`): `Journal`, a transaction's private catalog
  overlay (descriptors and name bindings with their originals, default ACLs,
  sequence generators, private relation storage as `StorageChange`, observed
  dependency targets as `DependencyObservation`, the cached private read,
  role overlays, fences and feature slots). `NewJournal`, `Clone` (a nil
  journal clones to an empty one) and `AddFence`.
- **Fences** (`fence.go`): `Fence` with `Kind() FenceKind`. The root engine
  declares the kinds, the fence types and the order in which publication
  checks them.
- **Journal slots** (`journal_slots.go`): `EnumSlot` and `DomainSlot`
  (implemented by `typedef.EnumJournal` and `typedef.DomainJournal`) and
  `IndexSlot` (the root's index journal). Each has an exported marker method
  and `Clone`.
- **Root slots** (`root_slots.go`): `PayloadSlot` (a session's SQL cursors,
  prepared SQL registry, retained SQL routine analyses, sequence session
  memory and notification listener), `OwnerSlot` (a transaction owner's
  foreign participants and cleanup, exclusion modes and detached cursors),
  `ExclusionMarkSlot` (a savepoint's exclusion modes and pending projection,
  nil when both are zero), `ReplaySlot` (an operation's Restore and
  materialized population state) and `AnalysisSlot` (an operation's binding
  host). Each has only an exported marker method; the root implements them
  with `sessionPayload`, `ownerExtension`, `exclusionMark`, `restoreReplay`
  and `bindingHost`.
- **The engine host** (`host.go`): `Host`, what session code needs from the
  engine that owns a session: `EndMaintenance` (TEMP collation teardown,
  called after the Transaction resource callback returns, without
  `Session.Mu` and never from inside a `SessionResource` callback) and
  `Unregister` (called under `Session.Mu`; takes only `Engine.sessionMu`).
  Both take the `*Session`. `NewOperation` (called by `Session.Operation`,
  possibly under `Session.Mu`) only allocates: the operation together with
  the root binding host in its analysis slot; a host-less test session gets
  a plain operation. The root implements `Host` with `sessionHost`, a
  pointer conversion of `*Engine`, and recovers the engine with
  `engineOf(session)`.

## Invariants

- **Locks moved, not added.** The session-layer mutexes moved here
  unchanged: `Session.Mu`, `TransactionOwner.Mu` and `Admission`,
  `SettingsStore.Mu`, and the leaf mutexes of `RandomState` and the UUID
  clock. Their order is in the [`lock` README](../lock/README.md):
  `TransactionOwner.Mu` before `Engine.mu`; `Engine.mu` before
  `Session.Mu`, `SettingsStore.Mu` and `Engine.sessionMu`; the lock manager
  innermost. Code here never takes `Engine.mu`; it reaches the engine only
  through `Host`.
- **No orchestration.** The transaction owner holds the journal; statement
  admission serializes writers, and the root engine validates and publishes
  it under `Engine.mu` (§3 rule 4). The journal itself takes no lock.
- **Copy-on-write.** A statement writes a `Clone` and installs it; savepoint
  marks keep older journals. `Clone` copies the maps, clones each non-nil
  slot through its `Clone`, and shares fences (immutable once recorded) in
  recording order.
- **Slot rules.** A nil slot means "none". Owners store and recover a slot
  with one checked helper each (`enumSlotOf`/`enumJournalOf`,
  `domainSlotOf`/`domainJournalOf`, `indexSlotOf`/`indexJournalOf` in the
  root) and never store a typed nil. A slot owner that writes in place (the
  index journal) deep-copies in `Clone`; the typedef slots are replaced
  before a write (`domainWritable`, `enumWritable`).
- **Root slot allocation.** The root allocates a session, its payload and its
  owner extension in one allocation (`sessionAllocation`), and an operation
  with its binding host in another (`Host.NewOperation`); both slots are
  set before the value is shared and never change, so neither adds an
  allocation. The replay slot is allocated on first write; the exclusion
  mark only for nonzero state. Root helpers: `payloadOf`,
  `ownerExtensionOf`/`ownerExtensionOrZero`, `exclusionMarkSlotOf`/
  `exclusionMarkOrZero`, `operationReplay`/`operationReplayOrZero` and
  `Engine.analysisHost`.
- **Fence order.** Publication checks fences by kind at fixed points, each
  kind in recording order, so the first error does not depend on storage.

## What stays in the engine

The statement scope and dispatch, and all orchestration across catalog,
storage and visibility: begin, commit, rollback and CHAIN
(`transaction.go`, `transaction_finish.go`, `transaction_controls.go`),
publication, preflight and 2PC (`transaction_publication.go`,
`transaction_prepared.go`), savepoint capture and restore (`savepoints.go`),
session start (`session_start.go`), settings application (`setting_apply.go`,
`discard.go`), notification publication and polling, the owner functions
that reach the engine (`transactionSessionEnd`, a defined type over the
owner), the private catalog read and descriptor recording, the fence types
and their `currentLocked` checks, the index journal type, the root slot
types (SQL cursors, protocol portals, the prepared SQL registry, the
notification listener, FDW participants, exclusion state, Restore replay,
the binding host).

## Testing

```sh
make check RUN='^TestJournal'
make check RUN='^(TestNoticesFilter|TestSettingsLocal(Actions|EndedCleanup))$'
make check RACE=1 RUN='^TestSessionHost'
make check RACE=1 RUN='^(TestSessionPayload|TestOwnerExtension|TestExclusionMark|TestOperation(AnalysisSlot|ReplaySlot))'
make check RUN='^(TestCatalogFences|TestCatalogTypeJournalSlotsCopyOnWrite|TestEnumProvenanceSavepoints)'
```
