# Engine split — status

This page tracks the `internal/engine` split. The plan is
[ENGINE-SPLIT.md](ENGINE-SPLIT.md); each item's scope is in the prompt file
linked from its line.

Items 1–180 are done: steps 1–30, the `access` steps, rounds 2–6 and four
closing reviews. Their record is in `git log` and in this file at `26f8fea15`
(`git show 26f8fea15:docs/ENGINE-SPLIT-STATUS.md`). Item 180, closing review 4,
measured the root, built the `closure` tool, planned the seventh round (items
181–205) and reset these docs.

Last verified against `master` at `8cddab66f` (item 183 rebased on it).

## Next ready items

Every item below has all its needs ticked. Items that share no needs may run
in parallel on separate branches.

- 184, 185, 186, 187, 188.

## Linear plan

One item per session. Start one with "Implement Item N from Linear Plan in
`docs/ENGINE-SPLIT-STATUS.md`". Mode and size (MB leaving the root) are from
ENGINE-SPLIT.md §4.

180. [x] Closing review 4 (docs only): re-measure, plan the seventh round —
     [ENGINE-SPLIT.md §4](ENGINE-SPLIT.md#item-180--closing-review-4) · needs
     the whole sixth round · (docs commit) · root 395 files and 48.5k lines
     (15.8% of the engine) plus 10.65 MB of tests; runtime core planned with
     `closure`; link and linear-plan checks and quickcheck passed;
     `REFERENCE=1`, bench, racecheck and finalcheck skipped (docs only, and
     racecheck/finalcheck at user request)
181. [x] Test fixture and pilot —
     [enginetest](ENGINE-SPLIT-enginetest.md#item-181--test-fixture-and-pilot) ·
     needs 180 · P · 0.3 MB (`89d097235`, `32fd10461`, `e54dc0604`, `8fa49b723`,
     `8067af598`, docs commit) · `enginetest` stage 1 (13 helpers over the
     exported API) added; of the 64 transitively free root test files, 16 pure
     units moved to `analyze`, `exec`, `view`, `plan` and `functions`, 39
     engine-fixture tests to 13 `<owner>/enginetests/` packages and `core/`, 2
     deleted as `reference/` duplicates and 7 helper files kept; T3 fallback
     fixed by measurement; root tests 1,343 files and 10.74 MB (−0.24 MB);
     quickcheck and deps passed; `REFERENCE=1` and bench skipped (no production
     change), racecheck and finalcheck skipped at user request
182. [x] Export pass in place —
     [core](ENGINE-SPLIT-core.md#item-182--export-pass-in-place) · needs 181 · S
     (`d8fd275b0`, `1d44144ab`, `c97bb4911`, docs commit) · 370 root names
     exported in place (176 fields, 194 declarations: the `closure` export set
     and 23 test-inspection names; two frame-matched methods kept unexported)
     and `enginetest` stage 2 (18 helpers) added; root production 1.75 MB (+1.9
     KB), tests 10.76 MB; quickcheck and the string-literal check passed; bench
     and `REFERENCE=1` skipped (renames only), racecheck and finalcheck skipped
     at user request
183. [x] Builtin function, operator and type tests —
     [enginetest](ENGINE-SPLIT-enginetest.md#item-183--builtin-function-operator-and-type-tests)
     · needs 182 · P · 1.25 MB (`fa8fdc8e7`, `6ad4ba40b`, `8b5c5a3c1`, docs
     commit) · 157 of the 174 transitively free root test files with
     builtin-function, operator and type prefixes (419 tests) moved to
     `functions/enginetests` (102), the new `internal/sqltype/enginetests` (51)
     and `typedef/enginetests` (4), no `reference/` duplicates deleted,
     `enginetest.SetObjectACL` added; root tests 1,211 files and 9.66 MB (−1.11
     MB); quickcheck passed; `REFERENCE=1` and bench skipped (no production
     change), racecheck and finalcheck skipped at user request
184. [ ] Query, DML, COPY, cursor and protocol tests —
     [enginetest](ENGINE-SPLIT-enginetest.md#item-184--query-dml-copy-cursor-and-protocol-tests)
     · needs 182 · P · 1.05 MB
185. [ ] Transaction, session, catalog and procedural tests —
     [enginetest](ENGINE-SPLIT-enginetest.md#item-185--transaction-session-catalog-and-procedural-tests)
     · needs 182 · P · 0.8 MB
186. [ ] Table, index, partition and sequence DDL tests —
     [enginetest](ENGINE-SPLIT-enginetest.md#item-186--table-index-partition-and-sequence-ddl-tests)
     · needs 182 · P · 0.95 MB
187. [ ] Type, view, role and schema-object DDL tests —
     [enginetest](ENGINE-SPLIT-enginetest.md#item-187--type-view-role-and-schema-object-ddl-tests)
     · needs 182 · P · 0.8 MB
188. [ ] Composition seams —
     [core](ENGINE-SPLIT-core.md#item-188--composition-seams) · needs 182 · P,
     bench
189. [ ] Core hooks — [core](ENGINE-SPLIT-core.md#item-189--core-hooks) ·
     needs 188 · P, bench
190. [ ] Host and slot splits —
     [core](ENGINE-SPLIT-core.md#item-190--host-and-slot-splits) · needs 189 ·
     P
191. [ ] Family methods to functions I —
     [core](ENGINE-SPLIT-core.md#item-191--family-methods-to-functions-i) ·
     needs 183, 184, 185, 186, 187, 190 · P
192. [ ] Family methods to functions II —
     [core](ENGINE-SPLIT-core.md#item-192--family-methods-to-functions-ii) ·
     needs 191 · P
193. [ ] Test accessors I: runtime areas —
     [enginetest](ENGINE-SPLIT-enginetest.md#item-193--test-accessors-i-runtime-areas)
     · needs 192 · P
194. [ ] Test accessors II: DDL areas and family tests —
     [enginetest](ENGINE-SPLIT-enginetest.md#item-194--test-accessors-ii-ddl-areas-and-family-tests)
     · needs 193 · P
195. [ ] Core move — [core](ENGINE-SPLIT-core.md#item-195--core-move) · needs
     194 · S, bench · 3.7 MB
196. [ ] `tablecmds`: tables —
     [cmds](ENGINE-SPLIT-cmds.md#item-196--tablecmds-tables) · needs 195 · S ·
     0.35 MB
197. [ ] `tablecmds`: sequences, identity, VACUUM —
     [cmds](ENGINE-SPLIT-cmds.md#item-197--tablecmds-sequences-identity-vacuum)
     · needs 196 · S
198. [ ] `indexcmds` — [cmds](ENGINE-SPLIT-cmds.md#item-198--indexcmds) ·
     needs 197 · S
199. [ ] `partitioncmds` —
     [cmds](ENGINE-SPLIT-cmds.md#item-199--partitioncmds) · needs 198 · S
200. [ ] `typecmds` — [cmds](ENGINE-SPLIT-cmds.md#item-200--typecmds) · needs
     199 · S
201. [ ] `viewcmds` — [cmds](ENGINE-SPLIT-cmds.md#item-201--viewcmds) · needs
     200 · S
202. [ ] `rolecmds` — [cmds](ENGINE-SPLIT-cmds.md#item-202--rolecmds) · needs
     201 · S
203. [ ] `schemacmds` — [cmds](ENGINE-SPLIT-cmds.md#item-203--schemacmds) ·
     needs 202 · S · 1.65 MB
204. [ ] Composition cleanup —
     [core](ENGINE-SPLIT-core.md#item-204--composition-cleanup) · needs 203 · S
205. [ ] Closing review 5 (docs only) —
     [ENGINE-SPLIT.md §4](ENGINE-SPLIT.md#item-205--closing-review-5) · needs
     204

## Updating this file

Every split session updates this page in the same commit that records its
item.
1. **Before starting**, check this page against `git log`. If they disagree,
   trust `git log`, fix the page and say so in the report.
2. **When an item is done** (ENGINE-SPLIT.md §5.3), tick it. Add its commit
   hashes in parentheses, with the docs commit marked `docs`, and one line:
   what moved, the root bytes after it, and which checks passed or were
   skipped.
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
