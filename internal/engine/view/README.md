# internal/engine/view

The code that *uses* stored queries and needs no engine: rule rewriting and
rule programs, the lowering of writes through auto-updatable views,
stored-projection demand, view options, concurrent materialized refresh and
the materialized snapshot codec, policy validation and RLS layer selection,
and the view, rule and policy dependency checks. Step 27 of the engine
split created it (27b moved the clean set). It is an upper feature in
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture):
it imports `access`, `analyze` (types and pure functions only),
`authorization`, `catalog`, `exec`, `expr`, `functions`, `ids`, `index`,
`plan`, `sequence`, `session`, `storage`, `stored`, `table`, `typedef`,
`visibility`, `parser`, `sqlstate`, `sqltype` and the standard library. It
never imports the engine, `partition`, `dml` or `control`, and it never
builds a binder. See
[ENGINE-SPLIT-view.md](../../../docs/ENGINE-SPLIT-view.md).

## What `stored` owns instead

The stored definitions are not here. The frozen view payload
(`stored.View`), the rule and policy payloads (`stored.Rule`,
`stored.RuleAction`, `stored.Policy`), their SQL rendering, dumps and remap
are in [`stored`](../stored/README.md). `view` imports `stored`; `analyze`
reaches views only through its host (`StoredViewVolatile`,
`ValidateViewReadGraph`, `CaptureRetainedViewInput`, `BindViewWrite`) and
never imports `view`.

## What it owns

- **Rule rewriting** (`rule_rewrite.go`, `rule_frames.go`,
  `rule_producer_demand.go`, `rule_query_*.go`, `rule_parameter_preflight.go`,
  `rule_update_output.go`): `RuleRewriter` (`ExpandLocked`, the INSTEAD,
  ALSO and conditional INSTEAD expansion with recursion detection by
  `ruleAncestry`), `ComposeRuleAction`, `RuleResidual`, `MakeRuleProducer`,
  the program records `RuleProgram` and `RuleCommand`, `ruleCommandSource`,
  `RefreshRuleSources`, `FinalizeRuleQuery`, `ValidateRuleNewUpdateOutput`.
- **Rule authority** (`rule_authority.go`): `RuleAuthority`, the opaque
  statement authority a rewritten command carries. The engine's
  `AuthorizationView` implements it; `ruleAuthorizationOf` (root) recovers
  the full view at execution time.
- **Rule CTEs** (`rule_cte*.go`): `RuleCTECommand`, `RuleCTEProgramError`,
  `ValidateRuleActionCTEs`, `IsolateRuleCTEFrames`,
  `RetainRuleCTEPresentation`, `RefreshRuleActionCTEs`.
- **Rule DDL checks** (`rule_definition.go`, `rule_dependencies.go`,
  `rule_execution.go`): `RuleDuplicate`, `RuleIndex`, `RuleMissing`,
  `RuleResultCheck`, `ruleReturning`, `RuleDepends`, `RuleReferences`,
  `BindingRuleRelation`.
- **Writes through views** (`write.go`, `write_insert.go`,
  `write_query.go`, `binding_slots.go`, `default_origin.go`): `Write`,
  `WriteLayer`, `CheckGroup`, `viewWriteOccurrence`, `BindViewWrite`,
  `lowerViewWrite`, `lowerViewInsert`, `PhysicalBinding`,
  `BindingDefaultOrigin`, and the `analyze` slot helpers `ViewWriteOf`,
  `ViewWriteSlotOf`, `RuleCTECommands`, `RuleCTEsOf`, `RuleCTEsSlotOf`.
- **View DDL records and checks** (`view.go`, `alter.go`,
  `dependencies.go`, `options.go`, `query.go`): `CreationScope` (the CREATE
  VIEW scope record), `CheckViewOutputNames`, `CheckViewPrefix`,
  `RequestedViewPersistence`, `AlterViewAuthority`, `ViewDropMissing`,
  `ViewDropOwner`, `ValidateViewDefinition`, `ViewDepends`,
  `DependencyEdge`, `TransformViewOptions`, `StoredProjectionDemand`.
- **Materialized views** (`materialized_*.go`): the refresh scope and stage
  records (`MaterializedRefreshScope`, `MaterializedReplacement`,
  `MaterializedStage`), `MaterializedRefreshAccess`,
  `MaterializedRefreshCompatible`, the concurrent-refresh row matching and
  index eligibility (`MaterializedConcurrentAdmission`,
  `MaterializedConcurrentRows`, `MaterializedDuplicatePreflight`,
  `MaterializedNewEntries`, `SameMaterializedLineage`), the snapshot codec
  (`MaterializedSnapshot`, `EmitMaterializedSnapshot`,
  `DecodeMaterializedSnapshot`, the `stuntgres-matview-v1` comment) and
  `MaterializedExtensionDepends`.
- **Policies and RLS** (`policy*.go`, `rls_base.go`): `ValidatePolicies`,
  `PolicyClauses`, `PolicyDuplicate`, `PolicyIndex`, `PolicyMissing`,
  `PolicyOwnerNoun`, `PolicyPreflight`, `ResolvePolicyRoles`,
  `PolicyDepends`, `PolicyTypeChangeError`, `PolicyPayloadOf`,
  `RowSecurityLayers`, `CaptureDeniedQuerySource`.

Names settled in 27b: types lose a stuttering view prefix (`viewWrite` is
`Write`, `viewCheckGroup` is `CheckGroup`, `viewCreationScope` is
`CreationScope`); everything else the engine names is capitalized.
`Write`'s base relation field is `BaseRelation`, because `Base()` is the
`analyze.ViewWriteSlot` method. Helpers only the package uses keep their
lower-case names.

## What stays in the engine

Dispatch (`execute.go`), `Engine.mu`, lock waits (`view_locks.go`,
`acquireViewWriteTarget`, `acquireMaterializedRefresh`) and
`relation_scope.go` admission. The DDL handlers and their staging and
publication (`executeCreateView`, `executeAlterView`, `executeDropView`,
`executeRule`, `executePolicy`), the binders (`bindRuleDefinitionLocked`,
`bindPolicyExpression`, `prepareViewCreation`), rule program execution and
admission (they run DML), `prepareStoredQueryLocked` and
`prepareStoredExecutionRoot` (query preflight), view source opening and
authorization (`view_source.go`), `checkViewRow`, refresh row production
and staging (`refreshMaterializedLocked`, `materializedRefreshRowsLocked`,
`stageMaterializedRows`), the materialized source fence and its kind,
`importMaterializedSnapshots`, RLS configuration
(`configureRowSecurityLocked`, `stageRowSecurityFlagsLocked`) and the scope
groups it writes (`rowSecurityMutation`, `ruleActionScope`), the privilege
walkers (`CheckViewWrite`, `row_security_setting.go`), the drop
diagnostics and `dropProvider`, and the slot accessors on `CatalogStage`
and `StatementScope`. 27d (planned in 27c) moves the handler decisions
behind `view.Host` and `view.StageHost`, both implemented by the root's
shared `ddlHost` under `Engine.mu`, and moves `rowSecurityMutation` here as
`RowSecurity`; the handler skeletons, waits and publication stay. See
[ENGINE-SPLIT-view.md](../../../docs/ENGINE-SPLIT-view.md) 27c.

## Invariants

- Rule rewrite order: INSTEAD, ALSO and conditional INSTEAD rules expand in
  the same order, with the same recursion detection (ancestry by object and
  event), command count and RETURNING provider. The composed action runs
  as the rule owner; the caller's authority is restored for the residual.
- Every message, SQLSTATE, detail and hint stays identical, including the
  first error when several rules or policies fail.
- Concurrent refresh keeps the unique-index requirement, duplicate
  detection and source lineage checks. The snapshot comments stay
  byte-identical and decode the same way.
- The package holds no engine state and no mutex. Functions here never
  wait; `RuleRewriter.ExpandLocked` reads live storage through the rule
  authority, so its caller holds `Engine.mu`.

## Testing

`binding_slots_test.go` covers the slot helpers. Every other view, rule,
policy and materialized-view unit test builds an engine and stays in the
root package, naming the moved code as `view.X`.

```sh
make check RUN='^Test(BindingSlots|ViewWriteSlot|Rule|View|Materialized|Polic|RLS|RowSecurity)'
make check RACE=1 RUN='^TestMaterializedRefreshConcurrent'
```

Public behavior is covered by the dual-mode reference suite:

```sh
make check RUN='^TestReference(View|Rule|Materialized|RLSBase)'
make check RUN='^TestReference(AccessRoleRLS|InheritanceQueryRLS|InheritanceDMLRLS|DriverBatchPolicy)'
make check RUN='^TestReference(DumpRoundTrip$$|Deparse)'
```
