# Engine split, step 12: `internal/engine/analyze`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-analyze.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §6 step 12; §3 (L8); §4.1
  patterns 3 and 4; §7 decisions 8 and 11.
- **Needs:** nothing for 12a and 12b (in-place regrouping). 12c needs 10c;
  12d needs 12c and 11c. Read the F3/F4 records in
  [`ENGINE-SPLIT-functions.md`](ENGINE-SPLIT-functions.md) (the
  `functions.Binder` interface is the model for a reviewed host surface).
- **Effort:** high for 12a and 12b; xhigh for 12c; high for each 12d
  session.
- **Session size:** one sub-step per session; 12d spans several sessions (one
  area each). 12a, 12b and 12d are broad rewrites.

## Goal

Statement binding becomes a package. Before it moves, its two god structs
become readable: `binding` gets one payload per statement kind, and
`expressionBinder` gets named field groups. The binder then reaches the
engine only through a reviewed host interface.

## Inventory (measured at `75fe0d710`)

**`binding`** (`engine.go:61`, 69 fields). Uses (selectors plus literal keys)
and main files:

- header candidates: `relation` 335 (46 files), `ref` 140, `targets` 128,
  `returning` 120, `query` 102, `querySources` 101, `viewWrite` 62,
  `arrayTargets` 60, `ctes` 52, `expressions` 51, `dependencies` 47,
  `predicate` 44, `storage` 28, `parameters` 22, `usedColumns` 15,
  `currentOf` 15 (UPDATE and DELETE), `subqueryDemand` 14, `commandQuery` 12,
  `visibilitySnapshot` 8, `cteSelectBodies` 5;
- already payloads: `update *boundUpdate` 134, `merge *boundMerge` 34;
- INSERT: `conflict` 113 (mostly `rule_*`), `defaults` 65,
  `identityDiscard` 23, `insertProjection` 17, `insertRows` 10,
  `insertSubquerySource` 5;
- DELETE: `deleteUsing` 35, `deleteSourceWidth` 4;
- CREATE TABLE (2–9 uses each, in `schema.go`, `engine.go`,
  `table_inheritance*.go`): `createColumns`, `createNotNull`,
  `createColumnIndex`, `createUniques`, `createDeclarations`, `createLikes`,
  `createOfType`, `createInheritance`, `createLocalColumns`;
- rules: `ruleTemplate` 8, `ruleCTEs` 17;
- view and rule rendering capture (5–6 uses each, in `view_definition.go`,
  `rule_definition.go`, `rule_action_presentation.go`,
  `policy_expression.go`, `engine.go`): `viewLiterals`, `viewCastUsages`,
  `viewIntaggRefs`, `viewOperatorRefs`, `viewComposites`,
  `viewSimilarSubstrings`, `viewPosixSubstrings`, `viewCollations`,
  `viewColumns`, `viewStars`, `viewNamespaces`;
- prepared captures: `sequenceConstants` 28, `regtypeConstants` 20,
  `temporals` 3, `regclassConcat` 3, `composites` 2;
- resolver inputs (4 uses each, in `execute.go`, `engine.go`,
  `materialized_snapshot.go`): `regclassInput`, `regtypeInput`,
  `regroleInput`, `operatorInput`, `regprocInput`, `regcollationInput`,
  `regnamespaceInput`, `regTSInput`, `aclitemInput`, `ftsInput`, plus
  `typeDiagnosticName` and `enumMemberSafe`.

`binding` is produced by `describeWithRule` (`engine.go:465`; `describe`
forwards to it), which copies binder fields into it after a type switch
(CREATE TABLE inline; INSERT/UPDATE/DELETE/MERGE through
`bindMutationCommand`, `cte_mutation_bind.go:13`; SELECT and DECLARE CURSOR
through `bindSelect`). 23 `binding{` literals in 15 files; 102 functions take
a `binding`; 19 functions return one (mostly `rule_*`).

**`expressionBinder`** (`expression.go:1204`, 101 fields, 348 methods in 103
files; top files `function_binder.go` 39, `expression.go` 33,
`composite_expression.go` 14, `array_comparison_syntax.go` 11,
`window_bind.go` 10). Uses per field:

- core: `engine` 42 (18 files), `ctx` 25, `op` 35 (12 files), `expected`
  57, `input` 63;
- target: `relation` 37, `insertTarget` 7, `generatedOwner` 10, `ref` 11,
  `ruleNamespace` 14, `deleteTarget` 6, `deleteUsingSources` 7, `mergeWhen`
  6, `mergeInput` 9, `targetName` 19, `targetAlias` 28, `returningAliases`
  28, `returningReadRequired` 9;
- parameters: `parameters` 26, `formalParameters` 4, `executeParameters` 9,
  `updateParameters` 15, `updateInputParameters` 16, `anyParameters` 17,
  `noParameters` 43;
- consumer: `defaultConsumer` 19, `defaultTemporary` 4,
  `datetimeZoneOperands` 5, `checkConsumer` 11, `policyConsumer` 7,
  `indexConsumer` 8, `indexPredicateConsumer` 9, `partitionConsumer` 10,
  `checkDefinitionEquality` 4, `visibilitySnapshot` 13, `catalogSnapshot` 10,
  `materializedDefinition` 29;
- captures: `uses` 16, `dependencies` 25, `temporals` 23, `composites` 10,
  `sequenceConstants` 7, `regtypeConstants` 33, `regclassConcat` 11, the
  view maps (5–22 each), `viewNamespaces` 15, `checkLiterals` 6,
  `storedExpressionSyntax` 9, `viewValuesCount`/`viewJoinCount`/
  `viewFunctionCount` 2 each;
- clause: `aggregateClause` 67, `stringSetGuard` 12, `stringFromForbidden`
  13, `stringSingleInsert` 6, `queryHasTargetSet` 10, `fromSetObservation`
  11, `regexpClause` 16, `jsonbRecordBase` 6, `domainValueUsage` 4,
  `functionProjection` 13, `selectedOperatorArgs` 12;
- grouping and windows: `aggregates` 31, `groupingActive` 6, `groupSets` 6,
  `windowRaw` 10, `windowDefinitions` 11, `windowCalls` 38,
  `argumentOwner` 19;
- query scope: `queryAuth` 47, `querySources` 38, `queryLockAdmission` 17,
  `queryNamespace` 81, `queryHidden` 58, `queryNonLateral` 8, `queryFrame`
  21, `nextQueryFrame` 9, `resolvedQueryFrame` 5, `queryScopes` 40,
  `presenceFrames` 4, `suppressWholePresence` 7, `queryRowDiagnosticMode` 8,
  `subqueryDemand` 9, `groupSubquery` 5;
- CTE: `cteBindings` 18, `recursiveDefinition` 4, `recursiveFrames` 4,
  `cteScanOrdinal` 5, `cteAnalysisDepth` 3, `cteExecutables` 3,
  `statementCTEs` 3, `cteSelectBodies` 2.

`newExpressionBinder` (`expression.go`): 21 calls in 20 files; 6
`expressionBinder{` literals.

**Engine services the binder calls** (distinct calling declarations):
`resolveAnnotationTypeInput`, `resolveAnnotationType`,
`resolveStringAnnotationType`, `resolveColumnCollation`, `lookupNamespaces`,
`authorizationLocked`, `resolveType`, `resolveReference`, `regclassLocked`,
`referencePath`, `lookupPath`, `lookupNamespacesSnapshot`,
`captureTypeDiagnosticName`, `captureTypeDiagnosticInput`,
`captureRegtypeInput`, `captureOperatorInput`, `captureElementInput`,
`bindMutationCommand`, `authorizationStorageLocked`, `admittedReference`;
fields `Engine.catalog`, `Operation.session`, `Operation.definition`,
`Session.token`, `Session.discardPlansGeneration`; scope fields
`routineAuth`, `output`, `foreignActionAuth`; `Operation.emitNotice`;
`StatementScope.DecodeLockedDomainInput`.

## Sub-steps

### 12a — Split `binding` per statement kind (broad)

1. Add kind payloads next to `boundUpdate` and `boundMerge`:
   - `insertBinding`: `insertSubquerySource`, `insertRows`, `defaults`,
     `identityDiscard`, `insertProjection`, `conflict`;
   - `deleteBinding`: `deleteUsing`, `deleteSourceWidth`;
   - `createTableBinding`: the nine `create*` fields.
   `binding` holds `insert *insertBinding`, `delete *deleteBinding`,
   `create *createTableBinding`. `currentOf` stays in the header (UPDATE and
   DELETE).
2. Add three value groups, shared with `expressionBinder` in 12b:
   - `viewCapture`: the ten `view*` maps and `viewNamespaces`;
   - `preparedCaptures`: `temporals`, `composites`, `sequenceConstants`,
     `regclassConcat`, `regtypeConstants`;
   - `resolverInputs`: the ten `*Input` resolvers, `typeDiagnosticName` and
     `enumMemberSafe`.
   `binding` holds them by value as `capture`, `prepared`, `resolvers`.
3. Group `ruleTemplate` and `ruleCTEs` into `ruleBinding` (by value, `rule`).
4. Rewrite selectors and literal keys with a `go/types` tool (field →
   path), with a per-field census before and after. Then simplify
   `describeWithRule`: it copies whole groups instead of fields.
5. Result: about 22 header fields. Record the final list here.

**Result (done).** The payloads and groups are held by value, not by pointer:
`binding` is copied by value and the copies are mutated (preflight, rule
remap and graft, MERGE arms, view-write INSERT), and generic code reads
`delete.using` and `insert.defaults` on every kind. Value groups keep the copy
and zero-value semantics of the flat fields. Group fields drop the redundant
prefix (`deleteUsing` → `delete.using`, `createColumns` → `create.columns`,
`viewLiterals` → `capture.literals`, `regclassInput` → `resolvers.regclass`,
`ruleCTEs` → `rule.ctes`); `defaultOrigins` (new since the inventory) joined
`insertBinding`, and `viewIntaggRefs` is now `capture.routineRefs`. The types
live in `binding_groups.go`; `newResolverInputs` captures the resolvers.

- Header (20): `relation`, `storage`, `ref`, `viewWrite`, `targets`,
  `arrayTargets`, `usedColumns`, `query`, `ctes`, `cteSelectBodies`,
  `querySources`, `visibilitySnapshot`, `subqueryDemand`, `commandQuery`,
  `currentOf`, `predicate`, `returning`, `dependencies`, `parameters`,
  `expressions`.
- Kind payloads: `update *boundUpdate`, `merge *boundMerge`,
  `insert insertBinding`, `delete deleteBinding`, `create createTableBinding`.
- Groups: `capture viewCapture`, `prepared preparedCaptures`,
  `resolvers resolverInputs`, `rule ruleBinding`.

### 12b — Group `expressionBinder` fields (broad)

1. Group fields into named sub-structs held by value (not embedded, so
   ownership is explicit at each use): `target targetContext`, `params
   parameterState`, `consumer consumerContext`, `capture viewCapture` (from
   12a, plus `checkLiterals`, `storedExpressionSyntax` and the three view
   counters), `prepared preparedCaptures` (from 12a, plus `uses` and
   `dependencies` if they always travel together; check), `clause
   clauseState`, `grouping groupingState` (aggregates and windows), `query
   queryScopeState`, `cte cteState`. `engine`, `ctx`, `op`, `expected` and
   `input` stay at the top level until 12c.
2. Rewrite with the same tool and census. `queryNamespace` (81 uses) and
   `aggregateClause` (67) are the largest.
3. Where a method uses only one group, consider moving it onto that group
   type (for example window definition lookups onto `groupingState`). Do this
   only when it removes the binder from the signature; record each move.
4. The `functions.Binder` methods stay on `*expressionBinder`.

### 12c — Refine, then host seam

1. **Refine (docs only, first commit of the session):** after 10c and 11c,
   re-run the appendix tool for the binder files with `binding` and
   `expressionBinder` as roots. List every path to `Engine`, `Operation`,
   `Session`, `StatementScope` and `EvalContext`. Decide whether constant
   evaluation (preflight, folding, literal input) imports `exec` or gets a
   narrower evaluation capability. Write the `analyzeHost` method list and
   the 12d area order here; wait for approval.
2. Declare the engine-local `analyzeHost` interface; implement it on a root
   adapter. Replace `expressionBinder.engine` and `op` with the host. Each
   method is a reviewed decision, as for `functions.Binder`: a genuine
   analyzer capability, or a leak to remove by passing a value.
3. `describe`/`describeWithRule` take the host instead of `*Engine`.

### 12d — Move, area by area (broad; several sessions)

Each session re-measures the clean set for its area, exports in place, moves,
and leaves aliases:

1. `binding`, its payloads and the capture types; `expressionBinder` and its
   group types; `newExpressionBinder`.
2. Expression core: `expression.go` binder methods, `function.go`,
   `function_binder.go`, `function_column_definitions.go`,
   `function_from_filter.go`, `operator_binding.go`,
   `operator_routine_binding.go`, `array_comparison*.go`,
   `expression_like.go`, `expression_row_comparison.go`,
   `composite_expression.go`, `composite_capture.go`, `enum_function.go`,
   `range_more.go` and `range_operator.go` binders, `regtype_prepared.go`.
3. Query, FROM and CTE binding: `query_bind.go`, `query_from_bind.go`,
   `query_cte_bind.go`, `query_cte_fold.go`, `query_cte_view.go`,
   `query_recursive_bind.go`, `query_set_bind.go`, `query_order_bind.go`,
   `query_distinct*_bind.go`, `query_search_bind.go`,
   `query_cycle_bind.go`, `query_rewrite.go`, `query_target_compare.go`,
   `query_name_hint.go`, `query_values_view.go`,
   `query_expression_walk.go`, `query_join_legality.go` (if 11a left it
   binder-side), `string_table_bind.go`, `string_set_lower.go`.
4. Aggregates, grouping and windows: `aggregate_bind.go`,
   `aggregate_modifiers.go`, `aggregate_definitions.go`,
   `aggregate_identity.go`, `aggregate_dispatch.go`,
   `grouping_sets_bind.go`, `window_bind.go`, `window_frame_bind.go`.
5. DML targets and describe: `cte_mutation_bind.go`, `insert_binding.go`,
   `returning.go` binders, `describe`.

### 12e — Remove the aliases (broad)

Use the alias tool; audit locals named `analyze`.

## Stays in the engine

Statement dispatch; rule rewriting (`rule_*.go`, which produce and transform
`binding` values), view and policy capture consumers
(`view_definition.go`, `rule_definition.go`, `policy_expression.go`),
materialized snapshots, prepared-statement rebinding (step 13), and the
central switches that are deparse or remap rather than binding (views
package, postponed).

## Invariants

- Bound results, inferred parameter types, errors, SQLSTATEs, positions and
  notices stay identical.
- Prepared SQL rules in `AGENTS.md` (analysis-identity cells, invalidation,
  retained identities, temporal/sequence/regtype captures) are unchanged:
  12a only regroups the captures they rely on.
- Stored and deparsed SQL (views, rules, policies, defaults, checks) stays
  byte-identical; the capture maps keep their contents and iteration rules.

## Targeted tests

- Engine units: grep `*_test.go` for `binding{`, `newExpressionBinder`,
  `expressionBinder{`, `describe(`. Adapt mechanically.
- Reference selections (quick mode and `REFERENCE=1`; grep `reference/`):
  prepared statements and parameter inference, INSERT (ON CONFLICT,
  defaults, identity), UPDATE, DELETE USING, MERGE, RETURNING, CREATE TABLE
  (LIKE, OF, INHERITS), views and rules (deparse), policies, CTEs, window and
  aggregate binding errors, and native Dump/Restore.
- `make bench` after 12a and each 12d session (statement preparation).

## Documentation

- Create `internal/engine/analyze/README.md` (12d, first session): the
  binding header and payloads, the binder groups, the host interface with
  the reason for each method, what stays in the engine, `## Testing`.
- Update the `functions` README (`Binder` is now implemented in `analyze`),
  the `access` README (binding walkers), and
  `docs/design/PG18-ARCH-EXPRESSION-ANALYSIS.md` and
  `PG18-ARCH-QUERY-EXECUTION.md` where they name moved types.
- `AGENTS.md`: add `analyze` to the shared subpackage bullet; the prepared SQL
  bullets stay until step 13 moves the prepared registry.

## Done when

`analyze` imports nothing above L8. `binding` has no kind-specific header
fields and `expressionBinder` has no ungrouped fields except the host and the
statement inputs. The root package calls one describe entry point. The checks
in §8.3 pass.

## Status

- [x] 12a — `binding` split per statement kind (`50c0912f5`). A
  type-checked tool rewrote 618 selectors and 10 composite literals (7
  files, 3 of them tests); per-field use totals are identical before and
  after (the `describeWithFormals` copies became whole-group literals). No
  tests moved or deleted. Targeted engine units, the selected reference
  tests in quick and real 18.6 modes, and quickcheck passed; racecheck and
  finalcheck skipped at user request. Startup bench unchanged (5,100
  allocs/op).
- [ ] 12b — `expressionBinder` fields grouped
- [ ] 12c — Refined; host seam in place
- [ ] 12d — `analyze` created and binding moved (sessions: binding types,
  expression core, query/FROM/CTE, aggregates/windows, DML targets)
- [ ] 12e — Aliases removed

Outstanding aliases: none yet.

Notes for later steps:

- 12b: the binder's `view*` maps, `viewNamespaces`, `temporals`,
  `composites`, `sequenceConstants`, `regclassConcat` and `regtypeConstants`
  map onto `viewCapture` and `preparedCaptures` with the 12a names
  (`viewLiterals` → `literals`, ...). Once the binder holds the groups,
  `describeWithFormals` copies `binder.capture` and `binder.prepared`
  directly, and the subset copies in `rule_definition.go` and
  `policy_expression.go` stay subsets (copying full groups could change
  deparse). Item 19's literal tool plus this step's `bindsplit` (selector and
  literal regrouping by field object) are the model for the rewrite.

- `access` B5 moves the binding walkers once 12d has moved `binding`.
- Step 15 (`typedef`): the composite, enum and range binders move here, not
  with `typedef`.
- Step 14 (`routine`): function and operator call resolution moves here.
