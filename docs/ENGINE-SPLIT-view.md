# Engine split, step 27: `internal/engine/view`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-view.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §6 step 27; §3 rules 6–8
  (`view` is an upper feature: it may import `analyze` types and pure
  functions, never build a binder); §4.1 patterns 2, 3, 4 and 6; the §4
  tests rule.
- **Needs:** 27a: nothing (rebase on 22a if 22a lands first; both touch
  `view_dependencies.go`). 27b: 27a, 22c (the rule and policy payloads live
  in `stored`), 24b (`table`, for `dumpRelation` and the default
  dependency checks; it also brings `index` for `indexOrdering`). 27c:
  27b and 20b (the shared `ddl` host). 27d: 27c.
- **Effort:** high for 27a; high for 27b (broad); xhigh for 27c; high for
  27d.
- **Session size:** one sub-step per session.
- **Linear items:** 27a = item 99, 27b = item 100 (**broad**), 27c = item
  101, 27d = item 102. Related: 20b = 61, 22c = 69, 24b = 87, 29b = 108.
- **Tools:** run from the worktree root.
  - `reach`: `.cswd/tmp/PG18-ENGINE-SPLIT-EXEC-REFINE/reach/reach -dir .
    -files '<regexp on file base names>'` prints each declaration as
    `clean` or `DIRTY path`.
  - `frontier`: `.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-QUERY-PREP/frontier/frontier
    -dir . -files a.go,b.go [-decls Name,Type.method] [-exclude ...]` prints
    the set's members, its OUT edges (root declarations it uses) and its IN
    edges (outside uses).
  - Rewrite tools: `movex`, `unalias`, `keylit`, `fieldrn`, `meth2fn` in
    `.cswd/tmp/PG18-ENGINE-SPLIT-PLAN-ALIASES/`; `expspec`, `movespec`,
    `mv.sh` in `.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-QUERY-PREP/`. Never pipe
    a tool into `head` (SIGPIPE stops it half way).
  - Measurements for this file are in
    `.cswd/tmp/ENGINE-SPLIT-POSTPONED-PLAN/view/`.

## Goal

Views, rules, row-level security policies and materialized views become a
feature package. This is the code that *uses* stored queries: rule
rewriting, the lowering of writes through views, materialized refresh,
row-level security application, and the DDL decisions of CREATE/ALTER/DROP
VIEW, RULE and POLICY and REFRESH MATERIALIZED VIEW.

The stored definitions themselves are not in this package. The frozen view
payload (`viewDefinition`), the rule and policy payloads, their SQL
rendering and their remap move to `stored` first (step 22). `view` imports
`stored`. `analyze` keeps reaching views only through its host
(`StoredViewVolatile`, `ValidateViewReadGraph`, `CaptureRetainedViewInput`,
`BindViewWrite`); it never imports `view`.

## Inventory (measured at `c154e14fb`)

The view area has 71 root files, about 17.5k lines, 11.1k of them clean.
About 6.4k of the clean lines belong to `stored` (22b, 22c): the renderers,
`viewDefinition` and its validators, the rule and policy payloads and dumps.
They are not part of this step. What is left for `view`:

| Sub-area | Files | Clean now | Clean after 27a |
|---|---|---:|---:|
| Rule rewriting and programs | `rule_rewrite.go`, `rule_producer_demand.go`, `rule_tree_mapper.go`, `rule_query_finalize.go`, `rule_binding_map.go`, `rule_query_graft.go`, `rule_cte_admission.go`, `rule_frames.go`, `rule_cte_frames.go`, `rule_cte_presentation.go`, `rule_parameter_preflight.go`, `rule_cte.go`, `rule_cte_refresh.go`, and the clean parts of `rule_definition.go`, `rule_dependencies.go`, `rule_execution.go` | 2.4k | +0.8k |
| View DDL records, options, checks | the clean parts of `view.go`, `view_alter.go`, `view_dependencies.go`; `view_options.go`, `view_cycles.go`, `view_retained_input.go`, `view_query.go` (clean part) | 0.5k | +0.05k |
| Writes through views | the clean parts of `view_write.go`, `view_write_insert.go`, `view_write_query.go` | 0.2k | +0.55k |
| Materialized views | `materialized_refresh_concurrent.go`, `materialized_snapshot.go`, `materialized_stage.go`, `materialized_alter.go`, the clean part of `materialized_refresh.go` | 0.5k | +0.02k |
| Policies and RLS | the clean parts of `policy.go`, `policy_dependencies.go`, `rls_base.go` | 0.1k | +0.05k |
| **Total** | | **3.7k** | **+1.45k = about 5.2k** |

### Clean declarations that move in 27b (by file)

- `rule_rewrite.go`: `loanRuleDefaultDependencies`, `makeRuleProducer`,
  `normalizeRuleInsert`, `ruleAlso`, `ruleAncestry`, `ruleBindingHasPseudo`, `ruleColumnInfo`,
  `ruleCommandSource`, `ruleConditionalInstead`, `ruleInstead`, `ruleJoin`,
  `ruleOriginal`, `ruleProducer`, `ruleQueryInput`, `ruleReturningProjection`,
  `ruleSetAction`; after 27a also `ruleCommand`, `ruleProgram`,
  `composeRuleAction`, `ruleResidual`, `refreshRuleSources`,
  `markRuleCommandSource` and the three expansion functions (today
  `Engine.expandRulesLocked`, `Engine.expandRuleResidualLocked`,
  `Engine.appendRuleCommand`).
- Whole files: `rule_producer_demand.go`, `rule_query_finalize.go`,
  `rule_query_graft.go`, `rule_cte_admission.go`, `rule_cte_frames.go`,
  `rule_cte_presentation.go`, `rule_parameter_preflight.go`,
  `rule_frames.go` (after the dead `rewriteRuleOccurrences` is deleted),
  `view_options.go`, `materialized_refresh_concurrent.go` (without
  `materializedSourceFence`, see "Stays in the root"),
  `materialized_snapshot.go` (without the two `Engine` import methods),
  `materialized_stage.go`, `materialized_alter.go`.
- `rule_cte.go`: `ruleCTECommand`, `ruleCTEProgramError` (after 27a).
  `rule_cte_refresh.go`: `refreshRuleActionCTEs` (after 27a).
- `rule_definition.go`: `ruleDuplicate`, `ruleEventCode`, `ruleIndex`,
  `ruleMissing`, `ruleResultCheck`, `ruleReturning`. (`ruleDefinition`,
  `ruleAction` and `ruleEvent` go to `stored` in 22c.)
- `rule_dependencies.go`: `ruleDepends`, `ruleReferences`.
  `rule_execution.go`: `bindingRuleRelation`.
- `view.go`: `checkViewOutputNames`, `checkViewPrefix`,
  `requestedViewPersistence`, `viewCreationScope` (the scope slot record).
  `view_alter.go`: `viewDropMissing`, and after 27a
  `alterViewAuthority`, `viewDropOwner`.
- `view_dependencies.go`: `validateViewDefinition`, `viewDepends`,
  `viewDependencyEdge`.
- `view_query.go`: `storedProjectionDemand`,
  `storedProjectionDemandExpressions`, `storedProjectionOutputDemand`,
  `storedScheduledPayloadDemand`.
- `view_write.go`: `stableViewSlot`, `viewCheckGroup`,
  `viewComputedWriteError`, `viewWrite`, `viewWriteError`, `viewWriteLayer`,
  `viewWritePredicate`, and after 27a `bindViewWrite`, `lowerViewWrite`.
  `view_write_insert.go`: `physicalBinding`, and after 27a
  `lowerViewInsert`. `view_write_query.go`: `viewWrite.graftQuery`,
  `viewWriteOccurrence`.
- `materialized_refresh.go`: `materializedRefreshCompatible`,
  `materializedRefreshScope`, `materializedReplacement`,
  `relationDefiningAuthorityCompatible` (unless 5i moved it to `catalog`:
  it only compares two `catalog.Relation` values), and after 27a
  `materializedRefreshAccess`.
- `policy.go`: `policyClauses`, `policyDuplicate`, `policyIndex`,
  `policyMissing`, `policyOwnerNoun`, `validatePolicies`, and after 27a
  `policyPreflight`, `resolvePolicyRoles`. `policy_dependencies.go`:
  `policyDepends`, `policyTypeChangeError`.
- `rls_base.go`: `captureDeniedQuerySource`, `rowSecurityLayers`.

### Clean code that does not go to `view`

- Settled with the `stored` prompt (its fixed point needs them): the rule
  tree mapper and binding map (`rule_tree_mapper.go`,
  `rule_binding_map.go`), `refreshRuleBinding` and `refreshRuleConflict`
  (`rule_rewrite.go`) go to `stored` in 22c; `viewOptions` (`view.go`),
  `view_cycles.go`, `view_retained_input.go` and `query_cte_volatility.go`
  go to `stored` in 22b. The sizes in the table above still count them;
  27b moves about 0.6k lines less.

- To `stored` (22b/22c): everything in the presentation fixed point (about
  300 declarations), including `view_deparse.go`, `view_render_sources.go`,
  `view_payload.go`, `view_check.go`, `view_literal_validation.go`,
  `view_composite_validation.go`, the `viewDefinition` methods and
  `validateViewLeaves`/`validateStoredSetNode` of `view_dependencies.go`,
  `rewriteViewExpression`, `viewDirectColumn`, `viewWriteShape`,
  `sameViewRelation` (`view_write.go`), `mapRuleAssignmentTargets`
  (`mapViewQualification` is `exec.MapViewQualification` since 11f), the rule and policy payloads and dumps, and
  the payload remap: `rule_remap.go` (`remapRules`, `remapRuleBinding*`,
  `remapRuleConstraintIdentities`), `policy_remap.go` (`remapPolicies`)
  and `catalogExpressionTypesChanged` (`expression_remap.go`). The remap goes
  with the catalog-wide reowner (`reownCatalogExpressions`, 22c), which
  calls it outside Restore too.
- To `dump` (step 18, done in 18b): the dump-graph records (`dump.Phase`,
  `dump.Event`, `dump.DeclarationEvents`, `dump.Order`, ...).
- To `access` (B6): the RLS action rules of `rls_base.go`
  (`rowSecurityAction`, `rowSecurityActionLabel`, `rowSecurityActions`,
  `rowSecurityFlags`, `rowSecurityKind`). Only ALTER TABLE code uses them.
  Done: `access.RowSecurityActions`, `RowSecurityFlags`, `RowSecurityKind`.
- Root drop diagnostics (27a moves them out of the view files; they stay in
  the root): `dependencyObject`, `dependencyRelationName`,
  `supportedDependent`, `supportedDependentLabels`,
  `supportedDependentLabelsWithPolicies`, `supportedRestrictErrorLabels`,
  `emitViewCascade`, `ruleDependentLabels`, `ruleDependentLabelsFrom`,
  `policyDependentLabels`, `policyDependentLabelsFrom`. They collect
  dependents of every feature (CHECK and DEFAULT edges, domains, views,
  rules, policies) for DROP and ALTER TYPE diagnostics, and table, typedef,
  sequence and inheritance code calls them. They belong to the DROP
  pipeline, which stays in the root (§6 step 20).

### Dirty declarations, and why

Most are dirty only because they hold or take `AuthorizationView`, which
holds `*Engine` (27a cuts these):

- `ruleCommand.auth AuthorizationView`, and through it `ruleProgram`,
  `markRuleCommandSource`, `ruleCTEProgramError`; `composeRuleAction`,
  `ruleResidual`, `refreshRuleSources`, `refreshRuleActionCTEs`,
  `ruleCTECommand.caller`. Uses: `auth.Catalog`, `auth.Owner.Domain`,
  `auth.State.CurrentUser` (changed to the rule owner in
  `composeRuleAction`), and one call to
  `auth.engine.authorizationStorageLocked` in `refreshRuleSources`.
- `Engine.expandRulesLocked` (`rule_rewrite.go:737`, about 150 lines),
  `expandRuleResidualLocked`, `appendRuleCommand`: they use the engine
  only for `e.catalog.owner()`, `e.catalog.Domain.Ref` and
  `e.bindViewWrite`; the `scope` parameter is only passed through.
- `Engine.bindViewWrite` (`view_write.go:163`), `lowerViewWrite`,
  `lowerViewInsert`: they read only `auth.Catalog` and `e.catalog.owner()`.
- `policyPreflight`, `resolvePolicyRoles`, `alterViewAuthority`,
  `viewDropOwner`, `materializedRefreshAccess`: they take
  `AuthorizationView` to run `access` checks; `access.View` plus an
  `access.Environment` is enough.

Genuinely dirty (they stay in the root, or their decisions move in 27d):

| Declaration | File | Why |
|---|---|---|
| `Engine.executeCreateView` (235 lines) | `view.go:214` | stage, publication, row type, journal |
| `StatementScope.prepareViewCreation`, `setActiveViews` | `view.go` | scope slot, called from `relation_scope.go:36` and `:101` |
| `Engine.executeAlterView` (151), `executeDropView` (149) | `view_alter.go` | stage, rename/SET SCHEMA dispatch, DROP cascade |
| `Engine.executeRule` (179) | `rule_ddl.go:17` | stage, publication, binds through `bindRuleDefinitionLocked` |
| `Engine.bindRuleDefinitionLocked` (102) | `rule_definition.go` | builds a binder (`analysisHost`) |
| `Engine.executePolicy` (155), `finishPolicyNoticeLocked` | `policy_ddl.go:15` | stage, publication, notices |
| `Engine.bindPolicyExpression` (62) | `policy_expression.go` | builds a binder |
| `Engine.refreshMaterializedLocked` (100), `materializedRefreshRowsLocked`, `stageMaterializedRows` | `materialized_refresh.go:124` | consumes `Engine.mu`, runs the stored query, stages storage |
| `Engine.stageRowSecurityFlagsLocked` | `rls_base_ddl.go` | ALTER TABLE ENABLE/FORCE ROW LEVEL SECURITY, called from `alter_table.go:153` |
| `Engine.configureRowSecurityLocked`, `rowSecurityInputLocked` | `rls_base.go` | sets the RLS scope fields, called from `execute_bound.go:398` |
| `checkRowSecurityBinding`, `checkRowSecurityRuleProgram`, `checkRowSecuritySource` | `row_security_setting.go` | privilege walkers (the `access` README keeps them in the root) |
| `Engine.executeRuleProgramLocked`, `admitRuleCommandsLocked`, `admitRuleCTEsLocked`, `normalizeRuleCTEsLocked`, `ruleCommandScratch` | `rule_execution.go`, `rule_admission.go`, `rule_cte.go` | rule program execution runs DML |
| `Engine.freezeRuleActionPresentationLocked`, `freezeRuleModifierPresentationsLocked` | `rule_action_presentation.go` | presentation freezing (with 22a's freeze cut they follow `stored`) |
| `StatementScope.acquire*View*` | `view_locks.go` | lock waits |
| `Engine.openRelationSourceLocked`, `authorizeViewSourceLocked`, `authorizeViewSourceSnapshot` | `view_source.go` | source opening and view-source authorization |
| `Engine.checkViewRow` | `view_check_row.go` | WITH CHECK OPTION at write time (exec runtime) |
| `AuthorizationView.CheckViewWrite`, `StatementScope.acquireViewWriteTarget` | `view_write_authority.go` | privilege walker, lock wait |
| `Engine.attachViewWriteSourcesLocked`, `freezeViewWriteSources`, `frozenViewSource` | `view_write_query.go` | scope source capture |
| `Engine.prepareStoredQueryLocked`, `prepareStoredExecutionRoot` | `view_query.go` | prepares and admits a stored query |
| `Engine.importMaterializedSnapshots`, `importMaterializedSnapshotRow` | `materialized_snapshot.go` | Restore import |
| `materializedSourceFence.currentLocked`, `Engine.materializedSourceFenceCurrent` | `materialized_refresh_concurrent.go` | journal fence |
| `dropProvider.viewClosure`, `viewDependents`, `ruleDependents`, `policyDependents`, `viewDependencyClosure`, `stageViewDependentsLocked` | `view_dependencies.go`, `rule_dependencies.go`, `policy_dependencies.go` | DROP pipeline |
| `Engine.checkTemporaryRuleSourcesReplacementLocked` | `rule_remap.go` | TEMP preservation |
| `Engine.reownCatalogExpressions`, `remapCatalogExpressions`, `remapViewRuleOIDs` | `catalog_expression_reown.go` | the reowner (22c) and Restore remap (step 30) |

### Dispatch sites

All in `execute.go` unless noted: `CreateRule`/`AlterRule`/`DropRule` →
`executeRule` (line 73); `CreatePolicy`/`AlterPolicy`/`DropPolicy` →
`executePolicy` (75); `CreateView` → `executeCreateView` (81); `AlterView`
→ `executeAlterView` (94; the owner action goes to
`executeRelationOwnership`); `DropView` → `executeDropView` (96);
`RefreshMaterializedView` → `refreshMaterializedLocked` (204, under
`Engine.mu`); a rule program → `executeRuleProgramLocked` (527); CREATE
MATERIALIZED VIEW → `prepareQueryTableLocked` (552, shared with CREATE
TABLE AS; table code). RLS application: `execute_bound.go:398`. ENABLE and
FORCE ROW LEVEL SECURITY: `alter_table.go:153` (inside `alterTableLocked`).

### State

- `StatementScope`: `viewCreationSlot *viewCreationScope`,
  `materializedSlot *materializedRefreshScope`, `ruleActionAuth
  *AuthorizationView` (header field, set by `installRuleScratch`), and in
  the embedded `statementMutation` (`cte_mutation_scope.go`): `viewWrite`,
  `rlsTargetDenied`, `rlsProposal`, `rlsMergeUpdate`, `rlsMergeInsert`,
  `rlsProposalTable`.
- `CatalogStage`: `materializedSlot *materializedStage`;
  `viewCascadeNames` (generic DROP cascade notice text, misnamed; used by
  `drop_provider.go`, `extension_drop.go`, `owned_roles.go`, `drop_table.go`,
  `sequence.go`, `domain_dependencies.go` and more).
- Journal fence: `materializedSourceFence` (kind `fenceMaterializedSource`,
  declared centrally in `catalog_fence.go`).
- No `Engine` fields.

### Outside users of the move set

All production users are root files (`execute*.go`, `relation_scope.go`,
`catalog*.go`, `domain_*.go`, `query_*.go`, `transaction*.go`,
`analysis_host.go`, `control.go`, `alter_type.go`, ...), which can import
`view`. No lower package uses it. Two checks for 27b:
`relationDefiningAuthorityCompatible` is also used by
`partition_locks.go`, and `physicalBinding` by `domain_default.go`; both
are root files today. If `partition` or `typedef` code that moves earlier
needs them, move them down instead (`catalog`, `analyze`).

## Part A

### 27a — In place

Re-measure first with `reach -files '^(rule_|view|policy|rls_|row_security|materialized_)'`
and compare with the inventory. Then, one commit per numbered item:

1. **Dead code.** Delete `rewriteRuleOccurrences` (`rule_frames.go`),
   `stagePolicyDependents` (`policy_dependencies.go`) and
   `supportedRestrictError` (`view_dependencies.go`): nothing references
   them. `stageRuleDependents` and `stageViewDependents` are used only by
   tests; delete them and adapt those tests to the production entry point
   (`stageViewDependentsLocked` or the DROP statement), or delete the tests
   if `reference/` covers the behavior.
2. **Split the generic drop diagnostics** into a new root file
   `drop_diagnostics.go`: `dependencyObject`, `dependencyRelationName`,
   `supportedDependent`, `supportedDependentLabels`,
   `supportedDependentLabelsWithPolicies`, `supportedRestrictErrorLabels`
   and `emitViewCascade` from `view_dependencies.go`; `ruleDependentLabels`
   and `ruleDependentLabelsFrom` from `rule_dependencies.go`;
   `policyDependentLabels` and `policyDependentLabelsFrom` from
   `policy_dependencies.go`. Moving only, no edits.
3. **Rename** `CatalogStage.viewCascadeNames` to `dropCascadeNames` (use
   `fieldrn`; about 25 sites).
4. **Rule authority cut** (pattern 3 and §3 rule 7). Declare a root-local
   interface for the authority a rule command carries:

   ```go
   // ruleAuthority is the statement authority a rewritten rule command runs
   // under. AuthorizationView implements it; execution recovers the view
   // with ruleAuthorizationOf.
   type ruleAuthority interface {
       Access() access.View
       AsUser(authorization.PrincipalRef) ruleAuthority // the rule owner
       StorageLocked(ids.ObjectAddress) *storage.Relation // caller holds Engine.mu
   }
   ```

   `ruleCommand.auth`, `ruleCTECommand.caller` and the `auth` parameters of
   `composeRuleAction`, `ruleResidual`, `refreshRuleSources` and
   `refreshRuleActionCTEs` take it. Replace `auth.Catalog` with
   `auth.Access().Catalog`, the `auth.State.CurrentUser = owner` copy with
   `AsUser`, and `auth.engine.authorizationStorageLocked(auth, a)` with
   `auth.StorageLocked(a)`. Add `ruleAuthorizationOf(ruleAuthority)
   AuthorizationView`, a checked helper that panics on a foreign type, and
   use it in `rule_execution.go`, `rule_admission.go` and `rule_cte.go`
   where the full view is needed (`CheckBinding`, `Recapture`,
   `prepareCTECommandsLocked`). Note: §6 step 27 says "take `access.View`";
   the records need the full authority again at execution time, so they
   hold this opaque interface instead.
5. **Rule expansion off `Engine`.** Turn `expandRulesLocked`,
   `expandRuleResidualLocked` and `appendRuleCommand` into methods of a
   small value `ruleRewriter{owner *catalog.Owner}` (or plain functions
   taking the owner). Drop the unused `scope` parameter. Use
   `owner.Domain.Ref` for `e.catalog.Domain.Ref`. The three callers
   (`engine.go:358`, `rule_execution.go:54`, `rule_cte.go:65`) build the
   value from `e.catalog.owner()`.
6. **View-write lowering off `Engine`.** `bindViewWrite`, `lowerViewWrite`
   and `lowerViewInsert` become functions taking `(owner *catalog.Owner,
   view catalog.Read, b *analyze.Binding, stmt parser.Statement, ...)`.
   `bindingHost.BindViewWrite` (`analysis_host.go:313`) passes
   `h.view(auth).Catalog`; the rule expansion passes its own catalog.
7. **Access-check helpers.** `policyPreflight`, `resolvePolicyRoles`,
   `alterViewAuthority`, `viewDropOwner` and `materializedRefreshAccess`
   take `(access.View, access.Environment)` instead of `AuthorizationView`
   (`AuthorizationView.Check` is `v.View.Check(v, req)`, so callers pass
   `auth.View, auth`).
8. **Slots** (pattern 2). The five `rls*` fields of `statementMutation`
   become one value group `rowSecurityMutation` (they are written per
   command, so a value, not a pointer slot); `ruleActionAuth` moves into a
   rule slot with the rule scratch. Keep the save/restore in
   `activateMutation` and the SQL routine scratch correct: copy the group
   as one unit.
9. **Materialized snapshot emitter.** `(*dumpRelation).emitMaterializedSnapshot`
   becomes `emitMaterializedSnapshot(t *dumpRelation, ...)`, so that 27b
   can move it although `dumpRelation` belongs to `table`. Done by table
   24a (`e247d07c6`): skip it.

Re-run `reach` on the same files. Every declaration in the "move" lists
above must now be clean. If one is not, stop and record the path under
"Notes for later steps".

Open questions (answer in the Status record, do not block on them):
`relationDefiningAuthorityCompatible` belongs in `catalog` if 5i has not
taken it; `physicalBinding` may fit `analyze` better (its README lists it
among the execution-side helpers over binding types).

### 27b — Create `view` and move the clean set (broad)

1. Re-run `frontier -files <move-set files> -decls <move-set declarations
   in mixed files>`. OUT may name only `stored`, `table`, `index`,
   `analyze`, `exec`, `plan` and lower packages, plus `sqlError`. Give the
   package its own `errors.go` copy of `sqlError`, as every other
   subpackage has.
2. Export in place with `expspec` (only what the root still uses), then
   move with `movex`/`mv.sh`. Use the names `stored` gave the payloads in
   22c (for example the rule definition and action types); do not invent
   new ones.
3. Imports must be a subset of: `access`, `analyze`, `authorization`,
   `catalog`, `exec`, `expr`, `ids`, `index`, `parser`, `plan`, `regobj`,
   `session`, `sqlstate`, `sqltype`, `storage`, `stored`, `table`,
   `typedef` and the standard library. Never the root, never a feature
   above `view`.
4. Alias policy: qualify every root reference directly (as 14b–17b did).
   Only if a type is named in more than about 300 places may it keep a
   temporary alias; list it under "Outstanding aliases" and remove it in
   27d.
5. Move the unit tests that exercise only moved code (pure rule rewriting,
   view-write lowering, concurrent refresh row matching) into
   `internal/engine/view`. Tests that need an engine fixture stay in the
   root and are rewritten to the new names. Tests never keep code in the
   root (§4 tests rule).

Suggested commits: (1) exports in place, (2) move with qualified
references, (3) tests, (4) docs.

## Part B

### 27c — Refine the host (docs only)

Re-measure the handlers listed under "Dirty declarations". Write the host
interface, the move set and the commit sequence into this file, and wait
for approval. Start from this sketch (draft signatures):

- **Shared DDL host** (from `ddl`, step 20): `AccessView`, `Recapture`,
  `CreationNamespace`, `CheckCreateIn`, `Stage`, `StageRead`,
  `AllocateOID`, `Revision`, `DropTarget`, `RecordDependencyPath`,
  `DependencyObject`, `EmitNotice`, `Context`. Embed `ddl.Host`; do not
  copy these.
- **Relations:** `ResolveRelation(auth ddl.Authority, name parser.ObjectName,
  kind string) (ids.ObjectRef, *catalog.Relation, error)` (today
  `resolveRelationReference`), `StageRowType(*catalog.Relation) error`,
  `DisplaceAssociatedArray`, `AssociatedArrayName` (CREATE VIEW stages a
  row type, as `typedef` does).
- **Binding, done by the root** (the root owns `newExpressionBinder`):
  `BindViewQuery(auth, stmt *parser.CreateView, input) (analyze.Binding,
  error)` (today in `prepareViewCreation`), `BindRuleDefinition(auth,
  relation *catalog.Relation, stmt *parser.CreateRule, input)
  (<stored rule definition>, error)` (today `bindRuleDefinitionLocked`),
  `BindPolicyExpression(auth, relation, raw parser.Expr, input)
  (expr.Expr, []catalog.ExpressionDependency, error)` (today
  `bindPolicyExpression`).
- **Dependents:** `StageExpressionDependents(...)` and
  `DropCascadeNames() []string` for DROP VIEW and DROP RULE/POLICY
  CASCADE (the root's `dropProvider` and `stageViewDependentsLocked` stay
  behind them).
- **Rename and schema moves:** `RenameRelation`, `SetRelationSchema` (they
  are table code; the host forwards to the root or to `table`).

Every method above runs with `Engine.mu` held and never waits. Lock waits
(`view_locks.go`, `acquireViewWriteTarget`, `acquireMaterializedRefresh`)
happen before the handler runs, in `relation_scope.go`, and stay in the
root. REFRESH MATERIALIZED VIEW releases `Engine.mu` to run its query
(`refreshMaterializedLocked` consumes the lock); keep that orchestration in
the root and move only its decisions.

Questions to settle in 27c:

1. Does `view.Host` live on the shared `ddl` adapter (20b), or on a
   `viewHost` over the same fields, as `typedefHost` did before 20b?
2. Which of CREATE OR REPLACE VIEW's checks (column compatibility, check
   option, persistence) are pure decisions over the old and new
   descriptors? List them; they move.
3. The RLS application (`configureRowSecurityLocked`) writes the scope's
   RLS group. Can its decision (which layers and policies apply) be a pure
   function over `access.View`, the relation and the binding, with the
   root only writing the result?

### 27d — Move the handler decisions

Follow 27c. Expected to move: CREATE VIEW descriptor and option decisions,
CREATE OR REPLACE checks, ALTER VIEW option changes, DROP VIEW target
decisions, CREATE/ALTER/DROP RULE and POLICY list changes and their
diagnostics, the ENABLE/FORCE ROW LEVEL SECURITY flag decision, the
REFRESH admission decisions, and the RLS layer selection. Remove any
outstanding aliases.

**Stays in the root after 27d:** dispatch (`execute.go`), `Engine.mu`, lock
waits, `relation_scope.go` admission, rule program execution and admission
(they run DML), view source opening and authorization (`view_source.go`),
`checkViewRow`, refresh row production and staging
(`materializedRefreshRowsLocked`, `stageMaterializedRows`), the
materialized source fence and its kind, `importMaterializedSnapshots`, the
privilege walkers (`CheckViewWrite`, `row_security_setting.go`), the drop
diagnostics and `dropProvider`, the journal and publication.

## Invariants

- **Rule rewrite order.** INSTEAD, ALSO and conditional INSTEAD rules
  expand in the same order, with the same recursion detection (ancestry by
  object and event), the same command count and the same RETURNING
  provider. The composed action runs as the rule owner; the caller's
  authority is restored for the residual.
- **Errors.** Every message, SQLSTATE, detail and hint stays identical,
  including the first error when several rules or policies fail.
- **Locks.** No new lock is taken under `Engine.mu`; no wait happens under
  it. `refreshMaterializedLocked` still consumes `Engine.mu` on every
  return.
- **Publication.** CREATE/ALTER/DROP VIEW, RULE and POLICY still stage and
  publish in one `publishCatalogOperation`; the materialized source fence
  keeps its position in the fence order (`catalog_fence.go`).
- **Concurrent refresh.** The unique-index requirement, duplicate
  detection and source lineage checks behave as before.
- **Dump and Restore.** View, rule and policy dumps (now produced in
  `stored`) and the materialized snapshot comments
  (`stuntgres-matview-v1`) stay byte-identical; Restore decodes them the
  same way.
- **`analyze` stays below.** `analyze` never imports `view`; it keeps its
  host methods.

## Targeted tests

- Engine units: `rule_*_test.go` (10 files), `view*_test.go` (13),
  `materialized_*_test.go` (17), `polic*_test.go` (5), `rls_*_test.go`,
  `row_security*_test.go`. Grep `*_test.go` for `expandRulesLocked`,
  `composeRuleAction`, `makeRuleProducer`, `bindViewWrite`,
  `lowerViewWrite`, `materializedConcurrent`, `viewCascadeNames`. Add
  `RACE=1` for the concurrent refresh tests.
- Reference selections, quick mode and `REFERENCE=1` (never edit
  `reference/` or `smoke/`):
  - `make check RUN='^TestReference(View|Rule|Materialized|RLSBase)'`;
  - `make check RUN='^TestReference(AccessRoleRLS|InheritanceQueryRLS|InheritanceDMLRLS|DriverBatchPolicy)'`;
  - `make check RUN='^TestReferenceDumpRoundTrip$'` and, for 27b,
    `make check RUN='^TestReferenceDeparse'`.

  Compare any `REFERENCE=1` failure with `master` before reporting it; some
  oracle drift is known and unrelated.
- `make bench` is not needed: startup does not change.

## Documentation

- Create `internal/engine/view/README.md` in 27b: what the package owns
  (rule rewriting and programs, writes through views, materialized
  refresh, RLS application, DDL decisions after 27d), what `stored` owns
  instead, its invariants, what stays in the root and why, and a
  `## Testing` section with the selectors above. Move the matching
  selectors out of `internal/engine/README.md`.
- Update the `stored`, `analyze` and `access` READMEs where they say rule
  rewriting, view-write lowering or the view walkers stay in the engine.
- `AGENTS.md`: add `view` to the brace list of the shared subpackage
  bullet. The file must not grow.

## Done when

Part A: the rule rewriting, view-write lowering, materialized refresh and
RLS selection code lives in `view`, which imports nothing above `stored`
and `table`. Part B: the view, rule and policy DDL decisions live there
too; dispatch, `Engine.mu`, lock waits, rule execution and publication stay
in the root. The checks in §8.3 pass.

## Status

- [ ] 27a — In place (dead code, drop diagnostics split, rule and
  view-write authority cuts, slots)
- [ ] 27b — `view` created and clean set moved
- [ ] 27c — Host refined (docs only)
- [ ] 27d — Handler decisions moved

Outstanding aliases: none yet.

### Notes for later steps

- `dml` (29b) needs the rule action records; they are in `stored` (22c),
  not here, so `dml` does not need `view` for them.
- `table` (24) calls the drop diagnostics (`emitViewCascade`,
  `ruleDependentLabels`, `policyDependentLabels`) from ALTER TABLE and
  ALTER TYPE; they stay in the root, so table's Part B reaches them
  through its host.
- The RLS action rules go to `access` in B6; only ALTER TABLE code
  (`alter_table_prepare.go`, `alter_table.go`, `partition_locks.go`,
  `table_inheritance_guards.go`, `control.go`) uses them, so B6 must land
  before `table` moves that code.
