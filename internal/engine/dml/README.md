# internal/engine/dml

The records and pure algorithms of the write executors: ON CONFLICT
arbitration and arbiter inference, the INSERT write overlay, RETURNING
outputs, MERGE and UPDATE/DELETE records, inheritance target helpers,
TRUNCATE records, foreign-key action records and COPY insertion batching,
plus the command state `Command`, the executors' host `Host`, the unique
and exclusion probing and staging loops, and the INSERT, ON CONFLICT DO
UPDATE and COPY FROM executors that run over it. Step 29 of the engine
split created it (29b moved the clean set, 29d.1 added the seam, 29d.2
moved the probing and staging loops, 29d.3 the INSERT and COPY FROM
executors). It sits at L13 in
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
- **RETURNING** (`returning.go`, `returning_preflight.go`):
  `InsertReturningOutput`, `MergeReturningOutput` (`Project`),
  `ForeignMutationRow`, `ProjectForeignReturning`, `PreflightReturning`.
- **MERGE, UPDATE, DELETE** (`merge.go`, `candidates.go`): `MergeRows`,
  `MergeActionRow`, `MergeCandidate`, `MergeMatches`, `SelectMergeArm`,
  `MergeCardinalityError`, `MergeOnTruth`, `MergeHasVirtualReads`,
  `UpdateCandidate`, `DeleteUsingCandidate`, `StatementCTERoot`,
  `MergeFollowStrong`.
- **Inheritance targets** (`mutation_targets.go`): `MutationRowIdentity`,
  `ScatterMutationRow`, `MutationPhysicalRow`, `VersionRows`.
- **TRUNCATE** (`truncate.go`): `TruncateScope`, `TruncateRelation`,
  `TruncateName`, `TruncateSequence`, `TruncateReferencer(s)`,
  `CompareTruncateAddress`, `TruncateForeignKeyError`,
  `TruncateOtherTemporary`, `TruncateOwnsSequence`.
- **Foreign-key actions** (`foreign_actions.go`): `ForeignKeyJob`,
  `ForeignActionScope`, `ForeignActionAdmission`, `ForeignActionTransition`,
  `ForeignActionPlan`, `ForeignActionQuery`, `ForeignActionSameKey`,
  `RefreshForeignActionJob`, `SameForeignKeyDefinition`.
- **COPY insertion** (`copy.go`): `CopyInsertionState`
  (`NewCopyInsertionState`, `Full`), `CopyInsertionBuffer`,
  `CopyInsertionCursor`, `CopyInsertionDiagnostic`, `CopyRelationKind`,
  `CopyLineTargets`. The COPY codecs are in [`copyfmt`](../copyfmt).

Names: everything the engine names is capitalized as it was
(`insertConflictState` is `InsertConflictState`); helpers only the package
uses keep their lower-case names.

## What stays in the engine

Statement dispatch, source capture under `Engine.mu`, `WriteStage` and row
publication, writable-CTE orchestration (`completeCTEMutation` returns the
root `Result`) and the COPY protocol (`CopyProtocol`, binding, input
capture, `copyIO` with `copyReader`/`copyWriter`, COPY TO output and
`copyCommandError`). INSERT keeps its locked prologue
(`executeInsertLocked`: snapshots, `capturePartitionInsertLocked`, source
capture, the unlock, source selection and freezing) and wraps the
`Outcome` in a `Result`. The other executors (`executeUpdate`,
`executeDelete`, `executeMerge`, TRUNCATE, FK enforcement and actions,
foreign DML) stay until 29d.4–29d.6, and so do `stageMergeRows` and
`preflightConflictUpdate`. Also root: `truncateGraphFence` (catalog fences implement the
root `catalogFence` interface; the kind order is in `catalog_fence.go`),
`mutationTarget` (the admitted target, which statement admission and DDL
read), `WriteStage` with the `Stage` windows (its public `Apply` checks
each row and calls `ApplyValidated`), the exclusion projection's `*Locked`
helpers, `readmitProgramsAfterWait`, and `preflightUpdate`/`preflightMerge`
(root query preflight). 29d moves the remaining executors behind `Host`
in steps (29d.4–29d.6).

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

## Testing

`insert_conflict_predicate_test.go` and `insert_conflict_truth_test.go`
cover the predicate proofs here. Every other DML unit test builds an engine
and stays in the root package, naming the moved code as `dml.X`; the host
seam tests are `statement_host_dml_test.go` (`LatestRow` per kind,
`StageReplacement` stale/flush/drop, `PrepareReplacement` order, the
statement slot reads, `Active` after Finish, `ReadmitRevalidation`'s lock
release). The probing and staging loops are covered by the engine's unique,
exclusion, ON CONFLICT, view write and allocation tests; the per-row cost
by `BenchmarkInsertConflictCommittedOmissions`,
`BenchmarkInsertConflictUpdateDistinctStage` and
`TestUpdateCoreSparseAllocation`. The INSERT and COPY FROM executors are
covered by the engine's INSERT, COPY, protocol COPY and partition
publication tests; the latter match the `dml.ApplyInsertValidated` frame.

```sh
make check RUN='^TestInsertConflictPredicate'
make check RUN='^TestStatementHost'
make check RUN='^Test(Insert|Update|Merge|Delete|Truncate|Copy|Foreign|InheritanceDML|CTE)'
make check RUN='^Test(Unique|Exclusion|PartialUnique|ConcurrentIndex|ViewWrite|ViewCheck|UpdateCore)'
make check RACE=1 RUN='^Test(Unique|Exclusion|InsertConflict)'
make check RACE=1 RUN='^Test(Truncate|ForeignAction|Copy)'
make check RACE=1 RUN='^Test(ProtocolCopy|ProtocolTerminal|InsertReturning|InsertSelect|(Range|List)PartitionPublication)'
```

Public behavior is covered by the dual-mode reference suite:

```sh
make check RUN='^TestReference(Insert|Update|Delete|Merge|Copy|Truncate)'
make check RUN='^TestReference(CTE|ForeignKey|ForeignAction)'
```
