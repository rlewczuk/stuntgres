# reference/catalog_objects

Schema-object catalogs: `pg_namespace`, schemas and `search_path`, visibility
inquiry functions, `pg_operator` (with the geometric and `lseg` operator
catalogs), `pg_cast`, `pg_aggregate`, `pg_am`, `pg_opclass`, `pg_collation`,
`pg_language`, `pg_transform`, `pg_ts_config`, `pg_largeobject`, and comments
(`pg_description`, `pg_shdescription`, comment inquiry functions and
transactional comments). The other catalog families are listed in
[reference/catalogs](../catalogs/README.md).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md) and the shared catalog rules in
[reference/catalogs](../catalogs/README.md#testing). `TestMain` is
`fixture.Main(m, fixture.WithLegacyCryptoProvider())`, as in every catalog
package.

Namespace controls replay nominal declarations and exercise owner-authorized
type removal.

- Expected data comes from released inventories, never from native output:
  `docs/design/pg-operator-pg18-supported.tsv` (shared by
  every `TestReferencePGOperator` test, `TestReferenceLsegOperatorCatalog` and
  the engine unit `TestPGOperatorCaptureOwnership`),
  `docs/design/regoperator-pg18-inventory.tsv`, the explicit 144×22 fixture in
  `pg_aggregate_fixture_test.go` and literal REL_18 catalog tuples in other
  inventories. Admitting a type or operator changes the shared TSV and every
  consumer, including `TestReferencePGTypeAdmission` in
  [reference/catalogs](../catalogs/README.md#testing).
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
- Operators: `RUN='^TestReferencePGOperator'`, `RUN='^TestReferenceLsegOperatorCatalog$'`
- Aggregates: `RUN='^TestReferencePGAggregate'`
- <a id="geometric-relation-operator-catalog-proof"></a>Geometric relation catalogs: `RUN='^TestReferenceGeometryRelationOperatorCatalog$'`
- Geometric position catalogs: `RUN='^TestReferenceGeometryPositionOperatorCatalog$'`
- Casts: `RUN='^TestReferencePGCast'`
- Collations, languages, transforms, text search: `RUN='^TestReferencePGCollation'`, `RUN='^TestReferencePGLanguage'`, `RUN='^TestReferencePGTransform'`, `RUN='^TestReferencePGTSConfig'`
- Large-object pages: `RUN='^TestReferencePGLargeobject'`
- Visibility inquiries: `RUN='^TestReferenceVisibilityInquiry'`, `RUN='^TestReferenceBooleanCall'`
- Catalog smoke scenarios migrated from the root package: `RUN='^TestReferenceSmoke'`
