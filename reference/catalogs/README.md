# reference/catalogs

Dual-mode scenarios for relation/type catalogs, catalog dependencies and
database-local or shared metadata lifetimes. Neighboring owners:

- [catalog_objects](../catalog_objects/README.md): schema-object catalogs.
- [catalog_privileges](../catalog_privileges/README.md): role/database/ACL catalogs
  and maintenance utilities.
- [information_schema](../information_schema/README.md): object metadata views.
- [extensions](../extensions/README.md): extension registry and loadable-code boundaries.
- [inheritance](../inheritance/README.md): inheritance links and optimizer statistics.
- [triggers](../triggers/README.md): executable/event-trigger catalogs.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` is
`fixture.Main(m, fixture.WithLegacyCryptoProvider())`; this package and the other
catalog packages listed above are the only ones whose oracle loads the legacy
OpenSSL provider
([fixture README](../fixture/README.md#test-only-legacy-crypto-oracle)).
The rules below apply to every catalog package.


- Helpers: family scenario and assertion helpers live in the family's
  `*_fixture_test.go` or `*_helpers_test.go` files (for example
  `partitionInfoScenario`). Column vectors and helpers shared between catalog
  packages live in `reference/fixture` (`catalog_columns.go`,
  `pg_table_views_helpers.go`, `pg_catalog_helpers.go`, for example
  `fixture.CatalogRelationsMetadata`).
- Expected data comes from released inventories and published vectors, never
  from native output: literal REL_18 catalog tuples in the inventories. Admitting
  a type changes the shared TSV and every consumer, including
  `TestReferencePGTypeAdmission`.
- Native inventory reads stay unfiltered, so extra native rows fail; PostgreSQL
  reads restrict to the supported candidate identities, because the oracle
  legitimately has more rows. Never assert whole-cluster row-count parity.
- Do not add unowned SQL catalogs or catalog projections just to make a
  discovery query run.
- Resolve identities through an independent authority (qualified `::regclass` or
  `::regtype`, or a separately owned helper relation's `pg_class.relowner` and
  `relnamespace`), never through the catalog under test. Check generated OIDs
  relationally (positive, distinct, reciprocal row/array links), never against
  an allocator; Reset/Restore may reuse numeric OIDs. Bootstrap role 10 is
  `postgres` natively and `stuntgres` on the oracle (`objectACLOwner`); other
  roles use their queried identities.
- Scope database-wide bags with scenario-unique names and filter before joining,
  so bounded scenario work does not depend on optimizer pushdown. Unordered scans
  are bags.
- State outside `public` survives the reset and needs its own cleanup:
  extensions (`pgcryptoCase` drops the extension, its schema and owner role),
  cast declarations between builtin types (pair-owned cleanup), database owner
  and ACL (restore the captured owner and nullable ACL through the admin
  fixture after borrowers close, before dropping roles), parameter ACL entries
  and roles (real REVOKE/DROP OWNED, never catalog DML), renamed system schemas
  such as `information_schema` (guaranteed restore), and the hidden conversion
  default (repair registered before mutation, under its own bounded context).
  PostgreSQL-only catalog GRANT/REVOKE phases restore catalog ACLs on success
  and failure.
  Register `RoleReferenceCleanup`, then `DatabaseReferenceCleanup`, then the
  scenario, appending names before creation attempts. Teardown closes selected
  pools and workers before standalone PostgreSQL DROP DATABASE, then role cleanup.
- Read pristine builtin expectations through the read-only `fixture.DSN` database
  path `/postgres`, not the reset fixture's rebuilt `public`.
- PostgreSQL TEMP retirement: close the owner's lease and pool, then wait on a
  real exit barrier (an advisory lock the owner held,
  `fixture.ExtensionTempExitBarrier`, or its backend leaving
  `pg_stat_activity`) before asserting absence. Never poll or treat socket close
  as backend exit.
- Native controls run on the scenario's unwrapped control pool and connector,
  never through middleware. PostgreSQL replay phases execute the actual
  native-emitted Dump bytes unchanged and fail if the dump is empty; never
  substitute handwritten SQL. Prove replay through restored values and
  identities, not dump spelling (quoting, deparse formatting).
- Provision every named owner, grantor and grantee before logical Restore.
- Keep password setup out of recorders and failure output.
- Client quirks: drain and close a PostgreSQL cursor before issuing another
  command on its physical connection; pq parses FLOAT4 shortest-decimal text
  into float64, so compare `pg_enum.enumsortorder` after rounding to binary32;
  TEXT reports `Length() = (math.MaxInt64, true)` on both adapters; bound OID
  input errors carry Context `unnamed portal parameter $1 = '...'` on both.
  OID result rows use `fixture.OidReferenceRows` to validate pq decimal bytes
  versus native int64 and normalize exact uint32 identities without changing
  the expected OID column types or values.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Relations and attributes: `RUN='^TestReference(PgClass|PGAttribute|Attrdef)'`
- Types: `RUN='^TestReferencePG(Type|Enum|Range)'`
- Routines: `RUN='^TestReferencePGProc'`
- Dependencies: `RUN='^TestReferencePGDepend'`
- Constraints: `RUN='^TestReferencePGConstraint'`
- Publications: `RUN='^TestReference(PGPublication|Publication)'`
- Subscriptions: `RUN='^TestReference(PGSubscription|Subscription)'`
- Replication origins: `RUN='^TestReference(PGReplicationOrigin|ReplicationOrigin)'`

Tree carriers are a representation boundary: PostgreSQL returns nodeToString
trees; native returns immutable structural expressions and checked programs.
Inspect raw carriers independently and assert semantic deparse/execution separately.
Catalog metadata does not authorize storage, codecs or executable callbacks.
