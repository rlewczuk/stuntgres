# internal/engine/index

Index records and algorithms: B-tree, hash and GiST keys and roots,
unique and exclusion enforcement records, index publication and snapshots,
write-time index maintenance records, and partitioned-index propagation.
Step 23 of the engine split created it (23b moved the clean set). The
package sits at layer L12 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture):
it imports `access`, `analyze` (types and pure functions only),
`authorization`, `catalog`, `ddl`, `exec`, `expr`, `ids`, `lock`, `plan`,
`regobj`, `routine`, `session`, `settings`, `storage`, `stored`, `typedef`,
`visibility`, `parser`, `sqlstate`, `sqltype` and the standard library.
It never imports the engine, `fdw`, or the features above it (`table`,
`partition`, `view`, `dml`); they import it. It never builds a binder.
See [ENGINE-SPLIT-index.md](../../../docs/ENGINE-SPLIT-index.md).

## What it owns

- **Operator-family DDL** (`operator_family_ddl.go`,
  `operator_family_members.go`, `operator_family_validation.go`,
  `operator_family_dump.go`): positioned parser items resolve through the shared
  namespace/type/routine/operator authorities, validate the complete ordered
  list privately, then use the ordinary catalog stage and drop closure.
  Genuine class DDL shares this machinery; no family-as-class shortcut is provided.
  `catalog.OperatorFamily`, `OperatorFamilyOperator` and `OperatorFamilySupport` own metadata; immutable
  `Read.FamilyOperator`/`Read.FamilySupport` selectors are the only member lookup
  authority. Registered routine identity selects runtime operations outside
  the DDL validator; metadata-only optional callbacks are not callable grants.
  Access-method utility staging (`access_method_ddl.go`) uses current global
  catalog descriptors and genuine registered native handlers. SQL MethodOID is
  retained independently of physical mechanics. Custom methods require their own
  real class/default/family and actual registered support. Family/member validation
  dispatches on physical implementation while all keys retain exact SQL identity.
  TABLE-kind definitions are rejected before index operation dispatch. Method
  property inquiries use the captured descriptor's physical authority; dump
  deparse quotes custom method names.
  Capability restrictions use the physical implementation but diagnostics use
  the exact declared method name, including hash/GiST ordering boundaries.
  Exclusion staging consults live descriptors before applying an unresolved
  gin/brin spelling boundary; registered custom btree methods with those names
  retain genuine btree class/support and exclusion behavior.

- **Keys, roots and programs** (`index.go`, `hash.go`, `hash_capture.go`,
  `gist.go`, `predicate.go`, `expression.go`, `program.go`, `clauses.go`,
  `options.go`, `property.go`, `catalog_key.go`): `Ordering` and
  `BindIndexOrdering`; `BuildIndexRoot`, `BuildIndexEntries`,
  `MergeIndexRoot`; `BindHashIndex`, `BuildHashIndexRoot`,
  `MergeHashIndexRoot`, `PublicationSlot`; `CaptureHashIndexesLocked` over
  `HashCapture` (the published hash roots that exactly cover a plain table
  query source; the engine fills the capture and holds `Engine.mu` across
  the call); `BindIndexKeys` (GiST);
  `ExecutionPreparation`, `PrepareIndexRecipe`, `NewLogicalIndexRoot`;
  `KeyUsage`, `KeyBaseUsage`, `ExpressionDepends`; `ResolveIndexMethod`
  (captured method resolution and capability validation); the clause and option
  validators (`ValidateIndexInclude`,
  `ValidateHashKeyDirection`, `ValidateIndexOptions`,
  `ValidateIndexReloptions`, `ValidateReindexOptions`,
  `ReindexControlError`); `Capability`, `ColumnCapability`; `CatalogKey`.
  Declared class identity is compared with `IndexClass.SameIdentity`, not
  structural equality including captured support members. Default-class
  reconstruction and presentation therefore retain their original behavior
  across TYPE changes; nondefault explicit classes still require compatible
  inputs. Compatible CIDR exclusion keys use the class's physical INET input
  identity, never a fabricated CIDR family row; other operator eligibility
  checks retain their declared SQL input semantics.
- **Publication, snapshots and ALTER TYPE rebuilds** (`publication.go`,
  `row_publication.go`, `build_transport.go`, `layout_transport.go`,
  `layout_capture.go`, `snapshot.go`, `snapshot_capture.go`,
  `snapshot_repair.go`, `replacement.go`, `remint.go`,
  `expression_validation.go`, `effective_root.go`, `alter_type.go`,
  `exclusion_type.go`):
  `PrepareIndexPublication` over `RowPublication`, which engine publication
  fills and `PrepareRowPublication` validates and materializes outside
  `Engine.mu` over the private roots (the engine passes its catalog
  domain); `ValidatePrivateExpressions` (the explicit-statement derived-key
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
names. 23f moved `prepareRowPublication` as `PrepareRowPublication`, the
body of `captureHashIndexesLocked` as `CaptureHashIndexesLocked`, and
`exclusionMark`, `exclusionMarkSlotOf` and `exclusionMarkOrZero` as
`ExclusionMark`, `ExclusionMarkSlotOf` and `ExclusionMarkOrZero`.

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
- **Savepoint exclusion mark.** `ExclusionMark` (`exclusion_mark.go`)
  implements `session.ExclusionMarkSlot`: the owner's exclusion modes and
  the pending projection a savepoint captured. `ExclusionMarkSlotOf` keeps
  exactly zero state a nil slot, so most savepoints allocate nothing, and
  `ExclusionMarkOrZero` reads a nil slot as zero.
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
the registry instances, the savepoint capture and restore windows (which
store and read an `ExclusionMark`), the Finish order, and publication and
Restore storage (`prepareIndexCatalog`, `catalog_overlay_indexes.go`), with
the capture windows of `captureIndexSnapshotParents`,
`prepareRemintedStorage`, `captureIndexLayouts`,
`validatePrivateIndexExpressions` and `captureHashIndexesLocked`.
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

Operator classes now resolve through current catalog descriptors, exact method
OID and real family members, for default/explicit binding, TYPE reconstruction,
UNIQUE/conflict/FK/partition/exclusion and native controls. Physical method enum
dispatch is not class identity; grouping compatibility is captured once in the
ordering's key mask, not resolved per row. Deparse follows current class names
and namespaces. See [design](../../../docs/design/PG18-INDEX-OPCLASS.md).

Required family members retain their insertion-time INTERNAL class owner.
The common drop closure rejects standalone deletion of those members and
redirects implementation dependencies through the class, discovering sibling
members and dependent indexes. Optional/cross-type and classless members stay
loose. `TestOperatorClassDependencyClosure` checks this descriptor graph without
duplicating the public SQL lifecycle scenario.

```sh
make check RUN='^TestOperatorClass(MethodIdentity|SupportExecution|CatalogAtomicity|DependencyClosure)$'
make check RACE=1 RUN='^TestOperatorClass(MethodIdentity|SupportExecution|CatalogAtomicity|DependencyClosure)$'
make check RUN='^TestReferenceOperatorClassFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceOperatorClassFeatureSmoke$'
```

Expected smoke: real custom family/class, real index over existing rows, then
maintained INSERT; qualifying rows `(1,2),(4,2)` become `(1,2),(4,2),(6,3)`.
Private units assert actual support IDs/routine and root membership, rather than
treating ordinary ORDER BY results as evidence of a btree query optimizer.


Package tests: `go test ./internal/engine/index` (the column capability
matrix, `EffectiveRoot` and the savepoint exclusion mark). Most index tests
build an engine and stay in the root package:

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

Operator-family selected proof (run each reference selector in native mode and
with `REFERENCE=1`; root owns generation and verification):
CREATE resolves namespace/schema CREATE before method and superuser checks.
Member ADD/DROP first validates the whole SQL list and its primary duplicates;
only then do operator-before-procedure storage/deletion checks run, followed by
private staging and one publication. FUNCTION lookup precedes explicit associated
types; ORDER BY resolves its btree sort family before method capability checks.
Associated-type counts are rejected after resolving at most the first two types.
Family DROP keeps strict method resolution before missing-ok object lookup:
an absent explicit schema produces the schema skip NOTICE under `IF EXISTS`,
while an existing schema's absent family retains the family skip NOTICE.
Schema USAGE denial is never skipped. ALTER and DROP share positionless
object-address namespace lookup; other namespace callers keep their own context.
Support-function identity lookup also has no expression position. An absent
explicit operator namespace yields typed operator-not-found `42883`, while
family/function namespace absence retains `3F000`; other errors are not masked.

```sh
make check RUN='^TestReferenceOperatorFamily(Definitions|Methods|Diagnostics|SecondaryUniqueness)$'
make check RUN='^TestReferenceOperatorFamily(Metadata|BuiltinMetadata|Dependencies|PinnedDependencies)$'
make check RUN='^TestReferenceOperatorFamily(Atomicity|Authority|MetadataInvalidation|Notices|TransactionBoundaries)$'
make check RUN='^TestReferenceOperatorFamily(Roundtrip|SeedRoundtrip|Smoke|SupportRoutines)$'
make check RUN='^TestOperatorFamily(Catalog|Validation|ProviderInventory|SupportExecution)$'
make check RUN='^TestOperatorFamilyPublication$' RACE=1
make check RUN='^TestReferenceOperatorFamilyReview(LookupMetadata|ListPhases|ResolutionOrder|CreateAuthorityOrder)$'
make check RUN='^TestReferenceOperatorFamilyReviewMissingSchema$'
make check RUN='^TestOperatorFamily(MemberPhases|AssociationResolution)$'
make check RUN='^TestBuiltinRoutineResultInventory$'
```

Smoke independently expects five integer strategies97/523/96/525/521 and support
routine351, DROP/reADD new member identity on the SAME held SELECT, atomic mixed
list failure and surviving builtin btree/hash indexes. It never creates a class
or installs an unrelated INTERNAL callback into a live builtin class.

SecondaryUniqueness checks direct/prepared stored and same-list conflicts against
the released `pg_amop_opr_fam_index` diagnostic, including relational family OID,
all exposed error fields, original-row identity and failed-list atomicity.
BuiltinMetadata asserts complete implemented-family/member bags; its class
association query retains the existing 63-class capability boundary, excluding
PostgreSQL's unimplemented btree `cidr_ops` (10009), not any family members.
Publication constructs its relation, rows and index root through ordinary staged
publication before testing canceled/invalid changes and immutable builtin roots.

`TestOperatorFamilyMacaddrSupportExecution` additionally binds the existing
class10024 through family1984/member10074/routine836, checks the independently
observed unsigned comparison boundary, and maintains real duplicate/NULL roots
across insertion and deletion while retaining the old root:

```sh
make check RUN='^TestOperatorFamily(MacaddrSupportExecution|ProviderInventory|Catalog)$'
make check RUN='^TestReferenceOperatorFamilyBuiltinMetadata$'
make check RUN='^TestReferenceOperatorFamilyBuiltinMetadata$' REFERENCE=1
```
