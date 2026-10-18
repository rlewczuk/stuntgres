# PostgreSQL regressions observed by the reference oracle

Upstream PostgreSQL defects that the pinned oracle (see
[reference/fixture/README.md](../reference/fixture/README.md#postgresql-oracle-policy-and-provenance))
exposes. Stuntgres does not imitate them; the affected reference tests record
the upstream result as an explicit release difference on the PostgreSQL side
and keep the intended behavior on the native side.

## `to_date`/`to_timestamp`: internal error for TM fields under an indeterminate collation

**Affected:** PostgreSQL 18.6 (first released there). Still present in the
development tree checked on 2026-10-18 (`38a6b8c7adb`). 18.0 behaves correctly.

**Introduced by:** commit `011384ba45f` "Fix calculating length of match to
localized month/weekday names" (Álvaro Herrera), back-patched into 18.6.

### Symptom

When `to_date` or `to_timestamp` parses a `TM`-prefixed month or day name
(`TMMon`, `TMMonth`, `TMDay`, `TMDy`) whose spelling does not match the
localized name exactly, and the call's input collation is indeterminate
(implicit collations conflict), the server fails with an internal error
instead of the indeterminate-collation diagnostic.

```sql
CREATE TABLE t(input text COLLATE "C", mask text COLLATE "POSIX");
INSERT INTO t VALUES ('05 dEc 2000', 'DD TMMon YYYY');
SELECT to_date(input, mask) FROM t;
```

| Release | SQLSTATE | Message | Hint |
|---|---|---|---|
| 18.0 (intended) | `42P22` | `could not determine which collation to use for upper() function` | `Use the COLLATE clause to set the collation explicitly.` |
| 18.6 | `XX000` | `cache lookup failed for collation 0` | none |

The same happens for `to_timestamp`, for `TMMonth`/`TMDay`/`TMDy`, for an
unknown name (`'05 Bogus 2000'`) and for all-uppercase input (`'05 DEC 2000'`).
Inputs that match a localized name byte for byte (`'05 Dec 2000'`) still
succeed, and an explicit `COLLATE` on either argument avoids the error.
Formatting with `to_char(..., 'TMMonth')` is unaffected and still reports
`42P22`.

### Cause

`seq_search_localized()` in `src/backend/utils/adt/formatting.c` first tries
an exact, case-sensitive match. Only when that fails does it case-fold the
input to compare case-insensitively. 18.0 folded through `str_toupper()` and
`str_tolower()`, which begin with:

```c
if (!OidIsValid(collid))
    ereport(ERROR,
            (errcode(ERRCODE_INDETERMINATE_COLLATION),
             errmsg("could not determine which collation to use for %s function", "upper()"),
             errhint("Use the COLLATE clause to set the collation explicitly.")));
```

The 18.6 change folds the array elements with `pg_strupper()`/`pg_strlower()`
on a `pg_locale_t`, so it now calls, before any of those checks:

```c
mylocale = pg_newlocale_from_collation(collid);
```

With an indeterminate collation `collid` is `InvalidOid` (0), and
`pg_newlocale_from_collation()` raises:

```c
if (!OidIsValid(collid))
    elog(ERROR, "cache lookup failed for collation %u", collid);
```

`elog(ERROR)` carries `XX000` and no hint, so the user-facing collation
diagnostic is lost.

### Suggested upstream fix

Check the collation before building the locale in `seq_search_localized()`,
reporting the same `42P22` error as `str_toupper()`, or move the
`pg_newlocale_from_collation()` call after the `str_toupper()` call that
already performs that check.

### Stuntgres behavior and tests

Stuntgres keeps the 18.0/intended `42P22` diagnostic. It does not produce an
internal error.
`TestReferenceDatetimeFormattingReviewCollationDemand`
(`reference/datetime_formatting/datetime_formatting_review_test.go`) expects
`XX000 cache lookup failed for collation 0` on the PostgreSQL side and `42P22`
on the native side. When a fixed PostgreSQL release is pinned, that
PostgreSQL-side branch must go, so both backends again assert `42P22`.
