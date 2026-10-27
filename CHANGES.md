# Changes

- Make regexp statement-timeout reference coverage independent of regex output
  budgets and execution speed: retain all six direct/prepared commands and the
  10 ms timer behind a durable relation blocker, with separate strict finite
  admission/release witnesses, exact timeout diagnostics, rollback and
  same-physical-owner recovery. Actual provider-cancellation coverage and
  internal resource-limit tests remain unchanged.
