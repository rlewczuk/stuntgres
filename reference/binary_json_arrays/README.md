# reference/binary_json_arrays

JSON, JSONB and JSONPATH types, operators and processing functions; JSONPath
evaluation; SQL/JSON constructors, predicates, query functions and `JSON_TABLE`;
ARRAY types, expressions and functions (construction, transformation, search,
comparison, dimensions, `array_sample`/`array_shuffle`/`unnest`, `array_agg`);
BIT/VARBIT types, operators and functions; BYTEA types, functions, hashing, bit and
byte access and binary conversion; and the `intagg` extension. JSON aggregates
live in `reference/aggregates`; Boolean aggregates, ranges and multiranges in
`reference/types`; other string functions in `reference/strings`; the intagg
`pg_aggregate` identity in `reference/catalogs`.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md); `array_sample`/`array_shuffle`
follow its random-property rule. `TestMain` uses the default `fixture.Main(m)`.

- **Helpers.** Each family keeps its scenario, metadata and diagnostic helpers in
  `<family>_helpers_test.go` (`array_search_fixture_test.go` and
  `binary_conversion_fixture_test.go` for those families); `output_gate_test.go`
  holds `outputGateCancel`.
- **Active cancellation** uses one of three patterns:
  - `fixture.BinaryConversionActiveCancel` with the native provider frame name
    (for example `sqltype.jsonbExists`, `sqltype.JSONBSetPlan.Evaluate`,
    `sqltype.(*JSONEachCursor).Next`, `sqltype.(*jsonpathExecutor).walk`,
    `sqltype.jsonbOutput`). `BinaryConversionActiveCancelAfter` adds a progress
    callback when PostgreSQL admission must observe argument progress (a sequence
    advance), as in the JSONPath query/method/operator, SQL/JSON query and
    `JSON_TABLE` families. See the [fixture README](../fixture/README.md#helpers).
    Provider names are matched at runtime: grep this package and update the
    strings when renaming or moving a provider.
  - `outputGateCancel`: a MATERIALIZED producer maps actual output length to an
    advisory transaction lock key (2 small, 67108864 large) held by an
    independent transaction. Admission requires the worker's exact waiting lock,
    the holder's grant and `pg_blocking_pids`; writers also require the earlier
    small-output grant. It proves cancellation after output exists, not inside a
    provider. Used by `BinaryConversionActiveCancellation`,
    `JSONElementsCancellation`, `JSONPathNavigateCancellation` and
    `JSONPathArithmeticCancellation`;
    `JSONBConcatDeleteCancellation` has its own gate in
    `jsonb_concat_delete_gate_test.go`. Provider-internal cancellation belongs in
    internal units.
  - `fixture.SleepActiveCancel` for ordinary predicates (`ArrayOrderCancellation`).
- **Heavy workloads.** Each JSONPath path, JSONPath query consumer and JSONB
  record read/write/valid attempt gets its own 30-second scenario
  (`jsonpathNavigateScenario`, `jsonpathQueryScenario`,
  `jsonbRecordCancellationScenario`). Within one, admission, cancellation, join
  and same-held recovery share the context, and helpers use that callback's
  `testing.T`.
- **Arithmetic.** `jsonpath_arithmetic_helpers_test.go` fixes an exact numeric
  document and reuses the navigation scenario/direct-prepared helpers. Assert
  decimal JSON text and scale, not float-normalized parity. Binary shape and
  ordered-demand cases differ from unary materialization/prefix cases.
  Arithmetic cancellation gates completed unary or multiplicative-filter output
  over typed numeric workloads below 32MiB. Native controls retain old output,
  replay stored recipes on the same/fresh Driver and PostgreSQL, then execute
  future writes; failed/canceled Restore must leave the prior dump unchanged.
- **Client metadata.** lib/pq reports an empty `DatabaseTypeName` for
  user-defined arrays, composites and JSONPATH; `interface{}` ScanType and byte
  payloads for JSON, JSONPATH and RECORD (native: type name, string); BYTEA
  `Length` `(MaxInt64, true)` (native `(0, false)`); unsized VARCHAR length -5
  (native MaxInt64). Native reports known nullability for stored table and view
  columns; computed projections are unknown on both. Assert each backend before
  normalization.
- **ACL output.** The authenticated grantor in ACL items is `postgres` natively
  and `stuntgres` on the oracle; specify both expectations.
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
- `json[b]_populate_record[set]`, `json[b]_to_record[set]`, `jsonb_populate_record_valid`: `RUN='^TestReferenceJSONB?Record'`
- `json_strip_nulls`: `RUN='^TestReferenceJSONStripNulls'`
- `to_json[b]`, `array_to_json`, `row_to_json`, `json[b]_build_*`: `RUN='^TestReferenceJSONCreate'`
- `json_object`, `jsonb_object` over text arrays: `RUN='^TestReferenceJSONB?Object'`
- JSONPATH type, grammar and regex syntax: `RUN='^TestReferenceJSONPath(Representation|Composition|Consumers|Diagnostic|Grammar|Regex|Review|FeatureSmoke)'`
- `@?`, `@@`: `RUN='^TestReferenceJSONBPathOps'`
- `jsonb_path_query_array` and path navigation: `RUN='^TestReferenceJSONPathNavigate'`
- `jsonb_path_exists`/`match`/`query`/`query_first`: `RUN='^TestReferenceJSONPathQuery'`
- JSONPath item methods: `RUN='^TestReferenceJSONPathMethods'`
- JSONPath arithmetic: `RUN='^TestReferenceJSONPathArithmetic'`
- SQL/JSON constructors, `IS JSON`, `JSON_SERIALIZE` (also selects `SQLJSONQuery`): `RUN='^TestReferenceSQLJSON'`
- <a id="sqljson-query-functions"></a>`JSON_EXISTS`, `JSON_QUERY`, `JSON_VALUE`: `RUN='^TestReferenceSQLJSONQuery'`
- `JSON_TABLE`: `RUN='^TestReferenceJSONTable'`
- ARRAY type: literals, domains, typmods, metadata, controls: `RUN='^TestReferenceArray(Atomicity|Diagnostics|Domain|Element|Frames|Generated|Interval|Literal|Metadata|NativeControls|Output|Regclass|Structural|Syntax|Typmods|Smoke)'`
- ARRAY constructors, subscripts and assignment: `RUN='^TestReferenceArrayExpression'`
- `array_append`, `array_cat`, `array_fill`, `array_sort`, ... and array `||`: `RUN='^TestReferenceArrayTransform'`
- `array_dims`, `array_length`, bounds, `cardinality`: `RUN='^TestReferenceArrayInspect'`
- `array_position[s]`, `array_remove`, `array_replace`, `@>`, `<@`, `&&`: `RUN='^TestReferenceArraySearch'`
- IN lists and `ANY`/`SOME`/`ALL`: `RUN='^TestReferenceArrayCompare'`
- Array comparison operators: `RUN='^TestReferenceArrayOrder'`
- `array_sample`, `array_shuffle`, `unnest`: `RUN='^TestReferenceArrayRelational'`
- `array_agg`: `RUN='^TestReferenceArrayAgg'`
- `intagg` extension: `RUN='^TestReferenceIntagg'`
- BIT and VARBIT types: `RUN='^TestReferenceBitstring'`
- Bit-string operators: `RUN='^TestReferenceBitOperators'`
- Bit-string functions (`bit_count`, `length`, `position`, `substring`, `overlay`, ...): `RUN='^TestReferenceBitStringFunctions'`
- Bit-string `get_bit`, `set_bit`: `RUN='^TestReferenceBitAccess'`
- BYTEA type, input and output settings: `RUN='^TestReferenceBytea'`
- BYTEA concatenation, length and trim: `RUN='^TestReferenceBinaryBasic'`
- `md5`, `sha*`, `crc32[c]`: `RUN='^TestReferenceBinaryHash'`
- BYTEA `substring`, `overlay`, `position`: `RUN='^TestReferenceBinarySlice'`
- BYTEA bit/byte access and `reverse`: `RUN='^TestReferenceBinaryAccess'`
- `encode`, `decode`, `convert*`: `RUN='^TestReferenceBinaryConversion'`
