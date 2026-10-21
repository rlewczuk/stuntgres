# Engine split, step 18: `internal/engine/dump`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-dump.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §6 step 18; §3 (layer table,
  rule 8); §4.1 pattern 5 (registration); §7 decisions 12 and 15.
- **Needs:** 18a: nothing. 18b: 18a. 18c: 18b. Nothing else is required:
  `dump` sits at L3.5 and imports only lower packages.
- **Linear items:** 18a = item 55 (needs —), 18b = item 56 (needs 55),
  18c = item 57 (needs 56). Later items that build on this step: 14e
  (item 80), 15e (item 81) and 30a (item 111).
- **Effort:** high for 18a and 18b; xhigh for 18c (it restructures the
  1,588-line `Engine.Dump` and must keep the output byte-identical).
- **Session size:** one sub-step per session.
- **Tools:** `reach`
  (`.cswd/tmp/PG18-ENGINE-SPLIT-EXEC-REFINE/reach/reach -dir . -files '<regexp>'`)
  and `frontier`
  (`.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-QUERY-PREP/frontier/frontier -dir . -files a.go,b.go`),
  run from the worktree root; never pipe them into `head`. Rewrite tools:
  `movex`, `unalias`, `keylit`, `shadowrn` in
  `.cswd/tmp/PG18-ENGINE-SPLIT-PLAN-ALIASES/`; `movespec` and `expspec` in
  `.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-QUERY-PREP/`.

## Goal

Native Dump writes the catalog as a SQL script. To order that script, the
engine builds a dependency graph of "events" (a declaration, a rename, a
row load, an attachment, ...) and sorts it. The graph records, the sort
and two small Restore validators are used by the dump code of almost every
feature (routines, types, tables, partitions, views), but they live in the
root, in a file misleadingly called `view_dump.go`. So feature dump code
cannot move into its package even when it is clean.

This step creates `dump`, a small leaf package that owns those records and
the sort, and declares two capabilities the feature dump code needs from
above (`Resolver`, `Presenter`). Then it turns the two central switches of
`Engine.Dump` into ordered tables (pattern 5), so that a feature package
adds a table entry instead of editing `control.go`.

What stays in the root: `Engine.Dump` itself (its `Engine.mu` window, the
snapshot capture and the emission sequence) until step 30, and
`dumpPredecessor`, which implements `Resolver` and reads partition,
operator and index helpers that are not clean.

## Inventory (measured at `c154e14fb`)

### Dump-graph primitives

| Declaration | Where | Status | Moves to |
|---|---|---|---|
| `dumpPhase` and its 16 constants (`dumpDeclaration` … `dumpOperatorLinks`) | `view_dump.go:128` | clean | `dump.Phase`, same numeric order |
| `dumpEventKey{oid, phase}` | `view_dump.go:149` | clean | `dump.Event{OID, Phase}` |
| the graph type `map[dumpEventKey][]dumpEventKey` (no name today; `control.go` calls it `viewDependencies`) | `control.go` | — | `dump.Graph` |
| `dumpDeclarationEvents`, `dumpDeclarationIDs` | `view_dump.go:154`, `:162` | clean | `dump.DeclarationEvents`, `dump.DeclarationIDs` |
| `viewDumpReady` (heap) and `viewDumpOrder` (topological sort) | `view_dump.go:172`, `:206` | clean | `dump.Order` (the heap stays unexported) |
| `dumpDomain{owner, sql}` | `domain_dump.go:18` | clean | `dump.Statement{Owner, SQL}` |
| `dumpPolicyGroup{owner, table, sql}` | `policy_dump.go:14` | clean | `dump.Group{Owner, Table, SQL}` |
| `commentLiteral` | `control.go:2292` | clean | `dump.CommentLiteral` |
| `restoreScalar` (219 lines) | `control.go:2299` | clean | `dump.RestoreScalar` |
| `restoreSQLJSONQuery` | `sqljson_restore.go` | clean | `dump.RestoreSQLJSONQuery` |
| `dumpPredecessor` | `view_dump.go:16` | dirty (`partitionReplayUsingIndex` → … → `dropProvider`) | stays; implements `dump.Resolver` |

The sort's error texts (`"stuntgres: missing view dump predecessor"`,
`"stuntgres: cyclic view dump dependencies"`) are internal errors. Keep
them byte for byte; do not "fix" the word *view*.

Uses today: `dumpEventKey` in 13 production and 10 test files,
`viewDumpOrder` in 2 and 10, `dumpDomain` in 7 production files,
`dumpPolicyGroup` in 3, `commentLiteral` in 6, `restoreScalar` in 4
production and 8 test files, `dumpPredecessor` in 9 and 7.

### Producers of the records

- `dump.Statement` (today `dumpDomain`) is produced by
  `collationDumpStatements` (`collation_dump.go`),
  `conversionDumpStatements` (`conversion_dump.go`),
  `compositeDumpStatement` and `mixedDumpStatements` (`composite_dump.go`),
  `domainDumpStatement` (`domain_dump.go`), `operatorDumpDeclaration`,
  `operatorDumpDefined`, `orphanOperatorShellDump` (`operator_dump.go`) and
  `sqlRoutineDumpStatement` (`sql_routine_dump.go`).
- `dump.Group` (today `dumpPolicyGroup`) is produced by
  `policyDumpGroups` (`policy_dump.go`), `rowSecurityDumpGroups`
  (`rls_base_dump.go`) and `ruleDumpGroups` (`rule_dump.go`).
- Graph edges are added by `operatorDumpEvents`, `sqlRoutineDumpEvents`,
  `domainInvalidDumpEvents`, `partitionDumpEvents`,
  `partitionInvalidDumpEvents`, `physicalNotNullDumpDependencies` and the
  inline code of `Engine.Dump`.

### Dump, remap and Restore files, and which step moves each

Lines, then clean/dirty lines from `reach`. "Hook" means a dirty Restore
executor that 30a puts into the Restore hook table; it stays in the root
until its owner's Part B.

| File | Lines (clean/dirty) | Step |
|---|---:|---|
| `view_dump.go` | 244 (118/112) | 18 (records); `dumpPredecessor` stays |
| `control.go` | 2,517 (281/2,213) | 18 (`commentLiteral`, `restoreScalar`), 18c and 30a (tables), 30c (drivers) |
| `restore_replay.go` | 45 (35/0) | root `Operation` slot; 30b decides |
| `sqljson_restore.go` | 15 (9/0) | 18 |
| `collation_dump.go`, `conversion_dump.go` | 85 (73/0), 77 (62/0) | typedef 15e (item 81) |
| `composite_dump.go`, `domain_alter_dump.go` | 110 (75/22), 156 (30/111) | typedef 15e; dirty parts are hooks |
| `domain_dump.go` | 315 (299/0) | literals and type declarations to `regobj` (21); `domainDumpStatement` to typedef 15e |
| `domain_default_restore.go` | 387 (89/278) | typedef 15e (clean); hook |
| `domain_remap.go` | 215 (203/0) | `stored` 22c |
| `collation_seed_dump.go` | 43 (0/35) | root (`validateTemporaryCollationDump` builds a `CatalogStage`) |
| `operator_dump.go` | 306 (292/0) | routine 14e (item 80) |
| `sql_routine_dump.go` | 89 (32/46) | routine 14e (statement); Restore validation is a hook |
| `table_dump.go`, `constraint_dump.go`, `default_replay.go` | 200 (159/22), 127 (119/0), 59 (43/0) | table 24b |
| `not_null_restore.go`, `attribute_rank_restore.go`, `foreign_key_restore.go` | 323 (152/161), 151 (65/75), 221 (29/177) | table 24b; hooks |
| `physical_not_null_dump.go`, `physical_not_null_restore*.go` | 207 (37/162), 321+130+238 | table 24b; hooks |
| `inheritance_*_restore*.go` | 318+108+429+262 | table 24b; hooks |
| `partition_dump.go`, `partition_dump_events.go`, `partition_constraint_dump.go`, `partition_invalid_dump.go`, `partition_unique_dump.go` | 360, 556, 215, 259, 184 | partition 25b |
| `partition_remap.go`, `partition_*_replay.go`, `partition_detach_restore.go` | 151, 74+201+64, 82 | partition 25b; hooks |
| `expression_remap.go` (was `view_remap.go`), `frozen_presentation_remap.go` | 1,169 (1,101/47), 118 (108/0) | `stored` 22b and 22c |
| `catalog_expression_reown.go` (was `view_catalog_remap.go`) | 302 (0/290) | `stored` 22c (`ReownExpressions`) |
| `rule_dump.go`, `rule_source_dump.go`, `rule_assignment_dump.go`, `update_row_dump.go`, `policy_dump.go`, `rls_base_dump.go`, `rule_remap.go`, `policy_remap.go` | 587, 56, 46, 76, 118, 44, 445, 34 | `stored` 22c |
| `materialized_snapshot.go` | 365 (151/191) | view 27b; hook |
| `regclass_dump.go`, `aclitem_dump.go`, `aclitem_remap.go` | 36, 149 (39/95), 70 (0/61) | `regobj` 21 (clean parts) |
| `principal_remap.go` | 303 (0/289) | descriptor switch to `catalog` 5i; the rest stays in the root |
| `*_control.go` (`index`, `inheritance`, `physical_not_null`, `sequence`, `identity`, `typed`) | 270, 133, 120, 13, 24, 32 | their feature steps (23, 24, 17 notes) |

## Part A

### 18a — In place

1. Re-measure: run `reach` on
   `'^(view_dump|control|domain_dump|policy_dump|sqljson_restore)\.go$'`
   and confirm the table above. Stop and report if a primitive turned
   dirty.
2. **Renames (one commit, file moves and renames only).**
   - `view_dump.go` → `dump_graph.go`.
   - `viewDumpOrder` → `dumpOrder`, `viewDumpReady` → `dumpReady`.
   - In `Engine.Dump`, the local `viewDependencies` → `graph`.
   - Move `commentLiteral` and `restoreScalar` from `control.go` into a
     new `dump_restore_scalar.go`, and `restoreSQLJSONQuery` beside them;
     delete `sqljson_restore.go`.
3. **`preflightACLItemDump` becomes a function** (`aclitem_dump.go:56`). It
   reads only `e.storage.Relations` and `e.visibility.CommitSeq()`. New
   signature:
   `preflightACLItemDump(ctx context.Context, view catalog.Read, relations map[ids.ObjectAddress]*storage.Relation, frontier ids.CommitSeq, output capturedACLItemOutput) error`.
   The call site (`control.go:133`) passes `e.storage.Relations` and
   `e.visibility.CommitSeq()`; it still runs under `Engine.mu`.
4. `validateTemporaryCollationDump` (`collation_seed_dump.go`) stays an
   `Engine` method: it builds a `CatalogStage` for
   `domainDependencyClosure`. Do not change it here.
5. Build, run the targeted units, commit.

### 18b — Create `dump` and move the records

1. Create `internal/engine/dump` with `errors.go` (a private `sqlError`
   copy, as every other subpackage has) and `README.md`.
2. Export and move, with `movex` or by hand (they are few):
   `dumpPhase` → `Phase` (constants keep their order: `Declaration`,
   `Rename`, `Defaults`, `Rows`, `InvalidConstraints`, `IndexAttachment`,
   `TableAttachment`, `TableCompletion`, `InvalidIndexPreparation`,
   `InvalidIndexCompletion`, `InheritanceRecipes`, `InheritanceChecks`,
   `RetiredConstraints`, `InheritedForeignKeys`, `DomainChecks`,
   `OperatorLinks`); `dumpEventKey` → `Event{OID ids.OID; Phase Phase}`;
   `type Graph map[Event][]Event`; `DeclarationEvents`, `DeclarationIDs`;
   `dumpOrder` → `Order(ctx, Graph, priorities map[ids.OID]ids.OID) ([]Event, error)`;
   the graph operations `addDumpDependency` and `dumpEventDependsOn`
   (`partition_dump_events.go`; 14 table, inheritance and type dump sites
   call them) → `Graph.Add` and `Graph.DependsOn`;
   `dumpDomain` → `Statement{Owner, SQL string}`; `dumpPolicyGroup` →
   `Group{Owner, Table, SQL string}`; `CommentLiteral`, `RestoreScalar`,
   `RestoreSQLJSONQuery`.
3. Declare the two capabilities (interfaces only; the root implements
   them):

   ```go
   // Resolver maps a dependency address to the OID of the dump event that
   // must precede consumer, or 0 when nothing must.
   type Resolver interface {
       Predecessor(view catalog.Read, address, consumer ids.ObjectAddress) (ids.OID, error)
   }

   // Presenter renders catalog values for dump statements. Every method is
   // a pure function of its arguments; implementations read no engine state.
   type Presenter interface {
       TypeDeclaration(view catalog.Read, reader catalog.DefinitionReader, u sqltype.Usage) (string, error)
       ColumnDeclaration(view catalog.Read, column catalog.Column) (string, error)
       RoutineIdentity(view catalog.Read, oid ids.OID) (catalog.RoutineIdentity, bool)
       // Literal and expression rendering: see the open question below.
   }
   ```

   Add a root adapter `dumpPresenter` (a zero-size type) that forwards to
   `catalogTypeDeclarationAt`, `catalogColumnDeclaration` and
   `routineIdentityByOID` (`regproc_lookup.go:9`, which returns
   `(catalog.RoutineIdentity, bool)`), and a root `dumpResolver` that
   forwards to `dumpPredecessor`.
4. Rewrite root uses to `dump.X`. Locals named `dump` in root code shadow
   the package: rename them first (`shadowrn`).
5. Imports of `dump`: `ids`, `catalog`, `parser`, `expr`, `sqltype`,
   `sqlstate` and the standard library. Check with
   `go list -f '{{join .Imports " "}}' ./internal/engine/dump`.
6. No aliases: the set is small, qualify every reference directly.

**Open questions for 18b (settle in the session, record the answer in
Status):**

- `catalogLiteralSQL` takes a `plan.LiteralExpr`. Either `Presenter`
  gains `Literal(ctx, view, value sqltype.Value, usage sqltype.Usage)` and
  the adapter rebuilds the call, or `dump` imports `plan` and sits at L6.5
  (nothing below L6 needs it). Prefer the first; take the second only if
  the first changes rendering.
- `domainDumpStatement` renders domain checks through `scalarRenderer`.
  15e needs a `Presenter.CheckSQL`-style method for it; add it in 15e, not
  here, so the interface only grows with a caller.

### 18c — Registration tables in `Engine.Dump` (pattern 5)

`Engine.Dump` (`control.go:73`–`1661`) has two central switches:

- **Switch #1** (`control.go:265`–`457`): a type switch over every
  non-relation descriptor (`Database`, `Parameter`, `Tablespace`,
  `Operator`, `Procedure`, `TSTemplate`/`TSDictionary`, `Extension`,
  `Type`, `Collation`, `Conversion`, then namespaces by `Kind()`), followed
  by the relation branches (`CompositeRelation`, `SQLIndex`, `View`,
  `SequenceRelation`, tables with `Inheritance`; `control.go:457`–`800`).
  It runs under `Engine.mu`, which is released by hand on about 60 error
  paths.
- **Switch #2** (`control.go:1095`–`1190`): the emission loop over
  `eventOrder`, with per-phase branches (`dumpOperatorLinks`,
  `dumpDomainChecks`, `dumpTableCompletion`, the invalid-index phases, the
  rename and attachment phases, and the table phases dispatched to
  `dumpRelation` emitters).

Do this:

1. **Capture function.** Move the locked part (`control.go:80`–`800`) into
   `func (e *Engine) captureDumpLocked(ctx) (dumpCapture, error)`. The
   caller takes `Engine.mu`, calls it, and unlocks once. The ~60 manual
   `e.mu.Unlock()` calls disappear. `dumpCapture` holds the slices and
   maps that the switch fills today (operators, SQL routines, extensions,
   domains, composites, collations, conversions, schemas, schema comments,
   tables, index and view edges, sequences, priorities, `foreignNamespaces`
   reads).
2. **Collect table.** Replace switch #1 with an ordered slice of entries:

   ```go
   type dumpCollector struct {
       name    string // for tests and error context only
       matches func(catalog.Descriptor) bool
       collect func(*dumpCapture, ids.ObjectAddress, catalog.Descriptor) error
   }
   ```

   The table lists the cases in today's order; the first matching entry
   handles a descriptor, as the type switch does. Keep each case body
   unchanged inside its `collect`. Relation kinds get entries too, in the
   order of today's `if` chain.
3. **Emit table.** Replace switch #2 with an array indexed by `dump.Phase`:
   each entry gives the owner rule (`"postgres"` or the table owner) and an
   emitter `func(ctx, dump.Event, emit func(string) error, setOwner func(string) error) error`.
   The trailing fixed blocks after the loop (sequence completion,
   materialized snapshots, policies, RLS, rules, attribute ranks) become an
   ordered `[]dumpSection` list.
4. Entries are root functions in this sub-step. Feature packages take over
   their entries in their Part A moves (14e, 15e, 23b, 24b, 25b, 27b); the
   table itself names no feature internals.
5. Run the byte-identity smoke (below) before and after each commit.

**Invariants specific to 18c:** the outer loop iterates
`catalog.State.Descriptors()`, a map, so descriptor order is already
random; only the per-descriptor check order and the later sorts decide
output. Keep every sort call where it is. The same first error must win
for one descriptor.

## Invariants

- Dump output is byte-identical: `Phase` values and the `Order` tie-break
  (priority, then OID, then phase) are unchanged; so are the emission
  sequence, the owner switches (`SET SESSION AUTHORIZATION`) and every
  error text.
- Dump reads engine state only inside its `Engine.mu` window
  (`control.go:80`–`800`); everything after reads captured immutable
  views. `captureDumpLocked` keeps that boundary exactly.
- `restoreScalar` keeps its two modes (stored CREATE expressions keep the
  binder's admission rules; INSERT values keep the Restore whitelist) and
  rejects every parameter.
- `dump` holds no state, no mutex and no engine callback.

## Targeted tests

Tests follow the code and never block a move (ENGINE-SPLIT §4): tests of
moved pure code move into `dump`; engine tests are rewritten to the new
names.

- Engine units: grep `*_test.go` for `viewDumpOrder`, `dumpEventKey`,
  `dumpDeclarationEvents`, `restoreScalar`, `dumpPredecessor`,
  `preflightACLItemDump`; the dump-heavy files include
  `partition_dump_events_test.go`, `operator_dump_test.go`,
  `conversion_dump_test.go`, `rule_named_dump_test.go`,
  `rule_inference_dump_test.go`, `index_property_dump_test.go`. Unit tests
  of `Order` and `RestoreScalar` that need no engine move into `dump`.
- Reference selections, quick mode and `REFERENCE=1` (names from
  `reference/`; never edit `reference/` or `smoke/`):
  `make check RUN='^TestReference.*(Dump|Restore|Native|RoundTrip|Replay)'`
  (about 230 tests) and `make check RUN='^TestReferenceControl'`; with
  `REFERENCE=1` at least `TestReferenceDumpRoundTrip`,
  `TestReferenceTypeCoreDumpRoundTrip`,
  `TestReferenceControlFailedRestorePreservesCatalog`,
  `TestReferenceCollationSeedNativeReplay` and
  `TestReferenceCompositeAlterDumpReplay`. A `REFERENCE=1` failure that
  also fails on a `master` snapshot is oracle drift, not a regression;
  report it.
- **Byte-identity smoke** (required for 18b and 18c). 18b wrote one:
  `.cswd/tmp/PG18-ENGINE-SPLIT-DUMP-PACKAGE/dumpsmoke/` (point its
  `go.mod` replace at your worktree; run it before the first edit to get
  the baseline). Otherwise write a small
  program under `.cswd/tmp/<task>/dumpsmoke/` (model:
  `.cswd/tmp/PG18-ENGINE-SPLIT-PGCATALOG-MOVE/smoke/main.go`) that opens a
  database with the root driver, runs a fixed corpus (schemas, enums,
  domains with defaults and checks, composites, collations, operators, SQL
  functions, tables with identity, generated columns, CHECK, FK, NOT NULL,
  indexes, partitions, inheritance, views, materialized views, rules,
  policies, grants, comments), calls `stuntgres.Dump`, restores into a
  fresh database with `stuntgres.Restore` and dumps again. Run it on a
  detached `master` worktree and on the branch; both dumps must match byte
  for byte.
- `make bench` is not needed: startup does not change.

## Documentation

- Create `internal/engine/dump/README.md` (18b): what the package owns
  (the event graph, its order, the statement and group records, Restore
  scalar validation), the `Resolver` and `Presenter` contracts (pure, no
  engine state, no locks), what stays in the root (`Engine.Dump`,
  `dumpPredecessor`, the adapters), and `## Testing` with the selectors.
- 18c: describe the Collect and Emit tables in the `dump` README and in
  the root `internal/engine/README.md` only where it names `Dump` internals.
- `AGENTS.md`: add `dump` to the subpackage brace list (18b). The file must
  not grow.
- Tick 18a–18c in Status and in
  [`ENGINE-SPLIT-STATUS.md`](ENGINE-SPLIT-STATUS.md); update the §8 cell.

## Done when

`dump` owns the graph records, the order, `Statement`, `Group`,
`CommentLiteral`, `RestoreScalar` and the two capability interfaces, and
imports nothing above L3.5 (or L6.5 if 18b chose `plan`). `Engine.Dump`
collects and emits through ordered tables and unlocks `Engine.mu` in one
place. Dump output is byte-identical and the checks in §8.3 pass.

## Status

- [x] 18a — In place (renames, `preflightACLItemDump` as a function)
  (`35544f886`, `c201b0a69`, then the docs commit). Re-measured at
  `1dad6120d`: every primitive still clean, line numbers unchanged.
  `view_dump.go` is `dump_graph.go` (`dumpOrder`, `dumpReady`; the local
  graph in `Engine.Dump` is `graph`); `commentLiteral`, `restoreScalar` and
  `restoreSQLJSONQuery` moved unchanged into `dump_restore_scalar.go`
  (`sqljson_restore.go` deleted); `preflightACLItemDump` takes the relation
  roots and the commit frontier, passed by `Engine.Dump` under `Engine.mu`.
  Tests adapted (renames only): 10 engine test files calling `dumpOrder`.
  Targeted units, quick-mode `^TestReference.*(Dump|Restore|Native|RoundTrip|Replay)`
  and `^TestReferenceControl`, `REFERENCE=1` for the five tests named under
  Targeted tests plus `TestReferenceACLItemDumpSnapshot`, and quickcheck
  passed; racecheck/finalcheck skipped at user request.
- [x] 18b — `dump` created; records, order and capabilities moved
  (`fd27cb76f`, `6d4ca83c9`, `929c3a8d0`, `4fced3bba`, then the docs
  commit). Re-measured at `b5cdd1efe`: every primitive still clean.
  `dump` owns `Phase` (same numeric order), `Event{OID, Phase}`, `Graph`
  with `Add`/`DependsOn` (were `addDumpDependency`/`dumpEventDependsOn`),
  `DeclarationEvents`/`DeclarationIDs`, `Order`, `Statement{Owner, SQL}`,
  `Group{Owner, Table, SQL}`, `CommentLiteral`, `RestoreScalar`,
  `RestoreSQLJSONQuery`, `Resolver` and `Presenter`
  (`TypeDeclaration`, `ColumnDeclaration`, `RoutineIdentity`). Root
  adapters: `dumpResolver` in `dump_resolver.go` (was `dump_graph.go`;
  it keeps `dumpPredecessor`) and `dumpPresenter` in
  `dump_presenter.go`. Field renames were type-checked (`fieldrn`); the
  34 spelled-out graph maps are `dump.Graph`. No aliases.
  - Open questions settled: no dump producer moving in 14e/15e calls
    `catalogLiteralSQL`, so `Presenter` has no `Literal` method yet and
    `dump` does not import `plan` (it stays at L3.5); 15e adds
    `Literal(ctx, view, value, usage)` if it needs one. The check-rendering
    method stays with 15e.
  - Deviations: `dump` imports `functions` (L3; `RestoreScalar` uses
    `RegexpSignature` and `PosixName`) and `sqltype` (the `Presenter`
    signatures), not `sqlstate`; no `errors.go`, because nothing in `dump`
    builds an SQLSTATE error of its own (`RestoreScalar` uses
    `expr.ErrorAt`), so a private `sqlError` copy would be dead code.
  - Tests moved into `dump`: `TestPartitionAttachDumpPhaseScheduler` →
    `TestOrderPhaseScheduler`, `TestArrayExpressionRestoreVisitorModes` →
    `TestRestoreScalarSubscriptModes`,
    `TestQueryTableCreateRestoreReferenceLiteralBoundary` →
    `TestRestoreScalarReferenceLiteralBoundary`,
    `TestCastRestoreRecursiveParameterGuard` →
    `TestRestoreScalarCastParameterGuard` (bodies unchanged apart from
    names and an inline parser helper). Engine tests adapted (names only):
    16 files.
  - Verification: targeted units after each commit; the byte-identity
    smoke (`.cswd/tmp/PG18-ENGINE-SPLIT-DUMP-PACKAGE/dumpsmoke`: five
    scenarios covering types, domains, collations, conversions, SQL
    functions, operators, tables with identity, generated, CHECK, FK and
    NOT NULL, indexes, inheritance, partitions, views, matviews, rules,
    policies and comments, each dumped, restored and dumped again)
    matched the pre-change baseline byte for byte after every commit;
    quick-mode `^TestReference.*(Dump|Restore|Native|RoundTrip|Replay)`
    and `^TestReferenceControl`, `REFERENCE=1` for the six tests named
    under 18a, and quickcheck passed; racecheck/finalcheck skipped at
    user request.
- [x] 18c — Collect and Emit tables in `Engine.Dump` (`82ccf9f5c`,
  `5342c324b`, `56731f78c`, `45eac6657`, `4beca3bd7`, then the docs
  commit). Measured at `205d59485`: `Engine.Dump`
  was `control.go:72`–`1659` with 60 manual unlocks; it is now capture,
  build, emit (about 30 lines), and `control.go` fell from 2,288 to 729
  lines.
  - **Capture** (`dump_capture.go`): `captureDumpLocked` returns a
    `dumpCapture`; `Dump` unlocks `Engine.mu` once. The pre-loop checks are
    `dumpPortable` and `dumpBuiltinSchemaPreamble`, in today's order.
  - **Collect tables** (`dump_collect.go`, `dump_collect_relation.go`):
    `dumpCollectors` (relation, database, parameter, tablespace, operator,
    procedure, text search, extension, type, collation, conversion,
    namespace; no match is `unsupported catalog descriptor kind`) and
    `dumpRelationCollectors` (composite, index, view, sequence, table)
    after the shared relation prelude. Case bodies moved unchanged.
  - **Build** (`dump_script.go`): `buildDumpScript` returns a `dumpScript`
    (groups, statements, both `Order` passes and the retry, declarations,
    sequence statements).
  - **Emit** (`dump_emit.go`, `dump_emit_declaration.go`,
    `dump_sections.go`): `dumpPhaseEmitters` indexed by `dump.Phase`,
    `dumpSections` for the whole script, `dumpWriter` for text and owner.
    The CREATE TABLE text is built by `dumpTableCreateSQL` before anything
    is written.
  - **Signatures settled (decision 15):** collectors are
    `{name, matches(catalog.Descriptor) bool, collect(ctx, *dumpCollection, ids.ObjectAddress, catalog.Descriptor) error}`
    — the prompt's sketch gained `ctx`, because index and view entries
    render SQL; relation entries also get the prelude's
    `relationDumpContext{namespace, owner}`. Phase entries are
    `{owner func(*dumpScript, dump.Event) string; emit func(ctx, *dumpScript, *dumpWriter, dump.Event) error}`
    — the writer carries `emit`/`setOwner`, and a `nil` owner means the
    emitter switches authority itself (`Declaration`; `DomainChecks`,
    which renders before `SET` so a rendering error writes nothing).
    Sections are `{name, emit(ctx, *dumpScript, *dumpWriter) error}`; the
    prompt asked only for the trailing blocks, the leading ones joined so
    the list is the whole script.
  - **Non-verbatim substitutions** (each equal by construction): the view
    entry's fresh `e.catalog.read(e.catalog.state)` is the capture's
    `originalCatalog` (the same `catalog.NewRead(owner, state)` under the
    same lock); `e.roles.domain.Principal(10)` is
    `Principal(authorization.BootstrapPrincipalID)` (10); every inline
    `if owner != currentOwner { SET ...; currentOwner = owner }` is
    `dumpWriter.setOwner`, and the two unconditional ones are
    `forceOwner`; `sequence.DumpStatements` runs at the end of
    `buildDumpScript` instead of between the schema comments and the
    sequence creation (it only sorts the captured sequence list).
  - Tests: none moved, added or rewritten. No unit test pins the tables'
    wiring (AGENTS.md); the smoke and the reference selections pin the
    output.
  - Verification: the byte-identity smoke
    (`.cswd/tmp/PG18-ENGINE-SPLIT-DUMP-TABLES/dumpsmoke`, the five 18b
    scenarios plus one with extensions, builtin schema renames and owner
    changes, a non-postgres domain and inherited-table owner, a typed table,
    a materialized view and a deferred regclass default, and five error
    scenarios pinning first-error texts) matched the `205d59485` baseline
    after every commit and after the rebase onto `8b70be811`; targeted units and quick-mode `*Dump*` after every
    commit; quick-mode `^TestReference.*(Dump|Restore|Native|RoundTrip|Replay)`,
    `^TestReferenceControl` and `RACE=1 ^TestReferenceControl`;
    `REFERENCE=1` for the six tests named under 18a; quickcheck passed;
    racecheck/finalcheck skipped at user request.

Outstanding aliases: none (18b and 18c added none).

### Notes for later steps

- 14e (routine) and 15e (typedef) move their dump statements onto
  `dump.Statement`, `dump.Resolver` and `dump.Presenter`; each adds only the
  `Presenter` methods its statements call.
- `dumpRelation` (`table_dump.go:21`, 15 methods in 9 files) is the table
  dump record; it moves with `table` (24b), not here.
- 18c entries per feature step: 14e takes the operator and procedure
  collectors and the `OperatorLinks` emitter; 15e the type, collation and
  conversion collectors and the `DomainChecks` emitter; 23b the index
  collector and `emitDumpIndex`; 24b the table collector,
  `tableDumpPredecessors`, `emitDumpTable`/`dumpTableCreateSQL` and the
  table-phase emitters; 25b the attachment and invalid-index emitters;
  27b the view collector and the materialized-snapshot section. A moved
  entry keeps its position in the root table.
- 30a can reuse the shape: Restore's hook table is the same "ordered
  entries, first match wins" list; 30c moves `dumpScript` and the emit
  files with the drivers, while `captureDumpLocked` and `dumpSource` stay
  in the root.
- `validateTemporaryCollationDump` stays in the root until the domain
  dependency closure gets a reader (pattern 6); 30b decides.
- Restore uses the same records; its statement switch (#3) and hooks are
  step 30 (30a).
- `dumpPredecessor` reaches partition, operator-shell and competing-index
  helpers and `routine.OperatorSeeded`, so it can never move into `dump`
  (L3.5 is below `routine`). After 22a re-run `reach` on it: if it is
  clean, it can move with `control` (30c) as the root's `Resolver`.
