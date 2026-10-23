# PostgreSQL 18.0 encoding provider

This package uses PostgreSQL **REL_18_0**, not the local development checkout,
Go's Unicode tables, iconv, or an external codec dependency. The immutable
runtime registry contains the release's 128 direct conversion pairs and their
actual associations to 84 routines, including grouped WIN/ISO-8859 handlers.
Identity verification covers the 42 release encoding IDs; SQL_ASCII and identity
shortcuts remain in the sqltype provider. SQL default selection belongs to the
engine's current conversion catalog, not this codec capability registry.
No indirect conversion path is added.

## Release inputs and licensing

`data/` preserves the original release directory layout and filenames. Inputs
were extracted from the official [PostgreSQL 18.0 release archive](https://ftp.postgresql.org/pub/source/v18.0/postgresql-18.0.tar.bz2).
The corresponding ordinary source references are:

- [Unicode directional maps](https://github.com/postgres/postgres/tree/REL_18_0/src/backend/utils/mb/Unicode): independently generated integer radix maps in **both directions**, including the 25 combined mappings in each JIS2004 direction.
- [pg_conversion.dat](https://github.com/postgres/postgres/blob/REL_18_0/src/include/catalog/pg_conversion.dat): direct pair-to-real-routine registry.
- [conv.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/utils/mb/conv.c): radix traversal, packed-word emission, combined-map precedence and MIC helpers.
- [wchar.c](https://github.com/postgres/postgres/blob/REL_18_0/src/common/wchar.c) and [pg_wchar.h](https://github.com/postgres/postgres/blob/REL_18_0/src/include/mb/pg_wchar.h): encoding IDs, verifier/mblen semantics, MIC prefixes and maximum expansion.
- [conversion_procs](https://github.com/postgres/postgres/tree/REL_18_0/src/backend/utils/mb/conversion_procs): dedicated Cyrillic, LATIN2/WIN1250, Japanese NEC/IBM/UDC, BIG5/CNS, MIC and JIS2004 algorithms; GB18030's 13 inclusive algorithmic ranges.

`LICENSE` retains the complete PostgreSQL grant and disclaimers, also preserved
as `data/COPYRIGHT`. Original source headers and attribution remain in every
vendored file. BIG5's additional lv/NARITA Tomio attribution is retained both in
`LICENSE` and the original `big5.c`; the runtime port names that source. The
integer `.map` inputs are PostgreSQL's already-generated release files, not a
new regeneration of their original upstream character databases. No extra
upstream source checkout or build tool is needed at runtime.

## Offline generation

From the repository root:

```sh
go run ./internal/sqltype/pgencoding18/generate
```

The generator uses only the Go standard library, reads vendored inputs without
network access, and writes `tables_generated.go` using `go/format`. Stable file
ordering and explicit numeric encoding ordering make output deterministic.
It rejects unknown integer syntax, duplicate arrays/pairs, wrong declared
lengths and widths, missing directional trees/combined maps, invalid value or
radix-index bounds, and a registry other than exactly 128 unique pairs. Each
mapping direction is emitted independently; reverse maps are never synthesized
by inversion. Runtime lookup does not parse data or allocate mutable maps/caches.
`HasPair` and `ConversionRoutine` expose immutable pair capabilities;
`ConversionRoutineArguments` is a generated allocation-free dispatch for fixed
and grouped argument checks. These capabilities are separate from SQL routine
ownership/metadata, default objects, diagnostics and portable dump definitions.

The hand-ported character algorithms use fixed packed words, not rune arrays or
an intermediate UTF8 conversion. Source validation, exact-size counting and
emission in the provider each poll the admitted context at bounded intervals.
A transform allocates only its final immutable output; identity and SQL_ASCII
paths reuse the source string. Numeric size checks use the release huge
allocation and result-allocation limits before output publication.

## Testing

Package units contain literal witnesses for all 128 pairs/all 42 identities,
verifier error widths and permissive identity cases, all 25 combined mappings
per JIS2004 encoding in both directions, radix zero/boundary entries, all 13
GB18030 range endpoints and adjacent GB gaps, Japanese replacement/IBM/UDC,
JIS2004 plane boundaries, BIG5/CNS private planes and direct table holes.
Generator units exercise invalid syntax, declarations and reachable bounds.
Public, independently specified PostgreSQL goldens live in `reference/`; they
are never generated from this implementation.

```sh
make check RUN='^TestBinaryEncoding'
```
