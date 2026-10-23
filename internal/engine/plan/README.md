# internal/engine/plan

The logical plan IR and the engine's concrete expression node types. Binding
builds these closed, immutable trees; [`exec`](../exec/README.md) opens and
evaluates them. Node `Eval` methods never name engine state: they use
`expr.Context`, or the `Runtime` capability declared here and implemented by
`exec.Context`. The package sits at layer L6 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports only `catalog`, `expr`, `functions`, `ids`, `lock`, `parser`,
`sqlstate`, `sqltype` and the standard library, and never the engine. See
[ENGINE-SPLIT-plan.md](../../../docs/ENGINE-SPLIT-plan.md) (step 10).

## What it owns

- **Plan IR** (`query.go`, `query_nodes.go`, `query_set.go`): `Node` (a
  closed interface: every implementation lives here), `Query` (a bound
  query and its output demands) and `QuerySchema`, which every node embeds
  for `Schema()`. Nodes: `SourceNode`, `SingletonNode`, `ValuesNode`,
  `FilterNode`, `SortNode`, `ProjectNode`, `AggregateNode`, `DerivedNode`,
  `DistinctNode`, `DistinctOnNode`, `JoinNode`, `LimitNode`,
  `EvaluationNode`, `WorktableScanNode`, `RecursiveUnionNode`,
  `RowLockNode`, `SetNode`, `FunctionFromNode`, `SetProjectNode` and
  `WindowNode`.
- **Plan records** (`query_records.go`, `query_search.go`, `query.go`,
  `expression_window.go`, `expression_aggregate.go`): `SortKey`,
  `Grouping`, `AggregateCall`, `WindowCall`, `WindowOperation` (and its
  `Window*` constants), `WindowSpec`, `WindowFrame`, `WindowBound`,
  `SearchRecipe`, `CycleRecipe`, `CTEDefinition`, `CTEMutation`,
  `FunctionCorrelation`, `SetTemplate`, `SetStep`, `SetStepKind`,
  `SetSlot`, `RowMark` and `SQLJSONArrayQuery`.
- **Expression nodes** (`expression*.go`, `assignment_value.go`, the
  `series_*`, `subscripts_*` and `unnest_*` files): the `XxxExpr` types
  (`LiteralExpr`, `ColumnExpr`, `ParameterExpr`, `CoercionExpr`,
  `CaseExpr`, `RowExpr`, `SubqueryExpr`, `WindowExpr`, ...) with their
  `Eval` methods, and the set-returning `UnnestExpr`, `SeriesExpr` and
  `SubscriptsExpr` with their source protocol and cursors.
- **Installed extension nodes** (`expression_installed.go`,
  `expression_extension_paths.go`): `InstalledTextExpr` (unaccent,
  `daitch_mokotoff`), `InstalledCryptoExpr`, `InstalledUUIDOsspExpr`,
  `InstalledMetaphoneExpr`, `InstalledLevenshteinExpr`,
  `InstalledPgrowlocksExpr` and `InstalledSoundexExpr` wrap a `functions` kernel with the captured
  installed procedure and its object reference; `ExtensionPathsExpr` is the
  `pg_extension_update_paths` set source. The engine validates their
  identity against the extension providers.
- **Three-valued logic** (`expression.go`): `Truth`, `True`, `False`,
  `Unknown` and `TruthOf`.
- **Structural NULL** (`structural_null.go`): `StructuralNull` recognizes a
  literal NULL under transparent collation and coercion nodes; the engine's
  column-default elision uses it (moved here in catalog 5i).
- **Walkers** shared by binding and execution: `WalkExpression`,
  `ExpressionChildren`, `WalkDemandedExpression` and
  `LiteralSearchedCaseMatch` (`expression_walk.go`);
  `RewriteQueryExpressions`, `VisitExecutableQuery`, `VisitCTEQuery`,
  `QueryInputs` and `QueryExpressions` (`query_walk.go`);
  `FunctionFromConjuncts`, `FunctionFromRestriction`,
  `PartitionFunctionConjuncts` and `JoinExpressionSides`
  (`function_from_filter.go`).
- **Copy-on-change mappers** (`expression_map.go`): `MapRow`,
  `MapRowAnalysis` (with composite expansions), `MapRowComparisonExpansions`
  and `MapScalarArrayChildren`, shared by binding, preflight, retag and view
  remapping; `MapSubscript` (`expression_subscript_map.go`); `NullLiteral`
  (`literal_null.go`); `SQLJSONSource` (`expression_sqljson_source.go`)
  strips a JSON FORMAT input coercion.
- **Node-tree output** (`node_tree.go`): `BoundTree` is the
  `pg_node_tree` carrier (`sqltype.StructuralTree`) of a bound expression;
  `AppendBoundTree` writes its native structural text (not PostgreSQL
  `nodeToString`) and `CatalogTree` stores it as a catalog value.
  `pg_get_expr` and the catalog producers read it through these names.
- **Generation admission** (`generation.go`): `GenerationImmutable` (a
  generation expression admits only immutable surviving providers) and
  `GenerationHasTableOID` (it reads the owner's `tableoid`), used by
  generated columns, index snapshot repair and partition checks.
  `SubscriptChildren` (`expression_array.go`) lists a subscript's base and
  bounds in caller storage for these and the other walkers.

## Node runtime

`Runtime` (`runtime.go`) embeds `expr.Context` and is the only capability a
node evaluation needs beyond it. `*exec.Context` is its only implementation.

- `BeginEvaluation` is the shared prologue: missing-context check,
  domain-check wiring while a domain value is active, then cancellation,
  in that order. The package function `BeginEvaluation(ctx)` recovers the
  runtime and runs it; a nil or foreign context yields the prologue's
  missing-context error.
- `ApplyCoercion` applies a coercion plan with the live statement scope's
  resolvers; `RowAbsent` reports an absent whole-row source.
- `FrameValue` and `ParameterValue` are the plain column and parameter reads
  by scalar coordinates, so the hot path copies no node.
- One `Eval*` method per frame or executor node (`EvalColumn`, `EvalGroup`,
  `EvalGrouping`, `EvalTableOID`, `EvalReturningColumn`,
  `EvalReturningWholeRow`, `EvalMergeAction`, `EvalCachedOrder`,
  `EvalWindow`, `EvalDomainValue`, `EvalUpdateOutput`, `EvalSubquery`,
  `EvalSQLRoutine`) holds that node's evaluation body in `exec`.
- `EvalInstalledText` runs the prologue, checks the captured provider and
  evaluates the kernel; `OpenExtensionPaths` lists the named extension's
  version pairs from the live catalog. The provider check and the catalog
  read stay in the engine, behind `exec`'s preparation value and host.

Statement-only nodes run the prologue and then read `FunctionContext()`,
`FunctionInput()` and `FunctionOutput()`; frame and executor nodes delegate
their whole `Eval` to the runtime.

## Invariants

- **Closed, immutable trees.** Execution never resolves names. A bound tree
  is never mutated after publication; rewrites copy.
- **No engine state, no locks.** Nothing here names the engine's evaluation
  context, statement scope or binder, and the package has no mutex.
- **Evaluation order and errors.** Node `Eval` keeps the engine's
  evaluation order, errors, SQLSTATEs and positions; the prologue order
  above is part of the contract.
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
  Function-cast calls also retain the ordinary selected routine input assembly:
  all supplied/synthetic arguments are written operands, with validated formal
  slots and inverse child slots. Normal calls and cast calls share assembly
  closure and validation. Provider volatility comes from the captured procedure;
  this does not expand the accepted SQL routine attribute set.
- **Value copies.** The runtime's `Eval*` methods take nodes by value
  (about 300 bytes each); keep new hot paths on scalar arguments, like
  `FrameValue`.
- **Opaque CTE mutation.** `CTEDefinition.Mutation` is a `CTEMutation`
  payload that plan code only tests for presence; the engine recovers the
  analyzed statement through `analyze.MutationBinding`.

## What stays outside

Execution lives in [`exec`](../exec/README.md): the evaluation context and
the `Eval*` bodies, `OpenNode` and the node openers (including
`OpenFunctionCall`), the runtime records, `RowCursor` and `RowResult`. The
engine keeps the binder that constructs the nodes; the central switches
that list node types (deparse, remap, retag, conditional planning, join
legality, aggregate binding, defaults, `table.ValidateGenerationPlan`); the installed-node validators
(`validateInstalled*`; `ValidInstalledText` and `ClosedPgcryptoDefault` are in
`analyze`) and `bindExtensionPaths`. The engine names the types here directly; no aliases
remain.

`DatumCastExpr` retains selected strict IO/binary semantics and separately planned
target sizing; it performs no live pair lookup. INOUT calls target input with
typmod `-1`, then applies sizing in the retained explicit/assignment context.
The same sizing tail handles binary casts and NULLs; Rebuild removes obsolete
sizing when the target becomes unconstrained. SQL function casts use the real
`SQLRoutineExpr`, not that strict wrapper. `ArrayCastExpr` maps a selected element
expression through an execution-owned scoped element frame while preserving the
source array's dimensions/lower bounds and one source evaluation. Binding
projects both outer usages' typmods and collations onto the element conversion.
Declared IO casts derive intrinsic volatility from the source output and target
input codecs, using the same session-dependence authority as fixed coercions.
Enum, composite, range, multirange and array IO are STABLE under the released
procedure declarations; domain codecs delegate to their bases. XID→REAL IO
remains immutable. Rebuilding a retained cast merges child effects and reapplies
that intrinsic classification.

## Testing

Pure codec-plan units cover contextual IO/binary sizing, multibyte boundaries,
NULL, retained Rebuild, custom descriptor IO volatility and generation admission:

```sh
make check RUN='^TestCastDefinition(IOTypmodSizing|IODescriptorVolatility)$'
make check RUN='^TestCastDefinition(IOTypmodSizing|IODescriptorVolatility)$' RACE=1
make check RUN='^TestReferenceCastDefinition(IOTypmod|IOVolatility)$'
make check RUN='^TestReferenceCastDefinition(IOTypmod|IOVolatility)$' REFERENCE=1
```

Public SQL/replay witnesses live in `reference/operators`; these units do not
replace selected dual-mode proof. Prologue seam (engine-owned):

```sh
make check RUN='^TestNodeRuntimePrologue$'
```

Node-tree output and generation admission (engine-owned, 10e):

```sh
make check RUN='^Test(Generated|ArrayGenerated|DeparseConstraints|ExprCollationFrontiers|TemporaryTableIndexComposition)'
```

Public evaluation proof is the reference selection used for step 10, in
quick mode and with `REFERENCE=1`:

```sh
make check RUN='^TestReference(QuerySubquery|QueryCTE|CTEDML|QueryRecursive)'
make check RUN='^TestReference(QueryWindow|GroupingSets|QueryAggregate)'
make check RUN='^TestReference(InsertReturning|UpdateReturning|DeleteReturning|Merge(Delete|Insert|Nothing|Returning|Source|Update))'
make check RUN='^TestReference(Domain|GeneratedStored|GeneratedVirtual)'
make check RUN='^TestReference(QueryJoins|QueryOuterJoins|ViewNative|DumpRound|DeparseConstraints)'
make check RUN='^TestReference(Extension|Pgcrypto|UUIDOssp|Metaphone|Levenshtein|Soundex|Intagg)'
```
