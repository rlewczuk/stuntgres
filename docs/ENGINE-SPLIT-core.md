# Engine split: `internal/engine/core`, seventh round

Session prompts for linear items 182, 188–192, 195 and 204 of the
[status page](ENGINE-SPLIT-STATUS.md). The rules are in
[ENGINE-SPLIT.md](ENGINE-SPLIT.md): §2, §3 (layer L15, rules 1–4 and 8) and §5.

Core is what the runtime needs without the family executors. `closure` computes
it: the closure of the exported root API and the interface methods of reached
types, with the dispatch edges out of `Engine.executeStatement` cut. At
`26f8fea15` it was 28.5k declaration lines (`assign.tsv`, `summary.txt` in
`.cswd/tmp/PG18-ENGINE-SPLIT-CLOSING-REVIEW-4/r2/`). Re-run `closure` at the
start of every item.

## Item 182 — export pass in place

Needs 181. Mode S.

- **What to export.** Export, in the root and under their final names:
  - the unexported core members that family, shared and root code use:
    `exportset.tsv`, 345 names at `26f8fea15` (179 declarations and 166 struct
    fields);
  - the 25 test-inspection names that `waves.py` lists, such as `engineOf`,
    `Engine.publishCatalog`, `engineCatalogRuntime`, `captureCatalogSourceRows`,
    `StatementScope.acquireRelation` and `replacementAdmission.TryLock`.
- **Names.** Capitalize. Build the clash table with `expspec` and record it in
  the Status line. A field whose exported name clashes with a method or a
  promoted name takes a descriptive name: `roles` clashes with
  `Engine.Roles()`, so it becomes `RoleStore`.
- **Tools.** `expspec` plus `movex` member mode, and `fieldrn` for fields. The
  rewrites cover `./internal/engine/...`, the root `stuntgres` package and
  `cmd/stuntgres`. Generate the commit from a replay script.
- **`enginetest` stage 2.** Add the shared inspection helpers that tests in two
  or more wave destinations use, for example role operations and table refs,
  session SQL, and lock and catalog inspection. Base them on the newly
  exported names, or on SQL where that observes the same thing (`pg_locks`).
- **Frame names.** Grep the 14 `runtime.Callers` test files for frames this item
  renames, and update those strings in the same commit.

**Verify:**
- build, and `go test -run '^$' ./internal/engine/...`;
- the string-literal multiset is unchanged except for frame strings;
- `make quickcheck`;
- bench skipped (renames only), and `REFERENCE=1` skipped (no behaviour change);
- merge in a freeze window.

## Item 188 — composition seams

Needs 182. Mode P, with bench.

1. **Dispatch.** Declare a `Statements` interface. It lives in the root for
   now, and moves to core in item 195. It has two methods:
   - unlocked family dispatch: the type switch at `execute.go:32–197`, minus
     the cases `closure` assigns to core (LISTEN/NOTIFY, PREPARE and
     DEALLOCATE, FETCH and CLOSE, DO, DISCARD, SET, SHOW and RESET, setting
     calls, TRUNCATE);
   - locked family dispatch: the `…Locked` handlers at `execute.go:237–262`,
     with ALTER TABLE, ALTER TABLE DEFAULT, DROP TABLE and CREATE TABLE. It is
     a lock-domain continuation: it is entered with `Engine.mu` held and
     releases it on every return.

   A root type implements both in today's case order, and a field on `Engine`
   holds it. Keep the fall-through code (the DeclareCursor checks,
   `scope.completed`) where it is.
2. **Factory hook.** `database_create.go:57` builds the `template1` seed with
   `newEngine`. Give `Engine` a factory field set by the composition.
3. **Reset, Dump and Restore** (`control.go`) become root functions
   (`engine.Reset(ctx, e)`, …). The driver's call sites change in the same
   commit. `principal_remap.go:74` (`restoreStage`) uses the composition
   constructor.
4. **Split `newEngine`.**
   - State seeding moves into one function that becomes `core.NewEngine`: the
     stores, settings, visibility, locks, roles and the builtin providers,
     including `relationProvider`.
   - Registering the statements, validators and factory is composition.

**Verify:**
- `make bench`: `BenchmarkOpenDatabase` and a per-statement benchmark, with no
  new allocation;
- quickcheck;
- `REFERENCE=1` selection: `^TestReferenceControl` (Reset, Dump and Restore
  witnesses) and `^TestReferenceDatabaseCreateControls$`.

## Item 189 — core hooks

Needs 188. Mode P, with bench.

Take each "family code pulled into core" entry from `closure`'s `summary.txt`
and decide it by R4:
- **Hook it** if it reaches at least 150 lines that only one family needs, and it
  is not on a per-row path;
- **keep it** in core otherwise.

At `26f8fea15`:
- **Hook.** `Engine.ValidateCatalogStage` reaches about 1.1k lines of family
  validators: schema definitions 413, types 321, sequences 91, views 76,
  inheritance 64, tables 40, indexes 36, partitions 24. They become an ordered
  validator table (rule 8) in today's order. It is entered with `Engine.mu`
  held.
- **Decide.**
  - `Engine.publishCatalogOperation` (132, schema objects);
  - `catalogDependencyPreflight` (115);
  - `dropProvider.removeObjectLocked` (87);
  - `Engine.catalogPreflightLocked` (80).
- **Keep in core:**
  - relation admission: `acquireRelation`, `acquireRelationTarget`, about 0.9k;
  - the runtime hosts: `statementHost`, about 1.2k;
  - the TEMP lifecycle: `preserveTemporaryLocked`, `prepareTemporaryCommit`,
    `sessionHost.EndMaintenance`;
  - the bound path and query-source capture;
  - COPY, protocol and rule programs.

**Verify:**
- re-run `closure` and record the new core size;
- publication bench, with no new allocation;
- quickcheck;
- a `REFERENCE=1` selection of catalog-validation failures (`tables`, `types`).

## Item 190 — host and slot splits

Needs 189. Mode P.

- **`ddlHost`.** It has 89 methods in 12 files and implements nine interfaces:
  `ddl.Host`, `ddl.SchemaHost`, `index.Host`, `index.KeyHost`,
  `partition.Host`, `fdw.Host`, `table.Host`, `typedef.StageHost` and
  `routine.DDLHost`.
  - Keep a core base with the methods that core paths use; `closure -nodyn
    ddlHost` shows them.
  - Add family host types with the same `{e, op, stage}` layout for the
    family-only interfaces, built by family code.
  - Measured effect: core shrinks by 1.2k lines.
- **Family-only adapters.** `extensionHost` (17 methods) and `alterHost` (10)
  stay root and move with their families. `plHost` is core.
- **Feature slots.** Each slot used by only one family becomes an opaque slot
  that core declares and the family owns (rule 7); runtime slots stay core:
  - `StatementScope`: `sqlRoutine`, `inheritance`, `protocolSlot`,
    `presentationSlot`, `dropsSlot`, `foreignTablesSlot`, `ruleAction`;
  - `CatalogStage`: `extensionNamespace`, `schemaCascadeNames`,
    `dropCascadeNames`, `domainBases`, `domainAlter`, and the database fields.

**Verify:**
- `closure`: no family-only host method is left in core;
- quickcheck;
- bench, if a slot is on a per-statement path.

## Item 191 — family methods to functions I

Needs 183–187 and 190. Mode P.

- **Methods to functions.** Turn the god-struct methods (`Engine`,
  `StatementScope`, `CatalogStage`) that `closure` assigns to these families
  into receiver-first root functions with `meth2fn` (pointer receivers):
  - tables, indexes, inheritance, partitions, sequences, identity and VACUUM,
    plus the code they share with tables;
  - about 126 methods at `26f8fea15`.

  Methods that implement an interface stay methods of their host type.
- **Split mixed files.** Split every root file that mixes core and family
  declarations (`movedecl`), so that each file is wholly core, wholly root or
  wholly one family.
- **Watch.** `meth2fn` misses pointer-receiver call sites nested in other
  rewritten calls; fix those by hand.

**Verify:** build, tests and quickcheck; bench if a converted function is on a
per-row path.

## Item 192 — family methods to functions II

Needs 191. Mode P.

The same as item 191 for the other families, about 100 methods: types, views,
rules, policies, triggers, roles, schema objects, routines, operators, casts,
extensions and FDW.

**Gate.** `closure` reports:
- no family declaration that is a god-struct method;
- every root file is wholly core, root or one family;
- the only core→family edges are dispatch and the item-189 hooks.

## Item 195 — core move

Needs 194. Mode S, with bench.

- **Production.** Move every declaration that `closure` assigns to core into
  `internal/engine/core` (package `core`), about 26k lines, together with
  `Statements`, the hooks and the factory. Move the root `inspect.go` to
  `core/inspect.go`.
- **What the root keeps:**
  - the composition: `New*`, the dispatch implementation, the factory, Reset,
    Dump and Restore, and the Restore hooks;
  - the family functions, now over `*core` types;
  - the family host types;
  - their tests.
- **Tests.**
  - Core-only units, which build no engine, move as internal tests.
  - `inspect_*_test.go` becomes `core/export_test.go`, and its users move to
    `core/` as `core_test`.
  - The wave tests already in `core/` change `engine.X` to `core.X` for moved
    names.
- **Driver.** The root `stuntgres` package and `cmd/stuntgres` import `core`
  for types and runtime methods (rule 2).
- **Docs.** Split the root README: most sections become the core README, and
  the root keeps the composition contract.
- **Tools.** `movespec` and `movex -qual core`, `git mv`, `impfix`, all driven by
  a replay script and merged in a freeze window.

**Verify:**
- build and `go test -run '^$' ./...`;
- quickcheck;
- `make bench`: `BenchmarkOpenDatabase` and the per-statement and per-row
  benchmarks;
- the §3 `go list` import check and `make deps`;
- a `REFERENCE=1` selection across `sessions`, `dml` and `tables`.

## Item 204 — composition cleanup

Needs 203. Mode S.

- Root tests that are left: keep the composition-level ones (`New*`, dispatch
  order, Restore hooks). Move or delete the others under T2 and T3.
- Remove the `core/inspect.go` accessors that no test uses any more, and
  unexport names that only tests use (`closure`).
- Check the §3 import rules with `go list`, then finish the root and core
  READMEs. Measure the root bytes.

**Verify:** quickcheck, a `REFERENCE=1` smoke selection, bench (startup).

## Status

- 182: not started.
- 188: not started.
- 189: not started.
- 190: not started.
- 191: not started.
- 192: not started.
- 195: not started.
- 204: not started.
