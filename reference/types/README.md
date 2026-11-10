# reference/types

Dual-mode scenarios for composite, enum and scalar utility types, installed citext,
ACL values, catalog vectors and comparison consumers.

Other type families: [reference/domain_types](../domain_types/README.md#testing)
(domains, `information_schema` domains, type identity lifecycle and
transactional types);
[reference/geometry_network](../geometry_network/README.md#testing) (geometry
and network address types);
[reference/ranges_text_search](../ranges_text_search/README.md#testing) (ranges,
multiranges, text search and UUID);
[reference/extension_types](../extension_types/README.md#testing) (ISN types,
`query_int`, `ltree`, `seg` and `lo`);
[reference/extension_cube](../extension_cube/README.md#testing) (`cube`, `earth`);
[reference/extension_hstore](../extension_hstore/README.md#testing) (`hstore`).

Related families elsewhere: character types in
[reference/strings](../strings/README.md#testing); numeric types in
[reference/numeric](../numeric/README.md#testing); DATE/TIME/TIMESTAMP/INTERVAL
and BYTEA in [reference/temporal_types](../temporal_types/README.md#testing);
bit strings in [strings](../strings/README.md#testing), arrays and JSON record
consumers in [cursors](../cursors/README.md#testing), JSON in
[binary_json_arrays](../binary_json_arrays/README.md#testing), JSONPath in
[datetime_formatting](../datetime_formatting/README.md#testing); OID
aliases, NAME/OID and the comparison resolution matrix in
[reference/type_resolution](../type_resolution/README.md#testing); `pg_type`
inventories in [reference/catalogs](../catalogs/README.md#testing); `pg_operator`
and `pg_opclass` inventories in
[reference/catalog_objects](../catalog_objects/README.md#testing); extension
availability and `uuid-ossp` in
[reference/extensions](../extensions/README.md#testing); binary COPY of
ranges in [reference/dml](../dml/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` is plain
`fixture.Main(m)`. Each family keeps its helpers in `<family>_helpers_test.go`.

Client quirks:

- lib/pq returns most textual values as `[]byte` with ScanType `any`; native
  returns strings. Internal CHAR returns raw strings on both backends, with
  ScanType `any` through lib/pq. Assert raw cells before normalization.
- Unbounded VARCHAR-based columns report Length `(MaxInt64,true)` natively and
  `(-5,true)` through lib/pq.
- A typed-nil `[]byte` bound to TEXT is SQL NULL on both backends; nonnil empty
  bytes remain empty input. FLOAT8 retains its separate native empty-input
  versus lib/pq NULL transport policy.
- lib/pq fails on an oversized ErrorResponse; obtain such a server diagnostic on
  PostgreSQL with `pg_input_error_info`.
- A pre-canceled lib/pq read can race database/sql's cancellation watcher: check
  the available metadata and ordered prefix, accept `context.Canceled` or
  `driver.ErrBadConn`, and require full rows only on clean EOF. Native
  pre-cancellation returns `context.Canceled` without a cursor. Cache
  `ColumnTypes` while the cursor is open to check descriptors after EOF/Close.
- Omitted-grantor ACL output names the bootstrap role: `stuntgres` on PostgreSQL,
  `postgres` natively. Compare ACLITEM arrays as exact bags; element order is not
  a contract.

Rules:

- Catalog identity probes first read the backend's actual identities from single
  catalogs, then bound every joined catalog input by them (derived operands before
  the join). Never use LIMIT, cached OIDs or expectation-derived catalog data.
  Compare allocated OIDs through relationships, never numerically across backends.
- Compare temporal defaults and relative inputs with `CURRENT_TIMESTAMP` or
  `CURRENT_DATE` captured in the same transaction, never with a wall-clock
  tolerance.
- While a DROP waits for its AccessExclusiveLock, observe it through catalog
  queries; a new reader of the table would queue behind the DROP.
- A new or changed builtin type, operator, opclass or extension also changes the
  inventories in reference/catalogs, reference/catalog_objects and
  reference/extensions: rerun `^TestReferencePGType`,
  `^TestReferencePGOperator`, `^TestReferencePGOpclass` and, for extension types,
  `^TestReferenceAvailableExtensionsFiltering$`.

### Family selectors

- Composites: `RUN='^TestReferenceComposite'`
- Enums: `RUN='^TestReferenceEnum'`
- Citext: `RUN='^TestReferenceCitext'`
- ACL values: `RUN='^TestReferenceACL(Item|Values)'`
- TID: `RUN='^TestReferenceTID'`
- Internal CHAR: `RUN='^TestReferenceInternalChar'`
- Catalog vectors: `RUN='^TestReferenceCatalogVectors'`
- Boolean: `RUN='^TestReferenceBoolean'`
- Comparison predicates: `RUN='^TestReferenceCompare(Null|Ops)'`
