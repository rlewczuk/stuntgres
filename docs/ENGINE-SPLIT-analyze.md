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
   only part of the authority the binder reads itself; built as
   `ReadCatalog()`, see the 12d.2 result). The binder stores
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

Area (3b), added by 12d.3: the expression-side binder files that no area
listed, prepared the same way before 12d.6: `array_assignment.go`,
`array_expression.go`, `array_operator.go`, `bitwise.go`, `conditional.go`,
`logical.go`, `expression_predicate.go`, `expression_regexp*.go`,
`string_expression.go`, `string_insert.go`, `string_window_lower.go`,
`subscripts_expression.go`, `series_expression.go`, `sequence_expression.go`,
`size_expression.go`, `sqljson_*.go`, `regoperator_prepared.go`,
`regproc_prepared.go`, `regts_prepared.go`, `enum_diagnostics.go`,
`domain_input.go`, `network_format.go`, `jsonb_record_columns.go`,
`intagg_*.go`, `row_comparison_interpretation.go`, `collation_names.go`,
`sql_routine_*.go`, `catalog_relations.go`, `extension_paths.go`,
`installed_routine.go`, `version_information.go`, `routine_presentation.go`
and the binder halves of `definition_reader.go`, `schema_path.go`,
`default.go`, `view_literals.go` and `rule_*.go` (re-measure: some may be
mixed DDL files).

#### 12d.6 — Move the binder (broad)

Re-measure the frontier, move every binder file with the group types and
`analyzeHost` (as `analyze.Host`), leave aliases, and keep `newExpressionBinder`
and `Engine.analysisHost` in the root.

#### 12d.1 result

Built at `062d21ac7`. Tools and scripts:
`.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-BINDING-TYPES/` (new `slotwrap`
go/types tool; `fixpoint*.sh`; `c4.sh`; `movespec` with a `-qual` flag; the
11c `frontier`, `expspec`, `poslit`, `meth2fn`, the 10c `movex`/`movedecl`
and the 10d `keylit`).

- **Slots** (`5173b29cd`): `binding.viewWrite`, `insertBinding.conflict` and
  `ruleBinding.ctes` became `ViewWriteSlot`, `InsertConflictSlot` and
  `RuleCTEsSlot` (the whole CTE command map is one slot, the engine type
  `ruleCTECommands`). `slotwrap` rewrote every use by context: 198 reads to
  `viewWriteOf`/`insertConflictOf`/`ruleCTEsOf`, 22 stores to the nil-safe
  `...SlotOf`, and left 49 nil tests and the field-to-field copies alone;
  four sites were fixed by hand (two nested store-of-read, one tuple
  assignment, one read in a region that did not type-check during the pass).
- **Functions** (`70d43f3e9`): `physicalBinding`, `bindingDefaultOrigin`,
  `evalBindingDefault`, `applyDefaultOrigin`, `capturedDomainReplayCell`,
  `mergeHasVirtualReads`, `selectMergeArm`, `validateCompositeOccurrence`
  and `remapLikeExpression` (`meth2fn`; the pointer-receiver call sites of
  `defaultOriginAt` by hand). Payload-only methods moved with their types:
  `CaptureComposite`, `CaptureViewComposite`, `CaptureRoutineReference`,
  `CTEMutationPayload` and the `LikeInput` helpers.
- **Export in place** (`f630be9b9`): the moving declarations were gathered
  into four `analyzemv_*.go` files plus `binding_groups.go`; the frontier of
  that set is closed (no outbound engine reference). `expspec` exported 230
  keys (fields, methods and the constants of `LikeMask`,
  `CompositeOccurrenceKind` and `ViewLiteralClause`) with overrides `CTEs`,
  `FTS` and `ACLItem`; 6,035 references, per-object census identical before
  and after.
- **Move** (`5ed818cdf`): `analyze` holds `binding.go`, `binding_groups.go`,
  `payloads.go`, `create_table.go`, `captures.go` and `doc.go`. Type names:
  `Binding`, `InsertBinding`, `DeleteBinding`, `CreateTableBinding`,
  `UpdateBinding`, `AssignmentGroup`, `MergeBinding`, `MergeArm`,
  `Returning`, `ColumnDeclaration`, `InheritanceInput`, `LikeInput`,
  `LikeMask`, `UniqueConstraint`, `ViewCapture`, `PreparedCaptures`,
  `ResolverInputs`, `RuleBinding`, `ParameterInfo`, `CompositeOccurrence`,
  `CompositeOccurrenceKind`, `PreparedCompositeKey`, `DefaultOrigin`,
  `RegtypeConstant`, `RegObjectReference`, `TemporalConstant`, `ViewColumn`,
  `ViewNamespace`, `ViewNamespaceColumn`, `ViewNamespaceQualifier`,
  `ViewLiteralPath`, `ViewLiteralClause` and the three slots (the plan's
  `LiteralPath`/`LiteralClause` kept their `View` prefix to match the
  `View*Literal` constants). 32 aliases in `analyze_aliases.go`; the 32
  constants and `NewResolverInputs` are qualified in 61 engine files; two
  positional literals keyed (`keylit`). The test local `analyze` in
  `insert_conflict_update_test.go` became `analyzeConflict`, so `analyze`
  always means the package. `analyze` imports `plan`, `exec`, `catalog`,
  `storage`, `expr`, `functions`, `ids`, `sequence`, `parser`, `sqltype` and
  the standard library.
- **Tests.** New: `TestBindingSlotsKeepNilAbsent` (`binding_slots_test.go`:
  nil values stay nil slots, stored pointers and empty maps come back). No
  test moved or deleted; engine tests changed only mechanically (field
  renames, slot helpers in literals and reads, the `analyzeConflict` local).
- **Verification.** `make build`, `go vet` (identical to the base) and
  targeted engine selections after every commit; `make quickcheck` passed on
  `5ed818cdf` (inside a 48 GB memory cap). `REFERENCE=1` for the step 12
  selection (INSERT, defaults, identity, UPDATE, DELETE, MERGE, inheritance,
  LIKE, typed tables, rules, views, RLS, prepared statements, CTE DML,
  windows, aggregates, grouping sets, Dump/Restore, composites, regtype and
  regoperator) passed except the known `/postgres` oracle drift
  (`TestReferenceDeleteReturningBoundaries`, the four MERGE repeatable-read
  tests and `TestReferenceCompositeExpr(Correlated)Returning`), which fail
  the same way on master. `make bench`, branch and master interleaved:
  835–903 µs/op against 849–958 µs/op, 5,014 allocs/op on both (warm-up runs
  near 1 ms on both). `make deps`: module paths only. No engine subpackage
  imports `analyze`. `AGENTS.md`: 100 lines before and after (`analyze`
  joined the shared subpackage bullet). racecheck/finalcheck skipped at user
  request.

#### 12d.2 result

Built at `fa25fc96c`. Tools and scripts:
`.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-SEAM-CUTS/` (new `ifacern`, which renames
interface methods together with every implementation; new `bindernames`,
which lists the names binder functions use; `fixcatalog.py`, which rewrote
`.Catalog` on an `Authority` from compile-error positions), plus `fieldrn`
and `unalias` (10d) and the 11a `reach`. `gopls` v0.21 cannot load the engine
tests (Go 1.26 `new(expr)`), so the renames used the go/types tools.

- **Definition reader** (`e0d90806f`): `definitionReader` became
  `catalog.DefinitionReader`, not an `analyze` type: it needs only `catalog`
  and `ids`, and root DDL and the Part B packages read through it.
  `ifacern` exported its five methods on the interface and its five
  implementations (stage, inheritance, prepared drop, identity drop, the
  staged-lookup test reader), 83 sites; `unalias` qualified 39 uses.
- **Lookup vocabulary** (`3ed685006`): `analyze.LookupKind` with seven
  exported constants (121 uses in 54 files qualified) and
  `analyze.NamespaceSelection{Path, Qualified}` with `Count` and `At`
  (`fieldrn`, tests included). Aliases `lookupKind` and `namespaceSelection`.
- **Captured inputs** (`f718185df`): `analyze.IdentifierTarget` and the
  `RegtypeRetarget`, `OperatorRetarget`, `RoutineRetarget` and
  `RegTSRetarget` interfaces (`captured_inputs.go`); the engine types
  implement them in `captured_input_target.go`, each setting only the fields
  its sites set (a nil sink keeps the captured one; text search has no
  position). The routine and text-search inputs joined the planned regtype
  and operator ones: the same files and pattern. The host's
  `CaptureRegtypeInput` and `CaptureOperatorInput` became
  `ResolveRegtypeText` and `ResolveOperatorText`, which resolve under a fresh
  capture and return the sinks by value (capture errors raw, resolution
  errors positioned as before), so nothing is boxed.
- **Held inputs** (`48ea9b211`): `analyze.Expected` (`expected.go`), held by
  value in the binder; `expectedOf` converts once in the root wrappers
  (`newExpressionBinder`, `describeWithRule`, `describeScoped`,
  `bindSQLRoutineLocked`). `describeWithFormals` and `newHostedBinder` take
  it.
- **Authority** (`9115279bd`): `analyze.Authority` is a marker and
  `ReadCatalog()`: `AuthorizationView` embeds `access.View`, whose `Catalog`
  field a `Catalog()` method would hide (about 1,400 root selectors).
  `analysis_authority.go` holds the pointer-receiver markers, the root type
  `bindingAuthorityView` (a binding authority: the stored view read through
  the operation's definition reader), `authorityOf`, the strict
  `authorizationOf` and the host's private `view`. `BindingAuthority`
  replaces `Authorization` and converts the stored pointer instead of copying
  the view; without a stored authority it boxes a fresh one (setting calls:
  one allocation per statement in the probe). `BindingDefinition` replaces
  `Definition`. New host methods: `DefinitionOf`, `CheckPrivilege`,
  `CanUse`, `StagedTemporary`, `Roles`, `DefinitionStamp` (43 in all). The
  statement-kind binders and `describeWithFormals` take an `Authority`;
  `bindInsertDefaults` takes `catalog.Read` and `sqlCallableShape` the
  catalog and reader. The binder's definition reads go through
  `authorityDescriptor`, `authorityRelation`, `authorityNamespace` and
  `authorityType` over the new root helpers `definitionRelationAt`,
  `definitionNamespaceAt` and `definitionProceduresNamedAt`.
- **Signature check** (`b7e9c8bd7`, found by `reach`): `checkExpectedLifetime`
  built two `StatementDescription`s; the comparison now runs on a
  `statementSignature` value and `checkStatementSignature` wraps it.
- **Frontier.** `reach` (no god-type cut except `expressionBinder`): all 341
  binder methods are clean (all 337 were dirty at `062d21ac7`). Of the binder
  free functions only the root wrappers `describe` and `describeWithRule`
  reach `AuthorizationView`.
- **Tests.** New: `TestCapturedInputsRetargetOnlyTheirSinks`,
  `TestExpectedOfCopiesHeldInputs`,
  `TestBindingAuthorityOverlaysOperationDefinition`. Adapted mechanically:
  four tests that assign `b.expected` use `expectedOf`
  (`between_temporal_capture_test.go`, `jsonb_record_expression_test.go`,
  `conversion_lookup_test.go`); `TestJSONBRecordArgumentIdentity` bumps the
  binder's held epoch instead of the description's (it relied on the shared
  pointer); two `sqlCallableShape` calls pass the catalog and reader;
  `TestAnalysisHostWithoutOperation` reads `BindingDefinition(nil)`. No test
  moved or deleted.
- **Verification.** `make build` and `go vet` (identical to the base) after
  every commit; targeted engine selections, `RACE=1` for the prepared tests;
  `make quickcheck` passed (48 GB memory cap). `REFERENCE=1` for the step 12
  selection plus regtype, regoperator, regproc, regclass, sequence, setting,
  NOT NULL, SQL function, search path, composite and enum tests: only the
  known `/postgres` drift failed (`TestReferenceDeleteReturningBoundaries`,
  the four MERGE repeatable-read tests,
  `TestReferenceCompositeExpr(Correlated)Returning`) plus
  `TestReferenceNotNullFormsAndAlter` and
  `TestReferenceNotNullCurrentCommandNamesAndCompatibleReuse` (PostgreSQL
  55000), which fail the same way on master. Allocation probe through the
  driver (direct and prepared SELECT with operators and functions, regtype,
  regoperator and regprocedure literals, regclass and sequence, enum
  comparison, CREATE TABLE … INHERITS): identical or one fewer allocation per
  execution; `set_config` and `current_setting` one more (accepted at
  planning). `make bench`, branch and master interleaved: 821–1,278 µs/op
  against 847–1,251 µs/op, 5,014 allocs/op and about 1.64 MB/op on both.
  `make deps`: module paths only. `AGENTS.md`: 100 lines before and after.
  racecheck/finalcheck skipped at user request.

#### 12d.3 result

Built at `372246284` in parallel with 12d.4 and 12d.5, rebased onto
12d.5 (`3225376b4`). Scope: the 18 files
listed for area 3; the unlisted expression files became area 3b (above).
Tools and scripts: `.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-EXPRESSION-CORE/`
(`c1.sh`–`c5.sh`; new `mvpkg.sh`, which gathers declarations with
`movedecl`, renames and qualifies them with `movex` and moves the file into
a lower package; new `impfix`, goimports with the module as local prefix;
the 11c `frontier`). Every area file is now wholly binder: its remaining
free functions and types are binder-only.

- **Split** (`429bf3989`): preflight with `foldedExpressionInfo` and
  `arithmeticSetConstantNull` → `expression_preflight.go`; the statement
  host's function hooks → `statement_host_function.go`;
  `Engine.captureElementInput` → `element_input_capture.go`;
  `newExpressionBinder` → `analysis_host.go`; the regtype analysis cell →
  `regtype_analysis.go`; `validateCompositeSource` →
  `view_composite_validation.go`; the comparison renderers →
  `deparse_renderer.go`.
- **Mappers to `plan`** (`a750bbf16`): `MapRow`, `MapRowAnalysis`,
  `MapRowComparisonExpansions`, `MapScalarArrayChildren`.
- **Shared helpers to `analyze`** (`4f491dace`): `CoercionProperties`,
  `DomainCoercionInput`, `RestoreDomainCastCollations`,
  `StorageDestination`, `StorageCoercionInfo`, `CoercionInfo`,
  `CollatedInfo`, `ComparisonOperator`, `InputErrorAt`,
  `RegtypeInputError`, `RecordTypeMapper` and the OVERLAPS inline cost model
  (`OverlapsInlineSafe`, and `OverlapsNodeCost`/`OverlapsTreeCost`, which
  `TestDatetimeOverlapsNullIfCostBoundary` pins). Deviation:
  `validateCompositeOccurrence` turned out engine-only (only view
  validation calls it) and joined `view_composite_validation.go`, so
  `record_shape.go` keeps `validExpressionUsage`.
- **Held signature** (`ff9c86427`): `analyze.Signature`, `CheckSignature`,
  `StatementCatalogType`, `SameStatementUsage`, `SameStatementResultUsage`,
  `RefreshableTemporal` and `Expected.CheckLifetime`, which replaces the
  root `checkExpectedLifetime` (this corrects the 12d.2 note that it stays
  in the root). It added a private `sqlError` to `analyze`; after the
  rebase onto 12d.5, which added its own in `statement_errors.go`, that one
  is kept.
- **Remaining edges** (`2ba59b6ba`): `catalog.ObjectNameText`; the builtin
  routine name and signature indexes join catalog's OID index
  (`BuiltinRoutineIdentitiesNamed` returns a copy, as
  `BuiltinRoutineIdentities` does; it runs only on the regproc bare-name and
  unavailable-function paths) and `routine.IdentityDefaults`; the reg*
  alias walkers, `AliasAddress` and `DescriptorAt` → `catalog`;
  `sequence.ValidateDefaultTemporaryProvider`; `plan.NullLiteral`;
  `analyze.OperatorSignature` (keyed literals). The whole-row demand walk
  maps through the new host method `MapBoundExpression` (the engine's retag
  walker with identity type and query mappers; 45 host methods with
  12d.5's `RelationHasRules`).
  `isSystemColumn` was left to 12d.5, which moves it as
  `catalog.IsSystemColumn`.
- **Left for later.** `sqlError` (12d.6 switches the binder to the analyze
  copy); edges into other areas' binder files (`query_*`, `window_bind.go`,
  `returning.go`, `string_table_bind.go`, `query_expression_walk.go`) and
  into area 3b files (`bitwise.go`, `conditional.go`, `array_operator.go`,
  `enum_diagnostics.go`, `sql_routine_syntax.go`,
  `expression_regexp_set.go`); exporting the binder's own members and its
  vocabulary types (`expressionPolicy`, `projectionContext`,
  `expressionContext`, `comparisonOperand`), deferred to 12d.6 at user
  request so the parallel area sessions do not conflict.
- **Tests.** New: `TestExpectedCheckLifetimeFencesCatalogTypes`
  (`analyze/signature_test.go`, the package's first test) and
  `TestAnalysisHostMapsBoundExpressions` (`analysis_host_map_test.go`); both
  failed before the seam existed. Adapted mechanically, no assertion
  changed: `datetime_overlaps_test.go`, `array_comparison_reduction_test.go`
  and the tests that call the renamed signature and temporal helpers
  (`date_range_capture_test.go` and `timestamp_range_capture_test.go` by
  `sed` only, as they are not gofmt-clean on master). No test moved or
  deleted.
- **Verification.** `make build`, `go vet` (identical to the base) and the
  targeted engine selection after every commit; `make quickcheck` passed (48
  GB memory cap). `REFERENCE=1` for the step 12 selection plus reg* types,
  composites, enums, arrays (ANY/ALL), LIKE/SIMILAR, row comparison, defaults,
  generated columns, domains, OVERLAPS, collations, casts, SQL functions,
  sequences, serial and temporary objects: only known `/postgres` drift failed
  (`TestReferenceDeleteReturningBoundaries`, the four MERGE repeatable-read
  tests, `TestReferenceCompositeExpr(Correlated)Returning`,
  `TestReferenceTemporaryTableDiagnostics` and the PostgreSQL-only
  `TestReferenceSerialDefaultProvider(PublicationHold|DropFirst)`), as on
  master; every stuntgres leaf passed. `make bench`, branch and master
  interleaved: 844–1,155 µs/op against 849–1,668 µs/op, 5,014 allocs/op and
  about 1.64 MB/op on both (warm-up runs 5,021–5,026; timing is noisy on the
  shared host). `make deps`: module paths only. No engine subpackage imports
  `analyze`; `analyze` now also imports `typedef` (Part A, decision 4).
  `AGENTS.md`: 100 lines before and after. racecheck/finalcheck skipped at
  user request.

#### 12d.5 result

Built at `372246284`, in parallel with 12d.3. Tools:
`.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-AGGREGATES-DML/` (the 15b `movedecl`, a
`goimports` build, frontier outputs and specs), plus the 11c `frontier`,
`expspec` and `movespec`, the 10c `movex` and the 10d `fieldrn`.

- **File split** (`949747b68`): every area-5 file is wholly binder or wholly
  engine. Binder halves went to `update_bind.go`, `merge_bind.go`,
  `insert_conflict_bind.go`, `insert_query_bind.go`, `typed_table_bind.go`,
  `table_inheritance_bind.go` and `describe_bind.go` (`describeWithFormals`
  out of `engine.go`; its CREATE TABLE branch became `bindCreateTable` in
  `create_table_bind.go`); engine halves to `update_assignment_eval.go`,
  `update_from_admission.go` (`checkTemporaryStorageAccess` and
  `queryTableRefsLocked` were engine-only), `merge_returning_output.go`,
  `returning_preflight.go` and `insert_conflict_update.go`. The aggregate,
  grouping-set and window files were already wholly binder.
- **ON CONFLICT split** (`804df42f5`): `analyze.InsertConflict` (with
  `ConflictKey` and `Clone`) is the bound clause and `InsertBinding.Conflict`
  points at it; `InsertConflictSlot`, `insertConflictOf`,
  `insertConflictSlotOf` and the `insertConflictSlot` alias are gone. The
  engine's `insertConflictState` refers to the clause and holds one command's
  arbiters, checkpoints, affected count, EXCLUDED frame and write overlay; it
  is built in `execute_bound.go`, which already handed the object to
  `StatementScope.insertConflict`. It shares the binding's clause, so
  predicate normalization and DO UPDATE preflight still write into it; rule
  paths `Clone` where they called `instantiate`. Field exports by `fieldrn`
  (16 fields, about 450 sites).
- **Shared helpers** (`1a329c118`): the CREATE TABLE column, NOT NULL, unique
  and LIKE helpers (`create_table_columns.go`) and the statement checks
  (`statement_checks.go`: view output, INSERT targets and value errors,
  generated-column and inheritance conflicts, `TableKindError`,
  `GuardedCatalogRelation`, `ValidateMergeReachability`) are used by both
  the area-5 binders and engine DDL and import only lower packages, so they
  moved to `analyze`, exported where the engine calls them and qualified at
  45 engine files (no aliases). `analyze` has its own `sqlError` and
  `insertError` (`statement_errors.go`). `isSystemColumn` became
  `catalog.IsSystemColumn`. `analyze` now also imports `pgcatalog` (L5) and
  the Part A `typedef`.
- **Seam** (`a73ebbc83`): `ViewWriteSlot` gained `Base`, the base relation of
  a view write, and the host gained `RelationHasRules`; `bindMutationCommand`
  no longer reads `viewWrite` or the rule payload.
- **Exports** (`363efc88e`): `describeWithFormals` became `Describe`, the one
  entry point behind `describe`, `describeWithRule`, `describeScoped` and the
  SQL routine definition path; `bindInsertDefaults` and the free
  `sameGroupExpression` became `BindInsertDefaults` and `SameGroupExpression`.
- **Frontier** after the session: the area-5 binder files reach only binder
  files, the host, `analyze` and these root declarations, all shared with
  other areas or deferred (see the notes): `sqlError`, `insertError`,
  `objectNameText`, `columnDefault` and `domainNullInputExpression`,
  `queryLockAdmission` (with `newQueryLockAdmission` and
  `admittedMutationTarget`), `mutationBinding`, `foldCTERestrictions`,
  `validateViewReadGraph`, `queryDemandProperties`, the `consumerContext`
  partition methods (`partition_bind.go`), and the area-4 files
  `query_name_hint.go` and `query_rewrite.go`.
- **Tests.** New: `TestInsertConflictCloneOwnsTheClauseHeader` (`analyze`),
  `TestViewWriteSlotReportsBaseRelation`,
  `TestAnalysisHostReportsRelationRules`. Adapted: the conflict tests build
  `insertConflictState` with `newInsertConflictState` and read execution
  state from the scope (`insert_conflict_*_test.go`, `insert_returning_test.go`);
  `TestRuleConflictInstantiation` and `TestRuleCTEConflictInstantiation`
  now assert that each composed command gets its own clause (an execution
  write through one command's state does not reach the recipe or the next
  command), since execution state no longer lives in the binding;
  `TestBindingSlotsKeepNilAbsent` lost its conflict-slot checks; rule test
  helpers return `*analyze.InsertConflict`. No test moved or deleted.
- **Verification.** `make build` and `go vet` (identical to the base) after
  every commit; targeted engine selections (INSERT ON CONFLICT and its rule
  and CTE paths, UPDATE, MERGE, RETURNING, CREATE TABLE LIKE/OF/INHERITS,
  grouping sets, windows, aggregates, slots and host); `make quickcheck`
  passed (48 GB memory cap). The step 12 reference selection plus CREATE
  TABLE, generated columns, typed tables and grouping sets passed in quick
  mode; with `REFERENCE=1` only the known `/postgres` drift failed
  (`TestReferenceDeleteReturningBoundaries` and the four MERGE
  repeatable-read tests), as on master. `make bench`, branch and master
  interleaved under load: 912–1,021 µs/op against 856–1,136 µs/op, 5,014
  allocs/op on both. `make deps`: module paths only; no engine subpackage
  imports the root. `AGENTS.md`: 100 lines before and after.
  racecheck/finalcheck skipped at user request.

#### 12d.4 result

Built at `372246284` in parallel with 12d.3 and 12d.5, then replayed onto
`3225376b4` (after 12d.5) and `2dcb67687` (after 12d.3) with `replay.sh`,
which re-creates each commit by re-running its tools and edits. Tools
and scripts: `.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-QUERY-PREP/` (`mv.sh`
gathers a staging file `analyzemv_<stage>.go`, checks that its frontier has
no outbound engine reference, exports the inbound members in place with
`expspec`/`movex`, moves the file into `analyze` with aliases for its types
and qualified functions, strips the package's own qualifier, then runs
`goimports`, `keylit` and `gofmt`), with the 11c `frontier`, `expspec` and
`movespec`, the 10c `movex`/`movedecl` and the 10d `keylit`. Scope decided
at planning: engine helpers that area 4 shares with the other areas stay
for 12d.6, members the root's tests read are exported too, and the binder
group fields stay for 12d.6.

- **Split files** (`d350f8a03`): `preflightQuerySet` (execution-time) moved
  to `query_set_preflight.go`, `cteStoredViewVolatile` to
  `query_cte_volatility.go`, `captureValuesViewLiteral` to
  `query_values_bind.go`; the `queryScopeState` methods
  `freezeViewColumns` (from `view.go`) and `partialIndexCandidate` (with
  `partialPredicateImpliedBy`/`Atom`, from the index predicate files) to
  binder files. `query_cte_view.go` and `query_values_view.go` are wholly
  view rendering; `query_join_legality.go` is execution-time and stays.
- **Row-lock admission** (`c6475646c`): `analyze/lock_admission.go`
  (`LockAdmission`, `NewLockAdmission`, `PreparedSource`, `MutationTarget`,
  `RowLockClause`, `RowLockDiagnostic`, `RowLockMode`, `RowLockTargets`,
  `RootRowMarks`, `FirstSetRowClause`, `FutureCTEError`); the closure had no
  engine reference. Aliases `queryLockAdmission`, `preparedSource`,
  `admittedMutationTarget`. `analyze` now imports `lock`.
- **Stored views** (`20e14024e`): host methods `StoredViewVolatile` and
  `CaptureRetainedViewInput` replace the binder's reads of `viewDefinition`;
  `captureRetainedDefinitionInput` moved to `view_retained_input.go`. The
  `View*Namespace` node kinds, `FrozenNamespaceNode` and `ViewFromRoot`
  moved to `analyze/view_namespace.go`.
- **Query namespaces** (`ee4b1ac07`): `QueryColumn`, `QueryQualifier`,
  `QueryNamespace` (fields exported) and `QueryTargetLeaf` in
  `analyze/namespace.go`; 703 references. Aliases
  `queryColumn`, `queryQualifier`, `queryNamespace`. The rebase, combine and
  USING helpers stay with the binder.
- **Shared helpers** (`36bdb885a`): `analyze/query_walk.go`,
  `query_sets.go` and `query_diagnostics.go` (see the README). Four engine
  files became empty and were removed (`query_rewrite.go`, `query_cycle.go`,
  `query_name_hint.go`, `cte_mutation_payload.go`). `SetOrderCollationError`
  uses the `analyze` `sqlError` that 12d.5 added. The `sourceOutName` wrapper
  is gone (callers use `expr.SourceOutName`). This also settles the area-4
  edges 12d.5 deferred: `queryLockAdmission`, `mutationBinding`,
  `queryDemandProperties`, `query_name_hint.go` and `query_rewrite.go`.
- **Exports in place** (`792c18427`): 18 binder methods the root's tests or
  rule finalization call (`BindSelect`, `BindFrom`, `FrameID`,
  `FinishOrder`, `FinishDistinctOn`, `ScheduleQueryProjection`,
  `LowerQuerySets`, ...), `queryScopeState.PartialIndexCandidate`,
  `PartialPredicateImpliedBy`, `PushCTEOccurrence`,
  `RebaseFunctionCorrelation` and `QueryTargetComparison` with the members
  its test reads; 395 references in 103 files. Members read only by the
  statement-kind binders and `Describe` (`assignOutput`,
  `bindCTEDefinitions`, `foldCTERestrictions`, `activateCTESources`,
  `combineQueryNamespace`, `rebaseQueryNamespace`) stay unexported: they
  move with the binder.
- **Host walker** (`86957752b`): `MapQuerySetStage` uses the host's
  `MapBoundExpression` (added by 12d.3), which is the same call shape.
- **Frontier after.** Area-4 declarations reach in the engine only host
  methods, the aliases, and the shared helpers left for 12d.6: `sqlError`
  and `insertError` (`analyze` has its own), `mapBoundExpression` (CTE
  restriction folding and `RebaseFunctionCorrelation` pass a query mapper,
  which the host's `MapBoundExpression` does not take) and `pgType`.
- **Tests.** New: `TestAnalysisHostReadsStoredViews`. Adapted mechanically:
  renames and qualifications in 85 engine test files (`json_elements_expression_test.go`
  calls `expr.SourceOutName`). No test moved or deleted.
- **Verification.** `make build` and `go vet` (identical to the base) after
  every commit; targeted engine selections (CTE, recursive, SEARCH/CYCLE,
  set operations, subqueries, DISTINCT ON, row marks and lock admission,
  prepared rebind, views, rules, policies, partial indexes, string-set
  lowering, target comparison); `make quickcheck` passed before the replays
  and on the final branch (48 GB memory cap). `REFERENCE=1` for the query, row-lock,
  partial index, generated, composite, view, rule, prepared, CTE, policy,
  INSERT SELECT, UPDATE FROM, MERGE and DELETE tests, before the replays and
  on the final branch: only the known `/postgres` drift failed
  (`TestReferenceDeleteReturningBoundaries`, the four MERGE repeatable-read
  tests, `TestReferenceCompositeExpr(Correlated)Returning`,
  `TestReferenceQueryFromValuesPhysicalReplacement`); no `/stuntgres`
  subtest failed. `make bench`, branch and master interleaved under other
  sessions' load: 833–1,246 µs/op against 829–1,155 µs/op, 5,014 allocs/op
  and about 1.64 MB/op on both. `make deps`: module paths only; no engine
  subpackage imports the root. `AGENTS.md`: 100 lines before and after.
  racecheck/finalcheck skipped at user request.

#### 12d.3b result

Built at `f049d2fe8`. Scope: the area-3b list above, re-measured with
`frontier` (all of its binder-side edges were already host calls or binder
files except nine helpers; the rest of the list's engine edges came from
engine declarations in mixed files). Tools and scripts:
`.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-EXPRESSION-REST/` (`c1.sh`–`c3.sh`,
`c3-docs.py`, the `c2-*.spec`/`c3-*.spec` rows), with the 12d.3 `movedecl`,
`mvpkg.sh` and `verify.sh` and the 12d.4 `frontier`. Every area-3b file is
now wholly binder or wholly engine.

- **Split** (`8186d9690`): engine halves to `array_assignment_preflight.go`
  (assignment-target preflight), `array_rendering.go` (`scalarRenderer`
  array and subscript SQL), `bitwise_preflight.go`, `collation_display.go`,
  `domain_input_scope.go` (`DecodeLockedDomainInput`, `domainScope`),
  `enum_diagnostic_capture.go` (the engine's type-diagnostic and enum
  capture), `statement_host_extension.go` (`OpenExtensionPaths`),
  `regclass_capture.go` (regclass resolution and capture) and
  `view_literal_validation.go` (`viewDefinition.validate*`); binder halves to
  `default_bind.go` (`bindDefault`), `definition_reader_bind.go`,
  `schema_path_bind.go` (`builtinVisible`), `rule_column_bind.go`,
  `intagg_reference_bind.go` and `sql_routine_reference_bind.go`.
  `default.go`, `definition_reader.go`, `schema_path.go`,
  `rule_definition.go`, `intagg_presentation.go` and
  `routine_presentation.go` are now engine files; `bindCreateDefaults` stays
  with them (DDL that drives a binder). `selectedBitwiseName` had no callers
  and was removed.
- **Mappers to `plan`** (`f18840612`): `MapSubscript` and `SQLJSONSource`.
- **Shared helpers down** (`e1f80935c`), qualified at their callers with no
  aliases (245 references in 84 files, 30 of them tests): `catalog`
  `DefinitionTypeAt`, `DefinitionArrayAt`, `DefinitionNamespaceAt`,
  `DefinitionRelationAt`, `DefinitionLookupAt`,
  `DefinitionProceduresNamedAt` and `IndexedDefinitionProcedures`
  (`definition_reads.go`); `analyze` `ClosedPgcryptoDefault`,
  `ClosedSequenceDefault`, `ClosedTransactionTimeDefault`,
  `ClosedUUIDDefault`, `ClosedSQLRoutineDefault`, `SQLRoutineDefaultClosure`,
  `CryptoExpressionSignature`, `ValidInstalledUUIDOsspShape` and
  `ValidInstalledText` (`default_closure.go`), `NeedsTypeDiagnosticInput` and
  `CaptureRetainedExpressionInput` (`retained_input.go`),
  `ArrayMembershipName`, `BindExtremaComparison` and
  `ValidateRegroleStoredLiteral` (`operator_names.go`),
  `ProcedureInNamespaces` (`procedure_lookup.go`) and `RuleNamespace` (fields
  `Relation`, `Ref`, `Event`) with `RulePseudoScope` (`rule_namespace.go`).
  `sql_routine_default.go`, `extension_expression.go` and
  `procedure_lookup.go` became empty and were removed. `analyze` now also
  imports the Part A `extension` (decision 4).
- **Frontier after.** The area-3b binder files reach in the engine only
  binder files, the host, the aliases and `sqlError` (12d.6 switches it to
  the identical `analyze` copy). Engine code reaches area-3b declarations
  only through binder methods (exported in 12d.6, see the notes).
- **Tests.** None new: no new seam (pure moves). Adapted mechanically by
  `movex`: qualified calls in 30 engine test files. No test moved or deleted.
- **Verification.** `make build`, `go vet` (identical to the base), gofmt and
  the targeted engine selection after every commit; `make quickcheck`
  passed on `e1f80935c` (48 GB memory cap). A broad `REFERENCE=1` selection
  was stopped at user request after ten packages (only the known `/postgres`
  drift had failed: `TestReferenceDeleteReturningBoundaries`, the four MERGE
  repeatable-read tests, `TestReferenceTemporaryTableDiagnostics`, the
  PostgreSQL-only `TestReferenceSerialDefaultProvider(PublicationHold|DropFirst)`);
  the session's narrow reference run follows 12d.6. `make bench`, branch and
  master: 851–938 µs/op (one 1,274 µs warm-up) against 828–889 µs/op, 5,014
  allocs/op and about 1.64 MB/op on both. `make deps`: module paths only; no
  engine subpackage imports the root. `AGENTS.md`: 100 lines before and
  after. racecheck/finalcheck skipped at user request.

#### 12d.6 result

Built at `cb31ead98` (after 12d.3b). Tools and scripts:
`.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-BINDER-MOVE/` (`c1.sh`–`c4.sh`,
`c2-edit.py`, `c3-pre.py`, the specs, `names.txt`, and the new `aliasqual`,
`unalias` limited to `-files`, which names moved types by their `analyze`
names inside the moving files only), with the 12d.4 `frontier`, `expspec`,
`poslit`, `movespec-qual` and `keylit`, the 10c `movex` and the 12d.3
`movedecl`, `mvpkg.sh` and `impfix`. The move set is 113 files: every file
with methods on the binder or its groups, the statement-kind binder files,
`binder_groups.go`, `query_cte_fold.go`, `insert_values_aggregate.go`,
`expression_regexp_set.go` and the host interface. Its frontier was closed
before the move (only `sqlError` and `insertError`, which resolve to the
identical `analyze` copies).

- **Last splits and helpers** (`947dec977`): the `consumerContext` partition
  methods → `partition_context_bind.go`; `frozenQueryDependencies` (engine
  only) → `view_definition.go` and `viewRenderer.quantifiedFieldsSQL` →
  `view_deparse.go`; `analyze.PGType` (`type_names.go`) and
  `analyze.ColumnDefault`, `DefaultDestination` and
  `DomainNullInputExpression` (`column_defaults.go`), qualified at their
  callers.
- **Host** (`5f9ca6d92`): `MapBoundQueryExpression` (the retag walker with
  identity types and a subquery-root mapper) and `ValidateViewReadGraph`;
  CTE restriction folding (`foldCTERestrictions`, `PushCTEOccurrence` and
  their helpers) and `RebaseFunctionCorrelation` take the host. 49 host
  methods.
- **Exports in place** (`babc61e76`): `expspec`/`movex` exported the 186 keys
  that engine code or its tests read (binder methods and fields, group
  fields, vocabulary constants, binder-file helpers; `newHostedBinder` →
  `NewBinder`, the `jsonbRecord*` members → `JSONBRecord*`), 6,856
  references. The exported wrappers `Info`, `CollatedInfo`,
  `DiagnosticTypeName` and `Type` only forwarded to unexported twins; the
  twins took their names (641 references) and the wrappers were deleted.
  The `analyzeHost` interface and `bindingSessionToken` moved to their own
  file.
- **Move** (`3fa00a84e`): `aliasqual` rewrote 307 alias uses in 42 moving
  files; `movespec` (names `expressionBinder` → `Binder`, `analyzeHost` →
  `Host`; `-extra fromSetObservation`, because its field shares the type's
  name) produced 18 alias rows, 31 qualified functions and constants and 2
  field renames (1,330 references, 197 engine files qualified); `git mv`
  into `analyze/` (`analysis_host_seam.go` → `host.go`), package line,
  `analyze.` qualifiers stripped, aliases merged, `goimports`, `keylit`,
  gofmt. The root keeps `newExpressionBinder`, `Engine.analysisHost`,
  `bindingHost`, the describe wrappers and the authority adapters. `analyze`
  now also imports `access` (L4), `authorization` (L1) and the Part A
  `routine` (decision 4); no lower package imports it.
- **Tests.** New: `TestAnalysisHostMapsBoundQueryExpressions` and
  `TestAnalysisHostValidatesViewReadGraph` (both failed to compile before the
  host methods). Adapted mechanically, no assertion changed: 264 engine test
  files (exported names, qualified helpers); `query_cte_test.go` and
  `query_regexp_set_test.go` pass a host to `PushCTEOccurrence` and
  `RebaseFunctionCorrelation`; `identity_check_test.go` calls
  `BuiltinVisible(name)` instead of the identical
  `builtinVisible(name, FunctionLookup)`. No test moved or deleted: binder
  tests need an engine host.
- **Verification.** `make build`, `go vet` (identical to the base up to the
  three pre-existing unkeyed-literal warnings now reported in `analyze`),
  gofmt and the targeted engine selection after every commit; the
  stack-frame strings the cancel fixtures wait on name no binder function.
  `make quickcheck` passed on `3fa00a84e` (48 GB memory cap). `REFERENCE=1` for CTE, CTE folding, regexp sets (correlation rebasing), subscripts, intagg, regclass, defaults and rule restrictions (`^TestReference(CTE|QueryCTE|RegexpSet|Subscript|Intagg|Regclass|Default|Rule(Restrictions|SelectReturn))`, eight packages; one run for the session at user request): passed. `make bench`, branch and master: 827–845 µs/op (one 963 µs warm-up) against 811–886 µs/op, 5,014 allocs/op and about 1.64 MB/op on both.
  `make deps`: module paths only; no engine subpackage imports the root.
  `AGENTS.md`: 100 lines before and after (the root bullet names the
  analysis host instead of binding). racecheck/finalcheck skipped at user
  request.

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
- [x] 12d — `analyze` created and binding moved (sessions: 12d.1 binding
  types, 12d.2 seam cuts, 12d.3–12d.5 and 12d.3b area preparation, 12d.6
  binder move)
  - [x] 12d refine (docs only; `b8850fca9`)
  - [x] 12d.1 — binding types (`5173b29cd`, `70d43f3e9`, `f630be9b9`,
    `5ed818cdf`, then the docs commit; see the 12d.1 result). Selected
    dual-mode proof and quickcheck passed; racecheck and finalcheck skipped
    at user request.
  - [x] 12d.2 — binder seam cuts (`e0d90806f`, `3ed685006`, `f718185df`,
    `48ea9b211`, `9115279bd`, `b7e9c8bd7`, then the docs commit; see the
    12d.2 result). Selected dual-mode proof and quickcheck passed;
    racecheck and finalcheck skipped at user request.
  - [x] 12d.3 — expression core preparation (`429bf3989`, `a750bbf16`,
    `4f491dace`, `ff9c86427`, `2ba59b6ba`, then the docs commit; see the
    12d.3 result). Selected dual-mode proof and quickcheck passed;
    racecheck and finalcheck skipped at user request.
  - [x] 12d.3b — area 3b (unlisted expression files) preparation
    (`8186d9690`, `f18840612`, `e1f80935c`, then the docs commit; see the
    12d.3b result). quickcheck passed; racecheck and finalcheck skipped at
    user request.
  - [x] 12d.4 — query, FROM and CTE preparation (`d350f8a03`, `c6475646c`,
    `20e14024e`, `ee4b1ac07`, `36bdb885a`, `792c18427`, `86957752b`, then
    the docs commit; see the 12d.4 result). Selected dual-mode proof and quickcheck
    passed; racecheck and finalcheck skipped at user request.
  - [x] 12d.5 — aggregates, windows and DML targets preparation
    (`949747b68`, `804df42f5`, `1a329c118`, `a73ebbc83`, `363efc88e`, then
    the docs commit; see the 12d.5 result). Selected dual-mode proof and
    quickcheck passed; racecheck and finalcheck skipped at user request.
  - [x] 12d.6 — binder moved (`947dec977`, `5f9ca6d92`, `babc61e76`,
    `3fa00a84e`, then the docs commit; see the 12d.6 result). quickcheck and
    the selected `REFERENCE=1` run passed (known drift only); racecheck and
    finalcheck skipped at user request.
- [ ] 12e — Aliases removed

Outstanding aliases: the 60 aliases in `internal/engine/analyze_aliases.go`:
the binding types (12d.1), including `ParameterInfo`, which the driver
names as `engine.ParameterInfo`; `lookupKind` and `namespaceSelection`
(12d.2); `queryLockAdmission`, `preparedSource`, `admittedMutationTarget`,
`queryColumn`, `queryQualifier` and `queryNamespace` (12d.4); and 12d.6's
binder types: `expressionBinder`, `analyzeHost`, the groups the engine
names (`clauseState`, `consumerContext`, `groupingState`, `queryScopeState`,
`storedSyntaxState`), the vocabulary (`expressionContext`,
`expressionPolicy`, `comparisonOperand`, `argumentWindowOwner`,
`functionProjectionScope`, `analyzedGroupItem`, `storedExpressionSyntaxKey`,
`fromSetObservation`, `sqlCallable`) and `QueryTargetComparison`/
`QueryTargetOccurrence`. 12d.5 removed `insertConflictSlot`.

Notes for later steps (the `12d.6` notes below were settled by the 12d.6
result):

- 12c: the host seam replaces only `engine` and `op`; `ctx`, `expected`,
  `input` and `dependencies` stay as statement inputs and output. The
  groups need no host: no group type holds `*Engine` or `*Operation`.
- 12d.3–12d.6, after 12d.2: binder methods reach no god type, but binder
  files still name root types and helpers that `reach` counts as clean.
  Re-run `frontier` for each area. Known: `scalarRenderer` and
  `capturedACLItemOutput` (`bindCreateDefaults`, `default.go`),
  `ruleNamespace` (binder field and `Describe` parameter; now
  `analyze.RuleNamespace`, 12d.3b; `queryLockAdmission` moved in 12d.4), and
  pure root helpers
  (`definition*At`, `statementCatalogType`, `refreshableTemporalCapture`,
  `checkExpectedLifetime`/`checkSignature`, `regtypeInputError`). 12d.3
  moved all of these except `definitionTypeAt` and `definitionArrayAt`,
  which 12d.3b moved to `catalog`.
- 12d.6: `bindingAuthorityView`, `authorityOf`, `authorizationOf` and
  `expectedOf` stay in the root (`checkExpectedLifetime` became
  `analyze.Expected.CheckLifetime` in 12d.3). Only the host
  (`bindingHost.view`) interprets a binding authority; `describe`,
  `describeWithRule`, `describeScoped` and `newExpressionBinder` are the root
  wrappers that convert `AuthorizationView` and `*StatementDescription`.
- Binders without a stored authority (setting calls, collation errors) box
  a fresh view per `bindingAuthorization`; a binder-local cache would remove
  it, but not provably without behavior change (12d.2 planning).
- 12d.6, from 12d.5: edges of the area-5 binders left for the move because
  other areas share them: `queryLockAdmission` (type, `newQueryLockAdmission`,
  `mutationTargets` and `admittedMutationTarget` read by `bindCTEMutation`)
  and `ruleNamespace` (`Describe` parameters), `mutationBinding`,
  `foldCTERestrictions`/`activateCTESources`, `validateViewReadGraph`,
  `queryDemandProperties` (`objectNameText` became `catalog.ObjectNameText`
  in 12d.3), and the `consumerContext`
  partition methods in `partition_bind.go` (a binder group's methods, so the
  file moves with the binder). `sqlError` and `insertError` resolve to the
  `analyze` copies once the binder moves.
- 12d.6, from 12d.5: `columnDefault`, `defaultDestination` and
  `domainNullInputExpression` (`domain_default.go`, `domain_check.go`) are
  expression builders the binder and engine DDL share. They were not moved
  because they reach `coercionProperties`, which 12d.3 moves to `analyze`;
  after 12d.3 they import only `analyze` and lower packages and can move to
  `analyze` exported.
- 12d.6, from 12d.5: the engine calls binder methods directly on binders it
  builds (`sameGroupExpression` from `check.go`, `partition_proof.go`,
  `partition_attach_schema.go` and `query_join_legality.go`, and
  `IndexExpressionBinder` users); export them in one pass with `expspec`.
  Only free functions were exported in the area sessions.
- 12d.6, from 12d.4: area-4 binder files still reach these shared engine
  helpers, left for the move: `sqlError` and `insertError` (they resolve to
  the `analyze` copies that 12d.5 added), `mapBoundExpression`
  (`expression_retag.go`; the query-mapper calls in `query_cte_fold.go` and
  `RebaseFunctionCorrelation`), `pgType`, and
  `captureRetainedExpressionInput` (now
  `analyze.CaptureRetainedExpressionInput`, 12d.3b).
- 12d.6, from 12d.3b: engine code that drives binders it builds, outside
  the statement-kind binders: `Engine.bindRuleDefinitionLocked`
  (`rule_definition.go`: sets `target.ruleNamespace`, `params.noParameters`,
  `query.auth`/`frame`/`nextQueryFrame` and `clause.aggregateClause`, calls
  `bind`, `normalizeStoredExpression`, `captureViewLiterals` and `FrameID`,
  reads `dependencies`, `query.sources`, `capture` and `prepared`) and
  `bindCreateDefaults` (`default.go`). Binder methods from area-3b files
  that engine files call: `bindBoolean`, `bindDefault`,
  `captureRetainedWriteInput`, `captureTypeDiagnosticInput`,
  `diagnosticTypeName`, `captureViewLiterals`, `definitionReader`. Binder-file
  declarations only root tests read: `sqlCallable` (and its fields),
  `appendSQLCallable`, `sqlCallableShape`, `selectSQLCallables`,
  `sequenceInputError`, `targetUsage`.
- 12d.6, from 12d.4: binder files call the helpers moved in 12d.3–12d.5 as
  `analyze.X`; strip that qualifier when the files move (`mv.sh` in the
  12d.4 tmp directory does it with `sed`). The binder group fields the
  engine sets or reads (`expressionBinder.query`: 86 production and 160 test
  reads; `.cte`; `queryBindingScope.clause` in one test) need exported names
  then. `assignOutput`, `bindCTEDefinitions`, `foldCTERestrictions`,
  `activateCTESources`, `combineQueryNamespace` and `rebaseQueryNamespace`
  are read only by the statement-kind binders and `Describe`, which move with
  the binder. Unlike 12d.5, 12d.4 also exported the area-4 binder methods
  that rule finalization and the engine tests call.
- 12d.5 (DML targets): split `boundInsertConflict` into the bound clause,
  which moves to `analyze`, and its per-execution state (arbiters, catalog
  checkpoints, `insertWriteOverlay`), which stays in the engine; then
  `InsertConflictSlot` can shrink to the runtime half.
- 12e: remove `analyze_aliases.go` with the `unalias` tool and settle §7
  decision 11 for `ParameterInfo` (the driver's `value.go` names it). After
  12d.6 the 60 aliases include `expressionBinder` (`analyze.Binder`, named in
  about 70 engine test files and by `newExpressionBinder` and
  `IndexExpressionBinder`) and `analyzeHost` (`analyze.Host`).
  Comments in engine files still name the old type names through the
  aliases.
- `slotwrap` (12d.1 tmp directory) turns a pointer field into an interface
  slot by use context; reuse it for later slots.
- 12d: `newHostedBinder` is the analyze-side constructor; the root keeps
  `newExpressionBinder` and `Engine.analysisHost`. `IndexExpressionBinder`
  returns an `*expressionBinder`, so the root must name the moved type.
- 12d.6: `binder_groups.go` moves with the binder; `binding_groups.go`
  moved in 12d.1. The `binderfields` tool (12b) and `bindsplit`
  (12a) rewrite selectors and literals by field object and are the model for
  export renames.

- 12d.6, from 12d.3: export the binder's members that engine code reads
  (`bind`, `capture`, `finish`, `info`, `query`, `params`, `consumer`,
  `target`, `input`, `dependencies`, ...) and its vocabulary types
  (`expressionPolicy`, `projectionContext`, `expressionContext`,
  `comparisonOperand`) in the scripted move, not in an area session.
- 12d.4: `string_table_bind.go` and `string_set_lower.go` call
  `mapBoundExpressionReader` like the whole-row demand did; reuse the host's
  `MapBoundExpression`.
- `catalog.ObjectNameText` now exists: `routine`'s private copy and
  `functions.qualifiedName` (both noted in their prompts) can use it.
- `access` B5 moves the binding walkers; 12d has moved `binding` and the
  binder (done with 12d.6).
- Step 15 (`typedef`): the composite, enum and range binders move here, not
  with `typedef`.
- Step 14 (`routine`): function and operator call resolution moves here.
