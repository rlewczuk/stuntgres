# Engine split — status

This page tracks the `internal/engine` split. The plan is
[ENGINE-SPLIT.md](ENGINE-SPLIT.md). Each item's scope is in the prompt file
linked from its line.

Items 1–167 are done:
- steps 1–30 and the `access` steps;
- rounds 2–5;
- three closing reviews.

Their record is in `git log` and in this file at `ea8c445fe`
(`git show ea8c445fe:docs/ENGINE-SPLIT-STATUS.md`). Item 167, the third
closing review, did these things:
- it measured the root at `ea8c445fe`;
- it planned the sixth round, items 168–180;
- it reset these docs.

Last verified against `master` at `861442188` (item 174 rebased on it; no
feature commit added root production code since item 173's check).

## Next ready items

Every item below has all its needs ticked. Items that share no needs may run
in parallel on separate branches. None of them is broad.

- 175 and 176.

## Linear plan

One item per session. Start one with "Implement Item N from Linear Plan in
`docs/ENGINE-SPLIT-STATUS.md`". Sizes are approximate lines at `ea8c445fe`.

167. [x] Closing review 3 (docs only): re-measure after the fifth round,
     plan the sixth round, reset the split docs —
     [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §1 and §4 · needs the whole fifth round · (docs
     commit) · root 402 files and 48.9k lines (16.5% of the engine);
     link and linear-plan checks and quickcheck passed;
     `REFERENCE=1`, bench, racecheck and finalcheck skipped (docs only, and
     racecheck/finalcheck at user request)
168. [x] Root cleanup and clean-now moves —
     [ENGINE-SPLIT.md §4](ENGINE-SPLIT.md#item-168--cleanup-and-clean-now-moves)
     · needs — · 0.2k (`ae0d9246e`, `abb1bb4ce`, `35ed552c8`, `a88e1c321`,
     `82477bd05`, docs commit) · clean helpers moved to `catalog`, `fdw`,
     `routine` and `session`, eight capture/identity methods freed,
     `access.WithObjectACL` moved into the root fixture; quickcheck, quick and
     `REFERENCE=1` selections and bench (allocations unchanged) passed;
     racecheck and finalcheck skipped (user decision)
169. [x] Proc: PL/pgSQL diagnostics, error phases and flow —
     [proc](ENGINE-SPLIT-proc.md#item-169--plpgsql-diagnostics-error-phases-and-flow)
     · needs — · 0.29k (`df6ca810d`, `6e0834bde`, `6b0a2f092`,
     `4b9cf5736`, docs commit) · diagnostic formatter, phase errors, flow,
     cursor name and trigger frame moved to `proc`, two assignment helpers
     to `plan`; quickcheck, quick and `REFERENCE=1`
     selections and deps passed; bench skipped (error paths and a value
     type only); racecheck and finalcheck skipped (user decision)
170. [x] Proc: PL/pgSQL statement walk —
     [proc](ENGINE-SPLIT-proc.md#item-170--plpgsql-statement-walk) · needs
     169 · 0.45k (`2a1ad8f5b`, `83e97e898`, `e10a85e40`, `7509d27ef`,
     `18db099f5`, docs commit) · the walk moved to `proc.PLRun` behind the
     11-method `proc.PLHost`, adapted by the root `plHost`; quickcheck, quick
     and `REFERENCE=1` selections, `RACE=1` exception and cancellation units,
     bench (+1 allocation per invocation only) and deps passed; racecheck and
     finalcheck skipped (user decision)
171. [x] View: trigger DDL decisions —
     [view](ENGINE-SPLIT-view.md#item-171--trigger-ddl-decisions) · needs —
     · 0.25k (`72e54064b`, `8f6aeb486`, docs commit) · CREATE, ALTER and DROP
     TRIGGER decisions moved to `view.PlanTrigger`, `view.Host` gained
     `SelectFunction` and `BindTriggerWhen`; quickcheck, quick and
     `REFERENCE=1` selections and deps passed; bench skipped (DDL decision
     path only); racecheck and finalcheck skipped (user decision)
172. [x] View: trigger partition synchronization —
     [view](ENGINE-SPLIT-view.md#item-172--trigger-partition-synchronization)
     · needs 171 · 0.18k (`08398dbea`, `578a40386`, docs commit) · partition
     trigger clone synchronization moved to `view.SynchronizeTriggersLocked`,
     the catalog store became its pointer-shaped `ddl.Allocator`; quickcheck,
     quick and `REFERENCE=1` selections, bench (allocations unchanged) and
     deps passed; racecheck and finalcheck skipped (user decision)
173. [x] Control: Restore comparison context —
     [control](ENGINE-SPLIT-control.md#item-173--restore-comparison-context)
     · needs — · 0.29k (`92e17acb0`, `b7a0c629e`, docs commit) · the
     comparison-context state machine moved to `control.ComparisonRestore`
     and the bound-definition check to `control.ValidateComparisonBinding`;
     quickcheck, quick and `REFERENCE=1` selections and deps passed; bench
     skipped (Restore-only path); racecheck and finalcheck skipped (user
     decision)
174. [x] Exec: row owners —
     [exec](ENGINE-SPLIT-exec.md#item-174--row-owners) · needs — · 0.17k
     (`62c06ce5a`, `e72db642f`, `9d71907ee`, docs commit) · the nested
     producer, `exec.CTEOwnedRows` and the `ts_rewrite` rule batches and
     diagnostic mapping moved to `exec`; quickcheck, quick and `REFERENCE=1`
     selections, `RACE=1` cancellation units, bench (allocations unchanged)
     and deps passed; racecheck and finalcheck skipped (user decision)
175. [ ] Table: prepared DROP log —
     [table](ENGINE-SPLIT-table.md#item-175--prepared-drop-log) · needs — ·
     0.37k
176. [ ] Index, storage, session: catalog publication sub-steps —
     [catalog](ENGINE-SPLIT-catalog.md#item-176--catalog-publication-sub-steps)
     · needs — · 0.3k
177. [ ] Catalog: `catalog.Store` —
     [catalog](ENGINE-SPLIT-catalog.md#item-177--catalogstore) · needs 168,
     169, 170, 171, 172, 173, 174, 175, 176 · 0.29k
178. [ ] Catalog: builtin catalog seed —
     [catalog](ENGINE-SPLIT-catalog.md#item-178--builtin-catalog-seed) ·
     needs 177 · 0.19k
179. [ ] Access: `access.RoleStore` —
     [catalog](ENGINE-SPLIT-catalog.md#item-179--accessrolestore) · needs
     177 · 0.19k
180. [ ] Closing review 4 (docs only): re-measure, then plan a seventh round
     or close the split —
     [ENGINE-SPLIT.md §4](ENGINE-SPLIT.md#item-180--closing-review-4) · needs
     168, 169, 170, 171, 172, 173, 174, 175, 176, 177, 178, 179

## Updating this file

Every split session updates this page, in the same commit that records its
item.

1. **Before starting**, check this page against `git log`. If they disagree,
   trust `git log`, fix the page and say so in the report.
2. **When an item is done** (verified under ENGINE-SPLIT.md §5.3), tick it,
   add its commit hashes in parentheses with the docs commit marked
   `docs`, and add one line: what moved and which checks passed or were
   skipped.
3. **Leave an item unticked** if one of its checks failed or was blocked,
   and add a short note such as `*(blocked: REFERENCE=1 needs Docker)*`.
4. **Keep the plan strictly linear.** New items go at the end with the next
   free number, a needs list naming only earlier items, and a section in a
   prompt file. Never renumber, and never add lettered sub-items. If an item
   splits, the part left over becomes a new item at the end, and the
   original records what it did.
5. **Rewrite "Next ready items"**: every unticked item whose needs are all
   ticked.
6. **Update the "Last verified" line** with the `master` commit you checked
   against.

Keep entries short. Details, deviations and notes for later items go in the
prompt file's Status section, not here.
