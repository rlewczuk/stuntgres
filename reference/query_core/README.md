# reference/query_core

Core query and routine behavior: SQL routines (CREATE FUNCTION/PROCEDURE with
quoted and SQL-standard bodies, overload resolution against builtins, DROP),
WITH queries including recursive CTEs, CYCLE and SEARCH, subqueries, row
comparison, joins, FROM VALUES, aliases, set operations, CREATE TABLE AS,
expression indexes, parallel SELECT scans, and scalar expressions (BETWEEN,
CASE/COALESCE/NULLIF, logical operators). Related families: routine parameter
modes, defaults, named/variadic calls and OUT/INOUT
(`^TestReferenceRoutineParameters`) in [operators](../operators/README.md);
explicit casts in [type_resolution](../type_resolution/README.md); comparison and
NULL predicates in [types](../types/README.md); windows, ordering, LIMIT and
DISTINCT in [query_order](../query_order/README.md); grouping sets in
[aggregates](../aggregates/README.md).

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
  unconstrained NUMERIC `DecimalSize` as `(65535,65531,true)` (native unknown),
  RECORD/`_RECORD`, internal `"char"` and VOID with `ScanType` `interface {}`
  (native `string`), and RECORD/`_RECORD` cells as `[]byte` (native string; the
  fixture raw-array validator checks each backend's form).
- Create permanent tables before BEGIN in transaction-failure scenarios.
- Parallel scans: matching rows do not prove that workers ran. Concurrent
  evaluation, exact-once ranges and cancellation joins are proven by
  `internal/engine` `TestQueryParallel*` units; reference tests assert occurrence
  bags, settings and lifecycle. A parallel-settings change also needs the
  registry inventory selector in [session_state](../session_state/README.md#testing).
- `TestReferenceConditional(TypeMatrix|NaNAndTies)` and
  `TestReferenceQueryJoins(Scope)?Diagnostics(Oracle)?` carry the legacy
  `reference` build tag and run only with `REFERENCE=1`.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Scalar SQL routines: `RUN='^TestReferenceRoutine(Core|StoredArgumentIdentity|Review)'`
- <a id="ordered-sql-routine-bodies"></a>Ordered and SQL-standard routine bodies, procedures: `RUN='^TestReferenceRoutineSQLBodies'`
- DROP FUNCTION/PROCEDURE: `RUN='^TestReferenceRoutineDrop'`
- SQL overloads beside builtin, window and aggregate providers; rule qualifications: `RUN='^TestReferenceRoutine(Resolution|Window|Nesting|LateChild|Rule)'`
- <a id="select-cte-verification"></a>WITH queries: `RUN='^TestReferenceQueryCTE'`
- <a id="recursive-cte-verification"></a>Recursive CTEs: `RUN='^TestReferenceQueryRecursiveCTE'`
- CYCLE: `RUN='^TestReferenceQueryCycle'`
- SEARCH: `RUN='^TestReferenceQuerySearch'`
- Subqueries: `RUN='^TestReferenceQuerySubquery'`
- Row comparison: `RUN='^TestReferenceRowCompare'`
- Joins: `RUN='^TestReferenceQueryJoins'`
- FROM VALUES: `RUN='^TestReferenceQueryFromValues'`
- Aliases: `RUN='^TestReferenceQueryAliases'`
- Set operations: `RUN='^TestReferenceQuerySetOps'`
- CREATE TABLE AS: `RUN='^TestReferenceQueryTableCreate'`
- Expression indexes: `RUN='^TestReferenceExpressionIndex'`
- Parallel SELECT scans: `RUN='^TestReferenceQueryParallel'`
- Scalar expressions: `RUN='^TestReference(Between|Conditional|Logical|ExpressionCore|QueryCore|Smoke(Between|Compare|Conditional|ExpressionCore|Logical|QueryCore))'`
