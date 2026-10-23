# internal/engine/control

The native Dump and Restore drivers and the catalog-replacement phases they
share with the engine. Dump turns a captured catalog into an ordered SQL script;
Restore replays such a script through an ordered hook table. A feature adds a
table entry instead of editing a central switch. The event graph, its order and
the statement records are in [`dump`](../dump/README.md); per-object serializers
live in their feature packages; the public contract is
[docs/FIXTURES.md](../../../docs/FIXTURES.md).

## Imports and state

It imports `access`, `analyze`, `authorization`, `catalog`, `dump`, `exec`,
`expr`, `extension`, `fdw`, `ids`, `index`, `largeobject`, `partition`,
`pgcatalog`, `plan`, `regobj`, `routine`, `sequence`, `session`, `storage`,
`stored`, `table`, `typedef`, `view`, `visibility`, `parser`, `sqltype` and
`sqlstate`. Only the root engine imports it. It holds no mutex, no engine
pointer and no state between calls.

## Dump

- **Collect** (`collect.go`, `collect_relation.go`, `collect_foreign.go`):
  `Collect(ctx, source)` builds the builtin-schema preamble, hands every
  descriptor of `source.State` to the collect tables and returns the filled
  `Capture`. `Source` is everything the tables read (catalog and role reads,
  constraint replay, ACL output, generation recipe plan, catalog state, relation
  roots, sequence generators, commit frontier, bootstrap principal, foreign
  registry). The root fills it under `Engine.mu` and calls `Collect` inside that
  window; nothing in `Source` is read after the unlock.
- **Collect tables.** `dumpCollectors` is an ordered list of
  `{name, matches, collect}` entries: the first entry whose `matches` accepts a
  descriptor collects it; a descriptor no entry accepts fails with
  `unsupported catalog descriptor kind`; objects in temporary namespaces are
  skipped before matching. The relation entry runs the shared relation checks,
  then `dumpRelationCollectors` (foreign, composite, index, view, sequence,
  table) the same way. **Entry order is error precedence:** keep it when adding
  an entry. Feature packages own an entry's decision (`routine.OperatorDumped`,
  `stored.ProcedureProvider`, `stored.TypeProvider`);
  `BootstrapNamespaceDumpable` is the builtin namespace ACL check.
- **Capture checks** (`capture_checks.go`, `aclitem_preflight.go`):
  `CheckPortable` rejects unserializable explicit SQL/MED ACLs, default
  privileges and invalid indexes; `PreflightACLItems` checks every ACL leaf a
  dump would write. The root calls both in its lock window, before `Collect`.
- **Capture record** (`dump.go`): `Capture` holds everything Dump reads (reads,
  collected schemas, types, routines, tables, views, indexes and the event
  `Graph`); `Schema`, `OwnedStatement` and `SchemaComment` are its records.
- **Driver** (`dump.go`): `Dump(ctx, capture, dst)` sets the ACL output
  context, builds the script, writes the sections and returns `ctx.Err()`.
- **Script** (`script.go`): `buildDumpScript` renders the policy, rule,
  row-security and type statements, completes the event graph, decides default
  deferral and orders events with `dump.Order`.
- **Sections and emit table** (`sections.go`, `emit.go`, `emit_declaration.go`):
  `dumpSections` is the whole script in order; its `events` section writes each
  ordered event through `dumpPhaseEmitters`, indexed by `dump.Phase`. Each entry
  has an `owner` that the loop sets before the entry runs (`nil`: the emitter
  sets it, as `Declaration` and `DomainChecks` do; domain checks render before
  the owner switch so an error writes nothing) and an `emit`; an empty entry
  (`TableCompletion`) writes nothing. `dumpWriter` writes text and tracks the
  `SET SESSION AUTHORIZATION` owner. A feature exports its dump function and the
  table names it in the matching position.
- **Adapters** (`resolver.go`, `presenter.go`): `Resolver` is the
  `dump.Resolver` (`Predecessor` maps a dependency to the event that must
  precede its consumer; `ColumnPredecessors` adds row and column-type
  predecessors) and `Presenter` the `dump.Presenter`. Both read only their
  arguments.
- **Large objects** (`largeobject.go`): captured immutable descriptors use a
  separate fixed-OID-sorted list, outside the relation graph keys. Owner and raw
  ACL formatting is preflighted before writer I/O; sparse chunks are emitted
  without materializing holes, under exact owner directives, followed by typed
  raw ACL repairs.

### Dump ordering rules

- **Foreign data:** wrapper before server, server before mappings and tables,
  types before tables, tables before views. Registered handler procedures are
  validated destination prerequisites, not dump events. Foreign relations never
  enter storage, row or default-repair collection.
- **Access methods:** one quoted CREATE with a qualified destination handler and
  no owner directive, before dependent families, classes and indexes; builtin
  methods are validated and omitted.
- **Operator classes:** family and implementation objects precede the class,
  the class precedes indexes and partition consumers. INTERNAL members are
  declared once in CREATE OPERATOR CLASS; loose members stay separate family
  events.
- **Routines and aggregates** share routine dependency scheduling: support and
  type predecessors precede aggregate declarations and their consumers.
  Explicit SQL-routine and aggregate ACLs are rejected before output, never
  silently reset.
- **Relation options** are emitted after the table or materialized-view
  declaration as one qualified owner-context `ALTER TABLE ... SET (...)`, base
  options then TOAST options in captured order; absent options emit nothing.
- **Bootstrap namespaces** that were renamed, dropped or recreated are
  reconstructed with ordinary DROP/ALTER SCHEMA before user schemas. Retained
  builtin ACLs must equal the canonical bootstrap rights transferred to the
  current owner; anything else is rejected before output.
- **Default deferral** also covers a retained, validated SQL-standard routine
  whose exact reference is in the default's captured dependencies, breaking the
  table → routine → table cycle as CREATE TABLE, CREATE FUNCTION, then
  ALTER TABLE SET DEFAULT. Neither deferral nor Restore evaluates a default.
- **Cast replay windows.** `cast.go` shares catalog dependency ownership for
  graph ordering. `cast_replay.go` derives ordinary-SQL cast windows only after
  default deferral and invalid/inherited CHECK phases are settled;
  `cast_consumer.go` identifies the statement actually emitted (CREATE, deferred
  SET DEFAULT or individual ADD CHECK), and each owns its own window. Cast
  declarations are kept out of the generic early declaration chain so a final
  pair can follow a post-data window; the final conflicting cast row is
  installed once after all windows for its pair, and no helper survives.
  Retained aggregate binary recipes and foreign defaults use the same windows:
  inline foreign members bind in their CREATE FOREIGN TABLE, late attributes in
  the post-rules foreign-default section, deferring only conflicting members.
- **Generated columns** with differing cast epochs pass explicit seeds to
  `table.PrepareGenerationPlaceholders`; SET EXPRESSION windows are per
  attribute. Recursive historical CHECKs use the real ADD CHECK executor after
  every affected declaration and before affected rows; local leaf CHECKs use
  `ONLY`.
- **Exact FLOAT4 words.** Ordinary values (including subnormals, signed zero,
  infinities and canonical NaN) keep canonical literals; only noncanonical NaN
  words need raw replay. `regobj.NeedsFloat4RawReplay` gates literal rendering
  and bounded composite-array INSERT targets, detected from visible physical
  rows (excluding generated cells) and retained literals. Dump uses the final
  binary `xid → real` edge, or a temporary CREATE/DROP CAST around raw recipes
  and data, preserving a conflicting final edge. Raw SQL names
  `pg_catalog.float4` (never `pg_catalog.real`) and raw word arrays
  `pg_catalog._xid`. Composite-array replay uses INSERT target slices to keep
  bounds; Restore has no hidden raw-datum decoder.

## Restore

- **Driver** (`restore.go`): `Restore[R](ctx, host, hooks, run, src)` reads `src`
  through `contextReader` (stops once `ctx` is done), parses it with the
  `Decode` entries reading line comments, then calls `Host.Begin` and
  `Host.Start` (its `end` is deferred). Per statement it calls `Host.Statement`,
  walks `Before`, `Admit` and `Prepare`, calls `Host.Execute` and walks `After`.
  Then it walks `Finish` and calls `Host.Publish`. The first error stops replay
  and skips `Publish`; `end` still runs.
- **Hook table** (`hooks.go`): `Hook[R]` has `Name` and the optional phase
  functions `Decode`, `Before`, `Admit`, `Prepare`, `After` and `Finish` over
  the caller's run state `R`. `Admission` is an admit answer: `NotHandled`,
  `Admitted` or `Consumed` (replayed, nothing executes); the first non-
  `NotHandled` answer wins. A statement no entry handles is rejected with
  SQLSTATE `0A000` and the allow-list text. `Outcome{Created}` is what an
  executed statement tells the `After` entries.
- **Admission checks** (`admit.go`, `admit_checks.go`): allow-list entries
  that never read the run. `AdmitKind[R, T]`, `AdmitSchemaObject[R]`,
  `AdmitOperator[R]` and `AdmitStatement[R, T](check)` (answers `NotHandled`
  with the check's error) over statement-only checks (`CheckCreateTable`,
  `CheckAlterDomain`, `CheckCreateView`, `CheckInsert`, ...). The root's hook
  table names them in its own order, so rejection order is the table's.

### Host contract

`Host` is the engine's side of one Restore (the root `restoreHost`):

- `Begin(source, statements)` records the script on the run and pairs domain
  replay records with their INSERTs.
- `Start(ctx)` starts the private stage's session as the bootstrap superuser
  and acquires its operation. `end` clears the operation's replay flags,
  releases the operation and ends the session, in that order. If `Start` fails
  it cleans up and returns no `end`.
- `Statement(index)` resets per-statement state; `Execute(op, stmt)` runs one
  admitted statement on the stage, clears the per-statement replay flags,
  closes its rows and detaches its notices.
- `Publish(ctx, op)` installs the stage catalog into the target engine.

Entries share state only through the run, so their order is part of the
contract; every entry runs on the stage, never on the target engine.

## Catalog replacement

- **Definition OID remap** (`definition_oids.go`, `view_rule_oids.go`):
  `RemapViewRuleOIDs(view, allocate)` gives each imported view rule an unused
  OID, then runs `RemapDefinitionOIDs`, which reserves every candidate OID,
  keeps identities claimed by retained TEMP objects and gives each colliding
  imported domain check, rule, policy, constraint, default or generation a
  fresh OID. It rebuilds touched unique/exclusion backing, repoints partition
  parents and rewrites rule references to remapped constraints. Both change the
  private candidate in place; the caller (`preserveTemporaryLocked`) holds
  `Engine.mu` for the whole call and supplies the engine's OID allocator.
- **TEMP preservation phases** (`persistent_replacement.go`, `temporary_*.go`,
  `replacement_remap*.go`), called in this order by `preserveTemporaryLocked`
  under `Engine.mu`: `PersistentReplacementCatalog` (the candidate without
  temporary namespaces and members), `TemporarySet` (collects them; rejects
  rebinding a live temporary extension's name), `RetainTemporaryBuiltinTypes`,
  `CheckTemporaryDependencies` (rejects changed policy sources, user providers
  or user domains under retained array columns; builtin types, seeded
  collations and seeded procedures may bridge only when exactly retained or
  unchanged), `TemporaryCollisionMapping` and `RemapReplacementIdentities`.
  These precede allocator and publication work; error texts are the engine's.
- **TEMP source checks** (`temporary_sources.go`), over the published catalog
  under `Engine.mu`: `CheckTemporaryRelationSourcesLocked` rejects a retained
  TEMP table with a persistent inheritance parent;
  `CheckTemporaryRuleSourcesLocked` rejects a retained TEMP rule depending on an
  object outside its session's temporary namespace (pinned builtins aside);
  `RetainTemporaryViewProvidersLocked` keeps retained TEMP views' exact
  bootstrap sources after checking shape and namespace/ACL authority.

## What stays in the engine

- `Engine.mu` and the Dump capture window (`captureDumpLocked` in
  `dump_capture.go`): reads engine state, calls `CheckPortable`, builds the
  generation recipe plan, calls `PreflightACLItems`, runs the temporary
  collation and foreign namespace checks, then fills `Source` and calls
  `Collect`. `Dump` sorts and completes the capture in place; the root never
  reads it afterwards.
- The OID allocator and `preserveTemporaryLocked`, which keeps the steps that
  need the engine or its stage (typedef scalar/domain imports, generation
  repair, TEMP descriptor and storage installation, final validation).
- The restore stage (`restoreStage`) and atomic publication (`replaceCatalog`)
  with their lock order and fences.
- `restoreRun`, `restoreHost`, the hook table `restoreHooks` and every hook
  that reads the run (ALTER OWNER, foreign-key capture, ALTER TABLE, DETACH
  PARTITION, DROP TABLE, ALTER SEQUENCE, SET SESSION AUTHORIZATION, UPDATE and
  DELETE repairs, SQL/MED), plus `Engine.Reset` and the thin `Engine.Dump`/
  `Engine.Restore` wrappers in `control.go`.

## Testing

Package units drive the Restore driver and hook walkers with a fake `Host` and
cover FDW dump definitions, dependencies and remaps; engine units cover the
collect, emit, remap and TEMP preservation callers. Public Dump/Restore
behavior is in `reference/`; follow [docs/TESTING.md](../../../docs/TESTING.md)
and add `REFERENCE=1` for the oracle. Dump output must stay byte-identical
unless a change intends otherwise.

- **Collect, emit, section or script changes** also need a byte-identity check:
  write a throwaway program under `.cswd/tmp/<task>/` that opens a database
  through the root driver, runs a fixed corpus covering every object kind,
  calls `stuntgres.Dump`, restores into a fresh database and dumps again. Run it
  on a detached `master` worktree and on the branch; both dumps must match byte
  for byte. Never commit the program.

Selectors:

- Driver and hooks: `RUN='^TestRestore(Driver|Hooks|AdmitsInsertTargetIndirection)'`
- FDW dump units: `RUN='^TestFDW(Dump|ReplacementRemap)'`
- Engine remap and TEMP preservation: `RUN='^Test(RuleNamedConstraintRemap|ViewRuleOIDUniquenessAfterRestore|DumpPresenterDomainExpressions|PartitionConstraintsDefinitionRemapping|.*RetainedTemporary.*|.*TemporaryCollision)$'`
- Control round trips: `RUN='^TestReference(Control|DumpRoundTrip)'`
- Cast persistence and replay windows: `RUN='^TestReferenceCastDefinition(Persistence|ReplayPhases|ReplayConflicts|RawFloat4Persistence|RawCompositeArrayBounds|Atomicity|ForeignDefaults)$'`
- Canonical FLOAT4 dumps: `RUN='^TestReferenceFloat(ControlSnapshots|ExactOutputText)'` and `RUN='^TestFloat4NeedsRawReplay$'`
- Aggregates and operator classes: `RUN='^TestReference(CreateAggregate(Persistence|TemporaryRemap)|OperatorClass(Roundtrip|SeedRoundtrip))$'`
- FDW import persistence: `RUN='^TestReferenceFDWImport'`
