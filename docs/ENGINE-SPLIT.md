# Splitting `internal/engine/core`

Status: the earlier rounds are done. They are linear items 1–204: the root split
into layered packages, then the runtime core and the `*cmds` families (round 7).
Their plan, specs and session records are in git history:
`git show 7b262861a:docs/ENGINE-SPLIT.md`, and the same for the status page and
the `ENGINE-SPLIT-{core,cmds,enginetest}.md` prompt files. Item 205 (closing
review 5) is superseded by this plan.

This round (round 8, items 206–230) splits `internal/engine/core` into packages
under `internal/engine/core/`. `docs/design/*` is background only; where it
disagrees with this document, this document wins.

## 1. Where core stands

Measured at `b7e30241f` with `.cswd/tmp/SPLIT-ENGINE-CORE/measure.py` and
`layout.py` (§2.3). Sizes are Go source bytes per package directory, tests
included.

- **Size.** Core is 11.26 MiB in 1,599 files:
  - production: 435 files, 1.96 MiB;
  - internal tests: 13 files, 0.06 MiB;
  - accessor files (`export_<area>_test.go`): 33 files, 0.10 MiB, 500 names;
  - external tests (`package core_test`): 1,118 files, 9.14 MiB.

  The next largest engine packages are `functions` (2.75 MiB), `schemacmds`
  (2.06) and `tablecmds` (1.63).
- **Tests.**
  - 694 external files (5.27 MiB) use no accessor.
  - 269 files (2.57 MiB) use the hub accessor files. These are
    `export_statement`, `export_txsession`, `export_engine` and
    `export_catalogorch`, which alias scope, transaction, engine and
    catalog-stage internals.
  - 178 files (1.80 MiB) use two or more accessor files.
  - 910 files build the composed engine through the root, and 882 use
    `enginetest`.
  - No core test calls `t.Parallel`, so the core test binary runs serially for
    about 103 s, the longest non-reference package in quickcheck.
- **Production.** Most code is methods on god types: `StatementScope` 419,
  `Engine` 329, `statementHost` 220, `DropProvider` 73, `EngineCatalogRuntime`
  63, `BindingHost` 51, `DDLHost` 49.
  - The leaves are cohesive subsystems with few inbound references. Each has
    its own types or a narrow seam: EXPLAIN rendering, contrib function
    runtimes, the text-search runtime, statistics, native triggers, the
    catalog runtime and inquiry functions.
  - The hubs stay in core: engine state, scope and admission, catalog stage and
    publication, the hosts, the bound query path, transactions, sessions,
    authorization, DML assembly, the SQL routine runtime, protocol, cursors and
    the trigger core.
- **Churn.** About 40 commits a day touch core, and 145–172 core test files
  change a day.

**Targets.**
- Core is under 4 MiB.
- Every package that receives extracted tests is under 2 MiB.
- Every new package lives under `internal/engine/core/`.
- Tests stay with their code: they follow production that moves; the rest go
  to `core/enginetests/<area>` packages, grouped by the code they exercise
  (T3, T8).

**Prefix estimate.** The groups below are by file prefix. Item 206's census
replaces them per test.
- B: leaf cuts below core.
- A: test moves whose owner is a lower or family package.
- C: gated hub cuts.

| Group | Prod KiB | Test files | Tests KiB | Accessor-free KiB | Hub-accessor KiB |
|---|---:|---:|---:|---:|---:|
| B explain | 117 | 19 | 127 | 50 | 36 |
| B catalog runtime | 74 | 91 | 1058 | 737 | 109 |
| B statistics | 54 | 25 | 206 | 206 | 0 |
| B contrib | 74 | 42 | 309 | 161 | 109 |
| B native triggers | 33 | 9 | 53 | 20 | 33 |
| B text search | 36 | 38 | 401 | 389 | 12 |
| B inquiry | 16 | 12 | 75 | 50 | 25 |
| A functions and types | 11 | 71 | 705 | 499 | 121 |
| A extensions | 1 | 22 | 188 | 109 | 58 |
| A DDL families | 221 | 103 | 852 | 300 | 257 |
| C routines and PL/pgSQL | 222 | 101 | 894 | 443 | 420 |
| C protocol, cursors, prepared, COPY | 171 | 60 | 459 | 215 | 216 |
| C DML | 110 | 157 | 1191 | 596 | 281 |
| C triggers, rules, event triggers | 38 | 48 | 373 | 212 | 113 |
| C transactions and locking | 125 | 91 | 691 | 436 | 233 |
| C sessions, settings, TEMP, notify | 91 | 44 | 411 | 122 | 257 |
| C FDW and replication | 69 | 30 | 230 | 120 | 89 |
| C security | 56 | 31 | 265 | 96 | 123 |
| core rest | 493 | 124 | 870 | 641 | 135 |

**Projection.** Core is about 8.9 MiB after phase A (items 208–211), 7.7 after
phase B (212–219), 5.15 after the C cuts (221–227), 4.7 after accessor
retirement (228), and ≈ 3.8 MiB (3.3–4.6) after the core engine test packages
(229). Phases A and B alone cannot reach 4 MiB, because the hub tests stay with
the hub code. Phase C is gated by item 220.

## 2. Method

### 2.1 Phases and cuts

Every commit keeps the build green.
1. **Seam:** add the type, interface, hook or capability first.
2. **Cut in place:** refactor core to use only that seam.
3. **Move:** move the code and its tests, and qualify every reference in the
   same commit, including the root, the driver and `cmd/stuntgres`. No aliases.

Cuts:
- **Leaf host** (below core). The leaf declares a `Host` interface, and core
  implements it on a defined type over `StatementScope` or `Engine`, as
  `statementHost` (`type statementHost StatementScope`) already does. The
  conversion is a free pointer conversion.
  - Every leaf Host that `statementHost` serves shares one method set, so
    method names must not clash.
  - Needs shared by several leaves are embedded capability interfaces, not
    copies.
- **Capability split** (item 213). `exec.Host` (107 methods, consumed only by
  the `exec.Context` forwarders in `context_runtimes.go`) gains accessors that
  return a capability, such as `Contrib() ContribRuntime`. Before the move the
  accessor returns `(*contribHost)(h)`; after it, a pointer to the leaf's state
  embedded in `StatementScope`, re-bound after `StatementScope.Copy`. This
  replaces forwarding methods (rule 2).
  - Never return a struct by value that holds a two-word interface: it
    allocates.
- **Hook** (above core). A fixed interface field on `Engine` set by the
  composition, like `core.Statements` and `Engine.Factory`. Nothing is
  allocated per statement or row.
- **Method to function** (`meth2fn`) and **export in place**: only what moving
  production needs. Never for tests (T6).
- **Small cuts** as before: state ownership, reader interface, value input or
  callback, narrow host method.

### 2.2 Tests

`reference/` is the golden suite: read-only, and it needs no new tests.
- **T1, inventory.** Record per test function:
  - the accessor names it reaches, directly and through helpers;
  - whether it builds an engine;
  - any concurrency, cancellation, allocation, frame-name, synctest,
    Dump/Restore or session-ID hooks;
  - its census owner (T8).
- **T2, duplicates.** Delete a test only if all of these hold:
  - it asserts only consumer-visible outcomes;
  - named reference tests assert the same statement shapes and values or
    SQLSTATEs;
  - it has no concurrency, cancellation, allocation, frame-name or barrier hook,
    and no assertion on internal state.

  Record each deletion in the commit body as
  `x_test.go TestFoo → reference/<pkg>: TestReferenceBar`.
- **T3, destination.** A test goes where its code is, and never outside
  `internal/engine/core/`.
  - A test owned by a moving subsystem moves with it to `core/<x>`. Pure units
    move as internal tests.
  - Any other SQL-driven test goes to `core/enginetests/<area>`. The area is
    named after the code it exercises: a lower package (`functions`,
    `pgcatalog`, `exec`, …), a `*cmds` family, or a core area.
  - Tests that need core-only accessors stay in core.
- **T4, helpers.** Helpers used by two or more destination packages go into
  `internal/engine/enginetest`; other helpers move with their users or are
  copied. Only `_test.go` files import `enginetest`, and internal tests cannot
  (import cycle).
- **T5, removed internals.** A test that pins a removed internal is rewritten to
  the new seam or deleted ("pins removed X" in the commit body). Frame-name
  strings change in the renaming commit: `runtime.Callers` tests match by
  function-name suffix, so a package move is safe but a type or method rename is
  not. Allocation and benchmark tests are never deleted.
- **T6, accessors.**
  - An accessor entry moves with the production it aliases, into that
    package's `export_test.go`.
  - An entry whose target becomes a production export is deleted, and its users
    switch to the exported name.
  - A test needing accessors from two destinations stays with the higher-layer
    one, or is split by test function.
  - No production name is exported for a test; TESTING.md forbids inspection
    exports.
  - Every case is recorded in the item's Status.
- **T7, placement and build cost.** An `export_test.go` makes Go rebuild every
  package between the package and the root as test variants. Measured on
  `functions`: +7.4 s wall and +48 s CPU, against +0.2 s and +6 s for one more
  test-only package.
  - If `core/<x>` needs an `export_test.go`, all its engine tests sit in
    `core/<x>`.
  - Otherwise `core/<x>` keeps only pure units, and its engine tests go to
    `core/<x>/enginetests/`.
  - `core/enginetests/<area>` packages hold only external tests, with a
    one-sentence `doc.go`.
  - Each item reports quickcheck CPU. More than +2% for one new package stops
    the item for a user decision. Item 212 measures the cascade once.
- **T8, census.** Item 206 assigns every core test an owner from its coverage
  (rule and thresholds in [item 206](ENGINE-SPLIT-core.md#item-206--tooling-and-owner-census)).
  - Every move item re-runs it on its candidates; names are never enough.
  - Private-state assertions that `AGENTS.md` requires (prepared-value
    retention) are never rewritten away.
- **T9, test package size.** Every package receiving tests stays under 2 MiB,
  and never reaches 4 MiB.
  - A destination that would pass 2 MiB splits by census area into sibling
    packages, such as `core/enginetests/<area>/<sub>` or a second area.
  - Shared helpers follow T4.

### 2.3 Measuring

The tools live in `.cswd/tmp/` (outside git) and run from a worktree root. All
of them except this round's scripts hard-code the root engine package as the
source; item 206 retargets them to core and nested destinations.
- **This round's scripts**, in `SPLIT-ENGINE-CORE/`:
  - `measure.py OUT`: package sizes, core totals, the accessor census per test
    file, and the frame, allocation and synctest lists;
  - `layout.py OUT/tests.json`: the §1 prefix table;
  - `probe.sh`: the census feasibility probe. The coverage build took 130 s wall
    under load and produced a 130 MB binary. There are 2,810 top-level tests,
    each running in 0.1–0.7 s alone and leaving about 2 MB of coverage data.
- **Round 7 tools:**
  - `PG18-ENGINE-SPLIT-204-COMPOSITION-CLEANUP/`: `cmdmv` (type-checked mover of
    production and tests; writes `export_test.go`, requalifies every package),
    `inv`, `testonly`, `unexp` and `layers.py` (§3 rules 1 and 6 against
    `go list`);
  - `PG18-ENGINE-SPLIT-190-HOST-SLOT-SPLITS/bin/closure` (source in
    `closure-src`): assigns declarations to areas;
  - `PG18-ENGINE-SPLIT-CLOSING-REVIEW-4/`: `check_links.py` and
    `check_linear.py`;
  - `PG18-ENGINE-SPLIT-EXEC-REFINE/reach`: the first path from a declaration to
    a god type.
- **Checks:**
  - `PG18-ENGINE-SPLIT-182-EXPORT-PASS-IN-PLACE/frames.py` (frame-matched
    functions);
  - `PG18-ENGINE-SPLIT-195-CORE-MOVE/lits.py` (string-literal multiset);
  - `PG18-ENGINE-SPLIT-198-INDEXCMDS/escdiff.py` (`-gcflags=-m` heap diff);
  - `SPLIT-REFERENCE-CATALOGS/revcheck.py` and `snap.py` (test names, literal
    multiset, reverse-rename diff).
- **Rewrite tools** (build each with `go build` in its directory):
  - `PG18-ENGINE-SPLIT-PLAN-ALIASES/`: `meth2fn`, `fieldrn`, `unalias`,
    `shadowrn`, `keylit`;
  - `PG18-ENGINE-SPLIT-ANALYZE-QUERY-PREP/`: `frontier`, `expspec`, `movespec`;
  - `PG18-ENGINE-SPLIT-ANALYZE-EXPRESSION-CORE/`: `movex`.

Re-run the census and `closure` before planning a move set; the sizes in §1 and
§4 are approximate.

## 3. Target architecture

| Layer | Package | Owns |
|---|---|---|
| L-1 | `engine/fts` | immutable full-text-search codecs; below `sqltype`, imports only `sqlstate` |
| L-1 | `workadmission` | cooperative request workload-credit admission; standard library only |
| L0 | `parser`, `sqltype`, `sqlstate`, `util` | grammar and AST, SQL types and codecs, shared errors, SQL-independent data structures |
| L1 | `engine/ids` | identity values and the provenance `Domain` |
| L1 | `engine/authorization` | roles, membership and ACL algorithms over immutable values |
| L1 | `engine/largeobject` | immutable large-object bytes and replay shapes |
| L1 | `engine/settings` | setting definitions, parsing, formatting and validation |
| L1 | `engine/proc` | SQL routine definitions, PL/pgSQL programs, the statement walk and invocation frames |
| L1 | `engine/host` | immutable application-provisioned scalar routine contracts and executable tokens |
| L1 | `engine/replication` | pure replication slot and plugin argument validation |
| L2 | `engine/expr` | the expression contract and node protocol |
| L2 | `engine/lock` | the heavyweight lock manager, with its own innermost mutex |
| L3 | `engine/functions` | builtin functions: signatures, nodes, evaluation, aggregates, windows |
| L3 | `engine/catalog` | descriptors, `State`, `Read`, `Store`, the provider registry, the `Candidate` overlay, the builtin seed |
| L3.5 | `engine/dump` | dump graph records, ordering, `Presenter` and `Resolver` capabilities |
| L4 | `engine/storage` | row versions, deletion roots, index roots, sequence state, `Store` |
| L4 | `engine/access` | authorization enforcement on catalog objects (`access.View`), the role store |
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
| L9 | `engine/subscription` | subscription option and connection validation, immutable metadata staging |
| L9.5 | `engine/exec` | the executor runtime and the concrete evaluation context; above `extension` only for the cube spatial call check |
| L10 | `engine/analyze` | the binder, `Describe`, `Binding` and `analyze.Host` |
| L11 | `engine/regobj` | reg* types, type declarations, typed literals, `aclitem` I/O, object identity |
| L11.5 | `engine/stored` | stored-SQL payloads (including triggers), their renderers, retag and remap |
| L12 | `engine/index` | index keys and roots, unique and exclusion enforcement, publication, AM DDL |
| L12 | `engine/table` | columns, constraints, defaults, generated columns, FKs, ALTER TABLE, DROP and VACUUM decisions |
| L12 | `engine/partition` | bounds, routing, key recipes, ATTACH/DETACH/DROP decisions, pruning, `pg_partition_*` |
| L12 | `engine/fdw` | wrappers, registry, validators, foreign writes and transaction participants |
| L12 | `engine/view` | view, rule, policy and trigger decisions, rewriting, updatable views, matview refresh, RLS selection |
| L12 | `engine/role` | GRANT/REVOKE, SET ROLE, DROP and REASSIGN OWNED, default privileges, COMMENT plans |
| L12.5 | `engine/publication` | logical publication validation, command staging and effective membership |
| L13 | `engine/dml` | write executors, unique and exclusion probing, FK enforcement and actions, trigger firing contract |
| L14 | `engine/control` | native Dump and Restore drivers over registered hooks, the Restore comparison context |
| L14.5 | `engine/core/explain` (item 212), `contrib` (214), `tsearch` (215), `stats` (216), `nativetrig` (217), `catalogrt` (218), `inquiry` (219); gated: `advisory`, `notify` (225) | leaves below core: EXPLAIN rendering, contrib function runtimes, the text-search runtime, statistics, native triggers, the `pgcatalog.Runtime` implementation, inquiry functions, advisory-lock and notification state; each over its own `Host` |
| L15 | `engine/core` | `Engine` state and `Engine.mu`, `StatementScope`, `CatalogStage`, admission and lock waits, publication and replacement, the transaction lifecycle, sessions, TEMP, the hosts and binder construction, protocol, prepared SQL, cursors, the bound DML and query path, the procedural runtime, the dispatch and factory hooks |
| L15.5 | gated: `engine/core/plpgsql` (221), `portalcmds`, `preparecmds`, `copycmds` (222), `eventcmds` (224), `variablecmds` (226), `foreigncmds`, `publicationcmds`, `subscriptioncmds` (227) | handlers above core: the PL/pgSQL language runtime behind a hook; statement executors reached through `Statements` dispatch |
| L16 | `engine/tablecmds`, `indexcmds`, `partitioncmds`, `typecmds`, `viewcmds`, `rolecmds`, `schemacmds` | statement executors per family, named after PostgreSQL's `commands/` |
| L17 | `engine` (root) | the composition: `New*`, both dispatch type switches behind `core.Statements`, the engine factory, `Reset`, `Dump` and `Restore` with the Restore hooks |
| test | `engine/enginetest`, `*/enginetests`, `engine/core/enginetests/<area>` (items 208–211, 229), `engine/core/<x>/enginetests` | test helpers over the exported API, imported only by `_test.go` files; test-only packages holding engine tests |

Each package README states its imports, ownership, invariants and what stays in
core.

### Dependency rules

1. **Imports point downward only.** Production packages never import the root;
   only `enginetest` and `_test.go` files may. Same-layer packages import each
   other only under rule 6. Check with
   `go list -f '{{.ImportPath}}: {{join .Imports " "}}' ./internal/engine/...`
   (`layers.py`, by full import path).
2. **The root is the composition layer.** The driver imports `core` for types
   and runtime methods, and the root for `New*`, `Reset`, `Dump` and `Restore`.
   No aliases and no forwarding facade.
3. **One lock authority.** `Engine.mu`, in core, is the single mutex for
   catalog, storage and visibility state, so that publication stays atomic.
   - Subsystem structs have no mutex. Code under it is a `…Locked` function that
     takes no lock.
   - Registered `…Locked` handlers and validators are lock-domain
     continuations: they are entered with `Engine.mu` held and release it on
     every return. Every other hook runs with nothing held.
   - The lock manager's mutex is innermost (lock README); the session mutex
     order is in the session README.
4. **Decision packages own data and decisions; core owns the lifecycle; families
   own statement orchestration.** Admission, lock waits, publication,
   cancellation fences, binder construction and dispatch re-entry (nested SQL)
   are core functions that leaves and families call.
5. **`functions` stays a leaf library** over `expr`, `ids`, `sqltype`, `parser`
   and `sqlstate`. Engine state reaches it through capabilities the evaluation
   context implements.
6. **Feature arrows.** Each arrow points from the imported package to its
   importer.
   - Lower: `typedef` → `extension` → `routine`; `sequence` stands alone. `exec`
     may import `extension`, and `analyze` all of them; they never import `exec`
     or `analyze`.
   - Upper: `index` → `table` → `partition`; `table` → `fdw`; `table` → `view`;
     `index` → `partition`; `index` → `view`; `role` stands alone.
   - `dml` may import every upper feature, and `control` every upper feature
     except `dml`.
   - Upper features may import `analyze` types and pure functions, `regobj` and
     `stored` (`stored` imports `regobj`), but never construct a binder.
   - `analyze`, `exec`, `session`, `pgcatalog` and the lower features never
     import an upper feature, `regobj` or `stored`. `session` never imports
     `exec`, `analyze` or a feature package.
   - Core leaves: `tsearch` → `nativetrig`; `catalogrt` → `inquiry`; the others
     stand alone.
   - Families: `tablecmds` → every other family except `rolecmds`; `indexcmds` →
     `partitioncmds`; `typecmds` → `viewcmds`; `rolecmds` stands alone.
     - Code shared by families with no arrow between them goes to core.
     - Core never imports a family.
7. **Lower layers never name upper-layer types.** They hold an interface or an
   opaque payload, and the owner recovers the concrete type with one checked
   helper.
8. **Registration replaces central switches.** Code that lists every feature
   becomes an ordered table the root composes in today's order, so error
   precedence and output order stay byte-identical.
9. **Leaves below core** (L14.5):
   - They import only packages below core and other leaves along rule-6
     arrows. They never import core, an L15.5 package, a family or the root.
   - They hold no lock. Releasing and re-acquiring `Engine.mu` around callbacks
     is a core host method.
   - Code entered with `Engine.mu` held is a `…Locked` function.
10. **Packages above core** (L15.5):
    - They import core, and are reached only through a fixed hook or
      `Statements` dispatch that the root sets. Core and the leaves never
      import them.
    - Families may import them along new rule-6 arrows that their items list.

## 4. Eighth round

Rules:
- One session per item. New packages only as listed in §3.
- Cuts follow §2.1 and tests follow §2.2. Each item names the hosts, hooks,
  capabilities and exported names it adds. No aliases.
- Behaviour, error text, error order, notice order, lock order and Dump output
  stay identical.
- Items on startup or on a per-statement or per-row path run `make bench` before
  and after, and add no allocations.
  - The 21 allocation-budget test files and the core benchmarks must keep
    passing: `insert_conflict*`, `plpgsql_walk`, `transaction_remedy` and
    `update_allocation`.
- **Modes.**
  - P items run in parallel with each other and with feature sessions; their
    generated moves are still replay scripts.
  - S items rewrite hundreds of files: they are generated by a replay script
    and merged in a freeze window (§5.2).
- Every item reports the sizes of core and of each package it creates or grows,
  and quickcheck CPU (§5.3).

Specs are in [ENGINE-SPLIT-core.md](ENGINE-SPLIT-core.md); items 220 and 230 are
below. "Core after" is the §1 projection in MiB; the census replaces it.

| Item | Mode | Moves | Core after ≈ | Needs |
|---|---|---|---:|---|
| 206 | P | [tooling and owner census](ENGINE-SPLIT-core.md#item-206--tooling-and-owner-census) | 11.2 | — |
| 207 | P | [test seams in place](ENGINE-SPLIT-core.md#item-207--test-seams-in-place) | 11.2 | 206 |
| 208 | P | [builtin function, type and extension tests](ENGINE-SPLIT-core.md#item-208--builtin-function-type-and-extension-tests) | 10.4 | 207 |
| 209 | P | [DDL family tests](ENGINE-SPLIT-core.md#item-209--ddl-family-tests) | 9.9 | 207 |
| 210 | P | [catalog producer tests](ENGINE-SPLIT-core.md#item-210--catalog-producer-tests) | 9.4 | 207 |
| 211 | P | [query and DML feature tests](ENGINE-SPLIT-core.md#item-211--query-and-dml-feature-tests) | 8.9 | 207 |
| 212 | P, bench | [explain pilot](ENGINE-SPLIT-core.md#item-212--explain-pilot) | 8.75 | 206 |
| 213 | P, bench | [host capability seam](ENGINE-SPLIT-core.md#item-213--host-capability-seam) | 8.75 | — |
| 214 | P, bench | [contrib runtimes](ENGINE-SPLIT-core.md#item-214--contrib-runtimes) | 8.55 | 207, 212, 213 |
| 215 | P, bench | [text-search runtime](ENGINE-SPLIT-core.md#item-215--text-search-runtime) | 8.4 | 207, 212, 213 |
| 216 | P, bench | [statistics runtime](ENGINE-SPLIT-core.md#item-216--statistics-runtime) | 8.2 | 207, 212 |
| 217 | P, bench | [native triggers](ENGINE-SPLIT-core.md#item-217--native-triggers) | 8.15 | 215 |
| 218 | P, bench | [catalog runtime](ENGINE-SPLIT-core.md#item-218--catalog-runtime) | 7.8 | 210, 212 |
| 219 | P, bench | [inquiry runtime](ENGINE-SPLIT-core.md#item-219--inquiry-runtime) | 7.7 | 213, 218 |
| 220 | — | [closing review 6](#item-220--closing-review-6) | — | 207–219 |
| 221 | S, bench | [PL/pgSQL above core](ENGINE-SPLIT-core.md#item-221--plpgsql-above-core) | 7.1 | 220 |
| 222 | S, bench | [cursor, prepare and COPY commands](ENGINE-SPLIT-core.md#item-222--cursor-prepare-and-copy-commands) | 6.6 | 220 |
| 223 | S, bench | [DML statement assembly](ENGINE-SPLIT-core.md#item-223--dml-statement-assembly) | 5.9 | 220 |
| 224 | P, bench | [event triggers](ENGINE-SPLIT-core.md#item-224--event-triggers) | 5.75 | 220 |
| 225 | P, bench | [advisory locks and notifications](ENGINE-SPLIT-core.md#item-225--advisory-locks-and-notifications) | 5.5 | 220 |
| 226 | P, bench | [settings commands](ENGINE-SPLIT-core.md#item-226--settings-commands) | 5.35 | 220 |
| 227 | P | [foreign-data and replication commands](ENGINE-SPLIT-core.md#item-227--foreign-data-and-replication-commands) | 5.15 | 220 |
| 228 | P | [hub accessor retirement](ENGINE-SPLIT-core.md#item-228--hub-accessor-retirement) | 4.7 | 221–227 |
| 229 | S | [core engine test packages](ENGINE-SPLIT-core.md#item-229--core-engine-test-packages) | ≈ 3.8 | 228 |
| 230 | — | [closing review 7](#item-230--closing-review-7) | — | 229 |

**Gating.** Phase C (items 221–229) starts only after item 220 and with the
user's approval. Item 220 may confirm, trim or replace those items, and leftovers
become new items at the end.

### Item 220 — closing review 6

Docs only, after phases A and B.
1. Re-run the census and `closure`, measure every package, and record
   quickcheck CPU and wall time against item 206's baseline.
2. Rewrite §1.
3. For each item 221–229, confirm the host or hook sketch against `closure`.
   Re-estimate what leaves core, and drop items that free less than 0.1 MiB.
4. Ask the user before phase C starts.

### Item 230 — closing review 7

Docs only, after item 229.
- Core must be under 4 MiB, and every package that received tests must be under
  2 MiB. Re-measure both.
- Anything over a limit becomes a new item, and so does any split of an
  existing test package that grew past 2 MiB.
- If both limits hold, close the split and reset these docs to the layering
  contract (§3).

### Not planned

- **A `prepared` value package.** It would mean 202 requalified sites for about
  0.03 MiB of test relief.
- **`xml_map_rows` below core.** It reaches 12 portal and 7 cursor internals;
  item 222 revisits it.
- **`AuthorizationView` to `access`.** It names `session`, `analyze`, `view`,
  `dml`, `plan` and `stored` types; leaves take `access.View` instead.
- **A forwarding facade for the driver.** The driver names core types.
- **Moving the SQL routine runtime, scope and admission, or publication out of
  core** (rule 4).
- **Migrating engine-fixture tests to `reference/`.** That is TESTING.md work;
  this round only moves, adapts or deletes them.

## 5. Sessions

Start a session with "Implement Item N from Linear Plan in
`docs/ENGINE-SPLIT-STATUS.md`". The item's section is the scope.

### 5.1 Protocol

1. **Read** `AGENTS.md`, `docs/TESTING.md`, §2–§5, the item's section and the
   READMEs of the packages it touches.
2. **Check needs** against `git log` and the status page; if one is missing,
   stop and report.
3. **Re-measure** the move set with the census and `closure`. Present a
   file-level plan:
   - moved declarations and tests, and new names;
   - hosts, hooks and capabilities;
   - call sites;
   - accessor handling (T6);
   - destination sizes (T9);
   - the commit sequence, risks and docs.
4. **Implement** in small commits; each builds and passes its targeted tests.
5. **Verify** (§5.3), then document and report (§5.4).

### 5.2 Constraints

- **Pure refactor:** no behaviour, SQL-surface or dependency change.
- **Read-only suites:** `reference/` and `smoke/`; never run `make refcheck`.
- **Scope.** Record findings for later items in the prompt file's Status
  section. New core code or tests from feature sessions are findings for the
  item that owns their area.
- **Feature inflow.** Feature sessions keep adding tests to core. Each move item
  re-runs the census on files added since the census commit.
- **Generated moves (all modes).**
  - Generate the move commits with a replay script in `.cswd/tmp/<NAME>/`.
  - On a conflict, reset to the new master and replay; do not hand-resolve tool
    output.
  - Rewrites cover `./internal/engine/...`, the root package, the driver and
    `cmd`.
- **Mode S.** Ask the user for a merge freeze before the final quickcheck, then
  chain `make quickcheck && <checks> && git merge --ff-only`.
- **Git.** Work in `.cswd/work/<NAME>` on `implement-part/<NAME>`, with
  temporary files in `.cswd/tmp/<NAME>/`. Record the master commit at the start
  and at every rebase. Rebase before verifying; merge `--ff-only` only after
  quickcheck passed on the rebased branch.

### 5.3 Verification

Commands and deadlines are in `AGENTS.md`.
- **While working:**
  - after every commit, `make build` and
    `go test -count=1 -run '^$' ./internal/engine/... .`;
  - targeted `make check RUN='...'` for every package that gained tests, with
    `RACE=1` for locks, sessions, cancellation and publication.
- **Moves:**
  - `revcheck.py`: the test-name multiset is unchanged against the base, and so
    is the string-literal multiset, except frame strings (T5);
  - `layers.py` against §3, rules 1, 6, 9 and 10;
  - every destination under 2 MiB (T9).
- **Before merging:** `make quickcheck`, then `make check REFERENCE=1 RUN='...'`
  once for the item's selections (production items only). Without Docker,
  report it as blocked.
- **Quickcheck CPU:** report total test CPU and the elapsed time of core and
  every new package (`go test -json`) before and after.
  - Load swings timings 2–3x, so compare interleaved runs or use an idle host
    (`remote-run` skill).
  - More than +2% CPU for one new package stops the item (T7).
- **Bench:** on startup, per-statement or per-row paths, run `make bench`
  before and after, report ns/op and allocations, and diff heap escapes with
  `escdiff.py`.
- **Deps:** `make deps` lists only this module's paths.
- **Skipped:** `make racecheck` and `make finalcheck` until the race suite is
  fixed (user decision); report them as skipped. Report unrelated failures; do
  not work around them.

### 5.4 Documentation and report

**Documentation:**
- **Package README.** Each new package gets one: contract, layering, `Host` or
  hook, lock rules, and `## Testing` with one selector per test family. Move
  the matching core README paragraphs with the code (`readme-map.tsv`, item
  207); the core README is 201 KB.
- **Prompt file:** replace the item's "not started" Status line with its
  commits, measurements, deviations and notes for later items.
- **Status page:** follow its rules.
- **This file:** change it only when the plan changes. `AGENTS.md` must not grow.

**Report:**
- what moved, and the hosts, hooks, capabilities and exported names added;
- the commits;
- verification, including skipped or blocked checks;
- tests moved, adapted or deleted, with the covering reference tests;
- core and destination sizes before and after, and quickcheck CPU;
- doc changes;
- deviations and follow-ups;
- the next ready items.
