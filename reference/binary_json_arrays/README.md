# reference/binary_json_arrays

JSON and JSONB types, operators and processing functions; SQL/JSON
constructors, predicates and query functions; and the `intagg` extension.
Collection/record-producing array and JSON record consumers live in
[cursors](../cursors/README.md#testing); BYTEA and binary codecs in
[temporal_types](../temporal_types/README.md#testing); BIT/VARBIT in
[strings](../strings/README.md#testing); JSONPath and `@?`/`@@` in
[datetime_formatting](../datetime_formatting/README.md#testing); `JSON_TABLE`
in [query_outer](../query_outer/README.md#testing). JSON aggregates live in
[aggregates](../aggregates/README.md#testing); the intagg `pg_aggregate`
identity is in [catalog_objects](../catalog_objects/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses the default `fixture.Main(m)`.

- **Helpers.** Family-local scenario helpers remain in `<family>_helpers_test.go`.
  Cross-family runners, JSON metadata, quoting and output cancellation are owned
  once by [reference/fixture](../fixture/README.md#helpers), including
  `JSONEachBoth`, `JSONKeysBoth`, `JSONKeysError`, `JSONTypeMetadataCheck`,
  `JSONTypeWithMetadata`, `JSONTypeSourceColumn`, `JSONBObjectMetadata`,
  `JSONBSetMetadataStep`, `JSONReferenceQuote`, `JSONReferenceValuer`,
  `SQLJSONBoth`, `SQLJSONDiagnostic`, `SQLJSONQueryOne` and `OutputGateCancel`.
- **Active cancellation and pause controls:**
  - `fixture.BinaryConversionActiveCancel` with a typed public work class,
    such as `stuntgres.WorkJSONBExistence` or `stuntgres.WorkJSONBObjectExpand`.
    Its synchronous real provider admission policy cannot be satisfied by input scans.
    `BinaryConversionActiveCancelAfter` adds a progress callback when PostgreSQL
    admission must observe argument progress (a sequence advance), as in the
    JSONPath query/operator, SQL/JSON query and `JSON_TABLE` families. See the
    [fixture README](../fixture/README.md#helpers).
    The typed class contract is documented in
    [DRIVER](../../docs/DRIVER.md#cooperative-work-admission).
  - `fixture.OutputGateCancel`: a MATERIALIZED producer maps actual output length to an
    advisory transaction lock key (2 small, 67108864 large) held by an
    independent transaction. Admission requires the worker's exact waiting lock,
    the holder's grant and `pg_blocking_pids`; writers also require the earlier
    small-output grant. It proves cancellation after output exists, not inside a
    provider. Used by `BinaryConversionActiveCancellation`,
    `JSONElementsCancellation`, `JSONBInsertCancellation`,
    `JSONBSetCancellation`, `JSONPathNavigateCancellation`,
    `JSONPathMethodsCancellation` and `JSONPathArithmeticCancellation`;
    `JSONBConcatDeleteCancellation` has its own gate in
    `jsonb_concat_delete_gate_test.go`. Provider-internal cancellation belongs in
    internal units.
    JSONB insert/set and item-method reads gate MATERIALIZED selected-operation
    results by their actual text length; writes gate destination values in a
    CHECK after the ordered small row. Both require completed large output
    before cancellation; writers also require the earlier small-output grant
    and assert no partial insertion.
  - `fixture.SleepActiveCancel` for ordinary predicates (`ArrayOrderCancellation`).
  - The distinct closed `ExecutionPause` capability, normal Resume, fresh
    cursor FETCH/HOLD demand and canceled writes: `RUN='^TestReferenceExecutionPause'`.
    Native control phases retain independently expected SQL/metadata and
    held-owner recovery on both backends; canceled cursor transactions recover
    by full rollback.
- **Client metadata.** lib/pq reports an empty `DatabaseTypeName` for
  user-defined arrays, composites and JSONPATH; `interface{}` ScanType and byte
  payloads for JSON, JSONPATH and RECORD (native: type name, string); BYTEA
  `Length` `(MaxInt64, true)` (native `(0, false)`); unsized VARCHAR length -5
  (native MaxInt64). Native reports known nullability for stored table and view
  columns; computed projections are unknown on both. Assert each backend before
  normalization.
- **Oracle defect.** For `JSON_SERIALIZE(jsonb)` the oracle returns an internal
  byte instead of JSON text (`"\x02"` for the bounded fixture input;
  [upstream report](https://www.postgresql.org/message-id/03583FE7-A5EE-407C-907D-67865F3F8BFC%40gmail.com)).
  `TestReferenceSQLJSONConversion` asserts canonical JSON natively and that byte
  on PostgreSQL; `jsonb::json` is the canonical control on both.

Add `REFERENCE=1` for the oracle.

- JSON and JSONB types: I/O, casts, comparison, ordering, storage: `RUN='^TestReferenceJSONB?(Representation|Casts|IOCasts|Bound|Invalid|Metadata|Null|Arrays|Numeric|Comparison|Ordering|Grouping|Diagnostic|Capability|Catalog|Context|Controls|Cancellation|Review|FeatureSmoke)'`
- `->`, `->>`, `#>`, `#>>`, `json[b]_extract_path[_text]`: `RUN='^TestReferenceJSONB?Extract'`
- JSONB `@>`, `<@`: `RUN='^TestReferenceJSONBContainment'`
- JSONB `?`, `?|`, `?&`: `RUN='^TestReferenceJSONBExists'`
- JSONB `||` and `-`: `RUN='^TestReferenceJSONBConcatDelete'`
- JSONB `#-`: `RUN='^TestReferenceJSONBPathDelete'`
- `jsonb_insert`: `RUN='^TestReferenceJSONBInsert'`
- `jsonb_set`, `jsonb_set_lax`: `RUN='^TestReferenceJSONBSet'`
- `jsonb_pretty`: `RUN='^TestReferenceJSONBPretty'`
- `json[b]_array_elements[_text]`: `RUN='^TestReferenceJSONB?Elements'`
- `json[b]_each[_text]`: `RUN='^TestReferenceJSONB?Each'`
- `json[b]_object_keys`: `RUN='^TestReferenceJSONB?Keys'`
- `json[b]_typeof`, array length and object keys: `RUN='^TestReferenceJSONType'`
- `json_array_length`, `jsonb_array_length`: `RUN='^TestReferenceJSONArrayLength'`
- `json_strip_nulls`: `RUN='^TestReferenceJSONStripNulls'`
- `to_json[b]`, `array_to_json`, `row_to_json`, `json[b]_build_*`: `RUN='^TestReferenceJSONCreate'`
- `json_object`, `jsonb_object` over text arrays: `RUN='^TestReferenceJSONB?Object'`
- SQL/JSON constructors, `IS JSON`, `JSON_SERIALIZE` (also selects `SQLJSONQuery`): `RUN='^TestReferenceSQLJSON'`
- <a id="sqljson-query-functions"></a>`JSON_EXISTS`, `JSON_QUERY`, `JSON_VALUE`: `RUN='^TestReferenceSQLJSONQuery'`
- `intagg` extension: `RUN='^TestReferenceIntagg'`
