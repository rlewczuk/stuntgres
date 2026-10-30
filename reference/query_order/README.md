# reference/query_order

Window functions and frames (ranking, lead/lag, ntile, value functions,
ROWS/RANGE/GROUPS and EXCLUDE), ORDER BY, LIMIT/OFFSET/FETCH including WITH
TIES, DISTINCT and DISTINCT ON, query-level aggregates including
`COUNT(expr)`, and EXPLAIN TEXT/JSON plans, options, nonexecution, actual execution and admission.
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
  JSON helpers independently require one typed JSON document row and structural
  node/list/numeric fields. Native logical trees and PostgreSQL physical trees
  have separate expectations; neither backend supplies the other's goldens.
  Capability tests assert exact native diagnostics separately from PostgreSQL's
  supported formats. ANALYZE cases assert emitted rows/loops, real effects,
  caller rollback, runtime failures and current prepared parameters independently;
  execution measurements and logical estimates are separate expectations.
  `TestReferenceExplainAnalyzeParallelAggregate` independently checks grouped,
  HAVING-empty, empty-global and empty-grouped string aggregates on direct/held
  routes, including exact TEXT values and NULL HAVING acceptance/rejection.
  Identical input strings avoid inventing unordered concatenation order.
  Count companions retain ordinary logical aggregate coverage. Real native
  worker/codec/combine identity uses the partial-capable string provider privately,
  not a demanded PostgreSQL parallel physical plan. Derived/shared-CTE/UNION ALL cases retain exact ordered
  ordinary outputs as well as analyzed counts.
  DML source-RETURNING cases assert source aliases in the RETURNING detail itself,
  including whole rows and correlated subplans. Prepared-admission coverage
  follows stored aliases through rename/replacement and native Dump/Restore,
  retaining independent PostgreSQL SQL-lifecycle expectations.
- Standalone ANALYZE families (`analyze_*_test.go`) are untagged public scenarios.
  They use retained relation facts, real notices and EXPLAIN output, never an
  internal collector or a native `pg_stats` replacement. Independent expected
  rows, types, multiplicities, counts and complete diagnostics precede any
  backend comparison. Native logical estimates and PostgreSQL physical plans
  are inspected separately; PostgreSQL's minimum-one output estimate is not
  imposed on native known-zero estimates.
  The uniform `1..10` range witness independently expects native ranks
  `<5/<=5/>5 = 4/5/5` and observed pinned PostgreSQL histogram estimates
  `3/4/6`. PostgreSQL's sampled-statistics estimator and native full-data rank
  approximation need not agree, even for this small fully collected population.
- ANALYZE distribution witnesses cover uniform, skewed, NULL and empty inputs,
  selected-column refresh and deliberately stale estimates, held prepared
  EXPLAIN, invariant query results, transaction-private column distributions
  versus globally published relation cardinality, savepoints, partial target
  progress, hierarchy/ONLY, materialized snapshots and TEMP ownership.
  Lock tests observe actual requests before cancellation or releasing a blocker;
  successful reuse and unchanged prior estimates are asserted after cancellation.
  Lifecycle tests use public native Reset/Dump/Restore only in their native
  phase; logical dumps intentionally do not transport planner statistics.
- Hierarchy-transition regressions detach or drop the last inheritance child,
  collect again, and independently check physical cardinality, ordinary/ONLY
  estimates, filters and exact rows. Transaction rollback restores the hierarchy
  before a second refresh, and both direct/prepared collection routes are covered.
  TRUNCATE ordering regressions distinguish collection before versus after
  truncation, empty versus reinserted contents, savepoint restoration, and
  COMMIT versus prepared-transaction completion. Successful scan cardinality and
  counters survive rollback of column-statistic deltas; later truncation instead
  invalidates a preceding scan. Unknown PostgreSQL physical estimates are not
  equated to native's logical 1000-row fallback.
  Mixed-rewrite regressions additionally carry a post-truncate scan through a
  later heap rewrite of an unselected column, and prove that an unscanned
  successor cannot acquire native scan authority from copied maintenance.
  Rewrite-before-truncate cases and savepoint rollback/release distinguish the
  retained predecessor from a published later successor. Rows and both column
  types, compatible filtered estimates, cumulative analyze counts and timestamp
  preservation/advancement are independently checked in both completion modes.
  Pinned PostgreSQL can publish unknown `reltuples=-1` after the later rewrite,
  despite native retaining its genuine two-row scan; rewrite-before-scan-before-
  truncate instead retains PostgreSQL's earlier estimate of three. These catalog
  facts are asserted separately. Unknown or physically stale PostgreSQL plans
  require an actual scan and applicable restriction without guessing physical
  estimates from the native logical population.
- Native collected population/NULL/distinct/retained value counts are exact
  full-data facts at capture. Top-100 MCV selection and at most 101 residual
  histogram endpoints are deterministic bounded distribution approximations;
  range interpolation, residual uniformity, independence and displayed logical
  costs are estimates, not PostgreSQL physical measurements. Full
  `pg_statistic`/`pg_stats` readers remain outside these scenarios' ownership.
  Valid BUFFER_USAGE_LIMIT controls do not imply native physical buffers.
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
- Standalone ANALYZE: `RUN='^TestReferenceAnalyze'`
- ANALYZE feature smoke: `RUN='^TestReferenceAnalyzeSmoke$'`
- ANALYZE diagnostics and permissions:
  `RUN='^TestReferenceAnalyze(Diagnostics|Privileges|SyntaxOptions|Verbose)$'`
- ANALYZE data, refresh and invariance:
  `RUN='^TestReferenceAnalyze(Distributions|RefreshColumns|ResultInvariance)$'`
- ANALYZE transaction, lock and lifecycle:
  `RUN='^TestReferenceAnalyze(Transactions|CancellationLocks|Lifecycle|Hierarchy|HierarchyTransitions|TruncateOrdering|TruncateRewriteOrdering)$'`
  Run every changed family both with and without `REFERENCE=1`; quickcheck
  does not replace real pinned PostgreSQL proof. Selected lock/lifecycle race
  proof uses `make check RUN='^TestReferenceAnalyze(CancellationLocks|Lifecycle)$' RACE=1`.
