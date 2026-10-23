# internal/engine/exec

The query executor runtime and the concrete evaluation context. It opens bound
`plan` trees over captured sources, produces rows, arbitrates row locks and
evaluates expressions. It reaches statement, session and engine state only
through one host interface and one installed preparation value, both
implemented by the root engine.

## Imports

Layer L7 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture). It imports
`plan`, `expr`, `functions`, `catalog`, `storage`, `visibility`, `lock`,
`access`, `ids`, `proc`, `authorization`, `parser`, `sqltype`, `sqlstate` and the
standard library, never the engine, `analyze`, `session` or a feature package
(`routine`, `extension`, ...).

## What it owns

- **Runtime records** (`query.go`, `query_sources.go`): `Source` (a captured
  relation or composite source), `State` (one query invocation), `Stage` (an
  indexed producer stage), `FinalRows`, `Authority` (the borrowed statement
  authority a held cursor keeps) and `LockContext`. `SelectQuerySource` takes
  resolved `visibility.HeapOperations`; the row loop calls them, never a catalog
  lookup, callback or alternate visibility algorithm.
- **Openers and producers** (`query*.go`, `window*_query.go`,
  `aggregate_query.go`): `OpenNode` dispatches every plan node; `Finalize` and
  `FinalizeSelect` turn a root into a `RowResult`. CTEs, set operations,
  recursive unions, windows, aggregates, DISTINCT, LIMIT/WITH TIES and subqueries
  live here.
- **Stored-view producers** (`view_producer.go`). A stored-view `Source` owns its
  retained stage and lazy flat prefix; later readers replay the prefix, then
  resume the one producer frontier. Failures stay terminal; `Source.Close`
  disposes the producer once, and reopening a reader never recaptures it.
- **Row locks** (`query_locks.go`, `query_pending_locks.go`, `query_lock_*.go`):
  view-row arbitration, recheck of locked rows, pending marks and locked-order
  refresh.
- **Cursors and other sources**: `RowCursor` and `RowResult` (read by the
  driver), the foreign scan stage (`foreign_*.go`) and the `pg_sequences` stage.
- **Evaluation context** (`context*.go`, `node_runtime.go`, `aggregate.go`).
  `Context`, with its `EvalFrame`, `EvalStatement` and `EvalAdmission` groups,
  implements `expr.Context`, `plan.Runtime`, the `functions` runtimes,
  `sqltype.DomainCheckEvaluator` and the `pgcatalog` ACL inquiry; capability
  methods forward to the host. `InputDomainChecks` scopes fresh text input:
  domain violations stay soft, CHECK execution errors carry the hard
  `sqlstate.InputError` marker. COPY and JSONB record input use it.
- **Routine admission and descriptors** (`routine_*.go`): planning-time EXECUTE
  admission (`AdmitRoutineCall`, `AdmitRoutineReference`,
  `EvaluatePlanningExpression`), the consumed-call footprint `RoutineProgram`,
  and descriptor extraction, revalidation and the SQL-body walk
  (`WalkRoutineBody`), shared with `analyze` and the engine. Admission reads
  `RoutineAccess`: the captured `access.View` and `access.Environment`.
- **Helpers the DML executors share**: constraint-violation DETAIL rows
  (`row_detail.go`), single-relation source capture (`source_capture.go`),
  CURRENT OF scans (`cursor_scan.go`), `pgrowlocks` values and the session
  identity snapshot (`identity.go`).
- **Seams**: `Host` (`host.go`), `DetachedPresentation`, `Preparation`
  (`preparation.go`) and the admission slots `AdmissionAuthority` and
  `RoutineProgram`.

## The host

`Host` (`host.go`) lists every method with its group. The engine implements it
on `statementHost`, a defined type over `StatementScope`.

| Group | Methods | Lock and lifetime contract |
|---|---|---|
| Environment | `Context`, `Environments`, `StatementStart`, `CTEInvocation` | Read where the runtime reads them; a held cursor detaches the host between fetches. |
| Presentation | the nine `sqltype` formatters | Read the statement's comment/deparse snapshot. With no host, output uses `DetachedPresentation`. |
| Row locks | `AcquireQueryRow`, `LatestQueryRow`, `QueryLocks` | `AcquireQueryRow` may block in the lock manager; it releases the replacement read lock across the wait, then takes `Engine.mu`. `LatestQueryRow` takes `Engine.mu` and records the physical row event after releasing it. |
| Foreign owner | `ReleaseReplacementRead`, `TouchForeignScan`, `EnlistForeign` | Release the replacement read lock before external I/O; enlist the statement's foreign transaction participant. |
| Admission | `RoutineAdmission` | Reads statement-owned routine authority and dependency manifest without a lock; fails once the statement has finished. |
| Sequences | `CompletePGSequence` | Acquires the sequence lock, then reads the generator under `Engine.mu`. |
| Capabilities | the function, expression and plan runtime bodies | Take `Engine.mu`, read session state or have side effects (advisory locks, XID assignment, sequence calls, notices). |

Every method is called without `Engine.mu` held, and none returns an engine
struct. Records store the host once, never per row. A record with no statement
scope holds an untyped nil host (`LiveHost` never returns a typed nil). Optional
host capabilities such as `PgcryptoNotice` are resolved through `LiveHost` per
delivery; the engine appends owned notice records and never calls user handlers
under engine locks.

## Preparation

Domain checks and preflight also run with no statement scope (generated
columns, index expressions and predicates, partition admission, temporary
commit, concurrent refresh), so they cannot hang off a nil-able host.
`Preparation` is one stateless package value: `AdmitExpression`,
`PreflightQuery`, `PrepareDomainConversion`, `SpecializeRuleParameters`,
`CheckDomain`, `DomainCatalog`, `ReturnsSet`, `StoredReferenceValid` and
`ValidInstalledText`. The engine installs its implementation from `init` with
`InstallPreparation`; until then every method panics with
`exec: preparation not installed`.

## Expression preflight

`exec` owns the expression preflight that runs before execution:
`PreflightCoercions` and `PreflightCoercionsWithPolicy`, conditional planning and
folding (`conditional_*.go`), strict-NULL parameter folding, array and UPDATE
value preflight, string variadic folding, view qualification mapping and the
OVERLAPS route. Preflight reaches the engine only through `Preparation` and runs
outside engine and publication locks. Conditional planning keeps every analyzed
dependency and authorization even when it removes a branch; held parameters are
reconsidered on every execution, never stored as planning facts.

## Invariants

- **No mutexes.** The package adds no lock. Lock order, modes and waits belong to
  the engine host bodies and the `lock` manager; the lock manager's mutex is
  never taken under `Engine.mu`.
- **Snapshots.** Records borrow immutable published snapshots and catalog reads;
  no snapshot copies row values. Repeatable read retains every immutable root
  before analysis waits; read committed views stay fresh at grant time (see the
  [`visibility` README](../visibility/README.md)).
- **Eager results.** Finite results are finalized before statement completion: a
  late projection, sort, source-close or cancellation failure exposes no
  successful prefix or cursor. `RowCursor` reads never resume SQL evaluation,
  acquire grants or publish writes, and results expose copied values or
  immutable owned backing, never execution scratch. Grouping, distinct and
  windows are serial; the bounded SELECT scan is the parallel exception.
- **Row locks.** Rowmarks lock actual physical identities. After a wait only
  frozen contributors are followed to current heads; rechecks rebuild their
  bound frames without finding new matches or resorting. Immediate wait policies
  use the lock manager's atomic grant path, and skipping an unavailable row
  releases no earlier grant.
- **Cancellation** is checked at fixed points; cursors release pins and sources
  on every exit, and `Close` is idempotent.
- **Admission slots are borrowed.** `EvalAdmission` copies the authority and
  routine program pointers, never the pointees; only engine helpers type-assert
  the authority. `RoutineAccess.Env` borrows the engine's view, is never retained
  past admission and is not consulted for procedures, so `Readmit` switches only
  `View.State.CurrentUser`.
- **Foreign parallelism.** Only `FinalizeSelect` admits foreign partitioning
  (`foreign_parallel.go`, owned by `ForeignExecution`): leader-only enrollment,
  bounded transfer of owned cells, cancellation on the first error, joined scans
  before shared cleanup. Local import, filter, sort and projection stay serial;
  a failed finalization keeps nil rows, count 0 and the last publication fence.
- **No per-row allocations** from the seams: the host and preparation are
  interface values stored once.

## Row-policy runtime

`RLSProgram` is an execution-owned set of separately conjoined command groups.
`EvaluateRowSecurity` is the sole true-only USING/WITH CHECK evaluator;
`CheckRowSecurity` adds the 42501 diagnostics without masking expression errors.
`Source.RLS` borrows its statement-owned guard, and `openRowSecurityStage` guards
the source before joins, aggregates and user work; guarded sources are excluded
from raw hash, pruning and parallel bypasses. Rowmarks inside policy subqueries
share the statement's one `LockContext`. The engine prepares the complete
policy/view occurrence tree before the first data capture, and physical capture
consumes it without another policy selection.

Statement Finish releases imported query states and nested pins outside
`Engine.mu`; cursor or portal publication instead transfers the programs to the
producer, which releases them on every exit before clearing authority. Borrowed
sources never close them.

## Transition aggregates

`TransitionAggregate` keeps an immutable selected plan and execution-local
state. Strict transitions distinguish never-seeded state (which may copy the
first valid input) from a transition-returned NULL (which never reseeds).
Implicit-binary SFUNC and seed conversions use the captured physical datum
operation, never the current cast row; strict providers without an initial
value recheck live binary compatibility once at startup, before any input.

## What stays in the engine

Statement dispatch; query admission and capture (`query_admission.go`,
`captureQuerySources*`); the bodies behind the `PreflightQuery` and
`AdmitExpression` forwarders (they need `stored` and `table`, so they live in
`view`); domain CHECK programme preparation; the `query_*_bind.go` binders and
join legality; the DML prologues (executors are in [`dml`](../dml/README.md));
data-modifying CTEs, CREATE TABLE AS, SQL cursors, portals and rule execution;
`statementHost` and `enginePreparation`; query grouping admission. Callers name
the types here directly (`exec.Context`, `exec.Source`, ...), without aliases.

## Testing

Package units cover the preparation seam, stored-view producers, source
ownership, transition aggregates, ordered-aggregate sort cancellation, the
pgcrypto notice host and `pgrowlocks` TIDs. Executor, row-lock, cursor,
foreign-scan, preflight and routine-admission units build an `Engine` and stay
in `internal/engine` (grep for `exec.OpenNode`); run row-lock, cursor,
foreign-scan, CTE and detach selections with `RACE=1`. `InputDomainChecks`
changes must keep the COPY filter and JSONB record reference tests passing.
Foreign parallel dispatch uses `testing/synctest` (`RUN='^TestFDWParallel'`):
settled callback counts, never elapsed time, establish backpressure.

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
