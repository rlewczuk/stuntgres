# reference/query_order

Window functions and frames (ranking, lead/lag, ntile, value functions,
ROWS/RANGE/GROUPS and EXCLUDE), ORDER BY, LIMIT/OFFSET/FETCH including WITH
TIES, DISTINCT and DISTINCT ON, query-level aggregates including
`COUNT(expr)`, and EXPLAIN text plans, options, nonexecution and admission.
Related families: set operations in
[query_core](../query_core/README.md#testing); grouping sets and
hypothetical-set aggregates in [aggregates](../aggregates/README.md#testing);
SUM providers shared with window SUM (`^TestReferenceNumericSum`) in
[numeric](../numeric/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md).

- `TestMain` is `fixture.Main(m)`.
- Helpers: `query_distinct_on_fixture_test.go` (`queryDistinctOnScenario`,
  `queryDistinctOnHeld`) and `query_limit_view_helpers_test.go`
  (`queryLimitHeldBounds`).
- EXPLAIN helpers in `explain_text_test.go` validate owned, one-line-per-row
  `QUERY PLAN`/TEXT results. Assert native logical stages/details and independently
  valid PostgreSQL plans, never bytewise plan parity or fixed PostgreSQL costs.
  Capability tests assert exact native diagnostics separately from PostgreSQL's
  supported formats; ANALYZE DML runs in rollback transactions.
  DML source-RETURNING cases assert source aliases in the RETURNING detail itself,
  including whole rows and correlated subplans. Prepared-admission coverage
  follows stored aliases through rename/replacement and native Dump/Restore,
  retaining independent PostgreSQL SQL-lifecycle expectations.
- Assert volatile draws as exact sets and counts without assuming the order in
  which separate calls are scheduled.
- Window frame kinds, offsets and sorting share one representation: after
  changing any of them, run the whole `^TestReferenceQueryWindow` family. After
  changing window SUM, also run `^TestReferenceNumericSum`.
- Do not send `MaxInt64 FOLLOWING` frame endpoints to PostgreSQL; moderate
  endpoints such as 100 are portable.
- lib/pq streams rows, so a late runtime error can surface after `QueryContext`
  returns, from `Next` or `Rows.Err`; a native eager query fails at
  `QueryContext` with nil Rows. Assert each independently; a successful
  `QueryContext` alone does not prove the query succeeded.
- `TestReferenceQueryOrder(HiddenVolatileAdmission|GroupedBoundaries|PreparedFrames|Providers|ValuesAndAtomicity|Regclass)`
  and `TestReferenceQueryLimitLockedCache` carry the legacy `reference` build tag
  and run only with `REFERENCE=1`.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- All window functions: `RUN='^TestReferenceQueryWindow'`
- Ranking windows: `RUN='^TestReferenceQueryWindowRank'`
- lead and lag: `RUN='^TestReferenceQueryWindowOffset'`
- ntile: `RUN='^TestReferenceQueryWindowNtile'`
- first/last/nth_value: `RUN='^TestReferenceQueryWindowValue'`
- ROWS, RANGE, GROUPS and EXCLUDE frames: `RUN='^TestReferenceQueryWindow(Rows|Range|Groups|Exclude)'`
- ORDER BY: `RUN='^TestReferenceQueryOrder'`
- LIMIT, OFFSET, FETCH WITH TIES: `RUN='^TestReferenceQuery(Limit|Ties)'`
- DISTINCT and DISTINCT ON: `RUN='^TestReferenceQueryDistinct'`
- DISTINCT ON: `RUN='^TestReferenceQueryDistinctOn'`
- Aggregates and `COUNT(expr)`: `RUN='^TestReference(QueryAggregate|CountExpression)'`
- EXPLAIN: `RUN='^TestReferenceExplain'`
