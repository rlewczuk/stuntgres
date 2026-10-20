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
(since 16b each declares the installer that embeds its stock provider),
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

### 16d — Move the installation handlers

Follow 16c. Remove the remaining aliases.

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
`extension`. Part B: CREATE/DROP EXTENSION handlers live there too. The
checks in §8.3 pass.

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
- [ ] 16c — Host refined (docs only; after 13d)
- [ ] 16d — Installation handlers moved

Outstanding aliases: none (16b moved without aliases).

Notes for later steps:

- 16c/16d: the engine installers (`*_stage.go`) are the staging seam to
  measure; when the handlers move, the installers become `extension`
  types again (or staging takes the provider as an argument) and the
  registry may hold the providers directly.
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
