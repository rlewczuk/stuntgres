# internal/engine/exec

The query executor runtime and the concrete evaluation context. It opens bound
`plan` trees over captured sources, produces rows, arbitrates row locks and
evaluates expressions. It reaches statement, session and engine state only
through one host interface and one installed preparation value, both
implemented by the root engine.

## Imports

Layer L7 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture)
(see also [ENGINE-SPLIT-exec.md](../../../docs/ENGINE-SPLIT-exec.md)). It imports
`plan`, `expr`, `functions`, `catalog`, `storage`, `visibility`, `lock`,
`access`, `ids`, `proc`, `authorization`, `parser`, `sqltype`, `sqlstate` and the
standard library, never the engine, `analyze`, `session` or a feature package
(`routine`, `extension`, ...).

## What it owns

- **Runtime records** (`query.go`, `query_sources.go`, `query_locks.go`):
  `Source` (a captured relation or composite source with its versions,
  visibility and back-pointers), `State` (one query invocation), `Stage` (an
  indexed producer stage), `FinalRows`, `Authority` (the borrowed statement
  authority a held cursor keeps), `Leaf`, `LockContext`, inheritance segments
  and physical rows. `SelectQuerySource` takes concrete
  `visibility.HeapOperations` explicitly: captured-source, inheritance and
  reselection owners resolve them once from the captured heap descriptor and
  handler; catalog replacement passes the owner engine's resolved authority;
  direct-vector callers pass real resolved operations (no nil-View fallback).
  The row loop calls the concrete liveness implementation, never a catalog
  lookup, callback or alternate visibility algorithm.
- **Openers and producers** (`query*.go`, `window*_query.go`,
  `aggregate_query.go`, `view_producer.go`): `OpenNode` dispatches every plan
  node; `Finalize` and `FinalizeSelect` (with the parallel finalizer) turn a root
  into a `RowResult`. CTE scans and invocations (`CTEInvocation`,
  `CTEProducer`), set operations, recursive unions, set lowering validators,
  windows, aggregates, DISTINCT, LIMIT/FETCH WITH TIES, subqueries and their
  presence rows.
- **Stored-view producers.** A stored-view `Source` owns its retained
  NestedStage and lazy flat prefix; short-circuiting subquery readers close only
  their borrowed invocation stage. Later readers replay an identity range over
  the cached prefix, then resume the one uncaptured producer frontier.
  ensure/Drain use `Stage.Count`, so range producers neither allocate
  prefix-index copies nor stall while draining. Child Reset invalidation and
  demanded volatile projection resets clear flat rows/lineage; stable replay
  does not reevaluate outputs. Producer failures/cancellation stay terminal
  before any later prefix replay. `Source.Close` (including transferred cursor
  teardown) disposes the producer once; reopening a reader does not rebind,
  recapture or eagerly drain it.
- **Row locks** (`query_locks.go`, `query_pending_locks.go`,
  `query_lock_order.go`, `query_lock_versions.go`): view-row arbitration,
  recheck of locked rows against the join and filter predicates, pending marks
  and locked-order refresh.
- **Sources outside the heap**: the foreign scan stage and its owner
  (`foreign_scan.go`, `foreign_values.go`, `fdw_error.go`) and the
  `pg_sequences` stage with its predicate admission
  (`query_pg_sequences_admission.go`).
- **Cursors** (`query.go`): `RowCursor` and `RowResult`; the driver reads
  results through them.
- **Evaluation context** (`context*.go`, `node_runtime.go`, `aggregate.go`):
  `Context` with its `EvalFrame`, `EvalStatement` and `EvalAdmission` groups. It
  implements `expr.Context`, `plan.Runtime`, the seven `functions` runtimes,
  `sqltype.DomainCheckEvaluator` and the `pgcatalog` ACL inquiry. The `Eval*`
  bodies and frame reads live here; capability methods forward to the host.
  `InputDomainChecks(evaluator, capturedCatalog)` scopes fresh text input:
  descriptor-owned domain violations stay soft, while errors executing CHECK
  expressions carry the shared `sqlstate.InputError` hard marker outside their
  unwrap-preserved cause, even a directly soft-marked alias cause. It captures no
  live catalog and changes no ordinary/default/predicate environment.
  `FunctionJSONBRecordInput` (the JSONB expression-context seam) delegates to
  this adapter; COPY wraps its captured scope evaluator.
- **Routine call admission** (`routine_admission.go`,
  `routine_body_admission.go`, `routine_program.go`): EXECUTE admission of the
  routines an expression invokes at planning time (`AdmitRoutineCall`,
  `AdmitRoutineReference`, `EvaluatePlanningExpression`), SQL-body edges
  (`AdmitOverlapsPair`, `BodyRoutineDescriptor`), grafted recipe selection
  (`SelectExpressionRecipe`) and the consumed-call footprint `RoutineProgram`
  (`RoutineCall` records; `Record`, `Readmit`, `InitializePhase`, `Include`).
  Admission reads `RoutineAccess` (`routine_access.go`): the captured
  `access.View` and its `access.Environment`, from the borrowed
  `AdmissionAuthority` slot or the host's `RoutineAdmission`.
- **Routine descriptors** (`routine_descriptor.go`): carrier extraction
  (`ExpressionRoutineDescriptor`, `ExpressionRecipeDescriptor`), revalidation
  (`ValidateRoutineDescriptor`, `ValidateStoredRoutineDescriptor`,
  `SameRoutineDeclaration`) and the instance-selected SQL-body walk
  (`WalkRoutineBody`, `PolymorphicBodyArgument`, `BodyCastMatches`), also used by
  `analyze` and the engine.
- **Statement helpers the DML executors share**: constraint-violation DETAIL
  rows (`row_detail.go`: `FailingRowDetail`, `WriteDiagnosticValue`),
  single-relation source capture (`source_capture.go`: `CaptureSource`,
  `CapturePrefix`) and the CURRENT OF scan helpers (`cursor_scan.go`).
- **Runtime source values**: the `pgrowlocks` values (`pgrowlocks.go`:
  `PgrowlocksLogicalTID`, `PgrowlocksPrefix`, `PgrowlocksValues`) and the table
  check `CheckPgrowlocksTable` over an `access.View` and its environment; the
  session identity snapshot (`identity.go`: `CaptureIdentity` and the kind
  switch `IdentitySnapshot.Value`).
- **Seams**: `Host` and `PresentationFormatters` (`host.go`),
  `DetachedPresentation` (`detached_presentation.go`), `Preparation` and
  `InstallPreparation` (`preparation.go`), and the admission slots
  `AdmissionAuthority` (`context_admission.go`) and `RoutineProgram`.

## The host

`Host` (`host.go`) lists every method with its group. The engine implements it
on `statementHost`, a defined type over `StatementScope`.

| Group | Methods | Lock and lifetime contract |
|---|---|---|
| Environment | `Context`, `Environments`, `StatementStart`, `CTEInvocation` | Read where the runtime reads them; a held cursor detaches the host between fetches. |
| Presentation | the nine `sqltype` formatters | Read the statement's comment/deparse snapshot. With no host, output uses `DetachedPresentation`, which owns the missing-snapshot messages. |
| Row locks | `AcquireQueryRow`, `LatestQueryRow`, `QueryLocks` | `AcquireQueryRow` may block in the lock manager; it releases the replacement read lock across the wait (retaking it for implicit statements), then takes `Engine.mu`. `LatestQueryRow` takes `Engine.mu` and records the physical row event after releasing it. |
| Foreign owner | `ReleaseReplacementRead`, `TouchForeignScan`, `EnlistForeign` | Release the replacement read lock before external I/O; enlist the statement's foreign transaction participant. |
| Admission | `RoutineAdmission` | Reads the statement's routine authority and dependency manifest (statement-owned fields, no lock); fails once the statement has finished. |
| Sequences | `CompletePGSequence` | Acquires the sequence lock, then reads the generator under `Engine.mu`. |
| Capabilities | the function, expression and plan runtime bodies | Take `Engine.mu`, read session state or have side effects (advisory locks, XID assignment, sequence calls, notices). |

Every method is called without `Engine.mu` held, and none returns an engine
struct. Records store the host once, never per row. A record with no statement
scope holds an untyped nil host (`LiveHost` never returns a typed nil); the
`Context` forwarders run their prologue first and then the nil-host diagnostic.

`FunctionPgcryptoNotice(sqlstate.Error)` is a narrow optional capability. It
resolves `LiveHost` for each delivery and invokes optional
`PgcryptoNotice(sqlstate.Error)` on that host; no host means no observer. The
engine host appends owned records through the current operation's notice filter
(`client_min_messages`), never calling user handlers under engine locks.
Nested/prepared/stored executions retain no bind-time operation or notice sink.

## Expression preflight

`exec` owns the expression preflight that runs before execution:
`PreflightCoercions` and `PreflightCoercionsWithPolicy` (the policy form plans
decisive constants for consumed stored defaults, CHECK rows and standalone
recipes), conditional planning and folding (`SimplifyConditional`,
`conditional_plan.go`, `conditional_fold.go`, `preflight_composite.go`), strict
parameter NULL folding (`FoldStrictParameterNull`, `ExecutionKnownNullArgument`),
the array assignment and UPDATE value preflight (`PreflightArrayTargets*`,
`PreflightUpdateExpression`), string variadic folding (`FoldStringVariadic`,
`QuoteOriginOf`), view qualification mapping (`MapViewQualification`) and the
OVERLAPS route with its inline cost model (`SimplifyOverlapsRoute`,
`OverlapsInlineSafe`, `overlaps_cost.go`). The join opener re-runs
`PreflightCoercions` on its predicate.

Preflight never reads a statement scope directly: it reaches the engine only
through `Preparation`. It runs outside engine and publication locks. Conditional
planning keeps every analyzed dependency and authorization even when it removes
a branch, and held parameters are reconsidered on every execution, never stored
as planning facts.

## Preparation

Domain checks and preflight also run in contexts with no statement scope
(generated columns, index expressions and predicates, partition admission,
temporary commit, concurrent refresh), so they cannot hang off a nil-able host.
`Preparation` is one stateless package value: `AdmitExpression` (planning-time
execution admission; index, table and partition evaluation code calls it
through the package forwarder of the same name), `PreflightQuery` (a subquery's
plan-tree preflight), `PrepareDomainConversion` (the domain CHECK programmes of
a conversion, kept in the statement's domain state), `SpecializeRuleParameters`
(a rule action's parameters replaced through the view rewriter), `CheckDomain`,
`DomainCatalog` (the catalog a domain check reads), `ReturnsSet` (set-returning,
including the installed intagg enumerator and pgrowlocks record source),
`StoredReferenceValid` (the stored-reference epoch bridge of a TEMP owner) and
`ValidInstalledText` (the installed text call's provider check). The engine
installs its implementation from `init` with `InstallPreparation`; until then
every method panics with `exec: preparation not installed`.

## Cooperative foreign execution

Only `FinalizeSelect` admits optional foreign partitioning, using the same
graph/expression classifier as physical scans but not their row-count worker
decision. Captured foreign sources start empty and have no physical View;
generic finalization stays serial and `State` carries only execution-local
admission. `foreign_parallel.go` is a scan adapter owned by `ForeignExecution`,
not another lifetime owner: leader-only enrollment and startup, bounded transfer
of owned cells, cancellation on the first error, then joined scans before shared
cleanup. Local import, filter, sort and projection stay serial, and every failed
finalization keeps nil rows, count 0 and the last publication fence.

## Invariants

- **No mutexes.** The package adds no lock. Lock order, modes and wait behavior
  belong to the engine host bodies and the `lock` manager; the lock manager's
  mutex is never taken under `Engine.mu`.
- **Snapshots.** Records borrow immutable published snapshots and catalog reads;
  no snapshot copies row values. Repeatable read retains every immutable root
  before analysis waits; read committed views stay fresh at grant time (see the
  [`visibility` README](../visibility/README.md)).
- **Eager results.** Finite results are finalized before statement completion:
  a late projection, sort, source-close or cancellation failure exposes no
  successful prefix or cursor. `RowCursor` reads never resume SQL evaluation,
  acquire grants or publish writes, and results expose copied values or
  immutable owned backing, never borrowed execution scratch. Sort, grouping,
  distinct and window state is execution-local and finishes before cursor
  publication; grouping, distinct and windows are serial (the bounded SELECT
  scan is the parallel exception).
- **Row locks.** Rowmarks lock actual physical identities. After a wait only
  frozen contributors are followed to current heads; rechecks rebuild their
  bound frames without finding new matches or resorting. Immediate wait
  policies use the lock manager's atomic grant path, and skipping an
  unavailable row releases no earlier grant.
- **Cancellation** is checked at fixed points; cursors release pins and sources
  on every exit, and `Close` is idempotent.
- **Admission slots are borrowed.** `EvalAdmission` copies the borrowed
  authority and routine program pointers with the context, never the pointees.
  The runtime reads the authority only through `AdmissionAccess`; only engine
  helpers type-assert it. A prepared domain programme's phases are an opaque
  slot; the runtime reads only its expressions.
- **The admission environment is never read for procedures.**
  `RoutineAccess.Env` supplies the TEMP namespace and type paths, which
  procedure EXECUTE checks do not consult, so `Readmit` switches only
  `View.State.CurrentUser`. `Env` borrows the engine's view and is never
  retained past admission; it is nil for an authority without a live engine
  (`Live`).
- **No per-row allocations** from the seams: the host and preparation are
  interface values stored once.

## Row-policy runtime

`RLSProgram` is an execution-owned set of separately conjoined command groups.
`EvaluateRowSecurity` is the sole true-only USING/WITH CHECK evaluator;
`CheckRowSecurity` adds named/unnamed 42501 diagnostics without masking
expression errors. Each imported occurrence owns serial clause context/query
scratch, separate from OLD/NEW and the caller frame. Scratch releases borrowed
tuple/frame references on every return. No catalog payload or prepared identity
is modified.

`Source.RLS` borrows its statement-owned guard. `openRowSecurityStage` is a lazy
source-local producer before joins/aggregates/user work, retaining physical
occurrence/version identity. Frozen-source bridges guard the live input before
projection, and row-mark refreshed-source checks invoke the same evaluator.
Guarded sources are excluded from raw hash/pruning/parallel bypasses. Ordinary
statement Finish releases imported query states and nested pins outside
Engine.mu. Successful SQL cursor/protocol publication instead transfers the
owning programs to the producer; borrowed sources never close them. Clause
adapters detach from the opening scope and use shared authority/current FETCH
callbacks. EOF/CLOSE/error/cancel/hold materialization release stages, programs
and nested pins before clearing authority. Eager immutable data clears borrowed
RLS pointers.

Rowmarks inside policy subqueries share the statement's one additive
`LockContext` manifest and the ordinary legality/ACL/wait/cancel/recheck paths.
Private query-source slots are not global physical occurrence IDs. The engine
prepares the complete selected policy/view occurrence tree before its first data
capture, then captures only the union of actual command, target and imported
source roots. Physical capture consumes those prepared occurrences without
another policy selection or view import, so a later policy's first mark cannot
leave an earlier relevant input without its identity.

The root owns admission records around the borrowed programs, including bypass
metadata, and fences the complete graph only at completed outer target/source
admission boundaries. An unchanged immutable role snapshot reuses applicability
proof, never row truth or privilege; changed snapshots compare actual selection.
Additional imported-source waits dirty only the current original logical command
for its ACL walker, before provider preflight. The original logical execution
record survives physical lowering and lexical rule/routine activation; no
historical producer declaration is reauthorized during FETCH.

## Transition aggregates

`TransitionAggregate` keeps an immutable selected plan and execution-local typed
state/frame. Every ordinary provider consumes one ordered argument vector.
Strictness distinguishes never-seeded state from a transition-returned NULL: the
former can copy the first valid input, the latter must never reseed. Non-strict
transitions receive real NULL state and inputs. Support EXECUTE uses the current
aggregate owner across SQL-program phases without impersonating that owner
inside the SQL body.

Declared implicit-binary SFUNC and first-seed conversions capture the physical
datum operation, so TEXT-to-BYTEA keeps literal bytes (including a backslash-x
prefix), not BYTEA input decoding, after the original cast disappears or becomes
INOUT. Provider remapping preserves that operation with destination descriptors.
At aggregate executor startup, strict/no-init providers check the live direct
implicit-binary compatibility once, before reading any input; an absent or
nonbinary replacement raises 42P13 with the current aggregate identity, even for
empty input, and never selects a replacement seed conversion.

The provider retains effective ordinary-input collation independently of its
state/result usage. Remapping derives it from an actual mapped typed input,
keeping full private formal checks. Only quoted aggregate SQL support forwards
it into closed-body analysis, implicitly and without caller COLLATE origin. The
single routine-analysis cache invalidates descriptions when effective collation
changes; raw syntax may still be reused. Frozen SQL-standard support stays
creation-analyzed and body invoker authority does not change.

## What stays in the engine

Statement dispatch (`executeStatement`, `executeBoundLocked`, ...); query
admission and capture (`query_admission.go`, `query_lock_admission.go`,
`captureQuerySources*`; the view row-lock walks and the admitted-source identity
check are `view.QueryLocks`, `view.FirstQueryRowMode` and
`view.CurrentQuerySource`); the `PreflightQuery` and `AdmitExpression`
forwarders of the installed preparation (query preflight and join legality
import `stored` and `table`, which sit above `exec` and `analyze`, so they live
in `view`); rule parameter specialization
(`view.SpecializeRuleParameterExpression`); the domain CHECK programme
preparation (`prepareDomainConversion`, which keeps its phases in the statement
scope) and the call of `stored.PruneQuoteCaptures`; the binder methods in the
`query_*_bind.go` files; the join legality checks; the INSERT, COPY FROM,
UPDATE, DELETE, MERGE and foreign DML prologues (the records and executors are
in [`dml`](../dml/README.md)); data-modifying CTE execution (completion is
`dml.CompleteCTEMutation`); CREATE TABLE AS; SQL cursors and protocol portals;
rule execution; `statementHost` with the capability bodies, `enginePreparation`,
query grouping admission (`admitQueryGroupings`, which needs `analyze`), and the
engine-side helpers that take a `*Context` (`identifyObjectValues`,
`expressionPrincipal`, ...). The `pgrowlocks` host method keeps name
resolution, the relation lock wait and the readmit check
(`pgrowlocks_capture.go`); `IdentityValue` keeps the authority-expression and
foreign-action overrides and the current-schema branch (`session_identity.go`).
The deparse inquiry bodies and the ALTER DOMAIN row scan are `table.Deparse` and
`table.ValidateSelectedDomainRows`. `ValidInstalledText` is
`analyze.ValidInstalledText`. Callers name the types here directly
(`exec.Context`, `exec.Source`, `exec.RowResult`, ...), without aliases.

## Testing

Package units cover the preparation install seam, the stored-view producer
(lazy first pull, cached-range replay, stable versus volatile evaluation counts,
exact-once disposal, failure retention), source ownership, the transition
aggregate (strict reseeding, NULL state, cancellation, call identity), the
ordered-aggregate sort cancellation, the pgcrypto notice host and the
`pgrowlocks` TID mapping. Executor, row-lock, cursor, foreign-scan, preflight and
routine-admission units build statements through an `Engine` and stay in
`internal/engine` (grep its tests for `exec.OpenNode`, `exec.PreflightCoercions`
or `exec.AdmitRoutineCall`); run row-lock, cursor, foreign-scan, CTE and detach
selections with `RACE=1`. Changes to `InputDomainChecks` must keep both the COPY
filter and JSONB record reference contracts passing.
Foreign parallel dispatch is unit-tested in `internal/engine` with
`testing/synctest` (`RUN='^TestFDWParallel'`): settled
callback counts, never elapsed time, establish backpressure.

**Frame names matched by tests.** `sortOrderedAggregate`
(`TestReferenceOrderedSetAggregateActiveCancellation`) stays unexported, and the
recursive union stage keeps the unexported defined type `recursiveUnionStage`
(aliased as `RecursiveUnionStage`; `TestReferenceQueryCycleLifecycle`). Both
fixtures wait for these stack frames by name; renaming either makes the test's
unbounded query run until it exhausts memory.

```sh
make check RUN='^Test(MissingPreparation|InstallPreparation|ViewProducer|SetArmSource|CorrelatedJoinSource|QueryBranchPruning|SortOrderedAggregate|PgcryptoNotice|PgrowlocksLogicalTID)'
make check RUN='^TestCreateAggregate'
make check RUN='^TestReferenceQuery'   # public query execution; add REFERENCE=1 for the oracle
```
