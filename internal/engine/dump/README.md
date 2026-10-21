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
  - `Presenter` renders type and column declarations and routine
    identities. Every method is a pure function of its arguments: no
    engine state, no lock. Methods are added with their first caller.

## What stays in the engine

`Engine.Dump` (its `Engine.mu` window, the snapshot capture and the
emission sequence) and `Engine.Restore` until step 30. `dumpPredecessor`
(`dump_resolver.go`) reaches partition, operator-shell and competing-index
helpers, so it stays in the root behind the `dumpResolver` adapter.
`dumpPresenter` (`dump_presenter.go`) forwards to the root's type, column
and routine presentation helpers until `regobj` (step 21) owns them. The
table dump record `dumpRelation` moves with `table` (step 24).

## Testing

Package units:

```sh
make check RUN='^Test(OrderPhaseScheduler|RestoreScalar(SubscriptModes|ReferenceLiteralBoundary|CastParameterGuard))$'
```

Engine witnesses that build dump graphs or call the validators:

```sh
make check RUN='^Test(PartitionAttachDumpPhaseDependencies|UserOperatorDump|PartitionDetach(Replay|UniqueReplay)|InheritanceControls|InheritanceGraph|DomainAlterConstraint|RegTS|PartitionConstraints|IndexPropertyDump)'
```

Public behavior is covered by the Dump, Restore and Control reference tests
(quick mode, then `REFERENCE=1`). The output must stay byte-identical:

```sh
make check RUN='^TestReference.*(Dump|Restore|Native|RoundTrip|Replay)'
make check RUN='^TestReferenceControl'
```
