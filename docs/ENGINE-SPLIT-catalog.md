# Engine split, step 5: `internal/engine/catalog`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-catalog.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §5 step 5; cuts 1, 2, 8 and 9
  and the extra opaque payloads in §2.3; §2.2 for the shape of today's map
  access.
- **Needs:** step 1 (`ids`) and step 2 (`expr`) through 2b. Independent of
  steps 3 and 4.
- **Effort:** high for 5a, 5d and 5e; xhigh for 5b (because of cut 1) and for
  5c.
- **Session size:** one sub-step per session. 5b may take two sessions:
  accessors and script first, then cut 1.

## Goal

"Catalog" here means the object model and its immutable snapshots, not DDL.
The package `catalog` owns descriptors, `State`, `Read`, NameKey helpers, the
provider registry, validation of core kinds, and a pure copy-on-write overlay
(`Candidate`). It imports only `ids`, `expr`, `authorization`, `sqltype`,
`parser` and `sqlstate`. DDL commands, publication orchestration and
`CatalogStage` stay in the engine.

## Sub-steps

### 5a — State ownership in place

- Group `Engine.owner` (now the `ids.Domain`), `state`, `providers`,
  `nextOID`, `nextRevision` and `nextEnumMember` into one engine-local
  `catalogStore`. In refinement, decide whether `extensions`,
  `databaseName` and `foreignNamespaces` belong there too, and record the
  reasoning.
- `Engine` methods that only touch this state become `catalogStore` methods.
- `catalogStore` has no mutex. Its mutating methods carry the `Locked` suffix
  or document that the caller holds `Engine.mu`.
- Correction found in 5a: the domain is engine-wide. It also mints session
  tokens and transaction references and seeds the lock manager. The store owns
  it as the engine's single provenance domain.

### 5b — Typed read API and cut 1

- Add typed accessors to `CatalogRead`: `Relation`, `Namespace`, `Type`,
  `Procedure`, `Operator`, `Collation`, ..., plus name lookup.
- Migrate the direct `state.descriptors[...]` reads with a script. At
  `a2252c0b1` there were about 1,550 map reads, 926 of them typed assertions:
  about 560 comma-ok forms and about 370 single-value forms. A single-value
  assertion panics when the descriptor is absent or has another kind.
  Preserve each form's semantics exactly, for example with a comma-ok accessor
  and an explicit "must" accessor. Never turn a panic into a silent nil.
- Confine the map writes (38 sites at `a2252c0b1`) to publication code.
- **Cut 1.** `CatalogRead.owner *Engine` serves as a provenance check and as a
  service locator (owner token, authorization domain, roles, providers).
  Replace it with a catalog owner record holding the domain and the provider
  registry, plus explicit parameters. The authorization domain and roles come
  from `AuthorizationView`, not `CatalogRead`. `CatalogRead.Comment` becomes
  an engine function.

### 5c — Payload seams (cuts 8, 9 and the extra payloads)

- Views, rules and policy presentation become catalog-defined payload
  interfaces that expose only what catalog code needs, such as dependencies
  and temporary ownership. The concrete `viewDefinition` and `ruleDefinition`
  (including `ruleDefinition.conditionBinding *binding` and
  `ruleAction.binding`) stay in the engine behind one checked accessor each,
  for example `viewOf(d)`.
- The extension provider pointers on `procedureDescriptor`
  (`unaccentProvider`, `pgcryptoProvider`, ...) become one implementation
  interface.
- Expression payloads (check, default, generation, index keys and predicates,
  domain checks, policy expressions) hold `expr.Expr`.

### 5d — Export descriptor fields

- Decide the exported type names first, for example `relationDescriptor`
  becoming `catalog.Relation` and `catalogState` becoming `catalog.State`.
  Record them here.
- Export fields one at a time with `gopls rename`. `relationDescriptor` alone
  had about 7,800 field uses in 357 files at `a2252c0b1`, so this must be
  tooled.
- Fields that collide with `Descriptor` interface methods (`address`, `name`,
  `namespace`, `revision`) are read through those methods.
- This is a broad rewrite: do not run it in parallel with another (§8).

### 5e — Move

- Move the descriptor types and their records (the 64 types listed in §2.3),
  `catalogState` as `catalog.State`, `CatalogRead` as `catalog.Read`, the
  NameKey helpers, the provider registry and the validation of core kinds.
- Split the pure overlay out of `CatalogStage` as `catalog.Candidate`:
  descriptors, names, touched names, removals and default ACLs, with
  `Create`, `Replace`, `Remove` and name lookup. `CatalogStage` stays in the
  engine, embeds the `Candidate` and keeps the publication coordination
  (storage row shapes, index candidates, domain validation, materialized
  replacement, role candidates). That is cut 2.
- Leave aliases in the engine.

### 5f — Remove the aliases (separate session)

Replace the aliases area by area with a tool aware of `go/types`. This is a
broad rewrite (§8).

### 5g — Optional: regroup `relationDescriptor` (needs user approval)

Group its fields into sub-records (column metadata, constraints, security,
kind payloads) as a separate readability change. Ask before starting.

## Invariants

- Published `State` values stay immutable. Publication stays atomic across
  catalog, storage and visibility under `Engine.mu`.
- Copy-on-write sharing is unchanged: no new deep copies on publication or
  capture.
- Epochs, revisions, OID allocation, name resolution, dependency order and
  native Dump/Restore output stay identical.
- Every provenance check (foreign, stale, zero) still rejects what it rejects
  today.

## Targeted tests

- Catalog, publication, name-resolution and Dump/Restore unit tests. Use
  `RACE=1` for publication and capture.
- `make bench` after every sub-step: builtin catalog seeding is on the startup
  path.
- 5c and 5e: reference selections for table, type and routine DDL, views and
  rules, policies, `reg*` types, extensions and native Dump/Restore, run with
  `REFERENCE=1` and in quick mode. Pick names by grepping `reference/`; never
  edit it.

## Documentation

- Create `internal/engine/catalog/README.md`: what the package owns, the
  immutability and copy-on-write rules, the read API, the payload interfaces,
  what stays in the engine (DDL, `CatalogStage`, publication), and
  `## Testing` selectors moved from `internal/engine/README.md`.
- Update the `authorization` README's references to `CatalogRead`.
- Design notes and docs that named catalog internals at `a2252c0b1`:
  - `PG18-ARCH-CATALOG-IDENTITY.md` first;
  - `docs/TABLES.md` and `docs/AUTHORIZATION.md`;
  - the design notes `PG18-ARCH-TX-VISIBILITY.md`, `PG18-ARCH-TYPE-SYSTEM.md`,
    `PG18-ARCH-SESSION.md`, `PG18-ARCH-AUTHORIZATION.md`,
    `PG18-TYPE-REG-TYPE.md`, `PG18-INDEX-BTREE.md`,
    `PG18-INDEX-MAINTENANCE.md`, `PG18-DDL-UNIQUE.md`,
    `PG18-DDL-ALTER-CONSTRAINT-RENAME.md` and `pg-operator-pg18-oracle.md`.

  Grep for more.
- `AGENTS.md`: add `catalog` to the shared subpackage bullet. Drop "catalog"
  from the `internal/engine` bullet, keeping "lifecycle publication". Check
  the sequence and prepared-statement bullets for wording that names moved
  internals.

## Done when

`catalog` imports only `ids`, `expr`, `authorization`, `sqltype`, `parser` and
`sqlstate`. The engine has no direct access to the `State` maps.
`CatalogStage` embeds `catalog.Candidate`. The checks in §8.3 pass with no
startup regression. 5f completes the step; 5g is optional.

## Status

- [x] 5a — `catalogStore` state ownership (`41d5fbda9`, `9a0e50246`, then
  the commit that records this status) *(full racecheck skipped at user
  request; targeted RACE=1 runs passed)*
- [x] 5b — Typed read API, map reads migrated, writes confined, cut 1
  (`17a54431b`, `ddd9eb246`, `0175300e5`, `5aeaedcdc`, then the commit that
  records this status) *(full racecheck skipped at user request; targeted
  RACE=1 runs passed)*
- [x] 5c — View, rule, policy, extension-provider and expression payload
  seams (`3f4eb275c`, `0559a8d7b`, `1e2576d18`, `b4d7e495b`, then the commit
  that records this status) *(racecheck skipped at user request)*
- [ ] 5d — Descriptor names decided, fields exported
- [ ] 5e — `catalog` created, types moved, `Candidate` split from
  `CatalogStage`
- [ ] 5f — Aliases removed
- [ ] 5g — Optional `relationDescriptor` regrouping

Exported names chosen: none yet.

Outstanding aliases: none yet.

5a results:

- **Store.** `catalogStore` (`internal/engine/catalog_store.go`) holds
  `domain`, `state`, `providers`, `nextOID`, `nextRevision` and
  `nextEnumMember`. `Engine` holds it by value as `catalog` and never
  replaces it whole. All 4,160 selector uses (about 730 in production code)
  were rewritten to `e.catalog.<field>` by a type-checked tool, including
  uses reached through `CatalogRead.owner` and `CatalogStage.owner`.
- **Domain.** The store owns `ids.Domain`, as the user decided. The domain is
  the engine's single provenance domain: it also mints session tokens
  (`Session`, `PrivateSession`) and transaction references (`Tx`), and
  `lock.New` receives it. Those 41 production sites read `e.catalog.domain`.
- **Methods moved.** `allocateOIDLocked`, `revisionLocked` and
  `allocateEnumMemberLocked`. The provider registry gained
  `registerProviderLocked` and `provider`. `RegisterCatalogProvider` keeps
  its nil and zero-class checks, takes `Engine.mu` and delegates.
- **Methods kept on `Engine`.** `validateObjectRefLocked`,
  `validateCatalogViewLocked` and `CatalogRead()` compare against or build
  `CatalogRead.owner *Engine` (cut 1, 5b). `privateCatalogLocked`,
  `catalogChangeLocked` and `stageCatalogBaseLocked` belong to the
  transaction catalog journal, which stays with the transaction owner (§2.3).
  About 30 feature methods (`bindGeneration`, `freezeQueryPresentationLocked`,
  `reownCatalogExpressions`, ...) touch only the domain or state, but they
  mint references or read state as feature code. Seeding helpers
  (`registerCatalogSource`, `initialize*`) wait for 5b's write confinement.
  Publication code still reads and assigns `e.catalog.nextRevision` directly,
  because the frontier is committed with `state` under `Engine.mu`.
- **Fields left out of the store.** `extensions` is the extension feature's
  registry of provider implementations. It closes at construction, and the
  catalog reads it only through the `CatalogRead.owner` service locator
  (`extension_catalog.go`, cut 1). `databaseName` and `databaseNameError`
  are construction configuration for session admission and for the
  `NewWithSettings` calls in `Reset` and `principal_remap.go`; the catalog's
  copy is `databaseDescriptor.name`. `foreignNamespaces` is FDW registration
  metadata that belongs with `foreignWrappers` and `foreignValidators`.
- **Tests.** Adapted mechanically by the same rewrite (two composite
  literals by hand: `newEngine` and `operator_index_test.go`). None moved or
  deleted. `TestCatalogAllocatorAndProviderBoundaries` and
  `TestCatalogProviderRegistersBeforeInitialization` cover the moved methods.
- **Verification.** `quickcheck` passed after each commit. Targeted
  `make check` runs (catalog, operator index, revision frontier and
  exhaustion, enum, Dump/Restore) passed, and so did a `RACE=1` run of the
  catalog, publication, capture and Restore selections. The full
  `racecheck` was skipped at the user's request. `make bench`: about 1.52 ms/op
  and 7,769 allocations, the same as `master` at `081ea0328` (the <1 ms target
  is already missed there). `make deps` lists only module paths. Imports are
  unchanged.

5b results:

- **Read API** (`catalog_read.go`). Accessors live on `*catalogState` and
  `CatalogRead` delegates: `Kind(a)` is the comma-ok assertion and
  `MustKind(a)` the single-value one, which panics with a
  `*runtime.TypeAssertionError` like the assertion it replaced, for all 14
  descriptor kinds; `Get`/`Find`, `NameAddress`/`LookupName`,
  `Descriptors()`/`Names()` iterators and the counts. None guards a nil
  state. The older nil-guarded `Descriptor`, `Lookup`, `LookupType` and
  `LookupArray` stay.
- **Reads migrated.** Measured on `5b986e558` in production: 912 typed reads
  (592 comma-ok, 320 single-value), 430 untyped, 206 ranges, 14 `len`, 135
  name reads. A type-checked rewrite tool (scratchpad `cat5b`, modes
  `count`, `reads`, `writes`, `owner`, `ownerfix`) applied 4,102 edits in
  production and tests, re-run until no nested site was left.
- **Writes confined** (`catalog_state_write.go`): `setDescriptor`,
  `removeDescriptor`, `bindName`, `unbindName`, `cloneDescriptors`,
  `resetMaps`; 560 sites rewritten (about 110 in production) plus 10
  multi-assignments in tests split by hand. Production code selects the maps
  only in those two files and in composite literals that construct a new
  state (`newEngine`, `cloneCatalog`, `stageRead`,
  `prepareRemintedStorage`). The write callers are 32 production files:
  seeding, clones, stage and replacement candidates, remaps and Restore;
  that list is the input to 5e's `Candidate`. `rebuildOperatorNames` takes
  an iterator.
- **Cut 1.** `CatalogRead.owner` is `*catalogOwner`, a record the store
  embeds by value (so a zero `Engine` stays safe) and returns from
  `catalogStore.owner()`. It holds `domain`, `principals`
  (`&Engine.authorizationDomain`), `aclAuthority` (`&Engine.aclAuthority`),
  `providers` and `extensions` (moved from `Engine`). Its address is the
  engine's catalog identity. `catalogStore.read(state)` builds views;
  `CatalogRead.Domain()` is nil-safe. `validateObjectRefLocked` and
  `validateCatalogViewLocked` are store methods.
- **Explicit parameters instead of the locator:** `Engine.Comment(view,
  ref)`; `ReplaceCatalog(ctx, source, replacement)` checks that `source` owns
  the view; `Engine.captureIndexSnapshot`; relation, path and type-name
  lookups use `AuthorizationView.engine`; scope ACL and regrole output use
  `StatementScope.commentEngine()`; `parameterProvider` carries the setting
  definitions; `StatementDescription.catalogOwner` is `*catalogOwner`.
- **Deviation from the plan.** The plan said the authorization domain comes
  from `AuthorizationView`. That holds wherever a view exists. Descriptor
  validation (provider `Validate`, stored-SQL rendering, temporary
  provenance, extension members) has only a `CatalogRead`, so the record
  carries `principals` and `aclAuthority` as identities, not services. The
  two role-root reads in stored routine validation only proved that the
  owner's principal is in the engine's domain; `validateStoredRoutineDescriptor`
  checks that against the record directly.
- **Tests.** Adapted mechanically (`ReplaceCatalog` source argument,
  `Engine.Comment`, `captureIndexSnapshot` receiver, fixture helpers
  `replaceWithFreshCatalog`, `hashPartitionUnitEngine`,
  `regexpSetUnitEngineTypes`). New seam tests `TestCatalogReadTypedAccessors`
  and `TestCatalogStateWritesStayOnTheirState`. None moved or deleted. Test
  fixtures keep about 130 whole-map uses (state literals and `maps.Equal`
  immutability checks).
- **Verification.** `quickcheck` passed after each commit; targeted catalog,
  comment, replacement, provider, snapshot and signature selections passed,
  and a `RACE=1` run of catalog, publication, capture, Restore and
  replacement selections passed. Full `racecheck` skipped at the user's
  request. `make bench`: about 1.52 ms/op and 7,769 allocations, unchanged
  from `5b986e558` (the <1 ms target is already missed there). `make deps`
  lists only module paths. Package imports are unchanged.

5c results:

- **Extension implementations** (`extension_install.go`,
  `extension_implementation.go`). One `extensionImplementation` interface
  (`Control() extensionControl`, `ValidateExtension(CatalogRead,
  *extensionDescriptor)`) replaces `extensionProvider`, `registeredExtension`
  and the six provider pointers on `procedureDescriptor` (`provider`,
  `queryCodec`, `fuzzy`, `crypto`, `intagg`, `uuidOssp`) plus the
  `*unaccentProvider` on the text search template and dictionary.
  - Each descriptor has one `implementation` field. The owner record holds
    `extensions map[string]extensionImplementation`, the same instance the
    members point at.
  - Providers own their control metadata (moved from `engine.go`);
    `Control()` returns a detached copy, which replaces the copy that
    registration used to make.
  - Staging needs `*Engine` and `*CatalogStage`, so it stays out of the
    interface: `extensionInstaller` embeds it and adds `stage`, and
    `registerExtension` takes an installer.
  - Typed accessors (`d.pgcryptoProvider()`, ..., comma-ok, nil otherwise)
    keep all 83 production reads exact. Exclusivity checks between two
    provider fields, which one field cannot violate, were removed;
    `sameRoutineDeclaration` compares the single field. Comparisons that
    looked at only some providers (`scalar_function_sql.go`) keep their
    typed form. `aggregate`, `conversion` and `foreign` are unchanged.
- **Views** (`view_payload.go`). `relationDescriptor.view` is a
  `viewPayload`: `RuleOID`, `DependsOn`, `TemporaryConsumer`,
  `CapturedReference` and `Validate`. `viewOf(d)` is the checked accessor
  (111 production uses, all feature code); `viewPayloadOf` keeps a nil
  definition a nil payload, so `d.view != nil` and `a.view != b.view` keep
  their meaning. Catalog-level code uses the methods: the relation
  validator (`validateViewDefinition` delegates to `Validate`),
  `viewDepends`, temporary provenance, recipe grafting and the pg_rewrite
  identity readers (`definition_identity`, `object_identity`,
  `object_address_lookup`, `catalog_sources`, `pg_depend`).
- **Policy presentation** (`policy_payload.go`). `policyDefinition.using`
  and `.check` are `policyPayload` (`DependsOn`, `Validate`). The payload is
  the whole `policyExpression`, not only its `presentation`: its validation
  walks engine expression nodes, so a catalog validator can reach it only
  through the interface. Accessor `policyExpressionOf` (19 production
  uses).
- **Rules, cuts 8 and 9** (`rule_payload.go`). `relationDescriptor.rules`
  is a `rulesPayload` with only `Validate`; `ruleList []ruleDefinition` is
  the engine's implementation and `rulesOf(d)` the accessor (53 production
  uses). The payload is the whole list, so slice value semantics and
  copy-on-write sharing are unchanged. Deviation from the plan: no
  `DependsOn`, because every rule dependency check is per rule and labels
  the rule by name, which is engine code (`ruleDepends`).
- **Expression payloads** needed no change: check, default, generation
  (`expression`, `planned`), index keys and predicates (`definition`,
  `execution`), domain checks, policy `expression` and rule `condition` were
  already `expr.Expr` (steps 2a–2d), and no engine-only method is called on
  them.
- **Tooling.** A `go/types` rewrite tool (scratchpad `payrw`, modes
  `count`, `reads`, `writes`) rewrote reads to the accessors, kept nil and
  field-to-field comparisons and copies, and wrapped non-literal writes in
  the `...PayloadOf` helpers; 7 value-typed receivers needed `&` by hand.
- **Tests.** None moved or deleted. New: `TestCatalogPayloadAccessors`, and
  implementation-identity assertions in `TestExtensionProviderIdentity`.
  Rewritten: the registry half of `TestAvailableExtensionsMetadataValues`
  (registration now keeps the implementation; detachment is pinned on
  `Control` and `extensionControl.detached`). Adapted mechanically: provider
  lookalike literals and two-provider mutations (now a single replacement,
  still rejected) in 10 provider/expression test files, and the view, rule
  and policy fixtures.
- **Verification.** `quickcheck` passed after each commit (3 and 4 together;
  the branch needed no rebase). Targeted extension, view, rule, policy,
  catalog, remap, Dump/Restore and provenance selections passed.
  `REFERENCE=1` extension, rule, RLS/policy, view, materialized, temporary
  and Restore selections passed except four tests whose PostgreSQL oracle
  subtests fail identically on `master` (18.6 drift, fixed on
  `fix/pg186-drift`): `TestReferencePGNamespaceViewStoragePrecedence`,
  `TestReferenceRLSBaseFreshAuthority`, `TestReferenceRLSBaseFreshness`,
  `TestReferenceTemporaryTableDiagnostics`. `racecheck` skipped at the
  user's request. `make bench`: 7,768 allocations (7,769 before) and about
  2.07 MB/op (2.12 MB before); ns/op was noise-dominated by machine load in
  both master and branch runs (1.5–2.2 ms), with the quiet baseline at about
  1.52 ms. `make deps` lists only module paths; imports are unchanged.

Notes for later steps:

- 5e: the payload methods take `CatalogRead` and `*relationDescriptor`,
  which become `catalog.Read` and the exported relation type when the
  interfaces move; their method names are already exported so the engine's
  `viewDefinition`, `policyExpression`, `ruleList` and providers can keep
  implementing them. `extensionInstaller.stage` stays in the engine.
- 5e: `extensionControl` is catalog data and moves with the interface.
- 5e: composite literals that build a `catalogState` and the test whole-map
  uses are the remaining direct map selectors.
- 5a was a broad rewrite in practice (670 files). Do not run it, or a
  similar store grouping such as 6a, alongside another broad rewrite.
- The revision-frontier loop ("advance past `descriptorRevisionFrontier`,
  saturating at the maximum") is duplicated in `transaction_publication.go`,
  `sequence_temp.go` and `catalog.go` (once in `publishCatalog`, twice in
  `replaceCatalog`). One store helper could own it.
- `foreignNamespaces` is replaced in the same Restore publication as
  `state` (`catalog.go`). Whoever moves the FDW state must keep that
  publication point.
