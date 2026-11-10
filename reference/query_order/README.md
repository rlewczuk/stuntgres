# reference/query_order

Dual-mode scenarios for windowing, ordering/bounds, query-level aggregation,
EXPLAIN and standalone ANALYZE. Core query composition belongs to
[query_core](../query_core/README.md#testing); aggregate providers to
[aggregates](../aggregates/README.md#testing) and [numeric](../numeric/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md).

- `TestMain` is `fixture.Main(m)`.
- Helpers: `query_distinct_on_fixture_test.go` (`queryDistinctOnScenario`,
  `queryDistinctOnHeld`) and `query_limit_view_helpers_test.go`
  (`queryLimitHeldBounds`).
- EXPLAIN helpers in `explain_text_test.go` validate owned, one-physical-line-per-row
  `QUERY PLAN`/TEXT results. Literal LF in accepted identifiers crosses SQL row
  boundaries; joining those rows retains the exact represented identifier,
  including empty physical lines. Structured formats preserve the original
  identifier value in one document, not split strings or escaped replacements.
  Assert native logical stages/details and independently valid PostgreSQL plans,
  never bytewise plan parity or fixed PostgreSQL costs.
  Structured helpers require one non-NULL typed document row, nested node/list
  boundaries and numeric/Boolean/string fields. XML is fully decoded with its
  namespace; YAML flow collections use the existing test-only YAML parser.
  Native logical trees and PostgreSQL physical trees have independent expectations;
  neither backend supplies the other's goldens. Analyze execution measurements,
  logical estimates and physical PostgreSQL fields independently.
- Automatic explanation captures complete unlocked driver diagnostics and decodes
  its log envelope separately from SQL EXPLAIN.
- Standalone ANALYZE uses real notices, retained relation facts and public
  EXPLAIN, never an internal collector. Exact native full-data population facts,
  bounded distribution approximations and physical PostgreSQL estimates are
  distinct. Query result invariance remains exact.
- Lock witnesses observe actual requests before release/cancellation, join workers,
  then check unchanged state and healthy recovery.
- Logical native dumps do not transport planner statistics.
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

- Windows: `RUN='^TestReferenceQueryWindow'`
- Ordering: `RUN='^TestReferenceQueryOrder'`
- Bounds: `RUN='^TestReferenceQuery(Limit|Ties)'`
- DISTINCT: `RUN='^TestReferenceQueryDistinct'`
- Grouped dependencies: `RUN='^TestReferenceQueryGroupDependency'`
- EXPLAIN: `RUN='^TestReferenceExplain'`
- Debug formats: `RUN='^TestReferenceOverexplain'`
- Automatic explanation: `RUN='^TestReferenceAutoExplain'`
- ANALYZE: `RUN='^TestReferenceAnalyze'`
