# internal/engine/view

The code that *uses* stored queries and needs no engine: rule rewriting and rule
programs, the lowering of writes through auto-updatable views, stored-projection
demand, view options, concurrent materialized refresh and the materialized
snapshot codec, policy validation and RLS layer selection, the view, rule and
policy dependency checks and DDL decisions behind `Host` and `StageHost`, the
execution-time query preflight and join legality, the privilege walkers over
view definitions and view writes, and the read side of DROP behind `DropReader`.

## Imports

An upper feature in [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture)
(see also [ENGINE-SPLIT-view.md](../../../docs/ENGINE-SPLIT-view.md)). It imports
`access`, `analyze` (types and pure functions only), `authorization`, `catalog`,
`ddl`, `exec`, `expr`, `functions`, `ids`, `index`, `lock`, `pgcatalog`, `plan`,
`routine`, `sequence`, `session`, `storage`, `stored`, `table`, `typedef`,
`visibility`, `parser`, `sqlstate`, `sqltype` and the standard library. It never
imports the engine, `fdw`, `partition`, `role`, `dml` or `control`, and it never
builds a binder.

## What `stored` owns instead

The stored definitions are not here. The frozen view payload (`stored.View`),
the rule and policy payloads (`stored.Rule`, `stored.RuleAction`,
`stored.Policy`), their SQL rendering, dumps and remap are in
[`stored`](../stored/README.md). `view` imports `stored`; `analyze` reaches views
only through its host (`StoredViewVolatile`, `ValidateViewReadGraph`,
`CaptureRetainedViewInput`, `BindViewWrite`) and never imports `view`.

## What it owns

- **DDL decisions** (`host.go`, `create.go`, `alter_drop.go`, `rule_ddl.go`,
  `rule_bind.go`, `policy_ddl.go`, `definition.go`,
  `rule_action_presentation.go`): `Host` (stage-less: allocation, the access
  view, relation resolution, the temporary owner and the root-built rule and
  policy binders) and `StageHost` (`Host` and `ddl.Host` with row-type staging,
  array displacement and `ReownDisplacedTypes`), both implemented by the root's
  `ddlHost`. CREATE OR REPLACE VIEW is `CreateTarget`, `CheckCreate` (with
  `CreateOptions`) and `StageCreate`; ALTER VIEW is `AlterChange`; DROP VIEW is
  `DropTargets` and, after the waits, `RecheckDropTargets`. CREATE/ALTER/DROP
  RULE and POLICY are `PlanRule` and `PlanPolicy`, which return a
  `RelationChange` (a skip NOTICE, or the next relation and its query sources)
  that the root stages and publishes. `FreezeDefinition` freezes view and
  materialized view queries; `freezeRuleActionPresentation` and
  `freezeRuleModifierPresentations` freeze rule actions.
- **Query preflight** (`query_preflight.go`, `query_set_preflight.go`,
  `query_join_legality.go`): `PreflightQuery` copies the plan paths that hold
  folded coercions for one execution (window, join, set, DISTINCT,
  function-from and set-project nodes, rule producer demand);
  `PrepareStoredExecutionRoot` preflights a stored view query for its demanded
  outputs and returns the source reads; the execution-only join and row-mark
  legality checks `ValidateQueryJoinExecution`, `ValidateMutationJoinExecution`,
  `ValidateExpressionJoinExecution` and `ValidateMarkedQueryJoin`. They run on
  execution-owned parameters, never at Prepare. The root's `exec` preparation
  forwarders (`PreflightQuery`, `AdmitExpression` in `exec_preparation.go`) call
  them.
- **Privilege walkers** (`write_authority.go`, `source_authority.go`,
  `query_table_refs.go`): `CheckWrite` (a write through a view: the logical
  target through `analyze.CheckBinding`, then each layer under its owner or the
  original invoker), `AuthorizeSource` (SELECT on a stored view's source leaves,
  then its row marks), `CheckRowMarks` (stored and inherited row marks under each
  nested view's principal), `QueryTableRefs` (every executable view occurrence
  authorized, then view bases, partitions and inheritance children expanded into
  the stored relations to capture) and `CheckRowSecuritySource` (the
  `row_security` setting guard on a relation and, through stored views, on each
  source under its layer's principal). They take the captured `access.View` and
  its `access.Environment`; the root's `AuthorizationView.CheckViewWrite`,
  `CheckViewRowMarks` and `queryTableRefsLocked` forward to them. They check only
  relations and columns, so the caller's environment serves every nested
  principal.
- **Rule rewriting** (`rule_rewrite.go`, `rule_frames.go`,
  `rule_producer_demand.go`, `rule_query_*.go`, `rule_parameter_preflight.go`,
  `rule_update_output.go`): `RuleRewriter` (`ExpandLocked`, the INSTEAD, ALSO and
  conditional INSTEAD expansion with recursion detection by `ruleAncestry`),
  `ComposeRuleAction`, `RuleResidual`, `MakeRuleProducer`, the program records
  `RuleProgram` and `RuleCommand`, `RefreshRuleSources`, `FinalizeRuleQuery`,
  `ValidateRuleNewUpdateOutput`.
- **Rule authority** (`rule_authority.go`): `RuleAuthority`, the opaque statement
  authority a rewritten command carries. The engine's `AuthorizationView`
  implements it; `ruleAuthorizationOf` (root) recovers the full view at execution
  time.
- **Rule CTEs** (`rule_cte*.go`): `RuleCTECommand`, `RuleCTEProgramError`,
  `NormalizeRuleCTEsLocked` (expands each data-modifying CTE's rules into the
  binding's rule CTE recipes, over the catalog owner), `ValidateRuleActionCTEs`,
  `IsolateRuleCTEFrames`, `RetainRuleCTEPresentation`, `RefreshRuleActionCTEs`.
- **Rule DDL checks** (`rule_definition.go`, `rule_dependencies.go`,
  `rule_execution.go`): `RuleDuplicate`, `RuleIndex`, `RuleMissing`,
  `RuleResultCheck`, `RuleDepends`, `RuleReferences`, `BindingRuleRelation`.
- **Writes through views** (`write.go`, `write_insert.go`, `write_query.go`,
  `write_sources.go`, `binding_slots.go`, `default_origin.go`): `Write` (its base
  relation field is `BaseRelation`, because `Base()` is the
  `analyze.ViewWriteSlot` method), `WriteLayer`, `CheckGroup`, `BindViewWrite`,
  `PhysicalBinding`, `BindingDefaultOrigin`, `EvalBindingDefault`,
  `FreezeWriteSources` (the frozen-layout bridges of a view write's sources,
  borrowing the statement's query authority or host), and the `analyze` slot
  helpers `ViewWriteOf`, `ViewWriteSlotOf`, `RuleCTECommands`, `RuleCTEsOf`,
  `RuleCTEsSlotOf`.
- **View DDL records and checks** (`view.go`, `alter.go`, `dependencies.go`,
  `options.go`, `query.go`): `CreationScope`, `CheckViewOutputNames`,
  `CheckViewPrefix`, `RequestedViewPersistence`, `CheckColumnCollations`,
  `AlterViewAuthority`, `ViewDropMissing`, `ViewDropOwner`,
  `ValidateViewDefinition`, `ViewDepends`, `DependencyEdge`,
  `TransformViewOptions` and `StoredProjectionDemand`.
- **Lock walks** (`source_locks.go`, `query_locks.go`): `SourceWalk.Closure`
  walks the view definitions reachable from a statement's locked target and
  query leaves and returns a `SourceClosure` (the strongest row mode per
  relation, the relations still to acquire, the temporary views to revalidate
  and, for a refresh, the observed sources); `StoredLeaves` lists the visited
  stored relations; `DefinitionReferences` lists the relations a binding's
  dependencies and sequence constants name. `FirstQueryRowMode` finds the first
  row-lock mode a query or its stored views request (the read-only diagnostics);
  `QueryLocks` validates the read graph and the row-lock legality of every
  reached view and relation and returns a `QueryLockDemand`;
  `CurrentQuerySource` revalidates an admitted leaf's exact identity (domain,
  epoch, name, kind, row shape, storage lineage or sequence plan stamp). They
  read the statement's private `catalog.Read`; the root acquires the locks.
- **Stored view sources** (`stored_sources.go`): `FreezeStoredSources` installs
  the frozen-layout bridges and stored references of a stored view's captured
  sources; the root's `captureStoredSourceLocked` calls it after selecting the
  raw prefixes.
- **The DROP read side** (`drop_reader.go`, `drop_closure.go`, `dependents.go`,
  `drop_diagnostics.go`): `DropReader` is the drop stage (the base catalog, the
  staged or prepared replacements and removals, the diagnostic path and
  `DefinitionInput`) that the root's `dropProvider` implements, as it implements
  `table.DropDependencyReader`. `DropClosure` is the dependency closure (its
  edges are `table.DomainDependency`), `RestrictError` its RESTRICT error and
  `PassiveTypeDependency` the passive view type capture check.
  `DependencyClosure`, `DependentViews`, `KeepRules`, `KeepPolicies`,
  `DropPolicyDependents` and `DropRuleDependents` are the view, rule and policy
  dependents; `TypeChangeError` rejects ALTER COLUMN TYPE under a rule or view.
  The diagnostics are `Dependent` with its label helpers,
  `DependentsRestrictError` and `EmitCascade`. Object descriptions are
  `stored.DependencyObject`.
- **Materialized views** (`materialized_*.go`): the refresh scope and stage
  records (`MaterializedRefreshScope`, `MaterializedReplacement`,
  `MaterializedStage`), `MaterializedRefreshAccess`,
  `MaterializedRefreshCompatible`, the concurrent-refresh row matching and index
  eligibility (`MaterializedConcurrentAdmission`, `MaterializedConcurrentRows`,
  `MaterializedDuplicatePreflight`, `MaterializedNewEntries`,
  `SameMaterializedLineage`), the snapshot codec (`MaterializedSnapshot`,
  `EmitMaterializedSnapshot`, `DecodeMaterializedSnapshot`, the
  `stuntgres-matview-v1` comment) and its import checks
  (`ParseMaterializedSnapshot`, `MaterializedSnapshotInsert`),
  `MaterializedExtensionDepends`, and the replacement cores
  (`ConcurrentPrevious`, `ValidateMaterializedReplacement`,
  `MaterializedReplacement.Current`, `CheckMaterializedRows`,
  `MaterializedIndexCount`).
- **Policies and RLS** (`policy*.go`, `rls_base.go`, `row_security*.go`):
  `ValidatePolicies`, `PolicyClauses`, `PolicyDuplicate`, `PolicyIndex`,
  `PolicyMissing`, `PolicyOwnerNoun`, `PolicyPreflight`, `ResolvePolicyRoles`,
  `PolicyDepends`, `PolicyTypeChangeError`, `PolicyPayloadOf`,
  `RowSecurityLayers`, `CaptureDeniedQuerySource`; the `row_security` setting
  walkers (`CheckRowSecurityBinding` over a binding's target, query sources and
  data-modifying CTEs, `CheckRowSecurityRuleProgram` over a rule program, each
  command under its `RuleAuthority.Access()`); and the command state
  `RowSecurity`, which borrows OLD/NEW `exec.RLSProgram`s (`RowSecurityTarget`
  resets selection, `CheckProposal` shares NEW enforcement and
  `SelectAdjacentRowSecurity` retains MERGE/ON CONFLICT action boundaries).
- **Frozen occurrence graft** (`write_query.go`): `StoredQueryImporter` and
  `Write.GraftQuery` share one graft. Target policy source zero stays a row
  frame, while nested sources retain original refs/attribute layouts. The graft
  translates frozen read attributes to live dense Uses once per occurrence; zero
  frozen Reads is explicitly presence-only, not live nil/full demand. Required
  view keys stay demanded after renames/reordering, while unrequested
  projections still prune.
- **Target admission** (`target_admission.go`): `TargetAdmission`, the
  `table.TargetEntry` of CREATE/ALTER/DROP RULE and POLICY, CREATE OR REPLACE
  VIEW and ALTER VIEW, which the root registers with the `table` and `dml`
  entries.

## What stays in the engine

Dispatch (`execute.go`), `Engine.mu`, lock waits (the acquire loops of
`view_locks.go`, `acquireViewWriteTarget`, `acquireMaterializedRefresh`) and
`relation_scope.go` admission with `prepareViewCreation` (it binds the view query
with the scope's lock admission). The handler skeletons (`executeCreateView`,
`executeAlterView`, `executeDropView`, `executeRule`, `executePolicy`,
`publishRelationChangeLocked`, `finishPolicyNoticeLocked`) keep the statement
authority, the trusted Restore guard of CREATE VIEW, the fenced IF EXISTS
NOTICEs, the stage lifecycle, the unlock, the AccessExclusive waits and
dependents of DROP VIEW, and publication; RENAME and SET SCHEMA dispatch to the
root's table handlers. Also the host bodies (`view_host.go`;
`BindPolicyExpression` in `policy_expression.go`), rule program execution and
admission (they run DML), `prepareStoredQueryLocked` (it opens the sources under
`Engine.mu`), view source opening and the authorization capture around
`AuthorizeSource` (`view_source.go`), refresh row production and staging
(`refreshMaterializedLocked`, `materializedRefreshRowsLocked`,
`stageMaterializedRows`), the `CatalogStage` replacement wrappers, the
materialized source fence, `importMaterializedSnapshots` (target lookup, row
evaluation and installation), `admitRuleCTEsLocked`, the row-security authority
wrappers (`stageRowSecurityFlagsLocked`, `configureRowSecurityLocked`,
`rowSecurityInputLocked`), `ruleActionScope`, the `initializeQueryLocksLocked`
and `currentQuerySourceLocked` wrappers, `dropProvider`'s staging, removal and
lock-wait core, and the slot accessors on `CatalogStage` and `StatementScope`.

## Invariants

- Rule rewrite order: INSTEAD, ALSO and conditional INSTEAD rules expand in a
  fixed order, with recursion detection by object and event ancestry, a fixed
  command count and RETURNING provider. The composed action runs as the rule
  owner; the caller's authority is restored for the residual.
- Messages, SQLSTATEs, details and hints are stable, including the first error
  when several rules or policies fail.
- Concurrent refresh keeps the unique-index requirement, duplicate detection and
  source lineage checks. Snapshot comments (`stuntgres-matview-v1`) are
  byte-stable and decode the same way.
- `Host` and `StageHost` methods run with `Engine.mu` held, never take or
  release it and never wait; the decisions never begin, publish or discard a
  stage. `StageCreate` writes only to the stage it is given, and the root holds
  `Engine.mu` until it returns.
- The package holds no engine state and no mutex. Functions here never wait;
  `RuleRewriter.ExpandLocked` reads live storage through the rule authority, so
  its caller (and `NormalizeRuleCTEsLocked`'s) holds `Engine.mu`.
- **Snapshot import admission.** Native snapshots preserve captured values
  without REFRESH; PostgreSQL ignores the comments, so emitted WITH NO DATA
  definitions need REFRESH there. Import binds every scalar through the ordinary
  typed binder and accepts only the closed shapes the emitter writes: the
  qualified negative-circle codec, composite ROW members under a
  destination-owned qualified cast, and nested rank-one arrays as the qualified
  three-argument `array_fill` singleton seed or two-argument `array_cat` seed
  plus a nonempty qualified typed ARRAY tail (closed signed int32 lower bounds,
  recursively closed elements). Named, VARIADIC, decorated, arbitrary and
  malformed calls are rejected. Before preflight or evaluation, admitted array
  calls must keep the genuine `ArrayTransform` provider and builtin array-family
  identity; same-named `pg_catalog` SQL overloads gain no codec authority. Import
  requires a full destination-ordered column list with closed integer slice
  bounds; constructor values must keep their rank and every dimension length.
  Only authentic stored domain NULLs authorize private cell transport, and a
  slice on a captured NULL placeholder is rejected.

## Testing

Package units cover the slot helpers and the target-admission entry. Every other
view, rule, policy, materialized-view and query preflight unit builds an engine
and stays in `internal/engine`, naming the code as `view.X`; `TestViewHostSeam`
and `TestDropReaderSeam` pin the two seams, and the
`TestMaterializedViewSnapshot*` units cover snapshot import admission. Public SQL
lives in [reference/views](../../../reference/views/README.md#testing) and RLS in
[reference/roles](../../../reference/roles/README.md#testing); add `REFERENCE=1`
for the oracle.

```sh
make check RUN='^Test(ViewHostSeam|DropReaderSeam|ViewTargetAdmission|BindingSlots|ViewWriteSlot|MaterializedViewSnapshot)'
make check RACE=1 RUN='^TestMaterializedRefreshConcurrent'
make check RUN='^TestReference(View|Rule|Materialized)'
make check RUN='^TestReferenceRLS'
```
