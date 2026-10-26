# reference/catalog_objects

Schema-object catalogs: `pg_namespace`, schemas and `search_path`, visibility
inquiry functions, `pg_operator` (with the geometric and `lseg` operator
catalogs), `pg_cast`, `pg_aggregate`, `pg_am`, `pg_opclass`, `pg_amop`, `pg_collation`,
`pg_language`, `pg_transform`, `pg_ts_config`, `pg_ts_parser`, `pg_largeobject`, and comments
(`pg_description`, `pg_shdescription`, comment inquiry functions and
transactional comments). The other catalog families are listed in
[reference/catalogs](../catalogs/README.md).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md) and the shared catalog rules in
[reference/catalogs](../catalogs/README.md#testing). `TestMain` is
`fixture.Main(m, fixture.WithLegacyCryptoProvider())`, as in every catalog
package.

Namespace controls replay nominal declarations and exercise owner-authorized
type removal. Schema authorization scenarios share direct/prepared creation and
lookup diagnostics across backends, including character positions. Schema notice
scenarios assert complete notice records through the shared notice-enabled pool.

- Expected data comes from released inventories, never from native output:
  `docs/design/pg-operator-pg18-supported.tsv` (shared by
  every `TestReferencePGOperator` test, `TestReferenceLsegOperatorCatalog` and
  the engine unit `TestPGOperatorCaptureOwnership`),
  `docs/design/regoperator-pg18-inventory.tsv`, the explicit 144×22 fixture in
  `pg_aggregate_fixture_test.go` and literal REL_18 catalog tuples in other
  inventories. Admitting a type or operator changes the shared TSV and every
  consumer, including `TestReferencePGTypeAdmission` in
  [reference/catalogs](../catalogs/README.md#testing).
  Access-method and operator-class inventories include BRIN's released method,
  handler, minmax classes and diagnostic-only algorithm identities. BRIN class
  tuples are independently captured in `pg_opclass_brin_facts_test.go`; catalog
  presence does not grant a native summary algorithm.
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
- Transactional comments share the seven-kind inventory and setup in
  `transactional_comment_helpers_test.go`, with direct/held SQL BEGIN and Go
  BeginTx routes. Keep literal, diagnostic and lifecycle assertions together
  when changing this family. Captured identity never retargets after a wait:
  replacement is fenced without changing the recreated object's text.
  Role DROP/recreate must not inherit text; this family does not add
  role-renaming SQL.

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
- Operators: `RUN='^TestReferencePGOperator'`, `RUN='^TestReferenceLsegOperatorCatalog$'`
- Aggregates: `RUN='^TestReferencePGAggregate'`
- <a id="geometric-relation-operator-catalog-proof"></a>Geometric relation catalogs: `RUN='^TestReferenceGeometryRelationOperatorCatalog$'`
- Geometric position catalogs: `RUN='^TestReferenceGeometryPositionOperatorCatalog$'`
- Casts: `RUN='^TestReferencePGCast'`
- Collations, languages, transforms, text search: `RUN='^TestReferencePGCollation'`, `RUN='^TestReferencePGLanguage'`, `RUN='^TestReferencePGTransform'`, `RUN='^TestReferencePGTSConfig'`, `RUN='^TestReferencePGTSParser'`
- Large-object pages: `RUN='^TestReferencePGLargeobject'`
- Visibility inquiries: `RUN='^TestReferenceVisibilityInquiry'`, `RUN='^TestReferenceBooleanCall'`
- Catalog smoke scenarios migrated from the root package: `RUN='^TestReferenceSmoke'`
