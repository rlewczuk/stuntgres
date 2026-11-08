# reference/expr

Dual-mode tests for `ARRAY(SELECT ...)` subquery expressions: values, wire
metadata, column naming, DML consumers, diagnostics, extended-protocol
parameters and stored view/rule deparse round-trips.

Related families: scalar and `EXISTS` subqueries in
[query_core](../query_core/README.md#testing); array constructors, subscripts,
`unnest` and array display in [cursors](../cursors/README.md#testing);
`UPDATE`/`INSERT` consumers in [dml](../dml/README.md#testing); stored view
definition replay in [views](../views/README.md#testing).

`ARRAY[...]` constructors and `unnest` are out of scope here: they already have
coverage and this family exercises only the `ARRAY(subquery)` form.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` is plain
`fixture.Main(m)`. Scenario, prepared-route and pinned-session helpers live in
`array_subquery_helpers_test.go`; array results use
`fixture.ArrayReferenceSelect` for raw codec, `ScanType` and `Length`
assertions, and prepared execution is never used for DML.

- `ARRAY(SELECT ...)` expressions: `make check RUN='^TestReferenceExprArraySubquery'`
  (add `REFERENCE=1` for the oracle).