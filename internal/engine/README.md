# internal/engine

The root is the composition layer: engine constructors, family statement dispatch,
the engine factory, and `Reset`, `Dump` and `Restore`. Runtime state, admission,
publication and transaction lifecycle belong to [`core`](core/README.md).
Statement families own their `*cmds` executors and hosts; lower packages own the
decisions those executors orchestrate.

No production package imports the root except the driver, which uses constructors
and controls and names core types for everything else. Tests compose engines here
and use [`enginetest`](enginetest/README.md).

## Composition

`newEngine` seeds state with `core.NewEngine`, installs `Engine.Statements` with
`familyStatements`, and sets `Engine.Factory` to a constructor retaining immutable
`core.Startup`. Feature-owned catalog providers register before builtin namespace
descriptors. The factory supplies template seeds and isolated Restore stages.

`Reset(ctx, e)`, `Dump(ctx, e, dst)` and `Restore(ctx, e, src)` are composition
functions over `*core.Engine`. Reset and Restore publish through core's catalog
replacement API, not a separate publication path.

## Hosts

Family hosts live with their executors. Core supplies their common `DDLHost`;
the root keeps `restoreHost`, which implements `control.Host`.

## Native Dump and Restore

[`control`](control/README.md) owns the drivers and ordered hook table. The root
owns Dump's `Engine.Mu` capture window, the isolated restore stage and `restoreRun`.
A hook's table position fixes its order within a phase. Replay repairs operate on
the private stage and remain subject to ordinary identity, owner, dependency and
publication fences. Names and metadata comments alone grant no replay authority.

Registered executable prerequisites select authentic destination tokens by logical
registration identity and version; callbacks and physical session resources are
not serialized or reconstructed by replay.

## Testing

Root tests cover constructor composition, controls and Restore-hook integration.
Runtime tests live in core, family tests beside `*cmds` executors, and lower-package
engine fixtures in their owner's `enginetests`. See
[docs/TESTING.md](../../docs/TESTING.md) for placement.
