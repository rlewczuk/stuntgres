# Engine split — status

One-page progress tracker for the `internal/engine` split. The plan is
[ENGINE-SPLIT.md](ENGINE-SPLIT.md). Each step has its own prompt file, and the
Status section of that file is the detailed record: commits, aliases,
corrections and notes. This page only shows what is done and what comes next.

Last verified against `master` at `081ea0328`.

## Next ready sub-steps

- **2d** — remove the `expr` aliases (needs 2c ✓) *(alias removal)*.
- **F2** — convert the remaining switches to the node protocol (needs 2c ✓)
  *(broad rewrite)*.
- **4c** — remove the `lock` aliases (needs 4b ✓) *(alias removal)*.
- **F4/F5** — port Tier A, Tier B and aggregate families one at a time
  (needs F3 ✓).
- **5b** — typed read API, map reads migrated, writes confined, cut 1
  (needs 5a ✓) *(broad rewrite)*.

They may run in parallel on separate branches, except that 2d, 4c, F2 and 5b
must not run alongside each other or another broad rewrite or alias removal
(§8).

## Task tree

- [x] **Step 1 — `ids`** ([prompt](ENGINE-SPLIT-ids.md); needs —)
  - [x] 1a — Domain replaces the `*Engine` back-pointers, cuts 3, 5, 10
    (`6dead324d`, `399abdd78`)
  - [x] 1b — Owner handles replace `attachment` and `private`, cuts 4, 6
    (`f39333b2f`)
  - [x] 1c — `ids` created, declarations moved, aliases in place
    (`9a56cf39d`, `3993c587c`, `ebb5ee0dd`, docs `27a4b6bb2`)
  - [x] 1d — Aliases removed (`282b6608a`, `369dc435f`, `3d31f69ba`,
    `01bb004d6`, `77a7d4bd0`, `e799ad1e3`, `ebdcd8c6b`, docs `b859670f7`)
- [ ] **Step 2 — `expr`** ([prompt](ENGINE-SPLIT-expr.md); needs 1)
  - [x] 2a — Evaluation through `ExprContext`; `routineRecipe` cut, cut 7
    (`52d15cad4`, `a7bc48424`, `d0fb3f388`, docs `19466ff87`; merged in
    `e884b9126`)
  - [x] 2b — `expr` created, contract moved, aliases in place (`dced86ba6`,
    `9e1ba1dae`, docs `8f542f797`)
  - [x] 2c — Node protocol landed with the walk switch as first adopter
    (`328df6341`, `aafd6fe97`, `f038faf09`, docs `767e1293e`)
    *(full racecheck skipped at user request)*
  - [ ] 2d — Aliases removed *(broad rewrite)*
- [ ] **Step 3 — `functions`** ([prompt](ENGINE-SPLIT-functions.md); needs
  2b, and 2c for F2)
  - [x] F1 — Generics removed (`69aeb6167`, `5ec01573f`, docs `937962c4f`)
  - [ ] F2 — Switches converted to the node protocol *(broad rewrite; needs
    2c)*
    - [ ] retag
    - [ ] remap
    - [ ] volatility
    - [ ] deparse
    - [ ] node tree
    - [ ] generated admission
    - [ ] conditional plan
    - [ ] join legality
  - [x] F3 — `Binder`, `Definition`/`Lookup` and engine dispatch; pilot
    family: UUID extraction (`60dc1cedd`, `cd4a88387`, `5d59e5737`, docs
    `9bca9b2b6`)
  - [ ] F4/F5 Tier A — port in place, then move
    - [ ] math
    - [ ] string
    - [ ] LIKE/SIMILAR/regex
    - [ ] datetime
    - [ ] JSON/JSONB/SQL-JSON
    - [ ] arrays
    - [ ] ranges
    - [ ] UUID
    - [ ] format
    - [ ] binary
    - [ ] numeric formatting
    - [ ] pgcrypto
    - [ ] fuzzystrmatch
    - [ ] unaccent
    - [ ] intarray
    - [ ] intagg
    - [ ] uuid-ossp
  - [ ] F4/F5 Tier B
    - [ ] random
    - [ ] current time and clocks
    - [ ] sleep
    - [ ] settings reads
  - [ ] F4/F5 aggregates and windows
  - [ ] Tier C refined *(after steps 4, 5 and 7)*
- [ ] **Step 4 — `lock`** ([prompt](ENGINE-SPLIT-lock.md); needs 1)
  - [x] 4a — Cut 11, `EndReason`, mutex encapsulation, lock-order audit
    (`78265dd49`, `d4a96abb9`, `e9c2356cd`, docs `ad4693205`)
  - [x] 4b — `lock` created and declarations moved (`6fe4ccdc6`,
    `ff15343fb`, `f2887fb33`, `8192746a5`, `dd7b1b796`, then the docs commit)
    *(full racecheck skipped at user request; targeted RACE=1 runs passed)*
  - [ ] 4c — Aliases removed
- [ ] **Step 5 — `catalog`** ([prompt](ENGINE-SPLIT-catalog.md); needs 1,
  2b)
  - [x] 5a — `catalogStore` state ownership (`41d5fbda9`, `9a0e50246`,
    then the docs commit) *(full racecheck skipped at user request; targeted
    RACE=1 runs passed)*
  - [ ] 5b — Typed read API, map reads migrated, writes confined, cut 1
    *(broad rewrite)*
  - [ ] 5c — View, rule, policy, extension-provider and expression payload
    seams
  - [ ] 5d — Descriptor names decided, fields exported *(broad rewrite)*
  - [ ] 5e — `catalog` created, types moved, `Candidate` split from
    `CatalogStage`
  - [ ] 5f — Aliases removed *(broad rewrite)*
  - [ ] 5g — Optional `relationDescriptor` regrouping *(needs user approval)*
- [ ] **Step 6 — `storage`** ([prompt](ENGINE-SPLIT-storage.md); needs 5e)
  - [ ] 6a — `storageStore` state ownership
  - [ ] 6b — `storage` created and types moved
  - [ ] 6c — Aliases removed *(broad rewrite)*
- [ ] **Step 7 — `visibility`** ([prompt](ENGINE-SPLIT-visibility.md); needs
  4 and 6 complete)
  - [ ] 7a — `visibilityRegistry` state ownership
  - [ ] 7b — `visibility` created and types moved
  - [ ] 7c — Aliases removed *(broad rewrite)*
- [ ] **Authorization and `access`**
  ([prompt](ENGINE-SPLIT-authorization.md); Part A needs —, Part B needs 5e)
  - [x] Part A
    - [x] A1 — Pure helpers moved, README rewritten (`e932fc381`,
      `96ee8d96b`, docs `31aa39dfb`)
  - [ ] Part B
    - [ ] B0 — `access` refined and approved (no code changes)
    - [ ] B1 — Authorization state grouped
    - [ ] B2 — Checks ported to functions over catalog and authorization
      reads
    - [ ] B3 — `access` created and code moved
    - [ ] B4 — Aliases removed *(broad rewrite)*
- [ ] **Later waves** ([ENGINE-SPLIT.md §6](ENGINE-SPLIT.md#6-later-waves-sketch);
  sketch only, no prompt files yet)
  - [ ] `pgcatalog` — system relations `pg_*` (needs 5)
  - [ ] `settings` — GUC definitions and validation (needs 5)
  - [ ] `plan`, `exec` — plan IR, executor, concrete engine nodes (needs 2, 5,
    6, 7 and the `EvalContext` split)
  - [ ] `analyze` — `expressionBinder` and statement binding (needs `plan`,
    3)
  - [ ] `session` — sessions, operations, prepared SQL, transaction owner
  - [ ] Feature packages — index, partition, inheritance, sequence, types,
    views/rules/policies, FDW, extensions, routines, DML
  - [ ] `control` — native Reset/Dump/Restore through a remap protocol (last)

`access` is tracked under Part B above, not under the later waves.

## Updating this file

Every split session updates this page together with its prompt file's Status
section, in the same commit that records the sub-step (§8.4).

1. **Before starting**, check the tree against `git log`. If they disagree,
   trust `git log` and the prompt file's Status section, fix this page, and
   say so in the report.
2. **When a sub-step is done** (verified under §8.3 and documented under
   §8.4):
   - tick its box and add its commit hashes in parentheses, with the docs
     commit marked `docs`, the same way as the done items above;
   - tick the parent step only when all of its non-optional sub-steps are
     ticked (5g is optional);
   - for F2 and F4/F5, tick each switch or family as it lands, and the group
     line when all of its children are ticked.
3. **Leave a sub-step unticked** if any of its §8.3 checks failed or were
   blocked, for example `REFERENCE=1` without Docker. Add a short note after
   the item instead, such as `*(blocked: REFERENCE=1 needs Docker)*`.
4. **When a session changes the plan**, change the tree to match:
   - F3 names its pilot family in place of `(name)`;
   - B0 replaces or refines the B1–B4 items it rewrote;
   - Tier C refinement adds its items under "Tier C refined";
   - a later wave that gets a prompt file gets a link and its sub-steps here.
   Do not add items that are not in a prompt file or in ENGINE-SPLIT.md.
5. **Rewrite "Next ready sub-steps"**: list each unticked sub-step whose
   prerequisites are all ticked. Prerequisites are in each prompt file's
   "Needs" line and in the §8 table. Mark broad rewrites, which must not run in
   parallel with each other (§8).
6. **Update the "Last verified" line** with the `master` commit you checked
   against.

Keep this page short. Corrections, outstanding aliases, moved tests and notes
for later steps go in the prompt file, not here.
