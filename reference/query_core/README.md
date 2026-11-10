# reference/query_core

Dual-mode scenarios for core query composition, scalar expressions and SQL
routine lifecycle. Routine parameters belong to
[operators](../operators/README.md#testing), explicit casts to
[type_resolution](../type_resolution/README.md#testing), windows/order/bounds to
[query_order](../query_order/README.md#testing), and session-owned CTAS to
[ddl](../ddl/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md).

- `TestMain` is `fixture.Main(m)`.
- Helpers: `routine_helpers_test.go` (`routineScenario`, `routineValue`,
  `routineScalarMetadata`), `routine_drop_helpers_test.go`,
  `query_cycle_helpers_test.go`, `query_recursive_cte_helpers_test.go`,
  `query_search_helpers_test.go`, `query_subquery_helpers_test.go` and
  `query_parallel_helpers_test.go`.
- **Persistence.** Routine, view and set-operation persistence phases run native
  Dump/Reset/Restore; in real mode they replay the exact native Dump stream on
  PostgreSQL, never reconstructed SQL.
- **Metadata.** Native computed results, including scalar SQL calls, report
  unknown nullability, while stored columns and `COUNT` report it as known; use
  `routineValueWithNativeNullability` where a stored column is projected.
  Client quirks asserted here: lib/pq reports unbounded VARCHAR `Length` as
  `(-5,true)` (native `(MaxInt64,true)`; TEXT is `(MaxInt64,true)` on both),
  unconstrained NUMERIC and `_NUMERIC` `DecimalSize` as `(65535,65531,true)`
  (native unknown), RECORD/`_RECORD`, internal `"char"`, CSTRING and VOID with
  `ScanType` `interface {}` (native `string`), and their cells as `[]byte`
  (native string). Its OID inventory omits multiranges and the query-only
  compatible array/range identities: their database type names are empty, while
  raw transport and logical result usages are asserted separately.
- Create committed predecessors before BEGIN when testing restoration; private
  source/table chains construct their definitions inside the owner's block.
- Parallel scans: matching rows do not prove that workers ran. Concurrent
  evaluation, exact-once ranges and cancellation joins are proven by
  `internal/engine` `TestQueryParallel*` units; reference tests assert occurrence
  bags, settings and lifecycle. A parallel-settings change also needs the
  registry inventory selector in [session_state](../session_state/README.md#testing).
- `TestReferenceConditional(TypeMatrix|NaNAndTies)` and
  `TestReferenceQueryJoins(Scope)?Diagnostics(Oracle)?` carry the legacy
  `reference` build tag and run only with `REFERENCE=1`.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Query composition: `RUN='^TestReferenceQuery'`
- SQL routines: `RUN='^TestReferenceRoutine'`
- Transactional routines: `RUN='^TestReferenceTransactionalRoutine'`
- Scalar expressions: `RUN='^TestReference(Between|Conditional|Logical|ExpressionCore)'`
