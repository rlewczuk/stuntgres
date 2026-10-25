# internal/engine/extension

The installed-extension layer that does not need the engine: provider records
and control data for the thirteen bundled extensions (amcheck, citext, cube,
earthdistance, fuzzystrmatch, intagg, intarray, isn, lo, pgcrypto, pgrowlocks, unaccent, uuid-ossp),
member validation, membership, the owner metadata codec used by native Dump,
extension remap, provider staging, and the decisions of the CREATE, ALTER
EXTENSION UPDATE and DROP EXTENSION handlers and of the Restore owner import.

## Imports

Layer L9 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports only `access` (privilege requirements and the captured view),
`authorization`, `catalog`, `ddl` (the shared authority and allocator), `expr`,
`functions` (kernels and member tables), `ids`, `parser`, `sqlstate`, `sqltype`,
`typedef` (the relation type name check of citext, cube and intarray staging)
and the standard library; never the engine, `analyze`, `routine` or `plan`.

## What it owns

- **Providers** (`*_provider.go`): one provider type per extension, each with
  `Control`, `Versions`, `ValidateExtension` (`catalog.ExtensionImplementation`)
  and, for providers with callable members, `ValidateProcedure`. The stock instances are
  `StockCitext` … `StockUUIDOssp`. The script contexts (`CitextTypeContext`,
  `CubeTypeContext`, `IntarrayInputContext`, `UnaccentTemplateContext`, …),
  signatures (`CubeSignature`, `CubeBinarySignature`, `QueryIntSignature`) and
  member tables (`FuzzystrmatchMembers`, `IntaggMembers`) are what staging needs.
- **Accessors** (`extension_implementation.go`): `CitextProviderOf` …
  `UnaccentDictionaryProviderOf` return a descriptor's implementation when it
  has that concrete type, and nil otherwise.
- **Routine identity**: `ProcedureSymbol(view, procedure)` checks current
  descriptor/address/index and typed owner provenance, installation/version,
  unique member addresses and versioned slot signature/kind/result/capability.
  Public name/namespace are presentation; `ProviderOf` alone is not installed
  authority. Supplied COW manifests must match current identity, version,
  revision, target namespace, owner and unique ordered inventory; pointer
  equality neither proves authority nor excuses corruption. An individual move
  changes no sibling or extension target. Fuzzystrmatch 1.1 omits slot-zero
  daitch_mokotoff; 1.2 inserts it while retaining incumbent addresses. Detached
  unaccent retains exact immutable metadata shape admission, not installed
  execution or replay authority; `ProcedureSymbol` rejects detached declarations.
- **Catalog validation** (`extension_catalog.go`): `CatalogProvider` validates
  the extension, text search template and dictionary classes and installed
  member procedures; `ValidateProviderHeader` is the identity check it shares
  with `stored.ProcedureProvider`; a descriptor's owning extension is
  `catalog.ExtensionMembership`. `extension_type_validation.go` validates an
  installed codec's array through the type provider registered in the catalog
  owner.
- **Cube members** (`cube_provider.go`, `cube_coordinates.go`, `cube_spatial.go`,
  `cube_spatial_call.go`): one atomic installation validates all 48 direct members.
  The comparison graph keeps its 18-member prefix; constructor/inquiry/coordinate
  and four transformation procedures and two coordinate operators precede the
  spatial procedure/operator tails. Starts and `CubeMemberCount` derive from the
  routine tables; each table
  retains its feature authority. Operators carry real extension membership and
  explicit backing-procedure indices independent of appended routine tails.
  Staging, validation and ownership import share these links. Symbolic owner signatures preserve every builtin
  formal, including `cube,1007` subset and `cube,23` coordinate signatures.
  Import resolves the expected
  destination member, not an arbitrary same-name object.
  Comparison support keeps its exact INTERNAL class address; a family owner is
  not an interchangeable execution capability. `RemapDescriptor` delegates class,
  family and member relocation to the typed catalog remappers, preserving the
  actual owner kind and immutable source descriptors.
- **intagg** (`intagg_identity.go`, `intagg_aggregate.go`):
  `ValidateIntaggIdentity`, `ProcedureSymbol`, `IntaggDefinition` and the
  owned-aggregate checks and remap (`plan.SameIntaggIdentity` compares captured
  identities).
- **amcheck** (`amcheck_provider.go`): the untrusted/superuser/relocatable 1.5
  provider installs one callable member, `verify_heapam`, with six canonical
  inputs and five trailing defaults, a non-STRICT volatile record-set result,
  parallel-unsafe declaration and explicit owner-only initial EXECUTE ACL.
  Exact manifest/signature/declaration authority survives ordinary member
  rename, move, owner transfer and symbolic replay. No scan state is serialized.
- **pgrowlocks** (`pgrowlocks_provider.go`, `pgrowlocks_stage.go`): the
  nontrusted/superuser/relocatable 1.2 singleton installs exactly
  `pgrowlocks(text) RETURNS SETOF record` (STRICT, VOLATILE, parallel safe).
  `ValidatePgrowlocksIdentity` checks actual owner-domain, epoch, descriptor and
  singleton manifest, not spelling. Owner metadata, transaction publication,
  dependency removal and remap use the shared extension lifecycle; no lock state
  is serialized.
- **isn** (`isn_provider.go`, `isn_stage.go`): the trusted, relocatable 1.3
  provider installs `ean13`, `isbn`, `isbn13`, `ismn`, `ismn13`, `issn`, `issn13` and `upc`, each with a
  reciprocal lookup-only associated array and immutable strict C-language codecs.
  Twenty-four append-only manifest slots bind `ean13_in(cstring)`/`ean13_out(ean13)`,
  `isbn_in(cstring)`/`isn_out(isbn)`,
  `isbn13_in(cstring)`/`ean13_out(isbn13)`,
  `ismn_in(cstring)`/`isn_out(ismn)`,
  `ismn13_in(cstring)`/`ean13_out(ismn13)`,
  `issn_in(cstring)`/`isn_out(issn)` and
  `issn13_in(cstring)`/`ean13_out(issn13)` and
  `upc_in(cstring)`/`isn_out(upc)` to their own scalars. The ISBN13,
  ISMN13 and ISSN13 input SQL identities have immutable source symbols `isbn_in`,
  `ismn_in` and `issn_in`; output overloads are
  distinguished by their own scalar signature. Owner metadata records
  each output argument symbolically and remaps it to its destination descriptor.
  Admission follows script order: input signature, output signature, then full
  type collision. An occupied type supplies only the existing output argument
  identity for collision detection; installation never adopts its descriptor.
- **citext and earthdistance.** Citext stages the scalar, two I/O routines,
  six strict immutable comparison routines and their six operators, plus the
  real strict `citext(bpchar)`, `citext(bool)` and `citext(inet)` conversions.
  Its eight strict immutable INTERNAL pattern procedures and sixteen operators
  accept `(citext,citext)` and `(citext,text)`. Their append-only slots preserve
  the storage/comparison/conversion prefix; paired negators retain exact routine
  and operand identities through symbolic owner metadata and import.
  Ten SQL-language regexp overloads occupy a bounded procedure range after the explicit
  pattern-operator bound: `regexp_match`, `regexp_matches`, `regexp_replace`,
  `regexp_split_to_array` and `regexp_split_to_table`, each with and without text
  flags. Their first two formals are citext; replacement and flags are text.
  Declaration metadata retains release argument names, strictness, immutability,
  parallel safety, set status and row estimates without granting execution.
  Shared regexp scalar/set hooks preserve original spelling and text/text-array
  results, default to insensitive matching unless flags contain `c`, and retain
  exact provider identity, current caller ACL admission and control remapping.
  Four subsequent SQL scalar slots bind `strpos(citext,citext)`,
  `replace(citext,citext,citext)`, `split_part(citext,citext,integer)` and
  `translate(citext,citext,text)`, with unnamed arguments and INT4/TEXT results.
  Their symbolic owner signatures retain the distinct third formals; category
  bounds keep regexp membership independent of the complete 56-member manifest.
  Incumbent comparison/operator slots remain fixed; the associated array is
  lookup-only. Operator links and procedure authority come from exact manifest
  slots and signatures. Comparison routine display declarations retain C
  language and original source symbols; I/O routines retain INTERNAL language
  and text codec symbols. I/O helper names use `ScriptFunctions`; owner metadata
  serializes citext inputs symbolically and resolves destination descriptors
  and installed conversion results on import, never copying a source OID.
  Conversion declarations own the released internal display symbols `rtrim1`,
  `booltext` and `network_show`, with invoker security and unnamed arguments;
  display metadata does not replace the installed provider's execution authority.
  Earthdistance requires installed cube and atomically stages the `earth` domain,
  its actual companion and six coordinate/conversion routines, followed by
  `geo_distance(point,point)` and its self-commuting `<@>` operator. Incumbent slots
  stay fixed. Operator admission validates its exact slot, formals, result and
  backing procedure against the current manifest. `Stage.EarthChecks` attaches the
  three released domain recipes. Owner metadata records type roles `earth`/`earth[]`,
  symbolic earth inputs and the fixed `600,600` point signature;
  companion resolution uses the domain's actual Array identity, not `_earth`.
  Routine labels, namespaces and owners can vary independently; domain and
  companion retain matching namespace/owner. Legacy two-member records reject.
- **lo** (`lo_provider.go`): the trusted, relocatable 1.2 provider stages a
  constraint-free concrete domain over the catalog's builtin OID generation and
  its lookup-only companion as two ordered extension members, each carrying a
  nonzero allocated catalog revision rather than a bootstrap fallback. Ordinary domain
  coercion, OID input/output and bind/result paths retain unsigned numeric
  payloads and typed NULLs; logical lo identity has physical OID result metadata.
  No large-object lookup or reference-management trigger is attached. Symbolic
  owner roles `lo`/`lo[]` resolve the actual reciprocal companion, independent
  of its current spelling. The codec and import require the two role-to-slot
  assignments, empty signatures and a common domain/companion owner; the
  extension installer may differ from its bootstrap-owned trusted members.
- **Registry and update paths** (`extension_register.go`, `extension_paths.go`):
  `Register(registry, name, implementation)` checks one startup registration
  (name, control version, `ValidateActions`, duplicates, in that order) and
  returns the extended registry (allocating a nil one); on error it returns nil
  and leaves the registry unchanged. `UpdatePathRows(ctx, registry, name,
  shape)` builds the `pg_extension_update_paths` rows (0A000 first, then one
  row per ordered version pair, checking `ctx` before each).
- **Staging** (`stage.go`, `*_stage.go`, `extension_actions.go`): `Stage` is the
  host a provider installs through (the stage base, name lookup, checked
  `Create`/`Replace`, OID and revision allocation, the associated array helpers,
  the creation type ACL and `ScriptFunctions`, the shared script helper lookup
  used by unaccent and citext). `Installer` is an implementation with
  `Stage(Stage, ext, owner)`; every stock provider is one, and fuzzystrmatch is
  also an `Updater` (`Updates`, one `UpdateAction` per executable version edge).
  `Actions`, `ValidateActions` (registration check) and `StagePath` (runs the
  update actions along a version path).
- **Handlers** (`host.go`, `extension_create.go`, `extension_update.go`,
  `extension_drop.go`, `extension_owner_import.go`): `Host` is the statement
  host of CREATE and ALTER EXTENSION UPDATE (context, notices, the creation
  path, the trusted-replay flag, the bootstrap superuser, OID and revision
  allocation through the embedded `ddl.Allocator`, and `BeginStage`, which
  begins and authorizes the stage and returns it as a `Stage`; `Stage` embeds
  `ddl.Allocator` and `ddl.TypeStaging`). The statement authority is a
  [`ddl.Authority`](../ddl/README.md); the handlers check it through the
  captured `access.View` passed beside it (`view.Check(auth, ...)`,
  `view.Effective(auth, ...)`), which also supplies the catalog, roles, role
  state, registry (`Owner.Extensions`) and object domain. `StageCreate` and
  `StageUpdate` make every decision of the two statements and stage the result,
  returning nil for a skipped statement (IF NOT EXISTS, already installed
  version). `DropTargets` selects DROP EXTENSION's targets (notices through a
  callback). `ImportOwners` applies Restore's owner/installed-procedure ACL
  records to a private catalog clone and reports whether authority changed.
  Ownership import uses the shared immutable ACL-owner transform for types,
  companions and procedures. Synthetic type baseline markers and raw NULL versus
  explicit-empty ACL distinctions survive; malformed baselines still fail the
  ordinary provider validator.
  Shared untrusted-extension creation rejects nonsuperusers with 42501.
  Owner metadata v2 records current schema, portable signature, owner and
  versioned manifest slot. Procedure entries retain current public names;
  installed type entries retain validated recipe roles, not mutable spellings.
  Restore replays actual routine alterations first; import validates labels
  against that destination slot, not names in the extension target schema.
  Output type signatures stay symbolic across OID remapping. Duplicate slots,
  corrupt/unknown fields and obsolete name-only records fail.
  Installed procedure ACL entries encode named grantors/grantees, PUBLIC,
  EXECUTE and grant options, never borrowed principal references. Strict nested
  decoding rejects duplicate/unknown/null fields and invalid grants. Import
  validates the actual destination slot before resolving captured role names;
  amcheck's canonical owner-only ACL is explicit metadata, not a PUBLIC default.
- **Owners, remap, errors**: the owner metadata codec (`OwnerMetadata`,
  `DecodeOwners`, `OwnerRecord`, `MemberOwner`, `OwnersMarker`;
  `extension_owners.go`), `RemapDescriptor` (`extension_remap.go`),
  `MemberConflict`, `NameConflict` and `VersionError` (`extension_errors.go`),
  `AuthorityObservation` (its `Current` is the publication fence check) and
  `RemovalRoots`.
  `RemapDescriptor` is the sole operator-descriptor remap owner, including
  membership and all type/procedure links. Each source maps once; a destination
  that is also a source does not trigger another mapping step.

Type identity is its validated manifest role, not its mutable name or namespace.
`InstalledTypes`/`MemberTypeRole` resolve cube/query_int/citext/ean13/isbn/isbn13/ismn/ismn13/earth/lo through the
existing manifest/provider and reciprocal array checks. Member ALTER retains
installation membership; direct DROP remains protected. The owner metadata codec
keeps canonical type role keys (including earth[] and lo[]) across rename/move, preserving
strict key/signature decoding and independent routine owners. Restore applies
these roles to the actual destination descriptors, never a same-named loose type.


## Invariants

- **No locks, no engine state.** The package has no mutex and reads the catalog
  only through `catalog.Read`, `Stage` and the `*catalog.State` values the engine
  passes (`ImportOwners` writes only its private clone).
- **Lock contract.** The engine calls `StageCreate`, `StageUpdate`,
  `DropTargets`, `ImportOwners`, `AuthorityObservation.Current`,
  `Installer.Stage`, the update actions and `StagePath` with `Engine.mu` held,
  and its `Host` and `Stage` implementation (`extensionHost`) runs under that
  lock too; nothing here may call anything that takes it. Provider validation
  (`ValidateExtension`) runs later, outside the lock. Allocation and check order
  are part of the output: installed OIDs, revisions and the first error must
  not change.
- **Immutable stock providers.** The stock instances are never mutated;
  `Control` and `Versions` return detached copies.
- **One provider identity.** The engine registers the stock providers in
  `catalog.Owner.Extensions`, and staging stores the same pointer in each
  member's `Implementation`. Every `*ProviderOf(d) != Stock*` check and the
  validators rely on that identity.
- **One lifecycle.** Family/signature/array validation, symbolic owner metadata
  and procedure remapping are registry-owned; providers never add a second
  extension lifecycle or invented callable members.
- **Stable output.** Member OIDs, namespaces, owners, messages, SQLSTATEs, the
  owner metadata format and remap are byte-stable; dumps depend on them.

## What stays in the engine

The `extension.Host` and `extension.Stage` adapter `extensionHost`
(`extension_host.go`, with `stageOf`, which gives the root its `CatalogStage`
back); the closed-registry check of `registerExtension` and the startup list in
`engine.go`; the snapshot check of `statementHost.OpenExtensionPaths`; the
orchestration of the handlers (`extension_install.go`, `extension_update.go`,
`extension_drop.go`, `extension_owner_import.go`): argument count, `Engine.mu`,
authorization capture, publication (`publishExtensionStage`), DROP's removal
pipeline and its multi-target 2BP01 text and cascade notice, and the owner
import's authority check, clone, expression reown and publish; the fence wrapper
`extensionAuthorityFence`; `namespaceChildren` and `database_grant.go`; the
installed expression wrappers and the `pg_extension_update_paths` node, which
belong with `plan`; the intagg binder and view recipe methods.

## Testing

`TestRegisterAddsOnlyValidImplementations` pins `Register` (nil registry
allocated; duplicate, nil, versionless or badly named registrations rejected
with the registry unchanged). Every other witness needs an engine that installs
the extension, so provider identity, conflicts, forged manifests and owner import
are engine units. Public SQL lives in
[reference/catalogs](../../../reference/catalogs/README.md#testing) (extension
DDL and catalogs),
[reference/extension_types](../../../reference/extension_types/README.md#testing)
(earth, cube, lo and ISN values),
[reference/types](../../../reference/types/README.md#testing) (citext) and
[reference/locking_transactions](../../../reference/locking_transactions/README.md#testing)
(pgrowlocks). Add `REFERENCE=1` for the oracle.

Identity-remap units retain genuine physical TEMP owners and derive collision
targets from actual imported identities. The earth control fixture allocates
namespace pairs until an observed identity collides, failing if allocation
passes the target. Replacement must remint the imported domain while preserving
the retained TEMP descriptor; incidental prerequisite-member counts are not
collision evidence.

```sh
make check RUN='^Test(Extension|Isn|Issn|Citext(Provider|Comparison|Pattern)|CubeProvider|CubeCoordinateProvider|CubeSpatialProvider|EarthProvider|EarthCoordinate|PgrowlocksProvider|PgcryptoOwner|InstalledCodecArray)'
make check RUN='^TestReference(Extension|AvailableExtensions)'
```


Routine slot/label/provenance proof (no SQL execution):
`make check RUN='^Test(InstalledProcedure(RelabelIdentity|Forgery|VersionTransition|RemapIdentity)|DetachedUnaccentImmutableCapability|ExtensionOwner(SlotCodec|ImportCurrentSlotIdentity|ImportTransfer))$'`.
Engine caller witnesses additionally cover current v2 metadata, unreplayed/
foreign import labels, COW inventory corruption and genuine provider relabeling.
Public provider/replay behavior uses shared `TestReferenceRoutineAlter*`
definitions in native and `REFERENCE=1` modes; see
[routine proof](../../../reference/query_core/README.md#routine-alter-proof).
