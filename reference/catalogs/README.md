# reference/catalogs

`pg_catalog` relation and type catalogs: `pg_class`, `pg_attribute`, `pg_attrdef`,
`pg_statistic_ext` definitions and restricted typed `pg_statistic_ext_data`,
shared `pg_subscription` identity and projection-aware sensitive-column access,
database-local `pg_subscription_rel` shape, stored owner/PUBLIC ACL and capture lifetime,
`pg_type`, `pg_enum`, `pg_range`, `pg_constraint`, `pg_inherits`, partition-navigation
inquiry functions, `pg_indexes`, `pg_tables`/`pg_views`/`pg_matviews`, `pg_rules`,
`pg_depend`, `pg_sequence`/`pg_sequences`, shared-memory allocation/NUMA views,
`pg_wait_events`, `pg_proc`, `pg_trigger`, catalog foreign keys, and catalog controls migrated
from the root package. The other catalog packages:
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

Trigger catalog witnesses use actual relation-owned executable definitions and
independent direct/prepared metadata, identity, visibility, consumer diagnostics
and native-control expectations. Native structural trees are opaque across
backends; semantic deparse and executable behavior are separate assertions.
Persistent FK witnesses cover all action-selected RI functions, four real trigger
identities, exact internal/constraint/index/opposite-relation fields, both
relations' trigger-presence flags and removal through held metadata reads.
Select the family with `make check RUN='^TestReferencePGTrigger'` in either mode.

Extended-statistics definition reads cover exact schema and row/array links,
PUBLIC access and current ACL revocation, ordinary-role mutation diagnostics,
and native controls without privileged PostgreSQL catalog writes. Select the
family in either native or real-reference mode with
`make check RUN='^TestReferencePGStatisticExt'` (add `REFERENCE=1` for PostgreSQL).
Statistics lifecycle scenarios include direct/prepared relation-kind admission,
virtual-column rejection, creation/removal, namespace/temp lookup, NORMAL/AUTO
cleanup, malformed source positions, bulk ownership and transaction lifecycle,
held conditional notices, typed NULLs and native definition replay with exact
paired-owner guards. Native-only phases cover pre-canceled utilities and gated
external I/O with live statistics definitions. Their family
selector is `make check RUN='^TestReferenceStatisticsObject'` in either mode.
Collected statistics remain independently owned.
Statistics adapter metadata is asserted independently: native database type names
are exact, while lib/pq returns an empty name for the three unknown statistics
OIDs. Catalog type identities remain exact; numeric cross-adapter OID witnesses
use explicit BIGINT casts rather than comparing driver-specific raw carriers.
Multivariate ndistinct scenarios cover real NULL-inclusive collection,
canonical subsets, target/column selection, separate inheritance flavors,
diagnostics, transactional publication, cancellation and lossless native state.
Retained-TEMP control phases keep the physical session alive through Restore
and Reset, read TEMP and imported persistent payloads through DATA, and retain
old rows/metadata independently of replacement type generations.
Native-control phases replay actual emitted definition/target SQL on PostgreSQL,
which ignores native state comments. Select the family in either mode with
`make check RUN='^TestReferenceStatisticsNDistinct'` (add `REFERENCE=1`).

Measured functional-dependency scenarios share the same direct/prepared helpers:
NULL-inclusive row-weighted strengths, all determinant widths, sparse stable
attributes and signed expression dimensions. Refresh, transaction visibility,
authorization warning/error distinction, genuine typed results and stale native
fractional/analyzed-NULL replay are covered by
`make check RUN='^TestReferenceStatisticsDependencies'` (add `REFERENCE=1`).
Native values describe complete logical populations, not physical TOAST-width
filtering or server sample emulation; substantive query rows remain exact.

Multivariate MCV scenarios share the direct/prepared fixture and independently
decode public `pg_mcv_list_send` BYTEA images using released physical layouts,
never production codecs. They assert full-population joint/marginal frequencies,
SQL NULL flags, unrenormalized target caps, typed dictionaries and logical OIDs.
Native physical images are not COPY-wire values or cross-backend byte goldens.
Array setup uses each backend's existing usable default-class authority rather
than shadowing a complete provider with incomplete exact metadata. Explicit
rejected-provider phases distinguish accepted class metadata from executable
statistics admission, while retaining native positive physical-codec assertions.
Remint witnesses combine reachable domain-over-array and scalar enum-domain
dimensions without introducing new column constructors.
Reachable native XID/CID/vector phases restore deliberately stale joint facts into
independent destinations and then distinguish them from real reanalysis. Public
vector assertions include exact extents, offsets, element OIDs and element words,
alongside NULL markers and full-population marginal baselines.
Authority, transaction visibility, real blocked cancellation, stale semantic
Dump/Restore, destination reminting and genuine retained TEMP facts use public
APIs. Select the family with `make check RUN='^TestReferenceStatisticsMCV'`
(add `REFERENCE=1` for the pinned oracle).

Expression-statistics scenarios use the same public direct/prepared fixture and
the genuine `pg_statistic_ext_data.stxdexpr` row array: subscript/field access,
typed INT4/TEXT MCV slots, FLOAT4 frequencies, NULL fractions/distinct encoding,
all-NULL versus nonstrict output, and ordered histogram bounds. Definitions
cover expression-only `{e}`, explicit `{d,e}`, default `{d,f,m,e}`, source-order
names, width limits, nonimmutable functions and conditional-create identity.
Dependency scenarios assert the full NORMAL/AUTO edge bag, current-name deparse,
stable identity, selected/unselected column retirement and routine RESTRICT/CASCADE.
Full diagnostics cover prohibited nodes, name resolution and nonowner admission.
The mixed/dual-expression witnesses require real NULL-inclusive joint ndistinct;
the dependencies and MCV families also cover these dimensions.
Selected-column ANALYZE checks simple statistics keys, while expression-only
and mixed definitions evaluate their expressions from full captured rows even
when expression input columns are not selected for scalar collection.
Native-control phases retain deliberately stale expression summaries through
Dump/Reset/Restore, sparse input layouts, mixed owners and destination reminting;
malformed/canceled imports retain destination data and old facts. PostgreSQL
replays actual emitted declaration SQL, not native semantic state comments.
Equality-only XID expression collection checks the authentic MCV equality
operator, typed values and continued readability of unrelated collected objects;
single-expression projection does not impose a delayed btree requirement.
All-NULL INT4 expression width is four, while unknown all-NULL TEXT width is
zero; native replay retains both stale widths after source rows change.
Hierarchy coverage attaches an existing child with different physical attribute
positions, checks separate parent-local/parent-inherited/child-local expression
distributions and real joint ndistinct, and requires inherited-only data for a
partitioned parent. Native replay preserves stale inherited expression and joint
facts despite the restored tables containing a different current distribution.
Select every expression definition in both modes:
`make check RUN='^TestReferenceStatisticsExpression'` (add `REFERENCE=1`).
The end-to-end smoke selector is
`make check RUN='^TestReferenceStatisticsExpressionFeatureSmoke$'` in both modes;
it checks real collection, rename, admission failures, staleness and refresh.

DATA-specific catalog scenarios independently assert all six fields (including
empty-result metadata), relation/row/array identity, genuine analyzed-object
population and exact missing-column diagnostics. The direct/fresh-prepared and
retained-prepared ACL matrix proves that owning statistics is insufficient,
explicit catalog SELECT permits other-owner data, and revoke/regrant affects
the same held statements, including WHERE FALSE. Its feature smoke retains an
unbuilt definition while collecting an actual count2 payload. Select these
witnesses with `make check RUN='^TestReferencePGStatisticExtData'` (add
`REFERENCE=1`); the existing NDISTINCT family still owns collector lifecycle,
transactions, hierarchy, cancellation and native controls.

Subscription scenarios cover the exact eighteen-column empty schema, shared
relation and reciprocal row/array identities, explicit owner-only table ACL,
PUBLIC column grants, current delegated/inherited authority and complete
diagnostics on direct, fresh and held prepared routes. Native controls retain
captured metadata and restore a real sentinel without populating subscriptions. Select the family with
`make check RUN='^TestReferencePGSubscription'` (add `REFERENCE=1`).
Subscription-relation scenarios check the four-column empty schema, database-local
identity, reciprocal row/array ownership, exact stored owner/PUBLIC privileges
and current held-statement admission through revoke/grant transitions. Native
controls roundtrip a real sentinel while preserving captured metadata without
creating mappings. Select this family with
`make check RUN='^TestReferencePGSubscriptionRel'` (add `REFERENCE=1`).


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

The `pg_proc_*` family reads all 30 PostgreSQL 18 columns in released attribute
order, including raw direct/prepared/held and empty-result metadata. Released
builtin expectations are independent of native descriptors; user matrices check
input-only overload identities separately from all-argument modes/names and
their array bounds. Owner, namespace, language and generated type references use
independent catalog identities. Metadata reads neither execute routine bodies
or defaults nor require support callbacks or other absent runtime facilities.
Routine lifecycle assertions retain old unread rows and descriptors while a
held query sees replacements, movement, ownership/ACL changes and rollback.
The full-shape check distinguishes pq's FLOAT4 `ScanType` (`float32`) from
its actual exported `float64` values and the native `float64` scan descriptor.
It likewise checks pq's INT2 `int16` scan descriptor versus native `int64`.
Raw OID results retain their OID column type and byte-versus-int64 assertions;
family expected rows follow the existing `PGAttributeCheck` decimal-text
adaptation for pq rather than casting catalog SQL to another type.
The sequence side-effect witness explicitly consumes its defaulted input:
an unused SQL parameter need not evaluate its supplied/default expression.

Tree carriers are an explicit representation boundary, not fake PostgreSQL
serialization parity: PostgreSQL returns nodeToString trees; native returns
immutable structural expression lists and checked routine programs. Tests inspect
both actual raw carriers independently, retain genuine defaults/body presence,
and require exact semantic `pg_get_expr` default text and real omitted-argument
execution. Quoted SQL/PL source has no SQL-standard body tree. Library provenance
is read from the owning declaration, not inferred from a public routine name.
Native Go callbacks retain the native-only `prolang=0` sentinel, empty source and
NULL binary; actual public registration proves reads never invoke a callback.
This phase has no PostgreSQL equivalent. Native controls replay nonempty actual
Dump bytes unchanged on PostgreSQL and verify logical routine/type relationships,
properties and invocation, rather than pinning allocator-generated OIDs.

`TestReferencePGProcDependencies` distinguishes immutable declaration acquisition
from current executable-dependency admission. Direct, prepared and held probes
read consumer/default metadata and unrelated builtin rows after callee attribute
changes/replacement/rename. Semantic default deparse follows the current callee
name, while retained raw body/default trees remain immutable.

Installed metadata has separate bounded acceptance families: the 14 released
citext SQL-standard bodies and six admitted earthdistance bodies are installed
with quoted, nondefault schemas, including a separately placed cube dependency.
Exact overload membership, source/language/binary/body presence and concrete
raw structures are independently checked on each backend. PostgreSQL function
references are resolved through `regprocedure`, not pinned generated OIDs;
native recipes retain concrete function/parameter/CASE/subscript syntax and
the actual quoted dependency schema. Existing executable overloads corroborate
the declarations. The library matrix covers every changed C-library owner,
including `_int` and differing public/source names, after actual native restore
and unchanged-byte PostgreSQL replay. Installed controls retain these facts
through SQL and C routine rename/schema movement, snapshots and logical replay.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Relations and attributes: `RUN='^TestReferencePgClass'`, `RUN='^TestReferencePGAttribute'`, `RUN='^TestReferenceAttrdef'`
- Types, enums, ranges: `RUN='^TestReferencePGType'`, `RUN='^TestReferencePGEnum'`, `RUN='^TestReferencePGRange'`
- Routine declarations and metadata: `RUN='^TestReferencePGProc'`; smoke:
  `RUN='^TestReferencePGProcFeatureSmoke$'`. Shape, released inventory,
  declarations/properties/visibility and lifecycle/controls/diagnostics are
  separate selected proof families; run each changed family natively and with
  `REFERENCE=1`. Private producer-offset/immutability tests remain lower-owner
  units; public SQL assertions belong here, not internal enginetests.
  Review-remedy selections:
  `RUN='^TestReferencePGProc(Dependencies|InstalledBodies|InstalledLibraries|InstalledControls)$'`.
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
