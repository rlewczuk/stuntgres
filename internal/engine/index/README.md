# internal/engine/index

Index records and algorithms: B-tree, hash, GiST and SP-GiST keys and roots, BRIN min/max
range summaries, unique and exclusion enforcement, publication and snapshots,
write-time maintenance, operator-family/access-method DDL and partition propagation.

## Imports

Layer L12 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture):
it imports lower engine packages (`analyze` for types and pure functions only),
`parser`, `sqlstate`, `sqltype` and the standard library. It never imports the
engine, `fdw`, `table`, `partition`, `view` or `dml`; they import it. It never
builds a binder.

## What it owns

- **Row-identity eligibility** (`replica_identity.go`) validates actual parent
  membership, uniqueness, actual constraint timing, expressions/predicate and physical
  key NOT NULL flags in release order. INCLUDE attributes and index validity
  do not add restrictions. ALTER owns the atomic parent/index replacement;
  immutable definition copies retain selection across TYPE and rename.
  Cross-parent LIKE/partition construction starts unselected; adoption copies
  the existing child's own definition rather than the parent's selection.
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
  These definitions share the canonical Journal/Finish path in autocommit,
  SQL/Go transactions and retained messages. Stagers do not wait under
  `Engine.Mu`; root admission owns exact provider/name locks and predecessor
  fences before publication. Private reads include earlier successful definitions.
  User-defined type diagnostics use the executing private catalog and current
  search-path visibility. Exact-default ambiguity names the domain's base type;
  missing-default diagnostics name the original requested type.
- **Keys, roots and programs** (`index.go`, `hash*.go`, `gist.go`,
  `predicate.go`, `expression.go`, `program.go`, `clauses.go`, `options.go`,
  `property.go`): `BindIndexOrdering`, `BuildIndexRoot`, `MergeIndexRoot`,
  `BindHashIndex`, `BuildHashIndexRoot`, `MergeHashIndexRoot`, `BindIndexKeys`
  (GiST and SP-GiST), `PrepareIndexRecipe`, `ResolveIndexMethod`, the clause and option
  validators, and `CaptureHashIndexesLocked` (published hash roots that exactly
  cover a plain table query source; caller holds `Engine.Mu`).
  BRIN uses `BindBrinIndex`, `BuildBrinIndexRoot`, `MergeBrinIndexRoot` and
  `BrinConsistent`; `BuildRelationIndexRoot` carries descriptor reloption intent
  into method dispatch. Summary support is not B-tree comparator support.
  GiST and SP-GiST roots share unsorted exact live membership extraction and
  immutable delta maintenance; scalar predicates and distances remain query authority.
  They contain original typed values, not bounding boxes or signatures.
  `pgstattuple.go` estimates tuple/page metrics from current logical roots and
  already-produced private outputs. Published logical removal stops contributing
  at once; pgstattuple dead index count is zero because roots have no killed-item
  marker. No archive,
  historical-key reconstruction or predicate/expression evaluation is required.
  Private outputs and current keys retain their normal compatible-generation
  transport; immutable saved roots remain usable. GiST root-only scans count no
  tuples, and hash reserves a metadata page and at least two estimated buckets.
  `ClassOptions` captures each key's explicit siglen independently; relation
  fillfactor/buffering and key siglen retain logical configuration without
  physical pages. Catalog inquiry and replay preserve explicit defaults and
  absent defaults distinctly. Column capabilities use the selected catalog's
  actual family ordering/fetch/compress support, not fixed class names.
  SP-GiST ordering members may match the class input on either operand; GiST
  retains its left-input and distance-support requirements. Both require a
  live ordering-purpose member and a sortable result family.
  SP-GiST keeps its distinct method/class identity, accepts one key plus INCLUDE
  and relation fillfactor only. Its seven classes preserve exact typed payloads,
  including polygon values despite BOX storage metadata. Distance eligibility
  uses actual ordering family members and their sortable result family; returnability
  uses SP-GiST compression metadata, never GiST callback slots.
  LIKE and partition copies retain immutable relation and per-key configuration;
  their roots are rebuilt against destination row and descriptor identities.
  Ordinary GiST and SP-GiST partitioned indexes use the same parent/child publication and
  attachment path as partitioned constraint indexes.
- **Compact B-tree statistics** (`pgstatindex.go`): `CapturePgstattupleStats` borrows
  one effective immutable root, parent versions/ends, commit frontier and actual
  private produced outputs. It validates row/version associations, counts resident
  live/dead memberships once and never reevaluates predicates or expressions.
  Removed committed memberships are absent, not invented dead heap history.
  The admitted attribute-only row-backed lane reads actual key slots directly.
  `CompactBtreeLayout` is a deterministic aggregate-packing approximation: each
  item weighs 20 bytes plus shared storage datum slots (NULL 0, positive fixed
  widths rounded to 8, variable slots 32), including INCLUDE attributes. Pages
  are 8192 bytes with 40 bytes overhead; leaves are ceil(weight/8152), internal
  fanout is 256. Metapage block 0 precedes leaves then bottom-up internal pages;
  the last block is root (one leaf uses block 1). Size is all pages times 8192.
  Empty captures use version 4, level/root 0, size 8192 and NaN density and
  fragmentation. Nonempty density is round2(100*weight/(leaves*8152));
  fragmentation, empty/deleted pages are 0. No retained page/split history is
  modeled, including DELETE-to-empty. Counts and inputs are exact; these page,
  byte, level, version and density outputs are explicitly the compact model,
  not physical inspection. Arithmetic is checked and loops honor cancellation.
- **Compact hash statistics** (`pgstatindex.go`): the shared capture samples the
  immutable effective hash root by summing bucket lengths, including duplicates.
  It never visits entries, heap rows or private projections. Live items are this
  simulated root sample and dead items are zero, not transaction-visible or
  LP_DEAD accounting. Private DML remains callable without forcing publication.
  `CompactHashLayout` uses version 4, four base bucket pages, one bitmap page,
  zero unused pages, 8152 usable bytes per page and 20 bytes per sampled item.
  With N items, P=max(4,ceil(N/407)); overflow=P-4 and free percent is
  100*(8152*P-20*N)/(8152*P). Checked arithmetic and bounded bucket cancellation
  require no per-entry allocation or physical page/split history.
- **Logical verification** (`amcheck*.go`): `CheckBtree` consumes an immutable
  `AmcheckCapture`, validates actual stored order/identity/positions and parent
  maps, searches the real baseline root independently, and checks demanded heap
  representation and visible uniqueness using its bound ordering and recipes.
  Capture pairs the baseline with coherent heap/end/frontier authority and
  real private row outputs or the admitted implicit row-backed lane. No root is
  rebuilt, repaired or published by verification. Uniqueness scratch is never
  verification-root authority. Engine orchestration owns scoped heap-then-index
  AccessShare (basic) or Share (parent) contributions and their reverse cleanup.
  Physical B-tree levels/downlinks/sibling pages, physical TIDs, WAL/LSNs and
  standby/recovery are non-engine mechanisms: logical verification neither
  fabricates them nor reports their physical invariants as checked. Private
  write overlays have no stored ordered tree to descend.
  Missing-heap diagnostics use the shared logical-version TID encoding; no
  physical index coordinates or LSN Detail are synthesized.
- **Publication, snapshots and ALTER TYPE rebuilds** (`publication.go`,
  `row_publication.go`, `layout_*.go`, `snapshot*.go`, `remint.go`,
  `effective_root.go`, `alter_type.go`): `PrepareIndexPublication` over
  `RowPublication` (materialized by `PrepareRowPublication` outside `Engine.Mu`
  over private roots), `ValidatePrivateExpressions`, `EvolveIndex*`,
  `CaptureLayouts`, `CaptureSnapshot`, `RemintStorage`, `EffectiveRoot` (a
  transaction's private root, else the committed one), `AlterIndexBuild` and
  `RebindExclusionOperators`. The engine fills capture inputs under `Engine.Mu`
  after its epoch checks and holds the lock across the call.
  Key reconstruction resolves the destination type's default within the declared
  method and rejects an absent default before replacing the captured class;
  it never publishes a zero class for a later provider to interpret.
  Constructors (including LIKE) and TYPE reconstruction use the allocation-free
  `ConstructorColumnStorage` leaf, also used by Dump to predict recreation.
  Every simple key/INCLUDE occurrence copies both destination column policies;
  expressions start with result-type defaults. Only an actual different opclass
  key type resets both policies to that type's default storage and empty compression;
  an explicit same-type key preserves the copied pair.
  Later ALTER policy changes intentionally update only the first simple match,
  without rebuilding executable roots.
- **Catalog publication captures** (`publication_stores.go`,
  `overlay_capture.go`): `CandidatesMatch` (the concurrent root-candidate
  fence of catalog publication and of overlay capture; the caller picks each
  parent's store), `CaptureStagedIndexStores` (the captured stores, including
  staged indexes' parents), `PublicationBuildStores` (build stores, rebuild
  set and source roots over a `storage.Publication`) and
  `SelectOverlayCapturesLocked` (the indexes a transaction overlay rebuilds,
  with their read fences). They take no lock; the engine calls them under
  `Engine.Mu` and keeps the fence order and the `Conflict` it returns.
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
  `Metadata.IndIsClustered` projects the single persisted
  `catalog.IndexDefinition.Clustered` scalar. Preference changes do not replace
  keys, executable providers or storage roots; table ALTER owns their atomic
  staged selection and uses `Property` for method eligibility.
  UNIQUE/PK timing remains on the parent constraint definition, not standalone
  index state. Metadata, LIKE, partition cloning and USING INDEX replay resolve
  the exact backing identity and retain those defaults; root validation still
  rejects duplicate committed keys regardless of initial timing.
- **Partitioned indexes** (`partition_*.go`, `row_history.go`): coverage,
  ATTACH checks, drop actions (`PartitionConstraintDropActions`,
  `DroppedUniqueOIDs`), `MapPartitionIndex` and Dump replay predicates.

## The DDL host

`Host` serves CREATE INDEX, ALTER/DROP INDEX, REINDEX and constraint-index
staging for one statement: `ddl.Host` plus object removal, diagnostics, the
stage and its index slot, `ACLOutput`, `DefinitionReader`, `ValidityTarget` and
the `TransformIndexExpression`/`TransformIndexPredicate` and matching `Complete`
methods (the engine builds binders; this package never does). `tablecmds.TableHost`
(`index_host.go`)
implements it. **Every method runs with `Engine.Mu` held, never takes or
releases it, and never waits.**

TEMP creation transforms its predicate, then expression keys, before consuming a
tablespace destination. It selects/checks that destination before access-method
lookup and physical-definition validation. `ExpressionTransform` holds analyzed
definition, input, dependencies and column uses only for this operation; completion
performs late virtual-use, coercion, volatility and rendering checks without
rebinding. Only completed recipes enter descriptors. Permanent admission order
and noncreation reconstruction semantics remain unchanged.

Narrower contracts:

- `KeyHost`: key binding and operator class lookup, no stage (the analysis host
  and partition key binding pass a stage-less core `DDLHost`).
- `DropStage` (`*CatalogStage`): DROP INDEX staging, which runs without
  `Engine.Mu`; the removal takes it. Its required relation-projection operation
  captures actual entering constraint identities before checked replacement,
  retaining records privately until successful catalog transfer. Each index
  retirement explicitly supplies its ownership direction: only index-driven
  removal marks a retired backing constraint NORMAL. Constraint-driven
  recursion does not promote descendants; SQL target selection alone marks
  ORIGINAL, independently of the expanded index admission/removal set.
- `ValidityTarget`: the partitioned-index validity pass. `UsingIndexStage`:
  attaching a prepared USING INDEX, no lock (`PrepareUsingIndex` takes only a
  `ddl.Allocator` and runs under `Engine.Mu`).

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
write-time unique and exclusion enforcement in `Engine.Mu` windows and lock
waits. **Each method either takes `Engine.Mu` itself for one window and never
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

Deferrable UNIQUE/PK reuse this projection/admission machinery with their real
equality ordering and NULL semantics, not synthetic EXCLUDE definitions or eager
exclusive key claims. Equality-root visits use the bound lower key boundary.
Only potential conflicts enlist exact row/version events; uncontested first
admission gets no retrospective obligation from a later peer. Due rechecks use
current committed and private/dirty visibility and the existing completion
tickets, then revalidate captures after every wait.


`BuildUniqueWriteBinding` also captures `ConstraintEffectsRelevant` for incoming
and outgoing FKs, exclusion and deferrable-unique obligations in its descriptor walk.
FK validity does not disable new-write enforcement; unenforced FKs supply no
effect obligation. Incoming references may use standalone unique indexes.
This classification belongs to the binding's
exact `Ref`/`Parent`/`Catalog` authority. Existing per-leaf insertion groups retain
it across routed rows; a changed reference, parent or catalog root requires rebind.

## State slots and registries

- **Registries.** `WaitRegistry` (CONCURRENTLY build waits) and
  `ExclusionRegistry` (shared dirty UNIQUE/PK/EX index projections plus a
  generation) have no mutex: `Engine.Mu` guards them and every method has the
  `Locked` suffix. The engine holds one of each (`Engine.IndexWaits`,
  `Engine.Exclusions`). Every projection change advances the generation, so a
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
- **Savepoint constraint mark.** `ExclusionMark` implements the incumbent
  `session.ExclusionMarkSlot`, retaining `ConstraintModes` and the shared dirty
  projection. Its singular event vector carries concrete index and FK payloads;
  defaults, ALL and per-OID modes are independent of persistent descriptors.
  `ExclusionMarkSlotOf` keeps exactly zero state nil. `Scope.AppendConstraintEvent`
  detaches an inherited event vector before adding the command's owned suffix.
- **Statement and stage slots.** `Scope` and `Stage` are the slot records; the
  accessors on `StatementScope` and `CatalogStage` stay in the engine.

## Invariants

- **Immutable roots.** Roots are ordered entries, unsorted GiST/SP-GiST memberships,
  equality buckets or logical range summaries, maintained at the same
  Finish/commit frontier as rows. Derived keys evaluate
  at Execute, after predicate qualification and before rows are installed;
  failed batches, waits and builds preserve predecessor rows and roots.
  Maintenance rebuilds roots atomically without changing rows or identities.
- **Class identity.** Operator classes resolve through current catalog
  descriptors, exact method OID and real family members; the physical method
  enum is not class identity. Declared identity compares with
  `sqltype.IndexClass.SameIdentity`, not structural equality. Grouping
  compatibility is captured once in the ordering's key mask, never per row.
- **Family members.** `InternalOwner` is the insertion-time class or family
  address, not a current-class search. Required GiST support1,2,5,6,7 belongs to
  the creating class or, for ALTER FAMILY, to the family; SP-GiST support1–5
  follows the same hard-ownership rule. Their operators and optional support stay
  loose. Btree/hash retain required same-type versus
  optional/cross-type rules, including physical403/405 ALTER lookup rather than
  SQL-alias adoption. The common closure rejects standalone hard-member deletion
  and promotes dependencies through the actual owner. Dump folds class-owned
  members into CREATE CLASS and emits family-owned members as ALTER FAMILY.
- **Exclusion events.** Deferral events enlist only actual potential conflicts.
  Savepoints retain immutable event/mode/projection snapshots; transient modes
  and events are never serialized.
- **Partitioned exclusion.** Each declaration covers every partition key of its
  target and descendants with actual equality; virtual (partitioned) indexes own
  metadata only and leaf providers enforce conflicts. CREATE and EXCLUDE
  ADD/DROP reuse staged recursive index production.
  Compatible unowned exclusion providers are adopted without changing identities,
  owned operators, timing or physical roots. Table ATTACH requires valid matching
  constraint-backed providers; recursive ADD can retain an invalid virtual subtree
  without healing its gaps. Explicit matching index ATTACH is idempotent and
  preserves diagnostic precedence for genuine mismatch and earlier ancestry errors.
  Exclusion `NoInherit` is declaration provenance, independent of current locality:
  local declarations start true, fresh inherited providers false, and attachment,
  detachment and immutable copies preserve it.
- **BRIN ranges.** A storage version-vector slot is one logical page; range
  ordinal divides the actual publication slot by the root's effective width.
  Summarized keys retain conservative independent extrema and NULL flags.
  Missing summaries always admit candidates and every candidate needs SQL
  recheck. Current `autosummarize` closes previous occupied unsummarized ranges
  on boundary crossing. SET/RESET width changes intent, not existing partitions;
  REINDEX/layout rebuild takes current intent. Destination owner repair rebuilds
  before locked remint; exact slot-preserving transport does not replay SQL.
  VACUUM rebuilds conservative destination summaries from its slot manifest and
  produced source summaries, propagating unsummarized contributors.
  Baseline transport consumes the caller's shared row-position map rather than
  allocating another map per BRIN index.
  See [BRIN design](../../../docs/design/PG18-INDEX-BRIN.md).

Snapshot `Transport`/`TransportRows`, `RemintStorage` and
`TransportEmptyIndexStorage` consume the actual `catalog.Read`, not an ownerless
view reconstructed from its state. Imported and generation-rebound keys require
the same registered installed-provider authority as ordinary binding; transport
does not infer a stock provider from a name or retained discriminator. The
caller supplies its existing owner with the candidate state (`WithState`).
Captured keys, source rows and index expressions remain immutable; this changes
authority retention, not predicate/expression evaluation during transport.

## What stays in the engine

The statement executors (CREATE INDEX with its CONCURRENTLY phases, ALTER
INDEX, DROP INDEX, REINDEX) are [`indexcmds`](../indexcmds/README.md)'s. Core
keeps the runtime: the `Engine.Mu` and capture windows, lock admission
(`acquireIndexMaintenance` over the grant plan), unique key pins, the registry
instances, savepoint capture/restore, Finish order, publication and Restore
storage (`prepareIndexCatalog`). Family code builds a `tablecmds.TableHost`
for production callers; tests call `index` functions directly with one. The
write-time loops
(unique probe/claim, exclusion probe/admission, ON CONFLICT arbiters) are
`dml`'s and reach this host through `dml.Host`.

## Testing

Compact statistics layout arithmetic and cancellation units use
`RUN='^TestPgstat(index|hashindex)Approximation$'` from `enginetests/`.

Package units cover column capabilities, `EffectiveRoot`, savepoint exclusion
marks, access-method properties, operator-family validation and BRIN summaries.
Index tests build an engine and live in `internal/engine/indexcmds` (index DDL),
`internal/engine/core` (unique and exclusion probing), `enginetests/` or, until
they move, `internal/engine`. BRIN structural and composed lifecycle units live
in `enginetests`. Their real callable/partial fixtures observe domain CHECK
execution through sequence state: VACUUM and same-ID generation transport must
not replay recipes, while admitted replacement repair produces destination
truth once before locked remint. Replacement also forces retained TEMP identity
collisions and checks later summary maintenance. Publication inspects both
rollback isolation and the root installed by a successful private-write commit.
SP-GiST structural coverage includes destination-domain ownership and exact
RowID/VersionID/position/value membership after real Dump/Restore with a retained
TEMP relation collision. Source, removed target and subsequently replaced roots
remain immutable; later writes in either engine cannot alter the other engine.
Run concurrency,
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
make check RUN='^TestAmcheck'   # immutable verification, identity, capture and invocation environments
make check RUN='^Test(CandidatesMatch|DriverBatch|PartitionAttachPublicationFences)'   # catalog publication captures
make check RACE=1 RUN='^Test(Unique|Exclusion|UsingIndex|ConcurrentIndex|HashIndex)'   # enforcement
make check RUN='^TestBrinIndex'   # range summaries, consistency, publication and transport
make check RUN='^TestSPGiST'   # genuine method, membership, publication and destination transport
```

Operator families, classes and access methods:

```sh
make check RUN='^Test(OperatorFamily|OperatorClass|AccessMethod|BuiltinRoutineResultInventory)'
make check RACE=1 RUN='^TestOperatorFamilyPublication$'
make check RUN='^TestReference(OperatorFamily|OperatorClass|AccessMethod)'
```
