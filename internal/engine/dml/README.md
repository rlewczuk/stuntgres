# internal/engine/dml

The write executors (INSERT, ON CONFLICT DO UPDATE, COPY FROM, UPDATE, DELETE,
MERGE, foreign DML), unique and exclusion probing, the staging loops, FK
enforcement and actions over `FinishHost`, and the pure pieces they use (ON
CONFLICT arbitration, the INSERT write overlay, RETURNING, inheritance targets,
TRUNCATE and FK action records, COPY insertion batching).

Layer L13 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture). It imports `access`,
`analyze` (payload types and pure functions only), `authorization`, `catalog`,
`copyfmt`, `exec`, `expr`, `fdw`, `ids`, `index`, `lock`, `partition`, `plan`,
`session` (the operation's COPY input state), `storage`, `stored`, `table`, `view`,
`visibility`, `parser`, `sqlstate`, `sqltype` and the standard library. Nothing
below it imports it; it never imports the engine and never builds a binder.

## Hosts

`Host` (`host.go`) embeds `exec.Host`, `index.WriteHost` and `fdw.WriteRuntime`
and adds command, stage, row-lock, presentation and row/statement trigger capabilities.
The engine's `statementHost` implements them (`statement_host_dml.go`,
`statement_host_stage.go`, `statement_host_latest_row.go`, `statement_host_trigger.go`).

- `Statement`: the active command, target, private rows, snapshot, unique
  journal, query authority, scopes, lock ticket, WHERE CURRENT OF row,
  disclosure environment and `Operation`. It never locks or waits and is read
  at call time.
- `Stage`: `Active` (checked before any candidate validation),
  `ReadmitRevalidation` (recaptures write authority under `Engine.Mu`, then
  readmits programs unlocked after a stale binding) and the write windows
  `PrepareReplacement`, `StageInsert`, `StageReplacement`,
  `AppendAcceptedInsert` and `Delete`. Each window takes the owner mutex and
  `Engine.Mu` itself and never waits.
- `RowLocks`: `AcquireRow`, `AcquireUpdateRow` and `AcquirePartitionLock` wait
  with the caller holding nothing; `LatestRow` reads one row per `RowKind`
  under one lock.
- `Presentation`: `RowDetail`, `SchemaName`, `CaptureRegclassOutput`.
- `BeforeRow`: captures physical relation trigger provenance under the engine
  mutex, then evaluates WHEN and PL unlocked in the same statement stage.
  UPDATE OF captures physical lexical target attributes once per command; when
  a BEFORE UPDATE row hook exists, PostgreSQL's conservative generated-column
  recomputation also includes every generated attribute in that firing vector.
  Ordinary non-generated OF columns still require lexical SET membership.
  Final generation updates both physical storage and named RETURNING frames.
  Producers invoke it once after defaults/latest OLD admission and before
  generation, proposal checks and output. Suppression skips the operation and
  its counts; DELETE ignores non-NULL returned contents. Accepted INSERT rows
  use incremental staging when triggers require same-statement visibility.
  Partition capture records applicable physical INSERT chains once over its
  admitted target subtree. Without RETURNING or such a chain, INSERT retains
  batch ownership and allocates no incremental output; leaf-only INSERT chains
  require accepted prefixes even when the named parent has no trigger.
  Foreign trigger inserts use the existing participant row by row, making each
  accepted insertion available before the next BEFORE invocation without
  introducing another transaction or changing participant rollback guarantees.
- `AfterRow` queues an accepted physical image after successful staging and before
  RETURNING. WHEN evaluates at admission on completed generated values; bodies
  run through the query owner's completion, never in retryable staging windows.
  `AfterStatement` registers logical target/event independently of row count,
  without selecting replication-role modes before row production. Successful
  producers report exhaustion through `CompleteAfterStatement`, where core checks
  current mode eligibility before WHEN, without draining the shared query.
  Core qualifies exhausted modifiers immediately;
  a suspended RETURNING source retains candidates until consumer EOF or auxiliary
  exhaustion. Repeated target/event completions replace prior decisions, including FALSE.
  Physical movement emits DELETE and INSERT row events while the named command
  retains its UPDATE statement event. Explicit timing/level filters keep AFTER
  definitions out of BEFORE generation and suppression decisions.

`FinishHost` adds `ForeignKeys`, the FK runtime's engine side
(`statement_host_foreign_key.go`). Mutating RI commands register statements even
for zero matches and qualify each producer at exhaustion. Like SPI execution
without a separate trigger boundary, their AFTER bodies remain in the caller's
query frame: all RI changes precede draining, and later same-target/event statement
sets supersede earlier ones. Helper SQL still owns independent nested frames.
`Staged` never locks; the job, authority and
row-image captures and the internal action windows each lock once and never
wait; `GrantForeignKey` waits with the caller holding nothing, then locks to
revalidate; `SwapForeignActionAuthority` swaps the running action's authority
slot. `Authority` is the statement authority the FK runtime checks and
evaluates with (`ActingAs` gives the referencing table owner's); the engine's
`*AuthorizationView` implements it and is also an `exec.Context`'s
`RoutineAuthority`.
RI execution uses the FK descriptor's actual event slot: DELETE/UPDATE parent
actions use 0/1, INSERT/UPDATE child checks use 2/3. Effective replication role
gates each slot independently, including final parent checks, so a disabled
action is not undone by an unconditional later check. Old/Next transition
identity determines the event even for updates of transaction-local inserts.
Eligibility is captured in the immutable row event before settlement. Suppression
vectors are shared within an unchanged relation/catalog/effective-role capture,
not mutated when settings or definitions change; all-enabled captures remain nil.
Vectors are sorted once by constraint identity when captured and searched by
binary lookup, rather than rescanned for each FK/slot during settlement.
Reached trigger-body statements, routine helpers and source reads retain the
referencing-table owner's RI authority through description, prepared analysis
and execution, including live role recapture after unlocked provider preflight.
Each recapture checks current ACLs as that owner; neither the initiator nor
trigger-function ownership supplies body privileges.
SET DEFAULT actions evaluate the actual referencing-table recipe as its current
owner and restore the initiating authority on every exit. Default preparation
retains consumed original calls even when immutable folding erases their nodes.
Readmission after a row or unique wait and at final publication rechecks those
calls as the child owner without replaying default or CHECK truth; failure
publishes neither staged rows nor counts.
COPY uses its importing caller, never the target owner, through the same staging
and final-publication fences. Input conversion/CHECK diagnostics own the active
row text; buffered insertion retains only each candidate's origin line, so a
post-wait admission failure has line context without raw input.

## Command state

`Command` (`command.go`) is the active command's DML state grouped by owner
(`Indexes`, `Insert`, `Partition`, `InheritedTargets`, `ViewWrite`, `RLS`), plus
target refs, storage, capture source, the INSERT write overlay and insertion
groups. Partition routing capture is shared by INSERT/COPY/UPDATE; only physical
mutation leaves enter a data view. The engine embeds the command by value in its
per-command mutation, so writable CTEs, rules and SQL routines swap it whole.
AFTER queues deliberately do not belong to this swapped command: core owns them
by query effect identity, shared across writable CTEs and separate for nested SQL.
`Insert.CopyFrom` points at the single `CopyFromState`; ordinary INSERT has none.

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

Static PL capture may attach `CaptureCardinality` to the existing active
`Command`. RETURNING producers call `CapturedRow` only after successful complete
projection; its second row signals the structured private error. Command swaps
isolate nested writes and writable-CTE auxiliaries. Ordinary SQL has no counter
and retains its normal output and affected-count semantics.

Prologue decisions run over values the root captures in its own window.
Package-internal helpers keep lower-case names.

## Execution order

- Executors run a captured input with the caller holding nothing. UPDATE and
  DELETE freeze their candidates (DELETE ... USING its target/source pairs)
  before the first row wait; MERGE completes its source and evaluates ON before
  any action wait. Per candidate they take the row lock, reread the latest
  version, recheck the predicate if the row moved, then evaluate, validate,
  claim unique keys and stage. MERGE stages its actions in action order.
- Retained routine/rule query sources use the existing `view.FreezeWriteSources`
  bridge after raw-source selection and before opening their immutable plans.
  MERGE applies this to its actual `Binding.QuerySources` even when the target's
  layout is unchanged. Its input cleanup follows the returned bridge/bundle head,
  including early errors, rather than closing only the pre-bridge source.
  Existing membership and `Outcome.finishSources` finalization still run before
  input/target cleanup, preserving foreign producer errors and admission authority.
  Nil/identical source layouts keep the bridge's no-allocation fast path.
  Foreign INSERT/UPDATE/DELETE owns the same returned head after the engine
  hands off captured inputs; planning/begin failures close the original input.
  Foreign INSERT borrows its command invocation for query-project assembly, so
  closing that reader cannot finish sources before checks and RETURNING. The
  exported `EvaluateInsertQuery` remains consuming for ordinary INSERT callers.
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
- Partition UPDATE runs the source BEFORE UPDATE chain before source generations
  and membership selection. Staying rows use source constraints and `ReplaceRows`;
  departing rows route only within the named target. Their source BEFORE DELETE
  chain precedes deletion; destination BEFORE INSERT sees that private deletion.
  A DELETE veto preserves OLD. An INSERT veto leaves the deletion but contributes
  no UPDATE count or RETURNING row. Destination replacements cannot reroute and
  must pass generation, row-policy and row-constraint checks before selected-leaf
  membership checks, then view and index checks. A table CHECK violation wins
  over an invalid partition key in both ordinary and trigger-modified rows.
  A move takes a delete-strength grant and passes the original captured deletion
  identity to `Delete`, which refreshes the current predecessor internally.
  `ApplyValidated` stages the fresh destination identity in the same write stage;
  INSERT/COPY groups are not UPDATE batches. Only Finish publishes accepted effects.
  Committed movement end markers distinguish concurrent movement from ordinary
  deletion without following a destination row.
- COPY FROM runs omitted defaults before rejection/limit admission; the shared
  post-default stage rejects or filters before generation, routing and
  constraints. Reached DEFAULT markers execute exactly once, in COPY-list order.

## Invariants

- The package holds no engine state and no mutex. It waits only through `Host`
  methods (`ClaimUniqueKey`, `WaitTransaction`, the row locks) with the caller
  holding nothing; executors run after `Engine.Mu` is released.
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
- The FK runtime runs from root or bounded nested-command completion, with the
  caller holding nothing; all waits go through `GrantForeignKey`. Accepted
  predecessor/successor events retain command ownership and independent action/
  check progress. Settlement follows newly appended causal events to a fixed
  point; completed actions are not replayed. Candidate checks use owned final
  child versions and parent old images, while probes retain full private
  visibility. `ActingAs` copies authority once per job/action, never per row.

## What stays in the engine

- Statement dispatch, source capture under `Engine.Mu`, `WriteStage` with the
  `Stage` windows and row publication, writable-CTE orchestration, and the
  locked prologues that turn an `Outcome` into a `Result`
  (`executeInsertLocked`, `execute_bound.go`, `executeForeignCommandLocked`).
- The COPY protocol: `copyProtocol`, input capture, the default loop around
  `FoldDefault`, `copyIO` and COPY TO output.
- Host windows: row lock waits, `LatestRow` with its lock and epoch checks, and
  the authority capture behind `RowDiagnosticDetail`.
- Shared command-completion order: due exclusion events, `SettleForeignActions`,
  exclusion events again when an action added one, then `CheckForeignKeys`
  unless only catalog roots were projected. The engine selects command ownership
  and root publication; the FK row/grant bodies and action admission are shared
  with ALTER TABLE and CREATE TABLE.
- TRUNCATE execution and `truncateGraphFence` (fence kind order in
  `catalog_fence.go`).

## Testing

Unit tests here cover the ON CONFLICT predicate proofs, target admission,
`CompleteCTEMutation`, COPY filter row admission and the routed RLS proposal
layout. Other DML units need an engine and live in `internal/engine/core`
(INSERT, UPDATE, DELETE, MERGE, TRUNCATE and COPY statements) or
`internal/engine`, naming the code as `dml.X`: host seams (`statement_host_dml_test.go`,
`statement_host_foreign_key_test.go`), executors, probing, FK runtime, COPY and
cancellation. Allocation guards: `BenchmarkInsertConflictCommittedOmissions`,
`BenchmarkInsertConflictUpdateDistinctStage`, `BenchmarkUpdateCoreSparse` and
`TestUpdateCoreSparseAllocation`. Public behavior lives in
[`reference/dml`](../../../reference/dml/README.md#testing).

Frame names matched by tests: range/list publication tests cancel at
`dml.ApplyInsertValidated`; partition UPDATE publication cancels at
`dml.ApplyValidated` after source deletion. Renaming these breaks the probes.

```sh
make check RUN='^Test(InsertConflict(Predicate|Truth)|DMLTargetAdmission$$|CTEDMLInvocationOwnership$$)'
make check RUN='^TestStatementHost'
make check RUN='^Test(UpdateCore|UpdateFrom|UpdateRow|MergeNothing|InsertConflictUpdate|InsertReturning)'
make check RACE=1 RUN='^Test(Unique|Exclusion|InsertConflict)'
make check RACE=1 RUN='^Test(Range|List)PartitionPublication'
make check RACE=1 RUN='^TestPartitionRouting(Capture|Publication|LatestRow)'
make check RUN='^Test(ForeignActions|ForeignKey)'
make check RUN='^Test(RLSRoutedProposalLayout|CopyFilter(RowAdmission|InputProvenance|FinishVeto)|ProtocolCopyCancellationContext)$$'
```
