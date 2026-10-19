# Engine split, step 10: `internal/engine/plan`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-plan.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §6 step 10; §2.6 (clean plan
  IR); §3 rule 7 and L6; §4.1 patterns 2 and 3; §7 decision 9.
- **Needs:** steps 2, 3 (F1) and 5, all done. 10b is easier after the
  functions F2 and Tier C items (they remove engine switch cases), but does
  not require them. Read [`ENGINE-SPLIT-expr.md`](ENGINE-SPLIT-expr.md) (2a:
  the same `Eval` port against an engine-local interface) and the 6b and 7b
  results for the export-then-move tools.
- **Effort:** xhigh for 10a and 10b (hot evaluation path); high for 10c and
  10d.
- **Session size:** one sub-step per session. 10a, 10c and 10d are broad
  rewrites (§8): never in parallel with another broad rewrite.

## Goal

The logical plan IR and the concrete engine expression node types get their
own package. Node `Eval` methods no longer recover `*EvalContext`: they use
`expr.Context`, or a node runtime capability declared in `plan` and
implemented by the evaluation context. Execution stays in the engine until
step 11.

## Inventory (measured at `75fe0d710`)

**Plan IR** (all clean as types except `cteDefinition`):

- `LogicalNode` (`query.go:19`, `logicalNode()` and `Schema()`),
  `BoundQuery` (`query.go:23`), `querySchema` (`query.go:42`);
- 20 node types: `sourceNode`, `singletonNode`, `valuesNode`, `filterNode`,
  `sortNode`, `projectNode` (`query.go`), `aggregateNode`
  (`aggregate_query.go`), `derivedNode` (`query_derived.go`), `distinctNode`,
  `distinctOnNode`, `joinNode`, `limitNode`, `evaluationNode`
  (`query_producer.go`), `worktableScanNode`, `recursiveUnionNode`
  (`query_recursive.go`), `rowLockNode`, `setNode`, `functionFromNode`,
  `setProjectNode` (`string_set_query.go`), `windowNode`
  (`window_query.go`);
- records: `sortKey`, `queryGrouping`, `aggregateCall`, `windowCall`,
  `windowOperation`, `boundWindowSpec`, `searchRecipe`, `cycleRecipe`,
  `cteDefinition` (`query_cte_bind.go:15`), `functionCorrelation`,
  `querySetTemplate`, `querySetStep`, `querySetSlot`, `queryRowMark`.
- Only dirty link: `cteDefinition.mutation *binding`. `derivedNode`,
  `worktableScanNode` and `recursiveUnionNode` are dirty only through
  `cteDefinition`.

**Expression node types with `Eval(expr.Context)`: 55.** All are clean as
types; only `Eval` (and a few helpers) recover `*EvalContext` through
`evalContext(ctx)` (`expression_context.go:9`) and the shared prologue
`evaluationContext(c)` (`expression.go:149`: nil check, then
`c.Input.DomainChecks = c` while a domain value is active, then
`c.Context.Err()`). Fields each `Eval` reads, from the appendix tool:

| Group | Nodes | `EvalContext` fields read |
|---|---|---|
| S: statement only | `arithmeticExpression`, `arrayExpression`, `bitwiseExpression`, `caseExpression`, `comparisonExpression`, `conditionalExpression`, `distinctExpression`, `likeExpression`, `literalExpression`, `logicalExpression`, `nullTestExpression`, `truthTestExpression`, `collationExpression`, `rowComparisonExpression`, `scalarArrayComparisonExpression`, `subscriptExpression`, `subscriptsExpression`, `fieldExpression`, `rowExpression`, `unnestExpression`, `seriesExpression`, `assignmentValueExpression`, `viewQualificationExpression`, `selectedOperatorExpression`, `aggregateExpression`, `unknownOutput`, `coercionExpression` | `Context`, `Input`, `Output` only; `coercionExpression` also reads the live scope's `input` (`liveScope()`) |
| F: frame | `columnExpression` | `Frame`, `Row`, `Scope`, `mergeSourcePresent`, `physicalTable`, `queryCurrentRows` |
| | `parameterExpression` | `Parameters` |
| | `groupExpression`, `groupingExpression` | `Frame`, `Outer`, `Row`, `Scope` |
| | `tableOIDExpression` | `Frame`, `Scope`, `physicalTable` |
| | `returningColumnExpression`, `returningWholeRowExpression` | `Row`, `OldRow` |
| | `mergeActionExpression` | `mergeAction`, `mergeReturning` |
| | `cachedOrderExpression` | `orderValues` |
| | `windowExpression` | `Row` |
| | `domainValueExpression` | `domainValue`, `domainFrameUsage`, `domainValueActive` |
| X: executor | `subqueryExpression` | `query`, `Parameters`, `Row`, `Input`, `Output`; its helpers `buildMembership`, `compare`, `evalJSONArray`, `hashMembership` are dirty too |
| Tier C (excluded) | `catalogUserNameExpression`, `catalogForeignKeysExpression`, `objectAddressExpression`, `namespaceInquiryExpression`, `currentSchemasExpression`, `visibilityInquiryExpression`, `indexPropertyExpression`, `commentInquiryExpression`, `deparseExpression`, `identityExpression`, `transactionIdentityExpression`, `commitTimestampExpression`, `advisoryExpression`, `blockingInquiryExpression`, `sequenceCallExpression` | statement scope, session, engine stores: they move to `functions` in step 3 Tier C |
| Engine wrapper | `installedFunctionExpression` | prologue only; holds an extension routine identity |

The transaction identity/timestamp entries above are historical: their private
nodes, binders and source/protocol behavior now belong to `functions`, behind
`TransactionInquiryRuntime`. Runtime allocation/history, scope/fences, locks and
publication remain engine-owned; see the functions package README.

Other dirty node helpers: `columnExpression.rowContext`,
`capturedGeneration`, `admitVirtualGeneration`; `rowExpression.absent`;
`rowComparisonExpression.compare`.

**`EvalContext`** (`expression.go:20`, 43 fields, 49 methods; uses in
production code, selectors plus composite-literal keys):

- frame: `Scope` 62, `Frame` 69, `Row` 76, `OldRow` 9, `Outer` 11,
  `orderValues` 20, `query` 61, `queryOccurrences` 19, `queryProvenance` 24,
  `queryRowSource` 2, `queryCurrentRows` 9, `presenceSlots` 10, `presence`
  12, `physicalTable` 10, `mergeReturning` 2, `mergeSourcePresent` 3,
  `mergeAction` 4, `domainValue` 7, `domainFrameUsage` 6,
  `domainValueActive` 7;
- statement runtime: `Context` 188, `Parameters` 19, `Input` 109, `Output`
  64, `pgRegex` 1, `quoteCaptures` 10, `quoteOrigin` 5, `planStaticOperators`
  11, `specializeRuleParameters` 4, `preparedChecks` 5,
  `preparedDomainChecks` 30, `preparedGenerations` 4, `statementScope` 45,
  `catalog` 61;
- routine admission: `routineAuthority` 20, `routineRecipe` 28,
  `routineGraft` 29, `routineChecks` 31, `routineReferences` 23,
  `routineConsumer` 34, `routineProgram` 11, `routineBodyContext` 7,
  `domainPhase` 5.

Value copies: 50 `EvalContext{...}` literals in 44 files and about 27
dereference copies (`x := *c`); `indexedStage` and an `execute_bound.go`
struct embed it by value; `queryState.evaluation` returns it by value.

## Sub-steps

### 10a — Split `EvalContext` in place (broad)

1. Declare three engine-local structs and embed them in `EvalContext`:
   `evalFrame` (the frame fields above), `evalStatement` (the statement
   runtime fields) and `evalAdmission` (the routine admission fields; not
   `routineAdmission`, which already names a type in `routine_program.go`).
   Embedding keeps every selector (`c.Row`, `c.routineConsumer`) working, so
   most call sites do not change.
2. Rewrite the 50 composite literals: promoted fields cannot be keys. Use a
   `go/types` tool (key → group) and check a per-field census before and
   after.
3. Document each group in its doc comment: who writes it, who reads it, and
   that copying `EvalContext` copies all three (unchanged semantics).
4. Re-run the appendix tool. Record in Status which nodes and methods read
   which group: it is the input for 10b and for step 11.

No behavior change; `make bench` before and after (allocation count must not
change).

### 10b — Cuts in place: opaque CTE mutation and the node runtime

1. **CTE mutation.** `cteDefinition.mutation *binding` becomes an
   engine-local interface `cteMutation` (rule 7 of §3) with no methods the
   plan needs, or an opaque payload type, and one checked accessor
   `mutationBinding(cte) (*binding, bool)` in the engine. Count the uses
   first (`query_cte_bind.go`, `cte_mutation_*.go`, `rule_cte*.go`).
2. **Node runtime.** Declare an engine-local interface `nodeRuntime`,
   implemented by `*EvalContext` with a compile-time assertion:
   - `BeginEvaluation() error`: the exact prologue of `evaluationContext`
     (nil check, domain-check wiring, cancellation), in that order;
   - one method per frame or executor node: `EvalColumn`, `EvalParameter`,
     `EvalGroup`, `EvalGrouping`, `EvalTableOID`, `EvalReturningColumn`,
     `EvalReturningWholeRow`, `EvalMergeAction`, `EvalCachedOrder`,
     `EvalWindow`, `EvalDomainValue`, `EvalSubquery`, each taking the node;
   - `StatementInput()` for `coercionExpression`'s live-scope input, if it
     cannot use `FunctionInput()`. Check why it reads the scope's input
     before choosing.

   The bodies of today's frame and executor `Eval` methods move into those
   `*EvalContext` methods unchanged (plus the dirty helpers listed above).
   The node's `Eval` becomes a one-line call. This keeps evaluation logic in
   the engine (later `exec`) while the node type can move (§7 decision 9).
   Settle the decision here, after measuring.
3. **Statement-only nodes.** Replace `c := evalContext(ctx);
   evaluationContext(c)` with a helper that asserts `nodeRuntime` and calls
   `BeginEvaluation()`. Then read `ctx.FunctionContext()`,
   `ctx.FunctionInput()` and `ctx.FunctionOutput()` instead of `c.Context`,
   `c.Input` and `c.Output`. `FunctionInput()` returns `c.Input`, which the
   prologue has just wired, so the order must stay prologue first.
4. Leave the Tier C nodes and `installedFunctionExpression` as they are.
5. Benchmark: one interface assertion per node evaluation replaces one type
   assertion. Run `make bench` and an evaluation-heavy reference selection;
   report both.

### 10c — Export in place, create `plan` and move (broad)

1. Export the fields and methods of the moving types in place with a
   type-checked rename tool (6b `fieldrn`, 7b `methx`), with a per-object use
   census before and after. Expect thousands of edits: plan node fields are
   read throughout the binder, the executor, the deparser and the remap code.
2. Name the types in the target package (`plan.Node` or keep
   `plan.LogicalNode`, `plan.Query`, `plan.Source`, `plan.Join`, ...;
   expression nodes `plan.Column`, `plan.Coercion`, ...). Decide with the
   user and record the table here.
3. Move with the `movex` approach: the IR, the records, the node types of
   groups S, F and X, their non-`Eval` methods that are clean, and
   `nodeRuntime` as `plan.Runtime`. The engine keeps the `Eval*` bodies on
   `*EvalContext`.
4. Leave unexported aliases in the engine. Check imports: `expr`,
   `functions` (aggregate and window providers), `catalog` (columns,
   procedures), `lock` (row mark modes), `ids`, `parser`, `sqltype`,
   `sqlstate` and the standard library.

### 10d — Remove the aliases (broad)

Use the 5f/6c alias tool. First audit locals named `plan` that would shadow
the package (`LookupParent` at every rewritten use).

## Stays in the engine (for step 11)

`EvalContext` and the `Eval*` bodies; `openQueryNode` and the node openers;
`queryState`, `querySource`, `indexedStage`, `finalRows`; `RowCursor` and
`RowResult` (public facade); the central switches that list node types
(`deparse_renderer.go`, `deparse_tree.go`, `view_remap.go`,
`expression_retag.go`, `conditional_plan.go`, `query_expression_walk.go`,
`query_join_legality.go`, `aggregate_bind.go`, `default.go`), which name the
`plan.` types after the move; the Tier C nodes until their Tier C parts.

## Invariants

- Evaluation results, errors, SQLSTATEs, positions and evaluation order stay
  identical, including the domain-check wiring of the prologue.
- No new allocation per row or per node evaluation (`make bench` allocations
  and an evaluation-heavy reference selection).
- Deparse, remap, retag and Dump output stay byte-identical (they read node
  fields).
- `EvalContext` value-copy semantics are unchanged.

## Targeted tests

- Engine units over expressions and queries: grep `*_test.go` for
  `EvalContext{`, `columnExpression`, `subqueryExpression`, `cteDefinition`,
  `LogicalNode`. Adapt them mechanically.
- Reference selections (quick mode and `REFERENCE=1`; grep `reference/`):
  queries, joins, subqueries, CTEs including recursive and data-modifying,
  window functions, aggregates and grouping sets, RETURNING, MERGE, domains,
  generated columns, views (deparse), and native Dump/Restore.
- `make bench` after every sub-step.

## Documentation

- Create `internal/engine/plan/README.md` (10c): the IR, the node runtime
  capability and its methods, what stays in the engine, `## Testing`.
- Update the `expr` README "What stays in the engine" paragraph (concrete
  nodes and the plan IR) and `docs/design/PG18-ARCH-QUERY-EXECUTION.md` and
  `PG18-ARCH-EXPRESSION-ANALYSIS.md` where they name `EvalContext`,
  `LogicalNode`, `BoundQuery` or moved files.
- `AGENTS.md`: add `plan` to the shared subpackage bullet; drop "binding/
  execution" wording from the `internal/engine` bullet only when step 11 and
  12 land.

## Done when

`plan` imports only the packages listed in 10c. No node `Eval` in `plan`
names `*EvalContext`. `EvalContext` is three documented groups. The checks in
§8.3 pass with no startup or evaluation regression.

## Status

- [ ] 10a — `EvalContext` split in place
- [ ] 10b — Opaque CTE mutation and node runtime, in place
- [ ] 10c — `plan` created, IR and node types moved
- [ ] 10d — Aliases removed

Outstanding aliases: none yet.

Notes for later steps:

- Step 11 moves `EvalContext` itself (as `exec.Context`) with the `Eval*`
  bodies. 10a's group record tells it which state is per-frame.
- Step 12: the binder constructs most node types; after 10c it names
  `plan.` types.
