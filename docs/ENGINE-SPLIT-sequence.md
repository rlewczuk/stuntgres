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
the decisions of DDL, identity and serial columns and prepared literals)
become a feature package; the handlers' orchestration stays in the root
(17c). The generator
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
- `serial.go`: `closedRegclassDefault`, `closedSequenceDefault` (now
  `analyze.ClosedSequenceDefault`, 12d.3b), `serialTypeName`;
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

**17c refine (approved; measured at `1f606f52d`).** The `frontier` tool
(`.cswd/tmp/PG18-ENGINE-SPLIT-EXEC-REFINE/`; output in
`.cswd/tmp/PG18-ENGINE-SPLIT-SEQUENCE-REFINE/frontier-sequence.txt`), with
`sequence.go`, `sequence_comment.go`, `sequence_prepared.go`,
`sequence_stage.go`, `identity_alter.go`, `identity_drop_definition.go`,
`serial.go`, `identity_control.go`, `sequence_control.go` and the identity
staging of `identity.go` as the move set:

- **Size.** The remaining 16 non-test `sequence*`, `identity*` and `serial*`
  files hold about 2.2k lines (`serializable*` excluded).
- **The handlers are orchestration.** Every handler holds `Engine.mu` and
  drops it around a lock wait: CREATE and ALTER SEQUENCE inside
  `ownedSequenceLocked` (OWNED BY), ALTER, DROP, COMMENT and identity
  SET/TYPE/DROP around `StatementScope.acquireSequence`. After each wait
  they refresh the authorization view and recheck the catalog epoch.
- **Engine edges (OUT).** The set reaches 110 engine members (types,
  functions, methods and fields): `AuthorizationView` (`Check`,
  `CheckCreate`, `Roles`, `stagedTemporary`, `catalogAuthorityLocked`),
  `CatalogStage` (`Create`, `Replace`, `creationACL`, `authorizeLocked`,
  `dependencyPath`, `dropCascadeNames`), the journal install
  (`catalogChangeLocked`, `privateCatalogLocked`), `allocateOIDLocked` and
  `revisionLocked`, the scope fields `catalog`, `catalogAuthority`, `owner`,
  `op`, `ticket` and `replacementRead`, the drop provider (`dropProvider`,
  `preparedDrop`, `dropEffect` and their fields), `payloadOf` and
  `operationReplayOrZero`.
- **Outside uses (IN).** `acquireSequence` has 24 production callers in 20
  files (schemas, domains, enums, extensions, inheritance, ownership,
  DISCARD, CHECK, `pg_sequences`): it is the scope's object lock
  acquisition, not sequence code. `sequenceEnvironmentLocked` also serves
  ordinary prepared SQL (`prepared_rebind.go`) and regtype analysis
  (`regtype_analysis.go`): it is the shared effective namespace comparator.
  `relationDropOwner` is shared with `index_maintenance_locks.go`. Identity
  and serial staging is reached only from table DDL (`create_table_stage.go`,
  `partition_create_child.go`, `table_like.go`, `alter_table.go`, `alter_type.go`,
  `inheritance_alter_prepare.go`).
- **A whole-handler host does not pay.** Moving the handlers whole would
  need a `sequence.Host` of about 25 methods, several of which release and
  retake `Engine.mu` around lock waits, plus new interfaces for
  `AuthorizationView` and `CatalogStage`. DROP SEQUENCE and identity DROP
  would still stay in the root: they are the shared drop machinery.

Decisions:

1. **Decisions move, orchestration stays** (§3 rule 4, as in 13c).
   `sequence` gets pure functions over `catalog.Read`, catalog records and
   parser syntax: descriptor construction, option and OWNED BY checks, the
   plan-stamp rule, the prepared-literal validity decisions and the call
   semantics. The root keeps `Engine.mu`, lock waits, authorization refresh,
   OID and revision allocation, the journal and stage install, scope fields
   and notices.
2. **No `sequence.Host`.** Nothing that moves needs the scope or the engine.
   `statementHost` keeps implementing `functions.SequenceRuntime`. Functions
   that read or change a `SessionMemory` or a generator carry the `Locked`
   suffix: the caller holds `Engine.mu`.
3. **Imports.** 17d adds `functions` (`SequenceRequest`) and `session`
   (`DependencyObservation`, for the stage slot); both are lower layers.
   `sequence` still never imports `analyze`, `exec` or the engine.
4. **Call order is unchanged**, so PostgreSQL's error precedence is too.
   For example, identity options bind (`BindOptions`) before the TEMP
   `logged`/`unlogged` 42P16 check, and CREATE SEQUENCE without IF NOT
   EXISTS binds options before the namespace CREATE check.

**Move set (17d; new names).**

- **Relation construction** (`relation.go`): `Columns(read)` (was
  `Engine.sequenceColumnsLocked`; the caller passes the live catalog read);
  `NewRelation(spec)` replaces the three descriptor literals in CREATE
  SEQUENCE, `stageIdentitySequence` and `stageCreateSerials` (columns,
  index, attributes, high water 3, kind, options, `SequencePlanStamp` equal
  to the revision; the caller still sets the ACL);
  `InitialGenerator(options, restart, revision)`; and
  `ReplacementGenerator(state, revision, restart)`, shared by ALTER SEQUENCE
  and `stageIdentitySequenceOptionsLocked`.
- **OWNED BY, ALTER and DROP checks** (`owned.go`, `alter.go`):
  `ParseOwnedBy` (`none`, arity, 42601 with its hint) and `CheckOwnedBy`
  (after the lock grant: 42809 not a table, 55000 owner, 55000 schema, 42703
  column); `IdentityOwnershipError` (0A000); `TemporaryLoggedError` (42P16
  with schema and table); `CheckSchemaMove` (0A000 TEMP schemas);
  `NotSequenceError` (42809 with the DROP INDEX or DROP TABLE hint); and
  `AlterEffects(old, next, raw, action)`, which decides the plan-stamp bump
  (any option other than OWNED BY, or a name, schema, owner or persistence
  change), the generator replacement and the session tail discard.
- **Identity and serial options** (`identity.go`):
  `IdentityTypeOption(id)` (smallint/integer/bigint check and the AS option;
  identity creation and ALTER TYPE); `SplitIdentityOptions` (duplicate
  `logged`/`unlogged`/`name` errors at their positions, persistence, the
  explicit name) with a `CheckTemporary` step for the 42P16 at its position;
  `CheckIdentitySetOptions` (XX000 and 42601 of ALTER ... SET);
  `SerialOptions(id)` and `SerialDefault(namespace, name)` (the synthetic
  `pg_catalog.nextval` default); and `CheckRestoreIdentityConfiguration`
  (the whitelist part of `validateRestoreIdentityConfiguration`).
- **Prepared literals** (`prepared.go`): an `Observation` record (catalog
  domain, `catalog.Read`, session token, journal identity, effective path
  and a definition-stamp function) and `NewAnalysis` (from
  `sequenceAnalysisLocked`), `Analysis.Valid` (from
  `sequenceAnalysisValidLocked`), `PinReplacement` (from
  `pinSequenceReplacementLocked`) and `TemporaryEpochValid` (from
  `temporarySequenceEpochValid` in `temporary_provenance.go`). The root
  builds the observation in one helper.
- **Calls** (`call.go`): `CallPrivileges(name)` (USAGE with SELECT, UPDATE or
  only UPDATE), `ReadOnlyCallError(name)` (25006 for `nextval`/`setval`) and
  `CallLocked(request, relation, generator, memory)`, the
  currval/lastval/nextval/setval switch of `statementHost.Sequence`
  (`DemandLocked`, `ObserveLocked`, `NextLocked`, `RecordLastLocked`, the
  22003 setval bounds, `SetLocked`).
- **Stage slot** (`stage.go`): `sequenceStage` becomes `sequence.Stage` with
  exported methods (`Generator`, `StagedGenerators`, `SetGenerator`,
  `DeleteGenerator`, `ResetGenerators`, `IdentityOriginal`,
  `IdentityOriginalMap`, `CaptureIdentityOriginal`,
  `ObserveIdentityDependencies`, `IdentityDependencyList`); nil reads keep
  working. `CatalogStage.sequenceState()` stays in the root. About 25
  production and 27 test call sites change mechanically.
- **Control** (`control.go`): `CopyGenerators(view, generators)` (from
  `copySequenceState`; the root passes the source engine's generators under
  source synchronization) and `OwnTemporary(read, token)`, the retain
  predicate of `clearPersistentSequenceMemoryLocked`.

**Stays in the root.**

- The orchestration of `executeSequenceDDL`, `dropSequences`,
  `executeSequenceComment`, `ownedSequenceLocked`, `statementHost.Sequence`
  and `clearPersistentSequenceMemoryLocked`.
- `StatementScope.acquireSequence` (the scope's object lock; its name
  misleads), `AuthorizationView.checkSequenceAny` and `relationDropOwner`
  (shared with index maintenance).
- `sequenceEnvironmentLocked`, `journalOf` and `preparedDefinitionStamp`:
  the effective namespace comparator and the definition stamp of ordinary
  prepared SQL, which sequence literals reuse.
- Identity and serial staging on `CatalogStage` (`stageCreateIdentities`,
  `stageIdentitySequence`, `stageIdentitySequenceOptionsLocked`,
  `refreshIdentitySequenceLocked`, `identityCandidate`,
  `alterIdentityTypeSequence`, `stageCreateSerials`): table DDL, for the
  postponed `table` package.
- The identity DROP flow (`preparedIdentityDrop`, `identityDropCapture`,
  `dropIdentityDefinition`, `captureIdentityDropDefinitionLocked`,
  `applyIdentityDropTombstone`): it reads and writes drop-provider internals
  (`preparedDrop.observed`, `grants`, `discovery`, `effects`, `slots`).
- `sequence_dependencies.go` (owned-sequence drop provider),
  `truncate_sequence.go`, `pg_sequences.go`, `evalSerialSequence` (an exec
  inquiry that reads the scope), `storedSequenceEpochValid` (stored
  reference provenance) and `closedRegclassDefault` (default replay over
  plan nodes).

**Prepared-literal rules and their enforcers.** Each `AGENTS.md` clause, the
code that enforces it today, and where that code is after 17d. The root call
sites and their order do not change.

| Rule | Enforced by |
|---|---|
| Private description analysis cell | `StatementDescription.sequences` (`statement_description.go`), non-nil only when the statement has literals; built in the describe path (`engine.go`) and republished at Execute (`execute.go`); held by `analyze.Expected.Sequences`. 17d: the record comes from `sequence.NewAnalysis`; the cell stays in the root |
| Distinct relation plan stamp | `catalog.Relation.SequencePlanStamp`: set at creation (CREATE SEQUENCE, identity, serial; 17d: `NewRelation`), bumped by ALTER SEQUENCE (17d: `AlterEffects`), identity options, rename, ownership, ALTER SCHEMA, role grants and TRUNCATE RESTART IDENTITY; read by `preparedDefinitionStamp` (root), which adds the session memory's `SourceStamp` |
| Physical-session effective namespace cache | `SessionMemory.CachedPath`/`StorePathLocked` (`sequence/memory.go`), keyed by `PathKey` and filled by `sequenceEnvironmentLocked` (root, shared) |
| Whole-group reanalysis at Execute start | `execute.go`: the validity check (`sequenceAnalysisValidLocked`; 17d: `Analysis.Valid`) clears `candidate.sequences`, and the republish rebuilds the analysis from every `callerSequences` literal |
| Immutable temporal, signature and result contracts | `checkStatementSignature` before the republish in `execute.go` (root, `analyze.CheckSignature`) |
| RX on demand after once-only argument evaluation | `functions.SequenceCall.Eval` evaluates each argument once; `statementHost.Sequence` takes `RowExclusive` at call time (root) |
| Grant-time opening keeps the in-flight OID; no retarget or replay | `SequenceCall.Eval` reads the OID from the datum and never resolves a name; `statementHost.Sequence` reopens the same address after the grant (XX000 `could not open relation`) |
| Filtered, zero-row and strict NULL calls do not open | `SequenceCall.Eval` returns NULL before calling the runtime; nodes evaluate on demand |
| Generator state and descriptor revision are not invalidation tokens | `preparedDefinitionStamp` reads `SequencePlanStamp`, not `ObjectRevision`; `Analysis` holds no generator; `nextval` and `setval` change no stamp |
| Native replacement fenced except exact caller-owned live TEMP provenance | the epoch branch of `sequenceAnalysisValidLocked` (17d: `Analysis.Valid`), `pinSequenceReplacementLocked` (17d: `PinReplacement`), `temporarySequenceEpochValid` in `statementHost.Sequence` (17d: `TemporaryEpochValid`) and `storedSequenceEpochValid` for stored defaults (root) |

### 17d — Move the decisions

One session, in this order. No aliases are needed: every moved name is
qualified at its call sites.

1. **Seams first.** Write `sequence` unit tests for the new seams and see
   them fail: `Analysis.Valid` and `PinReplacement` (session, journal,
   namespace, path and stamp changes; replaced epoch with and without the
   caller's live TEMP), `CallLocked` (memory and generator effects of each
   function) and `NewRelation` (the descriptor shape).
2. **Extract in place.** Turn each decision of the move set into a root
   function with its final signature and adapt the callers, keeping the
   call order and every message, SQLSTATE and position. The three
   descriptor literals become one builder.
3. **Stage slot.** Move `sequenceStage` to `sequence.Stage` (`movex`,
   `meth2fn` for the exported methods) and adapt the call sites.
4. **Move.** Move the decision set with `movex`; check with `go list` that
   `sequence` imports only the packages of decision 3 and lower ones.
5. **Docs.** Update the README (what it owns, what stays, Testing). Move the
   `AGENTS.md` prepared-literal bullet into the README with the enforcer
   table, leaving at most one line. Update ENGINE-SPLIT.md and
   ENGINE-SPLIT-STATUS.md.

Checks: the README's engine selectors, `RACE=1` for concurrent `nextval`
and TEMP teardown, the reference selection in quick mode and once with
`REFERENCE=1` at the end, `make quickcheck`, `make deps` and `make bench`.

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
- Part B keeps the handlers' call order: lock waits, authorization refresh
  and error precedence are unchanged (17c decision 4).

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
`sequence`. Part B: the decisions of the sequence, identity and serial
handlers and of prepared sequence literals live there too; their
orchestration stays in the root (17c). The checks in §8.3 pass.

## Status

- [x] 17a — In place (`6ab03f25b`, `68d49b5a8`, `88c3aac0f`, `d43c3b1fb`,
  `48fe404db`, then the docs commit) *(racecheck/finalcheck skipped at user
  request)*
- [x] 17b — `sequence` created and clean set moved (`b9aacc67c`, `6a7aaae6a`,
  then the docs commit) *(racecheck/finalcheck skipped at user request)*
- [x] 17c — Host refined (docs only; the commit that records this text)
  *(measured at `1f606f52d`; decisions move, orchestration stays, no
  `sequence.Host`; quickcheck passed; racecheck/finalcheck skipped at user
  request)*
- [x] 17d — Handler decisions moved (`10d8611a9`, `f5f7d1354`, `4be715db5`,
  `f66434ed1`, `468bb3ade`, then the docs commit) *(racecheck/finalcheck
  skipped at user request)*

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

### 17d record

Re-measured at `204022e05`; rebased onto `909cd38fc` (routine 14d, extension
16d.1) before verification. The 17c move set still matched the code.

- **Method.** The decisions reference no engine code, so each one was written
  directly into `sequence` with its final name, seam tests first (observed
  failing), and its root callers adapted in the same commit; no root staging
  files or `movex` pass were needed. Five commits, each built and passed its
  selected tests: relations, generators and calls (`10d8611a9`); prepared
  literals (`f5f7d1354`); DDL, identity, serial and Restore checks
  (`4be715db5`); Control (`f66434ed1`); the stage slot (`468bb3ade`).
- **Moved** (new files `relation.go`, `call.go`, `owned.go`, `alter.go`,
  `identity.go`, `control.go`, `stage.go`; `prepared.go` extended):
  `Columns` (was `Engine.sequenceColumnsLocked`), `NewRelation`,
  `InitialGenerator`, `ReplacementGenerator`; `CallPrivileges`,
  `ReadOnlyCallError`, `CallLocked`; `ParseOwnedBy`, `CheckOwnedBy`,
  `IdentityOwnershipError`, `TemporaryLoggedError`, `CheckSchemaMove`,
  `NotSequenceError`, `AlterEffects`/`Effects`; `IdentityTypeOption`,
  `SplitIdentityOptions`/`IdentityOptions.CheckTemporary`,
  `CheckIdentitySetOptions`, `SerialOptions`, `SerialDefault`,
  `CheckRestoreIdentityConfiguration`; `Observation`, `NewAnalysis`,
  `Analysis.Valid`, `PinReplacement`, `TemporaryEpochValid`,
  `DefinitionStamp`; `CopyGenerators` (was `copySequenceState`),
  `OwnTemporary`; `Stage` (was `sequenceStage`) with exported methods.
  `sequence` now imports `functions` and `session` as planned (decision 3).
- **Deviations from the 17c text.**
  - `Observation` is a value record: the root computes the effective path
    and passes the session memory, instead of a definition-stamp function.
    `TestSequencePreparedAuthority` requires that validating an unchanged
    cell allocates nothing, and a closure over the authorization view would
    allocate. The plan-stamp rule therefore moved as `DefinitionStamp`
    (decision 1 lists it); `preparedDefinitionStamp` stays as the shared
    entry point of ordinary prepared SQL and calls it.
  - `PinReplacement` takes the catalog epoch and the session token, not an
    `Observation`: it needs no path, and building an observation computes
    one.
  - `sequenceAnalysisLocked` and `sequenceAnalysisValidLocked` stay as thin
    root wrappers: they build the observation (`sequenceObservationLocked`)
    only for statements with literals, so the path computation, its cache
    fill and its errors happen exactly where they did.
  - CREATE SEQUENCE now assigns `d.Sequence.Owned`: the old code relied on
    the descriptor's options aliasing a local; `NewRelation` copies them.
- **Stays in the root**, as 17c planned: the handler orchestration,
  `acquireSequence`, `checkSequenceAny`, `relationDropOwner`,
  `sequenceEnvironmentLocked`, `journalOf`, `preparedDefinitionStamp`,
  identity and serial staging, the identity DROP flow,
  `storedSequenceEpochValid` (now calling `sequence.TemporaryEpochValid`) and
  `closedRegclassDefault`.
- **Tests.** New in `sequence`: `relation_test.go`, `call_test.go`,
  `prepared_test.go`, `alter_test.go`, `control_test.go`, `stage_test.go`
  (selectors in the README). Adapted mechanically: `object_acl_test.go`,
  `temporary_table_{cleanup,catalog}_test.go` (`sequence.Columns`,
  `sequence.TemporaryEpochValid`), `delete_replacement_test.go`,
  `advisory_lifecycle_test.go`, `control_transactions_test.go`
  (`sequence.CopyGenerators`), the stage-slot call sites in eight tests, and
  `truncate_restart_publication_test.go` and
  `table_like_retained_admission_test.go`, which assigned the private
  generator map and now use `ResetGenerators` and `SetGenerator`. None moved
  or deleted.
- **Verification.** Build, vet (only the existing findings), layering
  (`go list`), the README's seam and engine selections, `RACE=1` on
  `^Test(Sequence|Serial|Identity|TruncateRestart|Discard|TemporaryTable)`,
  the README's reference selection in quick mode, `make quickcheck` and
  `make deps` passed. `REFERENCE=1` on
  `^TestReference(Sequence|Serial|Identity|SmokeSequence|SmokeIdentity|AlterIdentity|AlterTypeIdentity|TruncateRestart|CheckAlterOwnedSequence|DiscardSequences)`:
  everything passed except the PG-only
  `TestReferenceSerialDefaultProvider(PublicationHold|DropFirst)` (the
  oracle drift recorded at 17b). Startup bench, interleaved 5 runs each on a
  loaded host (load ~17–19): median 960 µs/op on master, 975 µs/op on the
  branch; 5,020–5,024 allocs/op and about 1.64 MB/op on both. `AGENTS.md`
  100 → 99 lines (the prepared-literal bullet moved into the README).

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
  that index naming shares (index 23d.1 moved it to `ddl` as
  `ddl.GeneratedRelationName`), so `ownedSequenceName` stays with it;
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

- Done in 17d: `sequence.Stage` and the prepared-literal decisions;
  `identityDropCapture` stays with the identity DROP flow.
- Later: `statementHost.Sequence` builds the same `lastval`/XX000 "could not
  open relation" error before and after the lock wait; a small
  `sequence.OpenError` could name it once (not in the 17c move set).
- Later: `StatementScope.acquireSequence` is the scope's object lock
  acquisition (24 callers, most not sequences); rename it when its owner is
  next touched. `sequenceEnvironmentLocked` misleads the same way.
- Later (postponed `table` package): identity and serial staging on
  `CatalogStage`, the identity DROP flow and `relationDiagnosticKind`
  (`sequence_dependencies.go`). `tableKind` already moved to `analyze`
  (`1a329c118`).
- Done (analyze 12f): `closedRegclassDefault` (used by `default_replay.go`)
  is now `analyze.ClosedRegclassDefault`, next to
  `analyze.ClosedSequenceDefault`.
- `catalogChanges.generators` is core journal state (13b keeps it), not a
  sequence slot.
- `journalOf` stays in the engine; `sequence.JournalIdentity` moved in 17b.
- Second round (planned at `c154e14fb`; no new sub-step, step 17 is
  complete). The identity and serial staging on `CatalogStage`
  (`stageCreateIdentities`, `stageIdentitySequence`, `identity_alter.go`,
  `stageCreateSerials`) and the identity DROP flow
  (`identity_drop_definition.go`) are table DDL and move with `table`.
  Table 24c refined this: CREATE identity and serial staging moves in 24d.1
  (item 89a) over `table.Host.SequenceStage`/`SequenceColumns`; ALTER
  option, refresh and type staging stays root behind
  `AlterHost.StageIdentitySequenceOptions`; the DROP IDENTITY decision
  moves in 24d.2 (89b), prepared identity drops stay root.
  `relationDiagnosticKind` is not table code: it becomes
  `catalog.RelationDiagnosticKind` (24d.1).
  Regclass output (`sequence_output.go`) is reg* code: `regobj` 21a renames
  it `regclass_output.go` and 21b moves it. The orchestration in
  `sequence.go` and `sequence_comment.go` stays in the root.
