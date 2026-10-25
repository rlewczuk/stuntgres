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
  and membership, and `PublicationOperationLocked` adds the context check
  under `Session.Mu` for publication. `Operation` carries contexts, command budgets, lock timeouts,
  notices, the copy-input context, the statement lease and the replay and
  analysis slots; `Session.AcquireCommand` admits commands. Notice levels,
  emission and the relation skip notices (`ExistingRelation`,
  `MissingRelation`) are in `notices.go`.
- **Transaction owner** (`owner.go`, `savepoint_marks.go`, `protocol_cycle.go`):
  `TransactionOwner` with its state, characteristics, admission and journal;
  `TxState`, `Isolation`, `TxOptions`, `SavepointMark`, `ProtocolCycle`.
  Owner transitions (`owner_transitions.go`) edit only owner fields and the
  settings store's characteristic projections: `BeginLocked` (begin defaults),
  `Clear`, `Abort`, `ChainModes` and `InstallChain` (AND CHAIN),
  `PrepareRefusal` and `PreparedEntry`. Savepoint marks: `CaptureSavepoint`
  completes a mark the root seeded with its lock checkpoint, cursor ordinal and
  exclusion slot; `FindSavepoint` skips internal marks; `RollbackToLocked`
  restores owner state and settings (`SettingsStore.RestoreSavepointLocked`).
  The root runs its cursor, portal, extension, exclusion and SSI work around
  them, payload first. Checked
  handles (`owner_handles.go`) recover owners from the `ids` interfaces;
  `AssignModes`, `CharacteristicMode` and `SessionCharacteristicSetting` decide
  transaction modes and their session defaults
  (`transaction_characteristics.go`); `SerializableWriteSurvives` tells rollback
  which serializable write evidence survives.
  Stable isolation retains immutable tuple roots, the commit frontier, sequence
  catalog and public transaction snapshot as one acquisition on this owner.
  Snapshot-requiring analysis inside the transaction can establish that pair;
  preparing before BEGIN cannot retain it into the later transaction. RC analysis
  does not retain execution authority. Command-local pairs belong to
  `StatementScope`; cursors retain their producer's pair across fresh FETCH
  admissions. Root lifecycle cleanup releases the owner pair at transaction end.
  `RetainsTransaction` identifies message/protocol retention, not `TxActive`;
  root installers must account for ordinary explicit owners separately.
  `SnapshotSet` also records that RC execution has acquired a snapshot, closing
  the transaction-characteristics window without retaining a transaction pair.
- **2PC registry** (`prepared_registry.go`): `PreparedRegistry` of
  `PreparedTransaction` entries by GID, under `Engine.Mu`, with `Lookup`,
  `Len`, `All`, `ReserveLocked` (before the callback-free install),
  `InstallLocked`, `RemoveLocked`, `ClaimLocked`/`ReleaseLocked` and
  `PublicationPreflightLocked` over a `PreparedPublication`.
  `PreparedTransaction.CheckActor` takes the roles as an `authorization.Read`
  value; the claim shares its permission check.
- **Settings store** (`settings_store.go`): `SettingsStore` (values, defaults,
  provider provenance, SET LOCAL and role setting actions, characteristic
  projections) and `SettingsMark`. Default providers run once at physical
  startup, never for metadata reads or RESET.
  Effective defaults use the registry's cached `DefaultValue`, separately from
  nullable boot/reset metadata. Startup-only provider values must equal that
  effective default; identical supplied values retain provider provenance.
  The trusted native SessionDefaultProvider remains a pure typed capture seam:
  AM/tablespace/FTS catalog checks apply to explicit SQL candidates, not provider
  initialization or restoring captured defaults. A captured simulated name is
  not proof of a catalog object. No provider callback runs on RESET/DEFAULT.
  The settings cores (`Presentation`, `LookupDefinition`, `ParseValue`, `SetInput`,
  `SettingAccess`) parse and check without installing. Under the root's
  install window, `AdmitLocked` checks the operation and attachment;
  `ApplyLocked`, `ResetAllLocked` and `ShowAllRowsLocked` (`setting_install.go`)
  install, reset and list values; `CurrentSettingLocked` and
  `SetConfigInputLocked` (`setting_calls.go`) decode the setting-function
  arguments. Individual reads take explicit restricted-read authority captured
  by the engine from effective CurrentUser and INHERIT membership. `SettingReadAccess`
  rejects restricted individual reads; `ShowAllRowsLocked` omits their rows.
  Prepared execution receives fresh authority, never a prepare-time capability.
  Each takes the `settings.Registry` and domain as values. DISCARD
  ALL's identity reset (`discard_role_reset.go`): `DiscardRoleReset` computes
  it, `ValidateRoleResetLocked` checks it against an `authorization.Read`, and
  `Operation.FinishDiscardRoleResetLocked` installs it after publication. The
  root keeps the lock windows and the authorization behind `SettingAccess`'s
  `allowed` callback.
  Eligible selected privileged builtins use effective superuser/parameter SET
  authority; custom Privileged definitions and log_statement_stats stay
  deny-only. Restricted reads remain independent of parameter SET grants.
  `lo_compat_privileges` uses the same SET authority and unrestricted reads.
  Builtin-only Sighup refuses live mutation before parsing or ACL callbacks;
  providers may capture only its normalized initial value. RESET ALL skips
  Sighup along with startup-only settings.
  SET list flattening distinguishes raw keyword lists from quote-reconstructed
  tablespace/preload lists. Catalog-dependent client setting checks run in the
  root before the atomic install window, not under session locks. Simulated
  replication/check-function settings never weaken FK/final routine validation.
  RESET ALL resets previously assigned eligible privileged builtins without
  rechecking SET grants (including after revoke); individual RESET still
  checks current SET authority. The same setting action stack makes that reset
  transactional. Startup and deny-only definitions remain excluded.
- **Statement classification** (pure functions of the AST and owner values):
  command tags (`command_tag.go`); admission predicates and refusals
  (`statement_class.go`: `ModifyingTag`, `ExplicitUtility`, `ReadOnlyRefusal`,
  `ExplicitUtilityRefusal`, `TransactionalSchemaUtility`); snapshot, replacement-read and Describe classes
  (`statement_phases.go`); and the transaction-control table
  (`transaction_control.go`: `ClassifyControl` returns a `ControlStep`, plus
  `AbortedControlAllowed`, `SavepointOutsideBlockError` and the shared
  `AbortedError` and `NoTransactionWarning`). For COMMENT, `ExplicitUtility`
  admits only the enumerated shipped kinds; unknown kinds and unrelated
  utility exclusions remain intact. `TransactionalSchemaUtility` in
  `statement_class.go` is the shared bounded Describe/Execute admission policy:
  permanent CREATE/DROP TABLE, CTAS/SELECT INTO, CREATE/ALTER/DROP SCHEMA, shipped
  ALTER TABLE forms, CREATE/OR REPLACE/ALTER/DROP RULE, trigger
  CREATE/OR REPLACE/ALTER/DROP, CREATE/OR REPLACE/ALTER/DROP VIEW,
  CREATE/ALTER/DROP MATERIALIZED VIEW plus ordinary/concurrent REFRESH,
  CREATE/ALTER/DROP POLICY (including rename), CREATE DOMAIN/composite TYPE,
  ALTER DOMAIN/composite attributes, type/domain rename, schema move and DROP,
  CREATE/DROP ACCESS METHOD, CREATE/ALTER/DROP OPERATOR FAMILY and OPERATOR CLASS,
  shipped function creation, routine identity ALTER/DROP, aggregate creation,
  operator CREATE/ALTER/DROP, CREATE/ALTER/DROP COLLATION and CONVERSION, and
  CREATE/DROP CAST use the existing transaction journal, as do generic
  table/type/domain/database OWNER and REASSIGN/DROP OWNED. Definition utilities
  keep quiet Describe; Execute consumes current private providers and authority,
  with the existing Finish, savepoint and retained-message lifecycle. Feature-specific
  view/materialized OWNER is AlterView; collation/conversion OWNER is included.
  CreateFunction admits only non-procedure declarations; procedure creation and
  language lifecycle keep their separate admission. Unrelated utility exclusions
  retain their independent policies. Collation/conversion/cast Describe is
  syntax-only: lookup, authorization, locks, provider checks and notices belong
  to Execute. Execute's collation preparation can reject or skip before catalog
  ShareRowExclusive admission; protected cross-encoding/provider/insertion work
  follows admission, not Describe. An ordinary native `ErrCatalogReplaced` from
  definition identity admission aborts the explicit transaction and clears its
  savepoints: full rollback and a fresh same-owner transaction are required,
  unlike ordinary semantic errors recoverable with ROLLBACK TO.
  The root reads state, InBlock and ReadOnly under `t.Mu` and keeps its locks,
  preflights, short-circuits (`partitionIndexTransaction`,
  `transactionalIndexMaintenance`) and call order.
  Supported object GRANT/REVOKE and ALTER DEFAULT PRIVILEGES use the canonical
  transaction path and syntax-only utility Describe classification. Execution
  keeps read-only/aborted-state precedence and current identity checks. These
  directives do not widen existing role lifecycle/membership, ownership or
  ALTER SYSTEM policies; SQL PREPARE of these utilities remains excluded.
- **Protocol codec** (`protocol_values.go`, `protocol_statement.go`): the wire
  value types `ProtocolInput`, `ProtocolLogParameter`, `ProtocolContext` and
  `ProtocolField`; format decoding (`ProtocolFormats`,
  `ProtocolResultFormats`); `ImportProtocolParameters`, which decodes Bind and
  fastpath parameters from the operation context, output environment and
  formal types it is given; `EncodeProtocolRow`, run once per result row with
  reused buffers; and the pure statement checks `PreparableBody`,
  `ProtocolPlannedStatement`, `ProtocolPlainSelect`, `ProtocolHasRows` and
  `ProtocolParameterOIDs`. It takes no lock; the root calls it with nothing
  held.
- **Timeouts** (`timeout.go`): transaction, idle-in-transaction and idle session
  budgets and timers.
- **LISTEN/NOTIFY** (`notification_*.go`): records, persistent listener FIFO,
  immutable global page positions, `NotificationListener` (in the root payload),
  `NotificationRegistry` and publication (`CaptureLocked`, unlocked `Prepare`,
  `OldestPrepared`, `UnchangedLocked`, `InstallLocked`). Every mutable field but
  the listener's `Used` hint belongs to the cluster-shared `Engine.Mu`, which the
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
  wrappers pass the published base under `Engine.Mu`.
  Object/default ACLs, role mutations and comments share the immutable role
  overlay. The first actual role delta captures its committed predecessor as
  `RolesBase`; later private deltas preserve that base until a successful immutable
  read/publication refresh advances both base and candidate on a cloned journal.
  Actual-role and mixed journals keep the root's explicit merge and exact touched
  descriptor/name/membership predecessor checks; disjoint peer changes merge
  without loss, while older private roots and marks remain unchanged.
  ACL-only revision changes preserve their immutable authority origin; root
  publication checks and rebases their revision delta without conflicting with
  independent ACL targets. General access stamps stay exact. `RolesBase` describes
  actual private role writes, not a blanket committed-root identity veto or
  authorization consulted by a completed catalog statement; execution authority
  ends at Finish. DROP OWNED's nonnil cleanup candidate is checked against the
  current private role predecessor before joining `Journal.Roles`; a nil candidate
  preserves pending roles and their base without inventing a new role-write fence.
  Pending query-derived rows and materialized replacements separately retain a
  root-owned role-read fence until publication. Journal clones and savepoint marks
  retain or discard that fence with its producer, not as a role overlay. Savepoints
  also restore the private role root, retirement obligations, session identity,
  descriptors and lock marks.
  `StorageChange.MergeFills` merges added-column fills and drops those of
  removed attributes, replacing the shared fill map instead of mutating it.
  `RecordRowShapes` and `RecordMaterializedRefresh` (`journal_storage.go`)
  record a staged catalog change's heap replacements and materialized refresh
  as storage changes, keeping the first predecessor; the root supplies the
  committed-store check and the commit sequence.
- **Catalog authority** (`catalog_authority.go`): `CatalogAuthority`, a
  statement's entry authority (authorization and role revisions, resolved
  object, database predecessor, consulted providers), and its
  `PreflightLocked` fence over the committed catalog, the session and
  published role records and the database address. It expires at the
  statement's successful Finish, never at the outer Commit. The root captures
  it from its authorization view and passes those values under `Engine.Mu`
  and the session mutex; it takes no lock.
- **Journal capture** (`journal_capture.go`): `JournalCapture` turns a
  validated stage's `catalog.Candidate` into the retained transaction's
  observations and fences, over the published and private catalog states:
  `ObserveDatabase`, `Observe`, `ObserveBase`, `ObserveCandidate` and
  `FenceNames`. The root calls them between its own domain,
  materialized-view, index and storage steps, which fixes the recording
  order and the first error.
- **Storage projections** (`storage_projection.go`): column-shape and private
  snapshot projections that read only catalog, storage and visibility records.
- **Statement data views** (`data_view.go`, `snapshot_roots.go`):
  `DataViewInput` with `ValidateView` (an already captured view still covers
  the refs) and `Capture` (a new immutable `visibility.View`: RC target roots,
  stable owner roots with materialized and TRUNCATE lineage, projected
  snapshots, dynamic pgrowlocks roots, own writes and deletes);
  `PrivateStorage`, `CommittedRoots` and
  `TransactionOwner.NeedsSnapshotProjection`. The root fills the input under
  `Engine.Mu` after its epoch checks and snapshot step and passes it by
  pointer; `SameLineage` and `SameRowShape` are static func fields because
  `session` imports neither `view` nor `index`.
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
  never takes `Engine.Mu`; it reaches the engine only through `Host`.
- **No orchestration.** The transaction owner holds the journal; statement
  admission serializes writers, and the root validates and publishes it under
  `Engine.Mu`. The journal itself takes no lock.
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
and visibility: the `Engine.Mu` windows of begin, commit, rollback and CHAIN
(`transaction*.go`), of PREPARE and COMMIT/ROLLBACK PREPARED with the role read,
SSI and lock-manager calls (`transaction_prepared.go`), and of savepoints
(`savepoints.go`, with `restoreSavepointLocked` shared by ROLLBACK TO and
failed-statement or PL/pgSQL rollback), publication and preflight, session start,
the `Engine.Mu` → `Settings.Mu` → `Session.Mu` windows of settings application,
setting calls and DISCARD (`setting_apply.go`, `setting_calls.go`,
`discard.go`), the data-view shell (`transaction_visibility.go`: epoch
checks, `visibilitySnapshotLocked`, the pgrowlocks dependency flag and storing
the view), the protocol Parse,
Bind, Describe, Execute and fastpath steps (`protocol_*.go`, with
`ProtocolStatement`, `ProtocolPortalDescription` and `ProtocolExecution`,
which carry `StatementDescription`), the `Engine.Mu`
windows of notification publication and polling, the `…Locked` journal
wrappers, the root fence types and the fence check order, and the root slot
types.

## Testing

Package units cover the journal with its fences, capture and merge, notices,
SET LOCAL actions, RESET ALL and SHOW ALL, the 2PC registry's claim and preflight, CHAIN modes and
PREPARE refusals, the notification
queue, registry and publication, characteristic modes and session
characteristic settings, serializable write
survival, the `lc_messages` access callback, the UUID v1 allocator, the
protocol codec, statement classification and statement data views. Engine
tests (in `internal/engine`) cover the host and root slots, catalog fences,
column-shape projections, data-view and snapshot capture, and settings
provider capture. Metadata unit fixtures capture validated operation authority
before reading settings; a zero or incomplete role view is not a visibility grant.

```sh
make check RUN='^Test(AssignModesValidation|CharacteristicModeMapping|Journal|NoticesFilter|Notification(Queue|RegistryEndLocked|Publication)|Owner(ChainModes|PrepareRefusalOrder)|PreparedRegistry|ProtocolCodec|ClassifyControl|AbortedControlAllowed|ReadOnlyRefusal|ExplicitUtilityRefusal|TransactionalSchemaAdmission|StatementPhases|DataView|PrivateStorage|CommittedRoots|NeedsSnapshotProjection|SerializableWriteSurvives|SettingAccessAllowedCallback|Settings(Local|ResetAllLocked|ShowAllRowsLocked)|SessionCharacteristicSetting|UUIDOsspTime)'
make check RACE=1 RUN='^Test(SessionHost|SessionPayload|OwnerExtension|ExclusionMark|Operation(AnalysisSlot|ReplaySlot)|UUIDOsspTimeConcurrent$$)'
make check RUN='^Test(CatalogFences|TransactionalPrivileges(Finish|Pending)|RoleOwnershipD|EnumProvenanceSavepoints|AlterAddColumn(Projection|ZeroWidthProjection|CancellationDuringShapeTraversal|NonvolatileDemandOnce|PrivateSnapshotEqualWidthProjection)$$)'
make check RACE=1 RUN='^Test(PgrowlocksEligibility|SnapshotStatement(Retention|RootConsistency)|SnapshotBinding|SerializableSafeSnapshot)'
make check RUN='^Test(PGSettings(ProviderCapture|CoherentCapture|CatalogACLRecheck)|RowSecurity(SettingProvenance|ProviderLifecycle))$$'
```
