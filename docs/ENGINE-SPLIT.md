# Splitting `internal/engine`

Status: rounds 1–4 and two closing reviews (linear items 1–151) are done. The
second closing review (item 151) measured the root at `fe0683b68` and planned
a fifth round (§4, items 152–167). It also reset this file, the
[status page](ENGINE-SPLIT-STATUS.md) and the per-package prompt files to
what the next sessions need. The earlier analysis, step plans and session
records are in git history: `git show 71747c25c:docs/ENGINE-SPLIT.md`, and
the same for `ENGINE-SPLIT-STATUS.md` and `ENGINE-SPLIT-<package>.md`.

`docs/design/*` is background only; where it disagrees with this document,
this document wins.

## 1. Where the split stands

Measured at `fe0683b68` with `reach` (§2.4).

- **Size.** The root package has 384 production files and 46.4k lines:
  17.1% of the engine's 270.9k production lines. The 35 subpackages hold
  224.5k. The root unit tests are 1,217 files and 234.0k lines.
- **Clean code is almost gone.** 351 of the 1,772 root declarations are
  clean (they never reach `Engine`, `StatementScope` or `CatalogStage`), but
  they are only 3.3k lines: small root types, slot accessors, option parsers
  and the two stores.
  - 38.2k lines are dirty: `Engine` methods 20.4k, `StatementScope` methods
    9.1k, `CatalogStage` methods 0.3k.
  - The other 8.4k are free declarations that reach a god struct first through
    `statementHost` (1.1k with its `scope` field), `dropProvider` (1.0k),
    `StatementScope` (0.9k with its `engine` field), `restoreRun` (0.5k),
    `Engine` (0.5k), `ddlHost` (0.4k) and `engineCatalogRuntime` (0.4k).
- **God structs and adapters.**
  - `Engine` (`engine.go`): 462 methods in 218 files.
  - `StatementScope` (`transaction.go`): 277 methods in 116 files.
  - `CatalogStage` (`catalog.go`): 42 methods in 16 files.
  - Root adapters: `statementHost` 137 methods, `ddlHost` 87, `dropProvider`
    60, `bindingHost` 51, `engineCatalogRuntime` 38, `AuthorizationView` 33,
    `extensionHost` 17, `alterHost` 10, `enginePreparation` 9.
- **New feature code still lands in the root.** About 2.5k lines arrived
  after the fourth round was planned (`4c05da8fe`): VACUUM, the RLS runtime,
  the `pg_partition_*` functions, CREATE DATABASE and smaller files. Feature
  work should put its decisions in the owning package from the start (§5.2).

| Area (files) | Lines | Clean | Main content left |
|---|---:|---:|---|
| Statement dispatch, protocol, portals, cursors, prepared SQL (31) | 6.3k | 0.8k | dispatch, `relation_scope.go`, portals, cursors, host adapters |
| Transactions, sessions, TEMP, notifications (32) | 5.2k | 0.2k | publication, CHAIN, 2PC, savepoints, TEMP lifetime, setting windows |
| Tables, with FK runtime (40) | 4.0k | 0 | `alterTableLocked`, CREATE TABLE, FK grants, Restore cores |
| Type leftovers (30) | 3.5k | 0.1k | type executors, domain dependents and checks, domain replay |
| Views, rules, policies, RLS, materialized views (25) | 3.0k | 0.1k | refresh, rule execution, RLS runtime, lock waits |
| DML, COPY, TRUNCATE (18) | 3.0k | 0.1k | host windows, COPY protocol, writable CTEs, TRUNCATE |
| Catalog stage, publication, replacement (13) | 2.8k | 0.3k | `publishCatalog`, `replaceCatalog`, stage API, overlays |
| Schema DDL, DROP, renames, addresses (16) | 2.4k | 0.3k | `dropProvider`, schema DDL, builtin catalog seeding |
| Roles, ACL, databases (27) | 2.3k | 0.3k | role executors, authorization capture, CREATE DATABASE |
| Indexes (27) | 1.8k | 0 | build orchestration, host windows |
| Partitions (17) | 1.6k | 0.2k | ATTACH/DETACH, lock admission, `pg_partition_*` |
| Inheritance (14) | 1.4k | 0 | admission, Restore repair |
| Dump/Restore remainder (13) | 1.4k | 0.1k | hook bodies, admission table |
| Sequences and identity (10) | 1.2k | 0.1k | sequence and identity DDL executor |
| Function glue, large objects, `pgrowlocks` (14) | 1.1k | 0 | `*_runtime.go` host methods |
| VACUUM (4) | 0.9k | 0.2k | options, targets, lock windows, maintenance metadata |
| Foreign data wrappers (12) | 0.9k | 0.1k | participants, ALTER loop |
| SQL routines (7) | 0.8k | 0.1k | SQL routine invocation |
| Query admission (6) | 0.8k | 0.1k | admission and source capture |
| System catalog runtime (5) | 0.8k | 0 | `pgcatalog.Runtime` adapter |
| `engine.go` (1) | 0.6k | 0.1k | `New`, `describeOperation` |
| Extensions (10) | 0.5k | 0 | install, update and drop orchestration |
| reg* and expression leftovers (12) | 0.4k | 0 | output capture, spelling caches |

**What stays in the root, and why.** About 43k lines stay after the fifth
round:
- dispatch and the facade;
- the host adapters (`statementHost`, `bindingHost`, `ddlHost`, `alterHost`,
  `restoreHost`, `extensionHost`, `engineCatalogRuntime`, the `*_runtime.go`
  bodies);
- admission and lock waits;
- handler skeletons and binder bodies;
- protocol, prepared SQL, cursors, writable CTEs and SQL routines;
- the transaction lifecycle, sessions, TEMP and the settings windows;
- catalog publication and replacement;
- type executors, the DROP core and the Restore hook bodies;
- sequence DDL and TRUNCATE.

Each of these takes `Engine.mu`, `owner.Mu` or `Session.Mu`, waits in the
lock manager, publishes, builds a binder or re-enters dispatch, so §3 rules
3, 4 and 6 keep them in the root.

**What can still move.** Three surveys found about 3.5k lines of decisions
that are clean after one cut and fit a package that already owns the area,
plus about 0.4k lines of dead, test-only or duplicated root code. They are
the fifth round (§4). No survey found code that needs a new package.

## 2. Method

### 2.1 Phases

Every move keeps the build green:
1. **Seam.** If the target package lacks the type, interface or method the
   moved code needs, add it first.
2. **Cut in place.** Refactor the root code to use only that seam: replace
   the god-struct edge with a value input, a callback or an interface (§2.2).
   This is the risky phase, and it happens where the compiler and the
   existing tests cover everything.
3. **Move.** Move the declarations and their tests. Qualify every reference
   in the same commit: no aliases. A one-line root wrapper may stay where
   many root files call the function.

### 2.2 Cuts

1. **State ownership in place.** Group a subsystem's fields into one struct,
   turn the methods that touch only it into its methods, then move it. The
   struct has no mutex; mutating methods carry the `Locked` suffix.
2. **Slot.** A field of a shared struct that only one feature uses moves into
   a feature-owned struct behind one pointer and one accessor; reset and copy
   code handle the slot as a unit.
3. **Capability.** The package declares a narrow interface of value-taking
   methods; the root implements it with a compile-time assertion. It never
   returns root structs and never grows into a service locator.
4. **Registration.** A central switch over kinds becomes an ordered table that
   the root builds once, in the switch's order (rule 8).
5. **Reader interface.** A helper that borrows a root struct only for a few
   reads gets a small reader interface that the root struct and a
   `catalog.Read` adapter both implement.
6. **Value input or callback.** The caller reads the state first and passes
   it by value; a root effect (a wait, a notice, binding, progress) becomes a
   callback that runs with nothing held. Code that runs under `Engine.mu`
   becomes a `…Locked` function that takes no lock.
7. **Narrow host method.** Add a method to an interface the target package
   already declares, implemented by the existing root adapter. Each item
   names the methods it adds. A new host interface type is a decision, not a
   default; a host for a whole pipeline is a service locator and is rejected.

### 2.3 Tests

- `reference/` is the golden behaviour suite and is read-only during the
  split; it needs no new tests, because behaviour does not change.
- Root unit tests never decide what moves. Tests of moved code move with it.
  Tests that need an engine fixture stay and are rewritten to the new names,
  or are deleted when they pin internals that no longer exist or when
  `reference/` covers their behaviour (name the covering test).
- New unit tests only pin a new seam's semantics.

### 2.4 Measuring

`reach` is a small `go/packages` program kept outside git, in
`.cswd/tmp/PG18-ENGINE-SPLIT-EXEC-REFINE/reach/`. If it is missing, rebuild
it from this description. For every top-level root declaration in the files
selected by `-files <regexp>`, it follows package-level references and
method calls to the first god struct and prints `clean` or the path:
- struct fields are not followed;
- a method reaches its receiver type.

`-cut 'From>To,From>*'` drops edges, which tests whether a planned cut makes a
set clean. Line counts are declaration spans. The area map and the script used
for §1 are in `.cswd/tmp/PG18-ENGINE-SPLIT-CLOSING-REVIEW-2/`. Re-run `reach`
on an item's move set before planning it: the sizes in §4 are approximate.

## 3. Target architecture

| Layer | Package | Owns |
|---|---|---|
| L-1 | `engine/fts` | immutable full-text-search codecs; below `sqltype`, imports only `sqlstate` |
| L0 | `parser`, `sqltype`, `sqlstate` | grammar and AST, SQL types and codecs, shared errors |
| L1 | `engine/ids` | identity values and the provenance `Domain` |
| L1 | `engine/authorization` | roles, membership and ACL algorithms over immutable values |
| L1 | `engine/largeobject` | immutable large-object bytes and replay shapes |
| L1 | `engine/settings` | setting definitions, parsing, formatting and validation |
| L1 | `engine/proc` | SQL routine definitions and invocation frames |
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
| L7 | `engine/exec` | the executor runtime and the concrete evaluation context |
| L7 | `engine/session` | sessions, operations, the transaction owner, the journal, the settings store, the protocol cycle |
| L8 | `engine/ddl` | the shared DDL contract (`Authority`, `Stage`, `Host`), schema, tablespace and LOAD decisions |
| L9 | `engine/sequence` | sequences, identity and serial DDL decisions, sequence functions |
| L9 | `engine/routine` | routine and operator inventories; routine, operator, cast and aggregate DDL |
| L9 | `engine/typedef` | domains, enums, composites, typed tables, collations, conversions |
| L9 | `engine/extension` | bundled extension providers, control data, install and update decisions |
| L10 | `engine/analyze` | the binder, `Describe`, `Binding` and `analyze.Host` |
| L11 | `engine/regobj` | reg* types, type declarations, typed literals, `aclitem` I/O, object identity |
| L11 | `engine/stored` | stored-SQL payloads, their renderers, retag and remap |
| L12 | `engine/index` | index keys and roots, unique and exclusion enforcement, publication, AM DDL |
| L12 | `engine/table` | columns, constraints, defaults, generated columns, FKs, ALTER TABLE decisions |
| L12 | `engine/partition` | bounds, routing, key recipes, ATTACH/DETACH/DROP decisions, pruning |
| L12 | `engine/fdw` | wrappers, registry, validators, foreign writes and transaction participants |
| L12 | `engine/view` | view, rule and policy decisions, rewriting, updatable views, matview refresh, RLS selection |
| L12 | `engine/role` | GRANT/REVOKE, SET ROLE, DROP and REASSIGN OWNED, default privileges, COMMENT plans |
| L13 | `engine/dml` | write executors, unique and exclusion probing, FK enforcement and actions |
| L14 | `engine/control` | native Dump and Restore drivers over registered hooks |
| L15 | `engine` (root) | the public facade, dispatch, `StatementScope`, `Engine` orchestration, host adapters |

Each package README states its exact imports, what it owns, its invariants
and what stays in the root.

### Dependency rules

1. **Imports point downward only.** No subpackage imports the root. Packages
   on the same layer do not import each other unless rule 6 says so. Check
   with `go list -f '{{.ImportPath}}: {{join .Imports " "}}' ./internal/engine/...`.
2. **The root is the composition layer.** It wires the subsystems, owns
   sessions and statements, and keeps the facade the driver package uses
   source-compatible.
3. **One lock authority.** `Engine.mu` is the single mutex for catalog,
   storage and visibility state, because publication must stay atomic across
   all three. Subsystem structs have no mutex; code that runs under it is a
   `…Locked` function that takes no lock. The lock manager's mutex is
   innermost (lock README); the session mutex order is in the session
   README.
4. **Packages own data and decisions; the root owns orchestration.**
   Acquiring `Engine.mu`, lock waits, publication, cancellation fences,
   binder construction and dispatch re-entry stay in the root.
5. **`functions` stays a leaf library** over `expr`, `ids`, `sqltype`,
   `parser` and `sqlstate`. Engine state reaches it through narrow
   capabilities that the evaluation context implements.
6. **Feature packages import each other only along these arrows.**
   - Lower features: `sequence` → `routine`, `typedef` → `extension`.
     `analyze` may import them; they never import `analyze`.
   - Upper features: `index` → `table` → `partition`; `table` → `fdw`;
     `table` → `view`; `index` → `partition`; `index` → `view`; `role` stands
     alone.
   - `dml` may import every upper feature, and `control` every upper feature
     except `dml`.
   - Upper features may import `analyze` types and pure functions, `regobj`
     and `stored`, but never construct a binder: binding goes through a root
     host.
   - `analyze`, `exec`, `session`, `pgcatalog` and the lower features never
     import an upper feature, `regobj` or `stored`.
   - `session` never imports `exec`, `analyze` or a feature package.
7. **Lower layers never name upper-layer types.** They hold an interface or an
   opaque payload, and the owner recovers the concrete type with one checked
   helper.
8. **Registration replaces central switches.** Code that lists every feature
   becomes an ordered table that the root composes in today's order, so error
   precedence and output order stay byte-identical.

## 4. Fifth round

Planned by item 151 at `fe0683b68`. Rules:
- one session per item;
- no new package (VACUUM's movable part is about 0.5k lines, so it goes to
  `table`);
- cuts follow §2.2, and each item names the interface methods it adds;
- no aliases;
- behaviour, error text, error order, notice order, lock order and Dump
  output stay identical;
- items that touch a per-statement or per-row path run `make bench` before
  and after and add no allocations.

Item specs live in the prompt files linked below; item 152 is root-only and
specified here. Dependencies come from shared files; items with no needs may
run in parallel.

| Item | Target | Moves | ≈ Lines | Needs |
|---|---|---|---:|---|
| 152 | root | [cleanup: dead, test-only and duplicated code](#item-152--root-cleanup) | −0.3k | — |
| 153 | `catalog` | [candidate publication helpers](ENGINE-SPLIT-catalog.md#item-153--candidate-publication-helpers) | 0.38k | — |
| 154 | `table` | [DROP dependency decisions](ENGINE-SPLIT-table.md#item-154--drop-dependency-decisions) | 0.37k | 153 |
| 155 | `session` | [journal observation and state fences](ENGINE-SPLIT-session.md#item-155--journal-observation-and-state-fences) | 0.21k | 153 |
| 156 | `table` | [VACUUM decisions](ENGINE-SPLIT-table.md#item-156--vacuum-decisions) | 0.49k | — |
| 157 | `partition` | [`pg_partition_*` values](ENGINE-SPLIT-partition.md#item-157--pg_partition_-values) | 0.18k | — |
| 158 | `table` | [ALTER TABLE pass helpers, IF EXISTS no-op](ENGINE-SPLIT-table.md#item-158--alter-table-pass-helpers-and-the-if-exists-no-op) | 0.28k | — |
| 159 | `view` | [RLS policy import, matview snapshot rows](ENGINE-SPLIT-view.md#item-159--rls-policy-import-and-materialized-snapshot-rows) | 0.22k | — |
| 160 | `session` | [owner transitions and the 2PC registry](ENGINE-SPLIT-session.md#item-160--owner-transitions-and-the-2pc-registry) | 0.20k | — |
| 161 | `session` | [statement classification](ENGINE-SPLIT-session.md#item-161--statement-classification) | 0.17k | 160 |
| 162 | `session` | [statement data views](ENGINE-SPLIT-session.md#item-162--statement-data-views) | 0.17k | 161 |
| 163 | `exec` | [SQL cursor positioning](ENGINE-SPLIT-exec.md#item-163--sql-cursor-positioning) | 0.20k | — |
| 164 | `session` | [settings, DISCARD and setting calls](ENGINE-SPLIT-session.md#item-164--settings-discard-and-setting-calls) | 0.16k | 161, 163 |
| 165 | `session` | [protocol value codec](ENGINE-SPLIT-session.md#item-165--protocol-value-codec) | 0.24k | — |
| 166 | `ddl` | [CREATE DATABASE decisions](ENGINE-SPLIT-ddl.md#item-166--create-database-decisions) | 0.16k | — |
| 167 | docs | [closing review 3](#item-167--closing-review-3) | — | 152–166 |

### Item 152 — root cleanup

Remove root code that has no production purpose. Nothing moves to a package.
- **Empty files.** Delete `transaction_time.go` and `not_null_candidate.go`
  (each holds only `package engine`).
- **Test-only API.** These have no production caller in the module
  (including `cmd/` and the driver). Move them to root `_test.go` helpers, or
  rewrite their tests to public SQL:
  - `Engine.SetObjectACL` (`authorization.go`) and `Engine.Comment`
    (`comment.go`);
  - `ValidateObjectRef` and `PublishCatalog` (`catalog.go`);
  - `captureCatalogSourceLocked` and `explicitCatalogSource`
    (`catalog_source_capture.go`);
  - `definitionProceduresNamed`, `stageDefaultDependents`
    (`default_dependencies.go`) and `viewDependencyClosure`
    (`view_dependencies.go`).
- **Dead code.** Delete `AuthorizationView.definitionDescriptors`,
  `AuthorizationView.definitionRelation` and `schemaNotice`, and the ddl
  README note that `schemaNotice` is shared with FDW.
- **Unused role references.** `SetRoleReferences` is the only writer of
  `roleStore.references`, and nothing in production calls it, so the
  registry is always empty. Delete all of it:
  - `roleStore.references`, `setReferencesLocked` and `referencingFeatures`;
  - `ddlHost.ReferencingFeatures` and the `role.Host.ReferencingFeatures`
    method with its DROP ROLE loop (`role/roles.go`);
  - the callback argument of `role.RetirementPreflightLocked`.

  Adapt its 8 test uses.
- **One-caller wrappers.** Inline `remapCatalogExpressions` and
  `bindDomainConstraints`.
- **Single-fence files.** Merge `commit_timestamp_store.go`,
  `transaction_identity_store.go` and `unique_lock.go` into
  `transaction_visibility_registry.go`.
- **Duplicates.**
  - Collapse the 12 identical OID cases of `ValidateCapture`
    (`catalog_source_runtime.go`); they differ only in the error name.
  - Replace the five trusted-Restore blocks of `statementAuthorizationLocked`
    with one ordered table.
  - Merge the RoleSelection and SessionAuthorization install blocks of
    `executeRole`.
  - Merge the three builtin procedure-install loops.
- **Verify:**
  - `make build`, the root units that used the moved helpers, and quickcheck;
  - quick and `REFERENCE=1` selections for GRANT/REVOKE, COMMENT, DROP ROLE
    and DROP OWNED, SET ROLE and SESSION AUTHORIZATION, the `pg_catalog`
    capture paths, and trusted Restore.

### Item 167 — closing review 3

Docs only, after items 152–166. Re-measure the root with `reach` (§2.4) and
rewrite §1. Then decide whether a sixth round is worth planning; if not,
close the split. Keep this file, the status page and the prompt files at
the size this reset left them.

### Not planned

- **Weak candidates** (under about 0.1k lines each, or not clean after one
  cut):
  - the ALTER SEQUENCE action switch and the index build stores in
    `publishCatalog`;
  - the SQL/MED Restore admission and the rename decisions;
  - `clearOperatorLinks` and the large-object host body;
  - the journal-to-storage publication transforms and the TEMP leftovers;
  - the catalog-source capture policy and the small readers
    (`LatestQueryRow`, the fastpath checks, the predicate-lock capture);
  - the COPY default loop, the row-version builders and the FDW ALTER loop.
- **Prepared statements, portals and cursors as one package.** The driver
  names `StatementDescription`, `sqlCursor` holds `AuthorizationView`s and
  executor state, and the move would need an aliased facade and a host of
  about eight methods.
- **`restore_steps_*.go` to `control`.** The hooks call 36 engine entry
  points, which would make a `restoreRun` service locator.
- **The domain and typed executors** (`domain_check.go`,
  `typed_mutation.go`, `typed_type_alter.go`, `domain_dependencies.go`).
  They bind through the root binder and run the ALTER TABLE candidate
  functions.
- **`AuthorizationView` to `access`, and `catalogStore` and `roleStore` to
  packages.** That is thousands of field sites for a few hundred lines.
- **ALTER TABLE as a `table` state machine.** It would need about 14 host
  methods and a hidden `CatalogStage`.
- **Hosts for the whole DROP pipeline or for `restoreRun`.** These are
  service locators.
- **Composition points:** `catalogDependencyPreflight`, the `publishCatalog`
  post-apply validators and `replacement_admission.go`.
- **The root's own work:** handler skeletons, acquire loops, binder bodies,
  host adapters, publication and the SQL routine helpers.

## 5. Sessions

Start a session with "Implement Item N from Linear Plan in
`docs/ENGINE-SPLIT-STATUS.md`". The item's section in its prompt file, or in
§4, is the scope.

### 5.1 Protocol

1. **Read** `AGENTS.md`, `docs/TESTING.md`, §2–§5 of this file, the item's
   section, the target package README and the README of every package the
   item touches.
2. **Check prerequisites.** Compare the item's needs with `git log` and the
   status page. If one is missing, stop and report.
3. **Re-measure.** Run `reach` on the move set and confirm that the cut makes
   it clean. Present a file-level plan and wait for approval (plan mode). The
   plan covers:
   - the declarations that move;
   - new methods and types;
   - the call sites that change;
   - the commit sequence, risks and docs.
4. **Implement** in small commits: cut in place, then move. Every commit
   builds and passes its targeted tests.
5. **Verify** (§5.3), **document** (§5.4) and **report** (§5.5).

### 5.2 Constraints

- **Pure refactor.** No behaviour change, no SQL surface change, no new
  dependencies.
- **Read-only suites.** `reference/` and `smoke/` are read-only. Never run
  `make refcheck`.
- **Layering and locks.** Follow §3, including one lock authority.
  Subsystem packages add no mutexes.
- **No aliases.** No compatibility shims beyond the one-line wrappers of
  §2.1.
- **Tests.** Follow §2.3, and list every moved, deleted or substantially
  rewritten test in the report.
- **Scope.** Record findings for later items in the prompt file's Status
  section instead of doing them. New feature code that lands in the root
  during the round is a finding for item 167.
- **Git.**
  - Work in a worktree under `.cswd/work/<NAME>` on the branch
    `implement-part/<NAME>`, with temporary files in `.cswd/tmp/<NAME>/`.
  - Record the master commit at the start and at every rebase.
  - Rebase onto master before verifying. Merge with `--ff-only` only after
    quickcheck passed on the rebased branch.

### 5.3 Verification

- `make build` after every commit.
- Targeted `make check RUN='...'` for the affected tests while working. Add
  `RACE=1` for locks, sessions, cancellation and publication.
- `make quickcheck` (1800 s deadline) before merging; its quick-mode
  reference suite covers the public paths.
- `make check REFERENCE=1 RUN='...'` once, at the end of the session, for
  the item's selections, kept to a few prefixes. This needs Docker; if
  Docker is unavailable, report the check as blocked.
- `make bench` before and after an item that touches startup or a
  per-statement or per-row path. Report ns/op and allocations against the
  1 ms startup target.
- `make deps`: the output may contain only this module's paths.
- Do not run `make racecheck` or `make finalcheck` until the race suite is
  fixed (user decision); report them as skipped.
- Report failures unrelated to the change instead of working around them.

### 5.4 Documentation

- **Package README.** Update the contract: what the package owns, its
  invariants, its entry points, what stays in the root and its `## Testing`
  selectors.
- **Prompt file.** Add the item's Status entry: commits, deviations and
  notes for later items.
- **Status page.** Tick the item and refresh "Next ready items" and "Last
  verified", following its rules.
- **This file.** Change it only when the plan changes or a statement in it
  proves wrong.
- **Other docs.** Grep `docs/`, `README.md` and the package READMEs for moved
  identifiers and old file names.
- **`AGENTS.md`** must not grow.

### 5.5 Report

End the session with:
- what moved, and the seams and methods added;
- the commits;
- the verification results, including bench numbers and skipped or blocked
  checks;
- the tests moved, adapted or deleted;
- the docs changed, with the `AGENTS.md` line delta;
- deviations from the plan and follow-ups;
- the next ready items.
