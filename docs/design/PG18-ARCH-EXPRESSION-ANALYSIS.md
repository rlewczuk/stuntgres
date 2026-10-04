# PG18-ARCH-EXPRESSION-ANALYSIS — Expression analysis and evaluation

## 1. Decision and authority

Adopt one parser raw-expression representation and one immutable typed expression seam inside the **existing engine statement binder/executor**. This is a design-only contract: all declarations and SQL witnesses below are future requirements, not implemented APIs or exercised results. No production code or adjacent design changes are authorized. Individual syntax, operators/functions, casts, aggregates/windows, subqueries and clauses remain their bounded implementation owners. Production dependencies remain standard library/module only.

Binding prerequisites: [CATALOG-IDENTITY](PG18-ARCH-CATALOG-IDENTITY.md) §§2–4,6–7 owns provenance, normalized names, retained reads, lookup, dependencies, validity and publication; [TYPE-SYSTEM](PG18-ARCH-TYPE-SYSTEM.md) §§2–9 owns Usage/Value, coercions, callable selection, parameters, keys, metadata and adapter conversion. Use its `ColumnDescriptor`/`StatementDescription`, not catalog's illustrative parser-column result shape as a second metadata model. Completed designs do not imply executable capabilities.

Composition with the allowed companion contracts is explicit, not a new subsystem:
- [SESSION](PG18-ARCH-SESSION.md) §3 supplies the same pointer-owned `*Operation` to Describe/Execute, coherent resolution/selected target and combined cancellation context; statement-owned expressionBinder receives these privately. No retained session state in bound nodes.
- [AUTHORIZATION](PG18-ARCH-AUTHORIZATION.md) §§5,7–8 owns `AccessRequirement` collection/checkpoints, routine EXECUTE, scoped definer authority and RLS placement. Binder contributes actual referenced columns/callables and positions to statement requirements, including predicate-only uses. Binding an identity does not authorize access. Preserve its preparation/preflight versus execution phases; do not add an unconditional ACL-first rule.
- [TX-VISIBILITY](PG18-ARCH-TX-VISIBILITY.md) §§3,5–8 supplies statement scope, purpose-specific resolution catalog/data view, staged writes and completion. Preparation retains definitions, not execution snapshots; snapshot-requiring analysis can acquire a view. Scalar evaluation cannot acquire arbitrary snapshots, commit or invent retries. Source `{DatabaseTarget,ObjectRef}` provenance survives query/frame construction; frame slots are not authority.

### Acceptance

| Item | Contract | Independent future witness |
|---|---|---|
| A1 raw/bound interfaces, typed result, errors/locations | §§2,5 | W1–W3,W5 |
| A2 scopes, row/parameter access, correlation boundaries | §§3–4 | W1,W3–W4,W6 |
| A3 context, ownership/lifetime, NULL/truth | §§4,6 | W2,W6 |
| A4 catalog/type operator/function delegation | §§2,4 | W1,W5,W7 |
| A5 volatility, skipping/order, cancellation | §6 | W5–W6 |
| A6 extension and clean-cutover caller map | §§7–8 | W7 |
| A7 bounded backlog implementation breakdown | §9 | W1–W7 and owner exits |

## 2. Raw → bound → execution

Proposed parser contracts (package-private marker closes the set):

```go
type Expr interface { expression(); SourcePosition() Position }
type LiteralExpr struct { Value Value }
type ColumnExpr struct { Name ObjectName; Pos Position }
type ParameterExpr struct { Number int; Pos Position }
type OperatorExpr struct { Name ObjectName; Args []Expr; Pos Position }
type FunctionExpr struct { Name ObjectName; Args []Expr; Pos Position }
```

Reuse catalog's component-wise normalized `ObjectName`/`Identifier`; no splitting decoded dots or second normalization. Raw Value is literal syntax, not runtime storage. LiteralExpr returns Value.Pos; other kinds return Pos. ParameterExpr becomes the sole generalized `$n` occurrence, replacing scalar ParameterValue paths during migration; raw type-modifier Value syntax remains TYPE-CORE-owned until its owner migrates it. Feature owners add named/default/variadic syntax. Parentheses preserve child positions.

Proposed engine contracts:

```go
type BoundExpr interface {
    boundExpression()
    ResultUsage() sqltype.Usage
    SourcePosition() parser.Position
    Properties() ExprProperties
    Eval(*EvalContext) (sqltype.Value, error)
}
type Volatility uint8
const (Immutable Volatility = iota; Stable; Volatile)
type ExprProperties struct {
    Volatility Volatility
    RowDependent, ParameterDependent, StageDependent bool
}
type ExpressionAnalysis struct { Expr BoundExpr; Dependencies []PlanDependency }
func (b *expressionBinder) Bind(raw parser.Expr, policy ExprPolicy) (ExpressionAnalysis, error)
```

`expressionBinder` is private statement-binder state holding its operation context, catalog/type view, resolution context, scope allocation and parameter constraints. Bind adds analysis-private nodes/dependencies; **whole-statement successful finalization** freezes nodes, children, usages, providers, dependencies and descriptions. Failure publishes no executable binding or partial public metadata.

| Bound form | Retained immutable data | Evaluation |
|---|---|---|
| Literal | Concrete Usage, typed constant/materialization plan, occurrence | No raw unknown runtime Value |
| Column | Usage, BoundColumnRef, occurrence | Validated frame coordinates, never names |
| Parameter | Fixed index/Usage, occurrence conversion plan | Imported parameter then occurrence coercion |
| Coercion | Child, type-owned plan, destination Usage, occurrence | Child then CoercionPlan.Apply |
| Selected call | Typed children, BoundCall/provider, result Usage, occurrence | Selected input conversions then invocation/strict NULL |
| Control | Ordered typed children, common Usage/plans | Dedicated lazy runtime rule (§6) |
| Stage value | Usage, scope/frame/slot, stage dependence, occurrence | Already computed stage result |

Every executable kind contributes dependencies/properties and returns a valid Value matching its result descriptor. Usage carries occurrence typmod/collation; these are not per-cell descriptors. Zero Value is invalid, not NULL; SQL NULL retains a concrete type. Nullability is unknown unless proved. Deduplicate statement `PlanDependency` values for columns/types/casts/callables/providers/collations. Derived outputs invent no catalog identities. Stored definitions convert these to catalog edges; prepared dependencies remain ephemeral and cannot block DROP RESTRICT.

Lifecycle: whole-input parse → private analysis → type/scope/parameter/dependency finalization → immutable plan/StatementDescription → catalog validity/reanalysis at prepared use → private execution → retained result or staged publication. Direct execution analyzes fresh. Prepared use follows catalog §6's owner/epoch/revision/environment validity and fixed parameter/result shape; complete replacement only, no stale execution or metadata mutation on failed reanalysis. Original SQL remains with statement/plan, never runtime substring evaluation.

## 3. Scope, policy and correlation

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
    RelationVisible, ColumnsVisible bool
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

The statement/query owner creates analysis-immutable namespaces and plan-unique nonzero scope/frame IDs; zero is invalid. Catalog supplies descriptors; query owner supplies exposed aliases, qualification and flattened join/derived layouts. Origin.SubID is stable attribute number, not execution slot. Derived/stage outputs use zero Origin and valid coordinates. Preserve catalog/target provenance through source binding; no expression column catalog.

Resolution has distinct stopping rules. **Unqualified column:** search ColumnsVisible items in the current query scope; multiple matches fail 42702, one match shadows outer columns, and only no eligible column match permits an enabled Outer edge. **Qualified reference:** first select the nearest eligible RelationVisible relation/alias item by qualifier (same-scope alias ambiguity fails 42P09), traversing enabled outer edges only if no qualifier matches. Then search the column **only in that selected item**; a missing column cannot restart qualifier lookup in an outer scope. Feature-owned whole-row/function-style fallback also uses that selected item, not an outer alias. Schema qualification matches unaliased relation identity, not a dotted alias string. Depth counts query edges, not lexical nesting/policy changes. Missing columns fail 42703; unknown/alias-hidden qualifiers have feature-specific 42P01 paths. Aliases hide original names; inaccessible-column diagnostics distinguish hidden existing columns from no match.

Non-LATERAL FROM-derived scopes exclude sibling inputs. LATERAL owners expose only eligible preceding/left inputs and enforce join-side legality; an allowed outer query is distinct from excluded siblings. Query/subquery owner constructs the outer frame for the correct row and owns correlation iteration, width/cardinality and exact context errors. Scalar Eval never searches a convenient ancestor.

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
    AllowParameters, AllowAggregate, AllowWindow, AllowSubquery bool
}
```

Consumers construct every policy explicitly; zero is not permissive. Expected=nil means no destination, never automatic TEXT or assignment-cast overload admission. Nested binding receives policy by value; child changes/errors cannot leak into siblings. Common dispatch rejects disallowed kinds before finalization; feature owners check stage nesting, self/generated references and context-specific restrictions/errors.

| Context transition | Owner supplies | Acceptance/boundary |
|---|---|---|
| Projection | Query input namespace | Type-owned output fallback |
| Filter/ON/HAVING | Stage-appropriate namespace, BOOL expectation | Only True retained; owner controls stage legality |
| Assignment | Input frame, destination Usage, Assignment context | Expression resolution before sizing/constraints |
| Default | Empty row scope, no outer columns | Fresh per-row evaluation then assignment |
| Generated/CHECK/index | Feature-filtered same-row scope | Distinct dependencies, truth and immutability restrictions |
| RETURNING | Feature OLD/NEW/result namespace | No invented old-row/conflict semantics |
| Aggregate/window args | Pre-stage namespace | Owner controls nesting/computation |
| Subquery | New query scope, enabled outer edges only | Owner controls cardinality and iteration |
| RLS | Authorization-selected feature row scope, BOOL; no aggregates/windows | USING drops False/Unknown; WITH CHECK rejects them (not CHECK-constraint truth) |

RLS query placement/leakproof ordering belongs to authorization/query consumers. Do not infer reordering permission from volatility alone or implement a second policy evaluator.

## 4. Delegation and execution ownership

Analysis retains a coherent catalog/type view and resolution context for its purpose. Resolve objects with ResolveReference, routine/operator candidates with LookupCandidates and LookupKind. Adapt authoritative candidates to sqltype.Candidate including signature, strictness, visibility and provider properties; invent no search-path/overload/cast algorithm or callable catalog.

ResolveOperator/ResolveFunction choose BoundCall and **Implicit** input conversions. Apply destination **Assignment** coercion/sizing only after expression resolution. ResolveUnknown, PlanCoercion and ordered CommonType remain type-owned; failed selected input never retries overloads. CASE supplies CommonType's type-owned ELSE-before-THEN order, distinct from runtime WHEN order. Explicit cast syntax is CAST-EXPLICIT; executable conditional CommonType belongs to FUNC-CONDITIONAL. Preserve BoundCall's operation/input collation separately from result collation.

Use statement-private ConstrainParameter(number,expected,sourceOffset) and FinalizeParameters. Preserve sparse gap validation before count-sized allocation, fixed Usage (typmod -1/default collation), and occurrence-specific conversion/sizing. Invalid/unresolved/inconsistently unresolved parameters follow 42P02/42P18/42P08. Go argument kinds cannot change inferred types. TYPE-CORE's narrow mixed-destination 0A000 remains until its generalized inference owner migrates it.

Finalization prohibits unknown/invalid executable results and parameters. At the type-owned standalone SELECT/RETURNING output boundary, remaining unknown targets, including bare unresolved parameters, receive a TEXT contextual constraint after other inference. Contextual INSERT SELECT/set-operation inputs defer output fallback; unresolved parameters elsewhere (such as `$1 IS NULL`) still fail 42P18. This is not an unconditional parameter default.

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

Column Eval follows exactly Depth outer links, checks Scope, reads Frame/Slot. Missing coordinates/stage adapters are invariant errors, never NULL or fabricated undefined-column SQLSTATE. Stage owner supplies the correct correlated scope instance. Parameter n reads index n-1 from the imported array; public count/name/ordinal validation precedes Eval. Root ImportParameter/ExportValue own wire conversion. CallExecutor receives values normalized once to selected formals; native/routine owners handle privileges, body frames, scoped settings/authority, nested snapshots and effects.

Ownership/allocation rules:
- Bound plans contain no execution values, contexts, frames, scratch or mutable caches; concurrent executions never mutate nodes/descriptions.
- Each execution owns frames/parameters/stages and reusable scratch sized once for required calls/output. Nested live arguments use nonoverlapping scratch. Borrowed rows/argument slices cannot be retained by providers.
- Detach mutable external inputs once at import; share immutable strings/scalars/descriptors. No redundant whole-snapshot copying, primitive boxing or descriptors per cell. Returned Values cannot wrap mutable borrowed scratch.
- Frame maps describe logical setup, not per-cell allocation/catalog work; compile IDs to indexed access when appropriate without changing semantics.
- Retained catalog/provider/descriptor/row lifetimes outlive their plan/cursor uses. Reset/Restore installs a new lifetime, never mutates captured rows/metadata. Cursor owns typed scratch and ExportValue; NULL exports nil with declared metadata preserved.

## 5. Errors and source evidence

Bind/Eval return ordinary error using the **existing** internal/sqlstate transport/root alias. PG18-DRIVER-ERROR-FIELDS transport and full-field reference helpers already exist; expression consumers populate exact feature diagnostics, not another envelope. Preserve provider SQLSTATE/primary/detail/hint; never mutate a shared provider error or blanket-add Schema/Table/Column. Attach location only where the corresponding PostgreSQL diagnostic has it. Context/adapter/I/O/internal invariant errors remain ordinary errors.

Position.Offset is a zero-based byte offset into retained SQL; newly synthesized absent positions use -1, never zero (first character). Preserve existing Position.Character evidence. Convert to one-based **character** Position at the shared diagnostic boundary, not by substring search/byte counting. Responsible locations are reference start, `$n`, operator token or function name; parentheses do not replace children.

Delegated examples: undefined/ambiguous column 42703/42702, relation qualifier 42P01, no/ambiguous callable 42883/42725, assignment/common-type mismatch 42804, unsupported cast pair 42846, input/range/sizing 22P02/22003/22001. Feature owners supply exact wording/fields and unsupported-feature diagnostics, never a successful fallback or error-to-NULL suppression.

## 6. Truth, evaluation guarantees, volatility and cancellation

Typed nullable BOOL is the scalar result. Define internal `type Truth uint8`, `const (False Truth = iota; True; Unknown)` and `TruthOf(sqltype.Value) (Truth,error)`. Invalid/non-BOOL is an invariant error. Truth is only a predicate/control view, not a bool-only alternative evaluator. For input order T,F,U:

| Operation | T row | F row | U row |
|---|---|---|---|
| AND (columns T,F,U) | T,F,U | F,F,F | U,F,U |
| OR (columns T,F,U) | T,T,T | T,F,U | T,U,U |
| NOT | F | T | U |

Strict scalar comparison with NULL yields BOOL NULL. WHERE/ON/HAVING retain True only; CHECK accepts True/Unknown. RLS WITH CHECK instead requires True (§3). Sorting/grouping/key NULL policies are type/query-owned, not nullable equality; preserve ASC NULLS LAST/DESC NULLS FIRST.

**Bind/type/scope-check every arm.** Ordinary selected calls receive evaluated inputs; the straightforward executor may choose a deterministic order internally, but SQL exposes no fixed left-to-right order or portable callback count. Strictness suppresses provider invocation for NULL inputs without promising expression-input order. Boolean expressions may be reordered or omitted as PostgreSQL §4.2.14 permits, particularly in WHERE/HAVING. `TRUE OR failing_expression` and `x<>0 AND 10/x>1` are not guaranteed error guards.

CASE/COALESCE require dedicated bound control nodes, never ordinary eager-call desugaring. Searched CASE tests WHENs in order (Unknown is not True) and evaluates/coerces only its selected result/ELSE using that arm's prebound common-type plan, or returns typed NULL if none. Simple CASE evaluates its test once, then feature comparisons in order. **COALESCE evaluates each reached argument including its prebound common-type conversion, then tests the converted Value for NULL.** A converted nonnull result stops evaluation; a converted NULL continues, even if the unconverted child was nonnull. Reached conversion errors propagate; only later unreached children/conversions are skipped. All converted results NULL yields typed NULL. Do not assume type-owned coercions preserve NULLness.

Runtime skipping does **not** prevent binding errors, permitted immutable constant folding or earlier aggregate/window evaluation. A constant `1/0` in an unreachable CASE arm can fail during planning; CASE cannot protect aggregate arguments evaluated before scalar output. Parameter/function-local substitution can expose constants to a future folding owner. No optimizer is mandated in EXPR-CORE.

Properties conservatively combine maximum provider/child volatility, including no-argument calls, and separate row/parameter/stage dependence. Immutable does not mean constant. Optional folding requires concrete independent immutable constants and retains dependencies/errors; Stable reuse is statement-local under routine/type/session contracts, never cached across prepared executions; Volatile reevaluates wherever a row/execution needs its value and is not cached across needed rows. Skipped runtime CASE providers remain uninvoked. Routine/TX owns SQL-body snapshot semantics (stable/immutable calling snapshot versus volatile nested-query snapshots), not scalar Eval.

Check Context.Err at analysis/evaluation entry, unbounded child/row traversal, before/after providers and before publication. Providers receive the same cancellation context and cooperate; blocking external I/O is not forcibly cancelable. Later-row coercion/evaluation/constraint failure or cancellation publishes no statement fragment. Evaluate providers outside final publication locks; SELECT captures immutable input/metadata and evaluates outside target locks. TX/write owner stages, completes and publishes; Restore uses ordinary staging binder/executor and replaces only after complete success. These are retention/atomicity contracts, not new visibility/locking algorithms.

## 7. Extension rule

New scalar kinds extend closed parser/engine markers and existing bind dispatch: position, explicit policy validation, delegated Usage/coercions, dependencies, conservative properties and typed Eval/error are mandatory. Finalize one statement description/parameter set. No string-expression parser, substring evaluation, any-valued registry, separate prepared/DDL evaluator or bool-only predicate path.

Aggregate/window owners supply typed computed stage slots and legality; subquery owners supply scopes/frames/iteration/cardinality/result adaptation. Scalar Eval does not compute relational stages, publish DDL/DML or revalidate catalogs. QUERY-EXECUTION and feature owners specify stage order; no adjacent design is authored or consulted here.

## 8. Current callers → future clean cutover

This inventory is code read, not code fixed. TYPE-CORE/CAT-CORE implementations precede expression consumers. Reread symbols at each migration and remove every migrated alternate path, including tests, without aliases/shims.

| Current paths/symbols | Future migration / retained seam |
|---|---|
| parser/ast.go Insert.Rows, Select.Columns/Where, Predicate, ParameterValue | Rows→[][]Expr, expression targets/Where→Expr; remove migrated Predicate/string-only scalar paths. Retain star expansion, later query Order and type-owned modifier syntax. ObjectName already exists; do not restore string relation names. |
| parser/grammar.y, grammar_gen.go, lexer.go | Emit occurrence nodes through existing normalized lexer/goyacc grammar; regenerate output, never generated-only edits or second normalization. |
| engine/engine.go binding, Describe/describe local bind/column, Execute resolve | One private expressionBinder for all migrated occurrences, finalized usages/dependencies/access requirements/description, typed frames and evaluator; remove where bool/scalar indices. Preserve catalog identity-keyed relationStorage/staged CREATE and atomic INSERT; no name-map/store cutover. |
| engine.go prepareArgs/parameterColumn; engine/value.go input/parameter/literal | Root normalization/import, type codecs/occurrence plans replace migrated conversion switches. parameterColumn currently searches **diagnostics only**, not inference; replace its raw visitor with occurrence metadata, not another successful-path type scan. |
| engine/snapshot.go Values/compare/selectRows | Typed projected values/reusable scratch and bound predicates/projections; type-owned key families replace payload comparator under their owner. Keep immutable capture/nil-selection fast path and NULL sort policy. |
| conn.go PrepareContext/execute/direct Exec/Query; stmt.go NumInput/Exec/Query | Retain original SQL/AST/StatementDescription/BoundPlan, fixed params and catalog validity rather than AST/count-only preparation/rebinding every use; both prepared methods converge with direct path. SESSION/TX admission/lifecycle retained. |
| rows.go newRows/Next/metadata | ColumnDescriptor/typed scratch/ExportValue; retain Context/EOF/Close and descriptor/pin lifetime. Remove resolved parser-type switches under TYPE-CORE. |
| engine/control.go Restore/Dump | Shared raw expression visitor rejects every parameter occurrence with 42P02, replacing INSERT-Value-only scan. Keep CREATE/INSERT whitelist, ordinary stage.Execute and ReplaceCatalog; Dump uses type descriptors/codecs, no expression serializer invented here. Stored refs remint on replacement per catalog §7. |
| engine_test.go, parser_test.go, driver_test.go, reference_test.go | Migrate affected constructors/callers under owners without weakening independent expected rows/types/diagnostics/atomicity. Reuse referenceDiagnostic/referenceExtractDiagnostic/referenceAssertDiagnostic; they already compare complete exposed fields. |

Search Go/grammar callers for `Predicate|ParameterValue|parameterColumn|selectRows|literal\(|parameter\(|\.describe\(|\.Describe\(|\.Execute\(|\.Values\(`; inspect every match, including generated consumers/tests. Distinguish raw type modifiers, later query Order and referenceStep.selectRows fixture flag from obsolete scalar paths.

## 9. Bounded existing-backlog owners

These are implementation sessions, not new catalog tasks, prerequisite edges or implemented features. Each supplies independent public/unit/reference outcomes and concrete feature smoke when its capability exists.

| Owner | Bounded consumption and independent exit |
|---|---|
| PG18-EXPR-CORE (implemented CAT-CORE/TYPE-CORE) | Literals/columns/parameters, simple projections and existing equality filter on one seam; W1 typed params/columns and unit nullable equality. No arithmetic/functions/aliases/general clauses. Equality-projection syntax waits for its feature owner. |
| PG18-FUNC-LOGICAL | Typed AND/OR/NOT; complete truth matrices, no order/callback promise. |
| PG18-FUNC-COMPARE-OPS / PG18-FUNC-COMPARE-NULL | Selected comparisons/cross-width conversions / nonstrict NULL-distinct-truth predicates; independent BOOL/NULL rows and resolution errors, not strict equality reused for distinctness. |
| PG18-FUNC-CONDITIONAL / PG18-CAST-EXPLICIT | Lazy nodes/ordered executable CommonType (W5) / explicit syntax/type plans and cast/sizing diagnostics. |
| PG18-QUERY-CORE after PG18-ARCH-QUERY-EXECUTION | Source/filter/project/sort policies/frames; W2 typed projection versus filter and captured metadata. |
| PG18-QUERY-SUBQUERY | Correlation scopes/frames/iteration/width/cardinality; W4 forbidden sibling versus LATERAL. |
| PG18-QUERY-AGGREGATE / PG18-QUERY-WINDOW | Legality and typed stage results; earlier computations cannot be guarded by later CASE. |
| PG18-EXPR-COLLATION | Type-owned derivation/provider identities and exact conflict errors; no extra authority. |
| PG18-DDL-DEFAULT | Empty row/no outer scope, fresh per-row then assignment; no column capture. |
| PG18-DDL-GENERATED-STORED | Permitted same-row inputs, no self/generated/disallowed references or feature-disallowed volatility; owner controls dependency order and write-time results. |
| PG18-DDL-CHECK / PG18-INDEX-EXPRESSION | Same-row False-only rejection / same-table immutable expressions with no aggregates/subqueries; exact failures/atomic preservation and real computed keys. |
| PG18-DML-UPDATE-CORE | Every RHS reads old-row frame before replacement, then assignment/constraints/publication; simultaneous assignments and late-error atomicity. |
| PG18-DML-INSERT-RETURNING | Feature OLD/NEW/result frames/descriptors; exact returned rows/counts without invented old-row/conflict semantics. |

Stored-definition owners persist dependency-backed bound expressions via catalog staging, not name-text rebinding. Native/routine, authorization/RLS, session/TX, catalog/prepared/type owners keep their existing responsibilities; this breakdown does not absorb their algorithms.

## 10. Independently expected witnesses and evidence boundary

**Specifications only; no SQL/build/test/formatter/smoke was run by this implementer.** Future owners assert each backend against predetermined results before parity, direct/prepared database/sql where applicable. Unordered queries compare bags with multiplicity. Reuse serialized real PostgreSQL 18.0 UTF8/C fixtures/full-field helpers; Docker/image/network failure is failure, never mock/skip. Release-C/arithmetically derived expectations below require pinned-runtime confirmation before implementation acceptance, not claims of observation. Optional fields absent in the cited error construction stay absent; never fabricate object fields from references.

**W1 — typed column/parameter (A1/A2/A4).** Create expr_t(id INTEGER,v INTEGER); insert (1,1),(2,NULL),(3,2), affected=3. Ordered `SELECT id,v FROM expr_t ORDER BY id` expects [(1,1),(2,NULL),(3,2)], INT4/INT4. Prepared `SELECT v FROM expr_t WHERE v=$1`, argument 2, expects [(2)], parameter INT4/typmod -1 independent of Go integer kind. Units assert stable Origin.SubID separate from slot, fixed parameter/occurrence plans and call/type/cast dependencies; gap/invalid inference publishes no binding and allocates only after validation.

Output-context parameter boundary: future prepared `SELECT $1` with argument `hello` expects [('hello')], TEXT, parameter ID25/typmod -1/default collation; argument NULL still returns one TEXT-typed NULL. `SELECT $1 IS NULL` with no declared parameter type instead fails 42P18 / `could not determine data type of parameter $1`, Position absent, before execution: the release variable-parameter finalization loop adds no cursor location. Units assert contextual TEXT constraint, not a Go-kind or universal fallback; bare target grammar remains deferred.

**W2 — projection versus filtering (A1/A3).** Future `SELECT id,v=1 FROM expr_t ORDER BY id` expects [(1,TRUE),(2,NULL),(3,FALSE)], INT4/BOOL; `SELECT id FROM expr_t WHERE v=1 ORDER BY id` expects [(1)]. NULL exports nil without losing BOOL metadata. Equality-projection syntax is its feature owner's work, not silently added to narrow EXPR-CORE. Logical owners enumerate all nine ordered AND/OR pairs and three NOT inputs; CHECK accepts NULL, RLS check rejects NULL.

**W3 — unknown reference/location (A1/A2).** `SELECT missing FROM expr_t` expects ERROR/42703, primary `column "missing" does not exist`, Position 8. `SELECT /*é*/ missing FROM expr_t` expects Position 14 (not byte position 15). For this fixture, no detail/hint/context/object/internal-position fields: no exact/fuzzy match in release errorMissingColumn. No binding/result publication. Unit scopes also require same-scope 42702, local shadowing and distinct qualifier 42P01/alias ambiguity diagnostics.

**W4 — inaccessible sibling versus correlation (A2).** `SELECT o.id FROM expr_t AS o, (SELECT v) AS s` expects ERROR/42703, primary `column "v" does not exist`, Position 39 (prefix through `(SELECT ` is 38 characters); Detail `There is a column named "v" in table "o", but it cannot be referenced from this part of the query.`; Hint `To reference that column, you must mark this subquery with LATERAL.` No object/context/internal-position fields are added by this release branch. `SELECT o.id FROM expr_t AS o, LATERAL (SELECT v) AS s` expects bag [(1),(2),(3)], INT4, including the NULL-v row. Units prove permitted Depth/Scope/Frame/Slot, forbidden sibling traversal, local shadowing and alias hiding; owner separately checks lateral join-side legality.

Qualified-shadowing witness: create outer_t(v INTEGER) with (11),(22) and inner_t(id INTEGER) with (1), no applicable whole-row/function fallback. `SELECT (SELECT r.v FROM inner_t AS r) FROM outer_t AS r` must fail 42703, not correlate past local r. Release errorMissingColumn/searchRangeTableForCol predicts primary `column r.v does not exist`, Position 16, Detail `There is a column named "v" in table "r", but it cannot be referenced from this part of the query.`; Hint/object/context/internal-position fields absent. Confirm complete fields against the pinned release before implementation acceptance. Positive `SELECT (SELECT r.v FROM inner_t AS i) FROM outer_t AS r` expects bag [(11),(22)], INT4; no local r allows explicit outer correlation. Unqualified `SELECT (SELECT v FROM inner_t AS r) FROM outer_t AS r` also expects that bag: absence of local v permits column lookup outside, unlike qualified r.v. Units assert these distinct stopping conditions and the correct outer coordinates.

**W5 — skipped failing branch (A1/A4/A5).** Create branch_t(id INTEGER,v INTEGER); insert (1,0),(2,2), affected=2. `SELECT id,CASE WHEN v=0 THEN 7 ELSE 10/v END FROM branch_t ORDER BY id` expects [(1,7),(2,5)], INT4/INT4. `SELECT 10/v FROM branch_t` fails 22012 / `division by zero`; integer provider diagnostics, not expression-token location guessed by Eval. A unit provider proves zero-row branch not invoked. All arms still bind: missing column/incompatible type fails even in skipped arm. COALESCE's row-dependent skipped provider remains uninvoked. Constant `1/0` can fail in planning; aggregate argument division can fail before CASE; these caveats are not runtime lazy-node failures. No Boolean callback/order assertions.

COALESCE coercion units use predefined type-owned plans: first child is nonnull but its reached conversion returns typed NULL; second converts to value 7, so result is typed 7 and the third child/conversion remains uninvoked. Separately a reached conversion returning a sentinel error must return that same error without evaluating later arguments. With first converted result nonnull, all later conversions, including an error-producing one, remain uninvoked. These are independent provider/coercion expectations, not SQL user-cast syntax implemented here.

**W6 — ownership/volatility/cancellation/atomicity (A3/A5).** Concurrent/repeated executions of one plan with distinct args return their own values, without shared state. Mutate input bytes after import: retained values unchanged. Cursor across append/Reset/Restore retains original rows/types/labels; new reads see replacement. Units test buffers (PostgreSQL cannot prove Go ownership), stable state not reused across executions, volatile values not cached across needed rows, ordinary context.Canceled at entry/iteration, checks surrounding providers, late multirow error publishing no rows, and failed/canceled Restore preserving sentinel/lifetime. SQL reference fixtures independently establish applicable atomicity, not Go context ownership.

**W7 — one seam (A6/A7).** Review all §8 callers and §9 owners: typed nodes/source/properties/dependencies/errors, validated frames, explicit truth/legality policies, one finalization and no alternative migrated evaluator. Catalog validity and metadata stay prerequisite-owned; no scalar stage computes grouping/correlation iteration/publication.

`smoke_commands=[]`: design changes enable no runtime expression behavior; running W1–W5 against current narrow grammar is not a feature smoke. Root still runs fresh **full utility verification**, including existing public smoke (`go run ./cmd/smoke` from assigned worktree; existing four-line public assertion output), real reference/race/dependency/benchmark gates, then independent review. Baseline passing evidence cannot prove future expressions. Future implementation owners supply executable smoke argv for their new features and record expected versus observed results.

## 11. Sources and revision rationale

Primary PG18 documentation: [value expressions/evaluation §4.2](https://www.postgresql.org/docs/18/sql-expressions.html#SYNTAX-EXPRESS-EVAL), [scopes/LATERAL/WHERE §7.2](https://www.postgresql.org/docs/18/queries-table-expressions.html), [truth §9.1](https://www.postgresql.org/docs/18/functions-logical.html), [conditionals §9.18](https://www.postgresql.org/docs/18/functions-conditional.html), [volatility §36.7](https://www.postgresql.org/docs/18/xfunc-volatility.html), [conversion overview](https://www.postgresql.org/docs/18/typeconv-overview.html), [assignment](https://www.postgresql.org/docs/18/typeconv-query.html), [error fields](https://www.postgresql.org/docs/18/protocol-error-fields.html).

Release C (REL_18_0), not local 20devel, corroborates: [parse_relation.c](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/parser/parse_relation.c) refnameNamespaceItem/errorMissingColumn (scope/qualification and W3–W4 fields); [parse_target.c](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/parser/parse_target.c) resolveTargetListUnknowns; [parse_param.c](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/parser/parse_param.c) variable_coerce_param_hook (fixed identity, typmod/collation, inconsistency); [execExpr.c](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/executor/execExpr.c) T_CaseExpr/T_CoalesceExpr and strict-call dispatch (dedicated execution, not eager function sugar).

[parse_expr.c](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/parser/parse_expr.c) transformColumnRef first resolves a qualifier and then scans only that namespace item; transformCoalesceExpr wraps every argument in common-type coercion before execExpr.c's post-evaluation NULL jump.

[int.c](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/utils/adt/int.c) int4div constructs 22012/`division by zero` without a token Position; future fixtures confirm complete fields instead of blanket annotation.

[postgres.c](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/tcop/postgres.c) pg_analyze_and_rewrite_varparams rejects remaining unknown parameter types with 42P18/primary and no Position; distinguish this from parse_param's inconsistent-occurrence diagnostics.

Version provenance unchanged: /docs/18 navigation identifies 18.6; oracle remains `postgres:18.0-bookworm@sha256:3f55f8895c4ed50603e2fbdfc72fffeeaba3173321fee5cb825bbbeb30d9d854`, UTF8/C (`reference_helpers_test.go` referenceImage/setup). Only PG18-REF-ORACLE-18-6 owns minor alignment. `/home/rlew/projects/stuntgres/src-ref/postgres` 20devel and ramsql are research aids, never semantics precedence or release-runtime proof.

2026-10-04 compatible revision preserves original rationale: closed raw/bound seam, explicit scope coordinates/policies, delegated catalog/type authority, typed NULL, lazy controls, execution-private ownership and atomic staged publication. Compact tables replace repeated prescriptions; stale string-relation/SQLSTATE-only fixture and frozen-plan claims are superseded by inspected ObjectName/catalog/full-field seams and living-plan verification. SESSION/AUTHORIZATION/TX composition follows their established contracts without replacing them. Shared todo-conflict.md records **unimplemented** migrations, not fixed code; no consumer status is changed.

Explicit user decision **2026-10-04, “Follow type-system contract”**, after identifying prior expression §4.1 versus TYPE-SYSTEM §§3/5, supersedes exactly the blanket ban on TEXT fallback for unresolved output parameters. Current §4/W1 adopts the prerequisite's standalone SELECT/RETURNING contextual constraint; `$1 IS NULL` still fails 42P18. Bare parameter projections remain unimplemented; parser/binder/caller/reference migrations are recorded in the shared conflict report.

Review-1 M1/M2 compatible factual remediation: distinguish CASE selected-result coercion from COALESCE reached-argument conversion-before-NULL-testing; separate unqualified-column outer search from qualified-item shadowing. Added coercion-provider and qualified-shadowing/positive-correlation witnesses. Prerequisite contracts and design-only deferrals are unchanged.
