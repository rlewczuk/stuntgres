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

- [ ] 5a — `catalogStore` state ownership
- [ ] 5b — Typed read API, map reads migrated, writes confined, cut 1
- [ ] 5c — View, rule, policy, extension-provider and expression payload seams
- [ ] 5d — Descriptor names decided, fields exported
- [ ] 5e — `catalog` created, types moved, `Candidate` split from
  `CatalogStage`
- [ ] 5f — Aliases removed
- [ ] 5g — Optional `relationDescriptor` regrouping

Exported names chosen: none yet.

Outstanding aliases: none yet.

Notes for later steps: none yet.
