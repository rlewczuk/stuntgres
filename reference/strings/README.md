# reference/strings

Dual-mode scenarios for character/string and bit-string types, scalar functions,
XML scalar consumers and XML row mapping. Pattern matching and formatting belong
to [patterns_encoding](../patterns_encoding/README.md#testing); BYTEA to
[temporal_types](../temporal_types/README.md#testing); relational XPath consumers
to [query_outer](../query_outer/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` is plain
`fixture.Main(m)`.

- A typed-nil `[]byte` bound to a TEXT parameter is SQL NULL on both backends;
  nonnil empty bytes remain empty TEXT. Assert both through direct and held
  calls without selecting signatures by Go kind.
- Bare unresolved-parameter diagnostics use explicit Prepare. An argument-free
  `database/sql` call is not a PostgreSQL simple-query protocol witness; retain
  the adapter's ordinary inference and missing-argument contract separately.
- Release-specific Unicode diagnostic differences are documented in
  [POSTGRES_REGRESSION.md](../../docs/POSTGRES_REGRESSION.md#unicode-escape-error-cursors-can-split-valid-utf8-input).

Add `REFERENCE=1` for the oracle.

- Character types: `RUN='^TestReferenceCharacter'`
- String literals and Unicode escapes: `RUN='^TestReference(StringLiteral|UnicodeEscapes)'`
- Trim, `normalize`, concatenation, stored deparse: `RUN='^TestReferenceStringCore'`
- Case mapping: `RUN='^TestReferenceStringCase'`
- Length and position: `RUN='^TestReferenceStringLength'`
- Padding, `repeat`, `reverse`: `RUN='^TestReferenceStringPad'`
- `replace`, `translate`, `overlay`, `starts_with`: `RUN='^TestReferenceStringReplace'`
- `left`, `right`, `substring`/`substr`: `RUN='^TestReferenceStringSlice'`
- `split_part`, `string_to_array`, `string_to_table`: `RUN='^TestReferenceStringSplit'`
- `parse_ident`: `RUN='^TestReferenceStringIdent'`
- `to_hex`/`to_oct`/`to_bin`, TEXT `md5`: `RUN='^TestReferenceStringNumericBase'`
- `ascii`, `chr`, `unistr`, `to_ascii`: `RUN='^TestReferenceStringEncoding'`
- `string_agg`: `RUN='^TestReferenceStringAgg'`
- XML consumers: `RUN='^TestReferenceXML'`
- BIT and VARBIT types: `RUN='^TestReferenceBitstring'`
- Bit-string operators: `RUN='^TestReferenceBitOperators'`
- Bit-string functions (`bit_count`, `length`, `position`, `substring`, `overlay`, ...): `RUN='^TestReferenceBitStringFunctions'`
- Bit-string `get_bit`, `set_bit`: `RUN='^TestReferenceBitAccess'`

Bit families keep their local `<family>_helpers_test.go`; access diagnostics use
`fixture.BinaryAccessBodyError`, `BinaryAccessAnalysisError` and
`BinaryAccessDiagnosticStages`, with `fixture.BitAccessScenario` for setup.
The shared binary access witnesses are owned by
[temporal_types](../temporal_types/README.md#testing).
