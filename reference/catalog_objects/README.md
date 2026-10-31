# reference/catalog_objects

Schema-object catalogs: `pg_namespace`, schemas and `search_path`, visibility
inquiry functions, `pg_operator` (with the geometric and `lseg` operator
catalogs), `pg_cast`, `pg_aggregate`, `pg_am`, `pg_opclass`, `pg_opfamily`, `pg_amop`, `pg_amproc`, `pg_collation`,
`pg_language`, `pg_transform`, `pg_foreign_server`, `pg_ts_config`, `pg_ts_config_map`, `pg_ts_parser`, `pg_largeobject`, and comments
(`pg_description`, `pg_shdescription`, comment inquiry functions and
transactional comments). The other catalog families are listed in
[reference/catalogs](../catalogs/README.md).

Search visibility scenarios discover descriptor identities through their owning
catalogs, independently of the inquiry under test. The five search/statistics
classes use the statement-captured non-TEMP effective path, with schema USAGE
filtering but no object-rights predicate. Control phases replay actual emitted
SQL and rediscover destination identities rather than retaining allocator values.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md) and the shared catalog rules in
[reference/catalogs](../catalogs/README.md#testing). `TestMain` is
`fixture.Main(m, fixture.WithLegacyCryptoProvider())`, as in every catalog
package.

Namespace controls replay nominal declarations and exercise owner-authorized
type removal. Schema authorization scenarios share direct/prepared creation and
lookup diagnostics across backends, including character positions. Schema notice
scenarios assert complete notice records through the shared notice-enabled pool.

`TestReferenceSecurityLabel(ProviderBoundary|ObjectSyntax|ObjectPermissionPrecedence|AtomicityControls|SyntaxDiagnostics|OrderedSetModifiers|CallDecorations|FeatureSmoke)`
checks the zero-provider execution boundary with independent direct/prepared
diagnostics, full target syntax, inaccessible/non-owned objects, transaction
failure and native Dump/Reset/Restore. Reset verifies relation absence through
`pg_class`, without requiring unrelated `to_regclass` execution support. Session
authorization reset expects the configured native `postgres` or PostgreSQL
fixture `stuntgres` identity, independently of query output. Pre-canceled
admission checks ordinary context cancellation only on native, following the
adapter boundary in [docs/TESTING.md](../../docs/TESTING.md); shared provider,
transaction, data and comment diagnostics remain exact. NULL requires a provider
and never succeeds as a metadata no-op. Run `make check RUN='^TestReferenceSecurityLabel'`
and the same command with `REFERENCE=1`; the fixture remains pinned to 18.6
despite older catalog policy provenance. The quiet feature smoke selector is
`make check RUN='^TestReferenceSecurityLabelFeatureSmoke$'` in both modes.
Ordered-set VARIADIC signatures preserve PostgreSQL parsed modifier identity:
equal literal and compound syntax (including formatting, `CAST`/`::`, redundant
call/query parentheses, explicit/default ALL and decoded qualified type names)
reaches the provider error in both operand directions and direct/prepared modes.
Distinct literal/compound operands and function-call versus type-cast syntax
fail with positioned `0A000` during preparation, not provider lookup.
Nested CAST targets independently cover DECIMAL/NUMERIC and SQL keyword/
explicit pg_catalog aliases in both directions, including casts inside type
modifiers. Generic int4 versus INTEGER, quoted-case names, raw modifiers,
array ranks/bounds and signature SETOF/%TYPE retain genuine distinctions.
NCHAR/NATIONAL CHAR/NATIONAL CHARACTER cover fixed/default-length and VARYING
constructors, explicit lengths and arrays, with both-direction raw identities
against pg_catalog.bpchar/varchar and genuine quoted/generic mismatches.
Object syntax and complete diagnostic definitions distinguish forbidden JSON
keyword modifiers from valid quoted/qualified generic names, and character
integer lengths from rejected expression/multiple-length syntax. These utility
constructors do not expand ordinary declaration or execution capability.
The initial type/function-name category is distinct from terminal ColLabel:
national.ty(1) fails at dot32; nchar.ty and nchar.ty(1) both require full
42601/ERROR diagnostics at dot29, with only Position additionally populated.
Direct commands fail at execution and prepared commands at preparation.
Quoted heads "national".ty(1) and "nchar".ty(1), and terminals s.nchar(1) and
s."nchar"(1), reach provider22023 after successful preparation; modifier-free
NCHAR quoted-head/terminal controls do too. NCHAR character constructors and
national/nchar object/provider names retain their separate legal categories.
The terminal s.national(1) and s."national"(1) have identical raw names.
`CallDecorations` independently expects provider `22023` for every ordered subset
of WITHIN GROUP, FILTER and OVER across selected and ordinary call routes, in
direct and prepared modes. Equal WITHIN-plus-FILTER modifiers, with and without
OVER, retain parsed identity. Different FILTER predicates, WITHIN ordering,
window partition/order and in-call versus WITHIN ordering require positioned
preparation-time `0A000`, rather than being discarded as parser provenance.
`SyntaxDiagnostics` also fixes complete primary messages and character positions
for repeated/out-of-order WITHIN/FILTER/OVER and incompatible WITHIN clauses:
direct execution fails, while prepared commands fail during preparation.
Public event execution ordering
is covered by `TestReferenceSecurityLabelEventBoundary` in `reference/triggers`.

- Expected data comes from released inventories, never from native output:
  `docs/design/pg-operator-pg18-supported.tsv` (shared by
  every `TestReferencePGOperator` test, `TestReferenceLsegOperatorCatalog` and
  the engine unit `TestPGOperatorCaptureOwnership`),
  `docs/design/regoperator-pg18-inventory.tsv`, the explicit 145×22 fixture in
  `pg_aggregate_fixture_test.go` and literal REL_18 catalog tuples in other
  inventories. Admitting a type or operator changes the shared TSV and every
  consumer, including `TestReferencePGTypeAdmission` in
  [reference/catalogs](../catalogs/README.md#testing).
  XML aggregate OID2901 retains the released transition OID2900, XML state
  OID142, NULL seeds and no final, combine, serialization or moving support.
  Its catalog metadata does not admit the transition routine as a scalar call.
  Access-method and operator-class inventories include BRIN's released method,
  handler and minmax classes. Genuine minmax support operations 3383–3386 consume
  internal summaries; this does not advertise missing physical callback ABIs.
  BRIN class tuples are independently captured in `pg_opclass_brin_facts_test.go`.
  `pg_amop` projects live operator-family owners, not generic rows per class
  or per physical index. Its admitted inventory preserves cross-type search
  members and ordering members with their real sort-family links; catalog
  presence does not authorize additional index algorithms. Dedicated shape,
  inventory, held-read lifecycle, rollback, diagnostics, reader permissions,
  cancellation and native logical round-trip scenarios use independent
  expectations in both modes. `TestPGAmopCaptureOwnership` and
  `TestPGAmopCaptureValidation` protect current-generation type ownership and
  upfront shape validation, including empty inventories at the shared
  `pg_amop`/`pg_amproc`/`pg_opfamily` capture seam.
  Raw metadata distinguishes the base catalog's non-null columns from nullable
  stored-view columns and lib/pq's INT2 scan type. Relation index flags are owned
  builtin metadata, not synthetic physical index relations.
  Permission scenarios retain owner-prepared statements across SET ROLE and
  require complete reader-denial diagnostics before effects. Native owner writes
  remain unsupported and effect-free; PostgreSQL owner writes are never executed.
  `TestReferencePGAmproc(Inventory|Shape|Metadata|Diagnostics|Permissions|HeldExecution|Lifecycle|Extension|Cancellation|FeatureSmoke)`
  checks the six OID/OID/OID/OID/INT2/REGPROC columns, independent comparator,
  cross-type, hash and internal BRIN facts, installed cube ownership and dynamic
  owner lifecycle. Generated member OIDs are valid/stable within each backend,
  not harvested cross-backend goldens. Unsupported callbacks are absent natively
  and explicitly present upstream. `CatalogVisible` is an immutable owner fact:
  internal descriptors/dependencies and logical property consumers retain hidden
  memberships. Private `TestPGAmprocImplementedProjection` and
  `TestOperatorFamilySupportVisibility(Remap)?` cover projection/default denial,
  finite implementation facts and copy/remapping; use the exact selectors below.
  Selected proof: `make check RUN='^TestReferencePGAmproc'` and the same command
  with `REFERENCE=1`; private selector
  `make check RUN='^(TestPGAmprocImplementedProjection|TestOperatorFamilySupportVisibility|TestOperatorFamilySupportVisibilityRemap)$'`.
  `pg_amproc` uses the same execution-time catalog mutation guard as `pg_amop`.
  Its permission proof covers direct, fresh-prepared, reader-prepared held and
  owner-prepared held mutations after `SET ROLE`, including empty predicates.
  Reader writes fail with `42501`; native owner writes separately fail with
  `0A000`, without changing memberships or the sentinel. The guard admits
  preparation, not catalog writes or additional support implementations.
  `pg_opfamily` projects the immutable family owners' five identity columns.
  Same-name families are distinguished by method and namespace; class/operator/
  support links retain real associated types rather than inferred per-index
  rows. Genuine released metadata is distinct from executable admission:
  catalog presence does not authorize an algorithm or unsupported callback.
  Its shared guard likewise admits semantic preparation, never writes. Reader
  direct/fresh/reader-held/owner-held writes (including empty predicates) fail
  at execution with `42501 / permission denied for table pg_opfamily`; native
  owner routes fail with `0A000 / catalog source mutation is unsupported`.
  Qualified public sentinel reads use explicit schema USAGE and table SELECT,
  independently of catalog privileges. Private `TestPGOpfamilyCaptureOwnership`
  and `TestPGOpfamilyCaptureCancellation` protect actual producer projection,
  current-generation descriptors, detached backing, Reset retention and
  deterministic cancellation without partial publication.
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
  `TestReferencePGAggregateHelperPlacement` moves only the intagg aggregate and
  independently checks that helper OIDs and regproc/regprocedure selection stay
  in their original namespace, while the aggregate uses its new namespace.
  Both direct and prepared reads cover visibility changes; genuine extension
  removal separately checks retired numeric output. Selected proof is
  `make check RUN='^TestReferencePGAggregate(HelperPlacement|Lifecycle)$'`
  and the same command with `REFERENCE=1`.
- Transactional comments share the seven-kind inventory and setup in
  `transactional_comment_helpers_test.go`, with direct/held SQL BEGIN and Go
  BeginTx routes. Keep literal, diagnostic and lifecycle assertions together
  when changing this family. Captured identity never retargets after a wait:
  replacement is fenced without changing the recreated object's text.
  Role DROP/recreate must not inherit text; this family does not add
  role-renaming SQL.

Text-search mapping scenarios assert the four source-owned OID/INT4/INT4/OID
columns, genuine configuration/parser/token/dictionary references and explicit
dictionary sequence order. Held readers exercise changing parameters and current
mapping state. Native replacement checks distinguish detached retained rows and
metadata from fresh executions; PostgreSQL streaming rows are not treated as
native-control snapshots.

Foreign-server catalog scenarios independently assert all eight source columns,
attribute nullability/dimensions/collations and index-presence facts, actual
owner/wrapper joins, ordered options, ACLs, execution-time SELECT reauthorization,
transactional publication and complete diagnostics. Raw server options are
PUBLIC-readable catalog data even without server USAGE; separately filtered
information-schema views are not the oracle for raw catalog visibility.
Metadata succeeds for no-handler definitions while foreign-table execution has
its own diagnostic boundary. Native controls retain actual opened results across
logical replacement; registered provider callbacks are tripwires, not simulated
remote connections.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Namespaces, schemas, `search_path`: `RUN='^TestReferencePGNamespace'`, `RUN='^TestReferenceSchema'`
- Comments: `RUN='^TestReferencePGDescription'`, `RUN='^TestReferencePGShDescription'`, `RUN='^TestReferenceComment'`
- Comment transactions: `RUN='^TestReferenceTransactionalComment'`
- Access methods and operator classes: `RUN='^TestReferencePGAM'`, `RUN='^TestReferencePGOpclass'`
- Operator-family membership: `RUN='^TestReferencePGAmop'`; feature smoke:
  `make check RUN='^TestReferencePGAmopFeatureSmoke$'` and the same command with
  `REFERENCE=1`. It checks exact search/hash/cross-type/ordering facts, typed
  empty reads, real member ADD/DROP/rollback through a held read, stable
  memberships through index DDL, and exact diagnostics followed by recovery.
- Operator-family identity and membership, selected pairs (run each command both
  as shown and with `REFERENCE=1`, using the current pinned PostgreSQL 18.6 fixture):
  - `make check RUN='^TestReferencePGOpfamily(Shape|Identity|Membership)$'`
  - `make check RUN='^TestReferencePGOpfamily(Diagnostics|Permissions)$'`
  - `make check RUN='^TestReferencePGOpfamily(Lifecycle|Controls|Cancellation)$'`
  - Feature smoke: `make check RUN='^TestReferencePGOpfamilyFeatureSmoke$'`.
    It composes exact direct/fresh/held identity and typed empty reads,
    search/hash/cross-type/support membership, unchanged builtin bags through
    B-tree/hash index DDL, custom method-qualified rename/move/rollback/drop and
    full missing-column/OID diagnostics with recovery. Controls are a separate
    scenario: logical table/index/view/custom-family replay, retained rows and
    prepared reads across Reset/Restore, deterministic redump and failed/canceled
    replacement atomicity. No PostgreSQL equivalent of native controls is claimed.
  - Private capture proof:
    `make check RUN='^(TestPGOpfamilyCaptureOwnership|TestPGOpfamilyCaptureCancellation|TestPGAmopCaptureValidation)$'`.
  - Existing finite-support boundary:
    `make check RUN='^(TestPGAmprocImplementedProjection|TestOperatorFamilySupportVisibility|TestOperatorFamilySupportVisibilityRemap)$'`.
  - Existing family semantics:
    `make check RUN='^TestReferenceOperatorFamily(Definitions|Metadata|BuiltinMetadata)$'`
    and the same command with `REFERENCE=1`.
- Operators: `RUN='^TestReferencePGOperator'`, `RUN='^TestReferenceLsegOperatorCatalog$'`
- Aggregates: `RUN='^TestReferencePGAggregate'`
- <a id="geometric-relation-operator-catalog-proof"></a>Geometric relation catalogs: `RUN='^TestReferenceGeometryRelationOperatorCatalog$'`
- Geometric position catalogs: `RUN='^TestReferenceGeometryPositionOperatorCatalog$'`
- Casts: `RUN='^TestReferencePGCast'`
- Collations, languages, transforms, text search: `RUN='^TestReferencePGCollation'`, `RUN='^TestReferencePGLanguage'`, `RUN='^TestReferencePGTransform'`, `RUN='^TestReferencePGTSConfig'`, `RUN='^TestReferencePGTSParser'`
- Text-search mappings: `RUN='^TestReferencePGTSConfigMap'`; private ownership:
  `RUN='^TestPGTSConfigMap(Capture|Ownership)$'`.
- Large-object pages: `RUN='^TestReferencePGLargeobject'`
- Visibility inquiries: `RUN='^TestReferenceVisibility(Inquiry|Execution)'`, `RUN='^TestReferenceBooleanCall'`
- Search-path object visibility: `RUN='^TestReferenceSearchVisibility'`
- Catalog smoke scenarios migrated from the root package: `RUN='^TestReferenceSmoke'`
- Foreign-server catalog: `RUN='^TestReferencePGForeignServer(Shape|Metadata|Permissions|Lifecycle|Diagnostics|Controls|Smoke)$'`.
  Selected proof pairs: `make check RUN='^TestReferencePGForeignServer(Shape|Metadata)$'`,
  `make check RUN='^TestReferencePGForeignServer(Permissions|Lifecycle)$'`, and
  `make check RUN='^TestReferencePGForeignServer(Diagnostics|Controls)$'`; run
  each again with `REFERENCE=1` against the existing pinned PostgreSQL fixture.
  Feature smoke: `make check RUN='^TestReferencePGForeignServerSmoke$'` and
  `make check REFERENCE=1 RUN='^TestReferencePGForeignServerSmoke$'`.
  Private capture proof: `make check RUN='^TestPGForeignServer(Producer|Authority|Cancellation|Ownership)$'`
  (add `RACE=1` for selected race evidence).
