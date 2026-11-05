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

Installed file_fdw scalar evaluation authenticates the original selected
declaration and current routine lifetime, evaluates its typed children and enters
the ordinary routine boundary. STRICT NULL returns typed NULL; the validator
borrows the statement's bounded current-role runtime and returns concrete VOID.
The handler's method table remains an authenticated factory result, never a
printable scalar or an alternate source of ambient file/program privileges.

`CatalogSourceFalse` is a literal-only bound-plan proof for a single unsampled
source directly under a first-conjunct BOOL FALSE filter. It inspects occurrence,
reference and immutable literal authority without evaluating an expression.
Ordinary aggregates above that input qualify only without ordered-set direct
arguments: keys, ordinary arguments, FILTER and ordering consume input rows, not
the aggregate's synthesized empty-input result. Subqueries, identity reads,
unsupported shapes, joins and invocation/CTE boundaries yield no proof; a parameter
cannot establish FALSE. Core uses it only to suppress bgwriter cumulative
evaluation, not source opening, getter ACL admission or normal filtering;
`CatalogSourceKeep` retains its independent producer row-test contract.

Table sampling opens the ordinary visible source, including row security, before
WHERE. SYSTEM_ROWS evaluates its assignment-bigint count once per demanded
invocation and retains private reservoirs of visible occurrence indices.
Inherited and partitioned inputs receive a separate cap for each physical
source; ONLY retains just the named physical data, including an empty
partitioned parent. Zero yields an empty selection; finite visible populations
bound allocation even for max-bigint requests, and all-row samples borrow the
existing stage mapping and physical-layout identities.
Candidate, branch/catalog prefilter, direct-cursor and parallel-split shortcuts
cannot cross this sampling barrier. Constant WHERE rejection may avoid startup;
an ordinarily demanded empty source still checks NULL and invalid arguments.
Unchanged rescans reuse selection; changed correlated inputs reopen/rebuild it.
Close/error/cancellation release the child, selection and evaluator through the
existing statement pin owner without changing accepted plans or returned rows.
Physical PostgreSQL heap blocks and their clustering distribution are external
storage mechanisms; no block layout or portable selected-row order is invented.

SYSTEM_TIME evaluates its assignment-float8 millisecond argument once per
demanded invocation. Zero (including negative zero) is empty; positive infinity
and max-finite budgets are accepted without duration conversion/overflow.
Selection traverses actual NextOccurrence demand, retaining original source and
cache identities rather than draining first or deriving a row count from time.
Each physical inheritance/partition source receives its own monotonic budget.
Private context-carried budgets reach nested demand and RLS rejection loops.
The common expression prologue, cached/raw stage demand and drain, and nested
aggregate, sort, DISTINCT, window, membership, set and recursive executors share
cooperative checkpoints. These cover repeated fields/arguments/support calls,
NULL-sensitive cached membership candidates, set-output multiplicities,
SEARCH/CYCLE keys and paths, hash-join keys, WITH TIES comparisons, SRF cursors
and records, JSON-array providers, and nested SYSTEM_ROWS reservoir work.
Unlocked partition proofs use the same evaluation seam during normalization,
support calls, containment and physical-branch traversal, not a fresh clock.
A successful expression/support callback is checked before the next unit of
work; its genuine returned error stays primary. Host cancellation is checked
before budgets, and expired ancestors propagate unchanged to their owner.
Only that owner converts its private budget pointer into normal EOF, preserving
accepted prefixes and source/cache backing until Close. Borrowed subquery
unwind closes local stages/scratch, not the capture owner's reached producers.
CTE and producing-source budget unwind is nonterminal: the owner retains the
original unpublished occurrence and its validated whole-field prefix in a
reserved, hidden Flat tail. Live uncaptured demand rebinds the row/context/order
frame and evaluates only the remaining fields, saving successful values before
post-success checkpoints. Complete tuples and lineage publish together; captured
prefix replay does not eagerly finish pending work. This is whole-field
continuation, not arbitrary incomplete-expression subexpression replay safety.
Unchanged rewind and borrowed reader close retain that frontier. Changed rewind
(including a new volatile invocation), genuine failure/cancellation, actual
owner close and detach clear abandoned value backing. Ordinary derived/VALUES
partial projections remain unpublished. Window scratch disposal does not traverse
borrowed input edges. Genuine cleanup failure remains an error when no genuine
primary already exists. Invocation evaluation contexts are cleared on every
exit. No timer goroutine or mutable clock is retained in accepted plans.

The scan clock begins after source/snapshot preparation and excludes lock waits,
eager MVCC visibility-prefix construction, outer final sort/projection/aggregation
after sampling and client consumption. Nested SQL demanded while traversing the
sampled source (including an RLS scalar aggregate or window query) remains timed
scan work. Excluded phases still obey ordinary cancellation/statement timeout.
Cooperative checkpoints do not hard-preempt an indivisible policy/codec/provider
callback and do not impose a whole-query millisecond deadline.

The pg_stat_statements bridges authenticate installed typed carriers and borrow
the live statement runtime for owned reader rows or a demanded scalar reset.
They own no parallel accumulator, cached privileges or terminal success credit.
Scalar strict NULL keeps ordinary one-row cardinality. Planning metrics describe
every real accepted native preflight construction; retained fold backing is not a complete generic
plan, and no cost-based generic/custom chooser or definition graph cache is
introduced. Continuations of an already accepted portal do not rebuild a plan.

Database monitoring uses fixed-size execution-owned source counters, independent
of optional EXPLAIN observations. Only physical source demand counts: sequential
fetches, equal live index occurrences and their demanded heap fetches. Filters,
projections and aggregates do not add result-row counts; partial drains consume
only the undemanded suffix, retained materialization replay does not reread heap,
and genuine physical rewind restarts its frontier. Cursor FETCH flushes the
newly executed source facts before detaching its current host. Counts-off
per-row paths allocate nothing and sample no clock. Read timing, when enabled,
brackets actual local cell access rather than SQL-result construction.

Local projection and partial-aggregate workers own private source facts. The
coordinator joins and folds each once on success or error/cancel, never sharing a
session Writer with workers. Requested workers and actual starts are recorded at
the admitted launch loops. Foreign parallel startup records requested maximum
separately from successfully started partition goroutines; fewer partitions or a
startup failure do not fabricate launches. Its join helper is not a worker.
Serial/ineligible/nested execution and plain EXPLAIN contribute no workers;
EXPLAIN ANALYZE is real execution. Actual native workers need not match a
PostgreSQL Gather planner choice. `make check RUN='^TestStatDatabaseTupleFrontiers$'`
includes the disabled allocation control.


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
Mutation producers additionally own a fixed set of optional DML-role records,
not fabricated OpenNode identities. Literal/default input counts successful
source production before admission; MERGE records actual target passes/fetches
and emitted membership pairs before action selection. Known unentered roles
remain rows0/loops0; entered empty passes retain their real loops. Timing uses
the same active-span machinery and no row-loop map, lock or allocation.
UPDATE/DELETE roles separately measure original target fetches, reached joined
candidate frames, initial predicate survivors and actual cursor-position lookup.
EPQ retries and rejected/duplicate actions do not emit another initial candidate;
an empty FROM still permits UPDATE's real target scan, whereas empty USING can
leave DELETE's target genuinely unentered.
Each accepted mutation command owns separate roles and one command-root scalar,
shared by explicit and automatic rendering. Modifying CTE associations use the
exact invocation owner plus definition: lazy reservations remain unentered
until actual demand, sibling commands never accumulate together, and closing
the CTE owner drops its associations. Provider-presentation-only and nil paths
create no DML records. Real query source nodes keep their ordinary identities.
Final modifying-CTE presentation resolves the exact invocation/definition through
the already captured renderer family, so real query source identities survive
preflight copies and lazy startup rather than inheriting stale static nodes.

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
External-bound sources retain only their authentic installation token through
planning. Enlistment injects an operation-bound opener into actual execution
callbacks with a private matching context; planning/Describe receive nil.
Captured row-mark/rescan request snapshots discard the opener, retaining no
completed operation through a live cursor. Owned external connections and existing
transaction participants have their separate physical-session lifetime.
Modification planning checks observation presence before making its optional
defensive callback request; ordinary unobserved execution makes no unused copy.

Automatic explanation uses the same optional observation. A presentation-only
observation captures actual provider choice without stage counters. Requested
Logical Tuple Accesses count genuine base-source demand frontiers, including
indexed candidate fetches and real rescans, not projected fields, materialized
replay or backing capacity. Logical Tuple Writes count accepted staged/provider
operations, not commit outcomes or WAL. Trigger calls/timing use actual trigger
identity. Stage-private access counters are read only after workers join;
serial command staging and trigger invocation own their separate scalars.
No observer means no metric allocation, per-row locks or clock sampling.


## Imports

Layer L9.5 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture). It imports
`plan`, `expr`, `functions`, `catalog`, `storage`, `visibility`, `lock`,
`access`, `ids`, `proc`, `authorization`, `parser`, `sqltype`, `sqlstate`,
`pgcatalog` (projected relation-kind admission), `util` and the standard library.
Its `extension` imports validate genuine installed cube-spatial,
earth-point, autoinc and insert_username scalar calls before kernel evaluation.
Insert_username authenticates current provider identity and reference domain/epoch
before reporting scalar misuse; structural native-trigger capability grants no
execution authority. Its `routine` import authenticates original fmgr identities,
not SQL names or installed capabilities. It never imports the engine,
`analyze` or `session`.

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
`query.aggregate` precedes aggregate input materialization; the distinct
`query.aggregate.sort` point retains a successful real ordered-input tuple
comparison before its ordering result returns to the merge. It covers builtin
ordered-set/hypothetical finalization and ordinary ordered-input replay, not
aggregate initialization or child sorting. Empty/singleton and terminal inputs
need not compare or admit; typed comparison errors precede this checkpoint.


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
The hash path drains ordered categories before source SQL and keeps category
ordinals under opaque first-62-byte output-string keys. It groups adjacent full
NULL-safe row-name strings, captures first-row extras and overwrites matched
cells with the last value, including NULL. Only final selected strings enter
destination input/typmod/domain codecs at group emission; ignored values,
overwritten strings and later extras are not input-converted. Empty source
results bypass conditional category/source/result-width checks. Both consumers
close their cursor on every exit and publish no partial map or tuple collection.

XPath table source opening authenticates its current installed carrier before
argument evaluation and delegates five TEXT fragments plus the completed record
usage to the statement contrib runtime. The runtime fully materializes nested
SQL before calling the sqltype-owned legacy XML/text-I/O bridge, then returns
only immutable owned tuples. Exec imports no private XML parser and retains no
SPI authority, XML tree or conversion callback on the prepared node or cursor.



## What it owns

- **Runtime records** (`query.go`, `query_sources.go`): `Source` (a captured
  relation or composite source), `State` (one query invocation), `Stage` (an
  indexed producer stage), `FinalRows`, `Authority` (the borrowed statement
  authority a held cursor keeps) and `LockContext`. `SelectQuerySource` takes
  resolved `visibility.HeapOperations`; the row loop calls them, never a catalog
  lookup, callback or alternate visibility algorithm.
  `Source.ReadLease` is captured eligibility, not a preparation-time read.
  An ordinary SourceNode opening activates one invocation. Relation-source
  initialization alone does not: TABLESAMPLE initializes through the same
  identity/security/dispatch path, but activates only when nonzero valid sampling
  consumes its child (including SYSTEM_TIME positive infinity). Zero and rejected
  arguments never open a read lease, and an empty source has no representative.
  These are native logical read-attribution counters, not PostgreSQL physical
  buffer-count parity. Hierarchy stages activate only reached physical
  segments, and index consumers activate their selected index. Stage-local read
  and segment frontiers survive rewind but not fresh invocation.
  Capture-owned pins survive successful borrowed invocations. Owner Finish,
  error/cancel and Source.Close release them idempotently, before source graph
  detachment; close never reacquires engine authority.
  `Authority.PreserveParameterDemand` is immutable command-invocation metadata,
  captured from the preparation evaluator before executor source publication.
  `State.Evaluation` restores it when constructing fresh statement frames;
  without a source authority, nested frames inherit their lexical outer policy.
  Derived/frozen sources share that same slot, and held-source Host detachment
  or reattachment does not resample settings or change the captured policy.
  `Authority.RoutineCatalog` transfers the same actual initializer's immutable
  read beside its inert owner. Singleton, anonymous and foreign frames without
  a physical View use this explicit snapshot, never a current Scope lookup.
  Source opening rejects missing/foreign owner-read transfers and incompatible
  View/lexical domain or epoch; row pulls never mint a replacement owner.
  Query identity demand retains source-occurrence lineage for virtual recipes
  and typed system attributes alike, independently of rowmark demand. An unmarked
  identity-only capture still supplies physical prefixes to materialized adapters.
  Expression identity demand propagates explicitly through stored/derived query
  states and their joins/SRF expansion even when inner expressions do not read
  identity. Locator-only cursor manifests do not demand ineligible join caches;
  effective-version and recheck caches require actual rowmarks. Each expanded
  SRF row retains the child's resolved occurrence vector and selected provenance
  when demanded; materialized cells alone are not physical authority.
  A system leaf follows its lexical frame and actual physical source; ctid uses
  the captured VersionID, and xmin uses the immutable effective header. Demanded
  xmax uses detached manager raw facts in an immutable source/frame overlay,
  including lock-only, inherited MultiXact and ended/aborted raw headers.
  Expression evaluation never contacts the manager or modifies RowVersion's
  full-XID fields. EPQ finalizes a new presentation publication after genuine
  physical events; prior frames retain their original map/scalars. Mutation and
  OLD/NEW frames capture actual accepted headers after event flushing. Outer
  absence is typed NULL, independent of tuple width. ctid/xmin/tableoid-only
  consumers do not allocate raw capture batches. Generated-owner tableoid recipes
  retain their separate zero-coordinate mode; neither path fabricates a stored
  datum or row counter.
  `ProjectedQueryVersions` borrows only the accepted physical frontier, never a
  RowID-retargeted head. Materialized MERGE retains demanded immutable version
  slices/raw publications alongside its already-owned source provenance; closing
  the cursor does not turn later action/RETURNING into a fresh source scan.
  Dependent function sources reuse the same lineage on each invocation. An
  explicit identity-prefix marker carries zero-column ancestors without inventing
  a tuple slot, and nested joins merge it with their local-left source identities.
  Mixed hierarchy capture expands the existing lock manifest into actual
  physical prefixes, with no aggregate grant target. Its lazy indexed adapter
  copies parent-layout cells alongside the emitting child's actual visible
  index, absent sibling provenance and physical versions. Alternative members
  resolve one logical operand for recheck, virtual recipes, tableoid and cursor
  identity; mapped rechecks own their frame and apply logical RLS once.
  Rewind clears aggregate output cells, references, provenance and version caches
  together, retaining the immutable manifest and visibility pins. Foreign
  anonymous rows, captured identities and immutable request generations remain
  append-only under their existing scan owner; closing a borrowed stage is not
  transaction end.
- **Openers and producers** (`query*.go`, `window*_query.go`,
  `aggregate_query.go`): `OpenNode` dispatches every plan node; `Finalize` and
  `FinalizeSelect` turn a root into a `RowResult`. CTEs, set operations,
  recursive unions, windows, aggregates, DISTINCT, LIMIT/WITH TIES and subqueries
  live here.
  `NamedRelationScanNode` opens through `Source.LiveHost().OpenNamedRelation`,
  which validates the retained shape against the current invocation. The live
  authority slot is authoritative, including its detached nil host; no direct
  host fallback restores invocation access. Each open owns its Source and cursor,
  borrowing immutable accepted backing without copying the rowset; empty scans
  retain their typed schema. These leaves have no physical lineage, row locks or
  captured catalog authority.
  Demanded named whole-row presence participates in deferred correlated topology
  before an enclosing join borrows its packed layout. Null extension is distinct
  from a present all-null tuple. Physical-occurrence traversal treats the named
  scan as a leaf with no storage occurrence.
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
  arithmetic and labels these paths Hash Candidates, Bloom Candidates or Full
  Scan, not physical disk scans. Bloom admits only typed same-source equality
  conjuncts against retained current roots; unsupported query predicates fall
  back without excluding rows. Signature matches map exact row/version/position
  identities to captured visible occurrences, with no pre-recheck key comparison.
  The shared FilterNode producer always evaluates its original predicate.
  Candidate state retains the exact qualified index identity and one captured
  owner-local statistics capability. A genuine non-NULL hash search charges
  searches, equality-accepted returned entries and each first reached live heap
  candidate before residual filtering. Internal hash collisions do not count as
  returned entries. Bulk bloom signature traversal owns searches/lossy returned
  candidates but deliberately no per-index heap fetches: logical bitmap-style
  ownership, not a physical bitmap access method. Actual errors preserve already
  reached work. NULL/unconstrained shortcuts, unused root capture and retained
  replay are not new searches or entries; retained output is not another fetch.
  Existing implicit/current-root eligibility remains unchanged, so explicit,
  private and historical fallbacks produce no native index events. B-tree
  presence and covering projections imply no new access method or index-only
  accounting.
  `RelationScan` owns one actual physical sequential invocation across Stage
  copies, projections and security wrappers. Demand starts even an empty heap;
  live fetched occurrences are charged before predicates/RLS, with a shared
  high-water frontier preventing retained replay or repeated ensure from
  refetching. Contiguous Drain charges only its newly reached span, including
  worker-local range boundaries. LIMIT/EXISTS retain their demanded prefix,
  LIMIT0 and unopened sources do not start, and real reopened sources start
  independently. DML candidate loops borrow this same heap authority.
  Physical parallel query/aggregate lanes keep private start/tuple scalars at
  this same demand frontier. Workers never capture a relation capability or
  touch the physical session Writer. The serial leader captures that authority
  before fanout and reduces all lane prefixes after joining, before returning
  any worker error/cancellation or gathering output. Failed projected output
  cannot discard already fetched work; merely launching an unopened lane adds
  nothing. No provider lock, shared pending map or new statistics map is used
  per row, and parallel eligibility/results are unchanged.
  Inheritance/partition branches charge each reached physical relation, not
  an invented parent aggregate. The existing pruner additionally carries selected
  empty segment identities when ordinal intervals cannot represent them; the
  branch cursor reaches them lazily, without counting excluded/unreached leaves.
  This is distinct from ReadLease's nonempty buffer-representative contract.
  In mixed local/foreign hierarchies, each actually opened local child owns this
  same capability, borrowing statement authority from its enclosing capture
  without mutating the shared Source. A reached empty local prefix still starts
  a scan; unopened later children and foreign handlers never acquire local heap
  events. Retained hierarchy output replay is not a new local invocation.
  Actual bloom heap candidates charge the table's bitmap fetch subtotal before
  recheck, once per fetch, while index simple fetches remain zero. Neither
  preparation nor output replay nor public database/sql Next creates read work.
  Sources and stages release retained roots on close, invalidate stale selection
  on reset, and preserve terminal cancellation rather than returning partial
  success. Cursors/portals retain their opening decision; prepared execution
  admits current command methods/costs without mutating shared IR.
  `FinalizeSelect` compares serial work with setup+work/workers+tuple-delivery
  before either local query or partial-aggregate dispatch, after existing
  safety/shape gates and logical table-size admission. Foreign worker costing
  remains provider-owned. Explicit worker fixtures must request cheap costs.
- **Stored-view producers** (`view_producer.go`). A stored-view `Source` owns its
  retained stage and lazy flat prefix; later readers replay the prefix, then
  resume the same pending occurrence without replaying completed whole fields.
  Stable reopen retains the next-unpublished frontier; changed/volatile rewind
  clears stale output and pending backing for a new invocation. Failures stay
  terminal; `Source.Close` disposes the producer once, and reopening a reader
  never recaptures it.
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
  An optional borrowed `Processed` counter observes each successful live
  producer tuple, including skipped ABSOLUTE/RELATIVE and MOVE work. Promoted
  store reads do not report executor work; no callback or per-row allocation is
  introduced by observation.
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
  Replication statistics reset forwards its typed kind and current operand through
  `LiveHost` after ordinary mutable routine admission, including NULL-all calls.
  The bridge retains no engine/provider, privilege decision or reset timestamp.
  Shared/SLRU/backend reset uses the same typed `LiveHost` bridge. Backend
  strict NULL avoids runtime admission; nonstrict shared/SLRU NULL demands it.
  Neither bridge invalidates readers or drains pending session work.
  Session activity forwards through `LiveHost`: FETCH borrows its current client
  source without replacing or depending on the cursor's clock snapshots.
  Neither source nor live session authority enters `CursorExpressionData`.
  Legacy backend-ID calls also forward through `LiveHost.SessionInquiry` after
  EvaluationContext checks and ordinary routine admission. The narrow typed
  capability captures the shared activity cut at Execute, not Prepare, without
  retaining a provider, role mask or current-liveness decision in the recipe.
  For ordinary, noncaptured execution, `FunctionInput` overlays only XML input
  mode from the current admitted host:
  already-open cursor stages therefore use current FETCH/Execute mode for
  runtime character casts, without changing accepted XML datums or their other
  captured presentation. XML warning input follows the detachable authority;
  retained output environments are sink-free.
  `MapXMLRows` forwards the actual expression Context, typed operands and
  invocation-local `FunctionXMLOutput` policy to the live host, so scope mapping
  preserves expression identity independently of view relation-check ownership.
  SQL, namespace, relation and portal authority remain in core; retained
  expressions and XML datums keep no host, context or settings.
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
  Standalone immutable installed crypto calls enter this same fold path after
  child coercion and strict constant-NULL pruning; their original installed
  wrapper supplies authentic callable/input provenance. Parameters, row inputs
  and volatile salt generation remain runtime calls, not cached policy decisions.
  Dormant declaration cooking has no live host, so the shared context-aware
  execution barrier retains installed crypto transitively beneath composites,
  arrays, casts and operators. Type/NULL/dead-branch planning still runs; genuine
  unlocked runtime planning initializes and gates the surviving call. This
  readiness rule never permits direct evaluation without required authority.
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
  Active sepgsql simulation separates retained expression initialization from
  actual invocation. `RoutineInitialization` is an inert engine-minted frame
  identity; exact scope-local callsite receipts seal execute ACL/MAC before
  argument recursion. Body entry consumes that receipt without execute replay,
  then performs eligible current-subject FHET only. Original fmgr identity does
  not exempt retained execute initialization. Defaults, domain CHECK ordinals,
  index predicate/key preparations and trigger WHEN retain their actual owners;
  source headers and State copy the value, never mint on row pulls.
  The first admitted transaction command is numbered zero; its genuine domain,
  transaction and nonzero frame instance still distinguish it from a missing
  owner. Exact command matching and scope-local receipt ownership remain required.
  Genuine live source evaluators seal an association with their explicit
  Authority header and last transported owner/catalog. Retained cursor/CTE
  copies validate that local pair before refreshing the same attachment;
  missing or overwritten local ownership is never repaired. Detached headers
  reject, and closed captures omit this live association entirely. Real View
  and lexical Outer snapshots keep precedence; only no-View/no-Outer sources
  refresh their catalog from the header. Derived default/domain/index/body
  phases sever the caller association when selecting their own owner.
  Native SQL bodies borrow the entered child owner. Existing Body manifests
  retain preorder/repeated occurrences; DeclarationBody uses immutable parsed
  call-part ordinals linked to canonical builtin or installation member identity.
  A builtin may have both a parsed DeclarationBody for presentation and a finite
  executable Body manifest. Its real Body initializer and child lifetime checks
  use that manifest, not the unrelated parsed-part ordinal space. Manifest
  initialization retains the authenticated outer Info; runtime child expressions
  retain its source and their exact child ordinal. Inquiry programs record the
  outer in the caller phase, then record only immediate children in the derived
  body phase. Inactive finite-body initialization checks ordinary ACL/lifetime
  without attempting to acquire simulation initialization receipts.
  Strict-NULL EarthBox's source-selected expanded declaration uses a distinct
  typed child phase under the unchanged caller, authenticated by the original
  outer receipt. It initializes retained CallParts but does not fabricate an
  outer FHET; actual demanded dynamic children keep their own entry boundaries.
  Entered and expanded children cannot share owners or forge parent receipts.
  A private value lineage authenticates immediate canonical CallPart/Body-edge
  children from the admitted original outer source, including retained outer-only
  manifests across supported rename/COW. It never appends invented child
  dependencies. Initialization/entry also proves the exact source ordinal; a
  nested body replaces, rather than inherits, its parent's immediate lineage.
  Default/domain/index/grouping and unrelated recipe phases explicitly clear it.
  Consumed programs retain only an immutable source projection for parent/child
  lifetime and ordinary ACL fences, never the runtime owner, receipt or nonce.
  Declaration initialization also checks genuine ordinary SQL ACL/lifetime when
  simulation is inactive, without minting MAC receipts. The actual freespace
  adapter carries one private transient custom-NULL demand selection: after
  authenticating the original wrapper and canonical scalar member, only that
  scalar CallPart is omitted. Generic parameter NULL retains its initialization;
  the empty series still does not fabricate a scalar invocation.
  This is initialization, not a substitute for actual dynamic helper kernels or
  proof of PostgreSQL optimizer/audit multiplicity. Closed workers receive an
  immutable sealed source proof without a live Host; graphs requiring conditional
  entered-body initialization select the existing serial path.
  The live coordinator also certifies selected sort-comparison support leaves
  and aggregate grouping callbacks before sealing. This does not authorize all
  opclass members or add an execute hook to direct support kernels.
  Gather Merge borrows the same sealed comparator proof after workers join.
  Serial SortNode comparisons likewise borrow `State.Evaluation`'s original
  initializer/catalog through `FunctionInput`, rather than the bare statement
  environment. The allocation-only activity gate examines the existing source
  owners, genuinely active sealed proof and snapshots without selecting or
  recapturing authority; only the original evaluator chooses
  Outer/header/View precedence and validates it. Losing local owner and catalog
  cannot turn a real active sealed proof into the inactive lane. An all-zero
  inactive capture grants no simulation activity. Inactive sorts retain raw
  input without an escaping callback evaluator.
  Only actual non-NULL comparisons enter support: empty/all-NULL sort keys do
  not fabricate FHET or SQL EXECUTE checks.
  Active native aggregate leaders and workers use detached environments and
  captured admission; selected SQL/PL/host support requiring fresh runtime child
  initialization uses the existing serial owner. Ordinary native aggregates and
  arithmetic retain fanout. The inactive SQL-support lane keeps its historical
  lexical host gate; it is not an active-simulation fallback.
  Actual native aggregate support calls install a private value-only association
  on invoke's local Context, bound to that exact evaluator, existing initializer,
  selected read, aggregate/helper and root reference/recipe/occurrence/source.
  The selected postargument kernel consumes and zeros it before nested work.
  A copied evaluator, substituted entry or discarded/unconsumed association
  cannot adopt ordinary scalar authority; terminal success/error/panic clears
  both the association and its invocation-local completion bit. Active live calls
  use `Host.ValidateRoutineInitializationOwner`; active closed calls use the exact
  sealed owner/read guard without demanding or minting an ordinary support receipt.
  Both reuse the existing AggregateOwner membership and AggregateSupportDependency
  lifetime branch and genuine execute=false native support entry.
  If that support owns a native SQL Body (for example round FINAL or lpad SFUNC),
  only its immediate nativeBodyLineage borrows the SAME original owner and
  selected parent source. Each child revalidates that owner, aggregate membership
  and original support recipe before its existing finite-ordinal/CallPart and
  DefinitionDependency initialization. No parent receipt, new owner or source map
  is created. A child wrapper derives from its own real receipt, never aggregate
  root authority. These dynamic-body cases retain the existing serial gate;
  int4pl native sum/combine and arithmetic keep genuine closed worker fanout.
  Recipe/default/domain/index/query phases and closed capture sever this source.
  Existing copied Query relation access and worker scan counters are unchanged.
  Inactive genuine SQL Body/DeclarationBody support uses this same exact immediate
  association and AggregateSupportDependency parent, with its original zero MAC
  owner. Membership/read/recipe lifetime checks still apply, but no MAC owner
  guard, initializer, receipt or map is allocated. Original no-body native calls
  and standalone custom fixtures without SQL catalog authority keep their old lane.
  This does not relax closed-body rejection or broaden parallel eligibility.
  Explicit no-SQL-principal index publication uses a guarded exact-catalog-root
  context origin for pure support kernels. A missing live Host alone never
  establishes this origin; client, closed, query and initialized contexts reject.
  Intarray support-I/O, selected set, slice and sort strict NULL calls retain lifetime-only
  admission through their owning checks, stored-reference or direct-source lane. Missing/foreign
  manifests and stale direct revisions remain invalid even when invocation is
  elided; genuine NULL does not acquire EXECUTE or a consumed-call record.
  This bounded rule leaves unrelated strict-provider admission unchanged.
  Non-elided authenticated ISN C calls initialize the physical module during
  execution admission before row demand, not during Prepare/description.
  Operation-local input evaluators forward `PrepareIsnInput` unchanged through
  domain/soft-input wrappers, authenticating the exact installed codec and
  borrowing the current owner; initialization errors are not soft input data.
  Runtime text casts overlay live `IsnWeak`, while closed evaluation retains
  its detached input policy.
  The released pg_trgm provider also initializes its physical library at this
  actual fold/residual admission boundary, after current EXECUTE and declaration
  checks but before operands. A strict literal-NULL call erased by planning does
  not initialize; surviving dynamic-NULL and zero-row expressions do. The narrow
  `InitializePgTrgm`/`PgTrgmThreshold`/`PgTrgmSetLimit` live-host bridge retains no
  settings image or actor. Reads consume typed live thresholds, never parsed SHOW
  text or folded BOOLs. The setter formats its demanded REAL with the effective
  owner's live presentation, then uses ordinary session SET and its journal;
  its result is the installed typed threshold rather than an input echo.
  `CheckProgram` owns ordinary CHECK cooked expressions plus command/actor/RI
  phases, each holding a stable `RoutineProgram` pointer. The live statement
  retains that same pointer through lazy truth and existing wait/publication
  readmission; catalog trees retain neither executable state nor authority.
  A new actor phase admits original consumed calls before borrowing cooked
  expressions, without replaying truth or retaining permission results.
  Earth body failures use `EarthCoordinateDiagnosticNames` in
  `context_admission.go`: descriptor extraction and stored-declaration validation
  select the current query name and exact same-installation conversion-helper
  role. Hard calls captured before rename and remapped Restore calls never cache
  diagnostic labels; child/coercion failures do not enter this error-only seam.
  Strict NULL planning retains authenticated expanded box's independent
  conversion demand, including helper-only presentation. `BoxExecutionRoute`
  combines provenance with the existing command `PreserveParameterDemand`
  capture, not an error label or a retained statement's previous mode. Custom
  parameter-first calls keep ordinary strict NULL; explicit generic execution
  demands the conversion with helper-only diagnostics from the first call.
  Neither this path nor its tests introduce an automatic cost-based chooser.
  `earth_coordinate.go` owns the finite actual declaration kernels: nested
  conversions derive an entered child Context and execute their own selected
  `earth()` occurrences; cube constructors, coordinates, distance and enlargement
  consume the selected member's actual operands inside its token. It is not a
  new SQL interpreter or a permission-only all-member event sequence.
  Generated earth-domain CHECKs obtain their immutable source coordinates from
  the existing binder at installation; helper identity alone is not a callsite.
  Their actual selected C point/dimension/distance leaf consumes that receipt
  after VALUE/strict-NULL handling. Distance receives the retained same-generation
  `(0)` literal, not a fabricated operand; its token leaves before the SQL radius
  division/abs/comparison. Execute denial remains initializer-owned, while
  entrypoint denial and trusted subject unwind surround only the actual leaf.
  Diagnostic-only composed fixtures retain genuine analyzed source coordinates
  and manifests too, rather than treating a readable catalog as SQL admission.
  Renamed diagnostic fixtures use the existing authorization `Recapture` to
  pair their substituted catalog and role root; a catalog assignment alone
  remains foreign/stale authority. Captured declarations/source manifests stay
  immutable while only authenticated current names supply error presentation.
  Inquiry bodies derive their immediate entered-body lineage in both ordinary
  and simulated execution. In particular the implicit `ts_debug` wrapper cannot
  reuse its own lineage for the explicit nested query's helper occurrences.
  A pure inactive owner may retain genuine engine-built admission without a
  borrowed operation. The statement adapter returns that exact captured ordinary
  view only; missing/foreign active hosts or operation-less active scopes reject
  instead of adopting ambient authority or dereferencing an absent operation.
  `pgfreespace.go` retains only relation/series state and fixed source-owned
  literal slots between yields. Each consumed SQL-wrapper invocation borrows
  the evaluator anew; first work consumes genuine relation-size/settings/series
  kernels and each produced row consumes the C two-argument capture/probe.
  Its selected size child borrows the same `FreespaceLayout` capture with a
  typed wrapper-extent purpose and checked `Blocks * 8192`; global
  `pg_relation_size` logical byte counters are unchanged. Missing numeric OIDs
  produce size NULL, genuine no-storage kinds size zero, and neither invokes C2.
  Terminal series DONE does not invent a C probe, and errors/cancel/Close retain
  no Host, subject or token. The two literal slots avoid per-row adapter boxing.
  Custom/literal NULL freespace demand excludes only the canonical C2 retained
  call at actual outer initialization; size/settings/series keep their own
  source authority. Generic parameter-NULL demand retains C2's ordinary ACL
  and execute initialization without fabricating an actual C invocation.
  `pageinspect_heap_attrs.go` keeps the two-to-three-argument false default and
  exact returned source occurrences through the nested wrappers. Only actual
  C children require raw-page superuser authority: NULL page skips strict heap
  entry, while nonstrict split checks superuser even for NULL raw_data, validates
  its bitmap before relation lookup, and keeps actual splitting/conditional
  detoast inside the selected split token. Each produced heap item and consumed
  terminal call owns real work rather than empty transition preflight.
  `TestSepgsqlSimulationNativeBodies` and shared
  `TestReferenceSepgsqlSimulationNativeBodies` cover these owners. The latter
  requires native and pinned-reference modes; native protocol-entry assertions
  are source-grounded simulation evidence, not active SELinux/optimizer parity.
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
- **B-tree statistics** (`pgstatindex.go`): installed scalar/source calls check
  current ordinary routine EXECUTE before evaluating their child or returning
  STRICT NULL. The host captures the exact target and computes the compact
  logical model; ten typed fields form one owned RECORD. FROM evaluates the
  scalar once and expands NULL as one all-NULL row.
- **Installed surgery** (`pgsurgery.go`): authentic declaration and current
  schema USAGE/routine EXECUTE admission precede runtime child evaluation.
  Operands evaluate once in formal `reloid`, `tids` order, preserving their
  lexical provenance. Canonical constant-NULL planning can remove the call;
  a live nonfoldable STRICT NULL still reaches admission before returning
  typed NULL VOID without invoking the body. Nonnull calls pass the unchanged
  typed TID array and decoded class OID to the statement host, returning nonnull
  VOID only after successful publication. Prepared recipes retain no permission
  result or heap handle; the host readmits target authority at publication.
- **Buffer cache** (`pgbuffercache.go`): an installed anonymous-record carrier
  retains genuine routine provenance and caller field usages, never OUT aliases.
  Ordinary current EXECUTE/recipe admission precedes execution-time row/count/type
  validation. Namespace access follows ordinary prepared invalidation/reanalysis.
  Metadata Open does not capture buffers. First borrowed Advance authenticates the
  retained declaration against the current descriptor before strict provider
  identity validation, then obtains the sparse snapshot and first row together.
  Supported rename/owner/ACL presentation changes never replace original receipt
  coordinates or weaken forged-provider rejection. Subsequent VPC calls borrow
  the same initializer; cancellation and close release the payload without
  retaining session pins or an engine.
  Declared-OUT eviction records use a separate scalar kernel shared by Eval and
  first demanded source Advance. Open owns evaluated arguments, never a live
  owner. Current ordinary EXECUTE admission also applies to strict NULL; non-NULL
  work enters the genuine routine-body initializer and registered core cache
  authority before mutation. Results contain flags/counts only, with one row per
  invocation and no cached permission or cache-root references.
  Declared-OUT summary/usage inquiries enter the genuine body on scalar Eval or
  first demanded source Advance, revalidating ordinary perfunction ACL/schema
  access at the shared owner after reacquisition, without a superuser body gate.
  Summary emits one record with a NULL average iff used is zero; usage emits six
  buckets including capacity's unused slots in zero. Source payloads retain only
  detached fixed counters and type, never a page snapshot, engine or pin owner.
- **Byte pageinspect** (`pageinspect.go`): scalar/record/source invocation
  shares installed identity admission and once-only native written-order
  argument evaluation. A fixed six-value frame projects formal slots without
  copying a page or evaluating a child twice. The host supplies current
  superuser and exact relation-descriptor authority at the decoder's admission
  boundary.
  Immutable BRIN, B-tree, FSM, GIN, hash, heap and raw-header readers decode bytea without
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
  Hash routes in `pageinspect_hash.go` preserve unused new-page type versus
  required-type errors for stats/items/meta, without inheriting another family's
  new-page NULL shortcut. Stats/meta are scalar composites; only items is a set.
  Outputs own unsigned INT8/OID, native FLOAT8, TID and full INT8-array values.
  Heap routes in `pageinspect_heap.go` construct typed rows directly from checked
  immutable per-slot views; unavailable tuple headers and tails remain distinct
  NULL groups. Flags construct typed nonnull TEXT arrays, including empty arrays;
  a strict NULL flags source is one NULL record, while a NULL heap source is empty.
  `pageinspect_heap_attrs.go` reuses those per-slot views and one shared physical
  splitter. Bitmap admission precedes deferred schema capture; NULL tuple data
  never opens a relation. Captured physical slots are reused within the call.
  Wrapper startup resolves genuine sealed helper members and checks current
  EXECUTE privileges before NULL/zero-row shortcuts; outer grants do not authorize
  helper calls. Startup failures retain only the SQL frames actually entered.
  SQL wrappers retain truthful nested statement contexts, and detoasting consumes
  only actual supplied inline content, never logical table rows or process pointers.
  Caller-supplied bytes are not physical relation acquisition.
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
- **Seams**: `Host` (`host.go`) and its capability groups
  (`host_capabilities.go`), `NestedSQL` (`nested_sql.go`),
  `DetachedPresentation`, `Preparation` (`preparation.go`) and the admission
  slots `AdmissionAuthority` and `RoutineProgram`.

## The host

`Host` (`host.go`) lists every method with its group. The engine implements it
on `statementHost`, a defined type over `StatementScope`.

| Group | Methods | Lock and lifetime contract |
|---|---|---|
| Environment | `Context`, `Environments`, `StatementStart`, `CTEInvocation` | Read where the runtime reads them; a held cursor detaches the host between fetches. Partial aggregate workers use the outer CTE snapshot captured before launch, including nil, never a sibling support body's lexical scratch. |
| Presentation | the nine `sqltype` formatters | Read the statement's comment/deparse snapshot. With no host, output uses `DetachedPresentation`. |
| Row locks | `AcquireQueryRow`, `LatestQueryRow`, `QueryLocks`, `CaptureTupleXmax` | Admission uses actual RowVersion and exact immutable header resources, releases the replacement read across waits, and revalidates afterward. Latest-row reads follow only genuine successors. Raw presentation captures release all metadata locks before manager inspection and retain detached immutable scalars. |
| Foreign owner | `ReleaseReplacementRead`, `TouchForeignScan`, `EnlistForeign`, `AsyncAppendEnabled` | Release the replacement read lock before external I/O; enlist the statement's foreign transaction participant. Async eligibility reads the immutable admission snapshot once. |
| Admission | `RoutineAdmission` | Reads statement-owned routine authority and dependency manifest without a lock; fails once the statement has finished. |
| Sequences | `CompletePGSequence` | Acquires the sequence lock, then reads the generator under `Engine.Mu`. |
| Capabilities | the function, expression and plan runtime bodies | Take `Engine.Mu`, read session state or have side effects (advisory locks, XID assignment, sequence calls, notices). |
| Capability groups | `Contrib`, `TextSearch`, `CatalogInquiry`, `SessionInquiry`, `TransactionInquiry` (`host_capabilities.go`) | Each accessor returns the group's runtime; the engine converts the host pointer to the group's own type, so nothing is allocated. The groups are the runtimes a package below core can own. |

Every method is called without `Engine.Mu` held, and none returns an engine
struct. Records store the host once, never per row. A record with no statement
scope holds an untyped nil host (`LiveHost` never returns a typed nil). Optional
host capabilities such as `PgcryptoNotice` are resolved through `LiveHost` per
delivery; the engine appends owned notice records and never calls user handlers
under engine locks.
`InitializePgcrypto` and `CheckPgcryptoBuiltinCrypto` likewise resolve the current
host, never a prepared owner. Authentic surviving installed calls initialize
their physical provider after routine admission; strict constant-NULL and
decisively pruned branches do not. This also covers zero-row executor setup.
Actual installed evaluation initializes before demanded children; the password
kernel's separate live gate remains after typed STRICT handling. Readmission
does not replay bodies or invalidate accepted immutable values on SET.
Closed evaluation rejects both pgcrypto initialization and policy capabilities
before touching an incidental live host, including already-loaded/on sessions.
Installed crypto is not a closed-worker expression; aggregate SQL support bodies
enter their own gated live lexical evaluation rather than inherit that capture.

`NestedSQL` (`nested_sql.go`) is the SPI analogue for functions that run SQL
text inside the calling statement (ts_rewrite, ts_stat, crosstab, connectby, dblink).
The engine implements it, because admission, binder construction and
dispatch re-entry stay in core; the caller owns its statement loop, its SPI
policy (`NestedRequest`) and a `NestedConsumer` that admits each command and
consumes its rows. Each command runs in fresh lexical command scratch, with
no parameters, over the caller's private catalog and data.
`FreshCommand` opts into ordinary command snapshot admission instead of
reinstalling the caller's View/RLS references or borrowing source roots. Its
core-owned lexical routine policy determines volatile freshness versus stable
transaction visibility; activation restores exact outer snapshot and scratch.
This policy cannot be combined with `BorrowSource`. INSERT source diagnostics
stack the actual SPI statement frame once, including fetch/close failures and
admitted cancellation; existing nested callers keep their own diagnostic policy.


The typed `ReplicationOrigin` capability resolves through `LiveHost` for each
surviving invocation, after common routine admission and strict operand handling.
Its bridge carries only kind, two typed values and the result descriptor; neither
prepared recipes nor contexts retain a second origin store or captured actor.

Installed text execution authenticates captured provider/signature, exact formal
and result generations, and the current unaccent declaration/reference before
strict NULL can return. The optional REGDICTIONARY child precedes TEXT in
evaluation and planning; known strict NULL may discard runtime-only casts only
after ordinary input and constant planning. STABLE unaccent results are never
constant-folded. `TextSearchHost.UnaccentText` forwards a typed optional selector
to the existing acquisition owner; daitch_mokotoff retains unary host-free
immutable execution and its canonical TEXT[] element generation.


## Preparation

Domain checks and preflight also run with no statement scope (generated
columns, index expressions and predicates, partition admission, temporary
commit, concurrent refresh), so they cannot hang off a nil-able host.
`Preparation` is one stateless package value: `AdmitExpression`,
`PreflightQuery`, `PrepareDomainConversion`, `SpecializeRuleParameters`,
`CheckDomain`, `DomainCatalog`, `ReturnsSet`, `StoredReferenceValid`,
`ValidInstalledText`, `AcceptedDefaultSource`, `AcceptedDefaultRoutine` and
`MapAcceptedDefaultChildren`. Core's `enginePreparation` installs it from `init`
with `InstallPreparation`; until then every method panics with
`exec: preparation not installed`. The DEFAULT methods reuse stored's existing
scalar mapper and exact paired publication proof, not a second callback registry
or executable store.

`AcceptedDefaultSet` is owned by an accepted binding, not a reusable definition.
`AcceptedDefaultSet.Prepare` obtains its canonical DEFAULT root from the exact
current source descriptor. Private preparation-only tokens record original call
paths and a closed invocation mode before one incumbent typed preflight; only its
actual complete result is transferred into the candidate. Mode mint validates the
authentic source declaration, actual code definition and genuine language/handler
authority. Typed ordinary scalar PL sources select current-provider mode; other
valid sources select predecessor mode. Spelling, display metadata and caller flags
grant neither. Tokens retain exact original definition identity through legitimate
argument COW, grant no invocation rights during Bind, and are stripped from the
ordinary local result. Arbitrary output or ABI-only comparisons cannot mint
derivation. Artifacts retain original source, actual prepared recipe, original
lexical paths, mode/definition association and dependencies.
The stored dependency references remain original across genuine retained TEMP
replacement. Preparation and source validation reuse the canonical contextual
stored-reference proof, not an equal-current-epoch requirement or a general
stale-reference exception. Foreign namespaces, changed provider semantics and
ordinary stale `ObjectRef` uses still reject.
Source-call/path access is solely lineage evidence. Dispatch separately
authenticates the sealed actual prepared descriptor and its exact immutable
argument, assembly and signature backing; a value copy is valid, an independently
copied same-provider call is not. Source paths use stored's actual direct-child
mapper slots, including nil slots, rather than generic walker order. That mapper
returns actual changes and shares unchanged children. Closure validation includes
hidden defaults without granting them the enclosing call's capability.
Private root/call seals and a short-lived `AcceptedDefaultInvocation` constrain
historical execution to that exact selected DEFAULT; ordinary copied nodes and
other same-address calls do not acquire it. Activation and dispatch select the
same actual invocation host: explicit statement host first, then the query
source's live host. Foreign-host substitution cannot consume an active selection.
Current provider/type/ACL and owner/native/transaction checks
remain live. `RoutineProgram` records exact consumed occurrence evidence and
readmits it under the actual actor after waits; it never saves a permission
decision or carries that exception into an unrelated phase. Original argument
default and child-body manifests stay independent. No Context/Host, activation,
frame, data root, command graph or volatile result is retained in an artifact.
Live dispatch returns independently validated current owner environment and the
mode's executable: captured prepared descriptor for predecessor mode, freshly
validated compatible current PL descriptor only for its sealed source mode. All
source/prepared/readmission lanes reject zero, unknown or recombined modes. This
choice never rewrites prepared arguments, call assembly, Usage or accepted backing.
Retention uses the same closed source/prepared selection independently of
invocation. `AcceptedDefaultExpressionExportRootDemand`, accepted-set demand and
`RoutineProgram.ExportRootDemand` share one pure selector: the sealed SQL/host
predecessor or the authenticated current PL provider. Prepared arguments keep
ordinary traversal and independent nested/hidden/body manifests. Invalid lineage
or retired ownership means conservative possible demand, never an invocation
grant or an earlier diagnostic. No evaluation, preflight, ACL decision, snapshot,
provider cache or mutable artifact is created by this projection.

## Expression preflight

`exec` owns the expression preflight that runs before execution:
`PreflightCoercions` and `PreflightCoercionsWithPolicy`, conditional planning and
folding (`conditional_*.go`), literal/bound strict-NULL folding, array and UPDATE
value preflight, string variadic folding, view qualification mapping and the
OVERLAPS route. Preflight reaches the engine only through `Preparation` and runs
outside engine and publication locks. Conditional planning keeps every analyzed
dependency and authorization even when it removes a branch; held parameters are
reconsidered on every execution, never stored as planning facts.
XPath's narrow preflight route keeps the analyzed call's dependency properties
and `PreserveParameterDemand` authoritative for independence and generic demand.
The two-arg wrapper diagnostic route additionally requires immutable computation
and no execution barrier in the mapped call. STABLE/VOLATILE and barrier-bearing
calls remain runtime/inline; existing strict-NULL proof still precedes the
ineligible runtime return and may discard runtime-only siblings without execution.
A route-only change is an execution-private graph replacement even when no child
conversion changes. Constants remain eligible under generic mode; parameterized
generic and row-dependent calls remain symbolic. Custom parameter results live as
private graph literals, not persistent accepted entries. No execution route
mutates stored analysis or accepted constant recipes.
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
Source admission recognizes both ordinary and bare-metadata post-child strict
folds. A current typed-parameter NULL uses the same proof as a literal NULL;
unknown/generic parameter frames and row-derived NULLs still demand EXECUTE.
Neither route reduces RECORD-returning calls or set-returning calls.


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
also supply demanded immutable arithmetic descendants through the existing
planning admission evaluator. Discovery inspects comparison/operator/collation/
non-domain coercion carriers and ordinary immutable scalar Composite children,
but never executes an enclosing function merely to find arithmetic. Evaluation
still requires the narrow typed arithmetic tree, including its selected operator
and coercion authority; arbitrary providers do not become evaluable. Whole-argument
row/stage/domain/volatile and transitive execution-demand barriers stop descent.
Conditional branches, RECORD/set sources, special fold routes and uninitialized
arithmetic inputs remain outside this demand point. The temporary value is
discarded after checking errors, without parameter substitution, persistent
parameter-fold capture or mutation of the analyzed recipe. Generic parameter
demand and missing execution-context barriers remain intact.
A domain conversion in the NULL-supplying argument is not a proof. Runtime row
NULL is not a planning fact and retains sibling admission and effects.
Declared RECORD results, SRFs and compound NULL-aware carriers are excluded.
`LargeObjectCall` shares this strict scalar policy: known NULL can eliminate
runtime siblings, while a reached runtime call demands its arguments once.
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

`pgfreespace.go` authenticates the current declaration and schema/routine authority
before evaluating children once in written order. Runtime scalar NULL returns typed
INT2 NULL after admission; known-NULL simplification uses the shared strict planner.
The non-STRICT wrapper checks outer authority even for NULL (typed empty output).
A custom body can eliminate its STRICT scalar call for known NULL; preserved
generic body-parameter demand still checks that genuine scalar's EXECUTE. Every
non-NULL input checks it too, including empty, absent and no-storage targets.
Inner denial uses shared SQL-function Context with current scalar/wrapper names.
The current invocation descriptor/epoch never overwrites its retained source
carrier or repairs a missing original lifetime proof. Metadata Open retains only
supplied values; first borrowed Advance captures the extent and advances the
actual series. Each produced block separately enters and consumes its C2 probe,
and terminal DONE invokes the series without inventing a scalar probe. No page
vector, closure, evaluator, body token or retained host survives a yield.

`pgvisibility.go` authenticates sealed identity before evaluating lexical children
once into formal slots. Metadata Open performs no capture or truncation; the first
borrowed Advance uses the actual initialized callsite and current invocation
subject to capture findings/map plus the first row or DONE. Later VPC calls reuse
that immutable payload but still borrow their original initializer. The scalar
FROM route invokes the same scalar kernel only on its first demand; truncation
returns nonNULL VOID only after successful publication. Scalar STRICT NULL retains
one scalar-source row; STRICT sets retain metadata with no rows. PD comes from
the separate compressed current heap proof, not VM flags. Execute MAC belongs to
original initialization, while host readmission retains current ordinary SQL ACLs.
Completed results retain no child, host, actor, store or locks.

The dblink DELETE/INSERT/UPDATE evaluators authenticate the demanded installed scalar
and current schema/EXECUTE authority before evaluating their four/five written
children once. Runtime whole NULL returns typed TEXT NULL without relation
admission; ordinary strict planning may erase known NULL calls earlier.
Non-NULL calls use `ContribRuntime`: DELETE captures metadata only, whereas
INSERT and UPDATE share one real nested source SELECT executor and fully draining
consumer, with each builder's genuine caller identity and volatile command policy.
None retains returned SQL, selected rows, locks or a prepared text cache, or executes remotely.

Connection-family carriers follow current security/configuration and supported
planner estimates only after full authentic current installation validation.
Their original sealed signature, result, strictness, volatility, origin and body
recipe must still compare exactly; generic `SameRoutineDeclaration` is unchanged.
Native expression initialization owns the subject token, while the core host
separately installs and unwinds the shared effective-user/settings environment.

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
  over a captured hash index, and a WHERE (also inside derived subqueries) over a
  shared catalog capture (`Source.CatalogKeys`) that is one equality, or an AND
  of equalities, between a column and a literal or parameter under non-volatile
  casts, with at least one oid column; the oid key with the fewest rows selects.
  `CatalogKeyIndex` builds a column on first use and publishes it atomically. A
  column qualifies only when every cell has the column's exact oid descriptor,
  and the other conjuncts compare plain bool/char/name/int/text/oid columns with
  their own equality, so no skipped row could have raised an error (AND evaluates
  both operands); any failing probe keeps the scan.
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
Native selected kernels borrow only the immediate support association described
above. Their aggregate-owner SQL EXECUTE admission remains separate from the
invoking scope's SQL/PL/host body and relation rights. Strict skipped rows,
unseeded transitions and NULL finals create no fictitious native body entry;
nonstrict NULL invokes its genuine selected kernel with the actual NULL facts.
Neither runtime association nor native-body continuation is persisted by remap,
Dump/Restore or worker capture.

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

Sampling selection, visibility/layout preservation and lifecycle units use
`RUN='^TestSystem(Rows|Time)'`, including composed statement-owner coverage in core.
`TestSystemTimeNestedReaderDeadline` and `TestSystemTimeNestedSetDeadline`
filter the first logical caller before symbolizing unrelated ancestor frames.
Only `sampleCheckpoint` is transparent; matching reader calls still collect the
real target ancestor and grouping admission PC. Set-output observation needs
only its direct caller, not input-arm stacks. These observers leave the real
100ms scan budget, full fixture population, real elapsed-work gate, exact phase
counts, cancellation priority, Infinity traversal and capture-owner cleanup
assertions unchanged; they do not substitute a clock or a provider callback.

SYSTEM_TIME elapsed-work fixtures that call `timeWorkPastBudget` own virtual Go
time through `synctest.Test`, rather than spending their 100ms sample on suite
scheduling before genuine work begins. Shared test-only `timeRunDeadline` wraps
only timed leaf scenarios: producing continuation, nested physical/sample
owners, aggregate, blocking, CTE, join, projection, pruning, reader, membership
and cancellation witnesses. `TestSystemTimeElapsedBudget` and the existing ALL-set
modes use direct bubbles. Outer grouping subtests and unrelated visibility
preparation remain ordinary tests.
The existing ordinary 110ms timer/select advances bubble time at the original
observed input/support/output gate. The cancellation worker, its channel gates
and mandatory join remain genuinely owned inside that same bubble. Production
`time.Now`/`time.Since`, five-second host contexts/guards and all exact prefix,
input/output count, Infinity multiplicity, cancellation/error precedence,
retained-backing, continuation and cleanup assertions remain unchanged; no
context error or sampler clock is overridden. Selected native and race proof:
`RUN='^TestSystemTime(ProducingSourceContinuation|ElapsedBudget|Nested.*Deadline|ScanCancellation)$'`.

Window phase cancellation witnesses in core use
`RUN='^(TestQueryWindow(Groups|Rows|Exclude)Cancellation|TestQueryWindowValueOutputPublicationCancellation)$'`.
Their stack observers invoke an ordinary CancelFunc only at the selected real
operation. Direct logical-caller matching skips exactly `exec.sampleCheckpoint`,
not expression/coercion or aggregate/support frames: GROUPS/ROWS validation must
reach the offset validator, and SUM output must return to the window opener
after genuine finalization. These witnesses retain phase-entry, cancellation,
no partial result, resource release, idempotent close and same-graph recovery
assertions; checkpoint plumbing must not merge those phases.

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
Freespace layout, released identity and composed capture ownership use
`RUN='^TestPgFreespace'`.
Installed visibility runtime and composed capture ownership use
`RUN='^TestPgvisibility'`.
Surgery invocation and composed definition ownership use
`RUN='^TestPgsurgery(Freeze|Kill)Provider'`.


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
Named-leaf packed topology and physical-occurrence ownership units use
`RUN='^TestNamedRelation'`, also with `RACE=1`.

Semantic routine descriptor identity and immutable retained configuration use
`RUN='^TestRoutineAttribute'`, also with `RACE=1`.

Foreign parallel dispatch uses `testing/synctest` (`RUN='^TestFDWParallel'`):
settled callback counts, never elapsed time, establish backpressure.
Async protocol, cleanup, filtered-frontier and cursor-boundary units use
`RUN='^TestFDWAsync'`; public authentic-transport scenarios live in
`reference/tables` (`RUN='^TestReferenceFDWAsync'`).
Typed foreign query contract, layout, request-copy and resource units use
`RUN='^TestFDWQuery'`, including composed prepared-owner coverage in core.
Mixed hierarchy lineage, alternative-member recheck and rewind ownership use
`RUN='^TestForeignHierarchy'`, also with `RACE=1`.
Query source identity units use `RUN='^TestQuery(TableOID|SystemAttribute)'`; virtual recipe
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
make check RUN='^TestStrictParameterNull(Planning|Boundaries|CompoundSemantics|ComparisonInputs)$'
make check RUN='^TestReferenceObjectGrantStrictNull(Demand|Precedence|Imports|Held|Stored)$'
make check RUN='^TestReferenceObjectGrantStrictNull(Demand|Precedence|Imports|Held|Stored)$' REFERENCE=1
```


**Frame names matched by tests.** The ordered-set public controls require the
full worker label and live `sortOrderedAggregate.func1` in one complete pprof
debug=1 record. The original active-cancellation fixture retains a finite 64³
workload and unchanged 30-second route/15-second admission budgets; its semantic
sort hold supplements, never replaces, that profile witness. Renaming the
comparator requires updating these fixtures. The recursive union stage keeps
the unexported defined type `recursiveUnionStage` (aliased as
`RecursiveUnionStage`; `TestReferenceQueryCycleLifecycle`).

Real-sort pause, ordering and cancellation units use
`make check RUN='^TestSortOrderedAggregate'`, adding `RACE=1` for race coverage.
Public controls belong to `reference/aggregates` and its family selector.

```sh
make check RUN='^Test(MissingPreparation|InstallPreparation|AcceptedDefault|ViewProducer|SetArmSource|CorrelatedJoinSource|QueryBranchPruning|SortOrderedAggregate|PgcryptoNotice)'
make check RUN='^Test(CreateAggregate|AggregateFinal|AggregateMoving|AggregateOrdered)'
make check RUN='^Test(PLpgSQLQueryLoopLifetime|FTSQueryFunctionsBatch)'  # row owners
make check RUN='^TestReferenceQuery'   # public query execution; add REFERENCE=1 for the oracle
```
