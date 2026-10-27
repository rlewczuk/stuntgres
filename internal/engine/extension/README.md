# internal/engine/extension

The installed-extension layer that does not need the engine: provider records
and control data for the eighteen bundled extensions (amcheck, citext, cube,
earthdistance, fuzzystrmatch, hstore, intagg, intarray, isn, lo, ltree, pageinspect, pgcrypto, pgrowlocks, seg, tablefunc, unaccent, uuid-ossp),
member validation, membership, the owner metadata codec used by native Dump,
extension remap, provider staging, and the decisions of the CREATE, ALTER
EXTENSION UPDATE and DROP EXTENSION handlers and of the Restore owner import.

## Imports

Layer L9 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports only `access` (privilege requirements and the captured view),
`authorization`, `catalog`, `ddl` (the shared authority and allocator), `expr`,
`functions` (kernels and member tables), `ids`, `parser`, `sqlstate`, `sqltype`,
`typedef` (the relation type name check of installed-scalar staging)
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
  The pgrowlocks, pageinspect and amcheck manifest builders also own immutable fixed OUT
  type IDs and complete formal names, including their input names. Their
  comparable installed declaration carries the shared immutable payload through
  relabeling, owner import and address remapping; identity validation rejects a
  foreign payload even when its numeric formals happen to match.
- **Catalog validation** (`extension_catalog.go`): `CatalogProvider` validates
  the extension, text search template and dictionary classes and installed
  member procedures; `ValidateProviderHeader` is the identity check it shares
  with `stored.ProcedureProvider`; a descriptor's owning extension is
  `catalog.ExtensionMembership`. `extension_type_validation.go` validates an
  installed codec's array through the type provider registered in the catalog
  owner.
- **Seg members** (`seg_provider.go`, `seg_stage.go`): the trusted relocatable
  1.4 provider owns the twelve-byte scalar and strict immutable C input/output
  roles. Its reciprocal array is lookup-only, not an extension member. Staging,
  validation, symbolic owner signatures and import use the shared installation
  authority; mutable labels never grant codec execution.
- **Ltree members** (`ltree_provider.go`, `ltree_stage.go`): the current 1.3
  manifest owns lquery, ltree and ltxtquery, each with a strict immutable C text input/output
  pair. Fixed type/input/output triples preserve existing slots; all are staged
  and validated before publication. Ltxtquery uses the released ltxtq_in/ltxtq_out symbols. Each reciprocal associated array is internally
  dependent and lookup-only. Symbolic roles and owner signatures, not mutable names,
  authorize import remapping; scalar storage metadata uses extended storage.
  Lquery retains slots 0/1/2 and ltree 3/4/5; ltxtquery appends 6/7/8.
  The nine represented members are scalar/I/O roles, not an inventory of every
  object in the PostgreSQL extension.
- **Dictionary definitions** (`dictionary_*.go`): ordinary schema-CREATE and
  owner authority, independent of extension installation privileges. Preparation
  captures template/options and compilation runs outside the catalog mutex;
  completion rechecks current namespace/template/revision and role authority.
  SET SCHEMA checks destination CREATE before source ownership, both during
  preparation and at current-authority completion.
  Ordered immutable options and genuine simple/unaccent runtime state publish in
  the existing catalog journal. ALTER removes matching options before appending
  supplied replacements. Standalone dictionaries depend on their real namespace
  and template, without fabricated extension membership. `DictionaryDumpStatement`
  serializes qualified template/options and owner metadata, never runtime pointers.
  Installed dictionaries retain their genuine provider, template/helper links and
  reciprocal manifest slot across supported name/schema changes. Their public
  placement is not the installation namespace. Owner metadata accepts mutable
  dictionary labels only for the unaccent dictionary slot and checks the exact
  current name/schema/slot during import.
  The one-argument unaccent function independently resolves literal `unaccent` in
  its current function schema at execution; the original member slot is provider
  provenance, not a permanent default-dictionary binding.
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
- **hstore** (`hstore_provider.go`, `hstore_stage.go`): trusted, relocatable 1.8
  installation of the scalar and strict immutable parallel-safe C-language
  `hstore_in(cstring)`/`hstore_out(hstore)` codecs. The reciprocal associated
  array is internally dependent and lookup-only. Member identity, ownership and
  remapping use this bounded manifest; output owner signatures use symbolic
  `hstore`, never a source OID. Text storage does not install advanced operations.
- **intagg** (`intagg_identity.go`, `intagg_aggregate.go`):
  `ValidateIntaggIdentity`, `ProcedureSymbol`, `IntaggDefinition` and the
  owned-aggregate checks and remap (`plan.SameIntaggIdentity` compares captured
  identities).
- **pageinspect** (`pageinspect_provider.go`): the superuser, untrusted,
  relocatable 1.13 manifest owns four BRIN byte decoders and `bt_page_items(bytea)`.
  Scalar, record and set results retain exact released declaration shapes and input
  labels. `ValidatePageinspectCall` validates the selected symbol, formal slots
  and descriptor-owned results independently of mutable public names. Explicit
  source identities preserve `bt_page_items_bytea` through member renames.
  The existing owner codec admits its exact version, five slots and manifest
  signatures while preserving independent member owners, renames and schema
  moves; import still validates each actual destination member identity.
- **amcheck** (`amcheck_provider.go`): the untrusted/superuser/relocatable 1.5
  provider installs eight callable members. Slot zero is `verify_heapam`, with
  six canonical inputs and five trailing defaults, a non-STRICT volatile
  record-set result and parallel-unsafe declaration. Slots one through seven
  are the three `bt_index_check` and four `bt_index_parent_check` scalar
  overloads returning VOID, all STRICT, VOLATILE and parallel restricted.
  All members start with explicit owner-only EXECUTE ACLs. Ordered manifest
  symbols and the distinct heap/B-tree identity validators preserve exact
  installed declaration authority through rename, move, owner transfer and
  symbolic replay. Current arbitrary installed ACLs, including grantor and
  grant-option provenance and PUBLIC entries, use the shared member codec;
  mutable names never authorize execution. No scan state is serialized.
  After selected-lifetime and current EXECUTE admission, the B-tree host
  invocation borrows the admitted catalog's canonical procedure and epoch,
  never a retained preparation epoch. This invocation-local bridge neither
  mutates the stored selection nor authorizes an otherwise stale reference.
- **tablefunc** (`tablefunc_provider.go`): trusted/relocatable version 1.0
  retains strict volatile `normal_rand(integer,float8,float8)` at slot zero
  and appends four strict stable SETOF-record connectby overloads. One ordered
  manifest drives staging, immutable symbol checks and symbolic owner replay.
  Caller-owned record definitions are not provider OUT parameters. Mutable names
  never grant execution; crosstab and other upstream members are not advertised.
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
  amcheck's initial owner-only ACL and arbitrary later grants use the same
  explicit metadata, never a canonical-only restriction or PUBLIC-default bypass.
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
`InstalledTypes`/`MemberTypeRole` resolve seg/hstore/cube/query_int/lquery/ltree/ltxtquery/citext/ean13/isbn/isbn13/ismn/ismn13/earth/lo through the
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
  `Installer.Stage`, the update actions and `StagePath` with `Engine.Mu` held,
  and its `Host` and `Stage` implementation (`schemacmds`' `extensionHost`)
  runs under that lock too; nothing here may call anything that takes it. Provider validation
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

[`schemacmds`](../schemacmds/README.md#extensions) holds the `extension.Host`
and `extension.Stage` adapter `extensionHost` (`extension_host.go`, with
`stageOf`, which gives the executors their `CatalogStage` back) and the
orchestration of the handlers (`extension_install.go`, `extension_update.go`,
`extension_drop.go`, `extension_owner_import.go`): argument count, `Engine.Mu`,
authorization capture, publication (`publishExtensionStage`), DROP's removal
pipeline and its multi-target 2BP01 text and cascade notice, and the owner
import's authority check, clone, expression reown and publish; and the fence
wrapper `extensionAuthorityFence`. Core keeps the closed-registry check of
`registerExtension` and the startup list in `engine.go`; the snapshot check of
`statementHost.OpenExtensionPaths`; the installed expression wrappers and the
`pg_extension_update_paths` node, which belong with `plan`; the intagg binder
and view recipe methods.

## Testing

`TestRegisterAddsOnlyValidImplementations` pins `Register` (nil registry
allocated; duplicate, nil, versionless or badly named registrations rejected
with the registry unchanged). Pure provider identity and owner-codec units use
unpublished catalog states; composed installation, conflict and owner-import
witnesses live in `internal/engine/schemacmds`. Public SQL lives in
[reference/extensions](../../../reference/extensions/README.md#testing) (extension
DDL and catalogs),
[reference/extension_types](../../../reference/extension_types/README.md#testing)
(hstore, earth, cube, lo and ISN values),
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
make check RUN='^TestAmcheck'   # installed B-tree manifest and expression identity
make check RUN='^TestReference(Extension|AvailableExtensions)'
```


Routine slot/label/provenance proof (no SQL execution):
`make check RUN='^Test(InstalledProcedure(RelabelIdentity|Forgery|VersionTransition|RemapIdentity)|DetachedUnaccentImmutableCapability|ExtensionOwner(SlotCodec|ImportCurrentSlotIdentity|ImportTransfer))$'`.
The independently declared selected tablefunc inventory (normal_rand slot zero,
four connectby slots), mutable-label identity, manifest rejection and symbolic
owner codec are selected with `make check RUN='^TestTablefuncConnectby'`.
Engine caller witnesses additionally cover current v2 metadata, unreplayed/
foreign import labels, COW inventory corruption and genuine provider relabeling.
Public provider/replay behavior uses shared `TestReferenceRoutineAlter*`
definitions in native and `REFERENCE=1` modes; see
[routine proof](../../../reference/query_core/README.md#routine-alter-proof).
