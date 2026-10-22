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
`authorization`, `catalog`, `ddl`, `dump`, `exec`, `expr`, `extension`,
`functions`,
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
admission and DML expansion goes to `dml`; the pure query leaf walk and
pending-grant decision they call live here.

## What it owns

- **ALTER TABLE records** (`alter_*.go`, `rename.go`, `query_table_create.go`):
  `AddedColumn`, `AlterCookingError`, `OrdinaryColumnDrop` (over
  `DropDependencyReader`), `DroppedConstraint`, `PreparedAddedChecks`,
  `VerifyAlterPrimaryPrerequisites`, `AlterTypePreparation`,
  `AlterTypeColumn`, `AlterTypeIndex`, the ALTER TYPE check rebinding
  (`AlterTypeCheck`, `CaptureAlterTypeChecks`, `AlterRowValidator`),
  `AlterActionPhase` (the nine-pass order), `AlterTableLockMode`,
  `AlterTableLookupKind`, `AlterTablePreparation`, `AlterForeignKeyAdmission`,
  `ForeignKeyCreation` (the statement's CREATE TABLE REFERENCES targets and
  ALTER TABLE ADD FOREIGN KEY captures; the root keeps it in a
  `StatementScope` slot),
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
  helpers `InheritanceQueryClosure`, `CaptureInheritanceQueryPrefix`,
  `SourceOnly`, `InheritanceQueryLeaves` (the stored-view-aware leaf walk)
  and `InheritancePendingGrants` (24g: which physical leaves still need a
  grant and in which row-mark mode; the root fills their storage and
  waits). `inheritance_relationship.go` owns compatible ordinary attach
  and detach candidates; the root owns exact endpoint grants/admission and the
  existing single ALTER stage. Parent candidates read that stage's complete
  catalog view, including unchanged base descriptors, not its staged-only Get
  map. Capture observes every lexical endpoint, including absence, without
  emitting later lookup/owner errors. Each misc action consumes its own semantic
  lookup and parent grant before compatibility. Unreached captures are not final
  publication subjects. Live observation ignores the recipe-definition reader.
  Post-wait admission compares the original parent before refreshing it; only
  genuine first-child marker publication is allowed. An unstaged parent's actual
  admitted marker pointer can be consumed into the stage, never reminted or
  substituted for staged own changes; Stage.Base is not rebased.
  The same endpoint fence runs under the Engine mutex at actual implicit
  publication and explicit private-journal installation, not just stage
  construction; its authorization capture precedes the Session mutex.
  Attach's typed/partition preparation restrictions and detach's actual-partition
  execution restriction are distinct. Relationship-only detach reaches its
  missing-edge diagnostic without requiring storage; unsupported mixed physical
  partition ALTER remains behind its existing capability boundary.
  `InheritanceParent` stores identity plus a live
  sparse ordinal. Detached retired origins retain archival identity without a
  live parent provider; strict restore carriers pair that history with fresh
  facts. TYPE cleanup uses checked physical-NN historical causes and does not
  invent keys or constraint rows.
  Detach uses the admitted current parent identity plus ReferenceEpochValid for
  still-attached retained TEMP certificates; it neither requires whole old/new
  reference equality nor reconnects a detached archival source.
  Recursive DROP's RESTRICT primary distinguishes actual physical column
  deletions from origin decrements: a local or other-parent contribution can
  preserve the child's column, leaving a singular root deletion diagnostic.
  CHECK attachment and CHECK-origin validation share a CHECK-only cooked
  deparsed-definition comparator. `SameInheritedExpression` remains structural,
  normalizing only retained row-reference epochs; it never substitutes rendered
  SQL for differing type or external-provider authority. CREATE merges and
  default/generated recipe provenance keep their existing structural proofs.
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
  `RenameInheritanceConstraint[Precheck]`. The prepare side (24g;
  `inheritance_alter_prepare.go`, `inheritance_prepare_host.go`):
  `PrepareInheritanceNode` cooks phases 0–6 of one node before the sole
  stage exists, through the six-method `PrepareHost` (USING INDEX metadata,
  ADD COLUMN, column, identity and constraint-index drops, added CHECKs),
  and records `InheritancePreparedNode`/`InheritancePreparedAction`. Prepared
  drops cross as `PreparedDrop` (a `DefinitionDelta` with `Next`); the
  root's `inheritancePrepareHost` hands each call's definition reader to the
  authority and unwraps drops for the stage consumers. Callers hold
  nothing; the host methods take `Engine.mu` and the drops may wait.
  Over `DropDependencyReader`:
  `DropConstraintIndexes`, `OwnedSequences` and `DropIdentity`.
  The same plan and engine stage serve the narrow partition CHECK/NOT NULL
  policy (`partition_alter.go`, `partition_alter_check.go`). Membership remains
  partition topology; `AlterGraph` is statement-owned constraint scratch only.
  Root CHECKs are cooked once and remapped to descendant row identities, with
  structural merge checks and per-action stop events. Final descriptors carry
  exact `PartitionOrigin`, never synthetic ordinary parents. NOT NULL keeps
  column identity, operation-specific naming and local survival; ONLY removal
  localizes immediate child contributions without changing grandchildren.
- **Other table DDL cores** (pure, no stage): `DropTargets` (DROP TABLE
  resolution, kinds and ownership over `Resolver`), the SET SCHEMA cores
  `SetSchemaOwned`, `SetSchemaGroup`, `SetSchemaCandidates` and
  `SetSchemaConflict` (`set_schema.go`), `QueryTableColumns` (CREATE TABLE
  AS column derivation), `TypedObservationValid` (`typed.go`).
- **Relation validation** (24e; `relation_validation.go`):
  `RelationProvider`, the catalog provider of relation descriptors. The
  three checks owned by packages above `table` come in as
  `RelationValidators` slots, called at fixed points in this order:
  `Policies` after the column checks, `Partition` after the CHECK-kind
  check, `Definition` as the view and materialized-view tail. The root
  builds one `relationProvider` value with `view.ValidatePolicies`,
  `partition.ValidateDescriptor` and `view.ValidateViewDefinition`.
- **Generation remap** (24e; `generated_remap.go`):
  `RecomputeRemappedGenerations` recomputes the stored generated cells that
  read their owner's table OID after an OID remap, with NOT NULL and CHECK
  rechecks. Its CHECK snapshot capture is a callback, called with no lock
  held; the root's `remapCheckCapture` takes `Engine.mu` for it.
- **Tablespace-only ALTER helpers** (24e; `alter_tablespace.go`):
  `TablespaceOnlyActions`, `ValidateAlterTablespaceMove`; the bulk
  `ALTER TABLE ALL IN TABLESPACE` handler stays in the root.
- **Remaining CREATE and ALTER decisions** (24f): `BindChecks`
  (`check_bind.go`), the CHECK naming, inherited merge, duplicate
  diagnostics, ordinals and dependency rendering of CREATE and ADD
  CONSTRAINT, binding each expression through `Host.BindCheckExpression`;
  `CheckCreateShape` (`create_shape.go`), the CREATE TABLE shape errors in
  order; `PlanAlterScan` (`alter_scan.go`), whether ALTER TABLE scans old
  rows and with which validator; `AlterTableOf`, `ResolveTypedSource` and
  `TypedSources` (`typed_alter.go`), the ALTER TABLE [NOT] OF checks over a
  small `TypedResolver` and the OID-ordered OF types a typed-table change
  locks.
- **ALTER row rewrite and scan** (24h): `PlanAddedColumns`
  (`alter_add_column.go`), the ADD COLUMN DEFAULT preparation and one-time
  fills; `RewriteTypeRows` (`alter_rewrite.go`), the TYPE, added-column and
  generation rewrite of the effective live heads into a private
  replacement; `ValidateAddedConstraintRows` (`alter_constraint_rows.go`),
  the CHECK and NOT NULL scan of old rows. The caller passes the statement's
  `exec.Context` template (the routine authority's catalog for the fills
  and the rewrite, none for the scan) and the private write and delete
  roots; the functions take no lock, and expressions may reach host methods
  that take `Engine.mu`, so callers hold nothing.
- **Target admission** (24f; `target_admission.go`): `Target` is how a
  statement admits its target relation (name, lock mode, owner, rename,
  IF EXISTS, table-rename and view-replacement flags), decided from syntax.
  Each owning package exports a `TargetEntry` (`TargetAdmission` here, also
  for CREATE INDEX and ALTER OWNER, whose packages sit below `table`;
  `view.TargetAdmission`, `dml.TargetAdmission`); the root registers them
  once (`relationTargetEntries`) and `AdmitTarget` asks them in order. Each
  statement kind belongs to one entry, so the order cannot change a result.
- **Explicit locks** (B7; `explicit_lock.go`): `ExplicitLockMode` maps a
  LOCK TABLE mode to its lock, and `CheckExplicitLock` checks the relation
  kind, then the rights of the captured `access.View` through its
  `access.Environment` (any privilege PostgreSQL accepts for the mode), then
  the partition-hierarchy boundary. The root keeps the per-target lock loop.
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
`BindColumnDefaults`, `BindGenerations` and `BindCheckExpression`,
`OperationDefinition` (the operation's definition reader, which CHECK
rendering reads) and `LiveEpoch`. The root `ddlHost`
implements it (`table_host.go`). `Resolver` is its stage-less part, used by
`DropTargets` before DROP TABLE begins a stage; `RestoreResolver`
(`ResolveRestoredRelation`) is the stage-less part `RetiredConstraintCandidate`
resolves retired parents through. `AlterHost` adds what one
ALTER statement's bindings need: the live `Input` (captures mutate it, so
callers read it again before each binding), the snapshot
captures (`CaptureTypeDiagnosticName`, `CaptureCommentSnapshot`,
`CaptureCheckSnapshots`), the binders `BindDefault`, `BindGeneration`,
`RebindCheck` and `BindAlterTypeUsing`, `TypeChangeDependents` and
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
persistence and namespace checks, stage lifecycle, the typed-source waits,
providers, overlay or publication), `schemaTableLocked` (a forwarder that
builds the host), `dropTableLocked`, `alterSchemaLocked` and
`prepareQueryTableLocked` around their cores, `schemaPartitionTableLocked`
(partition 25d), `alterTableLocked` and its pass loop, the candidate
functions that stage or wait (`addColumnCandidate`, `alterTypeCandidate`,
`dropColumnCandidate`, ...), the row evaluation template
(`alterRowEvaluation`) and projection roots, `Engine.mu` windows,
lock admission (`acquireDefinitionProviders`, `admitInheritanceAlter`,
`acquireInheritanceDefinitions`), the identity sequence waits and staging
(`stageAlterIdentityTypes`, `alterIdentityTypeSequence`,
`refreshIdentitySequenceLocked`), the prepare host
(`inheritancePrepareHost`) and the prepared-node consumers
(`consumeInheritancePreparedAction`, the `alterTableLocked` passes),
prepared drops (`preparedDrop`, `preparedIdentityDrop`), and Finish. The candidate wrappers
(`addColumnCandidate`, `alterTypeCandidate`, `generationCandidate`,
`prepareAddedChecks`, `rebindAlterTypeChecks`, `rebindAlterTypeIndexes`,
`stageAddedUnique`, `stageAddedForeignKey`) take `Engine.mu` across the
`table` call and build the host; nothing inside waits.
Also in the root: the binder bodies behind the host (`bindCreateDefaults`,
`bindCreateGenerations`, `ddlHost.BindCheckExpression`), the
acquire loop of `acquireRelationTarget` around `AdmitTarget`,
the `projectColumnShape` wrapper that supplies the statement's roots to
`session.ProjectColumnShapeRoots` (moved with the storage projections in
catalog 5k) and the inheritance query scope methods (query admission:
`acquireInheritanceSources` keeps its loop, waits and storage lookup
around `InheritancePendingGrants`). Restore keeps its hook bodies in the
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
`TestTableHost(BindColumnDefaultsMatchesCreateTable|StageChangedReportsStagedDescriptor)`,
`TestAlterHostInputReflectsCheckSnapshotCapture`, the validator slots
`TestRelationValidatorsOrder` and the remap capture
`TestGeneratedRemapCheckCapture`. The 24f decisions have package-local
tests: `TestTypedSources`, `TestPlanAlterScan`, `TestCheckCreateShape`,
`TestTableTargetAdmission` and `TestAdmitTargetAsksEachEntry`. The 24g
prepare side and query grants have package-local tests over a fake host
and a small catalog: `TestPrepareInheritanceNode(PhaseOrder|StopsAtFailedStep)`
and `TestInheritancePendingGrants`. The 24h scan has
`TestValidateAddedConstraintRows`; the rewrite and fill tests need an
engine fixture and stay in the root (`TestAlterAddColumn*`,
`TestAlterGenerated*`).

```sh
make check RUN='^TestOwnedSequenceName|^TestTableHost|^TestAlterHost|^TestRelationValidatorsOrder$|^TestGeneratedRemapCheckCapture$'
make check RUN='^Test(TypedSources|PlanAlterScan|CheckCreateShape|TableTargetAdmission|AdmitTargetAsksEachEntry)$'
make check RUN='^Test(PrepareInheritanceNode|InheritancePendingGrants|ValidateAddedConstraintRows)'
make check RUN='^Test(AlterAdd|AlterGenerated|AlterTablespace|AlterType|AlterDefault|IdentityAlter|ConstraintRename|InheritanceAlter|InheritanceRetired|InheritanceMetadata)'
make check RUN='^Test(NotNull|Inheritance|PhysicalNotNull|RestoreInherited|TableLike|Typed|AlterDropColumn|GeneratedVirtual|Check(Native|Descriptor|Completed|Review))'
make check RUN='^Test(Serial|Identity|SchemaTable|DDLHostStageSchemaTable|QueryTableCreate|TemporaryTable)'
make check RACE=1 RUN='^Test(NotNullAXWaitCancellationAndEnd$|InheritanceAdmission|InheritanceAlterAdmission)'
make check RUN='^Test(AttributeRankRestore|RestoreConstraintHistory|RestoreNotNullNoInherit|RestoreInherited|InheritanceForeignKeyForwardReplay|PhysicalNotNull|InheritanceRetiredLocalCheckReconstruction)'
```

Public behavior is covered by the dual-mode reference suite:

```sh
make check RUN='^TestReference(TableCore|TableLike|Like|NotNull|Check|Default|GeneratedStored|GeneratedVirtual|ForeignKey|ForeignActions|Inheritance|ConstraintRename|TypedTable|Identity|Serial|Truncate|DumpRound)'
make check RUN='^TestReferenceAlter(AddColumn|AddConstraint|Default|DropColumn|Generated|Identity|Nullability|Rename|Schema|Tablespace|Type)'
```
