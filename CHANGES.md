# Changes

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
