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

## Node protocol

- **`Composite`** is a node whose expression children are all lexical:
  `Children()` lists them in evaluation order (entries may be nil; the slice
  may alias the node and must not be modified), and `Rebuild(Info, children)`
  returns a revalidated copy with new metadata and children of the same
  length, leaving the receiver unchanged. Every function node is a member.
- **`Walk`** visits a node and then its children depth first, skipping nil.
  The caller supplies the children enumerator, so the engine's own switch
  decides lexical boundaries (a subquery's plan is a separate frame) and falls
  back to `Composite` for protocol nodes.
- **`Map`** maps the non-nil children with the engine's changed-bit callback
  and rebuilds only when one changed, on a fresh slice with `InfoOf(node)`.
  Composite nodes never carry `UnknownNull` or `RegclassRef`.
- A central engine switch adopts the protocol with one final
  `case expr.Composite:` branch. Explicit cases before it keep precedence:
  engine wrappers that embed a protocol node (installed extension routines)
  keep their cases, because their catalog identity is engine state.

Optional hooks (`hooks.go`), each answering one switch's question; a node
without a hook gets the switch's previous generic behavior:

- **`Intrinsic`** (`IntrinsicVolatility`): the node's own planned volatility,
  excluding children. Planned volatility merges it with the children;
  generated-column admission requires it to be `Immutable` and admits every
  child.
- **`Folding`** (`PlanningFold`): `FoldConstant` (default) folds an immutable
  call with literal children; `FoldNever` leaves the call to execution and
  never counts it as constant inputs; `FoldOverlapsRoute` and
  `FoldQuoteCapture` name engine-owned folds that the engine dispatches by
  kind, not by node type.
- **`StrictFolding`** (`StrictNull`): when a literal NULL child replaces the
  call with a typed NULL: before simplifying children, after, or after with
  only usage and position kept.
- **`StoredProvider`** (`ValidateStoredProvider`): the node may appear in a
  stored generation plan; validation rejects protocol nodes without it.
- **`Retaggable`** (`Retag`): catalog retag for nodes with derived inputs. The
  engine passes the retagged metadata and children, its change bit and a
  `Retag` environment (retained descriptors, stored-collation remap, collation
  derivation); the node returns whether anything changed.
- **`Annotated`** (`Annotation`/`WithAnnotation`): an opaque engine value that
  rebuilds keep, such as a quote-capture origin.
- **`Renderable`** (`Render`, `render.go`): the node's presentation as one
  `Rendering` value, which every renderer that lists nodes reads. Node-tree
  output reads `Tree` and `Inputs`; the deparser reads the SQL `Form`: `Leaf`
  text, a `Call` (name `Quote` rule, written arguments with labels and
  VARIADIC), an `Infix` operator (namespace and a `Grouping` rule), or a
  keyword-syntax `Template` of `Piece`s. Each `RenderArg` says whether the
  argument shows implicit coercions, sits in function-argument position,
  replaces the inherited unknown-text flag, or displays a cast to its resolved
  formal type (`CastForm`); `SimpleCast` marks calls whose explicit cast keeps
  them bare. These fields encode per-call differences in PostgreSQL's
  ruleutils, so the deparser holds no per-node knowledge. The renderer owns
  identifier quoting, literals, casts and parentheses.
  Stored scalar SQL (replay) reads the same value plus `Replay`, the parts
  where replay differs from display: it renders every argument in order before
  assembling the form (literal capture and dependency order follow argument
  order), qualifies call names with `pg_catalog`, always quotes labels and
  parenthesizes operators, and ignores the display-only argument fields.
  `Replay` adds a lazy `Validate` (stored provider checks and refusals such as
  set-returning calls; display never runs it, so it cannot change deparse),
  replay `Name`/`Text`, a replay template (`Pieces` over `Args`, for POSIX
  `SUBSTRING ... FROM`, EXTRACT's field literal, `SIMILAR TO ... ESCAPE` over an
  `Absorbed` normalizer and the `num_nulls` field notation), `QuoteNamespace`
  for written operator schemas, the `CatalogSnapshot` and `VisibilitySnapshot`
  demands, `RelationOnly` (random calls) and carried `Types` that must belong
  to the definition's catalog (the pg_sleep clock). Each `RenderArg` names a
  `Transport` that keeps its type on replay: always cast to `TransportType`,
  or cast NAME, typed or ambiguously spelled literals, or type NULL intervals.
  Binary and string operators state their `pg_catalog` schema, which display
  shows bare and replay qualifies.

Adopted switches: the lexical walk (`boundExpressionChildren`), join legality
(`joinExpressionSides`), planned volatility, generated admission
(`generationImmutable`, `generationHasTableOID`, `validateGenerationPlan`),
conditional planning (`simplifyConditionalNode`, `conditionalConstantInputs`),
retag (`mapBoundExpressionReader`), remap (`remapScalarExpression`), node-tree
output (`appendBoundTree`), SQL deparse (`pgRenderer.sql`, interpreted in
`deparse_rendering.go`) and stored scalar SQL (`scalarRenderer.sql` and
`scalarFunctionSQL`, interpreted in `scalar_rendering.go`). The walk keeps one
explicit `Timezone` case: it visits the value before the zone, unlike
PostgreSQL, a divergence kept until a compatibility fix. The preflight and
aggregate switches still list function nodes.

## What stays in the engine

The concrete nodes (column, parameter, coercion, CASE, row, subquery and the
rest), the plan IR, `EvalContext` and its private state, binding, and the
recovery helper `evalContext`. Engine nodes recover `*EvalContext` from the
`Context` they receive.

Engine nodes embed `expr.Info` and the engine and `functions` use the
`expr` names directly; step 2d removed the temporary aliases. Since step 3
(F1), `functions` nodes embed `Info` and evaluate against `Context` directly,
with no type parameters.

## Testing

Unit tests pin the helpers: property merging, intrinsic rules, `InfoOf`,
the positioned errors, and the `Walk`/`Map` protocol helpers. The hook
implementations are pinned by the `functions` tests (see its README).
Expression evaluation itself is covered by the engine and `functions` tests
and the `reference/` suite.

```sh
make check RUN='^Test(MergeProperties|IntrinsicPropertyRules|InfoOf|ErrorAt|WithPosition)'
make check RUN='^Test(WalkVisitsPreOrderAndSkipsNil|MapUnchangedKeepsNode|MapChangedRebuildsCopy|MapPropagatesErrors)$'
```
