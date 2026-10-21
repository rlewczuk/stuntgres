# Engine split, step 22: `internal/engine/stored`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-stored.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §6 step 22; §2.7 (why this
  layer exists); §3 (layer L11 above `analyze` and `regobj`, rule 6:
  `analyze`, `exec`, `session`, `pgcatalog` and the lower features never
  import `stored`); §4.1 pattern 6 (reader-interface cut); the tests rule
  in §4; §7 decision 12 (the name).
- **Needs:** 22a: nothing (linear item 67). 22b: 22a, regobj 21b,
  functions F6, dump 18b and analyze 12f (items 67, 65, 71, 56 and 77;
  18b provides `dump.CommentLiteral`, 12f moves the `query_graph.go`
  helpers the set reads). 22c: 22b and catalog 5j (items 68 and 74). 22d:
  22c (item 69).
- **Effort:** high for 22a and 22c; xhigh for 22b and 22d (broad
  rewrites: never at the same time as another broad item, §8).
- **Session size:** one sub-step per session.
- **Linear items:** 22a = 67, 22b = 68, 22c = 69, 22d = 70.
- **Tools:** `reach`
  (`.cswd/tmp/PG18-ENGINE-SPLIT-EXEC-REFINE/reach/reach -dir . -files '<regexp>'`,
  with `-cut 'From>To,...'` to simulate a cut) and `frontier`
  (`.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-QUERY-PREP/frontier/frontier -dir . -files none.go -decls X,Y`),
  run from the worktree root; never pipe them into `head`. Rewrite tools:
  `movex`, `unalias`, `keylit`, `shadowrn` in
  `.cswd/tmp/PG18-ENGINE-SPLIT-PLAN-ALIASES/`; `movespec`, `expspec` and
  the move driver `mv.sh` in `.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-QUERY-PREP/`
  (12d.4: staging file, OUT check, export, move with aliases, strip the
  self-qualifier, `goimports`/`keylit`/`gofmt`). The sets measured below
  are in `.cswd/tmp/ENGINE-SPLIT-POSTPONED-PLAN/stored/` (`set4.txt` for
  22b, `set-22c.txt` for 22c).

## Goal

`stored` holds stored SQL definitions and their presentation:

- the frozen payloads that catalog descriptors carry: a view's
  `viewDefinition` (plan IR plus its analyzed captures), a policy's
  `policyExpression`, and in 22c the rule list;
- the three renderers: `pgRenderer` (expressions, in the style of
  PostgreSQL's `ruleutils.c`), `scalarRenderer` (stored scalar SQL for
  defaults, checks and dumps; it records the references it prints) and
  `viewRenderer`/`viewPresentation` (whole queries);
- the canonical expression mapper (retag, `expression_retag.go`) and the
  generic remap of stored expressions and view queries;
- small consumers that only read stored definitions: `pg_depend`
  references, temporary provenance checks, installed-node validators,
  stored-view read graphs.

Today this is about 10k clean lines in the root, used 1,052 times by 20
areas. Views, the table family, partitions, the type dumps and Dump
cannot leave the root until it moves (ENGINE-SPLIT.md §2.7). It makes
about 240 references to `analyze` (capture vocabulary such as `ViewLiteralPath`,
`ViewFromRoot`, closed defaults, lookups), so it sits above `analyze`.

The package is `stored` (settled in 22a, §7 decision 12): it holds the
frozen definitions, not only their rendering, so `deparse` was rejected.

What stays in the root: the `Engine` and scope entry points that capture
or publish definitions (view, rule and policy DDL until step 27), the
host bodies that call into `stored` (now one-line forwarders), and
`remapViewRuleOIDs`, which allocates OIDs.

## Inventory (measured at `c154e14fb`)

### The 22b set

A `frontier` fixed point from the presentation files closes at 120
top-level declarations with their methods: about 10.2k lines in 67 files,
43 of them whole. Its only root edges are lower pieces:

| Edge | Owner after the prerequisite |
|---|---|
| reg* spellings, dump outputs, remap and walker helpers, `catalogTypeDeclaration*`, `catalogLiteral*`, `catalogRegclassOutput`, `catalogRoutineOutput`, `catalogRegtypeOutput`, `aclItemContextFormatter`, `aclItemValidationOutput` (its `owner` field), `containsEmptyACL`, `regroleContextFormatter` | `regobj` (done in 21b; new names in `internal/engine/regobj/README.md`) |
| `commentLiteral` (used by `cycleConstantSQL`) | `dump.CommentLiteral` (done in 18b) |
| `pgIdentifier` (`deparse_renderer.go`) | `catalog.Identifier` (done in 21a) |
| `sqlError` | the package's own `errors.go` |

Package imports of the set (qualified references): `plan` 562, `analyze`
242, `functions` 72, `extension` 53, `proc` 27, `typedef` 19, `exec` 14,
`routine` 13, `sequence` 4, plus `regobj`, `catalog`, `expr`, `ids`,
`authorization`, `parser`, `sqltype`, `sqlstate`. No `session`,
`storage`, `visibility` or `lock`.

**Whole files (45 after 22a).** `view_deparse.go` (1,716),
`expression_remap.go` (1,120), `expression_retag.go` (949),
`scalar_renderer.go` (706), `view_render_sources.go`,
`view_literal_validation.go`, `scalar_rendering.go`,
`temporary_provenance.go`, `expression_references.go`,
`deparse_rendering.go`, `view_deparse_cte.go`, `view_deparse_values.go`,
`conditional_default.go`, `frozen_presentation_remap.go`,
`policy_validation.go`, `view_deparse_functions.go`,
`view_deparse_composite.go`, `procedure_provider.go`,
`routine_dependencies.go`, `view_composite_validation.go`,
`collation_validation.go`, `rule_assignment.go`, `array_rendering.go`,
`view_payload.go`, `catalog_pinning.go`, `routine_presentation.go`,
`expression_properties.go`, `expression_volatility.go`,
`intagg_presentation.go`, `sqljson_deparse.go`,
`record_shape.go`, `subscripts_deparse.go`, `collation_display.go`,
`datetime_overlaps_render.go`, `datetime_zone_deparse.go`,
`query_recursive.go`, `query_cte_volatility.go`, `view_cycles.go`,
`view_retained_input.go`, and the installed-node validators
`soundex_expression.go`, `levenshtein_expression.go`,
`metaphone_expression.go`, `pgcrypto_expression.go`,
`uuid_ossp_expression.go`.

`expression_properties.go` and `expression_volatility.go` (about 45 lines)
are used only by `expression_retag.go`; they move here, not to `exec`.

**Partial files: the declarations that move.**

| File | Moves | Stays |
|---|---|---|
| `view_definition.go` | `freezeQueryPresentation`, `frozenQueryDependencies` (22a made the freeze a function) | `Engine.freezeViewDefinitionLocked`, the wrapper that allocates a rule OID |
| `deparse_renderer.go` | everything (`pgIdentifier` is `catalog.Identifier` since 21a) | — |
| `view_dependencies.go` | `viewDefinition.Validate`, `validateViewLeaves`, `validateStoredSetNode` | dependency staging (step 27); the drop diagnostics go to a root `drop_diagnostics.go` (27a) |
| `view.go` | `viewDefinition`, `viewLeaf`, `viewOptions`, `validateNamespaces`, `namespaceRoot` | CREATE VIEW handling (step 27) |
| `scalar_function_sql.go` | `scalarRenderer.scalarFunctionSQL` | `literalSQL` is `regobj.ConstantLiteral` (21b) |
| `foreign_key_lifecycle.go` | `pgRenderer.foreignKeyConstraintSQL`, `foreignKeyConstraintSQLWithPrimary`, `foreignKeyActionSQL` | FK lifecycle (step 24) |
| `query_graph.go` | nothing: analyze 12f (item 77) moved the whole file (`analyze.CollectQueryFrames`, ...); the set calls `analyze`'s exported names | — |
| `view_write.go` | `viewWriteShape`, `viewDirectColumn`, `sameViewRelation` | view-write lowering (step 27) |
| `unique.go` | `pgRenderer.uniqueConstraintSQL` | unique enforcement (step 23) |
| `policy.go`, `policy_payload.go` | `policyExpression`, `policyExpressionOf`, its `DependsOn` and `Validate` | policy DDL (step 27) |
| `policy_dump.go` | `policyExpressionSQL` | the dump groups (22c) |
| `roles.go` | `relationVisibleAt`, `relationDiagnosticNameAt` | role code (step 28) |
| `view_check.go` | `viewCheckMode`, `validateViewCheckDeclaration` | — |
| `deparse_function.go` | `deparseAttribute` | `statementHost.DeparseInquiry` (22c item 4) |
| `definition_reader.go` | `scalarRenderer.descriptor` | the `AuthorizationView.definition*` wrappers |
| `definition_identity.go` | `structuralNull` (catalog 5i moved it to `plan.StructuralNull`) | OID assignment |
| `sql_routine.go` | `sameMappedSQLRoutine` | SQL routine invocation |
| `composite_field_dependency.go` | `compositeAttributeProvider` | dependency staging |
| `conversion_execution.go` | `conversionDefault` | — |
| `domain_default.go` | `omitColumnDefault` | the column default records |

`procedureProvider` and `pgDependPinned` are in the set because
`storedReferenceEpochValid` and `pgDependRefs.add` call them.

### Not in this step

- `deparse_tree.go` (node-tree output) moved to `plan` in 10e
  (`plan/node_tree.go`: `BoundTree`, `AppendBoundTree`, `CatalogTree`).
- Type declarations, literals and reg* code go to `regobj` (21).
- `dumpDomain` is `dump.Statement` (18b); `domainDumpStatement` goes to
  `typedef` (15e).
- The rule payload and its rendering are 22c.
- View, rule and policy DDL, view-write lowering, rule rewriting and
  materialized refresh are step 27.

### Outside users

1,052 production uses outside the set: views 237, tables 101, indexes 52,
partitions 44, type leftovers 36, the `pgcatalog` runtime 28,
inheritance 26, expression utilities 24, Dump 22, query admission 19,
and smaller counts elsewhere. 2,002 test uses in 265 test files:
`viewDefinition` 430, `scalarRenderer` 244, `pgRenderer` 226,
`retagBoundTypes` 146, `viewLeaf` 125, `viewOf` 109, `viewRenderer` 100,
`pgRenderParent` 69. Tests are rewritten mechanically; they never keep
code in the root.

### Host methods whose root bodies become forwarders

These interfaces live below `stored`, so they keep their methods; only
the bodies shrink:

- `analyze.Host`: `MapBoundExpression`, `MapBoundQueryExpression`
  (retag), `StoredViewVolatile` (`cteStoredViewVolatile`),
  `ValidateViewReadGraph`, `CaptureRetainedViewInput`
  (`captureRetainedDefinitionInput`). 5j removed `RelationHasRules`;
  `BindViewWrite` stays for step 27.
- `exec.Host`: `DeparseInquiry` (22c item 4).
- `pgcatalog.Runtime`: `ViewDefinition`, `IndexDefinition`,
  `ExpressionReferences`, `ViewReferences`, `IndexReferences`,
  `PartitionReferences`. `ExpressionTree` goes with 10e; 5j removed
  `HasRules`, `VisitRules`, `ViewOptions` and `VisitPolicyExpressions`
  (callers read the payloads; 22c moves `ruleList`, `*viewDefinition` and
  `*policyExpression` with their reader methods).

## Part A

### 22a — In place

One commit per item; `make build` and the targeted units after each.

1. **The coverage cut (pattern 6).** `partitionIndexCoverageComplete`
   (`partition_index_attach.go:14`) builds `&dropProvider{base: view}` to
   call three readers. Declare

   ```go
   // indexCoverageReader is what partitioned-index coverage reads.
   type indexCoverageReader interface {
       liveDescriptor(ids.ObjectAddress) catalog.Descriptor
       descriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor]
       removed(ids.ObjectAddress) bool
   }
   ```

   and turn the method into `indexCoverageComplete(r indexCoverageReader,
   index *catalog.Relation) bool`. `*dropProvider` implements the
   interface already. Add `catalogCoverage{view catalog.Read}` with
   `removed` = false, `descriptors` = `view.Descriptors()` and
   `liveDescriptor` = `view.Get`; first confirm that
   `dropProvider.projectedDescriptor` returns `base.Get` when it has no
   stage and no prepared drop, and stop if it does not.
   `partitionIndexCoverageComplete(view, index)` becomes
   `indexCoverageComplete(catalogCoverage{view}, index)`. `reach -cut`
   predicts 32 newly clean declarations, about 2.2k lines: partition dump
   and replay helpers (1.2k), `validateIndexDefinition`,
   `indexDefinitionSQL`, `indexCatalogMetadata` and index metadata (0.4k),
   `relationProvider.Validate` (0.3k), `dumpPredecessor`, the physical NOT
   NULL dump emitters, `domainInvalidDumpEvents`, `sqlRoutineDumpEvents`.
   Re-run `reach` and record the real numbers.
2. **The freeze cut.** `Engine.freezeViewDefinitionLocked` and
   `Engine.freezeQueryPresentationLocked` (`view_definition.go`) read only
   `auth.Catalog`, `auth.session.SessionToken`, `e.catalog.Domain` and, for
   a zero rule OID, `e.catalog.allocateOIDLocked`. Make
   `freezeQueryPresentation(view catalog.Read, session ids.SessionToken,
   syntax *parser.Select, b analyze.Binding, barrier, invoker bool, rule
   ids.OID, temporaryMessage string, temporaryConsumer bool)` a function
   that builds references with `view.Owner().Domain.Ref`. Callers that
   pass rule 0 allocate the OID first (keep the `Engine` method as the
   allocating wrapper). The callers are in `view.go`,
   `view_definition.go`, `rule_definition.go`, `rule_ddl.go`,
   `rule_action_presentation.go`, `query_table_create.go` and
   `policy_expression.go` (grep `freezeViewDefinitionLocked` and
   `freezeQueryPresentationLocked`). The function then joins the 22b set; re-run
   the fixed point.
3. **Read accessors** (ENGINE-SPLIT.md §6). `viewDefinition` gets read-only
   methods for the fields its readers outside the 22b set use: sources,
   dependencies, invoker, rule (the existing `RuleOID`), references and
   literals. Those readers switch to them; the set and every write keep
   field access.
4. **Renames, so files say what they hold.**
   - `scalarRenderer`, `defaultSQL`, `renderColumnDefault` and the
     renderer methods out of `default.go` into `scalar_renderer.go`;
     include the `record*` methods that 21a brought next to it.
   - `view_remap.go` → `expression_remap.go`; its two `Engine` methods
     move to `catalog_expression_reown.go`.
   - `view_catalog_remap.go` → `catalog_expression_reown.go` (it is the
     catalog-wide reowner, also used by type generations, owned roles and
     extension owner import).
   - `query_cte_view.go` → `view_deparse_cte.go`, `query_values_view.go` →
     `view_deparse_values.go`, `string_view_sources.go` →
     `view_deparse_functions.go`, `composite_render.go` →
     `view_deparse_composite.go`.
5. **Settle the name** (`stored` or `deparse`) and the exported names
   (table below). Write both into this file before 22b.

**Open questions.** None known. If a cut does not make its declarations
clean, stop and report the `reach` path instead of widening the cut.

**Result (measured at `1c5f5898a`).**

- Coverage cut: `reach` went from 1466 to 1504 clean declarations: the 32
  predicted (`relationProvider.Validate`, `validateIndexDefinition`,
  `indexDefinitionSQL`, `dumpPredecessor`, the partition dump and replay
  helpers, the physical NOT NULL emitters, `domainInvalidDumpEvents`,
  `sqlRoutineDumpEvents`) plus the six new declarations.
  `partitionIndexCoverageComplete(view, index)` is kept as the catalog entry
  point.
- Freeze cut: `freezeQueryPresentation` is clean. It takes the consumer's
  TEMP owner token (`catalogSourceTemporaryOwner(auth)`, zero without a
  session) and never allocates. The `frontier` fixed point from `set4.txt`
  plus `freezeQueryPresentation` and `frozenQueryDependencies` has the same
  OUT as before; only `sqlError` gains a user.
- Accessors: `Sources`, `Dependencies`, `Invoker`, `References` and
  `Literals` sit in `view_payload.go` next to `RuleOID`. 138 reads in 56
  root files (production and tests) were rewritten with a go/types pass.
  Outside the set, 21 writes remain as field access: 16 in tests and 5 in
  three production functions, `Engine.bindPolicyExpression` (dependencies),
  `Engine.executeAlterView` (invoker), `Engine.remapViewRuleOIDs` (rule).
- Renames: as listed. `scalar_renderer.go` and `expression_remap.go` are
  now whole files of the set. `default.go` keeps the two table helpers;
  `catalog_expression_reown.go` holds `reownCatalogExpressions`,
  `remapCatalogExpressions` and `remapViewRuleOIDs`. The `record*` methods
  stay in `regtype_persistence.go`/`regproc_persistence.go`, because 21a has
  not run. (21a later moved them into `scalar_renderer.go`, which is now a
  whole file of the set with them.)
- Name: `stored`, with the table in 22b step 2 as the final names.

### 22b — Create `stored` and move the set (broad)

1. **Prerequisites.** Check that 21b (`regobj`), F6 (no provider switch in
   `expression_remap.go`; only the documented `functions.SequenceCall` Ref
   case remains, see the functions F6 record) and 18b (`dump.CommentLiteral`)
   have landed. Re-run
   the fixed point from `set4.txt` plus the 22a additions; OUT must hold
   only `regobj`, `dump`, `catalog` and `sqlError`. Stop if not.
2. **Names.** Settled in 22a (record the table in the README):

   | Root name | `stored` name |
   |---|---|
   | `viewDefinition`, `viewLeaf`, `viewOf`, `viewPayloadOf` | `View`, `ViewLeaf`, `ViewOf`, `ViewPayload` |
   | `viewPresentation`, `viewRenderer`, `newViewRenderer` | `Presentation`, `QueryRenderer`, `NewQueryRenderer` |
   | `pgRenderer`, `pgRenderParent` | `Deparser`, `DeparseParent` |
   | `scalarRenderer`, `defaultSQL`, `renderColumnDefault` | `ScalarRenderer`, `DefaultSQL`, `RenderColumnDefault` |
   | `retagBoundTypes`, `retagBoundTypesWithQuery`, `mapBoundExpression` | `RetagTypes`, `RetagTypesWithQuery`, `MapExpression` |
   | `remapBoundExpression`, `remapScalarExpression`, `remapViewQuery` | `RemapExpression`, `RemapScalar`, `RemapViewQuery` |
   | `policyExpression`, `policyExpressionOf` | `Policy`, `PolicyOf` |
   | `pgDependRefs`, `pgDependExpressionRefs`, `pgDependQueryRefs` | `DependRefs`, `ExpressionDependRefs`, `QueryDependRefs` |
   | `validateInstalled*` | `ValidateInstalled*` |
   | `viewDefinition.Sources`, `Dependencies`, `Invoker`, `RuleOID`, `References`, `Literals` | unchanged (exported in 22a) |

3. **Export in place**, then **move** with the move driver (`mv.sh`):
   collect the partial-file declarations into a staging file, check OUT,
   export with `expspec` and `movex`, move with `movespec`. Whole files
   move with `git mv`. Add `stored/errors.go` with `sqlError`.
4. **Aliases.** Type aliases for the types the root names in many files
   (`viewDefinition`, `viewLeaf`, `scalarRenderer`, `pgRenderer`,
   `viewRenderer`, `policyExpression`, ...) go into one root file
   `stored_aliases.go`; functions are qualified directly. List the aliases
   in Status. 22d removes them.
5. **Layering.** `stored` must not import the root, `session` or an upper
   feature, and no package below it may import it:
   `go list -f '{{.ImportPath}}: {{join .Imports " "}}' ./internal/engine/...`.
6. **Forwarders.** Rewrite the host bodies listed above to call `stored`
   directly; keep the interface methods.

### 22c — Rule and policy payloads, and the remap protocol

1. **Rule payload (about 2.2k lines).** The fixed point from the rule
   payload and rendering files closes at 39 declarations with no root
   blockers (plus `remapLikeExpression` and `remapPartitionDescriptor`,
   which index 23a and the partition prompt send here): `ruleList`,
   `ruleDefinition`, `ruleAction`, `rulesOf`,
   `rulesPayloadOf` and the rest of `rule_payload.go` and the clean part
   of `rule_definition.go`; `rule_dump.go` (569), `rule_source_dump.go`,
   `rule_assignment_dump.go`, `update_row_dump.go`, `rule_validation.go`,
   the clean part of `rule_remap.go` (391), `policy_remap.go`,
   `rls_base_dump.go`, the dump groups of `policy_dump.go`
   (`policyDumpGroups`, `rowSecurityDumpGroups`); the rule tree mapper
   (`rule_tree_mapper.go`, 319) and binding map (`rule_binding_map.go`,
   188); `refreshRuleBinding` and `refreshRuleConflict` from
   `rule_rewrite.go`; `constraintName` and `constraintParent`
   (`view_dependencies.go`); `rewriteViewExpression` (`view_write.go`).
   `dml` needs `ruleAction` and `ruleAssignmentSQL`, so the rule payload
   cannot wait for step 27. `Engine.bindRuleDefinitionLocked` stays.
2. **Payload accessors (needs 5j).** The 5j accessors (`RulesPayload.Len`,
   the rule visitor with rule identity, `ViewPayload.Options`, the
   `PolicyPayload` expression visitor) move with the types that implement
   them.
3. **`ReownExpressions`.** `Engine.reownCatalogExpressions`
   (`catalog_expression_reown.go` after 22a) reads only
   `e.catalog.read(next)` and `e.catalog.Domain`. Make it
   `stored.ReownExpressions(view catalog.Read, next *catalog.State,
   mapping map[ids.ObjectAddress]ids.ObjectAddress, typesOnly bool) error`
   with the domain from `view.Owner()`. Its callers pass
   `e.catalog.read(next)`: `remapCatalogExpressions` (Restore),
   `type_generation.go`, `owned_roles.go`, `extension_owner_import.go`.
   `remapViewRuleOIDs` allocates OIDs and stays in the root.
4. **`DeparseInquiry` (optional; measure first).** `statementHost.DeparseInquiry`
   (`deparse_function.go`) mixes a decision (which signature, which
   relation, which output options) with scope access (`scope.inquiry`,
   `Engine.resolveRelationSnapshot`). If its decision body reaches only
   the catalog snapshot, the path, the output environment, the temporary
   namespace and a relation resolver, move it as a function over one
   input record and keep the root method as the adapter. Skip it if more
   is needed, and record why.

### 22d — Remove the aliases (broad)

Run `unalias` over `stored_aliases.go` (with `shadowrn` first for locals
that would capture `stored.X`), then `keylit` and `gofmt` on changed files
only. The root and every test name `stored.X` directly.

## Invariants

- Rendering output stays byte-identical: `pg_get_viewdef`, `pg_get_expr`,
  `pg_get_constraintdef`, `pg_get_indexdef`, rule definitions, policy
  expressions, `information_schema` view text, stored defaults and checks,
  and every Dump statement.
- Frozen payloads are immutable once published; remap and retag build new
  values (copy on write) and never mutate a published `viewDefinition`,
  rule list or policy.
- Retag keeps the canonical mapper's property rules
  (`rewrittenIntrinsicProperties`: eliminated branches keep analyzed
  dependence; replaced child demand is dropped).
- Temporary provenance and stored-reference epoch checks keep their
  answers; `procedureProvider.Validate` is the same validator.
- `stored` takes no lock and reads no engine state; every caller passes
  the catalog view it already holds.
- Dump/Restore error order does not change (`dumpPredecessor` and the
  partition replay helpers become clean, not reordered).

## Targeted tests

- Engine units: grep `*_test.go` for the moved names (table above); 265
  test files use them. Rewrite them with the alias tools. Tests that use
  only moved code (renderer and retag tests that build nodes directly)
  move into `stored`; engine-fixture tests stay and are adapted.
- Reference selections (quick mode, then `REFERENCE=1`; names from
  `grep -rh '^func TestReference' reference`; never modify `reference/`
  or `smoke/`):
  `make check RUN='^TestReference(View|Rule|Polic|RLS|RowSecurity|Materialized|Deparse|PGDepend|PGTableViews|GeneratedStored)'`,
  `RUN='Stored|Remap'` and `RUN='DumpRoundTrip|Restore|NativeControls'`.
  Some `REFERENCE=1` tests also fail on `master` (oracle drift): rerun a
  failure on a detached `master` worktree before reporting it, and report
  anything that passes there.
- `make bench` after 22b and 22d: evaluation and startup paths do not
  change, but the move is broad; report ns/op and allocations.

## Documentation

- Create `internal/engine/stored/README.md` (22b): what it owns (Goal
  list), the layering rule (who may import it and who reaches it through
  host methods), the immutability and byte-identity invariants, the name
  table, and `## Testing` with the selectors above.
- Update the `analyze` README ("What stays in the engine": retag walker,
  stored view definitions), the `pgcatalog` README (`Runtime` bodies), the
  `exec` README (`deparseRelation`, `DeparseInquiry`), the `catalog` README
  (payload interfaces) and `internal/engine/README.md` where they name
  moved code.
- `AGENTS.md`: add `stored` to the brace list of the engine subpackages
  bullet. The file must not grow.
- Tick the sub-step here and in
  [ENGINE-SPLIT-STATUS.md](ENGINE-SPLIT-STATUS.md); update the step 22
  cell of ENGINE-SPLIT.md §8.

## Done when

`stored` exists above `analyze` and `regobj`; no root file renders stored
SQL or declares a stored payload; the host bodies listed above are
forwarders; `reownCatalogExpressions` is `stored.ReownExpressions`; no
aliases remain; the checks in §8.3 pass.

## Status

- [x] 22a — In place: coverage and freeze cuts, accessors, renames, name
  settled (item 67) (`fc0f6dbe8`, `219495724`, `f9035b171`, `a0224bb18`,
  `905c53bad`, `bec9e323f`, `89b74100f`, then the docs commit) · selected
  unit and dual-mode proof and quickcheck passed; racecheck/finalcheck
  skipped at user request
- [x] 22b — `stored` created and presentation set moved *(broad)* (item 68)
  (`ae29be757`, `4f51dfb7c`, `b66915729`, `687d5dc84`, `96720fefc`,
  `020d1a446`, `b6960ba81`, then the docs commit) · selected unit and
  dual-mode proof and quickcheck passed; racecheck/finalcheck skipped at
  user request
- [x] 22c — Rule and policy payloads, `ReownExpressions` (item 69)
  (`7eb30702d`, `06b8adad6`, `a3cd3577f`, `c06636d9f`, `751c1a011`, then the docs commit) · selected unit and dual-mode proof,
  byte-identical dump smoke and quickcheck passed; racecheck/finalcheck
  skipped at user request
- [ ] 22d — Aliases removed *(broad)* (item 70)

Outstanding aliases (`internal/engine/stored_aliases.go`, types only):
`pgDependRefs`, `pgRenderer`, `pgRenderParent`, `policyExpression`,
`procedureProvider`, `ruleAction`, `ruleDefinition`, `ruleTreeMapping`,
`scalarRenderer`, `viewCheckMode`, `viewDefinition`, `viewLeaf`,
`viewPresentation`, `viewQueryRemapper`, `viewRenderer` (15; 22c added the
three rule types). Functions, constants, `ViewParts`, `RuleParts`,
`RuleActionParts` and `RuleList` are qualified directly.

### 22b record

Measured at `ca2167c7c` and replayed onto `c0dfba392` and `3f8d7d950`, then rebased onto `47b3bd17c`: the fixed point
from `set4.txt` plus the 22a additions had OUT = {`sqlError`} only.
Commits, each built by a script in
`.cswd/tmp/PG18-ENGINE-SPLIT-STORED-PACKAGE/` (`replay.sh` rebuilds them
on a new base; the payload API commit is cherry-picked):

1. Staging: the moving declarations of fifteen mixed files went to
   `stored_*.go` (`movedecl`); `deparseAttribute` joined
   `deparse_rendering.go`, `sameMappedSQLRoutine` `routine_presentation.go`.
2. Ten test locals named `stored` that would capture `stored.X` became
   `persisted` (`storedrn`, a variant of `shadowrn`).
3. Payload API, as chosen with the user instead of exported fields:
   `ViewParts` mirrors every `View` field; `NewView`, `Parts` and `With`
   (copy, edit, rebuild) replace field writes; read accessors cover every
   outside read; `NewPolicy` and three policy accessors. A go/types pass
   (`payloadapi`) rewrote 139 reads and 71 literals; 71 writes were
   converted by hand.
4. Export in place (`expspec`/`movex`, 145 rows).
5. Move: 55 files, `stored/errors.go`, `stored_aliases.go`; `movex`
   qualified the engine (about 320 files changed).
6. Forwarders: `ViewDefinitionSQL`, `IndexDependRefs` and
   `PartitionDependRefs` hold the `pgcatalog.Runtime` bodies.
7. `TestViewPartsRoundTrip` (new, pins the payload API) moved into
   `stored`; every other test that uses `stored` builds an engine, a
   catalog fixture or a test binder and stays in the root, rewritten.

Deviations from the plan above:

- The 22a note named three production writers of `viewDefinition` fields;
  there were seven functions (CREATE VIEW, ALTER VIEW SET/RESET, CREATE
  RULE, `remapViewRuleOIDs`, rule action and policy freezing, policy
  remap) plus 46 test sites. All use `With`/`Parts` now. Where a writer
  had already attached the definition to its descriptor (CREATE VIEW's
  TEMP consumer, CREATE RULE's quote revision), the descriptor takes the
  new copy.
- Role 28a had moved `relationVisibleAt` and `relationDiagnosticNameAt`
  from `roles.go` to `relation_diagnostics.go`; they move from there
  (`relationDiagnosticName` stays).
- `conversionDefault` stays in the root: it is a statement-scope cache that
  nothing in the set uses, not stored SQL.
- Names beyond the 22a table: `RemapView` (`remapFrozenPresentation`),
  `PolicySQL`, `ViewVolatile`, `CaptureRetainedInput`,
  `ExpressionTypesChanged`, `DependPinned`, `CheckMode`/`CheckNone`/
  `CheckLocal`/`CheckCascaded`, and the `stored*` helpers without their
  prefix (`ReferenceEpochValid`, `ViewUseReference`,
  `ValidateCollationDependency`, `ValidateSetNode`, ...). View 27a added
  `remapLikeExpression` to `expression_remap.go`; it moved as
  `RemapLikeExpression`. The README has the full table.

### 22c record

Measured at `d12bb058f`: the fixed point from the rule payload, dump and
remap files had OUT = {`sqlError`} plus seven `stored` aliases; after the
staging commit, which left the five rule DDL helpers in the root, OUT held
only the aliases. Commits, each replayable by a script in
`.cswd/tmp/PG18-ENGINE-SPLIT-STORED-RULES/` (`c2.sh`, `c5.sh`,
`c5-post.sh`, `c3-tests.py`, `payloadapi/`, `docs-edit.py`):

1. Reown cut: `Engine.reownCatalogExpressions` became a function over
   `(catalog.Read, *catalog.State, mapping, typesOnly)`. It reads the
   domain from `view.Owner()`, and callers pass `e.catalog.read(next)`.
2. Staging (`movedecl`): the rule payload types, `ruleEvent` and
   `ruleEventCode` (`rule_definition.go`), `refreshRuleBinding`/
   `refreshRuleConflict` (`rule_rewrite.go`), `constraintName`/
   `constraintParent` (`view_dependencies.go`), `rewriteViewExpression`
   (`view_write.go`), `refreshGenerationChecks` (`generated_virtual.go`)
   and the reowner went to `stored_*.go` files.
   `checkTemporaryRuleSourcesReplacementLocked` moved next to its only
   caller in `temporary_preservation.go`, so `rule_remap.go` moved whole.
3. Payload API, following the `View` precedent: `RuleParts`/`NewRule` and
   `RuleActionParts`/`NewRuleAction`, `Parts`, `With` and read accessors.
   `OID` and `Name` stay exported fields (`definition_identity.go` assigns
   `OID` in place). A go/types pass (`payloadapi`) rewrote 161 reads and
   22 literals. CREATE RULE binding builds `RuleParts`, and the 33 test
   writes use `With`. Tests that took `&action.binding` for
   `CTEDefinition.Mutation` identity now use a local copy.
   `TestRulePartsRoundTrip` (new) pins the API.
4. Export in place (`expspec`/`movex`, 29 rows).
5. Move: 21 files, three type aliases, `movex` qualify (about 70 engine
   files). `TestRulePartsRoundTrip` moved into `stored` and shares
   `nonZero` with `TestViewPartsRoundTrip`. Every other affected test
   builds an engine or a catalog fixture and stays in the root, rewritten.

Deviations from the plan above:

- `ruleDuplicate`, `ruleIndex`, `ruleMissing`, `ruleResultCheck` and
  `ruleReturning` stay in the root. The 22c set did not need them, and
  view 27b takes them. `ruleEventCode` moved here (`RuleList.Visit` needs
  it), not to `view`.
- `refreshGenerationChecks` moved too: `ReownExpressions` needs it. The
  root CHECK and ALTER paths call `stored.RefreshGenerationChecks`.
- `refreshRuleConflict`, `ruleAssignmentSQL`, `ruleUpdateAssignmentsSQL`
  and `ruleEventCode` stay unexported: nothing outside `stored` uses them
  yet.
- Item 4 (`DeparseInquiry`): measured and not moved. The constraint branch
  renders exclusion constraints, and `deparseRelation` renders
  `pg_get_indexdef`, through `indexDefinitionSQL` (index code, which sits
  above `stored` after step 23). The view-name form resolves through
  `Engine.resolveRelationSnapshot`, and `pg_get_serial_sequence` evaluates
  through the scope. That is more than the allowed input record.
- `remapLikeExpression` had already moved in 22b (`RemapLikeExpression`).

### Notes for later steps

- 22c and 27: `View` and `Policy` fields are private to `stored`. Root code
  builds them with `NewView`/`NewPolicy` and edits unpublished copies with
  `With`/`Parts`; a new outside read needs an accessor in
  `stored/view_payload.go`. `ViewLeaf` fields are exported (`Ref`, `Reads`,
  `Mark`, ...).
- 22d: `unalias` over `stored_aliases.go` needs `shadowrn` first; 22b
  renamed only the `stored` locals that shadowed qualified function uses,
  not alias uses. `storedrn` in `.cswd/tmp/PG18-ENGINE-SPLIT-STORED-PACKAGE/`
  is the qualify-time variant.
- Prompt files of steps 23–25, 27 and 28 still cite the root names of the
  moved code; the name table in `internal/engine/stored/README.md` maps
  them.
- 21a (regobj): put the `scalarRenderer.record*` methods in
  `scalar_renderer.go` (22a created it), not `default.go` (done).
- Index 23a: the reader interface is `indexCoverageReader`
  (`index_partition_attach.go` since 23a); `catalogCoverage` is the plain-catalog
  implementation.

- 22d: the three rule aliases join the others; `rule_*_test.go` name
  them often (`ruleDefinition` in literals, `ruleAction` in helpers).
- Step 27 and DML 29b: rule records are values with private fields. Build
  them with `stored.NewRule`/`NewRuleAction` and edit copies with `With`.
  `stored.RulesOf(d)[i] = ...` replaces a rule only in an unpublished
  descriptor copy. `ruleAssignmentSQL` and `ruleUpdateAssignmentsSQL` are
  unexported; export them in the step that first calls them from outside.
- Pre-existing Dump bug found by the 22c smoke (unchanged by this step):
  a rule action `INSERT ... SELECT` whose target list mixes `OLD`/`NEW`
  columns with literals and has no `FROM` (`CREATE RULE r AS ON DELETE TO
  a DO ALSO INSERT INTO b SELECT OLD.id, 'x'`) makes Dump fail with
  `missing analyzed view literal`. The `VALUES` form and `SELECT ... FROM`
  dump fine. It needs a dual-mode reference test and a fix outside the
  split.
- Step 27 (`view`) loses `rule_tree_mapper.go`, `rule_binding_map.go`,
  `refreshRuleBinding` and `refreshRuleConflict` to 22c: the rule remap
  needs them. Rule rewriting proper (`ruleRewriter` and the rule
  program records) stays for step 27.
- Analyze 12f must not take `query_cte_volatility.go`: `cteStoredViewVolatile`
  reads `viewDefinition`, which sits above `analyze`. It moves here (22b)
  as the body of `analyze.Host.StoredViewVolatile`.
- Exec 11e does not need `expression_properties.go` and
  `expression_volatility.go`: their only user is retag, so they move here.
- The 22a coverage cut also makes `relationProvider.Validate` clean; step
  24 (`table`) can take the relation validator with its clean set.
- Steps 23–25 and 27 render through `stored`; their prompt files cite the
  `stored` names settled in 22a.
