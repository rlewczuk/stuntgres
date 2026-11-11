# CPYTHON-DBAPI: the DB-API layer of the Python binding

Design of the public surface of the Python DB-API binding `bindings/cpython` (package
`stuntgres`): the module interface, the SQLSTATE exceptions and the diagnostics object,
connections, transactions, cursors, row factories, notices, notifications, cancellation,
the asyncio twins and the run of the vendored compliance suite. Produced by catalog part
`CPYTHON-DESIGN-DBAPI`. It builds on `docs/design/CPYTHON-PQ.md` (called **PQ** below; "PQ
7.5" is its section 7.5) and `docs/design/CPYTHON-ADAPT.md` (**ADAPT**), and is built on by
`CPYTHON-DESIGN-FEATURES` (COPY, named cursors, two-phase commit, cluster control, the
wheel). The contract for consumers is `docs/CPYTHON.md`.

Words used below: **(measured)** marks a value observed in the experiments of section 12;
**oracle mode** is real libpq 18.6 against the pinned PostgreSQL 18.6 image; **server
mode** is the same libpq against the stuntgres wire server; **unit mode** runs the tests
that need no native library. Every literal that depends on PostgreSQL or libpq (a command
tag, a status, an error text, a type modifier) was observed in oracle mode; literals that
follow from the binding's own algorithms (its own error texts, `repr` strings) were
produced by the designed code. That code, and every test file printed in section 11, ran
as scratch code in unit, oracle and server mode (section 12); each part's tests also ran
in a tree that holds only the files of the part's prerequisites (the "stage check" of
section 12), so the prerequisites listed in section 11.1 are sufficient.

Nothing under `bindings/` exists yet (`ls bindings` fails at commit `c1dcedc931`); the
parts of PQ 10.1 and ADAPT 8.1 create the lower layers. This design calls only names those
parts define.

## 1. Purpose, layering, layout

```
stuntgres/__init__.py          connect, apilevel, exceptions, type objects, classes     (2)
   connection.py               Connection: lock + synchronous waiter                  (9.2)
   connection_async.py         AsyncConnection: asyncio lock + asyncio waiter         (9.4)
   cursor.py, cursor_async.py  Cursor, AsyncCursor                               (9.3, 9.5)
   transaction.py, transaction_async.py   transaction() blocks                   (5.4, 9.6)
   _connection_base.py         BaseConnection = ConnCore + five mixins            (4.3-4.6)
      _conn_core.py            state, connect generator, checks                       (4.3)
      _conn_exec.py            command path, BEGIN, COMMIT, ROLLBACK, autocommit      (4.6)
      _conn_txnopts.py         isolation_level, read_only, deferrable                 (5.3)
      _conn_notices.py         notice handlers                                        (8.1)
      _conn_notify.py          notifications                                          (8.2)
      _conn_cancel.py          cancel, the way out of an interrupted wait        (8.3, 8.4)
   _cursor_base.py             BaseCursor = CurCore + four mixins                       (6)
      _cur_core.py             state, execute, description, rowcount                  (6.4)
      _cur_results.py          result check, COPY refused                             (6.3)
      _cur_fetch.py, _cur_many.py, _cur_nextset.py, _cur_optional.py             (6.5-6.9)
   _column.py                  Column                                                 (6.2)
   rows.py                     row factories                                            (7)
   conninfo.py, _connection_info.py   connection strings, ConnectionInfo         (4.1, 4.2)
   _diagnostic.py, _sqlstates.py      Diagnostic, error conversion, SQLSTATE classes    (3)
   _features.py                stub handed to CPYTHON-DESIGN-FEATURES                 (1.5)
stuntgres._queries, ._transformer, .adapt, ._defaults, .dbapi20            (ADAPT 2, 3, 7)
stuntgres.generators, .waiting, .waiting_async, .pq, .errors, ._encodings  (PQ 4.3, 5-8)
```

A layer imports only layers below it. Sans-IO stays the rule (plan A3, A7): **every
operation that talks to the server is a generator method of a base class**; the public
classes only take the connection's lock and hand the generator to a waiter. The base
classes hold all the state. There is no code generator and no duplicated logic: what the
synchronous and the asyncio class each contain is listed in section 9 and is, method by
method, "take the lock, wait for one generator".

### 1.1 Fixed points

| # | Point | Evidence |
|---|---|---|
| D1 | Standard library only, CPython 3.10 or newer, every module starts with `from __future__ import annotations` (rule C4). The designed code was parsed with `ast.parse(..., feature_version=(3, 10))` and ran on CPython 3.12.3; no Python 3.10 or 3.11 interpreter was available, so statements about them rest on reading and on that parse. | section 12 |
| D2 | `import stuntgres` maps no native library, starts no thread and imports neither `asyncio` nor the type adapters: the asyncio classes and `stuntgres.adapters` are resolved on first use (PEP 562). | **(measured)** `import stuntgres` takes 23 ms; `asyncio` would add 16 ms and the type adapters 6 ms; `/proc/self/maps` shows no native library (2) |
| D3 | `apilevel = "2.0"`, `threadsafety = 2`, `paramstyle = "pyformat"`. | PEP 249; plan A6 |
| D4 | `connect(conninfo="", *, autocommit=False, row_factory=None, cursor_factory=None, **kwargs)`. Keyword arguments are merged into the string, quoted, and validated through `pq.Conninfo.parse`. No SQL is sent at connect. `connect_timeout` bounds the waiter as libpq reads it (absent, zero, negative: no limit; 1 means 2 seconds). | **(measured)** 4.1; a listening socket that never answers gives `ConnectionTimeout` after 2.0 s with `connect_timeout=1` |
| D5 | Every exception for a server error is chosen by SQLSTATE: one class per SQLSTATE of PostgreSQL 18 Appendix A except the classes 00 and 01 (253 classes), each a subclass of the PEP 249 class of its SQLSTATE class; an unknown code gets the PEP 249 class of its two-character prefix, else `DatabaseError`. `sqlstate` and `diag` are always there. | **(measured)** 3; experiment 8: the class names equal psycopg 3's for all 253 codes |
| D6 | An exception keeps a copy of the error fields (`ErrorInfo`), never the native result: the result is released at once and the exception can be pickled. | **(measured)** 3.4; `tests/test_diagnostic.py`, `tests/test_module.py` |
| D7 | A result without SQLSTATE comes from the client library (a lost connection): `OperationalError` with libpq's wording. A connection whose status is no longer OK is `closed` and `broken`; every later operation raises `OperationalError("the connection is lost")`. | **(measured)** 3.5: `server closed the connection unexpectedly ...` |
| D8 | Transactions (plan A6): `autocommit` is `False` by default; before a statement in an idle session the connection sends `BEGIN` with the options of `isolation_level`, `read_only`, `deferrable`, alone, in a round trip of its own. Whether a transaction is open is always read from `pgconn.transaction_status`, never from a Python flag. `commit()` and `rollback()` send nothing when the session is idle. | **(measured)** experiment 1, section 5.1 |
| D9 | Changing `autocommit` or a transaction attribute inside a transaction is a `ProgrammingError`. `with conn:` commits or rolls back and then closes. `conn.transaction()` is a context manager: `BEGIN`/`COMMIT`/`ROLLBACK` at top level, `SAVEPOINT`/`RELEASE`/`ROLLBACK TO` when nested. | 5.2 to 5.4 |
| D10 | One lock per connection, held for the whole of one operation: `threading.Lock` in `Connection`, `asyncio.Lock` in `AsyncConnection`. Cursors of one connection share its transaction. A cursor is used by one thread (or task) at a time. | 9.1; **(measured)** four threads and four tasks sharing one connection |
| D11 | Outside the lock a connection calls exactly two things of the native library: `PGcancel.cancel()` on a handle made at connect time, and `PGconn.socket` (`docs/design/LIBSPQ-CABI.md` 7.2 allows only these beside a running call). `closed`, `broken`, `repr()`, `cursor()` and the finalizer answer from Python state. | 4.3 |
| D12 | Interruption: on `KeyboardInterrupt` during a synchronous wait, and on task cancellation during an asyncio wait, the connection cancels the statement, collects what it still returns (at most `CANCEL_TIMEOUT` = 5 s), stays usable and re-raises. If that fails or is interrupted again, the connection is closed (broken). | **(measured)** experiment 5, both modes, 8.4 |
| D13 | `close()` sends nothing and never waits for a statement to finish: when another thread or task is inside an operation it first cancels that statement, then closes; a listener in `notifies()` lets go of the connection every tenth of a second. An open transaction is rolled back by the server. | **(measured)** 8.2, 8.4, 9.2; `docs/LIBSPQ.md` 5.4 |
| D14 | `description` is a list of `Column` objects, each a 7-item sequence; `type_code` is the type OID; sizes, precision and scale come from `fmod` and `fsize` (6.2); `null_ok` is always `None`. | **(measured)** experiment 4 |
| D15 | `rowcount` is the count of the command tag (`PGresult.command_tuples`), else the number of rows of a row result, else -1. | **(measured)** experiment 3, 6.4 |
| D16 | `execute` without parameters sends the string with `send_query` and may hold several statements; `nextset()` walks their results. An error result anywhere makes `execute` raise and discards every result of the string. With parameters one statement is bound on the server. | **(measured)** experiment 2, 6.6 |
| D17 | `executemany` runs the statement once per parameter set, one round trip each, without pipeline mode; `rowcount` is the sum; `returning=True` keeps every result for `nextset()`. | 6.7 |
| D18 | `callproc` is **not** implemented (6.9). `setinputsizes` and `setoutputsize` do nothing. `lastrowid` is `None`. The optional error-handling extensions of PEP 249 (`messages`, `errorhandler`) are not implemented. | 6.9; experiment 7: the suite accepts a cursor without `callproc` |
| D19 | `execute` refuses a `COPY` statement with `ProgrammingError` and first abandons the COPY, so the session can take commands again. COPY support belongs to CPYTHON-DESIGN-FEATURES. | **(measured)** 6.3 |
| D20 | Row factories: a callable that takes the cursor and returns a row maker; `tuple_row` (default), `dict_row`, `namedtuple_row`; settable on the connection, in `cursor()` and on the cursor. | 7 |
| D21 | Notices go to the handlers registered with `add_notice_handler`, as a `Diagnostic`, while the library delivers them. Notifications go to the notify handlers, or, when none is registered, to a backlog that `notifies(timeout=, stop_after=)` yields from. They are read only when something consumes input: any statement, or `notifies()`. | **(measured)** experiment 6, 8.1, 8.2 |
| D22 | The client encoding is read from the `client_encoding` parameter status through `PGconn._encoding` (PQ 4.3, PQ 5.4): 40 names with a Python codec, default UTF8. `SQL_ASCII` is the codec `ascii`. An encoding without codec makes every operation raise `NotSupportedError`. | **(measured)** 4.7 |
| D23 | The vendored compliance suite runs unchanged: 36 cases, 33 as vendored, 3 with an override (10). | **(measured)** experiment 7: 36 of 36 pass in oracle and in server mode |
| D24 | No shared state relies on the GIL (1.4). | 1.4 |

Experiments decided what the part record had left open, and changed nothing an owner
decision fixed:

- **callproc is left out.** PostgreSQL has functions (`SELECT`) and procedures (`CALL`),
  PEP 249's contract (output parameters written back into the sequence) cannot be kept
  for either, psycopg 3 has no `callproc`, and the suite passes without it.
- **The named SQLSTATE classes are made in a loop from a table**, not written out as 253
  class statements: the generated module is 384 lines, one line per SQLSTATE, and imports
  in about 2 ms (3.3).
- **`closed`, `broken` and `repr()` do not call the library** (D11), because libspq
  forbids a second call on a connection while one runs.
- **`close()` cancels before it closes** when the connection is busy in another thread or
  task (D13): finishing a connection under a running call is forbidden for the same
  reason.
- **The cancel handle is made once, at connect time**, so that `cancel()` from another
  thread needs no call on the connection (8.3).
- **An exception carries a copy of the error fields**, not the native result (D6).
- **Parts were split** where a part exceeded about 400 lines (11.1).

Two server-mode differences were found on statements a designed test sends
**(measured)**, and are filed under rule D8 as prerequisites of the parts whose tests
send them:

| Difference | PostgreSQL 18.6 | stuntgres server | Part |
|---|---|---|---|
| Command tag of `START TRANSACTION` | `START TRANSACTION` | `BEGIN` | `CPYTHON-ENGINE-04-START-TRANSACTION-TAG` |
| Column name of a cast to an array type, `select '{1}'::int4[]` | `int4` | `_int4` | `CPYTHON-ENGINE-05-ARRAY-CAST-COLUMN-NAME` |

Everything else the binding sends, and every other statement of the designed tests,
answered identically in both modes. Differences seen only in probes that no designed test
needs are listed in section 12.

### 1.2 Files

Everything is under `bindings/cpython/`. "Owner" is the one part that writes the final
file; a stub is created earlier by the part named in 1.3 and replaced as a whole by its
owner. `CPYTHON-` is omitted from the part names.

| File | Responsibility | Owner |
|---|---|---|
| `tools/sqlstates.txt` | The SQLSTATE table: data (3.2). | ERRORS-02-SQLSTATE |
| `tools/gen_sqlstates.py` | Writes `stuntgres/_sqlstates.py` from the table (3.3). | ERRORS-02-SQLSTATE |
| `stuntgres/_sqlstates.py` | Generated: the 253 SQLSTATE classes, `lookup`, `class_for` (3.3). | ERRORS-02-SQLSTATE |
| `stuntgres/_diagnostic.py` | `ErrorInfo`, `Diagnostic`, `error_from_result` (3.4, 3.5). | ERRORS-01-DIAGNOSTIC |
| `stuntgres/rows.py` | Row factories (7). | ROWS-01-FACTORIES |
| `stuntgres/conninfo.py` | `make_conninfo`, `conninfo_to_dict`, `connect_timeout` (4.1). | CONN-01A-CONNINFO |
| `stuntgres/_connection_info.py` | `ConnectionInfo` (4.2). | CONN-01B-INFO |
| `stuntgres/_conn_core.py` | `ConnCore`, `IsolationLevel`, `Notify`, `NotifyWindow`, `CANCEL_TIMEOUT`, `NOTIFY_INTERVAL`, `logger` (4.3). | CONN-01C-CORE |
| `stuntgres/_features.py` | Stub handed to CPYTHON-DESIGN-FEATURES (1.5). | CONN-01D-BASE |
| `stuntgres/_connection_base.py` | `BaseConnection`: the composition (4.4). | CONN-01D-BASE |
| `stuntgres/connection.py` | `Connection` (9.2). | CONN-01E-SYNC |
| `stuntgres/_conn_exec.py` | `ExecMixin`: command path, autocommit (4.6). | replaced by CONN-02-EXEC (stub by CONN-01D-BASE) |
| `tests/dbapi_util.py` | Test helpers: `run`, `status`, `ConnTestCase` (11.3). | CONN-02-EXEC |
| `stuntgres/_conn_txnopts.py` | `TxnOptionsMixin` (5.3). | replaced by CONN-03-TXN-OPTIONS (stub by CONN-01D-BASE) |
| `stuntgres/transaction.py` | `Rollback`, `BaseTransaction`, `Transaction`, `quote_identifier` (5.4). | CONN-04-TRANSACTION |
| `stuntgres/_conn_notices.py` | `NoticesMixin` (8.1). | replaced by CONN-05-NOTICES (stub by CONN-01D-BASE) |
| `stuntgres/_conn_notify.py` | `NotifyMixin` (8.2). | replaced by CONN-06-NOTIFY (stub by CONN-01D-BASE) |
| `stuntgres/_conn_cancel.py` | `CancelMixin` (8.3). | replaced by CONN-07-CANCEL (stub by CONN-01D-BASE) |
| `stuntgres/_column.py` | `Column` (6.2). | CUR-01A-COLUMN |
| `stuntgres/_cur_results.py` | `check_results`, `end_copy` (6.3). | CUR-01B-RESULTS |
| `stuntgres/_cur_core.py` | `CurCore`: state, `execute` (6.1, 6.4). | CUR-01C-CORE |
| `stuntgres/_cursor_base.py` | `BaseCursor`: the composition (6.10). | CUR-01C-CORE |
| `stuntgres/cursor.py` | `Cursor` (9.3). | CUR-01C-CORE |
| `stuntgres/_cur_fetch.py` | `FetchMixin` (6.5). | replaced by CUR-02-FETCH (stub by CUR-01C-CORE) |
| `stuntgres/_cur_nextset.py` | `NextsetMixin` (6.8). | replaced by CUR-04-NEXTSET (stub by CUR-01C-CORE) |
| `stuntgres/_cur_many.py` | `ManyMixin`: `executemany` (6.7). | replaced by CUR-03-EXECUTEMANY (stub by CUR-01C-CORE) |
| `stuntgres/_cur_optional.py` | `OptionalMixin` (6.9). | replaced by CUR-05-OPTIONAL (stub by CUR-01C-CORE) |
| `stuntgres/connection_async.py` | `AsyncConnection` (9.4). | ASYNC-01-CONNECTION |
| `tests/dbapi_util_async.py` | Test helpers: `arun`, `AsyncConnTestCase` (11.3). | ASYNC-01-CONNECTION |
| `stuntgres/cursor_async.py` | `AsyncCursor` (9.5). | ASYNC-02-CURSOR |
| `stuntgres/transaction_async.py` | `AsyncTransaction` (9.6). | ASYNC-03-TRANSACTION |
| `stuntgres/__init__.py` | The module surface (2). Created with the version only by CPYTHON-BUILD-01-SKELETON. | MODULE-01-EXPORTS |
| `tests/test_<name>.py` | One test file per part (11.1 names each). | the part |

No file of PQ 1.2 or ADAPT 1.2 is edited, with one planned exception: `stuntgres/__init__.py`,
which CPYTHON-BUILD-01-SKELETON creates with the version only and hands to
CPYTHON-MODULE-01-EXPORTS (PQ 1.2). `stuntgres/errors.py` is not edited: its extension
points are filled at run time (3).

### 1.3 Stubs

Two classes are composed of mixins that live in files of their own, so that every file
has one owner and parts without an ordering edge never touch the same file. The part that
composes a class creates the mixin files it does not own as stubs; each later part
replaces the whole file it owns and nothing else.

CPYTHON-CONN-01D-BASE creates five stubs, identical but for the names
(`_conn_exec.py` / `ExecMixin` / CPYTHON-CONN-02-EXEC,
`_conn_txnopts.py` / `TxnOptionsMixin` / CPYTHON-CONN-03-TXN-OPTIONS,
`_conn_notices.py` / `NoticesMixin` / CPYTHON-CONN-05-NOTICES,
`_conn_notify.py` / `NotifyMixin` / CPYTHON-CONN-06-NOTIFY,
`_conn_cancel.py` / `CancelMixin` / CPYTHON-CONN-07-CANCEL):

```python
"""Stub: replaced by CPYTHON-CONN-02-EXEC."""
from __future__ import annotations

from ._conn_core import ConnCore


class ExecMixin(ConnCore):
    """Members are added by CPYTHON-CONN-02-EXEC."""
```

CPYTHON-CUR-01C-CORE creates four stubs, identical but for the names
(`_cur_fetch.py` / `FetchMixin` / CPYTHON-CUR-02-FETCH,
`_cur_many.py` / `ManyMixin` / CPYTHON-CUR-03-EXECUTEMANY,
`_cur_nextset.py` / `NextsetMixin` / CPYTHON-CUR-04-NEXTSET,
`_cur_optional.py` / `OptionalMixin` / CPYTHON-CUR-05-OPTIONAL):

```python
"""Stub: replaced by CPYTHON-CUR-02-FETCH."""
from __future__ import annotations

from ._cur_core import CurCore


class FetchMixin(CurCore):
    """Members are added by CPYTHON-CUR-02-FETCH."""
```

The public classes are complete from their first part: `connection.py` and `cursor.py`
already contain every thin method of section 9. A method whose generator lives in a stub
raises `AttributeError` until the owner of the stub lands (for example `Connection.commit`
before CPYTHON-CONN-02-EXEC). That is the state "the test fails first" of rule C7 for
each of those parts **(measured: the stage check with `--pre`, section 12)**. The stubs
are safe to import and to compose: `ConnCore` defines `_begin_statement`, `_on_notice`,
`_on_notify` and `_assign` with defaults that the mixins override.

A third hand-over file, `stuntgres/_features.py`, is a stub for the next design (1.5).

### 1.4 Rules for every module of this design

- **Private names are fixed.** Names with a leading underscore are used across files and
  by the tests; they are part of this design, not consumer API. Text of error messages is
  part of the design too: tests compare it literally.
- **`__module__`.** `Connection`, `AsyncConnection`, `Cursor`, `AsyncCursor`, `Column`,
  `ConnectionInfo`, `Transaction`, `AsyncTransaction` and `Rollback` set
  `__module__ = "stuntgres"`; `Diagnostic` and the SQLSTATE classes say
  `"stuntgres.errors"`. Until CPYTHON-MODULE-01-EXPORTS lands the names are not
  attributes of the package; that affects only pickling of the base exception classes of
  PQ 6 (which say `"stuntgres"` since PQ), so the pickling test of those is in
  `tests/test_module.py`.
- **The lock.** A generator method of a base class assumes that its caller holds the
  connection's lock; it never takes it. The public classes take it for exactly one
  generator (9.1). Methods that do no I/O and touch only one cursor (the fetch methods,
  `nextset`, `scroll`, `description`) take no lock.
- **No reliance on the GIL** (free-threaded CPython, 3.13t and later; ctypes releases the
  GIL around every call anyway). Shared state is either immutable after construction or
  guarded:

  | State | Shared between | How it is safe |
  |---|---|---|
  | the loader, the declaration table | all threads | PQ 2.3: one lock |
  | `_queries._convert_cached`, `rows._row_class`, `types.timestamp.session_zone` | all threads | `functools.lru_cache` (locked internally) |
  | `stuntgres.adapters` and every `AdaptersMap` | all threads | ADAPT 3.2: writers serialised by a lock, dictionaries replaced, never changed |
  | `_sqlstates._BY_CODE`, `_BASES`, the classes in `stuntgres.errors` | all threads | filled while the module is imported (the import lock), read-only afterwards |
  | `errors._diag_factory`, `errors.Diagnostic`, `errors.lookup` | all threads | assigned once at import |
  | a connection's session state, its `PGconn` | threads using one connection | the connection's lock (D10), with the two exceptions of D11 |
  | `_closed`, `_broken` | any thread | single attribute writes of `True`; read without the library |
  | `_notice_handlers`, `_notify_handlers` | any thread | tuples replaced under `_handlers_lock`; readers take one snapshot |
  | `_notify_backlog` | the thread inside an operation appends, the consumer of `notifies()` pops | `collections.deque`: `append` and `popleft` are atomic |
  | a cursor's results and position | none | a cursor belongs to one thread or task at a time (PEP 249 level 2 shares connections, not cursors) |
  | a `Transformer`, a `PGresult` being read | none / any | ADAPT 1.3; results are immutable (PQ 5) |

- **Handlers** (notice, notify) run inside an operation of their connection, with its lock
  held. They must not use that connection (`docs/LIBSPQ.md` 5.6); they may use others. An
  exception they raise is logged on the logger `stuntgres` at level `ERROR` and not
  raised.
- **Logging.** One logger, `logging.getLogger("stuntgres")` (`_conn_core.logger`, the same
  object PQ 5.9 uses). The package never prints.
- **Imports.** No module of this design imports `asyncio` except `connection_async.py`
  (and `waiting_async.py` below it). `stuntgres/types/` is a package whose modules are
  named like standard-library modules: never run Python with `bindings/cpython/stuntgres`
  as the current or script directory (ADAPT 1.3) **(measured again: a script started
  there failed in `import re`)**.

### 1.5 What is left for CPYTHON-DESIGN-FEATURES

`stuntgres/_features.py` is created by CPYTHON-CONN-01D-BASE with six empty classes and
an empty `__all__`, and is handed to the parts of CPYTHON-DESIGN-FEATURES, which replace
it (one owner there; that part lists CPYTHON-CONN-01D-BASE and
CPYTHON-MODULE-01-EXPORTS as prerequisites):

```python
"""Stub: handed to the parts of CPYTHON-DESIGN-FEATURES.

The classes are mixed into the connection and cursor classes; COPY, named
cursors, two-phase commit and cluster control add their members here.
"""
from __future__ import annotations

__all__: list[str] = []


class BaseConnectionFeatures:
    """Members shared by Connection and AsyncConnection."""


class ConnectionFeatures:
    """Members of Connection."""


class AsyncConnectionFeatures:
    """Members of AsyncConnection."""


class BaseCursorFeatures:
    """Members shared by Cursor and AsyncCursor."""


class CursorFeatures:
    """Members of Cursor."""


class AsyncCursorFeatures:
    """Members of AsyncCursor."""
```

The six classes are the first base of the class they extend, so a member defined there
is found first:

| Stub class | First base of | Meant for |
|---|---|---|
| `BaseConnectionFeatures` | `BaseConnection` | generators and state of two-phase commit |
| `ConnectionFeatures` | `Connection` | `tpc_*` methods, `xid`, `server_cursor_factory` |
| `AsyncConnectionFeatures` | `AsyncConnection` | the asyncio twins of those |
| `BaseCursorFeatures` | `BaseCursor` | COPY generators |
| `CursorFeatures` | `Cursor` | `copy()` |
| `AsyncCursorFeatures` | `AsyncCursor` | the asyncio `copy()` |

Hooks already in this design, each covered by a test of the part named:

| Hook | Where | Behavior now |
|---|---|---|
| `__all__` of `_features` | `stuntgres/__init__.py` copies every name listed there into the package and appends it to its own `__all__` (2) | nothing is added |
| `server_cursor_factory` | `Connection.cursor(name, ...)` and `AsyncConnection.cursor(name, ...)` call `self.server_cursor_factory(self, name, row_factory=..., scrollable=..., withhold=...)` when `name` is not empty (9.2) | the attribute does not exist: `NotSupportedError("named cursors are not available")` (`tests/test_connection.py`) |
| `ConnCore._tpc` | `None`; when it is not `None`, `commit()`, `rollback()` and `transaction()` raise `ProgrammingError` (4.6, 5.4) | always `None` (`tests/test_conn_exec.py`, `tests/test_transaction.py`) |
| COPY results | `generators.fetch_many` stops at a COPY status (PQ 7.5); `_cur_results.check_results` abandons the COPY and raises (6.3) | `execute("copy ...")` is refused; a `copy()` method uses `fetch_many` itself and never reaches the check |
| cursor internals | `CurCore._check_usable`, `_reset`, `_convert`, `_send`, `_set_results`, `_select_result` (6.4) | the building blocks a COPY or a server-side cursor needs |
| connection internals | `_start_query`, `_exec_command`, `_check_open`, `wait`, `lock` (4.3, 4.6, 9.1) | the same for two-phase commit and cluster control |
| `docs/CPYTHON.md` | sections "COPY", "Server-side cursors", "Two-phase commit", "Test isolation helpers", "Building and testing" | present as headings that say which design fills them |

## 2. Module interface and the export list

`stuntgres/__init__.py` is the PEP 249 module. Its globals:

| Name | Value |
|---|---|
| `apilevel` | `"2.0"` |
| `threadsafety` | `2`: threads may share the module and connections, not cursors |
| `paramstyle` | `"pyformat"`: `%s` and `%(name)s` (ADAPT 2). PostgreSQL's own `$1` is not PEP 249's `numeric` style (`:1`), which is why the driver converts |
| `connect` | `Connection.connect` (9.2) |
| `__version__` | from `stuntgres/_version.py` (PQ 1.2) |

The export list (`__all__`, 42 names, sorted by `sorted()`):

| Names | Defined in | Section |
|---|---|---|
| `Warning`, `Error`, `InterfaceError`, `DatabaseError`, `DataError`, `OperationalError`, `IntegrityError`, `InternalError`, `ProgrammingError`, `NotSupportedError` | `errors.py` | PQ 6 |
| `STRING`, `BINARY`, `NUMBER`, `DATETIME`, `ROWID`, `Date`, `Time`, `Timestamp`, `DateFromTicks`, `TimeFromTicks`, `TimestampFromTicks`, `Binary` | `dbapi20.py` | ADAPT 7 |
| `Connection`, `Cursor` | `connection.py`, `cursor.py` | 9.2, 9.3 |
| `AsyncConnection`, `AsyncCursor`, `AsyncTransaction` (lazy) | `connection_async.py`, `cursor_async.py`, `transaction_async.py` | 9.4 to 9.6 |
| `Transaction`, `Rollback` | `transaction.py` | 5.4 |
| `Column`, `ConnectionInfo`, `IsolationLevel`, `Notify` | `_column.py`, `_connection_info.py`, `_conn_core.py` | 6.2, 4.2, 4.3 |
| `adapters` (lazy) | `_defaults.py` | ADAPT 3.6, 6.11 |
| `errors`, `rows`, `pq` | the submodules | 3, 7, PQ 5 |
| `apilevel`, `threadsafety`, `paramstyle`, `connect`, `__version__` | here | above |

Not in `__all__` but public as submodules: `stuntgres.adapt` and `stuntgres.types.*`
(ADAPT 3.6), `stuntgres.conninfo` (4.1), `stuntgres.dbapi20`. `stuntgres.errors` holds,
besides the classes of PQ 6, `Diagnostic`, `lookup` and the 253 SQLSTATE classes (3):
`except stuntgres.errors.UniqueViolation` works after `import stuntgres` and after
`import stuntgres.errors`, because either runs this file, which imports `_sqlstates`.

**Lazy names.** `AsyncConnection`, `AsyncCursor`, `AsyncTransaction` and `adapters` are
resolved by a module-level `__getattr__` on first access and then stored in the module's
globals. So `import stuntgres` imports neither `asyncio` nor the nine type modules
**(measured: 23 ms for `import stuntgres` in a fresh interpreter, of which the SQLSTATE
classes take about 2 ms; importing `asyncio` afterwards costs another 16 ms and
`stuntgres._defaults` another 6 ms)**. `from stuntgres import *` and `from stuntgres import
AsyncConnection` work, because both go through `__getattr__`. The first connection
imports `_defaults` (through `adapt.context_adapters(None)`, ADAPT 3.3).

**How the names reach this file.** One part writes it: CPYTHON-MODULE-01-EXPORTS, after
every other part of this design. Until then the file is the version-only file of
CPYTHON-BUILD-01-SKELETON, and each part is tested through its own module
(`from stuntgres.connection import Connection`, and so on); no part but this one edits
the file. The names of the next design (COPY, named cursors, two-phase commit, cluster
control) arrive without another edit: the file copies every name of
`stuntgres._features.__all__` (1.5).

What `import stuntgres` imports: `errors`, `pq` (its enumerations only), `rows`,
`_features`, `_sqlstates` (which installs the SQLSTATE classes and `lookup` into
`errors`), `_column`, `_conn_core`, `_connection_info`, `connection` (which imports the
connection mixins, among them `_conn_exec`, which imports `_diagnostic`, which installs
`errors.Diagnostic` and the diagnostics factory), `cursor`, `dbapi20` and `transaction`.
No native library is loaded: the loader runs at the first `connect` (PQ 2).

The complete file:

```python
"""stuntgres: a PEP 249 driver for the embedded stuntgres engine."""
from __future__ import annotations

import importlib
from typing import TYPE_CHECKING, Any

from . import _features, _sqlstates, errors, pq, rows
from ._column import Column
from ._conn_core import IsolationLevel, Notify
from ._connection_info import ConnectionInfo
from ._version import __version__
from .connection import Connection
from .cursor import Cursor
from .dbapi20 import BINARY, DATETIME, NUMBER, ROWID, STRING, Binary, Date
from .dbapi20 import DateFromTicks, Time, TimeFromTicks, Timestamp
from .dbapi20 import TimestampFromTicks
from .errors import DatabaseError, DataError, Error, IntegrityError
from .errors import InterfaceError, InternalError, NotSupportedError
from .errors import OperationalError, ProgrammingError, Warning
from .transaction import Rollback, Transaction

if TYPE_CHECKING:
    from ._defaults import adapters
    from .connection_async import AsyncConnection
    from .cursor_async import AsyncCursor
    from .transaction_async import AsyncTransaction

apilevel = "2.0"
threadsafety = 2
paramstyle = "pyformat"

connect = Connection.connect

# Resolved on first use: asyncio and the type adapters are not imported before.
_LAZY = {
    "AsyncConnection": "connection_async",
    "AsyncCursor": "cursor_async",
    "AsyncTransaction": "transaction_async",
    "adapters": "_defaults",
}

__all__ = [
    "AsyncConnection", "AsyncCursor", "AsyncTransaction", "BINARY", "Binary",
    "Column", "Connection", "ConnectionInfo", "Cursor", "DATETIME", "DataError",
    "DatabaseError", "Date", "DateFromTicks", "Error", "IntegrityError",
    "InterfaceError", "InternalError", "IsolationLevel", "NUMBER",
    "NotSupportedError", "Notify", "OperationalError", "ProgrammingError",
    "ROWID", "Rollback", "STRING", "Time", "TimeFromTicks", "Timestamp",
    "TimestampFromTicks", "Transaction", "Warning", "__version__", "adapters",
    "apilevel", "connect", "errors", "paramstyle", "pq", "rows", "threadsafety",
]

# The names CPYTHON-DESIGN-FEATURES adds (COPY, named cursors, two-phase commit,
# cluster control) arrive through stuntgres/_features.py.
globals().update({name: getattr(_features, name) for name in _features.__all__})
__all__ += _features.__all__


def __getattr__(name: str) -> Any:
    module = _LAZY.get(name)
    if module is None:
        raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
    value = getattr(importlib.import_module("." + module, __name__), name)
    globals()[name] = value
    return value
```

## 3. Errors

`stuntgres/errors.py` (PQ 6) has the PEP 249 classes and is never edited. This design
fills its extension points at run time: `_sqlstates.py` adds the SQLSTATE classes and
`lookup`, `_diagnostic.py` adds `Diagnostic` and the factory behind `Error.diag`.

### 3.1 The mapping

**PEP 249 class of a SQLSTATE class** (the first two characters of the code). This is
the table psycopg 3 documents; classes it does not list give `DatabaseError`.

| PEP 249 class | SQLSTATE classes |
|---|---|
| `OperationalError` | `08`, `27`, `28`, `2F`, `38`, `39`, `3B`, `40`, `53`, `54`, `55`, `57`, `58`, `F0`, `HV` |
| `ProgrammingError` | `10`, `20`, `21`, `26`, `34`, `3D`, `3F`, `42`, `44`, `P0` |
| `InternalError` | `24`, `25`, `2B`, `2D`, `XX` |
| `DataError` | `22` |
| `IntegrityError` | `23` |
| `NotSupportedError` | `0A` |
| `DatabaseError` | `02`, `03`, `09`, `0B`, `0F`, `0L`, `0P`, `0Z` and every prefix not listed |

**One class per SQLSTATE.** For each of the 253 codes of the 41 classes of PostgreSQL
18's Appendix A other than `00` (successful completion) and `01` (warning), which are
never errors, there is a class in `stuntgres.errors`:

- its name is the condition name in CamelCase: `unique_violation` -> `UniqueViolation`,
  `sqlclient_unable_to_establish_sqlconnection` ->
  `SqlclientUnableToEstablishSqlconnection`;
- a name that `errors.py` already defines gets a trailing underscore: `XX000`
  `internal_error` -> `InternalError_`;
- a condition name that occurs a second time gets the suffix `Ext`: `38002`
  `ModifyingSqlDataNotPermittedExt`, `38003` `ProhibitedSqlStatementAttemptedExt`,
  `38004` `ReadingSqlDataNotPermittedExt`, `39004` `NullValueNotAllowedExt` (the first
  occurrences are `2F002`, `2F003`, `2F004`, `22004`);
- its only base is the PEP 249 class of its SQLSTATE class;
- class attributes: `sqlstate` (the code), `__module__ = "stuntgres.errors"`,
  `__doc__ = "The server reported SQLSTATE <code>."`.

These are the names psycopg 3 uses for the same codes **(measured: equal for all 253;
psycopg has one more, `72000`, which PostgreSQL 17 removed)**.

**Choosing the class for a SQLSTATE** (`_sqlstates.class_for`): the class of the exact
code; else the PEP 249 class of its first two characters; else `DatabaseError`. A code
without a class of its own (`ZZ123` from `RAISE ... USING ERRCODE`, `23999`) therefore
still gets the right PEP 249 class, and the instance carries the code in `sqlstate`.

### 3.2 The table (`tools/sqlstates.txt`)

The table is data: the class prefixes, the PEP 249 class chosen above, the class titles
and each code with its condition name, in the order of the manual. It was derived from
`errcodes.txt` of the pinned PostgreSQL 18.6 image (the source of Appendix A; SHA-256
`6e8de346643ba84aa3c9c6a73360acfc7b2dfb89162c06c08ce9bf5bcd5bbcba`) by
`.cswd/tmp/CPYTHON-DESIGN-DBAPI/exp8/derive.py`, which keeps only those facts: no comment
and no macro name of the source is copied. 301 lines: 7 comment lines, 41 `class` lines,
253 code lines. A line is `class <prefix> <PEP 249 class> <title>` or
`<SQLSTATE> <condition name>`; `#` starts a comment.

The complete file (SHA-256 `149afd6f05ebdf7bae3f6b7c49d9edc217f8d0edc2c0ed35fdd01ec140c4cc01`):

```
# SQLSTATE codes of PostgreSQL 18: the table of the manual's Appendix A
# (https://www.postgresql.org/docs/18/errcodes-appendix.html) without the classes
# 00 (successful completion) and 01 (warning), which are never errors.
# Input of tools/gen_sqlstates.py, which writes stuntgres/_sqlstates.py.
#
# class <prefix> <PEP 249 base class> <title of the class>
# <SQLSTATE> <condition name>
class 02 DatabaseError No Data
02000 no_data
02001 no_additional_dynamic_result_sets_returned
class 03 DatabaseError SQL Statement Not Yet Complete
03000 sql_statement_not_yet_complete
class 08 OperationalError Connection Exception
08000 connection_exception
08003 connection_does_not_exist
08006 connection_failure
08001 sqlclient_unable_to_establish_sqlconnection
08004 sqlserver_rejected_establishment_of_sqlconnection
08007 transaction_resolution_unknown
08P01 protocol_violation
class 09 DatabaseError Triggered Action Exception
09000 triggered_action_exception
class 0A NotSupportedError Feature Not Supported
0A000 feature_not_supported
class 0B DatabaseError Invalid Transaction Initiation
0B000 invalid_transaction_initiation
class 0F DatabaseError Locator Exception
0F000 locator_exception
0F001 invalid_locator_specification
class 0L DatabaseError Invalid Grantor
0L000 invalid_grantor
0LP01 invalid_grant_operation
class 0P DatabaseError Invalid Role Specification
0P000 invalid_role_specification
class 0Z DatabaseError Diagnostics Exception
0Z000 diagnostics_exception
0Z002 stacked_diagnostics_accessed_without_active_handler
class 10 ProgrammingError XQuery Error
10608 invalid_argument_for_xquery
class 20 ProgrammingError Case Not Found
20000 case_not_found
class 21 ProgrammingError Cardinality Violation
21000 cardinality_violation
class 22 DataError Data Exception
22000 data_exception
2202E array_subscript_error
22021 character_not_in_repertoire
22008 datetime_field_overflow
22012 division_by_zero
22005 error_in_assignment
2200B escape_character_conflict
22022 indicator_overflow
22015 interval_field_overflow
2201E invalid_argument_for_logarithm
22014 invalid_argument_for_ntile_function
22016 invalid_argument_for_nth_value_function
2201F invalid_argument_for_power_function
2201G invalid_argument_for_width_bucket_function
22018 invalid_character_value_for_cast
22007 invalid_datetime_format
22019 invalid_escape_character
2200D invalid_escape_octet
22025 invalid_escape_sequence
22P06 nonstandard_use_of_escape_character
22010 invalid_indicator_parameter_value
22023 invalid_parameter_value
22013 invalid_preceding_or_following_size
2201B invalid_regular_expression
2201W invalid_row_count_in_limit_clause
2201X invalid_row_count_in_result_offset_clause
2202H invalid_tablesample_argument
2202G invalid_tablesample_repeat
22009 invalid_time_zone_displacement_value
2200C invalid_use_of_escape_character
2200G most_specific_type_mismatch
22004 null_value_not_allowed
22002 null_value_no_indicator_parameter
22003 numeric_value_out_of_range
2200H sequence_generator_limit_exceeded
22026 string_data_length_mismatch
22001 string_data_right_truncation
22011 substring_error
22027 trim_error
22024 unterminated_c_string
2200F zero_length_character_string
22P01 floating_point_exception
22P02 invalid_text_representation
22P03 invalid_binary_representation
22P04 bad_copy_file_format
22P05 untranslatable_character
2200L not_an_xml_document
2200M invalid_xml_document
2200N invalid_xml_content
2200S invalid_xml_comment
2200T invalid_xml_processing_instruction
22030 duplicate_json_object_key_value
22031 invalid_argument_for_sql_json_datetime_function
22032 invalid_json_text
22033 invalid_sql_json_subscript
22034 more_than_one_sql_json_item
22035 no_sql_json_item
22036 non_numeric_sql_json_item
22037 non_unique_keys_in_a_json_object
22038 singleton_sql_json_item_required
22039 sql_json_array_not_found
2203A sql_json_member_not_found
2203B sql_json_number_not_found
2203C sql_json_object_not_found
2203D too_many_json_array_elements
2203E too_many_json_object_members
2203F sql_json_scalar_required
2203G sql_json_item_cannot_be_cast_to_target_type
class 23 IntegrityError Integrity Constraint Violation
23000 integrity_constraint_violation
23001 restrict_violation
23502 not_null_violation
23503 foreign_key_violation
23505 unique_violation
23514 check_violation
23P01 exclusion_violation
class 24 InternalError Invalid Cursor State
24000 invalid_cursor_state
class 25 InternalError Invalid Transaction State
25000 invalid_transaction_state
25001 active_sql_transaction
25002 branch_transaction_already_active
25008 held_cursor_requires_same_isolation_level
25003 inappropriate_access_mode_for_branch_transaction
25004 inappropriate_isolation_level_for_branch_transaction
25005 no_active_sql_transaction_for_branch_transaction
25006 read_only_sql_transaction
25007 schema_and_data_statement_mixing_not_supported
25P01 no_active_sql_transaction
25P02 in_failed_sql_transaction
25P03 idle_in_transaction_session_timeout
25P04 transaction_timeout
class 26 ProgrammingError Invalid SQL Statement Name
26000 invalid_sql_statement_name
class 27 OperationalError Triggered Data Change Violation
27000 triggered_data_change_violation
class 28 OperationalError Invalid Authorization Specification
28000 invalid_authorization_specification
28P01 invalid_password
class 2B InternalError Dependent Privilege Descriptors Still Exist
2B000 dependent_privilege_descriptors_still_exist
2BP01 dependent_objects_still_exist
class 2D InternalError Invalid Transaction Termination
2D000 invalid_transaction_termination
class 2F OperationalError SQL Routine Exception
2F000 sql_routine_exception
2F005 function_executed_no_return_statement
2F002 modifying_sql_data_not_permitted
2F003 prohibited_sql_statement_attempted
2F004 reading_sql_data_not_permitted
class 34 ProgrammingError Invalid Cursor Name
34000 invalid_cursor_name
class 38 OperationalError External Routine Exception
38000 external_routine_exception
38001 containing_sql_not_permitted
38002 modifying_sql_data_not_permitted
38003 prohibited_sql_statement_attempted
38004 reading_sql_data_not_permitted
class 39 OperationalError External Routine Invocation Exception
39000 external_routine_invocation_exception
39001 invalid_sqlstate_returned
39004 null_value_not_allowed
39P01 trigger_protocol_violated
39P02 srf_protocol_violated
39P03 event_trigger_protocol_violated
class 3B OperationalError Savepoint Exception
3B000 savepoint_exception
3B001 invalid_savepoint_specification
class 3D ProgrammingError Invalid Catalog Name
3D000 invalid_catalog_name
class 3F ProgrammingError Invalid Schema Name
3F000 invalid_schema_name
class 40 OperationalError Transaction Rollback
40000 transaction_rollback
40002 transaction_integrity_constraint_violation
40001 serialization_failure
40003 statement_completion_unknown
40P01 deadlock_detected
class 42 ProgrammingError Syntax Error or Access Rule Violation
42000 syntax_error_or_access_rule_violation
42601 syntax_error
42501 insufficient_privilege
42846 cannot_coerce
42803 grouping_error
42P20 windowing_error
42P19 invalid_recursion
42830 invalid_foreign_key
42602 invalid_name
42622 name_too_long
42939 reserved_name
42804 datatype_mismatch
42P18 indeterminate_datatype
42P21 collation_mismatch
42P22 indeterminate_collation
42809 wrong_object_type
428C9 generated_always
42703 undefined_column
42883 undefined_function
42P01 undefined_table
42P02 undefined_parameter
42704 undefined_object
42701 duplicate_column
42P03 duplicate_cursor
42P04 duplicate_database
42723 duplicate_function
42P05 duplicate_prepared_statement
42P06 duplicate_schema
42P07 duplicate_table
42712 duplicate_alias
42710 duplicate_object
42702 ambiguous_column
42725 ambiguous_function
42P08 ambiguous_parameter
42P09 ambiguous_alias
42P10 invalid_column_reference
42611 invalid_column_definition
42P11 invalid_cursor_definition
42P12 invalid_database_definition
42P13 invalid_function_definition
42P14 invalid_prepared_statement_definition
42P15 invalid_schema_definition
42P16 invalid_table_definition
42P17 invalid_object_definition
class 44 ProgrammingError WITH CHECK OPTION Violation
44000 with_check_option_violation
class 53 OperationalError Insufficient Resources
53000 insufficient_resources
53100 disk_full
53200 out_of_memory
53300 too_many_connections
53400 configuration_limit_exceeded
class 54 OperationalError Program Limit Exceeded
54000 program_limit_exceeded
54001 statement_too_complex
54011 too_many_columns
54023 too_many_arguments
class 55 OperationalError Object Not In Prerequisite State
55000 object_not_in_prerequisite_state
55006 object_in_use
55P02 cant_change_runtime_param
55P03 lock_not_available
55P04 unsafe_new_enum_value_usage
class 57 OperationalError Operator Intervention
57000 operator_intervention
57014 query_canceled
57P01 admin_shutdown
57P02 crash_shutdown
57P03 cannot_connect_now
57P04 database_dropped
57P05 idle_session_timeout
class 58 OperationalError System Error
58000 system_error
58030 io_error
58P01 undefined_file
58P02 duplicate_file
58P03 file_name_too_long
class F0 OperationalError Configuration File Error
F0000 config_file_error
F0001 lock_file_exists
class HV OperationalError Foreign Data Wrapper Error
HV000 fdw_error
HV005 fdw_column_name_not_found
HV002 fdw_dynamic_parameter_value_needed
HV010 fdw_function_sequence_error
HV021 fdw_inconsistent_descriptor_information
HV024 fdw_invalid_attribute_value
HV007 fdw_invalid_column_name
HV008 fdw_invalid_column_number
HV004 fdw_invalid_data_type
HV006 fdw_invalid_data_type_descriptors
HV091 fdw_invalid_descriptor_field_identifier
HV00B fdw_invalid_handle
HV00C fdw_invalid_option_index
HV00D fdw_invalid_option_name
HV090 fdw_invalid_string_length_or_buffer_length
HV00A fdw_invalid_string_format
HV009 fdw_invalid_use_of_null_pointer
HV014 fdw_too_many_handles
HV001 fdw_out_of_memory
HV00P fdw_no_schemas
HV00J fdw_option_name_not_found
HV00K fdw_reply_handle
HV00Q fdw_schema_not_found
HV00R fdw_table_not_found
HV00L fdw_unable_to_create_execution
HV00M fdw_unable_to_create_reply
HV00N fdw_unable_to_establish_connection
class P0 ProgrammingError PL/pgSQL Error
P0000 plpgsql_error
P0001 raise_exception
P0002 no_data_found
P0003 too_many_rows
P0004 assert_failure
class XX InternalError Internal Error
XX000 internal_error
XX001 data_corrupted
XX002 index_corrupted
```

### 3.3 The tool and the generated module

`tools/gen_sqlstates.py` reads the table and writes `stuntgres/_sqlstates.py`.

```sh
python3 bindings/cpython/tools/gen_sqlstates.py            # rewrite the module
python3 bindings/cpython/tools/gen_sqlstates.py --check    # exit 0 if it is up to date
```

| Function | Algorithm |
|---|---|
| `read_table(path: str = TABLE) -> list[SqlClass]` | For every line that is not empty and does not start with `#`: split on white space. A line whose first word is `class` needs at least four words, a two-character prefix and a base that is one of the nine `...Error` names of `errors.py`, else `ValueError(f"{file}:{line}: bad class line: {text}")`; it starts a new `SqlClass(prefix, base, title, [])`. Any other line needs exactly two words, a five-character code and a class before it, else `ValueError("...: bad code line: ...")`; the code must start with the prefix of the current class and must not have occurred, else `ValueError("...: misplaced or repeated code: ...")`; it is appended to the current class. |
| `class_name(condition: str, taken: set[str]) -> str` | `name = "".join(word.capitalize() for word in condition.split("_"))`; if `name` is one of the 13 names of `errors.py`: append `_`; if `name` is in `taken`: append `Ext`; if still in `taken`: `ValueError(f"no free class name for the condition {condition}")`; add to `taken`; return. |
| `render(classes: Sequence[SqlClass]) -> str` | The text of the module: the header, one line `    "<prefix>": e.<base>,` per class, the middle, then per class a comment `    # Class <prefix> - <title>` and one line `    ("<code>", "<class name>"),` per code (names from `class_name` with one `taken` set for the whole table), then the footer. |
| `main(argv: Sequence[str] \| None = None) -> int` | Arguments other than none or `--check`: print `usage: gen_sqlstates.py [--check]` on `stderr`, return 2. `text = render(read_table())`. With `--check`: return 0 if the module on disk equals `text`, else print `gen_sqlstates: <path> is not what the tool writes` and return 1. Otherwise write the module and return 0. |

The complete tool:

```python
#!/usr/bin/env python3
"""Write stuntgres/_sqlstates.py from the table tools/sqlstates.txt.

Usage: python3 bindings/cpython/tools/gen_sqlstates.py [--check]

Without an option the module is rewritten. With --check nothing is written: the
exit code is 0 when the module on disk equals what would be written, else 1.
"""
from __future__ import annotations

import os
import sys
from typing import NamedTuple, Sequence

TOOLS = os.path.dirname(os.path.abspath(__file__))
TABLE = os.path.join(TOOLS, "sqlstates.txt")
TARGET = os.path.join(os.path.dirname(TOOLS), "stuntgres", "_sqlstates.py")

# The names stuntgres/errors.py defines: a generated class must not hide one.
RESERVED = frozenset(
    (
        "Warning", "Error", "InterfaceError", "DatabaseError", "DataError",
        "OperationalError", "IntegrityError", "InternalError", "ProgrammingError",
        "NotSupportedError", "ConnectionTimeout", "CancellationTimeout",
        "WaitTimeout",
    )
)
BASES = frozenset(name for name in RESERVED if name.endswith("Error"))

HEADER = '''"""Exception classes named after the SQLSTATE codes of PostgreSQL 18.

Generated by tools/gen_sqlstates.py from tools/sqlstates.txt. Do not edit:
change the table or the tool and run the tool again.
"""
from __future__ import annotations

from . import errors as e

# SQLSTATE class (the first two characters) -> PEP 249 class.
_BASES: dict[str, type[e.Error]] = {
'''

MIDDLE = '''}

# (SQLSTATE, name of its exception class), in the order of the manual.
_CODES: tuple[tuple[str, str], ...] = (
'''

FOOTER = ''')

_BY_CODE: dict[str, type[e.Error]] = {}


def lookup(sqlstate: str) -> type[e.Error]:
    """Return the exception class named after a SQLSTATE; KeyError if none."""
    return _BY_CODE[sqlstate.upper()]


def class_for(sqlstate: str | None) -> type[e.Error]:
    """Return the class to raise for a SQLSTATE, known or not."""
    if not sqlstate:
        return e.DatabaseError
    cls = _BY_CODE.get(sqlstate)
    if cls is None:
        cls = _BASES.get(sqlstate[:2], e.DatabaseError)
    return cls


def _install() -> None:
    for code, name in _CODES:
        attrs = {
            "__module__": "stuntgres.errors",
            "__doc__": f"The server reported SQLSTATE {code}.",
            "sqlstate": code,
        }
        cls = type(name, (_BASES[code[:2]],), attrs)
        _BY_CODE[code] = cls
        setattr(e, name, cls)
    e.lookup = lookup  # type: ignore[attr-defined]


_install()
'''


class SqlClass(NamedTuple):
    """One class of the table: its prefix, PEP 249 base, title and codes."""

    prefix: str
    base: str
    title: str
    codes: list[tuple[str, str]]


def read_table(path: str = TABLE) -> list[SqlClass]:
    """Parse the table file; raise ValueError naming the first bad line."""
    classes: list[SqlClass] = []
    seen: set[str] = set()
    with open(path, encoding="ascii") as fh:
        for number, raw in enumerate(fh, 1):
            line = raw.strip()
            if not line or line.startswith("#"):
                continue
            words = line.split()
            where = f"{os.path.basename(path)}:{number}"
            if words[0] == "class":
                if len(words) < 4 or len(words[1]) != 2 or words[2] not in BASES:
                    raise ValueError(f"{where}: bad class line: {line}")
                classes.append(SqlClass(words[1], words[2], " ".join(words[3:]), []))
                continue
            if len(words) != 2 or len(words[0]) != 5 or not classes:
                raise ValueError(f"{where}: bad code line: {line}")
            code, condition = words
            if code[:2] != classes[-1].prefix or code in seen:
                raise ValueError(f"{where}: misplaced or repeated code: {line}")
            seen.add(code)
            classes[-1].codes.append((code, condition))
    return classes


def class_name(condition: str, taken: set[str]) -> str:
    """Return the class name of a condition name and add it to `taken`.

    unique_violation becomes UniqueViolation. A name that stuntgres/errors.py
    defines gets a trailing underscore; a name already given to an earlier
    code gets the suffix Ext.
    """
    name = "".join(word.capitalize() for word in condition.split("_"))
    if name in RESERVED:
        name += "_"
    if name in taken:
        name += "Ext"
    if name in taken:
        raise ValueError(f"no free class name for the condition {condition}")
    taken.add(name)
    return name


def render(classes: Sequence[SqlClass]) -> str:
    """Return the text of stuntgres/_sqlstates.py for a parsed table."""
    out = [HEADER]
    for cls in classes:
        out.append(f'    "{cls.prefix}": e.{cls.base},\n')
    out.append(MIDDLE)
    taken: set[str] = set()
    for cls in classes:
        out.append(f"    # Class {cls.prefix} - {cls.title}\n")
        for code, condition in cls.codes:
            out.append(f'    ("{code}", "{class_name(condition, taken)}"),\n')
    out.append(FOOTER)
    return "".join(out)


def main(argv: Sequence[str] | None = None) -> int:
    """Run the tool; return its exit code."""
    args = list(sys.argv[1:] if argv is None else argv)
    if args not in ([], ["--check"]):
        sys.stderr.write("usage: gen_sqlstates.py [--check]\n")
        return 2
    text = render(read_table())
    if args:
        with open(TARGET, encoding="ascii") as fh:
            if fh.read() == text:
                return 0
        sys.stderr.write(f"gen_sqlstates: {TARGET} is not what the tool writes\n")
        return 1
    with open(TARGET, "w", encoding="ascii") as fh:
        fh.write(text)
    return 0


if __name__ == "__main__":
    sys.exit(main())
```

The generated module has 384 lines (14555 bytes, SHA-256
`a39ae88c0f10f02af84ba294911bd6e71329e0df49caa080877210aabc8b4a9d`): 41 lines of `_BASES`, 253 + 41 lines of `_CODES`,
and the code of the footer. It is not printed here: the implementer runs the tool, and
the unit test `GeneratorToolTest.test_module_is_what_the_tool_writes` proves that the
file on disk is the tool's output. It stays below the 500-line limit of rule C6, so it
needs no exemption. Its public names:

```python
def lookup(sqlstate: str) -> type[e.Error]: ...
def class_for(sqlstate: str | None) -> type[e.Error]: ...
```

| Name | Algorithm |
|---|---|
| `lookup(sqlstate)` | `_BY_CODE[sqlstate.upper()]`; `KeyError` for a code without a class (also for `00000` and the `01` codes). Installed as `stuntgres.errors.lookup`. |
| `class_for(sqlstate)` | `None` or empty: `DatabaseError`. Else `_BY_CODE.get(sqlstate)`; if that is `None`: `_BASES.get(sqlstate[:2], DatabaseError)`. |
| `_install()` (runs once, at import) | For every `(code, name)` of `_CODES`: `cls = type(name, (_BASES[code[:2]],), {"__module__": "stuntgres.errors", "__doc__": f"The server reported SQLSTATE {code}.", "sqlstate": code})`; `_BY_CODE[code] = cls`; `setattr(errors, name, cls)`. Then `errors.lookup = lookup`. |

Importing the module costs about 2 ms **(measured: 1.7 to 2.6 ms in fresh
interpreters)**.
Static type checkers do not see the 253 names in `stuntgres.errors` (they are set at run
time, because `errors.py` is not edited); `stuntgres.errors.lookup("23505")` and the PEP
249 base classes are the typed way to the same classes.

### 3.4 `ErrorInfo` and `Diagnostic` (`stuntgres/_diagnostic.py`)

```python
class ErrorInfo:
    def __init__(
        self, fields: dict[int, bytes], message: bytes, encoding: str = "utf-8"
    ) -> None: ...
    @classmethod
    def from_result(cls, result: PGresult, encoding: str = "utf-8") -> ErrorInfo: ...
    def error_field(self, fieldcode: int) -> bytes | None: ...
    def text(self) -> str: ...

class Diagnostic:
    def __init__(self, info: ErrorInfo | None = None) -> None: ...
    # 18 read-only properties, each str | None

def error_from_result(result: PGresult, encoding: str = "utf-8") -> e.Error: ...
```

**`ErrorInfo`** is a copy of what an error or notice result said. It is what an
exception keeps in `Error.pgresult` (the attribute of PQ 6): a plain Python object, so
the native result can be released at once and the exception can be pickled.

| Member | Algorithm |
|---|---|
| `__init__(fields, message, encoding)` | Store the three values as `fields`, `message`, `encoding`. |
| `from_result(result, encoding)` | For every member `code` of `pq.DiagnosticField` (18): `value = result.error_field(code)`; keep it under `int(code)` when not `None`. `message = result.error_message`. Reads only the result, never the connection. |
| `error_field(fieldcode)` | `self.fields.get(int(fieldcode))`. |
| `text()` | `text = message.decode(encoding, "replace").rstrip("\n")`; if the field `SEVERITY` exists and `text` starts with `<severity>:  ` (the severity, a colon, two spaces): drop that prefix; return `text`, or `"no error details available"` when it is empty. This is the message as libpq formats it (primary message, `LINE n:` with the marker, `DETAIL:`, `HINT:`, `CONTEXT:`) without `ERROR:  `. |

**`Diagnostic`** exposes every `SPG_DIAG` field as text. Each property returns the field
decoded with `info.encoding` (errors `"replace"`), or `None` when the message has no such
field or there is no `info`.

| Property | Field (`pq.DiagnosticField`) | Property | Field |
|---|---|---|---|
| `severity` | `SEVERITY` | `context` | `CONTEXT` |
| `severity_nonlocalized` | `SEVERITY_NONLOCALIZED` | `schema_name` | `SCHEMA_NAME` |
| `sqlstate` | `SQLSTATE` | `table_name` | `TABLE_NAME` |
| `message_primary` | `MESSAGE_PRIMARY` | `column_name` | `COLUMN_NAME` |
| `message_detail` | `MESSAGE_DETAIL` | `datatype_name` | `DATATYPE_NAME` |
| `message_hint` | `MESSAGE_HINT` | `constraint_name` | `CONSTRAINT_NAME` |
| `statement_position` | `STATEMENT_POSITION` | `source_file` | `SOURCE_FILE` |
| `internal_position` | `INTERNAL_POSITION` | `source_line` | `SOURCE_LINE` |
| `internal_query` | `INTERNAL_QUERY` | `source_function` | `SOURCE_FUNCTION` |

The names are psycopg 3's. Positions and the source line are text, as the server sends
them. `Diagnostic.__module__` is `"stuntgres.errors"` and the class is installed as
`stuntgres.errors.Diagnostic`. The three `source_*` fields are `None` on the stuntgres
server (`.cswd/tmp/CPYTHON/known-gaps.md`); no test asserts their value.

At the end of the module, once: `errors._diag_factory = _diagnostic_of` and
`errors.Diagnostic = Diagnostic`. `_diagnostic_of(error)` returns
`Diagnostic(error.pgresult)` when `error.pgresult` is an `ErrorInfo`, else `Diagnostic()`
(every property `None`). So `Error.diag` (PQ 6) is never `None` once this module is
imported.

### 3.5 From a result or a broken connection to an exception

`error_from_result(result, encoding)`:

1. `info = ErrorInfo.from_result(result, encoding)`.
2. `raw = info.error_field(DiagnosticField.SQLSTATE)`. If `raw is None` the result was
   made by the client library, not by the server: return
   `OperationalError(info.text(), pgresult=info)`.
3. `sqlstate = raw.decode("ascii", "replace")`; `cls = _sqlstates.class_for(sqlstate)`;
   `error = cls(info.text(), pgresult=info)`.
4. If `cls.sqlstate is None` (a PEP 249 class chosen by prefix): `error.sqlstate =
   sqlstate` (an instance attribute). Return `error`.

The caller raises it and clears the result. `encoding` is `pgconn._encoding` at that
moment. What the callers of this design do:

| Situation | Where | Outcome |
|---|---|---|
| a result with status `FATAL_ERROR`, `NONFATAL_ERROR` or `BAD_RESPONSE` among the results of a statement | `_cur_results.check_results` (6.3), `ExecMixin._exec_command` (4.6) | the exception of `error_from_result`; every result of the statement is cleared |
| a call of `stuntgres.pq` fails (PQ 5: `send_query failed: ...`, `consume_input failed: ...`) | raised inside the generator | `OperationalError` with libpq's text after the method name; not converted |
| the connection is lost while a statement runs | libpq returns an error result without SQLSTATE, or `consume_input` fails | `OperationalError` as in the two rows above; afterwards `pgconn.status` is `BAD`, which `ConnCore._note_status` records: the connection is `closed` and `broken` (4.3) |
| the server ends the session (`pg_terminate_backend`) | with libpq over a socket `consume_input` fails **(measured, both modes)**; with libspq a `FATAL` result with SQLSTATE `57P01` arrives (`docs/LIBSPQ.md` 5.7) | an `OperationalError` either way: `consume_input failed: FATAL:  terminating connection due to administrator command` and the lines of the row above, or `errors.AdminShutdown`; the connection is then broken. Tests assert the class `OperationalError`, the phrase `terminating connection due to administrator command` and `broken` |
| an operation on a connection after `close()` | `ConnCore._check_closed` | `OperationalError("the connection is closed")` |
| an operation on a lost connection | `ConnCore._check_open` | `OperationalError("the connection is lost")` |
| the conninfo is malformed or names an unknown keyword | `conninfo._parse` (4.1) | `ProgrammingError` with libpq's text |
| the connection attempt fails | `generators.connect` (PQ 7.2) | `OperationalError("connection failed: <libpq's text>")` |
| the connection attempt takes longer than `connect_timeout` | `Connection.connect` (9.2) | `ConnectionTimeout("connection timeout expired")` |
| a client encoding without Python codec | `PGconn._encoding` (PQ 5.4) in `_check_open` | `NotSupportedError("client encoding EUC_TW has no Python codec")` |
| a parameter that cannot be converted, a placeholder error | ADAPT 2.5, 3.2 | `ProgrammingError`, `DataError`, `TypeError` before anything is sent |
| a text that the client encoding cannot represent | the codecs | `UnicodeEncodeError` / `UnicodeDecodeError` (4.7) |

Texts of client-side failures keep libpq's wording (`docs/design/LIBSPQ-CLIENT.md` 8.5),
because frameworks match on it.

Observed on PostgreSQL 18.6 **(measured, `exp7/probe_diag.oracle.txt`; the stuntgres
server answered the same except for the three `source_*` fields)**:

| Statement | Class (PEP 249 base) | `str(exc)` |
|---|---|---|
| a second `insert into t values (1, ...)` on a primary key | `UniqueViolation` (`IntegrityError`) | `duplicate key value violates unique constraint "t_pkey"` + newline + `DETAIL:  Key (id)=(1) already exists.` |
| `insert into t values (2, null, 1)` (`v` is `not null`) | `NotNullViolation` (`IntegrityError`) | `null value in column "v" of relation "t" violates not-null constraint` + newline + `DETAIL:  Failing row contains (2, null, 1).` |
| `insert into t values (2, 'abcd', 1)` (`v varchar(3)`) | `StringDataRightTruncation` (`DataError`) | `value too long for type character varying(3)` |
| `insert into t values (2, 'b', 0)` (`check (n > 0)`) | `CheckViolation` (`IntegrityError`) | `new row for relation "t" violates check constraint "t_n_check"` + newline + `DETAIL:  Failing row contains (2, b, 0).` |
| `select * from nosuch` | `UndefinedTable` (`ProgrammingError`) | `relation "nosuch" does not exist` + newline + `LINE 1: select * from nosuch` + newline + 22 spaces and `^`; `diag.statement_position == "15"` |
| `selec 1` | `SyntaxError` (`ProgrammingError`) | `syntax error at or near "selec"` + newline + `LINE 1: selec 1` + newline + 8 spaces and `^` |
| `select 1/0` | `DivisionByZero` (`DataError`) | `division by zero` |
| `select 'x'::int` | `InvalidTextRepresentation` (`DataError`) | `invalid input syntax for type integer: "x"` + the `LINE 1:` lines |
| `do $$ begin raise exception 'plain'; end $$` | `RaiseException` (`ProgrammingError`) | `plain` + newline + `CONTEXT:  PL/pgSQL function inline_code_block line 1 at RAISE` |
| `raise exception ... using errcode = 'ZZ123'` | `DatabaseError`, `sqlstate == "ZZ123"` | the message, `DETAIL:`, `HINT:`, `CONTEXT:` lines |
| `raise exception using errcode = '23999', message = 'made up'` | `IntegrityError`, `sqlstate == "23999"` | `made up` + the `CONTEXT:` line |
| any statement after the peer closed the socket | `OperationalError`, `sqlstate is None` | `server closed the connection unexpectedly` + newline + tab + `This probably means the server terminated abnormally` + newline + tab + `before or while processing the request.` |
| a statement in an aborted transaction | `InFailedSqlTransaction` (`InternalError`) | `current transaction is aborted, commands ignored until end of transaction block` |
| a statement cancelled by `cancel()` | `QueryCanceled` (`OperationalError`) | starts with `canceling statement due to user request` |

The complete file:

```python
"""Diagnostics of a server message and the conversion of error results."""
from __future__ import annotations

from typing import TYPE_CHECKING

from . import _sqlstates
from . import errors as e
from .pq import DiagnosticField

if TYPE_CHECKING:
    from .pq import PGresult


class ErrorInfo:
    """The fields and the text of an error or notice, copied out of a result.

    An exception keeps this copy instead of the native result, so the result is
    released at once and the exception can be pickled.
    """

    def __init__(
        self, fields: dict[int, bytes], message: bytes, encoding: str = "utf-8"
    ) -> None:
        self.fields = fields
        self.message = message
        self.encoding = encoding

    @classmethod
    def from_result(cls, result: PGresult, encoding: str = "utf-8") -> ErrorInfo:
        """Copy every diagnostic field and the message of a result."""
        fields: dict[int, bytes] = {}
        for code in DiagnosticField:
            value = result.error_field(code)
            if value is not None:
                fields[int(code)] = value
        return cls(fields, result.error_message, encoding)

    def error_field(self, fieldcode: int) -> bytes | None:
        """Return one diagnostic field, None when the message has none."""
        return self.fields.get(int(fieldcode))

    def text(self) -> str:
        """Return the message as libpq formats it, without the severity."""
        text = self.message.decode(self.encoding, "replace").rstrip("\n")
        severity = self.fields.get(DiagnosticField.SEVERITY)
        if severity is not None:
            prefix = severity.decode(self.encoding, "replace") + ":  "
            if text.startswith(prefix):
                text = text[len(prefix):]
        return text or "no error details available"


def _field(code: int, doc: str) -> property:
    def get(self: Diagnostic) -> str | None:
        return self._text(code)

    return property(get, doc=doc)


class Diagnostic:
    """The fields of an error or notice the server reported, as text or None."""

    __module__ = "stuntgres.errors"

    def __init__(self, info: ErrorInfo | None = None) -> None:
        self._info = info

    def _text(self, code: int) -> str | None:
        if self._info is None:
            return None
        raw = self._info.error_field(code)
        if raw is None:
            return None
        return raw.decode(self._info.encoding, "replace")

    severity = _field(DiagnosticField.SEVERITY, "Severity, possibly localized.")
    severity_nonlocalized = _field(
        DiagnosticField.SEVERITY_NONLOCALIZED, "Severity, never localized."
    )
    sqlstate = _field(DiagnosticField.SQLSTATE, "The SQLSTATE code.")
    message_primary = _field(DiagnosticField.MESSAGE_PRIMARY, "The primary message.")
    message_detail = _field(DiagnosticField.MESSAGE_DETAIL, "The detail message.")
    message_hint = _field(DiagnosticField.MESSAGE_HINT, "The hint.")
    statement_position = _field(
        DiagnosticField.STATEMENT_POSITION, "Error position in the statement."
    )
    internal_position = _field(
        DiagnosticField.INTERNAL_POSITION, "Error position in the internal query."
    )
    internal_query = _field(DiagnosticField.INTERNAL_QUERY, "The internal query.")
    context = _field(DiagnosticField.CONTEXT, "The call stack of the message.")
    schema_name = _field(DiagnosticField.SCHEMA_NAME, "Schema of the object.")
    table_name = _field(DiagnosticField.TABLE_NAME, "Table of the object.")
    column_name = _field(DiagnosticField.COLUMN_NAME, "Column of the object.")
    datatype_name = _field(DiagnosticField.DATATYPE_NAME, "Data type of the object.")
    constraint_name = _field(DiagnosticField.CONSTRAINT_NAME, "Constraint name.")
    source_file = _field(DiagnosticField.SOURCE_FILE, "Source file of the server.")
    source_line = _field(DiagnosticField.SOURCE_LINE, "Source line of the server.")
    source_function = _field(
        DiagnosticField.SOURCE_FUNCTION, "Source function of the server."
    )


def error_from_result(result: PGresult, encoding: str = "utf-8") -> e.Error:
    """Build the exception for a result with an error status.

    The class is chosen by SQLSTATE. A result without SQLSTATE was made by the
    client library (for example for a lost connection): OperationalError.
    """
    info = ErrorInfo.from_result(result, encoding)
    raw = info.error_field(DiagnosticField.SQLSTATE)
    if raw is None:
        return e.OperationalError(info.text(), pgresult=info)  # type: ignore[arg-type]
    sqlstate = raw.decode("ascii", "replace")
    cls = _sqlstates.class_for(sqlstate)
    error = cls(info.text(), pgresult=info)  # type: ignore[arg-type]
    if cls.sqlstate is None:
        error.sqlstate = sqlstate
    return error


def _diagnostic_of(error: e.Error) -> Diagnostic:
    info = error.pgresult
    return Diagnostic(info if isinstance(info, ErrorInfo) else None)


# stuntgres/errors.py is never edited: its extension points are filled here.
e._diag_factory = _diagnostic_of
e.Diagnostic = Diagnostic  # type: ignore[attr-defined]
```

## 4. Connection base class

`BaseConnection` is `ConnCore` plus five mixins (4.4). Section 4 covers what a connection
is made from and the command path; transactions are section 5, notices, notifications
and cancellation section 8, the public classes section 9.

### 4.1 Connection strings (`stuntgres/conninfo.py`)

```python
def conninfo_to_dict(conninfo: str = "", **kwargs: Any) -> dict[str, Any]: ...
def make_conninfo(conninfo: str = "", **kwargs: Any) -> str: ...
def connect_timeout(options: Mapping[str, Any]) -> float | None: ...
```

The names `make_conninfo` and `conninfo_to_dict` and their behavior are psycopg 3's
(`psycopg.conninfo`). What the keywords mean is not decided here: with libspq, `host`,
`hostaddr` and `port` name a cluster, `dbname` a logical database, `user` a role, all
created on demand (`docs/LIBSPQ.md` 5.1); with a real libpq they are libpq's.

| Function | Algorithm |
|---|---|
| `_quote(value: str) -> str` | If `value` is not empty and contains none of space, tab, newline, carriage return, vertical tab, form feed, `'` and `\`: return it. Else return `'` + `value` with every `\` doubled and every `'` written as `\'` + `'`. |
| `_parse(conninfo: str) -> dict[str, Any]` | `options = pq.Conninfo.parse(conninfo.encode("utf-8"))` (PQ 5.2; this loads the native library); an `OperationalError` from it is re-raised as `ProgrammingError(str(exc))` without context. Return `{keyword: value}` (both decoded as UTF-8) for the options whose `val` is not `None`, in the order libpq lists them. `stuntgres.pq` is imported inside the function. |
| `conninfo_to_dict(conninfo, **kwargs)` | `options = _parse(conninfo)` if `conninfo` is not empty, else `{}`. For every keyword argument in order: `None` removes the keyword; any other value replaces or adds it (the value is kept as given, not converted). Return `options`. The string may be `keyword=value` pairs or a `postgresql://` URI. |
| `make_conninfo(conninfo, **kwargs)` | Without keyword arguments: `_parse(conninfo)` when it is not empty (to validate it) and return `conninfo` unchanged. Otherwise `options = conninfo_to_dict(conninfo, **kwargs)`; `text = " ".join(f"{keyword}={_quote(str(value))}" ...)`; `_parse(text)` when `text` is not empty (this is what rejects an unknown keyword argument); return `text`. |
| `connect_timeout(options)` | `value = options.get("connect_timeout")`; `None` -> `None`; `seconds = int(value)`, where `TypeError` and `ValueError` become `ProgrammingError(f"bad value for connect_timeout: {value!r}")`; `seconds <= 0` -> `None`; else `float(max(seconds, 2))`. This is how libpq reads the option. |

**(measured, the same in both modes)**

| Call | Result |
|---|---|
| `make_conninfo()` | `""` (no library call) |
| `make_conninfo("host=h dbname=d")` | `"host=h dbname=d"` |
| `make_conninfo("host=h", dbname="d", port=5433)` | `"host=h dbname=d port=5433"` |
| `make_conninfo("host=h dbname=d", dbname="x", host=None)` | `"dbname=x"` |
| `make_conninfo("", password="a b", application_name="it's", options="-c x=1\\2", user="")` (Python source) | `password='a b' application_name='it\'s' options='-c x=1\\2' user=''` |
| `make_conninfo("postgresql://u:p@h:5433/d?application_name=app", connect_timeout=3)` | `"user=u password=p dbname=d host=h port=5433 application_name=app connect_timeout=3"` |
| `make_conninfo("host=h", nosuch="x")`, `make_conninfo("nosuch=x")` | `ProgrammingError`: `invalid connection option "nosuch"` |
| `make_conninfo("host")` | `ProgrammingError`: `missing "=" after "host" in connection info string` |
| `make_conninfo("host='x")` | `ProgrammingError`: `unterminated quoted string in connection info string` |
| `make_conninfo("postgresql://h/d?nosuch=1")` | `ProgrammingError`: `invalid URI query parameter: "nosuch"` |
| `conninfo_to_dict("host=h port=5433 dbname='a b'", user="u", port=None)` | `{"dbname": "a b", "host": "h", "user": "u"}` |
| `connect_timeout` of `"0"`, `0`, `"-1"`, absent | `None` |
| `connect_timeout` of `"1"`, `1`, `"2"`, `2.9` | `2.0` |
| `connect_timeout` of `"10"`, `30` | `10.0`, `30.0` |
| `connect_timeout` of `"x"`, `""`, `"1.5"` | `ProgrammingError`: `bad value for connect_timeout: 'x'` (the value with `repr`) |

Environment variables are not read by this module. A real libpq still applies its own
defaults (`PGHOST`, the service file) inside the connection attempt; libspq never does
(`docs/LIBSPQ.md` 5.1), and the test runner removes every `PG*` variable (PQ 9.1).

The complete file:

```python
"""Building and reading connection strings."""
from __future__ import annotations

from typing import Any, Mapping

from . import errors as e

_NEEDS_QUOTES = frozenset(" \t\n\r\v\f'\\")


def _quote(value: str) -> str:
    """Return a value as it is written after keyword= in a connection string."""
    if value and not _NEEDS_QUOTES.intersection(value):
        return value
    return "'" + value.replace("\\", "\\\\").replace("'", "\\'") + "'"


def _parse(conninfo: str) -> dict[str, Any]:
    """Return the options a connection string sets; ProgrammingError if bad."""
    from .pq import Conninfo

    try:
        options = Conninfo.parse(conninfo.encode("utf-8"))
    except e.OperationalError as exc:
        raise e.ProgrammingError(str(exc)) from None
    return {
        option.keyword.decode("utf-8"): option.val.decode("utf-8")
        for option in options
        if option.val is not None
    }


def conninfo_to_dict(conninfo: str = "", **kwargs: Any) -> dict[str, Any]:
    """Return the options of a connection string merged with keyword arguments.

    The string may be keyword=value pairs or a postgresql:// URI. A keyword
    argument replaces the option of the string; one that is None removes it.
    """
    options = _parse(conninfo) if conninfo else {}
    for keyword, value in kwargs.items():
        if value is None:
            options.pop(keyword, None)
        else:
            options[keyword] = value
    return options


def make_conninfo(conninfo: str = "", **kwargs: Any) -> str:
    """Merge a connection string and keyword arguments into one valid string.

    Without keyword arguments the string is checked and returned unchanged.
    Raises ProgrammingError for an unknown keyword or a malformed string.
    """
    if not kwargs:
        if conninfo:
            _parse(conninfo)
        return conninfo
    options = conninfo_to_dict(conninfo, **kwargs)
    text = " ".join(
        f"{keyword}={_quote(str(value))}" for keyword, value in options.items()
    )
    if text:
        _parse(text)
    return text


def connect_timeout(options: Mapping[str, Any]) -> float | None:
    """Return the seconds a connection attempt may take, None for no limit.

    The value of connect_timeout as libpq reads it: absent, zero or negative
    means no limit, and a limit below two seconds is raised to two.
    """
    value = options.get("connect_timeout")
    if value is None:
        return None
    try:
        seconds = int(value)
    except (TypeError, ValueError):
        raise e.ProgrammingError(
            f"bad value for connect_timeout: {value!r}"
        ) from None
    if seconds <= 0:
        return None
    return float(max(seconds, 2))
```

### 4.2 `ConnectionInfo` (`stuntgres/_connection_info.py`)

`Connection.info` returns a new `ConnectionInfo(pgconn)` on every access. The names are
psycopg 3's; `vendor`, `hostaddr`, `pipeline_status` and `timezone` are not provided.

| Member | Value |
|---|---|
| `encoding: str` | `pgconn._encoding` (PQ 5.4): the Python codec of the client encoding; `"utf-8"` on a closed connection. |
| `host: str`, `dbname: str`, `user: str`, `password: str`, `options: str` | `pgconn.host`, `.db`, `.user`, `.password`, `.options` decoded with `encoding` (errors `"replace"`). |
| `port: int` | `int(` the decoded `pgconn.port` `)`. |
| `get_parameters() -> dict[str, str]` | `{keyword: val}` (decoded) for the options of `pgconn.info` whose `val` is not `None`, whose keyword is not `password` and whose `val` differs from `compiled`. |
| `dsn: str` | `" ".join(f"{keyword}={_quote(value)}")` over `get_parameters()`: a connection string without the password. |
| `status: pq.ConnStatus` | `ConnStatus(pgconn.status)`. |
| `transaction_status: pq.TransactionStatus` | `TransactionStatus(pgconn.transaction_status)`. |
| `parameter_status(name: str) -> str \| None` | `pgconn.parameter_status(name.encode(encoding))`, decoded; `None` when the server reports no such parameter. |
| `server_version: int` | `pgconn.server_version` (PostgreSQL 18.6: `180006`). |
| `backend_pid: int` | `pgconn.backend_pid`. |
| `error_message: str` | `pgconn.error_message` decoded, without trailing newlines. |

On a finished connection `status` is `BAD`, `transaction_status` `UNKNOWN`,
`error_message` `""`, `encoding` `"utf-8"`; the members that need the connection raise
`OperationalError("the connection is closed")` (PQ 5.3 `_ensure`). `ConnectionInfo`
reads the native connection without the connection's lock: use it from the thread that
uses the connection, or while no other thread runs a statement on it (D11).

**(measured)** For a connection made with `options='-c geqo=off'
application_name='my app'`: `options == "-c geqo=off"`; `parameter_status` gives
`server_encoding` `UTF8`, `client_encoding` `UTF8`, `application_name` `my app`,
`standard_conforming_strings` `on`, `integer_datetimes` `on`, `DateStyle` `ISO, MDY`,
`IntervalStyle` `postgres`, an unknown name `None`; `dsn` contains
`application_name='my app'` and `options='-c geqo=off'` and no `password`;
`conninfo_to_dict(info.dsn) == info.get_parameters()`. Tests compare `host`, `port`,
`dbname` and `user` with `support.conninfo_dict(self.conninfo)` (PQ 9.6), never with
literals of one mode.

The complete file:

```python
"""Facts about a connection: where it leads and what the session reports."""
from __future__ import annotations

from typing import TYPE_CHECKING

from .conninfo import _quote
from .pq import ConnStatus, TransactionStatus

if TYPE_CHECKING:
    from .pq import PGconn


class ConnectionInfo:
    """Read-only facts of a connection (Connection.info)."""

    __module__ = "stuntgres"

    def __init__(self, pgconn: PGconn) -> None:
        self.pgconn = pgconn

    @property
    def encoding(self) -> str:
        """The Python codec of the session's client encoding."""
        return self.pgconn._encoding

    def _decode(self, value: bytes) -> str:
        return value.decode(self.encoding, "replace")

    @property
    def host(self) -> str:
        """The host (with stuntgres: the cluster name) of the connection."""
        return self._decode(self.pgconn.host)

    @property
    def port(self) -> int:
        """The port of the connection."""
        return int(self._decode(self.pgconn.port))

    @property
    def dbname(self) -> str:
        """The database of the connection."""
        return self._decode(self.pgconn.db)

    @property
    def user(self) -> str:
        """The role of the connection."""
        return self._decode(self.pgconn.user)

    @property
    def password(self) -> str:
        """The password given for the connection."""
        return self._decode(self.pgconn.password)

    @property
    def options(self) -> str:
        """The command-line options given in the connection string."""
        return self._decode(self.pgconn.options)

    def get_parameters(self) -> dict[str, str]:
        """Return the connection options that differ from the built-in default.

        The password is never included.
        """
        return {
            self._decode(option.keyword): self._decode(option.val)
            for option in self.pgconn.info
            if option.val is not None
            and option.keyword != b"password"
            and option.val != option.compiled
        }

    @property
    def dsn(self) -> str:
        """A connection string for get_parameters(): no password in it."""
        return " ".join(
            f"{keyword}={_quote(value)}"
            for keyword, value in self.get_parameters().items()
        )

    @property
    def status(self) -> ConnStatus:
        """The status of the connection."""
        return ConnStatus(self.pgconn.status)

    @property
    def transaction_status(self) -> TransactionStatus:
        """The transaction status of the session."""
        return TransactionStatus(self.pgconn.transaction_status)

    def parameter_status(self, name: str) -> str | None:
        """Return a parameter the server reports, None when it reports none."""
        value = self.pgconn.parameter_status(name.encode(self.encoding))
        return None if value is None else self._decode(value)

    @property
    def server_version(self) -> int:
        """The server version as a number, for example 180006."""
        return self.pgconn.server_version

    @property
    def backend_pid(self) -> int:
        """The process ID of the session on the server."""
        return self.pgconn.backend_pid

    @property
    def error_message(self) -> str:
        """The last error message of the connection, without trailing newline."""
        return self._decode(self.pgconn.error_message).rstrip("\n")
```

### 4.3 `ConnCore` (`stuntgres/_conn_core.py`)

The module also defines the public names `IsolationLevel` and `Notify`, the helper
`NotifyWindow` (8.2), the constants `CANCEL_TIMEOUT = 5.0` (8.4) and `NOTIFY_INTERVAL =
0.1` (8.2) and `logger = logging.getLogger("stuntgres")`.

```python
class IsolationLevel(enum.IntEnum):
    READ_UNCOMMITTED = 1
    READ_COMMITTED = 2
    REPEATABLE_READ = 3
    SERIALIZABLE = 4

class Notify(NamedTuple):
    channel: str
    payload: str
    pid: int

class ConnCore:
    def __init__(self, pgconn: PGconn) -> None: ...
    @classmethod
    def _connect_gen(cls, conninfo: str, *, autocommit: bool = False) -> PQGenConn[Any]: ...
    @property
    def closed(self) -> bool: ...
    @property
    def broken(self) -> bool: ...
    @property
    def info(self) -> ConnectionInfo: ...
    @property
    def adapters(self) -> AdaptersMap: ...
    @property
    def connection(self) -> Any: ...
    def fileno(self) -> int: ...
```

**State.** Everything a connection remembers is set in `ConnCore.__init__`; the mixins
add methods only.

| Attribute | Initial value | Meaning |
|---|---|---|
| `pgconn` | the argument | the `pq.PGconn` (public, psycopg's name; ADAPT 3.1 reads it) |
| `row_factory` | `rows.tuple_row` | default row factory of new cursors (public, assignable) |
| `cursor_factory` | `None` | class of new cursors; `None` means `Cursor` (`AsyncCursor` for the asyncio class) (public, assignable) |
| `_autocommit` | `False` | 4.6 |
| `_isolation_level`, `_read_only`, `_deferrable` | `None` | 5.3 |
| `_closed` | `False` | `close()` was called |
| `_broken` | `False` | the connection was found lost (`_note_status`) |
| `_pid` | `os.getpid()` | the finalizer is silent in another process |
| `_codec` | `"utf-8"` | the client encoding's codec as of the last `_check_open`; read by the notice callback, which must not call the library (8.1) |
| `_adapters` | `AdaptersMap(context_adapters(None))` | ADAPT 3.2: a snapshot of the global map |
| `_handlers_lock` | `threading.Lock()` | guards the two handler tuples (also in the asyncio class; it is never held across a wait) |
| `_notice_handlers`, `_notify_handlers` | `()` | 8.1, 8.2 |
| `_notify_backlog` | `collections.deque()` | notifications nobody handled yet (8.2) |
| `_num_transactions` | `0` | depth of `transaction()` blocks (5.4) |
| `_tpc` | `None` | reserved for two-phase commit (1.5) |
| `_cancel_handle` | `pgconn.get_cancel()` | the `PGcancel` that `cancel()` and `close()` use from any thread (8.3); released when the connection object is collected |

The last two statements of `__init__` route the library's callbacks to the connection
through a weak reference, so the `PGconn` does not keep its connection alive:
`pgconn.notice_handler = partial(_route_notice, weakref.ref(self))` and the same for
`notify_handler` with `_route_notify`. `_route_notice(wself, result)` calls
`wself()._on_notice(result)` when the connection still exists; `_route_notify` likewise
with `_on_notify(pgn)`. `ConnCore._on_notice` and `_on_notify` do nothing; the mixins of
8.1 and 8.2 override them.

`ConnCore` also has the ten PEP 249 exception classes as class attributes (`Warning`,
`Error`, `InterfaceError`, `DatabaseError`, `DataError`, `OperationalError`,
`IntegrityError`, `InternalError`, `ProgrammingError`, `NotSupportedError`): the optional
extension "exceptions as connection attributes".

| Member | Algorithm |
|---|---|
| `_connect_gen(conninfo, *, autocommit=False)` (classmethod, a `PQGenConn`) | 1. `pgconn = yield from generators.connect(conninfo.encode("utf-8"))` (PQ 7.2). 2. `conn = cls(pgconn)`. 3. `conn._autocommit = bool(autocommit)`. 4. Return `conn`. **No SQL is sent.** |
| `closed` | `self._closed or self._broken`. |
| `broken` | `self._broken and not self._closed`. |
| `info` | `ConnectionInfo(self.pgconn)`. |
| `adapters` | `self._adapters`. |
| `connection` | `self` (a connection is an `AdaptContext`, ADAPT 3.1). |
| `fileno()` | `self.pgconn.socket` (PQ 5.4): for callers that wait themselves; raises `OperationalError` on a closed connection. |
| `__repr__()` | `f"<{module}.{qualname} [{state}] at 0x{id(self):x}>"` with the class's `__module__` and `__qualname__`, `state` = `closed` if `_closed`, else `broken` if `_broken`, else `open`. Example: `<stuntgres.Connection [open] at 0x7f...>`. |
| `__del__()` | If `_closed` (or the attribute is missing: `__init__` failed), or `_broken`, or `_pid != os.getpid()`: return. Else `warnings.warn(f"{self!r} was deleted while still open: close it or use it in a 'with' statement", ResourceWarning, source=self)`. The `PGconn` finishes itself (PQ 5.3). |
| `_note_status()` | If not `_closed` and `pgconn.status != ConnStatus.OK`: `_broken = True`. Called only with the lock held: by `_check_open`, by `_wait_fileno` and at the end of every `wait` (9.1). |
| `_wait_fileno()` | `_note_status()`; return `-1` if `_closed or _broken`, else `pgconn.socket`. The `-1` is never polled: a generator of a dead connection raises in `_check_open` before it yields, or returns at once. |
| `_check_closed()` | If `_closed`: raise `OperationalError("the connection is closed")`. Calls nothing of the library (used by `cursor()`). |
| `_check_open()` | `_check_closed()`; `_note_status()`; if `_broken`: raise `OperationalError("the connection is lost")`; `self._codec = self.pgconn._encoding` (may raise `NotSupportedError` for an encoding without codec). First statement of every generator method. |
| `_check_intrans(attribute: str)` | `_check_open()`; `status = pgconn.transaction_status`; if it is not `IDLE`: raise `ProgrammingError(f"cannot change {attribute} inside a transaction (transaction status {name})")` with `name = TransactionStatus(status).name`. |
| `_begin_statement() -> bytes` | `b"BEGIN"`. Overridden in 5.3. |
| `_finish()` | `_closed = True`; `pgconn.finish()` (idempotent, never waits). |
| `_assign(name, value)` | `raise NotImplementedError`; the public classes define what assigning a session attribute does (9.2, 9.4). |

**(measured)** A lost connection: after the peer's end of the socket is shut down and one
statement has failed, `closed` and `broken` are `True`, `repr` shows `[broken]`,
`_check_open()` raises `the connection is lost`; `close()` then makes `broken` `False`
and `closed` stays `True`. A connection object dropped without `close()` produces exactly
one `ResourceWarning`; one that was closed produces none.

The complete file:

```python
"""State and life cycle shared by Connection and AsyncConnection."""
from __future__ import annotations

import enum
import logging
import os
import threading
import time
import warnings
import weakref
from collections import deque
from functools import partial
from typing import TYPE_CHECKING, Any, Callable, NamedTuple

from . import errors as e
from . import generators
from ._connection_info import ConnectionInfo
from ._gentypes import PQGenConn
from .adapt import AdaptersMap, context_adapters
from .pq import ConnStatus, TransactionStatus
from .rows import RowFactory, tuple_row

if TYPE_CHECKING:
    from .pq import PGconn, PGnotify, PGresult

logger = logging.getLogger("stuntgres")

# Seconds an interrupted statement gets to end after its cancel request.
CANCEL_TIMEOUT = 5.0
# Longest single wait of notifies(): it lets go of the connection this often.
NOTIFY_INTERVAL = 0.1


class IsolationLevel(enum.IntEnum):
    """The isolation levels a transaction can be started with."""

    READ_UNCOMMITTED = 1
    READ_COMMITTED = 2
    REPEATABLE_READ = 3
    SERIALIZABLE = 4


class Notify(NamedTuple):
    """A notification received from the server (LISTEN / NOTIFY)."""

    channel: str
    payload: str
    pid: int


class NotifyWindow:
    """Bookkeeping of one notifies() call: how many more, how much longer."""

    def __init__(self, timeout: float | None, stop_after: int | None) -> None:
        self.deadline = None if timeout is None else time.monotonic() + timeout
        self.stop_after = stop_after
        self.count = 0
        self.expired = False

    def full(self) -> bool:
        """True when stop_after notifications were handed out."""
        return self.stop_after is not None and self.count >= self.stop_after

    def interval(self) -> float:
        """Seconds the next wait may take; marks the window expired at the end."""
        if self.deadline is None:
            return NOTIFY_INTERVAL
        left = self.deadline - time.monotonic()
        if left <= 0:
            self.expired = True
            return 0.0
        return min(left, NOTIFY_INTERVAL)


def _route_notice(wself: Callable[[], Any], result: PGresult) -> None:
    self = wself()
    if self is not None:
        self._on_notice(result)


def _route_notify(wself: Callable[[], Any], pgn: PGnotify) -> None:
    self = wself()
    if self is not None:
        self._on_notify(pgn)


class ConnCore:
    """Everything a connection remembers; the mixins add behavior only."""

    # PEP 249 optional extension: the exceptions as connection attributes.
    Warning = e.Warning
    Error = e.Error
    InterfaceError = e.InterfaceError
    DatabaseError = e.DatabaseError
    DataError = e.DataError
    OperationalError = e.OperationalError
    IntegrityError = e.IntegrityError
    InternalError = e.InternalError
    ProgrammingError = e.ProgrammingError
    NotSupportedError = e.NotSupportedError

    def __init__(self, pgconn: PGconn) -> None:
        self.pgconn = pgconn
        self.row_factory: RowFactory = tuple_row
        self.cursor_factory: Any = None
        self._autocommit = False
        self._isolation_level: IsolationLevel | None = None
        self._read_only: bool | None = None
        self._deferrable: bool | None = None
        self._closed = False
        self._broken = False
        self._pid = os.getpid()
        self._codec = "utf-8"
        self._adapters = AdaptersMap(context_adapters(None))
        self._handlers_lock = threading.Lock()
        self._notice_handlers: tuple[Callable[[Any], None], ...] = ()
        self._notify_handlers: tuple[Callable[[Notify], None], ...] = ()
        self._notify_backlog: deque[Notify] = deque()
        self._num_transactions = 0
        self._tpc: Any = None
        self._cancel_handle = pgconn.get_cancel()
        wself = weakref.ref(self)
        pgconn.notice_handler = partial(_route_notice, wself)
        pgconn.notify_handler = partial(_route_notify, wself)

    def __del__(self) -> None:
        if getattr(self, "_closed", True) or self._broken:
            return
        if self._pid != os.getpid():
            return
        warnings.warn(
            f"{self!r} was deleted while still open: close it or use it in a"
            " 'with' statement",
            ResourceWarning,
            source=self,
        )

    def __repr__(self) -> str:
        cls = type(self)
        state = "closed" if self._closed else "broken" if self._broken else "open"
        return f"<{cls.__module__}.{cls.__qualname__} [{state}] at 0x{id(self):x}>"

    @classmethod
    def _connect_gen(
        cls, conninfo: str, *, autocommit: bool = False
    ) -> PQGenConn[Any]:
        """Make the connection; sends no SQL."""
        pgconn = yield from generators.connect(conninfo.encode("utf-8"))
        conn = cls(pgconn)
        conn._autocommit = bool(autocommit)
        return conn

    @property
    def closed(self) -> bool:
        """True when the connection cannot be used: closed or lost."""
        return self._closed or self._broken

    @property
    def broken(self) -> bool:
        """True when the connection was lost, not closed by close()."""
        return self._broken and not self._closed

    @property
    def info(self) -> ConnectionInfo:
        """Facts about the connection and the session."""
        return ConnectionInfo(self.pgconn)

    @property
    def adapters(self) -> AdaptersMap:
        """The adapters of this connection; cursors made later inherit them."""
        return self._adapters

    @property
    def connection(self) -> Any:
        """The connection itself (it is its own adaptation context)."""
        return self

    def fileno(self) -> int:
        """Return the descriptor to wait on; never read, write or close it."""
        return self.pgconn.socket

    def _wait_fileno(self) -> int:
        """The descriptor the waiters poll; -1 once the connection is gone.

        A generator of a closed or lost connection raises in its own check
        before it waits, so the -1 is never polled.
        """
        self._note_status()
        if self._closed or self._broken:
            return -1
        return self.pgconn.socket

    def _note_status(self) -> None:
        """Remember a lost connection; called with the connection's lock held.

        closed and broken answer from what is remembered here, so they never
        call the library and may be read from any thread.
        """
        if not self._closed and self.pgconn.status != ConnStatus.OK:
            self._broken = True

    def _check_closed(self) -> None:
        """Raise when close() was called; calls nothing of the library."""
        if self._closed:
            raise e.OperationalError("the connection is closed")

    def _check_open(self) -> None:
        """Raise unless the connection can take a command."""
        self._check_closed()
        self._note_status()
        if self._broken:
            raise e.OperationalError("the connection is lost")
        # Remembered for the notice callback, which must not call the library.
        self._codec = self.pgconn._encoding

    def _check_intrans(self, attribute: str) -> None:
        """Raise unless the session is idle: no attribute change in between."""
        self._check_open()
        status = self.pgconn.transaction_status
        if status != TransactionStatus.IDLE:
            name = TransactionStatus(status).name
            raise e.ProgrammingError(
                f"cannot change {attribute} inside a transaction"
                f" (transaction status {name})"
            )

    def _begin_statement(self) -> bytes:
        """The statement that starts a transaction; see the options mixin."""
        return b"BEGIN"

    def _finish(self) -> None:
        """Close the native connection; never waits for a running statement."""
        self._closed = True
        self.pgconn.finish()

    def _assign(self, name: str, value: Any) -> None:
        """Set a session attribute by assignment; the public classes define it."""
        raise NotImplementedError

    def _on_notice(self, result: PGresult) -> None:
        """Called inside the library for every notice; see the notices mixin."""

    def _on_notify(self, pgn: PGnotify) -> None:
        """Called for every notification; see the notify mixin."""
```

### 4.4 `BaseConnection` (`stuntgres/_connection_base.py`)

The composition, with the bases in exactly this order (every mixin derives from
`ConnCore`, so the method resolution order is `BaseConnection`,
`BaseConnectionFeatures`, `CancelMixin`, `NotifyMixin`, `NoticesMixin`,
`TxnOptionsMixin`, `ExecMixin`, `ConnCore`, `object`):

```python
"""The base class of Connection and AsyncConnection."""
from __future__ import annotations

from ._conn_cancel import CancelMixin
from ._conn_core import ConnCore, IsolationLevel, Notify
from ._conn_exec import ExecMixin
from ._conn_notices import NoticesMixin
from ._conn_notify import NotifyMixin
from ._conn_txnopts import TxnOptionsMixin
from ._features import BaseConnectionFeatures

__all__ = ["BaseConnection", "ConnCore", "IsolationLevel", "Notify"]


class BaseConnection(
    BaseConnectionFeatures,
    CancelMixin,
    NotifyMixin,
    NoticesMixin,
    TxnOptionsMixin,
    ExecMixin,
):
    """All state and every generator of a connection; no waiting, no lock."""
```

### 4.5 Every SQL statement this layer sends

| Statement (bytes, exactly) | Sent by | When |
|---|---|---|
| `BEGIN` followed, in this order and each only when its attribute is not `None`, by ` ISOLATION LEVEL READ UNCOMMITTED` \| ` ISOLATION LEVEL READ COMMITTED` \| ` ISOLATION LEVEL REPEATABLE READ` \| ` ISOLATION LEVEL SERIALIZABLE`, then ` READ ONLY` \| ` READ WRITE`, then ` DEFERRABLE` \| ` NOT DEFERRABLE` | `_start_query` (4.6); `BaseTransaction._enter_gen` (5.4) | before a statement when `autocommit` is off and the session is idle; on entering an outermost `transaction()` block |
| `COMMIT` | `_commit_gen`; `_exit_gen` of an outermost block | `commit()` when the session is not idle; leaving the block normally |
| `ROLLBACK` | `_rollback_gen`; `_exit_gen` of an outermost block | `rollback()` when the session is not idle; leaving the block with an exception or with `force_rollback` |
| `SAVEPOINT "<name>"` | `_enter_gen` | entering a nested block |
| `BEGIN...; SAVEPOINT "<name>"` (the `BEGIN` of the first row, `; `, the savepoint) | `_enter_gen` | entering an outermost block that was given a `savepoint_name` |
| `RELEASE "<name>"` | `_exit_gen` | leaving a nested block normally |
| `ROLLBACK TO "<name>"; RELEASE "<name>"` | `_exit_gen` | leaving a nested block with an exception or with `force_rollback` |
| the user's statement, unchanged or with `$n` for the placeholders (ADAPT 2) | `CurCore._execute_gen`, `ManyMixin._executemany_gen` | `execute`, `executemany` |

`<name>` is the savepoint name with every `"` doubled, in the client encoding;
automatic names are `_sg_tx_<n>` (5.4). Nothing is sent at connect, at `close()`, by
`cursor()`, by a fetch method, by `cancel()` (a cancel request is not SQL) or by
`notifies()`. A COPY that `execute` refuses is ended with a protocol message, not with
SQL (6.3).

### 4.6 The command path (`stuntgres/_conn_exec.py`)

```python
class ExecMixin(ConnCore):
    @property
    def autocommit(self) -> bool: ...
    @autocommit.setter
    def autocommit(self, value: bool) -> None: ...
    def _set_autocommit(self, value: bool) -> None: ...
    def _exec_command(self, command: bytes) -> PQGen[PGresult]: ...
    def _start_query(self) -> PQGen[None]: ...
    def _check_not_managed(self, method: str) -> None: ...
    def _commit_gen(self) -> PQGen[None]: ...
    def _rollback_gen(self) -> PQGen[None]: ...
```

| Member | Algorithm |
|---|---|
| `autocommit` (get) | `self._autocommit`. |
| `autocommit` (set) | `self._assign("autocommit", value)` (9.2: the synchronous class calls `set_autocommit`; 9.4: the asyncio class raises `AttributeError`). |
| `_set_autocommit(value)` | `_check_intrans("autocommit")`; `self._autocommit = bool(value)`. |
| `_exec_command(command)` | For the statements of 4.5. 1. `_check_open()`. 2. `pgconn.send_query(command)`. 3. `results = yield from generators.execute(pgconn)` (PQ 7.6). 4. No result: raise `InternalError(f"no result for {text}")` (`text` = `command.decode("ascii", "replace")`). 5. For each result in order: if its status is `FATAL_ERROR`, `NONFATAL_ERROR` or `BAD_RESPONSE`: raise `error_from_result(result, pgconn._encoding)`. 6. `result = results[-1]`; if its status is neither `COMMAND_OK` nor `TUPLES_OK`: raise `InternalError(f"unexpected result {name} for {text}")`. 7. Return `result`. |
| `_start_query()` | Before every user statement. 1. `_check_open()`. 2. If `_autocommit`: return. 3. If `pgconn.transaction_status != IDLE`: return. 4. `yield from self._exec_command(self._begin_statement())`. So the `BEGIN` travels alone, in its own round trip, and only when the server says the session is idle. |
| `_check_not_managed(method)` | If `_num_transactions` is not 0: raise `ProgrammingError(f"{method}() cannot be used inside a transaction() block")`. If `_tpc is not None`: raise `ProgrammingError(f"{method}() cannot be used during a two-phase transaction")`. |
| `_commit_gen()` | 1. `_check_open()`. 2. `_check_not_managed("commit")`. 3. If `pgconn.transaction_status == IDLE`: return (nothing is sent). 4. `yield from self._exec_command(b"COMMIT")`. |
| `_rollback_gen()` | The same with `"rollback"` and `b"ROLLBACK"`. |

Consequences, each **(measured)** in both modes (`tests/test_conn_exec.py`):

- Two statements in a row send one `BEGIN` (a second one would raise warning `25001`,
  which the test would see).
- `commit()` and `rollback()` on an idle session send nothing (a `COMMIT` there would
  raise warning `25P01`).
- A transaction opened or ended by the user's own SQL (`begin`, `commit`, `rollback`
  through `execute`) is seen through the transaction status: after `execute("commit")`
  the next statement gets a new `BEGIN`; after `execute("begin")` on an autocommit
  connection `commit()` sends `COMMIT`.
- After an error inside a transaction the status is `INERROR`; the next statement raises
  `InFailedSqlTransaction`; `rollback()` sends `ROLLBACK`; `commit()` sends `COMMIT`,
  which the server answers with the tag `ROLLBACK`, and raises nothing.
- A `COMMIT` that fails (a deferred constraint) raises the server's error from
  `commit()`; the session is idle afterwards and the transaction is gone.
- `close()` with an open transaction sends nothing; another connection never sees the
  uncommitted rows.

The complete file:

```python
"""The command path of a connection: implicit BEGIN, COMMIT, ROLLBACK."""
from __future__ import annotations

from typing import TYPE_CHECKING

from . import errors as e
from . import generators
from ._conn_core import ConnCore
from ._diagnostic import error_from_result
from ._gentypes import PQGen
from .pq import ExecStatus, TransactionStatus

if TYPE_CHECKING:
    from .pq import PGresult

_OK = (ExecStatus.COMMAND_OK, ExecStatus.TUPLES_OK)
_FAILED = (
    ExecStatus.FATAL_ERROR, ExecStatus.NONFATAL_ERROR, ExecStatus.BAD_RESPONSE,
)


class ExecMixin(ConnCore):
    """Sends the statements the connection itself issues."""

    @property
    def autocommit(self) -> bool:
        """False (the default): a transaction is begun before a statement."""
        return self._autocommit

    @autocommit.setter
    def autocommit(self, value: bool) -> None:
        self._assign("autocommit", value)

    def _set_autocommit(self, value: bool) -> None:
        self._check_intrans("autocommit")
        self._autocommit = bool(value)

    def _exec_command(self, command: bytes) -> PQGen[PGresult]:
        """Run a statement of the connection's own; return its last result."""
        self._check_open()
        self.pgconn.send_query(command)
        results = yield from generators.execute(self.pgconn)
        if not results:
            raise e.InternalError(f"no result for {command.decode('ascii', 'replace')}")
        for result in results:
            if result.status in _FAILED:
                raise error_from_result(result, self.pgconn._encoding)
        result = results[-1]
        if result.status not in _OK:
            name = ExecStatus(result.status).name
            raise e.InternalError(
                f"unexpected result {name} for {command.decode('ascii', 'replace')}"
            )
        return result

    def _start_query(self) -> PQGen[None]:
        """Before a statement: BEGIN when autocommit is off and none is open."""
        self._check_open()
        if self._autocommit:
            return
        if self.pgconn.transaction_status != TransactionStatus.IDLE:
            return
        yield from self._exec_command(self._begin_statement())

    def _check_not_managed(self, method: str) -> None:
        if self._num_transactions:
            raise e.ProgrammingError(
                f"{method}() cannot be used inside a transaction() block"
            )
        if self._tpc is not None:
            raise e.ProgrammingError(
                f"{method}() cannot be used during a two-phase transaction"
            )

    def _commit_gen(self) -> PQGen[None]:
        """COMMIT, unless the session is idle."""
        self._check_open()
        self._check_not_managed("commit")
        if self.pgconn.transaction_status == TransactionStatus.IDLE:
            return
        yield from self._exec_command(b"COMMIT")

    def _rollback_gen(self) -> PQGen[None]:
        """ROLLBACK, unless the session is idle."""
        self._check_open()
        self._check_not_managed("rollback")
        if self.pgconn.transaction_status == TransactionStatus.IDLE:
            return
        yield from self._exec_command(b"ROLLBACK")
```

### 4.7 The client encoding

The encoding of a session is whatever the server reports in the parameter
`client_encoding`; it is read through `PGconn._encoding` (PQ 5.4), which maps the name
with `stuntgres._encodings.python_codec` (PQ 4.3: 40 PostgreSQL names; default
`"utf-8"` when the parameter is unknown or the connection is not open). This design adds
no table of its own. It is read at these points:

| Reader | When | Used for |
|---|---|---|
| `Transformer(cursor)` (ADAPT 3.5) | once per `execute` or `executemany` | the query text, `str` parameters, text results |
| `CurCore.description` | from that transformer | column names |
| `ConnCore._check_open` | at the start of every operation, into `_codec` | notices and notifications that arrive during the operation (8.1, 8.2), savepoint names (5.4) |
| `error_from_result` callers | when an error result is converted | the message and the diagnostics |
| `ConnectionInfo` | on every access | its text members |

A statement `SET client_encoding TO ...` is therefore honoured from the next operation
on; set the encoding at connect time (`connect(..., client_encoding="latin1")`) or in a
statement of its own.

**(measured, the same in both modes, `exp7/probe_enc.oracle.txt`)**

| Client encoding | Codec | `select %s` with `"\u00e9"` | `select %s` with `"\u017c"` | `select chr(233)` |
|---|---|---|---|---|
| `UTF8` (default) | `utf-8` | `"\u00e9"` | `"\u017c"` | `"\u00e9"` |
| `LATIN1` | `iso8859-1` | `"\u00e9"` | `UnicodeEncodeError` | `"\u00e9"` |
| `WIN1250` | `cp1250` | `"\u00e9"` | `"\u017c"` | `"\u00e9"` |
| `SQL_ASCII` | `ascii` | `UnicodeEncodeError` | `UnicodeEncodeError` | `UnicodeDecodeError` (the server sends the bytes `c3 a9` unconverted) |
| `EUC_TW` | none | every operation raises `NotSupportedError("client encoding EUC_TW has no Python codec")`; `close()` still works | | |

**`SQL_ASCII`** means "no conversion" to the server. The driver treats it as the codec
`ascii`: ASCII text works in both directions; a `str` with a non-ASCII character raises
`UnicodeEncodeError` before anything is sent; non-ASCII bytes in a text result raise
`UnicodeDecodeError` when the row is fetched; `bytes` parameters and `bytea` results are
unaffected. The two `UnicodeError` classes are Python's own (subclasses of `ValueError`),
not `stuntgres` exceptions; the connection stays usable. An error message of the server
is decoded with errors `"replace"`, so reporting an error never fails.

## 5. Transactions

### 5.1 What the session does (experiment 1)

The driver keeps no transaction flag: it asks `pgconn.transaction_status` (PQ 5.4) and
the server is the only authority. This table is the complete record of experiment 1:
each statement was sent alone with `send_query` on one session; "Status after" is
`transaction_status` once every result was collected; the last column compares the
stuntgres server (135 steps; 1 of them differs in server mode: the command tag of `START TRANSACTION`, 1.1).

| Statement sent | Status after | Result | Notice | Server mode |
|---|---|---|---|---|
| **implicit BEGIN, commit, rollback** | | | | |
| `BEGIN` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `select 1` | INTRANS | TUPLES_OK `SELECT 1` rows `[(b'1',)]` |  | same |
| `COMMIT` | IDLE | COMMAND_OK `COMMIT` |  | same |
| `BEGIN` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `create table t1 (id int primary key, v text)` | INTRANS | COMMAND_OK `CREATE TABLE` |  | same |
| `insert into t1 values (1, 'a')` | INTRANS | COMMAND_OK `INSERT 0 1` |  | same |
| `ROLLBACK` | IDLE | COMMAND_OK `ROLLBACK` |  | same |
| `select count(*) from pg_class where relname = 't1'` | IDLE | TUPLES_OK `SELECT 1` rows `[(b'0',)]` |  | same |
| **commit and rollback when idle** | | | | |
| `COMMIT` | IDLE | COMMAND_OK `COMMIT` | WARNING 25P01 there is no transaction in progress | same |
| `ROLLBACK` | IDLE | COMMAND_OK `ROLLBACK` | WARNING 25P01 there is no transaction in progress | same |
| **an error inside a transaction, then a statement, then rollback** | | | | |
| `BEGIN` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `select 1/0` | INERROR | FATAL_ERROR `22012` division by zero |  | same |
| `select 1` | INERROR | FATAL_ERROR `25P02` current transaction is aborted, commands ignored until end of transaction block |  | same |
| `ROLLBACK` | IDLE | COMMAND_OK `ROLLBACK` |  | same |
| **an error inside a transaction, then commit** | | | | |
| `BEGIN` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `select 1/0` | INERROR | FATAL_ERROR `22012` division by zero |  | same |
| `COMMIT` | IDLE | COMMAND_OK `ROLLBACK` |  | same |
| **an error outside a transaction** | | | | |
| `select 1/0` | IDLE | FATAL_ERROR `22012` division by zero |  | same |
| **user-sent transaction control** | | | | |
| `begin` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `BEGIN` | INTRANS | COMMAND_OK `BEGIN` | WARNING 25001 there is already a transaction in progress | same |
| `commit` | IDLE | COMMAND_OK `COMMIT` |  | same |
| `start transaction` | INTRANS | COMMAND_OK `START TRANSACTION` |  | status INTRANS; COMMAND_OK `BEGIN` |
| `end` | IDLE | COMMAND_OK `COMMIT` |  | same |
| `begin; select 1` | INTRANS | COMMAND_OK `BEGIN`; TUPLES_OK `SELECT 1` rows `[(b'1',)]` |  | same |
| `abort` | IDLE | COMMAND_OK `ROLLBACK` |  | same |
| `begin transaction isolation level serializable` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `show transaction_isolation` | INTRANS | TUPLES_OK `SHOW` rows `[(b'serializable',)]` |  | same |
| `rollback` | IDLE | COMMAND_OK `ROLLBACK` |  | same |
| **a failing COMMIT of a deferred constraint** | | | | |
| `create table p (id int primary key)` | IDLE | COMMAND_OK `CREATE TABLE` |  | same |
| `create table ch (id int primary key, pid int references p (id) deferrable initially deferred)` | IDLE | COMMAND_OK `CREATE TABLE` |  | same |
| `create table u (id int, constraint u_id unique (id) deferrable initially deferred)` | IDLE | COMMAND_OK `CREATE TABLE` |  | same |
| `BEGIN` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `insert into ch values (1, 42)` | INTRANS | COMMAND_OK `INSERT 0 1` |  | same |
| `COMMIT` | IDLE | FATAL_ERROR `23503` insert or update on table "ch" violates foreign key constraint "ch_pid_fkey" |  | same |
| `select count(*) from ch` | IDLE | TUPLES_OK `SELECT 1` rows `[(b'0',)]` |  | same |
| `BEGIN` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `insert into u values (1), (1)` | INTRANS | COMMAND_OK `INSERT 0 2` |  | same |
| `COMMIT` | IDLE | FATAL_ERROR `23505` duplicate key value violates unique constraint "u_id" |  | same |
| `select count(*) from u` | IDLE | TUPLES_OK `SELECT 1` rows `[(b'0',)]` |  | same |
| **every BEGIN option string** | | | | |
| `BEGIN` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `select current_setting('transaction_isolation'), current_setting('transaction_read_only'), current_setting('transaction_deferrable')` | INTRANS | TUPLES_OK `SELECT 1` rows `[(b'read committed', b'off', b'off')]` |  | same |
| `ROLLBACK` | IDLE | COMMAND_OK `ROLLBACK` |  | same |
| `BEGIN ISOLATION LEVEL READ UNCOMMITTED` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `select current_setting('transaction_isolation'), current_setting('transaction_read_only'), current_setting('transaction_deferrable')` | INTRANS | TUPLES_OK `SELECT 1` rows `[(b'read uncommitted', b'off', b'off')]` |  | same |
| `ROLLBACK` | IDLE | COMMAND_OK `ROLLBACK` |  | same |
| `BEGIN ISOLATION LEVEL READ COMMITTED` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `select current_setting('transaction_isolation'), current_setting('transaction_read_only'), current_setting('transaction_deferrable')` | INTRANS | TUPLES_OK `SELECT 1` rows `[(b'read committed', b'off', b'off')]` |  | same |
| `ROLLBACK` | IDLE | COMMAND_OK `ROLLBACK` |  | same |
| `BEGIN ISOLATION LEVEL REPEATABLE READ` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `select current_setting('transaction_isolation'), current_setting('transaction_read_only'), current_setting('transaction_deferrable')` | INTRANS | TUPLES_OK `SELECT 1` rows `[(b'repeatable read', b'off', b'off')]` |  | same |
| `ROLLBACK` | IDLE | COMMAND_OK `ROLLBACK` |  | same |
| `BEGIN ISOLATION LEVEL SERIALIZABLE` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `select current_setting('transaction_isolation'), current_setting('transaction_read_only'), current_setting('transaction_deferrable')` | INTRANS | TUPLES_OK `SELECT 1` rows `[(b'serializable', b'off', b'off')]` |  | same |
| `ROLLBACK` | IDLE | COMMAND_OK `ROLLBACK` |  | same |
| `BEGIN READ ONLY` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `select current_setting('transaction_isolation'), current_setting('transaction_read_only'), current_setting('transaction_deferrable')` | INTRANS | TUPLES_OK `SELECT 1` rows `[(b'read committed', b'on', b'off')]` |  | same |
| `ROLLBACK` | IDLE | COMMAND_OK `ROLLBACK` |  | same |
| `BEGIN READ WRITE` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `select current_setting('transaction_isolation'), current_setting('transaction_read_only'), current_setting('transaction_deferrable')` | INTRANS | TUPLES_OK `SELECT 1` rows `[(b'read committed', b'off', b'off')]` |  | same |
| `ROLLBACK` | IDLE | COMMAND_OK `ROLLBACK` |  | same |
| `BEGIN DEFERRABLE` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `select current_setting('transaction_isolation'), current_setting('transaction_read_only'), current_setting('transaction_deferrable')` | INTRANS | TUPLES_OK `SELECT 1` rows `[(b'read committed', b'off', b'on')]` |  | same |
| `ROLLBACK` | IDLE | COMMAND_OK `ROLLBACK` |  | same |
| `BEGIN NOT DEFERRABLE` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `select current_setting('transaction_isolation'), current_setting('transaction_read_only'), current_setting('transaction_deferrable')` | INTRANS | TUPLES_OK `SELECT 1` rows `[(b'read committed', b'off', b'off')]` |  | same |
| `ROLLBACK` | IDLE | COMMAND_OK `ROLLBACK` |  | same |
| `BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `select current_setting('transaction_isolation'), current_setting('transaction_read_only'), current_setting('transaction_deferrable')` | INTRANS | TUPLES_OK `SELECT 1` rows `[(b'serializable', b'on', b'on')]` |  | same |
| `ROLLBACK` | IDLE | COMMAND_OK `ROLLBACK` |  | same |
| `BEGIN ISOLATION LEVEL REPEATABLE READ READ WRITE NOT DEFERRABLE` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `select current_setting('transaction_isolation'), current_setting('transaction_read_only'), current_setting('transaction_deferrable')` | INTRANS | TUPLES_OK `SELECT 1` rows `[(b'repeatable read', b'off', b'off')]` |  | same |
| `ROLLBACK` | IDLE | COMMAND_OK `ROLLBACK` |  | same |
| **read only is enforced** | | | | |
| `BEGIN READ ONLY` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `insert into p values (1)` | INERROR | FATAL_ERROR `25006` cannot execute INSERT in a read-only transaction |  | same |
| `ROLLBACK` | IDLE | COMMAND_OK `ROLLBACK` |  | same |
| **session defaults are used by a plain BEGIN** | | | | |
| `set default_transaction_isolation = 'serializable'` | IDLE | COMMAND_OK `SET` |  | same |
| `set default_transaction_read_only = on` | IDLE | COMMAND_OK `SET` |  | same |
| `BEGIN` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `select current_setting('transaction_isolation'), current_setting('transaction_read_only')` | INTRANS | TUPLES_OK `SELECT 1` rows `[(b'serializable', b'on')]` |  | same |
| `ROLLBACK` | IDLE | COMMAND_OK `ROLLBACK` |  | same |
| `BEGIN READ WRITE` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `select current_setting('transaction_isolation'), current_setting('transaction_read_only')` | INTRANS | TUPLES_OK `SELECT 1` rows `[(b'serializable', b'off')]` |  | same |
| `ROLLBACK` | IDLE | COMMAND_OK `ROLLBACK` |  | same |
| `reset default_transaction_isolation` | IDLE | COMMAND_OK `RESET` |  | same |
| `reset default_transaction_read_only` | IDLE | COMMAND_OK `RESET` |  | same |
| **savepoints** | | | | |
| `BEGIN` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `SAVEPOINT "_sg_tx_1"` | INTRANS | COMMAND_OK `SAVEPOINT` |  | same |
| `insert into p values (1)` | INTRANS | COMMAND_OK `INSERT 0 1` |  | same |
| `RELEASE "_sg_tx_1"` | INTRANS | COMMAND_OK `RELEASE` |  | same |
| `COMMIT` | IDLE | COMMAND_OK `COMMIT` |  | same |
| `select count(*) from p` | IDLE | TUPLES_OK `SELECT 1` rows `[(b'1',)]` |  | same |
| `BEGIN` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `SAVEPOINT "_sg_tx_1"` | INTRANS | COMMAND_OK `SAVEPOINT` |  | same |
| `insert into p values (2)` | INTRANS | COMMAND_OK `INSERT 0 1` |  | same |
| `ROLLBACK TO "_sg_tx_1"` | INTRANS | COMMAND_OK `ROLLBACK` |  | same |
| `RELEASE "_sg_tx_1"` | INTRANS | COMMAND_OK `RELEASE` |  | same |
| `COMMIT` | IDLE | COMMAND_OK `COMMIT` |  | same |
| `select count(*) from p` | IDLE | TUPLES_OK `SELECT 1` rows `[(b'1',)]` |  | same |
| `BEGIN` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `SAVEPOINT "_sg_tx_1"` | INTRANS | COMMAND_OK `SAVEPOINT` |  | same |
| `select 1/0` | INERROR | FATAL_ERROR `22012` division by zero |  | same |
| `ROLLBACK TO "_sg_tx_1"` | INTRANS | COMMAND_OK `ROLLBACK` |  | same |
| `RELEASE "_sg_tx_1"` | INTRANS | COMMAND_OK `RELEASE` |  | same |
| `select 2` | INTRANS | TUPLES_OK `SELECT 1` rows `[(b'2',)]` |  | same |
| `COMMIT` | IDLE | COMMAND_OK `COMMIT` |  | same |
| `BEGIN` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `SAVEPOINT "_sg_tx_1"` | INTRANS | COMMAND_OK `SAVEPOINT` |  | same |
| `select 1/0` | INERROR | FATAL_ERROR `22012` division by zero |  | same |
| `RELEASE "_sg_tx_1"` | INERROR | FATAL_ERROR `25P02` current transaction is aborted, commands ignored until end of transaction block |  | same |
| `ROLLBACK` | IDLE | COMMAND_OK `ROLLBACK` |  | same |
| `BEGIN` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `SAVEPOINT "_sg_tx_1"` | INTRANS | COMMAND_OK `SAVEPOINT` |  | same |
| `SAVEPOINT "_sg_tx_2"` | INTRANS | COMMAND_OK `SAVEPOINT` |  | same |
| `insert into p values (3)` | INTRANS | COMMAND_OK `INSERT 0 1` |  | same |
| `ROLLBACK TO "_sg_tx_1"` | INTRANS | COMMAND_OK `ROLLBACK` |  | same |
| `RELEASE "_sg_tx_2"` | INERROR | FATAL_ERROR `3B001` savepoint "_sg_tx_2" does not exist |  | same |
| `ROLLBACK` | IDLE | COMMAND_OK `ROLLBACK` |  | same |
| `SAVEPOINT "_sg_tx_1"` | IDLE | FATAL_ERROR `25P01` SAVEPOINT can only be used in transaction blocks |  | same |
| `RELEASE "_sg_tx_1"` | IDLE | FATAL_ERROR `25P01` RELEASE SAVEPOINT can only be used in transaction blocks |  | same |
| `ROLLBACK TO "_sg_tx_1"` | IDLE | FATAL_ERROR `25P01` ROLLBACK TO SAVEPOINT can only be used in transaction blocks |  | same |
| `BEGIN` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `ROLLBACK TO "nosuch"` | INERROR | FATAL_ERROR `3B001` savepoint "nosuch" does not exist |  | same |
| `ROLLBACK` | IDLE | COMMAND_OK `ROLLBACK` |  | same |
| `BEGIN` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `SAVEPOINT "a b""c"` | INTRANS | COMMAND_OK `SAVEPOINT` |  | same |
| `ROLLBACK TO "a b""c"; RELEASE "a b""c"` | INTRANS | COMMAND_OK `ROLLBACK`; COMMAND_OK `RELEASE` |  | same |
| `COMMIT` | IDLE | COMMAND_OK `COMMIT` |  | same |
| **changing transaction characteristics by SQL** | | | | |
| `BEGIN` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `SET TRANSACTION ISOLATION LEVEL SERIALIZABLE` | INTRANS | COMMAND_OK `SET` |  | same |
| `show transaction_isolation` | INTRANS | TUPLES_OK `SHOW` rows `[(b'serializable',)]` |  | same |
| `COMMIT` | IDLE | COMMAND_OK `COMMIT` |  | same |
| `BEGIN` | INTRANS | COMMAND_OK `BEGIN` |  | same |
| `select 1` | INTRANS | TUPLES_OK `SELECT 1` rows `[(b'1',)]` |  | same |
| `SET TRANSACTION ISOLATION LEVEL SERIALIZABLE` | INERROR | FATAL_ERROR `25001` SET TRANSACTION ISOLATION LEVEL must be called before any query |  | same |
| `ROLLBACK` | IDLE | COMMAND_OK `ROLLBACK` |  | same |

What the design takes from it:

| Status before | Driver action | Statement sent | Status after |
|---|---|---|---|
| `IDLE`, autocommit off | a user statement | `BEGIN ...`, then the statement | `INTRANS`, or `INERROR` when the statement failed |
| `IDLE`, autocommit on | a user statement | the statement | `IDLE` |
| `INTRANS` | a user statement | the statement | `INTRANS` or `INERROR` |
| `INERROR` | a user statement | the statement | `INERROR`; the statement raises `InFailedSqlTransaction` (`25P02`) |
| `IDLE` | `commit()`, `rollback()` | nothing | `IDLE` |
| `INTRANS` | `commit()` | `COMMIT` | `IDLE`; when the commit itself fails (a deferred constraint) the error is raised and the status is `IDLE` all the same |
| `INTRANS`, `INERROR` | `rollback()` | `ROLLBACK` | `IDLE` |
| `INERROR` | `commit()` | `COMMIT` | `IDLE`: the server rolls back and answers with the tag `ROLLBACK`; nothing is raised |
| any | the user sends `BEGIN`, `COMMIT`, `ROLLBACK`, `START TRANSACTION`, `END`, `ABORT` as SQL | that statement | as the server says; the driver follows |
| `IDLE` | setting `autocommit`, `isolation_level`, `read_only`, `deferrable` | nothing | `IDLE` |
| not `IDLE` | setting one of the four | nothing | unchanged; `ProgrammingError` |
| `IDLE` | entering `transaction()` | `BEGIN ...` (also on an autocommit connection) | `INTRANS` |
| not `IDLE` | entering `transaction()` | `SAVEPOINT "..."` | `INTRANS`, or the error `25P02` when the status was `INERROR` |
| `ACTIVE` | cannot happen between operations: every operation collects all results; the one exception is an interruption whose cancel failed, and then the connection is closed (8.4) | | |
| `UNKNOWN` | the connection is closed or lost | nothing | `OperationalError` from `_check_open` |

Facts of PostgreSQL the table shows and tests rely on: a plain `BEGIN` takes the
session defaults (`default_transaction_isolation`, `default_transaction_read_only`), and
an attribute that is `None` leaves them alone; `READ ONLY` is enforced with `25006`; a
second `BEGIN` raises warning `25001` and `COMMIT` or `ROLLBACK` outside a transaction
warning `25P01` (which is how the tests prove what was *not* sent); `SAVEPOINT`,
`RELEASE` and `ROLLBACK TO` outside a transaction block are errors (`25P01`);
`ROLLBACK TO` leaves `INERROR`, `RELEASE` does not.

### 5.2 `commit`, `rollback`, `autocommit`

The generators are `_commit_gen`, `_rollback_gen` and the plain method `_set_autocommit`
of 4.6; the public methods are in 9.2 and 9.4. `with connection:` (9.2):

1. `__enter__` returns the connection.
2. `__exit__(exc_type, exc_val, exc_tb)`: if `self.closed`: return. Otherwise, inside
   `try:` / `finally: self.close()`: when `exc_type is None` call `self.commit()` (an
   error of the commit propagates); else call `self.rollback()` and, if that raises an
   `Exception`, log `"error ignored in rollback of %r: %s"` at level `WARNING` and let
   the original exception go on. `__exit__` returns `None`: the exception of the block
   is never swallowed.

This is psycopg 3's behavior and differs from `sqlite3` and psycopg2, whose `with` leaves
the connection open.

### 5.3 Transaction characteristics (`stuntgres/_conn_txnopts.py`)

```python
class TxnOptionsMixin(ConnCore):
    @property
    def isolation_level(self) -> IsolationLevel | None: ...
    @property
    def read_only(self) -> bool | None: ...
    @property
    def deferrable(self) -> bool | None: ...
    def _set_isolation_level(self, value: IsolationLevel | None) -> None: ...
    def _set_read_only(self, value: bool | None) -> None: ...
    def _set_deferrable(self, value: bool | None) -> None: ...
    def _begin_statement(self) -> bytes: ...
```

Each property also has a setter that calls `self._assign("<name>", value)`, exactly like
`autocommit` (4.6). `None` means "say nothing in `BEGIN`": the session default applies.

| Member | Algorithm |
|---|---|
| `_set_isolation_level(value)` | `_check_intrans("isolation_level")`; `self._isolation_level = None if value is None else IsolationLevel(value)` (an `int` 1 to 4 is accepted; anything else raises `ValueError` from the enum and nothing changes). |
| `_set_read_only(value)` | `_check_intrans("read_only")`; `None if value is None else bool(value)`. |
| `_set_deferrable(value)` | `_check_intrans("deferrable")`; the same. |
| `_begin_statement()` | `parts = [b"BEGIN"]`; if `_isolation_level is not None`: append `b"ISOLATION LEVEL "` + the level's SQL name (`READ UNCOMMITTED`, `READ COMMITTED`, `REPEATABLE READ`, `SERIALIZABLE`); if `_read_only is not None`: append `b"READ ONLY"` or `b"READ WRITE"`; if `_deferrable is not None`: append `b"DEFERRABLE"` or `b"NOT DEFERRABLE"`; return `b" ".join(parts)`. |

**(measured, both modes)** Every statement this function can produce was sent and
`transaction_isolation`, `transaction_read_only`, `transaction_deferrable` read back:

| `isolation_level` | `read_only` | `deferrable` | `_begin_statement()` | The three settings |
|---|---|---|---|---|
| `None` | `None` | `None` | `BEGIN` | `read committed`, `off`, `off` |
| `READ_UNCOMMITTED` | `None` | `None` | `BEGIN ISOLATION LEVEL READ UNCOMMITTED` | `read uncommitted`, `off`, `off` |
| `READ_COMMITTED` | `None` | `None` | `BEGIN ISOLATION LEVEL READ COMMITTED` | `read committed`, `off`, `off` |
| `REPEATABLE_READ` | `None` | `None` | `BEGIN ISOLATION LEVEL REPEATABLE READ` | `repeatable read`, `off`, `off` |
| `SERIALIZABLE` | `None` | `None` | `BEGIN ISOLATION LEVEL SERIALIZABLE` | `serializable`, `off`, `off` |
| `None` | `True` | `None` | `BEGIN READ ONLY` | `read committed`, `on`, `off` |
| `None` | `False` | `None` | `BEGIN READ WRITE` | `read committed`, `off`, `off` |
| `None` | `None` | `True` | `BEGIN DEFERRABLE` | `read committed`, `off`, `on` |
| `None` | `None` | `False` | `BEGIN NOT DEFERRABLE` | `read committed`, `off`, `off` |
| `SERIALIZABLE` | `True` | `True` | `BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE` | `serializable`, `on`, `on` |
| `REPEATABLE_READ` | `False` | `False` | `BEGIN ISOLATION LEVEL REPEATABLE READ READ WRITE NOT DEFERRABLE` | `repeatable read`, `off`, `off` |

On an autocommit connection the three attributes have no effect on plain statements
(no `BEGIN` is sent); they apply to `transaction()` blocks, which do send it.

The complete file:

```python
"""Characteristics of the transactions a connection begins."""
from __future__ import annotations

from ._conn_core import ConnCore, IsolationLevel

_LEVELS = {
    IsolationLevel.READ_UNCOMMITTED: b"READ UNCOMMITTED",
    IsolationLevel.READ_COMMITTED: b"READ COMMITTED",
    IsolationLevel.REPEATABLE_READ: b"REPEATABLE READ",
    IsolationLevel.SERIALIZABLE: b"SERIALIZABLE",
}


class TxnOptionsMixin(ConnCore):
    """isolation_level, read_only and deferrable, and the BEGIN they give."""

    @property
    def isolation_level(self) -> IsolationLevel | None:
        """Isolation level of new transactions; None: the session default."""
        return self._isolation_level

    @isolation_level.setter
    def isolation_level(self, value: IsolationLevel | None) -> None:
        self._assign("isolation_level", value)

    @property
    def read_only(self) -> bool | None:
        """Whether new transactions are read-only; None: the session default."""
        return self._read_only

    @read_only.setter
    def read_only(self, value: bool | None) -> None:
        self._assign("read_only", value)

    @property
    def deferrable(self) -> bool | None:
        """Whether new transactions are deferrable; None: the session default."""
        return self._deferrable

    @deferrable.setter
    def deferrable(self, value: bool | None) -> None:
        self._assign("deferrable", value)

    def _set_isolation_level(self, value: IsolationLevel | None) -> None:
        self._check_intrans("isolation_level")
        self._isolation_level = None if value is None else IsolationLevel(value)

    def _set_read_only(self, value: bool | None) -> None:
        self._check_intrans("read_only")
        self._read_only = None if value is None else bool(value)

    def _set_deferrable(self, value: bool | None) -> None:
        self._check_intrans("deferrable")
        self._deferrable = None if value is None else bool(value)

    def _begin_statement(self) -> bytes:
        """BEGIN with the options the three attributes ask for."""
        parts = [b"BEGIN"]
        if self._isolation_level is not None:
            parts.append(b"ISOLATION LEVEL " + _LEVELS[self._isolation_level])
        if self._read_only is not None:
            parts.append(b"READ ONLY" if self._read_only else b"READ WRITE")
        if self._deferrable is not None:
            parts.append(b"DEFERRABLE" if self._deferrable else b"NOT DEFERRABLE")
        return b" ".join(parts)
```

### 5.4 Transaction blocks (`stuntgres/transaction.py`)

```python
class Rollback(Exception):
    def __init__(self, transaction: BaseTransaction | None = None) -> None: ...

def quote_identifier(name: str) -> str: ...

class BaseTransaction:
    def __init__(
        self,
        connection: Any,
        savepoint_name: str | None = None,
        force_rollback: bool = False,
    ) -> None: ...
    @property
    def connection(self) -> Any: ...
    @property
    def savepoint_name(self) -> str | None: ...
    def _enter_gen(self) -> PQGen[None]: ...
    def _exit_gen(
        self, exc_type: type[BaseException] | None, exc_val: BaseException | None
    ) -> PQGen[bool]: ...

class Transaction(BaseTransaction):
    def __enter__(self) -> Transaction: ...
    def __exit__(
        self,
        exc_type: type[BaseException] | None,
        exc_val: BaseException | None,
        exc_tb: TracebackType | None,
    ) -> bool: ...
```

`Connection.transaction(savepoint_name=None, force_rollback=False)` returns a
`Transaction`; `AsyncConnection.transaction(...)` an `AsyncTransaction` (9.6), which
shares `BaseTransaction`. The names and the behavior are psycopg 3's
(`Connection.transaction`, `Rollback`), except that `transaction()` returns the object
itself, which is the context manager.

State of a block: `_conn`, `_savepoint_name` (the argument, or the automatic name once
entered), `force_rollback` (public attribute), `_outer` (`True` when the block began the
transaction), `_level` (its depth while it is active, else 0).

`quote_identifier(name)`: a NUL character raises `ProgrammingError("an identifier cannot
contain NUL characters")`; otherwise `'"' + name.replace('"', '""') + '"'`.
`BaseTransaction._savepoint()` returns that text encoded with `conn._codec`.

`_enter_gen()`:

1. If `_level` is not 0: raise `ProgrammingError("a transaction block cannot be entered
   twice")`.
2. `conn._check_open()`.
3. If `conn._tpc is not None`: raise `ProgrammingError("transaction() cannot be used
   during a two-phase transaction")`.
4. If `conn.pgconn.transaction_status == IDLE`: `_outer = True`; `command =
   conn._begin_statement()`; if a `savepoint_name` was given: `command += b"; SAVEPOINT "
   + self._savepoint()`.
5. Otherwise: `_outer = False`; if no `savepoint_name` was given: `_savepoint_name =
   f"_sg_tx_{conn._num_transactions + 1}"`; `command = b"SAVEPOINT " + self._savepoint()`.
6. `yield from conn._exec_command(command)`. An error leaves the counters untouched.
7. `conn._num_transactions += 1`; `self._level = conn._num_transactions`.

`_exit_gen(exc_type, exc_val)` returns `True` when the exception of the block ends
here:

1. If `_level` is 0 or `conn._num_transactions != _level`: raise
   `ProgrammingError("transaction blocks must be left in the reverse order of
   entering")`.
2. `conn._num_transactions -= 1`; `_level = 0`.
3. If `exc_type is None` and not `force_rollback`: send `COMMIT` (outer) or `RELEASE
   "<name>"` (nested) with `conn._exec_command`; return `False`. An error propagates.
4. Otherwise the block is rolled back. If `conn._wait_fileno() < 0` (the connection is
   closed or lost): return `False`, nothing can be sent.
5. Inside `try:`: send `ROLLBACK` (outer) or `ROLLBACK TO "<name>"; RELEASE "<name>"`
   (nested, one string). `except Exception as exc:` if `exc_val is None` (the rollback of
   a `force_rollback` block) re-raise; else log `"error ignored while rolling back %r:
   %s"` at `WARNING` and return `False`.
6. If `exc_val` is a `Rollback`: return `True` when `exc_val.transaction is None` or is
   this block, else `False` (the exception travels on to the block it names).
7. Return `False`.

`Transaction.__enter__`: `with conn.lock: conn.wait(self._enter_gen())`; return `self`.
`Transaction.__exit__`: `with conn.lock: return conn.wait(self._exit_gen(exc_type,
exc_val))`.

Rules that follow, each **(measured, both modes, `tests/test_transaction.py`)**:

- The outermost block on an idle connection sends `BEGIN` with the options of 5.3 and
  commits or rolls back at its end, whatever `autocommit` says. A block entered while a
  transaction is open (an implicit one included) is a savepoint and leaves the
  transaction open.
- Automatic savepoint names count the depth: `_sg_tx_2` inside one block, `_sg_tx_3`
  inside two; a block inside an implicit transaction is `_sg_tx_1`.
- `commit()`, `rollback()` and the four attribute setters raise `ProgrammingError` inside
  a block.
- `raise Rollback` leaves the innermost block quietly after rolling it back;
  `raise Rollback(outer)` leaves every block up to and including `outer`; a `Rollback`
  naming a block that is not active propagates as an exception.
- A statement error caught *inside* a nested block leaves the session in `INERROR`; the
  block's `RELEASE` then fails with `InFailedSqlTransaction`. Catch the error outside
  the block that failed.
- When the connection was closed inside the block: with an exception pending the block
  exits silently and the exception propagates; without one the `COMMIT` raises
  `OperationalError("the connection is closed")`.
- `BaseException`s (`KeyboardInterrupt`, task cancellation) roll the block back like any
  exception (8.4).

The complete file:

```python
"""Transaction blocks: Connection.transaction()."""
from __future__ import annotations

from types import TracebackType
from typing import TYPE_CHECKING, Any

from . import errors as e
from ._conn_core import logger
from ._gentypes import PQGen
from .pq import TransactionStatus

if TYPE_CHECKING:
    from .connection import Connection


class Rollback(Exception):
    """Raise inside a transaction() block to roll it back and leave it quietly.

    Without argument the innermost block is left; with a Transaction object,
    that block and every block inside it.
    """

    __module__ = "stuntgres"

    def __init__(self, transaction: BaseTransaction | None = None) -> None:
        super().__init__(transaction)
        self.transaction = transaction


def quote_identifier(name: str) -> str:
    """Return a name as a quoted SQL identifier."""
    if "\x00" in name:
        raise e.ProgrammingError("an identifier cannot contain NUL characters")
    return '"' + name.replace('"', '""') + '"'


class BaseTransaction:
    """State and statements of a transaction block; no waiting, no lock."""

    def __init__(
        self,
        connection: Any,
        savepoint_name: str | None = None,
        force_rollback: bool = False,
    ) -> None:
        self._conn = connection
        self._savepoint_name = savepoint_name
        self.force_rollback = force_rollback
        self._outer = False
        self._level = 0

    @property
    def connection(self) -> Any:
        """The connection the block belongs to."""
        return self._conn

    @property
    def savepoint_name(self) -> str | None:
        """The savepoint of the block; None for an outermost block without one."""
        return self._savepoint_name

    def __repr__(self) -> str:
        cls = type(self)
        state = "active" if self._level else "inactive"
        return (
            f"<{cls.__module__}.{cls.__qualname__} ({state},"
            f" savepoint {self._savepoint_name!r}) at 0x{id(self):x}>"
        )

    def _savepoint(self) -> bytes:
        assert self._savepoint_name is not None
        return quote_identifier(self._savepoint_name).encode(self._conn._codec)

    def _enter_gen(self) -> PQGen[None]:
        conn = self._conn
        if self._level:
            raise e.ProgrammingError("a transaction block cannot be entered twice")
        conn._check_open()
        if conn._tpc is not None:
            raise e.ProgrammingError(
                "transaction() cannot be used during a two-phase transaction"
            )
        if conn.pgconn.transaction_status == TransactionStatus.IDLE:
            self._outer = True
            command = conn._begin_statement()
            if self._savepoint_name:
                command += b"; SAVEPOINT " + self._savepoint()
        else:
            self._outer = False
            if not self._savepoint_name:
                self._savepoint_name = f"_sg_tx_{conn._num_transactions + 1}"
            command = b"SAVEPOINT " + self._savepoint()
        yield from conn._exec_command(command)
        conn._num_transactions += 1
        self._level = conn._num_transactions

    def _exit_gen(
        self, exc_type: type[BaseException] | None, exc_val: BaseException | None
    ) -> PQGen[bool]:
        """Leave the block; return True when the exception ends here."""
        conn = self._conn
        if not self._level or conn._num_transactions != self._level:
            raise e.ProgrammingError(
                "transaction blocks must be left in the reverse order of entering"
            )
        conn._num_transactions -= 1
        self._level = 0
        if exc_type is None and not self.force_rollback:
            if self._outer:
                yield from conn._exec_command(b"COMMIT")
            else:
                yield from conn._exec_command(b"RELEASE " + self._savepoint())
            return False
        if conn._wait_fileno() < 0:
            return False
        try:
            if self._outer:
                yield from conn._exec_command(b"ROLLBACK")
            else:
                name = self._savepoint()
                yield from conn._exec_command(
                    b"ROLLBACK TO " + name + b"; RELEASE " + name
                )
        except Exception as exc:
            if exc_val is None:
                raise
            logger.warning("error ignored while rolling back %r: %s", self, exc)
            return False
        if isinstance(exc_val, Rollback):
            return exc_val.transaction is None or exc_val.transaction is self
        return False


class Transaction(BaseTransaction):
    """The context manager Connection.transaction() returns."""

    __module__ = "stuntgres"

    _conn: Connection

    def __enter__(self) -> Transaction:
        with self._conn.lock:
            self._conn.wait(self._enter_gen())
        return self

    def __exit__(
        self,
        exc_type: type[BaseException] | None,
        exc_val: BaseException | None,
        exc_tb: TracebackType | None,
    ) -> bool:
        with self._conn.lock:
            return self._conn.wait(self._exit_gen(exc_type, exc_val))
```

## 6. Cursor base class

`BaseCursor` is `CurCore` plus four mixins. The public classes `Cursor` and `AsyncCursor`
are in 9.3 and 9.5.

### 6.1 State (`stuntgres/_cur_core.py`)

```python
class CurCore:
    def __init__(
        self, connection: Any, *, row_factory: RowFactory | None = None
    ) -> None: ...
    arraysize: int
    pgresult: PGresult | None
    @property
    def connection(self) -> Any: ...
    @property
    def adapters(self) -> AdaptersMap: ...
    @property
    def closed(self) -> bool: ...
    @property
    def row_factory(self) -> RowFactory: ...
    @property
    def description(self) -> list[Column] | None: ...
    @property
    def rowcount(self) -> int: ...
    @property
    def statusmessage(self) -> str | None: ...
```

| Attribute | Initial value | Meaning |
|---|---|---|
| `_conn` | the connection | `connection` returns it (PEP 249 extension; ADAPT 3.1) |
| `_adapters` | `AdaptersMap(connection.adapters)` | ADAPT 3.2: the cursor's own map, a snapshot of the connection's |
| `_row_factory` | the argument, or `connection.row_factory` | 7 |
| `arraysize` | `1` | default size of `fetchmany` (public, assignable) |
| `_closed` | `False` | |
| `_results` | `[]` | the results of the last `execute` or `executemany(returning=True)`, in order |
| `_iresult` | `0` | index of the current result |
| `pgresult` | `None` | the current result (public, psycopg's name); valid until the next `execute`, `executemany` or `close` |
| `_tx` | `None` | the `Transformer` of the last `execute` (ADAPT 3.5) |
| `_make_row` | `None` | the row maker of the current result; `None` means plain tuples |
| `_description` | `None` | cache of `description` for the current result |
| `_pos` | `0` | index of the next row to fetch |
| `_rowcount` | `-1` | |

`_reset()` puts the last eight back to these values after calling `clear()` on every
result of `_results`, so the native memory of a statement is released when the next one
starts and when the cursor is closed, not when the garbage collector gets to it.

### 6.2 `Column` (`stuntgres/_column.py`)

```python
class Column(Sequence[Any]):
    def __init__(self, result: PGresult, index: int, encoding: str = "utf-8") -> None: ...
    @property
    def name(self) -> str: ...
    @property
    def type_code(self) -> int: ...
    @property
    def display_size(self) -> int | None: ...
    @property
    def internal_size(self) -> int | None: ...
    @property
    def precision(self) -> int | None: ...
    @property
    def scale(self) -> int | None: ...
    @property
    def null_ok(self) -> None: ...
```

One entry of `description`: an object with seven properties that is also the 7-item
sequence PEP 249 asks for (`len(column) == 7`, `column[0]` is the name, slices work). The
constructor copies `fname(index)`, `ftype(index)`, `fmod(index)` and `fsize(index)` out
of the result (PQ 5.5), so a `Column` stays valid after the result is released.

| Item | Property | Value |
|---|---|---|
| 0 | `name` | `fname` decoded with the client encoding's codec; `f"column_{index + 1}"` when the result has no name for the column. |
| 1 | `type_code` | `ftype`: the type OID. The PEP 249 type objects compare equal to it: `column.type_code == stuntgres.NUMBER` (ADAPT 7). |
| 2 | `display_size` | For `bpchar` (1042) and `varchar` (1043) with `fmod >= 4`: `fmod - 4`, the declared length. For `bit` (1560) and `varbit` (1562) with `fmod >= 0`: `fmod`. Else `None`. |
| 3 | `internal_size` | `fsize` when it is greater than 0 (a fixed-size type), else `None`. |
| 4 | `precision` | `numeric` (1700) with `fmod >= 4`: `((fmod - 4) >> 16) & 0xFFFF`. `time` (1083), `timetz` (1266), `timestamp` (1114), `timestamptz` (1184) with `fmod >= 0`: `fmod & 0xFFFF`, the fractional second digits. `interval` (1186) with `fmod >= 0`: `fmod & 0xFFFF`, or `None` when that is `0xFFFF` (no precision given). Else `None`. |
| 5 | `scale` | `numeric` with `fmod >= 4`: `(((fmod - 4) & 0x7FF) ^ 0x400) - 0x400` (11 bits, sign-extended: PostgreSQL allows a negative scale). Else `None`. |
| 6 | `null_ok` | Always `None`: a result does not say whether a column can be null. |

`repr(column)` is `<Column 'name', type OID 23>`. Array types have no derived values
(their `fmod` is the element's).

**(measured, experiment 4)** `fmod` and `fsize` as PostgreSQL 18.6 reports them, and the
four derived items as the code above computes them:

| Expression or column | `name` | `type_code` | `fmod` | `fsize` | `display_size` | `internal_size` | `precision` | `scale` | Server mode |
|---|---|---|---|---|---|---|---|---|---|
| `true` | `?column?` | 16 | -1 | 1 | None | 1 | None | None | same |
| `1::int2` | `int2` | 21 | -1 | 2 | None | 2 | None | None | same |
| `1::int4` | `int4` | 23 | -1 | 4 | None | 4 | None | None | same |
| `1::int8` | `int8` | 20 | -1 | 8 | None | 8 | None | None | same |
| `1::oid` | `oid` | 26 | -1 | 4 | None | 4 | None | None | same |
| `1.5::float4` | `float4` | 700 | -1 | 4 | None | 4 | None | None | same |
| `1.5::float8` | `float8` | 701 | -1 | 8 | None | 8 | None | None | same |
| `1.5::numeric` | `numeric` | 1700 | -1 | -1 | None | None | None | None | same |
| `1.5::numeric(10,2)` | `numeric` | 1700 | 655366 | -1 | None | None | 10 | 2 | same |
| `1.5::numeric(5)` | `numeric` | 1700 | 327684 | -1 | None | None | 5 | 0 | same |
| `1.5::numeric(1000,500)` | `numeric` | 1700 | 65536504 | -1 | None | None | 1000 | 500 | same |
| `1.5::numeric(3,-2)` | `numeric` | 1700 | 198658 | -1 | None | None | 3 | -2 | same |
| `'a'::text` | `text` | 25 | -1 | -1 | None | None | None | None | same |
| `'a'::varchar` | `varchar` | 1043 | -1 | -1 | None | None | None | None | same |
| `'a'::varchar(10)` | `varchar` | 1043 | 14 | -1 | 10 | None | None | None | same |
| `'a'::char` | `bpchar` | 1042 | 5 | -1 | 1 | None | None | None | same |
| `'a'::char(5)` | `bpchar` | 1042 | 9 | -1 | 5 | None | None | None | same |
| `'a'::bpchar` | `bpchar` | 1042 | -1 | -1 | None | None | None | None | same |
| `'a'::name` | `name` | 19 | -1 | 64 | None | 64 | None | None | same |
| `'a'::"char"` | `char` | 18 | -1 | 1 | None | 1 | None | None | same |
| `'\x00'::bytea` | `bytea` | 17 | -1 | -1 | None | None | None | None | same |
| `'2020-01-02'::date` | `date` | 1082 | -1 | 4 | None | 4 | None | None | same |
| `'10:00'::time` | `time` | 1083 | -1 | 8 | None | 8 | None | None | same |
| `'10:00'::time(3)` | `time` | 1083 | 3 | 8 | None | 8 | 3 | None | same |
| `'10:00+01'::timetz` | `timetz` | 1266 | -1 | 12 | None | 12 | None | None | same |
| `'10:00+01'::timetz(0)` | `timetz` | 1266 | 0 | 12 | None | 12 | 0 | None | same |
| `'2020-01-02 10:00'::timestamp` | `timestamp` | 1114 | -1 | 8 | None | 8 | None | None | same |
| `'2020-01-02 10:00'::timestamp(6)` | `timestamp` | 1114 | 6 | 8 | None | 8 | 6 | None | same |
| `'2020-01-02 10:00'::timestamp(2)` | `timestamp` | 1114 | 2 | 8 | None | 8 | 2 | None | same |
| `'2020-01-02 10:00+00'::timestamptz` | `timestamptz` | 1184 | -1 | 8 | None | 8 | None | None | same |
| `'2020-01-02 10:00+00'::timestamptz(4)` | `timestamptz` | 1184 | 4 | 8 | None | 8 | 4 | None | same |
| `'1 day'::interval` | `interval` | 1186 | -1 | 16 | None | 16 | None | None | same |
| `'1 day'::interval(3)` | `interval` | 1186 | 2147418115 | 16 | None | 16 | 3 | None | same |
| `'1 day'::interval day to second` | `interval` | 1186 | 470351871 | 16 | None | 16 | None | None | same |
| `'1 day'::interval second(2)` | `interval` | 1186 | 268435458 | 16 | None | 16 | 2 | None | same |
| `'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11'::uuid` | `uuid` | 2950 | -1 | 16 | None | 16 | None | None | same |
| `'{}'::json` | `json` | 114 | -1 | -1 | None | None | None | None | same |
| `'{}'::jsonb` | `jsonb` | 3802 | -1 | -1 | None | None | None | None | same |
| `'{1}'::int4[]` | `int4` | 1007 | -1 | -1 | None | None | None | None | name `_int4` |
| `'{a}'::text[]` | `text` | 1009 | -1 | -1 | None | None | None | None | name `_text` |
| `'{a}'::varchar(10)[]` | `varchar` | 1015 | 14 | -1 | None | None | None | None | name `_varchar` |
| `'{1.5}'::numeric(10,2)[]` | `numeric` | 1231 | 655366 | -1 | None | None | None | None | name `_numeric` |
| `'101'::bit(3)` | `bit` | 1560 | 3 | -1 | 3 | None | None | None | same |
| `'101'::varbit(8)` | `varbit` | 1562 | 8 | -1 | 8 | None | None | None | same |
| `'101'::varbit` | `varbit` | 1562 | -1 | -1 | None | None | None | None | same |
| `'(1,2)'::point` | `point` | 600 | -1 | 16 | None | 16 | None | None | same |
| `'1.2.3.4'::inet` | `inet` | 869 | -1 | -1 | None | None | None | None | same |
| `null` | `?column?` | 25 | -1 | -1 | None | None | None | None | same |
| `'x'` | `?column?` | 25 | -1 | -1 | None | None | None | None | same |
| `$1` | `?column?` | 25 | -1 | -1 | None | None | None | None | same |
| `cols.b` | `b` | 16 | -1 | 1 | None | 1 | None | None | same |
| `cols.i2` | `i2` | 21 | -1 | 2 | None | 2 | None | None | same |
| `cols.i4` | `i4` | 23 | -1 | 4 | None | 4 | None | None | same |
| `cols.i8` | `i8` | 20 | -1 | 8 | None | 8 | None | None | same |
| `cols.f4` | `f4` | 700 | -1 | 4 | None | 4 | None | None | same |
| `cols.f8` | `f8` | 701 | -1 | 8 | None | 8 | None | None | same |
| `cols.n` | `n` | 1700 | -1 | -1 | None | None | None | None | same |
| `cols.n102` | `n102` | 1700 | 655366 | -1 | None | None | 10 | 2 | same |
| `cols.t` | `t` | 25 | -1 | -1 | None | None | None | None | same |
| `cols.vc` | `vc` | 1043 | -1 | -1 | None | None | None | None | same |
| `cols.vc10` | `vc10` | 1043 | 14 | -1 | 10 | None | None | None | same |
| `cols.c5` | `c5` | 1042 | 9 | -1 | 5 | None | None | None | same |
| `cols.by` | `by` | 17 | -1 | -1 | None | None | None | None | same |
| `cols.d` | `d` | 1082 | -1 | 4 | None | 4 | None | None | same |
| `cols.tm` | `tm` | 1083 | -1 | 8 | None | 8 | None | None | same |
| `cols.tm3` | `tm3` | 1083 | 3 | 8 | None | 8 | 3 | None | same |
| `cols.tz` | `tz` | 1266 | -1 | 12 | None | 12 | None | None | same |
| `cols.ts` | `ts` | 1114 | -1 | 8 | None | 8 | None | None | same |
| `cols.ts2` | `ts2` | 1114 | 2 | 8 | None | 8 | 2 | None | same |
| `cols.tstz` | `tstz` | 1184 | -1 | 8 | None | 8 | None | None | same |
| `cols.iv` | `iv` | 1186 | -1 | 16 | None | 16 | None | None | same |
| `cols.iv3` | `iv3` | 1186 | 2147418115 | 16 | None | 16 | 3 | None | same |
| `cols.u` | `u` | 2950 | -1 | 16 | None | 16 | None | None | same |
| `cols.j` | `j` | 114 | -1 | -1 | None | None | None | None | same |
| `cols.jb` | `jb` | 3802 | -1 | -1 | None | None | None | None | same |
| `cols.ia` | `ia` | 1007 | -1 | -1 | None | None | None | None | same |
| `cols.va` | `va` | 1015 | 14 | -1 | None | None | None | None | same |
| `cols.pk` | `pk` | 23 | -1 | 4 | None | 4 | None | None | same |

Table columns report the same `fmod` and `fsize` as the casts above (`varchar(10)`: 14;
`numeric(10,2)`: 655366; `timestamp(2)`: 2; `interval(3)`: 2147418115), and an expression
over a column loses the modifier (`n102 * 2`: -1). The one server-mode difference is the
column *name* of a cast to an array type (1.1).

The complete file:

```python
"""The column objects of Cursor.description."""
from __future__ import annotations

from typing import TYPE_CHECKING, Any, Sequence, overload

if TYPE_CHECKING:
    from .pq import PGresult

_BPCHAR, _VARCHAR, _BIT, _VARBIT = 1042, 1043, 1560, 1562
_NUMERIC, _INTERVAL = 1700, 1186
_TIMES = (1083, 1266, 1114, 1184)


class Column(Sequence[Any]):
    """One column of a result: a PEP 249 seven-item sequence with attributes."""

    __module__ = "stuntgres"

    def __init__(self, result: PGresult, index: int, encoding: str = "utf-8") -> None:
        name = result.fname(index)
        self._name = name.decode(encoding) if name else f"column_{index + 1}"
        self._ftype = result.ftype(index)
        self._fmod = result.fmod(index)
        self._fsize = result.fsize(index)

    def _items(self) -> tuple[Any, ...]:
        return (
            self.name, self.type_code, self.display_size, self.internal_size,
            self.precision, self.scale, self.null_ok,
        )

    def __len__(self) -> int:
        return 7

    @overload
    def __getitem__(self, index: int) -> Any: ...

    @overload
    def __getitem__(self, index: slice) -> tuple[Any, ...]: ...

    def __getitem__(self, index: int | slice) -> Any:
        return self._items()[index]

    def __repr__(self) -> str:
        return f"<Column {self._name!r}, type OID {self._ftype}>"

    @property
    def name(self) -> str:
        """The name of the column."""
        return self._name

    @property
    def type_code(self) -> int:
        """The OID of the column's type."""
        return self._ftype

    @property
    def display_size(self) -> int | None:
        """The declared length of char(n), varchar(n), bit(n) and varbit(n)."""
        if self._ftype in (_BPCHAR, _VARCHAR) and self._fmod >= 4:
            return self._fmod - 4
        if self._ftype in (_BIT, _VARBIT) and self._fmod >= 0:
            return self._fmod
        return None

    @property
    def internal_size(self) -> int | None:
        """The size in bytes of a fixed-size type, None for a variable one."""
        return self._fsize if self._fsize > 0 else None

    @property
    def precision(self) -> int | None:
        """Digits of numeric(p, s); fractional second digits of the time types."""
        if self._ftype == _NUMERIC and self._fmod >= 4:
            return ((self._fmod - 4) >> 16) & 0xFFFF
        if self._ftype in _TIMES and self._fmod >= 0:
            return self._fmod & 0xFFFF
        if self._ftype == _INTERVAL and self._fmod >= 0:
            digits = self._fmod & 0xFFFF
            return None if digits == 0xFFFF else digits
        return None

    @property
    def scale(self) -> int | None:
        """The scale of numeric(p, s); may be negative."""
        if self._ftype == _NUMERIC and self._fmod >= 4:
            return (((self._fmod - 4) & 0x7FF) ^ 0x400) - 0x400
        return None

    @property
    def null_ok(self) -> None:
        """Always None: a result does not tell whether a column is nullable."""
        return None
```

### 6.3 The result check (`stuntgres/_cur_results.py`)

```python
def check_results(pgconn: PGconn, results: list[PGresult]) -> PQGen[None]: ...
def end_copy(pgconn: PGconn, status: int) -> PQGen[None]: ...
```

`check_results` is called by `execute` and `executemany` with everything
`generators.execute` collected for one statement string. It returns when every result is
a finished statement and raises otherwise, after releasing the results.

`check_results(pgconn, results)`:

1. `error = None`. If `results` is empty: `error = InternalError("the statement returned
   no result")`.
2. For each result in order, by `status`: `TUPLES_OK`, `COMMAND_OK`, `EMPTY_QUERY`:
   next result. `FATAL_ERROR`, `NONFATAL_ERROR`, `BAD_RESPONSE`: `error =
   error_from_result(result, pgconn._encoding)` (3.5), stop. `COPY_IN`, `COPY_OUT`,
   `COPY_BOTH`: `yield from end_copy(pgconn, status)`, `error = ProgrammingError("COPY
   cannot be run with execute() or executemany()")`, stop. Anything else: `error =
   InternalError(f"unexpected result status {name}")`, stop.
3. If `error` is set: call `clear()` on every result of the list, then raise it.

`end_copy(pgconn, status)` leaves a COPY that the statement started, so that the session
can take commands again (a session left inside COPY refuses everything with `another
command is already in progress`):

1. While `status` is a COPY status:
   1. `COPY_IN`: `while pgconn.put_copy_end(b"COPY is not supported by execute()") == 0:
      yield Wait.W` (PQ 5.12: 0 means "would block"); then `yield from
      generators.send(pgconn)`. The server aborts the COPY; its error result is collected
      in step 1.4 and dropped.
   2. `COPY_OUT`: loop `count, _ = pgconn.get_copy_data(1)`; `count < 0`: the COPY is
      over, leave the loop; `count == 0`: `ready = yield Wait.R` and, when `ready` is not
      0, `pgconn.consume_input()`; `count > 0`: a row, dropped.
   3. `COPY_BOTH` (replication, never produced by SQL a user sends here):
      `pgconn.finish()` and return.
   4. `more = yield from generators.fetch_many(pgconn)` (PQ 7.5; it stops again at the
      next COPY); `status = more[-1].status` if `more` else `COMMAND_OK`; clear every
      result of `more`.

**(measured, both modes, `tests/test_cur_results.py`)** After `copy (select 1) to
stdout`, after `copy t from stdin`, after a `COPY ... TO STDOUT` of 100000 rows and
after `select 1; copy (select 1) to stdout; copy (select 2) to stdout; select 3`, the
check raises the `ProgrammingError`, every result is cleared, the transaction status is
`IDLE` and `select 5` works; the table of the abandoned `COPY FROM` is empty. An error in
the middle of `select 1; select 1/0; select 2` gives two results (the server stops at
the error), raises `DivisionByZero` and clears both.

The complete file:

```python
"""Checking the results of a statement before a cursor shows them."""
from __future__ import annotations

from typing import TYPE_CHECKING

from . import errors as e
from . import generators
from ._diagnostic import error_from_result
from ._gentypes import PQGen, Wait
from .pq import ExecStatus

if TYPE_CHECKING:
    from .pq import PGconn, PGresult

_DONE = (ExecStatus.TUPLES_OK, ExecStatus.COMMAND_OK, ExecStatus.EMPTY_QUERY)
_FAILED = (
    ExecStatus.FATAL_ERROR, ExecStatus.NONFATAL_ERROR, ExecStatus.BAD_RESPONSE,
)
_COPY = (ExecStatus.COPY_IN, ExecStatus.COPY_OUT, ExecStatus.COPY_BOTH)


def check_results(pgconn: PGconn, results: list[PGresult]) -> PQGen[None]:
    """Raise for the first result that is no finished statement.

    Before raising, every result is released; a COPY that the statement
    started is abandoned first, so that the session can take commands again.
    """
    error: Exception | None = None
    if not results:
        error = e.InternalError("the statement returned no result")
    for result in results:
        status = result.status
        if status in _DONE:
            continue
        if status in _FAILED:
            error = error_from_result(result, pgconn._encoding)
        elif status in _COPY:
            yield from end_copy(pgconn, status)
            error = e.ProgrammingError(
                "COPY cannot be run with execute() or executemany()"
            )
        else:
            name = ExecStatus(status).name
            error = e.InternalError(f"unexpected result status {name}")
        break
    if error is not None:
        for result in results:
            result.clear()
        raise error


def end_copy(pgconn: PGconn, status: int) -> PQGen[None]:
    """Abandon the COPY the session is in and collect what follows it."""
    while status in _COPY:
        if status == ExecStatus.COPY_IN:
            while pgconn.put_copy_end(b"COPY is not supported by execute()") == 0:
                yield Wait.W
            yield from generators.send(pgconn)
        elif status == ExecStatus.COPY_OUT:
            while True:
                count, _data = pgconn.get_copy_data(1)
                if count < 0:
                    break
                if count == 0 and (yield Wait.R):
                    pgconn.consume_input()
        else:
            pgconn.finish()
            return
        more = yield from generators.fetch_many(pgconn)
        status = more[-1].status if more else ExecStatus.COMMAND_OK
        for result in more:
            result.clear()
```

### 6.4 `execute` (`stuntgres/_cur_core.py`)

```python
    def _check_usable(self) -> None: ...
    def _close(self) -> None: ...
    def _reset(self) -> None: ...
    def _convert(
        self, tx: Transformer, query: Query, params: Params | None
    ) -> tuple[bytes, list[bytes | None] | None, list[int] | None]: ...
    def _send(
        self,
        sql: bytes,
        dumped: Sequence[bytes | None] | None,
        oids: Sequence[int] | None,
    ) -> None: ...
    def _execute_gen(self, query: Query, params: Params | None = None) -> PQGen[None]: ...
    def _set_results(self, results: list[PGresult], tx: Transformer) -> None: ...
    def _select_result(self, index: int) -> None: ...
    def _set_row_maker(self) -> None: ...
```

`Query` and `Params` are the aliases of `stuntgres._queries` (ADAPT 2.1).

| Member | Algorithm |
|---|---|
| `_check_usable()` | If `_closed`: raise `InterfaceError("the cursor is closed")`. Then `self._conn._check_open()` (4.3). |
| `_close()` | `_closed = True`; `_reset()`. Idempotent. |
| `_convert(tx, query, params)` | `sql, values = _queries.prepare(query, params, tx.encoding)` (ADAPT 2.3). If `values is None` (no parameters were given): return `(sql, None, None)`. Else `dumped, oids = tx.dump_sequence(values)` (ADAPT 3.5) and return `(sql, dumped, oids)`. Sends nothing; raises what ADAPT 2.5 and 3.2 raise. |
| `_send(sql, dumped, oids)` | `dumped is None`: `pgconn.send_query(sql)` (several statements allowed). Else `pgconn.send_query_params(sql, dumped, oids)` (PQ 5.7; text format; one statement). |
| `_execute_gen(query, params=None)` | 1. `_check_usable()`. 2. `_reset()`. 3. `tx = Transformer(self)` (a new one per call, ADAPT fixed point A7). 4. `sql, dumped, oids = self._convert(tx, query, params)`. 5. `yield from self._conn._start_query()` (4.6: the implicit `BEGIN`). 6. `self._send(sql, dumped, oids)`. 7. `results = yield from generators.execute(pgconn)`. 8. `yield from check_results(pgconn, results)` (6.3). 9. `self._set_results(results, tx)`. |
| `_set_results(results, tx)` | `_results = results`; `_tx = tx`; `_select_result(0)`. |
| `_select_result(index)` | `result = _results[index]`; `_iresult = index`; `pgresult = result`; `_description = None`; `_pos = 0`; `_tx.set_pgresult(result)` (ADAPT 3.5: one loader per column); `count = result.command_tuples`; `_make_row = None`; if `result.status == TUPLES_OK`: when `count is None`, `count = result.ntuples`; then `_set_row_maker()`. Finally `_rowcount = -1 if count is None else count`. |
| `_set_row_maker()` | `maker = self._row_factory(self)`; `_make_row = None if maker is tuple else maker`. |
| `row_factory` (set) | Store it; if the current result exists and its status is `TUPLES_OK`: `_set_row_maker()`. |
| `description` | `None` when there is no current result or its status is not `TUPLES_OK`. Else, built once per result and cached: `[Column(result, i, encoding) for i in range(result.nfields)]`, `encoding = self._tx.encoding`. A query that returns no rows has a description; a `select` without columns has `[]`. |
| `rowcount` | `_rowcount`. |
| `statusmessage` | The command tag of the current result (`result.command_status`) decoded as ASCII; `None` without a result or for an empty tag (an empty query). |
| `__repr__()` | `<stuntgres.Cursor [state] at 0x...>` with `state` = `closed`, `no result`, or the name of the current result's status (`TUPLES_OK`, `COMMAND_OK`, `EMPTY_QUERY`). |

Order matters in `_execute_gen`: conversion and dumping happen **before** the `BEGIN`,
so a call that fails in its own arguments sends nothing and opens no transaction; the
previous result is released **before** anything is sent; an error at any step leaves the
cursor without result (`pgresult is None`, `description is None`, `rowcount == -1`).

**`rowcount` (experiment 3).** The rule of `_select_result`, applied to what PostgreSQL
18.6 answers:

| Statement | Results: status, command tag, `command_tuples`, `ntuples` | `rowcount` | Server mode |
|---|---|---|---|
| `create table t (id int primary key, v text)` | COMMAND_OK, `CREATE TABLE`, None, 0 | -1 | same |
| `insert into t values (1, 'a')` | COMMAND_OK, `INSERT 0 1`, 1, 0 | 1 | same |
| `insert into t values (2, 'b'), (3, 'c'), (4, 'd')` | COMMAND_OK, `INSERT 0 3`, 3, 0 | 3 | same |
| `insert into t select g, 'g' from generate_series(5, 6) g` | COMMAND_OK, `INSERT 0 2`, 2, 0 | 2 | same |
| `insert into t values ($1, $2) [b'9', b'nine']` | COMMAND_OK, `INSERT 0 1`, 1, 0 | 1 | same |
| `insert into t values (10, 'x') returning id, v` | TUPLES_OK, `INSERT 0 1`, 1, 1 | 1 | same |
| `insert into t values (1, 'dup') on conflict do nothing` | COMMAND_OK, `INSERT 0 0`, 0, 0 | 0 | same |
| `select * from t where id < 3` | TUPLES_OK, `SELECT 2`, 2, 2 | 2 | same |
| `select * from t where id = $1 [b'2']` | TUPLES_OK, `SELECT 1`, 1, 1 | 1 | same |
| `select * from t where false` | TUPLES_OK, `SELECT 0`, 0, 0 | 0 | same |
| `select 1 where false` | TUPLES_OK, `SELECT 0`, 0, 0 | 0 | same |
| `select` | TUPLES_OK, `SELECT 1`, 1, 1 | 1 | same |
| `values (1), (2)` | TUPLES_OK, `SELECT 2`, 2, 2 | 2 | same |
| `table t` | TUPLES_OK, `SELECT 8`, 8, 8 | 8 | FATAL_ERROR `42601` syntax error at or near "table" |
| `update t set v = 'z' where id > 4` | COMMAND_OK, `UPDATE 4`, 4, 0 | 4 | same |
| `update t set v = 'z' where false` | COMMAND_OK, `UPDATE 0`, 0, 0 | 0 | same |
| `update t set v = 'y' where id = 1 returning v` | TUPLES_OK, `UPDATE 1`, 1, 1 | 1 | same |
| `delete from t where id = 10` | COMMAND_OK, `DELETE 1`, 1, 0 | 1 | same |
| `delete from t where id in (5, 6) returning id` | TUPLES_OK, `DELETE 2`, 2, 2 | 2 | same |
| `merge into t using (values (1, 'm'), (100, 'n')) s(id, v) on t.id = s.id when matched then update set v = s.v when not matched then insert values (s.id, s.v)` | COMMAND_OK, `MERGE 2`, 2, 0 | 2 | same |
| `merge into t using (values (1, 'q')) s(id, v) on t.id = s.id when matched then delete returning merge_action(), t.id` | TUPLES_OK, `MERGE 1`, 1, 1 | 1 | same |
| `copy t from stdin` | COPY_IN; COMMAND_OK, `COPY 2`, 2, 0 | refused (6.3) | same |
| `copy t to stdout` | COPY_OUT; COMMAND_OK, `COPY 7`, 7, 0 | refused (6.3) | same |
| `copy (select 1) to stdout` | COPY_OUT; COMMAND_OK, `COPY 1`, 1, 0 | refused (6.3) | same |
| `begin` | COMMAND_OK, `BEGIN`, None, 0 | -1 | same |
| `declare cur cursor for select * from t order by id` | COMMAND_OK, `DECLARE CURSOR`, None, 0 | -1 | same |
| `move 3 in cur` | COMMAND_OK, `MOVE 3`, 3, 0 | 3 | same |
| `fetch 2 from cur` | TUPLES_OK, `FETCH 2`, 2, 2 | 2 | same |
| `fetch all from cur` | TUPLES_OK, `FETCH 2`, 2, 2 | 2 | same |
| `fetch 1 from cur` | TUPLES_OK, `FETCH 0`, 0, 0 | 0 | same |
| `move all in cur` | COMMAND_OK, `MOVE 0`, 0, 0 | 0 | same |
| `close cur` | COMMAND_OK, `CLOSE CURSOR`, None, 0 | -1 | same |
| `commit` | COMMAND_OK, `COMMIT`, None, 0 | -1 | same |
| `begin` | COMMAND_OK, `BEGIN`, None, 0 | -1 | same |
| `declare cur2 cursor for select * from t order by id` | COMMAND_OK, `DECLARE CURSOR`, None, 0 | -1 | same |
| `fetch all from cur2` | TUPLES_OK, `FETCH 7`, 7, 7 | 7 | same |
| `move absolute 0 in cur2` | COMMAND_OK, `MOVE 0`, 0, 0 | 0 | FATAL_ERROR `55000` cursor can only scan forward |
| `rollback` | COMMAND_OK, `ROLLBACK`, None, 0 | -1 | same |
| `create table t2 as select * from t` | COMMAND_OK, `SELECT 7`, 7, 0 | 7 | same |
| `select * into t3 from t where id < 4` | COMMAND_OK, `SELECT 2`, 2, 0 | 2 | same |
| `create table t4 as select * from t with no data` | COMMAND_OK, `CREATE TABLE AS`, None, 0 | -1 | same |
| `create materialized view mv as select * from t` | COMMAND_OK, `SELECT 7`, 7, 0 | 7 | same |
| `refresh materialized view mv` | COMMAND_OK, `REFRESH MATERIALIZED VIEW`, None, 0 | -1 | same |
| `create index on t (v)` | COMMAND_OK, `CREATE INDEX`, None, 0 | -1 | same |
| `alter table t add column w int` | COMMAND_OK, `ALTER TABLE`, None, 0 | -1 | same |
| `drop table t2` | COMMAND_OK, `DROP TABLE`, None, 0 | -1 | same |
| `create view vw as select 1` | COMMAND_OK, `CREATE VIEW`, None, 0 | -1 | same |
| `truncate t3` | COMMAND_OK, `TRUNCATE TABLE`, None, 0 | -1 | same |
| `set search_path to public` | COMMAND_OK, `SET`, None, 0 | -1 | same |
| `show search_path` | TUPLES_OK, `SHOW`, None, 1 | 1 | same |
| `reset search_path` | COMMAND_OK, `RESET`, None, 0 | -1 | same |
| `explain select 1` | TUPLES_OK, `EXPLAIN`, None, 1 | 1 | TUPLES_OK tag='EXPLAIN' cmdtuples=None ntuples=3 |
| `prepare ps as select $1::int` | COMMAND_OK, `PREPARE`, None, 0 | -1 | same |
| `execute ps (5)` | TUPLES_OK, `SELECT 1`, 1, 1 | 1 | same |
| `deallocate ps` | COMMAND_OK, `DEALLOCATE`, None, 0 | -1 | same |
| `listen ch` | COMMAND_OK, `LISTEN`, None, 0 | -1 | same |
| `notify ch` | COMMAND_OK, `NOTIFY`, None, 0 | -1 | same |
| `unlisten ch` | COMMAND_OK, `UNLISTEN`, None, 0 | -1 | same |
| `do $$ begin null; end $$` | COMMAND_OK, `DO`, None, 0 | -1 | FATAL_ERROR `42601` syntax error at or near "null" |
| `create function f() returns int language sql as 'select 7'` | COMMAND_OK, `CREATE FUNCTION`, None, 0 | -1 | same |
| `select f()` | TUPLES_OK, `SELECT 1`, 1, 1 | 1 | same |
| `create procedure pr(inout x int) language plpgsql as $$ begin x := x + 1; end $$` | COMMAND_OK, `CREATE PROCEDURE`, None, 0 | -1 | same |
| `call pr(1)` | TUPLES_OK, `CALL`, None, 1 | 1 | same |
| `lock table t in share mode` | FATAL_ERROR `25P01` LOCK TABLE can only be used in transaction blocks | error raised | same |
| `analyze t` | COMMAND_OK, `ANALYZE`, None, 0 | -1 | same |
| `comment on table t is 'c'` | COMMAND_OK, `COMMENT`, None, 0 | -1 | same |
| `grant select on t to public` | COMMAND_OK, `GRANT`, None, 0 | -1 | same |
| `discard all` | COMMAND_OK, `DISCARD ALL`, None, 0 | -1 | same |
| `checkpoint` | COMMAND_OK, `CHECKPOINT`, None, 0 | -1 | FATAL_ERROR `0A000` CHECKPOINT physical WAL and fsync operations are unsupported in embedded mode |
| (empty string) | EMPTY_QUERY, no tag, None, 0 | -1 | same |
| `;` | EMPTY_QUERY, no tag, None, 0 | -1 | same |
| `select 1/0` | FATAL_ERROR `22012` division by zero | error raised | same |

So: `SELECT`, `FETCH`, DML with or without `RETURNING`, `MERGE`, `MOVE`, `CREATE TABLE
AS` and `SELECT INTO` carry a count in their tag; `SHOW`, `EXPLAIN` and `CALL` return
rows without a count and get the number of rows; everything else is -1, including an
empty query (which has no tag: `statusmessage` is `None`). After `executemany` the
attribute is a sum (6.7). The server-mode differences in this table are all in statements that no
designed test sends (section 12).

The complete file:

```python
"""State of a cursor and the execution of one statement."""
from __future__ import annotations

from typing import TYPE_CHECKING, Any, Sequence

from . import _queries
from . import errors as e
from . import generators
from ._column import Column
from ._cur_results import check_results
from ._gentypes import PQGen
from ._queries import Params, Query
from ._transformer import Transformer
from .adapt import AdaptersMap
from .pq import ExecStatus
from .rows import RowFactory, RowMaker

if TYPE_CHECKING:
    from .pq import PGresult


class CurCore:
    """Everything a cursor remembers, and execute(); the mixins add the rest."""

    def __init__(
        self, connection: Any, *, row_factory: RowFactory | None = None
    ) -> None:
        self._conn = connection
        self._adapters = AdaptersMap(connection.adapters)
        self._row_factory: RowFactory = row_factory or connection.row_factory
        self.arraysize = 1
        self._closed = False
        self._results: list[PGresult] = []
        self._reset()

    def __repr__(self) -> str:
        cls = type(self)
        if self._closed:
            state = "closed"
        elif self.pgresult is None:
            state = "no result"
        else:
            state = ExecStatus(self.pgresult.status).name
        return f"<{cls.__module__}.{cls.__qualname__} [{state}] at 0x{id(self):x}>"

    def _reset(self) -> None:
        """Forget the results of the previous statement and release them."""
        for result in self._results:
            result.clear()
        self._results = []
        self._iresult = 0
        self.pgresult: PGresult | None = None
        self._tx: Transformer | None = None
        self._make_row: RowMaker | None = None
        self._description: list[Column] | None = None
        self._pos = 0
        self._rowcount = -1

    @property
    def connection(self) -> Any:
        """The connection the cursor belongs to."""
        return self._conn

    @property
    def adapters(self) -> AdaptersMap:
        """The adapters of this cursor, in effect from its next execute."""
        return self._adapters

    @property
    def closed(self) -> bool:
        """True after close()."""
        return self._closed

    @property
    def row_factory(self) -> RowFactory:
        """The row factory; assigning it also changes the rows not yet fetched."""
        return self._row_factory

    @row_factory.setter
    def row_factory(self, value: RowFactory) -> None:
        self._row_factory = value
        result = self.pgresult
        if result is not None and result.status == ExecStatus.TUPLES_OK:
            self._set_row_maker()

    @property
    def description(self) -> list[Column] | None:
        """The columns of the current result; None when it has no rows to give."""
        result = self.pgresult
        if result is None or result.status != ExecStatus.TUPLES_OK:
            return None
        if self._description is None:
            encoding = self._tx.encoding if self._tx is not None else "utf-8"
            self._description = [
                Column(result, index, encoding) for index in range(result.nfields)
            ]
        return self._description

    @property
    def rowcount(self) -> int:
        """Rows the last statement returned or affected; -1 when unknown."""
        return self._rowcount

    @property
    def statusmessage(self) -> str | None:
        """The command tag of the current result, for example 'INSERT 0 1'."""
        result = self.pgresult
        tag = result.command_status if result is not None else None
        return tag.decode("ascii", "replace") if tag else None

    def _check_usable(self) -> None:
        if self._closed:
            raise e.InterfaceError("the cursor is closed")
        self._conn._check_open()

    def _close(self) -> None:
        self._closed = True
        self._reset()

    def _convert(
        self, tx: Transformer, query: Query, params: Params | None
    ) -> tuple[bytes, list[bytes | None] | None, list[int] | None]:
        """Convert the placeholders and dump the parameters; sends nothing."""
        sql, values = _queries.prepare(query, params, tx.encoding)
        if values is None:
            return sql, None, None
        dumped, oids = tx.dump_sequence(values)
        return sql, dumped, oids

    def _send(
        self,
        sql: bytes,
        dumped: Sequence[bytes | None] | None,
        oids: Sequence[int] | None,
    ) -> None:
        pgconn = self._conn.pgconn
        if dumped is None:
            pgconn.send_query(sql)
        else:
            pgconn.send_query_params(sql, dumped, oids)

    def _execute_gen(self, query: Query, params: Params | None = None) -> PQGen[None]:
        self._check_usable()
        self._reset()
        tx = Transformer(self)  # type: ignore[arg-type]
        sql, dumped, oids = self._convert(tx, query, params)
        yield from self._conn._start_query()
        self._send(sql, dumped, oids)
        results = yield from generators.execute(self._conn.pgconn)
        yield from check_results(self._conn.pgconn, results)
        self._set_results(results, tx)

    def _set_results(self, results: list[PGresult], tx: Transformer) -> None:
        self._results = results
        self._tx = tx
        self._select_result(0)

    def _select_result(self, index: int) -> None:
        """Make one of the results the current one."""
        result = self._results[index]
        self._iresult = index
        self.pgresult = result
        self._description = None
        self._pos = 0
        assert self._tx is not None
        self._tx.set_pgresult(result)
        count = result.command_tuples
        self._make_row = None
        if result.status == ExecStatus.TUPLES_OK:
            if count is None:
                count = result.ntuples
            self._set_row_maker()
        self._rowcount = -1 if count is None else count

    def _set_row_maker(self) -> None:
        maker = self._row_factory(self)
        self._make_row = None if maker is tuple else maker
```

### 6.5 Fetching (`stuntgres/_cur_fetch.py`)

```python
class FetchMixin(CurCore):
    @property
    def rownumber(self) -> int | None: ...
    def _check_fetch(self) -> int: ...
    def _fetchone(self) -> Any: ...
    def _fetchmany(self, size: int | None = None) -> list[Any]: ...
    def _fetchall(self) -> list[Any]: ...
    def _scroll(self, value: int, mode: str = "relative") -> None: ...
```

The whole result is on the client (PQ 5.5), so fetching is reading: no I/O, no lock, no
generator. Rows are converted when they are fetched, a slice per call, through
`Transformer.load_rows` (ADAPT 3.5, 3.7).

| Member | Algorithm |
|---|---|
| `_check_fetch()` | If `_closed`: `InterfaceError("the cursor is closed")`. If `pgresult is None`: `ProgrammingError("no result available: execute a statement first")`. If its status is not `TUPLES_OK`: `ProgrammingError("the last statement returned no rows")`. Return `pgresult.ntuples`. |
| `rownumber` | `None` without a current result or when it is not `TUPLES_OK`; else `_pos` (PEP 249 extension: the 0-based index of the next row). |
| `_fetchone()` | `_check_fetch()`; `row = _tx.load_row(_pos, _make_row)`; if it is not `None`: `_pos += 1`; return `row` (`None` at the end). |
| `_fetchmany(size=None)` | `ntuples = _check_fetch()`; `size = arraysize` when `None`; `last = min(_pos + max(size, 0), ntuples)`; `rows = _tx.load_rows(_pos, last, _make_row)`; `_pos += len(rows)`; return `rows` (an empty list at the end, and for a size of 0 or less). |
| `_fetchall()` | `ntuples = _check_fetch()`; `rows = _tx.load_rows(_pos, ntuples, _make_row)`; `_pos = ntuples`; return `rows`. |
| `_scroll(value, mode="relative")` | `ntuples = _check_fetch()`; `target = _pos + value` for `"relative"`, `value` for `"absolute"`, else `ValueError(f"bad mode {mode!r}: use 'relative' or 'absolute'")`; if not `0 <= target < ntuples`: `IndexError("the position is outside the result")` (PEP 249) and the position stays; else `_pos = target`. |

Rows already fetched stay valid for ever; rows of a result can be fetched after
`commit()`, `rollback()` and even after the connection was closed, until the cursor is
closed or runs another statement.

The complete file:

```python
"""Reading the rows of the current result."""
from __future__ import annotations

from typing import Any

from . import errors as e
from ._cur_core import CurCore
from .pq import ExecStatus


class FetchMixin(CurCore):
    """fetchone, fetchmany, fetchall, scroll and rownumber on the client side."""

    @property
    def rownumber(self) -> int | None:
        """Index of the next row to fetch; None when there is no row result."""
        result = self.pgresult
        if result is None or result.status != ExecStatus.TUPLES_OK:
            return None
        return self._pos

    def _check_fetch(self) -> int:
        """Raise unless rows can be fetched; return the number of rows."""
        if self._closed:
            raise e.InterfaceError("the cursor is closed")
        result = self.pgresult
        if result is None:
            raise e.ProgrammingError("no result available: execute a statement first")
        if result.status != ExecStatus.TUPLES_OK:
            raise e.ProgrammingError("the last statement returned no rows")
        return result.ntuples

    def _fetchone(self) -> Any:
        self._check_fetch()
        assert self._tx is not None
        row = self._tx.load_row(self._pos, self._make_row)
        if row is not None:
            self._pos += 1
        return row

    def _fetchmany(self, size: int | None = None) -> list[Any]:
        ntuples = self._check_fetch()
        if size is None:
            size = self.arraysize
        assert self._tx is not None
        last = min(self._pos + max(size, 0), ntuples)
        rows = self._tx.load_rows(self._pos, last, self._make_row)
        self._pos += len(rows)
        return rows

    def _fetchall(self) -> list[Any]:
        ntuples = self._check_fetch()
        assert self._tx is not None
        rows = self._tx.load_rows(self._pos, ntuples, self._make_row)
        self._pos = ntuples
        return rows

    def _scroll(self, value: int, mode: str = "relative") -> None:
        ntuples = self._check_fetch()
        if mode == "relative":
            target = self._pos + value
        elif mode == "absolute":
            target = value
        else:
            raise ValueError(f"bad mode {mode!r}: use 'relative' or 'absolute'")
        if not 0 <= target < ntuples:
            raise IndexError("the position is outside the result")
        self._pos = target
```

### 6.6 Several statements in one string (experiment 2)

`execute(query)` without parameters sends the string with the simple query protocol.
What the server does with it **(measured; the stuntgres server answered every line the
same)**:

| String sent with `send_query` | Status after | Results, in order | Notice |
|---|---|---|---|
| `create table m (id int primary key)` | IDLE | COMMAND_OK `CREATE TABLE` |  |
| **plain** | | | |
| `select 1; select 2, 3; select 'x' where false` | IDLE | TUPLES_OK `SELECT 1` rows `[(b'1',)]`; TUPLES_OK `SELECT 1` rows `[(b'2', b'3')]`; TUPLES_OK `SELECT 0` rows `[]` |  |
| `insert into m values (1); insert into m values (2), (3); select count(*) from m` | IDLE | COMMAND_OK `INSERT 0 1`; COMMAND_OK `INSERT 0 2`; TUPLES_OK `SELECT 1` rows `[(b'3',)]` |  |
| `select 1;` | IDLE | TUPLES_OK `SELECT 1` rows `[(b'1',)]` |  |
| `select 1; ; select 2` | IDLE | TUPLES_OK `SELECT 1` rows `[(b'1',)]`; TUPLES_OK `SELECT 1` rows `[(b'2',)]` |  |
| `;` | IDLE | EMPTY_QUERY |  |
| (empty string) | IDLE | EMPTY_QUERY |  |
| (two spaces) | IDLE | EMPTY_QUERY |  |
| `-- only a comment` | IDLE | EMPTY_QUERY |  |
| `select 1; -- trailing comment` | IDLE | TUPLES_OK `SELECT 1` rows `[(b'1',)]` |  |
| **an error in the first, the middle and the last statement (outside a transaction)** | | | |
| `select 1/0; insert into m values (10); select 2` | IDLE | FATAL_ERROR `22012` division by zero |  |
| `select count(*) from m` | IDLE | TUPLES_OK `SELECT 1` rows `[(b'3',)]` |  |
| `insert into m values (11); select 1/0; insert into m values (12)` | IDLE | COMMAND_OK `INSERT 0 1`; FATAL_ERROR `22012` division by zero |  |
| `select count(*) from m` | IDLE | TUPLES_OK `SELECT 1` rows `[(b'3',)]` |  |
| `insert into m values (13); select 2; select 1/0` | IDLE | COMMAND_OK `INSERT 0 1`; TUPLES_OK `SELECT 1` rows `[(b'2',)]`; FATAL_ERROR `22012` division by zero |  |
| `select count(*) from m` | IDLE | TUPLES_OK `SELECT 1` rows `[(b'3',)]` |  |
| `select 1; select from nosuch; select 3` | IDLE | TUPLES_OK `SELECT 1` rows `[(b'1',)]`; FATAL_ERROR `42P01` relation "nosuch" does not exist |  |
| `select 1; selec 2; select 3` | IDLE | FATAL_ERROR `42601` syntax error at or near "selec" |  |
| **the same inside a transaction block** | | | |
| `BEGIN` | INTRANS | COMMAND_OK `BEGIN` |  |
| `insert into m values (20); select 1/0; insert into m values (21)` | INERROR | COMMAND_OK `INSERT 0 1`; FATAL_ERROR `22012` division by zero |  |
| `select 1` | INERROR | FATAL_ERROR `25P02` current transaction is aborted, commands ignored until end of transaction block |  |
| `ROLLBACK` | IDLE | COMMAND_OK `ROLLBACK` |  |
| `select count(*) from m` | IDLE | TUPLES_OK `SELECT 1` rows `[(b'3',)]` |  |
| **explicit transaction control inside the string** | | | |
| `begin; insert into m values (30); commit; select count(*) from m` | IDLE | COMMAND_OK `BEGIN`; COMMAND_OK `INSERT 0 1`; COMMAND_OK `COMMIT`; TUPLES_OK `SELECT 1` rows `[(b'4',)]` |  |
| `begin; insert into m values (31); commit; select 1/0` | IDLE | COMMAND_OK `BEGIN`; COMMAND_OK `INSERT 0 1`; COMMAND_OK `COMMIT`; FATAL_ERROR `22012` division by zero |  |
| `select count(*) from m` | IDLE | TUPLES_OK `SELECT 1` rows `[(b'5',)]` |  |
| `insert into m values (32); commit; insert into m values (33); select 1/0` | IDLE | COMMAND_OK `INSERT 0 1`; COMMAND_OK `COMMIT`; COMMAND_OK `INSERT 0 1`; FATAL_ERROR `22012` division by zero | WARNING 25P01 there is no transaction in progress |
| `select count(*) from m` | IDLE | TUPLES_OK `SELECT 1` rows `[(b'6',)]` |  |
| `begin; insert into m values (34)` | INTRANS | COMMAND_OK `BEGIN`; COMMAND_OK `INSERT 0 1` |  |
| `select 1/0; commit` | INERROR | FATAL_ERROR `22012` division by zero |  |
| `commit` | IDLE | COMMAND_OK `ROLLBACK` |  |
| `select count(*) from m` | IDLE | TUPLES_OK `SELECT 1` rows `[(b'6',)]` |  |
| `begin; insert into m values (35); rollback; insert into m values (36)` | IDLE | COMMAND_OK `BEGIN`; COMMAND_OK `INSERT 0 1`; COMMAND_OK `ROLLBACK`; COMMAND_OK `INSERT 0 1` |  |
| `select id from m where id > 33 order by id` | IDLE | TUPLES_OK `SELECT 1` rows `[(b'36',)]` |  |
| `insert into m values (40); savepoint s` | IDLE | COMMAND_OK `INSERT 0 1`; FATAL_ERROR `25P01` SAVEPOINT can only be used in transaction blocks |  |
| `select count(*) from m where id = 40` | IDLE | TUPLES_OK `SELECT 1` rows `[(b'0',)]` |  |
| **statements that cannot run in the implicit block** | | | |
| `select 1; vacuum m` | IDLE | TUPLES_OK `SELECT 1` rows `[(b'1',)]`; FATAL_ERROR `25001` VACUUM cannot run inside a transaction block |  |
| `vacuum m` | IDLE | COMMAND_OK `VACUUM` |  |
| `select 1; create database nope_x` | IDLE | TUPLES_OK `SELECT 1` rows `[(b'1',)]`; FATAL_ERROR `25001` CREATE DATABASE cannot run inside a transaction block |  |
| **a rowless statement between two queries, DML with RETURNING** | | | |
| `select 1; set search_path to public; select 2` | IDLE | TUPLES_OK `SELECT 1` rows `[(b'1',)]`; COMMAND_OK `SET`; TUPLES_OK `SELECT 1` rows `[(b'2',)]` |  |
| `insert into m values (50) returning id; update m set id = 51 where id = 50 returning id; delete from m where id = 51 returning id` | IDLE | TUPLES_OK `INSERT 0 1` rows `[(b'50',)]`; TUPLES_OK `UPDATE 1` rows `[(b'51',)]`; TUPLES_OK `DELETE 1` rows `[(b'51',)]` |  |

What the cursor makes of it:

- Statements produce results in order; an empty statement between semicolons produces
  none; a string of only white space, comments or `;` produces one `EMPTY_QUERY` result.
- The string runs in one implicit transaction unless it contains transaction control:
  after an error everything since the start of the string (or since the last `COMMIT`
  inside it) is rolled back, and results that were already produced are still delivered
  before the error result. `execute` raises that error and releases all of them: **a
  string either gives all its results or none.**
- With `autocommit` off the driver's `BEGIN` precedes the string, so the string is part
  of the open transaction and the status afterwards is `INTRANS` or `INERROR`.
- With parameters there can be only one statement: PostgreSQL answers `42601 cannot
  insert multiple commands into a prepared statement` (`errors.SyntaxError`).
- Statements that refuse to run in a transaction block (`VACUUM`, `CREATE DATABASE`)
  fail with `25001` when they are not alone in the string, or when `autocommit` is off.

### 6.7 `executemany` (`stuntgres/_cur_many.py`)

```python
class ManyMixin(CurCore):
    def _executemany_gen(
        self, query: Query, params_seq: Iterable[Params], returning: bool = False
    ) -> PQGen[None]: ...
```

One statement, sent once per parameter set, a round trip each (libspq has no pipeline
mode; the conversion of the query is cached by ADAPT 2.4 and the dumpers by the one
`Transformer`).

1. `_check_usable()`; `_reset()`; `tx = Transformer(self)`; `kept = []`; `total = 0`.
2. For each `params` of `params_seq` (any iterable, consumed lazily), inside `try:`:
   1. `sql, dumped, oids = self._convert(tx, query, params)`.
   2. `yield from self._conn._start_query()` (sends `BEGIN` before the first set only).
   3. `self._send(sql, dumped, oids)`; `results = yield from
      generators.execute(pgconn)`; `yield from check_results(pgconn, results)`.
   4. If `returning`: `kept.extend(results)`. Else for each result: `total +=
      result.command_tuples or 0`; `result.clear()`.
3. `except BaseException:` clear every result of `kept` and re-raise.
4. If `kept` is not empty: `self._set_results(kept, tx)` (the first result becomes
   current; `rowcount`, `description` and `statusmessage` are those of the current
   result, and `nextset()` walks the others). Else `self._rowcount = total`.

Consequences **(measured, `tests/test_cursor_executemany.py`)**: without `returning`
the cursor has no result afterwards (`fetchone()` raises, `description` is `None`) and
`rowcount` is the sum over all sets (3 inserts of one row: 3; `update ... where id <= %s`
with 1, 3, 0: 4); an empty sequence sends nothing and gives `rowcount` 0; an error in a
later set raises, leaves the cursor without result and `rowcount` -1, and what the
earlier sets did stays in the open transaction (roll it back), or is already committed
on an autocommit connection; a parameter set that fails in conversion stops the loop
before it is sent.

The complete file:

```python
"""executemany: one statement, many parameter sets."""
from __future__ import annotations

from typing import TYPE_CHECKING, Iterable

from . import generators
from ._cur_core import CurCore
from ._cur_results import check_results
from ._gentypes import PQGen
from ._queries import Params, Query
from ._transformer import Transformer

if TYPE_CHECKING:
    from .pq import PGresult


class ManyMixin(CurCore):
    """Runs the statement once per parameter set, one round trip each."""

    def _executemany_gen(
        self, query: Query, params_seq: Iterable[Params], returning: bool = False
    ) -> PQGen[None]:
        self._check_usable()
        self._reset()
        tx = Transformer(self)  # type: ignore[arg-type]
        pgconn = self._conn.pgconn
        kept: list[PGresult] = []
        total = 0
        try:
            for params in params_seq:
                sql, dumped, oids = self._convert(tx, query, params)
                yield from self._conn._start_query()
                self._send(sql, dumped, oids)
                results = yield from generators.execute(pgconn)
                yield from check_results(pgconn, results)
                if returning:
                    kept.extend(results)
                    continue
                for result in results:
                    total += result.command_tuples or 0
                    result.clear()
        except BaseException:
            for result in kept:
                result.clear()
            raise
        if kept:
            self._set_results(kept, tx)
        else:
            self._rowcount = total
```

### 6.8 `nextset` (`stuntgres/_cur_nextset.py`)

```python
class NextsetMixin(CurCore):
    def nextset(self) -> bool | None: ...
```

A plain method of the base class (no I/O), the same for `Cursor` and `AsyncCursor`.

1. If `_closed`: raise `InterfaceError("the cursor is closed")`.
2. If `_iresult + 1 >= len(_results)`: return `None`; the current result stays.
3. `self._select_result(_iresult + 1)` (6.4): the next result becomes current, its rows
   start at 0, `description`, `rowcount`, `statusmessage` and the row maker are its own;
   unread rows of the previous result are skipped. Return `True`.

Results without rows are sets too: after `execute("create table m (id int); insert into
m values (1), (2); select count(*) from m")` the cursor is on the `CREATE TABLE` result
(`rowcount` -1, `description` `None`, fetching raises `the last statement returned no
rows`), `nextset()` moves to `INSERT 0 2`, the next call to the query.

The complete file:

```python
"""Moving between the results of one execute or executemany."""
from __future__ import annotations

from . import errors as e
from ._cur_core import CurCore


class NextsetMixin(CurCore):
    """nextset(): the results of a multi-statement string, one after another."""

    def nextset(self) -> bool | None:
        """Make the next result current; None when the current one is the last."""
        if self._closed:
            raise e.InterfaceError("the cursor is closed")
        if self._iresult + 1 >= len(self._results):
            return None
        self._select_result(self._iresult + 1)
        return True
```

### 6.9 Optional members (`stuntgres/_cur_optional.py`)

```python
class OptionalMixin(CurCore):
    def setinputsizes(self, sizes: Sequence[Any]) -> None: ...
    def setoutputsize(self, size: int, column: int | None = None) -> None: ...
    @property
    def lastrowid(self) -> None: ...
```

| PEP 249 member | Here |
|---|---|
| `setinputsizes(sizes)`, `setoutputsize(size[, column])` | Accepted and ignored ("implementations are free to have this method do nothing"). They work on a closed cursor too. |
| `lastrowid` | Always `None`: PostgreSQL tables have no row IDs. Use `INSERT ... RETURNING`. |
| `callproc(procname[, parameters])` | **Not defined.** `hasattr(cursor, "callproc")` is `False`. Run `select f(%s)` or `call p(%s)` with `execute`; a procedure's `INOUT` values come back as a row (`call pr(1)` returns one row). |
| `connection`, `rownumber`, `scroll`, `next()` / `__iter__`, `arraysize` | Defined: 6.1, 6.5, 9.3. |
| `messages`, `errorhandler` (on cursor and connection) | Not defined. |

The complete file:

```python
"""PEP 249 members that have nothing to do in this driver."""
from __future__ import annotations

from typing import Any, Sequence

from ._cur_core import CurCore


class OptionalMixin(CurCore):
    """setinputsizes, setoutputsize and lastrowid."""

    def setinputsizes(self, sizes: Sequence[Any]) -> None:
        """Do nothing: parameters need no size declaration (PEP 249)."""

    def setoutputsize(self, size: int, column: int | None = None) -> None:
        """Do nothing: columns need no size declaration (PEP 249)."""

    @property
    def lastrowid(self) -> None:
        """Always None: PostgreSQL tables have no row IDs (PEP 249 extension)."""
        return None
```

### 6.10 `BaseCursor` (`stuntgres/_cursor_base.py`)

The composition, with the bases in exactly this order (every mixin derives from
`CurCore`, so the method resolution order is `BaseCursor`, `BaseCursorFeatures`,
`OptionalMixin`, `NextsetMixin`, `ManyMixin`, `FetchMixin`, `CurCore`, `object`):

```python
"""The base class of Cursor and AsyncCursor."""
from __future__ import annotations

from ._cur_fetch import FetchMixin
from ._cur_many import ManyMixin
from ._cur_nextset import NextsetMixin
from ._cur_optional import OptionalMixin
from ._features import BaseCursorFeatures

__all__ = ["BaseCursor"]


class BaseCursor(
    BaseCursorFeatures, OptionalMixin, NextsetMixin, ManyMixin, FetchMixin
):
    """All state and every generator of a cursor; no waiting, no lock."""
```

## 7. Row factories (`stuntgres/rows.py`)

```python
RowMaker = Callable[[tuple[Any, ...]], Any]
RowFactory = Callable[[Any], RowMaker]

def tuple_row(cursor: HasDescription) -> RowMaker: ...
def dict_row(cursor: HasDescription) -> RowMaker: ...
def namedtuple_row(cursor: HasDescription) -> RowMaker: ...
```

A **row factory** is a callable that takes the cursor and returns a **row maker**; the
row maker takes the tuple of loaded values of one row and returns the row. The cursor
calls the factory once per result that has rows, when the result becomes current
(`CurCore._select_result`, 6.4), so the factory may read `cursor.description`; the row
maker is then passed to `Transformer.load_rows(first, last, make_row)` (ADAPT 3.5), the
hook that design left. `HasDescription` is a `typing.Protocol` with the one property
`description`; `RowMaker` is the same alias as `stuntgres._transformer.RowMaker`,
repeated so that this module imports nothing of the package.

| Factory | Row maker | Notes |
|---|---|---|
| `tuple_row` | the built-in `tuple` | The default. `tuple(t)` returns `t` itself for a tuple, and the cursor recognises this maker (`maker is tuple`) and passes `make_row=None`, so the default costs nothing per row. |
| `dict_row` | `lambda values: dict(zip(names, values))`, `names` = the column names | A new `dict` per row, keys in column order; when two columns have the same name the last one wins. |
| `namedtuple_row` | `Row._make`, `Row = collections.namedtuple("Row", names, rename=True)` | The class is cached per tuple of column names (`functools.lru_cache(maxsize=512)` on `_row_class`). `rename=True` replaces a name that is no identifier, a keyword, a duplicate or starts with an underscore by its position: `_0`, `_1`, ... |

`_names(cursor)` is `tuple(column.name for column in cursor.description)`, or `()` when
`description` is `None`.

Where a factory is set: `connect(row_factory=...)` or `connection.row_factory = ...`
(the default of cursors made afterwards); `connection.cursor(row_factory=...)`;
`cursor.row_factory = ...`, which also applies to the rows of the current result that
were not fetched yet. A user-written factory follows the same two-step shape.

**(measured)**

| Query, factory | Row |
|---|---|
| `select 1 as a, 'x' as b`, `dict_row` | `{"a": 1, "b": "x"}` |
| `select 1 as a, 'x' as b`, `namedtuple_row` | `Row(a=1, b='x')` |
| `select 1 as a, 2 as a, 3`, `namedtuple_row` | fields `("a", "_1", "_2")` |
| columns named `a`, `?column?`, `a`, `class`, `_x`, an empty name | fields `("a", "_1", "_2", "_3", "_4", "_5")` |
| columns `a`, `a`, `?column?` with values 1, 2, 3, `dict_row` | `{"a": 2, "?column?": 3}` |

The complete file:

```python
"""Row factories: what a cursor returns for each row."""
from __future__ import annotations

import functools
from collections import namedtuple
from typing import Any, Callable, Protocol

RowMaker = Callable[[tuple[Any, ...]], Any]


class HasDescription(Protocol):
    """What a row factory reads of a cursor."""

    @property
    def description(self) -> Any: ...


RowFactory = Callable[[Any], RowMaker]


def _names(cursor: HasDescription) -> tuple[str, ...]:
    description = cursor.description
    if description is None:
        return ()
    return tuple(column.name for column in description)


def tuple_row(cursor: HasDescription) -> RowMaker:
    """Row factory: each row is a tuple of the column values (the default)."""
    return tuple


def dict_row(cursor: HasDescription) -> RowMaker:
    """Row factory: each row is a dict from column name to value.

    When two columns have the same name the last one wins.
    """
    names = _names(cursor)

    def make(values: tuple[Any, ...]) -> dict[str, Any]:
        return dict(zip(names, values))

    return make


@functools.lru_cache(maxsize=512)
def _row_class(names: tuple[str, ...]) -> Any:
    return namedtuple("Row", names, rename=True)  # type: ignore[misc]


def namedtuple_row(cursor: HasDescription) -> RowMaker:
    """Row factory: each row is a named tuple with the column names as fields.

    A name that is no Python identifier, a keyword or a repetition is replaced
    by its position: _0, _1, ... (collections.namedtuple with rename=True).
    """
    maker: RowMaker = _row_class(_names(cursor))._make
    return maker
```

## 8. Notices, notifications, cancel and interruption

### 8.1 Notices (`stuntgres/_conn_notices.py`)

```python
NoticeHandler = Callable[[Diagnostic], None]

class NoticesMixin(ConnCore):
    def add_notice_handler(self, callback: NoticeHandler) -> None: ...
    def remove_notice_handler(self, callback: NoticeHandler) -> None: ...
    def _on_notice(self, result: PGresult) -> None: ...
```

A notice (`NOTICE`, `WARNING`, `INFO`, ... of the server, `RAISE NOTICE` in a function)
reaches the connection through the receiver of PQ 5.9, which calls
`pgconn.notice_handler`; `ConnCore.__init__` set that to a router that calls
`_on_notice(result)` (4.3). The call happens **inside** a library call of an operation of
this connection, on the thread or task that runs the operation, with the connection's
lock held.

| Member | Algorithm |
|---|---|
| `add_notice_handler(callback)` | Under `_handlers_lock`: `_notice_handlers = _notice_handlers + (callback,)`. The same callable may be registered twice. |
| `remove_notice_handler(callback)` | Under `_handlers_lock`: make a list of the tuple, `remove(callback)` (the first occurrence; `ValueError` when it is not registered), store the tuple of the list. |
| `_on_notice(result)` | 1. `handlers = self._notice_handlers` (one snapshot). If empty: return (the notice is dropped; nothing is printed). 2. `diag = Diagnostic(ErrorInfo.from_result(result, self._codec))`: a copy, because the result is only borrowed for the duration of the callback (PQ 5.9), decoded with the codec remembered by `_check_open`, because the callback must not call a function of its connection (`docs/LIBSPQ.md` 5.6). 3. For each handler: `callback(diag)` inside `try`; `except Exception as exc:` `logger.error("notice handler %r failed: %s", callback, exc, exc_info=exc)` and go on with the next handler. |

Decision on the question PQ 5.9 left open: user handlers run **at once**, inside the
library call, not deferred to the end of the operation. A notice is therefore seen while
a long statement still runs (progress messages of a function). The price is the rule of
1.4: a handler must not use its own connection, and an exception it raises, including a
`KeyboardInterrupt` that arrives while it runs, is logged and not raised (PQ 5.9).

**(measured, both modes, experiment 6)** A function that executes `raise notice 'hello
%', n`, `raise warning 'careful' using detail = 'a detail', hint = 'a hint', errcode =
'01234'` and `raise info '...'` calls the handler three times, in order, before `select
noisy(1)` returns its row:

| `severity` | `sqlstate` | `message_primary` | `message_detail` | `message_hint` | `context` |
|---|---|---|---|---|---|
| `NOTICE` | `00000` | `hello 1` | `None` | `None` | `PL/pgSQL function noisy(integer) line 3 at RAISE` |
| `WARNING` | `01234` | `careful` | `a detail` | `a hint` | `PL/pgSQL function noisy(integer) line 4 at RAISE` |
| `INFO` | `00000` | the text | `None` | `None` | the same with the line of that statement |

With `client_min_messages = warning` the `NOTICE` is not sent, the `INFO` still is. `drop
table if exists nosuch_table` gives `NOTICE` `00000` `table "nosuch_table" does not
exist, skipping`; `commit` outside a transaction gives `WARNING` `25P01` `there is no
transaction in progress`. A handler that raises `ValueError("boom")` produces one log
record `notice handler <function ... at 0x...> failed: boom` per notice; the handlers
after it are still called and the statement returns its result.

The complete file:

```python
"""Notice handlers of a connection."""
from __future__ import annotations

from typing import TYPE_CHECKING, Callable

from ._conn_core import ConnCore, logger
from ._diagnostic import Diagnostic, ErrorInfo

if TYPE_CHECKING:
    from .pq import PGresult

NoticeHandler = Callable[[Diagnostic], None]


class NoticesMixin(ConnCore):
    """Calls the registered handlers for every notice of the session."""

    def add_notice_handler(self, callback: NoticeHandler) -> None:
        """Register a callable that gets the Diagnostic of every notice.

        It runs while the library delivers the notice: it must not use this
        connection. An exception it raises is logged, not raised.
        """
        with self._handlers_lock:
            self._notice_handlers = self._notice_handlers + (callback,)

    def remove_notice_handler(self, callback: NoticeHandler) -> None:
        """Unregister a notice handler; ValueError when it is not registered."""
        with self._handlers_lock:
            handlers = list(self._notice_handlers)
            handlers.remove(callback)
            self._notice_handlers = tuple(handlers)

    def _on_notice(self, result: PGresult) -> None:
        handlers = self._notice_handlers
        if not handlers:
            return
        diag = Diagnostic(ErrorInfo.from_result(result, self._codec))
        for callback in handlers:
            try:
                callback(diag)
            except Exception as exc:
                logger.error(
                    "notice handler %r failed: %s", callback, exc, exc_info=exc
                )
```

### 8.2 Notifications (`stuntgres/_conn_notify.py`)

```python
NotifyHandler = Callable[[Notify], None]

NOTIFY_INTERVAL = 0.1                              # in _conn_core.py

class NotifyWindow:                                # in _conn_core.py
    def __init__(self, timeout: float | None, stop_after: int | None) -> None: ...
    def full(self) -> bool: ...
    def interval(self) -> float: ...

class NotifyMixin(ConnCore):
    def add_notify_handler(self, callback: NotifyHandler) -> None: ...
    def remove_notify_handler(self, callback: NotifyHandler) -> None: ...
    def _on_notify(self, pgn: PGnotify) -> None: ...
    def _notifies_gen(self) -> PQGen[None]: ...
```

**The fact everything here rests on:** a notification becomes visible only after the
library consumed input (`docs/LIBSPQ.md` 5.6). The generators of PQ 7 do that in two
places: `fetch` (so every statement delivers the notifications that arrived before or
with its results) and `notifies` (one bounded wait for input). Both pass each
notification to `pgconn.notify_handler`, which `ConnCore.__init__` routed to
`_on_notify(pgn)`. Nothing is read while the connection is idle: the descriptor just
becomes readable **(measured: after `NOTIFY` from another session the listener's
descriptor is readable and its backlog is still empty)**.

| Member | Algorithm |
|---|---|
| `add_notify_handler(callback)`, `remove_notify_handler(callback)` | As the notice twins, on `_notify_handlers`. |
| `_on_notify(pgn)` | 1. `notify = Notify(pgn.relname.decode(codec, "replace"), pgn.extra.decode(codec, "replace"), pgn.be_pid)` with `codec = self._codec`. 2. `handlers = self._notify_handlers`. If empty: `self._notify_backlog.append(notify)` and return. 3. Else for each handler: `callback(notify)`; an `Exception` is logged as `"notify handler %r failed: %s"` at `ERROR` and the next handler is called. It is not raised, because it would otherwise abort the unrelated statement during which the notification happened to arrive. |
| `_notifies_gen()` | `_check_open()`; `yield from generators.notifies(self.pgconn)` (PQ 7.7: wait once for input, consume it, hand every notification to the handler; with `Ready.NONE` it returns without input). Its return value is not used: the notifications went through `_on_notify`. |

So there is one path, and two destinations: **handlers when any is registered, the
backlog otherwise.** The backlog is unbounded, like the queue libpq itself keeps until
`PQnotifies` is called; an application that listens and never reads grows it.

`notifies(*, timeout: float | None = None, stop_after: int | None = None)` of the public
classes (9.2; the asyncio twin is the same with `async with` and `await`) yields `Notify`
objects from the backlog and waits for more:

1. `window = NotifyWindow(timeout, stop_after)`; `backlog = self._notify_backlog`.
2. While the backlog is not empty and not `window.full()`: `window.count += 1`; yield
   `backlog.popleft()`.
3. If `window.full()` or `window.expired`: return.
4. `interval = window.interval()`.
5. `self._check_closed()` (no library call).
6. With the lock held: `self.wait(self._notifies_gen(), interval=interval)`. Go to 2.

`NotifyWindow`: `deadline` is `None` without timeout, else `time.monotonic() + timeout`;
`count` starts at 0, `expired` at `False`. `full()` is `stop_after is not None and count
>= stop_after`. `interval()` returns `NOTIFY_INTERVAL` (0.1 s) without deadline; else
`left = deadline - time.monotonic()`: when `left <= 0` it sets `expired = True` and
returns `0.0` (one last poll that does not block), otherwise it returns `min(left,
NOTIFY_INTERVAL)`.

One wait is therefore at most a tenth of a second long, and the lock is released after
each. That bound is what keeps `close()` from another thread or task from waiting for a
listener that would otherwise hold the lock until a notification arrives: `close()` sets
`_closed`, gets the lock within one interval, and the listener's next step 5 raises
`OperationalError("the connection is closed")`. (Step 5 matters with `threading.Lock`,
which is not fair: without it the listener could take the lock again before the closing
thread runs.)

Properties **(measured, both modes, experiment 6 and `tests/test_conn_notify.py`)**:

- `timeout=0` polls once and returns what is there; `timeout=0.2` with nothing arriving
  returns after 0.2 s; `timeout=None` waits until `stop_after` is reached (or for ever),
  in slices of `NOTIFY_INTERVAL`.
- `stop_after=n` yields exactly `n` and leaves the rest in the backlog for the next
  call: three notifications sent at once were taken as 1 and then 2.
- The lock is held only while waiting, never while the generator is suspended at a
  `yield`: the loop body may run statements on the same connection, and another thread
  may use it between two notifications.
- A notification that arrives while another statement runs is in the backlog when that
  statement returns.
- A session receives its own notifications (`pid` is its own `backend_pid`); an empty
  payload is `""`; a `NOTIFY` inside a transaction is delivered at commit.
- With a handler registered `notifies()` yields nothing, and still waits and delivers
  to the handlers: `list(conn.notifies(timeout=1))` is a way to pump them.
- `close()` from another thread or task while `notifies()` waits without timeout returns
  in well under a second, and the listener gets `the connection is closed`.
- `Notify` is `(channel, payload, pid)`.

The complete file:

```python
"""Notifications (LISTEN / NOTIFY) of a connection."""
from __future__ import annotations

from typing import TYPE_CHECKING, Callable

from . import generators
from ._conn_core import ConnCore, Notify, logger
from ._gentypes import PQGen

if TYPE_CHECKING:
    from .pq import PGnotify

NotifyHandler = Callable[[Notify], None]


class NotifyMixin(ConnCore):
    """Routes notifications to the handlers, or keeps them for notifies()."""

    def add_notify_handler(self, callback: NotifyHandler) -> None:
        """Register a callable that gets every Notify as it is received.

        While a handler is registered notifies() yields nothing. An exception
        the handler raises is logged, not raised.
        """
        with self._handlers_lock:
            self._notify_handlers = self._notify_handlers + (callback,)

    def remove_notify_handler(self, callback: NotifyHandler) -> None:
        """Unregister a notify handler; ValueError when it is not registered."""
        with self._handlers_lock:
            handlers = list(self._notify_handlers)
            handlers.remove(callback)
            self._notify_handlers = tuple(handlers)

    def _on_notify(self, pgn: PGnotify) -> None:
        codec = self._codec
        notify = Notify(
            pgn.relname.decode(codec, "replace"),
            pgn.extra.decode(codec, "replace"),
            pgn.be_pid,
        )
        handlers = self._notify_handlers
        if not handlers:
            self._notify_backlog.append(notify)
            return
        for callback in handlers:
            try:
                callback(notify)
            except Exception as exc:
                logger.error(
                    "notify handler %r failed: %s", callback, exc, exc_info=exc
                )

    def _notifies_gen(self) -> PQGen[None]:
        """Wait once for input and route the notifications that arrived."""
        self._check_open()
        yield from generators.notifies(self.pgconn)
```

### 8.3 `cancel` (`stuntgres/_conn_cancel.py`)

```python
class CancelMixin(ConnCore):
    def cancel(self) -> None: ...
    def _cancel_gen(self) -> PQGen[None]: ...
    def _interrupt_failed(self, exc: BaseException) -> None: ...
```

| Member | Algorithm |
|---|---|
| `cancel()` | If `_closed`: return. `self._cancel_handle.cancel()` (PQ 5.11). No lock: this is the call that may run while another thread is inside an operation of the connection. A failure raises `OperationalError("cancel failed: ...")`. |
| `_cancel_gen()` | `results = yield from generators.cancel(self.pgconn)` (PQ 7.8: nothing when no statement is active; else a cancel request and the collection of everything the statement still returns); clear every result. |
| `_interrupt_failed(exc)` | `logger.warning("cannot end the interrupted statement (%s: %s): closing the connection", type(exc).__name__, exc)`; `_broken = True`; `pgconn.finish()`. |

`cancel()` uses the handle made in `ConnCore.__init__`, because making one
(`PGconn.get_cancel`) is a call on the connection and only `SPQcancel` itself is allowed
beside a running call (`docs/design/LIBSPQ-CABI.md` 7.2). The handle is never freed
explicitly: another thread may be inside `cancel()` when the connection is closed, so it
lives as long as the connection object and its finalizer releases it (PQ 5.11).
`_cancel_gen` runs on the thread that owns the operation, where `generators.cancel` may
make and free a handle of its own.

What the canceller sees: nothing (the call returns when the request was delivered; with
libspq at once, with a real libpq after a round trip of 1 to 3 ms, PQ 5.11). What the
thread running the statement sees: `errors.QueryCanceled` (an `OperationalError`,
SQLSTATE `57014`, primary message `canceling statement due to user request`), after
which the session is `IDLE` or, inside a transaction, `INERROR`. A cancel that arrives
when nothing runs has no effect.

`cancel_safe(*, timeout: float = 30.0)` exists for psycopg compatibility and is
`cancel()`: psycopg's non-blocking variant needs `PQcancelCreate`, which libspq does not
have (PQ 5.16), and `SPQcancel` never blocks anyway. The `timeout` is accepted and
unused.

The complete file:

```python
"""Cancel requests and the way out of an interrupted wait."""
from __future__ import annotations

from . import generators
from ._conn_core import ConnCore, logger
from ._gentypes import PQGen


class CancelMixin(ConnCore):
    """cancel() and the pieces the public classes build interruption from."""

    def cancel(self) -> None:
        """Ask the server to cancel the running statement.

        May be called from any thread while another one uses the connection.
        Does nothing when the connection is closed or nothing is running.
        """
        if self._closed:
            return
        self._cancel_handle.cancel()

    def _cancel_gen(self) -> PQGen[None]:
        """Cancel the statement in flight and collect what it still returns."""
        results = yield from generators.cancel(self.pgconn)
        for result in results:
            result.clear()

    def _interrupt_failed(self, exc: BaseException) -> None:
        """The statement could not be ended: give the connection up."""
        logger.warning(
            "cannot end the interrupted statement (%s: %s): closing the connection",
            type(exc).__name__, exc,
        )
        self._broken = True
        self.pgconn.finish()
```

### 8.4 Interruption

Every operation waits in exactly one place: `Connection.wait` or `AsyncConnection.wait`
(9.1). That is where an interruption is handled, once, for every statement the driver
can send.

`Connection.wait(gen, interval=None)`:

1. `try: return waiting.wait(gen, self._wait_fileno(), interval=interval)` (PQ 8.1).
2. `except KeyboardInterrupt:` the waiter has already closed `gen` (PQ fixed point P12)
   and the statement is still running on the server. Inside a nested `try`:
   `waiting.wait(self._cancel_gen(), self._wait_fileno(), timeout=CANCEL_TIMEOUT)`;
   `except BaseException as exc: self._interrupt_failed(exc)`. Then `raise`: the
   `KeyboardInterrupt` goes on to the caller.
3. `finally: self._note_status()`.

`AsyncConnection.wait(gen, interval=None)` is the same with `await
waiting_async.wait_async(...)` (PQ 8.2) and `except (asyncio.CancelledError,
KeyboardInterrupt):`. Awaiting inside the handler of a cancellation is deliberate: the
task was asked to stop, and it does stop, after at most `CANCEL_TIMEOUT` seconds and
normally after a few milliseconds, with the `CancelledError` it was given; the event
loop keeps running other tasks meanwhile.

Outcomes **(measured, experiment 5, identical in both modes)**, with the statement `do
$$ begin raise notice 'started'; perform pg_sleep(30); end $$`, interrupted once its
notice had arrived:

| Interruption | Raised to the caller | Time | Connection afterwards |
|---|---|---|---|
| `SIGINT`, autocommit connection | `KeyboardInterrupt` | under 1 s | open; status `IDLE`; lock free; `select 1` works |
| `SIGINT` inside a transaction | `KeyboardInterrupt` | under 1 s | open; status `INERROR` (the next statement raises `25P02`); after `rollback()` it works |
| `SIGINT`, and the cancel path cannot finish (a generator that never ends, timeout patched to 0.3 s) | `KeyboardInterrupt` | under 1 s | closed and broken; one warning logged; later operations raise `the connection is lost` |
| `task.cancel()`, autocommit | `CancelledError` | under 1 s | open; `IDLE`; nothing registered in the event loop; `select 1` works |
| `task.cancel()` inside a transaction | `CancelledError` | under 1 s | open; `INERROR`; after `rollback()` it works |
| `task.cancel()` twice (the second while the cancel path waits) | `CancelledError` | under 1 s | closed and broken; one warning logged |
| `asyncio.wait_for(conn.execute("select pg_sleep(30)"), 0.3)` | `TimeoutError` | 0.3 s | open; `IDLE`; `select 1` works |
| `SIGINT` under `asyncio.run` (Python 3.11 and later: the main task is cancelled) | `CancelledError` in the task | under 1 s | open; `select 1` works |

Rules:

- The interrupted cursor has no result; nothing of the interrupted statement is kept.
  No native object is left behind: the results that the cancel path collected are
  cleared, its cancel handle is freed (PQ 7.8), and the count of live `PGresult` and
  `PGcancel` objects is the same before and after **(measured,
  `tests/test_conn_cancel.py`)**.
- A statement that finished before the cancel request arrived is simply complete: the
  cancel path finds the session not active and does nothing; the caller still gets the
  interruption.
- Inside a `transaction()` block the block's exit runs after `wait` re-raised: it finds
  the session in `INERROR` and rolls back (`ROLLBACK`, or `ROLLBACK TO` its savepoint),
  so the block is left cleanly **(measured: after cancelling a task inside a block the
  status is `IDLE`, the depth 0 and the row inserted in the block is gone)**.
- `KeyboardInterrupt` is raised by Python only in the main thread, and only when
  `SIGINT` has Python's default handler (PQ 8.1: a process started in the background
  inherits it ignored).
- A waiter timeout is not an interruption: `WaitTimeout` appears only in
  `Connection.connect` (translated to `ConnectionTimeout`) and inside the cancel path.

**`close()` and a running statement.** `close()` never waits for a statement to finish
and sends nothing (`docs/LIBSPQ.md` 5.4: `SPQfinish` cancels the statement and rolls the
session back). But finishing a connection while another thread is inside a library call
on it is forbidden, so `Connection.close()` is (9.2): set `_closed`; try to take the
lock without blocking; if another thread holds it, call `self._cancel_handle.cancel()`
(its statement ends with `57014` within milliseconds) and then take the lock; `_finish()`;
release. **(measured)** A thread blocked in `pg_sleep(30)` got one `57014` result, and
`close()` from the main thread returned in well under a second; `close()` on the owning
thread with a statement sent and not collected returned at once; `close()` while another
thread or task waits in `notifies()` returned within one `NOTIFY_INTERVAL` (8.2). What
`close()` does wait for is the end of the other thread's current operation after its
statement was cancelled; a statement the server cannot cancel delays it that long.

## 9. The synchronous and the asyncio classes

### 9.1 The wrapper pattern

```
Connection      = ConnectionFeatures      + BaseConnection    lock: threading.Lock
AsyncConnection = AsyncConnectionFeatures + BaseConnection    lock: asyncio.Lock
Cursor          = CursorFeatures          + BaseCursor
AsyncCursor     = AsyncCursorFeatures     + BaseCursor
Transaction     = BaseTransaction         (context manager)
AsyncTransaction = BaseTransaction        (async context manager)
```

A public method that talks to the server has exactly this body, and nothing else:

```python
def commit(self) -> None:                      async def commit(self) -> None:
    with self.lock:                                async with self.lock:
        self.wait(self._commit_gen())                  await self.wait(self._commit_gen())
```

`wait` is the method of 8.4: the one place that drives a generator, handles an
interruption and records a lost connection. Every public method of the four classes is
one of five kinds:

| Kind | Body | Examples |
|---|---|---|
| G | lock, then `wait(<generator of the base class>)` | `commit`, `rollback`, `execute`, `executemany`, `Transaction.__enter__` |
| P | lock, then a plain method of the base class (state change, no I/O) | `set_autocommit`, `set_isolation_level` |
| B | defined once in the base class, used by both twins unchanged | `cancel`, `add_notice_handler`, `nextset`, `description`, `closed` |
| F | no lock: reads the cursor's client-side result through a plain base method | `fetchone`, `fetchmany`, `fetchall`, `scroll` (coroutines in the asyncio class, for API symmetry with psycopg 3) |
| S | written out in both twins because the language forces it: a classmethod that waits, a context manager, a generator | `connect`, `close`, `__enter__`/`__aenter__`, `notifies`, `wait` |

The S methods are the only code that exists twice; their two versions differ in `with` /
`async with`, `wait` / `await wait`, `def` / `async def` and the waiter they call. There
are seven of them on the connection and three on the cursor.

Threads and tasks:

- `Connection`: any number of threads may share one connection (PEP 249 level 2). The
  lock makes each operation atomic; two threads that interleave statements share one
  transaction, exactly like two cursors do. `cancel()`, `close()`, `closed`, `broken`
  and `fileno()` may be called from any thread at any time; `info` only while no other
  thread runs a statement (D11).
- `AsyncConnection`: any number of tasks of one event loop may share one connection; the
  `asyncio.Lock` queues them. It must not be used from two threads or two loops.
- A cursor, a `Transaction` and the iterator of `notifies()` belong to one thread or
  task at a time.
- Handlers run on the thread or task that is inside an operation (1.4).
- After `fork()` a child must not use a connection of the parent (`docs/LIBSPQ.md`
  section 7 rule 3): the loader refuses there (PQ 2.4) and the finalizers are silent
  (`_pid`).

### 9.2 `Connection` (`stuntgres/connection.py`)

Every public member; "(4.3)" and the like point at the base-class algorithm.

| Member | Kind | Algorithm |
|---|---|---|
| `connect(conninfo: str = "", *, autocommit: bool = False, row_factory: RowFactory \| None = None, cursor_factory: type[Cursor] \| None = None, **kwargs: Any) -> Connection` (classmethod; `stuntgres.connect`) | S | 1. `options = conninfo_to_dict(conninfo, **kwargs)`. 2. `timeout = connect_timeout(options)`. 3. `dsn = make_conninfo(**options)` (4.1; `ProgrammingError` for a bad string or keyword). 4. `gen = cls._connect_gen(dsn, autocommit=autocommit)` (4.3). 5. `conn = waiting.wait_conn(gen, timeout=timeout)` (PQ 8.1); `except WaitTimeout:` raise `ConnectionTimeout("connection timeout expired")` without context. 6. If `row_factory is not None`: `conn.row_factory = row_factory`; likewise `cursor_factory`. 7. Return `conn`. No SQL is sent. |
| `__init__(self, pgconn: PGconn) -> None` | S | `super().__init__(pgconn)`; `self.lock = threading.Lock()`. Called by `_connect_gen`; not for consumers. |
| `close(self) -> None` | S | 1. If `_closed`: return (closing twice is allowed). 2. `_closed = True`. 3. `if not self.lock.acquire(blocking=False):` `self._cancel_handle.cancel()`; `self.lock.acquire()`. 4. `try: self._finish()` `finally: self.lock.release()`. (8.4) |
| `__enter__(self) -> Connection`, `__exit__(self, exc_type, exc_val, exc_tb) -> None` | S | 5.2: commit or roll back, then close. |
| `wait(self, gen: PQGen[RV], interval: float \| None = None) -> RV` | S | 8.4. The caller holds the lock. |
| `cursor(self, name: str = "", *, row_factory: RowFactory \| None = None, scrollable: bool \| None = None, withhold: bool = False) -> Cursor` | B-like (no I/O; written in both twins because the default class differs) | 1. `_check_closed()` (no library call). 2. `row_factory = self.row_factory` when `None`. 3. If `name`: `factory = getattr(self, "server_cursor_factory", None)`; `None`: raise `NotSupportedError("named cursors are not available")`; else return `factory(self, name, row_factory=row_factory, scrollable=scrollable, withhold=withhold)` (1.5). 4. `factory = self.cursor_factory`; when `None`: `from .cursor import Cursor` (inside the method) and use it. 5. Return `factory(self, row_factory=row_factory)`. |
| `execute(self, query: Query, params: Params \| None = None) -> Cursor` | S | `return self.cursor().execute(query, params)`: a new cursor per call (psycopg 3's shortcut). |
| `commit(self) -> None` | G | `_commit_gen()` (4.6). |
| `rollback(self) -> None` | G | `_rollback_gen()` (4.6). |
| `autocommit`, `isolation_level`, `read_only`, `deferrable` (properties) | B | Getters: 4.6, 5.3. Assignment calls `_assign(name, value)`, which here is `getattr(self, "set_" + name)(value)`. |
| `set_autocommit(self, value: bool) -> None` | P | `_set_autocommit(value)` (4.6). |
| `set_isolation_level(self, value: IsolationLevel \| None) -> None` | P | `_set_isolation_level(value)` (5.3). |
| `set_read_only(self, value: bool \| None) -> None` | P | `_set_read_only(value)`. |
| `set_deferrable(self, value: bool \| None) -> None` | P | `_set_deferrable(value)`. |
| `transaction(self, savepoint_name: str \| None = None, force_rollback: bool = False) -> Transaction` | S | `from .transaction import Transaction` (inside the method); `return Transaction(self, savepoint_name, force_rollback)` (5.4). Nothing is sent until the block is entered. |
| `notifies(self, *, timeout: float \| None = None, stop_after: int \| None = None) -> Iterator[Notify]` | S | A generator: 8.2. |
| `add_notice_handler`, `remove_notice_handler`, `add_notify_handler`, `remove_notify_handler` | B | 8.1, 8.2. |
| `cancel(self) -> None` | B | 8.3. |
| `cancel_safe(self, *, timeout: float = 30.0) -> None` | S | `self.cancel()` (8.3). |
| `closed`, `broken`, `info`, `adapters`, `connection`, `fileno()`, `pgconn`, `row_factory`, `cursor_factory`, `lock`, the ten exception attributes | B | 4.3. |

**(measured, both modes, `tests/test_connection.py`)** `connect(conninfo)`,
`connect("", **keywords)` and `connect(wrong_string, dbname=right)` reach the same
database; `connect("nosuch=1")` and `connect(conninfo, nosuch=1)` raise
`ProgrammingError('invalid connection option "nosuch"')`; `options="-c
nosuch_setting=1"` raises `OperationalError` starting with `connection failed: ` and
containing `unrecognized configuration parameter "nosuch_setting"`; a port nobody
listens on gives `connection failed: connection to server at "127.0.0.1", port 1 failed:
Connection refused`; `repr` is `<stuntgres.Connection [open] at 0x...>` and `[closed]`
after `close()`.

The complete file:

```python
"""The synchronous connection."""
from __future__ import annotations

import threading
from types import TracebackType
from typing import TYPE_CHECKING, Any, Iterator

from . import errors as e
from . import waiting
from ._conn_core import CANCEL_TIMEOUT, IsolationLevel, Notify, NotifyWindow
from ._conn_core import logger
from ._connection_base import BaseConnection
from ._features import ConnectionFeatures
from ._gentypes import RV, PQGen
from ._queries import Params, Query
from .conninfo import conninfo_to_dict, connect_timeout, make_conninfo
from .rows import RowFactory

if TYPE_CHECKING:
    from .cursor import Cursor
    from .pq import PGconn
    from .transaction import Transaction


class Connection(ConnectionFeatures, BaseConnection):
    """A connection to a database: PEP 249, blocking."""

    __module__ = "stuntgres"

    def __init__(self, pgconn: PGconn) -> None:
        super().__init__(pgconn)
        self.lock = threading.Lock()

    @classmethod
    def connect(
        cls,
        conninfo: str = "",
        *,
        autocommit: bool = False,
        row_factory: RowFactory | None = None,
        cursor_factory: type[Cursor] | None = None,
        **kwargs: Any,
    ) -> Connection:
        """Connect and return the connection; no SQL is sent."""
        options = conninfo_to_dict(conninfo, **kwargs)
        timeout = connect_timeout(options)
        dsn = make_conninfo(**options)
        gen = cls._connect_gen(dsn, autocommit=autocommit)
        try:
            conn: Connection = waiting.wait_conn(gen, timeout=timeout)
        except e.WaitTimeout:
            raise e.ConnectionTimeout("connection timeout expired") from None
        if row_factory is not None:
            conn.row_factory = row_factory
        if cursor_factory is not None:
            conn.cursor_factory = cursor_factory
        return conn

    def __enter__(self) -> Connection:
        return self

    def __exit__(
        self,
        exc_type: type[BaseException] | None,
        exc_val: BaseException | None,
        exc_tb: TracebackType | None,
    ) -> None:
        if self.closed:
            return
        try:
            if exc_type is None:
                self.commit()
            else:
                try:
                    self.rollback()
                except Exception as exc:
                    logger.warning("error ignored in rollback of %r: %s", self, exc)
        finally:
            self.close()

    def close(self) -> None:
        """Close the connection now; an open transaction is rolled back.

        Nothing is sent and no running statement is waited for. Closing twice
        is allowed.
        """
        if self._closed:
            return
        self._closed = True
        if not self.lock.acquire(blocking=False):
            # Another thread is inside an operation: end its statement first.
            self._cancel_handle.cancel()
            self.lock.acquire()
        try:
            self._finish()
        finally:
            self.lock.release()

    def wait(self, gen: PQGen[RV], interval: float | None = None) -> RV:
        """Drive a generator to its end, waiting on the connection's descriptor.

        On Ctrl-C the running statement is cancelled and its outcome collected,
        so the connection stays usable; then KeyboardInterrupt is raised again.
        """
        try:
            return waiting.wait(gen, self._wait_fileno(), interval=interval)
        except KeyboardInterrupt:
            try:
                waiting.wait(
                    self._cancel_gen(), self._wait_fileno(), timeout=CANCEL_TIMEOUT
                )
            except BaseException as exc:
                self._interrupt_failed(exc)
            raise
        finally:
            self._note_status()

    def cursor(
        self,
        name: str = "",
        *,
        row_factory: RowFactory | None = None,
        scrollable: bool | None = None,
        withhold: bool = False,
    ) -> Cursor:
        """Return a new cursor; `name` asks for a server-side cursor."""
        self._check_closed()
        if row_factory is None:
            row_factory = self.row_factory
        if name:
            factory = getattr(self, "server_cursor_factory", None)
            if factory is None:
                raise e.NotSupportedError("named cursors are not available")
            cursor: Cursor = factory(
                self,
                name,
                row_factory=row_factory,
                scrollable=scrollable,
                withhold=withhold,
            )
            return cursor
        factory = self.cursor_factory
        if factory is None:
            from .cursor import Cursor

            factory = Cursor
        cursor = factory(self, row_factory=row_factory)
        return cursor

    def execute(self, query: Query, params: Params | None = None) -> Cursor:
        """Run a statement on a new cursor and return that cursor."""
        return self.cursor().execute(query, params)

    def commit(self) -> None:
        """Commit the open transaction; does nothing when none is open."""
        with self.lock:
            self.wait(self._commit_gen())

    def rollback(self) -> None:
        """Roll the open transaction back; does nothing when none is open."""
        with self.lock:
            self.wait(self._rollback_gen())

    def _assign(self, name: str, value: Any) -> None:
        getattr(self, "set_" + name)(value)

    def set_autocommit(self, value: bool) -> None:
        """Set autocommit; ProgrammingError inside a transaction."""
        with self.lock:
            self._set_autocommit(value)

    def set_isolation_level(self, value: IsolationLevel | None) -> None:
        """Set isolation_level; ProgrammingError inside a transaction."""
        with self.lock:
            self._set_isolation_level(value)

    def set_read_only(self, value: bool | None) -> None:
        """Set read_only; ProgrammingError inside a transaction."""
        with self.lock:
            self._set_read_only(value)

    def set_deferrable(self, value: bool | None) -> None:
        """Set deferrable; ProgrammingError inside a transaction."""
        with self.lock:
            self._set_deferrable(value)

    def transaction(
        self, savepoint_name: str | None = None, force_rollback: bool = False
    ) -> Transaction:
        """Return a context manager for a transaction or a nested savepoint."""
        from .transaction import Transaction

        return Transaction(self, savepoint_name, force_rollback)

    def notifies(
        self, *, timeout: float | None = None, stop_after: int | None = None
    ) -> Iterator[Notify]:
        """Yield notifications as they arrive.

        Stops after `timeout` seconds (None: never) or after `stop_after`
        notifications (None: no limit). The connection is locked only while
        waiting, a tenth of a second at a time, so the loop body may use it
        and close() from another thread is not kept waiting.
        """
        window = NotifyWindow(timeout, stop_after)
        backlog = self._notify_backlog
        while True:
            while backlog and not window.full():
                window.count += 1
                yield backlog.popleft()
            if window.full() or window.expired:
                return
            interval = window.interval()
            self._check_closed()
            with self.lock:
                self.wait(self._notifies_gen(), interval=interval)

    def cancel_safe(self, *, timeout: float = 30.0) -> None:
        """Cancel the running statement; the same as cancel()."""
        self.cancel()
```

### 9.3 `Cursor` (`stuntgres/cursor.py`)

| Member | Kind | Algorithm |
|---|---|---|
| `__init__(self, connection: Any, *, row_factory: RowFactory \| None = None) -> None` | B | 6.1. Made by `Connection.cursor()`. |
| `execute(self, query: Query, params: Params \| None = None) -> Cursor` | G | `_execute_gen(query, params)` (6.4); returns `self`. |
| `executemany(self, query: Query, params_seq: Iterable[Params], *, returning: bool = False) -> None` | G | `_executemany_gen(query, params_seq, returning)` (6.7). |
| `fetchone(self) -> Any` | F | `_fetchone()` (6.5): the next row or `None`. |
| `fetchmany(self, size: int \| None = None) -> list[Any]` | F | `_fetchmany(size)`. |
| `fetchall(self) -> list[Any]` | F | `_fetchall()`. |
| `scroll(self, value: int, mode: str = "relative") -> None` | F | `_scroll(value, mode)`. |
| `__iter__(self) -> Cursor`, `__next__(self) -> Any` | F | `__iter__` returns `self`; `__next__`: `row = self._fetchone()`; `None`: raise `StopIteration`; else return it. |
| `nextset(self) -> bool \| None` | B | 6.8. |
| `close(self) -> None` | S | `_close()` (6.4): results released; later `execute`, `executemany`, fetches, `scroll` and `nextset` raise `InterfaceError("the cursor is closed")`. Closing twice is allowed. |
| `__enter__(self) -> Cursor`, `__exit__(self, exc_type, exc_val, exc_tb) -> None` | S | Returns `self`; `close()`. |
| `setinputsizes`, `setoutputsize`, `lastrowid` | B | 6.9. |
| `description`, `rowcount`, `rownumber`, `statusmessage`, `arraysize`, `connection`, `adapters`, `row_factory`, `closed`, `pgresult` | B | 6.1, 6.4, 6.5. |

The complete file:

```python
"""The synchronous cursor."""
from __future__ import annotations

from types import TracebackType
from typing import TYPE_CHECKING, Any, Iterable

from ._cursor_base import BaseCursor
from ._features import CursorFeatures
from ._queries import Params, Query

if TYPE_CHECKING:
    from .connection import Connection


class Cursor(CursorFeatures, BaseCursor):
    """A cursor: runs statements and reads their results (PEP 249, blocking)."""

    __module__ = "stuntgres"

    _conn: Connection

    def __enter__(self) -> Cursor:
        return self

    def __exit__(
        self,
        exc_type: type[BaseException] | None,
        exc_val: BaseException | None,
        exc_tb: TracebackType | None,
    ) -> None:
        self.close()

    def close(self) -> None:
        """Release the results; every later use raises InterfaceError."""
        self._close()

    def execute(self, query: Query, params: Params | None = None) -> Cursor:
        """Run a statement and return the cursor itself.

        Without `params` the string is sent as it is and may hold several
        statements; with them, %s and %(name)s are bound on the server.
        """
        with self._conn.lock:
            self._conn.wait(self._execute_gen(query, params))
        return self

    def executemany(
        self, query: Query, params_seq: Iterable[Params], *, returning: bool = False
    ) -> None:
        """Run a statement once for every parameter set.

        rowcount becomes the sum over all sets. With `returning` every result
        is kept instead and nextset() walks them.
        """
        with self._conn.lock:
            self._conn.wait(self._executemany_gen(query, params_seq, returning))

    def fetchone(self) -> Any:
        """Return the next row, or None when there is none left."""
        return self._fetchone()

    def fetchmany(self, size: int | None = None) -> list[Any]:
        """Return the next `size` rows (default: arraysize); fewer at the end."""
        return self._fetchmany(size)

    def fetchall(self) -> list[Any]:
        """Return all the rows that are left."""
        return self._fetchall()

    def scroll(self, value: int, mode: str = "relative") -> None:
        """Move in the result; IndexError when the target is outside it."""
        self._scroll(value, mode)

    def __iter__(self) -> Cursor:
        return self

    def __next__(self) -> Any:
        row = self._fetchone()
        if row is None:
            raise StopIteration
        return row
```

### 9.4 `AsyncConnection` (`stuntgres/connection_async.py`)

The twin of 9.2. Same names, same parameters, same algorithms; this table lists every
public member and what differs.

| Member | Differs from `Connection` in |
|---|---|
| `async connect(conninfo: str = "", *, autocommit: bool = False, row_factory: RowFactory \| None = None, cursor_factory: type[AsyncCursor] \| None = None, **kwargs: Any) -> AsyncConnection` (classmethod) | `await waiting_async.wait_conn_async(gen, timeout=timeout)` (PQ 8.2). `make_conninfo` runs before the first await; it loads the native library on first use (PQ 2), which blocks once per process. |
| `__init__(self, pgconn: PGconn) -> None` | `self.lock = asyncio.Lock()`. |
| `async close(self) -> None` | 1. If `_closed`: return. 2. `_closed = True`. 3. If `self.lock.locked()`: `self._cancel_handle.cancel()`. 4. `async with self.lock: self._finish()`. |
| `async __aenter__(self) -> AsyncConnection`, `async __aexit__(self, exc_type, exc_val, exc_tb) -> None` | `await self.commit()`, `await self.rollback()`, `await self.close()`. Use: `async with await AsyncConnection.connect(...) as conn:`. |
| `async wait(self, gen: PQGen[RV], interval: float \| None = None) -> RV` | 8.4. |
| `cursor(self, name: str = "", *, row_factory: RowFactory \| None = None, scrollable: bool \| None = None, withhold: bool = False) -> AsyncCursor` | A plain method (no I/O); the default class is `AsyncCursor`, imported inside the method. |
| `async execute(self, query: Query, params: Params \| None = None) -> AsyncCursor` | `return await self.cursor().execute(query, params)`. |
| `async commit(self) -> None`, `async rollback(self) -> None` | `async with self.lock: await self.wait(...)`. |
| `autocommit`, `isolation_level`, `read_only`, `deferrable` | Read as properties. **Assignment raises** `AttributeError(f"'{name}' cannot be assigned on an async connection: use 'await connection.set_{name}(value)'")`: `_assign` cannot take an `asyncio.Lock`. |
| `async set_autocommit(self, value: bool) -> None`, `async set_isolation_level(self, value: IsolationLevel \| None) -> None`, `async set_read_only(self, value: bool \| None) -> None`, `async set_deferrable(self, value: bool \| None) -> None` | `async with self.lock:` then the plain base method. |
| `transaction(self, savepoint_name: str \| None = None, force_rollback: bool = False) -> AsyncTransaction` | A plain method returning the object of 9.6, imported inside the method. Use: `async with conn.transaction():`. |
| `async notifies(self, *, timeout: float \| None = None, stop_after: int \| None = None) -> AsyncIterator[Notify]` | An async generator: `async for notify in conn.notifies(...)`. |
| `async cancel_safe(self, *, timeout: float = 30.0) -> None` | `self.cancel()`. |
| `cancel()`, the four handler methods, `closed`, `broken`, `info`, `adapters`, `connection`, `fileno()`, `pgconn`, `row_factory`, `cursor_factory`, `lock`, the exception attributes | Nothing: inherited from `BaseConnection`. |

`cancel()` blocks for one local round trip with a real libpq (oracle and server mode: 1
to 3 ms, PQ 8.2) and not at all with libspq; that is accepted, as in PQ.

The complete file:

```python
"""The asyncio connection."""
from __future__ import annotations

import asyncio
from types import TracebackType
from typing import TYPE_CHECKING, Any, AsyncIterator

from . import errors as e
from . import waiting_async
from ._conn_core import CANCEL_TIMEOUT, IsolationLevel, Notify, NotifyWindow
from ._conn_core import logger
from ._connection_base import BaseConnection
from ._features import AsyncConnectionFeatures
from ._gentypes import RV, PQGen
from ._queries import Params, Query
from .conninfo import conninfo_to_dict, connect_timeout, make_conninfo
from .rows import RowFactory

if TYPE_CHECKING:
    from .cursor_async import AsyncCursor
    from .pq import PGconn
    from .transaction_async import AsyncTransaction


class AsyncConnection(AsyncConnectionFeatures, BaseConnection):
    """A connection to a database for asyncio: the twin of Connection."""

    __module__ = "stuntgres"

    def __init__(self, pgconn: PGconn) -> None:
        super().__init__(pgconn)
        self.lock = asyncio.Lock()

    @classmethod
    async def connect(
        cls,
        conninfo: str = "",
        *,
        autocommit: bool = False,
        row_factory: RowFactory | None = None,
        cursor_factory: type[AsyncCursor] | None = None,
        **kwargs: Any,
    ) -> AsyncConnection:
        """Connect and return the connection; no SQL is sent."""
        options = conninfo_to_dict(conninfo, **kwargs)
        timeout = connect_timeout(options)
        dsn = make_conninfo(**options)
        gen = cls._connect_gen(dsn, autocommit=autocommit)
        try:
            conn: AsyncConnection = await waiting_async.wait_conn_async(
                gen, timeout=timeout
            )
        except e.WaitTimeout:
            raise e.ConnectionTimeout("connection timeout expired") from None
        if row_factory is not None:
            conn.row_factory = row_factory
        if cursor_factory is not None:
            conn.cursor_factory = cursor_factory
        return conn

    async def __aenter__(self) -> AsyncConnection:
        return self

    async def __aexit__(
        self,
        exc_type: type[BaseException] | None,
        exc_val: BaseException | None,
        exc_tb: TracebackType | None,
    ) -> None:
        if self.closed:
            return
        try:
            if exc_type is None:
                await self.commit()
            else:
                try:
                    await self.rollback()
                except Exception as exc:
                    logger.warning("error ignored in rollback of %r: %s", self, exc)
        finally:
            await self.close()

    async def close(self) -> None:
        """Close the connection now; an open transaction is rolled back.

        Nothing is sent and no running statement is waited for. Closing twice
        is allowed.
        """
        if self._closed:
            return
        self._closed = True
        if self.lock.locked():
            # Another task is inside an operation: end its statement first.
            self._cancel_handle.cancel()
        async with self.lock:
            self._finish()

    async def wait(self, gen: PQGen[RV], interval: float | None = None) -> RV:
        """Drive a generator to its end without blocking the event loop.

        When the task is cancelled the running statement is cancelled and its
        outcome collected, so the connection stays usable; then the
        cancellation is raised again.
        """
        try:
            return await waiting_async.wait_async(
                gen, self._wait_fileno(), interval=interval
            )
        except (asyncio.CancelledError, KeyboardInterrupt):
            try:
                await waiting_async.wait_async(
                    self._cancel_gen(), self._wait_fileno(), timeout=CANCEL_TIMEOUT
                )
            except BaseException as exc:
                self._interrupt_failed(exc)
            raise
        finally:
            self._note_status()

    def cursor(
        self,
        name: str = "",
        *,
        row_factory: RowFactory | None = None,
        scrollable: bool | None = None,
        withhold: bool = False,
    ) -> AsyncCursor:
        """Return a new cursor; `name` asks for a server-side cursor."""
        self._check_closed()
        if row_factory is None:
            row_factory = self.row_factory
        if name:
            factory = getattr(self, "server_cursor_factory", None)
            if factory is None:
                raise e.NotSupportedError("named cursors are not available")
            cursor: AsyncCursor = factory(
                self,
                name,
                row_factory=row_factory,
                scrollable=scrollable,
                withhold=withhold,
            )
            return cursor
        factory = self.cursor_factory
        if factory is None:
            from .cursor_async import AsyncCursor

            factory = AsyncCursor
        cursor = factory(self, row_factory=row_factory)
        return cursor

    async def execute(self, query: Query, params: Params | None = None) -> AsyncCursor:
        """Run a statement on a new cursor and return that cursor."""
        return await self.cursor().execute(query, params)

    async def commit(self) -> None:
        """Commit the open transaction; does nothing when none is open."""
        async with self.lock:
            await self.wait(self._commit_gen())

    async def rollback(self) -> None:
        """Roll the open transaction back; does nothing when none is open."""
        async with self.lock:
            await self.wait(self._rollback_gen())

    def _assign(self, name: str, value: Any) -> None:
        raise AttributeError(
            f"'{name}' cannot be assigned on an async connection:"
            f" use 'await connection.set_{name}(value)'"
        )

    async def set_autocommit(self, value: bool) -> None:
        """Set autocommit; ProgrammingError inside a transaction."""
        async with self.lock:
            self._set_autocommit(value)

    async def set_isolation_level(self, value: IsolationLevel | None) -> None:
        """Set isolation_level; ProgrammingError inside a transaction."""
        async with self.lock:
            self._set_isolation_level(value)

    async def set_read_only(self, value: bool | None) -> None:
        """Set read_only; ProgrammingError inside a transaction."""
        async with self.lock:
            self._set_read_only(value)

    async def set_deferrable(self, value: bool | None) -> None:
        """Set deferrable; ProgrammingError inside a transaction."""
        async with self.lock:
            self._set_deferrable(value)

    def transaction(
        self, savepoint_name: str | None = None, force_rollback: bool = False
    ) -> AsyncTransaction:
        """Return an async context manager for a transaction or savepoint."""
        from .transaction_async import AsyncTransaction

        return AsyncTransaction(self, savepoint_name, force_rollback)

    async def notifies(
        self, *, timeout: float | None = None, stop_after: int | None = None
    ) -> AsyncIterator[Notify]:
        """Yield notifications as they arrive.

        Stops after `timeout` seconds (None: never) or after `stop_after`
        notifications (None: no limit). The connection is locked only while
        waiting, a tenth of a second at a time, so the loop body may use it
        and close() from another task is not kept waiting.
        """
        window = NotifyWindow(timeout, stop_after)
        backlog = self._notify_backlog
        while True:
            while backlog and not window.full():
                window.count += 1
                yield backlog.popleft()
            if window.full() or window.expired:
                return
            interval = window.interval()
            self._check_closed()
            async with self.lock:
                await self.wait(self._notifies_gen(), interval=interval)

    async def cancel_safe(self, *, timeout: float = 30.0) -> None:
        """Cancel the running statement; the same as cancel()."""
        self.cancel()
```

### 9.5 `AsyncCursor` (`stuntgres/cursor_async.py`)

| Member | Differs from `Cursor` in |
|---|---|
| `async execute(self, query: Query, params: Params \| None = None) -> AsyncCursor` | `async with self._conn.lock: await self._conn.wait(self._execute_gen(query, params))`; returns `self`, so `cur = await conn.execute(...)` and `await (await conn.execute(...)).fetchone()` work. |
| `async executemany(self, query: Query, params_seq: Iterable[Params], *, returning: bool = False) -> None` | The same with `_executemany_gen`. |
| `async fetchone(self) -> Any`, `async fetchmany(self, size: int \| None = None) -> list[Any]`, `async fetchall(self) -> list[Any]`, `async scroll(self, value: int, mode: str = "relative") -> None` | Coroutines that call the plain base method and never wait. |
| `__aiter__(self) -> AsyncCursor`, `async __anext__(self) -> Any` | `async for row in cur:`; `StopAsyncIteration` at the end. |
| `async close(self) -> None`, `async __aenter__(self) -> AsyncCursor`, `async __aexit__(self, exc_type, exc_val, exc_tb) -> None` | `async with conn.cursor() as cur:`. |
| `nextset()`, `setinputsizes()`, `setoutputsize()`, `lastrowid`, `description`, `rowcount`, `rownumber`, `statusmessage`, `arraysize`, `connection`, `adapters`, `row_factory`, `closed`, `pgresult` | Nothing: inherited; `nextset()` is a plain method here too. |

The complete file:

```python
"""The asyncio cursor."""
from __future__ import annotations

from types import TracebackType
from typing import TYPE_CHECKING, Any, Iterable

from ._cursor_base import BaseCursor
from ._features import AsyncCursorFeatures
from ._queries import Params, Query

if TYPE_CHECKING:
    from .connection_async import AsyncConnection


class AsyncCursor(AsyncCursorFeatures, BaseCursor):
    """A cursor for asyncio: the twin of Cursor."""

    __module__ = "stuntgres"

    _conn: AsyncConnection

    async def __aenter__(self) -> AsyncCursor:
        return self

    async def __aexit__(
        self,
        exc_type: type[BaseException] | None,
        exc_val: BaseException | None,
        exc_tb: TracebackType | None,
    ) -> None:
        await self.close()

    async def close(self) -> None:
        """Release the results; every later use raises InterfaceError."""
        self._close()

    async def execute(self, query: Query, params: Params | None = None) -> AsyncCursor:
        """Run a statement and return the cursor itself.

        Without `params` the string is sent as it is and may hold several
        statements; with them, %s and %(name)s are bound on the server.
        """
        async with self._conn.lock:
            await self._conn.wait(self._execute_gen(query, params))
        return self

    async def executemany(
        self, query: Query, params_seq: Iterable[Params], *, returning: bool = False
    ) -> None:
        """Run a statement once for every parameter set.

        rowcount becomes the sum over all sets. With `returning` every result
        is kept instead and nextset() walks them.
        """
        async with self._conn.lock:
            await self._conn.wait(self._executemany_gen(query, params_seq, returning))

    async def fetchone(self) -> Any:
        """Return the next row, or None when there is none left."""
        return self._fetchone()

    async def fetchmany(self, size: int | None = None) -> list[Any]:
        """Return the next `size` rows (default: arraysize); fewer at the end."""
        return self._fetchmany(size)

    async def fetchall(self) -> list[Any]:
        """Return all the rows that are left."""
        return self._fetchall()

    async def scroll(self, value: int, mode: str = "relative") -> None:
        """Move in the result; IndexError when the target is outside it."""
        self._scroll(value, mode)

    def __aiter__(self) -> AsyncCursor:
        return self

    async def __anext__(self) -> Any:
        row = self._fetchone()
        if row is None:
            raise StopAsyncIteration
        return row
```

### 9.6 `AsyncTransaction` (`stuntgres/transaction_async.py`)

`async __aenter__(self) -> AsyncTransaction`: `async with conn.lock: await
conn.wait(self._enter_gen())`; return `self`. `async __aexit__(self, exc_type, exc_val,
exc_tb) -> bool`: `async with conn.lock: return await conn.wait(self._exit_gen(exc_type,
exc_val))`. Everything else is `BaseTransaction` (5.4); `Rollback` is the same class for
both.

```python
"""Transaction blocks of the asyncio connection."""
from __future__ import annotations

from types import TracebackType
from typing import TYPE_CHECKING

from .transaction import BaseTransaction

if TYPE_CHECKING:
    from .connection_async import AsyncConnection


class AsyncTransaction(BaseTransaction):
    """The async context manager AsyncConnection.transaction() returns."""

    __module__ = "stuntgres"

    _conn: AsyncConnection

    async def __aenter__(self) -> AsyncTransaction:
        async with self._conn.lock:
            await self._conn.wait(self._enter_gen())
        return self

    async def __aexit__(
        self,
        exc_type: type[BaseException] | None,
        exc_val: BaseException | None,
        exc_tb: TracebackType | None,
    ) -> bool:
        async with self._conn.lock:
            return await self._conn.wait(self._exit_gen(exc_type, exc_val))
```

## 10. The compliance module

`tests/test_dbapi20_compliance.py` runs the vendored suite
`tests/third_party/dbapi20.py` (PQ 9.8: version 1.15.0, unchanged) against the driver.
The suite's class `DatabaseAPI20Test` is used as a mixin of the harness's
`DatabaseTestCase`, so every case gets a database of its own and the suite's
`tearDown` (which drops its two tables) has nothing to do:

- `driver = stuntgres`: the suite reads `connect`, `apilevel`, `threadsafety`,
  `paramstyle`, the exceptions, the type objects and the constructors from the module.
- `setUp` calls `super().setUp()` (which sets `self.conninfo`) and then sets
  `self.connect_args = (self.conninfo,)`; `connect_kw_args = {}`.
- `tearDown` is overridden to do nothing.
- The DDL helpers `executeDDL1` and `executeDDL2` are used as they are (`create table
  dbapi20test_booze (name varchar(20))`, `create table dbapi20test_barflys (name
  varchar(20), drink varchar(30))`): with `autocommit` off they run in the test's
  transaction, which is what the suite expects.
- The module imports `dbapi20` as a module (`from tests.third_party import dbapi20`) and
  subclasses `dbapi20.DatabaseAPI20Test`, so the test loader does not collect the base
  class a second time.

**The suite was executed** (experiment 7): imported and run on CPython 3.12.3 with
warnings as errors, in oracle mode and in server mode. This settles what PQ 9.8 had to
leave open (there the file had only been compiled and read): the vendored file imports
and runs cleanly on a current Python. Every case, as vendored and with the three
overrides **(measured; identical in both modes)**:

| Case | What it checks | Suite as vendored | With the overrides | Override |
|---|---|---|---|---|
| `test_BINARY` | the module has `BINARY` | passes | passes | no |
| `test_Binary` | `Binary(b'Something')`, `Binary(b'')` | passes | passes | no |
| `test_DATETIME` | the module has `DATETIME` | passes | passes | no |
| `test_Date` | `Date`, `DateFromTicks` | passes | passes | no |
| `test_Exceptions` | the hierarchy of the module's exceptions | passes | passes | no |
| `test_ExceptionsAsConnectionAttributes` | `con.Error is driver.Error` and the eight others | passes | passes | no |
| `test_NUMBER` | the module has `NUMBER` | passes | passes | no |
| `test_None` | SQL NULL is `None` | passes | passes | no |
| `test_ROWID` | the module has `ROWID` | passes | passes | no |
| `test_STRING` | the module has `STRING` | passes | passes | no |
| `test_Time` | `Time`, `TimeFromTicks` | passes | passes | no |
| `test_Timestamp` | `Timestamp`, `TimestampFromTicks` | passes | passes | no |
| `test_apilevel` | `apilevel == '2.0'` | passes | passes | no |
| `test_arraysize` | the cursor has `arraysize` | passes | passes | no |
| `test_callproc` | runs only when the cursor has `callproc`: it has none (6.9), the body is skipped and the case passes | passes | passes | no |
| `test_close` | after `con.close()`: `cur.execute` and `con.commit` raise `Error` | passes | passes | no |
| `test_commit` | `commit()` right after connect | passes | passes | no |
| `test_connect` | `connect(conninfo)` and `close()` | passes | passes | no |
| `test_cursor` | `cursor()` | passes | passes | no |
| `test_cursor_isolation` | two cursors of one connection see each other's uncommitted rows | passes | passes | no |
| `test_description` | `description` is `None` after DDL, has one 7-item entry after a query, name `name`, `type_code == STRING` | passes | passes | no |
| `test_execute` | a literal `%s` without parameters; `%(beer)s` with a mapping and `%%` for a literal percent sign | passes | passes | no |
| `test_executemany` | `executemany` with two mappings; `rowcount` 2 | passes | passes | no |
| `test_fetchall` | `fetchall` errors, all rows, then an empty list | passes | passes | no |
| `test_fetchmany` | `arraysize` 1 by default, explicit sizes, empty list at the end, errors before a query | passes | passes | no |
| `test_fetchone` | `fetchone` errors before a query and after DDL or an insert; `None` at the end | passes | passes | no |
| `test_mixedfetch` | `fetchone`, `fetchmany`, `fetchall` on one result | passes | passes | no |
| `test_nextset` | the suite's body needs a stored procedure returning two result sets and raises `NotImplementedError` | error: `NotImplementedError: Drivers need to override this test` | passes | yes |
| `test_non_idempotent_close` | the suite wants an `Error` from a second `close()` | fails: `AssertionError: Error not raised by close` | passes | yes |
| `test_paramstyle` | `paramstyle` is a PEP 249 style | passes | passes | no |
| `test_rollback` | `rollback()` right after connect | passes | passes | no |
| `test_rowcount` | `rowcount` -1 after DDL, 1 after an insert, 1 after a one-row query | passes | passes | no |
| `test_setinputsizes` | `setinputsizes((25,))` then statements still work | passes | passes | no |
| `test_setoutputsize` | the suite's body raises `NotImplementedError` (driver dependent) | error: `NotImplementedError: Driver needed to override this test` | passes | yes |
| `test_setoutputsize_basic` | `setoutputsize(1000)`, `setoutputsize(2000, 0)` then statements still work | passes | passes | no |
| `test_threadsafety` | `threadsafety` in 0..3 | passes | passes | no |

The three overrides, and why:

| Case | Why the vendored body cannot pass | The override |
|---|---|---|
| `test_non_idempotent_close` | It asserts that a second `close()` raises `Error`. The suite's own comment says "reasonable persons differ about the usefulness of this test and this feature"; here `close()` may be called any number of times (9.2), as in psycopg 3 and `sqlite3`. | Closes twice and asserts `con.closed`. |
| `test_nextset` | The suite defines the method twice; the second definition, the one that counts, raises `NotImplementedError('Drivers need to override this test')`, and the first relies on a stored procedure returning two result sets and on `callproc`. | The same assertions over a two-statement string: `select count(*) from dbapi20test_booze; select name from dbapi20test_booze` gives the count, `nextset()` is `True`, six names, `nextset()` is `None`. |
| `test_setoutputsize` | Raises `NotImplementedError('Driver needed to override this test')`. | Calls `setoutputsize(1000)` and `setoutputsize(2000, 0)` and checks that a query still works. |

`test_callproc` needs no override: it runs its body only when the cursor has `callproc`
(6.9: it has none).

The complete file:

```python
"""The vendored DB-API 2.0 compliance suite, run against stuntgres."""
from __future__ import annotations

import stuntgres
from tests import support
from tests.third_party import dbapi20


class ComplianceTest(support.DatabaseTestCase, dbapi20.DatabaseAPI20Test):
    """Every case of the suite, on a database of its own."""

    driver = stuntgres
    connect_kw_args: dict[str, object] = {}

    def setUp(self) -> None:
        super().setUp()
        self.connect_args = (self.conninfo,)

    def tearDown(self) -> None:
        """Nothing to drop: the database belongs to this test alone."""

    def test_non_idempotent_close(self) -> None:
        """Override: close() may be called twice (the suite wants an Error)."""
        con = self._connect()
        con.close()
        con.close()
        self.assertTrue(con.closed)

    def test_nextset(self) -> None:
        """Override: the suite's helper needs a procedure with two result sets."""
        con = self._connect()
        try:
            cur = con.cursor()
            self.executeDDL1(cur)
            for sql in self._populate():
                cur.execute(sql)
            cur.execute(
                "select count(*) from %sbooze; select name from %sbooze"
                % (self.table_prefix, self.table_prefix)
            )
            self.assertEqual(cur.fetchone(), (len(self.samples),))
            self.assertIs(cur.nextset(), True)
            self.assertEqual(len(cur.fetchall()), len(self.samples))
            self.assertIsNone(cur.nextset())
        finally:
            con.close()

    def test_setoutputsize(self) -> None:
        """Override: setoutputsize does nothing in this driver."""
        con = self._connect()
        try:
            cur = con.cursor()
            cur.setoutputsize(1000)
            cur.setoutputsize(2000, 0)
            cur.execute("select 'x'")
            self.assertEqual(cur.fetchone(), ("x",))
        finally:
            con.close()
```

## 11. Steps

Each step is one catalog part. A part edits only its own files (rule C2), writes its
tests first (C7) and must pass `--mode unit`, `--mode oracle` and `--mode server` of the
runner (PQ 9.1). All paths are relative to `bindings/cpython/`. The files printed in
sections 2 to 10 and the test files printed below are complete: they ran, exactly as
printed, in unit, oracle and server mode (section 12).

### 11.1 Parts

Every part also has `CPYTHON-DESIGN-DBAPI` as a prerequisite. Sizes are lines of code +
lines of tests (stubs count as code, test helpers as tests). `CPYTHON-` is omitted
everywhere in the table; prerequisites that are parts of PQ 10.1 or ADAPT 8.1 are in
italics.

| Part | Files | Public names | Prerequisites | Size |
|---|---|---|---|---|
| ERRORS-02-SQLSTATE | `tools/sqlstates.txt`, `tools/gen_sqlstates.py`, `stuntgres/_sqlstates.py`, `tests/test_sqlstates.py` | `_sqlstates.lookup`, `_sqlstates.class_for`, 253 classes installed in `stuntgres.errors`, `errors.lookup`; tool `read_table`, `class_name`, `render`, `main` | *PQ-01-ERRORS-BASE* | 175 + 200 (plus 301 lines of table and 384 generated lines) |
| ERRORS-01-DIAGNOSTIC | `stuntgres/_diagnostic.py`, `tests/test_diagnostic.py` | `ErrorInfo`, `Diagnostic` (also `errors.Diagnostic`), `error_from_result` | ERRORS-02-SQLSTATE, *PQ-08-EXEC* | 130 + 305 |
| ROWS-01-FACTORIES | `stuntgres/rows.py`, `tests/test_rows.py` | `RowMaker`, `RowFactory`, `tuple_row`, `dict_row`, `namedtuple_row` | *BUILD-01-SKELETON* | 60 + 70 |
| CONN-01A-CONNINFO | `stuntgres/conninfo.py`, `tests/test_conninfo.py` | `make_conninfo`, `conninfo_to_dict`, `connect_timeout` | *PQ-05-CONNINFO* | 85 + 120 |
| CONN-01B-INFO | `stuntgres/_connection_info.py`, `tests/test_connection_info.py` | `ConnectionInfo` | CONN-01A-CONNINFO, *PQ-08-EXEC* | 110 + 95 |
| CONN-01C-CORE | `stuntgres/_conn_core.py`, `tests/test_conn_core.py` | `IsolationLevel`, `Notify`, `NotifyWindow`, `CANCEL_TIMEOUT`, `NOTIFY_INTERVAL`, `ConnCore` | CONN-01B-INFO, ROWS-01-FACTORIES, *PQ-16-GENERATORS*, *PQ-17-WAIT-SYNC*, *ADAPT-01A-MAP* | 245 + 200 |
| CONN-01D-BASE | `stuntgres/_features.py`, `stuntgres/_connection_base.py`, `tests/test_connection_base.py`; stubs: `stuntgres/_conn_exec.py`, `stuntgres/_conn_txnopts.py`, `stuntgres/_conn_notices.py`, `stuntgres/_conn_notify.py`, `stuntgres/_conn_cancel.py` | `BaseConnection`; the six classes of `_features` | CONN-01C-CORE | 95 + 95 |
| CONN-01E-SYNC | `stuntgres/connection.py`, `tests/test_connection.py` | `Connection` | CONN-01D-BASE, *QUERY-01-CONVERT* | 220 + 225 |
| CONN-02-EXEC | `stuntgres/_conn_exec.py`, `tests/dbapi_util.py`, `tests/test_conn_exec.py` | `ExecMixin`: `autocommit`, `_set_autocommit`, `_exec_command`, `_start_query`, `_commit_gen`, `_rollback_gen`; test helpers `run`, `status`, `ConnTestCase` | CONN-01E-SYNC, ERRORS-01-DIAGNOSTIC | 90 + 290 |
| CONN-03-TXN-OPTIONS | `stuntgres/_conn_txnopts.py`, `tests/test_conn_txnopts.py` | `TxnOptionsMixin`: the three properties, `_set_isolation_level`, `_set_read_only`, `_set_deferrable`, `_begin_statement` | CONN-02-EXEC | 65 + 150 |
| CONN-04-TRANSACTION | `stuntgres/transaction.py`, `tests/test_transaction.py` | `Rollback`, `BaseTransaction`, `Transaction`, `quote_identifier` | CONN-03-TXN-OPTIONS | 155 + 280 |
| CONN-05-NOTICES | `stuntgres/_conn_notices.py`, `tests/test_conn_notices.py` | `NoticesMixin`: `add_notice_handler`, `remove_notice_handler`, `_on_notice` | CONN-02-EXEC | 45 + 145 |
| CONN-06-NOTIFY | `stuntgres/_conn_notify.py`, `tests/test_conn_notify.py` | `NotifyMixin`: `add_notify_handler`, `remove_notify_handler`, `_on_notify`, `_notifies_gen` | CONN-02-EXEC | 55 + 175 |
| CONN-07-CANCEL | `stuntgres/_conn_cancel.py`, `tests/test_conn_cancel.py` | `CancelMixin`: `cancel`, `_cancel_gen`, `_interrupt_failed` | CONN-02-EXEC | 35 + 175 |
| CUR-01A-COLUMN | `stuntgres/_column.py`, `tests/test_column.py` | `Column` | *PQ-08-EXEC*, *ADAPT-10-DBAPI20-TYPES* | 95 + 180 |
| CUR-01B-RESULTS | `stuntgres/_cur_results.py`, `tests/test_cur_results.py` | `check_results`, `end_copy` | ERRORS-01-DIAGNOSTIC, *PQ-13-COPY*, *PQ-17-WAIT-SYNC* | 70 + 105 |
| CUR-01C-CORE | `stuntgres/_cur_core.py`, `stuntgres/_cursor_base.py`, `stuntgres/cursor.py`, `tests/test_cursor_core.py`; stubs: `stuntgres/_cur_fetch.py`, `stuntgres/_cur_many.py`, `stuntgres/_cur_nextset.py`, `stuntgres/_cur_optional.py` | `CurCore`, `BaseCursor`, `Cursor` | CUR-01A-COLUMN, CUR-01B-RESULTS, CONN-02-EXEC, *ADAPT-11-DEFAULTS* | 305 + 140 |
| CUR-02-FETCH | `stuntgres/_cur_fetch.py`, `tests/test_cursor_fetch.py` | `FetchMixin`: `rownumber`, `_fetchone`, `_fetchmany`, `_fetchall`, `_scroll` | CUR-01C-CORE | 70 + 230 |
| CUR-01D-EXECUTE-TESTS | `tests/test_cursor_execute.py` | - | CUR-02-FETCH, *ENGINE-04-START-TRANSACTION-TAG*, *ENGINE-05-ARRAY-CAST-COLUMN-NAME* | 0 + 370 |
| CUR-04-NEXTSET | `stuntgres/_cur_nextset.py`, `tests/test_cursor_nextset.py` | `NextsetMixin`: `nextset` | CUR-02-FETCH | 20 + 145 |
| CUR-03-EXECUTEMANY | `stuntgres/_cur_many.py`, `tests/test_cursor_executemany.py` | `ManyMixin`: `_executemany_gen` | CUR-04-NEXTSET | 50 + 150 |
| CUR-05-OPTIONAL | `stuntgres/_cur_optional.py`, `tests/test_cursor_optional.py` | `OptionalMixin`: `setinputsizes`, `setoutputsize`, `lastrowid` | CUR-02-FETCH | 20 + 45 |
| ASYNC-01-CONNECTION | `stuntgres/connection_async.py`, `tests/dbapi_util_async.py`, `tests/test_async_connection.py` | `AsyncConnection`; test helpers `arun`, `AsyncConnTestCase` | CONN-03-TXN-OPTIONS, *PQ-18-WAIT-ASYNC* | 225 + 195 |
| ASYNC-02-CURSOR | `stuntgres/cursor_async.py`, `tests/test_async_cursor.py` | `AsyncCursor` | ASYNC-01-CONNECTION, CUR-03-EXECUTEMANY, CUR-05-OPTIONAL | 80 + 170 |
| ASYNC-03-TRANSACTION | `stuntgres/transaction_async.py`, `tests/test_async_transaction.py` | `AsyncTransaction` | ASYNC-02-CURSOR, CONN-04-TRANSACTION, CONN-05-NOTICES, CONN-06-NOTIFY, CONN-07-CANCEL | 30 + 345 |
| MODULE-01-EXPORTS | `stuntgres/__init__.py`, `tests/test_module.py` | `connect`, `apilevel`, `threadsafety`, `paramstyle`, the export list | ASYNC-03-TRANSACTION, *ADAPT-10-DBAPI20-TYPES*, *ADAPT-11-DEFAULTS* | 65 + 175 |
| TEST-04-DBAPI20 | `tests/test_dbapi20_compliance.py` | `ComplianceTest` | MODULE-01-EXPORTS, *TEST-02-DBAPI20-VENDOR* | 0 + 60 |
| ENGINE-04-START-TRANSACTION-TAG | engine and reference test files, named by the part | - | - | 5 + 40 |
| ENGINE-05-ARRAY-CAST-COLUMN-NAME | engine and reference test files, named by the part | - | - | 15 + 60 |

Changes against the list in the part record, with the reason:

- `CPYTHON-CONN-01-CONNECT` is five parts: `-01A-CONNINFO` (connection strings),
  `-01B-INFO` (`ConnectionInfo`), `-01C-CORE` (state and life cycle), `-01D-BASE` (the
  composed class, its five mixin stubs and the features stub) and `-01E-SYNC`
  (`Connection`). Together they are about 1500 lines.
- `CPYTHON-CUR-01-EXECUTE` is four parts: `-01A-COLUMN`, `-01B-RESULTS` (the result
  check and the refusal of COPY), `-01C-CORE` (state, `execute`, the composed class, its
  four stubs and `Cursor`) and the test-only part `-01D-EXECUTE-TESTS`, which comes after
  `CPYTHON-CUR-02-FETCH` so that it can read rows with the fetch methods.
- `CPYTHON-ERRORS-01-DIAGNOSTIC` depends on `CPYTHON-ERRORS-02-SQLSTATE`, not the other
  way round: converting a result needs the class of its SQLSTATE.
- `CPYTHON-CUR-03-EXECUTEMANY` depends on `CPYTHON-CUR-04-NEXTSET`: its `returning` tests
  walk the kept results with `nextset()`.
- `CPYTHON-ASYNC-01-CONNECTION` needs only the command path and the transaction options
  of the synchronous side; `CPYTHON-ASYNC-03-TRANSACTION` comes after the synchronous
  transaction, notice, notify and cancel parts, whose base-class code it exercises.
- `CPYTHON-MODULE-01-EXPORTS` depends on every part that adds a module (all of them are
  in the prerequisite closure of `CPYTHON-ASYNC-03-TRANSACTION`); it does not wait for
  the test-only part `-01D-EXECUTE-TESTS`, the one part that needs the two engine fixes.
- Two engine parts are added (rule D8, 1.1): `CPYTHON-ENGINE-04-START-TRANSACTION-TAG`
  and `CPYTHON-ENGINE-05-ARRAY-CAST-COLUMN-NAME`. Their two assertions were put into the
  test-only part on purpose, so that an engine fix that has to wait blocks nothing else.

Parts that may run side by side never share a file. After `CPYTHON-CONN-02-EXEC`, the
parts `CONN-03`, `CONN-05`, `CONN-06`, `CONN-07` and `CUR-01C` each own their files;
after `CPYTHON-CUR-02-FETCH`, the parts `CUR-01D`, `CUR-04` and `CUR-05` do;
`ERRORS-02`, `ROWS-01`, `CONN-01A` and `CUR-01A` can start as soon as their PQ and ADAPT
prerequisites are done. The stage check of section 12 built, for every part, a tree with
exactly the files of its prerequisite closure and ran the part's tests there.

### 11.2 What "the test fails first" means here

Rule C7 asks that the tests of a part fail before its code exists. For a part that
creates a module that is an import error; for a part that replaces a stub it is an
`AttributeError` from the stub; both were observed (the stage check with `--pre`) and
are stated per part below. The two test-only parts have nothing that fails first: their
tests must pass as written, and a failure is to be reported, not repaired in the test.

Rule C5 for the two parts whose prerequisites do not include the test backends
(`CPYTHON-ERRORS-02-SQLSTATE`, `CPYTHON-ROWS-01-FACTORIES`): their tests need no
database, so `--mode unit` proves them. If `tests/support/oracle.py` does not exist yet,
`--mode oracle` exits 3 with `runtests: cannot start oracle mode: ...`; that outcome
satisfies C5 for these two parts and is recorded in the commit body. If it exists, the
mode must pass. The same holds for `--mode server`.

A selector that names only database tests exits 4 (`no tests selected`) in unit mode
(PQ 9.1); the acceptance of such a part therefore runs the whole suite in unit mode and
the part's own selector in the two database modes.

### 11.3 What each part does

For every part: the files, the sections to implement exactly, the step, why the tests
fail before the code exists, and the test file to write first. "As printed" means the
complete file printed in the cited section, character for character; where a table and a
printed file disagree, stop and report (rule C1). A part that replaces a stub replaces
the whole file.

Notes for every test file: literals with a backslash are Python source, exactly as
printed; non-ASCII characters are written as `\u` escapes; a test class that derives
from `unittest.TestCase` runs in every mode, one that derives from
`support.DatabaseTestCase` (directly, or through `tests.dbapi_util.ConnTestCase` or
`tests.dbapi_util_async.AsyncConnTestCase`) needs a database and is dropped in unit mode;
tests marked `@support.only_modes("oracle", "server")` assert libpq-over-a-socket
behavior and are skipped in native mode. No test sleeps: waits are on descriptors, on
events set by the code under test, or bounded by a deadline.

#### CPYTHON-ERRORS-02-SQLSTATE

Files: `tools/sqlstates.txt`, `tools/gen_sqlstates.py`, `stuntgres/_sqlstates.py`, `tests/test_sqlstates.py`. Design sections to implement: 3.1, 3.2, 3.3; also read: 1.4.

Step. 1. Create `tools/sqlstates.txt` with exactly the content printed in 3.2 (301 lines) and check its SHA-256 against the value given there (`sha256sum bindings/cpython/tools/sqlstates.txt`). 2. Write `tools/gen_sqlstates.py` as printed in 3.3. 3. Run `python3 bindings/cpython/tools/gen_sqlstates.py`; it writes `stuntgres/_sqlstates.py` (384 lines); check the SHA-256 given in 3.3 and that `--check` exits 0. Never write or edit the generated module by hand. 4. The tool and the table live in `tools/`, which is not a package: the test loads the tool with `importlib` from its path.

Before the code exists: `ModuleNotFoundError: No module named 'stuntgres._sqlstates'`.

Tests to write first (12 unit cases; no database case (the database modes of the runner: see 11.2)):

`tests/test_sqlstates.py`:

```python
"""Tests of the generated SQLSTATE classes and of the tool that writes them."""
from __future__ import annotations

import contextlib
import importlib.util
import io
import os
import pickle
import tempfile
import unittest
from typing import Any

from stuntgres import _sqlstates
from stuntgres import errors as e

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def load_tool() -> Any:
    """Import tools/gen_sqlstates.py, which is a script and not in a package."""
    path = os.path.join(ROOT, "tools", "gen_sqlstates.py")
    spec = importlib.util.spec_from_file_location("gen_sqlstates", path)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class SqlstateClassesTest(unittest.TestCase):
    def test_counts(self) -> None:
        self.assertEqual(len(_sqlstates._CODES), 253)
        self.assertEqual(len(_sqlstates._BY_CODE), 253)
        self.assertEqual(len(_sqlstates._BASES), 41)
        names = [name for _code, name in _sqlstates._CODES]
        self.assertEqual(len(set(names)), 253)

    def test_lookup(self) -> None:
        cls = e.lookup("23505")  # type: ignore[attr-defined]
        self.assertIs(cls, _sqlstates.lookup("23505"))
        self.assertEqual(cls.__name__, "UniqueViolation")
        self.assertEqual(cls.__module__, "stuntgres.errors")
        self.assertEqual(cls.__doc__, "The server reported SQLSTATE 23505.")
        self.assertEqual(cls.sqlstate, "23505")
        self.assertIs(cls, e.UniqueViolation)  # type: ignore[attr-defined]
        self.assertIs(_sqlstates.lookup("42p01"), _sqlstates.lookup("42P01"))
        with self.assertRaises(KeyError):
            _sqlstates.lookup("99999")
        with self.assertRaises(KeyError):
            _sqlstates.lookup("00000")
        with self.assertRaises(KeyError):
            _sqlstates.lookup("01000")

    def test_names_and_bases(self) -> None:
        expected = {
            "02000": ("NoData", e.DatabaseError),
            "08006": ("ConnectionFailure", e.OperationalError),
            "0A000": ("FeatureNotSupported", e.NotSupportedError),
            "21000": ("CardinalityViolation", e.ProgrammingError),
            "22012": ("DivisionByZero", e.DataError),
            "22001": ("StringDataRightTruncation", e.DataError),
            "22004": ("NullValueNotAllowed", e.DataError),
            "23503": ("ForeignKeyViolation", e.IntegrityError),
            "23505": ("UniqueViolation", e.IntegrityError),
            "25P02": ("InFailedSqlTransaction", e.InternalError),
            "2F002": ("ModifyingSqlDataNotPermitted", e.OperationalError),
            "38002": ("ModifyingSqlDataNotPermittedExt", e.OperationalError),
            "39004": ("NullValueNotAllowedExt", e.OperationalError),
            "3B001": ("InvalidSavepointSpecification", e.OperationalError),
            "40001": ("SerializationFailure", e.OperationalError),
            "40P01": ("DeadlockDetected", e.OperationalError),
            "42601": ("SyntaxError", e.ProgrammingError),
            "42P01": ("UndefinedTable", e.ProgrammingError),
            "42883": ("UndefinedFunction", e.ProgrammingError),
            "53300": ("TooManyConnections", e.OperationalError),
            "57014": ("QueryCanceled", e.OperationalError),
            "57P01": ("AdminShutdown", e.OperationalError),
            "F0000": ("ConfigFileError", e.OperationalError),
            "HV000": ("FdwError", e.OperationalError),
            "P0001": ("RaiseException", e.ProgrammingError),
            "XX000": ("InternalError_", e.InternalError),
            "XX001": ("DataCorrupted", e.InternalError),
        }
        for code, (name, base) in expected.items():
            cls = _sqlstates.lookup(code)
            self.assertEqual(cls.__name__, name, code)
            self.assertIs(cls.__bases__[0], base, code)
            self.assertIs(getattr(e, name), cls, code)

    def test_base_classes_are_not_replaced(self) -> None:
        self.assertEqual(e.InternalError.__module__, "stuntgres")
        self.assertIsNone(e.InternalError.sqlstate)
        self.assertTrue(issubclass(e.InternalError_, e.InternalError))  # type: ignore

    def test_class_for(self) -> None:
        class_for = _sqlstates.class_for
        self.assertIs(class_for("23505"), _sqlstates.lookup("23505"))
        self.assertIs(class_for("23999"), e.IntegrityError)
        self.assertIs(class_for("42ZZZ"), e.ProgrammingError)
        self.assertIs(class_for("XX999"), e.InternalError)
        self.assertIs(class_for("P0999"), e.ProgrammingError)
        self.assertIs(class_for("ZZ001"), e.DatabaseError)
        self.assertIs(class_for("00000"), e.DatabaseError)
        self.assertIs(class_for(""), e.DatabaseError)
        self.assertIs(class_for(None), e.DatabaseError)

    def test_instances(self) -> None:
        error = e.UniqueViolation("boom")  # type: ignore[attr-defined]
        self.assertIsInstance(error, e.IntegrityError)
        self.assertIsInstance(error, e.DatabaseError)
        self.assertEqual(str(error), "boom")
        self.assertEqual(error.sqlstate, "23505")
        self.assertIsNone(error.pgresult)
        copy = pickle.loads(pickle.dumps(error))
        self.assertIs(type(copy), type(error))
        self.assertEqual(copy.args, ("boom",))


class GeneratorToolTest(unittest.TestCase):
    def test_module_is_what_the_tool_writes(self) -> None:
        tool = load_tool()
        with open(tool.TARGET, encoding="ascii") as fh:
            on_disk = fh.read()
        self.assertEqual(tool.render(tool.read_table()), on_disk)
        self.assertEqual(tool.main(["--check"]), 0)

    def test_table(self) -> None:
        tool = load_tool()
        classes = tool.read_table()
        self.assertEqual(len(classes), 41)
        self.assertEqual(sum(len(cls.codes) for cls in classes), 253)
        first = classes[0]
        self.assertEqual((first.prefix, first.base, first.title), (
            "02", "DatabaseError", "No Data",
        ))
        self.assertEqual(first.codes[0], ("02000", "no_data"))
        last = classes[-1]
        self.assertEqual((last.prefix, last.base, last.title), (
            "XX", "InternalError", "Internal Error",
        ))
        self.assertEqual(last.codes[-1], ("XX002", "index_corrupted"))

    def test_class_name(self) -> None:
        tool = load_tool()
        taken: set[str] = set()
        self.assertEqual(tool.class_name("unique_violation", taken), "UniqueViolation")
        self.assertEqual(
            tool.class_name("sqlclient_unable_to_establish_sqlconnection", taken),
            "SqlclientUnableToEstablishSqlconnection",
        )
        self.assertEqual(tool.class_name("internal_error", taken), "InternalError_")
        self.assertEqual(
            tool.class_name("unique_violation", taken), "UniqueViolationExt"
        )
        with self.assertRaises(ValueError) as caught:
            tool.class_name("unique_violation", taken)
        self.assertEqual(
            str(caught.exception),
            "no free class name for the condition unique_violation",
        )

    def test_bad_tables(self) -> None:
        tool = load_tool()
        cases = {
            "class 08 Nope Title\n": "t.txt:3: bad class line: class 08 Nope Title",
            "08000 a\n": "t.txt:3: bad code line: 08000 a",
            "class 08 OperationalError T\n0800 a\n": "t.txt:4: bad code line: 0800 a",
            "class 08 OperationalError T\n09000 a\n":
                "t.txt:4: misplaced or repeated code: 09000 a",
            "class 08 OperationalError T\n08000 a\n08000 b\n":
                "t.txt:5: misplaced or repeated code: 08000 b",
        }
        with tempfile.TemporaryDirectory() as directory:
            path = os.path.join(directory, "t.txt")
            for text, message in cases.items():
                with open(path, "w", encoding="ascii") as fh:
                    fh.write("# a comment line\n\n" + text)
                with self.assertRaises(ValueError) as caught:
                    tool.read_table(path)
                self.assertEqual(str(caught.exception), message)

    def test_render_small_table(self) -> None:
        tool = load_tool()
        classes = [tool.SqlClass("23", "IntegrityError", "Integrity", [
            ("23505", "unique_violation"),
        ])]
        text = tool.render(classes)
        self.assertIn('    "23": e.IntegrityError,\n', text)
        self.assertIn(
            '    # Class 23 - Integrity\n    ("23505", "UniqueViolation"),\n', text
        )
        self.assertTrue(text.startswith('"""Exception classes named after'))
        self.assertTrue(text.endswith("_install()\n"))

    def test_usage(self) -> None:
        tool = load_tool()
        stderr = io.StringIO()
        with contextlib.redirect_stderr(stderr):
            self.assertEqual(tool.main(["--nope"]), 2)
        self.assertEqual(stderr.getvalue(), "usage: gen_sqlstates.py [--check]\n")
```

#### CPYTHON-ERRORS-01-DIAGNOSTIC

Files: `stuntgres/_diagnostic.py`, `tests/test_diagnostic.py`. Design sections to implement: 3.4, 3.5; also read: 3.1, 3.3.

Step. Write `stuntgres/_diagnostic.py` as printed in 3.4. The two assignments at its end (`e._diag_factory`, `e.Diagnostic`) are how `stuntgres/errors.py` is extended without being edited.

Before the code exists: `ModuleNotFoundError: No module named 'stuntgres._diagnostic'`.

Tests to write first (16 cases in oracle and in server mode, of which 10 also run in unit mode):

`tests/test_diagnostic.py`:

```python
"""Tests of the diagnostics object and of the conversion of error results."""
from __future__ import annotations

import os
import pickle
import socket
import unittest
from typing import Any

from stuntgres import _sqlstates
from stuntgres import errors as e
from stuntgres import pq
from stuntgres._diagnostic import Diagnostic, ErrorInfo, error_from_result
from tests import support

F = pq.DiagnosticField
NAMES = (
    "severity", "severity_nonlocalized", "sqlstate", "message_primary",
    "message_detail", "message_hint", "statement_position", "internal_position",
    "internal_query", "context", "schema_name", "table_name", "column_name",
    "datatype_name", "constraint_name", "source_file", "source_line",
    "source_function",
)


class FakeResult:
    """What ErrorInfo.from_result reads of a pq.PGresult."""

    def __init__(self, fields: dict[int, bytes], message: bytes) -> None:
        self.fields = fields
        self.error_message = message

    def error_field(self, fieldcode: int) -> bytes | None:
        return self.fields.get(int(fieldcode))


def fake(sqlstate: bytes | None, message: bytes, **more: bytes) -> Any:
    fields = {int(F.SEVERITY): b"ERROR", int(F.MESSAGE_PRIMARY): b"boom"}
    if sqlstate is not None:
        fields[int(F.SQLSTATE)] = sqlstate
    for name, value in more.items():
        fields[int(F[name])] = value
    return FakeResult(fields, message)


class DiagnosticUnitTest(unittest.TestCase):
    def test_empty(self) -> None:
        diag = Diagnostic()
        for name in NAMES:
            self.assertIsNone(getattr(diag, name), name)

    def test_every_field(self) -> None:
        fields = {int(code): code.name.lower().encode() for code in F}
        diag = Diagnostic(ErrorInfo(fields, b""))
        self.assertEqual(len(F), 18)
        for name in NAMES:
            self.assertEqual(getattr(diag, name), name, name)

    def test_fields_are_decoded(self) -> None:
        fields = {int(F.MESSAGE_PRIMARY): b"za\xbf\xf3\xb3\xe6"}
        info = ErrorInfo(fields, b"", "iso8859-2")
        self.assertEqual(
            Diagnostic(info).message_primary, "za\u017c\u00f3\u0142\u0107"
        )
        info = ErrorInfo({int(F.MESSAGE_PRIMARY): b"\xff"}, b"")
        self.assertEqual(Diagnostic(info).message_primary, "\ufffd")

    def test_module(self) -> None:
        self.assertEqual(Diagnostic.__module__, "stuntgres.errors")
        self.assertIs(e.Diagnostic, Diagnostic)  # type: ignore[attr-defined]
        self.assertIsNotNone(e._diag_factory)

    def test_error_class_and_text(self) -> None:
        error = error_from_result(fake(b"23505", b"ERROR:  boom\nDETAIL:  d\n"))
        self.assertIs(type(error), _sqlstates.lookup("23505"))
        self.assertEqual(str(error), "boom\nDETAIL:  d")
        self.assertEqual(error.sqlstate, "23505")
        self.assertNotIn("sqlstate", vars(error))
        self.assertEqual(error.diag.sqlstate, "23505")
        self.assertEqual(error.diag.message_primary, "boom")
        self.assertIsNone(error.pgconn)

    def test_unknown_sqlstate(self) -> None:
        error = error_from_result(fake(b"23999", b"ERROR:  boom\n"))
        self.assertIs(type(error), e.IntegrityError)
        self.assertEqual(error.sqlstate, "23999")
        self.assertIsNone(e.IntegrityError.sqlstate)
        error = error_from_result(fake(b"ZZ123", b"ERROR:  boom\n"))
        self.assertIs(type(error), e.DatabaseError)
        self.assertEqual(error.sqlstate, "ZZ123")

    def test_no_sqlstate_is_a_client_side_failure(self) -> None:
        result = FakeResult({}, b"server closed the connection unexpectedly\n")
        error = error_from_result(result)  # type: ignore[arg-type]
        self.assertIs(type(error), e.OperationalError)
        self.assertEqual(str(error), "server closed the connection unexpectedly")
        self.assertIsNone(error.sqlstate)
        self.assertIsNone(error.diag.sqlstate)

    def test_text(self) -> None:
        self.assertEqual(
            str(error_from_result(fake(b"22012", b""))), "no error details available"
        )
        fatal = fake(b"57P01", b"FATAL:  bye\n", SEVERITY=b"FATAL")
        self.assertEqual(str(error_from_result(fatal)), "bye")
        other = fake(b"22012", b"FEHLER:  boom\n")
        self.assertEqual(str(error_from_result(other)), "FEHLER:  boom")
        latin = fake(b"22012", b"ERROR:  \xe9\n")
        self.assertEqual(str(error_from_result(latin, "iso8859-1")), "\u00e9")

    def test_error_without_result(self) -> None:
        error = e.OperationalError("lost")
        self.assertIsInstance(error.diag, Diagnostic)
        self.assertIsNone(error.diag.sqlstate)
        self.assertIsNone(error.diag.message_primary)

    def test_pickle(self) -> None:
        error = error_from_result(
            fake(b"23505", b"ERROR:  boom\n", CONSTRAINT_NAME=b"c1")
        )
        copy = pickle.loads(pickle.dumps(error))
        self.assertIs(type(copy), _sqlstates.lookup("23505"))
        self.assertEqual(str(copy), "boom")
        self.assertEqual(copy.sqlstate, "23505")
        self.assertEqual(copy.diag.constraint_name, "c1")
        self.assertEqual(copy.diag.severity, "ERROR")
        self.assertIsInstance(copy.pgresult, ErrorInfo)


class DiagnosticTest(support.DatabaseTestCase):
    def setUp(self) -> None:
        super().setUp()
        self.pgconn = pq.PGconn.connect(self.conninfo.encode())
        self.addCleanup(self.pgconn.finish)
        self.assertEqual(self.pgconn.status, 0, self.pgconn.error_message)

    def run_sql(self, sql: str) -> None:
        result = self.pgconn.exec_(sql.encode())
        self.assertEqual(result.status, pq.ExecStatus.COMMAND_OK, result.error_message)

    def error(self, sql: str) -> e.Error:
        result = self.pgconn.exec_(sql.encode())
        self.assertEqual(result.status, pq.ExecStatus.FATAL_ERROR)
        error = error_from_result(result)
        result.clear()
        return error

    def check(self, error: e.Error, name: str, base: type, text: str) -> Any:
        self.assertEqual(type(error).__name__, name)
        self.assertIsInstance(error, base)
        self.assertEqual(str(error), text)
        return error.diag

    def test_constraint_errors(self) -> None:
        self.run_sql(
            "create table t (id int primary key, v varchar(3) not null,"
            " n int check (n > 0))"
        )
        self.run_sql("insert into t values (1, 'a', 1)")
        diag = self.check(
            self.error("insert into t values (1, 'b', 1)"),
            "UniqueViolation", e.IntegrityError,
            'duplicate key value violates unique constraint "t_pkey"\n'
            "DETAIL:  Key (id)=(1) already exists.",
        )
        self.assertEqual(diag.severity, "ERROR")
        self.assertEqual(diag.severity_nonlocalized, "ERROR")
        self.assertEqual(diag.sqlstate, "23505")
        self.assertEqual(
            diag.message_primary,
            'duplicate key value violates unique constraint "t_pkey"',
        )
        self.assertEqual(diag.message_detail, "Key (id)=(1) already exists.")
        self.assertEqual(diag.schema_name, "public")
        self.assertEqual(diag.table_name, "t")
        self.assertEqual(diag.constraint_name, "t_pkey")
        self.assertIsNone(diag.column_name)
        self.assertIsNone(diag.message_hint)
        self.assertIsNone(diag.statement_position)
        diag = self.check(
            self.error("insert into t values (2, null, 1)"),
            "NotNullViolation", e.IntegrityError,
            'null value in column "v" of relation "t" violates not-null constraint\n'
            "DETAIL:  Failing row contains (2, null, 1).",
        )
        self.assertEqual(diag.column_name, "v")
        self.assertEqual(diag.table_name, "t")
        diag = self.check(
            self.error("insert into t values (2, 'b', 0)"),
            "CheckViolation", e.IntegrityError,
            'new row for relation "t" violates check constraint "t_n_check"\n'
            "DETAIL:  Failing row contains (2, b, 0).",
        )
        self.assertEqual(diag.constraint_name, "t_n_check")
        self.check(
            self.error("insert into t values (2, 'abcd', 1)"),
            "StringDataRightTruncation", e.DataError,
            "value too long for type character varying(3)",
        )

    def test_statement_errors(self) -> None:
        diag = self.check(
            self.error("select * from nosuch"), "UndefinedTable", e.ProgrammingError,
            'relation "nosuch" does not exist\n'
            "LINE 1: select * from nosuch\n"
            "                      ^",
        )
        self.assertEqual(diag.statement_position, "15")
        diag = self.check(
            self.error("selec 1"), "SyntaxError", e.ProgrammingError,
            'syntax error at or near "selec"\nLINE 1: selec 1\n        ^',
        )
        self.assertEqual(diag.statement_position, "1")
        self.check(
            self.error("select 1/0"), "DivisionByZero", e.DataError, "division by zero"
        )
        diag = self.check(
            self.error("select nosuch(1)"), "UndefinedFunction", e.ProgrammingError,
            "function nosuch(integer) does not exist\n"
            "LINE 1: select nosuch(1)\n"
            "               ^\n"
            "HINT:  No function matches the given name and argument types."
            " You might need to add explicit type casts.",
        )
        self.assertEqual(
            diag.message_hint,
            "No function matches the given name and argument types."
            " You might need to add explicit type casts.",
        )
        self.check(
            self.error("select 'x'::int"), "InvalidTextRepresentation", e.DataError,
            'invalid input syntax for type integer: "x"\n'
            "LINE 1: select 'x'::int\n"
            "               ^",
        )

    def test_raised_errors(self) -> None:
        error = self.error(
            "do $$ begin raise exception 'custom %', 1 using errcode = 'ZZ123',"
            " detail = 'd', hint = 'h'; end $$"
        )
        self.assertIs(type(error), e.DatabaseError)
        self.assertEqual(error.sqlstate, "ZZ123")
        self.assertEqual(
            str(error),
            "custom 1\nDETAIL:  d\nHINT:  h\n"
            "CONTEXT:  PL/pgSQL function inline_code_block line 1 at RAISE",
        )
        self.assertEqual(
            error.diag.context, "PL/pgSQL function inline_code_block line 1 at RAISE"
        )
        diag = self.check(
            self.error("do $$ begin raise exception 'plain'; end $$"),
            "RaiseException", e.ProgrammingError,
            "plain\nCONTEXT:  PL/pgSQL function inline_code_block line 1 at RAISE",
        )
        self.assertEqual(diag.sqlstate, "P0001")
        error = self.error(
            "do $$ begin raise exception using errcode = '23999',"
            " message = 'made up'; end $$"
        )
        self.assertIs(type(error), e.IntegrityError)
        self.assertEqual(error.sqlstate, "23999")
        diag = self.check(
            self.error("do $$ begin perform 1/0; end $$"),
            "DivisionByZero", e.DataError,
            'division by zero\nCONTEXT:  SQL statement "SELECT 1/0"\n'
            "PL/pgSQL function inline_code_block line 1 at PERFORM",
        )
        self.assertEqual(
            diag.context,
            'SQL statement "SELECT 1/0"\n'
            "PL/pgSQL function inline_code_block line 1 at PERFORM",
        )

    def test_source_fields_are_text_or_none(self) -> None:
        diag = self.error("select 1/0").diag
        for name in ("source_file", "source_line", "source_function"):
            self.assertIsInstance(getattr(diag, name), (str, type(None)), name)

    def test_the_exception_keeps_no_native_result(self) -> None:
        result = self.pgconn.exec_(b"select 1/0")
        error = error_from_result(result)
        result.clear()
        self.assertIsInstance(error.pgresult, ErrorInfo)
        self.assertEqual(error.diag.message_primary, "division by zero")
        copy = pickle.loads(pickle.dumps(error))
        self.assertEqual(type(copy).__name__, "DivisionByZero")
        self.assertEqual(copy.diag.sqlstate, "22012")

    @support.only_modes("oracle", "server")
    def test_lost_connection(self) -> None:
        peer = socket.socket(fileno=os.dup(self.pgconn.socket))
        peer.shutdown(socket.SHUT_RDWR)
        peer.close()
        result = self.pgconn.exec_(b"select 1")
        self.assertEqual(result.status, pq.ExecStatus.FATAL_ERROR)
        error = error_from_result(result)
        self.assertIs(type(error), e.OperationalError)
        self.assertIsNone(error.sqlstate)
        self.assertEqual(
            str(error),
            "server closed the connection unexpectedly\n"
            "\tThis probably means the server terminated abnormally\n"
            "\tbefore or while processing the request.",
        )
        self.assertEqual(self.pgconn.status, pq.ConnStatus.BAD)
```

#### CPYTHON-ROWS-01-FACTORIES

Files: `stuntgres/rows.py`, `tests/test_rows.py`. Design sections to implement: 7; also read: 6.4.

Step. Write `stuntgres/rows.py` as printed in 7. It imports nothing of the package.

Before the code exists: `ImportError: cannot import name 'rows' from 'stuntgres'`.

Tests to write first (8 unit cases; no database case (the database modes of the runner: see 11.2)):

`tests/test_rows.py`:

```python
"""Tests of the row factories."""
from __future__ import annotations

import unittest
from typing import Any

from stuntgres import rows


class FakeColumn:
    def __init__(self, name: str) -> None:
        self.name = name


class FakeCursor:
    """What a row factory reads of a cursor."""

    def __init__(self, *names: str) -> None:
        self.description: Any = [FakeColumn(name) for name in names]


class RowsTest(unittest.TestCase):
    def test_tuple_row(self) -> None:
        make = rows.tuple_row(FakeCursor("a", "b"))
        self.assertIs(make, tuple)
        values = (1, "x")
        self.assertIs(make(values), values)

    def test_dict_row(self) -> None:
        make = rows.dict_row(FakeCursor("a", "b"))
        self.assertEqual(make((1, "x")), {"a": 1, "b": "x"})
        self.assertEqual(list(make((1, "x"))), ["a", "b"])
        self.assertIsNot(make((1, "x")), make((1, "x")))

    def test_dict_row_duplicate_names(self) -> None:
        make = rows.dict_row(FakeCursor("a", "a", "?column?"))
        self.assertEqual(make((1, 2, 3)), {"a": 2, "?column?": 3})

    def test_namedtuple_row(self) -> None:
        make = rows.namedtuple_row(FakeCursor("a", "b"))
        row = make((1, "x"))
        self.assertEqual(row, (1, "x"))
        self.assertEqual((row.a, row.b), (1, "x"))
        self.assertEqual(type(row).__name__, "Row")
        self.assertEqual(row._fields, ("a", "b"))
        self.assertEqual(repr(row), "Row(a=1, b='x')")

    def test_namedtuple_row_renames_bad_names(self) -> None:
        make = rows.namedtuple_row(FakeCursor("a", "?column?", "a", "class", "_x", ""))
        row = make((1, 2, 3, 4, 5, 6))
        self.assertEqual(row._fields, ("a", "_1", "_2", "_3", "_4", "_5"))
        self.assertEqual(row.a, 1)

    def test_namedtuple_class_is_cached(self) -> None:
        first = rows.namedtuple_row(FakeCursor("a", "b"))((1, 2))
        second = rows.namedtuple_row(FakeCursor("a", "b"))((3, 4))
        other = rows.namedtuple_row(FakeCursor("a", "c"))((5, 6))
        self.assertIs(type(first), type(second))
        self.assertIsNot(type(first), type(other))

    def test_no_columns(self) -> None:
        self.assertEqual(rows.dict_row(FakeCursor())(()), {})
        self.assertEqual(rows.namedtuple_row(FakeCursor())(()), ())

    def test_no_description(self) -> None:
        cursor = FakeCursor()
        cursor.description = None
        self.assertEqual(rows.dict_row(cursor)(()), {})
        self.assertEqual(rows.namedtuple_row(cursor)(())._fields, ())
```

#### CPYTHON-CONN-01A-CONNINFO

Files: `stuntgres/conninfo.py`, `tests/test_conninfo.py`. Design sections to implement: 4.1.

Step. Write `stuntgres/conninfo.py` as printed in 4.1. `stuntgres.pq` is imported inside `_parse`, so the three calls of `ConninfoUnitTest.test_nothing_to_do_needs_no_library` load no native library.

Before the code exists: `ModuleNotFoundError: No module named 'stuntgres.conninfo'`.

Tests to write first (9 cases in oracle and in server mode, of which 3 also run in unit mode):

`tests/test_conninfo.py`:

```python
"""Tests of the connection-string functions."""
from __future__ import annotations

import unittest

from stuntgres import errors as e
from stuntgres.conninfo import _quote, connect_timeout, conninfo_to_dict
from stuntgres.conninfo import make_conninfo
from tests import support


class ConninfoUnitTest(unittest.TestCase):
    def test_quote(self) -> None:
        cases = {
            "abc": "abc",
            "x=y": "x=y",
            "": "''",
            "a b": "'a b'",
            "tab\there": "'tab\there'",
            "it's": "'it\\'s'",
            "back\\slash": "'back\\\\slash'",
            "\u017c": "\u017c",
        }
        for value, quoted in cases.items():
            self.assertEqual(_quote(value), quoted, value)

    def test_connect_timeout(self) -> None:
        self.assertIsNone(connect_timeout({}))
        cases = {
            "0": None, 0: None, "-1": None, "1": 2.0, 1: 2.0, "2": 2.0, "10": 10.0,
            2.9: 2.0, 30: 30.0,
        }
        for value, seconds in cases.items():
            self.assertEqual(connect_timeout({"connect_timeout": value}), seconds)
        for value in ("x", "", "1.5"):
            with self.assertRaises(e.ProgrammingError) as caught:
                connect_timeout({"connect_timeout": value})
            self.assertEqual(
                str(caught.exception), f"bad value for connect_timeout: {value!r}"
            )

    def test_nothing_to_do_needs_no_library(self) -> None:
        self.assertEqual(make_conninfo(), "")
        self.assertEqual(conninfo_to_dict(), {})
        self.assertEqual(
            conninfo_to_dict("", connect_timeout=5), {"connect_timeout": 5}
        )


class ConninfoTest(support.LibraryTestCase):
    def test_string_alone_is_checked_and_kept(self) -> None:
        self.assertEqual(make_conninfo("host=h dbname=d"), "host=h dbname=d")
        self.assertEqual(make_conninfo("postgresql://u@h/d"), "postgresql://u@h/d")

    def test_merge(self) -> None:
        self.assertEqual(
            make_conninfo("host=h", dbname="d", port=5433), "host=h dbname=d port=5433"
        )
        self.assertEqual(
            make_conninfo("host=h dbname=d", dbname="x", host=None), "dbname=x"
        )
        self.assertEqual(make_conninfo("host=h", host=None), "")

    def test_values_are_quoted(self) -> None:
        self.assertEqual(
            make_conninfo(
                "", password="a b", application_name="it's", options="-c x=1\\2",
                user="",
            ),
            "password='a b' application_name='it\\'s' options='-c x=1\\\\2' user=''",
        )
        self.assertEqual(
            conninfo_to_dict(
                "password='a b' application_name='it\\'s' options='-c x=1\\\\2'"
            ),
            {"password": "a b", "application_name": "it's", "options": "-c x=1\\2"},
        )

    def test_uri(self) -> None:
        uri = "postgresql://u:p@h:5433/d?application_name=app"
        self.assertEqual(
            conninfo_to_dict(uri),
            {
                "user": "u", "password": "p", "dbname": "d", "host": "h",
                "port": "5433", "application_name": "app",
            },
        )
        self.assertEqual(
            make_conninfo(uri, connect_timeout=3),
            "user=u password=p dbname=d host=h port=5433 application_name=app"
            " connect_timeout=3",
        )

    def test_to_dict(self) -> None:
        self.assertEqual(
            conninfo_to_dict("host=h port=5433 dbname='a b'", user="u", port=None),
            {"dbname": "a b", "host": "h", "user": "u"},
        )

    def test_errors(self) -> None:
        cases = [
            (("host=h",), {"nosuch": "x"}, 'invalid connection option "nosuch"'),
            (("nosuch=x",), {}, 'invalid connection option "nosuch"'),
            (("host",), {}, 'missing "=" after "host" in connection info string'),
            (
                ("host='x",), {},
                "unterminated quoted string in connection info string",
            ),
            (
                ("postgresql://h/d?nosuch=1",), {},
                'invalid URI query parameter: "nosuch"',
            ),
        ]
        for args, kwargs, message in cases:
            with self.assertRaises(e.ProgrammingError) as caught:
                make_conninfo(*args, **kwargs)
            self.assertIs(type(caught.exception), e.ProgrammingError)
            self.assertEqual(str(caught.exception), message)
        with self.assertRaises(e.ProgrammingError):
            conninfo_to_dict("host")
```

#### CPYTHON-CONN-01B-INFO

Files: `stuntgres/_connection_info.py`, `tests/test_connection_info.py`. Design sections to implement: 4.2; also read: 4.1.

Step. Write `stuntgres/_connection_info.py` as printed in 4.2.

Before the code exists: `ModuleNotFoundError: No module named 'stuntgres._connection_info'`.

Tests to write first (6 cases in oracle and in server mode; none runs in unit mode):

`tests/test_connection_info.py`:

```python
"""Tests of ConnectionInfo."""
from __future__ import annotations

from stuntgres import errors as e
from stuntgres import pq
from stuntgres._connection_info import ConnectionInfo
from stuntgres.conninfo import conninfo_to_dict
from tests import support


class ConnectionInfoTest(support.DatabaseTestCase):
    def setUp(self) -> None:
        super().setUp()
        extra = " options='-c geqo=off' application_name='my app'"
        self.pgconn = pq.PGconn.connect((self.conninfo + extra).encode())
        self.addCleanup(self.pgconn.finish)
        self.assertEqual(self.pgconn.status, 0, self.pgconn.error_message)
        self.info = ConnectionInfo(self.pgconn)
        self.given = support.conninfo_dict(self.conninfo)

    def run_sql(self, sql: str) -> int:
        return self.pgconn.exec_(sql.encode()).status

    def test_where_the_connection_leads(self) -> None:
        info, given = self.info, self.given
        self.assertEqual(info.host, given["host"])
        self.assertEqual(info.port, int(given["port"]))
        self.assertEqual(info.dbname, given["dbname"])
        self.assertEqual(info.user, given["user"])
        self.assertEqual(info.password, given.get("password", ""))
        self.assertEqual(info.options, "-c geqo=off")
        self.assertEqual(ConnectionInfo.__module__, "stuntgres")

    def test_parameters_and_dsn(self) -> None:
        params = self.info.get_parameters()
        for keyword in ("host", "port", "dbname", "user"):
            self.assertEqual(params[keyword], self.given[keyword], keyword)
        self.assertEqual(params["options"], "-c geqo=off")
        self.assertEqual(params["application_name"], "my app")
        self.assertNotIn("password", params)
        dsn = self.info.dsn
        self.assertNotIn("password", dsn)
        self.assertIn("application_name='my app'", dsn)
        self.assertIn("options='-c geqo=off'", dsn)
        self.assertEqual(conninfo_to_dict(dsn), params)

    def test_session_facts(self) -> None:
        info = self.info
        self.assertIs(info.status, pq.ConnStatus.OK)
        self.assertIs(info.transaction_status, pq.TransactionStatus.IDLE)
        self.assertEqual(self.run_sql("begin"), pq.ExecStatus.COMMAND_OK)
        self.assertIs(info.transaction_status, pq.TransactionStatus.INTRANS)
        self.assertEqual(info.parameter_status("server_encoding"), "UTF8")
        self.assertEqual(info.parameter_status("client_encoding"), "UTF8")
        self.assertEqual(info.parameter_status("application_name"), "my app")
        self.assertEqual(info.parameter_status("standard_conforming_strings"), "on")
        self.assertEqual(info.parameter_status("integer_datetimes"), "on")
        self.assertEqual(info.parameter_status("DateStyle"), "ISO, MDY")
        self.assertEqual(info.parameter_status("IntervalStyle"), "postgres")
        self.assertIsNone(info.parameter_status("nosuch"))
        self.assertGreaterEqual(info.server_version, 180000)
        self.assertLess(info.server_version, 190000)
        self.assertGreater(info.backend_pid, 0)

    def test_encoding(self) -> None:
        info = self.info
        self.assertEqual(info.encoding, "utf-8")
        self.assertEqual(self.run_sql("set client_encoding to latin1"), 1)
        self.assertEqual(info.encoding, "iso8859-1")
        self.assertEqual(info.parameter_status("client_encoding"), "LATIN1")
        self.assertEqual(self.run_sql("set client_encoding to sql_ascii"), 1)
        self.assertEqual(info.encoding, "ascii")
        self.assertEqual(self.run_sql("set client_encoding to 'EUC_TW'"), 1)
        with self.assertRaises(e.NotSupportedError) as caught:
            info.encoding
        self.assertEqual(
            str(caught.exception), "client encoding EUC_TW has no Python codec"
        )

    def test_error_message(self) -> None:
        self.assertEqual(self.info.error_message, "")
        self.assertEqual(self.run_sql("select 1/0"), pq.ExecStatus.FATAL_ERROR)
        self.assertEqual(self.info.error_message, "ERROR:  division by zero")

    def test_after_finish(self) -> None:
        self.pgconn.finish()
        info = self.info
        self.assertIs(info.status, pq.ConnStatus.BAD)
        self.assertIs(info.transaction_status, pq.TransactionStatus.UNKNOWN)
        self.assertEqual(info.error_message, "")
        self.assertEqual(info.encoding, "utf-8")
        for name in ("host", "port", "dbname", "server_version", "backend_pid", "dsn"):
            with self.assertRaises(e.OperationalError) as caught:
                getattr(info, name)
            self.assertEqual(str(caught.exception), "the connection is closed", name)
        with self.assertRaises(e.OperationalError):
            info.parameter_status("server_encoding")
```

#### CPYTHON-CONN-01C-CORE

Files: `stuntgres/_conn_core.py`, `tests/test_conn_core.py`. Design sections to implement: 4.3; also read: 1.4, 8.2, 8.3.

Step. Write `stuntgres/_conn_core.py` as printed in 4.3. The tests connect with `waiting.wait_conn(ConnCore._connect_gen(...))`: `ConnCore` alone is a complete, if bare, connection.

Before the code exists: `ModuleNotFoundError: No module named 'stuntgres._conn_core'`.

Tests to write first (15 cases in oracle and in server mode, of which 6 also run in unit mode):

`tests/test_conn_core.py`:

```python
"""Tests of the state and life cycle shared by the connection classes."""
from __future__ import annotations

import os
import socket
import unittest
from typing import Any

from stuntgres import adapt
from stuntgres import errors as e
from stuntgres import pq, rows, waiting
from stuntgres._conn_core import CANCEL_TIMEOUT, NOTIFY_INTERVAL, ConnCore
from stuntgres._conn_core import IsolationLevel, Notify, NotifyWindow
from stuntgres._connection_info import ConnectionInfo
from tests import support


class NamesTest(unittest.TestCase):
    def test_isolation_level(self) -> None:
        self.assertEqual(
            [(level.name, level.value) for level in IsolationLevel],
            [
                ("READ_UNCOMMITTED", 1), ("READ_COMMITTED", 2),
                ("REPEATABLE_READ", 3), ("SERIALIZABLE", 4),
            ],
        )

    def test_notify(self) -> None:
        notify = Notify("chan", "payload", 42)
        self.assertEqual(notify, ("chan", "payload", 42))
        self.assertEqual((notify.channel, notify.payload, notify.pid), notify)

    def test_exceptions_as_attributes(self) -> None:
        for name in (
            "Warning", "Error", "InterfaceError", "DatabaseError", "DataError",
            "OperationalError", "IntegrityError", "InternalError",
            "ProgrammingError", "NotSupportedError",
        ):
            self.assertIs(getattr(ConnCore, name), getattr(e, name), name)


class WindowTest(unittest.TestCase):
    def test_no_limits(self) -> None:
        window = NotifyWindow(None, None)
        self.assertIs(window.full(), False)
        self.assertEqual(window.interval(), NOTIFY_INTERVAL)
        self.assertEqual(NOTIFY_INTERVAL, 0.1)
        self.assertIs(window.expired, False)
        window.count = 1000
        self.assertIs(window.full(), False)

    def test_stop_after(self) -> None:
        window = NotifyWindow(None, 2)
        self.assertIs(window.full(), False)
        window.count = 2
        self.assertIs(window.full(), True)
        self.assertIs(NotifyWindow(None, 0).full(), True)

    def test_timeout(self) -> None:
        window = NotifyWindow(60.0, None)
        self.assertEqual(window.interval(), NOTIFY_INTERVAL)
        self.assertIs(window.expired, False)
        window = NotifyWindow(0.05, None)
        left = window.interval()
        self.assertGreater(left, 0.0)
        self.assertLessEqual(left, 0.05)
        self.assertIs(window.expired, False)
        window = NotifyWindow(0, None)
        self.assertIs(window.expired, False)
        self.assertEqual(window.interval(), 0.0)
        self.assertIs(window.expired, True)
        self.assertEqual(CANCEL_TIMEOUT, 5.0)


class ConnCoreTest(support.DatabaseTestCase):
    def connect(self, **kwargs: Any) -> Any:
        gen = ConnCore._connect_gen(self.conninfo, **kwargs)
        conn = waiting.wait_conn(gen, timeout=10)
        self.addCleanup(conn._finish)
        return conn

    def test_new_connection(self) -> None:
        conn = self.connect()
        self.assertIs(type(conn), ConnCore)
        self.assertIsInstance(conn.pgconn, pq.PGconn)
        self.assertEqual(conn.pgconn.status, pq.ConnStatus.OK)
        self.assertEqual(conn.pgconn.nonblocking, 1)
        self.assertEqual(conn.pgconn.transaction_status, pq.TransactionStatus.IDLE)
        self.assertIs(conn._autocommit, False)
        self.assertIsNone(conn._isolation_level)
        self.assertIsNone(conn._read_only)
        self.assertIsNone(conn._deferrable)
        self.assertIs(conn.row_factory, rows.tuple_row)
        self.assertIsNone(conn.cursor_factory)
        self.assertIs(conn.closed, False)
        self.assertIs(conn.broken, False)
        self.assertIs(conn.connection, conn)
        self.assertEqual(conn._num_transactions, 0)
        self.assertIsNone(conn._tpc)
        self.assertEqual(conn.fileno(), conn.pgconn.socket)
        self.assertIs(self.connect(autocommit=True)._autocommit, True)

    def test_adapters(self) -> None:
        conn = self.connect()
        self.assertIsInstance(conn.adapters, adapt.AdaptersMap)
        self.assertIsNot(conn.adapters, adapt.context_adapters(None))
        self.assertIsNot(conn.adapters, self.connect().adapters)
        self.assertIs(adapt.context_adapters(conn), conn.adapters)
        self.assertIs(adapt.context_pgconn(conn), conn.pgconn)
        self.assertEqual(adapt.context_encoding(conn), "utf-8")

    def test_info(self) -> None:
        conn = self.connect()
        self.assertIsInstance(conn.info, ConnectionInfo)
        given = support.conninfo_dict(self.conninfo)
        self.assertEqual(conn.info.dbname, given["dbname"])

    def test_repr(self) -> None:
        conn = self.connect()
        self.assertRegex(
            repr(conn), r"^<stuntgres\._conn_core\.ConnCore \[open\] at 0x[0-9a-f]+>$"
        )
        conn._finish()
        self.assertRegex(
            repr(conn), r"^<stuntgres\._conn_core\.ConnCore \[closed\] at 0x[0-9a-f]+>$"
        )

    def test_check_open_and_finish(self) -> None:
        conn = self.connect()
        conn._check_open()
        self.assertEqual(conn._codec, "utf-8")
        conn.pgconn.exec_(b"set client_encoding to latin1")
        conn._check_open()
        self.assertEqual(conn._codec, "iso8859-1")
        self.assertGreater(conn._wait_fileno(), 0)
        conn._finish()
        self.assertIs(conn.closed, True)
        self.assertIs(conn.broken, False)
        self.assertEqual(conn._wait_fileno(), -1)
        conn._finish()
        for check in (conn._check_open, conn._check_closed):
            with self.assertRaises(e.OperationalError) as caught:
                check()
            self.assertEqual(str(caught.exception), "the connection is closed")

    def test_check_intrans(self) -> None:
        conn = self.connect()
        conn._check_intrans("autocommit")
        conn.pgconn.exec_(b"begin")
        with self.assertRaises(e.ProgrammingError) as caught:
            conn._check_intrans("autocommit")
        self.assertEqual(
            str(caught.exception),
            "cannot change autocommit inside a transaction"
            " (transaction status INTRANS)",
        )
        conn.pgconn.exec_(b"select 1/0")
        with self.assertRaises(e.ProgrammingError) as caught:
            conn._check_intrans("read_only")
        self.assertEqual(
            str(caught.exception),
            "cannot change read_only inside a transaction"
            " (transaction status INERROR)",
        )
        self.assertEqual(conn._begin_statement(), b"BEGIN")

    def test_assign_is_for_the_public_classes(self) -> None:
        with self.assertRaises(NotImplementedError):
            self.connect()._assign("autocommit", True)

    @support.only_modes("oracle", "server")
    def test_lost_connection_is_broken(self) -> None:
        conn = self.connect()
        peer = socket.socket(fileno=os.dup(conn.fileno()))
        peer.shutdown(socket.SHUT_RDWR)
        peer.close()
        conn.pgconn.exec_(b"select 1")
        self.assertIs(conn.closed, False)
        with self.assertRaises(e.OperationalError) as caught:
            conn._check_open()
        self.assertEqual(str(caught.exception), "the connection is lost")
        self.assertIs(conn.closed, True)
        self.assertIs(conn.broken, True)
        self.assertEqual(conn._wait_fileno(), -1)
        self.assertRegex(repr(conn), r"ConnCore \[broken\] at 0x")
        conn._finish()
        self.assertIs(conn.broken, False)
        self.assertIs(conn.closed, True)

    def test_failed_connection(self) -> None:
        gen = ConnCore._connect_gen(self.conninfo + " options='-c nosuch_setting=1'")
        with self.assertRaises(e.OperationalError) as caught:
            waiting.wait_conn(gen, timeout=10)
        self.assertIn(
            'unrecognized configuration parameter "nosuch_setting"',
            str(caught.exception),
        )
        self.assertTrue(str(caught.exception).startswith("connection failed: "))
```

#### CPYTHON-CONN-01D-BASE

Files: `stuntgres/_features.py`, `stuntgres/_connection_base.py`, `tests/test_connection_base.py`; stubs `stuntgres/_conn_exec.py`, `stuntgres/_conn_txnopts.py`, `stuntgres/_conn_notices.py`, `stuntgres/_conn_notify.py`, `stuntgres/_conn_cancel.py`. Design sections to implement: 1.3, 1.5, 4.4; also read: 4.3.

Step. 1. Write `stuntgres/_features.py` as printed in 1.5. 2. Create the five stub files of 1.3 (`_conn_exec.py`, `_conn_txnopts.py`, `_conn_notices.py`, `_conn_notify.py`, `_conn_cancel.py`): the printed stub with the class name and the owner part of each file. 3. Write `stuntgres/_connection_base.py` as printed in 4.4.

Before the code exists: `ImportError: cannot import name '_features' from 'stuntgres'`.

Tests to write first (7 cases in oracle and in server mode, of which 3 also run in unit mode):

`tests/test_connection_base.py`:

```python
"""Tests of the composed base class and of the files handed to later parts."""
from __future__ import annotations

import gc
import unittest
import warnings

from stuntgres import _features, pq, waiting
from stuntgres._conn_core import ConnCore
from stuntgres._connection_base import BaseConnection, IsolationLevel, Notify
from tests import support


class CompositionTest(unittest.TestCase):
    def test_bases(self) -> None:
        names = [cls.__name__ for cls in BaseConnection.__mro__]
        self.assertEqual(
            names,
            [
                "BaseConnection", "BaseConnectionFeatures", "CancelMixin",
                "NotifyMixin", "NoticesMixin", "TxnOptionsMixin", "ExecMixin",
                "ConnCore", "object",
            ],
        )

    def test_exports(self) -> None:
        from stuntgres import _conn_core, _connection_base

        self.assertEqual(
            _connection_base.__all__,
            ["BaseConnection", "ConnCore", "IsolationLevel", "Notify"],
        )
        self.assertIs(IsolationLevel, _conn_core.IsolationLevel)
        self.assertIs(Notify, _conn_core.Notify)

    def test_features_stub(self) -> None:
        self.assertEqual(_features.__all__, [])
        for name in (
            "BaseConnectionFeatures", "ConnectionFeatures", "AsyncConnectionFeatures",
            "BaseCursorFeatures", "CursorFeatures", "AsyncCursorFeatures",
        ):
            cls = getattr(_features, name)
            self.assertEqual(cls.__bases__, (object,), name)


class BaseConnectionTest(support.DatabaseTestCase):
    def test_connect(self) -> None:
        gen = BaseConnection._connect_gen(self.conninfo, autocommit=True)
        conn = waiting.wait_conn(gen, timeout=10)
        self.addCleanup(conn._finish)
        self.assertIs(type(conn), BaseConnection)
        self.assertIsInstance(conn, ConnCore)
        self.assertIs(conn._autocommit, True)
        self.assertIs(conn.closed, False)
        self.assertEqual(conn._begin_statement(), b"BEGIN")

    def test_unclosed_connection_warns(self) -> None:
        gen = BaseConnection._connect_gen(self.conninfo)
        conn = waiting.wait_conn(gen, timeout=10)
        pgconn = conn.pgconn
        with warnings.catch_warnings(record=True) as caught:
            warnings.simplefilter("always")
            del conn
            gc.collect()
        pgconn.finish()
        self.assertEqual(len(caught), 1)
        self.assertIs(caught[0].category, ResourceWarning)
        self.assertRegex(
            str(caught[0].message),
            r"^<stuntgres\._connection_base\.BaseConnection \[open\] at 0x[0-9a-f]+>"
            r" was deleted while still open: close it or use it in a 'with'"
            r" statement$",
        )

    def test_closed_connection_does_not_warn(self) -> None:
        gen = BaseConnection._connect_gen(self.conninfo)
        conn = waiting.wait_conn(gen, timeout=10)
        conn._finish()
        with warnings.catch_warnings(record=True) as caught:
            warnings.simplefilter("always")
            del conn
            gc.collect()
        self.assertEqual(caught, [])

    def test_handlers_do_not_keep_the_connection_alive(self) -> None:
        gen = BaseConnection._connect_gen(self.conninfo)
        conn = waiting.wait_conn(gen, timeout=10)
        pgconn = conn.pgconn
        conn._finish()
        del conn
        gc.collect()
        self.assertIsNotNone(pgconn.notice_handler)
        pgconn.notice_handler(None)
        pgconn.notify_handler(pq.PGnotify(b"c", 1, b""))
```

#### CPYTHON-CONN-01E-SYNC

Files: `stuntgres/connection.py`, `tests/test_connection.py`. Design sections to implement: 9.1, 9.2, 8.4; also read: 4.3, 5.2, 8.2, 1.5.

Step. Write `stuntgres/connection.py` as printed in 9.2: the complete class, including the methods whose generators arrive with later parts (`commit`, `rollback`, the `set_...` methods, `notifies`, the cancel path of `wait`). Those raise `AttributeError` until their mixin replaces its stub; the tests of this part do not call them. `Connection.cursor()` imports `stuntgres.cursor` only when it is called without `cursor_factory`; the tests of this part pass a factory or ask for a named cursor.

Before the code exists: `ModuleNotFoundError: No module named 'stuntgres.connection'`.

Tests to write first (14 cases in oracle and in server mode; none runs in unit mode):

`tests/test_connection.py`:

```python
"""Tests of Connection: connect, close and the plain members."""
from __future__ import annotations

import socket
import threading
import time
from typing import Any

from stuntgres import errors as e
from stuntgres import generators, pq, rows
from stuntgres.connection import Connection
from tests import support

SLEEP = b"do $$ begin raise notice 'started'; perform pg_sleep(30); end $$"


class ConnectionTest(support.DatabaseTestCase):
    def connect(self, conninfo: str | None = None, **kwargs: Any) -> Connection:
        if conninfo is None:
            conninfo = self.conninfo
        conn = Connection.connect(conninfo, **kwargs)
        self.addCleanup(conn.close)
        return conn

    def test_connect(self) -> None:
        conn = self.connect()
        self.assertIs(type(conn), Connection)
        self.assertEqual(Connection.__module__, "stuntgres")
        self.assertRegex(
            repr(conn),
            r"^<stuntgres\.Connection \[open\] at 0x[0-9a-f]+>$",
        )
        self.assertIs(conn._autocommit, False)
        self.assertIs(conn.row_factory, rows.tuple_row)
        self.assertIsNone(conn.cursor_factory)
        self.assertIs(type(conn.lock), type(threading.Lock()))
        self.assertIs(conn.closed, False)
        self.assertEqual(conn.pgconn.transaction_status, pq.TransactionStatus.IDLE)
        self.assertEqual(
            conn.info.dbname,
            support.conninfo_dict(self.conninfo)["dbname"],
        )
        self.assertIs(conn.__enter__(), conn)

    def test_connect_arguments(self) -> None:
        class Factory:
            pass

        conn = self.connect(
            autocommit=True, row_factory=rows.dict_row, cursor_factory=Factory
        )
        self.assertIs(conn._autocommit, True)
        self.assertIs(conn.row_factory, rows.dict_row)
        self.assertIs(conn.cursor_factory, Factory)

    def test_connect_with_keywords(self) -> None:
        given = support.conninfo_dict(self.conninfo)
        conn = self.connect("", **given)
        self.assertEqual(conn.info.dbname, given["dbname"])
        wrong = self.conninfo.replace("dbname=" + given["dbname"], "dbname=nosuch_db")
        conn = self.connect(wrong, dbname=given["dbname"], application_name="it's me")
        self.assertEqual(conn.info.dbname, given["dbname"])
        self.assertEqual(conn.info.parameter_status("application_name"), "it's me")

    def test_bad_connection_string(self) -> None:
        with self.assertRaises(e.ProgrammingError) as caught:
            Connection.connect("nosuch=1")
        self.assertEqual(str(caught.exception), 'invalid connection option "nosuch"')
        with self.assertRaises(e.ProgrammingError) as caught:
            Connection.connect(self.conninfo, nosuch=1)
        self.assertEqual(str(caught.exception), 'invalid connection option "nosuch"')
        with self.assertRaises(e.ProgrammingError) as caught:
            Connection.connect(self.conninfo, connect_timeout="x")
        self.assertEqual(str(caught.exception), "bad value for connect_timeout: 'x'")

    def test_rejected_by_the_server(self) -> None:
        with self.assertRaises(e.OperationalError) as caught:
            Connection.connect(self.conninfo, options="-c nosuch_setting=1")
        text = str(caught.exception)
        self.assertTrue(text.startswith("connection failed: "), text)
        self.assertIn('unrecognized configuration parameter "nosuch_setting"', text)

    @support.only_modes("oracle", "server")
    def test_connection_refused(self) -> None:
        with self.assertRaises(e.OperationalError) as caught:
            Connection.connect("host=127.0.0.1 port=1 sslmode=disable")
        self.assertTrue(
            str(caught.exception).startswith(
                'connection failed: connection to server at "127.0.0.1", port 1'
                " failed: Connection refused"
            ),
            str(caught.exception),
        )

    @support.only_modes("oracle", "server")
    def test_connect_timeout(self) -> None:
        silent = socket.socket()
        self.addCleanup(silent.close)
        silent.bind(("127.0.0.1", 0))
        silent.listen(1)
        port = silent.getsockname()[1]
        started = time.monotonic()
        with self.assertRaises(e.ConnectionTimeout) as caught:
            Connection.connect(
                f"host=127.0.0.1 port={port} dbname=x user=y sslmode=disable"
                " gssencmode=disable",
                connect_timeout=1,
            )
        elapsed = time.monotonic() - started
        self.assertEqual(str(caught.exception), "connection timeout expired")
        self.assertIsInstance(caught.exception, e.OperationalError)
        self.assertGreaterEqual(elapsed, 2.0)
        self.assertLess(elapsed, 10.0)

    def test_close(self) -> None:
        conn = self.connect()
        conn.close()
        self.assertIs(conn.closed, True)
        self.assertIs(conn.broken, False)
        self.assertEqual(conn.pgconn.status, pq.ConnStatus.BAD)
        self.assertRegex(
            repr(conn),
            r"^<stuntgres\.Connection \[closed\] at 0x[0-9a-f]+>$",
        )
        conn.close()
        with self.assertRaises(e.OperationalError) as caught:
            conn.cursor()
        self.assertEqual(str(caught.exception), "the connection is closed")
        self.assertIs(conn.lock.locked(), False)

    def test_wait_drives_a_generator(self) -> None:
        conn = self.connect()
        conn.pgconn.send_query(b"select 1; select 2")
        with conn.lock:
            results = conn.wait(generators.execute(conn.pgconn))
        self.assertEqual([r.get_rows(0, 1) for r in results], [[(b"1",)], [(b"2",)]])

    def test_close_does_not_wait_for_a_statement(self) -> None:
        conn = self.connect()
        conn.pgconn.send_query(b"select pg_sleep(30)")
        started = time.monotonic()
        conn.close()
        self.assertLess(time.monotonic() - started, 10.0)
        self.assertIs(conn.closed, True)

    def test_close_from_another_thread(self) -> None:
        conn = self.connect()
        seen: list[Any] = []
        running = threading.Event()
        outcome: list[Any] = []

        def announcing(inner: Any) -> Any:
            try:
                wanted = next(inner)
                while True:
                    if seen:
                        running.set()
                    wanted = inner.send((yield wanted))
            except StopIteration as stop:
                return stop.value
            finally:
                inner.close()

        def worker() -> None:
            conn.pgconn.notice_handler = seen.append
            conn.pgconn.send_query(SLEEP)
            with conn.lock:
                results = conn.wait(announcing(generators.execute(conn.pgconn)))
            outcome.extend(
                (r.status, r.error_field(pq.DiagnosticField.SQLSTATE)) for r in results
            )

        thread = threading.Thread(target=worker)
        thread.start()
        self.addCleanup(thread.join)
        self.assertTrue(running.wait(10))
        started = time.monotonic()
        conn.close()
        thread.join()
        self.assertLess(time.monotonic() - started, 10.0)
        self.assertEqual(outcome, [(pq.ExecStatus.FATAL_ERROR, b"57014")])
        self.assertIs(conn.closed, True)

    def test_named_cursor_is_not_available(self) -> None:
        conn = self.connect()
        with self.assertRaises(e.NotSupportedError) as caught:
            conn.cursor("big")
        self.assertEqual(str(caught.exception), "named cursors are not available")

    def test_named_cursor_goes_to_the_feature_hook(self) -> None:
        calls: list[Any] = []

        def factory(connection: Any, name: str, **kwargs: Any) -> str:
            calls.append((connection, name, kwargs))
            return "cursor object"

        conn = self.connect()
        conn.server_cursor_factory = factory  # type: ignore[attr-defined]
        made = conn.cursor("big", scrollable=True, withhold=True)
        self.assertEqual(made, "cursor object")
        self.assertEqual(
            calls,
            [(
                conn, "big",
                {"row_factory": rows.tuple_row, "scrollable": True, "withhold": True},
            )],
        )

    def test_cursor_factory_is_called(self) -> None:
        calls: list[Any] = []

        def factory(connection: Any, **kwargs: Any) -> str:
            calls.append((connection, kwargs))
            return "cursor object"

        conn = self.connect(cursor_factory=factory)
        self.assertEqual(conn.cursor(), "cursor object")
        self.assertEqual(conn.cursor(row_factory=rows.dict_row), "cursor object")
        self.assertEqual(
            calls,
            [
                (conn, {"row_factory": rows.tuple_row}),
                (conn, {"row_factory": rows.dict_row}),
            ],
        )
```

#### CPYTHON-CONN-02-EXEC

Files: `stuntgres/_conn_exec.py`, `tests/dbapi_util.py`, `tests/test_conn_exec.py`. Design sections to implement: 4.5, 4.6, 5.2; also read: 4.3, 5.1.

Step. 1. Write `tests/dbapi_util.py` (printed below; it is used by the tests of this and of later parts). 2. Replace the stub `stuntgres/_conn_exec.py` with the file printed in 4.6.

Before the code exists: With the stub in place 14 of the 15 tests fail or raise: `AttributeError: 'Connection' object has no attribute '_start_query'` (also `_commit_gen`, `autocommit`).

Tests to write first (15 cases in oracle and in server mode; none runs in unit mode):

`tests/dbapi_util.py`:

```python
"""Helpers of the DB-API tests: closed connections, SQL without a cursor."""
from __future__ import annotations

from typing import Any

from stuntgres import pq
from stuntgres.connection import Connection
from tests import support


def run(conn: Connection, sql: str) -> list[tuple[bytes | None, ...]]:
    """Run one statement the way a cursor does: implicit BEGIN, then send it."""
    with conn.lock:
        conn.wait(conn._start_query())
        command = sql.encode(conn.pgconn._encoding)
        result = conn.wait(conn._exec_command(command))
    return result.get_rows(0, result.ntuples)


def status(conn: Any) -> str:
    """The name of the session's transaction status."""
    return pq.TransactionStatus(conn.pgconn.transaction_status).name


class ConnTestCase(support.DatabaseTestCase):
    """A database test whose connections are closed when the test ends."""

    def connect(self, **kwargs: Any) -> Connection:
        """Connect to the test's database."""
        conn = Connection.connect(self.conninfo, **kwargs)
        self.addCleanup(conn.close)
        return conn

    def raw_notices(self, conn: Connection) -> list[bytes]:
        """Collect the SQLSTATE of every notice, below the notice handlers."""
        seen: list[bytes] = []

        def collect(result: Any) -> None:
            seen.append(result.error_field(pq.DiagnosticField.SQLSTATE))

        conn.pgconn.notice_handler = collect
        return seen
```

`tests/test_conn_exec.py`:

```python
"""Tests of the command path: implicit BEGIN, commit, rollback, autocommit."""
from __future__ import annotations

import threading
from typing import Any

from stuntgres import errors as e
from stuntgres.connection import Connection
from tests.dbapi_util import ConnTestCase, run, status


class ExecTest(ConnTestCase):
    def test_implicit_begin_and_commit(self) -> None:
        conn = self.connect()
        seen = self.raw_notices(conn)
        self.assertIs(conn.autocommit, False)
        self.assertEqual(status(conn), "IDLE")
        self.assertEqual(run(conn, "select 1"), [(b"1",)])
        self.assertEqual(status(conn), "INTRANS")
        self.assertEqual(run(conn, "select 2"), [(b"2",)])
        self.assertEqual(status(conn), "INTRANS")
        conn.commit()
        self.assertEqual(status(conn), "IDLE")
        # A second BEGIN would have raised warning 25001: one was sent.
        self.assertEqual(seen, [])

    def test_commit_and_rollback_send_nothing_when_idle(self) -> None:
        conn = self.connect()
        seen = self.raw_notices(conn)
        conn.commit()
        conn.rollback()
        # COMMIT or ROLLBACK outside a transaction raises warning 25P01.
        self.assertEqual(seen, [])
        self.assertEqual(status(conn), "IDLE")
        self.assertIs(conn.lock.locked(), False)

    def test_committed_data_is_seen_by_another_connection(self) -> None:
        writer = self.connect()
        reader = self.connect(autocommit=True)
        run(writer, "create table t (id int primary key, v text)")
        run(writer, "insert into t values (1, 'a')")
        count = "select count(*) from pg_class where relname = 't'"
        self.assertEqual(run(reader, count), [(b"0",)])
        writer.commit()
        self.assertEqual(run(reader, count), [(b"1",)])
        self.assertEqual(run(reader, "select id, v from t"), [(b"1", b"a")])
        run(writer, "insert into t values (2, 'b')")
        self.assertEqual(run(reader, "select count(*) from t"), [(b"1",)])
        self.assertEqual(run(writer, "select count(*) from t"), [(b"2",)])
        writer.rollback()
        self.assertEqual(status(writer), "IDLE")
        self.assertEqual(run(reader, "select count(*) from t"), [(b"1",)])

    def test_error_then_statement_then_rollback(self) -> None:
        conn = self.connect()
        run(conn, "select 1")
        with self.assertRaises(e.DataError) as caught:
            run(conn, "select 1/0")
        self.assertEqual(type(caught.exception).__name__, "DivisionByZero")
        self.assertEqual(str(caught.exception), "division by zero")
        self.assertEqual(caught.exception.sqlstate, "22012")
        self.assertEqual(status(conn), "INERROR")
        self.assertIs(conn.lock.locked(), False)
        with self.assertRaises(e.InternalError) as caught2:
            run(conn, "select 1")
        self.assertEqual(type(caught2.exception).__name__, "InFailedSqlTransaction")
        self.assertEqual(
            str(caught2.exception),
            "current transaction is aborted, commands ignored until end of"
            " transaction block",
        )
        self.assertEqual(status(conn), "INERROR")
        conn.rollback()
        self.assertEqual(status(conn), "IDLE")
        self.assertEqual(run(conn, "select 3"), [(b"3",)])

    def test_commit_after_an_error_rolls_back(self) -> None:
        conn = self.connect()
        run(conn, "create table t (id int)")
        with self.assertRaises(e.DataError):
            run(conn, "select 1/0")
        conn.commit()
        self.assertEqual(status(conn), "IDLE")
        count = "select count(*) from pg_class where relname = 't'"
        self.assertEqual(run(conn, count), [(b"0",)])

    def test_error_outside_a_transaction(self) -> None:
        conn = self.connect(autocommit=True)
        with self.assertRaises(e.ProgrammingError) as caught:
            run(conn, "select * from nosuch")
        self.assertEqual(type(caught.exception).__name__, "UndefinedTable")
        self.assertEqual(status(conn), "IDLE")
        self.assertEqual(run(conn, "select 1"), [(b"1",)])

    def test_autocommit(self) -> None:
        conn = self.connect(autocommit=True)
        self.assertIs(conn.autocommit, True)
        run(conn, "create table t (id int)")
        self.assertEqual(status(conn), "IDLE")
        other = self.connect(autocommit=True)
        self.assertEqual(run(other, "select count(*) from t"), [(b"0",)])
        conn.autocommit = False
        self.assertIs(conn.autocommit, False)
        run(conn, "insert into t values (1)")
        self.assertEqual(status(conn), "INTRANS")
        with self.assertRaises(e.ProgrammingError) as caught:
            conn.autocommit = True
        self.assertEqual(
            str(caught.exception),
            "cannot change autocommit inside a transaction"
            " (transaction status INTRANS)",
        )
        with self.assertRaises(e.ProgrammingError):
            conn.set_autocommit(True)
        self.assertIs(conn.autocommit, False)
        conn.rollback()
        conn.set_autocommit(1)  # type: ignore[arg-type]
        self.assertIs(conn.autocommit, True)
        self.assertIs(conn.lock.locked(), False)

    def test_transaction_control_sent_as_sql(self) -> None:
        conn = self.connect(autocommit=True)
        seen = self.raw_notices(conn)
        run(conn, "begin")
        self.assertEqual(status(conn), "INTRANS")
        run(conn, "create table t (id int)")
        conn.rollback()
        self.assertEqual(status(conn), "IDLE")
        count = "select count(*) from pg_class where relname = 't'"
        self.assertEqual(run(conn, count), [(b"0",)])
        conn.set_autocommit(False)
        run(conn, "create table t (id int)")
        run(conn, "commit")
        self.assertEqual(status(conn), "IDLE")
        conn.commit()
        self.assertEqual(run(conn, count), [(b"1",)])
        self.assertEqual(status(conn), "INTRANS")
        run(conn, "rollback")
        self.assertEqual(status(conn), "IDLE")
        conn.rollback()
        self.assertEqual(seen, [])

    def test_failing_commit(self) -> None:
        conn = self.connect()
        run(conn, "create table p (id int primary key)")
        run(
            conn,
            "create table ch (id int primary key,"
            " pid int references p (id) deferrable initially deferred)",
        )
        conn.commit()
        run(conn, "insert into ch values (1, 42)")
        with self.assertRaises(e.IntegrityError) as caught:
            conn.commit()
        self.assertEqual(type(caught.exception).__name__, "ForeignKeyViolation")
        self.assertEqual(
            str(caught.exception),
            'insert or update on table "ch" violates foreign key constraint'
            ' "ch_pid_fkey"\n'
            'DETAIL:  Key (pid)=(42) is not present in table "p".',
        )
        self.assertEqual(caught.exception.diag.constraint_name, "ch_pid_fkey")
        self.assertEqual(status(conn), "IDLE")
        self.assertEqual(run(conn, "select count(*) from ch"), [(b"0",)])

    def test_close_rolls_back(self) -> None:
        conn = self.connect()
        run(conn, "create table t (id int)")
        conn.commit()
        run(conn, "insert into t values (1)")
        conn.close()
        other = self.connect(autocommit=True)
        self.assertEqual(run(other, "select count(*) from t"), [(b"0",)])
        for method in (conn.commit, conn.rollback):
            with self.assertRaises(e.OperationalError) as caught:
                method()
            self.assertEqual(str(caught.exception), "the connection is closed")
        with self.assertRaises(e.OperationalError):
            conn.autocommit = True

    def test_with_commits_and_closes(self) -> None:
        with Connection.connect(self.conninfo) as conn:
            run(conn, "create table t (id int)")
            run(conn, "insert into t values (1)")
        self.assertIs(conn.closed, True)
        other = self.connect(autocommit=True)
        self.assertEqual(run(other, "select count(*) from t"), [(b"1",)])

    def test_with_rolls_back_on_error_and_closes(self) -> None:
        with self.assertRaises(ValueError):
            with Connection.connect(self.conninfo) as conn:
                run(conn, "create table t (id int)")
                raise ValueError("stop")
        self.assertIs(conn.closed, True)
        other = self.connect(autocommit=True)
        count = "select count(*) from pg_class where relname = 't'"
        self.assertEqual(run(other, count), [(b"0",)])

    def test_with_after_close_and_after_a_database_error(self) -> None:
        with Connection.connect(self.conninfo) as conn:
            conn.close()
        with self.assertRaises(e.DataError):
            with Connection.connect(self.conninfo) as conn:
                run(conn, "select 1/0")
        self.assertIs(conn.closed, True)

    def test_commit_is_refused_while_something_else_manages(self) -> None:
        conn = self.connect()
        conn._num_transactions = 1
        for name in ("commit", "rollback"):
            with self.assertRaises(e.ProgrammingError) as caught:
                getattr(conn, name)()
            self.assertEqual(
                str(caught.exception),
                f"{name}() cannot be used inside a transaction() block",
            )
        conn._num_transactions = 0
        conn._tpc = object()
        for name in ("commit", "rollback"):
            with self.assertRaises(e.ProgrammingError) as caught:
                getattr(conn, name)()
            self.assertEqual(
                str(caught.exception),
                f"{name}() cannot be used during a two-phase transaction",
            )
        conn._tpc = None
        conn.commit()

    def test_threads_share_a_connection(self) -> None:
        conn = self.connect(autocommit=True)
        results: dict[int, list[Any]] = {}
        errors: list[BaseException] = []

        def worker(number: int) -> None:
            try:
                results[number] = [
                    run(conn, f"select {number * 1000 + i}")[0][0] for i in range(25)
                ]
            except BaseException as exc:
                errors.append(exc)

        threads = [threading.Thread(target=worker, args=(n,)) for n in range(4)]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join()
        self.assertEqual(errors, [])
        for number in range(4):
            expected = [str(number * 1000 + i).encode() for i in range(25)]
            self.assertEqual(results[number], expected)
```

#### CPYTHON-CONN-03-TXN-OPTIONS

Files: `stuntgres/_conn_txnopts.py`, `tests/test_conn_txnopts.py`. Design sections to implement: 5.3; also read: 4.6, 5.1.

Step. Replace the stub `stuntgres/_conn_txnopts.py` with the file printed in 5.3.

Before the code exists: With the stub in place: `AttributeError: 'Connection' object has no attribute 'isolation_level'`, and `_begin_statement()` is always `b"BEGIN"`.

Tests to write first (8 cases in oracle and in server mode; none runs in unit mode):

`tests/test_conn_txnopts.py`:

```python
"""Tests of isolation_level, read_only and deferrable."""
from __future__ import annotations

from stuntgres import errors as e
from stuntgres._conn_core import IsolationLevel
from tests.dbapi_util import ConnTestCase, run, status

SHOW = (
    "select current_setting('transaction_isolation'),"
    " current_setting('transaction_read_only'),"
    " current_setting('transaction_deferrable')"
)


class TxnOptionsTest(ConnTestCase):
    def test_defaults(self) -> None:
        conn = self.connect()
        self.assertIsNone(conn.isolation_level)
        self.assertIsNone(conn.read_only)
        self.assertIsNone(conn.deferrable)
        self.assertEqual(conn._begin_statement(), b"BEGIN")
        self.assertEqual(run(conn, SHOW), [(b"read committed", b"off", b"off")])

    def test_begin_statements(self) -> None:
        conn = self.connect()
        cases = [
            (IsolationLevel.READ_UNCOMMITTED, None, None,
             b"BEGIN ISOLATION LEVEL READ UNCOMMITTED",
             (b"read uncommitted", b"off", b"off")),
            (IsolationLevel.READ_COMMITTED, None, None,
             b"BEGIN ISOLATION LEVEL READ COMMITTED",
             (b"read committed", b"off", b"off")),
            (IsolationLevel.REPEATABLE_READ, None, None,
             b"BEGIN ISOLATION LEVEL REPEATABLE READ",
             (b"repeatable read", b"off", b"off")),
            (IsolationLevel.SERIALIZABLE, None, None,
             b"BEGIN ISOLATION LEVEL SERIALIZABLE",
             (b"serializable", b"off", b"off")),
            (None, True, None, b"BEGIN READ ONLY",
             (b"read committed", b"on", b"off")),
            (None, False, None, b"BEGIN READ WRITE",
             (b"read committed", b"off", b"off")),
            (None, None, True, b"BEGIN DEFERRABLE",
             (b"read committed", b"off", b"on")),
            (None, None, False, b"BEGIN NOT DEFERRABLE",
             (b"read committed", b"off", b"off")),
            (IsolationLevel.SERIALIZABLE, True, True,
             b"BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE",
             (b"serializable", b"on", b"on")),
            (IsolationLevel.REPEATABLE_READ, False, False,
             b"BEGIN ISOLATION LEVEL REPEATABLE READ READ WRITE NOT DEFERRABLE",
             (b"repeatable read", b"off", b"off")),
        ]
        for level, read_only, deferrable, statement, shown in cases:
            conn.isolation_level = level
            conn.read_only = read_only
            conn.deferrable = deferrable
            self.assertEqual(conn._begin_statement(), statement)
            self.assertEqual(run(conn, SHOW), [shown], statement)
            conn.rollback()

    def test_values(self) -> None:
        conn = self.connect()
        conn.isolation_level = 4  # type: ignore[assignment]
        self.assertIs(conn.isolation_level, IsolationLevel.SERIALIZABLE)
        conn.set_isolation_level(IsolationLevel.READ_COMMITTED)
        self.assertIs(conn.isolation_level, IsolationLevel.READ_COMMITTED)
        with self.assertRaises(ValueError):
            conn.isolation_level = 5  # type: ignore[assignment]
        self.assertIs(conn.isolation_level, IsolationLevel.READ_COMMITTED)
        conn.set_isolation_level(None)
        self.assertIsNone(conn.isolation_level)
        conn.read_only = 1  # type: ignore[assignment]
        self.assertIs(conn.read_only, True)
        conn.set_read_only(False)
        self.assertIs(conn.read_only, False)
        conn.set_read_only(None)
        self.assertIsNone(conn.read_only)
        conn.deferrable = 0  # type: ignore[assignment]
        self.assertIs(conn.deferrable, False)
        conn.set_deferrable(True)
        self.assertIs(conn.deferrable, True)
        conn.set_deferrable(None)
        self.assertIsNone(conn.deferrable)

    def test_no_change_inside_a_transaction(self) -> None:
        conn = self.connect()
        run(conn, "select 1")
        cases = {
            "isolation_level": IsolationLevel.SERIALIZABLE,
            "read_only": True,
            "deferrable": True,
        }
        for name, value in cases.items():
            with self.assertRaises(e.ProgrammingError) as caught:
                setattr(conn, name, value)
            self.assertEqual(
                str(caught.exception),
                f"cannot change {name} inside a transaction"
                " (transaction status INTRANS)",
            )
            with self.assertRaises(e.ProgrammingError):
                getattr(conn, "set_" + name)(value)
            self.assertIsNone(getattr(conn, name))
        self.assertIs(conn.lock.locked(), False)
        conn.rollback()
        conn.read_only = True
        self.assertIs(conn.read_only, True)

    def test_read_only_is_enforced(self) -> None:
        conn = self.connect()
        run(conn, "create table t (id int)")
        conn.commit()
        conn.read_only = True
        with self.assertRaises(e.InternalError) as caught:
            run(conn, "insert into t values (1)")
        self.assertEqual(type(caught.exception).__name__, "ReadOnlySqlTransaction")
        self.assertEqual(
            str(caught.exception), "cannot execute INSERT in a read-only transaction"
        )
        self.assertEqual(status(conn), "INERROR")
        conn.rollback()
        conn.read_only = False
        run(conn, "insert into t values (1)")
        conn.commit()

    def test_none_leaves_the_session_default(self) -> None:
        conn = self.connect(autocommit=True)
        run(conn, "set default_transaction_isolation = 'serializable'")
        run(conn, "set default_transaction_read_only = on")
        conn.autocommit = False
        self.assertEqual(run(conn, SHOW), [(b"serializable", b"on", b"off")])
        conn.rollback()
        conn.read_only = False
        self.assertEqual(run(conn, SHOW), [(b"serializable", b"off", b"off")])
        conn.rollback()

    def test_autocommit_ignores_the_options(self) -> None:
        conn = self.connect(autocommit=True)
        conn.isolation_level = IsolationLevel.SERIALIZABLE
        conn.read_only = True
        self.assertEqual(run(conn, SHOW), [(b"read committed", b"off", b"off")])

    def test_closed_connection(self) -> None:
        conn = self.connect()
        conn.close()
        with self.assertRaises(e.OperationalError) as caught:
            conn.read_only = True
        self.assertEqual(str(caught.exception), "the connection is closed")
        self.assertIsNone(conn.read_only)
```

#### CPYTHON-CONN-04-TRANSACTION

Files: `stuntgres/transaction.py`, `tests/test_transaction.py`. Design sections to implement: 5.4; also read: 4.5, 4.6, 5.3.

Step. Write `stuntgres/transaction.py` as printed in 5.4. `Connection.transaction()` already imports it (9.2).

Before the code exists: `ModuleNotFoundError: No module named 'stuntgres.transaction'`.

Tests to write first (17 cases in oracle and in server mode; none runs in unit mode):

`tests/test_transaction.py`:

```python
"""Tests of Connection.transaction() blocks."""
from __future__ import annotations

from typing import Any

from stuntgres import errors as e
from stuntgres._conn_core import IsolationLevel
from stuntgres.transaction import BaseTransaction, Rollback, Transaction
from stuntgres.transaction import quote_identifier
from tests.dbapi_util import ConnTestCase, run, status

COUNT = "select count(*) from t"


class TransactionTest(ConnTestCase):
    def setUp(self) -> None:
        super().setUp()
        self.conn = self.connect(autocommit=True)
        run(self.conn, "create table t (id int primary key)")
        self.seen = self.raw_notices(self.conn)

    def count(self, conn: Any = None) -> int:
        rows = run(self.conn if conn is None else conn, COUNT)
        return int(rows[0][0] or b"0")

    def test_commit(self) -> None:
        conn = self.conn
        tx = conn.transaction()
        self.assertIs(type(tx), Transaction)
        self.assertIsInstance(tx, BaseTransaction)
        self.assertEqual(Transaction.__module__, "stuntgres")
        self.assertIs(tx.connection, conn)
        self.assertIsNone(tx.savepoint_name)
        self.assertIs(tx.force_rollback, False)
        self.assertRegex(
            repr(tx),
            r"^<stuntgres\.Transaction \(inactive, savepoint None\) at 0x[0-9a-f]+>$",
        )
        with tx as entered:
            self.assertIs(entered, tx)
            self.assertEqual(status(conn), "INTRANS")
            self.assertRegex(repr(tx), r"\(active, savepoint None\)")
            run(conn, "insert into t values (1)")
            self.assertEqual(self.count(self.connect(autocommit=True)), 0)
        self.assertEqual(status(conn), "IDLE")
        self.assertEqual(self.count(self.connect(autocommit=True)), 1)
        self.assertEqual(self.seen, [])

    def test_rollback_on_exception(self) -> None:
        conn = self.conn
        with self.assertRaises(ValueError):
            with conn.transaction():
                run(conn, "insert into t values (1)")
                raise ValueError("stop")
        self.assertEqual(status(conn), "IDLE")
        self.assertEqual(self.count(), 0)

    def test_database_error_inside(self) -> None:
        conn = self.conn
        with self.assertRaises(e.IntegrityError) as caught:
            with conn.transaction():
                run(conn, "insert into t values (1)")
                run(conn, "insert into t values (1)")
        self.assertEqual(type(caught.exception).__name__, "UniqueViolation")
        self.assertEqual(status(conn), "IDLE")
        self.assertEqual(self.count(), 0)

    def test_nested(self) -> None:
        conn = self.conn
        with conn.transaction() as outer:
            run(conn, "insert into t values (1)")
            with conn.transaction() as inner:
                self.assertEqual(inner.savepoint_name, "_sg_tx_2")
                run(conn, "insert into t values (2)")
                with conn.transaction() as third:
                    self.assertEqual(third.savepoint_name, "_sg_tx_3")
            with self.assertRaises(ValueError):
                with conn.transaction() as failing:
                    self.assertEqual(failing.savepoint_name, "_sg_tx_2")
                    run(conn, "insert into t values (3)")
                    raise ValueError("stop")
            self.assertEqual(status(conn), "INTRANS")
            self.assertEqual(self.count(), 2)
            self.assertIsNone(outer.savepoint_name)
        self.assertEqual(status(conn), "IDLE")
        self.assertEqual(self.count(), 2)
        self.assertEqual(conn._num_transactions, 0)
        self.assertEqual(self.seen, [])

    def test_nested_error_is_recovered(self) -> None:
        conn = self.conn
        with conn.transaction():
            run(conn, "insert into t values (1)")
            with self.assertRaises(e.DataError):
                with conn.transaction():
                    run(conn, "insert into t values (2)")
                    run(conn, "select 1/0")
            self.assertEqual(status(conn), "INTRANS")
            run(conn, "insert into t values (3)")
        self.assertEqual(run(conn, "select id from t order by id"), [(b"1",), (b"3",)])

    def test_inside_an_implicit_transaction(self) -> None:
        conn = self.connect()
        run(conn, "insert into t values (1)")
        with conn.transaction() as tx:
            self.assertEqual(tx.savepoint_name, "_sg_tx_1")
            run(conn, "insert into t values (2)")
        self.assertEqual(status(conn), "INTRANS")
        self.assertEqual(self.count(), 0)
        conn.commit()
        self.assertEqual(self.count(), 2)

    def test_without_autocommit_the_outermost_block_commits(self) -> None:
        conn = self.connect()
        with conn.transaction():
            run(conn, "insert into t values (1)")
        self.assertEqual(status(conn), "IDLE")
        self.assertEqual(self.count(), 1)

    def test_savepoint_name(self) -> None:
        conn = self.conn
        with conn.transaction('my "sp"') as tx:
            self.assertEqual(tx.savepoint_name, 'my "sp"')
            run(conn, "insert into t values (1)")
            run(conn, 'rollback to "my ""sp"""')
            with conn.transaction("inner one") as inner:
                self.assertEqual(inner.savepoint_name, "inner one")
                run(conn, "insert into t values (2)")
                run(conn, 'rollback to "inner one"')
        self.assertEqual(status(conn), "IDLE")
        self.assertEqual(self.count(), 0)

    def test_force_rollback(self) -> None:
        conn = self.conn
        with conn.transaction(force_rollback=True) as tx:
            self.assertIs(tx.force_rollback, True)
            run(conn, "insert into t values (1)")
        self.assertEqual(status(conn), "IDLE")
        self.assertEqual(self.count(), 0)
        with conn.transaction():
            run(conn, "insert into t values (1)")
            with conn.transaction(force_rollback=True):
                run(conn, "insert into t values (2)")
        self.assertEqual(run(conn, "select id from t"), [(b"1",)])

    def test_rollback_exception(self) -> None:
        conn = self.conn
        with conn.transaction():
            run(conn, "insert into t values (1)")
            raise Rollback()
        self.assertEqual(self.count(), 0)
        with conn.transaction():
            run(conn, "insert into t values (1)")
            with conn.transaction():
                run(conn, "insert into t values (2)")
                raise Rollback
            run(conn, "insert into t values (3)")
        self.assertEqual(run(conn, "select id from t order by id"), [(b"1",), (b"3",)])
        with conn.transaction() as outer:
            run(conn, "insert into t values (4)")
            with conn.transaction():
                raise Rollback(outer)
            self.fail("the outer block was left")  # pragma: no cover
        self.assertEqual(self.count(), 2)
        with self.assertRaises(Rollback):
            with conn.transaction():
                raise Rollback(BaseTransaction(conn))
        self.assertEqual(Rollback.__module__, "stuntgres")
        self.assertIs(Rollback(outer).transaction, outer)
        self.assertIsNone(Rollback().transaction)

    def test_options_apply_to_the_block(self) -> None:
        conn = self.conn
        conn.isolation_level = IsolationLevel.SERIALIZABLE
        conn.read_only = True
        with conn.transaction():
            shown = run(
                conn,
                "select current_setting('transaction_isolation'),"
                " current_setting('transaction_read_only')",
            )
        self.assertEqual(shown, [(b"serializable", b"on")])

    def test_commit_and_attribute_changes_are_refused_inside(self) -> None:
        conn = self.conn
        with conn.transaction():
            for name in ("commit", "rollback"):
                with self.assertRaises(e.ProgrammingError) as caught:
                    getattr(conn, name)()
                self.assertEqual(
                    str(caught.exception),
                    f"{name}() cannot be used inside a transaction() block",
                )
            with self.assertRaises(e.ProgrammingError):
                conn.autocommit = False
            self.assertEqual(status(conn), "INTRANS")
        conn.commit()

    def test_failing_commit_of_the_block(self) -> None:
        conn = self.conn
        run(conn, "create table p (id int primary key)")
        run(
            conn,
            "create table ch (pid int references p (id) deferrable initially deferred)",
        )
        with self.assertRaises(e.IntegrityError) as caught:
            with conn.transaction():
                run(conn, "insert into ch values (42)")
        self.assertEqual(type(caught.exception).__name__, "ForeignKeyViolation")
        self.assertEqual(status(conn), "IDLE")
        self.assertEqual(conn._num_transactions, 0)

    def test_error_caught_inside_a_nested_block(self) -> None:
        conn = self.conn
        with self.assertRaises(e.InternalError) as caught:
            with conn.transaction():
                with conn.transaction():
                    try:
                        run(conn, "select 1/0")
                    except e.DataError:
                        pass
        self.assertEqual(type(caught.exception).__name__, "InFailedSqlTransaction")
        self.assertEqual(status(conn), "IDLE")

    def test_misuse(self) -> None:
        conn = self.conn
        tx = conn.transaction()
        with tx:
            with self.assertRaises(e.ProgrammingError) as caught:
                tx.__enter__()
            self.assertEqual(
                str(caught.exception), "a transaction block cannot be entered twice"
            )
        with self.assertRaises(e.ProgrammingError) as caught:
            tx.__exit__(None, None, None)
        self.assertEqual(
            str(caught.exception),
            "transaction blocks must be left in the reverse order of entering",
        )
        outer, inner = conn.transaction(), conn.transaction()
        outer.__enter__()
        inner.__enter__()
        with self.assertRaises(e.ProgrammingError):
            outer.__exit__(None, None, None)
        inner.__exit__(None, None, None)
        outer.__exit__(None, None, None)
        self.assertEqual(status(conn), "IDLE")
        conn._tpc = object()
        with self.assertRaises(e.ProgrammingError) as caught:
            conn.transaction().__enter__()
        self.assertEqual(
            str(caught.exception),
            "transaction() cannot be used during a two-phase transaction",
        )
        conn._tpc = None
        self.assertIs(conn.lock.locked(), False)

    def test_closed_connection(self) -> None:
        conn = self.connect(autocommit=True)
        with self.assertRaises(ValueError):
            with conn.transaction():
                conn.close()
                raise ValueError("stop")
        with self.assertRaises(e.OperationalError) as caught:
            with conn.transaction():
                self.fail("entered")  # pragma: no cover
        self.assertEqual(str(caught.exception), "the connection is closed")
        conn = self.connect(autocommit=True)
        with self.assertRaises(e.OperationalError):
            with conn.transaction():
                conn.close()
        self.assertEqual(conn._num_transactions, 0)

    def test_quote_identifier(self) -> None:
        self.assertEqual(quote_identifier("abc"), '"abc"')
        self.assertEqual(quote_identifier('a "b" c'), '"a ""b"" c"')
        self.assertEqual(quote_identifier(""), '""')
        with self.assertRaises(e.ProgrammingError) as caught:
            quote_identifier("a\x00b")
        self.assertEqual(
            str(caught.exception), "an identifier cannot contain NUL characters"
        )
```

#### CPYTHON-CONN-05-NOTICES

Files: `stuntgres/_conn_notices.py`, `tests/test_conn_notices.py`. Design sections to implement: 8.1; also read: 1.4, 3.4, 4.3.

Step. Replace the stub `stuntgres/_conn_notices.py` with the file printed in 8.1.

Before the code exists: With the stub in place: `AttributeError: 'Connection' object has no attribute 'add_notice_handler'`.

Tests to write first (9 cases in oracle and in server mode; none runs in unit mode):

`tests/test_conn_notices.py`:

```python
"""Tests of the notice handlers of a connection."""
from __future__ import annotations

import logging
from typing import Any

from stuntgres._diagnostic import Diagnostic
from tests.dbapi_util import ConnTestCase, run

NOISY = """create function noisy(n int) returns int language plpgsql as $$
begin
  raise notice 'hello %', n;
  raise warning 'careful' using detail = 'a detail', hint = 'a hint',
    errcode = '01234';
  raise info 'za\u017c\u00f3\u0142\u0107';
  return n + 1;
end $$"""
CONTEXT = "PL/pgSQL function noisy(integer) line %d at RAISE"
TEXT = "za\u017c\u00f3\u0142\u0107"


def fields(diag: Diagnostic) -> tuple[Any, ...]:
    return (
        diag.severity, diag.sqlstate, diag.message_primary, diag.message_detail,
        diag.message_hint, diag.context,
    )


class NoticesTest(ConnTestCase):
    def setUp(self) -> None:
        super().setUp()
        self.conn = self.connect(autocommit=True)
        self.seen: list[Any] = []

    def collect(self, diag: Diagnostic) -> None:
        self.seen.append(fields(diag))

    def test_notices_of_a_function(self) -> None:
        conn = self.conn
        run(conn, NOISY)
        conn.add_notice_handler(self.collect)
        self.assertEqual(run(conn, "select noisy(1)"), [(b"2",)])
        self.assertEqual(
            self.seen,
            [
                ("NOTICE", "00000", "hello 1", None, None, CONTEXT % 3),
                ("WARNING", "01234", "careful", "a detail", "a hint", CONTEXT % 4),
                ("INFO", "00000", TEXT, None, None, CONTEXT % 6),
            ],
        )

    def test_the_handler_gets_a_diagnostic(self) -> None:
        got: list[Any] = []
        self.conn.add_notice_handler(got.append)
        run(self.conn, "drop table if exists nosuch_table")
        self.assertEqual(len(got), 1)
        diag = got[0]
        self.assertIs(type(diag), Diagnostic)
        self.assertEqual(diag.severity, "NOTICE")
        self.assertEqual(diag.severity_nonlocalized, "NOTICE")
        self.assertEqual(diag.sqlstate, "00000")
        self.assertEqual(
            diag.message_primary, 'table "nosuch_table" does not exist, skipping'
        )
        self.assertIsNone(diag.message_detail)
        self.assertIsNone(diag.table_name)

    def test_warning_of_the_server(self) -> None:
        self.conn.add_notice_handler(self.collect)
        run(self.conn, "commit")
        self.assertEqual(
            self.seen,
            [("WARNING", "25P01", "there is no transaction in progress", None, None,
              None)],
        )

    def test_several_handlers_and_removal(self) -> None:
        conn = self.conn
        first: list[Any] = []
        second: list[Any] = []

        def one(diag: Diagnostic) -> None:
            first.append(diag.message_primary)

        def two(diag: Diagnostic) -> None:
            second.append(diag.message_primary)

        conn.add_notice_handler(one)
        conn.add_notice_handler(two)
        run(conn, "do $$ begin raise notice 'a'; end $$")
        conn.remove_notice_handler(one)
        run(conn, "do $$ begin raise notice 'b'; end $$")
        conn.remove_notice_handler(two)
        run(conn, "do $$ begin raise notice 'c'; end $$")
        self.assertEqual(first, ["a"])
        self.assertEqual(second, ["a", "b"])
        with self.assertRaises(ValueError):
            conn.remove_notice_handler(one)

    def test_a_raising_handler_is_logged(self) -> None:
        conn = self.conn

        def bad(diag: Diagnostic) -> None:
            raise ValueError("boom")

        conn.add_notice_handler(bad)
        conn.add_notice_handler(self.collect)
        with self.assertLogs("stuntgres", logging.ERROR) as logs:
            rows = run(conn, "do $$ begin raise notice 'a'; end $$; select 5")
        self.assertEqual(rows, [(b"5",)])
        self.assertEqual(len(logs.records), 1)
        record = logs.records[0]
        self.assertEqual(record.levelno, logging.ERROR)
        self.assertRegex(
            record.getMessage(), r"^notice handler <function .*bad at 0x[0-9a-f]+>"
            r" failed: boom$",
        )
        self.assertIsNotNone(record.exc_info)
        self.assertEqual([item[2] for item in self.seen], ["a"])

    def test_client_min_messages(self) -> None:
        conn = self.conn
        run(conn, NOISY)
        conn.add_notice_handler(self.collect)
        run(conn, "set client_min_messages to warning")
        run(conn, "select noisy(2)")
        self.assertEqual([item[0] for item in self.seen], ["WARNING", "INFO"])

    def test_text_is_decoded_with_the_client_encoding(self) -> None:
        conn = self.conn
        run(conn, "set client_encoding to latin2")
        conn.add_notice_handler(self.collect)
        run(conn, "do $$ begin raise notice 'za\u017c\u00f3\u0142\u0107'; end $$")
        self.assertEqual(
            [item[2] for item in self.seen],
            ["za\u017c\u00f3\u0142\u0107"],
        )

    def test_no_handler_no_output(self) -> None:
        self.assertEqual(self.conn._notice_handlers, ())
        self.assertEqual(run(self.conn, "do $$ begin raise notice 'a'; end $$"), [])

    def test_handlers_belong_to_one_connection(self) -> None:
        other = self.connect(autocommit=True)
        self.conn.add_notice_handler(self.collect)
        run(other, "do $$ begin raise notice 'other'; end $$")
        self.assertEqual(self.seen, [])
```

#### CPYTHON-CONN-06-NOTIFY

Files: `stuntgres/_conn_notify.py`, `tests/test_conn_notify.py`. Design sections to implement: 8.2; also read: 1.4, 4.3, 9.2.

Step. Replace the stub `stuntgres/_conn_notify.py` with the file printed in 8.2. `NotifyWindow` is already in `_conn_core.py`.

Before the code exists: With the stub in place: `AttributeError: 'Connection' object has no attribute '_notifies_gen'` (and `add_notify_handler`); notifications are dropped by `ConnCore._on_notify`.

Tests to write first (12 cases in oracle and in server mode; none runs in unit mode):

`tests/test_conn_notify.py`:

```python
"""Tests of notifications: notifies() and the notify handlers."""
from __future__ import annotations

import logging
import threading
import time
from typing import Any

from stuntgres import errors as e
from stuntgres._conn_core import Notify
from tests import support
from tests.dbapi_util import ConnTestCase, run


class NotifyTest(ConnTestCase):
    def setUp(self) -> None:
        super().setUp()
        self.listener = self.connect(autocommit=True)
        self.sender = self.connect(autocommit=True)
        run(self.listener, "listen chan")
        self.pid = self.sender.info.backend_pid

    def take(self, **kwargs: Any) -> list[Notify]:
        return list(self.listener.notifies(**kwargs))

    def test_nothing_arrived(self) -> None:
        self.assertEqual(self.take(timeout=0), [])
        started = time.monotonic()
        self.assertEqual(self.take(timeout=0.2), [])
        elapsed = time.monotonic() - started
        self.assertGreaterEqual(elapsed, 0.2)
        self.assertLess(elapsed, 5.0)
        self.assertIs(self.listener.lock.locked(), False)

    def test_arrives_while_idle(self) -> None:
        run(self.sender, "notify chan, 'one'")
        support.wait_readable(self.listener.fileno())
        self.assertEqual(list(self.listener._notify_backlog), [])
        got = self.take(timeout=10, stop_after=1)
        self.assertEqual(got, [Notify("chan", "one", self.pid)])
        self.assertIs(type(got[0]), Notify)

    def test_stop_after_leaves_the_rest(self) -> None:
        run(
            self.sender,
            "notify chan, 'two'; notify chan, 'three';"
            " select pg_notify('chan', 'four')",
        )
        payloads = [n.payload for n in self.take(timeout=10, stop_after=1)]
        self.assertEqual(payloads, ["two"])
        payloads = [n.payload for n in self.take(timeout=10, stop_after=2)]
        self.assertEqual(payloads, ["three", "four"])
        self.assertEqual(self.take(timeout=0), [])
        self.assertEqual(self.take(timeout=10, stop_after=0), [])

    def test_received_during_another_statement(self) -> None:
        run(self.sender, "notify chan, 'five'")
        support.wait_readable(self.listener.fileno())
        self.assertEqual(run(self.listener, "select 1"), [(b"1",)])
        self.assertEqual(
            list(self.listener._notify_backlog), [Notify("chan", "five", self.pid)]
        )
        self.assertEqual(self.take(timeout=0), [Notify("chan", "five", self.pid)])
        self.assertEqual(self.take(timeout=0), [])

    def test_own_notifications_and_payloads(self) -> None:
        listener = self.listener
        own = listener.info.backend_pid
        run(listener, "notify chan")
        run(listener, "select pg_notify('chan', 'za\u017c\u00f3\u0142\u0107')")
        self.assertEqual(
            self.take(timeout=0),
            [
                Notify("chan", "", own),
                Notify("chan", "za\u017c\u00f3\u0142\u0107", own),
            ],
        )

    def test_the_loop_body_may_use_the_connection(self) -> None:
        run(self.sender, "notify chan, 'six'")
        answers = []
        for notify in self.listener.notifies(timeout=10, stop_after=1):
            answers.append((notify.payload, run(self.listener, "select 2")))
        self.assertEqual(answers, [("six", [(b"2",)])])

    def test_delivered_at_commit(self) -> None:
        sender = self.sender
        sender.autocommit = False
        run(sender, "notify chan, 'seven'")
        self.assertEqual(self.take(timeout=0.2), [])
        sender.commit()
        self.assertEqual(
            self.take(timeout=10, stop_after=1), [Notify("chan", "seven", self.pid)]
        )

    def test_handlers(self) -> None:
        listener = self.listener
        got: list[Notify] = []

        def bad(notify: Notify) -> None:
            raise ValueError("boom")

        listener.add_notify_handler(bad)
        listener.add_notify_handler(got.append)
        run(self.sender, "notify chan, 'eight'")
        support.wait_readable(listener.fileno())
        with self.assertLogs("stuntgres", logging.ERROR) as logs:
            self.assertEqual(self.take(timeout=0), [])
        self.assertEqual(got, [Notify("chan", "eight", self.pid)])
        self.assertEqual(list(listener._notify_backlog), [])
        self.assertEqual(len(logs.records), 1)
        self.assertRegex(
            logs.records[0].getMessage(),
            r"^notify handler <function .*bad at 0x[0-9a-f]+> failed: boom$",
        )
        listener.remove_notify_handler(bad)
        run(self.sender, "notify chan, 'nine'")
        support.wait_readable(listener.fileno())
        self.assertEqual(run(listener, "select 1"), [(b"1",)])
        self.assertEqual([n.payload for n in got], ["eight", "nine"])
        listener.remove_notify_handler(got.append)
        with self.assertRaises(ValueError):
            listener.remove_notify_handler(bad)
        run(self.sender, "notify chan, 'ten'")
        self.assertEqual(
            [n.payload for n in self.take(timeout=10, stop_after=1)], ["ten"]
        )

    def test_unlisten(self) -> None:
        run(self.listener, "unlisten chan")
        run(self.sender, "notify chan, 'gone'")
        self.assertEqual(self.take(timeout=0.2), [])

    def test_other_channel_and_several_listeners(self) -> None:
        second = self.connect(autocommit=True)
        run(second, "listen chan")
        run(second, "listen other")
        run(self.sender, "notify other, 'x'; notify chan, 'y'")
        self.assertEqual(
            [(n.channel, n.payload) for n in second.notifies(timeout=10, stop_after=2)],
            [("other", "x"), ("chan", "y")],
        )
        self.assertEqual(
            [(n.channel, n.payload) for n in self.take(timeout=10, stop_after=1)],
            [("chan", "y")],
        )

    def test_close_while_another_thread_listens(self) -> None:
        listener = self.listener
        outcome: list[Any] = []

        def listen() -> None:
            try:
                outcome.append(list(listener.notifies()))
            except e.OperationalError as exc:
                outcome.append(str(exc))

        thread = threading.Thread(target=listen)
        thread.start()
        self.addCleanup(thread.join)
        # No synchronisation is needed: wherever the thread is, it must end
        # with this error within one wait of notifies().
        started = time.monotonic()
        listener.close()
        thread.join(10)
        self.assertLess(time.monotonic() - started, 5.0)
        self.assertEqual(outcome, ["the connection is closed"])

    def test_closed_connection(self) -> None:
        self.listener.close()
        with self.assertRaises(e.OperationalError) as caught:
            self.take(timeout=0)
        self.assertEqual(str(caught.exception), "the connection is closed")
        self.assertIs(self.listener.lock.locked(), False)
```

#### CPYTHON-CONN-07-CANCEL

Files: `stuntgres/_conn_cancel.py`, `tests/test_conn_cancel.py`. Design sections to implement: 8.3, 8.4; also read: 4.3, 9.2.

Step. Replace the stub `stuntgres/_conn_cancel.py` with the file printed in 8.3. The interruption path itself is already in `Connection.wait` (8.4); this part supplies `cancel`, `_cancel_gen` and `_interrupt_failed`, which it calls. The three `KeyboardInterrupt` tests send a real `SIGINT` to the main thread from a helper thread; they install `signal.default_int_handler` first and skip themselves when not run on the main thread. A test that hangs for 30 s means the interrupt was lost: stop and report, do not add a sleep.

Before the code exists: With the stub in place: `AttributeError: 'Announcing' object has no attribute 'cancel'` (and `_cancel_gen`).

Tests to write first (6 cases in oracle and in server mode; none runs in unit mode):

`tests/test_conn_cancel.py`:

```python
"""Tests of cancel() and of the way out of an interrupted wait."""
from __future__ import annotations

import gc
import logging
import signal
import threading
from typing import Any, Callable

from stuntgres import _gentypes
from stuntgres import connection as connection_module
from stuntgres import errors as e
from stuntgres import pq
from stuntgres.connection import Connection
from tests.dbapi_util import ConnTestCase, run, status

SLEEP = "do $$ begin raise notice 'started'; perform pg_sleep(30); end $$"


def announcing(inner: Any, seen: list[bool], started: threading.Event) -> Any:
    """Forward to `inner`; set `started` once the statement is known to run.

    The event is set from generator code, never inside the notice callback,
    where a KeyboardInterrupt would be logged and dropped.
    """
    try:
        wanted = next(inner)
        while True:
            if seen:
                started.set()
            wanted = inner.send((yield wanted))
    except StopIteration as stop:
        return stop.value
    finally:
        inner.close()


class Announcing(Connection):
    """A connection that tells a helper thread when its statement runs."""

    def prepare(self) -> None:
        self.seen: list[bool] = []
        self.started = threading.Event()
        # Remember that a notice came, not the notice: its result is borrowed.
        self.pgconn.notice_handler = lambda result: self.seen.append(True)

    def wait(self, gen: Any, interval: float | None = None) -> Any:
        return super().wait(announcing(gen, self.seen, self.started), interval)


def live(cls: type) -> int:
    gc.collect()
    return sum(1 for obj in gc.get_objects() if type(obj) is cls)


class CancelTest(ConnTestCase):
    def announcing_connection(self, **kwargs: Any) -> Announcing:
        conn = Announcing.connect(self.conninfo, **kwargs)
        self.addCleanup(conn.close)
        conn.prepare()
        return conn

    def helper(self, conn: Announcing, action: Callable[[], None]) -> None:
        """Run `action` in a thread once the statement of `conn` runs."""

        def body() -> None:
            if conn.started.wait(10):
                action()

        thread = threading.Thread(target=body)
        thread.start()
        self.addCleanup(thread.join)

    def interrupt_main(self) -> None:
        signal.pthread_kill(threading.main_thread().ident or 0, signal.SIGINT)

    def need_main_thread(self) -> None:
        if threading.current_thread() is not threading.main_thread():
            self.skipTest("signals are delivered to the main thread")
        previous = signal.signal(signal.SIGINT, signal.default_int_handler)
        self.addCleanup(signal.signal, signal.SIGINT, previous)

    def test_cancel_with_nothing_running(self) -> None:
        conn = self.connect(autocommit=True)
        conn.cancel()
        conn.cancel_safe()
        conn.cancel_safe(timeout=1.0)
        self.assertEqual(run(conn, "select 1"), [(b"1",)])
        conn.close()
        conn.cancel()
        conn.cancel_safe()

    def test_cancel_from_another_thread(self) -> None:
        conn = self.announcing_connection(autocommit=True)
        self.helper(conn, conn.cancel)
        with self.assertRaises(e.OperationalError) as caught:
            run(conn, SLEEP)
        error = caught.exception
        self.assertEqual(type(error).__name__, "QueryCanceled")
        self.assertEqual(error.sqlstate, "57014")
        self.assertEqual(
            error.diag.message_primary, "canceling statement due to user request"
        )
        self.assertEqual(status(conn), "IDLE")
        self.assertIs(conn.lock.locked(), False)
        self.assertEqual(run(conn, "select 1"), [(b"1",)])

    def test_keyboard_interrupt_in_autocommit(self) -> None:
        self.need_main_thread()
        conn = self.announcing_connection(autocommit=True)
        results, handles = live(pq.PGresult), live(pq.PGcancel)
        self.helper(conn, self.interrupt_main)
        with self.assertRaises(KeyboardInterrupt):
            run(conn, SLEEP)
        self.assertIs(conn.closed, False)
        self.assertEqual(status(conn), "IDLE")
        self.assertIs(conn.lock.locked(), False)
        self.assertEqual(live(pq.PGresult), results)
        self.assertEqual(live(pq.PGcancel), handles)
        self.assertEqual(run(conn, "select 1"), [(b"1",)])

    def test_keyboard_interrupt_in_a_transaction(self) -> None:
        self.need_main_thread()
        conn = self.announcing_connection()
        run(conn, "create table t (id int)")
        self.helper(conn, self.interrupt_main)
        with self.assertRaises(KeyboardInterrupt):
            run(conn, SLEEP)
        self.assertEqual(status(conn), "INERROR")
        with self.assertRaises(e.InternalError) as caught:
            run(conn, "select 1")
        self.assertEqual(caught.exception.sqlstate, "25P02")
        conn.rollback()
        self.assertEqual(status(conn), "IDLE")
        count = "select count(*) from pg_class where relname = 't'"
        self.assertEqual(run(conn, count), [(b"0",)])

    def test_statement_that_cannot_be_ended_closes_the_connection(self) -> None:
        self.need_main_thread()
        conn = self.announcing_connection(autocommit=True)

        def never() -> Any:
            while True:
                yield _gentypes.Wait.R

        conn._cancel_gen = never  # type: ignore[method-assign]
        self.addCleanup(setattr, connection_module, "CANCEL_TIMEOUT", 5.0)
        connection_module.CANCEL_TIMEOUT = 0.2
        self.helper(conn, self.interrupt_main)
        with self.assertLogs("stuntgres", logging.WARNING) as logs:
            with self.assertRaises(KeyboardInterrupt):
                run(conn, SLEEP)
        self.assertEqual(
            [record.getMessage() for record in logs.records],
            [
                "cannot end the interrupted statement (WaitTimeout: timeout"
                " expired): closing the connection"
            ],
        )
        self.assertIs(conn.closed, True)
        self.assertIs(conn.broken, True)
        self.assertIs(conn.lock.locked(), False)
        with self.assertRaises(e.OperationalError) as caught:
            run(conn, "select 1")
        self.assertEqual(str(caught.exception), "the connection is lost")
        conn.close()
        self.assertIs(conn.broken, False)

    def test_interrupt_when_the_statement_already_ended(self) -> None:
        conn = self.connect(autocommit=True)
        run(conn, "select 1")
        with conn.lock:
            conn.wait(conn._cancel_gen())
        self.assertEqual(status(conn), "IDLE")
        self.assertEqual(run(conn, "select 2"), [(b"2",)])
```

#### CPYTHON-CUR-01A-COLUMN

Files: `stuntgres/_column.py`, `tests/test_column.py`. Design sections to implement: 6.2.

Step. Write `stuntgres/_column.py` as printed in 6.2.

Before the code exists: `ModuleNotFoundError: No module named 'stuntgres._column'`.

Tests to write first (6 cases in oracle and in server mode, of which 4 also run in unit mode):

`tests/test_column.py`:

```python
"""Tests of the column objects of Cursor.description."""
from __future__ import annotations

import unittest
from typing import Any

from stuntgres import dbapi20, pq
from stuntgres._column import Column
from tests import support


class FakeResult:
    """What Column reads of a pq.PGresult: one column."""

    def __init__(self, name: bytes | None, ftype: int, fmod: int, fsize: int) -> None:
        self._column = (name, ftype, fmod, fsize)

    def fname(self, index: int) -> bytes | None:
        return self._column[0]

    def ftype(self, index: int) -> int:
        return self._column[1]

    def fmod(self, index: int) -> int:
        return self._column[2]

    def fsize(self, index: int) -> int:
        return self._column[3]


def column(ftype: int, fmod: int, fsize: int, name: bytes | None = b"c") -> Column:
    result: Any = FakeResult(name, ftype, fmod, fsize)
    return Column(result, 0)


class ColumnUnitTest(unittest.TestCase):
    def test_sequence(self) -> None:
        col = column(1700, 655366, -1, b"price")
        self.assertEqual(len(col), 7)
        self.assertEqual(tuple(col), ("price", 1700, None, None, 10, 2, None))
        self.assertEqual(col[0], "price")
        self.assertEqual(col[1], 1700)
        self.assertEqual(col[-1], None)
        self.assertEqual(col[4:6], (10, 2))
        self.assertEqual(
            (col.name, col.type_code, col.display_size, col.internal_size),
            ("price", 1700, None, None),
        )
        self.assertEqual((col.precision, col.scale, col.null_ok), (10, 2, None))
        with self.assertRaises(IndexError):
            col[7]
        self.assertEqual(repr(col), "<Column 'price', type OID 1700>")
        self.assertEqual(Column.__module__, "stuntgres")

    def test_name(self) -> None:
        self.assertEqual(column(23, -1, 4, None).name, "column_1")
        result: Any = FakeResult(b"za\xbf\xf3\xb3\xe6", 23, -1, 4)
        self.assertEqual(
            Column(result, 0, "iso8859-2").name, "za\u017c\u00f3\u0142\u0107"
        )
        self.assertEqual(
            Column(result, 3, "iso8859-2").name,
            Column(result, 0, "iso8859-2").name,
        )

    def test_type_objects(self) -> None:
        self.assertTrue(column(1043, 14, -1)[1] == dbapi20.STRING)
        self.assertTrue(column(23, -1, 4).type_code == dbapi20.NUMBER)
        self.assertFalse(column(23, -1, 4).type_code == dbapi20.STRING)

    def test_derived_values(self) -> None:
        # (ftype, fmod, fsize) -> (display_size, internal_size, precision, scale)
        cases = {
            (16, -1, 1): (None, 1, None, None),              # bool
            (21, -1, 2): (None, 2, None, None),              # int2
            (23, -1, 4): (None, 4, None, None),              # int4
            (20, -1, 8): (None, 8, None, None),              # int8
            (700, -1, 4): (None, 4, None, None),             # float4
            (701, -1, 8): (None, 8, None, None),             # float8
            (1700, -1, -1): (None, None, None, None),        # numeric
            (1700, 655366, -1): (None, None, 10, 2),         # numeric(10,2)
            (1700, 327684, -1): (None, None, 5, 0),          # numeric(5)
            (1700, 65536504, -1): (None, None, 1000, 500),   # numeric(1000,500)
            (1700, 198658, -1): (None, None, 3, -2),         # numeric(3,-2)
            (25, -1, -1): (None, None, None, None),          # text
            (1043, -1, -1): (None, None, None, None),        # varchar
            (1043, 14, -1): (10, None, None, None),          # varchar(10)
            (1042, 5, -1): (1, None, None, None),            # char
            (1042, 9, -1): (5, None, None, None),            # char(5)
            (19, -1, 64): (None, 64, None, None),            # name
            (17, -1, -1): (None, None, None, None),          # bytea
            (1082, -1, 4): (None, 4, None, None),            # date
            (1083, -1, 8): (None, 8, None, None),            # time
            (1083, 3, 8): (None, 8, 3, None),                # time(3)
            (1266, 0, 12): (None, 12, 0, None),              # timetz(0)
            (1114, 6, 8): (None, 8, 6, None),                # timestamp(6)
            (1184, 4, 8): (None, 8, 4, None),                # timestamptz(4)
            (1186, -1, 16): (None, 16, None, None),          # interval
            (1186, 2147418115, 16): (None, 16, 3, None),     # interval(3)
            (1186, 470351871, 16): (None, 16, None, None),   # interval day to second
            (1186, 268435458, 16): (None, 16, 2, None),      # interval second(2)
            (2950, -1, 16): (None, 16, None, None),          # uuid
            (3802, -1, -1): (None, None, None, None),        # jsonb
            (1015, 14, -1): (None, None, None, None),        # varchar(10)[]
            (1560, 3, -1): (3, None, None, None),            # bit(3)
            (1562, 8, -1): (8, None, None, None),            # varbit(8)
            (1562, -1, -1): (None, None, None, None),        # varbit
        }
        for (ftype, fmod, fsize), expected in cases.items():
            col = column(ftype, fmod, fsize)
            self.assertEqual(tuple(col[2:6]), expected, (ftype, fmod, fsize))
            self.assertIsNone(col.null_ok)


class ColumnTest(support.DatabaseTestCase):
    """The values the fake results above stand for come from the server."""

    def setUp(self) -> None:
        super().setUp()
        self.pgconn = pq.PGconn.connect(self.conninfo.encode())
        self.addCleanup(self.pgconn.finish)
        self.assertEqual(self.pgconn.status, 0, self.pgconn.error_message)

    def columns(self, sql: str) -> list[tuple[Any, ...]]:
        result = self.pgconn.exec_(sql.encode())
        self.assertEqual(result.status, pq.ExecStatus.TUPLES_OK, result.error_message)
        return [tuple(Column(result, i)) for i in range(result.nfields)]

    def test_expressions(self) -> None:
        self.assertEqual(
            self.columns(
                "select 1::int4, 1.5::numeric(10,2), 'a'::varchar(10), 'a'::char(5),"
                " '10:00'::time(3), '1 day'::interval(3), 'a'::text,"
                " '{1}'::int4[] as arr, 1.5::numeric(3,-2),"
                " now()::timestamptz(4) as ts, 1 as \"A b\""
            ),
            [
                ("int4", 23, None, 4, None, None, None),
                ("numeric", 1700, None, None, 10, 2, None),
                ("varchar", 1043, 10, None, None, None, None),
                ("bpchar", 1042, 5, None, None, None, None),
                ("time", 1083, None, 8, 3, None, None),
                ("interval", 1186, None, 16, 3, None, None),
                ("text", 25, None, None, None, None, None),
                ("arr", 1007, None, None, None, None, None),
                ("numeric", 1700, None, None, 3, -2, None),
                ("ts", 1184, None, 8, 4, None, None),
                ("A b", 23, None, 4, None, None, None),
            ],
        )

    def test_table_columns(self) -> None:
        result = self.pgconn.exec_(
            b"create table cols (id serial primary key, name varchar(20) not null,"
            b" price numeric(8,3), at timestamp(2), note text)"
        )
        self.assertEqual(result.status, pq.ExecStatus.COMMAND_OK, result.error_message)
        self.assertEqual(
            self.columns("select * from cols"),
            [
                ("id", 23, None, 4, None, None, None),
                ("name", 1043, 20, None, None, None, None),
                ("price", 1700, None, None, 8, 3, None),
                ("at", 1114, None, 8, 2, None, None),
                ("note", 25, None, None, None, None, None),
            ],
        )
        self.assertEqual(
            self.columns("select count(*), max(name), price * 2, 1, null from cols"
                         " group by price"),
            [
                ("count", 20, None, 8, None, None, None),
                ("max", 25, None, None, None, None, None),
                ("?column?", 1700, None, None, None, None, None),
                ("?column?", 23, None, 4, None, None, None),
                ("?column?", 25, None, None, None, None, None),
            ],
        )
```

#### CPYTHON-CUR-01B-RESULTS

Files: `stuntgres/_cur_results.py`, `tests/test_cur_results.py`. Design sections to implement: 6.3; also read: 3.5.

Step. Write `stuntgres/_cur_results.py` as printed in 6.3.

Before the code exists: `ModuleNotFoundError: No module named 'stuntgres._cur_results'`.

Tests to write first (8 cases in oracle and in server mode; none runs in unit mode):

`tests/test_cur_results.py`:

```python
"""Tests of the result check a cursor applies before it shows results."""
from __future__ import annotations

from typing import Any

from stuntgres import errors as e
from stuntgres import generators, pq, waiting
from stuntgres._cur_results import check_results
from tests import support

IDLE = pq.TransactionStatus.IDLE


class FakeResult:
    def __init__(self, status: int) -> None:
        self.status = status
        self.cleared = False

    def clear(self) -> None:
        self.cleared = True


class CheckResultsTest(support.DatabaseTestCase):
    def setUp(self) -> None:
        super().setUp()
        gen = generators.connect(self.conninfo.encode())
        self.pgconn = waiting.wait_conn(gen, timeout=10)
        self.addCleanup(self.pgconn.finish)

    def results(self, sql: str) -> list[Any]:
        self.pgconn.send_query(sql.encode())
        gen = generators.execute(self.pgconn)
        return waiting.wait(gen, self.pgconn.socket, timeout=10)

    def check(self, results: list[Any]) -> None:
        waiting.wait(
            check_results(self.pgconn, results),
            self.pgconn.socket,
            timeout=30,
        )

    def test_finished_statements_pass(self) -> None:
        results = self.results("select 1; create table t (id int); ; select 2")
        self.check(results)
        self.assertEqual([r.status for r in results], [2, 1, 2])
        self.assertEqual(results[2].get_rows(0, 1), [(b"2",)])
        empty = self.results("")
        self.check(empty)
        self.assertEqual([r.status for r in empty], [pq.ExecStatus.EMPTY_QUERY])

    def test_an_error_releases_every_result(self) -> None:
        results = self.results("select 1; select 1/0; select 2")
        self.assertEqual([r.status for r in results], [2, 7])
        with self.assertRaises(e.DataError) as caught:
            self.check(results)
        self.assertEqual(type(caught.exception).__name__, "DivisionByZero")
        self.assertEqual(str(caught.exception), "division by zero")
        self.assertEqual([r.pgresult_ptr for r in results], [None, None])
        self.assertEqual(self.pgconn.transaction_status, IDLE)

    def test_no_result(self) -> None:
        with self.assertRaises(e.InternalError) as caught:
            self.check([])
        self.assertEqual(str(caught.exception), "the statement returned no result")

    def test_unexpected_status(self) -> None:
        fakes = [FakeResult(pq.ExecStatus.TUPLES_OK), FakeResult(9), FakeResult(2)]
        with self.assertRaises(e.InternalError) as caught:
            self.check(fakes)
        self.assertEqual(
            str(caught.exception), "unexpected result status SINGLE_TUPLE"
        )
        self.assertEqual([fake.cleared for fake in fakes], [True, True, True])

    def copy_is_refused(self, sql: str) -> None:
        results = self.results(sql)
        with self.assertRaises(e.ProgrammingError) as caught:
            self.check(results)
        self.assertIs(type(caught.exception), e.ProgrammingError)
        self.assertEqual(
            str(caught.exception), "COPY cannot be run with execute() or executemany()"
        )
        self.assertEqual([r.pgresult_ptr for r in results], [None] * len(results))
        self.assertEqual(self.pgconn.transaction_status, IDLE)
        self.assertEqual(self.results("select 5")[0].get_rows(0, 1), [(b"5",)])

    def test_copy_out_is_abandoned(self) -> None:
        self.copy_is_refused("copy (select 1) to stdout")

    def test_large_copy_out_is_abandoned(self) -> None:
        self.copy_is_refused(
            "copy (select g, repeat('x', 100) from generate_series(1, 100000) g)"
            " to stdout"
        )

    def test_copy_in_is_abandoned(self) -> None:
        self.check(self.results("create table t (id int)"))
        self.copy_is_refused("copy t from stdin")
        self.assertEqual(
            self.results("select count(*) from t")[0].get_rows(0, 1), [(b"0",)]
        )

    def test_copy_among_other_statements(self) -> None:
        self.copy_is_refused(
            "select 1; copy (select 1) to stdout; copy (select 2) to stdout; select 3"
        )
```

#### CPYTHON-CUR-01C-CORE

Files: `stuntgres/_cur_core.py`, `stuntgres/_cursor_base.py`, `stuntgres/cursor.py`, `tests/test_cursor_core.py`; stubs `stuntgres/_cur_fetch.py`, `stuntgres/_cur_many.py`, `stuntgres/_cur_nextset.py`, `stuntgres/_cur_optional.py`. Design sections to implement: 1.3, 6.1, 6.4, 6.10, 9.3; also read: 6.3, 7, 9.1.

Step. 1. Write `stuntgres/_cur_core.py` as printed in 6.4. 2. Create the four stub files of 1.3 (`_cur_fetch.py`, `_cur_many.py`, `_cur_nextset.py`, `_cur_optional.py`). 3. Write `stuntgres/_cursor_base.py` as printed in 6.10 and `stuntgres/cursor.py` as printed in 9.3: the complete class; its fetch methods, `executemany` and `scroll` raise `AttributeError` until their mixins land. The tests of this part read rows through `cur.pgresult.get_rows` (the helper `raw_rows` of the test file), not through a fetch method.

Before the code exists: `ModuleNotFoundError: No module named 'stuntgres._cursor_base'`.

Tests to write first (7 cases in oracle and in server mode, of which 1 also run in unit mode):

`tests/test_cursor_core.py`:

```python
"""Tests of the cursor core: execute and the facts about its result."""
from __future__ import annotations

import unittest
from stuntgres import adapt
from stuntgres import errors as e
from stuntgres import rows
from stuntgres._column import Column
from stuntgres._cursor_base import BaseCursor
from stuntgres.cursor import Cursor
from tests.dbapi_util import ConnTestCase, status


def raw_rows(cur: Cursor) -> list[tuple[bytes | None, ...]]:
    """The cells of the current result as the server sent them."""
    assert cur.pgresult is not None
    return cur.pgresult.get_rows(0, cur.pgresult.ntuples)


class CompositionTest(unittest.TestCase):
    def test_bases(self) -> None:
        self.assertEqual(
            [cls.__name__ for cls in Cursor.__mro__],
            [
                "Cursor", "CursorFeatures", "BaseCursor", "BaseCursorFeatures",
                "OptionalMixin", "NextsetMixin", "ManyMixin", "FetchMixin",
                "CurCore", "object",
            ],
        )
        self.assertEqual(Cursor.__module__, "stuntgres")
        self.assertTrue(issubclass(Cursor, BaseCursor))


class CursorCoreTest(ConnTestCase):
    def test_new_cursor(self) -> None:
        conn = self.connect()
        cur = conn.cursor()
        self.assertIs(type(cur), Cursor)
        self.assertIs(cur.connection, conn)
        self.assertEqual(cur.arraysize, 1)
        self.assertIsNone(cur.description)
        self.assertEqual(cur.rowcount, -1)
        self.assertIsNone(cur.statusmessage)
        self.assertIsNone(cur.pgresult)
        self.assertIs(cur.closed, False)
        self.assertIs(cur.row_factory, rows.tuple_row)
        self.assertIsInstance(cur.adapters, adapt.AdaptersMap)
        self.assertIsNot(cur.adapters, conn.adapters)
        self.assertIs(adapt.context_pgconn(cur), conn.pgconn)
        self.assertRegex(
            repr(cur),
            r"^<stuntgres\.Cursor \[no result\] at 0x[0-9a-f]+>$",
        )
        self.assertEqual(status(conn), "IDLE")

    def test_execute_returns_the_cursor(self) -> None:
        conn = self.connect()
        cur = conn.cursor()
        self.assertIs(cur.execute("select 1 as one, 'x' as two"), cur)
        self.assertEqual(raw_rows(cur), [(b"1", b"x")])
        self.assertEqual(cur.rowcount, 1)
        self.assertEqual(cur.statusmessage, "SELECT 1")
        self.assertEqual(status(conn), "INTRANS")
        self.assertRegex(repr(cur), r"^<stuntgres\.Cursor \[TUPLES_OK\] at 0x")
        description = cur.description
        assert description is not None
        self.assertIs(type(description[0]), Column)
        self.assertEqual(
            [tuple(column) for column in description],
            [
                ("one", 23, None, 4, None, None, None),
                ("two", 25, None, None, None, None, None),
            ],
        )
        self.assertIs(cur.description, description)

    def test_connection_execute(self) -> None:
        conn = self.connect()
        cur = conn.execute("select %s::int + 1", (41,))
        self.assertIs(type(cur), Cursor)
        self.assertEqual(raw_rows(cur), [(b"42",)])
        self.assertIsNot(conn.execute("select 1"), cur)

    def test_statement_without_rows(self) -> None:
        conn = self.connect()
        cur = conn.cursor()
        cur.execute("create table t (id int primary key, v text)")
        self.assertIsNone(cur.description)
        self.assertEqual(cur.rowcount, -1)
        self.assertEqual(cur.statusmessage, "CREATE TABLE")
        self.assertRegex(repr(cur), r"\[COMMAND_OK\]")
        cur.execute("insert into t values (1, 'a'), (2, 'b')")
        self.assertIsNone(cur.description)
        self.assertEqual(cur.rowcount, 2)
        self.assertEqual(cur.statusmessage, "INSERT 0 2")
        cur.execute("select id, v from t where false")
        self.assertEqual(cur.rowcount, 0)
        self.assertEqual(cur.statusmessage, "SELECT 0")
        description = cur.description
        assert description is not None
        self.assertEqual([column.name for column in description], ["id", "v"])
        cur.execute("")
        self.assertIsNone(cur.description)
        self.assertEqual(cur.rowcount, -1)
        self.assertIsNone(cur.statusmessage)
        self.assertRegex(repr(cur), r"\[EMPTY_QUERY\]")

    def test_error_leaves_no_result(self) -> None:
        conn = self.connect()
        cur = conn.cursor()
        cur.execute("select 1")
        with self.assertRaises(e.ProgrammingError) as caught:
            cur.execute("select * from nosuch")
        self.assertEqual(type(caught.exception).__name__, "UndefinedTable")
        self.assertIsNone(cur.pgresult)
        self.assertIsNone(cur.description)
        self.assertEqual(cur.rowcount, -1)
        self.assertIsNone(cur.statusmessage)
        self.assertEqual(status(conn), "INERROR")
        self.assertIs(conn.lock.locked(), False)

    def test_close(self) -> None:
        conn = self.connect()
        cur = conn.cursor()
        cur.execute("select 1")
        cur.close()
        self.assertIs(cur.closed, True)
        self.assertIsNone(cur.description)
        self.assertEqual(cur.rowcount, -1)
        self.assertIsNone(cur.pgresult)
        self.assertRegex(repr(cur), r"^<stuntgres\.Cursor \[closed\] at 0x")
        cur.close()
        with self.assertRaises(e.InterfaceError) as caught:
            cur.execute("select 1")
        self.assertIs(type(caught.exception), e.InterfaceError)
        self.assertEqual(str(caught.exception), "the cursor is closed")
        with conn.cursor() as managed:
            self.assertIs(managed.execute("select 1"), managed)
        self.assertIs(managed.closed, True)
```

#### CPYTHON-CUR-02-FETCH

Files: `stuntgres/_cur_fetch.py`, `tests/test_cursor_fetch.py`. Design sections to implement: 6.5; also read: 6.1, 9.3.

Step. Replace the stub `stuntgres/_cur_fetch.py` with the file printed in 6.5.

Before the code exists: With the stub in place every test raises `AttributeError: 'Cursor' object has no attribute '_fetchone'` (or `rownumber`, `_fetchmany`, `_fetchall`, `_scroll`).

Tests to write first (12 cases in oracle and in server mode; none runs in unit mode):

`tests/test_cursor_fetch.py`:

```python
"""Tests of fetchone, fetchmany, fetchall, iteration, scroll and rownumber."""
from __future__ import annotations

import datetime as dt
from decimal import Decimal

from stuntgres import errors as e
from stuntgres import rows
from tests.dbapi_util import ConnTestCase

SIX = "select g, 'r' || g from generate_series(1, 6) g"
NO_RESULT = "no result available: execute a statement first"
NO_ROWS = "the last statement returned no rows"


class FetchTest(ConnTestCase):
    def setUp(self) -> None:
        super().setUp()
        self.conn = self.connect()
        self.cur = self.conn.cursor()

    def test_fetchone(self) -> None:
        cur = self.cur
        cur.execute("select g, 'r' || g from generate_series(1, 2) g")
        self.assertEqual(cur.rownumber, 0)
        self.assertEqual(cur.fetchone(), (1, "r1"))
        self.assertEqual(cur.rownumber, 1)
        self.assertEqual(cur.fetchone(), (2, "r2"))
        self.assertEqual(cur.rownumber, 2)
        self.assertIsNone(cur.fetchone())
        self.assertIsNone(cur.fetchone())
        self.assertEqual(cur.rownumber, 2)
        self.assertEqual(cur.rowcount, 2)

    def test_fetchmany(self) -> None:
        cur = self.cur
        cur.execute(SIX)
        self.assertEqual(cur.fetchmany(), [(1, "r1")])
        self.assertEqual(cur.fetchmany(2), [(2, "r2"), (3, "r3")])
        cur.arraysize = 2
        self.assertEqual(cur.fetchmany(), [(4, "r4"), (5, "r5")])
        self.assertEqual(cur.fetchmany(10), [(6, "r6")])
        self.assertEqual(cur.fetchmany(10), [])
        self.assertEqual(cur.fetchmany(), [])
        self.assertEqual(cur.rownumber, 6)
        cur.execute(SIX)
        self.assertEqual(cur.fetchmany(0), [])
        self.assertEqual(cur.fetchmany(-3), [])
        self.assertEqual(cur.rownumber, 0)
        self.assertEqual(len(cur.fetchmany(100)), 6)

    def test_fetchall(self) -> None:
        cur = self.cur
        cur.execute(SIX)
        self.assertEqual(cur.fetchone(), (1, "r1"))
        self.assertEqual(
            cur.fetchall(), [(2, "r2"), (3, "r3"), (4, "r4"), (5, "r5"), (6, "r6")]
        )
        self.assertEqual(cur.fetchall(), [])
        self.assertIsNone(cur.fetchone())

    def test_iteration(self) -> None:
        cur = self.cur
        cur.execute(SIX)
        self.assertIs(iter(cur), cur)
        self.assertEqual(next(cur), (1, "r1"))
        self.assertEqual([row[0] for row in cur], [2, 3, 4, 5, 6])
        with self.assertRaises(StopIteration):
            next(cur)
        self.assertEqual(list(cur.execute(SIX))[5], (6, "r6"))
        self.assertEqual(self.conn.execute("select 7").fetchone(), (7,))

    def test_no_rows_still_has_a_description(self) -> None:
        cur = self.cur
        cur.execute("select 1 as a, 'x'::varchar(5) as b where false")
        description = cur.description
        assert description is not None
        self.assertEqual(
            [tuple(column) for column in description],
            [
                ("a", 23, None, 4, None, None, None),
                ("b", 1043, 5, None, None, None, None),
            ],
        )
        self.assertEqual(cur.rowcount, 0)
        self.assertEqual(cur.rownumber, 0)
        self.assertIsNone(cur.fetchone())
        self.assertEqual(cur.fetchmany(3), [])
        self.assertEqual(cur.fetchall(), [])
        self.assertEqual(list(cur), [])

    def test_returning_gives_rows_and_rowcount(self) -> None:
        cur = self.cur
        cur.execute("create table t (id int primary key, v text)")
        cur.execute("insert into t values (1, 'a'), (2, 'b') returning id, v")
        self.assertEqual(cur.rowcount, 2)
        self.assertEqual(cur.statusmessage, "INSERT 0 2")
        self.assertEqual(cur.fetchall(), [(1, "a"), (2, "b")])
        cur.execute("update t set v = v || '!' where id = 2 returning v")
        self.assertEqual(cur.rowcount, 1)
        self.assertEqual(cur.statusmessage, "UPDATE 1")
        self.assertEqual(cur.fetchone(), ("b!",))
        cur.execute("delete from t returning id")
        self.assertEqual(cur.rowcount, 2)
        self.assertEqual(cur.statusmessage, "DELETE 2")
        self.assertEqual(sorted(cur.fetchall()), [(1,), (2,)])
        cur.execute("delete from t returning id")
        self.assertEqual(cur.rowcount, 0)
        self.assertEqual(cur.fetchall(), [])
        self.assertIsNotNone(cur.description)

    def test_values_are_loaded(self) -> None:
        cur = self.cur
        cur.execute(
            "select 1::int8, 1.50::numeric, 'x'::text, null, true,"
            " '2020-01-02'::date, '\\x00ff'::bytea, array[1, 2], '1.2.3.4'::inet"
        )
        self.assertEqual(
            cur.fetchone(),
            (
                1, Decimal("1.50"), "x", None, True, dt.date(2020, 1, 2), b"\x00\xff",
                [1, 2], "1.2.3.4",
            ),
        )

    def test_scroll(self) -> None:
        cur = self.cur
        cur.execute(SIX)
        cur.scroll(2)
        self.assertEqual(cur.rownumber, 2)
        self.assertEqual(cur.fetchone(), (3, "r3"))
        cur.scroll(-3)
        self.assertEqual(cur.fetchone(), (1, "r1"))
        cur.scroll(0)
        self.assertEqual(cur.rownumber, 1)
        cur.scroll(5, "absolute")
        self.assertEqual(cur.fetchone(), (6, "r6"))
        cur.scroll(0, mode="absolute")
        self.assertEqual(cur.fetchone(), (1, "r1"))
        for value, mode in ((6, "absolute"), (-1, "absolute"), (5, "relative"),
                            (-2, "relative")):
            with self.assertRaises(IndexError) as caught:
                cur.scroll(value, mode)
            self.assertEqual(
                str(caught.exception),
                "the position is outside the result",
            )
        self.assertEqual(cur.rownumber, 1)
        with self.assertRaises(ValueError) as caught2:
            cur.scroll(1, "sideways")
        self.assertEqual(
            str(caught2.exception), "bad mode 'sideways': use 'relative' or 'absolute'"
        )
        cur.execute("select 1 where false")
        with self.assertRaises(IndexError):
            cur.scroll(0, "absolute")

    def test_row_factories(self) -> None:
        conn = self.conn
        cur = conn.cursor(row_factory=rows.dict_row)
        cur.execute("select 1 as a, 'x' as b")
        self.assertEqual(cur.fetchone(), {"a": 1, "b": "x"})
        cur.execute("select g as n from generate_series(1, 3) g")
        self.assertEqual(cur.fetchmany(2), [{"n": 1}, {"n": 2}])
        cur.row_factory = rows.namedtuple_row
        row = cur.fetchone()
        self.assertEqual((row.n, row._fields), (3, ("n",)))
        cur.execute("select 1 as a, 2 as a, 3")
        self.assertEqual(cur.fetchone()._fields, ("a", "_1", "_2"))
        conn.row_factory = rows.dict_row
        self.assertEqual(conn.execute("select 5 as five").fetchall(), [{"five": 5}])
        self.assertEqual(self.cur.execute("select 5").fetchall(), [(5,)])

    def test_errors(self) -> None:
        cur = self.cur
        calls = (
            cur.fetchone, cur.fetchmany, cur.fetchall, lambda: cur.scroll(0),
            lambda: next(cur),
        )
        for call in calls:
            with self.assertRaises(e.ProgrammingError) as caught:
                call()
            self.assertIs(type(caught.exception), e.ProgrammingError)
            self.assertEqual(str(caught.exception), NO_RESULT)
        self.assertIsNone(cur.rownumber)
        cur.execute("create table t (id int)")
        self.assertIsNone(cur.rownumber)
        for call in calls:
            with self.assertRaises(e.ProgrammingError) as caught:
                call()
            self.assertEqual(str(caught.exception), NO_ROWS)
        cur.execute("insert into t values (1)")
        with self.assertRaises(e.ProgrammingError):
            cur.fetchall()
        with self.assertRaises(e.DataError):
            cur.execute("select 1/0")
        with self.assertRaises(e.ProgrammingError) as caught:
            cur.fetchone()
        self.assertEqual(str(caught.exception), NO_RESULT)
        self.conn.rollback()
        cur.execute("select 1")
        cur.close()
        for call in calls:
            with self.assertRaises(e.InterfaceError) as caught2:
                call()
            self.assertEqual(str(caught2.exception), "the cursor is closed")
        self.assertIsNone(cur.rownumber)

    def test_rows_survive_the_end_of_the_transaction(self) -> None:
        cur = self.cur
        cur.execute(SIX)
        self.conn.commit()
        self.assertEqual(len(cur.fetchall()), 6)
        cur.execute(SIX)
        self.conn.close()
        self.assertEqual(cur.fetchone(), (1, "r1"))

    def test_large_result(self) -> None:
        cur = self.cur
        cur.execute("select g, md5(g::text) from generate_series(1, 20000) g")
        self.assertEqual(cur.rowcount, 20000)
        total = 0
        while True:
            chunk = cur.fetchmany(3000)
            if not chunk:
                break
            total += len(chunk)
        self.assertEqual(total, 20000)
        cur.scroll(19999, "absolute")
        self.assertEqual(cur.fetchone(), (20000, "d9798cdf31c02d86b8b81cc119d94836"))
```

#### CPYTHON-CUR-01D-EXECUTE-TESTS

Files: `tests/test_cursor_execute.py`. Design sections to implement: none (tests only); also read: 3.5, 4.7, 6.2, 6.3, 6.4, 6.6.

Step. This part adds tests only; it changes no file of the package. Write `tests/test_cursor_execute.py` as printed below. Every test must pass as written in oracle mode at once (the code exists since CPYTHON-CUR-02-FETCH). A failure in oracle mode is a defect of an earlier part or of the design: stop and report (C8); do not change the test. Two tests need the engine parts this part lists as prerequisites and pass in server mode only once those are merged: `test_command_tags_and_rowcount` (the tag `START TRANSACTION`) and `test_column_names_of_casts` (the names `int4`, `varchar`, `numeric`, `array`).

Before the code exists: Nothing fails first (tests only).

Tests to write first (19 cases in oracle and in server mode; none runs in unit mode (in server mode two of them wait for the engine parts)):

`tests/test_cursor_execute.py`:

```python
"""Tests of execute over the kinds of statements, errors and encodings."""
from __future__ import annotations

import os
import socket
from typing import Any

from stuntgres import adapt
from stuntgres import errors as e
from stuntgres import rows
from tests import support
from tests.dbapi_util import ConnTestCase, status


class ExecuteTest(ConnTestCase):
    def test_command_tags_and_rowcount(self) -> None:
        conn = self.connect(autocommit=True)
        cur = conn.cursor()
        # statement, statusmessage, rowcount, has a description
        cases: list[tuple[str, str | None, int, bool]] = [
            ("create table t (id int primary key, v text)", "CREATE TABLE", -1, False),
            ("insert into t values (1, 'a')", "INSERT 0 1", 1, False),
            ("insert into t values (2, 'b'), (3, 'c'), (4, 'd')",
             "INSERT 0 3", 3, False),
            ("insert into t select g, 'g' from generate_series(5, 6) g",
             "INSERT 0 2", 2, False),
            ("insert into t values (1, 'x') on conflict do nothing",
             "INSERT 0 0", 0, False),
            ("select * from t where id < 3", "SELECT 2", 2, True),
            ("select * from t where false", "SELECT 0", 0, True),
            ("select", "SELECT 1", 1, True),
            ("values (1), (2)", "SELECT 2", 2, True),
            ("update t set v = 'z' where id > 4", "UPDATE 2", 2, False),
            ("update t set v = 'z' where false", "UPDATE 0", 0, False),
            ("delete from t where id = 6", "DELETE 1", 1, False),
            ("merge into t using (values (1, 'm'), (100, 'n')) s(id, v)"
             " on t.id = s.id when matched then update set v = s.v"
             " when not matched then insert values (s.id, s.v)", "MERGE 2", 2, False),
            ("begin", "BEGIN", -1, False),
            ("declare c cursor for select * from t order by id",
             "DECLARE CURSOR", -1, False),
            ("fetch 2 from c", "FETCH 2", 2, True),
            ("move 1 in c", "MOVE 1", 1, False),
            ("fetch all from c", "FETCH 3", 3, True),
            ("fetch 1 from c", "FETCH 0", 0, True),
            ("close c", "CLOSE CURSOR", -1, False),
            ("commit", "COMMIT", -1, False),
            ("start transaction", "START TRANSACTION", -1, False),
            ("rollback", "ROLLBACK", -1, False),
            ("create table t2 as select * from t", "SELECT 6", 6, False),
            ("select * into t3 from t where id < 4", "SELECT 3", 3, False),
            ("create table t4 as select * from t with no data",
             "CREATE TABLE AS", -1, False),
            ("alter table t add column w int", "ALTER TABLE", -1, False),
            ("create index on t (v)", "CREATE INDEX", -1, False),
            ("truncate t3", "TRUNCATE TABLE", -1, False),
            ("drop table t2", "DROP TABLE", -1, False),
            ("set search_path to public", "SET", -1, False),
            ("show search_path", "SHOW", 1, True),
            ("prepare ps as select $1::int", "PREPARE", -1, False),
            ("execute ps (5)", "SELECT 1", 1, True),
            ("deallocate ps", "DEALLOCATE", -1, False),
            ("listen ch", "LISTEN", -1, False),
            ("notify ch", "NOTIFY", -1, False),
            ("create function f() returns int language sql as 'select 7'",
             "CREATE FUNCTION", -1, False),
            ("select f()", "SELECT 1", 1, True),
            ("create procedure pr(inout x int) language plpgsql"
             " as $$ begin x := x + 1; end $$", "CREATE PROCEDURE", -1, False),
            ("call pr(1)", "CALL", 1, True),
            ("", None, -1, False),
            (";", None, -1, False),
            ("-- only a comment", None, -1, False),
        ]
        for sql, tag, rowcount, described in cases:
            cur.execute(sql)
            self.assertEqual(cur.statusmessage, tag, sql)
            self.assertEqual(cur.rowcount, rowcount, sql)
            self.assertEqual(cur.description is not None, described, sql)
        self.assertEqual(cur.execute("call pr(1)").fetchall(), [(2,)])
        self.assertEqual(cur.execute("select").fetchall(), [()])
        self.assertEqual(cur.description, [])

    def test_column_names_of_casts(self) -> None:
        cur = self.connect().cursor()
        cur.execute(
            "select '1'::int4, 'a'::char(5), '{1}'::int4[], '{a}'::varchar(10)[],"
            " cast('{1.5}' as numeric(10,2)[]), array[1]::int8[], array[1, 2]"
        )
        description = cur.description
        assert description is not None
        self.assertEqual(
            [(column.name, column.type_code) for column in description],
            [
                ("int4", 23), ("bpchar", 1042), ("int4", 1007), ("varchar", 1015),
                ("numeric", 1231), ("array", 1016), ("array", 1007),
            ],
        )

    def test_an_error_in_a_multi_statement_string(self) -> None:
        conn = self.connect(autocommit=True)
        cur = conn.cursor()
        cur.execute("create table m (id int primary key)")
        for sql in (
            "select 1/0; insert into m values (1); select 2",
            "insert into m values (1); select 1/0; insert into m values (2)",
            "insert into m values (1); select 2; select 1/0",
        ):
            with self.assertRaises(e.DataError) as caught:
                cur.execute(sql)
            self.assertEqual(type(caught.exception).__name__, "DivisionByZero", sql)
            self.assertIsNone(cur.pgresult)
            self.assertEqual(status(conn), "IDLE")
            self.assertEqual(conn.execute("select count(*) from m").fetchone(), (0,))
        with self.assertRaises(e.ProgrammingError) as caught2:
            cur.execute("insert into m values (1); selec 2")
        self.assertEqual(type(caught2.exception).__name__, "SyntaxError")
        self.assertEqual(conn.execute("select count(*) from m").fetchone(), (0,))
        with self.assertRaises(e.DataError):
            cur.execute("insert into m values (1); commit; select 1/0")
        self.assertEqual(conn.execute("select count(*) from m").fetchone(), (1,))

    def test_several_statements_need_no_parameters(self) -> None:
        cur = self.connect(autocommit=True).cursor()
        with self.assertRaises(e.ProgrammingError) as caught:
            cur.execute("select 1; select 2", ())
        self.assertEqual(type(caught.exception).__name__, "SyntaxError")
        self.assertEqual(caught.exception.sqlstate, "42601")
        self.assertEqual(
            str(caught.exception),
            "cannot insert multiple commands into a prepared statement",
        )

    def test_error_then_recovery_in_a_transaction(self) -> None:
        conn = self.connect()
        cur = conn.cursor()
        cur.execute("create table t (id int primary key)")
        conn.commit()
        cur.execute("insert into t values (1)")
        with self.assertRaises(e.IntegrityError) as caught:
            cur.execute("insert into t values (1)")
        self.assertEqual(type(caught.exception).__name__, "UniqueViolation")
        self.assertEqual(caught.exception.diag.constraint_name, "t_pkey")
        with self.assertRaises(e.InternalError):
            cur.execute("select 1")
        conn.rollback()
        self.assertEqual(cur.execute("select count(*) from t").fetchone(), (0,))

    def test_two_connections(self) -> None:
        first, second = self.connect(), self.connect(autocommit=True)
        first.execute("create table t (id int)")
        first.commit()
        first.execute("insert into t values (1)")
        self.assertEqual(second.execute("select count(*) from t").fetchone(), (0,))
        first.commit()
        self.assertEqual(second.execute("select count(*) from t").fetchone(), (1,))
        first.execute("insert into t values (2)")
        first.close()
        self.assertEqual(second.execute("select count(*) from t").fetchone(), (1,))

    def test_session_ended_by_the_server(self) -> None:
        conn = self.connect(autocommit=True)
        other = self.connect(autocommit=True)
        pid = conn.info.backend_pid
        other.execute("select pg_terminate_backend(%s)", (pid,))
        support.wait_readable(conn.fileno())
        self.assertIs(conn.closed, False)
        with self.assertRaises(e.OperationalError) as caught:
            conn.execute("select 1")
        self.assertIn(
            "terminating connection due to administrator command",
            str(caught.exception),
        )
        self.assertIs(conn.closed, True)
        self.assertIs(conn.broken, True)
        self.assertIs(conn.lock.locked(), False)
        for call in (lambda: conn.execute("select 1"), conn.commit, conn.rollback):
            with self.assertRaises(e.OperationalError) as caught:
                call()
            self.assertEqual(str(caught.exception), "the connection is lost")
        conn.close()
        self.assertIs(conn.broken, False)
        self.assertEqual(other.execute("select 1").fetchone(), (1,))

    @support.only_modes("oracle", "server")
    def test_lost_connection(self) -> None:
        conn = self.connect()
        cur = conn.execute("select 1")
        peer = socket.socket(fileno=os.dup(conn.fileno()))
        peer.shutdown(socket.SHUT_RDWR)
        peer.close()
        with self.assertRaises(e.OperationalError) as caught:
            cur.execute("select 2")
        self.assertIs(type(caught.exception), e.OperationalError)
        self.assertIsNone(caught.exception.sqlstate)
        self.assertIn(
            "server closed the connection unexpectedly", str(caught.exception)
        )
        self.assertIs(conn.broken, True)
        self.assertIsNone(cur.pgresult)
        for call in (conn.commit, lambda: cur.execute("select 3")):
            with self.assertRaises(e.OperationalError) as caught:
                call()
            self.assertEqual(str(caught.exception), "the connection is lost")
        self.assertIs(conn.lock.locked(), False)

    def test_copy_is_refused(self) -> None:
        conn = self.connect(autocommit=True)
        cur = conn.cursor()
        cur.execute("create table t (id int)")
        for sql in ("copy t to stdout", "copy t from stdin"):
            with self.assertRaises(e.ProgrammingError) as caught:
                cur.execute(sql)
            self.assertEqual(
                str(caught.exception),
                "COPY cannot be run with execute() or executemany()",
            )
            self.assertIsNone(cur.pgresult)
            self.assertEqual(status(conn), "IDLE")
        self.assertEqual(cur.execute("select count(*) from t").fetchone(), (0,))

    def test_client_encodings(self) -> None:
        conn = self.connect(autocommit=True)
        cur = conn.cursor()
        cur.execute("set client_encoding to latin1")
        cur.execute(
            "select %s, length(%s), '\u00e9' as \"n\u00e9\"",
            ("\u00e9", "\u00e9"),
        )
        self.assertEqual(cur.fetchone(), ("\u00e9", 1, "\u00e9"))
        description = cur.description
        assert description is not None
        self.assertEqual(description[2].name, "n\u00e9")
        with self.assertRaises(UnicodeEncodeError):
            cur.execute("select %s", ("\u017c",))
        with self.assertRaises(e.ProgrammingError) as caught:
            cur.execute('select * from "t\u00e9"')
        self.assertEqual(
            caught.exception.diag.message_primary, 'relation "t\u00e9" does not exist'
        )
        cur.execute("set client_encoding to win1250")
        self.assertEqual(cur.execute("select %s", ("\u017c",)).fetchone(), ("\u017c",))

    def test_sql_ascii(self) -> None:
        conn = self.connect(autocommit=True, client_encoding="sql_ascii")
        self.assertEqual(conn.info.encoding, "ascii")
        cur = conn.cursor()
        self.assertEqual(
            cur.execute("select 'abc', %s", ("def",)).fetchone(),
            ("abc", "def"),
        )
        with self.assertRaises(UnicodeEncodeError):
            cur.execute("select %s", ("\u00e9",))
        with self.assertRaises(UnicodeEncodeError):
            cur.execute("select '\u00e9'")
        with self.assertRaises(UnicodeDecodeError):
            cur.execute("select chr(233)").fetchone()
        self.assertEqual(
            cur.execute(b"select '\xc3\xa9'::bytea").fetchone(), (b"\xc3\xa9",)
        )
        self.assertEqual(status(conn), "IDLE")

    def test_encoding_without_codec(self) -> None:
        conn = self.connect(autocommit=True)
        cur = conn.cursor()
        cur.execute("set client_encoding to 'EUC_TW'")
        other = conn.cursor()
        calls: list[Any] = [
            lambda: cur.execute("select 1"), lambda: other.execute("select 1"),
            conn.commit, conn.rollback,
        ]
        for call in calls:
            with self.assertRaises(e.NotSupportedError) as caught:
                call()
            self.assertEqual(
                str(caught.exception), "client encoding EUC_TW has no Python codec"
            )
        conn.close()
        self.assertIs(conn.closed, True)

    def test_parameters(self) -> None:
        cur = self.connect().cursor()
        cur.execute("select %s::int, %s, %s::text", (7, "it's", None))
        self.assertEqual(cur.fetchall(), [(7, "it's", None)])
        cur.execute("select %(a)s::int + %(b)s::int, %(a)s::int", {"a": 1, "b": 2})
        self.assertEqual(cur.fetchall(), [(3, 1)])
        cur.execute("select '100%'")
        self.assertEqual(cur.fetchall(), [("100%",)])
        cur.execute("select '100%%', %s::int", (1,))
        self.assertEqual(cur.fetchall(), [("100%", 1)])
        cur.execute(b"select 5")
        self.assertEqual(cur.fetchall(), [(5,)])

    def test_conversion_errors_send_nothing(self) -> None:
        conn = self.connect()
        cur = conn.cursor()
        with self.assertRaises(e.ProgrammingError) as caught:
            cur.execute("select %s, %s", (1,))
        self.assertEqual(
            str(caught.exception),
            "the query has 2 placeholders but 1 parameters were passed",
        )
        with self.assertRaises(e.ProgrammingError) as caught:
            cur.execute("select %s", (object(),))
        self.assertEqual(
            str(caught.exception), "cannot adapt type 'object' as a query parameter"
        )
        with self.assertRaises(TypeError):
            cur.execute(5)  # type: ignore[arg-type]
        self.assertEqual(status(conn), "IDLE")

    def test_cursors_share_the_transaction(self) -> None:
        conn = self.connect()
        first, second = conn.cursor(), conn.cursor()
        first.execute("create table t (id int)")
        first.execute("insert into t values (1)")
        second.execute("select count(*) from t")
        self.assertEqual(second.fetchall(), [(1,)])
        conn.rollback()
        with self.assertRaises(e.ProgrammingError):
            second.execute("select count(*) from t")

    def test_row_factory(self) -> None:
        conn = self.connect(row_factory=rows.dict_row)
        cur = conn.cursor()
        self.assertIs(cur.row_factory, rows.dict_row)
        self.assertIs(
            conn.cursor(row_factory=rows.tuple_row).row_factory,
            rows.tuple_row,
        )
        cur.execute("select g as a from generate_series(1, 3) g")
        self.assertEqual(cur.fetchone(), {"a": 1})
        cur.row_factory = rows.tuple_row
        self.assertIsNone(cur._make_row)
        self.assertEqual(cur.fetchone(), (2,))
        cur.row_factory = rows.namedtuple_row
        self.assertEqual(cur.fetchone().a, 3)

    def test_adapters_registered_on_the_cursor(self) -> None:
        class Upper(adapt.Loader):
            def load(self, data: bytes) -> Any:
                return data.decode().upper()

        conn = self.connect()
        cur = conn.cursor()
        cur.adapters.register_loader(25, Upper)
        cur.execute("select 'abc'::text")
        self.assertEqual(cur.fetchall(), [("ABC",)])
        other = conn.cursor().execute("select 'abc'::text")
        self.assertEqual(other.fetchall(), [("abc",)])

    def test_previous_result_is_released(self) -> None:
        cur = self.connect().cursor()
        cur.execute("select 1")
        first = cur.pgresult
        assert first is not None
        cur.execute("select 2")
        self.assertIsNone(first.pgresult_ptr)
        second = cur.pgresult
        assert second is not None
        cur.close()
        self.assertIsNone(second.pgresult_ptr)

    def test_closed_connection(self) -> None:
        conn = self.connect()
        cur = conn.cursor()
        conn.close()
        with self.assertRaises(e.OperationalError) as caught:
            cur.execute("select 1")
        self.assertEqual(str(caught.exception), "the connection is closed")
        with self.assertRaises(e.OperationalError):
            conn.execute("select 1")
```

#### CPYTHON-CUR-04-NEXTSET

Files: `stuntgres/_cur_nextset.py`, `tests/test_cursor_nextset.py`. Design sections to implement: 6.8; also read: 6.4, 6.6.

Step. Replace the stub `stuntgres/_cur_nextset.py` with the file printed in 6.8.

Before the code exists: With the stub in place: `AttributeError: 'Cursor' object has no attribute 'nextset'`.

Tests to write first (11 cases in oracle and in server mode; none runs in unit mode):

`tests/test_cursor_nextset.py`:

```python
"""Tests of nextset over the results of a multi-statement string."""
from __future__ import annotations

from stuntgres import errors as e
from stuntgres import rows
from tests.dbapi_util import ConnTestCase, status


class NextsetTest(ConnTestCase):
    def setUp(self) -> None:
        super().setUp()
        self.conn = self.connect(autocommit=True)
        self.cur = self.conn.cursor()

    def test_three_queries(self) -> None:
        cur = self.cur
        cur.execute("select 1 as a; select 2 as b, 3 as c; select 'x' as d where false")
        self.assertEqual(cur.statusmessage, "SELECT 1")
        self.assertEqual(cur.rowcount, 1)
        description = cur.description
        assert description is not None
        self.assertEqual([column.name for column in description], ["a"])
        self.assertEqual(cur.fetchall(), [(1,)])
        self.assertIs(cur.nextset(), True)
        description = cur.description
        assert description is not None
        self.assertEqual([column.name for column in description], ["b", "c"])
        self.assertEqual(cur.rownumber, 0)
        self.assertEqual(cur.fetchone(), (2, 3))
        self.assertIs(cur.nextset(), True)
        self.assertEqual(cur.rowcount, 0)
        self.assertEqual(cur.statusmessage, "SELECT 0")
        description = cur.description
        assert description is not None
        self.assertEqual([column.name for column in description], ["d"])
        self.assertEqual(cur.fetchall(), [])
        self.assertIsNone(cur.nextset())
        self.assertIsNone(cur.nextset())
        self.assertEqual(cur.statusmessage, "SELECT 0")

    def test_single_statement(self) -> None:
        cur = self.cur
        self.assertIsNone(cur.nextset())
        cur.execute("select 1")
        self.assertIsNone(cur.nextset())
        self.assertEqual(cur.fetchone(), (1,))

    def test_moving_on_drops_unread_rows(self) -> None:
        cur = self.cur
        cur.execute("select g from generate_series(1, 5) g; select 'next'")
        self.assertEqual(cur.fetchone(), (1,))
        self.assertIs(cur.nextset(), True)
        self.assertEqual(cur.fetchall(), [("next",)])

    def test_statements_without_rows(self) -> None:
        cur = self.cur
        cur.execute(
            "create table m (id int primary key);"
            " insert into m values (1), (2);"
            " select count(*) from m;"
            " update m set id = id + 10;"
            " delete from m where id = 11 returning id"
        )
        seen = []
        while True:
            seen.append((cur.statusmessage, cur.rowcount, cur.description is not None))
            if not cur.nextset():
                break
        self.assertEqual(
            seen,
            [
                ("CREATE TABLE", -1, False),
                ("INSERT 0 2", 2, False),
                ("SELECT 1", 1, True),
                ("UPDATE 2", 2, False),
                ("DELETE 1", 1, True),
            ],
        )
        self.assertEqual(cur.fetchall(), [(11,)])

    def test_fetch_on_a_result_without_rows(self) -> None:
        cur = self.cur
        cur.execute("set search_path to public; select 1")
        with self.assertRaises(e.ProgrammingError) as caught:
            cur.fetchone()
        self.assertEqual(str(caught.exception), "the last statement returned no rows")
        self.assertIs(cur.nextset(), True)
        self.assertEqual(cur.fetchone(), (1,))

    def test_empty_statements_give_no_result(self) -> None:
        cur = self.cur
        cur.execute("select 1; ; select 2;")
        self.assertEqual(cur.fetchall(), [(1,)])
        self.assertIs(cur.nextset(), True)
        self.assertEqual(cur.fetchall(), [(2,)])
        self.assertIsNone(cur.nextset())

    def test_transaction_control_inside_the_string(self) -> None:
        conn, cur = self.conn, self.cur
        cur.execute("create table m (id int primary key)")
        cur.execute("begin; insert into m values (1); commit; select count(*) from m")
        tags = [cur.statusmessage]
        while cur.nextset():
            tags.append(cur.statusmessage)
        self.assertEqual(tags, ["BEGIN", "INSERT 0 1", "COMMIT", "SELECT 1"])
        self.assertEqual(cur.fetchone(), (1,))
        self.assertEqual(status(conn), "IDLE")
        cur.execute("begin; insert into m values (2)")
        self.assertEqual(status(conn), "INTRANS")
        conn.rollback()
        self.assertEqual(conn.execute("select count(*) from m").fetchone(), (1,))

    def test_implicit_transaction_of_the_string(self) -> None:
        conn = self.connect()
        cur = conn.cursor()
        cur.execute("create table m (id int); insert into m values (1); select 1")
        self.assertEqual(status(conn), "INTRANS")
        self.assertEqual(cur.statusmessage, "CREATE TABLE")
        conn.rollback()
        with self.assertRaises(e.ProgrammingError):
            cur.execute("select * from m")
        conn.rollback()

    def test_row_factory_and_loaders_per_result(self) -> None:
        cur = self.conn.cursor(row_factory=rows.dict_row)
        cur.execute("select 1 as a, 'x' as b; select now()::date = current_date as ok")
        self.assertEqual(cur.fetchall(), [{"a": 1, "b": "x"}])
        cur.nextset()
        self.assertEqual(cur.fetchall(), [{"ok": True}])

    def test_new_execute_starts_over(self) -> None:
        cur = self.cur
        cur.execute("select 1; select 2")
        cur.nextset()
        cur.execute("select 3")
        self.assertEqual(cur.fetchall(), [(3,)])
        self.assertIsNone(cur.nextset())

    def test_closed(self) -> None:
        cur = self.cur
        cur.execute("select 1; select 2")
        cur.close()
        with self.assertRaises(e.InterfaceError) as caught:
            cur.nextset()
        self.assertEqual(str(caught.exception), "the cursor is closed")
```

#### CPYTHON-CUR-03-EXECUTEMANY

Files: `stuntgres/_cur_many.py`, `tests/test_cursor_executemany.py`. Design sections to implement: 6.7; also read: 6.3, 6.4, 6.8.

Step. Replace the stub `stuntgres/_cur_many.py` with the file printed in 6.7.

Before the code exists: With the stub in place: `AttributeError: 'Cursor' object has no attribute '_executemany_gen'`.

Tests to write first (10 cases in oracle and in server mode; none runs in unit mode):

`tests/test_cursor_executemany.py`:

```python
"""Tests of executemany."""
from __future__ import annotations

from typing import Any, Iterator

from stuntgres import errors as e
from tests.dbapi_util import ConnTestCase, status

NO_RESULT = "no result available: execute a statement first"


class ExecutemanyTest(ConnTestCase):
    def setUp(self) -> None:
        super().setUp()
        self.conn = self.connect()
        self.cur = self.conn.cursor()
        self.cur.execute("create table t (id int primary key, v text)")
        self.conn.commit()

    def all_rows(self) -> list[Any]:
        return self.conn.execute("select id, v from t order by id").fetchall()

    def test_insert(self) -> None:
        cur = self.cur
        result = cur.executemany(
            "insert into t values (%s, %s)", [(1, "a"), (2, "b"), (3, None)]
        )
        self.assertIsNone(result)
        self.assertEqual(cur.rowcount, 3)
        self.assertIsNone(cur.description)
        self.assertIsNone(cur.pgresult)
        self.assertIsNone(cur.statusmessage)
        self.assertEqual(status(self.conn), "INTRANS")
        with self.assertRaises(e.ProgrammingError) as caught:
            cur.fetchone()
        self.assertEqual(str(caught.exception), NO_RESULT)
        self.assertEqual(self.all_rows(), [(1, "a"), (2, "b"), (3, None)])

    def test_named_parameters_and_generators(self) -> None:
        cur = self.cur

        def params() -> Iterator[dict[str, Any]]:
            for number in range(1, 5):
                yield {"id": number, "v": "v%d" % number}

        cur.executemany("insert into t values (%(id)s, %(v)s)", params())
        self.assertEqual(cur.rowcount, 4)
        self.assertEqual(self.all_rows()[3], (4, "v4"))

    def test_rowcount_is_the_sum(self) -> None:
        cur = self.cur
        cur.executemany("insert into t values (%s, 'x')", [(n,) for n in range(1, 7)])
        cur.executemany("update t set v = 'y' where id <= %s", [(1,), (3,), (0,)])
        self.assertEqual(cur.rowcount, 4)
        cur.executemany("delete from t where id > %s", [(4,), (4,)])
        self.assertEqual(cur.rowcount, 2)
        cur.executemany("select %s::int", [(1,), (2,)])
        self.assertEqual(cur.rowcount, 2)
        self.assertIsNone(cur.pgresult)

    def test_no_parameter_sets(self) -> None:
        cur = self.cur
        cur.execute("select 1")
        cur.executemany("insert into t values (%s, %s)", [])
        self.assertEqual(cur.rowcount, 0)
        self.assertIsNone(cur.pgresult)
        self.assertEqual(status(self.conn), "INTRANS")
        self.conn.rollback()
        cur.executemany("insert into t values (%s, %s)", [], returning=True)
        self.assertEqual(cur.rowcount, 0)
        self.assertEqual(status(self.conn), "IDLE")

    def test_returning(self) -> None:
        cur = self.cur
        cur.executemany(
            "insert into t values (%s, %s) returning id, v",
            [(1, "a"), (2, "b"), (3, "c")],
            returning=True,
        )
        self.assertEqual(cur.rowcount, 1)
        self.assertEqual(cur.statusmessage, "INSERT 0 1")
        description = cur.description
        assert description is not None
        self.assertEqual([column.name for column in description], ["id", "v"])
        self.assertEqual(cur.fetchall(), [(1, "a")])
        self.assertIs(cur.nextset(), True)
        self.assertEqual(cur.fetchone(), (2, "b"))
        self.assertIs(cur.nextset(), True)
        self.assertEqual(cur.fetchall(), [(3, "c")])
        self.assertIsNone(cur.nextset())
        self.assertEqual(cur.fetchall(), [])

    def test_returning_without_rows(self) -> None:
        cur = self.cur
        cur.executemany(
            "insert into t values (%s, 'x')", [(1,), (2,)], returning=True
        )
        self.assertEqual(cur.rowcount, 1)
        self.assertIsNone(cur.description)
        self.assertIs(cur.nextset(), True)
        self.assertIsNone(cur.nextset())
        self.assertEqual(self.all_rows(), [(1, "x"), (2, "x")])

    def test_error_in_a_later_set(self) -> None:
        conn, cur = self.conn, self.cur
        with self.assertRaises(e.IntegrityError) as caught:
            cur.executemany(
                "insert into t values (%s, 'x') returning id",
                [(1,), (2,), (1,), (3,)],
                returning=True,
            )
        self.assertEqual(type(caught.exception).__name__, "UniqueViolation")
        self.assertIsNone(cur.pgresult)
        self.assertEqual(cur.rowcount, -1)
        self.assertEqual(status(conn), "INERROR")
        self.assertIs(conn.lock.locked(), False)
        conn.rollback()
        self.assertEqual(self.all_rows(), [])

    def test_autocommit_keeps_the_sets_before_the_error(self) -> None:
        conn = self.connect(autocommit=True)
        with self.assertRaises(e.IntegrityError):
            conn.cursor().executemany(
                "insert into t values (%s, 'x')", [(1,), (2,), (1,), (3,)]
            )
        self.assertEqual(status(conn), "IDLE")
        self.assertEqual(self.all_rows(), [(1, "x"), (2, "x")])

    def test_bad_parameter_set(self) -> None:
        conn, cur = self.conn, self.cur
        with self.assertRaises(e.ProgrammingError) as caught:
            cur.executemany("insert into t values (%s, %s)", [(1, "a"), (2,)])
        self.assertEqual(
            str(caught.exception),
            "the query has 2 placeholders but 1 parameters were passed",
        )
        self.assertEqual(status(conn), "INTRANS")
        self.assertEqual(self.all_rows(), [(1, "a")])

    def test_closed(self) -> None:
        self.cur.close()
        with self.assertRaises(e.InterfaceError) as caught:
            self.cur.executemany("select %s", [(1,)])
        self.assertEqual(str(caught.exception), "the cursor is closed")
        cur = self.conn.cursor()
        self.conn.close()
        with self.assertRaises(e.OperationalError) as caught2:
            cur.executemany("select %s", [(1,)])
        self.assertEqual(str(caught2.exception), "the connection is closed")
```

#### CPYTHON-CUR-05-OPTIONAL

Files: `stuntgres/_cur_optional.py`, `tests/test_cursor_optional.py`. Design sections to implement: 6.9.

Step. Replace the stub `stuntgres/_cur_optional.py` with the file printed in 6.9. Do not add `callproc`, `messages` or `errorhandler`: one test asserts that they are absent.

Before the code exists: With the stub in place: `AttributeError: 'Cursor' object has no attribute 'setinputsizes'` (three of four tests).

Tests to write first (4 cases in oracle and in server mode; none runs in unit mode):

`tests/test_cursor_optional.py`:

```python
"""Tests of the PEP 249 members that do nothing in this driver."""
from __future__ import annotations

from stuntgres.cursor import Cursor
from tests.dbapi_util import ConnTestCase


class OptionalTest(ConnTestCase):
    def test_sizes_are_accepted_and_ignored(self) -> None:
        cur = self.connect().cursor()
        self.assertIsNone(cur.setinputsizes((25, None)))
        self.assertIsNone(cur.setinputsizes([]))
        self.assertIsNone(cur.setoutputsize(1000))
        self.assertIsNone(cur.setoutputsize(2000, 0))
        cur.execute("select %s, repeat('x', 3000)", ("abc",))
        cur.setoutputsize(10, 1)
        self.assertEqual(cur.fetchone(), ("abc", "x" * 3000))

    def test_lastrowid(self) -> None:
        conn = self.connect()
        cur = conn.cursor()
        self.assertIsNone(cur.lastrowid)
        cur.execute("create table t (id serial primary key, v text)")
        cur.execute("insert into t (v) values ('a')")
        self.assertIsNone(cur.lastrowid)
        cur.execute("insert into t (v) values ('b') returning id")
        self.assertIsNone(cur.lastrowid)
        self.assertEqual(cur.fetchone(), (2,))

    def test_members_that_are_left_out(self) -> None:
        cur = self.connect().cursor()
        for name in ("callproc", "messages", "errorhandler"):
            self.assertFalse(hasattr(cur, name), name)
            self.assertFalse(hasattr(Cursor, name), name)
        for name in ("messages", "errorhandler"):
            self.assertFalse(hasattr(cur.connection, name), name)

    def test_after_close(self) -> None:
        cur = self.connect().cursor()
        cur.close()
        cur.setinputsizes((1,))
        cur.setoutputsize(1)
        self.assertIsNone(cur.lastrowid)
```

#### CPYTHON-ASYNC-01-CONNECTION

Files: `stuntgres/connection_async.py`, `tests/dbapi_util_async.py`, `tests/test_async_connection.py`. Design sections to implement: 9.1, 9.4, 8.4; also read: 9.2, 4.3.

Step. 1. Write `tests/dbapi_util_async.py` (printed below). 2. Write `stuntgres/connection_async.py` as printed in 9.4: the complete class. `transaction()` imports `stuntgres.transaction_async` only when called, and `cursor()` imports `stuntgres.cursor_async` only when called without `cursor_factory`; the tests of this part use neither (they run SQL with the helper `arun`).

Before the code exists: `ModuleNotFoundError: No module named 'stuntgres.connection_async'`.

Tests to write first (9 cases in oracle and in server mode; none runs in unit mode):

`tests/dbapi_util_async.py`:

```python
"""Helpers of the asyncio DB-API tests."""
from __future__ import annotations

from typing import Any

from stuntgres.connection_async import AsyncConnection
from tests import support


async def arun(conn: AsyncConnection, sql: str) -> list[tuple[bytes | None, ...]]:
    """Run one statement the way a cursor does: implicit BEGIN, then send it."""
    async with conn.lock:
        await conn.wait(conn._start_query())
        command = sql.encode(conn.pgconn._encoding)
        result = await conn.wait(conn._exec_command(command))
    return result.get_rows(0, result.ntuples)


class AsyncConnTestCase(support.AsyncDatabaseTestCase):
    """An asyncio database test whose connections are closed at its end."""

    async def connect(self, **kwargs: Any) -> AsyncConnection:
        """Connect to the test's database."""
        conn = await AsyncConnection.connect(self.conninfo, **kwargs)
        self.addAsyncCleanup(conn.close)
        return conn
```

`tests/test_async_connection.py`:

```python
"""Tests of AsyncConnection: the twin of Connection."""
from __future__ import annotations

import asyncio
import time
from typing import Any

from stuntgres import errors as e
from stuntgres import rows
from stuntgres._conn_core import IsolationLevel
from stuntgres._connection_base import BaseConnection
from stuntgres.connection_async import AsyncConnection
from tests import support
from tests.dbapi_util import status
from tests.dbapi_util_async import AsyncConnTestCase, arun

SHOW = (
    "select current_setting('transaction_isolation'),"
    " current_setting('transaction_read_only'),"
    " current_setting('transaction_deferrable')"
)


class AsyncConnectionTest(AsyncConnTestCase):
    async def test_connect(self) -> None:
        conn = await self.connect()
        self.assertIs(type(conn), AsyncConnection)
        self.assertIsInstance(conn, BaseConnection)
        self.assertEqual(AsyncConnection.__module__, "stuntgres")
        self.assertRegex(
            repr(conn), r"^<stuntgres\.AsyncConnection \[open\] at 0x[0-9a-f]+>$"
        )
        self.assertIs(type(conn.lock), asyncio.Lock)
        self.assertIs(conn.autocommit, False)
        self.assertIs(conn.row_factory, rows.tuple_row)
        self.assertIsNone(conn.cursor_factory)
        self.assertEqual(status(conn), "IDLE")
        given = support.conninfo_dict(self.conninfo)
        self.assertEqual(conn.info.dbname, given["dbname"])
        loop = asyncio.get_running_loop()
        self.assertIs(loop.remove_reader(conn.fileno()), False)
        self.assertIs(loop.remove_writer(conn.fileno()), False)

    async def test_connect_arguments(self) -> None:
        class Factory:
            pass

        given = support.conninfo_dict(self.conninfo)
        conn = await AsyncConnection.connect(
            "", autocommit=True, row_factory=rows.dict_row, cursor_factory=Factory,
            application_name="it's me", **given,
        )
        self.addAsyncCleanup(conn.close)
        self.assertIs(conn.autocommit, True)
        self.assertIs(conn.row_factory, rows.dict_row)
        self.assertIs(conn.cursor_factory, Factory)
        self.assertEqual(conn.info.parameter_status("application_name"), "it's me")

    async def test_connection_errors(self) -> None:
        with self.assertRaises(e.ProgrammingError) as caught:
            await AsyncConnection.connect("nosuch=1")
        self.assertEqual(str(caught.exception), 'invalid connection option "nosuch"')
        with self.assertRaises(e.OperationalError) as caught2:
            await AsyncConnection.connect(self.conninfo, options="-c nosuch_setting=1")
        self.assertIn(
            'unrecognized configuration parameter "nosuch_setting"',
            str(caught2.exception),
        )

    async def test_close(self) -> None:
        conn = await self.connect()
        await conn.close()
        self.assertIs(conn.closed, True)
        self.assertIs(conn.broken, False)
        await conn.close()
        for call in (conn.commit, conn.rollback):
            with self.assertRaises(e.OperationalError) as caught:
                await call()
            self.assertEqual(str(caught.exception), "the connection is closed")
        with self.assertRaises(e.OperationalError):
            conn.cursor()
        self.assertIs(conn.lock.locked(), False)

    async def test_close_does_not_wait_for_a_statement(self) -> None:
        conn = await self.connect()
        conn.pgconn.send_query(b"select pg_sleep(30)")
        started = time.monotonic()
        await conn.close()
        self.assertLess(time.monotonic() - started, 10.0)
        self.assertIs(conn.closed, True)

    async def test_transactions(self) -> None:
        conn = await self.connect()
        other = await self.connect(autocommit=True)
        await arun(conn, "create table t (id int primary key)")
        self.assertEqual(status(conn), "INTRANS")
        count = "select count(*) from pg_class where relname = 't'"
        self.assertEqual(await arun(other, count), [(b"0",)])
        await conn.commit()
        self.assertEqual(status(conn), "IDLE")
        self.assertEqual(await arun(other, count), [(b"1",)])
        await arun(conn, "insert into t values (1)")
        await conn.rollback()
        self.assertEqual(await arun(other, "select count(*) from t"), [(b"0",)])
        with self.assertRaises(e.DataError) as caught:
            await arun(conn, "select 1/0")
        self.assertEqual(type(caught.exception).__name__, "DivisionByZero")
        self.assertEqual(status(conn), "INERROR")
        self.assertIs(conn.lock.locked(), False)
        await conn.rollback()
        await conn.commit()
        self.assertEqual(status(conn), "IDLE")

    async def test_attributes_are_set_with_methods(self) -> None:
        conn = await self.connect()
        cases: dict[str, Any] = {
            "autocommit": True,
            "isolation_level": IsolationLevel.SERIALIZABLE,
            "read_only": True,
            "deferrable": True,
        }
        for name, value in cases.items():
            with self.assertRaises(AttributeError) as caught:
                setattr(conn, name, value)
            self.assertEqual(
                str(caught.exception),
                f"'{name}' cannot be assigned on an async connection:"
                f" use 'await connection.set_{name}(value)'",
            )
        await conn.set_isolation_level(IsolationLevel.SERIALIZABLE)
        await conn.set_read_only(True)
        await conn.set_deferrable(True)
        self.assertIs(conn.isolation_level, IsolationLevel.SERIALIZABLE)
        self.assertEqual(await arun(conn, SHOW), [(b"serializable", b"on", b"on")])
        with self.assertRaises(e.ProgrammingError) as caught2:
            await conn.set_autocommit(True)
        self.assertEqual(
            str(caught2.exception),
            "cannot change autocommit inside a transaction"
            " (transaction status INTRANS)",
        )
        with self.assertRaises(e.ProgrammingError):
            await conn.set_read_only(False)
        await conn.rollback()
        await conn.set_autocommit(True)
        self.assertIs(conn.autocommit, True)
        self.assertEqual(await arun(conn, SHOW), [(b"read committed", b"off", b"off")])
        self.assertEqual(status(conn), "IDLE")

    async def test_async_with(self) -> None:
        other = await self.connect(autocommit=True)
        async with await AsyncConnection.connect(self.conninfo) as conn:
            await arun(conn, "create table t (id int)")
        self.assertIs(conn.closed, True)
        self.assertEqual(await arun(other, "select count(*) from t"), [(b"0",)])
        with self.assertRaises(ValueError):
            async with await AsyncConnection.connect(self.conninfo) as conn:
                await arun(conn, "insert into t values (1)")
                raise ValueError("stop")
        self.assertIs(conn.closed, True)
        self.assertEqual(await arun(other, "select count(*) from t"), [(b"0",)])

    async def test_named_cursor_is_not_available(self) -> None:
        conn = await self.connect()
        with self.assertRaises(e.NotSupportedError) as caught:
            conn.cursor("big")
        self.assertEqual(str(caught.exception), "named cursors are not available")
```

#### CPYTHON-ASYNC-02-CURSOR

Files: `stuntgres/cursor_async.py`, `tests/test_async_cursor.py`. Design sections to implement: 9.5; also read: 9.1, 9.3.

Step. Write `stuntgres/cursor_async.py` as printed in 9.5.

Before the code exists: `ModuleNotFoundError: No module named 'stuntgres.cursor_async'`.

Tests to write first (8 cases in oracle and in server mode; none runs in unit mode):

`tests/test_async_cursor.py`:

```python
"""Tests of AsyncCursor: the twin of Cursor."""
from __future__ import annotations

import asyncio
from typing import Any

from stuntgres import errors as e
from stuntgres import rows
from stuntgres._cursor_base import BaseCursor
from stuntgres.cursor_async import AsyncCursor
from tests.dbapi_util import status
from tests.dbapi_util_async import AsyncConnTestCase

SIX = "select g, 'r' || g from generate_series(1, 6) g"


class AsyncCursorTest(AsyncConnTestCase):
    async def test_execute_and_fetch(self) -> None:
        conn = await self.connect()
        cur = conn.cursor()
        self.assertIs(type(cur), AsyncCursor)
        self.assertIsInstance(cur, BaseCursor)
        self.assertEqual(AsyncCursor.__module__, "stuntgres")
        self.assertEqual(
            [cls.__name__ for cls in AsyncCursor.__mro__[:4]],
            ["AsyncCursor", "AsyncCursorFeatures", "BaseCursor", "BaseCursorFeatures"],
        )
        self.assertIs(await cur.execute(SIX), cur)
        self.assertEqual(status(conn), "INTRANS")
        self.assertEqual(cur.rowcount, 6)
        self.assertEqual(cur.statusmessage, "SELECT 6")
        description = cur.description
        assert description is not None
        self.assertEqual(
            [tuple(column) for column in description],
            [
                ("g", 23, None, 4, None, None, None),
                ("?column?", 25, None, None, None, None, None),
            ],
        )
        self.assertEqual(await cur.fetchone(), (1, "r1"))
        self.assertEqual(await cur.fetchmany(), [(2, "r2")])
        self.assertEqual(await cur.fetchmany(2), [(3, "r3"), (4, "r4")])
        self.assertEqual(cur.rownumber, 4)
        self.assertEqual(await cur.fetchall(), [(5, "r5"), (6, "r6")])
        self.assertIsNone(await cur.fetchone())
        await cur.scroll(0, "absolute")
        await cur.scroll(2)
        self.assertEqual(await cur.fetchone(), (3, "r3"))
        with self.assertRaises(IndexError):
            await cur.scroll(10)

    async def test_iteration(self) -> None:
        conn = await self.connect()
        cur = await conn.execute(SIX)
        self.assertIs(type(cur), AsyncCursor)
        self.assertIs(cur.__aiter__(), cur)
        self.assertEqual([row[0] async for row in cur], [1, 2, 3, 4, 5, 6])
        with self.assertRaises(StopAsyncIteration):
            await cur.__anext__()

    async def test_parameters_and_row_factory(self) -> None:
        conn = await self.connect(row_factory=rows.dict_row)
        cur = await conn.execute(
            "select %(a)s::int + 1 as n, %(b)s as t", {"a": 41, "b": "x"}
        )
        self.assertEqual(await cur.fetchone(), {"n": 42, "t": "x"})
        cur = conn.cursor(row_factory=rows.namedtuple_row)
        await cur.execute("select 1 as a")
        self.assertEqual((await cur.fetchone()).a, 1)

    async def test_executemany_and_nextset(self) -> None:
        conn = await self.connect()
        cur = conn.cursor()
        await cur.execute("create table t (id int primary key, v text)")
        self.assertIsNone(
            await cur.executemany("insert into t values (%s, %s)", [(1, "a"), (2, "b")])
        )
        self.assertEqual(cur.rowcount, 2)
        await cur.executemany(
            "update t set v = v || '!' where id = %s returning v", [(1,), (2,)],
            returning=True,
        )
        self.assertEqual(await cur.fetchall(), [("a!",)])
        self.assertIs(cur.nextset(), True)
        self.assertEqual(await cur.fetchall(), [("b!",)])
        self.assertIsNone(cur.nextset())
        await cur.execute("select 1; select 2")
        self.assertEqual(await cur.fetchone(), (1,))
        self.assertIs(cur.nextset(), True)
        self.assertEqual(await cur.fetchone(), (2,))
        cur.setinputsizes((1,))
        cur.setoutputsize(1)
        self.assertIsNone(cur.lastrowid)

    async def test_errors(self) -> None:
        conn = await self.connect()
        cur = conn.cursor()
        with self.assertRaises(e.ProgrammingError) as caught:
            await cur.fetchone()
        self.assertEqual(
            str(caught.exception), "no result available: execute a statement first"
        )
        with self.assertRaises(e.ProgrammingError) as caught:
            await cur.execute("select * from nosuch")
        self.assertEqual(type(caught.exception).__name__, "UndefinedTable")
        self.assertEqual(status(conn), "INERROR")
        self.assertIs(conn.lock.locked(), False)
        await conn.rollback()
        with self.assertRaises(e.ProgrammingError) as caught:
            await cur.execute("copy (select 1) to stdout")
        self.assertEqual(
            str(caught.exception), "COPY cannot be run with execute() or executemany()"
        )
        self.assertEqual(await (await conn.execute("select 5")).fetchone(), (5,))

    async def test_close(self) -> None:
        conn = await self.connect()
        async with conn.cursor() as cur:
            await cur.execute("select 1")
        self.assertIs(cur.closed, True)
        await cur.close()
        calls: list[Any] = [
            lambda: cur.execute("select 1"), cur.fetchone, cur.fetchall,
            lambda: cur.executemany("select %s", [(1,)]),
        ]
        for call in calls:
            with self.assertRaises(e.InterfaceError) as caught:
                await call()
            self.assertEqual(str(caught.exception), "the cursor is closed")
        with self.assertRaises(e.InterfaceError):
            cur.nextset()
        other = conn.cursor()
        await conn.close()
        with self.assertRaises(e.OperationalError) as caught2:
            await other.execute("select 1")
        self.assertEqual(str(caught2.exception), "the connection is closed")

    async def test_the_loop_runs_while_a_statement_waits(self) -> None:
        conn = await self.connect(autocommit=True)
        ticks = 0

        async def ticker() -> None:
            nonlocal ticks
            while True:
                await asyncio.sleep(0.01)
                ticks += 1

        task = asyncio.create_task(ticker())
        cur = await conn.execute("select pg_sleep(0.3), 1")
        task.cancel()
        self.assertEqual((await cur.fetchone())[1], 1)
        self.assertGreaterEqual(ticks, 10)

    async def test_cursors_of_two_tasks(self) -> None:
        conn = await self.connect(autocommit=True)

        async def worker(number: int) -> list[Any]:
            cur = conn.cursor()
            out = []
            for i in range(10):
                await cur.execute("select %s::int", (number * 100 + i,))
                out.append((await cur.fetchone())[0])
            return out

        results = await asyncio.gather(*(worker(n) for n in range(4)))
        for number, got in enumerate(results):
            self.assertEqual(got, [number * 100 + i for i in range(10)])
```

#### CPYTHON-ASYNC-03-TRANSACTION

Files: `stuntgres/transaction_async.py`, `tests/test_async_transaction.py`. Design sections to implement: 9.6; also read: 5.4, 8.2, 8.4, 9.4.

Step. Write `stuntgres/transaction_async.py` as printed in 9.6. The asyncio `notifies()` and the cancellation path are already in `connection_async.py` (CPYTHON-ASYNC-01-CONNECTION); this part proves them. A test that hangs for 30 s means a cancellation was lost: stop and report.

Before the code exists: `ModuleNotFoundError: No module named 'stuntgres.transaction_async'`.

Tests to write first (22 cases in oracle and in server mode; none runs in unit mode):

`tests/test_async_transaction.py`:

```python
"""Tests of the asyncio transaction blocks, notifies() and task cancellation."""
from __future__ import annotations

import asyncio
import logging
import socket
import time
from typing import Any

from stuntgres import errors as e
from stuntgres import generators, pq
from stuntgres._conn_core import Notify
from stuntgres.connection_async import AsyncConnection
from stuntgres.transaction import BaseTransaction, Rollback
from stuntgres.transaction_async import AsyncTransaction
from tests import support
from tests.dbapi_util import status
from tests.dbapi_util_async import AsyncConnTestCase, arun

SLEEP = "do $$ begin raise notice 'started'; perform pg_sleep(30); end $$"


class AsyncTransactionTest(AsyncConnTestCase):
    async def asyncSetUp(self) -> None:
        self.conn = await self.connect(autocommit=True)
        await self.conn.execute("create table t (id int primary key)")

    async def ids(self) -> list[int]:
        cur = await self.conn.execute("select id from t order by id")
        return [row[0] for row in await cur.fetchall()]

    async def test_commit_and_rollback(self) -> None:
        conn = self.conn
        tx = conn.transaction()
        self.assertIs(type(tx), AsyncTransaction)
        self.assertIsInstance(tx, BaseTransaction)
        self.assertEqual(AsyncTransaction.__module__, "stuntgres")
        async with tx as entered:
            self.assertIs(entered, tx)
            self.assertEqual(status(conn), "INTRANS")
            await conn.execute("insert into t values (1)")
        self.assertEqual(status(conn), "IDLE")
        with self.assertRaises(ValueError):
            async with conn.transaction():
                await conn.execute("insert into t values (2)")
                raise ValueError("stop")
        self.assertEqual(await self.ids(), [1])
        self.assertIs(conn.lock.locked(), False)

    async def test_nested_and_rollback_exception(self) -> None:
        conn = self.conn
        async with conn.transaction() as outer:
            await conn.execute("insert into t values (1)")
            async with conn.transaction("sp") as inner:
                self.assertEqual(inner.savepoint_name, "sp")
                await conn.execute("insert into t values (2)")
                raise Rollback
            async with conn.transaction() as third:
                self.assertEqual(third.savepoint_name, "_sg_tx_2")
                await conn.execute("insert into t values (3)")
            with self.assertRaises(e.DataError):
                async with conn.transaction():
                    await conn.execute("select 1/0")
            self.assertEqual(status(conn), "INTRANS")
            self.assertIsNone(outer.savepoint_name)
        self.assertEqual(await self.ids(), [1, 3])
        async with conn.transaction() as outer:
            await conn.execute("insert into t values (4)")
            async with conn.transaction():
                raise Rollback(outer)
        self.assertEqual(await self.ids(), [1, 3])
        async with conn.transaction(force_rollback=True):
            await conn.execute("insert into t values (5)")
        self.assertEqual(await self.ids(), [1, 3])
        self.assertEqual(conn._num_transactions, 0)

    async def test_commit_is_refused_inside(self) -> None:
        conn = self.conn
        async with conn.transaction():
            with self.assertRaises(e.ProgrammingError) as caught:
                await conn.commit()
            self.assertEqual(
                str(caught.exception),
                "commit() cannot be used inside a transaction() block",
            )
            with self.assertRaises(e.ProgrammingError):
                await conn.set_autocommit(False)

    async def test_closed_connection(self) -> None:
        conn = await self.connect(autocommit=True)
        with self.assertRaises(ValueError):
            async with conn.transaction():
                await conn.close()
                raise ValueError("stop")
        with self.assertRaises(e.OperationalError) as caught:
            async with conn.transaction():
                self.fail("entered")  # pragma: no cover
        self.assertEqual(str(caught.exception), "the connection is closed")


class AsyncNotifyTest(AsyncConnTestCase):
    async def asyncSetUp(self) -> None:
        self.listener = await self.connect(autocommit=True)
        self.sender = await self.connect(autocommit=True)
        await self.listener.execute("listen chan")
        self.pid = self.sender.info.backend_pid

    async def take(self, **kwargs: Any) -> list[Notify]:
        return [notify async for notify in self.listener.notifies(**kwargs)]

    async def test_nothing_arrived(self) -> None:
        self.assertEqual(await self.take(timeout=0), [])
        started = time.monotonic()
        self.assertEqual(await self.take(timeout=0.2), [])
        self.assertGreaterEqual(time.monotonic() - started, 0.2)
        self.assertIs(self.listener.lock.locked(), False)

    async def test_notifications(self) -> None:
        await self.sender.execute("notify chan, 'one'; notify chan, 'two'")
        self.assertEqual(
            await self.take(timeout=10, stop_after=1), [Notify("chan", "one", self.pid)]
        )
        self.assertEqual(
            await self.take(timeout=10, stop_after=1), [Notify("chan", "two", self.pid)]
        )
        self.assertEqual(await self.take(timeout=0), [])

    async def test_waiting_task_is_woken(self) -> None:
        task = asyncio.create_task(self.take(timeout=10, stop_after=2))
        await asyncio.sleep(0)
        await self.sender.execute("notify chan, 'a'")
        await self.sender.execute("notify chan, 'b'")
        got = await asyncio.wait_for(task, 10)
        self.assertEqual([n.payload for n in got], ["a", "b"])

    async def test_the_loop_body_may_use_the_connection(self) -> None:
        await self.sender.execute("notify chan, 'six'")
        answers = []
        async for notify in self.listener.notifies(timeout=10, stop_after=1):
            cur = await self.listener.execute("select 2")
            answers.append((notify.payload, await cur.fetchone()))
        self.assertEqual(answers, [("six", (2,))])

    async def test_handlers(self) -> None:
        got: list[Notify] = []
        self.listener.add_notify_handler(got.append)
        await self.sender.execute("notify chan, 'eight'")
        deadline = time.monotonic() + 10
        while not got and time.monotonic() < deadline:
            self.assertEqual(await self.take(timeout=0.05), [])
        self.assertEqual(got, [Notify("chan", "eight", self.pid)])
        self.listener.remove_notify_handler(got.append)

    async def test_close_while_another_task_listens(self) -> None:
        task = asyncio.create_task(self.take())
        await asyncio.sleep(0)
        started = time.monotonic()
        await self.listener.close()
        self.assertLess(time.monotonic() - started, 5.0)
        with self.assertRaises(e.OperationalError) as caught:
            await asyncio.wait_for(task, 10)
        self.assertEqual(str(caught.exception), "the connection is closed")

    async def test_cancelled_while_waiting(self) -> None:
        task = asyncio.create_task(self.take())
        await asyncio.sleep(0)
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await task
        self.assertIs(self.listener.closed, False)
        self.assertIs(self.listener.lock.locked(), False)
        cur = await self.listener.execute("select 1")
        self.assertEqual(await cur.fetchone(), (1,))


class AsyncCancelTest(AsyncConnTestCase):
    async def start(self, **kwargs: Any) -> tuple[Any, Any, Any]:
        """Start the sleeping statement in a task; return when it runs."""
        conn = await self.connect(**kwargs)
        started = asyncio.Event()
        conn.add_notice_handler(lambda diag: started.set())
        cur = conn.cursor()
        task = asyncio.create_task(cur.execute(SLEEP))
        await asyncio.wait_for(started.wait(), 10)
        return conn, cur, task

    async def test_cancelled_task_in_autocommit(self) -> None:
        conn, cur, task = await self.start(autocommit=True)
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await task
        self.assertIs(conn.closed, False)
        self.assertEqual(status(conn), "IDLE")
        self.assertIs(conn.lock.locked(), False)
        self.assertIsNone(cur.pgresult)
        loop = asyncio.get_running_loop()
        self.assertIs(loop.remove_reader(conn.fileno()), False)
        self.assertEqual(await (await conn.execute("select 1")).fetchone(), (1,))

    async def test_cancelled_task_in_a_transaction(self) -> None:
        conn, cur, task = await self.start()
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await task
        self.assertEqual(status(conn), "INERROR")
        with self.assertRaises(e.InternalError) as caught:
            await conn.execute("select 1")
        self.assertEqual(caught.exception.sqlstate, "25P02")
        await conn.rollback()
        self.assertEqual(await (await conn.execute("select 1")).fetchone(), (1,))

    async def test_timeout_of_the_caller(self) -> None:
        conn = await self.connect(autocommit=True)
        with self.assertRaises(asyncio.TimeoutError):
            await asyncio.wait_for(conn.execute("select pg_sleep(30)"), 0.3)
        self.assertEqual(status(conn), "IDLE")
        self.assertEqual(await (await conn.execute("select 1")).fetchone(), (1,))

    async def test_cancelled_inside_a_transaction_block(self) -> None:
        conn = await self.connect(autocommit=True)
        await conn.execute("create table t (id int)")
        started = asyncio.Event()
        conn.add_notice_handler(lambda diag: started.set())

        async def work() -> None:
            async with conn.transaction():
                await conn.execute("insert into t values (1)")
                await conn.execute(SLEEP)

        task = asyncio.create_task(work())
        await asyncio.wait_for(started.wait(), 10)
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await task
        self.assertEqual(status(conn), "IDLE")
        self.assertEqual(conn._num_transactions, 0)
        cur = await conn.execute("select count(*) from t")
        self.assertEqual(await cur.fetchone(), (0,))

    async def test_second_cancel_closes_the_connection(self) -> None:
        conn, cur, task = await self.start(autocommit=True)
        with self.assertLogs("stuntgres", logging.WARNING) as logs:
            task.cancel()
            await asyncio.sleep(0)
            task.cancel()
            with self.assertRaises(asyncio.CancelledError):
                await task
        self.assertEqual(
            [record.getMessage() for record in logs.records],
            [
                "cannot end the interrupted statement (CancelledError: ):"
                " closing the connection"
            ],
        )
        self.assertIs(conn.closed, True)
        self.assertIs(conn.broken, True)
        self.assertIs(conn.lock.locked(), False)
        with self.assertRaises(e.OperationalError) as caught:
            await conn.execute("select 1")
        self.assertEqual(str(caught.exception), "the connection is lost")

    async def test_cancel_method_from_another_task(self) -> None:
        conn, cur, task = await self.start(autocommit=True)
        await conn.cancel_safe()
        with self.assertRaises(e.OperationalError) as caught:
            await asyncio.wait_for(task, 10)
        self.assertEqual(type(caught.exception).__name__, "QueryCanceled")
        self.assertEqual(caught.exception.sqlstate, "57014")
        self.assertEqual(await (await conn.execute("select 1")).fetchone(), (1,))


class AsyncSharingTest(AsyncConnTestCase):
    async def test_close_while_another_task_waits(self) -> None:
        conn = await self.connect(autocommit=True)
        running = asyncio.Event()
        conn.pgconn.notice_handler = lambda result: running.set()

        async def worker() -> Any:
            conn.pgconn.send_query(
                b"do $$ begin raise notice 'started'; perform pg_sleep(30); end $$"
            )
            async with conn.lock:
                results = await conn.wait(generators.execute(conn.pgconn))
            return [r.error_field(pq.DiagnosticField.SQLSTATE) for r in results]

        task = asyncio.create_task(worker())
        await asyncio.wait_for(running.wait(), 10)
        await conn.close()
        self.assertEqual(await asyncio.wait_for(task, 10), [b"57014"])
        self.assertIs(conn.closed, True)

    async def test_notice_handlers(self) -> None:
        conn = await self.connect(autocommit=True)
        seen: list[Any] = []
        conn.add_notice_handler(lambda diag: seen.append(diag.message_primary))
        await arun(conn, "do $$ begin raise notice 'async notice'; end $$")
        self.assertEqual(seen, ["async notice"])

    async def test_tasks_share_a_connection(self) -> None:
        conn = await self.connect(autocommit=True)

        async def worker(number: int) -> list[Any]:
            return [
                (await arun(conn, f"select {number * 100 + i}"))[0][0]
                for i in range(10)
            ]

        results = await asyncio.gather(*(worker(n) for n in range(4)))
        for number, got in enumerate(results):
            self.assertEqual(got, [str(number * 100 + i).encode() for i in range(10)])

    async def test_cancel_safe(self) -> None:
        conn = await self.connect(autocommit=True)
        conn.cancel()
        await conn.cancel_safe()
        await conn.cancel_safe(timeout=1.0)
        self.assertEqual(await arun(conn, "select 1"), [(b"1",)])

    @support.only_modes("oracle", "server")
    async def test_connect_timeout(self) -> None:
        silent = socket.socket()
        self.addCleanup(silent.close)
        silent.bind(("127.0.0.1", 0))
        silent.listen(1)
        port = silent.getsockname()[1]
        ticks = 0

        async def ticker() -> None:
            nonlocal ticks
            while True:
                await asyncio.sleep(0.01)
                ticks += 1

        task = asyncio.create_task(ticker())
        started = time.monotonic()
        with self.assertRaises(e.ConnectionTimeout) as caught:
            await AsyncConnection.connect(
                f"host=127.0.0.1 port={port} dbname=x user=y sslmode=disable"
                " gssencmode=disable",
                connect_timeout=1,
            )
        elapsed = time.monotonic() - started
        task.cancel()
        self.assertEqual(str(caught.exception), "connection timeout expired")
        self.assertGreaterEqual(elapsed, 2.0)
        self.assertLess(elapsed, 10.0)
        self.assertGreater(ticks, 50)
```

#### CPYTHON-MODULE-01-EXPORTS

Files: `stuntgres/__init__.py`, `tests/test_module.py`. Design sections to implement: 2; also read: 1.5, 3.3, 3.4.

Step. Replace `stuntgres/__init__.py` (the version-only file of CPYTHON-BUILD-01-SKELETON) with the file printed in 2. This is the only part that edits that file. Three tests start a fresh interpreter with `subprocess` to observe what an import does; they pass `PYTHONPATH` so that the tree under test is imported.

Before the code exists: With the version-only file: `AttributeError: module 'stuntgres' has no attribute 'apilevel'` and the like in eight of nine unit tests.

Tests to write first (11 cases in oracle and in server mode, of which 9 also run in unit mode):

`tests/test_module.py`:

```python
"""Tests of the module surface: the names of the package stuntgres."""
from __future__ import annotations

import os
import pickle
import subprocess
import sys
import unittest

import stuntgres
from stuntgres import errors as e
from tests import support

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
EXPECTED = [
    "AsyncConnection", "AsyncCursor", "AsyncTransaction", "BINARY", "Binary",
    "Column", "Connection", "ConnectionInfo", "Cursor", "DATETIME", "DataError",
    "DatabaseError", "Date", "DateFromTicks", "Error", "IntegrityError",
    "InterfaceError", "InternalError", "IsolationLevel", "NUMBER",
    "NotSupportedError", "Notify", "OperationalError", "ProgrammingError",
    "ROWID", "Rollback", "STRING", "Time", "TimeFromTicks", "Timestamp",
    "TimestampFromTicks", "Transaction", "Warning", "__version__", "adapters",
    "apilevel", "connect", "errors", "paramstyle", "pq", "rows", "threadsafety",
]


def in_a_fresh_process(code: str) -> str:
    """Run code in a new interpreter with the package on its path."""
    env = dict(os.environ, PYTHONPATH=ROOT)
    done = subprocess.run(
        [sys.executable, "-W", "error", "-c", code],
        capture_output=True, text=True, timeout=60, env=env,
    )
    assert done.returncode == 0, done.stderr
    return done.stdout.strip()


class ModuleTest(unittest.TestCase):
    def test_globals(self) -> None:
        self.assertEqual(stuntgres.apilevel, "2.0")
        self.assertEqual(stuntgres.threadsafety, 2)
        self.assertEqual(stuntgres.paramstyle, "pyformat")
        self.assertEqual(stuntgres.__version__, "0.1.0")

    def test_all(self) -> None:
        self.assertEqual(stuntgres.__all__, EXPECTED)
        for name in EXPECTED:
            self.assertTrue(hasattr(stuntgres, name), name)
        with self.assertRaises(AttributeError) as caught:
            stuntgres.nosuch
        self.assertEqual(
            str(caught.exception), "module 'stuntgres' has no attribute 'nosuch'"
        )

    def test_names_are_the_objects_of_their_modules(self) -> None:
        from stuntgres import _column, _conn_core, _connection_info, _defaults
        from stuntgres import connection, connection_async, cursor, cursor_async
        from stuntgres import dbapi20, transaction, transaction_async

        pairs = [
            (stuntgres.Connection, connection.Connection),
            (stuntgres.Cursor, cursor.Cursor),
            (stuntgres.AsyncConnection, connection_async.AsyncConnection),
            (stuntgres.AsyncCursor, cursor_async.AsyncCursor),
            (stuntgres.Transaction, transaction.Transaction),
            (stuntgres.AsyncTransaction, transaction_async.AsyncTransaction),
            (stuntgres.Rollback, transaction.Rollback),
            (stuntgres.Column, _column.Column),
            (stuntgres.ConnectionInfo, _connection_info.ConnectionInfo),
            (stuntgres.IsolationLevel, _conn_core.IsolationLevel),
            (stuntgres.Notify, _conn_core.Notify),
            (stuntgres.adapters, _defaults.adapters),
            (stuntgres.STRING, dbapi20.STRING),
            (stuntgres.Binary, dbapi20.Binary),
            (stuntgres.TimestampFromTicks, dbapi20.TimestampFromTicks),
        ]
        for exported, original in pairs:
            self.assertIs(exported, original)
        self.assertEqual(stuntgres.connect, connection.Connection.connect)
        for name in (
            "Warning", "Error", "InterfaceError", "DatabaseError", "DataError",
            "OperationalError", "IntegrityError", "InternalError",
            "ProgrammingError", "NotSupportedError",
        ):
            self.assertIs(getattr(stuntgres, name), getattr(e, name), name)
            self.assertEqual(getattr(stuntgres, name).__module__, "stuntgres")

    def test_public_classes_say_stuntgres(self) -> None:
        for name in (
            "Connection", "Cursor", "AsyncConnection", "AsyncCursor", "Transaction",
            "AsyncTransaction", "Rollback", "Column", "ConnectionInfo",
        ):
            self.assertEqual(getattr(stuntgres, name).__module__, "stuntgres", name)

    def test_sqlstate_classes_are_installed(self) -> None:
        self.assertEqual(stuntgres.errors.UniqueViolation.sqlstate, "23505")
        self.assertIs(stuntgres.errors.lookup("23505"), e.UniqueViolation)
        self.assertTrue(issubclass(e.UniqueViolation, stuntgres.IntegrityError))
        self.assertEqual(e.Diagnostic.__module__, "stuntgres.errors")

    def test_errors_can_be_pickled(self) -> None:
        from stuntgres._diagnostic import ErrorInfo

        fields = {ord("C"): b"ZZ123", ord("M"): b"boom", ord("n"): b"c1"}
        error = stuntgres.DatabaseError("boom", pgresult=ErrorInfo(fields, b""))
        error.sqlstate = "ZZ123"
        copy = pickle.loads(pickle.dumps(error))
        self.assertIs(type(copy), stuntgres.DatabaseError)
        self.assertEqual((str(copy), copy.sqlstate), ("boom", "ZZ123"))
        self.assertEqual(copy.diag.constraint_name, "c1")
        plain = pickle.loads(pickle.dumps(stuntgres.OperationalError("lost")))
        self.assertIs(type(plain), stuntgres.OperationalError)
        self.assertIsNone(plain.diag.sqlstate)
        named = pickle.loads(pickle.dumps(e.UniqueViolation("dup")))
        self.assertIs(type(named), e.UniqueViolation)

    def test_import_loads_no_library_and_starts_nothing(self) -> None:
        out = in_a_fresh_process(
            "import sys, threading, stuntgres\n"
            "maps = open('/proc/self/maps').read()\n"
            "print('libpq' in maps or 'libspq' in maps, threading.active_count(),\n"
            "      'asyncio' in sys.modules, 'stuntgres._defaults' in sys.modules,\n"
            "      'stuntgres.connection_async' in sys.modules)\n"
        )
        self.assertEqual(out, "False 1 False False False")

    def test_lazy_names(self) -> None:
        out = in_a_fresh_process(
            "import sys, stuntgres\n"
            "a = stuntgres.adapters\n"
            "c = stuntgres.AsyncConnection\n"
            "print('stuntgres._defaults' in sys.modules, 'asyncio' in sys.modules,\n"
            "      'adapters' in vars(stuntgres), c.__name__)\n"
        )
        self.assertEqual(out, "True True True AsyncConnection")
        out = in_a_fresh_process("from stuntgres import *\nprint(AsyncCursor.__name__)")
        self.assertEqual(out, "AsyncCursor")

    def test_errors_module_alone(self) -> None:
        out = in_a_fresh_process(
            "import stuntgres.errors as e\n"
            "print(e.UniqueViolation.__mro__[1].__name__, e.lookup('42P01').__name__)\n"
        )
        self.assertEqual(out, "IntegrityError UndefinedTable")


class ModuleDatabaseTest(support.DatabaseTestCase):
    def test_connect_and_run(self) -> None:
        with stuntgres.connect(self.conninfo) as conn:
            self.assertIs(type(conn), stuntgres.Connection)
            cur = conn.cursor()
            cur.execute("select %s::int + 1 as n, %s as t", (41, "x"))
            description = cur.description
            assert description is not None
            self.assertTrue(description[0].type_code == stuntgres.NUMBER)
            self.assertTrue(description[1].type_code == stuntgres.STRING)
            self.assertEqual(cur.fetchone(), (42, "x"))
            with self.assertRaises(stuntgres.ProgrammingError) as caught:
                cur.execute("select * from nosuch")
            self.assertIsInstance(caught.exception, stuntgres.errors.UndefinedTable)
            self.assertIsInstance(caught.exception, conn.ProgrammingError)
            conn.rollback()
        self.assertTrue(conn.closed)

    def test_global_adapters_configure_new_connections(self) -> None:
        self.assertIs(
            stuntgres.adapters.get_loader(23), stuntgres.adapters.get_loader(23)
        )
        with stuntgres.connect(self.conninfo) as conn:
            self.assertIs(
                conn.adapters.get_loader(23), stuntgres.adapters.get_loader(23)
            )
            self.assertIsNot(conn.adapters, stuntgres.adapters)
```

#### CPYTHON-TEST-04-DBAPI20

Files: `tests/test_dbapi20_compliance.py`. Design sections to implement: 10; also read: 6.9.

Step. This part adds one test module and changes no file of the package. Write `tests/test_dbapi20_compliance.py` as printed in 10. The vendored suite `tests/third_party/dbapi20.py` is never edited (C3). All 36 cases must pass at once in oracle and in server mode, with warnings as errors; if the import of the vendored file itself fails or warns, stop and report (PQ 9.8 says what the owner then decides).

Before the code exists: Nothing fails first (tests only).

Tests to write first (36 cases in oracle and in server mode; none runs in unit mode):

`tests/test_dbapi20_compliance.py`: the file printed in section 10.

#### CPYTHON-ENGINE-04-START-TRANSACTION-TAG

An engine gap found in server mode (rule D8). Smallest reproduction: the statement
`START TRANSACTION` (simple or extended protocol).

| | PostgreSQL 18.6 | stuntgres server (c1dcedc931) |
|---|---|---|
| `start transaction` | command tag `START TRANSACTION` | command tag `BEGIN` |
| `start transaction isolation level serializable, read only` | `START TRANSACTION` | `BEGIN` |
| `start transaction; select 1` (one simple query) | `START TRANSACTION`, then `SELECT 1` | `BEGIN`, then `SELECT 1` |
| `begin`, `begin work` | `BEGIN` | `BEGIN` (already correct) |
| `commit`, `end`, `commit and chain` | `COMMIT` | `COMMIT` (already correct) |
| `rollback`, `abort` | `ROLLBACK` | `ROLLBACK` (already correct) |

The tag is chosen in `internal/engine/session/command_tag.go`, function
`ClassifyCommandTag`, clause `*parser.TransactionControl`, `case parser.KindStart: base =
"BEGIN"`; the parser already records which keyword was written
(`parser.TransactionControl.StartKeyword`, `internal/parser/ast.go`). Needed by
`ExecuteTest.test_command_tags_and_rowcount` of CPYTHON-CUR-01D-EXECUTE-TESTS
(`cursor.statusmessage`), and by any client that shows or logs command tags.

That file is named by three pending parts of another family (CLEANUP-AST-KIND-ENUMS,
CLEANUP-STMT-02-CONSTANT-TAGS, CLEANUP-STMT-03-TRAITS-TAG; the last one moves the switch
into `session.StatementTraits` and renames the file). This part therefore starts by
checking their status and stops if one of them is in progress (its record says how).

#### CPYTHON-ENGINE-05-ARRAY-CAST-COLUMN-NAME

An engine gap found in server mode (rule D8). Smallest reproduction: `select
'{1}'::int4[]` and reading the name of the result column.

| Query | Column name, PostgreSQL 18.6 | stuntgres server (c1dcedc931) |
|---|---|---|
| `select '{1}'::int4[]`, `select '{1}'::int[]`, `select cast('{1}' as int4[])`, `select '{1}'::integer array`, `select '{1}'::pg_catalog.int4[]`, `select '{1}'::int4[][]`, `select null::int4[]` | `int4` | `_int4` |
| `select '{a}'::text[]` | `text` | `_text` |
| `select '{a}'::varchar(10)[]` | `varchar` | `_varchar` |
| `select '{a}'::char(5)[]` | `bpchar` | `_bpchar` |
| `select '{1.5}'::numeric(10,2)[]` | `numeric` | `_numeric` |
| `select '{}'::timestamptz[]`, `select '{t}'::bool[]` | `timestamptz`, `bool` | `_timestamptz`, `_bool` |
| `select array[1]::int8[]` | `array` | `_int8` |
| `select '{1}'::_int4` | `_int4` | `_int4` (already correct) |
| `select '1'::int4`, `select 'a'::char(5)`, `select array[1,2]`, `select ('{1}'::int4[])[1]`, `select '{1}'::int4[] \|\| 2` | `int4`, `bpchar`, `array`, `int4`, `?column?` | the same (already correct) |

PostgreSQL names the column of a type cast after the type name as written, without its
array bounds, and keeps the name `array` of an `ARRAY[...]` constructor through a cast.
The engine derives the name in `internal/engine/analyze/expression_literal_input.go`,
function `ExpressionName`: the clause `parser.CastExpr` falls back to
`usage.Type.Name()`, which for an array type is the catalog name with the leading
underscore, and the clause `parser.ArrayExpr` returns `"array"` as a weak name, which a
cast then replaces. No pending part of another family names that file (checked on
2026-11-11). Needed by `ExecuteTest.test_column_names_of_casts` of
CPYTHON-CUR-01D-EXECUTE-TESTS (`cursor.description`), and by any client that shows
column headers.

## 12. Experiments behind this design

Everything is under `.cswd/tmp/CPYTHON-DESIGN-DBAPI/` (not part of the repository):
`log.txt` (every command and its output, written by `run_all.sh`), the scripts in
`exp1` to `exp8`, the designed code in `scratch/stuntgres` and `scratch/tools`, the
designed tests in `scratch/tests`, the document generator in `docgen` and the record
generator in `parts`. Oracle mode: libpq 18.6 extracted from the pinned image
(`liblber`, `libldap`, `libpq`, opened in that order, PQ 9.3) against a container of
`postgres:18.6-bookworm@sha256:afc7e2d4...`; server mode: the same libpq against
`cmd/stuntgres` built from commit `c1dcedc931` with the tag `stuntgres_reduced_kdf`.
Host: Linux x86_64, CPython 3.12.3, Go 1.26.

| # | Experiment | Result |
|---|---|---|
| 1 | Transaction status after every step: implicit and explicit `BEGIN`, `COMMIT`, `ROLLBACK`, errors inside and outside a transaction, a failing deferred `COMMIT`, every `BEGIN` option string, session defaults, savepoints (`exp1/exp1.py`, 135 steps). | Section 5.1. One difference in server mode: the command tag of `START TRANSACTION`. Every statement this layer sends answered identically. |
| 2 | Multi-statement strings through `send_query`: results, tags and statuses; an error in the first, the middle and the last statement; empty statements; transaction control inside (`exp2/exp2.py`, 41 strings). | Section 6.6. No difference in server mode. |
| 3 | Command tags and the derived `rowcount` for `SELECT`, `INSERT`, `UPDATE`, `DELETE`, `MERGE`, `COPY`, `FETCH`, `MOVE`, `CREATE TABLE AS`, DDL, utility statements and an empty query (`exp3/exp3.py`, 72 statements). | Section 6.4. Five differences in server mode, all in statements no designed test sends (below). |
| 4 | `fmod` and `fsize` of the core types as expressions and as table columns, and the derived precision, scale and sizes (`exp4/exp4.py`). | Section 6.2. One difference in server mode: the column name of a cast to an array type. |
| 5 | A statement in `pg_sleep(30)` interrupted by `SIGINT` and by task cancellation through `Connection.wait` and `AsyncConnection.wait`, with and without a transaction, with a cancel path that cannot finish, with a second cancellation, and `SIGINT` under `asyncio.run` (`exp5/exp5.py`). | Section 8.4. Identical output in both modes; every interruption returned in under a second and left the connection usable or, in the two failure cases, closed and broken. |
| 6 | `LISTEN` and `NOTIFY` between two connections: nothing arrived, a notification that arrives while the listener is idle, several at once with `stop_after`, one that arrives during another statement, own notifications, payloads, delivery at commit, handlers, `UNLISTEN`; notices raised by `RAISE NOTICE`, `WARNING` and `INFO` inside a function, `client_min_messages`, a raising handler; the asyncio twins (`exp6/exp6.py`). | Sections 8.1 and 8.2. Identical output in both modes (apart from object addresses in two log lines). |
| 7 | The scratch `Connection` and `Cursor` against the vendored compliance suite, executed on CPython 3.12.3 with warnings as errors (`exp7/cases.py`), as vendored and with the overrides; plus the probes behind the tables of 3.5, 4.2 and 4.7 (`exp7/probe_*.py`). | Section 10: 36 of 36 cases pass in both modes with three overrides; exactly those three fail without. |
| 8 | The SQLSTATE table: `errcodes.txt` of the pinned image, the derived table, the tool's output, import cost, and the class names compared with psycopg 3's (`exp8/derive.py`, section 3). | 41 classes and 253 codes; the generated module is 384 lines and imports in about 2 ms; all 253 names equal psycopg's. |
| - | Stage check (`docgen/stage.py`): for each of the 27 parts, a tree holding only the files of the part's prerequisite closure (over the 48 existing CPYTHON parts and the new ones; stubs where the owner is not in the closure) and the part's tests run there in unit, oracle and server mode; then the same with the part's own files left out (`--pre`). | Every part passes in its own tree in oracle mode; in server mode every part passes except the test-only part that needs the two engine fixes. With `--pre` every part with code fails as section 11.3 states. This is what found the import of a constant from a stub, a hidden dependency of a test on `transaction.py` and the pickling of the base exception classes before the module surface exists. |
| - | The whole scratch tree (PQ, ADAPT and this design: 50 test files) through the scratch copy of the designed runner. | unit: all pass; oracle: all pass; server: all pass except three tests, each waiting for one engine part (`CPYTHON-ENGINE-03`, `-04`, `-05`). The runner left no container and no server process behind. |

What the experiments changed is listed in 1.1. Problems they found in the first version
of the designed code, all fixed in what this document prints:

- `Connection.wait` read `pgconn.socket` before it started the generator, so leaving a
  `transaction()` block on a closed connection replaced the pending exception by
  `the connection is closed`. `_wait_fileno` (4.3) fixed it.
- The asyncio and the synchronous `wait` did not record a lost connection; `closed`
  asked the library. Both now go through `_note_status` (D11).
- `conn.cursor()` on a connection whose client encoding has no codec raised; it no
  longer calls the library.
- A session ended by `pg_terminate_backend` does not give an `AdminShutdown` error with
  a real libpq in nonblocking mode: `consume_input` fails with the server's `FATAL`
  text in an `OperationalError` (3.5).
- The notice of the interruption tests must be recorded as a flag, not kept: a kept
  notice result is a live `PGresult` object and falsifies the leak check.

Server-mode differences found by probes that **no statement of this layer and no
designed test** needs; no part was added for them (rule D8 covers what the binding
sends and what designed tests need; `.cswd/tmp/CPYTHON/known-gaps.md` has the earlier
ones):

| Probe | PostgreSQL 18.6 | stuntgres server | Note |
|---|---|---|---|
| `table t` | 8 rows, tag `SELECT 8` | `42601 syntax error at or near "table"` | the `TABLE` command |
| `do $$ begin null; end $$` | `DO` | `42601 syntax error at or near "null"` | the PL/pgSQL `NULL` statement |
| `move absolute 0 in cur2` after `fetch all` on a cursor declared without `SCROLL` | `MOVE 0` | `55000 cursor can only scan forward` | matters to CPYTHON-DESIGN-FEATURES: a named cursor's `scroll` sends `MOVE ABSOLUTE` |
| `explain select 1` | 1 row | 3 rows | plan text; not compared by anything |
| `checkpoint` | `CHECKPOINT` | `0A000 ... unsupported in embedded mode` | deliberate |
| parameters reported at connect | also `in_hot_standby`, `default_transaction_read_only`, `scram_iterations` | not reported (`ConnectionInfo.parameter_status` gives `None`) | `pgserver/startup.go`, `settingsSnapshot`; that file belongs to the pending `LIBSPQ-PGSESSION` chain |
| `diag.source_file`, `source_line`, `source_function` | set | `None` | already in `known-gaps.md` |

Not established: behavior on CPython 3.10 and 3.11 (no interpreter available; the code
was parsed with `feature_version=(3, 10)` and uses no library feature newer than 3.10
by reading); native mode (libspq does not exist yet), in particular that `close()`,
`cancel()` and the interruption path behave with `SPQcancel` and `SPQfinish` as they do
with libpq, and that a session ended by the engine arrives as a `FATAL` result with
SQLSTATE `57P01`; free-threaded CPython (the rules of 1.4 were applied by reading, not
measured).
