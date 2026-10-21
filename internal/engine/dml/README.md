# internal/engine/dml

The records and pure algorithms of the write executors: ON CONFLICT
arbitration and arbiter inference, the INSERT write overlay, RETURNING
outputs, MERGE and UPDATE/DELETE records, inheritance target helpers,
TRUNCATE records, foreign-key action records and COPY insertion batching,
plus the command state `Command` and the executors' host `Host`. Step 29
of the engine split created it (29b moved the clean set, 29d.1 added the
seam). It sits at L13 in
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture):
it imports `access`, `analyze` (payload types and pure functions only),
`authorization`, `catalog`, `copyfmt`, `exec`, `expr`, `fdw`, `ids`,
`index`, `lock`, `partition`, `plan`, `storage`, `table`, `view`,
`visibility`, `parser`, `sqlstate`, `sqltype` and the standard library. Nothing below it imports
it, it never imports the engine, and it never builds a binder. See
[ENGINE-SPLIT-dml.md](../../../docs/ENGINE-SPLIT-dml.md).

## What it owns

- **Host** (`host.go`): `Host` embeds `exec.Host`, `index.WriteHost` and
  `fdw.WriteRuntime` and adds four pieces, each with one owner:
  `Statement` (the active command, target, private rows, snapshot, unique
  journal, query authority, FK action scope, WHERE CURRENT OF row, the ON
  CONFLICT binding check, the disclosure environment and command errors;
  never locks or waits, read at call time), `Stage` (the write stage
  windows `PrepareReplacement`, `StageInsert`, `StageReplacement` with its
  `ReplaceLane`, `AppendAcceptedInsert`, `Delete`; each takes the owner
  mutex and `Engine.mu` itself and never waits), `RowLocks` (`AcquireRow`,
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
  `InheritedTargets`, `ViewWrite` and `RLS`), with `TargetRefs`, `Storage`
  and `CaptureSource`. The engine embeds it by value in its per-command
  mutation, so writable CTEs, rules and SQL routines swap it whole.
- **ON CONFLICT** (`insert_conflict.go`, `insert_conflict_predicate.go`):
  `InsertConflictState` (`NewInsertConflictState`, `SelectArbiters`,
  `Matches`, `MarkAffected`), `BindingForConflict`,
  `ConflictUpdateCardinalityError`, and the partial-index predicate proofs
  `NormalizeConflictPredicate` (the proof `conflictPredicateImpliedBy` is
  package-internal).
- **INSERT write overlay** (`insert_write_overlay.go`): `InsertWriteOverlay`
  and `InsertWriteKeyOverlay`, the per-statement key buckets that unique
  probing consults for the command's own rows.
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
root `Result`) and the COPY protocol. The executors themselves
(`executeInsertLocked`, `executeUpdate`, `executeDelete`, `executeMerge`,
TRUNCATE, COPY, FK enforcement and actions, foreign DML) stay until 29d,
and so do the functions that run the records against the scope:
`stageMergeRows`, `recordCopyInsertion`, `flushCopyInsertion`,
`addInsertWriteKey`, `preflightConflictUpdate`. Also root: the COPY cursors
(they hold the scope), `truncateGraphFence` (catalog fences implement the
root `catalogFence` interface; the kind order is in `catalog_fence.go`),
`mutationTarget` (the admitted target, which statement admission and DDL
read), `WriteStage` with the loops around the `Stage` windows until 29d.2,
and `preflightUpdate`/`preflightMerge` (root query preflight). 29d moves
the executors behind `Host` in steps (29d.2–29d.6).

## Invariants

- The package holds no engine state and no mutex, and nothing here waits.
  Executors run after `Engine.mu` is released; row-lock waits never happen
  under it.
- Messages, details, SQLSTATEs and the first-error choice are unchanged,
  including MERGE and ON CONFLICT cardinality errors and COPY's line
  context.
- The engine's COPY cursors satisfy `CopyInsertionCursor` through a runtime
  type assertion; `copy_insertion.go` asserts it statically.

## Testing

`insert_conflict_predicate_test.go` and `insert_conflict_truth_test.go`
cover the predicate proofs here. Every other DML unit test builds an engine
and stays in the root package, naming the moved code as `dml.X`; the host
seam tests are `statement_host_dml_test.go` (`LatestRow` per kind,
`StageReplacement` stale/flush/drop, `PrepareReplacement` order).

```sh
make check RUN='^TestInsertConflictPredicate'
make check RUN='^TestStatementHost'
make check RUN='^Test(Insert|Update|Merge|Delete|Truncate|Copy|Foreign|InheritanceDML|CTE)'
make check RACE=1 RUN='^Test(Truncate|ForeignAction|Copy)'
```

Public behavior is covered by the dual-mode reference suite:

```sh
make check RUN='^TestReference(Insert|Update|Delete|Merge|Copy|Truncate)'
make check RUN='^TestReference(CTE|ForeignKey|ForeignAction)'
```
