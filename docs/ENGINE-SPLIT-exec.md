# Engine split, step 11: `internal/engine/exec`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-exec.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §6 step 11; §3 (L7); §4.1
  patterns 3 and 4; §7 decision 8.
- **Needs:** 10c (`plan` exists). 13a (scope slots) is strongly recommended
  first: it shrinks the scope surface that 11b must cover. Read the step 10
  Status section (the `EvalContext` group record of 10a and the node runtime
  of 10b) and the `lock` and `visibility` READMEs (lock order, snapshot
  retention).
- **Effort:** xhigh for 11a and 11b (locks, snapshots, cancellation); high
  for 11c and 11d.
- **Session size:** one sub-step per session. 11c and 11d are broad
  rewrites.

## Goal

The query executor and the concrete evaluation context move into `exec`. The
executor reaches statement, session and engine services only through one host
interface that the root package implements. The root package keeps statement
dispatch, the DML executors and cursors.

## Inventory (measured at `75fe0d710`; 11a re-measures)

**Execution files** (42 files, about 12.7k lines): `query.go` (1,714),
`query_sources.go` (501), `query_lock_views.go` (808),
`query_join_legality.go` (819; binder-side checks, decide in 11a),
`query_subquery.go` (671), `query_join.go` (438), `query_parallel.go` (428),
`query_graph.go` (400), `query_recursive.go` (300), `query_table_create.go`
(292; CREATE TABLE AS, stays), `query_producer.go`, `query_set.go`,
`query_row_marks.go`, `query_row_lock.go`, `query_presence.go`,
`query_cte.go`, `query_hash.go`, `query_distinct.go`, `query_cycle.go`,
`query_limit.go`, `query_frames.go`, `query_subquery_capture.go`,
`query_distinct_on.go`, `query_pending_locks.go`, `query_window_prune.go`,
`query_virtual.go`, `query_derived.go`,
`query_lock_order.go`, `query_lock_versions.go`, `query_ties.go`,
`query_authority.go`, `query_detach.go`, `query_join_absence.go`,
`string_set_query.go` (955, function source cursors), `window_query.go`
(684), `window_value_query.go`, `window_ntile_query.go`,
`window_offset_query.go`, `aggregate_query.go` (497), `aggregate.go`,
`cte_mutation_execute.go` (322; data-modifying CTEs, stays with DML).

**Entry and switch.** `finalizeSelectQuery` (`query_parallel.go:17`, called
from `execute_bound.go`) → `finalizeQuery` (`query.go`, 4 callers) →
`openQueryProjectStage` → `openQueryNode` (27 call sites) →
`openRetainedQueryNode`, a 20-case type switch over plan nodes.

**Runtime records.**

- `querySource` (`query.go:497`, 59 fields): visibility view, row versions,
  index roots, anonymous rows (catalog relations), foreign source, hash
  indexes, `scope *StatementScope` (41 uses in 16 files), `authority
  *queryAuthority`, lock context and lineage, nested roots, join, window and
  presence state.
- `queryState` (`query.go:746`): `source`, `invocation *EvalContext` and
  `outer *EvalContext`, parameters, environments, locking, CTE invocation,
  hash owner, memberships, execution context.
- `indexedStage` (`query.go:790`, embeds an `EvalContext` by value),
  `finalRows` (`query.go:1370`), `RowCursor` and `RowResult` (`query.go:29`,
  `:34`; `RowResult` is part of the public facade).
- Other `RowCursor` implementations: `sqlCursor`, `protocolCopyQueryRows`,
  `mergeRows`, `foreignKeyRows`, `functionSetCursor`,
  `copyInsertionCursor`.

**Scope and engine reach.**

- `EvalContext.liveScope()` (`query_authority.go:37`) is called in 37
  files: `liveScope().op` 10, `commentCatalog` 5, `queryLocks` 4, `output`
  2, `deparsePath` 2, `conversionProcedure` 1. `expression.go` also wires the
  scope into the output environment (`output.Regclass = c.liveScope()` and
  eight siblings).
  Since 13a, the inquiry fields are `inquiry.commentCatalog` and
  `inquiry.deparsePath`, and `inheritanceAlter` is `inheritance.alter`.
- `.scope.` selectors: `op` 20, `input` 5, `insertConflict` 3,
  `acquireSequence` 3, `inheritanceAlter` 2, `copyIO` 2, `copyCommandError`
  2, and once each `timeoutRef`, `stagePrivateColumnShape`,
  `stagePreparedColumnShape`, `replacementRead`, `protocolTerminalIO`,
  `owner`, `enterProtocolProducer`, `leaveProtocolProducer`,
  `captureProtocolOutput`, `active`.
- `Engine` methods called from execution files: `privateCatalogLocked` 6,
  `captureCommentSnapshotLocked` 5, `executeBoundCommandLocked` 4,
  `executePreparedCommandLocked` 3, `authorizationLocked` 3,
  `queryTableRefsLocked`, `closeQuerySourceSetLocked`,
  `captureVisibilitySnapshotLocked`, `captureQuerySourcesLocked` (2 each),
  and once each `visibilitySnapshotLocked`, `ValidateCatalogStage`,
  `stageRowTypeLocked`, `stageQueryTableRows`, `stageCatalogOverlay`,
  `sequenceGeneratorLocked`, `rowSecurityInputLocked`,
  `reownStagedTypeGenerationsLocked`, `referencePath`, `publishQueryTable`,
  `publishCatalogOperation`, `privateStorageLocked`,
  `prepareCTECommandsLocked`, `operationRolesLocked`,
  `openRelationSourceLocked`, `normalizeRuleCTEsLocked`.
- `StatementScope` methods called: `activateMutation` 5, `ruleScratch` 3,
  `installRuleScratch` 3, `inheritanceRead` 3,
  `refreshRelationGrantsLocked` 2, `partitionCaptureCatalog` 2,
  `acquireDataViewsLocked` 2, and once each `physicalRowEvent`,
  `mutationIndex`, `latestQueryRow`, `finishForeignOwner`, `active`,
  `acquireRelationTarget`, `acquireQueryRow`, `acquireDefinitionProviders`,
  `acquireAdmittedQueryRelations`.

**Capability implementations on `*EvalContext`** that reach engine state:
the 25 `expr.Context` methods, `functions.ACLRuntime` (`acl_runtime.go`),
`CheckDomain` (`sqltype` domain checks), and the Tier C capabilities added
by the remaining Tier C parts.

## Sub-steps

### 11a — Refine (docs only)

1. Re-run the appendix tool with `querySource`, `queryState` and
   `indexedStage` as extra roots. List every path from the execution files to
   `StatementScope`, `Engine`, `Session`, `Operation` and
   `transactionOwner`.
2. Classify each scope and engine use as one of: lock admission, private
   catalog or storage overlay, snapshot capture or retention, authorization,
   comment and deparse snapshots, protocol and COPY I/O, sequence
   acquisition, foreign owner, DML or rule hooks (these mark code that is not
   query execution and stays).
3. Decide the move set: which of the 42 files move, which stay
   (`query_table_create.go`, `cte_mutation_execute.go`, probably
   `query_join_legality.go` and `query_subquery_capture.go`, which are
   binder-side), and where `RowResult` lives (§7 decision 11).
4. Write the `execHost` method list, grouped, each with its callers and why
   it cannot be a value. Settle §7 decision 8 (one statement-host type in the
   root package, or one per consumer).
5. Write the 11b–11d commit sequence into this file and wait for approval.

### 11b — Host seam in place

1. Declare the engine-local `execHost` interface from 11a; implement it on a
   small root adapter that wraps the statement scope.
2. Replace `querySource.scope`, `EvalContext.statementScope` and the
   `liveScope()` results with the host, call site by call site, keeping lock
   order and error order. `liveScope()` becomes `liveHost()`.
3. Executor calls to `Engine` methods go through the host as well. Methods
   that release `Engine.mu` (for example `openRelationSourceLocked`) keep
   their unlock contract and document it on the host method.
4. The output environment wiring (`output.Regclass = c.liveScope()`, ...)
   passes the host or a presentation value; check what the reg* formatters
   need.
5. Run `RACE=1` on cancellation, lock wait and cursor tests after each
   commit.

### 11c — Export in place, create `exec` and move (broad)

1. Export in place the fields and methods of the moving records with the
   type-checked rename tools.
2. Move the 11a set: the runtime records, `openQueryNode` and the node
   openers, the cursors that implement `RowCursor` for queries, and
   `EvalContext` as `exec.Context` with its capability methods and the
   step-10 `Eval*` bodies. The capability implementations that need the
   engine call the host.
3. Leave aliases in the engine. Check imports: `plan`, `expr`, `functions`,
   `catalog`, `storage`, `visibility`, `lock`, `access`, `ids`, `parser`,
   `sqltype`, `sqlstate` and the standard library. Never `analyze`,
   `session` or the root package.

### 11d — Remove the aliases (broad)

Use the alias tool; audit locals named `exec`.

## Stays in the engine

`executeStatement`, `executeBoundLocked`, `executeBoundCommandLocked`,
`executePreparedCommandLocked` (statement dispatch); the DML executors
(`insert_*`, `update*`, `delete*`, `merge*`, `copy*`; postponed `dml`
package); data-modifying CTE execution; CREATE TABLE AS; SQL cursors and
protocol portals (step 13); rule execution.

## Invariants

- Rows, row order, errors, SQLSTATEs and notices stay identical.
- Lock acquisition order, lock modes and wait behavior are unchanged; the lock
  manager's mutex is never taken under `Engine.mu`.
- Snapshot retention: repeatable read keeps retaining every immutable root
  before analysis waits; read committed views stay fresh at grant time (see
  the `visibility` README). No snapshot copies row values.
- Cancellation is checked at the same points; cursors release resources on
  every exit.
- No new allocations per row (`make bench` plus a scan-heavy reference
  selection).

## Targeted tests

- Engine units: grep `*_test.go` for `openQueryNode`, `querySource`,
  `queryState`, `finalizeQuery`, `RowCursor`. Run with `RACE=1` for
  locking, cancellation and cursors.
- Reference selections (quick mode and `REFERENCE=1`; grep `reference/`):
  SELECT, joins (all kinds), subqueries, CTEs (recursive, SEARCH/CYCLE), set
  operations, DISTINCT/DISTINCT ON, LIMIT/OFFSET/FETCH WITH TIES, windows,
  aggregates and grouping sets, row locks (`FOR UPDATE`, `SKIP LOCKED`,
  `NOWAIT`), cursors, isolation levels, and function scans.
- `make bench` after 11b and 11c.

## Documentation

- Create `internal/engine/exec/README.md` (11c): the runtime records, the
  host interface and its contract (lock and unlock rules per method), what
  stays in the engine, `## Testing`.
- `docs/design/PG18-ARCH-QUERY-EXECUTION.md` and `docs/QUERIES.md` where
  they name moved types; the `plan` README (execution now in `exec`); the
  `access` README if the binding walkers' notes change.
- `AGENTS.md`: add `exec` to the shared subpackage bullet; shorten the
  `internal/engine` bullet ("binding/execution" becomes "binding" until step
  12).

## Done when

`exec` imports nothing above L7. The root package no longer opens plan nodes
or names `EvalContext` internals. The checks in §8.3 pass, including
`RACE=1` selections.

## Status

- [ ] 11a — Refine (docs only)
- [ ] 11b — Host seam in place
- [ ] 11c — `exec` created and executor moved
- [ ] 11d — Aliases removed

Outstanding aliases: none yet.

Notes for later steps:

- Step 12: the binder evaluates constants during preflight and folding
  through an evaluation context; decide in 12c whether `analyze` imports
  `exec` for it.
- `access` B5: the binding walkers (`checkBinding`, `checkQuerySources`,
  `checkViewRowMarks*`) read lock modes and query leaves; they follow step 12.
