# Splitting `internal/engine`

Status: five rounds and three closing reviews (linear items 1–167) are done.
Item 167 measured the root at `ea8c445fe`, planned the sixth round (§4,
items 168–180) and reset this file, the [status page](ENGINE-SPLIT-STATUS.md)
and the prompt files. The earlier analysis, specs and session records are in
git history: `git show ea8c445fe:docs/ENGINE-SPLIT.md`, and the same for
`ENGINE-SPLIT-STATUS.md` and `ENGINE-SPLIT-<package>.md`. `docs/design/*`
is background only; where it disagrees with this document, this document
wins.

## 1. Where the split stands

Measured at `ea8c445fe` with `reach` (§2.4).

- **Size.** The root has 402 production files and 48.9k lines: 16.5% of the
  engine's 296.1k. The 35 subpackages hold 247.2k. The root unit tests are
  1,307 files and 251.5k lines.
- **The root grew.** It had 46.4k lines when the fifth round was planned
  (`fe0683b68`). That round moved about 3.5k out, but about 6k of new feature
  code landed in the root at the same time: PL/pgSQL (2.2k), triggers (0.7k),
  Restore comparison, the FTS rewrite, catalog name locks and smaller files.
  Feature work must put its decisions in the owning package from the start
  (§5.2).
- **Clean code is almost gone.** 375 of 1,866 root declarations (3.5k lines)
  never reach `Engine`, `StatementScope` or `CatalogStage`. The rest is
  dirty:
  - god-struct methods, 31.8k lines: `Engine` 20.8k, `StatementScope` 10.8k,
    `CatalogStage` 0.3k;
  - free declarations, 8.4k lines. They first reach a god struct through
    `statementHost` (1.0k with its `scope` field), `dropProvider` (0.8k),
    `StatementScope` (0.8k with its `engine` field), `restoreRun` (0.7k),
    `Engine` (0.5k), `engineCatalogRuntime` and `ddlHost` (0.4k each) or
    `bindingHost` (0.3k).
- **God structs and adapters.**
  - God structs: `Engine` (470 methods in 221 files), `StatementScope` (328
    in 127) and `CatalogStage` (37 in 14).
  - Adapters: `statementHost` 143 methods, `ddlHost` 89, `dropProvider` 57,
    `bindingHost` 52, `engineCatalogRuntime` 44, `AuthorizationView` 30 and
    `extensionHost` 17.

| Area (files) | Lines | Clean | Main content left |
|---|---:|---:|---|
| Statement dispatch, protocol, portals, cursors, prepared SQL (30) | 6.2k | 0.7k | dispatch, `relation_scope.go`, portals, cursors, host adapters |
| Transactions, sessions, TEMP, notifications (28) | 5.0k | 0.2k | publication, CHAIN, 2PC, savepoints, TEMP lifetime, setting windows |
| Tables, with FK runtime (40) | 4.0k | 0 | `alterTableLocked`, CREATE TABLE, FK grants, Restore cores |
| Type leftovers (31) | 3.5k | 0.2k | type executors, domain dependents and checks, domain replay |
| DML, COPY, TRUNCATE, command effects (20) | 3.1k | 0.1k | host windows, COPY protocol, writable CTEs, TRUNCATE |
| Views, rules, policies, RLS, materialized views (25) | 2.8k | 0.1k | refresh, rule execution, RLS runtime, lock waits |
| Catalog stage, publication, replacement, the store (10) | 2.7k | 0.3k | `publishCatalog`, `replaceCatalog`, stage API, overlays, name locks |
| Schema DDL, DROP, renames, builtin seed (15) | 2.3k | 0.3k | `dropProvider`, schema DDL, builtin catalog seeding |
| PL/pgSQL (11) | 2.2k | 0.4k | the statement walk, cursors, exception checkpoints, compile, DO |
| Roles, ACL, databases (27) | 2.0k | 0.2k | role executors, authorization capture, the role store |
| Dump/Restore (17) | 1.9k | 0.2k | admission, hook bodies, Restore comparison |
| Indexes (27) | 1.8k | 0 | build orchestration, host windows |
| Inheritance (14) | 1.4k | 0 | admission, Restore repair |
| Partitions (17) | 1.4k | 0 | ATTACH/DETACH, lock admission |
| Sequences and identity (10) | 1.1k | 0.1k | sequence and identity DDL executor |
| Query admission, FTS rewrite, nested rows (9) | 1.1k | 0.3k | admission and source capture, rewrite re-entry |
| Function glue, large objects, `pgrowlocks` (14) | 1.1k | 0 | `*_runtime.go` host methods |
| System catalog runtime (7) | 0.9k | 0.1k | `pgcatalog.Runtime` adapter, catalog view sources |
| SQL routines (7) | 0.9k | 0.1k | SQL routine invocation |
| Foreign data wrappers (12) | 0.9k | 0.1k | participants, ALTER loop |
| Triggers (5) | 0.7k | 0 | trigger DDL, partition sync, the firing host |
| `engine.go`, extensions, VACUUM, reg*, others (26) | 1.9k | 0.1k | `New`, install/update/drop orchestration, lock windows |

**What stays in the root, and why.** About 46k lines stay after the sixth
round:
- dispatch, the facade and the host adapters;
- admission, lock waits, handler skeletons and binder bodies;
- protocol, prepared SQL, cursors, writable CTEs and SQL routines;
- the transaction lifecycle, sessions, TEMP and the settings windows;
- catalog publication and replacement;
- the type executors, the DROP core and the Restore hook bodies;
- PL/pgSQL binding, cursors and exception checkpoints;
- sequence DDL and TRUNCATE.

Each of these takes `Engine.mu`, `owner.Mu` or `Session.Mu`, waits in the
lock manager, publishes, builds a binder or re-enters dispatch, so §3 rules
3, 4 and 6 keep it in the root.

**What can still move.** Three surveys at `1f5b91b57` found about 3.2k
lines that fit a package that already owns the area. They are the sixth
round (§4):
- PL/pgSQL: diagnostics first, then the statement walk behind a `proc` host;
- trigger DDL and partition sync;
- the Restore comparison state machine and the executor row owners;
- the prepared DROP log;
- the publication sub-steps;
- the catalog and role stores. These were rejected before for their site
  counts, but most of those sites are tests (§2.3).

No survey found code that needs a new package.

## 2. Method

### 2.1 Phases

Every move keeps the build green:
1. **Seam.** Add the type, interface or method the moved code needs to the
   target package first.
2. **Cut in place.** Refactor the root code to use only that seam (§2.2),
   where the compiler and the existing tests still cover everything.
3. **Move.** Move the declarations and their tests and qualify every
   reference in the same commit: no aliases. A one-line root wrapper may stay
   where many root files call the function.

### 2.2 Cuts

1. **State ownership in place.** Group a subsystem's fields into one struct,
   make the methods that touch only it its methods, then move it. No mutex;
   mutating methods carry the `Locked` suffix.
2. **Slot.** A field only one feature uses moves into a feature-owned struct
   behind one pointer and one accessor.
3. **Capability.** The package declares a narrow interface of value-taking
   methods; the root implements it with a compile-time assertion and never
   returns root structs through it.
4. **Registration.** A central switch over kinds becomes an ordered table the
   root builds once, in the switch's order (rule 8).
5. **Reader interface.** A helper that borrows a root struct for a few reads
   gets a small reader interface.
6. **Value input or callback.** The caller passes state by value; a root
   effect (wait, notice, binding, progress) becomes a callback run with
   nothing held. Code under `Engine.mu` becomes a `…Locked` function.
7. **Narrow host method.** Add a named method to an interface the package
   already declares, implemented by an existing root adapter. A new host type
   is a decision, not a default; a host for a whole pipeline is a service
   locator and is rejected.

### 2.3 Tests

- `reference/` is the golden behaviour suite: read-only, and it needs no new
  tests, because behaviour does not change.
- Root unit tests never decide what moves or how. Tests of moved code move
  with it. Engine-fixture tests are rewritten to the new names, or deleted
  when they pin internals that no longer exist or `reference/` covers them
  (name the covering test). Mechanical test-site rewrites, even thousands, do
  not block a move.
- New unit tests only pin a new seam's semantics.

### 2.4 Measuring

`reach` (`.cswd/tmp/PG18-ENGINE-SPLIT-EXEC-REFINE/reach/`, outside git) is a
`go/packages` program. For each top-level root declaration in the files that
`-files <regexp>` selects, it follows package-level references and method
calls to the first god struct and prints `clean` or the path. Struct fields
are not followed; a method reaches its receiver type. `-cut 'From>To,From>*'`
drops edges to test a planned cut. Line counts are declaration spans. §1 comes
from `areas.json` and `measure.py` in
`.cswd/tmp/PG18-ENGINE-SPLIT-CLOSING-REVIEW-3/`. Re-run `reach` on an item's
move set before planning it; the sizes in §4 are approximate.

## 3. Target architecture

| Layer | Package | Owns |
|---|---|---|
| L-1 | `engine/fts` | immutable full-text-search codecs; below `sqltype`, imports only `sqlstate` |
| L0 | `parser`, `sqltype`, `sqlstate`, `util` | grammar and AST, SQL types and codecs, shared errors, SQL-independent data structures |
| L1 | `engine/ids` | identity values and the provenance `Domain` |
| L1 | `engine/authorization` | roles, membership and ACL algorithms over immutable values |
| L1 | `engine/largeobject` | immutable large-object bytes and replay shapes |
| L1 | `engine/settings` | setting definitions, parsing, formatting and validation |
| L1 | `engine/proc` | SQL routine definitions, PL/pgSQL programs and invocation frames |
| L2 | `engine/expr` | the expression contract and node protocol |
| L2 | `engine/lock` | the heavyweight lock manager, with its own innermost mutex |
| L3 | `engine/functions` | builtin functions: signatures, nodes, evaluation, aggregates, windows |
| L3 | `engine/catalog` | descriptors, `State`, `Read`, the provider registry, the `Candidate` overlay |
| L3.5 | `engine/dump` | dump graph records, ordering, `Presenter` and `Resolver` capabilities |
| L4 | `engine/storage` | row versions, deletion roots, index roots, sequence state, `Store` |
| L4 | `engine/access` | authorization enforcement on catalog objects (`access.View`) |
| L4 | `engine/copyfmt` | COPY text, CSV and binary codecs and options |
| L5 | `engine/visibility` | xids, commit order and timestamps, snapshots, SSI |
| L5 | `engine/pgcatalog` | system relation producers |
| L6 | `engine/plan` | the plan IR and the concrete expression nodes |
| L7 | `engine/session` | sessions, operations, the transaction owner, the journal, the settings store, the protocol cycle |
| L8 | `engine/ddl` | the shared DDL contract (`Authority`, `Stage`, `Host`), schema, tablespace, database and LOAD decisions |
| L9 | `engine/sequence` | sequences, identity and serial DDL decisions, sequence functions |
| L9 | `engine/typedef` | domains, enums, composites, typed tables, collations, conversions |
| L9 | `engine/extension` | bundled extension providers, control data, install and update decisions |
| L9 | `engine/routine` | routine and operator inventories; routine, operator, cast and aggregate DDL |
| L9.5 | `engine/exec` | the executor runtime and the concrete evaluation context; above `extension` only for the cube spatial call check |
| L10 | `engine/analyze` | the binder, `Describe`, `Binding` and `analyze.Host` |
| L11 | `engine/regobj` | reg* types, type declarations, typed literals, `aclitem` I/O, object identity |
| L11.5 | `engine/stored` | stored-SQL payloads (including triggers), their renderers, retag and remap |
| L12 | `engine/index` | index keys and roots, unique and exclusion enforcement, publication, AM DDL |
| L12 | `engine/table` | columns, constraints, defaults, generated columns, FKs, ALTER TABLE, DROP and VACUUM decisions |
| L12 | `engine/partition` | bounds, routing, key recipes, ATTACH/DETACH/DROP decisions, pruning, `pg_partition_*` |
| L12 | `engine/fdw` | wrappers, registry, validators, foreign writes and transaction participants |
| L12 | `engine/view` | view, rule and policy decisions, rewriting, updatable views, matview refresh, RLS selection |
| L12 | `engine/role` | GRANT/REVOKE, SET ROLE, DROP and REASSIGN OWNED, default privileges, COMMENT plans |
| L13 | `engine/dml` | write executors, unique and exclusion probing, FK enforcement and actions, trigger firing contract |
| L14 | `engine/control` | native Dump and Restore drivers over registered hooks |
| L15 | `engine` (root) | the public facade, dispatch, `StatementScope`, `Engine` orchestration, host adapters |

Each package README states its exact imports, what it owns, its invariants
and what stays in the root.

### Dependency rules

1. **Imports point downward only.** No subpackage imports the root.
   Packages on the same layer do not import each other unless rule 6 says
   so. Check with
   `go list -f '{{.ImportPath}}: {{join .Imports " "}}' ./internal/engine/...`.
2. **The root is the composition layer.** It wires the subsystems, owns
   sessions and statements, and keeps the facade the driver package uses
   source-compatible.
3. **One lock authority.** `Engine.mu` is the single mutex for catalog,
   storage and visibility state, because publication must stay atomic
   across all three. Subsystem structs have no mutex; code that runs under
   it is a `…Locked` function that takes no lock. The lock manager's mutex
   is innermost (lock README); the session mutex order is in the session
   README.
4. **Packages own data and decisions; the root owns orchestration.**
   Acquiring `Engine.mu`, lock waits, publication, cancellation fences,
   binder construction and dispatch re-entry stay in the root.
5. **`functions` stays a leaf library** over `expr`, `ids`, `sqltype`,
   `parser` and `sqlstate`. Engine state reaches it through narrow
   capabilities that the evaluation context implements.
6. **Feature packages import each other only along these arrows.** Each
   arrow points from the imported package to the package that imports it.
   - Lower features: `typedef` → `extension` → `routine`; `sequence` stands
     alone. `exec` may import `extension`, and `analyze` may import them
     all; they never import `exec` or `analyze`.
   - Upper features: `index` → `table` → `partition`; `table` → `fdw`;
     `table` → `view`; `index` → `partition`; `index` → `view`; `role`
     stands alone.
   - `dml` may import every upper feature, and `control` every upper
     feature except `dml`.
   - Upper features may import `analyze` types and pure functions, `regobj`
     and `stored` (`stored` imports `regobj`), but never construct a binder:
     binding goes through a root host.
   - `analyze`, `exec`, `session`, `pgcatalog` and the lower features never
     import an upper feature, `regobj` or `stored`.
   - `session` never imports `exec`, `analyze` or a feature package.
7. **Lower layers never name upper-layer types.** They hold an interface or
   an opaque payload, and the owner recovers the concrete type with one
   checked helper.
8. **Registration replaces central switches.** Code that lists every
   feature becomes an ordered table that the root composes in today's
   order, so error precedence and output order stay byte-identical.

## 4. Sixth round

Planned by item 167 at `ea8c445fe`. Rules:
- one session per item, and no new package;
- cuts follow §2.2, and each item names the interface methods it adds;
- no aliases;
- behaviour, error text, error order, notice order, lock order and Dump
  output stay identical;
- items on startup, a per-statement or a per-row path run `make bench`
  before and after and add no allocations.

Item specs live in the prompt files linked below; items 168 and 180 are
specified here. Dependencies come from shared files, and items with no
needs may run in parallel. The store items (177–179) rewrite about 190
production files, so they wait until every other code item has merged.

| Item | Target | Moves | ≈ Lines | Needs |
|---|---|---|---:|---|
| 168 | root, `catalog`, `fdw`, `routine`, `session` | [cleanup and clean-now moves](#item-168--cleanup-and-clean-now-moves) | 0.2k | — |
| 169 | `proc`, `plan` | [PL/pgSQL diagnostics, error phases and flow](ENGINE-SPLIT-proc.md#item-169--plpgsql-diagnostics-error-phases-and-flow) | 0.29k | — |
| 170 | `proc` | [PL/pgSQL statement walk](ENGINE-SPLIT-proc.md#item-170--plpgsql-statement-walk) | 0.45k | 169 |
| 171 | `view` | [trigger DDL decisions](ENGINE-SPLIT-view.md#item-171--trigger-ddl-decisions) | 0.25k | — |
| 172 | `view` | [trigger partition synchronization](ENGINE-SPLIT-view.md#item-172--trigger-partition-synchronization) | 0.18k | 171 |
| 173 | `control` | [Restore comparison context](ENGINE-SPLIT-control.md#item-173--restore-comparison-context) | 0.29k | — |
| 174 | `exec` | [row owners](ENGINE-SPLIT-exec.md#item-174--row-owners) | 0.17k | — |
| 175 | `table` | [prepared DROP log](ENGINE-SPLIT-table.md#item-175--prepared-drop-log) | 0.37k | — |
| 176 | `index`, `storage`, `session` | [catalog publication sub-steps](ENGINE-SPLIT-catalog.md#item-176--catalog-publication-sub-steps) | 0.3k | — |
| 177 | `catalog` | [`catalog.Store`](ENGINE-SPLIT-catalog.md#item-177--catalogstore) | 0.29k | 168–176 |
| 178 | `catalog` | [builtin catalog seed](ENGINE-SPLIT-catalog.md#item-178--builtin-catalog-seed) | 0.19k | 177 |
| 179 | `access` | [`access.RoleStore`](ENGINE-SPLIT-catalog.md#item-179--accessrolestore) | 0.19k | 177 |
| 180 | docs | [closing review 4](#item-180--closing-review-4) | — | 168–179 |

### Item 168 — cleanup and clean-now moves

Needs —. About 0.2k lines move, and nothing needs a cut. Each move lands
next to the package code that already owns the area, and each moved helper
keeps its one or two root callers.
- **Clean moves.** `reach` reports each one clean at `ea8c445fe`:
  - `rebindReplacementForeignProviders` (`restore_provider.go`) → `fdw`;
  - `rangeTypeRebinder` (`range_reown.go`) → `routine`, next to
    `BindClassSupport`;
  - `samePreparedTypeDefinition` and `sameLogicalUsage`
    (`prepared_type_identity.go`) → `catalog`;
  - `restoreRangeConstructorEligible` (`restore_range_constructor.go`) →
    `catalog`;
  - `catalogBindingClaim`, `catalogBindingCurrent` and
    `catalogBindingDuplicate` (`catalog_name_locks.go`) → `catalog`;
  - `transactionalSchemaUtility` (`transactional_schema.go`) → `session`.

  `admitRestoreInsert` and the acquire and skip methods stay in the root:
  they take `Engine.mu` or wait for locks.
- **Receiver-free methods.** These never use their `*Engine` receiver and
  become free root functions (`gofmt -r` per method), which leaves them
  clean for later moves:
  - `captureRegclassInput`, `captureRegtypeInput`, `captureRoutineInput`,
    `captureRegTSInput` and `captureRegObjectInput` (`captured_input.go`);
  - `captureEnumOperationInput`;
  - `touchOperationIdentityLocked`.
- **Dead API.** `access.WithObjectACL` has no production caller; only its own
  tests and the root `SetObjectACL` test fixture use it. Move it into that
  fixture, or rewrite the fixture over `access` primitives, then delete it
  from `access/view.go`.
- **Verify:**
  - `make build`, `go test -run '^$' ./internal/engine/...` and quickcheck;
  - the root units `restore_provider_test.go`,
    `prepared_type_identity_test.go`, `range_type_routine_remap_test.go`
    and `transactional_schema_test.go`;
  - the quick and `REFERENCE=1` selections
    `TestReferenceRoutineAlterProviderPersistence`,
    `TestReferenceTypeLifecycleIdentity`,
    `TestReferenceRangeTypeRestoreRecipeCollisions`,
    `TestReferenceRangeTypeConstructors` and
    `TestReferenceTransactionalSchemaLocks`.

### Item 180 — closing review 4

Docs only, after items 168–179. Re-measure the root with `reach` (§2.4) and
rewrite §1, then decide whether a seventh round is worth planning; if not,
close the split. Keep these docs at the size this reset left them.

### Not planned

- **Small, or not clean after one cut:**
  - PL/pgSQL cursor decisions: they need 6–8 more `PLHost` methods;
  - `statementHost.BeforeRow`: it needs at least 3 new host methods and an
    `exec.Context`;
  - the `view` DROP dependents pass (0.10k);
  - moves under 0.1k each: builtin view definitions, catalog leaf events,
    the type-owner restore, `mergeStageJournalLocked`, the FTS
    statement-shape checks;
  - earlier leftovers: the ALTER SEQUENCE action switch, the SQL/MED
    Restore admission, `clearOperatorLinks`, the TEMP leftovers, the COPY
    default loop and the FDW ALTER loop.
- **`AuthorizationView` to `access`.** It is blocked by layering: its fields
  name `session` types, and its methods take or return `analyze`, `view`,
  `dml`, `plan` and `stored` types.
- **The `exec` → `extension` edge.** It is one cube spatial call check, and
  §3 documents it.
- **Prepared statements, portals and cursors as one package.** The driver
  names `StatementDescription`, and `sqlCursor` holds `AuthorizationView`s and
  executor state.
- **Hosts for the whole DROP pipeline, `restoreRun` or ALTER TABLE.** These
  are service locators. ALTER TABLE alone would need about 14 host methods.
- **The domain and typed executors.** They bind through the root binder and
  run the ALTER TABLE candidate functions.
- **Composition points and the root's own work:**
  - the `publishCatalog` and `replaceCatalog` skeletons,
    `catalogDependencyPreflight`, the post-apply validators and
    `replacement_admission.go`;
  - handler skeletons, acquire loops, binder bodies and host adapters.

## 5. Sessions

Start a session with "Implement Item N from Linear Plan in
`docs/ENGINE-SPLIT-STATUS.md`". The item's section in its prompt file, or in
§4, is the scope.

### 5.1 Protocol

1. **Read** `AGENTS.md`, `docs/TESTING.md`, §2–§5, the item's section and
   the READMEs of the packages it touches.
2. **Check needs** against `git log` and the status page; if one is
   missing, stop and report.
3. **Re-measure** the move set with `reach` and present a file-level plan in
   plan mode: moved declarations, new methods and types, changed call sites,
   commit sequence, risks and docs.
4. **Implement** in small commits, cut in place then move; every commit
   builds and passes its targeted tests.
5. **Verify** (§5.3), **document** (§5.4) and **report** (§5.5).

### 5.2 Constraints

- **Pure refactor:** no behaviour, SQL-surface or dependency change.
- **Read-only suites:** `reference/` and `smoke/`; never run `make refcheck`.
- **Layering and locks** follow §3; no aliases beyond §2.1's wrappers; tests
  follow §2.3.
- **Scope.** Record findings for later items in the prompt file's Status
  section instead of doing them. New feature code that lands in the root is
  a finding for item 180.
- **Git.** Work in `.cswd/work/<NAME>` on `implement-part/<NAME>`, with
  temporary files in `.cswd/tmp/<NAME>/`. Record the master commit at the
  start and at every rebase. Rebase before verifying; merge `--ff-only` only
  after quickcheck passed on the rebased branch.

### 5.3 Verification

Commands and deadlines are in `AGENTS.md`.
- `make build` after every commit; targeted `make check RUN='...'` while
  working, with `RACE=1` for locks, sessions, cancellation and publication.
- `make quickcheck` before merging.
- `make check REFERENCE=1 RUN='...'` once, at the end, for the item's few
  selections; without Docker, report it as blocked.
- `make bench` before and after an item on startup or a per-statement or
  per-row path: report ns/op and allocations against the 1 ms target.
- `make deps` lists only this module's paths.
- `make racecheck` and `make finalcheck` are skipped until the race suite is
  fixed (user decision); report them as skipped. Report unrelated failures
  instead of working around them.

### 5.4 Documentation

- **Package README:** ownership, invariants, entry points, what stays in the
  root and `## Testing` selectors. Move the matching root README paragraphs
  with the code.
- **Prompt file:** replace the item's "not started" Status line with its
  commits, deviations and notes for later items.
- **Status page:** tick the item, refresh "Next ready items" and "Last
  verified", following its rules.
- **This file** changes only when the plan changes or a statement proves
  wrong. Grep `docs/`, `README.md` and the package READMEs for moved names;
  `AGENTS.md` must not grow.

### 5.5 Report

What moved and the seams added; the commits; verification (bench numbers,
skipped or blocked checks); tests moved, adapted or deleted; docs changed,
with the `AGENTS.md` line delta; deviations and follow-ups; the next ready
items.
