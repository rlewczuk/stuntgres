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

- [ ] 6a — `storageStore` state ownership
- [ ] 6b — `storage` created and types moved
- [ ] 6c — Aliases removed

Outstanding aliases: none yet.

Notes for later steps: none yet.
