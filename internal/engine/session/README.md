# internal/engine/session

Session-layer state at layer L7 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture): sessions and
operations, the transaction owner's state, savepoint marks, the per-session
settings store, timeouts, notices, LISTEN/NOTIFY records, the transaction
catalog journal and the slot interfaces through which this state holds root and
feature state. State lives here; orchestration stays in the root, which begins,
publishes and ends transactions, applies settings and dispatches statements.
The engine, the root driver and `cmd/stuntgres` name the session types
directly.

It imports only lower layers (`ids`, `authorization`, `catalog`, `functions`,
`lock`, `settings`, `storage`, `visibility`, `access`, `sqltype`, `sqlstate`,
`parser`), never `analyze`, a feature package (`routine`, `typedef`,
`extension`, `sequence`, ...) or the root: feature packages import `session` to
implement its slots, so either import would form a cycle.

## What it owns

- **Sessions and operations** (`session.go`): `Session` (lifecycle, leases,
  resources, settings store, transaction owner, delegates, timeouts, root
  payload) with `Acquire`, `RequestEnd`, `WaitEnd`, `Reuse`, `Detach` and
  `Check`; `ValidateOperation` and `TransactionAuthority` check liveness, domain
  and membership. `Operation` carries contexts, command budgets, lock timeouts,
  notices, the copy-input context, the statement lease and the replay and
  analysis slots; `Session.AcquireCommand` admits commands. Notice levels,
  emission and the relation skip notices (`ExistingRelation`,
  `MissingRelation`) are in `notices.go`.
- **Transaction owner** (`owner.go`, `savepoint_marks.go`, `protocol_cycle.go`):
  `TransactionOwner` with its state, characteristics, admission and journal;
  `TxState`, `Isolation`, `TxOptions`, `SavepointMark`, `ProtocolCycle`. Checked
  handles (`owner_handles.go`) recover owners from the `ids` interfaces;
  `AssignModes` and `CharacteristicMode` decide transaction modes
  (`transaction_characteristics.go`); `SerializableWriteSurvives` tells rollback
  which serializable write evidence survives.
  Stable isolation retains immutable tuple roots, the commit frontier, sequence
  catalog and public transaction snapshot as one acquisition on this owner.
  Snapshot-requiring analysis inside the transaction can establish that pair;
  preparing before BEGIN cannot retain it into the later transaction. RC analysis
  does not retain execution authority. Command-local pairs belong to
  `StatementScope`; cursors retain their producer's pair across fresh FETCH
  admissions. Root lifecycle cleanup releases the owner pair at transaction end.
  `SnapshotSet` also records that RC execution has acquired a snapshot, closing
  the transaction-characteristics window without retaining a transaction pair.
- **Settings store** (`settings_store.go`): `SettingsStore` (values, defaults,
  provider provenance, SET LOCAL and role setting actions, characteristic
  projections) and `SettingsMark`. Default providers run once at physical
  startup, never for metadata reads or RESET. The settings cores
  (`Presentation`, `LookupDefinition`, `ParseValue`, `SetInput`,
  `SettingAccess`) parse and check without installing; the root keeps the
  install windows and the authorization behind `SettingAccess`'s `allowed`
  callback.
- **Statement classification** (pure AST functions): command tags
  (`command_tag.go`) and admission predicates (`statement_class.go`:
  `ModifyingTag`, `ExplicitUtility`, ...). The root keeps the checks, their
  exceptions and errors.
- **Timeouts** (`timeout.go`): transaction, idle-in-transaction and idle session
  budgets and timers.
- **LISTEN/NOTIFY** (`notification_*.go`): records, persistent listener FIFO,
  immutable global page positions, `NotificationListener` (in the root payload),
  `NotificationRegistry` and publication (`CaptureLocked`, unlocked `Prepare`,
  `OldestPrepared`, `UnchangedLocked`, `InstallLocked`). Every mutable field but
  the listener's `Used` hint belongs to the cluster-shared `Engine.mu`, which the
  root takes around the `…Locked` methods. A state's optional unread position
  retains the oldest global boundary, including cursor-only foreign traffic;
  Pending owns local unprocessed batches and Ready owns client-ready events.
  Preparation receives the pre-batch head and local-delivery eligibility.
  Unlocked copying preserves every field; last UNLISTEN, outer idle processing
  and exact owner end release holds, while Ready dequeue preserves them.
- **Delegates**: advisory, `AuthorizationState`, `RandomState`, the v7 UUID
  clock, the v1 allocator (`uuid_v1.go`) and `StatementLease`.
- **Journal** (`journal.go`, `journal_bookkeeping.go`): `Journal`, a
  transaction's private catalog overlay (descriptors and name bindings with
  originals, default ACLs, sequence generators, private storage changes,
  dependency observations, role overlays, fences and feature slots), with
  `Clone`, `AddFence`, `PrivateRead`, `RecordDescriptor`, `RecordDefaultACL`,
  `RecordCandidateDefaults` and `MergeCandidate` (a `catalog.Candidate`
  checked and recorded against the published base). The root's `…Locked`
  wrappers pass the published base under `Engine.mu`.
  `StorageChange.MergeFills` merges added-column fills and drops those of
  removed attributes, replacing the shared fill map instead of mutating it.
- **Journal capture** (`journal_capture.go`): `JournalCapture` turns a
  validated stage's `catalog.Candidate` into the retained transaction's
  observations and fences, over the published and private catalog states:
  `ObserveDatabase`, `Observe`, `ObserveBase`, `ObserveCandidate` and
  `FenceNames`. The root calls them between its own domain,
  materialized-view, index and storage steps, which fixes the recording
  order and the first error.
- **Storage projections** (`storage_projection.go`): column-shape and private
  snapshot projections that read only catalog, storage and visibility records.
- **Fences** (`fence.go`, `journal_fences.go`): `Fence` with `Kind()`, and
  three session kinds with their fences: `FenceStorageRead`
  (`StorageReadFence`), `FenceIndexSet` (`IndexSetFence`) and `FenceNamespace`
  (`NamespaceFence`). They implement `CommittedFence`, whose `Current` reads
  only the published catalog and storage relations. The root declares its own
  kinds from `FenceRootKinds` on, and the order in which publication checks
  the kinds.
- **Slots**: journal slots (`journal_slots.go`: `EnumSlot`, `DomainSlot`,
  `IndexSlot`, implemented by `typedef` and `index`) and root slots
  (`root_slots.go`: `PayloadSlot`, `OwnerSlot`, `ExclusionMarkSlot`,
  `ReplaySlot`, `AnalysisSlot`, implemented by the root and `index`). Each has
  an exported marker method; journal slots also have `Clone`.
- **Engine host** (`host.go`): `Host`, implemented by the root's `sessionHost`
  (a pointer conversion of `*Engine`). `EndMaintenance` runs after the
  Transaction resource callback returns, without `Session.Mu` and never inside a
  `SessionResource` callback. `Unregister` runs under `Session.Mu` and takes
  only `Engine.sessionMu`. `NewOperation` (possibly under `Session.Mu`) only
  allocates the operation with its binding host.

## Invariants

- **Lock order.** The session-layer mutexes are `Session.Mu`,
  `TransactionOwner.Mu` and `Admission`, `SettingsStore.Mu`, and the leaf
  mutexes of `RandomState` and the UUID clocks; their order is in the
  [`lock` README](../lock/README.md#mutex-ownership-and-lock-order). Code here
  never takes `Engine.mu`; it reaches the engine only through `Host`.
- **No orchestration.** The transaction owner holds the journal; statement
  admission serializes writers, and the root validates and publishes it under
  `Engine.mu`. The journal itself takes no lock.
- **Copy-on-write.** A statement writes a `Clone` and installs it; savepoint
  marks keep older journals. `Clone` copies the maps, clones each non-nil slot
  and shares fences (immutable once recorded) in recording order.
  Permanent table/schema descriptors, companions and row storage share these
  roots. Their namespace/name holds use the transaction ticket's same checkpoint;
  sequence counter effects remain outside rollback of owned definitions.
- **Unnamed procedural marks.** The chronological owner savepoint stack includes
  internal exception marks for participant replay; SQL name lookup skips them.
  Statement-local stage/effect restoration remains engine orchestration.
- **Slot rules.** A nil slot means "none". Owners store and recover a slot with
  one checked helper each and never store a typed nil. A slot owner that writes
  in place (the index journal) deep-copies in `Clone`; typedef slots are
  replaced before a write.
- **Root slot allocation.** A session with its payload and owner extension
  (`sessionAllocation`), and an operation with its binding host, are one
  allocation each; their slots are set before the value is shared and never
  change. The replay slot is allocated on first write, the exclusion mark only
  for nonzero state.
- **Fence order.** Publication checks fences by kind at fixed points, each kind
  in recording order, so the first error does not depend on storage.
- **UUID v1.** A private leaf mutex couples the fallback node, microsecond
  bucket, 100 ns slot and sequence. No other mutex is held over entropy or
  clock waits, and slot waits release it and recapture state. Failed or
  canceled draws commit nothing; Rollback, Reset and Restore never rewind it.

## What stays in the engine

The statement scope and dispatch, and all orchestration across catalog, storage
and visibility: begin, commit, rollback and CHAIN (`transaction*.go`),
publication, preflight and 2PC, savepoints (`savepoints.go`), session start,
settings application (`setting_apply.go`, `discard.go`), the `Engine.mu`
windows of notification publication and polling, the `…Locked` journal
wrappers, the root fence types and the fence check order, and the root slot
types.

## Testing

Package units cover the journal with its fences, capture and merge, notices,
SET LOCAL actions, the notification
queue, registry and publication, characteristic modes, serializable write
survival, the `lc_messages` access callback and the UUID v1 allocator. Engine
tests (in `internal/engine`) cover the host and root slots, catalog fences,
column-shape projections and settings provider capture.

```sh
make check RUN='^Test(AssignModesValidation|CharacteristicModeMapping|Journal|NoticesFilter|Notification(Queue|RegistryEndLocked|Publication)|SerializableWriteSurvives|SettingAccessAllowedCallback|SettingsLocal|UUIDOsspTime)'
make check RACE=1 RUN='^Test(SessionHost|SessionPayload|OwnerExtension|ExclusionMark|Operation(AnalysisSlot|ReplaySlot)|UUIDOsspTimeConcurrent$$)'
make check RUN='^Test(CatalogFences|EnumProvenanceSavepoints|AlterAddColumn(Projection|ZeroWidthProjection|CancellationDuringShapeTraversal|NonvolatileDemandOnce|PrivateSnapshotEqualWidthProjection)$$)'
make check RUN='^Test(PGSettings(ProviderCapture|CoherentCapture|CatalogACLRecheck)|RowSecurity(SettingProvenance|ProviderLifecycle))$$'
```
