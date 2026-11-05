# XSLT POSIX collation data

This directory owns only the offline inputs and generator for
`../xslt_collation_table.go`. It does not change SQL collation or process locale.
The production comparator consumes immutable constant strings; it opens no files,
loads no locales, starts no processes, initializes no maps, and imports only the
standard library. Scratch belongs to one comparison, including storage needed to
reverse a long backward segment in linear time.

## Released mechanism

The pinned PostgreSQL image was independently observed to install glibc
`2.36-9+deb12u14` and libxslt `1.1.35-1+deb12u4`, with only `C`, `C.utf8`,
`en_US.utf8`, and `POSIX` in `locale -a`. The available linguistic locale is
therefore en_US. Locale selection follows the released ASCII tag parser, failed
territory lookup, bare-language lookup, and default-region lookup. For example,
`en-GB` reaches en_US, whereas malformed `en_US` and unavailable `de` do not.
An omitted language does not acquire the host locale.

[libxslt xsltlocale.c](https://raw.githubusercontent.com/GNOME/libxslt/v1.1.35/libxslt/xsltlocale.c)
selects the locale and transforms full UTF-8 strings using `strxfrm_l`;
[xsltutils.c](https://raw.githubusercontent.com/GNOME/libxslt/v1.1.35/libxslt/xsltutils.c)
compares transformed bytes. Although
[preproc.c](https://raw.githubusercontent.com/GNOME/libxslt/v1.1.35/libxslt/preproc.c)
records `lower_first`, that sorter does not read it. Consequently the comparator
does not invent case-order adjustments. Exact pinned-image sort qualification
is owned by the root's public fixtures; source inspection is not an executed
PostgreSQL parity claim.

The retained en_US definition copies iso14651_t1, which copies
iso14651_t1_common and tailors the Han range. This is the actual compiled locale,
not modern UCA, Go Unicode tables, Unicode normalization, or a one-rune mapping.
Its multibyte representation includes every released weight, backward SPECIAL
secondary runs, forward levels, positional fourth-level transformation,
contractions, expansions, ranges, and the compiled handling of unspecified
characters. Comments describing implicit Unicode weights are not substituted
for actual compiled entries. Undefined bytes/characters retain the released
lookup and weight representation, rather than an invented scalar tiebreak.

The native implementation independently interprets this data format. Research
references for its layout and stream semantics are glibc 2.36
[locale/categories.def](https://raw.githubusercontent.com/bminor/glibc/glibc-2.36/locale/categories.def),
[locale/weight.h](https://raw.githubusercontent.com/bminor/glibc/glibc-2.36/locale/weight.h),
and [string/strxfrm_l.c](https://raw.githubusercontent.com/bminor/glibc/glibc-2.36/string/strxfrm_l.c).
No GNU C Library implementation code, native library, or generated executable is
included in production. Comparisons return only an ascending sign; the sorter
owns direction and stable equal-key order.

## Exact acquisition

Permanent inputs were extracted without executing package code from Debian's
[glibc package pool](https://deb.debian.org/debian/pool/main/g/glibc/):

- `locales-all_2.36-9+deb12u14_amd64.deb`, SHA256
  `1e04749059effed50132c7e47b988c98269e40d636f2fefc257592d14ea7bf25`.
  Member `usr/lib/locale/en_US.utf8/LC_COLLATE`, 2,586,930 bytes, is retained
  losslessly as `en_US.LC_COLLATE.xz`; its uncompressed SHA256 is
  `f062bf818ec7c394208e4b12db855fbe3765ca1463b2e0cfe05e434f1c293981`.
- `locales_2.36-9+deb12u14_all.deb`, SHA256
  `00c6e3d9456411ac78b199b577f25231be80186796ebdff65089240154ebbcb0`.
  Members `usr/share/i18n/locales/en_US`, `iso14651_t1`, and
  `iso14651_t1_common` are retained as the first two plain files and the third
  losslessly XZ-compressed. These are the Debian package's actual source inputs,
  not an assumption that a generic upstream file is unchanged by Debian patches.
  Their uncompressed SHA256 values are pinned in `generate.py`.

The root independently extracted LC_COLLATE from the exact image's locale archive
using the released `locale/locarchive.h` layout (report `tests-12.md`). The
3,048,928-byte archive's en_US.utf8 category record has offset 457,488 and length
2,586,930; its SHA256 is exactly the retained full-category hash above. This
establishes actual binary identity, not inferred Debian patch equivalence.
The root retained the initially absent per-locale path and successful archive
extraction as evidence. This slice ran no candidate build, test, formatter,
locale invocation, or PostgreSQL oracle to generate these assets.

## Offline regeneration

Run from the worktree root:

```sh
python3 internal/xml/xsltcollationdata/generate.py
```

Python's standard library is sufficient; no network, installed locale, compiler,
C library call, host Unicode database, or third-party Python package is used.
The generator checks uncompressed hashes, the little-endian locale directory,
four-level rules, and multibyte section shapes, and extracts only the five
required multibyte data sections into constant strings. Unused wide-character,
regular-expression and category metadata are not shipped in production. Exact
input bytes are preserved; generation does not evaluate the candidate comparator
or record expected test outcomes. It is deterministic and requires no formatting
step.

## Notices

Each retained locale source carries this notice, preserved verbatim:

> This file is part of the GNU C Library and contains locale data.
> The Free Software Foundation does not claim any copyright interest
> in the locale data contained in this file. The foregoing does not
> affect the license of the GNU C Library as a whole. It does not
> exempt you from the conditions of the license if your use would
> otherwise be governed by that license.

The original sources, including all comments and notices, are retained in full.
The compiled lookup tables and their extracted representation are locale data,
not copied LGPL implementation code. `COPYING.debian` preserves the complete
package copyright/provenance file for audit (including the package's historical
version wording); it is not a claim that every license listed there applies to
these three locale data files. GNU C Library implementation sources consulted
for research remain covered by their own LGPL-2.1-or-later notices.
