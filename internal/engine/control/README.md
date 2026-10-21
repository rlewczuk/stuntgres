# internal/engine/control

The native Dump and Restore drivers. Dump turns a captured catalog into an
ordered SQL script; Restore replays such a script through an ordered hook
table. Before step 30 both lived in the root's `control.go`, which named
the internals of almost every feature. Now a feature adds a table entry
instead of editing a central switch.

The package imports `authorization`, `catalog`, `dump`, `expr`, `ids`,
`index`, `partition`, `regobj`, `routine`, `sequence`, `session`, `stored`,
`table`, `typedef`, `view`, `parser`, `sqltype` and `sqlstate`. Only the
root `engine` package imports it. It holds no mutex, no engine pointer and
no state between calls. Step 30 of the split created it; see
[ENGINE-SPLIT-control.md](../../../docs/ENGINE-SPLIT-control.md).

## What it owns

### Dump

- **Capture record** (`dump.go`): `Capture` is everything Dump reads from
  the engine (the catalog and role reads, the constraint replay, the ACL
  output, the collected schemas, types, routines, tables, views, indexes
  and the event `Graph`); `Schema`, `OwnedStatement` and `SchemaComment`
  are its records.
- **Driver** (`dump.go`): `Dump(ctx, capture, dst)` sets the ACL output
  context, builds the script and writes the sections, then returns
  `ctx.Err()`.
- **Script** (`script.go`): `buildDumpScript` renders the policy, rule and
  row-security groups and the type statements, completes the event graph,
  decides default deferral and orders the events with `dump.Order`.
- **Sections and Emit table** (`sections.go`, `emit.go`,
  `emit_declaration.go`): `dumpSections` is the whole script in order; its
  `events` section writes each ordered event through `dumpPhaseEmitters`,
  indexed by `dump.Phase`. Each entry has an `owner` the event loop sets
  before the entry runs (`nil`: the emitter sets it, as `Declaration` and
  `DomainChecks` do; domain checks render before the owner switch so an
  error writes nothing) and an `emit`; an empty entry (`TableCompletion`)
  writes nothing. `dumpWriter` writes text and tracks the `SET SESSION
  AUTHORIZATION` owner. A feature exports its dump function and the table
  names it in the same position.
- **Adapters** (`resolver.go`, `presenter.go`): `Resolver` is the
  `dump.Resolver` (`Predecessor` maps a dependency to the event that must
  precede its consumer) and `ColumnPredecessors` adds a relation's row and
  column type predecessors; `Presenter` is the `dump.Presenter`. Both read
  only their arguments.

### Restore

- **Driver** (`restore.go`): `Restore[R](ctx, host, hooks, run, src)`
  reads `src` through `contextReader` (it stops once `ctx` is done), parses
  it with the `Decode` entries reading line comments, then calls
  `Host.Begin` and `Host.Start` (its `end` is deferred). Per statement it
  calls `Host.Statement`, walks `Before`, `Admit` and `Prepare`, calls
  `Host.Execute` and walks `After`. Then it walks `Finish` and calls
  `Host.Publish`. The first error stops the replay and skips `Publish`;
  `end` still runs.
- **Hook table types** (`hooks.go`): `Hook[R]` has `Name` and the phase
  functions `Decode`, `Before`, `Admit`, `Prepare`, `After` and `Finish`,
  each over the caller's run state `R`; any may be nil. `Admission` is an
  admit entry's answer: `NotHandled`, `Admitted` or `Consumed` (replayed,
  nothing executes). A statement no entry handles is rejected with
  SQLSTATE `0A000` and the allow-list text. `Outcome{Created}` is what an
  executed statement tells the `After` entries.

## The host contract

`Host` is the engine's side of one Restore; the root `restoreHost`
implements it:

- `Begin(source, statements)` records the script on the run and pairs the
  domain replay records with their INSERTs.
- `Start(ctx)` starts the private restore stage's session as the bootstrap
  superuser and acquires its operation. `end` clears the operation's replay
  flags, releases the operation and ends the session, in that order. If
  `Start` fails it cleans up itself and returns no `end`.
- `Statement(index)` resets the per-statement state.
- `Execute(op, stmt)` runs one admitted statement on the stage, clears the
  per-statement replay flags, closes its rows and detaches its notices.
- `Publish(ctx, op)` installs the stage catalog into the target engine.

## What stays in the engine

- `Engine.mu` and the Dump capture: `captureDumpLocked`, the Collect tables
  (`dumpCollectors`, `dumpRelationCollectors`) and `dumpCollection`, which
  embeds the `*Capture` beside the generation recipe plan and the locked
  `dumpSource`. The root hands the capture over: `Dump` sorts and completes
  it in place, and the root never reads it after the call.
- The restore stage (`restoreStage` in `principal_remap.go`) and the atomic
  publication (`replaceCatalog`): its lock order, the fence after unlock,
  the sequence-memory and serializable fences and the `foreignNamespaces`
  replacement in the same publication.
- `restoreRun`, `restoreHost` and the hook table `restoreHooks`
  (`[]control.Hook[*restoreRun]`, composed at package initialization), the
  admit adapters (`restoreAdmitKind`, `restoreAdmitChecked`,
  `restoreAdmitReplayed`), every entry and hook body, and the operation
  replay slot (`restore_replay.go`). Entries share state only through
  `restoreRun`, so their order is part of the contract; every entry runs
  on the stage, never on the target engine.
- `Engine.Reset`, and the thin `Engine.Dump` (nil-writer and context
  checks, the `Engine.mu` window) and `Engine.Restore` (nil-reader and
  context checks, the stage) wrappers in `control.go`.

## Testing

Package units: the driver order with a fake `Host`, and the walkers:

```sh
make check RUN='^TestRestore(Driver|Hooks).*'
```

Engine witnesses: the INSERT admit entry, the presenter's domain
expressions, and the resolver users:

```sh
make check RUN='^Test(RestoreAdmitsInsertTargetIndirection|DumpPresenterDomainExpressions|IndexPropertyDump.*|IdentityDumpProviderMapping|IdentityControlsOwnerCollisionPredecessors|PartitionAttachDumpPhaseDependencies|PartitionDetach(Replay|UniqueReplay).*|DomainAlterConstraintReplayEligibility)$'
```

Public behavior is covered by the Dump, Restore and Control reference tests
(quick mode, then `REFERENCE=1`). Dump output must stay byte-identical:

```sh
make check RUN='^TestReference.*(Dump|Restore|Native|RoundTrip|Replay)|^TestReferenceControl'
make check RACE=1 RUN='^TestReferenceControl|^TestControlTransactions|^TestRestore(Driver|Hooks)'
```

Changes to the Emit table, the sections or the script also need a dump,
restore and re-dump comparison against a `master` baseline (see 18b and
18c in [ENGINE-SPLIT-dump.md](../../../docs/ENGINE-SPLIT-dump.md)).
