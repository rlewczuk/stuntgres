# reference/catalogs

`pg_catalog` relation and type catalogs: `pg_class`, `pg_attribute`, `pg_attrdef`,
`pg_statistic_ext` (typed empty definitions; CREATE STATISTICS owns population),
`pg_type`, `pg_enum`, `pg_range`, `pg_constraint`, `pg_inherits`, partition-navigation
inquiry functions, `pg_indexes`, `pg_tables`/`pg_views`/`pg_matviews`, `pg_rules`,
`pg_depend`, `pg_sequence`/`pg_sequences`, shared-memory allocation/NUMA views,
`pg_wait_events`, catalog foreign keys and the catalog
controls migrated from the root package. The other catalog packages:
namespaces, schemas, `search_path`, visibility inquiries, operator, cast,
aggregate, access-method, collation, language, transform, text-search,
large-object and comment catalogs in `reference/catalog_objects`; role, database
and ACL catalogs, tablespaces, VACUUM, size functions and the information_schema
grant, key, constraint and SQL metadata views in `reference/catalog_privileges`;
the remaining `information_schema` views in `reference/information_schema`;
extensions, LOAD, pgcrypto, uuid-ossp, fuzzystrmatch and encoding conversions in
`reference/extensions`. Related: object-address functions in
`reference/object_address`; access-method and operator-class DDL in
`reference/indexes`; reg* alias input in `reference/type_resolution`; timezone
views in `reference/datetime`; sequence DDL in `reference/sequences`;
large-object functions in `reference/object_grants`; uuid-ossp binary COPY in
`reference/dml`.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` is
`fixture.Main(m, fixture.WithLegacyCryptoProvider())`; this package and the other
catalog packages listed above are the only ones whose oracle loads the legacy
OpenSSL provider
([fixture README](../fixture/README.md#test-only-legacy-crypto-oracle)).
The rules below apply to every catalog package.

Extended-statistics definition reads cover exact schema and row/array links,
PUBLIC access and current ACL revocation, ordinary-role mutation diagnostics,
and native controls without privileged PostgreSQL catalog writes. Select the
family in either native or real-reference mode with
`make check RUN='^TestReferencePGStatisticExt'` (add `REFERENCE=1` for PostgreSQL).
Collected statistics and nonempty definition lifecycle are separately owned.

Type identity scenarios retain attribute privilege identities across rename and
retire obsolete lookup names.

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
  Pseudo-type declaration identities and their canonical companions are a finite
  metadata inventory; catalog presence grants no storage or codec authority.
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
- Catalog bags are database-wide: scope them with scenario-unique names, and
  filter by OID or name in the first join or a derived relation, so the
  30-second scenario budget never depends on optimizer pushdown. Unordered
  scans are bags.
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
- Pristine builtin expectations (`TestReferencePGNamespaceBuiltins` and the
  first-use TEMP transaction witness) read the oracle through `fixture.DSN` with
  the database path `/postgres`, read-only, never the reset fixture's rebuilt
  `public`.
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
- Native Dump preserves supported raw relation, column, routine, namespace,
  type, parameter and tablespace ACLs, preserving ordered grantor/options and
  sparse parameter identities by destination name.
  Provision every named owner, grantor and grantee before logical Restore.
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

- Relations and attributes: `RUN='^TestReferencePgClass'`, `RUN='^TestReferencePGAttribute'`, `RUN='^TestReferenceAttrdef'`
- Types, enums, ranges: `RUN='^TestReferencePGType'`, `RUN='^TestReferencePGEnum'`, `RUN='^TestReferencePGRange'`
- Dependencies: `RUN='^TestReferencePGDepend'`
  `TestReferencePGDependExpressionEdges` includes ordinary view `tableoid`
  references in both modes: the rule's internal view edge and the source table's
  normal `-6` attribute edge, without a redundant whole-table reference.
- Constraints, inheritance, partition navigation: `RUN='^TestReferencePGConstraint'`, `RUN='^TestReferencePGInherits'`, `RUN='^TestReferencePartitionInfo'`
- Table, view and index summaries: `RUN='^TestReferencePGTableViews'`, `RUN='^TestReferencePGIndexes'`
  (`TestReferencePGTableViewsBuiltins` lives in `reference/information_schema`)
- Rule summaries: `RUN='^TestReferencePGRules'`
- `pg_sequence`, `pg_sequences`: `RUN='^TestReferencePGSequence'`
- Shared-memory allocation projections: `RUN='^TestReferencePGShmem'`
- Wait-event definitions: `RUN='^TestReferencePGWaitEvents'`
- Catalog foreign keys, `pg_get_userbyid`: `RUN='^TestReferenceCatalogRelations'`
- Catalog and control scenarios migrated from the root package:
  `RUN='^TestReference(CatalogCore|CatalogControls|Control(Failed|Snapshot)|TransactionNativeControls|Smoke)'`
  (the `Smoke` scenarios live in `reference/catalog_objects`)
- Engine units use the family names without `Reference`, for example
  `make check RUN='^TestPGDepend'` or `make check RUN='^TestPGOperator'`.
