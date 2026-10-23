# reference/strings

Dual-mode tests for character types (CHAR/VARCHAR/TEXT values, casts, modifiers
and assignment), string literals and Unicode escapes, and string functions:
trim, `normalize` and concatenation, case mapping, length and position, padding,
`repeat` and `reverse`, replacement and `overlay`, slicing (`left`, `right`,
`substring`), splitting, `parse_ident`, integer-base formatting and TEXT `md5`,
`ascii`/`chr`/`unistr`/`to_ascii`, and `string_agg`. Pattern matching, `format`
and the variadic `concat`/`quote_*` functions are in
[reference/patterns_encoding](../patterns_encoding/README.md#testing); BYTEA,
bit strings and binary hashing are in
[reference/binary_json_arrays](../binary_json_arrays/README.md#testing).

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
