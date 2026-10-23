# reference/types

Dual-mode tests for builtin and extension data types: geometry (POINT, LSEG,
LINE, BOX, PATH, POLYGON, CIRCLE, measurement functions, constructors and
conversions); network types (INET/CIDR values, operators, mask, formatting and
subnet functions; MACADDR, MACADDR8); domains (CREATE/ALTER DOMAIN defaults,
nullability, CHECK and identity lifecycle); composite types (declarations, field
expressions, ALTER TYPE attributes); enums (values, ALTER TYPE, support
functions); integer, numeric, date, timestamp and timestamptz ranges and their
multiranges (operators, inspection, `multirange`/`unnest`); text search types and
operators; extension types (`cube`, `earth`, `citext`, `query_int`); ACL values
(`aclitem`, `makeaclitem`, `acldefault`, `aclexplode`); TID; UUID (values,
`gen_random_uuid`/`uuidv4`/`uuidv7`, extraction); BOOLEAN diagnostics and
aggregates; the `information_schema` domains; and scalar comparison operators and
null/distinct predicates.

Related families elsewhere: character types in
[reference/strings](../strings/README.md#testing); numeric types in
[reference/numeric](../numeric/README.md#testing); DATE/TIME/TIMESTAMP/INTERVAL
in [reference/temporal_types](../temporal_types/README.md#testing); bit strings,
BYTEA, JSON and arrays in
[reference/binary_json_arrays](../binary_json_arrays/README.md#testing); OID
aliases, NAME/OID and the comparison resolution matrix in
[reference/type_resolution](../type_resolution/README.md#testing); `pg_type`,
`pg_operator` and `pg_opclass` inventories, extension availability and
`uuid-ossp` in [reference/catalogs](../catalogs/README.md#testing); binary COPY of
ranges in [reference/dml](../dml/README.md#testing).

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
- `domain_helpers_test.go`: the shared domain fixture `domainReferenceScenario`
  (a pinned notice session per backend with schema `domain_s` and a one-row
  `public.one`; schema cleanup is registered before borrowing so handles close
  first). Most `domain_*` files use it; after changing it, run `^TestReferenceDomain`.
- `range_helpers_test.go`: `rangeSelect` checks the public any-scan and the
  middleware stream before normalization (a nil nullable slice asserts unknown
  nullability, a non-nil one declared nullability) and maps multirange type names
  to lib/pq's blank names. `rangeConsumeHeld` also serves INET and UUID stored-row
  tests.
- `range_control_helpers_test.go`: `rangeControlCycle`, the native control cycle
  (same/fresh-Driver Dump/Reset/Restore, PostgreSQL replay of the emitted SQL,
  deterministic redump, failed staged replacement, retained readers) used by the
  date, timestamp and timestamptz range, RangeInspect, RangeMore and
  RangeOperator (`operatorControlCycle`) controls.
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
- Unbounded VARCHAR-based columns report Length `(MaxInt64,true)` natively and
  `(-5,true)` through lib/pq.
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
- `polygon(circle)` coordinates have recorded backend-specific last-bit spellings
  (`geometryConversionCPGCircle`); `geometryConversionCCoordinates` also checks
  each coordinate within relative 1e-14 with exact sign and nonfinite category.
- A new or changed builtin type, operator, opclass or extension also changes the
  inventories in reference/catalogs: rerun `^TestReferencePGType`,
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

Add `REFERENCE=1` for the oracle. The range-family prefixes also run their binary
COPY test in reference/dml.

- <a id="point-oracle-first-contract"></a>POINT: `RUN='^TestReferencePoint'`
- <a id="lseg-oracle-first-contract"></a>LSEG: `RUN='^TestReferenceLseg[A-NP-Z]'`
- LINE: `RUN='^TestReferenceLine'`
- <a id="box-scalar-contract"></a>BOX: `RUN='^TestReferenceBox'`
- PATH: `RUN='^TestReferencePath'`
- POLYGON: `RUN='^TestReferencePolygon'`
- <a id="circle-scalar-contract"></a>CIRCLE: `RUN='^TestReferenceCircle'`
- <a id="geometric-measurement-oracle-contract"></a>Geometry measurement functions: `RUN='^TestReferenceGeometryMeasure'`
- <a id="geometric-relation-operator-oracle-contract"></a>Geometry relations: `RUN='^TestReferenceGeometryRelation'`
- <a id="geometric-constructor-conversion-contract"></a>Geometry constructors and conversions: `RUN='^TestReferenceGeometryConversion'`
- INET/CIDR (all): `RUN='^TestReferenceINET'`
- INET operators, mask, formatting, subnet: `RUN='^TestReferenceINET(Operator|Mask|Format|Subnet)'`
- MACADDR: `RUN='^TestReferenceMACADDR[A-Z]'`
- MACADDR8: `RUN='^TestReferenceMACADDR8'`
- <a id="mac-address-operations"></a>MAC bitwise operators, truncation and modified EUI-64:
  `RUN='^TestReferenceMACOperations'`. See the
  [MAC contract](../../internal/sqltype/README.md#mac-address-operations).
- Domains (all): `RUN='^TestReferenceDomain'`
- Domain defaults: `RUN='^TestReferenceDomainDefault'`
- Domain CHECK: `RUN='^TestReferenceDomainCheck'`
- ALTER DOMAIN: `RUN='^TestReferenceDomainAlter'`
- Domain identity lifecycle: `RUN='^TestReferenceDomainIdentity'`
- Composite types (all): `RUN='^TestReferenceComposite'`
- Composite field expressions: `RUN='^TestReferenceCompositeExpr'`
- ALTER TYPE attributes: `RUN='^TestReferenceCompositeAlter'`
- Enums (all): `RUN='^TestReferenceEnum'`
- ALTER TYPE for enums: `RUN='^TestReferenceEnumAlter'`
- Enum support functions: `RUN='^TestReferenceEnumFunctions'`
- INT4RANGE/INT8RANGE: `RUN='^TestReferenceIntegerRange'`
- NUMRANGE: `RUN='^TestReferenceNumericRange'`
- DATERANGE: `RUN='^TestReferenceDateRange'`
- TSRANGE: `RUN='^TestReferenceTimestampRange'`
- TSTZRANGE: `RUN='^TestReferenceTimestamptzRange'`
- Multiranges: `RUN='^TestReferenceMultirange'`
- Range operators: `RUN='^TestReferenceRangeOperator'`
- <a id="range-inspection-selected-proof"></a>Range inspection: `RUN='^TestReferenceRangeInspect'`
- `multirange(anyrange)`, `unnest(anymultirange)`: `RUN='^TestReferenceRangeMore'`
- Text search types: `RUN='^TestReferenceFTSTypes'`
- Text search operators: `RUN='^TestReferenceFTSOps'`
- `cube`: `RUN='^TestReferenceCube'`
- <a id="earth-domain-contract"></a>`earth`: `RUN='^TestReferenceEarth'`
- <a id="citext-storage-and-value-round-trips"></a>`citext`: `RUN='^TestReferenceCitext'`
- `query_int`: `RUN='^TestReferenceQueryInt'`
- `aclitem`: `RUN='^TestReferenceACLItem'`
- ACL construction and expansion: `RUN='^TestReferenceACLValues'`
- TID: `RUN='^TestReferenceTID'`
- UUID (excluding `uuid-ossp`): `RUN='^TestReferenceUUID([A-NP-Z]|Or)'`
- <a id="uuid-extraction-selected-proof"></a>UUID extraction: `RUN='^TestReferenceUUIDExtract'`
- BOOLEAN diagnostics and aggregates: `RUN='^TestReference(Boolean(Aggregate|Diagnostics)|AnyValueSourceACL)'`
- Comparison operators, null/distinct predicates: `RUN='^TestReferenceCompare(Null|Ops)'`
- `information_schema` domains: `RUN='^TestReferenceInfo(CardinalNumber|CharacterData|SQLIdentifier|TimeStamp|YesOrNo)'`
