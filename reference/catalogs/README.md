# reference/catalogs

System catalogs and catalog-facing features: `pg_catalog` tables and views
(`pg_class`, `pg_attribute`, `pg_type`, `pg_namespace`, `pg_depend`, `pg_operator`,
`pg_aggregate`, `pg_cast`, ACL, comment, role, sequence and summary views, and
others), `information_schema` views, catalog foreign keys, visibility, ACL and
partition-navigation inquiry functions, size functions, schemas and
`search_path`, encoding conversions, tablespaces, VACUUM, LOAD, and extensions
(registry, installation, TEMP installs, versions, removal, available-extension
views, pgcrypto, uuid-ossp and fuzzystrmatch). Related: object-address
functions in `reference/object_address`; access-method and operator-class DDL in
`reference/indexes`; reg* alias input in `reference/type_resolution`; timezone
views in `reference/datetime`; sequence DDL in `reference/sequences`;
large-object functions in `reference/object_grants`; uuid-ossp binary COPY in
`reference/dml`.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` is
`fixture.Main(m, fixture.WithLegacyCryptoProvider())`; this is the only package
whose oracle loads the legacy OpenSSL provider
([fixture README](../fixture/README.md#test-only-legacy-crypto-oracle)).

Type identity scenarios retain attribute privilege identities across rename and
retire obsolete lookup names. Namespace controls replay nominal declarations and
exercise owner-authorized type removal.

- Helpers: family scenario and assertion helpers live in the family's
  `*_fixture_test.go` or `*_helpers_test.go` files (for example
  `vacuumScenario`, `partitionInfoScenario`, `pgcryptoCase`, `uuidOsspCase`,
  `objectACLScenario`, `catalogRelationsMetadata`, `tablespaceContainer`).
- Expected data comes from released inventories and published vectors, never
  from native output: `docs/design/pg-operator-pg18-supported.tsv` (shared by
  every `TestReferencePGOperator` test, `TestReferenceLsegOperatorCatalog` and
  the engine unit `TestPGOperatorCaptureOwnership`),
  `docs/design/regoperator-pg18-inventory.tsv`, the explicit 144×22 fixture in
  `pg_aggregate_fixture_test.go`, literal REL_18 catalog tuples in other
  inventories, and RFC/NIST/Openwall/PostgreSQL regression vectors for pgcrypto,
  uuid-ossp and fuzzystrmatch. Admitting a type or operator changes the shared
  TSV and every consumer, including `TestReferencePGTypeAdmission`.
  `TestReferenceGeometryRelationOperatorCatalog` restricts these shared facts by
  selected spellings and geometric formals, independently asserts all15 fields
  and symbolic procedure links, and couples required rows to typed-column
  execution. Catalog presence alone does not grant named-call admission.
  `TestReferenceGeometryPositionOperatorCatalog` independently specifies all32
  released position tuples and procedure links and couples each OID to actual
  typed-column true/false/NULL BOOL execution. It complements the shared TSV
  consumers rather than deriving expectations from native admission.
  Boundary scenarios distinguish released lookup identities from executable
  rows: POINT-distance517 is an exact admitted control alongside TEXT/RECORD,
  while unsupported pattern-order and FTS aliases remain negative controls.
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
  real exit barrier (an advisory lock the owner held, `extensionTempExitBarrier`,
  or its backend leaving `pg_stat_activity`) before asserting absence. Never
  poll or treat socket close as backend exit.
- Native controls run on the scenario's unwrapped control pool and connector,
  never through middleware. PostgreSQL replay phases execute the actual
  native-emitted Dump bytes unchanged and fail if the dump is empty; never
  substitute handwritten SQL. Prove replay through restored values and
  identities, not dump spelling (quoting, deparse formatting).
- Native Dump rejects explicit relation, column, routine and parameter ACLs
  before writing: run Dump/Reset/Restore phases before explicit GRANT/REVOKE
  phases, or assert the rejection.
- Transactional comments share the seven-kind inventory and setup in
  `transactional_comment_helpers_test.go`, with direct/held SQL BEGIN and Go
  BeginTx routes. Keep literal, diagnostic and lifecycle assertions together
  when changing this family. Captured identity never retargets after a wait:
  replacement is fenced without changing the recreated object's text.
  Role DROP/recreate must not inherit text; this family does not add
  role-renaming SQL.
- PostgreSQL cumulative statistics: flush the writer's pending statistics with
  `pg_stat_force_next_flush()` before taking a baseline, then read with bounded
  fresh-read polling and exact equality (`vacuumStatistic`). PostgreSQL VERBOSE
  INFO text is not an expectation.
- LOAD (`load_fixture_test.go`) checks missing-file candidates inside the oracle
  container only and requires `dynamic_library_path=$libdir` and an installed
  `plpgsql.so`; unexpected files are failed preconditions, never skips.
- Tablespace directory scenarios use a real container-owned directory
  (`tablespaceContainer`), restore permissions before relation, tablespace and
  directory cleanup, and drop cluster objects explicitly.
- Keep password setup out of recorders and failure output.
- Client quirks: drain and close a PostgreSQL cursor before issuing another
  command on its physical connection; pq parses FLOAT4 shortest-decimal text
  into float64, so compare `pg_enum.enumsortorder` after rounding to binary32;
  TEXT reports `Length() = (math.MaxInt64, true)` on both adapters; bound OID
  input errors carry Context `unnamed portal parameter $1 = '...'` on both.
- FIPS mode needs one process per mode:
  `env GODEBUG=fips140=off make check RUN='^TestReferencePgcryptoRandomFIPSMode$'`,
  then the same with `fips140=on`.
- Frame names matched by tests (active-cancel admission): `sqltype.binaryEncodingPass`,
  `pgcryptoSHA` and `pgcryptoBcrypt` in `internal/sqltype`, and
  `(*relationRequest).wait` in `internal/engine/lock`. Update the tests when
  renaming them.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Relations and attributes: `RUN='^TestReferencePgClass'`, `RUN='^TestReferencePGAttribute'`, `RUN='^TestReferenceAttrdef'`
- Types, enums, ranges: `RUN='^TestReferencePGType'`, `RUN='^TestReferencePGEnum'`, `RUN='^TestReferencePGRange'`
- Namespaces, schemas, `search_path`: `RUN='^TestReferencePGNamespace'`, `RUN='^TestReferenceSchema'`
- Databases and role settings: `RUN='^TestReferencePGDatabase'`, `RUN='^TestReferencePGDBRoleSetting'`
- Role views: `RUN='^TestReferencePGRoles'`, `RUN='^TestReferencePGUser'`, `RUN='^TestReferencePGGroup'`
- Database creation, selection and targeted controls: `RUN='^TestReferenceDatabaseCreate'`
- ACL catalogs and `pg_get_acl`: `RUN='^TestReferencePGDefaultACL'`, `RUN='^TestReferencePGParameterACL'`, `RUN='^TestReferenceObjectACL'`
- Comments: `RUN='^TestReferencePGDescription'`, `RUN='^TestReferencePGShDescription'`, `RUN='^TestReferenceComment'`
- Comment transactions: `RUN='^TestReferenceTransactionalComment'`
- Dependencies: `RUN='^TestReferencePGDepend'`
- Constraints, inheritance, partition navigation: `RUN='^TestReferencePGConstraint'`, `RUN='^TestReferencePGInherits'`, `RUN='^TestReferencePartitionInfo'`
- Table, view and index summaries: `RUN='^TestReferencePGTableViews'`, `RUN='^TestReferencePGIndexes'`
- Rule summaries: `RUN='^TestReferencePGRules'`
- Access methods and operator classes: `RUN='^TestReferencePGAM'`, `RUN='^TestReferencePGOpclass'`
- Operators: `RUN='^TestReferencePGOperator'`, `RUN='^TestReferenceLsegOperatorCatalog$'`
- Aggregates: `RUN='^TestReferencePGAggregate'`
- <a id="geometric-relation-operator-catalog-proof"></a>Geometric relation catalogs: `RUN='^TestReferenceGeometryRelationOperatorCatalog$'`
- Geometric position catalogs: `RUN='^TestReferenceGeometryPositionOperatorCatalog$'`
- Casts: `RUN='^TestReferencePGCast'`
- Collations, languages, transforms, text search: `RUN='^TestReferencePGCollation'`, `RUN='^TestReferencePGLanguage'`, `RUN='^TestReferencePGTransform'`, `RUN='^TestReferencePGTSConfig'`
- Large-object pages: `RUN='^TestReferencePGLargeobject'`
- `pg_sequence`, `pg_sequences`: `RUN='^TestReferencePGSequence'`
- Catalog foreign keys, `pg_get_userbyid`: `RUN='^TestReferenceCatalogRelations'`
- Information schema: `RUN='^TestReferenceInfo'`, `RUN='^TestReferenceDataTypePrivileges'`
- Visibility inquiries: `RUN='^TestReferenceVisibilityInquiry'`, `RUN='^TestReferenceBooleanCall'`
- Encoding conversions: `RUN='^TestReferenceConversion'`
- Tablespaces: `RUN='^TestReferenceTablespace'`
- Size functions: `RUN='^TestReferenceSize'`
- VACUUM: `RUN='^TestReferenceVacuum'`
- LOAD: `RUN='^TestReferenceLoad'`
- Extensions (registry, TEMP, removal, versions): `RUN='^TestReferencePGExtension'`, `RUN='^TestReferenceExtensionTemporary'`, `RUN='^TestReferenceExtensionDrop'`, `RUN='^TestReferenceExtensionVersion'`, `RUN='^TestReferenceExtension'`
- Available-extension views: `RUN='^TestReferenceAvailableExtensions'`
- pgcrypto: `RUN='^TestReferencePgcryptoAES'`, `RUN='^TestReferencePgcryptoBF'`, `RUN='^TestReferencePgcryptoPassword'`, `RUN='^TestReferencePgcryptoRandom'`
- uuid-ossp: `RUN='^TestReferenceUUIDOssp'`, `RUN='^TestReferenceObjectGrantUUIDOsspAuthority$'`
- fuzzystrmatch: `RUN='^TestReferenceDaitchMokotoff'`, `RUN='^TestReferenceLevenshtein'`, `RUN='^TestReferenceMetaphone'`, `RUN='^TestReferenceSoundex'`
- Mutable routine/provider names and schemas retain genuine OID/kind and
  extension associations; this does not promote lookup-only catalog identities
  to execution. Cross-family ALTER metadata and replay proof lives in
  [query_core](../query_core/README.md#routine-alter-proof), selected in both
  native and `REFERENCE=1` modes.

- pgcrypto digest, HMAC, installation and controls:
  `RUN='^TestReferencePgcrypto(Digest|HMAC|Installed|Overloads|Diagnostics|Consumers|Control|Replay|Forced|FeatureSmoke)'`
- Catalog and control scenarios migrated from the root package:
  `RUN='^TestReference(CatalogCore|CatalogControls|Control(Failed|Snapshot)|TransactionNativeControls|Smoke)'`
- Engine units use the family names without `Reference`, for example
  `make check RUN='^TestPGDepend'` or `make check RUN='^TestPGOperator'`.

### Domain constraints

The seven `TestReferenceInfoDomainConstraints` definitions prescribe the exact
released eight columns, nullable logical sql_identifier/yes_or_no identity,
NAME/VARCHAR(3) driver metadata/raw transport and row/array relationships.
Actual own CHECK/NOT NULL rows include genuine bootstrap recipes. Duplicate
constraint names retain distinct domains; table checks, inherited definitions
and unconstrained domains do not create rows. Empty/false/NULL/nil-parameter
predicates keep the complete description through direct/prepared execution,
EOF and Close.

Visibility covers enabled owner roles, direct/inherited/PUBLIC type USAGE,
grant options, NOINHERIT/SET-only membership, actual SET ROLE, superuser and
owner/ACL refresh on the same held statement. Schema USAGE is neither sufficient
nor necessary for this view. Lifecycle assertions check full bags after
ADD/NOT VALID/VALIDATE/DROP, SET/DROP NOT NULL, rename/move/drop and failures.
All projected flags remain NO; validation is separately witnessed through
pg_constraint. Complete English diagnostics and failure stages include missing
columns/constraints, duplicates, ownership and stored-data validation.

The controls definition runs shared SQL acceptance in both modes, then native
Dump/Reset/Restore, fresh-driver restore, retained eager data/types and failed
or canceled restore/read atomicity. PostgreSQL transactional ALTER followed by
rollback is distinct from native's existing explicit-transaction capability
diagnostic; this view does not widen DDL admission.
See the [projection and visibility contract](../../internal/engine/pgcatalog/README.md#domain-constraints).

```sh
make check RUN='^TestReferenceInfoDomainConstraints(Shape|Population|Visibility|Lifecycle|Diagnostics|NativeControls|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceInfoDomainConstraints(Shape|Population|Visibility|Lifecycle|Diagnostics|NativeControls|FeatureSmoke)$'
make check RUN='^TestInfoDomainConstraints(Producer|Capture|Cancellation)$'
make check RUN='^TestCatalogSourceProducerCoverage$'
make check RUN='^TestReferenceInfoDomainConstraintsFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferenceInfoDomainConstraintsFeatureSmoke$'
```

Expected smoke behavior: exactly three initial full identity rows (two same-name
CHECKs and one own NOT NULL), then a fourth NOT VALID CHECK row; validation
leaves the bag unchanged and selected DROP removes only its actual row. The
same held consumer omits a domain after PUBLIC USAGE revocation and includes it
after direct type USAGE without schema USAGE. Typed empty NULL/nil predicates
and full42703/Position8 missing-column diagnostics finish the scenario.
These are proof commands and expected observations, not a passing-check claim.

### Information-schema sequence definitions

`info_sequences_helpers_test.go` owns the shared scenario, exact option vectors,
raw-codec and metadata assertions, safe role/schema cleanup and direct/new/held
consumers. The family covers definition, authority, object-boundary, lifecycle,
transaction, typed-NULL and diagnostic contracts using independent expectations.
Retained eager-row ownership is asserted only on native cursors.
See [producer ownership](../../internal/engine/pgcatalog/README.md#information-schema-sequence-definitions).

- Sequence definitions: `RUN='^TestReferenceInfoSequences'`
- Domain definitions: `RUN='^TestReferenceInfoDomains'`

### Column grant metadata

`info_role_column_grants_helpers_test.go` owns the shared scenario, exact
eight-column bags, domain/adapter metadata, identity setup and held consumers.
The family checks ACL origins and raw-UNION multiplicity, enabled grantors and
grantees, object classes, mutation atomicity and fresh execution visibility.
Eager retained rows and native controls are explicit native-only phases.

- Column grants: `RUN='^TestReferenceInfoRoleColumnGrants'`

### Column metadata

`info_columns_helpers_test.go` owns the ordered domain/type vectors, complete
independent row assembly, raw/descriptor checks and direct/new/held consumers.
Family-local provider vectors enumerate actual represented fields and stable
attributes; the common provider/self cases assert complete 44-cell rows.
Foreign capability uses genuine installed handlers and explicit native public
interface phases without external scans. Native controls replay actual Dump
bytes and preserve old eager rows, with explicit ACL rejection tested separately.

- Column metadata: `RUN='^TestReferenceInfoColumns'`

### Table metadata

`info_tables_helpers_test.go` owns exact twelve-column rows, logical domains,
raw/adapter metadata and direct/fresh/held consumers. Scenarios assert represented
object kinds, typed identities, physical TEMP scope, effective privilege
visibility and metadata refresh without executing view bodies or foreign scans.
Native retained rows and Dump/Reset/Restore are explicit public-control phases.
Builtin definition consumers retain independent PostgreSQL display expectations.

- Table metadata: `RUN='^TestReferenceInfoTables'`

### View definition metadata

`info_views_helpers_test.go` owns paired scenarios, exact ten-column domain and
adapter metadata, role/schema cleanup and direct/new/held consumers. The family
checks definition disclosure, physical TEMP isolation, options, lifecycle
atomicity and structural capabilities without body execution. Builtin definition
expectations are literal independent PostgreSQL observations for default,
information-schema and exact-shadow paths, with quote_all_identifiers both off
and on. `pg_views` uses the same consumer expectations.
Foreign-handler capability scenarios live with the real foreign-source fixture
in `reference/tables`; they share the same family selector.
See [producer ownership](../../internal/engine/pgcatalog/README.md#information-schema-views).

- View definitions: `RUN='^TestReferenceInfoViews'`
