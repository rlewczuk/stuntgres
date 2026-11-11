# stuntgres for Python: a DB-API driver for the embedded engine

`stuntgres` is a Python package that runs the stuntgres engine inside the calling
process and exposes it through the Python Database API 2.0 (PEP 249). It exists so that
the unit tests of applications written for PostgreSQL can run against the embedded
engine instead of a PostgreSQL server.

**Status.** This document is the consumer contract that the implementation must meet.
The package is being built by the `CPYTHON-*` catalog parts on top of libspq
(`docs/LIBSPQ.md`); until they are done, treat every statement here as the
specification, not as shipped behavior. Internal design lives in
`docs/design/CPYTHON-PQ.md`, `CPYTHON-ADAPT.md` and `CPYTHON-DBAPI.md`. Sections marked
"to be specified" are written by the design part `CPYTHON-DESIGN-FEATURES`.

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

To be specified by `CPYTHON-DESIGN-FEATURES` (the wheel that carries `libspq.so`, built
by `make cpython`). `import stuntgres` loads no native library; the library is loaded by
the first connection.

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
| Optional extensions not provided | `messages`, `errorhandler`. Two-phase commit: section 4.6. |
| Closed objects | A closed cursor raises `InterfaceError`; a closed connection raises `OperationalError`. |

### 4.2 Names shared with psycopg 3

These have psycopg 3's names and meaning:

| Area | Names |
|---|---|
| Connection | `Connection.connect`, `closed`, `broken`, `autocommit`, `isolation_level`, `read_only`, `deferrable` and their `set_...` methods, `execute`, `transaction()`, `cursor(row_factory=...)`, `row_factory`, `cursor_factory`, `add_notice_handler`, `remove_notice_handler`, `add_notify_handler`, `remove_notify_handler`, `notifies(timeout=, stop_after=)`, `cancel`, `cancel_safe`, `info`, `adapters`, `pgconn`, `fileno` |
| Cursor | `statusmessage`, `rownumber`, `row_factory`, `pgresult`, `adapters`, `closed` |
| Module | `IsolationLevel`, `Notify`, `Column`, `ConnectionInfo`, `Transaction`, `Rollback`, `AsyncConnection`, `AsyncCursor`, `AsyncTransaction`, `adapters`, `rows.tuple_row`, `rows.dict_row`, `rows.namedtuple_row`, `conninfo.make_conninfo`, `conninfo.conninfo_to_dict`, `errors.Diagnostic`, `errors.lookup`, one exception class per SQLSTATE (`errors.UniqueViolation`, ...), `pq` (the low-level interface) |

Differences from psycopg 3 that code may notice: `execute` and `executemany` take no
`prepare` and no `binary` argument; `transaction()` returns the context manager
itself; `cursor_factory` is `None` when the default class is used; `ConnectionInfo` has
no `vendor`, `hostaddr`, `pipeline_status` and `timezone`; `cancel_safe` is `cancel`;
`notifies()` holds the connection only while it waits, and yields nothing while a notify
handler is registered; a Python `int` parameter is sent as `integer` when it fits
(psycopg starts at `smallint`).

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

To be specified by `CPYTHON-DESIGN-FEATURES`. Until then `execute` refuses a `COPY`
statement with `ProgrammingError` (the session stays usable) and `cursor(name)` raises
`NotSupportedError`.

### 4.6 Two-phase commit

To be specified by `CPYTHON-DESIGN-FEATURES`.

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
  connection attempt fails with a clear error, and connections inherited from the parent
  must not be used. With `multiprocessing`, use the `spawn` or `forkserver` start
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
  `executemany`, `commit`, `rollback`, the `set_...` methods, and, for symmetry with
  psycopg 3, the fetch methods and `scroll`. `cursor()`, `transaction()`, `nextset()`
  and `cancel()` are plain methods.
- The four session attributes are read as attributes and set with
  `await conn.set_autocommit(True)` and so on; assigning them raises `AttributeError`.
- Tasks of one event loop may share a connection; a connection belongs to one loop.
- Cancelling a task that waits for a statement (also through `asyncio.wait_for`)
  cancels the statement; the connection stays usable.

## 10. Test isolation helpers

To be specified by `CPYTHON-DESIGN-FEATURES`: dropping and resetting a cluster, dump and
restore, and the recommended fixture pattern. What already follows from section 3: a
test that connects with a host name of its own (`host=test-<unique>`) has a cluster
nobody else sees.

## 11. Building and testing

To be specified by `CPYTHON-DESIGN-FEATURES` (`make cpython`, `make cpython-check`).
The test suite of the binding is run with
`python3 bindings/cpython/tools/runtests.py --mode <unit|oracle|server|native>`
(`docs/design/CPYTHON-PQ.md` section 9).
