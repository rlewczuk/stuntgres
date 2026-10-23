# internal/engine/stored

Stored SQL definitions and their presentation: the frozen payloads that catalog
descriptors carry (views, rules, policies, routine recipes), the renderers that
turn them back into SQL, and the mappers that retag and remap them. View, rule
and policy DDL decisions and rule rewriting live in [`view`](../view/README.md).

## Imports

Layer L11 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture),
above `analyze` and `regobj`. It imports `analyze`, `authorization`, `catalog`,
`dump`, `exec`, `expr`, `extension`, `functions`, `ids`, `lock` (row-lock
modes), `plan`, `proc`, `regobj`, `routine`, `sequence`, `storage`, `typedef`,
`parser`, `sqlstate`, `sqltype` and the standard library, and never the root
engine, `session` or `visibility`. The engine and the upper feature packages
(`control`, `dml`, `fdw`, `index`, `partition`, `role`, `table`, `view`) import
it; `analyze`, `exec`, `session` and `pgcatalog` reach it only through engine
host methods.

## What it owns

- **Frozen payloads.** `View` and `ViewLeaf` (`view.go`, `view_payload.go`),
  the `catalog.ViewPayload` of a relation, with `CheckMode`; `Policy`
  (`policy.go`), the `catalog.PolicyPayload`; `Rule`, `RuleAction` and
  `RuleList` (`rule.go`, `rule_payload.go`), the `catalog.RulesPayload`.
  `ViewOf`/`ViewPayload`, `PolicyOf` and `RulesOf`/`RulesPayload` recover and
  store them; `FreezeQueryPresentation` (`view_freeze.go`) builds a `View`
  from a binding.
- **Payload API.** Payload fields are private. `NewView(ViewParts)` builds a
  view; `Parts` returns a copy of the fields and `With(func(*ViewParts))` an
  edited copy, never changing the receiver. `NewPolicy`, `RuleParts`/`NewRule`
  and `RuleActionParts`/`NewRuleAction` (`rule_parts.go`) follow the same
  pattern; a rule's `OID` and `Name` stay exported fields. Read accessors share
  maps and slices instead of copying them: callers never write through an
  accessor.
- **Routine recipes.** `RoutineProgram` owns checked SQL-standard actions.
  `RoutineDefaults` owns an immutable input-indexed declaration-default recipe
  and its source-domain/epoch manifest, never evaluated values. Default
  dependencies are hard even for quoted bodies.
- **Renderers.** `Deparser`/`DeparseParent` (`deparse_*.go`): expressions in
  the style of PostgreSQL's `ruleutils.c`, plus constraint SQL
  (`constraint_sql.go`) and `DeparseAttribute`. `ScalarRenderer`
  (`scalar_*.go`): stored scalar SQL for defaults, checks and dumps, recording
  the references it prints (`DefaultSQL`, `RenderColumnDefault`).
  `QueryRenderer` and `Presentation` (`view_deparse*.go`,
  `view_render_sources.go`, `sqljson_deparse.go`): whole queries,
  `View.SelectSQL`, `ViewDefinitionSQL` (`pg_get_viewdef`) and `PolicySQL`.
- **Rule and policy dumps.** `RuleDumpGroups`/`RuleActionSQL`,
  `PolicyDumpGroups` and `RowSecurityDumpGroups`.
- **Retag and remap.** `MapExpression`, `RetagTypes`, `RetagTypesWithQuery`
  (`expression_retag.go`, the canonical mapper); `RemapExpression`,
  `RemapScalar`, `RemapLikeExpression`, `RemapViewQuery` (`expression_remap.go`);
  `RemapView` (`frozen_presentation_remap.go`).
- **The remap protocol.** `ReownExpressions` (`expression_reown.go`) remaps and
  retags every stored expression of a catalog state after a renumbering
  (Restore, extension owner import, DROP OWNED, TEMP preservation) or after type
  changes only. It walks domain checks and defaults, column defaults and
  generations (`RefreshGenerationChecks`), index keys and predicates, partition
  bounds (`RemapPartitionDescriptor`), rules (`RemapRules`, `RemapRuleBinding`,
  `RemapRuleConstraintIdentities`), policies (`RemapPolicies`) and views. The
  rule tree mapper (`NewRuleTreeMapper`, `MapRuleBindingRecipes`,
  `RefreshRuleBinding`) is shared with rule rewriting. `ReownScalarTypes`
  points relation column types and stored datums at current type generations;
  `ReownVersions` applies it after an enum or domain change.
- **Readers of stored definitions.** `pg_depend` references
  (`ExpressionDependRefs`, `QueryDependRefs`, `IndexDependRefs`,
  `PartitionDependRefs`, `DependPinned`); temporary provenance and reference
  epoch checks (`temporary_provenance.go`); `ProcedureProvider` and
  `TypeProvider` validators with the domain recipe validators and
  `ValidateTypeProviders`; `PruneQuoteCaptures`; installed-node validators
  (`ValidateInstalled*`); the view read graph and volatility
  (`ValidateViewReadGraph`, `ViewVolatile`, `CaptureRetainedInput`); row-mark
  readers (`view_row_marks.go`); `HasPgrowlocksDependency`; view validation and
  view-write shape; `ValidateRules`; and `DependencyObject` (object
  descriptions for DROP and role diagnostics).

- **Catalog relation pinning** (`catalog_pinning.go`): bootstrap catalog
  tables stay pinned, while catalog views such as `pg_views` record ordinary
  SELECT-rule dependency edges. Relation refs in the immutable source and
  dependency manifests, including attributes, are validated before the
  address-only dependency pass. Runtime view-reference capture checks
  cancellation first, then requires the exact live consumer and a concrete
  non-nil stored view definition; an invalid consumer returns
  `ids.ErrCatalogReplaced` with no references. Retained TEMP view lifetime is
  independent of pinning: `ViewReferenceValid` requires the exact captured
  catalog-relation descriptor and valid consumer, namespace, domain, epoch and
  attribute authority.

## Recipe rules

- **Selected recipes, not endpoints.** Stored trees keep the selected
  conversion: simple CASE arms keep their test conversion as a typed
  lexical-input template, and array-cast element templates keep their element
  routine. Remap and generation refresh include those templates and their
  routine dependencies and never rebuild a fixed coercion from endpoint types.
  Rendering emits ordinary simple CASE (the source is evaluated once) or array
  CAST syntax, never the placeholders. Selected datum casts render a literal
  with its source type before the target cast, so a quoted value never becomes
  target-codec input on replay.
- **SQL/JSON.** Query nodes carry their RETURNING usage in
  `Rendering.OwnedReturning`; replay and display use it, not raw type spelling.
  Raw view capture keeps all query children and options. Reconstruction remints
  executable query/default plans after descriptor generations change.
- **Routine evolution.** Replacement first stabilizes routine, formal and result
  type handles, then remaps default and standard captures; address-only and
  full-reference maps stay distinct and typed children are retagged once.
  Normal type evolution refreshes ordered formal/input/output/result handles
  before dependent trees, copies only affected declarations and payloads, and
  shares unchanged identity. Its fast paths compare declarations with
  `exec.SameRoutineDeclaration`, never descriptor pointers; replacement/retag
  uses the broad carrier-change detector. Domain DEFAULT and CHECK consumers use
  the same declaration-aware predicate.
- **Address vs reference mapping.** Regclass datum OIDs use the address mapper;
  only actual captures enter checked source-domain/epoch reference remapping.
  There is no zero-domain lifetime bypass.
- **Calls.** SQL routine call presentation keeps written labels and explicit
  VARIADIC. `Capture` exposes only written operands; `Children` also includes
  omitted defaults for dependency, refresh and remap traversal.
- **Aggregates.** Owned aggregate recipes keep the original selected argument
  syntax and catalog reference/epoch; capture, count and render share
  `AggregateRecipeProcedure`. Transition-expression remap validates the exact
  source and mapped destination private formal vectors before and after
  remapping; ordinary stored parameters keep their zero-bound rejection.
- **Policies.** Policy runtime consumes `Policy.Expression`/`Presentation`
  through `RewriteViewExpression`/`MapExpression` and never mutates the payload
  or reparses its SQL. A presentation keeps the binder's full execution
  manifest, including pinned callable providers; `pg_depend` edges are a
  filtered subset (validation requires inclusion, not equal size).
- **TEMP replacement.** Full runtime manifests differ from catalog edges: exact
  retained builtin type generations, equal seeded collation capabilities and
  unchanged seeded procedure declarations may bridge reconstructed wrappers.
  User providers, operators and physical sources stay exact, and full
  remapping and final validation still run. Never remove a lifetime to make
  replacement pass.
- **Access methods.** `ProcedureProvider` validates exact registered
  access-method identity, signature, result and kind; dependency pinning follows
  real bootstrap handlers, not an OID cutoff.
- The engine, not these payloads, owns admission records, post-wait
  selection/ACL fences and lazy producer pin lifetime.

## Invariants

- Rendering is byte-stable: `pg_get_viewdef`, `pg_get_expr`,
  `pg_get_constraintdef`, `pg_get_indexdef`, rule definitions, policy
  expressions, `information_schema` view text, stored defaults and checks and
  every Dump statement keep their exact bytes unless a change intends otherwise.
- Frozen payloads are immutable once published. Retag, remap and `With` build
  new values (copy on write) and never mutate a published `View`, rule list or
  `Policy`; source expressions stay immutable.
- Retag keeps the canonical mapper's property rules
  (`RewrittenIntrinsicProperties`: eliminated branches keep analyzed
  dependence; replaced child demand is dropped).
- `ProcedureProvider.Validate` and `TypeProvider.Validate` are the single
  validators, with fixed error text and order.
- No mutex and no engine state: every function reads only the `catalog.Read`,
  session token and arguments its caller passes.

## What stays in the engine

- View, rule and policy DDL entry points (decisions in `view`) and
  `remapCatalogExpressions` (`catalog_expression_reown.go`), the Restore entry
  that calls `ReownExpressions`. The imported rule OID remap is
  `control.RemapViewRuleOIDs`.
- Host forwarders into this package: the `analyze.Host` methods
  `MapBoundExpression`, `MapBoundQueryExpression`, `StoredViewVolatile`,
  `ValidateViewReadGraph`, `CaptureRetainedViewInput` (`analysis_host.go`) and
  the `pgcatalog.Runtime` methods `ViewDefinition`, `ExpressionReferences`,
  `ViewReferences`, `IndexReferences`, `PartitionReferences`
  (`catalog_source_runtime.go`). `IndexDefinition` renders through
  `index.DefinitionSQL`; relation deparsing is `table.Deparse`.

## Testing

Package units cover the payload API, routine recipes and aggregate recipes.
`TestViewPartsRoundTrip` and `TestRulePartsRoundTrip` require every `View`,
`Rule` and `RuleAction` field to have a parts twin that round-trips alone and
`With` to leave its receiver unchanged; extend them when adding a field.
Renderer, retag, remap, `pg_depend`, provenance and validator tests build an
`Engine` or catalog fixture and live in `internal/engine`. Public behavior
belongs in `reference/`; follow [docs/TESTING.md](../../../docs/TESTING.md) and
add `REFERENCE=1` for the oracle.

- Payload API and stored row marks: `RUN='^Test(ViewPartsRoundTrip|RulePartsRoundTrip|QuerySetStoredInheritedMarks)$'`
- Routine recipes, remap and type-generation refresh: `RUN='^Test(Routine|SQLRoutine)'`
- Aggregate recipes: `RUN='^TestCreateAggregate(FrozenRecipeAuthority|TransitionFrameRemap|PolymorphicTransitionRemap)$'`
- Engine renderer and `pg_depend` units: `RUN='^Test(Deparse|PGDepend|ViewCheck)'`
- Views, rules and deparsing: `RUN='^TestReference(Rule|Deparse|Materialized)'`
- Row security: `RUN='^TestReference(RLS|RowSecurity)'`
- Catalog projections: `RUN='^TestReference(PGDepend|PGTableViews)'`
- Stored generated columns: `RUN='^TestReferenceGeneratedStored'`
