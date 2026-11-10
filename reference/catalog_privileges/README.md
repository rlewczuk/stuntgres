# reference/catalog_privileges

Dual-mode scenarios for role, database and privilege catalogs, information-schema
grant metadata, tablespaces and maintenance utilities. Other catalog ownership
boundaries and shared rules are in [catalogs](../catalogs/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md) and the shared catalog rules.
`TestMain` uses `fixture.Main(m, fixture.WithLegacyCryptoProvider())`.
Family scenario/assertion helpers live in `*_fixture_test.go` or `*_helpers_test.go`.

- Expected inventories are independent released facts. SQL feature metadata uses
  the untouched REL_18_6 inventory in `testdata/sql_features_pg18.tsv`.
- Restore original PostgreSQL catalog ACLs on success and failure, using
  transaction/savepoint rollback and independently bounded cleanup.
- Password/verifier comparisons use unrecorded raw callbacks and redacted
  failures. Never expose credentials in recorded SQL or assertion output.
- Tablespace directory scenarios own real container directories; restore modes
  before relation, tablespace and directory cleanup. Drop cluster objects explicitly.
- Retire database/tablespace grants and owned objects before dropping their roles.
  Schema reset alone does not remove shared authority.
- PostgreSQL cumulative statistics need a writer flush before a measured baseline,
  followed by bounded fresh reads and independent expectations. VERBOSE INFO text
  is not an expected result.
- Native logical counters and PostgreSQL physical maintenance values are distinct.
  Progress claims require observed executing workers, not configured costs or delays.
  Retained streaming PostgreSQL rows must be consumed before reusing their connection.
- Native replacement phases use unwrapped public control pools and recorded
  destination SQL pools; eager native snapshots do not imply PostgreSQL retention.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Databases: `RUN='^TestReferenceDatabase'`
- Roles: `RUN='^TestReferencePG(Auth|Roles|User|Shadow|Group)'`
- ACL catalogs: `RUN='^TestReferencePG(DefaultACL|InitPrivs|Shdepend|ParameterACL)'`
- Information schema: `RUN='^TestReferenceInfo'`
- Tablespaces: `RUN='^TestReferenceTablespace'`
- Maintenance: `RUN='^TestReference(Vacuum|AnalyzeProgress)'`
