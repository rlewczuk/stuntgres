# Engine split: catalog publication and the stores, sixth round

Session prompt for linear items 176–179 of the
[status page](ENGINE-SPLIT-STATUS.md). The rules are in
[ENGINE-SPLIT.md](ENGINE-SPLIT.md) §2, §3 and §5. The package contracts are
in the READMEs of [catalog](../internal/engine/catalog/README.md),
[access](../internal/engine/access/README.md),
[index](../internal/engine/index/README.md),
[storage](../internal/engine/storage/README.md) and
[session](../internal/engine/session/README.md).

The root's catalog orchestration has three parts:
- **Publication:** `publishCatalog`, `replaceCatalog`, `ValidateCatalogStage`
  and the `CatalogStage` API.
- **The catalog store:** `catalogStore` in `catalog_store.go`, which is
  `Engine.catalog`, held by value.
- **The role store:** `roleStore` in `role_store.go`, which is
  `Engine.roles`, shared per cluster through `databaseCluster`.

The publication skeletons stay in the root (§3 rules 3 and 4). Their pure
sub-steps and the two stores can move.

The stores were rejected before because of thousands of field sites. Most
of those are tests: about 3,950 catalog and 1,140 role test sites, which a
type-checked rename rewrites for free (§2.3). Production sites are about 590
for the catalog store and 137 for the role store.

Sizes were measured at `1f5b91b57` and `ea8c445fe`, and these files did not
change between the two. Re-run `reach` on each move set first.

## Item 176 — catalog publication sub-steps

Needs —. About 0.3k lines move to three packages. Every sub-step is
value code inside a root function that otherwise stays.

**Moves to `index`** (`catalog.go` `publishCatalog`, `catalog_message.go`):
- the staged index-store capture (`publishCatalog`, about L552–567), as
  `index.CaptureStagedIndexStores(relations, current, staged)`;
- the build stores, the rebuild set and the source roots (about
  L605–654), as `index.PublicationBuildStores`;
- the candidate-match fence. It appears twice: `publishCatalog` about
  L531–551, and `captureCatalogStageLocked` about L134–157. It becomes one
  `index.CandidatesMatch(candidates, indexOf, storeOf func…)`.
- *Optional:* the overlay capture selection of
  `prepareCatalogOverlayIndexes` (`catalog_overlay_indexes.go` about L37–96),
  with `privateStorage` and `committedStorage` callbacks. Skip it if the
  callbacks need more than two functions.

**Moves to `storage`:**
- `catalogRowShape` and its three checks (`catalog.go` L75 and the uses near
  L383, L519 and L753), as `storage.RowShape` with `Matches` and
  `MatchesWithEnds`. The L383 check skips `Ends`; keep that difference.
- the relations-map construction in `publishCatalog` (about L580–601), as
  `storage.CandidateRelations`;
- the birth stamping in `publishCatalog` (about L863–907). It is written
  twice; it becomes one `(*storage.Relation).StampPublishedBirth(frontier, fullXID)`.
  The `visibility` calls stay in the root.
- the body of `replacementCapacityLocked`, over value inputs.

**Moves to `session`:**
- `catalogAuthority` and the body of `catalogAuthorityPreflightLocked`
  (`catalog_transaction_storage.go`). The committed state and the roles
  revision come in as values.
- the storage-shape recording in `stageCatalogChangesLocked`
  (`catalog_overlay_stage.go` about L94–131).

**Moves to `catalog`:** the namespace-changed scan as
`Candidate.NamespaceChanged()`, and the revision frontier, used at four
sites, as `catalog.RevisionFrontier`.

**Stays root.**
- the `publishCatalog` lock and retry loop, `authorizationLocked`, the
  session-mutex revalidation, XID assignment and the install block;
- `replaceCatalog` and `stageCatalogOverlay`;
- `catalogFencesCurrentLocked` and the fence order of
  `catalogPreflightLocked`;
- `catalogDependencyPreflight` and the post-apply validators.

**Callers and locks.** Everything runs under `Engine.mu` and takes no
lock. Every fence still returns the same `Conflict{}` in the same order.

**Bench.** Every DDL runs through publication: run `make bench` and the
`reference/tables` timing before and after. Allow no new allocation per
publication.

**Tests.**
- Root units:
  - `driver_batch_catalog_test.go`, `partition_attach_publication_test.go`
    and the `CatalogFences` selector of the root README;
  - the index publication units that call the moved code (rewrite them to
    the package names).
- New package units pin `RowShape.Matches` against `MatchesWithEnds`, and
  `CandidatesMatch`.
- Quick and `REFERENCE=1` selection:
  - `TestReferenceAlterAddColumn(Transactions|Cancellation)`;
  - `TestReferenceMaterializedRefreshConcurrentLifecycle`;
  - `TestReferencePartialIndexConcurrent`;
  - `TestReferenceTruncateBoundaries`;
  - `TestReferenceForeignActionsConcurrentDDL`.

  Use `RACE=1` on the concurrent selections.

**Docs.** Update the index, storage and session READMEs. The root README's
publication paragraph keeps only the skeleton.

## Item 177 — `catalog.Store`

Needs 168–176. The rename touches about 190 production files, so it waits
until no other code item is in flight. About 0.29k lines move.

**Commit 1: scripted in-place rename** (no move). Use a type-checked tool:
`fieldrn` from `.cswd/tmp/PG18-ENGINE-SPLIT-PLAN-ALIASES/` and `movex`
from `.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-EXPRESSION-CORE/` (the newest
build), or a small `go/types` program. Do not use regex. The script must
be re-runnable after a rebase, so regenerate the commit instead of
resolving conflicts.
- `catalogStore.state` becomes an exported field `State`. A
  `PublishLocked(next)` method covers the 15 writes in 11 files, so
  publication points stay greppable.
- The `nextOID` and `nextRevision` frontiers are read and written back at
  publication in five files: `catalog.go`, `transaction_publication.go`,
  `temporary_session.go`, `generated_replacement.go` and
  `database_cluster.go`. Give each frontier a reader and an
  `Install…FrontierLocked` writer, and use them at all 17 sites.
  `nextEnumMember` stays private behind `AllocateEnumMemberLocked`.
- `owner()` becomes `&s.Owner`. The store embeds `catalog.Owner`, so a
  method named `Owner` would clash. Use `gofmt -r`.
- The 44 `e.catalog.read(e.catalog.state)` pairs become `e.catalog.Current()`.
- Export the remaining methods: `RegisterProviderLocked`,
  `AllocateOIDLocked`, `OIDReservationLocked`, `AllocateOIDFromLocked`,
  `AllocateOIDAtLeastLocked`, `RevisionLocked`, `AllocateEnumMemberLocked`,
  `ValidateObjectRefLocked`, `ValidateCatalogViewLocked` and `Read`.

**Commit 2: move.**
- `catalog_store.go` becomes `catalog/store.go`, and `catalogOIDReservation`
  becomes `catalog.OIDReservation`.
- Add `catalog.NewStore`. It removes the duplicated construction in
  `newEngine` and `newDatabaseRuntime`.
- Move the database projection helpers of `database_cluster.go`:
  `retainDatabaseLocked`, `databaseProjectionLocked`,
  `installDatabaseProjectionLocked` and `databaseOIDLocked`. They are keyed
  by `*catalog.Store` instead of `*Engine`, as
  `catalog.RetainDatabasesLocked`, `ProjectDatabasesLocked` and
  `DatabaseOIDLocked(start, target, others []*Store)`.

**Stays root.**
- `Engine.catalog`, now a `catalog.Store` held by value.
- The journal wrappers (`catalogChangeLocked`, `privateCatalogLocked`).
- The `AuthorizationView`-taking wrappers.
- Every acquisition of `Engine.mu`.

Feature packages keep using the existing `AllocateOID` and `Revision` host
methods. They never receive the store, so publication stays in the root.

**Callers and locks.** No lock changes. The store has no mutex
(§3 rule 3).

**Tests.**
- Commit 1 rewrites every root test site with the same script.
- Store units (OID exhaustion, reserved OIDs, enum members, revision
  overflow) move to `catalog`.
- Quick and `REFERENCE=1` selection: `TestReferenceDatabaseCreate`,
  `TestReferenceControlSnapshotSurvivesCatalogReplacement`,
  `TestReferenceDriverBatchFeatureSmoke` and one Dump/Restore smoke
  (`TestReferenceDateDumpRestoreAndPostgresReplay`).

**Docs.** The catalog README gains `Store`. The root README's "the catalog
store" becomes a pointer to it.

## Item 178 — builtin catalog seed

Needs 177. About 0.19k lines move.

**Moves.**
- These become `catalog.Store` methods, or `catalog` functions over a store
  and value inputs:
  - from `builtin_catalog.go`: `installBuiltinProcedures`,
    `installBuiltinOperators`, `seedProcedureNames`, `finishSeed`,
    `validateSeed` and `validateCatalogDescriptors`;
  - `initializeTablespaces` (`tablespace_catalog.go`);
  - the catalog half of `installBuiltinLanguages` (`plpgsql_bootstrap.go`);
  - `registerCatalogSource` (`catalog_sources.go`).
- The namespace, type, collation and conversion seeding loops of `newEngine`.

**Stays root.**
- the order of `newEngine` and `builtinCatalogCapacity`;
- the builtin domain checks, which bind;
- the `pgcatalog` sources and the extension registration.

**Callers and locks.** Construction runs before the engine is shared. Keep
the process-level memoized seed identity, so every fresh engine shares the
same immutable builtin descriptors.

**Bench.** This is the startup path. Run `make bench`, interleaving prebuilt
before and after binaries, and compare against the 1 ms target. Allow no
new allocation per engine.

**Tests.**
- Seed units move to `catalog`.
- Root units that check seeded descriptors through an engine stay.
- Quick and `REFERENCE=1` selection: one catalog smoke per seeded class:
  - `TestReferencePGTypeLifecycle`;
  - `TestReferenceRangeTypeProviders`;
  - `TestReferencePLpgSQLCoreLanguageAuthority`.

**Docs.** The catalog README gains the seed.

## Item 179 — `access.RoleStore`

Needs 177. About 0.19k lines move. 137 production sites in 45 files change.

**Moves.**
- `roleStore` (`role_store.go`) becomes `access.RoleStore`. `authorization`
  cannot hold it, because the store needs `access.RoleRecord` and
  `catalog.Owner`.
- `PublishedRead(owner, view)` replaces the 24 copies of
  `roles.read(catalog.owner(), view, roles.published)`.
- The principal mapper and remap move as `access` functions over the store:
  `replacementPrincipalMapper` and `replacementPrincipalRemap`
  (`principal_remap.go`). `operatorSeeded` comes in as a value.
- `aclItemImporter` (`aclitem_remap.go`) becomes
  `ACLItemImporter(source *RoleStore, …)`.
- The role half of `replacementACLsLocked` (`catalog.go`) becomes
  `ReplacementACLsLocked`.
- The body of `checkDatabaseConnectLocked` (`database_acl.go`) becomes
  `access.CheckDatabaseConnect`.
- The role part of `restoreStage` (`principal_remap.go`).

**Cut.** State ownership in place first (§2.2 rule 1): turn the root
helpers into `roleStore` methods. Then move the type in one commit, with
the same re-runnable rename script as item 177.

**Stays root.**
- `Engine.roles`, a `*access.RoleStore`;
- `databaseCluster`;
- the session-level role code (`sessionRolesLocked`,
  `installOperationRolesLocked`, `preparedActorLocked`);
- `retainedRoutineProcedureRemapper`.

**Callers and locks.** Cluster runtimes share one pointer-stable store.
`catalog.Owner.Principals`, `catalog.Owner.ACLAuthority` and
`Root.Reprovenance` keep pointers to its domain and ACL authority, so the
store must never be copied: add a `noCopy` guard or document it.

**Tests.**
- Rewrite the root test sites with the script.
- Role store units move to `access`.
- Quick and `REFERENCE=1` selection:
  - `TestReferenceRoleRestoreOwnerReset`;
  - `TestReferenceRoleAttributesConnectionAdmission`;
  - `TestReferenceRoleOwnershipAuthority`;
  - `TestReferenceObjectACLDatabase`. It is order-dependent against the
    oracle: rerun it alone if it fails.

**Docs.** The access README gains `RoleStore`, and the root README's role
store sentence becomes a pointer to it.

## Status

- 176: done (`776886564`, `ec303876b`, `7c493cfdf`, `60fc6d9eb`,
  `91ecadea8`, docs commit), started at master `6005760ff`, rebased on
  `94ac1ef52` (item 175). `reach` confirmed `catalogRowShape` and
  `catalogAuthority` clean; the other sub-steps were cut by value inputs.
  - `catalog`: `Candidate.NamespaceChanged` and `RevisionFrontier` (four
    sites). `replacementACLsLocked` now validates every ACL first, then
    computes the frontier in a second pass (Restore only; one closure).
  - `storage`: `RowShape` with `Matches` (the stage-validation check, which
    skips `Ends`) and `MatchesWithEnds` (both publication fences);
    `Publication.CandidateRelations`; `Relation.StampPublishedBirth` plus
    `CountPublishedInserts` for the duplicated counter lines;
    `Store.ReplacementCapacityLocked`. `FullXIDLocked` (a map read) is read
    once per publication instead of per row.
  - `index`: `CandidatesMatch`, `CaptureStagedIndexStores`,
    `PublicationBuildStores` (one result struct) and the optional
    `SelectOverlayCapturesLocked`, which needed no callbacks: it reads the
    published `*storage.Store` through `session.PrivateStorage`.
  - `session`: `CatalogAuthority` with `PreflightLocked` (the root keeps a
    four-line adapter), `Journal.RecordRowShapes` and
    `Journal.RecordMaterializedRefresh`.
  - Deviations: `CandidatesMatch` takes the staged candidate and current
    state instead of an `indexOf` callback (both sites resolved the index the
    same way). At the capture site `storeOf` adds the parent's read fence
    before its match; a mismatch discards the journal, so successful fence
    order is unchanged. The refresh's committed-store check is evaluated
    eagerly (a pure read).
  - Checks: quickcheck; quick and `REFERENCE=1` selections
    (`TestReferenceAlterAddColumn(Transactions|Cancellation)`,
    `MaterializedRefreshConcurrentLifecycle`, `PartialIndexConcurrent`,
    `TruncateBoundaries`, `ForeignActionsConcurrentDDL`); `RACE=1` on the
    concurrent selections; deps. `make bench` with interleaved base and
    branch binaries: 5461–5479 allocations per op on both, ns/op overlapping
    (1.6–3.1 ms on both, above the 1 ms target only under load 14–170);
    `reference/tables` CPU time within noise (217/283 s branch,
    234/277 s base, interleaved). The `-gcflags=-m` diff: no new
    allocation on the publication path. Racecheck and finalcheck skipped
    (user decision).
  - For 177: `catalogStore` reads in the moved code now come in as values
    (`e.catalog.state`, `DatabaseAddress()`, `nextRevision`), so the rename
    touches only the root call sites.
- 177: done (`46cfad58e`, `bdddf63f9`, docs commit), started at master
  `73e7fb5ff` (item 176), rebased on `4b5a5ad64`.
  - Commit 1 is scripted and re-runnable
    (`.cswd/tmp/PG18-ENGINE-SPLIT-CATALOG-STORE/c1.sh`): a new go/types
    tool, `storerw`, turns the state and frontier writes into
    `PublishLocked` and `Install…FrontierLocked` calls (13 production
    publication points, about 150 with tests), frontier reads into `NextOID` and
    `NextRevision`, `owner()` into `&s.Owner` and the `read(state)` pairs
    into `Current()`; `movex` exports the rest, including
    `OIDReservation.Base` and `Next`.
  - Commit 2 moves the store to `catalog/store.go` and the database
    helpers to `catalog/database_projection.go`; the `ddl.Allocator`
    assertion stays in the root, because `catalog` cannot import `ddl`.
  - Deviations: `ProjectDatabasesLocked` and `DatabaseOIDLocked` take the
    cluster's stores as an `iter.Seq[*Store]` (the root's
    `databaseCluster.catalogs` method value) instead of `[]*Store`, so
    publication builds no slice; `-gcflags=-m` shows it does not escape.
    Install is `DatabaseProjection.InstallLocked`. CREATE DATABASE now
    calls `RetainDatabasesLocked` instead of its inline copy. Without a
    reader for the enum frontier, the root enum tests probe it with
    `AllocateEnumMemberLocked` on both sides.
  - Checks: quickcheck; quick and `REFERENCE=1` selections
    (`TestReferenceDatabaseCreate` as a prefix of its eleven tests,
    `DatabaseIdentity`, `ControlSnapshotSurvivesCatalogReplacement`,
    `DriverBatchFeatureSmoke`, `DateDumpRestoreAndPostgresReplay`); deps.
    `make bench` with interleaved base and branch binaries: 5,462
    allocations per op on both, ns/op overlapping (about 5 ms on both under
    load 74–98). Racecheck and finalcheck skipped (user decision).
  - For 179: the role reads pass `&e.catalog.Owner` where they called
    `catalog.owner()`, so `PublishedRead` replaces
    `roles.read(&e.catalog.Owner, view, roles.published)`.
- 178: done (`19c0852b8`, `8b2e99d16`, docs commit), started at master
  `7245ce77e`, rebased on `39c2be6b9`. `reach` showed every declaration in
  the move set dirty only through the `*Engine` receiver.
  - `catalog/seed.go`: `InstallBuiltinNamespaces`, `InstallBuiltinTypes`,
    `InstallBuiltinCollations`, `InstallBuiltinConversions`,
    `InstallBuiltinProcedures` (variadic groups, then the native access
    methods), `InstallBuiltinOperators`, `Store.InstallBuiltinLanguagesLocked`
    and `InstallCatalogSource`; `InstallBuiltinTablespaces` sits next to
    `BuiltinTablespaces`. The root files `tablespace_catalog.go` and
    `plpgsql_bootstrap.go` are gone; `registerCatalogSource` became a
    `pgcatalog.InitializeAttributes` call plus `InstallCatalogSource`.
  - `catalog/seed_share.go`: the three process-level memos became one
    `SeedShare` with `ShareProcedureNamesLocked`, `FinishSeedLocked`,
    `ValidateSeed` and `ValidateDescriptors`. The root keeps the one
    `builtinSeed` share, so the root seed-name test swaps the whole share.
  - Deviations: `installBuiltinLanguages` moved whole; its only root part
    was the owner, now passed in. `newEngine` reads the bootstrap owner and
    the `postgres` and `pg_database_owner` refs once instead of per loop
    iteration. New units: `TestSeedShareNamesMatchOwnSeed` and
    `TestInstallCatalogSourceOwnsRowType`.
  - Checks: quickcheck; quick and `REFERENCE=1` selections
    (`TestReference(PGTypeLifecycle|RangeTypeProviders|PLpgSQLCoreLanguageAuthority)`);
    the root seed units and the catalog store and seed units; deps.
    `make bench` with interleaved base and branch binaries under load 43–54:
    allocations vary per process on both (base 5,609–5,618, branch
    5,611–5,622 at one CPU; GC off 5,611–5,618 base, 5,617–5,618 branch),
    bytes per op flat, `-gcflags=-m` shows the same heap sites and the
    variadic procedure groups do not escape. ns/op overlap: about 1.25–1.3 ms
    on both with GC off, 2–12 ms under load, above the 1 ms target on both.
    Racecheck and finalcheck skipped (user decision).
  - For 179: `newEngine` reads `e.roles.domain` and `e.roles.published`
    once each for the seed owners; `bootstrapAuthorization` still calls
    `e.roles.read`.
- 179: not started.
