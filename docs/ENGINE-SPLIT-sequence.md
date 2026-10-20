# Engine split, step 17: `internal/engine/sequence`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-sequence.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §6 step 17; §4.1 (two parts,
  measured clean sets).
- **Needs:** Part A: the step 3 Tier C "sequence calls" part (it moves
  `sequenceCallExpression` and the `nextval`/`currval`/`setval`/`lastval`
  binding to `functions`). Part B: 13d. Read the `AGENTS.md` sequence
  bullets and the `storage` README (generator rules).
- **Effort:** high for 17a and 17b; xhigh for 17c (prepared literals and
  TEMP lifetime); high for 17d.
- **Session size:** one sub-step per session.

## Goal

Sequence objects (options, limits, session memories, dump formatting, then
DDL, identity and serial columns) become a feature package. The generator
stays in `storage`; the sequence functions go to `functions`. Files whose
names mislead are renamed first, so the feature's boundary is visible.

## Inventory (measured at `75fe0d710`; 17a corrections below)

Files matching `sequence*`, `serial*`, `identity*` (16 files, about 4.1k
lines; `serializable*` is not sequence code). Clean declarations:

- `sequence_options.go` (all clean, 214 lines): `bindSequenceOptions`,
  `reserveSequence`, `sequenceBoundOption`, `sequenceDefaults`,
  `sequenceDistance`, `sequenceMemory`, `sequenceOptionError`,
  `sequenceStep`, `sequenceTypeLimits`;
- `sequence_prepared.go`: `sameSequenceEnvironment`, `sequenceAnalysis`,
  `sequenceEnvironment`, `sequencePathCache`, `sequencePathKey` (records of
  the prepared-literal and physical-session caches);
- `sequence_control.go`: `arrayDomainDependency`, `dumpSequence`,
  `persistentReplacementCatalog`, `restoreSequenceState`,
  `retainedArrayDependencies`, `sequenceDumpStatements`;
- `sequence.go`: `sequenceKind`, `sequenceLinkedError`, `tableKind`;
- `sequence_expression.go`: `regclassNumeric`, `regclassQualifiedName`,
  `sequenceConstant`, `sequenceInputError` (the node itself goes with Tier
  C);
- `sequence_output.go`: `regclassIdentifier`, `regclassSpelling`,
  `regclassSpellingAt` (regclass presentation, not sequence code: leave for
  the presentation owner);
- `sequence_dependencies.go`: `relationDiagnosticKind`;
- `serial.go`: `closedRegclassDefault`, `closedSequenceDefault`,
  `serialTypeName`;
- `identity.go`: `generatedRelationName`, `identityInsertError`,
  `identitySequenceDropError`, `ownedSequenceName`; `identity_control.go`:
  `identityDumpDivergent`, `sequenceNumericSQL`;
  `identity_drop_definition.go`: `identityDropCapture`,
  `identitySequenceDefinition`.

**Files whose names mislead** (not sequence code):

| File | What it is | Suggested name |
|---|---|---|
| `sequence_temp.go` (400) | TEMP-session lifetime: `endTemporarySession`, `installTemporarySessionStageLocked`, `temporaryNamespace`, `creationNamespaceStagedLocked`, `temporarySessionIndexes` | `temporary_session.go` |
| `sequence_control.go`, `preserveTemporaryLocked` (537 lines of 801) and `retainTemporaryViewProvidersLocked` | TEMP preservation across Restore and replacement | `temporary_preservation.go` |
| `owned.go`, `owned_cleanup.go` | REASSIGN OWNED / DROP OWNED | `owned_roles.go`, `owned_roles_cleanup.go` |
| `identity_owner.go` | owner-handle helpers (`txOwnerOf`, `privateSessionOf`, ...) | `owner_handles.go` |

**Dirty, and why:** sequence DDL on `Engine` (`executeSequenceDDL` 292
lines, `dropSequences`, `ownedSequenceLocked`, `sequenceColumnsLocked`,
`executeSequenceComment`), `StatementScope.acquireSequence`, the identity
and serial staging (`stageCreateIdentities`, `stageIdentitySequence`,
`identity_alter.go`, `stageCreateSerials`, the identity drop definition
flow), the prepared-literal caches on `Engine` (`sequenceAnalysisLocked`,
`sequenceAnalysisValidLocked`, `sequenceEnvironmentLocked`,
`pinSequenceReplacementLocked`), `clearPersistentSequenceMemoryLocked`,
`copySequenceState`, and `evaluateIdentityValuesRow` (`EvalContext`,
`binding`).

## Part A

### 17a — In place

1. Confirm that the Tier C sequence part has landed. Re-measure.
2. Rename the misleading files above, moving only declarations that belong
   to the new name (one commit, file moves only; `git mv` plus splitting).
3. `Session.sequences map[ids.ObjectAddress]*sequenceMemory`,
   `Session.lastSequence` and `Session.sequencePath` are the physical-session
   memories. Keep them on `Session`, but make sure every read and write goes
   through a few named helpers, so 17b can move the record types without
   touching session logic.
4. If 13b has not done it, turn `CatalogStage.generators`,
   `identityOriginals` and `identityDependencies` into a sequence stage slot
   (pattern 2).

### 17b — Create `sequence` and move the clean set

1. Export in place, then move the clean set with the `movex` approach
   (options and limits, memory and prepared-cache records, dump and restore
   formatting, identity and serial naming helpers).
2. Check imports: `catalog`, `storage` (generator), `ids`, `parser`,
   `sqltype`, `sqlstate` and the standard library.
3. Remove the aliases in the same sub-step if they are few.

## Part B

### 17c — Refine the host (docs only), after 13d

Measure the DDL, identity, serial and comment handlers and the prepared
sequence-literal caches. Write the host interface and the move set. The
prepared-literal rules in `AGENTS.md` (private analysis cell, distinct plan
stamp, namespace cache, whole-group reanalysis, no retarget after admission)
must hold unchanged; list which handler enforces each. Wait for approval.

### 17d — Move the handlers

Follow 17c. Remove the remaining aliases.

## Invariants

- Generator state stays distinct from transactional sequence metadata; no
  per-CACHE arrays; no live catalog undo (`AGENTS.md`, `storage` README).
- TEMP sequence lifetime follows the physical session, not pooled
  borrowing.
- Sequence option validation, limits, error messages and SQLSTATEs are
  unchanged; `nextval` sequences and `currval`/`lastval` memories are
  unchanged.
- Dump output for sequences, identity and serial columns stays
  byte-identical.

## Targeted tests

- Engine units: grep `*_test.go` for `sequenceOptions`, `bindSequenceOptions`,
  `sequenceMemory`, `dumpSequence`, `identity`, `serial`. `RACE=1` for
  concurrent `nextval` and TEMP teardown.
- Reference selections (quick mode and `REFERENCE=1`; grep `reference/`):
  CREATE/ALTER/DROP SEQUENCE, identity columns, SERIAL, `nextval`,
  `currval`, `setval`, `lastval`, prepared sequence literals, TEMP
  sequences, `pg_sequence`/`pg_sequences`, and native Dump/Restore.

## Documentation

- Create `internal/engine/sequence/README.md` (17b): what it owns, the
  physical-session memory rules, what stays in `storage` and the engine.
- `AGENTS.md`: add `sequence` to the shared subpackage bullet. Move the
  sequence DDL and session-memory rules of the sequence bullet into the
  `sequence` README in 17b; move the prepared sequence-literal bullet in 17d,
  leaving at most one line each. The file must not grow.

## Done when

Part A: the misleading files are renamed and the clean set lives in
`sequence`. Part B: sequence, identity and serial handlers live there too.
The checks in §8.3 pass.

## Status

- [x] 17a — In place (`6ab03f25b`, `68d49b5a8`, `88c3aac0f`, `d43c3b1fb`,
  `48fe404db`, then the docs commit) *(racecheck/finalcheck skipped at user
  request)*
- [x] 17b — `sequence` created and clean set moved (`b9aacc67c`, `6a7aaae6a`,
  then the docs commit) *(racecheck/finalcheck skipped at user request)*
- [ ] 17c — Host refined (docs only; after 13d)
- [ ] 17d — Handlers moved

Outstanding aliases: none (17b qualified every reference; no type aliases).

### 17a record

Re-measured at `d27607c9c` (integrated on `e8598c73e`) with the reach tool (clean declarations in the
sequence, serial, identity, owned and renamed TEMP files: 49 before, 83
after).

- **Renamed** (file moves only): `sequence_temp.go` → `temporary_session.go`;
  `sequence_control.go` → `temporary_preservation.go`, with the sequence part
  split back into `sequence_control.go`; `owned.go` → `owned_roles.go`;
  `owned_cleanup.go` (and its test) → `owned_roles_cleanup.go`;
  `identity_owner.go` → `owner_handles.go`. `sequence_temp_*_test.go` keep their
  names: they test TEMP sequences.
- **Inventory corrections.** `persistentReplacementCatalog`,
  `arrayDomainDependency` and `retainedArrayDependencies` are TEMP-preservation
  helpers; they now live in `temporary_preservation.go` and leave the 17b
  clean set. `sequence_control.go` keeps `copySequenceState`,
  `clearPersistentSequenceMemoryLocked`, `dumpSequence`,
  `sequenceDumpStatements` and `restoreSequenceState`. `sequenceMemory` moved
  from `sequence_options.go` to `sequence_memory.go`.
- **Session memory.** `Session.sequences`, `lastSequence` and `sequencePath`
  are one value group, `Session.sequences sessionSequences`
  (`sequence_memory.go`). Every read and write goes through its helpers
  (`memory`, `demandLocked`, `last`, `recordLastLocked`, `forgetLocked`,
  `discardLocked`, `retainLocked`, `cachedPath`, `storePathLocked`) and the
  `sequenceMemory` methods (`observeLocked`, `current`, `nextLocked`,
  `setLocked`, `discardTailLocked`, `discardStaleTailLocked`,
  `markSourceEventLocked`, `sourceStamp`, `forgetValuesLocked`). Engine.mu
  guards both; the path cache is also serialized by physical-session
  admission. Both types are clean.
- **Prepared-literal records.** `sequenceAnalysis` and `sequencePathKey` had
  become dirty through a `*catalogChanges` field. They now hold the private
  journal as `journalIdentity` (an opaque `any`, built by `journalOf`, nil for
  no journal), compared only by identity. `sequenceAnalysis`,
  `sequencePathKey` and `sequencePathCache` are clean.
- **Stage slot.** `CatalogStage.generators`, `identityOriginals` and
  `identityDependencies` are now `CatalogStage.sequence *sequenceStage`
  (`sequence_stage.go`), allocated by `sequenceState()`; its read methods
  accept nil. No CatalogStage copy or reset code exists, so none changed.
  13b skips this slot.
- **Tests.** Engine tests were adapted mechanically to the helpers and the
  slot; none moved or were deleted.

### 17b record

Re-measured at `75fd0ec29` with the 14b frontier tool; rebased onto
`5ed1cfe22` (typedef 15b, plan 10b) before integration. Before the move the
set referenced only `sqlError` and `commentLiteral` in the engine.

- **`b9aacc67c` (prepare in place).** The clean declarations of the mixed
  files were split into whole files (`sequence_dump.go`,
  `sequence_diagnostics.go`, `sequence_analysis.go`). Seam test first:
  `TestSequenceSessionMemoryInspection` (observed failing), then read-only
  inspection accessors on the session memory (`Len`, `All`, `CachedPathKey`,
  `CachedTail`, `Generation`). Engine tests now use them and the memory
  helpers instead of private fields.
- **`6a7aaae6a` (move).** Five files moved into `internal/engine/sequence`
  (`options.go`, `memory.go`, `prepared.go`, `dump.go`, `diagnostics.go`,
  ~730 lines); the 6b `movex` tool qualified the engine references in 38
  files. Names lose their `sequence` prefix (`bindSequenceOptions` →
  `BindOptions`, `sessionSequences` → `SessionMemory`, `sequenceMemory` →
  `Memory`, `sequenceAnalysis` → `Analysis`, `sequenceConstant` → `Constant`,
  `dumpSequence` → `Dump`, `restoreSequenceState` → `RestoresState`,
  `sequenceKind` → `CheckKind`, `identitySequenceDropError` →
  `IdentityDropError`, ...). Record fields are exported; memory state stays
  private behind its methods. `sqlError` and `commentLiteral` got private
  copies in `errors.go`. Four engine locals named `sequence` that shadowed the
  package were renamed (`control.go` ×3, `identity_alter.go`). `sequence`
  imports `authorization` (`PathKey.Role`) and `expr` (`ErrorAt`) beyond the
  planned list; both are lower layers.
- **Inventory corrections (not moved).** `tableKind` and
  `relationDiagnosticKind` are generic relation diagnostics (17 and 11
  non-sequence callers); `generatedRelationName` is the relation-name chooser
  that index naming shares, so `ownedSequenceName` stays with it;
  `regclassNumeric`, `regclassQualifiedName` and `sequenceInputError` are
  regclass input, which stays with the regclass presentation owner like
  `sequence_output.go`; `closedRegclassDefault`/`closedSequenceDefault` name
  plan IR nodes (after step 10); `sequenceStage` needs
  `catalogDependencyObservation` and `identityDropCapture` is a drop-flow
  record (both 17d).
- **Tests.** Moved: `TestSequenceSessionMemoryInspection` (new) to
  `sequence/memory_test.go`. Adapted mechanically to the accessors:
  `discard_test.go`, `sequence_delete_atomic_fixture_test.go` (the memory-map
  identity check became count plus per-entry identity, since the container
  is private), `sequence_temp_creation_test.go`,
  `sequence_temp_move_test.go`, `sequence_prepared_test.go`,
  `ownership_database_path_test.go`, `ownership_sequence_fence_test.go`,
  `ownership_manual_utility_fence_test.go`, `alter_tablespace_prefix_test.go`
  and `truncate_restart_{locks,overlay,publication}_test.go` (helpers
  `currvalIs`, `currvalValid`, `currvalValue`, `cachedSequencePathIs` in
  `sequence_test.go`). None deleted.
- **Verification.** Build, vet (only the existing `composites`/`copylocks`
  findings), the targeted engine selection in the package README, layering
  (`go list`), `make deps` and `make quickcheck` passed. The 17a reference
  selection passed in quick mode. With `REFERENCE=1` every stuntgres subtest
  passed; the `/postgres` subtests of `TestReferenceAlterNullability(Names|ActionLists)`
  and `TestReferenceTemporaryTableDiagnostics` and the PG-only
  `TestReferenceSerialDefaultProvider(PublicationHold|DropFirst)` failed
  identically on a clean `75fd0ec29` snapshot (oracle drift, unrelated).
  Startup bench, interleaved 5 runs each on a loaded host (load ~18): median
  972 µs/op before, 898 µs/op after; 5,024–5,026 allocs/op and about 1.64 MB/op
  on both. Racecheck and finalcheck skipped at user request.

### Notes for later steps

- 17d: `sequenceStage` (`sequence_stage.go`) is clean but refers to
  `catalogDependencyObservation`; move it with the stage handlers, together
  with `identityDropCapture`.
- 17c/17d: the prepared-literal cache logic (`sequence_prepared.go`) can move
  once `AuthorizationView` and the journal have lower-layer forms; the
  records are already in `sequence`.
- Later: `tableKind` still lives in `sequence.go` and
  `relationDiagnosticKind` in `sequence_dependencies.go`; they belong to the
  postponed `table` package (or a relation-diagnostics file).
- `catalogChanges.generators` is core journal state (13b keeps it), not a
  sequence slot.
- `journalOf` stays in the engine; `sequence.JournalIdentity` moved in 17b.
