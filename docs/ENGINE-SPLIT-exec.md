# Engine split, step 11: `internal/engine/exec`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-exec.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §6 step 11; §3 (L7); §4.1
  patterns 3 and 4; §7 decisions 8 and 11.
- **Needs:** 10c (`plan` exists). 13a (scope slots) is strongly recommended
  first: it shrinks the scope surface that 11b must cover. Read the step 10
  Status section (the `EvalContext` group record of 10a and the node runtime
  of 10b) and the `lock` and `visibility` READMEs (lock order, snapshot
  retention).
- **Effort:** xhigh for 11a and 11b (locks, snapshots, cancellation); high
  for 11c and 11d.
- **Session size:** one sub-step per session. 11c and 11d are broad
  rewrites.
- **Second round (planned at `c154e14fb`):** 11e (linear item 75) needs
  nothing; 11f (item 76) needs 11e; `index` 23b needs 11e.

## Goal

The query executor and the concrete evaluation context move into `exec`. The
executor reaches statement, session and engine services only through one host
interface that the root package implements. The root package keeps statement
dispatch, the DML executors and cursors.

## Inventory (re-measured in 11a at `d76cc93a1`)

The first inventory (at `75fe0d710`) treated its `query*`, `window*` and
`aggregate*` files as execution code (it said 42 files but named 41; all
still exist). 11a measured them with go/types (tools
and outputs in `.cswd/tmp/PG18-ENGINE-SPLIT-EXEC-REFINE/`; the "11a result"
below has the details). They hold four kinds of code:

1. **Runtime** (moves): the records, `finalizeSelectQuery` →
   `finalizeQuery` → `openQueryProjectStage` → `openQueryNode` →
   `openRetainedQueryNode` (20 cases), the node openers and producers, row
   lock arbitration, the cursors, and `EvalContext` with its interface
   methods.
2. **Binder methods** (stay for step 12): `expressionBinder.enterQuery`,
   `frameID`, `queryColumnInFrame`, `queryTargetRelation`
   (`query_frames.go`), `bindQueryBounds`/`bindQueryBound` (`query_limit.go`),
   `bindSubquery`, `wholeRowPresence`, `scheduleQueryProjection`,
   `windowValueAliases`, `queryScopeState.outerQualifiers`, and all of
   `query_subquery_capture.go`.
3. **Admission and capture** (stay; they build the runtime records):
   `Engine.captureQuerySources(ForInvoker)Locked`, `captureHashIndexesLocked`,
   `captureSequenceSourceLocked`, `capturePGSequencesLocked`,
   `captureForeignSourceLocked`, `initializeQueryLocksLocked`,
   `firstQueryRowModeLocked`, the comment/visibility snapshot captures,
   `closeQuerySourceSetLocked`, `StatementScope.acquire(Admitted)QueryRelations`
   and `validateQuerySources`, `queryLockAdmission` with `queryLockTargets`
   and `firstSetRowClause`, the view row-lock legality checks and
   `AuthorizationView.checkViewRowMarks*` (they stayed in access B5: they
   walk the engine's view definitions).
4. **Preflight** (stays): `preflightQuery`, `preflightJoin`, `preflightSet`,
   `preflightDistinct`, `preflightDistinctOn`, `preflightFunctionFrom` and the
   `preflightCoercions` family. It is statement preparation, called from
   `execute_bound.go`, `merge.go`, `update.go`, `view_query.go` and
   `string_set_lower.go`, never from an opener, and it reaches routine
   admission (`selectExpressionRecipe` → `routineAdmissionAuthority` →
   `AuthorizationView`). One runtime call exists: `openJoinNode` re-runs
   `preflightCoercions` on the join predicate when it opens.

Whole files that stay: `query_table_create.go`, `cte_mutation_execute.go`
(except `queryState.inheritCTEs`, which moves), `query_subquery_capture.go`
and `query_join_legality.go` (its `fullJoinCondition` reaches
`expressionBinder`; the checks run before execution from dispatch, DML and
DDL recipes).

**Counts.**

- Runtime set: 37 files (the 41 minus the four that stay; about 10.1k lines, including the declarations that
  stay in the split files), plus `EvalContext` and its 105 methods in 45
  files. 76 methods implement an interface: `expr.Context`, `plan.Runtime`,
  the seven `functions` runtimes, `sqltype.DomainCheckEvaluator` and
  `pgcatalog.Runtime` (`ObjectACL`). The other 29 are helpers.
- `liveScope()`: 102 calls in 36 files. `querySource.scope`: 27 literal keys
  in 12 files and 23 selectors. `evalStatement.statementScope`: 49 uses in 39
  files. `queryAuthority`: 12 references; `.authority.scope` is read or
  cleared by `sql_cursor.go`, `protocol_portal.go`, `regtype_input.go`,
  `sql_cursor_hold.go`, `sql_routine_scope.go` and `transaction_finish.go`.
- Frontier of the runtime set (with categories 2–4 and the engine-side
  helpers excluded): 76 clean outside declarations it pulls in, 40 dirty
  ones (each becomes a host method or stays behind one), 70 outside fields.
- Inbound: 475 runtime keys are used 3,369 times by engine production code
  (1,282 for the `EvalContext` family, 1,009 for the four records) and
  10,002 times by engine tests.
- Callers that stay in the engine: `openQueryNode` from `delete_using.go`,
  `merge.go`, `update.go` and `fdw_execute.go`; `RowCursor`/`RowResult`
  from `copy*.go`, `insert_query.go`, `merge_rows.go`, `protocol_*.go`,
  `sql_routine.go` and `engine.go`; outside the engine `rows.go`, `conn.go`
  and `cmd/stuntgres/query_output.go` name `RowResult`.

## Sub-steps

### 11a — Refine (docs only)

Done; see "11a result" in Status.

### 11b — Host seam in place

Done; see "11b result" in Status (one deviation: the preparation callbacks).
The plan below is kept as the record.

The engine-local `execHost` interface (the "11a result" lists every method)
is implemented by `statementHost`, a defined type over `StatementScope`
(`(*statementHost)(scope)` is a free conversion). `liveHost()` returns an
untyped nil interface when there is no scope, never a typed nil. Commits, each
building and passing the targeted tests with `RACE=1` on cancellation, lock
wait and cursor selections:

1. **Host type.** Declare `statementHost`, `execHost` (environment and
   presentation group first) and the compile-time assertion; add
   `EvalContext.liveHost()` and `querySource.liveHost()` beside
   `liveScope()` (they wrap it until commit 7). Add `detachedPresentation`, one value implementing the nine
   `sqltype` output formatters with today's nil-scope messages ("missing
   regclass output snapshot", ...); the `StatementScope` formatters return
   its results in their missing-snapshot branch, so each message has one
   source.
2. **Environment and presentation.** `finalizeQuery`,
   `finalizeParallelQuery`, `openForeignStage` (`Environments()`); the
   reg* wiring in `aggregate_query.go`, `FunctionStringOutput`,
   `FunctionFormatOutput`, `FunctionJSONOutput` and `ApplyCoercion`
   (`presentation()`: the host, or `detachedPresentation` when nil);
   `ApplyCoercion`'s borrowed input resolvers; `setContext`
   (`Context()`); `FunctionStatementTime` (`StatementStart()`; the held
   cursor stamp stays first).
3. **Row locks.** `arbitrateViewQuery` calls `AcquireQueryRow` and
   `LatestQueryRow`; `recheckLockedQuery`/`recheckLockedSource` call
   `QueryLocks()`. The `StatementScope` bodies become `statementHost`
   methods unchanged.
4. **Foreign scans and `pg_sequences`.** `openForeignStage` and
   `finishForeignSources` call `ReleaseReplacementRead`,
   `TouchForeignScan` and `EnlistForeign`; `pgSequencesSource.scope`
   becomes the host and `complete` becomes `CompletePGSequence`.
5. **Preparation callbacks.** `openJoinNode` calls `PreflightCoercions`;
   `CheckDomain` and the JSONB record checks reach the domain catalog and
   prepared domain programs through `CheckDomain` on the host.
6. **Capability bodies.** The 36 bodies listed under "Capabilities" move to
   `statementHost` methods taking the context (same files, new receiver).
   Each `EvalContext` method becomes a forwarder that first does what the
   body did first (`evaluationContext(c)` where it ran, then the nil-host
   check with the same error text) and then calls the host. Split into two
   commits if large: `functions` runtimes, then `expr.Context` and
   `plan.Runtime`.
7. **Back-pointers.** `querySource.scope` and `queryAuthority.scope` become
   `host`; root code that detaches (`sql_cursor.go`, `sql_cursor_hold.go`,
   `protocol_portal.go`, `sql_routine_scope.go`, `transaction_finish.go`)
   clears the host, and code that reads the scope back
   (`regtype_input.go`, `sql_cursor.go`) uses `scopeOf(host)`, a checked
   conversion. `evalStatement.statementScope` becomes `host`.
   `liveScope()` is deleted.
8. **Admission slots.** `evalAdmission.routineAuthority`
   (`*AuthorizationView`) and `routineProgram` (`*routineAdmissionProgram`)
   become opaque marker-interface fields that only engine helpers type-assert;
   the borrowed pointers keep their by-value copy semantics. The other
   admission fields have clean types and move as they are.

Then `make bench` and a scan-heavy reference selection: no new allocations
per row (the host is stored once per record, never converted per row).

### 11c — Export in place, create `exec` and move (broad)

Done; see "11c result" in Status (deviations: three more cuts, the
evaluation groups are `EvalFrame`/`EvalStatement`/`EvalAdmission`, and only
the types the engine names are exported). The plan below is kept as the
record.

1. **Split the mixed files** so that every file is wholly runtime or wholly
   engine: binder methods into the matching `*_bind.go` files, admission and
   capture into `query_admission*.go`, the preflight family into
   `query_preflight*.go`, the capture halves of `foreign_scan.go` and
   `pg_sequences.go` into their own files. Clean plan walkers that the binder
   also uses (`walkBoundExpression`, `walkDemandedBoundExpression`,
   `boundExpressionChildren`, `rewriteQueryExpressions`,
   `visitExecutableQuery`, `queryTreeOccurrences`, `functionFromConjuncts`)
   move to `plan` instead, since step 12 needs them too.
2. **Staying methods become functions.** The 29 helper methods split: the
   frame helpers (`bindQueryRow`, `bindQueryIdentity`, `frameRow`,
   `queryOperandSource`, `queryOperandAbsent`, `setQueryPresence`,
   `setContext`, the `subquery*` helpers, `liveHost`) move as methods; the
   engine-side ones (`routineAdmissionAuthority`, `selectExpressionRecipe`,
   the domain-check preparation and evaluation helpers,
   `admitColumnVirtualGeneration`, `columnCapturedGeneration`,
   `expressionPrincipal`, `identifyObjectValues`, `deparseRelation`,
   `evalSerialSequence`, `aclInquiryAuthorizationLocked`) become engine
   functions taking the context (`meth2fn`), because Go allows no methods on
   an imported type.
3. **Export in place** with `movex` member mode: the fields and methods of
   the moving declarations that engine code uses (475 keys).
4. **Move with aliases.** Names: `EvalContext` → `exec.Context`,
   `querySource` → `exec.Source`, `queryState` → `exec.State`,
   `indexedStage` → `exec.Stage`, `finalRows` → `exec.FinalRows`,
   `queryAuthority` → `exec.Authority`, `queryLeaf` → `exec.Leaf`,
   `queryLockContext` → `exec.LockContext`, `execHost` → `exec.Host`,
   `RowCursor`/`RowResult` keep their names, `openQueryNode` →
   `exec.OpenNode`, `finalizeQuery` → `exec.Finalize`,
   `finalizeSelectQuery` → `exec.FinalizeSelect`. The frontier pulls in the
   clean declarations it needs (inheritance segments, `foreignQuerySource`,
   `pgSequencesSource`, `openSourceStage`, `identitySnapshot`,
   `domainCheckProgram`, the quote-capture records, `routineBodyContext`,
   `returningRowContext`, ...); iterate the frontier tool to a fixed point.
   `exec` gets a private `sqlError` copy as `plan` did. Imports: `plan`,
   `expr`, `functions`, `catalog`, `storage`, `visibility`, `lock`,
   `access`, `ids`, `parser`, `sqltype`, `sqlstate` and the standard library;
   never `analyze`, `session` or the root package.
5. **Docs.** `internal/engine/exec/README.md` (runtime records, the host and
   its per-method lock contract, what stays, `## Testing`).

The commits must be re-runnable scripts (as in 10c) so a rebase over another
broad item regenerates them.

### 11d — Remove the aliases (broad)

Done; see "11d result" in Status. The plan below is kept as the record.

Rename locals and parameters named `exec` first (one today, in
`insert_conflict_update_benchmark_test.go`), and alias the `os/exec` import
in `typed_review_regression_test.go` and
`typed_definition_admission_test.go`. Then remove the aliases with the alias
tool, including `engine.RowCursor`/`RowResult`: the driver (`rows.go`,
`conn.go`, tests) and `cmd/stuntgres` import `exec` directly (§7 decision
11).

### 11e — Expression-preparation seam and routine admission (linear item 75)

Added when the postponed packages were planned (measured at `c154e14fb`).
Index, table and partition evaluation code (key programs, CHECK rows,
generated columns, partition recipes; steps 23–25) is dirty only because it
calls root helpers to prepare and admit expressions: `preflightCoercions`
with a policy, `validateExpressionJoinExecution`, `admitRoutineCall`,
`routineAdmissionAuthority` and `scopeOf`. Give them one seam:

1. `exec.Preparation` gains coercion preflight with a policy and
   expression execution admission (names settled in the session, for
   example `PreflightCoercionsWithPolicy` and `AdmitExpression`). Cutting
   only `validateExpressionJoinExecution` cleans nothing in the index
   files: the seam must also cover `preflightCoercionsWithPolicy`, which
   reaches `prepareDomainConversion` and `selectExpressionRecipe`.
   `missingPreparation` gets the matching failing stubs and
   `enginePreparation` (`domain_check.go`) implements them.
2. The routine admission files (`routine_admission.go`,
   `routine_body_admission.go`, `routine_program.go`) are dirty only
   through `routineAdmissionAuthority`, which returns an
   `AuthorizationView`. Make it return the `access.View` and
   `access.Environment` the checks read, then move the admission decisions
   into `exec` (step 14 assigned call admission to `exec`).
   `routineAdmissionProgram` becomes an `exec` record.
3. Move with them: the constraint-violation DETAIL helpers
   (`failingRowDetail`, `writeDiagnosticValue` in `value.go`), the generic
   source capture (`captureQuerySource`, `captureQueryPrefix` in
   `delete.go`) and the cursor scan helpers (`cursorSimpleScan`,
   `cursorOccurrenceVersion`, `cursorOccurrencePhysicalRow` in
   `current_of.go`).

Measure first: `reach -files 'routine_(admission|body_admission|program)\.go'`
and `frontier` over the set. Stop and report if admission needs more than
the view, the environment and the statement context.

### 11f — Preflight (linear item 76)

After 11e the expression preflight files are dirty only through routine
admission: `expression_preflight.go`, `preflight_composite.go`,
`conditional_plan.go` and `conditional_fold.go`. Re-measure, then move them
into `exec` and update the `analyze` and `exec` READMEs, which list
preflight as engine-owned. `query_preflight.go` and `query_set_preflight.go`
name `analyze` symbols, so they cannot move to `exec` as they are: measure
whether they fit `analyze` (it imports `exec`) or stay, and record why.
`preflightReturning` (`returning_preflight.go`) stays with `dml`.
`strict_parameter_null.go` goes with them only if `procedureReturnsSet`
no longer needs the intagg extension provider; otherwise it stays and the
Status record says why.

## Stays in the engine

`executeStatement`, `executeBoundLocked`, `executeBoundCommandLocked`,
`executePreparedCommandLocked` (statement dispatch); query admission and
capture; preflight; the join legality checks; the DML executors (`insert_*`,
`update*`, `delete*`, `merge*`, `copy*`; postponed `dml` package), which
call `exec.OpenNode` for their inputs; data-modifying CTE execution; CREATE
TABLE AS; SQL cursors and protocol portals (step 13); rule execution;
`statementHost` and the capability bodies behind it.

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

`exec` imports nothing above L7. The root package has no node switch and
names no `exec.Context` internals: DML executors open their inputs through
`exec.OpenNode`. The checks in §8.3 pass, including `RACE=1` selections.

## Status

- [x] 11a — Refined (docs only; the commit that records this status)
- [x] 11b — Host seam in place (`220b72d76`, `f93b0f837`, `b82df3645`,
  `f205fff26`, `53c9ff63e`, `6071a1cc4`, `0a3e5e602`, `ccb197d87`,
  `e2d780fc6`, then the docs commit) *(racecheck/finalcheck skipped at user
  request)*
- [x] 11c — `exec` created and executor moved (`45af3aed4`, `79653b057`,
  `0305ed82a`, `7b9306c29`, `8a00419e6`, `ad098af9b`, `6d6267f35`,
  `0bfa0b5c5`, `2e68cbd93`, then the docs commit) *(racecheck/finalcheck
  skipped at user request)*
- [x] 11d — Aliases removed (`4d4e0942e`, `4acc2dac9`, `17fc012f5`,
  `7d6ffd65a`, then the docs commit) *(racecheck/finalcheck skipped at user
  request)*
- [ ] 11e — Expression-preparation seam; routine admission moved
  (linear item 75)
- [ ] 11f — Preflight moved (linear item 76)

Outstanding aliases: none. The engine, the root driver and `cmd/stuntgres`
name the `exec` types directly.

### 11a result

Measured at `d76cc93a1` with go/types: the `reach` and `frontier` tools of
earlier steps (with an edge-cut and an exclude option added), a `paths` tool
that lists every selector chain from the moving declarations into
`StatementScope`, `Engine`, `Session`, `Operation` and `transactionOwner`,
and an `ifaces` tool that maps each `EvalContext` method to the interfaces
declaring it. The Inventory above has the classification and counts.

**Where the runtime reaches the engine.** With the cuts of 11b applied, the
node openers and records reach engine state only through:

| Use | Call sites | Class |
|---|---|---|
| `scope.input`, `scope.output` | `finalizeQuery`, `finalizeParallelQuery`, `openForeignStage` | environment |
| scope as the nine reg* output formatters | `aggregate_query.go`, `FunctionStringOutput`, `FunctionFormatOutput`, `FunctionJSONOutput`, `ApplyCoercion` | presentation (comment/deparse snapshot) |
| `scope.input.Regrole/Regcollation/Regnamespace` | `ApplyCoercion` | environment |
| `scope.op.Context()` | `setContext` | statement context |
| `scope.statementStart` | `FunctionStatementTime` | statement clock |
| `acquireQueryRow`, `latestQueryRow` | `arbitrateViewQuery` | row lock admission |
| `scope.queryLocks` | `recheckLockedQuery`, `recheckLockedSource` | lock context |
| `replacementRead` + `replacementMu.RUnlock`, `touchForeignScan`, `enlistForeign` | `openForeignStage`, `finishForeignSources` | foreign owner |
| `acquireSequence`, `authorizationLocked`, `privateCatalogLocked`, `sequenceGeneratorLocked` | `pgSequencesSource.complete` | sequence acquisition, catalog overlay |
| `preflightCoercions` | `openJoinNode` | preparation callback |
| `scope.authority` | `querySource.borrowAuthority` (capture side) | admission slot |

Everything else is in the `EvalContext` capability bodies (next list). No
runtime code reads protocol or COPY I/O, `insertConflict`, `copyIO` or the
rule hooks (`activateMutation`, `ruleScratch`, `installRuleScratch`); those
uses in the first inventory sit in DML, COPY and rule code that stays.

**Capabilities** (bodies that move to `statementHost`; `EvalContext`
keeps forwarders):

- `functions.ACLRuntime`: `ACLAuthority`, `ObjectACL` (also
  `pgcatalog.Runtime`), `RoleMembership`, `RowSecurityActive`.
- `functions.AdvisoryRuntime`: `Advisory`. `functions.BlockingRuntime`:
  `BlockingInquiry`.
- `functions.CatalogInquiryRuntime`: `CatalogUserName`, `CommentInquiry`,
  `DeparseInquiry`, `DescribeObject`, `IdentifyObject`,
  `IdentifyObjectAddress`, `IndexProperty`, `LookupObjectAddress`,
  `VisibilityInquiry`.
- `functions.SessionInquiryRuntime`: `CurrentSchemas`, `IdentityValue`,
  `MyTempNamespace`, `OtherTempNamespace`.
- `functions.TransactionInquiryRuntime`: `CommitTimestamp`,
  `CurrentTransactionID`, `LastCommittedTransaction`, `MultiTransactionAge`,
  `TransactionAge`, `TransactionMembers`, `TransactionStatus`
  (`CheckTransactionInquiryContext` and `RequireTransactionIdentityScope`
  stay in `exec`: prologue and nil check).
- `functions.SequenceRuntime`: `Sequence`, `SequenceAuthority`.
- `expr.Context`: `FunctionArrayRandomOwner`, `FunctionRandomOwner`,
  `FunctionUUIDReady`, `FunctionUUIDTime` (session generators),
  `FunctionBinaryConversion` (conversion procedure),
  `FunctionRegexExecution` (presentation cache).
- `plan.Runtime`: `EvalSQLRoutine` (`invokeSQLRoutine`),
  `OpenExtensionPaths` (extension registry).

`CheckDomain` forwards through the preparation group. The other 39
interface methods (frame and statement reads, `Eval*` bodies, clocks,
entropy, output environments) move as they are.

**`execHost` method list** (`exec.Host` after 11c). Every method is called
without `Engine.mu` held. None returns an engine struct: results are values,
lower-layer types or `exec` types.

| Group | Methods | Callers | Why not a value |
|---|---|---|---|
| Environment | `Context()`, `Environments()`, `StatementStart()` | `setContext`, the finalizers, `openForeignStage`, `ApplyCoercion`, `FunctionStatementTime` | the runtime records hold the host, which a held cursor detaches; read where they are read today |
| Presentation | the nine `sqltype` formatter interfaces (`FormatRegclass`, ... `FormatACLRole`) | output wiring (five sites) | each call reads the statement's comment/deparse snapshot; nil host → `detachedPresentation` |
| Row locks | `AcquireQueryRow(leaf, row, mark)`, `LatestQueryRow(leaf, candidate, hint, mode)`, `QueryLocks()` | `arbitrateViewQuery`, `recheckLocked*` | lock waits and current row versions at grant time. `AcquireQueryRow` may block in the lock manager, releases the replacement read lock across the wait and retakes it for implicit statements, then takes `Engine.mu`; `LatestQueryRow` takes `Engine.mu` and records the physical row event after releasing it |
| Foreign owner | `ReleaseReplacementRead()`, `TouchForeignScan(ctx, source)`, `EnlistForeign(ctx, ...)` | `openForeignStage`, `finishForeignSources` | releases the replacement read lock before external I/O; enlists the statement's foreign transaction |
| Sequences | `CompletePGSequence(ctx, ref, row)` | `pg_sequences` stage | acquires the sequence lock, then reads the generator under `Engine.mu` |
| Preparation | `PreflightCoercions(c, e)`, `CheckDomain(c, target, value)` | `openJoinNode`; `CheckDomain`, JSONB record checks | preflight and domain programs reach routine admission and the domain catalog |
| Capabilities | the 36 bodies above, each taking the context | `exec.Context` forwarders | they take `Engine.mu`, read session state or have side effects (advisory locks, XID assignment, sequence calls, notices) |

**§7 decision 8 — settled: one root host type, one interface per
consumer.** Each consumer package declares its own narrow host interface
(`exec.Host`; `analyze`'s in 12c; the feature hosts in Part B). The root
package implements all of them on `statementHost`, with one compile-time
assertion per consumer. Lock contracts and nil or replaced-scope handling
then live in one place; each consumer still sees only its own methods, so
the host is no service locator; host methods do not widen the public
`StatementScope` facade. After 13d the adapter stays in the root package
over the moved scope type.

**§7 decision 11 for `RowResult` — settled: `exec` owns `RowCursor` and
`RowResult`.** The driver and `cmd/stuntgres` import `exec` directly in 11d,
as they import `settings` since 8c; no permanent facade alias.
`engine.Result.Rows` names `exec.RowResult`.

**Risks for 11b–11d.**

- Typed nil: today `output.Regclass = c.liveScope()` stores a typed nil
  whose formatters return "missing … snapshot". `liveHost()` must return an
  untyped nil, and presentation must fall back to `detachedPresentation`.
- Forwarder order: a forwarder must keep the body's first check (prologue,
  then the nil-scope error text), so error order does not change.
- Held cursors: `queryAuthority.expression` (identity and statement start)
  moves with the slot; detaching clears only the host.
- Locks: `AcquireQueryRow`, `LatestQueryRow`, `CompletePGSequence` and
  `ReleaseReplacementRead` keep their mutex contracts; run `RACE=1` on row
  lock, cursor and foreign scan selections after each commit.
- Size: `EvalContext` grows by one interface word (host instead of a
  pointer) and by the two opaque admission fields; it is copied per frame, so
  check `make bench` and a scan-heavy selection for allocations.
- 11c is broad: 3,369 production and 10,002 test references; script it.

Notes for later steps:

- Step 12 (12c): preflight, the join legality checks and the binder methods
  in the query files stay in the engine after step 11; they take an
  `exec.Context`. Decide in 12c whether they move to `analyze` (which then
  imports `exec`) or stay. `PreflightCoercions` on the preparation value
  (11b result) is the only runtime call into them.
- `access` B5: the binding walkers (`checkBinding`, `checkQuerySources`,
  `checkViewRowMarks*`) read lock modes and query leaves; they follow step 12.
  (Done: `analyze.CheckBinding` and `CheckQuerySources`; `checkViewRowMarks*`
  stays with the view definitions.)
- Step 13: the authority slot is cleared by SQL cursors, protocol portals,
  routine scopes and transaction finish; when they move, they clear
  `exec.Authority.Host`.

### 11b result

Built at `11715935b` (master did not move during the session). The runtime
now reaches statement, session and engine state only through `execHost`
(`statement_host.go`), implemented by `statementHost`, a defined type over
`StatementScope`; or through `execPreparation` (below).

- **Host methods (57).** Environment: `Context`, `Environments`,
  `StatementStart`, plus `CTEInvocation` (not in the 11a list:
  `queryState.inheritCTEs` read `scope.cteInvocation`; the result is the
  runtime's own `cteInvocation`). Presentation: the nine `sqltype` formatter
  interfaces (12 methods, `presentationFormatters`). Row locks:
  `AcquireQueryRow`, `LatestQueryRow`, `QueryLocks`. Foreign owner:
  `ReleaseReplacementRead` (the existing `foreignUnlocked`),
  `TouchForeignScan`, `EnlistForeign` (forwards to `enlistForeign`, which
  DML keeps calling). Sequences: `CompletePGSequence`. Capabilities: 35
  bodies; `SequenceAuthority`, `CheckTransactionInquiryContext` and
  `RequireTransactionIdentityScope` are only prologue and nil check, so they
  stay forwarder-side. A capability method takes the context only when its
  body uses it (12 do). `EvalSQLRoutine` calls `InvokeSQLRoutine`, keeping
  its own precedence (statement host first, then the query source's).
- **Nil handling.** `hostOf` returns an untyped nil for a nil scope and is
  the only way a scope becomes a host; `scopeOf` is the checked conversion
  back. `presentation()` falls back to `detachedPresentation`, which owns the
  missing-snapshot messages; the `StatementScope` formatters return its
  results in their missing branches.
- **Deviation: preparation callbacks are not host methods.** Domain checks
  also run in contexts built without a scope (generated, index-expression,
  index-predicate and partition admission, temporary commit, concurrent
  refresh), so a nil-able host cannot carry them. `execPreparation`
  (`exec_preparation.go`: `PreflightCoercions`, `CheckDomain`) is one
  stateless package value, `enginePreparation{}`; `openJoinNode` and
  `EvalContext.CheckDomain` (after its `ctx.Err()` check) always call it.
  Decided with the user in the 11b planning session.
- **Back-pointers.** `querySource.host`, `queryAuthority.host`,
  `evalStatement.host`, `pgSequencesSource.host`; `liveScope()` is deleted.
  Engine code that still needs the scope behind a record uses
  `scopeOf(x.liveHost())` (15 production sites, all staying code:
  `deparseRelation`, `identifyObjectValues`, `expressionPrincipal`,
  `admitInquiryBody`, `routineAdmissionAuthority`, the domain-check helpers,
  `evalSerialSequence`, the SQL routine runner, `regtype_input.go`, ...).
- **Admission slots.** `evalAdmission.routineAuthority` and
  `routineProgram` are `admissionAuthority`/`admissionProgram` marker
  interfaces (`admission_slots.go`), read through `routineAuthorityView()`
  and `routineAdmissionProgram()`; writes store non-nil pointers or copy
  another slot.
- **Size.** `EvalContext` grows by 24 bytes (1,648 → 1,672): the host and
  the two slots are interface words.

Tests: added `statement_host_test.go` (untyped nil from `hostOf` and
`liveHost`, `scopeOf` round trip, detached-presentation messages through
every route). Adapted mechanically, no assertion changed: the engine tests
that built `statementScope`/`scope` literals or read `authority.scope`
(`sql_cursor*_test.go`, `delete/update_current_of_test.go`,
`discard_test.go`, `session_test.go`, `partition_routine_authority_test.go`,
`serial_sequence_test.go`, `query_parallel_test.go`,
`query_outer_joins_recheck_test.go`, `transaction_time_test.go`, ...), the
`*_routine_authority_test.go` files that dereference the admission slot, and
`pg_locks_test.go` (`AcquireQueryRow`). No test moved or was deleted.

Verification: `make build` and targeted `make check` selections after each
commit, with `RACE=1` on the row-lock, cursor, foreign-scan, CTE and
detach selections; `make quickcheck` passed after commit 6 and on the final
code. `REFERENCE=1` passed for `^TestReferenceQuery` (joins, subqueries,
CTEs, set operations, DISTINCT, LIMIT/FETCH, windows) and for row locks,
query locks, SQL cursors, `pg_cursors`, isolation, foreign tables,
`pg_sequences`, domains, reg* types, aggregates, grouping sets, CTE DML,
commit time, transaction identity, advisory locks and routines, except six
`/postgres` leaves of `TestReferenceQueryFromValuesPhysicalReplacement`,
which fail the same way on master (oracle drift; stuntgres leaves pass).
`make bench`: 839–877 µs/op and 5,014 allocs/op after (two noisy warm-up
runs at 0.98–1.27 ms), against 843–893 µs/op and 5,014 allocs on master;
under the 1 ms target. A throwaway scan probe (filter, join, 5,000-row
output with regclass, window, `FOR UPDATE`, `statement_timestamp()`) showed
identical allocations per query before and after. `make deps`: module
paths only. racecheck/finalcheck skipped at user request.

Notes for 11c:

- `exec` declares `Host` (with `presentationFormatters`) and `Preparation`.
  The preparation value needs one install point the engine calls from
  `init()` (for example `exec.InstallPreparation(enginePreparation{})`),
  since `exec` cannot name engine code; a forgotten install must fail loudly.
- `detachedPresentation` has no engine dependencies and moves to `exec`;
  the `StatementScope` formatters then call the exported value.
- The admission-slot marker methods must be exported once the interfaces
  live in `exec` (the engine types implement them).
- Capability host methods take `*EvalContext`; after the move they take
  `*exec.Context`, and the engine helpers they call become functions
  (`meth2fn`), as the 11c plan already says.

### 11c result

Built at `21fce56f1` (after 12c landed; the first pass at `12d7ef8b9` was
regenerated by the scripts). Tools and scripts:
`.cswd/tmp/PG18-ENGINE-SPLIT-EXEC-PACKAGE/` (`c1.sh` … `c4.sh`, the
`frontier` fixed point with a member dump, and new `expspec`, `movespec`
and `poslit` go/types tools; `meth2fn` learned pointer receivers).

- **Cuts (hand commits).** Three runtime edges 11a did not list would have
  pulled the view renderer into `exec`: `queryState.evaluation` read
  `viewOf(recipe).dependencies` (the query source now captures the manifest
  as `routineChecks`), `quoteCaptureCandidate.prune` read the view
  definition (now the engine function `pruneQuoteCaptures`), and the JSONB
  record checks read the domain catalog (now `Preparation.DomainCatalog`).
  The prepared domain programme's phases hold routine admission programmes,
  so they became an opaque slot (`programPhases`), and
  `cteInvocation.completeMutation` (DML binding, statement `Result`) became
  `completeCTEMutation`. To keep `exec` below the L10 feature packages, the
  command identity moved from `typedef` to `ids.CommandIdentity`, the
  installed text provider check goes through `Preparation.ValidInstalledText`,
  and `validateSelectedDomainRows` became an engine function. The 12 dirty
  `EvalContext` helpers, the two slot accessors and `borrowAuthority` became
  engine functions; the preparation value starts as a panicking stub that the
  engine installs from `init` (`InstallPreparation`).
- **Walkers to `plan`** (`79653b057`): `WalkExpression`,
  `ExpressionChildren`, `WalkDemandedExpression`, `LiteralSearchedCaseMatch`,
  `RewriteQueryExpressions`, `VisitExecutableQuery`, `VisitCTEQuery`,
  `QueryInputs`, `QueryExpressions`, `FunctionFromConjuncts`,
  `FunctionFromRestriction`, `PartitionFunctionConjuncts`,
  `JoinExpressionSides`. `queryTreeOccurrences` reads query sources, so it
  moved to `exec` instead.
- **Split** (`6d6267f35`): the runtime halves of the mixed files went to 17
  `execmv_*.go` topic files; 23 files were wholly runtime. Staying halves:
  binder methods in `query_{frames,limit,presence,producer,subquery}_bind.go`
  and `window_value_bind.go`, preflight in `query_preflight.go`, admission in
  `query_admission.go` and `query_lock_admission.go`, the row-lock host
  bodies in `statement_host_row_lock.go`. Twenty-one analysis-time helpers in
  the runtime files (`collectQueryFrames`, `queryRootRowMarks`,
  `queryRowMode`, `presenceQueryPlan`, `pruneProjectWindows`, ...) are used
  only by binder, legality and admission code, so they stay.
- **Export in place** (`0bfa0b5c5`): 435 members, 9,441 references,
  including the fields that positional literals assign; the per-object
  census is identical before and after, and `expspec` refused any name
  reachable through embedding (the two `schema` stage fields became
  `Columns`). `sortOrderedAggregate` stays unexported: the reference fixture
  of `TestReferenceOrderedSetAggregateActiveCancellation` waits for that
  native stack frame, so its direct unit phase moved into exec as
  `TestSortOrderedAggregateCancellation`. Likewise the recursive union stage
  keeps its unexported defined type `recursiveUnionStage` (exec adds the
  alias `RecursiveUnionStage`): `TestReferenceQueryCycleLifecycle` waits for
  a `recursiveUnion` frame before it cancels an unbounded recursive query, and
  the exported name made that query run until the host ran out of memory.
- **Move** (`2e68cbd93`): 41 files, about 9.9k lines. Only the 56 types the
  engine names are exported (the naming table, plus `EvalFrame`,
  `EvalStatement`, `EvalAdmission`: `Frame` would have shadowed the promoted
  `Frame` field); 109 functions and constants are qualified in 268 engine
  files; five positional literals were keyed (`keylit`), so `go vet` is
  unchanged. `exec` imports `plan`, `expr`, `functions`, `catalog`,
  `storage`, `visibility`, `lock`, `ids`, `proc`, `authorization`, `parser`,
  `sqltype`, `sqlstate` and the standard library (not `access`).
- **Tests.** New: `exec/preparation_test.go` (missing install panics, the
  installed value receives calls). Moved: the direct sort phase of
  `TestOrderedSetAggregateCancellationPhases`, now
  `exec/ordered_aggregate_sort_test.go`. Adapted, no assertion changed: the
  window and aggregate cancellation tests that match stack-frame names
  (`query_window_test.go`, `query_window_exclude_test.go`,
  `query_aggregate_test.go`), and mechanical renames in the engine tests.
- **Verification.** `make build`, `go vet` (unchanged against the base) and
  targeted engine selections after every commit; `make quickcheck` passed
  on the final code (inside a 48 GB memory cap; the first two runs, before
  the frame-name fix, were OOM-killed in `reference/query_core`).
  `REFERENCE=1` passed for `^TestReferenceQuery`, row and query locks, view
  locks, SQL cursors, `pg_cursors`, transaction isolation, `pg_sequences`,
  the FDW scan/rescan/cursor/recheck/transaction selections, domains, reg*
  types, grouping sets, CTE DML, commit time, transaction identity,
  advisory locks, routines and ordered-set aggregates, except the six
  `/postgres` leaves of `TestReferenceQueryFromValuesPhysicalReplacement`,
  which fail the same way on master (oracle drift). `make bench`: 820–869
  µs/op and 5,014 allocs/op (one 1.04 ms warm-up run), against 852–864
  µs/op and 5,014 allocs/op on master; under the 1 ms target. The 11b scan probe (filter, join, 15,000-row
  output, window, `FOR UPDATE`, `statement_timestamp()`) shows the same
  allocations per query before and after. `make deps`: module paths only. racecheck/finalcheck skipped at user
  request.

Notes for later steps:

- 11d: remove `exec_aliases.go` with the alias tool; three test files have
  an `exec` local or an `os/exec` import (the 11d plan names them). Comments
  in engine files still name the old executor identifiers.
- 12d/12e: the binder methods and analysis helpers that stayed (above) are
  candidates for `analyze`; preflight still reaches `exec` through
  `Preparation.PreflightCoercions`.
- Step 13: the authority slot is `exec.Authority.Host`; `statementHost`
  stays with the scope type.
- Step 15/16 Part B: `exec` must keep not importing `typedef` or
  `extension`; route new runtime needs through the host or preparation.
- Any rename in `exec`: the reference cancellation fixtures match native
  frame names by substring (`recursiveUnion`, `sortOrderedAggregate`,
  `openAggregateNode`); a missed match leaves an unbounded query running
  without a memory limit. Run such selections under a memory cap.

### 11d result

Built at `5aa8e344e`. Tools and scripts:
`.cswd/tmp/PG18-ENGINE-SPLIT-EXEC-ALIASES/` (`c1.sh` … `c4.sh`; the
`unalias` tool of 10d, with the spec derived from `exec_aliases.go`).

- **Free the name** (`4d4e0942e`): the conflict-update benchmark's `exec`
  local is `execute`; the two typed-publication tests import `os/exec` as
  `osexec`. The dry run showed that no rewrite site needed this (none of the
  three files names an alias), so it is a guard: inside the engine, `exec`
  always means the executor package.
- **Driver and CLI** (`4acc2dac9`): `rows.go`, `conn.go`,
  `driver_batch_rows_test.go` and `cmd/stuntgres/query_output.go` name
  `exec.RowResult` (7 references); §7 decision 11 is now in effect.
- **Engine rewrite** (`17fc012f5`): 3,215 references in 578 engine files
  (production and tests; `EvalContext` 1,233, `evalStatement` 738,
  `querySource` 341, `evalFrame` 221, `queryLeaf` 129, `queryState` 125),
  0 shadowed; `exec_aliases.go` deleted. Besides the renames the diff holds
  only added imports and gofmt realignment; no file that was not
  gofmt-clean on master was touched. `go vet` output is identical to the
  base.
- **Comments** (`7d6ffd65a`): nine comments that named alias spellings
  (eight in the engine, one in `expr/context.go`) name the `exec` types.
- **Tests.** None moved, deleted or rewritten; the only test edits are the
  mechanical renames above.
- **Verification.** `make build`, `go vet` (unchanged against the base) and
  the targeted selection (statement host, preparation install, ordered
  aggregate sort, driver batch rows, node prologue, query joins/CTE/window/
  limit/parallel/lock-arbiter units, SQL cursors, typed publication) after
  every commit; `make quickcheck` passed on the final code (inside a 48 GB
  memory cap). `REFERENCE=1` passed for `^TestReferenceQuery` and for row
  and query locks, view locks, SQL cursors, `pg_cursors`, transaction
  isolation and `pg_sequences`, except the six `/postgres` leaves of
  `TestReferenceQueryFromValuesPhysicalReplacement`, which fail the same
  way on master (oracle drift). `make bench`: 841–871 µs/op and 5,014
  allocs/op (one 1.09 ms warm-up run), against 838–889 µs/op and 5,014
  allocs/op on master; under the 1 ms target. `exec` imports are
  unchanged. `make deps`: module paths only. `AGENTS.md`: 100 lines before
  and after. racecheck/finalcheck skipped at user request.

Notes for later steps:

- Aliases do not change runtime type or frame names, so the reference
  cancellation fixtures (`recursiveUnion`, `sortOrderedAggregate`,
  `openAggregateNode`) were unaffected; a real rename in `exec` still needs
  the memory-capped check described in the 11c notes.
- Step 12d/12e and step 13 inherit engine code that names `exec.Context`,
  `exec.Source` and `exec.Authority` directly; moving that code only needs
  the `exec` import, not an alias step.
