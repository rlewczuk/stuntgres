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
- `rule_definition.go`: `ruleDuplicate`, `ruleIndex`, `ruleMissing`,
  `ruleResultCheck`, `ruleReturning`. (Stored 22c moved `ruleDefinition`,
  `ruleAction`, `ruleEvent` and `ruleEventCode` to `stored` as `Rule`,
  `RuleAction`, `RuleEvent` and the unexported `ruleEventCode`.)
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

### 27c — Refine the host (docs only; linear item 101)

Re-measure the handlers listed under "Dirty declarations", then write the
host interface, the move set and the commit sequence into this file, and
wait for approval. The draft sketch had a `view.Host` embedding `ddl.Host`
plus relation resolution, row-type staging, root-built binders for view
queries, rule definitions and policy expressions, dependent staging and
rename/SET SCHEMA forwarders. Three questions were open: (1) where
`view.Host` lives; (2) which CREATE OR REPLACE VIEW checks are pure; (3)
whether the RLS layer selection can be a pure function.

**27c refine (approved; measured at `116432550`).** `frontier` over the 11
root files that hold the DDL handlers (`view.go`, `view_alter.go`,
`rule_ddl.go`, `rule_definition.go`, `rule_action_presentation.go`,
`policy_ddl.go`, `policy_expression.go`, `view_definition.go`,
`materialized_refresh.go`, `rls_base.go`, `rls_base_ddl.go`; output in
`.cswd/tmp/PG18-ENGINE-SPLIT-VIEW-REFINE/frontier-view.txt`): 27
declarations, 116 engine edges. The root still has 28 view files, about
3.9k lines. Findings:

- **One handler shape.** ALTER VIEW, CREATE/ALTER/DROP RULE and
  CREATE/ALTER/DROP POLICY all run: `Engine.mu` → statement authority →
  resolve the relation (an IF EXISTS miss ends in a fenced NOTICE) →
  compare with `scope.lockedRef` → decisions → `BeginCatalogStage` and
  `authorizeLocked` → unlock → `Replace` unlocked → `ValidateCatalogStage`
  → `publishCatalogOperation`. CREATE VIEW looks its name up in the
  admitted creation namespace and stages `Create`/`Replace`, the row type
  and the array displacement *before* it unlocks, then waits for its
  definition references. DROP VIEW resolves under one window, waits, checks
  again under a second and stages unlocked. The decisions read only
  `auth.Catalog`, `auth.Check`, `e.catalog.Domain.Ref`, OID and revision
  allocation, `resolveRelationReference(auth, name, true)`, the binder, and
  for CREATE VIEW the row-type, array-displacement and type-reown staging.
- **The shared DDL host covers most edges.** `AccessView` gives `Catalog`,
  `Check` (with the authority as environment), `Owner.Domain` and
  `State.CurrentUser`; `Allocator` is `allocateOIDLocked` and
  `revisionLocked`; `Stage()`, `CreationACL`, `StageRowType` and
  `DisplaceAssociatedArray` are already on `ddlHost`; role's
  `ResolveRelation` is exactly the utility `resolveRelationReference`.
- **Restore guard.** CREATE VIEW runs `CheckCreate`, then, whenever the
  operation is a trusted table destination, the superuser guard (its error
  wins), and only then drops a 42501. `CheckCreateIn` guards only after a
  42501, so it is not the same check; `TrustedRestoreDestination(auth,
  "destination")` is. The guard stays in the root header (below).
- **Freezing.** `freezeViewDefinitionLocked`,
  `freezeRuleActionPresentationLocked` and
  `freezeRuleModifierPresentationsLocked` are dirty only through
  `catalogSourceTemporaryOwner(auth)` (the session token) and one
  `allocateOIDLocked`. `freezeRuleActionPresentationLocked` calls
  `view.RetainRuleCTEPresentation`, so the freezers go to `view`, not
  `stored`.
- **`creationError`** (`engine.go`) is pure (`sqlstate`, `parser`) and has
  9 callers: `create_table.go` (5), `fdw_table.go`, `query_table_create.go`,
  `relation_scope.go`, `rule_definition.go`. Rule definitions need it in
  `view`, table 24d.1 in `table`.
- **`prepareViewCreation` is admission**, not a handler: it binds with the
  scope's lock admission, attaches CTE view targets, revalidates the query
  sources and writes the `view.CreationScope` slot. It stays, so the
  sketch's `BindViewQuery` is not needed.
- **DROP VIEW stages unlocked and waits**: `stage.Remove`,
  `stageExpressionDependents` (it waits) and the AccessExclusive locks on
  cascaded views all run after `Engine.mu` is released. So the sketch's
  `StageExpressionDependents` and `DropCascadeNames` are not needed; the
  root keeps that orchestration. ALTER VIEW RENAME and SET SCHEMA dispatch
  to the root's `renameRelationLocked` and `alterSchemaLocked` (table 24c
  keeps both), so `RenameRelation` and `SetRelationSchema` are not needed
  either.
- **ENABLE/FORCE ROW LEVEL SECURITY.** The flag decision is already
  `access.RowSecurityFlags` (B6). `stageRowSecurityFlagsLocked` only
  installs it: catalog authority, journal clone, revision bump, stage,
  `catalogChangeLocked`. Nothing is left to move.
- **REFRESH MATERIALIZED VIEW.** Its admission decisions moved in 27b
  (`MaterializedRefreshAccess`, `MaterializedRefreshCompatible`,
  `MaterializedConcurrentAdmission`, `MaterializedEligibleIndexes`,
  `MaterializedConcurrentRows`). Left in the root: the concurrent
  live-version filter of `refreshMaterializedLocked` and the replacement
  record checks (`validateMaterializedReplacement`,
  `materializedReplacementCurrent`), which read only the record, the staged
  descriptor and the stage base.
- **RLS.** `configureRowSecurityLocked` writes the five fields of
  `statementMutation.rls` from the binding, the statement kind, the roles,
  the catalog, the `row_security` setting and the current user; it writes
  some fields before it can fail. 14 root uses read the group, in
  `execute.go`, `execute_bound.go`, `insert_query.go`, `merge_rows.go`,
  `mutation_targets.go`, `cte_mutation_scope.go` and `rls_base.go`.
- **27b record correction.** Its Status line said "`REFERENCE=1` pending";
  `.cswd/tmp/PG18-ENGINE-SPLIT-VIEW-PACKAGE/ref-real.log` shows the
  selections passed (rc=0) before the merge (`116432550`).

Decisions:

1. **Two hosts on the shared `ddlHost`** (question 1), like role 28c's
   `role.Host`/`role.StageHost`. Every method of both runs with `Engine.mu`
   held, never takes or releases it, and never waits. New root file
   `view_host.go` with `var _ view.Host = ddlHost{}` and `var _
   view.StageHost = ddlHost{}`.
   - `view.Host`, for the decisions made before a stage exists (the root
     passes `ddlHost{e: e, op: scope.op}`, no stage):
     - `ddl.Allocator`, `Context() context.Context`,
       `AccessView(ddl.Authority) access.View`;
     - `ResolveRelation(auth ddl.Authority, name parser.ObjectName)
       (ids.ObjectRef, error)`: role's method, declared again;
     - `TemporaryOwner(auth ddl.Authority) ids.SessionToken`
       (`catalogSourceTemporaryOwner`);
     - binding, by the root (rule 6): `DescribeRule(auth, stmt
       parser.Statement, target ids.ObjectRef, input
       sqltype.InputEnvironment, ns *analyze.RuleNamespace)
       (analyze.Binding, error)` (`describeWithRule` over
       `analysisHost(op)`, no lock admission; both the ON SELECT
       `CreateView` and each rule action use it);
       `BindRuleCondition(auth, ns *analyze.RuleNamespace, raw parser.Expr,
       input) (expr.Expr, *parser.Select, analyze.Binding, error)` (the
       binder block of `bindRuleDefinitionLocked`: the bound condition, the
       synthetic SELECT and its capture binding with frozen attributes);
       `BindPolicyExpression(auth, relation *catalog.Relation, raw
       parser.Expr, input) (*stored.Policy, error)` (today
       `bindPolicyExpression`, body whole in the root like
       `BindDomainDefault`; nil for a nil expression).
   - `view.StageHost`, for CREATE VIEW staging (`ddlHost{e, op, stage}`):
     `Host` + `ddl.Host` + `StageRowType(*catalog.Relation) error` +
     `DisplaceAssociatedArray(namespace ids.ObjectAddress, name string)
     (bool, error)` + new `ReownDisplacedTypes() error` (no-op when
     `stage.OldNameCount()` is zero, else `reownStagedTypeGenerationsLocked`;
     `query_table_create.go` has the same pair for CREATE MATERIALIZED
     VIEW).
   - Not needed from the sketch: `BindViewQuery`, a relation-returning
     `ResolveRelation(…, kind)`, `AssociatedArrayName`,
     `StageExpressionDependents`, `DropCascadeNames`, `RenameRelation`,
     `SetRelationSchema`, `RecordDependencyPath` for DROP (the root keeps
     `referencePath` with its stage).
2. **CREATE OR REPLACE VIEW** (question 2). Three functions, in today's
   order:
   - `CreateTarget(v access.View, env access.Environment, s
     *parser.CreateView, ns, locked ids.ObjectRef) (*catalog.Relation,
     error)`: the existing relation, 42P07 without OR REPLACE,
     `ErrCatalogReplaced` against `locked` both ways, owner check, 42809
     "is not a view";
   - `CheckCreate(view catalog.Read, s *parser.CreateView, b
     analyze.Binding, old *catalog.Relation) (CreateOptions, error)`: the
     check-option declaration (`stored.ValidateViewCheckDeclaration`),
     `CheckViewOutputNames` and `CheckViewPrefix` against `old`, the
     `record`/`record[]` pseudo-type 42P16, the VOID boundary
     (`analyze.CheckViewOutputTypes`, OR REPLACE only), `stored.ViewOptions`
     (barrier, invoker, check mode), and output names for a new view;
   - `StageCreate(h StageHost, auth ddl.Authority, s *parser.CreateView,
     old *catalog.Relation, ns ids.ObjectRef, b analyze.Binding, options
     CreateOptions, temporary bool) error`: array displacement and its
     42710 (new views), the relation and rule OIDs (or the old ones), the
     revision, `FreezeDefinition`, the descriptor (fresh or copied from
     `old`), the creation ACL, the temporary consumer, attribute numbering
     and column origins, `StageRowType`, `Create`/`Replace`, then
     `ReownDisplacedTypes` for new views.
   The root keeps the header (`validateLockedRelation`, authority, the
   private catalog and `stagedTemporary` of a temporary creation,
   `CheckCreate` with the restore guard), `validateQuerySources`, the count
   check, stage begin and authorization, the unlock,
   `acquireViewDefinitionReferences`, the replacement-read release and the
   overlay or publication. Persistence and the namespace
   (`relationCreationNamespaceLocked`, `adjustRelationCreationPersistence`)
   are admission and stay. Optional: `prepareViewCreation`'s 0A000
   (modifying WITH), the temporary-view NOTICE text and the 42P22 collation
   check become `view.CheckCreationBinding`.
3. **RLS selection** (question 3): yes. `rowSecurityMutation` moves to
   `view` as `RowSecurity` with exported fields (`TargetDenied`,
   `Proposal`, `MergeUpdate`, `MergeInsert`, `ProposalTable`);
   `statementMutation.rls` keeps its name and holds `view.RowSecurity`.
   `configureRowSecurityLocked` becomes:
   - `view.RowSecurityTarget(b analyze.Binding, stmt parser.Statement)
     (RowSecurity, catalog.PolicyCommand, bool)`: the reset, the command,
     `ProposalTable`, and whether RLS applies (false for a non-DML
     statement, no target or RLS disabled);
   - `view.SelectRowSecurity(r RowSecurity, v access.View, enabled bool, b
     analyze.Binding, stmt parser.Statement, command
     catalog.PolicyCommand) (RowSecurity, error)`: the layers, the setting
     guard, the target decision and the proposal groups. It returns the
     partly written value with its error, and the root assigns it either
     way, as today.
   The root keeps the statement authority, the rule-action current user,
   the private catalog and the recapture. `rowSecurityInputLocked` keeps
   its authority setup; its core becomes `view.RowSecurityInputDenied(v
   access.View, enabled bool, d *catalog.Relation, principal
   authorization.PrincipalRef, command catalog.PolicyCommand, selectRead
   bool) (bool, error)`.
4. **Rules.** `view.PlanRule(h Host, auth ddl.Authority, stmt
   parser.Statement, locked ids.ObjectRef, input sqltype.InputEnvironment)
   (RelationChange, error)` is `executeRule` from the name switch to the
   QuoteRevision bump: resolve (an IF EXISTS miss becomes `Skip`),
   `ErrCatalogReplaced`, the missing-rule skip and 42704, the owner check,
   the relation-kind checks, the ON SELECT path (over `DescribeRule`,
   `RuleResultCheck`, `FreezeDefinition`), the CREATE/ALTER/DROP list
   change, the sort and the revision.
   `RelationChange{Skip string; Ref ids.ObjectRef; Next *catalog.Relation;
   Sources []catalog.Descriptor}`: `Skip` is the NOTICE text that the root
   passes to `finishPolicyNoticeLocked`; `Sources` are the relations the
   stage records as `querySources`; `Ref.Epoch` is the publication token.
   `bindRuleDefinitionLocked` becomes the unexported `bindRuleDefinition(h,
   auth, d, ref, s, input)` over `BindRuleCondition`, `ResolveRelation`
   (with `ddl.CreationError`) and `DescribeRule`; its 0A000, 42P02 and
   RETURNING checks move with it. The freezers move: `FreezeDefinition(a
   ddl.Allocator, view catalog.Read, owner ids.SessionToken, syntax
   *parser.Select, b analyze.Binding, barrier, invoker bool, rule ids.OID,
   kind catalog.RelationKind, temporary bool) (*stored.View, error)`
   (exported: CREATE MATERIALIZED VIEW in `query_table_create.go` stays a
   root caller and passes `ddlHost{e: e}`), and the unexported
   `freezeRuleActionPresentation` and `freezeRuleModifierPresentations`
   over `(catalog.Read, ids.SessionToken)`.
5. **Policies.** The root keeps `PolicyPreflight` (it takes the operation
   for its WARNING; the handler passes `notices=false`) and then calls
   `view.PlanPolicy(h Host, auth, stmt, locked, input) (RelationChange,
   error)`: `ResolvePolicyRoles`, resolve (the relation and schema skip
   texts), `ErrCatalogReplaced`, the missing-policy skip and 42704, the
   owner check with `PolicyOwnerNoun`, the catalog and kind checks,
   `BindPolicyExpression` for USING then CHECK, the list change with its
   OID, `PolicyClauses` for ALTER, the sort, the revision and the
   presentation sources.
6. **ALTER and DROP VIEW.**
   - `view.AlterChange(a ddl.Allocator, view catalog.Read, old
     *catalog.Relation, s *parser.AlterView) (*catalog.Relation,
     *catalog.Extension, error)`: DEPENDS ON EXTENSION, SET and RESET with
     their checks and the 0A000 default; a nil relation means no change (no
     revision is allocated), the extension is the stage's query source.
     The root keeps resolution with its fenced IF EXISTS NOTICE,
     `AlterViewAuthority`, the replacement-read release, the RENAME and SET
     SCHEMA dispatch, the stage and publication.
   - `view.DropTargets(h Host, auth, s *parser.DropView) ([]ids.ObjectRef,
     []sqlstate.Error, error)`: the resolve loop with `ViewDropMissing` and
     its NOTICE texts, the 42809 hints, `ViewDropOwner`, de-duplication.
     `view.RecheckDropTargets(v access.View, env access.Environment, refs
     []ids.ObjectRef) error`: the post-wait loop (missing relation or
     foreign domain is `ErrCatalogReplaced`, then `ViewDropOwner`). The root
     keeps the sort, the AccessExclusive waits, the epoch check, the stage,
     `referencePath`, removal, `stageExpressionDependents`, the cascade
     locks, publication and the cascade NOTICEs.
7. **REFRESH MATERIALIZED VIEW.** `view.ConcurrentPrevious(previous
   storage.Relation, own []storage.RowVersion, deletes
   *visibility.PrivateDeletes, frontier ids.CommitSeq) storage.Relation` (the
   live-version filter; the root keeps `index.BuildIndexEntries`, which
   needs the statement host). `view.ValidateMaterializedReplacement(r
   *MaterializedReplacement, staged catalog.Descriptor, base catalog.Read)
   error` and `(*MaterializedReplacement).Current(staged
   catalog.Descriptor, base catalog.Read, snapshot *catalog.State, stored
   *storage.Relation) error`; the `CatalogStage` methods stay as root
   wrappers, so their callers (`catalog.go`, `catalog_message.go`,
   `materialized_refresh_concurrent_test.go`) do not change.
   `acquireMaterializedRefresh`, `refreshMaterializedLocked`,
   `materializedRefreshRowsLocked` and `stageMaterializedRows` stay
   (admission waits, the consumed lock, the stored query, storage
   allocation and stage internals).
8. **Dependents** (optional pure cores; `dropProvider` stays):
   `view.DependencyClosure(views []*catalog.Relation, targets
   []ids.ObjectAddress) []DependencyEdge` (the root collects the live views
   in OID order), `view.KeepRules(base catalog.Read, rules []stored.Rule,
   targets []ids.ObjectAddress) []stored.Rule` and
   `view.KeepPolicies(policies []catalog.PolicyDefinition, targets
   []ids.ObjectAddress) []catalog.PolicyDefinition`. `viewClosure`,
   `ruleDependents` and `policyDependents` stay `dropProvider` methods.
9. **Hand-offs.** `creationError` becomes `ddl.CreationError` (next to
   `ddl.Check`); table 24d.1 (item 89a) needs it too, so whichever of 89a
   and 27d lands first moves it. CREATE MATERIALIZED VIEW
   (`prepareQueryTableLocked`, table 24c decision 8) calls
   `view.FreezeDefinition` and may use `ReownDisplacedTypes`.
   `viewLeafRowMark` stays in the root (`query_lock_admission.go`; table
   24c's note about `inheritanceQueryLeaves`): it is pure over `stored.View`
   and `plan.RowMark`, so `stored` can take it if table needs it. DML 29c
   composes `view.RowSecurity` and `SelectRowSecurity`; `execute_bound.go`
   stays the caller. Control 30c: `view` has no Restore hook body to move;
   `importMaterializedSnapshots` stays in the root.
10. **27d stays one item** (linear item 102; about 0.9k lines; not broad).

### 27d — Move the handler decisions (linear item 102)

Follow decisions 1–9 above.

**Moves to `view`:** `PlanRule` with `bindRuleDefinition`,
`FreezeDefinition`, `freezeRuleActionPresentation`,
`freezeRuleModifierPresentations` and `RelationChange`; `PlanPolicy`;
`CreateTarget`, `CheckCreate` with `CreateOptions`, `StageCreate`;
`AlterChange`; `DropTargets`, `RecheckDropTargets`; `RowSecurity`,
`RowSecurityTarget`, `SelectRowSecurity`, `RowSecurityInputDenied`;
`ConcurrentPrevious`, `ValidateMaterializedReplacement`,
`MaterializedReplacement.Current`; optionally `CheckCreationBinding`,
`DependencyClosure`, `KeepRules`, `KeepPolicies`. `creationError` goes to
`ddl` (unless 89a moved it).

**Stays in the root after 27d:** dispatch (`execute.go`), `Engine.mu`, lock
waits, `relation_scope.go` admission and `prepareViewCreation`, the handler
skeletons (`executeCreateView`, `executeAlterView`, `executeDropView`,
`executeRule`, `executePolicy`, `finishPolicyNoticeLocked`) with their
authority setup, stage lifecycle, unlocked staging and publication, the
host bodies (`DescribeRule`, `BindRuleCondition`, `BindPolicyExpression`),
rule program execution and admission (they run DML), view source opening
and authorization (`view_source.go`), `checkViewRow`, REFRESH admission and
`refreshMaterializedLocked` with refresh row production and staging
(`materializedRefreshRowsLocked`, `stageMaterializedRows`), the
`CatalogStage` replacement wrappers, the materialized source fence and its
kind, `importMaterializedSnapshots`, `stageRowSecurityFlagsLocked`,
`configureRowSecurityLocked` and `rowSecurityInputLocked` as authority
wrappers, the privilege walkers (`CheckViewWrite`,
`row_security_setting.go`), the drop diagnostics and `dropProvider`, the
journal and publication.

**Commit sequence:**

1. `creationError` → `ddl.CreationError` (skip if 89a did it).
2. Seam: `view/host.go` (`Host`, `StageHost`, `RelationChange`) and root
   `view_host.go` (the new `ddlHost` methods; `bindPolicyExpression` takes
   the operation and input instead of the scope). Unit test for
   `ReownDisplacedTypes` being a no-op without renamed names.
3. Port in place: the decisions become root-local functions over the two
   hosts (`planRule`, `planPolicy`, `createViewTarget`, …, the freezers over
   `(catalog.Read, ids.SessionToken)`); the handlers call them; no
   behavior or error-order change.
4. Move them into `view` (`movex -qual view`), with `RelationChange`.
5. RLS: `view.RowSecurity` and the three selection functions; the 14 field
   uses renamed.
6. REFRESH cores and, optionally, the dependents cores and
   `CheckCreationBinding`.
7. Docs (README, this file, STATUS, ENGINE-SPLIT.md).

Tests: `policy_test.go` calls the host's `BindPolicyExpression`;
`inheritance_query_stored_view_test.go`, `intagg_retention_test.go`,
`materialized_view_test.go` and `temporary_view_test.go` call
`view.FreezeDefinition`. Verify with the targeted tests and reference
selections below (quick and `REFERENCE=1`), and quickcheck.

### 27e — Query preflight moves to `view` (linear item 115)

Planned by the end-state review (linear item 114, ENGINE-SPLIT.md §2.8 and
§6 "Third round"), measured at `17e01ca8b`. Needs —.

`query_preflight.go` (574 lines), `query_set_preflight.go` (135),
`query_join_legality.go` (406) and `prepareStoredExecutionRoot`
(`view_query.go:146`) are reach-clean. They import `stored`
(`MapExpressionReader`, `ViewOf`, `QuerySetNode`), `table`
(`AdmitColumnVirtualGeneration`) and `view` (`RuleQueryProducerDemand`), so
they cannot go to `analyze`; `view` imports all of them. Frontier: the root
uses only `prepareStoredExecutionRoot`, `viewRowLockLegality` and
`sqlError` (`view` has its own).

- The row-mark readers `viewHasStoredRowMarks`, `viewRowLockLegality` and
  `viewLeafRowMark` (`query_lock_admission.go`) read only `stored.View`,
  `analyze`, `exec` and `plan`: they go to `stored` (analyze 12f planned
  that), so `table` (24g) can call `viewLeafRowMark` too.
- `enginePreparation.PreflightQuery` and `AdmitExpression`
  (`exec_preparation.go`) become forwarders. The DML preflights
  (`preflightUpdate*`, `preflightMerge`, `preflightConflictUpdate`) call the
  moved functions and move later (dml 29e, item 132).
- Before renaming, grep `reference/` and the engine tests for frame names of
  the moved functions (the cancellation fixtures match frames).
- Docs: correct the `exec` README "What stays" (query preflight blockers).

Verify with `make build`, the targeted tests below plus the unit tests of
the touched files, the quick and `REFERENCE=1` selections for the area, and
quickcheck; no behavior, error text or error-order change, no aliases.

### 27f — Privilege walkers and query table refs (linear item 116)

Planned by the end-state review (linear item 114, ENGINE-SPLIT.md §2.8 and
§6 "Third round"), measured at `17e01ca8b`. Needs 27e (item 115).

- `CheckViewWrite` (`view_write_authority.go:20`), `checkViewRowMarks`
  and `checkViewRowMarksExpanded` (`query_lock_admission.go:153-209`),
  `authorizeViewSourceSnapshot` (`view_source.go`) and `checkTruncate`
  (`truncate.go:21`) take an `access.View` and its `access.Environment`
  instead of `*AuthorizationView` (cut tested with `reach -cut`). The root
  keeps the one-line `AuthorizationView.CheckViewWrite`, which implements
  `analyze.Privileges`.
- `Engine.queryTableRefsLocked` (`query_table_refs.go`, 134 lines) becomes
  `view.QueryTableRefs` over a `catalog.Read`, the catalog domain and owner
  and an authorize callback; the replaced-catalog check stays in a root
  wrapper. It needs `stored.ViewLeafRowMark` (27e, done).

Verify with `make build`, the targeted tests below plus the unit tests of
the touched files, the quick and `REFERENCE=1` selections for the area, and
quickcheck; no behavior, error text or error-order change, no aliases.

### 27g — The DROP read side moves to `view` (pattern 6) (linear item 117)

Planned by the end-state review (linear item 114, ENGINE-SPLIT.md §2.8 and
§6 "Third round"), measured at `17e01ca8b`. Needs —.

`dropProvider` is the largest remaining root hub (1.4k dirty lines reach
the god structs through it, mostly its own methods). Its read side is
decisions over the drop stage:

- `dropProvider.DependencyClosure` (`domain_dependencies.go:34`, 392
  lines), `restrictError` (65), `policyDependents`, `ruleDependents`,
  `viewClosure`, the diagnostic half of `alterHost.TypeChangeDependents`
  (`alter_host.go:106-147`) and all of `drop_diagnostics.go` (334, clean:
  `dependencyObject`, the dependent labels, `emitViewCascade`).
- They read the stage through a view-local reader (`Base`, `Path`,
  `Removed`, `Replacement`, `Revision`, `Descriptor`, `Descriptors`,
  `DescriptorCount`, `InputDescriptors`, `Replace`, ...) that `dropProvider`
  implements, as it implements `table.DropDependencyReader` (whose
  `DependencyClosure` then forwards). Reach with those edges cut: both
  methods and the diagnostics are clean.
- First commit: `fdw.Label` (`fdw/ddl.go:35`) and `partition.DependsOn`
  (`partition/drop.go:8`) go down to `catalog`, because `view` imports
  neither package.
- Last commit: `dependencyDescription` (`roles.go:130`, 84 lines) goes to
  `role`, with the reference path passed in instead of
  `auth.engine.referencePath`.
- Stays root: the staging, removal and lock-wait core of `dropProvider`,
  `ExpressionDependencies` (lock waits) and `drop_table.go`.

Verify with `make build`, the targeted tests below plus the unit tests of
the touched files, the quick and `REFERENCE=1` selections for the area, and
quickcheck; no behavior, error text or error-order change, no aliases.

### 27h — Lock walks and materialized checks (linear item 118)

Planned by the end-state review (linear item 114, ENGINE-SPLIT.md §2.8 and
§6 "Third round"), measured at `17e01ca8b`. Needs —.

- The closure walk of `acquireViewSources` (`view_locks.go:40-111`) and
  the collection of `acquireViewDefinitionReferences` (`:224-249`) become
  `view` functions that return what to acquire; the acquire loops stay.
- `normalizeRuleCTEsLocked` (`rule_cte.go`) reads only the catalog owner:
  pass it in.
- The parse and shape checks of `importMaterializedSnapshots`
  (`materialized_snapshot.go:64-98`) and the row-width, type and index-count
  checks of `stageMaterializedRows` (`materialized_refresh.go`).

Verify with `make build`, the targeted tests below plus the unit tests of
the touched files, the quick and `REFERENCE=1` selections for the area, and
quickcheck; no behavior, error text or error-order change, no aliases.

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
  `row_security*_test.go`. Grep `*_test.go` for `ruleRewriter`,
  `composeRuleAction`, `makeRuleProducer`, `bindViewWrite`,
  `lowerViewWrite`, `materializedConcurrent`, `dropCascadeNames`. Add
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

- [x] 27a — In place (dead code, drop diagnostics split, rule and
  view-write authority cuts, slots) (`87bfbb497`, `16260a489`, `a0254a5c3`,
  `57c781f25`, `7472271b6`, `a5e32e52a`, `512cf3e1c`, `24b0bfde4`, then the
  docs commit). `reach` on
  `^(rule_|view|policy|rls_|row_security|materialized_)`: 253 clean of 322
  (243 of 330 before; the drop diagnostics left the matched files). Every
  declaration in the 27b move lists is clean, except
  `relationDefiningAuthorityCompatible`, which 5i had already moved to
  `catalog.RelationDefiningAuthorityCompatible`. Verified with
  `^Test(Rule|View|Materialized|Polic|RLS|RowSecurity|TemporaryTable|CTE)`
  (rule, RLS and CTE units also with `-race`), the three reference
  selections below in quick and `REFERENCE=1` mode, and quickcheck;
  racecheck/finalcheck skipped at user request. Tests: new
  `rule_authority_test.go` (`AsUser` and `ruleAuthorizationOf`);
  `rule_dependency_closure_test.go` and `temporary_table_cleanup_test.go`
  now call `dropProvider.applyViewDependents`; `rule_test.go`,
  `rule_source_authority_test.go`, `view_check_test.go`,
  `view_write_test.go`, `materialized_refresh_authorization_test.go` and
  `policy_restore_authorization_test.go` use the new signatures. No test
  moved or deleted.
- [x] 27b — `view` created and clean set moved (`03468b5b6`,
  `d7cd77e1a`, `b61bed3bb`, `73c98d60b`, then the docs commit) · selected
  unit proof, quickcheck and the `REFERENCE=1` selections passed;
  racecheck/finalcheck skipped at user request; see the 27b record below
- [x] 27c — Host refined (docs only; the refine is in Part B above; the
  docs commit only) · see the 27c record below
- [x] 27d — Handler decisions moved (`955d07306`, `e31c14c82`,
  `c0c3de8f8`, `d74446d38`, `41d0df237`, `bc76cde87`, then the docs commit)
  · selected unit, `RACE=1` concurrent refresh and dual-mode proof and
  quickcheck passed; racecheck/finalcheck skipped at user request; see the 27d record below
- [x] 27e — Query preflight moved (linear item 115) (`eb135c0fe`,
  `ad9c2b8ac`, then the docs commit) · selected unit and dual-mode proof and
  quickcheck passed; racecheck/finalcheck skipped at user request; see the
  27e record below
- [ ] 27f — Privilege walkers and query table refs moved (linear item 116)
- [ ] 27g — DROP read side moved (linear item 117)
- [ ] 27h — Lock walks and materialized checks moved (linear item 118)

Outstanding aliases: none (27b added none).

### 27b record

Done on `implement-part/PG18-ENGINE-SPLIT-VIEW-PACKAGE`, started at
`bdba4968a` (table 24b merged), replayed onto `0ab96a3a8` (index 23d.1:
the export commit conflicted; the scripts `c1.sh`, `c2.sh`, `c3.sh`,
`c3-post.sh` and `c4.sh` in `.cswd/tmp/PG18-ENGINE-SPLIT-VIEW-PACKAGE/`
rebuilt the commits, and the moved files came out byte-identical), and
replayed the same way onto `bd79e7663` (table 24c and partition 25b; the
move set, its OUT and IN edges and the moved files were unchanged).

| Commit | Change |
|---|---|
| `03468b5b6` | Staging: the clean declarations of 17 mixed files to `vwmv_*.go` (`movedecl`), with `bindingDefaultOrigin` |
| `d7cd77e1a` | Export in place (`expspec`/`movex`, 135 rows): `viewWrite.base` is `BaseRelation` (`Base()` is the `analyze.ViewWriteSlot` method); `ruleRewriter.owner` is `Owner` (root callers build it with an unkeyed literal) |
| `b61bed3bb` | Move: 35 files (about 4.8k lines) to `view`, `view/errors.go`, `movex -qual view`, 21 root locals named `view` renamed after their type (`catalogView`, `dataView`, `createView`, `viewRelation`, `storedView`), `keylit` for 13 unkeyed literals, doc comments renamed |
| `73c98d60b` | `binding_slots_test.go` moves to `view` |

- **Re-measure.** `reach` on
  `^(rule_|view|policy|rls_|row_security|materialized_)`: 190
  declarations (22b/22c had taken the presentation set). Every declaration
  in the 27b move lists was clean. The `frontier` fixed point: 126
  members, OUT `sqlError` alone, 154 IN keys.
- **Members the plan did not list:** `binding_slots.go` (the `viewWrite`
  slot methods, `viewWriteOf`, `viewWriteSlotOf`, `ruleCTECommands`,
  `ruleCTEsOf`, `ruleCTEsSlotOf`),
  `bindingDefaultOrigin` (`lowerViewInsert` calls it and it calls
  `physicalBinding`), `rule_update_output.go` (29a's file),
  `policy_payload.go` (`policyPayloadOf`) and `materialized_alter.go`'s
  `materializedExtensionDepends`.
- **Stayed in the root:** `prepareStoredExecutionRoot` (it calls the root
  `preflightQuery`), `materializedSourceFence` and its methods,
  `rowSecurityMutation` and `ruleActionScope` (clean, but scope state the
  root writes), `ruleAuthorizationOf` and the three `AuthorizationView`
  methods, the `CatalogStage`/`StatementScope` slot accessors, and every
  handler under "Dirty declarations".
- **Imports.** `view` also imports `functions`, `sequence` and
  `visibility`, which the 27b import list above omits; all three are lower
  layers that `table` imports too. It imports no `regobj`, `partition`,
  `dml`, `control` or root package (`go list`).
- **Names.** Types lose a stuttering view prefix (`viewWrite` is `Write`,
  `viewWriteLayer` is `WriteLayer`, `viewCheckGroup` is `CheckGroup`,
  `viewCreationScope` is `CreationScope`, `viewDependencyEdge` is
  `DependencyEdge`); everything else the root names is capitalized.
  Package-internal names keep their case (`viewWriteOccurrence`,
  `ruleAncestry`, `lowerViewWrite`, ...).
- **Tests.** `binding_slots_test.go` moved. Every other view, rule, policy
  and materialized-view unit test builds an engine (`New`,
  `AuthorizationView`, the catalog owner) and stays in the root, naming
  `view.X`. None deleted.
- **Checks.** String-literal multiset unchanged apart from
  `view/errors.go`; a Dump→Restore→Dump smoke (seven scenarios, one with
  check-option views, conditional INSTEAD rules, policies and a
  concurrently refreshed materialized view) printed identical output on
  master and the branch, and every restore round-tripped byte-identically;
  quickcheck passed; quick-mode reference suite passed (in quickcheck);
  the `REFERENCE=1` selections passed before the merge (`116432550`;
  `.cswd/tmp/PG18-ENGINE-SPLIT-VIEW-PACKAGE/ref-real.log`, corrected in
  27c). racecheck, finalcheck and bench skipped at user request (no startup
  change).

### 27c record

Done on `implement-part/PG18-ENGINE-SPLIT-VIEW-REFINE` from `116432550`
(view 27b merged), rebased onto `bbc6d23a8` (partition 25c and control
30b, both docs only; the measured code did not change). Docs only: the refine (findings and decisions 1–10) is
in Part B above, and 27d has its move set and commit sequence. No code
commit. Deviations from the notes and the sketch:

- `rowSecurityMutation` moves to `view` as `RowSecurity` after all
  (decision 3): it holds only `access` values, and the root assigning a
  `view` value is simpler than copying five fields. `ruleActionScope`
  stays root scope state (it holds an `*AuthorizationView`).
- The sketch's binder for view queries, the dependent-staging and
  rename/SET SCHEMA methods are not needed (findings); the rule definition
  binder splits into `DescribeRule` and `BindRuleCondition` so that the
  rule DDL checks and the presentation freezing move.
- The 27b record said the `REFERENCE=1` selections were pending; they had
  passed before the merge. Corrected above and in STATUS.

### 27d record

Done on `implement-part/PG18-ENGINE-SPLIT-VIEW-DECISIONS`, started at
`949c682a1` (index 23d.2 merged) and rebased onto `a8f6930c8` (table
24d.1 and DML 29b). The rebase conflicted only in the import blocks of
`create_table.go` and `relation_scope.go`; the 11 measured handler files
had not changed since 27c measured them.

| Commit | Change |
|---|---|
| `955d07306` | `creationError` → `ddl.CreationError` (`ddl/helpers.go`), 9 root callers; 89a had not moved it |
| `e31c14c82` | Seam: `view/host.go` (`Host`, `StageHost`, `RelationChange`), root `view_host.go` (`TemporaryOwner`, `DescribeRule`, `BindRuleCondition`, `ReownDisplacedTypes`); `bindPolicyExpression` becomes `ddlHost.BindPolicyExpression` over the operation and input |
| `c0c3de8f8` | Port in place: the decisions as root functions over the hosts, the freezers over `(catalog.Read, ids.SessionToken)`; RULE and POLICY share `publishRelationChangeLocked` |
| `d74446d38` | Move: 7 files to `view` (`create.go`, `alter_drop.go`, `rule_ddl.go`, `rule_bind.go`, `policy_ddl.go`, `definition.go`, `rule_action_presentation.go`), names exported, no aliases |
| `41d0df237` | RLS: `view.RowSecurity` (`row_security.go`) with `RowSecurityTarget`, `SelectRowSecurity`, `RowSecurityInputDenied`; the field uses renamed |
| `bc76cde87` | `view.ConcurrentPrevious`, `ValidateMaterializedReplacement`, `MaterializedReplacement.Current` (`materialized_replacement.go`); `DependencyClosure`, `KeepRules`, `KeepPolicies` (`dependents.go`) |

Deviations from decisions 1–9:

- **Splits.** `PlanRule` calls the unexported `planSelectRule` for ON
  SELECT, and `AlterChange` calls `alterViewOptions` for SET and RESET, to
  keep the functions short. The RULE and POLICY handler tails were the same,
  so the root has one `publishRelationChangeLocked`.
  `RelationChange.Sources` is `[]catalog.Descriptor`: the relations of the
  ON SELECT or action bindings and the condition presentation, and the
  descriptors the policy presentations read.
- **Host bodies.** `BindPolicyExpression` is the old body as a `ddlHost`
  method (`policy_expression.go`), with no forwarder; the rule condition
  binder block is `BindRuleCondition` (`view_host.go`).
- **RLS.** `RowSecurityTarget` also takes the current `RowSecurity`: a
  statement with no target, or one that is not INSERT, UPDATE, DELETE or
  MERGE, kept its `ProposalTable` before, and still does.
- **REFRESH.** `ValidateMaterializedReplacement(r, stage)` and
  `r.Current(stage, snapshot, stored)` take the stage's embedded
  `*catalog.Candidate` (its `Base` and `Get`) instead of a staged
  descriptor and a base: the validation reads the staged descriptor only
  after checking `r.Original`.
- **Optional cores.** `DependencyClosure`, `KeepRules` and `KeepPolicies`
  moved; `dropProvider` still collects the live descriptors and stages the
  changes. `CheckCreationBinding` was not done: `prepareViewCreation` emits
  its temporary-view NOTICE between the 0A000 and 42P22 checks, and it is
  admission, so it stays whole.
- **CREATE MATERIALIZED VIEW** calls `view.FreezeDefinition(ddlHost{e: e},
  …)` and keeps its own reown pair: it re-reads the relation after
  reowning, which `ReownDisplacedTypes` does not return.
- **Tests.** New `view_host_test.go` (`TestViewHostSeam`: the temporary
  owner, `ReownDisplacedTypes` a no-op on a clean stage and run after an
  array displacement) and `ddl` `TestCreationErrorPositionsAtFirstNamePart`.
  `policy_test.go` binds through the host; four tests call
  `view.FreezeDefinition`. None moved or deleted.
- **Checks.** Every commit builds; the string-literal multiset is unchanged
  by the move (`.cswd/tmp/PG18-ENGINE-SPLIT-VIEW-DECISIONS/lits-*.txt`);
  the view, rule, policy, RLS, materialized, CTE and DML units passed, and
  `^TestMaterialized.*Concurrent` (78 tests) with `-race`; quickcheck passed
  at `bc76cde87`; the selection
  `^TestReference(View|Rule|Materialized|RLSBase|AccessRoleRLS|InheritanceQueryRLS|InheritanceDMLRLS|DriverBatchPolicy|DumpRoundTrip$)`
  passed in quick and `REFERENCE=1` mode (`ref-real.log`). racecheck,
  finalcheck and bench skipped at user request (no startup change).

### 27e record

Done on `implement-part/PG18-ENGINE-SPLIT-VIEW-QUERY-PREFLIGHT`, started at
`9d0897aad`. Re-measured there: `reach` reports every moved declaration
clean; `frontier` shows only `prepareStoredExecutionRoot`,
`viewRowLockLegality` and `sqlError` as OUT edges (`view` has the same
`sqlError`). No reference cancellation fixture matches a moved frame name.

| Commit | Change |
|---|---|
| `eb135c0fe` | Row-mark readers to `stored` (`view_row_marks.go`): `ViewHasRowMarks`, `ViewRowLockLegality`, `ViewLeafRowMark`; nine root callers renamed; `TestQuerySetStoredInheritedMarks` moved to `stored/view_row_marks_test.go` |
| `ad9c2b8ac` | `query_preflight.go`, `query_set_preflight.go` and `query_join_legality.go` to `view`, with `prepareStoredExecutionRoot`; exported `PreflightQuery`, `PrepareStoredExecutionRoot`, `ValidateQueryJoinExecution`, `ValidateMutationJoinExecution`, `ValidateExpressionJoinExecution`, `ValidateMarkedQueryJoin`; the `exec` preparation forwarders and ten root callers call `view` |

Deviations and notes:

- **Names.** `viewHasStoredRowMarks` became `stored.ViewHasRowMarks` (the
  package already says "stored"). Locals named `view` that shadowed the
  package became `read` (`AdmitExpression`, `executeSettingCall`, and two
  engine tests).
- **Tests.** `object_address_routine_test.go` and
  `series_expression_test.go` called `preflightFunctionFrom` and
  `preflightQuerySet`; they now go through `view.PreflightQuery`, whose
  top-level switch dispatches to them, so neither had to be exported. The
  other engine tests name `view.X`. One test moved (above); none deleted.
- **Checks.** Every commit builds and vets; the string-literal multiset of
  the moved code is unchanged
  (`.cswd/tmp/PG18-ENGINE-SPLIT-VIEW-QUERY-PREFLIGHT/lits-*.txt`); the 76
  unit tests of the touched test files and the view, rule, query, DML,
  cursor, COPY, domain, setting, prepared, RLS, policy, CTE and inheritance
  units passed; quickcheck passed at `ad9c2b8ac`. `REFERENCE=1` on
  `^TestReference(View|Rule|Materialized|RLSBase|AccessRoleRLS|InheritanceQueryRLS|InheritanceDMLRLS|DriverBatchPolicy|DumpRoundTrip$|RowLock|QuerySet|QueryOuter|QueryLock|Merge|UpdateFrom)`
  passed except four MERGE REPEATABLE READ tests
  (`MergeUpdateRepeatableRead`, `...Followup`, `MergeDeleteRepeatableReadFollowup`,
  `MergeSourceUpdateRepeatableRead`), which fail the same way on master
  `9d0897aad` in their `/postgres` oracle subtests (known drift).
  racecheck, finalcheck and bench skipped at user request (no startup
  change).

### Notes for later steps

- 27f (from 27e): `query_table_refs.go`, `view_write_authority.go` and
  `checkViewRowMarksExpanded` now call `stored.ViewLeafRowMark` and
  `stored.ViewRowLockLegality`; `view.QueryTableRefs` can use them directly.
- 24g (from 27e): `inheritanceQueryLeaves` calls `stored.ViewLeafRowMark`.
- 29e (from 27e): the DML preflights call `view.PreflightQuery`,
  `view.ValidateQueryJoinExecution` and `view.ValidateMutationJoinExecution`
  (`execute_bound.go`, `merge.go`, `update.go`).

- 29c (from 27d): `statementMutation.rls` holds `view.RowSecurity`;
  `configureRowSecurityLocked` composes `view.RowSecurityTarget` and
  `view.SelectRowSecurity`, and `rowSecurityInputLocked` calls
  `view.RowSecurityInputDenied`. Both wrappers stay root authority setup.
- 27d: `prepareStoredExecutionRoot` stays with root query preflight.
  `view.CreationScope`, `view.MaterializedRefreshScope` and
  `view.MaterializedStage` are the slot records; their accessors stay root
  methods. `ruleActionScope` stays root scope state; `RowSecurity` moves
  (27c decision 3).

- Role 28c, for 27b/27d: ALTER MATERIALIZED VIEW OWNER keeps its
  `alterViewAuthority` call in the root's `executeRelationOwnership`,
  before `role.PlanRelationOwnership`; moving `alterViewAuthority` only
  renames that call. `role` never imports `view`.
- 27a deviations from the plan above, for 27b:
  - Items 5 and 6 ran in the opposite order: rule expansion calls
    `bindViewWrite`, so the lowering became functions first.
  - `stageViewDependentsLocked` was deleted with `stageViewDependents`: its
    only caller was that test helper. DROP reaches the view closure through
    `dropProvider.applyViewDependents` (`drop_provider.go`), which stays in
    the root.
  - New names: `ruleRewriter{owner}` with `expandLocked` and
    `expandResidualLocked` (the old `Engine.expandRulesLocked` and
    `expandRuleResidualLocked`); `appendRuleCommand` is a plain function.
    `bindViewWrite(owner, view, b, stmt, consumed...)`,
    `lowerViewWrite(owner, view, b, stmt)` and
    `lowerViewInsert(owner, view, b, s)`.
  - `ruleAuthority` and `ruleAuthorizationOf` are in `rule_authority.go`,
    with the three `AuthorizationView` methods that implement the interface
    (`Access`, `AsUser`, `StorageLocked`). The interface moves to `view` in
    27b, but the three methods stay in the root on `AuthorizationView`, and
    so does `ruleAuthorizationOf`. `normalizeRuleCTEsLocked` takes a
    `ruleAuthority` too, because it stores it as `ruleCTECommand.caller`.
  - `policyPreflight` and `resolvePolicyRoles` take only `access.View`: they
    read `Roles` and `SessionRoleState` and run no check. The other three
    helpers take `(access.View, access.Environment)`.
  - The RLS group is the field `statementMutation.rls` of type
    `rowSecurityMutation` (`rls_base.go`); the name avoids
    `StatementScope.rowSecurity`, the immutable setting. The rule-action
    authority is the value field `StatementScope.ruleAction` of type
    `ruleActionScope{auth *AuthorizationView}` (`rule_execution.go`). Both
    are values, not pointer slots, because they are swapped per command;
    `ruleCommandScratch` and the CTE mutation save/restore copy them as one
    unit. Both groups hold root types, so they stay in the root.
- 27b: in `materialized_stage.go` and `materialized_refresh.go` the slot
  accessors `CatalogStage.materialized`/`materializedOrZero` and
  `StatementScope.materialized`/`materializedOrZero` are root methods. They
  stay in a root file; only the slot record types move.
- Open questions from 27a: `relationDefiningAuthorityCompatible` is
  already `catalog.RelationDefiningAuthorityCompatible` (5i).
  `physicalBinding` unwraps the `viewWrite` record (`viewWriteOf`), a
  `view` type, so it moves with `view`, not to `analyze`. Its root callers
  (`domain_default.go`, `execute*.go`, rule execution and admission,
  `row_security_setting.go`) import `view`.
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
