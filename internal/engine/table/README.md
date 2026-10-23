# internal/engine/table

Table records and decisions that need no engine: column and constraint records,
NOT NULL (logical and physical), CHECK, DEFAULT and generated columns,
relation options, foreign-key definitions and the FK probe, ALTER TABLE
records, the table dump record (`DumpRelation`) with its emitters and Restore
parsers, and inheritance. FK write-time enforcement and FK actions live in
[`dml`](../dml/README.md); the materialized snapshot codec is in `view`.

## Imports

It sits above `index` and below `partition`, `view`, `dml` and `control` in
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture) (see
also [ENGINE-SPLIT-table.md](../../../docs/ENGINE-SPLIT-table.md)). It imports
`access`, `analyze` (types and pure functions only), `authorization`, `catalog`,
`ddl`, `dump`, `exec`, `expr`, `extension`, `functions`, `ids`, `index`, `lock`,
`pgcatalog`, `plan`, `regobj`, `routine`, `sequence`, `session`, `settings`,
`storage`, `stored`, `typedef`, `visibility`, `parser`, `sqlstate`, `sqltype` and
the standard library. It never imports the engine, `partition`, `view`, `dml` or
`control`; they import it. It never builds a binder.

## Why inheritance is here

Inheritance and table DDL use each other in both directions: ALTER TABLE on an
inheritance tree re-enters the ALTER handler once per node, and the dump record
has emitters for both. Go cannot split one type's methods across packages, so
one package holds both. Inheritance *query and DML expansion* is not DDL: the
query scope methods stay with root query admission and DML expansion is in
`dml`; the pure query leaf walk and pending-grant decision they call live here.

## What it owns

- **ALTER TABLE records** (`alter_*.go`, `rename.go`, `query_table_create.go`):
  `AddedColumn`, `AlterCookingError`, `OrdinaryColumnDrop` (over
  `DropDependencyReader`), `DroppedConstraint`, `PreparedAddedChecks`,
  `VerifyAlterPrimaryPrerequisites`, `AlterTypePreparation`, `AlterTypeColumn`,
  `AlterTypeIndex`, the ALTER TYPE check rebinding (`AlterTypeCheck`,
  `CaptureAlterTypeChecks`, `AlterRowValidator`), `AlterActionPhase` (the
  nine-pass order), `AlterTableLockMode`, `AlterTableLookupKind`,
  `AlterTablePreparation`, `AlterForeignKeyAdmission`, `ForeignKeyCreation` (the
  statement's CREATE TABLE REFERENCES targets and ADD FOREIGN KEY captures, kept
  in a `StatementScope` slot), `RenameColumnCandidate`, `RenameLocalConstraint`,
  `ValidateQueryTableOutputType`. `AlterCookingError` and
  `AlterTypeCheckReparseError` clone public diagnostics without dropping a direct
  input-origin annotation or mutating the source; CHECK reparse clears only its
  internal source's `Position`, `InternalPosition` and `InternalQuery`.
- **Relation options** (`relation_options.go`): the storage-parameter registry,
  `HasRelationOptions`/`RelationOptionsOnly` (option-only versus mixed-list
  admission) and `RelationOptionsCandidate`, which transforms each option
  namespace independently, base first.
- **NOT NULL** (`not_null_*.go`, `physical_not_null*.go`): `NotNullCandidate`
  with `NotNullRecursion`, `NameCreateNotNull`, `UsingIndexNotNullCandidate`;
  physical NOT NULL transitions (`PhysicalNotNullFromType`,
  `TransitionPhysicalNotNull`, `PrunePhysicalNotNull`, `ValidatePhysicalNotNull`),
  the competing-unique and self-FK emitters, and the Restore repair parsers
  (`RestorePhysicalNotNullSyntax`, `RestoreNoInheritSyntax`) next to the emitters
  whose output they read.
- **CHECK, DEFAULT and generated columns** (`check*.go`, `default*.go`,
  `generated*.go`, `domain_dependencies.go`, `row_constraints.go`):
  `OrderedChecks`, `ValidateChecks`, `ValidateCheckRow`, `PhysicalRowViolation`
  (the first NOT NULL or CHECK violation of a physical row, rendered by
  `RowViolation.Diagnostic` with the caller's detail), `NotNullDetailError`, the
  dependency passes (`CheckDependents`, `DefaultDependents`, over
  `DropDependencyReader`), `DefaultReplayDeferrable`, `EvaluateGenerations`,
  `PrepareGenerationProgram`, `GenerationDepends`, the virtual-generation rules
  (`VirtualDestination`, `AdmitColumnVirtualGeneration`; index uses are
  `index.RejectVirtualUses`), `DomainDependency` and `BindChecks` (CHECK naming,
  inherited merge, duplicate diagnostics, ordinals and dependency rendering of
  CREATE and ADD CONSTRAINT, binding through `Host.BindCheckExpression`).
- **Foreign keys** (`foreign_key*.go`): declarations and comparison binding
  (`ForeignKeyDeclarations`, `BindForeignKeyComparison`, `ValidateForeignKeys`),
  diagnostics over `access.View` (`ForeignKeyDiagnostic`,
  `ForeignKeyReferencesAllowed`), the dependency pass (`ForeignKeyDependents`),
  the probe (`FindForeignKeyParent`, `PrepareForeignKeyChild`,
  `ProbeForeignKeyIndex`, `ForeignKeyRows`), provider plans
  (`InstalledForeignKey`, `AlterForeignKeyIndex`, the OID-ordered
  `ForeignKeyTypeProviders`/`ForeignKeyDefinitionProviders` walks and the
  post-grant recheck `CheckForeignKeyDefinitionProviders`), the initial row scans
  (`ScanForeignKeys` over `AddedForeignKeys` or `ReferencingForeignKeys`, and
  `ScanRestoredForeignKeys`) and Restore (`RestoredForeignKeys` with an opaque
  `ddl.Authority`; `RestoredForeignKey.ResolveTarget` is the target-path
  fallback). A `ForeignKeyScan` carries the authority, the domain and a
  `ForeignKeyRowsCapture` callback that takes `Engine.mu`, so callers hold
  nothing.
- **CREATE TABLE decisions** (`create.go`, `create_shape.go`,
  `inheritance_create.go`, `like*.go`, `identity.go`, `serial.go`,
  `foreign_key_stage.go`), all over `Host`: `StageCreateTable` (CREATE TABLE and
  the CREATE SCHEMA table element, in fixed order: descriptor, row type,
  inheritance, NOT NULL names, identity and serial sequences, LIKE columns,
  defaults, generations, CHECKs, LIKE checks, constraint OIDs, UNIQUE, EXCLUDE,
  LIKE indexes, FOREIGN KEYs), `StageInheritance`, `StageLike*`,
  `StageCreateIdentities`, `StageIdentitySequence`, `StageCreateSerials`,
  `StageForeignKey`, `StageCreateForeignKeys`, `CreateCheckSyntax` and
  `CheckCreateShape` (the shape errors in order); `NewRelation` and
  `AssignDefinitionOIDs` over a `ddl.Allocator` (typedef's composite relations
  use `NewRelation` too).
- **ALTER TABLE decisions**: candidates over `AlterHost` (`AddColumnCandidate`,
  `GenerationCandidate`, `AlterTypeCandidate` with `AdmitAlterType`/
  `CompleteAlterType`/`PrepareAlterType`, `PrepareAlterTableActions`,
  `PrepareAddedChecks`, `RebindAlterType{Checks,Indexes}`, `CookAlterDefault`);
  staging over `Host` (`StageAddedUnique`, `StageAddedForeignKey`,
  `AddIdentityCandidate`); post-loop rebuilds over `ddl.Host`/`ddl.Stage`
  (`RebuildAlterNotNulls`, `RebuildAlterForeignKeys`,
  `RebindPhysicalNotNullType`); and pure pieces (`PrepareIdentitySet`/
  `FinishIdentitySet` around the root's sequence wait, `PlanAlterScan`, the
  ALTER [NOT] OF checks `AlterTableOf`/`TypedSources`, the loop pieces in
  `alter_table_loop.go`). Over `DropDependencyReader`: `DropConstraintIndexes`,
  `OwnedSequences` and `DropIdentity`.
- **ALTER row rewrite and scan**: `PlanAddedColumns` (ADD COLUMN DEFAULT
  preparation and one-time fills), `RewriteTypeRows` (the TYPE, added-column and
  generation rewrite of the effective live heads into a private replacement) and
  `ValidateAddedConstraintRows` (the CHECK and NOT NULL scan of old rows). The
  caller passes the statement's `exec.Context` template and the private write
  and delete roots; the functions take no lock, but expressions may reach host
  methods that take `Engine.mu`, so callers hold nothing.
- **Inheritance** (`inheritance_*.go`, `typed_inheritance.go`): the recursion
  plan `InheritanceAlterPlan` (the root holder `inheritanceAlterState` adds the
  stage, journal and prepared nodes), `InheritanceAlterOrder`,
  `InheritanceOnlyActions`, `InheritanceRecursiveAction`, the events, the guards,
  `ValidateInheritance`, retired constraints (`InheritRetiredColumnConstraints`,
  `RetireRebuilt*`), `RemapInheritance`, `InheritanceDefinitionReader` (overlays
  prepared drops through `DefinitionDelta`), typed-table propagation
  (`TypedClosure`, `TypedInheritedActions`), `InheritanceDropClosure` (the
  OID-ordered tables a DROP locks), and the query helpers
  `InheritanceQueryClosure`, `CaptureInheritanceQueryPrefix`, `SourceOnly`,
  `InheritanceQueryLeaves` (the stored-view-aware leaf walk) and
  `InheritancePendingGrants` (which physical leaves still need a grant and in
  which row-mark mode). The recursion decisions are host-less:
  `PlanInheritanceAlter`, `InheritedAlterActions`,
  `FinishInheritanceAlterCandidate`, the nil-safe plan methods
  (`inheritance_alter_record.go`), `InheritanceDefaultCandidate` and the rename
  cores `RenameInheritanceColumn[Precheck]`/`RenameInheritanceConstraint[Precheck]`.
  `PrepareInheritanceNode` cooks phases 0–6 of one node before the sole stage
  exists, through the six-method `PrepareHost`, recording
  `InheritancePreparedNode`/`InheritancePreparedAction`; prepared drops cross as
  `PreparedDrop`. Callers hold nothing; the prepare host methods take `Engine.mu`
  and the drops may wait. The same plan and stage serve the partition CHECK/NOT
  NULL policy (`partition_alter*.go`): root CHECKs are cooked once and remapped to
  descendant row identities, final descriptors carry exact `PartitionOrigin`
  (never synthetic ordinary parents), and ONLY removal localizes immediate child
  contributions without changing grandchildren.
- **Dump and Restore** (`dump.go`, `constraint_dump.go`, `*_restore*.go`,
  `default_replay.go`): `DumpRelation` and all its emitters, `RestoredNotNull`,
  `RestoreAttributeRankSyntax`, `RestoreAlterConstraints`, the retired-constraint
  and inherited recipe/owner/FK repair records, `PhysicalNotNullDumpDependencies`
  (the dump graph edges of physical NOT NULL providers over a `dump.Resolver`) and
  `PrepareGenerationPlaceholders(view, tables, seeds)` (the inheritance-component
  planner for historical or differing inherited generated recipes; nil seeds keep
  captured-inheritance selection, and each component restores its generation
  expressions before CHECKs and data).
- **Restore repair cores**: the decisions inside the root Restore hooks, over a
  `catalog.Read` or `access.View`: whole captures (`CaptureRestored*`,
  `CaptureHistoricalFacts`, `CapturePhysicalImportIndex`, `RemapRetiredConstraints`,
  `ValidateForeignKeyData`); split pairs of a shape check before authorization
  (`*Shape`) and a decision after resolution (`PairRestored*`, `Capture*`,
  `ValidateRestoredDefault`, ...); and stage-local candidates
  (`NoInheritNotNullCandidate`, `AttributeRankCandidate`, `PhysicalNotNullCleanup`,
  `RetiredConstraintCandidate`) that return the next relation (nil for no change)
  for the caller to `Advance` or `CommitRetiredRepair` after publication.
  Receipts hold a role record, not an authorization view; root-only validators
  come in as arguments (`catalog.Provider`, `*ids.Domain`).
- **Other cores**: `DropTargets` (DROP TABLE resolution over `Resolver`), the SET
  SCHEMA cores (`set_schema.go`), `QueryTableColumns` and `ValidateInitialTables`
  (CREATE TABLE AS), `TypedObservationValid`, `TablespaceOnlyActions` and
  `ValidateAlterTablespaceMove`, `RelationProvider` (the relation descriptor
  validator, whose `RelationValidators` slots are called in fixed order:
  `Policies` after the column checks, `Partition` after the CHECK-kind check,
  `Definition` as the view tail), `RecomputeRemappedGenerations` (stored
  generated cells that read their owner's table OID after an OID remap; its CHECK
  snapshot callback is called with no lock held), `Stage.DefinitionProviders`
  (OID-ordered provider lock requests — `ShareUpdateExclusive` for an
  inheritance parent of a staged relation, else `AccessShare` — which the root
  takes and rechecks under `Engine.mu`), `Deparse` (`pg_get_constraintdef`,
  `pg_get_expr`, `pg_get_indexdef`, `pg_get_viewdef`, `pg_get_serial_sequence`
  over `DeparseInputs`; here because the serial form needs
  `regobj.ClassQualifiedName`), `ValidateSelectedDomainRows` (ALTER DOMAIN row
  scans), `Target`/`TargetAdmission` (each statement kind belongs to exactly one
  registered `TargetEntry`, so `AdmitTarget`'s order cannot change a result),
  `ExplicitLockMode`/`CheckExplicitLock` (LOCK TABLE: relation kind, then the
  captured view's rights, then the partition-hierarchy boundary) and the `Stage`
  slot (LIKE sources recorded by `CaptureLikeSource`, checked at publication by
  `ValidateLikeSources`).

## Restore ADD CHECK admission

The root admits three real ALTER TABLE ADD CHECK replay forms when historical
cast recipes need separate statements: a qualified single named valid ADD CHECK
on a permanent ordinary table outside the inheritance graph; a fresh local
`ALTER TABLE ONLY ... ADD CHECK` on an inheritance participant that is `NO
INHERIT` or has no descendants (the flag is preserved and existing or inherited
names never merge into local definitions); and a fresh recursive ADD CHECK on a
parent with descendants, only after verifying permanent ordinary targets and
absent names across the real propagation closure. Connected generation-repair
authority takes precedence. All use normal binding, ownership, propagation, row
validation and cancellation, never a replay marker, and never promote NOT VALID
constraints. Retained selected-cast DEFAULTs likewise replay as independent
ALTER statements on ordinary and foreign tables, without changing global
deferral or evaluating a default.

## Inheritance attach and detach fences

`inheritance_relationship.go` owns compatible ordinary INHERIT/NO INHERIT
candidates; these are relationship-phase actions, not table recreation, and
preserve child attribute/fact identities, storage, rows, defaults and generated
recipes. Parent physical keys and indexes are never inherited. The root owns
exact endpoint grants/admission and the single ALTER stage.

- Parent candidates read the stage's complete catalog view, including unchanged
  base descriptors, not its staged-only Get map. Capture observes every lexical
  endpoint, including absence, without emitting later lookup/owner errors; each
  action consumes its own lookup and parent grant before compatibility, so
  unreached endpoints cannot preempt earlier errors.
- Post-wait admission compares the original parent before refreshing it and
  rejects replacement or newly shadowing targets. Only genuine first-child
  marker publication is allowed: an unstaged parent's admitted marker pointer
  can be consumed into the stage, never reminted or substituted for staged own
  changes, and `Stage.Base` is never rebased. The same endpoint fence runs under
  `Engine.mu` at implicit publication and explicit private-journal installation;
  its authorization capture precedes `Session.Mu`.
- Attach's typed/partition preparation restrictions and detach's
  actual-partition execution restriction are distinct; relationship-only detach
  reaches its missing-edge diagnostic without storage.
- `InheritanceParent` stores identity plus a live sparse ordinal. Last detach
  archives retired NN/CHECK origins as history without a live parent provider;
  strict restore carriers pair that history with fresh facts, and TYPE cleanup
  uses checked physical-NN historical causes without inventing keys or
  constraint rows. Detach of retained TEMP certificates uses the admitted parent
  identity plus `ReferenceEpochValid` and never reconnects an archived source.
- Recursive DROP's RESTRICT primary distinguishes physical column deletions from
  origin decrements: a local or other-parent contribution preserves the child's
  column. CHECK attachment and CHECK-origin validation share one cooked
  deparsed-definition comparator; `SameInheritedExpression` stays structural,
  normalizing only retained row-reference epochs.

## Hosts

`Host` (`host.go`) is what CREATE TABLE, the CREATE SCHEMA table element,
PARTITION OF children, ADD IDENTITY and FOREIGN KEY staging need: `index.Host`
(and through it `ddl.Host`), `ddl.TypeStaging`, row types, column type and
collation resolution, the table and sequence stage slots (`TableStage`,
`SequenceStage`, `SequenceColumns`, `NameBinding`, `StageChanged`),
`StagedTemporary`, relation resolution (`Resolver`, `RestoreResolver`),
`DescribeCreateTable`, the binders `BindColumnDefaults`, `BindGenerations` and
`BindCheckExpression`, `OperationDefinition` (the operation's definition reader)
and `LiveEpoch`. The root `ddlHost` implements it (`table_host.go`). `Resolver`
is its stage-less part, used by `DropTargets` before DROP TABLE begins a stage;
`RestoreResolver` is the stage-less part `RetiredConstraintCandidate` resolves
retired parents through.

`AlterHost` adds what one ALTER statement's bindings need: the live `Input`
(captures mutate it, so callers read it again before each binding), the snapshot
captures (`CaptureTypeDiagnosticName`, `CaptureCommentSnapshot`,
`CaptureCheckSnapshots`), the binders `BindDefault`, `BindGeneration`,
`RebindCheck` and `BindAlterTypeUsing`, `TypeChangeDependents` and
`StageIdentitySequenceOptions`. The root `alterHost{ddlHost; scope}`
(`alter_host.go`) implements it, one per call, because the ALTER loop begins its
stage lazily and the stage may be nil.

Every `Host` and `AlterHost` method runs with `Engine.mu` held, never takes or
releases it, and never waits; the authority is an opaque `ddl.Authority`, so
code here re-reads `AccessView` after every `Recapture`. The removal interface
`DropDependencyReader` is passed in with callers holding nothing; its comment
marks the methods that wait (`Acquire`, `CaptureIdentitySequence`, the
dependency passes) or take `Engine.mu` (`Remove`, `ApplyViewDependents`).

## What stays in the engine

The orchestration: `createTableLocked`, `schemaTableLocked`, `dropTableLocked`,
`alterSchemaLocked`, `prepareQueryTableLocked` and `alterTableLocked` with its
pass loop; the candidate wrappers that take `Engine.mu` across a `table` call and
build the host; the row evaluation template (`alterRowEvaluation`) and
projection roots; lock admission and post-wait checks
(`acquireDefinitionProviders`, `admitInheritanceAlter`,
`acquireAlterForeignKeys`, `admitInheritanceDropClosure`, `acquireRelationTarget`
around `AdmitTarget`, `acquireInheritanceSources` around
`InheritancePendingGrants`); the identity sequence waits; the prepare host
(`inheritancePrepareHost`), prepared drops and their consumers; Finish; the
binder bodies behind the host; the bulk `ALTER TABLE ALL IN TABLESPACE` handler;
the ALTER DOMAIN fence; and the Restore hook bodies (authorization, the
`Engine.mu` window, trust checks, name resolution, `publishRestoreRepairLocked`,
the pending foreign-key binder and `restore_replay.go`).

## Invariants

- ALTER TABLE keeps its nine-pass order (`AlterActionPhase`), the inheritance
  pass order and per-node re-entry, so the same first error is reported for
  every action list. Definition preparation uses ordered provider results once,
  before the sole stage and row rewrite; no partially prepared stage is rebased
  or patched to a newer base.
- Constraint order: index attachments stage first; physical builds follow index
  declaration order before historical CHECK scans, which follow declaration
  order. Ordinary DML enforces NOT NULL first and CHECKs in constraint-name
  order. TYPE rewrites check each converted row's NOT NULL and affected CHECKs
  before converting the next. Final rows and all required constraints validate
  before publication; failure leaves identities, constraints, rows and roots
  intact.
- Dump output is byte-stable: `DumpRelation` emitters keep their order and never
  iterate maps into output; each Restore syntax parser stays next to the emitter
  whose output it reads. Negative constructor CIRCLE leaves stay on qualified
  constructor SQL; only array-containing cells that need them use native
  captured-cell metadata alongside SQL NULL, which the engine authenticates
  (destination, owner, placeholder, operation lifetime) and PostgreSQL receives
  as SQL NULL.
- The package holds no engine state and no mutex. Functions here never wait;
  callers in the root own `Engine.mu` and lock waits.

## Testing

Package units run over a fake host or a small catalog; most table tests build an
engine and stay in `internal/engine`, naming the code as `table.X`. Run the
inheritance and NOT NULL admission units with `RACE=1`. Public SQL lives in
[reference/tables](../../../reference/tables/README.md#testing) and ALTER TABLE
in [reference/alter](../../../reference/alter/README.md#testing); add
`REFERENCE=1` for the oracle.

```sh
make check RUN='^Test(TableHost|AlterHost|RelationValidatorsOrder|GeneratedRemapCheckCapture|TableTargetAdmission|AdmitTargetAsksEachEntry)'
make check RUN='^Test(CheckCreateShape|TypedSources|PlanAlterScan|ValidateAddedConstraintRows|RelationOptions|PrepareInheritanceNode|InheritancePendingGrants|InheritanceDropClosure|DefinitionProviders|Deparse(SerialSequence|MissingObjects)|ValidateSelectedDomainRows|OwnedSequenceName)'
make check RUN='^Test(ForeignKey(TypeProviders|DefinitionProviders|Checks)|Scan(Restored)?ForeignKeys|RestoredForeignKeyTarget|RestoreInherited|AttributeRankRestore|RestoreConstraintHistory|RestoreNotNullNoInherit)'
make check RACE=1 RUN='^Test(NotNull|PhysicalNotNull|Inheritance)'
make check RUN='^TestReference(TableCore|TableLike|NotNull|Check|Inheritance|TypedTable)'
```
