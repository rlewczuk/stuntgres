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
  indexes, FOREIGN KEYs); `CheckCreateShape` reports shape errors in order,
  including column enforcement-attribute transformation before plain CREATE's
  duplicate-name error. Existing IF NOT EXISTS targets skip that transformation.
  Attribute errors stay Execute-phase even for held statements.
  `NewRelation`/`AssignDefinitionOIDs` work over a `ddl.Allocator`.
  The same stage serves permanent and TEMP creation in explicit transactions;
  transaction admission and shared catalog/storage publication belong to the
  engine, not `CheckCreateShape`.
  Ordinary descriptors retain logged/unlogged identity before owned sequence
  creation. `persistence.go` normalizes namespace-owned TEMP and prepares ALTER
  intentions against original FK facts. Persistence actions are root-only;
  definition passes retain original identity until the final staged transition.
  A same-state request changes no descriptor, generation or owned sequence.
  Table-kind creation owners initialize exact `Relation.TableMethod` from the
  captured current default; generic `NewRelation` remains method-neutral.
  `PrepareAccessMethod` resolves original-order, root-only method intent once.
  DEFAULT clears a partition root's explicit choice but selects the current
  method for stored tables/materialized views. Method-only work shares the final
  private ALTER stage without copying rows or rebuilding unaffected indexes.
  Identity sequences default to the destination identity only when the
  declaration omits LOGGED/UNLOGGED. Explicit nontemporary choices win for
  CREATE and ADD IDENTITY; lower staging receives the resolved identity.

  Inherited CHECK expressions remap parent columns to the child without
  recapturing external providers. Their original dependency revisions and
  purposes survive, including selected comparisons whose routines have since
  undergone lawful identity or owner publication. Missing original routine
  dependencies still reject; ordinary hard-manifest and current-authority
  validation remain mandatory for the copied CHECK.
- **ALTER TABLE** (`alter_*.go`, `rename.go`): `AlterActionPhase` (the
  provider-phase order), `AlterTableLockMode`, `AlterTablePreparation`, candidates
  over `AlterHost` (`AddColumnCandidate`, `AlterTypeCandidate`,
  `PrepareAlterTableActions`, ...), staging over `Host`, post-loop rebuilds
  over `ddl.Host`/`ddl.Stage` (`RebuildAlterNotNulls`,
  `RebuildAlterForeignKeys`) and the row passes `PlanAddedColumns`,
  `RewriteTypeRows` and `ValidateAddedConstraintRows`. The pass-loop helpers
  (`alter_table_finish.go`) take values: `RelationOptionsOnlyCandidate`,
  `AlterNotNullPass` (pass 4; a nil inheritance plan is allowed),
  `FinishAlterCandidate` (the after-loop finish, with the inheritance finish
  as a callback and the stage's emptiness as a `bool`), `BumpAlterRevision`,
  `AlterRewrites` and `AlterStorageShape`. `AlterEventRewriteReason`
  accumulates default/generated and checked TYPE causes without collapsing
  combinations or confusing native row copies with semantic events. The command
  owner adds actual persistence/method changes only for stored relation kinds.
  The row passes take no lock, but expressions may reach host methods that take
  `Engine.Mu`, so callers hold nothing. `AlterCookingError` clones public diagnostics without
  mutating the source.
  `ValidateConstraintCandidate` returns an immutable validity-only successor and
  selected stable scan identities; eligible valid facts return the identical
  predecessor. Misc resolution follows all ADD definitions, including deferred
  foreign keys. `PlanAlterScan` unions selected validation with independently
  required shape checks without scanning unrelated invalid facts.
  `ClusterCandidate` resolves an unqualified index in the target namespace from
  the current stage after index reconstruction. Capability, predicate and validity
  checks precede partitioned-target rejection and any bit replacement. Changed
  index envelopes and the parent advance effective revisions together; an index
  created or rebuilt by a companion action retains its already-staged revision.
  Fresh private indexes update the candidate rather than replacing a nonexistent
  base descriptor. Unchanged selections retain descriptor identity and all keys,
  providers and storage roots. A proven deferred no-op also retains the prior
  statement journal identity, so private prepared analysis and accepted fold backing
  remain reusable; companion mutations still install their complete candidate.
  Selection/clearing are root-only
  ShareUpdateExclusive metadata actions; `SET WITHOUT OIDS` is an AccessExclusive
  compatibility no-op, including phase-zero inheritance prefix preparation.
  Neither operation performs physical heap clustering or promises unordered query order.
  `EnforceConstraintCandidate` changes only an owned FK slice, retaining constraint
  identity and returning transition-time historical work. Already-enforced
  invalid facts remain invalid on an ENFORCED no-op; VALIDATE rejects unenforced
  facts. CHECK enforcement changes use ordinary atomic DROP/ADD replacement.
  `AlterConstraintTimingCandidate` changes only persistent FK timing defaults,
  retaining constraint/trigger OIDs, trigger modes, enforcement and validity.
  No-ops retain the predecessor; changed candidates detach only the FK slice.
  Timing is root-only MISC work, independent of enforcement and transaction-local
  mode overrides. Historical ADD/enable validation is not deferred by changing
  those defaults.
  `AlterConstraintInheritCandidate` changes only a selected NN's inheritability,
  retaining identity, validity, PK/physical backing and nullability. Explicit
  parent/attribute/constraint events localize only direct children on detach.
  Attach merges existing compatible facts without validation and stops that
  branch; missing facts use the ordinary inherited SET constructor and recurse.
  Ordinary ONLY still performs direct-child work. Bare and matching operations
  retain relation/owner/pending-event/kind guards; inherited-count rejection is
  specific to requested NO INHERIT.
  Every admitted ADD, VALIDATE and genuine-enable scan retains its captured
  historical obligation. Final false/invalid metadata is accepted only with an
  actual later disable of that exact queued identity; never-admitted invalid
  selections, changed definitions/providers and invalid-still-enforced facts
  remain fenced. `AlterForeignKeyNames` projects command-local naming without
  cooking definitions or allocating OIDs. It shares `ForeignKeyConstraintName`
  with binding, preserves namespace reservations outside the candidate, and
  distinguishes other-constraint assignment order from deferred installation.
  DROP NOT NULL uses the same pure stable-attribute/fact selector as
  `NotNullCandidate`. Only an actually selected NN identity is omitted from the
  private naming candidate; absence is a no-op, and relation/domain reservations
  remain intact. Physical-NN, identity, primary-key and other eligibility checks
  retain their ordinary execution-stage ordering and diagnostics.
  Named rule-mode actions are root-only miscellaneous work after deferred FKs.
  Their pure immutable candidates preserve recipes/OIDs/dependencies and return
  the identical predecessor for no-ops. Mode-only lists use metadata-only
  staging without heap copies; mixed lists share the ordinary candidate, stage
  and enclosing Finish, including cancellation and late validation failure.
  `TriggerModeCandidate` selects named/ALL/USER triggers from the current staged
  catalog and returns private replacements for user definitions, identity-linked
  partition clones and FK descriptor owners. ALL and named system selection
  require actual superuser authority even when unchanged. Trigger actions are
  root-only MISC work; ordinary inherited work composes from staged modes without
  forwarding trigger selectors to traditional descendants.
  Each invocation creates at most one envelope per changed owner and lazily
  detaches its user-trigger and FK slices independently, once each. Bulk updates
  reuse that private backing; user entries use captured indices and grouped FK
  facts advance a forward constraint cursor. Partition recursion selects parent
  identities together and visits each descendant list once, without a persistent
  trigger index. No-op selection retains every borrowed payload and owner.
  Structural mode composition uses matching positions directly and builds
  invocation-local identity positions only when surviving entries have shifted;
  it does not repeatedly search entire source lists.
  Column storage/compression candidates resolve the completed shape in misc,
  using type-default storage for toastability, never width or current policy.
  Type rejection uses the catalog's canonical diagnostic naming with the
  statement reference path, including visible or schema-qualified domain names.
  STORAGE recurses unless ONLY; COMPRESSION stays root-only. TYPE clears both;
  inheritance consumes analysis's checked parent/local policy merge, while
  partition constructors copy effective values. Selected LIKE storage is
  specified even at its type default; default compression is unspecified and
  preserves inherited explicit methods. CREATE materializes the checked pair
  once onto its private relation's normalized stable-attribute map, before row
  types or index constructors. StageInheritance and StageLikeColumns retain
  provenance and other recipes, not competing policy writers. LIKE indexes use
  the common constructor against final destination columns and staged providers,
  never source index overrides.
  These are logical catalog policies, not tuple rewrites.
- **Column rename** (`rename.go`, `partition_rename.go`): immutable name/index
  candidates retain local slots, stable attributes and all bound definitions.
  Declarative recursion uses caller-selected snapshot-visible children in
  breadth-first, per-parent OID order; ONLY uses that same child selection.
  ONLY-with-children and direct inherited-column eligibility precede the
  corresponding source/destination checks. Visibility, runtime locks and
  publication remain in `tablecmds`; selection never changes canonical membership.
- **NOT NULL, CHECK, DEFAULT and generated columns** (`not_null_*.go`,
  `physical_not_null*.go`, `check*.go`, `default*.go`, `generated*.go`,
  `row_constraints.go`): `NotNullCandidate`, the physical NOT NULL transitions,
  `BindChecks`, `ValidateChecks`, `PhysicalRowViolation` (first NOT NULL or
  CHECK violation of a row), `EvaluateGenerations`,
  `PrepareGenerationProgram` and `ValidateGenerationPlan`.
  Virtual generation security inspects the unsimplified expression's logical
  types and selected routine declarations before conditional simplification.
  Non-builtin routine provenance is rejected independently of volatility or
  spelling; stored generation keeps its separate immutability admission.
  `ValidateCheckRow` prepares every enforced CHECK before completed-row truth,
  retaining each constraint's immutable original manifest and relation consumer.
  `CheckRecipeHost` shares cooked expressions within the live statement and
  registers stable consumed routine programs for its existing wait/publication
  readmission. Actual invoker and trusted RI-owner phases initialize separately;
  repeated rows reuse preparation without per-row ACL lookup. Readmission never
  evaluates CHECK truth or folds expressions. Unenforced checks retain complete
  bound definitions/dependencies but enter neither runtime preparation nor truth.
  The no-CHECK and all-unenforced paths allocate nothing.
- **DROP dependency decisions** (`composite_field_dependency.go`,
  `owned_sequences.go`, `drop_removal.go`, the CHECK, DEFAULT, foreign-key and
  identity drop files): passes over `DropDependencyReader`, the drop stage the
  `core.DropProvider` implements. `OrdinaryColumnDrop` passes its original live
  stable attribute to `ColumnDependencies` before metadata compaction. That
  controller shares the complete expression closure, preflight, admissions and
  retirement executor; local AUTO CHECK/default/index/sequence payload and the
  root's physical shape remain column-owned. NORMAL same-table dependents and
  recursive generated columns are not ownership exemptions.
  `CheckDependents`, `DefaultDependents`, `ForeignKeyDependents`, `DropIdentity`,
  `PruneCompositeFieldChecks`, `RemoveDomainCheck`, `OwnedSequences` and
  `OwnedExpressionDependencies` preserve ordered preparation effects. Composite
  CHECK pruning consumes preflighted identities, retaining inherited origin maps
  and admitted typed-table ownership; it never discovers a second closure.
  `CheckRemovalLocked` admits whole-object removal under `Engine.Mu`;
  `ClosureTargetClass` and `RetiredProviderClass` classify retirement providers.
  Subscriptions and explicit replication-origin registrations are whole-object
  retirement providers: local NULL-slot subscription DROP removes the subscription
  and any live canonical-name registration through the same retirement pass.
  A stale creation ID never selects an unrelated registration after ID reuse.
  Text-search configurations are whole-object retirement providers, including
  dictionary-dependent CASCADE and DROP OWNED; their genuine seeded simple
  configuration remains system-required. Captured REGCONFIG default operands
  are admitted as real configuration providers, not relation providers.
  Genuine procedural languages also participate in this same retirement closure:
  extension removal retires its language member, and handler-dependent CASCADE
  reaches a detached language through its real normal dependencies. Built-in
  internal/C/SQL languages remain system-required; no language DDL is added.
  `CascadeColumnDrops` consumes already captured columns, domain CHECKs and
  providers without re-entering a dependency controller. Each surviving relation
  cooks all selected stable attributes before remapping surviving CHECK/generation
  recipes, then receives one physical projection from its captured predecessor.
  Captured closure identity/revision remains the physical candidate authority;
  the reread staged maps carry earlier CHECK/default/policy pruning but neither
  replace the projector's predecessor nor allocate a second revision for those
  effects. The existing physical +1, descriptor-identity and immutable storage-root
  guards remain unchanged.
  `SequenceOwnedBy` is the exact ownership predicate shared by closure discovery
  and `OwnedSequences`; `RemoveColumnSequences` consumes admitted payload after
  metadata cooking without discovering another closure. AUTO index descriptors
  remain readable until UNIQUE/exclusion pruning and the incumbent index evolution.
  Whole-provider callers retain their genuine separate
  `OwnedExpressionDependencies` phase and intermediate index-layout lineage;
  already captured column batches never initiate that phase again.
- **Prepared DROP log** (`drop_preparation.go`, `drop_preparation_view.go`):
  `DropPreparation` records, in order, the `DropEffect`s of a drop prepared
  before the statement's stage exists (ALTER TABLE DROP COLUMN, DROP IDENTITY
  and constraint-index drops over an inheritance tree), chained to the node's
  `Prior` preparation, including routine retirements and surviving-owner pruning.
  Effects carry captured pre-deletion identities and independent provenance;
  replay never substitutes a surviving relation for removed attributes/payloads.
  It is the prepared drop's `DefinitionReader`, `PreparedDrop` and
  `DefinitionDelta`. `Observe` captures an address's original descriptor before
  any read returns it, and `ValidateObservations` fences those captures and the
  statement, transaction, domain and epoch against a catalog; additive members
  are not replacements. `RewindNotices`
  positions index effects, not notices. `DropPreparationView` is the root
  `DropProvider`'s prepared-mode reads (replacement, removal, enumeration,
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
  `VacuumWarning`s, which [`tablecmds`](../tablecmds/README.md#vacuum) emits
  after unlocking, in order. The pass's `phase` callback takes `Engine.Mu`
  once per progress update, so callers hold nothing.
- **Foreign keys** (`foreign_key*.go`): declarations and comparison binding,
  diagnostics over `access.View`, the probe (`FindForeignKeyParent`,
  `ProbeForeignKeyIndex`), OID-ordered provider walks, initial row scans and
  Restore (`RestoredForeignKeys`). A `ForeignKeyScan` carries a
  `ForeignKeyRowsCapture` callback that takes `Engine.Mu`, so callers hold
  nothing.
  Supporting UNIQUE/PK constraints must be nondeferrable; eligible standalone
  unique indexes remain valid targets. Implicit primary-key selection and explicit
  target/provider validation retain their distinct PostgreSQL 55000 diagnostics.
  `ResolveAlterForeignKeys` resolves scheduled stable definitions in execution
  order, preserving an independently required changed-value prefix and deduping
  each child/OID at its first position. Authentic final names, keys, supporting
  indexes and action semantics remain checked; descriptor order is not queue order.
- **Inheritance** (`inheritance_*.go`, `typed_inheritance.go`,
  `partition_alter*.go`): the host-less recursion plan
  (`InheritanceAlterPlan`, `PlanInheritanceAlter`), `PrepareInheritanceNode`
  (cooks one node's phases before the sole stage exists through `PrepareHost`,
  whose methods take `Engine.Mu`; prepared drops cross as `PreparedDrop` and
  may wait), `InheritanceDefinitionReader`, `InheritanceDropClosure` (the
  OID-ordered tables a DROP locks), the query helpers
  `InheritanceQueryLeaves`/`InheritancePendingGrants`, and the partition
  CHECK/NOT NULL policy over the same plan and stage.
  Validation propagates CHECK names and NOT NULL logical column names, preserving
  provenance without ADD/SET localization. Invalid inheritable roots retain
  all-descendant intent through valid intermediate nodes; valid roots and
  NO INHERIT facts do not discover descendants. Recursive validity-only work uses
  root-owner authority and SUE grants; nonvalidation companions retain their
  ordinary owner checks and strongest modes.
- **Dump and Restore** (`dump.go`, `constraint_dump.go`, `*_restore*.go`,
  `default_replay.go`): `DumpRelation` and its emitters, the Restore syntax
  parsers, `PrepareGenerationPlaceholders`, and the repair cores inside the root
  Restore hooks (captures over `catalog.Read`/`access.View`, shape checks
  before authorization, decisions after resolution, stage-local candidates that
  return the next relation for the caller to commit after publication).
  `CaptureDomainReplayNode` preserves concrete array dimensions, composite
  members, domain-NULL authority and exact Seg construction facts; scalar leaves
  use the ordinary lossless control literal renderer. Retained statistics alone
  use `CaptureStatisticsReplayNode`, which threads a private structural-tree
  mode through the same domain/array traversal. Reconstruction remains
  `typedef.RestoreDomainReplayNode` with checked scalar input and an explicit
  statistics-only tree capability, never general SQL input or row provenance.
  Valid noninheritable NN declarations replay a qualified ONLY ALTER CONSTRAINT
  before descendants are declared. Invalid facts replay after rows as atomic
  named ADD NOT NULL NOT VALID NO INHERIT constructors, admitted only by the
  isolated parser option and exact Restore allowlist. Both use ordinary
  candidates and catalog publication, with no paired catalog UPDATE repair.
  Top-level off-typmod finite timestamp cells use the same paired record with
  distinct temporal provenance and canonical default-output text. Dump emits
  no temporal sidecar for fitting values, unlimited precision, NULL or infinity.
  Destination-owned replay preserves exact data without enabling an ordinary
  assignment bypass or requiring the historical trigger/provider to survive.
- **Other cores**: `DropTargets` (over `Resolver`), SET SCHEMA, CREATE TABLE AS
  (`QueryTableColumns`), `RelationProvider` (its `RelationValidators` slots run
  in fixed order), `RecomputeRemappedGenerations`, `Stage.DefinitionProviders`
  (OID-ordered provider lock requests the root takes and rechecks under
  `Engine.Mu`), `Deparse` (`pg_get_constraintdef`, `pg_get_expr`,
  `pg_get_indexdef`, `pg_get_viewdef`, `pg_get_serial_sequence`,
  `pg_get_triggerdef`, `pg_get_ruledef`),
  `ValidateSelectedDomainRows`, `Target`/`AdmitTarget` (each statement kind
  belongs to exactly one `TargetEntry`, so entry order cannot change a result;
  non-relation ownership, including numeric large-object ownership, admits no
  relation target during either preparation or execution),
  `CheckExplicitLock` (LOCK TABLE) and the `Stage` slot (LIKE sources checked
  at publication by `ValidateLikeSources`).
  Trigger targets use ShareRowExclusive for CREATE and AccessExclusive for DROP;
  both therefore contend with genuine execution RowExclusive on tables or views.
  OID/TEXT view-definition inquiries, including boolean format selectors, use
  captured `DeparseInputs.BuiltinViewDefinition` authority for builtin views
  without stored payloads. Default format uses canonical fragments; pretty
  format requires explicitly owned pretty fragments. Ordinary nonviews return
  NULL. Stored-view rendering retains its own pretty/wrap presentation path;
  builtin callbacks do not claim wrap-width support.
  Trigger/rule inquiries dispatch by their own object class before relation-OID
  lookup. Trigger inventory includes immutable user/partition-clone payloads and
  all FK-owned RI identities; projection and display share validated RI slot,
  function, event and timing facts. Rule inventory includes ordinary DML rules,
  real view/materialized-view `_RETURN` payloads and explicitly advertised
  bootstrap return rules. A bootstrap rule uses the captured default/pretty body
  callback, never executable view admission. Missing advertised body authority
  is an invariant error, not missing-object NULL.
  `TemporaryOwner` captures only the existing session token: qualified own-TEMP
  targets use `pg_temp`, foreign-session TEMP retains its actual namespace,
  and pretty first honors relation visibility. Naming never calls the lazy
  namespace-creating `Temporary` callback.

## Hosts

`Host` (`host.go`) serves CREATE TABLE, the CREATE SCHEMA table element,
PARTITION OF children, ADD IDENTITY and FOREIGN KEY staging: `index.Host` (and
through it `ddl.Host`), `ddl.TypeStaging`, type and collation resolution, the
table and sequence stage slots, relation resolution, the binders
`BindColumnDefaults`, `BindGenerations`, `BindCheckExpression`,
`OperationDefinition` and `LiveEpoch`. The `TableHost` of
[`tablecmds`](../tablecmds/README.md) (`table_host.go`) implements it; `TypedResolver` is the part core calls, on `DDLHost`. `Resolver` and `RestoreResolver` are its stage-less parts.

`AlterHost` adds one ALTER statement's bindings: the live `Input` (captures
mutate it, so read it again before each binding), snapshot captures, the
binders `BindDefault`, `BindGeneration`, `RebindCheck`, `BindAlterTypeUsing`,
`TypeChangeDependents` and `StageIdentitySequenceOptions`.
`tablecmds.AlterHost{TableHost; Scope}` (`alter_host.go`) implements it, one
per call, because the ALTER loop begins its stage lazily.

**Every `Host` and `AlterHost` method runs with `Engine.Mu` held, never takes or
releases it, and never waits.** The authority is an opaque `ddl.Authority`, so
code here re-reads `AccessView` after every `Recapture`. `DropDependencyReader`
is called with callers holding nothing; its comment marks the methods that wait
or take `Engine.Mu`.

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
  never rebased. The same endpoint fence runs under `Engine.Mu` at implicit
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

Orchestration ([`tablecmds`](../tablecmds/README.md)): `CreateTableLocked`,
`DropTableLocked`, `AlterTableLocked` with its pass loop, the lazy stage
(`authorizedStage`), read-only sequence-original admission
(`validateAlterSequenceOriginalsLocked`), and core's metadata publication
(`PublishAlterMetadata`) as the sole sequence installation/retirement owner before
the fresh physical stage (`finishAlterPhysical`), and the IF EXISTS no-op
(`SkipMissingRelationLocked`).
The statement handlers build the narrowed hosts under `Engine.Mu`; the row evaluation
template; lock admission and post-wait checks (`acquireDefinitionProviders`,
`admitInheritanceAlter`, `acquireRelationTarget` around `AdmitTarget`, ...);
identity sequence waits; the prepare host and prepared-drop consumers; the
DROP pipeline (`DropProvider`'s stage mode and lock waits, the prepared-drop
admission and the `consumePreparedDrop` replay into the stage, and the unlocked
controllers `ColumnDependencies`/`ExpressionDependencies` with their waits and
projection); Finish;
VACUUM's lock loop, `Engine.Mu` windows, retry, install and progress map;
the binder bodies; and the Restore hook bodies (authorization, the `Engine.Mu`
window, trust checks, `PublishRestoreRepairLocked`, `restore_replay.go`). The
inheritance Restore repairs (CHECK recipes, generation pairing, retired
origins, inherited foreign keys and owners) are
[`partitioncmds`](../partitioncmds/README.md)'s.

Permanent CREATE/DROP and the shipped ALTER forms consume the owner's private
catalog/storage and the ordinary journal. The engine claims actual destination
and companion names, holds ordinary DROP roots through transaction end, and
recaptures exact identities after waits before publishing their candidates.

## Invariants

- ALTER TABLE keeps its provider-phase order, deferred misc and
  per-node re-entry, so every action list reports the same first error.
  Definition preparation uses ordered provider results once. Canonical checked
  metadata enters the real journal before callbacks; fresh physical successors
  retain old tuple layouts and original fences. Final registered providers run
  once after the complete physical demand, without rebasing pending stages.
  Column statistics candidates run in misc against the completed shape, without
  heap work. Targets recurse unless ONLY; column option SET/RESET never recurse.
  Whole option transformations validate privately before descriptor publication.
  Column options reuse relation-option semantic text coercion: scanner INT4
  values become decimal text, while quoted strings and Float-node spellings
  remain intact. SET completes source-ordered namespace and structural-name
  transformation checks across the entire list before validating parameter names,
  duplicates, or numeric values. RESET keeps its independent name-removal and
  value-rejection rules.
- Constraint order: index attachments stage first; physical builds follow index
  declaration order, then CHECK scans in declaration order. Ordinary DML
  enforces NOT NULL first and CHECKs in constraint-name order. TYPE rewrites
  check each converted row before converting the next. Everything validates
  before publication; failure leaves identities, constraints, rows and roots
  intact.
  Scheduled CHECK scans reselect their original name/ordinal and assigned OID
  from the authentic final descriptor, retaining cooked
  provenance. Descriptor name order is separate from scheduled execution order.
- Dump output is byte-stable: `DumpRelation` emitters keep their order and
  never iterate maps into output; each Restore syntax parser stays next to the
  emitter whose output it reads.
- Restore replays separate ADD CHECK and DEFAULT statements through normal
  binding, ownership, propagation, row validation and cancellation, never a
  replay marker, and never promotes NOT VALID constraints.
- Separate exclusion replay admits qualified single private constraint
  declarations, including recursive captured declarations and temporary-parent
  seed declarations restricted to their reserved scratch table/name shape.
  Scalar keys/predicates retain the ordinary replay validator; execution still
  uses normal constraint binding, checks and atomic catalog publication.
- The package holds no engine state and no mutex. Functions may call the
  reader's waiting controllers only with no engine/session mutex held; the root
  owns admissions, mutex windows and publication.

`pg_get_expr` accepts checked bound-expression and frozen-policy tree carriers.
Policy display delegates to the stored query renderer (including correlated
subqueries), with current path/output settings and no evaluation or target-table
ACL filtering. Missing nonzero relation identities return NULL; owner Vars with
zero relation identity retain the `expression contains variables` diagnostic.

## Testing

Package units run over a fake host or a small catalog; most table tests build an
engine and live in `internal/engine/tablecmds` (CREATE, ALTER and DROP TABLE,
constraints, sequences, identity and VACUUM), `internal/engine/partitioncmds`
(inheritance Restore repairs), `internal/engine/core` (FK
enforcement, check evaluation on writes, lock waits) or, until they move,
`internal/engine`, naming the code as `table.X`. Run the
inheritance and NOT NULL admission units with `RACE=1`. Public SQL lives in
[reference/tables](../../../reference/tables/README.md#testing),
[reference/inheritance](../../../reference/inheritance/README.md#testing),
[reference/partitions](../../../reference/partitions/README.md#testing) and ALTER
TABLE in [reference/alter](../../../reference/alter/README.md#testing); add
`REFERENCE=1` for the oracle.

```sh
make check RUN='^Test(TableHost|AlterHost|RelationValidatorsOrder|GeneratedRemapCheckCapture|TableTargetAdmission|AdmitTargetAsksEachEntry)'
make check RUN='^Test(CheckCreateShape|TypedSources|PlanAlterScan|ValidateAddedConstraintRows|RelationOptions|PrepareInheritanceNode|InheritancePendingGrants|InheritanceDropClosure|DefinitionProviders|Deparse(SerialSequence|MissingObjects)|ValidateSelectedDomainRows|OwnedSequenceName)'
make check RUN='^TestAlterPersistence'
make check RUN='^Test(DropPreparation|DropColumnRoutine|DropReaderSeam|InheritanceAlterAdmission|ReferenceAlterDropColumn)'
make check RUN='^Test(ForeignKey(TypeProviders|DefinitionProviders|Checks)|Scan(Restored)?ForeignKeys|RestoredForeignKeyTarget|RestoreInherited|AttributeRankRestore|RestoreConstraintHistory|RestoreConstraintModeAdmission|AlterConstraintInherit)'
make check RACE=1 RUN='^Test(NotNull|PhysicalNotNull|Inheritance)'
make check RUN='^TestPartitionRenameColumn'
make check RUN='^Test(Reference)?Vacuum'
make check RACE=1 RUN='^TestVacuum(PublicationFences|AuthorizationLocks)$$'
make check RUN='^TestReference(TableCore|TableLike|NotNull|Check|Inheritance|TypedTable)'
```
