# internal/engine/stored

Stored SQL definitions and their presentation: the frozen payloads that
catalog descriptors carry, the renderers that turn them back into SQL, and
the mappers that retag and remap them. Step 22 of the engine split created
it (22b moved the presentation set). The package sits at layer L11 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture),
above `analyze` and `regobj`: it imports `analyze`, `catalog`, `dump`,
`exec`, `expr`, `extension`, `functions`, `ids`, `plan`, `regobj`,
`routine`, `sequence`, `typedef`, `parser`, `sqlstate`, `sqltype` and the
standard library, and never the engine, `session`, `storage`, `visibility`,
`lock` or an upper feature. Only the engine imports it (and later the upper
features). `analyze`, `exec`, `session`, `pgcatalog` and the lower features
reach it through the engine's host methods. See
[ENGINE-SPLIT-stored.md](../../../docs/ENGINE-SPLIT-stored.md).

## What it owns

- **Frozen payloads.** `View` (`view.go`, `view_payload.go`): a view's plan
  IR plus its analyzed captures, the `catalog.ViewPayload` of a relation;
  `ViewLeaf`, its stored sources; `CheckMode` (`CheckNone`, `CheckLocal`,
  `CheckCascaded`); `Policy` (`policy.go`), a policy condition, the
  `catalog.PolicyPayload`. `ViewOf`/`ViewPayload` and `PolicyOf` recover
  and store them. `FreezeQueryPresentation` (`view_freeze.go`) builds a
  `View` from a binding.
- **Payload API.** `View` fields are private. `NewView(ViewParts)` builds
  one; `Parts` returns a copy of the fields and `With(func(*ViewParts))`
  an edited copy, never changing the receiver. Read accessors cover what
  the engine reads (`Query`, `Syntax`, `Sources`, `Dependencies`, `Frames`,
  `Queries`, `CheckOption`, `Options`, `RuleOID`, `QuoteRevision`, ...).
  `NewPolicy(syntax, expression, presentation)` and `Syntax`, `Expression`,
  `Presentation` do the same for `Policy`. Maps and slices are shared, not
  copied: callers never write through an accessor.
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
- **Retag and remap.** `MapExpression`, `MapExpressionReader`,
  `RetagTypes`, `RetagTypesWithQuery` (`expression_retag.go`, the canonical
  mapper with `expression_properties.go` and `expression_volatility.go`);
  `RemapExpression`, `RemapExpressionWithQuery`, `RemapScalar`,
  `RemapLikeExpression`, `RemapViewQuery`, `ViewQueryRemapper`
  (`expression_remap.go`); `RemapView` (`frozen_presentation_remap.go`).
- **Readers of stored definitions.** `pg_depend` references: `DependRefs`,
  `ExpressionDependRefs`, `QueryDependRefs`, `IndexDependRefs`,
  `PartitionDependRefs`, `DependPinned` (`expression_references.go`,
  `catalog_pinning.go`). Temporary provenance and stored-reference checks
  (`temporary_provenance.go`: `ReferenceEpochValid`, `ViewReferenceValid`,
  `ViewUseReference`, ...). `ProcedureProvider`. Installed-node validators
  (`ValidateInstalled{Soundex,Levenshtein,Metaphone,Crypto,UUIDOssp}`).
  The stored-view read graph and CTE volatility (`ValidateViewReadGraph`,
  `ViewVolatile`, `CaptureRetainedInput`), view validation
  (`View.Validate`, `ValidateViewLeaves`, `ValidateSetNode`,
  `ValidateViewCheckDeclaration`, `ViewOptions`) and the view-write shape
  (`ViewWriteShape`, `ViewDirectColumn`, `SameViewRelation`).

## Names

Settled in 22a and 22b. The root names on the left still work through the
temporary type aliases in `internal/engine/stored_aliases.go`, which 22d
removes; functions and constants are qualified directly.

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
  answers; `ProcedureProvider.Validate` is the same validator.
- No mutex and no engine state: every function reads only the
  `catalog.Read`, session token and arguments its caller passes.

## What stays in the engine

- The `Engine` and scope entry points that capture or publish definitions:
  view, rule and policy DDL (step 27), `Engine.freezeViewDefinitionLocked`
  (allocates the rule OID), `remapViewRuleOIDs` and
  `reownCatalogExpressions` (`catalog_expression_reown.go`; 22c makes the
  latter `ReownExpressions`).
- The host bodies that call into `stored`, now forwarders: the
  `analyze.Host` methods `MapBoundExpression`, `MapBoundQueryExpression`,
  `StoredViewVolatile`, `ValidateViewReadGraph`, `CaptureRetainedViewInput`
  (`analysis_host.go`) and the `pgcatalog.Runtime` methods `ViewDefinition`,
  `ExpressionReferences`, `ViewReferences`, `IndexReferences`,
  `PartitionReferences` (`catalog_source_runtime.go`). `IndexDefinition`
  renders through the index code (step 23).
- The rule payload and its rendering (22c), `statementHost.DeparseInquiry`
  and `deparseRelation` (22c item 4), and the statement-scope
  `conversionDefault` cache.

## Testing

```sh
make check RUN='^TestViewPartsRoundTrip$'
make check RUN='^TestReference(View|Rule|Polic|RLS|RowSecurity|Materialized|Deparse|PGDepend|PGTableViews|GeneratedStored)'
make check RUN='Stored|Remap'
make check RUN='DumpRoundTrip|Restore|NativeControls'
```

Add `REFERENCE=1` for the real PostgreSQL oracle. `TestViewPartsRoundTrip`
pins the payload API: every `View` field has a `ViewParts` twin that
round-trips alone, and `With` never changes its receiver. The engine unit
tests of this code build an `Engine`, a catalog fixture or a test binder
and stay in `internal/engine` (renderer, retag, remap, `pg_depend`,
provenance and validator tests such as `TestDeparse*`, `TestPGDepend*`,
`TestViewCheck*`, `TestSQLRoutineFrozenPresentation`).
