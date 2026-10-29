# internal/engine/typedef

User-defined type support that does not need the engine: domains, enums, ranges,
composites and typed tables, collations and conversions. It owns the records,
validation, Dump/Restore and remap helpers, the DDL stage handlers (which reach
the engine through `StageHost`), the type dump statements (rendered through
`dump.Presenter`), the ALTER ENUM/DOMAIN resolvers, the native domain replay
walk and the scalar-type import of catalog replacement.

## Imports

Layer L9 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture). It
imports only `access`, `authorization`, `catalog`, `ddl` (the shared host
contract), `dump`, `expr`, `ids`, `pgcatalog` (collation versions), `session`
(journal slot interfaces, notice levels), `storage`, `parser`, `sqltype`,
`sqlstate` and the standard library, never the engine, `analyze`, `exec` or
`routine`.

## What it owns

- **Array storage admission** (`types.go`): `StoredArrayElement` validates concrete
  containers and storage-specific exclusions, then reuses sqltype's scalar array
  element policy, including the concrete dynamic ltree/lquery families and their
  supported domain elements. Internal CHAR and TID scalars and arrays share
  ordinary declaration, storage and replay paths.

- **Stage handlers** (`collation_ddl.go`, `conversion_ddl.go`, `domain*.go`,
  `enum.go`, `range*.go`, `composite.go`, `typed_type_drop.go`): CREATE/ALTER/DROP COLLATION
  and CONVERSION, CREATE/DROP DOMAIN, domain RENAME/SET SCHEMA, CREATE TYPE AS
  ENUM/RANGE/composite, and general type identity/DROP, as `Stage...Locked(h, auth, statement)`.
  `StageAlterEnumLabelLocked` and `StageAlterDomainLocked` stage one ALTER TYPE
  ... VALUE or ALTER DOMAIN action and return an edit record; the executors stay
  in the engine.
  Range creation stages both type/array identities and five genuine bootstrap-owned
  generated constructors in the range's creation namespace, even when its
  companion type starts elsewhere. Their finite Type/Form declaration is
  type-owned, not a builtin alias or SQL-text body; later type identity changes
  refresh captured generations without renaming or relocating those routines.
  `StageHost.RangeClass` selects genuine catalog support in the engine;
  typedef does not import routine binding.
- **ALTER resolvers** (`alter_resolve.go`): `ResolveAlterEnumLocked` and
  `ResolveAlterDomainLocked` resolve the target in diagnostic order (existence,
  kind, ownership; ALTER DOMAIN reads the reference path before the kind check)
  over `ddl.Host`, using only `SearchNamespaces` and `AccessView`.
  TYPE and DOMAIN identity stages share lookup, authorization and collision
  policy. Pure domain identity edits preserve the executable analysis stamp via
  `StageHost.ReplaceDomainIdentity`; OWNER remains the role package's authority.
- **Collations and conversions** (`collation_*.go`, `conversion_*.go`): SQL names,
  provider validation, the import remapper, the builtin conversion routine table
  and seed constructor `BuiltinConversionProcedures`, and the bootstrap seed dump
  statements.
  Their shipped CREATE/ALTER/DROP forms use the engine's canonical journal in
  autocommit, retained messages, SQL BEGIN and Go BeginTx. Owner-private lookup,
  provider authority and actual consumers share that catalog; commit publishes
  definitions with rows, while full/savepoint rollback restores identity,
  namespace, owner, version, handler and dependency state. FROM copies retain
  independent collation capabilities; REFRESH VERSION changes the recorded
  version, not dependent indexes. ALTER records its original descriptor with
  `AddDomainBase`, including no-op actions, independently of DROP target roots.
  Native replacement fencing requires full rollback and a fresh transaction;
  old savepoints cannot recover that fence.
  `PrepareCreateCollationLocked` performs namespace/CREATE authorization,
  option and provider-specific validation, FROM resolution and requested-encoding
  normalization before catalog ShareRowExclusive admission. An existing
  same-name, same-encoding definition errors or emits IF NOT EXISTS's skip notice
  in this phase, without waiting for that lock. `StagePreparedCollationLocked`
  owns completion for both admitted execution and effect-free direct staging.
  It recaptures supplied authority onto the candidate catalog and restores only
  the captured destination's TEMP classification through the session-token and
  non-TOAST guard. Admitted execution supplies fresh post-wait authority; direct
  composition acquires no locks. Both retain captured namespace/source identity
  and current CREATE checks, including the role's database TEMP privilege,
  without publishing the private namespace or granting peers authority.
  Cross-encoding shadows then error or skip with the generic duplicate message;
  remaining provider semantics, actual-version checks/warnings, allocation and
  insertion follow. A same-encoding key first committed during the wait is an
  exact unique-index `23505`, even with IF NOT EXISTS, not a serial duplicate or
  skip. `StageCreateCollationLocked` composes the same phases synchronously.
  Conversion CREATE checks handler EXECUTE and the registered six-argument
  integer-returning capability. Serial DEFAULT creation checks the visible
  schema/directed pair; legal moved or concurrent distinct-name defaults are
  not rejected by a global pair invariant. Runtime dispatch captures the owner's
  catalog once per statement, selecting by non-TEMP path then OID, without
  creating a stored dependency on the chosen conversion. Provider availability,
  encoding boundaries and conversion ABI remain provider-owned.
- **Domains** (`domain_*.go`): builtin `information_schema` seeds, ALTER DOMAIN
  discovery, own NOT NULL records, generation reownership, captured input
  providers and default snapshot records (qualified usage, never source OIDs).
  Concrete text-search alias domains receive executable associated arrays over
  the same domain generation, using the ordinary array admission checks.
  The Dump/Restore replay codec pairs each record with its INSERT;
  `RestoreDomainReplayNode` walks the replayed `SELECT` shapes over a
  `DomainReplayEnv` whose `Scalar` callback binds and evaluates the leaf, and
  `DomainReplayCells` checks each captured cell against its insert slot.
  Seg provenance is separate from domain-NULL provenance: only a checked
  destination-owned seg payload paired with its canonical literal is admitted.
  Domain NULL placeholders retain their existing exact-NULL and constraint rules.
  Exact temporal provenance is also separate: a top-level builtin timestamp or
  timestamptz precision 0..5 cell is admitted only when its finite canonical
  default-output text demonstrably loses precision under ordinary assignment.
  Its identical plain-string INSERT placeholder is reconstructed with unsized
  destination input, never interpreted as SQL. Session output settings, historical
  helper identity, domains and nested values confer no such authority.
  `WithoutDomainCheck` is the pure CHECK removal behind DROP CONSTRAINT and
  the DROP dependency passes.
- **Enums** (`enum_order.go`: sort-order insertion, `EnumSafety`) and
  **composites and typed tables** (`row_type.go`, `typed_shape.go`: row-type
  membership and metadata, array names, typed-table shape and inheritance plans).
- **Catalog replacement** (`scalar_import.go`, `typed_control.go`):
  `ImportScalarTypesLocked` gives an imported candidate's enum, `seg`, `hstore`, `query_int`, `lquery`, `ltree`, `ltxtquery`,
  `cube`, `citext`, `ean13`, `isbn`, `isbn13`, `ismn` and `ismn13` types their destination codecs, taking enum members from
  the engine's allocator callback in member order so Dump stays byte-identical;
  `ReownDomainTypes` rebuilds custom range families after their subtype, invoking
  the engine's range-ordering rebinder for destination class/support identities.
  Generated companions are reconstructed with their range, not independently.
  `CheckTemporaryTypedReplacementLocked` rejects a replacement while a TEMP typed
  table of the published state has a persistent OF type.
- **Dump statements** (`*_dump.go`): `CollationDumpStatements`,
  `ConversionDumpStatements`, `MixedDumpStatements` (domains, enums, ranges and
  composites in captured graph order) and the domain CHECK statements. They are
  pure functions of the captured view; declaration, DEFAULT and CHECK text comes
  from the caller's `dump.Presenter`, so the package never names the stored-SQL
  renderer or `regobj`.
  Mixed creation callbacks supply explicit private root/multirange coordinates
  without rebuilding executable type generations or changing the captured
  prerequisite view. After-create SQL remains part of the same declaration,
  before its existing ownership transfer and before the next graph consumer.
- **Associated-array replay** (`row_type.go`, `domain_replay_names.go`):
  `ChooseAssociatedArrayName` and its candidate helper are the sole forward
  naming rule, including UTF-8 clipping and numeric suffixes. `DomainReplayNames`
  models actual creation, association, relocation and retirement occupancy;
  captured final spelling is not proof that a name already exists in replay.
  Private range creation and ordinary domain/relation completion share that same
  model. Exact names are realized through real parent ALTER/domain displacement
  and ordinary scratch cleanup, never protected direct array ALTER or imported
  descriptor mutation.
- **Slot structs**: `DomainAlterStage` and `TypedStage` (the type state of a
  `CatalogStage`; `TypedFieldCheckOwner` says whether its admitted typed-table
  closure owns a table's inherited CHECKs), `DomainJournal` and `EnumJournal` (`journal_slots.go`, the type
  state of the catalog journal, implementing `session.DomainSlot` and
  `session.EnumSlot`), with `DomainWritable`/`EnumWritable` and
  `DomainJournalOf`/`EnumJournalOf`/`EnumSlotOf`.

## Type identity lifecycle

Identity ALTER preserves logical IDs and stored values for enums, domains,
standalone composites and installed seg/cube/query_int/lquery/ltree/ltxtquery/citext/ean13/isbn/isbn13/ismn/ismn13/earth/lo. Arrays follow
the reciprocal descriptor, never a guessed underscore name; standalone composite
backing relations remain reciprocal. Relation-owned rowtypes use their relation's
DDL instead. Rename may displace a genuine generated array; namespace moves must
admit both existing base and companion names atomically. OWNER reuses the existing
role/ACL authority and does not change unrelated contained-object owners.
TYPE rename checks ownership before protected-kind rejection. TYPE schema moves
check destination existence/CREATE and ownership before automatic-array
protection. Namespace-switch restrictions and destination base-name collisions
precede table-rowtype rejection; companion-name collisions follow it. A
same-schema move still rejects a protected rowtype without mutation. Array
ownership diagnostics identify the actual element.
`StageMoveTypeNamespace` reuses the type identity owner for extension relocation:
direct member ownership is checked before automatic-array/rowtype ignore, while
internal companion movement adds no public ALTER gates. A range move changes only
the requested type and its array; reciprocal multirange generation rebuilding
does not move that multirange or the generated constructor public identities.
No constructor acquires extension membership merely through its type dependency.
Direct catalog publication tolerates database maintenance-horizon drift only
when identity, revision, owner and exact ACL authority remain unchanged. It
composes against the current catalog root to retain the newest horizon; epoch,
session and role fences remain strict.

TYPE and DOMAIN drop share full-batch name/authorization preflight. Type or schema
ownership admits DROP; schema ownership alone does not admit ALTER. Explicit roots
receive actual internal/extension/pinned dependency protection before normal
closure discovery. A canonical companion may defer to its genuine pending
internal owner; that owner still faces its own protection. All explicit roots
remain in the shared closure for batch diagnostics and idempotent retirement.
RESTRICT reports genuine consumers; CASCADE prunes dependent columns and objects
while retaining unrelated columns, holder rows and zero-column table cardinality.
Current extension membership does not prohibit identity ALTER, but direct DROP
of a current member still requires its owning extension. Detached installed
objects retain genuine providers, not a hidden DROP restriction. Custom range/multirange families
reuse supported subtype providers; they do not introduce a general user-defined
base-type provider or change installed-array codec policy.

Held casts retain logical captures until normal effective-path/dependency
invalidation reanalyzes their original SQL; fresh lookup/metadata/diagnostics use
current names. DROP/CREATE at the same spelling is a different identity and cannot
retarget an old parameter/result signature. PostgreSQL may instead retain a
retired physical parameter OID and report a cache-lookup error. Dump rejects
unrepresentable explicit ACLs before emitting output.
The SQL authority is [ALTER TYPE](https://www.postgresql.org/docs/18/sql-altertype.html)
and [DROP TYPE](https://www.postgresql.org/docs/18/sql-droptype.html); public
proof belongs in [reference/domain_types](../../../reference/domain_types/README.md#type-identity-lifecycle).

The engine admits shipped domain/composite creation, domain rule changes,
composite attribute evolution and type/domain identity/drop through its canonical
transaction journal in autocommit, retained messages, SQL BEGIN and Go BeginTx.
Private definitions, constraints, consuming rows and logical keys commit or roll
back together, including savepoint recovery. Generic TYPE/DOMAIN OWNER retains its
separate transaction policy. These handlers stage decisions only; source authority,
historical scans and storage/index publication remain engine-owned.


## The stage host

The stage handlers run under `Engine.Mu` without a statement scope and reach the
engine only through `StageHost` (`stage_host.go`), which embeds
[`ddl.Host`](../ddl/README.md) and `ddl.TypeStaging` and adds the type methods.
The engine implements it on `TypeHost` (`typedef_host.go`), which embeds
`tablecmds.TableHost` for the type staging and row-type methods it shares with tables.
`BindDomainNotNull` takes only `ddl.Host`, so the engine's domain-check
binding passes the core `DDLHost`.

- **`ddl.Authority`** is the statement authority as an opaque value; handlers
  keep it by value. `CreationNamespace` may stage an implicit TEMP namespace and
  returns a new authority that reads the stage; the handler assigns it to its own
  `auth`, so later checks read the recaptured view.
- **`ddl.Stage`** is the private catalog change; the engine stage keeps its
  lifecycle checks. Methods that write engine-private stage state
  (`ReplaceUnsynchronized`, dependency recording, `RemoveDomainCheck`,
  `ReplaceDomainIdentity`) are host methods.
- Name and type resolution and domain default/constraint binding stay in the
  engine behind the host, because CHECKs need `analyze`.
  `BindDomainConstraints` installs definitions in declaration order; generated
  names see only earlier installed names.
- OIDs, revisions and enum members come from `AllocateOID`, `Revision` and
  `AllocateEnumMember` in the order the handlers ask.
- **Lock contract.** Every host method runs with `Engine.Mu` held; none takes or
  releases it, and none waits on the lock manager.

## Invariants

- **No locks, no engine state.** The package has no mutex and reads the catalog
  only through `catalog.Read`/`catalog.State`.
- **Lock contract of the decisions.** `ResolveAlter*Locked`,
  `ImportScalarTypesLocked` and `CheckTemporaryTypedReplacementLocked` run with
  `Engine.Mu` held and take no lock (the allocator callback runs under it).
  `RestoreDomainReplayNode` and `DomainReplayCells` run without `Engine.Mu`,
  because the engine's `Scalar` callback takes it to bind the leaf.
- **Slots are owned by the engine.** `CatalogStage` and the journal hold the
  slots; the engine allocates, recovers and writes them. A nil slot is the empty
  state, and the getters are nil-safe. Journal slots are copy-on-write: `Clone`
  makes a savepoint or statement copy, and a writer replaces a shared slot before
  changing it.
- **Pure seeds.** `BuiltinConversionProcedures` and `BuiltinDomainDefaultSeeds`
  return fresh values. `BuiltinDomainCheckSeeds` parses its declarations once
  per process and returns the same read-only constraints to every engine. The
  engine installs the seeds in `newEngine`'s order, which fixes their OIDs.
  Conversion seeds retain the exact released C symbol and dynamic-library path
  together in immutable routine display metadata, independent of public labels.
- **Domain identities.** ALTER keeps logical domain and array IDs; pure identity
  edits keep the executable analysis stamp while ordinary replacement
  invalidates it. Own NOT NULL definitions reserve real additional identities.
- **ALTER DOMAIN publication.** Validation observations (graph, storage,
  provider, owner, epoch) gate one immutable publication; cancellation before
  install leaves the roots unchanged.
- **Retained statistics replay.** The shared typed replay walker recognizes
  `statistics_tree` only with the core statistics adapter's capability and the
  exact current pg_node_tree descriptor. Domain/array wrappers recurse normally.
  This reconstructs immutable retained structural text without parsing SQL or
  granting domain-NULL/Seg row-sidecar provenance. Ordinary replay never supplies
  this capability and rejects the same node.

## What stays in the engine

[`typecmds`](../typecmds/README.md) holds the self-orchestrating executors
(`ExecuteEnumDDL`, `ExecuteAlterDomain`, `ExecuteAlterComposite`,
`AlterTableOfLocked`), which hold the lock windows, lock-manager waits, row
validation and publication; enum journal updates; and the native replay INSERT
(`domain_default_restore.go`, with the scalar leaf built by `DomainReplayEnv`).
The `TypeHost` methods are [`tablecmds`](../tablecmds/README.md#hosts)'s, and
the schema statements [`schemacmds`](../schemacmds/README.md)'. Core holds the TEMP preservation orchestrator;
publication-time reownership and domain index generations; TEMP collation
cleanup; and domain check programs and evaluation.

Elsewhere: type dump graph edges (`control.ColumnPredecessors`,
`regobj.DomainInvalidDumpEvents`), the `dump.Presenter` adapter (`control`),
`table.DomainDependency` and the type class validator `stored.TypeProvider`.

## Testing

Package units pin the seeds, replay pairing, the scalar leaf callback contract
(`TestRestoreDomainReplayScalarLeaf`), enum sort insertion, journal slot
copy-on-write, default snapshots and storage admission. Engine witnesses in
`internal/engine/schemacmds` cover the `TypeHost` side, ALTER DOMAIN
publication, the replay walk with the real leaf, the dump statements and
collation and conversion DDL; ALTER TYPE on typed tables and the
geometry domain replay in `internal/engine/typecmds`, CREATE TABLE OF in
`internal/engine/tablecmds`, and
runtime domain checks and type catalog descriptions in `internal/engine/core`.
Lifecycle units cover provider/companion ownership, pending-root protection,
retained immutable graphs, publication fences and replay-name cycles.
Family selector: `RUN='^TestTypeLifecycle'`. Range-type provider, bound,
multirange, constructor, alias-capacity, allocator, dump-authority and remap
tests run on a composed engine in `enginetests/`.

```sh
make check RUN='^TestRangeType(Parser|Providers|Bounds|Multirange|Atomicity|IdentityRemap|DependencyClosure|Adapter|Constructors|ExactEndpoints)$'
make check RUN='^TestRangeType(Alias|AllocatorFloor|GeneratedConstructorDumpAuthority)'
make check RUN='^Test(BuiltinConversionProcedures|BuiltinDomainSeeds|DomainDefaultReplayPairingInternal|DomainAlterDefaultReplayInternal|PGEnumSortInsertion|CatalogTypeJournalSlotsCopyOnWrite|RestoreDomainReplayScalarLeaf|MACADDR8?StorageAdmission)$$'
make check RUN='^TestDDLHost'
make check RACE=1 RUN='^TestDomainAlter(DefaultSnapshotsInternal|NotNullMetadataInternal|DefaultNullabilityPublication|ConstraintPublication)$$'
make check RUN='^Test(DomainDefaultReplay(Admission|DeferredChecks|RoutineManifest)Internal|CollationDumpLosslessBootstrapSelection|DumpPresenterDomainExpressions)$$'
```

Public behavior is covered by the domain, enum, composite, collation,
conversion, typed-table and Dump/Restore families in `reference/` (see
[reference/domain_types](../../../reference/domain_types/README.md#testing) and
[reference/types](../../../reference/types/README.md#testing)).
