# Engine split — status

This page tracks round 8 of the engine split, which splits
`internal/engine/core` into packages under `internal/engine/core/`. The plan is
[ENGINE-SPLIT.md](ENGINE-SPLIT.md); each item's scope is in the prompt file
linked from its line.

Items 1–204 are done: the earlier rounds and their closing reviews. Their record
is in `git log` and in this file at `7b262861a`
(`git show 7b262861a:docs/ENGINE-SPLIT-STATUS.md`). Item 205, closing review 5,
is superseded by this plan.

Last verified against `master` at `b7e30241f` (planning).

## Next ready items

Every item below has all its needs ticked. Items that share no needs may run
in parallel on separate branches.

- 206, 213.

## Linear plan

One item per session. Start one with "Implement Item N from Linear Plan in
`docs/ENGINE-SPLIT-STATUS.md`". Mode is from ENGINE-SPLIT.md §4; "core after"
is the §1 projection in MiB. Phase C (221–229) is gated by item 220.

206. [ ] Tooling and owner census —
     [core](ENGINE-SPLIT-core.md#item-206--tooling-and-owner-census) · needs — ·
     P · core after 11.2
207. [ ] Test seams in place —
     [core](ENGINE-SPLIT-core.md#item-207--test-seams-in-place) · needs 206 · P
     · core after 11.2
208. [ ] Builtin function, type and extension tests —
     [core](ENGINE-SPLIT-core.md#item-208--builtin-function-type-and-extension-tests)
     · needs 207 · P · core after 10.4
209. [ ] DDL family tests —
     [core](ENGINE-SPLIT-core.md#item-209--ddl-family-tests) · needs 207 · P ·
     core after 9.9
210. [ ] Catalog producer tests —
     [core](ENGINE-SPLIT-core.md#item-210--catalog-producer-tests) · needs 207 ·
     P · core after 9.4
211. [ ] Query and DML feature tests —
     [core](ENGINE-SPLIT-core.md#item-211--query-and-dml-feature-tests) · needs
     207 · P · core after 8.9
212. [ ] Explain pilot — [core](ENGINE-SPLIT-core.md#item-212--explain-pilot) ·
     needs 206 · P, bench · core after 8.75
213. [ ] Host capability seam —
     [core](ENGINE-SPLIT-core.md#item-213--host-capability-seam) · needs — · P,
     bench · core after 8.75
214. [ ] Contrib runtimes —
     [core](ENGINE-SPLIT-core.md#item-214--contrib-runtimes) · needs 207, 212,
     213 · P, bench · core after 8.55
215. [ ] Text-search runtime —
     [core](ENGINE-SPLIT-core.md#item-215--text-search-runtime) · needs 207,
     212, 213 · P, bench · core after 8.4
216. [ ] Statistics runtime —
     [core](ENGINE-SPLIT-core.md#item-216--statistics-runtime) · needs 207, 212
     · P, bench · core after 8.2
217. [ ] Native triggers —
     [core](ENGINE-SPLIT-core.md#item-217--native-triggers) · needs 215 · P,
     bench · core after 8.15
218. [ ] Catalog runtime —
     [core](ENGINE-SPLIT-core.md#item-218--catalog-runtime) · needs 210, 212 ·
     P, bench · core after 7.8
219. [ ] Inquiry runtime —
     [core](ENGINE-SPLIT-core.md#item-219--inquiry-runtime) · needs 213, 218 ·
     P, bench · core after 7.7
220. [ ] Closing review 6 (checkpoint, docs only) —
     [ENGINE-SPLIT.md §4](ENGINE-SPLIT.md#item-220--closing-review-6) · needs
     207–219
221. [ ] PL/pgSQL above core —
     [core](ENGINE-SPLIT-core.md#item-221--plpgsql-above-core) · needs 220 · S,
     bench · core after 7.1
222. [ ] Cursor, prepare and COPY commands —
     [core](ENGINE-SPLIT-core.md#item-222--cursor-prepare-and-copy-commands) ·
     needs 220 · S, bench · core after 6.6
223. [ ] DML statement assembly —
     [core](ENGINE-SPLIT-core.md#item-223--dml-statement-assembly) · needs 220 ·
     S, bench · core after 5.9
224. [ ] Event triggers — [core](ENGINE-SPLIT-core.md#item-224--event-triggers)
     · needs 220 · P, bench · core after 5.75
225. [ ] Advisory locks and notifications —
     [core](ENGINE-SPLIT-core.md#item-225--advisory-locks-and-notifications) ·
     needs 220 · P, bench · core after 5.5
226. [ ] Settings commands —
     [core](ENGINE-SPLIT-core.md#item-226--settings-commands) · needs 220 · P,
     bench · core after 5.35
227. [ ] Foreign-data and replication commands —
     [core](ENGINE-SPLIT-core.md#item-227--foreign-data-and-replication-commands)
     · needs 220 · P · core after 5.15
228. [ ] Hub accessor retirement —
     [core](ENGINE-SPLIT-core.md#item-228--hub-accessor-retirement) · needs
     221–227 · P · core after 4.7
229. [ ] Core engine test packages —
     [core](ENGINE-SPLIT-core.md#item-229--core-engine-test-packages) · needs
     228 · S · core after ≈ 3.8
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
