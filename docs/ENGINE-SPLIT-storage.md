# Engine split, step 6: `internal/engine/storage`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-storage.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §5 step 6; the storage row of
  the layer table in §2.3; §2.4 for the state-ownership method.
- **Needs:** step 5 (`catalog`) through 5e. Read the lessons recorded in
  [`ENGINE-SPLIT-lock.md`](ENGINE-SPLIT-lock.md) and
  [`ENGINE-SPLIT-catalog.md`](ENGINE-SPLIT-catalog.md).
- **Effort:** high.
- **Session size:** one sub-step per session.

## Goal

The physical data layer becomes a package that imports only `catalog`, `expr`,
`ids`, `sqltype`, `parser`, `sqlstate` and the standard library.

**Moves.** At `a2252c0b1` these were:

- `rowVersion` (`transaction.go`) and `relationStorage` (`catalog.go`);
- deletion roots (`deletionRoot`; decide in refinement whether the
  transaction-private `privateDeletionRoot` belongs with write staging
  instead);
- logical B-tree index roots and entries (`logicalIndexRoot`,
  `logicalIndexEntry`), hash index roots, entries and bindings, and
  `indexRowOutput`;
- `sequenceGenerator` (`sequence_options.go`);
- truncate and materialized-view lineage (`truncateLineage`,
  `materializedLineage`).

`CommitSeq`, `RowID` and `VersionID` are already in `ids`.

**Stays in the engine.** Write staging (`WriteStage`), DML candidate building,
index maintenance orchestration, sequence DDL and transactional sequence
metadata, and per-session sequence memories (currval and lastval).

## Sub-steps

### 6a — State ownership in place

- Group `Engine.storage`, `sequences`, `indexRoots`, `nextRow` and
  `nextVersion` into one engine-local `storageStore`.
- `Engine` methods that only touch these fields become `storageStore`
  methods. Methods that combine storage with catalog, visibility or locks stay
  on `Engine` as orchestration.
- No mutex in the store. Mutating methods carry the `Locked` suffix or
  document that the caller holds `Engine.mu`.

### 6b — Create `storage` and move

- Move the types and the store as `storage.Store`. Export only what the
  engine's orchestration needs.
- Leave aliases in the engine.

### 6c — Remove the aliases

Remove them with a tool aware of `go/types`. Do not run this in parallel with
another broad rewrite (§8).

### 6d — Optional: confine store map writes

Added when the later waves were planned (measured at `75fe0d710`).
At that historical measurement, `storage.Store` exposed `Relations`,
`Sequences` and `IndexRoots` as exported maps with 17 direct engine assignment
sites. The 6d cutover routes live writes, including bootstrap and fixtures,
through storage-owned methods; the historical count is not an acceptance census.
These are minimal `Locked` methods (install one, replace all, remove one) that
document the caller-holds-`Engine.mu` contract. Reads remain direct.
Keep publication atomic: a whole-map swap stays one assignment under
`Engine.mu`.

## Invariants

- Published roots stay immutable and shared copy-on-write. Never copy row
  values to retain a snapshot (`AGENTS.md`, transaction isolation rule).
- Row and version ID allocation order is unchanged.
- Sequence generator state stays distinct from transactional sequence
  metadata: no live catalog undo, no per-CACHE arrays (`AGENTS.md`, sequence
  rule).
- Index root contents and ordering (B-tree and hash) are unchanged.

## Targeted tests

- Storage, index maintenance, sequence, TRUNCATE and materialized-view unit
  tests. Use `RACE=1` for concurrent DML and index builds.
- `make bench` after each sub-step.
- 6b: reference selections for DML, index scans and uniqueness, sequences,
  TRUNCATE, materialized views and native Dump/Restore, run with
  `REFERENCE=1` and in quick mode. Pick names by grepping `reference/`; never
  edit it.

## Documentation

- Create `internal/engine/storage/README.md`: what the package owns, the
  immutability and sharing rules, the caller-holds-`Engine.mu` contract, what
  stays in the engine, and `## Testing` selectors moved from
  `internal/engine/README.md`.
- Design notes that named storage types at `a2252c0b1`:
  `PG18-ARCH-TX-VISIBILITY.md`, `PG18-ARCH-TYPE-SYSTEM.md`,
  `PG18-ARCH-EXPRESSION-ANALYSIS.md`, `PG18-ARCH-LOCKING.md`,
  `PG18-ARCH-SERIALIZABILITY.md`, and the `PG18-INDEX-*.md` notes. Also
  `docs/TABLES.md` where it names internals. Grep for more.
- `AGENTS.md`: add `storage` to the shared subpackage bullet. The sequence
  bullet mixes generator rules with DDL and session rules. Move the
  generator-only rule into the `storage` README, and leave the rest in
  `AGENTS.md` unless it is now redundant.

## Done when

`storage` imports only `catalog`, `expr`, `ids`, `sqltype`, `parser`,
`sqlstate` and the standard library. `Engine` holds one `storage.Store` field
instead of five. The checks in §8.3 pass with no startup regression. 6c
completes the step.

## Status

- [x] 6a — `storageStore` state ownership (`79c8a6f3a`, `d6bbc8a3e`, then
  the commit that records this status) *(racecheck skipped at user request;
  targeted RACE=1 runs passed)*
- [x] 6b — `storage` created and types moved (`c001ffa28`, `af2501215`,
  then the commit that records this status) *(racecheck skipped at user
  request; targeted RACE=1 runs passed)*
- [x] 6c — Aliases removed (`518fd7141`, `4c9a2a229`, `33c1b6f43`,
  `a57f5648e`, then the commit that records this status) *(racecheck
  skipped at user request)*
- [x] 6d — Store map writes confined to methods *(full racecheck and finalcheck
  waived at user request; selected race checks and quickcheck passed)*

Outstanding aliases: none. 6b kept 14 unexported type aliases in
`internal/engine/storage_aliases.go`; 6c removed them all.

6d implementation results:

- **Write authority.** `storage/state_write.go` defines nine minimal caller-locked
  install/remove/replace methods for relations, generators and index roots.
  Install/delete/whole-map assignment retain ordinary Go semantics, supplied
  pointers, exact map containers and nil shape, without allocation or copying.
- **Callers.** Catalog and transaction publication, same-owner/foreign replacement,
  generated repair, materialized import and TEMP publication use the methods at
  their existing publication positions. Detached candidate maps remain engine-owned.
  Bootstrap still allocates only the relation and sequence maps; index roots start
  nil. Engine callers, including bootstrap and existing fixture injections, use
  the existing `Engine.mu`; standalone storage unit fixtures have exclusive ownership.
- **Published aliases.** Generated repair installs the supplied containers in a
  real isolated stage. HASH redistribution therefore installs through that
  stage's Store, not a supposedly detached map alias. Repair returns the installed
  owner for subsequent TEMP merges; the no-install path keeps detached writes.
  Distinct private-stage installs take its mutex without provider callbacks;
  same-owner installs use the target mutex already held by the caller.
- **Behavioral scope.** No SQL, assertion, test name or reference scenario is
  changed and no forwarding test is added. Store remains by value; allocator,
  preflight, root sharing, sequence memory and publication responsibilities stay
  where they were.
- **Verification.** Compiler-backed inventory covered 125 configured production/test
  packages with no type/load errors; manual closure distinguishes detached
  candidates and post-install aliases. Build, module-only dependencies, selected
  native/real PostgreSQL 18.6 witnesses, selected race checks and public lifecycle
  smoke passed. Quickcheck passed after an earlier unchanged timeout witness
  failed with statement timeout instead of transaction timeout; no timeout
  assertions or behavior were changed. Full racecheck expired at 900 seconds;
  the user waived further full racecheck and finalcheck, relying on quickcheck.
- **Startup.** Baseline five runs: 972,840–1,124,460 ns/op and 5,152–5,159 allocs/op.
  Latest five runs: 842,507–899,102 ns/op and 5,152–5,158 allocs/op, all below
  1 ms with the same steady 5,152 allocations. Earlier post-change samples
  3,410,981–4,400,749 ns/op missed the target; variable runtime measurements
  do not establish a refactor speedup.

6a results:

- **Store.** `storageStore` (`internal/engine/storage_store.go`) holds
  `relations` (was `Engine.storage`), `sequences`, `indexRoots`, `nextRow`
  and `nextVersion`. `Engine` holds it by value as `storage` and never
  replaces it whole; publication, Restore, foreign replacement and TEMP
  installation swap its maps under `Engine.mu`. `newEngine` allocates the
  same two maps as before (`indexRoots` starts nil).
- **Rewrite.** A go/types selector rewriter changed all 2,008 uses (1,149
  `storage`, 316 `sequences`, 387 `indexRoots`, 80 `nextRow`, 76
  `nextVersion`) in 340 production and in-package test files. The
  per-field census matched before and after; one composite-literal key
  (`newEngine`) was edited by hand.
- **Methods moved.** `allocateRowVersionsLocked` (unchanged body) and
  `indexRootsCurrent`, which replaces `Engine.domainIndexRootsCurrent` and
  takes the stage's captured roots.
- **Methods kept on `Engine`.** `privateStorageLocked`,
  `sequenceGeneratorLocked` and `domainStageIndexRootLocked` first consult
  the transaction catalog journal or the session; about 36 others
  (publication, Restore, Dump, replacement, TEMP, index builds, ALTER)
  combine storage with catalog, visibility or locks and are orchestration.
- **Kept inline.** Row and version counter bumps in
  `exclusion_admission.go`, `insert_returning.go`, `update_versions.go`,
  `index_replacement.go` and `catalog.go` interleave with exclusion and
  replacement logic and have distinct exhaustion messages. Only their
  selectors changed, so allocation order and messages are unchanged.
- **Tests.** Adapted mechanically by the rewrite. None moved or deleted.
- **Verification.** `make check` storage selections (index, hash, sequence,
  TRUNCATE, materialized view, exclusion, UPDATE core, INSERT, Restore,
  Dump, TEMP, domain, commit time, CREATE) passed after each commit, and a
  `RACE=1` run of concurrent index build, UPDATE core, exclusion, INSERT
  conflict, TRUNCATE, materialized refresh and sequence tests passed.
  `quickcheck` passed. The full `racecheck` was skipped at the user's
  request. `make bench`: 1.52–1.58 ms/op and 7,768 allocations on `master`,
  1.51–1.56 ms/op and 7,768 allocations after (the <1 ms target is already
  missed on `master`). `make deps` lists only module paths. Imports are
  unchanged.

6b results:

- **Decisions (user).** `Store` exposes its maps as exported fields
  (`Relations`, `Sequences`, `IndexRoots`); only the row and version
  counters are private. The transaction-private deletion overlay
  (`privateDeletionRoot`, `deleteCandidate`), `liveVersion` and the
  journal-consulting overlays (`privateStorageLocked`,
  `sequenceGeneratorLocked`, `domainStageIndexRootLocked`) stay in the
  engine with write staging; `liveVersion` is a step-7 candidate.
- **Export in place** (`c001ffa28`). A type-checked rename capitalized
  every field of the moved types and the store maps: 6,522 edits in 432
  production and test files, with the per-field use census (6,625 uses)
  identical before and after. Methods: `AllocateRowVersionsLocked`,
  `IndexRootsCurrent`, `CaptureRowEntries`; `indexStorageIdentity(s)` and
  `sameTruncateLineage(a, b)` became `s.IndexIdentity()` and the nil-safe
  `a.SameTruncateLineage(b)`, so no `storage.X` reference lands in an
  engine scope. `indexRowOutput.matches` became the engine function
  `indexOutputMatches`, because it needs `sameIndexKeySemantics`; that was
  the move set's only reference back into the engine.
- **Counters.** `RowFrontier`, `VersionFrontier` (reads),
  `AllocateRowLocked`, `AllocateVersionLocked` (one step; the caller keeps
  its own exhaustion check and message) and `SetFrontierLocked` (Restore
  and foreign replacement). The inline sites in `exclusion_admission.go`
  (version before row), `insert_returning.go`, `update_versions.go`,
  `index_replacement.go` and `catalog.go` keep their order and diagnostics;
  about 25 test sites that set or compared the counters were rewritten.
- **Lineage tokens.** `marker` stays private. The engine allocates
  `&truncateLineage{}`; identity comes from the nonzero size, and the old
  `marker: 1` value was never read.
- **Move** (`af2501215`). 14 types and 10 methods moved into
  `internal/engine/storage` (`store.go`, `relation.go`, `lineage.go`,
  `index.go`, `hash.go`, `sequence.go`, about 230 lines) with the catalog
  step's `movex` tool, parametrized by package name. `storage` imports only
  `catalog`, `expr`, `ids`, `sqltype` and the standard library. `Engine`
  holds one `storage storageStore` (= `storage.Store`) field.
- **Tests.** New seam tests in `storage/store_test.go`:
  `TestStoreAllocateRowVersionsInOrder`,
  `TestStoreAllocateRowVersionsExhaustion`,
  `TestStoreSingleAllocatorsAdvanceIndependently`,
  `TestStoreIndexRootsCurrent`, `TestRelationIndexIdentity`,
  `TestRelationSameTruncateLineage`. Engine tests adapted mechanically by
  the rename and the counter rewrite; none moved or deleted.
  `internal/engine/README.md` had no storage selectors to move.
- **Verification.** `make build` and targeted `make check` storage
  selections (index, hash, sequence, TRUNCATE, materialized view,
  exclusion, UPDATE core, INSERT, Restore, Dump, TEMP, domain, commit time,
  unique, serial, partial, view) passed after each commit, and a `RACE=1`
  run of concurrent index build, UPDATE core, exclusion, INSERT conflict,
  TRUNCATE, materialized refresh, sequence and the new storage tests
  passed. `quickcheck` passed. `REFERENCE=1` selections passed:
  `^TestReference(Truncate|HashIndex|Exclusion|ConcurrentIndex|BtreeIndexMaintenance|DeleteCore|UpdateCore)`,
  `^TestReference(Unique|InsertConflict|Sequence)` and every
  `Materialized*`, `*Dump*` and `*Restore*` reference test (19 packages).
  The full `racecheck` was skipped at the user's request. `make bench`
  (interleaved, on a loaded machine): 7,768 allocations and about
  2.07 MB/op on both; best 1.87 ms/op on `master` and 1.79 ms/op on the
  branch, over the <1 ms target that `master` already misses. `make deps`
  lists only module paths. `AGENTS.md` stays at 100 lines.

6c results:

- **Scope.** Measured on `af324f738` with go/types: 1,172 alias uses in
  242 engine files, the largest `rowVersion` (512), `relationStorage`
  (270), `logicalIndexRoot` (95), `logicalIndexEntry` (83) and
  `sequenceGenerator` (82). No use outside package `engine` (the aliases
  were unexported). One embedded alias field, `updateCandidate.rowVersion`
  (`update.go`), became `storage.RowVersion`; its selectors follow.
- **Shadowing** (`518fd7141`). 33 of the 153 locals named `storage` would
  have captured an inserted `storage.X`. They became `relations`
  (`map[ids.ObjectAddress]*storage.Relation`) or `stored`
  (`*storage.Relation`): 142 edits in 24 files.
- **Rewrite.** Index types (`IndexRoot`, `IndexEntry`, `IndexOutput`,
  `HashRoot`, `HashEntry`, `HashBinding`; `4c9a2a229`, 305 edits in 62
  files), then the rest and deletion of `storage_aliases.go` (`33c1b6f43`,
  1,113 edits in 226 files). Imports were added to 242 files; two comments
  that named `rowVersion` were fixed by hand.
- **Readability** (`a57f5648e`). Locals named `storage` in any function that
  also names the package (for example `storage *storage.Relation`
  parameters, legal because parameter scope is the body) were renamed the
  same way: 69 edits in 20 files. No local named `storage` now shares a
  function with the package; two struct fields named `storage` remain.
- **Tool.** Scratchpad `storagex`, the 5f `aliasx` tool parametrized for
  `storage` (modes `count`, `locals`, `shadow`, `apply`, and a new `tidy`).
  Each write reloads the package with its tests, requires zero type errors
  and an exact per-object use census before and after, mapped through the
  edits. gofmt moved `"testing"` after the inserted import in eight test
  files.
- **Tests.** Adapted mechanically by the same tool; none moved, deleted or
  rewritten by hand.
- **Docs.** Design notes already name `storage.Relation` where they mean
  current code; plan-time records keep the old names. The `storage` README
  and `AGENTS.md` are unchanged (100 lines).
- **Verification.** `make build` and targeted `make check` storage
  selections (index, hash, unique, exclusion, sequence, TRUNCATE,
  materialized view, UPDATE, INSERT, DELETE, Restore, Dump, TEMP, domain,
  serial, partial, view, commit time, CREATE, foreign key, generated,
  remap, partition, replacement, `TestStore`, `TestRelation`) after each
  commit; `quickcheck` passed on the rebased branch. `racecheck` skipped at
  the user's request. No `REFERENCE=1` selection is required for a pure
  rename. `make bench` (interleaved, three rounds each, load average about
  22): 7,768 allocations and about 2.07 MB/op on both; best 2.76 ms on
  `master` and 2.29 ms on the branch in the last round, with times falling
  as the load dropped. The <1 ms target is already missed on `master`.
  `make deps` lists only module paths. `storage` still imports only
  `catalog`, `expr`, `ids`, `sqltype` and the standard library.
- **Unrelated failure.** One `quickcheck` run failed
  `TestReferenceServerCopyCancelInput/server/invalid_record` (57014 instead
  of 22P02: the cancel won the race with the bad record). It passed 10 of 10
  in isolation on both `master` and the branch, and the next `quickcheck`
  passed; it is a timing race in server COPY cancellation, not this change.

Notes for later steps:

- 96 engine locals (31 production) are still named `storage`, none in a
  function that names the package. New code that names the package inside
  their scope must rename the local first. The field `Engine.storage` is
  unaffected.
- Store maps remain exported for reads. 6d routes live mutations through
  storage-owned methods; detached candidate construction stays in the engine.
