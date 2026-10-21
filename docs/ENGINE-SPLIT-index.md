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
  - 23d (item 85): 84.
- **Effort:** high for 23a and 23b; xhigh for 23c; high for 23d.
- **Session size:** one sub-step per session. 23a is large: if it does not
  fit, finish steps 1–3 in one session and step 4 in the next, and leave
  the box unticked until both are done.
- **Linear items:** 23a = 82, 23b = 83, 23c = 84, 23d = 85. Items that wait
  for this step: 24b (87) needs 83, 24c (88) needs 84, 24d (89) needs 85,
  29a (107) needs 82, 29b (108) needs 83, 29c (109) needs 85, 30b (112)
  needs 83.
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
starting from this sketch, then write the final interfaces, the move set
and the commit sequence into this file. Wait for approval.

**`index.Host`** — the DDL side. Every method runs with `Engine.mu` held,
never takes or releases it, and never waits.

| Group | Methods (draft) | Replaces |
|---|---|---|
| Shared DDL contract | the methods of `ddl.Host` (20a): authority views and recapture, CREATE checks, creation namespaces, dependency text, the stage, OID and revision allocation, notices, the context | the `AuthorizationView`, `CatalogStage` and `op` parameters of the stage handlers |
| Index stage slot | `IndexStage() *index.Stage` | `CatalogStage.indexes()` |
| Binding (the root builds the binders) | `BindIndexAttributes(auth, parent, stmt)`, `BindIndexExpression(auth, parent, raw)`, `BindIndexPredicate(auth, parent, raw)` | `Engine.bindIndexAttributes`, `bindIndexKey`, `validateSystemIndexKey`, `bindIndexExpression`, `indexExpressionBinder`, `bindIndexPredicate` |
| Catalog lookups | `LookupIndexClass(auth, method, name, usage)` | `Engine.lookupIndexClass` |
| Evaluation | `Evaluation() exec.Context` (statement presentation and cancellation) | the `exec.Context` literals the handlers build |
| Storage reads | `RelationStorage(address)`, `IndexRoot(address)` | reads of `e.storage` |
| Diagnostics | `TypeDiagnosticName(usage)` | `Engine.captureTypeDiagnosticName` |
| Restore | `TrustedRestore() bool` | the trusted Restore index authority check |

**`index.WriteHost`** — the DML side, implemented on `statementHost`.

| Group | Methods (draft) | Lock contract |
|---|---|---|
| Parents | `UniqueParent(ref)` | under `Engine.mu`, never waits |
| Key pins | `PinUniqueKey(ctx, key)` | waits on the lock manager; caller holds nothing |
| Exclusion registry | install, read, remove, scan and check projections | decide in 23c (see below) |
| Rows | latest committed row, own writes, the statement snapshot | under `Engine.mu`, never waits |
| Programs | `ReadmitPrograms()` after a wait | caller holds nothing |

Questions to settle in 23c:

1. Do the exclusion registry methods of `WriteHost` take `Engine.mu`
   themselves (short critical sections), or must the caller hold it?
   Never pass a callback that runs under the lock.
2. Constraint indexes of CREATE TABLE and ALTER TABLE
   (`stageUniqueDefinitions`, `stageCreateUniques`, `stageExclusion`,
   `stageCreateExclusions`, `stageUsingIndex`, `rebuildAlter*Locked`) are
   called by `table` (step 24). Recommended: `table.Host` embeds
   `index.Host`, and `table` calls `index` functions directly.
3. SET CONSTRAINTS (`executeSetConstraints`) is transaction-level:
   confirm it stays in the root.
4. Where the `IndexProperty` capability body (`statementHost`, over the
   clean `indexColumnCapability`) goes: `index` function plus a root
   forwarder, or unchanged.

### 23d — Move the handler decisions (linear item 85)

Follow 23c. The decisions that move:

- CREATE INDEX: `executeCreateIndex`'s checks (column limit, relation
  kind, predicate and CONCURRENTLY restrictions, options, capabilities),
  name choice and descriptor construction;
- DROP/ALTER INDEX and REINDEX: `executeIndexMaintenance` and
  `CatalogStage.validateIndexMaintenance`;
- constraint indexes, `USING INDEX` and the ALTER TYPE rebuilds (question
  2 of 23c);
- partitioned indexes: child propagation, ATTACH PARTITION of an index,
  validity promotion, partition constraint drops;
- write-time enforcement behind `WriteHost`: the unique probe and claim,
  existing-output and candidate validation, and the exclusion probe and
  projection decisions.

**Stays in the root:** `executeConcurrentIndex` and its phases
(`publishConcurrentIndexRoot`/`Valid`, `concurrentIndexAuthorityLocked`);
the waits (`waitIndexWriters`, `waitCompletion`, `waitOlderIndexSnapshots`);
`acquireIndexMaintenance` (lock admission); `capturePartialMaskLocked`,
`captureIndexSnapshotParents`; publication and Restore storage
(`prepareIndexCatalog`, `prepareRemintedStorage`,
`prepareForeignIndexStorage`, `fenceEmptyIndexStorageLocked`,
`catalog_overlay_indexes.go`, the call sites in `catalog.go` and
`transaction_publication.go`); `validatePrivateIndexExpressions` (Finish);
`endLockOwner`, `removeExclusionOwner`, `checkTransactionExclusions`;
`executeSetConstraints`; `InspectIndex`; the registry instances.

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
- [ ] 23c — Host refined (docs only; linear item 84)
- [ ] 23d — Handler decisions moved (linear item 85)

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

### Notes for later steps

- 23b layering (done in 23b): `index_partition_replay.go` still calls
  `partitionPreservedReplayLocal` (with `partitionPreservedReplayChild`,
  `partitionReplayNameCompare`, `partitionIndexPreservedInvalid` and
  `partitionDumpIndexBacking`, `partition_invalid_dump.go`), and
  `partitionNeedsRowHistory` calls `partitionHashReplayTarget`, which the
  table prompt sends to `table` (24b), above `index`. 23b must move that
  chain into `index` (it is clean catalog reading) or leave these
  predicates in the root.
- 23d: `exclusionScan` and `uniqueConflict` still hold `AuthorizationView`
  (see the 23a record); give `WriteHost` the key-disclosure check.
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
  constraint-index staging and the replay-shape predicates; 23c decides
  whether `table.Host` embeds `index.Host`.
- Step 25 (`partition`) imports `index` for the partitioned-index code
  (`index_partition_*.go`), the row history and `index.WriteBatch`.
- Step 29 (`dml`): the `StatementScope` methods left in `unique_dml.go`
  and `index_write_candidate.go` are DML's callers of the write-time
  decisions; after 23d they call `index` behind `index.WriteHost`.
  `insert_write_overlay.go` is ON CONFLICT state and belongs to `dml`.
- `storage.RowByID` (23a) is also used by the DML update and merge code.
