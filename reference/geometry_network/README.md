# reference/geometry_network

Dual-mode tests for geometry (POINT, LSEG, LINE, BOX, PATH, POLYGON, CIRCLE,
measurement functions, constructors and conversions) and network types
(INET/CIDR values, operators, mask, formatting and subnet functions; MACADDR,
MACADDR8).

Other type families: [reference/domain_types](../domain_types/README.md#testing)
(domains, `information_schema` domains, type identity lifecycle and
transactional types);
[reference/ranges_text_search](../ranges_text_search/README.md#testing) (ranges,
multiranges, text search and UUID);
[reference/extension_types](../extension_types/README.md#testing) (ISN types,
`query_int`, `ltree`, `seg` and `lo`);
[reference/extension_cube](../extension_cube/README.md#testing) (`cube`, `earth`);
[reference/extension_hstore](../extension_hstore/README.md#testing) (`hstore`);
[reference/types](../types/README.md#testing) (composite types, enums, `citext`,
ACL values, TID, BOOLEAN and comparisons).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` is plain
`fixture.Main(m)`. Each family keeps its helpers in `<family>_helpers_test.go`;
these are shared across families:
- `box_helpers_test.go`: the geometry raw adapter. `boxScenario` and `boxSelect`
  check BOX/POINT/LSEG raw cells before normalization; LSEG (`lsegScenario`),
  POINT, LINE, PATH and POLYGON tests reuse them. After changing it, rerun the
  Box, Point, Lseg, Line, Path and Polygon families.
  Shared geometry witnesses reuse the raw adapter for direct, fresh-prepared and
  held execution. Exact distances and candidate values must not be hidden by a
  global epsilon comparator. Native controls replay actual emitted SQL.
- `geometry_measure_helpers_test.go`: shared measurement scenarios check raw
  cells and metadata before normalization, with explicit per-column nullability
  provenance. The GeometryMeasure, GeometryMeasureBoxLine and
  GeometryMeasurePath families share these helpers; path-specific helpers live in
  `geometry_measure_path_helpers_test.go`.

Client quirks:

- lib/pq returns textual values as `[]byte` with ScanType `any`; native returns
  strings. Assert raw cells before normalization.
- lib/pq reports an empty `DatabaseTypeName` for types missing from its OID table
  (MACADDR8, multiranges, extension types such as `cube`); native reports the
  concrete name. Assert the raw codec independently of the name.
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

Rules:

- Catalog identity probes first read the backend's actual identities from single
  catalogs, then bound every joined catalog input by them (derived operands before
  the join). Never use LIMIT, cached OIDs or expectation-derived catalog data.
  Compare allocated OIDs through relationships, never numerically across backends.
- While a DROP waits for its AccessExclusiveLock, observe it through catalog
  queries; a new reader of the table would queue behind the DROP.
- A new or changed builtin type, operator, opclass or extension also changes the
  inventories in reference/catalogs, reference/catalog_objects and
  reference/extensions: rerun `^TestReferencePGType`,
  `^TestReferencePGOperator`, `^TestReferencePGOpclass` and, for extension types,
  `^TestReferenceAvailableExtensionsFiltering$`.


### Family selectors

- Geometry scalars: `RUN='^TestReference(Point|Lseg|Line|Box|Path|Polygon|Circle)'`
- Geometry consumers: `RUN='^TestReferenceGeometry'`
- INET/CIDR: `RUN='^TestReferenceINET'`
- MAC types: `RUN='^TestReferenceMAC'`
