# internal/engine/extension

The installed-extension layer that does not need the engine: provider
records and control data for the seven bundled extensions (cube,
fuzzystrmatch, intagg, intarray, pgcrypto, unaccent, uuid-ossp), member
validation, membership, the owner metadata codec used by native Dump, and
extension remap. Part A of step 16 moved them here; CREATE/ALTER/DROP
EXTENSION and provider staging follow in Part B (16c/16d). The package sits
at layer L10 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports only `authorization`, `catalog`, `expr`, `functions` (the moved
kernels and member tables), `ids`, `parser`, `sqlstate`, `sqltype` and the
standard library, and never the engine, `routine` or `typedef`. See
[ENGINE-SPLIT-extension.md](../../../docs/ENGINE-SPLIT-extension.md).

## What it owns

- **Providers** (`*_provider.go`): `CubeProvider`, `FuzzystrmatchProvider`,
  `IntaggProvider`, `IntarrayProvider`, `PgcryptoProvider`,
  `UnaccentProvider` and `UUIDOsspProvider`, each with `Control`,
  `Versions`, `ValidateExtension` (`catalog.ExtensionImplementation`) and,
  except unaccent, `ValidateProcedure`. The stock instances are
  `StockCube` … `StockUUIDOssp`. The script contexts (`CubeTypeContext`,
  `IntarrayInputContext`, `UnaccentTemplateContext`, …), signatures
  (`CubeSignature`, `QueryIntSignature`) and member tables
  (`FuzzystrmatchMembers`, `IntaggMembers`) are what staging needs.
- **Accessors** (`extension_implementation.go`): `CubeProviderOf` …
  `UnaccentDictionaryProviderOf` return a descriptor's implementation when it
  has that concrete type, and nil otherwise.
- **Catalog validation** (`extension_catalog.go`): `CatalogProvider`
  validates the extension, text search template and dictionary classes and
  installed member procedures; `ValidateProviderHeader` is the identity check
  it shares with the engine's `procedureProvider`; `Membership` reads a
  descriptor's owning extension. `extension_type_validation.go` validates an
  installed codec's array through the type provider registered in the
  catalog owner.
- **intagg** (`intagg_identity.go`, `intagg_aggregate.go`):
  `ValidateIntaggIdentity`, `IntaggRecipeFunction`, `IntaggDefinition` and
  the owned-aggregate checks and remap (`plan.SameIntaggIdentity` compares
  captured identities).
- **Owners, remap, errors**: the owner metadata codec (`OwnerMetadata`,
  `DecodeOwners`, `OwnerRecord`, `MemberOwner`, `OwnersMarker`;
  `extension_owners.go`), `RemapDescriptor` (`extension_remap.go`),
  `MemberConflict`, `NameConflict` and `VersionError`
  (`extension_errors.go`), `AuthorityObservation` and `RemovalRoots`.

## Invariants

- **No locks, no engine state.** The package has no mutex and reads the
  catalog only through `catalog.Read`.
- **Immutable stock providers.** The stock instances are never mutated;
  `Control` and `Versions` return detached copies.
- **Members hold the bare provider.** The engine registers one installer per
  extension (`cubeInstaller{extension.StockCube}`, …) in
  `catalog.Owner.Extensions`; installed members store the stock provider in
  `Implementation`, never the installer. Every `*ProviderOf(d) != Stock*`
  check and the validators rely on that identity.
- **Byte-identical output.** Member OIDs, namespaces, owners, messages,
  SQLSTATEs, the owner metadata format and remap match the previous engine
  output exactly.

## What stays in the engine

The installers and staging (`*_stage.go`, `extension_install.go`,
`extension_update.go`, `extension_drop.go`, `extension_owner_import.go`;
Part B); `procedureProvider` (builtin, SQL, foreign and conversion routine
validation, a `routine` candidate); `procedureInNamespaces`,
`namespaceChildren` and `database_grant.go`; the installed expression
wrappers and the `pg_extension_update_paths` node, which move with `plan`;
the intagg binder and view recipe methods.

## Testing

The package has no unit tests of its own: every witness needs an engine
that installs the extension. Engine witnesses:

```sh
make check RUN='^(TestExtensionMembersHoldBareProviders|TestExtensionProviderIdentity|TestInstalledCodecArrayUsesRegisteredTypeProvider|TestExtensionProviderValidatesOnlyInstalledProcedures|TestExtensionOwnerManifestAdmission|TestPgcryptoOwnerMetadata|TestExtensionVersionGraph)$'
```

Public behavior is covered by the CREATE/ALTER/DROP EXTENSION, extension
function, `pg_extension`, `pg_available_extensions` and Dump/Restore
selections in `reference/` that the step 16 prompt file lists.
