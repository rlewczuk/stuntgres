# Engine split: `internal/engine/session`, fifth round

Session prompt for linear items 155, 160, 161, 162, 164 and 165 of the
[status page](ENGINE-SPLIT-STATUS.md). The rules are in
[ENGINE-SPLIT.md](ENGINE-SPLIT.md) §2, §3 and §5, and the package contract
is in [the session README](../internal/engine/session/README.md). `session`
never imports `exec`, `analyze` or a feature package, so anything those own
comes in as a value or a func field. Sizes were measured at `fe0683b68`;
re-run `reach` on the move set first.

The README's "What stays in the engine" list (begin, commit, rollback and
CHAIN, publication, preflight and 2PC, savepoints, settings application)
shrinks with each item to the `Engine.mu` windows and publication.

## Item 155 — Journal observation and state fences

Needs 153, because both edit `default_privileges_catalog.go` and the
publication fences. About 0.21k lines move.

**Moves.**
- The fence types `catalogIndexFence`, `catalogNamespaceFence`
  (`catalog_fence.go`) and `storageReadFence` (`storage_read_fence.go`),
  with one constructor for the four `storageReadFence{…}` literals.
- The observe, namespace, touched and fence parts of
  `captureCatalogStageLocked` (`catalog_message.go`). That file turns a
  validated stage into the retained transaction's `session.Journal`.
- `stageDefaultChangesLocked` (`default_privileges_catalog.go`).
- The journal merge in `dropSequences` (`sequence.go`, about L434-460).

**Cut.**
- The value inputs are the committed and private `*catalog.State`, the
  `*catalog.Candidate` and the predecessors.
- `session` declares its three fence kinds, and
  `catalogFencesCurrentLocked` gets one branch for session fences.

**Stays root.** The capture runs as 3–4 `session` calls interleaved with the
root's index, materialized-view and storage steps, so the fence recording
order and the first-error order stay the same.

**Callers and locks.** Everything runs under `Engine.mu` and takes no lock.

**Tests.**
- Root units: `driver_batch_catalog_test.go`,
  `driver_batch_catalog_provider_test.go`,
  `partition_attach_publication_test.go`,
  `ownership_database_fence_test.go`, `temporary_view_test.go`,
  `default_privileges_catalog_test.go` and the README's `CatalogFences`
  selector. Use `RACE=1`.
- Quick and `REFERENCE=1` selections: `TestReferenceRoleDefaultPrivileges`,
  `TestReferenceForeignActionsConcurrentDDL` and one RR or serializable DDL
  selection (`TestReferenceAlterTypeRepeatableRead`).

**Docs.** Replace the README's "the root declares the kinds" with the three
session kinds.

## Item 160 — Owner transitions and the 2PC registry

Needs —. About 0.2k lines move.

**Moves.**
- The `Engine.prepared` map becomes `session.PreparedRegistry`, with Locked
  methods for:
  - lookup;
  - the publication preflight (`preparedPublicationPreflightLocked`);
  - the actor check (`preparedActorLocked`);
  - the claim half of `completePrepared`;
  - the PREPARE refusal checks;
  - the install and finish edits (`transaction_prepared.go`).
- Owner transitions become `session` owner methods:
  - the owner-field reset of `clearTransaction` (`transaction.go`);
  - the abort reset in `failExplicitTransactionLocked`
    (`transaction_finish.go`);
  - the defaults in `beginLocked`;
  - the CHAIN mode choice and install from `endAdmitted`
    (`transaction_controls.go`);
  - the ROLLBACK TO install, and the savepoint name lookup and mark
    construction (`savepoints.go`).

**Cut.** Roles come in as an `authorization.Read` value, and the lock
checkpoint, the cursor ordinal and the exclusion slot as values. Cursors,
portals, the owner extension, exclusions and SSI calls stay in root
wrappers, which keep their order: root payload first, then the owner fields.

**Dedupes.**
- `restoreSavepoint` and the inline ROLLBACK TO branch.
- The permission check in `preparedActorLocked` and the one in
  `completePrepared`.
- The four `len(e.prepared)` sites become `Len()`, and the pgcatalog
  adapter iterates the registry.

**Locks.** The registry runs under `Engine.mu`, plus `Session.Mu` at the
publication preflight; the owner methods under `t.Mu`. The rollback install
keeps Engine.mu → Settings.Mu → Session.Mu, with `t.Mu`.

**Tests.**
- Root units: `transaction_prepared_test.go`,
  `transaction_prepared_boundary_test.go`, `transaction_identity_test.go`,
  `notification_publication_test.go` and the savepoint tests. Use `RACE=1`.
- Quick and `REFERENCE=1` selections: `TestReferencePreparedTransaction`,
  `TestReferenceTransactionControls` and `TestReferenceAdvisoryXact`.

## Item 161 — Statement classification

Needs 160, because both edit `transaction_controls.go` and
`transaction_prepared.go`. About 0.17k lines move, into a new
`session/statement_class.go`.

**Moves.**
- the class switch of `snapshotPhase`, `readOnlyUtilityEligibility` and the
  tail of `executionEligibility` (`transaction_isolation.go`);
- the replacement-read classification in `gate`;
- the two "describe as operation" switches in `Describe` (`execute.go`);
- the state, word and notice table of `executeControl`
  (`transaction_controls.go`);
- the notices in `executePrepared`, and the savepoint word;
- one shared error each for "current transaction is aborted, commands
  ignored until end of transaction block" (5 copies) and "there is no
  transaction in progress" (4 copies);
- the CHAIN word mapping (2 copies).

**Cut.** The inputs are values: state, InBlock and ReadOnly. The
`partitionIndexTransaction` short-circuit stays in the root, because it
takes `Engine.mu`.

**Risks.** `Describe` and `execute` keep their call points and only ask the
`session` predicates, so the error order stays the same.

**Tests.** Quick and `REFERENCE=1` selections:
`TestReferenceTransactionControls`, `TestReferenceServerAbortedDescribe`,
`TestReferenceQueryTableCreateReadOnly` and
`TestReferencePreparedTransactionUtilityProtocolStages`.

**Docs.** Rewrite the README sentence "The root keeps the checks, their
exceptions and errors".

## Item 162 — Statement data views

Needs 161, because both edit `transaction_isolation.go`. About 0.17k lines
move, next to `storage_projection.go`.

**Moves.**
- the body of `captureDataRootsAtLocked` (about 150 of its 192 lines,
  `transaction_visibility.go`);
- `needsProjection` from `snapshotDataPhase` (`transaction_isolation.go`);
- the root-capture loop that `snapshotDataPhase` and
  `acquireSerializableSnapshot` (`serializable_snapshot.go`) both repeat.

Also inline `captureDataViewsLocked`, which has two callers.

**Cut.**
- `privateStorageLocked`, `privateCatalogLocked` and the partitions slot
  become value inputs: the store, the journal, the read and the existing
  view.
- `view.SameMaterializedLineage` and `index.SameRelationRowShape` become
  static func fields, because `session` cannot import `view` or `index`.

**Stays root.** The epoch checks, `visibilitySnapshotLocked`, the
`pgrowlocks` dependency flag and storing `s.view`.

**Callers and locks.** `Engine.mu`, plus `t.Mu` from `AcquireDataView` and
`pgrowlocks`. Nothing new is locked.

**Risks.** This runs on every statement. Pass the input by pointer, use
static funcs, and add no allocations. Run `make bench` before and after.

**Tests.**
- Root units: `pgrowlocks_visibility_test.go` and the snapshot-phase tests
  that name the moved code. Use `RACE=1`.
- Quick and `REFERENCE=1` selections: `TestReferenceFixtureNativeIsolation`,
  one RR selection (`TestReferenceMergeDeleteRepeatableRead`) and one
  materialized view refresh visibility selection.

## Item 164 — Settings, DISCARD and setting calls

Needs 161 (`transaction_isolation.go`) and 163 (`discard.go`). About 0.16k
lines move into `session.SettingsStore`.

**Moves.**
- the install body of `applySetting` (`setting_apply.go`);
- `resetAllSettingsLocked` and the SHOW ALL rows (`discard.go`);
- the session-mode-to-setting mapping in `executeCharacteristics`
  (`transaction_isolation.go`);
- the DISCARD role reset compute and validate, which folds in
  `discardResetPreflightLocked` and its copy in `discardAllPreflight`;
- `finishDiscardRoleResetLocked`;
- the input decode of `current_setting` and `set_config`
  (`setting_calls.go`);
- `publicationOperationLocked` (`transaction_publication.go`), a locked
  near-copy of `session.ValidateOperation`.

**Cut.** Only `e.settings.Definitions()` and `domainOf(e)` reach the engine.
Pass the `settings` registry and the domain as values.

**Stays root.** The Engine.mu → Settings.Mu → Session.Mu windows, and the
binder that `setting_calls.go` builds.

**Tests.**
- Root units: `settings_metadata_test.go`, `session_timeout_pending_test.go`,
  `schema_test.go` and `row_security_provider_lifecycle_test.go`.
- Quick and `REFERENCE=1` selections: `TestReferenceSettings` and
  `TestReferenceDiscard`.

**Docs.** Narrow the README's "settings application" entry to the lock
windows.

## Item 165 — Protocol value codec

Needs —. Item 163 also edits `protocol_portal.go`, so whichever of the two
lands second rebases over the other. About 0.24k lines move.

**Moves.**
- All of `protocol_values.go`: `ProtocolInput`, `ProtocolLogParameter`,
  `ProtocolContext`, `ProtocolField`, the format and parameter decoding,
  `protocolImport` and `encodeProtocolRow`.
- The pure statement checks: `preparableBody` (`prepared_rebind.go`),
  `protocolPlannedStatement` (`protocol_simple.go`), and
  `protocolPlainSelect`, `protocolHasRows` and `protocolParameterOIDs`
  (`protocol_prepared.go`).

**Cut.**
- `protocolImport` reads only `op.Context()` and `s.output`, so pass those as
  values.
- `analyze.ParameterInfo` becomes a `[]sqltype.Usage` and a count.

**Callers.** Bind, Execute and the fastpath call with nothing held.
`cmd/stuntgres` switches to `session.Protocol*`: about 13 references in 5
files. It already imports `session`, and the driver package does not name
these types.

**Stays root.** `ProtocolStatement`, `ProtocolPortalDescription` and
`ProtocolExecution`, because they carry `StatementDescription`.

**Risks.** `encodeProtocolRow` runs once per row: keep the code unchanged and
run `make bench` before and after.

**Tests.**
- Root units: `protocol_mechanism_test.go` (its pure codec cases move to
  `session`) and the `cmd/stuntgres` tests.
- Quick and `REFERENCE=1` selections: `TestReferenceServerExtended`,
  `TestReferenceServerPortal`, `TestReferenceServerProtocol` and
  `TestReferenceLikeBinaryParameters`.

**Docs.** Add the protocol codec to the session README.

## Status

- 155: done (`3bc20a6ca`, `ea5eb4842`, then the docs commit).
  - On `session`: `FenceStorageRead`, `FenceIndexSet`, `FenceNamespace` and
    `FenceRootKinds`; `StorageReadFence` (`NewStorageReadFence`, `Address`,
    `Matches`), `IndexSetFence` and `NamespaceFence`, all `CommittedFence`
    (`journal_fences.go`); `JournalCapture` with `ObserveDatabase`,
    `Observe`, `ObserveBase`, `ObserveCandidate` and `FenceNames`
    (`journal_capture.go`); `Journal.RecordCandidateDefaults` and
    `Journal.MergeCandidate`. The root keeps `catalogFence`, its three kinds,
    `catalogFencesCurrentLocked` (one `session.CommittedFence` branch),
    `captureCatalogStageLocked` (three session calls) and
    `storageReadsValidLocked`, now in `catalog_message.go`;
    `storage_read_fence.go` is gone.
  - Deviations: the database predecessor block moved too (`ObserveDatabase`,
    the "predecessors" input); its `catalogAuthority` update stays in the root
    right after the call. The `dropSequences` epoch check stays in the root,
    because `catalog.Read.Epoch` accepts a nil state and `State.Epoch` does
    not.
  - Root 391 → 390 files and 48.9k → 48.7k lines (−0.21k); `session` gains
    0.33k production lines.
  - Tests: none moved, because all use engine fixtures. Adapted
    `catalog_fence_test.go`, `driver_batch_catalog_provider_test.go`,
    `partition_attach_publication_test.go` and
    `default_privileges_catalog_test.go`. New session units are in
    `journal_fences_test.go` and `journal_capture_test.go`.
  - For item 167: new root feature code since 153: `catalog_source_views.go`
    (0.17k, `b8a166c33`), `plpgsql_assignment.go`, `plpgsql_checkpoint.go`,
    `plpgsql_conditional.go` and `sql_command_effects.go` (0.53k,
    `8ee510460`, `9b91b59d0`, `c0fa3a453`) and `restore_provider.go`
    (59 lines, `7ab862ceb`). Root production code grew about 1.3k net from
    `9942278d2` to `c0fa3a453`.
- 160: done (`af02a443f`, `ac427b956`, `225857607`, then the docs commit).
  - On `session`: `PreparedRegistry` (`Lookup`, `Len`, `All`, `ReserveLocked`,
    `InstallLocked`, `RemoveLocked`, `ClaimLocked`, `ReleaseLocked`,
    `PublicationPreflightLocked`), `PreparedTransaction` with `CheckActor`,
    and `PreparedPublication` (`prepared_registry.go`); owner methods
    `BeginLocked`, `Clear`, `Abort`, `ChainModes`, `InstallChain`,
    `PrepareRefusal`, `PreparedEntry` (`owner_transitions.go`),
    `CaptureSavepoint`, `FindSavepoint`, `RollbackToLocked`; and
    `SettingsStore.RestoreSavepointLocked`, which replaces `RestoreSavepoint`.
  - The root keeps the role read in `preparedActorLocked`, the
    visibility/SSI/lock calls, `clearTransaction` (cursors, portals, foreign,
    exclusions, then `Clear`) and a new `restoreSavepointLocked` (cursors,
    portals, `RollbackToLocked`, exclusions) shared by ROLLBACK TO and
    `restoreSavepoint`; each keeps its own SSI calls and order.
  - Deviations: `PrepareRefusal` and `PreparedEntry` are owner methods, not
    registry ones, because they run under `t.Mu` without `Engine.mu`.
    `PreparedEntry` takes the statement's `ref`, so the stale-preparing check
    keeps its meaning. `restoreSavepoint` now takes Engine.mu → Settings.Mu →
    Session.Mu once instead of three separate windows (same order).
  - Root 402 files and 49.8k → 49.7k lines (−0.15k); `session` gains 0.31k
    production lines.
  - Tests: none moved (all use engine fixtures). Adapted to the new names:
    `transaction_prepared_test.go`, `transaction_prepared_boundary_test.go`,
    `transaction_prepared_snapshot_test.go`, `transaction_identity_test.go`,
    `pg_locks_test.go`, `notification_publication_test.go`,
    `commit_timestamp_test.go`, `advisory_xact_snapshot_test.go`,
    `driver_batch_{role_retirement,detached_identity,prepared_admission}_test.go`,
    `foreign_actions_locks_test.go`, `exclusion_lifecycle_test.go`,
    `vacuum_{review_regressions,visibility}_test.go`,
    `catalog_source_contract_test.go` and
    `row_security_provider_lifecycle_test.go`. New session units:
    `prepared_registry_test.go`, `owner_transitions_test.go`.
  - For item 167: new root feature code since 155: the trigger DDL family
    (`trigger_ddl.go`, `trigger_stage.go`, `trigger_lifecycle.go`,
    `statement_host_trigger.go`, `plpgsql_trigger.go`,
    `trigger_authorization.go`, 0.78k, `8d86f11c0`, `6d78b74d5`),
    `plpgsql_cursor.go` (0.45k, `3a9e5f701`), `plpgsql_loop.go` (85 lines,
    `cadb6dba6`), `catalog_name_locks.go` and `transactional_schema.go`
    (0.33k, `b1787a044`), and `range_reown.go` and
    `restore_range_constructor.go` (86 lines, `212c92013`).
- 161: done (`210068247`, `ead37a123`, then the docs commit).
  - On `session`: `ControlAction`, `ControlStep`, `ClassifyControl`,
    `AbortedControlAllowed`, `SavepointOutsideBlockError`, `AbortedError`
    and `NoTransactionWarning` (`transaction_control.go`); `ReadOnlyExempt`,
    `ReadOnlyRefusal`, `ExplicitUtilityRefusal` and `UnsupportedInBlockError`
    (`statement_class.go`); `SnapshotStatement`, `SnapshotTargetAdmission`,
    `ReplacementRead`, `DescribeClass`, `DescribeBeforeSnapshot` and
    `DescribeAfterSnapshot` (new `statement_phases.go`). The root's
    `protocolAbortedError` is gone; the 5 aborted-block, 4 no-transaction,
    2 CHAIN-word and 2 "unsupported in an explicit transaction" copies are
    one each.
  - The root keeps every call point and lock: `executeControl` and
    `executePrepared` switch on the `ControlStep` (PREPARE in an aborted
    block still ends it in the root), `readOnlyUtilityEligibility` returns
    before `t.Mu` for exempt statements, and `executionEligibility` keeps its
    short-circuits before `ExplicitUtilityRefusal`.
  - Deviations: `session/statement_class.go` already existed (item 129), so
    the phase predicates went to a new `statement_phases.go` and the control
    table to `transaction_control.go`. The two Describe switches were in
    `transaction.go`, not `execute.go`; the first also absorbed the
    TransactionCharacteristics, SET CONSTRAINTS and RENAME CONSTRAINT
    empties. `UnsupportedInBlockError` is an extra dedupe (2 copies).
  - Root 400 files and 49.3k → 49.2k lines (−0.11k); `session` gains 0.27k
    production lines (the control table and doc comments).
  - Tests: none moved or rewritten (root tests call the root methods, which
    stay). New session units: `transaction_control_test.go`
    (`TestClassifyControl`, `TestAbortedControlAllowed`) and
    `statement_phases_test.go` (`TestReadOnlyRefusal`,
    `TestExplicitUtilityRefusal`, `TestStatementPhases`).
  - For item 167: `077c060a8` added a non-concurrent CREATE INDEX early
    return to the root prefix of `executionEligibility`; it stays in the root
    because it must return before `t.Mu` and before the 0A000 refusal. No new
    root feature files since 165.
- 162: done (`f60ab5f2e`, `d1ef52aa9`, then the docs commit; started at
  master `249501c80`, rebased onto `f83d170e8`, `d542e772d`,
  `a23fe82e6` and `aab80720d`).
  - On `session`: `DataViewInput` with `ValidateView` and `Capture`
    (`data_view.go`); `PrivateStorage`, `CommittedRoots` and
    `TransactionOwner.NeedsSnapshotProjection` (`snapshot_roots.go`). The
    root's `privateStorage` and `snapshotTargetRoot` are gone,
    `privateStorageLocked` is a one-line wrapper, `captureDataViewsLocked` is
    inlined into its two callers, and `retainTransactionSnapshotLocked` calls
    `CommittedRoots`.
  - The root keeps the epoch, domain and missing-storage checks, the choice
    of the existing view, `visibilitySnapshotLocked` and
    `captureStatementSnapshotLocked`, the frontier choice, the private read,
    the pgrowlocks dependency flag and storing `s.view` or the validation
    view. It fills the capture fields after the snapshot step, which sets the
    RC target roots.
  - Deviations: the existing-view check reads the private catalog lazily
    through `Journal.PrivateRead(Owner, Published)` instead of a `Read`
    input, so a reused view never builds a read it did not build before.
    `DataViewInput` does not escape; startup allocations are unchanged.
  - Root 402 files and 49.0k → 48.9k lines (−0.18k); `session` gains 0.31k
    production lines.
  - Tests: none moved or rewritten (the root tests call root methods that
    stay). New session units: `snapshot_roots_test.go`
    (`TestPrivateStorage`, `TestCommittedRoots`,
    `TestNeedsSnapshotProjection`) and `data_view_test.go`
    (`TestDataView*`).
  - For item 167: the prompt's `TestReferenceMergeDeleteRepeatableRead` does
    not exist; `TestReferenceMergeDeleteRepeatableReadFollowup` was used.
    Under `REFERENCE=1` its `/postgres` subtests fail with PostgreSQL's own
    40001 (oracle drift seen on master before); its `/stuntgres` subtests
    pass. New root feature code since 164: `plpgsql_query_loop.go` and
    `cte_owned_rows.go` (0.19k, `aab80720d`).
- 164: done (`02525be60`, `5bd350fee`, then the docs commit; started at
  master `249501c80`, rebased onto `f83d170e8`).
  - On `session`: `SettingsStore.AdmitLocked`, `ApplyLocked`,
    `ResetAllLocked` and `ShowAllRowsLocked` (new `setting_install.go`);
    `SettingsStore.CurrentSettingLocked` and `SetConfigInputLocked` (new
    `setting_calls.go`); `DiscardRoleReset`, `ValidateRoleResetLocked`,
    `Operation.DiscardRoleResetPending` and
    `Operation.FinishDiscardRoleResetLocked` (new `discard_role_reset.go`);
    `SessionCharacteristicSetting`; and `PublicationOperationLocked`, which
    shares a locked body with `TransactionAuthority`. All take the
    `settings.Registry` and the domain as values.
  - The root keeps every lock window and the binder. `publicationOperationLocked`
    stays as a one-line wrapper (about 35 callers). The two publication
    preflights call `ValidateRoleResetLocked` only when
    `DiscardRoleResetPending`, so ordinary commits build no role read.
  - Deviations: `AdmitLocked` is an extra dedupe of the
    operation-plus-attachment check (4 copies). The publisher's role reset is
    an inline guarded call rather than a session method, to keep the role read
    lazy without a callback.
  - Root 400 files and 49.0k → 48.8k lines (−0.16k); `session` gains 0.24k
    production lines.
  - Tests: none moved (all use engine fixtures). Adapted `schema_test.go`
    (`ResetAllLocked`) and `serializable_publication_test.go`, whose context
    barrier now matches the `session.PublicationOperationLocked` frame. New
    session units: `setting_install_test.go`
    (`TestSettingsResetAllLocked`, `TestSettingsShowAllRowsLocked`) and
    `TestSessionCharacteristicSetting`.
  - For item 167: no new root feature files since 161; item 166 added
    `ddl/database.go` only.
- 165: done (`1b75fbd78`, `6fb0e5f37`, then the docs commit).
  - On `session`: `ProtocolInput`, `ProtocolLogParameter`, `ProtocolContext`,
    `ProtocolField`, `ProtocolFormats`, `ProtocolFormat`,
    `ProtocolResultFormats`, `ProtocolReceiveKind` (`ProtocolBindInput`,
    `ProtocolFunctionInput`), `ImportProtocolParameters` and
    `EncodeProtocolRow` (`protocol_values.go`); `PreparableBody`,
    `ProtocolPlannedStatement`, `ProtocolPlainSelect`, `ProtocolHasRows` and
    `ProtocolParameterOIDs` (`protocol_statement.go`). The root's
    `protocol_values.go` is gone; `cmd/stuntgres` names the four types from
    `session`.
  - Deviations: `protocolImport` became `ImportProtocolParameters`, a free
    function taking the context, output environment, count and types;
    `ProtocolParameterOIDs` takes the count and types. `EncodeProtocolRow`
    is unchanged.
  - Root 402 → 401 files and 49.7k → 49.5k lines (−0.24k); `session` gains
    0.26k production lines.
  - Tests: moved the scratch-reuse and unused-result-selector cases of
    `TestProtocolValueCodecs` and the has-rows assertion of
    `TestProtocolPreparedRegistry` to `session/protocol_values_test.go`
    (`TestProtocolCodec*`), with a new import case for the value inputs.
    Adapted `protocol_mechanism_test.go`, `protocol_terminal_test.go` (a
    local `session` variable renamed) and `cmd/stuntgres/protocol_test.go`.
  - For item 167: no new root feature files since 160; `077c060a8` grew
    `index.go` by 24 lines.