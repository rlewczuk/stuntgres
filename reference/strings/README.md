# reference/strings

Dual-mode tests for character types (CHAR/VARCHAR/TEXT values, casts, modifiers
and assignment), string literals and Unicode escapes, and string functions:
trim, `normalize` and concatenation, case mapping, length and position, padding,
`repeat` and `reverse`, replacement and `overlay`, slicing (`left`, `right`,
`substring`), splitting, `parse_ident`, integer-base formatting and TEXT `md5`,
`ascii`/`chr`/`unistr`/`to_ascii`, `string_agg`, XML scalar input, parsing,
serialization, validation/document/XPath existence predicates, session/prepared
lifetimes, wire/COPY and controls. Pattern matching, `format`
and the variadic `concat`/`quote_*` functions are in
[reference/patterns_encoding](../patterns_encoding/README.md#testing); BYTEA and
binary hashing are in [temporal_types](../temporal_types/README.md#testing).
BIT/VARBIT types, operators, functions and bit access are owned here.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` is plain
`fixture.Main(m)`.

- The per-case deparse matrices (`TestReferenceStringCoreReview2Deparse*`, one
  case per constraint) give every case its own serial `NewScenario`. The case
  keeps that scenario's 30-second context from table creation through native
  Dump/Reset/Restore and cleanup, and cleanup finishes before the next case starts.
- A typed nil `[]byte` bound to a parameter inferred as TEXT is empty input
  natively but NULL through lib/pq (for example, `md5` of it). Encode both
  expectations; never select signatures by Go kind.

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
- XML scalar acceptance: `RUN='^TestReferenceXML'`
- XML predicate cast-bearing held/view/deparse/control lifetimes and deterministic
  late INSERT/UPDATE failures: `RUN='^(TestReferenceXMLPredicateLifecycle|TestReferenceXMLPredicateDiagnostics)$'`
  in both native and real-reference modes.
- BIT and VARBIT types: `RUN='^TestReferenceBitstring'`
- Bit-string operators: `RUN='^TestReferenceBitOperators'`
- Bit-string functions (`bit_count`, `length`, `position`, `substring`, `overlay`, ...): `RUN='^TestReferenceBitStringFunctions'`
- Bit-string `get_bit`, `set_bit`: `RUN='^TestReferenceBitAccess'`

Bit families keep their local `<family>_helpers_test.go`; access diagnostics use
`fixture.BinaryAccessBodyError`, `BinaryAccessAnalysisError` and
`BinaryAccessDiagnosticStages`, with `fixture.BitAccessScenario` for setup.
The shared binary access witnesses are owned by
[temporal_types](../temporal_types/README.md#testing).
