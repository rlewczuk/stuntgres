# internal/engine/expr

The expression contract shared by the engine and `internal/engine/functions`.
It imports only `ids`, `parser`, `sqlstate`, `sqltype` and the standard
library, and sits at layer L2 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It owns types and small pure helpers; it has no state and no mutex.

## The contract

- **`Expr`** is a bound expression: result usage, source position,
  `Properties`, collation metadata and `Eval(Context)`. Dependence and
  volatility propagate independently.
- **`Properties`** and **`Volatility`** (`Immutable`, `Stable`, `Volatile`)
  are the analysis properties. `MergeProperties` combines two operands: the
  higher volatility wins and each dependence flag is ORed.
- **`IntrinsicPropertyRule`** states the properties a builtin call must carry.
  `IntrinsicProperties` gives a call's starting properties and
  `PropertiesMatch` checks them; the exact rules also reject any dependence,
  `VolatileOnly` checks volatility alone.
- **`Info`** is the metadata every node embeds: usage, position, properties,
  the unknown-NULL marker, a captured REGCLASS reference, collation conflict
  data, and the optional routine references. Its methods implement the
  metadata half of `Expr`. `InfoOf` copies a node's metadata for a wrapper;
  it does not copy `UnknownNull` or `RegclassRef`.
- **`RoutineReference`** and **`RoutineRecipeReference`** are opaque identity
  tokens for the selected routine and for the stored recipe a grafted node
  came from. They carry no catalog or permission authority.
- **Positioned errors.** `ErrorAt` builds a SQLSTATE ERROR positioned at a
  source position (a zero position leaves it unpositioned). `WithPosition`
  copies an existing SQLSTATE error with a new position.

## `Context` capability groups

`Context` is the evaluation runtime visible outside the engine. The engine's
`*EvalContext` is its only implementation. Every method is a per-evaluation
capability of the current statement; none may be backed by a global or
package-level variable. Evaluation always receives a non-nil `Context`.

- **Core:** `FunctionContext`, the statement's cancellation context.
- **Presentation environments:** session input/output, plus JSON, string and
  format outputs and JSONB record input bound to the live statement scope.
- **Argument and property inspection:** literal NULL, constant `OVERLAPS`
  operands, sleep argument presence, whole-row NULL presence, intrinsic
  property match.
- **RNG and entropy:** session random owners (`RandomOwner`), the UUID clock
  and entropy, pgcrypto entropy and FIPS mode.
- **Clocks:** statement time, wall time, time-of-day zone, sleep clock.
- **Regex cache** and **conversion handlers.**

## What stays in the engine

The concrete nodes (column, parameter, coercion, CASE, row, subquery and the
rest), the plan IR, `EvalContext` and its private state, binding, and the
recovery helper `evalContext`. Engine nodes recover `*EvalContext` from the
`Context` they receive.

Temporary aliases until step 2d: in the engine, `BoundExpr`,
`ExprProperties`, `ExprContext`, `Volatility`, `expressionInfo`, the
volatility constants and the forwarding helpers `boundExpressionInfo`,
`mergeExpressionProperties` and `expressionError` (`expr_aliases.go`); in
`functions`, `RoutineReference`, `RoutineRecipeReference`, `RandomOwner`,
`IntrinsicPropertyRule` and its constants (`expr_aliases.go`).
Since step 3 (F1), `functions` nodes embed `Info` and evaluate against
`Context` directly, with no type parameters.

The node protocol (`Composite`, render hooks, walk/map helpers) arrives in
step 2c.

## Testing

Unit tests pin the helpers: property merging, intrinsic rules, `InfoOf` and
the positioned errors. Expression evaluation itself is covered by the engine
and `functions` tests and the `reference/` suite.

```sh
make check RUN='^Test(MergeProperties|IntrinsicPropertyRules|InfoOf|ErrorAt|WithPosition)'
```
