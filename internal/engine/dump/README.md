# internal/engine/dump

The records native Dump orders and emits. Dump writes the catalog as a SQL
script. To order that script, the engine builds a dependency graph of
events (a declaration, a rename, a row load, an attachment, ...) and sorts
it. This package owns that graph, its order, the statement records feature
dump code produces and the Restore scalar validators, so that feature
packages can produce dump statements without reaching into the engine.

The package sits at layer L3.5 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports only `ids`, `catalog`, `expr`, `functions`, `parser`, `sqltype`
and the standard library. It holds no state, no mutex and no engine
callback. Step 18 of the split created it; see
[ENGINE-SPLIT-dump.md](../../../docs/ENGINE-SPLIT-dump.md).

## What it owns

- **Event graph** (`graph.go`): `Phase` and its 16 ordered constants
  (`Declaration` … `OperatorLinks`), `Event{OID, Phase}`, `Graph` (each
  event to the events that must precede it) with `Add` and `DependsOn`,
  and `DeclarationEvents`/`DeclarationIDs`. The numeric phase order is part
  of the output order: add new phases at the end.
- **Order** (`order.go`): `Order` sorts a captured graph topologically. It
  breaks ties by captured priority (the OID when none is captured), then
  OID, then phase. It never reads the catalog, honors cancellation, and
  fails on missing predecessors and cycles, self edges included. Its error
  texts say "view" for historical reasons and stay byte for byte.
- **Records** (`records.go`): `Statement{Owner, SQL}` (one statement and
  the role that owns its objects), `Group{Owner, Table, SQL}` (a table's
  statement block) and `CommentLiteral` (an explicit `E'...'` string).
- **Restore scalar validation** (`restore_scalar.go`): `RestoreScalar` in
  two modes (stored CREATE expressions keep the binder's admission rules;
  INSERT values keep the Restore whitelist) and `RestoreSQLJSONQuery`. Both
  reject every parameter with SQLSTATE 42P02.
- **Capabilities** (`capabilities.go`), implemented above this package:
  - `Resolver.Predecessor` maps a dependency address to the OID of the
    dump event that must precede its consumer, or 0.
  - `Presenter` renders type and column declarations, routine
    identities and domain DEFAULT and CHECK expressions (`DomainDefault`,
    `DomainCheck`; first callers: the `typedef` dump statements, 15e).
    Every method is a pure function of its arguments: no engine state, no
    lock; the expression methods also honor cancellation. Methods are
    added with their first caller.

## What stays in the engine

`Engine.Dump` (its `Engine.mu` window, the snapshot capture and the
emission sequence) and `Engine.Restore` until step 30. `dumpPredecessor`
(`dump_resolver.go`) reaches partition, operator-shell and competing-index
helpers, so it stays in the root behind the `dumpResolver` adapter.
`dumpPresenter` (`dump_presenter.go`) forwards type, column and routine
presentation to `regobj` and the domain expressions to the root's
stored-SQL renderer (`scalarRenderer`, until `stored`, step 22). The
root passes both adapters to the feature dump functions; `routine` (14e)
and `typedef` (15e) call them. The table dump record `dumpRelation` moves
with `table` (step 24).

### How `Engine.Dump` runs (root)

`Engine.Dump` has three stages, each in its own root file:

1. **Capture** (`dump_capture.go`). Dump takes `Engine.mu`, calls
   `captureDumpLocked` and unlocks once. The capture checks portability,
   builds the builtin-schema preamble and passes every descriptor to the
   Collect tables. It returns a `dumpCapture`; nothing after the unlock
   reads engine state.
2. **Build** (`dump_script.go`). `buildDumpScript` renders the groups and
   statements, completes the event graph, decides default deferral and
   orders the events with `Order`.
3. **Emit** (`dump_sections.go`, `dump_emit.go`,
   `dump_emit_declaration.go`). `dumpSections` is the whole script in
   order; its `events` section writes each ordered event through the Emit
   table. `dumpWriter` writes text and tracks the `SET SESSION
   AUTHORIZATION` owner.

**Collect tables** (`dump_collect.go`, `dump_collect_relation.go`).
`dumpCollectors` is an ordered list of
`{name, matches(Descriptor), collect(ctx, *dumpCollection, address, Descriptor)}`
entries. The first entry whose `matches` accepts a descriptor collects it;
a descriptor no entry accepts is `unsupported catalog descriptor kind`.
Objects in temporary namespaces are skipped before matching. The relation
entry runs the checks shared by every relation, then
`dumpRelationCollectors` (composite, index, view, sequence, table) in the
same first-match way. Entries fill the capture and read locked engine
state only through `dumpCollection.source`, which never outlives
`Engine.mu`. Order is error precedence: keep it when adding an entry.

**Emit table** (`dump_emit.go`). `dumpPhaseEmitters` is indexed by
`Phase`. Each entry has an `owner` the event loop sets before the entry
runs (`nil`: the emitter sets it, as `Declaration` and `DomainChecks` do;
domain checks render before the owner switch so an error writes nothing)
and an `emit`; an empty entry (`TableCompletion`) writes nothing. Table
phases emit a copy of the captured `dumpRelation`.

A feature package takes over an entry when its dump code moves (14e, 15e,
23b, 24b, 25b, 27b): it exports the entry's function, and the root table
names it in the same position. The entry types stay in the root until
step 30 (`control`) declares them. Because the entry signatures name root
types, the root entry stays a thin wrapper over the package's decision:
`collectOperatorDump` calls `routine.OperatorDumped`, and
`emitDumpOperatorLinks` writes the text `routine.OperatorDumpStatements`
rendered. `collectProcedureDump` stays in the root until the root
`procedureProvider` moves (`stored`, 22b).

## Testing

Package units:

```sh
make check RUN='^Test(OrderPhaseScheduler|RestoreScalar(SubscriptModes|ReferenceLiteralBoundary|CastParameterGuard))$'
```

Engine witnesses that build dump graphs or call the validators:

```sh
make check RUN='^Test(PartitionAttachDumpPhaseDependencies|UserOperatorDump|PartitionDetach(Replay|UniqueReplay)|InheritanceControls|InheritanceGraph|DomainAlterConstraint|RegTS|PartitionConstraints|IndexPropertyDump)'
```

The root presenter's domain expression methods:

```sh
make check RUN='^TestDumpPresenterDomainExpressions$'
```

Public behavior is covered by the Dump, Restore and Control reference tests
(quick mode, then `REFERENCE=1`). The output must stay byte-identical:

```sh
make check RUN='^TestReference.*(Dump|Restore|Native|RoundTrip|Replay)'
make check RUN='^TestReferenceControl'
```

Changes to the root Collect or Emit tables, or to the `Engine.mu` window,
also need `make check RACE=1 RUN='^TestReferenceControl'` and a dump,
restore and re-dump comparison against a `master` baseline (see 18b and 18c
in [ENGINE-SPLIT-dump.md](../../../docs/ENGINE-SPLIT-dump.md)).
