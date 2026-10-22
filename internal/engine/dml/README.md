# internal/engine/dml

The records and pure algorithms of the write executors: ON CONFLICT
arbitration and arbiter inference, the INSERT write overlay, RETURNING
outputs, MERGE and UPDATE/DELETE records, inheritance target helpers,
TRUNCATE records, foreign-key action records and COPY insertion batching,
plus the command state `Command`, the executors' host `Host`, the unique
and exclusion probing and staging loops, and the INSERT, ON CONFLICT DO
UPDATE, COPY FROM, UPDATE, DELETE, MERGE and foreign DML executors that
run over it, and FK enforcement and actions over `FinishHost`. Step 29 of
the engine split created it (29b moved the clean set, 29d.1 added the
seam, 29d.2 moved the probing and staging loops, 29d.3 the INSERT and COPY
FROM executors, 29d.4 the UPDATE and DELETE executors, 29d.5 the MERGE and
foreign DML executors, 29d.6 the FK seam, enforcement and actions, 29e
the DML preflights). It sits at L13 in
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture):
it imports `access`, `analyze` (payload types and pure functions only),
`authorization`, `catalog`, `copyfmt`, `exec`, `expr`, `fdw`, `ids`,
`index`, `lock`, `partition`, `plan`, `session` (the operation's COPY
input state), `storage`, `table`, `view`, `visibility`, `parser`, `sqlstate`, `sqltype` and the standard library. Nothing below it imports
it, it never imports the engine, and it never builds a binder. See
[ENGINE-SPLIT-dml.md](../../../docs/ENGINE-SPLIT-dml.md).

## What it owns

- **Host** (`host.go`): `Host` embeds `exec.Host`, `index.WriteHost` and
  `fdw.WriteRuntime` and adds four pieces, each with one owner:
  `Statement` (the active command, target, private rows, snapshot, unique
  journal, query authority, the FK action and index scopes (read without
  allocating unless asked), the lock ticket, catalog reads of a captured
  state, WHERE CURRENT OF row, the ON CONFLICT binding check, the
  disclosure environment, command errors and the `Operation`; never locks
  or waits, read at call time), `Stage` (`Active`, the activity check before any candidate
  validation; `ReadmitRevalidation`, which recaptures the write authority
  under `Engine.mu` and readmits the programs unlocked after a stale
  binding; and the write stage windows `PrepareReplacement`, `StageInsert`,
  `StageReplacement` with its `ReplaceLane`, `AppendAcceptedInsert`,
  `Delete`; each window takes the owner mutex and `Engine.mu` itself and
  never waits), `RowLocks` (`AcquireRow`,
  `AcquireUpdateRow`, `AcquirePartitionLock` wait with the caller holding
  nothing; `LatestRow` reads one row per `RowKind` under one lock;
  `PhysicalRowEvent`) and `Presentation` (`RowDetail` by `DetailPurpose`,
  `SchemaName`, `CaptureRegclassOutput`). `UpdateRowRequest`,
  `MergeRowRequest` and `DeleteRowRequest` build the `RowRequest`s;
  `Outcome` is an executor's result. The engine's `statementHost`
  implements `Host` (`statement_host_dml.go`, `statement_host_stage.go`,
  `statement_host_latest_row.go`).
- **Finish host** (`host.go`): `FinishHost` is `Host` plus `ForeignKeys`,
  the FK runtime's engine side: `Staged` (never locks), the authority and
  job captures `ForeignKeyJobs`, `RecaptureAuthority` and
  `ForeignKeyAuthority`, the row images `ForeignKeyRows`,
  `RetainedForeignKeyRows` and `RefreshForeignKeyRows`, the internal action
  windows `RefreshForeignActionJob`, `AdmitForeignAction`,
  `ForeignActionRow`, `DeleteForeignAction` and `PlanForeignActionInput`
  (each locks once and never waits), the grant `GrantForeignKey` (waits
  with the caller holding nothing, then locks to revalidate) and
  `SwapForeignActionAuthority` (the running action's authority slot).
  `Authority` is the statement authority the FK runtime checks and
  evaluates with (`Access`, `access.Environment`,
  `exec.AdmissionAuthority`, `ActingAs` for the referencing table's
  owner); the engine's `*AuthorizationView` implements it, so it doubles as
  an `exec.Context`'s `RoutineAuthority`. `statementHost` implements
  `FinishHost` in `statement_host_foreign_key.go`.
- **Command state** (`command.go`): `Command`, the active command's DML
  state, grouped by owner (`Indexes`, `Insert`, `Partition`, plus
  `InheritedTargets`, `ViewWrite` and `RLS`), with `TargetRefs`, `Storage`,
  `CaptureSource`, `WriteOverlay` (ON CONFLICT DO UPDATE's or
  RETURNING's INSERT write overlay) and `PartitionStoredRow` with the
  partition insertion groups. The engine embeds it by value in its per-command
  mutation, so writable CTEs, rules and SQL routines swap it whole.
- **ON CONFLICT** (`insert_conflict.go`, `insert_conflict_predicate.go`):
  `InsertConflictState` (`NewInsertConflictState`, `SelectArbiters`,
  `Matches`, `MarkAffected`), `BindingForConflict`,
  `ConflictUpdateCardinalityError`, and the partial-index predicate proofs
  `NormalizeConflictPredicate` (the proof `conflictPredicateImpliedBy` is
  package-internal).
- **INSERT write overlay** (`insert_write_overlay.go`,
  `write_overlay_probe.go`): `InsertWriteOverlay` and
  `InsertWriteKeyOverlay`, the per-statement key buckets that unique
  probing consults for the command's own rows, and `InsertOwnRow`.
- **Unique and exclusion probing** (`unique.go`, `exclusion.go`,
  `index_candidates.go`): `BindUniqueWrite`, `ProbeUniqueRow` (committed
  roots, own and staged rows, the write overlay and the batch prefix;
  with an ON CONFLICT state only the arbiters), `CheckUniqueRow`,
  `ClaimUniqueRow`, `UniqueKeyChanged`, `UniqueConflict` with
  `Diagnostic`, `ProbeExclusion` (also SET CONSTRAINTS' deferred recheck),
  `DiscardExclusion`,
  `ValidateIndexCandidates` and `ExistingUniqueOutput`; ON CONFLICT DO
  NOTHING admission `AdmitInsertConflictRow` and `InsertConflictVisible`
  (`insert_conflict_admission.go`). They wait only through
  `ClaimUniqueKey` and `WaitTransaction`, with the caller holding nothing.
- **Staging loops** (`stage.go`): `ApplyValidated` and `ReplaceRows` own
  the work around the `Stage` windows: key-change classification, row
  grants, index candidate validation, and after a stale unique binding
  the revalidation (`ReadmitRevalidation`, then a reprobe of every row, or
  the ON CONFLICT DO NOTHING readmission) and the retry.
- **INSERT** (`insert.go`, `insert_query.go`, `insert_values.go`,
  `insert_admission.go`, `insert_validate.go`): `ExecuteInsert` runs a
  captured `InsertInput` (VALUES rows, or INSERT ... SELECT through
  `EvaluateInsertQuery`) with the caller holding nothing and returns an
  `Outcome`; `AssembleInsertRows` produces, validates and admits rows from
  any cursor (INSERT ... SELECT, COPY; a nil host assembles without
  statement authority); `EvaluateInsertValuesRow`, `ValidateInsertRow`,
  `CheckViewRow`, `RowConstraintError`, `AdmitInsertRow` (unique check,
  DO NOTHING or DO UPDATE) and `ApplyInsertBatch`.
- **INSERT RETURNING and ON CONFLICT DO UPDATE** (`insert_returning.go`,
  `insert_conflict_update.go`, `update_assignments.go`):
  `BeginInsertReturning`, `AdmitInsertProducer`, `AppendAcceptedInsert`
  (the final gate before the `Stage` window), the DO UPDATE action (probe,
  row wait, latest row, predicate, assignments, replacement) and
  `EvaluateUpdateAssignments`, which UPDATE and foreign DML share.
- **Partition routing execution** (`partition_insert.go`): routing and
  validation of each row against its leaf and the target's partition
  constraint, per-leaf unique checks and `ApplyInsertValidated`, which
  stages each leaf group in first-use order.
- **COPY FROM** (`copy_cursor.go`, `copy_binary_cursor.go`,
  `copy_insertion.go`): `CopyCursor` and `CopyBinaryCursor`
  (`NewCopyCursor`, `NewCopyBinaryCursor`, `Diagnostic`) over the reader
  the engine's COPY protocol builds, and the buffered insertion flush.
- **UPDATE** (`update.go`, `update_validate.go`): `ExecuteUpdate` runs a
  captured `UpdateInput` (target source, FROM sources, parameters, schema)
  with the caller holding nothing and returns an `Outcome`: it freezes the
  candidate set before the first row wait, then per candidate takes the
  row lock, rereads the latest version, rechecks the predicate if the row
  moved, evaluates the assignments, validates, claims and checks unique
  keys and stages the replacement through `ReplaceRows`.
  `ValidateMutationRow` (generation, NOT NULL and CHECK of the named
  target; RI SET NULL/DEFAULT actions share it) and its inherited-leaf
  variant, which reports the physical leaf with the named target's row.
- **DELETE** (`delete.go`, `delete_using.go`): `ExecuteDelete` runs a
  captured `DeleteInput` (target source, query sources, parameters) the
  same way and stages each row through `Stage.Delete`; DELETE ... USING
  freezes the qualifying target/source pairs first.
- **MERGE** (`merge_execute.go`, `merge_rows.go`): `ExecuteMerge` runs a
  captured `MergeInput` (target source, query sources, parameters, schema
  and the catalog the INSERT arms' value errors use). `AssembleMergeRows`
  completes the source and evaluates ON before any action wait, then per
  candidate selects the arm, takes the row lock (`AcquireRow` for DELETE,
  `AcquireUpdateRow` otherwise), rereads the row (`RowMerge`), follows
  the update chain and reclassifies, and builds the actions;
  `StageMergeRows` stages them in action order (each DELETE through
  `Stage.Delete`, runs of INSERTs through `ApplyValidated` and of UPDATEs
  through `ReplaceRows`).
- **Foreign DML** (`foreign.go`): `ExecuteForeign` runs a captured
  `ForeignCommand` (the foreign target, query sources, parameters,
  statement evaluation, INSERT ... SELECT root, catalog and VALUES rows):
  it plans and enters the modification, then inserts the evaluated rows,
  or updates and deletes the rows of the identity scan, checking view
  options and projecting RETURNING per row. Foreign callbacks run with the
  caller holding nothing.
- **RETURNING** (`returning.go`, `returning_preflight.go`):
  `InsertReturningOutput`, `MergeReturningOutput` (`Project`),
  `ForeignMutationRow`, `ProjectForeignReturning`, `PreflightReturning`.
- **Preflights** (`update_preflight.go`, `merge_preflight.go`,
  `insert_conflict_update.go`): `PreflightUpdate`,
  `PreflightUpdateWithPlanning`, `PreflightMerge` and
  `PreflightConflictUpdate` fold the private binding of each Execute over
  `exec` expression preflight and `view.PreflightQuery`, copying only what
  changes; `execute_bound.go` calls them before the executors.
- **MERGE, UPDATE, DELETE records** (`merge.go`, `candidates.go`): `MergeRows`,
  `MergeActionRow`, `MergeCandidate`, `MergeMatches`, `SelectMergeArm`,
  `MergeCardinalityError`, `MergeOnTruth`, `MergeHasVirtualReads`,
  `UpdateCandidate`, `DeleteUsingCandidate`, `StatementCTERoot`,
  `MergeFollowStrong`.
- **Inheritance targets** (`mutation_targets.go`): `MutationRowIdentity`,
  `ScatterMutationRow`, `MutationPhysicalRow`, `VersionRows`.
- **TRUNCATE** (`truncate.go`): `TruncateScope`, `TruncateRelation`,
  `TruncateName`, `TruncateSequence`, `TruncateReferencer(s)`,
  `CompareTruncateAddress`, `TruncateForeignKeyError`,
  `TruncateOtherTemporary`, `TruncateOwnsSequence`; the target checks
  (`truncate_check.go`) `CheckTruncate` and `GuardedCatalogMutationError`
  (also the error of INSERT, UPDATE, DELETE and MERGE on a guarded system
  catalog), over `access.View`.
- **Foreign-key actions** (`foreign_actions.go`, `foreign_key_reach.go`,
  `foreign_action_settle.go`, `foreign_action_apply.go`): the records
  `ForeignKeyJob`, `ForeignActionScope` (DML state only; CREATE and ALTER
  TABLE's FK captures are `table.ForeignKeyCreation`), `ForeignActionAdmission`,
  `ForeignActionTransition`, `ForeignActionPlan`, `ForeignActionQuery`,
  `ForeignActionSameKey`, `RefreshForeignActionJob`,
  `SameForeignKeyDefinition`; `ForeignKeyReach`, the jobs and relation lock
  modes the staged transitions reach; `PrepareForeignActions` (capture,
  relation locks in OID order, recapture, comparison binding) and
  `SettleForeignActions`, which runs RESTRICT, CASCADE, SET NULL and SET
  DEFAULT for every transition, including the ones the actions stage, as
  the referencing table's owner, with the action's statement in the error
  context.
- **Foreign-key checks** (`foreign_key_check.go`): `CheckForeignKeys`, the
  final check that every new child row has a parent and no changed or
  deleted parent key is still referenced, with key-share grants on
  uncommitted parents.
- **COPY insertion** (`copy.go`): `CopyInsertionState`
  (`NewCopyInsertionState`, `Full`), `CopyInsertionBuffer`,
  `CopyInsertionCursor`, `CopyInsertionDiagnostic`, `CopyRelationKind`,
  `CopyLineTargets`. The COPY codecs are in [`copyfmt`](../copyfmt).
- **Prologue decisions** (29f), each over values the root captures in its
  own window: `LatestUpdateRow`, `LatestDeleteRow` and `LatestMergeRow`
  (`latest_row.go`) read one row over a `LatestRowSource` (the store, own
  writes and deletes, the commit sequence, the stable flag and frontier;
  RR conflicts by `visibility.RepeatableConflict`); `FoldDefault`
  (`default_fold.go`) folds one column default for COPY FROM and FK SET
  DEFAULT, each passing its own admission check; `CompleteCTEMutation`
  (`cte_completion.go`) records a finished data-modifying CTE from its
  count and rows; `RowDiagnosticDetail` (`row_diagnostic.go`) is the
  failing-row disclosure over an `access.View` and its environment;
  `ForeignCommandPrologue` (`foreign_prologue.go`) rejects ON CONFLICT and
  CURRENT OF on a foreign table and picks the operation and invoker;
  `TruncateGraph.CheckCurrent` (`truncate_fence.go`) is the TRUNCATE
  fence check at publication (caller holds `Engine.mu`; it takes no lock).
- **Target admission** (`target_admission.go`): `TargetAdmission`, the
  `table.TargetEntry` of single-table SELECT, COPY, INSERT, MERGE, DELETE and
  UPDATE (also while preparing), which the root registers with the `view` and
  `table` entries.

Names: everything the engine names is capitalized as it was
(`insertConflictState` is `InsertConflictState`); helpers only the package
uses keep their lower-case names.

## What stays in the engine

Statement dispatch, source capture under `Engine.mu`, `WriteStage` and row
publication, writable-CTE orchestration (it passes each command's count
and rows to `CompleteCTEMutation`) and the COPY protocol (`CopyProtocol`, binding, input
capture, the COPY FROM default loop (binary receive checks, unselected
defaults, null-literal clearing) around `FoldDefault`, `copyIO` with
`copyReader`/`copyWriter`, COPY TO output and `copyCommandError`). INSERT keeps its locked prologue
(`executeInsertLocked`: snapshots, `capturePartitionInsertLocked`, source
capture, the unlock, source selection and freezing) and wraps the
`Outcome` in a `Result`. UPDATE and DELETE keep their locked prologues
in `execute_bound.go` and thin wrappers (`executeUpdate`, `executeDelete`,
which also captures the target source) that wrap the `Outcome`. The row
lock waits (`acquireRow`, `acquireUpdateRowMode`), `WriteStage.Delete`,
`acquireInheritedMutationTargets`, `validateInheritedReplacements` and the
row detail windows (`rowConstraintDetail`, and `rowDiagnosticDetail`'s
authority capture under `Engine.mu` before `RowDiagnosticDetail`) are host
windows and stay. `LatestRow` keeps its lock, `validateLockedRelation`,
the UPDATE and MERGE epoch checks and the source capture; the
conflict-update reader (authority and binding check) stays whole. MERGE keeps its locked prologue in `execute_bound.go`
and the thin wrapper `executeMerge`; foreign DML keeps
`executeForeignCommandLocked` (the unlock after `ForeignCommandPrologue`,
the foreign target and source captures, `fdw.OperationSupported`, the
unlock and `foreignUnlocked`) and `foreignTableScope`. Finish keeps the FK order
(exclusion events, `SettleForeignActions`, exclusion events again when an
action added one, then `CheckForeignKeys` unless the statement only
projected catalog roots). The FK row and grant bodies (`foreignKeyGrant`,
`captureForeignKeyRows`, `refreshForeignKeyRows`), the internal action
admission and `WriteStage.deleteForeignAction`, `writeAuthorizationLocked`,
the `foreignActionAuth` slot and its readers, and
`acquireForeignKeyCreation` stay: ALTER TABLE, CREATE TABLE and the write
stage use them too. TRUNCATE stays. Also root: `truncateGraphFence`, which embeds
`TruncateGraph` (catalog fences implement the root `catalogFence`
interface; the kind order is in `catalog_fence.go`),
`mutationTarget` (the admitted target, which statement admission and DDL
read), `WriteStage` with the `Stage` windows (its public `Apply` checks
each row and calls `ApplyValidated`), the exclusion projection's `*Locked`
helpers and `readmitProgramsAfterWait`.

## Invariants

- No pointer to a per-row stack value (a probe candidate, a one-row batch
  array) crosses a `Host` method: through the interface it escapes, and
  the value moves to the heap for every row. Pass a copy and apply the
  result here (`PublishExclusion` and `WriteCandidate.RecordExclusion`);
  the ON CONFLICT and UPDATE benchmarks and `TestUpdateCoreSparseAllocation`
  guard the allocation counts.
- The package holds no engine state and no mutex. It waits only through
  `Host` methods (`ClaimUniqueKey`, `WaitTransaction`, `AcquireUpdateRow`)
  with the caller holding nothing. Executors run after `Engine.mu` is
  released; row-lock waits never happen under it.
- Messages, details, SQLSTATEs and the first-error choice are unchanged,
  including MERGE and ON CONFLICT cardinality errors and COPY's line
  context.
- The COPY cursors satisfy `CopyInsertionCursor` through a runtime type
  assertion; `copy_insertion.go` asserts it statically. They read the
  operation's COPY input state and the host's input environment at each
  use and keep the 57014/FATAL mapping through `CopyCommandError`.
- A failure-only `RowDetail` takes a copy of the binding, so only the error
  path allocates it.
- The FK runtime runs only from Finish, with the caller holding nothing; its
  waits all go through `GrantForeignKey`. Each FK job costs one heap row
  image (`RefreshForeignKeyRows` takes a pointer), and `ActingAs` copies the
  authority once per job or action, never per row.

## Testing

`insert_conflict_predicate_test.go` and `insert_conflict_truth_test.go`
cover the predicate proofs here, `TestDMLTargetAdmission` the
target-admission entry, `TestCTEDMLInvocationOwnership`
(`cte_completion_test.go`) `CompleteCTEMutation`. Every other DML unit test builds an engine
and stays in the root package, naming the moved code as `dml.X`; the host
seam tests are `statement_host_dml_test.go` (`LatestRow` per kind,
`StageReplacement` stale/flush/drop, `PrepareReplacement` order, the
statement slot reads, `Active` after Finish, `ReadmitRevalidation`'s lock
release). The probing and staging loops are covered by the engine's unique,
exclusion, ON CONFLICT, view write and allocation tests; the per-row cost
by `BenchmarkInsertConflictCommittedOmissions`,
`BenchmarkInsertConflictUpdateDistinctStage`, `BenchmarkUpdateCoreSparse`
and `TestUpdateCoreSparseAllocation`. The INSERT and COPY FROM executors are
covered by the engine's INSERT, COPY, protocol COPY and partition
publication tests; the latter match the `dml.ApplyInsertValidated` frame.
UPDATE and DELETE are covered by the engine's UPDATE, DELETE, inheritance
DML, row-lock wait and cancellation tests; the DELETE tests call
`dml.ExecuteDelete` over the scope's host. MERGE is covered by the
engine's MERGE tests, which call `executeMerge` or
`dml.AssembleMergeRows` and `dml.StageMergeRows` over the scope's host;
foreign DML by the FDW modification tests. FK enforcement and actions are
covered by the engine's `TestForeignActions*` and `TestForeignKey*` tests,
which call `dml.PrepareForeignActions` and `dml.SettleForeignActions` over
the scope's host; the FK seam tests are `statement_host_foreign_key_test.go`
(the authority swap, `ActingAs`, the captured jobs and modes, the retained
image). The preflights are covered by the engine's `TestUpdateCore*`,
`TestUpdateFrom*`, `TestUpdateRow*`, `TestRuleStaticOperatorPreflight`,
`TestMergeNothing*`, `TestInsertConflictUpdate*` and `TestInsertReturning*`
tests, which call `dml.PreflightUpdate`, `dml.PreflightMerge` and
`dml.PreflightConflictUpdate`.

```sh
make check RUN='^TestInsertConflictPredicate|^TestDMLTargetAdmission$$|^TestCTEDMLInvocationOwnership$'
make check RUN='^TestStatementHost'
make check RUN='^Test(UpdateCore|UpdateFrom|UpdateRow|RuleStaticOperatorPreflight|MergeNothing|InsertConflictUpdate|InsertReturning)'
make check RUN='^Test(Insert|Update|Merge|Delete|Truncate|Copy|Foreign|InheritanceDML|CTE)'
make check RUN='^Test(Unique|Exclusion|PartialUnique|ConcurrentIndex|ViewWrite|ViewCheck|UpdateCore)'
make check RACE=1 RUN='^Test(Unique|Exclusion|InsertConflict)'
make check RACE=1 RUN='^Test(Truncate|ForeignAction|Copy)'
make check RUN='^Test(StatementHostForeign|StatementHostRetained|ForeignActions|ForeignKey|DomainProgramPhase)'
make check RACE=1 RUN='^Test(ProtocolCopy|ProtocolTerminal|InsertReturning|InsertSelect|(Range|List)PartitionPublication)'
make check RACE=1 RUN='^Test(Update|Delete)(Lock|Contention|Cancellation|Core|From|Using)|^TestInheritanceDML'
make check RACE=1 RUN='^Test(Merge|GeneratedVirtualMerge|FDWModify|FDWTransaction|FDWStage)'
```

Public behavior is covered by the dual-mode reference suite:

```sh
make check RUN='^TestReference(Insert|Update|Delete|Merge|Copy|Truncate)'
make check RUN='^TestReference(CTE|ForeignKey|ForeignAction)'
make check RUN='^TestReference(InheritanceDML|GeneratedStoredUpdate|ViewCheck)'
make check RUN='^TestReferenceFDWWrite'
```
