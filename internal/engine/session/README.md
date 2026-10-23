# internal/engine/session

Session-layer state of the engine, at layer L7 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture)
([ENGINE-SPLIT-session.md](../../../docs/ENGINE-SPLIT-session.md)): sessions
and operations, the transaction owner's state, savepoint marks, the
per-session settings store, timeouts, notices, the transaction catalog journal
and the interfaces through which this state holds root and feature state and
calls back into the engine. State lives here, orchestration stays in the root:
the engine begins, publishes and ends transactions, applies settings and
dispatches statements. The engine, the root driver and `cmd/stuntgres` name
the session types directly (`session.Session`, `session.SessionStart`,
`session.TxOptions`, the end-reason, transaction-state and isolation
constants, `session.ProtocolCycle`).

The package imports only lower layers (`ids`, `authorization`, `catalog`,
`functions`, `lock`, `settings`, `storage`, `visibility`, `access`, `sqltype`,
`sqlstate`, `parser`). It never imports `analyze`, a feature package
(`routine`, `typedef`, `extension`, `sequence`, ...) or the root engine:
feature packages import `session` to implement its slot interfaces, so either
import would form a cycle.

## What it owns

- **Sessions** (`session.go`): `Session` (lifecycle `State`, leases,
  resources, the settings store, transaction owner, advisory and authorization
  delegates, RNG and UUID state, timeouts, the root payload), `SessionStart`,
  `EndReason`, `ResourceKind`, `ResourceHandle`, `SessionResource`, and the
  lifecycle (`Acquire`, `AcquireActivity`, `RequestEnd`, `WaitEnd`, `Reuse`,
  `Detach`, `Check`). `ValidateOperation` checks that an operation is live and
  belongs to the domain; `TransactionAuthority` checks transaction admission
  authority: domain, a live session and the operation's membership.
- **Operations** (`session.go`, `setting_admission.go`, `notices.go`,
  `protocol_input_context.go`): `Operation` (contexts, command budgets, lock
  timeouts, notices, the copy-input context, the statement lease and the
  replay and analysis slots), command admission (`Session.AcquireCommand`,
  `RefreshStatementBudget`, `CommandError`, `StatementStart`), notice levels
  (`NoticeLevel`, `LevelDebug5`…`LevelError`) and emission. `FTSInput`
  (`fts_input.go`) emits an operation's text-search input notices.
- **The transaction owner** (`owner.go`, `savepoint_marks.go`,
  `protocol_cycle.go`): `TransactionOwner` with its state, characteristics,
  admission and the journal it holds; `TxState`, `Isolation` (`Stable`),
  `TxOptions`; `Validate`, `AcquireAdmission`, `InBlock`,
  `RetainsTransaction`, `TruncateSavepoints`; `SavepointMark`;
  `ProtocolCycle`.
  - Isolation setting text: `IsolationFromText`, `IsolationText`
    (`isolation_text.go`).
  - Checked owner handles (`owner_handles.go`): `TxOwnerOf` and
    `PrivateSessionOf` recover the owner and the private session from the
    `ids` owner interfaces; `TxOwnerAttached` and `TokenAttached` check them
    before lock registration.
  - Characteristic decisions (`transaction_characteristics.go`):
    `AssignModes` validates and installs SET TRANSACTION modes;
    `CharacteristicMode` and `Characteristic` map the current-characteristic
    settings to modes and back.
  - `SerializableWriteSurvives` (`serializable_writes.go`) tells rollback
    which serializable write evidence the owner still holds.
  - The root keeps the `TransactionOwner.Mu` and `Engine.mu` windows and the
    settings sync.
- **Settings store** (`settings_store.go`, `savepoint_marks.go`):
  `SettingsStore` (values, typed values, defaults, sparse per-definition
  provider-present provenance, SET LOCAL actions, role setting actions and the
  owner's characteristic projections) and `SettingsMark` for savepoints.
  - Default providers are called once at physical startup, never by metadata
    reads or RESET. Boot-equal present responses still have client
    provenance; absent responses stay defaults. Teardown clears the owned
    provenance map with value storage.
  - Settings cores: `Presentation` captures the typed input and output
    environments (`settings_presentation.go`); `LookupDefinition` resolves a
    name against the registry and placeholders, `ParseValue` parses an input
    without installing it, `SetInput` normalizes SET arguments and emits its
    warnings (`setting_values.go`); `SettingAccess` checks the access class,
    asking an `allowed` callback for the `lc_messages` parameter grant
    (`setting_access.go`). The root keeps the install windows (`Engine.mu`,
    `SettingsStore.Mu`, `Session.Mu`) and the authorization behind `allowed`.
- **Statement classification** (pure AST functions): command tags
  (`command_tag.go`: `ClassifyCommandTag`, `QueryCreationUtilityTag`,
  `ForeignDDLTag`) and the admission predicates (`statement_class.go`:
  `ModifyingTag` for read-only refusal, `ExplicitUtility`,
  `EarlyIndexUtility`, `ForeignGrantKind`). The root dispatch keeps the
  admission checks, their exceptions and their errors.
- **Timeouts** (`timeout.go`): transaction, idle-in-transaction and idle
  session budgets and their timers; `TimeoutFatal`.
- **Records and delegates**: `Notification` and `NotificationAction`
  (`notification_records.go`); the listener queue records
  (`notification_queue.go`): `NotificationState`, the persistent
  `NotificationFIFO` of `NotificationLink` batches and the pure helpers
  `AppendNotificationEvents`, `FilterNotificationBatch` and
  `ScanNotificationPending`; the advisory delegate (`advisory.go`),
  `AuthorizationState` (`authorization.go`), `RandomState` (`random.go`), the
  v7 UUID clock (`uuid_clock.go`), the coupled nontransactional v1 allocator
  (`uuid_v1.go`, `Session.UUIDV1`) and `StatementLease`
  (`statement_lease.go`).
- **LISTEN/NOTIFY**: `NotificationListener` (`notification_listener.go`), one
  session's listener held in the root payload, with its `Session`
  back-pointer, state, busy, ended and wake fields and the monotonic `Used`
  hint; `NotificationRegistry`, the engine's committed registrations by
  session token (nil until the first), with `EndLocked`.
  - Publication (`notification_publication.go`): `CaptureLocked` records the
    listeners into a `NotificationPublication`, `Prepare` computes the
    replacement states and registry without the lock, and `UnchangedLocked`
    and `InstallLocked` check and install them.
  - `NotificationEffects` (`notification_effects.go`) returns a publication's
    actions and events over a `NotificationStatement`;
    `TransactionOwner.MergeNotifications` merges a statement's into the
    owner's.
  - Every field but `Used` belongs to `Engine.mu`; the root takes it around
    the `…Locked` methods and keeps polling, the idle-boundary scan,
    `executeNotification` and the `notificationSessionEnd` resource.
- **The journal** (`journal.go`): `Journal`, a transaction's private catalog
  overlay (descriptors and name bindings with their originals, default ACLs,
  sequence generators, private relation storage as `StorageChange`, observed
  dependency targets as `DependencyObservation`, the cached private read, role
  overlays, fences and feature slots). `NewJournal`, `Clone` (a nil journal
  clones to an empty one) and `AddFence`. The bookkeeping over the published
  base state (`journal_bookkeeping.go`): `PrivateRead` (cached; a nil journal
  reads the base), `RecordDescriptor`, `RecordDefaultACL` and
  `ObserveCreationACL`. The root's `…Locked` wrappers pass the base under
  `Engine.mu`.
- **Storage projections** (`storage_projection.go`): `ColumnProjection`
  (`NewColumnProjection`, `Versions`), `ProjectVersions`, `ProjectDeletions`,
  `ProjectPrivateSnapshotRoots` (attaches dense snapshot projections to a
  journal's `StorageChange` entries, cloning only the shape map) and
  `ProjectColumnShapeRoots` (projects a column shape change through committed
  storage, private writes and deletes and a stable snapshot root). They read
  only catalog, storage and visibility records; the root's
  `projectColumnShape` supplies the statement's roots.
- **Fences** (`fence.go`): `Fence` with `Kind() FenceKind`. The root engine
  declares the kinds, the fence types and the order in which publication
  checks them.
- **Journal slots** (`journal_slots.go`): `EnumSlot` and `DomainSlot`
  (implemented by `typedef.EnumJournal` and `typedef.DomainJournal`) and
  `IndexSlot` (implemented by `index.Journal`). Each has an exported marker
  method and `Clone`; `IndexSlot` also has `Forget`, which `RecordDescriptor`
  calls for a removal.
- **Root slots** (`root_slots.go`): `PayloadSlot` (a session's SQL cursors,
  prepared SQL registry, retained SQL routine analyses, sequence session
  memory and notification listener), `OwnerSlot` (a transaction owner's
  foreign participants and cleanup, exclusion modes and detached cursors),
  `ExclusionMarkSlot` (a savepoint's exclusion modes and pending projection,
  nil when both are zero), `ReplaySlot` (an operation's Restore and
  materialized population state) and `AnalysisSlot` (an operation's binding
  host). Each has only an exported marker method; the root implements them
  with `sessionPayload`, `ownerExtension`, `restoreReplay` and `bindingHost`,
  and `index` implements the exclusion mark with `index.ExclusionMark`.
- **The engine host** (`host.go`): `Host`, what session code needs from the
  engine that owns a session. `EndMaintenance` (TEMP collation teardown) is
  called after the Transaction resource callback returns, without
  `Session.Mu` and never from inside a `SessionResource` callback.
  `Unregister` is called under `Session.Mu` and takes only
  `Engine.sessionMu`. Both take the `*Session`. `NewOperation` (called by
  `Session.Operation`, possibly under `Session.Mu`) only allocates: the
  operation together with the root binding host in its analysis slot; a
  host-less test session gets a plain operation. The root implements `Host`
  with `sessionHost`, a pointer conversion of `*Engine`, and recovers the
  engine with `engineOf(session)`.

## Invariants

- **Lock order.** The session-layer mutexes are `Session.Mu`,
  `TransactionOwner.Mu` and `Admission`, `SettingsStore.Mu`, and the leaf
  mutexes of `RandomState` and the UUID clocks. Their order is in the
  [`lock` README](../lock/README.md#mutex-ownership-and-lock-order):
  `TransactionOwner.Mu` before `Engine.mu`; `Engine.mu` before `Session.Mu`,
  `SettingsStore.Mu` and `Engine.sessionMu`; the lock manager innermost. Code
  here never takes `Engine.mu`; it reaches the engine only through `Host`.
- **UUID v1 allocation.** A private leaf mutex couples the v1/v1mc fallback
  node, the observed microsecond bucket, the 100 ns slot and the 14-bit
  sequence. No catalog, transaction, lifecycle or v7 mutex is held over
  entropy or clock-progress waiting. Entropy staging holds only the allocator
  mutex; cancellation-aware slot waits release it and recapture clock and
  state on return. Failed or canceled draws commit nothing. Rollback, Reset
  and Restore do not rewind this physical session state, which is never
  serialized. Sequence wrap rotates the random fallback node.
  Cross-session/random-epoch uniqueness is probabilistic; there is no host-MAC
  discovery, uuidd or persistent clock file.
- **No orchestration.** The transaction owner holds the journal; statement
  admission serializes writers, and the root engine validates and publishes it
  under `Engine.mu` (§3 rule 4). The journal itself takes no lock.
- **Copy-on-write.** A statement writes a `Clone` and installs it; savepoint
  marks keep older journals. `Clone` copies the maps, clones each non-nil slot
  through its `Clone`, and shares fences (immutable once recorded) in
  recording order.
- **Slot rules.** A nil slot means "none". Owners store and recover a slot
  with one checked helper each (`typedef.EnumSlotOf`/`EnumJournalOf`,
  `DomainJournalOf` and its unexported store, `index.SlotOf`/`index.JournalOf`
  for `index.Journal`) and never store a typed nil. A slot owner that writes
  in place (the index journal) deep-copies in `Clone`; the typedef slots are
  replaced before a write (`typedef.DomainWritable`, `EnumWritable`).
- **Root slot allocation.** The root allocates a session, its payload and its
  owner extension in one allocation (`sessionAllocation`), and an operation
  with its binding host in another (`Host.NewOperation`); both slots are set
  before the value is shared and never change, so neither adds an allocation.
  The replay slot is allocated on first write, the exclusion mark only for
  nonzero state. Root helpers: `payloadOf`,
  `ownerExtensionOf`/`ownerExtensionOrZero`,
  `operationReplay`/`operationReplayOrZero` and `Engine.analysisHost`; for the
  exclusion mark `index.ExclusionMarkSlotOf`/`ExclusionMarkOrZero`.
- **Fence order.** Publication checks fences by kind at fixed points, each
  kind in recording order, so the first error does not depend on storage.

## What stays in the engine

The statement scope and dispatch, and all orchestration across catalog,
storage and visibility: begin, commit, rollback and CHAIN (`transaction.go`,
`transaction_finish.go`, `transaction_controls.go`), publication, preflight
and 2PC (`transaction_publication.go`, `transaction_prepared.go`), savepoint
capture and restore (`savepoints.go`), session start (`session_start.go`),
settings application (`setting_apply.go`, `discard.go`), the `Engine.mu`
windows of notification publication (capture, check and install), polling and
the idle-boundary scan, `executeNotification`, the owner functions that reach
the engine (`transactionSessionEnd`, a defined type over the owner, and
`notificationSessionEnd` over the listener), the `…Locked` wrappers of the
private catalog read and descriptor recording, the fence types and their
`currentLocked` checks, and the root slot types (SQL cursors, protocol
portals, the prepared SQL registry, FDW participants, exclusion state, Restore
replay, the binding host).

## Testing

Package units cover the journal, notices, SET LOCAL actions, the notification
queue, registry and publication, characteristic modes, serializable write
survival, the `lc_messages` access callback and the UUID v1 allocator. Engine
tests (in `internal/engine`) cover the host and root slots, catalog fences and
column-shape projections, and settings provider capture with retained
authority through the engine's package seams (there is no public provider
API for them).

```sh
make check RUN='^Test(AssignModesValidation|CharacteristicModeMapping|Journal|NoticesFilter|Notification(Queue|RegistryEndLocked|Publication)|SerializableWriteSurvives|SettingAccessAllowedCallback|SettingsLocal|UUIDOsspTime)'
make check RACE=1 RUN='^Test(SessionHost|SessionPayload|OwnerExtension|ExclusionMark|Operation(AnalysisSlot|ReplaySlot)|UUIDOsspTimeConcurrent$$)'
make check RUN='^Test(CatalogFences|EnumProvenanceSavepoints|AlterAddColumn(Projection|ZeroWidthProjection|CancellationDuringShapeTraversal|NonvolatileDemandOnce|PrivateSnapshotEqualWidthProjection)$$)'
make check RUN='^Test(PGSettings(ProviderCapture|CoherentCapture|CatalogACLRecheck)|RowSecurity(SettingProvenance|ProviderLifecycle))$$'
```
