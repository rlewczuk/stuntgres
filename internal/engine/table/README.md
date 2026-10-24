# internal/engine/table

Table records and decisions that need no engine: columns and constraints, NOT
NULL (logical and physical), CHECK, DEFAULT and generated columns, relation
options, foreign-key definitions and the FK probe, ALTER TABLE, the table dump
record (`DumpRelation`) with its emitters and Restore parsers, and inheritance.
FK write-time enforcement and actions live in [`dml`](../dml/README.md); the
materialized snapshot codec is in `view`.

## Imports

It sits above `index` and below `partition`, `view`, `dml` and `control` in
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture). It
imports the lower engine packages (`access`, `analyze` types and pure
functions, `catalog`, `ddl`, `dump`, `exec`, `functions`, `index`, `lock`,
`sequence`, `stored`, ...), `parser`, `sqlstate`, `sqltype` and the standard
library. It never imports the engine or the packages above it, and never
builds a binder.

Inheritance lives here because ALTER TABLE re-enters its handler once per
inheritance node and the dump record emits both, and Go cannot split one type's
methods across packages. Inheritance query and DML expansion stay with root
query admission and `dml`; the pure leaf walk and pending-grant decision they
call live here.

## What it owns

- **CREATE TABLE** (`create*.go`, `inheritance_create.go`, `like*.go`,
  `identity.go`, `serial.go`, `foreign_key_stage.go`), over `Host`:
  `StageCreateTable` stages in fixed order (descriptor, row type, inheritance,
  NOT NULL names, identity and serial sequences, LIKE columns, defaults,
  generations, CHECKs, LIKE checks, constraint OIDs, UNIQUE, EXCLUDE, LIKE
  indexes, FOREIGN KEYs); `CheckCreateShape` reports shape errors in order;
  `NewRelation`/`AssignDefinitionOIDs` work over a `ddl.Allocator`.
  The same stage serves permanent and TEMP creation in explicit transactions;
  transaction admission and shared catalog/storage publication belong to the
  engine, not `CheckCreateShape`.
- **ALTER TABLE** (`alter_*.go`, `rename.go`): `AlterActionPhase` (the
  nine-pass order), `AlterTableLockMode`, `AlterTablePreparation`, candidates
  over `AlterHost` (`AddColumnCandidate`, `AlterTypeCandidate`,
  `PrepareAlterTableActions`, ...), staging over `Host`, post-loop rebuilds
  over `ddl.Host`/`ddl.Stage` (`RebuildAlterNotNulls`,
  `RebuildAlterForeignKeys`) and the row passes `PlanAddedColumns`,
  `RewriteTypeRows` and `ValidateAddedConstraintRows`. The pass-loop helpers
  (`alter_table_finish.go`) take values: `RelationOptionsOnlyCandidate`,
  `AlterNotNullPass` (pass 4; a nil inheritance plan is allowed),
  `FinishAlterCandidate` (the after-loop finish, with the inheritance finish
  as a callback and the stage's emptiness as a `bool`), `BumpAlterRevision`,
  `AlterRewrites` and `AlterStorageShape`. The row passes take no
  lock, but expressions may reach host methods that take `Engine.mu`, so
  callers hold nothing. `AlterCookingError` clones public diagnostics without
  mutating the source.
- **NOT NULL, CHECK, DEFAULT and generated columns** (`not_null_*.go`,
  `physical_not_null*.go`, `check*.go`, `default*.go`, `generated*.go`,
  `row_constraints.go`): `NotNullCandidate`, the physical NOT NULL transitions,
  `BindChecks`, `ValidateChecks`, `PhysicalRowViolation` (first NOT NULL or
  CHECK violation of a row), `EvaluateGenerations`,
  `PrepareGenerationProgram` and `ValidateGenerationPlan`.
- **DROP dependency decisions** (`composite_field_dependency.go`,
  `owned_sequences.go`, `drop_removal.go`, the CHECK, DEFAULT, foreign-key and
  identity drop files): passes over `DropDependencyReader`, the drop stage the
  root `dropProvider` implements. `OrdinaryColumnDrop`, `CheckDependents`,
  `DefaultDependents`, `ForeignKeyDependents`, `DropIdentity`,
  `CompositeFieldDependencies` (CHECKs that use a dropped composite field),
  `RemoveDomainCheck`, `OwnedSequences` and `OwnedExpressionDependencies` (the
  AUTO sequences of the targets, including removed inheritance owners) keep
  the reader's call order, so prepared-drop effects and notices keep theirs.
  `CheckRemovalLocked` admits a whole-object removal under `Engine.mu`
  (objects the system requires, then the registered class provider);
  `ClosureTargetClass` and `RetiredProviderClass` are the class lists of the
  root's expression-dependency pass; `CascadeColumnDrops` is that pass's
  column, domain-CHECK and retired-provider steps, with the root projecting
  each new shape.
- **Prepared DROP log** (`drop_preparation.go`, `drop_preparation_view.go`):
  `DropPreparation` records, in order, the `DropEffect`s of a drop prepared
  before the statement's stage exists (ALTER TABLE DROP COLUMN, DROP IDENTITY
  and constraint-index drops over an inheritance tree), chained to the node's
  `Prior` preparation. It is the prepared drop's `DefinitionReader`,
  `PreparedDrop` and `DefinitionDelta`. `Observe` captures an address's
  original descriptor before any read returns it, and `ValidateObservations`
  fences those captures and the statement, transaction, domain and epoch
  against a catalog; additive members are not replacements. `RewindNotices`
  positions index effects, not notices. `DropPreparationView` is the root
  `dropProvider`'s prepared-mode reads (replacement, removal, enumeration,
  index layouts, input descriptors) over a base, a preparation (nil for a bare
  provider) and the identity sequence captured at its grant;
  `DescriptorsOver` enumerates over a base that may change while it runs. The preparing
  statement's goroutine owns the log; it has no lock, and the statement
  identity is an opaque `any`.
- **Relation options** (`relation_options.go`): the storage-parameter registry
  and `RelationOptionsCandidate`, which transforms each option namespace
  independently, base first.
- **VACUUM** (`vacuum_*.go`): `NormalizeVacuum`, then `CheckVacuumEntry`
  (transaction block, FULL, ANALYZE); target selection over `access.View`
  (`VacuumSelection.AddDatabase` and `AddRoot`, whose descendants are
  breadth-first and OID-ordered per root) with `VacuumAllowed`, and the
  post-grant gates `VacuumWork`; the detached work pass `PrepareVacuumPass`
  over a `VacuumCapture` (`CaptureIndexesLocked`, `SameLocked`), plus
  `DatabaseFrozenXIDLocked` and `VacuumReport`. Selection and gates return
  `VacuumWarning`s, which the root emits after unlocking, in order. The pass's
  `phase` callback takes `Engine.mu` once per progress update, so callers hold
  nothing.
- **Foreign keys** (`foreign_key*.go`): declarations and comparison binding,
  diagnostics over `access.View`, the probe (`FindForeignKeyParent`,
  `ProbeForeignKeyIndex`), OID-ordered provider walks, initial row scans and
  Restore (`RestoredForeignKeys`). A `ForeignKeyScan` carries a
  `ForeignKeyRowsCapture` callback that takes `Engine.mu`, so callers hold
  nothing.
- **Inheritance** (`inheritance_*.go`, `typed_inheritance.go`,
  `partition_alter*.go`): the host-less recursion plan
  (`InheritanceAlterPlan`, `PlanInheritanceAlter`), `PrepareInheritanceNode`
  (cooks one node's phases before the sole stage exists through `PrepareHost`,
  whose methods take `Engine.mu`; prepared drops cross as `PreparedDrop` and
  may wait), `InheritanceDefinitionReader`, `InheritanceDropClosure` (the
  OID-ordered tables a DROP locks), the query helpers
  `InheritanceQueryLeaves`/`InheritancePendingGrants`, and the partition
  CHECK/NOT NULL policy over the same plan and stage.
- **Dump and Restore** (`dump.go`, `constraint_dump.go`, `*_restore*.go`,
  `default_replay.go`): `DumpRelation` and its emitters, the Restore syntax
  parsers, `PrepareGenerationPlaceholders`, and the repair cores inside the root
  Restore hooks (captures over `catalog.Read`/`access.View`, shape checks
  before authorization, decisions after resolution, stage-local candidates that
  return the next relation for the caller to commit after publication).
- **Other cores**: `DropTargets` (over `Resolver`), SET SCHEMA, CREATE TABLE AS
  (`QueryTableColumns`), `RelationProvider` (its `RelationValidators` slots run
  in fixed order), `RecomputeRemappedGenerations`, `Stage.DefinitionProviders`
  (OID-ordered provider lock requests the root takes and rechecks under
  `Engine.mu`), `Deparse` (`pg_get_constraintdef`, `pg_get_expr`,
  `pg_get_indexdef`, `pg_get_viewdef`, `pg_get_serial_sequence`),
  `ValidateSelectedDomainRows`, `Target`/`AdmitTarget` (each statement kind
  belongs to exactly one `TargetEntry`, so entry order cannot change a result),
  `CheckExplicitLock` (LOCK TABLE) and the `Stage` slot (LIKE sources checked
  at publication by `ValidateLikeSources`).
  Default OID/TEXT view-definition inquiries use the captured
  `DeparseInputs.BuiltinViewDefinition` capability for builtin views without
  stored payloads; ordinary nonviews still return NULL. Stored-view rendering
  retains its own pretty/wrap presentation path.

## Hosts

`Host` (`host.go`) serves CREATE TABLE, the CREATE SCHEMA table element,
PARTITION OF children, ADD IDENTITY and FOREIGN KEY staging: `index.Host` (and
through it `ddl.Host`), `ddl.TypeStaging`, type and collation resolution, the
table and sequence stage slots, relation resolution, the binders
`BindColumnDefaults`, `BindGenerations`, `BindCheckExpression`,
`OperationDefinition` and `LiveEpoch`. The root `ddlHost` (`table_host.go`)
implements it. `Resolver` and `RestoreResolver` are its stage-less parts.

`AlterHost` adds one ALTER statement's bindings: the live `Input` (captures
mutate it, so read it again before each binding), snapshot captures, the
binders `BindDefault`, `BindGeneration`, `RebindCheck`, `BindAlterTypeUsing`,
`TypeChangeDependents` and `StageIdentitySequenceOptions`. The root
`alterHost{ddlHost; scope}` (`alter_host.go`) implements it, one per call,
because the ALTER loop begins its stage lazily.

**Every `Host` and `AlterHost` method runs with `Engine.mu` held, never takes or
releases it, and never waits.** The authority is an opaque `ddl.Authority`, so
code here re-reads `AccessView` after every `Recapture`. `DropDependencyReader`
is called with callers holding nothing; its comment marks the methods that wait
or take `Engine.mu`.

## Inheritance attach and detach fences

`inheritance_relationship.go` owns INHERIT/NO INHERIT candidates: relationship
actions that preserve child attribute/fact identities, storage, rows, defaults
and generated recipes. The root owns endpoint grants, admission and the single
ALTER stage.

- Parent candidates read the stage's complete catalog view. Capture observes
  every lexical endpoint, including absence, without emitting later errors; each
  action consumes its own lookup and grant before compatibility, so unreached
  endpoints cannot preempt earlier errors.
- Post-wait admission compares the original parent before refreshing it and
  rejects replacement or newly shadowing targets. An unstaged parent's admitted
  marker can be consumed into the stage, never reminted, and `Stage.Base` is
  never rebased. The same endpoint fence runs under `Engine.mu` at implicit
  publication and explicit journal installation; its authorization capture
  precedes `Session.Mu`.
- `catalog.InheritanceParent` stores identity plus a live sparse ordinal. Last
  detach archives retired NOT NULL/CHECK origins as history without a live
  parent provider.
- CHECK attachment and CHECK-origin validation share one cooked
  deparsed-definition comparator; `SameInheritedExpression` stays structural.
- `DumpRelation.ColumnProvenance` supplies the same effective recipe ownership to
  CREATE emission and comparison-binding collection. Unchanged inherited recipes
  do not become CREATE expressions; actual deferred defaults and placeholder
  generation repairs keep their own attribute-specific ALTER sites.

## What stays in the engine

Orchestration: `createTableLocked`, `dropTableLocked`, `alterTableLocked` with
its pass loop, the lazy stage (`authorizedStage`), the stage-to-journal merge
(`mergeStageJournalLocked`) and the IF EXISTS no-op
(`skipMissingRelationLocked`), and the other statement handlers; the wrappers that take
`Engine.mu` across a `table` call and build the host; the row evaluation
template; lock admission and post-wait checks (`acquireDefinitionProviders`,
`admitInheritanceAlter`, `acquireRelationTarget` around `AdmitTarget`, ...);
identity sequence waits; the prepare host and prepared-drop consumers; the
DROP pipeline (`dropProvider`'s stage mode and lock waits, the prepared-drop
admission and the `consumePreparedDrop` replay into the stage, and the lock
waits and projection of `ExpressionDependencies`); Finish;
VACUUM's lock loop, `Engine.mu` windows, retry, install and progress map;
the binder bodies; and the Restore hook bodies (authorization, the `Engine.mu`
window, trust checks, `publishRestoreRepairLocked`, `restore_replay.go`).

Permanent CREATE/DROP and the shipped ALTER forms consume the owner's private
catalog/storage and the ordinary journal. The engine claims actual destination
and companion names, holds ordinary DROP roots through transaction end, and
recaptures exact identities after waits before publishing their candidates.

## Invariants

- ALTER TABLE keeps its nine-pass order, the inheritance pass order and
  per-node re-entry, so every action list reports the same first error.
  Definition preparation uses ordered provider results once, before the sole
  stage and row rewrite; no partially prepared stage is rebased or patched.
- Constraint order: index attachments stage first; physical builds follow index
  declaration order, then CHECK scans in declaration order. Ordinary DML
  enforces NOT NULL first and CHECKs in constraint-name order. TYPE rewrites
  check each converted row before converting the next. Everything validates
  before publication; failure leaves identities, constraints, rows and roots
  intact.
- Dump output is byte-stable: `DumpRelation` emitters keep their order and
  never iterate maps into output; each Restore syntax parser stays next to the
  emitter whose output it reads.
- Restore replays separate ADD CHECK and DEFAULT statements through normal
  binding, ownership, propagation, row validation and cancellation, never a
  replay marker, and never promotes NOT VALID constraints.
- The package holds no engine state and no mutex. Functions here never wait;
  root callers own `Engine.mu` and lock waits.

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
make check RUN='^Test(DropPreparation|DropReaderSeam|InheritanceAlterAdmission|ReferenceAlterDropColumn)'
make check RUN='^Test(ForeignKey(TypeProviders|DefinitionProviders|Checks)|Scan(Restored)?ForeignKeys|RestoredForeignKeyTarget|RestoreInherited|AttributeRankRestore|RestoreConstraintHistory|RestoreNotNullNoInherit)'
make check RACE=1 RUN='^Test(NotNull|PhysicalNotNull|Inheritance)'
make check RUN='^Test(Reference)?Vacuum'
make check RACE=1 RUN='^TestVacuum(PublicationFences|AuthorizationLocks)$$'
make check RUN='^TestReference(TableCore|TableLike|NotNull|Check|Inheritance|TypedTable)'
```
