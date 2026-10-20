# Engine split — status

Progress tracker for the `internal/engine` split. The plan is
[ENGINE-SPLIT.md](ENGINE-SPLIT.md). Each step has its own prompt file, and the
Status section of that file is the detailed record: commits, aliases,
corrections and notes. This page shows what is done, what comes next, and
the order of all open work (the "Linear plan" below).

Last verified against `master` at `cb31ead98`. The later-wave plans
(steps 8–17) were measured at `75fe0d710`.

## Next ready sub-steps

Every item below has all its prerequisites ticked. Numbers refer to the
"Linear plan" section. Items marked **broad** must not run at the same time
as another broad item (§8).

- **Step 12 `analyze`** — 12e, the alias removal (item 41) **broad**. 12a–12d
  are done (12d.6 moved the binder).
- **`access` B5** — the binding walkers (item 42).
- **Step 13 `session`** — 13c, the refine (item 43, docs only).

Steps 1, 2 and 4–11 and `access` are done. Step 13's in-place untangling (13a,
13b) is done; 13c (item 43) is ready. Step 16
Part A (16a, 16b) is done; 16c (item 50) waits for 13d (item 44). Step 17 Part
A (17a, 17b) is done; 17c (item 52) waits for 13d (item 44).

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
    - [x] grouping identity (`063fe2d1e`)
    - [x] grouping rewrite and aggregate occurrence (`622b3d3ba`,
      substitution `b2675f4c2`)
    - [x] preflight (`a187f7f5d`, substitution `b2675f4c2`)
    - [x] index key display (`39c887998`)
    - [x] closed DEFAULT checks (`632814b91`)
    - [x] `Arguments()` removed (`25fcad768`, then the docs commit)
      *(racecheck skipped at user request)*
    - [x] view literal capture *(broad; explicit written-operands protocol;
      racecheck/finalcheck waived at user request)*
    - [x] function sources, with `seriesExpression` and `unnestExpression`
      *(broad; quickcheck and public smoke; remaining checks waived at user request)*
    - [x] `SQLJSON` rendering *(protocol cutover; exact public byte smoke,
      selected dual-mode and quickcheck passed; remaining checks, including
      full racecheck/finalcheck, waived by user)*
    - [x] result nullability hook (`query_bind.go`, `returning.go`; explicit
      SELECT/RETURNING protocol; racecheck/finalcheck waived at user request)
    - [x] conditional closure hook (closed-DEFAULT check; selected proof and
      quickcheck passed; racecheck/finalcheck waived by user;
      [adoption/evidence note](ENGINE-SPLIT-functions.md#conditional-closed-default-adoption))
  - [x] F3 — `Binder`, `Definition`/`Lookup` and engine dispatch; pilot
    family: UUID extraction (`60dc1cedd`, `cd4a88387`, `5d59e5737`, docs
    `9bca9b2b6`)
  - [x] F3 aliases removed (engine provider type/23 constants and deferred math
    builder/Hint aliases; latest-base semantic and public smoke proof passed;
    preceding-base build, selected native/real, quickcheck and full race proof
    passed; complete-candidate gates refresh after rebase; finalcheck waived)
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
  - [x] Tier C refined *(after steps 4, 5 and 7)*
    - [x] ACL SQL function nodes (`b3ce43139`, `574de18f1`, `436322070`,
      `75d906ea8`, then the docs commit) *(racecheck skipped at user
      request)*
    - [x] catalog inquiry, with session identity (ten-node protocol/runtime cutover;
      selected dual-mode, quickcheck, bounded-CPU racecheck and public replay smoke)
    - [x] transaction identity and commit timestamps *(this commit; selected dual-mode, quickcheck, racecheck and driver smoke passed)*
    - [x] advisory locks, with blocking inquiry *(extraction complete in candidate;
      quickcheck-only waiver; synchronized proof, review and integration pending)*
    - [x] sequence calls *(Group A Item 10; selected dual-mode, quickcheck and
      byte-identical public probe passed; racecheck/finalcheck skipped at user request)*
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
  - [x] 5h — One revision-frontier helper; private `Candidate` methods
    (the commit that records this status) *(quickcheck passed; further
    racecheck and finalcheck skipped at user request)*
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
  - [ ] 6d — Optional: store map writes confined to methods
- [x] **Step 7 — `visibility`** ([prompt](ENGINE-SPLIT-visibility.md); needs
  4 and 6 complete)
  - [x] 7a — `visibilityRegistry` state ownership (`f556f670c`,
    `e2745162f`, `89aed2c58`, then the docs commit) *(racecheck skipped at
    user request; targeted RACE=1 runs passed)*
  - [x] 7b — `visibility` created and types moved (`41a569c1c`,
    `e896f75d7`, `334ddf32c`, `eb53b840a`, then the docs commit)
    *(racecheck skipped at user request; targeted RACE=1 runs passed;
    REFERENCE=1 MERGE RR `/postgres` drift also fails on `master`)*
  - [x] 7c — Aliases removed (`81766feb0`, then the docs commit)
    *(racecheck skipped at user request)*
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
    - [x] Names cleanup — diagnostic name helpers moved to `catalog`
      (`9a73852a6`, `416945ecb`, then the docs commit) *(racecheck skipped
      at user request)*
    - [ ] B5 — Binding walkers moved next to `binding` (needs 12d)
- [x] **Step 8 — `settings`** ([prompt](ENGINE-SPLIT-settings.md); needs —)
  - [x] 8a — Registry in place (`3c1e1115a`)
  - [x] 8b — `settings` created and definitions moved (`8057bb8d0`,
    `dbf23631c`, `cc31fb6f6`, then the docs commit) *(racecheck skipped at
    user request)*
  - [x] 8c — Aliases removed (`4a5e2dd43`, `ac1cfe10c`, then the docs
    commit) *(racecheck skipped at user request)*
- [x] **Step 9 — `pgcatalog`** ([prompt](ENGINE-SPLIT-pgcatalog.md); needs
  5, B4; 9b also 8b)
  - [x] 9a — One producer contract and one registry, in place
  - [x] 9b — `pgcatalog` created and producers moved (`1642db564`,
    `195e9a584`, `a4a5c66af`, `18da282df`, then the docs commit)
    *(racecheck skipped at user request)*
  - [x] 9c — Aliases removed (`62a29d11d`, `7ea3c95e9`, then the docs
    commit) *(racecheck skipped at user request)*
- [x] **Step 10 — `plan`** ([prompt](ENGINE-SPLIT-plan.md); needs 2, 3 F1, 5)
  - [x] 10a — `EvalContext` split in place *(broad)* (the commit that
    records this status) *(racecheck/finalcheck skipped at user request)*
  - [x] 10b — Opaque CTE mutation and node runtime, in place
    (`61c56b76b`, `55a5497bd`, `d046dec09`, `854c270a9`, `65b24c846`, docs:
    the commit that records this status) *(racecheck/finalcheck skipped at
    user request)*
  - [x] 10c — `plan` created, IR and node types moved *(broad)*
    (`6189f6483`, `00e890338`, `a6188a084`, then the docs commit) *(racecheck/finalcheck skipped at user
    request)*
  - [x] 10d — Aliases removed *(broad)* (`fb6c19185`, `367313c0d`,
    `3860f511e`, `9ba165e93`, `3f66996a4`, `50e07094a`, then the docs commit)
    *(racecheck/finalcheck skipped at user request)*
- [x] **Step 11 — `exec`** ([prompt](ENGINE-SPLIT-exec.md); needs 10c, 13a)
  - [x] 11a — Refined (docs only; host list, move set, §7 decisions 8 and
    11; the commit that records this status)
  - [x] 11b — Host seam in place (`220b72d76`, `f93b0f837`, `b82df3645`,
    `f205fff26`, `53c9ff63e`, `6071a1cc4`, `0a3e5e602`, `ccb197d87`,
    `e2d780fc6`, then the docs commit) *(racecheck/finalcheck skipped at user
    request)*
  - [x] 11c — `exec` created and executor moved *(broad)* (`45af3aed4` …
    `2e68cbd93`, then the docs commit) *(racecheck/finalcheck skipped at
    user request)*
  - [x] 11d — Aliases removed *(broad)* (`4d4e0942e`, `4acc2dac9`,
    `17fc012f5`, `7d6ffd65a`, then the docs commit) *(racecheck/finalcheck
    skipped at user request)*
- [ ] **Step 12 — `analyze`** ([prompt](ENGINE-SPLIT-analyze.md); 12a–12b
  need —; 12c needs 10c; 12d needs 11c)
  - [x] 12a — `binding` split per statement kind *(broad)* (`50c0912f5`)
  - [x] 12b — `expressionBinder` fields grouped *(broad)* (`b1dc47647`,
    `7b8cd2e23`, `449bd93c3`, `807bccc88`, `fdd30aa67`, then the docs commit)
    *(racecheck/finalcheck skipped at user request)*
  - [x] 12c — Refined; host seam in place (refine `4c5fdc26c`; `fcd02ccd9`,
    `befafa13a`, `30378dd42`, `9aed67fdb`, `bc537ad6e`, then the docs commit)
    *(racecheck/finalcheck skipped at user request)*
  - [x] 12d — `analyze` created and binding moved (several sessions)
    *(broad)*
    - [x] 12d refine (docs only; `b8850fca9`): the binder moves last
    - [x] 12d.1 — Binding types moved *(broad)* (`5173b29cd`, `70d43f3e9`,
      `f630be9b9`, `5ed818cdf`, then the docs commit)
      *(racecheck/finalcheck skipped at user request)*
    - [x] 12d.2 — Binder seam cuts in place *(broad)* (`e0d90806f`,
      `3ed685006`, `f718185df`, `48ea9b211`, `9115279bd`, `b7e9c8bd7`, then
      the docs commit) *(racecheck/finalcheck skipped at user request)*
    - [x] 12d.3 — Expression core prepared in place (`429bf3989`,
      `a750bbf16`, `4f491dace`, `ff9c86427`, `2ba59b6ba`, then the docs
      commit) *(racecheck/finalcheck skipped at user request)*
    - [x] 12d.3b — Unlisted expression files prepared in place (`8186d9690`,
      `f18840612`, `e1f80935c`, then the docs commit)
      *(racecheck/finalcheck skipped at user request)*
    - [x] 12d.4 — Query, FROM and CTE binding prepared in place
      (`d350f8a03`, `c6475646c`, `20e14024e`, `ee4b1ac07`, `36bdb885a`,
      `792c18427`, `86957752b`, then the docs commit)
      *(racecheck/finalcheck skipped at user request)*
    - [x] 12d.5 — Aggregates, windows and DML targets prepared in place
      (`949747b68`, `804df42f5`, `1a329c118`, `a73ebbc83`, `363efc88e`,
      then the docs commit) *(racecheck/finalcheck skipped at user request)*
    - [x] 12d.6 — Binder moved *(broad)* (`947dec977`, `5f9ca6d92`,
      `babc61e76`, `3fa00a84e`, then the docs commit)
      *(racecheck/finalcheck skipped at user request)*
  - [ ] 12e — Aliases removed *(broad)*
- [ ] **Step 13 — `session`** ([prompt](ENGINE-SPLIT-session.md); 13a–13b
  need —; 13c needs 11c, 12d, 8b)
  - [x] 13a — Statement scope slots *(broad)* (`21d5f3a49`, `a4219195b`,
    `dd7faf016`, `dd018885f`, `df6045183`, `a067c88db`, `40dc0f177`,
    `3271f3ea1`, `dd2394458`, `ef136c2c5`, `c23fe3429`, `a08db5ff1`, then the
    docs commit) *(racecheck/finalcheck skipped at user request)*
  - [x] 13b — Operation, journal, stage and owner slots *(broad)*
    (`3ce970c39`, `43237d634`, `45992db99`, `f37f352f3`, `8915f5271`,
    `92ae3b8f3`, then the docs commit) *(racecheck/finalcheck skipped at user
    request)*
  - [ ] 13c — Refine (docs only)
  - [ ] 13d — `session` created and moved (several sessions) *(broad)*
  - [ ] 13e — Aliases removed *(broad)*
- [ ] **Step 14 — `routine`** ([prompt](ENGINE-SPLIT-routine.md); Part A
  needs `PG18-ROUTINE-CORE` merged, 14b also the F3 aliases item; Part B
  needs 13d)
  - [x] 14a — In place (`f4645bc19`, `87bf1455e`, `f33847363`, then the docs
    commit) *(racecheck skipped at user request)*
  - [x] 14b — `routine` created and clean set moved (`aaf6759a4`,
    `d6becc9d6`, then the docs commit) *(racecheck skipped at user request)*
  - [ ] 14c — Host refined (docs only)
  - [ ] 14d — DDL handlers moved
- [ ] **Step 15 — `typedef`** ([prompt](ENGINE-SPLIT-typedef.md); Part A
  needs 14b; Part B needs 13d)
  - [x] 15a — In place (`1045960f6`, `9e4924bbd`, `0add6a48b`, `92fd3299a`,
    `ed21b3bf3`, then the docs commit) *(racecheck/finalcheck skipped at user
    request)*
  - [x] 15b — `typedef` created and clean set moved (`d4d99a162`,
    `5d3f4af95`, `b67b62d22`, then the docs commit) *(racecheck/finalcheck
    skipped at user request)*
  - [ ] 15c — Host refined (docs only)
  - [ ] 15d — DDL handlers moved
- [ ] **Step 16 — `extension`** ([prompt](ENGINE-SPLIT-extension.md); Part A
  needs 15b; Part B needs 13d)
  - [x] 16a — In place (`d0a013406`, `bc0e3d81b`, `54d886952`, `46da1763e`, then the docs commit)
  - [x] 16b — `extension` created and clean set moved (`e6ff96286`,
    `a8f3f5a8e`, `d55a47824`, then the docs commit) *(racecheck/finalcheck
    skipped at user request)*
  - [ ] 16c — Host refined (docs only)
  - [ ] 16d — Installation handlers moved
- [ ] **Step 17 — `sequence`** ([prompt](ENGINE-SPLIT-sequence.md); Part A
  needs Tier C sequence calls; Part B needs 13d)
  - [x] 17a — In place (`6ab03f25b`, `68d49b5a8`, `88c3aac0f`, `d43c3b1fb`,
    `48fe404db`, then the docs commit) *(racecheck skipped at user request)*
  - [x] 17b — `sequence` created and clean set moved (`b9aacc67c`,
    `6a7aaae6a`, then the docs commit) *(racecheck/finalcheck skipped at user
    request)*
  - [ ] 17c — Host refined (docs only)
  - [ ] 17d — Handlers moved
- [ ] **Postponed packages** ([ENGINE-SPLIT.md §6](ENGINE-SPLIT.md#postponed-packages);
  no prompt files yet): `index`, `partition`, `inheritance`, `table`,
  `view`, `fdw`, `dml`, `control`

`access` is tracked under Part B above, not under the later waves.

## Linear plan

All open sub-steps in execution order. Give an agent one item number; it
implements that item from the named prompt file and ticks the box here and
in the prompt file's Status section.

- **needs** lists the item numbers that must be ticked first. Items with no
  unticked prerequisites may run in parallel, on separate branches.
- **broad** items rewrite hundreds of files. Never run two broad items at the
  same time; rebase other open branches right after a broad item lands.
- Groups are only for reading; the "needs" lists decide the order.

### Group A — finish the existing packages

1. [x] Functions F2: view literal capture —
   [functions](ENGINE-SPLIT-functions.md) F2 · needs — · **broad**
2. [x] Functions F2: function sources (with `seriesExpression`,
   `unnestExpression`) — [functions](ENGINE-SPLIT-functions.md) F2 · needs
   — · **broad**
   Typed source protocol, provider-local cursors and immutable marker cutover.
   Earlier quickcheck, selected dual-mode/race proof and replay smoke passed.
   Latest quickcheck LIKE-observer failure and further tests, including
   racecheck/finalcheck, explicitly waived at user request.
3. [x] Functions F2: `SQLJSON` rendering —
   [functions](ENGINE-SPLIT-functions.md) F2 · needs — · protocol cutover;
   selected dual-mode/quickcheck passed; remaining checks, including full
   racecheck/finalcheck, waived by user
4. [x] Functions F2: result nullability hook — explicit SELECT/RETURNING protocol;
   racecheck/finalcheck waived · [functions](ENGINE-SPLIT-functions.md) F2 · needs —
5. [x] Functions F2: conditional closure hook —
   [functions](ENGINE-SPLIT-functions.md#conditional-closed-default-adoption)
   F2 · needs — · selected proof/quickcheck passed; racecheck/finalcheck
   explicitly waived by user
6. [x] Functions F3 aliases removed —
   [functions](ENGINE-SPLIT-functions.md) Status, "Later-wave refresh" ·
   needs — · **broad** · tests-12 latest-base semantic/public smoke passed.
   Tests-10/tests-11 build, selected native/real, quickcheck, full racecheck and
   dependency/import proof passed on the preceding base; latest complete-candidate
   gates must refresh before review.
   Three of five startup samples exceeded1ms; comparison is qualified by
   the advanced integration base and shared-host contention, not a speedup
   claim. Post-integration finalcheck explicitly waived, not run or passed.
7. [x] Tier C: catalog inquiry, with session identity —
   [functions](ENGINE-SPLIT-functions.md) Tier C · needs — (easier after 4)
   Ten-node leaf/runtime and complete visitor cutover. All35 selected batches
   passed quick and real18.6 modes; full quickcheck, focused races, full
   GOMAXPROCS=8 racecheck, dependency/import checks and public replay smoke passed.
   Default-CPU racecheck hit two fixture deadlines under contention; preserved
   evidence, unchanged suite passed with bounded CPU/package concurrency.
   Startup5152 steady allocations; three of five after samples exceeded1ms.
8. [x] Tier C: transaction identity and commit timestamps —
   [functions](ENGINE-SPLIT-functions.md) Tier C · needs — · this commit; verified
9. [x] Tier C: advisory locks, with blocking inquiry — extraction complete in candidate;
   [functions](ENGINE-SPLIT-functions.md) Tier C · needs — · historical selected
   native/real/race, smoke and quickcheck passed; full racecheck attempts failed
   or expired, not passed. User waived racecheck/race-based finalcheck;
   synchronized quickcheck/public smoke, independent review and integration
   remain root-owned gates, not completion claims.
10. [x] Tier C: sequence calls — [functions](ENGINE-SPLIT-functions.md)
    Tier C · needs 7, 8, 9 · selected dual-mode, quickcheck and byte-identical
    public probe passed; racecheck/finalcheck skipped at user request
11. [x] Catalog 5h — [catalog](ENGINE-SPLIT-catalog.md) 5h · needs —
    (the commit that records this status) *(quickcheck passed; further
    racecheck and finalcheck skipped at user request)*
12. [x] Storage 6d — [storage](ENGINE-SPLIT-storage.md) 6d · needs — ·
    selected dual-mode/race proof and quickcheck passed; full racecheck/finalcheck
    explicitly waived by user

### Group B — independent leaves

13. [x] Settings 8a — [settings](ENGINE-SPLIT-settings.md) 8a · needs — (`3c1e1115a`)
14. [x] Settings 8b — [settings](ENGINE-SPLIT-settings.md) 8b · needs 13 ·
    racecheck skipped at user request
15. [x] Settings 8c — [settings](ENGINE-SPLIT-settings.md) 8c · needs 14 ·
    **broad** (`4a5e2dd43`, `ac1cfe10c`, then the docs commit) · racecheck
    skipped at user request
16. [x] pgcatalog 9a — [pgcatalog](ENGINE-SPLIT-pgcatalog.md) 9a · needs —
17. [x] pgcatalog 9b — [pgcatalog](ENGINE-SPLIT-pgcatalog.md) 9b · needs
    16, 14 · (`1642db564`, `195e9a584`, `a4a5c66af`, `18da282df`, then the
    docs commit) · racecheck skipped at user request
18. [x] pgcatalog 9c — [pgcatalog](ENGINE-SPLIT-pgcatalog.md) 9c · needs 17 ·
    (`62a29d11d`, `7ea3c95e9`, then the docs commit) · racecheck skipped at
    user request

### Group C — untangle the shared structs in place

19. [x] Plan 10a: `EvalContext` split — [plan](ENGINE-SPLIT-plan.md) 10a ·
    needs — · **broad** · selected native proof, bench and quickcheck passed;
    real 18.6 mode passed except oracle-side `/postgres` drift (DELETE
    RETURNING boundaries, MERGE repeatable read); racecheck/finalcheck
    skipped at user request
20. [x] Analyze 12a: `binding` split — [analyze](ENGINE-SPLIT-analyze.md)
    12a · needs — · **broad** (`50c0912f5`) · selected dual-mode proof and
    quickcheck passed; racecheck/finalcheck skipped at user request
21. [x] Analyze 12b: binder field groups —
    [analyze](ENGINE-SPLIT-analyze.md) 12b · needs 20 · **broad** (`b1dc47647`,
    `7b8cd2e23`, `449bd93c3`, `807bccc88`, `fdd30aa67`, then the docs commit)
    · selected dual-mode proof and quickcheck passed; racecheck/finalcheck
    skipped at user request
22. [x] Session 13a: scope slots — [session](ENGINE-SPLIT-session.md) 13a ·
    needs — · **broad** · selected unit/`RACE=1` and dual-mode proof and
    quickcheck passed; racecheck/finalcheck skipped at user request
23. [x] Session 13b: operation, journal, stage and owner slots —
    [session](ENGINE-SPLIT-session.md) 13b · needs 22 · **broad**
    (`3ce970c39`, `43237d634`, `45992db99`, `f37f352f3`, `8915f5271`,
    `92ae3b8f3`, then the docs commit) · selected unit/`RACE=1` and dual-mode
    proof and quickcheck passed; racecheck/finalcheck skipped at user request

### Group D — feature packages, Part A

24. [x] Routine 14a — [routine](ENGINE-SPLIT-routine.md) 14a · needs
    `PG18-ROUTINE-CORE` merged · racecheck skipped at user request
25. [x] Routine 14b — [routine](ENGINE-SPLIT-routine.md) 14b · needs 24, 6 ·
    racecheck skipped at user request
26. [x] Typedef 15a — [typedef](ENGINE-SPLIT-typedef.md) 15a · needs 25 ·
    racecheck/finalcheck skipped at user request
27. [x] Typedef 15b — [typedef](ENGINE-SPLIT-typedef.md) 15b · needs 26 ·
    selected dual-mode proof and quickcheck passed; racecheck/finalcheck
    skipped at user request
28. [x] Extension 16a — [extension](ENGINE-SPLIT-extension.md) 16a · needs
    27 · (`d0a013406`, `bc0e3d81b`, `54d886952`, `46da1763e`, then the docs commit) · selected dual-mode proof and
    quickcheck passed; racecheck/finalcheck skipped at user request
29. [x] Extension 16b — [extension](ENGINE-SPLIT-extension.md) 16b · needs
    28 · (`e6ff96286`, `a8f3f5a8e`, `d55a47824`, then the docs commit) ·
    selected dual-mode proof and quickcheck passed; racecheck/finalcheck
    skipped at user request
30. [x] Sequence 17a — [sequence](ENGINE-SPLIT-sequence.md) 17a · needs 10 ·
    selected dual-mode proof and quickcheck passed; racecheck/finalcheck
    skipped at user request
31. [x] Sequence 17b — [sequence](ENGINE-SPLIT-sequence.md) 17b · needs 30 ·
    selected dual-mode proof and quickcheck passed; racecheck/finalcheck
    skipped at user request

### Group E — core moves

32. [x] Plan 10b — [plan](ENGINE-SPLIT-plan.md) 10b · needs 19 · selected
    dual-mode proof and quickcheck passed; racecheck/finalcheck skipped at
    user request; decision 9 settled with a measured evaluation cost
33. [x] Plan 10c — [plan](ENGINE-SPLIT-plan.md) 10c · needs 32 · **broad**
    (`6189f6483`, `00e890338`, `a6188a084`, then the docs commit) · selected dual-mode proof and
    quickcheck passed; racecheck/finalcheck skipped at user request
34. [x] Plan 10d — [plan](ENGINE-SPLIT-plan.md) 10d · needs 33 · **broad**
    (`fb6c19185`, `367313c0d`, `3860f511e`, `9ba165e93`, `3f66996a4`,
    `50e07094a`, then the docs commit) · with the three 10c follow-ups ·
    selected dual-mode proof and quickcheck passed; racecheck/finalcheck
    skipped at user request
35. [x] Exec 11a (refine, docs only) — [exec](ENGINE-SPLIT-exec.md) 11a ·
    needs 33, 22 · (docs: the commit that records this status) · docs-only;
    quickcheck passed; racecheck/finalcheck skipped at user request
36. [x] Exec 11b — [exec](ENGINE-SPLIT-exec.md) 11b · needs 35 ·
    (`220b72d76` … `e2d780fc6`, then the docs commit) · selected dual-mode
    proof and quickcheck passed; racecheck/finalcheck skipped at user
    request; preparation callbacks use one stateless value, not the host
37. [x] Exec 11c — [exec](ENGINE-SPLIT-exec.md) 11c · needs 36 · **broad**
    (`45af3aed4` … `2e68cbd93`, then the docs commit) · selected dual-mode
    proof and quickcheck passed; racecheck/finalcheck skipped at user
    request; exec stays below the feature packages
38. [x] Exec 11d — [exec](ENGINE-SPLIT-exec.md) 11d · needs 37 · **broad**
    (`4d4e0942e` … `7d6ffd65a`, then the docs commit) · selected dual-mode
    proof and quickcheck passed; racecheck/finalcheck skipped at user
    request; the driver and `cmd/stuntgres` import `exec` directly
39. [x] Analyze 12c (refine, then host seam) —
    [analyze](ENGINE-SPLIT-analyze.md) 12c · needs 21, 33 · (refine
    `4c5fdc26c`; `fcd02ccd9` … `bc537ad6e`, then the docs commit) · selected
    dual-mode proof and quickcheck passed; racecheck/finalcheck skipped at
    user request
40. [x] Analyze 12d (several sessions) — [analyze](ENGINE-SPLIT-analyze.md)
    12d · needs 39, 37 · **broad** · done when 40a–40h are ticked
    - 40a. [x] 12d refine (docs only; `b8850fca9`) · the binder moves in one
      final session; the areas are prepared in place
    - 40b. [x] 12d.1 binding types · needs 40a · **broad** (`5173b29cd`,
      `70d43f3e9`, `f630be9b9`, `5ed818cdf`, then the docs commit) · selected
      dual-mode proof and quickcheck passed; racecheck/finalcheck skipped at
      user request
    - 40c. [x] 12d.2 binder seam cuts · needs 40b · **broad** (`e0d90806f`,
      `3ed685006`, `f718185df`, `48ea9b211`, `9115279bd`, `b7e9c8bd7`, then
      the docs commit) · selected dual-mode proof and quickcheck passed;
      racecheck/finalcheck skipped at user request
    - 40d. [x] 12d.3 expression core preparation · needs 40c (`429bf3989`,
      `a750bbf16`, `4f491dace`, `ff9c86427`, `2ba59b6ba`, then the docs
      commit) · selected dual-mode proof and quickcheck passed;
      racecheck/finalcheck skipped at user request
    - 40e. [x] 12d.4 query, FROM and CTE preparation · needs 40c
      (`d350f8a03`, `c6475646c`, `20e14024e`, `ee4b1ac07`, `36bdb885a`,
      `792c18427`, `86957752b`, then the docs commit) · selected dual-mode
      proof and quickcheck passed; racecheck/finalcheck skipped at user
      request
    - 40f. [x] 12d.5 aggregates, windows and DML targets preparation · needs
      40c (`949747b68`, `804df42f5`, `1a329c118`, `a73ebbc83`, `363efc88e`,
      then the docs commit) · selected dual-mode proof and quickcheck passed;
      racecheck/finalcheck skipped at user request
    - 40g. [x] 12d.6 binder moved · needs 40d, 40e, 40f, 40h · **broad**
      (`947dec977`, `5f9ca6d92`, `babc61e76`, `3fa00a84e`, then the docs
      commit) · quickcheck and selected dual-mode proof passed;
      racecheck/finalcheck skipped at user request
    - 40h. [x] 12d.3b unlisted expression files preparation (area 3b in the
      analyze prompt) · needs 40c (`8186d9690`, `f18840612`, `e1f80935c`,
      then the docs commit) · quickcheck passed; broad reference run
      stopped at user request (known drift only); racecheck/finalcheck
      skipped at user request
41. [ ] Analyze 12e — [analyze](ENGINE-SPLIT-analyze.md) 12e · needs 40 ·
    **broad**
42. [ ] Access B5: binding walkers —
    [authorization](ENGINE-SPLIT-authorization.md) B5 · needs 40
43. [ ] Session 13c (refine, docs only) —
    [session](ENGINE-SPLIT-session.md) 13c · needs 23, 37, 40, 14
44. [ ] Session 13d (several sessions) — [session](ENGINE-SPLIT-session.md)
    13d · needs 43 · **broad**
45. [ ] Session 13e — [session](ENGINE-SPLIT-session.md) 13e · needs 44 ·
    **broad**

### Group F — feature packages, Part B

46. [ ] Routine 14c (refine, docs only) — [routine](ENGINE-SPLIT-routine.md)
    14c · needs 44, 25
47. [ ] Routine 14d — [routine](ENGINE-SPLIT-routine.md) 14d · needs 46
48. [ ] Typedef 15c (refine, docs only) — [typedef](ENGINE-SPLIT-typedef.md)
    15c · needs 44, 27
49. [ ] Typedef 15d — [typedef](ENGINE-SPLIT-typedef.md) 15d · needs 48
50. [ ] Extension 16c (refine, docs only) —
    [extension](ENGINE-SPLIT-extension.md) 16c · needs 44, 29
51. [ ] Extension 16d — [extension](ENGINE-SPLIT-extension.md) 16d · needs
    50
52. [ ] Sequence 17c (refine, docs only) —
    [sequence](ENGINE-SPLIT-sequence.md) 17c · needs 44, 31
53. [ ] Sequence 17d — [sequence](ENGINE-SPLIT-sequence.md) 17d · needs 52

### Group G — next planning round

54. [ ] Plan the postponed packages (docs only, like this round): prompt
    files for `index`, `partition`, `inheritance`, `table`, `view`, `fdw`,
    `dml` and `control` — [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §6 "Postponed
    packages" · needs 44

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
   - a refine sub-step (11a, 12c, 13c, 14c–17c) adds or splits the items it
     planned, in the tree and in the linear plan;
   - a later wave that gets a prompt file gets a link and its sub-steps here.
   Do not add items that are not in a prompt file or in ENGINE-SPLIT.md.
5. **Keep the linear plan in step with the tree.** Tick the item when its
   tree entry is ticked. When an item splits (for example 12d's sessions),
   add lettered sub-items (`40a`, `40b`, ...) instead of renumbering, so
   numbers given to other agents stay valid. Add new items at the end of
   their group with the next free number, and give them "needs" lists.
6. **Rewrite "Next ready sub-steps"**: list each unticked item whose
   prerequisites are all ticked. Prerequisites are the linear plan's "needs"
   lists, each prompt file's "Needs" line and the §8 table. Mark broad
   rewrites, which must not run in parallel with each other (§8).
7. **Update the "Last verified" line** with the `master` commit you checked
   against.

Keep entries short: one line per item, plus commit hashes. Corrections,
outstanding aliases, moved tests and notes for later steps go in the prompt
file, not here.
