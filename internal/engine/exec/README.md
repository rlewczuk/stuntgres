# internal/engine/exec

The query executor runtime and the concrete evaluation context. It opens
bound `plan` trees over captured sources, produces rows, arbitrates row
locks and evaluates expressions. It reaches statement, session and engine
state only through one host interface and one installed preparation value,
both implemented by the root engine. The package sits at layer L7 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports `plan`, `expr`, `functions`, `catalog`, `storage`, `visibility`,
`lock`, `access`, `ids`, `proc`, `authorization`, `parser`, `sqltype`,
`sqlstate` and the standard library, and never the engine, `analyze`,
`session` or a feature package (`routine`, `extension`, ...). See
[ENGINE-SPLIT-exec.md](../../../docs/ENGINE-SPLIT-exec.md) (step 11).

## What it owns

- **Runtime records** (`query.go`, `query_sources.go`, `query_locks.go`):
  `Source` (a captured relation or composite source with its versions,
  visibility and back-pointers), `State` (one query invocation), `Stage`
  (an indexed producer stage), `FinalRows`, `Authority` (the borrowed
  statement authority a held cursor keeps), `Leaf`, `LockContext`,
  inheritance segments and physical rows.
  `SelectQuerySource` takes concrete `visibility.HeapOperations` explicitly.
  Captured-source/inheritance/reselection owners resolve it once from their
  actual captured builtin heap2 descriptor and genuine handler3; catalog
  replacement passes the owner engine's resolved authority. Direct-vector
  callers also pass real resolved operations, with no nil-View fallback.
  The row loop calls the concrete liveness implementation, not a catalog
  lookup, callback or alternate visibility algorithm.
- **Openers and producers** (`query*.go`, `window*_query.go`,
  `aggregate_query.go`, `view_producer.go`): `OpenNode` dispatches every
  plan node; `Finalize` and `FinalizeSelect` (with the parallel finalizer)
  turn a root into a `RowResult`. CTE scans and invocations
  (`CTEInvocation`, `CTEProducer`), set operations, recursive unions, set
  lowering validators, windows, aggregates, DISTINCT, LIMIT/FETCH WITH TIES,
  subqueries and their presence rows.
  A stored-view Source owns its retained NestedStage and lazy flat prefix;
  short-circuiting subquery readers close only their borrowed invocation stage.
  Later readers replay an identity range over the cached prefix, then resume
  the one uncaptured producer frontier. ensure/Drain use Stage.Count so range
  producers neither allocate prefix-index copies nor stall while draining.
  Existing child Reset invalidation and demanded volatile projection resets
  clear flat rows/lineage; stable replay does not reevaluate outputs. Producer
  failures/cancellation remain terminal before any later prefix replay.
  Source.Close (including transferred cursor teardown) disposes the producer
  once; reopening a reader does not rebind, recapture or eagerly drain it.
- **Row locks** (`query_locks.go`, `query_pending_locks.go`,
  `query_lock_order.go`, `query_lock_versions.go`): view-row arbitration,
  recheck of locked rows against the join and filter predicates, pending
  marks and locked-order refresh.
- **Sources outside the heap**: the foreign scan stage and its owner
  (`foreign_scan.go`, `foreign_values.go`, `fdw_error.go`) and the
  `pg_sequences` stage with its predicate admission
  (`query_pg_sequences_admission.go`).
- **Cursors** (`query.go`): `RowCursor` and `RowResult`; the driver reads
  results through them.
- **Evaluation context** (`context*.go`, `node_runtime.go`, `aggregate.go`):
  `Context` with its `EvalFrame`, `EvalStatement` and `EvalAdmission`
  groups. It implements `expr.Context`, `plan.Runtime`, the seven
  `functions` runtimes, `sqltype.DomainCheckEvaluator` and the
  `pgcatalog` ACL inquiry. The `Eval*` bodies and frame reads live here;
  capability methods forward to the host.
  `InputDomainChecks(evaluator, capturedCatalog)` scopes fresh text input:
  descriptor-owned domain violations remain soft, while errors executing CHECK
  expressions retain the shared `sqlstate.InputError` hard marker outside their
  original, unwrap-preserved cause, including a directly soft-marked alias cause.
  It captures no live catalog and changes no ordinary/default/predicate environment.
  `FunctionJSONBRecordInput` remains the JSONB expression-context seam and
  delegates to this adapter; COPY wraps its existing captured scope evaluator.
- **Routine call admission** (`routine_admission.go`,
  `routine_body_admission.go`, `routine_program.go`): EXECUTE admission of
  the routines an expression invokes at planning time (`AdmitRoutineCall`,
  `AdmitRoutineReference`, `EvaluatePlanningExpression`), SQL-body edges
  (`AdmitOverlapsPair`, `BodyRoutineDescriptor`), grafted recipe selection
  (`SelectExpressionRecipe`) and the consumed-call footprint
  `RoutineProgram` (`RoutineCall` records; `Record`, `Readmit`,
  `InitializePhase`, `Include`). Admission reads `RoutineAccess`
  (`routine_access.go`): the captured `access.View` and its
  `access.Environment`, from the borrowed `AdmissionAuthority` slot or the
  host's `RoutineAdmission`.
- **Routine descriptors** (`routine_descriptor.go`): carrier extraction
  (`ExpressionRoutineDescriptor`, `ExpressionRecipeDescriptor`),
  revalidation (`ValidateRoutineDescriptor`,
  `ValidateStoredRoutineDescriptor`, `SameRoutineDeclaration`) and the
  instance-selected SQL-body walk (`WalkRoutineBody`,
  `PolymorphicBodyArgument`, `BodyCastMatches`). `analyze` and the engine
  use them as well.
- **Statement helpers the DML executors share**: constraint-violation
  DETAIL rows (`row_detail.go`: `FailingRowDetail`,
  `WriteDiagnosticValue`), single-relation source capture
  (`source_capture.go`: `CaptureSource`, `CapturePrefix`) and the CURRENT OF
  scan helpers (`cursor_scan.go`).
- **Seams**: `Host` and `PresentationFormatters` (`host.go`),
  `DetachedPresentation` (`detached_presentation.go`), `Preparation` and
  `InstallPreparation` (`preparation.go`), and the admission slots
  `AdmissionAuthority` (`context_admission.go`) and `RoutineProgram`.

## The host

`Host` (`host.go`) lists every method with its group. The engine
implements it on `statementHost`, a defined type over `StatementScope`.

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
struct. Records store the host once, never per row. A record with no
statement scope holds an untyped nil host (`LiveHost` never returns a typed
nil); the `Context` forwarders keep their prologue first and then the
nil-host diagnostic, so error order is unchanged.

`FunctionPgcryptoNotice(sqlstate.Error)` is a narrow optional function capability.
It resolves `LiveHost` for each delivery and invokes optional
`PgcryptoNotice(sqlstate.Error)` on that host; no host means no observer.
The engine host appends owned records through the current operation's notice
filter (`client_min_messages`), never calling user handlers under engine locks.
Nested/prepared/stored executions retain no bind-time operation or notice sink.
`TestPgcryptoNoticeCurrentHost` checks host replacement and absent-host behavior;
public SHA notice scenarios own actual session filtering and handler delivery.

## Expression preflight

`exec` owns the expression preflight that runs before execution:
`PreflightCoercions` and `PreflightCoercionsWithPolicy` (the coercion
preflight; the policy form plans decisive constants for consumed stored
defaults, CHECK rows and standalone recipes), conditional planning and
folding (`SimplifyConditional`, `conditional_plan.go`,
`conditional_fold.go`, `preflight_composite.go`), strict parameter NULL
folding (`FoldStrictParameterNull`, `ExecutionKnownNullArgument`), the
array assignment and UPDATE value preflight (`PreflightArrayTargets*`,
`PreflightUpdateExpression`), string variadic folding (`FoldStringVariadic`,
`QuoteOriginOf`), view qualification mapping (`MapViewQualification`) and the
OVERLAPS route with its inline cost model (`SimplifyOverlapsRoute`,
`OverlapsInlineSafe`, `overlaps_cost.go`). The join opener re-runs
`PreflightCoercions` on its predicate. Preflight never reads a statement
scope directly: it reaches the engine only through `Preparation`.

## Preparation

Domain checks and preflight also run in contexts with no statement scope
(generated columns, index expressions and predicates, partition admission,
temporary commit, concurrent refresh), so they cannot hang off a nil-able
host. `Preparation` is one stateless package value: `AdmitExpression`
(planning-time execution admission; index, table and partition evaluation
code calls it through the package forwarder of the same name),
`PreflightQuery` (a subquery's plan-tree preflight), `PrepareDomainConversion`
(the domain CHECK programmes of a conversion, kept in the statement's
domain state), `SpecializeRuleParameters` (a rule action's parameters
replaced through the view rewriter), `CheckDomain`, `DomainCatalog` (the
catalog a domain check reads), `ReturnsSet` (set-returning, including the
installed intagg enumerator and pgrowlocks record source), `StoredReferenceValid` (the stored-reference
epoch bridge of a TEMP owner) and `ValidInstalledText` (the installed text
call's provider check). The engine installs its implementation from `init`
with `InstallPreparation`; until then every method panics with
`exec: preparation not installed`.

## Invariants

- **No mutexes.** The package adds no lock. Lock order, modes and wait
  behavior belong to the engine host bodies and the `lock` manager; the lock
  manager's mutex is never taken under `Engine.mu`.
- **Snapshots.** Records borrow immutable published snapshots and catalog
  reads; no snapshot copies row values. Repeatable read retains every
  immutable root before analysis waits; read committed views stay fresh at
  grant time (see the `visibility` README).
- **Cancellation** is checked at the same points as before the move;
  cursors release pins and sources on every exit, and `Close` is
  idempotent.
- **Admission slots are borrowed.** `EvalAdmission` copies the borrowed
  authority and routine program pointers with the context, never the
  pointees. The runtime reads the authority only through `AdmissionAccess`;
  only engine helpers type-assert it. A prepared domain programme's phases
  are an opaque slot; the runtime reads only its expressions.
- **The admission environment is never read for procedures.**
  `RoutineAccess.Env` supplies the TEMP namespace and type paths, which
  procedure EXECUTE checks do not consult, so `Readmit` switches only
  `View.State.CurrentUser`. `Env` borrows the engine's view and is never
  retained past admission; it is nil for an authority without a live
  engine (`Live`).
- **No per-row allocations** from the seams: the host and preparation are
  interface values stored once.

## What stays in the engine

Statement dispatch (`executeStatement`, `executeBoundLocked`, ...); query
admission and capture (`query_admission.go`, `query_lock_admission.go`,
`captureQuerySources*`; the view row-lock walks and the admitted-source
identity check are `view.QueryLocks`, `view.FirstQueryRowMode` and
`view.CurrentQuerySource` since view 27i); the
`PreflightQuery` and `AdmitExpression` forwarders of the installed
preparation (query preflight and join legality import `stored` and `table`,
which sit above `exec` and `analyze`, so view 27e moved them to `view`, not
here), rule parameter
specialization (`view.SpecializeRuleParameterExpression` behind the
`SpecializeRuleParameters` forwarder), the domain CHECK programme preparation (`prepareDomainConversion`, which
keeps its phases in the statement scope) and the call of
`stored.PruneQuoteCaptures` (stored 22e moved the pruning); the binder
methods in the `query_*_bind.go` files; the join legality checks; the
INSERT, COPY FROM, UPDATE, DELETE, MERGE and foreign DML prologues (the
records and the executors are in [`dml`](../dml/README.md));
data-modifying CTE
execution (completion is `dml.CompleteCTEMutation`); CREATE TABLE AS; SQL cursors and protocol
portals; rule execution; `statementHost` with the capability bodies,
`enginePreparation`, query grouping admission (`admitQueryGroupings`, which
needs `analyze`), and the engine-side helpers that take a `*Context` (the
domain-check preparation, `identifyObjectValues`,
`expressionPrincipal`, ...). The deparse inquiry bodies and the ALTER
DOMAIN row scan are `table.Deparse` and `table.ValidateSelectedDomainRows`
(table 24j). `ValidInstalledText` is `analyze.ValidInstalledText`.

The engine, the root driver and `cmd/stuntgres` name these types directly
(`exec.Context`, `exec.Source`, `exec.RowResult`, ...); no aliases remain.

## Testing

`TransitionAggregate` keeps an immutable selected plan and execution-local typed
state/frame. Every ordinary provider consumes one ordered argument vector.
Strictness distinguishes never-seeded state from a transition-returned NULL:
the former can copy the first valid input, the latter must never reseed.
Non-strict transitions receive real NULL state and inputs. Support EXECUTE uses
the current aggregate owner across SQL-program phases without impersonating
that owner inside the SQL body. Cancellation and existing effect ownership
remain on the ordinary execution path.

The provider also retains effective ordinary-input collation independently of
its state/result usage. Remapping derives that reference from an actual mapped
typed input, preserving full private formal checks. Only quoted aggregate SQL
support forwards it into existing closed-body analysis, implicitly and without
caller COLLATE origin. The existing single routine-analysis cache invalidates
descriptions when effective collation changes; raw syntax may still be reused.
Frozen SQL-standard support remains creation-analyzed and body invoker authority
does not change. Public SQLCollation checks both cold orders and alternating
same-held calls; `TestCreateAggregateCollationBinding` covers private metadata.

Private unreachable/structural transition proof:

```sh
make check RUN='^TestCreateAggregate(StrictReturnedNullDoesNotReseed|NonstrictNullArgumentsAndState|TransitionCancellation|TransitionCallIdentity)$'
```

The call-identity unit rejects substituted support address/declaration/result
authority independently of typed remap validation. Public dual-mode strictness,
authorization, cancellation, atomicity and replay selectors are in
[reference/aggregates](../../../reference/aggregates/README.md#create-aggregate-contract).

Package unit tests (the preparation install seam, including the preflight
hooks, and the ordered-aggregate sort cancellation):

```sh
make check RUN='^(TestMissingPreparationFailsLoudly|TestInstallPreparation|TestSortOrderedAggregateCancellation)$'
```

Heap source selection and migrated direct-vector/denied/prepared captures retain
their existing independent assertions and bind actual bootstrap operations:

```sh
make check RUN='^TestAccessMethodHeapOperations$'
make check RUN='^TestMergeUpdateCapture(BaseOverlay|Cancellation)$'
make check RUN='^TestRLSBase(Decision|DeniedTargetHistory)$'
make check RUN='^TestPrepared(PopulatedCompetingCompletionAndRetry|CompetingGIDDisposesWholeLoser|LateCancellationAndWaiterObservePublication)$'
make check RUN='^(TestPreparedSerializableCandidateRetainsAdmittedSnapshot|TestTruncateSnapshotsBirthFilter)$'
```

Routine call admission is pinned by engine units that build their
contexts through an `Engine` (grep `internal/engine/*_test.go` for
`exec.AdmitRoutineCall`, `exec.RoutineProgram` and `routineAccessOf`), for
example:

```sh
make check RUN='^Test.*(OriginalAuthority|RoutineAuthority|BodyLifetime|PreparedSQLActual|InvokerAndRIOwnerPhases|CheckReviewScalar|JSONBPath|UserOperatorRoutine|StrictParameterNull|StrictDemand).*$'
```

Two reference fixtures wait for native stack frames by name, so keep them:
`sortOrderedAggregate` (`TestReferenceOrderedSetAggregateActiveCancellation`)
stays unexported, and the recursive union stage keeps the unexported defined
type `recursiveUnionStage`, which `exec` names as the alias
`RecursiveUnionStage` (`TestReferenceQueryCycleLifecycle`). Renaming either
makes that test's unbounded query run until it exhausts memory.

Expression preflight is pinned by engine units that bind through an
`Engine` (grep `internal/engine/*_test.go` for `exec.PreflightCoercions`,
`exec.SimplifyConditional` and `exec.OverlapsInlineSafe`), for example:

```sh
make check RUN='^Test(CastCoercion.*Preflight.*|Conditional.*|StrictParameterNull.*|StringVariadic.*|DatetimeOverlaps.*|DomainCheckPreflightDemandInternal|RuleStaticOperatorPreflight|UpdateCoreRecursivePreflight.*)$'
```

The executor's unit tests still live in the engine, because they build
statements through an `Engine` and `Session`: grep
`internal/engine/*_test.go` for `exec.OpenNode`, `exec.Finalize`,
`exec.Source`, `exec.RowCursor` and `statementHost`. Run row-lock, cursor,
foreign-scan, CTE and detach selections with `RACE=1`.

Public proof is the reference selection of step 11, in quick mode and with
`REFERENCE=1`:

```sh
make check RUN='^TestReferenceQuery'
make check RUN='^TestReference(RowLock|QueryLock|ViewLocks|SQLCursor|PGCursor|TransactionIsolation|PGSequence)'
make check RUN='^TestReferenceFDW(Scan|Rescan|CursorLifecycle|LocalRechecks|LocalConsumers|Transactions|Concurrency)'
make check RUN='^TestReference(Domain|Reg(class|type|proc|role|oper|namespace|collation|config|dictionary)|GroupingSets|CTEDML|CommitTime|TransactionIdentity|Advisory|Routine)'
```

Scoped input provenance proof: `make check RUN='^TestCopyFilter(InputProvenance|AliasProvenance)$'`
and existing `^TestJSONBRecordDomainErrors$`. Alias proof invokes the actual scoped
adapter with a direct soft-marked42P01 CHECK-body cause and requires a hard outer
result, unchanged public diagnostic/cause, and cancellation identity. Both COPY-filter and JSONB-record
dual-mode contracts remain required when this shared adapter changes; selectors
are documented by their owning reference packages.

## Row-policy runtime

`RLSProgram` is an execution-owned set of separately conjoined command groups.
`EvaluateRowSecurity` is the sole true-only USING/WITH CHECK evaluator;
`CheckRowSecurity` adds named/unnamed42501 diagnostics without masking expression
errors. Each imported occurrence owns serial clause context/query scratch,
separate from OLD/NEW and the caller frame. Scratch releases borrowed tuple/frame
references on every return. No catalog payload or prepared identity is modified.

`Source.RLS` borrows its statement-owned guard. `openRowSecurityStage` is a lazy
source-local producer before joins/aggregates/user work, retaining physical
occurrence/version identity. Frozen-source bridges guard the live input before
projection, and row-mark refreshed-source checks invoke the same evaluator.
Guarded sources are excluded from raw hash/pruning/parallel bypasses. Ordinary
statement Finish releases imported query states and nested pins outside Engine.mu.
Successful SQL cursor/protocol publication instead transfers the owning programs
to the existing producer; borrowed sources never close them. Clause adapters
detach from the opening scope and use shared authority/current FETCH callbacks.
EOF/CLOSE/error/cancel/hold materialization release stages, programs and nested
pins before clearing authority. Eager immutable data clears borrowed RLS pointers.

Supported rowmarks inside policy subqueries retain the statement's one additive
`LockContext` manifest and ordinary legality/ACL/wait/cancel/recheck paths. Private
query-source slots are not global physical occurrence IDs. The engine prepares
the complete selected policy/view occurrence tree before its first data capture,
then captures only the union of actual command, target and imported source roots.
Physical capture consumes those prepared occurrences without another policy
selection or view import. A later policy's first mark therefore cannot leave an
earlier relevant input without its identity. Existing hierarchy-lock limits remain.

The root owns admission records around the borrowed programs, including bypass
metadata, and fences the complete graph only at completed outer target/source
admission boundaries. An unchanged immutable role snapshot reuses applicability
proof, never row truth or privilege; changed snapshots compare actual selection.
Additional imported-source waits dirty only the current original logical command
for its existing ACL walker, before provider preflight. The original logical
execution record survives physical lowering and lexical rule/routine activation;
no historical producer declaration is reauthorized during FETCH.

`TestViewProducerReentry` directly checks lazy first pull, short-circuit reader
close, cached-range replay/remaining-row resume, stable versus demanded-volatile
output evaluation counts, lineage reset, range Drain and exact-once source-owner
disposal. `TestViewProducerFailure` retains expression/cancellation failures
across reader reopen and rejects stale prefix replay. Select
`make check RUN='^TestViewProducer(Reentry|Failure)$'`; these native owner/count
invariants supplement, not replace, the pinned RLSCombinationViewDemand witness.
