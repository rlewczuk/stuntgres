# internal/engine/expr

The expression contract shared by the engine and `internal/engine/functions`:
types and small pure helpers, including invocation-private cursor handles. It has
no shared state and no mutex. Layer L2 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture); it
imports only `ids`, `parser`, `sqlstate`, `sqltype` and the standard library.

## The contract

- **`Expr`**: a bound expression with result usage, source position,
  `Properties`, collation metadata and `Eval(Context)`.
- **`Properties`/`Volatility`** (`Immutable`, `Stable`, `Volatile`): dependence
  and volatility propagate independently; `MergeProperties` takes the higher
  volatility and ORs each dependence flag. `IntrinsicPropertyRule`,
  `IntrinsicProperties` and `PropertiesMatch` state and check a builtin call's
  required properties (`VolatileOnly` checks volatility alone).
- **`Info`**: the metadata every node embeds (usage, position, properties,
  unknown-NULL marker, captured REGCLASS reference, collation conflict data,
  routine references); its methods implement the metadata half of `Expr`.
  `InfoOf` copies it for a wrapper without `UnknownNull` or `RegclassRef`.
- **`RoutineReference`/`RoutineRecipeReference`**: opaque identity tokens for
  the selected routine and the stored recipe a grafted node came from; they
  carry no catalog or permission authority. **`RoutineProvider`** is the bit set
  of builtin bind families (constants defined in `functions`) that may select a
  builtin routine declaration.
- **Positioned errors**: `ErrorAt` (a zero position leaves it unpositioned) and
  `WithPosition`.

## `Context`

`Context` is the evaluation runtime visible outside the engine;
`*exec.Context` ([`exec`](../exec/README.md)) is its only implementation and
evaluation always receives a non-nil one. Every method is a per-evaluation
capability of the current statement, never backed by a global: cancellation
(`FunctionContext`), presentation environments (session input/output, JSON,
string and format outputs, JSONB record input), argument/property inspection,
session RNG and entropy (`RandomOwner`, UUID clock, pgcrypto, FIPS mode),
clocks, the regex cache and conversion handlers.

## Node protocol

- **`Composite`**: a node whose expression children are all lexical.
  `Children()` lists them in evaluation order (entries may be nil; the slice may
  alias the node and must not be modified); `Rebuild(Info, children)` returns a
  revalidated copy with the same child count and leaves the receiver unchanged.
  Every function node is a member, and none carries `UnknownNull` or
  `RegclassRef`.
- **`Walk`** visits a node, then its non-nil children depth first, over a
  caller-supplied enumerator so the engine decides lexical boundaries (a
  subquery's plan is a separate frame). **`Map`** maps non-nil children with a
  changed-bit callback and rebuilds, on a fresh slice with `InfoOf(node)`, only
  when one changed.
- Each central engine switch adopts the protocol with one final
  `case expr.Composite:`; explicit cases before it keep precedence (engine
  wrappers around protocol nodes such as installed extension routines, whose
  catalog identity is engine state).
- **`Capturable`** (`capture.go`) explicitly admits a `CaptureKind` and supplies
  immutable written operands. `CaptureOperands` borrows safe slice storage or
  two inline operands; fixed-array nodes use `Indexed: true` with the optional
  `IndexedCapturable` accessor. `Check(source, count)` and `At(source, written)`
  take the original boxed node and reject missing accessors, malformed counts or
  slots and nil operands before any engine map write; the descriptor retains no
  source or callback. Composite membership alone never admits capture, and
  capture never evaluates, renders, rebuilds or mutates. Identity keywords admit
  `CaptureIdentity` with zero operands, ordinary calls `CaptureFunction`. The
  engine owns structural paths, coercions, catalog identity and frames.

### Optional hooks

Each hook (`hooks.go`) answers one switch's question; a node without it gets the
switch's generic behavior.

- **`ResultNullability(ResultSite)`**: a pure claim about the top-level
  expression at `ResultSelect` or `ResultReturning`, independent of `Composite`.
  A missing hook or `known=false` leaves the descriptor untouched. Answers use
  immutable kind/recipe data only: no evaluation, authority, catalog read,
  allocation, mutation or child traversal; consumers do not unwrap wrappers.
  Result descriptor construction (`analyze/query_bind.go`, `returning.go`)
  asks after its own provenance, grouping, aggregate, window, source-column,
  OLD/NEW and MERGE rules.
- **`Intrinsic`** (`IntrinsicVolatility`): the node's own planned volatility,
  excluding children; planned volatility merges it with the children and
  generated-column admission requires `Immutable`. Retained-analysis inquiry
  nodes return their stored `Property.Volatility`.
- **`SnapshotDemander`**: the owner's own analysis-catalog,
  execution-catalog-text and visibility demand, using the caller's literal-NULL
  predicate and never capturing authority; children keep independent demands.
  Stored `Replay` manifests are a separate contract.
- **`CallCoster`**: inline-policy procost without evaluation or catalog lookup.
- **`GroupPropertyRewriter`**: merges already rewritten children at grouping
  only; substitution, preflight and retag never invoke it.
- **`Folding`** (`PlanningFold`): `FoldConstant` (default), `FoldNever`, and
  the engine-owned folds `FoldOverlapsRoute`/`FoldQuoteCapture` dispatched by
  kind. **`StrictFolding`** (`StrictNull`): when a literal NULL child replaces
  the call with a typed NULL.
- **`StoredProvider`** (`ValidateStoredProvider`): stored generation plans
  reject protocol nodes without it.
- **`Retaggable`** (`Retag`): catalog retag for nodes with derived inputs over
  the engine's `Retag` environment; returns whether anything changed.
  **`Annotated`**: an opaque engine value (such as a quote-capture origin) that
  rebuilds keep.
- **`Renderable`** (`Render`, `render.go`): the node's presentation as one
  `Rendering` value read by node-tree output (`Tree`, `Inputs`), the deparser
  (the SQL `Form`: `Leaf`, `Call`, `Infix` or a `Template` of `Piece`s, with
  per-argument `RenderArg` flags) and stored scalar SQL (`Replay`). The
  renderers own quoting, literals, casts and parentheses and hold no per-node
  knowledge. Replay renders every argument in order before assembling the form
  (literal capture and dependency order follow it), qualifies names with
  `pg_catalog`, keeps argument types through each `RenderArg.Transport`, and
  runs the lazy `Replay.Validate`, which display never runs. SQL/JSON
  constructors mark their boundary with `JSONConstructor`/`JSONReturning`;
  query nodes use `OwnedReturning`. The engine supplies the RETURNING slot and
  keeps conversion plans; no renderer resolves a RETURNING type from raw
  spelling, and the description stores no engine callback.
- **`GroupIdentity`** (`SameGroup`): GROUP BY, DISTINCT ON and CHECK definition
  equality; the node compares its own identity and calls the engine's `same`
  for children. Nodes without it never match. **`GroupLeaf`**: childless calls
  the grouping rewrite leaves unchanged.
- **`Preflighting`** (`PreflightFold`): how statement preparation folds a call
  after preflighting its children. **`ConstantTemplate`**
  (`SpecializeTemplate`): parse a literal formatting template once.
- **`Substitutable`** (`Substitute`): replace metadata and children but keep
  the provider and derived inputs, where `Rebuild` would rebind and could
  reject a preflighted child. `Substitute` falls back to `Rebuild`;
  `MapSubstituted` maps through it and follows **`EvaluationOrdered`**, so
  preflight errors keep formal-slot precedence.
- **`FunctionSyntax`** (`LooksLikeFunction`): index keys that
  `pg_get_indexdef` prints bare.
- **`ConditionalClosure`** (`ClosedDefault`) and **`ClosureTransparent`**:
  closed-DEFAULT admission. A node may refuse, defer to its children through the
  caller's predicate, or be transparent; inspection never evaluates or mutates.

### Adopted switches

The lexical walk (`plan.ExpressionChildren`), join legality
(`plan.JoinExpressionSides`), planned volatility, generated admission
(`plan.GenerationImmutable`, `plan.GenerationHasTableOID`,
`table.ValidateGenerationPlan`), conditional planning
(`exec.simplifyConditionalNode`), retag (`stored.MapExpressionReader`), remap
(`stored.RemapScalar`), node-tree output (`plan.AppendBoundTree`), deparse
(`stored/deparse_rendering.go`), stored scalar SQL
(`stored/scalar_rendering.go`), grouping identity, the grouping rewrite and
aggregate occurrence (`analyze` `sameGroupExpressionIn`, `RewriteGroup`,
`AggregateOccurrence`), preflight (`exec/preflight_composite.go`), index key
display (`index.PGIndexFunctionExpression`) and the closed-DEFAULT checks
(`analyze/default_closure.go`). Remaining explicit cases: `Timezone` (value
before zone) in the walk, grouping and aggregate occurrence, and the UUID
closed-DEFAULT check's calendar calls.

## Closed function sources

`Source.SourceAdmission` answers FROM admission and set cardinality
independently; neither spelling nor `Composite`/`Renderable` membership admits
a call. `ScalarSourceOut` only supplies an OUT label. `SourceOccurrence`
compares occurrence identity (grouping identity is a different contract).
`SourceOpening` opens fresh execution-private state. Lexical rewrites use
`Children` and `Substitute`, keeping provider, provenance and template.

`SourceCursor` is a by-value handle with shared primitive fields and
noncapturing named provider callbacks:

- Scalars, vectors, arithmetic and borrowed arrays use the shared fields;
  complex providers keep their native cursor pointer in `State`. Never retain an
  evaluation context, node, child slice or closure, and never copy a live
  native-state handle for independent iteration.
- `Release` receives only `State`, so primitive sources stay allocation-free;
  complex cursors cost one pointer-state allocation, never a per-row adapter.
- `Next` checks cancellation first, returns typed NULL with `present=false` at
  EOF, and releases payload on EOF, error or cancellation. `Close` is idempotent.
  Emitted borrowed immutable values stay valid after close. The engine closes
  partial sibling opens and owns scheduling.

## What stays in the engine

Binding; the evaluation context ([`exec`](../exec/README.md)); the concrete
nodes and plan IR ([`plan`](../plan/README.md)), which recover `plan.Runtime`
from the `Context` and never `*exec.Context`. Catalog/session function nodes in
`functions` recover its `CatalogInquiryRuntime`/`SessionInquiryRuntime` from the
`Context`, keeping engine authority out of the leaf. Nodes embed `expr.Info`,
use `expr` names directly (no aliases) and evaluate without type parameters.

## Testing

Unit tests here pin the helpers: property merging, intrinsic rules, `InfoOf`,
positioned errors, `Walk`/`Map`/`MapSubstituted`, capture operands and source
cursors. Hook implementations are tested in `functions`
([README](../functions/README.md#testing)) and the engine; public behavior in
`reference/` (result nullability in
[reference/dml](../../../reference/dml/README.md#testing)).

```sh
make check RUN='^Test(MergeProperties|IntrinsicPropertyRules|InfoOf|ErrorAt|WithPosition|WalkVisitsPreOrderAndSkipsNil|Map(Unchanged|Changed|Propagates|Substituted)|SubstitutePrefersTheHook|CaptureOperands|SourceCursor(ScalarPresence|OwnedLifetime))'
make check RUN='^Test(ResultNullability(Engine|Functions|Identity|CatalogInquiry)|CatalogInquiry(GroupProperties|RetainedVolatility|IntrinsicVolatility))$$'
make check RUN='^TestConditionalDefaultClosure'
make check RUN='^Test(SQLJSONProtocol|JSONPredicateProtocol|SQLJSONRendererBoundaries|SQLJSONReturningOwnershipRebind)$$'
make check RUN='^TestReferenceResultNullability'   # add REFERENCE=1 for the oracle
```
