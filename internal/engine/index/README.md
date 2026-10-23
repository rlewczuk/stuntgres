# internal/engine/index

Index records and algorithms: B-tree, hash and GiST keys and roots, unique and
exclusion enforcement records, index publication and snapshots, write-time index
maintenance records, operator-family and access-method DDL, and
partitioned-index propagation.

## Imports

Layer L12 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture)
(see also [ENGINE-SPLIT-index.md](../../../docs/ENGINE-SPLIT-index.md)). It
imports `access`, `analyze` (types and pure functions only), `authorization`,
`catalog`, `ddl`, `exec`, `expr`, `ids`, `lock`, `plan`, `regobj`, `routine`,
`session`, `settings`, `storage`, `stored`, `typedef`, `visibility`, `parser`,
`sqlstate`, `sqltype` and the standard library. It never imports the engine,
`fdw`, or the features above it (`table`, `partition`, `view`, `dml`); they
import it. It never builds a binder.

## What it owns

- **Operator-family and access-method DDL** (`operator_family_*.go`,
  `access_method_ddl.go`): positioned parser items resolve through the shared
  namespace/type/routine/operator authorities, validate the complete ordered
  list privately, then use the ordinary catalog stage and drop closure. Class
  DDL shares this machinery; there is no family-as-class shortcut.
  `catalog.OperatorFamily`, `OperatorFamilyOperator` and `OperatorFamilySupport`
  own metadata; immutable `Read.FamilyOperator`/`Read.FamilySupport` selectors
  are the only member lookup authority. Registered routine identity selects
  runtime operations outside the DDL validator; metadata-only optional callbacks
  are not callable grants. Access-method staging uses current global catalog
  descriptors and genuine registered native handlers; SQL MethodOID is retained
  independently of physical mechanics, and custom methods require their own real
  class/default/family and registered support. Validation and capability
  restrictions dispatch on the physical implementation, while keys keep exact
  SQL identity and diagnostics use the declared method name. TABLE-kind
  definitions are rejected before index operation dispatch. Exclusion staging
  consults live descriptors before applying an unresolved gin/brin spelling
  rule, so registered custom btree methods with those names keep btree
  behavior. Dump deparse quotes custom method names.
- **Keys, roots and programs** (`index.go`, `hash.go`, `hash_capture.go`,
  `gist.go`, `predicate.go`, `expression.go`, `program.go`, `clauses.go`,
  `options.go`, `property.go`, `catalog_key.go`): `Ordering` and
  `BindIndexOrdering`; `BuildIndexRoot`, `BuildIndexEntries`, `MergeIndexRoot`;
  `BindHashIndex`, `BuildHashIndexRoot`, `MergeHashIndexRoot`,
  `PublicationSlot`; `CaptureHashIndexesLocked` over `HashCapture` (the
  published hash roots that exactly cover a plain table query source; the engine
  fills the capture and holds `Engine.mu` across the call); `BindIndexKeys`
  (GiST); `ExecutionPreparation`, `PrepareIndexRecipe`, `NewLogicalIndexRoot`;
  `KeyUsage`, `KeyBaseUsage`, `ExpressionDepends`; `ResolveIndexMethod`
  (captured method resolution and capability validation); the clause and
  option validators (`ValidateIndexInclude`, `ValidateHashKeyDirection`,
  `ValidateIndexOptions`, `ValidateIndexReloptions`, `ValidateReindexOptions`,
  `ReindexControlError`); `Capability`, `ColumnCapability`; `CatalogKey`.
- **Publication, snapshots and ALTER TYPE rebuilds** (`publication.go`,
  `row_publication.go`, `build_transport.go`, `layout_*.go`, `snapshot*.go`,
  `replacement.go`, `remint.go`, `expression_validation.go`, `effective_root.go`,
  `alter_type.go`, `exclusion_type.go`): `PrepareIndexPublication` over
  `RowPublication`, which engine publication fills and `PrepareRowPublication`
  validates and materializes outside `Engine.mu` over the private roots (the
  engine passes its catalog domain); `ValidatePrivateExpressions` (the
  explicit-statement derived-key preflight); index evolution (`EvolveIndex`,
  `EvolveIndexes`, `ApplyIndexEvolution`, `EvolutionInput`) over
  `EvolutionReader` and `EvolutionTarget`; `CaptureLayouts` over
  `LayoutCaptureInput`, `LayoutCapture`, `TransportCapturedIndexLayouts`,
  `TransportEmptyIndexStorage`; `Snapshot` and `CaptureSnapshot` over
  `SnapshotCapture`; `RemintedIndexStorage` and `RemintStorage` over
  `RemintBase`; `EffectiveRoot` (a transaction's private root, else the
  committed one); `AlterIndexBuild`, `ValidateAlterIndexBuilds`,
  `PrepareAlterIndexRoots`, `ColumnTransformation`; `RebindExclusionOperators`.
  The engine fills the capture inputs under `Engine.mu` after its own epoch
  checks and holds the lock across the call.
- **Write-time maintenance records** (`write_candidate.go`, `insertion.go`,
  `unique_binding.go`, `unique_key.go`): `WriteBatch`, `WriteCandidate`,
  `WriteState`, `OutputMatches`, `MaterializeIndexOutput`, `RowMaintainsIndex`;
  `InsertionBinding` and `ProbePurpose`; `UniqueWriteBinding`, `UniqueBoundKey`,
  `ConflictEntryVersion`, `SameRelationRowShape`; `UniqueKeyValues` and
  `UniqueKeyHash`, the opaque key the engine pins in the lock manager.
- **Unique and exclusion enforcement** (`unique.go`, `unique_index.go`,
  `exclusion_*.go`, `constraint.go`): `ValidateUniques`, `ValidateUniqueRoot`,
  `UniqueKeyDetailAllowed` (key disclosure over `access.View` and an
  `access.Environment`; a nil environment means trusted publication),
  `UniqueDiagnostic`, `ValidateUniqueProviders`, `PhysicalNotNullOriginValid`,
  `PhysicalPrimarySupportValid`; the exclusion records (`ExclusionProjection`,
  `ExclusionEntry`, `ExclusionEvent`, `ExclusionModes`, `ExclusionDefinition`),
  `ValidateExclusions`, `ValidateExclusionRoot`; `UniqueBacking`,
  `ExclusionBacking`, `BackingConstraintPresent`. `DisclosureEnv`
  (`disclosure.go`) is the environment ALTER TYPE builds and publication passes.
- **DDL records and presentation** (`control.go`, `metadata.go`,
  `maintenance_grant.go`, `maintenance_plan.go`, `set_constraints.go`,
  `rename.go`, `using_index*.go`, `concurrent.go`, `domain_outputs.go`):
  `DefinitionSQL` (`pg_get_indexdef` and Dump) with `Presentation`,
  `PGIndexFunctionExpression`; `Metadata` (`pg_index`) and `IndexesMetadata`
  (`pg_indexes`) through `ProjectIndexMetadata` and `CatalogMetadata`;
  `MaintenanceGrant` with its grant plan (`MaintenanceNames`, `AddTarget`,
  `CollectGroup`, `NeedsAttach`, `AddAttach`, `FinishPlan`, `SortLockOrder`,
  `ParentMode`, `AlterTarget`), `WrongKind`, `RelationDropOwner`; SET
  CONSTRAINTS name selection (`SelectConstraintName`,
  `SelectInheritedExclusions`); `StageBackingIndexRename` over
  `DescriptorStage`; the `USING INDEX` records; `RootCandidate`,
  `SimpleConcurrentIndex`; `ReownDomainIndexOutputs`.
- **Partitioned indexes** (`partition_attach.go`, `partition_drop.go`,
  `partition_unique.go`, `partition_replay.go`, `row_history.go`): coverage
  (`CoverageReader`, `CoverageComplete`, `PartitionIndexCoverageComplete`),
  ATTACH checks, drop actions, inline-index mapping (`MapPartitionIndex`,
  `PartitionIndexEquivalent`, `PartitionUniqueCoverage`,
  `StagePartitionDescriptor`), the Dump replay predicates (`PartitionReplay*`,
  `PartitionPreservedReplayLocal`, `PartitionHashReplayTarget`,
  `PartitionDumpIndexBacking`), and `PartitionRowHistory`.
- **DDL decisions** (`host.go`, `create.go`, `bind.go`, `maintenance.go`,
  `unique_stage.go`, `exclusion_stage.go` and the staging functions of the files
  above) and **write decisions** (`write_host.go`, `unique_probe.go`,
  `exclusion_visit.go`, `exclusion_shape.go`, `scope.go`): see the two hosts
  below.

## The DDL host

`Host` is what CREATE INDEX, ALTER/DROP INDEX, REINDEX and the constraint-index
staging need for one statement: `ddl.Host` plus `AddDomainBase`, `RemoveObject`,
`TrustedRestoreDestination`, `RelationDiagnostic`, the stage's index slot
(`IndexStage`), the stage itself (`DescriptorStage`), `ACLOutput`,
`DefinitionReader`, `ValidityTarget` (the stage's drop provider) and the two
binding methods `BindIndexExpression` and `BindIndexPredicate`: the engine
builds binders, this package never does. The engine's one staging adapter,
`ddlHost`, implements it (`index_host.go`). Every method runs with `Engine.mu`
held, never takes or releases it, and never waits. Narrower contracts:

- `KeyHost` (`AccessView`, `SearchNamespaces`, `LookupCollation`) for key
  binding and operator class lookup, which read no stage; the analysis host and
  partition key binding pass a stage-less `ddlHost`.
- `DropStage` (`DescriptorStage` plus `StageRead` and `RemoveRelation`,
  implemented by `*CatalogStage`) for DROP INDEX staging, which runs without
  `Engine.mu`; the removal takes it.
- `ValidityTarget` (`CoverageReader` plus `DescriptorCount` and
  `ReplacePartitionDescriptor`, implemented by the engine's drop provider) for
  the partitioned-index validity pass.
- `UsingIndexStage` (`DescriptorStage` and `EmitNotice`) for attaching a
  prepared USING INDEX, which needs no lock; `PrepareUsingIndex` takes only a
  `ddl.Allocator` and runs under `Engine.mu`.

Entry points:

- CREATE INDEX: `PrepareCreate` (Restore authority, limits, relation kind,
  CREATE privilege, method and clauses, predicate and attributes, name, unique
  providers, coverage, identities; `CreatePlan.Skip` for IF NOT EXISTS) and
  `StageCreate` (the descriptor and partition children).
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
  exclusion rebuilds, and prepare the primary key's NOT NULL constraints before
  `PrepareUsingIndex`.
- Partitioned indexes: `StagePartitionIndexChild`,
  `StagePartitionIndexChildren`, `StagePartitionIndexAttach`,
  `PartitionIndexValidity`, `PartitionIndexPromotionPath`,
  `StagePartitionIndexDrop`, `StagePartitionConstraintDrop`.
- `Property` answers `pg_index_has_property` and `pg_index_column_has_property`.

## The write host

`WriteHost` is the engine side of write-time unique and exclusion enforcement:
the `Engine.mu` windows and the lock waits around this package's decisions.
`*statementHost` implements it (`statement_host_index.go`). Each method either
takes `Engine.mu` itself for one window and never waits (the caller must not
hold it), or waits with the caller holding nothing; none takes a callback or an
evaluation context.

- Windows: `UniqueParent`, `BindingAuthority` (stale means rebind; it readmits
  the prepared programs after unlocking), `CaptureUniqueProbe` (`UniqueProbe`:
  parent, index, view, schema, root and, when asked, the head versions),
  `ExistingIndexRoot`, `CaptureExclusionScan` (`ExclusionScan`),
  `ExclusionScanCurrent`, `PublishExclusion` (epoch and state check, row
  identity, the `ExclusionEdit` and install in one window; it takes the
  candidate by value and returns the reserved row, which the caller records with
  `WriteCandidate.RecordExclusion`, so no candidate escapes through the
  interface), `DiscardExclusion` (of the version `WriteCandidate.TakeExclusion`
  clears), `OwnExclusionKeys`, `AdmitExclusion` and `EndExclusionRow`
  (standalone windows; the admit window checks no epoch; `EndExclusionRow`
  projects the end of a row version deleted outside the write stage, for MERGE's
  DELETE arm).
- Waits: `ClaimUniqueKey` (pin, unique claim with the operation's lock policy,
  the `replacementMu` release and re-acquire, program readmission after a wait)
  and `WaitTransaction`.

The probe and scan captures read the write authority in the same window and
return it as `UniqueProbe.Authority` and `ExclusionScan.Authority`, an
`access.View` held by value (the scan's acts as a mutating FK action's owner).
The engine rebuilds the access environment from it only on error paths (key
disclosure) and for the ON CONFLICT binding check.

Pure decisions over captured values:

- `BuildUniqueWriteBinding` binds a parent's unique, exclusion and checked
  insertion indexes; the engine adds the consumed programs and authority.
- `UniqueRootStart` (the committed-root lower bound) and `ExistingRootOutput`
  (a row's output read from a captured root).
- `OwnExclusionEntry`, `VisitExclusion` (the committed-root and pending passes;
  an `ExclusionVisit` with the potential flag, the owner to wait for and the
  first hit) and `ExclusionDeferred`. The visit takes a context source (the
  operation or `dml.Host`) and asks it for its context per comparison.
- Projection edits: `ExclusionEdit.Apply`, and `ExclusionProjection`'s
  `DiscardVersion`, `EndRow`, `Admit` and `ReplaceShape`;
  `ExclusionShapeEntries` projects an ALTER's surviving writes. Each edit works
  on a `ProjectionLocked` copy and replaces slices and maps, so an installed
  projection never changes.
- `Scope.RetainExistingOutput`.

## State slots and registries

- **Registries.** `WaitRegistry` (CONCURRENTLY build waits, read by
  `InspectIndex`) and `ExclusionRegistry` (every open lock owner's dirty
  exclusion projection and their generation). They have no mutex: `Engine.mu`
  guards them, and every method carries the `Locked` suffix because the caller
  holds it. The engine holds one value of each (`Engine.indexWaits`,
  `Engine.exclusions`). Every change to a projection advances the generation
  (install, remove, savepoint restore, reset), so an exclusion scan captured
  with `ScanLocked` fails `CurrentLocked` and is retaken. The maps stay private
  so no caller can change a projection without advancing it.
- **Journal slot.** `Journal` implements `session.IndexSlot`: private unique
  roots, produced candidates and REINDEX intents. `JournalOf` is the checked
  read, `JournalIndexes` allocates on first use and `JournalIndexesOrZero` reads
  a nil slot as zero. `Clone` deep-copies the maps, so savepoint marks keep
  sharing older journals.
- **Savepoint exclusion mark.** `ExclusionMark` (`exclusion_mark.go`) implements
  `session.ExclusionMarkSlot`: the owner's exclusion modes and the pending
  projection a savepoint captured. `ExclusionMarkSlotOf` keeps zero state as a
  nil slot, so most savepoints allocate nothing, and `ExclusionMarkOrZero` reads
  a nil slot as zero.
- **Statement and stage slots.** `Scope` (the statement's index maintenance,
  existing outputs and exclusion modes) and `Stage` (a catalog stage's REINDEX
  intents, candidates, layouts, parent and index-set observations, output
  environment) are the slot records; the accessors on `StatementScope` and
  `CatalogStage` stay in the engine.

## Invariants

- **Immutable roots.** Index roots are immutable ordered entries or equality
  buckets. Writes maintain them at the same Finish/commit frontier as rows, with
  distinct RowID/VersionID membership. Derived keys evaluate at Execute, after
  predicate qualification and before committed or private rows are installed;
  failed batches, waits and builds preserve predecessor rows and roots.
  Maintenance rebuilds selected roots atomically without changing rows or
  definition/version identities.
- **Class identity.** Operator classes resolve through current catalog
  descriptors, exact method OID and real family members; the physical method
  enum is not class identity. Declared class identity is compared with
  `IndexClass.SameIdentity`, not structural equality including captured support
  members, so default-class reconstruction survives TYPE changes while
  nondefault explicit classes still require compatible inputs. Grouping
  compatibility is captured once in the ordering's key mask, never resolved per
  row. Compatible CIDR exclusion keys use the class's physical INET input
  identity, never a fabricated CIDR family row.
- **Family members.** Required family members keep their insertion-time
  INTERNAL class owner. The common drop closure rejects standalone deletion of
  those members and redirects implementation dependencies through the class,
  discovering sibling members and dependent indexes; optional/cross-type and
  classless members stay loose.
- **Exclusion events.** Deferral events enlist only actual potential conflicts.
  Savepoints retain immutable event/mode/projection snapshots; repairing or
  removing the actual row/version retires its event. Transient modes and events
  are never serialized.
- **Partitioned exclusion providers.** Each local declaration covers every
  partition key of its target and descendants with actual equality, matching
  canonical operator inputs, stable attributes and effective collation; INCLUDE
  columns and expressions do not provide coverage. Independent child-local
  constraints are scoped locally. Virtual (partitioned) indexes own metadata
  only; leaf providers enforce all-key conflicts. CREATE and EXCLUDE ADD/DROP
  reuse staged recursive index production.

## What stays in the engine

The orchestration: the `Engine.mu` windows, the CONCURRENTLY phases and their
waits, lock admission (`acquireIndexMaintenance`: name resolution under
`Engine.mu` and the acquire loop with its recheck, over the `index` grant plan),
unique key pins, the registry instances, the savepoint capture and restore
windows (which store and read an `ExclusionMark`), the Finish order, and
publication and Restore storage (`prepareIndexCatalog`,
`catalog_overlay_indexes.go`), with the capture windows of
`captureIndexSnapshotParents`, `prepareRemintedStorage`, `captureIndexLayouts`,
`validatePrivateIndexExpressions` and `captureHashIndexesLocked`.
`executeCreateIndex` and `executeIndexMaintenance` stay as orchestration: the
retry loop and lock, authorization and identity capture, the stage lifecycle,
RENAME, the unlocked DROP path with its dependency recording, validation, the
partitioned-index transaction overlay, publication and the conflict recheck. So
do `executePartitionConstraintDropLocked`, `partitionIndexTransaction`,
`stageDomainIndexGenerationsLocked`, SET CONSTRAINTS (the namespace lookup, the
mode candidate and the event checks), `InspectIndex`, the `IndexProperty`
snapshot read, and thin wrappers that build a `ddlHost` for production callers
(`lookupIndexClass`, `stageUsingIndex`, `prepareUsingIndexMetadata`,
`stagePartitionIndexChildren`, `stagePartitionIndexValidity`,
`stagePartitionConstraintDrop`); tests call the `index` functions directly with
a `ddlHost`. The write-time loops are `dml`'s and run over this host through
`dml.Host`: the unique probe and claim (`dml.ProbeUniqueRow`, `ClaimUniqueRow`,
`CheckUniqueRow`, `BindUniqueWrite`, `ExistingUniqueOutput`) and the exclusion
probe and admission (`dml.ProbeExclusion`). They interleave ON CONFLICT
arbiters, the insert write overlay, own writes and deletes and foreign action
owners. The ALTER shape window (`prepareExclusionShape`),
`projectExclusionDeletionLocked`, savepoint restore and the publication resets
stay engine callers of the registry primitives.

## Testing

Package units cover the column capability matrix, `EffectiveRoot`, the
savepoint exclusion mark, access-method properties and operator-family
validation phases. Most index tests build an engine and stay in
`internal/engine`; run concurrency, exclusion and unique-claim selections with
`RACE=1`. Private units assert actual support IDs/routines and root membership,
never ordinary ORDER BY results as evidence of index use. Public SQL lives in
[reference/indexes](../../../reference/indexes/README.md#testing). Add
`REFERENCE=1` to reference selectors for the oracle.

**Frame names matched by tests.** `TestHashIndexMergeCancellation` matches
`.MergeHashIndexRoot` and `TestSerializablePublicationRealChangedPredecessorRetry`
matches `.MergeIndexRoot`. Rename those functions only together with the
fixtures.

```sh
make check RUN='^Test(Index|EffectiveRoot)'   # keys, clauses, properties, maintenance, hosts
make check RACE=1 RUN='^Test(Unique|Exclusion|UsingIndex|ConcurrentIndex|HashIndex)'   # enforcement
```

Operator families, classes and access methods:

```sh
make check RUN='^Test(OperatorFamily|OperatorClass|AccessMethod|BuiltinRoutineResultInventory)'
make check RACE=1 RUN='^TestOperatorFamilyPublication$'
make check RUN='^TestReference(OperatorFamily|OperatorClass|AccessMethod)'
```
