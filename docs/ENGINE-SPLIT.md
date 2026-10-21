# Splitting `internal/engine` — analysis and first steps

Status: approved; implementation runs one session per sub-step (§8). Steps
1–17 are done: the first steps (§5) and the first ten later-wave packages
(§6, steps 8–17). §6 also plans steps 18–30, the postponed packages and the
seams they need, in the same detail. Every step is still refined with
file-level detail at the start of its session.

The analysis of §2.1–§2.5 is based on the production code at commit
`a2252c0b1`; §2.6 and steps 8–17 were measured at `75fe0d710`; §2.7 and
steps 18–30 at `c154e14fb`. None of it relies on the design notes in
`docs/design/`. Those notes are background only: where they disagree with a
clearer split, this document takes precedence. Update them after the step
they describe lands.

The ownership record of the catalog inquiry and session identity nodes
(Group A item 7) is in the [functions prompt file](ENGINE-SPLIT-functions.md)
Status section ("Item7 catalog/session ownership record").


## 1. Summary

`internal/engine` is one Go package: 736 production files and about 188k
lines, plus about 199k lines of unit tests. Every subsystem shares the same
private types, the same `Engine` struct and the same mutex.

- **Five god structs hold everything together.** `Engine` (566 methods, 51
  state fields), `expressionBinder` (422 methods, 101 fields), `StatementScope`
  (277 methods, 116 fields), `EvalContext` (44 fields) and `relationDescriptor`
  (47 fields). Every feature adds fields to them.
- **The data model is one knot that is easy to cut.** 193 types form a single
  strongly connected component. Eleven back-pointer fields hold it together.
  Replacing those eleven fields with tokens, interfaces or opaque payloads
  splits the core data model into clean layers (§2.3).
- **The two existing splits stayed shallow for the same reason.** The shared
  core types (object identity, the expression contract, the catalog model)
  still live in the root package. A subpackage cannot name them, so only leaf
  algorithms could move. `functions` works around this with generics
  (`Math[E, C, P]`) and `authorization` with value-only inputs. Everything that
  touches a core type stayed behind.
- **The proposal is to extract the shared core bottom-up.** Each step first
  defines its seam, then refactors inside the engine, then moves code.

```text
ids → expr → functions (rebuilt)
ids → lock
ids, expr → catalog → storage → visibility
```

After these steps the root `engine` package is still the facade and the
orchestrator (sessions, statements, binder, executor, DDL/DML features). Later
waves split those (§6). Each later wave first untangles a shared struct in
place (`EvalContext`, `binding`, `expressionBinder`, `StatementScope`), so the
root package becomes readable even before code moves. Steps 8–17 did this
for settings, the system catalog, the plan IR, the executor, the binder,
sessions and four feature packages:

```text
settings → pgcatalog
plan → exec → analyze;  session → routine, typedef, extension, sequence
```

The root still holds about half of the engine's production code (§2.7).
Steps 18–30 move most of it: first the shared seams that clean code is
stuck on (dump graph, stored-SQL rendering, reg* types, one DDL contract),
then the table family and views, then DML and Dump/Restore:

```text
dump, copyfmt, ddl, regobj → stored → index → table → partition → dml → control
                                  └──→ view, fdw, role ───────────┘
```

## 2. Analysis

### 2.1 Shape of the package

The largest areas, by filename prefix (production code only):

| Area | Files | Lines | Notes |
|---|---:|---:|---|
| `query_*`, `cte_*`, `window_*` | 62 | ~16.6k | Binding and execution of SELECT |
| `view_*`, `rule_*` | 50 | ~15.8k | Stored queries, rewriting, presentation |
| `partition_*`, `inheritance_*`, `table*`, `alter_*` | 78 | ~16.9k | Table DDL |
| `index_*`, `unique*`, `exclusion_*` | 41 | ~9.6k | Index DDL and maintenance |
| `expression*`, `conditional*`, `deparse_*` | 18 | ~12.4k | Expression nodes, rendering |
| Function glue (`string_*`, `datetime_*`, `json*`, aggregates, ...) | ~123 | ~19k | Engine side of builtin functions |
| `catalog*`, `object_*`, `definition_*` | 17 | ~6.7k | Catalog core |
| `reg*` (regproc, regtype, ...) | 23 | ~6.3k | Object-identifier types |
| Engine-side authorization (`acl*`, `role*`, `authorization.go`, ...) | 34 | ~6k | ACL enforcement on catalog objects |
| `transaction*`, `serializable*`, `savepoints`, `commit_*` | 22 | ~6k | Transactions and visibility |
| `pg_*` | 34 | ~4.3k | System catalog projections |
| `relation_lock*`, `relation_deadlock`, `advisory_lock`, `unique_lock`, multixact, `blocking_pids` | 9 | ~2.5k | Lock manager |

`control.go` (2.4k lines) and per-feature `*_dump.go`, `*_remap.go` and
`*_restore.go` files implement native Reset/Dump/Restore across all features.

### 2.2 Shared state and god structs

- **One mutex.** `Engine.mu` is used in 184 files (about 1,400 sites). It
  protects catalog state, storage roots, sequences, the visibility registry and
  commit ordering. Publication relies on this single lock to make catalog,
  storage and visibility changes atomic. That invariant must survive the split.
- **No catalog encapsulation.** `catalogState.descriptors` is read directly as
  a map in 359 files (1,662 sites). About 840 of those are typed lookups such
  as `state.descriptors[a].(*relationDescriptor)`. Only 38 sites write the map.
- **Feature state on shared structs.** `StatementScope` has 116 fields, most
  used by one or two features (truncate, foreign-key actions, inheritance
  ALTER, protocol portals, regproc output caches, ...). `EvalContext`,
  `CatalogStage` (46 fields), `binding` (68 fields) and `expressionBinder`
  follow the same pattern.
- **Behavior lives on `Engine`.** Most subsystem code is `Engine` methods that
  read `Engine` state fields directly (`e.visibility`, `e.commitSeq`,
  `e.storage`, `e.state`, ...). Core types have very few methods of their own.

### 2.3 The type knot and the cuts that untangle it

The declaration-level reference graph (method, appendix) shows 193 of 786
types in one strongly connected component, including `Engine`,
`StatementScope`, `EvalContext`, `relationDescriptor`, `CatalogRead`, `TxRef`,
`rowVersion` and the lock manager. Almost every path into that component goes
through one of these fields:

| # | Field | What it is used for | Replacement |
|---:|---|---|---|
| 1 | `CatalogRead.owner *Engine` | Provenance check, plus a service locator (owner token, authorization domain, roles, providers) | Catalog-owned owner record; engine-only services become engine functions |
| 2 | `CatalogStage.owner *Engine` | Publication orchestration | The stage stays in the engine; only its pure overlay moves (§5.5) |
| 3 | `TxRef.owner *Engine` | Provenance check; the lock manager also called `removeExclusionOwner` through it | `ids.Domain` token; `Engine.endLockOwner` does the exclusion cleanup (done in step 1) |
| 4 | `TxRef.attachment *transactionOwner` | Pointer-identity check, owner lookup | Sealed `ids.TxOwner` interface; `transactionOwner` embeds `ids.TxOwnerMarker` (done in step 1) |
| 5 | `SessionToken.owner *Engine` | Provenance check; also backed `Session.Engine()` and session and notification teardown | `ids.Domain` token; `Session` and `sessionNotifications` hold their engine (done in step 1; since 13d.3 the session holds it as a `session.Host`) |
| 6 | `SessionToken.private *Session` | Identity of private maintenance sessions | Sealed `ids.SessionOwner` interface; `Session` embeds `ids.SessionOwnerMarker` (done in step 1) |
| 7 | `BoundExpr.Eval(*EvalContext)` | Expression evaluation | `Eval(expr.Context)`, an interface `*EvalContext` implements (engine-local `ExprContext` in 2a, `expr.Context` since 2b) |
| 8 | `ruleDefinition.conditionBinding *binding` | Stored rule condition analysis | Rule payload that is opaque to the catalog (done in 5c: `rulesPayload`) |
| 9 | `ruleAction.binding binding` | Stored rule action analysis | Same rule payload (done in 5c) |
| 10 | `relationLockManager.engine *Engine` | Identity check only | `ids.Domain` token (done in step 1) |
| 11 | `relationLockPolicy.operation *Operation` | Published the queued request and the sticky timeout ERROR into two atomic `Operation` fields | `relationTimeoutSlot`, a lock-owned atomic slot that `Operation` embeds; the policy holds a pointer to it (done in step 4a) |

The engine allocates its catalog-owner token once and never replaces it. So a
single per-engine `ids.Domain` can serve every check that currently compares
against `*Engine`. The sealed owner interfaces keep today's pointer-identity
semantics without naming engine types; the engine recovers the concrete owner
with one checked helper per type.

A few more payloads should also be opaque to keep the catalog small. They are
not part of the knot itself:

- **`viewDefinition`.** It is reached from relations, rule actions and policy
  presentation. It is the frozen stored-SQL presentation and holds plan IR.
  Done in 5c: relations hold `viewPayload`, policies `policyPayload`.
- **Extension provider pointers on `procedureDescriptor`.** These are
  `unaccentProvider`, `pgcryptoProvider` and similar. Done in 5c: one
  `extensionImplementation` field, also held by the catalog owner record.
- **`expressionInfo.routineRecipe *relationDescriptor`.** It holds the
  `RoutineRecipeReference` interface since 2a, now `expr.RoutineRecipeReference`.
- **`relationTicket` storing a `transactionIdentityState`.** That enum moves
  down to `ids` (done in step 1 as `ids.TxCompletion`).

With these cuts the core data model layers cleanly. Each row counts only the
types new to that layer:

| Layer | New types | Content |
|---|---:|---|
| `ids` | 11 | OID, ObjectAddress, ObjectRef, epochs, revisions, NameKey, ObjectKind, SessionToken, TxRef, TxID, ... |
| `expr` | 9 | Expr, Properties, Volatility, Info, Context, RoutineReference, RoutineRecipeReference, RandomOwner, IntrinsicPropertyRule (measured at 2b) |
| `catalog` | 64 | Descriptors (namespace, relation, type, procedure, operator, collation), constraint, default, generation, identity, index, partition, inheritance, policy and foreign-table records, `catalogState`, `CatalogRead`, default ACL records, operator name index, `expressionDependency` |
| `lock` | 20 | Relation, tuple, advisory and unique-key resources, tickets, policies, multixact |
| `storage` | 18 | `rowVersion`, `relationStorage`, deletion roots, logical and hash index roots, `sequenceGenerator`, truncate and materialized-view lineage, `CommitSeq`/`RowID`/`VersionID` |
| `visibility` | 16 | Visibility registry, xid identity records, `View`/`Data`, `Root`, commit timestamps, serializable tracker, the private deletion overlay (moved in 7b) |

`savepointMark`, the `catalogChanges` journal, prepared transactions and
`transactionOwner` do not belong in `visibility`. They pull in domains, index
builds, materialized views, notifications and settings. They stay with the
session-level transaction owner (§6).

### 2.4 Behavior entanglement

The types separate cleanly; the code that uses them does not yet. Measuring
declarations that never reach a god struct once the cuts are applied:

| Area | Lines (declarations) | Already free of god structs | Main blockers |
|---|---:|---:|---|
| Lock manager | 2.3k | 1.25k | `Operation` timeouts, `Session` lookups in a few entry points (both resolved in step 4a) |
| Visibility | 2.1k | 0.5k | `Engine` methods reading engine-held registry fields (40 sites) |
| Catalog core | 6.4k | 1.9k | `Engine`, `CatalogStage`, `AuthorizationView`, session/statement plumbing |

So every core extraction needs a **state-ownership step**. Group the
subsystem's fields out of `Engine` into one struct, turn the `Engine` methods
that only touch that struct into its methods, then move it. Methods that
combine several subsystems stay in the engine as orchestration.

### 2.5 Why the `functions` split is shallow

- **Generics stand in for the missing expression contract.** `functions` has
  82 generic types such as `Math[E Expression[C], C MathContext[E], P any]`.
  The engine instantiates them as
  `functions.Math[BoundExpr, *EvalContext, ExprProperties]`: 1,257 references
  to 56 distinct instantiations, and 2,128 `functions.` references in 116
  files. Re-measured for 2a: 1,323 production and 313 test references in 169
  files, now with `ExprContext` as the context argument.
- **Binding stays in the engine.** About 123 engine files (~19k lines) are
  function glue, compared with ~11.6k lines in `functions`. Bind code calls
  private binder methods directly. There is no binder interface.
- **Dispatch is a name if-chain.** `bindFunctionProvider` (`function.go`)
  tests names one by one; `bindFunction` (`composite_expression.go`) only
  wraps it for the composite projection fallback. There is no registry.
- **Node operations are central type switches.** Walk, retag, remap, deparse,
  node-tree output, volatility, generated-column admission and join legality
  each enumerate every node type. Examples: `aggregate_bind.go` (159
  `functions.` cases), `generated.go` (125), `conditional_plan.go` (94),
  `view_remap.go` (74), `deparse_renderer.go` (60), `expression_retag.go` (55),
  `query_expression_walk.go` (52) and `expression_volatility.go` (49). Moving
  one function means touching all of them.

The runtime capabilities that functions need already exist: 25 methods on
`EvalContext` (24 `Function*` plus `WholeRowNullPresence`: RNG, clocks, UUID
entropy, regex cache, output environments, literal-NULL checks). Since 2a they
form the engine's `ExprContext` interface, which became `expr.Context` in 2b;
F1 replaced the `functions` generic constraints with it.

Engine-side authorization (~6k lines) is blocked the same way. Its main
dependency is the catalog (381 references to `catalog.go` alone), so it cannot
leave the root package until the catalog does. B0 corrected the size: only
about 750 of those lines are enforcement; the rest is orchestration, ACL SQL
functions, `pg_*` projections, Dump/Restore and policy DDL
([ENGINE-SPLIT-authorization.md](ENGINE-SPLIT-authorization.md), B0 outcome).

### 2.6 The root package after steps 1–7

Measured at `75fe0d710` with the appendix method: a `go/types` pass over the
production files of `internal/engine`, recording each top-level declaration's
references, method calls and field uses. "Clean" means the declaration never
reaches one of the nine god structs below, directly or through other engine
declarations.

- **Size.** 688 production files, about 170k lines, 4,700 top-level
  declarations. The ten subpackages hold about 40k lines.
- **God structs.**

  | Struct | Declared at | Fields | Methods | Note |
  |---|---|---:|---:|---|
  | `Engine` | `engine.go:23` | 24 | 553 | Subsystem stores are already grouped (`catalog`, `storage`, `visibility`, `roles`, `locks`) |
  | `StatementScope` | `transaction.go:276` | 119 + 21 embedded | 290 | Over 50 fields are used by one feature only (§6 step 13) |
  | `expressionBinder` | `expression.go:1204` | 101 | 348 in 103 files | Flags for every statement kind and consumer |
  | `binding` | `engine.go:61` | 69 | — | One struct for SELECT, INSERT, UPDATE, DELETE, MERGE, CREATE TABLE and view capture |
  | `EvalContext` | `expression.go:20` | 43 | 49 | Frame, statement runtime and routine admission mixed |
  | `CatalogStage` | `catalog.go:340` | 39 | — | Index, domain, typed-table and materialized fields |
  | `Operation` | `session.go:110` | 35 | — | 12 Restore replay flags |
  | `transactionOwner` | `transaction.go:49` | 39 | — | Includes FDW participants |
  | `Session` | `session.go:70` | 35 | — | |

- **The plan IR is already clean.** `LogicalNode`, `BoundQuery`, the 20 plan
  node types and their records reach no god struct, except
  `cteDefinition.mutation *binding`. All 54 concrete expression node types are
  clean as types; only their `Eval` methods are not, because they recover
  `*EvalContext` through `evalContext(ctx)`. Most of them read only the
  cancellation context and the input/output environments, which `expr.Context`
  already offers.
- **Builtin inventories are clean.** The builtin routine and operator tables
  and their metadata (about 3.2k lines in `routine_inventory.go`,
  `operator_inventory.go`, `operator_metadata.go`, `operator_routines.go`)
  reach no god struct.
- **System catalog producers are nearly clean.** 30 of the producers in
  `pg_*.go` are dirty only because they return a `*querySource`, which holds a
  `*StatementScope`. The rest check catalog provenance against `Engine` fields
  that `catalog.Owner` already holds.
- **Settings definitions are clean.** About 700 of the 950 lines in
  `settings.go` and `settings_format_encoding.go` reach no god struct; the
  per-session store (`session_settings.go`) does.
- **Feature areas, cross-referenced.** Counting identifiers defined in one
  area and used in another: operators/routines are used most by all others
  (about 620 references in), types next (about 190), then index. Index,
  partition, inheritance, views and DML use many other areas. The order of the
  feature steps in §6 follows from this.

### 2.7 The root package after steps 1–17

Measured at `c154e14fb` with the appendix method: `reach` over every root
production declaration, `frontier` per area for cross-area uses, and a
file-to-area map (one candidate owner per root file). "Clean" now means the
declaration never reaches `Engine`, `StatementScope` or `CatalogStage`.

- **Size.** 558 production files, about 114.5k lines: 51% of the engine's
  production code, the 21 subpackages hold about 110k. 1,098 unit test files,
  about 212k lines; about 95k of them run SQL through the engine and match no
  single production file.
- **God structs.** Three remain. `Engine` (`engine.go:28`): 24 fields, 514
  methods. `StatementScope` (`transaction.go:201`): 66 fields, 12 of them
  feature slots, and 321 methods. `CatalogStage` (`catalog.go:347`): 28
  fields, 31 methods. `AuthorizationView` holds `*Engine`, so code that only
  takes it to read `access.View` data counts as dirty.
- **Clean code.** 37.3k lines (1,464 declarations) are clean; 70.3k are not.
  Of the dirty lines about 34.5k are `Engine` methods and 13.4k
  `StatementScope` methods; the rest reach them through other root
  declarations, often only through `AuthorizationView` or `dropProvider`.

  | Area (files) | Lines | Clean | Planned owner (§6) |
  |---|---:|---:|---|
  | Views, rules, policies, RLS, materialized views (71) | 17.5k | 11.1k | `stored` (22), `view` (27) |
  | Tables: CREATE/ALTER, NOT NULL, CHECK, DEFAULT, generated, FK (49) | 13.0k | 4.3k | `table` (24); FK enforcement to `dml` |
  | Indexes, unique and exclusion constraints (41) | 8.5k | 2.5k | `index` (23) |
  | DML, COPY, TRUNCATE (35) | 8.0k | 1.8k | `dml` (29), `copyfmt` (19) |
  | Partitions (31) | 7.2k | 2.0k | `partition` (25); partitioned indexes to `index` |
  | Transactions, sessions, TEMP lifetime, notifications (36) | 6.9k | 0.9k | root orchestration; `session` (13f) |
  | Statement dispatch, protocol, cursors, prepared SQL, host adapters (30) | 6.9k | 0.7k | root |
  | Type leftovers: domains, enums, composites, collations, typed tables (43) | 6.4k | 1.8k | `typedef` (15e), `stored`, `table` |
  | Inheritance (25) | 5.7k | 2.3k | `table` (24); DML expansion to `dml` |
  | Expression utilities: deparse, retag, preflight (20) | 5.3k | 3.0k | `stored` (22), `exec` (11f), `plan` (10e) |
  | Schema-level DDL: schemas, tablespaces, COMMENT, RENAME, DROP, object addresses (21) | 5.1k | 1.2k | `ddl` (20), `catalog` (5i), `regobj`, `table`, root |
  | Roles and ACL DDL (25) | 4.0k | 0.5k | `role` (28), `access` (B6), `regobj` |
  | Catalog orchestration and publication (12) | 3.1k | 0.6k | root |
  | Foreign data wrappers (15) | 2.8k | 0.6k | `fdw` (26) |
  | Reset/Dump/Restore driver (2) | 2.6k | 0.3k | `dump` (18), `control` (30) |
  | reg* object-identifier types (20) | 2.5k | 1.3k | `regobj` (21) |
  | Routine leftovers (17) | 2.0k | 0.8k | `routine` (14e), `stored`, `exec` (11e) |
  | Query admission and legality (9) | 1.9k | 0.8k | `analyze` (12f), root |
  | Sequence, identity and serial leftovers (14) | 1.9k | 0.3k | root, `table` (24d), `regobj` |
  | Function glue and `statementHost` capability bodies (28) | 1.4k | 0.4k | `stored`, root |
  | System catalog runtime, `pg_locks`, `pg_sequences` (6) | 0.8k | 0.1k | root |
  | Extension handler orchestration (7) | 0.4k | 0 | root |
  | `engine.go` | 0.5k | 0 | root |

- **Why clean code stays in the root.** Most clean leftovers use shared root
  helpers that have no package yet: the stored-SQL renderers (`pgRenderer`,
  `scalarRenderer`, `viewRenderer`), the dump graph (`dumpEventKey`,
  `dumpPhase`, `dumpRelation`), the reg* formatters and the stored view
  payload (`viewDefinition`, `viewOf`). Earlier prompt files call these the
  "dump seam" and the "stored-SQL rendering seam" (typedef, routine and
  catalog notes). For example `domain_dump.go`, `operator_dump.go`,
  `domain_remap.go` and `typed_inheritance.go` are clean but stuck. Counting
  distinct declarations that clean code of one area uses from another:
  views use 38 from the expression utilities, inheritance 29 from tables,
  the expression utilities 21 from reg* code, `pgcatalog`'s root runtime 19
  from views, the type leftovers 18 from reg* code. Followed to a fixed
  point, the presentation code forms one closed set of about 283
  declarations (about 11k lines) whose only outside reference is `sqlError`.
- **Three copies of one DDL contract.** `routine`, `typedef` and
  `extension` each declare an `Authority` and a `Stage` with nearly the same
  methods, so the root keeps two staging adapters (`schemaHost`,
  `typedefHost`) with the same fields. Every further feature would add a
  copy.
- **The import graph differs from the §3 numbering.** `session` imports
  neither `exec` nor `analyze`; `routine`, `typedef` and `sequence` import
  `session`; `analyze` imports `routine`, `typedef`, `sequence`, `extension`
  and `pgcatalog`. So the step 14–17 features sit between `session` and
  `analyze`. The presentation code uses about 50 `analyze` symbols (capture
  vocabulary, closed defaults, lookups), so it sits above `analyze`, and with
  it everything that renders stored SQL: views, the table family and Dump.
  §3 now shows the measured order.
- **Central switches.** `control.go` holds three: the descriptor-kind switch
  of `Dump` (one 1,588-line function), its phase switch, and `Restore`'s
  statement allow-list with about 30 pre/post hooks. Four more walk every
  feature: `remapPreparedCatalog`, `reownCatalogExpressions` (also used
  outside Restore), `remapViewRuleOIDs` and `dumpPredecessor`.

## 3. Target architecture

```text
 L-1 engine/fts            immutable FTS codecs (imports stdlib, sqlstate only)
 L0  parser   sqltype   sqlstate                         (existing leaves)
 L1  engine/ids            identity values and provenance domain
 L1  engine/authorization  role graph and ACL algorithms (existing)
 L2  engine/expr           expression contract and node protocol
 L2  engine/lock           heavyweight lock manager            (imports ids)
 L3  engine/functions      builtin functions                   (imports expr, ids)
 L3  engine/catalog        descriptors, State, Read, Candidate (imports ids, expr, authorization)
 L4  engine/storage        row versions, storage and index roots (imports catalog, expr)
 L4  engine/access         authorization enforcement (imports catalog, authorization)
 L5  engine/visibility     xids, commit order, snapshots, SSI  (imports storage, catalog, lock)
 L6  engine                facade, sessions, statements, binder, executor, features
```

The later waves (§6) refine L6 into these layers. The numbers follow the
import graph measured at `c154e14fb` (§2.7); steps 18–30 are planned.

```text
 L1   engine/settings    setting definitions, values, validation (sqltype, sqlstate)           step 8
 L3.5 engine/dump        dump graph records, ordering, Presenter/Resolver capabilities
                         (ids, catalog, parser, expr, functions, sqltype)                      step 18
 L4   engine/copyfmt     COPY text, CSV and binary codecs and options (catalog, parser, sqltype) step 19
 L5   engine/pgcatalog   system relation producers (catalog, authorization, lock, expr)        step 9
 L6   engine/plan        plan IR and concrete expression node types (expr, functions, catalog, lock) step 10
 L7   engine/exec        executor runtime, the concrete evaluation context (plan, storage, visibility, lock) step 11
 L7   engine/session     sessions, operations, transaction owner, journal, settings store
                         (storage, visibility, settings; never exec, analyze or a feature)     step 13
 L8   engine/ddl         shared DDL contract (Authority, Stage, Host), schema and tablespace DDL
                         (access, catalog, session)                                            step 20
 L9   engine/routine, typedef, extension, sequence
                         lower features: records, algorithms, handler decisions; analyze imports them  steps 14–17
 L10  engine/analyze     binder and statement binding (plan, exec for constant folding, lower features) step 12
 L11  engine/regobj      reg* types, aclitem I/O, object identity (analyze, exec)              step 21
 L11  engine/stored      stored-SQL definitions: view, rule and policy payloads, renderers,
                         retag and remap (regobj, analyze)                                     step 22
 L12  engine/index → engine/table → engine/partition;  engine/fdw;  engine/view;  engine/role
                         upper features: may import analyze types, never build a binder       steps 23–28
 L13  engine/dml         write executors (analyze payloads, every upper feature)               step 29
 L14  engine/control     native Reset/Dump/Restore drivers over registered providers           step 30
 L15  engine             public facade, statement dispatch, StatementScope, Engine
                         orchestration and the host adapters
```

Until step 17 this table proposed `analyze` at L8, `session` at L9 and the
features at L10. The measured graph put `session` beside `exec` and the
step 14–17 features below `analyze` (rule 6).

`engine/proc` (SQL routine definitions and invocation frames) arrives with the
`PG18-ROUTINE-CORE` feature work. It imports only `parser`, `sqltype` and
`sqlstate`, and `catalog` holds its definitions, so it sits at L1. It is not
the step 14 `routine` package (§6).

Package names are proposals (§7).
`engine/fts` is intentionally below `sqltype`, despite directory nesting. SQL
type descriptors import this stateless codec leaf; it never imports `sqltype`,
the SQL parser, catalogs, expressions, storage, engine orchestration or driver.
Its private immutable payloads contain no session/operation/notice authority.
`sqltype` owns SQL IO integration, and the root engine queues operation notices.


### Dependency rules

1. **Imports point downward only.** No subpackage imports the root `engine`
   package. Siblings on the same level do not import each other unless this
   document says so.
2. **The root package is the composition layer.** It wires subsystems, owns
   sessions and statements, and keeps the public facade used by the driver
   (about 56 symbols, kept stable through aliases or wrappers).
3. **One lock authority.** `Engine.mu` stays the single mutex for catalog,
   storage and visibility state, because publication must stay atomic across
   all three. Subsystem state structs have no mutex of their own. Their
   mutating methods carry the `Locked` suffix or document that the caller holds
   the engine mutex. The lock manager keeps its own mutex, innermost: never
   taken under `Engine.mu` or the session mutexes, and holding nothing else.
   Step 4a confirmed this against the code; the order is documented in
   [`internal/engine/lock/README.md`](../internal/engine/lock/README.md).
4. **Subsystem packages own data, not orchestration.** They own immutable
   records, copy-on-write transforms and validation. Installing a change,
   cancellation fences and lifecycle stay in the root until a later wave gives
   them an owner.
5. **`functions` stays a leaf library.** It imports `expr`, `ids`, `sqltype`,
   `parser` and `sqlstate`, never `catalog`, `storage`, `lock` or `visibility`.
   When a function needs engine state, it declares a narrow capability that the
   binder or evaluation context implements.
   Transaction identity/age/member and commit-timestamp nodes use
   `functions.TransactionInquiryRuntime`, implemented on `*EvalContext`; their
   binders/source/protocol behavior are leaf-owned while live scope, allocation,
   history, mutexes/tickets and publication retain their canonical owners.
6. **Feature packages do not import each other** unless §6 says so. Shared
   needs go down into a core package or up into the root dispatch. There
   are two kinds:
   - **Lower features** (steps 14–17). Their content sits below `analyze`:
     the binder calls it, so `analyze` may import it. Their handlers never
     import `analyze`; they reach binding through the root host (12d
     refine). For the same reason `session`, which they import, never
     imports `analyze` or a feature package (13c refine).
   - **Upper features** (steps 23–29). They render stored SQL, so they sit
     above `stored`, which sits above `analyze` (§2.7). They may import
     `analyze` types and pure functions, `regobj` and `stored`, but never
     construct a binder: binding goes through the root host, which owns
     `newExpressionBinder`. `analyze`, `exec`, `session`, `pgcatalog` and
     the lower features never import an upper feature, `regobj` or
     `stored`; they reach them through their host interfaces. Upper
     features import each other only along the arrows of the layer table:
     `index` → `table` → `partition`, and `dml` may import all of them.
7. **Lower layers never name upper-layer types.** When a lower package must
   carry an upper-layer value (a feature's statement state, a journal fence,
   a CTE's mutation binding), it holds a lower-layer interface or an opaque
   payload, and the owner recovers the concrete type with one checked helper
   (as `viewOf` did in 5c).
8. **Registration replaces central switches.** Code that lists every
   feature (Dump's descriptor and phase switches, Restore's statement
   allow-list and hooks, the catalog-wide expression reowner) becomes an
   ordered table of entries that the root composes in today's order. A
   feature package adds its entry; it never edits the switch (§4.1 pattern
   5).

## 4. Migration method

Every step follows the same three phases. Each commit keeps the build green.

1. **Define the seam** in the target package: types, interfaces and the
   minimal API. Nothing moves yet.
2. **Port inside the engine.** Refactor the engine code to use only that seam:
   group state fields, replace back-pointers, convert methods to functions over
   the interface, add accessors. This is the risky phase, and it happens where
   the compiler and tests already cover everything.
3. **Move.** Move the declarations into the subpackage. Leave temporary
   aliases in the engine (`type ObjectAddress = ids.ObjectAddress`) so the move
   is one small diff. Remove the aliases in follow-up commits, area by area.

Tooling for the mechanical parts:

- `gopls rename` renames single identifiers across the package, including
  tests. For hundreds of fields, a `go/types` tool with a before/after
  reference census is much faster (5d).
- `gofmt -r` rewrites simple expression patterns.
- A short script converts the typed map-lookup pattern (§5.5) to accessors.
- The dependency-graph tool from the appendix re-checks for cycles before each
  move.

Verification for every step:

- `make build`, then `make quickcheck` and `make racecheck`, within the
  900-second deadlines from `AGENTS.md`.
- `make bench` after each core step. Fresh-engine startup must stay under 1 ms
  with no new allocations.
- Selected `make check REFERENCE=1 RUN='...'` runs for areas whose evaluation,
  rendering, remap or Dump/Restore paths change.

Rules for tests during the split:

- `reference/` is read-only. Its definitions are the golden behavioral
  standard and are not modified. The split adds no SQL surface, so it adds no
  reference tests.
- Engine unit tests are adapted mechanically (the same renames apply) or
  deleted when they only pin internals that no longer exist. Unit tests for
  moved code move with it.
- Tests follow the code; they never decide what moves. A declaration is not
  kept in the root because root tests call it: tests of moved pure code move
  into the package, and tests that need an engine fixture stay in the root
  and are rewritten to the new names, or deleted when `reference/` covers
  their behavior (name the covering test).
- `docs/TESTING.md` asks for offending public-API tests to be migrated into
  `reference/` whenever an area is touched. That conflicts with keeping
  `reference/` read-only. Proposal: during the split, delete such tests when
  `reference/` already covers the behavior. Otherwise keep them adapted and
  record them as follow-ups (§7).
- Documentation, including `AGENTS.md`, is updated with every sub-step under
  the rules in §8.4. The current `functions` and `authorization` READMEs
  describe the shallow split and need rewriting once the matching steps land.

### 4.1 Patterns for the later waves

The first steps proved four patterns. Every step in §6 names the ones it uses.

1. **State ownership in place** (5a, 6a, 7a, B1). Group a subsystem's fields
   into one engine-local struct, turn the methods that only touch it into its
   methods, then move it. The struct has no mutex; mutating methods carry the
   `Locked` suffix.
2. **Slot.** A field of a shared struct (`StatementScope`, `CatalogStage`,
   the `catalogChanges` journal, `Operation`, `transactionOwner`) that only
   one feature uses moves into a feature-owned struct. The shared struct keeps
   one pointer to it, allocated on first use by one accessor
   (`s.truncateState()`). Reset (`transaction_finish.go`) and copy
   (`sql_cursor_hold.go`) code then handle the slot as one unit. Before the
   shared struct moves to a lower package, its slot fields become lower-layer
   interfaces (rule 7 of §3).
3. **Capability.** When moved code needs engine state at run time, its
   package declares a narrow interface of value-taking methods. The engine
   implements it with a compile-time assertion, and the moved code recovers
   it from a context it already receives (`expr.Context`, a producer input).
   `functions.ACLRuntime` (Tier C) is the model. Capabilities never return
   engine structs and never grow into service locators.
4. **Refine first.** When a step's file-level plan depends on earlier steps
   that have not landed, its first sub-step is a docs-only refinement (as B0
   was): re-measure with the appendix tool, write the move set, host
   interface and commit sequence into the prompt file, and wait for approval.
5. **Registration** (18c, 30a; rule 8 of §3). Replace a central switch over
   descriptor kinds or statement types with an ordered table of entries.
   The root builds the table once, in the switch's order, so error
   precedence and output order stay byte-identical; each feature package
   exports its entry and the table names no feature internals.
6. **Reader-interface cut** (22a, 23a). When a helper is dirty only because
   it borrows a root struct for a few reads (`&dropProvider{base: view}`),
   give it a small reader interface that the root struct and a `catalog.Read`
   adapter both implement. `partitionIndexCoverageComplete` is the model:
   that one cut makes about 2.2k lines of index, partition and dump code
   clean.

Two more rules apply to the feature packages (§6 steps 14–17 and 23–29):

- **Two parts.** Part A: feature state leaves the shared structs (slots),
  and the records and algorithms that reach no god struct move into the
  package. Part B: a docs-only refine writes the host interface, then the
  DDL and DML handler decisions move behind it. For steps 14–17 Part A ran
  before `session` moved and Part B after; for steps 23–29 `session` has
  moved, so Part B follows each package's own Part A. The orchestration
  (`Engine.mu`, lock waits, the journal and publication) stays in the root.
- **Clean sets are measured, not guessed.** Each Part A session re-runs the
  appendix tool and moves only declarations that reach no god struct, plus
  those the in-place sub-step made clean.

## 5. First steps

Steps 2 and 3 form the expression track. Step 4 (lock) needs only step 1.
Steps 5 to 7 form the state track: step 5 needs step 2, and step 7 also needs
step 4. After step 1, the expression track and the lock step can run in
parallel.

```text
        ┌─▶ 2 expr ──▶ 3 functions (family by family)
1 ids ──┤        └──────────┐
        └─▶ 4 lock          ▼
                         5 catalog ──▶ 6 storage ──▶ 7 visibility ◀── (4 lock)
```

### Step 1 — `engine/ids`: identity values and provenance

**Moves.**

- Catalog identity: `OID`, `CatalogEpoch`, `DefinitionRevision`,
  `ObjectAddress`, `ObjectRef`, `NameKey`, `ObjectKind` with its constants,
  catalog class IDs and builtin namespace OIDs.
- Runtime identity: `SessionID`, `SessionToken`, `TxID`, `TxRef`, `CommandID`,
  `CommitSeq`, `RowID`, `VersionID`, and the transaction completion state
  (`ids.TxCompletion`, formerly `transactionIdentityState`).
- `catalogOwner` becomes `ids.Domain`: one per engine, nonzero size, never
  reused.

**In-place port first.**

- Replace `TxRef.owner`, `SessionToken.owner` and `relationLockManager.engine`
  with the domain (cuts 3, 5 and 10).
- Replace `TxRef.attachment` and `SessionToken.private` with sealed
  `ids.TxOwner` and `ids.SessionOwner` interfaces, implemented by markers
  embedded in `transactionOwner` and `Session` (cuts 4 and 6).
- Add constructors and accessors for the unexported fields. `ObjectRef{owner:
  ...}` literals become `domain.Ref(epoch, address)`. `TxRef` had 180
  production field uses and `SessionToken` 62; `ObjectRef.owner` had 373
  production and 428 test references. The rewrite touched 466 files.

**Stays in the engine.** `Isolation`, `TxOptions` and other transaction
options are behavior, not identity.

**Doable because** the closure is 11 types depending only on the standard
library, there are no methods to relocate beyond trivial ones, and aliases make
the move a one-file diff.

**Done when** `ids` has no imports beyond the standard library and the engine
uses aliases or direct `ids.` names. Done: 1d removed the aliases.

### Step 2 — `engine/expr`: the expression contract

**Moves.**

- `BoundExpr` becomes `expr.Expr`. The engine kept a temporary alias until
  2d removed it with the other `expr` aliases.
- `ExprProperties` becomes `expr.Properties`; `Volatility` and its constants
  move too.
- `expressionInfo` becomes `expr.Info`, replacing `functions.Info[P]`. In 2b
  `expr.Info` took the `functions.Info[P]` field names; the generic type
  itself stayed until F1, because its `P` reached the context constraints.
- `functions.RoutineReference` and `RoutineRecipeReference` move to `expr`.
  `expressionInfo.routineRecipe` holds the interface instead of
  `*relationDescriptor`.
- Generic helpers: property merging and positioned expression errors.

**The evaluation context becomes an interface (cut 7).**

```go
package expr

type Expr interface {
	ResultUsage() sqltype.Usage
	SourcePosition() parser.Position
	Properties() Properties
	CollationIndeterminate() bool
	ImplicitCollationConflict() (sqltype.CollationRef, sqltype.CollationRef, parser.Position)
	ExplicitCollation() parser.Position
	Eval(Context) (sqltype.Value, error)
}

// Context is the evaluation runtime visible outside the engine. The engine's
// *EvalContext implements it; engine-private state stays on that struct.
type Context interface {
	FunctionContext() context.Context
	FunctionInput() sqltype.InputEnvironment
	FunctionOutput() sqltype.OutputEnvironment
	FunctionLiteralNull(Expr) bool
	// ...the existing Function* capabilities, in documented groups:
	// presentation environments, RNG and UUID entropy, clocks and statement
	// time, regex execution cache, conversion handlers.
}
```

- The 60 engine `Eval` methods change from `Eval(c *EvalContext)` to
  `Eval(ctx expr.Context)`. One helper recovers the engine context
  (`c := evalContext(ctx)`). Nodes that only forward the context to children
  skip the recovery (done in 2a, against the engine-local `ExprContext`).
- Value copies of `EvalContext` (about 50 sites) and its private fields stay
  unchanged. That is why this design keeps the concrete struct in the engine
  instead of splitting it now.

**Node protocol, defined here and adopted in step 3.**

- `Composite`: `Children() []Expr` and `Rebuild(Info, []Expr) (Expr, error)`.
  `Rebuild` is the copy-on-change reconstruction that functions already
  implement as `Reconstruct`.
- Optional rendering hooks for SQL deparse and node-tree output. Moved to
  step 3 F2 at 2c: without a caller their signature would be a guess.
- Walk and map helpers built on the protocol. Engine switches can then fall
  back to `case expr.Composite:` instead of listing every node type.
- Landed in 2c with the lexical walk switch as the first adopter; 35
  function nodes implement it (see the `expr` README).

**Stays in the engine.** Concrete engine nodes (column, parameter, coercion,
CASE, row and subquery nodes) and the plan IR. They move later with the
executor (§6), once `EvalContext` is split into frame state and statement
runtime.

**Doable because** the contract closure is 4 types over `ids`, `sqltype` and
`parser`, and `*EvalContext` already implements the capability methods.

**Risk.** One extra type assertion per engine node evaluation. Confirm with
`make bench` and an evaluation-heavy reference selection.

### Step 3 — `engine/functions`: a real seam

**Target design.**

- **Plain nodes.** Function nodes are non-generic structs over `expr.Expr`,
  `expr.Context` and `expr.Info`.
- **Nodes carry their own operations.** Each node implements the node
  protocol (children, rebuild, render, validate). The engine's central switches
  no longer list function nodes.
- **Bind code depends on an interface.** It uses a `functions.Binder`
  interface that `expressionBinder` implements, never binder internals.
- **A registry replaces the if-chain.**

```go
package functions

// Binder is what builtin bind code may ask of the engine's analyzer.
type Binder interface {
	AnalyzeArgument(parser.Expr, AnalysisRequest) (Argument, error)
	CoerceArgument(...) (expr.Expr, error)
	Type(sqltype.ID) sqltype.Usage
	Info(sqltype.Usage, parser.Position, expr.Properties) expr.Info
	BuiltinVisible(parser.ObjectName) (bool, error)
	BuiltinRoutine(provider, name string, args ...sqltype.Usage) (expr.Info, error)
	SignatureError(...) error
	Consumer() ConsumerContext // DEFAULT / CHECK / index / policy restrictions
	// ...measured, deliberately extended (see F4)
}

type Definition struct {
	Names []string
	Bind  func(b Binder, call parser.FunctionExpr) (expr.Expr, error)
}

func Lookup(name string) (Definition, bool)
```

The `Binder` methods above are the ones function glue uses most today. They
are `info`, `typ`, `builtinRoutineInfo`, `AnalyzeArgument`,
`functionSignatureErrorForTypes`, `builtinVisible`, `diagnosticTypeName`,
`CoerceArgument`, `comparisonOperand`, `coerceConditional` and
`builtinRoutineReference`, each used in 17 to 79 files. Glue uses 47 binder
methods in total. The long tail is mostly aggregate, grouping and query
analysis, which keeps an engine-side seam.

F3 landed the interface; the `functions` README lists its methods and why.
It differs from the sketch: `Type` returns a descriptor, `comparisonOperand`
and `coerceConditional` are argument intents rather than methods, routine
identity takes a `functions.RoutineProvider`, and `Consumer` is not needed
yet.

**Sub-steps.**

- **F1 — Remove generics.** Mechanical; needs step 2. Each `X[E, C, P]`
  becomes `X`. Context constraints fold into `expr.Context`. In the engine,
  `[BoundExpr, ExprContext, ExprProperties]` suffixes are deleted (1,808
  references in 184 files when F1 landed) and `functionInfo` conversions
  disappear.
- **F2 — Adopt the node protocol, one switch at a time.** Walk, retag, remap,
  volatility, deparse, node tree, stored scalar SQL, generated admission,
  conditional planning, join legality. Each switch gains one protocol branch
  and loses its per-function cases; behavior beyond children goes into small
  optional hooks (listed in the `expr` README). Verify views, stored defaults
  and checks, and native Dump/Restore selections, since they exercise remap
  and deparse.
- **F3 — Binder interface and registry.** Engine dispatch consults
  `functions.Lookup` first, then falls through to the existing chain.
- **F4 — Port inside the engine, family by family.** Convert
  `func (b *expressionBinder) bindMath(...)` into
  `func bindMath(b functions.Binder, ...)` while it is still in the engine.
  The compiler proves the interface is enough. Each new `Binder` method is a
  reviewed decision: a genuine analyzer capability, or a leak to remove.
- **F5 — Move.** Move the ported family into `functions`, register it, and
  delete its engine branch and glue file.

**Family order, by how much engine state they need.**

- **Tier A, pure values.** Math, string, LIKE/SIMILAR/regex, datetime,
  JSON/JSONB/SQL-JSON, arrays, ranges, UUID, format, binary, numeric
  formatting, and the kernels of installed extensions (pgcrypto,
  fuzzystrmatch, unaccent, intarray, intagg, uuid-ossp). Installed functions
  get their procedure identity through a `Binder` capability, not catalog
  access.
- **Tier B, runtime services.** Random, current time and clocks, sleep, and
  settings reads. These use `expr.Context` capabilities.
- **Aggregates and windows.** Signatures, provider selection and state
  algorithms move. FILTER, DISTINCT, ORDER, grouping and window execution stay
  with the engine's grouping code. `aggregate_bind.go` is the largest single
  item.
- **Tier C, inquiry functions.** Catalog, ACL, xid, advisory-lock and sequence
  inquiry wait for `catalog`, `lock` and `visibility`. Then they either get
  narrow capability interfaces in `functions` or live with their subsystem.
  Settled by the refinement: each part moves to `functions` with a narrow
  capability interface recovered from `expr.Context` (`ACLRuntime` first).

**Done when** no generic instantiations remain, `function.go` dispatch is a
registry lookup, central switches contain no function node cases, and the Tier
A and Tier B glue files are gone from the engine.

### Step 4 — `engine/lock`: lock manager (pilot for state extraction)

Small and self-contained, with its own mutex. This is the cheapest place to
prove the state-ownership method.

**Moves.** `relationLockManager`, tickets, requests, policies, lock resources
and modes. Also multixact members and rows, unique-key buckets, the advisory
lock manager part, deadlock detection, and the wait and timeout mechanics
(`relation_lock*.go`, `relation_deadlock.go`, `relation_timeout.go`,
`unique_lock.go`, `transaction_multixact.go`, parts of `advisory_lock.go` and
`blocking_pids.go`).

**Cuts.**

- The engine back-pointer becomes the domain (cut 10, done in step 1).
- `relationLockPolicy.operation` becomes a pointer to the lock-owned
  pending-timeout slot that `Operation` embeds (cut 11, done in 4a).
- `EndReason` stays in the engine. The manager never used it; only the
  session's advisory delegate (`sessionAdvisory.EndSession`), which stays in
  the engine, receives it. The completion state comes from `ids`.
- Unique-lock keys hold an opaque owned value with a comparer the manager
  calls outside its mutex, and `blockingPids` returns plain PIDs, so the
  manager needs no `sqltype` (done in 4a).

**Stays in the engine.**

- Query lock admission, `LOCK TABLE` statement handling and relation scopes,
  which combine locks with the catalog.
- The `pg_locks` projection (later `pgcatalog`).
- Safe-snapshot blocking (`visibility`).

**Doable because** after the cuts the closure is 20 types over `ids` only.
Step 4a confirmed it: the lock-owned files compile against `ids`, `sqlstate`
and the standard library apart from `ErrCatalogReplaced` and `sqlError`.
More than half the code (1.25k of 2.3k lines) is already free of god structs.
The remaining references are entry points that stay in the engine.
Step 4b moved it: `ErrCatalogReplaced` went to `ids` and `lock` has a private
`sqlError`. The larger cost was test coupling, not code: about 140 engine test
files read manager internals under its mutex. They now read an owned
diagnostic snapshot (`Inspect`), and tests that staged races under the mutex
moved into `lock` on an engine-free fixture. Step 4c removed the aliases and
the engine's `ErrCatalogReplaced` re-export; the engine names `lock.` and
`ids.ErrCatalogReplaced` directly.

**Done when** `lock` imports only `ids`, `sqlstate` (lock diagnostics) and the
standard library. Its README documents mutex ownership and lock order. `make racecheck` passes, including
the existing deadlock, cancellation and teardown units.

### Step 5 — `engine/catalog`: descriptors, State, Read, Candidate

The largest step. It is split into sub-steps that are each safe and mostly
mechanical. "Catalog" means the object model and its immutable snapshots, not
DDL. DDL commands stay in the engine until their feature waves.

**5a — State ownership in place.** Group `Engine.state`, `providers`,
`nextOID`, `nextRevision` and `nextEnumMember` with the owner domain into one
`catalogStore`. `Engine` methods that only touch it become its methods. The
domain turned out to be engine-wide (it also mints session and transaction
references); the store owns it as the engine's single provenance domain.

**5b — Typed read API.**

- Add accessors to `CatalogRead`: `Relation(a)`, `Namespace(a)`, `Type(a)`,
  `Procedure(a)`, `Operator(a)`, `Collation(a)`, ..., plus name lookup.
- Migrate the direct map reads (about 1,700 in production and 2,600 in
  tests when done, all scripted). Route the writes (about 110 in
  production) through one write API.
- Replace `CatalogRead.owner` service-locator uses (cut 1) with explicit
  parameters and a catalog owner record holding the domain, the principal
  domain and ACL authority that stored values must belong to, and the
  provider registries. Code with an `AuthorizationView` takes roles and
  engine services from it. `CatalogRead.Comment` becomes an engine function.
  Done; see the prompt file's 5b results.

**5c — Payload seams.**

- Views, rules and policy presentation become catalog-defined payload
  interfaces (cuts 8 and 9). They expose only what catalog code needs, for
  example dependencies and temporary ownership. The concrete `viewDefinition`
  and `ruleDefinition` stay in the engine behind one checked accessor each
  (`viewOf(d)`), until views and rules get their own package.
- Extension provider pointers on `procedureDescriptor` become one
  implementation interface.
- Expression payloads (check, default, generation, index keys and predicates,
  domain checks, policy expressions) hold `expr.Expr`.

Done; see the prompt file's 5c results. Expression payloads were already
`expr.Expr`.

**5d — Export descriptor fields.**

- Export the fields with a tool. `relationDescriptor` alone has 7,800 field
  uses in 357 files.
- Descriptors are immutable value records, so exported fields are idiomatic.
- Fields that collide with `Descriptor` interface methods (`address`, `name`,
  `namespace`, `revision`) are read through those methods.

Done; see the prompt file's 5d results. A `go/types` tool replaced
per-field `gopls rename`; the identity fields are `Object*`.

**5e — Move.**

- The 64 types in §2.3 move: descriptors, `catalogState` as `catalog.State`,
  `CatalogRead` as `catalog.Read`, NameKey helpers, the provider registry and
  validation of core kinds.
- A pure copy-on-write overlay moves as `catalog.Candidate`: descriptors,
  names, touched names, removals, default ACLs, with
  `Create`/`Replace`/`Remove`/`lookupName`.
- `CatalogStage` stays in the engine and embeds the `Candidate`. It keeps the
  publication coordination: storage row shapes, index candidates, domain
  validation, materialized replacement, role candidates (cut 2).

Done; see the prompt file's 5e results. Correction: validation of the core
kinds moved only for namespaces, databases, tablespaces, collations,
conversions and foreign objects. Relation, type, extension, operator and
parameter validation need stored-SQL rendering, engine expression nodes or
engine inventories and stay in the engine behind the moved registry.
`RoutineProvider` moved to `expr` so that catalog routine declarations do not
import `functions`.

**Doable because** after the cuts the catalog closure contains no god struct.
39 of 40 methods on these types are already clean (the one dirty method is
handled in 5b). Every sub-step before 5e is an in-package refactor that keeps
the build green.

**Done when** `catalog` imports only `ids`, `expr`, `authorization`,
`sqltype`, `parser` and `sqlstate`, and the engine has no direct access to the
State maps. Optional 5g regrouped `catalog.Relation` fields into value
sub-records (column metadata, constraints, security, publication history);
see the prompt file's 5g results.

### Step 6 — `engine/storage`: physical roots

**Moves.** `rowVersion`, `relationStorage`, deletion roots, logical B-tree and
hash index roots and entries, `indexRowOutput`, `sequenceGenerator`, and
truncate and materialized-view lineage.

**State ownership.** `Engine.storage`, `sequences`, `indexRoots`, `nextRow`
and `nextVersion` become `storage.Store`, mutated only while the caller holds
`Engine.mu`.

**Dependencies.** `catalog`: index outputs reference relation descriptors, and
private rows pin a `catalog.State`. `expr`: hash index bindings hold
expressions.

**Doable because** the layer is 18 types above `catalog`, with no references
back to statements or sessions.

**Stays in the engine.** Write staging (`WriteStage`), DML candidate building
and index maintenance orchestration.

### Step 7 — `engine/visibility`: xids, commit order, snapshots, SSI

**Moves.**

- Visibility registry and xid assignment, plus identity history and horizons.
- Commit sequence and commit timestamps.
- `VisibilityView` and `visibilityData` (snapshot resolution over storage
  roots), and `transactionRoot` (repeatable-read retention).
- The serializable tracker.

**State ownership.** About 14 `Engine` fields (`commitSeq`, `visibility`,
`assignedXID`, `transactionHistory`, commit-timestamp state, `serializable`,
...) become `visibility.Registry`.

**Stays in the engine.** `transactionOwner` (the per-session state machine),
savepoints, the `catalogChanges` journal, prepared transactions and
`StatementScope`.

**Doable because** the layer is 13 types above `storage`, `catalog` and
`lock`. Its blockers today are mainly `Engine` methods over the fields listed
above, which become `Registry` methods, and statement or transaction-owner
entry points, which stay in the engine.

## 6. Later waves

The later waves split the rest of the root package. They follow the method of
§4 and the patterns of §4.1. The first round planned ten packages, picked
in dependency order (steps 8–17); they are done. The second round (linear
item 54, measured at `c154e14fb`) plans the postponed packages and the
seams they need (steps 18–30, see "Steps 18–30" below). `access` was the
first later wave and is done
([README](../internal/engine/access/README.md),
[prompt file](ENGINE-SPLIT-authorization.md)).

In the first round, step 8 and step 9 were independent leaves. Steps 10–13
were the core track: each started with an in-place untangling sub-step, and
each move needed the previous core move. Feature steps 14–17 ran Part A
early and Part B after step 13.

```text
 8 settings ──▶ 9 pgcatalog
10 plan ──▶ 11 exec ──▶ 12 analyze ──▶ 13 session ──▶ Part B of 14–17
in place now: 10a EvalContext, 12a binding, 12b binder fields, 13a/13b slots
Part A now:   14 routine ──▶ 15 typedef ──▶ 16 extension;  Tier C sequence ──▶ 17 sequence
```

### Step 8 — `engine/settings`: setting definitions

**Moves.** `SettingDefinition`, `SettingType`, `SettingAccess`, `SettingUnit`
and their constants, `settingValue`, parsing, formatting, unit conversion and
validation (`settings.go`), the builtin table `builtinSettings`, the encoding
and locale normalizers (`settings_format_encoding.go`), `parseSearchPath`
(`settings_search_path.go`) and the client-min-messages definition/normalizer
(`settings_notices.go`). The latter two were relocated in place in 8a.
Names lose their stutter in the move (`settings.Definition`, `settings.Type`,
`settings.Value`). **Moved in 8b**; see the
[package README](../internal/engine/settings/README.md).

**In-place port (8a), done.** `Engine.settings`
holds one immutable `settingsRegistry`, built once by a pure constructor taking
`trackCommitTimestamps` instead of reading `e.visibility`. `Lookup`,
`Definitions` and `Order` replace the former fields/methods. Exact typed defaults
in schema-path, prepared-sequence and session initialization consumers use
`SettingDefinition.BootValue`; borrowed maps/slices must remain read-only.
`parameterProvider` borrows a pointer to the same Engine-owned registry value.
Root proof includes selected native/pinned checks, full quickcheck and a full
racecheck pass with `GOMAXPROCS=8`; prior failures and measured startup-target
misses remain recorded. See the
[8a evidence/status](ENGINE-SPLIT-settings.md#status).

**Package (8b), done.** `settings` holds the definitions, registry,
normalizers and search-path parser and imports only `sqlstate`, `sqltype` and
the standard library. `Engine.settings` is a `settings.Registry`;
`RegistrationError` stays in the engine.

**Aliases removed (8c), done.** The engine, the root driver and
`cmd/stuntgres` import `settings` directly; `engine.NewWithSettings` takes
`[]settings.Definition`. The root `SettingDefinition` mirror types stay the
public API.

**Stays in the engine** until step 13: per-session storage
(`sessionSettings`, the SET LOCAL action stack, savepoint `settingsMark`),
SET/RESET/SHOW execution, `current_setting`/`set_config`, command admission,
`sessionPresentation`, RESET ALL and timeout publication. They all read
`Session`, `Operation` or the transaction owner.

**Doable because** the definition code imports only `sqltype` and `sqlstate`
already; about 700 of its 950 lines reach no god struct.

**Done when** `settings` imports only `sqltype`, `sqlstate` and the standard
library, `Engine` holds one registry, and the root driver and `cmd/stuntgres`
name `settings.` directly. Prompt file:
[ENGINE-SPLIT-settings.md](ENGINE-SPLIT-settings.md).

### Step 9 — `engine/pgcatalog`: system relations

**Moves.** The row producers of the `pg_*` relations and the two
`information_schema` views (`pg_*.go`, `catalog_sources.go`,
`extension_sources.go`, `available_extension_sources.go`,
`timezone_sources.go`, `prepared_source.go`, `table_view_summary.go`,
`table_privileges.go`, `data_type_privileges.go`), their static column specs
(`initializeCatalogSources` and the smaller spec tables) and their pure helpers
(`pgLockRows`, `pgConstraintBuilder`, `pgTypeFacts`, ...). `pgDependPinned`
stays in the engine (9b): it reads unpublished definition state.

**In-place port (9a), package move (9b) and alias removal (9c):
implemented and verified.** The current layout is described in the
[`pgcatalog` README](../internal/engine/pgcatalog/README.md); the 9a bullets
below use the engine-local names of that time.

- Non-producer publication/index history now lives in
  `catalog_relation_history.go`, sequence predicate admission in
  `query_pg_sequences_admission.go`, and shared expression/plan reference
  walking in `expression_references.go`. `table_view_summary.go` owns rows,
  not publication history.
- One seven-field `catalogSourceInput` (`Context`, `Catalog`, `Roles`, `Owner`,
  `Ref`, `Source`, `Runtime`) feeds free rows-only producers:
  `func(catalogSourceInput) ([][]sqltype.Value, error)`. The engine's
  `captureCatalogSourceRows` alone wraps rows into `querySource`.
- `catalog_source_producers.go` holds one process-initialized, immutable,
  OID-sorted registry with binary-search lookup, replacing both selection
  chains. All five foreign metadata relations use it with fresh paired
  authorization; only locks and lazy sequences retain explicit handlers.
- Provenance compares the same Owner/Domain/Principals/ACLAuthority pointers,
  not equal names or numbers. `pgClassACL` and hidden regrole/ACL-item output
  formatters use `*catalog.Owner` and immutable roles instead of Engine.
- The per-call `engineCatalogRuntime` adapter is demand-only, not eager
  capture or a service locator. Value operations expose no Engine, scope,
  authorization view or binding. It supplies actual cursors, coherent bounded
  settings, actual prepared XID/published-owner facts, timezone/database/TEMP
  facts, error-bearing privilege checks, expression/query walks and definition
  presentation without changing capture/unlock or cancellation boundaries.
- Pure process-initialized ordered `catalog_source_specs.go` data is separate
  from engine type resolution and descriptor registration. Registration order,
  metadata and actual row/array links remain the contract; startup impact is
  pending measurement, not assumed unchanged.

**Stays in the engine.** The `querySource` wrapping and view-source
authorization; `pg_locks` capture, which releases and retakes `Engine.mu`;
`pg_sequences`, whose lazy completion acquires sequence locks through the
statement scope; `pg_sequence`'s transaction-journal overlay; descriptor
registration during catalog seeding.

**Doable because** 30 producers are dirty only through `*querySource`, and
the rest only through provenance checks that `catalog.Owner` can answer.
9b found more reach than this: expression trees, the foreign-key binder, type
and ACL validation, output formatters, pin checks and the `routine` operator
inventory. Ten more value-taking `Runtime` methods cover them.

**Done when** `pgcatalog` imports only `catalog`, `authorization`, `lock`,
`settings`, `expr`, `ids`, `sqltype`, `sqlstate`, `parser` and the standard
library (9b: `parser` because descriptors store parser enums; `settings` is not
needed), and
the engine dispatches system relations through one registry lookup. Prompt
file: [ENGINE-SPLIT-pgcatalog.md](ENGINE-SPLIT-pgcatalog.md).

### Step 10 — `engine/plan`: plan IR and expression nodes

**Moves.** `LogicalNode`, `BoundQuery`, `querySchema`, the 20 plan node types
(`sourceNode` … `windowNode`) and their records (`sortKey`, `queryGrouping`,
`aggregateCall`, `windowCall`, `boundWindowSpec`, `searchRecipe`,
`cycleRecipe`, `cteDefinition`, `functionCorrelation`, `querySetTemplate`,
`queryRowMark`), and the concrete engine expression nodes (column, parameter,
literal, coercion, arithmetic, comparison, CASE, row, field, array, subscript,
subquery, aggregate, window, returning, MERGE action, domain value, ...). The
Tier C inquiry nodes are excluded: they move to `functions` in step 3.

**In-place port.**

- **10a — split `EvalContext`** into three named groups: the frame (row,
  frame and scope numbers, outer link, presence, query links, merge and
  domain values), the statement runtime (cancellation context, parameters,
  input and output environments, quote captures, plan flags, prepared check
  caches, statement scope, catalog) and routine admission (the `routine*`
  fields and `domainPhase`). This is a pure regrouping; value copies keep
  their meaning.
- **10b — cuts.** `cteDefinition.mutation *binding` becomes an opaque payload
  (rule 7 of §3). Node `Eval` methods stop recovering `*EvalContext`: nodes
  that read only cancellation and environments use `expr.Context`; nodes that
  read frame state or run subqueries use a node runtime capability (pattern
  3), which `*EvalContext` implements. The shared prologue
  (`evaluationContext`: nil check, domain-check wiring, cancellation) keeps its
  order.

**Stays in the engine** until step 11: execution (`openQueryNode` and the node
openers), `queryState`, `querySource`, `EvalContext` itself, and the central
switches that list node types (deparse, remap, retag, walk; they name `plan.`
types after the move).

**Doable because** every plan node type and every expression node type is
already clean as a type (§2.6); only `cteDefinition.mutation` and the `Eval`
recoveries reach a god struct.

**Done when** `plan` imports only `expr`, `functions`, `catalog`, `lock`,
`ids`, `parser`, `sqltype`, `sqlstate` and the standard library, and no node
`Eval` names `*EvalContext`. Prompt file:
[ENGINE-SPLIT-plan.md](ENGINE-SPLIT-plan.md).

### Step 11 — `engine/exec`: the executor

**Moves.** `queryState`, `querySource`, `indexedStage`, `finalRows`, the
`openQueryNode` switch and the node openers (joins, hashing, sets, distinct,
limit, recursion, CTE scans, windows, aggregation, function sources, row
locks), the `RowCursor` implementations, and `EvalContext` as `exec.Context`
(it implements `expr.Context`, the step-10 node runtime and the `functions`
capabilities).

**In-place port.**

- **11a — refine** (pattern 4) after step 10. Done: the query files mix
  runtime code with binder methods, admission/capture and preflight, which
  stay; the runtime reaches the engine from 17 functions plus 36
  capability bodies (record in the exec prompt).
- **11b — host seam.** Done: an `execHost` interface, implemented by the
  root adapter `statementHost`, replaces the `*StatementScope` back-pointers
  in `querySource`, `queryAuthority`, `pgSequencesSource` and the evaluation
  context: environment and presentation, run-time row locks, foreign owners
  and `pg_sequences` completion. The capability bodies that read engine state
  (Tier C and a few `expr.Context` and `plan.Runtime` methods) moved onto the
  adapter behind forwarders. The two preparation callbacks go through one
  stateless `execPreparation` value instead, because domain checks also run
  in contexts without a statement scope.

**Stays in the engine.** Statement dispatch (`execute.go`,
`execute_bound.go`), query admission and capture, preflight and the join
legality checks (step 12 decides), the DML executors (postponed DML
package), CTE mutation execution, CREATE TABLE AS, SQL cursors and protocol
portals (step 13).

**Doable because** after step 10 the plan IR has a home, and the executor's
coupling to the statement scope is one back-pointer per runtime record.

**Done when** `exec` imports nothing above L7 and the root package no longer
opens plan nodes. Prompt file: [ENGINE-SPLIT-exec.md](ENGINE-SPLIT-exec.md).

### Step 12 — `engine/analyze`: binding

**Moves.** `expressionBinder` and its 348 methods, `binding`, `describe` /
`describeWithRule`, the query, FROM, CTE, aggregate, window and DML-target
binding files, function and operator call resolution (`function*.go`,
`operator_binding.go`, `operator_routine_binding.go`) and the binder halves of
the type features (composite, enum and range binding).

**In-place port.**

- **12a — split `binding` per statement kind.** A header keeps what every
  kind uses (target relation and ref, targets, dependencies, parameters,
  RETURNING, query, query sources, view write). Payloads take the rest:
  INSERT, DELETE and CREATE TABLE payloads (held by value) next to the
  existing UPDATE and MERGE ones, plus three shared value groups:
  `viewCapture` (11 maps), `preparedCaptures` (temporal, composite, sequence
  and regtype constants) and `resolverInputs` (10 statement input resolvers).
  Done; the final layout is in the prompt file.
- **12b — group `expressionBinder` fields** into target, parameter, consumer,
  clause, grouping and window, query scope and CTE state, reusing the 12a
  capture types.
  Done; the final layout is in the prompt file.
- **12c — refine, then host seam** (pattern 4). Done: an `analyzeHost`
  interface (37 methods), implemented by the root adapter `bindingHost`
  (engine and operation), replaces `expressionBinder.engine` and `op`;
  `describe` takes the host and the binder-side statement-kind binders became
  functions on it. `analyze` imports `exec` only for scope-free constant
  folding; domain literal input goes through the host and preflight stays in
  the engine.
- **12d — move** (several sessions). Go allows no methods on an imported
  type, so the binder moves in one final session (12d.6). Before it: the
  binding types move (12d.1), the remaining engine edges are cut in place
  (12d.2) and each area is prepared in place (12d.3–12d.5). The refine and
  its decisions (authority, opaque slots, feature layering) are in the
  prompt file.
- **12e — remove the aliases.** Done: the engine names the `analyze` types
  directly and the driver imports `analyze` for `ParameterInfo` (§7
  decision 11).

**Stays in the engine** until their features move: rule rewriting, view and
policy capture consumers, materialized snapshots, and statement dispatch.

**Doable because** 12a and 12b make the binder's state explicit, and after
step 11 the binder's outputs (plan IR) and its constant evaluation have homes.

**Done when** `analyze` imports nothing above L8 (except the feature Part A
content, rule 6), `binding` has no
kind-specific header fields, and the root package calls one describe entry
point. Prompt file: [ENGINE-SPLIT-analyze.md](ENGINE-SPLIT-analyze.md).

### Step 13 — `engine/session`: sessions and statement scope

**Moves** (refined in 13c). `Session`, `Operation`, the transaction owner's
state, savepoint marks, the `catalogChanges` journal, timeouts, notices,
`ProtocolCycle`, the notification records, the per-session settings store,
the transaction and session value types, and a small `StatementLease` that
replaces the scope back-references in timeouts and COPY admission.

**In-place port.**

- **13a — scope slots.** Every single-feature `StatementScope` field moves
  into a slot (pattern 2): protocol, prepared rebind, inquiry snapshot,
  presentation caches, TRUNCATE, foreign-key actions, view creation,
  materialized refresh, inheritance, index and exclusion, partition, foreign
  tables and domains. About 70 fields leave the header.
- **13b — operation, journal and stage slots.** `Operation`'s Restore replay
  flags become one slot. The journal's and `CatalogStage`'s feature fields
  (index candidates and fences, partition read fences, TRUNCATE fences,
  materialized sources, domain validation, enum generations, typed-table
  observation) become feature slots, and the journal fences one fence
  interface. The transaction owner's FDW participants become a slot.
- **13c — refine** after steps 11 and 12. Done: most of the session layer
  is orchestration (57 `Engine` methods install changes under `Engine.mu`),
  only 16 of the 320 scope methods touch nothing but a possible core, and
  `analyze` imports the feature packages, so `session` must not import it.
  State moves; orchestration and the scope stay. 13d runs in six sessions
  (journal, statement lease, engine edges, root payloads, file splits and
  exports, move); details in the prompt file.

**Stays in the engine.** Statement dispatch (`executeStatement`), the whole
`StatementScope`, the cross-subsystem orchestration (begin, end, publication,
2PC install, prepared re-analysis), SQL cursors, protocol portals, the
prepared SQL registry, `StatementDescription`, the notification listener,
the 2PC records, and the feature handlers until Part B of their feature
steps. Part B handlers reach the scope through host interfaces on
`statementHost` (§7 decision 8).

**Doable because** 13a and 13b remove every feature type from the shared
structs, so `session` imports no feature package.

**Done when** `session` imports no feature package, not `analyze`, and
nothing above L9. Prompt file: [ENGINE-SPLIT-session.md](ENGINE-SPLIT-session.md).

### Step 14 — `engine/routine`: builtin routines and operators

**Part A (14a, 14b).** The builtin routine and operator inventories and
metadata (`routine_inventory.go`, `operator_inventory.go`,
`operator_metadata.go`, `operator_routines.go`, about 3.2k lines, all clean),
builtin declaration lookup, operator descriptor validation, operator DDL
option validation, operator dump formatting and routine body edges. Builtin
seeding becomes pure constructors that the engine installs.

**Part B (14c, 14d)** after step 13: CREATE/ALTER/DROP OPERATOR staging and
the SQL routine DDL that `PG18-ROUTINE-CORE` adds (CREATE FUNCTION staging),
behind `routine.DDLHost` on the root `schemaHost` (14c refine, moved by
14d; `ddlHost` since 20b). The operator and SQL routine dumps stay in the
root until the dump graph has a seam (postponed `control`).

**Stays elsewhere.** Call resolution belongs to `analyze` (step 12), routine
call admission (`routine_admission.go`, `routine_body_admission.go`,
`routine_program.go`) to `exec` (step 11), SQL routine definitions and frames
to `proc` (L1).

**Needs** `PG18-ROUTINE-CORE` merged (it touches the same files). Prompt
file: [ENGINE-SPLIT-routine.md](ENGINE-SPLIT-routine.md).

### Step 15 — `engine/typedef`: user-defined types

**Part A (15a, 15b).** Domains, enums, composites, ranges, collations and
conversions: dump and remap helpers (`domain_remap.go`, `collation_remap.go`,
`conversion_dump.go`, ...), validation (`collation_validation.go`), builtin
conversion tables, domain-alter discovery, typed-table inheritance planning
and the domain check program records. About 2.5k clean lines were measured;
15b moved 2.1k. The collation, conversion and composite dump statements and
type-declaration rendering wait for a dump seam (see the prompt file).

**Part B (15c, 15d)** after step 13: the stage handlers for CREATE/ALTER/DROP
COLLATION and CONVERSION, CREATE/DROP DOMAIN, CREATE TYPE AS ENUM and
CREATE/DROP TYPE (composite), plus the ALTER ENUM and ALTER DOMAIN staging
cores, move behind `typedef.StageHost` (15c refine; done in 15d). The
executors (ALTER DOMAIN validation runs, ALTER TYPE and typed-table
mutation, ALTER TABLE OF), TEMP collation lifetime and type-generation
reownership stay in the root.

**Stays elsewhere.** Domain check evaluation (`EvalContext` methods) goes to
`exec`; composite, enum and range binding to `analyze`; `StatementDescription`
(in `statement_description.go`, part of the public facade) to `analyze`.

**Needs** 14b (operators and types reference each other; 14b settles the
cut). Prompt file: [ENGINE-SPLIT-typedef.md](ENGINE-SPLIT-typedef.md).

### Step 16 — `engine/extension`: installed extensions

**Part A (16a, 16b).** Extension provider records and control data
(cube, fuzzystrmatch, intagg, intarray, pgcrypto, unaccent, uuid-ossp), member
validation, membership and owner metadata decoding, extension remap.

**Part B (16c, 16d)** after step 13: CREATE/DROP EXTENSION, provider staging,
owner import. Until then the staging lives on engine installers that embed
the stock providers (16b): Go cannot keep engine methods on moved types.
Refined in 16c: staging becomes a provider method behind `extension.Stage`
(the installers go), the decisions of CREATE, ALTER EXTENSION UPDATE, DROP
EXTENSION and the owner import move behind `extension.Host`, and one root
adapter `extensionHost` implements both; dispatch, `Engine.mu`, the removal
pipeline and publication stay in the root. 16d runs in two sessions
(staging, handlers); details in the prompt file.

**Stays elsewhere.** The installed-function expression wrappers and the
`pg_extension_update_paths` node stay with the engine's node code and move
with `plan` (their catalog identity is engine state, see the `expr` README).
Generic procedure validation (`procedureProvider`: builtin, SQL, foreign and
conversion routines) is routine code, split from the extension validator in
16a. `database_grant.go` (formerly `extension_database.go`) is database
GRANT, not extension code.

**Needs** 15b (`intarray` validation goes through type validation). Prompt
file: [ENGINE-SPLIT-extension.md](ENGINE-SPLIT-extension.md).

### Step 17 — `engine/sequence`: sequence objects

**Part A (17a, 17b).** Sequence option binding and limits, the
physical-session memory, the prepared-literal records, sequence dump
formatting and the sequence and identity diagnostics, now in
[`internal/engine/sequence`](../internal/engine/sequence/README.md) (17b). 17a
renamed the files whose names misled (now `temporary_session.go`,
`temporary_preservation.go`, `owned_roles*.go` and `owner_handles.go`).
Generic relation diagnostics (`tableKind`), the relation-name chooser and
regclass input stayed in the engine: they are not sequence code.

**Part B (17c, 17d)** after step 13: CREATE/ALTER/DROP SEQUENCE, identity
columns, serial columns and sequence comments. Refined in 17c: their
decisions move (descriptor construction, option and OWNED BY checks, the
plan-stamp rule, prepared-literal validity, call semantics, the stage slot);
`Engine.mu`, lock waits, authorization refresh, the journal and stage
install, and the identity and serial staging on `CatalogStage` stay in the
root, so no `sequence.Host` is needed. 17d is one session; details in the
prompt file.

**Stays elsewhere.** The generator (`storage.SequenceGenerator`) stays in
`storage`; `nextval`/`currval`/`setval` move to `functions` in Tier C.

**Needs** Tier C sequence calls (step 3). Prompt file:
[ENGINE-SPLIT-sequence.md](ENGINE-SPLIT-sequence.md).

### Steps 18–30 — the postponed packages and their seams

Planned in the second round (linear item 54) and measured at `c154e14fb`.
The first round postponed `index`, `partition`, `inheritance`, `table`,
`view`, `fdw`, `dml` and `control`. Measuring them showed:

- **Seams first.** Clean code of every postponed area is stuck on five
  shared pieces that have no package yet (§2.7): the dump graph, the COPY
  codecs, the DDL contract, the reg* types and the stored-SQL presentation.
  They become steps 18–22.
- **`inheritance` folds into `table`** (step 24; §7 decision 14). The two
  areas use each other in both directions (69 dirty and 29 clean
  declarations from inheritance into table, 52 and 10 back),
  `dumpRelation`'s methods span both, and ALTER recursion re-enters
  `alterTableLocked`. Inheritance DML expansion goes to `dml`; the clean
  query-expansion helpers move with `table` while the scope parts stay
  with the root's query admission; the pure hierarchy helpers go to
  `catalog` (5i).
- **Role and ACL DDL get `role`** (step 28). **Schema-level DDL gets no
  package of its own:** `ddl` (step 20) owns CREATE/ALTER/DROP SCHEMA and
  CREATE TABLESPACE; object identity goes to `regobj`; definition identity,
  namespace children and tablespace validation to `catalog`; SET SCHEMA
  (`relation_set_schema.go`) and RENAME (`rename.go`) to `table`. Search path
  resolution, COMMENT staging, the DROP pipeline (`dropProvider`) and the
  dispatch stay in the root.
- **Upper features.** The table family, views and DML render stored SQL,
  so they sit above `analyze` (rule 6). `dml` sits above all of them.

```text
18 dump ──▶ 18c providers ───────────────────────────────────────▶ 30 control
19 copyfmt ──────────────────────────────────────────────────▶ 29 dml
20 ddl ──▶ the hosts of 23–28 (Part B)
21 regobj ──▶ 22 stored ──▶ 23 index ──▶ 24 table ──▶ 25 partition ──▶ 29 dml
                     └──────────────────────▶ 27 view ──────────────────▲
26 fdw: Part A now, foreign tables after 24d ─────────────────────────────┘
28 role: after 21b and B6
now:        F6, 5i, 5j, 10e, 11e ─▶ 11f, 12f, 13f, B6
after 18b:  14e routine dumps, 15e type dumps
```

Every step uses the same sub-step shape: `a` in place, `b` create the
package and move the measured clean set, `c` refine the host (docs only,
pattern 4), `d` move the handler decisions. The seam packages have no
handlers, so no `c`/`d`. Sizes are clean lines at `c154e14fb`.

### Step 18 — `engine/dump`: the dump graph

**Moves (18b, about 0.4k lines).** The dump-graph records now spread over
`view_dump.go`, `domain_dump.go`, `policy_dump.go` and `control.go`:
`dumpPhase` (16 ordered constants) as `dump.Phase`, `dumpEventKey` as `Event`,
the event graph as `Graph`, the topological order (`viewDumpReady`,
`viewDumpOrder`) as `Order`, `dumpDeclarationEvents` and `dumpDeclarationIDs`,
`dumpDomain` as `Statement`, `dumpPolicyGroup` as `Group`, the graph
operations `addDumpDependency` and `dumpEventDependsOn`, and the pure helpers
`commentLiteral` and `restoreScalar`. Two capabilities: `Resolver` (the
descriptor-kind predecessor; the root's `dumpPredecessor` implements it) and
`Presenter` (type and column declarations, literals, routine identity; the
root implements it, later `regobj`).

**In place.** 18a renames `view_dump.go` to `dump_graph.go` and
`viewDumpOrder` to `dumpOrder`, and turns `preflightACLItemDump` into a
function over a captured snapshot (`validateTemporaryCollationDump` builds a
`CatalogStage` and stays). 18c applies pattern 5: Dump's descriptor-kind
switch (#1) and phase switch (#2) become ordered Collect and Emit tables that
the root composes in today's order; feature packages later export their
entries.

**Stays in the root** until step 30: `Dump` itself (its `Engine.mu` window,
snapshot capture and emission sequence) and `dumpPredecessor`.
`dumpRelation` moves with `table`.

**Unblocks** the collation and conversion dumps at once, and the operator,
SQL routine, composite and domain dumps with `Presenter` (14e, 15e).

**Done when** `dump` imports only `ids`, `catalog`, `parser`, `expr`,
`functions` (the Restore regexp and POSIX operator rules), `sqltype` and the
standard library, and Dump output is byte-identical. 18b settled the
`plan` question: no dump producer renders stored literals yet, so
`Presenter` has no `Literal` method and `dump` stays at L3.5. Prompt file:
[ENGINE-SPLIT-dump.md](ENGINE-SPLIT-dump.md).

### Step 19 — `engine/copyfmt`: COPY codecs

**Moves (19b, about 0.9k lines).** COPY options (`copy_options.go`), FORCE
binding (`copy_force.go`), text and CSV encoding and decoding, binary
framing and the line diagnostics. 19a first splits `copyCursor` and
`copyBinaryCursor` into a codec part (reader, options, buffers) and a DML
part (scope, binding, `Next`); after it the codec is 913 clean lines in
five files. 19b moved them (1.05k lines with `ResolveColumns`, the
reader accessors and docs). The name avoids `copy`, which would shadow the
builtin.

**Stays.** The COPY protocol and reservation (`copy_admission.go`,
`protocol_copy*.go`, `CopyBoundary`, `StatementScope.Copy`) stay in the
root; COPY execution (`bindCopyLocked`, `copyTableRows`, the insertion
state, including the `copyInsertion*` batching records) goes to `dml`.

**Done when** `copyfmt` imports only `catalog`, `parser`, `sqltype`,
`sqlstate` and the standard library, with no new per-field allocation.
Prompt file: [ENGINE-SPLIT-copyfmt.md](ENGINE-SPLIT-copyfmt.md).

### Step 20 — `engine/ddl`: one DDL contract, and schema-level DDL

**Contract (20a).** One opaque `ddl.Authority`, one `ddl.Stage` (`Create`,
`Replace`, `UpdateDescriptor`, `Get`, `LookupName`) and a common `ddl.Host`:
authority views and recapture, creation namespaces and CREATE checks,
function and type lookups, dependency text and paths, OID and revision
allocation, notices and the context. `routine.DDLHost`, `typedef.StageHost`
and the `extension` host and stage embed them and keep only their
package-specific methods. The prompt file tabulates what differs today
(`ResolveType`, `DependencyObject`, `CreationNamespace`, `extension`'s
`Check`/`Effective` authority) and settles each. Done: `ddl` holds
`Authority`, `Stage`, `Allocator`, `TypeStaging`, `Host` and the `Check`,
`ObjectRef` and `Notice` helpers; the three packages embed them.

**One adapter (20b).** One root staging adapter, `ddlHost`, replaces
`schemaHost` and `typedefHost`. `extensionHost` stays a separate type (it
captures one view and wraps the stage, 16c) and reuses the shared root
functions. This answers the typedef note: one `Authority` and `Stage`, not
a third copy. Done: `ddlHost` implements `ddl.Host` (`ddl_host.go`),
`routine.DDLHost` (`routine_host.go`) and `typedef.StageHost`
(`typedef_host.go`).

**Schema-level DDL (20c, 20d).** The decisions of CREATE/ALTER/DROP SCHEMA
and CREATE TABLESPACE (about 0.3k lines) move behind the shared host.
Refined (20c): `ddl.SchemaHost` is `ddl.Host` plus five methods on
`ddlHost` (one table element, the schema dependents, one removal, a cascade
notice, a relation diagnostic); CREATE TABLESPACE takes only the
`access.View`; the seed comments go to `catalog`. Done (20d): `ddl.StageCreateSchema`,
`StageAlterSchema`, `StageDropSchema` and `CreateTablespace`;
`catalog.NamespaceCommentBaseline` and `BuiltinTypeComment`.

**Stays in the root.** Search path resolution (`schema_path.go`), COMMENT
staging, the schema statement dispatch and `dropProvider`. The first Part B
that stages dependents (24c) adds a `ddl` removal interface that
`dropProvider` implements (rule 7), instead of passing the root type.

**Done when** `routine`, `typedef` and `extension` declare no `Authority` or
`Stage` of their own, the root has one staging adapter (`ddlHost`) besides
`extensionHost`, and `ddl` imports nothing above `session`. Prompt file:
[ENGINE-SPLIT-ddl.md](ENGINE-SPLIT-ddl.md).

### Step 21 — `engine/regobj`: catalog objects and typed values as SQL text

**Moves.** The reg* types (`regclass`, `regproc`, `regprocedure`, `regoper`,
`regoperator`, `regtype`, `regrole`, `regnamespace`, the text-search and
collation types): spellings, catalog-backed output, dump spellings,
literal SQL and reference walkers for stored values, remap of stored reg*
datums, routine lookup by signature (`selectRoutine`,
`routineIdentityByOID`) and output capture into `exec.FinalRows`. With
them: type-declaration and literal rendering (`catalogTypeDeclaration*`,
`catalogColumnDeclaration`, `catalogLiteral*`, now in `domain_dump.go`;
they reach no renderer), the aclitem I/O, and object identity
(`pg_identify_object`, `pg_describe_object`, `pg_get_object_address`:
`object_identity.go` and the clean part of `object_address_lookup.go`).
21b moves the clean set (about 2.5k lines; it reads rule identity through the
5j accessors); 21c moves the captured input resolvers (about 0.7k lines),
retyped to `access.View`, `access.Environment`, a definition reader and a
notice function instead of `AuthorizationView`.

**In place (21a).** `sequence_output.go` becomes `regclass_output.go`;
`catalogRegclassOutput` and `remapRegclassDatum` move into regclass files;
the search-path visibility helpers of `visibility_inquiry.go` and
`visibility_metadata.go` go to `namespace_visibility.go`; the
`scalarRenderer.record*` methods are split off (they stay with the
renderer); `pgIdentifier` goes to `catalog` (5i, or here if 5i has not run);
the dead `routineContainerLiteral` is deleted.

**Stays in the root.** `regtypeAnalysisLocked`, `regclassLocked`, the
`Engine.capture*Input` constructors, the `StatementScope.Format*` methods
behind `exec.Host`, and the `analyze.Host` resolver methods, which become
forwarders.

**Done when** `regobj` imports nothing above `analyze` and `exec`, and no
root file declares reg* spelling, persistence, literal or identity code.
Prompt file: [ENGINE-SPLIT-regobj.md](ENGINE-SPLIT-regobj.md).

### Step 22 — `engine/stored`: stored-SQL definitions and their presentation

`stored` holds the frozen definitions as well as their rendering, so 22a
chose it over `deparse` (§7 decision 12).

**Moves (22b, about 10.2k lines; broad).** The closed presentation set of
§2.7: `pgRenderer` (`deparse_renderer.go`, `deparse_rendering.go`),
`scalarRenderer` (`scalar_renderer.go`, methods in ten files),
`viewPresentation` and `viewRenderer` (`view_deparse.go`,
`view_render_sources.go`, and the renderer methods filed under query, typedef,
routine and function glue), the stored view payload (`viewDefinition`,
`viewLeaf`, `viewOf`) and the policy payload (`policyExpression`), retag
(`expression_retag.go`) with the volatility and property wrappers it alone
uses, the generic expression remap (`expression_remap.go`), frozen-presentation
remap, `expression_references.go`, `pgDependRefs` and the installed-node
validators. 22c adds the rule payload and its rendering, the rule tree mapper
and binding map, and the payload remaps (`rule_remap.go`, `policy_remap.go`,
`remapLikeExpression`, `remapPartitionDescriptor`; about 2.2k lines; `dml`
needs `ruleAction`, so they cannot wait for `view`) and the remap protocol:
`reownCatalogExpressions` becomes `stored.ReownExpressions` over a remap
value, so its non-Restore callers (type generations, owned roles, extension
owner import) use the same function. 22d removes the aliases (broad).

**In place (22a, done).** The `partitionIndexCoverageComplete` cut
(pattern 6): it reads through `indexCoverageReader` instead of
`&dropProvider{}`, which made 32 declarations (about 2.2k lines of dump,
index and partition code, and `relationProvider.Validate`) clean.
`freezeQueryPresentation` became a function of its captured inputs (rule OID
passed in); `Engine.freezeViewDefinitionLocked` stays as the wrapper that
allocates. `viewDefinition` got read accessors for its readers outside the
set (`Sources`, `Dependencies`, `Invoker`, `RuleOID`, `References`,
`Literals`). Renames: `scalarRenderer` out of `default.go` into
`scalar_renderer.go`; `view_remap.go` to `expression_remap.go`, with its two
`Engine` methods in `catalog_expression_reown.go` (was
`view_catalog_remap.go`); `query_cte_view.go`, `query_values_view.go`,
`string_view_sources.go` and `composite_render.go` to `view_deparse_*.go`.

**Stays in the root.** The host bodies that call into it:
`analyze.Host`'s retag and stored-view methods, `exec.Host`'s
`DeparseInquiry`, and `pgcatalog.Runtime`'s definition and reference
methods. They shrink to forwarders, because those interfaces' packages sit
below `stored`.

22b needs `regobj` (21b), F6, `dump` 18b (`dump.CommentLiteral`) and analyze
12f (the query-graph helpers it reads).

**Done when** `stored` imports nothing above `analyze` (plus `regobj`), and
no root file renders stored SQL. Prompt file:
[ENGINE-SPLIT-stored.md](ENGINE-SPLIT-stored.md).

### Step 23 — `engine/index`: indexes, unique and exclusion constraints

**Part A.** 23a, in place:
- Renames: the `unique_dml.go` records to `index_unique_binding.go`;
  `partition_index_attach.go`, `partition_index_drop.go` and
  `partition_unique.go` to `index_partition_*.go`;
  `inheritance_using_index.go` to `using_index_prepare.go`;
  `unique_type.go` to `index_alter_type.go`.
- State ownership (pattern 1): `Engine.indexBuildWaits` becomes an
  `indexWaitRegistry`; `exclusionPending` and `exclusionGeneration` become an
  `exclusionRegistry`. Every `exclusionGeneration` increment (six sites)
  keeps its place and meaning.
- Moves into index files: `columnTransformation` (the ALTER TYPE input of
  index evolution), the partition replay-shape predicates
  (`partitionReplayUsingIndex`, `partitionReplayVirtualUniqueRoot`,
  `partitionReplaySeparateConstraint`), `partitionRowHistory`; `rowByID`
  goes to `storage`.
- Cuts: the unique and exclusion validators take `access.View` and
  `access.Environment` (nil for trusted publication) instead of
  `*AuthorizationView`; `prepareIndexPublication` becomes a function over a
  `catalog.Read` and a diagnostic-name capability; the stage helpers take a
  narrow stage interface; `transportIndexLayouts` splits into a root
  capture under the lock and a pure transport.

23b moves the clean set: ordering, keys and programs, build transport,
publication evolution, snapshots, write candidates and insertion bindings,
unique and exclusion records and validators, `USING INDEX` records.

**Part B.** 23c refines `index.Host` (runs under `Engine.mu`, never waits:
authority, stage, OIDs, key/predicate/expression binding by the root,
operator classes, evaluation contexts, storage reads) and `index.WriteHost`
(DML side: unique parents, key pins that may wait, the exclusion registry).
23d moves the CREATE INDEX, REINDEX, ALTER/DROP INDEX and constraint-index
decisions.

**Stays in the root.** The orchestration of `executeCreateIndex`,
`executeConcurrentIndex` and `executeIndexMaintenance`; the writer and
snapshot waits; the registry instances; savepoint marks; the Finish order;
the publication call sites. Needs 22b, 5i, 10e and 11e (the
expression-preparation seam). Prompt file:
[ENGINE-SPLIT-index.md](ENGINE-SPLIT-index.md).

### Step 24 — `engine/table`: tables, constraints and inheritance

**Part A.** 24a, in place:
- Renames that make the boundary visible: `not_null.go` (which holds
  `alterTableLocked`) to `alter_table.go`; `temporary_table.go` to
  `create_table.go`; `table.go` to `drop_table.go`; `schemaTableLocked`
  out of `schema.go`; `prepareAlterTableActions` out of
  `alter_tablespace.go`; `inheritance_dml*.go` to `mutation_targets.go`
  (for `dml`).
- Slot: `CatalogStage.likeSources` becomes a table stage slot.
  (`foreignActionAuth` belongs to FK actions and goes with `dml`.)
- Cuts: `inheritanceAlterState` splits into a pure plan record and a root
  holder of the stage and journal; `preparedDrop.scope` becomes an opaque
  statement token (it is only an identity check); the `dropProvider`
  dependency queries become functions over a reader (pattern 6).

24b moves the clean set (broad, about 5.6k lines): NOT NULL candidates
and physical NOT NULL syntax, generated columns, CHECK ordering and
validation, FK DDL and probe records, column drop metadata, ALTER TYPE
checks, the table dump record `dumpRelation` with its emitters, the
inheritance recipes, retired constraints, guards and Restore repair, and
the clean inheritance query-expansion helpers.

**Part B.** 24c refines `table.Host`: stage and descriptor creation,
authority (from `ddl`), binding done by the root (columns, checks,
generated columns, defaults, LIKE), type resolution, identity and serial
staging, dependency staging (the `ddl` removal interface), row and storage
access for rewrites, notices. 24d moves the CREATE TABLE and ALTER TABLE
candidate decisions, the inheritance recursion decisions and the identity
and serial staging (sequence notes); the skeleton of `alterTableLocked`
(its `Engine.mu` windows and pass loop) stays in the root and calls them.

**Not table code.** FK enforcement at write time and FK actions
(`foreign_key_writes.go`, `foreign_action_*.go`) go to `dml`; FK DDL,
validation scans and dumps stay here. 24b needs 23b, `dump` 18b and access B6
(24a needs only 5i). Prompt file:
[ENGINE-SPLIT-table.md](ENGINE-SPLIT-table.md).

### Step 25 — `engine/partition`: partitioned tables

**Part A.** 25a, in place: `partitionProof` carries a context and input
environment instead of the scope; recipe evaluation uses the 11e
expression seam; `partitionReadFence` and `CatalogStage.partitionReads`
are renamed `storageReadFence` and `storageReads` (they are generic
storage-read fences); `partition_scope.go` becomes `partition_locks.go`.
25b moves bounds and routing, retained checks, proofs, hash bounds and
the partition dump and constraint-replay records (about 1.6k clean lines
today, about 3.1k after the cuts); `partition_remap.go` goes to `stored`
(22c) and the partitioned-index files to `index` (23a).

**Part B.** 25c refines the host (stage and authority, key and bound
binding by the root, evaluation contexts, validation views, storage-read
fences, a child-table builder callback, index child staging through
`index`); 25d moves the CREATE/ATTACH/DETACH decisions. INSERT routing
stays in the root until `dml` moves it. Needs 24b. Prompt file:
[ENGINE-SPLIT-partition.md](ENGINE-SPLIT-partition.md).

### Step 26 — `engine/fdw`: foreign data wrappers

**Part A.** 26a, in place: the `foreign_*` files that are FDW code get
the `fdw_` prefix (today they collide with the foreign-key files); the five
`Engine` registry fields become one `foreignRegistry` record (pattern 1);
the mapping and ownership helpers take `access.View` instead of
`AuthorizationView`; the participant functions become methods of the
foreign transaction record; scan request building becomes a pure function.
26b moves the clean set (about 0.95k lines). The public definition types
move too; the driver imports `fdw` directly (§7 decision 11).

**Part B.** 26c refines `fdw.Host`; 26d moves the WRAPPER, SERVER, USER
MAPPING, IMPORT FOREIGN SCHEMA and GRANT decisions; 26e moves the
CREATE/ALTER FOREIGN TABLE decisions after `table` (24d).

**Stays in the root.** The DDL orchestration (`Engine.mu`, releasing
`replacementMu` before validator and importer callbacks, publication),
`InstallForeignDefinitions`, the commit and savepoint call sites and the
publication point of `foreignNamespaces` (catalog notes). Foreign DML
execution goes to `dml`. Prompt file:
[ENGINE-SPLIT-fdw.md](ENGINE-SPLIT-fdw.md).

### Step 27 — `engine/view`: views, rules, policies, materialized views

**Part A.** 27a, in place: view-write lowering takes the catalog owner and
a `catalog.Read` instead of `AuthorizationView`; the rule program records
hold an opaque rule authority with one checked helper, because rule
execution needs the full authority back; the RLS fields of
`statementMutation` and `ruleActionAuth` become slots;
`CatalogStage.viewCascadeNames` becomes `dropCascadeNames` (it is generic
cascade text); the generic drop diagnostics move into a root
`drop_diagnostics.go`; dead code goes (`rewriteRuleOccurrences`,
`stagePolicyDependents`, `supportedRestrictError`). 27b moves the clean set
(about 5.2k lines; broad): rule programs and rewriting, view-write
lowering, stored query options, concurrent materialized refresh and
snapshots, RLS and policy dependencies.

**Part B.** 27c refines `view.Host` (authority; binding of view queries,
rule definitions and policy expressions by the root; stage; dependents;
notices); 27d moves the CREATE/ALTER/DROP VIEW, RULE and POLICY decisions
and the materialized-view DDL decisions.

**Stays in the root.** Lock waits (`view_locks.go`, write-target and
refresh acquisition), rule program execution (it runs DML), refresh row
production and staging, `fenceMaterializedSource`, the journal and
publication. Needs 22c and 24b. Prompt file:
[ENGINE-SPLIT-view.md](ENGINE-SPLIT-view.md).

### Step 28 — `engine/role`: role and ACL DDL

**Part A.** 28a renames: `relationDiagnosticName*` out of `roles.go` into
`relation_diagnostics.go`; `parameter_permission.go` to
`setting_permission.go`; `initializeDatabase` out of `database_grant.go`.
28b moves the clean set: grant records and warnings, role diagnostics and
errors, the password notice. The pure ownership helpers go to `access`
(B6), the aclitem I/O to `regobj`.

**Part B.** 28c refines `role.Host`; 28d moves the decisions of
CREATE/ALTER/DROP ROLE, GRANT/REVOKE target resolution and ACL change
construction, ALTER DEFAULT PRIVILEGES and REASSIGN/DROP OWNED planning.

**Stays in the root.** `AuthorizationView` capture, `roleStore` publication
and `installOperationRolesLocked`, `SetObjectACL` and `SetRoleReferences`,
and `Engine.mu`. Lower priority than the other steps: less code moves.
Prompt file: [ENGINE-SPLIT-role.md](ENGINE-SPLIT-role.md).

### Step 29 — `engine/dml`: write executors

`dml` consumes the bound statements of `analyze` (`analyze.Binding`, the
INSERT conflict, RETURNING, UPDATE and MERGE payloads), so it is an upper
package that may import `analyze` payload types; `analyze` never imports
it.

**Part A.** 29a, in place: the INSERT VALUES loop leaves
`executePreparedCommandLocked` (`execute_bound.go`) for an `insert.go`;
misplaced code goes to its owners (unique records to `index`, rule
helpers to `stored` and `view`, generic source capture to `exec`,
`rowByID` to `storage`); one receiver style
for the executors (`executeUpdate` and `executeMerge` are `Engine`
methods, `executeDelete` a scope method); FK enforcement and FK actions
are grouped here. 29b moves the clean set: ON CONFLICT state and arbiter
inference, MERGE and UPDATE records, RETURNING output, TRUNCATE records,
COPY execution records.

**Part B.** 29c refines the host. A flat host would need about 55 methods
from eight owners, which would be a service locator, so `dml` goes last:
its host is the union of the upper features' write-side interfaces plus
about ten write-stage and row-lock methods. 29d moves the executors.

**Stays in the root.** Dispatch (`executeBoundCommandLocked`,
`executePreparedCommandLocked`), source capture under `Engine.mu`,
writable-CTE orchestration (it re-enters dispatch), `WriteStage` and row
publication, COPY reservation and protocol. Prompt file:
[ENGINE-SPLIT-dml.md](ENGINE-SPLIT-dml.md).

### Step 30 — `engine/control`: Reset, Dump and Restore

**In place.** 30a applies pattern 5 to Restore: its statement allow-list
(#3) and its about 30 pre/post hooks become a table of entries (decode,
admit, before, after, finish), with today's order. Together with 18c,
`control.go` no longer names feature internals.

**Part B.** 30b refines `control.Host` (execute a statement on the restore
stage, read the catalog, the session, the registered providers and hooks);
30c creates `control` and moves the drivers: the Dump graph assembly and
emission, the Restore replay loop and finishing hooks.

**Stays in the root.** `Engine.mu` and the snapshot capture (catalog state,
roles, row versions, frontier, sequences, foreign namespaces), the private
`restoreStage`, `replaceCatalog` (storage reminting, remap, its lock order,
`locks.Fence` after unlock, the sequence-memory and serializable fences),
`Reset`, and the public `Dump`, `Restore` and `Reset`. Prompt file:
[ENGINE-SPLIT-control.md](ENGINE-SPLIT-control.md).

### Follow-ups in existing packages

Measuring the root also found work that belongs to packages that already
exist. Each item is a new sub-step in that package's prompt file.

- **Functions F6 — close step 3.** The last function-node cases of a root
  central switch (three, in `expression_remap.go`) go through the node protocol;
  the function cases left in `analyze` are audited (binder construction is
  fine; operation switches are not); then F2 and step 3 are ticked.
- **Catalog 5i — pure helpers move down.** The physical NOT NULL
  validators, the hierarchy helpers (`inheritanceChildren`,
  `inheritanceHasChildren`, `cloneInheritance`, `hierarchyRelation`, ...),
  constraint naming, `sameRelationRowShape`, `pgIdentifier` (as
  `catalog.Identifier`) and `relationDefiningAuthorityCompatible`; the
  clean part of definition identity, `namespaceChildren` and tablespace
  validation; the principal-remap descriptor switch as a catalog
  function.
  Done (item 72): the hierarchy helpers, constraint naming, the two
  physical primary helpers, `relationDefiningAuthorityCompatible`,
  definition-OID checks, tablespace validation, `namespaceChildren` (with
  `extension.Membership` as `catalog.ExtensionMembership`) and
  `catalog.RemapPrincipals`; `structuralNull` went to `plan`. The physical
  NOT NULL validators and `sameRelationRowShape` stay in the root (they
  reach `plan`, `analyze` and root providers).
- **Catalog 5j — payload accessors.** `RulesPayload.Len`, a rule visitor
  with each rule's identity, `ViewPayload.Options` and a `PolicyPayload`
  expression visitor let `analyze`, `regobj` and
  `pgcatalog` read what they need directly, removing
  `analyze.Host.RelationHasRules` and the `pgcatalog.Runtime` methods
  `HasRules`, `VisitRules`, `ViewOptions` and `VisitPolicyExpressions`
  (done, linear item 74).
- **Plan 10e.** The node-tree output (`deparse_tree.go`: `boundTree`,
  `appendBoundTree`, `catalogTree`) and the generation helpers
  (`generationHasTableOID`, `generationImmutable`). Done: `plan.BoundTree`,
  `plan.AppendBoundTree`, `plan.CatalogTree`, `plan.GenerationHasTableOID`
  and `plan.GenerationImmutable`, with `exec.SubscriptChildren` moved down
  to `plan` first.
- **Exec 11e — expression-preparation seam.** `exec.Preparation` gains
  coercion preflight with a policy and expression execution admission, so
  index, table and partition evaluation code no longer needs the scope;
  the seam also covers `preflightCoercionsWithPolicy` (through
  `prepareDomainConversion` and `selectExpressionRecipe`). The routine
  admission files (`routine_admission.go`, `routine_body_admission.go`,
  `routine_program.go`), `failingRowDetail`, `captureQuerySource` and
  `cursorSimpleScan` move with it. Done (item 75), with four cuts the plan
  missed because `routine` and `extension` sit above `exec`: the routine
  descriptor and SQL-body walk helpers moved down from `routine` to
  `exec`; `procedureReturnsSet` and `storedReferenceEpochValid` are reached
  through `Preparation.ReturnsSet`/`StoredReferenceValid`;
  `ViewPayload.Dependencies` replaced `viewOf`; `admitQueryGroupings`
  (it needs `analyze`) stays in the root. Admission reads
  `exec.RoutineAccess` (an `access.View` plus its environment) from the
  slot or `Host.RoutineAdmission`.
- **Exec 11f — preflight.** The expression preflight files
  (`expression_preflight.go`, `preflight_composite.go`,
  `conditional_plan.go`, `conditional_fold.go`), once routine admission
  lives in `exec`. `query_preflight.go` and `query_set_preflight.go` name
  `analyze` symbols, so 11f measures whether they go to `analyze` or stay;
  `preflightReturning` stays with `dml`. Done (item 76): the family moved
  with its clean helpers (array/UPDATE value preflight, string variadic
  folding, view qualification mapping, the OVERLAPS route; the OVERLAPS cost
  model moved down from `analyze`). It reaches the root through
  `Preparation.PreflightQuery`, `PrepareDomainConversion` and
  `SpecializeRuleParameters`. Query preflight stays in the root: it reaches
  the retag walker, rule producer demand (headed for `stored`) and window
  pruning (12f), so it fits neither `exec` nor `analyze` yet.
- **Analyze 12f.** The clean query-legality helpers (`query_graph.go`,
  the clean parts of `query_lock_admission.go` and `query_join_legality.go`,
  `query_window_prune.go`) and `closedRegclassDefault` (`serial.go`, next
  to `analyze.ClosedSequenceDefault`). The stored-view readers
  (`query_cte_volatility.go` and two row-mark helpers) go to `stored`;
  `expectedOf` waits for `StatementDescription`. Done (item 77); the
  executor walks of `query_join_legality.go` stay in the root.
- **Session 13f.** The notification records (`notificationFIFO`,
  `notificationLink`, `notificationState`) and the isolation text helpers.
- **Access B6.** The pure ownership helpers (`ownershipRoot`, `ownedRoots`,
  `ownershipRelationLocks`, `ownedPolicies`, `ownedMembershipCandidate`) and
  the RLS action rules (`rowSecurityAction*`). Done (item 79): the
  seeded-operator predicate and the table-kind diagnostic are passed as
  values, because `routine` and `analyze` import `access`.
- **Routine 14e.** `operator_dump.go` and `sqlRoutineDumpStatement`, with
  `dump.Presenter`.
- **Typedef 15e.** The collation and conversion dump statements (with
  `dump.Statement`), and the composite and domain dump statements (with
  `dump.Presenter`).
- **Notes only.** Sequence: identity and serial staging and the identity
  DROP flow go with `table` (24d), regclass output with `regobj` (21a).
  Extension: `procedureProvider` goes with `stored` (it imports `routine`
  and `extension`), `namespaceChildren` with `catalog` (5i), owner decoding
  with the Restore hooks (30a).

## 7. Open decisions

1. **Package names.** `ids`, `expr`, `catalog`, `lock`, `storage`,
   `visibility`. Settled for step 1: `ids` keeps today's type names and holds
   runtime identities as well as catalog ones.
2. **Shape of `expr.Context`.** Settled in 2a: one documented interface with
   grouped capabilities (core context, presentation environments, argument
   inspection, RNG and entropy, clocks, regex cache, conversion handlers). It
   satisfies every context constraint in `functions`. Because it names
   `functions.RandomOwner` and `functions.IntrinsicPropertyRule`, both moved to
   `expr` in 2b. Evaluation requires a non-nil `Context` (2b).
3. **Descriptor representation after the move.** Exported fields (proposed)
   or accessors, and whether to regroup `relationDescriptor` fields in 5e or
   later.
4. **Engine unit tests and `docs/TESTING.md`.** Confirm the split-time rule:
   adapt or delete, record follow-ups, never edit `reference/`.
5. **Tooling.** Whether to commit the dependency-graph tool, and a layering
   check that fails when a subpackage imports upward.
6. **Later-wave package names.** Proposed: `settings`, `pgcatalog`, `plan`,
   `exec`, `analyze`, `session`, `routine` (not `proc`, which the routine
   feature work already uses for SQL routine definitions), `typedef` (not
   `types`, which reads like `go/types` and `sqltype`), `extension`,
   `sequence`. Each step confirms its name in its first sub-step. Settled
   for step 8 (8b): `settings`, with `Definition`, `Type`, `Access`, `Unit`,
   `Value` (exported fields), `Registry`/`NewRegistry`, `Validate` and
   `SessionDefaultProvider`; constants drop the `Setting` prefix. Settled
   for step 10 (10c): `plan`, with `Node`, `Query`, `XxxNode` plan nodes and
   `XxxExpr` expression nodes (table in the plan prompt).
7. **Slot typing.** Before `session` moves, scope and journal slots name
   engine types; after it moves they must be lower-layer interfaces (§3 rule
   7). Settle in 13c whether that is one interface per slot or a typed key
   (`session.Slot[T]`). Settled in 13c: one interface per slot, with an
   exported marker method (and `Clone` where the journal or savepoints copy
   it) and one checked helper in the owner; a typed nil is never stored.
   Root-only slots with one lifetime collapse into one root payload per
   struct; the fence list becomes `[]session.Fence`. No `Slot[T]`. Scope
   slots need neither, because `StatementScope` stays in the root.
8. **Host interfaces.** `exec` (11b), `analyze` (12c) and the feature Part B
   steps each declare a host interface that the root package implements.
   Settled in 11a: one root adapter type, `statementHost` (a defined type
   over `StatementScope`), implements every consumer's narrow interface,
   with one compile-time assertion per consumer. Interfaces stay per
   consumer, so none becomes a service locator; lock contracts and nil-scope
   handling live in one place; the public `StatementScope` gains no host
   methods. Two scope-free exceptions: 11b's stateless `execPreparation`
   value, and 12c's `bindingHost` over the engine and operation, because
   binders also run without a scope (DDL, defaults, checks, bootstrap).
   13c confirms the pattern for the feature Part B steps: `StatementScope`
   stays in the root, so their handlers reach it only through such hosts.
   16c adds a third receiver for catalog staging: `extensionHost` over the
   engine, operation, captured `AuthorizationView` and `CatalogStage`
   (staging runs under `Engine.mu`), with per-consumer interfaces as above;
   never a defined type over `CatalogStage`, which would promote
   `catalog.Candidate`'s unchecked `Create`/`Replace`. 14c follows that
   shape with `schemaHost` (engine, operation, `CatalogStage`) for
   `routine.DDLHost`, but passes authority as an opaque `routine.Authority`
   value instead of capturing one view: the operator and CREATE FUNCTION
   helpers copy and recapture the view per helper, as `analyze.Host` takes
   an `analyze.Authority` per call.
   Step 17 needs no feature host (17c): the sequence handlers' decisions
   move as pure functions and their orchestration stays in the root.
   15c reuses `schemaHost` for `typedef.StageHost`: the type stage
   handlers share the `stageSchemaLocked` dispatch, and authority stays an
   opaque per-call `typedef.Authority` value, as in 14c. 15d implements it
   on `typedefHost`, a defined type over `schemaHost`'s fields, because the
   two hosts' same-named methods take each package's own `Authority` and
   `Stage`. 20a gave both one `ddl.Authority` and `ddl.Stage`, and 20b
   merged them into one adapter, `ddlHost`.
9. **Where `Eval` lives.** Step 10 keeps node types and `Eval` in `plan`,
   with engine state behind a node runtime capability; the alternative is to
   keep `Eval` for frame-reading nodes in `exec`. Settled in 10b: `Eval`
   stays on the node types, frame and executor bodies live on `*EvalContext`
   behind `nodeRuntime`. Startup allocations are unchanged; per-node
   dispatch costs about +3–16% on expression-heavy scans, accepted by the
   user (numbers in the plan prompt's 10b result).
10. **Settings session store.** Whether `sessionSettings` (with its SET LOCAL
    stack) moves into `settings` or `session`. It reads session authorization
    and timeouts today, so step 8 leaves it for 13c. Settled in 13c:
    `session`. It holds the `*Session` back-pointer, role setting actions
    and the owner's characteristic projections, and its mutex sits between
    `Engine.mu` and `session.mu`.
11. **Facade types.** `RegistrationError`, `SettingDefinition`, `RowResult`
    and `StatementDescription` are named by the root driver. Settle per step
    whether the driver imports the subpackage directly (as it does `catalog`
    and `ids`) or the engine keeps a permanent facade alias (§3 rule 2).
    Settled for step 8 (8c): the driver imports `settings` directly;
    `RegistrationError` stays an engine function. Settled for step 11 (11a):
    `exec` owns `RowCursor` and `RowResult`; since 11d the driver and
    `cmd/stuntgres` import `exec` directly, with no permanent alias.
    Settled for step 12 (12e): `analyze` owns `ParameterInfo`; the driver
    imports `analyze` directly (`frameImporter` and the adapter tests), with
    no permanent alias. Settled for step 13 (13c; applied in 13e): the
    driver and `cmd/stuntgres` import `session` directly for `Session`,
    `SessionStart`, `EndReason`, `ResourceKind`, `Tx*`, `TxOptions`, the
    isolation constants and `ProtocolCycle`; `StatementScope`,
    `StatementDescription`, `Result`, the other `Protocol*`/`CopyProtocol*`
    types, `CopyBoundary` and `FrameImporter` stay root types.
    `Session.Engine()` goes away, so the driver keeps its own engine field.
    Step 26 applies the same rule to the public FDW definition types: the
    driver imports `fdw` directly.
12. **Names of the step 18–30 packages.** Proposed: `dump`, `copyfmt` (not
    `copy`, which shadows the builtin), `ddl`, `regobj`, `stored` (settled
    in 22a over `deparse`: it also holds the frozen payloads),
    `index`, `table`, `partition`, `fdw`, `view`, `role`, `dml`, `control`.
    Each step confirms its name in its first sub-step.
13. **Upper features.** Settled by the second planning round (rule 6): the
    stored-SQL presentation needs about 50 `analyze` symbols, so it and the
    features that render stored SQL sit above `analyze`. They may import
    `analyze` types and pure functions but never construct a binder.
    Revisit only if a later step moves `analyze`'s capture vocabulary
    (`captures.go`, `view_namespace.go`, `type_names.go`) down to `plan`;
    the closed-default and coercion helpers import `extension` and
    `typedef`, so that is not planned.
14. **Inheritance.** Settled by the second planning round: no `inheritance`
    package. Inheritance DDL, recipes, retired constraints and Restore
    repair are part of `table`, with the clean query-expansion helpers; DML
    expansion goes to `dml`; the scope parts of query expansion stay with
    the root's query admission; the pure hierarchy helpers go to `catalog`
    (5i).
15. **Dump and Restore protocol.** Collect and Emit tables for Dump (18c)
    and a hook table for Restore (30a), composed by the root in today's
    order (pattern 5). The entry signatures are settled in 18c and 30a;
    output and error order must stay byte-identical. Settled by 18c:
    Collect entries are `{name, matches, collect(ctx, *dumpCollection,
    address, descriptor)}`, first match wins, and relation kinds have
    their own table after a shared prelude; the Emit table is indexed by
    `dump.Phase`, each entry an `owner` rule (nil: the emitter switches
    itself) and an `emit(ctx, *dumpScript, *dumpWriter, event)`; the
    whole script is an ordered `dumpSections` list (see the `dump`
    README).
16. **One DDL contract.** One `ddl.Authority` and `ddl.Stage` for every
    package that stages catalog changes, with one root staging adapter
    (20a, 20b). Settled by 20a: the per-package markers went; one
    `DDLAuthority` marker on `AuthorizationView`, recovered by
    `ddlAuthorityOf`. `extension`'s handlers take a `ddl.Authority` and check
    it through the view passed beside it (`view.Check(auth, req)`,
    `view.Effective(auth, ref, g)`, identical to the old methods).
17. **DML host size.** `dml` executors call about 55 root methods of eight
    owners today. 29c builds the host from the upper features' write-side
    interfaces plus the write stage and row locks, never one flat service
    locator; settle the exact split there.

## 8. Implementation sessions

Each package has a prompt file. Start a fresh session and say only:
"Implement `docs/ENGINE-SPLIT-<package>.md`." The prompt file holds the
package-specific scope. This section holds the rules every session follows.

| Step | Prompt file | Needs | Status |
|---|---|---|---|
| 1 | [`ENGINE-SPLIT-ids.md`](ENGINE-SPLIT-ids.md) | — | done (1a–1d) |
| 2 | [`ENGINE-SPLIT-expr.md`](ENGINE-SPLIT-expr.md) | 1 | done (2a–2d) |
| 3 | [`ENGINE-SPLIT-functions.md`](ENGINE-SPLIT-functions.md) | 2 | F1, F3, F4/F5 Tier A, Tier B, and aggregates and windows done; F2 view literal capture, result nullability protocol and conditional closure hook done (selected proof/quickcheck passed; racecheck/finalcheck waived by user); `SQLJSON` rendering done (selected dual-mode/quickcheck passed; remaining checks, including full racecheck/finalcheck, waived by user); function sources done (proof and specific LIKE-observer failure waiver recorded in the functions prompt); Tier C done: ACL, catalog inquiry, transaction inquiries, advisory/blocking and sequence calls moved (sequence calls: selected dual-mode/quickcheck passed, racecheck/finalcheck skipped at user request); F3 aliases removed |
| 4 | [`ENGINE-SPLIT-lock.md`](ENGINE-SPLIT-lock.md) | 1 | done (4a–4c) |
| 5 | [`ENGINE-SPLIT-catalog.md`](ENGINE-SPLIT-catalog.md) | 1, 2 | done (5a–5h); 5i done (pure helpers moved, some left in the root) and 5j done (payload readers replace five host and runtime methods) (racecheck/finalcheck skipped at user request) |
| 6 | [`ENGINE-SPLIT-storage.md`](ENGINE-SPLIT-storage.md) | 5 | done (6a–6c); optional 6d open |
| 7 | [`ENGINE-SPLIT-visibility.md`](ENGINE-SPLIT-visibility.md) | 4, 6 | done (7a–7c) |
| A/B | [`ENGINE-SPLIT-authorization.md`](ENGINE-SPLIT-authorization.md) | A: —; B: 5 | done (A1, B0–B6, names cleanup; B5 moved the binding walkers into `analyze`; B6 moved the ownership helpers and RLS action rules into `access`; racecheck/finalcheck skipped at user request) |
| 8 | [`ENGINE-SPLIT-settings.md`](ENGINE-SPLIT-settings.md) | — | done (8a–8c; racecheck skipped at user request) |
| 9 | [`ENGINE-SPLIT-pgcatalog.md`](ENGINE-SPLIT-pgcatalog.md) | 5, B4; 8b for 9b | done (9a in place, 9b package created and producers moved, 9c aliases removed; racecheck skipped at user request) |
| 10 | [`ENGINE-SPLIT-plan.md`](ENGINE-SPLIT-plan.md) | 2, 3 F1, 5 | done (10a–10e): `plan` created, IR, node types and installed extension nodes moved, aliases removed; 10e moved node-tree output and the generation helpers (racecheck/finalcheck skipped by user) |
| 11 | [`ENGINE-SPLIT-exec.md`](ENGINE-SPLIT-exec.md) | 10c, 13a | done (11a–11d): refined (decisions 8 and 11), host seam, `exec` created and executor moved, aliases removed (racecheck/finalcheck skipped at user request); 11e done (preparation seam, routine admission moved; racecheck/finalcheck skipped at user request); 11f done (expression preflight moved; query preflight stays; racecheck/finalcheck skipped at user request) |
| 12 | [`ENGINE-SPLIT-analyze.md`](ENGINE-SPLIT-analyze.md) | 12a–12b: —; 12c: 10c; 12d: 12c, 11c | done (12a–12e): `binding` split per statement kind, binder fields grouped, host seam, `analyze` created, areas prepared in place and the binder moved (12d.1–12d.6), aliases removed; the driver imports `analyze` for `ParameterInfo`; 12f done (query legality helpers moved) (racecheck/finalcheck skipped at user request) |
| 13 | [`ENGINE-SPLIT-session.md`](ENGINE-SPLIT-session.md) | 13a–13b: —; 13c: 11c, 12d, 8b | 13a and 13b done (statement scope slots; operation, journal, stage and owner slots; racecheck/finalcheck skipped at user request); 13c refined (state moves, the scope and orchestration stay; decisions 7, 10 and 11 settled); 13d.1 done (`session` created, journal moved with aliases), 13d.2 done (statement lease), 13d.3 done (engine edges: `session.Host`, `engineOf`, owner root functions) 13d.4 done (root payloads: session payload, owner extension, savepoint and operation slots; notification polling on `Engine`) 13d.5 done (file splits: 15 moving-only files; 192 members the root reads exported) and 13d.6 done (session state moved into `session` with type and constant aliases; `Host.NewOperation`); 13e done (aliases removed; the driver and `cmd/stuntgres` import `session`, decision 11 applied) (racecheck/finalcheck skipped at user request); 13f done (notification queue records, isolation text and owner handles moved; racecheck/finalcheck skipped at user request) |
| 14 | [`ENGINE-SPLIT-routine.md`](ENGINE-SPLIT-routine.md) | A: `PG18-ROUTINE-CORE` merged, 14b also the F3 aliases item of step 3; B: 13d | 14a done in place, 14b `routine` created and clean set moved (racecheck skipped at user request); 14c refined (`DDLHost` on a scope-free root `schemaHost`, authority as an opaque value, dumps wait for a dump seam); 14d done (operator and CREATE FUNCTION staging moved behind `DDLHost`; racecheck/finalcheck skipped at user request); 14e done (operator and SQL routine dumps moved behind `dump.Presenter`/`dump.Resolver`; racecheck/finalcheck skipped at user request) |
| 15 | [`ENGINE-SPLIT-typedef.md`](ENGINE-SPLIT-typedef.md) | A: 14b; B: 13d | 15a done in place, 15b `typedef` created and clean set moved (racecheck/finalcheck skipped at user request); 15c refined (`typedef.StageHost` on 14c's `schemaHost`, authority as a value; stage handlers move, executors stay); 15d done (stage handlers and the ALTER ENUM/DOMAIN staging cores moved behind `StageHost` on `typedefHost`; racecheck/finalcheck skipped at user request); 15e done (type dump statements moved; `dump.Presenter` renders domain DEFAULT and CHECK text; racecheck/finalcheck skipped at user request) |
| 16 | [`ENGINE-SPLIT-extension.md`](ENGINE-SPLIT-extension.md) | A: 15b; B: 13d | 16a done in place, 16b `extension` created and clean set moved (racecheck/finalcheck skipped at user request); 16c refined (providers own staging behind `extension.Stage`, handler decisions behind `extension.Host`, one root adapter); 16d done: 16d.1 providers stage themselves, 16d.2 handler decisions moved behind `extension.Host` (racecheck/finalcheck skipped at user request) |
| 17 | [`ENGINE-SPLIT-sequence.md`](ENGINE-SPLIT-sequence.md) | A: step 3 Tier C sequence calls; B: 13d | 17a done in place, 17b `sequence` created and clean set moved (racecheck/finalcheck skipped at user request); 17c refined (decisions move, orchestration stays; no `sequence.Host`); 17d done (handler, identity, serial, prepared-literal, call and Control decisions and the stage slot moved; orchestration stays in the root) (racecheck/finalcheck skipped at user request) |
| 18 | [`ENGINE-SPLIT-dump.md`](ENGINE-SPLIT-dump.md) | — | 18a done in place (`dump_graph.go`, `dump_restore_scalar.go`, ACL preflight as a function; racecheck/finalcheck skipped at user request); 18b done (`dump` created: event graph, `Order`, `Statement`, `Group`, Restore scalar validators, `Resolver`/`Presenter` with root adapters; no aliases; racecheck/finalcheck skipped at user request); 18c done (`captureDumpLocked` unlocks once; ordered Collect tables, phase-indexed Emit table and section list; output byte-identical; racecheck/finalcheck skipped at user request) |
| 19 | [`ENGINE-SPLIT-copyfmt.md`](ENGINE-SPLIT-copyfmt.md) | — | done (19a cursors split in place; 19b `copyfmt` created and the codec moved: options, FORCE binding, `LineReader`, `BinaryReader`, no aliases; racecheck/finalcheck skipped at user request) |
| 20 | [`ENGINE-SPLIT-ddl.md`](ENGINE-SPLIT-ddl.md) | — | 20a done (`ddl` created; `routine`, `typedef` and `extension` adopt it; racecheck/finalcheck skipped at user request); 20b done (one root staging adapter `ddlHost`; racecheck/finalcheck skipped at user request); 20c refined (`ddl.SchemaHost` on `ddlHost`, schema and tablespace decisions move, orchestration stays); 20d done (CREATE/ALTER/DROP SCHEMA and CREATE TABLESPACE decisions in `ddl` behind `ddl.SchemaHost`; seed comments in `catalog`; no aliases; racecheck/finalcheck skipped at user request) |
| 21 | [`ENGINE-SPLIT-regobj.md`](ENGINE-SPLIT-regobj.md) | 21b: 5j | 21a done in place (regclass, literal, namespace-visibility and identity files; `pgIdentifier` is `catalog.Identifier`; racecheck/finalcheck skipped at user request); 21b done (`regobj` created, clean set moved, no aliases; `regtypeAnalysis` stays in the root; racecheck/finalcheck skipped at user request); 21c done (captured input resolvers moved behind `regobj.Authority` and `NoticeHost`; constructors stay in the root; no aliases; racecheck/finalcheck skipped at user request) |
| 22 | [`ENGINE-SPLIT-stored.md`](ENGINE-SPLIT-stored.md) | 22b: 21b, F6, 18b, 12f; 22c: 5j | 22a done in place (coverage and freeze cuts, `viewDefinition` read accessors, renames, name `stored` settled; racecheck/finalcheck skipped at user request); 22b–22d planned |
| 23 | [`ENGINE-SPLIT-index.md`](ENGINE-SPLIT-index.md) | 23b: 22b, 5i, 10e, 11e; 23c: 20b | 23a done in place (renames, wait and exclusion registries, validator, publication, evolution, stage and transport cuts; racecheck/finalcheck skipped at user request); 23b–23d planned |
| 24 | [`ENGINE-SPLIT-table.md`](ENGINE-SPLIT-table.md) | 24a: 5i; 24b: 23b, 18b, B6; 24c: 23c; 24d: 23d | 24a done in place (renames, LIKE source slot, statement token, inheritance plan record, FK authority and drop dependency reader cuts; racecheck/finalcheck skipped at user request); 24b–24d planned |
| 25 | [`ENGINE-SPLIT-partition.md`](ENGINE-SPLIT-partition.md) | 25b: 24b; 25c: 24c; 25d: 24d | 25a done in place (context cut, `partitionRecipeHost`, storage-read fence and file renames; racecheck/finalcheck skipped at user request); 25b–25d planned |
| 26 | [`ENGINE-SPLIT-fdw.md`](ENGINE-SPLIT-fdw.md) | 26c: 20b; 26e: 24d | 26a done in place (`fdw_` files, `foreignRegistry`, authority cuts, participant methods, scan request, write runtime; racecheck/finalcheck skipped at user request); 26b done (`fdw` created: definition types, registry, options, identity set, write protocol, participants and DDL helpers; the driver imports `fdw`; no aliases; racecheck/finalcheck skipped at user request); 26c–26e planned |
| 27 | [`ENGINE-SPLIT-view.md`](ENGINE-SPLIT-view.md) | 27b: 22c, 24b; 27c: 20b | 27a done in place (dead code, drop diagnostics split, `dropCascadeNames`, view-write lowering and rule expansion off `Engine`, opaque rule authority, access helpers over `access.View`, RLS and rule-action value groups; racecheck/finalcheck skipped at user request); 27b–27d planned |
| 28 | [`ENGINE-SPLIT-role.md`](ENGINE-SPLIT-role.md) | 28b: 21b, B6; 28c: 20b | planned (28a–28d) |
| 29 | [`ENGINE-SPLIT-dml.md`](ENGINE-SPLIT-dml.md) | 29a: 23a, 24a; 29b: Part A of 19, 23–27; 29c: Part B of 23–27 | planned (29a–29d) |
| 30 | [`ENGINE-SPLIT-control.md`](ENGINE-SPLIT-control.md) | 30a: 18c; 30b: 22d, 21b, 14e, 15e, Part A of 23–27 | planned (30a–30c) |

The follow-ups of §6 (F6, 5i, 5j, 10e, 11e, 11f, 12f, 13f, B6, 14e, 15e)
are sub-steps of the existing prompt files above.

Tracks may run in parallel only on separate branches. Do not run two broad
mechanical rewrites at the same time (F1, F2, 5b, 5d, any alias removal, and
in the later waves 10a, 10c, 11c, 12a, 12b, 12d, 13a, 13b, 13d, 22b, 22d, 24b
and 27b): they touch hundreds of the same files. Rebase and re-verify before
integration. The numbered "Linear plan" in
[`ENGINE-SPLIT-STATUS.md`](ENGINE-SPLIT-STATUS.md) orders every open sub-step
and marks which ones may run in parallel.

### 8.1 Session protocol

1. **Read** `AGENTS.md`, `docs/TESTING.md`, this document (§2.3, §3, §4, your
   step in §5 and this section) and the whole prompt file, including its
   Status section. `docs/design/*` is background only (see the top of this
   document).
2. **Check prerequisites** against the table above and `git log`. If a
   prerequisite is not done, stop and report.
3. **Pick the work.** Take the first unchecked sub-step in the prompt file's
   Status section, or the linear-plan item you were given. Do one sub-step per
   session unless the prompt file says otherwise. A refine sub-step (§4.1
   pattern 4) changes only docs: its result is the refined plan written into
   the prompt file, approved by the user.
4. **Refine before editing.** Names, counts and file lists in these documents
   were measured at commit `a2252c0b1` and may be stale, so re-measure on the
   current code. Then present a file-level plan: the declarations that move,
   new types, interfaces and accessors, the groups of call sites that change,
   the commit sequence, the risks, and the docs to update. Wait for approval.
   Starting the session in plan mode enforces this.
5. **Implement** in small commits, following the phases of §4 (seam, port in
   place, move with aliases). Every commit builds and passes the targeted
   tests.
6. **Verify** (§8.3), **document** (§8.4), update the prompt file's Status
   section and [`ENGINE-SPLIT-STATUS.md`](ENGINE-SPLIT-STATUS.md), and
   **report** (§8.5).

### 8.2 Constraints

- **Pure refactor.** No behavior change, no SQL surface change, no new
  dependencies. The public facade used by the root driver package stays
  source-compatible.
- **`reference/` and `smoke/` are read-only.** Never run `make refcheck`. The
  split adds no reference tests, because behavior does not change. TDD's
  failing-test-first rule applies only to new unit tests that pin a new seam's
  semantics.
- **Layering.** Imports point downward only (§3). Check with
  `go list -f '{{.ImportPath}}: {{join .Imports " "}}' ./internal/engine/...`.
- **One lock authority** (§3, rule 3). Subsystem packages add no mutexes
  (`lock` keeps its own). Preserve immutable published snapshots and atomic
  publication.
- **Temporary aliases only.** The aliases of §4 are the only allowed
  compatibility shims. List them in the Status section until they are removed.
- **Unit tests.** Follow §4. A test moves with the code it covers. Delete an
  engine test only if it pins internals that no longer exist, or if
  `reference/` covers its public behavior; name the covering reference test.
  List every moved, deleted or substantially rewritten test in the report.
- **Scope.** Record anything found for later steps under "Notes for later
  steps" in the Status section instead of doing it.
- **Git.** Work on a branch named `split/<package>` (create it from the
  starting branch if needed). Commit each green phase. Do not push or merge.

### 8.3 Verification

Each command gets a 900-second deadline (`timeout 900s make ...`).

- `make build` after every commit.
- During work: `make check RUN='...'` for the directly affected tests. Add
  `RACE=1` for locks, sessions, cancellation and publication.
- Before finishing a sub-step: `make quickcheck` and `make racecheck`. The
  quick-mode reference suite inside `quickcheck` exercises the public paths.
- `make bench` after every sub-step that touches startup, catalog seeding or
  evaluation hot paths. Report ns/op and allocations against the <1 ms
  startup target, before and after.
- `make check REFERENCE=1 RUN='...'` for the selections the prompt file asks
  for. This needs Docker. If Docker is unavailable, report the check as
  blocked; never skip it silently.
- `make deps`: output may contain only this module's paths.
- If a failure is unrelated to the change, report it instead of working around
  it.

### 8.4 Documentation

Update only what the sub-step changed, but always check each item:

- **Package README** (`internal/engine/<package>/README.md`): purpose, what the
  package owns, its invariants (lock ownership, immutability, provenance), its
  main entry points, and what stays in the engine. Its `## Testing` section
  lists selectors for the package's tests. Move the selectors of moved tests
  out of `internal/engine/README.md`.
- **This document.** Update the step's Status cell in §8. Correct any statement
  the work proved wrong (counts, feasibility, layering). Record answers to §7
  questions that the step settled.
- **Prompt file Status section**: sub-steps done with commits, outstanding
  aliases, tests moved or deleted, follow-ups, and lessons for later steps.
- **[`ENGINE-SPLIT-STATUS.md`](ENGINE-SPLIT-STATUS.md)**: tick the sub-step
  and refresh "Next ready sub-steps", following its "Updating this file"
  section.
- **Design notes and other docs.** Grep `docs/`, `README.md` and all package
  READMEs for the moved identifiers and the old file names. Update package and
  ownership statements and names. Do not rewrite design rationale. Where a
  design note describes a structure this split replaced, replace that passage
  with a one-line pointer to the package README or this document. User-facing
  docs (`README.md` and the non-design `docs/*.md`) change only where they name
  internals.
- **`AGENTS.md` must not grow.** Report `wc -l AGENTS.md` before and after;
  net growth needs an explicit justification in the report.
  - The `internal/engine` bullet lists only what remains in the root package.
  - Engine subpackages share one bullet that names them and points to their
    READMEs and §3. Do not add a bullet per package.
  - When a subsystem moves, its specific rules in `AGENTS.md` move to the
    package README. Leave at most one line in `AGENTS.md`, and only if agents
    working elsewhere need it.
  - Delete `AGENTS.md` text that the step made redundant or obsolete.

### 8.5 Report

End the session with: what moved; the seams and interfaces introduced; the
commits; verification results, including bench numbers and blocked checks;
tests moved, adapted or deleted; docs changed, with the `AGENTS.md` line
delta; deviations from the plan; follow-ups; and whether the next sub-step is
ready.

## Appendix: how the analysis was produced

A throwaway, standard-library-only Go program type-checked the production
files of `internal/engine` with `go/types`, using compiler export data from
`go list -export -deps`. For every top-level declaration it recorded:

- which package-level declarations it references,
- which methods it calls,
- which struct fields it uses, attributed to the owning type.

Small Python scripts then computed:

- strongly connected components of the type graph;
- shortest type paths from core types to the god structs, which identified the
  eleven cuts;
- layered type closures with the cuts applied (§2.3 tables);
- the maximal set of declarations that never reach a god struct (§2.4);
- per-field usage counts (§2.2) and per-area external references (§2.5).

File-group line counts use filename prefixes and are approximate. Re-run the
analysis when refining a step: it shows immediately whether a proposed move
set still has upward references.
