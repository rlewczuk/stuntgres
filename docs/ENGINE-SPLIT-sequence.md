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

## Inventory (measured at `75fe0d710`)

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

- [ ] 17a — In place (after Tier C sequence calls)
- [ ] 17b — `sequence` created and clean set moved
- [ ] 17c — Host refined (docs only; after 13d)
- [ ] 17d — Handlers moved

Outstanding aliases: none yet.
