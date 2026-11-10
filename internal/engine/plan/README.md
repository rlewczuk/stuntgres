# internal/engine/plan

Closed immutable logical plan IR and concrete expression nodes. Binding builds
typed trees; `exec` executes them. This package imports lower catalog/expression/
function facilities, never core, the binder or a statement scope.

## What it owns

`Node`/`Query` and query records retain analyzed source/frame occurrences,
projections, grouping, windows, CTEs and dependencies. Expression nodes preserve
selected types, conversions, routines and lexical children. Installed wrappers
retain genuine procedure/ref authority around leaf kernels, not mutable spelling.

Shared walkers and copy-on-change mappers preserve analyzed metadata independently
of execution pruning. Group/occurrence equality, structural NULL, volatility,
generation admission, jumbling and node-tree presentation are explicit typed
protocols, not reflection or SQL-text identity. Logical estimates consume captured
facts without claiming physical cost or provider execution.

## Node runtime

`Runtime` embeds `expr.Context`; `exec.Context` implements it. Nodes recover only
this capability, never a concrete executor context. `BeginEvaluation` checks
context, wires active domain checks, then checks cancellation in defined order.
Runtime methods read current frames/parameters and run selected typed operations.
Hot paths pass scalar arguments rather than copying whole nodes per row.

## Invariants

- Published trees never mutate and execution never resolves names. Rewrites copy
  changed branches; analyzed dependencies survive removal of executable branches.
- No engine state, locks or hidden global evaluation runtime.
- Node evaluation order, strictness and diagnostics are compatibility contracts.
  Lexical child traversal and selected conversion wrappers preserve those rules.
- Rebuild/substitution preserve provider identity, input assembly, Usage and
  original occurrence without importing live authority or cached values.
- Lexical temporary frames save/restore across nested evaluation and failure;
  they are not SQL parameters or reusable callback state.
- Opaque upper-layer mutation payloads are tested for presence and recovered by
  their owner; plan code does not import analyzed statements or expand authority.

## What stays outside

Analysis and name resolution belong to `analyze`; runtime state, openers and
producer scheduling to `exec`; stored deparse/remap to `stored`; authority and
publication to core. Optional hooks live in [`expr`](../expr/README.md).

## Testing

Pure node/protocol/mapper/runtime-prologue tests live here. Composed binding,
stored ownership and execution tests live with those owners; public SQL belongs
to reference packages under [docs/TESTING.md](../../../docs/TESTING.md).
