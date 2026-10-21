# internal/engine/table

Table records and decisions that need no engine: column and constraint
records, NOT NULL (logical and physical), CHECK, DEFAULT and generated
columns, foreign-key definitions and the FK probe, ALTER TABLE records,
the table dump record (`DumpRelation`) with its emitters and Restore
parsers, and inheritance. Step 24 of the engine split created it (24b
moved the clean set). It sits above `index` and below `partition`, `view`,
`dml` and `control` in
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture):
it imports `access`, `analyze` (types and pure functions only),
`authorization`, `catalog`, `ddl`, `dump`, `exec`, `expr`, `functions`,
`ids`, `index`, `lock`, `pgcatalog`, `plan`, `regobj`, `sequence`,
`session`, `storage`, `stored`, `typedef`, `visibility`, `parser`,
`sqlstate`, `sqltype` and the standard library. It never imports the
engine, `partition`, `view`, `dml` or `control`; they import it. It never
builds a binder. See
[ENGINE-SPLIT-table.md](../../../docs/ENGINE-SPLIT-table.md).

## Why inheritance is here

Inheritance and table DDL use each other in both directions: ALTER TABLE
on an inheritance tree re-enters the ALTER handler once per node, and the
dump record has emitters for both. Go cannot split one type's methods
across packages, so one package holds both. Inheritance *query and DML
expansion* is not DDL: the query scope methods stay with root query
admission and DML expansion goes to `dml`.

## What it owns

- **ALTER TABLE records** (`alter_*.go`, `rename.go`, `query_table_create.go`):
  `AddedColumn`, `AlterCookingError`, `OrdinaryColumnDrop` (over
  `DropDependencyReader`), `DroppedConstraint`, `PreparedAddedChecks`,
  `VerifyAlterPrimaryPrerequisites`, `AlterTypePreparation`,
  `AlterTypeColumn`, `AlterTypeIndex`, the ALTER TYPE check rebinding
  (`AlterTypeCheck`, `CaptureAlterTypeChecks`, `AlterRowValidator`),
  `AlterActionPhase` (the nine-pass order), `AlterTableLockMode`,
  `AlterTableLookupKind`, `AlterTablePreparation`, `AlterForeignKeyAdmission`,
  `RenameColumnCandidate`, `RenameLocalConstraint`,
  `ValidateQueryTableOutputType`.
- **NOT NULL** (`not_null_*.go`, `physical_not_null*.go`): `NotNullCandidate`
  with `NotNullRecursion`, `NameCreateNotNull`, `UsingIndexNotNullCandidate`;
  physical NOT NULL transitions (`PhysicalNotNullFromType`,
  `TransitionPhysicalNotNull`, `PrunePhysicalNotNull`,
  `ValidatePhysicalNotNull`), the competing-unique and self-FK emitters, and
  the Restore repair parsers (`RestorePhysicalNotNullSyntax`,
  `RestoreNoInheritSyntax`) next to the emitters whose output they read.
- **CHECK, DEFAULT and generated columns** (`check.go`, `default*.go`,
  `generated*.go`, `domain_dependencies.go`, `row_constraints.go`):
  `OrderedChecks`, `ValidateChecks`, `ValidateCheckRow`,
  `PhysicalRowViolation` (the first NOT NULL or CHECK violation of a
  physical row, rendered by `RowViolation.Diagnostic` with the caller's
  detail), `NotNullDetailError`, the CHECK and DEFAULT dependency
  passes (`CheckDependents`, `DefaultDependents`, over
  `DropDependencyReader`), `DefaultReplayDeferrable`,
  `EvaluateGenerations`, `PrepareGenerationProgram`, `GenerationDepends`,
  the virtual-generation rules (`VirtualDestination`,
  `AdmitColumnVirtualGeneration`; index uses are `index.RejectVirtualUses`),
  and
  `DomainDependency`.
- **Foreign keys** (`foreign_key*.go`): declarations and comparison binding
  (`ForeignKeyDeclarations`, `BindForeignKeyComparison`,
  `ValidateForeignKeys`), the diagnostics over `access.View`
  (`ForeignKeyDiagnostic`, `ForeignKeyReferencesAllowed`), the dependency
  pass (`ForeignKeyDependents`), the probe (`FindForeignKeyParent`,
  `PrepareForeignKeyChild`, `ProbeForeignKeyIndex`, `ForeignKeyRows`), and
  Restore (`RestoredForeignKeys`, whose authority is an opaque
  `ddl.Authority`).
- **Dump and Restore** (`dump.go`, `constraint_dump.go`, `*_restore*.go`,
  `domain_default_restore.go`): `DumpRelation` and all its emitters,
  `RestoredNotNull`, `RestoreAttributeRankSyntax`,
  `RestoreAlterConstraints`, the retired-constraint and inherited
  recipe/owner/FK repair records, and `PhysicalNotNullDumpDependencies`
  (`physical_not_null_dump_graph.go`), the dump graph edges of moved
  physical NOT NULL providers over a `dump.Resolver`.
- **Restore repair cores** (24d.3; `*_restore*.go`,
  `inheritance_recipe_restore.go`, `inheritance_retired_remap.go`,
  `default_replay.go`): the decisions inside the root Restore hooks, over a
  `catalog.Read` or `access.View`. Whole: the captures
  (`CaptureRestoredPlaceholders`, `CaptureRestoredNotNulls`,
  `CaptureHistoricalFacts`, `CapturePhysicalImportIndex`),
  `RetiredArchiveHighWater` and `RemapRetiredConstraints`, and
  `ValidateForeignKeyData`. Split, with no stage: a shape check before
  authorization (`RestoredGenerationShape`, `InheritedOwnerShape`,
  `PhysicalSelfForeignKeyShape`, ...) and a decision after resolution
  (`PairRestoredGeneration`, `InheritedCheckRecipe`, `PairRestoredNotNull`,
  `CaptureInheritedOwner`, `CaptureInheritedForeignKey`,
  `PreparePhysicalNotNullConstructor`, `CapturePhysicalNotNullConstructor`,
  `CapturePhysicalForeignKeyProvider`, `ValidateRestoredDefault`, ...).
  Around their own stage: `NoInheritNotNullCandidate`,
  `AttributeRankCandidate`, `PhysicalNotNullCleanup` and
  `RetiredConstraintCandidate` return the next relation (nil for no
  change), and the caller applies `Advance` or `CommitRetiredRepair` after
  publication. The receipts `RestoredPhysicalNotNull` and
  `RestoredPhysicalForeignKeyProvider` hold a role record, not an
  authorization view. Root-only validators come in as arguments
  (`catalog.Provider`, `*ids.Domain`).
- **Inheritance** (`inheritance_*.go`, `typed_inheritance.go`): the recursion
  plan record `InheritanceAlterPlan` (the root holder
  `inheritanceAlterState` adds the stage, journal and prepared nodes),
  `InheritanceAlterOrder`, `InheritanceOnlyActions`,
  `InheritanceRecursiveAction`, the events (`InheritanceAlterEvent`,
  `InheritanceParentActions`), the guards, `ValidateInheritance`, retired
  constraints (`InheritRetiredColumnConstraints`, `RetireRebuilt*`),
  `RemapInheritance`, `InheritanceDefinitionReader` (it overlays prepared
  drops through `DefinitionDelta`, which the root's prepared column and
  identity drops implement), the typed-table propagation
  (`TypedClosure`, `TypedInheritedActions`), and the query-expansion
  helpers `InheritanceQueryClosure`, `CaptureInheritanceQueryPrefix` and
  `SourceOnly`.
- **CREATE TABLE decisions** (24d.1; `create.go`, `inheritance_create.go`,
  `like.go`, `like_index.go`, `identity.go`, `serial.go`,
  `foreign_key_stage.go`), all over `Host`: `StageCreateTable` (the body of
  CREATE TABLE and of the CREATE SCHEMA table element, in its fixed order:
  descriptor, row type, inheritance, NOT NULL names, identity and serial
  sequences, LIKE columns, defaults, generations, CHECKs, LIKE checks,
  constraint OIDs, UNIQUE, EXCLUDE, LIKE indexes, FOREIGN KEYs),
  `StageInheritance`, `StageLikeColumns`, `StageLikeChecks`,
  `StageLikeIndexes`, `StageCreateIdentities`, `StageIdentitySequence`,
  `StageCreateSerials`, `StageForeignKey`, `StageCreateForeignKeys` and
  `CreateCheckSyntax`; `NewRelation` and `AssignDefinitionOIDs` over a
  `ddl.Allocator` (typedef's composite relations use `NewRelation` too).
- **ALTER TABLE decisions** (24d.2), over `AlterHost`: the candidates
  `AddColumnCandidate`, `GenerationCandidate`, `AlterTypeCandidate` with
  `AdmitAlterType`, `CompleteAlterType` and `PrepareAlterType`,
  `PrepareAlterTableActions` with `AlterTableActionKind`,
  `PrepareAddedChecks` (it takes the nil-safe recursion plan),
  `RebindAlterTypeChecks`, `RebindAlterTypeIndexes`, `CookAlterDefault`;
  over `Host`: `StageAddedUnique`, `StageAddedForeignKey`,
  `AddIdentityCandidate`; over `ddl.Host` or `ddl.Stage`: the post-loop
  rebuilds `RebuildAlterNotNulls`, `RebuildAlterForeignKeys`,
  `RebindPhysicalNotNullType`; pure: `CaptureAlterTypeIndexes`,
  `PrepareIdentitySet`/`FinishIdentitySet` (around the root's sequence
  wait), `AlterDefaultColumn`, `AlterDefaultCandidate`,
  `RemapPhysicalNotNull` (over a `catalog.Read`), `RenameConstraintKind`,
  `StageRenameConstraint` (over an `index.DescriptorStage`) and the loop
  pieces `OnlySetNotNull`, `AddedCheckSyntax`, `StoredReplacements`,
  `DependencyTargets` (`alter_table_loop.go`). The inheritance recursion
  decisions are host-less: `PlanInheritanceAlter`, `InheritedAlterActions`,
  `FinishInheritanceAlterCandidate`, the nil-safe plan methods
  `RecordAction`, `RecordChecks`, `NotNullMode` and `OnlyCreatedConstraint`
  (`inheritance_alter_record.go`), `InheritanceDefaultCandidate` and the
  per-member rename cores `RenameInheritanceColumn[Precheck]` and
  `RenameInheritanceConstraint[Precheck]`. Over `DropDependencyReader`:
  `DropConstraintIndexes`, `OwnedSequences` and `DropIdentity`.
- **Other table DDL cores** (pure, no stage): `DropTargets` (DROP TABLE
  resolution, kinds and ownership over `Resolver`), the SET SCHEMA cores
  `SetSchemaOwned`, `SetSchemaGroup`, `SetSchemaCandidates` and
  `SetSchemaConflict` (`set_schema.go`), `QueryTableColumns` (CREATE TABLE
  AS column derivation), `TypedObservationValid` (`typed.go`).
- **Stage slot** (`stage.go`, `like.go`): `Stage` holds CREATE … LIKE
  sources, recorded by `CaptureLikeSource` and checked at publication by
  `ValidateLikeSources`; the `CatalogStage` accessors stay in the engine.

## Hosts

`Host` (`host.go`) is what CREATE TABLE, the CREATE SCHEMA table element,
PARTITION OF children, ADD IDENTITY and FOREIGN KEY staging need: `index.Host`
(and through it `ddl.Host`), `ddl.TypeStaging`, row types, column type and
collation resolution, the table and sequence stage slots
(`TableStage`, `SequenceStage`, `SequenceColumns`, `NameBinding`,
`StageChanged`), `StagedTemporary`, relation resolution (`Resolver`,
`RestoreResolver`), `DescribeCreateTable` and the binders
`BindColumnDefaults`, `BindGenerations` and `BindChecks`. The root `ddlHost`
implements it (`table_host.go`). `Resolver` is its stage-less part, used by
`DropTargets` before DROP TABLE begins a stage; `RestoreResolver`
(`ResolveRestoredRelation`) is the stage-less part `RetiredConstraintCandidate`
resolves retired parents through. `AlterHost` adds what one
ALTER statement's bindings need: the live `Input` (captures mutate it, so
callers read it again before each binding), `LiveEpoch`, the snapshot
captures (`CaptureTypeDiagnosticName`, `CaptureCommentSnapshot`,
`CaptureCheckSnapshots`), the binders `BindDefault`, `BindGeneration`,
`BindAddedChecks`, `RebindCheck` and `BindAlterTypeUsing`,
`OperationDefinition`, `TypeChangeDependents` and
`StageIdentitySequenceOptions`. The root `alterHost{ddlHost; scope}`
(`alter_host.go`) implements it; the root builds one per call because the
ALTER loop begins its stage lazily, so the stage may be nil. Every method runs with
`Engine.mu` held, never takes or releases it, and never waits; the
authority is an opaque `ddl.Authority`, so code here re-reads
`AccessView` after every `Recapture`.

Names settled in 24b: every moved name is capitalized; `tableStage` is
`Stage`. Helpers only the package uses keep their lower-case names. Index
23d.1 moved `IncompatibleNotValid` and `RejectVirtualIndexUses` down into
`index` (`index.IncompatibleNotValid`, `index.RejectVirtualUses`).

## What stays in the engine

The orchestration of CREATE and ALTER TABLE: `createTableLocked` (lock,
persistence and namespace checks, stage lifecycle, typed sources,
providers, overlay or publication), `schemaTableLocked` (a forwarder that
builds the host), `dropTableLocked`, `alterSchemaLocked` and
`prepareQueryTableLocked` around their cores, `schemaPartitionTableLocked`
(partition 25d), `alterTableLocked` and its pass loop, the candidate
functions that stage or wait (`addColumnCandidate`, `alterTypeCandidate`,
`dropColumnCandidate`, ...), row rewrites and validation
(`rewriteTypeRows`, `validateAddedConstraintRows`), `Engine.mu` windows,
lock admission (`acquireDefinitionProviders`, `admitInheritanceAlter`,
`acquireInheritanceDefinitions`), the identity sequence waits and staging
(`stageAlterIdentityTypes`, `alterIdentityTypeSequence`,
`refreshIdentitySequenceLocked`), the inheritance prepare side
(`prepareInheritanceNode`, `inheritancePreparedNode`,
`inheritancePreparedAction`), prepared drops (`preparedDrop`,
`preparedIdentityDrop`), and Finish. The candidate wrappers
(`addColumnCandidate`, `alterTypeCandidate`, `generationCandidate`,
`prepareAddedChecks`, `rebindAlterTypeChecks`, `rebindAlterTypeIndexes`,
`stageAddedUnique`, `stageAddedForeignKey`) take `Engine.mu` across the
`table` call and build the host; nothing inside waits.
Also in the root: the binder bodies behind the host (`bindCreateDefaults`,
`bindCreateGenerations`, `bindColumnChecks`),
`projectColumnShapeRoots`, `inheritanceQueryLeaves` and the inheritance query
scope methods (query admission). Restore keeps its hook bodies in the
root (control reaches them): the context check, authorization, the
superuser 42501, the `Engine.mu` window, the trust checks
(`partitionConstraintReplayImport`, the captured roles, the attribute-rank
authority), name resolution, and the shared stage tail
`publishRestoreRepairLocked`; also the pending foreign-key binder
(`captureRestoredForeignKeys`, `resolveRestoredForeignKeyTargets`,
`bindRestoredForeignKeys`, `validateRestoredInheritedForeignKey`) and
`restore_replay.go`. FK write-time
enforcement and FK actions (`foreignKeyJob`, `foreign_action*.go`) and
`mutation_targets.go` go to `dml`; the materialized snapshot codec is in `view`.
24c refined the hosts: `table.Host` (on the root `ddlHost`) and
`table.AlterHost` (on a scope-bound root `alterHost`), every method under
`Engine.mu` and never waiting; 24d.1 moved the CREATE decisions, 24d.2 the
ALTER and inheritance decisions and 24d.3 the Restore cores (see
[ENGINE-SPLIT-table.md](../../../docs/ENGINE-SPLIT-table.md) Part B). The
removal interface stays table-local: `DropDependencyReader` is passed in,
and its callers hold nothing; its comment marks the methods that wait
(`Acquire`, `CaptureIdentitySequence`, the dependency passes) or take
`Engine.mu` (`Remove`, `ApplyViewDependents`).

## Invariants

- ALTER TABLE keeps its nine-pass order (`AlterActionPhase`), the
  inheritance pass order and per-node re-entry, so the same first error is
  reported for every action list.
- Dump output is byte-identical: `DumpRelation` emitters keep their order
  and never iterate maps into output; each Restore syntax parser stays next
  to the emitter whose output it reads.
- The package holds no engine state and no mutex. Functions here never
  wait; callers in the root own `Engine.mu` and lock waits.

## Testing

Package-local tests cover pure helpers (`TestOwnedSequenceNameTruncatesToValidUTF8`).
Every other table unit test builds an engine and stays in the root package,
naming the moved code as `table.X`; the host seams have
`TestTableHost(BindColumnDefaultsMatchesCreateTable|StageChangedReportsStagedDescriptor)`
and `TestAlterHostInputReflectsCheckSnapshotCapture`.

```sh
make check RUN='^TestOwnedSequenceName|^TestTableHost|^TestAlterHost'
make check RUN='^Test(AlterAdd|AlterGenerated|AlterTablespace|AlterType|AlterDefault|IdentityAlter|ConstraintRename|InheritanceAlter|InheritanceRetired|InheritanceMetadata)'
make check RUN='^Test(NotNull|Inheritance|PhysicalNotNull|RestoreInherited|TableLike|Typed|AlterDropColumn|GeneratedVirtual|Check(Native|Descriptor|Completed|Review))'
make check RUN='^Test(Serial|Identity|SchemaTable|DDLHostStageSchemaTable|QueryTableCreate|TemporaryTable)'
make check RACE=1 RUN='^TestNotNullAXWaitCancellationAndEnd$'
make check RUN='^Test(AttributeRankRestore|RestoreConstraintHistory|RestoreNotNullNoInherit|RestoreInherited|InheritanceForeignKeyForwardReplay|PhysicalNotNull|InheritanceRetiredLocalCheckReconstruction)'
```

Public behavior is covered by the dual-mode reference suite:

```sh
make check RUN='^TestReference(TableCore|TableLike|Like|NotNull|Check|Default|GeneratedStored|GeneratedVirtual|ForeignKey|ForeignActions|Inheritance|ConstraintRename|TypedTable|Identity|Serial|Truncate|DumpRound)'
make check RUN='^TestReferenceAlter(AddColumn|AddConstraint|Default|DropColumn|Generated|Identity|Nullability|Rename|Schema|Tablespace|Type)'
```
