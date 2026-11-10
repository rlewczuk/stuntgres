# internal/engine/enginetest

Shared test helpers over exported core APIs: sessions, admitted operations,
statement execution, prepared artifacts and results. The package is a test layer,
not a production runtime dependency.

## Contract

- Only external `_test.go` files import it. It imports core, never the root;
  callers pass a composed engine. Helpers use exported APIs, not private state.
- Shared helpers used across destination packages belong here; helpers requiring
  owner-private accessors stay with their tests. Helpers use `testing.TB`.
- Use `NextSessionID` for every test-created session to avoid duplicate IDs.
  Operation helpers return cleanup that releases the operation and ends its
  session; callers own cleanup for error-returning startup helpers.
- Public startup and private Restore startup are distinct fixtures with real
  ownership. Detached catalog fixtures use genuine admitted source transactions;
  publishing a stage and completing that source owner are separate steps.

## Helpers

Common entry points are `Operation`, `Statement`, `Execute`/`ExecuteFrame`,
`Run`, `Describe`, session/transaction helpers, and typed result/diagnostic
helpers. Prepared-fold and catalog-stage helpers retain actual accepted artifacts
for ownership assertions. Consult their declarations for signatures instead of
copying package-specific fixtures here.

Captured query helpers retain their genuine statement owner through release,
installing the description's authorization and dependency manifest before source
capture. Async helpers cancel and join workers on cleanup. Neither an empty
fabricated authority nor SQL result equality substitutes for owner/backing checks.

## Testing

Tests consuming these helpers exercise them; placement is defined by
[docs/TESTING.md](../../../docs/TESTING.md).
