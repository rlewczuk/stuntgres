# Statistics reference scenarios

Public dual-mode scenarios for cumulative cluster, relation, index, function and
subscription statistics, and transaction-local statistics views.
Tests use public stuntgres and `reference/fixture` APIs; native controls remain
explicit phases, not simulated PostgreSQL control commands.

## Fixture contracts

- Common lifecycle bodies live in `fixture/statistics_helpers.go`; family-local
  row/expectation helpers retain independently specified metadata, values and
  full diagnostics across direct, prepared, empty and retained results.
- Counter assertions need actual access ownership: authenticate the executed
  plan, not just output cardinality or launch settings. Prepare and EXPLAIN alone
  do not establish execution events.
- Exact native logical counts and absent-physical-facility estimates are distinct
  from independently bounded PostgreSQL physical measurements. Timings are not
  cross-backend wall-clock goldens.
- Reader none/cache/snapshot cuts and writer publication are separate.
  Snapshot clear discards a reader cut; it does not flush pending work.
  Physical reporter retirement is a real terminal boundary, not a new SQL flush API.
  Throttled pending work can cross transaction ends.
- Native controls rebuild definitions/data without serialized statistics history.
  Retained TEMP state and peer history remain destination-owned. Replay actual
  emitted SQL on PostgreSQL using independent roles and recorded destination pools.
- Restore raw ACL absence/empty/grantor/options distinctly. Remove actual grant
  dependencies before repairing stock authority and tearing down roles.
- Multirow results use bags or ORDER BY. Timestamp equality compares instants
  and NULL validity, never Go time-zone pointer identity.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md).
Use `make check RUN='...'`, adding `REFERENCE=1` for the oracle.

- Cluster views: `RUN='^TestReferencePGStat(Archiver|WAL|BGWriter|Checkpointer)'`
- Relation/index views: `RUN='^TestReferencePGStat(All|Sys|User)(Tables|Indexes)'`
- Function views: `RUN='^TestReferencePGStatUserFunctions'`
- Subscription views: `RUN='^TestReferencePGStatSubscription'`
- Transaction-local views: `RUN='^TestReferencePGStatXact'`

Shared-helper changes also affect the originating
[session_state](../session_state/README.md#testing) snapshot/reporting consumers.
