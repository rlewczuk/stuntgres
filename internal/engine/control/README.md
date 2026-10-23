# internal/engine/control

The native Dump and Restore drivers and the catalog-replacement phases they
share with the engine. Dump turns a captured catalog into an ordered SQL script;
Restore replays such a script through an ordered hook table; features add
table entries, not switch cases. The event graph and order are in
[`dump`](../dump/README.md), per-object serializers in their feature packages,
the public contract in [docs/FIXTURES.md](../../../docs/FIXTURES.md).

## Imports and state

It imports `access`, `analyze`, `authorization`, `catalog`, `dump`, `exec`,
`expr`, `extension`, `fdw`, `ids`, `index`, `largeobject`, `partition`,
`pgcatalog`, `plan`, `regobj`, `routine`, `sequence`, `session`, `storage`,
`stored`, `table`, `typedef`, `view`, `visibility`, `parser`, `sqltype` and
`sqlstate`. Only the root engine imports it. It holds no mutex, no engine
pointer and no state between calls.

## Dump

The root's capture window (`captureDumpLocked`, under `Engine.mu`) calls
`CheckPortable` and `PreflightACLItems`, fills `Source` and calls
`Collect(ctx, source)`; nothing in `Source` is read after the unlock.
`Dump(ctx, capture, dst)` (`dump.go`) then sorts and completes the `Capture` in
place, builds the script and writes it; the root never reads the capture again.

- **Collect tables** (`collect*.go`): `dumpCollectors` is an ordered list of
  `{name, matches, collect}`. Temporary-namespace objects are skipped, the first
  matching entry collects a descriptor and an unmatched one fails with
  `unsupported catalog descriptor kind`; the relation entry dispatches through
  `dumpRelationCollectors` the same way. **Entry order is error precedence.**
  Feature packages own each entry's decision (`routine.OperatorDumped`,
  `stored.ProcedureProvider`, `stored.TypeProvider`).
- **Script** (`script.go`): `buildDumpScript` renders policies, rules,
  row-security and types, completes the event graph, decides default deferral
  and orders events with `dump.Order`.
- **Sections and emit table** (`sections.go`, `emit*.go`): `dumpSections` is
  the script in order; its `events` section writes each event through
  `dumpPhaseEmitters`, indexed by `dump.Phase`. The loop sets an entry's `owner`
  before `emit` runs (`nil`: the emitter sets it); an emitter that can fail
  renders before switching owner so an error writes nothing. `dumpWriter`
  tracks the `SET SESSION AUTHORIZATION` owner. **Adding a dumped object:**
  export the feature's dump function and name it at the matching table position.
- **Adapters** (`resolver.go`, `presenter.go`): `Resolver` (`dump.Resolver`)
  and `Presenter` (`dump.Presenter`) read only their arguments.
  Extension-member predecessors collapse to their actual owning extension;
  internally owned family rows first collapse to their class. Lifetime comes
  from the extension's target namespace: a missing namespace is invalid, while
  a valid TEMP installation gets the same atomic nonportable rejection as
  extension collection, independent of descriptor traversal order.
- **Large objects** (`largeobject.go`): a fixed-OID-sorted list outside the
  relation graph; owner and ACL formatting is preflighted before writer I/O.

### Ordering and replay rules

- Anything Dump cannot serialize exactly (explicit SQL/MED, SQL-routine or
  aggregate ACLs, default privileges, invalid indexes, non-canonical retained
  bootstrap ACLs) is rejected before any output, never silently reset.
- **Default deferral** breaks table → routine → table cycles as CREATE TABLE,
  CREATE FUNCTION, then ALTER TABLE SET DEFAULT. Neither deferral nor Restore
  evaluates a default.
- **Cast replay windows** (`cast.go`, `cast_replay.go`, `cast_consumer.go`):
  ordinary-SQL cast windows are derived only after default deferral and CHECK
  phases are settled, one per emitted consumer statement; the final conflicting
  cast row is installed once after all windows for its pair. Aggregate binary
  recipes, foreign defaults and generated-column placeholders
  (`table.PrepareGenerationPlaceholders`) use the same windows.
- **Raw FLOAT4 words** (noncanonical NaN only, `regobj.NeedsFloat4RawReplay`)
  replay through a binary `xid → real` cast; Restore has no raw-datum decoder.

## Restore

- **Driver** (`restore.go`): `Restore[R](ctx, host, hooks, run, src)` reads `src`
  through `contextReader`, parses it with the `Decode` entries, then calls
  `Host.Begin` and `Host.Start` (its `end` is deferred). Per statement:
  `Host.Statement`, the `Before`, `Admit` and `Prepare` entries,
  `Host.Execute`, the `After` entries. Then `Finish` and `Host.Publish`. The
  first error stops replay and skips `Publish`; `end` still runs.
- **Hook table** (`hooks.go`): `Hook[R]` has a `Name` and optional phase
  functions over the caller's run state `R`. The first `Admit` answer other
  than `NotHandled` (`Admitted`, or `Consumed`: replayed, nothing executes)
  wins; an unhandled statement fails with `0A000` and the allow-list text.
- **Admission checks** (`admit*.go`): run-independent allow-list entries
  (`AdmitKind`, `AdmitSchemaObject`, `AdmitOperator`, `AdmitStatement(check)`
  over `Check*` functions). The root's table names them in its own order, so
  rejection order is the table's.

### Host contract

`Host` is the engine's side of one Restore (root `restoreHost`):

- `Begin(source, statements)` records the script on the run. `Start(ctx)`
  starts the private stage's session as the bootstrap superuser and acquires
  its operation; `end` clears replay flags, releases the operation and ends the
  session, in that order. A failed `Start` cleans up and returns no `end`.
- `Statement(index)` resets per-statement state; `Execute(op, stmt)` runs one
  admitted statement on the stage, clears per-statement replay flags, closes
  its rows and detaches its notices.
- `Publish(ctx, op)` installs the stage catalog into the target engine.

Entries share state only through the run, so their order is part of the
contract; every entry runs on the stage, never on the target engine.

## Catalog replacement

The root's `preserveTemporaryLocked` holds `Engine.mu` for the whole replacement
and calls these on the private candidate, in order, before allocator and
publication work; error texts are the engine's:

1. `PersistentReplacementCatalog`, `TemporarySet`,
   `RetainTemporaryBuiltinTypes`, `CheckTemporaryDependencies`,
   `TemporaryCollisionMapping`, `RemapReplacementIdentities`
   (`persistent_replacement.go`, `temporary_*.go`, `replacement_remap*.go`).
2. `RemapViewRuleOIDs(view, allocate)` and `RemapDefinitionOIDs`
   (`view_rule_oids.go`, `definition_oids.go`): give colliding imported rules,
   checks, policies, constraints, defaults and generations fresh OIDs from the
   engine allocator, keeping identities claimed by retained TEMP objects, and
   repoint everything that referenced them.
3. `Check*SourcesLocked` and `RetainTemporaryViewProvidersLocked`
   (`temporary_sources.go`) validate retained TEMP objects against the
   published catalog.

## What stays in the engine

`Engine.mu`, the capture window (`dump_capture.go`), the OID allocator and
`preserveTemporaryLocked`; the restore stage (`restoreStage`) and atomic
publication (`replaceCatalog`) with their lock order and fences; `restoreRun`,
`restoreHost`, `restoreHooks` and every hook that reads the run; `Engine.Reset`
and the `Engine.Dump`/`Engine.Restore` wrappers (`control.go`).

## Testing

Package units drive the Restore driver and hooks with a fake `Host`; engine
units cover the collect, emit, remap and TEMP preservation callers. Public
Dump/Restore behavior is in `reference/` ([docs/TESTING.md](../../../docs/TESTING.md)).

Dump output must stay byte-identical unless a change intends otherwise. For
collect, emit, section or script changes, write a throwaway program under
`.cswd/tmp/<task>/` that runs a fixed corpus covering every object kind through
the root driver, calls `stuntgres.Dump`, restores into a fresh database and
dumps again; run it on a detached `master` worktree and on the branch and
compare byte for byte. Never commit the program.

- Driver and hooks: `RUN='^TestRestore(Driver|Hooks|AdmitsInsertTargetIndirection)'`
- FDW dump units: `RUN='^TestFDW(Dump|ReplacementRemap)'`
- Engine remap and TEMP preservation: `RUN='^Test(RuleNamedConstraintRemap|ViewRuleOIDUniquenessAfterRestore|PartitionConstraintsDefinitionRemapping|.*RetainedTemporary.*|.*TemporaryCollision)$'`
- Round trips: `RUN='^TestReference(Control|DumpRoundTrip)'`
- Cast replay windows: `RUN='^TestReferenceCastDefinition(ReplayPhases|ReplayConflicts|RawFloat4Persistence|ForeignDefaults)$'`
