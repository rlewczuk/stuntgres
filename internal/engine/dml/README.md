# internal/engine/dml

The write executors and their records: the command state `Command`, the
executors' host `Host`, unique and exclusion probing and the staging loops,
the INSERT, ON CONFLICT DO UPDATE, COPY FROM, UPDATE, DELETE, MERGE and
foreign DML executors, FK enforcement and actions over `FinishHost`, and the
pure pieces they use (ON CONFLICT arbitration, the INSERT write overlay,
RETURNING outputs, inheritance target helpers, TRUNCATE and FK action
records, COPY insertion batching).

The package sits at layer L13 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture). It
imports `access`, `analyze` (payload types and pure functions only),
`authorization`, `catalog`, `copyfmt`, `exec`, `expr`, `fdw`, `ids`, `index`,
`lock`, `partition`, `plan`, `session` (the operation's COPY input state),
`storage`, `table`, `view`, `visibility`, `parser`, `sqlstate`, `sqltype` and
the standard library. Nothing below it imports it; it never imports the engine
and never builds a binder. See
[ENGINE-SPLIT-dml.md](../../../docs/ENGINE-SPLIT-dml.md).

## What it owns

- **Host** (`host.go`): `Host` embeds `exec.Host`, `index.WriteHost` and
  `fdw.WriteRuntime` and adds four pieces, each with one owner:
  - `Statement`: the active command, target, private rows, snapshot, unique
    journal, query authority, FK action and index scopes (read without
    allocating unless asked), lock ticket, catalog reads of a captured state,
    WHERE CURRENT OF row, ON CONFLICT binding check, disclosure environment,
    command errors and the `Operation`. It never locks or waits and is read
    at call time.
  - `Stage`: `Active` (the activity check before any candidate validation);
    `ReadmitRevalidation`, which recaptures the write authority under
    `Engine.mu` and readmits the programs unlocked after a stale binding; and
    the write windows `PrepareReplacement`, `StageInsert`, `StageReplacement`
    (with its `ReplaceLane`), `AppendAcceptedInsert` and `Delete`. Each window
    takes the owner mutex and `Engine.mu` itself and never waits.
  - `RowLocks`: `AcquireRow`, `AcquireUpdateRow` and `AcquirePartitionLock`
    wait with the caller holding nothing; `LatestRow` reads one row per
    `RowKind` under one lock; `PhysicalRowEvent`.
  - `Presentation`: `RowDetail` by `DetailPurpose`, `SchemaName`,
    `CaptureRegclassOutput`.

  `UpdateRowRequest`, `MergeRowRequest` and `DeleteRowRequest` build the
  `RowRequest`s; `Outcome` is an executor's result. The engine's
  `statementHost` implements `Host` (`statement_host_dml.go`,
  `statement_host_stage.go`, `statement_host_latest_row.go`).
- **Finish host** (`host.go`): `FinishHost` is `Host` plus `ForeignKeys`, the
  FK runtime's engine side: `Staged` (never locks); the authority and job
  captures `ForeignKeyJobs`, `RecaptureAuthority` and `ForeignKeyAuthority`;
  the row images `ForeignKeyRows`, `RetainedForeignKeyRows` and
  `RefreshForeignKeyRows`; the internal action windows
  `RefreshForeignActionJob`, `AdmitForeignAction`, `ForeignActionRow`,
  `DeleteForeignAction` and `PlanForeignActionInput` (each locks once and
  never waits); `GrantForeignKey` (waits with the caller holding nothing, then
  locks to revalidate); and `SwapForeignActionAuthority` (the running action's
  authority slot). `Authority` is the statement authority the FK runtime
  checks and evaluates with (`Access`, `access.Environment`,
  `exec.AdmissionAuthority`, `ActingAs` for the referencing table's owner);
  the engine's `*AuthorizationView` implements it and doubles as an
  `exec.Context`'s `RoutineAuthority`. `statementHost` implements
  `FinishHost` in `statement_host_foreign_key.go`.
- **Command state** (`command.go`): `Command`, the active command's DML state
  grouped by owner (`Indexes`, `Insert`, `Partition`, `InheritedTargets`,
  `ViewWrite`, `RLS`), with `TargetRefs`, `Storage`, `CaptureSource`,
  `WriteOverlay` (ON CONFLICT DO UPDATE's or RETURNING's INSERT write overlay)
  and `PartitionStoredRow` with the partition insertion groups. The engine
  embeds it by value in its per-command mutation, so writable CTEs, rules and
  SQL routines swap it whole.
- **COPY FROM state**: `Insert.CopyFrom` points at the single `CopyFromState`
  (predicate, parsed policy, captured input-domain adapter, per-command reject
  state). A soft conversion yields one present pending-rejected row. Omitted
  defaults run before rejection/limit admission; the shared post-default stage
  rejects or filters before generation, routing and constraints. Ordinary
  INSERT has no COPY state; binary shares filtering without a
  conversion-recovery seam. Reached selected DEFAULT markers execute
  immediately in COPY-list order through the default-origin evaluator, with the
  real host environments and without the soft-input adapter. Their assembler
  mask is consumed so they never execute twice; after an earlier failure later
  fields and markers stay undemanded. Omitted defaults stay in the assembler.
- **ON CONFLICT** (`insert_conflict.go`, `insert_conflict_predicate.go`):
  `InsertConflictState` (`NewInsertConflictState`, `SelectArbiters`,
  `Matches`, `MarkAffected`), `BindingForConflict`,
  `ConflictUpdateCardinalityError` and the partial-index predicate proof
  `NormalizeConflictPredicate` (`conflictPredicateImpliedBy` is internal).
- **INSERT write overlay** (`insert_write_overlay.go`,
  `write_overlay_probe.go`): `InsertWriteOverlay` and `InsertWriteKeyOverlay`,
  the per-statement key buckets unique probing consults for the command's own
  rows, and `InsertOwnRow`.
- **Unique and exclusion probing** (`unique.go`, `exclusion.go`,
  `index_candidates.go`, `insert_conflict_admission.go`): `BindUniqueWrite`,
  `ProbeUniqueRow` (committed roots, own and staged rows, the write overlay and
  the batch prefix; with an ON CONFLICT state only the arbiters),
  `CheckUniqueRow`, `ClaimUniqueRow`, `UniqueKeyChanged`, `UniqueConflict`
  with `Diagnostic`, `ProbeExclusion` (also SET CONSTRAINTS' deferred
  recheck), `DiscardExclusion`, `ValidateIndexCandidates`,
  `ExistingUniqueOutput`, and DO NOTHING admission `AdmitInsertConflictRow`
  and `InsertConflictVisible`. They wait only through `ClaimUniqueKey` and
  `WaitTransaction`, with the caller holding nothing.
- **Staging loops** (`stage.go`): `ApplyValidated` and `ReplaceRows` own the
  work around the `Stage` windows: key-change classification, row grants,
  index candidate validation and, after a stale unique binding, the
  revalidation (`ReadmitRevalidation`, then a reprobe of every row or the DO
  NOTHING readmission) and the retry.
- **INSERT** (`insert.go`, `insert_query.go`, `insert_values.go`,
  `insert_admission.go`, `insert_validate.go`): `ExecuteInsert` runs a
  captured `InsertInput` (VALUES rows, or INSERT ... SELECT through
  `EvaluateInsertQuery`) with the caller holding nothing and returns an
  `Outcome`. `AssembleInsertRows` produces, validates and admits rows from any
  cursor (INSERT ... SELECT, COPY; a nil host assembles without statement
  authority). Also `EvaluateInsertValuesRow`, `ValidateInsertRow`,
  `CheckViewRow`, `RowConstraintError`, `AdmitInsertRow` (unique check, DO
  NOTHING or DO UPDATE) and `ApplyInsertBatch`.
  - Both row-production paths check the same `RowSecurity.CheckProposal` after
    generation and routing and before constraints, including the SELECT NEW
    check RETURNING requires.
  - Routed NEW projection runs only for an actual NEW program and uses the
    destination's admitted physical-to-named layout, copying finalized
    generated values by index into the private NamedRow. Identity layouts use
    the physical row directly; no-program/bypass success does no proposal
    mapping. No row matches columns by name or builds a second layout.
  - A physical constraint failure refreshes named diagnostic values through
    that same admitted layout when no NEW program already projected them.
- **INSERT RETURNING and ON CONFLICT DO UPDATE** (`insert_returning.go`,
  `insert_conflict_update.go`, `update_assignments.go`):
  `BeginInsertReturning`, `AdmitInsertProducer`, `AppendAcceptedInsert` (the
  final gate before the `Stage` window), the DO UPDATE action (probe, row
  wait, latest row, predicate, assignments, replacement) and
  `EvaluateUpdateAssignments`, shared with UPDATE and foreign DML.
- **Partition routing** (`partition_insert.go`): routing and validation of
  each row against its leaf and the target's partition constraint, per-leaf
  unique checks and `ApplyInsertValidated`, which stages each leaf group in
  first-use order.
- **COPY FROM cursors** (`copy_cursor.go`, `copy_binary_cursor.go`,
  `copy_insertion.go`): `CopyCursor` and `CopyBinaryCursor`
  (`NewCopyCursor`, `NewCopyBinaryCursor`, `Diagnostic`) over the reader the
  engine's COPY protocol builds, and the buffered insertion flush. A
  source-marked fresh-input diagnostic may keep inner parser Context before
  its COPY frame. Other already-contextual primary errors keep their original
  diagnostic and wrapper identity, including a primary joined with pending
  cancellation: COPY never flattens such an error or lets cancellation
  replace it.
- **UPDATE** (`update.go`, `update_validate.go`, `row_security.go`):
  `ExecuteUpdate` runs a captured `UpdateInput` (target source, FROM sources,
  parameters, schema) with the caller holding nothing. It freezes the
  candidate set before the first row wait, then per candidate takes the row
  lock, rereads the latest version, rechecks the predicate if the row moved,
  evaluates the assignments, validates, claims and checks unique keys and
  stages the replacement through `ReplaceRows`.
  - `ValidateMutationRow` (generation, NOT NULL and CHECK of the named target;
    RI SET NULL/DEFAULT actions share it) and its inherited-leaf variant,
    which reports the physical leaf with the named target's row.
  - Public UPDATE checks NEW between generation and constraints; the shared RI
    validator does not apply caller RLS. `MutationMatches` guards OLD before
    user predicates/RHS and again after refreshed-row waits, including the
    secondary key-lock refresh.
  - An inherited NEW proposal projects finalized physical values into the
    private logical replacement, never into segment scratch still borrowed by
    OLD evaluation or RETURNING; no extra per-row clone is needed.
- **DELETE** (`delete.go`, `delete_using.go`): `ExecuteDelete` runs a captured
  `DeleteInput` (target source, query sources, parameters) the same way and
  stages each row through `Stage.Delete`; DELETE ... USING freezes the
  qualifying target/source pairs first. OLD policy guards precede user
  conditions on both routes and are re-evaluated against refreshed
  named-target tuples.
- **MERGE** (`merge_execute.go`, `merge_rows.go`): `ExecuteMerge` runs a
  captured `MergeInput` (target source, query sources, parameters, schema and
  the catalog the INSERT arms' value errors use). `AssembleMergeRows`
  completes the source and evaluates ON before any action wait, then per
  candidate selects the arm, takes the row lock (`AcquireRow` for DELETE,
  `AcquireUpdateRow` otherwise), rereads the row (`RowMerge`), follows the
  update chain, reclassifies and builds the actions. `StageMergeRows` stages
  them in action order (DELETE through `Stage.Delete`, runs of INSERTs through
  `ApplyValidated`, runs of UPDATEs through `ReplaceRows`).
- **Foreign DML** (`foreign.go`): `ExecuteForeign` runs a captured
  `ForeignCommand` (foreign target, query sources, parameters, statement
  evaluation, INSERT ... SELECT root, catalog and VALUES rows): it plans and
  enters the modification, then inserts the evaluated rows or updates and
  deletes the rows of the identity scan, checking view options and projecting
  RETURNING per row. Foreign callbacks run with the caller holding nothing.
- **RETURNING** (`returning.go`, `returning_preflight.go`):
  `InsertReturningOutput`, `MergeReturningOutput` (`Project`),
  `ForeignMutationRow`, `ProjectForeignReturning`, `PreflightReturning`.
- **Preflights** (`update_preflight.go`, `merge_preflight.go`,
  `insert_conflict_update.go`): `PreflightUpdate`,
  `PreflightUpdateWithPlanning`, `PreflightMerge` and
  `PreflightConflictUpdate` fold each executor's private binding over `exec`
  expression preflight and `view.PreflightQuery`, copying only what changes;
  the engine's `execute_bound.go` calls them before the executors.
- **MERGE, UPDATE, DELETE records** (`merge.go`, `candidates.go`):
  `MergeRows`, `MergeActionRow`, `MergeCandidate`, `MergeMatches`,
  `SelectMergeArm`, `MergeCardinalityError`, `MergeOnTruth`,
  `MergeHasVirtualReads`, `UpdateCandidate`, `DeleteUsingCandidate`,
  `StatementCTERoot`, `MergeFollowStrong`.
- **Inheritance targets** (`mutation_targets.go`): `MutationRowIdentity`,
  `ScatterMutationRow`, `MutationPhysicalRow`, `VersionRows`.
- **TRUNCATE** (`truncate.go`, `truncate_check.go`): `TruncateScope`,
  `TruncateRelation`, `TruncateName`, `TruncateSequence`,
  `TruncateReferencer(s)`, `CompareTruncateAddress`,
  `TruncateForeignKeyError`, `TruncateOtherTemporary`,
  `TruncateOwnsSequence`; the target checks `CheckTruncate` and
  `GuardedCatalogMutationError` (also the error of INSERT, UPDATE, DELETE and
  MERGE on a guarded system catalog) over `access.View`.
- **Foreign-key actions** (`foreign_actions.go`, `foreign_key_reach.go`,
  `foreign_action_settle.go`, `foreign_action_apply.go`): the records
  `ForeignKeyJob`, `ForeignActionScope` (DML state only; CREATE and ALTER
  TABLE's FK captures are `table.ForeignKeyCreation`),
  `ForeignActionAdmission`, `ForeignActionTransition`, `ForeignActionPlan`,
  `ForeignActionQuery`, `ForeignActionSameKey`, `RefreshForeignActionJob`,
  `SameForeignKeyDefinition`; `ForeignKeyReach` (the jobs and relation lock
  modes the staged transitions reach); `PrepareForeignActions` (capture,
  relation locks in OID order, recapture, comparison binding); and
  `SettleForeignActions`, which runs RESTRICT, CASCADE, SET NULL and SET
  DEFAULT for every transition, including those the actions stage, as the
  referencing table's owner, with the action's statement in the error
  context.
- **Foreign-key checks** (`foreign_key_check.go`): `CheckForeignKeys`, the
  final check that every new child row has a parent and no changed or deleted
  parent key is still referenced, with key-share grants on uncommitted
  parents.
- **COPY insertion** (`copy.go`): `CopyInsertionState`
  (`NewCopyInsertionState`, `Full`), `CopyInsertionBuffer`,
  `CopyInsertionCursor`, `CopyInsertionDiagnostic`, `CopyRelationKind`,
  `CopyLineTargets`. The COPY codecs are in [`copyfmt`](../copyfmt/README.md).
- **Prologue decisions**, each over values the root captures in its own
  window: `LatestUpdateRow`, `LatestDeleteRow` and `LatestMergeRow`
  (`latest_row.go`) read one row over a `LatestRowSource` (store, own writes
  and deletes, commit sequence, stable flag and frontier; RR conflicts by
  `visibility.RepeatableConflict`); `FoldDefault` (`default_fold.go`) folds
  one column default for COPY FROM and FK SET DEFAULT, each with its own
  admission check; `CompleteCTEMutation` (`cte_completion.go`) records a
  finished data-modifying CTE from its count and rows; `RowDiagnosticDetail`
  (`row_diagnostic.go`) is the failing-row disclosure over an `access.View`
  and its environment; `ForeignCommandPrologue` (`foreign_prologue.go`)
  rejects ON CONFLICT and CURRENT OF on a foreign table and picks the
  operation and invoker; `TruncateGraph.CheckCurrent` (`truncate_fence.go`)
  is the TRUNCATE fence check at publication (the caller holds `Engine.mu`;
  it takes no lock).
- **Target admission** (`target_admission.go`): `TargetAdmission`, the
  `table.TargetEntry` of single-table SELECT, COPY, INSERT, MERGE, DELETE and
  UPDATE (also while preparing), registered by the root with the `view` and
  `table` entries.

Helpers used only inside the package keep lower-case names.

## What stays in the engine

- Statement dispatch, source capture under `Engine.mu`, `WriteStage` and row
  publication, and writable-CTE orchestration (it passes each command's count
  and rows to `CompleteCTEMutation`).
- The COPY protocol: `CopyProtocol`, binding, input capture, the COPY FROM
  default loop around `FoldDefault` (binary receive checks, unselected
  defaults, null-literal clearing), `copyIO` with `copyReader`/`copyWriter`,
  COPY TO output and `copyCommandError`.
- The locked prologues and thin wrappers that turn an `Outcome` into a
  `Result`: INSERT's `executeInsertLocked` (snapshots,
  `capturePartitionInsertLocked`, source capture, unlock, source selection and
  freezing); UPDATE, DELETE and MERGE prologues in `execute_bound.go` with
  `executeUpdate`, `executeDelete` (which also captures the target source) and
  `executeMerge`; foreign DML's `executeForeignCommandLocked` (the unlock
  after `ForeignCommandPrologue`, target and source captures,
  `fdw.OperationSupported`, `foreignUnlocked`) and `foreignTableScope`.
- Host windows: the row lock waits (`acquireRow`, `acquireUpdateRowMode`),
  `WriteStage.Delete`, `acquireInheritedMutationTargets`,
  `validateInheritedReplacements`, `rowConstraintDetail`, and
  `rowDiagnosticDetail`'s authority capture under `Engine.mu` before
  `RowDiagnosticDetail`. `LatestRow` keeps its lock, `validateLockedRelation`,
  the UPDATE and MERGE epoch checks and the source capture; the
  conflict-update reader (authority and binding check) stays whole.
- **FK order in Finish:** exclusion events, `SettleForeignActions`, exclusion
  events again when an action added one, then `CheckForeignKeys` unless the
  statement only projected catalog roots.
- Shared with ALTER TABLE, CREATE TABLE and the write stage: the FK row and
  grant bodies (`foreignKeyGrant`, `captureForeignKeyRows`,
  `refreshForeignKeyRows`), internal action admission and
  `WriteStage.deleteForeignAction`, `writeAuthorizationLocked`, the
  `foreignActionAuth` slot and its readers, and `acquireForeignKeyCreation`.
- TRUNCATE execution; `truncateGraphFence`, which embeds `TruncateGraph`
  (catalog fences implement the root `catalogFence` interface; the kind order
  is in `catalog_fence.go`); `mutationTarget` (the admitted target, read by
  statement admission and DDL); `WriteStage` with the `Stage` windows (its
  public `Apply` checks each row and calls `ApplyValidated`); the exclusion
  projection's `*Locked` helpers; and `readmitProgramsAfterWait`.

## Invariants

- No pointer to a per-row stack value (a probe candidate, a one-row batch
  array) crosses a `Host` method: through the interface it escapes and moves
  to the heap for every row. Pass a copy and apply the result here
  (`PublishExclusion`, `WriteCandidate.RecordExclusion`).
- The package holds no engine state and no mutex. It waits only through
  `Host` methods (`ClaimUniqueKey`, `WaitTransaction`, `AcquireUpdateRow`)
  with the caller holding nothing. Executors run after `Engine.mu` is
  released; row-lock waits never happen under it.
- Messages, details, SQLSTATEs and the first-error choice are compatibility
  contracts, including MERGE and ON CONFLICT cardinality errors and COPY's
  line context.
- The COPY cursors satisfy `CopyInsertionCursor` through a runtime type
  assertion (`copy_insertion.go` asserts it statically). They read the
  operation's COPY input state and the host's input environment at each use
  and keep the 57014/FATAL mapping through `CopyCommandError`.
- A failure-only `RowDetail` takes a copy of the binding, so only the error
  path allocates it. The host suppresses failing-row values under
  metadata-only RLS eligibility before its table SELECT shortcut, keeping
  named-target command authority. SPI-local RI NOT NULL/CHECK reporting keeps
  owner-visible Detail under FORCE only for the validated writable child-owner
  settlement context; a scoped authority marker alone is not enough. FK
  after-event key disclosure is independent and keeps its RLS privacy checks.
- The FK runtime runs only from Finish, with the caller holding nothing; all
  its waits go through `GrantForeignKey`. Each FK job costs one heap row image
  (`RefreshForeignKeyRows` takes a pointer), and `ActingAs` copies the
  authority once per job or action, never per row.

## Testing

Unit tests here cover the ON CONFLICT predicate proofs
(`insert_conflict_predicate_test.go`, `insert_conflict_truth_test.go`), the
target-admission entry, `CompleteCTEMutation`, COPY filter row admission and
the routed RLS proposal layout. Every other DML unit test needs an engine and
lives in `internal/engine`, naming the code as `dml.X`:

- Host seams: `statement_host_dml_test.go` (`LatestRow` per kind,
  `StageReplacement` stale/flush/drop, `PrepareReplacement` order, statement
  slot reads, `Active` after Finish, `ReadmitRevalidation`'s lock release) and
  `statement_host_foreign_key_test.go` (authority swap, `ActingAs`, captured
  jobs and modes, retained image).
- Executors, probing and FK runtime: the engine's INSERT, COPY, UPDATE,
  DELETE, MERGE, unique, exclusion, ON CONFLICT, view write, inheritance DML,
  row-lock wait, cancellation and `TestForeignActions*`/`TestForeignKey*`
  tests, which call `dml.ExecuteDelete`, `dml.AssembleMergeRows`,
  `dml.StageMergeRows`, `dml.PrepareForeignActions`,
  `dml.SettleForeignActions` and the preflights over the scope's host.
- Allocation guards: `BenchmarkInsertConflictCommittedOmissions`,
  `BenchmarkInsertConflictUpdateDistinctStage`, `BenchmarkUpdateCoreSparse`
  and `TestUpdateCoreSparseAllocation`.
- `TestRLSRoutedProposalLayout` checks the reordered/sparse admitted layout
  and private proposal buffer: stored generation is finalized before NEW
  evaluation, the no-program path leaves NamedRow untouched, a NEW program
  consumes indexed finalized values without replacing layout or buffer
  ownership, and NamedRow holds finalized generated values at the RowDetail
  call boundary.
- COPY: `TestCopyFilterRowAdmission` here; in the engine
  `TestCopyFilterInputProvenance` and `TestCopyFilterFinishVeto` (scoped
  hard/soft causes, terminal and final publication vetoes) and
  `TestProtocolCopyCancellationContext` (contextual errors under pending
  cancellation).

Frame name matched by tests: the range and list partition publication tests
cancel at the `dml.ApplyInsertValidated` frame; renaming it breaks them.

```sh
make check RUN='^Test(InsertConflict(Predicate|Truth)|DMLTargetAdmission$$|CTEDMLInvocationOwnership$$)'
make check RUN='^TestStatementHost'
make check RUN='^Test(UpdateCore|UpdateFrom|UpdateRow|RuleStaticOperatorPreflight|MergeNothing|InsertConflictUpdate|InsertReturning)'
make check RACE=1 RUN='^Test(Unique|Exclusion|InsertConflict)'
make check RACE=1 RUN='^Test(Range|List)PartitionPublication'
make check RUN='^Test(ForeignActions|ForeignKey)'
make check RUN='^Test(RLSRoutedProposalLayout|CopyFilter(RowAdmission|InputProvenance|FinishVeto)|ProtocolCopyCancellationContext)$$'
```

Public behavior lives in [`reference/dml`](../../../reference/dml/README.md#testing).
