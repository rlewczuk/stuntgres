# Changes

- Complete `pg_statistic_ext_data` reader/security acceptance with exact
  six-column metadata, definition/population identity, diagnostics and current
  catalog SELECT checks for direct and retained prepared reads, even WHERE FALSE.
  Reject DATA runtimes paired with a missing or foreign backing engine while
  preserving transaction-private catalog capture. Reuse the existing ANALYZE
  collector; only its deterministic full-data ndistinct metric differs from a
  potentially sampled PostgreSQL estimator.

- Collect genuine multivariate ndistinct tuples during ANALYZE, including NULL
  groups and canonical subsets, with isolated object targets and inheritance
  flavors. Publish immutable typed facts through the existing metadata Journal.
  Register restricted six-column pg_statistic_ext_data with exact NULL-only
  pg_statistic row/array descriptions. Native Dump/Restore preserves checked
  stale/zero/absent state without recomputation and retains genuine TEMP facts,
  reconstructing retained payloads with the replacement's current type authority.

- Add installed `hstore(record)`, `populate_record` and `#=` with typed composite
  replacement, destination typmods, precise NULL semantics and preserved stored
  provider identities through native Dump/Restore.

- Implement CREATE/DROP STATISTICS for simple multicolumn definitions, with
  immutable keys/kinds, ownership, automatic column/relation dependencies and
  populated pg_statistic_ext rows. Native controls preserve definitions and
  sparse attribute identities. Internal pg_ndistinct, pg_dependencies and
  pg_mcv_list have concrete typed NULLs and genuine output/send codecs while
  rejecting non-NULL input; MCV payload validation respects released by-value
  versus by-reference allocation extents. Analyzed-data collection remains
  independently owned.

- Register PUBLIC-readable `pg_statistic_ext` with its exact nine definition
  columns, authentic row/array carriers, attribute nullability and catalog
  index/table summaries. CREATE STATISTICS populates definitions;
  ANALYZE metrics never manufacture definitions.
  Preserve current ACL checks, guarded unsupported native mutations and
  Reset/Dump/Restore metadata ownership.

- Add standalone ANALYZE collection, retained typed column distributions and
  logical EXPLAIN selectivity, with selected-column staleness, maintenance locks,
  privilege notices and transactional column publication distinct from
  nontransactional relation estimates. Native collection is full-data and bounded
  distribution/selectivity summaries are explicitly approximate.
  Hierarchy refresh handles last-child removal; native replacement preserves
  genuinely retained TEMP facts while dropping reminted metadata; private
  TRUNCATE/ANALYZE ordering composes with savepoints and prepared completion.

- Make regexp statement-timeout reference coverage independent of regex output
  budgets and execution speed: retain all six direct/prepared commands and the
  10 ms timer behind a durable relation blocker, with separate strict finite
  admission/release witnesses, exact timeout diagnostics, rollback and
  same-physical-owner recovery. Actual provider-cancellation coverage and
  internal resource-limit tests remain unchanged.
