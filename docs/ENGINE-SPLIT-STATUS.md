# Engine split — status

This page tracks round 8 of the engine split, which splits
`internal/engine/core` into packages under `internal/engine/core/`. The plan is
[ENGINE-SPLIT.md](ENGINE-SPLIT.md); each item's scope is in the prompt file
linked from its line.

Items 1–204 are done: the earlier rounds and their closing reviews. Their record
is in `git log` and in this file at `7b262861a`
(`git show 7b262861a:docs/ENGINE-SPLIT-STATUS.md`). Item 205, closing review 5,
is superseded by this plan.

Last verified against `master` at `7731d0abf` (item 212 rebased on it).

## Next ready items

Every item below has all its needs ticked. Items that share no needs may run
in parallel on separate branches.

- 208, 209, 210, 211, 214, 215, 216.

## Linear plan

One item per session. Start one with "Implement Item N from Linear Plan in
`docs/ENGINE-SPLIT-STATUS.md`". Mode is from ENGINE-SPLIT.md §4; "core after"
is the §1 projection in MiB. Phase C (221–229) is gated by item 220.

206. [x] Tooling and owner census —
     [core](ENGINE-SPLIT-core.md#item-206--tooling-and-owner-census) · needs — ·
     P · core after 11.5 (docs commit) · tools retargeted to core in
     `.cswd/tmp/PG18-ENGINE-SPLIT-206-TOOLING-CENSUS/`; census of 2,849 core
     tests at `3320f21b6`. Rule 2 uses the plurality area (user decision).
     Core is 11.53 MiB; phase A moves 3.73 MiB and phase B 0.46. Rewritten:
     the §1 and §4 projection, and §3 (pgcatalog moves to L6.5; adds `xml` and
     `statistics`). Passed: `layers.py` 0, the item-212 `cmdmv` dry run with
     0 violations, `check_links`, `check_linear`. Quickcheck not needed (docs
     only); racecheck and finalcheck skipped at user request.
207. [x] Test seams in place —
     [core](ENGINE-SPLIT-core.md#item-207--test-seams-in-place) · needs 206 · P
     · core after 11.5 (`69025b2bb`, `c17408e51`, `378f1b83f`, `f6c26512b`, docs
     commit) · test files only: the statement and transaction-session accessor
     files are split into cursor, portal, prepared, scope, transaction,
     temporary, notification and session files; core and tablecmds mint test
     session IDs from `enginetest.NextSessionID()`; enginetest stage 3 adds
     `QueryFixture`, `PreparedDescription`, `PreparedExecute` and
     `ProtocolReader`, and core's session and transaction helper copies are
     gone. `readme-map.tsv` is in
     `.cswd/tmp/PG18-ENGINE-SPLIT-207-TEST-SEAMS-IN-PLACE/`. Core grows from
     11.61 to 11.64 MiB (the `enginetest.` qualifiers). Passed: core and
     tablecmds packages, the 28 session-ID files alone, `revcheck` (test names
     unchanged; one duplicate literal left with the deleted `transactionRows`
     copy), quickcheck. Racecheck and finalcheck skipped at user request.
208. [ ] Builtin function, type and extension tests —
     [core](ENGINE-SPLIT-core.md#item-208--builtin-function-type-and-extension-tests)
     · needs 207 · P · core after 10.9
209. [ ] DDL family tests —
     [core](ENGINE-SPLIT-core.md#item-209--ddl-family-tests) · needs 207 · P ·
     core after 9.6
210. [ ] Catalog producer tests —
     [core](ENGINE-SPLIT-core.md#item-210--catalog-producer-tests) · needs 207 ·
     P · core after 9.0
211. [ ] Query and DML feature tests —
     [core](ENGINE-SPLIT-core.md#item-211--query-and-dml-feature-tests) · needs
     207 · P · core after 7.8
212. [x] Explain pilot — [core](ENGINE-SPLIT-core.md#item-212--explain-pilot) ·
     needs 206 · P, bench · core after 7.7 (`11b535b5d`, `e87134e1a`,
     `9fa0e76ca`, `f60d36322`, `27aff0554`, docs commit) · the EXPLAIN renderer
     (11 files plus the new `explain_host.go`, 114 KiB) and its internal JSON
     presentation test move to the leaf `core/explain` (L14.5) over
     `explain.Host` (16 methods on `statementHost`) and `explain.Authority`
     (implemented by `AuthorizationView`); one recorded T6 exception,
     `Renderer.PresentedNodes` (user decision). Core 11.67 → 11.57 MiB. T7: an
     engine test inside `core/explain` costs about +34 s CPU and +5–16 s wall
     per build, so engine tests of leaves go to `core/<x>/enginetests`. Bench:
     startup and EXPLAIN allocations unchanged. Passed: quickcheck (two deadline
     load flakes passed alone), REFERENCE=1
     `^TestReference(Explain|PlannerCosts)`, `layers.py` 0, `revcheck.py`, deps.
     Racecheck and finalcheck skipped at user request.
213. [x] Host capability seam —
     [core](ENGINE-SPLIT-core.md#item-213--host-capability-seam) · needs — · P,
     bench · core after 7.7 (`66e8f81a5`, `b861d4501`, docs commit) ·
     `exec.Host` capability accessors `Contrib`, `TextSearch`, `CatalogInquiry`,
     `SessionInquiry`, `TransactionInquiry` (57 methods leave Host, 109 → 57;
     core implements each group on its own `StatementScope` conversion), and the
     `exec.NestedSQL` SPI capability on `statementHost` used by ts_rewrite,
     ts_stat, crosstab and connectby. In place; core 11.64 MiB (+5.5 KiB), exec
     +6.1 KiB. Bench: startup and inquiry allocations unchanged; +1 alloc per
     nested-SQL function call (the consumer). Passed: quickcheck (two load
     flakes passed alone), REFERENCE=1
     `^TestReference(Amcheck|Tablefunc|Connectby|FTS|Thesaurus|Size)`,
     `layers.py`, `frames.py check`, `lits.py`, deps. Racecheck and finalcheck
     skipped at user request.
214. [ ] Contrib runtimes —
     [core](ENGINE-SPLIT-core.md#item-214--contrib-runtimes) · needs 207, 212,
     213 · P, bench · core after 7.6
215. [ ] Text-search runtime —
     [core](ENGINE-SPLIT-core.md#item-215--text-search-runtime) · needs 207,
     212, 213 · P, bench · core after 7.6
216. [ ] Statistics runtime —
     [core](ENGINE-SPLIT-core.md#item-216--statistics-runtime) · needs 207, 212
     · P, bench · core after 7.55
217. [ ] Native triggers —
     [core](ENGINE-SPLIT-core.md#item-217--native-triggers) · needs 215 · P,
     bench · core after 7.5
218. [ ] Catalog runtime —
     [core](ENGINE-SPLIT-core.md#item-218--catalog-runtime) · needs 210, 212 ·
     P, bench · core after 7.4
219. [ ] Inquiry runtime —
     [core](ENGINE-SPLIT-core.md#item-219--inquiry-runtime) · needs 213, 218 ·
     P, bench · core after 7.35
220. [ ] Closing review 6 (checkpoint, docs only) —
     [ENGINE-SPLIT.md §4](ENGINE-SPLIT.md#item-220--closing-review-6) · needs
     207–219
221. [ ] PL/pgSQL above core —
     [core](ENGINE-SPLIT-core.md#item-221--plpgsql-above-core) · needs 220 · S,
     bench · core after 6.65
222. [ ] Cursor, prepare and COPY commands —
     [core](ENGINE-SPLIT-core.md#item-222--cursor-prepare-and-copy-commands) ·
     needs 220 · S, bench · core after 6.15
223. [ ] DML statement assembly —
     [core](ENGINE-SPLIT-core.md#item-223--dml-statement-assembly) · needs 220 ·
     S, bench · core after 5.95
224. [ ] Event triggers — [core](ENGINE-SPLIT-core.md#item-224--event-triggers)
     · needs 220 · P, bench · core after 5.9
225. [ ] Advisory locks and notifications —
     [core](ENGINE-SPLIT-core.md#item-225--advisory-locks-and-notifications) ·
     needs 220 · P, bench · core after 5.55
226. [ ] Settings commands —
     [core](ENGINE-SPLIT-core.md#item-226--settings-commands) · needs 220 · P,
     bench · core after 5.35
227. [ ] Foreign-data and replication commands —
     [core](ENGINE-SPLIT-core.md#item-227--foreign-data-and-replication-commands)
     · needs 220 · P · core after 5.25
228. [ ] Hub accessor retirement —
     [core](ENGINE-SPLIT-core.md#item-228--hub-accessor-retirement) · needs
     221–227 · P · core after 3.9
229. [ ] Core engine test packages —
     [core](ENGINE-SPLIT-core.md#item-229--core-engine-test-packages) · needs
     228 · S · core after ≈ 2
230. [ ] Closing review 7 —
     [ENGINE-SPLIT.md §4](ENGINE-SPLIT.md#item-230--closing-review-7) · needs
     229

## Updating this file

Every split session updates this page in the same commit that records its
item.
1. **Before starting**, check this page against `git log`. If they disagree,
   trust `git log`, fix the page and say so in the report.
2. **When an item is done** (ENGINE-SPLIT.md §5.3), tick it. Add its commit
   hashes in parentheses, with the docs commit marked `docs`, and one line:
   what moved, core's size and the sizes of the packages it created or grew,
   and which checks passed or were skipped.
3. **Leave an item unticked** if a check failed or was blocked, with a short
   note such as `*(blocked: REFERENCE=1 needs Docker)*`.
4. **Keep the plan strictly linear.** New items go at the end with the next free
   number, a needs list naming only earlier items, and a section in a prompt
   file. Never renumber, and never add lettered sub-items. If an item splits,
   the part left over becomes a new item at the end.
5. **Rewrite "Next ready items"**: every unticked item whose needs are all
   ticked.
6. **Update "Last verified"** with the `master` commit you checked against.

Keep entries short. Details and notes for later items go in the prompt file's
Status section.
