# stuntgres for Python: a DB-API driver for the embedded engine

`stuntgres` is a Python package that runs the stuntgres engine inside the calling
process and exposes it through the Python Database API 2.0 (PEP 249). It exists so that
the unit tests of applications written for PostgreSQL can run against the embedded
engine instead of a PostgreSQL server.

**Status.** This document is the consumer contract that the implementation must meet.
The package is being built by the `CPYTHON-*` catalog parts on top of libspq
(`docs/LIBSPQ.md`); until they are done, treat every statement here as the
specification, not as shipped behavior. Internal design lives in
`docs/design/CPYTHON-PQ.md`, `CPYTHON-ADAPT.md`, `CPYTHON-DBAPI.md` and
`CPYTHON-FEATURES.md`.

## 1. What it is

A PEP 249 driver whose database is **inside the process** of the program that uses it:
there is no server to start and nothing to connect to over a network. A test connects,
gets a cluster and a database of its own at once, and throws them away.

- Pure Python over `ctypes` and the native library `libspq` (`docs/LIBSPQ.md`), which
  the package carries. Standard library only; CPython 3.10 or newer; Linux with glibc,
  amd64 and arm64.
- The interface is PEP 249, with the extensions and names of psycopg 3 where it goes
  beyond PEP 249 (section 4). Code written for psycopg 3's `Connection` and `Cursor`
  mostly needs only the import changed.
- SQL is PostgreSQL 18's. This document does not restate the PostgreSQL manual.

What it is not:

- Not a client for a PostgreSQL server: it cannot connect over a network.
- Not psycopg: there is no `sql` composition module, no connection pool, no pipeline
  mode, no binary format, no automatic prepared statements, no client-side parameter
  binding and no `callproc`. A psycopg adapter, a SQLAlchemy dialect and a Django
  backend are separate, later work.
- Not durable storage: the data lives in the process (`docs/LIBSPQ.md` section 1).

## 2. Installing

The package is one wheel that carries the native library; it needs nothing else at run
time.

```sh
make cpython          # build/cpython/dist/stuntgres-<version>-py3-none-manylinux_<glibc>_<arch>.whl
python3 -m pip install build/cpython/dist/stuntgres-*.whl
```

- The wheel is tagged `py3-none-manylinux_<major>_<minor>_<arch>`: any CPython 3.10 or
  newer, on Linux with glibc at least `<major>.<minor>`, on the architecture it was built
  for (`x86_64` or `aarch64`). The glibc version is the lowest one that can load the
  bundled library, which is usually older than the build host's.
- `pip install bindings/cpython` (from a checkout, after `make libspq`) builds and
  installs the same wheel without downloading anything.
- `import stuntgres` loads no native library; the library is loaded by the first
  connection. `stuntgres.libspq_version()` tells its version.
- To use another build of the library, set the environment variable `STUNTGRES_LIBSPQ`
  to the absolute path of a `libspq.so` before the first connection.

## 3. Connecting

```python
import stuntgres

with stuntgres.connect("host=orders-test dbname=app user=alice") as conn:
    with conn.cursor() as cur:
        cur.execute("create table item (id int primary key, name text)")
        cur.execute("insert into item values (%s, %s)", (1, "pen"))
        cur.execute("select id, name from item")
        print(cur.fetchall())          # [(1, 'pen')]
# leaving the block commits and closes the connection
```

`connect(conninfo="", *, autocommit=False, row_factory=None, cursor_factory=None,
**kwargs)` returns a `Connection`. The connection string has libpq's two forms
(`keyword=value ...` and `postgresql://...`); keyword arguments are merged into it and
replace what the string says (`connect("host=a", dbname="b")`).

What the keywords mean here (`docs/LIBSPQ.md` section 5.1):

| Keyword | Meaning |
|---|---|
| `host` (or `hostaddr`), `port` | Name a **cluster**: an engine instance inside the process. Nothing is resolved or dialled; two connections with the same host and port see the same cluster, any other value is a different, independent one. Default `localhost`, `5432`. |
| `dbname` | A database of that cluster. Default: the user name. |
| `user` | The role of the session. Default `postgres`. |
| `password` | Stored, never checked. |
| `options`, `application_name`, `client_encoding` | Session defaults, as in PostgreSQL. |
| `connect_timeout` and every other libpq keyword | Accepted and checked for syntax; without effect, because a connection is made at once. |

A cluster, a database and a role that do not exist are **created by the first
connection that names them** (the role as a superuser); a cluster lives until it is
dropped (section 10) or the process exits. Two connections with the same string share
committed data; an uncommitted change is invisible to the other one. Environment
variables, service files and password files are never read.

No SQL is sent when a connection is made. `Connection.info` tells where a connection
leads and what the session reports (`host`, `port`, `dbname`, `user`, `server_version`,
`encoding`, `parameter_status(name)`, `dsn`).

`connection.close()` closes at once: an open transaction is rolled back, nothing is
waited for. Closing twice is allowed. A connection object that is dropped without
`close()` emits a `ResourceWarning`.

## 4. The API

### 4.1 PEP 249

Everything PEP 249 requires is there: the module globals (`apilevel` `"2.0"`,
`threadsafety` `2`, `paramstyle` `"pyformat"`), `connect`, the exception hierarchy, the
type objects and constructors, `Connection` (`close`, `commit`, `rollback`, `cursor`)
and `Cursor` (`description`, `rowcount`, `execute`, `executemany`, `fetchone`,
`fetchmany`, `fetchall`, `nextset`, `arraysize`, `setinputsizes`, `setoutputsize`,
`close`).

| PEP 249 point | Here |
|---|---|
| Parameters | `%s` and `%(name)s`, bound on the server; a literal percent sign is `%%` whenever parameters are passed. A statement without parameters is sent as it is and may contain several statements. |
| `description` | A list of `Column` objects, each also a 7-item sequence. `type_code` is the PostgreSQL type OID and compares equal to `STRING`, `BINARY`, `NUMBER`, `DATETIME`, `ROWID`. `null_ok` is always `None`. A query that returns no rows still has a description. |
| `rowcount` | The count of the command tag (rows returned or affected); -1 when the statement has none (DDL). After `executemany`, the sum. |
| `executemany(query, params_seq, *, returning=False)` | One round trip per parameter set. With `returning=True` every result is kept and `nextset()` walks them. |
| `nextset()` | Moves to the next result of a multi-statement string; `None` after the last. |
| `callproc` | Not provided. Use `execute("select f(%s)", ...)` or `execute("call p(%s)", ...)`. |
| `setinputsizes`, `setoutputsize` | Do nothing. |
| Optional extensions provided | `cursor.connection`, `cursor.rownumber`, `cursor.scroll()`, iteration over a cursor, `cursor.lastrowid` (always `None`), the exceptions as attributes of the connection, `execute` returning the cursor. |
| Optional extensions provided, continued | Two-phase commit (`xid`, `tpc_begin`, `tpc_prepare`, `tpc_commit`, `tpc_rollback`, `tpc_recover`): section 4.6. |
| Optional extensions not provided | `messages`, `errorhandler`. |
| Closed objects | A closed cursor raises `InterfaceError`; a closed connection raises `OperationalError`. |

### 4.2 Names shared with psycopg 3

These have psycopg 3's names and meaning:

| Area | Names |
|---|---|
| Connection | `Connection.connect`, `closed`, `broken`, `autocommit`, `isolation_level`, `read_only`, `deferrable` and their `set_...` methods, `execute`, `transaction()`, `cursor(row_factory=...)`, `cursor(name, scrollable=, withhold=)`, `row_factory`, `cursor_factory`, `server_cursor_factory`, `add_notice_handler`, `remove_notice_handler`, `add_notify_handler`, `remove_notify_handler`, `notifies(timeout=, stop_after=)`, `cancel`, `cancel_safe`, `xid` and the `tpc_...` methods, `info`, `adapters`, `pgconn`, `fileno` |
| Cursor | `statusmessage`, `rownumber`, `row_factory`, `pgresult`, `adapters`, `closed`, `copy()` |
| Module | `IsolationLevel`, `Notify`, `Column`, `ConnectionInfo`, `Transaction`, `Rollback`, `AsyncConnection`, `AsyncCursor`, `AsyncTransaction`, `Copy`, `AsyncCopy`, `ServerCursor`, `AsyncServerCursor`, `Xid`, `adapters`, `rows.tuple_row`, `rows.dict_row`, `rows.namedtuple_row`, `conninfo.make_conninfo`, `conninfo.conninfo_to_dict`, `errors.Diagnostic`, `errors.lookup`, one exception class per SQLSTATE (`errors.UniqueViolation`, ...), `pq` (the low-level interface) |

Differences from psycopg 3 that code may notice: `execute` and `executemany` take no
`prepare` and no `binary` argument; `transaction()` returns the context manager
itself; `cursor_factory` is `None` when the default class is used; `ConnectionInfo` has
no `vendor`, `hostaddr`, `pipeline_status` and `timezone`; `cancel_safe` is `cancel`;
`notifies()` holds the connection only while it waits, and yields nothing while a notify
handler is registered; a Python `int` parameter is sent as `integer` when it fits
(psycopg starts at `smallint`); `copy()` binds no parameters, has no binary row format
and no `writer` argument, and `set_types()` takes type OIDs; a server-side cursor is not
a subclass of `Cursor`, its `rowcount` is always -1 and its `scroll()` raises
`IndexError` when the target is beyond the last row.

### 4.3 Errors

Every error of the engine is an exception chosen by its SQLSTATE: a class named after
the condition (`errors.UniqueViolation`, `errors.UndefinedTable`,
`errors.SerializationFailure`, ... for every code of PostgreSQL 18's Appendix A), each a
subclass of the PEP 249 class of its SQLSTATE class. Catch either level:

```python
try:
    cur.execute("insert into item values (1, 'again')")
except stuntgres.errors.UniqueViolation as exc:      # or stuntgres.IntegrityError
    print(exc.sqlstate)                  # '23505'
    print(exc.diag.constraint_name)      # 'item_pkey'
    print(exc.diag.message_detail)       # 'Key (id)=(1) already exists.'
```

`exc.sqlstate` is the code (`None` for an error that did not come from the engine);
`exc.diag` gives every field of the message (`message_primary`, `message_detail`,
`message_hint`, `context`, `schema_name`, `table_name`, `column_name`,
`constraint_name`, `statement_position`, ...). `str(exc)` is the message as `psql`
would print it, without the leading `ERROR:`. `errors.lookup("23505")` returns the
class of a code. Exceptions can be pickled.

### 4.4 Rows

A cursor returns tuples. A **row factory** changes that, per connection or per cursor:

```python
from stuntgres.rows import dict_row, namedtuple_row

cur = conn.cursor(row_factory=dict_row)
cur.execute("select 1 as id, 'pen' as name").fetchone()     # {'id': 1, 'name': 'pen'}
```

### 4.5 COPY and server-side cursors

**COPY.** `cursor.copy(statement)` returns a context manager for one
`COPY ... FROM STDIN` or `COPY ... TO STDOUT` statement:

```python
with cur.copy("COPY item (id, name) FROM STDIN") as copy:
    copy.write_row((1, "pen"))               # values are converted like parameters
    copy.write(b"2\tink\n")                  # or send data in the COPY text format yourself
print(cur.rowcount)                          # 2

with cur.copy("COPY item TO STDOUT") as copy:
    copy.set_types([23, 25])                 # type OIDs: int4, text
    for row in copy.rows():                  # (1, 'pen'), (2, 'ink')
        ...
```

| Member | Use |
|---|---|
| `write(buffer)` | Send a block of `COPY FROM STDIN` data: `bytes`, `bytearray`, `memoryview`, or `str` (encoded in the client encoding). Blocks need not end at row boundaries. |
| `write_row(row)` | Send one row. Each value is converted as a parameter would be; `None` is NULL. Text format only. |
| `read()` | The next block of `COPY TO STDOUT` data as `bytes` (one row per block); `b""` at the end. Iterating over the copy object yields the blocks. |
| `read_row()`, `rows()` | The next row as a tuple (`None` at the end), or an iterator over the rows. Text format only. Values are `str` unless `set_types()` was called. |
| `set_types(types)` | Choose the Python types of `read_row()` by type OID, one per column (`Column.type_code` values work). |

- Leaving the block ends the COPY; `cursor.rowcount` is then the number of rows copied
  and `cursor.statusmessage` the tag (`COPY 2`). An error of the server (a row the table
  rejects) is raised when the block is left. Leaving a `COPY TO` block early reads and
  discards the rest.
- An exception inside the block aborts the COPY (nothing is stored; a `COPY TO` is
  cancelled) and propagates. Inside a transaction, roll back afterwards.
- While the block is open the connection does nothing else: another thread or task that
  uses it waits; code inside the block must use only the copy object.
- The statement is one statement and takes no parameters (PostgreSQL binds none in
  `COPY`); passing an empty sequence only turns `%%` into `%`. Other formats work
  through `read()` and `write()`, which pass the data through unchanged:
  `COPY item TO STDOUT (FORMAT csv, HEADER)`.
- `execute("COPY ...")` is refused with `ProgrammingError`; the session stays usable.

**Server-side cursors.** `connection.cursor(name)` returns a `ServerCursor`: the result
stays on the server (`DECLARE name CURSOR ...`) and is fetched in pieces.

```python
with conn.cursor("big") as cur:
    cur.execute("select id, name from item where id > %s order by id", (100,))
    while rows := cur.fetchmany(1000):
        ...
```

| Argument or member | Meaning |
|---|---|
| `cursor(name, *, row_factory=None, scrollable=None, withhold=False)` | `scrollable=True` declares the cursor `SCROLL`, `False` `NO SCROLL`, `None` leaves it to the server; `withhold=True` adds `WITH HOLD`. |
| `execute(query, params=None)` | Declares the cursor for one query; executing again closes the previous one first. |
| `fetchone()`, `fetchmany(size)`, `fetchall()` | `FETCH FORWARD` 1, `size` or `ALL` rows. |
| iteration | Fetches `cursor.itersize` rows (default 100) per round trip. |
| `scroll(value, mode="relative")` | Moves the cursor; `IndexError` when the target is before the first or beyond the last row. Use `scrollable=True` to move backward. |
| `description`, `rownumber` | The columns (available right after `execute`); the index of the next row. `rowcount` is always -1. |
| `close()` | Closes the cursor on the server if it still exists there. Also done by `with`. |
| `name`, `scrollable`, `withhold` | The arguments, read-only. |

- A cursor lives in the transaction that declared it. With `autocommit=False` (the
  default) that transaction is begun as usual and the cursor ends with `commit()` or
  `rollback()`. A `withhold=True` cursor survives `commit()`; it is the only kind that
  can be used with `autocommit=True`.
- No `executemany`, no `nextset` (it returns `None`), no `copy` on a server-side cursor.
- `connection.server_cursor_factory` is the class `cursor(name)` uses.

### 4.6 Two-phase commit

The PEP 249 two-phase commit extension, on `Connection` and (as coroutines, except
`xid`) on `AsyncConnection`:

```python
xid = conn.xid(42, "order-7781", "inventory")     # format id, global id, branch qualifier
conn.tpc_begin(xid)
conn.execute("update stock set n = n - 1 where item = %s", (17,))
conn.tpc_prepare()
conn.tpc_commit()                                 # or conn.tpc_rollback()
```

| Method | Effect |
|---|---|
| `xid(format_id, gtrid, bqual)` | Returns an `Xid`: `format_id` a non-negative 32-bit integer, `gtrid` and `bqual` strings of at most 64 bytes. An `Xid` is a sequence of the three values; a plain `str` may be used instead wherever an xid is expected. |
| `tpc_begin(xid)` | Begins a transaction with that identifier. Not in autocommit mode and not inside a transaction. |
| `tpc_prepare()` | First phase (`PREPARE TRANSACTION`). Afterwards the session is outside the transaction; only `tpc_commit()` or `tpc_rollback()` finish it. Raises `OperationalError` if the transaction had failed: PostgreSQL then rolls it back instead of preparing it. |
| `tpc_commit()`, `tpc_rollback()` | Finish the transaction begun with `tpc_begin`: the second phase after `tpc_prepare`, an ordinary commit or rollback without it. |
| `tpc_commit(xid)`, `tpc_rollback(xid)` | Finish a prepared transaction that another connection left behind (recovery). |
| `tpc_recover()` | The prepared transactions of the cluster as `Xid` objects with `prepared` (time), `owner` and `database` filled in. |

- Between `tpc_begin()` and the end of the transaction `commit()`, `rollback()` and
  `transaction()` raise `ProgrammingError`.
- PostgreSQL knows an `Xid` by the text `str(xid)`:
  `<format_id>_<base64 of gtrid>_<base64 of bqual>`, the form pgjdbc and psycopg use, so
  transactions can be recovered across those drivers. An identifier of another form is
  recovered as `Xid(None, <text>, None)`.
- A PostgreSQL server needs `max_prepared_transactions` above zero; the embedded engine
  needs no setting.

## 5. Types

Parameters and results are converted as follows. Types not listed arrive as `str` (the
text PostgreSQL prints) and can be sent as `str`; `connection.adapters` and
`stuntgres.adapters` accept user-written dumpers and loaders
(`docs/design/CPYTHON-ADAPT.md` section 3.6).

| PostgreSQL | Python | Notes |
|---|---|---|
| `boolean` | `bool` | |
| `smallint`, `integer`, `bigint`, `oid` | `int` | A Python `int` is sent as `integer`, `bigint` or `numeric`, whichever is the smallest that holds it. |
| `real`, `double precision` | `float` | |
| `numeric` | `decimal.Decimal` | Exact; the scale is kept. |
| `text`, `varchar`, `char`, `name` | `str` | A `str` parameter is sent untyped, so the statement decides its type. |
| `bytea` | `bytes` | Also `bytearray` and `memoryview` as parameters; `Binary(x)` is `bytes(x)`. |
| `date` | `datetime.date` | |
| `time`, `timetz` | `datetime.time` | |
| `timestamp` | `datetime.datetime` (naive) | |
| `timestamptz` | `datetime.datetime` (aware) | The wall time and offset the session's `TimeZone` gives. |
| `interval` | `datetime.timedelta` | |
| `uuid` | `uuid.UUID` | |
| `json`, `jsonb` | what `json.loads` returns | Send with `stuntgres.types.json.Json(obj)` or `Jsonb(obj)`. |
| arrays of the above | `list` (nested for more dimensions) | |
| SQL `NULL` | `None` | |

Values Python cannot represent (infinite dates, years above 9999, ...) raise
`DataError` when fetched; cast them to `text` in the query. The client encoding is the
session's `client_encoding` (UTF-8 by default); with `SQL_ASCII` only ASCII text can be
passed and read as `str`.

## 6. Transactions

- `autocommit` is `False` by default: the first statement after connecting, committing
  or rolling back starts a transaction, and `commit()` or `rollback()` ends it. Nothing
  is committed until you say so; `close()` rolls back.
- With `autocommit=True` every statement is its own transaction.
- `isolation_level` (`IsolationLevel.READ_COMMITTED`, ...), `read_only` and `deferrable`
  set the characteristics of the transactions the connection starts; `None` (the
  default) leaves the session default.
- These four attributes cannot be changed while a transaction is open
  (`ProgrammingError`).
- `with connection:` commits when the block ends normally, rolls back when it raises,
  and **closes the connection** either way.
- `with connection.transaction():` is a transaction block that can be nested: the
  outermost one begins and commits (or rolls back, when the block raises); a nested one
  is a savepoint. `raise stuntgres.Rollback` leaves the block and rolls it back without
  an error. `commit()` and `rollback()` are not allowed inside.

```python
with conn.transaction():
    cur.execute("insert into item values (2, 'ink')")
    try:
        with conn.transaction():                       # a savepoint
            cur.execute("insert into item values (2, 'dup')")
    except stuntgres.errors.UniqueViolation:
        pass                                           # the outer block goes on
```

- After an error inside a transaction, statements fail (`errors.InFailedSqlTransaction`)
  until `rollback()`, or until the enclosing `transaction()` block is left.
- The driver follows transaction control that is sent as SQL (`BEGIN`, `COMMIT`,
  `ROLLBACK` through `execute`).

## 7. Notices, notifications, cancel

- `connection.add_notice_handler(callback)`: the callback gets a `Diagnostic` for every
  notice and warning of the session (`RAISE NOTICE`, ...), while the statement runs.
- `LISTEN` / `NOTIFY`: `for notify in connection.notifies(timeout=5.0, stop_after=1):`
  yields `Notify(channel, payload, pid)` objects as they arrive, or register a callback
  with `add_notify_handler`. Notifications are read while a statement runs and inside
  `notifies()`, not while the connection is idle.
- Handlers must not use the connection they belong to; an exception they raise is logged
  (logger `stuntgres`), not raised.
- `connection.cancel()` cancels the statement that is running; it may be called from
  another thread. The statement fails with `errors.QueryCanceled`.
- Ctrl-C during a statement cancels it and raises `KeyboardInterrupt`; the connection
  stays usable (inside a transaction, roll back first).

## 8. Threads, processes and fork

- **Threads.** `threadsafety` is 2: threads may share the module and connections. Each
  operation on a connection is atomic; threads that share a connection share its
  transaction. Do not share a cursor between threads. `cancel()`, `close()`, `closed`
  and `broken` may be used from any thread at any time; read `connection.info` only
  while no other thread runs a statement on that connection.
- **Processes.** The data of a cluster lives in the process that created it. Another
  process that uses the same connection string gets its own, empty cluster.
- **Fork.** Connect **after** forking. The native library cannot be used in a child of
  a process that had loaded it (`docs/LIBSPQ.md` section 7, rule 3): there every
  connection attempt raises `InterfaceError` (`libspq cannot be used in a process forked
  after the library was loaded`), and connections inherited from the parent must not be
  used. With `multiprocessing`, use the `spawn` or `forkserver` start
  method, or make the first connection inside the worker.
- **Free-threaded CPython.** The package does not rely on the GIL.

## 9. asyncio

`AsyncConnection` and `AsyncCursor` are the same API with coroutines; statements do not
block the event loop.

```python
async with await stuntgres.AsyncConnection.connect("host=orders-test") as conn:
    cur = await conn.execute("select %s::int + 1", (41,))
    print(await cur.fetchone())                        # (42,)
    async with conn.transaction():
        await conn.execute("create table t (id int)")
    async for notify in conn.notifies(timeout=1.0):
        ...
```

- Methods that talk to the engine are coroutines: `connect`, `close`, `execute`,
  `executemany`, `commit`, `rollback`, the `set_...` methods, the `tpc_...` methods,
  `reset_database`, `dump`, `restore`, and, for symmetry with psycopg 3, the fetch
  methods and `scroll`. `cursor()`, `copy()`, `xid()`, `transaction()`, `nextset()` and
  `cancel()` are plain methods.
- `cursor.copy()` returns an `AsyncCopy` for `async with`; its `write`, `write_row`,
  `read` and `read_row` are coroutines, and it and its `rows()` are iterated with
  `async for`. `conn.cursor(name)` returns an `AsyncServerCursor`.
- The four session attributes are read as attributes and set with
  `await conn.set_autocommit(True)` and so on; assigning them raises `AttributeError`.
- Tasks of one event loop may share a connection; a connection belongs to one loop.
- Cancelling a task that waits for a statement (also through `asyncio.wait_for`)
  cancels the statement; the connection stays usable.

## 10. Test isolation helpers

A cluster is named by the `host` and `port` of the connection string and created by the
first connection that names it (section 3), so a test that connects with a name of its
own has an engine nobody else sees. Four tools go with that; they exist only in this
driver (they are libspq extensions, `docs/LIBSPQ.md` section 6):

| Name | Effect |
|---|---|
| `stuntgres.testing.temp_cluster(dbname="test", user="test")` | A context manager that yields the connection string of a new cluster with a unique name and drops the cluster when the block ends. |
| `stuntgres.drop_cluster(host="localhost", port=5432)` | Forgets a cluster with everything in it. Fails with `OperationalError` while a connection to it is open; a cluster that does not exist is no error. |
| `connection.reset_database()` | Empties the connected database: every persistent object and all data. The cluster, its roles, its other databases and the connection stay. |
| `connection.dump()`, `connection.restore(data)` | The connected database as `bytes`, and back. `restore` replaces what the database holds. |

`reset_database`, `dump` and `restore` need a session outside a transaction
(`ProgrammingError` otherwise); on `AsyncConnection` they are coroutines.
`stuntgres.libspq_version()` returns the version of the native library.

The recommended fixture: one cluster per test.

```python
import unittest
import stuntgres
from stuntgres.testing import temp_cluster

class OrderTest(unittest.TestCase):
    def setUp(self):
        cluster = temp_cluster()
        self.conninfo = cluster.__enter__()
        self.addCleanup(cluster.__exit__, None, None, None)     # drops the cluster

    def test_insert(self):
        with stuntgres.connect(self.conninfo) as conn:
            conn.execute("create table orders (id int primary key)")
            ...
```

With pytest: `@pytest.fixture` whose body is `with temp_cluster() as conninfo: yield
conninfo`. Close every connection before the block ends: the drop fails while one is
open, which makes a test that leaks a connection fail.

When creating the schema is expensive, create it once, keep `template = conn.dump()`,
and start each test with `conn.restore(template)` on a connection to a fresh cluster or
after `conn.reset_database()`.

Data never crosses process boundaries: with `pytest-xdist` every worker has its own
engine, and the same connection string means a different, empty cluster in each.

## 11. Building and testing

```sh
make cpython          # build the wheel (builds libspq first)
make cpython-check    # build it, unpack it and run the test suite and a smoke test against it
```

Both need Go with cgo and a C compiler (for libspq, `docs/LIBSPQ.md` section 9) and
`python3` (CPython 3.10 or newer; `PYTHON=` chooses another interpreter). Neither needs
pip or a network, and neither is part of `make quickcheck`. `make cpython-check
REFERENCE=1` also runs the suite against real PostgreSQL (needs Docker), `SERVER=1`
against the stuntgres wire server.

The test suite of the binding is run with
`python3 bindings/cpython/tools/runtests.py --mode <unit|oracle|server|native>`
(`docs/design/CPYTHON-PQ.md` section 9); `bindings/cpython/README.md` describes the
modes.
