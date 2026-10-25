# PostgreSQL 18.6 regressions and release differences

The reference oracle is PostgreSQL 18.6, pinned in
[reference/fixture/README.md](../reference/fixture/README.md#postgresql-oracle-policy-and-provenance).
This file records upstream defects that the oracle exposes, with enough detail
to reproduce them outside this repository. Stuntgres does not imitate an
upstream defect: the affected reference test records the upstream result as an
explicit release difference on the PostgreSQL side and keeps the intended
behavior on the native side.

Moving the oracle from 18.0 to 18.6 exposed one 18.6 regression (below). The
other differences were 18.0 defects that 18.6 fixed; stuntgres now follows
18.6 for those (see [the last section](#180-defects-fixed-in-186)).

## Reproduction environment

The earlier regression outputs were observed on 2026-10-18 with these images,
initialized like the reference fixture (UTF8, C locale, English messages):

| Release | Image |
|---|---|
| 18.6 | `postgres:18.6-bookworm@sha256:afc7e2d441324c0388fa80c3d24f733b4194a4eb7f47dd8ee2b08eb1a24a647c` (`PostgreSQL 18.6 (Debian 18.6-1.pgdg12+2)`) |
| 18.0 | `postgres:18.0-bookworm@sha256:3f55f8895c4ed50603e2fbdfc72fffeeaba3173321fee5cb825bbbeb30d9d854` (`PostgreSQL 18.0 (Debian 18.0-1.pgdg12+3)`) |

```sh
IMAGE='postgres:18.6-bookworm@sha256:afc7e2d441324c0388fa80c3d24f733b4194a4eb7f47dd8ee2b08eb1a24a647c'
docker run -d --rm --name pg-regress -e POSTGRES_PASSWORD=x \
  -e POSTGRES_INITDB_ARGS='--locale=C --encoding=UTF8' "$IMAGE"
until docker exec pg-regress pg_isready -U postgres; do sleep 1; done
docker exec -i pg-regress psql -U postgres -X < repro.sql
docker rm -f pg-regress
```

## ISSN output discards invalid markers and nonzero issue digits

PostgreSQL 18.6's `isn` extension emits short ISSN output even when that output
cannot preserve the complete value. The following outputs were observed with
the pinned 18.6 image above on 2026-10-25, UTF8 encoding, C locale and English
messages. No claim about another release is implied.

```sql
CREATE EXTENSION isn;
SELECT '12345670!'::issn AS marked,
       '9771234567898'::issn AS issue89,
       '9771234567898!'::issn AS marked_issue89;
```

All three columns return `1234-5679`: the invalid-input `!` marker is absent,
and issue digits `89` are not represented. The
[ISN manual](https://www.postgresql.org/docs/18/isn.html) describes retained
invalid markers and short notation when a value can fit. In
[18.6 `ean2ISSN`](https://github.com/postgres/postgres/blob/REL_18_6/contrib/isn/isn.c),
short output unconditionally terminates after the recomputed ISSN check digit,
discarding the remaining long-output characters.

Native ISSN output keeps `1234-5679!` for marked zero-issue values and uses
`977-1234-567-89-8` or `977-1234-567-89-8!` when issue digits cannot fit in
short notation. SQL serialization always retains the complete thirteen-digit
number and optional marker. The shared ISSN reference scenarios explicitly
expect the three truncated PostgreSQL displays while keeping independent
lossless native expectations; actual native dump replay uses those PostgreSQL
display expectations too.

## Regression: `to_date` and `to_timestamp` fail internally on TM fields under an indeterminate collation

**Affected functions:** `to_date(text, text)` and `to_timestamp(text, text)`.
Both parse through the same code (`do_to_timestamp()` in `formatting.c`), so
they fail identically.

**Affected releases:** 18.6, where it first appeared. It is still present in
the PostgreSQL development tree at commit `38a6b8c7adb` (checked 2026-10-18).
18.0 behaves correctly.

**Introduced by:** commit `011384ba45f` "Fix calculating length of match to
localized month/weekday names" (Álvaro Herrera, back-patched into 18.6).

### Trigger

All three conditions together:

1. The template parses a `TM`-prefixed name field: `TMMon`, `TMMonth`,
   `TMDay` or `TMDy` (the localized month and day names).
2. The input text at that field does not match a localized name byte for byte,
   so the case-insensitive fallback runs: different letter case (`dEc`,
   `DEC`, `deCember`, `tHursday`, `tHu`) or an unknown word (`Bogus`).
3. The call's input collation is indeterminate: the two arguments carry
   conflicting implicit collations and no `COLLATE` clause resolves them.

### Reproduction (`repro.sql`)

```sql
\set VERBOSITY verbose
\set ON_ERROR_STOP 0
CREATE TABLE t(input text COLLATE "C", mask text COLLATE "POSIX");
INSERT INTO t VALUES
  ('05 Dec 2000',         'DD TMMon YYYY'),
  ('05 dEc 2000',         'DD TMMon YYYY'),
  ('05 DEC 2000',         'DD TMMon YYYY'),
  ('05 Bogus 2000',       'DD TMMon YYYY'),
  ('05 deCember 2000',    'DD TMMonth YYYY'),
  ('2006-10-19 tHursday', 'YYYY-MM-DD TMDay'),
  ('2006-10-19 tHu',      'YYYY-MM-DD TMDy');

-- 1. Exact spelling: no case folding, succeeds on both releases.
SELECT to_date(input, mask)      FROM t WHERE input = '05 Dec 2000';
SELECT to_timestamp(input, mask) FROM t WHERE input = '05 Dec 2000';

-- 2. Case-insensitive fallback under an indeterminate collation: the bug.
SELECT to_date(input, mask)      FROM t WHERE input = '05 dEc 2000';
SELECT to_timestamp(input, mask) FROM t WHERE input = '05 dEc 2000';
SELECT to_date(input, mask)      FROM t WHERE input = '05 DEC 2000';
SELECT to_timestamp(input, mask) FROM t WHERE input = '05 Bogus 2000';
SELECT to_date(input, mask)      FROM t WHERE input = '05 deCember 2000';
SELECT to_timestamp(input, mask) FROM t WHERE input = '2006-10-19 tHursday';
SELECT to_date(input, mask)      FROM t WHERE input = '2006-10-19 tHu';

-- 3. Workarounds: an explicit collation on either argument, or literals
--    (which have no conflicting collations).
SELECT to_date(input COLLATE "C", mask)      FROM t WHERE input = '05 dEc 2000';
SELECT to_timestamp(input, mask COLLATE "C") FROM t WHERE input = '05 dEc 2000';
SELECT to_date('05 dEc 2000', 'DD TMMon YYYY');
SELECT to_timestamp('05 dEc 2000', 'DD TMMon YYYY');
```

### Observed results

Group 1 succeeds on both releases: `to_date` returns `2000-12-05`,
`to_timestamp` returns `2000-12-05 00:00:00+00`. Group 3 succeeds on both
releases with the same values.

Every statement in group 2, for both functions, reports on **18.0** (intended):

```text
ERROR:  42P22: could not determine which collation to use for upper() function
HINT:  Use the COLLATE clause to set the collation explicitly.
LOCATION:  str_toupper, formatting.c:1715
```

and on **18.6**:

```text
ERROR:  XX000: cache lookup failed for collation 0
LOCATION:  pg_newlocale_from_collation, pg_locale.c:1218
```

The 18.6 error is an internal error (`XX000`, from `elog`): wrong SQLSTATE, no
hint, and a message that exposes an implementation detail. Clients that handle
`42P22` (indeterminate collation) no longer recognize the failure.

### Cause

`seq_search_localized()` in `src/backend/utils/adt/formatting.c` matches a TM
name. It first tries an exact, case-sensitive comparison, which is why group 1
works. When that fails it case-folds the input. In 18.0 it folded only through
`str_toupper()` and `str_tolower()`, which begin by rejecting an invalid
collation with the intended error:

```c
if (!OidIsValid(collid))
    ereport(ERROR,
            (errcode(ERRCODE_INDETERMINATE_COLLATION),
             errmsg("could not determine which collation to use for %s function", "upper()"),
             errhint("Use the COLLATE clause to set the collation explicitly.")));
```

Commit `011384ba45f` changed the fallback to fold the candidate names with
`pg_strupper()`/`pg_strlower()` on a `pg_locale_t`, and builds that locale
before the first `str_toupper()` call:

```c
mylocale = pg_newlocale_from_collation(collid);   /* runs first */
...
upper_name = str_toupper(name, name_len, collid); /* would have raised 42P22 */
```

With an indeterminate collation `collid` is `InvalidOid` (0), and
`pg_newlocale_from_collation()` in `src/backend/utils/adt/pg_locale.c` fails
before any collation check:

```c
if (!OidIsValid(collid))
    elog(ERROR, "cache lookup failed for collation %u", collid);
```

### Suggested upstream fix

In `seq_search_localized()`, check `OidIsValid(collid)` before calling
`pg_newlocale_from_collation()` and raise the same `42P22` error and hint as
`str_toupper()`, or move the `pg_newlocale_from_collation()` call after the
`str_toupper()` call that already performs the check. A regression test is the
group 2 query above, expecting `42P22`.

### Stuntgres handling

Native keeps the intended `42P22` diagnostic with its hint for both functions.
`TestReferenceDatetimeFormattingReviewCollationDemand`
(`reference/datetime_formatting/datetime_formatting_review_test.go`) runs every
case above through `to_date` and `to_timestamp`, direct and prepared. It
expects `XX000 cache lookup failed for collation 0` on the PostgreSQL side and
`42P22` on the native side. When a fixed PostgreSQL release is pinned, remove
that PostgreSQL-side branch so both backends assert `42P22` again.

## 18.0 defects fixed in 18.6

These are not 18.6 bugs. They are listed because tests written against 18.0
encoded the old behavior; stuntgres and its reference tests now follow 18.6.
Single-session reproductions (run `CREATE EXTENSION fuzzystrmatch;` first for
the `levenshtein` line):

| Area | Upstream commit | Query | 18.0 | 18.6 |
|---|---|---|---|---|
| `uuidv7` shift guards | `c31b0fca059` | `SELECT uuidv7(interval 'infinity');` | returns a UUID | `22008 interval out of range for UUID version 7` |
| `json_strip_nulls` volatility | `2f6ee7b389c` | `SELECT provolatile FROM pg_proc WHERE proname='json_strip_nulls';` | `s` | `i` (fresh cluster) |
| EXTRACT field deparse | `0ddd9098a31` | `CREATE VIEW vx AS SELECT EXTRACT('YeAr' FROM now()) AS y; SELECT pg_get_viewdef('vx');` | `EXTRACT(YeAr FROM now())` | `EXTRACT("YeAr" FROM now())` |
| Final sigma (`pg_unicode_fast`) | `66ec24276b1` | `SELECT lower(U&'\0345\03A3' COLLATE pg_unicode_fast);` | `U+0345 ς` | `U+0345 σ` |
| Hangul TBASE composition | `273fe94852b` | `SELECT normalize(U&'\AC00\11A7') = U&'\AC00\11A7';` | `f` (U+11A7 consumed) | `t` |
| Levenshtein overflow | `e88eb4e7663` | `SELECT levenshtein('', 'aa', 2147483647, 1, 1);` | `-2` (wrapped) | `22003 levenshtein distance out of range` |
| `ALTER TABLE OF` source USAGE (CVE-2026-6470) | `57f59ca1d95` | As a role with schema CREATE and no USAGE on composite `pair`: `CREATE TABLE t(id int); ALTER TABLE t OF pair;` | attaches | `42501 permission denied for type pair` |
| Aggregate argument limit | `2a03f21daf5` | `rank(1,...,50) WITHIN GROUP (ORDER BY 1,...,50)` (50 pairs) | server crash | `54023 aggregates cannot have more than 99 arguments` |

Two more need two sessions:

- **Row marks on function scans** (`f335457e8ad`). Session A: `CREATE TABLE
  p(id int, payload text); INSERT INTO p VALUES (1, 'a,b'); BEGIN; UPDATE p SET
  payload = 'new';`. Session B: `SELECT p.payload, f.v, f.n FROM p CROSS JOIN
  LATERAL string_to_table(p.payload, ',') WITH ORDINALITY AS f(v, n) ORDER BY
  f.n FOR UPDATE OF p;` (waits). Session A: `COMMIT;`. 18.0 skipped the
  function scan's row mark and reran it on the refreshed row:
  `(new,new,1),(new,new,1)`. 18.6 keeps each row's function tuple:
  `(new,a,1),(new,b,2)`.
- **Locking referenced objects when recording dependencies** (`c8cd3d6976f`,
  wording `c8b4186d6ee`). Session A: `CREATE SEQUENCE s; BEGIN; DROP SEQUENCE
  s;`. Session B: `CREATE TABLE d(a int CHECK (nextval('s') > 0));`. 18.0
  creates `d` immediately, and after A commits `INSERT INTO d VALUES (1)` fails
  `XX000 could not open relation with OID ...` (orphaned dependency). 18.6
  makes B wait; after A commits B fails `42704 referenced relation was
  concurrently dropped`, and after A rolls back B succeeds.
