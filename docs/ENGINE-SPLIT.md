# Splitting `internal/engine` — analysis and first steps

Status: approved; implementation runs one session per sub-step (§8). The first
steps (§5) are refined with file-level detail at the start of each session.
Later waves (§6) are only sketched.

The analysis is based on the production code at commit `a2252c0b1`, not on the
design notes in `docs/design/`. Those notes are background only: where they
disagree with a clearer split, this document takes precedence. Update them
after the step they describe lands.

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
waves split those (§6).

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
| 5 | `SessionToken.owner *Engine` | Provenance check; also backed `Session.Engine()` and session and notification teardown | `ids.Domain` token; `Session` and `sessionNotifications` hold their engine (done in step 1) |
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
| `visibility` | 13 | Visibility registry, xid identity records, `VisibilityView`/`visibilityData`, `transactionRoot`, commit timestamps, serializable tracker |

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
leave the root package until the catalog does.

## 3. Target architecture

```text
 L0  parser   sqltype   sqlstate                         (existing leaves)
 L1  engine/ids            identity values and provenance domain
 L1  engine/authorization  role graph and ACL algorithms (existing)
 L2  engine/expr           expression contract and node protocol
 L2  engine/lock           heavyweight lock manager            (imports ids)
 L3  engine/functions      builtin functions                   (imports expr, ids)
 L3  engine/catalog        descriptors, State, Read, Candidate (imports ids, expr, authorization)
 L4  engine/storage        row versions, storage and index roots (imports catalog, expr)
 L5  engine/visibility     xids, commit order, snapshots, SSI  (imports storage, catalog, lock)
 L6  engine                facade, sessions, statements, binder, executor, features
```

Package names are proposals (§7).

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
- `docs/TESTING.md` asks for offending public-API tests to be migrated into
  `reference/` whenever an area is touched. That conflicts with keeping
  `reference/` read-only. Proposal: during the split, delete such tests when
  `reference/` already covers the behavior. Otherwise keep them adapted and
  record them as follow-ups (§7).
- Documentation, including `AGENTS.md`, is updated with every sub-step under
  the rules in §8.4. The current `functions` and `authorization` READMEs
  describe the shallow split and need rewriting once the matching steps land.

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
State maps. Optionally, regroup `relationDescriptor` fields into sub-records
afterwards (columns metadata, constraints, security, kind payloads), as a
separate readability change.

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

## 6. Later waves (sketch)

| Package | Content | Needs | Why this order |
|---|---|---|---|
| `access` | Authorization enforcement on catalog objects: `AuthorizationView` capture, privilege checks, default privileges, RLS policy selection (~6k lines) | `catalog`, `authorization` | Unblocked as soon as step 5 lands |
| `pgcatalog` | System relations `pg_*` (34 files, ~4k lines) | `catalog`, a row-producer contract | Leaf consumers of the catalog |
| `settings` | GUC definitions, typed values, validation, formatting (~1.4k lines); session storage stays with sessions | `catalog` (types) | Nearly independent |
| `plan`, `exec` | Logical plan IR (`BoundQuery`, `LogicalNode` and nodes), executor (`query_*`, CTE, sets, windows), concrete engine expression nodes | `expr`, `catalog`, `storage`, `visibility`; `EvalContext` split | Removes the second-largest knot |
| `analyze` | `expressionBinder` and statement binding; break up `binding` per statement kind | `plan`, `functions` | After the plan IR has a home |
| `session` | `Session`, `Operation`, prepared SQL, portals, cursors, notifications, timeouts; transaction owner, savepoints, journal, 2PC | Most of the above | The facade core; last of the shared pieces |
| Feature packages | Index, partition, inheritance, sequence, types (domain, enum, composite, range, collation, conversion), views/rules/policies (owner of the stored-query payload), FDW, extensions, operators/routines, DML | Core packages | Each owns its DDL and dump/remap code |
| `control` | Native Reset/Dump/Restore, through a catalog-level remap protocol instead of central switches | Feature packages | Last |

Two patterns from the first steps carry over to every wave. First, a
`StatementScope` field used by one feature becomes that feature's statement
state, reached through one typed slot, so the shared struct stops growing.
Second, central type switches are replaced by per-node or per-payload
protocols.

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

## 8. Implementation sessions

Each package has a prompt file. Start a fresh session and say only:
"Implement `docs/ENGINE-SPLIT-<package>.md`." The prompt file holds the
package-specific scope. This section holds the rules every session follows.

| Step | Prompt file | Needs | Status |
|---|---|---|---|
| 1 | [`ENGINE-SPLIT-ids.md`](ENGINE-SPLIT-ids.md) | — | done (1a–1d) |
| 2 | [`ENGINE-SPLIT-expr.md`](ENGINE-SPLIT-expr.md) | 1 | done (2a–2d) |
| 3 | [`ENGINE-SPLIT-functions.md`](ENGINE-SPLIT-functions.md) | 2 | F1, F3, F4/F5 Tier A, Tier B, and aggregates and windows done; F2 eight of nine switches done (deparse and node tree through `expr.Rendering`); F2 stored scalar SQL next |
| 4 | [`ENGINE-SPLIT-lock.md`](ENGINE-SPLIT-lock.md) | 1 | done (4a–4c) |
| 5 | [`ENGINE-SPLIT-catalog.md`](ENGINE-SPLIT-catalog.md) | 1, 2 | done (5a–5f); optional 5g needs approval |
| 6 | [`ENGINE-SPLIT-storage.md`](ENGINE-SPLIT-storage.md) | 5 | not started |
| 7 | [`ENGINE-SPLIT-visibility.md`](ENGINE-SPLIT-visibility.md) | 4, 6 | not started |
| A/B | [`ENGINE-SPLIT-authorization.md`](ENGINE-SPLIT-authorization.md) | A: —; B: 5 | A1 done; B0 waits for step 5 |

Tracks may run in parallel only on separate branches. Do not run two broad
mechanical rewrites at the same time (F1, F2, 5b, 5d, or any alias removal):
they touch hundreds of the same files. Rebase and re-verify before integration.

### 8.1 Session protocol

1. **Read** `AGENTS.md`, `docs/TESTING.md`, this document (§2.3, §3, §4, your
   step in §5 and this section) and the whole prompt file, including its
   Status section. `docs/design/*` is background only (see the top of this
   document).
2. **Check prerequisites** against the table above and `git log`. If a
   prerequisite is not done, stop and report.
3. **Pick the work.** Take the first unchecked sub-step in the prompt file's
   Status section. Do one sub-step per session unless the prompt file says
   otherwise.
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
