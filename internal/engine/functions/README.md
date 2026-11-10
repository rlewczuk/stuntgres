# internal/engine/functions

Builtin/installed function binding recipes, immutable expression nodes, kernels,
and aggregate/window providers. This leaf imports `expr`, `fts`, `ids`, scalar
facilities and parser/diagnostics, never catalog, core, storage or sessions.
Engine-owned wrappers and narrow runtime capabilities bridge those boundaries.

## Registry

`registry.go` owns a static `definitions` table; there is no `init` registration.
`Lookup` claims unqualified names under its exact-spelling policy. The analyzer
runs shared callable competition before the provider binds; registered names
must not overlap a remaining dispatch-chain owner. `Bind` owns argument analysis
and the provider's diagnostics.

Definition flags specify shared spelling/label gates. `ProbeCall`, `ResultCall`
and `NamedCall` are effect-free selection hooks: losing candidates never bind,
infer parameters, coerce values, capture dependencies or evaluate. Resolution
failure/ambiguity does not retry through a builtin fallback.

Installed discovery uses the genuine selected declaration and provider family.
Public names and executable symbols are not invocation authority or aliases.
Syntax/operator-only paths call exported bind/finish/select helpers directly.

## Binder

`Binder` is the reviewed capability seam implemented by `analyze.Binder`: argument
analysis/coercion, type/collation lookup, selected routine identity, diagnostics,
snapshot demand and installed attachment. `AggregateBinder` adds grouping-only
operations. Pass explicit values rather than expanding this into a service locator.

Declared formals rank overloads independently of concrete invocation targets.
Written operands retain source order and formal-slot provenance. The selected
provider owns strictness, effect/error order, defaults and allowed decorations.
Binding and Describe never initialize physical resources.

## Closed source providers

Each source-capable family implements `expr.Source` and `SourceOpening`; there
is no scalar-FROM fallback or central source registry. Set cardinality, nominal
shape and column nullability are separate contracts. Open creates invocation-
private state; EOF, error, cancellation and Close release it without invalidating
already emitted immutable values. Engine scheduling owns query lifetime.

## Evaluation capabilities

Subsystem-dependent nodes ask `expr.Context` for a narrow runtime interface.
`exec.Context` forwards to the current statement host, which owns locking and
returns owned values. Recipes retain typed operands, selected identities and
immutable metadata, never Engine, Session, StatementScope, manager, callback,
resource or cached authorization. Missing runtime is an error, not a fake value.

## Aggregate and window registries

Static registries select immutable providers; owned aggregate declarations remain
catalog authority rather than same-name table entries. Group/window state is
execution-local. The executor owns ordering, FILTER, transitions and finalization;
providers borrow the current context and evaluated inputs, never retain them.
Stored providers implement the remapping contract explicitly.

## Adding or porting a family

Place bind code beside its node, register only its claimed names, and reuse shared
callable/coercion/diagnostic paths. Installed families attach through
`InstalledRoutine`; catalog-dependent execution stays in an engine-owned wrapper.
Add leaf protocol and registry tests, with composed owner tests at the engine seam.

## Node protocol

Every node implements `expr.Composite` and the applicable optional hooks defined
in [`expr`](../expr/README.md#node-protocol).

- `Children` lists lexical operands. `Rebuild` validates child count and derived
  inputs without changing the receiver; wrappers preserve selected identity.
- `Substitute` preserves provider/derived inputs instead of rebinding.
- Capture explicitly implements `Capturable`; composite membership is not capture
  admission. Hooks never evaluate, render, rebind or mutate during capture.
- `SameGroup` compares semantic recipe identity, not presentation.
- `ResultNullability` answers only from immutable recipe facts.
- `Render` supplies a rendering descriptor; engine renderers own quoting, casts
  and installed namespace presentation.

## What stays in the engine

Catalog/name/ACL/dependency resolution, type generations, stored rendering,
installed wrappers, query expansion, mutable aggregate state, session RNG/clocks,
and runtime authority bodies remain in their respective engine owners.

## Testing

Leaf registry/node/kernel/provider tests live here. Real-analyzer and execution
fixtures live in `core/enginetests/functions` or their owning package. Public SQL
belongs to reference packages. Unit fakes implement only exercised capabilities
and fail if an unexpected runtime capability is requested.
