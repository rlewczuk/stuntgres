# internal/engine/control

The native Dump and Restore drivers. Dump turns a captured catalog into an
ordered SQL script; Restore replays such a script through an ordered hook
table. Before step 30 both lived in the root's `control.go`, which named
the internals of almost every feature. Now a feature adds a table entry
instead of editing a central switch.

The package imports `access`, `analyze`, `authorization`, `catalog`, `dump`, `expr`,
`extension`, `fdw`, `ids`, `index`, `partition`, `pgcatalog`, `plan`,
`regobj`, `routine`, `sequence`, `session`, `storage`, `stored`, `table`,
`typedef`, `view`, `visibility`, `parser`, `sqltype` and `sqlstate`. Only the
root `engine` package imports it. It holds no mutex, no engine pointer and
no state between calls. Step 30 of the split created it; see
[ENGINE-SPLIT-control.md](../../../docs/ENGINE-SPLIT-control.md).

## What it owns

### Dump

- **Collect** (`collect.go`, `collect_relation.go`): `Collect(ctx,
  source)` builds the builtin-schema preamble, then hands every descriptor
  of `source.State` to the Collect tables and returns the filled
  `Capture`. `Source` is what the tables read: the capture's catalog and
  role reads, the constraint replay, the ACL output, the generation recipe
  plan, and the locked engine state (catalog state, relation roots,
  sequence generators, commit frontier, bootstrap principal, foreign
  registry). The root fills it under `Engine.mu` and calls `Collect`
  inside that window, with the ACL output context on `ctx`; nothing in
  `Source` is read after the unlock. `dumpCollectors` is an ordered list
  of `{name, matches(Descriptor), collect(ctx, *dumpCollection, address,
  Descriptor)}` entries: the first entry whose `matches` accepts a
  descriptor collects it, a descriptor no entry accepts is `unsupported
  catalog descriptor kind`, and objects in temporary namespaces are
  skipped before matching. The relation entry runs the checks shared by
  every relation, then `dumpRelationCollectors` (composite, index, view,
  sequence, table) the same way. Order is error precedence: keep it when
  adding an entry. A feature package owns an entry's decision once its
  dump code moves (`routine.OperatorDumped`, `stored.ProcedureProvider`,
  `stored.TypeProvider`). `BootstrapNamespaceDumpable` is the builtin
  namespace ACL check.
- **Capture checks** (`capture_checks.go`, `aclitem_preflight.go`):
  `CheckPortable` rejects catalogs Dump cannot serialize (SQL/MED
  metadata, default privileges, invalid indexes); `PreflightACLItems`
  checks every ACL leaf a dump would write. The root calls both in its
  lock window, before `Collect`, in today's order.
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
- **Large objects** (`largeobject.go`): immutable captured descriptors use a
  separate fixed-OID-sorted list, outside numeric relation graph keys. Owner and
  raw ACL formatting is preflighted through existing codecs before writer I/O.
  Sparse chunks are emitted without copying or materializing holes, under exact
  owner directives; typed raw ACL repairs follow content. The root admits only
  these bounded literal replay shapes and installs in the private stage.


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

### Catalog replacement

- **Definition OID remap** (`definition_oids.go`, `view_rule_oids.go`):
  `RemapViewRuleOIDs(view, allocate)` gives each imported view rule an OID
  that no descriptor, other rule or earlier claim uses, then runs
  `RemapDefinitionOIDs(view, allocate)`. That reserves every OID of the
  candidate (descriptors, aggregate helpers, domain checks and relation
  definitions), keeps the identities retained TEMP objects claim, and gives
  each colliding imported domain check, rule, policy, constraint, default
  or generation a fresh OID. It rebuilds the unique and exclusion backing
  of the index it touches, repoints partition parents of remapped unique
  and exclusion constraints, and rewrites rule references to remapped
  constraints. `view` reads the private replacement candidate, which both
  functions change in place. `allocate` is the engine's OID allocator: the
  caller (`preserveTemporaryLocked`) holds `Engine.mu` for the whole call.
  Error texts are the engine's (`stuntgres: invalid imported ...`).
- **TEMP preservation phases** (`persistent_replacement.go`,
  `temporary_set.go`, `temporary_types.go`, `temporary_identity.go`,
  `replacement_remap.go`, `replacement_remap_relation.go`): the decisions a
  persistent replacement makes to keep live TEMP objects.
  `PersistentReplacementCatalog(view)` returns the private candidate
  without temporary namespaces, their members and names (a temporary
  extension installation is not portable). Then, over `current` (the
  published catalog) and `next` (the candidate): `TemporarySet` collects
  the temporary namespaces and their members and rejects a candidate
  that rebinds a live temporary extension's name; `CheckTemporaryDependencies`
  rejects a candidate that changes a retained policy's sources or
  dependencies or a user domain under a retained array column;
  `RetainTemporaryBuiltinTypes` keeps in `next` the exact builtin type
  generations retained objects use and reports whether it changed
  anything; `TemporaryCollisionMapping(next, temporary, allocate)` gives
  each imported identity (and aggregate helper) that collides with a
  retained OID a fresh one and returns the mapping;
  `RemapReplacementIdentities` moves the mapped descriptors, their
  references, dump priorities, default schemas, the name indexes and the
  storage and sequence roots. The root calls them in this order from
  `preserveTemporaryLocked`, under `Engine.mu`, with the retain, check,
  import, repair and install steps between them; error texts are the
  engine's.

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

- `Engine.mu` and the Dump capture window: `captureDumpLocked`
  (`dump_capture.go`) reads the engine state, calls `CheckPortable`, builds
  the generation recipe plan, calls `PreflightACLItems`, projects the
  constraint catalog, runs the temporary collation check (it needs a
  catalog stage) and the configured foreign namespace check, then fills a
  `Source` and calls `Collect`. The root hands the capture over: `Dump`
  sorts and completes it in place, and the root never reads it after the
  call.
- The OID allocator (`catalogStore.allocateOIDLocked`) and the TEMP
  preservation orchestrator (`preserveTemporaryLocked`): it holds
  `Engine.mu`, calls the phases above and keeps the steps that need the
  engine or its stage: `retainDatabaseLocked`, the relation, typed and
  rule source checks, `retainTemporaryViewProvidersLocked`, the scalar
  and domain imports, `repairRemappedGenerationsLocked`, the installation
  of the live TEMP descriptors and storage (with the stage lock), the TEMP
  policy and rule ref refresh, and the final validation.
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
expressions, the builtin namespace ACL check, the resolver users and the
definition OID remap and the TEMP preservation callers:

```sh
make check RUN='^Test(RuleNamedConstraintRemap|DeparseConstraintsAllocatorOccupancy|PartitionConstraintsDefinitionRemapping|ViewRuleOIDUniquenessAfterRestore|RestoreAdmitsInsertTargetIndirection|DumpPresenterDomainExpressions|PGNamespaceShape|PredefinedRoleControls|IndexPropertyDump.*|IdentityDumpProviderMapping|IdentityControlsOwnerCollisionPredecessors|PartitionAttachDumpPhaseDependencies|PartitionDetach(Replay|UniqueReplay).*|DomainAlterConstraintReplayEligibility|InheritanceControls.*|TypedTemporary.*|PGAggregateIntagg.*|UserOperatorIndex.*|ExtensionTemporary.*|DefaultPrivilegesControls|PartitionConstraints.*|.*RetainedTemporary.*|.*TemporaryCollision)$'
```

Public behavior is covered by the Dump, Restore and Control reference tests
(quick mode, then `REFERENCE=1`). Dump output must stay byte-identical:

```sh
make check RUN='^TestReference.*(Dump|Restore|Native|RoundTrip|Replay)|^TestReferenceControl'
make check RACE=1 RUN='^TestReferenceControl|^TestControlTransactions|^TestRestore(Driver|Hooks)'
```

Changes to the Collect or Emit tables, the sections or the script also need a dump,
restore and re-dump comparison against a `master` baseline (see 18b and
18c in [ENGINE-SPLIT-dump.md](../../../docs/ENGINE-SPLIT-dump.md)).
