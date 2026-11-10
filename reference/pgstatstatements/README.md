# reference/pgstatstatements

Public statement-statistics scenarios share the ordinary dual-mode fixture and
one package-owned oracle. `TestMain` opts into `WithPgStatStatements()`; the
existing pinned PostgreSQL container genuinely preloads the module. Each
scenario explicitly installs version 1.12 in `pgss_part`. Tests use only public
SQL, `database/sql` and `reference/fixture`, never internal engine APIs.

The observer's pinned session disables collection. A separate worker supplies
measured statements. New resolved objects and distinct statement shapes isolate
streams; each counter assertion establishes its matching baseline before work.
Cluster history is not assumed empty. Reset scenarios explicitly assert each
destructive setup/result; other families isolate shapes without using SQL reset
as an unasserted setup helper. All borrowers close before extension/schema/role
teardown. PostgreSQL scenarios run serially because their oracle is shared.

Expectations come from released SQL, independently of backend metadata.
Check generated identities relationally and keep native logical estimates
separate from physical PostgreSQL measurements. Native controls and protocol
consumers use the shared fixture ownership rules.

## Testing

`pgstatstatements_fixture_test.go` owns the shared row contract, isolated
observer/worker sessions and direct/prepared readers. Protocol consumers use the
existing real server fixture and public frontend messages, not collector mocks.
Expectations are independently specified; the backend loop is unfiltered.

Follow [docs/TESTING.md](../../docs/TESTING.md).
Family selector: `RUN='^TestReferencePgStatStatements'`;
add `REFERENCE=1` for the oracle.
