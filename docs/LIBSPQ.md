# libspq: C bindings for the embedded engine

libspq is a C library that runs the stuntgres engine inside the calling process and
exposes it through a subset of the PostgreSQL `libpq` API under renamed symbols. It
exists so that language bindings (Python, Ruby, Node.js, Java) can be written on top of
one C ABI and let application unit tests run against the embedded engine instead of a
PostgreSQL server.

**Status.** This document is the consumer contract that the implementation must meet.
The library is being built by the `LIBSPQ-*` catalog parts; until they are done, treat
every statement here as the specification, not as shipped behavior. Internal design
lives in `docs/design/LIBSPQ-PGSESSION.md`, `LIBSPQ-CLIENT.md` and `LIBSPQ-CABI.md`.

libspq is not a general replacement for libpq. It has no network, no authentication, no
TLS, and only the functions the bindings need.

## 1. Model

| libpq concept | libspq meaning |
|---|---|
| server (`host`, `port`) | An in-process **cluster**: one engine with its own roles and databases. It is created the first time a connection names it and lives until `SPQXclusterDrop` or process exit. Clusters are isolated from each other. |
| database (`dbname`) | A logical database inside the cluster. Created on connect when missing. |
| role (`user`) | A role inside the cluster. Created on connect when missing, as `LOGIN SUPERUSER`. |
| password, TLS, GSS | Ignored. Every connection is trusted. |
| connection (`PGconn`) | One engine session. Session state (settings, transactions, prepared statements, temporary objects, `LISTEN` registrations) belongs to it, as in PostgreSQL. |
| backend PID | A positive session number, unique within the cluster. `SPQbackendPID`, `pg_backend_pid()` and the `be_pid` of notifications agree. It is not an operating-system process. |
| socket | A descriptor that only signals readiness (section 5.3). No protocol bytes flow through it. |

Data lives in memory only. Two connections that name the same cluster and database see
the same data; that is how connection pools work against libspq.

## 2. Names

Every public identifier of libpq keeps its spelling and gains a prefix, so a program
can include and link libpq and libspq together:

- An identifier that starts with `PQ`, `PG`, `pq` or `pg` gets an `S` in front:
  `PQexec` → `SPQexec`, `PGconn` → `SPGconn`, `PGresult` → `SPGresult`,
  `PGRES_TUPLES_OK` → `SPGRES_TUPLES_OK`, `PG_DIAG_SQLSTATE` → `SPG_DIAG_SQLSTATE`,
  `PQTRANS_IDLE` → `SPQTRANS_IDLE`, `pg_encoding_to_char` → `spg_encoding_to_char`.
  The added letter takes the case of the letter it precedes (`pgNotify` → `spgNotify`),
  and a struct tag that starts with an underscore keeps it (`_SPQconninfoOption`).
- Any other identifier gets `SPQ` (`SPQ_` for upper-case constants): `Oid` → `SPQOid`,
  `InvalidOid` → `SPQInvalidOid`, `ConnStatusType` → `SPQConnStatusType`,
  `ExecStatusType` → `SPQExecStatusType`, `CONNECTION_OK` → `SPQ_CONNECTION_OK`.
- Functions that libpq does not have live in `libspq-ext.h` and start with `SPQX`.

Enumerator values, struct layouts and function signatures are the same as in libpq 18.
All enumerators exist, including ones libspq never returns (for example
`SPGRES_PIPELINE_SYNC`), so numeric values line up. Strings that libpq returns as data
are unchanged too: `SPQresStatus(SPGRES_TUPLES_OK)` is `"PGRES_TUPLES_OK"`.

Headers:

| Header | Content |
|---|---|
| `libspq-fe.h` | The libpq subset: types, constants, functions. |
| `libspq-ext.h` | `SPQX` extensions (section 6). |
| `libspq-compat.h` | Optional. Maps the original libpq names onto the libspq names with macros, so source written for libpq compiles against libspq. Never include it together with `libpq-fe.h`. |

**How to read the PostgreSQL manual for libspq.** A function listed in section 3 behaves
as the [PostgreSQL 18 libpq chapter](https://www.postgresql.org/docs/18/libpq.html)
describes for the function of the same name without the `S`, except where section 5
says otherwise. A function not listed in section 3 does not exist: the linker reports
it. libspq has no stubs that pretend to work.

## 3. Functions

| Family | Functions | Manual |
|---|---|---|
| Connect | `SPQconnectdb`, `SPQconnectdbParams`, `SPQconnectStart`, `SPQconnectStartParams`, `SPQconnectPoll`, `SPQfinish`, `SPQreset`, `SPQping`, `SPQpingParams` | [libpq-connect](https://www.postgresql.org/docs/18/libpq-connect.html) |
| Conninfo | `SPQconninfoParse`, `SPQconndefaults`, `SPQconninfo`, `SPQconninfoFree` | [libpq-connect](https://www.postgresql.org/docs/18/libpq-connect.html) |
| Status | `SPQstatus`, `SPQtransactionStatus`, `SPQerrorMessage`, `SPQdb`, `SPQuser`, `SPQpass`, `SPQhost`, `SPQport`, `SPQoptions`, `SPQparameterStatus`, `SPQprotocolVersion`, `SPQserverVersion`, `SPQbackendPID`, `SPQsocket`, `SPQclientEncoding`, `SPQsetClientEncoding`, `SPQlibVersion`, `SPQisthreadsafe`, `spg_encoding_to_char`, `spg_char_to_encoding` | [libpq-status](https://www.postgresql.org/docs/18/libpq-status.html), [libpq-control](https://www.postgresql.org/docs/18/libpq-control.html), [libpq-misc](https://www.postgresql.org/docs/18/libpq-misc.html) |
| Nonblocking execution | `SPQsendQuery`, `SPQsendQueryParams`, `SPQsendPrepare`, `SPQsendQueryPrepared`, `SPQsendDescribePrepared`, `SPQsendDescribePortal`, `SPQsendClosePrepared`, `SPQsendClosePortal`, `SPQgetResult`, `SPQconsumeInput`, `SPQisBusy`, `SPQflush`, `SPQsetnonblocking`, `SPQisnonblocking`, `SPQsocketPoll`, `SPQgetCurrentTimeUSec` | [libpq-async](https://www.postgresql.org/docs/18/libpq-async.html) |
| Row-by-row retrieval | `SPQsetSingleRowMode`, `SPQsetChunkedRowsMode` | [libpq-single-row-mode](https://www.postgresql.org/docs/18/libpq-single-row-mode.html) |
| Blocking execution | `SPQexec`, `SPQexecParams`, `SPQprepare`, `SPQexecPrepared`, `SPQdescribePrepared`, `SPQdescribePortal`, `SPQclosePrepared`, `SPQclosePortal` | [libpq-exec](https://www.postgresql.org/docs/18/libpq-exec.html) |
| Results | `SPQresultStatus`, `SPQresStatus`, `SPQntuples`, `SPQnfields`, `SPQbinaryTuples`, `SPQfname`, `SPQfnumber`, `SPQftable`, `SPQftablecol`, `SPQfformat`, `SPQftype`, `SPQfsize`, `SPQfmod`, `SPQgetvalue`, `SPQgetlength`, `SPQgetisnull`, `SPQnparams`, `SPQparamtype`, `SPQcmdStatus`, `SPQcmdTuples`, `SPQoidValue`, `SPQresultMemorySize`, `SPQclear`, `SPQfreemem` | [libpq-exec](https://www.postgresql.org/docs/18/libpq-exec.html), [libpq-misc](https://www.postgresql.org/docs/18/libpq-misc.html) |
| Errors and notices | `SPQresultErrorMessage`, `SPQresultErrorField`, `SPQresultVerboseErrorMessage`, `SPQsetErrorVerbosity`, `SPQsetErrorContextVisibility`, `SPQsetNoticeReceiver`, `SPQsetNoticeProcessor` | [libpq-exec](https://www.postgresql.org/docs/18/libpq-exec.html), [libpq-notice-processing](https://www.postgresql.org/docs/18/libpq-notice-processing.html) |
| Escaping | `SPQescapeLiteral`, `SPQescapeIdentifier`, `SPQescapeStringConn`, `SPQescapeString`, `SPQescapeByteaConn`, `SPQescapeBytea`, `SPQunescapeBytea` | [libpq-exec, escaping](https://www.postgresql.org/docs/18/libpq-exec.html#LIBPQ-EXEC-ESCAPE-STRING) |
| Cancel | `SPQgetCancel`, `SPQcancel`, `SPQfreeCancel` | [libpq-cancel](https://www.postgresql.org/docs/18/libpq-cancel.html) |
| COPY | `SPQputCopyData`, `SPQputCopyEnd`, `SPQgetCopyData` | [libpq-copy](https://www.postgresql.org/docs/18/libpq-copy.html) |
| Notifications | `SPQnotifies` | [libpq-notify](https://www.postgresql.org/docs/18/libpq-notify.html) |
| Extensions | `SPQXversion`, `SPQXclusterDrop`, `SPQXreset`, `SPQXdump`, `SPQXrestore` | section 6 |

Types and constants that go with these functions are all present: `SPGconn`,
`SPGresult`, `SPGcancel`, `SPGnotify`, `SPQconninfoOption`, `SPQnoticeReceiver`,
`SPQnoticeProcessor`, `spg_usec_time_t`, the status and polling enums, `SPGVerbosity`,
`SPGContextVisibility`, `SPGPing`, the `SPG_DIAG_*` field codes, `spg_int64`,
`SPQ_OID_MAX`, `SPQatooid`, `SPQ_QUERY_PARAM_MAX_LIMIT` and the `SPQfreeNotify` macro.
`libspq-compat.h` defines the libpq feature macros only for features libspq has
(`LIBPQ_HAS_CHUNK_MODE`, `LIBPQ_HAS_CLOSE_PREPARED`, `LIBPQ_HAS_SOCKET_POLL`).

### Absent on purpose

| libpq feature | What a binding does instead |
|---|---|
| Pipeline mode (`PQenterPipelineMode` …) | Send commands one after another. There are no round trips to save in-process. |
| Large-object client functions (`lo_*`) and `PQfn` | Call the SQL functions (`lo_create`, `lo_open`, `loread`, `lowrite`, `lo_lseek64`, `lo_get`, `lo_put` …) with `SPQexecParams`. |
| `PGcancelConn` family, `PQrequestCancel` | `SPQgetCancel` + `SPQcancel`. |
| `PQsetdbLogin`, `PQresetStart`, `PQresetPoll` | `SPQconnectdbParams`, `SPQreset`. |
| `PQtrace`, `PQprint`, events API, result construction (`PQmakeEmptyPGresult`, `PQsetvalue`, `PQcopyResult`) | Not needed by a binding. |
| TLS, GSS, OAuth, password functions (`PQsslInUse`, `PQencryptPasswordConn`, `PQconnectionUsedPassword` …) | A binding reports "no TLS, no password" itself. |
| Legacy COPY (`PQgetline`, `PQputline`, `PQendcopy`), `PQExpBuffer`, `PQmblen`, `PQdsplen` | Use the COPY functions above. |
| `PG*` environment variables, service files, password files | Pass everything in the conninfo. |

## 4. Typical call sequences

Blocking, one statement:

```c
SPGconn *conn = SPQconnectdb("host=t1 dbname=app user=alice");
if (SPQstatus(conn) != SPQ_CONNECTION_OK) { /* SPQerrorMessage(conn) */ }
SPGresult *res = SPQexecParams(conn, "SELECT $1::int4 + 1", 1, NULL,
                               (const char *const[]){"41"}, NULL, NULL, 0);
if (SPQresultStatus(res) == SPGRES_TUPLES_OK) puts(SPQgetvalue(res, 0, 0));
SPQclear(res);
SPQfinish(conn);
```

Every result of a multi-statement string (the form a binding needs for `nextset()`,
`getMoreResults()` and similar):

```c
SPQsendQuery(conn, "SELECT 1; SELECT 2");
for (SPGresult *res; (res = SPQgetResult(conn)) != NULL; SPQclear(res)) { /* one per statement */ }
```

Event-loop style: `SPQsendQuery…`, then wait until `SPQsocket(conn)` is readable, call
`SPQconsumeInput`, and while `SPQisBusy` returns 0 take results with `SPQgetResult`
until it returns `NULL`. Notifications are collected with `SPQnotifies` after every
`SPQconsumeInput`.

## 5. Behavior specific to libspq

### 5.1 Connection strings

- Both forms are accepted: `keyword=value …` and `postgresql://…` URIs. Every keyword
  that libpq 18 knows is accepted; an unknown keyword fails as in libpq.
- Keywords with an effect:

  | Keyword | Effect |
  |---|---|
  | `host`, `hostaddr`, `port` | Name the cluster. The cluster name is `host` when non-empty, else `hostaddr`, else `localhost`; of a comma-separated list only the first entry is used. `port` defaults to `5432`. Names are compared byte for byte; nothing is resolved. |
  | `dbname` | Logical database. Defaults to the user name. A `dbname` that is itself a conninfo string or URI is expanded as in libpq when `expand_dbname` is set. |
  | `user` | Role. Defaults to `postgres`, never to the operating-system user. |
  | `password` | Stored and returned by `SPQpass`; never checked. |
  | `options` | `-c name=value` session defaults, as in PostgreSQL. |
  | `application_name`, `fallback_application_name`, `client_encoding` | Session defaults. `client_encoding=auto` means `UTF8`. |

- Every other keyword (`sslmode`, `connect_timeout`, `target_session_attrs`,
  `gssencmode`, `channel_binding`, `keepalives…`, …) is validated for syntax only and
  otherwise ignored.
- Environment variables, service files and password files are never read. The same
  conninfo gives the same connection on every machine.
- A connection attempt always returns an `SPGconn` unless memory is exhausted. A failed
  attempt has status `SPQ_CONNECTION_BAD` and a message in `SPQerrorMessage`; the
  object still has to be released with `SPQfinish`. A malformed conninfo gives libpq's
  bare parse message. A rejection by the engine (a bad `options` value or session
  default, a role without `LOGIN`) gives
  `connection to server at "<host>", port <port> failed: FATAL:  <message>`.
- `SPQconnectStart` completes the connection before it returns. `SPQconnectPoll` then
  reports `SPGRES_POLLING_OK` (or `SPGRES_POLLING_FAILED`). Callers that start by
  waiting for write-readiness, as the manual prescribes, work unchanged because the
  descriptor is always writable.
- `SPQping` reports `SPQPING_OK` for any well-formed conninfo and
  `SPQPING_NO_ATTEMPT` for a malformed one. It creates nothing.

### 5.2 Session facts

- `SPQprotocolVersion` is 3. `SPQserverVersion` is the engine's `server_version_num`
  (currently 180000). `SPQlibVersion` is 180006: the libpq release whose behavior this
  document refers to. Do not derive feature availability from it; section 3 is the
  feature list.
- `SPQparameterStatus` knows the parameters the engine reports: `server_version`,
  `server_encoding`, `client_encoding`, `application_name`, `DateStyle`,
  `IntervalStyle`, `TimeZone`, `integer_datetimes`, `standard_conforming_strings`,
  `search_path`, `session_authorization`, `is_superuser`,
  `default_transaction_read_only`, `in_hot_standby`, `scram_iterations`. Values are
  refreshed when a command completes, as in libpq.
- Built-in types have PostgreSQL's OIDs, so OID tables compiled into drivers are valid.
- Text values, names, command tags and messages are in the session's client encoding.
  The default is `UTF8`; `SPQsetClientEncoding` and `SET client_encoding` work.

### 5.3 The descriptor returned by `SPQsocket`

A binding may hand this descriptor to `select`, `poll`, `epoll`, libuv, a Ruby `IO`, or
an asyncio reader. It must never read from it, write to it, or close it.

- It is valid (≥ 0) from the moment `SPQconnectStart` or `SPQconnectdb` returns a
  connection with status other than `SPQ_CONNECTION_BAD`, and it is the same number
  for the whole life of the connection, including across `SPQreset`.
- It is always writable.
- It is readable, level-triggered, exactly while `SPQconsumeInput` would make
  progress: a result, a notice, a notification, COPY data, or the end of the session
  is waiting. Spurious readiness may occur; a missed wake-up does not.
- `SPQfinish` closes it. Remove it from the event loop before calling `SPQfinish`.
- It is a local stream socket with close-on-exec set.

### 5.4 Execution

- All `SPQsend…` functions queue the command and return at once. `SPQflush` always
  returns 0. `SPQsetnonblocking` is accepted and remembered; it changes nothing else.
- `SPQgetResult` blocks until the next result is complete. `SPQisBusy` tells whether
  it would block. The blocking functions (`SPQexec` …) are the same machinery driven
  to completion inside the library.
- The waiting happens in C, not inside the engine runtime, so a thread blocked in
  `SPQexec` or `SPQgetResult` is an ordinary thread in `poll()`. The one exception is
  `SPQputCopyData`, which may wait briefly inside the runtime for queue space.
- `SPQexec` returns the last result of a multi-statement string and stops at the first
  error, as in libpq. Use `SPQsendQuery` + `SPQgetResult` to see every result.
- A command with parameters accepts one statement and at most 65535 parameters; text
  and binary formats work for parameters and results.
- libpq cannot fetch a portal in slices. To bound memory use
  `SPQsetChunkedRowsMode` / `SPQsetSingleRowMode` (the engine pauses while results
  are not collected), or SQL cursors (`DECLARE … CURSOR`, `FETCH n`), which also
  allow other statements in between.
- `SPQputCopyData` never returns 0. When the engine is slower than the caller it
  blocks briefly instead, in blocking and nonblocking mode alike. `SPQgetCopyData`
  with `async` set returns 0 when no row is ready yet.
- `SPQfinish` never waits for a running statement: it cancels it, rolls the session
  back and returns.
- `SPQreset` ends the session and opens a new one with the same parameters, in the
  same cluster. Committed data is unaffected.

### 5.5 Cancel

`SPQcancel` may be called from any thread and from a signal handler. It never blocks.
It cancels the command that is running when the request is processed; when nothing is
running it has no effect. A cancelled command ends with SQLSTATE `57014`. An
`SPGcancel` object stays usable after `SPQfinish` of its connection (the call then does
nothing and reports success) and must be released with `SPQfreeCancel`.

### 5.6 Notices and notifications

- The notice receiver and processor run on the thread that is inside an `SPQ…` call
  for that connection, never on another thread. They must return normally (no
  `longjmp`, no exception passing through) and must not call libspq functions for the
  same connection.
- Without a receiver, notices are written to `stderr`, as in libpq. A notice raised
  while the session starts is delivered inside the connect call through these default
  hooks, because a receiver cannot be installed before the connection exists.
- Notifications become visible to `SPQnotifies` after `SPQconsumeInput` (or any
  blocking call). To wait for one, wait for the descriptor to become readable.
  `SPQsocketPoll(SPQsocket(conn), 1, 0, end_time)` is such a wait for callers that
  cannot use `poll` themselves.

### 5.7 Errors

- Errors raised by the engine arrive as `SPGRES_FATAL_ERROR` results with every
  `SPG_DIAG_*` field the engine provides. `SPQresultErrorMessage` formats them as
  libpq does (`ERROR:  …`, `LINE n:`, `DETAIL:`, `HINT:`, `CONTEXT:`), according to
  the verbosity and context visibility of the connection.
- Messages that libpq produces on the client side keep libpq's wording, because
  frameworks match on them: `no connection to the server`, `another command is
  already in progress`, `server closed the connection unexpectedly …`, and the
  others listed in `docs/design/LIBSPQ-CLIENT.md`.
- When the engine ends a session (`pg_terminate_backend`, a session timeout, cluster
  drop), the pending command returns a `FATAL` result, the connection status becomes
  `SPQ_CONNECTION_BAD`, and the descriptor becomes readable.
- An internal failure of the engine is reported as SQLSTATE `XX000`; it does not
  terminate the host process.

### 5.8 Memory and lifetime

| Object | Owner and release |
|---|---|
| `SPGconn *` | Caller; `SPQfinish`. Strings returned by connection accessors (`SPQerrorMessage`, `SPQdb`, `SPQparameterStatus` …) stay valid until the next call on that connection. |
| `SPGresult *` | Caller; `SPQclear`. A result is immutable, may be read from any thread, and stays valid after `SPQfinish` of its connection. Pointers from result accessors live as long as the result. |
| `SPGcancel *` | Caller; `SPQfreeCancel`. |
| `SPGnotify *`, escaped strings, `SPQgetCopyData` buffers, `SPQresultVerboseErrorMessage`, `SPQXdump` buffers | Caller; `SPQfreemem`. |
| `SPQconninfoOption *` | Caller; `SPQconninfoFree`. |

`SPQgetvalue` returns a NUL-terminated buffer for text and binary values alike;
`SPQgetlength` gives the length without the terminator.

## 6. Extensions (`libspq-ext.h`)

```c
const char *SPQXversion(void);
int SPQXclusterDrop(const char *host, const char *port, char *errbuf, int errbufsize);
int SPQXreset(SPGconn *conn);
int SPQXdump(SPGconn *conn, char **buffer, size_t *length);
int SPQXrestore(SPGconn *conn, const char *buffer, size_t length);
```

The `int` functions return 1 on success and 0 on failure; a failure of a function that
takes a connection leaves its message in `SPQerrorMessage`.

| Function | Contract |
|---|---|
| `SPQXversion()` | The version of libspq itself, as a static string. |
| `SPQXclusterDrop(host, port, …)` | Forget the named cluster. Fails while it still has open connections. The next connection to that name gets a new, empty cluster. This is the way to give each test a clean engine and to release memory. |
| `SPQXreset(conn)` | Remove all persistent objects and data of the connected database through the engine's Reset, keeping the cluster and the connection. Roles and other databases of the cluster are untouched. Temporary tables and prepared statements of open sessions survive. Another session that is inside a transaction gets an error until it rolls back. |
| `SPQXdump(conn, …)` | Serialize the connected database to a caller-owned buffer (the engine's native dump). |
| `SPQXrestore(conn, …)` | Replace the connected database with the content of a dump. |

Two further extensions are planned with low priority and are not required by a first
binding: executing a portal with a row limit (`SPQXsendBindPortal`,
`SPQXsendExecutePortal`, their blocking forms and `SPQXresultSuspended`), and a queue
from which notices can be pulled instead of receiving them through a callback
(`SPQXsetNoticeQueue`, `SPQXnextNotice`).

## 7. Rules for binding authors

These follow from running a Go runtime inside the host process. They are requirements,
not advice.

1. **Linux with glibc only** (amd64, arm64). The library does not load on musl.
2. **One copy per process.** Load one `libspq.so` (by absolute path). It is never
   unloaded; `dlclose` has no effect. Prefer the shared library over linking the
   static archive into an extension that the host may unmap.
3. **Never use libspq in a forked child.** The runtime starts when the library is
   loaded and does not survive `fork()`. Load the library lazily (at first connect,
   not at import) and connect after forking. In a child of a process that had loaded
   it, connection attempts fail with `libspq cannot be used in a process forked after
   the library was loaded`, `SPQcancel` does nothing, and `SPQfinish` / `SPQclear`
   only release memory; nothing hangs. Engine data is per process in any case.
4. **Load with signals blocked.** Block all signals on the loading thread around the
   `dlopen`, then restore the mask, so that process-directed signals are not delivered
   to runtime threads. Any signal handler the host installs afterwards should use
   `SA_ONSTACK`. libspq installs no handlers of its own beyond what the Go runtime
   needs for synchronous signals.
5. **Threads.** One thread at a time may use a given connection; different connections
   may be used concurrently. A connection has no thread affinity: it may move between
   threads between calls. Results are read-only and shareable. `SPQcancel` is the only
   function that may run concurrently with another call on the same connection.
6. **Blocking calls and interrupts.** A blocked `SPQexec` cannot be interrupted by a
   host signal. Wherever the host has interrupts, timeouts or an event loop, use the
   nonblocking functions and wait on the descriptor yourself; on interrupt call
   `SPQcancel` and keep collecting results.
7. **Callbacks** follow section 5.6. Wrap them so that host exceptions cannot unwind
   through libspq.
8. **Release before exit is optional.** Open connections at process exit are harmless.

## 8. Notes per language

These are starting points for the binding designs, not decisions.

**Python.** The natural shape is an implementation of psycopg 3's `pq` abstraction
(`psycopg/pq/abc.py`) over libspq with `ctypes` or Cython, which lets psycopg,
SQLAlchemy and Django run unchanged in sync and asyncio mode. psycopg drives only the
nonblocking functions and waits on the descriptor. `ctypes` releases the GIL around
calls and re-acquires it for callbacks. Fork-based runners (`multiprocessing` fork
start method, `pytest-forked`, preloading servers) need rule 3; `pytest-xdist` workers
are separate processes and each gets its own engine. psycopg's optional pipeline mode
must be reported as unsupported.

**Ruby.** A C extension with the `PG` namespace of the `pg` gem lets ActiveRecord and
Sequel work. Use the `send…` functions with `rb_io_wait` on the descriptor so the
fiber scheduler works; where a blocking call is made, release the GVL and register an
unblock function that calls `SPQcancel`. Wrap notice callbacks in `rb_protect`.
Process-based parallel test runners need rule 3; thread-based ones do not.

**Node.js.** An N-API addon that drives the connection from the JavaScript thread with
a `uv_poll_t` on the descriptor needs no worker threads and delivers notices on the
JavaScript thread. Close the poll handle before `SPQfinish`. A context-aware addon can
be loaded by several worker threads; they share one runtime and one set of clusters.

**Java.** A JDBC driver calls the blocking functions through JNI (Java 8–21) or the
FFM API (22+). It needs `SPQsendQuery` + `SPQgetResult` for multiple results,
`SPQdescribePrepared` for metadata, `SPQcancel` from another thread for
`Statement.cancel` and timeouts, and `SPQsocketPoll` for `getNotifications(timeout)`.
`fetchSize` is served by SQL cursors inside a transaction. A blocking native call pins
a virtual thread's carrier. Load the JNI shim so that it links `libspq.so`
dynamically.

Unmodified drivers that implement the wire protocol themselves (pgjdbc, node `pg`)
cannot use libspq. They connect to `pgserver`; see [pgserver](../pgserver/README.md).

All four ecosystems issue fixed catalog queries at connect (type lookups in `pg_type`,
`pg_range`, `pg_namespace`, `current_schemas`, cursor statements). Those statements
are covered by reference tests so the engine answers them as PostgreSQL does.

## 9. Building and testing

```sh
make libspq         # build/libspq/lib/libspq.so, libspq.a and build/libspq/include/*.h
make libspq-check   # build the library and run its C++ tests
```

Both need a C and C++ compiler and cgo; neither is part of `make quickcheck`. The Go
packages underneath (`internal/pgsession`, `bindings/libspq/spq`) are ordinary
packages and are covered by `make quickcheck`.

Link with `-I build/libspq/include -L build/libspq/lib -lspq`. The shared library
(soname `libspq.so`) exports only `SPQ…` and `spg_…` symbols, so it can be loaded next
to `libpq.so`. It contains the whole engine and is about 94 MB. The static archive also
exposes runtime support symbols to the linker; prefer the shared library.

The C++ tests live in `bindings/libspq/tests` and use the vendored single-header
doctest. They check the behavior of the C API (states, ownership, ordering, error
reporting), not SQL features; SQL behavior is covered by the reference tests.
