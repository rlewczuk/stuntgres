# internal/engine/expr

The expression contract shared by the engine and `internal/engine/functions`.
It imports only `ids`, `parser`, `sqlstate`, `sqltype` and the standard
library, and sits at layer L2 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It owns types and small pure helpers, including invocation-private cursor
handles; it has no shared state and no mutex.

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
  data and the optional routine references. Its methods implement the
  metadata half of `Expr`. `InfoOf` copies a node's metadata for a wrapper; it
  does not copy `UnknownNull` or `RegclassRef`.
- **`RoutineReference`** and **`RoutineRecipeReference`** are opaque identity
  tokens for the selected routine and for the stored recipe a grafted node
  came from. They carry no catalog or permission authority.
- **`RoutineProvider`** is the bit set of builtin bind families that may
  select a builtin routine declaration. `functions` defines the family
  constants (through an alias); catalog routine declarations hold the set.
- **Positioned errors.** `ErrorAt` builds a SQLSTATE ERROR at a source
  position (a zero position leaves it unpositioned). `WithPosition` copies a
  SQLSTATE error with a new position.

## `Context` capability groups

`Context` is the evaluation runtime visible outside the engine;
`*exec.Context` ([`exec`](../exec/README.md)) is its only implementation.
Every method is a per-evaluation capability of the current statement; none may
be backed by a global or package-level variable. Evaluation always receives a
non-nil `Context`.

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
- **`Capturable`** (`capture.go`) explicitly admits a `CaptureKind` and
  supplies immutable written operands, not execution or rendering order.
  - `CaptureOperands` borrows safe slice storage or uses two inline operands.
    Fixed-array value nodes use `Indexed: true` and the optional
    value-receiver `IndexedCapturable` accessor to read written-slot
    provenance directly, without retaining receiver slices, copying or
    reordering arguments or reboxing the node.
  - `Check(source, count)` and `At(source, written)` take the original
    already-boxed Capturable, including promoted wrappers; the descriptor
    retains no source or callback. The explicit count includes zero; checks
    reject missing accessors, conflicting indexed storage, malformed
    counts/slots and nil operands before engine map writes.
  - Composite membership alone never admits capture syntax. Capture must not
    Eval, Render, Rebuild, rebind or mutate. Selected/call implicit usages,
    substring kind, formatted JSON sources and dot-builtin spelling are
    annotation requests only: the engine owns structural paths, maps,
    coercions, catalog identity and frames.
  - Identity keywords admit `CaptureIdentity` with zero operands;
    ordinary-call identities admit `CaptureFunction`. This keeps spelling and
    routine provenance without a concrete engine identity switch.

### Optional hooks

Each hook (`hooks.go`) answers one switch's question; a node without the hook
gets the switch's generic behavior.

- **`ResultNullability`** (`ResultNullability(ResultSite)`): a pure claim
  about the actual top-level expression at `ResultSelect` or
  `ResultReturning`, independent of `Composite`. A missing hook or
  `known=false` leaves both descriptor fields untouched; neither implies
  nonnullability. Answers use immutable kind/recipe data only, without
  evaluation, authority, catalog reads, allocation, mutation or child
  traversal. The hook is not on `Info`, and consumers do not unwrap coercion,
  COLLATE or other wrappers. SELECT asks after provenance, grouping,
  aggregate and window rules; RETURNING asks only after its explicit
  source-column, OLD/NEW and MERGE cases decline. Deparse and index-property
  inquiries claim known nullable only for SELECT. Other inquiry/ACL owners
  keep their claims at both sites; among identity kinds only `current_schema`
  claims. Literal-NULL index inquiry folding removes the inquiry node, so that
  literal still has unknown metadata. Result descriptor construction
  (`query_bind.go`, `returning.go`) asks it without requiring Composite
  membership.
- **`Intrinsic`** (`IntrinsicVolatility`): the node's own planned volatility,
  excluding children. Planned volatility merges it with the children;
  generated-column admission requires it to be `Immutable` and admits every
  child. Retained-analysis exception: Identity, CurrentSchemas,
  NamespaceInquiry, VisibilityInquiry, IndexProperty, CommentInquiry and
  Deparse return their stored `Property.Volatility`, including eliminated-arm
  dependence. Username/linkage/object-address supply STABLE and keep physical
  child merging; the generic walker policy is unchanged.
- **`SnapshotDemander`** reports only an owner's immutable analysis-catalog,
  execution-catalog-text and visibility demand, using the caller's
  literal-NULL predicate and never capturing authority. Children keep
  independent demands. This is distinct from stored Replay manifests:
  deparse's stored catalog demand stays unconditional even when bare NULL
  suppresses its own analysis demand. Retained visibility inquiries request
  `Replay.AlwaysVisibilitySnapshot`, including coercion-wrapped NULL;
  bare-NULL executable folding stays separate.
- **`CallCoster`** keeps inline-policy procost distinctions
  (username/visibility 10, comments 100), without evaluation or a catalog
  lookup.
- **`GroupPropertyRewriter`** merges already rewritten children at grouping
  only. IndexProperty/Deparse merge STABLE without forced stage dependence;
  absence keeps generic staging. Substitution, preflight and retag do not
  invoke it.
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
  `Rendering` value, which every renderer that lists nodes reads.
  - Node-tree output reads `Tree` and `Inputs`; the deparser reads the SQL
    `Form`: `Leaf` text, a `Call` (name `Quote` rule, written arguments with
    labels and VARIADIC), an `Infix` operator (namespace and a `Grouping`
    rule), or a keyword-syntax `Template` of `Piece`s. Each `RenderArg` says
    whether the argument shows implicit coercions, sits in function-argument
    position, replaces the inherited unknown-text flag, or displays a cast to
    its resolved formal type (`CastForm`); `SimpleCast` marks calls whose
    explicit cast keeps them bare. These fields encode per-call differences in
    PostgreSQL's ruleutils, so the deparser holds no per-node knowledge. The
    renderer owns identifier quoting, literals, casts and parentheses.
  - Stored scalar SQL (replay) reads the same value plus `Replay`, the parts
    where replay differs from display: it renders every argument in order
    before assembling the form (literal capture and dependency order follow
    argument order), qualifies call names with `pg_catalog`, always quotes
    labels and parenthesizes operators, and ignores the display-only argument
    fields. `Replay` adds a lazy `Validate` (stored provider checks and
    refusals such as set-returning calls; display never runs it, so it cannot
    change deparse), replay `Name`/`Text`, a replay template (`Pieces` over
    `Args`, for POSIX `SUBSTRING ... FROM`, EXTRACT's field literal,
    `SIMILAR TO ... ESCAPE` over an `Absorbed` normalizer and the `num_nulls`
    field notation), `QuoteNamespace` for written operator schemas,
    `CatalogSnapshot`, NULL-sensitive `VisibilitySnapshot` and unconditional
    `AlwaysVisibilitySnapshot` demands, `RelationOnly` (random calls) and
    carried `Types` that must belong to the definition's catalog (the
    pg_sleep clock). Each `RenderArg` names a `Transport` that keeps its type
    on replay: always cast to `TransportType`, or cast NAME, typed or
    ambiguously spelled literals, or type NULL intervals. Binary and string
    operators state their `pg_catalog` schema, which display shows bare and
    replay qualifies.
  - SQL/JSON constructors use the same template protocol: `JSONConstructor`
    marks their representation boundary (ordinary call names do not), and
    `JSONReturning` permits a named `Piece.Returning` type slot. The engine
    supplies that slot only from the enclosing constructor-owned conversion;
    without it the whole piece, including its prefix and FORMAT/ENCODING
    suffix, is absent. `RenderArg.JSONFormattedSource` requests the original
    child of exactly one recognized formatted-input conversion, never an
    arbitrary cast. The engine keeps conversion plans, catalog/typmod/domain
    declarations, literal transport and validation; the description stores
    no engine callback. Standard SQL/JSON query nodes instead supply
    `OwnedReturning`: their own result usage owns the named type slot because
    conversion is inside ON ERROR. A genuine enclosing cast stays outside that
    boundary. No renderer resolves a second RETURNING type from raw spelling.
- **`GroupIdentity`** (`SameGroup`): whether another expression is the same
  grouping expression (GROUP BY matching, DISTINCT ON, CHECK definition
  equality). The node compares its own identity and calls the engine's `same`
  for children, so frames and subqueries stay engine knowledge; `definition`
  selects CHECK definition equality. Nodes without it never match.
- **`GroupLeaf`**: the childless calls (SQL value functions, transaction
  clocks, version information) that the grouping rewrite leaves unchanged; it
  rebuilds every other call over its rewritten children as stage dependent.
- **`Preflighting`** (`PreflightFold`): how statement preparation folds a call
  after preflighting its children: conditional simplification (default),
  keep (the leaves), children only, evaluate an immutable independent call,
  NULL-or-evaluate (with a regex execution reset for the POSIX and SIMILAR
  ESCAPE calls), NULL before preflight, or a bare NULL (`pg_sleep`).
  **`ConstantTemplate`** (`SpecializeTemplate`) lets the formatting calls
  parse a literal template once, before simplification.
- **`Substitutable`** (`Substitute`): replace metadata and children but keep
  the provider and derived inputs. Preflight and the grouping rewrite swap
  children in place, and `Rebuild` would rebind the provider and could reject
  a preflighted child (`pg_size_bytes('1 MB'::name)`). `Substitute` uses the
  hook and falls back to `Rebuild`; `MapSubstituted` maps through it.
- **`EvaluationOrdered`** (`EvaluationOrder`): child indices in evaluation
  order for a node whose `Children` keep written order (`ArrayTransform` with
  named arguments). `MapSubstituted` follows it, so preflight errors keep
  formal-slot precedence.
- **`FunctionSyntax`** (`LooksLikeFunction`): PostgreSQL's
  `looks_like_function` for index keys; `pg_get_indexdef` prints such keys
  bare and parenthesizes the rest.
- **`ConditionalClosure`** (`ClosedDefault`): a node may refuse
  closed-DEFAULT admission; otherwise every relevant child must satisfy the
  caller's predicate. Inspection neither evaluates nor mutates nodes or
  children. `closedTransactionTimeDefault` adopts it for scalar ACL calls
  versus `aclexplode`, after dependency guards and explicit leaf/engine-wrapper
  cases, before unconditional transparency and the stable fallback. UUID
  closure does not dispatch it and keeps its ACL root-volatility fallback.
- **`ClosureTransparent`**: calls whose closed-DEFAULT closure is their
  children's (`closedUUIDDefault`, `closedTransactionTimeDefault`),
  independent of conditional closure. Other calls use each caller's
  specialized cases or the at-most-stable fallback.

### Adopted switches

The lexical walk (`boundExpressionChildren`), join legality
(`joinExpressionSides`), planned volatility, generated admission
(`plan.GenerationImmutable`, `plan.GenerationHasTableOID`,
`table.ValidateGenerationPlan`), conditional planning (`exec`'s
`simplifyConditionalNode`, `ConditionalConstantInputs`), retag
(`stored.MapExpressionReader`), remap (`stored.RemapScalar`), node-tree output
(`plan.AppendBoundTree`), SQL deparse (`stored.Deparser.SQL`, interpreted in
`stored/deparse_rendering.go`), stored scalar SQL (`stored.ScalarRenderer.SQL`
and `ScalarFunctionSQL`, interpreted in `stored/scalar_rendering.go`),
grouping identity (`sameGroupExpressionIn`), the grouping rewrite
(`rewriteGroup`), aggregate occurrence (`aggregateOccurrence`), preflight
(`exec`'s `preflightCoercionNode`, interpreted in
`exec/preflight_composite.go`), index key display
(`index.PGIndexFunctionExpression`) and the closed-DEFAULT checks.

- The walk, the grouping rewrite and aggregate occurrence keep one explicit
  `Timezone` case that visits the value before the zone.
- The UUID closed-DEFAULT check keeps explicit cases for `date_trunc`,
  `date_bin` and the zone calendar calls, which only it looks through.
- Function sources use the explicit capabilities in `source.go`; the engine
  keeps query scheduling, lexical-frame boundaries, layout and installed
  identity authority.
- SQL/JSON display and stored SQL use `Renderable`; its query form, coercions
  and raw stored-view occurrence capture stay engine-owned.
- Literal capture uses `Capturable` only after engine-owned wrappers,
  coercions, constructors and query-frame boundaries.

## Closed function sources

`Source.SourceAdmission` answers two independent questions: FROM admission and
set cardinality. Neither a spelling nor `Composite`/`Renderable` membership
admits a call. `ScalarSourceOut` only supplies an OUT label; it says nothing
about cardinality or physical composite expansion. `SourceOccurrence` compares
the owner's occurrence identity after engine set/position guards; grouping
identity is a different contract. `SourceOpening` opens fresh
execution-private state. All lexical rewrites use `Children` and `Substitute`,
keeping the selected provider, provenance and original immutable template.

`SourceCursor` is a safe by-value handle with documented shared primitive
fields and noncapturing named provider callbacks, not an interface allocation
per open or a provider-tagged union.

- Scalars, vectors, arithmetic and borrowed arrays use the shared fields.
  Complex providers store their own native cursor pointer in `State`; catalog
  keys borrow immutable metadata instead. Only owner callbacks interpret that
  payload. Do not retain an evaluation context, node, child slice or closure.
- Never copy a live native-state handle to get independent iteration (numeric
  cursors contain mutable big integers).
- `Release` receives only native `State`, not the whole handle: passing the
  handle to an indirect release callback would allocate even for primitive
  sources. Complex native cursors need one pointer-state allocation at this
  type-erasure boundary, never a per-row adapter or second wrapper.
  Integer/scalar/array opening is allocation-free.
- `Next` checks cancellation before unopened/exhausted state, returns typed
  NULL with `present=false` at EOF, and releases payload and native references
  on EOF, error or cancellation. `Close` is idempotent and prevents further
  emission. A SQL NULL can be present, and a zero-width composite can
  contribute a row. Emitted borrowed immutable values stay valid after close.
  The engine closes partial sibling opens and owns invocation scheduling and
  early finish.

## What stays in the engine

Binding. The evaluation context lives in [`exec`](../exec/README.md). The
concrete nodes (column, parameter, coercion, CASE, row, subquery and the rest)
and the plan IR live in [`plan`](../plan/README.md); they recover the
`plan.Runtime` capability from the `Context` they receive: statement-only nodes
run its prologue and then use this `Context`'s environments; frame and executor
nodes delegate to its `Eval*` methods, whose bodies live in `exec`. No node
recovers `*exec.Context`; the installed extension nodes use runtime methods
too.

Catalog/session function nodes live in `functions` and recover its
`CatalogInquiryRuntime`/`SessionInquiryRuntime` from this `Context`. Those
finite capabilities keep engine authority and state out of the leaf without
extending `Context` per family. Source admission, layout and scheduling stay
engine-owned; username/linkage/address source hooks and cursors stay beside
their nodes.

Engine and `functions` nodes embed `expr.Info`, use the `expr` names directly
(no aliases) and evaluate against `Context` without type parameters.

## Testing

Unit tests here pin the helpers: property merging, intrinsic rules, `InfoOf`,
the positioned errors, the `Walk`/`Map`/`MapSubstituted` protocol helpers,
capture operands and source cursors. The hook implementations are pinned by
the `functions` tests (see its [README](../functions/README.md#testing));
expression evaluation is covered by the engine and `functions` tests and the
`reference/` suite.

```sh
make check RUN='^Test(MergeProperties|IntrinsicPropertyRules|InfoOf|ErrorAt|WithPosition|WalkVisitsPreOrderAndSkipsNil|Map(Unchanged|Changed|Propagates|Substituted)|SubstitutePrefersTheHook|CaptureOperands|SourceCursor(ScalarPresence|OwnedLifetime))'
```

Engine-side hook consumers:

- Result nullability descriptor kind/site boundaries (including immutable
  value/pointer recipes); the public matrix
  (`TestReferenceResultNullability*`, in
  [reference/dml](../../../reference/dml/README.md#testing)) checks metadata
  before iteration, including empty results, fresh arguments/search_path and
  native descriptions; pq's unknown nullability is an independent
  expectation.
- Real-provider conditional closure: every scalar ACL slot, set refusal,
  dependency/fallback precedence, UUID/time asymmetry, context-free immutable
  inspection and the SQL-routine fallback consumer.
- SQL/JSON bound-renderer and coercion boundaries (with real catalog
  fixtures); the constructor protocol tests live in `functions`.

```sh
make check RUN='^Test(ResultNullability(Engine|Functions|Identity|CatalogInquiry)|CatalogInquiry(GroupProperties|RetainedVolatility|IntrinsicVolatility))$$'
make check RUN='^TestReferenceResultNullability(Select|Returning)$$'
make check RUN='^TestConditionalDefaultClosure'
make check RUN='^Test(SQLJSONProtocol|JSONPredicateProtocol|SQLJSONRendererBoundaries|SQLJSONReturningOwnershipRebind)$$'
```
