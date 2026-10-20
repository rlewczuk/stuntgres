# PG18-ARCH-QUERY-EXECUTION — Query execution pipeline

## 1. Decision, scope and authority

Adopt one immutable typed relational tree inside the existing statement binder and one typed row/result path for SELECT and expression-consuming DML. The query owner binds source layouts and relational dependencies, orchestrates expression evaluation, and owns intermediate row retention. It does not create another catalog, expression binder, store, prepared-plan validity system or transaction owner.

**This document is a design deliverable, not implemented APIs, running SQL behavior or passing tests.** All declarations below are proposed contracts. Feature syntax and execution remain the exact downstream owners in §10. This part changes only this document; it implements no SQL feature, prerequisite or adjacent design. Production dependencies remain standard library/module only. No physical PostgreSQL planner/storage replication, spill subsystem, wire server or network executor is authorized.

Consume these authorities, with their existing precedence:

- [EXPRESSION-ANALYSIS](PG18-ARCH-EXPRESSION-ANALYSIS.md) §§2–6: private `expressionBinder`, `expr.Expr`, explicit `ExprPolicy`, scope/frame coordinates, `EvalContext`, `StageValues`, scalar laziness, diagnostics and no statement-fragment publication.
- [TYPE-SYSTEM](PG18-ARCH-TYPE-SYSTEM.md) §§2,5–7: `sqltype.Usage`/immutable `Value`, coercions, ordered `CommonType`, parameters, typed key families, `ColumnDescriptor` (now `catalog.Column`)/`StatementDescription`, `ImportParameter`/`ExportValue`.
- [CATALOG-IDENTITY](PG18-ARCH-CATALOG-IDENTITY.md) §§2–7: authoritative lookup, provenance, dependencies, retained definitions, `BoundPlan` validity/reanalysis and storage/publication identity. PG18-CAT-CORE is implemented; the other typed execution contracts are not thereby implemented.
- [SESSION](PG18-ARCH-SESSION.md) §3: same pointer-owned `*Operation`, selected target, coherent resolution and combined cancellation; [TX-VISIBILITY](PG18-ARCH-TX-VISIBILITY.md) §§2–3,5–8: `StatementScope`, conditional `AcquireDataView`, write stage and `Finish`. [AUTHORIZATION](PG18-ARCH-AUTHORIZATION.md) owns access requirements, checks and RLS/leakproof placement. Query scheduling cannot bypass these policies.

The release semantic authority is the official PostgreSQL [18 SELECT](https://www.postgresql.org/docs/18/sql-select.html), [table expressions](https://www.postgresql.org/docs/18/queries-table-expressions.html), [select lists](https://www.postgresql.org/docs/18/queries-select-lists.html), [expressions](https://www.postgresql.org/docs/18/sql-expressions.html), [WITH](https://www.postgresql.org/docs/18/queries-with.html) and [RETURNING](https://www.postgresql.org/docs/18/dml-returning.html) documentation. Those pages identify 18.6; preserve the existing reference runtime **18.0, UTF8/C**, image `postgres:18.0-bookworm@sha256:3f55f8895c4ed50603e2fbdfc72fffeeaba3173321fee5cb825bbbeb30d9d854`. Only PG18-REF-ORACLE-18-6 owns any necessary minor-version alignment. Local PostgreSQL 20devel and ramsql are research aids, not release oracles or production dependencies.

Acceptance is traceable as follows: shared interfaces/ownership §§2–3, stage and semantic boundaries §§3–5, lifetime/cancellation §§6–7, producer-defined RETURNING §8, complete cutover §9, exact bounded owners §10, and independent future witnesses §11. The seven minimum acceptance witnesses map respectively to W2, W1/W2, W3, W4/W5, W8, W9 and W10.

## 2. Shared plan, row and result contracts

Proposed engine-private declarations use the sole type-owned metadata shape:

```go
// Closed engine node set, not a public provider/plugin registry.
type LogicalNode interface {
    logicalNode()
    Schema() []ColumnDescriptor
}
type BoundQuery struct {
    Root LogicalNode
    Description StatementDescription
    Layouts []FrameLayout
    // Statement-owned immutable dependencies/access requests are finalized
    // with the containing BoundPlan, not tracked by another validity owner.
}
type FrameLayout struct {
    Scope ScopeID
    Frame FrameID
    Usages []sqltype.Usage
}
type RowCursor interface {
    Schema() []ColumnDescriptor
    Next(ctx context.Context, dst []sqltype.Value) (present bool, err error)
    Close() error
}
type RowResult interface { RowCursor }
type Result struct {
    Count int64
    Rows RowResult
}
```

The IR is implemented as `plan.Node` and `plan.Query`; see the
[`plan` README](../../internal/engine/plan/README.md).

`BoundQuery` is the relational payload of catalog's `BoundPlan`, not a second prepared object/cache. Statement analysis stores one frozen `StatementDescription`; when the query supplies public rows, `Description.Results` and its root schema are the same logical output shape. A DML producer may instead consume a private typed `BoundQuery` source: ordinary INSERT describes zero public results and returns `Rows == nil`, regardless of that input schema. Catalog's illustrative parser-column plan fields consume the type-owned shape at cutover. For execution, `Rows.Schema()` is authoritative; remove independently mutable `Result.Columns` rather than maintaining two result metadata lists. This is the query integration refinement of type design's illustrative `Result.Columns`, not a competing descriptor definition.

`Rows == nil` means no row relation (ordinary command). A SELECT or RETURNING producing zero rows still has a nonnil result and its full schema. Empty schema is not absence of a relation. `Count` is command-defined: preserve baseline SELECT count as final result cardinality where exposed; DML count is exclusively the producer's affected count and need not equal RETURNING/CTE result cardinality. Joins, grouping and other relational stages never invent affected-write counts.

`Next` requires `len(dst) == len(Schema())`, supplies valid immutable Values of the bound usages, and returns `present=true` for one occurrence, including a zero-column occurrence. `false,nil` is EOF; `false,err` is failure; `true,err` is forbidden. Contents of dst on EOF/error are unspecified and must not be consumed. Width/usage mismatch is an ordinary internal invariant error, not a SQL diagnostic. The caller owns/reuses dst; a stage/provider must not retain that borrowed slice. Values do not borrow mutable scratch. Retaining stages copy only needed Value slots into execution-owned storage once, or retain a proven immutable indexed source view. Schema slices are read-only borrowed immutable metadata; adapters return copies where public mutable slices are required.

Internal cursors and public results use this same typed contract. `RowResult` adds no alternate metadata, ownership or error behavior and does **not** imply public streaming. The initial public result is a cursor over fully finalized retained output (§6). Root rows owns one reusable typed destination and exports into driver dst using type-owned `ExportValue`.

### Closed node records and opening

Every node contains immutable output schema/frame layout and child references. The statement binder validates widths/usages before freezing; execution opening compiles layouts to private indexed frame access and creates cursors/accumulators/scratch. A single engine-private `openNode(state, node, outerFrame)` dispatches the closed kinds. It never binds expressions or resolves row names. Opening failure closes already opened children and releases their pins. Node kinds below describe concrete required fields, not string stage names:

| Record | Bound fields besides schema/layout | Execution responsibility |
|---|---|---|
| SourceNode / UnitNode | Source target/ObjectRef, descriptor generation/read purpose; unit has no columns | Retain permitted input view; scan each occurrence; unit emits one zero-column row |
| DerivedNode / CTEScanNode | Child query, output slot mapping; CTE producer ID/consumer configuration | Open child with explicit outer frame; CTE has independent consumer position |
| JoinNode | Left/right, closed join kind, BOOL ON expression, legal lateral dependencies, USING merge slot/coercion recipe | Pair occurrences, evaluate ON, track matches, extend typed NULLs |
| FilterNode | Child, BOOL predicate and its frame | Consume until True or EOF; check cancellation for rejected rows |
| GroupNode | Child, fixed post-group key slots/usages, grouping-set membership lists and occurrence IDs, nonnull key families, aggregate definitions with args/FILTER/local DISTINCT/order, GROUPING result stage coordinates | Initialize/transition/finalize private accumulators; emit typed NULL for absent keys; expose key/aggregate/GROUPING slots while retaining separate membership/identity |
| WindowNode | Child, partition keys/families, sort keys, frame/exclusion definitions, bound calls and stage slots, equivalent-spec order identity | Buffer required partitions, evaluate feature frame semantics, preserve occurrence count |
| ComputeNode / ProjectNode | Child, bound expressions, input/output slot mappings, evaluation dependency phase | Compute needed keys/targets at legal phase; expose only selected output slots |
| DistinctNode | Child, projected tuple keys/equality families | Retain one equivalent projected tuple |
| SetNode | Left/right, UNION/INTERSECT/EXCEPT kind, ALL flag, per-side common-type coercions/collation derivation; tuple families only for operations requiring equivalence | UNION ALL coerces and concatenates bags without key/count state; other operations coerce before equivalence/counting and preserve exact multiplicities |
| SortNode / DistinctOnNode | Child, per-key expr/slot, direction/null placement/family; DISTINCT ON key slots/leftmost legality | Error-capable ordered production; sorted first occurrence per distinct key |
| SliceNode | Child, bound offset/count, unlimited/FETCH/ties configuration and final peer keys | Skip offset, demand count plus ties; close unused child production |

CTE producers and scalar subqueries are query-owned subplans with explicit dependencies, not new scalar-owned scans. VALUES and FROM-SRF owners attach typed sources through the same contract. Feature-specific aggregate/window providers may extend immutable configuration; they cannot add per-execution state to nodes. Required key capability errors are diagnosed at the appropriate analysis consumer, not replaced by Go comparisons at runtime.

## 3. Binding, scopes, stage order and correlation

The **existing statement binder** owns relational analysis and its private `expressionBinder`. It obtains source descriptors with catalog `ResolveReference`; callable candidates with `LookupCandidates`/`LookupKind`; and delegates `ResolveOperator`, `ResolveFunction`, `PlanCoercion`, ordered `CommonType` and key-family selection to TYPE/EXPR. Query analysis supplies explicit policies for every target, ON/WHERE/HAVING, grouping key, aggregate/window argument, ordering key and count expression. BOOL expectations and aggregate/window/subquery legality are clause-specific. Bind every occurrence even on empty sources or unreachable scalar branches. No fallback truthiness, runtime row-name lookup or SQL-substring evaluation.

Finalize the whole statement only after all source/output schemas, common types, parameters, coercions, scope legality, dependencies and access requirements succeed. Failed analysis exposes no executable plan or partial description. Standalone SELECT/RETURNING unresolved output fallback is type-owned TEXT contextual finalization; set operands and INSERT SELECT defer fallback to their surrounding type context. Sparse parameter constraints/finalization remain statement-private, and execution values cannot change prepared types.

Query analysis allocates plan-unique **nonzero** ScopeID/FrameID and slots. Physical `ObjectRef.SubID`/OriginAttribute is a catalog attribute number, never a slot. Derived/group/window outputs have valid stage coordinates and zero physical Origin unless the metadata authority explicitly preserves a direct physical projection. Retain `{DatabaseTarget,ObjectRef}` identity provenance for access/visibility independently of frame layout. Parent-child widths/usages, stage slots and coordinates must agree. Output names, type identity, typmod, collation and knownness of nullability are exact; hidden keys cannot leak columns. Outer extension must weaken source NOT NULL guarantees. Expression nullability is unknown unless proved; do not fabricate lengths/precision.

Logical dependency order is:

`WITH environment → FROM/join → WHERE → group/aggregate → HAVING → window → target/key dependencies → DISTINCT or set composition → final ORDER / sorted DISTINCT ON → OFFSET/LIMIT/FETCH → final output/export`.

This is a typed **tree**, not a compulsory physical linear schedule. Each set operand has its own pipeline; parenthesized operand ORDER/LIMIT stays local, unparenthesized final clauses apply to the set tree. DISTINCT ON is sorted first-per-key, not ordinary unsorted DISTINCT before ORDER.

**Scheduling qualification:** targets not needed by DISTINCT/ORDER/GROUP may execute after sort/limit. Target-list SRFs execute after sorting but before limiting. Aggregate arguments execute at their earlier aggregate stage; later CASE cannot guard them. EXPR owns scalar evaluation/laziness/properties, query owns relational scheduling. Bind all expressions regardless of later demand; do not eagerly evaluate every expression on discarded rows. The initial QUERY-CORE has only its bounded current capability; relevant later owners implement these schedules, not an optimizer invented here.

| Clause/stage | Available namespace and transition |
|---|---|
| FROM/ON | Source schemas, legal left/right inputs; JOIN binds tighter than commas, parentheses preserve nesting; ON uses joined input slots before extension |
| WHERE | Complete joined/extended source namespace; no SELECT output aliases; no aggregate/window outputs |
| GROUP BY / aggregate inputs | Pre-group inputs; ambiguous GROUP BY names prefer input column over output alias; aggregate FILTER/args/local keys use eligible pre-group frames |
| HAVING | Legal group keys and aggregate slots plus feature-permitted grouped dependencies; no SELECT aliases, no window outputs; HAVING alone induces grouped query even without GROUP BY |
| Window | Post-HAVING rows/groups and aggregate slots; not raw ungrouped rows discarded earlier; no nested illegal window use |
| SELECT targets | Stage-appropriate row/group/window frame; star expanded once from exposed schema; output labels become derived names/metadata |
| ORDER BY | Simple SELECT may use hidden input expressions; ambiguous name prefers output alias. Set-level ORDER uses output name/ordinal only; DISTINCT restrictions checked by owner |
| OFFSET/LIMIT/FETCH | Feature-restricted count expressions, not per-source row columns; contextual count typing and diagnostics owned by limit owner |

Aliases hide original relation/join names. USING schema is merged columns in listed order, then remaining left columns, then remaining right columns; preserve feature-defined qualified access without duplicating exposed merged columns. NATURAL derives shared names from schemas, and no shared names means cross join.

### Correlated frame protocol

A non-LATERAL derived FROM scope excludes sibling inputs. An enabled outer query scope is distinct from sibling visibility. LATERAL admits only eligible preceding/left inputs, and dependent RIGHT/FULL references are rejected. Preserve EXPR's resolution stopping rule: unqualified columns may traverse an enabled outer edge only if no eligible local match; qualified lookup selects the nearest visible qualifier first, and missing column in that item never restarts in an outer qualifier.

For **each outer occurrence**, bind its current values to a private EvalFrame instance with the immutable Scope/Frame coordinates, set the exact Outer chain, open/reopen the dependent child, consume according to its consumer, close the child, then reuse scratch only after that child is no longer live. Depth follows exactly the prebound outer links. Retained child rows cannot borrow an outer mutable slice. Independent executions/outer occurrences have distinct frame/StageValues state; no unkeyed child-result cache across rows or executions.

Scalar-subquery query adapter owns one-column width validation and cardinality: zero occurrences yields typed NULL, one yields its value, the second fails with cardinality violation. It need not drain a third occurrence to prove failure. EXPR Eval reads this supplied adapter/stage result and never independently captures storage. LATERAL source iteration instead preserves all demanded child occurrences and applies outer join rules where relevant.

## 4. Bags, NULLs, joins and grouping

Default relations are **bags of occurrences**. Equal values never imply occurrence identity. Projection preserves multiplicity unless an explicit cardinality-changing feature intervenes. Without FROM the unit source has one zero-column occurrence (`SELECT 7` produces one row); an actual empty table produces none even for constant targets. Zero-column projection preserves source multiplicity wherever its syntax owner permits it; cursor `present` distinguishes a zero-width row from EOF.

NULL is a concrete typed Value, not zero/invalid Value or a missing frame. `TruthOf` from EXPR maps nullable BOOL to True/False/Unknown. ON, WHERE, HAVING and aggregate FILTER retain **True only**. Strict comparison of NULL is Unknown; it is not key equivalence. Invalid/non-BOOL runtime conditions are invariants; bind enforces BOOL.

### Join occurrence algorithm

Use the boring initial nested-loop algorithm, not key existence/deduplication. For each left occurrence evaluate ON for every relevant right occurrence. Each True pair emits a row and marks that **occurrence** matched. False/Unknown does not mark it. LEFT emits exactly one tuple with right typed NULLs if that left occurrence has no True match. RIGHT is the converse. FULL also retains right-occurrence matched bits and emits one left-NULL tuple for each unmatched right occurrence after paired production. Empty sides follow the same rules. Match state is execution-private and includes duplicate right occurrences separately.

ON determines matching **before** post-join filtering; WHERE sees the extended tuple. A post-extension WHERE rejecting the NULL side cannot retroactively make a matching pair unmatched or cause another extension. USING binds equality/coercion recipes once. PostgreSQL18.0 INNER merged values prefer an uncoerced left Var, otherwise an uncoerced right Var, otherwise converted left: INT4/INT8 therefore preserves the genuine right INT8 origin. A direct merged Var retains physical metadata; a conversion/computation has synthetic origin and resolved common typmod. LEFT chooses the left, RIGHT the right, and FULL the appropriate nonmissing side (coalesced with bound common-type conversions). SQL NULL=NULL still does not match USING equality. FULL must retain the right merged value on unmatched-right rows. Remaining qualified columns retain their proper source values/NULL extension.

### Key equivalence and aggregate transitions

Query wraps type-owned **nonnull** `KeyEqual`/`KeyCompare`/optional `KeyHash`: two NULL keys are equivalent for grouping, DISTINCT, equivalence-requiring sets and partitions; exactly one NULL is not. UNION ALL requires no such equivalence capability. Nonnull values delegate to the bound family/collation. Tuple equality is componentwise. Hash collisions always require equality; equal keys must hash alike for the same seed. Equality-only implementations are permitted if the selected strategy needs no hash; equality does not imply order/hash support. No reflection, `driver.Value` comparison or string serialization fallback.

GroupNode initializes one private accumulator set per grouping occurrence; evaluates eligible aggregate FILTER, args, local DISTINCT/order under feature rules; transitions; finalizes typed StageValues; then HAVING filters group output. Aggregate-local DISTINCT/order is separate from query DISTINCT/order. `count(*)` counts occurrences; `count(expr)` counts nonnull values. Empty input with nonempty grouping keys has zero groups. Global aggregate, HAVING-induced single group, or empty grouping set `()` initializes **one** group even on empty input: count is 0, sum is NULL. Duplicate grouping sets produce duplicate group occurrences unless `GROUP BY DISTINCT` deduplicates the sets; `SELECT DISTINCT` instead deduplicates projected rows. Ungrouped-reference legality (including permitted functional dependencies) remains feature binding, not an arbitrary representative-row shortcut. CASE in final projection/HAVING does not suppress earlier failing aggregate arguments.

Across mixed grouping sets, GroupNode exposes one **fixed typed post-group key layout**. A present grouping expression supplies its actual group-key Value, including SQL NULL; an absent expression supplies a typed NULL of that slot's bound Usage, never an input/representative value. If a nonnullable input key can be absent, result nullability must be weakened accordingly. The execution retains current set membership and its grouping-set occurrence identity separately from key payloads. Identical duplicate sets have distinct occurrence identities/accumulators unless GROUP BY DISTINCT removes them; actual NULL and subtotal NULL cannot collapse merely because their displayed slots agree.

PG18-QUERY-GROUPING-SETS binds each `GROUPING(...)` argument to grouping-expression membership coordinates, not runtime argument evaluation. Its group-stage computation produces the INT4 mask (rightmost argument is the least-significant bit; absent argument contributes 1) from membership and installs it at bound StageValues coordinates. Existing BoundExpr stage-value evaluation reads that slot in eligible HAVING/projection/window/order consumers. Retain membership or its precomputed GROUPING slots through those consumers; never infer membership using `IsNull()` and never add another scalar evaluator. See [table expressions §7.2.4](https://www.postgresql.org/docs/18/queries-table-expressions.html#QUERIES-GROUPING-SETS) and [aggregate functions Table9.66](https://www.postgresql.org/docs/18/functions-aggregate.html).

## 5. Windows, DISTINCT, sets, order and CTEs

### Windows and frames

WindowNode reads only post-WHERE/post-group/post-HAVING input, appends typed stage values and preserves its input cardinality. NULL partition keys use query equivalence. Bound partition/order keys define peer groups; peers compare equal on **all** window order keys, not necessarily equal output rows. Window order is not final output order. Equivalent partition/order specifications share the same peer input ordering even when frames differ; different specifications may use different sort/buffer passes.

Frames have concrete ROWS (physical ordered occurrences), RANGE (order-value/peer boundaries) or GROUPS (peer-group counts) mode, bound start/end offsets and exclusion. Default with ORDER is RANGE UNBOUNDED PRECEDING through CURRENT ROW (including its last peer); without ORDER all rows are peers. ROWS offsets count rows; GROUPS offsets count peer groups; RANGE offset arithmetic and compatible sort-key/type restrictions come from the RANGE owner/type providers. EXCLUDE NO OTHERS keeps the frame; CURRENT ROW removes only current occurrence; GROUP removes current peers; TIES removes other current peers but retains current occurrence if in frame. Empty frames remain legal where feature rules allow. Offset validity, unsupported combinations, ranking versus frame-sensitive function semantics and exact diagnostics are separate frame owners (§10), not approximations here. A zero-row partition input produces zero rows.

### DISTINCT and sets

Ordinary SELECT DISTINCT retains one equivalent **projected** tuple with NULLs equivalent. DISTINCT ON retains the first occurrence of each key in sorted input; its expressions must satisfy PostgreSQL's leftmost ORDER BY rule. Without enough ordering the chosen occurrence is not deterministic.

All set binding validates equal widths, resolves corresponding columns' ordered CommonType, type/typmod metadata and collation derivation, and attaches operand coercions. Names derive from the left operand; origin/typmod/nullability metadata follows type and set analysis, not blindly copied physical NOT NULL. **UNION ALL is coercing bag concatenation within the same SetNode/result path**: it needs no tuple equality/hash/order family and no duplicate-count structure or count buffer. It can pull one operand then the other under parent demand without promising portable output order.

UNION ALL permits an unresolved/indeterminate common collation from conflicting **implicit** input collations; retain the type-owned derivation and source evidence. A later collation-sensitive order/comparison consumer owns the failure if it needs a usable collation. Conflicting **explicit** collations still fail under TYPE's derivation rules; this exception does not weaken them. UNION DISTINCT, INTERSECT and EXCEPT (including INTERSECT ALL/EXCEPT ALL) require a common usable collation where applicable and tuple equality plus only the other capabilities requested by their selected strategy. Their coercions precede equivalence/counting. UNION/EXCEPT associate left; INTERSECT binds tighter; parentheses override. The multiplicity formulas below specify outcomes; UNION ALL's m+n does not require computing equality buckets:

| Operator | ALL | Non-ALL |
|---|---:|---:|
| UNION | m+n | 1 if m+n>0 else 0 |
| INTERSECT | min(m,n) | 1 if m>0 and n>0 else 0 |
| EXCEPT | max(m−n,0) | 1 if m>0 and n=0 else 0 |

For operations requiring tuple equivalence, NULL components compare equivalent, unlike strict ON equality. UNION ALL simply retains every coerced occurrence, NULL or nonnull. Empty operands obey the formulas. Width/type and explicit-collation conflicts fail at analysis; common-usable-collation/key-family requirements apply only to consumers that need them, not blanket UNION ALL rejection. Never substitute runtime ad hoc comparison. The [official REL_18_0 analyzer, transformSetOperationTree](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/parser/analyze.c) independently corroborates UNION ALL's common-collation/grouping-operator exceptions.

### Ordering and demand

No ORDER BY promises portable row order. Ties on the complete ORDER key promise **no stable order** without a tiebreaker, even if an internal algorithm is stable. Each bound key has independent ASC/DESC and NULL placement: ASC defaults NULLS LAST, DESC defaults NULLS FIRST; explicit placement overrides direction defaults. Nonnull comparisons use type family/collation and propagate errors. Use an error-capable sorting implementation (for example a reusable-scratch merge sort returning error), not a bool comparator that suppresses provider/context errors. Hidden input-only keys never leak public metadata.

OFFSET skips before LIMIT counts. LIMIT NULL is unlimited; OFFSET NULL is zero; LIMIT 0 returns none; omitted FETCH count is one. Negative/invalid counts fail with owner-defined diagnostics. WITH TIES requires ORDER BY and, after offset/count, consumes all peers of the last retained complete key. Parent demand stops unneeded child work, but a sort/group/set/window blocker may necessarily consume more input. Demand is not blanket permission to reorder volatile calls or authorization barriers.

### CTE producer environment

CTEs are statement-local named typed producers, not catalog tables. A retained shared producer supplies immutable rows and independent positions to each consumer. For eligible side-effect-free SELECT, singly referenced CTE may fold; multiply referenced generally retains once; MATERIALIZED fences; NOT MATERIALIZED permits repeated computation and is ignored for recursive/non-side-effect-free cases as PostgreSQL specifies. A SELECT CTE is demand-sensitive, not universally drained on declaration. Release shared buffers only when all consumers and their retained views are done.

Recursive owner opens seed, then private working/intermediate bags per iteration, swapping after each pass. UNION removes repeats within a pass and against prior result; ALL preserves occurrences. Check cancellation on every pass and rejected/deduplicated traversal. SEARCH builds a sort column, not a promised traversal order. CYCLE builds mark/path and stops marked paths according to its owner. Demand-limited recursion must not attempt to drain an infinite child (§6).

DML CTEs execute once **to completion even unread**, share the statement snapshot, and communicate effects through RETURNING rather than sibling base-table rescans. Omitted RETURNING supplies no referenceable relation. Sibling action execution order is not promised; the outer command owns its own affected count. Scheduling these producers belongs to query orchestration, action/atomicity to DML/TX.

## 6. Materialization, ownership and release

**Settled precedence:** preserve EXPR's initial eager finite SELECT/no statement-fragment policy. QUERY-CORE may pull internally, but completes all permitted finite **final** output production before publishing RowResult. A later finite projection error returns no earlier public row. This document does not weaken the prerequisite.

This policy increases memory and first-row latency. Reuse immutable captured rows and nil-selection/indexed views for identity projections; do not copy an entire snapshot merely to expose it. Sort/group/window/distinct/set/reused CTE retain only required slots and state; no needless per-cell maps/descriptors/boxing. Initial capability does not authorize disk spilling or a physical optimizer.

**Eager final output is not eager every child/expression.** Internal Slice can stop a recursive child at five demanded rows and finalize those five before publication. Targets unrelated to sort/distinct/group can run only on retained limit rows. SELECT CTE consumers can partially demand a producer. A parent sort may genuinely need the whole child and fail to terminate on an infinite stream; the design does not claim to materialize infinite results. Later recursive/volatile/SRF/limit owners must implement required internal demand schedules. If public incremental output or weaker later-error atomicity becomes necessary, its owner must explicitly obtain/document extension or supersession against EXPR/QUERY/TX; it is not authorized silently here.

Execution-private state owns imported typed parameters, EvalFrames, StageValues, call/output scratch, source/provider/catalog pins, intermediate buffers and retained final output. It temporarily references Operation/StatementScope while executing; neither bound nodes nor final eager result retains an unfinished statement lease. Capture source rows/descriptor generation under authoritative synchronization, retain the selected view, then evaluate outside target locks. Do not hold engine locks over provider/external I/O. Preparation stores definitions, not execution snapshots; snapshot-requiring analysis follows TX conditional acquisition.

| Resource | Owner and release boundary |
|---|---|
| Frozen plan/description/provider handles | Prepared/direct plan retention under catalog validity; no mutable execution caches |
| Statement data view and write stage | StatementScope; `Finish` releases temporary scope pins/stage, not separately retained result pins |
| Child cursors/frames/accumulators/scratch | Private execution; close on EOF, failure, cancellation or demand stop; unwind partial open |
| Shared CTE buffers | Private producer; retain for live consumers/views, release at last dependent close |
| Final rows/source index views/catalog descriptors | RowResult separately retains everything needed before statement Finish/Operation.Done; releases on EOF/error/Close |
| Exported Go values | Consumer owns values; no Release API; mutable exported bytes detach, never borrow result resources |

Immutable scalar/string/payload sharing is safe internally. `ExportValue` detaches mutable byte payloads from internal Values **and from another export/consumer**. Saved exported values remain stable after Next, cancellation, Close, another execution, append and Reset/Restore. The consumer must copy entries it wants to save from a deliberately reused destination slice; that slice itself is allowed to change on the next call. Old metadata and captured values belong to the old retained lifetime; replacement installs new lifetimes, never mutates them.

Internal logical-stage fixtures must retain genuine admitted statement authority until stage consumption ends. An implicit SELECT also reserves the catalog lifetime: a synchronous Reset/Restore cannot run while that fixture admission remains live. Replacement-before-consumption witnesses therefore use a real explicit transaction admission, keeping immutable captured rows consumable while separately requiring old physical grant identities to fail with `ErrCatalogReplaced`. Detached-result witnesses instead close their source and finish the fixture admission before replacement; result metadata and values remain independently retained. Fixture cleanup owns source closure, statement completion and operation/session teardown in that order, idempotently; no production replacement fence is relaxed.

Root adapters must close discarded results in **ExecContext** (including prepared Exec and any RETURNING relation discarded by Exec), Query adapter construction failures, and all error paths after ownership transfer. Query transfers sole cursor ownership to rows. EOF/error automatically releases execution resources; immutable schema remains available for the result object's metadata lifetime. Explicit Close remains idempotent and returns its stored cleanup result. Already exported values require no pin and cannot be invalidated by cleanup.

## 7. State transitions, cancellation and errors

| State/event | Transition and effects |
|---|---|
| Raw analysis | Private layouts/expressions only; failure yields no plan/description |
| Successful whole finalization | Frozen bound plan; catalog validity checked/reanalyzed at prepared use, never execute stale pointers |
| Open | Execution-private source captures/pins/children; failure unwinds acquired resources |
| Producing | Internal pull/stage materialization; no initial public rows |
| Completed production | Retain final output/schema independently, close no-longer-needed children; prepublication/context and statement completion checks |
| Successful Finish and lease completion | Publish common RowResult; public iteration is over finalized retained output |
| Production/Finish failure or prepublication cancellation | Terminal failure; discard unexposed output, complete scope with error, release pins; no public finite fragment |
| Public Next reaches EOF | Terminal exhausted; release pins/scratch, keep immutable metadata; later Next EOF |
| Public Next detects cancellation/error | Terminal failed; return originating error, release resources; subsequent Next returns stored terminal error until Close |
| Close | Terminal closed; cleanup once, later Next EOF; repeated Close returns stored cleanup result |

Check cancellation at admission/analysis/eval entry, scans **including rejected rows**, join pair loops, aggregate transitions/finalization, window buffering/frame traversals, set counting/deduplication, sort comparisons/materialization, CTE/recursive passes, before/after providers and before publication. Emitted-row-only checks cannot cancel a nonproductive traversal. Context comes from the same admitted Operation while executing; finalized rows uses the query iteration cancellation context without retaining the completed lease. Future session-owned named portals use SESSION cursor lifetime, not this eager cursor as a new session registry.

Before publication, cancellation discards output and returns the original ordinary context error. After first public row, cancellation ends further iteration but does not alter saved values. No automatic retries, error-to-NULL, provider suppression or invented SQLSTATE. Cooperative provider cancellation is required; blocking external I/O cannot be forcibly interrupted.

Keep originating bind/eval/provider/context/Finish errors primary. On an already failed path, child Close/resource cleanup errors cannot replace or change that diagnostic; preserve cleanup evidence separately where available. On an otherwise successful path, a fallible required child close or Finish error prevents publication and becomes the primary error. Explicit result Close returns cleanup error if any; cleanup still visits all acquired resources exactly once. Scope completion is mandatory/effect-idempotent on every admitted path; Operation.Done follows it.

Reuse existing `internal/sqlstate` and root Error alias/full diagnostic adapters. Preserve English primary, SQLSTATE, detail/hint and available release-defined Position/InternalPosition/InternalQuery/Context/object fields; do not blanket-add Schema/Table/Column or fabricate PostgreSQL C File/Line/Routine. Context/I/O/adapter/invariant failures remain ordinary errors. Retain SQL and AST occurrence evidence: source Offset is zero-based bytes, absent synthesized position is −1, public Position is one-based **characters**, with existing Position.Character respected. Bind failure occurs before scan even if input empty. Optional fields are populated only where the matching PostgreSQL diagnostic supplies them (§11 W9).

## 8. DML RETURNING and transaction separation

INSERT/UPDATE/DELETE/MERGE producers own actions, affected counts, row selection, defaults, constraints, triggers, conflicts/rewrite behavior and supplied OLD/NEW/source/target frames. Query binds RETURNING with explicit Returning ExprPolicy over that producer namespace and evaluates one output occurrence for each **producer-authorized** returning occurrence. It cannot infer a universal missing OLD/NEW rule, invent conflict/no-op rows or choose MERGE actions.

INSERT ordinarily exposes inserted/default/trigger-final rows, UPDATE new rows, DELETE deleted rows, MERGE only its producing actions and source/modified-target semantics. ON CONFLICT DO NOTHING contributes no conflict row; rejected conflict UPDATE WHERE contributes none; MERGE NOTHING contributes no returned action. OLD/NEW exceptional conflict/rewrite semantics remain producer-owned. Zero affected/returned rows still yields RETURNING's typed schema. INSERT SELECT consumes the same typed source and destination coercions under write ownership, not another store.

Protocol against TX's existing `StatementScope.Writes()` (the WriteStage accessor) and `Finish`:

1. Admit the same Operation/scope, bind/revalidate whole statement, acquire required view, import parameters.
2. Producer chooses actions and stages writes; query evaluates RETURNING over final producer frames into **unpublished** common typed output. DML CTE producers must complete even with no consumer.
3. On action/RETURNING error or cancellation, call `Finish(err)` and discard both output and failed write stage under TX. Preserve originating diagnostics and TX explicit-abort/savepoint rules; query does not implement them.
4. On successful production, obtain any independent output pin needed beyond the statement; perform cancellation/precompletion checks; call `Finish(nil)` and honor its result. Failure discards output. Success merges private writes for explicit TX or publishes for implicit TX as TX defines; it is not synonymous with explicit commit.
5. Release scope references/Operation via their owners, then expose the same RowResult. Count remains producer-defined. No fallible query/provider work is deferred to cursor-driven write publication.

Closing RETURNING early releases output only: it neither commits, rolls back, chooses actions nor delays completion. Subsequent connection operations cannot trigger leftover partial producer work. A later transaction rollback may discard private writes but cannot mutate values already exported; result retention is not permission to publish rolled-back writes. Post-Finish iteration cancellation does not undo completed actions; precompletion cancellation is handled by TX. No query-owned retry, publication token or parallel result implementation.

## 9. Clean-cutover inventory

Paths/line ranges are ordinary current-source anchors, not frozen proof. Future owners must search/reread every actual caller in their execution session and migrate its tests/docs in the same bounded cutover. This design edits none of them.

| Current seam | Replacement and retained authority |
|---|---|
| `internal/engine/engine.go:24–42` ParameterInfo/Result/binding | Type-owned parameter/description shape, catalog BoundPlan carrying BoundQuery; replace Result Snapshot/parser metadata with common RowResult. Remove migrated projection/predicate/order fields; nonquery target binding remains its owner until migrated |
| `engine.go:130–138,197–340` Engine.Describe/describe; SELECT :290–329 | One statement analysis/private expressionBinder, catalog source schemas and typed layouts. Remove migrated local `column`/raw parameter inference paths; preserve shared lookup/storage identities/finalization. SESSION Operation signature remains authoritative |
| `engine.go:482–634` Execute; SELECT :612–630 | Replace SELECT switch body with bound tree open/demand-sensitive internal execution/finite final materialization. Capture immutable rows/generation coherently, evaluate outside locks. Remove legacy branch, not keep two SELECT implementations |
| `engine.go:579–611` INSERT staging | Preserve existing complete-row staging/atomicity until DML/TX owner migrates; RETURNING attaches only through real producer seam. UPDATE/DELETE/MERGE are future entrypoints, not present APIs |
| `internal/engine/snapshot.go:9–90` Snapshot/Len/Values/compare/selectRows | Replace public single-table filter/project/sort path with typed sources/stages/type keys; retain useful immutable capture/nil-selection/index view internally. Delete migrated driver.Value comparator and obsolete callers; no alternate public Snapshot result |
| `conn.go:51–110` PrepareContext/execute/ExecContext/QueryContext | Retain original SQL/AST/description/bound plan; direct/prepared converge on existing conn.execute/newRows. Catalog owns validity/fixed shape. Exec closes discarded Rows, Query transfers ownership; no preparation snapshots |
| `stmt.go:11–55` stmt/NumInput/ExecContext/QueryContext | Frozen parameter count/description with replaceable valid binding, independent execution state; use same adapters and discarded-result cleanup, not prepared-only binder |
| `rows.go:12–60` rows/newRows/Columns/Next/Close/metadata | Common RowResult/ColumnDescriptor, one typed scratch, ExportValue, correct EOF/context/idempotent cleanup; remove parser.Column/Snapshot fields and primitive metadata switch |
| `result.go` command adapter | Retain affected-count adapter, no query-stage write count inference |
| Expression occurrences/parameterColumn/Predicate | Route all migrated query resolution/evaluation through EXPR-CORE and shared type parameter plans. Parser/general syntax belongs to its dedicated owner; never edit generated grammar directly |
| INSERT/UPDATE/DELETE/MERGE RETURNING producers | Bind producer namespace, stage expression rows, Finish under TX, attach common result; no query action/TX subsystem |
| `reference/fixture/fixture.go` fixture.Diagnostic/fixture.ExtractDiagnostic/fixture.AssertDiagnostic | Reuse complete-field independent fixtures, not alternate error envelope or SQLSTATE-only parity |
| `driver_test.go` Test sections around :270,:344,:382,:467 | Expand existing metadata, cancellation/closed object, native cursor and retained snapshot witness seams; baseline tests are not future-feature evidence |
| `internal/engine/engine_test.go:28–46` run/tuples | Migrate direct Execute signature through its responsible owner; replace Result.Columns/Snapshot.Len/Values iteration with schema-width typed Next/export and explicit cleanup. No test-only legacy result shim |
| `internal/engine/catalog_test.go:29,47,189,294` direct Engine.Execute callers | Migrate internal signature/admission and discarded-result cleanup with the responsible owner; retain catalog identity/replacement/concurrency assertions |
| `internal/engine/control.go:181` Restore staged Execute caller | Migrate internal execution signature/cleanup under control/SESSION/TX owners while preserving accepted CREATE/INSERT scripts, parameter rejection and atomic staged catalog replacement. QUERY-CORE does not broaden Restore syntax |
| `control_test.go:141–188` TestSnapshotSurvivesCatalogReplacement | Retain public native append/Reset/Restore witness and old cursor values/metadata; use common result semantics without weakening accepted native-control behavior |

Migration searches over Go/grammar must include `binding`, `selectRows`, `Snapshot`, `.Values(`, `.describe(`, `.Describe(`, `.Execute(`, `newRows`, `parser.Column`, `parameterColumn`, `Predicate` and Result consumers (including Exec-discard paths). Inspect actual matches, remove obsolete aliases/reexports/dual paths in migrated scope. Retain catalog/native Reset/Dump/Restore storage/publication authority and existing supported behavior. No prerequisite edits or feature implementation are part of this document.

## 10. Bounded downstream ownership

These are exact observed catalog symbols, **not invented prerequisite edges**. Each implementation session must deliver independently testable public behavior, unit contracts, direct/prepared reference expectations and feature smoke, not an inert scaffold. Designs do not mark these capabilities implemented.

| Exact owner | Bounded deliverable; witness families |
|---|---|
| PG18-QUERY-CORE | After implemented dedicated prerequisites, complete public current single-source filter/projection/baseline order cutover to typed tree/result; W1/W3/W8/W9 |
| PG18-QUERY-ALIASES-DISTINCT | Names, labels, alias visibility/metadata; W1/W9 |
| PG18-QUERY-ALIASES-DISTINCT-DISTINCT | Projected NULL-aware duplicate elimination; W4 |
| PG18-QUERY-ALIASES-DISTINCT-DISTINCT-ON | Sorted first-per-key and leftmost-key legality; W3/W4 |
| PG18-QUERY-JOINS; PG18-QUERY-OUTER-JOINS | Inner/cross then LEFT/RIGHT/FULL, each independently witnessed within actual owners; W2 |
| PG18-QUERY-AGGREGATE; PG18-QUERY-AGGREGATE-MODIFIERS; PG18-QUERY-GROUPING-SETS | Basic accumulators/empty groups; FILTER/local DISTINCT/order; grouping-set multiplicities; W4 |
| PG18-QUERY-WINDOW | Post-HAVING partitions/ranking/stage slots; W6 |
| PG18-QUERY-WINDOW-ROWS; PG18-QUERY-WINDOW-RANGE; PG18-QUERY-WINDOW-GROUPS; PG18-QUERY-WINDOW-EXCLUDE | Separate frame/peer/offset/exclusion implementations and diagnostics; W6 |
| PG18-QUERY-SET-OPS | Common-type coercions, widths, precedence and tuple counts; W5 |
| PG18-QUERY-SUBQUERY | Legal correlation, scalar width/cardinality; W7/W9 |
| PG18-QUERY-FROM-VALUES; PG18-QUERY-SRF-FROM | Typed derived sources/duplicates; legal lateral and SRF scheduling; W1/W7 |
| PG18-QUERY-CTE; PG18-QUERY-CTE-DML | SELECT producer folding/shared/demand; once-complete DML snapshot/RETURNING; W7/W10 |
| PG18-QUERY-CTE-RECURSIVE; PG18-QUERY-CTE-RECURSIVE-SEARCH; PG18-QUERY-CTE-RECURSIVE-CYCLE | Working/intermediate bags/demand/cancel; constructed sort column; cycle mark/path stopping; W7 |
| PG18-QUERY-ORDER-LIMIT; PG18-QUERY-ORDER-LIMIT-LIMIT; PG18-QUERY-ORDER-LIMIT-TIES | Expression order/keys; offset/limit/fetch/scheduling; final-key peers; W3 |
| PG18-DML-INSERT-SELECT | Shared source and contextual destination coercion under write owner; W1/W10 |
| PG18-DML-INSERT-RETURNING; PG18-DML-UPDATE-RETURNING; PG18-DML-DELETE-RETURNING; PG18-DML-MERGE-RETURNING | Each real producer's exact rows/actions/counts and common staged result; W10 |
| PG18-QUERY-LOCKING; PG18-QUERY-FDW-SCAN; PG18-QUERY-PARALLEL | Later algorithms must preserve type/result/retention/cancellation and owner boundaries; no second binder/store. Feature witnesses come from their own records, not implementation authorization here |

PG18-EXPR-CORE supplies expression execution; PG18-CAT-CORE supplies existing lookup. This map does not absorb action/core DML semantics or create LEFT/RIGHT/FULL or advanced-callback catalog symbols.

## 11. Independently expected future witnesses

**Specifications only; none were executed for this design.** Future owners assert predetermined outcomes against unit tests and each backend separately, then parity, using direct/prepared database/sql when supported. Compare unordered bags with multiplicities; exact presentation requires a unique complete ORDER key. Every family includes positive, boundary, failure and interleaving requirements. Native Go ownership/invariant tests are not proved by PostgreSQL SQL parity. Exact new diagnostics need pinned-runtime field capture in the respective implementation session; do not guess wording or optional fields.

### W1 — Sources, filters, projection and metadata

- t(id,v)={(1,1),(2,NULL),(3,2)}: `SELECT id,v=1 FROM t ORDER BY id` expects (1,true),(2,NULL),(3,false), INT4/BOOL; `WHERE v=1` retains id1 only. Base id,v metadata is INT4/INT4; nullable BOOL stays typed.
- No-FROM `SELECT 7` yields one INT4 row; same constant over actual empty input yields none. Zero-width unit cursors emit one present occurrence and EOF separately; zero-width projections preserve duplicate source occurrences.
- Bind missing/non-BOOL predicate before scanning, not truthy runtime conversion. Alias hides original qualifier; star expands bound schema once; hidden sort keys add no columns. VALUES duplicates remain occurrences; FROM-SRF producer outputs use the same typed schema, with feature-owned legal lateral visibility.
- Concurrent/repeated prepared executions with different parameters and inputs cannot share frames/scratch/results. Interleave captured input with append/Reset/Restore without changing old values/metadata. Units assert every boundary width/usage and no extra copies for identity views.

### W2 — Join multiplicity and extension

L(k)={1,2,NULL}; R(k,v)={(1,'a'),(1,'b'),(3,'c'),(NULL,'n')} with ON L.k=R.k:

- LEFT bag={(1,1,'a'),(1,1,'b'),(2,NULL,NULL),(NULL,NULL,NULL)}.
- RIGHT bag={(1,1,'a'),(1,1,'b'),(NULL,3,'c'),(NULL,NULL,'n')}.
- FULL bag contains the two matches, two unmatched-left and two unmatched-right occurrences (six). NULL=NULL never matches.
- Replacing both matching right payloads by 'a' yields **two identical output matches**. Duplicating a preserved unmatched left occurrence yields two separate extensions, one each.
- LEFT followed by WHERE R.v='a' retains only the a match; placing that condition in ON retains the a match plus left2/leftNULL extension. Empty-side LEFT/RIGHT/FULL yields exactly one extension per preserved occurrence; both empty yields none; cross cardinality is product.
- FULL USING(k) exposes merged k followed by remainders and retains unmatched keys2/3/NULL with correct multiplicity, coercions and widened nullability. Ambiguous unqualified k binds 42702 with independent complete English/location fixture.
- Cancel inside zero-match pair traversal; concurrent outer frames/occurrence bits remain isolated. Captured sources survive append/reset; failure before finalization exposes no partial public join.

### W3 — Order, DISTINCT ON and counts

- No-order comparison asserts bags only. Complete-key ties allow peer permutation; add unique id to assert exact order. ASC NULL last/DESC NULL first, explicit opposite placements and mixed key direction are independent. Input-only hidden order key never appears in metadata.
- LIMIT0/offset past end empty; NULL LIMIT unlimited, NULL OFFSET0; FETCH omitted count1. Keys1,1,2 with FETCH FIRST1 WITH TIES retains both1. Offset applies before tie count; ties use all final keys.
- DISTINCT ON with compatible leftmost order and unique last key chooses its specified first row; with no unique tiebreaker do not pin winner. Bind negative counts, missing ties order and leftmost mismatch with owner-captured full diagnostics.
- `SELECT 10/v FROM branch_t ORDER BY id LIMIT 1`, input (id,v)=(1,2),(2,0), expects5 without evaluating second target division. An order/distinct-needed expression must instead run at its dependency phase; target SRFs have the post-sort/pre-limit boundary.
- Unit comparator sentinel error propagates unchanged; cancellation within sort/materialization yields no finite public fragment. Concurrent sorting never shares keys/scratch; interleaving exports preserves prior values. Do not claim baseline stable sort as SQL tie guarantee.

### W4 — Groups and duplicate elimination

- Empty keyed group gives zero rows; empty global `count(*),sum(v)` and GROUP BY() give one (0,NULL); HAVING NULL removes that group. HAVING-induced grouping is likewise one group before filtering.
- Input1,1,NULL,NULL: count*=4, count(v)=2, count(DISTINCT v)=1; SELECT DISTINCT bag{1,NULL}. `GROUP BY GROUPING SETS ((v),(v))` produces two occurrences of each group unless GROUP BY DISTINCT removes repeated sets; SELECT DISTINCT is a later row operation.
- Mixed-set fixture under PG18-QUERY-GROUPING-SETS: t(v)={(1),(NULL)}, `SELECT v,count(*),GROUPING(v) FROM t GROUP BY GROUPING SETS ((v),())` expects unordered bag{(1,1,0),(NULL,1,0),(NULL,2,1)}, usages INT4/INT8/INT4. Actual grouped NULL has membership bit0; absent-key subtotal NULL has bit1. The fixed schema does not vary by row. Separately use a NOT NULL input key omitted by () and assert widened result nullability.
- Units assert membership/occurrence identity separately from payload NULLness, GROUPING argument coordinates and rightmost-bit convention, typed absent-key slots and existing StageValues lookup. Duplicate set identities remain independent; concurrent/canceled mixed-set executions cannot leak membership into HAVING/window/projection consumers.
- FILTER retains only True; ordered aggregates receive independently specified local ordering, not query order. Prior CASE cannot hide a failed aggregate argument. Invalid grouped reference/nesting binds with full feature diagnostics.
- Units assert initialized/finalized typed slots and NULL key policy; cancellation inside transition/retained local order no result fragment; concurrent executions/duplicate grouping occurrences isolate accumulators. Window over grouped result sees groups, not original rows.

### W5 — Sets and common types

A={1×3,NULL×2}; B={1×2,2×1,NULL×1}:

- UNION ALL={1×5,2×1,NULL×3}; UNION={1,2,NULL}; INTERSECT ALL={1×2,NULL×1}; INTERSECT={1,NULL}; EXCEPT ALL={1×1,NULL×1}; EXCEPT distinct=empty.
- Units distinguish INTERSECT precedence, left UNION/EXCEPT association and operand-local versus final ORDER/LIMIT. Width/type/explicit-collation failures bind atomically; usable-common-collation/key-hook failures apply to the operations requiring those capabilities, with full owner fixtures. Equality/hash collisions and comparator sentinel errors never silently compare interface values.
- Unit type with a valid immutable Value but no key hooks: UNION ALL succeeds with two occurrences and binds/allocates no equality/hash/order/count state; UNION DISTINCT or INTERSECT/EXCEPT requiring equality fails at its binding boundary. When JSON/cast syntax owners are available, `SELECT '{}'::json UNION ALL SELECT '{}'::json` independently expects two JSON occurrences; its non-ALL counterpart requires owner-captured complete diagnostics, not guessed wording.
- Unit/source fixture with differing implicit collations and compatible text types: UNION ALL returns both rows with permitted indeterminate output derivation; a later collation-sensitive consumer fails at its proper boundary. Under the collation owner, create `uc(v text COLLATE "C")` and `up(v text COLLATE "POSIX")`, each containing 'a': `SELECT v FROM uc UNION ALL SELECT v FROM up` expects bag{'a'×2}; appending ORDER BY v requires usable collation and must fail with independently captured complete diagnostics. Column references supply implicit collations even though declarations name them explicitly. Distinct explicit expression COLLATE clauses still fail under TYPE's conflict rules, including for UNION ALL.
- Empty operands exercise all operators/ALL forms. INT4/INT8 operands use ordered common-type coercions and expected metadata; coercions precede equality/counting where required, while UNION ALL concatenates without those structures. NULL equivalence for counting differs from ON.
- Concurrent or canceled set buffers/counts remain isolated; later failure produces no initial finite public fragment; retained previous exports survive cleanup/replacement.

### W6 — Windows and frames

- (id,k)=(1,1),(2,1),(3,2), window ORDER k: rank=1,1,3, dense_rank=1,1,2; row_number peer assignment not pinned without id. Final ORDER id controls presentation, not window peer identity.
- For sum(k), default RANGE ORDER k gives2,2,4; ROWS ORDER k,id UNBOUNDED PRECEDING through CURRENT ROW gives1,2,4. GROUPS ORDER k CURRENT ROW through CURRENT ROW gives2,2,2. RANGE ORDER k CURRENT ROW through CURRENT ROW likewise sums peers2,2,2; RANGE offset cases require their compatible-type owner.
- With RANGE ORDER k UNBOUNDED PRECEDING through CURRENT ROW, EXCLUDE CURRENT ROW sum(k)=1,1,2; EXCLUDE GROUP=NULL,NULL,2; EXCLUDE TIES=1,1,4; NO OTHERS=2,2,4. Assert function return usages as aggregate/type rules specify, not merely numeric parity.
- WHERE removes before window; GROUP/HAVING outputs feed windows; empty input none. Units equivalent order specs share peer sequence even with different frames; distinguish physical ROWS and peer GROUPS boundaries, empty frames and invalid offsets.
- Window in WHERE/illegal nesting fails with full diagnostics. Cancel partition/frame buffering before publication; parallel executions isolate StageValues and retained partition scratch. Separate specs cannot accidentally overwrite one another's slots.

### W7 — Subqueries, lateral and CTE producers

- Scalar child over R for outer k1 fails on second row: expected21000, `more than one row returned by a subquery used as an expression`; available other fields captured independently. For k2 yields typed NULL. Units cover0/1/2 occurrences and scalar width2 binding failure.
- Legal LATERAL two matches plus LEFT unmatched preserves multiplicity; non-LATERAL sibling fails W9. Qualified local alias missing column cannot fall back to same-named outer alias. Different outer values/parameters in concurrent executions never cross-contaminate; child error/cancel unwinds current outer pins.
- MATERIALIZED two consumers have independent positions, immutable shared rows, producer once. Fold/NOT MATERIALIZED follows side-effect-free/reference-count rules; volatile producer must not be folded into repeated effects. Units attach predetermined invocation behavior to the explicit materialization policy, not unspecified ordinary scalar evaluation order.
- Finite recursive n1..3 bag{1,2,3}; duplicate seed `1,1` with recursive termination at1 yields one row for UNION and two for ALL. Unbounded increment recursion under parent LIMIT5 expects1..5 via demand stop, not infinite draining; cancel nonproductive passes even with no emitted rows.
- SEARCH DEPTH FIRST on an acyclic tree rooted1 with children2,3 and child4 under2, explicitly ORDER BY constructed search column, yields1,2,4,3; BREADTH FIRST with deterministic key yields1,2,3,4. No ORDER promises traversal. CYCLE on path1→2→1 marks the repeated1 and stops extending that path; independent noncycle branch continues. SEARCH/CYCLE owner fixes exact constructed column types/path representations independently.
- Concurrent CTE/recursive execution states and consumer offsets remain separate; failed/canceled iteration frees working/intermediate bags. Unread SELECT CTE demand and unread DML CTE completion are distinct (§8/W10).

### W8 — Ownership, cancellation and release

- Save copied exported row entries, advance, cancel, Close twice, execute another cursor, append/Reset/Restore: saved payloads and metadata unchanged. Future exported []byte must not alias internal values or a second consumer's bytes. Reused destination slice itself is not promised stable.
- Pre-canceled admission, production cancellation and prepublication cancellation expect context.Canceled and no finite public result. Cancel after first public Next returns terminal context.Canceled while saved row stays unchanged; Close then Next is EOF.
- Unit later finite projection sentinel error yields no first-row fragment; open/child-close/Finish failures preserve primary errors and unwind every pin exactly once. EOF frees resources; metadata remains usable; Exec discards/closes row relations. Prepared parallel executions share only immutable plan/descriptors, not frames/stages.
- Native buffer lifetimes are unit/public Go assertions, not SQL reference evidence. Reference atomicity fixtures test actual statement effects separately.

### W9 — Binding diagnostics and migration

Reuse prerequisite independent fixtures, with ERROR severity/unlocalized severity and absent optional fields except those stated:

- `SELECT missing FROM expr_t`:42703, `column "missing" does not exist`, Position8.
- `SELECT /*é*/ missing FROM expr_t`: same primary/SQLSTATE, Position14, not byte15; detail/hint/context/object/internal fields absent for these fixtures.
- `SELECT id FROM expr_t WHERE missing = 1`:42703, same primary, responsible reference Position28; binding fails even when expr_t empty. Future owner independently confirms all available fields, not only code.
- `SELECT o.id FROM expr_t AS o, (SELECT v) AS s`:42703, `column "v" does not exist`, Position39, Detail `There is a column named "v" in table "o", but it cannot be referenced from this part of the query.`, Hint `To reference that column, you must mark this subquery with LATERAL.`; other optional fields absent. This preserves the prerequisite fixture; runtime capture remains future evidence.
- Ambiguous column/alias/bad qualifier failures use their distinct owner diagnostics; qualified shadowing unit forbids fallback. Binding failure publishes no plan/description. Context/invariant errors stay ordinary; no blanket object fields or fake C source locations.
- Interleave schema replacement with prepared validity/reanalysis: no stale execution or mutation of retained old result metadata; incompatible fixed shape retains catalog's0A000/`cached plan must not change result type`. Inspect every §9 caller and reuse full-field reference helpers, not SQLSTATE-only parity.

### W10 — Producer-defined RETURNING and write consumers

- INSERT two producer-defined rows returns exactly their inserted/default/trigger-final values and affected count2; UPDATE selected rows returns new values, DELETE deleted values. No-match produces zero rows with schema. MERGE NOTHING has no returned action. ON CONFLICT DO NOTHING/rejected conflict UPDATE WHERE have no conflict returned row.
- Producer-supplied OLD/NEW/source/target fixtures cover ordinary missing sides and conflict/rewrite exceptions; query must not synthesize them. INSERT SELECT consumes typed duplicates and contextual coercions under write owner, with late source/conversion failure publishing no write fragment.
- Later RETURNING evaluation error/cancel discards staged writes and every return fragment; explicit TX state follows TX rather than cursor rules. Finish is effect-once; its error prevents result exposure. Closing early then another operation cannot cause delayed/partial action publication; cancel after successful Finish does not undo it.
- Unread DML CTE completes once. In `WITH changed AS (UPDATE t SET v=v+1 RETURNING v) SELECT v FROM t`, main base-table reads original statement-snapshot v; reading changed instead sees returned incremented v. Outer count is outer command's count, not sum of sibling writes. Sibling execution order is not asserted.
- Unit common RowResult/schema/export path and producer count separation; concurrent producers/RETURNING buffers isolate frames. Transaction rollback cannot mutate already exported values; retention cannot publish rolled-back private rows.

## 12. Verification, limitations and sources

This design adds **no executable feature smoke** (`smoke_commands=[]`). Do not run unsupported future witness SQL against the baseline to pretend validation. No build/test/formatter or SQL witness was run by the document implementer. The root owns complete verification and independent design review after edits finish:

```sh
bash /home/rlew/projects/stuntgres/stuntgres/.omp/bin/implement-part.sh verify PG18-ARCH-QUERY-EXECUTION --repo /home/rlew/projects/stuntgres/stuntgres
```

The utility runs in the assigned worktree and records full gates. Existing public smoke is `make quickcheck` from that worktree and must retain its existing assertions; it proves no future join, window, recursive or RETURNING behavior. Docker/image/network failures are failures, never skips/mocks. No verification result is asserted here.

Independent design review must check every acceptance mapping, declarations/layout invariants, no eager/demand contradiction, all migrated callers including discarded results, producer/TX separation, exact owners and complete independently specified future witnesses. Future owners capture exact additional English/SQLSTATE/location fields against pinned runtime before claiming feature acceptance. This is an evidence obligation, not missing permission to invent fields. No material design question remains: initial eager finite/no-fragment precedence is preserved; public streaming supersession, if needed, is a later explicit design decision.

Repository grounding: expression artifact §§2–6, type artifact §§2,5–7, catalog §§2–7, session §3, TX §§2–3, and the actual current code ranges in §9. Official linked release pages above, especially SELECT's SELECT-list scheduling, sets/order/limit; table expressions §§7.2.1–7.2.5; select lists §7.3; expressions §§4.2.7–4.2.14; WITH §§7.8.2–7.8.4; RETURNING §6.4, define semantic expectations. Queries overview: [chapter7](https://www.postgresql.org/docs/18/queries.html).

Research-only local aids: PostgreSQL `src/backend/executor/nodeNestloop.c:74,150–218` separates ON match bookkeeping from post-join quals; `nodeWindowAgg.c:6–22` illustrates partition buffering; `nodeSetOp.c:175,531` and `nodeLimit.c:48` illustrate interrupt checks. Local 20devel does not establish PG18 diagnostics or force the same physical algorithms. ramsql executor token/sorter/lexical-limit machinery is not reused as a PG contract. No future SQL outcome in §11 is claimed observed.
