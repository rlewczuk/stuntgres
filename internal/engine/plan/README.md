# internal/engine/plan

The logical plan IR and the engine's concrete expression node types. Binding
builds these closed, immutable trees; [`exec`](../exec/README.md) opens and
evaluates them. Node `Eval` methods never name engine state: they use
`expr.Context`, or the `Runtime` capability declared here and implemented by
`exec.Context`.

## Imports

Layer L6 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture). It imports
only `catalog`, `expr`, `functions`, `ids`, `lock`, `parser`, `sqlstate`,
`sqltype` and the standard library, never the engine.

## What it owns

- **Plan IR** (`query.go`, `query_nodes.go`, `query_set.go`): `Node` (a closed
  interface: every implementation lives here), `Query` (a bound query and its
  output demands) and `QuerySchema`, which every node embeds for `Schema()`.
  Nodes: `SourceNode`, `SingletonNode`, `ValuesNode`, `FilterNode`, `SortNode`,
  `ProjectNode`, `AggregateNode`, `DerivedNode`, `DistinctNode`,
  `DistinctOnNode`, `JoinNode`, `LimitNode`, `EvaluationNode`,
  `WorktableScanNode`, `RecursiveUnionNode`, `RowLockNode`, `SetNode`,
  `FunctionFromNode`, `SetProjectNode` and `WindowNode`.
- **Plan records** (`query_records.go`, `query_search.go`, `query.go`,
  `expression_window.go`, `expression_aggregate.go`): `SortKey`, `Grouping`,
  `AggregateCall`, `WindowCall`, `WindowOperation` (and its `Window*`
  constants), `WindowSpec`, `WindowFrame`, `WindowBound`, `SearchRecipe`,
  `CycleRecipe`, `CTEDefinition`, `CTEMutation`, `FunctionCorrelation`,
  `SetTemplate`, `SetStep`, `SetStepKind`, `SetSlot`, `RowMark` and
  `SQLJSONArrayQuery`.
  Each declaration-specific aggregate slot retains its original owning declaration,
  reference and opaque defining recipe; windows carry the recipe on their original occurrence's `Info`.
  The token separates graft ancestry, not catalog permission or execution.
  Query `Grouping` keys and window peer `SortKey`s retain their selected original
  equality declaration beside its reference and defining recipe in the existing
  key allocation. This proof is independent of a substituted key expression.
  Private semantic-tree groupings retain no callable declaration or recipe.
- **Expression nodes** (`expression*.go`, `assignment_value.go`, the `series_*`,
  `subscripts_*` and `unnest_*` files): the `XxxExpr` types (`LiteralExpr`,
  `ColumnExpr`, `ParameterExpr`, `CoercionExpr`, `CaseExpr`, `RowExpr`,
  `SubqueryExpr`, `WindowExpr`, ...) with their `Eval` methods, and the
  set-returning `UnnestExpr`, `SeriesExpr` and `SubscriptsExpr` with their source
  protocol and cursors.
  Invocation-only PL assignment wrappers preserve constant-planning error origin
  and reuse datum IO/sizing/volatility; stored-provider validation rejects them.
  The mutation projection exposes its captured datum, bounds and RHS to ordinary
  expression lowering, while shared targets evaluate runtime bounds before the
  leaf. Each output row privately reconstructs the same original datum; activation
  publication and detached field-ancestor reconstruction belong to the engine.
- **Generated range constructors** (`range_constructor.go`):
  `RangeConstructorExpr` captures the actual type-owned procedure and result
  generation, with ordinary callable coercion, dependency and EXECUTE admission.
  Its closed native declaration invokes typed range/member construction directly;
  explicit VARIADIC retains the genuine associated range-array formal.
- **Installed extension nodes** (`expression_installed.go`,
  `expression_cube.go`, `expression_cube_spatial.go`, `expression_earth_coordinate.go`, `expression_earth_point.go`, `expression_extension_paths.go`):
  `InstalledTextExpr` (unaccent, `daitch_mokotoff`), `InstalledCryptoExpr`,
  `InstalledUUIDOsspExpr`, `InstalledMetaphoneExpr`, `InstalledLevenshteinExpr`,
  `InstalledCubeExpr`, `InstalledCubeSpatialExpr`, `InstalledEarthExpr`, `InstalledEarthPointExpr`, `InstalledPgrowlocksExpr`, `InstalledVerifyHeapamExpr` and
  `InstalledSoundexExpr` wrap a `functions` kernel with the captured
  installed procedure and its object reference; `ExtensionPathsExpr` is the
  `pg_extension_update_paths` set source. The engine validates their identity
  against the extension providers.
- **Three-valued logic** (`expression.go`): `Truth`, `True`, `False`, `Unknown`
  and `TruthOf`.
- **Structural NULL** (`structural_null.go`): `StructuralNull` recognizes a
  literal NULL under transparent collation and coercion nodes; column-default
  elision uses it.
- **Walkers** shared by binding and execution: `WalkExpression`,
  `ExpressionChildren`, `WalkDemandedExpression`, `LiteralSearchedCaseMatch` and
  `ExpressionUsesCompositeField` (the field dependency test of composite ALTER)
  (`expression_walk.go`); `RewriteQueryExpressions`, `VisitExecutableQuery`,
  `VisitCTEQuery`, `QueryInputs` and `QueryExpressions` (`query_walk.go`);
  `FunctionFromConjuncts`, `FunctionFromRestriction`,
  `PartitionFunctionConjuncts` and `JoinExpressionSides`
  (`function_from_filter.go`).
- **Copy-on-change mappers** (`expression_map.go`): `MapRow`, `MapRowAnalysis`
  (with composite expansions), `MapRowComparisonExpansions` and
  `MapScalarArrayChildren`, shared by binding, preflight, retag and view
  remapping; `MapSubscript` (`expression_subscript_map.go`); `NullLiteral`
  (`literal_null.go`); `SQLJSONSource` (`expression_sqljson_source.go`) strips a
  JSON FORMAT input coercion.
- **Node-tree output** (`node_tree.go`): `BoundTree` is the `pg_node_tree`
  carrier (`sqltype.StructuralTree`) of a bound expression; `AppendBoundTree`
  writes its native structural text (not PostgreSQL `nodeToString`) and
  `CatalogTree` stores it as a catalog value. `pg_get_expr` and the catalog
  producers read it through these names.
- **Generation admission** (`generation.go`): `GenerationImmutable` (a
  generation expression admits only immutable surviving providers) and
  `GenerationHasTableOID` (it reads the owner's `tableoid`), used by generated
  columns, index snapshot repair and partition checks. `SubscriptChildren`
  (`expression_array.go`) lists a subscript's base and bounds in caller storage
  for these and the other walkers.

## Node runtime

`Runtime` (`runtime.go`) embeds `expr.Context` and is the only capability a node
evaluation needs beyond it. `*exec.Context` is its only implementation.

- `BeginEvaluation` is the shared prologue: missing-context check, domain-check
  wiring while a domain value is active, then cancellation, in that order. The
  package function `BeginEvaluation(ctx)` recovers the runtime and runs it; a nil
  or foreign context yields the prologue's missing-context error.
- `ApplyCoercion` applies a coercion plan with the live statement scope's
  resolvers; `RowAbsent` reports an absent whole-row source.
- `FrameValue` and `ParameterValue` are the plain column and parameter reads by
  scalar coordinates, so the hot path copies no node.
- One `Eval*` method per frame or executor node (`EvalColumn`, `EvalGroup`,
  `EvalGrouping`, `EvalTableOID`, `EvalReturningColumn`, `EvalReturningWholeRow`,
  `EvalMergeAction`, `EvalCachedOrder`, `EvalWindow`, `EvalDomainValue`,
  `EvalUpdateOutput`, `EvalSubquery`, `EvalSQLRoutine`) holds that node's
  evaluation body in `exec`.
- `EvalInstalledText`, `EvalInstalledCubeSpatial` and `EvalInstalledEarthPoint`
  run the prologue, validate captured provider authority and evaluate the kernel,
  including strict NULL calls. Spatial and point wrappers retain Procedure/Ref
  through reconstruction; stored remap/retag rebuilds their checked plans against
  destination descriptors.
  `OpenExtensionPaths` reads version pairs from the live catalog. Provider
  checks and catalog reads stay in the engine behind `exec`'s runtime seam.

Statement-only nodes run the prologue and then read `FunctionContext()`,
`FunctionInput()` and `FunctionOutput()`; frame and executor nodes delegate their
whole `Eval` to the runtime.

## Invariants

- **Closed, immutable trees.** Execution never resolves names. A bound tree is
  never mutated after publication; rewrites copy only changed branches, and
  analyzed dependencies stay attached even when execution removes a branch.
- **No engine state, no locks.** Nothing here names the engine's evaluation
  context, statement scope or binder, and the package has no mutex.
- **Evaluation order and errors.** Node `Eval` fixes evaluation order, errors,
  SQLSTATEs and positions; the prologue order above is part of the contract.
- **Value copies.** The runtime's `Eval*` methods take nodes by value (about 300
  bytes each); keep new hot paths on scalar arguments, like `FrameValue`.
- **Saved simple CASE input.** `CaseExpr.Test` is evaluated once. Each reached
  branch retains its own selected `CaseBranch.Test` conversion tree over a
  lexical `CaseTestExpr`; that conversion runs before the branch's WHEN operand.
  The ordinary cast nodes retain function, IO, domain, NULL and ACL semantics.
  Runtime input frames save and restore across nested CASE and failures; they
  are not SQL parameters or cached callback results. Walkers include these
  templates so effects, dependencies and remapping cannot disappear.
  Constant/join optimizers may use `FixedCaseTest` only when the retained tree
  already is a fixed conversion; they never recreate a substitute for a user
  recipe. Stored simple CASE keeps its original syntax and retained recipe
  requirements rather than duplicating the source expression.
- **Function-cast calls** retain the ordinary selected routine input assembly:
  all supplied/synthetic arguments are written operands, with validated formal
  slots and inverse child slots. Normal calls and cast calls share assembly
  closure and validation; provider volatility comes from the captured procedure.
- **Cast nodes.** `DatumCastExpr` retains selected strict IO/binary semantics and
  separately planned target sizing; it performs no live pair lookup. INOUT calls
  target input with typmod `-1`, then apply sizing in the retained
  explicit/assignment context; the same sizing tail handles binary casts and
  NULLs, and Rebuild removes obsolete sizing when the target becomes
  unconstrained. SQL function casts use the real `SQLRoutineExpr`, not that
  strict wrapper. `ArrayCastExpr` maps a selected element expression through an
  execution-owned scoped element frame, preserving the source array's
  dimensions/lower bounds and one source evaluation; binding projects both outer
  usages' typmods and collations onto the element conversion. Declared IO casts
  derive intrinsic volatility from the source output and target input codecs,
  using the same session-dependence authority as fixed coercions; domain codecs
  delegate to their bases. Rebuilding a retained cast merges child effects and
  reapplies that classification.
- **Opaque CTE mutation.** `CTEDefinition.Mutation` is a `CTEMutation` payload
  that plan code only tests for presence; the engine recovers the analyzed
  statement through `analyze.MutationBinding`.

## What stays outside

Execution lives in [`exec`](../exec/README.md): the evaluation context and the
`Eval*` bodies, `OpenNode` and the node openers (including `OpenFunctionCall`),
the runtime records, `RowCursor` and `RowResult`. The engine keeps the binder
that constructs the nodes; the central switches that list node types (deparse,
remap, retag, conditional planning, join legality, aggregate binding, defaults,
`table.ValidateGenerationPlan`); the installed-node validators
(`validateInstalled*`; `ValidInstalledText` and `ClosedPgcryptoDefault` are in
`analyze`) and `bindExtensionPaths`. Callers name the types here directly,
without aliases.

## Testing

Package units cover cast IO/binary sizing, multibyte boundaries, NULL, retained
Rebuild, IO volatility and the walk through installed-routine wrappers. The
evaluation prologue is pinned by an `exec` unit (it needs the concrete context)
and generation admission by an engine unit; public evaluation lives in the
query reference packages and cast definitions in
[reference/operators](../../../reference/operators/README.md#testing). Add
`REFERENCE=1` for the oracle.

```sh
make check RUN='^TestCastDefinitionIO'
make check RUN='^TestNodeRuntimePrologue$'
make check RUN='^TestReferenceCastDefinitionIO'
```
