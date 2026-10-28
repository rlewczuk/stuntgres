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
Run `go run ./internal/xml/generate` from the repository root. It requires only
this compact input, not Docker, network, iconv, ICU, a host XML parser or the
original temporary research helpers.

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
