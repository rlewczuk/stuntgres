# Engine split, step 23: `internal/engine/index`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-index.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §6 step 23; §3 rules 6–8
  (`index` is an upper feature: it may import `analyze` types and pure
  functions, `regobj` and `stored`, and never builds a binder); §4.1
  patterns 1 (state ownership), 3 (capability), 4 (refine first) and 6
  (reader-interface cut); §4 "Rules for tests during the split".
- **Needs:**
  - 23a (linear item 82): nothing. It may run beside other items on its
    own branch. 29a (item 107) needs it.
  - 23b (item 83): 82; 68 (`stored` 22b, which brings `regobj` 21b and
    the 22a coverage cut); 72 (catalog 5i); 75 (exec 11e); 73 (plan 10e,
    for `generationHasTableOID`).
  - 23c (item 84): 83 and 61 (`ddl` 20b, one root staging adapter).
  - 23d.1 (item 85a): 84. 23d.2 (item 85b): 84 and 85a's seam commit
    (it may run beside the rest of 85a on its own branch).
- **Effort:** high for 23a and 23b; xhigh for 23c; high for 23d.1;
  xhigh for 23d.2 (per-row DML path).
- **Session size:** one sub-step per session. 23a is large: if it does not
  fit, finish steps 1–3 in one session and step 4 in the next, and leave
  the box unticked until both are done.
- **Linear items:** 23a = 82, 23b = 83, 23c = 84, 23d.1 = 85a, 23d.2 =
  85b. Items that wait for this step: 24b (87) needs 83, 24c (88) needs 84,
  24d.1 (89a) needs 85a, 29a (107) needs 82, 29b (108) needs 83, 29c (109)
  needs 85b, 30b (112) needs 83.
  Fourth round (item 133): 23f = 142.
- **Tools:** run from the worktree root; never pipe them into `head`.
  - `reach` (which declarations reach `Engine`, `StatementScope` or
    `CatalogStage`):
    `.cswd/tmp/PG18-ENGINE-SPLIT-EXEC-REFINE/reach/reach -dir . -files '<regexp on base names>'`;
    add `-cut 'From>To,...'` (or `From>*`) to simulate a cut.
  - `frontier` (what a set uses, and who uses it):
    `.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-QUERY-PREP/frontier/frontier -dir . -files a.go,b.go [-decls X,T.m] [-exclude ...]`.
  - Rewrite tools: `movex`, `unalias`, `keylit`, `shadowrn` in
    `.cswd/tmp/PG18-ENGINE-SPLIT-PLAN-ALIASES/`; `movespec`, `expspec` in
    `.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-QUERY-PREP/`. Each is a small Go
    module: `go build -o <name> .` in its directory.
  - The planning measurements are in
    `.cswd/tmp/ENGINE-SPLIT-POSTPONED-PLAN/index/` (`reach.txt`,
    `frontier.txt`, `reach-c123.txt` with the simulated cuts, `gain.py`).

## Goal

Index support becomes a package: B-tree, hash and GiST index records and
algorithms, unique and exclusion constraint enforcement records, index
publication and snapshots, and partitioned-index propagation. First the
records and algorithms move (Part A), then the decisions of CREATE INDEX,
ALTER/DROP INDEX, REINDEX and the constraint-backed indexes (Part B).

The root keeps the orchestration: the `Engine.mu` windows, the CONCURRENTLY
phases and their waits, the registry instances, savepoint marks, the Finish
order and publication. `index` is the lowest package of the table family:
`table` and `partition` import it, never the other way.

## Inventory (measured at `c154e14fb`)

`reach` over 47 root files: the index area, `unique_dml.go`,
`unique_lock.go` and the three partitioned-index files. Together they hold
about 10.5k lines; 3.1k are clean today. "After cuts" counts what is clean
once the 22a coverage cut, the 11e preparation seam and the 23a cuts below
are applied (simulated with `reach -cut`). Line counts are per declaration
and approximate.

| Sub-area | Files | Lines | Clean | After cuts |
|---|---|---:|---:|---:|
| Build, keys, programs | `index.go`, `index_hash.go`, `index_gist.go`, `index_predicate.go`, `index_predicate_query.go`, `index_expression.go`, `index_program.go`, `index_options.go`, `index_clauses.go`, `index_property.go`, `index_catalog_key.go` | 2,184 | 826 | 1,469 |
| CONCURRENTLY and waits | `index_concurrent.go`, `index_wait.go`, `index_inspect.go` | 354 | 44 | 44 |
| Publication, transport, snapshots, ALTER TYPE rebuild | `index_publication.go`, `index_build_transport.go`, `index_layout_transport.go`, `index_snapshot.go`, `index_snapshot_repair.go`, `index_replacement.go`, `index_stage.go`, `index_journal.go`, `unique_type.go`, `exclusion_type.go` | 2,422 | 978 | 1,620 |
| Write-time maintenance | `index_write_candidate.go`, `index_insertion.go`, `index_admission.go`, `unique_dml.go`, `unique_lock.go` | 1,281 | 184 | 341 |
| Unique and exclusion enforcement | `unique.go`, `unique_index.go`, `exclusion_admission.go`, `exclusion_controls.go`, `exclusion_publication.go`, `exclusion_shape.go`, `exclusion_definition.go` | 1,717 | 367 | 557 |
| DDL, maintenance, presentation | `index_maintenance.go`, `index_maintenance_locks.go`, `index_rename.go`, `index_constraint.go`, `using_index.go`, `inheritance_using_index.go`, `index_control.go`, `index_metadata.go` | 1,427 | 301 | 705 |
| Partitioned indexes | `partition_index_attach.go`, `partition_index_drop.go`, `partition_unique.go` | 1,090 | 390 | 581 |
| **Total** | 47 files | 10,475 | 3,090 | about 5,300 |

**Re-measured at `c02c9d97e` (23a, step 1).** Declaration lines total
9,893 (file lines 10,502, within 10% of the table). Clean lines were 4,289,
not 3,090: the 22a coverage cut and the 11e preparation seam had landed, so
34 more declarations were clean before 23a started. The columns below are
total / clean declaration lines before 23a and after it (23a's file names;
the new `index_*` files count with their sub-area).

| Sub-area | Before 23a | After 23a |
|---|---:|---:|
| Build, keys, programs | 2,041 / 1,312 | 2,041 / 1,469 |
| CONCURRENTLY and waits | 324 / 44 | 359 / 84 |
| Publication, transport, snapshots, ALTER TYPE rebuild | 2,286 / 1,036 | 2,438 / 1,709 |
| Write-time maintenance | 1,219 / 350 | 1,223 / 350 |
| Unique and exclusion enforcement | 1,620 / 412 | 1,682 / 609 |
| DDL, maintenance, presentation | 1,330 / 614 | 1,304 / 764 |
| Partitioned indexes | 1,073 / 521 | 1,107 / 661 |
| **Total** | 9,893 / 4,289 | 10,154 / 5,646 |

Index files filed elsewhere: `domain_alter_index_outputs.go` (87 lines,
`reownDomainIndexOutputs` clean) comes here in 23b (the typedef notes send
it here). `domain_alter_indexes.go` (`stageDomainIndexGenerationsLocked`)
comes in 23d only if the refine finds it free of orchestration.
`alter_type_index.go` is ALTER TABLE ... TYPE code and goes with `table`.
`catalog_overlay_indexes.go` is publication and stays in the root.
`insert_write_overlay.go` is ON CONFLICT state and goes with `dml`.

### Clean today (moves in 23b)

- **Whole files:** `index_build_transport.go`, `index_catalog_key.go`,
  `index_constraint.go`, `index_gist.go`, `index_journal.go`,
  `index_options.go`, `index_program.go`, `unique_index.go`.
- **Build:** `indexOrdering` with its methods and `bindIndexOrdering`
  (`index.go`); the validators of `index_clauses.go`
  (`validateIndexMethod`, `validateIndexInclude`,
  `validateHashKeyDirection`, `reconstructIndexKey`, `indexKeyCollation`,
  `indexSemanticError`, `indexDefaultClassError`); `indexKeyUsage`,
  `indexKeyBaseUsage`, `indexExpressionDepends`; `bindHashIndex`,
  `bindIndexProvider`, `extractIndexKey`, `publicationSlot`;
  `indexColumnCapability`; `indexExecutionPreparation`, `keyProgram`,
  `indexRecipeEvaluation`, `newLogicalIndexRoot`.
- **Publication and snapshots:** `evolveIndex`, `evolveIndexRevision`,
  `sameIndexDefinitionSemantics`, `sameIndexKeySemantics`,
  `preserveUnchangedIndexRoots`, `captureIndexStores`,
  `indexStoresUnchanged`; `indexSnapshot` with `captureMapping`, `datum`,
  `destination`, `transport`, `transportRows`, `indexSnapshotSource`,
  `needsOwnerRepair`, `trackedGenerations`, `indexHasTableOID`;
  `indexLayoutLineage`, `indexLayoutInputsUnchanged`,
  `indexLayoutSlotsUnchanged`; `remintedIndexStorage`; `indexStage`;
  `alterIndexBuild`, `indexBuildCapture`, `indexAttributeAffected`,
  `uniqueIndexTypeAffected`, `uniqueIndexValuesAffected`;
  `rebindExclusionOperators`, `rebindImportedExclusions`.
- **Write maintenance:** `indexWriteBatch` and its methods,
  `indexWriteCandidate`, `indexWriteState`, `indexOutputMatches`;
  `indexInsertionBinding` and the `indexProbe*` records;
  `rowMaintainsIndex`; from `unique_dml.go` `uniqueBoundKey`,
  `uniqueWriteBinding`, `conflictEntryVersion`, `uniqueRowDeleted`,
  `uniqueRowExcluded`; from `unique_lock.go` `uniqueKeyHash`,
  `uniqueKeyValues` and its `EqualKey`/`OwnedKey` methods (the lock
  manager's opaque key; the lock README lists it as engine-owned today).
- **Enforcement:** `validateUniques`, `uniqueDiagnostic`,
  `uniqueKeyOutput`, `uniqueHasNull`, `pgRenderer.uniqueConstraintSQL`
  (a renderer method: it moves with `stored` in 22b, not here);
  `enforcingIndexes`, `validateUniqueProviders`; `exclusionProjection`,
  `exclusionModes`, `exclusionEvent`, `exclusionEntry`,
  `exclusionDefinition`, `exclusionKeysMatch`, `cloneExclusionProjection`,
  `bindExclusionOrdering`; `validateExclusions`, `exclusionDiagnostic`.
- **DDL records:** `indexMaintenanceGrant`, `indexWrongKind`, `indexScope`
  (the statement slot struct); `indexPresentation`, `compactIndexKeySQL`,
  `pgIndexFunctionExpression`; `indexMetadata`, `indexesMetadata`;
  `usingIndexAttachment`, `usingIndexEligibility`, `usingIndexDump`,
  `usingIndexCatalogDuplicate`; `usingIndexPreparation`,
  `captureUsingIndexDefinition(s)`; `backingConstraintPresent`,
  `exclusionBacking`, `uniqueBacking`; `indexBuildWait`,
  `indexBuildWaitKind`, `indexWaitWriters`, `indexWaitOlderSnapshots`;
  `indexRootCandidate`, `simpleConcurrentIndex`.
- **Partitioned indexes:** `appendPartitionInlineIndexes`,
  `mapPartitionIndex`, `partitionIndexDeclarationOrder`,
  `partitionIndexEquivalent`, `partitionUniqueCoverage`,
  `partitionIndexAttachCheck`, `partitionConstraintDropActions`,
  `partitionIndexDropDependency`.

### Made clean by cuts (moves in 23b after the cuts)

- **22a coverage cut** (item 67; `partitionIndexCoverageComplete` reads
  through a reader interface instead of `&dropProvider{}`), about 0.46k
  lines here: `indexDefinitionSQL` (197), `projectIndexMetadata`,
  `indexCatalogMetadata`, `validatePartitionIndex`,
  `validateIndexDefinition`. Across the root this cut cleans 33
  declarations (about 2.2k lines), mostly partition and table dump code.
- **11e preparation seam** (item 75; `exec.Preparation` gains coercion
  preflight with a policy and expression execution admission), about
  0.63k lines: `buildIndexEntries`, `buildIndexRoot`, `buildHashIndexRoot`,
  `mergeHashIndexRoot`, `prepareIndexRecipe`, `prepareKeys`,
  `prepareConstants`, `prepareOrdinaryKeys`, `predicateMatches`,
  `materializeIndexOutput`, `uniqueOutput`, `matchesUnique`,
  `materializeInsertion`, `uniqueBoundKey.fillKeys`/`load`/`matches`,
  `prepareAlterIndexRoots`. They are dirty only through
  `preflightCoercionsWithPolicy` and `validateExpressionJoinExecution`.
  Done (item 75): they call `exec.PreflightCoercionsWithPolicy` and
  `exec.AdmitExpression`, and the program is `exec.RoutineProgram`.
- **23a validator cut** (step 4a below), about 0.51k lines:
  `mergeIndexRoot`, `uniqueKeyDetailAllowed`, `validateUnique{Entries,
  Pair,Root}`, `validateExclusion{Entries,Pair,Root}`, `exclusionScan`,
  `uniqueConflict`, `indexSnapshot.repair`,
  `indexMaintenanceGrant.collectPartitionIndexes`.
- **23a publication and stage cuts** (steps 4b–4e), up to about 0.63k
  lines: `prepareIndexPublication`, `evolveIndex`/`evolveIndexes`/
  `indexEvolutionInput` (today `dropProvider` methods),
  `transportIndexLayouts`, `transportEmptyIndexStorage`,
  `stageBackingIndexRename`, `stagePreparedUsingIndex`,
  `stagePartitionDescriptor`.

### Dirty, and why (stays until Part B or for good)

- **Handlers on `Engine`** (45 declarations): `executeCreateIndex` (206
  lines), `executeIndexMaintenance`, `executeConcurrentIndex`,
  `publishConcurrentIndexRoot`/`Valid`, `concurrentIndexAuthorityLocked`,
  `bindIndexAttributes`, `bindIndexKey`, `bindIndexExpression`,
  `bindIndexPredicate`, `indexExpressionBinder`, `lookupIndexClass`,
  `validateSystemIndexKey`, `stageUniqueDefinitions` (189),
  `stageCreateUniques`, `stageExclusion` (154), `stageCreateExclusions`,
  `rebuildAlterUniquesLocked`, `rebuildAlterExclusionsLocked`,
  `stagePartitionIndexChild` (155), `stagePartitionIndexChildren`,
  `stagePartitionIndexAttach`, `stagePartitionIndexValidity`,
  `executePartitionConstraintDropLocked`, `stagePartitionConstraintDrop`,
  `stagePartitionIndexDrop`, `captureUsingIndexes`, `stageUsingIndex`,
  `prepareUsingIndexMetadata`, `captureIndexSnapshotParents`,
  `prepareIndexCatalog`, `prepareRemintedStorage`,
  `prepareForeignIndexStorage`, `fenceEmptyIndexStorageLocked`,
  `capturePartialMaskLocked`, `admitIndexBuildWaitLocked`,
  `clearIndexBuildWait`, `pinUniqueKey`, `endLockOwner`,
  `removeExclusionOwner`, `checkTransactionExclusions`, `InspectIndex`
  (backs the public `stuntgres.InspectIndex`).
- **Write-time enforcement on `StatementScope`** (33 declarations):
  `probeUniqueRow` (245), `bindUniqueWrite` (170), `existingUniqueOutput`
  (166), `claimUniqueRow`, `checkUniqueRow`, `uniqueParent`,
  `uniqueKeyChanged`, `acquireUnique`, `revalidateIndexRows`,
  `readmitProgramsAfterWait`, `retainExistingIndexOutput`,
  `validateIndexCandidates`, `matchesExistingUnique`,
  `validatePrivateIndexExpressions`, the exclusion probe and projection
  methods of `exclusion_admission.go`, `prepareExclusionShape`,
  `executeSetConstraints`, `checkExclusionEvents`,
  `acquireIndexMaintenance` (208), `waitIndexWriters`, `waitCompletion`,
  `partitionIndexTransaction`.
- **`dropProvider` methods:** `partitionIndexValidity`,
  `partitionIndexPromotionPath`, `partitionIndexCoverageComplete` (22a
  replaces it).
- **`CatalogStage` methods:** `indexes`, `indexesOrZero`,
  `validateIndexMaintenance`.
- `statementHost.IndexProperty` (an `exec.Host` capability body).

### State

- **`Engine` fields** (`engine.go`):
  - `indexBuildWaits map[ids.ObjectAddress]indexBuildWait`, written by
    `index_wait.go`, read by `index_inspect.go`, cleared in
    `serializable.go` (lines 31 and 42);
  - `exclusionPending map[*lock.Ticket]*exclusionProjection` and
    `exclusionGeneration uint64`, used by `exclusion_admission.go`,
    `exclusion_controls.go`, `exclusion_shape.go`, `savepoints.go` and
    `catalog.go`. The generation is incremented at six sites:
    `exclusion_admission.go:123`, `exclusion_controls.go:216`,
    `savepoints.go:54` and `:193`, `catalog.go:1457` and `:1560`.
- **`StatementScope`:** `indexesSlot *indexScope` (`index_maintenance.go`:
  `maintenance`, `existingOutputs`, `exclusionModes`, `exclusionStarted`);
  in the embedded `statementMutation` (`cte_mutation_scope.go`),
  `uniqueBinding` and `indexBindings`.
- **`CatalogStage`:** `indexesSlot *indexStage` (`index_stage.go`:
  `rebuilds`, `candidates`, `layouts`, `parents`, `sets`, `output`).
- **Journal:** `session.Journal.Indexes` (`session.IndexSlot`) holds
  `indexJournal` (`index_journal.go`: `uniqueRoots`, `candidates`,
  `rebuilds`), recovered by `indexJournalOf`. Fence kind `fenceIndexSet`
  (`catalogIndexFence`, `catalog_message.go`); fence kinds stay declared
  in `catalog_fence.go`.
- **Owner and savepoints:** `ownerExtension.exclusionModes`
  (`transaction_extension.go`); `session.SavepointMark.Exclusion` holds an
  `exclusionMark` (`savepoints.go`, a `session.ExclusionMarkSlot`).

### Outside users

`frontier` IN over the set: 755 production and 979 test uses. By using
area (declaration–file pairs): DML 137, table 76, catalog orchestration
67, transactions 65, partition 55, type leftovers 43, views 17. The set
uses, by area, transactions 45, table 31, catalog orchestration 27,
schema-level DDL 23, DML 18 distinct declarations.

**Root helpers the clean set still needs**, and where they go first:

| Helper | Today | Goes to |
|---|---|---|
| `pgRenderer`, `pgRenderParent`, `deparseAttribute`, `scalarRenderer`, `remapBoundExpression`, `collationDisplayName`, `relationDiagnosticNameAt` | root presentation code | `stored` (22b) |
| `remapOperatorDatum`, `remapRoutineDatum`, `remapRegTSDatum`, `remapCatalogAliasDatum`, `remapRegclassDatum` | reg* files, `domain_remap.go` | `regobj` (done in 21b: `Remap{Operator,Routine,TextSearch,Alias,Class}Datum`) |
| `sameRelationRowShape`, `physicalNotNullOriginValid`, `physicalPrimarySupportValid` | `relation_scope.go`, `physical_not_null.go` | root: 5i could not move them (`analyze.SameStatementUsage`, `storedReferenceEpochValid`, `indexCatalogKey`); re-measure in 23b |
| `generationHasTableOID` | `plan.GenerationHasTableOID` (`plan/generation.go`) | done in 10e |
| `preflightCoercionsWithPolicy`, `validateExpressionJoinExecution`, `routineAdmissionProgram` | `exec.PreflightCoercionsWithPolicy`, `exec.AdmitExpression`, `exec.RoutineProgram` | done in 11e |
| `columnTransformation` | `alter_type.go` | `index` (23a step 2) |
| `partitionRowHistory`, `partitionNeedsRowHistory` | `partition_retained_checks.go` | `index` (23a step 2) |
| `rowPublication` | `transaction_publication.go` | `index` (23b step 4) |
| `rowByID` | `update_versions.go` | `storage` (23a step 2) |
| `remapLikeExpression` | `table_like.go` | `stored`, next to `remapScalarExpression` (23a step 2) |
| `sqlError`, `insertError` | `engine.go` | a local `index/errors.go` copy |

## Part A

### 23a — In place (linear item 82)

1. **Re-measure.** Run `reach` and `frontier` over the files of the
   inventory. If the totals differ from this file by more than about 10%,
   update the inventory first and say so in the report.
2. **Renames and file moves** (one commit; `git mv` and moving whole
   declarations only, no logic change):
   - `unique_dml.go`: the clean records (`uniqueBoundKey`,
     `uniqueWriteBinding`, `conflictEntryVersion`, `uniqueRowDeleted`,
     `uniqueRowExcluded`) go to `index_unique_binding.go`. The
     `StatementScope` methods stay in `unique_dml.go`.
   - `partition_index_attach.go` → `index_partition_attach.go`,
     `partition_index_drop.go` → `index_partition_drop.go`,
     `partition_unique.go` → `index_partition_unique.go`.
   - `inheritance_using_index.go` → `using_index_prepare.go` (it is not
     inheritance code). `usingIndexNotNullCandidate` is NOT NULL staging:
     move it to `not_null_candidate.go`, so it goes with `table`.
   - `unique_type.go` → `index_alter_type.go`; move `columnTransformation`
     (`alter_type.go`), the ALTER TYPE input of index evolution, into it.
   - `exclusion_controls.go`: `executeSetConstraints` and
     `checkExclusionEvents` → `set_constraints.go`; `endLockOwner`,
     `removeExclusionOwner` and `checkTransactionExclusions` →
     `exclusion_registry.go`.
   - `index_admission.go` (`rowMaintainsIndex`, 8 lines) folds into
     `index_write_candidate.go`.
   - `partitionRowHistory` and `partitionNeedsRowHistory` (from
     `partition_retained_checks.go`) → `index_row_history.go`. Index
     snapshots read them; `partition` imports them later.
   - `partitionReplayUsingIndex`, `partitionReplayVirtualUniqueRoot` and
     `partitionReplaySeparateConstraint` (`partition_invalid_dump.go`,
     `partition_unique_dump.go`) → `index_partition_replay.go`. They are
     replay-shape predicates over indexes; table NOT NULL dump code and
     `dumpPredecessor` use them, and `table` sits below `partition`.
   - `rowByID` (`update_versions.go`, pure over `storage.RowVersion`)
     moves to `storage` as `storage.RowByID`; qualify its callers.
   - If `remapLikeExpression` is still in `table_like.go`, move it next to
     `remapScalarExpression` (`expression_remap.go`), so it moves with
     `stored`.
3. **Registries (pattern 1).** Two root structs without a mutex; their
   mutating methods carry the `Locked` suffix (caller holds `Engine.mu`):
   - `indexWaitRegistry` replaces `Engine.indexBuildWaits`: `admitLocked`,
     `clearLocked`, `endOwnerLocked` (the loop at `serializable.go:31`),
     `clearAllLocked` (`serializable.go:42`) and `lookupLocked` (for
     `InspectIndex`).
   - `exclusionRegistry` replaces `exclusionPending` and
     `exclusionGeneration`: `installLocked`, `projectionLocked` (returns
     the clone the callers take today), `removeLocked`, `resetLocked`
     (the two `catalog.go` sites), `scanLocked` (the `exclusionScan`
     capture), `currentLocked(scan)` and the savepoint capture and
     restore used by `savepoints.go`. Every one of the six generation
     increments stays, in the same place in the call order.
   - `Engine` holds one value of each. Run the exclusion and concurrent
     index units with `RACE=1` after this commit.
4. **Cuts.** One commit each; re-run `reach` after each and write the
   measured gain into the Status record.
   - a. **Validators take `access.View` and `access.Environment`**
     instead of `AuthorizationView` (which holds `*Engine`):
     `uniqueKeyDetailAllowed` calls `view.Effective(env, ref, false)`
     (that is what `AuthorizationView.Effective` does today; table 24a
     already gave it the `(view, env, ...)` signature);
     `validateUnique{Entries,Pair,Root}`,
     `validateExclusion{Entries,Pair,Root}`, `mergeIndexRoot`,
     `exclusionScan`, `uniqueConflict`, `indexSnapshot.repair`,
     `indexMaintenanceGrant.check` and `.collectPartitionIndexes`,
     `validateAlterIndexBuilds` and `uniqueWriteBinding.readmitPrograms`
     follow. The trusted-publication test `auth == nil ||
     auth.engine == nil` (`unique.go:427`, `exclusion_publication.go:18`)
     becomes `env == nil`. Check every caller: a nil environment must
     mean exactly what an engine-less view meant. **Stop and ask** if a
     caller builds an engine-less view for another reason.
   - b. **`prepareIndexPublication` becomes a function.** It uses the
     engine only for `e.catalog.read(next)` and
     `e.captureTypeDiagnosticName`; pass a catalog-read function and a
     type-name function. Its `auth` parameter follows cut a.
   - c. **`evolveIndex`, `evolveIndexes`, `indexEvolutionInput`** become
     functions over `indexCoverageReader`, the reader interface 22a
     introduced (`partition_index_attach.go`; extend it if they need more
     reads).
   - d. **Stage helpers take a narrow stage interface.**
     `stageBackingIndexRename`, `stagePreparedUsingIndex` and
     `stagePartitionDescriptor` use only `Replace`, `Get`,
     `UpdateDescriptor`, `RenameStagedDescriptor` and `Base` reads.
     Declare a root interface with exactly those methods (use `ddl.Stage`
     once 20a has landed). Never a defined type over `CatalogStage`: it
     would promote `catalog.Candidate`'s unchecked `Create`/`Replace`
     (§7 decision 8).
   - e. **Split the layout transports.** `transportIndexLayouts` takes
     `Engine.mu` itself (`stage.owner`) to check the epoch and read the
     storage and index roots. Split it into a root capture step (under
     the lock) and a pure transport function over the captured values.
     `transportEmptyIndexStorage` reads only the stage's index candidates:
     pass them in.
5. **Open questions** (record the answers in the Status record):
   - Settled during planning: `indexHasTableOID` and
     `indexSnapshot.trackedGenerations` use `generationHasTableOID`, which
     10e moved to `plan` (`plan.GenerationHasTableOID`), so item 83 needs
     item 73 (done).
   - `rowPublication` is built by root publication and read by index
     merging (`mergeIndexRoot`, `mergeHashIndexRoot`, `publicationSlot`,
     `prepareIndexPublication`). Confirm nothing else reads it before 23b
     moves it.

Commit sequence: renames → registries → validator cut → publication and
stage cuts → docs (inventory refresh, Status).

### 23b — Create `index` and move the clean set (linear item 83)

1. **Check prerequisites:** 22b (`stored`: renderers, remap, retag), 21b
   through 22b (`regobj`), 5i (catalog helpers), 11e (`exec` preparation
   seam and `exec`'s routine admission program), and the 10e question of
   23a.
2. **Re-run `frontier`** over the set. OUT may name only lower packages,
   `analyze` types and pure functions, `regobj`, `stored`, and
   `sqlError`/`insertError` (give `index` its own `errors.go` copy, as
   every subpackage has). Anything else: stop and list it.
3. **Move** with the `movex` approach into `internal/engine/index`
   (package `index`). Names lose their prefix where it stutters
   (`indexOrdering` → `index.Ordering`, `indexWriteBatch` →
   `index.WriteBatch`, `indexJournal` → `index.Journal`, `indexScope` →
   `index.Scope`, `indexStage` → `index.Stage`, `indexWaitRegistry` →
   `index.WaitRegistry`, `exclusionRegistry` →
   `index.ExclusionRegistry`). Decide the final names in the commit that
   exports them, and list them in the Status record.
4. **Records the root keeps naming:**
   - `index.Journal` implements `session.IndexSlot`; `index.JournalOf` is
     the checked helper. The root keeps `journalIndexes` and
     `journalIndexesOrZero` as root functions (Go cannot add methods to
     the session type).
   - `index.Scope` and `index.Stage` are the slot structs; the accessors
     on `StatementScope` and `CatalogStage` stay in the root.
   - The exclusion savepoint mark type implements
     `session.ExclusionMarkSlot` and moves with the registry.
   - `rowPublication` moves as `index.RowPublication` with exported fields;
     root publication fills it.
   - `Engine` holds `index.WaitRegistry` and `index.ExclusionRegistry`.
5. **Imports.** Expected: `catalog`, `storage`, `visibility`, `lock`,
   `exec`, `plan`, `expr`, `access`, `authorization`, `analyze` (types and
   pure functions only), `regobj`, `stored`, `session`, `ids`, `parser`,
   `sqltype`, `sqlstate`. Never the root, `table`, `partition`, `view` or
   `dml`. Check with
   `go list -f '{{.ImportPath}}: {{join .Imports " "}}' ./internal/engine/...`.
6. **No aliases.** Qualify the root references in the same commit, as
   14b–17b did. If the qualifying pass touches more than about 150 files,
   it is a broad rewrite: tell the user before starting, because it must
   not run beside another broad item.
7. **Tests follow the code.** Unit tests that use only moved code move
   into `index` (package-internal tests). Engine-fixture tests stay in the
   root and are rewritten to `index.X`, or deleted when `reference/`
   covers them (name the covering test). A test never keeps a declaration
   in the root.

Commit sequence: export in place → move → qualify → docs.

## Part B

### 23c — Refine the host (docs only; linear item 84)

Measure the handlers of "Dirty, and why" again with `reach` and `frontier`,
starting from the draft sketch (an `index.Host` for the DDL side, an
`index.WriteHost` for the DML side), then write the final interfaces, the
move set and the commit sequence into this file, and wait for approval. Four
questions were open: (1) do the exclusion registry methods of `WriteHost`
take `Engine.mu` themselves, or must the caller hold it; (2) how `table`
reaches the constraint-index staging; (3) does SET CONSTRAINTS stay in the
root; (4) where the `IndexProperty` capability body goes.

**23c refine (approved; measured at `42a45f774`).** `frontier` over the 33
root files that still hold index code (`index_*.go`, `unique*.go`,
`exclusion_*.go`, `using_index*.go`, `domain_alter_indexes.go`; output in
`.cswd/tmp/PG18-ENGINE-SPLIT-INDEX-REFINE/frontier-index.txt`): 94
declarations, about 150 engine edges. `executeCreateIndex` (`index.go`,
205 lines) was read directly. Findings:

- **The shared DDL host covers most edges.** `allocateOIDLocked` and
  `revisionLocked` are `Allocator`; `stage.Create`, `Replace`, `Get` and
  `LookupName` are `Stage()`; `stage.Base` is `StageBase()`; `stageRead` is
  `StageRead()`; `op.Context()` and `op.EmitNotice` are `Context` and
  `EmitNotice`; `auth.CheckCreate` with the Restore 42501 exception is
  `CheckCreateIn`; `lookupNamespaces(auth, name, OperatorLookup)` is
  `SearchNamespaces(auth, name, ids.OperatorClassID)`, the same path and
  error. Four methods already on `ddlHost` fit as they are:
  `AddDomainBase` (`stage.domainBases` appends), `RemoveObject`
  (`removeObjectLocked`), `TrustedRestoreDestination(auth, "index")`
  (exactly CREATE INDEX's trusted Restore check and its text) and
  `RelationDiagnostic` (the kind in the 42809 detail). `scalarRenderer` was
  an alias of `stored.ScalarRenderer` (22d removed it), and `sqlError` has
  the `index` copy.
- **Not needed** (the draft had them): `Evaluation()`, `RelationStorage`,
  `IndexRoot`, `TypeDiagnosticName` and `TrustedRestore()`. Only the
  CONCURRENTLY phases and publication read storage, build evaluation
  contexts or name types, and they stay in the root.
- **Lock contracts the draft did not foresee.** `captureUsingIndexes` takes
  `Engine.mu` twice around allocation and removal, and
  `prepareUsingIndexMetadata` once around allocation; their callers hold
  nothing, and nothing inside waits (`CatalogStage.Create`/`Replace` and
  `stageDefinitionReader` take no lock). DROP INDEX staging
  (`stagePartitionIndexDrop`) runs after `executeIndexMaintenance` releases
  `Engine.mu`, and `CatalogStage.Remove` takes it. `stagePartitionIndexValidity`
  runs both with and without the lock.
- **Table-owned edges** inside the index staging: `captureLikeSource`
  (`stageUniqueDefinitions`), `rebuildAlterNotNullsLocked` (the tail of
  `rebuildAlterUniquesLocked`), `usingIndexNotNullCandidate`
  (`prepareUsingIndexMetadata`) and `notNullCandidate`/`notNullDirect`
  (`executePartitionConstraintDropLocked`).
- **Write side.** On the write path every exclusion registry access but
  three sits in a compound `Engine.mu` window: epoch and private catalog check, projection
  begin, row identity allocation, the projection edit and install
  (`publishExclusionCandidate`), or a coherent capture of authority, roots,
  storage and the pending map (`captureExclusionScan`,
  `exclusionScanCurrent`). The three standalone windows are
  `discardExclusionCandidate`, the `OwnLocked` read and the admit
  read-modify-write in `probeExclusionRow`. The unique probe, claim and
  existing-output lookups interleave DML state on every row: ON CONFLICT
  arbiters, the insert write overlay, own writes and deletes, and foreign
  action owners.
- **Outside uses (IN).** `executeCreateIndex` and `executeIndexMaintenance`:
  one production caller each (`execute.go`). Constraint staging: table and
  partition code (`create_table_stage.go`, `partition_create_child.go`,
  `alter_constraint.go`, `alter_table.go`, `typed_mutation.go`,
  `inheritance_alter_prepare.go`, `partition_exclusion.go`,
  `partition_attach.go`, `partition_create.go`, `partition_unique_replay.go`).
  Write side: DML (`insert_*`, `update*`, `delete*`, `merge*`, `copy*`,
  `foreign_action_apply.go`, `transaction.go`). `partitionIndexPromotionPath`
  (the free function) has no user.

Decisions:

1. **`index.Host` lives on the shared `ddlHost`**, like `routine.DDLHost`,
   `typedef.StageHost`, `ddl.SchemaHost` and `fdw.Host`; a new root
   `index_host.go` holds `var _ index.Host = ddlHost{}`. Every method runs
   with `Engine.mu` held, never takes or releases it, and never waits. It is
   `ddl.Host` plus:
   - already on `ddlHost`, declared again in `index`: `AddDomainBase(
     catalog.Descriptor)`, `RemoveObject(ref ids.ObjectRef, namespace bool)
     error`, `TrustedRestoreDestination(auth ddl.Authority, what string)
     (bool, error)`, `RelationDiagnostic(auth ddl.Authority, relation
     *catalog.Relation) (kind, name string)`;
   - new: `IndexStage() *Stage` (today `CatalogStage.indexes()`);
     `DescriptorStage() DescriptorStage` (the stage itself, for
     `StagePartitionDescriptor`, `StageBackingIndexRename` and
     `StagePreparedUsingIndex`; `ddl.Stage` lacks `RenameStagedDescriptor`
     and `StageBase`); `ACLOutput(view catalog.Read) regobj.ACLItemOutput`
     (the engine owner and `roles.read(owner, view,
     operationRolesLocked(op))`; LIKE passes `StageBase()`, partition
     children `StageRead()`); `DefinitionReader() catalog.DefinitionReader`
     (`stageDefinitionReader{stage}`); `LookupCollation(raw
     parser.ObjectName) (uint32, error)` (`Engine.lookupCollation(raw, op)`:
     the operation's authority, not the statement's, and the same 0A000 and
     42704 errors; `typedef.StageHost.FindCollation` is not equivalent);
   - binding, where the root builds the binder (§3: `index` never builds
     one): `BindIndexExpression(auth ddl.Authority, parent
     *catalog.Relation, raw parser.Expr, literals []plan.LiteralExpr, input
     sqltype.InputEnvironment) (*catalog.DerivedIndexKey, string, error)`
     and `BindIndexPredicate(auth ddl.Authority, parent *catalog.Relation,
     raw parser.Expr, input sqltype.InputEnvironment)
     (*catalog.IndexPredicate, error)`. Today's bodies stay whole behind
     them (binder, preflight, volatility checks, dependency rendering),
     because `RoutineAuthority` needs the `*AuthorizationView` that a
     `ddl.Authority` value cannot give. `bindIndexAttributes`,
     `bindIndexKey`, `validateSystemIndexKey` and the class lookup then move
     into `index` over these, `LookupCollation` and `SearchNamespaces`. The
     analyze host's `LookupIndexClass` and `IndexExpressionBinder` stay in
     `analysis_host.go` and call the moved class lookup.
2. **Lock windows.** `captureUsingIndexes` and `prepareUsingIndexMetadata`
   become `index` functions over the host; their root wrappers hold
   `Engine.mu` across the call. The ALTER TABLE caller already takes the
   lock just before (`evolveStageIndexesLocked`), so its two windows merge;
   the windows only widen and contain no wait. DROP INDEX staging keeps its
   unlocked place: `index.StagePartitionIndexDrop(stage DropStage, idx
   *catalog.Relation) error` over `DropStage` (`DescriptorStage` plus
   `RemoveRelation(ids.ObjectAddress) error`, a root `CatalogStage` method
   that builds the reference and calls `Remove`, which locks), with no host.
   `stagePartitionConstraintDrop` follows it. The validity pass becomes
   `index.PartitionIndexValidity(t ValidityTarget, promote
   ids.ObjectAddress) error` with `PartitionIndexPromotionPath`;
   `ValidityTarget` is `CoverageReader` plus `DescriptorCount() int` and
   `ReplacePartitionDescriptor(*catalog.Relation) error`, which
   `*dropProvider` exports. The root `stagePartitionIndexValidity` stays as
   the one line that builds the provider.
3. **CREATE INDEX.** The root keeps the argument and Restore CONCURRENTLY
   checks, the retry loop, the lock, `refreshRelationGrantsLocked`,
   `statementAuthorizationLocked`, `captureIdentityLocked`, the parent
   lookup by `lockedRef`, `BeginCatalogStage`, `authorizeLocked` and the
   stage output (now before the decisions; beginning a stage has no side
   effects), the CONCURRENTLY `visibilityWriteLocked`, the unlock,
   validation, publication, the `catalog.Conflict` branch (retry for an
   unnamed index, the relocked IF NOT EXISTS recheck with its owner and
   plain `CheckCreate` checks) and `executeConcurrentIndex`. Two functions
   move:
   - `index.PrepareCreate(h Host, auth ddl.Authority, parent
     *catalog.Relation, s *parser.CreateIndex, input
     sqltype.InputEnvironment) (CreatePlan, error)`: today's lines 38–145
     in the same order (Restore authority, the 32-column limit, relation
     kind, CREATE with the Restore exception, method, partial CONCURRENTLY,
     reloptions, predicate, attributes, generated name, the existing name
     (a `Skip` result for IF NOT EXISTS; the root emits
     `ExistingRelationNotice` after unlocking, the 42P07 error otherwise),
     operator class options, unique providers, partition coverage, storage
     parameters, OID and revision, columns and descriptor). `CreatePlan`
     holds `Relation`, `Key ids.NameKey`, `Namespace ids.ObjectRef` (for
     the conflict recheck) and `Skip`.
   - `index.StageCreate(h Host, parent, idx *catalog.Relation, only bool)
     error`: `Create` and the partitioned branch (lines 156–163).
     `definition.Valid` is set after `Create` through the shared
     `SQLIndex` pointer today; keep that aliasing (or set it before
     `Create`) so the staged descriptor sees it.
   `generatedRelationName` (`identity.go`, pure; identity and LIKE index
   naming use it too) goes to `ddl` as `ddl.GeneratedRelationName`; the
   method-name mapping that `executeCreateIndex` and `bindIndexAttributes`
   both spell out becomes `index.MethodOf(string) catalog.IndexMethod`.
4. **ALTER/DROP INDEX and REINDEX.** `index.StageMaintenance(h Host, auth
   ddl.Authority, grant *MaintenanceGrant, stmt parser.Statement) error`
   moves today's lines 60–100 of `executeIndexMaintenance`: the stage's
   parent and index-set observations, the domain bases, the REINDEX
   rebuild intents and flag revisions ("index revision exhausted"), and
   ATTACH PARTITION (`stagePartitionIndexAttach` →
   `index.StagePartitionIndexAttach(h, parent, child)`, with
   `PartitionIndexAttachCheck` taking a context instead of the operation).
   The root keeps the lock, the epoch fence, authorization, the grant read
   and `Check`, identity capture, the no-target and REINDEX notices,
   RENAME (`renameRelationLocked`), the unlocked DROP path (decision 2,
   then the dependency path recording and `stageExpressionDependents`,
   which may wait), validation, `partitionIndexTransaction`, the overlay
   and publication. The root discards the stage on every
   `StageMaintenance` error; today the two REINDEX error paths skip
   `DiscardCatalogStage`, which has no effect (it only marks a stage
   nothing else holds). `CatalogStage.validateIndexMaintenance` becomes
   `index.ValidateMaintenance(st *Stage, base catalog.Read, current
   *catalog.State, replacedParent ids.ObjectAddress) error`; the root
   method stays as the forwarder that passes the materialized replacement's
   original address, or zero (a zero address never equals a parent).
   `acquireIndexMaintenance` is lock admission and stays.
5. **Constraint indexes** (question 2). `table.Host` embeds `index.Host`
   (one `ddlHost` implements both), and `table` calls `index` functions
   directly: `StageUniqueDefinitions(h, auth, d, declarations, altering)`
   and `StageCreateUniques`, `StageExclusion(h, auth, input, parent, raw,
   recurse)` and `StageCreateExclusions`, `RebuildAlterUniques(h, parent,
   transformations)` and `RebuildAlterExclusions`, `CaptureUsingIndexes(h,
   parent, actions, transformations)`, `PrepareUsingIndex(h, view, reader,
   parent, captured)` and `StageUsingIndex` (`StagePreparedUsingIndex`
   emits its rename NOTICE through the host instead of the operation),
   and the partitioned
   `StagePartitionIndexChild`/`Children`. The table-owned edges are
   hoisted to the callers, which become table code in 24d:
   - `captureLikeSource`: CREATE TABLE and PARTITION OF capture
     `declarations[i].Source` before the call (ALTER never sets it);
   - `rebuildAlterNotNullsLocked`: `alter_table.go` and
     `typed_mutation.go` call it after `RebuildAlterUniques` returns, so
     the order stays uniques, NOT NULL, exclusions;
     `primary_key_lifecycle_test.go` follows;
   - `usingIndexNotNullCandidate`: the callers prepare the parent first,
     as `captured.NotNullPrepared` already models;
   - `notNullCandidate`/`notNullDirect`:
     `executePartitionConstraintDropLocked` stays in the root.
   `incompatibleNotValid` (pure, also used by NOT NULL code) moves to
   `index` as `IncompatibleNotValid`.
6. **SET CONSTRAINTS stays in the root** (question 3). It is
   transaction-level and writes the owner's exclusion modes; so do
   `checkExclusionEvents`, `endLockOwner`, `removeExclusionOwner` and
   `checkTransactionExclusions`.
7. **`IndexProperty`** (question 4). `index.Property(def
   *catalog.IndexDefinition, whole bool, ordinal int64, property string)
   (value, known bool)` (`index` does not import `functions`); the root
   `statementHost.IndexProperty` keeps the snapshot read and the ordinal
   decode, so the error order stays.
8. **`index.WriteHost`** (question 1). The `ExclusionRegistry` primitives
   keep "the caller holds `Engine.mu`" and only the root calls them. Every
   `WriteHost` method either takes `Engine.mu` itself for one coarse
   window (it never runs under the lock) or waits with the caller holding
   nothing. Methods take data, never a callback, and no `*exec.Context`
   (an interface call would make that stack value escape):
   - `UniqueParent(ref ids.ObjectRef) (*catalog.Relation, catalog.Read,
     error)` — locks, never waits;
   - `BindingAuthority(b *UniqueWriteBinding, view catalog.Read) (stale
     bool, err error)` — locks for the private catalog recheck, write
     authority and type names (`bindUniqueWrite`'s checked window), then
     readmits programs unlocked; never waits;
   - `ClaimUniqueKey(ref ids.ObjectRef, bound *UniqueBoundKey, key
     []sqltype.Value) error` — waits; caller holds nothing. Today's
     `acquireUnique`: the pin, the lock-manager acquire with the
     operation's policy, the `replacementMu` release before a wait and
     re-acquire for implicit statements, and program readmission after a
     wait. It replaces the draft's separate `PinUniqueKey` and
     `ReadmitPrograms`, which are never used apart;
   - `WaitTransaction(owner *lock.Ticket, purpose lock.CompletionPurpose)
     error` — waits; caller holds nothing (`waitCompletion`; it keeps not
     re-taking `replacementMu`);
   - `CaptureUniqueProbe(ref ids.ObjectRef, b *UniqueWriteBinding, idx
     *catalog.Relation, heads bool) (UniqueProbe, error)` — locks: the
     `probeUniqueRow` window (epoch, catalog state, revisions, authority,
     namespace, the committed or private root, the head versions);
   - `ExistingIndexRoot(idx *catalog.Relation) (*storage.IndexRoot,
     error)` — locks: the `existingUniqueOutput` window;
   - `CaptureExclusionScan(ref ids.ObjectRef, b *UniqueWriteBinding, idx
     *catalog.Relation) (ExclusionScan, error)` and
     `ExclusionScanCurrent(scan ExclusionScan, idx ids.ObjectAddress)
     bool` — lock;
   - `PublishExclusion(ref ids.ObjectRef, state *catalog.State, proposal
     *WriteCandidate, edit ExclusionEdit) (storage.RowVersion, error)` —
     locks for the whole compound window (check, begin, allocate, the pure
     edit, install), so a publication reset cannot land between check and
     install;
   - `DiscardExclusion(proposal *WriteCandidate)`, `OwnExclusionKeys(idx
     ids.ObjectAddress, version ids.VersionID) []sqltype.Value`,
     `AdmitExclusion(idx ids.ObjectAddress, version ids.VersionID)` — lock,
     standalone windows as today (the admit window has no epoch check
     today; keep it, and say so in the 23d report).
   `*statementHost` implements it (`var _ index.WriteHost =
   (*statementHost)(nil)`). In 23d the root calls the concrete methods, so
   the per-row path gets no interface dispatch; `dml.Host` embeds the
   interface in 29c. `projectExclusionDeletionLocked` (inside
   `stageDeletionLocked`'s larger window), savepoint restore and the
   publication resets stay root callers of the primitives.
   The pure write decisions move into `index`: `BuildUniqueWriteBinding(ref,
   parent, view)` (the rebind body of `bindUniqueWrite`), `UniqueRootRange`
   (the committed-root binary search of `probeUniqueRow`), the
   existing-output lookup over a root (`existingUniqueOutput` after its
   window), the exclusion visit over a captured scan (`probeExclusion`'s
   own-entry, committed-root and pending passes and the deferrable
   decision), the projection edits (publish, discard, deletion, admit and
   the ALTER shape replacement), the ALTER shape entries
   (`prepareExclusionShape`'s loop) and `Scope.RetainExistingOutput`. The
   probe, claim and exclusion loops (with their retries, waits and the DML
   state above) stay root `StatementScope` methods until `dml` (29d).
   `uniqueConflict` and `exclusionScan` keep their `AuthorizationView` and
   stay in the root with the probe loops: an `access.Environment` there
   boxes the view once per probe and per ON CONFLICT hit. `index` takes
   the scan's plain fields as `ExclusionScan` (no authority) and returns
   the hit; the root builds the conflict and its disclosure-checked
   diagnostic with `index.UniqueKeyDetailAllowed`. This settles the 23a
   note.
9. **Stays in the root**, beyond the list of 23d below:
   `stageDomainIndexGenerationsLocked` (storage, journal and snapshot
   orchestration; its decisions are already `ReownDomainIndexOutputs`,
   `Snapshot.TransportRows` and `RootCandidate`, so it does not come to
   `index`); `capturePartialMaskLocked` (query-time fencing);
   `executePartitionConstraintDropLocked` (stage lifecycle and
   publication; its constraint lookup may move later as a pure function);
   `partitionIndexTransaction` (transaction classification); the slot
   accessors. The dead free `partitionIndexPromotionPath` is deleted.
10. **23d splits** into 23d.1 (DDL decisions, linear item 85a) and 23d.2
   (write-time decisions, item 85b). They touch different files and risks:
   23d.1 is catalog staging, 23d.2 the per-row DML path. Table 24d.1 (89a)
   needs 85a; DML 29c (109) needs 85b. 85b may run beside 85a once 85a's
   seam commit has landed.

### 23d — Move the handler decisions (linear items 85a and 85b)

Follow the 23c refine above. One sub-step per session.

**23d.1 move set (item 85a).** Into `index`:

- `host.go`: `Host`, `DropStage`, `ValidityTarget`.
- `create.go`: `PrepareCreate`, `CreatePlan`, `StageCreate`, `MethodOf`.
- `bind.go`: `BindAttributes`, `BindKey`, `validateSystemKey`,
  `LookupClass` (from `index_clauses.go`); `RejectVirtualUses` (from
  `generated_virtual.go`, if no other root user remains; otherwise a copy).
- `maintenance.go`: `StageMaintenance`, `ValidateMaintenance`.
- `unique_stage.go`: `StageUniqueDefinitions`, `StageCreateUniques`.
- `exclusion_stage.go`: `StageExclusion`, `StageCreateExclusions`.
- `alter_type.go`/`exclusion_type.go`: `RebuildAlterUniques`,
  `RebuildAlterExclusions`.
- `using_index.go`/`using_index_prepare.go`: `CaptureUsingIndexes`,
  `PrepareUsingIndex`, `StageUsingIndex`, `IncompatibleNotValid`.
- `partition_unique.go`, `partition_attach.go`, `partition_drop.go`:
  `StagePartitionIndexChild`, `StagePartitionIndexChildren`,
  `StagePartitionIndexAttach`, `PartitionIndexValidity`,
  `PartitionIndexPromotionPath`, `StagePartitionIndexDrop`,
  `StagePartitionConstraintDrop`.
- `property.go`: `Property`.

Into `ddl`: `GeneratedRelationName`.

**23d.2 move set (item 85b).** Into `index`: `WriteHost`, `UniqueProbe`,
`ExclusionScan`, `ExclusionEdit`; `BuildUniqueWriteBinding`,
`UniqueRootRange`, the existing-output lookup, the exclusion visit, the
projection edits and shape entries, `Scope.RetainExistingOutput`.

**Stays in the root:** `executeCreateIndex` and `executeIndexMaintenance`
as orchestration (decisions 3 and 4); `executeConcurrentIndex` and its
phases (`publishConcurrentIndexRoot`/`Valid`,
`concurrentIndexAuthorityLocked`); the waits (`waitIndexWriters`,
`waitCompletion`, `waitOlderIndexSnapshots`) and the lock admission
(`acquireIndexMaintenance`, `acquireUnique`, both behind `WriteHost` where
decision 8 says); `capturePartialMaskLocked`, `captureIndexSnapshot` and
`captureIndexSnapshotParents`; publication and Restore storage
(`prepareIndexCatalog`, `prepareIndexRoots`, `prepareRemintedStorage`,
`prepareForeignIndexStorage`, `fenceEmptyIndexStorageLocked`,
`captureIndexLayouts`, `transportIndexLayouts`, `evolveStageIndexesLocked`,
`catalog_overlay_indexes.go`, the call sites in `catalog.go` and
`transaction_publication.go`); `validatePrivateIndexExpressions` (Finish);
`stageDomainIndexGenerationsLocked`; `executePartitionConstraintDropLocked`;
`partitionIndexTransaction`; the probe, claim and exclusion loops on
`StatementScope` with `uniqueConflict` and `exclusionScan` (decision 8);
`endLockOwner`, `removeExclusionOwner`, `checkTransactionExclusions`;
`executeSetConstraints` and `checkExclusionEvents`; `InspectIndex`;
`disclosureEnv`; the registry instances and the slot accessors; the thin
wrappers that build a `ddlHost` for callers outside the moved set
(`stageCreateUniques`, `stageExclusion`, ... until 24d moves their
callers).

**Commit sequence (23d.1).**

1. Pure helpers: `generatedRelationName` → `ddl.GeneratedRelationName`
   (callers in `identity.go`, `table_like_index.go`, `index.go`);
   `incompatibleNotValid` → `index.IncompatibleNotValid`;
   `rejectVirtualIndexUses` into `index` (table 24b moved both to
   `table`: move `table.IncompatibleNotValid` and
   `table.RejectVirtualIndexUses` down, since `table` imports `index`);
   `index.MethodOf`; delete the
   dead `partitionIndexPromotionPath`.
2. Seam: `index.Host`, `DropStage`, `ValidityTarget`; root
   `index_host.go` on `ddlHost` with the assertion and the new methods;
   `CatalogStage.RemoveRelation`; `dropProvider.DescriptorCount` and
   `ReplacePartitionDescriptor`. Unit test first, observed failing:
   `BindIndexPredicate` through the host binds the same predicate as
   `bindIndexPredicate`, and `LookupCollation` returns its 0A000 error.
3. Port in place: root functions over `index.Host` (and `DropStage`,
   `ValidityTarget`) for decisions 2–5 and 7, naming no `Engine`,
   `CatalogStage`, `AuthorizationView` or `StatementScope`; the
   table-owned edges hoisted; the lock windows of decision 2. Old
   entry points become thin wrappers that build the `ddlHost`.
4. Move them into `index` with `movex` and qualify; no aliases.
5. Docs: the `index` README (`Host`, entry points, what stays), this
   file's Status, the §8 row and the status page; `AGENTS.md` unchanged.

**Commit sequence (23d.2).**

1. Seam: `index.WriteHost` with `UniqueProbe`, `ExclusionScan` and
   `ExclusionEdit`; the `*statementHost` methods carved out of today's
   windows in place, the root callers calling them directly. Unit test
   first, observed failing: `PublishExclusion` advances the registry
   generation once and a scan captured before it is not current.
2. Port in place: the pure write decisions as root functions over the
   captured values.
3. Move them into `index` with `movex`; no aliases.
4. Docs, as in 23d.1.

Proof for 23d: the engine units of "Targeted tests" (the exclusion,
concurrent-index, `unique_lifetime` and `index_hash_cancellation` units
also with `RACE=1`), the reference selections in quick and `REFERENCE=1`
mode, and quickcheck. For 23d.2 also compare `go build -gcflags=-m
./internal/engine` on `probeUniqueRow`, `publishExclusionCandidate` and
`probeExclusion` before and after: no new escape on the per-row path;
report any new direct call that replaced inline code there.

### 23e — Pure index algorithms (linear item 126)

Planned by the end-state review (linear item 114, ENGINE-SPLIT.md §2.8 and
§6 "Third round"), measured at `17e01ca8b`. Needs —.

Functions over value inputs the root captures under `Engine.mu`:

- `captureIndexSnapshotParents` (`index_snapshot.go:18-121`), with the
  root keeping the check at its start and building the lookups.
- `prepareRemintedStorage` (`index_replacement.go:26-115`).
- The loop of `validatePrivateIndexExpressions`
  (`index_publication.go:112-203`); the locked authority prelude stays.
- The decision of `captureIndexLayouts` (`index_layout_transport.go:37-89`).
- SET CONSTRAINTS name selection (`set_constraints.go`); the unique loops
  it calls belong to dml 29d.2, so rebase on 110b if it lands first.
- The grant plan of `acquireIndexMaintenance`
  (`index_maintenance_locks.go`: targets, missing-name notice, parent and
  index collection, option checks, lock modes); the acquire loop stays.
- `index.EffectiveRoot(committed, journal, addr)` replaces the copies in
  `statement_host_index.go` (four), `index_snapshot.go`,
  `materialized_refresh.go` and `index_layout_transport.go`.
- Delete the dead `bindIndexKey` and the test-only `bindIndexAttributes`,
  `stagePartitionIndexAttach` and `stagePartitionIndexDrop` wrappers
  (point the tests at the package functions), and correct the README
  sentence about wrappers that wait for 24d and 25d.

Verify with `make build`, the targeted tests below plus the unit tests of
the touched files, the quick and `REFERENCE=1` selections for the area, and
quickcheck; no behavior, error text or error-order change, no aliases.

### 23f — Row publication and hash capture (linear item 142)

Planned by the closing review (linear item 133, ENGINE-SPLIT.md §2.9 and
§6 "Fourth round"), measured at `4c05da8fe`. Needs —.

- `prepareRowPublication` (`transaction_row_publication.go`, about 100
  lines): its only engine use is `e.catalog.Domain`, so the domain is
  passed in. It runs outside the lock, over private roots.
- `captureHashIndexesLocked` (`query_admission.go`) over the eligibility
  flag, the live state, the storage relation and index-root maps and the
  commit sequence; it stays `…Locked`, with the caller holding
  `Engine.mu`.
- `exclusionMark` and its helpers (`savepoints.go`, clean).

About eight test call sites use `e.prepareRowPublication` (exclusion,
unique, hash and prepared-boundary tests); adapt them.

Verify with `make build`, the unit tests of the touched root files and
the target package, quickcheck, and the quick and `REFERENCE=1` selections
for unique, exclusion and hash indexes, savepoints and two-phase commit; no behavior, error text, error-order or lock-order change, no
aliases. Re-run `reach` on the moved set first: sizes are approximate.

## Invariants

- **Lock order.** The lock manager stays innermost. Writer waits, snapshot
  waits and unique key pins never run under `Engine.mu`. Every host method
  documents either "under `Engine.mu`, never waits" or "waits; caller
  holds nothing".
- **Exclusion registry.** All six `exclusionGeneration` increments stay;
  `exclusionScanCurrent` keeps comparing generations; savepoint rollback
  restores the projection and the modes together.
- **Index build waits** are cleared at transaction end and by the
  serializable fence (`serializable.go`); `InspectIndex` reads them.
- **CONCURRENTLY.** Cancellation between phases still releases the SHARE
  UPDATE EXCLUSIVE lock; a late success is never retracted.
- **Publication atomicity.** `prepareIndexPublication` runs inside the
  publication window; the catalog, storage relations, index roots and the
  exclusion reset are installed in one `Engine.mu` window.
- **Fences.** The order `fenceStorageRead`, `fenceIndexSet`,
  `fenceNamespace` (`catalog_message.go`) is unchanged; fence kinds stay in
  `catalog_fence.go`.
- **Journal.** `indexJournal.clone` stays copy-on-write; savepoint marks
  keep sharing older journals.
- **Disclosure.** Unique and exclusion key details still follow the row
  security and privilege rules of `uniqueKeyDetailAllowed`; error text,
  SQLSTATEs and the order of checks are unchanged.
- **Dump.** `indexDefinitionSQL`, `usingIndexDump` and the index snapshot
  datum remap stay byte-identical.
- **Write path.** Per-row index maintenance gets no new allocation or
  extra indirection. If a host call replaces a direct call on the per-row
  path, say so in the report.

## Targeted tests

- **Engine units:** `index_*_test.go`, `unique_*_test.go`,
  `exclusion_*_test.go`, `using_index_test.go`,
  `concurrent_index_*_test.go`, `partition_unique_replay_test.go`,
  `partition_using_index_replay_test.go`,
  `partition_attach_index_test.go`; grep `*_test.go` for every moved
  identifier. Run with `RACE=1`:
  `make check RACE=1 RUN='^TestConcurrentIndexU'` and the exclusion,
  `unique_lifetime` and `index_hash_cancellation` tests.
- **Reference selections** (quick mode and `REFERENCE=1`):
  `make check RUN='^TestReference(BtreeIndex|HashIndex|PartialIndex|UniqueIndex|ConcurrentIndex|IndexMaintenance|IndexClauses|IndexProperties|ExpressionIndex|UsingIndex|Exclusion|InsertConflict|UniqueRepeatable|UniqueType)'`,
  then the same with `REFERENCE=1`; add
  `'^TestReference(AlterAddConstraint|PartitionConstraints|RangePartition|HashPartition|DumpRoundTrip)'`
  for the constraint, partitioned-index and Dump paths.
- `PartitionExclusion*` has known `REFERENCE=1` oracle drift on `master`;
  compare with `master` before reporting a failure there.
- Never modify `reference/` or `smoke/`. Tests follow the code; they never
  keep code in the root.

## Documentation

- Create `internal/engine/index/README.md` in 23b: what the package owns,
  the registries (no mutex, `Locked` suffix, the generation rule), the
  journal slot, the entry points, what stays in the engine, and a
  `## Testing` section with the selectors moved out of
  `internal/engine/README.md`.
- Update the `storage` README (`indexOutputMatches` is now `index` code),
  the `lock` README (`uniqueKeyValues` moved), the `session` README (the
  index journal type), the `typedef` README (domain index outputs) and the
  `stored` README if 22b listed index renderer methods.
- `AGENTS.md`: add `index` to the subpackage brace list; the file must not
  grow.
- Tick the item in [ENGINE-SPLIT-STATUS.md](ENGINE-SPLIT-STATUS.md) and
  update the step 23 cell of ENGINE-SPLIT.md §8.

## Done when

Part A: the clean set, the registries' types and the slot records live in
`index`; `index` imports no package above it. Part B: the CREATE INDEX,
ALTER/DROP INDEX, REINDEX, constraint-index and partitioned-index
decisions and the write-time enforcement decisions live there too, behind
`index.Host` and `index.WriteHost`; the root keeps only the orchestration
listed above. The checks in §8.3 pass.

## Status

- [x] 23a — In place: renames, registries, cuts (linear item 82)
- [x] 23b — `index` created and clean set moved (linear item 83)
- [x] 23c — Host refined (docs only; linear item 84; the docs commit).
  Measured at `42a45f774`: about 150 engine edges over the 33 root index
  files, most covered by `ddl.Host` and four existing `ddlHost` methods.
  `index.Host` on `ddlHost` adds `IndexStage`, `DescriptorStage`,
  `ACLOutput`, `DefinitionReader`, `LookupCollation`,
  `BindIndexExpression` and `BindIndexPredicate`; the draft's
  `Evaluation`, storage reads, `TypeDiagnosticName` and `TrustedRestore`
  are not needed. DROP staging and the validity pass take narrow stage
  interfaces, no host. `table.Host` embeds `index.Host`; table-owned edges
  are hoisted to the callers. `index.WriteHost` on `statementHost` has
  coarse self-locking windows and waits, no callbacks; the registry
  primitives stay caller-locked and root-only; the probe loops,
  `uniqueConflict` and `exclusionScan` stay root until `dml`. SET
  CONSTRAINTS stays; `IndexProperty`'s decision becomes `index.Property`.
  23d split into 23d.1 (85a) and 23d.2 (85b); move sets and commit
  sequences written.
- [x] 23d.1 — DDL decisions moved (linear item 85a; `5e230528b`, `e46899fe6`,
  `506da64a4`, `a92895430`, then the docs commit). CREATE INDEX, ALTER/DROP INDEX,
  REINDEX, key binding, UNIQUE/EXCLUDE staging, the ALTER TYPE backing
  rebuilds, USING INDEX, the partitioned-index staging and `Property` are
  `index` functions over `index.Host` on `ddlHost`; no aliases. See the
  23d.1 record.
- [x] 23d.2 — Write-time decisions moved (linear item 85b; `8b8d9e3af`,
  `b63014942`, `2f9b50b4b`, then the docs commit). `index.WriteHost` on
  `*statementHost` holds the windows and waits; the unique binding, root lower
  bound, existing output, exclusion visit, projection edits and ALTER shape
  entries are `index` functions; the probe loops stay root until `dml`; no
  aliases. See the 23d.2 record.
- [x] 23e — Pure index algorithms moved (linear item 126; `cde98e8e6`,
  `bf853e512`, `c0cefcab1`, `459cf5149`, `50c210b8a`, then the docs commit).
  The snapshot capture, storage reminting, private expression validation,
  layout capture, SET CONSTRAINTS name selection and the maintenance grant
  plan are `index` functions over inputs the root captures under
  `Engine.mu`; `index.EffectiveRoot` replaces nine copies; the dead and
  test-only wrappers are deleted; no aliases. See the 23e record.
- [x] 23f — Row publication and hash capture (linear item 142;
  `6b8f2d4b3`, `44b0296a0`, `107d5c96e`, then the docs commit).
  `index.PrepareRowPublication` with the catalog domain passed in,
  `index.CaptureHashIndexesLocked` over a root-filled `HashCapture` under
  `Engine.mu`, and the savepoint `index.ExclusionMark` with its slot helpers;
  no aliases. See the 23f record.

Outstanding aliases: none (23b added none).

### 23a record

Done on `implement-part/PG18-ENGINE-SPLIT-INDEX-IN-PLACE`, rebased on
`92ce8c794`. No aliases. Measured at `c02c9d97e` with `reach` over the 47
inventory files under their 23a names (gains are clean declaration lines):

| Commit | Change | Gain |
|---|---|---:|
| `61d7f49b6` | Renames and moves (step 2) | +77 (moves into the set) |
| `36cdc6168` | Registries (step 3) | +105 (new registry methods) |
| `98ee18fcf` | Cut a: validators over `access.View` and an environment | +613 |
| `dec8ce2ea` | Cut b: `prepareIndexPublication` a function | +193 |
| `b29db018c` | Cut c: index evolution functions | +107 |
| `79bc16332` | Cut d: `indexDescriptorStage` | +106 |
| `040604526` | Cut e: layout capture and transport split | +173 |

Clean went from 4,289 to 5,646 declaration lines (196 to 243 declarations).

- **Moves.** As step 2 lists, plus `partitionReplayOnlyExclusion` (the
  replay predicates call it; the partition prompt is updated).
  `partitionRowHistory.nextRow` moved with its type. `delete.go` renamed a
  local `storage` to `store` so `storage.RowByID` resolves.
- **Registries.** `indexWaitRegistry` (`index_wait.go`; `admitLocked`,
  `clearLocked`, `endOwnerLocked`, `clearAllLocked`, `lookupLocked`) and
  `exclusionRegistry` (`exclusion_registry.go`; `installLocked`,
  `projectionLocked` (the clone), `ownLocked` (the shared read savepoint
  capture and the nondeferrable key read take today), `removeLocked`,
  `restoreLocked`, `resetLocked`, `scanLocked`, `currentLocked`). Fields
  `Engine.indexWaits` and `Engine.exclusions`. All six generation
  increments keep their place. Exclusion, concurrent-index, savepoint
  exclusion, `unique_lifetime` and `index_hash_cancellation` units passed
  with `RACE=1`.
- **Cut a.** A nil environment means trusted publication: a nil view
  becomes `(nil, nil)`, the engine-less "trusted replacement" view
  `prepareIndexPublication` built becomes `(&view, nil)`, a live view
  `(&view, env)`. No caller built an engine-less view for another reason.
  Per-pair validators take `*access.View` (no struct copy per pair).
  `disclosureEnv` maps an `*AuthorizationView`; `indexDisclosureEnv`
  (`index_disclosure.go`) adds `exec.AdmissionAuthority` and
  `TypeDiagnosticNames(access.View)`, which `*AuthorizationView`
  implements, so ALTER TYPE builds and publication name types exactly as
  before without a closure. `relationDropOwner` also takes view and
  environment, so `indexMaintenanceGrant.check` is clean. Table 24a had
  already converted `uniqueKeyDetailAllowed` the same way. Where the
  environment is the caller's view rather than its recaptured or
  runtime-owner copy, nothing changes: `View.Effective` consults the
  environment only for namespace descriptors, and key disclosure asks only
  about the parent relation and its columns.
- **Kept dirty (user decision).** `exclusionScan` and `uniqueConflict` keep
  their `AuthorizationView`: an environment there boxes the view, one heap
  allocation per exclusion probe and per ON CONFLICT hit. 23d settles them
  behind `index.WriteHost`.
- **Cut b.** Deviation: the root adapter `Engine.prepareIndexRoots` reads
  the catalog and recaptures the view before the call, instead of passing
  a read function, because the recapture needs the view first.
  `RoutineAuthority` is still the recaptured `*AuthorizationView`
  (`routineAuthorityView` type-asserts it). Same heap moves as before.
- **Cut c.** Deviation: not over `indexCoverageReader`. The selector
  `indexEvolutionInput` reads `indexEvolutionReader` (`removed`,
  `replacement`), which `*CatalogStage` implements and the provider's stage
  branches delegate to; `applyIndexEvolution` (the old
  `dropProvider.evolveIndex`) and `evolveIndexes` write
  `indexEvolutionTarget` (`*dropProvider`; new `evolutionBase` and
  `removeIndex`). `evolveStageIndexesLocked` builds a provider only for an
  affected index, so the zero-allocation tests for untouched parents pass.
- **Cut d.** `indexDescriptorStage` = `ddl.Stage` +
  `RenameStagedDescriptor` + `StageBase` (new `*CatalogStage` method).
- **Cut e.** `captureIndexLayouts` (root, takes `Engine.mu`) and
  `transportCapturedIndexLayouts` (pure) over `indexLayoutCapture`;
  `transportIndexLayouts` composes them. `transportEmptyIndexStorage`
  records candidates through a callback (`CatalogStage.recordIndexCandidate`
  for TRUNCATE).
- **Open questions.** `rowPublication` is built by
  `transaction_publication.go`, filled by `prepareRowPublication`, and read
  only by `mergeIndexRoot`, `mergeHashIndexRoot`, `publicationSlot` and
  `prepareIndexPublication`; 23b can move it. The 10e question was settled
  in planning.
- **Tests.** No test moved or was deleted. Root unit tests follow the new
  signatures and fields (`e.exclusions.pending`, `e.indexWaits.waits`,
  `e.prepareIndexRoots`, view and environment arguments).
- **Checks.** Targeted units per commit (registry units also with
  `RACE=1`); quickcheck passed at the final tip; the prompt's reference
  selection passed in quick mode and with `REFERENCE=1`, except three
  `/postgres` leaves of `TestReferenceAlterAddConstraintDiagnostics` (55000
  "cannot create not-null constraint"), which fail the same way on a clean
  `92ce8c794` snapshot (NOT NULL oracle drift). racecheck, finalcheck and
  bench skipped at user request (no startup change).

### 23b record

Done on `implement-part/PG18-ENGINE-SPLIT-INDEX-PACKAGE`, started at
`d12bb058f` (22b merged) and rebased onto `ab11d56ab` (stored 22c, fdw 26d,
role 28b). No aliases. Each code commit is rebuilt by a script in
`.cswd/tmp/PG18-ENGINE-SPLIT-INDEX-PACKAGE/` (`c1-moves.sh`, `c2.sh`,
`c3.sh`, `fixdoc.py`).

| Commit | Change | Files |
|---|---|---:|
| `5d34f151d` | Staging: the clean declarations of 36 mixed files to `idxmv_*.go` (`movedecl`); stored aliases in the moving files qualified as `stored.X` (`aliasqual`) | 68 |
| `b372d8d83` | Export in place: interface methods on five root interfaces and their implementers (`ifacern`), then the inbound functions, fields and methods (`expspec`/`movex`, 330 rows) | 294 |
| `0f4efc1d7` | Move: 48 files to `index`, `index/errors.go`, `movex -qual index` (170 rows), `index` locals renamed | 262 |
| `ef8ed94f9` | Registry maps kept private (`CountLocked`) | 8 |

- **Re-measure.** `reach` over the 23a files plus
  `domain_alter_index_outputs.go`: 339 declarations, 243 clean (5,688 of
  10,196 lines), within 1% of the 23a record; 22b changed no cleanness but
  emptied OUT of the stored helpers. The `frontier` fixed point added 11
  clean root declarations the set calls: `rowPublication` (planned), the
  replay chain of the 23a notes (`partitionHashReplayTarget`,
  `partitionPreservedReplayLocal`, `partitionPreservedReplayChild`,
  `partitionIndexPreservedInvalid`, `partitionReplayNameCompare`,
  `partitionDumpIndexBacking`), `physicalNotNullOriginValid`,
  `physicalPrimarySupportValid`, `sameRelationRowShape` and
  `relationDropOwner`. OUT was then `sqlError` and `insertError` only.
  187 top-level declarations with their methods moved, about 6.4k lines
  in 48 files.
- **Broad.** The qualify pass touched about 210 files outside the set, over
  the ~150 limit; the user approved running it as a broad item (no other
  broad item was running).
- **Names.** Types and functions lost a leading `index` (`Ordering`,
  `Journal`, `JournalOf`, `SlotOf`, `Scope`, `Stage`, `Snapshot`,
  `WaitRegistry`, `BuildWait`, `CoverageReader`, `EvolutionReader`,
  `EvolutionTarget`, `DescriptorStage`, `DisclosureEnv`, `DefinitionSQL`,
  `Metadata`, `CatalogKey`, ...); the rest are capitalized
  (`ExclusionRegistry`, `RowPublication`, `UniqueKeyValues`,
  `UniqueWriteBinding`, `IndexesMetadata`, `JournalIndexes`, ...). The
  six unexported types an exported signature named were exported. The
  README has the groups.
- **Deviations.** `journalIndexes`/`journalIndexesOrZero` moved
  (`index.JournalIndexes`, `index.JournalIndexesOrZero`) instead of
  staying in the root: `RetainIndexBuild`, `PrepareAlterIndexRoots` and
  `TransportEmptyIndexStorage` call them. They stay functions over
  `*session.Journal`. The interface methods `removed`, `replacement`,
  `replace`, `inputDescriptors` and `indexLayout` are shared with
  `dropDependencyReader` and `definitionIndexLayoutReader`, so those two
  root interfaces were exported with them (`Removed`, `Replace`, ...).
  `index` also imports `ddl` (`ddl.Stage`), `settings` (option parsing)
  and `typedef` (domain output reownership and collation remap), all
  lower layers.
- **Mechanics.** 180 root locals named `index` that would capture
  `index.X` became `idx` (two passes: renaming an inner one exposed two
  outer `index int`). `mergeRows` embeds `index.WriteBatch`, so its field
  is `WriteBatch`. The registries' maps stay private (`CountLocked` for
  tests): an exported projection map would allow a change without a
  generation step.
- **Tests.** `TestIndexPropertyColumnCapabilityMatrix` moved to
  `index/property_test.go`; every other test builds an engine or uses root
  helpers and stays, rewritten to `index.X`. Genuine fixture fixes:
  `TestHashIndexMergeCancellation` and
  `TestSerializablePublicationRealChangedPredecessorRetry` match frames by
  name and now look for `.MergeHashIndexRoot` and `.MergeIndexRoot`. The
  exclusion, concurrent-index and restore tests read the registries
  through `CountLocked`/`OwnLocked`. No test was deleted.
- **Checks.** `go vet` output identical to the base; string-literal
  multiset unchanged apart from `index/errors.go` and imports; targeted
  units and the 53 registry/concurrent/exclusion units with `RACE=1`
  passed; quickcheck passed; the reference selection of this prompt passed
  in quick mode and with `REFERENCE=1` (see the report for drift).
  racecheck, finalcheck and bench skipped at user request (no startup
  change).

### 23d.1 record

Done on `implement-part/PG18-ENGINE-SPLIT-INDEX-DDL-DECISIONS`, started
after table 24b merged (`bdba4968a`; the user chose to wait for it). The
re-measure found the 23c findings unchanged; 24b had moved
`incompatibleNotValid`, `rejectVirtualIndexUses` and
`usingIndexNotNullCandidate` into `table`. Scripts in
`.cswd/tmp/PG18-ENGINE-SPLIT-INDEX-DDL-DECISIONS/` (`c1.py`, `c4.py`,
`docs-index.py`).

| Commit | Change |
|---|---|
| `5e230528b` | `ddl.GeneratedRelationName`; `table.IncompatibleNotValid` and `table.RejectVirtualIndexUses` down to `index.IncompatibleNotValid` and `index.RejectVirtualUses`; `index.MethodOf`; the dead free `partitionIndexPromotionPath` deleted |
| `e46899fe6` | Seam, test first: `index.Host`, `KeyHost`, `DropStage`, `ValidityTarget`; `index_host.go`; `CatalogStage.StageRead`/`RemoveRelation`; `dropProvider.DescriptorCount`/`ReplacePartitionDescriptor` |
| `506da64a4` | Port in place: the decisions as root functions over the host (staging files `idxmv_*.go`), table edges hoisted, lock windows of decision 2 |
| `a92895430` | Move into `index` (`c4.py`: new `bind.go`, `create.go`, `maintenance.go`, `unique_stage.go`, `exclusion_stage.go`; the rest appended to the existing files) and qualify the root callers |

- **Deviations from the 23c sketch.** (1) `KeyHost` (`AccessView`,
  `SearchNamespaces`, `LookupCollation`), embedded in `Host`, types
  `LookupClass` and `BindKey`, so the analysis host and partition key
  binding pass a stage-less `ddlHost` that cannot reach a nil stage.
  (2) `DropStage` also has `StageRead`: drop staging reads the candidate
  catalog. (3) `Host` also has `ValidityTarget()` (the stage's drop
  provider): `StagePartitionIndexAttach` ends with the validity pass.
  (4) `StageUsingIndex` (the old `StagePreparedUsingIndex`) takes the
  narrow `UsingIndexStage` (`DescriptorStage`, `EmitNotice`), not `Host`:
  attaching needs no lock, and ALTER TABLE and the inheritance consumer
  keep calling it unlocked as before. (5) `PrepareUsingIndex(a
  ddl.Allocator, reader, parent, effective, captured)`: the view was only
  for the hoisted NOT NULL preparation, and `effective` is the prepared
  parent while `Parent` keeps the original for the attach check.
  (6) `StageUniqueDefinitions`/`StageCreateUniques` and `StageMaintenance`
  take no authority (none was used). (7) `ValidateMaintenance` takes the
  `Stage` slot by value, so a nil slot reads as zero as before.
- **Table edges.** CREATE TABLE and PARTITION OF record each declaration's
  LIKE source before `StageCreateUniques`. ALTER TABLE and typed mutation
  run `rebuildAlterNotNullsLocked` between `RebuildAlterUniques` and
  `RebuildAlterExclusions`; it now returns its parent for no changes, which
  keeps the old order (no NOT NULL pass without transformations).
  `prepareUsingIndexMetadata` runs `table.UsingIndexNotNullCandidate`
  first unless the capture prepared it.
- **Lock windows.** ALTER TABLE captures USING INDEX inside the
  `evolveStageIndexesLocked` window (two windows merge).
  `prepareUsingIndexMetadata` holds `Engine.mu` across `PrepareUsingIndex`
  (it only held it around the OID): the stage reader and the inheritance
  definition reader with its prepared-drop deltas take no lock. DROP INDEX
  staging stays unlocked; `RemoveRelation` locks through `Remove`. No wait
  runs under the lock.
- **CREATE INDEX.** The stage, its authorization and output environment
  now begin before `PrepareCreate` (no side effects) and are discarded on
  every early exit, the IF NOT EXISTS skip included; the NOTICE still
  follows the unlock. The CREATE check is `CheckCreateIn`, whose Restore
  42501 exception re-checks the trusted Restore authority
  `TrustedRestoreDestination(auth, "index")` already proved, so the result
  and the "invalid trusted restore index authority" text are unchanged.
  `CreatePlan.Namespace` feeds the relocked IF NOT EXISTS recheck.
- **REINDEX.** The root now discards the stage on every `StageMaintenance`
  error; the two REINDEX error paths used to skip it, with no effect.
- **Stays in the root** as thin wrappers over a `ddlHost`:
  `lookupIndexClass` (analysis host), `bindIndexKey` (partition key
  binding), `bindIndexAttributes` (unit tests), `stagePartitionIndexChild`
  and `stagePartitionIndexChildren` (partition ATTACH, PARTITION OF,
  replay), `stagePartitionIndexAttach` (unit tests),
  `stagePartitionIndexValidity` (the one-line provider builder),
  `stagePartitionIndexDrop`/`stagePartitionConstraintDrop` (ALTER TABLE,
  `executePartitionConstraintDropLocked`, unit tests),
  `prepareUsingIndexMetadata`/`stageUsingIndex` (ALTER TABLE, inheritance)
  and the `validateIndexMaintenance` forwarder. The other old entry points
  are gone; their callers call `index` directly.
- **Tests.** New `index_host_test.go` (the seam test, observed failing
  first: host and engine predicate binding agree; `LookupCollation` keeps
  the 0A000 "collation is unsupported" error). `primary_key_lifecycle_test.go`
  rebuilds through a helper that calls `index.RebuildAlterUniques` then
  `rebuildAlterNotNullsLocked`, as ALTER TABLE does;
  `partition_constraints_test.go` passes `op.Context()` to
  `PartitionIndexAttachCheck`. None moved or deleted.
- **Checks.** `frontier` OUT of the ported set was `sqlError` alone;
  `index` imports nothing above it (`go list`); the string-literal multiset
  changed only by the shared CREATE check (`"42501"` and one copy of the
  Restore text) and import paths. Targeted units (293) passed per commit,
  also with `RACE=1`, as did the concurrent-index, exclusion,
  `unique_lifetime` and `index_hash_cancellation` units; the reference
  selection passed in quick mode and quickcheck passed. With `REFERENCE=1`
  the prompt's selection passed except three `/postgres` leaves of
  `TestReferenceAlterAddConstraintDiagnostics` (55000 "cannot create
  not-null constraint"), which fail the same way on a clean `bdba4968a`
  snapshot (NOT NULL oracle drift). racecheck, finalcheck and bench skipped
  at user request (no startup change).

### 23d.2 record

Done on `implement-part/PG18-ENGINE-SPLIT-INDEX-WRITE-DECISIONS`, started at
master `116432550` and rebased onto `edba54105` (docs-only commits in
between). Scripts in `.cswd/tmp/PG18-ENGINE-SPLIT-INDEX-WRITE-DECISIONS/`
(`c3.py`, `dropimports.py`, `escape.py`, `lits.sh`, `docs-edit.py`).

| Commit | Change |
|---|---|
| `8b8d9e3af` | Seam, test first: `index.WriteHost`, `UniqueProbe`, `ExclusionScan`, `ExclusionEdit`; `statement_host_index.go` carves the `*statementHost` methods out of today's windows; `acquireUnique` and `waitCompletion` removed, every caller uses the host |
| `b63014942` | Port in place: the pure decisions as root functions in `idxwmv_unique.go`/`idxwmv_exclusion.go`; `frontier` OUT empty |
| `2f9b50b4b` | Move into `index` (`c3.py`: new `unique_probe.go`, `exclusion_visit.go`, `exclusion_shape.go`; additions to `unique_binding.go`, `exclusion_projection.go`, `scope.go`) and qualify the root callers |

- **Deviations from the 23c sketch.** (1) The probe and scan captures read
  the write authority in the same window as their plain fields, but `index`
  types cannot hold the root `AuthorizationView` and boxing it would allocate
  per probe. The concrete root methods `captureUniqueProbe` and
  `captureExclusionScan` return the view as well; the exported
  `CaptureUniqueProbe`/`CaptureExclusionScan` drop it and serve the interface
  assertion and the seam test. 29c decides how the authority crosses. (2)
  `UniqueRootRange` is `UniqueRootStart`: it returns only the lower bound, and
  the equal-run loop stays in the root because it checks DML state and
  compares lazily. (3) The exclusion visit is `OwnExclusionEntry` (so the
  evaluation context is still built only after the own entry is found),
  `VisitExclusion` returning an `ExclusionVisit`, and the free function
  `ExclusionDeferred`. `VisitExclusion` takes the `*session.Operation` and
  calls `Context()` per key comparison, as before: that context can change
  while COPY input is held. (4) The edits are methods: `ExclusionEdit.Apply`
  and `ExclusionProjection.DiscardVersion`, `EndRow`, `Admit`, `ReplaceShape`;
  `ExclusionShapeEntries` is the ALTER shape loop.
- **Lock windows.** Unchanged: each `WriteHost` window is today's, in the
  same order with the same errors. `AdmitExclusion` keeps its standalone
  window with no epoch check, as before. `heads` (the head versions of the ON
  CONFLICT probe) is computed before the capture from the bound index;
  `SameIndexKeySemantics` compares the keys (with their derived-key pointers),
  so the refreshed index has the same runtime domain conversion and the result
  is the same.
- **Per-row path.** `go build -gcflags=-m` before and after (per enclosing
  function, positions dropped): every allocation site moved with its code;
  `Admit` and `RetainExistingOutput` are now inlined into their callers, so
  their sites are reported twice without a runtime change. The only new escape
  is the ALTER shape entries map, which `ExclusionShapeEntries` returns (once
  per ALTER TABLE, not per row). New direct calls replace inline code:
  `index.UniqueRootStart`, `ExistingRootOutput`, `OwnExclusionEntry`,
  `VisitExclusion`, `ExclusionDeferred`, `ExclusionEdit.Apply`, and the
  concrete `*statementHost` window methods. No interface dispatch on the
  per-row path.
- **Tests.** New `index_write_host_test.go` (the seam test, observed failing
  first): `PublishExclusion` and `DiscardExclusion` each advance the registry
  generation once, so a scan captured before them is not current and one
  captured after is. `insert_conflict_update_stage_test.go` calls
  `writeHost().ClaimUniqueKey` instead of `acquireUnique`. None moved or
  deleted.
- **Checks.** `index` imports nothing above it (`go list`); the
  string-literal multiset changed only by import paths. Targeted units (the
  index, unique, exclusion, ON CONFLICT, MERGE, UPDATE, DELETE, COPY,
  savepoint, transaction, partition and ALTER selections) passed per commit,
  as did `RACE=1` over `ConcurrentIndex`, `Exclusion`, `UniqueClaim`,
  `UniqueLifetime`, `HashIndexMergeCancellation`, `InsertConflictUpdate` and
  the new seam test; the quick-mode reference selection passed and quickcheck
  passed. With `REFERENCE=1` the selection (index, unique, exclusion, ON
  CONFLICT, ALTER ADD CONSTRAINT and partition constraint/exclusion tests)
  passed except 15 `/postgres` leaves: three of
  `TestReferenceAlterAddConstraintDiagnostics` (55000 "cannot create not-null
  constraint") and 12 of `TestReferencePartitionExclusion*`, which fail
  identically on a clean `edba54105` snapshot (oracle drift); every stuntgres
  leaf passed. racecheck, finalcheck and bench skipped at user request (no
  startup change).

### 23e record

Done on `implement-part/PG18-ENGINE-SPLIT-INDEX-ALGORITHMS`, started at
master `c378ffbcc` and rebased onto `eb545a156` (table 24e, item 123,
and three unrelated commits landed in between; no code conflict).
Scripts and logs in `.cswd/tmp/PG18-ENGINE-SPLIT-INDEX-ALGORITHMS/`.

| Commit | Change |
|---|---|
| `cde98e8e6` | `index.EffectiveRoot` (test first) and the replaced copies |
| `bf853e512` | `index.CaptureSnapshot` over `SnapshotCapture`; `index.RemintStorage` over `RemintBase` |
| `c0cefcab1` | `index.ValidatePrivateExpressions`; `index.CaptureLayouts` over `LayoutCaptureInput` |
| `459cf5149` | `index.SelectConstraintName`, `SelectInheritedExclusions`; the `MaintenanceGrant` plan steps |
| `50c210b8a` | Dead and test-only wrappers deleted; tests call the `index` functions |

- **Shape.** Each moved decision takes a value input that the root fills in
  its existing `Engine.mu` window after its own checks (cancellation,
  owner, epoch, frontier); the root holds the lock across the call, as
  before. `captureIndexSnapshotParents`, `prepareRemintedStorage`,
  `captureIndexLayouts` and `validatePrivateIndexExpressions` keep their
  signatures, so callers and tests did not change. `SnapshotCapture` takes
  the current store and root as lookups: the store is the root's
  `privateStorageLocked`, which is not copied into `index`.
- **Maintenance grant plan.** Relation resolution stays in the root under
  `Engine.mu`, so no callback runs under the lock: `MaintenanceNames`
  (before the lock, with the REINDEX checks), then `AddTarget` per resolved
  name (the root passes `table.RenameMissing`, which `index` cannot
  import), `CollectGroup`, `NeedsAttach`/`AddAttach` (the root resolves the
  partition), `FinishPlan`; after the unlock `SortLockOrder`, `ParentMode`
  and `AlterTarget`. The acquire loop and its recheck stay. The order of
  checks, resolutions, errors and locks is unchanged.
- **Deviations.** (1) `EffectiveRoot` also replaces the identical copies
  in `foreign_key_writes.go` (two) and `domain_alter_message.go`, nine in
  all. (2) `CaptureExclusionScan` and `ExclusionScanCurrent` tested map
  presence, not a non-nil root. Every `UniqueRoots` write stores a built
  root (`RetainIndexBuild` and the stage candidates, whose roots come from
  `BuildIndexRoot`, `BaselineRoot`, `TransportRows` or
  `NewLogicalIndexRoot`, none nil), so the forms read the same.
- **Tests.** New `index/effective_root_test.go` (observed failing first).
  `materialized_refresh_concurrent_test.go`, `table_like_partial_test.go`
  and `index_clauses_test.go` call `index.BindAttributes` with a `ddlHost`;
  `partition_constraints_test.go` calls `index.StagePartitionIndexAttach`
  and `index.StagePartitionIndexDrop`. Two locals named `index` were
  renamed. None moved or deleted.
- **Checks.** `index` imports nothing new (`go list`); the string-literal
  multiset of `internal/engine` changed only by import paths. Targeted
  units (index, unique, exclusion, partition, foreign key, materialized
  refresh, domain, replacement, Restore, Dump, SET CONSTRAINTS and the
  DROP/ALTER/REINDEX INDEX tests) passed per commit; `RACE=1` over
  `ConcurrentIndex`, `Exclusion`, `UniqueClaim`, `UniqueLifetime`,
  `HashIndexMergeCancellation`, `IndexWriteHost`, `IndexMaintenance` and
  `EffectiveRoot` passed; the quick reference selection and quickcheck
  passed, on the rebased tree too. With `REFERENCE=1` the selection
  (index, unique, exclusion, ON CONFLICT, ALTER ADD CONSTRAINT, SET
  CONSTRAINTS, REINDEX/DROP/ALTER INDEX, partition constraint and Dump
  round-trip tests) passed in full. `make deps` lists only this module.
  racecheck, finalcheck and bench skipped at user request (no startup
  change).

### 23f record

Done on `implement-part/PG18-ENGINE-SPLIT-INDEX-ROW-PUBLICATION`, started at
master `3fd377a2d` and rebased onto `80706f8ca` (one unrelated geometry
commit landed in between; no conflict). Logs in
`.cswd/tmp/PG18-ENGINE-SPLIT-INDEX-ROW-PUBLICATION/`. `reach` on master
confirmed the plan: `exclusionMark` and its helpers were clean, and
`prepareRowPublication` reached `Engine` only through `e.catalog.Domain`.

| Commit | Change |
|---|---|
| `6b8f2d4b3` | `index.PrepareRowPublication`; `RowPublication` moves beside it into `row_publication.go`; `transaction_row_publication.go` removed |
| `44b0296a0` | `index.CaptureHashIndexesLocked` over `HashCapture`; root `captureHashIndexesLocked` keeps the capture window |
| `107d5c96e` | `index.ExclusionMark`, `ExclusionMarkSlotOf`, `ExclusionMarkOrZero`; the test moves with them |

- **Shape.** `PrepareRowPublication(p, domain, ref, materialize)` is the old
  body with `e.catalog.Domain` replaced by `domain`; transaction publication
  passes the engine's domain and still calls it outside `Engine.mu`.
  `HashCapture` holds the eligibility flag (an implicit statement without a
  private catalog), the published catalog, storage relations, index roots
  and commit sequence. The root fills it under `Engine.mu` and the package
  function keeps the `Locked` suffix; the guard order is unchanged, and
  the engine fields (plain reads under the same lock) are read before the
  guards instead of after them. The
  savepoint capture, restore and ROLLBACK TO windows store and read an
  `index.ExclusionMark`.
- **Deviations.** (1) `executeSavepoint`'s local `index` (the savepoint
  position) is renamed `position`, since it would shadow the package.
  (2) `session/root_slots.go` and `session/savepoint_marks.go` comments now
  name the `index` implementer.
- **Per-row and query paths.** `go build -gcflags=-m`: the `HashCapture`
  literal does not escape and `PrepareRowPublication` allocates what the
  root method did; the publication loop and query admission each make one
  direct call instead of a method call.
- **Tests.** `savepoint_exclusion_test.go` moved to
  `index/exclusion_mark_test.go` (package `index`, same test names). Twelve
  calls in `transaction_prepared_boundary_test.go`,
  `exclusion_publication_test.go`, `unique_index_publication_test.go`,
  `insert_conflict_position_test.go`, `index_hash_cancellation_test.go` and
  `index_hash_test.go` call `index.PrepareRowPublication` with the engine's
  domain. None deleted.
- **Checks.** `index` imports nothing new (`go list`); the string-literal
  multiset of `internal/engine` changed only by import paths. Targeted
  units (index, unique, exclusion, hash index, query hash, prepared
  boundary, ON CONFLICT position, savepoint, lock-mode and TRUNCATE
  savepoint, serializable publication) passed per commit; `RACE=1` over
  `Exclusion`, `UniqueLifetime`, `UniqueClaim`,
  `HashIndexMergeCancellation`, `Savepoints` and
  `PreparedValidationOmitsInstallationRoots` passed; the quick `HashIndex`,
  `BtreeIndex` and `PartialIndex` reference selection passed; quickcheck
  passed on the rebased tree. With `REFERENCE=1` the hash index, unique
  index, exclusion, ON CONFLICT, partial-index savepoint, lock-mode
  savepoint and prepared-transaction selection passed in full. `make deps`
  lists only this module. racecheck, finalcheck and bench skipped at user
  request (no startup change).

### Notes for later steps

- 23b layering (done in 23b): `index_partition_replay.go` still calls
  `partitionPreservedReplayLocal` (with `partitionPreservedReplayChild`,
  `partitionReplayNameCompare`, `partitionIndexPreservedInvalid` and
  `partitionDumpIndexBacking`, `partition_invalid_dump.go`), and
  `partitionNeedsRowHistory` calls `partitionHashReplayTarget`, which the
  table prompt sends to `table` (24b), above `index`. 23b must move that
  chain into `index` (it is clean catalog reading) or leave these
  predicates in the root.
- 23d.2 (done): `index.Host` and the 23d.1 functions are DDL-only;
  `WriteHost` is separate (decision 8).
- 24d (89): the table handlers already call the `index` staging directly
  with a `ddlHost`; the wrappers `stageUsingIndex`,
  `prepareUsingIndexMetadata` and `stagePartitionConstraintDrop` go with
  their callers. 25d: `stagePartitionIndexChild`/`Children`,
  `stagePartitionIndexValidity` and `bindIndexKey` (partition keys) go with
  theirs.
- 23d: `exclusionScan` and `uniqueConflict` still hold `AuthorizationView`
  (see the 23a record). Settled in 23c (decision 8): they stay root with
  the probe loops, `index` returns the hit, and the root keeps the
  key-disclosure check; `dml` (29c) decides their final home.
- 23b (done): the interfaces are `index.DisclosureEnv`,
  `index.EvolutionReader`/`EvolutionTarget`, `index.DescriptorStage` and
  `index.CoverageReader`, with exported methods on the root implementers.
- 23c/23d: the handlers still name the moved code with `index.` (about
  210 files). The other steps' prompt files cite the root names of the
  moved code; the name table in `internal/engine/index/README.md` maps
  them. `reference/catalogs/README.md` and
  `reference/locking_transactions/README.md` still name
  `projectIndexMetadata` and `mergeIndexRoot` (`reference/` is read-only
  for the split).

- Step 24 (`table`) imports `index` for `columnTransformation`, the
  constraint-index staging and the replay-shape predicates. 23c settled
  that `table.Host` embeds `index.Host` and calls the 23d.1 functions
  directly; `captureLikeSource`, `rebuildAlterNotNullsLocked`,
  `usingIndexNotNullCandidate` and `notNullCandidate`/`notNullDirect` are
  table's and 23d.1 hoists them to their callers.
- Step 25 (`partition`) imports `index` for the partitioned-index code
  (`index_partition_*.go`), the row history and `index.WriteBatch`.
- Step 29 (`dml`): the `StatementScope` methods left in `unique_dml.go`,
  `index_write_candidate.go`, `index_insertion.go` and
  `exclusion_admission.go` (the probe, claim and exclusion loops, with
  `uniqueConflict` and `exclusionScan`) are DML's. Since 23d.2 they call
  the `index` decisions directly and the root windows as
  `*statementHost` methods (`s.writeHost()`); `dml.Host` embeds
  `index.WriteHost` (29c), which then also settles how the probe and scan
  authority crosses (the 23d.2 deviation 1).
  `insert_write_overlay.go` is ON CONFLICT state and belongs to `dml`.
- `storage.RowByID` (23a) is also used by the DML update and merge code.
