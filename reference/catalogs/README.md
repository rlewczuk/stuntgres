# reference/catalogs

`pg_catalog` relation and type catalogs: `pg_class`, `pg_attribute`, `pg_attrdef`,
shared `pg_subscription` identity and projection-aware sensitive-column access,
database-local `pg_subscription_rel` shape, stored owner/PUBLIC ACL and capture lifetime,
`pg_type`, `pg_enum`, `pg_range`, `pg_constraint`, partition-navigation
inquiry functions, `pg_indexes`, `pg_tables`/`pg_views`/`pg_matviews`, `pg_rules`,
`pg_depend`, `pg_sequence`/`pg_sequences`, shared-memory allocation/NUMA views,
`pg_wait_events`, `pg_proc`, catalog foreign keys, and catalog controls migrated
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
`reference/dml`. Relation inheritance links (`pg_inherits`), extended-statistics
catalogs and relation-owned statistics belong to
[reference/inheritance](../inheritance/README.md); executable-trigger catalogs
(`pg_trigger`) belong to [reference/triggers](../triggers/README.md).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` is
`fixture.Main(m, fixture.WithLegacyCryptoProvider())`; this package and the other
catalog packages listed above are the only ones whose oracle loads the legacy
OpenSSL provider
([fixture README](../fixture/README.md#test-only-legacy-crypto-oracle)).
The rules below apply to every catalog package.

Encoding-name and parser-keyword scenarios use `RUN='^TestReferenceEncodingKeywords'`.
Their independent release inventories cover all canonical encodings/aliases and
the complete keyword bag. Shared direct/prepared bodies cover typed metadata,
routine declarations, diagnostics, invocation-local cursors, cancellation,
mutation atomicity and symbolic native-control replay.

Ordinary selected-provider decoration diagnostics use
`RUN='^TestReferenceSelectedFunctionFilterDiagnostics$'`. Shared direct/prepared
cases assert complete FILTER errors before any scalar result, including predicate
analysis, failed cursor/statement publication and same-connection recovery.

Subscription lifecycle scenarios exercise disconnected definitions, options and
connection syntax, ordered remote publications, metadata-only desired enabled
state, role dependencies, transactions, real blocked cancellation, shared
cross-database visibility and native replacement. Renamed implicit-slot control
scenarios retain uppercase, UTF8 and literal `none` spelling through repeated
origins, final-name cycles and staging-name collisions, checking every stored
field and owner/database linkage across canonical rounds and failed replay.
Select the family with
`make check RUN='^TestReferenceSubscription'` (add `REFERENCE=1` for PostgreSQL).
Publisher connections, remote slot changes/removal, workers and synchronization
are separate external mechanisms, never inferred from stored enabled metadata.

Replication-origin scenarios independently assert explicit standalone and
subscription-created identity, typed five-function declarations/results, exact
diagnostics/strictness and mutable routine ACLs. Initial privileges retain the
actual initdb bootstrap grantor, independently of the fixture's later login role,
and stay unchanged through grants/revokes. Admitted cancellation validates the
complete PostgreSQL index-insertion context with only physical coordinates
variable; native context cancellation claims no physical tuple.
Identity journals and immediate unsigned progress have separate rollback witnesses,
including executed advances surviving later expression and caught exception failures.
The denied runtime-NULL witness reads a granted, public-qualified table independently
of session-role search paths. Native controls prove coherent deterministic canonical
logical replay/remint, raw absent/empty/granted routine ACL repair and rejected
widened repairs, retained rows, stale replacement fences and preserved peer
progress/pending exclusions, private peer savepoint/commit survival and atomic
retained/imported capacity admission including allocated zero. Held statements
recapture current state. Select the family with
`make check RUN='^TestReferenceReplicationOrigin'` (add `REFERENCE=1`).

Shared-label scenarios independently specify the exact empty `pg_shseclabel`
shape, fixed relation/rowtype identities, reciprocal allocated array, ordinary
PUBLIC/current held-statement ACLs, full missing-column/provider diagnostics and
failed-transaction recovery. Shared-target commands include string/empty/escaped
providers and NULL removal attempts; ordinary DDL/comments never populate labels.
Native controls preserve real sentinel data and captured metadata across
Dump/Reset/Restore. No PostgreSQL native provider ABI/loading or SELinux/MAC
integration is available. Select `make check RUN='^TestReferencePGShseclabel'`
and the same command with `REFERENCE=1`; feature smoke uses the exact
`TestReferencePGShseclabelFeatureSmoke` name.

Subscription scenarios cover the exact eighteen-column empty schema, shared
relation and reciprocal row/array identities, explicit owner-only table ACL,
PUBLIC column grants, current delegated/inherited authority and complete
diagnostics on direct, fresh and held prepared routes. Native controls retain
captured metadata and restore a real sentinel without populating subscriptions. Select the family with
`make check RUN='^TestReferencePGSubscription'` (add `REFERENCE=1`).

The populated `TestReferenceSubscription` family additionally checks secret-bearing
conninfo under owner access, explicit column grants and inherited grants, reusing
the same prepared query across revoke and native Reset/Restore. Ownership commands
are retained across owner and membership changes; prepared execution also covers
pre-canceled and pre-expired contexts. Shared-catalog scenarios admit real
same-name CREATE and cross-database REASSIGN-versus-local-ALTER waits before
releasing holders, with bounded joins. Native REASSIGN recaptures the definition
after object-lock admission and preserves the committed ALTER plus new owner.
PostgreSQL 18.6's owner handler instead waits on the catalog tuple's transaction
and reports `XX000: tuple concurrently updated`; the scenario checks that full
diagnostic, committed ALTER and retained original ownership/dependency without
retrying REASSIGN. This difference follows the released
[`AlterSubscriptionOwner_internal`](https://github.com/postgres/postgres/blob/REL_18_6/src/backend/commands/subscriptioncmds.c)
and [`simple_heap_update`](https://github.com/postgres/postgres/blob/REL_18_6/src/backend/access/heap/heapam.c)
paths, not an object-lock parity claim. Native-only control phases replace the
selected database while retaining a live peer subscription, advance that peer
behind a gated Restore reader, and check all stored fields, reminted local
identity, retained peer identities and both owner dependencies. Gated owner
rename/recreation and cancellation reject publication without changing the
sentinel. Core units separately target the actual prepared-generation final
cancellation and stale-catalog fences with populated subscriptions.
Subscription-relation scenarios check the four-column empty schema, database-local
identity, reciprocal row/array ownership, exact stored owner/PUBLIC privileges
and current held-statement admission through revoke/grant transitions. Native
controls roundtrip a real sentinel while preserving captured metadata without
creating mappings. Select this family with
`make check RUN='^TestReferencePGSubscriptionRel'` (add `REFERENCE=1`).

Publication-namespace scenarios assert the exact three-OID database-local catalog,
reciprocal types, stored ACLs, current held-statement admission, transactional
catalog/ACL roots and native replacement. Ordinary schema/table DDL never creates
publication memberships. Select with
`make check RUN='^TestReferencePGPublicationNamespace'` (add `REFERENCE=1`).

Dedicated relation-edge scenarios (`pg_publication_rel*_test.go`) retain the
existing fourteen publication-command families unchanged. They independently
specify PostgreSQL 18.6's five fields (`oid`, `prpubid`, `prrelid`, `prqual`,
`prattrs`), raw driver cell types and metadata on empty, populated, limited,
prepared and retained reads. Many-to-many relationships resolve actual
publication/table identities; generated OIDs are not cross-backend constants.
The stable attribute vector includes dropped-column holes, uses zero-based
subscripts, and distinguishes an omitted list from an explicit all-column list.
Filters retain genuine opaque `pg_node_tree` carriers with independently
specified `pg_get_expr` semantics. Native BoundTree encoding is not claimed to
equal PostgreSQL nodeToString bytes; raw bytes are checked only within a backend.

Rewrite scenarios (`pg_rewrite*_test.go`) independently assert all eight
attributes, C-collated nonnullable tree declarations, actual owning identities,
ordered commands, conditional namespaces, builtin declaration ownership,
replacement/mode/rename stability, executable effects, dependency rewiring and
drop/rollback. Retained raw trees are compared only within each backend.
Native builtin trees are parsed declaration metadata, never counterfeit checked
views. Controls replay actual native dumps and assert failed/canceled restore
atomicity. Scalar `pg_get_expr` diagnostics are asserted before missing-relation
lookup, including action-query rejection and OLD/NEW range-slot distinctions.
Absent qualification nodes render as empty TEXT, independently of strict
SQL-NULL inputs. View aliases are asserted through relation result descriptors.
Native actions retain their genuine defining-query output names; PostgreSQL's
ordinary-view target entries carry declared aliases while its materialized-view
query retains defining-query names. Raw structural expectations distinguish these
owners instead of equating backend bytes. Replay refreshes materialized views
before asserting their data and proves refresh leaves rewrite trees unchanged.
The qualification subquery matrix labels zero/existing/missing relation contexts
and both pretty overloads separately: parent namespace presence, not pretty,
determines child qualification. It includes child-local `old` aliases, outer
OLD/NEW, nested correlation, rename/current names and real nonmatch→match effects.
Additional lexical-owner witnesses deliberately separate a NULL child value from
a positive event value, require local OLD on INSERT/local NEW on DELETE to bind
their real source, and cover scalar, whole-row and outer-child alias scopes.
Their zero-context inquiries and nonmatch→match effects complement the exact
context/pretty SQL matrix rather than masking binding with display substitutions.
Actual whole-row CREATE RULE owners distinguish OLD/NEW and non-NULL records
with NULL members; no unrelated scalar presentation is swapped into a fixture.
Selected declaration witnesses use identical caller-written SQL under two
schemas with independently expected sum3 versus max2, fresh Prepare after the
path change, original bound declaration addresses, window dependencies, real
rule effects and predecessor retention. Mutation-CTE variants retain actual
aggregate/window/subquery roots, target/RETURNING fields, conflict and MERGE
owners through the existing checked modifier visitor. No raw-byte equality
between PostgreSQL and native is claimed.
Assignment-target witnesses select a volatile callable only in the destination
bound: independent first-versus-second array writes, stable rule identity,
dependency rewiring/drop, retained predecessor and current renamed rule display.
They cover ordinary/CTE partial INSERT, UPDATE, conflict UPDATE, slices, scalar
subqueries, nested composite members and supported CTE MERGE matched UPDATE.
The adjacent assignment-default matrix independently checks supplied, reordered,
explicit SQL NULL and partial values against a conflicting sink default, omitted
columns, identity-discard inputs and single/multirow explicit event DEFAULT
through direct/prepared routes. Genuine captured-rule producer units require
executable default recipes rather than nil sentinels and preserve original rows.
Separate stored-action cases execute reordered `(v,id) VALUES(DEFAULT,7)`,
mixed DEFAULT/supplied rows, no-column-default typed NULL and supplied NULL.
They assert event counts/rows and held predecessor identity rather than only
calling a private producer. Additional witnesses cover volatile sequence
defaults exactly once per requested row, identity discard without double
generation, real default failure atomicity, and current default replacement
through a held prepared event. Native units inspect actual composed output
usages, copied/default versus shared/supplied rows, and the genuine default
manifest's isolation from unrelated caller provenance.
MERGE partial INSERT remains the native `0A000` boundary (`MERGE partial insertion
targets are unsupported`), not a successful dual-mode fixture or new capability.
Genuine-owner units also distinguish usages, logical associations, nested steps,
dimension/bound order and cancellation without mutating installed predecessors.
The smoke selector includes successful qualification sublinks, the selected
aggregate and LHS-only callable/index-effect transitions, plus a real stored
INSERT-action DEFAULT producing `(id=7,v=101)`; it is not a schema-only check.
Select the rewrite family with `make check RUN='^TestReferencePGRewrite'`;
add `REFERENCE=1` for the pinned oracle.

Raw rows represent explicit edges, not synthesized schema/all-table or partition
child coverage. Broader ordinary-inheritance command expansion remains covered by
`TestReferencePublicationInheritancePartitions`. Lifecycle coverage exercises
ADD/DROP/SET, peer isolation, savepoint/full rollback, committed visibility,
failed multi-table ADD atomicity, table/publication drops, captured predecessor
rows and actual native Dump/Reset/Restore. Current catalog ACLs govern direct,
newly prepared and already-held execution even for zero-row/count projections;
catalog SELECT does not require ownership or SELECT on the published table.
Complete diagnostic records cover permission, duplicate/missing membership,
unknown column and invalid OID errors. Local negative/NULL sentinel rows remain
unfiltered: this is catalog metadata, not simulated replication.

Run both selected modes before review/integration:

- `make check RUN='^TestReferencePGPublicationRel(Shape|Payloads|Lifecycle|Privileges)$'`
- `make check REFERENCE=1 RUN='^TestReferencePGPublicationRel(Shape|Payloads|Lifecycle|Privileges)$'`
- `make check RUN='^TestReferencePGPublicationRelFeatureSmoke$'`
- `make check REFERENCE=1 RUN='^TestReferencePGPublicationRelFeatureSmoke$'`

The distinct feature smoke checks five-field raw metadata, stable vector/filter
payloads, unchanged local data, rollback, exact duplicate rejection with unchanged
edge identity, and native replacement. For oracle-first discovery use
`make check REFERENCE=1 RUN='^TestReferencePGPublicationRel(Shape|Payloads|Lifecycle|Privileges|FeatureSmoke)$/^postgres$'`;
this does not replace complete selected dual-mode proof.

Publication command scenarios (`publication_*_test.go`) populate the existing
`pg_publication` and `pg_publication_namespace` producers and observe actual
effective membership through `pg_publication_tables`. Existing empty-source
ownership and ACL scenarios remain unchanged. The contract is metadata-only:
local DML is never filtered, and these scenarios do not claim WAL delivery.

Run each selected group both natively and with `REFERENCE=1`:

- `make check RUN='^TestReferencePublication(DefinitionOptions|Membership|InheritancePartitions)$'`
- `make check RUN='^TestReferencePublication(ColumnsGenerated|Filters|DeferredIdentity)$'`
- `make check RUN='^TestReferencePublication(Privileges|Dependencies|Transactions)$'`
- `make check RUN='^TestReferencePublication(NativeControls|Smoke)$'`
- `make check RUN='^TestReferencePublicationInquiry$'`: intrinsic relation
  publishability (including NULL/missing/retired OIDs) and variadic publication
  table inquiry. Direct/prepared witnesses assert real owner OID relationships,
  typed empty SRF shape, column/filter/generated policies, schema/ALL dynamic
  membership, array bounds/dimensions/multiplicity, full diagnostics and
  combined partition via-root suppression.
- `make check RUN='^TestReferencePublicationTablePresentation$'`: direct
  presented-relation column/filter metadata across cross-schema ancestry and
  overlapping via-root mappings in both relation/mapping allocation orders;
  NULL empty projections and generated/ordinary-column transitions. Both the
  view and executable inquiry have independent direct/prepared expectations.

The self-contained feature smoke is
`make check RUN='^TestReferencePublicationSmoke$'` (also with `REFERENCE=1`):
real definitions, populated catalogs, retained column/filter metadata, deferred
zero-row DELETE rejection, transaction rollback and native logical roundtrip.
The controls scenario additionally replays actual emitted bytes on an independent
native destination and, in real mode, PostgreSQL; checks canonical redump,
retained rows and held statements; and rejects late-invalid/canceled replacement
without changing the sentinel or publication state.

Transactions include direct and prepared publication ADD cancellation after a
strict candidate-table lock admission witness, with canceled workers joined
before releasing resources and no mapping or option publication. PostgreSQL uses
server cancellation and a complete `57014` record; native pre-cancellation also
checks ordinary context errors. lib/pq pre-canceled utility results are not
constrained, as required by the shared testing policy.

For oracle-first discovery before native implementation, select the existing
`postgres` subtest rather than adding a backend filter. Ordinary families use
`RUN='^TestReferencePublication(DefinitionOptions|Membership|InheritancePartitions|ColumnsGenerated|Filters|Privileges|Dependencies|Transactions|NativeControls|Smoke)$$/^postgres$'`.
The per-case identity matrix has an extra level:
`RUN='^TestReferencePublicationDeferredIdentity$$/./^postgres$'`.
These discovery selectors do not replace selected full dual-mode proof.


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
- Constraints and partition navigation: `RUN='^TestReferencePGConstraint'`, `RUN='^TestReferencePartitionInfo'`
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
