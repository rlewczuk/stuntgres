# Engine split, step 2: `internal/engine/expr`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-expr.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §5 step 2; cut 7 in §2.3; §2.5
  for how `functions` uses the contract today.
- **Needs:** step 1 (`ids`), at least through 1c.
- **Effort:** xhigh for 2a (it fixes the shape of `expr.Context`); high for
  2b–2d.
- **Session size:** one sub-step per session. 2a and 2b may fit together.

## Goal

A package `internal/engine/expr` that defines the expression contract shared
by the engine and `functions`. It imports only `ids`, `sqltype`, `parser`,
`sqlstate` and the standard library:

- `Expr` (today `BoundExpr`);
- `Context`, the evaluation runtime visible outside the engine;
- `Properties` (today `ExprProperties`), `Volatility` and its constants;
- `Info` (today the engine's `expressionInfo` and `functions.Info[P]`);
- `RoutineReference` and `RoutineRecipeReference` (today in `functions`);
- property merging and positioned expression errors;
- the node protocol (`Composite` and render hooks) and walk/map helpers.

The concrete engine nodes, the plan IR and the `EvalContext` struct stay in
the engine (§5 step 2, "Stays in the engine").

## Sub-steps

### 2a — Port in place: evaluation through an interface (cut 7)

- Declare an engine-local interface that will become `expr.Context`, and
  change `BoundExpr.Eval(*EvalContext)` to take it. `*EvalContext` implements
  it. Each engine `Eval` method (about 60) recovers the concrete context
  through one checked helper (`c := evalContext(ctx)`).
- The interface carries the existing `Function*` capability methods of
  `EvalContext` (about 24), grouped and documented: presentation environments,
  RNG and UUID entropy, clocks and statement time, regex cache, conversion
  handlers, literal-NULL checks. It must satisfy every context constraint in
  `functions` (`LiteralContext[E]`, `BinaryContext[E]`,
  `CurrentDateTimeContext`, ...).
- Change the generic instantiations from `[BoundExpr, *EvalContext,
  ExprProperties]` to use the interface (about 1,257 references, mechanical).
  Removing the generics is step 3 (F1), not this step.
- Make `expressionInfo.routineRecipe` hold `RoutineRecipeReference` instead
  of `*relationDescriptor`. The engine asserts back to the descriptor only
  where it needs one.
- Settle §7 question 2 (the shape of `expr.Context`) and record the answer in
  ENGINE-SPLIT.md.
- Keep `EvalContext`'s value-copy sites (about 50) unchanged.

### 2b — Create `expr` and move the contract

- Move `Expr`, `Context`, `Properties`, `Volatility`, `Info`, the routine
  reference interfaces and the generic helpers. Leave aliases in the engine
  (`type BoundExpr = expr.Expr`, ...) and in `functions` where they make the
  diff smaller.
- `expr.Info` is the union of `expressionInfo` and `functions.Info[P]`
  (`unknownNull`, `regclassRef` and the routine references included).
  Unexported fields get exported names or constructors.
- `functions` imports `expr` from now on. Replace `functions.Info[P]` only
  where that is trivial; F1 finishes it.

### 2c — Node protocol with its first adopter

- Define `expr.Composite` (`Children() []Expr`, `Rebuild(Info, []Expr) (Expr,
  error)`), the optional render hooks for SQL deparse and node-tree output, and
  `Walk`/`Map` helpers.
- Land it together with one adopter, so no unused API is merged: the walk
  switch in `query_expression_walk.go` gains a `case expr.Composite:` branch,
  and the nodes that already implement `Reconstruct` implement the protocol.
  Remove the cases the new branch makes redundant. Step 3 (F2) converts the
  other switches.

### 2d — Remove the aliases (separate session)

Replace `BoundExpr`, `ExprProperties` and the other aliases with `expr.`
names using a tool aware of `go/types`, then delete the aliases. Do not run
this in parallel with another broad rewrite (§8).

## Invariants and risks

- Evaluation results, errors, SQLSTATEs and positions stay identical.
- The checked recovery adds one type assertion per engine node evaluation.
  Compare `make bench` before and after, and watch for regressions on
  evaluation-heavy tests.
- No `Function*` capability may be routed through a global or package-level
  variable to make it fit the interface.

## Targeted tests

- Unit tests for expression evaluation, collation conflicts and the functions
  package. Grep for them.
- 2a and 2c: an evaluation-heavy reference selection run with `REFERENCE=1`
  and in quick mode. Cover scalar functions, CASE/COALESCE, subqueries,
  aggregates and windows, generated columns, defaults and checks, and views.
  Pick the test names by grepping `reference/`; never edit it.

## Documentation

- Create `internal/engine/expr/README.md`: the contract, the `Context`
  capability groups, the node protocol, what stays in the engine, and a
  `## Testing` section.
- Update `internal/engine/functions/README.md` for the moved routine
  references and the new import.
- Design notes that name these types: `PG18-ARCH-EXPRESSION-ANALYSIS.md`,
  `PG18-ARCH-QUERY-EXECUTION.md` and `PG18-EXPR-COMPOSITE.md`. Grep for more.
- `AGENTS.md`: add `expr` to the shared subpackage bullet. Nothing else should
  be needed; remove any expression-contract text made obsolete.

## Done when

`expr` imports only `ids`, `sqltype`, `parser`, `sqlstate` and the standard
library. Engine nodes evaluate through `expr.Context`. The node protocol exists
with one converted switch. The checks in §8.3 pass with no startup or
evaluation regression. 2d completes the step.

## Status

- [x] 2a — Evaluation through an interface; `routineRecipe` cut (`52d15cad4`
  seam, `a7bc48424` port, `d0fb3f388` recipe cut)
- [x] 2b — `expr` created, contract moved, aliases in place (`dced86ba6`
  field export, `9e1ba1dae` move, docs `8f542f797`)
- [x] 2c — Node protocol landed with the walk switch as first adopter
  (`328df6341` protocol, `aafd6fe97` function nodes, `f038faf09` walk
  adopter, docs DOCS_COMMIT)
- [ ] 2d — Aliases removed

Outstanding aliases (2d removes them):

- Engine, `internal/engine/expr_aliases.go`: types `BoundExpr`,
  `ExprProperties`, `ExprContext`, `Volatility` and `expressionInfo`;
  constants `Immutable`, `Stable` and `Volatile`; forwarding functions
  `boundExpressionInfo` (→ `expr.InfoOf`, 132 uses),
  `mergeExpressionProperties` (→ `expr.MergeProperties`, 95) and
  `expressionError` (→ `expr.ErrorAt`, 577).
- `functions`, `internal/engine/functions/expr_aliases.go`: types
  `RoutineReference`, `RoutineRecipeReference`, `RandomOwner` and
  `IntrinsicPropertyRule`; constants `ExactStable`, `ExactVolatile`,
  `VolatileOnly` and `ExactImmutable`.

Corrections to this prompt, measured at the 2c start (on `2ae77489b`):

- The render hooks moved to F2, by decision at the 2c start: the deparse
  renderer's state (implicit-cast display, parent precedence) is engine-private,
  so a hook signature without a caller would be a guess.
- "The nodes that already implement `Reconstruct`" was narrowed to the 35
  whose `Reconstruct` depends only on metadata and children (18 slice nodes,
  14 fixed-arity nodes, `LikeEscape`, `SimilarEscape`, `UUIDExtract`). The
  others take engine-derived inputs (collation, retained descriptors, clock or
  timestamp usages), and 10 `Reconstruct` methods are aggregate providers, not
  nodes. Their walk cases stay; F2 lists them.
- `Walk` takes the children enumerator as an argument, so the engine's switch
  keeps deciding lexical boundaries while engine nodes are not protocol nodes.
  `Map` uses the engine's changed-bit callback shape and rebuilds with
  `InfoOf(node)`.
- The installed soundex, metaphone, levenshtein and pgcrypto wrappers embed a
  protocol node, so they override `Rebuild` to keep their routine identity; the
  walk uses their promoted `Children`. The walk lost 35 cases (31 function
  nodes and the 4 wrappers) and gained one final `case expr.Composite:`.

Tests in 2c: new `expr/composite_test.go` (`Walk`, `Map`),
`functions/composite_test.go` (rebuild identity and arity for slice,
arity-limited, counted and named-argument nodes) and the engine's
`expression_protocol_test.go` (wrapper `Rebuild`, walk through the Composite
branch). No test was moved, adapted or deleted.

Bench in 2c (`make bench`, same session, master `2ae77489b` vs branch):
1.84–1.93 ms/op and 7,753 allocs/op before, 1.89–2.01 ms/op and 7,769 after.
The 16 extra allocations come from `Children()` on value-receiver nodes with
fixed arrays or named fields (the receiver copy or a small slice escapes);
the old switch sliced a local without allocating.

Notes for F2: if the protocol reaches a hot path, give `Composite` a
non-allocating child visitor (for example `EachChild(func(Expr))`) beside
`Children`, or move fixed-arity children into slices.

Corrections to this prompt, measured at the 2b start:

- `expr.Info` needed exported fields, so 2b starts with a port-in-place commit:
  `gopls rename` of the 12 `expressionInfo` fields to the `functions.Info[P]`
  names (`Usage`, `Pos`, `Property`, `Collation*`, `RoutineReference`,
  `RoutineRecipeReference`) plus `UnknownNull` and `RegclassRef`. It touched
  329 files. Matching names keep F1's `functions.Info[P]` → `expr.Info` swap
  mechanical.
- Replacing `functions.Info[P]` was not trivial anywhere: its `P` reaches the
  `FunctionPropertiesMatch(P, ...)` context constraints, and the `functions`
  unit tests instantiate `P` as `bool` and `IntrinsicPropertyRule`. It stays
  generic; the engine's `functionInfo` conversion stays until F1.
- Besides the listed helpers, `functionIntrinsicProperties` and
  `functionPropertiesMatch` moved as `expr.IntrinsicProperties` and
  `expr.PropertiesMatch`, and `expressionErrorFrom` as `expr.WithPosition`.
  Their callers (14, 21 and 2) call `expr` directly. `foldedExpressionInfo`
  stays in the engine: dropping the routine reference is folding policy.
- The nil-context question is settled: evaluation requires a non-nil
  `Context` (documented on `expr.Context` and `Expr.Eval`). Nil-safety is not
  added; a nil `*EvalContext` still reports "missing expression context".

Tests in 2b: new `expr/expr_test.go` pins the moved helpers. Engine tests were
adapted only by the same mechanical renames (field names, `expr.PropertiesMatch`,
`expr.IntrinsicProperties`). None were moved or deleted.

Corrections to this prompt, measured at the 2a start:

- The interface has 25 methods: the 24 `Function*` capabilities plus
  `WholeRowNullPresence`. `CheckDomain` stays off it; it is the engine's
  `sqltype` domain-check hook.
- Instantiation references: 1,323 production and 313 test, in 169 files. The
  two-parameter `TransactionTime`, `VersionInformation` and `CurrentDateTime`
  instantiations compile with any context, so a leftover would make a type
  switch case silently miss. A throwaway `go/types` scan of
  `types.Info.Instances` confirmed that no `*EvalContext` type argument
  remains.
- 60 production and 14 test `Eval` methods. Six production nodes only
  forward the context (collation, null test, truth test, selected operator)
  or never use it (aggregate and foreign-key placeholders), so they skip the
  recovery.

Tests adapted in 2a: the 14 test `Eval` fakes; `deparse_review_identity_test.go`,
`logical_test.go` and `transaction_time_test.go` pass `(*EvalContext)(nil)`
where they passed `nil`; `query_recursive_test.go` and `query_set_test.go`
wrap `original.Eval` for the `*EvalContext` probe helper. None were moved or
deleted.

Notes for later steps:

- 2d: the forwarding functions are not type aliases; rewrite their call
  sites to the `expr` names (adding the import), then delete them.
- 2d: engine nodes embed `expr.Info` under the alias name `expressionInfo`,
  so composite literals spell `expressionInfo: ...` and code selects
  `.expressionInfo`. Removing that alias renames the embedded field to `Info`
  at every such site.
- F1: `functions/pgcrypto.go` checks entropy, UUID and FIPS capabilities with
  `any(c).(interface{...})`. Once nodes take `expr.Context`, these become
  direct calls.
- Outside the split: `SELECT 1 + abs(1)` and `SELECT abs(1) + 1` fail with
  "syntax error at end of input"; `SELECT 1 + length('a')` and
  `SELECT 1 + (abs(1))` work. This is a compatibility bug and needs reference
  coverage in a normal session.
- Outside the split: with the pinned 18.6 image, these `REFERENCE=1` tests fail
  on the PostgreSQL side only (found in 2c: `TestReferenceJSONStripNullsFeatureSmoke`,
  which expects 18.0's 42P17 for `json_strip_nulls` in an index expression): `TestReferenceStringCaseReviewSigma` (final sigma
  case mapping), `TestReferenceUUIDGeneration{StoredConsumersAndControls,
  FeatureSmoke,GuardDiagnostics}` (they expect 18.0's unchecked UUIDv7 shifts)
  and `TestReferenceCheckReview{2SchemaPrivateDropOutcomes,
  DependencyPrivateDeletionNonwaiting}` (bad connection after 5 s).
