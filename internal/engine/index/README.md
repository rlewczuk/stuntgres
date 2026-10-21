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
  `build_transport.go`, `layout_transport.go`, `snapshot.go`,
  `snapshot_repair.go`, `replacement.go`, `alter_type.go`,
  `exclusion_type.go`): `PrepareIndexPublication` over `RowPublication`
  (which engine publication fills); index evolution (`EvolveIndex`,
  `EvolveIndexes`, `ApplyIndexEvolution`, `EvolutionInput`) over
  `EvolutionReader` and `EvolutionTarget`; `LayoutCapture`,
  `TransportCapturedIndexLayouts`, `TransportEmptyIndexStorage`;
  `Snapshot`; `RemintedIndexStorage`; `AlterIndexBuild`,
  `ValidateAlterIndexBuilds`, `PrepareAlterIndexRoots`,
  `ColumnTransformation`; `RebindExclusionOperators`.
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
  `maintenance_grant.go`, `rename.go`, `using_index.go`,
  `using_index_prepare.go`, `concurrent.go`, `domain_outputs.go`):
  `DefinitionSQL` (`pg_get_indexdef` and Dump) with `Presentation`,
  `PGIndexFunctionExpression`; `Metadata` (`pg_index`) and
  `IndexesMetadata` (`pg_indexes`) through `ProjectIndexMetadata` and
  `CatalogMetadata`; `MaintenanceGrant`, `WrongKind`, `RelationDropOwner`;
  `StageBackingIndexRename`, `StagePreparedUsingIndex` over
  `DescriptorStage`; the `USING INDEX` records; `RootCandidate`,
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
their waits, lock admission (`acquireIndexMaintenance`), unique key pins,
the registry instances, savepoint marks (`exclusionMark`), the Finish
order, and publication and Restore storage (`prepareIndexCatalog`,
`catalog_overlay_indexes.go`). The handlers on `Engine` (CREATE INDEX,
ALTER/DROP INDEX, REINDEX, the constraint-index staging, partitioned-index
child propagation, `InspectIndex`) and the write-time enforcement on
`StatementScope` (unique probe and claim, the exclusion probe and
projection) stay until 23d moves their decisions behind `index.Host` and
`index.WriteHost`. `exclusionScan` and `uniqueConflict` keep their
`AuthorizationView` (one allocation per probe otherwise; 23d settles them).

## Testing

Package tests: `go test ./internal/engine/index` (the column capability
matrix). Most index tests build an engine and stay in the root package:

```sh
make check RUN='^Test(Index|Unique|Exclusion|UsingIndex|ConcurrentIndex|HashIndex|PartitionDetach(Unique|UsingIndex)Replay|PartitionAttachIndex)'
make check RACE=1 RUN='^Test(ConcurrentIndex|Exclusion|UniqueClaim|HashIndexMergeCancellation)'
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
