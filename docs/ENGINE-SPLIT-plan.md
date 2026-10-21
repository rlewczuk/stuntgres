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
- **Second round (planned at `c154e14fb`):** 10e (linear item 73) needs
  nothing.

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

Re-measured for 10a at `4d3568a9d`: 46 fields (the three new
`updateOutput*` fields belong to the frame), 76 methods, and 744 non-empty
composite literals: about 50 in 44 production files and about 694 in 255
internal test files. Nine empty `EvalContext{}` literals need no change.

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

   Settled with the user in 10c (Node/Expr suffixes):

   | Engine (alias until 10d) | `plan` |
   |---|---|
   | `LogicalNode`, `BoundQuery`, `querySchema` | `Node`, `Query`, `QuerySchema` |
   | `xxxNode` (the 20 plan nodes) | `XxxNode` |
   | `xxxExpression` (`columnExpression`, `sqlRoutineExpression`, `tableOIDExpression`, ...) | `XxxExpr` (`ColumnExpr`, `SQLRoutineExpr`, `TableOIDExpr`, ...) |
   | `unknownOutput`, `nodeRuntime` | `UnknownOutput`, `Runtime` |
   | `sortKey`, `queryGrouping`, `aggregateCall`, `windowCall`, `windowOperation` | `SortKey`, `Grouping`, `AggregateCall`, `WindowCall`, `WindowOperation` |
   | `boundWindowSpec`, `boundWindowFrame`, `boundWindowBound` | `WindowSpec`, `WindowFrame`, `WindowBound` |
   | `searchRecipe`, `cycleRecipe`, `cteDefinition`, `cteMutation` | `SearchRecipe`, `CycleRecipe`, `CTEDefinition`, `CTEMutation` |
   | `functionCorrelation`, `querySetTemplate`, `querySetStep`, `querySetStepKind`, `querySetSlot`, `queryRowMark` | `FunctionCorrelation`, `SetTemplate`, `SetStep`, `SetStepKind`, `SetSlot`, `RowMark` |
   | `sqlJSONArrayQuery`, other helper types | `SQLJSONArrayQuery`, capitalized |
   | `Truth`, `True`, `False`, `Unknown`, `TruthOf` | same names |

   Fields and methods are capitalized with Go initialisms (`CTE`, `SQL`,
   `ID`); embedded `querySchema`, `setNode`, `derivedNode` and
   `columnExpression` fields take the new type names.
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

### 10e — Node-tree output and generation helpers (linear item 73)

Added when the postponed packages were planned (measured at `c154e14fb`).
Two clean root pieces only walk the plan IR; `frontier` reports no root
declaration they use:

1. `deparse_tree.go` (232 lines): `boundTree`, `appendBoundTree` and
   `catalogTree`, the `pg_node_tree` output of plan nodes. Export them
   (for example `plan.BoundTree`); `DeparseInquiry` and the catalog
   producers call them through the root today.
2. `generated.go`: `generationHasTableOID` and `generationImmutable`, which
   index, table and partition code use (steps 23–25).

Move them with qualified callers, no aliases. Engine tests that call them
are rewritten to the new names.

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

- [x] 10a — `EvalContext` split in place (the commit that records this
  status) *(racecheck/finalcheck skipped at user request)*
- [x] 10b — Opaque CTE mutation and node runtime, in place (`61c56b76b`,
  `55a5497bd`, `d046dec09`, `854c270a9`, `65b24c846`, docs: the commit that
  records this status) *(racecheck/finalcheck skipped at user request)*
- [x] 10c — `plan` created, IR and node types moved (`6189f6483`,
  `00e890338`, `a6188a084`, then the docs commit) *(racecheck/finalcheck
  skipped at user request)*
- [x] 10d — Aliases removed, plus the three 10c follow-ups (`fb6c19185`,
  `367313c0d`, `3860f511e`, `9ba165e93`, `3f66996a4`, `50e07094a`, then the
  docs commit) *(racecheck/finalcheck skipped at user request)*
- [ ] 10e — Node-tree output and generation helpers moved (linear item 73)

Outstanding aliases: none. 10d deleted `plan_aliases.go` (90 aliases).

### 10a result

`EvalContext` (`expression.go`) now embeds `evalFrame`, `evalStatement` and
`evalAdmission`, in that order, and has no other fields. Each group's doc
comment says who writes and reads it. Promoted selectors are unchanged.
Copying `EvalContext` still copies all three groups.

- `evalFrame`: `Scope`, `Frame`, `Row`, `OldRow`, `mergeReturning`,
  `mergeSourcePresent`, `mergeAction`, `orderValues`, `updateOutputScratch`,
  `updateOutputs`, `updateOutputsReady`, `Outer`, `query`,
  `queryOccurrences`, `queryProvenance`, `queryRowSource`,
  `queryCurrentRows`, `physicalTable`, `presenceSlots`, `presence`,
  `domainValue`, `domainFrameUsage`, `domainValueActive`.
- `evalStatement`: `Context`, `Parameters`, `statementScope`, `Input`,
  `Output`, `catalog`, `preparedChecks`, `preparedDomainChecks`,
  `preparedGenerations`, `pgRegex`, `quoteCaptures`, `quoteOrigin`,
  `planStaticOperators`, `specializeRuleParameters`.
- `evalAdmission`: `routineAuthority`, `routineRecipe`, `routineGraft`,
  `routineChecks`, `routineReferences`, `routineConsumer`, `routineProgram`,
  `routineBodyContext`, `domainPhase`.

A type-checked tool (go/types, by key name) rewrote all 744 literals with no
manual cases. No regrouping reordered calls, and no literal held comments or
unkeyed elements. The per-field and per-file census of selectors and keys is
identical before and after. `make bench`: 5,100 steady allocs/op before and
after; ns/op 846–866 µs before and 840–1,070 µs after. The two slow samples
came from a shared, contended host, so this is not a regression claim.

Selected native unit tests and five reference selections passed in quick
mode: subqueries, CTEs, recursive queries, windows, grouping sets,
aggregates, RETURNING, MERGE, domains, generated columns, joins, views,
deparse and Dump round trips. They also passed against real PostgreSQL 18.6,
except for `/postgres` oracle subtests that disagree with recorded
expectations independently of the engine: `DeleteReturningBoundaries` and
four MERGE repeatable-read tests.

**Group reads** (direct field uses in each body, at `4d3568a9d`; helpers
such as `evaluationContext` and `liveScope` are listed separately):

| Reads | Node `Eval` methods |
|---|---|
| statement only | `arithmetic`, `array`, `bitwise`, `case`, `coercion`, `comparison`, `conditional`, `distinct`, `like`, `scalarArrayComparison`, `subscript`, `viewQualification` (all `Context`/`Input`/`Output`); `parameter` (`Parameters`) |
| frame only | `cachedOrder` (`orderValues`), `domainValue` (`domainValue*`), `group`/`grouping` (`Frame`, `Outer`, `Row`, `Scope`), `mergeAction` (`mergeAction`, `mergeReturning`), `returningWholeRow` (`OldRow`), `tableOID` (`Frame`, `Scope`, `physicalTable`), `window` (`Row`), `updateOutput` (`updateOutputs`, `updateOutputsReady`; new since the inventory) |
| frame + statement | `column` (`Frame`, `Row`, `Scope`, `mergeSourcePresent`, `physicalTable`, `queryCurrentRows`, `Context`), `returningColumn` (`OldRow`, `Row`, `Context`), `subquery` (`Row`, `query`, `Context`, `Input`, `Output`, `Parameters`), `sqlRoutine` (`query`, `statementScope`; new since the inventory) |

`coercionExpression.Eval` reads only `Context`, `Input` and `Output`
directly; its live-scope read goes through the `liveScope` method (frame
`query` plus statement `statementScope`).

Of the `EvalContext` methods: 18 read only the statement group (the
`Function*` capabilities, `Advisory`, `RoleMembership`, `DeparseInquiry`,
`LookupObjectAddress` and others) and 8 only the frame (`bindQueryRow`,
`bindQueryIdentity`, `queryOperandSource`, `queryOperandAbsent`,
`setQueryPresence`, `expressionPrincipal`, `IdentityValue`,
`FunctionStatementTime`). Four read frame and statement (`CheckDomain`,
`evaluateSelectedDomainCheck`, `liveScope`, `setContext`). Two read only
admission (`routineAdmissionAuthority`, `selectExpressionRecipe`). Only
`evaluateOwnDomainChecks` reads all three, and only `prepareOwnDomainChecks`
reads statement plus admission. Step 11 can treat admission as a separate
preparation-time value.

Notes for later steps:

- Step 11 moves `EvalContext` itself (as `exec.Context`) with the `Eval*`
  bodies. 10a's group record tells it which state is per-frame.
- Step 12: the binder constructs most node types; after 10c it names
  `plan.` types.

### 10b result

Re-measured at `3745e9011`: 42 engine node `Eval` methods (the Tier C nodes
had already moved to `functions`); `updateOutputExpression` (frame) and
`sqlRoutineExpression` (frame and statement) are new since the inventory, and
`rowExpression`/`fieldExpression` read frame state through `absent`.
`cteDefinition.mutation` had 77 references (47 production, 30 test).

- **CTE mutation.** `cteDefinition.mutation` is the engine-local marker
  interface `cteMutation` (`cte_mutation_payload.go`), implemented by
  `*binding`; `mutationBinding(d) (*binding, bool)` is the one checked
  accessor. Plan-side code (`folded`, `outputSchema`) tests presence only.
  Writers always store a non-nil `*binding`, so nil checks and identity
  comparisons keep their meaning. 27 direct field references remain (nil
  checks, writers, identity), all others go through the accessor.
- **Node runtime.** `nodeRuntime` (`node_runtime.go`) embeds `expr.Context`
  and is implemented only by `*EvalContext` (compile-time assertion):
  `BeginEvaluation` (the unchanged `evaluationContext` prologue),
  `ApplyCoercion`, `RowAbsent`, `FrameValue`, `ParameterValue`, and
  `EvalColumn`, `EvalGroup`, `EvalGrouping`, `EvalTableOID`,
  `EvalReturningColumn`, `EvalReturningWholeRow`, `EvalMergeAction`,
  `EvalCachedOrder`, `EvalWindow`, `EvalDomainValue`, `EvalUpdateOutput`,
  `EvalSubquery` and `EvalSQLRoutine`. `nodeRuntimeOf(ctx)` recovers it; a nil
  or foreign context yields the prologue's missing-context error.
  `beginEvaluation(ctx)` recovers it and runs the prologue.
- **Statement-only nodes** (literal, array, subscript, scalar-array
  comparison, row, field, CASE, conditional, logical, row comparison, LIKE,
  comparison, arithmetic, bitwise, distinct, coercion, unnest/series/
  subscripts `Eval` and `OpenSource`, assignment value, view qualification)
  read `FunctionContext()`, `FunctionInput()` and `FunctionOutput()` at the
  points where they read the fields before. Nodes that never ran the prologue
  still do not. Their helpers take `expr.Context`: `evaluateArrayBounds`,
  `rowComparisonExpression.compare`, `subqueryExpression.compare`,
  `arrayAssignmentTarget.apply*`, `queryPredicateProofValue`. The binder-side
  `arrayAssignmentTarget.diagnostic` became
  `expressionBinder.arrayAssignmentDiagnostic`.
- **Coercion** cannot use `FunctionInput()`: when the evaluation context
  lacks the Regrole, Regcollation or Regnamespace input resolvers it borrows
  them from the live statement scope, and it wires domain checks to the
  context. `ApplyCoercion` holds that assembly verbatim and applies the plan.
- **Frame and executor nodes** delegate to their `Eval*` method; the bodies
  moved unchanged. Their dirty helpers became `*EvalContext` methods taking
  the node: `columnCapturedGeneration`, `admitColumnVirtualGeneration`,
  `subqueryMembership`, `subqueryHashMembership`, `subqueryJSONArray`,
  `subqueryUpdateOutputs`; `RowAbsent` replaced `rowExpression.absent`, and
  `frameRow` (the column frame walk) replaced `columnExpression.rowContext`.
- **Left as they were:** `installedFunctionExpression` and
  `extensionPathsExpression` still recover `*EvalContext` through
  `evalContext` (extension step 16); Tier C lives in `functions`.

The `reach` census (appendix tool) is clean for every node type, `cteDefinition`,
`cteMutation`, `nodeRuntime` and `arrayAssignmentTarget`, except the two
extension nodes. Among the plan IR only `functionFromNode.openCall` (a query
opener) still names `*EvalContext`.

**§7 decision 9 — settled: `Eval` stays on the node types (they move to
`plan` in 10c), with engine state behind the node runtime.** The user
accepted the measured cost. Per evaluated node the interface dispatch adds
about 2.6 ns to the prologue (micro-benchmark: 2.3 ns before, 4.9 ns after).
The first cut also copied 300-byte nodes through `EvalColumn` and
`EvalParameter`; the scalar `FrameValue`/`ParameterValue` paths and
`ApplyCoercion` removed most of that. Final throwaway evaluation benchmark
(database/sql over `generate_series` and a 5,000-row table; eight
alternating runs pinned to two CPUs, median/minimum vs `3745e9011`):
arithmetic and comparisons +2.8%/+7.0%, CASE/COALESCE/GREATEST +7.2%/+8.5%,
casts +5.8%/+3.9%, table scan with LIKE and row comparison +16.1%/+11.9%.
Allocations per query are unchanged in all seven cases (also subquery,
window and grouping sets). Moving the prologue into `expr.Context`, or
returning the environments by pointer, gave no measurable gain and was
dropped. `make bench`: 5,100 steady allocs/op before and after; ns/op
1.19–2.92 ms before and 2.27–3.51 ms after on a heavily shared host, so
not a startup comparison.

Tests: new `TestNodeRuntimePrologue` (prologue seam: nil, typed-nil and
foreign contexts, cancellation, domain-check wiring before input reads).
Adapted: `alter_generation_virtual_test.go` (`columnCapturedGeneration`) and
the CTE/rule tests that read the mutation binding (`rule_cte_test.go`,
`rule_named_dependency_test.go`, `rule_named_dump_test.go`,
`rule_inference_dump_test.go`, `cte_dml_atomicity_test.go`) through the
test helper `cteMutationOf`. None moved or deleted.

Notes for later steps:

- 10c: convert `functionFromNode.openCall` to an engine function before the
  move. The node runtime becomes `plan.Runtime`; its `Eval*` methods name
  `plan` node types, so `*EvalContext` keeps the bodies. The node types are
  copied by value through the runtime (about 300 bytes each); keep new hot
  paths on scalar arguments like `FrameValue`.
- Step 11: the `Eval*` bodies move with `EvalContext` into `exec`. A cheaper
  per-node dispatch (batching, or compiling statement-only subtrees) is the
  place to win back the 10b cost.
- Step 16: the installed-function and extension-path nodes still recover
  `*EvalContext`.

### 10c result

Re-measured at `1ecaa1876`, `cdf48fdd6` (after 13a) and `01c1a9ec4` (after
16a): the move
set is the 20 plan nodes, `LogicalNode`, `BoundQuery`, `querySchema`, the
records, `cteMutation`, the 41 expression node types (all but the two
extension nodes) and `nodeRuntime`. A fixed-point frontier pass (15a
`frontier` tool) added the helper types and functions they need:
`Truth`/`TruthOf`, `caseBranch`, `boundArraySubscript`, `boundWindowFrame`,
`boundWindowBound`, `rowFieldProjection`, `compositeExpansionBinding`,
`mergeReturningSide`, `wholeRowSource`, `sqlJSONArrayQuery`,
`querySetStepKind`, all `windowOperation` constants,
`arrayAssignmentTarget`, `decisiveLogicalValue`, `evaluateArrayBounds`,
`queryPredicateProofValue`, `sameIntaggIdentity`, `unnestInputTypes` and the
series/unnest source cursors: 92 types, about 3k lines.

- `6189f6483` (split): `functionFromNode.openCall` became the engine
  function `openFunctionCall` (it named `*EvalContext`). The moving
  declarations of 56 mixed files moved unchanged into fifteen
  `plan_*.go` topic files; eight files moved whole.
- `00e890338` (export in place): `movex` member mode capitalized the 430
  fields and methods and 18 functions and constants that other engine code
  uses (14,490 references; the per-object census is identical before and
  after). The `cteMutation` marker method became `CTEMutationPayload`, on
  the interface and on `*binding`.
- `a6188a084` (move): 23 files into `internal/engine/plan` with the naming
  table above (alias mode inside the set, embedded-field renames,
  qualification outside), `plan_aliases.go`, and a private `sqlError` copy
  in `plan/errors.go`. `plan` imports `catalog`, `expr`, `functions`, `ids`,
  `lock`, `parser`, `sqlstate`, `sqltype` and the standard library; no
  engine subpackage imports the engine, and no `plan` declaration names
  `*EvalContext`. The `Eval*` bodies stay on `*EvalContext`.
- The commits are produced by re-runnable scripts, so the rebases over 13a
  and 16a regenerated them on the new tip instead of merging conflicts (16a
  only needed one more split mapping: `sameIntaggIdentity` now lives in
  `intagg_identity.go`); the regenerated `plan` package and alias file are
  byte-identical to the first pass.
- Tests: no engine test covers only moved code, so none moved or was
  deleted. Adapted mechanically: `object_address_routine_test.go`
  (`openFunctionCall`), embedded-field keys and selectors
  (`QuerySchema:`, `SetNode:`, ...), and four tests that match type or
  frame names: `query_window_test.go` and `query_window_exclude_test.go`
  (stack-frame phases `ColumnExpr.Eval`, `LiteralExpr.Eval`,
  `WindowExpr).Eval`), `expression_protocol_test.go` and
  `intagg_routine_authority_test.go` (`%T` names `plan.LiteralExpr`,
  `plan.ParameterExpr`).
- Observable only in internal invariant errors: messages that print a node
  with `%T` (for example "unsupported deparse node %T") now name
  `plan.XxxExpr` types. No reference or smoke test matches them.
- `go vet` composites findings grow by 145 (2,719 to 2,864 at `01c1a9ec4`): unkeyed engine
  literals of node types (mostly `literalExpression` and
  `coercionExpression`) are now literals of an imported package. Other vet
  findings are unchanged.
- Verification: build, vet and the 1,181 targeted engine unit tests after
  every commit; `make quickcheck` passed before and after the rebase, and
  `make deps` lists only module paths. The five step-10 reference
  selections passed in quick mode (inside quickcheck) and with
  `REFERENCE=1`, before and after the rebase, except the known oracle-side
  `/postgres` drift (`DeleteReturningBoundaries` and four MERGE
  repeatable-read tests); every stuntgres subtest passed. Startup bench
  (`make bench`, two interleaved rounds of five vs `cdf48fdd6`, host load
  about 10): median 892 µs/op before and 874 µs/op after, 5,018–5,027
  allocs/op on both. Evaluation benchmark (the 10b throwaway, six
  interleaved runs, median/minimum): arithmetic −0.4%/−0.0%, coercion
  +3.3%/+0.4%, conditional −4.7%/−1.1%, grouping −1.5%/−8.2%, subquery
  +0.7%/+1.0%, table scan +1.3%/+1.8%, window −5.2%/−14.4%; allocations
  identical. The move adds no cost. Racecheck and finalcheck skipped at
  user request.

### 10d result

Measured at `faaebbf0d` (after 13b and 16b) with go/types over the engine
package and its tests. The user asked for the three 10c follow-ups in the
same sub-step: keyed node literals, the `CTEDefinition` rename and the
installed-node move.

- `fb6c19185` (shadowing): 82 locals and parameters named `plan` would have
  captured an inserted `plan.X` (232 rewrite sites). They were renamed by
  type with a free-name check at every use: `coercionPlan` (56),
  `stringPlan`, `binaryPlan`, `enumSupportPlan`, `queryPlan` (`*plan.Query`
  in three tests), ... and `arithmetic` once; 205 identifiers in 65 files.
  Other `plan` locals (not at rewrite sites) keep their name; new code that
  names the package inside their scope must rename them first.
- `367313c0d` (aliases): 6,360 references in 596 engine files (largest
  `projectNode` 640, `literalExpression` 596, `columnExpression` 414,
  `LogicalNode` 408, `querySchema` 218, `coercionExpression` 184) now name
  `plan.` types; `plan_aliases.go` is deleted. No package outside the
  engine used the exported `BoundQuery`, `LogicalNode` or `Truth`. Four
  comments and one test message named old types. The tool's gofmt pass
  reformatted two files that were not gofmt-clean on master
  (`window_frame_bind.go`, `query_window_exclude_remedy_test.go`).
- `3860f511e` (keyed literals): the 145 positional engine literals of
  `plan` types got field keys; positional `false`/`nil` elements were
  dropped (same zero value). `go vet` findings for the engine went from
  2,904 to 2,759 and back to 2,760 after the move (none names a `plan`
  type); the others are unchanged.
- `9ba165e93`: `SubqueryExpr.CTEDefinition` (the analysis-only CTE edge
  flag) is now `CTEEdge`; 23 references in 17 files.
- `3f66996a4` and `50e07094a` (installed nodes): the six installed
  extension wrappers and the `pg_extension_update_paths` node moved to
  `plan` as `InstalledTextExpr`, `InstalledCryptoExpr`,
  `InstalledUUIDOsspExpr`, `InstalledMetaphoneExpr`,
  `InstalledLevenshteinExpr`, `InstalledSoundexExpr` and
  `ExtensionPathsExpr` (`expression_installed.go`,
  `expression_extension_paths.go`), with exported identity fields
  (`Procedure`, `Ref`, `Dictionary`, `Template`, `Arg`) and their clean
  `Rebuild` and source-protocol methods. The provider validators read
  `extension` registries, which `plan` must not import, so they became
  engine functions taking the node (`validInstalledText`,
  `validInstalledUUIDOsspShape`, `validateInstalled*`) and stayed in their
  files, as did `closedPgcryptoDefault`, `cryptoExpressionSignature` and
  `bindExtensionPaths`. Two `plan.Runtime` methods hold the former engine
  state reads: `EvalInstalledText` (prologue, provider check, kernel) and
  `OpenExtensionPaths`. A nil or foreign context still fails with "missing
  expression context". No production code recovers `*EvalContext` from an
  `expr.Context` any more; `evalContext` is a test helper
  (`eval_context_test.go`).
- Tests: none moved or deleted. Adapted: renamed identifiers throughout;
  `expression_protocol_test.go` expects `%T` `plan.InstalledSoundexExpr`;
  `TestViewLiteralProtocolInstalledWrapper` lost its test-only `Capture`
  override on the UUID wrapper (Go allows no methods on an imported type).
  That override proved the view-literal capture takes the explicit
  installed-wrapper branch before the promoted `Capture` hook; for this node
  both paths capture the same operands, so the order is no longer
  observable. The test's capture, remap-identity and validation assertions
  are unchanged.
- `plan` imports are unchanged (`catalog`, `expr`, `functions`, `ids`,
  `lock`, `parser`, `sqlstate`, `sqltype` and the standard library); no
  engine subpackage imports the engine.
- Verification: `go build` and `go vet` (diffed against master) after every
  commit, and a targeted engine unit selection of 1,551 tests (the 10c set
  plus every test in a changed engine test file and the extension wrapper
  tests) after every commit. The six reference selections (the five step-10
  sets and the extension set in the README's `## Testing`) passed in quick
  mode and with `REFERENCE=1` against PostgreSQL 18.6, except the known
  oracle-side `/postgres` drift (`DeleteReturningBoundaries` and four MERGE
  repeatable-read tests); every stuntgres subtest passed. `make quickcheck`
  passed and `make deps` lists only module paths. Startup bench (`make
  bench`, two interleaved rounds of five vs `faaebbf0d`, host load about 7):
  median 853 µs/op before and 867 µs/op after, 5,014–5,022 allocs/op on
  both; the difference is within the run-to-run noise. Racecheck and
  finalcheck skipped at user request.

Notes for later steps (10c):

- 10d (done): shadowing locals renamed, the 145 node literals keyed and
  `SubqueryExpr.CTEDefinition` renamed `CTEEdge` (see 10d result).
- 11a: `EvalInstalledText` and `OpenExtensionPaths` (`extension_expression.go`,
  `extension_paths.go`) join the `Eval*` bodies on `*EvalContext` that step
  11 inventories.
- 11a: `query_search.go` (`searchRecipe`) moved whole into `plan`, so the
  exec inventory no longer lists it; the series, subscripts and unnest
  source cursors moved with their nodes.
- 12c: the binder constructs `plan` types directly since 10d.
- 16a decided that the installed expression wrappers
  (`installedFunctionExpression`, `installedCryptoFunctionExpression`,
  `installedUUIDOsspFunctionExpression`) and `extensionPathsExpression` go
  with `plan`. 10c left them in the engine: their `Eval` still recovers
  `*EvalContext` through `evalContext`, which `plan` must not name. Moving
  them needs a `plan.Runtime` method (or a `functions` runtime) for the
  state they read first; do it in 10d or a follow-up sub-step. Done in 10d
  with `EvalInstalledText` and `OpenExtensionPaths`.

