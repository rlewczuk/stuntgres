# internal/engine/dump

The records native Dump orders and emits. Dump writes the catalog as a SQL
script; to order it, the engine builds a dependency graph of events (a
declaration, a rename, a row load, an attachment, ...) and sorts it. This
package owns that graph, its order, the statement records feature dump code
produces and the Restore scalar validators, so feature packages can produce
dump statements without reaching into the engine. The Dump driver itself is in
[`control`](../control/README.md).

The package sits at layer L3.5 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture). It
imports only `ids`, `catalog`, `expr`, `functions`, `parser`, `sqltype` and the
standard library, and holds no state, no mutex and no engine callback.

## What it owns

- **Event graph** (`graph.go`): `Phase` and its 16 ordered constants
  (`Declaration` … `OperatorLinks`), `Event{OID, Phase}`, `Graph` (each event
  to the events that must precede it) with `Add` and `DependsOn`, and
  `DeclarationEvents`/`DeclarationIDs`. The numeric phase order is part of the
  output order: add new phases at the end.
- **Order** (`order.go`): `Order` sorts a captured graph topologically. It
  breaks ties by captured priority (the OID when none is captured), then OID,
  then phase. It never reads the catalog, honors cancellation, and fails on
  missing predecessors and on cycles, self edges included. Its error texts
  (`stuntgres: cyclic view dump dependencies`, `stuntgres: missing view dump
  predecessor`) name views for every event kind; they are public and stay
  byte for byte.
- **Records** (`records.go`): `Statement{Owner, SQL}` (one statement and the
  role that owns its objects), `Group{Owner, Table, SQL}` (a table's statement
  block) and `CommentLiteral` (an explicit `E'...'` string).
- **Restore scalar validation** (`restore_scalar.go`): `RestoreScalar` in two
  modes (stored CREATE expressions keep the binder's admission rules; INSERT
  values keep the Restore whitelist) and `RestoreSQLJSONQuery`. Both reject
  every parameter with SQLSTATE 42P02.
  - Standard SQL/JSON query scalars recurse through the document, path, every
    PASSING operand and both DEFAULT children, including branches skipped at
    runtime.
  - The seven FTS algebra routine names (eight signatures, phrase2/3
    included) are admitted through the same recursive INSERT VALUES
    whitelist. Their ordinary binder still owns signatures and types, and
    every parameter is rejected even beneath strict-NULL expressions. This
    gives staged runtime phrase-failure atomicity without admitting Restore
    INSERT query sources.
  - Selected `cube`/`cube_dim`/`cube_is_point` names admit constructor/inquiry
    recipes in Restore INSERT values. Their ordinary installed binder owns
    overloads, provider authority and value diagnostics; recursive parameter
    rejection remains unconditional. Spatial cube routine names remain excluded.
- **Capabilities** (`capabilities.go`), implemented above this package:
  - `Resolver.Predecessor` maps a dependency address to the OID of the dump
    event that must precede its consumer, or 0.
  - `Presenter` renders type and column declarations, routine identities and
    domain DEFAULT and CHECK expressions (`DomainDefault`, `DomainCheck`).
    Every method is a pure function of its arguments: no engine state, no
    lock; the expression methods also honor cancellation. Add a method only
    together with its first caller.

## Where Dump runs

- **Driver** ([`control`](../control/README.md)): the script build, the
  section list and the Emit table, and the `control.Resolver` and
  `control.Presenter` adapters. `control.Resolver` reaches partition,
  operator-shell and competing-index helpers; the presenter forwards type,
  column and routine presentation to `regobj` and the domain expressions to
  `stored.ScalarRenderer`.
- **Feature dump functions** take the capabilities as `Resolver` and
  `Presenter`: those of `routine`, `typedef`, `partition`, `table`
  (`PhysicalNotNullDumpDependencies`) and `regobj`
  (`DomainInvalidDumpEvents`). The table dump record is `table.DumpRelation`.
- **Capture (root).** `Engine.Dump` takes `Engine.Mu`, calls
  `captureDumpLocked` (`dump_capture.go`) and unlocks once. Inside that window
  the root reads engine state and runs `control.Collect` (the Collect tables)
  over a `control.Source`; it returns a `control.Capture` and hands it to
  `control.Dump`. Nothing after the unlock reads engine state; everything
  reads the captured immutable views. The Collect tables and their entry
  contract are in the `control` README.

## Testing

Package units pin `Order`'s phase scheduler and the `RestoreScalar` modes;
engine witnesses build dump graphs (partition attach/detach, operators,
inheritance, domain constraints, index properties) or call the validators;
`TestDumpPresenterDomainExpressions` pins `control.Presenter`'s domain
expression methods.

```sh
make check RUN='^Test(OrderPhaseScheduler|RestoreScalar|FTSOpsRestoreScalar|GeometryConversionReplayShapes)'
make check RUN='^Test(PartitionAttachDumpPhaseDependencies|UserOperatorDump|PartitionDetach(Replay|UniqueReplay)|InheritanceGraph|IndexPropertyDump|DumpPresenterDomainExpressions)'
make check RUN='^TestReference.*(Dump|Restore|Native|RoundTrip|Replay)'
make check RUN='^TestReferenceControl'
```

Dump output must stay byte-identical. Changes to the root Collect tables,
`control`'s Emit table or the `Engine.Mu` window also need
`make check RACE=1 RUN='^TestReferenceControl'` and a baseline comparison: a
throwaway program under `.cswd/tmp/<task>/` (never committed) opens a database
with the root driver, runs a fixed corpus covering every object kind, calls
`stuntgres.Dump`, restores into a fresh database with `stuntgres.Restore` and
dumps again. Run it on a detached `master` worktree and on the branch; both
dumps must match byte for byte.
