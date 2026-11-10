# internal/engine/core/explain

EXPLAIN presentation below core: typed command trees, logical estimates, selected
provider details and TEXT/XML/JSON/YAML output. Core owns command execution and
publication; this leaf never imports core.

## Contract

`ValidateOptions` validates ordered options against current session recognition;
`DescribeFormat` obtains result shape without running a command.
`NewRenderer`/`Command` build one command's trees; `Render`/`Output` encode them.
`LogOutput` consumes the same trees for automatic explanation, not replayed SQL.

Ordinary EXPLAIN uses effect-free analysis and never opens execution producers,
evaluates folds or starts provider execution. ANALYZE receives the actual accepted
command's `Observation` and selected plan facts from core. Invocation-owned
measurements and provider choices are never inferred from labels, affected rows
or static copied graphs. Estimates and explicitly logical metrics are not
physical costs/pages/WAL. Rendering neither evaluates PL values nor reconstructs
live SQL context from text.

All formats consume the same typed tree/actuals. Output is finalized through one
owner; failed rendering or final publication exposes no successful partial plan.

## Host and authority

Core's `statementHost` supplies `Host`: context, operation, I/O, captured/private
catalog/storage, planner methods and current authority fences. `Authority`
combines binding, policy and rewritten-command checks, with its paired access
view. Typed parameters and provider exports belong to the current command.

## Locks

The renderer enters with `Engine.Mu` held and owns no lock. Provider callbacks
and detached comparisons release it; foreign callbacks also release the
replacement read lock. Reacquisition rechecks context, catalog and authority.

## Testing

Pure tree/encoder units live here. Core tests cover real description, execution,
provider and publication ownership; public plans and diagnostics belong in
`reference/query_order`.
