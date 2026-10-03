# PG18-ARCH-EXPRESSION-ANALYSIS — Expression analysis and evaluation contract

## 1. Decision, scope, and authority

This is the completed **design decision artifact**, not an executable expression foundation. Adopt one raw-expression representation in `internal/parser` and one immutable, typed bound-expression seam in the existing `internal/engine` statement binder and executor. All signatures below are proposed internal contracts. No SQL, unit/reference tests, builds, formatters, or smoke commands were executed for this design. The frozen plan declares `smoke_commands=[]`; future executable consumers must supply actual feature smoke, not baseline-only evidence.

This decision covers scalar expression analysis and evaluation interfaces, references, scopes, contexts, NULL, evaluation guarantees, dependencies, and diagnostics. Individual syntax, operators/functions, casts, aggregates/windows, subqueries, and query clauses remain separate implementation capabilities. No production code, prerequisite, catalog record/status, or adjacent design is changed by this artifact. Production dependencies remain standard library/module only.

Binding prerequisites, whose completed design status does **not** imply implemented APIs:

* [Catalog identity design](PG18-ARCH-CATALOG-IDENTITY.md), §§2–4 and 6–7: engine/epoch/object ownership, normalized names, coherent retained `CatalogRead`, candidate discovery, `PlanDependency`, prepared validity/reanalysis, and publication.
* [Type-system design](PG18-ARCH-TYPE-SYSTEM.md), §§2–9: `sqltype.Usage`, immutable nullable `sqltype.Value`, raw unknown handling, coercion/call plans, parameter constraints, comparison/key families, metadata, adapters, and cutover ownership.

Use the type design's `ColumnDescriptor`/`StatementDescription` in the catalog design's illustrative `BoundPlan` result slots; do not preserve `[]parser.Column` as a second resolved metadata model. Diagnostic transport is shared `internal/sqlstate`, extended by **PG18-DRIVER-ERROR-FIELDS**, not expression-owned.

### 1.1 Version and source policy

Normative semantics are the official PostgreSQL `/docs/18/` manual, whose research navigation identifies **18.6**. The real reference stays **PostgreSQL 18.0**, UTF8/C, pinned to:

```
postgres:18.0-bookworm@sha256:3f55f8895c4ed50603e2fbdfc72fffeeaba3173321fee5cb825bbbeb30d9d854
```

Only **PG18-REF-ORACLE-18-6** owns minor-specific oracle alignment. Local PostgreSQL 20devel is research-only, never the release oracle. Future reference outcomes below are independently specified expectations, not observed executions or parity claims.

Normative links: [value expressions §4.2](https://www.postgresql.org/docs/18/sql-expressions.html), [evaluation §4.2.14](https://www.postgresql.org/docs/18/sql-expressions.html#SYNTAX-EXPRESS-EVAL), [query scopes and WHERE §7.2](https://www.postgresql.org/docs/18/queries-table-expressions.html), [logical truth §9.1](https://www.postgresql.org/docs/18/functions-logical.html), [conditionals §9.18](https://www.postgresql.org/docs/18/functions-conditional.html), [volatility §36.7](https://www.postgresql.org/docs/18/xfunc-volatility.html), [conversion §10.1](https://www.postgresql.org/docs/18/typeconv-overview.html), [assignment §10.4](https://www.postgresql.org/docs/18/typeconv-query.html), and [error fields §54.8](https://www.postgresql.org/docs/18/protocol-error-fields.html).

### 1.2 Acceptance map

| Selected acceptance | Decision | Independent future witnesses |
|---|---|---|
| A1 raw/bound, typed result, error/location | §§2, 5 | W1–W3, W5 |
| A2 resolution, rows/parameters, correlation | §§3–4 | W1, W3–W4, W6 |
| A3 context, ownership, NULL/truth | §§4, 6 | W2, W6–W7 |
| A4 catalog/type delegation | §§2–4 | W1, W5, W7 |
| A5 volatility, skipping/order, cancellation | §§6–7 | W5–W6 |
| A6 extension and clean cutover | §§8–9 | W7 |
| A7 bounded backlog consumers | §10 | W1–W7 and owner exit witnesses |

## 2. Raw syntax → finalized immutable expression

### 2.1 Parser-owned raw contract

The following logical declarations belong in the existing parser package when their consumer implements them:

```go
type Expr interface {
    expression()
    SourcePosition() Position
}
type LiteralExpr struct { Value Value }
type ColumnExpr struct { Name ObjectName; Pos Position }
type ParameterExpr struct { Number int; Pos Position }
type OperatorExpr struct { Name ObjectName; Args []Expr; Pos Position }
type FunctionExpr struct { Name ObjectName; Args []Expr; Pos Position }
```

The package-private marker closes the set; new syntax adds concrete parser kinds and grammar productions, not a public string/`any` expression registry. `ObjectName`/`Identifier` reuse the catalog parser contract: individual normalized parts and positions, normalization exactly once. Raw `Value` denotes literal syntax only, never runtime storage or a resolved type. `LiteralExpr.SourcePosition` returns its literal token's position; other nodes return `Pos`. Parentheses preserve child occurrence positions.

`ParameterExpr` is the sole generalized parameter occurrence. During migration, grammar emits it for `$n`; do not wrap `ParameterValue` as a literal or retain both expression paths. TYPE-CORE-owned raw type modifiers stay distinct until that owner migrates them; their retained `Value` syntax does not authorize a second scalar evaluator. Named/default/variadic function syntax is feature-owned.

Retain the original SQL with the statement/plan, rather than evaluating SQL substrings or reconstructing source from nodes. `Position.Offset` is a zero-based byte offset into that SQL. For newly synthesized expressions, `Offset=-1` denotes absence; zero is the first character. An occurrence reports the responsible token: column reference start, `$n`, operator token, or function-name token.

### 2.2 Engine-owned bound contract

```go
type BoundExpr interface {
    boundExpression()
    ResultUsage() sqltype.Usage
    SourcePosition() parser.Position
    Properties() ExprProperties
    Eval(*EvalContext) (sqltype.Value, error)
}
type Volatility uint8
const (
    Immutable Volatility = iota
    Stable
    Volatile
)
type ExprProperties struct {
    Volatility Volatility
    RowDependent bool
    ParameterDependent bool
    StageDependent bool
}
type ExpressionAnalysis struct {
    Expr BoundExpr
    Dependencies []PlanDependency
}
func (b *expressionBinder) Bind(raw parser.Expr, policy ExprPolicy) (ExpressionAnalysis, error)
```

`expressionBinder` is **private state of the existing statement binder**. Its parent supplies the cancellation context, retained catalog/type view, resolution context, scope allocator, and statement-local parameter state. It is not another externally reachable statement analyzer. A successful `Bind` yields a typed node plus dependencies into analysis-private state; only successful whole-statement finalization publishes the complete immutable plan/description. On failure, no partial result or changed public parameter description is installed.

Required node forms and invariants:

| Form | Immutable retained data | Eval behavior |
|---|---|---|
| Literal | Concrete Usage, immutable typed constant or type-owned materialization plan, occurrence | Returns typed value; no raw unknown runtime Value |
| Column | Usage, `BoundColumnRef`, occurrence | Reads validated frame coordinates; no runtime name lookup |
| Parameter | Fixed parameter index/Usage and occurrence-specific conversion plan | Reads imported execution parameter then applies that occurrence plan |
| Coercion | Child, type-owned immutable plan, destination Usage, occurrence | Evaluates child and invokes `CoercionPlan.Apply` |
| Selected call | Children, immutable `sqltype.BoundCall`/provider handle, result Usage, occurrence | Evaluates call inputs, applies selected conversions, invokes provider or strict NULL rule |
| Conditional/control | Ordered typed children and selected common Usage/plans | Implements the dedicated lazy contract in §6, never eager-call desugaring |
| Stage value | Typed scope/frame/slot, occurrence and stage dependence | Reads an already computed value through `StageValues` |

Every executable node has concrete `Usage`, source evidence, properties and dependency contribution. `Eval` succeeds only with a valid Value whose descriptor matches `ResultUsage().Type`; typmod/collation stay occurrence metadata, not per-cell descriptors. SQL NULL is typed. Zero `sqltype.Value` is invalid/unset, never NULL. Result nullability remains unknown unless analysis proves it, per the type metadata contract.

Freeze all nodes, children, usages, provider handles, parameter descriptions and dependency collections at whole-statement finalization. Deduplicate dependencies in the parent statement binder using catalog `PlanDependency`: columns, types, casts, selected callable/provider and collation identities. Derived values do not invent catalog objects. Stored-definition consumers translate bound dependencies to their catalog edges; prepared validity dependencies are not durable `pg_depend` entries and never block DROP RESTRICT.

### 2.3 Lifecycle and publication

| Transition | Owner and complete output | Failure |
|---|---|---|
| SQL → raw AST | Existing whole-input parser; source/positions | No execution of a valid prefix |
| Raw → private analysis | Statement binder delegates each expression with explicit policy | No executable statement/description published |
| Resolve/finalize | Type/candidate resolution, occurrence coercions, parameters and dependencies; immutable bound plan plus StatementDescription | Unresolved usages or illegal contexts reject the statement |
| Validate prepared use | Catalog validity/reanalysis against current retained view | No stale binding executes; no partial replacement |
| Execute | Fresh typed parameters, frames, stage values, scratch | Ordinary context/provider errors propagate; no staged write publishes |
| Result/publication | Cursor retains paired rows/metadata; write owner publishes once | Final cancellation/precondition failure preserves committed state |

Direct execution analyzes fresh; prepared execution follows catalog §6's engine/epoch/revision/environment state machine and fixed parameter/result shape. Reanalysis uses original SQL, installs one complete replacement only after success, and may fail without running stale nodes. Expression analysis neither invents retries nor changes public metadata.

## 3. Scope resolution and explicit correlation coordinates

```go
type ScopeID uint32
type FrameID uint32
type ColumnBinding struct {
    Name parser.Identifier
    Origin ObjectRef
    Usage sqltype.Usage
    Frame FrameID
    Slot int
}
type NamespaceItem struct {
    Name parser.ObjectName
    Columns []ColumnBinding
    RelationVisible bool
    ColumnsVisible bool
}
type ExpressionScope struct {
    ID ScopeID
    Items []NamespaceItem
    Outer *ExpressionScope
    OuterAllowed bool
}
type BoundColumnRef struct {
    Origin ObjectRef
    Scope ScopeID
    Depth int
    Frame FrameID
    Slot int
}
```

These are engine-owned, analysis-immutable records. The statement/query binder allocates nonzero IDs unique within the bound plan; zero IDs are invalid. `Depth=0` addresses the current query frame; each permitted outer-query edge increases it by one. Lexical nesting, policy changes and a conditional arm do not create a query depth. Physical column `Origin.Address.SubID` is the catalog's stable attribute number, never a slice index. Derived/stage outputs use zero Origin and valid execution coordinates. The query owner supplies aliases, namespace visibility and flattened join/derived layouts from authoritative catalog descriptors; expression analysis does not own another column catalog.

Resolution algorithm:

1. Select visible namespace items appropriate to the raw qualification. Unqualified lookup uses `ColumnsVisible`; a qualifier matches `RelationVisible` items by their exposed relation/alias name.
2. Resolve matches in the nearest permitted query scope. More than one matching column there fails **42702**; never choose registration order. A local match shadows outer matches.
3. Only no local match allows an outer lookup, and only through an explicitly enabled `OuterAllowed` edge. Record the resolved Scope, Depth, Frame and Slot once.
4. Missing unqualified columns fail **42703**. Unknown/hidden relation qualifiers have command-specific **42P01** diagnostics, not a universal column error. Diagnostics distinguish an inaccessible existing column from one that does not exist.

An alias hides the original relation name. Query owners must provide a filtered namespace, not expose every execution frame and expect Eval to police it. A non-LATERAL FROM-derived context excludes sibling inputs; a LATERAL context includes only eligible preceding/left inputs. Allowed outer-query references are separate from excluded siblings. A scalar subquery owner builds its own scope and permitted outer edge, then a runtime frame retaining the correct outer row. Cardinality/width checks and correlation iteration belong to **PG18-QUERY-SUBQUERY**, not scalar Eval.

### 3.1 Policy is explicit and copied into nested binding

```go
type ExprContext uint8
const (
    Projection ExprContext = iota
    Filter
    Assignment
    DefaultExpression
    GeneratedExpression
    CheckExpression
    IndexExpression
    Returning
    AggregateArgument
    WindowArgument
)
type ExprPolicy struct {
    Context ExprContext
    Scope *ExpressionScope
    Expected *sqltype.Usage
    Coercion sqltype.CoercionContext
    MaxVolatility Volatility
    AllowParameters bool
    AllowAggregate bool
    AllowWindow bool
    AllowSubquery bool
}
```

Consumers explicitly construct every policy; a zero-valued policy is not a permissive default. `Expected=nil` means no contextual destination, never implicitly TEXT. Expected usage is a destination/construct constraint, not permission to use assignment casts in overload selection. Nested binding receives policy by value; any child-specific changes are local, and failures cannot leak context into siblings. Consumers validate feature-specific legality (including self/generated references and stage nesting), while common dispatch rejects nodes disallowed by the supplied policy before finalization. Unsupported features return their owning feature's error, never a successful fallback.

| Context transition | Supplied scope/expected usage | Boundary |
|---|---|---|
| Projection | Query-visible input; optional construct destination | Output unknown default only at type-owned boundary |
| Filter/ON/HAVING | Stage-appropriate namespace; BOOL expected | Consume only True; stage owner controls aggregate/window legality |
| Assignment | Owner's input frame; destination Usage, Assignment context | Resolve expression first, then sizing/constraints |
| Default | Empty row namespace, no outer columns | Fresh per-row value, followed by assignment |
| Generated/CHECK/index | Feature-filtered same-table row namespace | Generated dependencies, CHECK truth and index immutability remain distinct |
| RETURNING | Feature-provided OLD/NEW/result namespace | No invented old-row or conflict semantics |
| Aggregate/window argument | Feature-provided pre-stage namespace | Feature owns nesting legality and stage computation |
| Subquery entry | New scope and explicitly permitted outer edge | No accidental sibling or lexical-parent capture |

## 4. Catalog/type delegation and execution inputs

### 4.1 Analysis uses the prerequisite authorities

Retain one coherent `CatalogRead` and `ResolutionContext` for a statement analysis. Obtain object descriptors through catalog `ResolveReference`; obtain routine/operator candidates through `LookupCandidates` with the proper `LookupKind`. Adapt authoritative descriptors to type-owned `sqltype.Candidate`, including signature, strictness, provider metadata and visibility. Do not duplicate search-path filtering, identity allocation, overload rules or cast graphs.

Use `ResolveOperator`/`ResolveFunction` to select `BoundCall` and inserted **Implicit** conversions. Use `ResolveUnknown`, `PlanCoercion` and ordered `CommonType` as specified by the type design, not a local expression type enum. Resolve the expression/operator first; apply destination **Assignment** coercion and typmod sizing afterward. Unknown literals run only the selected input codec; failed input does not retry another overload. Explicit cast syntax/plans remain **PG18-CAST-EXPLICIT**; generalized executable CommonType remains **PG18-FUNC-CONDITIONAL**.

Reuse statement-private `ConstrainParameter(number, expected, sourceOffset)` and `FinalizeParameters()` from type §5.2. Retain sparse references until validating gaps before count-sized allocation. The fixed published parameter Usage has typmod -1 and descriptor default collation; each occurrence retains its own conversion/sizing plan. Invalid references, unresolved/gap parameters and incompatible unresolved occurrences follow the prerequisite's **42P02**, **42P18** and **42P08** boundaries. Do not change parameter types from execution Go kinds. TYPE-CORE's narrow unsupported mixed-destination rule stays until its generalized inference owner migrates it.

Finalization prohibits unknown/invalid executable results and parameters. Standalone unknown literal output defaults to TEXT only at the designated type output boundary; contextual/set-operation inputs defer that default. An unresolved parameter does not receive the literal TEXT default.

### 4.2 Runtime context and access

```go
type EvalFrame struct {
    Scope ScopeID
    Rows map[FrameID][]sqltype.Value
    Outer *EvalFrame
}
type EvalContext struct {
    Context context.Context
    Frame *EvalFrame
    Parameters []sqltype.Value
    Calls CallExecutor
    Stages StageValues
}
type CallExecutor interface {
    Invoke(*EvalContext, sqltype.BoundCall, []sqltype.Value) (sqltype.Value, error)
}
type StageValues interface {
    Value(scope ScopeID, frame FrameID, slot int) (sqltype.Value, error)
}
```

The execution owner supplies a nonnil Context, current frame and any adapters needed by its finalized nodes. A column read follows exactly `Depth` outer links, checks the expected Scope and reads its Frame/Slot. It does not search ancestors opportunistically or resolve names. Invalid/missing coordinates or absent required stage adapters are internal invariant errors, not SQL NULL or invented undefined-column SQLSTATE. Stage access consumes already computed typed values; its query owner ensures any correlated stage address uses the correct retained scope instance.

Parameters read index n-1 from a fixed imported array. Public missing/extra/invalid args fail the existing adapter count/ordinal/name boundary before Eval. Root `ImportParameter`/`ExportValue` own driver conversion; detached typed inputs are supplied to the engine. `CallExecutor.Invoke` receives values normalized to the selected formal signature; conversion runs once under the type-owned plans, not again in provider lookup. It bridges native/routine owners, not another routine interpreter. Those owners control privileges, body frames, nested snapshots and publication.

### 4.3 Ownership and allocation invariants

* A retained bound plan contains no execution arguments, frames, context pointers, mutable caches or scratch. Concurrent/repeated use requires no mutation of expression nodes.
* Each execution owns its frames, parameter array, stage adapters and reusable scratch sized for its expression/call requirements. Rows and argument slices are borrowed for the invocation; providers cannot retain those slices or context for later mutation. Nested calls use nonoverlapping live scratch regions, so a child cannot overwrite still-live parent arguments.
* Detach mutable external inputs once at import. Share immutable scalars/strings/descriptors and checked immutable extension payloads; do not box primitives, copy whole snapshots or allocate descriptors per cell.
* Frame maps are logical setup records, not per-cell catalog lookups/allocations. Consumers may compile Frame IDs to indexed access without changing identity or visibility semantics.
* Provider results obey type-owned immutable-payload construction and resolved result identity. Returning borrowed mutable scratch as a Value is forbidden.
* Catalog reads, descriptors, bound provider handles and captured snapshots are retained for the plan/cursor's required lifetime. Reset/Restore installs a new lifetime; it never mutates values or metadata already held by a cursor.
* Cursor output uses type-owned `ColumnDescriptor`, typed private scratch and `ExportValue`. NULL exports nil but never loses BOOL or another declared result descriptor.

## 5. Errors and locations

`Eval`/`Bind` return ordinary `error`; SQL errors reuse `internal/sqlstate` and **PG18-DRIVER-ERROR-FIELDS** for SQLSTATE, primary/detail/hint, optional object fields and source positions. There is no expression-specific error envelope. Preserve provider SQLSTATE and English primary/detail/hint; do not mutate a shared provider error. Attach consumer location only where the corresponding PostgreSQL diagnostic has it. Do not blanket-fill Schema/Table/Column for every expression failure.

The diagnostic adapter converts an occurrence's byte offset using original SQL into PostgreSQL's one-based **character** Position. Absent offset -1 stays absent. Parentheses do not displace responsible tokens. Internal execution-layout errors are engine invariant failures, not fabricated SQL user errors. Go context, driver argument, converter and I/O errors stay ordinary errors at their documented boundaries.

Representative delegated SQL failure classes: missing column 42703, same-scope ambiguous column 42702, qualifier/relation lookup 42P01, no callable 42883, ambiguous callable 42725, assignment/common-type mismatch 42804, unsupported explicit pair 42846, invalid input 22P02, range 22003 and sizing 22001. Feature owners determine exact English diagnostics and optional fields through release reference witnesses. Cancellation or a provider failure rejects the entire staged write; no error is suppressed into NULL.

## 6. NULL, three-valued truth, and evaluation guarantees

### 6.1 Truth is a view of typed BOOL

```go
type Truth uint8
const (
    False Truth = iota
    True
    Unknown
)
func TruthOf(v sqltype.Value) (Truth, error)
```

`TruthOf` accepts only a valid resolved BOOL Value. BOOL NULL maps to Unknown; it does not become Go false. A non-BOOL/invalid value here is an internal typing violation. Typed nullable Values remain the scalar representation, including for projection; Truth is only the predicate/control view.

| AND | True | False | Unknown |
|---|---|---|---|
| True | True | False | Unknown |
| False | False | False | False |
| Unknown | Unknown | False | Unknown |

| OR | True | False | Unknown |
|---|---|---|---|
| True | True | True | True |
| False | True | False | Unknown |
| Unknown | True | Unknown | Unknown |

NOT maps True→False, False→True, Unknown→Unknown. Strict scalar comparison with a NULL input returns BOOL NULL. WHERE/ON/HAVING retain only True; CHECK accepts True or Unknown and rejects False. These are consumer acceptance policies, not different equality operators. NULL grouping/key equivalence and ordering belong to type/query key families, not SQL nullable comparison; preserve baseline ASC NULLS LAST/DESC NULLS FIRST.

### 6.2 Bind all arms; lazy runtime control is explicit

Every arm is bound and type/scope checked even if runtime skips it. Ordinary selected calls evaluate inputs eagerly, but expose **no portable left-to-right argument order**. Strictness may suppress provider invocation and produce typed NULL when an argument is NULL; it does not establish expression-argument order. Any optimization of Boolean evaluation must preserve SQL truth and documented volatility/error boundaries; callers cannot rely on callback counts or `TRUE OR failing_expression` as a guaranteed guard.

CASE and COALESCE have dedicated bound control nodes:

* Searched CASE evaluates WHEN conditions in order; Unknown is not True. It evaluates only the selected result arm, otherwise ELSE (or a typed NULL when absent).
* Simple CASE evaluates its test once, then its feature-owned comparisons in order, with the same selected-result rule.
* COALESCE evaluates through the first nonnull input and skips later inputs; all-NULL produces typed NULL of the resolved common result Usage.

Branch-result type/coercion plans are chosen before execution under type-owned CommonType and applied only to the chosen runtime input. Runtime branch skipping does **not** promise to avoid bind-time validation, permitted immutable constant evaluation, or earlier aggregate/window-stage evaluation. In particular, an unreachable constant `1/0` can fail during planning, and CASE cannot guard aggregate arguments that its query stage computes earlier. This design does not mandate a constant-folding optimizer in EXPR-CORE.

## 7. Volatility, cancellation, and publication

Compute a node's volatility as the maximum of provider metadata and its children. No-argument calls still carry provider volatility. Record row, parameter and stage dependence separately; do not equate Immutable with constant. Callable properties come from the existing candidate/provider adapter, not another catalog field authority.

| Property | Permitted reuse | Forbidden |
|---|---|---|
| Immutable | Optional plan-time folding of concretely typed, independent constants | Folding row/parameter/stage-dependent expressions as constants; losing dependencies/errors |
| Stable | Statement-local reuse only under routine/type/session snapshot contracts | Retaining results or execution args across prepared executions |
| Volatile | Evaluate wherever a row/execution needs the value | Caching across needed rows/executions or invoking runtime skipped CASE branches |

A future folding owner preserves dependency and error behavior, including CASE's constant-error caveat. Routine/TX owns volatility-specific SQL-body snapshot visibility; scalar Eval cannot create that policy.

Check `Context.Err()` before analysis/evaluation entry, while traversing unbounded children/rows, before and after provider calls and before final publication. The parent binder holds the same context even though Bind's logical signature uses private binder state. Providers receive it and must cooperate; external blocking I/O is not forcibly interruptible. No context escapes into an immutable node.

A later-row evaluation/coercion/constraint error or cancellation publishes **none** of a statement's staged writes. The write owner evaluates/validates outside final publication and retains one indivisible publish boundary. SELECT captures immutable input and paired metadata at the existing synchronization seam, evaluates outside target locks and keeps captured row lifetime. Arbitrary callable execution must not move under the publication lock. Restore still uses the ordinary parser/binder/evaluator in a staging Engine and replaces the target only after complete success. Snapshot visibility, transactions, locks and retries remain their assigned owners, not expression decisions.

## 8. Extension contract

New scalar kinds extend the closed raw and bound markers and the existing bind dispatch. Each kind supplies occurrence position, policy validation, delegated typing/coercion, dependencies, conservative properties and Eval returning typed Value/error. The containing statement still finalizes all parameters and descriptions once. No string expression parser, runtime SQL substring evaluation, `any` functor registry, bool-only predicate evaluator or separate DDL/prepared evaluator is permitted.

Aggregate/window/subquery syntax may have feature-owned concrete nodes, but their relational work remains stage-owned. Aggregate/window consumers expose typed computed slots via `StageValues`; absence is an invariant error. Subquery consumers own query entry, frame creation, correlated iteration, cardinality/width errors and result adaptation through the same typed boundary. Scalar Eval does not implement grouping, window partitioning, query-stage order, DDL/DML publication or catalog revalidation. **PG18-ARCH-QUERY-EXECUTION** and each consumer supply those responsibilities.

## 9. Clean-cutover callsite map

The anchors below describe the inspected baseline in this worktree, not edits performed by the design. Future owners reread each callsite before migration and delete all migrated alternate paths without aliases/shims. Shared prerequisite implementations precede their consumers; this table does not authorize changing them here.

| Current file/symbols | Future replacement/removal | Owner/retained seam |
|---|---|---|
| `internal/parser/ast.go:32–78` — Insert.Rows, Select.Columns/Where, Predicate, Value parameters | Rows→`[][]Expr`; expression targets; Where→Expr; remove Predicate/string-only migrated projection binding and generalized ParameterValue path | EXPR-CORE baseline expression containers; star stays statement expansion; Order waits for query owner; raw type modifiers stay TYPE-CORE-owned |
| `internal/parser/grammar.y:4–18,61–84`, generated `grammar_gen.go` | Emit occurrence nodes/positions and selected feature productions; generate output from grammar source, never hand-edit generated parser | One lexer/goyacc path; each syntax owner adds its own grammar |
| `internal/parser/lexer.go` — normalization and offsets | Supply existing normalized identifiers and byte offsets; no resolver normalization/substr search | Catalog/identifier contracts, original SQL retained |
| `internal/engine/engine.go:32–189` — binding, Describe/describe, local bind/column closures | Route migrated occurrences through expressionBinder; finalize parameters/dependencies/description once; bound projections/predicates replace scalar indices/where bool | Sole statement analysis authority; catalog/type resolution retained |
| `engine.go:225–343` — parameterColumn, Execute resolve/canonical paths | Use occurrence metadata, fresh typed imports/frames and BoundExpr evaluation; remove migrated raw-expression inference/traversal | Root import/type plans, atomic staging and final cancellation |
| `internal/engine/value.go:66–121` — parameter/literal/input switches | Delete obsolete helpers when TYPE-CORE codecs and expression binding take all callers | TYPE-CORE owns codecs; no duplicate expression conversion switches |
| `internal/engine/snapshot.go:9–90` — projection, Values, compare/selectRows | Bound predicate/projection plus typed cursor scratch; use prerequisite key families, remove migrated payload comparator | Immutable capture/all-rows fast path; QUERY-CORE owns general stages/sort |
| `conn.go:35–110` — PrepareContext, execute, direct Exec/Query | Retain source/AST/StatementDescription/BoundPlan, shared typed import and expression seam | parseOne; catalog validity; no prepared-only binder |
| `stmt.go:11–47` — count/NumInput, Exec/Query | Fixed description and complete prepared binding; no mutation of inferred parameter metadata | Closed-state lifecycle and conn.execute convergence |
| `rows.go:12–60` — Next/metadata/Values | Typed private scratch, ColumnDescriptor and ExportValue | Existing Context/EOF/Close/public metadata boundary |
| `internal/engine/control.go:134–171` — Restore parameter scan/stage.Execute | Shared raw expression visitor rejects every parameter at occurrence with 42P02; ordinary staged execution | CREATE/INSERT whitelist, final replacement and failed-target preservation; Dump uses type codecs/descriptors, not a new expression serializer |
| `internal/engine/engine_test.go`, `driver_test.go`, `reference_test.go` | Migrate representation callers under feature owners, retain independent expected rows/types/counts/atomicity | PG18-DRIVER-ERROR-FIELDS expands diagnostics; do not weaken tests to fit implementation |

At each migration session, use repository grep over Go and grammar files for:

```
Predicate|ParameterValue|parameterColumn|selectRows|literal\(|parameter\(|\.describe\(|\.Describe\(|\.Execute\(|\.Values\(
```

Inspect every match, including tests/generated consumers. Distinguish raw type modifiers and intentionally later query stages from obsolete scalar paths. `referenceStep.selectRows`, for example, is a fixture result-kind flag, not the engine predicate evaluator. Completion requires every migrated caller on the one seam, no compatibility aliases, and no retained second binder/evaluator.

## 10. Bounded existing-backlog ownership

These are existing capability sessions, not new tasks/status changes. Each owner consumes implemented prerequisites, updates all of its callers and supplies independent unit/public/reference outcomes and concrete feature smoke. No row in this table is implemented by this document.

| Backlog owner | Bounded work / policy and frame | Independent exit witness and exclusion |
|---|---|---|
| **PG18-EXPR-CORE** | After implemented CAT-CORE/TYPE-CORE: literals, columns, parameters, simple projections and existing equality filtering through one bound seam | W1 typed projection/parameter and nullable equality versus WHERE unit semantics; no arithmetic, general clauses, functions or aliases. Equality projection syntax is its selected expression/operator owner's work, not silent widening of this foundation |
| **PG18-FUNC-LOGICAL** | AND/OR/NOT typed truth nodes | All nine ordered AND/OR pairs and three NOT inputs; no callback-order promises |
| **PG18-FUNC-COMPARE-OPS** | Expanded comparisons/cross-width selected calls | Independent BOOL/NULL rows, resolution identity and coercion failures |
| **PG18-FUNC-COMPARE-NULL** | NULL/distinct/truth predicates | Independently enumerate NULL/non-NULL inputs; do not reuse strict equality's NULL result for nonstrict predicates |
| **PG18-FUNC-CONDITIONAL** | Dedicated lazy CASE/COALESCE; executable ordered CommonType | W5 row-dependent skipped branch; incompatible arm types still fail binding |
| **PG18-CAST-EXPLICIT** | Explicit syntax and type-owned plans | Success/no-cast SQLSTATE/positions and sizing; no new overload algorithm |
| **PG18-QUERY-CORE** after **PG18-ARCH-QUERY-EXECUTION** | Source/filter/project/sort stage cutover with supplied policies/frames | W2 predicate projection/filter difference, ordered output and captured metadata; scalar Eval does not define query order |
| **PG18-QUERY-SUBQUERY** | New scope/frame plus permitted outer edges; correlated iteration/width/cardinality | W4 forbidden sibling versus LATERAL, correct outer values; scalar evaluator does not iterate queries |
| **PG18-QUERY-AGGREGATE** | Legality and computed typed aggregate slots | Earlier aggregate evaluation independent of later CASE selection; grouping is stage-owned |
| **PG18-QUERY-WINDOW** | Legality and typed window-result slots | Correct stage context/result values; partition/order semantics not expression-owned |
| **PG18-EXPR-COLLATION** | Type-owned derivation/provider identity in nodes/calls/keys | Independently expected conflicting derivation/provider errors; no extra collation authority |
| **PG18-DDL-DEFAULT** | Empty row/no outer scope, fresh per-row evaluation then assignment | Multiple inserted rows receive feature-correct values; no accidental row-column capture |
| **PG18-DDL-GENERATED-STORED** | Permitted same-row inputs, excluded self/generated/disallowed refs and feature-disallowed volatility | Stored write-time results and rejected illegal references; owner controls dependency order |
| **PG18-DDL-CHECK** | Same-row scope, True/Unknown acceptance | NULL accepted, False rejected with feature diagnostics and atomic preservation |
| **PG18-INDEX-EXPRESSION** | Same-table immutable inputs; no aggregate/subquery contexts | Real computed keys and rejected volatile/illegal expressions; no index implementation in Eval |
| **PG18-DML-UPDATE-CORE** | Every RHS reads retained old-row frame before replacement; assignment/constraints then one publish | Simultaneous assignments see original row; late failure publishes none |
| **PG18-DML-INSERT-RETURNING** | Feature-owned OLD/NEW frames and result descriptors | Exact typed returned rows and affected counts; no fabricated old-row/conflict values |

DDL owners persist dependency-backed bound expressions via catalog staging, not text-name rebinding. Routine/native providers consume CallExecutor with their own authorization, body frames and snapshot contracts; catalog/type/driver-prepared owners retain their prerequisite responsibilities.

## 11. Independent future acceptance witnesses

**None of these scenarios were executed in this design session.** Future owners specify expectations independently for both engine and pinned PostgreSQL, then compare. Use public direct and prepared `database/sql` paths where applicable; unit tests additionally assert binding identities/frames/properties not visible in SQL. Without ORDER BY compare bags with multiplicity. Existing serialized reference fixture/schema reset is reused; Docker/image/version errors remain visible failures. Structured diagnostic assertions require PG18-DRIVER-ERROR-FIELDS rather than SQLSTATE-only weakening. Optional fields not captured here are future release-reference obligations, not observed evidence.

### W1 — Typed column and parameter resolution (A1/A2/A4)

```
CREATE TABLE expr_t(id INTEGER,v INTEGER);
INSERT INTO expr_t VALUES (1,1),(2,NULL),(3,2);
SELECT id,v FROM expr_t ORDER BY id;
```

INSERT affects 3. SELECT independently expects `[(1,1),(2,NULL),(3,2)]`, types INT4/INT4. Prepared `SELECT v FROM expr_t WHERE v=$1` with argument 2 returns `[(2)]`; parameter Usage is INT4, typmod -1, not inferred as INT8 from Go integer kind. Unit assertions cover stable column Origin/SubID, separate frame slot, selected call/type/cast dependencies, fixed parameter description and occurrence coercions. Invalid/unresolved sparse parameter validation precedes large allocation; failed analysis installs no executable binding.

### W2 — NULL projection is not filtering (A1/A3)

Future expression projection `SELECT id,v=1 FROM expr_t ORDER BY id` independently expects `[(1,TRUE),(2,NULL),(3,FALSE)]`, INT4/BOOL. `SELECT id FROM expr_t WHERE v=1 ORDER BY id` expects `[(1)]`. Projected NULL exports nil with BOOL metadata. Equality projection grammar is implemented by its selected expression/operator owner, not an expansion silently assigned to narrow EXPR-CORE.

Logical-owner unit/reference cases enumerate the complete §6 matrices. CHECK-owner cases accept True and Unknown but reject False. NULL key grouping/sorting expectations are specified separately under their owners; nullable equality is not a key-equivalence implementation.

### W3 — Missing reference and UTF8 source location (A1/A2)

`SELECT missing FROM expr_t` fails **42703**, primary `column "missing" does not exist`, Position **8**, no binding/result publication. `SELECT /*é*/ missing FROM expr_t` expects Position **14**, not byte-based position 15. These positions are arithmetic-derived independent expectations, not captures. The release-source research grounds primary wording; future pinned reference checks exact optional detail/hint/object fields rather than assuming their presence/absence.

Unit scope failures additionally prove **42702** for same-scope ambiguity and the feature's **42P01** qualifier boundary for hidden/unknown aliases. No first-match fallback is accepted.

### W4 — Correlation is permitted explicitly (A2)

Unit scopes independently prove local shadowing, same-scope ambiguity, correct outer Depth/Scope/Frame/Slot and prohibited sibling traversal. Future `SELECT o.id FROM expr_t AS o, (SELECT v) AS s` fails **42703** with inaccessible-column detail and LATERAL hint as grounded by release-source research. The LATERAL version returns ids 1,2,3 as a bag of three rows, including the row whose v is NULL. Exact full Position/detail/hint fields remain future pinned-release capture obligations; do not claim them observed here. Aliases hide original relation qualifiers. Correlated execution holds the right outer frame for each row, not any convenient parent frame.

### W5 — Skipped failing branch, not a Boolean-order promise (A1/A4/A5)

Conditional/arithmetic consumers establish:

```
CREATE TABLE branch_t(id INTEGER,v INTEGER);
INSERT INTO branch_t VALUES (1,0),(2,2);
SELECT id,CASE WHEN v=0 THEN 7 ELSE 10/v END FROM branch_t ORDER BY id;
```

INSERT affects 2; SELECT independently expects `[(1,7),(2,5)]`, INT4/INT4. `SELECT 10/v FROM branch_t` fails **22012**, expected English primary `division by zero`, requiring future pinned-release confirmation of fields. A unit provider proves the row-dependent failing branch is not invoked for the zero row. Bind all arms: a missing column or incompatible arm cannot be hidden by runtime selection. Do not replace the row-dependent case with constant `1/0` and promise success; permitted planning evaluation may fail. Do not assert left-to-right Boolean callback counts or use `TRUE OR failing_expression` as an error guard.

### W6 — Lifetime, volatility, cancellation and atomicity (A3/A5)

Execute one retained plan concurrently/repeatedly with distinct args: each result matches its own typed frame with no leakage. Mutate a borrowed `[]byte` after import: retained values stay unchanged. Hold a cursor across append/Reset/Restore: original rows/types/labels remain captured; replacement affects only new reads. Go buffer ownership is a unit/public boundary witness, not something real PostgreSQL can prove.

Unit providers assert Stable state is not reused on the next execution, Volatile is not cached across needed rows, and runtime skipped CASE providers are uninvoked. Pre-canceled execution and canceled cursor iteration return ordinary `context.Canceled`. Cancellation checks surround providers and unbounded traversal. A late multirow INSERT evaluation/input/coercion/constraint failure changes no rows; failed/canceled Restore preserves a target sentinel and catalog lifetime. Reference SQL verifies corresponding feature atomicity independently; it does not stand in for Go-context/buffer assertions.

### W7 — One seam and bounded ownership (A6/A7)

Artifact/consumer review follows every §9 caller to its owner and validates the lifecycle: raw → private analysis → complete immutable plan → private execution → result/staged publication. Every new node has source/type/properties/dependencies/error behavior; every allowed scope has a validated runtime frame; every consumer has a truth/legality policy. Retained metadata and plan invalidation stay catalog/type-owned. No stage policy authorizes scalar Eval to implement grouping, query iteration or publication.

Baseline source evidence read, not run: `internal/engine/engine_test.go:54–139,152–209` covers parameter/NULL/multirow/snapshot/control seams; `reference_test.go:17–65,84–170` has independent rows/types/count fixture paths; `reference_helpers_test.go:20,57–88` is the research anchor for pinned release/locale. Existing SQLSTATE-only fixtures must gain structured diagnostics through their owner. Future implementations supply new-behavior smoke under their own frozen plans; this design's empty smoke list is not executable acceptance evidence.
