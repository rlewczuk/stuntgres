# XML provider source inputs

Vendored recognition metadata, licensed character mappings and independent oracle
corpora. Runtime uses generated immutable data, never these files, host codecs,
filesystem/network loading or mutable mapping caches.

## Runtime provenance

Captures use
`postgres:18.6-bookworm@sha256:afc7e2d441324c0388fa80c3d24f733b4194a4eb7f47dd8ee2b08eb1a24a647c`,
PostgreSQL `18.6-1.pgdg12+2`, UTF8/C, libxml2
`2.9.14+dfsg-1.3~deb12u6`, glibc `2.36-9+deb12u14` and ICU `72.1-3+deb12u1`.
The authoritative [fixture policy](../../../reference/fixture/README.md)
distinguishes pinned observations from source-derived expectations.

## Recognition metadata

`recognition.tsv` records independently observed names and preferred resolution,
not conversion mappings. Rows are tab-separated, without quoting or implicit
repair:

- `G NAME`: exact ASCII-uppercase glibc names accepted by DOCUMENT.
- `D NAME`: exact-case shipped ICU `.cnv` basenames accepted by DOCUMENT.
- `A ALIAS KEY PREFERRED AVAILABILITY AMBIGUOUS`: compiled aliases from
  `cnvalias.icu`, including normalized keys, canonical options and ambiguity.
  Availability is `shipped-cnv`, `algorithmic` or `not-in-cnv-or-algorithmic`;
  ambiguity is `yes` or `no`.
- `K NAME`: independently admitted algorithmic representatives.
- `O LABEL ACCEPT|REJECT`: finite DOCUMENT character-input observations.

Runtime branch order and normalization are handwritten. Glibc membership is exact
ASCII-uppercase; ICU matching has its own raw-length/fuzzy-normalization rules.
Recognition does not authorize a converter. Unavailable ICU preferences stay
unavailable even when glibc recognizes the label independently.

Reproduce inventories by extracting the pinned image's
`/usr/lib/x86_64-linux-gnu/libicudata.so.72.1` with `docker create`/`docker cp`
and decoding CmnD/CvAl metadata, without executing the library. Glibc candidates
come from that image's `iconv --list`, stripping terminal triplet slashes and
retaining legal XML EncName candidates. Actual recognition is independently
observed with `SET xmloption=DOCUMENT` and
`pg_input_error_info('<?xml version="1.0" encoding="'||label||'"?><a>é</a>','xml')`.
The body is UTF8 regardless of label: this measures text recognition, not byte
conversion or a bidirectional iconv-open sweep.

Source authorities:

- [libxml2 encoding.c](https://sources.debian.org/data/main/libx/libxml2/2.9.14%2Bdfsg-1.3~deb12u6/encoding.c),
  [parser.c](https://sources.debian.org/data/main/libx/libxml2/2.9.14%2Bdfsg-1.3~deb12u6/parser.c)
  and [parserInternals.c](https://sources.debian.org/data/main/libx/libxml2/2.9.14%2Bdfsg-1.3~deb12u6/parserInternals.c).
- [ICU alias handling](https://github.com/unicode-org/icu/blob/release-72-1/icu4c/source/common/ucnv_io.cpp),
  [handler resolution](https://github.com/unicode-org/icu/blob/release-72-1/icu4c/source/common/ucnv_bld.cpp),
  [CmnD format](https://github.com/unicode-org/icu/blob/release-72-1/icu4c/source/common/ucmndata.h)
  and [alias format](https://github.com/unicode-org/icu/blob/release-72-1/icu4c/source/common/ucnv_io.h).
- [PostgreSQL REL_18_0 xml.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/utils/adt/xml.c).

ICU-derived metadata retains `LICENSE-ICU`; handwritten libxml production ports
retain `../LICENSE-libxml2`. Recognition facts are not glibc conversion code or
mapping tables. PostgreSQL codec attribution remains in `pgencoding18`.

## Character mapping sources

`JIS0208.TXT` and `JIS0212.TXT` retain original bytes and headers from Unicode's
[JIS0208](https://www.unicode.org/Public/MAPPINGS/OBSOLETE/EASTASIA/JIS/JIS0208.TXT)
and [JIS0212](https://www.unicode.org/Public/MAPPINGS/OBSOLETE/EASTASIA/JIS/JIS0212.TXT)
mappings, table version 2.0, Unicode 1.1, copyright 2015 Unicode, Inc. Their
complete Unicode License V3 is in `LICENSE-Unicode`. No glibc LGPL mapping table
is copied or relicensed.

Each source spans 94×94 cells. Independent exact-length pinned `xmlReadMemory`
qualification supplies one correction per repertoire:

| Source | Cell | Original | Reader value |
| --- | --- | --- | --- |
| JIS0208 | 2140 | U+005C | U+FF3C |
| JIS0212 | 2237 | U+007E | U+FF5E |

Generation asserts the original value before applying each correction; licensed
inputs remain unchanged. Generated tables contain two big-endian BMP bytes per
cell, zero for holes. Tests independently interpret original inputs rather than
learning expected values from generated tables or the candidate decoder.

KR/CN source corpora use original PostgreSQL release `euc_kr_to_utf8.map`,
`euc_cn_to_utf8.map` and `euc_tw_to_utf8.map` under
`../../sqltype/pgencoding18/data/src/backend/utils/mb/Unicode/`, retaining that
provider's `LICENSE` and `data/COPYRIGHT`. Raw framing is separate from PostgreSQL
byte-string verification. JOHAB values reuse the same licensed PostgreSQL
mapping-only seam rather than a duplicate host mapping.

`Unihan-16.0.0-CNS1-extract.txt` is a minimal original-header/record extract from
[Unicode 16.0.0 Unihan.zip](https://www.unicode.org/Public/16.0.0/ucd/Unihan.zip),
`Unihan_IRGSources.txt` lines 38437, 44636 and 47223, copyright 2024 Unicode, Inc.
`LICENSE-Unicode` supplies its complete license. It contributes CNS1
`2728→U+4EA0`, `272F→U+51AB`, `2734→U+52F9` only after asserting their absence
from the PostgreSQL map. The original maps and global conversion tables remain
unchanged; no restricted Unihan 3.2 material is redistributed.

Stateful framing authorities include [RFC1557](https://www.rfc-editor.org/rfc/rfc1557),
[RFC1922](https://www.rfc-editor.org/rfc/rfc1922),
[RFC2152](https://www.rfc-editor.org/rfc/rfc2152) and matching glibc 2.36 input
loops. These are behavioral research, not redistributed converter tables.
Further corrections require independently licensed facts and whole-reader
qualification, never candidate-derived goldens.

## Oracle corpora

`dtd-oracle.json` and `serialize-oracle.json` retain independent pinned-runtime
observations. Corpus IDs/groups identify captures; source-derived extensions are
labeled separately. No expectation is generated by the Go implementation.

For serialization capture, use `psql -X -U postgres -At -v ON_ERROR_STOP=1` in
the pinned image initialized with
`POSTGRES_INITDB_ARGS=--locale=C --encoding=UTF8` and `LANG=C`. Verify `version()`,
server encoding, `lc_collate`, `lc_ctype` and `dpkg-query -W libxml2` first. A
PL/pgSQL wrapper binds input to
`SELECT XMLSERIALIZE(%s XMLPARSE(CONTENT $1) AS text %s)` and returns output or
caught SQLSTATE/message/detail/hint as JSON. DOCUMENT-entry cases use DOCUMENT
for the initial parse. Preserve literal CR/LF and distinguish initial rejection
from serialization qualification failure. Binary-input provenance is separate
from these Unicode character-input captures.

Notation order is independently nondeterministic: only that contiguous section
is compared unordered; bytes, multiplicities and surrounding placement remain
exact. The local saver uses deterministic first-binding order. Serialization
rules come from PostgreSQL `xmltotext_with_options`/`xml_parse`/`parse_xml_decl`
and matching libxml2 parser/save/entity/validity sources, attributed in
`../LICENSE-libxml2`.

## Offline generation

Run `go run ./internal/xml/generate` from the repository root. The stdlib-only
generator validates vendored metadata/mapping syntax, inventories, bounds,
uniqueness and correction preconditions, then emits deterministic immutable
lookups using `go/format`. It requires no Docker, network, iconv, ICU, native XML
parser or temporary research bundle. Independent generator tests read licensed
source inputs and reject corrupt or incomplete records.
