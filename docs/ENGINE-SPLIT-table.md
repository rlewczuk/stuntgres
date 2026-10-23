# Engine split, step 24: `internal/engine/table`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-table.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §6 step 24; §3 rules 6–8
  (an upper feature: it may import `analyze` types and pure functions, but
  never builds a binder; `index` → `table` → `partition`); §4.1 (two parts,
  measured clean sets, pattern 6); the §4 tests rule; §7 decision 14.
- **Needs:** 24a: catalog 5i (linear item 72). 24b: 24a, index 23b (item
  83), `dump` 18b (item 56) and access B6 (item 79); 23b already brings
  `stored` 22b (item 68) and `regobj` 21b (item 65). 24c: 24b and index
  23c (item 84). 24d.1: 24c and index 23d.1 (item 85a); 24d.2 and 24d.3:
  24d.1. Read the sequence
  prompt's "Notes for later steps" (identity and serial staging belong
  here).
- **Effort:** high for 24a; xhigh for 24b (broad), 24c and 24d.1–24d.3.
- **Session size:** one sub-step per session. 24a may need two sessions:
  do the renames and slots first (commit), then the cuts.
- **Linear items:** 24a = 86, 24b = 87 (broad), 24c = 88, 24d.1 = 89a,
  24d.2 = 89b, 24d.3 = 89c.
  Fourth round (item 133): 24h = 134, 24i = 135, 24j = 136.
- **Tools:** `reach`
  (`.cswd/tmp/PG18-ENGINE-SPLIT-EXEC-REFINE/reach/reach -dir . -files
  '<regexp>' [-cut 'From>To,...']`) and `frontier`
  (`.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-QUERY-PREP/frontier/frontier -dir .
  -files a.go,b.go [-decls X,T.m] [-exclude ...]`), run from the worktree
  root; never pipe them into `head`. Rewrite tools (`movex`, `unalias`,
  `keylit`, `meth2fn`, `expspec`, `movespec`, `mv.sh`) are in
  `.cswd/tmp/PG18-ENGINE-SPLIT-PLAN-ALIASES/` and
  `.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-QUERY-PREP/`. Measurement outputs of
  this plan are in `.cswd/tmp/ENGINE-SPLIT-POSTPONED-PLAN/table/`.

## Goal

Tables become a feature package: column and constraint records, NOT NULL,
CHECK, DEFAULT, generated columns, foreign-key definitions, ALTER TABLE
candidates, the table dump record, and inheritance. The orchestration of
CREATE and ALTER TABLE (`Engine.mu` windows, lock waits, publication)
stays in the root; since 24h the row rewrites and scans are `table`
functions over a root evaluation template.

**Why inheritance is part of `table`.** Inheritance and tables use each
other in both directions: 69 dirty and 29 clean inheritance declarations
use table code, and 52 and 10 the other way. `dumpRelation` has methods in
both areas, and Go cannot split one type's methods across packages. ALTER
TABLE on an inheritance tree re-enters `alterTableLocked` once per node.
One package removes the cycle; two would need a third package for shared
records. Inheritance *query and DML expansion* is not DDL and leaves the
area (see "Not table code").

## Inventory (measured at `c154e14fb`)

The tables keep the measured file names. 24a renamed `not_null.go` to
`alter_table.go`, `temporary_table.go` to `create_table.go`, `table.go` to
`drop_table.go`, `alter_schema.go` to `relation_set_schema.go`,
`inheritance_control.go` to `inheritance_remap.go` and the two
`inheritance_dml*.go` files to `mutation_targets.go` (see Status).

Two candidate areas, 74 files, about 18.7k lines, 6.6k clean (they reach
none of `Engine`, `StatementScope`, `CatalogStage`).

**Table area (49 files, 13.0k lines, 4.3k clean).**

| Sub-area | Files | Lines / clean |
|---|---|---:|
| CREATE, DROP, LIKE, CREATE TABLE AS | `temporary_table.go`, `table.go`, `table_like.go`, `table_like_index.go`, `query_table_create.go` | 1.23k / 0.03k |
| ALTER core | `not_null.go` (holds `alterTableLocked`, 898 lines), `alter_add_column.go`, `alter_column_candidate.go`, `alter_default.go`, `alter_generation.go`, `column_projection_change.go`, `alter_type.go`, `alter_type_check.go`, `alter_type_index.go` | 2.79k / 0.40k |
| NOT NULL | `not_null_binding.go`, `not_null_candidate.go`, `not_null_type.go`, `physical_not_null.go`, `physical_not_null_competing.go`, `physical_not_null_control.go` | 0.94k / 0.67k |
| CHECK and constraint DDL | `check.go`, `alter_constraint.go`, `alter_constraint_drop.go`, `alter_constraint_rows.go`, `constraint_rename.go` | 1.23k / 0.30k |
| DEFAULT and generated | `default.go`, `default_dependencies.go`, `default_replay.go`, `generated.go`, `generated_replacement.go`, `generated_virtual.go` | 2.00k / 1.39k |
| FK DDL and probes | `foreign_key.go`, `foreign_key_lifecycle.go`, `foreign_key_probe.go`, `foreign_key_writes.go`, `alter_foreign_key.go` | 1.68k / 0.48k |
| FK actions | `foreign_action_admission.go`, `foreign_action_apply.go`, `foreign_actions.go` | 1.01k / 0.15k |
| Dump and Restore | `table_dump.go`, `constraint_dump.go`, `not_null_restore.go`, `physical_not_null_dump.go`, `physical_not_null_foreign_key.go`, `physical_not_null_restore.go`, `physical_not_null_restore_primary.go`, `physical_not_null_restore_syntax.go`, `foreign_key_restore.go`, `attribute_rank_restore.go` | 2.11k / 0.91k |

Of the 1.39k clean DEFAULT lines, about 0.68k are `scalarRenderer`
(declared in `default.go` when measured, in `scalar_renderer.go` since 22a: the type, `sql` at 618 lines, `functionSQL`,
`record`, plus `defaultSQL` and `renderColumnDefault`). That is
stored-SQL presentation and moves with `stored` (22b), not here.

**Inheritance area (25 files, 5.7k lines, 2.3k clean).**

| Sub-area | Files | Lines / clean |
|---|---|---:|
| ALTER recursion | `inheritance_alter.go`, `inheritance_alter_admission.go`, `inheritance_alter_default.go`, `inheritance_alter_events.go`, `inheritance_alter_prepare.go`, `inheritance_alter_rename.go`, `inheritance_definition_reader.go` | 1.91k / 0.43k |
| CREATE merge, guards, validation, DROP | `table_inheritance.go`, `table_inheritance_definitions.go`, `table_inheritance_guards.go`, `table_inheritance_validation.go`, `inheritance_drop.go` | 0.69k / 0.36k |
| Recipes, retired constraints, Restore repair, remap | `inheritance_alter_recipes.go`, `inheritance_recipe_checks.go`, `inheritance_retired.go`, `inheritance_retired_restore.go`, `inheritance_retired_restore_syntax.go`, `inheritance_foreign_key_restore.go`, `inheritance_owner_restore.go`, `inheritance_control.go` | 2.29k / 1.40k |
| Query and DML expansion | `inheritance_query.go`, `inheritance_query_scope.go`, `inheritance_dml.go`, `inheritance_dml_authority.go` | 0.62k / 0.20k |
| `USING INDEX` preparation (index code) | `inheritance_using_index.go` | 0.16k / 0.07k |

**Also table code, filed elsewhere.** `alter_schema.go` (ALTER … SET
SCHEMA, 214 lines, dirty); `rename.go` (`renameRelationLocked` dirty,
`renameColumnCandidate` and `renameMissing` clean); `schemaTableLocked`
and `newTableDescriptorLocked` in `schema.go`; `prepareAlterTableActions`,
`alterTablePreparation` and `alterTableActionKind` in
`alter_tablespace.go`; the clean typed-table propagation in
`typed_inheritance.go` (`typedClosure`, `typedPropagationOrder`,
`typedInheritedActions`, `typedTransformAttributes`, about 0.2k lines; it
uses `columnTransformation`, so `typedef` cannot own it).

**Pipelines (orchestration stays in the root).**

- *CREATE TABLE:* `createTableLocked` (`create_table.go:13`) →
  `schemaTableLocked` (`create_table_stage.go:14`), or the partition variants in
  `partition_create.go`/`partition_create_child.go` → `newTableDescriptorLocked`,
  row type, `stageInheritance`, `nameCreateNotNull`,
  `stageCreateIdentities`/`stageCreateSerials`, `stageLikeColumns`,
  `bindCreateDefaults`/generations/checks, `stageLikeChecks`, definition
  OIDs, uniques, exclusions, `stageLikeIndexes`, `stageCreateForeignKeys`,
  then `stageCatalogOverlay` writes the journal.
- *ALTER TABLE:* `alterTableLocked` (`alter_table.go:36`):
  `prepareAlterTableActions` (`alter_table_prepare.go:24`) → inheritance
  recursion through `alterInheritanceLocked` (`inheritance_alter.go:188`),
  which re-enters `alterTableLocked` per node with
  `scope.inheritance.alter` set → guards → journal clone → a nine-pass loop
  ordered by `alterActionPhase` (`alter_table_phase.go:8`), each
  action calling a candidate function that returns the next
  `*catalog.Relation` (`identityCandidate`, `generationCandidate`,
  `dropColumnCandidate`, `addColumnCandidate`, `alterTypeCandidate`,
  `stageUsingIndex`, `stageAddedUnique`, `stageExclusion`,
  `notNullCandidate`) → CHECK and FK staging → row phase
  (`table.RewriteTypeRows`, row validation) → index work → `scope.catalog =
  changes`. `Engine.mu` is taken and released about 30 times inside.
  Publication happens at Finish (`transaction_finish.go`).
- *Admission:* lock modes and waits in `relation_scope.go`,
  `alterTableLockMode` (`alter_table_locks.go:8`),
  `acquireDefinitionProviders` (`definition_providers.go:14`), `admitInheritanceAlter`.

**Dirty declarations and why.** 6.5k lines are `Engine` methods (the
handlers and stagers above), 2.5k are `StatementScope` methods (FK
actions, `acquire*`, `finishInheritanceAlterCandidate`, rewrites), 1.0k
are functions that reach a god struct:

- through `AuthorizationView` (it holds `*Engine`): `foreignKeyDiagnostic`,
  `foreignKeyReferencesAllowed`, `restoredForeignKeys`,
  `physicalNotNullDumpDependencies`, the physical NOT NULL emitters, and
  every evaluation path through routine admission (`validateCheckRow`,
  `evaluateGenerations`, `prepareGenerationProgram`,
  `alterRowValidator.validate` → `validateExpressionJoinExecution` →
  `admitRoutineCall` → `admitRecipeRoutineDescriptor`; item 75 cut this
  path: these callers use `exec.PreflightCoercionsWithPolicy` and
  `exec.AdmitExpression`, and admission reads `exec.RoutineAccess`);
- through `dropProvider` (holds the scope and stage): `ordinaryColumnDrop`,
  `dropProvider.checkDependents`, `defaultDependents`,
  `foreignKeyColumnDependencies`, `foreignKeyDependents`;
- through `preparedDrop.scope` and `preparedIdentityDrop.scope` (an
  identity check only);
- through `inheritanceAlterState.stage` (`*CatalogStage`): every
  inheritance prepare and definition-reader declaration;
- through `partitionIndexCoverageComplete` (fixed by 22a).

**Simulated cuts** (`reach -cut`, lines clean in both areas):

| Cut | Clean |
|---|---:|
| none | 6.57k |
| 22a coverage cut (`partitionIndexCoverageComplete` and its method) | 6.80k |
| + `preparedDrop.scope`, `preparedIdentityDrop.scope` as tokens | — |
| + `inheritanceAlterState` without `*CatalogStage` | 7.14k |
| + `access.View` signatures and the 11e routine-admission seam (simulated by cutting `AuthorizationView>Engine`) | 7.30k |

**State.**

- `StatementScope`: the value group `inheritance inheritanceScope`
  (`inheritance_query_scope.go:247`: `relations`, `catalog`, `alter`); its
  `alter *inheritanceAlterState` field is read in 9 production files.
  `foreignActionsSlot *foreignActionScope` and `foreignActionAuth` (13
  files) belong to FK actions (`dml`). `dropsSlot *dropScope` belongs to the
  DROP pipeline (root).
- `CatalogStage`: `likeSources` (CREATE … LIKE only; read in
  `catalog_message.go`, `check.go`, `table_like.go`); `rowShapes`,
  `initialTables`/`initialTx`, `querySources`, `dependencyPath`,
  `schemaCascadeNames` are shared root fields. `sequence *sequence.Stage`
  carries identity staging.
- Journal and fences: `fenceStorageRead` (`storageReadFence`) and
  `fenceIndexSet` are declared in `catalog_fence.go`; publication owns
  their order.
- `Operation` replay slot (`restore_replay.go`, `restoreReplay`):
  `tableDestination`, `defaultReplay`, the partition flags. It stays with
  the Restore driver (`control`).

**Outside users.** 1,826 production and 1,053 test uses of the clean
set; the top ones are `dumpRelation` fields (69 uses of `.descriptor`),
`scalarRenderer` (stored), `columnTransformation`, `foreignKeyJob`,
`mutationRowIdentity`, `addedColumn`, `restoredNotNull`,
`remapLikeExpression`.

**Not table code** (moves elsewhere; do not move it here):

| What | Where | Item |
|---|---|---|
| `scalarRenderer` and its methods, `defaultSQL`, `renderColumnDefault`, `pgRenderer.foreignKeyConstraintSQL(WithPrimary)`, `foreignKeyActionSQL` (about 0.78k) | `stored` | 22b (68) |
| Hierarchy helpers, moved by 5i: `catalog.InheritanceChildren`, `InheritanceHasChildren`, `InheritanceParticipant`, `CloneInheritance`, `InheritanceQueryLayout`, `HierarchyRelation`, `SamePartitionColumnUsage` (`catalog/inheritance.go`). `sameRelationRowShape` (`relation_scope.go`) stayed in the root: it calls `analyze.SameStatementUsage` | `catalog`; `sameRelationRowShape` root until `SameStatementUsage` has a home below `analyze` | 5i (72) |
| Constraint naming, moved by 5i: `catalog.ConstraintNames`, `RelationConstraintNameExists`, `GeneratedConstraintName` (`nameCreateNotNull` stays here) | `catalog` | 5i (72) |
| Physical NOT NULL: 5i moved `catalog.PhysicalPrimaryForAttribute` and `CapturePhysicalPrimarySupport`. The validators `physicalNotNullOriginValid`, `physicalPrimarySupportValid` and `validatePhysicalNotNull` stayed in the root: they call `storedReferenceEpochValid` (root providers) and `indexCatalogKey` (`plan` nodes) | moved part: `catalog`; validators: root, re-measured in 23b/24b | 5i (72) |
| `generationHasTableOID`, `generationImmutable` (187 lines) | `plan` (`plan.GenerationHasTableOID`, `plan.GenerationImmutable`) | 10e (73), done |
| FK write-time enforcement (`checkForeignKeys`, `foreignKeyJob` scheduling in `foreign_key_writes.go`) and FK actions (`foreign_action_*.go`, `foreign_actions.go`) | `dml` | 29a/29b |
| Inheritance DML expansion (`mutation_targets.go` since 24a) | `dml` | 29a/29b |
| Inheritance query expansion: the scope methods and `inheritanceScope` (`inheritance_query_scope.go`) | root query admission (open question 1) | — |
| `inheritance_using_index.go` | `index` (renamed `using_index_prepare.go` in 23a) | 23a/23b |
| `typed_mutation.go` (dirty) and `typedObservationValid` | root | — |

The FK *probe* (`foreign_key_probe.go`: `findForeignKeyParent`,
`prepareForeignKeyChild`, `probeForeignKeyIndex`, `foreignKeyRows`) stays
here: ALTER TABLE validation (`validateForeignKeyData`), Restore and the
Finish-time checks all use it, and `dml` imports `table`.

## Part A

### 24a — In place

Do the steps in this order; each numbered step is one commit and builds.

1. **Re-measure.** Run `reach` and `frontier` over the files above at
   your base. 5i (item 72) is done; the helpers it left in the root are
   named under "Not table code" below. Do not move them here.
2. **Renames (file moves only; `git mv` plus moving whole declarations).**
   - `not_null.go` → `alter_table.go` (it holds `alterTableLocked` and
     `alterTableLookupKind`).
   - `temporary_table.go` → `create_table.go`; move `stageCatalogOverlay`
     and `stageCatalogChangesLocked` to a new root file
     `catalog_overlay_stage.go` (they are generic publication helpers).
   - `table.go` → `drop_table.go`.
   - `schemaTableLocked` and `newTableDescriptorLocked` out of `schema.go`
     into `create_table_stage.go`. 20d does not split `schema.go` (20c
     refine), so take them from there.
   - `prepareAlterTableActions`, `alterTablePreparation` and
     `alterTableActionKind` out of `alter_tablespace.go` into
     `alter_table_prepare.go`; the SET TABLESPACE checks stay.
   - `alterActionPhase` (`inheritance_alter_prepare.go`) and
     `inheritanceActionPass` (`inheritance_alter_events.go`) into
     `alter_table_phase.go`; `alterTableLockMode` into
     `alter_table_locks.go`.
   - `acquireDefinitionProviders` out of `check.go` into
     `definition_providers.go`; `recomputeRemappedGenerations` out of
     `generated.go` into `generated_remap.go`.
   - In `inheritance_control.go`: `checkTemporaryRelationSourcesReplacementLocked`
     to `temporary_preservation.go`; the file becomes `inheritance_remap.go`.
   - `inheritance_dml.go` and `inheritance_dml_authority.go` →
     `mutation_targets.go` (for `dml`).
   - `alter_schema.go` → `relation_set_schema.go`.
   Keep each Restore syntax parser next to its emitter
   (`physical_not_null_restore_syntax.go`,
   `inheritance_retired_restore_syntax.go`); do not merge them.
3. **Slot.** `CatalogStage.likeSources` becomes a table stage slot
   (pattern 2): one pointer field and one accessor, nil until CREATE … LIKE
   writes it. Readers: `catalog_message.go`, `check.go`, `table_like.go`.
4. **Cuts** (pattern 6 and signature cuts; each its own commit):
   - `preparedDrop.scope` and `preparedIdentityDrop.scope` become an opaque
     statement token compared by identity (they are only an identity
     check). 20c planned no `ddl` removal interface (only
     `SchemaHost.RemoveObject`), so there is nothing to coordinate with.
   - Split `inheritanceAlterState` (`inheritance_alter.go:18`) into a pure
     plan record (root, targets, selected, graph, actions, events,
     prepared, tablespace, types) and a root holder of `stage` and
     `changes`. Measured gain with the previous cut: +0.34k lines; the
     definition reader, prepared nodes and actions become clean.
   - `foreignKeyDiagnostic`, `foreignKeyReferencesAllowed` and
     `restoredForeignKeys` take `access.View` and `access.Environment`
     instead of `AuthorizationView` (they only read its view data).
   - The `dropProvider` dependency queries
     (`dropProvider.checkDependents`, `defaultDependents`,
     `foreignKeyColumnDependencies`, `foreignKeyDependents`) and
     `ordinaryColumnDrop` become functions over a small dependency reader
     that `dropProvider` implements.
   - `dumpRelation.emitMaterializedSnapshot` (`materialized_snapshot.go`,
     a view file) becomes a function taking the dump record, so the type's
     methods all live in table files and the function can move with
     `view` (27b). View 27a has the same item: whichever runs first does
     it, the other skips it.
   - `dumpRelation.emitRows` and `emitPhysicalNotNull` call two partition
     dump helpers. They need no interface: index 23a moves
     `partitionReplaySeparateConstraint` (a replay-shape predicate over
     indexes) into `index`, and `partitionHashReplayTarget` (no root
     dependencies) moves with `table` in 24b. `table` imports `index` and
     never `partition`.
5. **Docs commit** (§8.4) with the measured result.

**Open questions** (answer in the 24a result; ask before deviating):

1. Settled during planning: inheritance query expansion is called by root
   query admission (`query_admission.go`, `query_table_refs.go`), not
   by DML executors. Its clean helpers (`captureInheritanceQueryPrefix`,
   `inheritanceQueryClosure`, `inheritanceQueryLeaves`, `sourceOnly`) read
   stored view leaves and nothing in the root, so they move with `table`
   in 24b; the scope methods (`acquireInheritanceSources`,
   `inheritanceRead`) and the `inheritanceScope` value group stay with
   query admission. `inheritanceQueryLayout` goes to `catalog` (5i).
2. `bindCreateDefaults` (`default.go`) is clean but drives a binder passed
   in (`b.BindDefault`). Under rule 6 an upper feature never builds a
   binder; calling a passed one is borderline. Recommendation: keep it in
   the root as the body of a host binding method (24c).
3. `projectColumnShapeRoots` (`column_projection_change.go`) uses
   `catalog_transaction_storage.go` projections (`projectVersions`,
   `newColumnProjection`). Leave it in the root unless a storage-side
   step moves those projections.

### 24b — Create `table` and move the clean set (broad)

1. Re-measure after 24a, 5i, 21b, 22b, 23b and B6. The clean set should be
   about 5.6k lines: the 7.3k measured after the cuts minus what goes
   elsewhere (stored 0.78k, catalog 0.27k, plan 0.19k, dml 0.25k, root
   0.25k).
2. Move, by file (whole files where all of it is clean):
   - NOT NULL: `not_null_candidate.go`, `nameCreateNotNull`, the clean part
     of `physical_not_null.go` (`physicalNotNullFromType`,
     `prunePhysicalNotNull`, `removeDetachedNotNullMetadata`,
     `transitionPhysicalNotNull`), `physical_not_null_competing.go`,
     `physical_not_null_control.go`, `physical_not_null_restore_syntax.go`
     and the clean parts of the other `physical_not_null_*` files.
   - CHECK: `orderedChecks`, `checkDepends`, `checkDependencyEdge`,
     `validateCheckProviders`, `validateChecks`, `checkDetailError`;
     `alter_constraint*.go` clean parts; `renameLocalConstraint`.
   - DEFAULT and generated: `default_dependencies.go` clean part,
     `defaultReplayDeferrable`, `validateCookedDefaultTemporary`,
     `generationDepends`, `generationRecipeExpression`,
     `validateGenerationPlan`, `validateGenerationProviders`,
     `generated_virtual.go` clean part.
   - FK DDL: `foreign_key.go` clean part, `foreign_key_probe.go`,
     `foreign_key_lifecycle.go` clean non-renderer part,
     `foreign_key_restore.go` clean part, `alterForeignKeyAdmission`,
     `alterTableLockMode`.
   - ALTER records: `addedColumn` (`columnTransformation` is in `index`
     after 23a: index evolution uses it), `alterTypePreparation`, `alterTypeColumn`, `alterTypeNamespace`,
     `dropColumnMetadata`, `alter_type_check.go` clean part,
     `preparedAddedChecks`, `alterActionPhase`, `inheritanceActionPass`,
     `renameColumnCandidate`.
   - Dump: `dumpRelation` with all its methods (`table_dump.go`,
     `constraint_dump.go`, `not_null_restore.go`, the dump files above and
     the inheritance emitters), `restoredNotNull`,
     `restoreAttributeRank*`, `restoreAlterConstraints`.
   - Inheritance: `inheritance_recipe_checks.go`,
     `inheritance_retired.go`, `inheritance_retired_restore.go` and its
     syntax file, `inheritance_alter_recipes.go`,
     `inheritance_foreign_key_restore.go` (clean part),
     `inheritance_owner_restore.go` (clean part),
     `table_inheritance_guards.go`, `table_inheritance_validation.go`,
     `inheritance_remap.go`, the pure parts of `inheritance_alter.go`
     (`inheritanceAlterOrder`, `inheritanceOnlyActions`,
     `inheritanceRecursiveAction`, `inheritedNotNull`), the plan record of
     `inheritanceAlterState`, `inheritance_definition_reader.go`,
     `inheritance_alter_events.go` clean part.
   - Typed tables: the clean part of `typed_inheritance.go`.
   - Inheritance query expansion: the clean helpers of open question 1;
     `partitionHashReplayTarget` (`partition_dump.go`).
3. Give the package its own `errors.go` (a `sqlError` copy), as every
   subpackage has.
4. Check imports: `table` may import `analyze` types and pure functions,
   `stored`, `regobj`, `dump`, `index`, `typedef`, `sequence`, `catalog`,
   `access`, `storage`, `plan`, `exec` types, `expr`, `ids`, `parser`,
   `sqltype`, `sqlstate`. It must not import `partition`, `view`, `dml`,
   `control` or the root. `go list -f '{{.Imports}}'` proves it.
5. Alias policy: qualify references directly with `movex` (as 14b–17b
   did). The move is broad (about 1.4k production references outside
   the set, plus tests), so if the diff is too large for one commit, move
   with type aliases and remove them in the same sub-step.
6. Commit sequence: export in place (`expspec`, `movex member`); move
   (`movespec`/`mv.sh`); qualify; docs commit.

## Part B

### 24c — Refine the host (docs only; linear item 88)

Measure the remaining handlers with `reach` and `frontier`, starting from
the draft sketch (`table.Host` = `ddl.Host` plus descriptors, row types,
binding, type resolution, identity staging and a `Removal()` accessor),
then write the final interfaces, the move set and the commit sequence into
this file, and wait for approval. Five questions were open: (1) which ALTER
candidates move; (2) where identity and serial staging and
`relationDiagnosticKind` go; (3) how index decisions are reached; (4)
whether the inheritance recursion decisions move; (5) the lock contract of
every host method.

**24c refine (approved; measured at `bdba4968a`).** `frontier` over the 67
root files that still hold table code (the inventory files left in the
root after 24b, plus `identity.go`, `identity_alter.go`,
`identity_drop_definition.go`, `serial.go`, `sequence_dependencies.go`,
`typed_mutation.go`, `rename.go`, `relation_set_schema.go` and
`drop_dependency_reader.go`; FK write-time and action files,
`mutation_targets.go`, `catalog_overlay_stage.go` and
`temporary_preservation.go` left out; output in
`.cswd/tmp/PG18-ENGINE-SPLIT-TABLE-REFINE/frontier-table.txt`): 180
declarations, about 270 engine edges. `createTableLocked`,
`schemaTableLocked`, `alterTableLocked`, `alterInheritanceLocked`,
`prepareInheritanceNode`, the drop provider and the Restore hooks were read
directly. The 85a seam (`index.Host`, `index.KeyHost`, `index.DropStage`,
`index.ValidityTarget` on `ddlHost`) was read on its branch; 85a merged
(`0ab96a3a8`) before this record and its result agrees. Findings:

- **The shared DDL host covers most CREATE edges.** `allocateOIDLocked`
  and `revisionLocked` are `Allocator`; `stage.Base`, `stageRead`,
  `stage.Create`/`Replace`/`Get`/`UpdateDescriptor` are `StageBase`,
  `StageRead` and `Stage()`; `op.Context`, `op.EmitNotice` and the
  temporal-modifier WARNING are `Context` and `EmitNotice`;
  `creationACL` is `CreationACL`; `referencePath` is
  `RecordDependencyPath`; `e.creationNamespace` is
  `ExistingCreationNamespace`. `AuthorizationView` fields the decisions
  read (`Catalog`, `Owner`, `Roles`, `CanUse`, `Check`, `CheckCreate`, the
  temporary namespace, `domainOf(e)`, `e.catalog.Domain.Ref`) are all on the
  `access.View` that `AccessView(auth)` returns. Existing `ddlHost` methods
  fit as they are: `StageRowType`, `ResolveType`, `ResolveColumnCollation`,
  `DisplaceAssociatedArray`, `AssociatedArrayName`, `CreationTypeACL`
  (typedef), `AddDomainBase`, `RemoveObject`, `RelationDiagnostic`,
  `TrustedRestoreDestination` (its "destination" text is
  `create_table.go`'s), and the 85a `index.Host` methods.
- **ALTER needs the scope.** Its candidates read `scope.input`, which the
  comment and CHECK snapshot captures (`captureCommentSnapshotLocked`,
  `captureCheckCommentSnapshotLocked`, `captureCheckVisibilitySnapshotLocked`)
  and `captureTypeDiagnosticName` mutate in place, `scope.routineAuth`,
  `scope.inquiry.deparsePath`, `scope.output` and the sequence generator
  (`sequenceGeneratorLocked`). `ddlHost` is scope-less by design.
- **Lock contracts the sketch did not foresee.** `alterTableLocked` holds
  `Engine.mu` through admission, unlocks at line 168 and then opens 14
  windows of its own; its callees open more. Three candidates break the
  "under `Engine.mu`" rule today: `generationCandidate` binds with no lock
  at all, `bindColumnChecks(altering=true)` and `rebindAlterTypeChecks`
  take `Engine.mu` once per CHECK, `rebindAlterTypeIndexes` once per key
  and predicate. None of those windows contains a wait.
- **Waits** stay where they are: `stageAlterIdentityTypes`, identity SET
  and DROP (`acquireSequence`), DROP COLUMN/CONSTRAINT providers
  (`Acquire`, `lockInheritanceClosure`), `acquireDefinitionProviders`,
  `acquireForeignKeyTypeProviders`, `acquireSerializableSnapshot`,
  `admitInheritanceAlter`, `acquireInheritanceDefinitions`. Row work stays
  too: `projectColumnShape`, `transportIndexLayouts`, FK validation.
  (Correction, 24h: `rewriteTypeRows`, `validateAddedConstraintRows` and
  `planAddedColumns` needed only roots and an evaluation template; they
  are `table` functions now. Correction, 24i: the FK provider walks and
  the FK validation scans are `table` functions too, over a row-capture
  callback; the grant loops and the authority captures stay.)
- **Removal has mixed contracts.** Of `table.DropDependencyReader`'s 18
  methods about 8 are generic reads; `Acquire`, `OwnedExpressionDependencies`
  and `CompositeFieldDependencies` wait, `ApplyViewDependents` takes
  `Engine.mu`, the rest take no lock. The prepared drops (`preparedDrop`,
  `preparedIdentityDrop`) are fences on statement identity that replay
  into `CatalogStage` internals.
- **Restore.** Each repair hook takes `operationAuthorization` and the
  trust check (`partitionConstraintReplayImport`) under `Engine.mu`, four
  begin and publish their own stage; their decisions read only a catalog
  view and an authority.
- **Regression found and fixed** (``e6176d3c3``): 24b moved
  `discoveryReader()` into `table` unexported, so the anonymous-interface
  assertion in `prepareDropDefinitionProvider` never matched and drop
  discovery during inheritance admission recorded into the node reader's
  observations (a stricter fence than intended). It is now
  `table.InheritanceDefinitionReader.DiscoveryReader`, asserted on the
  concrete type in `dropDiscoveryInput`; unit test
  `TestDropDiscoveryInputLeavesInheritanceFenceUnobserved`.
- **Not needed** (the sketch had them): `NewTableDescriptor` (a function
  over `ddl.Allocator`), `AssignDefinitionOIDs` (likewise),
  `ResolveColumnType` (CREATE column types come from analysis through
  `ResolveTableColumnType`, which has the Restore `"char"` exception; ALTER
  uses `ResolveType` and `ResolveColumnCollation`, the operation's
  authority, exactly as today), `StageIdentitySequence` (a `table`
  function) and `Removal()` (decision 6).

Decisions:

1. **Two hosts, like `index.Host`/`WriteHost`.** Every method of both
   runs with `Engine.mu` held, never takes or releases it, and never waits.
   - `table.Host` lives on the shared `ddlHost` (new root
     `table_host.go`, `var _ table.Host = ddlHost{}`): CREATE TABLE, the
     CREATE SCHEMA element, PARTITION OF children, LIKE, identity and
     serial columns, FK staging. It is `ddl.Host` + `index.Host` +
     `ddl.TypeStaging` plus:
     - already on `ddlHost`, declared again: `StageRowType(*catalog.Relation)
       error`, `ResolveType(parser.TypeName) (sqltype.Usage, error)`,
       `ResolveColumnCollation(sqltype.Usage, *parser.ObjectName)
       (sqltype.Usage, error)`;
     - stage slots: `TableStage() *Stage` (`CatalogStage.table()`),
       `SequenceStage() *sequence.Stage` (`sequenceState()`),
       `SequenceColumns() []catalog.Column` (`sequence.Columns` over the
       live state, not the stage, as `identity.go` and `serial.go` read
       it), `NameBinding(ids.NameKey) (ids.ObjectAddress, bool)` (the
       stage's own binding; `Stage().LookupName` filters removals and
       vacated names), `StageChanged() bool` (descriptor, removal or old
       name count non-zero; `StageRead` alone would clone the state and
       change `auth.Catalog`'s identity);
     - authority: `StagedTemporary(auth ddl.Authority, namespace
       *catalog.Namespace) ddl.Authority` (sets `stagedTemporary` for the
       session's own non-TOAST temporary namespace; it takes the
       descriptor because identity staging reads it from the stage);
     - resolution: `ResolveReference(auth ddl.Authority, name
       parser.ObjectName) (ids.ObjectRef, error)` (`resolveReference`, the
       positioned 42P01/42501 of analyzed names: FK targets, DROP TABLE,
       restored defaults) and `ResolveRestoredRelation(auth, name)
       (ids.ObjectRef, error)` (`resolveRelationReference(…, false)`, the
       Restore repairs). `ResolveRelation` is taken: it is role's utility
       variant;
     - binding, bodies whole in the root (rule 6): `DescribeCreateTable(auth,
       s *parser.CreateTable, input sqltype.InputEnvironment)
       (analyze.Binding, error)` (`describe` over `analysisHost`);
       `BindColumnDefaults(auth, columns []catalog.Column, declarations
       []analyze.ColumnDeclaration, input sqltype.InputEnvironment,
       temporary ids.SessionToken) (map[int32]catalog.ColumnDefault,
       error)` (binder, `DefaultTemporary`, `bindCreateDefaults`; the
       PARTITION OF child passes the zero token, as today);
       `BindGenerations(auth, rel *catalog.Relation, declarations
       []analyze.ColumnDeclaration, input) error` (`bindCreateGenerations`
       with one shared namespace); `BindChecks(auth, rel, raw
       []*parser.CheckConstraint, input, inherited
       map[string]catalog.CheckConstraint) error` (`bindColumnChecks`
       without the `altering` lock).
   - `table.AlterHost` = `Host` plus the scope-bound methods, on a new root
     `alterHost{ddlHost; scope *StatementScope}` (`var _ table.AlterHost =
     alterHost{}`). Embedding gives it the `index.Host` methods too.
     - `Input() sqltype.InputEnvironment`: the live `scope.input`; `table`
       re-reads it before every binding call because captures mutate it;
     - captures: `CaptureTypeDiagnosticName(auth) error`,
       `CaptureCommentSnapshot() error`, `CaptureCheckSnapshots(rel
       *catalog.Relation, visibility bool) error`;
     - binding: `CookDefault(auth, rel *catalog.Relation, slot int, raw
       parser.Expr, withDefinition bool) (expr.Expr, catalog.ColumnDefault,
       error)` (binder and renderer; ADD COLUMN passes `op.Definition`,
       SET DEFAULT does not, so the flag keeps both renderings);
       `BindGeneration(auth, rel, slot int, raw parser.Expr)
       (catalog.ColumnGeneration, error)`; `RebindCheck(auth, rel, ref
       ids.ObjectRef, raw parser.Expr, literals []plan.LiteralExpr)
       (expr.Expr, error)`; `BindAlterTypeUsing(auth, original
       *catalog.Relation, ref ids.ObjectRef, column string, using
       parser.Expr, usage sqltype.Usage) (expr.Expr,
       []catalog.ExpressionDependency, error)`;
       `OperationDefinition() catalog.DefinitionReader` (`op.Definition`,
       not 23c's stage `DefinitionReader()`);
     - dependents: `TypeChangeDependents(auth, target ids.ObjectAddress)
       error` (the policy, rule and view 0A000 checks of ALTER TYPE);
     - identity: `StageIdentitySequenceOptions(table *catalog.Relation,
       attribute int32, sequence ids.ObjectAddress, raw
       []parser.SequenceOption) error` (`stageIdentitySequenceOptionsLocked`;
       the caller already holds the sequence's SRE grant).
   - The CREATE SCHEMA element keeps `ddl.SchemaHost.StageSchemaTable` (`ddl`
     is below `table`); its body becomes a forwarder to
     `table.StageCreateTable`. `fdw.Host.StageForeignTable` likewise until
     26e.
2. **ALTER candidates** (question 1). The root wrappers hold `Engine.mu`
   across each call; for `generationCandidate`, `prepareAddedChecks`,
   `rebindAlterTypeChecks` and `rebindAlterTypeIndexes` the window only
   widens, and nothing in it waits (23c decision 2's reasoning).
   - Move: `addColumnCandidate` (the wrapper keeps `scope.routineAuth =
     auth`), `alterTypeCandidate`, `generationCandidate`, the TYPE trio
     (`admitAlterTypeLocked`, `completeAlterTypeLocked`,
     `prepareAlterTypeLocked`), `prepareAlterTableActions` with
     `alterTableActionKind` (after `alterTablespaceKind` and
     `checkAlterTablespaceCreate` go to `ddl`), `stageAddedUnique` (calls
     `index.StageUniqueDefinitions`), `prepareAddedChecks`,
     `stageAddedForeignKey` with `stageForeignKey`,
     `captureAlterTypeIndexes` (a plain function of context, view, deparse
     path and output), `rebindAlterTypeIndexes` (over `index.Host`'s
     binders), ADD IDENTITY, and the post-loop rebuilds
     `RebuildAlterNotNulls`, `RebuildAlterForeignKeys`,
     `RebindPhysicalNotNullType` (over `ddl.Stage`), and
     `remapPhysicalNotNull` (TEMP preservation's; a function of the next
     state, its read, the previous descriptors and a ref builder).
   - Split: SET IDENTITY (`PrepareIdentitySet` → the root waits and
     stages → `FinishIdentitySet`; numeric errors still precede the
     conflict error), DROP IDENTITY (decision 6), `rebindAlterTypeChecks`
     (binder behind `RebindCheck`), `stageAddedChecks` (the prepared-node
     cache selection stays), `alterDefaultLocked` (guards and the next
     descriptor as `AlterDefaultCandidate`) and `cookAlterDefaultLocked`
     (behind `CookDefault`), `renameConstraintLocked` (`RenameConstraint`
     for lines 36–110; the root keeps resolution, the inheritance branch
     and the stage lifecycle), and the pure pieces of the loop: the ONLY
     SET NOT NULL rewrite, the CHECK syntax collection (shared with the
     prepare side), `storedReplacements`, the no-change predicate and
     `dependencyTargets`.
   - Index's (85a): the root loop calls `index.StageExclusion` and
     `index.CaptureUsingIndexes` directly; `stageUsingIndex` and
     `prepareUsingIndexMetadata` stay root wrappers (`index.StageUsingIndex`
     runs unlocked over `UsingIndexStage`), called from the loop and the
     inheritance prepare side.
   - Already in `table`: `NotNullCandidate`, `UsingIndexNotNullCandidate`,
     `OrdinaryColumnDrop`.
   - Stays: the `alterTableLocked` skeleton (admission, journal clone,
     its windows, the nine-pass loop, row phase, install); the waits and
     row work listed above; `dropColumnCandidate` (a provider builder);
     `stageDroppedConstraintIndexes`; `stageAlterIdentityTypes`,
     `alterIdentityTypeSequence`, `refreshIdentitySequenceLocked`; column
     projection; `recomputeRemappedGenerations`,
     `repairRemappedGenerationsLocked`; `typedMutation`, `stageTypedRows`;
     `acquireAlterForeignKeys`, `acquireForeignKeyDefinitionProviders`.
     The stage is begun with partial authority at three sites
     (`alter_table.go` twice, `alter_constraint_drop.go`); keep them as they
     are (pure refactor).
3. **Identity and serial** (question 2). CREATE staging moves:
   `StageCreateIdentities`, `StageIdentitySequence`, `StageCreateSerials`
   and the unexported `ownedSequenceName` over `SequenceStage`,
   `SequenceColumns`, `StagedTemporary`, `ExistingCreationNamespace` and
   `CreationACL(…, DefaultSequences)`; allocation order unchanged. ALTER's
   option, refresh and type staging stays in the root behind
   `StageIdentitySequenceOptions`; the decisions are already in
   `sequence`. The identity DROP decision moves over the extended reader
   (decision 6); `preparedIdentityDrop` and its prepare/consume stay.
   `relationDiagnosticKind` is not table code (10 production callers in
   drop diagnostics, domains, indexes, roles and the schema host): it
   becomes `catalog.RelationDiagnosticKind` (pure over the relation kind;
   `ddl` cannot import `stored`, and `alterTablespaceKind` needs it in
   `ddl`). `ddlHost.RelationDiagnostic` keeps its signature.
   `evaluateInsertValuesRow` (`identity.go`) is DML: it goes with `dml`
   (29a/29b), not here.
4. **Index decisions** (question 3). As 23c decision 5 and the 85a seam
   built it: `table.Host` embeds `index.Host` (and so `index.KeyHost`),
   and `table` calls the 23d.1 `index` functions directly. The edges 85a
   hoists to the callers (`captureLikeSource` before unique staging,
   `rebuildAlterNotNullsLocked` after `RebuildAlterUniques`, the USING
   INDEX NOT NULL preparation) become table code in 24d.1/24d.2.
5. **Inheritance recursion** (question 4). The decisions move with no
   host: `PlanInheritanceAlter(view, root, raw)` (the guard, ONLY-primary
   XX000, targets and plan initialization of `alterInheritanceLocked`
   lines 29–62; the root unlocks on its error), `InheritedAlterActions(plan,
   view, retired catalog.Read, old)`, `FinishInheritanceAlterCandidate(plan,
   view, old, next)`, and nil-safe `*InheritanceAlterPlan` methods
   `RecordAction`, `RecordChecks`, `NotNullMode`, `OnlyCreatedConstraint`
   (the root adds `scope.inheritancePlan()`, nil when not recursing); the
   inheritance DEFAULT core (`InheritanceDefaultCandidate`) and the rename
   cores (`RenameInheritanceColumn[Precheck]`,
   `RenameInheritanceConstraint[Precheck]`). The prepare side stays in the
   root: `prepareInheritanceNode` replays phases 0–6 of the loop that
   stays, mixes all three lock contracts in one loop, needs scope state
   (`active`, `txRef`, `holdsColumnDropGrant`), sets the root-private
   `auth.definition`, and records root prepared drops. So do
   `inheritancePreparedNode`/`Action`, `consumeInheritancePreparedAction`,
   `admitInheritanceAlter` and `acquireInheritanceDefinitions`.
6. **Removal** (the `ddl` removal interface step 20 left to 24c). No
   `ddl.Removal` and no `Removal()` host method: no other feature needs
   one (fdw, role and the view 27c sketch run dependents as root
   orchestration), half of the reader is table-shaped
   (`DomainDependency`, inheritance guards), and its waits break `ddl`'s
   "under `Engine.mu`, never waits" rule. `table.DropDependencyReader`
   stays the table-local removal interface, built by the root
   (`stageDropProvider`, `prepareDropDefinitionProvider`) and passed as an
   argument, as `OrdinaryColumnDrop` already takes it. Its comment says
   "callers hold nothing" and marks which methods wait or take
   `Engine.mu`. 24d.2 adds `Remove(ids.ObjectRef) error`,
   `ExpressionDependencies(roots []ids.ObjectAddress, mode
   parser.DropMode, subject ids.ObjectAddress) error` (waits), a generic
   `Notice(sqlstate.Error)` and the identity hooks
   `CaptureIdentitySequence(table *catalog.Relation, attribute int32, seq
   ids.ObjectRef) error` (waits, then one window) and
   `TombstoneIdentity(ids.ObjectAddress)`, so `dropConstraintIndexes`,
   `ownedSequences` and the DROP IDENTITY decision move. Prepared drops
   (`preparedDrop`, `prepare/finish/consumePreparedDrop`,
   `holdsColumnDropGrant`) stay root; `table` never names them.
7. **Restore and dump.** The root wrappers stay the hook bodies (control
   30c reaches them); their cores move:
   - A, whole over a `catalog.Read`: `captureRestoredPlaceholders`,
     `captureRestoredNotNulls`, `captureHistoricalFacts`,
     `retiredArchiveHighWater`, `capturePhysicalImportIndex`,
     `remapRetiredConstraints` (shares a high-water helper with
     `retiredArchiveHighWater`), `validateForeignKeyData` (over `access.View`
     and `access.Environment`), the records `restoredPhysicalNotNull` and
     `restoredPhysicalForeignKeyProvider` (their authority field becomes a
     `RoleRecord`), and `physicalNotNullDumpDependencies` over a
     `dump.Resolver` (the `routine.NormalizeOperatorDumpPredecessors`
     precedent). Control 30c moves it the same way if it lands first
     (control 30b); then 24d.3 skips it. 30c landed first:
     `table.PhysicalNotNullDumpDependencies` exists, so 24d.3 skips it.
   - B, split with no stage: `pairRestoredGeneration`,
     `captureRestoredGeneration`, `closeRestoredRecipesForData`,
     `restoreInheritedCheck`, `pairRestoredNotNull`,
     `captureInheritedOwner`, `restoreInheritedOwner`,
     `captureInheritedForeignKey`, the unchanged-check of
     `validateRestoredInheritedForeignKey` (`(*RestoredInheritedForeignKey).Unchanged`),
     `preparePhysicalNotNullConstructor`,
     `capturePhysicalNotNullConstructor`, `prepareDeferredPhysicalPrimary`,
     `preparePhysicalCompetingUnique`, `capturePhysicalForeignKeyProvider`,
     `capturePhysicalSelfForeignKey`, `validateRestoredDefault`. The root
     keeps the context check, `operationAuthorization`, any superuser
     42501 that precedes the lock, the window, the trust check and
     resolution; the core takes `catalog.Read` or `access.View` +
     `access.Environment`.
   - C, split around their own stage: `restoreRetiredConstraint` (per-row
     parents through `ResolveRestoredRelation`, then
     `CommitRetiredRepair` after publication), `restoreNoInheritNotNull`,
     `restorePhysicalNotNull`, `restoreAttributeRank`. The core returns
     `next` (nil for no change); one root helper takes the shared tail
     (begin, authorize, unlock, `Replace`, validate, publish with a nil
     scope). Trust checks and error order differ per hook, so the wrappers
     stay separate.
   - D, stays: `bindRestoredForeignKeys` (stage, rows, publication),
     `resolveRestoredForeignKeyTargets` (`lookupNamespacesSnapshot` skips
     the epoch check that `SearchNamespaces` makes),
     `captureRestoredForeignKeys`, `restore_replay.go`.
   (Correction, 24i: `bindRestoredForeignKeys` keeps the stage and
   publication; its target-path fallback and its row scan are
   `RestoredForeignKey.ResolveTarget` and `table.ScanRestoredForeignKeys`.)
   Restore's unlocked `resolveRelationReference` calls and
   `bindRestoredForeignKeys`'s unlocked `BeginCatalogStage` run on the
   private single-goroutine restore engine; keep them as they are.
8. **CREATE side.**
   - Move: `schemaTableLocked` as `StageCreateTable(h Host, auth, s
     *parser.CreateTable, ns ids.ObjectAddress, owner
     authorization.PrincipalRef, input sqltype.InputEnvironment,
     foreignTargets []ids.ObjectRef) error` (same order; the 42P07 NOTICE
     through `EmitNotice`); `newTableDescriptorLocked` as `NewRelation(a
     ddl.Allocator, …)` (OID then revision; typedef's
     `NewCompositeRelation` forwards to it and sets the kind);
     `assignDefinitionOIDsLocked` as `AssignDefinitionOIDs(a ddl.Allocator,
     d)`; `stageInheritance`; LIKE (`captureLikeSource` as
     `(*Stage).CaptureLikeSource`, `stageLikeColumns`, `stageLikeChecks`,
     `stageLikeIndexes`); `stageCreateForeignKeys`; `typedObservationValid`
     (pure over `*typedef.TypedStage`).
   - Split: `validateLikeSources` (pure body to `(*Stage).ValidateLikeSources`;
     the `CatalogStage` method forwards), `dropTableLocked` (`DropTargets`:
     the resolve, kind and ownership loop), `alterSchemaLocked` (four pure
     cores: owned sequences, the group, candidates, conflicts;
     `catalogStore.advanceRevisionFrontier` never reads its receiver and
     becomes a function), `prepareQueryTableLocked` (column derivation as
     `QueryTableColumns`, `CanUse` in column order), `createTableLocked`'s
     persistence checks (optional).
   - Stays: `createTableLocked` (unlock, partition finish, typed sources,
     providers, overlay or publication, conflict recheck), CREATE TABLE AS
     rows and publication (`stageQueryTableRows`, `validateInitialTables`,
     `publishQueryTable`, `executeQueryTableLocked`),
     (Correction, 24i: the `validateInitialTables` body is
     `table.ValidateInitialTables`.)
     `renameRelationLocked` (generic relation rename with eight callers;
     optional pure cores), `stageCheckDependents` (test-only; moves to a
     test file), `schemaPartitionTableLocked` (partition 25).
9. **Hand-offs.** Partition 25c builds on `table.Host`:
   `schemaPartitionChildLocked` needs exactly the CREATE set
   (`StageCreateIdentities`, `BindColumnDefaults`, `NewRelation`, ...), so
   `partition.Host` can embed `table.Host`. fdw 26e calls the moved
   `table` functions directly and drops the two forwarders. Control's
   Restore hooks keep their root bodies (decision 7). `ddl` gets
   `alterTablespaceKind`, `checkAlterTablespaceCreate` (next to
   `TablespaceRef`) and `GeneratedRelationName` (85a).
10. **24d splits** into 24d.1 (CREATE, linear item 89a), 24d.2 (ALTER,
    89b, needs 89a) and 24d.3 (Restore and dump cores, 89c, needs 89a).
    Partition 25d, fdw 26e and DML 29c need 89a and 89b; the end-state
    review needs all three.

### 24d — Move the handler decisions (linear items 89a, 89b and 89c)

Follow the 24c refine above. One sub-step per session. The handlers'
orchestration (`Engine.mu`, lock waits, journal clone and install,
Finish) stays in the root and calls the moved decisions through the host.
Moved names are capitalized; no aliases.

**24d.1 move set (item 89a; needs 88 and 85a, both done).** Into `table`:

- `host.go`: `Host`, `AlterHost` (declared here; implemented in 24d.2).
- `create.go`: `StageCreateTable`, `NewRelation`, `AssignDefinitionOIDs`,
  `CheckCreatePersistence` (optional).
- `inheritance_create.go`: `StageInheritance`.
- `like.go`: `(*Stage).CaptureLikeSource`, `(*Stage).ValidateLikeSources`,
  `StageLikeColumns`, `StageLikeChecks`, `StageLikeIndexes`.
- `identity.go`/`serial.go`: `StageCreateIdentities`,
  `StageIdentitySequence`, `StageCreateSerials`, `ownedSequenceName`.
- `foreign_key_stage.go`: `StageForeignKey`, `StageCreateForeignKeys`.
- `drop.go`, `set_schema.go`, `query_table.go`: `DropTargets`, the four
  SET SCHEMA cores, `QueryTableColumns`.
- `typed.go`: `TypedObservationValid`.

Into `catalog`: `RelationDiagnosticKind`. Root: `table_host.go` (the
`ddlHost` methods, `alterHost` skeleton), `ddlHost.NewCompositeRelation`
and `StageSchemaTable` and `StageForeignTable` as forwarders.

**24d.2 move set (item 89b; needs 89a).** Into `table`: the ALTER
candidates and splits of decision 2, the inheritance decisions of decision
5, `DropDependencyReader`'s additions and the decisions they enable
(`DropConstraintIndexes`, `OwnedSequences`, DROP IDENTITY). Into `ddl`:
`AlterTablespaceKind`, `CheckAlterTablespaceCreate`. Root:
`alterHost` methods.

**24d.3 move set (item 89c; needs 89a).** Into `table`: the Restore and
dump cores of decision 7, groups A–C.

**Stays in the root** (all three): `createTableLocked`, `alterTableLocked`
and its loop, `alterInheritanceLocked` and the prepare side,
`dropTableLocked`, `alterSchemaLocked`, `renameRelationLocked` and
`renameConstraintLocked` as orchestration, CREATE TABLE AS execution and
publication, every wait, row and storage path listed in 24c, the DROP
pipeline (`drop_provider.go`), the binder bodies behind the host methods,
`restore_replay.go`, the Restore hook wrappers, and the thin wrappers that
build a host for callers outside the moved set.

**Commit sequence (24d.1).**

1. Pure helpers: `catalog.RelationDiagnosticKind` (10 call sites);
   `NewRelation`, `AssignDefinitionOIDs` over `ddl.Allocator`;
   `typedObservationValid`; `stageCheckDependents` to its test file.
2. Seam: `table.Host` and `table.AlterHost`; root `table_host.go` with the
   assertions and the new `ddlHost` methods. Unit test first, observed
   failing: `BindColumnDefaults` through the host binds the same defaults
   as `schemaTableLocked`, and `StageChanged` reports a staged descriptor.
3. Port in place: root functions over `table.Host` for decisions 3 and 8,
   naming no `Engine`, `CatalogStage`, `AuthorizationView` or
   `StatementScope`; old entry points become thin wrappers.
4. Move into `table` with `movex` and qualify; no aliases.
5. Docs: `table` README (host, entry points, what stays), this Status,
   the §8 row, the status page; `AGENTS.md` unchanged.

**Commit sequence (24d.2).**

1. `ddl.AlterTablespaceKind`, `ddl.CheckAlterTablespaceCreate`; the plan
   methods of decision 5 and `scope.inheritancePlan()`.
2. Seam: `alterHost` methods; the root wrappers take `Engine.mu` around
   `generationCandidate`, `prepareAddedChecks`, `rebindAlterTypeChecks`
   and `rebindAlterTypeIndexes` (bodies lose their inner windows). Unit
   test first, observed failing: `Input()` reflects a capture made through
   `CaptureCheckSnapshots`.
3. `DropDependencyReader` additions on `*dropProvider`.
4. Port in place (decisions 2, 5, 6), then move with `movex`; no aliases.
5. Docs.

**Commit sequence (24d.3).**

1. Port the cores in place (groups A–C), the root wrappers calling them;
   the shared stage-tail helper.
2. Move with `movex`; no aliases. 3. Docs.

Proof for each: the engine units of "Targeted tests" (the ALTER and
inheritance lock and cancellation units also with `RACE=1`), the reference
selections in quick and `REFERENCE=1` mode, and quickcheck. 24d.2 also
checks that the ALTER first-error order is unchanged on the multi-action
reference cases. 24d.1 and 24d.3 also run the Dump→Restore→Dump smoke of
24b (`.cswd/tmp/PG18-ENGINE-SPLIT-TABLE-PACKAGE/dumpsmoke`): output
byte-identical.

### 24e — Relation validation and generation remap (linear item 123)

Planned by the end-state review (linear item 114, ENGINE-SPLIT.md §2.8 and
§6 "Third round"), measured at `17e01ca8b`. Needs —.

- `relationProvider.Validate` (`catalog.go:45`, 280 lines, clean) moves
  to `table`. Its three calls above `table`
  (`view.ValidatePolicies`, `partition.ValidateDescriptor`,
  `view.ValidateViewDefinition`) are passed in as an ordered validator
  table that the root builds once (pattern 5), in today's order.
- `recomputeRemappedGenerations` (`generated_remap.go`) takes the
  check-snapshot capture (`:49-60`) as a callback.
- `tablespaceOnlyActions` and `validateAlterTablespaceMove`
  (`alter_tablespace.go`, clean).
- Correct the `catalog` README sentence on "validators that need engine
  services".

Verify with `make build`, the targeted tests below plus the unit tests of
the touched files, the quick and `REFERENCE=1` selections for the area, and
quickcheck; no behavior, error text or error-order change, no aliases.

### 24f — The remaining CREATE and ALTER decisions (linear item 124)

Planned by the end-state review (linear item 114, ENGINE-SPLIT.md §2.8 and
§6 "Third round"), measured at `17e01ca8b`. Needs 24e (item 123).

- The non-binding core of `bindColumnChecks` (`check.go:25-155`: naming,
  inherited merge, duplicate diagnostics, ordinals, the dependency
  renderer) behind one `BindCheckExpression` host method; delete the
  test-only `bindCreateChecks`.
- The target-admission table of `acquireRelationTarget`
  (`relation_scope.go:161-324`, pure over `parser.Statement`) as a
  registration table (§3 rule 8); the acquire loop stays.
- The ALTER TABLE OF checks (`typed_table.go:41-79`) and source collection
  (`:115-148`), the CREATE TABLE shape errors (`create_table.go:54-90`) and
  the `alterTableLocked` scan plan (`alter_table.go:701-736`); the
  skeleton stays (24d).
- One `analyze.RelationNamespace` helper replaces the copies in
  `alter_host.go`, `alter_type.go`, `check.go`, `generated.go` and
  `partition_host.go`.

Verify with `make build`, the targeted tests below plus the unit tests of
the touched files, the quick and `REFERENCE=1` selections for the area, and
quickcheck; no behavior, error text or error-order change, no aliases.

### 24g — The inheritance prepare side (linear item 125)

Planned by the end-state review (linear item 114, ENGINE-SPLIT.md §2.8 and
§6 "Third round"), measured at `17e01ca8b`. Needs 24f (item 124), view 27e
(item 115).

- `prepareInheritanceNode` (`inheritance_alter_prepare.go:41-215`)
  behind a six-method prepare host (`prepareUsingIndexMetadata`,
  `addColumnCandidate`, `prepareColumnDropAfter`,
  `prepareIdentityDropDefinition`, `prepareConstraintIndexDrop`,
  `prepareAddedChecks`), the prepared drops crossing as
  `table.DefinitionDelta` values that also expose their next relation.
- `inheritanceQueryLeaves` (clean; it calls `stored.ViewLeafRowMark` since
  27e) and the pending-grant decision of `acquireInheritanceSources`
  (`inheritance_query_scope.go:104-141`).
- Delete the test-only `lockInheritanceDropClosure` and
  `inheritanceStageAlterGuard` wrappers. Correct the `table` README and
  `ENGINE-SPLIT-dml.md` notes that keep `inheritanceQueryLeaves` in the root.

Verify with `make build`, the targeted tests below plus the unit tests of
the touched files, the quick and `REFERENCE=1` selections for the area, and
quickcheck; no behavior, error text or error-order change, no aliases.

### 24h — ALTER row rewrite and scan (linear item 134)

Planned by the closing review (linear item 133, ENGINE-SPLIT.md §2.9 and
§6 "Fourth round"), measured at `4c05da8fe`. Needs —.

`rewriteTypeRows` (`alter_type.go`, about 160 lines),
`validateAddedConstraintRows` (`alter_constraint_rows.go`) and
`planAddedColumns` (`alter_add_column.go`) reach the engine only through
`*StatementScope`: the projection roots (`columnProjectionRoots`),
`hostOf(scope)`, the input and output environments and
`routineAuth.Catalog`. Cut: the caller passes the write and delete roots
and a ready `exec.Context` template (the routine authority's catalog for
the rewrite and the add-column fills, none for the scan, exactly as
today). The callers are `alter_table.go` and `typed_mutation.go`; both
call with nothing held, and expressions may still reach host methods that
take `Engine.mu`, as today. `table` already imports `exec`, `expr`,
`storage`, `visibility`, `index` and `catalog`. Remove the README sentence
that keeps the row rewrites in the root. `alter_add_column_candidate_test.go`
and `alter_generation_test.go` call `rewriteTypeRows`; adapt them.

Verify with `make build`, the unit tests of the touched root files and
the target package, quickcheck, and the quick and `REFERENCE=1` selections
for ALTER TABLE type changes, added columns and constraints, typed tables; no behavior, error text, error-order or lock-order change, no
aliases. Re-run `reach` on the moved set first: sizes are approximate.

### 24i — FK provider plans, validation loops and inheritance decisions (linear item 135)

Planned by the closing review (linear item 133, ENGINE-SPLIT.md §2.9 and
§6 "Fourth round"), measured at `4c05da8fe`. Needs 134 (both edit `alter_table.go`).

FK decisions:
- `installedForeignKey` (reads only `stage.Base`), the parent-index
  capture of `acquireAlterForeignKeys` and `validateAddedForeignKeys`
  (`alter_foreign_key.go`), with `validateAlterForeignKeys`
  (`foreign_key_lifecycle.go`) merged into one validation function;
- the target walks of `acquireForeignKeyTypeProviders` and
  `acquireForeignKeyDefinitionProviders` (with the latter's post-grant
  check);
- the target-path fallback and the post-stage validation pass of
  `bindRestoredForeignKeys` (`foreign_key_restore.go`).

Inputs: a `captureForeignKeyRows` callback (it takes `Engine.mu`; the
callers hold nothing), a `catalog.Read`, and an `access.View` with its
`access.Environment`. The `foreignKeyGrant` loops and the authority
capture windows stay root.

Inheritance decisions:
- `dropProvider.inheritanceColumnDropMultiple` (`view_dependencies.go`)
  over the `table.InheritanceAlterPlan` and the base `catalog.Read`
  instead of the provider;
- the closure walk and sort of `admitInheritanceDropClosure`
  (`inheritance_drop.go`); the acquire loop stays root, as in 27h;
- the body of `CatalogStage.validateInitialTables`
  (`query_table_create.go`) over a descriptor getter.

Verify with `make build`, the unit tests of the touched root files and
the target package, quickcheck, and the quick and `REFERENCE=1` selections
for foreign keys (ALTER, CREATE, Restore), inheritance DROP and CREATE TABLE AS; no behavior, error text, error-order or lock-order change, no
aliases. Re-run `reach` on the moved set first: sizes are approximate.

### 24j — Definition-provider collection and deparse inquiries (linear item 136)

Planned by the closing review (linear item 133, ENGINE-SPLIT.md §2.9 and
§6 "Fourth round"), measured at `4c05da8fe`. Needs —.

- The collection half of `StatementScope.acquireDefinitionProviders`
  (`definition_providers.go`): the walk over LIKE sources, view leaves
  and dependencies, CHECKs, defaults, generations and index keys, the
  retained map, the sort and the lock mode per ref (inheritance parents
  differ). It becomes a `table.Stage` method next to
  `ValidateLikeSources`, over `stage.Base`, a `ddl.Stage`, the staged
  descriptors and the domain, returning `{Ref, Mode}` requests and the
  retained map. The acquire loop, the post-wait checks under `Engine.mu`
  and the final `validateLikeSources` stay root.
- The body of `statementHost.DeparseInquiry` and `deparseRelation`
  (`deparse_function.go`) and `evalSerialSequence` (`serial_sequence.go`)
  over a `table.DeparseInputs` value (catalog, path, output, nonstandard
  strings, temporary namespace, database and a relation resolver), built
  by the root from `scope.inquiry`; the missing-snapshot check stays
  root. It goes to `table`, not `view`, because it needs
  `regobj.ClassQualifiedName` and `view` does not import `regobj`. This
  settles the regobj 21d note.
- `validateSelectedDomainRows` (`domain_alter.go`, clean).

`table` already imports `stored`, `lock`, `ddl`, `exec`, `functions`,
`index`, `plan` and `regobj`.

Verify with `make build`, the unit tests of the touched root files and
the target package, quickcheck, and the quick and `REFERENCE=1` selections
for LIKE, CREATE TABLE providers, `pg_get_*def`, `pg_get_expr`, `pg_get_serial_sequence` and ALTER DOMAIN; no behavior, error text, error-order or lock-order change, no
aliases. Re-run `reach` on the moved set first: sizes are approximate.

## Invariants

- ALTER TABLE keeps its nine-pass order (`alterActionPhase`), the
  inheritance pass order (`inheritanceActionPass`) and per-node re-entry,
  so the same first error is reported for every action list.
- Lock order: the lock manager is innermost; ALTER releases `Engine.mu`
  before waits (`acquireDefinitionProviders`, identity type staging); the
  rewrite path takes `scope.owner.Mu` before `Engine.mu`.
- Finish order stays: `checkExclusionEvents` → `settleForeignActions` →
  `checkExclusionEvents` → `checkForeignKeys` → row publication →
  `catalogPreflightLocked` → `prepareIndexPublication`. Fence kinds stay
  declared centrally in `catalog_fence.go`.
- Dump output is byte-identical: `dumpRelation` emitters keep their order;
  no map iteration leaks into output; each Restore syntax parser stays
  next to the emitter whose output it reads.
- Restore replay flags stay on the operation's replay slot; no new state
  on `StatementScope`.
- No new mutex. The package holds no engine state.

## Targeted tests

- Unit tests follow the code (ENGINE-SPLIT §4): tests of moved pure code
  move into `table`; tests that need an engine fixture stay in the root and
  are rewritten to the new names, or deleted when `reference/` covers
  them (name the covering test). A test is never a reason to keep code in
  the root. Grep `*_test.go` for `notNullCandidate`, `dumpRelation`,
  `columnTransformation`, `inheritance`, `foreignKey`, `generation`,
  `restoreRetired`, `physicalNotNull`.
- `RACE=1` for ALTER TABLE cancellation and lock waits
  (`AlterDropColumnWaitCancellation…`, `AlterIdentityWaitCancellation`).
- Reference selections, quick mode and `REFERENCE=1` (never modify
  `reference/` or `smoke/`): `^TestReference(TableCore|TableLike|Like)`,
  `^TestReferenceAlter(AddColumn|AddConstraint|Default|DropColumn|Generated|Identity|Nullability|Rename|Schema|Tablespace|Type)`,
  `^TestReference(NotNull|Check|Default|GeneratedStored|GeneratedVirtual)`,
  `^TestReference(ForeignKey|ForeignActions)`, `^TestReferenceInheritance`,
  `^TestReference(ConstraintRename|TypedTable|Identity|Serial|Truncate)`,
  and the native Reset/Dump/Restore tests of these families (names with
  `Controls`, `Restore` or `Dump`, plus `^TestReferenceDumpRound`). Known
  drift on `REFERENCE=1` (`AlterNullability(Names|ActionLists)`,
  `NotNull(FormsAndAlter|CurrentCommandNamesAndCompatibleReuse)`) also
  fails on master; report it, do not chase it.

## Documentation

- Create `internal/engine/table/README.md` (24b): what it owns, why
  inheritance is part of it, what stays in the root (orchestration, the
  query expansion, FK actions until `dml`), invariants, `## Testing`.
- Move table selectors out of `internal/engine/README.md` `## Testing`.
- `AGENTS.md` must not grow: add `table` to the subpackage bullet only.
- ENGINE-SPLIT.md §6 step 24 and §8: correct what the work proved wrong
  (open questions above).

## Done when

Part A: the misleading files are renamed, the cuts are in, and the clean
set lives in `table`. Part B: the CREATE/ALTER TABLE and inheritance
decisions live there too, behind `table.Host`. The checks of §8.3 pass.

## Status

- [x] 24a — In place (renames, slot, cuts) — linear item 86
  (`56270ba2f`, `4dcdf411c`, `b210c6777`, `260ed66c6`, `5b4ac930a`,
  `36249b2b4`, `e247d07c6`, then the docs commit) · selected unit and
  dual-mode proof and quickcheck passed; racecheck/finalcheck skipped at
  user request
  - Re-measured at `e68b939e8` (`reach` over the 73 inventory files that
    still exist): 16.6k lines, 5.93k clean (5i had moved its helpers out).
    After the cuts, over the same declarations plus the files 24a created:
    6.12k → 6.91k clean (+0.79k). Clean now: the definition reader and
    prepared inheritance nodes, `inheritanceParentActions`,
    `inheritanceRemovedOrigin`, `inheritRetiredColumnConstraints`,
    `ordinaryColumnDrop`, the four dependency passes, `foreignKeyDiagnostic`,
    `foreignKeyReferencesAllowed`, `restoredForeignKeys`. Outputs in
    `.cswd/tmp/PG18-ENGINE-SPLIT-TABLE-IN-PLACE/` (`reach-*.txt`,
    `delta.py`).
  - `56270ba2f` (renames): as listed in 24a step 2. Two placements differ:
    `discardRemovedWrites` went to `catalog_overlay_stage.go` with its only
    caller `stageCatalogOverlay`, and `temporaryDataTarget` (read-only DML
    admission) to `temporary_access.go`; neither is CREATE TABLE code.
    The two `inheritance_dml*.go` files merged into `mutation_targets.go`.
    Rebased over B6, which had changed `alterTableActionKind`; the move was
    redone from the new body. Final base `e6f9c647b` (routine 14e; no
    overlap).
  - `4dcdf411c` (slot): `tableStage{likeSources}` behind
    `CatalogStage.tableSlot`, `table()`/`tableOrZero()` in `table_stage.go`.
  - `b210c6777`: `preparedDrop.statement any` replaces `scope`; it is only
    compared with the consuming scope. `preparedIdentityDrop` had no scope
    field of its own (it holds `drop *preparedDrop`), so one cut covers
    both.
  - `260ed66c6`: `inheritanceAlterPlan` (the pure record) embedded in
    `inheritanceAlterState`, which keeps `stage` and `changes`.
    `inheritRetiredColumnConstraints` takes the read it names against;
    `inheritanceAlterState.retiredBase` supplies it.
  - `5b4ac930a`: `foreignKeyDiagnostic`, `foreignKeyReferencesAllowed` and
    `uniqueKeyDetailAllowed` take `access.View` and `access.Environment`
    (index 23a cut a needs the same signature for `uniqueKeyDetailAllowed`;
    it is already in). Correction: `restoredForeignKeys` keeps the declaring
    authority for later staging (`stageForeignKey`,
    `lookupNamespacesSnapshot`), so it cannot hold a view; its field is the
    opaque `ddl.Authority`, recovered with `ddlAuthorityOf`.
  - `36249b2b4`: `dropDependencyReader` (`drop_dependency_reader.go`, 18
    methods, not the small reader the plan expected: `ordinaryColumnDrop`
    also calls the view, composite and owned-sequence passes) implemented
    by `*dropProvider`; `checkDependents`, `defaultDependents`,
    `foreignKeyColumnDependencies`, `foreignKeyDependents` and
    `ordinaryColumnDrop` are functions over it. The unused wrappers
    `foreignKeyColumnDependencies(ctx, stage, ...)` and
    `stageForeignKeyDependents(Locked)` were deleted.
  - `e247d07c6`: `emitMaterializedSnapshot(ctx, t *dumpRelation, ...)`;
    view 27a item 9 is done.
  - Step 4's partition dump helpers needed no 24a code (see Notes).
  - Open questions: 1 as settled; 2 `bindCreateDefaults` stays in the root
    (24c host binding); 3 `projectColumnShapeRoots` stays in the root.
  - Tests: none moved or deleted. Literals and calls were adapted in
    `table_like*_test.go`, `table_inheritance_admission_test.go`,
    `inheritance_retired_test.go`, `inheritance_alter_test.go`,
    `alter_tablespace_prefix_test.go` and
    `primary_key_authorization_test.go`.
- [x] 24b — `table` created and clean set moved — linear item 87 (`7e9873c11`,
  `b05bbcdf9`, `8797fe715`, `a0ef76b6c`, `e0eb9d15d`, `30ed6f576`, then the
  docs commit) · selected unit and dual-mode proof and quickcheck passed;
  racecheck/finalcheck skipped at user request; see the 24b record below
- [x] 24c — Host refined (docs only) — linear item 88 (`e6176d3c3`, then
  the docs commit) · `table.Host` on `ddlHost`, `table.AlterHost` on a
  scope-bound `alterHost`; no `ddl.Removal`; 24d split into 24d.1–24d.3 ·
  the 24b regression fix (`e6176d3c3`) passed its unit test, the inheritance
  and drop units (also `RACE=1`), `^TestReference(Inheritance|AlterDropColumn)`
  in quick and `REFERENCE=1` mode, and quickcheck; racecheck skipped at user
  request
- [x] 24d.1 — CREATE decisions moved — linear item 89a (`89660982b`,
  `383a943e6`, `3c0c77d50`, `7f9263745`, then the docs commit) · selected
  unit (also `RACE=1`) and dual-mode proof, Dump→Restore→Dump smoke and
  quickcheck passed; racecheck/finalcheck skipped at user request; see the
  24d.1 record below
- [x] 24d.2 — ALTER and inheritance decisions moved — linear item 89b
  (`dbaf9d8f9`, `b7ed253d3`, `4bb411191`, `b542587c5`, `e39ec498a`, `f4f982aa9`,
  then the docs commit) · selected unit (also `RACE=1`) and dual-mode proof
  (`REFERENCE=1` apart from drift that fails on master too) and quickcheck
  passed; racecheck/finalcheck skipped at user request; see
  the 24d.2 record below
- [x] 24d.3 — Restore and dump cores moved — linear item 89c (`a6936164a`,
  `44f1ae9bf`, then the docs commit) · selected unit (also `RACE=1`) and
  dual-mode proof, Dump→Restore→Dump smoke and quickcheck passed;
  racecheck/finalcheck skipped at user request; see the 24d.3 record below
- [x] 24e — Relation validation and generation remap moved — linear item
  123 (`20e2675f2`, `77e73dff4`, `9e28f488c`, then the docs commit) ·
  selected unit (also `RACE=1`) and dual-mode proof and quickcheck passed;
  racecheck/finalcheck skipped at user request; see the 24e record below
- [x] 24f — Remaining CREATE and ALTER decisions moved — linear item 124
  (`98a193227`, `479d57f07`, `3e48a8de5`, `8f6d54f40`, `4c97e1cd9`, then
  the docs commit) · selected unit (also `RACE=1`) and dual-mode proof and
  quickcheck passed; racecheck/finalcheck skipped at user request; see the
  24f record below
- [x] 24g — Inheritance prepare side moved — linear item 125 (`0124428f1`,
  `319ad1289`, `35e3d98ae`, then the docs commit) · selected unit (also `RACE=1`) and
  dual-mode proof and quickcheck passed; racecheck/finalcheck skipped at
  user request; see the 24g record below
- [x] 24h — ALTER row rewrite and scan — linear item 134 (`b79597f14`,
  `656dc171c`, then the docs commit) · selected unit (also `RACE=1`) and
  dual-mode proof and quickcheck passed; racecheck/finalcheck skipped at
  user request; see the 24h record below
- [x] 24i — FK provider plans, validation loops and inheritance decisions
  — linear item 135 (`82bded1cd`, `9d38f7d39`, then the docs commit) · selected
  unit (also `RACE=1`) and dual-mode proof and quickcheck passed;
  racecheck/finalcheck skipped at user request; see the 24i record below
- [x] 24j — Definition-provider collection and deparse inquiries — linear
  item 136 (`5d66c4037`, `7ef360259`, then the docs commit) · selected unit
  (also `RACE=1`) and dual-mode proof and quickcheck passed;
  racecheck/finalcheck skipped at user request; see the 24j record below

Outstanding aliases: none (24a and 24b added none).

### 24b record

Done on `implement-part/PG18-ENGINE-SPLIT-TABLE-PACKAGE`, started at
`42a45f774` (index 23b merged) and replayed onto `e2fef61ae` (stored 22d,
role 28c, index 23c: the three cuts cherry-picked, the move commits
rebuilt by the scripts in `.cswd/tmp/PG18-ENGINE-SPLIT-TABLE-PACKAGE/`,
`c1-moves.sh`, `c2.sh`, `c3.sh`, `docs-edit.py`), then rebased onto
`9528f845f` (role 28d; it touched two root call sites). Re-measured after
the replay: the same 250 declarations; 22d had removed the stored aliases, so
OUT was `sqlError` alone. No aliases.

| Commit | Change |
|---|---|
| `7e9873c11` | Cut: the inheritance definition reader overlays prepared drops through `definitionDelta` (`DefinitionDelta`, `LayoutDelta`, implemented by `*preparedDrop` and `*preparedIdentityDrop`); `prepared` moves from `inheritanceAlterPlan` to the root holder `inheritanceAlterState` |
| `b05bbcdf9` | Cut: `restoredInheritedForeignKey.validate` (reads the engine) becomes the root function `validateRestoredInheritedForeignKey` |
| `8797fe715` | `synchronizeRowTypeMetadata` → `typedef.SynchronizeRowTypeMetadata` |
| `a0ef76b6c` | Staging: the clean declarations of 49 mixed files to `tblmv_*.go` (`movedecl`) |
| `e0eb9d15d` | Export in place: `dropDependencyReader`'s methods and `*dropProvider`'s implementations (`ifacern`, 14 methods), then the inbound functions, fields and methods (`expspec`/`movex`, 355 rows) |
| `30ed6f576` | Move: 63 files to `table`, `table/errors.go`, `movex -qual table`, 21 root locals named `table` renamed `tbl` |

- **Re-measure.** `reach` over the 24a files plus `typed_inheritance.go`,
  `rename.go`, `domain_dependencies.go` and `partition_dump.go`: 465
  declarations, 281 clean (7.27k of 18.5k lines). Minus the "Not table
  code" rows (FK actions and `foreignKeyJob` for `dml`,
  `mutation_targets.go`, `materialized_snapshot.go` for `view`,
  `partition_dump.go` for `partition`) and the 24a answers
  (`bindCreateDefaults`, `projectColumnShapeRoots`), the `frontier` fixed
  point gave 250 declarations, about 6.3k lines (the plan expected 5.6k;
  `partitionHashReplayTarget` had already gone to `index` in 23b). OUT was
  then `sqlError` and three stored aliases.
- **Blockers the plan did not list** (approved before the move):
  `inheritanceDefinitionEffect`, `inheritancePreparedAction`/`Node` and the
  plan record's `prepared` map held the root DROP records
  (`preparedDrop`, `preparedIdentityDrop`); without the first cut the
  reader, the plan record, `inheritanceParentActions`,
  `inheritanceRemovedOrigin`, `inheritRetiredColumnConstraints` and
  `prepareInheritanceRowType` (about 0.4k lines) could not move. Each
  effect held exactly one kind of drop, so the lookup order is unchanged.
  `restoredInheritedForeignKey.validate` was the only dirty method on a
  clean type.
- **Stayed in the root** (they call root-only code):
  `inheritancePreparedAction`/`inheritancePreparedNode` (24c question 4),
  `alterTableActionKind` (the SET TABLESPACE check),
  `inheritanceQueryLeaves` (`viewLeafRowMark`, query admission),
  `physicalNotNullDumpDependencies` (the dump driver; `dumpPredecessor`),
  `validateRestoredInheritedForeignKey`.
- **Deviation.** `captureDomainReplayNode` (table dump rows) was planned for
  `typedef`, but it calls `regobj.Literal` and `regobj` imports `typedef`;
  it moved with `table` (`domain_default_restore.go`), its only caller
  being `DumpRelation.emitRows`.
- **Names.** Every moved name is capitalized; `tableStage` is `Stage`. The
  embedded plan record makes the holder's field `InheritanceAlterPlan`.
  Unexported types named by exported signatures were exported
  (`DefinitionDelta`, `RestoredInheritedForeignColumn`, `RetiredRepair`,
  `RetiredRepairRow`); `restoredGenerationKey`'s fields were exported by
  hand because root code builds it with unkeyed literals, which `frontier`
  does not report. `table` imports no `partition`, `view`, `dml`,
  `control` or root package (`go list`).
- **Tests.** None moved or deleted: every table unit test builds an engine
  or uses root fixtures; they name the moved code as `table.X`. The 558
  unit tests of the 154 root test files that reference `table` passed, the
  38 ALTER/inheritance/NOT NULL lock and cancellation tests passed with
  `RACE=1`.
- **Checks.** String-literal multiset unchanged apart from
  `table/errors.go`; a Dump→Restore→Dump smoke (six scenarios, one with
  inheritance, NOT NULL, NO INHERIT CHECK, virtual generation and NOT VALID
  FK) produced byte-identical output before and after; quickcheck passed;
  the prompt's reference selection passed in quick mode and with
  `REFERENCE=1` apart from known drift (see the report). racecheck,
  finalcheck and bench skipped at user request (no startup change).

### 24c record

Done on `implement-part/PG18-ENGINE-SPLIT-TABLE-REFINE` from `bdba4968a`
(table 24b merged). The refine is in Part B above. One code commit:

- `e6176d3c3` fixes a 24b regression the measurement found: the
  `discoveryReader()` assertion in `prepareDropDefinitionProvider` named an
  unexported method that had moved to `table`, so it never matched (see the
  findings). Lesson for later moves: grep the root for anonymous-interface
  assertions with unexported methods (`.(interface {` followed by a
  lower-case method) before moving their implementers; the build cannot
  catch them. No other such assertion exists in `internal/engine`.

Notes for 24d (from index 23d.1, item 85a, merged in `0ab96a3a8`):

- 85a moved `table.IncompatibleNotValid` and `table.RejectVirtualIndexUses`
  down to `index.IncompatibleNotValid` and `index.RejectVirtualUses`, and
  hoisted the table edges: CREATE TABLE and PARTITION OF record each LIKE
  source before `index.StageCreateUniques`; ALTER TABLE and typed mutation
  run `rebuildAlterNotNullsLocked` between `index.RebuildAlterUniques` and
  `RebuildAlterExclusions` (it returns its parent when nothing changes);
  `prepareUsingIndexMetadata` runs `table.UsingIndexNotNullCandidate` first.
- Root wrappers left for 24d: `stageUsingIndex` and
  `prepareUsingIndexMetadata` (ALTER TABLE, inheritance prepare),
  `stagePartitionConstraintDrop`, `stagePartitionIndexChild(ren)`.
  `index.StageUsingIndex` takes the narrow `UsingIndexStage` and runs
  unlocked; `index.StageUniqueDefinitions`/`StageCreateUniques` take no
  authority; `index.Host` also has `ValidityTarget()`.
- `alterTableActionKind` needs the SET TABLESPACE check
  (`alterTablespaceKind`, decision 2); `inheritanceQueryLeaves` needs
  `viewLeafRowMark` (view 27b may move both into `view` or keep them with
  query admission).
- View 27c (item 101): `viewLeafRowMark` stayed in the root
  (`query_lock_admission.go`); it is pure over `stored.View` and
  `plan.RowMark`, so `stored` can take it if 24d.2 moves
  `inheritanceQueryLeaves`. `creationError` becomes `ddl.CreationError`;
  24d.1 and view 27d both need it, and whichever lands first moves it.
  CREATE MATERIALIZED VIEW (`prepareQueryTableLocked`, decision 8) calls
  `view.FreezeDefinition` after 27d (today `freezeViewDefinitionLocked`),
  and its `OldNameCount`/reown pair matches the new
  `ddlHost.ReownDisplacedTypes`.

### 24d.1 record

Done on `implement-part/PG18-ENGINE-SPLIT-TABLE-CREATE-DECISIONS` from
`116432550` (view 27b merged), rebased onto `949c682a1` (index 23d.2 and
the 25c, 27c and 30b refines) and then `78ec4c49d` (DML 29b); code
conflict-free, only the status page conflicted. Re-measured with `frontier`
over the nine CREATE files: 19 members, about 30 engine edges, all of them
on `ddl.Host`, `index.Host`, `access.View` plus the `ddl.Authority`
environment, or the new host methods. After the port the staged set's OUT
was `sqlError` alone.

| Commit | Change |
|---|---|
| `89660982b` | Pure helpers: `catalog.RelationDiagnosticKind` (9 call sites), `catalog.AdvanceRevisionFrontier` (the receiver-free `catalogStore` method; its unit test moved to `catalog`), `session.ExistingRelation` (the 42P07 skipping NOTICE); `table.NewRelation`, `table.AssignDefinitionOIDs` over `ddl.Allocator`, `table.TypedObservationValid` over the typed stage; `stageCheckDependents` to `check_dependents_helpers_test.go` |
| `383a943e6` | Seam: `table/host.go` (`Host`, `AlterHost`), root `table_host.go`; `TestTableHost*` written first and observed failing (missing methods) |
| `3c0c77d50` | Port in place into `tblmv_*.go` over `table.Host`; `(*table.Stage).CaptureLikeSource`/`ValidateLikeSources`; `DropTargets` over the stage-less `table.Resolver`; the four SET SCHEMA cores; `QueryTableColumns`; `CreateCheckSyntax` |
| `7f9263745` | Move into `table` (script `.cswd/tmp/PG18-ENGINE-SPLIT-TABLE-CREATE-DECISIONS/c4.py`), root callers qualified; six emptied root files deleted; no aliases |

- **Moved** (about 1.5k lines, plus the 118-line `host.go`): `StageCreateTable` (was `schemaTableLocked`'s
  body), `StageInheritance`, `StageLikeColumns`, `StageLikeChecks`,
  `StageLikeIndexes`, `StageCreateIdentities`, `StageIdentitySequence`,
  `StageCreateSerials`, `ownedSequenceName`, `StageForeignKey`,
  `StageCreateForeignKeys`, `CreateCheckSyntax`, `NewRelation`,
  `AssignDefinitionOIDs`, `DropTargets`, `SetSchemaOwned`, `SetSchemaGroup`,
  `SetSchemaCandidates`, `SetSchemaConflict`, `QueryTableColumns`,
  `TypedObservationValid`, the two LIKE source methods.
- **Root left**: `schemaTableLocked` (a forwarder next to
  `createTableLocked`; 4 production and 17 test callers), the
  `validateLikeSources` forwarder (`table_stage.go`), `StageSchemaTable`,
  `StageForeignTable` and `NewCompositeRelation`, which now reach the
  `table` functions; `bindCreateChecks` (PARTITION OF still calls it) over
  `table.CreateCheckSyntax`. Deleted: `create_table_stage.go`,
  `table_like.go`, `table_like_index.go`, `table_inheritance_definitions.go`,
  `serial.go`, `foreign_key.go`, `typed_inheritance.go`.
- **Deviations.** (1) `AlterHost` is declared but the root `alterHost` is
  not added yet: a method-less skeleton would be dead code; 24d.2 adds it
  with its methods. (2) `ResolveReference` sits in a small `table.Resolver`
  (`AccessView` + `ResolveReference`) that `Host` embeds, so `DropTargets`
  runs before DROP TABLE begins a stage (the root passes a stage-less
  `ddlHost`). (3) The optional `CheckCreatePersistence` was not split:
  `createTableLocked` keeps its checks, and `creationError` stays in the root
  for view 27d to move (nothing 24d.1 moved calls it). (4) SET SCHEMA's
  conflict core takes a `catalog.Read`; the root wraps
  `stageCatalogBaseLocked`'s state with `e.catalog.read`. (5) LIKE indexes
  are in `like_index.go` (the plan listed `like.go`), serial staging in
  `serial.go`, `QueryTableColumns` in the existing `query_table_create.go`.
- **Op-less hosts.** `identityCandidate` (ADD IDENTITY) and
  `stageAddedForeignKey` (ALTER … ADD FOREIGN KEY) call
  `StageCreateIdentities`/`StageForeignKey` with `ddlHost{e, stage}` and no
  operation: neither function reads `Context` or `EmitNotice`, and a unit
  test calls `identityCandidate` without a scope. 24d.2's `alterHost` should
  supply the operation.
- **Tests.** Moved: `TestCatalogStoreRevisionFrontier` →
  `catalog.TestAdvanceRevisionFrontier` (the allocator check went with the
  receiver); the UTF-8 assertion of `TestSerialAtomicDeclarationsAndNaming`
  → `table.TestOwnedSequenceNameTruncatesToValidUTF8`. New:
  `TestTableHostBindColumnDefaultsMatchesCreateTable`,
  `TestTableHostStageChangedReportsStagedDescriptor`. Adapted to the new
  names: 21 root test files (locals named `table` in `typed_table_test.go`,
  `typed_type_lifecycle_test.go` and `partition_unique_replay_test.go`
  renamed `tbl`). None deleted.
- **Checks.** String-literal multiset unchanged apart from the new tests and
  the frontier test's messages; the 24b Dump→Restore→Dump smoke
  byte-identical; 272 selected engine units passed, also with `RACE=1`;
  `table` imports no root, `partition`, `view`, `dml` or `control` package;
  no anonymous-interface assertion in the root names an unexported moved
  method.

### 24d.2 record

Done on `implement-part/PG18-ENGINE-SPLIT-TABLE-ALTER-DECISIONS` from
`9156a1120` (control 30c merged), rebased onto `10581f8cc` (table 24d.3);
the code replayed without conflicts, only the `table` README conflicted.
Re-measured with `frontier` at `2f96d483d`: the ALTER files gave 39
members, the inheritance files 13. About 2.1k lines moved into `table`; the
root lost about 1.8k.

| Commit | Change |
|---|---|
| `dbaf9d8f9` | Pure helpers: `ddl.AlterTablespaceKind`, `ddl.CheckAlterTablespaceCreate` (over `access.View`); the four inheritance event functions as nil-safe `*table.InheritanceAlterPlan` methods (`inheritance_alter_record.go`) behind `scope.inheritancePlan()`; the dead `stageOwnedSequenceDependencies` deleted |
| `b7ed253d3` | Seam: `table.AlterHost` revised, root `alterHost{ddlHost; scope}` (`alter_host.go`); `TestAlterHostInputReflectsCheckSnapshotCapture` written first and observed failing (`undefined: alterHost`); the candidate wrappers take `Engine.mu` across the call |
| `4bb411191` | `DropDependencyReader` gains `Remove`, `ExpressionDependencies`, `Notice`, `CaptureIdentitySequence`, `TombstoneIdentity` and its lock-contract comment; `MissingColumnNotice` became a `Notice` call |
| `b542587c5` | The ALTER candidates and post-loop rebuilds ported straight into `table` |
| `e39ec498a` | The loop pieces, constraint rename, inheritance and drop decisions ported straight into `table` |
| `f4f982aa9` | The SET TABLESPACE kind assertions moved to `ddl.TestAlterTablespaceKind` |

- **Moved**: `AddColumnCandidate`, `GenerationCandidate`,
  `AdmitAlterType`, `CompleteAlterType`, `PrepareAlterType`,
  `AlterTypeCandidate`, `PrepareAlterTableActions`, `AlterTableActionKind`,
  `PrepareAddedChecks`, `StageAddedUnique`, `StageAddedForeignKey`,
  `CaptureAlterTypeIndexes`, `RebindAlterTypeIndexes`,
  `RebindAlterTypeChecks`, `AlterDefaultColumn`, `AlterDefaultCandidate`,
  `CookAlterDefault`, `AddIdentityCandidate`, `PrepareIdentitySet`,
  `FinishIdentitySet`, `DropIdentity`, `RebuildAlterNotNulls`,
  `RebuildAlterForeignKeys`, `RebindPhysicalNotNullType`,
  `RemapPhysicalNotNull`, `OnlySetNotNull`, `AddedCheckSyntax`,
  `StoredReplacements`, `DependencyTargets`, `RenameConstraintKind`,
  `StageRenameConstraint`, `PlanInheritanceAlter`, `InheritedAlterActions`,
  `FinishInheritanceAlterCandidate`, `InheritanceDefaultCandidate`,
  `RenameInheritanceColumn[Precheck]`,
  `RenameInheritanceConstraint[Precheck]`, `DropConstraintIndexes`,
  `OwnedSequences`, and `insertError` (`table/errors.go`).
- **Root left**: the locking wrappers that build the host
  (`addColumnCandidate`, `generationCandidate`, `alterTypeCandidate`,
  `prepareAlterTypeLocked`, `prepareAlterTableActions`,
  `prepareAddedChecks`, `stageAddedUnique`, `stageAddedForeignKey`,
  `rebindAlterTypeChecks`, `rebindAlterTypeIndexes`,
  `cookAlterDefaultLocked`, `captureAlterTypeIndexes`); `identityCandidate`
  (the SET wait and option staging between the two cores);
  `inheritedAlterActions` and `finishInheritanceAlterCandidate` (they pick
  the stage or definition read); `stageAddedChecks` (the prepared-node
  selection, the stage install and `RecordChecks`); `alterDefaultLocked`,
  `alterInheritanceDefaultLocked`, `renameConstraintLocked`,
  `alterInheritanceLocked` and the rename handlers as orchestration;
  `dropIdentityDefinition` is gone (`prepareIdentityDropDefinition` reads the
  capture from the provider). `rebuildAlterNotNullsLocked`,
  `rebuildAlterForeignKeysLocked`, `rebindPhysicalNotNullType`,
  `remapPhysicalNotNull`, `dropConstraintIndexes` and `ownedSequences` have no
  root forwarder; callers name `table` directly. Deleted:
  `physical_not_null.go`, `physical_not_null_control.go`, `not_null_type.go`,
  `inheritance_alter_events.go`.
- **Deviations from the plan.** (1) `AlterHost` gained `LiveEpoch` (four
  mid-function epoch checks keep their place) and `BindAddedChecks` (ADD
  CHECK keeps `bindColumnChecks`' altering semantics); `CookDefault` became
  `BindDefault` (table renders and omits, in each caller's order: ADD COLUMN
  binds, omits, renders with the operation's definitions; SET DEFAULT binds,
  renders, omits); `TypeChangeDependents` takes the column name;
  `BindAlterTypeUsing` lost its column (a missing USING is the old column,
  built in `table`). (2) The port went straight into `table` instead of
  root `tblmv_*.go` files: `table` cannot import the root, so the compiler
  proves the edges, and no move script was needed. (3) RENAME CONSTRAINT has
  two cores, because the inheritance branch and the stage begin sit between
  the kind lookup and the staging. (4) The inheritance rename cores run per
  member inside the root's `Replace` loop, so the first error is unchanged.
  (5) ALTER DEFAULT has a guard core and a candidate core; the candidate runs
  where its revision check ran, before the storage lookup. (6) The no-change
  predicate and the `stageAddedChecks` install stayed in the root: both read
  stage internals and are a few lines. (7) `PrepareAddedChecks` takes the
  nil-safe plan instead of a new value type. (8) `RemapPhysicalNotNull`
  takes one `catalog.Read` (its domain replaces the ref builder).
- **Lock windows.** The wrappers hold `Engine.mu` across ADD COLUMN, ALTER
  TYPE (prefix included), SET/DROP EXPRESSION (unlocked before), ADD CHECK
  binding, the TYPE CHECK and index rebinds (per item before) and
  ADD UNIQUE / FOREIGN KEY staging (their `NotNullCandidate`, stage read and
  `Replace` were outside the window). None of these windows contains a wait:
  the binders, the snapshot captures, type and collation resolution and
  `CatalogStage.Replace` take no lock. `bindColumnChecks(altering)` no longer
  locks per CHECK; ALTER FOREIGN TABLE's two callers hold `Engine.mu` around
  it. ADD IDENTITY now passes the operation when a scope is present.
  `CaptureIdentitySequence` keeps the direct AX `acquireSequence` wait, then
  one window.
- **Tests.** New: `TestAlterHostInputReflectsCheckSnapshotCapture`. Moved: the
  kind assertions of `TestAlterTablespacePreparation` →
  `ddl.TestAlterTablespaceKind`. Adapted: `inheritance_alter_test.go` and
  `inheritance_retired_test.go` (`RecordAction` on the plan),
  `primary_key_lifecycle_test.go` and `physical_not_null_test.go`
  (`table.RebuildAlterNotNulls`, `table.RebindPhysicalNotNullType`). None
  deleted.
- **Checks.** String-literal multiset unchanged apart from the new tests,
  `insertError` and import paths; 767 selected engine units passed, also
  with `RACE=1`; `table` imports no root, `partition`, `view`, `dml` or
  `control` package; no root anonymous-interface assertion names an
  unexported method. The 24d.1 reference selection passed in quick mode and
  with `REFERENCE=1` apart from known drift that fails the same way on
  master: the `postgres` leaves of `AlterAddConstraintDiagnostics`,
  `AlterNullability(Names|ActionLists)`,
  `NotNull(FormsAndAlter|CurrentCommandNamesAndCompatibleReuse)` and
  `TemporaryTableDiagnostics`, and the PG-only `SerialDefaultProvider`
  tests.

### 24d.3 record

Done on `implement-part/PG18-ENGINE-SPLIT-TABLE-RESTORE-CORES` from
`9156a1120` (control 30c merged). 30c had already moved
`physicalNotNullDumpDependencies` (`table.PhysicalNotNullDumpDependencies`),
so this step skipped it. After the port the staged set's `frontier` OUT
was `sqlError` alone.

| Commit | Change |
|---|---|
| `a6936164a` | Port in place into `tblrs_*.go` over `catalog.Read`/`access.View`; root wrappers keep the context check, authorization, superuser 42501, `Engine.mu` window, trust checks and resolution; shared stage tail `publishRestoreRepairLocked` |
| `44f1ae9bf` | Move into `table` (script `.cswd/tmp/PG18-ENGINE-SPLIT-TABLE-RESTORE-CORES/c2.py`); three staged receiver-first functions become methods; `RestoreResolver` embedded in `Host`; root callers qualified; no aliases |

- **Moved.** Group A whole: `CaptureRestoredPlaceholders`,
  `CaptureRestoredNotNulls`, `CaptureHistoricalFacts`,
  `RetiredArchiveHighWater` and `RemapRetiredConstraints` (one high-water
  helper; the domain is an argument), `CapturePhysicalImportIndex` (both
  reads and the domain are arguments), `ValidateForeignKeyData` (over
  `access.View` and `access.Environment`), and the receipts
  `RestoredPhysicalNotNull` and `RestoredPhysicalForeignKeyProvider` with
  exported fields and a `RoleRecord` in place of the authorization view
  (methods `Admits`, `Holds`, `Pairs`, `Advance`). Group B, split into a
  shape check before authorization and a decision after resolution:
  `RestoredGenerationShape`/`PairRestoredGeneration`,
  `CaptureRestoredGeneration`, `RestoredCheckShape`/`InheritedCheckRecipe`,
  `CloseRestoredRecipes`, `RestoredNotNullShape`/`PairRestoredNotNull`,
  `CaptureInheritedOwner`, `InheritedOwnerShape`/`InheritedOwnerDirective`/`InheritedOwnerTarget`,
  `InheritedForeignKeyShape`/`CaptureInheritedForeignKey`,
  `(*RestoredInheritedForeignKey).Unchanged`,
  `PhysicalNotNullConstructorShape`/`PreparePhysicalNotNullConstructor`,
  `CapturePhysicalNotNullConstructor`,
  `DeferredPhysicalPrimaryShape`/`PrepareDeferredPhysicalPrimary`,
  `CompetingUniqueShape`/`PreparePhysicalCompetingUnique`,
  `CapturePhysicalForeignKeyProvider`,
  `PhysicalSelfForeignKeyShape`/`PhysicalSelfForeignKeyColumns`/`PinPhysicalSelfForeignKey`,
  `ValidateRestoredDefault`. Group C: `NoInheritNotNullCandidate`,
  `AttributeRankCandidate`, `PhysicalNotNullCleanup`,
  `RetiredConstraintCandidate` (with `RetiredRepairCommit`,
  `(*RestoredHistoryAuthority).Complete` and `CommitRetiredRepair`). Two
  root reject texts became `RestoreGenerationBoundary` and
  `RestoreInheritedCheckBoundary`. New files `inheritance_recipe_restore.go`
  and `inheritance_retired_remap.go`; the rest joined the existing Restore
  files.
- **Root left.** Every hook wrapper (same names, same error order),
  `publishRestoreRepairLocked` (`restore_repair_publish.go`; called with
  `Engine.mu` held, releases it), and group D: `captureRestoredForeignKeys`,
  `resolveRestoredForeignKeyTargets`, `bindRestoredForeignKeys`,
  `validateRestoredInheritedForeignKey` (its domain, epoch, role and trust
  checks and the owner check; the observation check is `Unchanged`),
  `restore_replay.go`. Root-only validators come in as arguments:
  `relationProvider{}` as a `catalog.Provider`, `e.catalog.Domain`.
- **Deviations.** (1) Group B needed two- and three-part splits where the
  root interleaves work (owner: directive before resolution, target after;
  self FK: columns before the root's pending binder, pin after). (2)
  `RestoreResolver` is a separate interface embedded in `Host` (the 24d.1
  `Resolver` precedent): `RetiredConstraintCandidate` runs before any stage
  and gets a stage-less `ddlHost`. (3) The one-line group A wrappers were
  deleted; callers pass `CatalogRead()`.
- **Tests.** None moved or deleted: every Restore unit needs an engine.
  Adapted: `inheritance_recipe_restore_test.go` and
  `inheritance_retired_restore_test.go` call the `table` captures.
  The 24b dump smoke gained a Restore-repair scenario (retired origins,
  attribute rank, inherited recipes and foreign key).
- **Checks.** String-literal multiset unchanged apart from import paths and
  the extra `"0A000"` codes of split cores; the dump smoke byte-identical;
  106 selected engine units passed, also with `RACE=1`;
  `^TestReference(Inheritance|NotNull|ForeignKey|PGAttributeRank|DumpRound|AlterDefault|Control)`
  passed in quick mode and in `REFERENCE=1` mode apart from the known
  `NotNull(FormsAndAlter|CurrentCommandNamesAndCompatibleReuse)` drift,
  which fails on master too; `table` imports no root,
  `partition`, `view`, `dml` or `control` package.

### 24e record

Re-measured at `16cb9f7d0` (rebased onto `c378ffbcc`): names, line ranges and callers matched the
plan. Three commits, each green on `make build` and its units; no aliases.

- `20e2675f2`: `table.TablespaceOnlyActions` and
  `table.ValidateAlterTablespaceMove` (`table/alter_tablespace.go`); the
  root `alter_tablespace.go` keeps only `alterAllTablespaceLocked`.
  Callers: `alter_table.go` (5×), `partition_locks.go`.
- `77e73dff4`: `table.RecomputeRemappedGenerations`
  (`table/generated_remap.go`; root `generated_remap.go` deleted) takes
  `capture func(*catalog.Relation) error`, called once per remapped
  relation with its valid CHECK traversal before any row is evaluated,
  with no lock held; nil skips it. The root `remapCheckCapture(host)`
  (`generated_replacement.go`) returns nil without a statement scope and
  otherwise keeps today's single `Engine.mu` window around the comment and
  visibility captures.
- `9e28f488c`: `table.RelationProvider` (`table/relation_validation.go`,
  body unchanged). Pattern 5 as named slots, not a positional slice: the
  three checks of packages above `table` sit at different points of the
  sequence, so `Validate` keeps the order and calls
  `RelationValidators.Policies` (after the column checks), `.Partition`
  (after the CHECK-kind check) and `.Definition` (the view and
  materialized-view tail). The root builds one package-level
  `relationProvider` value with `view.ValidatePolicies`,
  `partition.ValidateDescriptor` and `view.ValidateViewDefinition`, so
  registration costs nothing extra at startup. `table` now imports
  `extension` (already below it through `stored`).
- Tests: new `TestGeneratedRemapCheckCapture` and
  `TestRelationValidatorsOrder` (root, engine fixtures; both observed
  failing first). Adapted to the new names, unchanged otherwise:
  `alter_tablespace_test.go`, `TestGeneratedReplacementPrivateRowsAndCancellation`,
  `TestGeneratedVirtualReplacementDoesNotMaterializeOwner`, and the root
  tests calling `relationProvider.Validate` (`relationProvider{}` became
  the root value). None moved or deleted.
- Docs: the `catalog` README sentence on validators that need engine
  services was stale beyond 24e (extension, procedure and operator
  providers already live in their packages); it now lists only parameter
  validation as root and names each provider's package.

### 24f record

Re-measured at `e52587400` (rebased onto `2b3d7a138`, after index 23e; no
overlap): names and callers matched the plan; the scan plan had moved to
`alter_table.go:727-746`. Five commits, each green on `make build` and its
units; no aliases.

- `98a193227`: `analyze.RelationNamespace(view, relation, ref, leaf)`
  replaces the copies in `alter_host.go`, `alter_type.go`
  (`alterTypeNamespace` deleted; `analyze.QueryTargetLeaf`), `generated.go`,
  `partition_host.go` and (with the next commit) `check.go`.
- `479d57f07`: `table.BindChecks(h, auth, relation, raw, input, altering,
  inherited)` (`table/check_bind.go`) holds the naming, inherited merge,
  duplicate diagnostics, ordinals and dependency rendering;
  `Host.BindCheckExpression` binds one expression and returns its frame
  uses. CHECK rendering reads the operation's definitions, not the stage
  reader of `index.Host.DefinitionReader`, so `OperationDefinition` moved
  from `AlterHost` to `Host`; the ALTER epoch check needs `LiveEpoch`,
  which moved with it (the check stays just before each binding).
  `Engine.bindColumnChecks`, `bindCreateChecks`, `Host.BindChecks` and
  `AlterHost.BindAddedChecks` are gone; `partition/create.go`, ALTER
  FOREIGN TABLE (`fdw_table.go`, an `alterHost` over its `ddlHost`) and
  `table/alter_constraint.go` call `BindChecks`.
- `3e48a8de5`: `table.AlterTableOf`, `table.ResolveTypedSource` and
  `table.TypedSources` (`table/typed_alter.go`) over `table.TypedResolver`
  (`AccessView`, `NamedType`; `ddlHost` already had both). The reference
  path is the authority's `access.Environment.ReferencePath`. The
  AlterTableOf admission in `relation_scope.go` uses `ResolveTypedSource`
  too. The root keeps `namedType` (the analysis host uses it), the stage,
  the source waits of `acquireTypedSources` and publication.
- `8f6d54f40`: `table.CheckCreateShape(s, temporary, explicit, exists)`
  (`table/create_shape.go`); the root now restores the permanent catalog
  and looks the name up before the TEMP-partition check, which neither
  affects. `table.PlanAlterScan` (`table/alter_scan.go`) returns the scan
  validator; the root keeps the CHECK snapshot capture under `Engine.mu`
  and `validateAddedConstraintRows`.
- `4c97e1cd9`: the target-admission switch of `acquireRelationTarget` is a
  registration table (§3 rule 8, split by owner at the user's choice):
  `table.Target`, `table.TargetEntry`, `table.AdmitTarget`; entries
  `view.TargetAdmission` (RULE, POLICY, CREATE OR REPLACE VIEW, ALTER VIEW),
  `table.TargetAdmission` (PARTITION OF, the ALTER TABLE family, COMMENT,
  and CREATE INDEX and ALTER OWNER, whose packages sit below `table`) and
  `dml.TargetAdmission` (SELECT, COPY, INSERT, MERGE, DELETE, UPDATE). The
  root registers them once as `relationTargetEntries`; statement kinds are
  disjoint, so the order cannot change a result. The lock policy and the
  acquire loop stay.
- Tests: new `TestRelationNamespaceShape` (analyze), `TestTypedSources`,
  `TestPlanAlterScan`, `TestCheckCreateShape`, `TestTableTargetAdmission`,
  `TestAdmitTargetAsksEachEntry` (table), `TestViewTargetAdmission` (view),
  `TestDMLTargetAdmission` (dml). Adapted to the new names, unchanged
  otherwise: `TestRangePartitionInheritedCookedCheckStructuralIdentity`,
  `TestAlterAddColumnSparseCheckOrdinalsAndOwnership`. None moved or
  deleted.

### 24g record

Started at `c40b97c0f` (24f recorded) and rebased onto `38e365e6f` (after partition 25e; no code overlap); names,
line ranges and callers matched the plan. Three code commits, each green
on `make build` and its units; no aliases.

- `0124428f1`: `table.InheritanceQueryLeaves` (`table/inheritance_query_leaves.go`,
  body unchanged) and `table.InheritancePendingGrants`, the pure
  pending-grant decision of `acquireInheritanceSources` (named-root skip,
  prior-grant skip, pending row-mark upgrade, lazy map). It returns the
  map it may allocate and leaves `Storage` empty: the root fills it with
  `privateStorageLocked` in the same `Engine.mu` window, then waits as
  before.
- `319ad1289`: `table.PrepareInheritanceNode` over the six-method
  `table.PrepareHost`; the records are `table.InheritancePreparedNode` and
  `InheritancePreparedAction` with exported fields. Prepared drops cross as
  `table.PreparedDrop` (`DefinitionDelta` plus `Next`; `*preparedDrop` and
  `*preparedIdentityDrop` gained `Next`). The function keeps setting and
  restoring `op.Definition`; the root-private `auth.definition` assignment
  became each host call's `definition` argument (every such call was
  preceded by that assignment with the same reader).
  `inheritancePrepareHost` (`inheritance_prepare_host.go`) returns nil
  interfaces for nil drops and recovers drops with comma-ok assertions;
  `consumePreparedDrop` and `consumeIdentityDropDefinition` already reject
  nil with `ErrCatalogReplaced`. `consumeInheritancePreparedAction`,
  `scope.inheritancePreparedNode()` and the `alterTableLocked` and
  `stageAddedChecks` consumers stay root.
- `35e3d98ae`: `lockInheritanceDropClosure` and `inheritanceStageAlterGuard`
  (`table_inheritance_guards.go`) deleted; their tests call the production
  `dropProvider.lockInheritanceClosure` (it leaves `stage.Base` unchanged on
  error, which the cancel case already asserted) and
  `dropProvider.InheritanceAlterGuard`.
- Tests: new `TestInheritancePendingGrants` and
  `TestPrepareInheritanceNode(PhaseOrder|StopsAtFailedStep)` (table, over a
  small catalog and a fake host; observed failing first). Adapted to the
  new names, unchanged otherwise: `inheritance_query_test.go`,
  `inheritance_alter_admission_test.go`, `inheritance_admission_test.go`,
  `inheritance_dependency_authority_test.go`. None moved or deleted.

### 24h record

Started at `47331f37f` (item 133 recorded) and rebased onto `28e17c28d`
(information_schema.key_column_usage) and `fdcac17ae` (reference test
remedies); neither overlaps. `reach` at the start
confirmed the plan: `rewriteTypeRows`, `StatementScope.validateAddedConstraintRows`
and `planAddedColumns` were DIRTY only through `StatementScope`; after the
move the four touched root files keep only the candidate wrappers and
`alterRowEvaluation` (outputs in
`.cswd/tmp/PG18-ENGINE-SPLIT-TABLE-ROW-REWRITE/`). Two code commits, each
green on `make build` and its units; no aliases.

- `b79597f14` (cut in place): the three functions take an `exec.Context`
  template by value and, for the rewrite, the private write and delete
  roots. The root `alterRowEvaluation(scope, op, read)` builds the
  template right before each call: `scope.op` and the routine authority's
  catalog for the fills, the `op` argument and the same catalog for the
  ALTER TABLE rewrite, `scope.op` and an empty `catalog.Read` for the scan,
  exactly as the functions built it before. The roots come from
  `scope.columnProjectionRoots` of the storage owner (ALTER TABLE) or the
  old relation (typed tables), the address the rewrite read before.
  Cancellation checks read the template's context, which is that same
  `op.Context()`.
- `656dc171c` (move): `table.RewriteTypeRows` (new `table/alter_rewrite.go`),
  `table.ValidateAddedConstraintRows` (`table/alter_constraint_rows.go`) and
  `table.PlanAddedColumns` (`table/alter_add_column.go`), bodies unchanged
  apart from the `table.` qualifiers. The root `alter_constraint_rows.go`
  was deleted. No new import edge.
- Tests: new `TestValidateAddedConstraintRows` (table; observed failing to
  compile first). Adapted to the new signatures, unchanged otherwise:
  `alter_generation_test.go` and `alter_add_column_candidate_test.go` (they
  stay in the root: engine fixture, `queryProbeExpression` and the
  `Engine.mu` TryLock assertion). None moved or deleted.

### 24i record

Started at `42490b71e` (routine parameters merged, after 24h) and rebased
onto `fd4ba5a12` (collation versions; no overlap). `reach` at the start
confirmed the plan: every moved body was DIRTY only through
`StatementScope`, `CatalogStage`, `Engine` or `dropProvider` (outputs in
`.cswd/tmp/PG18-ENGINE-SPLIT-TABLE-FK-DECISIONS/`). Two code commits, each
green on `make build` and its units; no aliases, no new import edge, the
string-literal multiset of the root and `table` and the vet output equal
the start. The targeted units passed, also with `RACE=1`; quickcheck
passed before and after the rebase; the quick and `REFERENCE=1`
`^TestReference(ForeignKey|AlterTypeForeignKey|InheritanceDrop|InheritanceMaint|InheritanceForeign|InheritanceAlterColumns|QueryTableCreate|MaterializedViewCreate|DumpRoundTrip$)`
selections passed. racecheck, finalcheck and bench skipped at user
request (no startup change).

- `82bded1cd` (FK): new `table/foreign_key_providers.go` with
  `InstalledForeignKey`, `AlterForeignKeyIndex` (the parent-index capture
  of `acquireAlterForeignKeys`), `ForeignKeyTypeProviders`,
  `ForeignKeyDefinitionProviders` (the walks and OID sort) and
  `CheckForeignKeyDefinitionProviders` (the post-grant check over an
  `access.View` and its `access.Environment`); new
  `table/foreign_key_scan.go` with `ForeignKeyScan`,
  `ForeignKeyRowsCapture`, `ForeignKeyCheck`, the selectors
  `AddedForeignKeys` and `ReferencingForeignKeys`, the merged
  `ScanForeignKeys` and `ScanRestoredForeignKeys`; and
  `RestoredForeignKey.ResolveTarget` (target-path fallback, 42P01 and the
  `Find` recheck). The root keeps one `StatementScope.validateForeignKeys`
  (authority capture, `captureForeignKeyRows` as the callback), the grant
  loops of `acquireAlterForeignKeys`, `acquireForeignKeyTypeProviders` and
  `acquireForeignKeyDefinitionProviders` with its `Engine.mu` recapture,
  and `bindRestoredForeignKeys`'s stage, `Engine.mu` window, per-declaration
  authority, committed-storage capture callback and publication.
- `9d38f7d39` (inheritance): `table.InheritanceDropClosure` (walk and sort;
  `admitInheritanceDropClosure` keeps the participation check, the acquire
  loop, the TEMP check and the epoch reconciliation),
  `InheritanceAlterPlan.ColumnDropMultiple(base, target)` (new
  `table/inheritance_drop.go`) and `table.ValidateInitialTables(initial,
  staged, base)`, called by `ValidateCatalogStage` with `s.Get` and
  `s.Base`; the root `CatalogStage.validateInitialTables` and
  `dropProvider.inheritanceColumnDropMultiple` were deleted.
- **Names.** `table.ValidateForeignKeys` already existed (the catalog
  shape check), so the merged row scan is `ScanForeignKeys`.
- **Deviation.** The ALTER (`validateAlterForeignKeys`) and Restore scans
  looked up the parent and index with `MustRelation`; the merged scan
  returns `ErrCatalogReplaced`, as the ADD path already did. Only a corrupt
  candidate reaches it. The ADD path keeps its `len(added) == 0` return
  before the authority capture.
- **Commits.** The plan's three code commits became two: the Restore part
  shares the FK scan file with the ALTER part.
- Tests: new `TestForeignKey(TypeProviders|DefinitionProviders|Checks)`,
  `TestScanForeignKeysReplaced`, `TestScanRestoredForeignKeysSlots`,
  `TestRestoredForeignKeyTarget` and `TestInheritanceDropClosure` (table;
  observed failing to compile first). Adapted to the new entry points,
  unchanged otherwise: `inheritance_maint_admission_test.go`
  (`plan.ColumnDropMultiple`) and `materialized_view_test.go`
  (`table.ValidateInitialTables`). None moved or deleted.

### 24j record

Started at `8b401d705` (partition tree navigation, after 24i) and rebased
onto `42fe008a5` (dml 29f; no overlap). `reach` at
the start confirmed the plan: `acquireDefinitionProviders`,
`DeparseInquiry`, `deparseRelation` and `evalSerialSequence` were DIRTY
only through `StatementScope`; `validateSelectedDomainRows` was clean
(outputs in `.cswd/tmp/PG18-ENGINE-SPLIT-TABLE-DEFINITION-PROVIDERS/`).
Two code commits, each green on `make build` and its units; no aliases, no
new import edge, the string-literal multiset of the root and `table` and
the vet output equal the start. The targeted units passed, also with
`RACE=1`; quickcheck passed after the rebase (before it, only
`TestReferenceRegexpSubstrCancellation/memory` failed under load and
passed three times alone); the quick and `REFERENCE=1`
`^TestReference(TableLike|DeparseConstraints|DeparseRelations|SerialIdentity|DomainAlter|CheckReview|QueryTableCreate|InheritanceAlter|TypedTable|AlterDefault|MaterializedViewCreate)`
selections passed. racecheck, finalcheck and bench skipped at user
request (no startup change).

- `5d66c4037` (providers): `(*table.Stage).DefinitionProviders(base,
  staged, descriptors, domain)` (new `table/definition_providers.go`)
  returns OID-ordered `DefinitionProviderRequest{Ref, Mode}`s and the
  retained map; the per-ref mode (inheritance parent of a staged relation)
  is decided with the walk, before any wait, over the same private stage
  descriptors the loop read before. The root keeps the FK provider call,
  the acquire loop, the post-wait checks under `Engine.mu` and the final
  `validateLikeSources`; a nil table slot reads as no LIKE sources, as
  `tableOrZero` did.
- `7ef360259` (deparse and domain rows): `table.Deparse` over
  `table.DeparseInputs` (new `table/deparse.go`, with `deparseRelation` and
  `serialSequence`); the root `statementHost.DeparseInquiry` keeps the
  missing-snapshot check and `StatementScope.deparseInputs` builds the
  inputs. `serial_sequence.go` was deleted. `Temporary` is a callback
  because `temporaryNamespace` may scan every descriptor and only index
  deparse reads it; the relation resolver wraps `resolveRelationSnapshot`
  (it still marks TEMP access). The old helpers read `scopeOf(c.LiveHost())`,
  which is the dispatching host, so one inputs value is equivalent.
  `table.ValidateSelectedDomainRows` (new `table/domain_rows.go`) is the
  unchanged body.
- **Deviation.** `deparseInputs` reads `op.NonstandardStrings` only when
  the scope has an operation: `pg_get_serial_sequence` never read it, and
  `TestSerialSequenceSystemColumns` runs it on a scope without one.
- Tests: new `TestDefinitionProviders(Invalid)`,
  `TestDeparse(SerialSequence|MissingObjects)` and
  `TestValidateSelectedDomainRows` (table; observed failing to compile
  first). None moved, adapted or deleted.

### Notes for later steps

- 24j: step 24 has no planned sub-step left. `acquireDefinitionProviders`
  is now the FK provider call, one `DefinitionProviders` call and the
  grant loop; `deparse_function.go` is the snapshot check and the inputs
  builder.

- 24i for 24j: `definition_providers.go` changed not at all; it still
  calls `acquireForeignKeyDefinitionProviders` first, which now walks with
  `table.ForeignKeyDefinitionProviders` and checks with
  `table.CheckForeignKeyDefinitionProviders`. `admitInheritanceDropClosure`
  still holds two small pure checks (participation and the private TEMP
  closure) that could join `InheritanceDropClosure` if a later review wants.

- 24h for 24i: `alter_table.go` changed only at the three call sites
  (`table.PlanAddedColumns`, `table.RewriteTypeRows`,
  `table.ValidateAddedConstraintRows`); the FK code 24i moves is untouched.
  `typed_mutation.go`'s own CHECK loop (no rewrite, `!rewrite &&
  len(mutation.checks) != 0`) still builds its template inline and could
  call `alterRowEvaluation`.

- 24g: step 24 has no planned sub-step left. The root keeps the
  inheritance waits and stage consumers: `admitInheritanceAlter`,
  `acquireInheritanceSources`, `consumeInheritancePreparedAction` and the
  prepared drops. A later move of `preparedDrop` needs the drop provider.

- 24b: the dependency passes need `dropDependencyReader` and
  `domainDependency` (`domain_dependencies.go`) in `table`; the
  `dropProvider` methods stay in the root. `foreignKeyDiagnostic` is also
  called by FK actions and enforcement (`dml`), which import `table`.
  `restoredForeignKeys.auth` is a `ddl.Authority`. `inheritanceScope.alter`
  still holds the root `inheritanceAlterState`; only the plan moves.

- `dml` (29a): FK write-time enforcement and FK actions, the
  `foreignActionsSlot` and `foreignActionAuth` scope fields, and
  `mutation_targets.go` (inheritance DML expansion) are `dml`'s.
- `partition` (25a/25b): partition imports `table`; table never imports
  partition. The two dump helpers need no interface: index 23a moves
  `partitionReplaySeparateConstraint` into `index`, and
  `partitionHashReplayTarget` moves with `table` in 24b.
- `view` (27b): `emitMaterializedSnapshot` became a function in 24a and
  moves with the materialized snapshot records.
- `typedef`: `typed_inheritance.go`'s clean part moves here, not to
  `typedef` (it uses `columnTransformation`).
- Index 23c refine (question 3 of 24c): `table.Host` embeds `index.Host`
  (one `ddlHost` implements both), and `table` calls the 23d.1 `index`
  functions directly: `StageUniqueDefinitions`/`StageCreateUniques`,
  `StageExclusion`/`StageCreateExclusions`, `RebuildAlterUniques`,
  `RebuildAlterExclusions`, `CaptureUsingIndexes`, `PrepareUsingIndex`,
  `StageUsingIndex`. 23d.1 hoists the table-owned edges to today's
  callers, which become table code: the LIKE source capture
  (`captureLikeSource`) before unique staging,
  `rebuildAlterNotNullsLocked` right after `RebuildAlterUniques` (order
  uniques, NOT NULL, exclusions), the `usingIndexNotNullCandidate`
  preparation before `PrepareUsingIndex`. `incompatibleNotValid` becomes
  `index.IncompatibleNotValid`. The `CaptureUsingIndexes` caller in
  `alter_table.go` holds `Engine.mu` across the call (one window with
  `evolveStageIndexesLocked`).
- `analyze` (12f, done): `closedRegclassDefault` (used by
  `defaultReplayDeferrable`) is now `analyze.ClosedRegclassDefault`.
- 24d.1 for 24d.2 (done): `alterHost{ddlHost; scope}` exists; ADD IDENTITY
  and `stageAddedForeignKey` now pass the operation. `identity.go` in the
  root holds only `evaluateInsertValuesRow` (DML; 29b left it in the root).
- 24d.2 for partition 25d: ALTER on a partitioned table reaches the same
  `table` candidates; a partition host that embeds `table.Host` can build an
  `alterHost` the same way (`alterHost{ddlHost{e, op, stage}, scope}`, one
  per call, under `Engine.mu`). `partition_unique_replay.go` calls the root
  `stageAddedUnique` wrapper.
- 24d.2 for fdw 26e: ALTER FOREIGN TABLE still calls the root wrappers
  (`addColumnCandidate`, `generationCandidate`, `prepareAlterTypeLocked`,
  `alterTypeCandidate`, `rebindAlterTypeChecks`, `cookAlterDefaultLocked`);
  26e can call the `table` functions with an `alterHost` and drop them. Its
  `bindColumnChecks(altering)` calls hold `Engine.mu` themselves now;
  `AlterHost.BindAddedChecks` is the same binding.
- 24d.2 for DML 29c: nothing ALTER-side is left for `dml`; the FK action and
  write-time paths are unchanged.
- 24d.1 for 24d.3 (done): `ResolveRestoredRelation` now sits in
  `table.RestoreResolver`, which `Host` embeds; `RetiredConstraintCandidate`
  uses it.
- 24d.3 for 24d.2 and partition 25d: a Restore or replay repair that
  replaces one relation can reuse the root `publishRestoreRepairLocked`
  tail; the receipts' `Holds` checks keep the role record and current
  relation identity without an authorization view.
- 24f for 24g and later: `index_expression.go` (`indexExpressionBinder`)
  and `policy_expression.go` (`BindPolicyExpression`) still build the same
  relation namespace by hand (index 23e, which ran in parallel, kept its
  copy); an index or view follow-up can call `analyze.RelationNamespace`. The fdw
  and partition notes above that name `bindColumnChecks`,
  `bindCreateChecks` or `BindChecks` now mean `table.BindChecks`.
- 24e for 24f: `relationProvider` is a root value, not a type; new
  callers pass it as is. Nothing in 24e touched the 24f files
  (`check.go`, `relation_scope.go`, `typed_table.go`, `create_table.go`,
  `alter_table.go:701-736`).
- 24d.1 for partition 25d: `schemaPartitionChildLocked` already calls
  `table.StageCreateIdentities`, `table.NewRelation`,
  `table.AssignDefinitionOIDs` and `(*table.Stage).CaptureLikeSource`; its
  defaults, generations and CHECKs still use the root binders
  (`bindCreateDefaults`, `bindCreateGenerations`, `bindCreateChecks`),
  which `BindColumnDefaults` (zero token), `BindGenerations` and
  `BindChecks` wrap.
