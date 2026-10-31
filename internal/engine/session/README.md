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
`lock`, `plan`, `settings`, `storage`, `visibility`, `access`, `sqltype`, `sqlstate`,
`parser`), never `analyze`, a feature package (`routine`, `typedef`,
`extension`, `sequence`, ...) or the root: feature packages import `session` to
implement its slots, so either import would form a cycle.

`Journal.Statistics` contains immutable selected-column and selected-definition
deltas keyed by relation, stable attribute or generation-qualified statistics
object identity, and inherited flavor. `Clone` copies the outer map while sharing
immutable entries; a new collection replaces the delta, so savepoints and
prepared transactions retain their own prior roots. `ResolveStatistics` overlays
these facts on the existing Store's canonical `RelationMetadata` root and filters
incompatible usages, definitions and current hierarchy authority.
New-object maintenance is retained privately beside
the same selected deltas until creation commits; existing-object maintenance
is not rolled back. Descriptor/epoch and optional real-heap identities fence
publication, not metadata-pointer changes caused by a peer's compatible refresh.
This is not a second committed store or a row rewrite.
Selected-deletion tombstones remain in the immutable delta until commit applies
them to the newest canonical root, including structural inherited-flavor removal.
`StorageChange.TruncateScanReset` records actual truncate scan invalidation
separately from the pure-lineage `Truncated` flag. Compatible successors carry
the actual private heap's post-truncate scan witness; savepoints/prepared owners
retain that physical maintenance authority without making column facts
nontransactional.
Imported relation fields use the same journal with `ImportedReset` field masks
and surviving later `Imported` writes. Semantic new-file staging discards earlier
writes for reset fields and records defaults only for currently present imports;
no-import operations do not manufacture overrides of native maintenance.
Owner reads and commit remove reset canonical bits before merging later writes.
`ImportedStorage` is the opaque last-new-file token, not the latest heap pointer.
Compared with the innermost retained subtransaction entry, it distinguishes a
new-file TRUNCATE from an in-place one that preserves statistics. Creation uses
entry catalog absence; compatible heap transports and ordinary writes do not
advance the token. RELEASE, rollback, internal statement marks and prepared
owners preserve/reclassify the same immutable journal state. No token is
published to canonical metadata or native snapshots, and historical storage
flags never erase later restore/clear.
`ImportedAnalyzeCount` borrows the existing canonical ANALYZE count as write
provenance. Successful maintenance survives savepoint rollback, so old saved
tuple writes (also foreign pages) are ignored when that baseline has changed.
Actual restore/reset filters old writes before refreshing its baseline, including
unrelated page/visibility updates. No new canonical counter is introduced.


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
  New operation contexts suspend inherited workload and closed-pause activation
  before ordinary cancellation derivation. Reuse of an active callback context
  on another connection cannot borrow its runtime owner or block metadata capture.
  Both attached requests remain dormant until the new operation's own detached
  runtime; `SourceContext` retains the exact caller context and cause/deadline
  authority.
  `Operation.OriginalSQL` and `SQLAvailable` retain the command-admitted immutable
  frontend text, not a statement substring or mutable session setting. A simple
  batch shares it through every statement; prepared execution installs its
  original source. Nested execution keeps the outer command, while source-less
  internal operations remain explicitly unavailable. Source lives until Done and
  is never copied into a retained cursor or prepared expression.
  `XMLInput` copies literal/parameter provenance into mapped XML errors and
  warnings for the active operation. `OutputNotices` retains only a handler and
  captured filtering threshold; a fresh `OutputNoticeCollector` records actual
  typed output before unlocked delivery, including warnings before later errors.
  Retained rows keep sink-free output presentation, never a completed operation.
  XMLInput and FTSInput can value-specialize their notice sink for a parallel
  evaluator while preserving literal/parameter provenance. Such sinks collect
  privately; the aggregate coordinator alone drains them after join, including
  failure/cancellation. Outside-gate evaluation never appends directly to
  Operation.Notices; the sole gated SQL runner retains ordinary notice delivery.
  The complete capture also specializes regobj type-name and ACL-input callbacks;
  ACL INITCOND decoding outside the SQL gate must retain authority and provenance
  while queueing privately. XML/FTS adapters give their private sink precedence;
  ordinary unspecialized adapters still deliver through their live operation.
  Command admission captures typed CPU/parallel planner costs, statistics target,
  constraint-exclusion mode, cursor fraction and recursive-worktable factor under
  the existing settings mutex alongside the worker ceiling. Consumers receive
  detached scalars, not a settings provider. Changes affect the next command,
  not the running collection/proof or a retained cursor-source decision.
- **Client encoding** (`client_encoding.go`): one lazily allocated fixed-ID
  cache per physical session retains only the newest successfully accepted
  immutable pair for each released encoding. Effective state and journal,
  SAVE, default and invocation images retain the typed encoding ID, not a
  frozen pair. Detached invocation stores share the physical cache authority.
  Restoring an ID selects its newest accepted pair even after catalog
  retirement; a new explicit assignment must still pass fresh core admission.
  Presentation environments copy capabilities, never a settings callback or
  catalog graph. Native environments leave byte transport disabled.
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
  transaction modes and their session defaults. `CheckModes` shares the same
  evaluation without assignment, including caller-owned utility snapshots;
  callers serialize owner inspection through admission or the owner mutex
  (`transaction_characteristics.go`). `SerializableWriteSurvives` tells rollback
  which serializable write evidence survives.
  The same owner holds the TEMP tablespace selector: immutable builtin OIDs,
  assignment identity, transaction identity and a circular cursor, serialized by
  statement admission. Failed creation consumes a destination; unchanged savepoint
  restoration does not rewind it. The settings journal retains only immutable
  assignment tokens, with new identities for detached populated policies.
  Untouched empty settings preserve their nil journal image; retained nonempty
  text has an unprepared identity, never fabricated accepted OIDs. Changed
  restoration invalidates the selector; the host lazily prepares retained values
  once per transaction/assignment using current coherent authority. Clear/abort
  retire the cursor without changing physical-session reset semantics.
  Stable isolation retains immutable tuple roots, the commit frontier, sequence
  catalog and public transaction snapshot as one acquisition on this owner.
  Supporting unique index roots retain only immutable descriptor/root pointers
  alongside that original tuple frontier. FK crosschecks probe these originals
  to detect snapshot/current disagreement; current roots remain integrity
  authority. Later-created indexes never enter an existing snapshot, and live
  index publication, savepoint copies and row projections never mutate its map.
  Snapshot-requiring analysis inside the transaction can establish that pair;
  preparing before BEGIN cannot retain it into the later transaction. RC analysis
  does not retain execution authority. Command-local pairs belong to
  `StatementScope`; cursors retain their producer's pair across fresh FETCH
  admissions. Root lifecycle cleanup releases the owner pair at transaction end.
  `RetainsTransaction` identifies message/protocol retention, not `TxActive`;
  root installers must account for ordinary explicit owners separately.
  Retention is not CALL/DO atomicity: core captures explicit/multi-message and
  prior-Execute block context before the current protocol command mutates flags.
  PL segment rotation reuses this owner with a fresh implicit identity/ticket,
  settles old settings normally, and preserves only CHAIN characteristics when
  requested. It does not acquire a second operation or refresh statement budget.
  `SnapshotSet` also records that RC execution has acquired a snapshot, closing
  the transaction-characteristics window without retaining a transaction pair.
- **2PC registry** (`prepared_registry.go`): `PreparedRegistry` of
  `PreparedTransaction` entries by GID, under `Engine.Mu`, with `Lookup`,
  `Len`, `All`, `ReserveLocked` (before the callback-free install),
  `InstallLocked`, `RemoveLocked`, `ClaimLocked`/`ReleaseLocked` and
  `PublicationPreflightLocked` over a `PreparedPublication`.
  `PreparedTransaction.CheckActor` takes the roles as an `authorization.Read`
  value; the claim shares its permission check.
- **Settings store** (`settings_store.go`, `routine_settings.go`, `settings_defaults.go`):
  `SettingsStore` owns typed/text values and defaults, captured reset sources,
  provider provenance, role actions, characteristic projections and one explicit
  SET/LOCAL/SAVE journal. Routine SAVE levels capture only touched variables,
  with immutable per-variable tails and masked SET candidates; no whole-store
  snapshot is used. SAVE restores entry values, LOCAL remains confined, and
  ordinary SET merges outward while retaining transaction rollback authority.
  Nested SET/LOCAL merges retain the effective LOCAL until transaction completion.
  `SettingsMark` retains action state and nesting for exception/savepoint rollback.
  The typed role action and mark retain the complete current-ref/active/text
  triple, including masked SET candidates and active-only self/NONE changes.
  Restoration uses captured refs/text without name or membership rechecks.
  The physical `RoleState` retains its startup reset ref/text pair independently
  of effective invocation actors; DISCARD restores that same pair.
  Physical startup selects committed global, database, login-role and
  database/login defaults before callbacks. Present provider responses win per
  parameter, including equal values; providers run once at physical startup,
  never for metadata reads or RESET. Provider-less startup shares immutable
  registry reset maps until the first ordinary persisted default detaches them;
  provider-owned maps need no copy. Persisted custom placeholders are captured
  too. Typed reset values and their `DefaultSource` remain frozen across
  transactions, detached invocation images, exception marks, pool reuse and
  logical replacement; EndSession releases them.
  Command admission captures `plan.PlannerMethods` under the effective store's
  mutex. Disabled bits make detached zero values preserve enabled algorithms;
  the value retains no settings owner. Core separately captures effective
  detached routine settings at each nested SQL admission. Async and parallel
  permissions retain their separate operation admission facts.
  `SessionStart.NoticeHandler` transports startup diagnostics only after the
  coherent startup image is complete and all runtime locks are released.
  Effective defaults use the registry's cached `DefaultValue`, separately from
  nullable boot/reset metadata. Startup-only provider values must equal that
  effective default; identical supplied values retain provider provenance.
  The trusted native SessionDefaultProvider remains a pure typed capture seam:
  AM/tablespace/FTS catalog checks apply to explicit SQL candidates, not provider
  initialization or restoring captured defaults. A captured simulated name is
  not proof of a catalog object. No provider callback runs on RESET/DEFAULT.
  Internal transport providers may supply an already admitted immutable typed
  default through `SessionDefaultValueProvider`. Capture retains that value and
  provider provenance without reparsing display text; ordinary/native string
  providers still pass through the definition parser. All callbacks remain
  outside runtime locks.
  Presentation captures `xmloption` under this same mutex; current execution
  parameters/text casts use that mode, while accepted XML literals retain theirs.
  The settings cores (`Presentation`, `LookupDefinition`, `ParseValue`, `SetInput`,
  `SettingAccess`, `ValidateValueLocked`) parse and check without installing. The
  engine's persisted test path reuses them without changing the issuer. Under the root's
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
  Eligible selected privileged builtins, including all four performance-statistics
  flags, use effective superuser/parameter SET authority; custom Privileged
  definitions stay deny-only. Restricted reads are independent of SET grants.
  Explicit true statistics assignments check effective typed peers after admission
  and before Touch/install. Cached RESET/DEFAULT/NULL, RESET ALL and transaction
  restoration bypass those hooks, including when captured defaults conflict with
  current overrides. Stage flags can coexist; statement statistics cannot be
  explicitly enabled alongside them. These flags have no logging consumer.
  A session assignment of a cached default subsequently masked by SET LOCAL
  retains the exact effective typed default as its commit candidate. Committed
  masked restoration has session source; rollback/savepoint originals and the
  captured default remain unchanged. A later explicit RESET restores reset source.
  `lo_compat_privileges` uses the same SET authority and unrestricted reads.
  Builtin-only Sighup refuses live mutation before parsing or ACL callbacks;
  providers may capture only its normalized initial value. Backend and
  SuperuserBackend likewise refuse all live writes, but the trusted native
  provider may capture changed typed defaults once at physical startup.
  This does not authenticate client startup options or sleep for delay values.
  RESET ALL skips every immutable context regardless of parameter grants.
  Logging uses the same owner: only log_connections/log_destination flatten
  raw identifier lists; scoped mutable superuser settings use parameter SET
  authority. log_directory/log_filename read restrictions are independent.
  Their validated settings have no physical logging or presentation consumers.
  Resource settings add two grant-eligible privileged names, max_stack_depth
  and temp_file_limit; all44 reads are unrestricted. One private
  `SettingsStore.temporaryBuffers` count, accessed atomically, captures the
  effective typed temp_buffers value once at successful owned logical TEMP
  table access. Detached restricted-owner settings and exception checkpoints
  share that physical-session authority, not a snapshot of its count: local
  values/actions stay isolated, but first access in a nested invocation freezes
  the physical count even if its local settings later roll back. It is not the
  transaction's AccessedTemporary footprint or a journal action. Explicit
  differing candidates fail through the ordinary validation before Touch/install;
  reset/default/NULL and transactional restoration bypass that check.
  Rollback, savepoints, DROP/recreate, DISCARD TEMP, logical Reset/Restore and
  pool reuse do not clear it. EndSession clears it. Providers run only before
  TEMP use and retain their ordinary pure parsing/immutable-default contract.
  SET list flattening distinguishes raw keyword lists from quote-reconstructed
  tablespace/preload lists. Catalog-dependent client setting checks run in the
  root before the atomic install window, not under session locks.
  session_replication_role controls ordinary rewrite-rule selection and
  invalidates session prepared analysis on effective changes, including SAVE,
  savepoint/transaction and detached-invocation restoration. The shared plan
  generation preflight reserves capacity for pending infallible restorations;
  installation/restoration does not invoke cache callbacks under settings locks.
  It does not implement replication transport or weaken FK/routine validation.
  RESET ALL resets previously assigned eligible privileged builtins without
  rechecking SET grants (including after revoke); individual RESET still
  checks current SET authority. The same setting action stack makes that reset
  transactional. Immutable and deny-only definitions remain excluded.
  Replication shares the store: wal_sender_timeout is user-settable and
  output_plugin_libraries uses parameter SET authority independently of its
  restricted reads. primary_conninfo also restricts reads. Plugin SQL SET
  reconstructs quoted identifier lists; synchronous_standby_names and
  synchronized_standby_slots flatten raw lists. No physical replication or
  plugin-loading consumer is installed by setting capture or mutation.
  WAL settings share these same grants, typed capture and journal actions;
  commit_delay, wal_compression, wal_init_zero and wal_recycle are eligible
  privileged builtins. The fixed archive_mode=off profile presents only the
  current archive_command as `(disabled)`, preserving empty stored defaults.
  No WAL, compressor, file or commit-delay consumer runs here.
  set_config validates SET/context/value authority before checking permission
  to examine its result, and preflights that read before atomic installation.
  A denied result therefore cannot leave a changed setting or source behind.
  Restricted invocations use `Operation.EffectiveSettings`, a lazily detached
  full image inherited from the parent invocation. Local installs preserve
  normal validation/callback staging but never publish session lifetime timers
  or mutate caller identity, settings actions, placeholders or modes.
  `EffectiveReadOnly` and `AssignInvocationModes` retain ordinary admission
  against real snapshot/subtransaction facts while changing only local mode
  projections. Internal exception marks retain the complete invocation image
  and initial-path marker; data/catalog rollback is independent of session
  setting restoration.
- **Local-userid routine environment** (`effective_user.go`): an operation-local
  `EffectiveUserInvocation` stack selects a definer's live owner without changing
  SET ROLE, authenticated/session identity or the effective settings store.
  Invokers inherit it. It is distinct from `RestrictedInvocation`; nesting keeps
  maintenance restrictions and the most recently entered actor's precedence.
  Core owns balanced entry, authority/presentation refresh and unwinding.
- **Statement classification** (pure functions of the AST and owner values):
  command tags (`command_tag.go`), including non-counted ALTER STATISTICS targets;
  ALTER DATABASE defaults are modifying, non-counted transactional utilities,
  excluded from event-trigger dispatch independently of their completion tag.
  SECURITY LABEL is modifying and non-counted, with DescribeOperation before
  snapshots. It is not a SQL PREPARE body, read-only exemption or aborted-control
  command. Healthy read-write transactions reach provider resolution without a
  provider-state journal or standalone-utility refusal; read-only and failed-state
  admission precede callbacks and the execution-time absent-provider error.
  EventCommand admits SECURITY LABEL for its database-local target families
  and subscription, excluding database/tablespace/role/event-trigger targets.
  The existing event frame invokes command-start before provider resolution,
  retains callback writes atomically, and suppresses command-end on failure.
  With zero installed providers it needs no target snapshot, relation lock
  or replacement fence.
  admission predicates and refusals
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
  CREATE/DROP CAST and CREATE/ALTER/DROP LANGUAGE use the existing transaction
  journal, as do generic table/type/domain/database/large-object OWNER and
  REASSIGN/DROP OWNED. Definition utilities
  keep quiet Describe; Execute consumes current private providers and authority,
  with the existing Finish, savepoint and retained-message lifecycle. Feature-specific
  view/materialized OWNER is AlterView; collation/conversion OWNER is included.
  CreateFunction admits only non-procedure declarations; procedure creation keeps
  its separate admission. Unrelated utility exclusions retain their independent
  policies. Collation/conversion/cast Describe is
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
  `EarlyStandaloneUtility` also places CONCURRENTLY detach's block refusal before
  relation admission. FINALIZE remains an ordinary transactional schema utility.
  Concurrent detach uses independent publication phases, not the lexical event
  utility's one journal, following concurrent-index ownership.
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
  CALL takes the ordinary statement snapshot and catalog replacement fence,
  with a producer-free analyzed Describe and a non-counted CALL completion tag.
  It is not event DDL; procedure CREATE keeps its existing utility admission.
  EXPLAIN is an extended-protocol planned, row-bearing utility with a
  non-counted EXPLAIN completion. It is not a SQL PREPARE body and never enters
  the child command's execution fold group. Classification keeps the wrapper
  nonmodifying even when its child writes or requests row locks; ordinary child
  identity and semantic privilege admission still applies.
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
  originals, default and historical initial ACLs, sequence generators, private storage changes,
  dependency observations, role overlays, fences and feature slots), with
  `Clone`, `AddFence`, `PrivateRead`, `RecordDescriptor`, `RecordDefaultACL`,
  `RecordCandidateDefaults` and `MergeCandidate` (a `catalog.Candidate`
  checked and recorded against the published base). The root's `…Locked`
  wrappers pass the published base under `Engine.Mu`.
  Initial ACL deltas, predecessor observations and tombstones participate in
  private-read caching, savepoint clones and publication even without descriptor
  changes. `RecordCandidateInitialPrivileges` retains the first committed
  predecessor; `JournalCapture` also observes the represented object's descriptor.
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
  The existing stable owner's `SequenceCatalog` is shared with the core's narrow
  pending-detach eligibility accessor. It does not become general name-resolution
  or permission authority, and no second snapshot catalog is retained.
- **Fences** (`fence.go`, `journal_fences.go`): `Fence` with `Kind()`, and
  three session kinds with their fences: `FenceStorageRead`
  (`StorageReadFence`), `FenceIndexSet` (`IndexSetFence`) and `FenceNamespace`
  (`NamespaceFence`). They implement `CommittedFence`, whose `Current` reads
  only the published catalog and storage relations. The root declares its own
  kinds from `FenceRootKinds` on, and the order in which publication checks
  the kinds.
  `NewStorageIdentityFence` is the narrow validity-only scan capture: it retains
  heap pointer identity while permitting enforced DML tuple/generation evolution.
  `NewStorageReadFence` retains its strict length/Ends semantics; independent
  prior shape/read fences are never removed or relaxed.
- **Slots**: journal slots (`journal_slots.go`: `EnumSlot`, `DomainSlot`,
  `IndexSlot`, implemented by `typedef` and `index`) and root slots
  (`root_slots.go`: `PayloadSlot`, `OwnerSlot`, `ExclusionMarkSlot`,
  `ReplaySlot`, `AnalysisSlot`, implemented by the root and `index`). Each has
  an exported marker method; journal slots also have `Clone`.
- **Engine host** (`host.go`): `Host`, implemented by the root's `sessionHost`
  (a pointer conversion of `*Engine`). `EndMaintenance` runs after the
  Transaction resource callback returns, without `Session.Mu` and never inside a
  `SessionResource` callback. `Unregister` runs under `Session.Mu`, removes the
  exact local registration under `Engine.sessionMu`, drops that mutex, then
  releases the physical reservation under cluster AdmissionMu. It never takes
  metadata Mu. `NewOperation` (possibly under `Session.Mu`) only allocates the
  operation with its binding host.

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
which carry the prepared description owned by `core.Description`), the `Engine.Mu`
windows of notification publication and polling, the `…Locked` journal
wrappers, the root fence types and the fence check order, and the root slot
types.

## Testing

TEMP selector, immutable-assignment ownership and lifecycle units use
`RUN='^TestTempTablespaces'` from the external `enginetests` package.

New-operation workload activation/source-identity units use `RUN='^TestWorkAdmission'`.

Package units cover the journal with its fences, capture and merge, notices,
SET/LOCAL/SAVE actions, RESET ALL and SHOW ALL, the 2PC registry's claim and preflight, CHAIN modes and
PREPARE refusals, the notification
queue, registry and publication, characteristic modes and session
characteristic settings, serializable write
survival, the `lc_messages` access callback, the UUID v1 allocator, the
protocol codec, statement classification and statement data views. Engine
tests (in `internal/engine`) cover the host and root slots, catalog fences,
column-shape projections, data-view and snapshot capture, and settings
provider capture. Metadata unit fixtures capture validated operation authority
before reading settings; a zero or incomplete role view is not a visibility grant.
Routine configuration journal units live in `enginetests/` and use
`RUN='^TestRoutineConfiguration'`, also with `RACE=1`; they assert typed action
ownership, immutable checkpoint backing and transaction versus SAVE boundaries.
XML diagnostic ownership and real protocol output codec units use
`RUN='^TestXML(Input|Protocol)'`, including `RACE=1`
for detached collector/cancellation ownership.

```sh
make check RUN='^Test(AssignModesValidation|CharacteristicModeMapping|Journal|NoticesFilter|Notification(Queue|RegistryEndLocked|Publication)|Owner(ChainModes|PrepareRefusalOrder)|PreparedRegistry|ProtocolCodec|ClassifyControl|AbortedControlAllowed|ReadOnlyRefusal|ExplicitUtilityRefusal|TransactionalSchemaAdmission|StatementPhases|DataView|PrivateStorage|CommittedRoots|NeedsSnapshotProjection|SerializableWriteSurvives|SettingAccessAllowedCallback|Settings(Local|ResetAllLocked|ShowAllRowsLocked)|SessionCharacteristicSetting|UUIDOsspTime)'
make check RACE=1 RUN='^Test(SessionHost|SessionPayload|OwnerExtension|ExclusionMark|Operation(AnalysisSlot|ReplaySlot)|UUIDOsspTimeConcurrent$$)'
make check RUN='^Test(CatalogFences|TransactionalPrivileges(Finish|Pending)|RoleOwnershipD|EnumProvenanceSavepoints|AlterAddColumn(Projection|ZeroWidthProjection|CancellationDuringShapeTraversal|NonvolatileDemandOnce|PrivateSnapshotEqualWidthProjection)$$)'
make check RACE=1 RUN='^Test(PgrowlocksEligibility|SnapshotStatement(Retention|RootConsistency)|SnapshotBinding|SerializableSafeSnapshot)'
make check RUN='^Test(PGSettings(ProviderCapture|CoherentCapture|CatalogACLRecheck)|RowSecurity(SettingProvenance|ProviderLifecycle))$$'
make check RUN='^TestLoggingSettings'
make check RUN='^TestWALSettings'
make check RUN='^TestSharedSessionDefaults'
```
