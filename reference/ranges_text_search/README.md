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
[reference/extension_types](../extension_types/README.md#testing) (ISN types,
`query_int`, `ltree`, `seg` and `lo`);
[reference/extension_cube](../extension_cube/README.md#testing) (`cube`, `earth`);
[reference/extension_hstore](../extension_hstore/README.md#testing) (`hstore`);
[reference/types](../types/README.md#testing) (composite types, enums, `citext`,
ACL values, TID, BOOLEAN and comparisons).

DATE/TIME/TIMESTAMP/INTERVAL are in
[reference/temporal_types](../temporal_types/README.md#testing); binary COPY of
ranges is in [reference/dml](../dml/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` is plain
`fixture.Main(m)`. Each family keeps its helpers in `<family>_helpers_test.go`.
- Family-local range and FTS helpers own scalar/raw transport, metadata and
  complete phase-specific diagnostics. Shared pinned sessions, held statements
  and retained-row helpers remain in the fixture.
- `rangeSelect` checks raw/public and middleware streams before normalization.
  A nil nullable slice means unknown nullability; nonnil means declared
  nullability. `fixture.RangeConsumeHeld` also serves INET and UUID consumers.
- `rangeControlCycle` shares same/fresh-Driver replacement, deterministic redump,
  failed replacement and actual emitted-SQL PostgreSQL replay.
- FTS rank REAL results require independent recovered binary32 bits: pq parses
  shortest-decimal text while native widens binary32 exactly.
- Active cancellation distinguishes actual provider entry from completed-output
  gates. Sampling alone does not hold worker lifetime; cancel/join before cleanup.

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

- Builtin ranges: `RUN='^TestReference(Integer|Numeric|Date|Timestamp|Timestamptz)Range'`
- Multiranges: `RUN='^TestReferenceMultirange'`
- Custom families: `RUN='^TestReferenceRangeType'`
- Range operators/inspection: `RUN='^TestReferenceRange(Operator|Inspect|More)'`
- Text search: `RUN='^TestReferenceFTS'`
- Text-search settings: `RUN='^TestReferenceTextSearchSetting'`
- Thesaurus: `RUN='^TestReferenceThesaurus'`
- UUID: `RUN='^TestReferenceUUID([A-NP-Z]|Or)'`

Text-search resources are real fixture files. Replay requires the actual native
emitted SQL; PostgreSQL-only discovery cannot supply that stream.
