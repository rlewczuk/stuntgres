# internal/engine/analyze

Statement binding: `Binder`, `Describe`, `Binding`, the engine-facing `Host`, and
immutable captures used by prepared and stored definitions. Rule rewriting and
view-write/RLS walkers belong to [`view`](../view/README.md); mutation execution
belongs to [`dml`](../dml/README.md).

## Imports

This package sits above the plan/execution and catalog layers and never imports
the engine. Core constructs its hosts and owns runtime publication.

## What it owns

- **`Binder`** owns per-statement name/type resolution, parameter inference,
  coercion and query namespace state. `Describe` produces one typed `Binding`.
- **`Host`** supplies engine services through explicit capabilities; only the host
  interprets opaque binding authority. Retargeting returns a copy.
- **`Binding`** carries the statement header, kind payload and typed query/write
  inputs. Capture groups retain original syntax occurrences, selected identities,
  declarations and dependencies, not execution grants or mutable row state.
- **`Expected`** carries the fixed held-description signature and lifetime checks.
- **Lookup and privilege walkers** share resolution and semantic demand across
  ordinary queries and stored consumers.
- **Default preparation** retains the genuine source-bound typed recipe, original
  consumer and occurrence. Evaluation installs that origin only for the selected
  artifact; it cannot replace current write, body or constraint authority.
- **Statement jumbling** projects typed discriminators and selected fields before
  folding. Fingerprints and replacement spans are separate from runtime values;
  unknown families fail classification rather than receiving a fallback identity.

## Invariants

- Bindings have value semantics, but pointer payloads and slot values may be shared.
  Clone command-mutable payloads before changing them; execution state does not
  belong in retained analysis. Nil slots mean absent; never store typed nils.
- Binding runs under the caller's `Engine.Mu`; host methods neither acquire nor
  release it. The package owns no mutex or engine state.
- Describe is effect-free: no producers, defaults, sequences, writes or application
  workload callbacks. Binding keeps execution-pause requests dormant while
  preserving cancellation and the caller's request for later unlocked activation.
- Parameter types derive from SQL destinations, not Go argument kinds. Published
  analyzed trees and dependency manifests are immutable. Execution pruning never
  removes analysis-time dependencies or authorization requirements.
- Capture keys use each node's own `Pos`, not a derived source position. Stored
  SQL capture order and rendering are compatibility contracts.
- Every row consumer installs its lexical namespace explicitly. Nil/empty exposes
  no local rows; retained target metadata grants no implicit visibility. Aliases,
  correlation, stable attributes and physical source demands remain occurrence-
  and frame-owned. Dense slots never encode negative system attributes.

## Binding rules

Callable probing is effect-free. Shared namespace/signature competition selects
one winner before defaults, coercion, dependency capture and executable provider
attachment. Declared inputs rank candidates; concrete invocation targets govern
coercion. Failure or ambiguity never falls back to a different provider.

Catalog-aware coercion retains selected conversion identity and full Usage.
Installed callable spelling is discovery/presentation only: genuine declaration,
provider and current authority authorize execution. Neither names nor mutable
extension membership synthesize capability.

## What stays in the engine

Core owns binder construction, path/settings capture, authority interpretation,
execution preflight, source capture and publication. Analysis retains no host,
context, frame, actor permission or provider resource for later execution.

## Testing

Pure binding seams are tested here; engine-host tests live in core and its
`enginetests/binder`. Public SQL belongs to reference packages. Use real captured
catalog/role authority when composing binder fixtures; see
[docs/TESTING.md](../../../docs/TESTING.md).
