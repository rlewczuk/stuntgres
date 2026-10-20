# internal/engine/plan

The logical plan IR and the engine's concrete expression node types. Binding
builds these closed, immutable trees; execution opens and evaluates them in
the engine. Node `Eval` methods never name engine state: they use
`expr.Context`, or the `Runtime` capability declared here and implemented by
the engine's evaluation context. The package sits at layer L6 of
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
- **Three-valued logic** (`expression.go`): `Truth`, `True`, `False`,
  `Unknown` and `TruthOf`.

## Node runtime

`Runtime` (`runtime.go`) embeds `expr.Context` and is the only capability a
node evaluation needs beyond it. The engine's `*EvalContext` is its only
implementation.

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
  `EvalSQLRoutine`) holds that node's evaluation body in the engine.

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
- **Value copies.** The runtime's `Eval*` methods take nodes by value
  (about 300 bytes each); keep new hot paths on scalar arguments, like
  `FrameValue`.
- **Opaque CTE mutation.** `CTEDefinition.Mutation` is a `CTEMutation`
  payload that plan code only tests for presence; the engine recovers the
  analyzed statement through its `mutationBinding`.

## What stays in the engine

`EvalContext` and the `Eval*` bodies; `openQueryNode` and the node openers
(including `openFunctionCall`); `queryState`, `querySource`,
`indexedStage`, `finalRows`; `RowCursor` and `RowResult`; the binder that
constructs the nodes; the central switches that list node types (deparse,
remap, retag, conditional planning, query walks, join legality, aggregate
binding, defaults); the installed-function and extension-path nodes, which
still recover `*EvalContext` (16a assigned them to this package once they
no longer do). Until 10d the engine names these types through temporary aliases in
`plan_aliases.go`.

## Testing

The package has no unit tests of its own: its types are exercised through the
engine. Prologue seam (engine-owned):

```sh
make check RUN='^TestNodeRuntimePrologue$'
```

Public evaluation proof is the reference selection used for step 10, in
quick mode and with `REFERENCE=1`:

```sh
make check RUN='^TestReference(QuerySubquery|QueryCTE|CTEDML|QueryRecursive)'
make check RUN='^TestReference(QueryWindow|GroupingSets|QueryAggregate)'
make check RUN='^TestReference(InsertReturning|UpdateReturning|DeleteReturning|Merge(Delete|Insert|Nothing|Returning|Source|Update))'
make check RUN='^TestReference(Domain|GeneratedStored|GeneratedVirtual)'
make check RUN='^TestReference(QueryJoins|QueryOuterJoins|ViewNative|DumpRound|DeparseConstraints)'
```
