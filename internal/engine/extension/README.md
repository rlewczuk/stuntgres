# internal/engine/extension

The installed-extension layer that does not need the engine: provider
records and control data for the ten bundled extensions (citext, cube,
earthdistance, fuzzystrmatch, intagg, intarray, pgcrypto, pgrowlocks, unaccent, uuid-ossp), member
validation, membership, the owner metadata codec used by native Dump,
extension remap, provider staging, and the decisions of the CREATE, ALTER
EXTENSION UPDATE and DROP EXTENSION handlers and of the Restore owner
import. Part A of step 16 moved the records here, 16d.1 the staging and
16d.2 the handler decisions. The package sits at layer L9 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports only `access` (privilege requirements and the captured view),
`authorization`, `catalog`, `ddl` (the shared authority and allocator),
`expr`, `functions` (the moved
kernels and member tables), `ids`, `parser`, `sqlstate`, `sqltype`, `typedef`
(the relation type name check of citext, cube and intarray staging) and the standard
library, and never the engine, `analyze`, `routine` or `plan`. See
[ENGINE-SPLIT-extension.md](../../../docs/ENGINE-SPLIT-extension.md).

Citext1.8 stages the scalar and two genuine text-I/O members, with a reciprocal
lookup-only companion array. The full CREATE TYPE script resolves its I/O helper
names through ScriptFunctions, preserving actual TEMP lookup/owner authority and
atomic failure. Family/signature/array validation, symbolic owner metadata and
procedure remapping remain registry-owned, not a second extension lifecycle.
The owner codec admits citext version1.8 with exactly the scalar `citext`,
`citextin(cstring)` and `citextout(citext)` members. The output signature is
serialized symbolically as `citext` and resolved to the destination's installed
descriptor during owner import, never copied as a source-generation OID.
Public values and selected dual-mode proof live in
[`reference/types`](../../../reference/types/README.md#citext-storage-and-value-round-trips);
function-level staging/conflict/import witnesses live in the engine package.

## What it owns

- **Providers** (`*_provider.go`): `CitextProvider`, `CubeProvider`, `FuzzystrmatchProvider`,
  `IntaggProvider`, `IntarrayProvider`, `PgcryptoProvider`, `PgrowlocksProvider`,
  `UnaccentProvider` and `UUIDOsspProvider`, each with `Control`,
  `Versions`, `ValidateExtension` (`catalog.ExtensionImplementation`) and,
  except unaccent, `ValidateProcedure`. The stock instances are
  `StockCitext` … `StockUUIDOssp`. The script contexts (`CitextTypeContext`, `CubeTypeContext`,
  `IntarrayInputContext`, `UnaccentTemplateContext`, …), signatures
  (`CubeSignature`, `QueryIntSignature`) and member tables
  (`FuzzystrmatchMembers`, `IntaggMembers`) are what staging needs.
- **Accessors** (`extension_implementation.go`): `CitextProviderOf` …
  `UnaccentDictionaryProviderOf` return a descriptor's implementation when it
  has that concrete type, and nil otherwise.
- **Catalog validation** (`extension_catalog.go`): `CatalogProvider`
  validates the extension, text search template and dictionary classes and
  installed member procedures; `ValidateProviderHeader` is the identity check
  it shares with `stored.ProcedureProvider`; a descriptor's owning
  extension is `catalog.ExtensionMembership` (moved down in catalog 5i).
  `extension_type_validation.go` validates an
  installed codec's array through the type provider registered in the
  catalog owner.
- **intagg** (`intagg_identity.go`, `intagg_aggregate.go`):
  `ValidateIntaggIdentity`, `IntaggRecipeFunction`, `IntaggDefinition` and
  the owned-aggregate checks and remap (`plan.SameIntaggIdentity` compares
  captured identities).
- **pgrowlocks** (`pgrowlocks_provider.go`, `pgrowlocks_stage.go`):
  the nontrusted/superuser/relocatable 1.2 singleton installs precisely
  `pgrowlocks(text) RETURNS SETOF record`. Its member is STRICT, VOLATILE and
  parallel safe. `ValidatePgrowlocksIdentity` checks actual owner-domain,
  epoch, descriptor and singleton manifest, not spelling. Owner metadata,
  transaction publication, dependency removal and remap use the same shared
  extension lifecycle as the scalar providers; no lock state is serialized.
  Shared untrusted-extension creation rejects nonsuperusers with 42501 and
  the PostgreSQL hint `Must be superuser to create this extension.`
- **Staging** (`stage.go`, `*_stage.go`, `extension_actions.go`): `Stage`
  is the host a provider installs through (the stage base, name lookup,
  checked `Create`/`Replace`, OID and revision allocation, the associated
  array helpers, the creation type ACL and `ScriptFunctions`, the shared
  script helper lookup used by unaccent and citext). `Installer` is an implementation with
  `Stage(Stage, ext, owner)`; every stock provider is one, and
  fuzzystrmatch is also an `Updater` (`Updates`, one `UpdateAction` per
  executable version edge). `Actions`, `ValidateActions` (registration
  check) and `StagePath` (runs the update actions along a version path).
- **Handlers** (`host.go`, `extension_create.go`, `extension_update.go`,
  `extension_drop.go`, `extension_owner_import.go`): `Host` is the statement
  host of CREATE and ALTER EXTENSION UPDATE (context, notices, the creation
  path, the trusted-replay flag, the bootstrap superuser, OID and revision
  allocation through the embedded `ddl.Allocator`, and `BeginStage`, which
  begins and authorizes the stage and returns it as a `Stage`; `Stage`
  embeds `ddl.Allocator` and `ddl.TypeStaging`). The statement authority is
  a [`ddl.Authority`](../ddl/README.md); the handlers check it through the
  captured `access.View` passed beside it (`view.Check(auth, ...)`,
  `view.Effective(auth, ...)`), which also supplies the catalog, roles, role
  state, registry (`Owner.Extensions`) and object domain. `StageCreate` and `StageUpdate` make
  every decision of the two statements and stage the result, returning nil
  for a skipped statement (IF NOT EXISTS, already installed version).
  `DropTargets` selects DROP EXTENSION's targets (notices through a
  callback). `ImportOwners` applies Restore's owner records to a private
  clone of the catalog state and reports whether an owner changed.
- **Registry and update paths** (`extension_register.go`,
  `extension_paths.go`): `Register(registry, name, implementation)` checks
  one startup registration (name, control version, `ValidateActions`,
  duplicates, in that order) and returns the registry with it added,
  allocating a nil one; on error it returns nil and leaves the registry
  unchanged. `UpdatePathRows(ctx, registry, name, shape)` builds the
  `pg_extension_update_paths` rows (the 0A000 "is not available" error
  first, then one row per ordered version pair, with `ctx` checked before
  each).
- **Owners, remap, errors**: the owner metadata codec (`OwnerMetadata`,
  `DecodeOwners`, `OwnerRecord`, `MemberOwner`, `OwnersMarker`;
  `extension_owners.go`), `RemapDescriptor` (`extension_remap.go`),
  `MemberConflict`, `NameConflict` and `VersionError`
  (`extension_errors.go`), `AuthorityObservation` (its `Current` is the
publication fence check) and `RemovalRoots`.

## Invariants

- **No locks, no engine state.** The package has no mutex and reads the
  catalog only through `catalog.Read`, `Stage` and the `*catalog.State`
  values the engine passes (`ImportOwners` writes only its private clone).
- **Lock contract.** The engine calls `StageCreate`, `StageUpdate`,
  `DropTargets`, `ImportOwners`, `AuthorityObservation.Current`,
  `Installer.Stage`, the update actions and `StagePath` with `Engine.mu`
  held, and its `Host` and `Stage` implementation (`extensionHost`) runs
  under that lock too; nothing here may call anything that takes it.
  Provider validation (`ValidateExtension`) runs later, outside the lock. Allocation and check order are part of the
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

The `extension.Host` and `extension.Stage` adapter `extensionHost`
(`extension_host.go`, with `stageOf`, which gives the root its
`CatalogStage` back); the closed-registry check of `registerExtension` and
the startup list in `engine.go`; the snapshot check of
`statementHost.OpenExtensionPaths`; the orchestration of the handlers (`extension_install.go`,
`extension_update.go`, `extension_drop.go`, `extension_owner_import.go`):
argument count, `Engine.mu`, authorization capture, publication
(`publishExtensionStage`), DROP's removal pipeline and its multi-target
2BP01 text and cascade notice, and the owner import's authority check,
clone, expression reown and publish; the fence wrapper
`extensionAuthorityFence`; `namespaceChildren` and
`database_grant.go` (`procedureInNamespaces` is `analyze.ProcedureInNamespaces`); the installed expression
wrappers and the `pg_extension_update_paths` node, which move with `plan`;
the intagg binder and view recipe methods.

## Testing

Earthdistance1.2 requires the actual installed cube extension. It stages earth
and its lookup-only companion as genuine type members, with three ordinary
domain CHECK recipes attached through Stage.EarthChecks. Read-only structural
recipe inspection preserves this package's no-plan-import boundary; concrete
tree validation remains the ordinary stored renderer's authority. Registered
prerequisites share catalog projection, dump ordering and DROP dependency
traversal. There are no invented callable cube/geographic procedure members.
Owner metadata records the exact roles `earth` and `earth[]`, resolving the latter
through the installed domain's actual Array identity. It never assumes the
companion's physical name is `_earth`; name collisions retain ordinary associated
array naming. Both owner roles must agree, independent of import record order.
Selected engine units: `make check RUN='^TestEarthProvider(Identity|Conflicts|Controls)$'`.
Public both-mode definitions are documented in
[`reference/types`](../../../reference/types/README.md#earth-domain-contract).


The package test pins `Register`: a nil registry is allocated, and a
duplicate, nil, versionless or badly named registration is rejected with
the registry unchanged:

```sh
make check RUN='^TestRegisterAddsOnlyValidImplementations$'
```

Every other witness needs an engine that installs the extension. Engine
witnesses:

```sh
make check RUN='^(TestExtensionHostRefusesDiscardedStage|TestExtensionHostBeginStageHandsBackRootStage|TestExtensionMembersHoldBareProviders|TestExtensionProviderIdentity|TestInstalledCodecArrayUsesRegisteredTypeProvider|TestExtensionProviderValidatesOnlyInstalledProcedures|TestExtensionOwnerManifestAdmission|TestPgcryptoOwnerMetadata|TestExtensionVersionGraph)$'
```

Citext's real staged identity, type/signature conflicts, invalid manifests and
source-pointer-authorized import/remapping are exercised without public SQL:

```sh
make check RUN='^TestCitextProvider(Identity|Conflicts|Import)$'
```


Public behavior is covered by the CREATE/ALTER/DROP EXTENSION, extension
function, `pg_extension`, `pg_available_extensions` and Dump/Restore
selections in `reference/` that the step 16 prompt file lists.

Pgrowlocks provider/OUT-shape, forged manifests, stale identity and identity remap:

```sh
make check RUN='^TestPgrowlocksProviderIdentity$'
```

Its public SQL, lifecycle and controls are in
[`reference/locking_transactions`](../../../reference/locking_transactions/README.md#installed-pgrowlocks).

