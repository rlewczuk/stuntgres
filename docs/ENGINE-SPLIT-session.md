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
- 160: not started.
- 161: not started.
- 162: not started.
- 164: not started.
- 165: not started.
