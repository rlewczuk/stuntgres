# reference/aggregates

Aggregate families: `CREATE AGGREGATE`; aggregate modifiers (DISTINCT, local
ORDER BY, FILTER); `any_value`; `min`/`max`; GROUPING SETS; `num_nulls`/`num_nonnulls`;
variance, standard deviation and regression aggregates; hypothetical-set and
ordered-set aggregates; SUM over MONEY and INTERVAL; JSON/JSONB object and array
aggregates and SQL/JSON `JSON_OBJECTAGG`/`JSON_ARRAYAGG`; bitwise and range
aggregates. Related families elsewhere: numeric SUM/AVG in `reference/numeric`,
INTERVAL AVG in `reference/temporal_types`, `array_agg` and `intagg` in
`reference/binary_json_arrays`, `string_agg` in `reference/strings`, Boolean
aggregates and multiranges in `reference/types`, count expressions in
`reference/query_order`.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses the default
`fixture.Main(m)`.

- **Helpers.** Per-family `*_helpers_test.go` files (`aggregate_modifiers`,
  `bitwise_aggregate`, `create_aggregate_remap`, `hypothetical`,
  `json_array_aggregate`, `json_object_aggregate`, `json_object_unique_aggregate`,
  `money_interval_sum`, `ordered_set`, `ordered_set_cancel`, `range_aggregate`,
  `sqljson_object_aggregate`).
- **Catalog probes.** CREATE AGGREGATE lifecycle checks select the owning
  procedure in a filtered derived source before catalog joins, so they never
  join complete procedure inventories within the 30-second scenario.
- **Floating results.** Results that depend on accumulation order use named SQL
  interval witnesses such as `abs(actual-expected) <= 1e-12*max(1,abs(expected))`;
  every other cell, count, metadata and diagnostic stays exact. Do not add a
  global approximate comparator.
- **Late-group atomicity.** To prove a later group's failure publishes nothing on
  PostgreSQL, force a blocking sort (ORDER BY the aggregate result, then the key)
  so no cursor rows are sent before the error.
- **Ordered-set active cancellation** (`TestReferenceOrderedSetAggregateActiveCancellation`,
  `ordered_set_cancel_helpers_test.go`):
  - Each direct/prepared route creates its own fixture. The 30-second scenario and
    15-second admission deadlines are never refreshed within a route.
  - Native admission requires the executing worker's exact goroutine with a live
    `exec.sortOrderedAggregate` frame in a complete all-goroutine capture; a
    truncated buffer never admits (it grows, then waits like a miss).
  - Native cancellation returns nil Rows/Result/Stmt and `context.Canceled`.
    Cleanup shares one 5-second join with normal cancellation handling.
  - Recovery reuses the same held statement with a small input instead of
    repeating the large sort.
- **Other cancellation.** Hypothetical-set cancellation admits on
  `exec.openAggregateNode` through `fixture.BinaryConversionActiveCancel`; sort
  phases are covered by private units. SQL/JSON object aggregation places a
  `pg_sleep` inside the aggregated value and uses `fixture.SleepActiveCancel`;
  transition and sort cancellation belong to the internal
  `TestSQLJSONObjectAggregateReachedCancellation` unit, not public stack sampling.
- **Frame names matched by tests.** `exec.sortOrderedAggregate`,
  `exec.openAggregateNode`. Update the probes when renaming them.
- **Client metadata.** The pinned lib/pq OID map lacks some types (for example
  REGCOLLATION and XID8): assert their pq database type name separately from the
  native name.

Add `REFERENCE=1` for the oracle.

- <a id="create-aggregate-contract"></a>CREATE AGGREGATE: `RUN='^TestReferenceCreateAggregate'`
- The transactional routine/operator/aggregate matrix lives in
  [query_core](../query_core/README.md#testing), reusing its held-handle routes.
  Aggregate results keep their explicit native nullable/known metadata contract
  rather than inheriting scalar SQL-function metadata.
- DISTINCT, local ORDER BY, FILTER: `RUN='^TestReferenceAggregateModifiers'`
- `any_value`: `RUN='^TestReferenceAnyValue'`
- `min`, `max`: `RUN='^TestReferenceExtrema'`
- GROUPING SETS, ROLLUP, CUBE: `RUN='^TestReferenceGroupingSets'`
- `num_nulls`, `num_nonnulls`: `RUN='^TestReferenceNullCount'`
- Variance, stddev, `regr_slope`/`regr_s*`: `RUN='^TestReferenceStatisticalAggregate'`
- `corr`, `covar_*`, remaining `regr_*`: `RUN='^TestReferenceCorrelationRegression'`
- Hypothetical-set (`rank`, `dense_rank`, `percent_rank`, `cume_dist`): `RUN='^TestReferenceHypotheticalAggregate'`
- Ordered-set (`percentile_cont`, `percentile_disc`, `mode`): `RUN='^TestReferenceOrderedSetAggregate'`
- SUM over MONEY and INTERVAL: `RUN='^TestReferenceMoneyIntervalSum'`
- `json[b]_object_agg[_strict]`: `RUN='^TestReferenceJSONObjectAggregate'`
- `json[b]_object_agg_unique[_strict]`: `RUN='^TestReferenceJSONObjectUniqueAggregate'`
- `json[b]_agg[_strict]`, `json_arrayagg`: `RUN='^TestReferenceJSONArrayAggregate'`
- `JSON_OBJECTAGG` and SQL/JSON aggregate output encoding: `RUN='^TestReferenceSQLJSON(Object)?Aggregate'`
- `bit_and`, `bit_or`, `bit_xor`: `RUN='^TestReferenceBitwiseAggregate'`
- `range_agg`, `range_intersect_agg`: `RUN='^TestReferenceRangeAggregate'`
