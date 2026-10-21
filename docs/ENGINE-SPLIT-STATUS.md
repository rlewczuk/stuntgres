# Engine split — status

Progress tracker for the `internal/engine` split. The plan is
[ENGINE-SPLIT.md](ENGINE-SPLIT.md). Each step has its own prompt file, and the
Status section of that file is the detailed record: commits, aliases,
corrections and notes. This page shows what is done, what comes next, and
the order of all open work (the "Linear plan" below).

Last verified against `master` at `bd79e7663` (items 55, 56, 57, 58, 59, 60, 61, 62, 63, 64, 65, 66, 67, 68, 69, 70, 71, 72, 73, 74, 75, 76, 77, 78, 79, 80, 81, 82, 83, 84, 85a, 86, 87, 88, 90, 91, 94, 95, 96, 97, 99, 100, 103, 104, 105, 106, 107 and 111). Steps 8–17 were
planned at `75fe0d710`; steps 18–30 and the second-round follow-ups (items
55–114) at `c154e14fb`.

## Next ready sub-steps

Every item below has all its prerequisites ticked. Numbers refer to the
"Linear plan" section. Items marked **broad** must not run at the same time
as another broad item (§8); none is ready now.

- **Upper features, refined:** 92 (partition 25c, docs only), 101 (view
  27c, docs only).
- **DML and Dump/Restore:** 108 (DML 29b), 112 (control 30b, docs only).
- **Upper features, decisions:** 85b (index 23d.2), 89a (table 24d.1).

Steps 1–17 and `access` (A1–B6) are done, and so is item 54, the second
planning round: steps 18–30 and the follow-ups have prompt files. Items 55
(dump 18a), 58 (copyfmt 19a), 60 (ddl 20a), 64 (regobj 21a) and 67
(stored 22a) are done. Item 71 (functions F6) closed F2 and step 3. Items 74
(catalog 5j) and 65 (regobj 21b) are done, so regobj 21c (item 66) is
ready; stored 22c (item 69) still waits for 22b (item 68). Item 73 (plan 10e) is done;
index 23b (item 83) still waits for 82 and 68. Item 56 (dump
18b) is done, so dump 18c (item 57), routine 14e (item 80) and typedef 15e
(item 81) are ready; stored 22b (item 68) then waited for 65 and 77.
Item 72 (catalog 5i) is done, so table 24a (item 86) is ready.
Item 75 (exec 11e) is done, so exec 11f (item 76) is ready; index 23b
(item 83) still waits for 82 and 68. Item 61 (ddl 20b) is done, so ddl 20c
(item 62) is ready; the Part B refines that build on it (items 84, 96, 101
and 105) still wait for their own earlier items. Item 59 (copyfmt 19b) is
done and closes step 19; DML 29b (item 108) still waits for 107, 83, 87,
91, 95 and 100. Item 57 (dump 18c) is done and closes step 18, so control
30a (item 111) is ready; control 30b (item 112) still waits for 111 and the
Part A moves. Item 76 (exec 11f) is done; only the end-state review
(item 114) needs it, and that still waits for its other items. Item 77
(analyze 12f) is done, and with 65 done stored 22b (item 68) is ready. Item 78
(session 13f) is done; only item 114 needs it. Item 62 (ddl 20c, docs
only) is done, so ddl 20d (item 63) is ready. Item 79
(access B6) is done; table 24b (item 87) still waits for 86 and 83, and
role 28b (item 104) for 103. Items 80 (routine 14e) and 81 (typedef 15e)
are done; control 30b (item 112) and the end-state review (item 114) still
wait for their other items.
Item 86 (table 24a) is done; table 24b (item 87) still waits for 83,
and DML 29a (item 107) for 82. Item 94 (fdw 26a) is done, so fdw 26b
(item 95) is ready; fdw 26c (item 96) then needs 95 and 61.
Item 95 (fdw 26b) is done, so fdw 26c (item 96) is ready; DML 29b
(item 108) still waits for 83, 87, 91, 100 and its other items.
Item 82 (index 23a) is done, so DML 29a (item 107) is ready; index 23b
(item 83) now waits only for 68.
Item 90 (partition 25a) is done; partition 25b (item 91) still waits
for table 24b (item 87).
Item 63 (ddl 20d) is done and closes step 20; only the end-state review
(item 114) needs it. Item 66 (regobj 21c) is done and closes step 21; only
item 114 needs it. Item 99 (view 27a) is done; view 27b (item 100) still
waits for stored 22c (item 69) and table 24b (item 87).
Item 107 (DML 29a) is done; DML 29b (item 108) still waits for 83, 87, 91
and 100.
Item 103 (role 28a) is done, so role 28b (item 104) is ready; role 28c
(item 105) then needs 104 and 61.
Item 111 (control 30a) is done; control 30b (item 112) still waits for
70, 83, 87, 91 and 100.
Item 104 (role 28b) is done, so role 28c (item 105, docs only) is ready;
role 28d (item 106) then needs 105.
Item 96 (fdw 26c, docs only) is done, so fdw 26d (item 97) is ready; fdw
26e (item 98) then needs 97 and table 24d (item 89).
Item 68 (stored 22b) is done, so stored 22c (item 69) and index 23b
(item 83) are ready; stored 22d (item 70) waits for 69.
Item 69 (stored 22c) is done, so stored 22d (item 70, broad) is ready;
view 27b (item 100) still waits for table 24b (item 87).
Item 97 (fdw 26d) is done; fdw 26e (item 98) still waits for table 24d
(item 89), and DML 29c (item 109) for 108, 85b, 89, 93 and 102.
Item 83 (index 23b) is done, so index 23c (item 84, docs only) and table
24b (item 87, broad) are ready; DML 29b (item 108) still waits for 87, 91
and 100, and control 30b (item 112) for 70, 87, 91 and 100.
Item 70 (stored 22d) is done and closes step 22; control 30b (item 112)
still waits for 87, 91 and 100, and table 24b (item 87, broad) no longer
competes with another ready broad item.
Item 105 (role 28c, docs only) is done, so role 28d (item 106) is ready.
Item 84 (index 23c, docs only) is done and split 23d into 23d.1 (item
85a, DDL decisions) and 23d.2 (item 85b, write-time decisions): 85a is
ready, 85b needs 85a's seam commit; table 24c (item 88) still waits for
87, table 24d (item 89) needs 85a and DML 29c (item 109) needs 85b.
Item 106 (role 28d) is done and closes step 28; only the end-state
review (item 114) needs it, and that still waits for 98, 110 and 113.
Item 87 (table 24b) is done, so table 24c (item 88, docs only),
partition 25b (item 91) and view 27b (item 100, broad) are ready; DML
29b (item 108) and control 30b (item 112) still wait for 91 and 100.
Item 85a (index 23d.1) is done, so index 23d.2 (item 85b) is ready; table
24d (item 89) still waits for 24c (item 88).
Item 88 (table 24c, docs only, with a fix of a 24b regression) is done
and split 24d into 24d.1 (item 89a, CREATE), 24d.2 (89b, ALTER and
inheritance) and 24d.3 (89c, Restore and dump cores): 89a is ready
(85a is done), 89b and 89c need 89a; partition 25c (item 92) still waits
for 91, and partition 25d (93), fdw 26e (98) and DML 29c (109) now need
89a and 89b.
Item 91 (partition 25b) is done, so partition 25c (item 92, docs only) is
ready; DML 29b (item 108) and control 30b (item 112) still wait for view
27b (item 100).
Item 100 (view 27b, broad) is done, so view 27c (item 101, docs only),
DML 29b (item 108) and control 30b (item 112) are ready.

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
- [x] **Step 3 — `functions`** ([prompt](ENGINE-SPLIT-functions.md); needs
  2b, and 2c for F2)
  - [x] F1 — Generics removed (`69aeb6167`, `5ec01573f`, docs `937962c4f`)
  - [x] F2 — Switches converted to the node protocol (closed by F6) *(broad rewrite; needs
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
  - [x] F6 — Step 3 closed: aggregate provider remap hook, remaining glue
    recorded; ticks F2 and step 3 (second round, item 71; `c3ba6773d`,
    `95f4feb13`, then the docs commit) *(racecheck/finalcheck skipped at
    user request)*
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
  - [x] 6d — Optional: store map writes confined to methods *(selected
    dual-mode/race proof and quickcheck passed; full racecheck/finalcheck
    waived by user; see the storage prompt Status)*
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
    - [x] B5 — Binding walkers moved into `analyze` (`76039ffea`,
      `4748b28ca`, then the docs commit) *(racecheck/finalcheck skipped at
      user request)*
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
- [x] **Step 12 — `analyze`** ([prompt](ENGINE-SPLIT-analyze.md); 12a–12b
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
  - [x] 12e — Aliases removed *(broad)* (`b899826fd`, `541d4b757`,
    `83c15f51d`, then the docs commit) *(racecheck/finalcheck skipped at user
    request)*
- [x] **Step 13 — `session`** ([prompt](ENGINE-SPLIT-session.md); 13a–13b
  need —; 13c needs 11c, 12d, 8b)
  - [x] 13a — Statement scope slots *(broad)* (`21d5f3a49`, `a4219195b`,
    `dd7faf016`, `dd018885f`, `df6045183`, `a067c88db`, `40dc0f177`,
    `3271f3ea1`, `dd2394458`, `ef136c2c5`, `c23fe3429`, `a08db5ff1`, then the
    docs commit) *(racecheck/finalcheck skipped at user request)*
  - [x] 13b — Operation, journal, stage and owner slots *(broad)*
    (`3ce970c39`, `43237d634`, `45992db99`, `f37f352f3`, `8915f5271`,
    `92ae3b8f3`, then the docs commit) *(racecheck/finalcheck skipped at user
    request)*
  - [x] 13c — Refine (docs only; the commit that records this status)
    *(quickcheck passed; racecheck/finalcheck skipped at user request)*
  - [x] 13d — `session` created and moved (several sessions)
    - [x] 13d.1 — Journal, creates `session` *(broad)*
    - [x] 13d.2 — Statement lease and small cuts (in place)
    - [x] 13d.3 — Engine edges *(broad)*
    - [x] 13d.4 — Root payloads (in place)
    - [x] 13d.5 — File splits and exports *(broad)*
    - [x] 13d.6 — Move *(broad)* (`1770770a8`, `f4c4bd5de`, `71a7ad850`,
      then the docs commit) *(racecheck/finalcheck skipped at user request)*
  - [x] 13e — Aliases removed *(broad)* (`7893db6ce`, `a858f66ba`,
    `6079f8c03`, `99a56020a`, then the docs commit) *(racecheck/finalcheck
    skipped at user request)*
- [x] **Step 14 — `routine`** ([prompt](ENGINE-SPLIT-routine.md); Part A
  needs `PG18-ROUTINE-CORE` merged, 14b also the F3 aliases item; Part B
  needs 13d)
  - [x] 14a — In place (`f4645bc19`, `87bf1455e`, `f33847363`, then the docs
    commit) *(racecheck skipped at user request)*
  - [x] 14b — `routine` created and clean set moved (`aaf6759a4`,
    `d6becc9d6`, then the docs commit) *(racecheck skipped at user request)*
  - [x] 14c — Host refined (docs only; the commit that records this status)
  - [x] 14d — DDL handlers moved (`df37c5dfb`, `193c703f7`, `5604d3ccf`,
    then the docs commit) *(racecheck/finalcheck skipped at user request)*
- [x] **Step 15 — `typedef`** ([prompt](ENGINE-SPLIT-typedef.md); Part A
  needs 14b; Part B needs 13d)
  - [x] 15a — In place (`1045960f6`, `9e4924bbd`, `0add6a48b`, `92fd3299a`,
    `ed21b3bf3`, then the docs commit) *(racecheck/finalcheck skipped at user
    request)*
  - [x] 15b — `typedef` created and clean set moved (`d4d99a162`,
    `5d3f4af95`, `b67b62d22`, then the docs commit) *(racecheck/finalcheck
    skipped at user request)*
  - [x] 15c — Host refined (docs: the commit that records this status)
    *(racecheck/finalcheck skipped at user request)*
  - [x] 15d — DDL handlers moved (`b239d72bd`, `6e22f709a`, `2122c7bc2`,
    `37c02a9f6`, `9688a64ac`, then the docs commit) *(racecheck/finalcheck
    skipped at user request)*
- [x] **Step 16 — `extension`** ([prompt](ENGINE-SPLIT-extension.md); Part A
  needs 15b; Part B needs 13d)
  - [x] 16a — In place (`d0a013406`, `bc0e3d81b`, `54d886952`, `46da1763e`, then the docs commit)
  - [x] 16b — `extension` created and clean set moved (`e6ff96286`,
    `a8f3f5a8e`, `d55a47824`, then the docs commit) *(racecheck/finalcheck
    skipped at user request)*
  - [x] 16c — Host refined (docs only; the docs commit)
  - [x] 16d — Installation handlers moved
    - [x] 16d.1 — Staging behind `extension.Stage`; installers removed
      (`9b88e37b1`, `6ee7b321c`, then the docs commit) *(racecheck/finalcheck
      skipped at user request)*
    - [x] 16d.2 — Handler decisions behind `extension.Host`
      (`b1398311f`, `659f7699d`, `ff925b36e`, then the docs commit)
      *(racecheck/finalcheck skipped at user request)*
- [x] **Step 17 — `sequence`** ([prompt](ENGINE-SPLIT-sequence.md); Part A
  needs Tier C sequence calls; Part B needs 13d)
  - [x] 17a — In place (`6ab03f25b`, `68d49b5a8`, `88c3aac0f`, `d43c3b1fb`,
    `48fe404db`, then the docs commit) *(racecheck skipped at user request)*
  - [x] 17b — `sequence` created and clean set moved (`b9aacc67c`,
    `6a7aaae6a`, then the docs commit) *(racecheck/finalcheck skipped at user
    request)*
  - [x] 17c — Host refined (docs only; the docs commit)
  - [x] 17d — Handler decisions moved (`10d8611a9`, `f5f7d1354`,
    `4be715db5`, `f66434ed1`, `468bb3ade`, then the docs commit)
    *(racecheck/finalcheck skipped at user request)*
- [x] **Postponed packages** — planned in the second round (item 54): steps
  18–30 below. `inheritance` is part of step 24 (`table`); schema-level DDL
  is split between step 20 (`ddl`), `catalog` 5i, `regobj` and `table`
  ([ENGINE-SPLIT.md §6](ENGINE-SPLIT.md#steps-1830--the-postponed-packages-and-their-seams)).
- [x] **Step 18 — `dump`** ([prompt](ENGINE-SPLIT-dump.md); needs —)
  - [x] 18a — In place: `dump_graph.go`, snapshot functions (`35544f886`,
    `c201b0a69`, then the docs commit)
  - [x] 18b — `dump` created and graph records moved (`fd27cb76f`,
    `6d4ca83c9`, `929c3a8d0`, `4fced3bba`, then the docs commit)
  - [x] 18c — Collect/Emit provider tables replace Dump switches #1 and #2
    (`82ccf9f5c`, `5342c324b`, `56731f78c`, `45eac6657`, `4beca3bd7`, then
    the docs commit) *(racecheck/finalcheck skipped at user request)*
- [x] **Step 19 — `copyfmt`** ([prompt](ENGINE-SPLIT-copyfmt.md); needs —)
  - [x] 19a — COPY cursors split into codec and DML parts, in place
    (`b2d22701d`, `50cc79234`, `decb25737`, `9118a9cf0`, then the docs commit)
  - [x] 19b — `copyfmt` created and codecs moved (`9e19cdd1a`, `644babd76`, `3b0f57c68`,
    then the docs commit)
- [x] **Step 20 — `ddl`** ([prompt](ENGINE-SPLIT-ddl.md); needs —)
  - [x] 20a — One `Authority`, `Stage` and `Host` contract
  - [x] 20b — One root staging adapter (`b0d3e53b7`, `d25058ea5`, then the
    docs commit)
  - [x] 20c — Schema-level DDL host refined (docs only; the docs commit)
  - [x] 20d — Schema and tablespace decisions moved (`77b3fabf1`, `40deebd48`, `c8167af8d`, `4d5daf404`, then the
    docs commit)
- [x] **Step 21 — `regobj`** ([prompt](ENGINE-SPLIT-regobj.md); 21b needs
  5j)
  - [x] 21a — In place: regclass and visibility files, renderer split
    (`faabae6f1`, `fdea22a7e`, `bc4972dc4`, `4c5e82e76`, `c8c5d6f0c`,
    `108381a52`, `3a26d7fbb`, `46a317e4d`, then the docs commit)
  - [x] 21b — `regobj` created and clean set moved (`91fc9de2f`,
    `8ea91a2eb`, `ca7c8d575`, then the docs commit)
  - [x] 21c — Captured input resolvers moved (`8565a61e7`, `fc8a45cdd`, `b65da8100`, then the docs
    commit)
- [x] **Step 22 — `stored`** ([prompt](ENGINE-SPLIT-stored.md); 22b needs
  21b, F6, 18b and 12f, 22c needs 5j)
  - [x] 22a — In place: coverage cut, freeze cut, accessors, renames
    (`fc0f6dbe8`, `219495724`, `f9035b171`, `a0224bb18`, `905c53bad`,
    `bec9e323f`, `89b74100f`, then the docs commit)
  - [x] 22b — `stored` created, presentation set moved with aliases *(broad)*
    (`ae29be757`, `4f51dfb7c`, `b66915729`, `687d5dc84`, `96720fefc`, `020d1a446`, `b6960ba81`,
    then the docs commit)
  - [x] 22c — Rule and policy payloads, remap protocol
    (`7eb30702d`, `06b8adad6`, `a3cd3577f`, `c06636d9f`, `751c1a011`,
    then the docs commit)
  - [x] 22d — Aliases removed *(broad)*
    (`b4cdcf403`, `ffd4a418d`, then the docs commit)
- [ ] **Step 23 — `index`** ([prompt](ENGINE-SPLIT-index.md); 23b needs 22b,
  5i, 10e, 11e)
  - [x] 23a — In place: renames, wait and exclusion registries, cuts
    (`61d7f49b6`, `36cdc6168`, `98ee18fcf`, `dec8ce2ea`, `b29db018c`, `79bc16332`, `040604526`, docs)
  - [x] 23b — `index` created and clean set moved (`5d34f151d`,
    `b372d8d83`, `0f4efc1d7`, `ef8ed94f9`, docs)
  - [x] 23c — Host refined (docs only) (the docs commit)
  - [x] 23d.1 — DDL decisions moved (CREATE INDEX, ALTER/DROP INDEX,
    REINDEX, constraint and partitioned indexes) (`5e230528b`, `e46899fe6`,
    `506da64a4`, `a92895430`, docs)
  - [ ] 23d.2 — Write-time decisions moved (unique and exclusion
    enforcement)
- [ ] **Step 24 — `table`**, with inheritance ([prompt](ENGINE-SPLIT-table.md);
  24a needs 5i, 24b needs 23b, 18b, B6)
  - [x] 24a — In place: renames, slots, cuts (`56270ba2f`, `4dcdf411c`,
    `b210c6777`, `260ed66c6`, `5b4ac930a`, `36249b2b4`, `e247d07c6`, docs)
  - [x] 24b — `table` created and clean set moved *(broad)* (`7e9873c11`,
    `b05bbcdf9`, `8797fe715`, `a0ef76b6c`, `e0eb9d15d`, `30ed6f576`, docs)
  - [x] 24c — Host refined (docs only) (`e6176d3c3` regression fix, docs)
  - [ ] 24d.1 — CREATE decisions moved (CREATE TABLE, LIKE, identity and
    serial, FK staging, DROP/SET SCHEMA cores)
  - [ ] 24d.2 — ALTER and inheritance decisions moved (candidates,
    rebuilds, recursion decisions, removal reader)
  - [ ] 24d.3 — Restore and dump cores moved
- [ ] **Step 25 — `partition`** ([prompt](ENGINE-SPLIT-partition.md); 25b
  needs 24b)
  - [x] 25a — In place: proof inputs, storage-read fences, renames
    (`22bba0e50`, `47d732220`, `bbe3e3cc1`, then the docs commit)
  - [x] 25b — `partition` created and clean set moved (`5ed4b040c`,
    `f3e01d002`, `b7acfe47e`, `917899609`, docs)
  - [ ] 25c — Host refined (docs only)
  - [ ] 25d — Handler decisions moved
- [ ] **Step 26 — `fdw`** ([prompt](ENGINE-SPLIT-fdw.md); 26e needs 24d)
  - [x] 26a — In place: `fdw_` files, `foreignRegistry`, cuts
    (`6cb327eb1`, `c3a7cf7e1`, `1ba975174`, `0c991e6c8`, `d7405f486`, `a02ccecb1`, then the docs commit)
  - [x] 26b — `fdw` created and clean set moved
    (`6bce27045`, `f2df07054`, `fe35300c3`, `5e3147354`, then the docs commit)
  - [x] 26c — Host refined (docs only; the docs commit)
  - [x] 26d — Wrapper, server, mapping, import and grant decisions moved
    (`743eb4d47`, `6fe2aeaa2`, `e942c6231`, then the docs commit)
  - [ ] 26e — Foreign-table decisions moved
- [ ] **Step 27 — `view`** ([prompt](ENGINE-SPLIT-view.md); 27b needs 22c,
  24b)
  - [x] 27a — In place: `access.View` cuts, slots, renames, dead code
    (`87bfbb497`, `16260a489`, `a0254a5c3`, `57c781f25`, `7472271b6`,
    `a5e32e52a`, `512cf3e1c`, `24b0bfde4`, then the docs commit)
  - [x] 27b — `view` created and clean set moved *(broad)*
    (`03468b5b6`, `d7cd77e1a`, `b61bed3bb`, `73c98d60b`, then the docs commit)
  - [ ] 27c — Host refined (docs only)
  - [ ] 27d — Handler decisions moved
- [x] **Step 28 — `role`** ([prompt](ENGINE-SPLIT-role.md); 28b needs 21b,
  B6) *(lower priority)*
  - [x] 28a — In place: renames, `access.View` cuts, ownership stage
    interfaces, grant, default-privilege and owned plans split
    (`23c4487dc`, `40d6de294`, `5f467d54d`, `32097b551`, `86c27112c`,
    `0ed6411b1`, then the docs commit)
  - [x] 28b — `role` created and clean set moved (`a781528f1`,
    `eb6d750cc`, `0521e3b83`, `2492afb3f`, then the docs commit)
  - [x] 28c — Host refined (docs only) (the docs commit)
  - [x] 28d — Handler decisions moved (`c320c6ec8`, `d0ad04e54`, `1ef3f87f9`, `0a0c9ef3e`, `6455845cc`, then the docs commit)
- [ ] **Step 29 — `dml`** ([prompt](ENGINE-SPLIT-dml.md); 29b needs Part A of
  19 and 23–27, 29c needs Part B of 23–27)
  - [x] 29a — In place: `insert.go`, misplaced code to owners, receivers
    (`919c6319f`, `3bbec01e0`, `146d99d5b`, `03b5e1c78`, then the docs commit)
  - [ ] 29b — `dml` created and clean set moved
  - [ ] 29c — Host refined (docs only)
  - [ ] 29d — Executors moved
- [ ] **Step 30 — `control`** ([prompt](ENGINE-SPLIT-control.md); 30a needs
  18c; 30b needs the Part A moves and 14e, 15e)
  - [x] 30a — Restore hook table replaces the statement switch, in place
    (`dc839f171`, `192993651`, `fbcdc7fc9`, `22ef19659`, `6dc277b11`,
    `2eb9396e7`, `ba1fc1495`, `c22c9b9fb`, `80cc70d38`, `050e10e07`,
    `26ecb355d`, then the docs commit)
  - [ ] 30b — Host refined (docs only)
  - [ ] 30c — `control` created and drivers moved
- [x] **Second-round follow-ups in existing packages** (each in its step's
  prompt file; F6 is listed under step 3)
  - [x] 5i — Pure helpers moved into `catalog` ([catalog](ENGINE-SPLIT-catalog.md))
    (`a81741ca9`, `167ed0a8e`, `cab75ed1a`, `bd4de2c3f`, `7fcac5c5a`,
    `86e3fd0e4`, `71759a72e`, docs) *(racecheck/finalcheck skipped at user
    request)*
  - [x] 5j — Payload accessors replace five host and runtime methods
    ([catalog](ENGINE-SPLIT-catalog.md)) (`946137fa2`, `f4e53eef6`, then the
    docs commit)
  - [x] 10e — Node-tree output and generation helpers moved
    ([plan](ENGINE-SPLIT-plan.md); `fd104099c`, `8ec047f40`, `9b8cf388f`,
    docs)
  - [x] 11e — Expression-preparation seam; routine admission moved
    ([exec](ENGINE-SPLIT-exec.md); `cee6f2c90`, `ce43a2044`, `674032f28`,
    `239a13dc5`, `61f0e8305`, docs) *(racecheck/finalcheck skipped at user
    request)*
  - [x] 11f — Preflight moved ([exec](ENGINE-SPLIT-exec.md); `78e557fef`, `f1287c2c4`, `013311667`,
    docs) *(racecheck/finalcheck skipped at user request)*
  - [x] 12f — Query legality helpers moved ([analyze](ENGINE-SPLIT-analyze.md);
    `d65defe64`, `d77505532`, docs) *(racecheck/finalcheck skipped at user
    request)*
  - [x] 13f — Notification records, isolation text and owner handles moved
    ([session](ENGINE-SPLIT-session.md); `c639cc64d`, `8473b1789`, `4a6bea8d3`, docs)
    *(racecheck/finalcheck skipped at user request)*
  - [x] B6 — Ownership helpers and RLS action rules moved into `access`
    ([authorization](ENGINE-SPLIT-authorization.md); `27d60116f`, `53410d65b`,
    docs) *(racecheck/finalcheck skipped at user request)*
  - [x] 14e — Operator and SQL routine dumps moved
    ([routine](ENGINE-SPLIT-routine.md); `a921ae3f5`, `d507782c3`, docs)
    *(racecheck/finalcheck skipped at user request)*
  - [x] 15e — Type dump statements moved ([typedef](ENGINE-SPLIT-typedef.md);
    `daaaf0da3`, `f1b6a78c7`, `27fe76274`, docs) *(racecheck/finalcheck skipped at user request)*

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
41. [x] Analyze 12e — [analyze](ENGINE-SPLIT-analyze.md) 12e · needs 40 ·
    **broad** (`b899826fd`, `541d4b757`, `83c15f51d`, then the docs commit)
    · selected dual-mode proof and quickcheck passed; racecheck/finalcheck
    skipped at user request; the driver imports `analyze` for
    `ParameterInfo`
42. [x] Access B5: binding walkers —
    [authorization](ENGINE-SPLIT-authorization.md) B5 · needs 40 ·
    (`76039ffea`, `4748b28ca`, then the docs commit) · `CheckBinding` and
    `CheckQuerySources` moved; view, view-write and RLS walkers stay with
    their payloads · selected dual-mode proof and quickcheck passed;
    racecheck/finalcheck skipped at user request
43. [x] Session 13c (refine, docs only) —
    [session](ENGINE-SPLIT-session.md) 13c · needs 23, 37, 40, 14 · (the
    commit that records this status) · state moves, the scope and
    orchestration stay; quickcheck passed; racecheck/finalcheck skipped at
    user request
44. [x] Session 13d (several sessions) — [session](ENGINE-SPLIT-session.md)
    13d · needs 43 · **broad** · done when 44a–44f are ticked
    - 44a. [x] 13d.1 journal, creates `session` · needs 43 · **broad**
      (`548b81e54`, `353ec50ae`, `7cc803313`, `2eb0cefb3`, then the docs
      commit) · selected unit/`RACE=1` and dual-mode proof (47
      `REFERENCE=1` tests) and quickcheck passed; bench unchanged;
      racecheck/finalcheck skipped at user request
    - 44b. [x] 13d.2 statement lease and small cuts · needs 43 · may run in
      parallel with 44a (`d31baeb58`, `00fec2882`, then the docs commit) ·
      selected unit/`RACE=1` and dual-mode proof and quickcheck passed;
      racecheck/finalcheck skipped at user request
    - 44c. [x] 13d.3 engine edges · needs 44a, 44b · **broad**
      (`41dc92a34`, `12273fdb0`, `ff9eaf1a6`, `0ae40ee4e`, then the docs
      commit) · `session.Host`, `engineOf`, owner root functions · selected
      unit/`RACE=1` and dual-mode proof and quickcheck passed;
      racecheck/finalcheck skipped at user request
    - 44d. [x] 13d.4 root payloads · needs 44c (`de6299e91`, `40085169a`,
      `b29e65cda`, `902eec726`, `405e83b53`, then the docs commit) · session
      payload, owner extension, savepoint exclusion mark and operation
      replay/analysis slots; notification polling on `Engine` · selected
      unit/`RACE=1` and dual-mode proof and quickcheck passed; bench
      unchanged; racecheck/finalcheck skipped at user request
    - 44e. [x] 13d.5 file splits and exports · needs 44d · **broad**
      (`c56fbf397`, `18c5f63d2`, `6aea40304`, `e07c17cae`, `eb5e6fe83`,
      then the docs commit) · 15 moving-only files, 192 members exported ·
      selected unit/`RACE=1` and dual-mode proof and quickcheck passed;
      bench unchanged; racecheck/finalcheck skipped at user request
    - 44f. [x] 13d.6 move · needs 44e · **broad** (`1770770a8`,
      `f4c4bd5de`, `71a7ad850`, then the docs commit) · session state moved
      with 21 type and 22 constant aliases; `Host.NewOperation` · selected
      unit/`RACE=1` and dual-mode proof and quickcheck passed; bench
      unchanged; racecheck/finalcheck skipped at user request
45. [x] Session 13e — [session](ENGINE-SPLIT-session.md) 13e · needs 44 ·
    **broad** (`7893db6ce`, `a858f66ba`, `6079f8c03`, `99a56020a`, then the
    docs commit) · 46 aliases removed, 1,536 engine and 66 driver/CLI
    references; the driver and `cmd/stuntgres` import `session` · selected
    unit/`RACE=1` and dual-mode proof and quickcheck passed; bench unchanged;
    racecheck/finalcheck skipped at user request

### Group F — feature packages, Part B

46. [x] Routine 14c (refine, docs only) — [routine](ENGINE-SPLIT-routine.md)
    14c · needs 44, 25 · (the commit that records this status) ·
    `routine.DDLHost` on a scope-free root `schemaHost`, authority as an
    opaque value, dumps wait for a dump seam; 14d is one session ·
    quickcheck passed; racecheck/finalcheck skipped at user request
47. [x] Routine 14d — [routine](ENGINE-SPLIT-routine.md) 14d · needs 46 ·
    (`df37c5dfb`, `193c703f7`, `5604d3ccf`, then the docs commit) · operator
    and CREATE FUNCTION staging in `routine` behind `DDLHost` on the root
    `schemaHost`; nine option helpers unexported; no aliases · selected
    unit and dual-mode proof and quickcheck passed; racecheck/finalcheck
    skipped at user request
48. [x] Typedef 15c (refine, docs only) — [typedef](ENGINE-SPLIT-typedef.md)
    15c · needs 44, 27 · (docs: the commit that records this status) ·
    `typedef.StageHost` on 14c's `schemaHost`, authority as a value; stage
    handlers and two staging cores move, executors stay · quickcheck passed;
    racecheck/finalcheck skipped at user request
49. [x] Typedef 15d — [typedef](ENGINE-SPLIT-typedef.md) 15d · needs 48 ·
    (`b239d72bd`, `6e22f709a`, `2122c7bc2`, `37c02a9f6`, `9688a64ac`, then
    the docs commit) · type stage handlers and the ALTER ENUM/DOMAIN staging
    cores in `typedef` behind `StageHost` on the root `typedefHost`; no
    aliases · selected unit and dual-mode proof and quickcheck passed;
    racecheck/finalcheck skipped at user request
50. [x] Extension 16c (refine, docs only) —
    [extension](ENGINE-SPLIT-extension.md) 16c · needs 44, 29 · (the docs
    commit) · measured at `1f606f52d`: providers own staging behind
    `extension.Stage`, handler decisions behind `extension.Host`, one root
    adapter `extensionHost`; dispatch and publication stay in the root; no
    aliases
51. [x] Extension 16d — [extension](ENGINE-SPLIT-extension.md) 16d · needs
    50
    - 51a. [x] 16d.1 staging — [extension](ENGINE-SPLIT-extension.md#16d1--staging-linear-item-51a)
      · needs 50 (`9b88e37b1`, `6ee7b321c`, then the docs commit) · providers
      stage themselves behind `extension.Stage`, root `extensionHost`,
      installers removed · selected unit and dual-mode proof and quickcheck
      passed; bench unchanged; racecheck/finalcheck skipped at user request
    - 51b. [x] 16d.2 handlers — [extension](ENGINE-SPLIT-extension.md#16d2--handlers-linear-item-51b)
      · needs 51a (`b1398311f`, `659f7699d`, `ff925b36e`, then the docs
      commit) · `StageCreate`, `StageUpdate`, `DropTargets`, `ImportOwners`
      and `AuthorityObservation.Current` in `extension` behind `Host` and
      `Authority`; dispatch, publication and the removal pipeline stay in
      the root; no aliases · selected unit and dual-mode proof and
      quickcheck passed; racecheck/finalcheck skipped at user request
52. [x] Sequence 17c (refine, docs only) —
    [sequence](ENGINE-SPLIT-sequence.md) 17c · needs 44, 31 · (the docs
    commit) · measured at `1f606f52d`: decisions move as pure functions,
    orchestration stays in the root; no `sequence.Host`; no aliases
53. [x] Sequence 17d — [sequence](ENGINE-SPLIT-sequence.md) 17d · needs 52
    (`10d8611a9`, `f5f7d1354`, `4be715db5`, `f66434ed1`, `468bb3ade`, then
    the docs commit) · decisions moved as pure functions, `sequence.Stage`;
    orchestration stays in the root; no aliases · selected unit/`RACE=1` and
    dual-mode proof and quickcheck passed; bench unchanged;
    racecheck/finalcheck skipped at user request

### Group G — next planning round

54. [x] Plan the postponed packages (docs only, like this round): prompt
    files for `index`, `partition`, `inheritance`, `table`, `view`, `fdw`,
    `dml` and `control` — [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §6 "Postponed
    packages" · needs 44 · (the docs commit; measured at `c154e14fb`) ·
    steps 18–30 and the follow-ups below; `inheritance` folded into
    `table`; seams `dump`, `copyfmt`, `ddl`, `regobj`, `stored` and `role`
    added

The items below come from the second planning round. Each is one session
unless its prompt file says otherwise; the list is flat on purpose: when an
item splits, add lettered sub-items (`68a`, `68b`) as rule 5 says.

### Group H — shared seams (steps 18–22)

55. [x] Dump 18a: in place — [dump](ENGINE-SPLIT-dump.md) 18a · needs —
    (`35544f886`, `c201b0a69`, then the docs commit) · `dump_graph.go`,
    `dump_restore_scalar.go`, ACL preflight as a function · selected unit
    and dual-mode proof and quickcheck passed; racecheck/finalcheck skipped
    at user request
56. [x] Dump 18b: `dump` created, graph records moved —
    [dump](ENGINE-SPLIT-dump.md) 18b · needs 55 (`fd27cb76f`, `6d4ca83c9`,
    `929c3a8d0`, `4fced3bba`, then the docs commit) · event graph, `Order`,
    `Statement`, `Group`, Restore scalar validators, `Resolver`/`Presenter`
    with root adapters; no `plan` import, no aliases · selected unit and
    dual-mode proof, byte-identical dump smoke and quickcheck passed;
    racecheck/finalcheck skipped at user request
57. [x] Dump 18c: Collect/Emit provider tables, in place —
    [dump](ENGINE-SPLIT-dump.md) 18c · needs 56 (`82ccf9f5c`, `5342c324b`,
    `56731f78c`, `45eac6657`, `4beca3bd7`, then the docs commit) ·
    `captureDumpLocked` unlocks `Engine.mu` once; `dumpCollectors` and
    `dumpRelationCollectors`, the phase-indexed `dumpPhaseEmitters` and the
    `dumpSections` list; `control.go` 2,288 → 729 lines · byte-identical
    dump smoke, selected unit, `RACE=1` Control and dual-mode proof and
    quickcheck passed; racecheck/finalcheck skipped at user request
58. [x] COPY codecs 19a: cursors split, in place —
    [copyfmt](ENGINE-SPLIT-copyfmt.md) 19a · needs — (`b2d22701d`, `50cc79234`, `decb25737`, `9118a9cf0`,
    then the docs commit) · `copyLineReader` and `copyBinaryReader` hold
    the codec, 913 clean lines; allocations unchanged · selected
    unit/`RACE=1` and dual-mode proof and quickcheck passed;
    racecheck/finalcheck skipped at user request
59. [x] COPY codecs 19b: `copyfmt` created, codecs moved —
    [copyfmt](ENGINE-SPLIT-copyfmt.md) 19b · needs 58 (`9e19cdd1a`, `644babd76`, `3b0f57c68`,
    then the docs commit) · options, FORCE binding, `LineReader`,
    `BinaryReader`; `copyTargets` is `copyfmt.ResolveColumns`; no aliases;
    allocations unchanged · selected unit/`RACE=1` and dual-mode proof and
    quickcheck passed; racecheck/finalcheck skipped at user request
60. [x] DDL 20a: one `Authority`, `Stage` and `Host` contract —
    [ddl](ENGINE-SPLIT-ddl.md) 20a · needs — (`3ec2314b9`, `19701cafd`,
    `578ed8479`, `201947f6d`, then the docs commit) · `routine`, `typedef`
    and `extension` adopt `ddl.Authority`/`Stage`/`Host`; one
    `DDLAuthority` marker · selected unit and dual-mode proof and
    quickcheck passed; racecheck/finalcheck skipped at user request
61. [x] DDL 20b: one root staging adapter — [ddl](ENGINE-SPLIT-ddl.md) 20b ·
    needs 60 (`b0d3e53b7`, `d25058ea5`, then the docs commit) · `ddlHost`
    replaces `schemaHost` and `typedefHost`; consumer methods in
    `routine_host.go` and `typedef_host.go`; `extensionHost` unchanged; no
    aliases · selected unit and dual-mode proof and quickcheck passed;
    racecheck/finalcheck skipped at user request
62. [x] DDL 20c: schema-level DDL host refined (docs only) —
    [ddl](ENGINE-SPLIT-ddl.md) 20c · needs 61 (the docs commit) ·
    `ddl.SchemaHost` = `Host` + five methods on `ddlHost`;
    `RecordDependencyPath` returns its error; CREATE TABLESPACE over
    `access.View`; seed comments to `catalog`; 20d move set and commit
    sequence written · docs only, no checks needed
63. [x] DDL 20d: schema and tablespace decisions moved —
    [ddl](ENGINE-SPLIT-ddl.md) 20d · needs 62 (`77b3fabf1`, `40deebd48`, `c8167af8d`, `4d5daf404`,
    then the docs commit) · `ddl.SchemaHost` on `ddlHost`;
    `StageCreateSchema`/`StageAlterSchema`/`StageDropSchema` and
    `CreateTablespace` in `ddl`; seed comments in `catalog`; no aliases ·
    selected unit and dual-mode proof and quickcheck passed;
    racecheck/finalcheck skipped at user request
64. [x] regobj 21a: in place — [regobj](ENGINE-SPLIT-regobj.md) 21a · needs —
    (`faabae6f1` … `46a317e4d`, then the docs commit) · regclass, literal,
    namespace-visibility, identity and presentation files gathered;
    `pgIdentifier` is `catalog.Identifier` · selected unit and dual-mode
    proof and quickcheck passed; racecheck/finalcheck skipped at user request
65. [x] regobj 21b: `regobj` created, clean set moved —
    [regobj](ENGINE-SPLIT-regobj.md) 21b · needs 64, 74 (`91fc9de2f`,
    `8ea91a2eb`, `ca7c8d575`, then the docs commit) · 23 files (about 2.8k
    lines) in `regobj`; names settled in its README; `regtypeAnalysis`
    stays in the root; no aliases · selected unit and dual-mode proof and
    quickcheck passed; racecheck/finalcheck skipped at user request
66. [x] regobj 21c: captured input resolvers moved —
    [regobj](ENGINE-SPLIT-regobj.md) 21c · needs 65 (`8565a61e7`, `fc8a45cdd`, `b65da8100`, then
    the docs commit) · six resolvers in `regobj` behind `regobj.Authority`
    and `NoticeHost`; constructors stay in the root; no aliases · selected
    unit/`RACE=1` and dual-mode proof and quickcheck passed;
    racecheck/finalcheck skipped at user request
67. [x] stored 22a: in place (coverage cut, freeze cuts, accessors,
    renames) — [stored](ENGINE-SPLIT-stored.md) 22a · needs —
    (`fc0f6dbe8`, `219495724`, `f9035b171`, `a0224bb18`, `905c53bad`,
    `bec9e323f`, `89b74100f`, then the docs commit) · coverage through
    `indexCoverageReader` (32 declarations clean), `freezeQueryPresentation`
    a function, `viewDefinition` read accessors, renames, name `stored` ·
    selected unit and dual-mode proof and quickcheck passed;
    racecheck/finalcheck skipped at user request
68. [x] stored 22b: `stored` created, presentation set moved with aliases —
    [stored](ENGINE-SPLIT-stored.md) 22b · needs 67, 65, 71, 56, 77 ·
    **broad** (`ae29be757`, `4f51dfb7c`, `b66915729`, `687d5dc84`, `96720fefc`, `020d1a446`, `b6960ba81`, then the docs commit) · 55 files (about 10k lines) in `stored`; `View`/`Policy`
    payload API (`ViewParts`, `NewView`, `Parts`, `With`, accessors,
    `NewPolicy`); runtime bodies forward; 12 type aliases in
    `stored_aliases.go` · selected unit and dual-mode proof and quickcheck
    passed; racecheck/finalcheck skipped at user request
69. [x] stored 22c: rule and policy payloads, remap protocol —
    [stored](ENGINE-SPLIT-stored.md) 22c · needs 68, 74 (`7eb30702d`, `06b8adad6`, `a3cd3577f`, `c06636d9f`, `751c1a011`, then the
    docs commit) · 21 files in `stored`; `Rule`/`RuleAction` parts API;
    rule, policy and row-security dumps; rule, policy and partition
    remaps; `ReownExpressions`; `DeparseInquiry` measured and left in the
    root; 3 more type aliases · selected unit and dual-mode proof,
    byte-identical dump smoke and quickcheck passed; racecheck/finalcheck
    skipped at user request
70. [x] stored 22d: aliases removed — [stored](ENGINE-SPLIT-stored.md) 22d ·
    needs 69 · **broad** (`b4cdcf403`, `ffd4a418d`, then the docs commit) · 440 uses
    in 180 root files name `stored.X`; `stored_aliases.go` and its 15 type
    aliases removed; no shadowing locals; five subpackage comments
    renamed · selected unit, quick-mode reference selections, quickcheck and a narrow `REFERENCE=1` selection passed; racecheck/finalcheck skipped at user request

### Group I — follow-ups in existing packages

71. [x] Functions F6: step 3 closed — [functions](ENGINE-SPLIT-functions.md)
    F6 · needs — · (`c3ba6773d` seam, `95f4feb13` engine cutover, then the
    docs commit) · `RemappableProvider` replaces both provider switches in
    `view_remap.go`; remaining `case functions.` matches recorded · selected
    unit and dual-mode proof, quickcheck and a byte-identical public probe
    passed; racecheck/finalcheck skipped at user request
72. [x] Catalog 5i: pure helpers moved down —
    [catalog](ENGINE-SPLIT-catalog.md) 5i · needs — (`a81741ca9`,
    `167ed0a8e`, `cab75ed1a`, `bd4de2c3f`, `7fcac5c5a`, `86e3fd0e4`,
    `71759a72e`, then the docs commit) · hierarchy, constraint-naming,
    definition-OID, tablespace and namespace helpers, `ExtensionMembership`
    and `RemapPrincipals` in `catalog`; `structuralNull` in `plan`; the
    physical NOT NULL validators and `sameRelationRowShape` stay in the root ·
    selected unit and dual-mode proof and quickcheck passed;
    racecheck/finalcheck skipped at user request
73. [x] Plan 10e: node-tree output and generation helpers —
    [plan](ENGINE-SPLIT-plan.md) 10e · needs — (`fd104099c`, `8ec047f40`,
    `9b8cf388f`, then the docs commit) · `plan.BoundTree`,
    `AppendBoundTree`, `CatalogTree`, `GenerationImmutable`,
    `GenerationHasTableOID`; `SubscriptChildren` moved down from `exec` ·
    selected unit and dual-mode proof and quickcheck passed;
    racecheck/finalcheck skipped at user request
74. [x] Catalog 5j: payload accessors — [catalog](ENGINE-SPLIT-catalog.md) 5j ·
    needs — (`946137fa2`, `f4e53eef6`, then the docs commit) · rule, view
    and policy payload readers; five host/runtime methods and
    `pgcatalog.RuleFact` removed; no aliases · selected unit and dual-mode
    proof and quickcheck passed; racecheck/finalcheck skipped at user request
75. [x] Exec 11e: expression-preparation seam, routine admission —
    [exec](ENGINE-SPLIT-exec.md) 11e · needs — (`cee6f2c90`, `ce43a2044`,
    `674032f28`, `239a13dc5`, `61f0e8305`, then the docs commit) ·
    `Preparation.PreflightCoercionsWithPolicy`/`AdmitExpression` serve the
    index, table and partition callers; routine call admission, the routine
    descriptor helpers, DETAIL/capture/cursor-scan helpers in `exec`;
    `exec.RoutineAccess`, `Host.RoutineAdmission`, `ReturnsSet` and
    `StoredReferenceValid` hooks, `ViewPayload.Dependencies` ·
    selected unit and dual-mode proof and quickcheck passed;
    racecheck/finalcheck skipped at user request
76. [x] Exec 11f: preflight — [exec](ENGINE-SPLIT-exec.md) 11f · needs 75
    (`78e557fef`, `f1287c2c4`, `013311667`, then the docs commit) · the expression
    preflight family, array/UPDATE value preflight, string variadic folding
    and the OVERLAPS route and cost model in `exec`; `Preparation` gains
    `PreflightQuery`, `PrepareDomainConversion` and
    `SpecializeRuleParameters` and drops the coercion methods; query
    preflight, rule parameter specialization and the domain programme stay
    in the root · selected unit and dual-mode proof and quickcheck passed;
    racecheck/finalcheck skipped at user request
77. [x] Analyze 12f: query legality helpers —
    [analyze](ENGINE-SPLIT-analyze.md) 12f · needs — (`d65defe64`,
    `d77505532`, then the docs commit) · `query_graph.go` and
    `query_window_prune.go` whole, the clean join and row-lock legality
    helpers and `ClosedRegclassDefault` in `analyze`, exported where the
    root calls them; executor walks and stored-view row-mark readers stay;
    no aliases · selected unit and dual-mode proof and quickcheck passed;
    racecheck/finalcheck skipped at user request
78. [x] Session 13f: notification records, isolation text, owner handles —
    [session](ENGINE-SPLIT-session.md) 13f · needs — (`c639cc64d`, `8473b1789`, `4a6bea8d3`, then the docs
    commit) · `session.IsolationFromText`/`IsolationText`, the owner handles
    (`TxOwnerOf`, `PrivateSessionOf`, `TxOwnerAttached`, `TokenAttached`) and
    the notification queue records and helpers (with
    `ScanNotificationPending`) in `session`; no aliases · selected unit and
    dual-mode proof and quickcheck passed; racecheck/finalcheck skipped at
    user request
79. [x] Access B6: ownership helpers and RLS action rules —
    [authorization](ENGINE-SPLIT-authorization.md) B6 · needs — (`27d60116f`,
    `53410d65b`, then the docs commit) · `OwnershipRoot`, `OwnedRoots`,
    `OwnershipRelationLocks`, `OwnedPolicies`, `OwnedMembershipCandidate`,
    `RowSecurityActions`, `RowSecurityFlags` and `RowSecurityKind` in
    `access`; the seeded-operator predicate and the table-kind diagnostic
    are passed as values (`routine` and `analyze` import `access`); no
    aliases · selected unit and dual-mode proof and quickcheck passed;
    racecheck/finalcheck skipped at user request
80. [x] Routine 14e: operator and SQL routine dumps —
    [routine](ENGINE-SPLIT-routine.md) 14e · needs 56, 67 (`a921ae3f5`,
    `d507782c3`, then the docs commit) · `routine/operator_dump.go` and
    `function_dump.go` render through `dump.Presenter` and `dump.Resolver`
    (the root passes `dumpPresenter{}`/`dumpResolver{}`); the operator
    Collect decision and the `OperatorLinks` text moved, the table entries,
    the procedure collector (`procedureProvider`, 22b) and the Restore hook
    (`sql_routine_restore.go`) stay in the root · selected unit and
    dual-mode proof, byte-identical dump smoke and quickcheck passed;
    racecheck/finalcheck skipped at user request
81. [x] Typedef 15e: type dump statements —
    [typedef](ENGINE-SPLIT-typedef.md) 15e · needs 56, 67
    (`daaaf0da3`, `f1b6a78c7`, `27fe76274`, then the docs commit) · collation, conversion,
    domain/composite (`MixedDumpStatements`) and NOT VALID domain CHECK dump
    statements in `typedef`; `dump.Presenter` gains `DomainDefault` and
    `DomainCheck`; graph edges and the Emit entry stay; no aliases ·
    selected unit and dual-mode proof, byte-identical dump smoke and
    quickcheck passed; racecheck/finalcheck skipped at user request

### Group J — upper features (steps 23–28)

82. [x] Index 23a: in place — [index](ENGINE-SPLIT-index.md) 23a · needs —
    (`61d7f49b6`, `36cdc6168`, `98ee18fcf`, `dec8ce2ea`, `b29db018c`, `79bc16332`, `040604526`, then the docs commit) · files renamed (`index_partition_*.go`,
    `using_index_prepare.go`, `index_alter_type.go`, `set_constraints.go`,
    `exclusion_registry.go`, ...); `storage.RowByID`; `indexWaitRegistry`
    and `exclusionRegistry`; validators over `access.View` and an
    environment; `prepareIndexPublication` a function; evolution, stage
    and layout-transport cuts; clean +1.36k (`exclusionScan` and
    `uniqueConflict` kept for 23d) · selected unit and dual-mode proof and
    quickcheck passed; racecheck/finalcheck skipped at user request
83. [x] Index 23b: `index` created, clean set moved —
    [index](ENGINE-SPLIT-index.md) 23b · needs 82, 68, 72, 75, 73
    (`5d34f151d`, `b372d8d83`, `0f4efc1d7`, `ef8ed94f9`, then the docs
    commit) · 48 files (about 6.4k lines) in `index`, with the partition
    replay chain, the unique NOT NULL support checks and the journal
    helpers; no aliases; ran broad (about 210 files qualified) · selected
    unit and dual-mode proof and quickcheck passed; racecheck/finalcheck
    skipped at user request
84. [x] Index 23c: host refined (docs only) — [index](ENGINE-SPLIT-index.md)
    23c · needs 83, 61 (the docs commit) · `index.Host` on `ddlHost` =
    `ddl.Host` + `AddDomainBase`, `RemoveObject`,
    `TrustedRestoreDestination`, `RelationDiagnostic` (existing) and
    `IndexStage`, `DescriptorStage`, `ACLOutput`, `DefinitionReader`,
    `LookupCollation`, `BindIndexExpression`, `BindIndexPredicate`; DROP
    staging and validity over narrow stage interfaces; `table.Host` embeds
    `index.Host`, table-owned edges hoisted; `index.WriteHost` on
    `statementHost` with self-locking windows and waits, registry
    primitives root-only, probe loops root until `dml`; SET CONSTRAINTS
    stays; 23d split into 85a/85b with move sets and commit sequences ·
    docs only, no checks needed
85a. [x] Index 23d.1: DDL decisions moved — [index](ENGINE-SPLIT-index.md)
    23d · needs 84 (`5e230528b`, `e46899fe6`, `506da64a4`, `a92895430`, then the docs commit) ·
    `index.Host` (with `KeyHost`) on `ddlHost`, `DropStage`,
    `ValidityTarget`, `UsingIndexStage`; CREATE INDEX, ALTER/DROP INDEX,
    REINDEX, key binding, UNIQUE/EXCLUDE staging, ALTER TYPE rebuilds,
    USING INDEX, partitioned-index staging and `Property` in `index`;
    `ddl.GeneratedRelationName`; table edges hoisted to the callers; no
    aliases · selected unit and dual-mode proof and quickcheck passed;
    racecheck/finalcheck skipped at user request
85b. [ ] Index 23d.2: write-time decisions moved —
    [index](ENGINE-SPLIT-index.md) 23d · needs 84, 85a's seam commit
86. [x] Table 24a: in place — [table](ENGINE-SPLIT-table.md) 24a · needs 72
    (`56270ba2f`, `4dcdf411c`, `b210c6777`, `260ed66c6`, `5b4ac930a`,
    `36249b2b4`, `e247d07c6`, then the docs commit) · files renamed
    (`alter_table.go`, `create_table.go`, `drop_table.go`,
    `mutation_targets.go`, ...); `tableStage` LIKE slot; `preparedDrop`
    statement token; `inheritanceAlterPlan`; FK diagnostics over
    `access.View`, restored FK authority as `ddl.Authority`;
    `dropDependencyReader`; `emitMaterializedSnapshot` a function; clean
    +0.79k · selected unit and dual-mode proof and quickcheck passed;
    racecheck/finalcheck skipped at user request
87. [x] Table 24b: `table` created, clean set moved (with inheritance) —
    [table](ENGINE-SPLIT-table.md) 24b · needs 86, 83, 56, 79 · **broad**
    (`7e9873c11`, `b05bbcdf9`, `8797fe715`, `a0ef76b6c`, `e0eb9d15d`,
    `30ed6f576`, then the docs commit) · 64 files (about 7.0k lines) in
    `table`: the dump record, NOT NULL, CHECK, DEFAULT and generated, FK DDL
    and probe, ALTER records and inheritance; definition delta and
    prepared-holder cuts; no aliases; ran broad (about 240 files) · selected
    unit and dual-mode proof and quickcheck passed; racecheck/finalcheck
    skipped at user request
88. [x] Table 24c: host refined (docs only) — [table](ENGINE-SPLIT-table.md)
    24c · needs 87, 84 (`e6176d3c3`, then the docs commit) · `table.Host`
    on `ddlHost` = `ddl.Host` + `index.Host` + `ddl.TypeStaging` +
    `StageRowType`, `ResolveType`, `ResolveColumnCollation` (existing) and
    `TableStage`, `SequenceStage`, `SequenceColumns`, `NameBinding`,
    `StageChanged`, `StagedTemporary`, `ResolveReference`,
    `ResolveRestoredRelation`, `DescribeCreateTable`, `BindColumnDefaults`,
    `BindGenerations`, `BindChecks`; `table.AlterHost` on a scope-bound
    `alterHost` adds `Input`, the snapshot captures, `CookDefault`,
    `BindGeneration`, `RebindCheck`, `BindAlterTypeUsing`,
    `TypeChangeDependents`, `StageIdentitySequenceOptions`; no
    `ddl.Removal` (`DropDependencyReader` stays table-local, passed in);
    prepare side and prepared drops stay root; Restore cores move, hooks
    stay; `relationDiagnosticKind` to `catalog`; 24d split into 89a/89b/89c
    with move sets and commit sequences · `e6176d3c3` fixes a 24b
    regression (the inheritance drop-discovery reader assertion never
    matched): unit, `RACE=1`, dual-mode selection and quickcheck passed;
    racecheck/finalcheck skipped at user request
89a. [ ] Table 24d.1: CREATE decisions moved —
    [table](ENGINE-SPLIT-table.md) 24d · needs 88, 85a
89b. [ ] Table 24d.2: ALTER and inheritance decisions moved —
    [table](ENGINE-SPLIT-table.md) 24d · needs 89a
89c. [ ] Table 24d.3: Restore and dump cores moved —
    [table](ENGINE-SPLIT-table.md) 24d · needs 89a
90. [x] Partition 25a: in place — [partition](ENGINE-SPLIT-partition.md) 25a ·
    needs — (`22bba0e50`, `47d732220`, `bbe3e3cc1`, then the docs commit) ·
    `partitionProof` takes the context and input environment;
    `partitionRecipeHost` on `statementHost`; `storageReadFence`,
    `partition_locks.go`, `partition_create_child.go` · selected unit and
    dual-mode proof and quickcheck passed; racecheck/finalcheck skipped at
    user request
91. [x] Partition 25b: `partition` created, clean set moved —
    [partition](ENGINE-SPLIT-partition.md) 25b · needs 90, 87 (`5ed4b040c`,
    `f3e01d002`, `b7acfe47e`, `917899609`, then the docs commit) · 22 files
    (about 3.1k lines) in `partition`: bounds, hash bounds and routing
    helpers, `InsertGroup`, key recipes with `RecipeHost` and the `Scope`
    slot, attach/detach helpers, proofs, retained checks, dump SQL and events,
    constraint and Restore replay records; dump code takes a `dump.Resolver`;
    `partitionInsertState` stays (dirty); no aliases · selected unit and
    dual-mode proof and quickcheck passed; racecheck/finalcheck skipped at
    user request
92. [ ] Partition 25c: host refined (docs only) —
    [partition](ENGINE-SPLIT-partition.md) 25c · needs 91, 88
93. [ ] Partition 25d: handler decisions moved —
    [partition](ENGINE-SPLIT-partition.md) 25d · needs 92, 89a, 89b
94. [x] FDW 26a: in place — [fdw](ENGINE-SPLIT-fdw.md) 26a · needs —
    (`6cb327eb1`, `c3a7cf7e1`, `1ba975174`, `0c991e6c8`, `d7405f486`, `a02ccecb1`, then the docs commit) · `fdw_` file prefix, `foreignRegistry`
    record, authority helpers over `access.View`, `foreignTransaction`
    participant methods, pure `foreignScanRequest`, `foreignWriteRuntime`;
    every 26b move-set declaration clean · selected unit and dual-mode proof
    and quickcheck passed; racecheck/finalcheck skipped at user request
95. [x] FDW 26b: `fdw` created, clean set moved — [fdw](ENGINE-SPLIT-fdw.md)
    26b · needs 94 (`6bce27045`, `f2df07054`, `fe35300c3`, `5e3147354`, then the docs commit) ·
    definition types, registry, options, identity set, write protocol,
    participants and DDL helpers in `fdw`; the driver imports `fdw`; no
    aliases · selected unit and dual-mode proof and quickcheck passed;
    racecheck/finalcheck skipped at user request
96. [x] FDW 26c: host refined (docs only) — [fdw](ENGINE-SPLIT-fdw.md) 26c ·
    needs 95, 61 (the docs commit) · `fdw.Host` = `ddl.Host` +
    `AddDomainBase`, `RemoveObject` (both already on `ddlHost`) and
    `StageForeignTable` (until 26e); `StageDDL`, `PlanImport`,
    `ImportSelected`, `ImportedTable`; GRANT a pure `PlanGrant` with no
    host; `grantWarning` to `authorization`; 26d move set and commit
    sequence written · docs only, no checks needed
97. [x] FDW 26d: wrapper, server, mapping, import and grant decisions —
    [fdw](ENGINE-SPLIT-fdw.md) 26d · needs 96 (`743eb4d47`, `6fe2aeaa2`,
    `e942c6231`, then the docs commit) · `fdw.Host` on `ddlHost`;
    `StageDDL`, `PlanImport`, `ImportSelected`, `ImportedTable`, `PlanGrant`
    in `fdw`; the root keeps `Engine.mu`, stage begin and discard, callbacks,
    the GRANT journal writes and publication; `GrantWarning` was moved by
    28b · selected unit and dual-mode proof and quickcheck passed;
    racecheck/finalcheck skipped at user request
98. [ ] FDW 26e: foreign-table decisions — [fdw](ENGINE-SPLIT-fdw.md) 26e ·
    needs 97, 89a, 89b
99. [x] View 27a: in place — [view](ENGINE-SPLIT-view.md) 27a · needs —
    (`87bfbb497`, `16260a489`, `a0254a5c3`, `57c781f25`, `7472271b6`,
    `a5e32e52a`, `512cf3e1c`, `24b0bfde4`, then the docs commit) · dead
    code and test-only staging helpers deleted; `drop_diagnostics.go`;
    `dropCascadeNames`; view-write lowering over the catalog owner and a
    `catalog.Read`; opaque `ruleAuthority`; `ruleRewriter`; access helpers
    over `access.View`; `rls` and `ruleAction` value groups; every 27b
    move-set declaration clean · selected unit and dual-mode proof and
    quickcheck passed; racecheck/finalcheck skipped at user request
100. [x] View 27b: `view` created, clean set moved —
     [view](ENGINE-SPLIT-view.md) 27b · needs 99, 69, 87 · **broad**
     (`03468b5b6`, `d7cd77e1a`, `b61bed3bb`, `73c98d60b`, then the docs
     commit) · 35 files (about 4.8k lines) in `view`: rule rewriting and
     programs, rule CTEs, view-write lowering and its binding slots,
     stored-projection demand, view options, policy and RLS helpers,
     concurrent refresh, refresh records and the snapshot codec; no
     aliases; ran broad (about 125 files) · selected unit and dual-mode
     proof and quickcheck passed; racecheck/finalcheck skipped at user
     request
101. [ ] View 27c: host refined (docs only) — [view](ENGINE-SPLIT-view.md)
     27c · needs 100, 61
102. [ ] View 27d: handler decisions moved — [view](ENGINE-SPLIT-view.md)
     27d · needs 101
103. [x] Role 28a: in place — [role](ENGINE-SPLIT-role.md) 28a · needs —
     (`23c4487dc`, `40d6de294`, `5f467d54d`, `32097b551`, `86c27112c`, `0ed6411b1`, then the docs commit) · `relation_diagnostics.go`,
     `setting_permission.go`, `database.go`; `access.View` cuts of
     `ownedSources`, `changeNonRelationGrant`, `grantRoutineArgumentDisplay`;
     `ownerStage`/`ownedCleanupStage`; `objectGrantRequest`/`objectGrantPlan`,
     `defaultPrivilegesPlan`, `planOwned`; every request and plan function
     clean · selected unit and dual-mode proof and quickcheck passed;
     racecheck/finalcheck skipped at user request
104. [x] Role 28b: `role` created, clean set moved —
     [role](ENGINE-SPLIT-role.md) 28b · needs 103, 65, 79
     (`a781528f1`, `eb6d750cc`, `0521e3b83`, `2492afb3f`, then the docs
     commit) · grant, default-privilege and owned plans, ownership and DROP
     OWNED staging over `role.OwnerStage`/`OwnedCleanupStage`, role
     diagnostics; `GrantWarning` to `authorization` (fdw 26d's first step);
     `PlanOwned` takes a describer for the root `dependencyObject`; no
     aliases · selected unit and dual-mode proof and quickcheck passed;
     racecheck/finalcheck skipped at user request
105. [x] Role 28c: host refined (docs only) — [role](ENGINE-SPLIT-role.md)
     28c · needs 104, 61 (the docs commit) · `role.Host` on a stage-less
     `ddlHost` (`ResolveRelation`, `ArgumentTypes`, `CanonicalParameter`,
     `GrantParameter`, `ReferencingFeatures`, `DescribeRoleDependency`) and
     `role.StageHost` (`ddl.Host` + `AddDomainBase`, `RemoveObject`,
     `OwnerStage`); GRANT targets, database grant, SET ROLE/SESSION
     AUTHORIZATION targets, DROP ROLE, ALTER OWNER checks and REASSIGN/DROP
     OWNED staging move; `fdw.OwnershipEligibility` to `access`,
     `resolveGrantTablespace` to `ddl.TablespaceRef`; 28d move set and
     commit sequence written · docs only, no checks needed
106. [x] Role 28d: handler decisions moved — [role](ENGINE-SPLIT-role.md)
     28d · needs 105 (`c320c6ec8`, `d0ad04e54`, `1ef3f87f9`, `0a0c9ef3e`, `6455845cc`, then the docs commit) · `role.Host` and
     `role.StageHost` on `ddlHost` (`role_host.go`); GRANT targets,
     `PlanDatabaseGrant`, `SetRoleTarget`, `SessionAuthorizationTarget`,
     `PlanDropRoles`, the ALTER OWNER plans with `OwnerChange`, `StageOwned`
     and `OwnedDependentsError` in `role`; `access.ForeignOwnershipEligibility`,
     `ddl.TablespaceRef`; the root keeps capture, lock windows and waits,
     the stage lifecycle, publication and dispatch; no aliases · selected
     unit, `RACE=1` session and dual-mode proof and quickcheck passed;
     racecheck/finalcheck skipped at user request

### Group K — DML and Dump/Restore (steps 29–30)

107. [x] DML 29a: in place — [dml](ENGINE-SPLIT-dml.md) 29a · needs 82, 86
     (`919c6319f`, `3bbec01e0`, `146d99d5b`, `03b5e1c78`, then the docs commit) ·
     `executeInsertLocked` in `insert.go`; write-set, rule, temporary-access,
     query-admission and row-event helpers in their owners' files; UPDATE,
     MERGE and DELETE executors are functions over the scope;
     `statementMutation` grouped into embedded value groups · selected unit
     and dual-mode proof and quickcheck passed; racecheck/finalcheck skipped
     at user request
108. [ ] DML 29b: `dml` created, clean set moved — [dml](ENGINE-SPLIT-dml.md)
     29b · needs 107, 59, 83, 87, 91, 95, 100
109. [ ] DML 29c: host refined (docs only) — [dml](ENGINE-SPLIT-dml.md) 29c ·
     needs 108, 85b, 89a, 89b, 93, 97, 102
110. [ ] DML 29d: executors moved — [dml](ENGINE-SPLIT-dml.md) 29d · needs 109
111. [x] Control 30a: Restore hook table, in place —
     [control](ENGINE-SPLIT-control.md) 30a · needs 57 (`dc839f171`,
     `192993651`, `fbcdc7fc9`, `22ef19659`, `6dc277b11`, `2eb9396e7`,
     `ba1fc1495`, `c22c9b9fb`, `80cc70d38`, `050e10e07`, `26ecb355d`, then
     the docs commit) · `restoreRun` run state and the ordered
     `restoreHooks` table (66 entries: decode, before, admit, prepare,
     after, finish); switch #3 is one admit entry per statement kind;
     `control.go` 731 → 225 lines · byte-identical dump smoke, selected
     unit, `RACE=1` Control and dual-mode proof and quickcheck passed;
     racecheck/finalcheck skipped at user request
112. [ ] Control 30b: host refined (docs only) —
     [control](ENGINE-SPLIT-control.md) 30b · needs 111, 70, 83, 87, 91, 95,
     100, 65, 80, 81
113. [ ] Control 30c: `control` created, drivers moved —
     [control](ENGINE-SPLIT-control.md) 30c · needs 112

### Group L — closing

114. [ ] End-state review (docs only): re-measure the root, update
     ENGINE-SPLIT.md §2 and §3, record what stays in the root and why, and
     decide whether a third round is needed —
     [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §6 · needs 63, 66, 73, 76, 77, 78,
     80, 81, 89c, 98, 106, 110, 113

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
   - a refine sub-step (11a, 12c, 13c, 14c–17c, and the docs-only `c`
     sub-steps of steps 20 and 23–29 and 30b) adds or splits the items it
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
