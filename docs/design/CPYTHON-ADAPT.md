# CPYTHON-ADAPT: query conversion and type adaptation of the Python binding

Design of the layer of `bindings/cpython` (package `stuntgres`) that turns a DB-API call
into what the native library needs and results back into Python objects: the conversion
of pyformat placeholders into `$n`, the adaptation framework (dumpers, loaders, the
adapters map, the transformer), the type OID constants, the text-format adapters of the
core types and the PEP 249 type objects and constructors. Produced by catalog part
`CPYTHON-DESIGN-ADAPT`. It builds on `docs/design/CPYTHON-PQ.md` (called **PQ** below; "PQ
5.5" is its section 5.5) and is built on by `CPYTHON-DESIGN-DBAPI` and
`CPYTHON-DESIGN-FEATURES`.

Words used below: **(measured)** marks a value observed in the experiments of section 9;
**oracle mode** is real libpq 18.6 against the pinned PostgreSQL 18.6 image; **server
mode** is the same libpq against the stuntgres wire server; **unit mode** runs the tests
that need no native library. Every literal that depends on PostgreSQL (a text
representation, an OID, an error message of the server) was observed in oracle mode;
literals that follow from the binding's own algorithms (texts the dumpers produce, error
messages of the binding) were produced by the designed code, which ran as scratch code in
unit, oracle and server mode.

Nothing under `bindings/` exists yet (`ls bindings` fails at commit `0c704a82ce`); the
parts of PQ section 10.1 create the lower layers. This design uses only names those parts
define.

## 1. Purpose, layering, layout

```
stuntgres                      PEP 249 surface: Connection, Cursor   (CPYTHON-DESIGN-DBAPI)
   stuntgres._queries          %s, %(name)s, %% -> $1..$n                              (2)
   stuntgres._transformer      Transformer: dump a parameter sequence, load rows     (3.5)
   stuntgres.adapt             Dumper, Loader, AdaptersMap, context functions   (3.1-3.4)
   stuntgres.types.*           one module per type family                            (5, 6)
   stuntgres._defaults         the global map with every default adapter          (6.11)
   stuntgres._oids             type OID constants                                      (4)
   stuntgres.dbapi20           PEP 249 type objects and constructors                   (7)
stuntgres.pq                   PGconn, PGresult (get_rows: the row-read boundary)  (PQ 5)
stuntgres.errors               PEP 249 base classes                                (PQ 6)
```

What the cursor of CPYTHON-DESIGN-DBAPI does with these pieces (the **central path**; it
ran as `Session.run` in experiment 5 and is the helper `tests/adapt_db.py` of section
8.3):

1. `tx = Transformer(context)`, where `context` is the cursor (an `AdaptContext`, 3.1).
2. `sql, values = _queries.prepare(query, params, tx.encoding)`.
3. If `values is None`: `pgconn.send_query(sql)` (several statements allowed). Otherwise
   `dumped, oids = tx.dump_sequence(values)` and
   `pgconn.send_query_params(sql, dumped, oids)` (PQ 5.7; formats default to text).
4. For every result with rows: `tx.set_pgresult(result)`, then
   `tx.load_rows(first, last)` as rows are fetched.

### 1.1 Fixed points

| # | Point | Evidence |
|---|---|---|
| A1 | Standard library only, CPython 3.10 or newer, every module starts with `from __future__ import annotations` (rule C4). The code of this design was parsed with `ast.parse(..., feature_version=(3, 10))` and ran on CPython 3.12.3; no Python 3.10 or 3.11 interpreter was available, so statements about them rest on reading and on that parse. | section 9 |
| A2 | Text format only. The format is carried in the interfaces: `Dumper.format`, `Loader.format`, the `format` argument of `AdaptersMap.get_dumper`, `get_loader` and of `Transformer.get_dumper`, `get_loader`, `set_loader_types`; registrations are keyed by the class's `format`. A later binary wave adds classes and no signature. | plan A5; 3.1 |
| A3 | The converter does not parse SQL. With `params=None` the query is sent unchanged and may hold several statements; otherwise every `%` in the query is a placeholder or `%%`. | **(measured)** experiment 4 |
| A4 | `%t` means the same as `%s` (text format is what this wave sends). `%b` raises `NotSupportedError` during conversion. | 2.2; decided here |
| A5 | Conversions are cached by query text and encoding (`functools.lru_cache`, 512 entries, queries of at most 4096 bytes). | **(measured)** experiment 4 |
| A6 | An `AdaptersMap` is a snapshot of its template: a map made from another one sees the template's registrations up to that moment and none made later; registering replaces the map's dictionary instead of changing it (copy on write), so readers need no lock. | 3.2 |
| A7 | A `Transformer` belongs to one cursor and one `execute` call: the cursor makes a new one at the start of every `execute` and `executemany`, so adapters registered and session parameters changed before the call are in effect. It caches one dumper instance per Python class; it makes one loader instance per distinct column type of each result when the result is set, never per cell. | **(measured)** experiment 6: 10 columns of 9 types, 9 loader lookups in `set_pgresult`, 0 in `load_rows` |
| A8 | The transformer reads cells only through `PGresult.get_rows(first, last)` (PQ 5.5, 5.6), so the compiled row reader of plan U2 replaces `stuntgres/pq/_rows.py` and nothing here. | 3.7; **(measured)** experiment 6: of the time `load_rows` takes for 10000 cells of mixed types, more than half is `get_rows` |
| A9 | A type without a loader loads as `str`: the loader registered for OID 0 is the fallback. | 3.5; **(measured)** experiment 5 |
| A10 | A Python `int` is sent as `int4`, `int8` or `numeric`: the type PostgreSQL gives the same number written as a literal. psycopg 3 starts at `int2`; experiment 2 showed that this makes `%s + %s` overflow for two small parameters and makes a list of small ints an `int2[]`, which cannot be compared with an `integer[]` column or with `array[1,2,3]`. | **(measured)** 5.2 |
| A11 | `str` is sent with OID 0 (unknown), so the server infers the type from the context; so is `None`, an empty list, a list of only `None` and a list of `str`. | **(measured)** 5.2 |
| A12 | `date`, `timestamp` and `timestamptz` load only from `DateStyle` ISO and `interval` only from `IntervalStyle` postgres; any other style and any value Python cannot represent (infinity, BC, years above 9999, 24:00:00, intervals beyond `timedelta`) raise `DataError` with the messages of 6.1. The connection stays usable. | **(measured)** experiments 1 and 5 |
| A13 | `timestamptz` loads as an aware `datetime` whose wall time and UTC offset are exactly what the server printed. Its `tzinfo` is `zoneinfo.ZoneInfo(<TimeZone parameter>)` when Python knows that zone and it gives the printed offset, else `datetime.timezone(<printed offset>)`. | **(measured)** 6.5 |
| A14 | The dumps of `date`, `datetime` and `timedelta` are read the same way under every `DateStyle` and `IntervalStyle`. | **(measured)** experiment 2: 36 of 36 interval comparisons true in four styles; five date styles |
| A15 | Array elements are always quoted when dumped; arrays are parsed in character space (decoded first), so client encodings whose trail bytes look like `\` or `"` are safe. Lower bounds of an array are dropped. | 6.9, 6.10 |
| A16 | PEP 249 `DateFromTicks`, `TimeFromTicks` and `TimestampFromTicks` follow the text of PEP 249 (`time.localtime`), `Binary(x)` returns `bytes(x)`. | 7 |

Experiments changed these points against the part record or against psycopg 3, and
nothing an owner decision fixed: A10 (int), A13 (the zone is verified against the printed
offset instead of converting from UTC, which also removes an overflow at year 1), and the
split of two parts (8.1).

One server-mode difference was found on a path this design sends **(measured,
experiment 3)**: subscripting a parameter of unknown type (`select $1[1]`, which is what
`cur.execute("select %s[1]", (["a", "b"],))` sends) is answered by PostgreSQL 18.6 with
`42804 cannot subscript type unknown because it does not support subscripting`; the
stuntgres server closes the connection, and the simple-protocol form
`prepare p as select $1[1]` terminates the server process. It is filed as
`CPYTHON-ENGINE-03-SUBSCRIPT-UNKNOWN` (rule D8) and is a prerequisite of the part whose
test sends it. Every other statement of experiments 1 to 5 that belongs to the core set
answered identically in both modes (section 9).

### 1.2 Files

Everything is under `bindings/cpython/`. "Owner" is the one part that creates the file.

| File | Responsibility | Owner |
|---|---|---|
| `stuntgres/_queries.py` | Placeholder conversion (2). | CPYTHON-QUERY-01-CONVERT |
| `stuntgres/adapt.py` | `AdaptContext`, `Dumper`, `Loader`, `AdaptersMap`, context functions (3.1 to 3.4). | CPYTHON-ADAPT-01A-MAP |
| `stuntgres/types/__init__.py` | Package marker (docstring only). | CPYTHON-ADAPT-01A-MAP |
| `stuntgres/_defaults.py` | The global map (6.11). Stub by ADAPT-01A (8.2). | replaced by CPYTHON-ADAPT-11-DEFAULTS |
| `stuntgres/_transformer.py` | `Transformer` (3.5). | CPYTHON-ADAPT-01B-TRANSFORMER |
| `stuntgres/_oids.py` | OID constants (4). | CPYTHON-ADAPT-02-OIDS |
| `stuntgres/types/numeric.py` | bool, int2, int4, int8, oid, float4, float8, numeric (6.2). | CPYTHON-ADAPT-03-NUMERIC |
| `stuntgres/types/string.py` | text, varchar, bpchar, name, the fallback, bytea (6.3). | CPYTHON-ADAPT-04-STRING |
| `stuntgres/types/datetime.py` | date, time, timetz (6.4). | CPYTHON-ADAPT-05-DATE-TIME |
| `stuntgres/types/timestamp.py` | timestamp, timestamptz (6.5). | CPYTHON-ADAPT-06A-TIMESTAMP |
| `stuntgres/types/interval.py` | interval (6.6). | CPYTHON-ADAPT-06B-INTERVAL |
| `stuntgres/types/uuid.py`, `stuntgres/types/json.py` | uuid (6.7); json, jsonb, `Json`, `Jsonb` (6.8). | CPYTHON-ADAPT-07-UUID-JSON |
| `stuntgres/types/array.py` | Array parser and loader, `register_array` (6.9). | CPYTHON-ADAPT-08-ARRAY-LOAD |
| `stuntgres/types/list.py` | `ListDumper` (6.10). | CPYTHON-ADAPT-09-ARRAY-DUMP |
| `stuntgres/dbapi20.py` | PEP 249 type objects and constructors (7). | CPYTHON-ADAPT-10-DBAPI20-TYPES |
| `tests/adapt_util.py` | `FakePGconn`, `FakeConnection`: stand-ins for unit tests (3.4). | CPYTHON-ADAPT-01A-MAP |
| `tests/adapt_db.py` | `AdaptTestCase`: the central path for database tests (8.3). | CPYTHON-ADAPT-12A-ROUNDTRIP-BASIC |
| `tests/test_<name>.py` | One or two test files per part (8.1 names each). | the part |

No file of PQ section 1.2 is edited. `stuntgres/__init__.py` stays as
CPYTHON-BUILD-01-SKELETON wrote it; the names it will re-export are listed in 3.6 and 7
for the part of CPYTHON-DESIGN-DBAPI that builds the module surface.

### 1.3 Rules for every module of this design

- A module under `stuntgres/types/` is named after its PostgreSQL family, as in psycopg 3
  (`numeric`, `string`, `datetime`, `uuid`, `json`, `array`, `list`). Several of those
  names are also standard-library modules. Imports are absolute in Python 3, so
  `import json` inside `stuntgres/types/json.py` is the standard library; the sibling is
  reached only as `from . import json` or `from stuntgres.types import json`. Never run
  Python with `bindings/cpython/stuntgres` or `bindings/cpython/stuntgres/types` as the
  current or script directory: there the directory `types` and the files in it hide the
  standard library **(measured: `python3 -` started in `stuntgres/` failed in `import re`
  with `cannot import name 'MappingProxyType' from 'types'`)**. The runner puts
  `bindings/cpython` on the path, which is safe.
- The modules of this design import nothing from `stuntgres.pq` at run time (only under
  `if TYPE_CHECKING:`). They reach the connection through the duck-typed attributes named
  in 3.3. That keeps the pure parts free of every database part.
- Import graph (an arrow means "imports at module level"): `types.*` -> `adapt`, `_oids`,
  `errors`; `types.list` -> `_transformer`; `_transformer` -> `adapt`, `errors`;
  `_defaults` -> `adapt`, every `types.*`; `dbapi20` -> `_oids`; `_queries` -> `errors`.
  `adapt` imports `_defaults` only inside the function `context_adapters`, and
  `types.json` reaches the global map only through that function, so there is no cycle.
- Concurrency (the package must not rely on the GIL): module-level state is immutable
  after import or guarded. `_queries._convert_cached`, `types.timestamp.session_zone` and
  `types.array._tokens` are `functools.lru_cache` functions (internally locked); an
  `AdaptersMap` serialises its writers with a lock and replaces its dictionaries, which
  readers only read; compiled regular expressions and the OID tables are constants. A
  `Transformer` and the dumper and loader instances it makes are used by one thread at a
  time, like the cursor that owns them.
- Text of error messages is part of the design: tests compare it literally.

## 2. Query conversion (`stuntgres/_queries.py`)

PostgreSQL's own placeholders are `$1..$n`; PEP 249's `paramstyle` of this driver is
`pyformat` (plan U5). This module rewrites one into the other. It never looks at the
values and never parses SQL.

### 2.1 Public names

```python
Query = Union[str, bytes]
Params = Union[Sequence[Any], Mapping[str, Any]]

MAX_CACHED_LENGTH = 4096
CACHE_SIZE = 512

class ConvertedQuery(NamedTuple):
    query: bytes                    # the text with $1..$n
    names: tuple[str, ...] | None   # named style: the names in $n order; else None
    count: int                      # number of parameters the query takes

def to_bytes(query: Query, encoding: str = "utf-8") -> bytes: ...
def convert(query: bytes, encoding: str = "utf-8") -> ConvertedQuery: ...
def bind(converted: ConvertedQuery, params: Params) -> Sequence[Any]: ...
def prepare(
    query: Query, params: Params | None, encoding: str = "utf-8"
) -> tuple[bytes, Sequence[Any] | None]: ...
```

`prepare` is the one function the cursor calls. `encoding` is the Python codec of the
connection's client encoding (`Transformer.encoding`, 3.5).

### 2.2 Grammar of placeholders

When `params` is not `None`, every `%` in the query starts one of these:

| Written | Meaning |
|---|---|
| `%%` | One literal `%`. |
| `%s` | The next positional parameter. |
| `%t` | The same as `%s` (text format; A4). |
| `%(name)s`, `%(name)t` | The parameter called `name`. A name is any non-empty run of bytes without `)`; it is decoded with `encoding` and looked up in the mapping. The same name may be used many times and takes one `$n`. |
| `%b`, `%(name)b` | Binary format: not supported, `NotSupportedError`. |
| anything else | `ProgrammingError` (2.5). |

Positional parameters are numbered `$1, $2, ...` in the order they appear; named ones in
the order of first appearance. One query uses one style.

When `params` is `None` nothing is a placeholder: the query is sent byte for byte, `%s`
and `%%` included, and may contain several statements.

Consequences of not parsing SQL, each **(measured)** in experiment 4 on PostgreSQL 18.6
and identically on the stuntgres server:

| Call | Sent | Answer |
|---|---|---|
| `execute("select '100%'")` | `select '100%'` (simple query) | `100%` |
| `execute("select '%s', '%%'")` | unchanged | `%s`, `%%` |
| `execute("select '100%%', %s", (1,))` | `select '100%', $1` | `100%`, `1` |
| `execute("select '100%'", ())` | nothing | `ProgrammingError` (2.5, error 6) |
| `execute("select '%s'", ("x",))` | `select '$1'`, one value of unknown type | `42P18 could not determine data type of parameter $1` |
| `execute("select '%s'", (5,))` | `select '$1'`, one `int4` value | the text `$1` |
| `execute('select 1 as "%s"', ("x",))`, `"select 1 -- %s"`, `"select 1 /* %s */"`, `"select $$%s$$"` with `("x",)` | the text with `$1` inside the identifier, comment or body | `42P18 could not determine data type of parameter $1` |
| `execute("select 1 -- %s", (5,))` | `select 1 -- $1` | `1` |
| `execute("select $q$ %(a)s $q$, %(a)s", {"a": "x"})` | `select $q$ $1 $q$, $1` | ` $1 `, `x` |
| `execute("select %s0", (1,))` | `select $10` | `42P18 could not determine data type of parameter $2` |
| `execute("select 1; select 2", ())` | `select 1; select 2` with zero parameters | `42601 cannot insert multiple commands into a prepared statement` |
| `execute("select 1; select 2")` | unchanged (simple query) | two results |

So: a literal percent sign is doubled whenever parameters are given (an empty sequence
counts); a placeholder inside a string literal, a quoted identifier, a comment or a
dollar-quoted body is still replaced; a placeholder must not be followed directly by a
digit; a `$n` already present in the query is passed through and collides with the
generated numbers.

### 2.3 Algorithm

`to_bytes(query, encoding)`: `bytes` is returned as it is; `str` is returned as
`query.encode(encoding)`; anything else (also `bytearray`) raises
`TypeError(f"the query must be str or bytes, got {type(query).__name__}")`.

`_convert(query, encoding)` (private; `query` is `bytes`):

1. `out = []` (pieces of the result), `names = []` (names in order, as `bytes`),
   `numbers = {}` (name to its number), `positional = 0`, `pos = 0`, `end = len(query)`.
2. `i = query.find(b"%", pos)`. If `i < 0`: append `query[pos:]` and go to step 9.
3. Append `query[pos:i]`. If `i + 1 == end`: error 1 of 2.5.
4. `char = query[i + 1:i + 2]`. If it is `b"%"`: append `b"%"`, `pos = i + 2`, go to 2.
5. If `char == b"("`: `close = query.find(b")", i + 2)`; if `close < 0`: error 2;
   `name = query[i + 2:close]`; if it is empty: error 3; `char = query[close + 1:close + 2]`;
   `pos = close + 2`; `shown = query[i:pos].decode("utf-8", "replace")`; if `char` is
   empty: error 4. Otherwise (`char` is not `b"("`): `name = None`, `pos = i + 2`,
   `shown = query[i:pos].decode("utf-8", "replace")`.
6. If `char == b"b"`: error 5 (`NotSupportedError`). If `char` is neither `b"s"` nor
   `b"t"`: error 6.
7. Positional (`name is None`): if `names` is not empty: error 7; `positional += 1`;
   append `b"$%d" % positional`. Go to 2.
8. Named: if `positional` is not zero: error 7; `number = numbers.get(name)`; if `None`:
   append `name` to `names`, `number = numbers[name] = len(names)`; append
   `b"$%d" % number`. Go to 2.
9. If `names`: return `ConvertedQuery(b"".join(out), tuple(n.decode(encoding) for n in
   names), len(names))`; else return `ConvertedQuery(b"".join(out), None, positional)`.

`convert(query, encoding)`: if `len(query) <= MAX_CACHED_LENGTH` return
`_convert_cached(query, encoding)`, else `_convert(query, encoding)`.

`bind(converted, params)`; `names, count = converted.names, converted.count`:

1. If `params` is a `collections.abc.Mapping`: if `names is None`: if `count` is not
   zero raise error 9, else return `()`. `missing = [n for n in names if n not in
   params]`; if not empty raise error 11. Return `tuple(params[n] for n in names)`.
   Keys the query does not use are ignored.
2. If `params` is a `str`, `bytes` or `bytearray`, or is not a
   `collections.abc.Sequence`: raise error 8 (`TypeError`).
3. If `names is not None`: raise error 10. If `len(params) != count`: raise error 12.
4. Return `params` itself (not a copy).

`prepare(query, params, encoding)`: `raw = to_bytes(query, encoding)`; if `params is
None` return `(raw, None)`; `converted = convert(raw, encoding)`; return
`(converted.query, bind(converted, params))`.

### 2.4 Cache

`_convert_cached = functools.lru_cache(maxsize=CACHE_SIZE)(_convert)`. The key is the
pair (query bytes, encoding). A conversion that raises is not cached. Queries longer than
`MAX_CACHED_LENGTH` bytes are converted every time, so a large generated statement is not
kept alive by the cache **(measured: an 8005-byte query left `cache_info()` unchanged;
converting the same short query twice returned the same object)**. Parameter checks
(`bind`) are never cached: they depend on the call.

### 2.5 Errors

| # | Raised when | Class | Message |
|---|---|---|---|
| 1 | The query ends after `%`. | `ProgrammingError` | `incomplete placeholder: '%' at the end of the query (write '%%' for a literal percent sign)` |
| 2 | `%(` has no `)`. | `ProgrammingError` | `incomplete placeholder: {fragment!r} has no ')'`, where `fragment` is `query[i:i + 30].decode("utf-8", "replace")`; for `select %(a` it is `incomplete placeholder: '%(a' has no ')'` |
| 3 | `%()`. | `ProgrammingError` | `placeholder with an empty name: '%()'` |
| 4 | The query ends after `%(name)`. | `ProgrammingError` | `incomplete placeholder: '%(a)' has no format character` (with the placeholder as written) |
| 5 | Format `b`. | `NotSupportedError` | `binary parameters are not supported: '%b' (use 's' instead of 'b')`; for a named one `'%(a)b'` |
| 6 | Any other format character, a space included. | `ProgrammingError` | `unsupported placeholder '%d': use %s or %(name)s for a parameter and %% for a literal percent sign`. The placeholder is shown with `repr`, so `%'` appears as `"%'"`. |
| 7 | Both styles in one query. | `ProgrammingError` | `positional (%s) and named (%(name)s) placeholders cannot be mixed` |
| 8 | `params` is neither a sequence nor a mapping, or is `str`, `bytes`, `bytearray`. | `TypeError` | `query parameters must be a sequence or a mapping, got {type(params).__name__}` |
| 9 | Positional placeholders, mapping given. | `ProgrammingError` | `the query has positional placeholders (%s) but the parameters are a mapping` |
| 10 | Named placeholders, sequence given. | `ProgrammingError` | `the query has named placeholders (%(name)s) but the parameters are a sequence` |
| 11 | Names missing from the mapping. | `ProgrammingError` | `query parameters missing: 'a', 'c'` (every missing name with `repr`, in placeholder order, joined by `, `) |
| 12 | Wrong number of positional parameters. | `ProgrammingError` | `the query has 2 placeholders but 1 parameters were passed` (the two numbers as they are; the plural is not adjusted) |

The classes are those of `stuntgres/errors.py` (PQ 6). A query without placeholders
accepts an empty sequence and any mapping.

### 2.6 The complete file

```python
"""Conversion of pyformat placeholders (%s, %(name)s, %%) into $1..$n."""
from __future__ import annotations

from collections.abc import Mapping, Sequence
from functools import lru_cache
from typing import Any, NamedTuple, Union

from . import errors as e

Query = Union[str, bytes]
Params = Union[Sequence[Any], Mapping[str, Any]]

MAX_CACHED_LENGTH = 4096
CACHE_SIZE = 512


class ConvertedQuery(NamedTuple):
    """A query whose placeholders were replaced by $1..$n."""

    query: bytes
    names: tuple[str, ...] | None
    count: int


def to_bytes(query: Query, encoding: str = "utf-8") -> bytes:
    """Return the query as bytes in the connection's encoding."""
    if isinstance(query, bytes):
        return query
    if isinstance(query, str):
        return query.encode(encoding)
    raise TypeError(f"the query must be str or bytes, got {type(query).__name__}")


def _fragment(query: bytes, start: int) -> str:
    return query[start:start + 30].decode("utf-8", "replace")


def _convert(query: bytes, encoding: str) -> ConvertedQuery:
    out: list[bytes] = []
    names: list[bytes] = []
    numbers: dict[bytes, int] = {}
    positional = 0
    pos = 0
    end = len(query)
    while True:
        i = query.find(b"%", pos)
        if i < 0:
            out.append(query[pos:])
            break
        out.append(query[pos:i])
        if i + 1 == end:
            raise e.ProgrammingError(
                "incomplete placeholder: '%' at the end of the query"
                " (write '%%' for a literal percent sign)"
            )
        char = query[i + 1:i + 2]
        if char == b"%":
            out.append(b"%")
            pos = i + 2
            continue
        name: bytes | None = None
        if char == b"(":
            close = query.find(b")", i + 2)
            if close < 0:
                raise e.ProgrammingError(
                    f"incomplete placeholder: {_fragment(query, i)!r} has no ')'"
                )
            name = query[i + 2:close]
            if not name:
                raise e.ProgrammingError("placeholder with an empty name: '%()'")
            char = query[close + 1:close + 2]
            pos = close + 2
            shown = query[i:pos].decode("utf-8", "replace")
            if not char:
                raise e.ProgrammingError(
                    f"incomplete placeholder: {shown!r} has no format character"
                )
        else:
            pos = i + 2
            shown = query[i:pos].decode("utf-8", "replace")
        if char == b"b":
            raise e.NotSupportedError(
                f"binary parameters are not supported: {shown!r}"
                " (use 's' instead of 'b')"
            )
        if char not in (b"s", b"t"):
            raise e.ProgrammingError(
                f"unsupported placeholder {shown!r}: use %s or %(name)s for a"
                " parameter and %% for a literal percent sign"
            )
        if name is None:
            if names:
                raise e.ProgrammingError(_MIXED)
            positional += 1
            out.append(b"$%d" % positional)
        else:
            if positional:
                raise e.ProgrammingError(_MIXED)
            number = numbers.get(name)
            if number is None:
                names.append(name)
                number = numbers[name] = len(names)
            out.append(b"$%d" % number)
    if names:
        decoded = tuple(n.decode(encoding) for n in names)
        return ConvertedQuery(b"".join(out), decoded, len(names))
    return ConvertedQuery(b"".join(out), None, positional)


_MIXED = "positional (%s) and named (%(name)s) placeholders cannot be mixed"

_convert_cached = lru_cache(maxsize=CACHE_SIZE)(_convert)


def convert(query: bytes, encoding: str = "utf-8") -> ConvertedQuery:
    """Replace the placeholders of a query; results are cached by query text."""
    if len(query) <= MAX_CACHED_LENGTH:
        return _convert_cached(query, encoding)
    return _convert(query, encoding)


def bind(converted: ConvertedQuery, params: Params) -> Sequence[Any]:
    """Check the parameters against a converted query; return them in $n order."""
    names, count = converted.names, converted.count
    if isinstance(params, Mapping):
        if names is None:
            if count:
                raise e.ProgrammingError(
                    "the query has positional placeholders (%s)"
                    " but the parameters are a mapping"
                )
            return ()
        missing = [name for name in names if name not in params]
        if missing:
            raise e.ProgrammingError(
                "query parameters missing: " + ", ".join(repr(n) for n in missing)
            )
        return tuple(params[name] for name in names)
    if isinstance(params, (str, bytes, bytearray)) or not isinstance(params, Sequence):
        raise TypeError(
            "query parameters must be a sequence or a mapping,"
            f" got {type(params).__name__}"
        )
    if names is not None:
        raise e.ProgrammingError(
            "the query has named placeholders (%(name)s)"
            " but the parameters are a sequence"
        )
    if len(params) != count:
        raise e.ProgrammingError(
            f"the query has {count} placeholders but {len(params)} parameters"
            " were passed"
        )
    return params


def prepare(
    query: Query, params: Params | None, encoding: str = "utf-8"
) -> tuple[bytes, Sequence[Any] | None]:
    """Return what to send: (query, None) unchanged, or ($n query, values)."""
    raw = to_bytes(query, encoding)
    if params is None:
        return raw, None
    converted = convert(raw, encoding)
    return converted.query, bind(converted, params)
```

## 3. Framework

### 3.1 Protocols (`stuntgres/adapt.py`)

```python
TEXT = 0
BINARY = 1

class AdaptContext(Protocol):
    @property
    def adapters(self) -> AdaptersMap: ...
    @property
    def connection(self) -> Any: ...

class Dumper:
    format: int = TEXT
    oid: int = 0
    def __init__(self, cls: type, context: AdaptContext | None = None) -> None: ...
    def dump(self, obj: Any) -> bytes: ...
    def dump_typed(self, obj: Any) -> tuple[bytes, int]: ...

class Loader:
    format: int = TEXT
    def __init__(self, oid: int, context: AdaptContext | None = None) -> None: ...
    def load(self, data: bytes) -> Any: ...
```

`TEXT` and `BINARY` equal `pq.Format.TEXT` and `pq.Format.BINARY` (PQ 4.1); they are
plain integers here so that this module does not import `stuntgres.pq`.

**`AdaptContext`** is what adaptation is configured by: an object with the property
`adapters` (an `AdaptersMap`) and the property `connection`, which is `None` or an object
whose attribute `pgconn` is a `stuntgres.pq.PGconn` (or `None`). Three kinds of object
are contexts: an `AdaptersMap` (its `adapters` is itself, its `connection` is `None`), a
`Transformer` (3.5), and the `Connection` and `Cursor` of CPYTHON-DESIGN-DBAPI, which
must provide exactly these names: `Connection.adapters`, `Connection.connection`
(returning the connection itself), `Connection.pgconn`, `Cursor.adapters`,
`Cursor.connection`. In unit tests `tests/adapt_util.FakeConnection` plays the
connection.

**`Dumper`** converts Python objects of one class into the text PostgreSQL reads.

| Member | Contract |
|---|---|
| `format` | Class attribute: the format the dumper produces (`TEXT` in this wave). |
| `oid` | Class attribute: the OID of the type the text is declared as; 0 means unknown (the server infers the type). |
| `__init__(cls, context)` | `self.cls = cls` (the class of the objects it will get); `self.context = context`. A dumper made by a transformer gets that transformer as context. Subclasses that need the client encoding read `context_encoding(context)` here. |
| `dump(obj)` | The value as input text of the type, in the client encoding, never `None` (SQL NULL is handled by the transformer). The base class raises `NotImplementedError`. |
| `dump_typed(obj)` | `return self.dump(obj), self.oid`. Dumpers whose OID depends on the value (int, time, datetime, list) override it. The transformer and `ListDumper` call only this method. |

**`Loader`** converts the text of one type into a Python object.

| Member | Contract |
|---|---|
| `format` | Class attribute: the format the loader reads (`TEXT`). |
| `__init__(oid, context)` | `self.oid = oid` (the OID of the column it was made for; one class may serve several OIDs); `self.context = context`. Session facts (encoding, `TimeZone`) are read here, once per loader. |
| `load(data)` | The Python object for a non-NULL value; `data` is exactly the bytes libpq returned. The base class raises `NotImplementedError`. A value that cannot be represented or parsed raises `stuntgres.errors.DataError` (6.1). |

### 3.2 `AdaptersMap` (`stuntgres/adapt.py`)

```python
class AdaptersMap:
    def __init__(self, template: AdaptersMap | None = None) -> None: ...
    @property
    def adapters(self) -> AdaptersMap: ...
    @property
    def connection(self) -> None: ...
    def register_dumper(self, cls: type, dumper: type[Dumper]) -> None: ...
    def register_loader(self, oid: int, loader: type[Loader]) -> None: ...
    def register_array_oid(self, oid: int, array_oid: int) -> None: ...
    def get_dumper(self, cls: type, format: int = TEXT) -> type[Dumper]: ...
    def get_loader(self, oid: int, format: int = TEXT) -> type[Loader] | None: ...
    def get_array_oid(self, oid: int) -> int: ...
```

State: `_lock` (a `threading.Lock`), `_dumpers: dict[tuple[type, int], type[Dumper]]`
keyed by (class, format), `_loaders: dict[tuple[int, int], type[Loader]]` keyed by (OID,
format), `_array_oids: dict[int, int]` (element OID to array OID).

| Member | Algorithm |
|---|---|
| `__init__(template)` | Make the lock and three empty dictionaries; if `template` is not `None`, take the template's three dictionary objects instead (no copy). |
| `adapters`, `connection` | `self`, `None`: a map is a context. |
| `register_dumper(cls, dumper)` | If `cls` is not a class raise `TypeError(f"a class expected, got {cls!r}")`. Under the lock: `changed = dict(self._dumpers)`; `changed[cls, dumper.format] = dumper`; `self._dumpers = changed`. |
| `register_loader(oid, loader)` | If `oid` is not an `int` raise `TypeError(f"an OID (int) expected, got {oid!r}")`. Under the lock, the same replace-on-write with `changed[oid, loader.format] = loader`. OID 0 registers the fallback loader. |
| `register_array_oid(oid, array_oid)` | Under the lock, the same with `changed[oid] = array_oid`. |
| `get_dumper(cls, format)` | `dumpers = self._dumpers` (one read); for `base` in `cls.__mro__`: if `(base, format)` is in `dumpers` return its value. Nothing found: raise `ProgrammingError(f"cannot adapt type {cls.__qualname__!r} as a query parameter")`. No lock. |
| `get_loader(oid, format)` | `self._loaders.get((oid, format))`. No lock, no fallback (the transformer applies the fallback). |
| `get_array_oid(oid)` | `self._array_oids.get(oid, 0)`. |

Because a registration replaces the dictionary, a map made from a template keeps the
dictionaries the template had at that moment: later registrations on either side are not
seen by the other, and a dictionary that a reader holds never changes. A later
registration for the same key replaces the earlier one. The lookup along the MRO makes a
dumper registered for a class serve its subclasses (`bool` before `int` because `bool`
has its own registration; an `enum.IntEnum` member is dumped by the `int` dumper).

Inheritance of maps (plan U6): the global map `stuntgres._defaults.adapters` (6.11); a
connection has `AdaptersMap(<global map>)` made when it is created; a cursor has
`AdaptersMap(connection.adapters)` made when it is created. Making one costs three
attribute reads.

### 3.3 Context functions (`stuntgres/adapt.py`)

```python
def context_adapters(context: AdaptContext | None) -> AdaptersMap: ...
def context_pgconn(context: AdaptContext | None) -> PGconn | None: ...
def context_encoding(context: AdaptContext | None) -> str: ...
def context_parameter(context: AdaptContext | None, name: bytes) -> bytes | None: ...
```

| Function | Algorithm |
|---|---|
| `context_adapters(context)` | `context.adapters` when `context` is not `None`; else `from . import _defaults` (inside the function) and return `_defaults.adapters`. |
| `context_pgconn(context)` | `None` when `context` is `None`, when `context.connection` is `None`, when `connection.pgconn` is `None` or when `pgconn.status != 0` (`ConnStatus.OK`, PQ 5.4); else the `pgconn`. |
| `context_encoding(context)` | `pgconn._encoding` (PQ 5.4: the Python codec of `client_encoding`) of `context_pgconn(context)`; `"utf-8"` when that is `None`. |
| `context_parameter(context, name)` | `pgconn.parameter_status(name)` (PQ 5.4) of `context_pgconn(context)`; `None` when that is `None`. |

These four functions are the only way the modules of this design read the connection.

### 3.4 The complete files of CPYTHON-ADAPT-01A-MAP

`stuntgres/adapt.py`:

```python
"""Adaptation framework: dumpers, loaders and the map that registers them."""
from __future__ import annotations

import threading
from typing import TYPE_CHECKING, Any, Protocol

from . import errors as e

if TYPE_CHECKING:
    from .pq import PGconn

TEXT = 0
BINARY = 1


class AdaptContext(Protocol):
    """What gives adaptation its configuration: a map, a connection or a cursor."""

    @property
    def adapters(self) -> AdaptersMap:
        """The map dumpers and loaders are looked up in."""
        ...

    @property
    def connection(self) -> Any:
        """An object whose attribute `pgconn` is a pq.PGconn, or None."""
        ...


class Dumper:
    """Convert Python objects of one class into the text PostgreSQL reads."""

    format: int = TEXT
    oid: int = 0

    def __init__(self, cls: type, context: AdaptContext | None = None) -> None:
        self.cls = cls
        self.context = context

    def dump(self, obj: Any) -> bytes:
        """Return the value as PostgreSQL input text in the client encoding."""
        raise NotImplementedError

    def dump_typed(self, obj: Any) -> tuple[bytes, int]:
        """Return the dumped value and the OID to declare for it (0: unknown)."""
        return self.dump(obj), self.oid


class Loader:
    """Convert the text PostgreSQL sends for one type into a Python object."""

    format: int = TEXT

    def __init__(self, oid: int, context: AdaptContext | None = None) -> None:
        self.oid = oid
        self.context = context

    def load(self, data: bytes) -> Any:
        """Return the Python object for a non-NULL value."""
        raise NotImplementedError


class AdaptersMap:
    """Dumpers by Python class and loaders by type OID, with inheritance."""

    def __init__(self, template: AdaptersMap | None = None) -> None:
        self._lock = threading.Lock()
        self._dumpers: dict[tuple[type, int], type[Dumper]] = {}
        self._loaders: dict[tuple[int, int], type[Loader]] = {}
        self._array_oids: dict[int, int] = {}
        if template is not None:
            self._dumpers = template._dumpers
            self._loaders = template._loaders
            self._array_oids = template._array_oids

    @property
    def adapters(self) -> AdaptersMap:
        """The map itself, so that a map can be used as a context."""
        return self

    @property
    def connection(self) -> None:
        """A map has no connection."""
        return None

    def register_dumper(self, cls: type, dumper: type[Dumper]) -> None:
        """Use `dumper` for objects of class `cls` and of its subclasses."""
        if not isinstance(cls, type):
            raise TypeError(f"a class expected, got {cls!r}")
        with self._lock:
            changed = dict(self._dumpers)
            changed[cls, dumper.format] = dumper
            self._dumpers = changed

    def register_loader(self, oid: int, loader: type[Loader]) -> None:
        """Use `loader` for values of the type with this OID (0: the fallback)."""
        if not isinstance(oid, int):
            raise TypeError(f"an OID (int) expected, got {oid!r}")
        with self._lock:
            changed = dict(self._loaders)
            changed[oid, loader.format] = loader
            self._loaders = changed

    def register_array_oid(self, oid: int, array_oid: int) -> None:
        """Record the OID of the array type whose elements have type `oid`."""
        with self._lock:
            changed = dict(self._array_oids)
            changed[oid] = array_oid
            self._array_oids = changed

    def get_dumper(self, cls: type, format: int = TEXT) -> type[Dumper]:
        """Return the dumper class for `cls`, looking along its MRO."""
        dumpers = self._dumpers
        for base in cls.__mro__:
            found = dumpers.get((base, format))
            if found is not None:
                return found
        raise e.ProgrammingError(
            f"cannot adapt type {cls.__qualname__!r} as a query parameter"
        )

    def get_loader(self, oid: int, format: int = TEXT) -> type[Loader] | None:
        """Return the loader class registered for an OID, or None."""
        return self._loaders.get((oid, format))

    def get_array_oid(self, oid: int) -> int:
        """Return the OID of the array type of an element type, or 0."""
        return self._array_oids.get(oid, 0)


def context_adapters(context: AdaptContext | None) -> AdaptersMap:
    """Return the map of a context; the global map when there is none."""
    if context is not None:
        return context.adapters
    from . import _defaults

    return _defaults.adapters


def context_pgconn(context: AdaptContext | None) -> PGconn | None:
    """Return the open pq connection behind a context, or None."""
    if context is None:
        return None
    connection = context.connection
    if connection is None:
        return None
    pgconn: PGconn | None = connection.pgconn
    if pgconn is None or pgconn.status != 0:
        return None
    return pgconn


def context_encoding(context: AdaptContext | None) -> str:
    """Return the Python codec of the connection's client encoding."""
    pgconn = context_pgconn(context)
    return pgconn._encoding if pgconn is not None else "utf-8"


def context_parameter(context: AdaptContext | None, name: bytes) -> bytes | None:
    """Return a parameter status of the connection, None when unknown."""
    pgconn = context_pgconn(context)
    return pgconn.parameter_status(name) if pgconn is not None else None
```

`stuntgres/types/__init__.py` is one line:

```python
"""Dumpers and loaders of the built-in PostgreSQL types, one module per family."""
```

`tests/adapt_util.py` (stand-ins used by the unit tests of every later part and, for
`FakeConnection`, by the database tests until CPYTHON-DESIGN-DBAPI provides the real
connection):

```python
"""Stand-ins for the connection objects that adaptation reads."""
from __future__ import annotations

from typing import Any

from stuntgres.adapt import AdaptersMap, context_adapters


class FakePGconn:
    """What adaptation reads of a pq.PGconn: status, parameters, the encoding."""

    status = 0

    def __init__(
        self, parameters: dict[bytes, bytes] | None = None, encoding: str = "utf-8"
    ) -> None:
        self.parameters = dict(parameters or {})
        self._encoding = encoding

    def parameter_status(self, name: bytes) -> bytes | None:
        """Return a session parameter, None when unknown."""
        return self.parameters.get(name)


class FakeConnection:
    """An AdaptContext: `adapters`, `connection` (itself) and `pgconn`."""

    def __init__(self, pgconn: Any = None, adapters: AdaptersMap | None = None) -> None:
        self.pgconn = pgconn if pgconn is not None else FakePGconn()
        if adapters is None:
            adapters = AdaptersMap(context_adapters(None))
        self.adapters = adapters

    @property
    def connection(self) -> FakeConnection:
        """The connection is its own context."""
        return self
```

### 3.5 `Transformer` (`stuntgres/_transformer.py`)

```python
RowMaker = Callable[[tuple[Any, ...]], Any]

class Transformer:
    encoding: str
    def __init__(self, context: AdaptContext | None = None) -> None: ...
    @classmethod
    def from_context(cls, context: AdaptContext | None) -> Transformer: ...
    @property
    def adapters(self) -> AdaptersMap: ...
    @property
    def connection(self) -> Any: ...
    @property
    def pgresult(self) -> PGresult | None: ...
    def get_dumper(self, cls: type, format: int = TEXT) -> Dumper: ...
    def dump_sequence(
        self, params: Sequence[Any]
    ) -> tuple[list[bytes | None], list[int]]: ...
    def get_loader(self, oid: int, format: int = TEXT) -> Loader: ...
    def set_pgresult(self, result: PGresult | None) -> None: ...
    def set_loader_types(self, oids: Sequence[int], format: int = TEXT) -> None: ...
    def load_rows(
        self, first: int, last: int, make_row: RowMaker | None = None
    ) -> list[Any]: ...
    def load_row(self, row: int, make_row: RowMaker | None = None) -> Any: ...
    def load_sequence(self, record: Sequence[bytes | None]) -> tuple[Any, ...]: ...
```

A transformer is itself an `AdaptContext` (it has `adapters` and `connection`): it is the
context handed to every dumper and loader it makes.

| Member | Algorithm |
|---|---|
| `__init__(context)` | `self._adapters = context_adapters(context)`; `self._connection = context.connection` (or `None` without context); `self.encoding = context_encoding(context)` (read once); `self._dumpers = {}` (instances by (class, format)); `self._pgresult = None`; `self._row_loaders = []` (one bound `load` method per column). |
| `from_context(context)` | `context` itself when it is a `Transformer`, else `cls(context)`. Used by adapters that need a transformer (`ListDumper`). |
| `adapters`, `connection`, `pgresult` | The stored values. |
| `get_dumper(cls, format)` | Look `(cls, format)` up in `self._dumpers`; on a miss make `self._adapters.get_dumper(cls, format)(cls, self)`, store and return it. One instance per exact class: a subclass gets an instance of its own whose `cls` is the subclass. Raises what `AdaptersMap.get_dumper` raises. |
| `dump_sequence(params)` | For each parameter in order: `None` gives value `None` and OID 0; otherwise `data, oid = self.get_dumper(type(param)).dump_typed(param)` (looking the instance up in `self._dumpers` first). Returns `(values, oids)`, two lists as long as `params`: the arguments `param_values` and `param_types` of `PGconn.send_query_params` (PQ 5.7). |
| `get_loader(oid, format)` | `cls = self._adapters.get_loader(oid, format)`; if `None`: `cls = self._adapters.get_loader(0, format)`; if still `None` raise `InterfaceError(f"no loader for the type with OID {oid} and no fallback loader")`. Return `cls(oid, self)`: a new instance every call. |
| `set_pgresult(result)` | Store it. `None`: forget the loaders and return. Otherwise build the column list `[(result.ftype(i), result.fformat(i)) for i in range(result.nfields)]` and call `_set_loaders`. |
| `set_loader_types(oids, format)` | `_set_loaders([(oid, format) for oid in oids])`: for records that do not come from a result (COPY rows, CPYTHON-DESIGN-FEATURES). Does not touch `pgresult`. |
| `_set_loaders(columns)` | Private. With a dictionary local to the call, make one loader per distinct `(oid, format)` through `get_loader` and store its bound `load` method once per column in `self._row_loaders`. Loaders are not kept from one result to the next, so session parameters are read again for every result. |
| `load_rows(first, last, make_row)` | No result set: raise `InterfaceError("no result to load rows from")`. `records = self._pgresult.get_rows(first, last)` (one call; it clamps the range, PQ 5.5). For each record build `tuple([None if value is None else load(value) for load, value in zip(loaders, record)])`; append it, or `make_row(it)` when `make_row` is given. Return the list. |
| `load_row(row, make_row)` | `rows = self.load_rows(row, row + 1, make_row)`; the first element, or `None` when the row does not exist (also for a negative number). |
| `load_sequence(record)` | If `len(record) != len(self._row_loaders)` raise `InterfaceError(f"cannot load a record of {len(record)} values with {len(loaders)} loaders")`; else the tuple built as in `load_rows`. |

`make_row` is the hook for the row factories of CPYTHON-DESIGN-DBAPI: a callable that
receives the tuple of loaded values of one row.

The complete file:

```python
"""The object that dumps the parameters and loads the rows of one cursor."""
from __future__ import annotations

from typing import TYPE_CHECKING, Any, Callable, Sequence

from . import errors as e
from .adapt import TEXT, AdaptContext, AdaptersMap, Dumper, Loader
from .adapt import context_adapters, context_encoding

if TYPE_CHECKING:
    from .pq import PGresult

RowMaker = Callable[[tuple[Any, ...]], Any]


class Transformer:
    """Dump parameter sequences and load result rows with cached adapters."""

    def __init__(self, context: AdaptContext | None = None) -> None:
        self._adapters = context_adapters(context)
        self._connection = context.connection if context is not None else None
        self.encoding = context_encoding(context)
        self._dumpers: dict[tuple[type, int], Dumper] = {}
        self._pgresult: PGresult | None = None
        self._row_loaders: list[Callable[[bytes], Any]] = []

    @classmethod
    def from_context(cls, context: AdaptContext | None) -> Transformer:
        """Return `context` when it is a Transformer, else a new one for it."""
        if isinstance(context, Transformer):
            return context
        return cls(context)

    @property
    def adapters(self) -> AdaptersMap:
        """The map this transformer looks adapters up in."""
        return self._adapters

    @property
    def connection(self) -> Any:
        """The connection of the context, or None."""
        return self._connection

    @property
    def pgresult(self) -> PGresult | None:
        """The result whose rows `load_rows` converts."""
        return self._pgresult

    def get_dumper(self, cls: type, format: int = TEXT) -> Dumper:
        """Return the dumper instance for a class; one instance per class."""
        key = (cls, format)
        dumper = self._dumpers.get(key)
        if dumper is None:
            dumper = self._adapters.get_dumper(cls, format)(cls, self)
            self._dumpers[key] = dumper
        return dumper

    def dump_sequence(
        self, params: Sequence[Any]
    ) -> tuple[list[bytes | None], list[int]]:
        """Dump the parameters of one statement into values and OIDs."""
        values: list[bytes | None] = []
        oids: list[int] = []
        dumpers = self._dumpers
        for param in params:
            if param is None:
                values.append(None)
                oids.append(0)
                continue
            dumper = dumpers.get((type(param), TEXT))
            if dumper is None:
                dumper = self.get_dumper(type(param))
            data, oid = dumper.dump_typed(param)
            values.append(data)
            oids.append(oid)
        return values, oids

    def get_loader(self, oid: int, format: int = TEXT) -> Loader:
        """Return a new loader for a type OID; unknown types use the fallback."""
        cls = self._adapters.get_loader(oid, format)
        if cls is None:
            cls = self._adapters.get_loader(0, format)
            if cls is None:
                raise e.InterfaceError(
                    f"no loader for the type with OID {oid} and no fallback loader"
                )
        return cls(oid, self)

    def set_pgresult(self, result: PGresult | None) -> None:
        """Choose one loader per column of `result`; None forgets the result."""
        self._pgresult = result
        if result is None:
            self._row_loaders = []
            return
        self._set_loaders(
            [(result.ftype(i), result.fformat(i)) for i in range(result.nfields)]
        )

    def set_loader_types(self, oids: Sequence[int], format: int = TEXT) -> None:
        """Choose the loaders of `load_sequence` from a list of type OIDs."""
        self._set_loaders([(oid, format) for oid in oids])

    def _set_loaders(self, columns: list[tuple[int, int]]) -> None:
        made: dict[tuple[int, int], Callable[[bytes], Any]] = {}
        loaders = []
        for column in columns:
            load = made.get(column)
            if load is None:
                load = made[column] = self.get_loader(*column).load
            loaders.append(load)
        self._row_loaders = loaders

    def load_rows(
        self, first: int, last: int, make_row: RowMaker | None = None
    ) -> list[Any]:
        """Convert rows first..last-1 of the result into tuples (or rows)."""
        if self._pgresult is None:
            raise e.InterfaceError("no result to load rows from")
        loaders = self._row_loaders
        rows: list[Any] = []
        for record in self._pgresult.get_rows(first, last):
            row = tuple(
                [
                    None if value is None else load(value)
                    for load, value in zip(loaders, record)
                ]
            )
            rows.append(row if make_row is None else make_row(row))
        return rows

    def load_row(self, row: int, make_row: RowMaker | None = None) -> Any:
        """Convert one row of the result; None when the row does not exist."""
        rows = self.load_rows(row, row + 1, make_row)
        return rows[0] if rows else None

    def load_sequence(self, record: Sequence[bytes | None]) -> tuple[Any, ...]:
        """Convert one record of values with the loaders that were set."""
        loaders = self._row_loaders
        if len(record) != len(loaders):
            raise e.InterfaceError(
                f"cannot load a record of {len(record)} values"
                f" with {len(loaders)} loaders"
            )
        return tuple(
            [
                None if value is None else load(value)
                for load, value in zip(loaders, record)
            ]
        )
```

### 3.6 The registry contract

This is what a user of the driver may rely on (plan U6); CPYTHON-DESIGN-DBAPI re-exports
`stuntgres.adapters` (the object `stuntgres._defaults.adapters`) and documents it for
consumers.

1. **Where.** `stuntgres.adapters` configures connections made afterwards;
   `connection.adapters` configures that connection and the cursors made from it
   afterwards; `cursor.adapters` configures that cursor from its next `execute` on. An
   existing connection or cursor is never changed by a registration on its parent (A6).
2. **A dumper** is a subclass of `stuntgres.adapt.Dumper` with `dump(self, obj) -> bytes`
   and the class attribute `oid` (0 when the server should infer the type). It is
   registered with `adapters.register_dumper(SomeClass, TheDumper)` and then serves
   `SomeClass` and its subclasses. `dump` returns the text PostgreSQL accepts as input
   for that type, encoded in the client encoding; a text parameter cannot contain a NUL
   byte.
3. **A loader** is a subclass of `stuntgres.adapt.Loader` with `load(self, data: bytes)`.
   It is registered with `adapters.register_loader(oid, TheLoader)`; `data` is the text
   output of the type. Registering for OID 0 replaces the fallback that returns `str`.
   The OID of a user-defined type is found with
   `select oid, typarray from pg_type where typname = '...'`; this wave has no catalog
   lookup helper.
4. **Arrays.** `stuntgres.types.array.register_array(element_oid, array_oid, context)`
   makes the context load arrays of that type as lists (elements go through the loader
   of `element_oid`, or the fallback) and makes a list of objects whose dumper has
   `oid == element_oid` go out as `array_oid`. Without it such a list goes out with OID
   0 and the array column loads as `str`.
5. **Replacing a default** is a registration for the same class or OID, for example
   `adapters.register_dumper(str, stuntgres.types.string.StrTextDumper)` to send `str`
   as `text` instead of unknown (5.2 tells when that helps and what it breaks).
6. **JSON functions.** `stuntgres.types.json.set_json_dumps(dumps, context=None)` and
   `set_json_loads(loads, context=None)` (6.8); with `context=None` they change the
   global map.
7. **What a context gives an adapter.** `self.context` in a dumper or loader is the
   transformer; `stuntgres.adapt.context_encoding(self.context)` and
   `context_parameter(self.context, b"TimeZone")` read the session.
8. **Errors.** An adapter raises `stuntgres.errors.DataError` for a value it cannot
   convert. An exception from an adapter leaves the connection usable: dumping happens
   before anything is sent, loading after the result was received.

Worked example **(measured in experiment 5 and in the test `RegistryTest`, both modes)**:

```python
class PointLoader(adapt.Loader):
    def load(self, data):                     # b"(1,2.5)"
        x, y = data.decode()[1:-1].split(",")
        return float(x), float(y)

class PointDumper(adapt.Dumper):
    oid = 600                                 # point
    def dump(self, obj):
        return b"(%g,%g)" % (obj.x, obj.y)

conn.adapters.register_loader(600, PointLoader)
conn.adapters.register_dumper(Point, PointDumper)
# select %s, %s[0], point(3,4)  with (Point(1, 2.5), Point(5, 6))
#   -> ((1.0, 2.5), 5.0, (3.0, 4.0)), column OIDs [600, 701, 600]
```

### 3.7 The row-read boundary

`Transformer.load_rows` calls `PGresult.get_rows(first, last)` once per call and nothing
else of the result while converting; `set_pgresult` calls `nfields`, `ftype` and
`fformat` once per column. `get_rows` is the only caller of
`stuntgres/pq/_rows.py: read_rows` (PQ 5.6), the seam of plan U2: a compiled reader
replaces that function and no line of this design. No code of this design calls
`PGresult.get_value`.

Cost **(measured, experiment 6, oracle mode, 10000 cells, best of 7)**:

| Result | `get_rows` alone | `load_rows` | of which conversion | a loader per cell instead | lookups in `set_pgresult` | lookups in `load_rows` |
|---|---|---|---|---|---|---|
| 10 columns x 1000 rows, mixed | 6.4 ms | 10.5 ms | 4.1 ms | 18.9 ms | 9 | 0 |
| 1 column x 10000 rows, int4 | 7.6 ms | 9.7 ms | 2.1 ms | 17.4 ms | 1 | 0 |
| 1 column x 10000 rows, text | 7.9 ms | 9.7 ms | 1.8 ms | 25.9 ms | 1 | 0 |
| 1 column x 10000 rows, timestamptz | 7.4 ms | 27.6 ms | 20.2 ms | 41.5 ms | 1 | 0 |
| 1 column x 10000 rows, int4[] of 3 elements | 7.7 ms | 28.7 ms | 21.0 ms | 48.1 ms | 2 | 0 |

The lookups counted are calls of `AdaptersMap.get_loader`: one per distinct column type
when the result is set (the array loader adds one for its element loader) and none while
rows are loaded. Server mode gave the same counts (`exp6/server.txt`).

## 4. OID constants (`stuntgres/_oids.py`)

One module holds every type OID the binding names. The values are those of `pg_type` in
PostgreSQL 18.6 and in the stuntgres server **(measured in both modes with the query
below; the 24 rows were identical)**:

```sql
select typname, oid, typarray, typdelim from pg_type
where typname in ('bool','bytea','name','int8','int2','int4','text','oid','json','float4',
 'float8','bpchar','varchar','date','time','timestamp','timestamptz','interval','timetz',
 'numeric','uuid','jsonb') order by oid
```

| typname | oid | typarray | | typname | oid | typarray |
|---|---|---|---|---|---|---|
| bool | 16 | 1000 | | bpchar | 1042 | 1014 |
| bytea | 17 | 1001 | | varchar | 1043 | 1015 |
| name | 19 | 1003 | | date | 1082 | 1182 |
| int8 | 20 | 1016 | | time | 1083 | 1183 |
| int2 | 21 | 1005 | | timestamp | 1114 | 1115 |
| int4 | 23 | 1007 | | timestamptz | 1184 | 1185 |
| text | 25 | 1009 | | interval | 1186 | 1187 |
| oid | 26 | 1028 | | timetz | 1266 | 1270 |
| json | 114 | 199 | | numeric | 1700 | 1231 |
| float4 | 700 | 1021 | | uuid | 2950 | 2951 |
| float8 | 701 | 1022 | | jsonb | 3802 | 3807 |

`typdelim` is `,` for all of them. Also defined: `INVALID_OID = 0` (no type: the unknown
parameter type and the key of the fallback loader) and `UNKNOWN_OID = 705` (the
pseudo-type `unknown`, `typarray` 0).

Names: for a type `xyz` the constants are `XYZ_OID` and `XYZ_ARRAY_OID`.
`BUILTIN_TYPES` is a tuple of `(typname, oid, array_oid)` in the order of the table read
column by column (ascending `oid`); `types/array.py` and the catalog test iterate it.
Nothing else is in the module; it imports nothing.

The complete file:

```python
"""OIDs of the built-in PostgreSQL types the binding adapts."""
from __future__ import annotations

INVALID_OID = 0
UNKNOWN_OID = 705

BOOL_OID = 16
BYTEA_OID = 17
NAME_OID = 19
INT8_OID = 20
INT2_OID = 21
INT4_OID = 23
TEXT_OID = 25
OID_OID = 26
JSON_OID = 114
FLOAT4_OID = 700
FLOAT8_OID = 701
BPCHAR_OID = 1042
VARCHAR_OID = 1043
DATE_OID = 1082
TIME_OID = 1083
TIMESTAMP_OID = 1114
TIMESTAMPTZ_OID = 1184
INTERVAL_OID = 1186
TIMETZ_OID = 1266
NUMERIC_OID = 1700
UUID_OID = 2950
JSONB_OID = 3802

BOOL_ARRAY_OID = 1000
BYTEA_ARRAY_OID = 1001
NAME_ARRAY_OID = 1003
INT8_ARRAY_OID = 1016
INT2_ARRAY_OID = 1005
INT4_ARRAY_OID = 1007
TEXT_ARRAY_OID = 1009
OID_ARRAY_OID = 1028
JSON_ARRAY_OID = 199
FLOAT4_ARRAY_OID = 1021
FLOAT8_ARRAY_OID = 1022
BPCHAR_ARRAY_OID = 1014
VARCHAR_ARRAY_OID = 1015
DATE_ARRAY_OID = 1182
TIME_ARRAY_OID = 1183
TIMESTAMP_ARRAY_OID = 1115
TIMESTAMPTZ_ARRAY_OID = 1185
INTERVAL_ARRAY_OID = 1187
TIMETZ_ARRAY_OID = 1270
NUMERIC_ARRAY_OID = 1231
UUID_ARRAY_OID = 2951
JSONB_ARRAY_OID = 3807

# (pg_type.typname, pg_type.oid, pg_type.typarray) of the core set, ordered by oid.
BUILTIN_TYPES: tuple[tuple[str, int, int], ...] = (
    ("bool", BOOL_OID, BOOL_ARRAY_OID),
    ("bytea", BYTEA_OID, BYTEA_ARRAY_OID),
    ("name", NAME_OID, NAME_ARRAY_OID),
    ("int8", INT8_OID, INT8_ARRAY_OID),
    ("int2", INT2_OID, INT2_ARRAY_OID),
    ("int4", INT4_OID, INT4_ARRAY_OID),
    ("text", TEXT_OID, TEXT_ARRAY_OID),
    ("oid", OID_OID, OID_ARRAY_OID),
    ("json", JSON_OID, JSON_ARRAY_OID),
    ("float4", FLOAT4_OID, FLOAT4_ARRAY_OID),
    ("float8", FLOAT8_OID, FLOAT8_ARRAY_OID),
    ("bpchar", BPCHAR_OID, BPCHAR_ARRAY_OID),
    ("varchar", VARCHAR_OID, VARCHAR_ARRAY_OID),
    ("date", DATE_OID, DATE_ARRAY_OID),
    ("time", TIME_OID, TIME_ARRAY_OID),
    ("timestamp", TIMESTAMP_OID, TIMESTAMP_ARRAY_OID),
    ("timestamptz", TIMESTAMPTZ_OID, TIMESTAMPTZ_ARRAY_OID),
    ("interval", INTERVAL_OID, INTERVAL_ARRAY_OID),
    ("timetz", TIMETZ_OID, TIMETZ_ARRAY_OID),
    ("numeric", NUMERIC_OID, NUMERIC_ARRAY_OID),
    ("uuid", UUID_OID, UUID_ARRAY_OID),
    ("jsonb", JSONB_OID, JSONB_ARRAY_OID),
)
```

The catalog check is a database test of its own part (CPYTHON-ADAPT-02B-OIDS-CATALOG),
so that the constants themselves need no database part.

## 5. Dump table

What `Transformer.dump_sequence` produces for every Python type of the first wave, and
what PostgreSQL 18.6 answered when exactly that text and OID were sent as the only
parameter of `select $1` and of `select pg_typeof($1)::text` **(measured, experiment 2;
the stuntgres server answered every row identically, experiment 3)**. "error 42P18" is
`could not determine data type of parameter $1`: an untyped parameter needs a context.

### 5.1 The table

| Python type | Value | Text sent | OID | PostgreSQL 18.6: `select $1` returns | type of `$1` |
|---|---|---|---|---|---|
| `None` | `None` | NULL (no value) | 0 | NULL (OID 25) | error 42P18 |
| `bool` | `True` | `b't'` | 16 | `b't'` (OID 16) | boolean |
|  | `False` | `b'f'` | 16 | `b'f'` (OID 16) | boolean |
| `int` | `1` | `b'1'` | 23 | `b'1'` (OID 23) | integer |
|  | `-1` | `b'-1'` | 23 | `b'-1'` (OID 23) | integer |
|  | `2147483647` | `b'2147483647'` | 23 | `b'2147483647'` (OID 23) | integer |
|  | `-2147483648` | `b'-2147483648'` | 23 | `b'-2147483648'` (OID 23) | integer |
|  | `2147483648` | `b'2147483648'` | 20 | `b'2147483648'` (OID 20) | bigint |
|  | `9223372036854775807` | `b'9223372036854775807'` | 20 | `b'9223372036854775807'` (OID 20) | bigint |
|  | `-9223372036854775808` | `b'-9223372036854775808'` | 20 | `b'-9223372036854775808'` (OID 20) | bigint |
|  | `9223372036854775808` | `b'9223372036854775808'` | 1700 | `b'9223372036854775808'` (OID 1700) | numeric |
| `float` | `1.5` | `b'1.5'` | 701 | `b'1.5'` (OID 701) | double precision |
|  | `0.1` | `b'0.1'` | 701 | `b'0.1'` (OID 701) | double precision |
|  | `-0.0` | `b'-0.0'` | 701 | `b'-0'` (OID 701) | double precision |
|  | `1e+16` | `b'1e+16'` | 701 | `b'1e+16'` (OID 701) | double precision |
|  | `1e-05` | `b'1e-05'` | 701 | `b'1e-05'` (OID 701) | double precision |
|  | `5e-324` | `b'5e-324'` | 701 | `b'5e-324'` (OID 701) | double precision |
|  | `nan` | `b'NaN'` | 701 | `b'NaN'` (OID 701) | double precision |
|  | `inf` | `b'Infinity'` | 701 | `b'Infinity'` (OID 701) | double precision |
|  | `-inf` | `b'-Infinity'` | 701 | `b'-Infinity'` (OID 701) | double precision |
| `decimal.Decimal` | `Decimal('1.50')` | `b'1.50'` | 1700 | `b'1.50'` (OID 1700) | numeric |
|  | `Decimal('12345678901234567890.123456789')` | `b'12345678901234567890.123456789'` | 1700 | `b'12345678901234567890.123456789'` (OID 1700) | numeric |
|  | `Decimal('1E+2')` | `b'1E+2'` | 1700 | `b'100'` (OID 1700) | numeric |
|  | `Decimal('0E-7')` | `b'0E-7'` | 1700 | `b'0.0000000'` (OID 1700) | numeric |
|  | `Decimal('-0')` | `b'-0'` | 1700 | `b'0'` (OID 1700) | numeric |
|  | `Decimal('NaN')` | `b'NaN'` | 1700 | `b'NaN'` (OID 1700) | numeric |
|  | `Decimal('sNaN')` | `b'NaN'` | 1700 | `b'NaN'` (OID 1700) | numeric |
|  | `Decimal('Infinity')` | `b'Infinity'` | 1700 | `b'Infinity'` (OID 1700) | numeric |
|  | `Decimal('-Infinity')` | `b'-Infinity'` | 1700 | `b'-Infinity'` (OID 1700) | numeric |
| `str` | `'abc'` | `b'abc'` | 0 | `b'abc'` (OID 25) | error 42P18 |
|  | `''` | `b''` | 0 | `b''` (OID 25) | error 42P18 |
|  | `'ć 😀'` | `b'\xc4\x87 \xf0\x9f\x98\x80'` | 0 | `b'\xc4\x87 \xf0\x9f\x98\x80'` (OID 25) | error 42P18 |
|  | `'it\'s "q" \\ back'` | `b'it\'s "q" \\ back'` | 0 | `b'it\'s "q" \\ back'` (OID 25) | error 42P18 |
| `bytes` | `b'\x00\xff'` | `b'\\x00ff'` | 17 | `b'\\x00ff'` (OID 17) | bytea |
|  | `b''` | `b'\\x'` | 17 | `b'\\x'` (OID 17) | bytea |
| `bytearray` | `bytearray(b"ab\\c\'d")` | `b'\\x61625c632764'` | 17 | `b'\\x61625c632764'` (OID 17) | bytea |
| `memoryview` | `memoryview(b'\x01\x02')` | `b'\\x0102'` | 17 | `b'\\x0102'` (OID 17) | bytea |
| `datetime.date` | `date(2000, 1, 2)` | `b'2000-01-02'` | 1082 | `b'2000-01-02'` (OID 1082) | date |
|  | `date(1, 1, 1)` | `b'0001-01-01'` | 1082 | `b'0001-01-01'` (OID 1082) | date |
|  | `date(9999, 12, 31)` | `b'9999-12-31'` | 1082 | `b'9999-12-31'` (OID 1082) | date |
| `datetime.time`, naive | `time(3, 4, 5, 6)` | `b'03:04:05.000006'` | 1083 | `b'03:04:05.000006'` (OID 1083) | time without time zone |
|  | `time(3, 4, 5)` | `b'03:04:05'` | 1083 | `b'03:04:05'` (OID 1083) | time without time zone |
|  | `time(23, 59, 59, 999999)` | `b'23:59:59.999999'` | 1083 | `b'23:59:59.999999'` (OID 1083) | time without time zone |
| `datetime.time`, aware | `time(3, 4, 5, 6, tzinfo=timezone(timedelta(seconds=19800)))` | `b'03:04:05.000006+05:30'` | 1266 | `b'03:04:05.000006+05:30'` (OID 1266) | time with time zone |
|  | `time(3, 4, 5, tzinfo=timezone(timedelta(days=-1, seconds=57600)))` | `b'03:04:05-08:00'` | 1266 | `b'03:04:05-08'` (OID 1266) | time with time zone |
|  | `time(3, 4, 5, tzinfo=timezone.utc)` | `b'03:04:05+00:00'` | 1266 | `b'03:04:05+00'` (OID 1266) | time with time zone |
|  | `time(3, 4, 5, tzinfo=timezone(timedelta(seconds=3723)))` | `b'03:04:05+01:02:03'` | 1266 | `b'03:04:05+01:02:03'` (OID 1266) | time with time zone |
| `datetime.datetime`, naive | `datetime(2000, 1, 2, 3, 4, 5, 6)` | `b'2000-01-02 03:04:05.000006'` | 1114 | `b'2000-01-02 03:04:05.000006'` (OID 1114) | timestamp without time zone |
|  | `datetime(2000, 1, 2, 3, 4, 5)` | `b'2000-01-02 03:04:05'` | 1114 | `b'2000-01-02 03:04:05'` (OID 1114) | timestamp without time zone |
|  | `datetime(1, 1, 1, 0, 0)` | `b'0001-01-01 00:00:00'` | 1114 | `b'0001-01-01 00:00:00'` (OID 1114) | timestamp without time zone |
|  | `datetime(99, 7, 4, 1, 2, 3)` | `b'0099-07-04 01:02:03'` | 1114 | `b'0099-07-04 01:02:03'` (OID 1114) | timestamp without time zone |
|  | `datetime(9999, 12, 31, 23, 59, 59, 999999)` | `b'9999-12-31 23:59:59.999999'` | 1114 | `b'9999-12-31 23:59:59.999999'` (OID 1114) | timestamp without time zone |
| `datetime.datetime`, aware | `datetime(2000, 1, 2, 3, 4, 5, 6, tzinfo=timezone(timedelta(seconds=19800)))` | `b'2000-01-02 03:04:05.000006+05:30'` | 1184 | `b'2000-01-01 21:34:05.000006+00'` (OID 1184) | timestamp with time zone |
|  | `datetime(2000, 1, 2, 3, 4, 5, tzinfo=timezone.utc)` | `b'2000-01-02 03:04:05+00:00'` | 1184 | `b'2000-01-02 03:04:05+00'` (OID 1184) | timestamp with time zone |
|  | `datetime(2000, 1, 2, 3, 4, 5, tzinfo=timezone(timedelta(seconds=3723)))` | `b'2000-01-02 03:04:05+01:02:03'` | 1184 | `b'2000-01-02 02:02:02+00'` (OID 1184) | timestamp with time zone |
| `datetime.timedelta` | `timedelta(days=1, seconds=2, microseconds=3)` | `b'+1 days +2 seconds +3 microseconds'` | 1186 | `b'1 day 00:00:02.000003'` (OID 1186) | interval |
|  | `timedelta(0)` | `b'+0 days +0 seconds +0 microseconds'` | 1186 | `b'00:00:00'` (OID 1186) | interval |
|  | `timedelta(days=-1, seconds=86399, microseconds=999999)` | `b'-1 days +86399 seconds +999999 microseconds'` | 1186 | `b'-1 days +23:59:59.999999'` (OID 1186) | interval |
|  | `timedelta(days=-1, seconds=5)` | `b'-1 days +5 seconds +0 microseconds'` | 1186 | `b'-1 days +00:00:05'` (OID 1186) | interval |
|  | `timedelta(days=999999999, seconds=86399, microseconds=999999)` | `b'+999999999 days +86399 seconds +999999 microseconds'` | 1186 | `b'999999999 days 23:59:59.999999'` (OID 1186) | interval |
|  | `timedelta(days=-999999999)` | `b'-999999999 days +0 seconds +0 microseconds'` | 1186 | `b'-999999999 days'` (OID 1186) | interval |
| `uuid.UUID` | `UUID('a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11')` | `b'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11'` | 2950 | `b'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11'` (OID 2950) | uuid |
| `Json` | `Json({'a': [1, None, 'x'], 'b': {'c': 1.5}, 'u': 'ć😀'})` | `b'{"a": [1, null, "x"], "b": {"c": 1.5}, "u": "\\u0107\\ud83d\\ude00"}'` | 114 | `b'{"a": [1, null, "x"], "b": {"c": 1.5}, "u": "\\u0107\\ud83d\\ude00"}'` (OID 114) | json |
|  | `Json([1, 'a', None, True])` | `b'[1, "a", null, true]'` | 114 | `b'[1, "a", null, true]'` (OID 114) | json |
| `Jsonb` | `Jsonb({'a': [1, None, 'x'], 'b': {'c': 1.5}, 'u': 'ć😀'})` | `b'{"a": [1, null, "x"], "b": {"c": 1.5}, "u": "\\u0107\\ud83d\\ude00"}'` | 3802 | `b'{"a": [1, null, "x"], "b": {"c": 1.5}, "u": "\xc4\x87\xf0\x9f\x98\x80"}'` (OID 3802) | jsonb |
|  | `Jsonb(None)` | `b'null'` | 3802 | `b'null'` (OID 3802) | jsonb |
|  | `Jsonb(5)` | `b'5'` | 3802 | `b'5'` (OID 3802) | jsonb |
| `list` of `int` | `[1, 2, 3]` | `b'{"1","2","3"}'` | 1007 | `b'{1,2,3}'` (OID 1007) | integer[] |
|  | `[1, None, 3]` | `b'{"1",NULL,"3"}'` | 1007 | `b'{1,NULL,3}'` (OID 1007) | integer[] |
|  | `[[1, 2], [3, 4]]` | `b'{{"1","2"},{"3","4"}}'` | 1007 | `b'{{1,2},{3,4}}'` (OID 1007) | integer[] |
|  | `[40000, 1]` | `b'{"40000","1"}'` | 1007 | `b'{40000,1}'` (OID 1007) | integer[] |
|  | `[1, 1099511627776]` | `b'{"1","1099511627776"}'` | 1016 | `b'{1,1099511627776}'` (OID 1016) | bigint[] |
|  | `[1, 1180591620717411303424]` | `b'{"1","1180591620717411303424"}'` | 1231 | `b'{1,1180591620717411303424}'` (OID 1231) | numeric[] |
| `list`, empty or only `None` | `[]` | `b'{}'` | 0 | `b'{}'` (OID 25) | error 42P18 |
|  | `[None]` | `b'{NULL}'` | 0 | `b'{NULL}'` (OID 25) | error 42P18 |
|  | `[[]]` | `b'{{}}'` | 0 | `b'{{}}'` (OID 25) | error 42P18 |
| `list` of `str` | `['a', 'b c', '', 'NULL', 'q"q', 'b\\s', '{x}', 'a,b', None, 'ć😀']` | `b'{"a","b c","","NULL","q\\"q","b\\\\s","{x}","a,b",NULL,"\xc4\x87\xf0\x9f\x98\x80"}'` | 0 | `b'{"a","b c","","NULL","q\\"q","b\\\\s","{x}","a,b",NULL,"\xc4\x87\xf0\x9f\x98\x80"}'` (OID 25) | error 42P18 |
|  | `['1', '2']` | `b'{"1","2"}'` | 0 | `b'{"1","2"}'` (OID 25) | error 42P18 |
| `list` of other types | `[1.5, None, nan, inf]` | `b'{"1.5",NULL,"NaN","Infinity"}'` | 1022 | `b'{1.5,NULL,NaN,Infinity}'` (OID 1022) | double precision[] |
|  | `[Decimal('1.50'), Decimal('NaN'), None]` | `b'{"1.50","NaN",NULL}'` | 1231 | `b'{1.50,NaN,NULL}'` (OID 1231) | numeric[] |
|  | `[True, False, None]` | `b'{"t","f",NULL}'` | 1000 | `b'{t,f,NULL}'` (OID 1000) | boolean[] |
|  | `[b'\x00\xff', b'', b'a\\b"c', None]` | `b'{"\\\\x00ff","\\\\x","\\\\x615c622263",NULL}'` | 1001 | `b'{"\\\\x00ff","\\\\x","\\\\x615c622263",NULL}'` (OID 1001) | bytea[] |
|  | `[date(2000, 1, 2), None]` | `b'{"2000-01-02",NULL}'` | 1182 | `b'{2000-01-02,NULL}'` (OID 1182) | date[] |
|  | `[time(3, 4, 5)]` | `b'{"03:04:05"}'` | 1183 | `b'{03:04:05}'` (OID 1183) | time without time zone[] |
|  | `[time(3, 4, 5, tzinfo=timezone(timedelta(seconds=19800)))]` | `b'{"03:04:05+05:30"}'` | 1270 | `b'{03:04:05+05:30}'` (OID 1270) | time with time zone[] |
|  | `[datetime(2000, 1, 2, 3, 4, 5, 6)]` | `b'{"2000-01-02 03:04:05.000006"}'` | 1115 | `b'{"2000-01-02 03:04:05.000006"}'` (OID 1115) | timestamp without time zone[] |
|  | `[datetime(2000, 1, 2, 3, 4, 5, 6, tzinfo=timezone(timedelta(seconds=19800)))]` | `b'{"2000-01-02 03:04:05.000006+05:30"}'` | 1185 | `b'{"2000-01-01 21:34:05.000006+00"}'` (OID 1185) | timestamp with time zone[] |
|  | `[timedelta(days=1, seconds=2)]` | `b'{"+1 days +2 seconds +0 microseconds"}'` | 1187 | `b'{"1 day 00:00:02"}'` (OID 1187) | interval[] |
|  | `[UUID('a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11'), None]` | `b'{"a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11",NULL}'` | 2951 | `b'{a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11,NULL}'` (OID 2951) | uuid[] |
|  | `[Json({'a': 'x "y"'}), Json([1, None])]` | `b'{"{\\"a\\": \\"x \\\\\\"y\\\\\\"\\"}","[1, null]"}'` | 199 | `b'{"{\\"a\\": \\"x \\\\\\"y\\\\\\"\\"}","[1, null]"}'` (OID 199) | json[] |
|  | `[Jsonb({'a': 1}), Jsonb(None), None]` | `b'{"{\\"a\\": 1}","null",NULL}'` | 3807 | `b'{"{\\"a\\": 1}","null",NULL}'` (OID 3807) | jsonb[] |

Not adaptable in this wave (plan U10): `dict` and any JSON value without a wrapper,
`tuple`, `set`, `range`, `enum.Enum` without an `int` base, `ipaddress` objects. They
raise `ProgrammingError("cannot adapt type 'dict' as a query parameter")` (the class's
`__qualname__`) before anything is sent. A subclass of a listed class is dumped like its
base (`enum.IntEnum` as `int`).

Errors raised while dumping:

| Value | Error |
|---|---|
| a `str` containing `"\x00"` | `DataError("PostgreSQL text cannot contain NUL (0x00) characters")` |
| a `str` the client encoding cannot encode | the `UnicodeEncodeError` of `str.encode` |
| a `datetime.time` whose `tzinfo` is set but whose `utcoffset()` is `None` (for example a `zoneinfo.ZoneInfo`, whose offset depends on the date) | `DataError(f"cannot dump the time {obj!r}: its time zone has no fixed UTC offset")` |
| a list whose non-`None` elements dump with different OIDs, other than the integer OIDs 21, 23, 20, 1700 among themselves | `DataError("cannot dump a list whose elements have different types (type OIDs 0, 23)")` (the OIDs sorted ascending, joined by `, `) |

A ragged nested list (`[[1, 2], [3]]`) is not checked by the binding; the server answers
`22P02 malformed array literal: "{{"1","2"},{"3"}}"` **(measured)**.

### 5.2 Decisions and their evidence

For each open choice experiment 2 sent the same text with the candidate OIDs through
`select $1`, a set of expressions and `insert into t values ($1) returning c` followed by
`select ... where c = $1` for several column types. Complete output:
`.cswd/tmp/CPYTHON-DESIGN-ADAPT/exp2/oracle.txt` (1494 lines; `first-run/` holds the run
with psycopg's integer rule).

| Python type | Decision | Evidence (PostgreSQL 18.6) |
|---|---|---|
| `None` | value NULL, OID 0 | `select $1` gives NULL of type text; `insert` into `int4` and `text` columns works; `coalesce($1, 5)` works. `select $1 is null` fails with 42P18 (write `%s::text is null`). No single OID would allow the inserts. |
| `bool` | OID 16 | `not $1`, `$1 and true`, `case when $1` work. |
| `int` | OID 23 for -2^31..2^31-1, OID 20 for -2^63..2^63-1, else OID 1700 | Of 38 statements with the value 1: OID 23 fails 2 (one is a syntax error for every OID, the other `text_column = $1`), unknown fails 5 (`$1 + $2` is ambiguous), `int8` fails 7 (`repeat('ab', $1)`, `substr`, `round(numeric, $1)`, `date + $1`, `chr`), `numeric` fails 10. psycopg's rule (OID 21 for small values) fails `select $1 + $2` with 32767 and 32767 (`22003 smallint out of range`) and makes `[1, 2, 3]` an `int2[]`, for which `$1 @> array[1]`, `$1 = array[1,2,3]` and `integer_array_column = $1` fail with `42883 operator does not exist`. With the rule chosen here all of these work; the remaining failures are those of SQL literals too (`2147483647 + 1`: `22003 integer out of range`). A small `int` goes into `int2`, `int8`, `numeric`, `float8`, `text` and `oid` columns by assignment cast; a value too large for the column fails with `22003`. |
| `float` | OID 701; `NaN`, `Infinity`, `-Infinity` spelled out; otherwise `float.__repr__` | `$1 * 2`, `$1 + 1.5`, `round($1::numeric, 1)` work; inserts into `float4`, `float8`, `numeric`, `text`, `int4` columns work. Unknown fails 6 of 19. |
| `decimal.Decimal` | OID 1700; any NaN as `NaN` | Exact round trip of `12345678901234567890.123456789`; `1E+2` is read as `100`, `0E-7` as `0.0000000`, `-0` as `0`. Unknown fails 5 of 17. |
| `str` | OID 0 | Unknown lets one string feed any column type: inserts into `text`, `varchar`, `char`, `name`, `bytea`, `int4`, `numeric`, `date`, `timestamp`, `json`, `jsonb`, `uuid`, `bool` columns all work; with OID 25 the same eleven statements for the string `5` fail 8 times (`42804 column "c" is of type integer but expression is of type text`). The price: `select $1` has type text but `pg_typeof($1)`, `concat($1, 1)` and `format('%s!', $1)` fail with 42P18. `StrTextDumper` (6.3) is the opt-in for OID 25. |
| `bytes`, `bytearray`, `memoryview` | OID 17, hex format | All 256 byte values round-trip; `length`, `encode`, `\|\|` work. |
| `datetime.date` | OID 1082, `YYYY-MM-DD` | `$1 + 1`, `$1 - date`, `extract`, comparison with `now()`; inserts into `date`, `timestamp`, `timestamptz`, `text` columns. Read the same in every DateStyle (A14). |
| `datetime.time` | OID 1083 when `utcoffset()` is `None`, else OID 1266 | Both insert into `time` and `timetz` columns. |
| `datetime.datetime` | OID 1114 when `utcoffset()` is `None`, else OID 1184 | A naive value sent as `timestamptz` would be shifted by the session's zone when it meets a `timestamp`; sent as `timestamp` it is exact for `timestamp` columns and is interpreted in the session's zone for `timestamptz` columns, which is PostgreSQL's own rule for a literal. Both fail 2 of 21 (the same two). |
| `datetime.timedelta` | OID 1186, `"%+d days %+d seconds %+d microseconds"` | With explicit signs the text means the same in the four IntervalStyles (36 of 36 comparisons true); `str(timedelta)` is read wrongly in `sql_standard` (`-1 day, 23:59:59.999999` becomes -172799.999999 s). |
| `uuid.UUID` | OID 2950, hyphenated | Inserts into `uuid` and `text` columns work. |
| `Json`, `Jsonb` | OID 114, OID 3802 | `->`, `->>`, `@>`, `?` work with the typed values and are ambiguous (`42725 operator is not unique`) with unknown. A `Json` value inserts into a `jsonb` column and the reverse. |
| `list` | every element quoted, `None` as `NULL`; the array OID of the element OID; OID 0 when there is no element OID (empty, only `None`, `str` elements) or no known array type | Typed arrays work with `= any($1)`, `array_length`, `unnest`, subscripts, `\|\|`. Integer lists follow the widest element: `[1, 2**40]` is `int8[]`, `[1, 2**70]` is `numeric[]`. A list of `str` with OID 1009 could not be inserted into an `int4[]` column and could not be compared with a `varchar[]` column; with OID 0 it can, but `array_length($1, 1)`, `cardinality($1)` and `$1[1]` need a cast (`%s::text[]`). `select $1` of an untyped list returns the text `{"a","b"}`. |

### 5.3 What `%b` and `%t` do

Section 2.2: `%t` is `%s`; `%b` raises `NotSupportedError`. When binary format arrives,
`%b` selects it, `%t` keeps text and `%s` becomes "the adapter's choice" as in psycopg 3,
without changing a query that works today.

## 6. Loaders and the type modules

### 6.1 Conventions

- Every type module defines its dumpers, its loaders and
  `register_default_adapters(adapters: AdaptersMap) -> None`, which registers them in the
  map it is given. `stuntgres/_defaults.py` (6.11) calls these functions for the global
  map; a unit test calls the one of its module on a map of its own.
- "Text sent by PostgreSQL 18.6" in the tables is the value of
  `PQgetvalue` recorded in experiment 1 with the session defaults of the pinned image
  (`DateStyle` `ISO, MDY`, `IntervalStyle` `postgres`, `bytea_output` `hex`,
  `extra_float_digits` 1, `client_encoding` `UTF8`) unless a row names another setting.
  The stuntgres server sent the same bytes for every row of the core set **(measured:
  `diff exp1/oracle.txt exp1/server.txt`)**.
- A loader is given the bytes of a non-NULL value. Text of the types below is ASCII
  except for the text family, json and arrays of them, which are decoded with the client
  encoding.
- Errors of loaders. Two messages, both `stuntgres.errors.DataError`, with `{kind}` the
  PostgreSQL type name (`date`, `time`, `timetz`, `timestamp`, `timestamptz`,
  `interval`) and `{text}` the value decoded as ASCII with `replace`:
  1. out of range: `f"{kind} value {text!r} is out of the range Python can represent"`;
  2. not parsable: `f"cannot parse {kind} value {text!r}"`, followed, when the session's
     `DateStyle` (for `date`, `timestamp`, `timestamptz`) is known and is neither `ISO`
     nor starts with `ISO,`, by
     `f": the session's DateStyle is {style!r}, only ISO is supported"`; for `interval`,
     when `IntervalStyle` is known and is not `postgres`, by
     `f": the session's IntervalStyle is {style!r}, only postgres is supported"`.
  The style is read with `context_parameter` only when the error is built. `time` and
  `timetz` do not depend on `DateStyle` **(measured)** and never add the suffix.
- The loaders of bool, the numbers, uuid and json do no validation of their own: text
  that PostgreSQL cannot send makes the Python constructor raise (`ValueError`,
  `decimal.InvalidOperation`, `json.JSONDecodeError`).

### 6.2 `stuntgres/types/numeric.py`

| Class | Registered for | Algorithm |
|---|---|---|
| `BoolDumper` (`oid` 16) | `bool` | `b"t"` if the object is true, else `b"f"`. |
| `IntDumper` (`oid` 23) | `int` | `dump`: `b"%d" % obj` (digits also for `int` subclasses). `dump_typed`: OID 23 when `-2**31 <= obj <= 2**31 - 1`, else OID 20 when `-2**63 <= obj <= 2**63 - 1`, else OID 1700. |
| `FloatDumper` (`oid` 701) | `float` | `b"NaN"` when `obj != obj`; `b"Infinity"` and `b"-Infinity"` for the infinities; else `float.__repr__(obj).encode("ascii")` (the shortest text that reads back as the same double; PostgreSQL reads `1e+16`, `1e-05`, `-0.0`). |
| `DecimalDumper` (`oid` 1700) | `decimal.Decimal` | `b"NaN"` when `obj.is_nan()` (quiet, signaling or signed); else `str(obj).encode("ascii")` (`Infinity`, `-Infinity`, `1E+2`, `0E-7` are valid numeric input). |
| `BoolLoader` | OID 16 | `data == b"t"`. |
| `IntLoader` | OIDs 21, 23, 20, 26 | `int(data)`. |
| `FloatLoader` | OIDs 700, 701 | `float(data)`: Python reads `NaN`, `Infinity`, `-Infinity`, `-0` and the exponent forms. A `float4` becomes the nearest double of its shortest text (`1.5`, `3.4028235e+38`). |
| `NumericLoader` | OID 1700 | `decimal.Decimal(data.decode("ascii"))`: exact, the scale is kept (`1.50` stays `Decimal('1.50')`), `NaN` and the infinities are understood. |

| Type (OID) | Text sent by PostgreSQL 18.6 | Python result |
|---|---|---|
| bool (16) | `b't'` | `True` |
| bool (16) | `b'f'` | `False` |
| int2 (21) | `b'-32768'` | `-32768` |
| int2 (21) | `b'32767'` | `32767` |
| int4 (23) | `b'-2147483648'` | `-2147483648` |
| int4 (23) | `b'2147483647'` | `2147483647` |
| int8 (20) | `b'-9223372036854775808'` | `-9223372036854775808` |
| int8 (20) | `b'9223372036854775807'` | `9223372036854775807` |
| oid (26) | `b'0'` | `0` |
| oid (26) | `b'4294967295'` | `4294967295` |
| float4 (700) | `b'0'` | `0.0` |
| float4 (700) | `b'-0'` | `-0.0` |
| float4 (700) | `b'1.5'` | `1.5` |
| float4 (700) | `b'3.4028235e+38'` | `3.4028235e+38` |
| float4 (700) | `b'1e-45'` | `1e-45` |
| float4 (700) | `b'1.6777216e+07'` | `16777216.0` |
| float4 (700) | `b'NaN'` | `nan` |
| float4 (700) | `b'Infinity'` | `inf` |
| float4 (700) | `b'-Infinity'` | `-inf` |
| float8 (701) | `b'0'` | `0.0` |
| float8 (701) | `b'-0'` | `-0.0` |
| float8 (701) | `b'0.1'` | `0.1` |
| float8 (701) | `b'1.7976931348623157e+308'` | `1.7976931348623157e+308` |
| float8 (701) | `b'5e-324'` | `5e-324` |
| float8 (701) | `b'1e+15'` | `1000000000000000.0` |
| float8 (701) | `b'1e-05'` | `1e-05` |
| float8 (701) | `b'0.3333333333333333'` | `0.3333333333333333` |
| float8 (701) | `b'1.2345678901234568e+17'` | `1.2345678901234568e+17` |
| float8 (701) | `b'NaN'` | `nan` |
| float8 (701) | `b'Infinity'` | `inf` |
| float8 (701) | `b'-Infinity'` | `-inf` |
| numeric (1700) | `b'0'` | `Decimal('0')` |
| numeric (1700) | `b'0.00'` | `Decimal('0.00')` |
| numeric (1700) | `b'-1'` | `Decimal('-1')` |
| numeric (1700) | `b'1.50'` | `Decimal('1.50')` |
| numeric (1700) | `b'-123.4500'` | `Decimal('-123.4500')` |
| numeric (1700) | `b'9007199254740993'` | `Decimal('9007199254740993')` |
| numeric (1700) | `b'12345678901234567890.123456789'` | `Decimal('12345678901234567890.123456789')` |
| numeric (1700) | `b'0.00000000000000000001'` | `Decimal('1E-20')` |
| numeric (1700) | `b'100000000000000000000'` | `Decimal('100000000000000000000')` |
| numeric (1700) | `b'NaN'` | `Decimal('NaN')` |
| numeric (1700) | `b'Infinity'` | `Decimal('Infinity')` |
| numeric (1700) | `b'-Infinity'` | `Decimal('-Infinity')` |

The complete file:

```python
"""Adapters of bool, the integer types, the float types and numeric."""
from __future__ import annotations

from decimal import Decimal
from typing import Any

from .. import _oids
from ..adapt import AdaptersMap, Dumper, Loader

_INT4_MIN, _INT4_MAX = -(2**31), 2**31 - 1
_INT8_MIN, _INT8_MAX = -(2**63), 2**63 - 1


class BoolDumper(Dumper):
    """bool -> bool."""

    oid = _oids.BOOL_OID

    def dump(self, obj: Any) -> bytes:
        """Return b"t" or b"f"."""
        return b"t" if obj else b"f"


class IntDumper(Dumper):
    """int -> int4, int8 or numeric: the type PostgreSQL gives the same literal."""

    oid = _oids.INT4_OID

    def dump(self, obj: Any) -> bytes:
        """Return the decimal digits."""
        return b"%d" % obj

    def dump_typed(self, obj: Any) -> tuple[bytes, int]:
        """Return the digits and the OID chosen by magnitude."""
        if _INT4_MIN <= obj <= _INT4_MAX:
            oid = _oids.INT4_OID
        elif _INT8_MIN <= obj <= _INT8_MAX:
            oid = _oids.INT8_OID
        else:
            oid = _oids.NUMERIC_OID
        return b"%d" % obj, oid


class FloatDumper(Dumper):
    """float -> float8."""

    oid = _oids.FLOAT8_OID

    def dump(self, obj: Any) -> bytes:
        """Return the shortest text that reads back as the same float8."""
        if obj != obj:
            return b"NaN"
        if obj == float("inf"):
            return b"Infinity"
        if obj == float("-inf"):
            return b"-Infinity"
        return float.__repr__(obj).encode("ascii")


class DecimalDumper(Dumper):
    """decimal.Decimal -> numeric."""

    oid = _oids.NUMERIC_OID

    def dump(self, obj: Any) -> bytes:
        """Return the exact text of the number; any NaN becomes NaN."""
        if obj.is_nan():
            return b"NaN"
        return str(obj).encode("ascii")


class BoolLoader(Loader):
    """bool -> bool."""

    def load(self, data: bytes) -> bool:
        """Return True for b"t"."""
        return data == b"t"


class IntLoader(Loader):
    """int2, int4, int8, oid -> int."""

    def load(self, data: bytes) -> int:
        """Return the integer."""
        return int(data)


class FloatLoader(Loader):
    """float4, float8 -> float."""

    def load(self, data: bytes) -> float:
        """Return the float; NaN, Infinity and -Infinity are understood."""
        return float(data)


class NumericLoader(Loader):
    """numeric -> decimal.Decimal."""

    def load(self, data: bytes) -> Decimal:
        """Return the exact number; NaN, Infinity and -Infinity are understood."""
        return Decimal(data.decode("ascii"))


def register_default_adapters(adapters: AdaptersMap) -> None:
    """Register the adapters of this module in a map."""
    adapters.register_dumper(bool, BoolDumper)
    adapters.register_dumper(int, IntDumper)
    adapters.register_dumper(float, FloatDumper)
    adapters.register_dumper(Decimal, DecimalDumper)
    adapters.register_loader(_oids.BOOL_OID, BoolLoader)
    for oid in (_oids.INT2_OID, _oids.INT4_OID, _oids.INT8_OID, _oids.OID_OID):
        adapters.register_loader(oid, IntLoader)
    adapters.register_loader(_oids.FLOAT4_OID, FloatLoader)
    adapters.register_loader(_oids.FLOAT8_OID, FloatLoader)
    adapters.register_loader(_oids.NUMERIC_OID, NumericLoader)
```

### 6.3 `stuntgres/types/string.py`

| Class | Registered for | Algorithm |
|---|---|---|
| `StrDumper` (`oid` 0) | `str` | `__init__` stores `context_encoding(context)`. `dump`: `data = obj.encode(encoding)`; a NUL byte in `data` raises the `DataError` of 5.1; return `data`. |
| `StrTextDumper` (`oid` 25) | nothing (opt-in, 3.6 item 5) | Subclass of `StrDumper`; only the OID differs. |
| `BytesDumper` (`oid` 17) | `bytes`, `bytearray`, `memoryview` | `b"\\x" + binascii.hexlify(obj)`: the hex input format, accepted whatever `bytea_output` is. |
| `TextLoader` | OIDs 0 (the fallback), 25, 1043, 1042, 19, 705 | `__init__` stores the encoding. `load`: `data.decode(encoding)`. `bpchar` keeps its padding (`'ab   '`). An undecodable value raises `UnicodeDecodeError`. |
| `ByteaLoader` | OID 17 | If `data[:2] == b"\\x"` (hex output): `binascii.unhexlify(data[2:])`. Otherwise (escape output): replace every match of the bytes pattern `\\(\\|[0-7]{3})` by one byte: a backslash for `\\`, else the byte with that octal value; everything else is literal. Escape output never starts with `\x`, because a data backslash is printed as two **(measured: the bytes `5c 78` print as `\\x`)**. |

| Type (OID) | Text sent by PostgreSQL 18.6 | Python result |
|---|---|---|
| text (25) | `b'abc'` | `'abc'` |
| text (25) | `b''` | `''` |
| text (25) | `b'line1\nline2\ttab'` | `'line1\nline2\ttab'` |
| text (25) | `b'back\\slash "dq" \'sq\''` | `'back\\slash "dq" \'sq\''` |
| text (25) | `b'\xf0\x9f\x98\x80 \xc4\x87'` | `'😀 ć'` |
| varchar (1043) | `b'abc'` | `'abc'` |
| bpchar (1042) | `b'ab   '` | `'ab   '` |
| name (19) | `b'n'` | `'n'` |
| "char", no loader (18) | `b'x'` | `'x'` |
| inet, no loader (869) | `b'192.168.0.1/24'` | `'192.168.0.1/24'` |
| int4range, no loader (3904) | `b'[1,5)'` | `'[1,5)'` |
| point, no loader (600) | `b'(1,2)'` | `'(1,2)'` |
| record, no loader (2249) | `b'(1,a)'` | `'(1,a)'` |
| bytea, hex (17) | `b'\\x'` | `b''` |
| bytea, hex (17) | `b'\\x00ff'` | `b'\x00\xff'` |
| bytea, hex (17) | `b'\\xdeadbeef'` | `b'\xde\xad\xbe\xef'` |
| bytea, hex (17) | `b'\\x616263'` | `b'abc'` |
| bytea, escape (17) | `b''` | `b''` |
| bytea, escape (17) | `b'abc'` | `b'abc'` |
| bytea, escape (17) | `b'\\000\\377'` | `b'\x00\xff'` |
| bytea, escape (17) | `b"\\000\\001\\177\\200\\377\\\\'"` | `b"\x00\x01\x7f\x80\xff\\'"` |
| bytea, escape (17) | `b'\\\\\\\\'` | `b'\\\\'` |
| bytea, escape (17) | `b'\\\\x'` | `b'\\x'` |

All 256 byte values **(measured)**: hex output is `\x` followed by `000102...feff`;
escape output is `\000` to `\037` for 0 to 31, the printable ASCII characters as
themselves except `\\` for the backslash, and `\177` to `\377` for 127 to 255 (the test
file builds this literal and it was compared with the recorded output).

The complete file:

```python
"""Adapters of the text types (text, varchar, bpchar, name) and of bytea."""
from __future__ import annotations

import re
from binascii import hexlify, unhexlify
from typing import Any

from .. import _oids
from .. import errors as e
from ..adapt import AdaptContext, AdaptersMap, Dumper, Loader, context_encoding

_ESCAPED = re.compile(rb"\\(\\|[0-7]{3})")


def _unescape(match: re.Match[bytes]) -> bytes:
    code = match.group(1)
    return b"\\" if code == b"\\" else bytes((int(code, 8),))


class StrDumper(Dumper):
    """str -> text of unknown type: the server infers the type."""

    oid = _oids.INVALID_OID

    def __init__(self, cls: type, context: AdaptContext | None = None) -> None:
        super().__init__(cls, context)
        self._encoding = context_encoding(context)

    def dump(self, obj: Any) -> bytes:
        """Return the string in the client encoding."""
        data: bytes = obj.encode(self._encoding)
        if b"\x00" in data:
            raise e.DataError("PostgreSQL text cannot contain NUL (0x00) characters")
        return data


class StrTextDumper(StrDumper):
    """str -> text. Not registered by default; see the registry contract."""

    oid = _oids.TEXT_OID


class BytesDumper(Dumper):
    """bytes, bytearray, memoryview -> bytea."""

    oid = _oids.BYTEA_OID

    def dump(self, obj: Any) -> bytes:
        """Return the hex input format of bytea."""
        return b"\\x" + hexlify(obj)


class TextLoader(Loader):
    """text, varchar, bpchar, name and every type without a loader -> str."""

    def __init__(self, oid: int, context: AdaptContext | None = None) -> None:
        super().__init__(oid, context)
        self._encoding = context_encoding(context)

    def load(self, data: bytes) -> str:
        """Return the text decoded from the client encoding."""
        return data.decode(self._encoding)


class ByteaLoader(Loader):
    """bytea -> bytes, from the hex and from the escape output format."""

    def load(self, data: bytes) -> bytes:
        """Return the binary string."""
        if data[:2] == b"\\x":
            return unhexlify(data[2:])
        return _ESCAPED.sub(_unescape, data)


def register_default_adapters(adapters: AdaptersMap) -> None:
    """Register the adapters of this module in a map."""
    adapters.register_dumper(str, StrDumper)
    for cls in (bytes, bytearray, memoryview):
        adapters.register_dumper(cls, BytesDumper)
    for oid in (
        _oids.INVALID_OID,
        _oids.TEXT_OID,
        _oids.VARCHAR_OID,
        _oids.BPCHAR_OID,
        _oids.NAME_OID,
        _oids.UNKNOWN_OID,
    ):
        adapters.register_loader(oid, TextLoader)
    adapters.register_loader(_oids.BYTEA_OID, ByteaLoader)
```

### 6.4 `stuntgres/types/datetime.py` (date, time, timetz)

Grammars accepted (bytes regular expressions, anchored):

| Type | Pattern | Groups |
|---|---|---|
| date | `^(\d{4,})-(\d\d)-(\d\d)( BC)?$` | year, month, day, BC |
| time | `^(\d\d):(\d\d):(\d\d)(?:\.(\d{1,6}))?$` | hour, minute, second, fraction |
| timetz | `^(\d\d):(\d\d):(\d\d)(?:\.(\d{1,6}))?([-+])(\d\d)(?::(\d\d))?(?::(\d\d))?$` | the same, then sign, offset hours, minutes, seconds |

Microseconds are `int(fraction.ljust(6, b"0"))`, 0 without a fraction (PostgreSQL drops
trailing zeros: `.7` is 700000).

| Class | Registered for | Algorithm |
|---|---|---|
| `DateDumper` (`oid` 1082) | `datetime.date` | `obj.isoformat().encode("ascii")`: `YYYY-MM-DD` with a four-digit year. (`datetime.datetime` has a dumper of its own in 6.5, found first on the MRO.) |
| `TimeDumper` (`oid` 1083) | `datetime.time` | `dump_typed`: `data = obj.isoformat().encode("ascii")` (`HH:MM:SS`, `.ffffff` when microseconds are not zero, then `+HH:MM` or `+HH:MM:SS` when aware). If `obj.utcoffset()` is not `None`: OID 1266. Else if `obj.tzinfo` is not `None`: the `DataError` of 5.1. Else OID 1083. `dump` returns the text of `dump_typed`. |
| `DateLoader` | OID 1082 | No match: `infinity` and `-infinity` raise error 1, anything else error 2. A BC group raises error 1. `datetime.date(year, month, day)`; its `ValueError` (year above 9999) becomes error 1. |
| `TimeLoader` | OID 1083 | No match: error 2 (never with the DateStyle suffix). `datetime.time(hour, minute, second, microsecond)`; `ValueError` (hour 24) becomes error 1. The result is naive. |
| `TimetzLoader` | OID 1266 | As `TimeLoader`, with `tzinfo=datetime.timezone(offset)`, `offset = timedelta(hours, minutes, seconds)` of the three offset groups (missing ones are 0), negated for the sign `-`. |

| Type (OID) | Text sent by PostgreSQL 18.6 | Python result |
|---|---|---|
| date (1082) | `b'2000-01-01'` | `date(2000, 1, 1)` |
| date (1082) | `b'0001-01-01'` | `date(1, 1, 1)` |
| date (1082) | `b'0099-07-04'` | `date(99, 7, 4)` |
| date (1082) | `b'2024-02-29'` | `date(2024, 2, 29)` |
| date (1082) | `b'9999-12-31'` | `date(9999, 12, 31)` |
| date (1082) | `b'10000-01-01'` | raises `DataError`: date value '10000-01-01' is out of the range Python can represent |
| date (1082) | `b'5874897-12-31'` | raises `DataError`: date value '5874897-12-31' is out of the range Python can represent |
| date (1082) | `b'0001-12-31 BC'` | raises `DataError`: date value '0001-12-31 BC' is out of the range Python can represent |
| date (1082) | `b'4713-01-01 BC'` | raises `DataError`: date value '4713-01-01 BC' is out of the range Python can represent |
| date (1082) | `b'infinity'` | raises `DataError`: date value 'infinity' is out of the range Python can represent |
| date (1082) | `b'-infinity'` | raises `DataError`: date value '-infinity' is out of the range Python can represent |
| date, DateStyle SQL, MDY (1082) | `b'01/02/2000'` | raises `DataError`: cannot parse date value '01/02/2000': the session's DateStyle is 'SQL, MDY', only ISO is supported |
| date, DateStyle German (1082) | `b'02.01.2000'` | raises `DataError`: cannot parse date value '02.01.2000': the session's DateStyle is 'German, DMY', only ISO is supported |
| date, DateStyle Postgres, MDY (1082) | `b'01-02-2000'` | raises `DataError`: cannot parse date value '01-02-2000': the session's DateStyle is 'Postgres, MDY', only ISO is supported |
| time (1083) | `b'00:00:00'` | `time(0, 0)` |
| time (1083) | `b'23:59:59.999999'` | `time(23, 59, 59, 999999)` |
| time (1083) | `b'12:34:56.7'` | `time(12, 34, 56, 700000)` |
| time (1083) | `b'12:34:56.0001'` | `time(12, 34, 56, 100)` |
| time (1083) | `b'24:00:00'` | raises `DataError`: time value '24:00:00' is out of the range Python can represent |
| timetz (1266) | `b'00:00:00+00'` | `time(0, 0, tzinfo=timezone.utc)` |
| timetz (1266) | `b'12:34:56.789+05:30'` | `time(12, 34, 56, 789000, tzinfo=timezone(timedelta(seconds=19800)))` |
| timetz (1266) | `b'12:00:00-08'` | `time(12, 0, tzinfo=timezone(timedelta(days=-1, seconds=57600)))` |
| timetz (1266) | `b'12:00:00+01:02:03'` | `time(12, 0, tzinfo=timezone(timedelta(seconds=3723)))` |
| timetz (1266) | `b'23:59:59.999999-15:59'` | `time(23, 59, 59, 999999, tzinfo=timezone(timedelta(days=-1, seconds=28860)))` |
| timetz (1266) | `b'12:00:00+15:59:59'` | `time(12, 0, tzinfo=timezone(timedelta(seconds=57599)))` |
| timetz (1266) | `b'12:00:00-00:00:30'` | `time(12, 0, tzinfo=timezone(timedelta(days=-1, seconds=86370)))` |
| timetz (1266) | `b'24:00:00-15:59'` | raises `DataError`: timetz value '24:00:00-15:59' is out of the range Python can represent |

The complete file:

```python
"""Adapters of date, time and timetz."""
from __future__ import annotations

import datetime as dt
import re
from typing import Any

from .. import _oids
from .. import errors as e
from ..adapt import AdaptContext, AdaptersMap, Dumper, Loader, context_parameter

_DATE = re.compile(rb"^(\d{4,})-(\d\d)-(\d\d)( BC)?$")
_TIME = re.compile(rb"^(\d\d):(\d\d):(\d\d)(?:\.(\d{1,6}))?$")
_TIMETZ = re.compile(
    rb"^(\d\d):(\d\d):(\d\d)(?:\.(\d{1,6}))?([-+])(\d\d)(?::(\d\d))?(?::(\d\d))?$"
)


def _out_of_range(kind: str, data: bytes) -> e.DataError:
    text = data.decode("ascii", "replace")
    return e.DataError(
        f"{kind} value {text!r} is out of the range Python can represent"
    )


def _unparsable(kind: str, data: bytes, context: AdaptContext | None) -> e.DataError:
    text = data.decode("ascii", "replace")
    style = context_parameter(context, b"DateStyle")
    if style is not None and style != b"ISO" and not style.startswith(b"ISO,"):
        shown = style.decode("ascii", "replace")
        return e.DataError(
            f"cannot parse {kind} value {text!r}: the session's DateStyle is"
            f" {shown!r}, only ISO is supported"
        )
    return e.DataError(f"cannot parse {kind} value {text!r}")


def _micros(fraction: bytes | None) -> int:
    return int(fraction.ljust(6, b"0")) if fraction else 0


class DateDumper(Dumper):
    """datetime.date -> date."""

    oid = _oids.DATE_OID

    def dump(self, obj: Any) -> bytes:
        """Return YYYY-MM-DD, which every DateStyle reads the same way."""
        return obj.isoformat().encode("ascii")


class TimeDumper(Dumper):
    """datetime.time -> time, or timetz when the value has a UTC offset."""

    oid = _oids.TIME_OID

    def dump(self, obj: Any) -> bytes:
        """Return HH:MM:SS[.ffffff] followed by the UTC offset, if any."""
        return self.dump_typed(obj)[0]

    def dump_typed(self, obj: Any) -> tuple[bytes, int]:
        """Return the text and the OID of time or of timetz."""
        data = obj.isoformat().encode("ascii")
        if obj.utcoffset() is not None:
            return data, _oids.TIMETZ_OID
        if obj.tzinfo is not None:
            raise e.DataError(
                f"cannot dump the time {obj!r}: its time zone has no fixed"
                " UTC offset"
            )
        return data, _oids.TIME_OID


class DateLoader(Loader):
    """date -> datetime.date."""

    def load(self, data: bytes) -> dt.date:
        """Return the date; BC dates, years above 9999 and infinity fail."""
        match = _DATE.match(data)
        if match is None:
            if data in (b"infinity", b"-infinity"):
                raise _out_of_range("date", data)
            raise _unparsable("date", data, self.context)
        year, month, day, bc = match.groups()
        if bc:
            raise _out_of_range("date", data)
        try:
            return dt.date(int(year), int(month), int(day))
        except ValueError:
            raise _out_of_range("date", data) from None


class TimeLoader(Loader):
    """time -> datetime.time."""

    def load(self, data: bytes) -> dt.time:
        """Return the naive time; 24:00:00 fails."""
        match = _TIME.match(data)
        if match is None:
            raise _unparsable("time", data, None)
        hour, minute, second, fraction = match.groups()
        try:
            return dt.time(int(hour), int(minute), int(second), _micros(fraction))
        except ValueError:
            raise _out_of_range("time", data) from None


class TimetzLoader(Loader):
    """timetz -> datetime.time with a fixed-offset tzinfo."""

    def load(self, data: bytes) -> dt.time:
        """Return the aware time; 24:00:00 fails."""
        match = _TIMETZ.match(data)
        if match is None:
            raise _unparsable("timetz", data, None)
        hour, minute, second, fraction, sign, oh, om, os_ = match.groups()
        offset = dt.timedelta(
            hours=int(oh), minutes=int(om or 0), seconds=int(os_ or 0)
        )
        if sign == b"-":
            offset = -offset
        try:
            return dt.time(
                int(hour), int(minute), int(second), _micros(fraction),
                dt.timezone(offset),
            )
        except ValueError:
            raise _out_of_range("timetz", data) from None


def register_default_adapters(adapters: AdaptersMap) -> None:
    """Register the adapters of this module in a map."""
    adapters.register_dumper(dt.date, DateDumper)
    adapters.register_dumper(dt.time, TimeDumper)
    adapters.register_loader(_oids.DATE_OID, DateLoader)
    adapters.register_loader(_oids.TIME_OID, TimeLoader)
    adapters.register_loader(_oids.TIMETZ_OID, TimetzLoader)
```

### 6.5 `stuntgres/types/timestamp.py` (timestamp, timestamptz)

Grammars:

| Type | Pattern |
|---|---|
| timestamp | `^(\d{4,})-(\d\d)-(\d\d) (\d\d):(\d\d):(\d\d)(?:\.(\d{1,6}))?( BC)?$` |
| timestamptz | `^(\d{4,})-(\d\d)-(\d\d) (\d\d):(\d\d):(\d\d)(?:\.(\d{1,6}))?([-+])(\d\d)(?::(\d\d))?(?::(\d\d))?( BC)?$` |

PostgreSQL prints a `timestamptz` in the session's `TimeZone` with the UTC offset of
that instant: `+HH`, `+HH:MM` or, for local mean times before standard time,
`+HH:MM:SS` **(measured: `1900-01-01 05:21:10+05:21:10` in `Asia/Kolkata`)**.

| Member | Algorithm |
|---|---|
| `session_zone(name: bytes \| None) -> datetime.tzinfo \| None` | Public function, `functools.lru_cache(maxsize=64)`. `None` or empty: `None`. `zoneinfo.ZoneInfo(name.decode("ascii"))`; `KeyError` (which `ZoneInfoNotFoundError` is), `ValueError` (also the decode error) and `OSError` give `None`. |
| `DatetimeDumper` (`oid` 1184), for `datetime.datetime` | `dump`: `obj.isoformat(" ").encode("ascii")`. `dump_typed`: OID 1114 when `obj.utcoffset()` is `None`, else OID 1184. |
| `TimestampLoader`, OID 1114 | No match: `infinity` and `-infinity` raise error 1, anything else error 2. BC: error 1. `datetime.datetime(year, month, day, hour, minute, second, microsecond)`; `ValueError` becomes error 1. Naive. |
| `TimestamptzLoader.__init__`, OID 1184 | `self._zone = session_zone(context_parameter(context, b"TimeZone"))`. |
| `TimestamptzLoader.load` | 1. Match; errors as for timestamp with the kind `timestamptz`. 2. `offset` from sign and the three offset groups as in 6.4. 3. `naive = datetime.datetime(...)` of the seven date and time groups; `ValueError` becomes error 1. 4. If `self._zone` is not `None`: `local = naive.replace(tzinfo=zone)`; if `local.utcoffset() == offset` return `local`; `local = local.replace(fold=1)`; if `local.utcoffset() == offset` return `local`. 5. Return `naive.replace(tzinfo=datetime.timezone(offset))`. |

Step 4 never converts through UTC, so it cannot overflow at year 1 or 9999, and the
result always shows the wall time and the offset the server printed. `fold=1` is the
second occurrence of a repeated hour **(measured: `2021-10-31 02:30:00+01` in
`Europe/Warsaw` loads with `fold == 1`, `2021-10-31 02:30:00+02` with `fold == 0`)**. Step
5 is the defined fallback: no `TimeZone` parameter, a zone name Python does not know
(`<+0530>-5:30`, `<+03>-03`, `-03:30`; an operating system without tzdata), or a zone
database that disagrees with the server's. Two servers name UTC differently (`Etc/UTC` in
the pinned image, `UTC` in the stuntgres server), so tests compare values, offsets and
`str(value.tzinfo)` against the reported parameter, never against a literal zone name.

| Type (OID) | Text sent by PostgreSQL 18.6 | Python result |
|---|---|---|
| timestamp (1114) | `b'2000-01-01 00:00:00'` | `datetime(2000, 1, 1, 0, 0)` |
| timestamp (1114) | `b'2000-01-01 12:34:56.123456'` | `datetime(2000, 1, 1, 12, 34, 56, 123456)` |
| timestamp (1114) | `b'2000-01-01 12:34:56.5'` | `datetime(2000, 1, 1, 12, 34, 56, 500000)` |
| timestamp (1114) | `b'2000-01-01 12:34:56.0001'` | `datetime(2000, 1, 1, 12, 34, 56, 100)` |
| timestamp (1114) | `b'0001-01-01 00:00:00'` | `datetime(1, 1, 1, 0, 0)` |
| timestamp (1114) | `b'0099-07-04 01:02:03'` | `datetime(99, 7, 4, 1, 2, 3)` |
| timestamp (1114) | `b'9999-12-31 23:59:59.999999'` | `datetime(9999, 12, 31, 23, 59, 59, 999999)` |
| timestamp (1114) | `b'10000-01-01 00:00:00'` | raises `DataError`: timestamp value '10000-01-01 00:00:00' is out of the range Python can represent |
| timestamp (1114) | `b'294276-12-31 23:59:59.999999'` | raises `DataError`: timestamp value '294276-12-31 23:59:59.999999' is out of the range Python can represent |
| timestamp (1114) | `b'0001-12-31 23:59:59 BC'` | raises `DataError`: timestamp value '0001-12-31 23:59:59 BC' is out of the range Python can represent |
| timestamp (1114) | `b'4713-11-24 00:00:00 BC'` | raises `DataError`: timestamp value '4713-11-24 00:00:00 BC' is out of the range Python can represent |
| timestamp (1114) | `b'infinity'` | raises `DataError`: timestamp value 'infinity' is out of the range Python can represent |
| timestamp (1114) | `b'-infinity'` | raises `DataError`: timestamp value '-infinity' is out of the range Python can represent |
| timestamp, DateStyle SQL, MDY (1114) | `b'01/02/2000 03:04:05.6'` | raises `DataError`: cannot parse timestamp value '01/02/2000 03:04:05.6': the session's DateStyle is 'SQL, MDY', only ISO is supported |
| timestamp, DateStyle Postgres, MDY (1114) | `b'Sun Jan 02 03:04:05.6 2000'` | raises `DataError`: cannot parse timestamp value 'Sun Jan 02 03:04:05.6 2000': the session's DateStyle is 'Postgres, MDY', only ISO is supported |
| timestamp, DateStyle German (1114) | `b'02.01.2000 03:04:05.6'` | raises `DataError`: cannot parse timestamp value '02.01.2000 03:04:05.6': the session's DateStyle is 'German, DMY', only ISO is supported |

timestamptz with `TimeZone` `UTC`:

| Type (OID) | Text sent by PostgreSQL 18.6 | Python result |
|---|---|---|
| TimeZone UTC (1184) | `b'2000-01-01 00:00:00+00'` | `datetime(2000, 1, 1, 0, 0, tzinfo=ZoneInfo(key='UTC'))` |
| TimeZone UTC (1184) | `b'2000-07-01 12:34:56.123456+00'` | `datetime(2000, 7, 1, 12, 34, 56, 123456, tzinfo=ZoneInfo(key='UTC'))` |
| TimeZone UTC (1184) | `b'0001-01-01 00:00:00+00'` | `datetime(1, 1, 1, 0, 0, tzinfo=ZoneInfo(key='UTC'))` |
| TimeZone UTC (1184) | `b'9999-12-31 23:59:59.999999+00'` | `datetime(9999, 12, 31, 23, 59, 59, 999999, tzinfo=ZoneInfo(key='UTC'))` |
| TimeZone UTC (1184) | `b'10000-01-01 00:00:00+00'` | raises `DataError`: timestamptz value '10000-01-01 00:00:00+00' is out of the range Python can represent |
| TimeZone UTC (1184) | `b'0001-12-31 23:59:59+00 BC'` | raises `DataError`: timestamptz value '0001-12-31 23:59:59+00 BC' is out of the range Python can represent |
| TimeZone UTC (1184) | `b'infinity'` | raises `DataError`: timestamptz value 'infinity' is out of the range Python can represent |
| TimeZone UTC (1184) | `b'-infinity'` | raises `DataError`: timestamptz value '-infinity' is out of the range Python can represent |

timestamptz with `TimeZone` `Europe/Warsaw` (the texts are what PostgreSQL 18.6 printed
for the same instants in that zone):

| Type (OID) | Text sent by PostgreSQL 18.6 | Python result |
|---|---|---|
| TimeZone Europe/Warsaw (1184) | `b'2000-01-01 01:00:00+01'` | `datetime(2000, 1, 1, 1, 0, tzinfo=ZoneInfo(key='Europe/Warsaw'))` |
| TimeZone Europe/Warsaw (1184) | `b'2000-07-01 14:34:56.123456+02'` | `datetime(2000, 7, 1, 14, 34, 56, 123456, tzinfo=ZoneInfo(key='Europe/Warsaw'))` |
| TimeZone Europe/Warsaw (1184) | `b'1900-01-01 01:24:00+01:24'` | `datetime(1900, 1, 1, 1, 24, tzinfo=ZoneInfo(key='Europe/Warsaw'))` |
| TimeZone Europe/Warsaw (1184) | `b'0001-01-01 01:24:00+01:24'` | `datetime(1, 1, 1, 1, 24, tzinfo=ZoneInfo(key='Europe/Warsaw'))` |
| TimeZone Europe/Warsaw (1184) | `b'2021-03-28 01:59:59+01'` | `datetime(2021, 3, 28, 1, 59, 59, tzinfo=ZoneInfo(key='Europe/Warsaw'))` |
| TimeZone Europe/Warsaw (1184) | `b'2021-03-28 03:00:00+02'` | `datetime(2021, 3, 28, 3, 0, tzinfo=ZoneInfo(key='Europe/Warsaw'))` |
| TimeZone Europe/Warsaw (1184) | `b'10000-01-01 00:59:59.999999+01'` | raises `DataError`: timestamptz value '10000-01-01 00:59:59.999999+01' is out of the range Python can represent |
| TimeZone Europe/Warsaw (1184) | `b'0001-01-01 01:23:59+01:24'` | `datetime(1, 1, 1, 1, 23, 59, tzinfo=ZoneInfo(key='Europe/Warsaw'))` |

Other zones and styles (the first column names the session setting):

| Type (OID) | Text sent by PostgreSQL 18.6 | Python result |
|---|---|---|
| TimeZone Asia/Kolkata (1184) | `b'1900-01-01 05:21:10+05:21:10'` | `datetime(1900, 1, 1, 5, 21, 10, tzinfo=ZoneInfo(key='Asia/Kolkata'))` |
| TimeZone America/St_Johns (1184) | `b'1899-12-31 20:29:08-03:30:52'` | `datetime(1899, 12, 31, 20, 29, 8, tzinfo=ZoneInfo(key='America/St_Johns'))` |
| TimeZone <+0530>-5:30 (1184) | `b'2000-01-01 05:30:00+05:30'` | `datetime(2000, 1, 1, 5, 30, tzinfo=timezone(timedelta(seconds=19800)))` |
| TimeZone +3, reported as <+03>-03 (1184) | `b'2000-01-01 03:00:00+03'` | `datetime(2000, 1, 1, 3, 0, tzinfo=timezone(timedelta(seconds=10800)))` |
| TimeZone -03:30 (1184) | `b'2000-01-01 03:30:00+03:30'` | `datetime(2000, 1, 1, 3, 30, tzinfo=timezone(timedelta(seconds=12600)))` |
| TimeZone Etc/GMT+12 (1184) | `b'1999-12-31 12:00:00-12'` | `datetime(1999, 12, 31, 12, 0, tzinfo=ZoneInfo(key='Etc/GMT+12'))` |
| TimeZone Pacific/Kiritimati (1184) | `b'2000-01-01 14:00:00+14'` | `datetime(2000, 1, 1, 14, 0, tzinfo=ZoneInfo(key='Pacific/Kiritimati'))` |
| TimeZone America/St_Johns (1184) | `b'0001-12-31 20:29:08-03:30:52 BC'` | raises `DataError`: timestamptz value '0001-12-31 20:29:08-03:30:52 BC' is out of the range Python can represent |
| DateStyle SQL, MDY (1184) | `b'01/02/2000 03:04:05.6 UTC'` | raises `DataError`: cannot parse timestamptz value '01/02/2000 03:04:05.6 UTC': the session's DateStyle is 'SQL, MDY', only ISO is supported |
| DateStyle Postgres, MDY (1184) | `b'Sun Jan 02 03:04:05.6 2000 UTC'` | raises `DataError`: cannot parse timestamptz value 'Sun Jan 02 03:04:05.6 2000 UTC': the session's DateStyle is 'Postgres, MDY', only ISO is supported |
| DateStyle German (1184) | `b'02.01.2000 03:04:05.6 UTC'` | raises `DataError`: cannot parse timestamptz value '02.01.2000 03:04:05.6 UTC': the session's DateStyle is 'German, DMY', only ISO is supported |

The complete file:

```python
"""Adapters of timestamp and timestamptz."""
from __future__ import annotations

import datetime as dt
import re
import zoneinfo
from functools import lru_cache
from typing import Any

from .. import _oids
from .. import errors as e
from ..adapt import AdaptContext, AdaptersMap, Dumper, Loader, context_parameter

_STAMP = rb"^(\d{4,})-(\d\d)-(\d\d) (\d\d):(\d\d):(\d\d)(?:\.(\d{1,6}))?"
_TIMESTAMP = re.compile(_STAMP + rb"( BC)?$")
_TIMESTAMPTZ = re.compile(
    _STAMP + rb"([-+])(\d\d)(?::(\d\d))?(?::(\d\d))?( BC)?$"
)
_INFINITE = (b"infinity", b"-infinity")


def _out_of_range(kind: str, data: bytes) -> e.DataError:
    text = data.decode("ascii", "replace")
    return e.DataError(
        f"{kind} value {text!r} is out of the range Python can represent"
    )


def _unparsable(kind: str, data: bytes, context: AdaptContext | None) -> e.DataError:
    text = data.decode("ascii", "replace")
    style = context_parameter(context, b"DateStyle")
    if style is not None and style != b"ISO" and not style.startswith(b"ISO,"):
        shown = style.decode("ascii", "replace")
        return e.DataError(
            f"cannot parse {kind} value {text!r}: the session's DateStyle is"
            f" {shown!r}, only ISO is supported"
        )
    return e.DataError(f"cannot parse {kind} value {text!r}")


def _micros(fraction: bytes | None) -> int:
    return int(fraction.ljust(6, b"0")) if fraction else 0


@lru_cache(maxsize=64)
def session_zone(name: bytes | None) -> dt.tzinfo | None:
    """Return the tzinfo of a TimeZone setting, None when Python has none."""
    if not name:
        return None
    try:
        return zoneinfo.ZoneInfo(name.decode("ascii"))
    except (KeyError, ValueError, OSError):
        return None


class DatetimeDumper(Dumper):
    """datetime.datetime -> timestamptz when aware, timestamp when naive."""

    oid = _oids.TIMESTAMPTZ_OID

    def dump(self, obj: Any) -> bytes:
        """Return YYYY-MM-DD HH:MM:SS[.ffffff] and the UTC offset, if any."""
        data: bytes = obj.isoformat(" ").encode("ascii")
        return data

    def dump_typed(self, obj: Any) -> tuple[bytes, int]:
        """Return the text and the OID of timestamptz or of timestamp."""
        if obj.utcoffset() is None:
            return self.dump(obj), _oids.TIMESTAMP_OID
        return self.dump(obj), _oids.TIMESTAMPTZ_OID


class TimestampLoader(Loader):
    """timestamp -> naive datetime.datetime."""

    def load(self, data: bytes) -> dt.datetime:
        """Return the datetime; BC, years above 9999 and infinity fail."""
        match = _TIMESTAMP.match(data)
        if match is None:
            if data in _INFINITE:
                raise _out_of_range("timestamp", data)
            raise _unparsable("timestamp", data, self.context)
        year, month, day, hour, minute, second, fraction, bc = match.groups()
        if bc:
            raise _out_of_range("timestamp", data)
        try:
            return dt.datetime(
                int(year), int(month), int(day),
                int(hour), int(minute), int(second), _micros(fraction),
            )
        except ValueError:
            raise _out_of_range("timestamp", data) from None


class TimestamptzLoader(Loader):
    """timestamptz -> aware datetime.datetime in the session's time zone."""

    def __init__(self, oid: int, context: AdaptContext | None = None) -> None:
        super().__init__(oid, context)
        self._zone = session_zone(context_parameter(context, b"TimeZone"))

    def load(self, data: bytes) -> dt.datetime:
        """Return the datetime; BC, years above 9999 and infinity fail."""
        match = _TIMESTAMPTZ.match(data)
        if match is None:
            if data in _INFINITE:
                raise _out_of_range("timestamptz", data)
            raise _unparsable("timestamptz", data, self.context)
        (year, month, day, hour, minute, second, fraction,
         sign, oh, om, os_, bc) = match.groups()
        if bc:
            raise _out_of_range("timestamptz", data)
        offset = dt.timedelta(
            hours=int(oh), minutes=int(om or 0), seconds=int(os_ or 0)
        )
        if sign == b"-":
            offset = -offset
        try:
            naive = dt.datetime(
                int(year), int(month), int(day),
                int(hour), int(minute), int(second), _micros(fraction),
            )
        except ValueError:
            raise _out_of_range("timestamptz", data) from None
        zone = self._zone
        if zone is not None:
            local = naive.replace(tzinfo=zone)
            if local.utcoffset() == offset:
                return local
            local = local.replace(fold=1)
            if local.utcoffset() == offset:
                return local
        return naive.replace(tzinfo=dt.timezone(offset))


def register_default_adapters(adapters: AdaptersMap) -> None:
    """Register the adapters of this module in a map."""
    adapters.register_dumper(dt.datetime, DatetimeDumper)
    adapters.register_loader(_oids.TIMESTAMP_OID, TimestampLoader)
    adapters.register_loader(_oids.TIMESTAMPTZ_OID, TimestamptzLoader)
```

### 6.6 `stuntgres/types/interval.py`

Grammar of the `postgres` IntervalStyle (anchored bytes pattern, every part optional but
not all absent):

```
^(?:([-+]?\d+) years? ?)?(?:([-+]?\d+) mons? ?)?(?:([-+]?\d+) days? ?)?
 (?:([-+])?(\d+):(\d\d):(\d\d)(?:\.(\d{1,6}))?)?$
```

(one pattern; the line break is not part of it). Groups: years, months, days, the sign of
the clock part, hours, minutes, seconds, fraction. PostgreSQL writes `year`/`years`,
`mon`/`mons`, `day`/`days` (plural for every count but 1, also for -1), puts an explicit
`+` on a field that follows a negative one, and applies the clock sign to hours, minutes,
seconds and fraction together.

| Class | Registered for | Algorithm |
|---|---|---|
| `TimedeltaDumper` (`oid` 1186) | `datetime.timedelta` | `b"%+d days %+d seconds %+d microseconds" % (obj.days, obj.seconds, obj.microseconds)`: the three normalised fields of the timedelta, each with an explicit sign. |
| `IntervalLoader` | OID 1186 | 1. No match or empty input: `infinity` and `-infinity` raise error 1, anything else error 2. 2. `clock = timedelta(hours, minutes, seconds, microseconds)` of the clock groups (missing ones 0), negated for the sign `-`. 3. `days = 365 * years + 30 * months + days` (missing ones 0). 4. Return `timedelta(days=days) + clock`. An `OverflowError` in steps 2 to 4 becomes error 1. |

A `timedelta` has no months: a year counts as 365 days and a month as 30 days (the
convention of psycopg 2 and 3). Values with months or years therefore do not round-trip
exactly; values of days and time do. PostgreSQL intervals reach beyond `timedelta`
(999999999 days): those raise error 1. In the `sql_standard` style a value with only a
clock part prints like the `postgres` style and loads; every other output of the three
other styles raises error 2 with the IntervalStyle suffix.

| Type (OID) | Text sent by PostgreSQL 18.6 | Python result |
|---|---|---|
| interval, IntervalStyle postgres (1186) | `b'00:00:00'` | `timedelta(0)` |
| interval, IntervalStyle postgres (1186) | `b'1 day'` | `timedelta(days=1)` |
| interval, IntervalStyle postgres (1186) | `b'2 days'` | `timedelta(days=2)` |
| interval, IntervalStyle postgres (1186) | `b'-1 days'` | `timedelta(days=-1)` |
| interval, IntervalStyle postgres (1186) | `b'7 days'` | `timedelta(days=7)` |
| interval, IntervalStyle postgres (1186) | `b'1 day 12:00:00'` | `timedelta(days=1, seconds=43200)` |
| interval, IntervalStyle postgres (1186) | `b'00:00:01'` | `timedelta(seconds=1)` |
| interval, IntervalStyle postgres (1186) | `b'-00:00:01'` | `timedelta(days=-1, seconds=86399)` |
| interval, IntervalStyle postgres (1186) | `b'01:00:00'` | `timedelta(seconds=3600)` |
| interval, IntervalStyle postgres (1186) | `b'100:00:00'` | `timedelta(days=4, seconds=14400)` |
| interval, IntervalStyle postgres (1186) | `b'00:00:00.000001'` | `timedelta(microseconds=1)` |
| interval, IntervalStyle postgres (1186) | `b'-00:00:00.000001'` | `timedelta(days=-1, seconds=86399, microseconds=999999)` |
| interval, IntervalStyle postgres (1186) | `b'1 day 00:00:00.5'` | `timedelta(days=1, microseconds=500000)` |
| interval, IntervalStyle postgres (1186) | `b'1 day -00:00:01'` | `timedelta(seconds=86399)` |
| interval, IntervalStyle postgres (1186) | `b'3 days -04:05:06'` | `timedelta(days=2, seconds=71694)` |
| interval, IntervalStyle postgres (1186) | `b'1 year'` | `timedelta(days=365)` |
| interval, IntervalStyle postgres (1186) | `b'2 years'` | `timedelta(days=730)` |
| interval, IntervalStyle postgres (1186) | `b'-1 years'` | `timedelta(days=-365)` |
| interval, IntervalStyle postgres (1186) | `b'1 mon'` | `timedelta(days=30)` |
| interval, IntervalStyle postgres (1186) | `b'2 mons'` | `timedelta(days=60)` |
| interval, IntervalStyle postgres (1186) | `b'11 mons'` | `timedelta(days=330)` |
| interval, IntervalStyle postgres (1186) | `b'-10 mons'` | `timedelta(days=-300)` |
| interval, IntervalStyle postgres (1186) | `b'1 year 2 mons'` | `timedelta(days=425)` |
| interval, IntervalStyle postgres (1186) | `b'1 mon 1 day'` | `timedelta(days=31)` |
| interval, IntervalStyle postgres (1186) | `b'-1 mons +1 day'` | `timedelta(days=-29)` |
| interval, IntervalStyle postgres (1186) | `b'1 mon -1 days +00:00:01'` | `timedelta(days=29, seconds=1)` |
| interval, IntervalStyle postgres (1186) | `b'1 year 2 mons 3 days 04:05:06.789'` | `timedelta(days=428, seconds=14706, microseconds=789000)` |
| interval, IntervalStyle postgres (1186) | `b'-1 years -2 mons -3 days -04:05:06.789'` | `timedelta(days=-429, seconds=71693, microseconds=211000)` |
| interval, IntervalStyle postgres (1186) | `b'999999999 days'` | `timedelta(days=999999999)` |
| interval, IntervalStyle postgres (1186) | `b'-999999999 days'` | `timedelta(days=-999999999)` |
| interval, IntervalStyle postgres (1186) | `b'2562047788:00:54.775807'` | `timedelta(days=106751991, seconds=14454, microseconds=775807)` |
| interval, IntervalStyle postgres (1186) | `b'1000000000 days'` | raises `DataError`: interval value '1000000000 days' is out of the range Python can represent |
| interval, IntervalStyle postgres (1186) | `b'178000000 years'` | raises `DataError`: interval value '178000000 years' is out of the range Python can represent |
| interval, IntervalStyle postgres (1186) | `b'infinity'` | raises `DataError`: interval value 'infinity' is out of the range Python can represent |
| interval, IntervalStyle postgres (1186) | `b'-infinity'` | raises `DataError`: interval value '-infinity' is out of the range Python can represent |
| interval, IntervalStyle sql_standard (1186) | `b'+1-2 +3 +4:05:06.789'` | raises `DataError`: cannot parse interval value '+1-2 +3 +4:05:06.789': the session's IntervalStyle is 'sql_standard', only postgres is supported |
| interval, IntervalStyle sql_standard (1186) | `b'0'` | raises `DataError`: cannot parse interval value '0': the session's IntervalStyle is 'sql_standard', only postgres is supported |
| interval, IntervalStyle sql_standard (1186) | `b'0-1'` | raises `DataError`: cannot parse interval value '0-1': the session's IntervalStyle is 'sql_standard', only postgres is supported |
| interval, IntervalStyle sql_standard (1186) | `b'2 0:00:00'` | raises `DataError`: cannot parse interval value '2 0:00:00': the session's IntervalStyle is 'sql_standard', only postgres is supported |
| interval, IntervalStyle sql_standard (1186) | `b'0:00:00.000001'` | `timedelta(microseconds=1)` |
| interval, IntervalStyle sql_standard (1186) | `b'-0:00:01'` | `timedelta(days=-1, seconds=86399)` |
| interval, IntervalStyle postgres_verbose (1186) | `b'@ 1 year 2 mons 3 days 4 hours 5 mins 6.789 secs'` | raises `DataError`: cannot parse interval value '@ 1 year 2 mons 3 days 4 hours 5 mins 6.789 secs': the session's IntervalStyle is 'postgres_verbose', only postgres is supported |
| interval, IntervalStyle postgres_verbose (1186) | `b'@ 0'` | raises `DataError`: cannot parse interval value '@ 0': the session's IntervalStyle is 'postgres_verbose', only postgres is supported |
| interval, IntervalStyle postgres_verbose (1186) | `b'@ 1 sec ago'` | raises `DataError`: cannot parse interval value '@ 1 sec ago': the session's IntervalStyle is 'postgres_verbose', only postgres is supported |
| interval, IntervalStyle iso_8601 (1186) | `b'P1Y2M3DT4H5M6.789S'` | raises `DataError`: cannot parse interval value 'P1Y2M3DT4H5M6.789S': the session's IntervalStyle is 'iso_8601', only postgres is supported |
| interval, IntervalStyle iso_8601 (1186) | `b'PT0S'` | raises `DataError`: cannot parse interval value 'PT0S': the session's IntervalStyle is 'iso_8601', only postgres is supported |
| interval, IntervalStyle iso_8601 (1186) | `b'P1DT-1S'` | raises `DataError`: cannot parse interval value 'P1DT-1S': the session's IntervalStyle is 'iso_8601', only postgres is supported |

The complete file:

```python
"""Adapters of interval."""
from __future__ import annotations

import datetime as dt
import re
from typing import Any

from .. import _oids
from .. import errors as e
from ..adapt import AdaptContext, AdaptersMap, Dumper, Loader, context_parameter

_INTERVAL = re.compile(
    rb"^(?:([-+]?\d+) years? ?)?(?:([-+]?\d+) mons? ?)?(?:([-+]?\d+) days? ?)?"
    rb"(?:([-+])?(\d+):(\d\d):(\d\d)(?:\.(\d{1,6}))?)?$"
)
_INFINITE = (b"infinity", b"-infinity")


def _out_of_range(data: bytes) -> e.DataError:
    text = data.decode("ascii", "replace")
    return e.DataError(
        f"interval value {text!r} is out of the range Python can represent"
    )


def _unparsable(data: bytes, context: AdaptContext | None) -> e.DataError:
    text = data.decode("ascii", "replace")
    style = context_parameter(context, b"IntervalStyle")
    if style is not None and style != b"postgres":
        shown = style.decode("ascii", "replace")
        return e.DataError(
            f"cannot parse interval value {text!r}: the session's IntervalStyle"
            f" is {shown!r}, only postgres is supported"
        )
    return e.DataError(f"cannot parse interval value {text!r}")


class TimedeltaDumper(Dumper):
    """datetime.timedelta -> interval."""

    oid = _oids.INTERVAL_OID

    def dump(self, obj: Any) -> bytes:
        """Return signed days, seconds and microseconds (any IntervalStyle)."""
        return b"%+d days %+d seconds %+d microseconds" % (
            obj.days, obj.seconds, obj.microseconds,
        )


class IntervalLoader(Loader):
    """interval -> datetime.timedelta (a year is 365 days, a month 30 days)."""

    def load(self, data: bytes) -> dt.timedelta:
        """Return the timedelta; infinity and values beyond timedelta fail."""
        match = _INTERVAL.match(data)
        if match is None or not data:
            if data in _INFINITE:
                raise _out_of_range(data)
            raise _unparsable(data, self.context)
        years, months, days, sign, hours, minutes, seconds, fraction = match.groups()
        try:
            clock = dt.timedelta(
                hours=int(hours or 0),
                minutes=int(minutes or 0),
                seconds=int(seconds or 0),
                microseconds=int(fraction.ljust(6, b"0")) if fraction else 0,
            )
            if sign == b"-":
                clock = -clock
            whole = 365 * int(years or 0) + 30 * int(months or 0) + int(days or 0)
            return dt.timedelta(days=whole) + clock
        except OverflowError:
            raise _out_of_range(data) from None


def register_default_adapters(adapters: AdaptersMap) -> None:
    """Register the adapters of this module in a map."""
    adapters.register_dumper(dt.timedelta, TimedeltaDumper)
    adapters.register_loader(_oids.INTERVAL_OID, IntervalLoader)
```

### 6.7 `stuntgres/types/uuid.py`

| Class | Registered for | Algorithm |
|---|---|---|
| `UUIDDumper` (`oid` 2950) | `uuid.UUID` | `str(obj).encode("ascii")`: 36 characters with hyphens. |
| `UUIDLoader` | OID 2950 | `uuid.UUID(data.decode("ascii"))`. PostgreSQL prints lower case with hyphens. |

The complete file:

```python
"""Adapters of uuid."""
from __future__ import annotations

import uuid
from typing import Any

from .. import _oids
from ..adapt import AdaptersMap, Dumper, Loader


class UUIDDumper(Dumper):
    """uuid.UUID -> uuid."""

    oid = _oids.UUID_OID

    def dump(self, obj: Any) -> bytes:
        """Return the 36-character form with hyphens."""
        return str(obj).encode("ascii")


class UUIDLoader(Loader):
    """uuid -> uuid.UUID."""

    def load(self, data: bytes) -> uuid.UUID:
        """Return the UUID."""
        return uuid.UUID(data.decode("ascii"))


def register_default_adapters(adapters: AdaptersMap) -> None:
    """Register the adapters of this module in a map."""
    adapters.register_dumper(uuid.UUID, UUIDDumper)
    adapters.register_loader(_oids.UUID_OID, UUIDLoader)
```

### 6.8 `stuntgres/types/json.py`

```python
JsonDumps = Callable[[Any], Union[str, bytes]]
JsonLoads = Callable[[str], Any]

class Json:
    __slots__ = ("obj", "dumps")
    def __init__(self, obj: Any, dumps: JsonDumps | None = None) -> None: ...
class Jsonb(Json): ...

def set_json_dumps(dumps: JsonDumps, context: AdaptContext | None = None) -> None: ...
def set_json_loads(loads: JsonLoads, context: AdaptContext | None = None) -> None: ...
```

A Python value is sent as JSON only inside a wrapper: `Json(value)` for the type `json`,
`Jsonb(value)` for `jsonb` (public names of psycopg 3). `repr` is
`f"{type(self).__name__}({self.obj!r})"`. `Jsonb(None)` is the JSON value `null`, not
SQL NULL.

| Class | Registered for | Algorithm |
|---|---|---|
| `JsonDumper` (`oid` 114) | `Json` | Class attribute `_dumps = staticmethod(json.dumps)`. `__init__` stores the encoding. `dump`: `dumps = obj.dumps or self._dumps`; `data = dumps(obj.obj)`; a `str` is encoded with the client encoding, anything else is returned as `bytes(data)`. |
| `JsonbDumper` (`oid` 3802) | `Jsonb` | Subclass of `JsonDumper`; only the OID differs. |
| `JsonLoader` | OID 114 | Class attribute `_loads = staticmethod(json.loads)`. `load`: `self._loads(data.decode(encoding))`. |
| `JsonbLoader` | OID 3802 | Subclass of `JsonLoader`, nothing added. |
| `set_json_dumps(dumps, context)` | | `adapters = context_adapters(context)`; for `(Json, JsonDumper)` and `(Jsonb, JsonbDumper)`: register for the wrapper a new subclass `type(base.__name__, (base,), {"_dumps": staticmethod(dumps)})`. |
| `set_json_loads(loads, context)` | | The same for `(114, JsonLoader)` and `(3802, JsonbLoader)` with `_loads`. |

With the default functions: numbers load as `int` or `float`, so `1.10` becomes `1.1`
and a number of 30 digits loses precision; a caller who needs exact numbers sets
`loads=lambda text: json.loads(text, parse_float=decimal.Decimal)` **(measured:
`{"a": 1.10}` then loads as `{'a': Decimal('1.10')}`)**. `json` keeps the text as it was
stored; `jsonb` is normalised by the server (sorted keys, one space after `:` and `,`,
the last of duplicate keys).

| Type (OID) | Text sent by PostgreSQL 18.6 | Python result |
|---|---|---|
| uuid (2950) | `b'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11'` | `UUID('a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11')` |
| uuid (2950) | `b'00000000-0000-0000-0000-000000000000'` | `UUID('00000000-0000-0000-0000-000000000000')` |
| uuid (2950) | `b'ffffffff-ffff-ffff-ffff-ffffffffffff'` | `UUID('ffffffff-ffff-ffff-ffff-ffffffffffff')` |
| json (114) | `b'{"a": 1, "b": [1, 2, null],   "c": "x"}'` | `{'a': 1, 'b': [1, 2, None], 'c': 'x'}` |
| jsonb (3802) | `b'{"a": 1, "b": [1, 2, null], "c": "x"}'` | `{'a': 1, 'b': [1, 2, None], 'c': 'x'}` |
| json (114) | `b'null'` | `None` |
| jsonb (3802) | `b'null'` | `None` |
| jsonb (3802) | `b'1'` | `1` |
| jsonb (3802) | `b'1.0'` | `1.0` |
| jsonb (3802) | `b'"s"'` | `'s'` |
| jsonb (3802) | `b'[]'` | `[]` |
| jsonb (3802) | `b'{}'` | `{}` |
| jsonb (3802) | `b'true'` | `True` |
| jsonb (3802) | `b'{"a": 1.10}'` | `{'a': 1.1}` |
| jsonb (3802) | `b'"\xc3\xa9"'` | `'é'` |
| jsonb (3802) | `b'"\xf0\x9f\x98\x80"'` | `'😀'` |
| json (114) | `b'"\\u00e9"'` | `'é'` |
| jsonb (3802) | `b'{"a": 3, "b": 1}'` | `{'a': 3, 'b': 1}` |
| jsonb (3802) | `b'12345678901234567890.123456789'` | `1.2345678901234567e+19` |

The complete file:

```python
"""Adapters of json and jsonb, and the wrappers that mark values as JSON."""
from __future__ import annotations

import json
from typing import Any, Callable, Union

from .. import _oids
from ..adapt import AdaptContext, AdaptersMap, Dumper, Loader
from ..adapt import context_adapters, context_encoding

JsonDumps = Callable[[Any], Union[str, bytes]]
JsonLoads = Callable[[str], Any]


class Json:
    """Mark a Python object to be sent as a json value."""

    __slots__ = ("obj", "dumps")

    def __init__(self, obj: Any, dumps: JsonDumps | None = None) -> None:
        self.obj = obj
        self.dumps = dumps

    def __repr__(self) -> str:
        return f"{type(self).__name__}({self.obj!r})"


class Jsonb(Json):
    """Mark a Python object to be sent as a jsonb value."""

    __slots__ = ()


class JsonDumper(Dumper):
    """Json -> json."""

    oid = _oids.JSON_OID
    _dumps: JsonDumps = staticmethod(json.dumps)

    def __init__(self, cls: type, context: AdaptContext | None = None) -> None:
        super().__init__(cls, context)
        self._encoding = context_encoding(context)

    def dump(self, obj: Any) -> bytes:
        """Serialize the wrapped object with its own or the registered function."""
        dumps = obj.dumps or self._dumps
        data = dumps(obj.obj)
        if isinstance(data, str):
            return data.encode(self._encoding)
        return bytes(data)


class JsonbDumper(JsonDumper):
    """Jsonb -> jsonb."""

    oid = _oids.JSONB_OID


class JsonLoader(Loader):
    """json -> the object json.loads makes."""

    _loads: JsonLoads = staticmethod(json.loads)

    def __init__(self, oid: int, context: AdaptContext | None = None) -> None:
        super().__init__(oid, context)
        self._encoding = context_encoding(context)

    def load(self, data: bytes) -> Any:
        """Parse the JSON text."""
        return self._loads(data.decode(self._encoding))


class JsonbLoader(JsonLoader):
    """jsonb -> the object json.loads makes."""


def set_json_dumps(dumps: JsonDumps, context: AdaptContext | None = None) -> None:
    """Set the function that serializes Json and Jsonb values in a context."""
    adapters = context_adapters(context)
    for wrapper, base in ((Json, JsonDumper), (Jsonb, JsonbDumper)):
        custom = type(base.__name__, (base,), {"_dumps": staticmethod(dumps)})
        adapters.register_dumper(wrapper, custom)


def set_json_loads(loads: JsonLoads, context: AdaptContext | None = None) -> None:
    """Set the function that parses json and jsonb values in a context."""
    adapters = context_adapters(context)
    for oid, base in ((_oids.JSON_OID, JsonLoader), (_oids.JSONB_OID, JsonbLoader)):
        custom = type(base.__name__, (base,), {"_loads": staticmethod(loads)})
        adapters.register_loader(oid, custom)


def register_default_adapters(adapters: AdaptersMap) -> None:
    """Register the adapters of this module in a map."""
    adapters.register_dumper(Json, JsonDumper)
    adapters.register_dumper(Jsonb, JsonbDumper)
    adapters.register_loader(_oids.JSON_OID, JsonLoader)
    adapters.register_loader(_oids.JSONB_OID, JsonbLoader)
```

### 6.9 `stuntgres/types/array.py`

```python
def parse_array(
    text: str, delimiter: str = ",", convert: Callable[[str], Any] | None = None
) -> list[Any]: ...

class ArrayLoader(Loader):
    base_oid: int = 0
    delimiter: str = ","

def register_array(
    element_oid: int, array_oid: int, context: AdaptContext | None = None,
    delimiter: str = ",",
) -> None: ...
```

The text of an array **(measured, experiment 1)**: an optional prefix of dimensions with
explicit bounds, `[2:4]=` or `[0:1][2:3]=`, printed only when some lower bound is not 1;
then `{`, elements separated by the type's delimiter, `}`; a nested array is an element
in braces; an empty array of any shape is `{}`. An element is `NULL` (a null), or its
text unquoted, or in double quotes with `\"` and `\\` as the only escapes. PostgreSQL
quotes an element that is empty, equals `NULL` in any letter case, or contains `{`, `}`,
the delimiter, `"`, `\` or white space (space, tab, newline, carriage return, vertical
tab, form feed); `;` and `'` and `=` are not quoted. Every core type uses the delimiter
`,` (`box` alone uses `;`).

`parse_array(text, delimiter, convert)`: `text` is the decoded value.

1. `pos = 0`. If `text` starts with `[`: `pos = text.find("=") + 1`; if that is 0 the
   value is malformed. (The bounds are skipped: Python lists start at 0.)
2. Token pattern for the delimiter `D` (compiled once per delimiter, `re.DOTALL`, `D`
   escaped with `re.escape`):
   `(\{)|(\})|"((?:[^"\\]|\\.)*)"|([^{}"\\D]+)|D`
   that is: group 1 an opening brace, group 2 a closing brace, group 3 the inside of a
   quoted element, group 4 an unquoted element, no group for a delimiter.
3. `stack = []` (the lists being filled, innermost last), `result = None`.
4. While `pos < len(text)`: match the pattern at `pos`. No match, or `result` is already
   set (text after the closing brace): malformed. `pos = match.end()`. By
   `match.lastindex`:
   - 1: make a new list; if the stack is not empty append it to the innermost list; push
     it.
   - 2: empty stack: malformed. Pop; if the stack is now empty, the popped list is
     `result`.
   - `None` (delimiter): empty stack: malformed. Nothing else (delimiters are not
     validated further).
   - 3: empty stack: malformed. `item = group 3`; if it contains a backslash replace
     every `\x` by `x` (pattern `\\(.)` with `re.DOTALL`). Append `convert(item)`, or
     `item` when `convert` is `None`.
   - 4: empty stack: malformed. If the token is exactly `NULL` append `None`; else
     append `convert(item)` or `item`.
5. `result is None` at the end (unclosed brace, empty text): malformed. Return `result`.

"Malformed" raises `DataError(f"malformed array value: {text[:60]!r}")`. The parser reads
what PostgreSQL prints; it is not a validator of array input (`{1,,2}` is accepted).

`ArrayLoader` is the base of one generated class per array type:

| Member | Algorithm |
|---|---|
| `base_oid`, `delimiter` | Class attributes set by `register_array`. |
| `__init__(oid, context)` | `adapters = context_adapters(context)`; `cls = adapters.get_loader(self.base_oid) or adapters.get_loader(0)`; `None`: `InterfaceError(f"no loader for the array element type with OID {self.base_oid}")`; `self._load_element = cls(self.base_oid, context).load`; store `context_encoding(context)`. |
| `load(data)` | `parse_array(data.decode(encoding), self.delimiter, lambda item: load_element(item.encode(encoding)))`. Elements are converted while parsing; nested arrays give nested lists. |

`register_array(element_oid, array_oid, context, delimiter)`: `adapters =
context_adapters(context)`; make `type(f"ArrayLoader_{array_oid}", (ArrayLoader,),
{"base_oid": element_oid, "delimiter": delimiter})`; `adapters.register_loader(array_oid,
<it>)`; `adapters.register_array_oid(element_oid, array_oid)`. `register_default_adapters`
calls it for every row of `_oids.BUILTIN_TYPES`.

Parsing happens on `str`, not on bytes, because in some client encodings (SJIS, BIG5,
GBK, GB18030, JOHAB, UHC) the second byte of a character can be `\`, `"`, `{` or `,`.

| Type (OID) | Text sent by PostgreSQL 18.6 | Python result |
|---|---|---|
| int4[] (1007) | `b'{}'` | `[]` |
| int4[] (1007) | `b'{1,2,3}'` | `[1, 2, 3]` |
| int4[] (1007) | `b'{{1,2},{3,4}}'` | `[[1, 2], [3, 4]]` |
| int4[] (1007) | `b'{1,NULL,3}'` | `[1, None, 3]` |
| int4[] (1007) | `b'[2:4]={1,2,3}'` | `[1, 2, 3]` |
| int4[] (1007) | `b'[0:1][2:3]={{1,2},{3,4}}'` | `[[1, 2], [3, 4]]` |
| int4[] (1007) | `b'[-1:0]={1,2}'` | `[1, 2]` |
| int4[] (1007) | `b'{{{1},{2}},{{3},{4}}}'` | `[[[1], [2]], [[3], [4]]]` |
| int4[] (1007) | `b'{NULL}'` | `[None]` |
| int4[] (1007) | `b'{{1,NULL},{NULL,4}}'` | `[[1, None], [None, 4]]` |
| int4[] (1007) | `b'[2:2][1:1]={{5}}'` | `[[5]]` |
| text[] (1009) | `b'{a}'` | `['a']` |
| text[] (1009) | `b'{""}'` | `['']` |
| text[] (1009) | `b'{"NULL"}'` | `['NULL']` |
| text[] (1009) | `b'{NULL,x}'` | `[None, 'x']` |
| text[] (1009) | `b'{"a b"}'` | `['a b']` |
| text[] (1009) | `b'{"a,b"}'` | `['a,b']` |
| text[] (1009) | `b'{"a\\"b"}'` | `['a"b']` |
| text[] (1009) | `b'{"a\\\\b"}'` | `['a\\b']` |
| text[] (1009) | `b'{"{"}'` | `['{']` |
| text[] (1009) | `b'{"}"}'` | `['}']` |
| text[] (1009) | `b'{"\n"}'` | `['\n']` |
| text[] (1009) | `b'{a;b}'` | `['a;b']` |
| text[] (1009) | `b'{"null"}'` | `['null']` |
| text[] (1009) | `b'{nul}'` | `['nul']` |
| text[] (1009) | `b'{{a,b},{c,NULL}}'` | `[['a', 'b'], ['c', None]]` |
| bool[] (1000) | `b'{t,f,NULL}'` | `[True, False, None]` |
| int2[] (1005) | `b'{1,2}'` | `[1, 2]` |
| int8[] (1016) | `b'{1,2}'` | `[1, 2]` |
| oid[] (1028) | `b'{1,2}'` | `[1, 2]` |
| float4[] (1021) | `b'{1.5,NaN,Infinity,-Infinity}'` | `[1.5, nan, inf, -inf]` |
| float8[] (1022) | `b'{1.5,NaN,Infinity,-Infinity}'` | `[1.5, nan, inf, -inf]` |
| numeric[] (1231) | `b'{1.50,NaN,Infinity,NULL}'` | `[Decimal('1.50'), Decimal('NaN'), Decimal('Infinity'), None]` |
| varchar[] (1015) | `b'{a,"b c"}'` | `['a', 'b c']` |
| bpchar[] (1014) | `b'{"a  ","b c"}'` | `['a  ', 'b c']` |
| name[] (1003) | `b'{a,"b c"}'` | `['a', 'b c']` |
| bytea[], hex (1001) | `b'{"\\\\x00ff","\\\\x"}'` | `[b'\x00\xff', b'']` |
| bytea[], escape (1001) | `b'{"\\\\000\\\\377\\\\\\\\",""}'` | `[b'\x00\xff\\', b'']` |
| date[] (1182) | `b'{2000-01-01,infinity,"0044-03-15 BC"}'` | raises `DataError`: date value 'infinity' is out of the range Python can represent |
| time[] (1183) | `b'{12:34:56,00:00:00.5}'` | `[time(12, 34, 56), time(0, 0, 0, 500000)]` |
| timetz[] (1270) | `b'{12:34:56+05:30,00:00:00.5-08}'` | `[time(12, 34, 56, tzinfo=timezone(timedelta(seconds=19800))), time(0, 0, 0, 500000, tzinfo=timezone(timedelta(days=-1, seconds=57600)))]` |
| timestamp[] (1115) | `b'{"2000-01-01 12:34:56",infinity}'` | raises `DataError`: timestamp value 'infinity' is out of the range Python can represent |
| timestamptz[] (1185) | `b'{"2000-01-01 12:34:56+00",infinity}'` | raises `DataError`: timestamptz value 'infinity' is out of the range Python can represent |
| interval[] (1187) | `b'{"1 day",00:00:01,"1 year 2 mons"}'` | `[timedelta(days=1), timedelta(seconds=1), timedelta(days=425)]` |
| uuid[] (2951) | `b'{a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11}'` | `[UUID('a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11')]` |
| json[] (199) | `b'{"{\\"a\\": 1}","[1,2]","null",NULL}'` | `[{'a': 1}, [1, 2], None, None]` |
| jsonb[] (3807) | `b'{"{\\"a\\": \\"x y\\"}","[1, 2]","\\"s\\"",1}'` | `[{'a': 'x y'}, [1, 2], 's', 1]` |
| box[], no loader (1020) | `b'{(1,1),(0,0);(3,3),(2,2)}'` | `'{(1,1),(0,0);(3,3),(2,2)}'` |

The text array with every character class **(measured)**: the SQL value
`ARRAY['a','','NULL','null','nuLL','a b',' a','a ','a,b','a"b','a\b','{}','a{b','x}y',E'new\nline',E'tab\t',E'cr\r',E'vt\x0b',E'ff\x0c',NULL,'ć','😀','a;b','a''b','a=b','[1]',' ']::text[]`
is printed as the Python string
`'{a,"","NULL","null","nuLL","a b"," a","a ","a,b","a\\"b","a\\\\b","{}","a{b","x}y","new\nline","tab\t","cr\r","vt\x0b","ff\x0c",NULL,ć,😀,a;b,a\'b,a=b,[1]," "}'`
(the constant `QUOTED` of the test file).

The complete file:

```python
"""Loading of PostgreSQL arrays into nested Python lists."""
from __future__ import annotations

import re
from functools import lru_cache
from typing import Any, Callable

from .. import _oids
from .. import errors as e
from ..adapt import AdaptContext, AdaptersMap, Loader
from ..adapt import context_adapters, context_encoding

_UNESCAPE = re.compile(r"\\(.)", re.DOTALL)


@lru_cache(maxsize=16)
def _tokens(delimiter: str) -> re.Pattern[str]:
    d = re.escape(delimiter)
    return re.compile(
        r'(\{)|(\})|"((?:[^"\\]|\\.)*)"|([^{}"\\' + d + r"]+)|" + d, re.DOTALL
    )


def _malformed(text: str) -> e.DataError:
    return e.DataError(f"malformed array value: {text[:60]!r}")


def parse_array(
    text: str,
    delimiter: str = ",",
    convert: Callable[[str], Any] | None = None,
) -> list[Any]:
    """Parse the text of an array into nested lists; NULL elements become None."""
    pos = 0
    if text[:1] == "[":
        pos = text.find("=") + 1
        if pos == 0:
            raise _malformed(text)
    match = _tokens(delimiter).match
    stack: list[list[Any]] = []
    result: list[Any] | None = None
    end = len(text)
    while pos < end:
        token = match(text, pos)
        if token is None or result is not None:
            raise _malformed(text)
        pos = token.end()
        kind = token.lastindex
        if kind == 1:
            new: list[Any] = []
            if stack:
                stack[-1].append(new)
            stack.append(new)
        elif kind is None:
            if not stack:
                raise _malformed(text)
        elif kind == 2:
            if not stack:
                raise _malformed(text)
            done = stack.pop()
            if not stack:
                result = done
        else:
            if not stack:
                raise _malformed(text)
            if kind == 3:
                item = token.group(3)
                if "\\" in item:
                    item = _UNESCAPE.sub(r"\1", item)
            else:
                item = token.group(4)
                if item == "NULL":
                    stack[-1].append(None)
                    continue
            stack[-1].append(item if convert is None else convert(item))
    if result is None:
        raise _malformed(text)
    return result


class ArrayLoader(Loader):
    """An array type -> nested lists of what the element loader returns."""

    base_oid: int = _oids.INVALID_OID
    delimiter: str = ","

    def __init__(self, oid: int, context: AdaptContext | None = None) -> None:
        super().__init__(oid, context)
        adapters = context_adapters(context)
        cls = adapters.get_loader(self.base_oid) or adapters.get_loader(0)
        if cls is None:
            raise e.InterfaceError(
                f"no loader for the array element type with OID {self.base_oid}"
            )
        self._load_element = cls(self.base_oid, context).load
        self._encoding = context_encoding(context)

    def load(self, data: bytes) -> list[Any]:
        """Return the array as nested lists; lower bounds are dropped."""
        encoding = self._encoding
        load = self._load_element
        return parse_array(
            data.decode(encoding),
            self.delimiter,
            lambda item: load(item.encode(encoding)),
        )


def register_array(
    element_oid: int,
    array_oid: int,
    context: AdaptContext | None = None,
    delimiter: str = ",",
) -> None:
    """Make a context load and dump arrays of the element type `element_oid`."""
    adapters = context_adapters(context)
    loader = type(
        f"ArrayLoader_{array_oid}",
        (ArrayLoader,),
        {"base_oid": element_oid, "delimiter": delimiter},
    )
    adapters.register_loader(array_oid, loader)
    adapters.register_array_oid(element_oid, array_oid)


def register_default_adapters(adapters: AdaptersMap) -> None:
    """Register the array types of the core set in a map."""
    for _name, oid, array_oid in _oids.BUILTIN_TYPES:
        register_array(oid, array_oid, adapters)
```

### 6.10 `stuntgres/types/list.py`

`ListDumper` (`oid` 0), registered for `list`:

| Member | Algorithm |
|---|---|
| `__init__(cls, context)` | `self._tx = Transformer.from_context(context)`: the transformer that made the dumper, or a new one when the dumper was made by hand. |
| `dump_typed(obj)` | 1. `out = []` (pieces of text), `oids = []` (distinct element OIDs in order of first appearance). 2. `_walk(obj, out, oids)`. 3. `text = "".join(out).encode(self._tx.encoding)`. 4. No OID collected: return `(text, 0)`. 5. One OID: it is the element OID. Several: if any is not one of 21, 23, 20, 1700 raise the `DataError` of 5.1; else the element OID is the one latest in the order 21, 23, 20, 1700. 6. Return `(text, self._tx.adapters.get_array_oid(element))` (0 when the map knows no array type for it). |
| `_walk(items, out, oids)` | Append `{`. For each item, a `,` before every item but the first, then: `None` gives `NULL`; a `list` recurses; anything else: `data, oid = self._tx.get_dumper(type(item)).dump_typed(item)`; remember `oid` if new; `text = data.decode(encoding)`; double every backslash, then put a backslash before every `"`; append `"` + text + `"`. Append `}`. |
| `dump(obj)` | The text of `dump_typed`. |

Every element is quoted (always valid input, also for numbers), so no rule about which
characters need quotes is involved. Only `list` is an array: a `tuple` is not adaptable.
Elements are dumped by the dumpers of the same transformer, so registrations on the
context apply to elements too.

The complete file:

```python
"""Dumping of Python lists as PostgreSQL arrays."""
from __future__ import annotations

from typing import Any

from .. import _oids
from .. import errors as e
from .._transformer import Transformer
from ..adapt import AdaptContext, AdaptersMap, Dumper

_INTEGERS = (_oids.INT2_OID, _oids.INT4_OID, _oids.INT8_OID, _oids.NUMERIC_OID)


class ListDumper(Dumper):
    """list -> the array type of its elements; unknown when there is none."""

    oid = _oids.INVALID_OID

    def __init__(self, cls: type, context: AdaptContext | None = None) -> None:
        super().__init__(cls, context)
        self._tx = Transformer.from_context(context)

    def dump(self, obj: Any) -> bytes:
        """Return the array text; every element is quoted, None is NULL."""
        return self.dump_typed(obj)[0]

    def dump_typed(self, obj: Any) -> tuple[bytes, int]:
        """Return the array text and the OID of the array type."""
        out: list[str] = []
        oids: list[int] = []
        self._walk(obj, out, oids)
        text = "".join(out).encode(self._tx.encoding)
        if not oids:
            return text, _oids.INVALID_OID
        element = oids[0]
        if len(oids) > 1:
            if any(oid not in _INTEGERS for oid in oids):
                raise e.DataError(
                    "cannot dump a list whose elements have different types"
                    f" (type OIDs {', '.join(str(oid) for oid in sorted(oids))})"
                )
            element = max(oids, key=_INTEGERS.index)
        return text, self._tx.adapters.get_array_oid(element)

    def _walk(self, items: list[Any], out: list[str], oids: list[int]) -> None:
        tx = self._tx
        encoding = tx.encoding
        out.append("{")
        first = True
        for item in items:
            if first:
                first = False
            else:
                out.append(",")
            if item is None:
                out.append("NULL")
            elif isinstance(item, list):
                self._walk(item, out, oids)
            else:
                data, oid = tx.get_dumper(type(item)).dump_typed(item)
                if oid not in oids:
                    oids.append(oid)
                text = data.decode(encoding)
                if "\\" in text or '"' in text:
                    text = text.replace("\\", "\\\\").replace('"', '\\"')
                out.append('"' + text + '"')
        out.append("}")


def register_default_adapters(adapters: AdaptersMap) -> None:
    """Register the adapters of this module in a map."""
    adapters.register_dumper(list, ListDumper)
```

### 6.11 `stuntgres/_defaults.py`

The global map: `adapters = AdaptersMap()`, filled when the module is first imported by
calling `register_default_adapters(adapters)` of `numeric`, `string`, `datetime`,
`timestamp`, `interval`, `uuid`, `json`, `array`, `list`, in this order. Afterwards it
holds 16 dumpers, 46 loaders (24 scalar ones, the fallback for OID 0 and `unknown`
included, and 22 array loaders) and 22 array OIDs; `tests/test_defaults.py` checks the
whole table. The module is imported lazily by `adapt.context_adapters(None)`; nothing
imports it at module level except tests.

The complete file:

```python
"""The global adapters map with the adapters of the core types."""
from __future__ import annotations

from .adapt import AdaptersMap
from .types import array, datetime, interval, json, numeric, string, timestamp
from .types import list as list_
from .types import uuid

adapters = AdaptersMap()

for _module in (
    numeric, string, datetime, timestamp, interval, uuid, json, array, list_
):
    _module.register_default_adapters(adapters)
del _module
```

## 7. PEP 249 type objects and constructors (`stuntgres/dbapi20.py`)

```python
import datetime as dt

Buffer = Union[bytes, bytearray, memoryview]

class DBAPITypeObject:
    def __init__(self, name: str, oids: tuple[int, ...]) -> None: ...
    def __repr__(self) -> str: ...
    def __eq__(self, other: Any) -> bool: ...
    def __ne__(self, other: Any) -> bool: ...
    __hash__ = None

STRING: DBAPITypeObject
BINARY: DBAPITypeObject
NUMBER: DBAPITypeObject
DATETIME: DBAPITypeObject
ROWID: DBAPITypeObject

def Date(year: int, month: int, day: int) -> dt.date: ...
def Time(hour: int, minute: int, second: int) -> dt.time: ...
def Timestamp(
    year: int, month: int, day: int, hour: int, minute: int, second: int
) -> dt.datetime: ...
def DateFromTicks(ticks: float) -> dt.date: ...
def TimeFromTicks(ticks: float) -> dt.time: ...
def TimestampFromTicks(ticks: float) -> dt.datetime: ...
def Binary(value: Buffer) -> bytes: ...
```

**Type objects.** PEP 249 asks for objects that compare equal to the `type_code` of the
columns they describe. The `type_code` of this driver is the type OID
(CPYTHON-DESIGN-DBAPI), so a type object is equal to each OID of its group:

| Object | OIDs (types) |
|---|---|
| `STRING` | 25, 1043, 1042, 19 (text, varchar, bpchar, name) |
| `BINARY` | 17 (bytea) |
| `NUMBER` | 21, 23, 20, 700, 701, 1700 (int2, int4, int8, float4, float8, numeric) |
| `DATETIME` | 1082, 1083, 1266, 1114, 1184, 1186 (date, time, timetz, timestamp, timestamptz, interval) |
| `ROWID` | 26 (oid) |

`bool`, `uuid`, `json` and `jsonb` belong to no group (PEP 249 has none for them).

| Member | Algorithm |
|---|---|
| `__init__(name, oids)` | `self.name = name`; `self.oids = frozenset(oids)`. |
| `__repr__()` | `f"stuntgres.{self.name}"`. |
| `__eq__(other)` | An `int`: `other in self.oids`. Anything else: `NotImplemented` (so `STRING == STRING` is identity and `STRING == "text"` is `False`). |
| `__ne__(other)` | An `int`: `other not in self.oids`. Anything else: `NotImplemented`. |
| `__hash__` | `None`: the objects are not hashable, because equal objects (an OID and its group) could not have equal hashes. |

**Constructors.** `Date`, `Time` and `Timestamp` build `datetime.date`, `datetime.time`
and a naive `datetime.datetime` from their arguments. The `FromTicks` functions are the
definitions of PEP 249: `Date(*time.localtime(ticks)[:3])`,
`Time(*time.localtime(ticks)[3:6])`, `Timestamp(*time.localtime(ticks)[:6])`: local
time of the process, fractions of a second dropped, naive results. `Binary(value)` is
`bytes(value)`, which the `bytes` dumper sends as `bytea`.

The names are PEP 249's, hence the capitals (`# noqa: N802` on each function). The module
surface re-exports all twelve names (CPYTHON-DESIGN-DBAPI).

The complete file:

```python
"""PEP 249 type objects and constructors."""
from __future__ import annotations

import datetime as dt
import time
from typing import Any, Union

from . import _oids

Buffer = Union[bytes, bytearray, memoryview]


class DBAPITypeObject:
    """A PEP 249 type object: equal to every type OID of its group."""

    def __init__(self, name: str, oids: tuple[int, ...]) -> None:
        self.name = name
        self.oids = frozenset(oids)

    def __repr__(self) -> str:
        return f"stuntgres.{self.name}"

    def __eq__(self, other: Any) -> bool:
        if isinstance(other, int):
            return other in self.oids
        return NotImplemented

    def __ne__(self, other: Any) -> bool:
        if isinstance(other, int):
            return other not in self.oids
        return NotImplemented

    __hash__ = None  # type: ignore[assignment]


STRING = DBAPITypeObject(
    "STRING", (_oids.TEXT_OID, _oids.VARCHAR_OID, _oids.BPCHAR_OID, _oids.NAME_OID)
)
BINARY = DBAPITypeObject("BINARY", (_oids.BYTEA_OID,))
NUMBER = DBAPITypeObject(
    "NUMBER",
    (
        _oids.INT2_OID, _oids.INT4_OID, _oids.INT8_OID,
        _oids.FLOAT4_OID, _oids.FLOAT8_OID, _oids.NUMERIC_OID,
    ),
)
DATETIME = DBAPITypeObject(
    "DATETIME",
    (
        _oids.DATE_OID, _oids.TIME_OID, _oids.TIMETZ_OID,
        _oids.TIMESTAMP_OID, _oids.TIMESTAMPTZ_OID, _oids.INTERVAL_OID,
    ),
)
ROWID = DBAPITypeObject("ROWID", (_oids.OID_OID,))


def Date(year: int, month: int, day: int) -> dt.date:  # noqa: N802
    """Construct a date value."""
    return dt.date(year, month, day)


def Time(hour: int, minute: int, second: int) -> dt.time:  # noqa: N802
    """Construct a time value."""
    return dt.time(hour, minute, second)


def Timestamp(  # noqa: N802
    year: int, month: int, day: int, hour: int, minute: int, second: int
) -> dt.datetime:
    """Construct a timestamp value."""
    return dt.datetime(year, month, day, hour, minute, second)


def DateFromTicks(ticks: float) -> dt.date:  # noqa: N802
    """Construct a date from seconds since the epoch, in local time."""
    return Date(*time.localtime(ticks)[:3])


def TimeFromTicks(ticks: float) -> dt.time:  # noqa: N802
    """Construct a time from seconds since the epoch, in local time."""
    return Time(*time.localtime(ticks)[3:6])


def TimestampFromTicks(ticks: float) -> dt.datetime:  # noqa: N802
    """Construct a timestamp from seconds since the epoch, in local time."""
    return Timestamp(*time.localtime(ticks)[:6])


def Binary(value: Buffer) -> bytes:  # noqa: N802
    """Construct a value that is sent as bytea."""
    return bytes(value)
```

## 8. Steps

Each step is one catalog part. A part edits only its own files (rule C2), writes its
tests first (C7) and must pass `--mode unit`, `--mode oracle` and `--mode server` of the
runner (PQ 9.1); before CPYTHON-TEST-01B-ORACLE and CPYTHON-TEST-01C-SERVER exist the two
database modes exit 3, which the parts that do not depend on them record instead. All
paths are relative to `bindings/cpython/`. The test files below are complete: they ran,
exactly as printed, against the code of sections 2 to 7 in unit, oracle and server mode
(section 9).

### 8.1 Parts

Every part also has `CPYTHON-DESIGN-ADAPT` as a prerequisite. Sizes are lines of code +
lines of tests. `ADAPT-nn` abbreviates `CPYTHON-ADAPT-nn-…`; `BUILD-01`, `PQ-01`,
`PQ-08`, `TEST-01B`, `TEST-01C` are the parts of PQ 10.1.

| Part | Files | Public names | Prerequisites | Size |
|---|---|---|---|---|
| CPYTHON-QUERY-01-CONVERT | `stuntgres/_queries.py`, `tests/test_queries.py` | `ConvertedQuery`, `to_bytes`, `convert`, `bind`, `prepare` | BUILD-01, PQ-01 | 165 + 255 |
| CPYTHON-ADAPT-01A-MAP | `stuntgres/adapt.py`, `stuntgres/types/__init__.py`, `stuntgres/_defaults.py` (stub), `tests/adapt_util.py`, `tests/test_adapt_map.py` | `TEXT`, `BINARY`, `AdaptContext`, `Dumper`, `Loader`, `AdaptersMap`, `context_adapters`, `context_pgconn`, `context_encoding`, `context_parameter`; `FakePGconn`, `FakeConnection` | BUILD-01, PQ-01 | 210 + 225 |
| CPYTHON-ADAPT-01B-TRANSFORMER | `stuntgres/_transformer.py`, `tests/test_transformer.py` | `Transformer`, `RowMaker` | ADAPT-01A | 150 + 265 |
| CPYTHON-ADAPT-02-OIDS | `stuntgres/_oids.py`, `tests/test_oids.py` | the constants, `BUILTIN_TYPES` | BUILD-01 | 80 + 35 |
| CPYTHON-ADAPT-02B-OIDS-CATALOG | `tests/test_oids_catalog.py` | — | ADAPT-02, TEST-01B, TEST-01C | 0 + 30 |
| CPYTHON-ADAPT-03-NUMERIC | `stuntgres/types/numeric.py`, `tests/test_types_numeric.py` | `BoolDumper`, `IntDumper`, `FloatDumper`, `DecimalDumper`, `BoolLoader`, `IntLoader`, `FloatLoader`, `NumericLoader`, `register_default_adapters` | ADAPT-01B, ADAPT-02 | 115 + 170 |
| CPYTHON-ADAPT-04-STRING | `stuntgres/types/string.py`, `tests/test_types_string.py` | `StrDumper`, `StrTextDumper`, `BytesDumper`, `TextLoader`, `ByteaLoader`, `register_default_adapters` | ADAPT-01B, ADAPT-02 | 90 + 120 |
| CPYTHON-ADAPT-05-DATE-TIME | `stuntgres/types/datetime.py`, `tests/test_types_datetime.py` | `DateDumper`, `TimeDumper`, `DateLoader`, `TimeLoader`, `TimetzLoader`, `register_default_adapters` | ADAPT-01B, ADAPT-02 | 140 + 175 |
| CPYTHON-ADAPT-06A-TIMESTAMP | `stuntgres/types/timestamp.py`, `tests/test_types_timestamp.py` | `session_zone`, `DatetimeDumper`, `TimestampLoader`, `TimestamptzLoader`, `register_default_adapters` | ADAPT-01B, ADAPT-02 | 140 + 250 |
| CPYTHON-ADAPT-06B-INTERVAL | `stuntgres/types/interval.py`, `tests/test_types_interval.py` | `TimedeltaDumper`, `IntervalLoader`, `register_default_adapters` | ADAPT-01B, ADAPT-02 | 80 + 150 |
| CPYTHON-ADAPT-07-UUID-JSON | `stuntgres/types/uuid.py`, `stuntgres/types/json.py`, `tests/test_types_uuid.py`, `tests/test_types_json.py` | `UUIDDumper`, `UUIDLoader`; `Json`, `Jsonb`, `JsonDumper`, `JsonbDumper`, `JsonLoader`, `JsonbLoader`, `set_json_dumps`, `set_json_loads`; two `register_default_adapters` | ADAPT-01B, ADAPT-02 | 130 + 190 |
| CPYTHON-ADAPT-08-ARRAY-LOAD | `stuntgres/types/array.py`, `tests/test_types_array.py` | `parse_array`, `ArrayLoader`, `register_array`, `register_default_adapters` | ADAPT-03, ADAPT-04 | 130 + 200 |
| CPYTHON-ADAPT-09-ARRAY-DUMP | `stuntgres/types/list.py`, `tests/test_types_list.py` | `ListDumper`, `register_default_adapters` | ADAPT-08 | 75 + 145 |
| CPYTHON-ADAPT-10-DBAPI20-TYPES | `stuntgres/dbapi20.py`, `tests/test_dbapi20_types.py` | `DBAPITypeObject`, `STRING`, `BINARY`, `NUMBER`, `DATETIME`, `ROWID`, `Date`, `Time`, `Timestamp`, `DateFromTicks`, `TimeFromTicks`, `TimestampFromTicks`, `Binary` | ADAPT-02 | 90 + 100 |
| CPYTHON-ADAPT-11-DEFAULTS | `stuntgres/_defaults.py` (replaces the stub), `tests/test_defaults.py` | `adapters` | ADAPT-03, -04, -05, -06A, -06B, -07, -08, -09 | 15 + 140 |
| CPYTHON-ADAPT-12A-ROUNDTRIP-BASIC | `tests/adapt_db.py`, `tests/test_roundtrip_basic.py` | `AdaptTestCase` | ADAPT-11, CPYTHON-QUERY-01-CONVERT, PQ-08 | 0 + 320 |
| CPYTHON-ADAPT-12B-ROUNDTRIP-DATETIME | `tests/test_roundtrip_datetime.py` | — | ADAPT-12A | 0 + 225 |
| CPYTHON-ADAPT-12C-ROUNDTRIP-COMPOSITE | `tests/test_roundtrip_composite.py` | — | ADAPT-12A, CPYTHON-ENGINE-03-SUBSCRIPT-UNKNOWN | 0 + 230 |
| CPYTHON-ENGINE-03-SUBSCRIPT-UNKNOWN | engine and reference test files, named by the part | — | — | 15 + 60 |

Changes against the list in the part record, with the reason:

- `CPYTHON-ADAPT-01-CORE` is split into `-01A-MAP` (protocols, the map, the context
  functions, the stub of the global map) and `-01B-TRANSFORMER`: together they exceed
  400 lines.
- `CPYTHON-ADAPT-02-OIDS` keeps the constants and a unit test; its catalog check is the
  database part `-02B-OIDS-CATALOG`, so that the type parts, which import the constants,
  need no database part.
- `CPYTHON-ADAPT-06-TIMESTAMP` is split into `-06A-TIMESTAMP` (timestamp, timestamptz)
  and `-06B-INTERVAL` (a module of its own): together they exceed 400 lines.
- `CPYTHON-ADAPT-08` and `-09` depend on the numeric and string parts because their unit
  tests load and dump elements of those types; `-09` depends on `-08` for the array
  OIDs.
- `CPYTHON-ADAPT-12-ROUNDTRIP` is three parts, one test file each; `-12A` also owns the
  shared database test case. `-12C` depends on `CPYTHON-ENGINE-03-SUBSCRIPT-UNKNOWN`:
  one of its tests sends the statement that the stuntgres server cannot answer yet.
- Every database round trip is in the `-12x` parts; the type parts are pure and can
  start as soon as BUILD-01, PQ-01 and their two framework parts are done.

Parts that may run side by side never share a file: after ADAPT-01B and ADAPT-02 the
parts 03, 04, 05, 06A, 06B and 07 each own their module and test file; QUERY-01 and 10
are independent of all of them; 12B and 12C own one test file each.

### 8.2 Stub

CPYTHON-ADAPT-01A-MAP creates `stuntgres/_defaults.py` as this stub, so that
`context_adapters(None)` works before the type modules exist; CPYTHON-ADAPT-11-DEFAULTS
replaces the whole file with the one of 6.11.

```python
"""Stub: replaced by CPYTHON-ADAPT-11-DEFAULTS."""
from __future__ import annotations

from .adapt import AdaptersMap

adapters = AdaptersMap()
```

With the stub the global map is empty: `Transformer()` cannot dump or load anything by
itself, and the unit tests of the parts before ADAPT-11 register the adapters they need
on maps of their own **(measured: the unit tests of QUERY-01, 01A, 01B, 02, 03 to 10
pass with the stub in place)**.

### 8.3 What each part does

For every part: the files, the sections to implement exactly, the step, and the test
file to write first. "Write the module as specified" means: the complete file printed in
the cited section; where a table and the file disagree, stop and report (rule C1).

Notes for every test file: literals with a backslash are Python source, exactly as
printed; non-ASCII characters are written as escapes (`"ć"` is ć, `"\U0001F600"` is
😀); the helper functions `make`, `dump` and `load` at the top of a test file are part of
the file; the type-specific loaders are reached through `Transformer.get_loader`, never
instantiated directly, so that the fallback and the context are exercised too.

#### CPYTHON-QUERY-01-CONVERT

Files: `stuntgres/_queries.py`, `tests/test_queries.py`. Design sections: 2.

Step. Write the test file, see it fail with `ModuleNotFoundError`, then write the module
as specified in 2.6.

Tests to write first (`tests/test_queries.py`, unit mode; 17 cases; every expectation is
a row of experiment 4):

```python
"""Tests of the placeholder converter (stuntgres/_queries.py)."""
from __future__ import annotations

import unittest
from typing import Any

from stuntgres import _queries as q
from stuntgres import errors as e


class QueriesTest(unittest.TestCase):
    """Conversion, parameter checks and the cache; no database."""

    def fails(self, cls: type, message: str, query: Any, params: Any) -> None:
        with self.assertRaises(cls) as caught:
            q.prepare(query, params)
        self.assertIs(type(caught.exception), cls)
        self.assertEqual(str(caught.exception), message)

    def test_without_parameters_the_query_is_unchanged(self) -> None:
        eq = self.assertEqual
        eq(q.prepare("select 1", None), (b"select 1", None))
        eq(q.prepare("select '100%'", None), (b"select '100%'", None))
        eq(
            q.prepare("select '%s', '%(a)s', '%%'", None),
            (b"select '%s', '%(a)s', '%%'", None),
        )
        eq(q.prepare("select 1; select 2", None), (b"select 1; select 2", None))
        eq(q.prepare(b"select 1", None), (b"select 1", None))

    def test_query_encoding(self) -> None:
        eq = self.assertEqual
        eq(q.prepare("select '\u0107'", None), (b"select '\xc4\x87'", None))
        eq(q.prepare("select '\u0107'", None, "latin2"), (b"select '\xe6'", None))
        eq(
            q.prepare("select '\u0107', %s", (1,), "latin2"),
            (b"select '\xe6', $1", (1,)),
        )
        eq(
            q.prepare(b"select %(\xb1)s", {"\u0105": 1}, "latin2"),
            (b"select $1", (1,)),
        )
        eq(q.to_bytes("a"), b"a")
        eq(q.to_bytes(b"\xff"), b"\xff")

    def test_positional(self) -> None:
        eq = self.assertEqual
        eq(q.prepare("select %s", (1,)), (b"select $1", (1,)))
        eq(q.prepare("select %s, %s", (1, "a")), (b"select $1, $2", (1, "a")))
        eq(q.prepare("select %s, %s", [1, 2]), (b"select $1, $2", [1, 2]))
        eq(q.prepare(b"select %s", (1,)), (b"select $1", (1,)))
        eq(
            q.prepare("insert into t values (%s, %s, %s)", (1, None, "x")),
            (b"insert into t values ($1, $2, $3)", (1, None, "x")),
        )
        eq(q.prepare("select 1", ()), (b"select 1", ()))
        eq(q.prepare("select 1", []), (b"select 1", []))

    def test_named(self) -> None:
        eq = self.assertEqual
        eq(q.prepare("select %(a)s", {"a": 1}), (b"select $1", (1,)))
        eq(
            q.prepare("select %(a)s, %(b)s, %(a)s", {"a": 1, "b": 2}),
            (b"select $1, $2, $1", (1, 2)),
        )
        eq(
            q.prepare("select %(b)s, %(a)s", {"a": 1, "b": 2}),
            (b"select $1, $2", (2, 1)),
        )
        eq(q.prepare("select %(a b-c)s", {"a b-c": 1}), (b"select $1", (1,)))
        eq(q.prepare("select %(\u0107)s", {"\u0107": 1}), (b"select $1", (1,)))

    def test_surplus_names_are_ignored(self) -> None:
        eq = self.assertEqual
        eq(q.prepare("select %(a)s", {"a": 1, "zz": 9}), (b"select $1", (1,)))
        eq(q.prepare("select 1", {}), (b"select 1", ()))
        eq(q.prepare("select 1", {"a": 1}), (b"select 1", ()))

    def test_text_format_placeholder(self) -> None:
        eq = self.assertEqual
        eq(q.prepare("select %t", (1,)), (b"select $1", (1,)))
        eq(q.prepare("select %(a)t", {"a": 1}), (b"select $1", (1,)))

    def test_binary_placeholder_is_not_supported(self) -> None:
        self.fails(
            e.NotSupportedError,
            "binary parameters are not supported: '%b' (use 's' instead of 'b')",
            "select %b",
            (1,),
        )
        self.fails(
            e.NotSupportedError,
            "binary parameters are not supported: '%(a)b' (use 's' instead of 'b')",
            "select %(a)b",
            {"a": 1},
        )

    def test_literal_percent(self) -> None:
        eq = self.assertEqual
        eq(q.prepare("select '100%%', %s", (1,)), (b"select '100%', $1", (1,)))
        eq(q.prepare("select 10 %% 3", ()), (b"select 10 % 3", ()))
        eq(q.prepare("select %%s", ()), (b"select %s", ()))
        eq(q.prepare("select %%%s", (1,)), (b"select %$1", (1,)))
        eq(q.prepare("select '%%(a)s'", {}), (b"select '%(a)s'", ()))
        hint = ": use %s or %(name)s for a parameter and %% for a literal percent sign"
        self.fails(
            e.ProgrammingError,
            'unsupported placeholder "%\'"' + hint,
            "select '100%'",
            (),
        )
        self.fails(
            e.ProgrammingError,
            'unsupported placeholder "%\'"' + hint,
            "select %s like 'a%'",
            ("x",),
        )
        self.fails(
            e.ProgrammingError, "unsupported placeholder '% '" + hint,
            "select 10 % 3", (),
        )
        self.fails(
            e.ProgrammingError,
            "incomplete placeholder: '%' at the end of the query"
            " (write '%%' for a literal percent sign)",
            "select 5 %",
            (),
        )

    def test_malformed_placeholders(self) -> None:
        hint = ": use %s or %(name)s for a parameter and %% for a literal percent sign"
        for bad, shown in (("%d", "'%d'"), ("%S", "'%S'"), ("%(a)d", "'%(a)d'")):
            self.fails(
                e.ProgrammingError,
                "unsupported placeholder " + shown + hint,
                "select " + bad,
                (1,),
            )
        self.fails(
            e.ProgrammingError,
            "incomplete placeholder: '%(a)' has no format character",
            "select %(a)",
            {"a": 1},
        )
        self.fails(
            e.ProgrammingError,
            "incomplete placeholder: '%(a' has no ')'",
            "select %(a",
            {"a": 1},
        )
        self.fails(
            e.ProgrammingError,
            "placeholder with an empty name: '%()'",
            "select %()s",
            {"": 1},
        )

    def test_mixed_styles(self) -> None:
        message = "positional (%s) and named (%(name)s) placeholders cannot be mixed"
        self.fails(e.ProgrammingError, message, "select %s, %(a)s", (1,))
        self.fails(e.ProgrammingError, message, "select %(a)s, %s", {"a": 1})

    def test_wrong_number_of_parameters(self) -> None:
        for query, params, text in (
            ("select %s, %s", (1,), "2 placeholders but 1"),
            ("select %s", (1, 2), "1 placeholders but 2"),
            ("select %s", (), "1 placeholders but 0"),
            ("select 1", (1,), "0 placeholders but 1"),
        ):
            self.fails(
                e.ProgrammingError,
                "the query has " + text + " parameters were passed",
                query,
                params,
            )

    def test_missing_names(self) -> None:
        self.fails(
            e.ProgrammingError, "query parameters missing: 'b'",
            "select %(a)s, %(b)s", {"a": 1},
        )
        self.fails(
            e.ProgrammingError, "query parameters missing: 'a', 'c'",
            "select %(a)s, %(b)s, %(c)s", {"b": 1},
        )

    def test_wrong_container(self) -> None:
        self.fails(
            e.ProgrammingError,
            "the query has positional placeholders (%s)"
            " but the parameters are a mapping",
            "select %s",
            {"a": 1},
        )
        for params in ((1,), [1]):
            self.fails(
                e.ProgrammingError,
                "the query has named placeholders (%(name)s)"
                " but the parameters are a sequence",
                "select %(a)s",
                params,
            )
        for params, name in (("x", "str"), (b"x", "bytes"), (5, "int"), ({1}, "set")):
            self.fails(
                TypeError,
                "query parameters must be a sequence or a mapping, got " + name,
                "select %s",
                params,
            )

    def test_query_type(self) -> None:
        cases = ((5, "int"), (None, "NoneType"), (bytearray(b"x"), "bytearray"))
        for query, name in cases:
            self.fails(
                TypeError, "the query must be str or bytes, got " + name, query, None
            )

    def test_sql_is_not_parsed(self) -> None:
        eq = self.assertEqual
        eq(q.prepare("select '%s'", ("x",)), (b"select '$1'", ("x",)))
        eq(q.prepare('select 1 as "%s"', ("x",)), (b'select 1 as "$1"', ("x",)))
        eq(q.prepare("select 1 -- %s", ("x",)), (b"select 1 -- $1", ("x",)))
        eq(q.prepare("select 1 /* %s */", ("x",)), (b"select 1 /* $1 */", ("x",)))
        eq(q.prepare("select $$%s$$", ("x",)), (b"select $$$1$$", ("x",)))
        eq(
            q.prepare("select $q$ %(a)s $q$, %(a)s", {"a": "x"}),
            (b"select $q$ $1 $q$, $1", ("x",)),
        )
        eq(q.prepare("select %s0", (1,)), (b"select $10", (1,)))
        eq(q.prepare("select $1, %s", (5,)), (b"select $1, $1", (5,)))

    def test_convert_and_bind(self) -> None:
        eq = self.assertEqual
        eq(q.convert(b"select %s, %s"), q.ConvertedQuery(b"select $1, $2", None, 2))
        eq(
            q.convert(b"select %(a)s, %(b)s, %(a)s"),
            q.ConvertedQuery(b"select $1, $2, $1", ("a", "b"), 2),
        )
        eq(q.convert(b"select 1"), q.ConvertedQuery(b"select 1", None, 0))
        params = [1, 2]
        self.assertIs(q.bind(q.convert(b"select %s, %s"), params), params)

    def test_cache(self) -> None:
        query = b"select %s -- cache"
        self.assertIs(q.convert(query), q.convert(query))
        before = q._convert_cached.cache_info()
        big = b"select " + b", ".join([b"%s"] * 2000)
        self.assertGreater(len(big), q.MAX_CACHED_LENGTH)
        self.assertEqual(q.convert(big).count, 2000)
        self.assertEqual(q.convert(big).count, 2000)
        after = q._convert_cached.cache_info()
        self.assertEqual((after.hits, after.misses), (before.hits, before.misses))
        self.assertEqual(after.maxsize, 512)
```

#### CPYTHON-ADAPT-01A-MAP

Files: `stuntgres/adapt.py`, `stuntgres/types/__init__.py`, `stuntgres/_defaults.py` (the
stub of 8.2), `tests/adapt_util.py`, `tests/test_adapt_map.py`. Design sections: 3.1 to
3.4, 8.2.

Step. Write `tests/adapt_util.py` (3.4) and the test file; then `stuntgres/adapt.py`
(3.4), the one-line `stuntgres/types/__init__.py` and the stub.

Tests to write first (`tests/test_adapt_map.py`, unit mode; 16 cases):

```python
"""Tests of the adaptation framework (stuntgres/adapt.py); no database."""
from __future__ import annotations

import threading
import unittest
from typing import Any

from stuntgres import adapt
from stuntgres import errors as e
from tests.adapt_util import FakeConnection, FakePGconn


class UpperDumper(adapt.Dumper):
    """Dump str in upper case as text."""

    oid = 25

    def dump(self, obj: Any) -> bytes:
        return str(obj).upper().encode()


class OtherDumper(adapt.Dumper):
    """A second dumper, to tell registrations apart."""

    def dump(self, obj: Any) -> bytes:
        return b"other"


class BinaryDumper(OtherDumper):
    """A dumper that declares the binary format."""

    format = adapt.BINARY


class IntLoader(adapt.Loader):
    """Load decimal digits."""

    def load(self, data: bytes) -> int:
        return int(data)


class BinaryLoader(IntLoader):
    """A loader that declares the binary format."""

    format = adapt.BINARY


class Base:
    """A class with a subclass, for the MRO lookup."""


class Child(Base):
    """Subclass of Base."""


class ProtocolTest(unittest.TestCase):
    """Dumper and Loader base classes."""

    def test_constants(self) -> None:
        self.assertEqual((adapt.TEXT, adapt.BINARY), (0, 1))

    def test_dumper(self) -> None:
        dumper = UpperDumper(str, None)
        self.assertIs(dumper.cls, str)
        self.assertIsNone(dumper.context)
        self.assertEqual(dumper.format, 0)
        self.assertEqual(dumper.dump("ab"), b"AB")
        self.assertEqual(dumper.dump_typed("ab"), (b"AB", 25))
        self.assertEqual(OtherDumper(str).dump_typed("x"), (b"other", 0))
        with self.assertRaises(NotImplementedError):
            adapt.Dumper(str).dump("x")

    def test_loader(self) -> None:
        context = adapt.AdaptersMap()
        loader = IntLoader(23, context)
        self.assertEqual(loader.oid, 23)
        self.assertIs(loader.context, context)
        self.assertEqual(loader.format, 0)
        self.assertEqual(loader.load(b"42"), 42)
        with self.assertRaises(NotImplementedError):
            adapt.Loader(23).load(b"1")


class AdaptersMapTest(unittest.TestCase):
    """Registration, lookup and inheritance of AdaptersMap."""

    def test_empty_map(self) -> None:
        m = adapt.AdaptersMap()
        self.assertIsNone(m.get_loader(23))
        self.assertEqual(m.get_array_oid(23), 0)
        with self.assertRaises(e.ProgrammingError) as caught:
            m.get_dumper(dict)
        self.assertEqual(
            str(caught.exception), "cannot adapt type 'dict' as a query parameter"
        )

    def test_map_is_a_context(self) -> None:
        m = adapt.AdaptersMap()
        self.assertIs(m.adapters, m)
        self.assertIsNone(m.connection)
        self.assertIs(adapt.context_adapters(m), m)

    def test_register_and_get(self) -> None:
        m = adapt.AdaptersMap()
        m.register_dumper(str, UpperDumper)
        m.register_loader(23, IntLoader)
        m.register_array_oid(23, 1007)
        self.assertIs(m.get_dumper(str), UpperDumper)
        self.assertIs(m.get_dumper(str, adapt.TEXT), UpperDumper)
        self.assertIs(m.get_loader(23), IntLoader)
        self.assertEqual(m.get_array_oid(23), 1007)
        m.register_dumper(str, OtherDumper)
        self.assertIs(m.get_dumper(str), OtherDumper)

    def test_format_is_part_of_the_key(self) -> None:
        m = adapt.AdaptersMap()
        m.register_dumper(str, BinaryDumper)
        m.register_loader(23, BinaryLoader)
        self.assertIs(m.get_dumper(str, adapt.BINARY), BinaryDumper)
        self.assertIs(m.get_loader(23, adapt.BINARY), BinaryLoader)
        self.assertIsNone(m.get_loader(23))
        with self.assertRaises(e.ProgrammingError):
            m.get_dumper(str)

    def test_lookup_follows_the_mro(self) -> None:
        m = adapt.AdaptersMap()
        m.register_dumper(Base, UpperDumper)
        self.assertIs(m.get_dumper(Child), UpperDumper)
        m.register_dumper(Child, OtherDumper)
        self.assertIs(m.get_dumper(Child), OtherDumper)
        self.assertIs(m.get_dumper(Base), UpperDumper)
        m.register_dumper(int, UpperDumper)
        m.register_dumper(bool, OtherDumper)
        self.assertIs(m.get_dumper(bool), OtherDumper)
        self.assertIs(m.get_dumper(int), UpperDumper)
        m.register_dumper(object, OtherDumper)
        self.assertIs(m.get_dumper(dict), OtherDumper)

    def test_argument_checks(self) -> None:
        m = adapt.AdaptersMap()
        with self.assertRaises(TypeError) as caught:
            m.register_dumper("str", UpperDumper)  # type: ignore[arg-type]
        self.assertEqual(str(caught.exception), "a class expected, got 'str'")
        with self.assertRaises(TypeError) as caught:
            m.register_loader("int4", IntLoader)  # type: ignore[arg-type]
        self.assertEqual(str(caught.exception), "an OID (int) expected, got 'int4'")

    def test_child_inherits_a_snapshot(self) -> None:
        parent = adapt.AdaptersMap()
        parent.register_dumper(str, UpperDumper)
        parent.register_loader(23, IntLoader)
        parent.register_array_oid(23, 1007)
        child = adapt.AdaptersMap(parent)
        self.assertIs(child.get_dumper(str), UpperDumper)
        self.assertIs(child.get_loader(23), IntLoader)
        self.assertEqual(child.get_array_oid(23), 1007)
        self.assertIs(child._dumpers, parent._dumpers)
        child.register_dumper(str, OtherDumper)
        child.register_loader(20, IntLoader)
        child.register_array_oid(20, 1016)
        self.assertIs(parent.get_dumper(str), UpperDumper)
        self.assertIsNone(parent.get_loader(20))
        self.assertEqual(parent.get_array_oid(20), 0)
        parent.register_loader(21, IntLoader)
        self.assertIsNone(child.get_loader(21))
        grandchild = adapt.AdaptersMap(child)
        self.assertIs(grandchild.get_dumper(str), OtherDumper)

    def test_registration_from_threads(self) -> None:
        m = adapt.AdaptersMap()

        def work(start: int) -> None:
            for oid in range(start, start + 200):
                m.register_loader(oid, IntLoader)

        threads = [threading.Thread(target=work, args=(n * 200,)) for n in range(8)]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join()
        self.assertEqual(len(m._loaders), 1600)


class ContextTest(unittest.TestCase):
    """The context helper functions."""

    def test_no_context(self) -> None:
        self.assertIsNone(adapt.context_pgconn(None))
        self.assertEqual(adapt.context_encoding(None), "utf-8")
        self.assertIsNone(adapt.context_parameter(None, b"TimeZone"))
        from stuntgres import _defaults

        self.assertIs(adapt.context_adapters(None), _defaults.adapters)
        self.assertIsInstance(_defaults.adapters, adapt.AdaptersMap)

    def test_map_context_has_no_connection(self) -> None:
        m = adapt.AdaptersMap()
        self.assertIsNone(adapt.context_pgconn(m))
        self.assertEqual(adapt.context_encoding(m), "utf-8")

    def test_connection_context(self) -> None:
        pgconn = FakePGconn({b"TimeZone": b"Europe/Warsaw"}, "iso8859-2")
        conn = FakeConnection(pgconn, adapt.AdaptersMap())
        self.assertIs(adapt.context_pgconn(conn), pgconn)
        self.assertIs(adapt.context_adapters(conn), conn.adapters)
        self.assertEqual(adapt.context_encoding(conn), "iso8859-2")
        self.assertEqual(adapt.context_parameter(conn, b"TimeZone"), b"Europe/Warsaw")
        self.assertIsNone(adapt.context_parameter(conn, b"DateStyle"))

    def test_broken_connection_counts_as_none(self) -> None:
        pgconn = FakePGconn({b"TimeZone": b"UTC"}, "iso8859-2")
        pgconn.status = 1
        conn = FakeConnection(pgconn, adapt.AdaptersMap())
        self.assertIsNone(adapt.context_pgconn(conn))
        self.assertEqual(adapt.context_encoding(conn), "utf-8")
        self.assertIsNone(adapt.context_parameter(conn, b"TimeZone"))

    def test_fake_connection_defaults(self) -> None:
        conn = FakeConnection()
        self.assertIs(conn.connection, conn)
        self.assertEqual(conn.pgconn.status, 0)
        self.assertIsInstance(conn.adapters, adapt.AdaptersMap)
        self.assertIsNot(conn.adapters, adapt.context_adapters(None))
```

#### CPYTHON-ADAPT-01B-TRANSFORMER

Files: `stuntgres/_transformer.py`, `tests/test_transformer.py`. Design sections: 3.5,
3.7.

Step. Write the test file, then the module as specified in 3.5.

Tests to write first (`tests/test_transformer.py`, unit mode; 15 cases). `FakeResult`
stands for `pq.PGresult` and counts the calls of `get_rows`; the adapters at the top
record their instantiations in `MADE`, which is how the tests prove one dumper per class
and one loader per column type:

```python
"""Tests of the Transformer (stuntgres/_transformer.py); no database."""
from __future__ import annotations

import unittest
from typing import Any

from stuntgres import adapt
from stuntgres import errors as e
from stuntgres._transformer import Transformer
from tests.adapt_util import FakeConnection, FakePGconn

MADE: list[str] = []


class FakeResult:
    """What the Transformer reads of a pq.PGresult."""

    def __init__(
        self,
        rows: list[tuple[bytes | None, ...]],
        oids: list[int],
        formats: list[int] | None = None,
    ) -> None:
        self.rows = rows
        self.oids = oids
        self.formats = formats or [0] * len(oids)
        self.get_rows_calls = 0

    @property
    def ntuples(self) -> int:
        """Number of rows."""
        return len(self.rows)

    @property
    def nfields(self) -> int:
        """Number of columns."""
        return len(self.oids)

    def ftype(self, column: int) -> int:
        """Type OID of a column."""
        return self.oids[column]

    def fformat(self, column: int) -> int:
        """Format of a column."""
        return self.formats[column]

    def get_rows(self, first: int, last: int) -> list[tuple[bytes | None, ...]]:
        """Rows first..last-1, clamped like pq.PGresult.get_rows."""
        self.get_rows_calls += 1
        return self.rows[max(first, 0):min(last, len(self.rows))]


class IntDumper(adapt.Dumper):
    """int -> int4, or int8 above 99, to test value-dependent OIDs."""

    oid = 23

    def __init__(self, cls: type, context: adapt.AdaptContext | None = None) -> None:
        super().__init__(cls, context)
        MADE.append("IntDumper")

    def dump(self, obj: Any) -> bytes:
        return b"%d" % obj

    def dump_typed(self, obj: Any) -> tuple[bytes, int]:
        return self.dump(obj), 20 if obj > 99 else 23


class StrDumper(adapt.Dumper):
    """str -> unknown, in the encoding of the context."""

    def __init__(self, cls: type, context: adapt.AdaptContext | None = None) -> None:
        super().__init__(cls, context)
        self.encoding = adapt.context_encoding(context)

    def dump(self, obj: Any) -> bytes:
        return str(obj).encode(self.encoding)


class IntLoader(adapt.Loader):
    """Load decimal digits; counts its instances."""

    def __init__(self, oid: int, context: adapt.AdaptContext | None = None) -> None:
        super().__init__(oid, context)
        MADE.append(f"IntLoader {oid}")

    def load(self, data: bytes) -> int:
        return int(data)


class TextLoader(adapt.Loader):
    """The fallback: decode in the encoding of the context."""

    def __init__(self, oid: int, context: adapt.AdaptContext | None = None) -> None:
        super().__init__(oid, context)
        self.encoding = adapt.context_encoding(context)
        MADE.append(f"TextLoader {oid}")

    def load(self, data: bytes) -> str:
        return data.decode(self.encoding)


def make(encoding: str = "utf-8") -> tuple[Transformer, FakeConnection]:
    """A transformer on a connection with the four adapters above."""
    m = adapt.AdaptersMap()
    m.register_dumper(int, IntDumper)
    m.register_dumper(str, StrDumper)
    m.register_loader(23, IntLoader)
    m.register_loader(20, IntLoader)
    m.register_loader(0, TextLoader)
    conn = FakeConnection(FakePGconn({}, encoding), m)
    return Transformer(conn), conn


class TransformerTest(unittest.TestCase):
    """Dumping parameters and loading rows with fakes."""

    def setUp(self) -> None:
        MADE.clear()

    def test_context(self) -> None:
        tx, conn = make("iso8859-2")
        self.assertIs(tx.adapters, conn.adapters)
        self.assertIs(tx.connection, conn)
        self.assertEqual(tx.encoding, "iso8859-2")
        self.assertIsNone(tx.pgresult)
        self.assertIs(Transformer.from_context(tx), tx)
        other = Transformer.from_context(conn)
        self.assertIsNot(other, tx)
        self.assertIs(other.adapters, conn.adapters)

    def test_without_context_uses_the_global_map(self) -> None:
        tx = Transformer()
        self.assertIs(tx.adapters, adapt.context_adapters(None))
        self.assertIsNone(tx.connection)
        self.assertEqual(tx.encoding, "utf-8")

    def test_map_as_context(self) -> None:
        m = adapt.AdaptersMap()
        tx = Transformer(m)
        self.assertIs(tx.adapters, m)
        self.assertIsNone(tx.connection)

    def test_dump_sequence(self) -> None:
        tx, _ = make()
        self.assertEqual(
            tx.dump_sequence([1, None, "a", 100, 2]),
            ([b"1", None, b"a", b"100", b"2"], [23, 0, 0, 20, 23]),
        )
        self.assertEqual(tx.dump_sequence([]), ([], []))
        self.assertEqual(tx.dump_sequence((None,)), ([None], [0]))
        self.assertEqual(MADE, ["IntDumper"])

    def test_dumper_gets_the_transformer_as_context(self) -> None:
        tx, _ = make("iso8859-2")
        dumper = tx.get_dumper(str)
        self.assertIs(dumper.context, tx)
        self.assertIs(dumper.cls, str)
        self.assertIs(tx.get_dumper(str), dumper)
        self.assertEqual(tx.dump_sequence(["\u0105"]), ([b"\xb1"], [0]))

    def test_dump_unknown_type(self) -> None:
        tx, _ = make()
        with self.assertRaises(e.ProgrammingError) as caught:
            tx.dump_sequence([1, 1.5])
        self.assertEqual(
            str(caught.exception), "cannot adapt type 'float' as a query parameter"
        )

    def test_subclass_gets_its_own_instance(self) -> None:
        tx, _ = make()
        self.assertEqual(tx.dump_sequence([True, 5]), ([b"1", b"5"], [23, 23]))
        self.assertIs(tx.get_dumper(bool).cls, bool)
        self.assertIsNot(tx.get_dumper(bool), tx.get_dumper(int))

    def test_load_rows(self) -> None:
        tx, _ = make()
        result = FakeResult(
            [
                (b"1", b"x", b"10", None),
                (b"2", None, b"20", b"7"),
                (b"3", b"", b"30", b"8"),
            ],
            [23, 25, 23, 20],
        )
        tx.set_pgresult(result)
        self.assertIs(tx.pgresult, result)
        self.assertEqual(MADE, ["IntLoader 23", "TextLoader 25", "IntLoader 20"])
        self.assertEqual(
            tx.load_rows(0, 3), [(1, "x", 10, None), (2, None, 20, 7), (3, "", 30, 8)]
        )
        self.assertEqual(tx.load_rows(1, 2), [(2, None, 20, 7)])
        self.assertEqual(tx.load_rows(2, 99), [(3, "", 30, 8)])
        self.assertEqual(tx.load_rows(3, 3), [])
        self.assertEqual(result.get_rows_calls, 4)
        self.assertEqual(len(MADE), 3)

    def test_load_row(self) -> None:
        tx, _ = make()
        tx.set_pgresult(FakeResult([(b"1",), (b"2",)], [23]))
        self.assertEqual(tx.load_row(1), (2,))
        self.assertIsNone(tx.load_row(2))
        self.assertIsNone(tx.load_row(-1))
        self.assertEqual(tx.load_row(0, list), [1])

    def test_make_row(self) -> None:
        tx, _ = make()
        tx.set_pgresult(FakeResult([(b"1", b"a"), (b"2", None)], [23, 25]))
        rows = tx.load_rows(0, 2, lambda row: dict(zip("xy", row)))
        self.assertEqual(rows, [{"x": 1, "y": "a"}, {"x": 2, "y": None}])

    def test_new_loaders_for_every_result(self) -> None:
        tx, conn = make()
        tx.set_pgresult(FakeResult([(b"a",)], [25]))
        self.assertEqual(tx.load_rows(0, 1), [("a",)])
        conn.pgconn._encoding = "iso8859-2"
        tx.set_pgresult(FakeResult([(b"\xb1",)], [25]))
        self.assertEqual(MADE, ["TextLoader 25", "TextLoader 25"])
        self.assertEqual(tx.load_rows(0, 1), [("\u0105",)])

    def test_no_result(self) -> None:
        tx, _ = make()
        with self.assertRaises(e.InterfaceError) as caught:
            tx.load_rows(0, 1)
        self.assertEqual(str(caught.exception), "no result to load rows from")
        tx.set_pgresult(FakeResult([(b"1",)], [23]))
        tx.set_pgresult(None)
        self.assertIsNone(tx.pgresult)
        with self.assertRaises(e.InterfaceError):
            tx.load_row(0)

    def test_result_without_columns(self) -> None:
        tx, _ = make()
        tx.set_pgresult(FakeResult([], []))
        self.assertEqual(tx.load_rows(0, 5), [])

    def test_get_loader_and_fallback(self) -> None:
        tx, _ = make()
        loader = tx.get_loader(23)
        self.assertIsInstance(loader, IntLoader)
        self.assertIs(loader.context, tx)
        self.assertIsNot(tx.get_loader(23), loader)
        fallback = tx.get_loader(600)
        self.assertIsInstance(fallback, TextLoader)
        self.assertEqual(fallback.oid, 600)
        empty = Transformer(adapt.AdaptersMap())
        with self.assertRaises(e.InterfaceError) as caught:
            empty.get_loader(600)
        self.assertEqual(
            str(caught.exception),
            "no loader for the type with OID 600 and no fallback loader",
        )

    def test_load_sequence(self) -> None:
        tx, _ = make()
        tx.set_loader_types([23, 25, 20])
        self.assertEqual(tx.load_sequence([b"1", None, b"5"]), (1, None, 5))
        self.assertEqual(tx.load_sequence((b"2", b"z", None)), (2, "z", None))
        with self.assertRaises(e.InterfaceError) as caught:
            tx.load_sequence([b"1"])
        self.assertEqual(
            str(caught.exception), "cannot load a record of 1 values with 3 loaders"
        )
```

#### CPYTHON-ADAPT-02-OIDS

Files: `stuntgres/_oids.py`, `tests/test_oids.py`. Design sections: 4.

Step. Write the test file, then the module as specified in 4.

Tests to write first (`tests/test_oids.py`, unit mode; 3 cases; `EXPECTED` is the table
of section 4):

```python
"""Tests of the OID constants (stuntgres/_oids.py); no database."""
from __future__ import annotations

import unittest

from stuntgres import _oids

EXPECTED = (
    ("bool", 16, 1000), ("bytea", 17, 1001), ("name", 19, 1003), ("int8", 20, 1016),
    ("int2", 21, 1005), ("int4", 23, 1007), ("text", 25, 1009), ("oid", 26, 1028),
    ("json", 114, 199), ("float4", 700, 1021), ("float8", 701, 1022),
    ("bpchar", 1042, 1014), ("varchar", 1043, 1015), ("date", 1082, 1182),
    ("time", 1083, 1183), ("timestamp", 1114, 1115), ("timestamptz", 1184, 1185),
    ("interval", 1186, 1187), ("timetz", 1266, 1270), ("numeric", 1700, 1231),
    ("uuid", 2950, 2951), ("jsonb", 3802, 3807),
)


class OidsTest(unittest.TestCase):
    """The constants and the table agree with the recorded catalog values."""

    def test_table(self) -> None:
        self.assertEqual(_oids.BUILTIN_TYPES, EXPECTED)

    def test_constants(self) -> None:
        for name, oid, array_oid in EXPECTED:
            self.assertEqual(getattr(_oids, name.upper() + "_OID"), oid)
            self.assertEqual(getattr(_oids, name.upper() + "_ARRAY_OID"), array_oid)
        self.assertEqual(_oids.INVALID_OID, 0)
        self.assertEqual(_oids.UNKNOWN_OID, 705)

    def test_no_duplicates(self) -> None:
        numbers = [oid for _, oid, _ in EXPECTED] + [arr for _, _, arr in EXPECTED]
        self.assertEqual(len(set(numbers)), 44)
```

#### CPYTHON-ADAPT-02B-OIDS-CATALOG

Files: `tests/test_oids_catalog.py`. Design sections: 4.

Step. Write the test file. It uses `support.DatabaseTestCase` and `raw.RawConn` (PQ
9.6), so it needs no `PGconn`. It must pass in oracle and in server mode.

Tests (`tests/test_oids_catalog.py`, database modes; 2 cases):

```python
"""The OID constants against pg_type of the connected server."""
from __future__ import annotations

from stuntgres import _oids
from tests import support
from tests.support import raw


class OidsCatalogTest(support.DatabaseTestCase):
    """pg_type agrees with stuntgres._oids in every database mode."""

    def test_builtin_types(self) -> None:
        conn = raw.RawConn(self.conninfo)
        self.addCleanup(conn.close)
        names = ", ".join(f"'{name}'" for name, _, _ in _oids.BUILTIN_TYPES)
        rows = conn.execute(
            "select typname, oid, typarray, typdelim from pg_type"
            f" where typname in ({names}) order by oid"
        )
        found = tuple((r[0].decode(), int(r[1]), int(r[2])) for r in rows)
        self.assertEqual(found, _oids.BUILTIN_TYPES)
        self.assertEqual({r[3] for r in rows}, {b","})

    def test_unknown(self) -> None:
        conn = raw.RawConn(self.conninfo)
        self.addCleanup(conn.close)
        rows = conn.execute(
            "select oid, typarray from pg_type where typname = 'unknown'"
        )
        self.assertEqual(rows, [(b"705", b"0")])
```

#### CPYTHON-ADAPT-03-NUMERIC

Files: `stuntgres/types/numeric.py`, `tests/test_types_numeric.py`. Design sections:
6.1, 6.2, the rows `bool`, `int`, `float`, `decimal.Decimal` of 5.1.

Step. Write the test file, then the module as specified in 6.2.

Tests to write first (`tests/test_types_numeric.py`, unit mode; 10 cases):

```python
"""Tests of stuntgres/types/numeric.py; no database."""
from __future__ import annotations

import enum
import math
import unittest
from decimal import Decimal
from typing import Any

from stuntgres import adapt
from stuntgres._transformer import Transformer
from stuntgres.types import numeric


class Color(enum.IntEnum):
    """An int subclass."""

    RED = 7


def make() -> Transformer:
    """A transformer whose map holds only the adapters of the module."""
    m = adapt.AdaptersMap()
    numeric.register_default_adapters(m)
    return Transformer(m)


def dump(value: Any) -> tuple[bytes | None, int]:
    """Dump one parameter; return (text, oid)."""
    values, oids = make().dump_sequence([value])
    return values[0], oids[0]


def load(oid: int, data: bytes) -> Any:
    """Load one value of the type with this OID."""
    return make().get_loader(oid).load(data)


class NumericDumpTest(unittest.TestCase):
    """bool, int, float and Decimal as parameters."""

    def test_bool(self) -> None:
        self.assertEqual(dump(True), (b"t", 16))
        self.assertEqual(dump(False), (b"f", 16))

    def test_int_by_magnitude(self) -> None:
        eq = self.assertEqual
        eq(dump(0), (b"0", 23))
        eq(dump(1), (b"1", 23))
        eq(dump(-1), (b"-1", 23))
        eq(dump(32768), (b"32768", 23))
        eq(dump(2**31 - 1), (b"2147483647", 23))
        eq(dump(-(2**31)), (b"-2147483648", 23))
        eq(dump(2**31), (b"2147483648", 20))
        eq(dump(-(2**31) - 1), (b"-2147483649", 20))
        eq(dump(2**63 - 1), (b"9223372036854775807", 20))
        eq(dump(-(2**63)), (b"-9223372036854775808", 20))
        eq(dump(2**63), (b"9223372036854775808", 1700))
        eq(dump(-(2**63) - 1), (b"-9223372036854775809", 1700))
        eq(dump(2**70), (b"1180591620717411303424", 1700))

    def test_int_subclass(self) -> None:
        self.assertEqual(dump(Color.RED), (b"7", 23))
        tx = make()
        self.assertEqual(tx.get_dumper(int).dump(5), b"5")
        self.assertEqual(tx.get_dumper(int).oid, 23)

    def test_float(self) -> None:
        eq = self.assertEqual
        eq(dump(1.5), (b"1.5", 701))
        eq(dump(0.0), (b"0.0", 701))
        eq(dump(-0.0), (b"-0.0", 701))
        eq(dump(0.1), (b"0.1", 701))
        eq(dump(1e16), (b"1e+16", 701))
        eq(dump(1e-5), (b"1e-05", 701))
        eq(dump(1e300), (b"1e+300", 701))
        eq(dump(5e-324), (b"5e-324", 701))
        eq(dump(1.7976931348623157e308), (b"1.7976931348623157e+308", 701))
        eq(dump(float("nan")), (b"NaN", 701))
        eq(dump(float("inf")), (b"Infinity", 701))
        eq(dump(float("-inf")), (b"-Infinity", 701))

    def test_decimal(self) -> None:
        eq = self.assertEqual
        eq(dump(Decimal("1.50")), (b"1.50", 1700))
        eq(
            dump(Decimal("12345678901234567890.123456789")),
            (b"12345678901234567890.123456789", 1700),
        )
        eq(dump(Decimal("-0")), (b"-0", 1700))
        eq(dump(Decimal("1E+2")), (b"1E+2", 1700))
        eq(dump(Decimal("0E-7")), (b"0E-7", 1700))
        eq(dump(Decimal("1E-30")), (b"1E-30", 1700))
        eq(dump(Decimal("NaN")), (b"NaN", 1700))
        eq(dump(Decimal("sNaN")), (b"NaN", 1700))
        eq(dump(Decimal("-NaN")), (b"NaN", 1700))
        eq(dump(Decimal("Infinity")), (b"Infinity", 1700))
        eq(dump(Decimal("-Infinity")), (b"-Infinity", 1700))


class NumericLoadTest(unittest.TestCase):
    """The text PostgreSQL 18.6 sends, loaded."""

    def test_bool(self) -> None:
        self.assertIs(load(16, b"t"), True)
        self.assertIs(load(16, b"f"), False)

    def test_integers(self) -> None:
        eq = self.assertEqual
        eq(load(21, b"-32768"), -32768)
        eq(load(21, b"32767"), 32767)
        eq(load(23, b"-2147483648"), -2147483648)
        eq(load(23, b"2147483647"), 2147483647)
        eq(load(20, b"-9223372036854775808"), -9223372036854775808)
        eq(load(20, b"9223372036854775807"), 9223372036854775807)
        eq(load(26, b"0"), 0)
        eq(load(26, b"4294967295"), 4294967295)
        self.assertIs(type(load(23, b"0")), int)

    def test_floats(self) -> None:
        eq = self.assertEqual
        eq(load(700, b"1.5"), 1.5)
        eq(load(700, b"3.4028235e+38"), 3.4028235e38)
        eq(load(700, b"1e-45"), 1e-45)
        eq(load(700, b"1.6777216e+07"), 16777216.0)
        eq(load(701, b"0"), 0.0)
        eq(load(701, b"1.7976931348623157e+308"), 1.7976931348623157e308)
        eq(load(701, b"5e-324"), 5e-324)
        eq(load(701, b"1e-05"), 1e-05)
        eq(load(701, b"0.3333333333333333"), 0.3333333333333333)
        eq(load(701, b"1.2345678901234568e+17"), 1.2345678901234568e17)
        eq(load(701, b"Infinity"), math.inf)
        eq(load(701, b"-Infinity"), -math.inf)
        self.assertTrue(math.isnan(load(701, b"NaN")))
        self.assertTrue(math.isnan(load(700, b"NaN")))
        self.assertEqual(math.copysign(1.0, load(701, b"-0")), -1.0)
        self.assertIs(type(load(701, b"0")), float)

    def test_numeric(self) -> None:
        eq = self.assertEqual
        for text in (
            "0", "0.0", "0.00", "1", "-1", "1.50", "-123.4500", "9007199254740993",
            "123456789012345678901234567890.123456789012345678901234567890",
            "0.00000000000000000001", "100000000000000000000", "Infinity", "-Infinity",
        ):
            value = load(1700, text.encode())
            self.assertIs(type(value), Decimal)
            eq(format(value, "f"), text)
        eq(load(1700, b"1.50").as_tuple().exponent, -2)
        self.assertTrue(load(1700, b"NaN").is_nan())
        eq(load(1700, b"12345678901234567890.123456789") % 1, Decimal("0.123456789"))

    def test_registration(self) -> None:
        m = adapt.AdaptersMap()
        numeric.register_default_adapters(m)
        eq = self.assertIs
        eq(m.get_dumper(bool), numeric.BoolDumper)
        eq(m.get_dumper(int), numeric.IntDumper)
        eq(m.get_dumper(float), numeric.FloatDumper)
        eq(m.get_dumper(Decimal), numeric.DecimalDumper)
        eq(m.get_loader(16), numeric.BoolLoader)
        for oid in (21, 23, 20, 26):
            eq(m.get_loader(oid), numeric.IntLoader)
        eq(m.get_loader(700), numeric.FloatLoader)
        eq(m.get_loader(701), numeric.FloatLoader)
        eq(m.get_loader(1700), numeric.NumericLoader)
        self.assertIsNone(m.get_loader(25))
```

#### CPYTHON-ADAPT-04-STRING

Files: `stuntgres/types/string.py`, `tests/test_types_string.py`. Design sections: 6.1,
6.3, the rows `str`, `bytes`, `bytearray`, `memoryview` of 5.1.

Step. Write the test file, then the module as specified in 6.3.

Tests to write first (`tests/test_types_string.py`, unit mode; 8 cases). `ESCAPED_ALL`
is the escape output PostgreSQL 18.6 printed for the bytes 0 to 255:

```python
"""Tests of stuntgres/types/string.py; no database."""
from __future__ import annotations

import unittest
from typing import Any

from stuntgres import adapt
from stuntgres import errors as e
from stuntgres._transformer import Transformer
from stuntgres.types import string
from tests.adapt_util import FakeConnection, FakePGconn

ALL_BYTES = bytes(range(256))
ESCAPED_ALL = (
    b"".join(b"\\%03o" % n for n in range(32))
    + b" !\"#$%&'()*+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\\\]^_`"
    + b"abcdefghijklmnopqrstuvwxyz{|}~"
    + b"".join(b"\\%03o" % n for n in range(127, 256))
)


def make(encoding: str = "utf-8") -> Transformer:
    """A transformer with the adapters of the module and a client encoding."""
    m = adapt.AdaptersMap()
    string.register_default_adapters(m)
    return Transformer(FakeConnection(FakePGconn({}, encoding), m))


def dump(value: Any, encoding: str = "utf-8") -> tuple[bytes | None, int]:
    """Dump one parameter; return (text, oid)."""
    values, oids = make(encoding).dump_sequence([value])
    return values[0], oids[0]


def load(oid: int, data: bytes, encoding: str = "utf-8") -> Any:
    """Load one value of the type with this OID."""
    return make(encoding).get_loader(oid).load(data)


class StringTest(unittest.TestCase):
    """str parameters and the text types."""

    def test_dump_str(self) -> None:
        eq = self.assertEqual
        eq(dump("abc"), (b"abc", 0))
        eq(dump(""), (b"", 0))
        eq(dump("it's \"q\" \\ back"), (b"it's \"q\" \\ back", 0))
        eq(dump("\u0107 \U0001F600"), (b"\xc4\x87 \xf0\x9f\x98\x80", 0))
        eq(dump("\u0105\u0107", "iso8859-2"), (b"\xb1\xe6", 0))

    def test_dump_str_errors(self) -> None:
        with self.assertRaises(e.DataError) as caught:
            dump("a\x00b")
        self.assertEqual(
            str(caught.exception),
            "PostgreSQL text cannot contain NUL (0x00) characters",
        )
        with self.assertRaises(UnicodeEncodeError):
            dump("\U0001F600", "iso8859-2")

    def test_str_text_dumper(self) -> None:
        m = adapt.AdaptersMap()
        string.register_default_adapters(m)
        m.register_dumper(str, string.StrTextDumper)
        self.assertEqual(Transformer(m).dump_sequence(["a"]), ([b"a"], [25]))

    def test_load_text(self) -> None:
        eq = self.assertEqual
        for oid in (25, 1043, 19, 705, 0, 600, 869):
            eq(load(oid, b"abc"), "abc")
        eq(load(25, b""), "")
        eq(load(25, b"line1\nline2\ttab"), "line1\nline2\ttab")
        eq(load(25, b"\xf0\x9f\x98\x80 \xc4\x87"), "\U0001F600 \u0107")
        eq(load(1042, b"ab   "), "ab   ")
        eq(load(25, b"\xb1\xe6", "iso8859-2"), "\u0105\u0107")
        with self.assertRaises(UnicodeDecodeError):
            load(25, b"\xff")


class ByteaTest(unittest.TestCase):
    """bytes-like parameters and both output formats of bytea."""

    def test_dump(self) -> None:
        eq = self.assertEqual
        eq(dump(b"\x00\xff"), (b"\\x00ff", 17))
        eq(dump(b""), (b"\\x", 17))
        eq(dump(bytearray(b"ab\\c'd")), (b"\\x61625c632764", 17))
        eq(dump(memoryview(b"\x01\x02")), (b"\\x0102", 17))
        eq(dump(ALL_BYTES), (b"\\x" + ALL_BYTES.hex().encode(), 17))

    def test_load_hex(self) -> None:
        eq = self.assertEqual
        eq(load(17, b"\\x"), b"")
        eq(load(17, b"\\x00ff"), b"\x00\xff")
        eq(load(17, b"\\xdeadbeef"), b"\xde\xad\xbe\xef")
        eq(load(17, b"\\x" + ALL_BYTES.hex().encode()), ALL_BYTES)
        self.assertIs(type(load(17, b"\\x00")), bytes)

    def test_load_escape(self) -> None:
        eq = self.assertEqual
        eq(load(17, b""), b"")
        eq(load(17, b"abc"), b"abc")
        eq(load(17, b"x"), b"x")
        eq(load(17, b"\\000\\377"), b"\x00\xff")
        eq(load(17, b"\\000\\001\\177\\200\\377\\\\'"), b"\x00\x01\x7f\x80\xff\\'")
        eq(load(17, b"\\\\\\\\"), b"\\\\")
        eq(load(17, b"\\\\x"), b"\\x")
        eq(load(17, ESCAPED_ALL), ALL_BYTES)

    def test_registration(self) -> None:
        m = adapt.AdaptersMap()
        string.register_default_adapters(m)
        same = self.assertIs
        same(m.get_dumper(str), string.StrDumper)
        for cls in (bytes, bytearray, memoryview):
            same(m.get_dumper(cls), string.BytesDumper)
        for oid in (0, 25, 1043, 1042, 19, 705):
            same(m.get_loader(oid), string.TextLoader)
        same(m.get_loader(17), string.ByteaLoader)
```

#### CPYTHON-ADAPT-05-DATE-TIME

Files: `stuntgres/types/datetime.py`, `tests/test_types_datetime.py`. Design sections:
6.1, 6.4, the rows `datetime.date` and `datetime.time` of 5.1.

Step. Write the test file, then the module as specified in 6.4. Inside the module the
standard library is imported as `import datetime as dt` (1.3).

Tests to write first (`tests/test_types_datetime.py`, unit mode; 10 cases):

```python
"""Tests of stuntgres/types/datetime.py (date, time, timetz); no database."""
from __future__ import annotations

import datetime as dt
import unittest
from typing import Any

from stuntgres import adapt
from stuntgres import errors as e
from stuntgres._transformer import Transformer
from stuntgres.types import datetime as module
from tests.adapt_util import FakeConnection, FakePGconn

RANGE = " is out of the range Python can represent"


class NoOffset(dt.tzinfo):
    """A time zone that gives no offset without a date."""

    def utcoffset(self, when: dt.datetime | None) -> dt.timedelta | None:
        return None

    def __repr__(self) -> str:
        return "NoOffset()"


def make(**parameters: bytes) -> Transformer:
    """A transformer with the adapters of the module and session parameters."""
    m = adapt.AdaptersMap()
    module.register_default_adapters(m)
    session = {name.encode(): value for name, value in parameters.items()}
    return Transformer(FakeConnection(FakePGconn(session), m))


def dump(value: Any) -> tuple[bytes | None, int]:
    """Dump one parameter; return (text, oid)."""
    values, oids = make().dump_sequence([value])
    return values[0], oids[0]


def load(oid: int, data: bytes, **parameters: bytes) -> Any:
    """Load one value of the type with this OID."""
    return make(**parameters).get_loader(oid).load(data)


def offset(**fields: int) -> dt.timezone:
    """A fixed-offset time zone."""
    return dt.timezone(dt.timedelta(**fields))


class DateTimeTest(unittest.TestCase):
    """date, time and timetz in both directions."""

    def fails(self, message: str, oid: int, data: bytes, **parameters: bytes) -> None:
        with self.assertRaises(e.DataError) as caught:
            load(oid, data, **parameters)
        self.assertEqual(str(caught.exception), message)

    def test_dump_date(self) -> None:
        eq = self.assertEqual
        eq(dump(dt.date(2000, 1, 2)), (b"2000-01-02", 1082))
        eq(dump(dt.date(1, 1, 1)), (b"0001-01-01", 1082))
        eq(dump(dt.date(99, 1, 2)), (b"0099-01-02", 1082))
        eq(dump(dt.date(9999, 12, 31)), (b"9999-12-31", 1082))

    def test_dump_time(self) -> None:
        eq = self.assertEqual
        eq(dump(dt.time(3, 4, 5, 6)), (b"03:04:05.000006", 1083))
        eq(dump(dt.time(3, 4, 5)), (b"03:04:05", 1083))
        eq(dump(dt.time(0, 0)), (b"00:00:00", 1083))
        eq(dump(dt.time(23, 59, 59, 999999)), (b"23:59:59.999999", 1083))

    def test_dump_timetz(self) -> None:
        eq = self.assertEqual
        tz = offset(hours=5, minutes=30)
        eq(dump(dt.time(3, 4, 5, 6, tzinfo=tz)), (b"03:04:05.000006+05:30", 1266))
        eq(dump(dt.time(3, 4, 5, tzinfo=offset(hours=-8))), (b"03:04:05-08:00", 1266))
        eq(dump(dt.time(3, 4, 5, tzinfo=dt.timezone.utc)), (b"03:04:05+00:00", 1266))
        tz = offset(hours=1, minutes=2, seconds=3)
        eq(dump(dt.time(3, 4, 5, tzinfo=tz)), (b"03:04:05+01:02:03", 1266))

    def test_dump_time_without_fixed_offset(self) -> None:
        with self.assertRaises(e.DataError) as caught:
            dump(dt.time(1, 2, tzinfo=NoOffset()))
        self.assertEqual(
            str(caught.exception),
            "cannot dump the time datetime.time(1, 2, tzinfo=NoOffset()):"
            " its time zone has no fixed UTC offset",
        )

    def test_load_date(self) -> None:
        eq = self.assertEqual
        eq(load(1082, b"2000-01-01"), dt.date(2000, 1, 1))
        eq(load(1082, b"0001-01-01"), dt.date(1, 1, 1))
        eq(load(1082, b"0099-07-04"), dt.date(99, 7, 4))
        eq(load(1082, b"2024-02-29"), dt.date(2024, 2, 29))
        eq(load(1082, b"9999-12-31"), dt.date(9999, 12, 31))
        eq(load(1082, b"2000-01-02", DateStyle=b"ISO, DMY"), dt.date(2000, 1, 2))
        self.assertIs(type(load(1082, b"2000-01-01")), dt.date)

    def test_load_date_out_of_range(self) -> None:
        for text in (
            b"infinity", b"-infinity", b"0001-12-31 BC", b"0044-03-15 BC",
            b"4713-01-01 BC", b"10000-01-01", b"5874897-12-31",
        ):
            self.fails(f"date value {text.decode()!r}" + RANGE, 1082, text)

    def test_load_date_other_styles(self) -> None:
        only = ", only ISO is supported"
        for text, style, shown in (
            (b"01/02/2000", b"SQL, MDY", "'SQL, MDY'"),
            (b"02/01/2000", b"SQL, DMY", "'SQL, DMY'"),
            (b"01-02-2000", b"Postgres, MDY", "'Postgres, MDY'"),
            (b"02.01.2000", b"German, DMY", "'German, DMY'"),
            (b"15.03.0044 BC", b"German, DMY", "'German, DMY'"),
        ):
            self.fails(
                f"cannot parse date value {text.decode()!r}:"
                f" the session's DateStyle is {shown}" + only,
                1082, text, DateStyle=style,
            )
        self.fails("cannot parse date value 'junk'", 1082, b"junk")
        self.fails(
            "cannot parse date value 'junk'", 1082, b"junk", DateStyle=b"ISO, MDY"
        )

    def test_load_time(self) -> None:
        eq = self.assertEqual
        eq(load(1083, b"00:00:00"), dt.time(0, 0))
        eq(load(1083, b"23:59:59.999999"), dt.time(23, 59, 59, 999999))
        eq(load(1083, b"12:34:56.7"), dt.time(12, 34, 56, 700000))
        eq(load(1083, b"12:34:56.123456"), dt.time(12, 34, 56, 123456))
        eq(load(1083, b"12:34:56.0001"), dt.time(12, 34, 56, 100))
        value = load(1083, b"03:04:05.6", DateStyle=b"German, DMY")
        eq(value, dt.time(3, 4, 5, 600000))
        self.assertIsNone(load(1083, b"00:00:00").tzinfo)
        self.fails("time value '24:00:00'" + RANGE, 1083, b"24:00:00")
        self.fails("cannot parse time value '12:34'", 1083, b"12:34")

    def test_load_timetz(self) -> None:
        eq = self.assertEqual
        eq(load(1266, b"00:00:00+00"), dt.time(0, 0, tzinfo=dt.timezone.utc))
        eq(
            load(1266, b"12:34:56.789+05:30"),
            dt.time(12, 34, 56, 789000, tzinfo=offset(hours=5, minutes=30)),
        )
        eq(load(1266, b"12:00:00-08"), dt.time(12, 0, tzinfo=offset(hours=-8)))
        eq(
            load(1266, b"12:00:00+01:02:03"),
            dt.time(12, 0, tzinfo=offset(hours=1, minutes=2, seconds=3)),
        )
        eq(
            load(1266, b"23:59:59.999999-15:59"),
            dt.time(23, 59, 59, 999999, tzinfo=offset(hours=-15, minutes=-59)),
        )
        eq(
            load(1266, b"12:00:00+15:59:59").utcoffset(),
            dt.timedelta(hours=15, minutes=59, seconds=59),
        )
        eq(load(1266, b"12:00:00-00:00:30").utcoffset(), dt.timedelta(seconds=-30))
        eq(load(1266, b"12:00:00-08").utcoffset(), dt.timedelta(hours=-8))
        self.fails("timetz value '24:00:00-15:59'" + RANGE, 1266, b"24:00:00-15:59")
        self.fails("cannot parse timetz value '12:00:00'", 1266, b"12:00:00")

    def test_registration(self) -> None:
        m = adapt.AdaptersMap()
        module.register_default_adapters(m)
        same = self.assertIs
        same(m.get_dumper(dt.date), module.DateDumper)
        same(m.get_dumper(dt.time), module.TimeDumper)
        same(m.get_loader(1082), module.DateLoader)
        same(m.get_loader(1083), module.TimeLoader)
        same(m.get_loader(1266), module.TimetzLoader)
        self.assertIsNone(m.get_loader(1114))
```

#### CPYTHON-ADAPT-06A-TIMESTAMP

Files: `stuntgres/types/timestamp.py`, `tests/test_types_timestamp.py`. Design sections:
6.1, 6.5, the rows `datetime.datetime` of 5.1.

Step. Write the test file, then the module as specified in 6.5.

Tests to write first (`tests/test_types_timestamp.py`, unit mode; 12 cases). Three cases
need the time zone `Europe/Warsaw` in Python's zone database and skip themselves with
`self.skipTest` when it is absent (an operating system without tzdata); that skip is
allowed here because the fallback path is what the other cases test:

```python
"""Tests of stuntgres/types/timestamp.py; no database."""
from __future__ import annotations

import datetime as dt
import unittest
import zoneinfo
from typing import Any

from stuntgres import adapt
from stuntgres import errors as e
from stuntgres._transformer import Transformer
from stuntgres.types import timestamp as module
from tests.adapt_util import FakeConnection, FakePGconn

RANGE = " is out of the range Python can represent"
UTC = dt.timezone.utc


def make(**parameters: bytes) -> Transformer:
    """A transformer with the adapters of the module and session parameters."""
    m = adapt.AdaptersMap()
    module.register_default_adapters(m)
    session = {name.encode(): value for name, value in parameters.items()}
    return Transformer(FakeConnection(FakePGconn(session), m))


def dump(value: Any) -> tuple[bytes | None, int]:
    """Dump one parameter; return (text, oid)."""
    values, oids = make().dump_sequence([value])
    return values[0], oids[0]


def load(oid: int, data: bytes, **parameters: bytes) -> Any:
    """Load one value of the type with this OID."""
    return make(**parameters).get_loader(oid).load(data)


def offset(**fields: int) -> dt.timezone:
    """A fixed-offset time zone."""
    return dt.timezone(dt.timedelta(**fields))


def have_zone(name: str) -> bool:
    """Tell whether this Python has the time zone database entry."""
    try:
        zoneinfo.ZoneInfo(name)
    except (KeyError, ValueError, OSError):
        return False
    return True


class TimestampTest(unittest.TestCase):
    """datetime parameters and the timestamp type."""

    def fails(self, message: str, oid: int, data: bytes, **parameters: bytes) -> None:
        with self.assertRaises(e.DataError) as caught:
            load(oid, data, **parameters)
        self.assertEqual(str(caught.exception), message)

    def test_dump_naive(self) -> None:
        eq = self.assertEqual
        eq(
            dump(dt.datetime(2000, 1, 2, 3, 4, 5, 6)),
            (b"2000-01-02 03:04:05.000006", 1114),
        )
        eq(dump(dt.datetime(2000, 1, 2, 3, 4, 5)), (b"2000-01-02 03:04:05", 1114))
        eq(dump(dt.datetime(1, 1, 1)), (b"0001-01-01 00:00:00", 1114))
        eq(dump(dt.datetime(99, 7, 4, 1, 2, 3)), (b"0099-07-04 01:02:03", 1114))
        eq(
            dump(dt.datetime(9999, 12, 31, 23, 59, 59, 999999)),
            (b"9999-12-31 23:59:59.999999", 1114),
        )

    def test_dump_aware(self) -> None:
        eq = self.assertEqual
        tz = offset(hours=5, minutes=30)
        eq(
            dump(dt.datetime(2000, 1, 2, 3, 4, 5, 6, tzinfo=tz)),
            (b"2000-01-02 03:04:05.000006+05:30", 1184),
        )
        eq(
            dump(dt.datetime(2000, 1, 2, 3, 4, 5, tzinfo=UTC)),
            (b"2000-01-02 03:04:05+00:00", 1184),
        )
        eq(
            dump(dt.datetime(2000, 1, 2, 3, 4, 5, tzinfo=offset(hours=-8))),
            (b"2000-01-02 03:04:05-08:00", 1184),
        )
        tz = offset(hours=1, minutes=2, seconds=3)
        eq(
            dump(dt.datetime(2000, 1, 2, 3, 4, 5, tzinfo=tz)),
            (b"2000-01-02 03:04:05+01:02:03", 1184),
        )

    def test_load_timestamp(self) -> None:
        eq = self.assertEqual
        eq(load(1114, b"2000-01-01 00:00:00"), dt.datetime(2000, 1, 1))
        eq(
            load(1114, b"2000-01-01 12:34:56.123456"),
            dt.datetime(2000, 1, 1, 12, 34, 56, 123456),
        )
        eq(
            load(1114, b"2000-01-01 12:34:56.5"),
            dt.datetime(2000, 1, 1, 12, 34, 56, 500000),
        )
        eq(
            load(1114, b"2000-01-01 12:34:56.0001"),
            dt.datetime(2000, 1, 1, 12, 34, 56, 100),
        )
        eq(load(1114, b"0001-01-01 00:00:00"), dt.datetime(1, 1, 1))
        eq(load(1114, b"0099-07-04 01:02:03"), dt.datetime(99, 7, 4, 1, 2, 3))
        eq(
            load(1114, b"9999-12-31 23:59:59.999999"),
            dt.datetime(9999, 12, 31, 23, 59, 59, 999999),
        )
        self.assertIsNone(load(1114, b"2000-01-01 00:00:00").tzinfo)

    def test_load_timestamp_errors(self) -> None:
        for text in (
            b"infinity", b"-infinity", b"0001-12-31 23:59:59 BC",
            b"4713-11-24 00:00:00 BC", b"10000-01-01 00:00:00",
            b"294276-12-31 23:59:59.999999",
        ):
            self.fails(f"timestamp value {text.decode()!r}" + RANGE, 1114, text)
        only = ", only ISO is supported"
        for text, style in (
            (b"01/02/2000 03:04:05.6", b"SQL, MDY"),
            (b"Sun Jan 02 03:04:05.6 2000", b"Postgres, MDY"),
            (b"02.01.2000 03:04:05.6", b"German, DMY"),
        ):
            self.fails(
                f"cannot parse timestamp value {text.decode()!r}: the session's"
                f" DateStyle is {style.decode()!r}" + only,
                1114, text, DateStyle=style,
            )
        self.fails("cannot parse timestamp value '2000-01-01'", 1114, b"2000-01-01")


class TimestamptzTest(unittest.TestCase):
    """The timestamptz type and the session's time zone."""

    def fails(self, message: str, data: bytes, **parameters: bytes) -> None:
        with self.assertRaises(e.DataError) as caught:
            load(1184, data, **parameters)
        self.assertEqual(str(caught.exception), message)

    def test_without_session_zone(self) -> None:
        eq = self.assertEqual
        value = load(1184, b"2000-01-01 00:00:00+00")
        eq(value, dt.datetime(2000, 1, 1, tzinfo=UTC))
        self.assertIs(value.tzinfo, UTC)
        value = load(1184, b"2000-07-01 18:04:56.123456+05:30")
        eq(value, dt.datetime(2000, 7, 1, 12, 34, 56, 123456, tzinfo=UTC))
        eq(value.utcoffset(), dt.timedelta(hours=5, minutes=30))
        eq((value.hour, value.minute, value.microsecond), (18, 4, 123456))
        value = load(1184, b"1899-12-31 20:29:08-03:30:52")
        eq(value, dt.datetime(1900, 1, 1, tzinfo=UTC))
        eq(value.utcoffset(), -dt.timedelta(hours=3, minutes=30, seconds=52))
        value = load(1184, b"1999-12-31 12:00:00-12")
        eq(value, dt.datetime(2000, 1, 1, tzinfo=UTC))
        eq(load(1184, b"0001-01-01 00:00:00+00"), dt.datetime(1, 1, 1, tzinfo=UTC))
        eq(
            load(1184, b"9999-12-31 23:59:59.999999+00"),
            dt.datetime(9999, 12, 31, 23, 59, 59, 999999, tzinfo=UTC),
        )
        value = load(1184, b"0001-01-01 00:19:32+00:19:32")
        eq(value.utcoffset(), dt.timedelta(minutes=19, seconds=32))
        eq((value.year, value.hour, value.minute, value.second), (1, 0, 19, 32))

    def test_zone_python_does_not_know(self) -> None:
        for name in (b"<+0530>-5:30", b"<+03>-03", b"-03:30", b"No/Such_Zone", b""):
            value = load(1184, b"2000-01-01 05:30:00+05:30", TimeZone=name)
            self.assertEqual(value, dt.datetime(2000, 1, 1, tzinfo=UTC))
            self.assertEqual(value.tzinfo, offset(hours=5, minutes=30))
        self.assertIsNone(module.session_zone(None))
        self.assertIsNone(module.session_zone(b"\xff"))
        self.assertIsNone(module.session_zone(b"../etc/passwd"))

    def test_session_zone(self) -> None:
        if not have_zone("Europe/Warsaw"):
            self.skipTest("this Python has no time zone database")
        eq = self.assertEqual
        warsaw = zoneinfo.ZoneInfo("Europe/Warsaw")
        self.assertIs(module.session_zone(b"Europe/Warsaw"), warsaw)
        value = load(1184, b"2000-07-01 14:34:56.123456+02", TimeZone=b"Europe/Warsaw")
        eq(value, dt.datetime(2000, 7, 1, 12, 34, 56, 123456, tzinfo=UTC))
        self.assertIs(value.tzinfo, warsaw)
        eq((value.hour, value.utcoffset()), (14, dt.timedelta(hours=2)))
        value = load(1184, b"2000-01-01 01:00:00+01", TimeZone=b"Europe/Warsaw")
        eq(value, dt.datetime(2000, 1, 1, tzinfo=UTC))
        self.assertIs(value.tzinfo, warsaw)
        value = load(1184, b"1900-01-01 01:24:00+01:24", TimeZone=b"Europe/Warsaw")
        eq(value, dt.datetime(1900, 1, 1, tzinfo=UTC))
        self.assertIs(value.tzinfo, warsaw)

    def test_repeated_hour_sets_fold(self) -> None:
        if not have_zone("Europe/Warsaw"):
            self.skipTest("this Python has no time zone database")
        eq = self.assertEqual
        first = load(1184, b"2021-10-31 02:30:00+02", TimeZone=b"Europe/Warsaw")
        second = load(1184, b"2021-10-31 02:30:00+01", TimeZone=b"Europe/Warsaw")
        eq((first.fold, first.utcoffset()), (0, dt.timedelta(hours=2)))
        eq((second.fold, second.utcoffset()), (1, dt.timedelta(hours=1)))
        eq(first.astimezone(UTC), dt.datetime(2021, 10, 31, 0, 30, tzinfo=UTC))
        eq(second.astimezone(UTC), dt.datetime(2021, 10, 31, 1, 30, tzinfo=UTC))
        self.assertIsInstance(second.tzinfo, zoneinfo.ZoneInfo)

    def test_offset_python_disagrees_with(self) -> None:
        if not have_zone("Europe/Warsaw"):
            self.skipTest("this Python has no time zone database")
        value = load(1184, b"2000-07-01 13:00:00+01", TimeZone=b"Europe/Warsaw")
        self.assertEqual(value, dt.datetime(2000, 7, 1, 12, tzinfo=UTC))
        self.assertEqual(value.tzinfo, offset(hours=1))

    def test_out_of_range(self) -> None:
        for text in (
            b"infinity", b"-infinity", b"0001-12-31 23:59:59+00 BC",
            b"0001-12-31 20:29:08-03:30:52 BC", b"4714-11-24 00:00:00+00 BC",
            b"10000-01-01 05:29:59.999999+05:30", b"294276-12-31 23:59:59.999999+00",
        ):
            self.fails(f"timestamptz value {text.decode()!r}" + RANGE, text)

    def test_other_styles(self) -> None:
        only = ", only ISO is supported"
        for text, style in (
            (b"01/02/2000 03:04:05.6 UTC", b"SQL, MDY"),
            (b"Sun Jan 02 03:04:05.6 2000 UTC", b"Postgres, MDY"),
            (b"02.01.2000 03:04:05.6 UTC", b"German, DMY"),
        ):
            self.fails(
                f"cannot parse timestamptz value {text.decode()!r}: the session's"
                f" DateStyle is {style.decode()!r}" + only,
                text, DateStyle=style,
            )
        self.fails(
            "cannot parse timestamptz value '2000-01-01 00:00:00'",
            b"2000-01-01 00:00:00",
        )

    def test_registration(self) -> None:
        m = adapt.AdaptersMap()
        module.register_default_adapters(m)
        self.assertIs(m.get_dumper(dt.datetime), module.DatetimeDumper)
        self.assertIs(m.get_loader(1114), module.TimestampLoader)
        self.assertIs(m.get_loader(1184), module.TimestamptzLoader)
        self.assertIsNone(m.get_loader(1186))
```

#### CPYTHON-ADAPT-06B-INTERVAL

Files: `stuntgres/types/interval.py`, `tests/test_types_interval.py`. Design sections:
6.1, 6.6, the row `datetime.timedelta` of 5.1.

Step. Write the test file, then the module as specified in 6.6.

Tests to write first (`tests/test_types_interval.py`, unit mode; 7 cases):

```python
"""Tests of stuntgres/types/interval.py; no database."""
from __future__ import annotations

import datetime as dt
import unittest
from typing import Any

from stuntgres import adapt
from stuntgres import errors as e
from stuntgres._transformer import Transformer
from stuntgres.types import interval as module
from tests.adapt_util import FakeConnection, FakePGconn

RANGE = " is out of the range Python can represent"
TD = dt.timedelta


def make(**parameters: bytes) -> Transformer:
    """A transformer with the adapters of the module and session parameters."""
    m = adapt.AdaptersMap()
    module.register_default_adapters(m)
    session = {name.encode(): value for name, value in parameters.items()}
    return Transformer(FakeConnection(FakePGconn(session), m))


def dump(value: Any) -> tuple[bytes | None, int]:
    """Dump one parameter; return (text, oid)."""
    values, oids = make().dump_sequence([value])
    return values[0], oids[0]


def load(data: bytes, **parameters: bytes) -> Any:
    """Load one interval value."""
    return make(**parameters).get_loader(1186).load(data)


class IntervalTest(unittest.TestCase):
    """timedelta parameters and the interval type."""

    def fails(self, message: str, data: bytes, **parameters: bytes) -> None:
        with self.assertRaises(e.DataError) as caught:
            load(data, **parameters)
        self.assertEqual(str(caught.exception), message)

    def test_dump(self) -> None:
        eq = self.assertEqual
        eq(
            dump(TD(days=1, seconds=2, microseconds=3)),
            (b"+1 days +2 seconds +3 microseconds", 1186),
        )
        eq(dump(TD(0)), (b"+0 days +0 seconds +0 microseconds", 1186))
        eq(
            dump(TD(microseconds=-1)),
            (b"-1 days +86399 seconds +999999 microseconds", 1186),
        )
        eq(dump(TD(days=-1, seconds=5)), (b"-1 days +5 seconds +0 microseconds", 1186))
        eq(dump(TD(hours=36)), (b"+1 days +43200 seconds +0 microseconds", 1186))
        eq(dump(TD(minutes=-90)), (b"-1 days +81000 seconds +0 microseconds", 1186))
        eq(
            dump(TD.max),
            (b"+999999999 days +86399 seconds +999999 microseconds", 1186),
        )
        eq(dump(TD.min), (b"-999999999 days +0 seconds +0 microseconds", 1186))

    def test_load(self) -> None:
        eq = self.assertEqual
        eq(load(b"00:00:00"), TD(0))
        eq(load(b"1 day"), TD(days=1))
        eq(load(b"2 days"), TD(days=2))
        eq(load(b"-1 days"), TD(days=-1))
        eq(load(b"7 days"), TD(days=7))
        eq(load(b"00:00:01"), TD(seconds=1))
        eq(load(b"-00:00:01"), TD(seconds=-1))
        eq(load(b"01:00:00"), TD(hours=1))
        eq(load(b"100:00:00"), TD(hours=100))
        eq(load(b"00:00:00.000001"), TD(microseconds=1))
        eq(load(b"-00:00:00.000001"), TD(microseconds=-1))
        eq(load(b"1 day 00:00:00.5"), TD(days=1, microseconds=500000))
        eq(load(b"1 day 12:00:00"), TD(days=1, hours=12))
        eq(load(b"1 day -00:00:01"), TD(days=1, seconds=-1))
        eq(load(b"3 days -04:05:06"), TD(days=3, hours=-4, minutes=-5, seconds=-6))
        eq(load(b"999999999 days"), TD(days=999999999))
        eq(load(b"-999999999 days"), TD(days=-999999999))
        eq(
            load(b"2562047788:00:54.775807"),
            TD(hours=2562047788, seconds=54, microseconds=775807),
        )
        eq(load(b"1 day", IntervalStyle=b"postgres"), TD(days=1))
        self.assertIs(type(load(b"1 day")), TD)

    def test_load_months_and_years(self) -> None:
        eq = self.assertEqual
        eq(load(b"1 year"), TD(days=365))
        eq(load(b"2 years"), TD(days=730))
        eq(load(b"-1 years"), TD(days=-365))
        eq(load(b"1 mon"), TD(days=30))
        eq(load(b"2 mons"), TD(days=60))
        eq(load(b"11 mons"), TD(days=330))
        eq(load(b"-10 mons"), TD(days=-300))
        eq(load(b"1 year 2 mons"), TD(days=425))
        eq(load(b"1 mon 1 day"), TD(days=31))
        eq(load(b"-1 mons +1 day"), TD(days=-29))
        eq(load(b"1 mon -1 days +00:00:01"), TD(days=29, seconds=1))
        eq(
            load(b"1 year 2 mons 3 days 04:05:06.789"),
            TD(days=428, hours=4, minutes=5, seconds=6, microseconds=789000),
        )
        eq(
            load(b"-1 years -2 mons -3 days -04:05:06.789"),
            -TD(days=428, hours=4, minutes=5, seconds=6, microseconds=789000),
        )

    def test_out_of_range(self) -> None:
        for text in (
            b"infinity", b"-infinity", b"1000000000 days", b"-1000000000 days",
            b"178000000 years", b"-178000000 years",
        ):
            self.fails(f"interval value {text.decode()!r}" + RANGE, text)

    def test_other_styles(self) -> None:
        only = ", only postgres is supported"
        for text, style in (
            (b"+1-2 +3 +4:05:06.789", b"sql_standard"),
            (b"0", b"sql_standard"),
            (b"0-1", b"sql_standard"),
            (b"2 0:00:00", b"sql_standard"),
            (b"@ 1 year 2 mons 3 days 4 hours 5 mins 6.789 secs", b"postgres_verbose"),
            (b"@ 0", b"postgres_verbose"),
            (b"P1Y2M3DT4H5M6.789S", b"iso_8601"),
            (b"PT0S", b"iso_8601"),
        ):
            self.fails(
                f"cannot parse interval value {text.decode()!r}: the session's"
                f" IntervalStyle is {style.decode()!r}" + only,
                text, IntervalStyle=style,
            )
        self.fails("cannot parse interval value ''", b"")
        self.fails("cannot parse interval value 'junk'", b"junk")

    def test_sql_standard_clock_values_parse(self) -> None:
        eq = self.assertEqual
        eq(load(b"100:00:00", IntervalStyle=b"sql_standard"), TD(hours=100))
        eq(load(b"0:00:00.000001", IntervalStyle=b"sql_standard"), TD(microseconds=1))
        eq(load(b"-0:00:01", IntervalStyle=b"sql_standard"), TD(seconds=-1))

    def test_registration(self) -> None:
        m = adapt.AdaptersMap()
        module.register_default_adapters(m)
        self.assertIs(m.get_dumper(TD), module.TimedeltaDumper)
        self.assertIs(m.get_loader(1186), module.IntervalLoader)
```

#### CPYTHON-ADAPT-07-UUID-JSON

Files: `stuntgres/types/uuid.py`, `stuntgres/types/json.py`, `tests/test_types_uuid.py`,
`tests/test_types_json.py`. Design sections: 6.1, 6.7, 6.8, the rows `uuid.UUID`, `Json`,
`Jsonb` of 5.1.

Step. Write the two test files, then the two modules as specified in 6.7 and 6.8.

Tests to write first (`tests/test_types_uuid.py`, unit mode; 3 cases):

```python
"""Tests of stuntgres/types/uuid.py; no database."""
from __future__ import annotations

import unittest
import uuid

from stuntgres import adapt
from stuntgres._transformer import Transformer
from stuntgres.types import uuid as module

U = uuid.UUID("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")


def make() -> Transformer:
    """A transformer whose map holds only the adapters of the module."""
    m = adapt.AdaptersMap()
    module.register_default_adapters(m)
    return Transformer(m)


class UUIDTest(unittest.TestCase):
    """uuid.UUID in both directions."""

    def test_dump(self) -> None:
        eq = self.assertEqual
        eq(
            make().dump_sequence([U]),
            ([b"a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"], [2950]),
        )
        eq(
            make().dump_sequence([uuid.UUID(int=0)]),
            ([b"00000000-0000-0000-0000-000000000000"], [2950]),
        )

    def test_load(self) -> None:
        eq = self.assertEqual
        loader = make().get_loader(2950)
        eq(loader.load(b"a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"), U)
        eq(loader.load(b"00000000-0000-0000-0000-000000000000"), uuid.UUID(int=0))
        eq(
            loader.load(b"ffffffff-ffff-ffff-ffff-ffffffffffff"),
            uuid.UUID(int=2**128 - 1),
        )
        self.assertIs(
            type(loader.load(b"a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")),
            uuid.UUID,
        )

    def test_registration(self) -> None:
        m = adapt.AdaptersMap()
        module.register_default_adapters(m)
        self.assertIs(m.get_dumper(uuid.UUID), module.UUIDDumper)
        self.assertIs(m.get_loader(2950), module.UUIDLoader)
```

`tests/test_types_json.py` (unit mode; 8 cases):

```python
"""Tests of stuntgres/types/json.py; no database."""
from __future__ import annotations

import json
import unittest
from decimal import Decimal
from typing import Any

from stuntgres import adapt
from stuntgres import errors as e
from stuntgres._transformer import Transformer
from stuntgres.types import json as module
from stuntgres.types.json import Json, Jsonb
from tests.adapt_util import FakeConnection, FakePGconn


def connection(encoding: str = "utf-8") -> FakeConnection:
    """A connection whose map holds only the adapters of the module."""
    m = adapt.AdaptersMap()
    module.register_default_adapters(m)
    return FakeConnection(FakePGconn({}, encoding), m)


def dump(value: Any, encoding: str = "utf-8") -> tuple[bytes | None, int]:
    """Dump one parameter; return (text, oid)."""
    values, oids = Transformer(connection(encoding)).dump_sequence([value])
    return values[0], oids[0]


def load(oid: int, data: bytes, encoding: str = "utf-8") -> Any:
    """Load one json (114) or jsonb (3802) value."""
    return Transformer(connection(encoding)).get_loader(oid).load(data)


class JsonTest(unittest.TestCase):
    """The wrappers Json and Jsonb, and the json and jsonb types."""

    def test_wrappers(self) -> None:
        value = {"a": 1}
        wrapped = Json(value)
        self.assertIs(wrapped.obj, value)
        self.assertIsNone(wrapped.dumps)
        self.assertEqual(repr(wrapped), "Json({'a': 1})")
        self.assertEqual(repr(Jsonb([1])), "Jsonb([1])")
        self.assertIsInstance(Jsonb(1), Json)
        self.assertIs(Json(1, dumps=str).dumps, str)
        with self.assertRaises(AttributeError):
            wrapped.other = 1  # type: ignore[attr-defined]

    def test_dump(self) -> None:
        eq = self.assertEqual
        value = {"a": [1, None, "x"], "b": {"c": 1.5}, "u": "\u0107\U0001F600"}
        text = (
            b'{"a": [1, null, "x"], "b": {"c": 1.5}, "u": "\\u0107\\ud83d\\ude00"}'
        )
        eq(dump(Json(value)), (text, 114))
        eq(dump(Jsonb(value)), (text, 3802))
        eq(dump(Jsonb(None)), (b"null", 3802))
        eq(dump(Jsonb(5)), (b"5", 3802))
        eq(dump(Json([1, "a", None, True])), (b'[1, "a", null, true]', 114))
        eq(dump(Json("x")), (b'"x"', 114))

    def test_dump_with_own_function(self) -> None:
        eq = self.assertEqual

        def compact(obj: Any) -> str:
            return json.dumps(obj, separators=(",", ":"), ensure_ascii=False)

        eq(dump(Jsonb({"a": "\u0105"}, dumps=compact)), (b'{"a":"\xc4\x85"}', 3802))
        eq(
            dump(Json({"a": "\u0105"}, dumps=compact), "iso8859-2"),
            (b'{"a":"\xb1"}', 114),
        )
        eq(dump(Json(1, dumps=lambda obj: b"[1]")), (b"[1]", 114))
        eq(dump(Json(1, dumps=lambda obj: bytearray(b"[2]"))), (b"[2]", 114))

    def test_dict_needs_a_wrapper(self) -> None:
        with self.assertRaises(e.ProgrammingError) as caught:
            dump({"a": 1})
        self.assertEqual(
            str(caught.exception), "cannot adapt type 'dict' as a query parameter"
        )

    def test_load(self) -> None:
        eq = self.assertEqual
        text = b'{"a": 1, "b": [1, 2, null],   "c": "x"}'
        eq(load(114, text), {"a": 1, "b": [1, 2, None], "c": "x"})
        eq(load(3802, b'{"a": 1, "b": [1, 2, null], "c": "x"}'), load(114, text))
        self.assertIsNone(load(3802, b"null"))
        eq(load(3802, b"1"), 1)
        eq(load(3802, b"1.0"), 1.0)
        eq(load(3802, b'"s"'), "s")
        eq(load(3802, b"[]"), [])
        eq(load(3802, b"{}"), {})
        self.assertIs(load(3802, b"true"), True)
        eq(load(3802, b'{"a": 1.10}'), {"a": 1.1})
        eq(load(3802, b'"\xc3\xa9"'), "\u00e9")
        eq(load(3802, b'"\xf0\x9f\x98\x80"'), "\U0001F600")
        eq(load(114, b'"\\u00e9"'), "\u00e9")
        eq(load(3802, b'"\xb1"', "iso8859-2"), "\u0105")
        eq(load(3802, b"1" + b"0" * 100), 10**100)

    def test_set_json_loads(self) -> None:
        conn = connection()
        module.set_json_loads(lambda text: json.loads(text, parse_float=Decimal), conn)
        tx = Transformer(conn)
        self.assertEqual(
            tx.get_loader(3802).load(b'{"a": 1.10}'),
            {"a": Decimal("1.10")},
        )
        self.assertEqual(tx.get_loader(114).load(b"[1.5]"), [Decimal("1.5")])
        loader = conn.adapters.get_loader(3802)
        self.assertTrue(issubclass(loader, module.JsonbLoader))
        self.assertEqual(loader.__name__, "JsonbLoader")
        self.assertEqual(load(3802, b"1.5"), 1.5)

    def test_set_json_dumps(self) -> None:
        conn = connection()
        module.set_json_dumps(lambda obj: json.dumps(obj, sort_keys=True), conn)
        tx = Transformer(conn)
        self.assertEqual(
            tx.dump_sequence([Json({"b": 1, "a": 2}), Jsonb({"b": 1, "a": 2})]),
            ([b'{"a": 2, "b": 1}', b'{"a": 2, "b": 1}'], [114, 3802]),
        )
        self.assertEqual(
            tx.dump_sequence([Json({"b": 1, "a": 2}, dumps=str)]),
            ([b"{'b': 1, 'a': 2}"], [114]),
        )
        self.assertEqual(dump(Json({"b": 1, "a": 2})), (b'{"b": 1, "a": 2}', 114))

    def test_registration(self) -> None:
        m = adapt.AdaptersMap()
        module.register_default_adapters(m)
        self.assertIs(m.get_dumper(Json), module.JsonDumper)
        self.assertIs(m.get_dumper(Jsonb), module.JsonbDumper)
        self.assertIs(m.get_loader(114), module.JsonLoader)
        self.assertIs(m.get_loader(3802), module.JsonbLoader)
```

#### CPYTHON-ADAPT-08-ARRAY-LOAD

Files: `stuntgres/types/array.py`, `tests/test_types_array.py`. Design sections: 6.1,
6.9.

Step. Write the test file, then the module as specified in 6.9.

Tests to write first (`tests/test_types_array.py`, unit mode; 12 cases). `QUOTED` is the
text PostgreSQL 18.6 printed for the array of 6.9:

```python
"""Tests of stuntgres/types/array.py; no database."""
from __future__ import annotations

import unittest
from decimal import Decimal
from typing import Any

from stuntgres import adapt
from stuntgres import errors as e
from stuntgres._transformer import Transformer
from stuntgres.types import array, numeric, string
from stuntgres.types.array import parse_array
from tests.adapt_util import FakeConnection, FakePGconn

# The text of experiment 1 for a text[] with every character that needs quotes.
QUOTED = (
    '{a,"","NULL","null","nuLL","a b"," a","a ","a,b","a\\"b","a\\\\b","{}","a{b",'
    '"x}y","new\nline","tab\t","cr\r","vt\x0b","ff\x0c",NULL,\u0107,\U0001F600,a;b,'
    "a'b,a=b,[1],\" \"}"
)
QUOTED_ITEMS = [
    "a", "", "NULL", "null", "nuLL", "a b", " a", "a ", "a,b", 'a"b', "a\\b", "{}",
    "a{b", "x}y", "new\nline", "tab\t", "cr\r", "vt\x0b", "ff\x0c", None, "\u0107",
    "\U0001F600", "a;b", "a'b", "a=b", "[1]", " ",
]


def make(encoding: str = "utf-8") -> Transformer:
    """A transformer with numeric, string and array adapters."""
    m = adapt.AdaptersMap()
    numeric.register_default_adapters(m)
    string.register_default_adapters(m)
    array.register_default_adapters(m)
    return Transformer(FakeConnection(FakePGconn({}, encoding), m))


def load(oid: int, data: bytes, encoding: str = "utf-8") -> Any:
    """Load one array value of the array type with this OID."""
    return make(encoding).get_loader(oid).load(data)


class ParseArrayTest(unittest.TestCase):
    """The array text parser."""

    def test_flat(self) -> None:
        eq = self.assertEqual
        eq(parse_array("{}"), [])
        eq(parse_array("{1,2,3}"), ["1", "2", "3"])
        eq(parse_array("{1,NULL,3}"), ["1", None, "3"])
        eq(parse_array("{NULL}"), [None])
        eq(parse_array('{""}'), [""])
        eq(parse_array('{"NULL"}'), ["NULL"])
        eq(parse_array("{nul,nulls}"), ["nul", "nulls"])
        eq(parse_array("{t,f,NULL}"), ["t", "f", None])

    def test_quoting(self) -> None:
        eq = self.assertEqual
        eq(parse_array(QUOTED), QUOTED_ITEMS)
        eq(parse_array('{"a\\"b"}'), ['a"b'])
        eq(parse_array('{"a\\\\b"}'), ["a\\b"])
        eq(parse_array('{"{"}'), ["{"])
        eq(parse_array('{"}"}'), ["}"])
        eq(parse_array('{"\n"}'), ["\n"])
        eq(parse_array('{"\\\\x00ff","\\\\x"}'), ["\\x00ff", "\\x"])
        eq(
            parse_array('{"{\\"a\\": 1}","[1,2]","null",NULL}'),
            ['{"a": 1}', "[1,2]", "null", None],
        )
        eq(
            parse_array('{"2000-01-01 12:34:56",infinity}'),
            ["2000-01-01 12:34:56", "infinity"],
        )
        eq(parse_array('{"a  ","b c"}'), ["a  ", "b c"])

    def test_nested(self) -> None:
        eq = self.assertEqual
        eq(parse_array("{{1,2},{3,4}}"), [["1", "2"], ["3", "4"]])
        eq(parse_array("{{{1},{2}},{{3},{4}}}"), [[["1"], ["2"]], [["3"], ["4"]]])
        eq(parse_array("{{1,NULL},{NULL,4}}"), [["1", None], [None, "4"]])
        eq(parse_array("{{a,b},{c,NULL}}"), [["a", "b"], ["c", None]])

    def test_lower_bounds_are_dropped(self) -> None:
        eq = self.assertEqual
        eq(parse_array("[2:4]={1,2,3}"), ["1", "2", "3"])
        eq(parse_array("[-1:0]={1,2}"), ["1", "2"])
        eq(parse_array("[0:1][2:3]={{1,2},{3,4}}"), [["1", "2"], ["3", "4"]])
        eq(parse_array("[2:2][1:1]={{5}}"), [["5"]])

    def test_delimiter(self) -> None:
        eq = self.assertEqual
        eq(
            parse_array("{(1,1),(0,0);(3,3),(2,2)}", ";"),
            ["(1,1),(0,0)", "(3,3),(2,2)"],
        )
        eq(parse_array("{a;b}"), ["a;b"])
        eq(parse_array("{a;b}", ";"), ["a", "b"])

    def test_convert(self) -> None:
        eq = self.assertEqual
        eq(parse_array("{1,NULL,3}", ",", int), [1, None, 3])
        eq(parse_array("{{1},{2}}", convert=int), [[1], [2]])
        eq(parse_array('{"1"}', convert=int), [1])

    def test_malformed(self) -> None:
        bad = ("", "1,2", "{1,2", "{1,2}}", "{1}{2}", "[1:2]{1,2}", '{"a}', "{}x", ",")
        for text in bad:
            with self.assertRaises(e.DataError) as caught:
                parse_array(text)
            self.assertEqual(
                str(caught.exception), f"malformed array value: {text!r}"
            )
        with self.assertRaises(e.DataError) as caught:
            parse_array("{" + "x" * 100)
        self.assertEqual(
            str(caught.exception), "malformed array value: " + repr("{" + "x" * 59)
        )


class ArrayLoaderTest(unittest.TestCase):
    """Array loaders over the element loaders."""

    def test_core_arrays(self) -> None:
        eq = self.assertEqual
        eq(load(1007, b"{}"), [])
        eq(load(1007, b"{1,2,3}"), [1, 2, 3])
        eq(load(1007, b"{{1,2},{3,4}}"), [[1, 2], [3, 4]])
        eq(load(1007, b"{1,NULL,3}"), [1, None, 3])
        eq(load(1007, b"[2:4]={1,2,3}"), [1, 2, 3])
        eq(load(1005, b"{1,2}"), [1, 2])
        eq(load(1016, b"{1,2}"), [1, 2])
        eq(load(1028, b"{1,2}"), [1, 2])
        eq(load(1000, b"{t,f,NULL}"), [True, False, None])
        eq(load(1022, b"{1.5,Infinity,-Infinity}"), [1.5, float("inf"), float("-inf")])
        eq(load(1021, b"{1.5}"), [1.5])
        eq(
            load(1231, b"{1.50,Infinity,NULL}"),
            [Decimal("1.50"), Decimal("Infinity"), None],
        )
        eq(load(1009, QUOTED.encode()), QUOTED_ITEMS)
        eq(load(1009, b"{{a,b},{c,NULL}}"), [["a", "b"], ["c", None]])
        eq(load(1015, b'{a,"b c"}'), ["a", "b c"])
        eq(load(1014, b'{"a  ","b c"}'), ["a  ", "b c"])
        eq(load(1003, b'{a,"b c"}'), ["a", "b c"])
        eq(load(1001, b'{"\\\\x00ff","\\\\x"}'), [b"\x00\xff", b""])
        eq(load(1001, b'{"\\\\000\\\\377\\\\\\\\",""}'), [b"\x00\xff\\", b""])

    def test_client_encoding(self) -> None:
        self.assertEqual(
            load(1009, b'{\xb1,"\xe6 x"}', "iso8859-2"),
            ["\u0105", "\u0107 x"],
        )

    def test_register_array(self) -> None:
        conn = FakeConnection(adapters=adapt.AdaptersMap())
        string.register_default_adapters(conn.adapters)
        self.assertIsNone(array.register_array(600, 1017, conn))
        array.register_array(603, 1020, conn, ";")
        tx = Transformer(conn)
        self.assertEqual(
            tx.get_loader(1017).load(b'{"(1,2)","(3,4)"}'),
            ["(1,2)", "(3,4)"],
        )
        self.assertEqual(
            tx.get_loader(1020).load(b"{(1,1),(0,0);(3,3),(2,2)}"),
            ["(1,1),(0,0)", "(3,3),(2,2)"],
        )
        cls = conn.adapters.get_loader(1020)
        self.assertTrue(issubclass(cls, array.ArrayLoader))
        self.assertEqual(
            (cls.__name__, cls.base_oid, cls.delimiter),
            ("ArrayLoader_1020", 603, ";"),
        )
        self.assertEqual(conn.adapters.get_array_oid(600), 1017)
        self.assertEqual(adapt.context_adapters(None).get_array_oid(600), 0)

    def test_no_element_loader(self) -> None:
        m = adapt.AdaptersMap()
        array.register_array(23, 1007, m)
        with self.assertRaises(e.InterfaceError) as caught:
            Transformer(m).get_loader(1007)
        self.assertEqual(
            str(caught.exception),
            "no loader for the array element type with OID 23",
        )

    def test_registration(self) -> None:
        m = adapt.AdaptersMap()
        array.register_default_adapters(m)
        pairs = {
            16: 1000, 17: 1001, 19: 1003, 20: 1016, 21: 1005, 23: 1007, 25: 1009,
            26: 1028, 114: 199, 700: 1021, 701: 1022, 1042: 1014, 1043: 1015,
            1082: 1182, 1083: 1183, 1114: 1115, 1184: 1185, 1186: 1187, 1266: 1270,
            1700: 1231, 2950: 2951, 3802: 3807,
        }
        for oid, array_oid in pairs.items():
            cls = m.get_loader(array_oid)
            self.assertTrue(issubclass(cls, array.ArrayLoader))
            self.assertEqual((cls.base_oid, cls.delimiter), (oid, ","))
            self.assertEqual(m.get_array_oid(oid), array_oid)
        self.assertIsNone(m.get_loader(23))
```

#### CPYTHON-ADAPT-09-ARRAY-DUMP

Files: `stuntgres/types/list.py`, `tests/test_types_list.py`. Design sections: 6.10, the
`list` rows of 5.1 and 5.2.

Step. Write the test file, then the module as specified in 6.10.

Tests to write first (`tests/test_types_list.py`, unit mode; 9 cases):

```python
"""Tests of stuntgres/types/list.py; no database."""
from __future__ import annotations

import unittest
from decimal import Decimal
from typing import Any

from stuntgres import adapt
from stuntgres import errors as e
from stuntgres._transformer import Transformer
from stuntgres.types import array, list as module, numeric, string
from tests.adapt_util import FakeConnection, FakePGconn


class Code(int):
    """A user type dumped as "char" (OID 18)."""


class CodeDumper(adapt.Dumper):
    """Code -> "char"."""

    oid = 18

    def dump(self, obj: Any) -> bytes:
        return bytes((obj,))


def make(encoding: str = "utf-8") -> Transformer:
    """A transformer with numeric, string, array and list adapters."""
    m = adapt.AdaptersMap()
    numeric.register_default_adapters(m)
    string.register_default_adapters(m)
    array.register_default_adapters(m)
    module.register_default_adapters(m)
    return Transformer(FakeConnection(FakePGconn({}, encoding), m))


def dump(value: Any, encoding: str = "utf-8") -> tuple[bytes | None, int]:
    """Dump one parameter; return (text, oid)."""
    values, oids = make(encoding).dump_sequence([value])
    return values[0], oids[0]


class ListDumperTest(unittest.TestCase):
    """Python lists as array parameters."""

    def test_integers(self) -> None:
        eq = self.assertEqual
        eq(dump([1, 2, 3]), (b'{"1","2","3"}', 1007))
        eq(dump([1, None, 3]), (b'{"1",NULL,"3"}', 1007))
        eq(dump([[1, 2], [3, 4]]), (b'{{"1","2"},{"3","4"}}', 1007))
        eq(dump([40000, 1]), (b'{"40000","1"}', 1007))
        eq(dump([1, 2**40]), (b'{"1","1099511627776"}', 1016))
        eq(dump([2**40, 1]), (b'{"1099511627776","1"}', 1016))
        eq(dump([1, 2**70]), (b'{"1","1180591620717411303424"}', 1231))
        eq(
            dump([2**70, 2**40, 1]),
            (b'{"1180591620717411303424","1099511627776","1"}', 1231),
        )
        eq(dump([Decimal("1.5"), 2]), (b'{"1.5","2"}', 1231))

    def test_empty_and_null_only(self) -> None:
        eq = self.assertEqual
        eq(dump([]), (b"{}", 0))
        eq(dump([None]), (b"{NULL}", 0))
        eq(dump([None, None]), (b"{NULL,NULL}", 0))
        eq(dump([[]]), (b"{{}}", 0))
        eq(dump([[None], [None]]), (b"{{NULL},{NULL}}", 0))

    def test_strings(self) -> None:
        eq = self.assertEqual
        value = [
            "a", "b c", "", "NULL", 'q"q', "b\\s", "{x}", "a,b", None,
            "\u0107\U0001F600",
        ]
        eq(
            dump(value),
            (
                b'{"a","b c","","NULL","q\\"q","b\\\\s","{x}","a,b",NULL,'
                b'"\xc4\x87\xf0\x9f\x98\x80"}',
                0,
            ),
        )
        eq(dump(["1", "2"]), (b'{"1","2"}', 0))
        eq(dump(["\u0105"], "iso8859-2"), (b'{"\xb1"}', 0))

    def test_other_element_types(self) -> None:
        eq = self.assertEqual
        eq(
            dump([1.5, None, float("nan"), float("inf")]),
            (b'{"1.5",NULL,"NaN","Infinity"}', 1022),
        )
        eq(
            dump([Decimal("1.50"), Decimal("NaN"), None]),
            (b'{"1.50","NaN",NULL}', 1231),
        )
        eq(dump([True, False, None]), (b'{"t","f",NULL}', 1000))
        eq(
            dump([b"\x00\xff", b"", b'a\\b"c', None]),
            (b'{"\\\\x00ff","\\\\x","\\\\x615c622263",NULL}', 1001),
        )
        eq(dump([bytearray(b"a"), b"b"]), (b'{"\\\\x61","\\\\x62"}', 1001))

    def test_mixed_types(self) -> None:
        with self.assertRaises(e.DataError) as caught:
            dump([1, "a"])
        self.assertEqual(
            str(caught.exception),
            "cannot dump a list whose elements have different types"
            " (type OIDs 0, 23)",
        )
        with self.assertRaises(e.DataError):
            dump([1, 1.5])
        with self.assertRaises(e.DataError):
            dump([[1], [True]])

    def test_element_type_without_array_type(self) -> None:
        tx = make()
        tx.adapters.register_dumper(Code, CodeDumper)
        self.assertEqual(tx.dump_sequence([[Code(120), None]]), ([b'{"x",NULL}'], [0]))
        array.register_array(18, 1002, tx.adapters)
        other = Transformer(tx.adapters)
        self.assertEqual(other.dump_sequence([[Code(120)]]), ([b'{"x"}'], [1002]))

    def test_tuple_is_not_a_list(self) -> None:
        with self.assertRaises(e.ProgrammingError) as caught:
            dump((1, 2))
        self.assertEqual(
            str(caught.exception), "cannot adapt type 'tuple' as a query parameter"
        )

    def test_dumper_without_transformer(self) -> None:
        m = make().adapters
        dumper = module.ListDumper(list, FakeConnection(adapters=m))
        self.assertEqual(dumper.dump([1, None]), b'{"1",NULL}')
        self.assertEqual(dumper.dump_typed([1]), (b'{"1"}', 1007))
        self.assertEqual(dumper.oid, 0)

    def test_registration(self) -> None:
        m = adapt.AdaptersMap()
        module.register_default_adapters(m)
        self.assertIs(m.get_dumper(list), module.ListDumper)
```

#### CPYTHON-ADAPT-10-DBAPI20-TYPES

Files: `stuntgres/dbapi20.py`, `tests/test_dbapi20_types.py`. Design sections: 7.

Step. Write the test file, then the module as specified in 7.

Tests to write first (`tests/test_dbapi20_types.py`, unit mode; 6 cases). The
`FromTicks` cases set the environment variable `TZ` to POSIX zone strings (which need no
zone database), call `time.tzset()` and restore both in a cleanup:

```python
"""Tests of the PEP 249 type objects and constructors (stuntgres/dbapi20.py)."""
from __future__ import annotations

import datetime as dt
import os
import time
import unittest

from stuntgres import dbapi20


class TypeObjectsTest(unittest.TestCase):
    """STRING, BINARY, NUMBER, DATETIME and ROWID against type OIDs."""

    def test_membership(self) -> None:
        groups = {
            "STRING": (25, 1043, 1042, 19),
            "BINARY": (17,),
            "NUMBER": (21, 23, 20, 700, 701, 1700),
            "DATETIME": (1082, 1083, 1266, 1114, 1184, 1186),
            "ROWID": (26,),
        }
        every = [oid for oids in groups.values() for oid in oids] + [16, 114, 2950, 0]
        for name, oids in groups.items():
            obj = getattr(dbapi20, name)
            for oid in every:
                self.assertEqual(obj == oid, oid in oids, (name, oid))
                self.assertEqual(oid == obj, oid in oids, (name, oid))
                self.assertEqual(obj != oid, oid not in oids, (name, oid))
            self.assertEqual(obj.oids, frozenset(oids))
            self.assertEqual(obj.name, name)
            self.assertEqual(repr(obj), "stuntgres." + name)

    def test_other_operands(self) -> None:
        self.assertFalse(dbapi20.STRING == "text")
        self.assertTrue(dbapi20.STRING != "text")
        self.assertFalse(dbapi20.STRING == dbapi20.BINARY)
        self.assertTrue(dbapi20.STRING == dbapi20.STRING)
        self.assertFalse(dbapi20.NUMBER == 23.5)
        with self.assertRaises(TypeError):
            hash(dbapi20.STRING)

    def test_description_use(self) -> None:
        type_code = 1043
        self.assertTrue(type_code == dbapi20.STRING)
        self.assertIn(type_code, [dbapi20.NUMBER, dbapi20.STRING])


class ConstructorsTest(unittest.TestCase):
    """Date, Time, Timestamp, their *FromTicks twins and Binary."""

    def setUp(self) -> None:
        saved = os.environ.get("TZ")

        def restore() -> None:
            if saved is None:
                os.environ.pop("TZ", None)
            else:
                os.environ["TZ"] = saved
            time.tzset()

        self.addCleanup(restore)

    def test_plain(self) -> None:
        eq = self.assertEqual
        eq(dbapi20.Date(2000, 1, 2), dt.date(2000, 1, 2))
        eq(dbapi20.Time(3, 4, 5), dt.time(3, 4, 5))
        eq(dbapi20.Timestamp(2000, 1, 2, 3, 4, 5), dt.datetime(2000, 1, 2, 3, 4, 5))
        self.assertIs(type(dbapi20.Date(2000, 1, 2)), dt.date)
        self.assertIsNone(dbapi20.Timestamp(2000, 1, 2, 3, 4, 5).tzinfo)

    def test_from_ticks_use_local_time(self) -> None:
        eq = self.assertEqual
        ticks = 946782245.75  # 2000-01-02 03:04:05.75 UTC
        os.environ["TZ"] = "UTC"
        time.tzset()
        eq(dbapi20.DateFromTicks(ticks), dt.date(2000, 1, 2))
        eq(dbapi20.TimeFromTicks(ticks), dt.time(3, 4, 5))
        eq(dbapi20.TimestampFromTicks(ticks), dt.datetime(2000, 1, 2, 3, 4, 5))
        os.environ["TZ"] = "XYZ-05:30"  # POSIX notation: 5 h 30 min east of UTC
        time.tzset()
        eq(dbapi20.DateFromTicks(ticks), dt.date(2000, 1, 2))
        eq(dbapi20.TimeFromTicks(ticks), dt.time(8, 34, 5))
        eq(dbapi20.TimestampFromTicks(ticks), dt.datetime(2000, 1, 2, 8, 34, 5))
        os.environ["TZ"] = "XYZ+04"  # 4 h west of UTC
        time.tzset()
        eq(dbapi20.DateFromTicks(ticks), dt.date(2000, 1, 1))
        eq(dbapi20.TimestampFromTicks(ticks), dt.datetime(2000, 1, 1, 23, 4, 5))
        eq(dbapi20.TimestampFromTicks(0), dt.datetime(1969, 12, 31, 20, 0, 0))
        self.assertIsNone(dbapi20.TimestampFromTicks(ticks).tzinfo)

    def test_binary(self) -> None:
        eq = self.assertEqual
        eq(dbapi20.Binary(b"\x00\xff"), b"\x00\xff")
        eq(dbapi20.Binary(bytearray(b"ab")), b"ab")
        eq(dbapi20.Binary(memoryview(b"mv")), b"mv")
        eq(dbapi20.Binary(b""), b"")
        self.assertIs(type(dbapi20.Binary(bytearray(b"ab"))), bytes)
```

#### CPYTHON-ADAPT-11-DEFAULTS

Files: `stuntgres/_defaults.py` (replaces the stub of 8.2), `tests/test_defaults.py`.
Design sections: 6.11, 3.2 (inheritance of maps), the whole of 5.1.

Step. Write the test file and see it fail against the stub; then replace the stub with
the file of 6.11.

Tests to write first (`tests/test_defaults.py`, unit mode; 6 cases):

```python
"""Tests of the global adapters map (stuntgres/_defaults.py); no database."""
from __future__ import annotations

import datetime as dt
import decimal
import unittest
import uuid

from stuntgres import _defaults, adapt
from stuntgres._transformer import Transformer
from stuntgres.types import array
from stuntgres.types.json import Json, Jsonb
from tests.adapt_util import FakeConnection

DUMPERS = {
    bool: "BoolDumper", int: "IntDumper", float: "FloatDumper",
    decimal.Decimal: "DecimalDumper", str: "StrDumper", bytes: "BytesDumper",
    bytearray: "BytesDumper", memoryview: "BytesDumper", dt.date: "DateDumper",
    dt.time: "TimeDumper", dt.datetime: "DatetimeDumper",
    dt.timedelta: "TimedeltaDumper", uuid.UUID: "UUIDDumper", Json: "JsonDumper",
    Jsonb: "JsonbDumper", list: "ListDumper",
}
LOADERS = {
    0: "TextLoader", 16: "BoolLoader", 17: "ByteaLoader", 19: "TextLoader",
    20: "IntLoader", 21: "IntLoader", 23: "IntLoader", 25: "TextLoader",
    26: "IntLoader", 114: "JsonLoader", 700: "FloatLoader", 701: "FloatLoader",
    705: "TextLoader", 1042: "TextLoader", 1043: "TextLoader", 1082: "DateLoader",
    1083: "TimeLoader", 1114: "TimestampLoader", 1184: "TimestamptzLoader",
    1186: "IntervalLoader", 1266: "TimetzLoader", 1700: "NumericLoader",
    2950: "UUIDLoader", 3802: "JsonbLoader",
}
ARRAYS = {
    16: 1000, 17: 1001, 19: 1003, 20: 1016, 21: 1005, 23: 1007, 25: 1009, 26: 1028,
    114: 199, 700: 1021, 701: 1022, 1042: 1014, 1043: 1015, 1082: 1182, 1083: 1183,
    1114: 1115, 1184: 1185, 1186: 1187, 1266: 1270, 1700: 1231, 2950: 2951,
    3802: 3807,
}


class DefaultsTest(unittest.TestCase):
    """What the global map holds once every type module is registered."""

    def test_global_map(self) -> None:
        self.assertIsInstance(_defaults.adapters, adapt.AdaptersMap)
        self.assertIs(adapt.context_adapters(None), _defaults.adapters)

    def test_dumpers(self) -> None:
        m = _defaults.adapters
        for cls, name in DUMPERS.items():
            self.assertEqual(m.get_dumper(cls).__name__, name, cls)
        self.assertEqual(len(m._dumpers), len(DUMPERS))
        self.assertTrue(all(fmt == 0 for _, fmt in m._dumpers))

    def test_loaders(self) -> None:
        m = _defaults.adapters
        for oid, name in LOADERS.items():
            self.assertEqual(m.get_loader(oid).__name__, name, oid)
        for oid, array_oid in ARRAYS.items():
            cls = m.get_loader(array_oid)
            self.assertTrue(issubclass(cls, array.ArrayLoader), array_oid)
            self.assertEqual((cls.base_oid, cls.delimiter), (oid, ","))
            self.assertEqual(m.get_array_oid(oid), array_oid)
        self.assertEqual(len(m._loaders), len(LOADERS) + len(ARRAYS))
        self.assertEqual(len(m._array_oids), len(ARRAYS))
        self.assertIsNone(m.get_loader(600))

    def test_every_python_type_dumps(self) -> None:
        tz = dt.timezone(dt.timedelta(hours=5, minutes=30))
        params = [
            None, True, 1, 2**40, 2**70, 1.5, decimal.Decimal("1.50"), "x", b"\x00",
            bytearray(b"a"), memoryview(b"b"), dt.date(2000, 1, 2), dt.time(3, 4, 5),
            dt.time(3, 4, 5, tzinfo=tz), dt.datetime(2000, 1, 2, 3, 4, 5),
            dt.datetime(2000, 1, 2, 3, 4, 5, tzinfo=tz), dt.timedelta(days=1),
            uuid.UUID(int=5), Json({"a": 1}), Jsonb([1]), [1, None], [[1.5]], ["a"],
            [], [dt.date(2000, 1, 2)], [uuid.UUID(int=5)], [Jsonb(1)],
            [dt.datetime(2000, 1, 2, tzinfo=tz)], [dt.timedelta(seconds=1)],
        ]
        values, oids = Transformer().dump_sequence(params)
        self.assertEqual(
            oids,
            [
                0, 16, 23, 20, 1700, 701, 1700, 0, 17, 17, 17, 1082, 1083, 1266, 1114,
                1184, 1186, 2950, 114, 3802, 1007, 1022, 0, 0, 1182, 2951, 3807, 1185,
                1187,
            ],
        )
        self.assertEqual(
            values,
            [
                None, b"t", b"1", b"1099511627776", b"1180591620717411303424", b"1.5",
                b"1.50", b"x", b"\\x00", b"\\x61", b"\\x62", b"2000-01-02",
                b"03:04:05", b"03:04:05+05:30", b"2000-01-02 03:04:05",
                b"2000-01-02 03:04:05+05:30", b"+1 days +0 seconds +0 microseconds",
                b"00000000-0000-0000-0000-000000000005", b'{"a": 1}', b"[1]",
                b'{"1",NULL}', b'{{"1.5"}}', b'{"a"}', b"{}", b'{"2000-01-02"}',
                b'{"00000000-0000-0000-0000-000000000005"}', b'{"1"}',
                b'{"2000-01-02 00:00:00+05:30"}',
                b'{"+0 days +1 seconds +0 microseconds"}',
            ],
        )

    def test_every_core_type_loads(self) -> None:
        tx = Transformer()
        cells = {
            16: b"t", 17: b"\\x00ff", 19: b"n", 20: b"5", 21: b"5", 23: b"5",
            25: b"x", 26: b"5", 114: b"[1]", 700: b"1.5", 701: b"1.5", 1042: b"x ",
            1043: b"x", 1082: b"2000-01-02", 1083: b"03:04:05",
            1114: b"2000-01-02 03:04:05", 1184: b"2000-01-02 03:04:05+00",
            1186: b"1 day", 1266: b"03:04:05+00", 1700: b"1.50",
            2950: b"00000000-0000-0000-0000-000000000005", 3802: b"[1]", 600: b"(1,2)",
        }
        utc = dt.timezone.utc
        expected = {
            16: True, 17: b"\x00\xff", 19: "n", 20: 5, 21: 5, 23: 5, 25: "x", 26: 5,
            114: [1], 700: 1.5, 701: 1.5, 1042: "x ", 1043: "x",
            1082: dt.date(2000, 1, 2), 1083: dt.time(3, 4, 5),
            1114: dt.datetime(2000, 1, 2, 3, 4, 5),
            1184: dt.datetime(2000, 1, 2, 3, 4, 5, tzinfo=utc),
            1186: dt.timedelta(days=1), 1266: dt.time(3, 4, 5, tzinfo=utc),
            1700: decimal.Decimal("1.50"), 2950: uuid.UUID(int=5), 3802: [1],
            600: "(1,2)",
        }
        for oid, data in cells.items():
            self.assertEqual(tx.get_loader(oid).load(data), expected[oid], oid)
        self.assertEqual(
            tx.get_loader(1185).load(b'{"2000-01-02 03:04:05+00",NULL}'),
            [dt.datetime(2000, 1, 2, 3, 4, 5, tzinfo=utc), None],
        )
        self.assertEqual(
            tx.get_loader(3807).load(b'{"{\\"a\\": 1}","null",NULL}'),
            [{"a": 1}, None, None],
        )
        self.assertEqual(tx.get_loader(1017).load(b'{"(1,2)"}'), '{"(1,2)"}')

    def test_connection_map_is_a_copy(self) -> None:
        conn = FakeConnection()
        conn.adapters.register_loader(600, adapt.Loader)
        self.assertIs(conn.adapters.get_loader(600), adapt.Loader)
        self.assertIsNone(_defaults.adapters.get_loader(600))
        self.assertEqual(conn.adapters.get_loader(23).__name__, "IntLoader")
```

#### CPYTHON-ADAPT-12A-ROUNDTRIP-BASIC

Files: `tests/adapt_db.py`, `tests/test_roundtrip_basic.py`. Design sections: 1 (the
central path), 2.2, 5, 6.2, 6.3.

Step. Write `tests/adapt_db.py` and the test file. Nothing in the package changes: these
are the database round trips of NULL, the numbers, the text family and bytea and of the
placeholder cases, in oracle and server mode. A failure in oracle mode means a module of
an earlier part deviates from this document; a failure in server mode only is an engine
gap (rule C8).

`tests/adapt_db.py`: the central path of section 1 with the blocking calls of PQ 5.7
(`exec_`, `exec_params`). `AdaptTestCase.query` returns the loaded rows, or `None` for a
command without rows, and raises `DatabaseError(f"{sqlstate}: {primary message}")` for a
server error; `self.oids` holds the column type OIDs of the last result:

```python
"""Database test case of the adaptation layer: a cursor's central path in small."""
from __future__ import annotations

from typing import Any

from stuntgres import _queries
from stuntgres import errors as e
from stuntgres import pq
from stuntgres._transformer import Transformer
from tests import support
from tests.adapt_util import FakeConnection


class AdaptTestCase(support.DatabaseTestCase):
    """Convert, dump, execute through pq and load, on a database of its own."""

    def setUp(self) -> None:
        super().setUp()
        self.pgconn = pq.PGconn.connect(self.conninfo.encode())
        self.addCleanup(self.pgconn.finish)
        self.assertEqual(self.pgconn.status, 0, self.pgconn.error_message)
        self.context = FakeConnection(self.pgconn)
        self.oids: list[int] = []

    def query(self, query: Any, params: Any = None) -> list[tuple[Any, ...]] | None:
        """Run one statement; return its rows as Python objects (None: no rows)."""
        tx = Transformer(self.context)
        sql, values = _queries.prepare(query, params, tx.encoding)
        if values is None:
            result = self.pgconn.exec_(sql)
        else:
            dumped, oids = tx.dump_sequence(values)
            result = self.pgconn.exec_params(sql, dumped, oids)
        if result.status == pq.ExecStatus.COMMAND_OK:
            return None
        if result.status != pq.ExecStatus.TUPLES_OK:
            state = result.error_field(pq.DiagnosticField.SQLSTATE) or b"?"
            text = result.error_field(pq.DiagnosticField.MESSAGE_PRIMARY) or b""
            raise e.DatabaseError(f"{state.decode()}: {text.decode()}")
        tx.set_pgresult(result)
        self.oids = [result.ftype(i) for i in range(result.nfields)]
        return tx.load_rows(0, result.ntuples)

    def one(self, query: Any, params: Any = None) -> tuple[Any, ...]:
        """Run a statement that returns exactly one row; return that row."""
        rows = self.query(query, params)
        assert rows is not None and len(rows) == 1, rows
        return rows[0]

    def fails(self, cls: type, message: str, query: Any, params: Any = None) -> None:
        """Assert that the statement raises exactly `cls` with this message."""
        with self.assertRaises(cls) as caught:
            self.query(query, params)
        self.assertIs(type(caught.exception), cls)
        self.assertEqual(str(caught.exception), message)
```

Tests (`tests/test_roundtrip_basic.py`, database modes; 17 cases; expectations are rows
of experiments 2, 4 and 5):

```python
"""Round trips of NULL, numbers, text and bytea, and placeholder cases."""
from __future__ import annotations

import math
from decimal import Decimal

from stuntgres import errors as e
from stuntgres.types.string import StrTextDumper
from tests.adapt_db import AdaptTestCase

ALL_BYTES = bytes(range(256))
UNTYPED = "42P18: could not determine data type of parameter $1"


class NullTest(AdaptTestCase):
    """SQL NULL in every position."""

    def test_null_parameter(self) -> None:
        self.assertEqual(self.one("select %s", (None,)), (None,))
        self.assertEqual(self.oids, [25])
        self.assertEqual(self.one("select %s::int4", (None,)), (None,))
        self.assertEqual(self.oids, [23])
        self.assertEqual(self.one("select coalesce(%s, 5)", (None,)), (5,))
        self.fails(e.DatabaseError, UNTYPED, "select %s is null", (None,))
        self.assertEqual(self.one("select %s::text is null", (None,)), (True,))

    def test_null_in_columns_and_arrays(self) -> None:
        self.query("create table n (id int4, i int4, t text, b bytea, a int4[])")
        self.query(
            "insert into n values (%s, %s, %s, %s, %s)",
            (1, None, None, None, None),
        )
        self.query(
            "insert into n values (%s, %s, %s, %s, %s)",
            (2, 5, "", b"", [None, 1]),
        )
        self.assertEqual(
            self.query("select * from n order by id"),
            [(1, None, None, None, None), (2, 5, "", b"", [None, 1])],
        )
        self.assertEqual(self.query("select * from n where i = %s", (None,)), [])
        self.assertEqual(self.one("select %s", ([None, None],)), ("{NULL,NULL}",))


class NumberTest(AdaptTestCase):
    """bool, integers, floats and numeric."""

    def test_columns(self) -> None:
        self.query(
            "create table t (b bool, i2 int2, i4 int4, i8 int8, o oid, f4 float4,"
            " f8 float8, n numeric, n2 numeric(10,2))"
        )
        row = (
            True, 32767, -2147483648, 9223372036854775807, 4294967295, 1.5, 0.1,
            Decimal("12345678901234567890.123456789"), Decimal("12345.678"),
        )
        self.query("insert into t values (%s, %s, %s, %s, %s, %s, %s, %s, %s)", row)
        self.assertEqual(
            self.one("select * from t"), row[:8] + (Decimal("12345.68"),)
        )
        self.assertEqual(self.oids, [16, 21, 23, 20, 26, 700, 701, 1700, 1700])
        self.assertEqual(
            self.one("select not %s, %s and true", (True, False)),
            (False, False),
        )

    def test_integer_type_follows_magnitude(self) -> None:
        row = self.one(
            "select %s, %s, %s, %s", (1, 2**31 - 1, 2**31, 2**63)
        )
        self.assertEqual(row, (1, 2147483647, 2147483648, Decimal(2**63)))
        self.assertEqual(self.oids, [23, 23, 20, 1700])
        self.assertEqual(
            self.one("select pg_typeof(%s)::text, pg_typeof(%s)::text", (1, 2**40)),
            ("integer", "bigint"),
        )

    def test_integers_in_expressions(self) -> None:
        eq = self.assertEqual
        eq(self.one("select %s + 1, %s + %s", (1, 32767, 32767)), (2, 65534))
        eq(self.one("select repeat('ab', %s), 'a' || %s", (2, 1)), ("abab", "a1"))
        eq(self.one("select date '2000-01-01' + %s", (1,))[0].isoformat(), "2000-01-02")
        eq(
            self.one("select %s = any('{1,2}'::int4[]), %s * 2.5", (1, 1)),
            (True, Decimal("2.5")),
        )
        eq(self.query("select generate_series(1, %s)", (3,)), [(1,), (2,), (3,)])
        self.fails(
            e.DatabaseError,
            "22003: integer out of range",
            "select %s + 1",
            (2**31 - 1,),
        )

    def test_integers_into_other_columns(self) -> None:
        self.query("create table t (i2 int2, i8 int8, n numeric, f float8, t text)")
        self.query("insert into t values (%s, %s, %s, %s, %s)", (1, 1, 1, 1, 1))
        self.assertEqual(self.one("select * from t"), (1, 1, Decimal("1"), 1.0, "1"))
        self.fails(
            e.DatabaseError, "22003: smallint out of range",
            "insert into t (i2) values (%s)", (40000,),
        )

    def test_float_specials(self) -> None:
        row = self.one(
            "select %s, %s, %s, %s", (float("inf"), float("-inf"), float("nan"), -0.0)
        )
        self.assertEqual(row[:2], (math.inf, -math.inf))
        self.assertTrue(math.isnan(row[2]))
        self.assertEqual(math.copysign(1.0, row[3]), -1.0)
        self.assertEqual(self.oids, [701, 701, 701, 701])
        self.assertEqual(self.one("select %s * 2, %s + 1.5", (1.5, 1.5)), (3.0, 3.0))
        row = self.one("select 1e-5::float8, 1e16::float8, 16777216::float4")
        self.assertEqual(row, (1e-05, 1e16, 16777216.0))

    def test_numeric_is_exact(self) -> None:
        eq = self.assertEqual
        big = Decimal("123456789012345678901234567890.123456789012345678901234567890")
        eq(self.one("select %s, %s + 1", (big, Decimal("9007199254740993"))),
           (big, Decimal("9007199254740994")))
        eq(self.one("select %s * 2, %s + 1.5", (Decimal("1.50"), Decimal("1.50"))),
           (Decimal("3.00"), Decimal("3.00")))
        row = self.one(
            "select %s, %s, %s",
            (Decimal("Infinity"), Decimal("-Infinity"), Decimal("sNaN")),
        )
        eq(row[:2], (Decimal("Infinity"), Decimal("-Infinity")))
        self.assertTrue(row[2].is_nan())
        row = self.one("select %s, %s", (Decimal("1E+2"), Decimal("0E-7")))
        eq([format(v, "f") for v in row], ["100", "0.0000000"])
        row = self.one("select 0.1::numeric + 0.2::numeric, 1e-20::numeric, 1/2.0")
        eq(
            [format(v, "f") for v in row],
            ["0.3", "0.00000000000000000001", "0.50000000000000000000"],
        )


class TextTest(AdaptTestCase):
    """str, the text types and bytea."""

    def test_text_columns(self) -> None:
        self.query("create table t (t text, v varchar(5), c char(5), n name)")
        text = "\u0107 \U0001F600 it's \"q\" \\ back\nline"
        self.query("insert into t values (%s, %s, %s, %s)", (text, "abc", "ab", "nm"))
        self.assertEqual(self.one("select * from t"), (text, "abc", "ab   ", "nm"))
        self.assertEqual(self.oids, [25, 1043, 1042, 19])
        self.assertEqual(
            self.one("select length(%s), octet_length(%s)", ("\u0107 \U0001F600",) * 2),
            (3, 7),
        )
        self.assertEqual(self.one("select %s", ("",)), ("",))

    def test_str_is_untyped(self) -> None:
        eq = self.assertEqual
        eq(
            self.one("select %s, %s || 'x', upper(%s)", ("abc",) * 3),
            ("abc", "abcx", "ABC"),
        )
        eq(self.oids, [25, 25, 25])
        eq(self.one("select %s = 'abc', %s like 'a%%'", ("abc", "abc")), (True, True))
        self.query("create table t (i int4, d date, j jsonb, u uuid, b bool)")
        self.query(
            "insert into t values (%s, %s, %s, %s, %s)",
            (
                "5", "2000-01-02", '{"a": 1}',
                "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", "true",
            ),
        )
        row = self.one("select i, d::text, j, u::text, b from t where i = %s", ("5",))
        eq(
            row,
            (5, "2000-01-02", {"a": 1}, "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", True),
        )
        self.fails(e.DatabaseError, UNTYPED, "select concat(%s, 1)", ("abc",))
        eq(self.one("select concat(%s::text, 1)", ("abc",)), ("abc1",))

    def test_str_text_dumper(self) -> None:
        self.context.adapters.register_dumper(str, StrTextDumper)
        self.assertEqual(
            self.one("select concat(%s, 1), pg_typeof(%s)::text", ("abc", "x")),
            ("abc1", "text"),
        )
        self.query("create table t (i int4)")
        self.fails(
            e.DatabaseError,
            '42804: column "i" is of type integer but expression is of type text',
            "insert into t values (%s)",
            ("5",),
        )

    def test_bytea(self) -> None:
        eq = self.assertEqual
        self.query("create table t (b bytea)")
        self.query(
            "insert into t values (%s), (%s), (%s)",
            (ALL_BYTES, b"", bytearray(b"ab")),
        )
        eq(self.query("select b from t"), [(ALL_BYTES,), (b"",), (b"ab",)])
        eq(self.oids, [17])
        eq(self.one("select length(%s), md5(%s)", (ALL_BYTES, memoryview(b"\x01\x02"))),
           (256, "0cb988d042a7f28dd5fe2b55b3f5ac7a"))
        self.query("set bytea_output = 'escape'")
        eq(self.query("select b from t"), [(ALL_BYTES,), (b"",), (b"ab",)])
        eq(self.one("select %s::bytea, 'abc'::bytea", (b"\\x",)), (b"\\x", b"abc"))

    def test_types_without_a_loader_load_as_str(self) -> None:
        row = self.one(
            "select '192.168.0.1/24'::inet, point(1,2), '[1,5)'::int4range,"
            " 'x'::\"char\", '101'::bit(3), row(1,'a'), '1 2'::oidvector"
        )
        self.assertEqual(
            row,
            ("192.168.0.1/24", "(1,2)", "[1,5)", "x", "101", "(1,a)", "1 2"),
        )
        self.assertEqual(self.oids, [869, 600, 3904, 18, 1560, 2249, 30])


class PlaceholderTest(AdaptTestCase):
    """What the server answers for the placeholder cases of the converter."""

    def test_literal_percent(self) -> None:
        eq = self.assertEqual
        eq(self.one("select '100%'"), ("100%",))
        eq(self.one("select '%s', '%%'"), ("%s", "%%"))
        eq(self.one("select '100%%', %s", (1,)), ("100%", 1))
        eq(self.one("select 10 %% 3", ()), (1,))
        eq(self.one("select 10 % 3"), (1,))

    def test_named_and_repeated(self) -> None:
        self.assertEqual(
            self.one("select %(a)s, %(b)s, %(a)s", {"a": 1, "b": "two", "c": 3}),
            (1, "two", 1),
        )
        self.assertEqual(self.oids, [23, 25, 23])
        self.assertEqual(self.one("select 1", {"a": 1}), (1,))

    def test_several_statements_need_no_parameters(self) -> None:
        self.assertEqual(self.one("select 1; select 2"), (2,))
        self.fails(
            e.DatabaseError,
            "42601: cannot insert multiple commands into a prepared statement",
            "select 1; select 2",
            (),
        )

    def test_sql_is_not_parsed(self) -> None:
        eq = self.assertEqual
        for query in (
            "select '%s'", 'select 1 as "%s"', "select 1 -- %s", "select 1 /* %s */",
            "select $$%s$$",
        ):
            self.fails(e.DatabaseError, UNTYPED, query, ("x",))
        eq(self.one("select '%s'", (5,)), ("$1",))
        eq(self.one("select 1 -- %s", (5,)), (1,))
        eq(self.one("select $q$ %(a)s $q$, %(a)s", {"a": "x"}), (" $1 ", "x"))
        self.fails(
            e.DatabaseError,
            "42P18: could not determine data type of parameter $2",
            "select %s0",
            (1,),
        )
```

#### CPYTHON-ADAPT-12B-ROUNDTRIP-DATETIME

Files: `tests/test_roundtrip_datetime.py`. Design sections: 5, 6.4, 6.5, 6.6.

Step. Write the test file; nothing in the package changes.

Tests (`tests/test_roundtrip_datetime.py`, database modes; 12 cases). The session time
zone is named differently by the two servers, so the tests compare instants, offsets and
`str(value.tzinfo)` against the reported `TimeZone`:

```python
"""Round trips of date, time, timetz, timestamp, timestamptz and interval."""
from __future__ import annotations

import datetime as dt
import zoneinfo

from stuntgres import errors as e
from tests.adapt_db import AdaptTestCase

RANGE = " is out of the range Python can represent"
UTC = dt.timezone.utc
TZ530 = dt.timezone(dt.timedelta(hours=5, minutes=30))
TD = dt.timedelta


class DateTimeRoundTripTest(AdaptTestCase):
    """Columns of every date and time type, and the session settings."""

    def test_columns(self) -> None:
        self.query(
            "create table t (d date, tm time, tmz timetz, ts timestamp,"
            " tsz timestamptz, iv interval)"
        )
        row = (
            dt.date(2000, 1, 2), dt.time(3, 4, 5, 6), dt.time(3, 4, 5, tzinfo=TZ530),
            dt.datetime(2000, 1, 2, 3, 4, 5, 6),
            dt.datetime(2000, 1, 2, 3, 4, 5, 6, tzinfo=TZ530),
            TD(days=1, seconds=2, microseconds=3),
        )
        self.query("insert into t values (%s, %s, %s, %s, %s, %s)", row)
        got = self.one("select * from t")
        self.assertEqual(got, row)
        self.assertEqual(self.oids, [1082, 1083, 1266, 1114, 1184, 1186])
        self.assertEqual(got[2].utcoffset(), TD(hours=5, minutes=30))
        self.assertEqual(got[4].utcoffset(), TD(0))
        self.assertEqual((got[4].day, got[4].hour, got[4].minute), (1, 21, 34))
        zone = self.pgconn.parameter_status(b"TimeZone")
        assert zone is not None
        self.assertEqual(str(got[4].tzinfo), zone.decode())
        self.assertEqual(self.query("select * from t where tsz = %s", (row[4],)), [got])

    def test_parameter_types(self) -> None:
        params = (
            dt.date(2000, 1, 2), dt.time(3, 4, 5), dt.time(3, 4, 5, tzinfo=UTC),
            dt.datetime(2000, 1, 2), dt.datetime(2000, 1, 2, tzinfo=UTC), TD(1),
        )
        self.query("select %s, %s, %s, %s, %s, %s", params)
        self.assertEqual(self.oids, [1082, 1083, 1266, 1114, 1184, 1186])

    def test_expressions(self) -> None:
        eq = self.assertEqual
        day = dt.date(2000, 1, 2)
        row = self.one("select %s + 1, %s - date '2000-01-01'", (day, day))
        eq(row, (dt.date(2000, 1, 3), 1))
        row = self.one("select %s + interval '1 day'", (day,))
        eq(row, (dt.datetime(2000, 1, 3),))
        stamp = dt.datetime(2000, 1, 2, 3, 4, 5, 6)
        row = self.one("select %s + interval '1 day', %s::date", (stamp, stamp))
        eq(row, (dt.datetime(2000, 1, 3, 3, 4, 5, 6), day))
        aware = stamp.replace(tzinfo=TZ530)
        row = self.one("select %s at time zone 'UTC'", (aware,))
        eq(row, (dt.datetime(2000, 1, 1, 21, 34, 5, 6),))
        params = (TD(days=1, seconds=2), TD(hours=36))
        row = self.one("select %s * 2, date '2000-01-01' + %s", params)
        eq(row, (TD(days=2, seconds=4), dt.datetime(2000, 1, 2, 12)))
        row = self.one("select %s + interval '1 hour'", (dt.time(3, 4, 5, 6),))
        eq(row, (dt.time(4, 4, 5, 6),))

    def test_limits(self) -> None:
        eq = self.assertEqual
        row = (
            dt.date(1, 1, 1), dt.date(9999, 12, 31), dt.time(0, 0),
            dt.time(23, 59, 59, 999999), dt.datetime(1, 1, 1),
            dt.datetime(9999, 12, 31, 23, 59, 59, 999999),
            dt.datetime(1, 1, 1, tzinfo=UTC),
            dt.datetime(9999, 12, 31, 23, 59, 59, 999999, tzinfo=UTC),
            TD.max, TD.min, TD(microseconds=-1),
        )
        eq(self.one("select " + ", ".join(["%s"] * len(row)), row), row)
        got = self.one("select '0099-07-04'::date, '0099-07-04 01:02:03'::timestamp")
        eq(got, (dt.date(99, 7, 4), dt.datetime(99, 7, 4, 1, 2, 3)))

    def test_values_python_cannot_represent(self) -> None:
        for kind, text in (
            ("date", "infinity"), ("date", "-infinity"), ("date", "0044-03-15 BC"),
            ("date", "10000-01-01"), ("time", "24:00:00"),
            ("timetz", "24:00:00-15:59"), ("timestamp", "infinity"),
            ("timestamp", "0001-12-31 23:59:59 BC"),
            ("timestamp", "10000-01-01 00:00:00"), ("timestamptz", "-infinity"),
            ("timestamptz", "10000-01-01 00:00:00+00"),
            ("timestamptz", "0001-12-31 23:59:59+00 BC"), ("interval", "infinity"),
            ("interval", "178000000 years"), ("interval", "1000000000 days"),
        ):
            self.fails(
                e.DataError,
                f"{kind} value {text!r}" + RANGE,
                f"select '{text}'::{kind}",
            )
        self.assertEqual(self.one("select 1"), (1,))

    def test_session_time_zone(self) -> None:
        eq = self.assertEqual
        self.query("set timezone = 'Europe/Warsaw'")
        row = self.one(
            "select '2021-07-01 10:00:00+00'::timestamptz,"
            " '2021-10-31 00:30:00+00'::timestamptz,"
            " '2021-10-31 01:30:00+00'::timestamptz,"
            " '1900-01-01 00:00:00+00'::timestamptz"
        )
        instants = [
            dt.datetime(2021, 7, 1, 10, tzinfo=UTC),
            dt.datetime(2021, 10, 31, 0, 30, tzinfo=UTC),
            dt.datetime(2021, 10, 31, 1, 30, tzinfo=UTC),
            dt.datetime(1900, 1, 1, tzinfo=UTC),
        ]
        eq([v.astimezone(UTC) for v in row], instants)
        eq([(v.hour, v.minute) for v in row], [(12, 0), (2, 30), (2, 30), (1, 24)])
        offsets = ["2:00:00", "2:00:00", "1:00:00", "1:24:00"]
        eq([str(v.utcoffset()) for v in row], offsets)
        try:
            zoneinfo.ZoneInfo("Europe/Warsaw")
        except (KeyError, ValueError, OSError):
            names = ["UTC+02:00", "UTC+02:00", "UTC+01:00", "UTC+01:24"]
            folds = [0, 0, 0, 0]
        else:
            names = ["Europe/Warsaw"] * 4
            folds = [0, 0, 1, 0]
        eq([str(v.tzinfo) for v in row], names)
        eq([v.fold for v in row], folds)
        query = "select %s = '2021-10-31 01:30:00+00'::timestamptz"
        eq(self.one(query, (row[2],)), (True,))

    def test_time_zone_python_does_not_know(self) -> None:
        eq = self.assertEqual
        self.query("set timezone = '<+0530>-5:30'")
        value = self.one("select '2021-07-01 10:00:00+00'::timestamptz")[0]
        eq(value, dt.datetime(2021, 7, 1, 15, 30, tzinfo=TZ530))
        eq(str(value.tzinfo), "UTC+05:30")
        eq(self.one("select '12:00:00+00'::timetz"), (dt.time(12, 0, tzinfo=UTC),))

    def test_time_zone_moves_value_out_of_range(self) -> None:
        eq = self.assertEqual
        self.query("set timezone = 'Asia/Kolkata'")
        self.fails(
            e.DataError,
            "timestamptz value '10000-01-01 05:29:59.999999+05:30'" + RANGE,
            "select '9999-12-31 23:59:59.999999+00'::timestamptz",
        )
        value = self.one("select '1900-01-01 00:00:00+00'::timestamptz")[0]
        eq((value.hour, value.minute, value.second), (5, 21, 10))
        eq(value.utcoffset(), TD(hours=5, minutes=21, seconds=10))

    def test_date_style(self) -> None:
        eq = self.assertEqual
        self.query("set datestyle = 'German'")
        only = ": the session's DateStyle is 'German, DMY', only ISO is supported"
        self.fails(
            e.DataError,
            "cannot parse date value '02.01.2000'" + only,
            "select '2000-01-02'::date",
        )
        self.fails(
            e.DataError,
            "cannot parse timestamp value '02.01.2000 03:04:05'" + only,
            "select '2000-01-02 03:04:05'::timestamp",
        )
        self.fails(
            e.DataError,
            "cannot parse timestamptz value '02.01.2000 03:04:05 UTC'" + only,
            "select '2000-01-02 03:04:05+00'::timestamptz",
        )
        row = self.one("select '03:04:05.6'::time, '03:04:05.6+01'::timetz")
        plus1 = dt.timezone(TD(hours=1))
        eq(row, (dt.time(3, 4, 5, 600000), dt.time(3, 4, 5, 600000, tzinfo=plus1)))
        day, stamp = dt.date(2000, 1, 2), dt.datetime(2000, 1, 2, 3, 4, 5)
        query = "select %s::text, %s = '2000-01-02 03:04:05'::timestamp"
        eq(self.one(query, (day, stamp)), ("02.01.2000", True))

    def test_dumps_are_read_alike_in_every_date_style(self) -> None:
        query = (
            "select to_char(%s, 'YYYY-MM-DD'),"
            " to_char(%s, 'YYYY-MM-DD HH24:MI:SS.US')"
        )
        params = (dt.date(99, 1, 2), dt.datetime(2000, 1, 2, 3, 4, 5, 6))
        for style in ("SQL, DMY", "Postgres, MDY", "German", "ISO, DMY"):
            self.query(f"set datestyle = '{style}'")
            row = self.one(query, params)
            self.assertEqual(row, ("0099-01-02", "2000-01-02 03:04:05.000006"), style)
        row = self.one("select '2000-01-02'::date, '2000-01-02 03:04:05'::timestamp")
        self.assertEqual(row, (dt.date(2000, 1, 2), dt.datetime(2000, 1, 2, 3, 4, 5)))

    def test_interval_style(self) -> None:
        eq = self.assertEqual
        values = (
            TD(days=1, seconds=2, microseconds=3), TD(microseconds=-1),
            TD(days=-1, seconds=5), TD(minutes=-90),
        )
        query = (
            "select %s = interval '1 day 2 seconds 3 microseconds',"
            " %s = interval '-1 microseconds', %s = interval '-1 day +5 seconds',"
            " %s = interval '-90 minutes'"
        )
        for style in ("sql_standard", "postgres_verbose", "iso_8601", "postgres"):
            self.query(f"set intervalstyle = '{style}'")
            eq(self.one(query, values), (True, True, True, True), style)
        self.query("set intervalstyle = 'iso_8601'")
        self.fails(
            e.DataError,
            "cannot parse interval value 'P1D': the session's IntervalStyle is"
            " 'iso_8601', only postgres is supported",
            "select '1 day'::interval",
        )

    def test_interval_months_and_years(self) -> None:
        row = self.one(
            "select '1 year 2 mons 3 days 04:05:06.789'::interval,"
            " '-1 mons +1 day'::interval, '14 mons'::interval,"
            " '1.5 days'::interval, '1 week'::interval"
        )
        clock = TD(hours=4, minutes=5, seconds=6, microseconds=789000)
        expected = (
            TD(days=428) + clock, TD(days=-29), TD(days=425), TD(hours=36),
            TD(days=7),
        )
        self.assertEqual(row, expected)
```

#### CPYTHON-ADAPT-12C-ROUNDTRIP-COMPOSITE

Files: `tests/test_roundtrip_composite.py`. Design sections: 3.6, 5, 6.7 to 6.10.

Step. Write the test file; nothing in the package changes.
`test_untyped_list_cannot_be_subscripted` needs the engine fix of
CPYTHON-ENGINE-03-SUBSCRIPT-UNKNOWN in server mode; if it fails there although that part
is done, stop and report; do not change the test.

Tests (`tests/test_roundtrip_composite.py`, database modes; 14 cases):

```python
"""Round trips of uuid, json, jsonb and arrays, and the registry on a connection."""
from __future__ import annotations

import datetime as dt
import json
import uuid
from decimal import Decimal
from typing import Any

from stuntgres import adapt
from stuntgres import errors as e
from stuntgres.types.array import register_array
from stuntgres.types.json import Json, Jsonb, set_json_dumps, set_json_loads
from tests.adapt_db import AdaptTestCase

U = uuid.UUID("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
UTC = dt.timezone.utc
TD = dt.timedelta
DOC = {"a": [1, None, "x"], "b": {"c": 1.5}, "u": "\u0107\U0001F600"}


class Point:
    """A user type for the registry tests."""

    def __init__(self, x: float, y: float) -> None:
        self.x, self.y = x, y


class PointDumper(adapt.Dumper):
    """Point -> point."""

    oid = 600

    def dump(self, obj: Any) -> bytes:
        return b"(%g,%g)" % (obj.x, obj.y)


class PointLoader(adapt.Loader):
    """point -> (x, y)."""

    def load(self, data: bytes) -> tuple[float, float]:
        x, y = data.decode()[1:-1].split(",")
        return float(x), float(y)


class CharLoader(adapt.Loader):
    """"char" -> its code."""

    def load(self, data: bytes) -> int:
        return ord(data)


class UuidJsonTest(AdaptTestCase):
    """uuid, json and jsonb."""

    def test_uuid(self) -> None:
        self.query("create table t (u uuid, s text)")
        self.query("insert into t values (%s, %s)", (U, U))
        self.assertEqual(self.one("select * from t where u = %s", (U,)), (U, str(U)))
        self.assertEqual(self.oids, [2950, 25])
        row = self.one("select 'FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF'::uuid, %s", (U,))
        self.assertEqual(row, (uuid.UUID(int=2**128 - 1), U))

    def test_json_columns(self) -> None:
        self.query("create table t (j json, jb jsonb)")
        self.query("insert into t values (%s, %s)", (Json(DOC), Jsonb(DOC)))
        self.query("insert into t values (%s, %s)", (Jsonb(None), Json([1, "a", None])))
        self.assertEqual(
            self.query("select * from t"), [(DOC, DOC), (None, [1, "a", None])]
        )
        self.assertEqual(self.oids, [114, 3802])
        row = self.one("select j is null, jsonb_typeof(jb) from t limit 1 offset 1")
        self.assertEqual(row, (False, "array"))

    def test_json_expressions(self) -> None:
        eq = self.assertEqual
        row = self.one("select %s -> 'a', %s ->> 'u'", (Jsonb(DOC), Jsonb(DOC)))
        eq(row, ([1, None, "x"], "\u0107\U0001F600"))
        query = "select %s @> '{\"b\": {\"c\": 1.5}}', %s ? 'a'"
        eq(self.one(query, (Jsonb(DOC), Jsonb(DOC))), (True, True))
        row = self.one("select %s -> 'a', json_typeof(%s)", (Json(DOC), Json(DOC)))
        eq(row, ([1, None, "x"], "object"))
        query = "select pg_typeof(%s)::text, pg_typeof(%s)::text"
        eq(self.one(query, (Json(1), Jsonb(1))), ("json", "jsonb"))
        row = self.one(
            "select '{\"b\":1,\"a\":2,\"a\":3}'::jsonb, '1e100'::jsonb, '1.0'::jsonb"
        )
        eq(row, ({"a": 3, "b": 1}, 10**100, 1.0))

    def test_json_functions_of_a_connection(self) -> None:
        set_json_loads(lambda text: json.loads(text, parse_float=Decimal), self.context)
        set_json_dumps(lambda obj: json.dumps(obj, sort_keys=True), self.context)
        query = "select '{\"a\": 1.10}'::jsonb, '[1.5]'::json, %s::text"
        row = self.one(query, (Json({"b": 1, "a": 2}),))
        expected = ({"a": Decimal("1.10")}, [Decimal("1.5")], '{"a": 2, "b": 1}')
        self.assertEqual(row, expected)


class ArrayTest(AdaptTestCase):
    """Lists as parameters and arrays as results."""

    def test_columns(self) -> None:
        self.query(
            "create table t (i int4[], i8 int8[], n numeric[], f float8[], b bool[],"
            " t text[], v varchar[], by bytea[], d date[], tm time[], ts timestamp[],"
            " tsz timestamptz[], iv interval[], u uuid[], jb jsonb[])"
        )
        text = ["a", None, 'q"\\ ,{}', "", "NULL", " x ", "\u0107\U0001F600"]
        row = (
            [1, None, 3], [1, 2**40], [Decimal("1.50"), None], [1.5, float("inf")],
            [True, False, None], text, ["a", "b c"], [b"\x00\xff", b"", b'a\\b"c'],
            [dt.date(2000, 1, 2), None], [dt.time(3, 4, 5)],
            [dt.datetime(2000, 1, 2, 3, 4, 5, 6)],
            [dt.datetime(2000, 1, 2, tzinfo=UTC)], [TD(days=1, seconds=2)],
            [U, None], [Jsonb({"a": 'x "y"'}), Jsonb(None), None],
        )
        self.query("insert into t values (" + ", ".join(["%s"] * 15) + ")", row)
        expected = row[:14] + ([{"a": 'x "y"'}, None, None],)
        self.assertEqual(self.one("select * from t"), expected)
        oids = [
            1007, 1016, 1231, 1022, 1000, 1009, 1015, 1001, 1182, 1183, 1115, 1185,
            1187, 2951, 3807,
        ]
        self.assertEqual(self.oids, oids)

    def test_parameter_types(self) -> None:
        params = (
            [1], [2**40], [2**70], [1.5], [True], [b"x"], [dt.date(2000, 1, 2)],
            [dt.time(1, 2)], [dt.time(1, 2, tzinfo=UTC)], [dt.datetime(2000, 1, 2)],
            [dt.datetime(2000, 1, 2, tzinfo=UTC)], [TD(1)], [U], [Json(1)], [Jsonb(1)],
        )
        self.query("select " + ", ".join(["%s"] * 15), params)
        oids = [
            1007, 1016, 1231, 1022, 1000, 1001, 1182, 1183, 1270, 1115, 1185, 1187,
            2951, 199, 3807,
        ]
        self.assertEqual(self.oids, oids)

    def test_shapes(self) -> None:
        eq = self.assertEqual
        row = self.one(
            "select '{}'::int4[], '{{1,2},{3,4}}'::int4[], '[2:4]={1,2,3}'::int4[],"
            " '{{{1},{2}},{{3},{4}}}'::int4[], '{NULL}'::int4[],"
            " '[0:1][2:3]={{1,2},{3,4}}'::int4[], array[array[]::int4[]]"
        )
        expected = (
            [], [[1, 2], [3, 4]], [1, 2, 3], [[[1], [2]], [[3], [4]]], [None],
            [[1, 2], [3, 4]], [],
        )
        eq(row, expected)
        nested = [[1, 2], [3, 4]]
        row = self.one("select %s, array_dims(%s)", (nested, nested))
        eq(row, (nested, "[1:2][1:2]"))
        self.fails(
            e.DatabaseError,
            '22P02: malformed array literal: "{{"1","2"},{"3"}}"',
            "select %s",
            ([[1, 2], [3]],),
        )

    def test_expressions(self) -> None:
        eq = self.assertEqual
        items = [1, 2, 3]
        query = "select 2 = any(%s), array_length(%s, 1), %s[1]"
        eq(self.one(query, (items, items, items)), (True, 3, 1))
        query = "select %s || 4, %s @> array[1], %s = array[1,2,3]"
        eq(self.one(query, (items, items, items)), ([1, 2, 3, 4], True, True))
        query = "select x from generate_series(1, 5) x where x = any(%s)"
        eq(self.query(query, ([2, 4],)), [(2,), (4,)])
        words = ["a", "b"]
        row = self.one("select 'a' = any(%s), %s::text[]", (words, words))
        eq(row, (True, words))

    def test_untyped_lists(self) -> None:
        eq = self.assertEqual
        self.query("create table t (i int4[], t text[], d date[])")
        self.query("insert into t values (%s, %s, %s)", ([], [], [None]))
        row = (["1", "2"], ["a"], ["2000-01-02"])
        self.query("insert into t values (%s, %s, %s)", row)
        expected = [([], [], [None]), ([1, 2], ["a"], [dt.date(2000, 1, 2)])]
        eq(self.query("select * from t"), expected)
        row = self.one("select %s, %s, %s", ([], [None], ["a", "b"]))
        eq(row, ("{}", "{NULL}", '{"a","b"}'))
        eq(self.oids, [25, 25, 25])
        eq(self.one("select 1 = any(%s), %s::int4[]", ([], [])), (False, []))

    def test_untyped_list_cannot_be_subscripted(self) -> None:
        self.fails(
            e.DatabaseError,
            "42804: cannot subscript type unknown because it does not support"
            " subscripting",
            "select %s[1]",
            (["a", "b"],),
        )
        self.assertEqual(self.one("select (%s::text[])[1]", (["a", "b"],)), ("a",))

    def test_bytea_arrays_in_escape_output(self) -> None:
        self.query("set bytea_output = 'escape'")
        row = self.one("select %s", ([b"\x00\xff\\", b""],))
        self.assertEqual(row, ([b"\x00\xff\\", b""],))


class RegistryTest(AdaptTestCase):
    """Adapters registered on a connection."""

    def test_loader_and_dumper(self) -> None:
        adapters = self.context.adapters
        adapters.register_loader(600, PointLoader)
        adapters.register_dumper(Point, PointDumper)
        row = self.one("select %s, %s[0], point(3,4)", (Point(1, 2.5), Point(5, 6)))
        self.assertEqual(row, ((1.0, 2.5), 5.0, (3.0, 4.0)))
        self.assertEqual(self.oids, [600, 701, 600])

    def test_array_of_a_registered_type(self) -> None:
        self.context.adapters.register_loader(18, CharLoader)
        register_array(18, 1002, self.context)
        row = self.one("select array['a'::\"char\", null, 'b'], 'c'::\"char\"")
        self.assertEqual(row, ([97, None, 98], 99))

    def test_global_map_is_untouched(self) -> None:
        self.context.adapters.register_loader(600, PointLoader)
        register_array(18, 1002, self.context)
        defaults = adapt.context_adapters(None)
        self.assertIsNone(defaults.get_loader(600))
        self.assertIsNone(defaults.get_loader(1002))
        self.assertEqual(defaults.get_array_oid(18), 0)
        self.context = type(self.context)(self.pgconn)
        row = self.one("select point(1,2), array['a'::\"char\"]")
        self.assertEqual(row, ("(1,2)", "{a}"))
```

#### CPYTHON-ENGINE-03-SUBSCRIPT-UNKNOWN

An engine gap found in server mode (rule D8). Smallest reproduction, simple protocol:
`PREPARE p AS SELECT $1[1]`. Extended-protocol form (what the Python binding sends for
`cur.execute("select %s[1]", (["a", "b"],))`): Parse of `select $1[1]` with one parameter
of type OID 0, Bind with the text value `{"a","b"}`, Describe, Execute, Sync; libpq form:
`PQexecParams(conn, "select $1[1]", 1, {0}, {"{\"a\",\"b\"}"}, NULL, NULL, 0)`.

| | PostgreSQL 18.6 | stuntgres server (0c704a82ce) |
|---|---|---|
| `PREPARE p AS SELECT $1[1]` (simple query) | `ERROR`, SQLSTATE `42804`, `cannot subscript type unknown because it does not support subscripting`; the session continues | the server process terminates: `panic: runtime error: invalid memory address or nil pointer dereference` in `internal/engine/analyze.(*Binder).BindSubscript` (`array_expression.go:243`, the call `usage.Type.ID()` on a nil type), reached from `pgserver.(*client).executeSimple`; every connection of the server is lost |
| `select $1[1]` through `PQexecParams` with parameter type 0 | the same error `42804`; the connection stays usable | the connection is closed without an error message (`server closed the connection unexpectedly`); the server process survives |
| `select ($1)[1]`, `select ($1::unknown)[1]` through `PQexecParams` | `42804`, the same message | the connection is closed |
| `select ($1::text[])[1]` with the same value | `a` | `a` (already correct) |

A parameter whose type is still unknown has no type descriptor when the subscript is
bound; PostgreSQL reports the unknown base type as an error at that point. Needed by
`ArrayTest.test_untyped_list_cannot_be_subscripted` of
CPYTHON-ADAPT-12C-ROUNDTRIP-COMPOSITE, and by any client: one statement from one session
stops the whole server.

## 9. Experiments behind this design

Scripts, commands and complete output are in `.cswd/tmp/CPYTHON-DESIGN-ADAPT/`
(`run_all.sh` re-runs everything and writes `log.txt`). Environment: Linux x86_64, CPython
3.12.3, Go and Docker as on the host; worktree at commit `0c704a82ce`. Oracle mode: the
image `postgres:18.6-bookworm@sha256:afc7e2d4…` (`reference/fixture/fixture.go`, constant
`referenceImage`), reached through libpq 18.6 extracted from that image (PQ 9.3). Server
mode: `cmd/stuntgres` built with `-tags stuntgres_reduced_kdf` from the worktree, the same
libpq. The pq layer under the experiments is the scratch copy of PQ's designed code
(`scratch/stuntgres/pq`, copied from `.cswd/tmp/CPYTHON-DESIGN-PQ/scratch`); the code of
sections 2 to 7 is `scratch/stuntgres/`, the tests of 8.3 are `scratch/tests/`.

Limits of this run, stated where they matter: no CPython 3.10 or 3.11 was available and
none was fetched (the session forbids running downloaded files and pulling images), so
compatibility with 3.10 rests on `ast.parse(source, feature_version=(3, 10))` of every
file (passed) and on reading; nothing ran in native mode (libspq does not exist yet).

| # | Experiment | Command | Result |
|---|---|---|---|
| 1 | Text output of every core type at edge values, 850 lines per mode: minimum, maximum, zero, negative, NaN and infinities, BC and year limits, eight DateStyles, eighteen TimeZone settings (offsets with minutes and seconds), four IntervalStyles, quoting of every character class in arrays, empty and nested arrays, NULL elements, lower bounds, bytea in both output settings, extra_float_digits 0, 1, 3 | `python3 exp1/exp1.py oracle`, `... server`; outputs `exp1/oracle.txt`, `exp1/server.txt` | The literals of sections 4 to 6 and of the tests. Server mode differs in three places, none in the core set: `ARRAY[...]::box[]` (`42846 cannot cast type box[] to box[]`), `SET TimeZone = 'posixrules'` (`22023 invalid value`), and the `TimeZone` parameter reads `UTC` where the image says `Etc/UTC` (known from PQ). |
| 2 | For every Python type of the dump table and each candidate OID: `select $1`, `select pg_typeof($1)::text`, expressions, `insert ... returning` into several column types, `select ... where c = $1`; interval dumps in four IntervalStyles; date dumps in five DateStyles; 1494 lines | `python3 exp2/exp2.py oracle`; output `exp2/oracle.txt` (`exp2/first-run/` is the same with psycopg's integer rule) | Section 5.2. It changed one point: `int` starts at `int4`, not `int2` (A10). |
| 3 | The same statements in server mode | `python3 exp2/exp2.py server`; `diff exp2/oracle.txt exp2/server.txt`; `python3 exp2/crash.py oracle`, `... server` (`exp2/crash.txt`) | 1492 of 1494 lines identical. The two others are `select $1[1]` and `select $1[2]` with a parameter of unknown type: the server closes the connection, and `prepare p as select $1[1]` terminates the server process (`CPYTHON-ENGINE-03-SUBSCRIPT-UNKNOWN`). After the first occurrence the script does not send that statement again in server mode and says so in its output. |
| 4 | The converter against 75 placeholder cases, the cache, and 21 statements executed in both modes | `python3 exp4/exp4.py oracle`, `... server` | Sections 2.2 and 2.5. Identical in both modes. |
| 5 | The validation slice (rule D7): `_queries.prepare`, `Transformer.dump_sequence`, `PGconn.send_query_params` or `send_query`, the `execute` generator and the synchronous waiter of PQ, `set_pgresult`, `load_rows`; a 27-column table with every core type and four array types, NULLs, expressions, specials, session zones, DateStyle and IntervalStyle errors, a registered loader, dumper and array type, replaced JSON functions, errors that leave the connection usable | `python3 exp5/slice.py oracle`, `... server` | 67 of 67 checks pass in each mode. Arrays of `point`, `inet`, `bit`, `money` and `macaddr` are not supported by the stuntgres engine (`0A000`); they are outside the core set (plan U6, U10), so the registry test uses `"char"[]`, which both servers support. |
| 6 | Cost of converting 10000 cells and the number of loader lookups | `python3 exp6/exp6.py oracle`, `... server` | Section 3.7: loaders are chosen per column type when the result is set, never while rows load. |
| — | The designed test files with the designed runner of PQ (scratch copy) | `python3 scratch/tools/runtests.py --mode unit`, `--mode oracle`, `--mode server` | unit: every test passes; oracle: every test passes; server: every test passes but `ArrayTest.test_untyped_list_cannot_be_subscripted` (the engine gap). The pure tests also pass with the stub of 8.2 in place of `_defaults.py`. |

Server-mode differences and what was done (rule D8):

| Difference | Where it matters | Handling |
|---|---|---|
| Subscript of an untyped parameter closes the connection or stops the server | a list of `str` (or an empty list) used as `%s[1]` | `CPYTHON-ENGINE-03-SUBSCRIPT-UNKNOWN`, prerequisite of CPYTHON-ADAPT-12C-ROUNDTRIP-COMPOSITE |
| `TimeZone` is reported as `UTC`, the image reports `Etc/UTC` | tests of timestamptz | no literal zone name in tests (6.5); known since PQ |
| Arrays of `box`, `point`, `inet`, `bit`, `money`, `macaddr` are unsupported; `TimeZone` `posixrules` is rejected | nothing this design sends and no designed test | reported to the owner by the design part; outside the core set |

No experiment contradicted `docs/design/CPYTHON-PQ.md`; it is not changed.
