# internal/engine/exec

Query executor runtime and concrete evaluation context. It opens immutable bound
[`plan`](../plan/README.md) trees over captured sources, produces rows, handles
row-lock rechecks and evaluates expressions. Core owns admission, capture and
publication; this package never imports core or the root.

## What it owns

- `Source` describes captured relation/composite input. `State` owns one query
  invocation, `Stage` its producer state, and `Context` its current evaluation.
- Openers, nested/CTE row owners, stored-view producers and cursor-motion helpers
  retain exact source/frame membership and release resources at their own exits.
- Expression preflight builds private typed plans/folds before execution. Accepted
  artifacts carry genuine source/occurrence provenance; a supplied expression
  cannot mint DEFAULT or fold authority.
- Routine frames/arguments and row-policy programs are invocation-owned. Selected
  routine identity and defining manifests are distinct from current admission.
- Aggregate/window kernels own per-occurrence state and borrowed current-context
  finalization, never mutable state on a shared provider or definition.

## The host

`Host` is implemented by core's `statementHost`. Every method is called without
`Engine.Mu` held; host bodies own lock windows and return values, not engine
structs. Records store a host once, never per row. `NestedSQL` lends the caller's
admitted statement and balanced lexical scope to SQL-executing functions.

Runtime contexts belong to current pulls/evaluations. Workload/pause activation
is granted by known-unlocked owners, not inherited from a retained definition.
Detaching or transferring a producer clears old scoped context/authority and
readmits the current request through the live host.

## Preparation

`Preparation` supports scope-free domain/generated/index/partition evaluation.
It is installed explicitly; absence is an error, not a default live authority.
`AcceptedDefaultSet` belongs to an accepted binding and authenticates the
canonical source-bound recipe before invocation. Planning and executable
admission remain separate from publication of accepted immutable folds.

## Expression preflight

Preflight recursively preserves lexical/formal demand, typed inputs, conditional
planning and selected provider identity. Strict-NULL simplification applies only
through each node's explicit contract; it cannot infer set cardinality or erase
independent current-authority checks. Execution-demand barriers prevent enclosing
planning from evaluating runtime-only work.

## Invariants

- No package-owned mutex. Host bodies and the lock manager own locks/waits;
  manager mutexes never nest under `Engine.Mu`.
- Captured snapshots/catalogs and published trees are immutable. Stable isolation
  retains roots/frontier; RC captures execution roots after relation admission.
  Cursors retain the producer's pair when borrowing a fresh operation host.
- Finite public results finalize before completion. Projection, sort, close,
  cancellation or publication failure exposes no successful prefix. Result reads
  do not resume SQL, acquire grants or publish writes. Nested streaming SQL owns
  an active lexical scope and closes stages, CTEs and pins before restoration.
- Rowmarks follow admitted physical contributors after waits, without discovering
  new matches or resorting. Source occurrence/frame identity remains exact.
- Cancellation is polled at bounded work points. Close is idempotent and every
  terminal releases sources, imported state, memberships and pins.
- Retained admission slots borrow immutable values, never cache permissions.
- Parallel workers own private state/counters; the leader joins all work before
  shared cleanup or publication. Async requests are bounded and canceled/joined
  before scans end; pending control is never an expression error or EOF.
- Optimized candidate/index paths still evaluate the full semantic predicate and
  preserve ordinary order/errors. Unsupported proofs fall back to ordinary work.
- Host/preparation interfaces are stored once, not allocated per row.

## Row-policy runtime

`RLSProgram` owns conjoined command groups. `EvaluateRowSecurity` is the shared
true-only evaluator; selection metadata and action capability gates are separate.
Statement Finish disposes programs outside `Engine.Mu`; cursor/portal publication
transfers them to the real producer owner.

## What stays in the engine

Statement dispatch, live authority, source/visibility capture, relation admission,
write staging, runtime callbacks, transaction completion and final publication.

## Testing

Pure preparation, source, cursor, preflight and aggregate ownership units live
here. Core tests cover real admission, transfer and publication. Worker tests
use joined deterministic controls and assert actual state rather than timing;
public SQL remains in reference packages under
[docs/TESTING.md](../../../docs/TESTING.md).
