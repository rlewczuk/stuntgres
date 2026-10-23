# internal/engine/dml

The write executors (INSERT, ON CONFLICT DO UPDATE, COPY FROM, UPDATE, DELETE,
MERGE, foreign DML), unique and exclusion probing, the staging loops, FK
enforcement and actions over `FinishHost`, and the pure pieces they use (ON
CONFLICT arbitration, the INSERT write overlay, RETURNING, inheritance targets,
TRUNCATE and FK action records, COPY insertion batching).

Layer L13 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture). It imports `access`,
`analyze` (payload types and pure functions only), `authorization`, `catalog`,
`copyfmt`, `exec`, `expr`, `fdw`, `ids`, `index`, `lock`, `partition`, `plan`,
`session` (the operation's COPY input state), `storage`, `table`, `view`,
`visibility`, `parser`, `sqlstate`, `sqltype` and the standard library. Nothing
below it imports it; it never imports the engine and never builds a binder.

## Hosts

`Host` (`host.go`) embeds `exec.Host`, `index.WriteHost` and `fdw.WriteRuntime`
and adds four parts, each with one owner. The engine's `statementHost`
implements it (`statement_host_dml.go`, `statement_host_stage.go`,
`statement_host_latest_row.go`).

- `Statement`: the active command, target, private rows, snapshot, unique
  journal, query authority, scopes, lock ticket, WHERE CURRENT OF row,
  disclosure environment and `Operation`. It never locks or waits and is read
  at call time.
- `Stage`: `Active` (checked before any candidate validation),
  `ReadmitRevalidation` (recaptures write authority under `Engine.mu`, then
  readmits programs unlocked after a stale binding) and the write windows
  `PrepareReplacement`, `StageInsert`, `StageReplacement`,
  `AppendAcceptedInsert` and `Delete`. Each window takes the owner mutex and
  `Engine.mu` itself and never waits.
- `RowLocks`: `AcquireRow`, `AcquireUpdateRow` and `AcquirePartitionLock` wait
  with the caller holding nothing; `LatestRow` reads one row per `RowKind`
  under one lock.
- `Presentation`: `RowDetail`, `SchemaName`, `CaptureRegclassOutput`.

`FinishHost` adds `ForeignKeys`, the FK runtime's engine side
(`statement_host_foreign_key.go`). `Staged` never locks; the job, authority and
row-image captures and the internal action windows each lock once and never
wait; `GrantForeignKey` waits with the caller holding nothing, then locks to
revalidate; `SwapForeignActionAuthority` swaps the running action's authority
slot. `Authority` is the statement authority the FK runtime checks and
evaluates with (`ActingAs` gives the referencing table owner's); the engine's
`*AuthorizationView` implements it and is also an `exec.Context`'s
`RoutineAuthority`.

## Command state

`Command` (`command.go`) is the active command's DML state grouped by owner
(`Indexes`, `Insert`, `Partition`, `InheritedTargets`, `ViewWrite`, `RLS`), plus
target refs, storage, capture source, the INSERT write overlay and partition
insertion groups. The engine embeds it by value in its per-command mutation, so
writable CTEs, rules and SQL routines swap it whole. `Insert.CopyFrom` points at
the single `CopyFromState`; ordinary INSERT has none.

## Entry points

| Area | Files | Entry points |
| --- | --- | --- |
| INSERT | `insert*.go` | `ExecuteInsert` (captured `InsertInput`), `AssembleInsertRows` (any cursor: INSERT ... SELECT, COPY), `AdmitInsertRow`, `ApplyInsertBatch` |
| ON CONFLICT | `insert_conflict*.go`, `insert_write_overlay.go` | `InsertConflictState`, `NormalizeConflictPredicate`, `InsertWriteOverlay`, `AdmitInsertConflictRow`, the DO UPDATE action |
| UPDATE | `update*.go`, `row_security.go` | `ExecuteUpdate`, `ValidateMutationRow`, `MutationMatches`, `EvaluateUpdateAssignments` (shared with DO UPDATE and foreign DML) |
| DELETE, MERGE | `delete*.go`, `merge*.go` | `ExecuteDelete`, `ExecuteMerge`, `AssembleMergeRows`, `StageMergeRows`, `SelectMergeArm` |
| Foreign DML | `foreign.go`, `foreign_prologue.go` | `ExecuteForeign`, `ForeignCommandPrologue` |
| Probing | `unique.go`, `exclusion.go`, `index_candidates.go` | `ProbeUniqueRow`, `ClaimUniqueRow`, `ProbeExclusion` (also SET CONSTRAINTS' deferred recheck), `ValidateIndexCandidates` |
| Staging | `stage.go`, `partition_insert.go` | `ApplyValidated`, `ReplaceRows`, `ApplyInsertValidated` (each leaf group in first-use order) |
| Preflight | `*_preflight.go`, `insert_conflict_update.go` | `PreflightUpdate`, `PreflightMerge`, `PreflightConflictUpdate`, `PreflightReturning`, called from the engine's `execute_bound.go` |
| RETURNING | `returning.go`, `insert_returning.go` | `InsertReturningOutput`, `MergeReturningOutput`, `ProjectForeignReturning` |
| COPY FROM | `copy*.go` | `CopyCursor`, `CopyBinaryCursor`, `CopyInsertionState`; codecs are in [`copyfmt`](../copyfmt/README.md) |
| FK | `foreign_action*.go`, `foreign_key*.go` | `PrepareForeignActions`, `SettleForeignActions`, `CheckForeignKeys`, `ForeignKeyReach` |
| TRUNCATE | `truncate*.go` | `TruncateScope`, `CheckTruncate`, `GuardedCatalogMutationError`, `TruncateGraph.CheckCurrent` |
| Prologue decisions | `latest_row.go`, `default_fold.go`, `cte_completion.go`, `row_diagnostic.go` | `LatestUpdateRow`/`LatestDeleteRow`/`LatestMergeRow`, `FoldDefault`, `CompleteCTEMutation`, `RowDiagnosticDetail` |
| Target admission | `target_admission.go` | `TargetAdmission`, the `table.TargetEntry` of single-table SELECT, COPY and DML |

Prologue decisions run over values the root captures in its own window.
Package-internal helpers keep lower-case names.

## Execution order

- Executors run a captured input with the caller holding nothing. UPDATE and
  DELETE freeze their candidates (DELETE ... USING its target/source pairs)
  before the first row wait; MERGE completes its source and evaluates ON before
  any action wait. Per candidate they take the row lock, reread the latest
  version, recheck the predicate if the row moved, then evaluate, validate,
  claim unique keys and stage. MERGE stages its actions in action order.
- `ApplyValidated` and `ReplaceRows` own the work around the `Stage` windows.
  After a stale unique binding they run `ReadmitRevalidation`, reprobe every row
  (or readmit DO NOTHING) and retry.
- Both INSERT row paths check `RowSecurity.CheckProposal` after generation and
  routing and before constraints; UPDATE checks NEW between generation and
  constraints. OLD policy guards (`MutationMatches`) precede user predicates and
  are re-evaluated after refreshed-row waits. The shared RI validator does not
  apply caller RLS.
- Routed and inherited NEW projection uses the destination's admitted
  physical-to-named layout and copies finalized values by index into a private
  buffer, never into scratch still borrowed by OLD evaluation or RETURNING. No
  row matches columns by name or builds a second layout.
- COPY FROM runs omitted defaults before rejection/limit admission; the shared
  post-default stage rejects or filters before generation, routing and
  constraints. Reached DEFAULT markers execute exactly once, in COPY-list order.

## Invariants

- The package holds no engine state and no mutex. It waits only through `Host`
  methods (`ClaimUniqueKey`, `WaitTransaction`, the row locks) with the caller
  holding nothing; executors run after `Engine.mu` is released.
- No pointer to a per-row stack value (a probe candidate, a one-row batch array)
  crosses a `Host` method: through the interface it escapes to the heap for
  every row. Pass a copy and apply the result here (`PublishExclusion`,
  `WriteCandidate.RecordExclusion`).
- Messages, details, SQLSTATEs and the first-error choice are compatibility
  contracts, including cardinality errors and COPY's line context. COPY never
  flattens an already-contextual primary error or lets pending cancellation
  replace it, and keeps the 57014/FATAL mapping through `CopyCommandError`.
- A failure-only `RowDetail` copies the binding, so only the error path
  allocates. The host suppresses failing-row values under metadata-only RLS
  eligibility. RI NOT NULL/CHECK Detail under FORCE stays owner-visible only in
  the validated child-owner settlement context; FK key disclosure keeps its own
  RLS privacy checks.
- The FK runtime runs only from Finish, with the caller holding nothing; all its
  waits go through `GrantForeignKey`. Each FK job costs one heap row image, and
  `ActingAs` copies the authority once per job or action, never per row.

## What stays in the engine

- Statement dispatch, source capture under `Engine.mu`, `WriteStage` with the
  `Stage` windows and row publication, writable-CTE orchestration, and the
  locked prologues that turn an `Outcome` into a `Result`
  (`executeInsertLocked`, `execute_bound.go`, `executeForeignCommandLocked`).
- The COPY protocol: `CopyProtocol`, input capture, the default loop around
  `FoldDefault`, `copyIO` and COPY TO output.
- Host windows: row lock waits, `LatestRow` with its lock and epoch checks, and
  the authority capture behind `RowDiagnosticDetail`.
- FK order in Finish: exclusion events, `SettleForeignActions`, exclusion events
  again when an action added one, then `CheckForeignKeys` unless the statement
  only projected catalog roots. The FK row and grant bodies and internal action
  admission are shared with ALTER TABLE and CREATE TABLE.
- TRUNCATE execution and `truncateGraphFence` (fence kind order in
  `catalog_fence.go`).

## Testing

Unit tests here cover the ON CONFLICT predicate proofs, target admission,
`CompleteCTEMutation`, COPY filter row admission and the routed RLS proposal
layout. Other DML units need an engine and live in `internal/engine`, naming the
code as `dml.X`: host seams (`statement_host_dml_test.go`,
`statement_host_foreign_key_test.go`), executors, probing, FK runtime, COPY and
cancellation. Allocation guards: `BenchmarkInsertConflictCommittedOmissions`,
`BenchmarkInsertConflictUpdateDistinctStage`, `BenchmarkUpdateCoreSparse` and
`TestUpdateCoreSparseAllocation`. Public behavior lives in
[`reference/dml`](../../../reference/dml/README.md#testing).

Frame name matched by tests: the range and list partition publication tests
cancel at the `dml.ApplyInsertValidated` frame; renaming it breaks them.

```sh
make check RUN='^Test(InsertConflict(Predicate|Truth)|DMLTargetAdmission$$|CTEDMLInvocationOwnership$$)'
make check RUN='^TestStatementHost'
make check RUN='^Test(UpdateCore|UpdateFrom|UpdateRow|MergeNothing|InsertConflictUpdate|InsertReturning)'
make check RACE=1 RUN='^Test(Unique|Exclusion|InsertConflict)'
make check RACE=1 RUN='^Test(Range|List)PartitionPublication'
make check RUN='^Test(ForeignActions|ForeignKey)'
make check RUN='^Test(RLSRoutedProposalLayout|CopyFilter(RowAdmission|InputProvenance|FinishVeto)|ProtocolCopyCancellationContext)$$'
```
