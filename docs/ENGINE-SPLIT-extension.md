# Engine split, step 16: `internal/engine/extension`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-extension.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §6 step 16; §4.1 (two parts,
  measured clean sets).
- **Needs:** Part A: 15b (`typedef`), because `intarray` validation goes
  through type validation. Part B: 13d. Read the 5c record in
  [`ENGINE-SPLIT-catalog.md`](ENGINE-SPLIT-catalog.md) (the
  `extensionImplementation` seam) and the pgcrypto/fuzzystrmatch/unaccent
  records in [`ENGINE-SPLIT-functions.md`](ENGINE-SPLIT-functions.md) (the
  kernels already moved).
- **Effort:** high for 16a and 16b; xhigh for 16c; high for 16d.
- **Session size:** one sub-step per session.

## Goal

The installed-extension layer (provider records, control data, member
validation, ownership metadata and remap) becomes a feature package. The
function kernels are already in `functions`; installation stays in the engine
until Part B.

## Inventory (re-measured at `1ecaa1876` in 16a)

Since `75fe0d710` feature work added `cube`, provider `Versions()`, ALTER
EXTENSION UPDATE (`extension_update.go`; fuzzystrmatch `updates`,
`stageUpdate`, `stageMembers`) and `pg_extension_update_paths`
(`extension_paths*.go`). Before 16a, the extension files held 144
declarations: 99 clean, 45 dirty. Every provider record and its validation
was clean except intarray and cube validation, which called `typeProvider`
directly and so reached the binder through domain default validation. 16a
made them clean (Status below).

**16b move set** (17 whole files, about 1.3k lines; the frontier tool finds
no engine declaration they use; moved in 16b, see Status):

- providers: `cube_provider.go`, `fuzzystrmatch_provider.go`,
  `intagg_provider.go`, `intarray_provider.go`, `pgcrypto_provider.go`,
  `unaccent_provider.go`, `uuid_ossp_provider.go` (type, `Control`,
  `Versions`, stock instance, script contexts, `ValidateExtension`,
  `validateProcedure`);
- `extension_catalog.go`: `extensionCatalogProvider` (Extension, TS template
  and TS dictionary classes, plus installed member procedures),
  `validateProviderHeader`, `validateInstalledProcedure`,
  `extensionMembership`;
- `extension_type_validation.go` (`validateRegisteredType`),
  `extension_implementation.go` (the `*ProviderOf` accessors),
  `extension_remap.go`, `extension_owners.go` (owner metadata codec),
  `extension_errors.go`, `extension_authority.go`, `extension_removal.go`;
- `intagg_identity.go` (`sameIntaggIdentity`, `validateIntaggIdentity`,
  `intaggRecipeFunction`) and `intagg_aggregate.go` (`intaggDefinition`,
  `validateOwnedAggregate`, `remapOwnedAggregate`).

Imports of the move set: `authorization`, `catalog`, `expr`, `functions`,
`ids`, `parser`, `sqlstate`, `sqltype` and the standard library. No
`routine`, no `typedef`.

**Part B** (stay in the engine until 16d): the provider `*_stage.go` files
(since 16b each declares the installer that embeds its stock provider;
moved into `extension` as provider methods in 16d.1),
`extension_install.go` (`extensionInstaller`, `registerExtension`,
`executeExtension`, `publishExtensionStage`, `stageExtensionLocked`),
`extension_update.go`, `extension_drop.go`, `extension_owner_import.go`.

**Not extension code, stays in the engine:**

- `procedure_provider.go`: `procedureProvider`, the procedure-class
  validator for builtin, SQL, foreign and conversion routines (uses
  `routine` and `typedef` lookups). A candidate for `routine` Part B (14c).
- `procedure_lookup.go` (`procedureInNamespaces`, used by conversion,
  foreign, operator DDL and regproc; now `analyze.ProcedureInNamespaces`,
  12d.3b) and `namespace_children.go`
  (`namespaceChildren`, schema/DISCARD/TEMP retirement).
- `database_grant.go` (was `extension_database.go`): database GRANT and
  `initializeDatabase`.
- Node and binder code (decision of 16a item 4, see Status): the installed
  expression wrappers and the `pg_extension_update_paths` node moved to
  `plan` in 10d (`plan.InstalledTextExpr`, `plan.InstalledCryptoExpr`,
  `plan.InstalledUUIDOsspExpr`, the three fuzzystrmatch wrappers and
  `plan.ExtensionPathsExpr`); their provider validators,
  `closedPgcryptoDefault` and `cryptoExpressionSignature` stay in the engine
  with the binder (`validInstalledText`, `closedPgcryptoDefault` and
  `cryptoExpressionSignature` moved to `analyze` in 12d.3b); `EvalContext.FunctionPgcrypto*` with step 11; the intagg
  binder methods and `bindExtensionPaths` with step 12; the intagg view
  recipe methods with the postponed `view` package.

## Part A

### 16a — In place

1. Re-measure; update the inventory.
2. Split each provider file into its records (clean) and its `stage` method
   (Part B), so the clean halves move as files. Rename
   `extension_database.go`.
3. Give `intarrayProvider.ValidateExtension` and
   `extensionCatalogProvider.Validate` a narrow validation capability for
   the type checks they need, so they become clean (coordinate with step
   15's seam).
4. Decide where the installed expression wrappers
   (`installedCryptoFunctionExpression`, `installedUUIDOsspFunctionExpression`)
   go: they hold catalog identity (see the `expr` README); record the
   decision.

### 16b — Create `extension` and move the clean set

1. Export in place, then move with the `movex` approach.
2. Check imports: `catalog`, `functions` (kernels, if providers name them),
   `expr`, `authorization`, `ids`, `parser`, `sqltype`, `sqlstate` and the
   standard library. Not `typedef` unless 16a recorded it.
3. The catalog's `extensionImplementation` field and `catalog.Owner.
   Extensions` keep holding the implementation interface; only the concrete
   providers move.

## Part B

### 16c — Refine the host (docs only), after 13d

Measure `executeExtension`, `stageExtensionLocked`, the provider `stage`
methods, `executeDropExtension` and `importExtensionOwners`; write the host
interface and move set; wait for approval.

**16c refine (approved; measured at `1f606f52d`).** The `frontier` tool
(`.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-QUERY-PREP/frontier`; output in
`.cswd/tmp/PG18-ENGINE-SPLIT-EXTENSION-REFINE/`), with the 12 Part B files
(about 1.08k lines: the seven `*_stage.go` installers, 472 lines;
`extension_install.go` 201, `extension_update.go` 166,
`extension_drop.go` 92, `extension_owner_import.go` 116 and
`extension_authority_fence.go` 36) as the move set:

- **69 engine edges**, in five groups:
  - *Installer staging* (every `stage`, `stageUpdate`, `stageMembers`; all
    run under `Engine.mu`): `catalog.allocateOIDLocked`,
    `catalog.revisionLocked`, `CatalogStage.Create` and `LookupName`, the
    stage base (the `AuthorizationView` they receive is only used for
    `auth.Catalog`, which is the stage base), and for cube and intarray
    `displaceAssociatedArrayLocked`, `associatedArrayName` and
    `creationTypeACL`. unaccent also re-reads the stage (`stageRead`),
    recaptures the view, checks USAGE on the target schema as the member
    owner, builds `effectivePath(…, analyze.InstalledFunctionLookup)` and
    resolves its two helpers with `analyze.ProcedureInNamespaces`.
  - *CREATE and ALTER UPDATE staging* (`stageExtensionLocked`,
    `stageExtensionUpdateLocked`): `authorizationLocked`, notices,
    `explicitPath`, `AuthorizationView.Effective`/`Check`, the replay flag
    `operationReplayOrZero(op).tableDestination`, the bootstrap superuser
    `e.roles.domain.Principal(10)`, OID and revision allocation,
    `BeginCatalogStage`, `authorizeLocked`, `stage.extensionNamespace`,
    `Create`/`Replace`. The registry and the object domain are already
    reachable from the view (`Catalog.Owner().Extensions`, `.Domain`).
  - *Publication* (`publishExtensionStage`): stage validation and discard,
    the journal clone and original capture, `catalogChangeLocked`, the
    authority fence, `scope.catalog` and `scope.catalogAuthority`.
  - *DROP* (`executeDropExtension`): the target selection (names, notices,
    owner check) is extension logic; the rest is the root removal pipeline
    (`ownershipStage`, `referencePath`, `ownershipRelationLocks`,
    `acquireSequence`, `removeObjectLocked`, `stageExpressionDependencies`,
    `stageOwnedSequences`, the replacement read release, `publishOwnership`,
    `viewCascadeNames`), the same shape DROP OWNED has in `owned_roles.go`.
  - *Owner import and fence*: `importExtensionOwners` needs `Engine.mu`, the
    import authority (`importTargets`), the cloned state, `roles.read` and
    `reownCatalogExpressions`; its per-record matching loop reads only the
    cloned `*catalog.State` and an `authorization.Read`. The fence check
    reads only the journal names, `catalog.state` and the published role
    revision; `catalogFence` and the fence kinds are root.
- **Inbound:** production uses only `execute.go` (three dispatch calls),
  `control.go` (`importExtensionOwners`) and `engine.go` (the registration
  list); 14 test files build installers or call the handlers
  (`aggregate_identity_test.go`, `available_extension_sources_test.go`,
  `cube_provider_test.go`, `extension_fences_test.go`,
  `extension_provider_test.go`, `extension_version_graph_test.go`,
  `extension_version_manifest_test.go`, `extension_version_owners_test.go`,
  `extension_version_publication_test.go`, `intagg_routine_authority_test.go`,
  `intarray_provider_test.go`, `pgcrypto_provider_test.go`,
  `uuid_ossp_namespace_test.go`, `uuid_ossp_provider_test.go`).
- **Layering.** `analyze` imports `extension`, so `extension` can never
  import `analyze`: unaccent's helper lookup goes through the host.
  `extension` may import `access`, `session` and `typedef` (none of them
  imports it).

Decisions:

1. **Providers own staging; the installers go.** `extension.Installer` is
   `catalog.ExtensionImplementation` plus `Stage(Stage, *catalog.Extension,
   authorization.PrincipalRef) error`; fuzzystrmatch also implements
   `Updater` (`Updates() []UpdateAction`, each an edge and a stage
   function). The registry holds the `Stock*` providers, registered in the
   same order. Installed members keep the same provider pointer, so every
   `*ProviderOf(d) != Stock*` check and the validators are unchanged.
   Rejected: moving the installer wrapper types into `extension` (two types
   per provider for nothing, once staging can be a provider method).
2. **Staging host `extension.Stage`.** Every method is called with
   `Engine.mu` held: `Read() catalog.Read` (the stage base), `LookupName`,
   `Create`, `Replace`, `AllocateOID`, `Revision`,
   `DisplaceAssociatedArray`, `AssociatedArrayName`, `CreationTypeACL` and
   `ScriptFunctions(owner, namespace, helpers)`. The last one does
   unaccent's whole script lookup in one call (staged read, recapture,
   USAGE check, effective path, one lookup per helper, nil for a missing
   one); `extension` keeps the 42883 texts and their script context.
   Staging loses its `*Engine` and `AuthorizationView` parameters.
3. **Statement host `extension.Host`.** Called with `Engine.mu` held:
   `Context`, `Notice`, `CreationPath` (the explicit path),
   `ReplayingTableDestination`, `BootstrapSuperuser`, `AllocateOID`,
   `Revision` and `BeginStage(namespace) Stage` (begins and authorizes the
   stage, sets its extension namespace). Authority checks go through
   `extension.Authority` (`Check`, `Effective`, which `AuthorizationView`
   already has) and the captured `access.View` values. One root adapter,
   `extensionHost` (engine, operation, captured `AuthorizationView`, and
   the stage once begun), implements `Host` and `Stage` with a compile-time
   assertion each; a checked `stageOf` gives the root its `*CatalogStage`
   back. It is not `statementHost` (§7 decision 8): staging runs under
   `Engine.mu` with a captured view and a stage, as 12c's `bindingHost`
   runs over the engine and operation. It is not a defined type over
   `CatalogStage` either: that would drop `CatalogStage`'s checked
   `Create`/`Replace` and silently promote `catalog.Candidate`'s unchecked
   ones.
4. **Move set.** The staging bodies (as provider methods), `extensionActions`,
   `validateExtensionActions` and `stageExtensionPath` (as `Actions`,
   `ValidateActions`, `StagePath`), `StageCreate` and `StageUpdate` (from
   the two `stage…Locked` functions), `DropTargets` (the DROP selection),
   `ImportOwners(ctx, *catalog.State, authorization.Read, records)
   (changed bool, err error)` (the matching loop) and
   `AuthorityObservation.Current(names, state, roleRevision)` (the fence
   check).
5. **Stays in the root (orchestration, as 13c decision 1):**
   `executeExtension` and `executeExtensionUpdate` (argument count,
   `Engine.mu`, then publication), `publishExtensionStage`,
   `executeDropExtension` (lock, `DropTargets`, the removal pipeline, the
   multi-target 2BP01 text, the cascade notice), `importExtensionOwners`
   (lock, import authority, clone, reown, publish), `extensionAuthorityFence`
   (a three-line wrapper over `Current`), `registerExtension` (the
   registry-closed check; it calls `ValidateActions`), the registration list
   in `engine.go` and `extensionHost`. "Done when" is corrected to match:
   the handlers' decisions live in `extension`; dispatch and publication
   stay in the root.
6. **No aliases.** Every moved name is unexported engine code with no user
   outside the engine, so 16d needs no alias step.
7. **Invariant to guard:** OID and revision allocation order, and the order
   of checks (so the first error), stay identical: installed OIDs, native
   Dump bytes, messages and SQLSTATEs must not change.

### 16d — Move the installation handlers (two sessions)

Follow 16c. Each session re-measures its move set with `frontier`, checks
with `go list` that `extension` imports neither `analyze` nor `routine`,
`plan` or the engine, and runs the targeted engine units after every
commit, the 16a reference selection in quick mode and with `REFERENCE=1`,
the 16a public smoke (identical output on base and branch),
`make quickcheck` and `make deps`.

#### 16d.1 — Staging (linear item 51a)

1. Seam in place: declare `Stage`, `Installer`, `Updater`, `UpdateAction`
   and the script helper record in `extension`; add the root `extensionHost`
   implementing `Stage`; port the seven installers' staging onto it (unaccent
   through `ScriptFunctions`). Write the seam unit test first and see it
   fail: the adapter's `Create` refuses a discarded stage.
2. Move: staging becomes provider methods in `extension` (one
   `*_stage.go` per provider there), the installer types are deleted, the
   registry holds the providers, and the three action helpers move. Adapt
   the tests that build installers or call `stage`
   (`TestExtensionMembersHoldBareProviders` keeps its member assertion; the
   `extensionVersionRegistration` fixture embeds the provider).
3. `make bench` before and after (registration runs at startup).

#### 16d.2 — Handlers (linear item 51b)

1. In place: declare `Host` and `Authority`; rewrite `stageExtensionLocked`
   and `stageExtensionUpdateLocked` over them in the root; turn the DROP
   selection, the owner-import loop and the fence check into functions over
   values.
2. Move them into `extension` (`StageCreate`, `StageUpdate`, `DropTargets`,
   `ImportOwners`, `AuthorityObservation.Current`); update the
   `extension` README (Part B contract, lock contract of both hosts) and
   the "Stays in the engine" lists.

## Invariants

- Installed members, their OIDs, namespaces, owners and dependency records
  stay identical; CREATE/DROP EXTENSION messages and SQLSTATEs unchanged.
- Extension remap and owner metadata in native Dump stay byte-identical.
- Provider registration order on engine startup is unchanged.

## Targeted tests

- Engine units: grep `*_test.go` for each provider type, `extension.`,
  `DecodeOwners`, `RemapDescriptor` and the installers.
- Reference selections (quick mode and `REFERENCE=1`; grep `reference/`):
  CREATE/DROP EXTENSION, each extension's functions (fuzzystrmatch, intagg,
  intarray, pgcrypto, unaccent, uuid-ossp), `pg_extension`,
  `pg_available_extensions`, and native Dump/Restore with extensions.
- `make bench` after 16b (provider registration runs at startup).

## Documentation

- Create `internal/engine/extension/README.md` (16b).
- The `functions` README names provider files; update it.
- `AGENTS.md`: add `extension` to the shared subpackage bullet.

## Done when

Part A: provider records, validation, owner metadata and remap live in
`extension`. Part B: provider staging and the decisions of the CREATE,
ALTER EXTENSION UPDATE and DROP EXTENSION handlers and of the owner import
live there too; dispatch, `Engine.mu` and publication stay in the root
(16c decision 5). The checks in §8.3 pass.

## Status

- [x] 16a — In place (`d0a013406`, `bc0e3d81b`, `54d886952`, `46da1763e`,
  then the docs commit) *(racecheck/finalcheck skipped at user request)*
  - Re-measured with the reach and frontier tools of 15b at `1ecaa1876` and
    after the work; the inventory above is current.
  - `d0a013406` (splits, declarations unchanged): each provider's `stage`
    (fuzzystrmatch also `updates`, `stageUpdate`, `stageMembers`) moved to a
    sibling `*_stage.go`; `unaccentProvider.ValidateExtension` and
    `unaccentTemplateContext` joined `unaccent_provider.go`; the clean
    helpers left the handler files (`extension_errors.go`,
    `extension_authority.go`, `extension_removal.go`, `intagg_identity.go`,
    `extension_owner_import.go` takes `importExtensionOwners` out of
    `extension_owners.go`). `procedureInNamespaces` and `namespaceChildren`
    are generic and got engine files. `extension_database.go` →
    `database_grant.go`, `aggregate_identity.go` → `intagg_aggregate.go`
    (its regproc helper `routineDescriptorOwner` → `regproc_lookup.go`).
  - `bc0e3d81b` (item 3, capability): intarray and cube validate their
    associated array through `validateRegisteredType`, which takes the type
    provider from `catalog.Owner.Provider(ids.TypeClassID)`, the immutable
    registry the `catalog.Read` already carries (§4.1 pattern 3). No new
    interface: the registry is the narrow seam. A live engine always
    registers `typeProvider` there. `typeProvider.Validate` still calls
    `stockIntarray`/`stockCube` (engine → extension after 16b).
  - `54d886952` (deviation, approved): `extensionCatalogProvider.Validate`
    also validated every builtin, SQL, foreign and conversion procedure,
    with `routine.BuiltinRoutine` and `typedef.ConversionRoutineName`.
    `procedureProvider` now owns the procedure class: routines without an
    extension implementation keep their checks in the old order, routines
    with one are delegated to `extensionCatalogProvider`, which rejects
    everything else. The shared address/name/namespace check is
    `validateProviderHeader`. Registration keeps its order
    (`extensionCatalogProvider{Extension}`, `procedureProvider{}`, TS
    template, TS dictionary). The nine direct procedure-class validations
    (`control.go`, `generated.go`, `temporary_provenance.go`) and
    `TestRoutineReleaseInventory` use `procedureProvider`. Every descriptor
    an engine can create validates with the same result and error; only an
    unknown, non-engine implementation type on a conversion routine (not
    constructible: `registerExtension` takes the unexported
    `extensionInstaller`) would now be rejected.
  - `46da1763e` (beyond the plan): `validateOwnedAggregate` read the engine's
    `routineByOID`. The lookup-only REL_18_0 identity table
    (`regproc_identities.go`, pure `catalog.RoutineIdentity` data, no
    generator) moved to `catalog/builtin_routine_identities.go` with
    `catalog.BuiltinRoutineIdentities()` (a copy) and
    `catalog.BuiltinRoutineIdentityByOID`; the engine keeps its name and
    signature indexes, built from it once at package init.
  - Item 4 (wrappers): the installed expression wrappers and the
    `pg_extension_update_paths` node are plan nodes whose catalog identity is
    engine state (`expr` README); they stay with node code and move with
    `plan`, not `extension` (inventory above).
  - Tests: new seam tests `TestInstalledCodecArrayUsesRegisteredTypeProvider`
    (`extension_type_validation_test.go`) and
    `TestExtensionProviderValidatesOnlyInstalledProcedures`
    (`procedure_provider_test.go`), written first and observed failing.
    `TestRoutineReleaseInventory`, `TestObjectAddress*` and the
    `pg_aggregate` test read the identity table through the catalog
    accessors (OID uniqueness now checked per fact); none moved or deleted.
  - Verification: build, vet (only the existing `composites`,
    `copylocks` and `lostcancel` findings), 117 targeted engine unit tests
    after every commit, a 356-test reference selection (every test in a
    `reference/` file naming CREATE/ALTER/DROP EXTENSION, `pg_extension`,
    `pg_available_extensions`, an extension member, regproc or `pg_proc`) in
    quick mode and with `REFERENCE=1`, `make quickcheck` and `make deps`
    passed on the base `1ecaa1876`. With `REFERENCE=1` only
    `TestReferenceObjectACLDatabase/postgres` fails: order-dependent
    PostgreSQL-side state; it passes alone and fails the same way on a clean
    `1ecaa1876` snapshot. Layering: no engine subpackage imports the engine.
    A public `database/sql` smoke (CREATE/ALTER/DROP EXTENSION for all seven
    extensions, members in tables and views, `pg_extension`, regproc,
    native Dump/Restore round trip) printed identical output on base and
    branch. Startup bench, interleaved 8 runs each on a loaded host (load
    16–25): median 1,137 µs/op before, 1,077 µs/op after; about 5,026
    allocs/op before and 5,022 after, 1.64 MB/op on both. Samples above 1 ms
    come from host contention. After rebasing on `cdf48fdd6` (session 13a)
    build, the targeted units, the quick-mode reference selection, the
    frontier check and `make quickcheck` passed again. Racecheck and
    finalcheck skipped at user request.
- [x] 16b — `extension` created and clean set moved (`e6ff96286`,
  `a8f3f5a8e`, `d55a47824`, then the docs commit) *(racecheck/finalcheck
  skipped at user request)*
  - Re-measured at `01c1a9ec4` and again at `28a0934f1` (after 10c) with the
    16a frontier tool: the 17 files use no engine declaration. 10c had
    already moved `sameIntaggIdentity` to `plan.SameIntaggIdentity`, so it
    is not part of the move.
  - `e6ff96286` (seam, deviation approved at planning): the plan kept each
    provider's `stage` in the engine, but Go cannot declare engine methods
    on a type of another package. Each `*_stage.go` now declares an
    installer (`cubeInstaller struct{ *extension.CubeProvider }`, …) that
    owns `stage` (fuzzystrmatch also `updates`, `stageUpdate`,
    `stageMembers`). `extensionInstaller` is unchanged; the registry
    (`catalog.Owner.Extensions`) holds the installers, registered in the
    same order; installed members keep the bare stock provider in
    `Implementation`, so every `*ProviderOf(d) != Stock*` check and the
    validators are unchanged. Rejected: a type switch from provider to
    stage or a second installer map (a parallel registry).
  - `a8f3f5a8e` (export in place): the `movex` member mode renamed 66
    names, fields and methods, dropping the `extension` prefix the package
    carries (`CatalogProvider{Class}`, `Membership`, `OwnerRecord`,
    `MemberOwner`, `OwnersMarker`, `OwnerMetadata`, `DecodeOwners`,
    `RemapDescriptor`, `MemberConflict`, `NameConflict`, `VersionError`,
    `AuthorityObservation`, `RemovalRoots`) and capitalizing the rest
    (`CubeProvider` … `UUIDOsspProvider`, `StockCube` …, `*ProviderOf`,
    `ValidateProcedure`, `UnaccentProvider.Matcher`, script contexts,
    signatures, `FuzzystrmatchMembers`/`IntaggMembers` and their fields,
    `IntaggDefinition`, `ValidateIntaggIdentity`, `IntaggRecipeFunction`,
    `ValidateProviderHeader`, `InvalidProviderDescriptor`). Names used only
    inside the set stay unexported, including the providers' `control`.
  - `d55a47824` (move): 17 files moved into `internal/engine/extension`;
    `movex` qualified the engine references in 72 files. Locals named
    `extension` that would shadow the package were renamed (`catalog.go`,
    `engine.go`, `installed_routine.go`, `namespace_children.go`).
    `extension` imports `authorization`, `catalog`, `expr`, `functions`,
    `ids`, `parser`, `sqlstate`, `sqltype` and the standard library; no
    `routine`, `typedef` or `plan`.
  - The first pass was made on `01c1a9ec4`; 10c landed meanwhile, so the
    export and move were regenerated on `28a0934f1` with the same rename
    table instead of merging conflicts. The resulting package is identical
    to the first pass except for the function 10c had already moved.
  - Tests: new seam test `TestExtensionMembersHoldBareProviders`
    (`extension_provider_test.go`), written first and observed failing.
    Adapted: the registry identity assertions in
    `TestExtensionProviderIdentity` and `available_extension_sources_test.go`
    compare the installer's provider; the `extensionVersionRegistration`
    fixture embeds `fuzzystrmatchInstaller`; direct `stage`/`updates` calls
    and registrations use installers; ten tests that built a foreign
    provider through the private `control` field copy the stock provider
    (`new(*extension.StockIntagg)`, same version). No test moved or was
    deleted: every witness needs an engine that installs the extension.
  - Verification on `28a0934f1`: build, vet (only the existing
    `composites`, `copylocks` and `lostcancel` findings), the 118 targeted
    engine units after every commit, the 356-test 16a reference selection
    in quick mode and with `REFERENCE=1`, `make quickcheck` and `make deps`
    passed. With `REFERENCE=1` only `TestReferenceObjectACLDatabase/postgres`
    fails, the known order-dependent PostgreSQL-side state from 16a; it
    passes alone. Layering: no engine subpackage imports the engine. The
    16a public smoke printed identical output on base and branch. Startup
    bench, interleaved 8 runs each (load 5–12): median 934 µs/op before,
    919 µs/op after, samples 844–1,842 µs/op on both sides; 5,020–5,027
    allocs/op and about 1.64 MB/op on both. Racecheck and finalcheck
    skipped at user request.
- [x] 16c — Host refined (docs only; the commit that records this text)
- [x] 16d — Installation handlers moved (two sessions, per the 16c plan)
  - [x] 16d.1 — Staging: providers own staging behind `extension.Stage`
    (`9b88e37b1`, `6ee7b321c`, then the docs commit) *(racecheck/finalcheck
    skipped at user request)*
    - Base `204022e05`. The frontier tool over the seven `*_stage.go`
      files and the three action helpers, after the seam commit, reports
      no engine declaration used (OUT empty); inbound uses were only
      `engine.go`, `extension_install.go`, `extension_update.go` and six
      test files.
    - `9b88e37b1` (seam in place): `extension/stage.go` declares `Stage`,
      `ScriptFunction`, `Installer`, `Updater` and `UpdateAction` as 16c
      decisions 1 and 2 describe. `ScriptFunctions(owner, namespace,
      helpers) ([]*catalog.Procedure, error)` returns nil for a missing
      helper; unaccent checks them in order and keeps its 42883 texts, so
      the first error is unchanged (both lookups are pure reads). The root
      `extensionHost` (`extension_host.go`: engine, captured
      `AuthorizationView`, `*CatalogStage`; compile-time assertion)
      implements `Stage` by delegating to the existing root code; `Read`
      is the stage base, which is the `auth.Catalog` cube and intarray read
      before. `ScriptFunctions` works on a copy of the view. The operation
      field, `Host` and `stageOf` come with 16d.2. The seven installers
      staged through the host; `extensionInstaller` became
      `extension.Installer`.
    - `6ee7b321c` (move): the seven files moved with `git mv` into
      `extension` as `Stage` methods on the providers (fuzzystrmatch also
      `Updates`, `stageUpdate`, `stageMembers`); the installer types are
      deleted. `extensionActions`, `validateExtensionActions` and
      `stageExtensionPath` moved to `extension_actions.go` as `Actions`,
      `ValidateActions` and `StagePath`. The registry holds the `Stock*`
      providers in the same order. `extension` now imports `typedef`
      (`RelationTypeNameOccupied`); `go list -deps` shows no engine,
      `analyze`, `routine` or `plan`.
    - Tests: new seam test `TestExtensionHostRefusesDiscardedStage`
      (`extension_host_test.go`), written first and observed failing.
      Adapted: `TestExtensionMembersHoldBareProviders` keeps its member
      assertion (`implementation != tc.stock`) and checks that the registry
      holds the stock provider; `TestExtensionProviderIdentity` and
      `available_extension_sources_test.go` compare to the stock provider;
      the `extensionVersionRegistration` fixture embeds
      `*extension.FuzzystrmatchProvider`; the direct staging calls in
      `aggregate_identity_test.go`, `cube_provider_test.go` and the two
      uuid-ossp tests call the providers with an `extensionHost`. No test
      moved or was deleted.
    - Verification: build, vet (only the existing `composites` and
      `copylocks` findings), the 119 targeted engine units after every
      commit, the 356-test 16a reference selection in quick mode, the 16a
      public smoke (85 lines, identical on base and branch), `make
      quickcheck` and `make deps` passed. With `REFERENCE=1`, the 123
      reference tests named for an extension in files that run CREATE or
      ALTER EXTENSION (eight packages) passed. Layering: no engine subpackage imports the
      engine. Startup bench, interleaved 8 runs each (load 8–10): median
      1,038 µs/op before, 997 µs/op after, samples 859–1,202 µs/op on both
      sides; 5,020–5,023 allocs/op and about 1.64 MB/op on both.
  - [x] 16d.2 — Handlers: CREATE/UPDATE staging, DROP selection, owner
    import and fence check behind `extension.Host` (`b1398311f`,
    `659f7699d`, `ff925b36e`, then the docs commit) *(racecheck/finalcheck
    skipped at user request)*
    - Base `909cd38fc`, rebased on `3c6a855bc` (15d and 17d; neither
      touched the extension files except `extension_drop_test.go`). The
      frontier tool over the five in-place functions reported one engine
      declaration used, `sqlError`; `extension` got its own unexported
      copy. Inbound uses were only the five root wrappers.
    - `b1398311f` (seam): `extension/host.go` declares `Host` (`Context`,
      `Notice`, `CreationPath`, `ReplayingTableDestination`,
      `BootstrapSuperuser`, `AllocateOID`, `Revision`, `BeginStage`) and
      `Authority` (`Check`, `Effective`) as 16c decision 3 describes.
      `extensionHost` gains the operation and implements `Host`;
      `BeginStage` begins the stage from the captured catalog, authorizes
      it and sets the extension namespace, and the checked `stageOf`
      returns the `*CatalogStage` (refusing a foreign `Stage` or a host
      with no begun stage). Compile-time assertions for `Host`, `Stage` and
      `AuthorizationView` as `Authority`.
    - `659f7699d` (in place): CREATE and UPDATE staging became root
      functions over `Host`, `Authority` and the captured `access.View`;
      the registry and the object domain are read from `view.Owner`, the
      same `catalog.Owner` the root embeds. DROP selection, the owner
      import loop and the fence check became functions over values.
      `stageExtensionLocked` and `stageExtensionUpdateLocked` stay as thin
      root wrappers with their signatures, so their ten test callers are
      unchanged.
    - `ff925b36e` (move): `StageCreate` (`extension_create.go`),
      `StageUpdate` (`extension_update.go`), `DropTargets`
      (`extension_drop.go`, notices through a callback), `ImportOwners`
      (`extension_owner_import.go`) and `AuthorityObservation.Current`
      (`extension_authority.go`). The root keeps `registerExtension`,
      `executeExtension`, `executeExtensionUpdate`,
      `publishExtensionStage`, `executeDropExtension`,
      `importExtensionOwners`, `extensionAuthorityFence` and
      `extensionHost`. `extension` now imports `access`; `go list -deps`
      shows no engine, `analyze`, `routine` or `plan`.
    - Lesson: the first move pass stripped the `extension.` qualifier with
      a word regex, which also cut "this extension." out of the two
      42501 CREATE privilege hints. The 119 targeted engine units passed;
      the quick-mode reference selection caught it (four tests). Fixed in
      the move commit; a literal-by-literal comparison of the handler code
      before and after the move now matches exactly. Strip qualifiers with
      a type-checked tool, or compare string literals after a regex move.
    - Tests: new seam test `TestExtensionHostBeginStageHandsBackRootStage`
      (`extension_host_test.go`), written first and observed failing
      against stubs. No test moved, adapted or deleted.
    - Verification: build, vet (only the existing `composites` and
      `copylocks` findings), the 120 targeted engine units after every
      commit and after the rebase, the 356-test 16a reference selection in
      quick mode, the 16a public smoke (85 lines, identical on base and
      branch), `make quickcheck` and `make deps` passed. With
      `REFERENCE=1`, the 123 extension reference tests of 16d.1 passed.
      Layering: no engine subpackage imports the engine. `make bench` not
      run: registration and startup are unchanged.

Outstanding aliases: none (16b moved without aliases).

Notes for later steps:

- 16c/16d: the engine installers (`*_stage.go`) were the staging seam;
  16c decision 1 settles it: staging becomes a provider method behind
  `extension.Stage` and the registry holds the providers directly (done in
  16d.1).
- 16d.2 (done): `extensionHost` gained the operation and `Host`;
  `StageCreate` and `StageUpdate` call `Installer.Stage` and `StagePath` on
  the stage `BeginStage` returns.
- 16d, for other Part B steps (14c, 15c, 17c): the staging host shape
  (16c decisions 2 and 3: one root adapter over engine, operation, view and
  stage; not a defined type over `CatalogStage`) applies to any feature
  whose DDL stages catalog changes. `displaceAssociatedArrayLocked`,
  `associatedArrayName` and `creationTypeACL` are also used by domain,
  enum, composite, view, table and schema DDL;
  they stay root functions behind the host methods.
- 10d (done): the installed wrappers' provider checks
  (`extension.PgcryptoProviderOf(proc) != extension.StockPgcrypto`,
  `extension.Stock*.ValidateProcedure`) became engine functions taking the
  node (`validInstalledText`, `validateInstalled*`); only the node types
  moved to `plan`, so `plan` names no extension type.
- 14c/14d: `procedureProvider` (builtin, SQL, foreign and conversion
  routine validation) is routine code; `routineByOID`'s callers now use the
  catalog identity table.
- Unrelated, found by the 16a public smoke on base and branch alike:
  after `CREATE TABLE t (id int, q query_int)` and
  `CREATE VIEW v AS SELECT id FROM t`, `DROP EXTENSION intarray CASCADE`
  fails with the internal error `stuntgres: foreign view query type`
  (PostgreSQL drops only column `t.q`). Without the view, or with a view
  that reads `q`, it succeeds. Needs a reference test and a fix (not split
  work).
- Stale citation, not fixed because `reference/` is read-only for split
  work: `reference/type_resolution/comparison-ledger.md` cites
  `internal/engine/intarray_provider.go:47`, now
  `internal/engine/extension/intarray_provider.go`.
