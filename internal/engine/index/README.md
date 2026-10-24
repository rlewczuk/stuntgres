# internal/engine/index

Index records and algorithms: B-tree, hash and GiST keys and roots, unique and
exclusion enforcement, publication and snapshots, write-time maintenance,
operator-family and access-method DDL, and partitioned-index propagation.

## Imports

Layer L12 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture):
it imports lower engine packages (`analyze` for types and pure functions only),
`parser`, `sqlstate`, `sqltype` and the standard library. It never imports the
engine, `fdw`, `table`, `partition`, `view` or `dml`; they import it. It never
builds a binder.

## What it owns

- **Operator-family and access-method DDL** (`operator_family_*.go`,
  `access_method_ddl.go`): items resolve through the shared namespace, type,
  routine and operator authorities, validate the complete ordered list
  privately, then use the ordinary catalog stage and drop closure; class DDL
  shares this machinery. `catalog.OperatorFamily*` own metadata and
  `Read.FamilyOperator`/`Read.FamilySupport` are the only member lookup
  authority. Registered routine identity selects runtime operations;
  metadata-only callbacks are not callable grants. Validation dispatches on the
  physical implementation while keys keep the exact SQL method identity and
  diagnostics use the declared method name.
  The installed cube boundary formats type visibility from the captured catalog;
  unrelated input families retain their established DiagnosticName formatting.
- **Keys, roots and programs** (`index.go`, `hash*.go`, `gist.go`,
  `predicate.go`, `expression.go`, `program.go`, `clauses.go`, `options.go`,
  `property.go`): `BindIndexOrdering`, `BuildIndexRoot`, `MergeIndexRoot`,
  `BindHashIndex`, `BuildHashIndexRoot`, `MergeHashIndexRoot`, `BindIndexKeys`
  (GiST), `PrepareIndexRecipe`, `ResolveIndexMethod`, the clause and option
  validators, and `CaptureHashIndexesLocked` (published hash roots that exactly
  cover a plain table query source; caller holds `Engine.mu`).
- **Publication, snapshots and ALTER TYPE rebuilds** (`publication.go`,
  `row_publication.go`, `layout_*.go`, `snapshot*.go`, `remint.go`,
  `effective_root.go`, `alter_type.go`): `PrepareIndexPublication` over
  `RowPublication` (materialized by `PrepareRowPublication` outside `Engine.mu`
  over private roots), `ValidatePrivateExpressions`, `EvolveIndex*`,
  `CaptureLayouts`, `CaptureSnapshot`, `RemintStorage`, `EffectiveRoot` (a
  transaction's private root, else the committed one), `AlterIndexBuild` and
  `RebindExclusionOperators`. The engine fills capture inputs under `Engine.mu`
  after its epoch checks and holds the lock across the call.
- **Catalog publication captures** (`publication_stores.go`,
  `overlay_capture.go`): `CandidatesMatch` (the concurrent root-candidate
  fence of catalog publication and of overlay capture; the caller picks each
  parent's store), `CaptureStagedIndexStores` (the captured stores, including
  staged indexes' parents), `PublicationBuildStores` (build stores, rebuild
  set and source roots over a `storage.Publication`) and
  `SelectOverlayCapturesLocked` (the indexes a transaction overlay rebuilds,
  with their read fences). They take no lock; the engine calls them under
  `Engine.mu` and keeps the fence order and the `Conflict` it returns.
- **Write-time records and enforcement** (`write_candidate.go`, `insertion.go`,
  `unique*.go`, `exclusion_*.go`, `constraint.go`): `WriteCandidate`,
  `UniqueWriteBinding`, `UniqueKeyHash` (the opaque key the engine pins in the
  lock manager), `ValidateUniques`, `ValidateExclusions`, the exclusion
  projection/event records, and `UniqueKeyDetailAllowed` (key disclosure over
  `access.View`; a nil environment means trusted publication).
- **DDL records and presentation** (`control.go`, `metadata.go`,
  `maintenance_*.go`, `set_constraints.go`, `using_index*.go`): `DefinitionSQL`
  (`pg_get_indexdef` and Dump), `Metadata`/`IndexesMetadata`, the
  `MaintenanceGrant` lock plan, SET CONSTRAINTS name selection and the
  `USING INDEX` records.
- **Partitioned indexes** (`partition_*.go`, `row_history.go`): coverage,
  ATTACH checks, drop actions (`PartitionConstraintDropActions`,
  `DroppedUniqueOIDs`), `MapPartitionIndex` and Dump replay predicates.

## The DDL host

`Host` serves CREATE INDEX, ALTER/DROP INDEX, REINDEX and constraint-index
staging for one statement: `ddl.Host` plus object removal, diagnostics, the
stage and its index slot, `ACLOutput`, `DefinitionReader`, `ValidityTarget` and
the binders `BindIndexExpression`/`BindIndexPredicate` (the engine builds
binders; this package never does). The engine's `ddlHost` (`index_host.go`)
implements it. **Every method runs with `Engine.mu` held, never takes or
releases it, and never waits.** Narrower contracts:

- `KeyHost`: key binding and operator class lookup, no stage (the analysis host
  and partition key binding pass a stage-less `ddlHost`).
- `DropStage` (`*CatalogStage`): DROP INDEX staging, which runs without
  `Engine.mu`; the removal takes it.
- `ValidityTarget`: the partitioned-index validity pass. `UsingIndexStage`:
  attaching a prepared USING INDEX, no lock (`PrepareUsingIndex` takes only a
  `ddl.Allocator` and runs under `Engine.mu`).

Entry points:

- CREATE INDEX: `PrepareCreate` (authority, privilege, method, clauses,
  predicate, name, unique providers, coverage; `CreatePlan.Skip` for IF NOT
  EXISTS), then `StageCreate`.
  Ordinary creation in explicit transactions uses that same path, including
  UNIQUE and shipped methods/options. The engine captures the owner-private
  parent and current execution authority under its canonical Share grant.
  Names/descriptors/dependencies and candidate roots remain private until the
  existing combined DDL/DML publisher commits; no live publication/undo path
  exists. Baseline candidates exclude own writes, whose frozen `IndexOutputs`
  contribute once during enforcement/publication. Read-only and concurrent
  transaction restrictions remain engine execution-admission responsibilities.
  For unnamed ordinary creation the engine also passes an owned snapshot of
  other owners' granted relation-name claims, captured outside engine/session
  mutexes. `PrepareCreate` includes these names in its existing generated-name
  predicate without exposing other private definitions. Named explicit
  post-capture catalog uniqueness errors, including IF NOT EXISTS, propagate
  unchanged; preexisting-name behavior and autocommit recovery stay distinct.
- DROP INDEX, ALTER INDEX SET/RESET, ATTACH PARTITION and REINDEX:
  `StageMaintenance`, `ValidateMaintenance`; rename delegates to the shared
  relation rename owner. Ordinary DROP/rename/options use the existing private
  Journal and combined DDL/DML publication path in explicit and retained
  transactions. REINDEX retains its separate transaction boundary.
  `TransformIndexOptions` owns immutable logical option validation/ordering;
  metadata-only edits retain identity/providers/key roots and do not rebuild
  pages or change enforcement. Binding: `LookupClass`, `BindKey`,
  `BindAttributes`, `RejectVirtualUses`.
- Constraint indexes, called by `table`: `StageUniqueDefinitions`,
  `StageCreateUniques`, `StageExclusion`, `StageCreateExclusions`,
  `RebuildAlter{Uniques,Exclusions}`, `CaptureUsingIndexes`,
  `PrepareUsingIndex`, `StageUsingIndex`. Callers own the table edges: LIKE
  sources before unique staging, NOT NULL rebuild between the unique and
  exclusion rebuilds, primary-key NOT NULL before `PrepareUsingIndex`.
- Partitioned indexes: `StagePartitionIndex*`, `PartitionIndexValidity`,
  `StagePartitionConstraintDrop`.
- `Property`: `pg_index_has_property`, `pg_index_column_has_property`.

## The write host

`WriteHost` (implemented by `*statementHost`, `statement_host_index.go`) wraps
write-time unique and exclusion enforcement in `Engine.mu` windows and lock
waits. **Each method either takes `Engine.mu` itself for one window and never
waits (the caller must not hold it), or waits with the caller holding nothing;
none takes a callback or evaluation context.**

- Windows: `UniqueParent`, `BindingAuthority` (stale means rebind),
  `CaptureUniqueProbe`, `ExistingIndexRoot`, `CaptureExclusionScan`,
  `ExclusionScanCurrent`, `PublishExclusion` (epoch check, row identity, edit
  and install in one window; takes the candidate by value and returns the
  reserved row for `WriteCandidate.RecordExclusion`, so no candidate escapes),
  `DiscardExclusion`, `OwnExclusionKeys`, `AdmitExclusion`, `EndExclusionRow`.
- Waits: `ClaimUniqueKey` (pin, claim, `replacementMu` release and
  re-acquire, program readmission) and `WaitTransaction`.

Probe and scan captures return the write authority read in the same window as
an `access.View` by value; the engine rebuilds an access environment from it
only for key disclosure on error paths and the ON CONFLICT binding check.

Pure decisions over captured values: `BuildUniqueWriteBinding`,
`UniqueRootStart`, `ExistingRootOutput`, `VisitExclusion` (committed-root and
pending passes; asks its context source for a context per comparison),
`ExclusionDeferred` and the projection edits (`ExclusionEdit.Apply`,
`ExclusionProjection` methods, `ExclusionShapeEntries`). Each edit works on a
`ProjectionLocked` copy and replaces slices and maps, so an installed
projection never changes.

`BuildUniqueWriteBinding` also captures `ConstraintEffectsRelevant` for incoming
and outgoing FKs or exclusion obligations, using its existing descriptor walk.
FK validity does not disable new-write enforcement, and incoming references may
use standalone unique indexes. This classification belongs to the binding's
exact `Ref`/`Parent`/`Catalog` authority. Existing per-leaf insertion groups retain
it across routed rows; a changed reference, parent or catalog root requires rebind.

## State slots and registries

- **Registries.** `WaitRegistry` (CONCURRENTLY build waits) and
  `ExclusionRegistry` (each open lock owner's dirty exclusion projection plus a
  generation) have no mutex: `Engine.mu` guards them and every method has the
  `Locked` suffix. The engine holds one of each (`Engine.indexWaits`,
  `Engine.exclusions`). Every projection change advances the generation, so a
  scan captured with `ScanLocked` fails `CurrentLocked` and is retaken; the maps
  stay private so nothing changes a projection without advancing it.
- **Journal slot.** `Journal` implements `session.IndexSlot` (private logical
  roots, candidates, REINDEX intents); `Clone` copies its maps while sharing
  immutable roots and candidate pins. Savepoint marks retain older journals.
  Rollback-to restores the catalog/row prefix and lock checkpoint; full rollback
  discards private index names, descriptors, dependencies and roots.
  DROP removes only selected private descriptors/dependencies/root candidates;
  rename/options preserve key semantics. Dump sees committed state and replays
  persistent options with qualified ALTER after final names and attachments,
  including indexes declared by constraints, without broadening CREATE syntax.
- **Savepoint exclusion mark.** `ExclusionMark` implements
  `session.ExclusionMarkSlot`; `ExclusionMarkSlotOf` keeps zero state as a nil
  slot, so most savepoints allocate nothing.
- **Statement and stage slots.** `Scope` and `Stage` are the slot records; the
  accessors on `StatementScope` and `CatalogStage` stay in the engine.

## Invariants

- **Immutable roots.** Roots are immutable ordered entries or equality buckets,
  maintained at the same Finish/commit frontier as rows. Derived keys evaluate
  at Execute, after predicate qualification and before rows are installed;
  failed batches, waits and builds preserve predecessor rows and roots.
  Maintenance rebuilds roots atomically without changing rows or identities.
- **Class identity.** Operator classes resolve through current catalog
  descriptors, exact method OID and real family members; the physical method
  enum is not class identity. Declared identity compares with
  `sqltype.IndexClass.SameIdentity`, not structural equality. Grouping
  compatibility is captured once in the ordering's key mask, never per row.
- **Family members.** Required members keep their INTERNAL class owner; the
  drop closure rejects their standalone deletion and redirects dependencies
  through the class. Optional, cross-type and classless members stay loose.
- **Exclusion events.** Deferral events enlist only actual potential conflicts.
  Savepoints retain immutable event/mode/projection snapshots; transient modes
  and events are never serialized.
- **Partitioned exclusion.** Each declaration covers every partition key of its
  target and descendants with actual equality; virtual (partitioned) indexes own
  metadata only and leaf providers enforce conflicts. CREATE and EXCLUDE
  ADD/DROP reuse staged recursive index production.

Snapshot `Transport`/`TransportRows`, `RemintStorage` and
`TransportEmptyIndexStorage` consume the actual `catalog.Read`, not an ownerless
view reconstructed from its state. Imported and generation-rebound keys require
the same registered installed-provider authority as ordinary binding; transport
does not infer a stock provider from a name or retained discriminator. The
caller supplies its existing owner with the candidate state (`WithState`).
Captured keys, source rows and index expressions remain immutable; this changes
authority retention, not predicate/expression evaluation during transport.

## What stays in the engine

Orchestration: the `Engine.mu` and capture windows, CONCURRENTLY phases, lock
admission (`acquireIndexMaintenance` over the grant plan), unique key pins, the
registry instances, savepoint capture/restore, Finish order, publication and
Restore storage (`prepareIndexCatalog`), and `executeCreateIndex`/
`executeIndexMaintenance`. Thin wrappers build a `ddlHost` for production
callers; tests call `index` functions directly with one. The write-time loops
(unique probe/claim, exclusion probe/admission, ON CONFLICT arbiters) are
`dml`'s and reach this host through `dml.Host`.

## Testing

Package units cover column capabilities, `EffectiveRoot`, the savepoint
exclusion mark, access-method properties and operator-family validation; most
index tests build an engine and stay in `internal/engine`. Run concurrency,
exclusion and unique-claim selections with `RACE=1`. Private units assert
support IDs/routines and root membership, never ORDER BY results as evidence of
index use. Public SQL: [reference/indexes](../../../reference/indexes/README.md#testing)
(add `REFERENCE=1` for the oracle).

**Frame names matched by tests.** `TestHashIndexMergeCancellation` matches
`.MergeHashIndexRoot` and `TestSerializablePublicationRealChangedPredecessorRetry`
matches `.MergeIndexRoot`. Rename those functions only together with the
fixtures.

```sh
make check RUN='^Test(Index|EffectiveRoot)'   # keys, clauses, properties, maintenance, hosts
make check RUN='^Test(CandidatesMatch|DriverBatch|PartitionAttachPublicationFences)'   # catalog publication captures
make check RACE=1 RUN='^Test(Unique|Exclusion|UsingIndex|ConcurrentIndex|HashIndex)'   # enforcement
```

Operator families, classes and access methods:

```sh
make check RUN='^Test(OperatorFamily|OperatorClass|AccessMethod|BuiltinRoutineResultInventory)'
make check RACE=1 RUN='^TestOperatorFamilyPublication$'
make check RUN='^TestReference(OperatorFamily|OperatorClass|AccessMethod)'
```
