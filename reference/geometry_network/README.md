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
  Geometry relations reuse `geometryRelationScenario`, `geometryRelationWitness`
  and this raw adapter for direct/new-prepared/reused-held execution, NULL/empty
  metadata and stored consumers. Distance/candidate expectations are exact;
  epsilon comparisons must not hide endpoint-order differences. Native control
  phases execute actual emitted SQL on PostgreSQL and retain deterministic dumps.
  `TestReferenceGeometryRelationClosingEdge` keeps exact horizontal/vertical
  closing-anchor distances1 for POINT/POLYGON and0.75 for radius0.25
  CIRCLE/POLYGON, in both operand orders. `TestReferenceGeometryRelationInvalidUnknown`
  retains same-type LSEG inference and exact22P02/character8 input failure for
  `'(1,1)' ## lseg '[(0,0),(2,0)]'` on direct analysis and Prepare.
  Geometry positions reuse the same witnesses for all32 signatures, including
  vertical corresponding-edge extent predicates, touching BOX aliases versus
  strict POINT aliases, polygon raw bounds versus fuzzy positions, and complete
  overload diagnostics. Shared stored generated/CHECK/view expressions retain
  non-NULL vertical computation through replay and new writes. The shared-spelling
  cases retain shift, range, network and custom-operator selection.
  Alignment uses the same fixture/raw adapter for unary and binary LINE/LSEG
  execution, physical columns, overload diagnostics and stored replay. Sameness
  witnesses distinguish corner-local NaNs and vertex sequence matching from
  area-based comparison. Select this family with
  `make check RUN='^TestReferenceGeometry(Alignment|SamenessArea)'`.
  Geometric transforms reuse this adapter through `geometry_transform_helpers_test.go`
  for POINT/BOX/PATH/CIRCLE raw results and metadata. Independent value, numeric-edge,
  corner, concatenation, diagnostic and stored-replay witnesses share direct,
  prepared and retained-statement routes. Select them with
  `make check RUN='^TestReferenceGeometryTransform'`.
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
- A typed-nil `[]byte` bound to FLOAT8 or to an inferred TEXT parameter is empty
  input natively but NULL through lib/pq; do not use it as a shared NULL witness.
- lib/pq fails on an oversized ErrorResponse; obtain such a server diagnostic on
  PostgreSQL with `pg_input_error_info`.
- A pre-canceled lib/pq read can race database/sql's cancellation watcher: check
  the available metadata and ordered prefix, accept `context.Canceled` or
  `driver.ErrBadConn`, and require full rows only on clean EOF. Native
  pre-cancellation returns `context.Canceled` without a cursor. Cache
  `ColumnTypes` while the cursor is open to check descriptors after EOF/Close.
- PostgreSQL moves a repeatedly executed held statement to a generic plan that
  folds all-constant immutable calls, so that route keeps succeeding after
  EXECUTE revocation. Encode this only for the same-held PostgreSQL route
  (`pgWarmConstant` in `inet_format_authorization_test.go`).

Rules:

- Catalog identity probes first read the backend's actual identities from single
  catalogs, then bound every joined catalog input by them (derived operands before
  the join). Never use LIMIT, cached OIDs or expectation-derived catalog data.
  Compare allocated OIDs through relationships, never numerically across backends.
- While a DROP waits for its AccessExclusiveLock, observe it through catalog
  queries; a new reader of the table would queue behind the DROP.
- `polygon(circle)` coordinates have recorded backend-specific last-bit spellings
  (`geometryConversionCPGCircle`); `geometryConversionCCoordinates` also checks
  each coordinate within relative 1e-14 with exact sign and nonfinite category.
- A new or changed builtin type, operator, opclass or extension also changes the
  inventories in reference/catalogs, reference/catalog_objects and
  reference/extensions: rerun `^TestReferencePGType`,
  `^TestReferencePGOperator`, `^TestReferencePGOpclass` and, for extension types,
  `^TestReferenceAvailableExtensionsFiltering$`.

Path measurement scenarios cover all eight `isclosed/isopen/pclose/popen(path)`,
`length(path/lseg)` and `npoints(path/polygon)` signatures. Closing/reopening a
triangle preserves coordinates and counts while lengths change 9→12→9 and areas
NULL→6→NULL. Coverage includes strict NULL and degenerate/nonfinite inputs,
diagnostics, direct/prepared/same-held calls, BOOL/INT4/PATH/FLOAT8 metadata before
Next (including empty/all-NULL results), and retained text/bytea/bit/varbit length
selection. Fixed-signature scalar VARIADIC syntax remains callable; scalar OVER
reaches the function diagnostic rather than a parser error.

Stored default/generated/CHECK/view/SQL-wrapper consumers and native controls
exercise same/fresh-Driver Restore, actual dump SQL replay on PostgreSQL and new
writes after replacement. Literal and typed-NULL calls retain all eight selected
overloads, including FLOAT8 LSEG length and unambiguous POLYGON point counts.

### Family selectors

- <a id="point-oracle-first-contract"></a>POINT: `RUN='^TestReferencePoint'`
- <a id="lseg-oracle-first-contract"></a>LSEG: `RUN='^TestReferenceLseg[A-NP-Z]'`
- LINE: `RUN='^TestReferenceLine'`
- <a id="box-scalar-contract"></a>BOX: `RUN='^TestReferenceBox'`
- PATH: `RUN='^TestReferencePath'`
- POLYGON: `RUN='^TestReferencePolygon'`
- <a id="circle-scalar-contract"></a>CIRCLE: `RUN='^TestReferenceCircle'`
- <a id="geometric-measurement-oracle-contract"></a>Geometry measurement functions: `RUN='^TestReferenceGeometryMeasure'`
- <a id="geometric-relation-operator-oracle-contract"></a>Geometry relations: `RUN='^TestReferenceGeometryRelation'`
- Geometry positions: `RUN='^TestReferenceGeometryPosition'`
- <a id="geometric-constructor-conversion-contract"></a>Geometry constructors and conversions: `RUN='^TestReferenceGeometryConversion'`
- INET/CIDR (all): `RUN='^TestReferenceINET'`
- INET operators, mask, formatting, subnet: `RUN='^TestReferenceINET(Operator|Mask|Format|Subnet)'`
- MACADDR: `RUN='^TestReferenceMACADDR[A-Z]'`
- MACADDR8: `RUN='^TestReferenceMACADDR8'`
- <a id="mac-address-operations"></a>MAC bitwise operators, truncation and modified EUI-64:
  `RUN='^TestReferenceMACOperations'`. See the
  [MAC contract](../../internal/sqltype/README.md#mac-address-operations).
