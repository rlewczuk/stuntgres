# internal/engine/extension

The installed-extension layer that does not need the engine: provider
records and control data for the seven bundled extensions (cube,
fuzzystrmatch, intagg, intarray, pgcrypto, unaccent, uuid-ossp), member
validation, membership, the owner metadata codec used by native Dump, and
extension remap, and provider staging. Part A of step 16 moved the records
here and 16d.1 the staging; the CREATE/ALTER/DROP EXTENSION handler
decisions follow in 16d.2. The package sits
at layer L10 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports only `authorization`, `catalog`, `expr`, `functions` (the moved
kernels and member tables), `ids`, `parser`, `sqlstate`, `sqltype`, `typedef`
(the relation type name check of cube and intarray staging) and the standard
library, and never the engine, `analyze`, `routine` or `plan`. See
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
- **Staging** (`stage.go`, `*_stage.go`, `extension_actions.go`): `Stage`
  is the host a provider installs through (the stage base, name lookup,
  checked `Create`/`Replace`, OID and revision allocation, the associated
  array helpers, the creation type ACL and `ScriptFunctions`, unaccent's
  script helper lookup). `Installer` is an implementation with
  `Stage(Stage, ext, owner)`; every stock provider is one, and
  fuzzystrmatch is also an `Updater` (`Updates`, one `UpdateAction` per
  executable version edge). `Actions`, `ValidateActions` (registration
  check) and `StagePath` (runs the update actions along a version path).
- **Owners, remap, errors**: the owner metadata codec (`OwnerMetadata`,
  `DecodeOwners`, `OwnerRecord`, `MemberOwner`, `OwnersMarker`;
  `extension_owners.go`), `RemapDescriptor` (`extension_remap.go`),
  `MemberConflict`, `NameConflict` and `VersionError`
  (`extension_errors.go`), `AuthorityObservation` and `RemovalRoots`.

## Invariants

- **No locks, no engine state.** The package has no mutex and reads the
  catalog only through `catalog.Read` and `Stage`.
- **Lock contract.** The engine calls `Installer.Stage`, the update actions
  and `StagePath` with `Engine.mu` held, and its `Stage` implementation
  (`extensionHost`) runs under that lock too; staging must not call
  anything that takes it. Allocation and check order are part of the
  output: installed OIDs, revisions and the first error must not change.
- **Immutable stock providers.** The stock instances are never mutated;
  `Control` and `Versions` return detached copies.
- **One provider identity.** The engine registers the stock providers
  (`StockCube`, …) in `catalog.Owner.Extensions`, and staging stores the
  same pointer in each member's `Implementation`. Every
  `*ProviderOf(d) != Stock*` check and the validators rely on that
  identity.
- **Byte-identical output.** Member OIDs, namespaces, owners, messages,
  SQLSTATEs, the owner metadata format and remap match the previous engine
  output exactly.

## What stays in the engine

The `extension.Stage` adapter `extensionHost` (`extension_host.go`); the
handlers and registration (`extension_install.go`, `extension_update.go`,
`extension_drop.go`, `extension_owner_import.go`; their decisions move in
16d.2); `procedureProvider` (builtin, SQL, foreign and conversion routine
validation, a `routine` candidate); `namespaceChildren` and
`database_grant.go` (`procedureInNamespaces` is `analyze.ProcedureInNamespaces`); the installed expression
wrappers and the `pg_extension_update_paths` node, which move with `plan`;
the intagg binder and view recipe methods.

## Testing

The package has no unit tests of its own: every witness needs an engine
that installs the extension. Engine witnesses:

```sh
make check RUN='^(TestExtensionHostRefusesDiscardedStage|TestExtensionMembersHoldBareProviders|TestExtensionProviderIdentity|TestInstalledCodecArrayUsesRegisteredTypeProvider|TestExtensionProviderValidatesOnlyInstalledProcedures|TestExtensionOwnerManifestAdmission|TestPgcryptoOwnerMetadata|TestExtensionVersionGraph)$'
```

Public behavior is covered by the CREATE/ALTER/DROP EXTENSION, extension
function, `pg_extension`, `pg_available_extensions` and Dump/Restore
selections in `reference/` that the step 16 prompt file lists.
