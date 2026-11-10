# Ispell reference data

The seven sample files in this directory are unmodified UTF-8 bytes from
[PostgreSQL REL_18_6, src/backend/tsearch/dicts](https://github.com/postgres/postgres/tree/REL_18_6/src/backend/tsearch/dicts):

- `ispell_sample.affix` and `ispell_sample.dict` (legacy format)
- `hunspell_sample.affix` (default flags, paired upstream with `ispell_sample.dict`)
- `hunspell_sample_long.affix` and `hunspell_sample_long.dict` (long flags/aliases)
- `hunspell_sample_num.affix` and `hunspell_sample_num.dict` (numeric flags)

Their PostgreSQL license is reproduced in `COPYRIGHT`. The default format has
no separate released `hunspell_sample.dict`; the manifest deliberately reuses
`ispell_sample.dict` under the default scenario's unique installed basename.

Ordered morphology and compound expectations come independently from
[REL_18_6 tsdicts.out](https://github.com/postgres/postgres/blob/REL_18_6/src/test/regress/expected/tsdicts.out).
The public-reference manifest in `ispell_fixture_test.go` supplies the same
contents to native Driver registration and the existing PostgreSQL fixture.
Explicit small malformed, empty, stopword and positional-compound files live in
that manifest, with no production sample matcher or host filesystem loader.
