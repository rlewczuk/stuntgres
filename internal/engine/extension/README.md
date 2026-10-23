# internal/engine/extension

The installed-extension layer that does not need the engine: provider records
and control data for the ten bundled extensions (citext, cube, earthdistance,
fuzzystrmatch, intagg, intarray, pgcrypto, pgrowlocks, unaccent, uuid-ossp),
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
  and, except unaccent, `ValidateProcedure`. The stock instances are
  `StockCitext` … `StockUUIDOssp`. The script contexts (`CitextTypeContext`,
  `CubeTypeContext`, `IntarrayInputContext`, `UnaccentTemplateContext`, …),
  signatures (`CubeSignature`, `CubeBinarySignature`, `QueryIntSignature`) and
  member tables (`FuzzystrmatchMembers`, `IntaggMembers`) are what staging needs.
- **Accessors** (`extension_implementation.go`): `CitextProviderOf` …
  `UnaccentDictionaryProviderOf` return a descriptor's implementation when it
  has that concrete type, and nil otherwise.
- **Catalog validation** (`extension_catalog.go`): `CatalogProvider` validates
  the extension, text search template and dictionary classes and installed
  member procedures; `ValidateProviderHeader` is the identity check it shares
  with `stored.ProcedureProvider`; a descriptor's owning extension is
  `catalog.ExtensionMembership`. `extension_type_validation.go` validates an
  installed codec's array through the type provider registered in the catalog
  owner.
- **Cube spatial members** (`cube_spatial.go`, `cube_spatial_call.go`): cube owns
  its scalar type, two codecs, six constructors, two inquiries, six binary spatial
  procedures and six operators as one validated 23-member manifest. Existing
  routine order is retained before spatial procedure/operator tails; offsets and
  member count derive from the routine tables. Operators carry real Extension
  membership. Owner signatures replace every cube formal with symbolic `cube`,
  preserving mixed constructor signatures and spatial `cube,cube` signatures.
  `ImportOwners` enforces the same table-derived member count as staging and
  owner-record decoding.
- **intagg** (`intagg_identity.go`, `intagg_aggregate.go`):
  `ValidateIntaggIdentity`, `IntaggRecipeFunction`, `IntaggDefinition` and the
  owned-aggregate checks and remap (`plan.SameIntaggIdentity` compares captured
  identities).
- **pgrowlocks** (`pgrowlocks_provider.go`, `pgrowlocks_stage.go`): the
  nontrusted/superuser/relocatable 1.2 singleton installs exactly
  `pgrowlocks(text) RETURNS SETOF record` (STRICT, VOLATILE, parallel safe).
  `ValidatePgrowlocksIdentity` checks actual owner-domain, epoch, descriptor and
  singleton manifest, not spelling. Owner metadata, transaction publication,
  dependency removal and remap use the shared extension lifecycle; no lock state
  is serialized.
- **citext and earthdistance.** Citext stages the scalar and its two text-I/O
  members, resolving I/O helper names through `ScriptFunctions`; its owner
  metadata serializes the output signature symbolically and resolves it to the
  destination's installed descriptor on import, never copying a source OID.
  Earthdistance requires installed cube and stages `earth` with three domain
  CHECK recipes through `Stage.EarthChecks`; its owner metadata records the roles
  `earth` and `earth[]`, resolving the array through the domain's actual Array
  identity, never by assuming the name `_earth`.
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
  callback). `ImportOwners` applies Restore's owner records to a private clone
  of the catalog state and reports whether an owner changed. Shared
  untrusted-extension creation rejects nonsuperusers with 42501.
- **Owners, remap, errors**: the owner metadata codec (`OwnerMetadata`,
  `DecodeOwners`, `OwnerRecord`, `MemberOwner`, `OwnersMarker`;
  `extension_owners.go`), `RemapDescriptor` (`extension_remap.go`),
  `MemberConflict`, `NameConflict` and `VersionError` (`extension_errors.go`),
  `AuthorityObservation` (its `Current` is the publication fence check) and
  `RemovalRoots`.
  `RemapDescriptor` is the sole operator-descriptor remap owner, including
  membership and all type/procedure links. Each source maps once; a destination
  that is also a source does not trigger another mapping step.

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
DDL and catalogs), [reference/types](../../../reference/types/README.md#testing)
(citext, earth, cube values) and
[reference/locking_transactions](../../../reference/locking_transactions/README.md#testing)
(pgrowlocks). Add `REFERENCE=1` for the oracle.

Identity-remap units retain genuine physical TEMP owners and derive collision
targets from actual imported identities. The earth control fixture allocates
namespace pairs until an observed identity collides, failing if allocation
passes the target. Replacement must remint the imported domain while preserving
the retained TEMP descriptor; incidental prerequisite-member counts are not
collision evidence.

```sh
make check RUN='^Test(Extension|CitextProvider|CubeProvider|CubeSpatialProvider|EarthProvider|PgrowlocksProvider|PgcryptoOwner|InstalledCodecArray)'
make check RUN='^TestReference(Extension|AvailableExtensions)'
```
