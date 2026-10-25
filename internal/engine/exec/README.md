# internal/engine/exec

The query executor runtime and the concrete evaluation context. It opens bound
`plan` trees over captured sources, produces rows, arbitrates row locks and
evaluates expressions. It reaches statement, session and engine state only
through one host interface and one installed preparation value, both
implemented by the root engine.

## Imports

Layer L9.5 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture). It imports
`plan`, `expr`, `functions`, `catalog`, `storage`, `visibility`, `lock`,
`access`, `ids`, `proc`, `authorization`, `parser`, `sqltype`, `sqlstate` and the
standard library. Its `extension` imports validate genuine installed cube-spatial
and earth-point calls before kernel evaluation. It never imports the engine,
`analyze`, `session` or another feature package (`routine`, ...).

The shared checked routine call/frame contract also admits PL definitions.
Declaration equivalence includes real language/body identity; current validated
PL call capability is required even on retained paths. EXECUTE remains live,
while language USAGE belongs to DO/CREATE admission rather than each call.
PL execution stays with the root's existing lexical/root operation owner; no
second SQLRoutineExpr family or fake handler scalar is introduced.


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
- **Private row owners** (`nested_query_rows.go`, `cte_owned_rows.go`,
  `fts_rewrite_rules.go`). `OpenNestedQueryRows` is the all-row producer that
  PL/pgSQL static query loops, bounded static SELECT capture and `ts_rewrite` SQL
  borrow. It reads the live host's environments for its whole lifetime, owns its
  source from the call on, and keeps open errors distinct from row-demand errors.
  Streaming SELECT CTE resources transfer to `CTEOwnedRows`, including completed
  writable auxiliaries under static capture: its `Close` closes the main stage
  before its CTE chain and captured sources, which FOUND publication after
  producer cleanup relies on. Borrowed CTE scope pointers restore independently
  of retained resource ownership. `ConsumeRewriteRules` reads rule rows in fetches
  of at most 100 into `sqltype.FTSRewriteRules` and closes the cursor;
  `RewriteSQLDiagnostic` maps SPI prepare/open errors, never FETCH errors.
  Statement preparation and dispatch re-entry stay in the engine.
- **SQL cursor positioning** (`cursor_motion.go`): `CursorPosition` (embedded
  in the root cursor) with its `CursorPlace`, and `Move` and `Rewind`, which
  run FETCH and MOVE over a `CursorInput`: a live producer reached through the
  `CursorProducer` capability (`Context`, `Open`, `Exhausted`), or a promoted
  HOLD store. Motion takes no lock; its callers (FETCH, MOVE, PL cursor
  motion and HOLD materialization) hold nothing, and the capability runs with
  nothing held. A forward-only producer closes its stage, then reports
  `Exhausted` once, at its end.
- **Evaluation context** (`context*.go`, `node_runtime.go`, `aggregate.go`).
  `Context`, with its `EvalFrame`, `EvalStatement` and `EvalAdmission` groups,
  implements `expr.Context`, `plan.Runtime`, the `functions` runtimes,
  `sqltype.DomainCheckEvaluator` and the `pgcatalog` ACL inquiry; capability
  methods forward to the host. `InputDomainChecks` scopes fresh text input:
  domain violations stay soft, CHECK execution errors carry the hard
  `sqlstate.InputError` marker. COPY and JSONB record input use it.
  Snapshot inquiry forwards to the admitted host's immutable public value,
  selected together with the tuple frontier, never sampled during Eval.
  Immutable expression nodes and prepared descriptors retain no runtime owner.
  PL assignment projections borrow a balanced planning-origin flag while
  evaluating their child; SQL calls forward it explicitly, excluding active
  domain VALUE frames. Diagnostic cloning preserves planning-origin markers;
  runtime coercion and CHECK failures do not become planning errors.
  Assignment preflight simplifies SQL constants before importing execution-frame
  coercions; failures in the import phase retain their runtime origin.
- **Routine admission and descriptors** (`routine_*.go`): planning-time EXECUTE
  admission (`AdmitRoutineCall`, `AdmitRoutineReference`,
  `EvaluatePlanningExpression`), the consumed-call footprint `RoutineProgram`,
  and descriptor extraction, revalidation and the SQL-body walk
  (`WalkRoutineBody`), shared with `analyze` and the engine. Admission reads
  `RoutineAccess`: the captured `access.View` and `access.Environment`.
  Public labels/owners are mutable COW metadata, not immutable declaration
  identity. Address, kind, exact signature/result, payload/provider/body links
  and original domain/epoch/SubID manifests remain checked. Hard recipes prove
  their captured declaration and original revision while following current
  labels/ownership; original-source calls retain their revision fence and
  prepared reanalysis. `RoutineProgram` preserves this distinction through
  readmission and phase initialization without retaining granted rights.
  `CheckProgram` owns ordinary CHECK cooked expressions plus command/actor/RI
  phases, each holding a stable `RoutineProgram` pointer. The live statement
  retains that same pointer through lazy truth and existing wait/publication
  readmission; catalog trees retain neither executable state nor authority.
  A new actor phase admits original consumed calls before borrowing cooked
  expressions, without replaying truth or retaining permission results.
  Earth coordinate body failures use `EarthCoordinateDiagnosticName` in
  `context_admission.go`: the same descriptor extraction and stored-declaration
  revalidation select the current name from the statement's catalog. Hard calls
  captured before rename and remapped Restore calls do not cache diagnostic
  labels; argument/coercion failures never enter this error-only accessor.
  Grafted rules borrow the original manifest only after validating the live
  relation's exact rule/action-or-condition presentation. Ancestry is per
  occurrence, not the whole rewritten command; substituted fresh inputs return
  to their own source lane. Consumed-call dedup retains recipe and original
  declaration identity in addition to reference, consumer and actor.
  Aggregate/window admission receives each slot's original checked declaration.
  Distinct aggregate declarations retain separate slots even when their providers
  compute equal values. An old manifest stamp is never paired with a borrowed
  current descriptor. Only the existing hard lane follows mutable revisions;
  direct and genuinely ref-only lanes stay strict. Each slot checks current EXECUTE.
  Query grouping and window peer equality use that same original-declaration
  fence, with recipe ancestry selected by the owning query key rather than its
  possibly substituted expression. The nil-carrier revision rejection remains
  intentional for genuinely reference-only consumers; no current descriptor
  can supply proof beside an old manifest stamp.
  Strict NULL suppresses invocation/EXECUTE demand, not original-source lifetime
  validation. Post-child strict elimination first computes and checks admission
  for required immutable children. Random's conditional, preflight and typed-NULL
  entry paths share one readiness decision: required immutable parameter inputs
  must be fully imported before erasure; missing frames defer without evaluation.
  Runtime-only siblings are never sampled to prove NULL. A bare literal NULL
  retains usage and position, not the erased call's carrier or invocation
  properties. The analyzed source and full original manifest remain unchanged.
  `TestJSONBRecordRoutineOwnershipCOW` contrasts stale direct
  admission with an original-manifest hard capture after owner COW, including
  current-owner success, revoked-reader denial and retained-root immutability.
  `TestRoutineDirectStrictNullLifetime` uses the actual statement-host source
  lane to require strict-NULL admission without EXECUTE while rejecting an
  owner-revised current root and preserving the old snapshot. Selected proof:
  `make check RUN='^(TestJSONBRecordRoutineOwnershipCOW|TestRoutineDirectStrictNullLifetime)$'`.
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
| Foreign owner | `ReleaseReplacementRead`, `TouchForeignScan`, `EnlistForeign`, `AsyncAppendEnabled` | Release the replacement read lock before external I/O; enlist the statement's foreign transaction participant. Async eligibility reads the immutable admission snapshot once. |
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
Only a private PL query-preflight context opts into failure provenance: actual
`Eval` failures retain whether that computation consumed imported parameters or
SQL constants, and the current computation shadows a nested callee's origin.
The original context is not marked or retained; prepared domain checks propagate
back through the existing copyback seam. Routine-admission failures occur before
this boundary and remain unmarked. Ordinary preflight returns the original provider
error unchanged, including its direct SQL error interface. Opted-in failure wrappers
preserve the original cause for diagnostic decoration.

`StrictRoutineArguments` maps expression children to actual nullable call
formals for both permission admission and parameter-NULL folding. Expanded
generated multirange constructor members form one non-NULL variadic array:
a NULL member must reach the constructor's `22004` check, while an explicit
NULL VARIADIC array and the strict single-range overload retain NULL results.

## Invariants

- **No mutexes.** The package adds no lock. Lock order, modes and waits belong to
  the engine host bodies and the `lock` manager; the lock manager's mutex is
  never taken under `Engine.mu`.
- **Snapshots.** Records borrow immutable published snapshots and catalog reads;
  no snapshot copies row values. Stable isolation retains its public value and
  tuple roots/frontier during the first snapshot-requiring analysis. Ordinary RC
  queries select their execution pair after relation admission, before Eval;
  snapshot-requiring utilities select theirs at utility entry, before internal
  waits. Late physical roots use that same frontier with birth/end filtering.
  Cursors retain their producer's pair when borrowing a fresh operation host
  (see the [`visibility` README](../visibility/README.md)).
- **Eager public results.** Finite public results are finalized before statement
  completion: late projection, sort, source-close or cancellation failure exposes
  no successful prefix. Public result reads do not resume SQL evaluation,
  acquire grants or publish writes, and expose copied values or immutable owned
  backing, never execution scratch. Nested SQL rewrite and PL query-loop rows
  instead borrow an active lexical scope and `OpenQueryProjectStage`, exposing
  schema before ordered row demand. Their owner closes the main stage before any
  retained CTE chain, memberships and sources, then restores lexical scratch.
  DML results retain their ordinary eager owner and are traversed without copying
  the full result. Grouping, distinct and windows are serial; the
  bounded SELECT scan is the parallel exception.
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
- **Async append.** Compatible UNION ALL arms share an execution-local foreign
  request frontier while retaining their ordinary nested SetArm evaluators.
  First demand primes every admitted leaf before waiting. Only the root waits
  on readiness; nested filters/projections propagate the exact private pending
  control token without closing, marking EOF, or changing occurrence position.
  No expression or callback error is reclassified as pending. Local evaluation,
  raw field import and publication stay on the leader. Each ForeignExecution
  owns one request or one imported buffered row, never an unbounded pump.
  ForeignScanOwner cancels all requests, joins all requests, then Ends scans.
  Its finite cursor Quiesce boundary imports pending source rows without local
  expression evaluation or further I/O; successful motion retains no request
  context. The next motion uses current authority and its admitted context.
  Expression invocations and repeatedly reopened correlated RHS use ordinary
  reusable scans instead of new async frontiers. A sticky lifetime fact survives
  frame-dropping child constructors; eligibility is checked at append admission
  and source adoption. CTE producers inherit their owner's state, not a reader's
  exclusion, so safe enclosing producers and root cursors remain eligible.
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

`TransitionAggregate` keeps an immutable selected plan with separate state/result
usages. Every group owns its transition state, transition `proc.Frame` and optional
final frame; immutable values need no speculative copying for any modification mode.
Strict transitions distinguish never-seeded state from transition-returned NULL.
Both support vectors use retained binary datum conversions, never current casts;
strict transitions without an initial value recheck compatibility at startup.
Admission checks current aggregate-owner EXECUTE for both supports even when
strict NULL finalization will skip invocation.
Owned window calls retain the actual checked provider and full argument vector.
Window startup rejects SHAREABLE/READ_WRITE before opening its input, so an
empty input cannot bypass mode admission or run a support routine.
The rejection formats the selected declaration through the current statement's
regprocedure presenter, preserving visibility-aware qualification, identifier
quoting and argument type spelling rather than reconstructing written syntax.

`FinalizeWithContext` borrows the current statement runtime and invokes the real
selected scalar final once per computed group without overwriting its state.
Ordinary direct-argument count is zero: the final receives state, plus typed NULL
extras when declared, never last-row values or enclosing query parameters.
Strict skipping returns NULL of the result type. SQL finals share the statement's
staged writes and cancellation boundary. Context-free `Finalize` is valid only
without an actual final function. C memory contexts, expanded-datum pointers and
`AggCheckCallContext`/`AggStateIsShared` belong to the external native ABI, not
mutable SQL value carriers.

## What stays in the engine

Statement dispatch; query admission and capture (`query_admission.go`,
`captureQuerySources*`); the bodies behind the `PreflightQuery` and
`AdmitExpression` forwarders (they need `stored` and `table`, so they live in
`view`); domain CHECK programme preparation; the `query_*_bind.go` binders and
join legality; the DML prologues (executors are in [`dml`](../dml/README.md));
data-modifying CTEs, CREATE TABLE AS, the SQL cursor registry, admission, HOLD
materialization and disposal, portals and rule execution;
`statementHost` and `enginePreparation`; query grouping admission. Callers name
the types here directly (`exec.Context`, `exec.Source`, ...), without aliases.

## Testing

Package units cover the preparation seam, stored-view producers, source
ownership, cursor motion (`RUN='^TestCursor'`), transition aggregates,
ordered-aggregate sort cancellation, the pgcrypto notice host and `pgrowlocks`
TIDs. Executor, row-lock, cursor,
foreign-scan, preflight and routine-admission units build an `Engine` and stay
in `internal/engine` (grep for `exec.OpenNode`); run row-lock, cursor,
foreign-scan, CTE and detach selections with `RACE=1`. `InputDomainChecks`
changes must keep the COPY filter and JSONB record reference tests passing.
Foreign parallel dispatch uses `testing/synctest` (`RUN='^TestFDWParallel'`):
settled callback counts, never elapsed time, establish backpressure.
Async protocol, cleanup, filtered-frontier and cursor-boundary units use
`RUN='^TestFDWAsync'`; public authentic-transport scenarios live in
`reference/tables` (`RUN='^TestReferenceFDWAsync'`).

**Frame names matched by tests.** `sortOrderedAggregate`
(`TestReferenceOrderedSetAggregateActiveCancellation`) stays unexported, and the
recursive union stage keeps the unexported defined type `recursiveUnionStage`
(aliased as `RecursiveUnionStage`; `TestReferenceQueryCycleLifecycle`). Both
fixtures wait for these stack frames by name; renaming either makes the test's
unbounded query run until it exhausts memory.

```sh
make check RUN='^Test(MissingPreparation|InstallPreparation|ViewProducer|SetArmSource|CorrelatedJoinSource|QueryBranchPruning|SortOrderedAggregate|PgcryptoNotice|PgrowlocksLogicalTID)'
make check RUN='^Test(CreateAggregate|AggregateFinal)'
make check RUN='^Test(PLpgSQLQueryLoopLifetime|FTSQueryFunctionsBatch)'  # row owners
make check RUN='^TestReferenceQuery'   # public query execution; add REFERENCE=1 for the oracle
```
