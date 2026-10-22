# internal/engine/index

Index records and algorithms: B-tree, hash and GiST keys and roots,
unique and exclusion enforcement records, index publication and snapshots,
write-time index maintenance records, and partitioned-index propagation.
Step 23 of the engine split created it (23b moved the clean set). The
package sits at layer L12 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture):
it imports `access`, `analyze` (types and pure functions only),
`authorization`, `catalog`, `ddl`, `exec`, `expr`, `ids`, `lock`, `plan`,
`regobj`, `session`, `settings`, `storage`, `stored`, `typedef`,
`visibility`, `parser`, `sqlstate`, `sqltype` and the standard library.
It never imports the engine, `fdw`, or the features above it (`table`,
`partition`, `view`, `dml`); they import it. It never builds a binder.
See [ENGINE-SPLIT-index.md](../../../docs/ENGINE-SPLIT-index.md).

## What it owns

- **Keys, roots and programs** (`index.go`, `hash.go`, `gist.go`,
  `predicate.go`, `expression.go`, `program.go`, `clauses.go`,
  `options.go`, `property.go`, `catalog_key.go`): `Ordering` and
  `BindIndexOrdering`; `BuildIndexRoot`, `BuildIndexEntries`,
  `MergeIndexRoot`; `BindHashIndex`, `BuildHashIndexRoot`,
  `MergeHashIndexRoot`, `PublicationSlot`; `BindIndexKeys` (GiST);
  `ExecutionPreparation`, `PrepareIndexRecipe`, `NewLogicalIndexRoot`;
  `KeyUsage`, `KeyBaseUsage`, `ExpressionDepends`; the clause and option
  validators (`ValidateIndexMethod`, `ValidateIndexInclude`,
  `ValidateHashKeyDirection`, `ValidateIndexOptions`,
  `ValidateIndexReloptions`, `ValidateReindexOptions`,
  `ReindexControlError`); `Capability`, `ColumnCapability`; `CatalogKey`.
- **Publication, snapshots and ALTER TYPE rebuilds** (`publication.go`,
  `build_transport.go`, `layout_transport.go`, `layout_capture.go`,
  `snapshot.go`, `snapshot_capture.go`, `snapshot_repair.go`,
  `replacement.go`, `remint.go`, `expression_validation.go`,
  `effective_root.go`, `alter_type.go`, `exclusion_type.go`):
  `PrepareIndexPublication` over `RowPublication` (which engine publication
  fills); `ValidatePrivateExpressions` (the explicit-statement derived-key
  preflight); index evolution (`EvolveIndex`, `EvolveIndexes`,
  `ApplyIndexEvolution`, `EvolutionInput`) over `EvolutionReader` and
  `EvolutionTarget`; `CaptureLayouts` over `LayoutCaptureInput`,
  `LayoutCapture`, `TransportCapturedIndexLayouts`,
  `TransportEmptyIndexStorage`; `Snapshot` and `CaptureSnapshot` over
  `SnapshotCapture`; `RemintedIndexStorage` and `RemintStorage` over
  `RemintBase`; `EffectiveRoot` (a transaction's private root, else the
  committed one); `AlterIndexBuild`, `ValidateAlterIndexBuilds`,
  `PrepareAlterIndexRoots`, `ColumnTransformation`;
  `RebindExclusionOperators`. The engine fills the capture inputs under
  `Engine.mu` after its own epoch checks and holds the lock across the
  call.
- **Write-time maintenance records** (`write_candidate.go`,
  `insertion.go`, `unique_binding.go`, `unique_key.go`): `WriteBatch`,
  `WriteCandidate`, `WriteState`, `OutputMatches`,
  `MaterializeIndexOutput`, `RowMaintainsIndex`; `InsertionBinding` and
  `ProbePurpose`; `UniqueWriteBinding`, `UniqueBoundKey`,
  `ConflictEntryVersion`, `SameRelationRowShape`; `UniqueKeyValues` and
  `UniqueKeyHash`, the opaque key the engine pins in the lock manager.
- **Unique and exclusion enforcement** (`unique.go`, `unique_index.go`,
  `exclusion_projection.go`, `exclusion_definition.go`,
  `exclusion_publication.go`, `constraint.go`): `ValidateUniques`,
  `ValidateUniqueRoot`, `UniqueKeyDetailAllowed` (key disclosure over
  `access.View` and an `access.Environment`; a nil environment means
  trusted publication), `UniqueDiagnostic`, `ValidateUniqueProviders`,
  `PhysicalNotNullOriginValid`, `PhysicalPrimarySupportValid`; the
  exclusion records (`ExclusionProjection`, `ExclusionEntry`,
  `ExclusionEvent`, `ExclusionModes`, `ExclusionDefinition`),
  `ValidateExclusions`, `ValidateExclusionRoot`; `UniqueBacking`,
  `ExclusionBacking`, `BackingConstraintPresent`. `DisclosureEnv`
  (`disclosure.go`) is the environment ALTER TYPE builds and publication
  pass.
- **DDL records and presentation** (`control.go`, `metadata.go`,
  `maintenance_grant.go`, `maintenance_plan.go`, `set_constraints.go`,
  `rename.go`, `using_index.go`,
  `using_index_prepare.go`, `concurrent.go`, `domain_outputs.go`):
  `DefinitionSQL` (`pg_get_indexdef` and Dump) with `Presentation`,
  `PGIndexFunctionExpression`; `Metadata` (`pg_index`) and
  `IndexesMetadata` (`pg_indexes`) through `ProjectIndexMetadata` and
  `CatalogMetadata`; `MaintenanceGrant` with its grant plan
  (`MaintenanceNames`, `AddTarget`, `CollectGroup`, `NeedsAttach`,
  `AddAttach`, `FinishPlan`, `SortLockOrder`, `ParentMode`, `AlterTarget`),
  `WrongKind`, `RelationDropOwner`; SET CONSTRAINTS name selection
  (`SelectConstraintName`, `SelectInheritedExclusions`);
  `StageBackingIndexRename` over `DescriptorStage`; the `USING INDEX`
  records; `RootCandidate`,
  `SimpleConcurrentIndex`; `ReownDomainIndexOutputs`.
- **Partitioned indexes** (`partition_attach.go`, `partition_drop.go`,
  `partition_unique.go`, `partition_replay.go`, `row_history.go`):
  coverage (`CoverageReader`, `CoverageComplete`,
  `PartitionIndexCoverageComplete`), ATTACH checks, drop actions,
  inline-index mapping (`MapPartitionIndex`, `PartitionIndexEquivalent`,
  `PartitionUniqueCoverage`, `StagePartitionDescriptor`), the Dump replay
  predicates (`PartitionReplay*`, `PartitionPreservedReplayLocal`,
  `PartitionHashReplayTarget`, `PartitionDumpIndexBacking`), and
  `PartitionRowHistory`.
- **DDL decisions** (23d.1; `host.go`, `create.go`, `bind.go`,
  `maintenance.go`, `unique_stage.go`, `exclusion_stage.go`, and the
  staging functions in `alter_type.go`, `exclusion_type.go`,
  `using_index.go`, `using_index_prepare.go`, `partition_unique.go`,
  `partition_attach.go`, `partition_drop.go`, `property.go`): see
  "The DDL host" below.
- **Write decisions** (23d.2; `write_host.go`, `unique_probe.go`,
  `exclusion_visit.go`, `exclusion_shape.go`, and additions to
  `unique_binding.go`, `exclusion_projection.go`, `scope.go`): see
  "The write host" below.

Names settled in 23b: types and functions lost a leading `index` prefix
(`indexOrdering` is `Ordering`, `indexJournal` is `Journal`,
`indexJournalOf` is `JournalOf`, `indexScope` is `Scope`, `indexStage` is
`Stage`, `indexSnapshot` is `Snapshot`, `indexWaitRegistry` is
`WaitRegistry`, `indexDefinitionSQL` is `DefinitionSQL`, `indexMetadata`
is `Metadata`, `indexCoverageReader` is `CoverageReader`); every other
name is capitalized (`exclusionRegistry` is `ExclusionRegistry`,
`rowPublication` is `RowPublication`, `journalIndexes` is
`JournalIndexes`). Helpers only the package uses keep their lower-case
names.

## The DDL host

`Host` is what CREATE INDEX, ALTER/DROP INDEX, REINDEX and the
constraint-index staging need for one statement: `ddl.Host` plus
`AddDomainBase`, `RemoveObject`, `TrustedRestoreDestination`,
`RelationDiagnostic`, the stage's index slot (`IndexStage`), the stage
itself (`DescriptorStage`), `ACLOutput`, `DefinitionReader`,
`ValidityTarget` (the stage's drop provider) and the two binding methods
`BindIndexExpression` and `BindIndexPredicate`: the engine builds binders,
this package never does. The engine's one staging adapter, `ddlHost`,
implements it (`index_host.go`). Every method runs with `Engine.mu` held,
never takes or releases it, and never waits. Narrower contracts:

- `KeyHost` (`AccessView`, `SearchNamespaces`, `LookupCollation`) for key
  binding and operator class lookup, which read no stage; the analysis
  host and partition key binding pass a stage-less `ddlHost`.
- `DropStage` (`DescriptorStage` plus `StageRead` and `RemoveRelation`,
  implemented by `*CatalogStage`) for DROP INDEX staging, which runs
  without `Engine.mu`; the removal takes it.
- `ValidityTarget` (`CoverageReader` plus `DescriptorCount` and
  `ReplacePartitionDescriptor`, implemented by the engine's drop provider)
  for the partitioned-index validity pass.
- `UsingIndexStage` (`DescriptorStage` and `EmitNotice`) for attaching a
  prepared USING INDEX, which needs no lock; `PrepareUsingIndex` takes only
  a `ddl.Allocator` and runs under `Engine.mu`.

Entry points:

- CREATE INDEX: `PrepareCreate` (Restore authority, limits, relation kind,
  CREATE privilege, method and clauses, predicate and attributes, name,
  unique providers, coverage, identities; `CreatePlan.Skip` for IF NOT
  EXISTS) and `StageCreate` (the descriptor and partition children).
- ALTER INDEX ATTACH PARTITION and REINDEX: `StageMaintenance`;
  `ValidateMaintenance` checks the stage's observations at validation.
- Binding: `LookupClass`, `BindKey`, `BindAttributes`, `MethodOf`,
  `RejectVirtualUses`.
- Constraint indexes, called by table code: `StageUniqueDefinitions`,
  `StageCreateUniques`, `StageExclusion`, `StageCreateExclusions`,
  `RebuildAlterUniques`, `RebuildAlterExclusions` (ALTER COLUMN TYPE),
  `CaptureUsingIndexes`, `PrepareUsingIndex`, `StageUsingIndex`,
  `IncompatibleNotValid`. The callers own the table edges: they record LIKE
  sources before unique staging, rebuild NOT NULL between the unique and
  exclusion rebuilds, and prepare the primary key's NOT NULL constraints
  before `PrepareUsingIndex`.
- Partitioned indexes: `StagePartitionIndexChild`,
  `StagePartitionIndexChildren`, `StagePartitionIndexAttach`,
  `PartitionIndexValidity`, `PartitionIndexPromotionPath`,
  `StagePartitionIndexDrop`, `StagePartitionConstraintDrop`.
- `Property` answers `pg_index_has_property` and
  `pg_index_column_has_property`.

Names settled in 23d.1 (engine name → `index` name):
`executeCreateIndex`'s decisions → `PrepareCreate`/`StageCreate`;
`executeIndexMaintenance`'s staging → `StageMaintenance`;
`CatalogStage.validateIndexMaintenance` → `ValidateMaintenance` (the method
stays as a forwarder); `lookupIndexClass` → `LookupClass`; `bindIndexKey`
→ `BindKey`; `validateSystemIndexKey` → `validateSystemKey`;
`bindIndexAttributes` → `BindAttributes`; `stageUniqueDefinitions`,
`stageCreateUniques`, `stageExclusion`, `stageCreateExclusions` →
`StageUniqueDefinitions`, `StageCreateUniques`, `StageExclusion`,
`StageCreateExclusions`; `rebuildAlterUniquesLocked`,
`rebuildAlterExclusionsLocked` → `RebuildAlterUniques`,
`RebuildAlterExclusions`; `captureUsingIndexes` → `CaptureUsingIndexes`;
`prepareUsingIndexMetadata` → `PrepareUsingIndex`;
`StagePreparedUsingIndex` → `StageUsingIndex`; `stagePartitionIndex*` and
`stagePartitionConstraintDrop` → `StagePartitionIndex*` and
`StagePartitionConstraintDrop`; `dropProvider.partitionIndexValidity` →
`PartitionIndexValidity`; `rejectVirtualIndexUses` → `RejectVirtualUses`;
`incompatibleNotValid` → `IncompatibleNotValid`; the `IndexProperty`
decision → `Property`. `generatedRelationName` went to `ddl` as
`GeneratedRelationName`.

## The write host

`WriteHost` is the engine side of write-time unique and exclusion
enforcement: the `Engine.mu` windows and the lock waits around this
package's decisions. `*statementHost` implements it
(`statement_host_index.go`). Each method either takes `Engine.mu` itself
for one window and never waits (the caller must not hold it), or waits
with the caller holding nothing; none takes a callback or an evaluation
context.

- Windows: `UniqueParent`, `BindingAuthority` (stale means rebind; it
  readmits the prepared programs after unlocking), `CaptureUniqueProbe`
  (`UniqueProbe`: parent, index, view, schema, root and, when asked, the
  head versions), `ExistingIndexRoot`, `CaptureExclusionScan`
  (`ExclusionScan`), `ExclusionScanCurrent`, `PublishExclusion` (epoch and
  state check, row identity, the `ExclusionEdit` and install in one
  window; it takes the candidate by value and returns the reserved row,
  which the caller records with `WriteCandidate.RecordExclusion`, so no
  candidate escapes through the interface), `DiscardExclusion` (of the
  version `WriteCandidate.TakeExclusion` clears), `OwnExclusionKeys`, `AdmitExclusion` and
  `EndExclusionRow` (standalone windows; the admit window checks no epoch,
  as before; `EndExclusionRow` projects the end of a row version deleted
  outside the write stage, for MERGE's DELETE arm).
- Waits: `ClaimUniqueKey` (pin, unique claim with the operation's lock
  policy, the `replacementMu` release and re-acquire, program readmission
  after a wait) and `WaitTransaction`.

The probe and scan captures read the write authority in the same window
and return it as `UniqueProbe.Authority` and `ExclusionScan.Authority`, an
`access.View` held by value (the scan's acts as a mutating FK action's
owner). The engine rebuilds the access environment from it only on error
paths (key disclosure) and for the ON CONFLICT binding check.

Pure decisions over captured values:

- `BuildUniqueWriteBinding` binds a parent's unique, exclusion and checked
  insertion indexes; the engine adds the consumed programs and authority.
- `UniqueRootStart` (the committed-root lower bound) and
  `ExistingRootOutput` (a row's output read from a captured root).
- `OwnExclusionEntry`, `VisitExclusion` (the committed-root and pending
  passes; an `ExclusionVisit` with the potential flag, the owner to wait
  for and the first hit) and `ExclusionDeferred`. The visit takes a context
  source (the operation or `dml.Host`) and asks it for its context per
  comparison.
- Projection edits: `ExclusionEdit.Apply`, and `ExclusionProjection`'s
  `DiscardVersion`, `EndRow`, `Admit` and `ReplaceShape`;
  `ExclusionShapeEntries` projects an ALTER's surviving writes. Each edit
  works on a `ProjectionLocked` copy and replaces slices and maps, so an
  installed projection never changes.
- `Scope.RetainExistingOutput`.

Names settled in 23d.2 (engine name → `index` name): `uniqueParent` →
`UniqueParent`; `acquireUnique` → `ClaimUniqueKey`; `waitCompletion` →
`WaitTransaction`; `discardExclusionCandidate` → `DiscardExclusion`;
`exclusionScan`'s plain fields → `ExclusionScan`;
`exclusionScanCurrent` → `ExclusionScanCurrent`;
`retainExistingIndexOutput` → `Scope.RetainExistingOutput`.

## State slots and registries

- **Registries.** `WaitRegistry` (CONCURRENTLY build waits, read by
  `InspectIndex`) and `ExclusionRegistry` (every open lock owner's dirty
  exclusion projection and their generation). They have no mutex:
  `Engine.mu` guards them, and every method carries the `Locked` suffix
  because the caller holds it. The engine holds one value of each
  (`Engine.indexWaits`, `Engine.exclusions`). Every change to a projection
  advances the generation (install, remove, savepoint restore, reset), so
  an exclusion scan captured with `ScanLocked` fails `CurrentLocked` and
  is retaken. The maps stay private so no caller can change a projection
  without advancing it.
- **Journal slot.** `Journal` implements `session.IndexSlot`: private
  unique roots, produced candidates and REINDEX intents. `JournalOf` is the
  checked read, `JournalIndexes` allocates on first use and
  `JournalIndexesOrZero` reads a nil slot as zero. `Clone` deep-copies the
  maps, so savepoint marks keep sharing older journals.
- **Statement and stage slots.** `Scope` (the statement's index
  maintenance, existing outputs and exclusion modes) and `Stage` (a catalog
  stage's REINDEX intents, candidates, layouts, parent and index-set
  observations, output environment) are the slot records; the accessors on
  `StatementScope` and `CatalogStage` stay in the engine.

## What stays in the engine

The orchestration: the `Engine.mu` windows, the CONCURRENTLY phases and
their waits, lock admission (`acquireIndexMaintenance`: name resolution
under `Engine.mu` and the acquire loop with its recheck, over the `index`
grant plan), unique key pins,
the registry instances, savepoint marks (`exclusionMark`), the Finish
order, and publication and Restore storage (`prepareIndexCatalog`,
`catalog_overlay_indexes.go`), with the capture windows of
`captureIndexSnapshotParents`, `prepareRemintedStorage`,
`captureIndexLayouts` and `validatePrivateIndexExpressions`.
`executeCreateIndex` and `executeIndexMaintenance` stay as orchestration: the retry loop and lock,
authorization and identity capture, the stage lifecycle, RENAME, the
unlocked DROP path with its dependency recording, validation, the
partitioned-index transaction overlay, publication and the conflict
recheck. So do `executePartitionConstraintDropLocked`,
`partitionIndexTransaction`, `stageDomainIndexGenerationsLocked`, SET
CONSTRAINTS (the namespace lookup, the mode candidate and the event
checks), `InspectIndex`, the `IndexProperty` snapshot read, and thin
wrappers that build a `ddlHost` for production callers outside the moved
set (`lookupIndexClass`, `stageUsingIndex`, `prepareUsingIndexMetadata`,
`stagePartitionIndexChildren`, `stagePartitionIndexValidity`,
`stagePartitionConstraintDrop`). Tests call the `index` functions
directly with a `ddlHost`; 23e deleted the wrappers that only they used. The write-time
loops are `dml`'s (29d.2) and run over this host through `dml.Host`: the
unique probe and claim (`dml.ProbeUniqueRow`, `ClaimUniqueRow`,
`CheckUniqueRow`, `BindUniqueWrite`, `ExistingUniqueOutput`) and the
exclusion probe and admission (`dml.ProbeExclusion`). They interleave ON
CONFLICT arbiters, the insert write overlay, own writes and deletes and
foreign action owners. The ALTER shape window (`prepareExclusionShape`),
`projectExclusionDeletionLocked`, savepoint restore and the publication
resets stay engine callers of the registry primitives.

## Testing

Package tests: `go test ./internal/engine/index` (the column capability
matrix and `EffectiveRoot`). Most index tests build an engine and stay
in the root package:

```sh
make check RUN='^Test(Index|Unique|Exclusion|UsingIndex|ConcurrentIndex|HashIndex|EffectiveRoot|PartitionDetach(Unique|UsingIndex)Replay|PartitionAttachIndex|PartitionConstraint|PrimaryKey)'
make check RACE=1 RUN='^Test(ConcurrentIndex|Exclusion|UniqueClaim|HashIndexMergeCancellation|IndexWriteHost)'
```

Two fixtures find index code by stack-frame name:
`TestHashIndexMergeCancellation` matches `.MergeHashIndexRoot` and
`TestSerializablePublicationRealChangedPredecessorRetry` matches
`.MergeIndexRoot`. Rename those functions only together with the fixtures.

Public behavior is covered by the dual-mode reference suite:

```sh
make check RUN='^TestReference(BtreeIndex|HashIndex|PartialIndex|UniqueIndex|ConcurrentIndex|IndexMaintenance|IndexClauses|IndexProperties|ExpressionIndex|UsingIndex|Exclusion|InsertConflict|UniqueRepeatable|UniqueType)'
make check RUN='^TestReference(AlterAddConstraint|PartitionConstraints|RangePartition|HashPartition|DumpRoundTrip)'
```

Run the same selections with `REFERENCE=1` against PostgreSQL 18.
