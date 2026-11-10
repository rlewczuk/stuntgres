# internal/engine/indexcmds

Index statement orchestration over core and `index` decisions. It imports core
and tablecmds' shared family hosts, never the root. Composition dispatches here;
partitioncmds may reuse it through the declared family layering.

## Layering

Core keeps maintenance admission, unique/exclusion runtime, root publication and
wait registries. Index owns typed binding/build/maintenance decisions. This
package owns utility phases, short lock windows and stage lifetime.

## Entry points

`ExecuteCreateIndex` and `ExecuteIndexMaintenance` enter with nothing held.
Resolve/stage under Engine.Mu, release it for waits/provider/expression work,
then recheck original identity and authority before continuing. Final stage
publication retains independent descriptor, storage and cancellation fences.
Explicit targets and expanded dependency closure retain distinct provenance.

## CONCURRENTLY

The concurrent build publishes explicit readiness/validity phases, with frozen
writer/snapshot cohorts between them. Each phase rechecks current authority and
its admitted predecessor; waits run unlocked and registry entries clear on every
exit. Detached builds activate only their current invocation context after
capture unlocks. Runtime grants and immutable root generation are not retained
by the definition itself.

## Testing

External `indexcmds_test` tests use a composed engine and `enginetest` helpers.
Test-only accessors belong in `export_test.go` only when exported APIs cannot
assert the contract. Public SQL belongs to reference index packages under
[docs/TESTING.md](../../../docs/TESTING.md).
