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
  23c (item 84). 24d: 24c and index 23d.1 (item 85a). Read the sequence
  prompt's "Notes for later steps" (identity and serial staging belong
  here).
- **Effort:** high for 24a; xhigh for 24b (broad), 24c and 24d.
- **Session size:** one sub-step per session. 24a may need two sessions:
  do the renames and slots first (commit), then the cuts.
- **Linear items:** 24a = 86, 24b = 87 (broad), 24c = 88, 24d = 89.
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
CREATE and ALTER TABLE (`Engine.mu` windows, lock waits, row rewrites,
publication) stays in the root.

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
  (`rewriteTypeRows`, row validation) → index work → `scope.catalog =
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

### 24c — Refine the host (docs only)

Re-measure the remaining handlers and write the host and the move set
here; wait for approval. Start from this sketch (draft signatures; the
`ddl` contract of step 20 provides authority, stage, namespaces, OIDs,
revisions, notices and context):

```go
package table

type Host interface {
	ddl.Host

	// Descriptors and row types, under Engine.mu.
	NewTableDescriptor(namespace ids.ObjectAddress, name string, owner authorization.PrincipalRef, columns []catalog.Column, index map[string]int) (*catalog.Relation, error)
	StageRowType(*catalog.Relation) error
	AssignDefinitionOIDs(*catalog.Relation) error

	// Binding is done by the root (rule 6): the host builds the binder.
	// Today: bindCreateDefaults, bindColumnChecks, bindCreateGenerations.
	BindColumnDefaults(auth ddl.Authority, rel *catalog.Relation, declarations []analyze.ColumnDeclaration) (map[int32]catalog.ColumnDefault, error)
	BindChecks(auth ddl.Authority, rel *catalog.Relation, raw []*parser.CheckConstraint, input sqltype.InputEnvironment, altering bool, inherited map[string]catalog.CheckConstraint) error
	BindGenerations(auth ddl.Authority, rel *catalog.Relation, declarations []analyze.ColumnDeclaration, input sqltype.InputEnvironment) error

	// Types.
	ResolveColumnType(auth ddl.Authority, name parser.TypeName, collation *parser.ObjectName) (sqltype.Usage, error)

	// Identity and serial staging (sequence notes): descriptor decisions
	// come from package sequence; the host stages on CatalogStage.
	// Today: Engine.stageIdentitySequence (identity.go:114).
	StageIdentitySequence(table *catalog.Relation, slot int, name string, mode parser.IdentityMode, options catalog.SequenceOptions, restart *int64) error

	// Dependents (the ddl removal interface implemented by dropProvider).
	Removal() ddl.Removal
}
```

Questions to settle in 24c:

1. Which candidate functions of the ALTER pass loop move (the
   `*Candidate` decisions) and which stay as root steps because they wait
   or rewrite rows (`rewriteTypeRows`, `validateAddedConstraintRows`).
   The loop skeleton of `alterTableLocked` stays in the root.
2. The identity and serial staging (`stageCreateIdentities`,
   `stageIdentitySequence` in `identity.go`, `identityCandidate` in
   `identity_alter.go`, `stageCreateSerials` in `serial.go`, the identity
   DROP flow in `identity_drop_definition.go`) and
   `relationDiagnosticKind` (`sequence_dependencies.go`): which decisions
   move here and which stay on `CatalogStage` (`sequence.Stage`).
3. How index decisions are reached: through `index.Host`/`WriteHost` (23c)
   or through package `index` functions called with this host.
4. Whether the inheritance recursion decisions
   (`inheritedAlterActions`, `prepareInheritanceNode`) can move once
   `inheritanceAlterState` is split, or stay as root orchestration.
5. Every host method documents "runs under `Engine.mu`, never waits" or
   "waits; the caller holds nothing" (index notes).

### 24d — Move the handler decisions

Follow 24c. The handlers' orchestration (`Engine.mu`, lock waits, journal
clone and install, Finish) stays in the root; it calls the moved
decisions through the host.

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
- [ ] 24b — `table` created and clean set moved — linear item 87
- [ ] 24c — Host refined (docs only) — linear item 88
- [ ] 24d — Handler decisions moved — linear item 89

Outstanding aliases: none yet.

### Notes for later steps

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
