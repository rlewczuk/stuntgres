# Engine split — status

One-page progress tracker for the `internal/engine` split. The plan is
[ENGINE-SPLIT.md](ENGINE-SPLIT.md). Each step has its own prompt file, and the
Status section of that file is the detailed record: commits, aliases,
corrections and notes. This page only shows what is done and what comes next.

Last verified against `master` at `80de2df73`.

## Next ready sub-steps

- **7c** — `visibility` aliases removed (needs 7b ✓). Broad rewrite; see
  the 7b notes in the prompt file.

Authorization and `access` are done. Step 3 Tier C refinement waits for
step 7 (after 7c).

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
- [x] **Step 2 — `expr`** ([prompt](ENGINE-SPLIT-expr.md); needs 1)
  - [x] 2a — Evaluation through `ExprContext`; `routineRecipe` cut, cut 7
    (`52d15cad4`, `a7bc48424`, `d0fb3f388`, docs `19466ff87`; merged in
    `e884b9126`)
  - [x] 2b — `expr` created, contract moved, aliases in place (`dced86ba6`,
    `9e1ba1dae`, docs `8f542f797`)
  - [x] 2c — Node protocol landed with the walk switch as first adopter
    (`328df6341`, `aafd6fe97`, `f038faf09`, docs `767e1293e`)
    *(full racecheck skipped at user request)*
  - [x] 2d — Aliases removed (`a30776ce9`, `d981cc9c0`, `f70e8a7d9`,
    `f6107872e`, docs `7dfcd7b80`) *(racecheck skipped at user request)*
- [ ] **Step 3 — `functions`** ([prompt](ENGINE-SPLIT-functions.md); needs
  2b, and 2c for F2)
  - [x] F1 — Generics removed (`69aeb6167`, `5ec01573f`, docs `937962c4f`)
  - [ ] F2 — Switches converted to the node protocol *(broad rewrite; needs
    2c)*
    - [x] retag (`1e6bd8aec`)
    - [x] remap (`c703fd8d9`)
    - [x] volatility (`5d7935b7d`)
    - [x] deparse (`42ec6b7e5`, then the docs commit) *(racecheck skipped
      at user request)*
    - [x] node tree (`68cbeefed`) *(racecheck skipped at user request)*
    - [x] generated admission (`1fa051e96`)
    - [x] conditional plan (`4c54eaf54`)
    - [x] join legality (`144458647`; protocol membership `c6de24e8f`, docs
      `ec2fba8bd`)
    - [x] stored scalar SQL (`2e7673441`, `cca30a78c`, docs `b0d07f5e5`)
  - [x] F3 — `Binder`, `Definition`/`Lookup` and engine dispatch; pilot
    family: UUID extraction (`60dc1cedd`, `cd4a88387`, `5d59e5737`, docs
    `9bca9b2b6`)
  - [x] F4/F5 Tier A — port in place, then move
    - [x] math (`3078b889f`, `d5dc34c59`, `0f3c7e5a2`, docs `96e11f662`)
      *(racecheck skipped at user request)*
    - [x] string (`69133565c`, `7c28e5c79`, `76d0201d9`, `db9694a8f`, then
      the docs commit) *(racecheck skipped at user request)*
    - [x] LIKE/SIMILAR/regex (`ba236a2b8`, `1807e7ee9`, `34f7300d1`, then the
      docs commit; the LIKE operator binder stays in the engine until F2)
      *(racecheck skipped at user request)*
    - [x] datetime (`5c5928497`, `5a293c0b3`, `4acb5e42b`, then the docs
      commit; pg_sleep is Tier B) *(racecheck skipped at user request)*
    - [x] JSON/JSONB/SQL-JSON (function calls: `059a86924`, `94b71bb40`,
      `a6c399c2b`, `b1099500b`, then the docs commit; operators, SQL/JSON
      constructors and `IS JSON`: `13aba123e` diagnostics seam, `d7103073a`/
      `06d03de3c` operators, `d4b19bb66`/`b3e056f62` SQL/JSON, then the docs
      commit) *(racecheck skipped at user request)*
    - [x] arrays (with ranges: `a46ee7c5c`, `48d987da7`, `62f610cb1`,
      `050805a1e`, `a5be023f9`, then the docs commit)
      *(racecheck skipped at user request)*
    - [x] ranges (`lower`/`upper` stay with string)
    - [x] UUID (with format and uuid-ossp: `e29efd342`, `6bcd3fb43`,
      `0a04835ae`, `36411c4fd`, `0a80f30a0`, then the docs commit)
      *(racecheck skipped at user request)*
    - [x] format
    - [x] binary (`6e15eb725`, `8e4f3177b`, `0d43b5592`, then the docs
      commit) *(racecheck skipped at user request)*
    - [x] numeric formatting (`b6dc6c486`, then the docs commit)
    - [x] pgcrypto (with fuzzystrmatch and unaccent: `a4e02ba94`,
      `c037c89d9`, `56271aa34`, docs `f6e3d98c5`) *(racecheck skipped at
      user request)*
    - [x] fuzzystrmatch
    - [x] unaccent
    - [x] intarray (nothing to port)
    - [x] intagg (with random, clocks and sleep: `50ab66515`, `a59940644`,
      `208390a37`, docs `11cad8edf`) *(racecheck skipped at user request)*
    - [x] uuid-ossp
  - [x] F4/F5 Tier B
    - [x] random
    - [x] current time and clocks (FILTER fix `920fafff4`)
    - [x] sleep
    - [x] settings reads (`4014a7726`, `e030789b6`, then the docs commit)
      *(racecheck skipped at user request)*
  - [x] F4/F5 aggregates and windows (`f4ee7801c`, `d9d8e6482`, `8066e02e9`,
    `ff28e5996`, `a18766bcc`, `c4a15e187`, then the docs commit)
    *(racecheck skipped at user request)*
  - [ ] Tier C refined *(after steps 4, 5 and 7)*
- [x] **Step 4 — `lock`** ([prompt](ENGINE-SPLIT-lock.md); needs 1)
  - [x] 4a — Cut 11, `EndReason`, mutex encapsulation, lock-order audit
    (`78265dd49`, `d4a96abb9`, `e9c2356cd`, docs `ad4693205`)
  - [x] 4b — `lock` created and declarations moved (`6fe4ccdc6`,
    `ff15343fb`, `f2887fb33`, `8192746a5`, `dd7b1b796`, then the docs commit)
    *(full racecheck skipped at user request; targeted RACE=1 runs passed)*
  - [x] 4c — Aliases removed (`78796c5df`, `27ae525e7`, `0462d4b61`, then
    the docs commit) *(racecheck skipped at user request; targeted RACE=1
    runs passed)*
- [x] **Step 5 — `catalog`** ([prompt](ENGINE-SPLIT-catalog.md); needs 1,
  2b)
  - [x] 5a — `catalogStore` state ownership (`41d5fbda9`, `9a0e50246`,
    then the docs commit) *(full racecheck skipped at user request; targeted
    RACE=1 runs passed)*
  - [x] 5b — Typed read API, map reads migrated, writes confined, cut 1
    (`17a54431b`, `ddd9eb246`, `0175300e5`, `5aeaedcdc`, then the docs
    commit) *(full racecheck skipped at user request; targeted RACE=1 runs
    passed)*
  - [x] 5c — View, rule, policy, extension-provider and expression payload
    seams (`3f4eb275c`, `0559a8d7b`, `1e2576d18`, `b4d7e495b`, then the docs
    commit) *(racecheck skipped at user request)*
  - [x] 5d — Descriptor names decided, fields exported (`a7c3461ce`,
    `b69a715b2`, `31ca756a9`, `4a6055b85`, then the docs commit)
    *(racecheck skipped at user request)*
  - [x] 5e — `catalog` created, types moved, `Candidate` split from
    `CatalogStage` (`eb68d288b`, `fa79f1917`, `0f3bad42b`, `eb612a052`,
    `3b5d2b883`, `943e7a42d`, then the docs commit) *(racecheck skipped at
    user request; targeted RACE=1 runs passed)*
  - [x] 5f — Aliases removed (`2772419e7`, `c4b8068fc`, `0b9103f6d`,
    `69fb93a49`, `353528e2d`, `78ec4daeb`, then the docs commit)
    *(racecheck skipped at user request)*
  - [x] 5g — `catalog.Relation` regrouped into value sub-records
    (`43c6b86ec`, `47a4495b2`, `089bf7856`, `3ef61771b`, `90f6a9b52`, then
    the docs commit) *(racecheck skipped at user request; targeted RACE=1
    runs passed)*
- [x] **Step 6 — `storage`** ([prompt](ENGINE-SPLIT-storage.md); needs 5e)
  - [x] 6a — `storageStore` state ownership (`79c8a6f3a`, `d6bbc8a3e`,
    then the docs commit) *(racecheck skipped at user request; targeted
    RACE=1 runs passed)*
  - [x] 6b — `storage` created and types moved (`c001ffa28`, `af2501215`,
    then the docs commit) *(racecheck skipped at user request; targeted
    RACE=1 runs passed)*
  - [x] 6c — Aliases removed (`518fd7141`, `4c9a2a229`, `33c1b6f43`,
    `a57f5648e`, then the docs commit) *(racecheck skipped at user
    request)*
- [ ] **Step 7 — `visibility`** ([prompt](ENGINE-SPLIT-visibility.md); needs
  4 and 6 complete)
  - [x] 7a — `visibilityRegistry` state ownership (`f556f670c`,
    `e2745162f`, `89aed2c58`, then the docs commit) *(racecheck skipped at
    user request; targeted RACE=1 runs passed)*
  - [x] 7b — `visibility` created and types moved (`41a569c1c`,
    `e896f75d7`, `334ddf32c`, `eb53b840a`, then the docs commit)
    *(racecheck skipped at user request; targeted RACE=1 runs passed;
    REFERENCE=1 MERGE RR `/postgres` drift also fails on `master`)*
  - [ ] 7c — Aliases removed *(broad rewrite)*
- [x] **Authorization and `access`**
  ([prompt](ENGINE-SPLIT-authorization.md); Part A needs —, Part B needs 5e)
  - [x] Part A
    - [x] A1 — Pure helpers moved, README rewritten (`e932fc381`,
      `96ee8d96b`, docs `31aa39dfb`)
  - [x] Part B
    - [x] B0 — `access` refined and approved (docs only, then the docs
      commit)
    - [x] B1 — `roleStore` role state ownership (`e4174a713`, `e91c6d175`,
      then the docs commit) *(racecheck skipped at user request; targeted
      RACE=1 runs passed)*
    - [x] B2 — `accessView` and `accessEnvironment`; checks ported in place
      (`8e41e7091`, `a0fa8edec`, `7d5e526b8`, `809a3ed62`, then the docs
      commit) *(racecheck skipped at user request; targeted RACE=1 runs
      passed)*
    - [x] B3 — `access` created and code moved (`8e529feda`, `0d549283e`,
      `9cfb1306b`, `a168628d4`, then the docs commit) *(racecheck skipped
      at user request; targeted RACE=1 runs passed)*
    - [x] B4 — Aliases removed (`68be6bf5a`, `4130fa669`, then the docs
      commit) *(racecheck skipped at user request)*
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
