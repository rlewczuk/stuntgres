# Splitting `internal/engine`

Status: six rounds and four closing reviews (linear items 1–180) are done.
Item 180 measured the root at `26f8fea15` and planned the seventh round (§4,
items 181–205): a runtime core. The god structs move down into `engine/core`,
and statement executors move into family packages above it. Earlier analysis,
specs and session records are in git history
(`git show 26f8fea15:docs/ENGINE-SPLIT.md`, and the same for the status page and
the round-6 prompt files). `docs/design/*` is background only; where it
disagrees with this document, this document wins.

## 1. Where the split stands

Measured at `26f8fea15` with the tools in §2.3. Lines are declaration lines
unless a column says otherwise.

- **Size.** The root has 395 production files: 48.5k lines (1.74 MB), 15.8% of
  the engine's 307k production lines. The 36 subpackages hold 258.7k. The root
  also holds 1,346 test files: 259.8k lines (10.65 MB). That is 86% of the
  root's bytes and 85% of the engine's test bytes.
- **Why nothing more moves under the old rules.** 312 of 1,836 root
  declarations (2.9k lines) are clean; everything else reaches a god struct:
  - god structs: `Engine` (454 methods in 215 files, 20.4k lines),
    `StatementScope` (356 in 133, 11.4k) and `CatalogStage` (38, 0.3k);
  - adapters: `statementHost` 145 methods, `ddlHost` 89, `dropProvider` 58,
    `bindingHost` 52, `engineCatalogRuntime` 44, `AuthorizationView` 30.

  No package can import the root.
- **Runtime-core boundary** (`closure`):
  - core 28.5k;
  - families 9.1k, plus 1.6k shared;
  - root composition (Restore) 1.2k;
  - unassigned 0.1k.

  Two cuts shrink core to about 26k, about the size of `analyze`: the `ddlHost`
  split (item 190) moves 1.2k to the families, and the validator table (item 189)
  about 1.1k.
- **Tests.**
  - 63 files (0.26 MB) reach no root internals, even through helpers.
  - Exporting the 345 names the families need anyway, plus 25 inspection names
    (item 182), frees 4.8 MB.
  - 2.7 MB reach family or Restore code and move with their family; 2.9 MB need
    528 test-only core internals. Of the family tests, 1.5 MB need 175 of those
    internals as well, so they must be fixed before the core move (item 194).
  - Few tests are pure duplicates of `reference/`. 0.9 MB of files have at least
    80% of their SQL literals in `reference/`, but the samples also assert
    internal state; expect 5–15% deletions.
- **Churn.** About 100 commits a day touch root files, and feature sessions
  change 400–800 root test files a day.

| Area (root files) | File lines | Tests MB | Destination |
|---|---:|---:|---|
| Statement dispatch, protocol, portals, cursors, hosts (29) | 6.3k | 0.24 | core |
| Transactions, sessions, TEMP, notifications (28) | 5.0k | 0.57 | core |
| Tables, FK runtime (40) | 4.0k | 0.75 | `tablecmds` 1.7k, core 1.3k |
| Types, domains (29) | 3.3k | 0.48 | `typecmds` 1.1k, core 1.5k |
| DML, COPY, TRUNCATE, command effects (19) | 3.1k | 0.77 | core |
| Views, rules, policies, RLS, matviews (25) | 3.0k | 0.48 | `viewcmds` 1.0k, core 1.5k |
| Roles, ACL, databases (27) | 2.6k | 0.55 | `rolecmds` 1.1k, core 0.9k |
| Schema DDL, DROP, renames, definitions (17) | 2.5k | 0.28 | `schemacmds` 0.8k, core 1.1k |
| Catalog stage, publication, replacement (9) | 2.4k | 0.06 | core |
| PL/pgSQL (13) | 2.0k | 0.18 | core |
| Indexes (27) | 1.8k | 0.34 | `indexcmds` 0.45k, core 0.9k |
| Dump/Restore (15) | 1.5k | 0.02 | root 1.0k |
| Inheritance, partitions (31) | 2.9k | 0.50 | `partitioncmds` 0.75k, `tablecmds` 0.76k, core 0.65k |
| Sequences, identity, VACUUM (13) | 1.6k | 0.30 | `tablecmds` 1.0k, core 0.33k |
| Function glue, query admission, catalog runtime (28) | 2.9k | 1.60 | core |
| SQL routines, FDW, extensions, reg*, triggers, others (45) | 3.4k | 0.73 | `schemacmds` 0.7k, core 1.9k, root 0.1k |
| Test-only prefixes (384 test files) | — | 2.79 | `functions`/`sqltype` 1.65, extensions 0.32, others by owner 0.83 |

## 2. Method

### 2.1 Phases and cuts

Every commit keeps the build green.
1. **Seam:** add the type, interface or hook first.
2. **Cut in place:** refactor the root to use only that seam.
3. **Move:** move the code and its tests, and qualify every reference in the
   same commit, including the driver and `cmd/stuntgres`. No aliases.

Cuts:
- **Export in place** (item 182): export the members that code outside the
  future core needs, under their final names, before anything moves.
- **Method to function** (items 191–192): Go keeps methods with their type, so a
  family method on a god struct first becomes a receiver-first root function
  (`meth2fn`).
- **Hook** (item 189): a core→family edge that is not dispatch becomes a fixed
  interface field on `Engine`, set by the composition. That is an ordered
  validator table or a `core.Statements` method; nothing is allocated per
  statement or row.
- **Host split** (item 190): an adapter serving several families (`ddlHost`
  implements nine host interfaces) becomes a core base plus small family hosts.
- **Small cuts** as before: state ownership, capability, reader interface,
  value input or callback, narrow host method.

### 2.2 Tests

`reference/` is the golden suite: read-only, and it needs no new tests.
- **T1, inventory.** Per test function: the unexported root names it reaches
  through helpers, whether it builds an engine, and any concurrency,
  allocation, frame-name or Dump/Restore hooks.
- **T2, duplicates.** Delete a test only if all of these hold:
  - it asserts only consumer-visible outcomes;
  - named reference tests assert the same statement shapes and values or
    SQLSTATEs;
  - it has no concurrency, cancellation, allocation, frame-name or barrier hook,
    and no assertion on internal state.

  Record each deletion in the commit body as
  `x_test.go TestFoo → reference/<pkg>: TestReferenceBar`.
- **T3, destination.** A test goes to the owner of the code it exercises: the
  area of its file prefix, overridden by the owners of the unexported names it
  reaches.
  - Pure units move with their code as internal tests.
  - Tests that build an engine become external tests (`package x_test`) in the
    owner's directory, which holds only a `doc.go` until the owner exists.
  - They use root names until item 195, and core names after it.
  - Before the core move, test-only internals go through accessor files
    (`inspect_*_test.go`, items 193–194). These become core's
    `export_test.go`, and their users move to `core/` as `core_test` in the move
    commit.
  - A test that needs both family code and a test-only core internal is
    rewritten to the exported API, or deleted under T2. As a last resort it
    uses a listed exported inspection accessor in `core/inspect.go`, which item
    204 removes once unused.
  - Item 181 measures the rebuild cost of an external engine test in a low
    package (`functions`) and fixes the fallback: a test-only subdirectory of
    the owner, or `core/`.
- **T4, helpers.** Helpers used by two or more destinations go into
  `internal/engine/enginetest` (stage 1 in item 181, stage 2 in item 182); other
  helpers move with their users. Only `_test.go` files import `enginetest`.
- **T5, removed internals.** A test that pins a removed internal is rewritten to
  the new seam or deleted ("pins removed X" in the commit body). Frame-name
  strings change in the renaming commit. Allocation and benchmark tests are
  never deleted.

### 2.3 Measuring

Tools live in `.cswd/tmp/` (outside git) and run from a worktree root.
- `PG18-ENGINE-SPLIT-CLOSING-REVIEW-4/closure` (go/types) assigns every root
  production declaration to core, a family or the root, and buckets every root
  test file. Run it as `closure -dir . -areas areas.json -out DIR`; add
  `-nodyn ddlHost` to preview a host split.
  - Outputs: `assign.tsv` (each core declaration with its referrer path),
    `summary.txt` (totals, family code pulled into core and its referrer, family
    edges), `exportset.tsv`, `tests.tsv` and `testrefs.tsv`.
  - Helpers next to it: `testarea.py` and `waves.py` (test areas and wave sizes),
    `refcover.py` (duplicate share), `measure.py`, `areas.json`,
    `check_links.py` and `check_linear.py`. `r2/` holds the outputs at
    `26f8fea15`.
- `PG18-ENGINE-SPLIT-EXEC-REFINE/reach` prints the first path from a declaration
  to a god struct.
- Rewrite tools (build each with `go build` in its directory):
  - `PG18-ENGINE-SPLIT-PLAN-ALIASES/`: `meth2fn`, `fieldrn`, `unalias`,
    `shadowrn`, `keylit`;
  - `PG18-ENGINE-SPLIT-ANALYZE-QUERY-PREP/`: `frontier`, `expspec`, `movespec`;
  - `PG18-ENGINE-SPLIT-ANALYZE-EXPRESSION-CORE/`: `movex`.

Re-run `closure` before planning a move set; the sizes in §4 are approximate.

## 3. Target architecture

| Layer | Package | Owns |
|---|---|---|
| L-1 | `engine/fts` | immutable full-text-search codecs; below `sqltype`, imports only `sqlstate` |
| L0 | `parser`, `sqltype`, `sqlstate`, `util` | grammar and AST, SQL types and codecs, shared errors, SQL-independent data structures |
| L1 | `engine/ids` | identity values and the provenance `Domain` |
| L1 | `engine/authorization` | roles, membership and ACL algorithms over immutable values |
| L1 | `engine/largeobject` | immutable large-object bytes and replay shapes |
| L1 | `engine/settings` | setting definitions, parsing, formatting and validation |
| L1 | `engine/proc` | SQL routine definitions, PL/pgSQL programs, the statement walk and invocation frames |
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
| L13 | `engine/dml` | write executors, unique and exclusion probing, FK enforcement and actions, trigger firing contract |
| L14 | `engine/control` | native Dump and Restore drivers over registered hooks, the Restore comparison context |
| L15 | `engine/core` (item 195) | `Engine` state and `Engine.mu`, `StatementScope`, `CatalogStage`, admission and lock waits, publication and replacement, the transaction lifecycle, sessions, TEMP, the hosts and binder construction, protocol, prepared SQL, cursors, the bound DML and query path, the procedural runtime, the dispatch and validator hooks |
| L16 | `engine/tablecmds`, `indexcmds`, `partitioncmds`, `typecmds`, `viewcmds`, `rolecmds`, `schemacmds` (items 196–203) | statement executors per family, named after PostgreSQL's `commands/` |
| L17 | `engine` (root) | the composition: `New*`, both dispatch type switches behind `core.Statements`, the engine factory, `Reset`, `Dump` and `Restore` with the Restore hooks |
| test | `engine/enginetest` (item 181) | test helpers over the exported API; only `_test.go` files import it |

Until item 195 the root plays the core role, and until each family item it keeps
that family's executors. Each package README states its imports, ownership,
invariants and what stays in the root.

### Dependency rules

1. **Imports point downward only.** Production packages never import the root;
   only `enginetest` and `_test.go` files may. Same-layer packages import each
   other only under rule 6. Check with
   `go list -f '{{.ImportPath}}: {{join .Imports " "}}' ./internal/engine/...`.
2. **The root is the composition layer.** After item 195 the driver imports
   `core` for types and runtime methods, and the root for `New*`, `Reset`,
   `Dump` and `Restore`. No aliases and no forwarding facade.
3. **One lock authority.** `Engine.mu`, in core, is the single mutex for
   catalog, storage and visibility state, so that publication stays atomic.
   - Subsystem structs have no mutex. Code under it is a `…Locked` function that
     takes no lock.
   - Registered `…Locked` handlers and validators are lock-domain
     continuations: they are entered with `Engine.mu` held and release it on
     every return, as today. Every other hook runs with nothing held.
   - The lock manager's mutex is innermost (lock README); the session mutex
     order is in the session README.
4. **Decision packages own data and decisions; core owns the lifecycle; families
   own statement orchestration.** Admission, lock waits, publication,
   cancellation fences, binder construction and dispatch re-entry are core
   functions that families call.
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

## 4. Seventh round

Rules:
- One session per item. New packages only as listed in §3.
- Cuts follow §2.1 and tests follow §2.2. Each item names the hooks, host
  methods and exported names it adds. No aliases.
- Behaviour, error text, error order, notice order, lock order and Dump output
  stay identical.
- Items on startup or on a per-statement or per-row path run `make bench` before
  and after, and add no allocations. The 25 allocation-budget test files and the
  `insert_conflict*`, `plpgsql_walk`, `transaction_remedy` and
  `update_allocation` benchmarks keep passing.
- **Modes.** P items run in parallel with each other and with feature sessions.
  S items rewrite hundreds of files: they are generated by a replay script and
  merged in a freeze window (§5.2).

Specs are in the linked prompt files; items 180 and 205 are below. "Out" is MB of
files leaving the root, moved or deleted.

| Item | Mode | Moves | Out ≈ | Needs |
|---|---|---|---:|---|
| 181 | P | [test fixture and pilot](ENGINE-SPLIT-enginetest.md#item-181--test-fixture-and-pilot) | 0.3 | 180 |
| 182 | S | [export pass in place](ENGINE-SPLIT-core.md#item-182--export-pass-in-place) | 0 | 181 |
| 183 | P | [builtin function, operator and type tests](ENGINE-SPLIT-enginetest.md#item-183--builtin-function-operator-and-type-tests) | 1.25 | 182 |
| 184 | P | [query, DML, COPY, cursor and protocol tests](ENGINE-SPLIT-enginetest.md#item-184--query-dml-copy-cursor-and-protocol-tests) | 1.05 | 182 |
| 185 | P | [transaction, session, catalog and procedural tests](ENGINE-SPLIT-enginetest.md#item-185--transaction-session-catalog-and-procedural-tests) | 0.8 | 182 |
| 186 | P | [table, index, partition and sequence DDL tests](ENGINE-SPLIT-enginetest.md#item-186--table-index-partition-and-sequence-ddl-tests) | 0.95 | 182 |
| 187 | P | [type, view, role and schema-object DDL tests](ENGINE-SPLIT-enginetest.md#item-187--type-view-role-and-schema-object-ddl-tests) | 0.8 | 182 |
| 188 | P | [composition seams](ENGINE-SPLIT-core.md#item-188--composition-seams) | 0 | 182 |
| 189 | P | [core hooks](ENGINE-SPLIT-core.md#item-189--core-hooks) | 0 | 188 |
| 190 | P | [host and slot splits](ENGINE-SPLIT-core.md#item-190--host-and-slot-splits) | 0 | 189 |
| 191 | P | [family methods to functions I](ENGINE-SPLIT-core.md#item-191--family-methods-to-functions-i) | 0 | 183–187, 190 |
| 192 | P | [family methods to functions II](ENGINE-SPLIT-core.md#item-192--family-methods-to-functions-ii) | 0 | 191 |
| 193 | P | [test accessors I: runtime areas](ENGINE-SPLIT-enginetest.md#item-193--test-accessors-i-runtime-areas) | 0.1 | 192 |
| 194 | P | [test accessors II: DDL areas and family tests](ENGINE-SPLIT-enginetest.md#item-194--test-accessors-ii-ddl-areas-and-family-tests) | 0.1 | 193 |
| 195 | S | [core move](ENGINE-SPLIT-core.md#item-195--core-move) | 3.7 | 194 |
| 196 | S | [`tablecmds`: tables](ENGINE-SPLIT-cmds.md#item-196--tablecmds-tables) | 0.35 | 195 |
| 197 | S | [`tablecmds`: sequences, identity, VACUUM](ENGINE-SPLIT-cmds.md#item-197--tablecmds-sequences-identity-vacuum) | 0.1 | 196 |
| 198 | S | [`indexcmds`](ENGINE-SPLIT-cmds.md#item-198--indexcmds) | 0.05 | 197 |
| 199 | S | [`partitioncmds`](ENGINE-SPLIT-cmds.md#item-199--partitioncmds) | 0.15 | 198 |
| 200 | S | [`typecmds`](ENGINE-SPLIT-cmds.md#item-200--typecmds) | 0.1 | 199 |
| 201 | S | [`viewcmds`](ENGINE-SPLIT-cmds.md#item-201--viewcmds) | 0.15 | 200 |
| 202 | S | [`rolecmds`](ENGINE-SPLIT-cmds.md#item-202--rolecmds) | 0.2 | 201 |
| 203 | S | [`schemacmds`](ENGINE-SPLIT-cmds.md#item-203--schemacmds) | 1.65 | 202 |
| 204 | S | [composition cleanup](ENGINE-SPLIT-core.md#item-204--composition-cleanup) | 0.2 | 203 |
| 205 | — | [closing review 5](#item-205--closing-review-5) | — | 204 |

**Where 4 MB is crossed.**
- The pilot moves 0.26 MB, and the five test waves (183–187) move or delete
  4.8 MB. The total passes 4 MB with the fourth or fifth wave to merge.
- The core move (item 195) adds about 3.7 MB with its tests, and the families
  about 2.8 MB. Most of the families' share is extension-provider tests, which
  stage extensions directly and so move with `schemacmds`.
- The root keeps the composition (about 1.2k lines) and its own tests.

### Item 180 — closing review 4

Done: re-measured the root, built `closure`, planned this round and reset these
docs.

### Item 205 — closing review 5

Docs only, after item 204. Re-measure with `closure` and rewrite §1. Then decide
whether an eighth round is worth planning, for example splitting core along its
protocol, cursor or procedural-runtime seams; if not, close the split. Keep these
docs at this size.

### Not planned

- **Splitting core in this round.** The method-set rule applies inside core too.
- **`AuthorizationView` to `access`.** Blocked by layering: it names `session`,
  `analyze`, `view`, `dml`, `plan` and `stored` types. It moves with core.
- **A forwarding facade for the driver.** It would take about 60 forwarding
  methods and risk a per-statement allocation; the driver names core types.
- **Migrating engine-fixture tests to `reference/`.** That is TESTING.md work.
  This round only moves, adapts or deletes them.

## 5. Sessions

Start a session with "Implement Item N from Linear Plan in
`docs/ENGINE-SPLIT-STATUS.md`". The item's section is the scope.

### 5.1 Protocol

1. **Read** `AGENTS.md`, `docs/TESTING.md`, §2–§5, the item's section and the
   READMEs of the packages it touches.
2. **Check needs** against `git log` and the status page; if one is missing,
   stop and report.
3. **Re-measure** the move set with `closure`, and present a file-level plan:
   moved declarations, new names, hooks and host methods, call sites, tests
   moved or deleted, the commit sequence, risks and docs.
4. **Implement** in small commits; each builds and passes its targeted tests.
5. **Verify** (§5.3), **document** (§5.4) and **report** (§5.5).

### 5.2 Constraints

- **Pure refactor:** no behaviour, SQL-surface or dependency change.
- **Read-only suites:** `reference/` and `smoke/`; never run `make refcheck`.
- **Scope.** Record findings for later items in the prompt file's Status
  section. New root code or tests from feature sessions are findings for the
  item that owns their area.
- **Feature inflow.** Feature work puts decisions in the owning package. After
  item 181 it puts engine-fixture tests beside their owner through
  `enginetest`, never in the root (TESTING.md).
- **Mode S.**
  - Generate the commits with a replay script in `.cswd/tmp/<NAME>/`. On a
    conflict, reset to the new master and replay; do not hand-resolve tool
    output.
  - Ask the user for a merge freeze before the final quickcheck, then chain
    `make quickcheck && <checks> && git merge --ff-only`.
  - Rewrites cover `./internal/engine/...`, the driver and `cmd`.
- **Git.** Work in `.cswd/work/<NAME>` on `implement-part/<NAME>`, with
  temporary files in `.cswd/tmp/<NAME>/`. Record the master commit at the start
  and at every rebase. Rebase before verifying; merge `--ff-only` only after
  quickcheck passed on the rebased branch.

### 5.3 Verification

Commands and deadlines are in `AGENTS.md`.
- **While working:** `make build` and `go test -run '^$' ./internal/engine/...`
  after every commit; targeted `make check RUN='...'`, with `RACE=1` for locks,
  sessions, cancellation and publication.
- **Before merging:** `make quickcheck`, then `make check REFERENCE=1 RUN='...'`
  once for the item's few selections. Without Docker, report it as blocked.
- **Bench:** on startup, per-statement or per-row paths, run `make bench`
  before and after and report ns/op and allocations.
- **Deps:** `make deps` lists only this module's paths.
- **Skipped:** `make racecheck` and `make finalcheck` until the race suite is
  fixed (user decision); report them as skipped. Report unrelated failures; do
  not work around them.

### 5.4 Documentation and report

**Documentation:**
- **Package README:** contract and `## Testing` selectors. Move the matching
  root README paragraphs with the code; the root README (50 KB) mostly becomes
  the core README at item 195.
- **Prompt file:** replace the item's "not started" Status line with its
  commits, deviations and notes for later items.
- **Status page:** follow its rules.
- **This file:** change it only when the plan changes. `AGENTS.md` must not grow.

**Report:**
- what moved, and the seams, hooks and exported names added;
- the commits;
- verification, including skipped or blocked checks;
- tests moved, adapted or deleted, with the covering reference tests;
- the root bytes before and after;
- doc changes;
- deviations and follow-ups;
- the next ready items.
