# Recognition-only source inputs

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
