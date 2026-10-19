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
- [ ] 6b — `storage` created and types moved
- [ ] 6c — Aliases removed

Outstanding aliases: none yet.

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

Notes for later steps:

- 6b: the engine bumps `nextRow`/`nextVersion` directly at the inline
  sites above, and Restore and foreign replacement read and assign both
  counters as a pair (`prepareRemintedStorage`, `installForeignCatalogLocked`).
  `storage.Store` needs exported accessors or allocation methods for them
  that keep each site's order and error text.
- 6b: about 61 production and 93 test locals or parameters are named
  `storage`. Rename those that would capture a `storage.X` reference
  before naming the package inside their scope (as 5f did for `catalog`).
- 6b: `privateStorageLocked` and `sequenceGeneratorLocked` overlay
  transaction-private storage on the store; decide with
  `privateDeletionRoot` whether that overlay belongs with write staging.
