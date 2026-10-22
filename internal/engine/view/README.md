# internal/engine/view

The code that *uses* stored queries and needs no engine: rule rewriting and
rule programs, the lowering of writes through auto-updatable views,
stored-projection demand, view options, concurrent materialized refresh and
the materialized snapshot codec, policy validation and RLS layer selection,
and the view, rule and policy dependency checks, the view, rule and policy
DDL decisions behind `Host` and `StageHost`, and the execution-time query
preflight and join legality. Step 27 of the engine split created it (27b
moved the clean set, 27d the handler decisions, 27e the query preflight). It
is an
upper feature in
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture):
it imports `access`, `analyze` (types and pure functions only),
`authorization`, `catalog`, `ddl`, `exec`, `expr`, `functions`, `ids`, `index`,
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

- **DDL decisions** (`host.go`, `create.go`, `alter_drop.go`,
  `rule_ddl.go`, `rule_bind.go`, `policy_ddl.go`, `definition.go`,
  `rule_action_presentation.go`): `Host` (stage-less: allocation, the
  access view, relation resolution, the temporary owner and the
  root-built rule and policy binders) and `StageHost` (`Host` and
  `ddl.Host` with row-type staging, array displacement and
  `ReownDisplacedTypes`), both implemented by the root's `ddlHost`.
  CREATE OR REPLACE VIEW is `CreateTarget`, `CheckCreate` (with
  `CreateOptions`) and `StageCreate`; ALTER VIEW is `AlterChange`; DROP
  VIEW is `DropTargets` and, after the waits, `RecheckDropTargets`.
  CREATE/ALTER/DROP RULE and POLICY are `PlanRule` (with `planSelectRule`
  and `bindRuleDefinition`) and `PlanPolicy`, which return a
  `RelationChange` (a skip NOTICE, or the next relation and its query
  sources) that the root stages and publishes. `FreezeDefinition` freezes
  view and materialized view queries; `freezeRuleActionPresentation` and
  `freezeRuleModifierPresentations` freeze rule actions.
- **Query preflight** (`query_preflight.go`, `query_set_preflight.go`,
  `query_join_legality.go`): `PreflightQuery` copies the plan paths that
  hold folded coercions for one execution (window, join, set, DISTINCT,
  function-from and set-project nodes, rule producer demand);
  `PrepareStoredExecutionRoot` preflights a stored view query for its
  demanded outputs and returns the source reads; the execution-only join
  and row-mark legality checks `ValidateQueryJoinExecution`,
  `ValidateMutationJoinExecution`, `ValidateExpressionJoinExecution` and
  `ValidateMarkedQueryJoin`. They run on execution-owned parameters, never
  at Prepare. The root's `exec` preparation forwarders (`PreflightQuery`,
  `AdmitExpression` in `exec_preparation.go`) call them.
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
  `write_query.go`, `write_sources.go`, `binding_slots.go`,
  `default_origin.go`): `Write`, `WriteLayer`, `CheckGroup`,
  `viewWriteOccurrence`, `BindViewWrite`, `lowerViewWrite`,
  `lowerViewInsert`, `PhysicalBinding`, `BindingDefaultOrigin`,
  `EvalBindingDefault`, `FreezeWriteSources` (the frozen-layout bridges of
  a view write's sources, borrowing the statement's query authority or
  host), and the `analyze` slot helpers `ViewWriteOf`,
  `ViewWriteSlotOf`, `RuleCTECommands`, `RuleCTEsOf`, `RuleCTEsSlotOf`.
- **View DDL records and checks** (`view.go`, `alter.go`,
  `dependencies.go`, `options.go`, `query.go`): `CreationScope` (the CREATE
  VIEW scope record), `CheckViewOutputNames`, `CheckViewPrefix`,
  `RequestedViewPersistence`, `AlterViewAuthority`, `ViewDropMissing`,
  `ViewDropOwner`, `ValidateViewDefinition`, `ViewDepends`,
  `DependencyEdge`, `TransformViewOptions`, `StoredProjectionDemand`, and
  the pure dependents cores of DROP (`dependents.go`):
  `DependencyClosure`, `KeepRules`, `KeepPolicies`.
- **Materialized views** (`materialized_*.go`): the refresh scope and stage
  records (`MaterializedRefreshScope`, `MaterializedReplacement`,
  `MaterializedStage`), `MaterializedRefreshAccess`,
  `MaterializedRefreshCompatible`, the concurrent-refresh row matching and
  index eligibility (`MaterializedConcurrentAdmission`,
  `MaterializedConcurrentRows`, `MaterializedDuplicatePreflight`,
  `MaterializedNewEntries`, `SameMaterializedLineage`), the snapshot codec
  (`MaterializedSnapshot`, `EmitMaterializedSnapshot`,
  `DecodeMaterializedSnapshot`, the `stuntgres-matview-v1` comment),
  `MaterializedExtensionDepends`, and the replacement cores
  (`materialized_replacement.go`): `ConcurrentPrevious`,
  `ValidateMaterializedReplacement`, `MaterializedReplacement.Current`.
- **Policies and RLS** (`policy*.go`, `rls_base.go`): `ValidatePolicies`,
  `PolicyClauses`, `PolicyDuplicate`, `PolicyIndex`, `PolicyMissing`,
  `PolicyOwnerNoun`, `PolicyPreflight`, `ResolvePolicyRoles`,
  `PolicyDepends`, `PolicyTypeChangeError`, `PolicyPayloadOf`,
  `RowSecurityLayers`, `CaptureDeniedQuerySource`, and the RLS selection
  (`row_security.go`): `RowSecurity` (the command's state the root keeps in
  `statementMutation.rls`), `RowSecurityTarget`, `SelectRowSecurity`,
  `RowSecurityInputDenied`.

Names settled in 27b: types lose a stuttering view prefix (`viewWrite` is
`Write`, `viewCheckGroup` is `CheckGroup`, `viewCreationScope` is
`CreationScope`); everything else the engine names is capitalized.
`Write`'s base relation field is `BaseRelation`, because `Base()` is the
`analyze.ViewWriteSlot` method. Helpers only the package uses keep their
lower-case names.

## What stays in the engine

Dispatch (`execute.go`), `Engine.mu`, lock waits (`view_locks.go`,
`acquireViewWriteTarget`, `acquireMaterializedRefresh`) and
`relation_scope.go` admission with `prepareViewCreation` (it binds the view
query with the scope's lock admission). The handler skeletons
(`executeCreateView`, `executeAlterView`, `executeDropView`, `executeRule`,
`executePolicy`, `publishRelationChangeLocked`, `finishPolicyNoticeLocked`)
keep the statement authority, the trusted Restore guard of CREATE VIEW, the
fenced IF EXISTS NOTICEs, the stage lifecycle, the unlock, the
AccessExclusive waits and dependents of DROP VIEW, and publication; RENAME
and SET SCHEMA dispatch to the root's table handlers. The host bodies
(`view_host.go`; `BindPolicyExpression` in `policy_expression.go`), rule
program execution and admission (they run DML), `prepareStoredQueryLocked`
(it opens the sources under `Engine.mu`), view source opening and
authorization (`view_source.go`), `checkViewRow`, refresh row production and
staging (`refreshMaterializedLocked`, `materializedRefreshRowsLocked`,
`stageMaterializedRows`), the `CatalogStage` replacement wrappers, the
materialized source fence and its kind, `importMaterializedSnapshots`,
`stageRowSecurityFlagsLocked`, `configureRowSecurityLocked` and
`rowSecurityInputLocked` as authority wrappers, `ruleActionScope`, the
privilege walkers (`CheckViewWrite`, `row_security_setting.go`), the drop
diagnostics and `dropProvider` (it collects the live descriptors and stages
the dependents), and the slot accessors on `CatalogStage` and
`StatementScope`. The third round moves the clean parts here: query
preflight (27e, done), the privilege walkers over `access.View` (27f), the drop
diagnostics and `dropProvider`'s read side behind a reader (27g), and the
closure walks and materialized checks (27h). See
[ENGINE-SPLIT-view.md](../../../docs/ENGINE-SPLIT-view.md) 27d.

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
- `Host` and `StageHost` methods run with `Engine.mu` held, never take or
  release it and never wait; the decisions never begin, publish or discard
  a stage. `StageCreate` writes only to the stage it is given, and the root
  holds `Engine.mu` until it returns.
- The package holds no engine state and no mutex. Functions here never
  wait; `RuleRewriter.ExpandLocked` reads live storage through the rule
  authority, so its caller holds `Engine.mu`.

## Testing

`binding_slots_test.go` covers the slot helpers. Every other view, rule,
policy, materialized-view and query preflight unit test builds an engine and
stays in the root package, naming the moved code as `view.X`; `view_host_test.go`
(`TestViewHostSeam`) pins the host seam.

```sh
make check RUN='^Test(BindingSlots|ViewWriteSlot|Rule|View|Materialized|Polic|RLS|RowSecurity)'
make check RUN='^Test(Query(OuterJoins|Lock|Set|Window)|CastCoercion.*Preflight|ExprCollation|ObjectAddress|Series|StringSplit|SourceProtocol)'
make check RACE=1 RUN='^TestMaterializedRefreshConcurrent'
```

Public behavior is covered by the dual-mode reference suite:

```sh
make check RUN='^TestReference(View|Rule|Materialized|RLSBase)'
make check RUN='^TestReference(AccessRoleRLS|InheritanceQueryRLS|InheritanceDMLRLS|DriverBatchPolicy)'
make check RUN='^TestReference(DumpRoundTrip$$|Deparse)'
```
