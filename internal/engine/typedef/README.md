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

- **Stage handlers** (`collation_ddl.go`, `conversion_ddl.go`, `domain*.go`,
  `enum.go`, `range*.go`, `composite.go`, `typed_type_drop.go`): CREATE/ALTER/DROP COLLATION
  and CONVERSION, CREATE/DROP DOMAIN, domain RENAME/SET SCHEMA, CREATE TYPE AS
  ENUM/RANGE/composite, and general type identity/DROP, as `Stage...Locked(h, auth, statement)`.
  `StageAlterEnumLabelLocked` and `StageAlterDomainLocked` stage one ALTER TYPE
  ... VALUE or ALTER DOMAIN action and return an edit record; the executors stay
  in the engine.
  Range creation stages both type/array identities and five genuine generated
  constructor declarations atomically. The constructors live in the range's
  namespace even when its companion type lives elsewhere. Their finite native
  declaration is type-owned, not a builtin alias or SQL-text body.
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
- **Domains** (`domain_*.go`): builtin `information_schema` seeds, ALTER DOMAIN
  discovery, own NOT NULL records, generation reownership, captured input
  providers and default snapshot records (qualified usage, never source OIDs).
  The Dump/Restore replay codec pairs each record with its INSERT;
  `RestoreDomainReplayNode` walks the replayed `SELECT` shapes over a
  `DomainReplayEnv` whose `Scalar` callback binds and evaluates the leaf, and
  `DomainReplayCells` checks each captured cell against its insert slot.
- **Enums** (`enum_order.go`: sort-order insertion, `EnumSafety`) and
  **composites and typed tables** (`row_type.go`, `typed_shape.go`: row-type
  membership and metadata, array names, typed-table shape and inheritance plans).
- **Catalog replacement** (`scalar_import.go`, `typed_control.go`):
  `ImportScalarTypesLocked` gives an imported candidate's enum, `query_int`,
  `cube`, `citext` and `ean13` types their destination codecs, taking enum members from
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
- **Slot structs**: `DomainAlterStage` and `TypedStage` (the type state of a
  `CatalogStage`), `DomainJournal` and `EnumJournal` (`journal_slots.go`, the type
  state of the catalog journal, implementing `session.DomainSlot` and
  `session.EnumSlot`), with `DomainWritable`/`EnumWritable` and
  `DomainJournalOf`/`EnumJournalOf`/`EnumSlotOf`.

## Type identity lifecycle

Identity ALTER preserves logical IDs and stored values for enums, domains,
standalone composites and installed cube/query_int/citext/ean13/earth. Arrays follow
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
Installed membership does not prohibit identity ALTER, but
direct DROP still requires the owning extension. Custom range/multirange families
reuse supported subtype providers; they do not introduce a general user-defined
base-type provider or change installed-array codec policy.

Held casts retain logical captures until normal effective-path/dependency
invalidation reanalyzes their original SQL; fresh lookup/metadata/diagnostics use
current names. DROP/CREATE at the same spelling is a different identity and cannot
retarget an old parameter/result signature. PostgreSQL may instead retain a
retired physical parameter OID and report a cache-lookup error. Existing native
explicit-transaction and arbitrary explicit-ACL portability limits remain.
The SQL authority is [ALTER TYPE](https://www.postgresql.org/docs/18/sql-altertype.html)
and [DROP TYPE](https://www.postgresql.org/docs/18/sql-droptype.html); public
proof belongs in [reference/types](../../../reference/types/README.md#type-identity-lifecycle).

## The stage host

The stage handlers run under `Engine.mu` without a statement scope and reach the
engine only through `StageHost` (`stage_host.go`), which embeds
[`ddl.Host`](../ddl/README.md) and `ddl.TypeStaging` and adds the type methods.
The engine implements it on its one staging adapter, `ddlHost`
(`typedef_host.go`), which also backs `routine.DDLHost`.

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
- **Lock contract.** Every host method runs with `Engine.mu` held; none takes or
  releases it, and none waits on the lock manager.

## Invariants

- **No locks, no engine state.** The package has no mutex and reads the catalog
  only through `catalog.Read`/`catalog.State`.
- **Lock contract of the decisions.** `ResolveAlter*Locked`,
  `ImportScalarTypesLocked` and `CheckTemporaryTypedReplacementLocked` run with
  `Engine.mu` held and take no lock (the allocator callback runs under it).
  `RestoreDomainReplayNode` and `DomainReplayCells` run without `Engine.mu`,
  because the engine's `Scalar` callback takes it to bind the leaf.
- **Slots are owned by the engine.** `CatalogStage` and the journal hold the
  slots; the engine allocates, recovers and writes them. A nil slot is the empty
  state, and the getters are nil-safe. Journal slots are copy-on-write: `Clone`
  makes a savepoint or statement copy, and a writer replaces a shared slot before
  changing it.
- **Pure seeds.** `BuiltinConversionProcedures`, `BuiltinDomainCheckSeeds` and
  `BuiltinDomainDefaultSeeds` return fresh values; the engine installs them in
  `newEngine`'s order, which fixes their OIDs.
- **Domain identities.** ALTER keeps logical domain and array IDs; pure identity
  edits keep the executable analysis stamp while ordinary replacement
  invalidates it. Own NOT NULL definitions reserve real additional identities.
- **ALTER DOMAIN publication.** Validation observations (graph, storage,
  provider, owner, epoch) gate one immutable publication; cancellation before
  install leaves the roots unchanged.

## What stays in the engine

The `ddlHost` type methods (`typedef_host.go`) and schema dispatch; the
self-orchestrating executors (`executeEnumDDL`, `executeAlterDomain`,
`executeAlterComposite`, `alterTableOfLocked`), which hold the lock windows,
lock-manager waits, row validation and publication; enum journal updates; the
native replay INSERT (`domain_default_restore.go`, with the scalar leaf built by
`Engine.domainReplayEnv`); the TEMP preservation orchestrator;
publication-time reownership and domain index generations; TEMP collation
cleanup; and domain check programs and evaluation.

Elsewhere: type dump graph edges (`control.ColumnPredecessors`,
`regobj.DomainInvalidDumpEvents`), the `dump.Presenter` adapter (`control`),
`table.DomainDependency` and the type class validator `stored.TypeProvider`.

## Testing

Package units pin the seeds, replay pairing, the scalar leaf callback contract
(`TestRestoreDomainReplayScalarLeaf`), enum sort insertion, journal slot
copy-on-write, default snapshots and storage admission. Engine witnesses in
`internal/engine` cover the `ddlHost` side, ALTER DOMAIN publication, the replay
walk with the real leaf and the dump statements.
Lifecycle units cover provider/companion ownership, pending-root protection,
retained immutable graphs, publication fences and replay-name cycles.
Family selector: `RUN='^TestTypeLifecycle'`.


```sh
make check RUN='^Test(BuiltinConversionProcedures|BuiltinDomainSeeds|DomainDefaultReplayPairingInternal|DomainAlterDefaultReplayInternal|PGEnumSortInsertion|CatalogTypeJournalSlotsCopyOnWrite|RestoreDomainReplayScalarLeaf|MACADDR8?StorageAdmission)$$'
make check RUN='^TestDDLHost'
make check RACE=1 RUN='^TestDomainAlter(DefaultSnapshotsInternal|NotNullMetadataInternal|DefaultNullabilityPublication|ConstraintPublication)$$'
make check RUN='^Test(DomainDefaultReplay(Admission|DeferredChecks|RoutineManifest)Internal|CollationDumpLosslessBootstrapSelection|DumpPresenterDomainExpressions)$$'
```

Public behavior is covered by the domain, enum, composite, collation,
conversion, typed-table and Dump/Restore families in `reference/` (see
[reference/types](../../../reference/types/README.md#testing)).
