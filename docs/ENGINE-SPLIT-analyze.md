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

**Result (done).** Ten value groups, declared in `binder_groups.go` (the
two shared 12a types stay in `binding_groups.go`). Group fields drop only a
leading group-name prefix (`targetName` → `target.name`, `queryNamespace` →
`query.namespace`, `cteBindings` → `cte.bindings`, `groupingActive` →
`grouping.active`, the 12a names for `capture`); all other names are kept.

- Top level (6): `engine`, `ctx`, `op`, `expected`, `input` (host and
  statement inputs until 12c) and `dependencies`. `dependencies` does not
  travel with the prepared captures (`binding` keeps it in its header and
  25+ readers outside describe use it), so `preparedCaptures` stays the
  exact 12a type.
- `target targetContext` (14): relation, ref, insert/delete/merge target
  flags, rule namespace, generated owner, name/alias, RETURNING aliases and
  `uses`, which is saved and restored with name/alias.
- `params parameterState` (8), `consumer consumerContext` (12, including the
  `visibilitySnapshot`/`catalogSnapshot` demands).
- `capture viewCapture` and `prepared preparedCaptures`: the 12a types, so
  `describeWithFormals` copies `binder.capture` and `binder.prepared` whole.
  The policy expression capture listed all 11 `viewCapture` fields and now
  copies the group too; the `rule_definition.go` capture and the policy
  `prepared` copy are real subsets and stay field lists.
- `stored storedSyntaxState` (5, binder-only): `checkLiterals`,
  `storedExpressionSyntax` → `syntax` and the three view occurrence counters.
  Deviation from step 1: they are not in `capture`, which would have made
  `binding.capture` carry them.
- `clause clauseState` (15, with the five `selected*` call slots added since
  the inventory), `grouping groupingState` (8; `groupSubquery` is here, not
  in query scope, because only aggregate and grouping-set binding uses it),
  `query queryScopeState` (14), `cte cteState` (8).

A type-checked tool rewrote 2,263 selectors and 17 composite literals in
256 files (96 production, 160 test). Per-field use totals are identical before
and after except the 27 field copies that became whole-group copies (11
capture fields in describe and in the policy capture, 5 prepared fields in
describe).

Method moves (step 3), each touching one group and nothing else of the
binder: `captureComposite` → `preparedCaptures`; `captureViewComposite`,
`captureRoutineReference` → `viewCapture`; `noteFunctionSignature` →
`clauseState`; `partitionContextPlural`, `partitionExpressionError` →
`consumerContext`; `outerQualifiers`, `freezeViewColumns`,
`partialIndexCandidate` → `queryScopeState`; `returningPseudo` →
`targetContext`. `frameID` also qualifies but stays on the binder (129
callers). `queryAliasVisible`, `queryQualifierNeedsLateral`,
`wholeRowPresence` and `outerReturningScope` also touch only `query`; they
can move with 12d's query area. No window definition lookup qualified: they
all read other groups too.

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

**Refine result (approved; measured at `12d7ef8b9`, before 11c).** The 11a
`paths`, `frontier` and `reach` tools, rooted at `expressionBinder` and
`newExpressionBinder` (outputs in
`.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-HOST/`).

- **Direct paths.** `b.engine` has 42 uses and `b.op` 34 in binder methods.
  Outside them, production reads the binder's `engine` in `default.go`,
  `installed_routine.go`, `intagg_expression.go` and
  `foreign_action_apply.go`, and its `op` in `cte_mutation_bind.go`,
  `insert_binding.go`, `insert_conflict.go` and `foreign_action_apply.go`.
- **The describe chain.** `describeWithFormals` calls `bindTypedColumns`,
  `resolveTableColumnType`, `resolveColumnCollation`, `resolveReference`,
  `catalog.Domain.Ref`, `describeInheritance`, `bindMutationCommand`,
  `bindViewWrite` and `captureEnumOperationInput` on the engine. The
  binder-side `*Engine` methods `bindMutationCommand`, `bindInsertInput`,
  `bindInsertDefaults`, `bindInsertReturning`, `bindInsertConflict`,
  `bindInsertConflictUpdate`, `bindInsertSource`, `bindMerge`, `bindUpdate`,
  `bindUpdateAssignments`, `prepareUpdateAssignments`,
  `bindUpdateAssignmentSource`, `bindTypedColumns`, `describeInheritance` and
  `indexExpressionBinder` use the engine only to call each other and a few
  services. They are ported in place to functions that reach the services
  through the binder's host; `bindInsertDefaults` takes the `*catalog.Owner`
  instead (its only engine use). `indexExpressionBinder` stays an engine
  constructor behind the host (see the 12c result).
- **Indirect paths, left for 12d.** `AuthorizationView` (it holds `engine` and
  `session`; the binder keeps `query.auth` and passes it to every resolution
  service); the captured resolver inputs (`capturedRegtypeInput`,
  `capturedOperatorInput`, `capturedRoutineInput`, `capturedFTSInput` hold an
  `*Operation`, `capturedRegTSInput` an `AuthorizationView`);
  `StatementDescription` (→ `preparedAnalysis` → `catalogChanges`);
  `binding.rule` (→ `ruleCTECommand` → `AuthorizationView`);
  `installedExtensionRoutine` (holds the binder).
- **Constant evaluation.** Only `foldRegtypeConstant` (a scope-free context
  with `Context` and `Input`) and `decodeLiteralInput` (a domain literal under
  the scope's locked input) build an `EvalContext`. `analyze` imports `exec`
  only for scope-free constant folding, as §3 L8 allows; domain-constrained
  literal decoding goes through the host. Preflight stays in the engine: it
  is statement preparation, reaches routine admission and is never called by
  the binder.
- **Host adapter.** Binders also run without a `StatementScope` (DDL,
  defaults, checks, index expressions, generated columns, Restore), always
  with an engine and usually with an operation. So the adapter is
  `bindingHost{engine, op}`, not `statementHost` (a deviation from §7
  decision 8, like 11b's preparation value). A nil `op` keeps today's nil-op
  semantics. Every host method is called with `Engine.mu` held and never
  takes or releases it.
- **`analyzeHost` methods (35 planned, 37 built; see the 12c result).**

  | Group | Methods | Today |
  |---|---|---|
  | Authority | `Authorization`, `Definition`, `AuthorizationStorage`, `AdmittedReference`, `ResolveReference` | `authorizationLocked(op)`, `op.definition`, `authorizationStorageLocked`, `admittedReference`, `resolveReference` |
  | Catalog | `CatalogOwner`, `CurrentCatalog` | `catalog.owner()` (and `Domain.Ref`), `catalog.read(catalog.state)` |
  | Names | `LookupNamespaces`, `LookupNamespacesSnapshot`, `LookupPath`, `ReferencePath`, `Regclass`, `LookupCollation`, `LookupIndexClass`, `NamedType` | the same-named engine methods |
  | Types | `ResolveType`, `ResolveAnnotationType`, `ResolveStringAnnotationType`, `ResolveAnnotationTypeInput`, `ResolveTableColumnType`, `ResolveColumnCollation` | the same, which take `op` |
  | Captures | `CaptureElementInput`, `CaptureOperatorInput`, `CaptureRegtypeInput`, `CaptureTypeDiagnosticInput`, `CaptureTypeDiagnosticName`, `CaptureEnumOperationInput`, `CaptureEnumOperationSafety`, `FTSInput` | the `capture*` services, `captureEnumOperationSafety(op, …)`, `capturedFTSInput{op}` |
  | Session | `PlanOwner` | `op.session.token`, `discardPlansGeneration` |
  | Effects | `EmitWarning`, `ReplayInsertAuthority`, `ReplayDomainCell` | `op.emitNotice`, `op.replayOrZero()`, `capturedDomainReplayCell(op, …)` |
  | Upward | `DecodeDomainLiteral`, `BindViewWrite` | the scope half of `decodeLiteralInput`; `bindViewWrite` (view-write lowering, postponed `view`) |

- **Entry points.** `describe`, `describeWithRule` and `describeWithFormals`
  become functions taking the host; `describeScoped` stays an engine method
  that builds the host from its scope. `newExpressionBinder(ctx, e, input,
  op, expected)` keeps its signature as the root construction point.
- **12d area order** stays as below, with two refinements: the first session
  (binding types) also settles how `analyze` represents the authority, since
  `queryScopeState.auth` and `binding.rule` move there; area 5 also takes the
  statement-kind binders ported in place here (`bindUpdate*`, `bindMerge`,
  the INSERT source, conflict and RETURNING binders, `bindTypedColumns`,
  `describeInheritance`, `indexExpressionBinder`), split out of their mixed
  DML and DDL files.

**12c result (done).** Built at `12d7ef8b9`. The binder reaches engine,
session and operation state only through `analyzeHost`
(`analysis_host.go`); `expressionBinder` has no `engine` or `op` field.

- **Host methods (37).** The 35 of the refine plus two found while porting:
  `EmitNotice` (`describeInheritance` emits NOTICE-level merge messages) and
  `IndexExpressionBinder`. The latter is a deviation: the ON CONFLICT
  inference binder was built from `shared.op.Context()`, which follows COPY
  input and can differ from the binder's `ctx`, so `indexExpressionBinder`
  stays an engine constructor (it also serves index DDL) and the host builds
  it under the operation's current context.
- **Adapter.** `bindingHost{engine, op}`. A session operation carries its own
  host value (`Operation.analysis`, set once at creation) and the engine one
  for binding without an operation (`Engine.analysis`; the builtin domain
  CHECK seeds at startup), so `Engine.analysisHost(op)` returns a pointer and
  binding allocates no host; a copied operation gets a fresh host. A nil operation keeps the old nil-op behavior: no definition
  reader, notices dropped, no plan owner (`PlanOwner` not ok) and no Restore
  replay.
- **Entry points.** `describe`, `describeWithRule` and `describeWithFormals`
  are functions taking `(ctx, host, ...)`; the operation parameter is gone.
  `describeScoped` stays an engine method. `newExpressionBinder(ctx, e,
  input, op, expected)` is the root wrapper over `newHostedBinder(ctx, host,
  input, expected)`; enum-safety capture now writes into the binder's own
  input (passing a parameter's address to a host method made it escape).
- **Ported in place.** `bindMutationCommand`, `bindInsertInput`,
  `bindInsertReturning`, `bindInsertConflict`, `bindInsertConflictUpdate`,
  `bindInsertSource`, `bindMerge`, `bindUpdate`, `bindUpdateAssignments`,
  `prepareUpdateAssignments`, `bindUpdateAssignmentSource` are functions
  using the binder's host; `bindTypedColumns` and `describeInheritance` take
  the host; `bindInsertDefaults` takes the `*catalog.Owner`.

### 12d — Move, area by area (superseded; see the refine below)

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
   `aggregate_modifiers.go`, `aggregate_dispatch.go`,
   `grouping_sets_bind.go`, `window_bind.go`, `window_frame_bind.go`.
5. DML targets and describe: `cte_mutation_bind.go`, `insert_binding.go`,
   `returning.go` binders, `describe`.

### 12d — Move (broad; several sessions)

The original plan moved the binder area by area. Go allows no methods on an
imported type, so `expressionBinder` and all of its methods (94 files, about
24k lines) must move in one commit. The refine below replaces the area moves
with in-place preparation and one final move.

**12d refine (approved; measured at `062d21ac7`).** The `reach` and
`frontier` tools, rooted at `expressionBinder`, `binding` and the group
types (outputs in `.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-BINDING-TYPES/`):

- **Where the binder is still dirty.** With the host cut, every binder method
  still reaches a god struct through a few types: `AuthorizationView` (holds
  the engine, the session and the settings), `StatementDescription` (the
  `expected` field, through `preparedAnalysis`), the captured
  `capturedRegtypeInput`/`capturedOperatorInput` (they call
  `Operation.emitNotice` and `scopeOf`), `binding.rule` (through
  `ruleCTECommand`) and `binding.viewWrite`.
- **The binding type closure.** Moving only type declarations (methods that
  evaluate or reach engine state become engine functions) gives 32 types,
  226 member keys and about 4.5k production and 2.0k test references.
- **Insert conflict state.** `boundInsertConflict` mixes the bound clause with
  per-execution state (arbiters, catalog checkpoints, `insertWriteOverlay`,
  index orderings, routine admission programmes). Moving it would pull index
  and routine admission runtime into `analyze`.
- **Feature packages.** `preparedCaptures` names `sequence.Constant`, and the
  binder calls `routine` (15 symbols), `extension` (17), `typedef` (5) and
  `sequence` (7). Those packages import only L1–L5 packages today.

Decisions:

1. **Sessions.** 12d.1 binding types; 12d.2 binder seam cuts in place
   (authority, `StatementDescription` inputs, captured inputs, the host
   signatures that still name engine types); 12d.3–12d.5 per-area preparation
   in place (expression core; query, FROM and CTE; aggregates, windows and DML
   targets): split mixed files so every file is wholly binder or wholly
   engine, cut the remaining engine edges, export what the engine reads, and
   move clean non-method helpers; 12d.6 moves the binder, its group types,
   the host interface and the describe entry points in one scripted session,
   as 11c did for `exec`. The area sessions touch mostly disjoint files and
   may run in parallel after 12d.2.
2. **Authority (built in 12d.2).** `analyze` declares `Authority`, an
   interface with an exported marker method and `Catalog() catalog.Read` (the
   only part of the authority the binder reads itself). The binder stores
   `*AuthorizationView` behind it (a pointer in an interface does not
   allocate); host methods take `analyze.Authority`, and the engine recovers
   the view with one checked helper. The binder's direct `definition*` and
   `session` reads become host methods.
3. **Opaque slots** (§3 rule 7, as `programPhases` and `viewOf`): the rule CTE
   commands of `binding.rule`, `binding.viewWrite` and the INSERT conflict
   clause are held as `analyze` slot interfaces with an exported marker
   method; the engine recovers each with one checked helper. A nil pointer is
   never stored, so a nil test keeps its meaning. The DML-targets session
   splits `boundInsertConflict` into its binding and runtime halves.
4. **Layering.** `analyze` may import the Part A content of the feature
   packages (`routine`, `typedef`, `extension`, `sequence`), which imports
   only L1–L5. Part B handlers never import `analyze`; they reach binding
   through the root host.
5. **`ParameterInfo`** moves with `binding` behind a root alias; 12e settles
   whether the driver imports `analyze` directly (§7 decision 11).
6. **Stays until 12d.6:** `expressionBinder`, `binder_groups.go` (only the
   binder uses its types, and `queryScopeState` needs decision 2) and
   `newExpressionBinder`.

#### 12d.1 — Binding types (broad)

Slots first, then the engine-side methods of the moving types become
functions, the members the engine uses are exported in place, and the types
move with aliases. Moving types: `binding` and its payloads (INSERT, DELETE,
CREATE TABLE, UPDATE, MERGE and its arms, RETURNING), the capture groups,
`ParameterInfo`, and the records they hold (composite occurrences, defaults,
regtype and temporal constants, CREATE TABLE declarations, LIKE and
inheritance inputs, unique constraints, update assignment groups, view
columns, namespaces and literal paths).

#### 12d.2 — Binder seam cuts (in place)

Decisions 2 and the captured inputs: host methods take `analyze.Authority`;
`definitionReader`, `namespaceSelection`, `lookupKind` and the captured
regtype/operator inputs reach the binder as `analyze` or `sqltype` types; the
binder reads its prepared inputs from an `analyze` value instead of
`*StatementDescription`.

#### 12d.3–12d.5 — Area preparation (in place)

Areas: (3) expression core: `expression.go` binder methods, `function*.go`,
`operator_binding.go`, `operator_routine_binding.go`, `array_comparison*.go`,
`expression_like.go`, `expression_row_comparison.go`,
`composite_expression.go`, `composite_capture.go`, `enum_function.go`, the
`range_*` binders, `regtype_prepared.go`; (4) query, FROM and CTE binding:
the `query_*_bind.go` files, `query_cte_*.go`, `query_rewrite.go`,
`query_target_compare.go`, `query_name_hint.go`, `query_values_view.go`,
`query_expression_walk.go`, `string_table_bind.go`, `string_set_lower.go`;
(5) aggregates, grouping, windows and DML targets: `aggregate_*.go`,
`grouping_sets_bind.go`, `window_bind.go`, `window_frame_bind.go`,
`cte_mutation_bind.go`, `insert_binding.go`, the `returning.go` binders, the
statement-kind binders ported in 12c and `describe`.

#### 12d.6 — Move the binder (broad)

Re-measure the frontier, move every binder file with the group types and
`analyzeHost` (as `analyze.Host`), leave aliases, and keep `newExpressionBinder`
and `Engine.analysisHost` in the root.

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
- [x] 12b — `expressionBinder` fields grouped (`b1dc47647`, `7b8cd2e23`,
  `449bd93c3`, `807bccc88`, `fdd30aa67`, then the docs commit). No tests
  moved or deleted; 160 engine test files adapted mechanically by the tool.
  Targeted engine units, the selected reference tests in quick and real 18.6
  modes (only the known `/postgres` oracle drift in DELETE RETURNING
  boundaries and MERGE repeatable read failed, as on `master`) and
  quickcheck passed; racecheck and finalcheck skipped at user request.
  Startup bench unchanged (5,100 allocs/op).
- [x] 12c — Refined; host seam in place (refine `4c5fdc26c`; `fcd02ccd9`,
  `befafa13a`, `30378dd42`, `9aed67fdb`, `bc537ad6e`, then the docs commit).
  Added `analysis_host_test.go` (nil-op semantics, host reuse, constructor
  wiring). Adapted mechanically, no assertion changed: 55 engine test files
  call `describe(ctx, e.analysisHost(op), ...)`; 21 read the binder's
  operation or engine through the test helpers `binderOperation` and
  `binderEngine`; five binder literals key `host` instead of `engine`;
  `enum_retained_test.go` keeps its deliberate nil engine as `(*Engine)(nil)`.
  No test moved or deleted. Targeted engine units, the selected reference
  tests in quick and real 18.6 modes (only the known `/postgres` oracle drift
  in DELETE RETURNING boundaries and MERGE repeatable read failed, as on
  `master`), quickcheck and `make deps` passed; racecheck and finalcheck
  skipped at user request. Startup bench: 855–1,221 µs/op and 5,014
  allocs/op after (master 870–1,116 µs/op, 5,014 allocs; timing is noisy on
  both); a throwaway probe of four statements showed identical allocations
  per execution before and after.
- [ ] 12d — `analyze` created and binding moved (sessions: 12d.1 binding
  types, 12d.2 seam cuts, 12d.3–12d.5 area preparation, 12d.6 binder move)
  - [x] 12d refine (docs only; the commit that records this text)
  - [ ] 12d.1 — binding types
  - [ ] 12d.2 — binder seam cuts
  - [ ] 12d.3 — expression core preparation
  - [ ] 12d.4 — query, FROM and CTE preparation
  - [ ] 12d.5 — aggregates, windows and DML targets preparation
  - [ ] 12d.6 — binder moved
- [ ] 12e — Aliases removed

Outstanding aliases: none yet.

Notes for later steps:

- 12c: the host seam replaces only `engine` and `op`; `ctx`, `expected`,
  `input` and `dependencies` stay as statement inputs and output. The
  groups need no host: no group type holds `*Engine` or `*Operation`.
- 12d: host signatures still name engine types that `analyze` cannot import
  as they are: `AuthorizationView` (nearly every method), `definitionReader`,
  `namespaceSelection`, `lookupKind`, `capturedOperatorInput`,
  `capturedRegtypeInput`. The first 12d session
  settles the authority representation; the captured inputs can be returned
  as their `sqltype` interfaces where the binder only stores them.
- 12d: `newHostedBinder` is the analyze-side constructor; the root keeps
  `newExpressionBinder` and `Engine.analysisHost`. `IndexExpressionBinder`
  returns an `*expressionBinder`, so the root must name the moved type.
- 12d area 5: the statement-kind binders ported here sit in mixed files
  (`update*.go`, `merge.go`, `insert_*.go`, `typed_table.go`,
  `table_inheritance.go`); split their binder halves out first.
- 12d: `binder_groups.go` and `binding_groups.go` move together in the first
  session (binding types). The `binderfields` tool (12b) and `bindsplit`
  (12a) rewrite selectors and literals by field object and are the model for
  export renames.

- `access` B5 moves the binding walkers once 12d has moved `binding`.
- Step 15 (`typedef`): the composite, enum and range binders move here, not
  with `typedef`.
- Step 14 (`routine`): function and operator call resolution moves here.
