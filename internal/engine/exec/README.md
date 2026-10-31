# internal/engine/exec

The query executor runtime and the concrete evaluation context. It opens bound
`plan` trees over captured sources, produces rows, arbitrates row locks and
evaluates expressions. It reaches statement, session and engine state only
through one host interface and one installed preparation value, both
implemented by the root engine.

Runtime workload admission is a local context capability supplied by unlocked
core owners. `setContext` keeps the selected query execution context (or host
fallback) authoritative for cancellation, deadlines, causes and unrelated values;
only an active work policy follows a runtime context replacement. Retained stage,
function-source, join, CTE and set-arm evaluators hold no completed pull context:
their local context is installed for evaluation and cleared on every exit.
Full-text vector conversion forwards the effective evaluator context explicitly,
never recovers an older operation context from the host.

EXPLAIN ANALYZE attaches an invocation-local `Observation` through the actual
runtime context. Prepared graph/node identities, not labels or source capacity,
join frozen presentation to actual output. Each opened stage owns private
counters; parallel workers register separate records and the settled owner
aggregates them after joining. Demand enters a loop, including empty demand;
rewind alone does not. Occurrence pulls, projected output and blocking drains
share a frontier, so expression access does not emit a second row. Blocking sort
separates its consumed input frontier from its demanded sorted output. Optional
monotonic timing measures active inclusive spans, never consumer idle intervals.
Derived, materialized-CTE and set-arm consumers keep the owning projection span
active across their actual expression/coercion/validation frontier, including
error exits. Cached CTE replay does not reevaluate or retime projection work.
Parallel aggregation reports one logical aggregate loop and demanded finalized
groups, not worker partial tuples. Its elapsed value sums active worker partial
and leader combine/finalization work plus logical output demand; this can exceed
wall time. Copied leader HAVING/sort/project wrappers use invocation-local aliases
to their original nodes. This is not CPU time or a physical startup measurement.
No observation means no counter allocation or clock sampling. Observations own
neither source pins nor statement/publication authority and are never retained
in prepared definitions.
Foreign observations capture detached properties at actual provider plan
selection, before runtime startup, rather than rediscovering paths for display.
Composite observations retain the original replaced node key, not a synthetic
scan identity; pushed children have no invented local measurements. Provider
explain callbacks receive separate layout backing and the actual selected
private token. Only owned presentation strings and, for JSON, detached operation,
source identities, provider estimates and ordered properties survive scan cleanup.
Structured values are captured at that selection callback, not parsed from text;
Foreign returns separate backing for all slices/maps under the observation lock.
Composite discovery freezes current typed parameters once before provider
selection. Explanation and initial Begin reuse that detached statement snapshot;
runtime transaction/characteristics join later without replacing selected facts.
Each ReScan instead re-exports the current typed frame and output environment
under its current demand context through the existing scan owner, without
rediscovery, another import or expression evaluation. The explained selection
and its private token remain unchanged.
Modification planning checks observation presence before making its optional
defensive callback request; ordinary unobserved execution makes no unused copy.

## Imports

Layer L9.5 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture). It imports
`plan`, `expr`, `functions`, `catalog`, `storage`, `visibility`, `lock`,
`access`, `ids`, `proc`, `authorization`, `parser`, `sqltype`, `sqlstate`,
`pgcatalog` (projected relation-kind admission), `util` and the standard library.
Its `extension` imports validate genuine installed cube-spatial,
earth-point, autoinc and insert_username scalar calls before kernel evaluation.
Insert_username authenticates current provider identity and reference domain/epoch
before reporting scalar misuse; structural native-trigger capability grants no
execution authority. It never imports the engine,
`analyze`, `session` or another feature package (`routine`, ...).

Detached query evaluations admit context-scoped cooperative execution pauses.
The request is dormant during analysis; `State.Evaluation` enables it only for
runtime evaluation. Aggregate and recursive work use stable kernel identities,
not callbacks or stack inspection. Raw operation contexts remain cancellation
and publication authority; nested binders remove execution permission.
Invocation-owned worker states retain their selected activated effective child
across pull resets, without per-row wrappers. A fresh worker invocation selects
its own child and pause attachment; the incoming source never replaces that
child's cancellation, deadline, cause or unrelated values. Generic `setContext`
does not activate pauses or metadata classifiers. Terminal stages clear their
borrowed evaluator graph.


The shared checked routine call/frame contract also admits PL definitions.
Declaration equivalence includes real language/body identity, semantic routine
attributes and immutable configuration identity; stale carriers cannot borrow a
changed declaration. Current validated PL call capability is required even on retained paths. EXECUTE remains live,
while language USAGE belongs to DO/CREATE admission rather than each call.
PL execution stays with the root's existing lexical/root operation owner; no
second SQLRoutineExpr family or fake handler scalar is introduced.
Standalone CALL consumes that same private input frame and handler. Procedure
outputs retain the declaration's tuple identity, including sole OUT; no-output
PL handlers retain their private empty return, normalized only at the typed
invocation-value boundary. No caller slot receives writable copyback here.

SQL scalar and set openings share once-only written/default input evaluation and
one private frame. The statement host runs the same borrowed routine runner;
set opening completes all terminal rows privately before transferring immutable
values to `expr.OwnedSource`. Errors expose no partial collection. Closing,
exhausting or canceling a cursor releases its collection, independently of
returned values and sibling invocations. The scalar path creates no set buffer.

`ConsumeCrosstab` drains the admitted source before compatibility checks, closes
it on success, error and cancellation, and transfers only owned immutable tuples.
The pivot groups adjacent NULL-safe output strings, consumes NULL value slots
and never serializes discarded excess cells. Declared type identity and
directional typmods are checked without implicit conversion acceptance. Mismatch
diagnostics borrow the caller's catalog-backed type namer for scalar and recursive
array-element qualification and identifier quoting while retaining builtin
typmods; naming is captured only on failure and is never retained in result tuples.


## What it owns

- **Runtime records** (`query.go`, `query_sources.go`): `Source` (a captured
  relation or composite source), `State` (one query invocation), `Stage` (an
  indexed producer stage), `FinalRows`, `Authority` (the borrowed statement
  authority a held cursor keeps) and `LockContext`. `SelectQuerySource` takes
  resolved `visibility.HeapOperations`; the row loop calls them, never a catalog
  lookup, callback or alternate visibility algorithm.
  Query identity demand retains source-occurrence lineage for virtual recipes
  and ordinary `tableoid` alike. A tableoid leaf follows its lexical frame and
  actual physical source reference; outer absence is typed NULL, not inferred
  from ordinary tuple values. Generated-owner recipes retain their separate
  zero-coordinate mode. Neither path fabricates a stored datum or row counter.
  Dependent function sources reuse the same lineage on each invocation. An
  explicit identity-prefix marker carries zero-column ancestors without inventing
  a tuple slot, and nested joins merge it with their local-left source identities.
- **Openers and producers** (`query*.go`, `window*_query.go`,
  `aggregate_query.go`): `OpenNode` dispatches every plan node; `Finalize` and
  `FinalizeSelect` turn a root into a `RowResult`. CTEs, set operations,
  recursive unions, windows, aggregates, DISTINCT, LIMIT/WITH TIES and subqueries
  live here.
  Runtime work admission consumes one `WorkAggregateInput` grant per aggregate
  support/input initialization before child materialization, not per sort or
  complete child drain. Recursive union consumes one `WorkRecursiveUnion` grant
  per producer row/round step. Refusal uses the existing private-stage and CTE
  failure/close owners; no policy or lifetime grant is retained by the plan.
  Aggregate partial workers capture the leader's outer CTE owner before launch,
  including a real nil owner. `State.ClosedEvaluation` closes environment and
  presentation inheritance for each evaluator and its synchronous derived
  frames, including authoritative nil resolvers/formatters/CTEs. The host's
  `CaptureParallelEvaluation` factory shares immutable admitted facts while
  giving each worker and the leader private lazy caches and diagnostic sinks.
  Capture happens before support invocation, never per row. Workers must not
  reread XML mode, coercion resolver fallbacks or presentation from a sibling's
  mutable SQL host. Nested SQL bodies retain their own gate-owned live lexical
  inheritance, not their caller's outer closed capture.
  `State.Methods` is a value-only admission snapshot. Child, correlated, CTE,
  view and worker states copy it; retained streams capture before host rebinding.
  Hashjoin off avoids the join hash structure, indexscan off avoids candidate
  probing, and hashagg off uses collision-safe equality search without key
  hashes/buckets (including plain DISTINCT and partial-aggregate admission).
  Duplicate grouping-set occurrences retain separate groups. Aggregate-local
  DISTINCT/ORDER BY remain mandatory input operations. Gathermerge off routes
  only eligible ordered local SELECT to serial finalization; unordered scans,
  foreign partition scans, async append and other aggregate choices keep their
  independent permissions. Required scan/loop/sort/materialization work remains
  valid even when its optional alternatives are discouraged.
  A separate parallel-operation context spans leader admission, partial workers,
  state combination, final support and the result graph. Worker identity implies
  that operation, but the leader is not a worker. Reached sequence mutation and
  nested SQL write admission use the operation marker, so leader support cannot
  escape restrictions that apply to worker support. The derived context retains
  request cancellation and does not mark subsequent ordinary executions.
  `SQLInvocationContext` is separate synchronous lexical ownership, not worker
  or effect identity. `FinalizeSelect` never launches nested work within it or
  within an existing parallel operation; eligible outer partial scans remain
  concurrent. Captured evaluator diagnostics flush once after joined workers
  and leader completion on success, SQL error and cancellation; source pins and
  membership release retain their original lifetimes.
  This includes ACL-state INITCOND decoding in `TransitionAggregate.initialize`,
  which runs outside the SQL gate: its captured ACL resolver must use that
  evaluator's private sink, not append to the shared Operation notice list.
  The coordinator's existing drain is the sole publication boundary; repeated
  drains are empty. Literal/parameter specialization preserves provenance and
  immutable role/ACL authority without changing ordinary lexical input.
  Sources retain admitted typed CPU/parallel costs and detached maintenance
  cardinality, never a settings-store pointer. Each opening selects its scan
  decision before hashing or allocating candidates; the original predicate
  still checks every candidate. With indexscan enabled, seqscan off prefers a
  safely eligible captured index even when its estimated cost is higher; both
  methods off or no eligible index retain the required full-scan fallback.
  Default-on selection and all numeric cost estimates remain cost-based.
  Core EXPLAIN uses the same metadata-only eligibility, method policy and
  arithmetic and labels these paths Hash Candidates or Full Scan, not physical
  disk scans. Cursors/portals retain their opening decision; prepared execution
  admits current command methods/costs without mutating shared IR.
  `FinalizeSelect` compares serial work with setup+work/workers+tuple-delivery
  before either local query or partial-aggregate dispatch, after existing
  safety/shape gates and logical table-size admission. Foreign worker costing
  remains provider-owned. Explicit worker fixtures must request cheap costs.
- **Stored-view producers** (`view_producer.go`). A stored-view `Source` owns its
  retained stage and lazy flat prefix; later readers replay the prefix, then
  resume the one producer frontier. Failures stay terminal; `Source.Close`
  disposes the producer once, and reopening a reader never recaptures it.
  Frozen Input layout wrappers preserve the original actual `SourceNode` through
  recursive opening. A foreign input therefore still opens its genuine scan,
  with the same occurrence/async key and import diagnostics, below the layout
  projection. The schema-only `OpenSourceStage` entry serves manually owned
  nested-view/layout stages; physical query nodes enter through `OpenNode`.
  Foreign conversion diagnostics invert the occurrence's existing stable-
  attribute-derived Input Layout to select its original column alias. Physical
  import slots and remote widths remain unchanged; the same actual SourceNode,
  async key and ordinary codec/cleanup path are preserved.
- **Row locks** (`query_locks.go`, `query_pending_locks.go`, `query_lock_*.go`):
  view-row arbitration, recheck of locked rows, pending marks and locked-order
  refresh.
  Stored producers settle pending foreign marks before evaluating their output
  expressions, retaining the effective tuple versions in their existing lineage.
  Enclosing arbitration reuses those settled versions without projecting again.
  Local view marks keep their enclosing qualification frontier; neither path
  rescans sources, changes frozen contributors, or reorders captured rows.
- **Cursors and other sources**: `RowCursor` and `RowResult` (read by the
  driver), the foreign scan stage (`foreign_*.go`) and the `pg_sequences` stage.
  Optional typed foreign query paths replace an admitted join/aggregate/order/limit
  subtree at its opener, before opening absorbed children. Translation uses bound
  identities and full frame/cache/presence layouts; unknown expressions, local
  restrictions and incompatible mappings retain the incumbent local frontier.
  Ordinary grouping ships its single complete canonical set, preserving the
  aggregate frame's hidden typed INT4 occurrence-zero cell after keys/calls.
  Aggregate output expressions address the key/call result frame rather than
  the input frame; partial, repeated and multiple grouping sets remain local.
  Discovery and Begin run unlocked. Composite scans use the same codec importer,
  transaction enlistment and `ForeignScanOwner` as base scans, without a second
  cleanup registry. Private replacements never enter retained IR or persistence.
  Execution-private join restriction copies retain their canonical `ScanOwner`
  for both runtime measurements and selected composite properties. Local
  fallback and pushed execution therefore report the original join identity;
  pushed children are still unopened rather than credited with fictitious work.
  Rewind re-exports the invocation's statement parameters under the current demand
  context and calls ReScan on the owned scan. Selection and order-cache demand
  restart; source rows and packed presence remain append-only with stable
  occurrence identities. Correlated/repeated invocation shapes requiring a
  different parameterized plan are declined before composite discovery.
  `ForeignQuerySource.Plan` is shared with plain EXPLAIN: validate genuine size,
  every path and final layout, choose the first minimum TotalCost, and retain
  owned Size/Path/Plan/Slots. Returned layouts and subsequent callback arguments
  have separate backing; immutable opaque Private values remain provider-owned.
  This planning entry never begins a scan or enlists a transaction.
  `PlanForeignQuery` shares detached eligibility, typed translation, all-path
  validation and first compatible minimum selection with EXPLAIN. Its input
  contains occurrence authority and codec metadata, not live rows. Execution
  alone installs the retained complete selected path into a scan owner and opens
  it. `PlanForeignRowMark` similarly owns mark/identity/full-tuple metadata,
  including COPY and REFERENCE siblings in a marked graph, without transaction
  characteristics, enlistment, refetch or recheck.
- **Private row owners** (`nested_query_rows.go`, `cte_owned_rows.go`,
  `fts_rewrite_rules.go`). `OpenNestedQueryRows` is the all-row producer that
  PL/pgSQL static query loops, bounded static SELECT capture, `ts_rewrite` and
  `ts_stat` borrow. It reads the live host's environments for its whole lifetime, owns its
  source from the call on, and keeps open errors distinct from row-demand errors.
  Streaming SELECT CTE resources transfer to `CTEOwnedRows`, including pending
  writable auxiliaries under static capture. `Finish(primary)` closes the main
  stage, exhausts auxiliaries on success, then releases its CTE chain and sources;
  it separates execution failure from resource-cleanup failure. An existing
  caller failure aborts auxiliaries without evaluating their callbacks. `Close`
  uses the same owner and returns either cause. Invocation resource close does
  not report an already retained producer execution error as a new cleanup error.
  The caller keeps its command frame live through this finish. Borrowed CTE scope
  pointers restore independently of retained resource ownership.
  `ConsumeRewriteRules` reads rule rows in fetches
  of at most 100 into `sqltype.FTSRewriteRules` and closes the cursor;
  statistics shares the bounded fetch helper, but validates one binary TSVECTOR
  column (including domains) after the first fetch and consumes every later batch.
  Its invocation-local typed accumulator counts retained positions without text
  re-input. Both consumers close before their caller restores lexical scratch.
  `RewriteSQLDiagnostic` maps SPI prepare/open errors, never FETCH errors, and
  preserves diagnostics already owned by nested SQL or an existing context.
  Actual refint SPI command boundaries instead use `SPIStatementDiagnostic`:
  positioned errors become internal query locations; positionless errors append
  the enclosing SQL statement to their context stack. Both copy before enrichment
  and leave non-SQL errors untouched.
  Modifying CTEs with statement effects may retain activation and exhaustion
  callbacks on their existing invocation-owned producer. First source demand
  activates eager typed materialization; reading a RETURNING row does not complete
  the modifier. EOF completes it exactly once, including empty output. Reader
  close never exhausts another reader's shared producer; the command owner
  exhausts remaining auxiliaries before success. Failure/owner close discards
  callbacks without evaluating helper SQL.
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
  TEXT and JSON text-search conversion forward separately through `LiveHost`;
  JSON carries typed document/filter operands so traversal and lazy configuration
  capture remain runtime-owned rather than expression-context state.
  Snapshot inquiry forwards to the admitted host's immutable public value,
  selected together with the tuple frontier, never sampled during Eval.
  Immutable expression nodes and prepared descriptors retain no runtime owner.
  Session activity forwards through `LiveHost`: FETCH borrows its current client
  source without replacing or depending on the cursor's clock snapshots.
  Neither source nor live session authority enters `CursorExpressionData`.
  For ordinary, noncaptured execution, `FunctionInput` overlays only XML input
  mode from the current admitted host:
  already-open cursor stages therefore use current FETCH/Execute mode for
  runtime character casts, without changing accepted XML datums or their other
  captured presentation. XML warning input follows the detachable authority;
  retained output environments are sink-free.
  `MapXMLRows` forwards the already typed operands and invocation-local
  `FunctionXMLOutput` policy to the live host. SQL, relation and portal authority
  remain in core; retained expressions and XML datums keep no host or settings.
  Dropped-object inspection forwards per opening to the live statement's lexical
  event command; no host or inactive `sql_drop` yields the protocol diagnostic.
  The context retains neither records nor a phase guard between invocations.
  Catalog-backed type inquiries forward `FormatType`/`BaseType` to that live host
  and its coherent catalog/path capture; detached contexts cannot recapture it.
  Logical `pg_typeof` identity stays on its immutable expression, not the frame.
  PL assignment projections borrow a balanced planning-origin flag while
  evaluating their child; SQL calls forward it explicitly, excluding active
  domain VALUE frames. Diagnostic cloning preserves planning-origin markers;
  runtime coercion and CHECK failures do not become planning errors.
  Assignment preflight simplifies SQL constants before importing execution-frame
  coercions; failures in the import phase retain their runtime origin.
  `FoldPlanningExpression` is the single demanded conditional/preflight computation
  and current routine admission boundary, including physical IO coercions and
  strict null-or-evaluate inputs. Actual computation borrows `ConstantPlanning`
  on the evaluation context and restores it on success/error. SQL/PL invocation
  forwards this phase separately from assignment diagnostic provenance, so
  pre-executor constant calls and source-free runtime calls select their genuine
  snapshot owners. `FoldDefinition` owns the live generation and
  immutable typed entries/dependencies; `Begin` borrows them into a
  `FoldAttempt` and changed planning uses COW. The prepared runtime validates
  the complete operation's environment, current authority and cancellation
  before `Accept` seals it. Stale, foreign or retired attempts cannot publish.
  Root-only invalidation preserves exact stored origins for their own lifetime
  validation; all-origin invalidation retires them too. Repeated invalidation
  cannot resurrect retired syntax or needlessly advance the generation.
  `Current` supplies the actual accepted planning input without copying;
  `AcceptedFolds` preserves its generation and immutable backing independently
  of later definition retirement. Its fixed `Candidate` has no definition
  publication rights, so an old portal cannot update a replacement definition.
  Source/expansion and exact stored origin, selected provider/coercion, usages
  and physical literal inputs qualify hits. Literal and result identity uses
  `sqltype.SameDatum`, preserving exact descriptor provenance, FLOAT4 raw words,
  FLOAT8 NaN payloads and signed zero; usage and UnknownNull remain exact.
  Optional nil children retain their ordered slots, distinct from typed NULL
  and invalid literals, without evaluation or dependencies. Selected-operator
  provider nodes contribute usage and recursively visited inputs, not a datum.
  Changed or invalid literal data cannot hit an earlier value. An accepted fixed
  portal generation rejects a miss rather than recomputing. Stable hits borrow
  immutable compound backing without copying. Frame qualification is original
  analysis metadata, never execution frame identity. Strictness and conditional
  demand remain at their existing callers;
  row, parameter, stage, runtime-domain and nonimmutable values never persist.
  Retention does not demand otherwise execution-only arithmetic or ROW
  projections, and raw literals are not new computations. Typed constructor
  ownership witnesses use an existing conditional/preflight demand such as
  COALESCE rather than introducing an eager expression walk.
- **Routine admission and descriptors** (`routine_*.go`): planning-time EXECUTE
  admission (`AdmitRoutineCall`, `AdmitRoutineReference`,
  `FoldPlanningExpression`), the consumed-call footprint `RoutineProgram`,
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
- **Byte pageinspect** (`pageinspect.go`): scalar/record/source invocation
  shares installed identity admission and once-only native written-order
  argument evaluation. A fixed two-value frame projects formal slots; its
  result retains the already evaluated checksum INT8 operand without copying
  a page or evaluating a child twice. The host supplies current superuser and,
  for BRIN items, exact index-descriptor authority before raw validation.
  Immutable BRIN, B-tree, FSM, GIN and raw-header readers decode bytea without
  table-row access. Header/checksum routes in `pageinspect_header.go` produce
  owned PG_LSN/XID composites or INT2; header preserves all-zero records,
  while checksum alone returns NULL for `pd_upper==0`. Their non-set FROM
  routes always return one row, including SQL NULL. Shared planning may
  eliminate strict scalar constant-NULL calls before EXECUTE admission, but
  retains RECORD calls and runtime NULL columns for permission checks.
  GIN routes in `pageinspect_gin.go` build ordinary typed composites and
  arrays; STRICT leaf NULL (no rows), new leaf (one NULL record) and empty
  compressed leaf (no rows) remain distinct. FSM returns one typed NULL TEXT
  row for SQL NULL or new pages. Declared result shapes own cursor metadata;
  deleted B-tree notices use the current statement operation.
- **Heap verification** (`verify_heapam.go`): validate required NULLs and skip
  before target-kind admission; explicit physical heap/visibility-map/TOAST
  requests raise `0A000`. `VerifyHeapamPrefix` retains an unfiltered prefix,
  checks logical roots through storage, and constructs records with NULL
  physical coordinates and stable logical attribute identities. The root host
  owns temporary relation locks, capture and incarnation fences. After exact
  postwait identity and target validation, it records the sticky TEMP-target
  attempt and rejects foreign-session TEMP table/sequence storage before
  capturing rows or accepting a healthy sequence. Installed EXECUTE denial
  precedes target accounting; savepoint rollback does not erase that footprint.
  Virtual catalog views retain view-kind rejection; table-like catalog sources
  without stored heap roots report explicit stored-data unavailability.
- **Seams**: `Host` (`host.go`), `DetachedPresentation`, `Preparation`
  (`preparation.go`) and the admission slots `AdmissionAuthority` and
  `RoutineProgram`.

## The host

`Host` (`host.go`) lists every method with its group. The engine implements it
on `statementHost`, a defined type over `StatementScope`.

| Group | Methods | Lock and lifetime contract |
|---|---|---|
| Environment | `Context`, `Environments`, `StatementStart`, `CTEInvocation` | Read where the runtime reads them; a held cursor detaches the host between fetches. Partial aggregate workers use the outer CTE snapshot captured before launch, including nil, never a sibling support body's lexical scratch. |
| Presentation | the nine `sqltype` formatters | Read the statement's comment/deparse snapshot. With no host, output uses `DetachedPresentation`. |
| Row locks | `AcquireQueryRow`, `LatestQueryRow`, `QueryLocks` | `AcquireQueryRow` may block in the lock manager; it releases the replacement read lock across the wait, then takes `Engine.Mu`. `LatestQueryRow` takes `Engine.Mu` and records the physical row event after releasing it. |
| Foreign owner | `ReleaseReplacementRead`, `TouchForeignScan`, `EnlistForeign`, `AsyncAppendEnabled` | Release the replacement read lock before external I/O; enlist the statement's foreign transaction participant. Async eligibility reads the immutable admission snapshot once. |
| Admission | `RoutineAdmission` | Reads statement-owned routine authority and dependency manifest without a lock; fails once the statement has finished. |
| Sequences | `CompletePGSequence` | Acquires the sequence lock, then reads the generator under `Engine.Mu`. |
| Capabilities | the function, expression and plan runtime bodies | Take `Engine.Mu`, read session state or have side effects (advisory locks, XID assignment, sequence calls, notices). |

Every method is called without `Engine.Mu` held, and none returns an engine
struct. Records store the host once, never per row. A record with no statement
scope holds an untyped nil host (`LiveHost` never returns a typed nil). Optional
host capabilities such as `PgcryptoNotice` are resolved through `LiveHost` per
delivery; the engine appends owned notice records and never calls user handlers
under engine locks.

The typed `ReplicationOrigin` capability resolves through `LiveHost` for each
surviving invocation, after common routine admission and strict operand handling.
Its bridge carries only kind, two typed values and the result descriptor; neither
prepared recipes nor contexts retain a second origin store or captured actor.


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
folding (`conditional_*.go`), literal/bound strict-NULL folding, array and UPDATE
value preflight, string variadic folding, view qualification mapping and the
OVERLAPS route. Preflight reaches the engine only through `Preparation` and runs
outside engine and publication locks. Conditional planning keeps every analyzed
dependency and authorization even when it removes a branch; held parameters are
reconsidered on every execution, never stored as planning facts.
Network and `pg_lsn` arithmetic explicitly opt into immutable available-frame
planning through `OperatorExecutionConstant`. This may expose an immutable child
error before its volatile parent's runtime EXECUTE admission. Missing parameters,
row/stage/domain dependence and volatile children never qualify; other arithmetic
keeps its existing frontier. Imported values are reconsidered, not retained.
An `expr.ExecutionDemand` provider is an execution barrier for every enclosing
computational frontier, including coercions and array/composite preflight. The
bound-tree predicate preserves demanded diagnostics without relabelling semantic
volatility or disabling ordinary typed-parameter computations. `FoldNever` alone
is node-local and does not block enclosing preflight or relational input validation.
Only a private PL query-preflight context opts into failure provenance: actual
`Eval` failures retain whether that computation consumed imported parameters or
SQL constants, and the current computation shadows a nested callee's origin.
The original context is not marked or retained; prepared domain checks propagate
back through the existing copyback seam. Routine-admission failures occur before
this boundary and remain unmarked. Ordinary preflight returns the original provider
error unchanged, including its direct SQL error interface. Opted-in failure wrappers
preserve the original cause for diagnostic decoration.

`StrictRoutineArguments` maps expression children to actual nullable call
formals for both permission admission and literal/bound strict-NULL folding. Expanded
SQL/PL variadic tails and generated multirange constructor members form one
non-NULL array: a NULL member must not erase the call. Only non-expanded
SQL/PL input slots participate in strict-NULL proofs; the runtime still assembles
the complete frame and checks live caller authority before testing its formals.
An explicit NULL VARIADIC array retains the strict NULL result. Multirange NULL
members instead reach the constructor's `22004` check.

After recursive child planning and typed-input validation, a strict scalar call
with a literal NULL or a proven NULL in the current typed parameter frame becomes
a result-typed NULL even when its declaration is STABLE or VOLATILE. Commands
capture `force_generic_plan` as a parameter-demand barrier: imported parameter
NULL cannot erase the call before current EXECUTE admission, while literal NULL
and custom-plan pruning retain their existing behavior. This does not add a
generic/custom plan cache or change SQL/PL/host invocation owners. Immutable
child errors and their current-actor EXECUTE checks still precede erasure; a
runtime-only sibling, including a domain coercion, is discarded without execution.
Within this already-proven NULL frontier, initialized custom-plan parameters
also supply demanded immutable arithmetic children through the existing planning
admission evaluator. Only typed arithmetic and its selected operator/coercion
wrappers enter; arbitrary functions, row/stage/domain/volatile providers and
uninitialized frames do not. The temporary value is discarded after checking
errors, without parameter substitution, a global arithmetic-folding change or
mutation of the analyzed recipe. Generic parameter-demand barriers remain intact.
A domain conversion in the NULL-supplying argument is not a proof. Runtime row
NULL is not a planning fact and retains sibling admission and effects.
Declared RECORD results, SRFs and compound NULL-aware carriers are excluded.
Shared constant planning also retains every set-source node, including literal
operands, for target lifting rather than calling scalar `Eval`. Installed set
classification includes authenticated hstore declarations, so strict literal
NULL does not erase their EXECUTE admission.
Bounded Random's specialized readiness/immutable-input rule remains first.
The generic folded value preserves conservative metadata and recipe provenance
but clears the callable carrier; the original graph and manifest remain intact.
Generic coercion preflight completes ordinary planning of every physical child
before erasing a proven strict-NULL parent; ArrayRandom also enters the existing
conditional simplifier without making nonNULL volatile calls foldable.
Query projection preflight replays a successfully changed executable expression
onto its occurrence-correspondent analysis carrier using private copy-on-change.
It does not compute unmatched unused analysis payloads. Surviving analysis demand
still requires admission, while original carriers remain for lifetime validation.
Closed literal-NULL array random defaults retain their original nextval leaves
for stored rendering and lifetime validation, not as executable demand. This
does not admit nonNULL volatile array defaults or fold nonNULL volatile calls.

## Invariants

- **No mutexes.** The package adds no lock. Lock order, modes and waits belong to
  the engine host bodies and the `lock` manager; the lock manager's mutex is
  never taken under `Engine.Mu`.
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
- **Filter candidates.** Two WHERE shapes visit only candidate rows, and the
  produce loop still evaluates the whole predicate on each: a top-level equality
  over a captured hash index, and a WHERE (also inside derived subqueries) that is
  one oid equality between a column of a shared catalog capture
  (`Source.CatalogKeys`) and a literal or parameter under non-volatile casts.
  `CatalogKeyIndex` builds a column on first use and publishes it atomically. A
  column qualifies only when every cell has the column's exact oid descriptor, so
  no skipped row could have raised an error; any failing probe keeps the scan.
- **Join keys** (`query_join_hash.go`). When a join's whole predicate is a
  conjunction of builtin same-type equalities between a left and a right key (a
  plain column, maybe cast to oid from another OID type), the first left row
  still scans every right row; then the right rows are indexed by key hash, and
  each later left row visits only its matching-hash right rows in ascending
  order and still evaluates the whole predicate. NULL keys match nothing; a key
  that fails falls back to the full scan, so output order and errors are those
  of the nested loop.
- **InitPlans.** The binder marks a scalar or EXISTS subquery `InitPlan` when it
  reads no outer row, calls nothing volatile and reads no CTE. Its execution
  owner (`membershipOwner`, which also owns hashed `IN` memberships) evaluates it
  on first demand and keeps the value in `State.InitPlans` until
  `ReleaseMemberships`; a failed evaluation is not kept.

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
`Engine.Mu`; cursor or portal publication instead transfers the programs to the
producer, which releases them on every exit before clearing authority. Borrowed
sources never close them.

## Transition aggregates

`TransitionAggregate` keeps an immutable selected ordinary or moving plan with
independent state/result usages, seed and final mode. Each occurrence owns its
state and forward/inverse/final `proc.Frame`s; immutable values need no speculative
copying. Strict transitions distinguish never-seeded state from returned NULL.
Frame-backed SQL/PL/host supports have no scalar expression call: their common
`RoutineDefinition()` selects `InvokeAggregateRoutine` on the actual statement
host. Builtin expression and separately owned aggregate-context paths remain
distinct; the common layout does not manufacture either capability.
All vectors use retained binary conversions, never current casts; strict
transitions without a seed recheck compatibility once at startup. Admission
checks current aggregate-owner EXECUTE on exactly the selected supports, with
final support before forward and inverse transition support, matching
per-aggregate/per-transition startup order even when empty input or strict NULL
skipping prevents invocation.
Admission matches purpose as well as original reference/revision: written calls
use Definition captures, selected supports use AggregateSupport captures.
Consumed-call recording, readmission and phase initialization derive that lane
from the existing support-aggregate association; neither lane borrows the other
lane's historical stamp at a shared address.

`WindowAggregates` retains full owned argument vectors and independent
per-occurrence head/tail cursors. Overlapping moving frames remove contributors
through the actual inverse before adding new members. NULL inverse requests
fresh initialization and real forward replay for that occurrence alone. Last
contributor removal resets the execution-environment seed without inverse.
Moving forward NULL is an error. Exclusion/disjoint frames replay; unchanged
frames may reuse the final. Builtin SUM keeps its real forward replay path.
Replay remains row-major and buffers are reused, not allocated per member.
Window startup rejects selected SHAREABLE/READ_WRITE modes before opening input.
The rejection formats the selected declaration through the current statement's
regprocedure presenter, preserving visibility-aware qualification, identifier
quoting and argument type spelling rather than reconstructing written syntax.

`FinalizeWithContext` borrows the current statement runtime and evaluated direct
vector, invoking the selected final once per group without replacing its state.
Direct expressions evaluate left-to-right in the final group-key frame, even
without a final or before strict suppression. Finals receive state, direct
values and declared typed NULL row extras, never last-row values or outer params.
SQL supports share the statement's staged writes and cancellation boundary.
User ordered/hypothetical SFUNC streams admitted rows in child order; support
routines own sorting. Ordinary ordered replay and builtin ordered providers
retain their existing executor-owned sort contract. Selected native ordered
support routines use immutable recipes and private per-group opaque state,
never SQL INTERNAL datums; only their final requests the tuple comparator/sorter.
C loading, fmgr pointers, memory-context ABI and physical spill remain external.

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

Runtime observation units use
`RUN='^(TestExplainAnalyzeInstrumentation|TestExplainAnalyzeProjectionTimingOwnership|TestExplainAnalyzeForeignPlanIdentity)$'`
over real stage demand, repeated invocations, joined worker-local records,
controlled active projection evaluators and actual detached foreign-plan identity.
Core's `TestExplainAnalyzeParallelAggregateOwnership` exercises real partial,
combine/final and copied-wrapper owners; run these selected owners with `RACE=1`.

Effective work-policy context and retained evaluator ownership units use
`RUN='^TestWorkAdmission'`; public SQL credit waits remain in reference packages.

Execution-only XML provider composition through enclosing planning frontiers uses
`RUN='^TestXMLPlanningExecutionBarrier$'`.

Type inquiry datum identity, ordinary cancellation and detached-authority units
use `RUN='^TestTypeDescribeExecutionOwnership$'` in `enginetests`.

Crosstab pivot, directional type compatibility and source cleanup units share
`RUN='^TestTablefuncCrosstab'`; the cursor owner asserts error precedence and
deterministic cancellation as well as detached immutable results.

Package units cover the preparation seam, stored-view producers, source
ownership, cursor motion (`RUN='^TestCursor'`), transition aggregates,
ordered-aggregate sort cancellation, the pgcrypto notice host, `pgrowlocks`
TIDs and the query CTE, outer-join, row-mark, set-operation and subquery runtime
units (`RUN='^TestQuery'`). Executor, row-lock, cursor, foreign-scan, preflight
and routine-admission units build an `Engine` and live in `internal/engine` or,
for query, DML, row-lock and foreign parallel scan units, in
`internal/engine/core` (grep for
`exec.OpenNode`); run row-lock, cursor,
foreign-scan, CTE and detach selections with `RACE=1`. `InputDomainChecks`
changes must keep the COPY filter and JSONB record reference tests passing.

Semantic routine descriptor identity and immutable retained configuration use
`RUN='^TestRoutineAttribute'`, also with `RACE=1`.

Foreign parallel dispatch uses `testing/synctest` (`RUN='^TestFDWParallel'`):
settled callback counts, never elapsed time, establish backpressure.
Async protocol, cleanup, filtered-frontier and cursor-boundary units use
`RUN='^TestFDWAsync'`; public authentic-transport scenarios live in
`reference/tables` (`RUN='^TestReferenceFDWAsync'`).
Typed foreign query contract, layout, request-copy and resource units use
`RUN='^TestFDWQuery'`, including composed prepared-owner coverage in core.
Query source identity units use `RUN='^TestQueryTableOID'`; virtual recipe
consumers additionally use `RUN='^TestGeneratedVirtual'`. Catalog key index units
here and candidate admission units in `internal/engine/core` use
`RUN='^TestCatalogKey'` (with `RACE=1`); their public outcomes are
`reference/catalogs` `RUN='^TestReferencePG(CatalogKeyProbes|TypeProcRepeatedProbes)$'`.

Strict scalar demand and original ownership use these selected definitions;
add `RACE=1` for the concurrent-reuse proof. Public whole definitions require
both native and `REFERENCE=1` runs against the pinned PostgreSQL 18.6 fixture.

```sh
make check RUN='^TestArrayRandomStrictNull(Planning|OriginalAuthority|ConcurrentReuse|StoredDefaultClosure)$'
make check RUN='^TestArrayRandomStrictNullConcurrentReuse$' RACE=1
make check RUN='^TestStrictParameterNull(Planning|Boundaries|CompoundSemantics)$'
make check RUN='^TestReferenceObjectGrantStrictNull(Demand|Precedence|Imports|Held|Stored)$'
make check RUN='^TestReferenceObjectGrantStrictNull(Demand|Precedence|Imports|Held|Stored)$' REFERENCE=1
```


**Frame names matched by tests.** `sortOrderedAggregate`
(`TestReferenceOrderedSetAggregateActiveCancellation`) stays unexported, and the
recursive union stage keeps the unexported defined type `recursiveUnionStage`
(aliased as `RecursiveUnionStage`; `TestReferenceQueryCycleLifecycle`). Both
fixtures wait for these stack frames by name; renaming either makes the test's
unbounded query run until it exhausts memory.

```sh
make check RUN='^Test(MissingPreparation|InstallPreparation|ViewProducer|SetArmSource|CorrelatedJoinSource|QueryBranchPruning|SortOrderedAggregate|PgcryptoNotice|PgrowlocksLogicalTID)'
make check RUN='^Test(CreateAggregate|AggregateFinal|AggregateMoving|AggregateOrdered)'
make check RUN='^Test(PLpgSQLQueryLoopLifetime|FTSQueryFunctionsBatch)'  # row owners
make check RUN='^TestReferenceQuery'   # public query execution; add REFERENCE=1 for the oracle
```
