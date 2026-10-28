# reference/ranges_text_search

Dual-mode tests for integer, numeric, date, timestamp and timestamptz ranges,
custom range definitions and their multiranges (operators, inspection,
`multirange`/`unnest`); text search types and operators; and UUID (values,
`gen_random_uuid`/`uuidv4`/`uuidv7`, extraction).

Other type families: [reference/domain_types](../domain_types/README.md#testing)
(domains, `information_schema` domains, type identity lifecycle and
transactional types);
[reference/geometry_network](../geometry_network/README.md#testing) (geometry
and network address types);
[reference/extension_types](../extension_types/README.md#testing) (`cube`,
`earth`, ISN types, `query_int` and `lo`);
[reference/types](../types/README.md#testing) (composite types, enums, `citext`,
ACL values, TID, BOOLEAN and comparisons).

DATE/TIME/TIMESTAMP/INTERVAL are in
[reference/temporal_types](../temporal_types/README.md#testing); binary COPY of
ranges is in [reference/dml](../dml/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` is plain
`fixture.Main(m)`. Each family keeps its helpers in `<family>_helpers_test.go`;
these are shared across families:
- `fts_query_functions_helpers_test.go` owns the shared query-utility callback
  and direct/new-prepared scalar routes. Query utilities reuse the FTS scalar
  quoting and native logical-control helpers; diagnostics assert complete
  phase-specific records, and held SQL rules use one pinned session.
- Vector utilities reuse the query utility scenario/scalar helpers and scalar
  quoting; their held routes retain typed selector and weight operands across
  executions. Select this family with
  `make check RUN='^TestReferenceFTSVectorFunctions'`.
- `range_helpers_test.go`: `rangeSelect` checks the public any-scan and the
  middleware stream before normalization (a nil nullable slice asserts unknown
  nullability, a non-nil one declared nullability) and maps multirange type names
  to lib/pq's blank names. `fixture.RangeConsumeHeld` also serves INET and UUID stored-row
  tests.
- `range_type_helpers_test.go`: custom family scenarios assert raw custom-OID
  transport independently of type-name spelling and share one backend callback.
- `range_type_constructor*_test.go`: generated two/three-bound and
  zero/single/variadic companion constructors, current routine metadata and
  internal ownership, atomic routine-name collision, actual comparator errors,
  raw FLOAT4 endpoint payloads and symbolic alias endpoint identity. Shared
  SQL checks also consume native same/fresh-engine Restore and actual dump SQL
  replayed against PostgreSQL; raw32 uses the existing xid/real binary casts.
  Custom-family cast admission is identified by the actual range definition;
  it does not widen existing builtin-range explicit-cast gates or diagnostics.
  The pg_operator polymorphic identity scenario shares custom-range CREATE,
  storage and containment success assertions across both backends.
- `range_type_*remedy*`, companion ACL, schema retirement, recipe-collision,
  alias-ordering, ownership and generated-cast scenarios cover the full family
  lifecycle. Companion grants reject independently; real type-use checks and
  privilege inquiries prove range/array/multirange delegation. Explicit range
  ACLs retain exact grantor/grantee provenance through emitted catalog repairs
  and native replacement, alongside baseline family roundtrips.
  Alias collision fixtures match both TEMP namespace allocations, assert actual
  object identities and preserve finite bounds and multirange ordering.
  Dangling-numeric witnesses derive their sentinels from the source catalog:
  unrelated namespace collisions must retain feasible noncolliding endpoints;
  only a genuinely impossible destination assignment rejects atomically. Routine
  owner scenarios alter each generated signature independently, retain the other
  four owners, and require zero-output Dump rejection without losing authority,
  dependencies or readers; restoring the original owner recovers roundtrip.
  Select the shared scenarios with
  `make check RUN='^TestReferenceRangeType(CompanionACL|SchemaRetirement|RestoreRecipeCollisions|AliasOrdering|AliasNumericAnchor|UnrelatedCollisionFeasible|ConstructorOwnerPortability|Ownership|GeneratedCast)'`,
  then repeat with `REFERENCE=1`. Ordinary symbolic and large-numeric
  emitted streams replay on pinned PostgreSQL, not hand-written substitutes.
  The unsatisfiable numeric stream proves actual PG source validity and native
  atomic rejection, not unconditional portability across PG allocator frontiers.
- `range_control_helpers_test.go`: `rangeControlCycle`, the native control cycle
  (same/fresh-Driver Dump/Reset/Restore, PostgreSQL replay of the emitted SQL,
  deterministic redump, failed staged replacement, retained readers) used by the
  date, timestamp and timestamptz range, RangeInspect, RangeMore and
  RangeOperator (`operatorControlCycle`) controls.

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

Add `REFERENCE=1` for the oracle. The range-family prefixes also run their binary
COPY test in reference/dml.

### Family selectors

- INT4RANGE/INT8RANGE: `RUN='^TestReferenceIntegerRange'`
- NUMRANGE: `RUN='^TestReferenceNumericRange'`
- DATERANGE: `RUN='^TestReferenceDateRange'`
- TSRANGE: `RUN='^TestReferenceTimestampRange'`
- TSTZRANGE: `RUN='^TestReferenceTimestamptzRange'`
- Multiranges: `RUN='^TestReferenceMultirange'`
- Custom range definitions and companions: `RUN='^TestReferenceRangeType'`
- Custom family constructors and exact endpoint replay:
  `RUN='^TestReferenceRangeType(Constructors|ConstructorDiagnostics|ConstructorFloat4|ConstructorAliases)$'`
- Range operators: `RUN='^TestReferenceRangeOperator'`
- <a id="range-inspection-selected-proof"></a>Range inspection: `RUN='^TestReferenceRangeInspect'`
- `multirange(anyrange)`, `unnest(anymultirange)`: `RUN='^TestReferenceRangeMore'`
- Text search types: `RUN='^TestReferenceFTSTypes'`
- Text search operators: `RUN='^TestReferenceFTSOps'`
- Text search query utilities: `RUN='^TestReferenceFTSQueryFunctions'`
- Text search conversion: `RUN='^TestReferenceFTSConvert'`; shared configuration
  callbacks own pinned-session settings, typed metadata and held statements.
  Exact diagnostics distinguish input/coercion from constructor execution;
  notice phases distinguish first-only immutable folds from stable/runtime calls.
- Text search document parser: `RUN='^TestReferenceFTSParser'`
  Parser definition lifecycle, checked callback execution, diagnostics, privileges,
  dependencies and native replacement/replay share `RUN='^TestReferenceFTSParserDDL'`.
- English Snowball configuration mappings and seed replay:
  `RUN='^TestReferenceFTSSnowball'`.
  Mapping proofs join actual parser aliases to catalog mappings and specify
  the six English and thirteen simple aliases independently of numeric ordering.
- Text search configurations: `RUN='^TestReferenceFTSConfiguration'`; configuration
  helpers own pinned-session lifecycle, ordered mapping rows and typed conversion
  assertions. Diagnostics keep complete records and literal, bound-input and
  runtime-cast phases distinct; unique-key details use independently queried
  catalog identities. Held conversion separates runtime mapping changes from
  warmed immutable all-constant results. Native control phases exercise actual
  emitted SQL, same/fresh Driver replacement, retained readers, current owner/
  dictionary edges, seed reset/replay and exact retained temporary-class collisions.
- UUID (excluding `uuid-ossp`): `RUN='^TestReferenceUUID([A-NP-Z]|Or)'`
- <a id="uuid-extraction-selected-proof"></a>UUID extraction: `RUN='^TestReferenceUUIDExtract'`
