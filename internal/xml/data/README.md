# XML provider source inputs

`recognition.tsv` is a compact transcription of independently captured pinned
names and preferred resolution, not conversion data or expectations learned from
this parser. The image is the project's pinned
`postgres:18.6-bookworm@sha256:afc7e2d441324c0388fa80c3d24f733b4194a4eb7f47dd8ee2b08eb1a24a647c`;
PostgreSQL `18.6-1.pgdg12+2`, UTF8/C, libxml2 `2.9.14+dfsg-1.3~deb12u6`,
glibc `2.36-9+deb12u14`, ICU `72.1-3+deb12u1`. The authoritative fixture policy is
[reference/fixture/README.md](../../../reference/fixture/README.md).

Rows are tab-separated, with no quoting or implicit row repair:

- `G NAME`: 1118 exact ASCII-uppercase glibc names observed accepted by DOCUMENT.
- `D NAME`: 190 exact-case shipped ICU `.cnv` basenames observed accepted.
- `A ALIAS KEY PREFERRED AVAILABILITY AMBIGUOUS`: all1548 compiled unique aliases
  from the actual shipped `cnvalias.icu`, including illegal literal EncName
  spellings whose normalized keys can nevertheless be matched. Preferred names
  include canonical options. Availability is `shipped-cnv`, `algorithmic` or
  `not-in-cnv-or-algorithmic`; ambiguity is `yes`/`no`. Unavailable preferences
  remain unavailable even when glibc independently recognizes a label.
- `K NAME`: independently admitted algorithmic representatives. The source key
  `iso2022` alone was rejected and is not an unconditional admitted key; its
  locale/version-bearing preferred converters remain distinct.
- `O LABEL ACCEPT|REJECT`: all2514 finite DOCUMENT character-input observations,
  2153 accepted and361 rejected. These remain independent from generated switches.

Runtime branch order and normalization are handwritten. Glibc membership is
exact after ASCII uppercase, not ICU-fuzzy. ICU raw names must be shorter than60
bytes before fuzzy normalization; direct data fallback preserves original case
and punctuation. No runtime source parsing or mutable map/cache is introduced.

## Source and reproduction

The temporary research captures were146 (actual compiled inventory),156 (finite
SQL observations, including failure Detail as bytea-hex),159 (compact sets).
They are provenance, not runtime or generator dependencies. The vendored TSV is
the complete compact input; no31MB bundle is required for ordinary generation.

Inventory extraction used `docker create` on the pinned image and `docker cp`
of `/usr/lib/x86_64-linux-gnu/libicudata.so.72.1`, then a stdlib byte decoder of
CmnD/CvAl data structures (no native execution). The shipped library had31262256
bytes, CmnD header offset8192 and3920 entries. ICU's `ucmndata.h:40-74` defines the
CmnD header/table; `ucnv_io.cpp:80-115,178-208,238-311` defines compiled aliases and
16-bit-unit string offsets; `ucnv_io.h:24-26` defines preferred index/ambiguity.
The actual alias inventory contains365 canonical records and1548 unique aliases.
The temporary library and containers were removed after capture.

Glibc names came from this exact image's `iconv --list`, removing terminal triplet
slashes and retaining legal XML EncName candidates. This is not an assertion that
an independent per-name bidirectional iconv-open sweep was performed. Actual
DOCUMENT SQL membership, rather than iconv-list plausibility alone, supplies the
finite observation: in the pinned UTF8/C server, for each candidate `label`,
`SET xmloption=DOCUMENT` followed by
`pg_input_error_info('<?xml version="1.0" encoding="'||label||'"?><a>é</a>','xml')`.
The body is real UTF8é regardless of the label. All four NULL error fields mean
acceptance; rejected labels have2200M and their actual English diagnostics. These
are text recognition observations, not new binary conversion capabilities.

Sources (release/package sources, never the local20devel tree):

- [Debian libxml2 encoding.c](https://sources.debian.org/data/main/libx/libxml2/2.9.14%2Bdfsg-1.3~deb12u6/encoding.c):1160-1232,1399-1450,1657-1790 (registered, native handler and canonical lookup order).
- [Debian parser.c](https://sources.debian.org/data/main/libx/libxml2/2.9.14%2Bdfsg-1.3~deb12u6/parser.c):10350-10445 and [parserInternals.c](https://sources.debian.org/data/main/libx/libxml2/2.9.14%2Bdfsg-1.3~deb12u6/parserInternals.c):1220-1265 (static-memory character-input recognition without decoding).
- [ICU72.1 ucnv_io.cpp](https://github.com/unicode-org/icu/blob/release-72-1/icu4c/source/common/ucnv_io.cpp):383-413,550-559,595-613 (precise normalization, raw length, default preferences).
- [ICU72.1 ucnv_bld.cpp](https://github.com/unicode-org/icu/blob/release-72-1/icu4c/source/common/ucnv_bld.cpp):127-195,337-412,630-653,773-809 (algorithmic names, exact data fallback and raw parsing).
- [ICU CmnD format](https://github.com/unicode-org/icu/blob/release-72-1/icu4c/source/common/ucmndata.h) and [alias format](https://github.com/unicode-org/icu/blob/release-72-1/icu4c/source/common/ucnv_io.h).
- [glibc gconv_charset.h](https://sources.debian.org/data/main/g/glibc/2.36-9%2Bdeb12u14/iconv/gconv_charset.h):55-83, [gconv_builtin.h](https://sources.debian.org/data/main/g/glibc/2.36-9%2Bdeb12u14/iconv/gconv_builtin.h), [modules](https://sources.debian.org/data/main/g/glibc/2.36-9%2Bdeb12u14/iconvdata/gconv-modules), [extra modules](https://sources.debian.org/data/main/g/glibc/2.36-9%2Bdeb12u14/iconvdata/gconv-modules-extra.conf).
- [PostgreSQL REL_18_0 xml.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/utils/adt/xml.c):248-434,1433-1640,1748-1925,2088-2307; [release XML regression](https://github.com/postgres/postgres/blob/REL_18_0/src/test/regress/expected/xml.out):252-341.

The compact names are facts extracted from installed inventories, not a copy of
glibc conversion implementations or any mapping tables. ICU-derived metadata and
normalization retain the Unicode/IBM notices in `LICENSE-ICU`; handwritten libxml
production/context ports retain the notice in `../LICENSE-libxml2`. PostgreSQL
identity/codec attribution and its full license remain in `pgencoding18`.

The offline stdlib generator rejects invalid/duplicate/conflicting/unhandled
rows and incomplete inventories, sorts keys and writes deterministic switches
using standard-library `go/format`, following the existing codec generator.
Run `go run ./internal/xml/generate` from the repository root. It requires the
vendored recognition and mapping inputs only, not Docker, network, iconv, ICU,
a host XML parser or the original temporary research helpers.

## Raw-memory JIS0208 mapping

`JIS0208.TXT` preserves the complete original bytes and header from
[Unicode's JIS X 0208 (1990) mapping](https://www.unicode.org/Public/MAPPINGS/OBSOLETE/EASTASIA/JIS/JIS0208.TXT),
table version 2.0, Unicode version 1.1, header dated 2015-12-02 and copyright
2015 Unicode, Inc. The source's possible-mappings caveat is not treated as
product-equivalence authority. The accompanying original
[Unicode License V3](https://www.unicode.org/license.txt) notice is retained in
`LICENSE-Unicode`; both files were retrieved 2026-11-02. The generated mapping
retains that attribution and license reference.

Independent exhaustive qualification against the exact pinned PostgreSQL18.6
image/libxml2 raw `xmlReadMemory(..., XML_PARSE_NOENT)` entry compared every one
of the 94×94 cells: 8836 parsed, 6879 assigned and accepted, 1957 holes rejected.
Exactly one source cell differs: JIS **2140**, original **U+005C**, decodes as
**U+FF3C** in that reader. Generation applies this explicit correction only after
asserting its original value; the licensed input itself remains unchanged.
All other assignments and holes agree. This is independent reader evidence,
not generated expectations learned from the Go decoder.

The existing offline stdlib generator validates row syntax, scalar/cell bounds,
duplicates, the complete assigned count and the correction's original value.
It emits `../jis0208_generated.go`, an immutable big-endian BMP string indexed
by `(lead-0x21)*94+(trail-0x21)`, two bytes per cell, zero for holes. No runtime
parsing, mutable map, cache, external library or filesystem/network I/O exists.
Generator units independently parse the original source, compare every emitted
cell and hole, and reject malformed, missing, duplicate and drifting inputs.
The package raw-input family additionally parses genuine documents through all
8836 cells and checks independently expected characters or complete failures.

[glibc2.36's ISO-2022-JP input loop](https://raw.githubusercontent.com/bminor/glibc/glibc-2.36/iconvdata/iso-2022-jp.c)
is behavior research for designations, literal unknown escapes and incomplete
buffering, not a copied mapping source. No LGPL glibc table or derived data is
relicensed here. The Unicode input plus independent single-cell observation
supplies the entire new forward mapping. PostgreSQL EUC_JP is deliberately not
used as a substitute: its mappings and extensions differ. Existing direct-codec
families continue to use the independently licensed PostgreSQL release kernels.
Raw declaration selection follows the matching libxml DOCUMENT grammar, not
the CONTENT/Binary preflight or a header regex. Text/Binary recognition and
output contracts remain separate.

Raw diagnostic qualification uses that same pinned image and exact-length input.
Physical EOF one-/two-byte conversion faults exposed native preview reads beyond
the terminating NUL: e.g. actual `80` appeared as `80 00 3C 72`, and `80 41` as
`80 41 00 72`; those later bytes are stale memory, not transport. The provider's
deterministic safe preview retains actual remaining bytes (at most four) and at
most one NUL terminator, never residue or zero-padding to four. Three actual
bytes plus the terminator reproduce the pinned four-byte preview exactly.
The reader also returned nonnil document pointers after completed roots while
reporting two fatal conversion events and an I/O error. The package's structured
failure-atomic contract rejects genuine conversion faults regardless of that
pointer, and adds the parser's premature-end event only for an unclosed root.
This is distinct from fault-free incomplete buffering, which remains admissible
after a completed root. Permanent closed/unclosed EOF boundary cases preserve
the distinction without retaining or manufacturing host-memory bytes.

Raw JOHAB's byte framing follows the independently inspected
[glibc2.36 johab.c input loop](https://raw.githubusercontent.com/bminor/glibc/glibc-2.36/iconvdata/johab.c):
ASCII is one byte; Hangul leads84–D3 use trails41–7E/81–FE; symbols use
leadsD9–DE/E0–F9 and trails31–7E/91–FE with the input loop's D9/DA/DE
subrange restrictions. This is framing research, not copied conversion data.
Assigned character values come exclusively from the existing immutable
PostgreSQL `johab_to_utf8` map through the mapping-only `LookupUTF8` seam.
The observed raw `8861` witness is U+AC00. The independently source-derived
`8F41` witness is U+B028 (Hangul bitfields and the release map's EB80A8 leaf).
Both are two-byte raw characters; PostgreSQL's existing JOHAB verifier and
EUC-style `Length` remain unchanged, as do Binary/SQL conversions. ASCII follows
the existing map policy rather than adding a host-specific won-sign conversion.
Framed-but-unassigned cells and illegal/truncated frames remain failures; no
second JOHAB map or external runtime decoder is introduced.

## Character serialization oracle

`serialize-oracle.json` contains 322 independently captured PostgreSQL records
from the pinned image/runtime/UTF8/C and libxml2 package identified above. No
expectation was produced by this provider. Groups retain the finite initial,
further, qualification, edge, composed, XHTML, document-entry, notation-order,
branches, DTD-edges and review1 observations; IDs identify inputs within their group.
The output strings retain literal CR/LF, empty fields and declaration spelling.

Reproduction uses `psql -X -U postgres -At -v ON_ERROR_STOP=1` inside that
image, initialized with `POSTGRES_INITDB_ARGS=--locale=C --encoding=UTF8` and
`LANG=C`. Verify `version()`, server encoding, `lc_collate`, `lc_ctype` and
`dpkg-query -W libxml2` before capture. A temporary PL/pgSQL function accepts
input text, requested mode and indent, binds input with dynamic SQL
`SELECT XMLSERIALIZE(%s XMLPARSE(CONTENT $1) AS text %s)`, and returns JSON with
the output or caught RETURNED_SQLSTATE/MESSAGE_TEXT/PG_EXCEPTION_DETAIL/
PG_EXCEPTION_HINT. Finite VALUES cross-products cover both requested modes and
indent flags where present. Document-entry changes initial XMLPARSE to DOCUMENT.
Initial input rejection is distinct from serialization qualification failure.
Binary composition units inherit CORE’s actual binary-receive decoding evidence;
these new SQL serialization captures themselves use Unicode character input.

`TestXMLSerializeDTDEntityValueIdentity` additionally holds two literal full-output
captures from this same runtime and recipe, one per requested mode. Its legal
nested declaration uses p=`ok` and r=`<!ENTITY &#37; q "&#38;#65;&#37;p;">`,
then expands `%r;` in the internal subset. q’s processed replacement is `Aok`,
while its saved original EntityValue is `&#65;%p;`. The test asserts the entire
DTD and document, including CONTENT’s final LF, not a substring projection.

The 36 `review1` records independently capture complete INDENT outputs in both
requested modes for CR followed by 298/299/300/301/400 tabs (unspecified and
explicit-default space state), namespace-bound element/local DTD-model lookup,
and adjacent/nonadjacent duplicate ENUMERATION/NOTATION members. They use the
same runtime and capture recipe above. The callback boundary is observable:
the first complex callback contains 300 normalized bytes, and its raw lookahead
determines blank admission before text coalescing. QName witnesses distinguish
local `a` declarations from lexical `p:a`; duplicate-member witnesses preserve
first-admitted distinct order without adding validity rejection.
Source-derived width extensions retain these first-admission rules for 20,000
distinct fixed-width members in ENUMERATION and NOTATION, with repeated members
and separate following attributes/declarations. They assert full canonical output,
not a measured runtime bound or a fresh SQL capture. Deterministic cancellation
witnesses enter actual wide-list admission and long-token indexed lookup work.

Twenty identical DOCUMENT notation saves produced all six permutations of three
records. Only the contiguous notation section is compared unordered: exact
records, multiplicities and placement before ordinary declarations remain fixed.
The local serializer deliberately uses deterministic first-binding order.

Release authorities: [PostgreSQL REL_18_0 xml.c](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/utils/adt/xml.c)
`xmltotext_with_options`, `xml_parse`, `parse_xml_decl`; [release regression](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/test/regress/expected/xml.out)
478–684. Matching [libxml2 xmlsave.c](https://raw.githubusercontent.com/GNOME/libxml2/v2.9.14/xmlsave.c)
owns escaping, ordinary/DTD/XHTML layout and identifier detection;
[parser.c](https://raw.githubusercontent.com/GNOME/libxml2/v2.9.14/parser.c)
owns parse-time blank callbacks and model occurrence normalization;
[valid.c](https://raw.githubusercontent.com/GNOME/libxml2/v2.9.14/valid.c),
[entities.c](https://raw.githubusercontent.com/GNOME/libxml2/v2.9.14/entities.c)
and [HTMLtree.c](https://raw.githubusercontent.com/GNOME/libxml2/v2.9.14/HTMLtree.c)
own canonical declarations and XHTML boolean membership. Additional labeled units
extend these source rules without claiming fresh SQL observations. Attribution
remains in `../LICENSE-libxml2`.
