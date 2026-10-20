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
- Export the fields with a tool. `relationDescriptor` alone had about 7,800
  field uses in 357 files at `a2252c0b1`. Done with a `go/types` tool rather
  than `gopls rename` per field (see 5d results).
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

### 5g — Optional: regroup `catalog.Relation`

Group its fields into sub-records (column metadata, constraints, security,
kind payloads) as a separate readability change. Done with user approval;
see 5g results (kind payloads stayed top-level).

### 5h — Optional: close the remaining catalog write paths

Added when the later waves were planned (measured at `75fe0d710`). Two small
readability changes that need no other step:

1. **One revision-frontier helper.** `catalogStore.advanceRevisionFrontier`
   now calculates advance-past-revision/sequence-plan-stamp with saturation,
   without mutating the allocator. All six publication/move sites and three
   installer fixtures use it. Publication commits its local scalar only at the
   existing successful install; ALTER SET SCHEMA advances descriptor-local
   clocks and retains its distinct terminal error.
2. **`Candidate` overlay behind methods.** All seven backing maps are private.
   Overlay-only lookup/presence/count/iterator methods share immutable payloads
   without copies or base scans. Private completion, explicit-key staged rename,
   original-ref retirement and separate default observation/delta methods own
   writes. Engine DDL/lifecycle/rowtype/storage validation stays engine-owned.
   Original observations include nil absence; present nil output is a tombstone.
   The typed prechange census covered 393 uses across 110 files, including all
   109 external engine files; production and fixture callers migrate together.

Implementation and root-owned proof are recorded in the 5h results below.
The latest user instruction makes quickcheck the active gate and explicitly
waives further racecheck and race-containing finalcheck; waived checks are not
passing checks. The existing startup target miss remains reported separately.

The relation, type, extension, operator and parameter validators that 5e
left in the engine stay until a stored-SQL rendering seam exists (step 12
and the postponed `view` package); see the notes for later steps.

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
startup regression. 5f completes the step; optional 5g is done too.

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
- [x] 5d — Descriptor names decided, fields exported (`a7c3461ce`,
  `b69a715b2`, `31ca756a9`, `4a6055b85`, then the commit that records this
  status) *(racecheck skipped at user request)*
- [x] 5e — `catalog` created, types moved, `Candidate` split from
  `CatalogStage` (`eb68d288b`, `fa79f1917`, `0f3bad42b`, `eb612a052`,
  `3b5d2b883`, `943e7a42d`, then the commit that records this status)
  *(racecheck skipped at user request; targeted RACE=1 runs passed)*
- [x] 5f — Aliases removed (`2772419e7`, `c4b8068fc`, `0b9103f6d`,
  `69fb93a49`, `353528e2d`, `78ec4daeb`, then the commit that records this
  status) *(racecheck skipped at user request)*
- [x] 5g — `catalog.Relation` regrouped into value sub-records
  (`43c6b86ec`, `47a4495b2`, `089bf7856`, `3ef61771b`, `90f6a9b52`, then
  the commit that records this status) *(racecheck skipped at user
  request; targeted RACE=1 runs passed)*
- [x] 5h — One revision-frontier helper; private `Candidate` overlay methods
  (the commit that records this status) *(quickcheck passed; further racecheck
  and finalcheck skipped at user request)*

Exported names chosen (5d; applied by 5e's move, with engine aliases):

| Engine name | Catalog name |
|---|---|
| `namespaceDescriptor`, `relationDescriptor`, `databaseDescriptor`, `extensionDescriptor`, `procedureDescriptor`, `operatorDescriptor`, `parameterDescriptor`, `tablespaceDescriptor`, `typeDescriptor`, `collationDescriptor`, `conversionDescriptor` | the kind without the suffix: `catalog.Namespace`, `Relation`, `Database`, `Extension`, `Procedure`, `Operator`, `Parameter`, `Tablespace`, `Type`, `Collation`, `Conversion` |
| `tsTemplateDescriptor`, `tsDictionaryDescriptor` | `catalog.TSTemplate`, `catalog.TSDictionary` |
| `foreignDescriptor` (wrappers, servers, user mappings) | `catalog.ForeignObject` |
| `ColumnDescriptor` | `catalog.Column` (the engine alias keeps the public name) |
| `catalogState`, `CatalogRead`, `catalogOwner` | `catalog.State`, `catalog.Read`, `catalog.Owner` |
| records, enums and payload interfaces | CamelCase of the engine name: `checkConstraint` → `CheckConstraint`, `relationKind` → `RelationKind`, `viewPayload` → `ViewPayload`, `extensionImplementation` → `ExtensionImplementation`, `defaultACLRecord` → `DefaultACLRecord` |

Field names: the engine name capitalized, with Go initialisms on the
leading word (`acl` → `ACL`, `columnACL` → `ColumnACL`, `oid` → `OID`,
`sqlIndex` → `SQLIndex`, `rlsEnabled` → `RLSEnabled`, `id` → `ID`,
`ctype` → `CType`), and `typ` → `SQLType` (so `catalog.Type` has no `Type`
field). The descriptor identity fields that collide with the `Descriptor`
methods are `ObjectAddress`, `ObjectNamespace`, `ObjectName` and
`ObjectRevision` (user decision).

Outstanding aliases: none. 5e kept 97 type and 45 constant aliases in
`internal/engine/catalog_aliases.go`; 5f removed them all, including the
exported ones (user decision). The 5e note that `Descriptor`, `CatalogRead`,
`CatalogProvider` and `CatalogConflict` were the root package's facade was
wrong: only `ColumnDescriptor` and the FDW types and kinds were used outside
the engine. The root package (`fdw.go`, `rows.go`) and `cmd/stuntgres` now
name `catalog.Column` and `catalog.Foreign*` directly, as they already name
`ids`; the public `stuntgres.Foreign*` aliases are unchanged.
`functions.RoutineProvider` is an alias of `expr.RoutineProvider` (5e) and
keeps its constants; it is not a catalog alias.

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

5d results:

- **Scope.** Measured on `423c4a9da`: the catalog closure (the 14
  descriptor kinds, `catalogState` and `CatalogRead`, followed through
  fields and interface method signatures, stopping at `Engine`,
  `CatalogStage`, `Session`, `StatementScope`, `EvalContext`, `binding`,
  `viewDefinition` and `ruleDefinition`) is 71 structs, 8 interfaces and 11
  other named types. 387 unexported fields on 57 structs were exported:
  43 on `relationDescriptor`, 87 on the other descriptors, 211 on records
  and 46 identity fields. `catalogState` and `CatalogRead` keep their
  fields unexported: they are catalog-private ("Done when"), and
  `descriptors`/`names` collide with the 5b iterators. `ColumnDescriptor`,
  `uniqueConstraint`, `exclusionConstraint`, `foreignKeyConstraint` and the
  public FDW structs were already exported. No closure struct has an
  embedded field.
- **Identity fields.** Reads go through the `Descriptor` method wherever
  the method only returns the field (6,007 sites, including map-literal
  keys). Writes, increments, address-taking uses and the getter bodies keep
  the field. `typeDescriptor.namespace`/`revision` and
  `collationDescriptor.namespace`/`revision` keep raw reads under the new
  name because their methods default or derive the value.
- **Tool.** Deviation from the plan: a `go/types` rewrite tool (scratchpad
  `fieldx`, modes `closure`, `plan`, `apply -group`) instead of
  `gopls rename` per field. `gopls references` alone took about 8 s per
  field, and gopls cannot turn identity reads into method calls. The tool
  renames every definition, selector and composite-literal key of a
  field, field doc comments that open with the old name and qualified
  `Type.field` mentions in comments and design notes, checks new names
  against the type's fields and methods, then re-type-checks and compares
  the use count of every object before and after. Any reference that
  changed its target would abort the run. 32,130 edits in 1,030 files
  (557 test files); the diff is a pure rename (17,969 lines each way).
- **Tests.** Adapted mechanically by the same tool; none moved, deleted or
  rewritten by hand.
- **Verification.** `quickcheck` passed after each commit. Targeted
  catalog, typed-read, payload, extension, sequence, rename and revision
  selections passed. `racecheck` skipped at the user's request; no
  `REFERENCE=1` selection is required for a pure rename. `make bench`:
  7,768 allocations and about 2.07 MB/op on both `master` and the branch.
  ns/op was noisy because other sessions loaded the machine (load average
  12–25). In interleaved runs the best was 1.510 ms on `master` and
  1.515 ms on the branch, with medians of about 1.53 and 1.60 ms. That is
  over the <1 ms target, which `master` already misses. `make deps` lists
  only module paths. Imports are unchanged.

5e results:

- **Moved** (`internal/engine/catalog`, 55 files, about 3,100 lines): 395
  declarations in 362 source spans, chosen by a type-checked move set
  whose frontier (references back into the engine) had to be empty. The 90
  closure types with their methods and constants, `State`, `Read`, `Owner`,
  the `Provider` registry, `Conflict`, the read API (`read.go`), the state
  access and write API (`state_access.go`, `state_write.go`), `CloneState`,
  `DescriptorNameKey`, the operator name index with `OperatorIndexEdit` and
  `RebuildOperatorNames`, default ACL records and helpers, the conversion,
  collation and tablespace inventories, and `Candidate`. Names follow the 5d
  table, plus `Provider`, `Conflict`, `Candidate`, `Removal`, `NewState`,
  `NewRead`, `NewCandidate` and `CloneState`; other declarations used from
  the engine got CamelCase names, the rest stay unexported.
- **Layering.** `catalog` imports only `ids`, `expr`, `authorization`,
  `sqltype`, `parser` and the standard library. The one sibling dependency,
  `builtinRoutineDeclaration.Providers` of type `functions.RoutineProvider`,
  was resolved by moving the type to `expr` (`functions` keeps an alias and
  its constants).
- **State seam** (`fa79f1917`). The engine reaches the non-map fields
  through getters and setters (`Epoch`/`SetEpoch`, `OperatorNames`,
  `DefaultACL`/`DefaultACLs`/`OverlayDefaultACLs`, `DumpPriority`/
  `LookupDumpPriority`/`HasDumpPriorities`, `ScalarImports`,
  `ACLItemImport`, ...) and `Read.Owner`/`State`/`WithState`; literals use
  `NewState`/`NewRead`. A type-checked rewrite changed about 1,700 sites in
  484 files and reran to a fixpoint for nested selectors; about 50 sites
  were edited by hand. `ResetMaps` now iterates the previous descriptors
  instead of returning the map. The engine has no direct access to the
  State maps.
- **Methods** (`0f3bad42b`). 29 unexported methods the engine calls were
  exported (the write API, `HasAttribute`, `IndexForAttribute`, `Seeded`,
  `IsExclusion`, `Stored`, ...). Ten methods that need engine-only types
  became engine functions: the typed extension-provider accessors
  (`pgcryptoProviderOf(d)`, ..., `unaccentTemplateProviderOf`,
  `unaccentDictionaryProviderOf`), `procedureReturnsSet` and
  `operatorSeeded` (the 805-line operator inventory stays with seeding).
  The namespace validator checks `Temporary.Private() != nil` instead of
  `privateSessionOf` (only `Session` mints private tokens, never nil), and
  `CollationSemantics` builds its errors with `expr.ErrorAt`.
- **Candidate, cut 2** (`eb612a052`). `catalog.Candidate` holds `Base`,
  `Descriptors`, `Names`, `Touched`, `Removals`, `OldNames`, `DefaultACLs`
  and `DefaultReads`, with `LookupName`, `Create`, `Replace`,
  `RecordRemoval` and `State` (the merged unpublished state `stageRead`
  built). `CatalogStage` embeds it and keeps the lifecycle checks, row-type
  synchronization and publication coordination. Decision (user): the
  overlay fields are exported, because about 250 stage sites still write
  them directly; tightening to methods is later work.
- **Deviation: validators.** Only the pure core kinds moved (namespace,
  database, tablespace, collation, conversion, foreign objects). Relation,
  type, extension/procedure, operator and parameter validation stay in the
  engine (user decision): they render stored SQL through `scalarRenderer`,
  walk engine expression nodes, read the operator inventory, settings
  definitions or extension implementations. They register through the
  moved `Provider` registry.
- **Shadowing** (`3b5d2b883`). 420 engine locals are named `catalog`. The
  30 that would shadow a moved `catalog.X` reference became `view` or
  `state` (192 edits in 23 files); the move tool checked every inserted
  reference against its scope.
- **Tooling.** Scratchpad `movex` (go/types): `setfront` (move-set
  frontier), `kinds`/`acc` (field-use classification and accessor rewrite),
  `methx` (method rename or conversion to functions), `fieldrn`,
  `unshadow`, `move` (extracts the declarations with doc comments, renames
  inside moved text and type names in its comments, writes the aliases and
  qualified references). Floating file-header comments are not attached to
  a declaration; five were restored by hand.
- **Tests.** Moved with the code: `catalog_read_test.go` and
  `catalog_state_write_test.go` (now `catalog/read_test.go` and
  `catalog/state_write_test.go`: `TestCatalogReadTypedAccessors`,
  `TestCatalogReadUntypedAndNameAccessors`,
  `TestCatalogReadAccessorsRequirePopulatedRead`,
  `TestCatalogStateWritesStayOnTheirState`). Engine tests adapted
  mechanically: map snapshots use `maps.Collect` over the iterators,
  fixtures use the new `catalogStateFixture` (`catalog_state_fixture_test.go`)
  or `CloneDescriptors`/`CloneNames`/`ResetDescriptors`. None deleted.
- **Verification.** `quickcheck` passed after C2, after C3+C4 and after the
  move. Targeted catalog, provider, collation, conversion, extension and
  operator selections passed, and a `RACE=1` run of catalog, operator index,
  dump priority, default privilege, Restore, replacement and temporary
  sequence selections passed. `REFERENCE=1` selections (table, type, domain, schema, comment and
  default DDL; views, rules and RLS; `reg*`, extensions, collations,
  conversions and tablespaces; Restore, Dump and routine tests) passed
  except four tests that fail identically on `master`:
  `TestReferenceRLSBaseFreshAuthority`, `TestReferenceRLSBaseFreshness` and
  `TestReferenceTypedTableAuthorization` (PostgreSQL 18.6 oracle subtests)
  and `TestReferenceSchemaAuthorization/memory#05` (the expected database
  name differs from the reference-mode DSN). `racecheck` skipped at the user's
  request. `make bench` (interleaved): 7,768 allocations and about 2.07
  MB/op on both; best 1.503 ms on `master` and 1.512 ms on the branch,
  medians about 1.55 ms on both, over the <1 ms target that `master`
  already misses. `make deps` lists only module paths.

5f results:

- **Scope.** Measured on `b99d84bdf` with go/types: 141 aliases with uses
  (`ForeignWrapper`, `ForeignServer` and `ForeignUserMapping` had none in the
  engine) and about 9,800 alias references in 906 engine files, the largest
  `relationDescriptor` (1,956), `routineIdentity` (1,264), `functionRoutine`
  (1,114), `CatalogRead` (684; the other grep hits are the
  `Engine.CatalogRead()` method) and `ColumnDescriptor` (401). No
  `engine_test` package uses an alias: its `engine.CatalogRead()` hits are
  method calls on a local named `engine`.
- **Shadowing** (`2772419e7`). 160 locals named `catalog` would have
  captured an inserted `catalog.X`. They became `view` or `catalogView`
  (`catalog.Read`), `state` or `snapshot` (`*catalog.State`), `candidate`,
  `changes` (`*catalogChanges`), `catalogAddress` and `catalogKindRef` in
  three tests, by type with a free-name check: 948 edits in 126 files.
- **Rewrite**, one commit per area: relation types, kinds, constraints,
  column, index, partition, inheritance, physical NOT NULL, policy and view
  and rule payloads (`c4b8068fc`, 4,647 edits in 636 files); routines,
  operators and signatures (`0b9103f6d`, 3,201 in 120); the other
  descriptor kinds and their providers (`69fb93a49`, 822 in 218); the core
  types `State`, `Read`, `Owner`, `Candidate`, `Conflict`, `Provider`,
  `Descriptor` (`353528e2d`, 1,315 in 462); `Column` and the FDW types with
  the root package and `cmd/stuntgres`, and deletion of
  `catalog_aliases.go` (`78ec4daeb`). Imports were added to 706 files.
- **Embedded fields** follow their type name: `catalogStore.Owner`,
  `CatalogStage.Candidate`, `createUniqueConstraint.UniqueConstraint`, the
  NOT NULL binding's `NotNullConstraint`, and `Provider` in four test
  fixtures that embed a provider.
- **Tool.** Scratchpad `aliasx` (go/types; modes `count`, `shadow`,
  `apply`, `locals`). Each write reloads the package with its tests,
  requires zero type errors and compares every object's use count before and
  after, mapped through the edits. Only the alias objects may lose uses and
  only their `catalog` targets may gain them. Comments that named the old
  types were already rewritten by 5e's move; two were left and fixed by hand.
- **Tests.** Adapted mechanically by the same tool; none moved, deleted or
  rewritten by hand.
- **Docs.** The catalog and authorization READMEs, `TABLES.md`, the
  authorization prompt, and the design notes for transaction visibility,
  authorization, type system, query execution, index maintenance and the
  operator oracle now name `catalog.Read`, `catalog.Relation`,
  `catalog.Procedure` and `catalog.Column` where they meant current code.
  Pseudocode for target contracts and records of earlier steps keep the old
  names. `AGENTS.md` is unchanged (100 lines).
- **Verification.** `make build` and targeted `make check` selections after
  each commit; `quickcheck` passed on the full rewrite. `racecheck` skipped
  at the user's request. No `REFERENCE=1` selection is required for a pure
  rename. `make bench` (interleaved): 7,768 allocations and about 2.07 MB/op
  on both; best 1.504 ms on `master` and 1.476 ms on the branch, over the
  <1 ms target that `master` already misses. `make deps` lists only module
  paths. `catalog` still imports only `ids`, `expr`, `authorization`,
  `sqltype`, `parser` and the standard library.

5g results:

- **Decisions (user).** Named sub-records reached as `d.Constraints.Check`,
  not embedded ones with promoted fields. Kind payloads stay top-level:
  each is already its own record or pointer, so another wrapper would add
  churn without clarity.
- **Layout** (`catalog.go`). Top level: identity, `RelationKind`, `Owner`,
  `Comment`, `AutoExtensions`, the column shape (`Columns`, `Index`,
  `Attributes`, `RowType`, `OfType`), the four sub-records, then the kind
  payloads under a comment. Sub-records, with renames where the group name
  makes a suffix redundant:
  - `ColumnMeta ColumnMetadata`: `AttributeHighWater`→`HighWater`,
    `DroppedAttributes`→`Dropped`, `CatalogAttributes`,
    `ColumnComments`→`Comments`, `Defaults`, `Generations`, `Identities`.
  - `Constraints RelationConstraints`: `NotNullConstraints`→`NotNull`,
    `PhysicalNotNull`, `CheckConstraints`→`Check`,
    `UniqueConstraints`→`Unique`, `ExclusionConstraints`→`Exclusion`,
    `ForeignKeys`.
  - `Security RelationSecurity`: `ACL`, `ColumnACL`, `Policies`,
    `RLSEnabled`, `RLSForced`.
  - `History PublicationHistory`: `HasIndexes`, `HasSubclasses`,
    `SubclassPredecessor`, `SubclassPublication`.
- **Value semantics.** Sub-records are values, never pointers: `next := *d`
  copies them and shares their slices and maps exactly as before, so
  copy-on-write sharing and published-state immutability are unchanged.
  No reflection walks `Relation` fields by name.
- **Scope.** Measured on `b9579b397` with go/types: 3,354 uses of the 22
  moved fields (4 in `catalog`, the rest in the engine), against a grep
  estimate several times larger. Constraints 1,767, columns 936, security
  554, history 97. 411 files changed (252 test files).
- **Tool.** Scratchpad `relx` (go/types; modes `count`, `apply GROUP`,
  `verify`). It rewrites each selector's field identifier to `Group.New`
  and folds the moved keys of each `Relation` composite literal into one
  nested `Group: catalog.T{...}` literal, applying any rewrites inside the
  copied values. It checks that `catalog` names the package at each
  literal, renders all files in memory before writing any, and `verify`
  requires zero type errors and an exact per-object use census (keyed by
  package, kind, name and type). The `Relation` declaration was edited by
  hand per group. A first run hit nested edits
  (`AttributeHighWater: parent.AttributeHighWater`); it was reset and
  rerun after the fix.
- **Tests.** Adapted mechanically by the same tool; gofmt expanded some
  one-line closures that grew past its size limit. None moved, deleted or
  rewritten by hand. No new seam test: the change adds no semantics.
- **Verification.** `make build` and targeted `make check` runs (every test
  in the touched test files plus `^TestCatalog`) after each commit;
  `quickcheck` passed; a `RACE=1` run of catalog, Restore, replacement,
  pg_class history, dump priority, default privilege and physical NOT NULL
  selections passed. `racecheck` skipped at the user's request. No
  `REFERENCE=1` selection is required for a pure rename. `make bench`
  (interleaved): 7,768 allocations and about 2.07 MB/op on both; best
  1.507 ms on `master` and 1.508 ms on the branch, over the <1 ms target
  that `master` already misses. `make deps` lists only module paths.
  `catalog` imports are unchanged.

5h results:

- **Ownership.** One pure, saturating store frontier calculation serves all six
  production sites and three installer fixtures. Publication retains its
  speculative local scalar and original successful install point; ALTER moves
  use descriptor-local clocks and preserve their exhaustion diagnostic.
  All seven Candidate maps are private, with overlay-only readers and
  method-owned writes. Original references, name fences, sparse default
  observations/tombstones, immutable payload sharing and derived index
  ownership stay with their existing authorities. Validators remain engine-owned.
- **Coverage.** New independent frontier and Candidate units cover terminal
  arithmetic, private completion, repeated rename/removal/name reuse, original
  default inputs and retained snapshots/index sharing. All 109 external files
  from the typed 393-use census migrated, including deliberate negative fixtures.
  The authentic namespace fixture explicitly selects its renamed
  `candidate_schema` through the canonical immutable settings registry;
  it does not install an arbitrary namespace alias or evaluate a generator.
- **Observed proof.** Production build, focused internal selections, unchanged
  public definitions in quick and pinned PostgreSQL 18.6 modes, selected
  publication/capture/cancellation races, actual direct/prepared four-phase
  schema/default-privilege smoke and module-only dependency checks passed.
  After rebase onto `9c6c4562326304fbe4cacff06b2b98b75fe03911`, fresh full
  quickcheck and all four actual smoke phases passed (142.18s combined).
  SAME-held rows/metadata, retained cursors, canonical Dump/Reset/Restore and
  future-only ACL effects remained intact. Earlier API/fixture failures were
  corrected; the post-rebase fixture uses `e.settings.Definitions()`.
- **Execution disposition.** Earlier full-race attempts failed on scenario
  deadlines/cancellation phase expectations, not reported data races; they are
  not full-race passes. The user's latest instruction, “skip racecheck and rely
  only on quickcheck,” waives further race execution and the race-containing
  finalcheck. Post-integration quickcheck replaces finalcheck; independent
  review and integration remain separate lifecycle gates.
- **Startup.** The original five-run baseline was 1.013–1.245ms; isolated
  postchange samples were 1.680–1.800ms versus contemporaneous unchanged
  integration samples of 1.569–1.714ms. All series miss the <1ms target.
  Warm allocations remain 5152 and bytes about 1.65–1.66MB/op. No warm allocation
  regression was demonstrated; timing equivalence, optimization or target
  attainment is not claimed. Startup optimization remains outside this part.

Notes for later steps:

- Later broad rewrites: the `fieldx`/`aliasx` approach (rename table, then
  a before/after per-object use census) is fast (about 20 s per run on the
  engine package with tests) and catches shadowing; prefer it to
  per-identifier gopls runs.
- About 230 engine locals are still named `catalog` (5f renamed only the
  160 that would capture a `catalog.X` reference). New code that names the
  package inside their scope must rename the local first.
- Engine callers now name `functions.RoutineProvider` and its family constants
  directly. The intentional `functions.RoutineProvider = expr.RoutineProvider`
  seam preserves catalog's expr-owned representation without a functions
  import; see the functions README Binder section. Item6's latest-base semantic
  and public smoke proofs passed in tests-12. The preceding-base selected
  native/real, build, quickcheck, full race and dependency/import passes remain
  recorded in tests-10/tests-11; complete-candidate gates refresh before review.
  Startup qualifications are recorded there. Post-integration finalcheck alone
  was waived by the user.
- Later: the relation, type, extension, operator and parameter validators
  can move once the catalog has a narrow seam for stored-SQL rendering,
  expression-node walks and the operator and settings inventories.
  Candidate overlay encapsulation is independent and implemented in 5h.
- 5a was a broad rewrite in practice (670 files). Do not run it, or a
  similar store grouping such as 6a, alongside another broad rewrite.
- Revision-frontier policy is now owned by the pure store helper in 5h;
  successful publication still owns committing the speculative scalar.
- `foreignNamespaces` is replaced in the same Restore publication as
  `state` (`catalog.go`). Whoever moves the FDW state must keep that
  publication point.
