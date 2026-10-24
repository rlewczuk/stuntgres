# Engine split — status

This page tracks the `internal/engine` split. The plan is
[ENGINE-SPLIT.md](ENGINE-SPLIT.md); each item's scope is in the prompt file
linked from its line.

Items 1–151 are done: steps 1–30, the `access` steps, rounds 2–4 and two
closing reviews. Their record is in `git log` and in this file at
`71747c25c` (`git show 71747c25c:docs/ENGINE-SPLIT-STATUS.md`). Item 151 (the
second closing review) measured the root at `fe0683b68`, planned the fifth
round (items 152–167) and reset these docs.

Last verified against `master` at `9e839cf02`.

## Next ready items

Every item below has all its needs ticked. Items that share no needs may run
in parallel on separate branches; none of them is broad.

- 159 (view), 160 (session, 2PC), 163 (exec, cursors), 165 (session,
  protocol codec) and 166 (ddl, CREATE DATABASE).

## Linear plan

One item per session. Start one with "Implement Item N from Linear Plan in
`docs/ENGINE-SPLIT-STATUS.md`". Sizes are approximate lines at `fe0683b68`.

151. [x] Closing review 2 (docs only): re-measure after the fourth round, plan
     the fifth round, reset the split docs — [ENGINE-SPLIT.md](ENGINE-SPLIT.md)
     §1 and §4 · needs 134–150 · (docs commit)
152. [x] Root cleanup: dead, test-only and duplicated code —
     [ENGINE-SPLIT.md §4](ENGINE-SPLIT.md#item-152--root-cleanup) · needs — ·
     −0.3k (`824bf2d84`, `cbdc57c47`, `029942506`, `142caed1a`, `b42acb633`,
     `1e796cdab`, then the docs commit) · root 390 → 383 files and 47.5k →
     47.2k lines, no allocation change · targeted units, quickcheck and the
     quick and `REFERENCE=1` role, COMMENT, capture and Restore selections
     passed; racecheck/finalcheck skipped at user request
153. [x] Catalog: candidate publication helpers —
     [catalog](ENGINE-SPLIT-catalog.md#item-153--candidate-publication-helpers)
     · needs — · 0.38k (`e65488cfd`, `37034b87f`, `9dd8665fb`, then the docs
     commit) · root 383 → 380 files and 47.3k → 46.9k lines · targeted
     units (also `RACE=1`), quickcheck and the quick and `REFERENCE=1`
     access-method, default-privilege, pg_depend, CREATE DATABASE,
     concurrent foreign DDL, extension matview and Restore selections
     passed; racecheck/finalcheck skipped at user request
154. [x] Table: DROP dependency decisions —
     [table](ENGINE-SPLIT-table.md#item-154--drop-dependency-decisions) ·
     needs 153 · 0.37k (`c0aea647f`, `b8912be99`, `ecd030b6e`, then the docs
     commit) · root 399 → 398 files and 50.5k → 50.1k lines · targeted units,
     quickcheck, `make deps`, the quick and `REFERENCE=1` pg_depend, typed
     table, DROP COLUMN, extension domain-check and CHECK sequence-drop
     selections, and `RACE=1` DROP COLUMN and sequence-drop selections
     passed; bench skipped (DDL-only path); racecheck/finalcheck skipped at
     user request
155. [x] Session: journal observation and state fences —
     [session](ENGINE-SPLIT-session.md#item-155--journal-observation-and-state-fences)
     · needs 153 · 0.21k (`3bc20a6ca`, `ea5eb4842`, then the docs commit) ·
     root 391 → 390 files and 48.9k → 48.7k lines · targeted units (also
     `RACE=1`), quickcheck and the quick and `REFERENCE=1` default-privilege,
     concurrent foreign DDL, ALTER TYPE repeatable-read, DROP SEQUENCE and
     serial cascade selections passed; racecheck/finalcheck skipped at user
     request
156. [x] Table: VACUUM decisions —
     [table](ENGINE-SPLIT-table.md#item-156--vacuum-decisions) · needs — ·
     0.47k (`990a57741`, `c37b29f2a`, `afda91bc2`, then the docs commit) · root
     VACUUM 894 → 425 lines, root 391 → 390 files and 48.9k → 48.4k lines · no
     startup allocation change · targeted units (also `RACE=1`), quickcheck and
     the quick and `REFERENCE=1` VACUUM and pg_database selections passed (an
     earlier quickcheck hit
     `TestReferenceUpdateCurrentOfVersionsAndArbitration`, which fails on
     master too); racecheck/finalcheck skipped at user request
157. [x] Partition: `pg_partition_*` values —
     [partition](ENGINE-SPLIT-partition.md#item-157--pg_partition_-values) ·
     needs — · 0.18k (`cc1e5d46c`, then the docs commit) · root
     `partition_info_runtime.go` 234 → 53 lines, root 399 files and 50.5k →
     50.3k lines · targeted units, quickcheck and the quick and `REFERENCE=1`
     `TestReferencePartitionInfo` selections passed; bench skipped (no startup
     or hot path); racecheck/finalcheck skipped at user request
158. [x] Table: ALTER TABLE pass helpers and the IF EXISTS no-op —
     [table](ENGINE-SPLIT-table.md#item-158--alter-table-pass-helpers-and-the-if-exists-no-op)
     · needs — · 0.16k moved, −0.12k (`6e5d68df3`, `87059b4e9`, `484e930e7`,
     then the docs commit) · `alter_table.go` 899 → 701 lines, root 398 → 400
     files and 49.9k → 49.7k lines · targeted units, quickcheck, `make deps`
     and the quick and `REFERENCE=1` ALTER nullability, ADD/DROP COLUMN,
     identity, TYPE CHECK, generated, typed, RENAME/SET SCHEMA/view IF EXISTS
     and ATTACH/DETACH diagnostics selections passed; bench skipped (DDL-only
     path); racecheck/finalcheck skipped at user request
159. [ ] View: RLS policy import and materialized snapshot rows —
     [view](ENGINE-SPLIT-view.md#item-159--rls-policy-import-and-materialized-snapshot-rows)
     · needs — · 0.22k
160. [ ] Session: owner transitions and the 2PC registry —
     [session](ENGINE-SPLIT-session.md#item-160--owner-transitions-and-the-2pc-registry)
     · needs — · 0.20k
161. [ ] Session: statement classification —
     [session](ENGINE-SPLIT-session.md#item-161--statement-classification) ·
     needs 160 · 0.17k
162. [ ] Session: statement data views —
     [session](ENGINE-SPLIT-session.md#item-162--statement-data-views) ·
     needs 161 · 0.17k
163. [ ] Exec: SQL cursor positioning —
     [exec](ENGINE-SPLIT-exec.md#item-163--sql-cursor-positioning) · needs — ·
     0.20k
164. [ ] Session: settings, DISCARD and setting calls —
     [session](ENGINE-SPLIT-session.md#item-164--settings-discard-and-setting-calls)
     · needs 161, 163 · 0.16k
165. [ ] Session: protocol value codec —
     [session](ENGINE-SPLIT-session.md#item-165--protocol-value-codec) ·
     needs — · 0.24k
166. [ ] DDL: CREATE DATABASE decisions —
     [ddl](ENGINE-SPLIT-ddl.md#item-166--create-database-decisions) · needs —
     · 0.16k
167. [ ] Closing review 3 (docs only): re-measure, then plan a sixth round or
     close the split — [ENGINE-SPLIT.md §4](ENGINE-SPLIT.md#item-167--closing-review-3)
     · needs 152, 153, 154, 155, 156, 157, 158, 159, 160, 161, 162, 163,
     164, 165, 166

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
