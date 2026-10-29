# reference/datetime_formatting

Dual-mode consumers of date/time formatting and template parsing (`to_char`,
`to_date`, `to_timestamp`), plus JSONPATH representation, grammar and evaluation:
`@?`/`@@`, path navigation/query, item methods, temporal methods and arithmetic.
Scalar date/time values belong to [temporal_types](../temporal_types/README.md#testing);
other date/time functions to [datetime](../datetime/README.md#testing).
JSON/JSONB codecs and SQL/JSON scalar functions remain in
[binary_json_arrays](../binary_json_arrays/README.md#testing); JSON_TABLE
relational sources are in [query_outer](../query_outer/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses `fixture.Main(m)`.

- Formatting uses `datetime_formatting_fixture_test.go`: `datetimeFormattingScenario`,
  `datetimeFormattingCases` and explicit SQL-keyed contract maps split by semantic
  family. The same independent expectations serve direct and prepared execution.
- JSONPath family-local helpers remain in `jsonpath_*_helpers_test.go`.
  Cross-family quoting, metadata, direct/prepared and replay helpers use the
  single-owner [fixture](../fixture/README.md#helpers): `JSONReferenceQuote`,
  `JSONReferenceValuer`, `JSONBSetMetadataStep`, `JSONBObjectMetadata`,
  `JSONPathNavigateBoth`, `JSONPathNavigateReplay`, `JSONPathNavigateRawCheck`
  and `JSONPathNavigateViewMetadataStep`.
- Every heavy path/query attempt owns its scenario, context and callback
  `testing.T`; admission, cancellation, joins and same-held recovery share it.
  `fixture.BinaryConversionActiveCancel`/`BinaryConversionActiveCancelAfter`
  require actual provider admission. `fixture.OutputGateCancel` proves
  cancellation after completed output, not entry inside a provider.
- Arithmetic asserts exact decimal JSON text and scale, never float-normalized
  parity. Unary materialization/prefix cases remain distinct from binary shape
  and ordered-demand cases. Typed arithmetic cancellation workloads stay below
  32MiB and gate completed output.
- Integer/bigint and decimal item-method scenarios assert exact signed bounds,
  lexical versus numeric conversions, retained decimal scale and typmod demand.
  Full diagnostics, prefix suppression, held inputs and persistent recipes use
  the same shared fixture contracts as the other item methods.
- Native controls retain old output, replay actual stored recipes on the same
  and fresh Driver and PostgreSQL, then exercise future writes. Failed/canceled
  Restore leaves the prior dump unchanged.
- pq reports empty JSONPATH `DatabaseTypeName`, `interface{}` ScanType and byte
  payloads; native reports the type name and strings. Assert each before
  normalization, including stored versus computed nullability.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Date/time formatting and template parsing: `RUN='^TestReferenceDatetimeFormatting'`
- JSONPATH type, grammar and regex syntax: `RUN='^TestReferenceJSONPath(Representation|Composition|Consumers|Diagnostic|Grammar|Regex|Review|FeatureSmoke)'`
- `@?`, `@@`: `RUN='^TestReferenceJSONBPathOps'`
- `jsonb_path_query_array` and path navigation: `RUN='^TestReferenceJSONPathNavigate'`
- `jsonb_path_exists`/`match`/`query`/`query_first`: `RUN='^TestReferenceJSONPathQuery'`
- JSONPath item methods: `RUN='^TestReferenceJSONPathMethods'`
- JSONPath integer/bigint/decimal methods: `RUN='^TestReferenceJSONPathIntegerDecimal'`
- JSONPath temporal methods: `RUN='^TestReferenceJSONPathDatetime'`
- JSONPath arithmetic: `RUN='^TestReferenceJSONPathArithmetic'`
