# internal/engine/stored

Stored SQL definitions and their presentation: the frozen payloads that
catalog descriptors carry, the renderers that turn them back into SQL, and
the mappers that retag and remap them. Step 22 of the engine split created
it (22b moved the presentation set; 22c the rule payload, the rule and
policy dumps and the remap protocol; 22e the type provider validation).
The package sits at layer L11 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture),
above `analyze` and `regobj`: it imports `analyze`, `authorization`, `catalog`, `dump`,
`exec`, `expr`, `extension`, `functions`, `ids`, `plan`, `regobj`,
`routine`, `sequence`, `storage`, `typedef`, `parser`, `sqlstate`,
`sqltype` and the standard library, and never the engine, `session`, `visibility`,
`lock` or an upper feature. Only the engine imports it (and later the upper
features). `analyze`, `exec`, `session`, `pgcatalog` and the lower features
reach it through the engine's host methods. See
[ENGINE-SPLIT-stored.md](../../../docs/ENGINE-SPLIT-stored.md).

SQL/JSON query nodes carry their internal RETURNING usage through
`Rendering.OwnedReturning`; scalar replay and display use that descriptor, not
raw type spelling or an enclosing cast. Raw view capture retains all query
children/options and RETURNING usage. Generic reconstruction remints executable
query/default plans after descriptor generations change.

## What it owns

- **Frozen payloads.** `View` (`view.go`, `view_payload.go`): a view's plan
  IR plus its analyzed captures, the `catalog.ViewPayload` of a relation;
  `ViewLeaf`, its stored sources; `CheckMode` (`CheckNone`, `CheckLocal`,
  `CheckCascaded`); `Policy` (`policy.go`), a policy condition, the
  `catalog.PolicyPayload`. `Rule` and `RuleAction` (`rule.go`): a rule's
  event, condition and actions with their analyzed bindings and frozen
  presentations; `RuleList` (`rule_payload.go`) is the
  `catalog.RulesPayload` of a relation, with the `Len`/`Visit`/`Validate`
  accessors catalog 5j introduced; `RuleEvent` names a statement's event.
  `ViewOf`/`ViewPayload`, `PolicyOf` and `RulesOf`/`RulesPayload` recover
  and store them. `FreezeQueryPresentation` (`view_freeze.go`) builds a
  `View` from a binding.
- **Payload API.** `View` fields are private. `NewView(ViewParts)` builds
  one; `Parts` returns a copy of the fields and `With(func(*ViewParts))`
  an edited copy, never changing the receiver. Read accessors cover what
  the engine reads (`Query`, `Syntax`, `Sources`, `Dependencies`, `Frames`,
  `Queries`, `CheckOption`, `Options`, `RuleOID`, `QuoteRevision`, ...).
  `NewPolicy(syntax, expression, presentation)` and `Syntax`, `Expression`,
  `Presentation` do the same for `Policy`. `Rule` and `RuleAction` are
  values with the same API (`rule_parts.go`): `RuleParts`/`NewRule` and
  `RuleActionParts`/`NewRuleAction`, `Parts`, `With`, and read accessors
  (`Event`, `Instead`, `Condition`, `ConditionPresentation`,
  `ConditionBinding`, `Actions`, `References`; `Syntax`, `Binding`,
  `Presentation`, `ModifierPresentations`). A rule's `OID` and `Name` stay
  exported fields. Maps and slices are shared, not copied: callers never
  write through an accessor.
- **Renderers.** `Deparser` with `DeparseParent` (`deparse_renderer.go`,
  `deparse_rendering.go`): expressions in the style of PostgreSQL's
  `ruleutils.c`, plus `ForeignKeyConstraintSQL` and `UniqueConstraintSQL`
  (`constraint_sql.go`) and `DeparseAttribute`. `ScalarRenderer`
  (`scalar_renderer.go`, `scalar_rendering.go`, `scalar_support.go`):
  stored scalar SQL for defaults, checks and dumps, recording the
  references it prints; `DefaultSQL`, `RenderColumnDefault`,
  `OmitColumnDefault`. `QueryRenderer` and `Presentation`
  (`view_deparse*.go`, `view_render_sources.go`, `sqljson_deparse.go`, ...):
  whole queries; `View.SelectSQL`, `ViewDefinitionSQL` (`pg_get_viewdef`),
  `PolicySQL`.
- **Dumps of rules and policies.** `RuleDumpGroups` with `RuleActionSQL`
  (`rule_dump.go`, `rule_source_dump.go`, `rule_assignment_dump.go`,
  `update_row_dump.go`), `PolicyDumpGroups` (`policy_dump.go`) and
  `RowSecurityDumpGroups` (`rls_base_dump.go`).
- **Retag and remap.** `MapExpression`, `MapExpressionReader`,
  `RetagTypes`, `RetagTypesWithQuery` (`expression_retag.go`, the canonical
  mapper with `expression_properties.go` and `expression_volatility.go`);
  `RemapExpression`, `RemapExpressionWithQuery`, `RemapScalar`,
  `RemapLikeExpression`, `RemapViewQuery`, `ViewQueryRemapper`
  (`expression_remap.go`); `RemapView` (`frozen_presentation_remap.go`).
- **The remap protocol.** `ReownExpressions` (`expression_reown.go`)
  remaps and retags every stored expression of a catalog state after a
  renumbering (Restore, extension owner import, DROP OWNED, TEMP
  preservation) or after type changes only (type generations). It walks
  domain checks and defaults, column defaults and generations, CHECK
  constraints (`RefreshGenerationChecks`), index keys and predicates,
  partition bounds (`RemapPartitionDescriptor`), rules (`RemapRules`,
  `RemapRuleBinding`, `rule_remap.go`), policies (`RemapPolicies`) and
  views. `RemapRuleConstraintIdentities` follows renumbered constraint
  OIDs. The rule tree mapper and binding map (`NewRuleTreeMapper`,
  `RuleTreeMapping`, `MapRuleBindingRecipes`, `RefreshRuleBinding`) are
  shared with rule rewriting, which stays in the engine.
- **Readers of stored definitions.** `pg_depend` references: `DependRefs`,
  `ExpressionDependRefs`, `QueryDependRefs`, `IndexDependRefs`,
  `PartitionDependRefs`, `DependPinned` (`expression_references.go`,
  `catalog_pinning.go`). Temporary provenance and stored-reference checks
  (`temporary_provenance.go`: `ReferenceEpochValid`, `ViewReferenceValid`,
  `ViewUseReference`, ...). `ProcedureProvider`. `TypeProvider`
  (`type_provider.go`, the type class validator) with the domain recipe
  validators it calls, `ValidateDomainDefault` and
  `ValidateDomainCheckDefinitions` (`domain_*_validation.go`), and
  `ValidateTypeProviders` (`type_provider_validation.go`, the publication
  check that types and relations name current type generations).
  `ReownScalarTypes` (`scalar_type_reown.go`) is the scalar companion of
  `ReownExpressions`: it points relation column types and stored datums
  (domain values, reg* and aclitem values) at current type generations.
  `ReownVersions` applies it to a private write list or a retained owner
  snapshot after an enum or domain change (catalog 5k; `typedef` cannot
  own it because this package imports `typedef`).
  `PruneQuoteCaptures` (`quote_captures.go`) drops quote constants whose
  column default or view definition was retired. Installed-node validators
  (`ValidateInstalled{Soundex,Levenshtein,Metaphone,Crypto,UUIDOssp}`).
  The stored-view read graph and CTE volatility (`ValidateViewReadGraph`,
  `ViewVolatile`, `CaptureRetainedInput`), the stored row-mark readers
  (`view_row_marks.go`: `ViewHasRowMarks`, `ViewRowLockLegality`,
  `ViewLeafRowMark`), view validation
  (`View.Validate`, `ValidateViewLeaves`, `ValidateSetNode`,
  `ValidateViewCheckDeclaration`, `ViewOptions`) and the view-write shape
  (`ViewWriteShape`, `ViewDirectColumn`, `SameViewRelation`,
  `RewriteViewExpression`). Rule validation (`ValidateRules`) and the
  constraint names rules depend on (`ConstraintName`, `ConstraintParent`).
  Dependency object descriptions (`dependency_object.go`):
  `DependencyObject` (PostgreSQL's getObjectDescription for DROP and
  role diagnostics) and `DependencyRelationName`; `view` and `role` both
  call them (view 27g).

## Names

Settled in 22a, 22b and 22c. The root names on the left are gone: 22d removed
the temporary type aliases, and the root and its tests name `stored.X`. The
table maps the root names that older prompt files and design notes cite.

| Root name | `stored` name |
|---|---|
| `viewDefinition`, `viewLeaf`, `viewOf`, `viewPayloadOf` | `View`, `ViewLeaf`, `ViewOf`, `ViewPayload` |
| `viewPresentation`, `viewRenderer`, `newViewRenderer` | `Presentation`, `QueryRenderer`, `NewQueryRenderer` |
| `pgRenderer`, `pgRenderParent` | `Deparser`, `DeparseParent` |
| `scalarRenderer`, `defaultSQL`, `renderColumnDefault` | `ScalarRenderer`, `DefaultSQL`, `RenderColumnDefault` |
| `retagBoundTypes`, `retagBoundTypesWithQuery`, `mapBoundExpression` | `RetagTypes`, `RetagTypesWithQuery`, `MapExpression` |
| `remapBoundExpression`, `remapScalarExpression`, `remapViewQuery`, `remapFrozenPresentation` | `RemapExpression`, `RemapScalar`, `RemapViewQuery`, `RemapView` |
| `policyExpression`, `policyExpressionOf`, `policyExpressionSQL` | `Policy`, `PolicyOf`, `PolicySQL` |
| `pgDependRefs`, `pgDependExpressionRefs`, `pgDependQueryRefs`, `pgDependPinned` | `DependRefs`, `ExpressionDependRefs`, `QueryDependRefs`, `DependPinned` |
| `viewCheckMode`, `viewCheckNone`, ... | `CheckMode`, `CheckNone`, ... |
| `stored*` helpers (`storedReferenceEpochValid`, `storedViewUseReference`, ...) | without the prefix (`ReferenceEpochValid`, `ViewUseReference`, ...) |
| `validateInstalled*` | `ValidateInstalled*` |
| `ruleDefinition`, `ruleAction`, `ruleList`, `rulesOf`, `rulesPayloadOf`, `ruleEvent` | `Rule`, `RuleAction`, `RuleList`, `RulesOf`, `RulesPayload`, `RuleEvent` |
| `ruleDumpGroups`, `ruleActionSQL`, `policyDumpGroups`, `rowSecurityDumpGroups` | `RuleDumpGroups`, `RuleActionSQL`, `PolicyDumpGroups`, `RowSecurityDumpGroups` |
| `remapRules`, `remapRuleBinding`, `remapRuleConstraintIdentities`, `remapPolicies`, `remapPartitionDescriptor` | `RemapRules`, `RemapRuleBinding`, `RemapRuleConstraintIdentities`, `RemapPolicies`, `RemapPartitionDescriptor` |
| `ruleTreeMapper`, `ruleTreeMapping`, `newRuleTreeMapper`, `mapRuleBindingRecipes`, `refreshRuleBinding` | `RuleTreeMapper`, `RuleTreeMapping`, `NewRuleTreeMapper`, `MapRuleBindingRecipes`, `RefreshRuleBinding` |
| `Engine.reownCatalogExpressions`, `refreshGenerationChecks`, `validateRules`, `constraintName`, `constraintParent`, `rewriteViewExpression` | `ReownExpressions`, `RefreshGenerationChecks`, `ValidateRules`, `ConstraintName`, `ConstraintParent`, `RewriteViewExpression` |
| `typeProvider`, `validateTypeProviders`, `validateDomainDefault`, `validateDomainCheckDefinitions` (22e) | `TypeProvider`, `ValidateTypeProviders`, `ValidateDomainDefault`, `ValidateDomainCheckDefinitions` |
| `reownCatalogScalarTypes`, `pruneQuoteCaptures` (22e) | `ReownScalarTypes`, `PruneQuoteCaptures` |

## Invariants

- Rendering output is byte-identical to the engine code it came from:
  `pg_get_viewdef`, `pg_get_expr`, `pg_get_constraintdef`,
  `pg_get_indexdef`, rule definitions, policy expressions,
  `information_schema` view text, stored defaults and checks, and every
  Dump statement.
- Frozen payloads are immutable once published. Retag, remap and `With`
  build new values (copy on write) and never mutate a published `View`,
  rule list or `Policy`.
- Retag keeps the canonical mapper's property rules
  (`RewrittenIntrinsicProperties`: eliminated branches keep analyzed
  dependence; replaced child demand is dropped).
- Temporary provenance and stored-reference epoch checks keep their
  answers; `ProcedureProvider.Validate` and `TypeProvider.Validate` are
  the same validators, with the same error text and order.
- No mutex and no engine state: every function reads only the
  `catalog.Read`, session token and arguments its caller passes.

## What stays in the engine

- The `Engine` and scope entry points that capture or publish definitions:
  the view, rule and policy DDL handlers (step 27; their decisions, the
  rule binding and `view.FreezeDefinition`, which allocates the rule OID,
  are in [`view`](../view/README.md)) and `remapCatalogExpressions`,
  the Restore entry that calls `ReownExpressions`
  (`catalog_expression_reown.go`). Rule rewriting and the rule program
  records are in `view`; the imported rule OID remap is
  `control.RemapViewRuleOIDs`.
- The host bodies that call into `stored`, now forwarders: the
  `analyze.Host` methods `MapBoundExpression`, `MapBoundQueryExpression`,
  `StoredViewVolatile`, `ValidateViewReadGraph`, `CaptureRetainedViewInput`
  (`analysis_host.go`) and the `pgcatalog.Runtime` methods `ViewDefinition`,
  `ExpressionReferences`, `ViewReferences`, `IndexReferences`,
  `PartitionReferences` (`catalog_source_runtime.go`). `IndexDefinition`
  renders through `index.DefinitionSQL`.
- `statementHost.DeparseInquiry` and `deparseRelation`: 22c measured them
  and left them in the engine. Exclusion constraints and `pg_get_indexdef`
  render through `index.DefinitionSQL` (above `stored`), and the view-name form resolves through
  `Engine.resolveRelationSnapshot`. The statement-scope `conversionDefault`
  cache stays too.

## Testing

```sh
make check RUN='^Test((View|Rule)PartsRoundTrip|QuerySetStoredInheritedMarks)$'
make check RUN='^TestReference(View|Rule|Polic|RLS|RowSecurity|Materialized|Deparse|PGDepend|PGTableViews|GeneratedStored)'
make check RUN='Stored|Remap'
make check RUN='DumpRoundTrip|Restore|NativeControls'
make check RUN='^Test(DomainDefault|DomainCheck|CubeProvider|IntarrayProvider|ObjectACLType|ArrayCatalogAssociation|StringVariadicRetainedGeneration|QueryTableCreate(Regclass|XID)|NetworkDomainCatalogRemap)'
```

Add `REFERENCE=1` for the real PostgreSQL oracle. `TestViewPartsRoundTrip`
and `TestRulePartsRoundTrip` pin the payload API: every `View`, `Rule` and
`RuleAction` field has a parts twin that round-trips alone, and `With`
never changes its receiver. The engine unit
tests of this code build an `Engine`, a catalog fixture or a test binder
and stay in `internal/engine` (renderer, retag, remap, `pg_depend`,
provenance and validator tests such as `TestDeparse*`, `TestPGDepend*`,
`TestViewCheck*`, `TestSQLRoutineFrozenPresentation`; the rule, policy and
reowner tests in `rule_*_test.go`, `update_row_dump_test.go`,
`rls_base_test.go` and the tests that call `stored.ReownExpressions`).
