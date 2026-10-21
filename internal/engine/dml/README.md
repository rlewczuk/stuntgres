# internal/engine/dml

The records and pure algorithms of the write executors: ON CONFLICT
arbitration and arbiter inference, the INSERT write overlay, RETURNING
outputs, MERGE and UPDATE/DELETE records, inheritance target helpers,
TRUNCATE records, foreign-key action records and COPY insertion batching.
Step 29 of the engine split created it (29b moved the clean set). It sits
at L13 in [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture):
it imports `analyze` (payload types and pure functions only),
`authorization`, `catalog`, `copyfmt`, `exec`, `expr`, `ids`, `index`,
`lock`, `partition`, `plan`, `storage`, `table`, `visibility`, `parser`,
`sqlstate`, `sqltype` and the standard library. Nothing below it imports
it, it never imports the engine, and it never builds a binder. See
[ENGINE-SPLIT-dml.md](../../../docs/ENGINE-SPLIT-dml.md).

## What it owns

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
  `UpdateCandidate`, `DeleteUsingCandidate`, `StatementCTERoot`.
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
the `statementMutation` groups (`mutationTarget`, `mutationIndexes`,
`mutationInsert`: scope state that `activateMutation` swaps by value),
`evaluateUpdateAssignments` and `evaluateInsertValuesRow` (they call the
root default evaluators), and `preflightUpdate`/`preflightMerge` (root
query preflight). 29c designs a `dml.Host` from the upper features' write
interfaces; 29d moves the executors behind it.

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
and stays in the root package, naming the moved code as `dml.X`.

```sh
make check RUN='^TestInsertConflictPredicate'
make check RUN='^Test(Insert|Update|Merge|Delete|Truncate|Copy|Foreign|InheritanceDML|CTE)'
make check RACE=1 RUN='^Test(Truncate|ForeignAction|Copy)'
```

Public behavior is covered by the dual-mode reference suite:

```sh
make check RUN='^TestReference(Insert|Update|Delete|Merge|Copy|Truncate)'
make check RUN='^TestReference(CTE|ForeignKey|ForeignAction)'
```
