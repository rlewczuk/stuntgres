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

- [ ] 2a — Evaluation through an interface; `routineRecipe` cut
- [ ] 2b — `expr` created, contract moved, aliases in place
- [ ] 2c — Node protocol landed with the walk switch as first adopter
- [ ] 2d — Aliases removed

Outstanding aliases: none yet.

Notes for later steps: none yet.
