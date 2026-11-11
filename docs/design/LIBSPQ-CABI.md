# LIBSPQ-CABI: the libspq C ABI

Design for catalog part LIBSPQ-DESIGN-CABI. It specifies everything between the C
programmer and package `bindings/libspq/spq`: the public headers, the cgo package
`bindings/libspq` (package `main`), its C code, the build, and the C++ tests.
[docs/LIBSPQ.md](../LIBSPQ.md) is the consumer contract;
[LIBSPQ-CLIENT.md](LIBSPQ-CLIENT.md) defines the Go package this layer calls. Where
this document says "CLIENT n.n" it means that section of LIBSPQ-CLIENT.md.

Names, values and signatures of the libpq API were taken from the PostgreSQL 18.6
public headers (`libpq-fe.h`, `postgres_ext.h` at tag `REL_18_6`); behavior for NULL
and out-of-range arguments was learned from the documentation and by reading libpq at
that tag. No code and no comment is copied. Message texts that programs can observe
are quoted exactly.

Statements marked **(measured)** were observed in scratch builds on the tree at commit
`c662029a11` with go 1.27.1 and gcc 13.3 (section 11).

## 1. Purpose, layering, layout

```
application / language binding
        │ libspq-fe.h, libspq-ext.h        SPQ..., spg_... symbols only
C files of bindings/libspq                 every public function; results; waiting;
        │ spqgo_* (hidden)                  fork guard; cancel; callbacks
Go files of bindings/libspq (package main) marshalling only
        │ exported API of CLIENT section 2
package bindings/libspq/spq                all libpq behavior
```

### 1.1 Fixed points

- **F1 Every public function is defined in C.** Go exports are named `spqgo_*`. A C
  file reaches them through the cgo-generated `_cgo_export.h`; their parameters have
  no `const`, so C casts at the call. A linker version script hides everything except
  `SPQ*` and `spg_*` in the shared library.
- **F2 Results are immutable C memory** built once from a Go `*spq.Result`. Result
  accessors, `SPQclear` and `SPQfreemem` never enter Go. A result survives
  `SPQfinish` and may be read from any thread.
- **F3 The connection object is a C struct** holding the `runtime/cgo.Handle` of the
  `*spq.Conn`, the readiness and cancel descriptors, the notice hooks, the queue of
  notices waiting for delivery, and C-owned copies of the strings connection
  accessors return.
- **F4 Blocking functions wait in C.** Every `spq` method is nonblocking and reports
  "would block" (CLIENT R2). The shim loop is: call; when it reports would-block,
  `poll()` the readiness descriptor (retry on `EINTR`) and call again. The rules of
  `PQexec` (discard pending results, last result wins, stop at COPY) are implemented
  in Go by `ExecStart` / `ExecFinish` (CLIENT 7.2); the shim only drives them. This
  replaces the sentence of the part record that allowed specifying those rules in C.
  The one call that may wait inside Go is `PutCopyData` (CLIENT 6.3).
- **F5 `SPQcancel` is pure C**: one `send()` on a descriptor the `SPGcancel` object
  owns. It is async-signal-safe and never enters Go.
- **F6 Fork guard.** A constructor records that the library was loaded and registers a
  `pthread_atfork` child handler. Every function that would enter Go first checks the
  flag that handler sets. In a forked child nothing enters Go (section 7.1).
- **F7 Notice callbacks run from C** on the calling thread after the Go call that
  consumed the notices has returned: Go hands them over as result blocks queued on the
  connection object; C pops, calls the receiver and frees. No Go frame is on the stack
  and no lock is held during a callback.
- **F8 No panic crosses the boundary.** Every `spqgo_*` function has a deferred
  `recover()` that turns a panic into the function's failure value. (`spq` already
  recovers internally, CLIENT R5; this is the second line.)
- **F9 Build tag.** Every Go file of package `main` starts with
  `//go:build libspq && cgo && linux`. Without the tag the directory has no Go files,
  so `go build ./...`, `go vet ./...` and `go test ./...` skip it and never need a C
  compiler **(measured)**. C files carry no build constraint; they are only compiled
  when the Go files are **(measured)**.
- **F10 One C file, one Go file and one doctest file per function family**, each owned
  by one part (section 10). The private header `spq_int.h` is written once, completely
  (section 4.1); later parts implement what it declares and never edit it.

### 1.2 Files

```
bindings/libspq/
  README.md                 contract of the directory (build, layout, rules)
  exports.map               linker version script (section 8.2)
  main.go                   package clause, #cgo flags, func main
  version.c                 SPQlibVersion, SPQisthreadsafe, SPQXversion
  include/libspq-fe.h       the libpq subset (section 3.1)
  include/libspq-ext.h      SPQX extensions (section 3.2)
  include/libspq-compat.h   libpq names as macros (section 3.3)
  spq_int.h                 private structs and helper prototypes (section 4.1)
  guard.c                   fork guard
  conn.c        conn.go     connect, finish, status accessors, string cache
  notices.c     notices.go  notice queue, delivery, default hooks, internal notices
  result.c      result.go   result block, builders, core accessors, SPQclear
  query.c       query.go    SPQsendQuery, SPQgetResult, the wait helper
  accessors.c               remaining result accessors, SPQresStatus, SPQfreemem
  async.c       async.go    SPQconsumeInput, SPQisBusy, SPQflush, nonblocking flag
  exec.c        exec.go     SPQexec and the exec start/finish helpers
  extended.c    extended.go parameterized, prepared, describe, close (send + blocking)
  errors.c      errors.go   error fields, verbose message, verbosity, notice hooks
  escape.c      escape.go   escaping
  conninfo.c    conninfo.go SPQconninfoParse, SPQconndefaults, SPQconninfo, ...Free
  cancel.c                  SPQgetCancel, SPQcancel, SPQfreeCancel
  copy.c        copy.go     COPY
  notify.c      notify.go   SPQnotifies, SPQsocketPoll, SPQgetCurrentTimeUSec
  rowmodes.c    rowmodes.go single-row and chunked modes
  connect_async.c connect_async.go  params variants, start/poll, reset, ping
  encoding.c    encoding.go client encoding
  control.c     control.go  SPQXclusterDrop, SPQXreset
  dump.c        dump.go     SPQXdump, SPQXrestore
  tests/
    third_party/doctest.h, third_party/LICENSE.doctest.txt
    main.cpp                doctest main
    spqtest.h               helpers (section 9.1)
    test_<family>.cpp       one per family (section 9.2)
    syntax/*.c              C11 compile-only checks of the headers
    coexist/pqstub.c, coexist/coexist.c   libpq-named symbols next to libspq
    check_symbols.sh        export-list check
```

Artifacts of `make libspq`, all under the git-ignored `build/libspq/`:
`lib/libspq.so`, `lib/libspq.a`, `include/libspq-fe.h`, `include/libspq-ext.h`,
`include/libspq-compat.h`. `make libspq-check` adds `obj/` and the test binary
`spq_tests`.
## 2. Rename table

The rule of docs/LIBSPQ.md section 2, applied mechanically to every identifier that
`libpq-fe.h` and `postgres_ext.h` of PostgreSQL 18.6 declare:

1. An identifier that starts with `PQ` or `PG` gets `S` in front; one that starts with
   `pq` or `pg` gets `s` in front (the added letter has the case of the first letter).
2. Any other identifier gets `SPQ` in front, or `SPQ_` when it consists only of
   upper-case letters, digits and underscores.
3. A struct tag that starts with an underscore keeps the underscore:
   `_PQconninfoOption` becomes `_SPQconninfoOption`.

The table was generated by a script from the two headers (326 identifiers: 200 kept,
126 dropped) and checked: no two kept identifiers map to the same new name, no new
name equals a libpq identifier, none is a C or C++ keyword **(measured)**. It is the
source for `libspq-fe.h` and `libspq-compat.h`.

### 2.1 Types

| libpq | libspq | Kind |
|---|---|---|
| `ConnStatusType` | `SPQConnStatusType` | enum type |
| `PostgresPollingStatusType` | `SPQPostgresPollingStatusType` | enum type |
| `ExecStatusType` | `SPQExecStatusType` | enum type |
| `PGTransactionStatusType` | `SPGTransactionStatusType` | enum type |
| `PGVerbosity` | `SPGVerbosity` | enum type |
| `PGContextVisibility` | `SPGContextVisibility` | enum type |
| `PGPing` | `SPGPing` | enum type |
| `PGconn` | `SPGconn` | opaque type |
| `pg_conn` | `spg_conn` | struct tag |
| `PGresult` | `SPGresult` | opaque type |
| `pg_result` | `spg_result` | struct tag |
| `PGcancel` | `SPGcancel` | opaque type |
| `pg_cancel` | `spg_cancel` | struct tag |
| `PGnotify` | `SPGnotify` | struct type |
| `pgNotify` | `spgNotify` | struct tag |
| `PQconninfoOption` | `SPQconninfoOption` | struct type |
| `_PQconninfoOption` | `_SPQconninfoOption` | struct tag |
| `PQnoticeReceiver` | `SPQnoticeReceiver` | function-pointer type |
| `PQnoticeProcessor` | `SPQnoticeProcessor` | function-pointer type |
| `pg_usec_time_t` | `spg_usec_time_t` | typedef of `int64_t` |
| `Oid` | `SPQOid` | typedef of `unsigned int` |
| `pg_int64` | `spg_int64` | typedef of `int64_t` |

Struct members keep their libpq names (`relname`, `be_pid`, `extra`, `next`;
`keyword`, `envvar`, `compiled`, `val`, `label`, `dispchar`, `dispsize`).

### 2.2 Enumerators

Values are libpq's; the header part asserts each one.

| Enum | Enumerators |
|---|---|
| `SPQConnStatusType` | `SPQ_CONNECTION_OK` (0), `SPQ_CONNECTION_BAD` (1), `SPQ_CONNECTION_STARTED` (2), `SPQ_CONNECTION_MADE` (3), `SPQ_CONNECTION_AWAITING_RESPONSE` (4), `SPQ_CONNECTION_AUTH_OK` (5), `SPQ_CONNECTION_SETENV` (6), `SPQ_CONNECTION_SSL_STARTUP` (7), `SPQ_CONNECTION_NEEDED` (8), `SPQ_CONNECTION_CHECK_WRITABLE` (9), `SPQ_CONNECTION_CONSUME` (10), `SPQ_CONNECTION_GSS_STARTUP` (11), `SPQ_CONNECTION_CHECK_TARGET` (12), `SPQ_CONNECTION_CHECK_STANDBY` (13), `SPQ_CONNECTION_ALLOCATED` (14), `SPQ_CONNECTION_AUTHENTICATING` (15) |
| `SPQPostgresPollingStatusType` | `SPGRES_POLLING_FAILED` (0), `SPGRES_POLLING_READING` (1), `SPGRES_POLLING_WRITING` (2), `SPGRES_POLLING_OK` (3), `SPGRES_POLLING_ACTIVE` (4) |
| `SPQExecStatusType` | `SPGRES_EMPTY_QUERY` (0), `SPGRES_COMMAND_OK` (1), `SPGRES_TUPLES_OK` (2), `SPGRES_COPY_OUT` (3), `SPGRES_COPY_IN` (4), `SPGRES_BAD_RESPONSE` (5), `SPGRES_NONFATAL_ERROR` (6), `SPGRES_FATAL_ERROR` (7), `SPGRES_COPY_BOTH` (8), `SPGRES_SINGLE_TUPLE` (9), `SPGRES_PIPELINE_SYNC` (10), `SPGRES_PIPELINE_ABORTED` (11), `SPGRES_TUPLES_CHUNK` (12) |
| `SPGTransactionStatusType` | `SPQTRANS_IDLE` (0), `SPQTRANS_ACTIVE` (1), `SPQTRANS_INTRANS` (2), `SPQTRANS_INERROR` (3), `SPQTRANS_UNKNOWN` (4) |
| `SPGVerbosity` | `SPQERRORS_TERSE` (0), `SPQERRORS_DEFAULT` (1), `SPQERRORS_VERBOSE` (2), `SPQERRORS_SQLSTATE` (3) |
| `SPGContextVisibility` | `SPQSHOW_CONTEXT_NEVER` (0), `SPQSHOW_CONTEXT_ERRORS` (1), `SPQSHOW_CONTEXT_ALWAYS` (2) |
| `SPGPing` | `SPQPING_OK` (0), `SPQPING_REJECT` (1), `SPQPING_NO_RESPONSE` (2), `SPQPING_NO_ATTEMPT` (3) |

### 2.3 Macros

| libpq | libspq | Definition in libspq |
|---|---|---|
| `PQ_QUERY_PARAM_MAX_LIMIT` | `SPQ_QUERY_PARAM_MAX_LIMIT` | `65535` |
| `PQfreeNotify` | `SPQfreeNotify` | `(ptr) SPQfreemem(ptr)` |
| `InvalidOid` | `SPQInvalidOid` | `((SPQOid) 0)` in C, `(SPQOid(0))` in C++ |
| `OID_MAX` | `SPQ_OID_MAX` | `UINT_MAX` |
| `atooid` | `SPQatooid` | `(x) ((SPQOid) strtoul((x), NULL, 10))` |
| `PG_DIAG_SEVERITY` | `SPG_DIAG_SEVERITY` | `'S'` |
| `PG_DIAG_SEVERITY_NONLOCALIZED` | `SPG_DIAG_SEVERITY_NONLOCALIZED` | `'V'` |
| `PG_DIAG_SQLSTATE` | `SPG_DIAG_SQLSTATE` | `'C'` |
| `PG_DIAG_MESSAGE_PRIMARY` | `SPG_DIAG_MESSAGE_PRIMARY` | `'M'` |
| `PG_DIAG_MESSAGE_DETAIL` | `SPG_DIAG_MESSAGE_DETAIL` | `'D'` |
| `PG_DIAG_MESSAGE_HINT` | `SPG_DIAG_MESSAGE_HINT` | `'H'` |
| `PG_DIAG_STATEMENT_POSITION` | `SPG_DIAG_STATEMENT_POSITION` | `'P'` |
| `PG_DIAG_INTERNAL_POSITION` | `SPG_DIAG_INTERNAL_POSITION` | `'p'` |
| `PG_DIAG_INTERNAL_QUERY` | `SPG_DIAG_INTERNAL_QUERY` | `'q'` |
| `PG_DIAG_CONTEXT` | `SPG_DIAG_CONTEXT` | `'W'` |
| `PG_DIAG_SCHEMA_NAME` | `SPG_DIAG_SCHEMA_NAME` | `'s'` |
| `PG_DIAG_TABLE_NAME` | `SPG_DIAG_TABLE_NAME` | `'t'` |
| `PG_DIAG_COLUMN_NAME` | `SPG_DIAG_COLUMN_NAME` | `'c'` |
| `PG_DIAG_DATATYPE_NAME` | `SPG_DIAG_DATATYPE_NAME` | `'d'` |
| `PG_DIAG_CONSTRAINT_NAME` | `SPG_DIAG_CONSTRAINT_NAME` | `'n'` |
| `PG_DIAG_SOURCE_FILE` | `SPG_DIAG_SOURCE_FILE` | `'F'` |
| `PG_DIAG_SOURCE_LINE` | `SPG_DIAG_SOURCE_LINE` | `'L'` |
| `PG_DIAG_SOURCE_FUNCTION` | `SPG_DIAG_SOURCE_FUNCTION` | `'R'` |

### 2.4 Functions

The 104 kept functions are listed with their final prototypes in section 3.1. Each is
the libpq function of the same name without the leading `S` (`s` for the two `spg_`
functions).

### 2.5 Dropped identifiers

Dropped because the functions that use them are absent (docs/LIBSPQ.md, "Absent on
purpose"). Nothing in this list appears in a libspq header, with one exception: the
three feature macros named in section 3.3.

- enum type (2): `PGpipelineStatus`, `PGauthData`
- enumerator (5): `PQ_PIPELINE_OFF`, `PQ_PIPELINE_ON`, `PQ_PIPELINE_ABORTED`, `PQAUTHDATA_PROMPT_OAUTH_DEVICE`, `PQAUTHDATA_OAUTH_BEARER_TOKEN`
- opaque type (1): `PGcancelConn`
- struct tag (5): `pg_cancel_conn`, `_PQprintOpt`, `pgresAttDesc`, `_PGpromptOAuthDevice`, `PGoauthBearerRequest`
- struct type (5): `PQprintOpt`, `u`, `PGresAttDesc`, `PGpromptOAuthDevice`, `PGoauthBearerRequest`
- function-pointer type (3): `pgthreadlock_t`, `PQauthDataHook_type`, `PQsslKeyPassHook_OpenSSL_type`
- typedef (1): `pqbool`
- macro (20): `LIBPQ_HAS_PIPELINING`, `LIBPQ_HAS_TRACE_FLAGS`, `LIBPQ_HAS_SSL_LIBRARY_DETECTION`, `LIBPQ_HAS_ASYNC_CANCEL`, `LIBPQ_HAS_CHANGE_PASSWORD`, `LIBPQ_HAS_CHUNK_MODE`, `LIBPQ_HAS_CLOSE_PREPARED`, `LIBPQ_HAS_SEND_PIPELINE_SYNC`, `LIBPQ_HAS_SOCKET_POLL`, `LIBPQ_HAS_FULL_PROTOCOL_VERSION`, `LIBPQ_HAS_PROMPT_OAUTH_DEVICE`, `PG_COPYRES_ATTRS`, `PG_COPYRES_TUPLES`, `PG_COPYRES_EVENTS`, `PG_COPYRES_NOTICEHOOKS`, `PQsetdb`, `PQTRACE_SUPPRESS_TIMESTAMPS`, `PQTRACE_REGRESS_MODE`, `PQnoPasswordSupplied`, `PQ_SOCKTYPE`
- function (83): `PQsetdbLogin`, `PQresetStart`, `PQresetPoll`, `PQcancelCreate`, `PQcancelStart`, `PQcancelBlocking`, `PQcancelPoll`, `PQcancelStatus`, `PQcancelSocket`, `PQcancelErrorMessage`, `PQcancelReset`, `PQcancelFinish`, `PQrequestCancel`, `PQhostaddr`, `PQtty`, `PQfullProtocolVersion`, `PQpipelineStatus`, `PQconnectionNeedsPassword`, `PQconnectionUsedPassword`, `PQconnectionUsedGSSAPI`, `PQsslInUse`, `PQsslStruct`, `PQsslAttribute`, `PQsslAttributeNames`, `PQgetssl`, `PQinitSSL`, `PQinitOpenSSL`, `PQgssEncInUse`, `PQgetgssctx`, `PQregisterThreadLock`, `PQtrace`, `PQuntrace`, `PQsetTraceFlags`, `PQenterPipelineMode`, `PQexitPipelineMode`, `PQpipelineSync`, `PQsendFlushRequest`, `PQsendPipelineSync`, `PQgetline`, `PQputline`, `PQgetlineAsync`, `PQputnbytes`, `PQendcopy`, `PQfn`, `PQoidStatus`, `PQmakeEmptyPGresult`, `PQcopyResult`, `PQsetResultAttrs`, `PQresultAlloc`, `PQsetvalue`, `PQprint`, `PQdisplayTuples`, `PQprintTuples`, `lo_open`, `lo_close`, `lo_read`, `lo_write`, `lo_lseek`, `lo_lseek64`, `lo_creat`, `lo_create`, `lo_tell`, `lo_tell64`, `lo_truncate`, `lo_truncate64`, `lo_unlink`, `lo_import`, `lo_import_with_oid`, `lo_export`, `PQmblen`, `PQmblenBounded`, `PQdsplen`, `PQenv2encoding`, `PQencryptPassword`, `PQencryptPasswordConn`, `PQchangePassword`, `PQsetAuthDataHook`, `PQgetAuthDataHook`, `PQdefaultAuthDataHook`, `pg_valid_server_encoding_id`, `PQgetSSLKeyPassHook_OpenSSL`, `PQsetSSLKeyPassHook_OpenSSL`, `PQdefaultSSLKeyPassHook_OpenSSL`

`libpq-events.h` (the events API) has no counterpart at all.
## 3. Headers

All three headers are written from section 2 and the libpq documentation. They contain
short comments of our own and none of libpq's.

### 3.1 `libspq-fe.h`

Order of the file:

1. Include guard `LIBSPQ_FE_H`; `#include <limits.h>`, `<stddef.h>`, `<stdint.h>`,
   `<stdio.h>`, `<stdlib.h>` (`SPQ_OID_MAX` needs `UINT_MAX`, `SPQatooid` needs
   `strtoul`); `extern "C" {` under `#ifdef __cplusplus`.
2. The export macro:
   ```c
   #if defined(__GNUC__)
   #define SPQ_API __attribute__((visibility("default")))
   #else
   #define SPQ_API
   #endif
   ```
3. `typedef unsigned int SPQOid;`, `SPQInvalidOid` (both forms of 2.3), `SPQ_OID_MAX`,
   `SPQatooid`, `typedef int64_t spg_int64;`, `typedef int64_t spg_usec_time_t;`.
4. The 18 `SPG_DIAG_*` macros of 2.3 and `SPQ_QUERY_PARAM_MAX_LIMIT`.
5. The seven enums of 2.2, each `typedef enum { ... } Name;` with the enumerators in
   the order and with the values of 2.2. Write the first enumerator of each as
   `= 0`; the rest follow by position.
6. Opaque types: `typedef struct spg_conn SPGconn;`,
   `typedef struct spg_result SPGresult;`, `typedef struct spg_cancel SPGcancel;`.
7. Public structs, with libpq's member names, types and order:
   ```c
   typedef struct spgNotify
   {
   	char	   *relname;
   	int			be_pid;
   	char	   *extra;
   	struct spgNotify *next;
   } SPGnotify;

   typedef struct _SPQconninfoOption
   {
   	char	   *keyword;
   	char	   *envvar;
   	char	   *compiled;
   	char	   *val;
   	char	   *label;
   	char	   *dispchar;
   	int			dispsize;
   } SPQconninfoOption;
   ```
8. Callback types:
   ```c
   typedef void (*SPQnoticeReceiver) (void *arg, const SPGresult *res);
   typedef void (*SPQnoticeProcessor) (void *arg, const char *message);
   ```
9. The prototypes below, in this order, then `#define SPQfreeNotify(ptr) SPQfreemem(ptr)`.
10. Closing brace of `extern "C"`, `#endif`.

```c
/* Connect */
SPQ_API extern SPGconn *SPQconnectdb(const char *conninfo);
SPQ_API extern SPGconn *SPQconnectdbParams(const char *const *keywords, const char *const *values, int expand_dbname);
SPQ_API extern SPGconn *SPQconnectStart(const char *conninfo);
SPQ_API extern SPGconn *SPQconnectStartParams(const char *const *keywords, const char *const *values, int expand_dbname);
SPQ_API extern SPQPostgresPollingStatusType SPQconnectPoll(SPGconn *conn);
SPQ_API extern void SPQfinish(SPGconn *conn);
SPQ_API extern void SPQreset(SPGconn *conn);
SPQ_API extern SPGPing SPQping(const char *conninfo);
SPQ_API extern SPGPing SPQpingParams(const char *const *keywords, const char *const *values, int expand_dbname);

/* Connection options */
SPQ_API extern SPQconninfoOption *SPQconninfoParse(const char *conninfo, char **errmsg);
SPQ_API extern SPQconninfoOption *SPQconndefaults(void);
SPQ_API extern SPQconninfoOption *SPQconninfo(SPGconn *conn);
SPQ_API extern void SPQconninfoFree(SPQconninfoOption *connOptions);

/* Status */
SPQ_API extern SPQConnStatusType SPQstatus(const SPGconn *conn);
SPQ_API extern SPGTransactionStatusType SPQtransactionStatus(const SPGconn *conn);
SPQ_API extern char *SPQerrorMessage(const SPGconn *conn);
SPQ_API extern char *SPQdb(const SPGconn *conn);
SPQ_API extern char *SPQuser(const SPGconn *conn);
SPQ_API extern char *SPQpass(const SPGconn *conn);
SPQ_API extern char *SPQhost(const SPGconn *conn);
SPQ_API extern char *SPQport(const SPGconn *conn);
SPQ_API extern char *SPQoptions(const SPGconn *conn);
SPQ_API extern const char *SPQparameterStatus(const SPGconn *conn, const char *paramName);
SPQ_API extern int SPQprotocolVersion(const SPGconn *conn);
SPQ_API extern int SPQserverVersion(const SPGconn *conn);
SPQ_API extern int SPQbackendPID(const SPGconn *conn);
SPQ_API extern int SPQsocket(const SPGconn *conn);
SPQ_API extern int SPQclientEncoding(const SPGconn *conn);
SPQ_API extern int SPQsetClientEncoding(SPGconn *conn, const char *encoding);
SPQ_API extern int SPQlibVersion(void);
SPQ_API extern int SPQisthreadsafe(void);
SPQ_API extern const char *spg_encoding_to_char(int encoding);
SPQ_API extern int spg_char_to_encoding(const char *name);

/* Nonblocking execution */
SPQ_API extern int SPQsendQuery(SPGconn *conn, const char *query);
SPQ_API extern int SPQsendQueryParams(SPGconn *conn, const char *command, int nParams, const SPQOid *paramTypes, const char *const *paramValues, const int *paramLengths, const int *paramFormats, int resultFormat);
SPQ_API extern int SPQsendPrepare(SPGconn *conn, const char *stmtName, const char *query, int nParams, const SPQOid *paramTypes);
SPQ_API extern int SPQsendQueryPrepared(SPGconn *conn, const char *stmtName, int nParams, const char *const *paramValues, const int *paramLengths, const int *paramFormats, int resultFormat);
SPQ_API extern int SPQsendDescribePrepared(SPGconn *conn, const char *stmt);
SPQ_API extern int SPQsendDescribePortal(SPGconn *conn, const char *portal);
SPQ_API extern int SPQsendClosePrepared(SPGconn *conn, const char *stmt);
SPQ_API extern int SPQsendClosePortal(SPGconn *conn, const char *portal);
SPQ_API extern SPGresult *SPQgetResult(SPGconn *conn);
SPQ_API extern int SPQconsumeInput(SPGconn *conn);
SPQ_API extern int SPQisBusy(SPGconn *conn);
SPQ_API extern int SPQflush(SPGconn *conn);
SPQ_API extern int SPQsetnonblocking(SPGconn *conn, int arg);
SPQ_API extern int SPQisnonblocking(const SPGconn *conn);
SPQ_API extern int SPQsocketPoll(int sock, int forRead, int forWrite, spg_usec_time_t end_time);
SPQ_API extern spg_usec_time_t SPQgetCurrentTimeUSec(void);

/* Row-by-row retrieval */
SPQ_API extern int SPQsetSingleRowMode(SPGconn *conn);
SPQ_API extern int SPQsetChunkedRowsMode(SPGconn *conn, int chunkSize);

/* Blocking execution */
SPQ_API extern SPGresult *SPQexec(SPGconn *conn, const char *query);
SPQ_API extern SPGresult *SPQexecParams(SPGconn *conn, const char *command, int nParams, const SPQOid *paramTypes, const char *const *paramValues, const int *paramLengths, const int *paramFormats, int resultFormat);
SPQ_API extern SPGresult *SPQprepare(SPGconn *conn, const char *stmtName, const char *query, int nParams, const SPQOid *paramTypes);
SPQ_API extern SPGresult *SPQexecPrepared(SPGconn *conn, const char *stmtName, int nParams, const char *const *paramValues, const int *paramLengths, const int *paramFormats, int resultFormat);
SPQ_API extern SPGresult *SPQdescribePrepared(SPGconn *conn, const char *stmt);
SPQ_API extern SPGresult *SPQdescribePortal(SPGconn *conn, const char *portal);
SPQ_API extern SPGresult *SPQclosePrepared(SPGconn *conn, const char *stmt);
SPQ_API extern SPGresult *SPQclosePortal(SPGconn *conn, const char *portal);

/* Results */
SPQ_API extern SPQExecStatusType SPQresultStatus(const SPGresult *res);
SPQ_API extern char *SPQresStatus(SPQExecStatusType status);
SPQ_API extern int SPQntuples(const SPGresult *res);
SPQ_API extern int SPQnfields(const SPGresult *res);
SPQ_API extern int SPQbinaryTuples(const SPGresult *res);
SPQ_API extern char *SPQfname(const SPGresult *res, int field_num);
SPQ_API extern int SPQfnumber(const SPGresult *res, const char *field_name);
SPQ_API extern SPQOid SPQftable(const SPGresult *res, int field_num);
SPQ_API extern int SPQftablecol(const SPGresult *res, int field_num);
SPQ_API extern int SPQfformat(const SPGresult *res, int field_num);
SPQ_API extern SPQOid SPQftype(const SPGresult *res, int field_num);
SPQ_API extern int SPQfsize(const SPGresult *res, int field_num);
SPQ_API extern int SPQfmod(const SPGresult *res, int field_num);
SPQ_API extern char *SPQgetvalue(const SPGresult *res, int tup_num, int field_num);
SPQ_API extern int SPQgetlength(const SPGresult *res, int tup_num, int field_num);
SPQ_API extern int SPQgetisnull(const SPGresult *res, int tup_num, int field_num);
SPQ_API extern int SPQnparams(const SPGresult *res);
SPQ_API extern SPQOid SPQparamtype(const SPGresult *res, int param_num);
SPQ_API extern char *SPQcmdStatus(SPGresult *res);
SPQ_API extern char *SPQcmdTuples(SPGresult *res);
SPQ_API extern SPQOid SPQoidValue(const SPGresult *res);
SPQ_API extern size_t SPQresultMemorySize(const SPGresult *res);
SPQ_API extern void SPQclear(SPGresult *res);
SPQ_API extern void SPQfreemem(void *ptr);

/* Errors and notices */
SPQ_API extern char *SPQresultErrorMessage(const SPGresult *res);
SPQ_API extern char *SPQresultErrorField(const SPGresult *res, int fieldcode);
SPQ_API extern char *SPQresultVerboseErrorMessage(const SPGresult *res, SPGVerbosity verbosity, SPGContextVisibility show_context);
SPQ_API extern SPGVerbosity SPQsetErrorVerbosity(SPGconn *conn, SPGVerbosity verbosity);
SPQ_API extern SPGContextVisibility SPQsetErrorContextVisibility(SPGconn *conn, SPGContextVisibility show_context);
SPQ_API extern SPQnoticeReceiver SPQsetNoticeReceiver(SPGconn *conn, SPQnoticeReceiver proc, void *arg);
SPQ_API extern SPQnoticeProcessor SPQsetNoticeProcessor(SPGconn *conn, SPQnoticeProcessor proc, void *arg);

/* Escaping */
SPQ_API extern char *SPQescapeLiteral(SPGconn *conn, const char *str, size_t len);
SPQ_API extern char *SPQescapeIdentifier(SPGconn *conn, const char *str, size_t len);
SPQ_API extern size_t SPQescapeStringConn(SPGconn *conn, char *to, const char *from, size_t length, int *error);
SPQ_API extern size_t SPQescapeString(char *to, const char *from, size_t length);
SPQ_API extern unsigned char *SPQescapeByteaConn(SPGconn *conn, const unsigned char *from, size_t from_length, size_t *to_length);
SPQ_API extern unsigned char *SPQescapeBytea(const unsigned char *from, size_t from_length, size_t *to_length);
SPQ_API extern unsigned char *SPQunescapeBytea(const unsigned char *strtext, size_t *retbuflen);

/* Cancel */
SPQ_API extern SPGcancel *SPQgetCancel(SPGconn *conn);
SPQ_API extern int SPQcancel(SPGcancel *cancel, char *errbuf, int errbufsize);
SPQ_API extern void SPQfreeCancel(SPGcancel *cancel);

/* COPY */
SPQ_API extern int SPQputCopyData(SPGconn *conn, const char *buffer, int nbytes);
SPQ_API extern int SPQputCopyEnd(SPGconn *conn, const char *errormsg);
SPQ_API extern int SPQgetCopyData(SPGconn *conn, char **buffer, int async);

/* Notifications */
SPQ_API extern SPGnotify *SPQnotifies(SPGconn *conn);
```

The header must compile as C11 and as C++17 with
`-pedantic -Wall -Wextra -Werror` **(measured for the scratch subset)**.

### 3.2 `libspq-ext.h`

Includes `libspq-fe.h`; own guard `LIBSPQ_EXT_H` and `extern "C"` block. Final
signatures:

```c
SPQ_API extern const char *SPQXversion(void);
SPQ_API extern int SPQXclusterDrop(const char *host, const char *port, char *errbuf, int errbufsize);
SPQ_API extern int SPQXreset(SPGconn *conn);
SPQ_API extern int SPQXdump(SPGconn *conn, char **buffer, size_t *length);
SPQ_API extern int SPQXrestore(SPGconn *conn, const char *buffer, size_t length);
```

| Function | Result and ownership |
|---|---|
| `SPQXversion` | A static string, the version of libspq (`"0.1.0"`, equal to `spq.Version`). Never freed. |
| `SPQXclusterDrop` | 1 when the cluster is gone (also when it never existed), 0 on failure. `host` NULL or empty means `localhost`, `port` NULL or empty means `5432` (CLIENT 4.6). On failure the message, without a trailing newline, is copied to `errbuf` (truncated to `errbufsize - 1` bytes and NUL-terminated; skipped when `errbuf` is NULL or `errbufsize <= 0`). Not tied to a connection. |
| `SPQXreset` | 1 on success, 0 on failure with the text in `SPQerrorMessage(conn)`. 0 for a NULL or unusable connection. |
| `SPQXdump` | 1 on success: `*buffer` is a `malloc` block the caller releases with `SPQfreemem`; it holds `*length` bytes followed by one NUL. 0 on failure: `*buffer` is NULL, `*length` 0, text in `SPQerrorMessage(conn)`. `buffer` must not be NULL (0 is returned); `length` may be NULL. |
| `SPQXrestore` | 1 on success, 0 on failure with the text in `SPQerrorMessage(conn)`. `buffer` NULL with `length` 0 is an empty dump. |

#### Optional extensions (parts LIBSPQ-EXT-01-PORTAL and LIBSPQ-EXT-02-NOTICE-QUEUE)

These declarations are added to `libspq-ext.h` by the part that implements them, not
before (the symbol check requires every declared-and-exported pair to match).

```c
/* LIBSPQ-EXT-01-PORTAL */
SPQ_API extern int SPQXsendBindPortal(SPGconn *conn, const char *portalName, const char *stmtName,
									  int nParams, const char *const *paramValues,
									  const int *paramLengths, const int *paramFormats, int resultFormat);
SPQ_API extern int SPQXsendExecutePortal(SPGconn *conn, const char *portalName, int maxRows);
SPQ_API extern SPGresult *SPQXbindPortal(SPGconn *conn, const char *portalName, const char *stmtName,
										 int nParams, const char *const *paramValues,
										 const int *paramLengths, const int *paramFormats, int resultFormat);
SPQ_API extern SPGresult *SPQXexecutePortal(SPGconn *conn, const char *portalName, int maxRows);
SPQ_API extern int SPQXresultSuspended(const SPGresult *res);

/* LIBSPQ-EXT-02-NOTICE-QUEUE */
SPQ_API extern int SPQXsetNoticeQueue(SPGconn *conn, int enable);
SPQ_API extern SPGresult *SPQXnextNotice(SPGconn *conn);
```

Portal execution with a row limit (EXT-01):

- `SPQXsendBindPortal` sends Bind for a named portal on a prepared statement
  (`stmtName`, created with `SPQprepare`; parameters as in `SPQsendQueryPrepared`) and
  Sync. The command yields one `SPGRES_COMMAND_OK` result, then NULL. A portal lives
  until the end of the transaction, so this is only useful inside a transaction block;
  outside one the portal is gone at the Sync.
- `SPQXsendExecutePortal` sends Describe (portal), Execute with `maxRows` (0 means all
  rows) and Sync. The command yields one result: `SPGRES_TUPLES_OK` with at most
  `maxRows` rows (or `SPGRES_COMMAND_OK` for a portal without rows), then NULL.
  `SPQXresultSuspended(res)` is 1 when the portal has more rows (the engine answered
  PortalSuspended; `SPQcmdStatus` is then empty) and 0 when it ran to completion.
  Calling it again fetches the next slice. An unknown portal gives an error result
  with SQLSTATE `34000`.
- `SPQXbindPortal` and `SPQXexecutePortal` are the blocking forms (the `SPQexec`
  sequence with the corresponding send function).
- Go side (new file `spq/portal.go`, plus the edits the part lists):
  `func (c *Conn) SendBindPortal(portal, stmt *string, nParams int, params []Param, resultFormat int) bool`,
  `func (c *Conn) SendExecutePortal(portal *string, maxRows int) bool`,
  `func (r *Result) Suspended() bool`; a command class `cmdBind` for which message `2`
  completes a `CommandOK` result (it is ignored for the other classes, CLIENT 7.4
  row 12); a new row in CLIENT table 7.4: state busy, message `s`: if a result is
  pending mark it suspended; `async = asyncReady`.

Notice queue (EXT-02), pure C on the queue of section 4.3:

- `SPQXsetNoticeQueue(conn, enable)` sets `conn->notice_queue` and returns the previous
  value (0 for a NULL connection). While it is 1, notices are not passed to the
  receiver; they stay queued in arrival order.
- `SPQXnextNotice(conn)` removes and returns the oldest queued notice as an
  `SPGRES_NONFATAL_ERROR` result owned by the caller (`SPQclear`), or NULL when the
  queue is empty. It does not consume input; call it after any function that does.
- Turning the queue off delivers nothing retroactively; the next consuming call
  delivers what is still queued through the receiver.

### 3.3 `libspq-compat.h`

```c
#ifndef LIBSPQ_COMPAT_H
#define LIBSPQ_COMPAT_H

#ifdef LIBPQ_FE_H
#error "libspq-compat.h cannot be combined with libpq-fe.h"
#endif

#include "libspq-fe.h"

#define LIBPQ_HAS_CHUNK_MODE 1
#define LIBPQ_HAS_CLOSE_PREPARED 1
#define LIBPQ_HAS_SOCKET_POLL 1

#define PQexec SPQexec
/* ... one object-like macro per kept identifier of section 2 ... */
#endif
```

One `#define <libpq name> <libspq name>` for each kept identifier of sections 2.1 to
2.4: the 22 type and tag names, the 50 enumerators, the 23 macros (as names:
`#define PQfreeNotify SPQfreeNotify`, `#define InvalidOid SPQInvalidOid`) and the 104
functions, 199 lines in all. Of libpq's `LIBPQ_HAS_*` macros it defines exactly the
three that are true for libspq. It does not include `libspq-ext.h` and defines nothing
for dropped identifiers, so source that uses an absent function fails to compile with
an undeclared-identifier error.

A generated draft of the three headers, compiled as C11 and C++17 with
`-pedantic -Wall -Wextra -Werror`, is kept in `.cswd/tmp/LIBSPQ/cabi/hdr/`
together with the generator `rename.py` **(measured)**. The header part may start from
it; this section and section 2 are authoritative.
## 4. C data structures

### 4.1 `spq_int.h`

The complete text of the private header. It is created by LIBSPQ-CABI-01-CONNECT and
not edited afterwards (F10); a prototype whose function does not exist yet is harmless
as long as nothing calls it.

```c
/*
 * spq_int.h
 *	  Private declarations shared by the C files and the cgo preambles of
 *	  bindings/libspq. Not installed. Written once (LIBSPQ-CABI-01-CONNECT);
 *	  later parts implement what it declares and do not edit it.
 */
#ifndef SPQ_INT_H
#define SPQ_INT_H

#include <stddef.h>
#include <stdint.h>

#include "libspq-fe.h"
#include "libspq-ext.h"

#define SPQ_CMDSTATUS_LEN 64

/* Selectors of spqgo_conn_string and indexes of spg_conn.str. */
enum spq_str
{
	SPQ_STR_ERROR = 0,
	SPQ_STR_DB,
	SPQ_STR_USER,
	SPQ_STR_PASS,
	SPQ_STR_HOST,
	SPQ_STR_PORT,
	SPQ_STR_OPTIONS,
	SPQ_STR_COUNT
};

/* Selectors of spqgo_conn_int. */
enum spq_int
{
	SPQ_INT_STATUS = 0,
	SPQ_INT_TRANSACTION_STATUS,
	SPQ_INT_PROTOCOL_VERSION,
	SPQ_INT_SERVER_VERSION,
	SPQ_INT_BACKEND_PID
};

/* Notice hooks of a connection; a result keeps the copy it was created with. */
struct spq_hooks
{
	SPQnoticeReceiver noticeRec;
	void	   *noticeRecArg;
	SPQnoticeProcessor noticeProc;
	void	   *noticeProcArg;
};

/* One cached SPQparameterStatus value. */
struct spq_param
{
	struct spq_param *next;
	char	   *name;
	char	   *value;
};

struct spg_conn
{
	uintptr_t	handle;			/* runtime/cgo.Handle of the *spq.Conn; 0 when
								 * the object was built without entering Go */
	int			sock;			/* readiness descriptor, -1 when none */
	int			cancel_fd;		/* write end of the cancel socketpair, -1 when
								 * none; owned by Go */
	struct spq_hooks hooks;
	int			notice_queue;	/* 1: notices stay queued (LIBSPQ-EXT-02) */
	SPGresult  *notices_head;	/* notices handed over by Go, oldest first */
	SPGresult  *notices_tail;
	char	   *str[SPQ_STR_COUNT];	/* strings returned by accessors */
	struct spq_param *params;
};

struct spq_field
{
	char	   *name;
	SPQOid		tableid;
	int			columnid;
	int			format;
	SPQOid		typid;
	int			typlen;
	int			atttypmod;
};

struct spq_errfield
{
	char		code;
	char	   *value;
};

struct spq_cell
{
	int			len;			/* -1 for SQL NULL */
	char	   *value;			/* NUL-terminated; null_field for SQL NULL */
};

struct spg_result
{
	SPQExecStatusType status;
	int			ntups;
	int			nfields;
	int			nparams;
	int			binary;
	int			client_encoding;
	int			suspended;		/* LIBSPQ-EXT-01; 0 otherwise */
	struct spq_field *fields;	/* nfields entries */
	SPQOid	   *paramTypes;		/* nparams entries */
	struct spq_cell *cells;		/* ntups * nfields entries, row-major */
	char	   *data;			/* copy of the DataRow bodies; cells point into it */
	char		cmdStatus[SPQ_CMDSTATUS_LEN];
	char	   *cmdTuples;		/* row count as text, "" when the tag has none */
	int			cmdTuplesOk;	/* 0: the tag could not be interpreted */
	SPQOid		oidValue;
	char	   *errMsg;			/* formatted message, NULL when none */
	char	   *errQuery;		/* query text kept for position display, or NULL */
	struct spq_errfield *errFields;
	int			nerrFields;
	size_t		memsize;		/* bytes allocated for this result */
	struct spq_hooks hooks;
	SPGresult  *next;			/* link in spg_conn.notices_* */
	char		null_field[1];	/* the value of every SQL NULL: "" */
};

struct spg_cancel
{
	int			fd;				/* duplicate of spg_conn.cancel_fd */
};

/* guard.c */
extern const char spq_child_message[];
extern int	spq_in_child(void);

/* conn.c */
extern SPGconn *spq_conn_alloc(void);
extern int	spq_conn_usable(const SPGconn *conn);

/* notices.c */
extern void spq_default_hooks(struct spq_hooks *hooks);
extern void spq_conn_queue_notice(SPGconn *conn, SPGresult *res);
extern void spq_deliver_notices(SPGconn *conn);
extern void spq_discard_notices(SPGconn *conn);
extern void spq_internal_notice(const struct spq_hooks *hooks, const char *fmt,...)
			__attribute__((format(printf, 2, 3)));

/* result.c: builders called from Go. Each returns 1, or 0 when out of memory or
 * given inconsistent data. char * arguments documented as "owned" were allocated
 * with malloc by the caller and belong to the result afterwards, also on failure. */
extern SPGresult *spq_result_new(const struct spq_hooks *hooks, int status, int client_encoding);
extern int	spq_result_set_fields(SPGresult *res, int nfields);
extern int	spq_result_set_field(SPGresult *res, int i, char *name_owned, SPQOid tableid, int columnid,
								 int format, SPQOid typid, int typlen, int atttypmod);
extern int	spq_result_set_params(SPGresult *res, int nparams, const SPQOid *types);
extern int	spq_result_set_command(SPGresult *res, const char *cmdStatus, char *cmdTuples_owned,
								   int cmdTuplesOk, SPQOid oidValue, int binary);
extern int	spq_result_set_error(SPGresult *res, char *message_owned, char *query_owned);
extern int	spq_result_add_error_field(SPGresult *res, char code, char *value_owned);
extern int	spq_result_load_rows(SPGresult *res, const char *data, size_t len, int ntups);

/* query.c */
extern int	spq_wait_readable(SPGconn *conn);

/* exec.c */
extern int	spq_exec_start(SPGconn *conn);
extern SPGresult *spq_exec_finish(SPGconn *conn);

/* conninfo.c: builders called from Go; strings are owned as above */
extern SPQconninfoOption *spq_conninfo_new(int n);
extern void spq_conninfo_set(SPQconninfoOption *options, int i, char *keyword_owned, char *envvar_owned,
							 char *compiled_owned, char *val_owned, char *label_owned,
							 char *dispchar_owned, int dispsize);

/* notify.c: builder called from Go; the strings are copied */
extern SPGnotify *spq_notify_new(const char *relname, int be_pid, const char *extra);

#endif							/* SPQ_INT_H */
```

### 4.2 Connection object

- Allocated by `spq_conn_alloc` with `calloc`; `sock` and `cancel_fd` start at -1 and
  `hooks` is set by `spq_default_hooks`. Freed only by `SPQfinish`.
- `handle` is written by the Go connect functions (`spqgo_connect` and the variants of
  the connect-async part) together with `sock` and `cancel_fd`:
  `conn.handle = C.uintptr_t(cgo.NewHandle(c))`, `conn.sock = C.int(c.Socket())`,
  `conn.cancel_fd = C.int(c.CancelFD())`. The descriptor numbers never change
  afterwards (CLIENT 6.7); `SPQsocket` returns the cached number without entering Go.
- A connection is **usable** when `spq_conn_usable(conn)` holds:
  `conn != NULL && conn->handle != 0 && !spq_in_child()`. Every function that enters Go
  checks it first.
- `str[]` caches the last string each accessor returned. An accessor frees the old
  entry, stores the new `malloc` copy that Go returned and returns it, so a pointer
  stays valid until the same accessor is called again on that connection (docs/LIBSPQ.md
  5.8 promises less: until the next call on the connection).
- `params` is a singly linked list keyed by parameter name; `SPQparameterStatus`
  replaces the value of an existing entry or prepends a new one.
- `SPQfinish` frees `str[]`, the `params` list and the queued notices
  (`spq_discard_notices`), then the object.

### 4.3 Notices

Queue: `notices_head` / `notices_tail` of the connection, linked through
`spg_result.next`. Only Go appends (`spq_conn_queue_notice`, called from
`queueNotices` in `notices.go` before the `spqgo_*` function returns); only C removes.

```c
void
spq_deliver_notices(SPGconn *conn)
{
	if (conn->notice_queue)
		return;
	while (conn->notices_head != NULL)
	{
		SPGresult  *res = conn->notices_head;

		conn->notices_head = res->next;
		if (conn->notices_head == NULL)
			conn->notices_tail = NULL;
		res->next = NULL;
		if (res->hooks.noticeRec != NULL)
			res->hooks.noticeRec(res->hooks.noticeRecArg, res);
		SPQclear(res);
	}
}
```

The element is unlinked before the callback runs, so a callback that (against the
rules) calls back into the library for the same connection cannot corrupt the queue.
`spq_discard_notices` is the same loop without the callback and without the
`notice_queue` test.

Default hooks (`spq_default_hooks` stores these four values):

| Hook | Default |
|---|---|
| `noticeRec` | a static function that calls `res->hooks.noticeProc(res->hooks.noticeProcArg, SPQresultErrorMessage(res))` when `noticeProc` is not NULL |
| `noticeRecArg` | NULL |
| `noticeProc` | a static function that does `fprintf(stderr, "%s", message)` |
| `noticeProcArg` | NULL |

`spq_internal_notice(hooks, fmt, ...)` formats the text into a 1024-byte buffer
(`vsnprintf`), builds a result with `spq_result_new(hooks, SPGRES_NONFATAL_ERROR, 0)`,
adds the error fields `M` = text, `S` = `NOTICE`, `V` = `NOTICE`, sets the message to
the text plus a newline, calls `hooks->noticeRec` when not NULL and clears the result.
It does nothing when `hooks->noticeRec` is NULL or memory runs out. It is the C
counterpart of CLIENT 8.4 "internal notice" and is used by the accessors of section
6.4.

### 4.4 Result block

One `struct spg_result` plus separately allocated members; `SPQclear` frees, in this
order: every `fields[i].name` and `fields`; `paramTypes`; `cells`; `data`;
`cmdTuples`; `errMsg`; `errQuery`; every `errFields[i].value` and `errFields`; the
struct. `SPQclear(NULL)` does nothing. `memsize` is `sizeof(struct spg_result)` plus
the size of every allocation made by the builders (for strings `strlen + 1`);
`SPQresultMemorySize` returns it (0 for NULL).

A result is never modified after the builder sequence that created it returns, which
is what makes it safe to read from any thread.

Builders (called from Go, `result.go`), all returning 1 on success and 0 on failure:

| Builder | Does |
|---|---|
| `spq_result_new(hooks, status, client_encoding)` | `calloc`; stores status and encoding; copies `*hooks` when `hooks` is not NULL; `memsize = sizeof`. Returns NULL when out of memory. |
| `spq_result_set_fields(res, nfields)` | `calloc(nfields, sizeof(struct spq_field))`; sets `res->nfields`. `nfields` 0 is allowed and allocates nothing. |
| `spq_result_set_field(res, i, name, ...)` | stores the descriptor; takes ownership of `name`. Fails (and frees `name`) when `i` is out of range. |
| `spq_result_set_params(res, nparams, types)` | copies the array. |
| `spq_result_set_command(res, cmdStatus, cmdTuples, ok, oid, binary)` | copies at most 63 bytes of `cmdStatus` into the fixed buffer and terminates it; takes ownership of `cmdTuples`; stores the three scalars. |
| `spq_result_set_error(res, message, query)` | takes ownership of both; `query` may be NULL. |
| `spq_result_add_error_field(res, code, value)` | grows `errFields` by one (`realloc`); takes ownership of `value`. |
| `spq_result_load_rows(res, data, len, ntups)` | section 4.5. `data` is borrowed for the call. |

"Takes ownership" includes the failure case: the builder frees the string itself when
it cannot store it, so the Go caller never frees an owned argument.

### 4.5 Row index

`Result.RowData()` is the concatenation of `ntups` DataRow bodies: for each row an
int16 field count, then per field an int32 length (-1 for NULL) and that many bytes,
integers big-endian (CLIENT 5.3). `spq_result_load_rows` copies it once into
`res->data` (one spare byte at the end) and builds `res->cells` in a single pass.
Values are NUL-terminated **in place**: the terminator of a value overwrites the first
byte of whatever header follows it (the next length or the next row's field count),
which has been read before; the terminator of the very last value goes into the spare
byte. `SPQgetvalue` therefore returns a NUL-terminated buffer for text and binary
values alike without a second copy.

This is the complete function **(measured: tested under AddressSanitizer and
UndefinedBehaviorSanitizer with NULL, empty, binary-with-zero-byte values, zero rows,
truncated data, a wrong field count and trailing bytes)**:

```c
static unsigned int
spq_rd16(const unsigned char *p)
{
	return ((unsigned int) p[0] << 8) | p[1];
}

static int
spq_rd32(const unsigned char *p)
{
	return (int) (((uint32_t) p[0] << 24) | ((uint32_t) p[1] << 16) | ((uint32_t) p[2] << 8) | p[3]);
}

int
spq_result_load_rows(SPGresult *res, const char *data, size_t len, int ntups)
{
	size_t		ncells = (size_t) ntups * (size_t) res->nfields;
	unsigned char *p;
	unsigned char *end;
	char	   *pending = NULL;	/* where the terminator of the last value goes */
	size_t		cell = 0;

	if (ntups < 0 || res->cells != NULL || res->data != NULL)
		return 0;
	res->data = malloc(len + 1);
	res->cells = calloc(ncells > 0 ? ncells : 1, sizeof(struct spq_cell));
	if (res->data == NULL || res->cells == NULL)
		return 0;
	res->memsize += len + 1 + (ncells > 0 ? ncells : 1) * sizeof(struct spq_cell);
	if (len > 0)
		memcpy(res->data, data, len);
	p = (unsigned char *) res->data;
	end = p + len;
	for (int r = 0; r < ntups; r++)
	{
		unsigned int n;

		if (end - p < 2)
			return 0;
		n = spq_rd16(p);		/* read the header before it is overwritten */
		if (pending != NULL)
		{
			*pending = '\0';
			pending = NULL;
		}
		p += 2;
		if (n != (unsigned int) res->nfields)
			return 0;
		for (unsigned int f = 0; f < n; f++)
		{
			int			l;

			if (end - p < 4)
				return 0;
			l = spq_rd32(p);
			if (pending != NULL)
			{
				*pending = '\0';
				pending = NULL;
			}
			p += 4;
			if (l < 0)
			{
				res->cells[cell].len = -1;
				res->cells[cell].value = res->null_field;
			}
			else
			{
				if ((size_t) (end - p) < (size_t) l)
					return 0;
				res->cells[cell].len = l;
				res->cells[cell].value = (char *) p;
				p += l;
				pending = (char *) p;
			}
			cell++;
		}
	}
	if (pending != NULL)
		*pending = '\0';		/* the spare byte at data[len] */
	if (p != end)
		return 0;
	res->ntups = ntups;
	return 1;
}
```

It returns 0 on any inconsistency; the Go caller then clears the result and returns
NULL. It must run after `spq_result_set_fields` (it reads `res->nfields`); for a
result without fields `ntups` is 0 and `len` is 0.

### 4.6 Other objects

- `SPGcancel`: `malloc`; `fd` is `fcntl(conn->cancel_fd, F_DUPFD_CLOEXEC, 0)`, so it
  stays valid after `SPQfinish` closes the original. `SPQfreeCancel` closes it and
  frees.
- `SPGnotify`: one `malloc` block holding the struct followed by the two strings;
  `relname` and `extra` point into the block; `next` is NULL. Released with
  `SPQfreemem`.
- `SPQconninfoOption` array: `spq_conninfo_new(n)` is `calloc(n + 1, ...)` (the extra
  entry is the terminator with `keyword == NULL`); `spq_conninfo_set` stores six owned
  strings (any may be NULL) and `dispsize`. `SPQconninfoFree` frees the six strings of
  every entry up to the terminator, then the array.
- Buffers returned to the caller (`SPQescapeLiteral`, `SPQescapeIdentifier`,
  `SPQescapeByteaConn`, `SPQescapeBytea`, `SPQunescapeBytea`, `SPQgetCopyData`,
  `SPQresultVerboseErrorMessage`, `SPQconninfoParse`'s `*errmsg`, `SPQXdump`) are
  `malloc` blocks made by Go with `C.CString` / `C.CBytes` / `C.malloc`; `SPQfreemem`
  is `free`.
## 5. The Go side: package `main` and the `spqgo_*` exports

### 5.1 Conventions

Every Go file of the package:

```go
//go:build libspq && cgo && linux

package main

/*
#include <stdlib.h>
#include <string.h>
#include "spq_int.h"
*/
import "C"
```

(`main.go` additionally carries the `#cgo` line of section 8.1 and `func main() {}`;
its preamble includes nothing.) A preamble contains only `#include` lines: cgo forbids
definitions in the preamble of a file that uses `//export`.

- **Export names** are `spqgo_<what>`. The C prototype is the one cgo generates in
  `_cgo_export.h` from the Go signature; C files include `"_cgo_export.h"` and cast
  `const` away at the call (`(char *) query`). Go never retains a C pointer after the
  call returns, except the `*C.SPGconn` fields it is documented to write.
- **Type mapping**: `C.uintptr_t` handle; `*C.SPGconn`, `*C.SPGresult`,
  `*C.SPQconninfoOption`, `*C.SPGnotify`; `*C.char` strings; `C.int`; `C.size_t`;
  `C.SPQOid`; `*C.uchar` for `unsigned char *`.
- **Panics (F8)**: every export has named results and starts with

  ```go
  defer func() {
  	if recover() != nil {
  		rc = 0 // the failure value of this export, listed in 5.3
  	}
  }()
  ```

- **Notices (F7)**: an export marked ◆ in 5.3 has `defer queueNotices(conn, c)` right
  after the handle lookup (so it runs before the recover handler). The C caller then
  calls `spq_deliver_notices(conn)`.
- **Strings returned to C** are `C.CString(s)` (a `malloc` copy). A Go `nil`/absent
  value is a C NULL.

### 5.2 Helpers (not exported to C)

| Helper | File | Definition |
|---|---|---|
| `lookup(h C.uintptr_t) *spq.Conn` | `conn.go` | `cgo.Handle(h).Value().(*spq.Conn)` |
| `attach(conn *C.SPGconn, c *spq.Conn)` | `conn.go` | writes `handle`, `sock`, `cancel_fd` (4.2) |
| `goString(s *C.char) string` | `conn.go` | `""` for NULL, else `C.GoString(s)` |
| `optString(s *C.char) *string` | `conn.go` | nil for NULL, else pointer to `C.GoString(s)` |
| `boolInt(b bool) C.int` | `conn.go` | 1 or 0 |
| `queueNotices(conn *C.SPGconn, c *spq.Conn)` | `notices.go` | for each of `c.TakeNotices()`: `res := newCResult(&conn.hooks, n)`; if not nil, `C.spq_conn_queue_notice(conn, res)` |
| `newCResult(hooks *C.struct_spq_hooks, r *spq.Result) *C.SPGresult` | `result.go` | below |
| `goParams(n C.int, values **C.char, lengths, formats *C.int) []spq.Param` | `extended.go` | below |
| `goOids(n C.int, types *C.SPQOid) []uint32` | `extended.go` | nil when `types` is NULL or `n` is outside 1..65535; else a copy of `unsafe.Slice(types, n)` |
| `goStrings(list **C.char) []string`, `goPairs(keywords, values **C.char) (k, v []string)` | `connect_async.go` | walk the NULL-terminated `keywords` array; a NULL `values` array or entry gives `""` |
| `cOptions(options []spq.Option) *C.SPQconninfoOption` | `conninfo.go` | `spq_conninfo_new(len)`, then `spq_conninfo_set` per option with `C.CString` for non-nil strings; NULL when `spq_conninfo_new` fails |

`conn.go` also holds two compile-time checks that the constants written in C equal the
Go ones:

```go
const _ = uint(spq.LibVersion - 180006) // version.c returns 180006
const _ = uint(180006 - spq.LibVersion)

// A duplicate map key is a compile error: this fails unless spq.Version is "0.1.0",
// the string version.c returns.
var _ = map[bool]struct{}{false: {}, spq.Version == "0.1.0": {}}
```

`newCResult`:

```go
func newCResult(hooks *C.struct_spq_hooks, r *spq.Result) *C.SPGresult {
	res := C.spq_result_new(hooks, C.int(r.Status()), C.int(r.ClientEncoding()))
	if res == nil {
		return nil
	}
	ok := true
	if fields := r.Fields(); len(fields) > 0 {
		ok = C.spq_result_set_fields(res, C.int(len(fields))) != 0
		for i := 0; ok && i < len(fields); i++ {
			f := fields[i]
			ok = C.spq_result_set_field(res, C.int(i), C.CString(f.Name), C.SPQOid(f.TableOID),
				C.int(f.ColumnID), C.int(f.Format), C.SPQOid(f.TypeOID), C.int(f.TypeSize),
				C.int(f.TypeMod)) != 0
		}
	}
	if types := r.ParamTypes(); ok && len(types) > 0 {
		ok = C.spq_result_set_params(res, C.int(len(types)), (*C.SPQOid)(unsafe.Pointer(&types[0]))) != 0
	}
	if ok {
		count, countOK := r.CmdTuples()
		status := C.CString(r.CmdStatus())
		ok = C.spq_result_set_command(res, status, C.CString(count), boolInt(countOK),
			C.SPQOid(r.OidValue()), boolInt(r.Binary())) != 0
		C.free(unsafe.Pointer(status))
	}
	if ok && (r.Status() == spq.FatalError || r.Status() == spq.NonfatalError) {
		var query *C.char
		if q := r.ErrorQuery(); q != "" {
			query = C.CString(q)
		}
		ok = C.spq_result_set_error(res, C.CString(r.ErrorMessage()), query) != 0
		for _, f := range r.ErrorFields() {
			if !ok {
				break
			}
			ok = C.spq_result_add_error_field(res, C.char(f.Code), C.CString(f.Value)) != 0
		}
	}
	if ok {
		data := r.RowData()
		var p *C.char
		if len(data) > 0 {
			p = (*C.char)(unsafe.Pointer(&data[0]))
		}
		ok = C.spq_result_load_rows(res, p, C.size_t(len(data)), C.int(r.NTuples())) != 0
	}
	if !ok {
		C.SPQclear(res)
		return nil
	}
	return res
}
```

`goParams` (libpq's rules for the three parallel arrays):

```go
func goParams(n C.int, values **C.char, lengths, formats *C.int) []spq.Param {
	if n <= 0 || n > spq.MaxParams {
		return nil // spq reports the range error from nParams itself
	}
	params := make([]spq.Param, int(n))
	var vs []*C.char
	var ls, fs []C.int
	if values != nil {
		vs = unsafe.Slice(values, int(n))
	}
	if lengths != nil {
		ls = unsafe.Slice(lengths, int(n))
	}
	if formats != nil {
		fs = unsafe.Slice(formats, int(n))
	}
	for i := range params {
		p := &params[i]
		p.Binary = fs != nil && fs[i] != 0
		switch {
		case vs == nil || vs[i] == nil:
			p.Null = true
		case !p.Binary:
			p.Value = C.GoBytes(unsafe.Pointer(vs[i]), C.int(C.strlen(vs[i])))
		case ls == nil:
			p.LengthMissing = true
		case ls[i] > 0:
			p.Value = C.GoBytes(unsafe.Pointer(vs[i]), ls[i])
		default:
			p.Value = []byte{}
		}
	}
	return params
}
```

### 5.3 Export list

"h" is the `C.uintptr_t` handle, "conn" the `*C.SPGconn`. ◆ marks exports that queue
notices. "Fail" is the value returned after a recovered panic.

| Export (C prototype as generated) | Go signature | Wraps (CLIENT 2) | Pointers and results | Fail | File |
|---|---|---|---|---|---|
| `void spqgo_connect(SPGconn* conn, char* conninfo)` ◆ | `(conn *C.SPGconn, conninfo *C.char)` | `Connect(goString(conninfo))`, `attach` | `conninfo` borrowed; writes `conn` fields | fields stay 0 / -1 | `conn.go` |
| `void spqgo_finish(uintptr_t h)` | `(h C.uintptr_t)` | `Close()`, then `cgo.Handle(h).Delete()` | — | — | `conn.go` |
| `int spqgo_conn_int(uintptr_t h, int which)` | `(h C.uintptr_t, which C.int) (rc C.int)` | `Status`, `TransactionStatus`, `ProtocolVersion`, `ServerVersion`, `BackendPID` by `enum spq_int` | — | `SPQ_CONNECTION_BAD` for `SPQ_INT_STATUS`, `SPQTRANS_UNKNOWN` for the transaction status, 0 otherwise | `conn.go` |
| `char* spqgo_conn_string(uintptr_t h, int which)` | `(h C.uintptr_t, which C.int) (s *C.char)` | `ErrorMessage`, `DB`, `User`, `Password`, `Host`, `Port`, `Options` by `enum spq_str` | result: `malloc` string, C owns | NULL | `conn.go` |
| `char* spqgo_parameter_status(uintptr_t h, char* name)` | `(h C.uintptr_t, name *C.char) (s *C.char)` | `ParameterStatus` | result: `malloc` string or NULL when unknown | NULL | `conn.go` |
| `int spqgo_send_query(uintptr_t h, char* query)` | `(h C.uintptr_t, query *C.char) (rc C.int)` | `SendQuery(optString(query))` | borrowed | 0 | `query.go` |
| `SPGresult* spqgo_get_result(uintptr_t h, SPGconn* conn, int* would_block)` ◆ | `(h C.uintptr_t, conn *C.SPGconn, wouldBlock *C.int) (res *C.SPGresult)` | `GetResult`, `newCResult(&conn.hooks, r)` | result block owned by the caller; `*would_block` written | NULL, `*would_block` 0 | `query.go` |
| `int spqgo_consume_input(uintptr_t h, SPGconn* conn)` ◆ | `(h, conn) (rc C.int)` | `ConsumeInput` | — | 0 | `async.go` |
| `int spqgo_is_busy(uintptr_t h, SPGconn* conn)` ◆ | `(h, conn) (rc C.int)` | `IsBusy` | — | 0 | `async.go` |
| `int spqgo_flush(uintptr_t h)` | `(h) (rc C.int)` | `Flush` | — | -1 | `async.go` |
| `int spqgo_set_nonblocking(uintptr_t h, int arg)` | `(h, arg C.int) (rc C.int)` | `SetNonblocking(arg != 0)` | — | -1 | `async.go` |
| `int spqgo_is_nonblocking(uintptr_t h)` | `(h) (rc C.int)` | `IsNonblocking` | — | 0 | `async.go` |
| `int spqgo_exec_start(uintptr_t h, SPGconn* conn, int* would_block)` ◆ | `(h, conn, wouldBlock *C.int) (rc C.int)` | `ExecStart`; returns `ready` | `*would_block` written | 0, `*would_block` 0 | `exec.go` |
| `SPGresult* spqgo_exec_finish(uintptr_t h, SPGconn* conn, int* would_block)` ◆ | `(h, conn, wouldBlock *C.int) (res *C.SPGresult)` | `ExecFinish`, `newCResult` | as `spqgo_get_result` | NULL, 0 | `exec.go` |
| `int spqgo_send_query_params(uintptr_t h, char* command, int nParams, SPQOid* paramTypes, char** paramValues, int* paramLengths, int* paramFormats, int resultFormat)` | `(h, command *C.char, nParams C.int, paramTypes *C.SPQOid, paramValues **C.char, paramLengths, paramFormats *C.int, resultFormat C.int) (rc C.int)` | `SendQueryParams(optString(command), int(nParams), goOids(...), goParams(...), int(resultFormat))` | all borrowed; values copied by `goParams` | 0 | `extended.go` |
| `int spqgo_send_prepare(uintptr_t h, char* stmtName, char* query, int nParams, SPQOid* paramTypes)` | analogous | `SendPrepare(optString(stmtName), optString(query), int(nParams), goOids(...))` | borrowed | 0 | `extended.go` |
| `int spqgo_send_query_prepared(uintptr_t h, char* stmtName, int nParams, char** paramValues, int* paramLengths, int* paramFormats, int resultFormat)` | analogous | `SendQueryPrepared(optString(stmtName), int(nParams), goParams(...), int(resultFormat))` | borrowed | 0 | `extended.go` |
| `int spqgo_send_describe(uintptr_t h, int kind, char* name)` | `(h, kind C.int, name *C.char) (rc C.int)` | `kind == 'S'`: `SendDescribePrepared(goString(name))`; `'P'`: `SendDescribePortal` | borrowed | 0 | `extended.go` |
| `int spqgo_send_close(uintptr_t h, int kind, char* name)` | as above | `SendClosePrepared` / `SendClosePortal` | borrowed | 0 | `extended.go` |
| `char* spqgo_format_error(SPGresult* res, int verbosity, int show_context)` | `(res *C.SPGresult, verbosity, showContext C.int) (s *C.char)` | `spq.FormatError(fields, query, int(res.client_encoding), res.status == SPGRES_FATAL_ERROR, ...)` with the fields and `errQuery` read from the C block | reads `res`; result: `malloc` string | NULL | `errors.go` |
| `int spqgo_set_error_verbosity(uintptr_t h, int verbosity)` | `(h, v C.int) (old C.int)` | `SetErrorVerbosity` | — | `SPQERRORS_DEFAULT` | `errors.go` |
| `int spqgo_set_error_context_visibility(uintptr_t h, int show_context)` | `(h, v C.int) (old C.int)` | `SetErrorContextVisibility` | — | `SPQSHOW_CONTEXT_ERRORS` | `errors.go` |
| `size_t spqgo_escape_string_conn(uintptr_t h, char* to, char* from, size_t length, int* error)` | `(h, to, from *C.char, length C.size_t, errp *C.int) (n C.size_t)` | `EscapeStringConn(C.GoBytes(from, length))`; copies the output and a NUL into `to`; `*error` = 0 or 1 | `to` is caller memory of at least `2*length+1` bytes; `error` may be NULL | 0, `to[0] = 0`, `*error = 1` | `escape.go` |
| `size_t spqgo_escape_string(char* to, char* from, size_t length)` | `(to, from *C.char, length C.size_t) (n C.size_t)` | `EscapeStringStatic` | as above | 0, `to[0] = 0` | `escape.go` |
| `char* spqgo_escape_literal(uintptr_t h, char* str, size_t len, int identifier)` | `(h, str *C.char, n C.size_t, identifier C.int) (s *C.char)` | `EscapeLiteral` or `EscapeIdentifier` on `C.GoBytes(str, n)`; NUL appended | result: `malloc` or NULL when the method reports failure | NULL | `escape.go` |
| `unsigned char* spqgo_escape_bytea(uintptr_t h, unsigned char* from, size_t from_length, size_t* to_length)` | `(h, from *C.uchar, n C.size_t, toLength *C.size_t) (p *C.uchar)` | `h != 0`: `EscapeByteaConn`; `h == 0`: `EscapeByteaStatic`; NUL appended; `*to_length` = output length + 1 | result: `malloc` | NULL | `escape.go` |
| `unsigned char* spqgo_unescape_bytea(unsigned char* strtext, size_t* retbuflen)` | `(text *C.uchar, n *C.size_t) (p *C.uchar)` | `UnescapeBytea(C.GoBytes(text, strlen))`; one extra NUL byte allocated; `*retbuflen` = output length | result: `malloc` | NULL | `escape.go` |
| `SPQconninfoOption* spqgo_conninfo_parse(char* conninfo, char** errmsg)` | `(conninfo *C.char, errmsg **C.char) (o *C.SPQconninfoOption)` | `ParseConninfo`; on error `*errmsg = C.CString(err.Error() + "\n")` when `errmsg` is not NULL | result owned by caller (`SPQconninfoFree`) | NULL | `conninfo.go` |
| `SPQconninfoOption* spqgo_conndefaults(void)` | `() (o *C.SPQconninfoOption)` | `DefaultOptions` | owned by caller | NULL | `conninfo.go` |
| `SPQconninfoOption* spqgo_conninfo(uintptr_t h)` | `(h) (o *C.SPQconninfoOption)` | `Conn.Conninfo` | owned by caller | NULL | `conninfo.go` |
| `int spqgo_put_copy_data(uintptr_t h, SPGconn* conn, char* buffer, int nbytes)` ◆ | `(h, conn, buffer *C.char, nbytes C.int) (rc C.int)` | `PutCopyData(C.GoBytes(buffer, nbytes))`; `nbytes <= 0` or NULL buffer: empty slice | borrowed, copied | -1 | `copy.go` |
| `int spqgo_put_copy_end(uintptr_t h, char* errormsg)` | `(h, errormsg *C.char) (rc C.int)` | `PutCopyEnd(optString(errormsg))` | borrowed | -1 | `copy.go` |
| `int spqgo_get_copy_data(uintptr_t h, SPGconn* conn, char** buffer)` ◆ | `(h, conn, buffer **C.char) (rc C.int)` | `GetCopyData`; for `rc > 0`: `*buffer` = `malloc(rc + 1)` holding the row and a NUL | `*buffer` owned by caller | -2 | `copy.go` |
| `SPGnotify* spqgo_notifies(uintptr_t h, SPGconn* conn)` ◆ | `(h, conn) (n *C.SPGnotify)` | `Notifies`; `C.spq_notify_new(relname, C.int(pid), extra)` with temporary C strings freed after the call | owned by caller (`SPQfreemem`) | NULL | `notify.go` |
| `int spqgo_set_single_row_mode(uintptr_t h)` | `(h) (rc C.int)` | `SetSingleRowMode` | — | 0 | `rowmodes.go` |
| `int spqgo_set_chunked_rows_mode(uintptr_t h, int chunkSize)` | `(h, n C.int) (rc C.int)` | `SetChunkedRowsMode` | — | 0 | `rowmodes.go` |
| `void spqgo_connect_start(SPGconn* conn, char* conninfo)` ◆ | as `spqgo_connect` | `ConnectStart` | as `spqgo_connect` | — | `connect_async.go` |
| `void spqgo_connect_params(SPGconn* conn, char** keywords, char** values, int expand_dbname, int start)` ◆ | `(conn, keywords, values **C.char, expand, start C.int)` | `ConnectParams` or, when `start != 0`, `ConnectStartParams`, with `goPairs` | arrays borrowed | — | `connect_async.go` |
| `int spqgo_connect_poll(uintptr_t h)` | `(h) (rc C.int)` | `ConnectPoll` | — | `SPGRES_POLLING_FAILED` | `connect_async.go` |
| `void spqgo_reset(uintptr_t h, SPGconn* conn)` ◆ | `(h, conn)` | `Reset` | — | — | `connect_async.go` |
| `int spqgo_ping(char* conninfo)` | `(conninfo *C.char) (rc C.int)` | `Ping` | borrowed | `SPQPING_NO_ATTEMPT` | `connect_async.go` |
| `int spqgo_ping_params(char** keywords, char** values, int expand_dbname)` | analogous | `PingParams` | borrowed | `SPQPING_NO_ATTEMPT` | `connect_async.go` |
| `int spqgo_client_encoding(uintptr_t h)` | `(h) (rc C.int)` | `ClientEncoding` | — | -1 | `encoding.go` |
| `char* spqgo_client_encoding_statement(char* encoding)` | `(encoding *C.char) (s *C.char)` | `ClientEncodingStatement` | result: `malloc`, or NULL when unusable | NULL | `encoding.go` |
| `char* spqgo_encoding_to_char(int encoding)` | `(id C.int) (s *C.char)` | `EncodingToChar` | result: `malloc` (possibly `""`) | NULL | `encoding.go` |
| `int spqgo_char_to_encoding(char* name)` | `(name *C.char) (rc C.int)` | `CharToEncoding(goString(name))` | borrowed | -1 | `encoding.go` |
| `char* spqgo_cluster_drop(char* host, char* port)` | `(host, port *C.char) (errmsg *C.char)` | `DropCluster(goString(host), goString(port))` | NULL on success, else the error text as a `malloc` string | a `malloc` copy of `libspq: internal error` | `control.go` |
| `int spqgo_reset_database(uintptr_t h)` | `(h) (rc C.int)` | `ResetDatabase` | — | 0 | `control.go` |
| `int spqgo_dump(uintptr_t h, char** buffer, size_t* length)` | `(h, buffer **C.char, length *C.size_t) (rc C.int)` | `Dump`; `*buffer` = `malloc(len + 1)` with a NUL after the bytes; `*length` = len when `length` is not NULL | owned by caller | 0 | `dump.go` |
| `int spqgo_restore(uintptr_t h, char* buffer, size_t length)` | `(h, buffer *C.char, length C.size_t) (rc C.int)` | `Restore(C.GoBytes(buffer, length))` | borrowed | 0 | `dump.go` |

Lengths passed to `C.GoBytes` are `C.int`; a value above `INT_MAX` is rejected by the C
wrapper before the call (the function fails as for out of memory).
## 6. Public functions

"Unusable" means `!spq_conn_usable(conn)` (4.2): NULL connection, an object built in a
forked child, or any call made in a forked child. The column "Unusable / NULL" gives
the result in that case; it is libpq's result for a NULL argument. After a call marked
◆ the C function calls `spq_deliver_notices(conn)` before it returns.

### 6.1 The wait helper and the three loop shapes

```c
/* query.c */
int
spq_wait_readable(SPGconn *conn)
{
	struct pollfd pfd;

	if (conn->sock < 0)
		return 0;
	pfd.fd = conn->sock;
	pfd.events = POLLIN;
	for (;;)
	{
		int			rc;

		pfd.revents = 0;
		rc = poll(&pfd, 1, -1);
		if (rc > 0)
			return 1;
		if (rc < 0 && errno != EINTR)
			return 0;
	}
}
```

A would-block answer is only given while the session is alive, and the descriptor
stays readable once the session has ended (CLIENT 6.2), so the loop cannot wait
forever on a dead connection.

```c
/* query.c */
SPGresult *
SPQgetResult(SPGconn *conn)
{
	if (!spq_conn_usable(conn))
		return NULL;
	for (;;)
	{
		int			would_block = 0;
		SPGresult  *res = spqgo_get_result(conn->handle, conn, &would_block);

		spq_deliver_notices(conn);
		if (!would_block)
			return res;
		if (!spq_wait_readable(conn))
			return NULL;
	}
}

/* exec.c */
int
spq_exec_start(SPGconn *conn)
{
	for (;;)
	{
		int			would_block = 0;
		int			ready = spqgo_exec_start(conn->handle, conn, &would_block);

		spq_deliver_notices(conn);
		if (!would_block)
			return ready;
		if (!spq_wait_readable(conn))
			return 0;
	}
}

SPGresult *
spq_exec_finish(SPGconn *conn)
{
	for (;;)
	{
		int			would_block = 0;
		SPGresult  *res = spqgo_exec_finish(conn->handle, conn, &would_block);

		spq_deliver_notices(conn);
		if (!would_block)
			return res;
		if (!spq_wait_readable(conn))
			return NULL;
	}
}

SPGresult *
SPQexec(SPGconn *conn, const char *query)
{
	if (!spq_conn_usable(conn))
		return NULL;
	if (!spq_exec_start(conn))
		return NULL;
	if (!spqgo_send_query(conn->handle, (char *) query))
		return NULL;
	return spq_exec_finish(conn);
}
```

Every other blocking function is `SPQexec` with a different send call.

### 6.2 Connect and status (`conn.c`, `connect_async.c`, `version.c`)

```c
SPGconn *
SPQconnectdb(const char *conninfo)
{
	SPGconn    *conn = spq_conn_alloc();

	if (conn == NULL)
		return NULL;
	if (spq_in_child())
		return conn;			/* handle 0: SPQ_CONNECTION_BAD, section 7.1 */
	spqgo_connect(conn, (char *) (conninfo != NULL ? conninfo : ""));
	spq_deliver_notices(conn);
	return conn;
}
```

| Function | Unusable / NULL | Algorithm |
|---|---|---|
| `SPQconnectdb` ◆ | — (never NULL unless out of memory) | above |
| `SPQconnectdbParams` ◆, `SPQconnectStartParams` ◆ | as `SPQconnectdb` | same shape with `spqgo_connect_params(conn, (char **) keywords, (char **) values, expand_dbname, start)`; a NULL `keywords` is an empty list |
| `SPQconnectStart` ◆ | as `SPQconnectdb` | same shape with `spqgo_connect_start` |
| `SPQconnectPoll` | `SPGRES_POLLING_FAILED` | `spqgo_connect_poll` |
| `SPQfinish` | NULL: nothing | if usable: `spqgo_finish(conn->handle)`; then `spq_discard_notices`, free `str[]`, the `params` list and the object. In a forked child only the C memory is freed. |
| `SPQreset` ◆ | nothing | `spqgo_reset(conn->handle, conn)` |
| `SPQping`, `SPQpingParams` | in a forked child `SPQPING_NO_ATTEMPT` | `spqgo_ping`, `spqgo_ping_params`; NULL conninfo is `""` |
| `SPQstatus` | `SPQ_CONNECTION_BAD` | `spqgo_conn_int(h, SPQ_INT_STATUS)` |
| `SPQtransactionStatus` | `SPQTRANS_UNKNOWN` | `spqgo_conn_int(h, SPQ_INT_TRANSACTION_STATUS)` |
| `SPQerrorMessage` | NULL connection: `"connection pointer is NULL\n"`; other unusable: `spq_child_message` | cached string `SPQ_STR_ERROR`; when Go returns NULL: `"out of memory\n"` |
| `SPQdb`, `SPQuser`, `SPQpass`, `SPQhost`, `SPQport`, `SPQoptions` | NULL | cached string of the matching `enum spq_str` |
| `SPQparameterStatus` | NULL; also NULL when `paramName` is NULL | `spqgo_parameter_status`; store in the `params` list (4.2); NULL when the parameter is unknown |
| `SPQprotocolVersion`, `SPQserverVersion`, `SPQbackendPID` | 0 | `spqgo_conn_int` |
| `SPQsocket` | -1 | `conn->sock` (no Go call) |
| `SPQlibVersion` | — | returns 180006 |
| `SPQisthreadsafe` | — | returns 1 |
| `SPQXversion` | — | returns `"0.1.0"` |

The cached-string accessor used by seven functions:

```c
static char *
spq_conn_string(const SPGconn *cconn, enum spq_str which)
{
	SPGconn    *conn = (SPGconn *) cconn;
	char	   *value;

	if (!spq_conn_usable(conn))
		return NULL;
	value = spqgo_conn_string(conn->handle, which);
	free(conn->str[which]);
	conn->str[which] = value;
	return value;
}
```

`spq` returns Go strings, so an accessor of a usable connection returns `""` where
libpq would return NULL for a value that was never set (for example `SPQdb` of a
connection whose conninfo did not parse). That difference is accepted.

### 6.3 Nonblocking execution (`query.c`, `async.c`, `extended.c`, `rowmodes.c`)

| Function | Unusable / NULL | Algorithm |
|---|---|---|
| `SPQsendQuery` | 0 | `spqgo_send_query` |
| `SPQsendQueryParams`, `SPQsendPrepare`, `SPQsendQueryPrepared` | 0 | the export of the same name, arguments passed through with `const` cast away |
| `SPQsendDescribePrepared`, `SPQsendDescribePortal` | 0 | `spqgo_send_describe(h, 'S' or 'P', (char *) name)`; a NULL name is the empty name |
| `SPQsendClosePrepared`, `SPQsendClosePortal` | 0 | `spqgo_send_close(h, 'S' or 'P', (char *) name)` |
| `SPQgetResult` ◆ | NULL | 6.1 |
| `SPQconsumeInput` ◆ | 0 | `spqgo_consume_input` |
| `SPQisBusy` ◆ | 0 | `spqgo_is_busy` |
| `SPQflush` | -1 | `spqgo_flush` |
| `SPQsetnonblocking` | -1 | -1 as well when `spqgo_conn_int(h, SPQ_INT_STATUS)` is `SPQ_CONNECTION_BAD` (libpq's result; `spq` itself always answers 0); else `spqgo_set_nonblocking` |
| `SPQisnonblocking` | 0 | `spqgo_is_nonblocking` |
| `SPQsetSingleRowMode`, `SPQsetChunkedRowsMode` | 0 | `spqgo_set_single_row_mode`, `spqgo_set_chunked_rows_mode` |

A failed send leaves its message in `SPQerrorMessage` (CLIENT 7.2).

### 6.4 Results (`result.c`, `accessors.c`)

No function of this section enters Go, and none checks the fork flag.

| Function | NULL result | Otherwise |
|---|---|---|
| `SPQresultStatus` | `SPGRES_FATAL_ERROR` | `res->status` |
| `SPQresStatus(status)` | — | a static table of the 13 strings `PGRES_EMPTY_QUERY` … `PGRES_TUPLES_CHUNK` in the order of 2.2 (libpq's spellings, unchanged); `"invalid ExecStatusType code"` when `(unsigned) status >= 13` |
| `SPQntuples`, `SPQnfields`, `SPQbinaryTuples`, `SPQnparams` | 0 | the member |
| `SPQfname(res, f)` | NULL | field check; `fields[f].name` |
| `SPQfnumber(res, name)` | -1 | below |
| `SPQftable`, `SPQftype` | `SPQInvalidOid` | field check (failure: `SPQInvalidOid`); the member |
| `SPQftablecol`, `SPQfformat`, `SPQfsize`, `SPQfmod` | 0 | field check (failure: 0); the member |
| `SPQgetvalue(res, t, f)` | NULL | tuple-field check (failure: NULL); `cells[t * nfields + f].value` |
| `SPQgetlength` | 0 | tuple-field check (failure: 0); `len`, or 0 for a NULL value |
| `SPQgetisnull` | 1 | tuple-field check (failure: 1); `len < 0` |
| `SPQparamtype(res, p)` | `SPQInvalidOid` | parameter check (failure: `SPQInvalidOid`); `paramTypes[p]` |
| `SPQcmdStatus` | NULL | `res->cmdStatus` |
| `SPQcmdTuples` | `""` | when `!cmdTuplesOk`: `spq_internal_notice(&res->hooks, "could not interpret result from server: %s", res->cmdStatus)` and `""`; else `res->cmdTuples` |
| `SPQoidValue` | `SPQInvalidOid` | `res->oidValue` |
| `SPQresultErrorMessage` | `""` | `res->errMsg`, or `""` when it is NULL |
| `SPQresultErrorField(res, code)` | NULL | the value of the first entry of `errFields` whose code equals `code`, else NULL |
| `SPQresultMemorySize` | 0 | `res->memsize` |
| `SPQclear` | nothing | 4.4 |
| `SPQfreemem(ptr)` | — | `free(ptr)` |

Range checks, with libpq's notices sent through the hooks the result was created with:

| Check | Fails when | Notice (`spq_internal_notice(&res->hooks, ...)`) |
|---|---|---|
| field | `f < 0 \|\| f >= nfields` | `column number %d is out of range 0..%d` with `f`, `nfields - 1` |
| tuple-field | `t < 0 \|\| t >= ntups`, then the field check | `row number %d is out of range 0..%d` with `t`, `ntups - 1` |
| parameter | `p < 0 \|\| p >= nparams` | `parameter number %d is out of range 0..%d` with `p`, `nparams - 1` |

`SPQfnumber(res, name)`, in C (the rule of CLIENT 7.6): -1 when `name` is NULL or
empty or the result has no fields. If `name` contains neither `"` nor an upper-case
ASCII letter, compare it with each field name by `strcmp` and return the first match.
Otherwise fold a copy: outside double quotes lower-case ASCII letters; drop the
quotes; inside quotes `""` stands for one `"`; then compare the folded copy the same
way. -1 when nothing matches or memory runs out.

### 6.5 Blocking execution (`exec.c`, `extended.c`)

| Function | Unusable / NULL | Send step between `spq_exec_start` and `spq_exec_finish` |
|---|---|---|
| `SPQexec` | NULL | `spqgo_send_query` |
| `SPQexecParams` | NULL | `spqgo_send_query_params` |
| `SPQprepare` | NULL | `spqgo_send_prepare` |
| `SPQexecPrepared` | NULL | `spqgo_send_query_prepared` |
| `SPQdescribePrepared`, `SPQdescribePortal` | NULL | `spqgo_send_describe` |
| `SPQclosePrepared`, `SPQclosePortal` | NULL | `spqgo_send_close` |

Each returns NULL when the start step or the send step fails (the text is in
`SPQerrorMessage`), as libpq does.

### 6.6 Errors and notices (`errors.c`)

| Function | Unusable / NULL | Algorithm |
|---|---|---|
| `SPQresultVerboseErrorMessage(res, v, cv)` | — | NULL result or status neither `SPGRES_FATAL_ERROR` nor `SPGRES_NONFATAL_ERROR`: `strdup("PGresult is not an error result\n")`. No error fields: `strdup` of `errMsg`, or of `"no error message available\n"` when `errMsg` is NULL or empty. Otherwise `spqgo_format_error(res, v, cv)`; in a forked child NULL. The caller frees with `SPQfreemem`. |
| `SPQsetErrorVerbosity` | `SPQERRORS_DEFAULT` | `spqgo_set_error_verbosity`; returns the old value |
| `SPQsetErrorContextVisibility` | `SPQSHOW_CONTEXT_ERRORS` | `spqgo_set_error_context_visibility` |
| `SPQsetNoticeReceiver(conn, proc, arg)` | NULL when `conn` is NULL | pure C on `conn->hooks`, also for an unusable non-NULL connection: `old = hooks.noticeRec`; when `proc` is not NULL store `proc` and `arg`; return `old` |
| `SPQsetNoticeProcessor(conn, proc, arg)` | NULL when `conn` is NULL | the same on `noticeProc` / `noticeProcArg` |

The setters never return NULL for a real connection, because the defaults of 4.3 are
installed at allocation. A result keeps the hooks of the moment it was created.

### 6.7 Escaping (`escape.c`)

All of them enter Go (the rules live in `spq`, CLIENT 9); in a forked child they fail
as listed under "Unusable".

| Function | Unusable / NULL | Algorithm |
|---|---|---|
| `SPQescapeStringConn(conn, to, from, length, error)` | `to[0] = '\0'`; `*error = 1` when `error` is not NULL; returns 0 | `n = strnlen(from, length)`; `spqgo_escape_string_conn(h, to, (char *) from, n, error)` |
| `SPQescapeString(to, from, length)` | in a forked child: `to[0] = '\0'`, 0 | `n = strnlen(from, length)`; `spqgo_escape_string` |
| `SPQescapeLiteral(conn, str, len)`, `SPQescapeIdentifier` | NULL | `n = strnlen(str, len)`; `spqgo_escape_literal(h, (char *) str, n, 0 or 1)`; NULL result means failure with the text in `SPQerrorMessage` |
| `SPQescapeByteaConn(conn, from, from_length, to_length)` | NULL | `spqgo_escape_bytea(h, (unsigned char *) from, from_length, to_length)` |
| `SPQescapeBytea(from, from_length, to_length)` | in a forked child NULL | `spqgo_escape_bytea(0, ...)` |
| `SPQunescapeBytea(strtext, retbuflen)` | NULL when `strtext` is NULL or in a forked child | `spqgo_unescape_bytea` |

`strnlen` in C implements "input ends at the first zero byte or at the given length"
before any byte is handed to Go, so Go never reads past a terminator. `*to_length`
counts the terminating NUL, as in libpq.

### 6.8 Connection options (`conninfo.c`)

| Function | Unusable / NULL | Algorithm |
|---|---|---|
| `SPQconninfoParse(conninfo, errmsg)` | in a forked child NULL (and `*errmsg = NULL`) | `if (errmsg) *errmsg = NULL;` then `spqgo_conninfo_parse((char *) (conninfo ? conninfo : ""), errmsg)` |
| `SPQconndefaults()` | in a forked child NULL | `spqgo_conndefaults()` |
| `SPQconninfo(conn)` | NULL | `spqgo_conninfo(conn->handle)` |
| `SPQconninfoFree(options)` | NULL: nothing | 4.6; pure C |

### 6.9 Cancel (`cancel.c`)

Pure C; the file includes no Go header.

```c
SPGcancel *
SPQgetCancel(SPGconn *conn)
{
	SPGcancel  *cancel;

	if (conn == NULL || conn->cancel_fd < 0 || spq_in_child())
		return NULL;
	cancel = malloc(sizeof(*cancel));
	if (cancel == NULL)
		return NULL;
	cancel->fd = fcntl(conn->cancel_fd, F_DUPFD_CLOEXEC, 0);
	if (cancel->fd < 0)
	{
		free(cancel);
		return NULL;
	}
	return cancel;
}

int
SPQcancel(SPGcancel *cancel, char *errbuf, int errbufsize)
{
	static const char no_object[] = "PQcancel() -- no cancel object supplied";
	int			saved_errno = errno;

	if (cancel == NULL)
	{
		if (errbuf != NULL && errbufsize > 0)
		{
			size_t		n = sizeof(no_object) - 1;

			if (n > (size_t) errbufsize - 1)
				n = (size_t) errbufsize - 1;
			memcpy(errbuf, no_object, n);
			errbuf[n] = '\0';
		}
		return 0;
	}
	if (!spq_in_child())
		(void) send(cancel->fd, "c", 1, MSG_NOSIGNAL | MSG_DONTWAIT);
	errno = saved_errno;
	return 1;
}

void
SPQfreeCancel(SPGcancel *cancel)
{
	if (cancel != NULL)
	{
		close(cancel->fd);
		free(cancel);
	}
}
```

`SPQcancel` calls only `send`, `memcpy` and reads one `sig_atomic_t`; it is
async-signal-safe and restores `errno`. It returns 1 whether or not anything was
running and also after `SPQfinish` (the peer is closed; `MSG_NOSIGNAL` prevents
`SIGPIPE`) **(measured)**. In a forked child it sends nothing, so a child cannot cancel
its parent's command **(measured)**. The message text is libpq's.

### 6.10 COPY (`copy.c`)

| Function | Unusable / NULL | Algorithm |
|---|---|---|
| `SPQputCopyData(conn, buffer, nbytes)` ◆ | -1 | `spqgo_put_copy_data` (may wait inside Go for queue space, F4) |
| `SPQputCopyEnd(conn, errormsg)` | -1 | `spqgo_put_copy_end` |
| `SPQgetCopyData(conn, buffer, async)` ◆ | `*buffer = NULL` when `buffer` is not NULL; -2 | below |

```c
int
SPQgetCopyData(SPGconn *conn, char **buffer, int async)
{
	if (buffer == NULL)
		return -2;
	*buffer = NULL;
	if (!spq_conn_usable(conn))
		return -2;
	for (;;)
	{
		int			rc = spqgo_get_copy_data(conn->handle, conn, buffer);

		spq_deliver_notices(conn);
		if (rc != 0 || async)
			return rc;
		if (!spq_wait_readable(conn))
			return -2;
	}
}
```

A positive result is the row length; `*buffer` holds the row and a terminating NUL and
is released with `SPQfreemem`.

### 6.11 Notifications and time (`notify.c`)

| Function | Unusable / NULL | Algorithm |
|---|---|---|
| `SPQnotifies(conn)` ◆ | NULL | `spqgo_notifies`; NULL when none is pending |
| `SPQsocketPoll(sock, forRead, forWrite, end_time)` | — | pure C, below |
| `SPQgetCurrentTimeUSec()` | — | pure C: `gettimeofday`; `tv_sec * 1000000 + tv_usec` as `spg_usec_time_t` |

`SPQsocketPoll`: return 0 at once when both `forRead` and `forWrite` are 0. Otherwise
`poll()` one descriptor for `POLLERR`, plus `POLLIN` when `forRead`, plus `POLLOUT` when
`forWrite`. Timeout: -1 (wait forever) when `end_time` is -1; 0 when `end_time` is 0 or
not later than now; else `(end_time - now) / 1000` milliseconds. Return what `poll`
returns: positive when the condition holds, 0 on timeout, -1 on error including
`EINTR` (no retry; the caller decides).

### 6.12 Client encoding (`encoding.c`)

| Function | Unusable / NULL | Algorithm |
|---|---|---|
| `SPQclientEncoding(conn)` | -1 | `spqgo_client_encoding` |
| `SPQsetClientEncoding(conn, encoding)` | -1; also -1 when `encoding` is NULL | `stmt = spqgo_client_encoding_statement((char *) encoding)`; NULL: -1. `res = SPQexec(conn, stmt)`; `free(stmt)`; result 0 when `res` is not NULL and its status is `SPGRES_COMMAND_OK`, else -1; `SPQclear(res)` |
| `spg_encoding_to_char(encoding)` | `""` for an id outside 0..63, or when the name is not cached in a forked child | static cache, below |
| `spg_char_to_encoding(name)` | -1 for NULL or in a forked child | `spqgo_char_to_encoding` |

`spg_encoding_to_char` must return a pointer that stays valid forever. A static array
of 64 `char *` slots, all NULL at start: load the slot with
`__atomic_load_n(&slot, __ATOMIC_ACQUIRE)`; when NULL, get the name from
`spqgo_encoding_to_char(encoding)` (a `malloc` string, `""` for an id without a name)
and install it with `__atomic_compare_exchange_n`; if another thread won the race,
free the new string and use the installed one. A NULL from Go (out of memory) returns
`""` without caching.

### 6.13 Extensions (`control.c`, `dump.c`)

| Function | Unusable / NULL | Algorithm |
|---|---|---|
| `SPQXclusterDrop(host, port, errbuf, errbufsize)` | in a forked child: 0 with `spq_child_message` (without its newline) in `errbuf` | `msg = spqgo_cluster_drop((char *) host, (char *) port)`; NULL: 1. Else copy `msg` to `errbuf` as 3.2 says, `free(msg)`, 0 |
| `SPQXreset(conn)` | 0 | `spqgo_reset_database` |
| `SPQXdump(conn, buffer, length)` | `*buffer = NULL`, `*length = 0` (each when not NULL); 0; also 0 when `buffer` is NULL | `spqgo_dump(h, buffer, length)` |
| `SPQXrestore(conn, buffer, length)` | 0 | `spqgo_restore(h, (char *) buffer, length)` |
## 7. Process and thread rules as implemented

### 7.1 Fork guard (`guard.c`)

```c
#include <pthread.h>
#include <signal.h>

#include "spq_int.h"

const char	spq_child_message[] =
"libspq cannot be used in a process forked after the library was loaded\n";

static volatile sig_atomic_t spq_forked;

static void
spq_atfork_child(void)
{
	spq_forked = 1;
}

__attribute__((constructor))
static void
spq_guard_init(void)
{
	pthread_atfork(NULL, NULL, spq_atfork_child);
}

int
spq_in_child(void)
{
	return spq_forked != 0;
}
```

The constructor runs when the library is loaded, which is also when the Go runtime
starts. After `fork()` the child has only the forking thread; the Go scheduler threads
are gone, so any call into Go would hang. The handler sets the flag in the child
before `fork()` returns there. A child that loads the library itself runs the
constructor fresh and is not affected. `vfork` and raw `clone` do not run the handler
and are outside the contract.

In a forked child **(measured with a program that connects, forks and exercises the
child)**:

| Call | Behavior |
|---|---|
| `SPQconnectdb` and the other connect functions | return an object built in C: status `SPQ_CONNECTION_BAD`, `SPQerrorMessage` = `spq_child_message`, `SPQsocket` -1 |
| any function taking a connection, including one inherited from the parent | the "Unusable" result of section 6; `SPQerrorMessage` = `spq_child_message` |
| `SPQfinish` | frees the C memory of the object; the session and descriptors belong to the parent and are untouched |
| result accessors, `SPQclear`, `SPQfreemem`, `SPQconninfoFree`, `SPQfreeCancel`, `SPQsocketPoll`, `SPQgetCurrentTimeUSec`, `SPQlibVersion`, `SPQXversion`, `SPQresStatus` | work normally (pure C) |
| `SPQcancel` | returns 1 and sends nothing |
| functions without a connection that need Go (`SPQping`, `SPQconninfoParse`, `SPQconndefaults`, escaping without a connection, `spg_char_to_encoding`, `SPQXclusterDrop`) | their failure value (section 6) |

The parent is unaffected by anything the child does.

### 7.2 Threads

- One thread at a time per connection (docs/LIBSPQ.md rule 5). The C object is not
  locked. Different connections are independent: they share only the cluster registry
  inside `spq`, which has its own locks **(measured: four threads, 20,000 calls each
  on their own connections)**.
- `SPQcancel` may run concurrently with anything, on any thread or in a signal handler.
  It touches only its own `SPGcancel` object.
- `SPQsocket` reads one `int` that never changes after connect.
- Results are immutable; any number of threads may read one. `SPQclear` must not race
  with readers.
- A connection has no thread affinity: consecutive calls may come from different
  threads. `runtime/cgo.Handle` values are plain integers valid on every thread.

### 7.3 Callbacks

Notice receivers and processors are called by `spq_deliver_notices` and
`spq_internal_notice`, in C, on the thread that called the public function, after any
Go call has returned **(measured: the receiver ran on the calling thread and could
itself execute a statement on another connection)**. A callback must return normally.
It must not call a function of the same connection; the queue is nevertheless
consistent if it does (4.3).

### 7.4 Lifetime

- `SPQfinish` deletes the handle; the pointer is dead afterwards, as in libpq.
- Results, `SPGcancel` objects, notifications, option arrays and returned buffers are
  independent of the connection that produced them.
- The library is linked with `NODELETE` by the Go toolchain **(measured)**: it is never
  unloaded, so cached strings and the encoding-name cache live until the process ends.
## 8. Build

### 8.1 Tags and flags

`main.go`:

```go
//go:build libspq && cgo && linux

// Command libspq is the cgo shim of the libspq C library (docs/LIBSPQ.md). It is
// built only by `make libspq`, with -tags libspq and -buildmode=c-shared or
// c-archive; without the tag the directory contains no Go files.
package main

/*
#cgo CFLAGS: -std=c11 -D_GNU_SOURCE -Wall -Wextra -Werror -fvisibility=hidden -I${SRCDIR}/include
*/
import "C"

func main() {}
```

- The `#cgo CFLAGS` line applies to every C file and cgo-generated file of the
  package. With go 1.27.1 and gcc 13.3 the generated files compile cleanly under
  `-Wall -Wextra -Werror` **(measured)**. `-D_GNU_SOURCE` makes `strnlen`,
  `F_DUPFD_CLOEXEC`, `MSG_NOSIGNAL` and `pthread_atfork` visible under `-std=c11`; C
  files do not define feature macros themselves.
- `-fvisibility=hidden` hides the `spqgo_*` wrappers and the `spq_*` helpers; the
  public functions are visible because their prototypes carry `SPQ_API`. The cgo
  runtime still exports about fifty symbols of its own (`_cgo_*`, `x_cgo_*`,
  `crosscall2`, `fatalf`, `threadentry`, `_cgoexp_*`) **(measured)**; the version
  script removes them.
- **(measured)** With the tag absent: `go list ./...` does not list the package;
  `go build ./...`, `go vet ./...` and `go test ./...` skip it, with `CGO_ENABLED` 0 or
  1; naming the package explicitly fails with "build constraints exclude all Go files".
  With the tag and `CGO_ENABLED=0` it is skipped as well.

### 8.2 Version script (`exports.map`)

```
{
	global:
		SPQ*;
		spg_*;
	local:
		*;
};
```

An anonymous script: exported symbols are unversioned. **(measured)**
`nm -D --defined-only libspq.so` then lists the public functions and nothing else.

### 8.3 Makefile

Appended to the root `Makefile` **(measured: this text built the library and ran the
tests in the scratch tree)**:

```make
# libspq: C bindings over the embedded engine (docs/LIBSPQ.md). These targets need
# cgo and a C compiler; libspq-check also needs g++. They are not part of quickcheck.
LIBSPQ_OUT = build/libspq
LIBSPQ_SRC = bindings/libspq
LIBSPQ_TESTS = $(LIBSPQ_SRC)/tests
LIBSPQ_LDFLAGS = -extldflags=-Wl,--version-script=$(CURDIR)/$(LIBSPQ_SRC)/exports.map,-soname,libspq.so
LIBSPQ_TEST_OBJS = $(patsubst $(LIBSPQ_TESTS)/%.cpp,$(LIBSPQ_OUT)/obj/%.o,$(wildcard $(LIBSPQ_TESTS)/*.cpp))

# Build the shared library, the static archive and the public headers.
libspq:
	mkdir -p $(LIBSPQ_OUT)/lib $(LIBSPQ_OUT)/include
	CGO_ENABLED=1 go build -tags libspq -buildmode=c-shared -ldflags='$(LIBSPQ_LDFLAGS)' -o $(LIBSPQ_OUT)/lib/libspq.so ./$(LIBSPQ_SRC)
	CGO_ENABLED=1 go build -tags libspq -buildmode=c-archive -o $(LIBSPQ_OUT)/lib/libspq.a ./$(LIBSPQ_SRC)
	rm -f $(LIBSPQ_OUT)/lib/libspq.h
	cp $(LIBSPQ_SRC)/include/*.h $(LIBSPQ_OUT)/include/

$(LIBSPQ_OUT)/obj/%.o: $(LIBSPQ_TESTS)/%.cpp $(wildcard $(LIBSPQ_TESTS)/*.h) $(wildcard $(LIBSPQ_SRC)/include/*.h)
	mkdir -p $(LIBSPQ_OUT)/obj
	g++ -std=c++17 -Wall -Wextra -Werror -I $(LIBSPQ_SRC)/include -I $(LIBSPQ_TESTS) -I $(LIBSPQ_TESTS)/third_party -c $< -o $@

# Build the library and run its C and C++ tests.
libspq-check: libspq $(LIBSPQ_TEST_OBJS)
	sh $(LIBSPQ_TESTS)/check_symbols.sh $(LIBSPQ_OUT)/lib/libspq.so $(LIBSPQ_OUT)/include
	for f in $(LIBSPQ_TESTS)/syntax/*.c; do cc -std=c11 -pedantic -Wall -Wextra -Werror -I $(LIBSPQ_OUT)/include -fsyntax-only $$f || exit 1; done
	cc -shared -fPIC -o $(LIBSPQ_OUT)/obj/libpqstub.so $(LIBSPQ_TESTS)/coexist/pqstub.c
	cc -std=c11 -Wall -Wextra -Werror -I $(LIBSPQ_OUT)/include $(LIBSPQ_TESTS)/coexist/coexist.c -L $(LIBSPQ_OUT)/obj -lpqstub -L $(LIBSPQ_OUT)/lib -lspq -Wl,-rpath,$(CURDIR)/$(LIBSPQ_OUT)/obj -Wl,-rpath,$(CURDIR)/$(LIBSPQ_OUT)/lib -o $(LIBSPQ_OUT)/obj/coexist
	$(LIBSPQ_OUT)/obj/coexist
	g++ $(LIBSPQ_TEST_OBJS) -L $(LIBSPQ_OUT)/lib -lspq -lpthread -Wl,-rpath,$(CURDIR)/$(LIBSPQ_OUT)/lib -o $(LIBSPQ_OUT)/spq_tests
	$(LIBSPQ_OUT)/spq_tests
```

Also:

- `libspq` and `libspq-check` are added to the `.PHONY` line.
- `/build/` is added to `.gitignore`.
- The two dependency listings (targets `finalcheck` and `deps`) become
  `$(LIST_DEPS) . ./dual ./pgserver/... ./internal/... ./bindings/...` followed by a new
  line `$(LIST_DEPS) -tags libspq ./bindings/...`. **(measured)** Without the tag the
  pattern lists only `stuntgres/bindings/libspq/spq` (or prints the harmless warning
  `matched no packages` while that package does not exist yet); with the tag it also
  lists `stuntgres/bindings/libspq`. Both stay inside the module, as the dependency
  rule requires. `go vet -tags libspq ./bindings/libspq` works and is clean.
- Neither target is added to `quickcheck`.

`go build` writes a cgo header `libspq.h` next to each artifact; it describes the
hidden `spqgo_*` functions and is deleted. Each new test file is picked up by the
wildcards; no part after the skeleton edits the Makefile.

Facts for planning **(measured, with the whole engine linked in)**: a cold `c-shared`
build takes about 30 s on the 32-core host and about 2 s when cached; `libspq.so` is
94 MB (75 MB with `-ldflags=-s -w`, which is not used so that Go tracebacks stay
readable); `libspq.a` is 157 MB; loading the library, creating an engine and running
the first call takes 33 ms. The soname is `libspq.so`: there is no versioned file name
while the ABI is at 0.x.

### 8.4 Tests are built against the sources' headers

Test objects are compiled with `-I bindings/libspq/include`, so they do not depend on
the copy step; the syntax checks and the coexistence program use the installed copies
in `build/libspq/include` to prove those are complete.

### 8.5 The vendored doctest header

- Upstream release **v2.4.11** of https://github.com/doctest/doctest, file
  `doctest/doctest.h`, stored as `bindings/libspq/tests/third_party/doctest.h`
  (321,644 bytes), SHA-256
  `44faa038e9c3f9728efbda143748d01124ea0a27f4bf78f35a15d8fab2e039fb`. On this host
  `/usr/include/doctest/doctest.h` is byte-identical **(measured)**, so it can be
  copied from there; otherwise fetch
  `https://raw.githubusercontent.com/doctest/doctest/v2.4.11/doctest/doctest.h`.
- Its licence (MIT), upstream file `LICENSE.txt` of the same tag, stored as
  `bindings/libspq/tests/third_party/LICENSE.doctest.txt`, SHA-256
  `0fe0b331fa1513dcce8604ff1fa925f32d1cea17d8aeb1c2471fad40d291adc5`
  (`https://raw.githubusercontent.com/doctest/doctest/v2.4.11/LICENSE.txt`).
- Verify with `sha256sum`; never edit either file.
## 9. Tests

The C++ suite tests the C ABI: states, ownership and lifetime, NULL arguments,
ordering, error reporting, the descriptor contract, threads and fork. It is the one
sanctioned native-only suite that issues SQL through a public API (owner decision,
docs/LIBSPQ.md section 9); SQL in it exists only to produce protocol shapes
(`SELECT 1`, `generate_series`, `pg_sleep`, `CREATE TABLE`, `INSERT`, `COPY`,
`LISTEN`/`NOTIFY`, `SET`, `BEGIN`/`ROLLBACK`, `DO $$ BEGIN RAISE NOTICE ... END $$`,
`pg_terminate_backend`). Behavior of the state machine itself is tested in Go
(CLIENT 11); these tests check that the C layer transports it faithfully.

Rules:

- C++17, doctest, one binary `build/libspq/spq_tests`; `tests/main.cpp` is

  ```cpp
  #define DOCTEST_CONFIG_IMPLEMENT_WITH_MAIN
  #include "doctest.h"
  ```

- One file `tests/test_<family>.cpp` per family, owned by the part of that family.
- Test case names start with the family (`"connect: ..."`), so
  `build/libspq/spq_tests --test-case='connect:*'` selects one family.
- Every test case uses its own cluster and never sleeps to synchronize: it waits on
  `SPQsocket` or uses a blocking call. Waits are bounded (ten seconds) so a defect
  fails the test instead of hanging it.
- A part writes its test cases first and sees them fail to link or fail to pass before
  implementing (rule L7).

### 9.1 `tests/spqtest.h` (created by LIBSPQ-CABI-01-CONNECT)

```cpp
#pragma once

#include <poll.h>
#include <string>

#include "doctest.h"
#include "libspq-fe.h"
#include "libspq-ext.h"

// The drop function arrives with LIBSPQ-CABI-14-CONTROL. A weak reference lets
// this header use it as soon as the library has it and not before.
extern "C" int SPQXclusterDrop(const char *host, const char *port, char *errbuf,
                               int errbufsize) __attribute__((weak));

namespace spqtest {

// A cluster of its own for one test case. Declare it before the connections that
// use it, so they are finished first and the drop at scope end succeeds.
struct Cluster {
    std::string host;
    explicit Cluster(const char *name) : host(std::string("t-") + name) {}
    ~Cluster() {
        char errbuf[256];
        if (SPQXclusterDrop) SPQXclusterDrop(host.c_str(), "5432", errbuf, sizeof errbuf);
    }
    std::string conninfo(const char *rest = "dbname=app user=alice") const {
        return "host=" + host + " " + rest;
    }
};

struct Conn {
    SPGconn *c;
    explicit Conn(const Cluster &cluster, const char *rest = "dbname=app user=alice")
        : c(SPQconnectdb(cluster.conninfo(rest).c_str())) {}
    ~Conn() { if (c) SPQfinish(c); }
    Conn(const Conn &) = delete;
    Conn &operator=(const Conn &) = delete;
    operator SPGconn *() const { return c; }
};

struct Result {
    SPGresult *r;
    explicit Result(SPGresult *res) : r(res) {}
    ~Result() { SPQclear(r); }
    Result(const Result &) = delete;
    Result &operator=(const Result &) = delete;
    operator SPGresult *() const { return r; }
};

// Waits until the readiness descriptor is readable; false after timeout_ms.
inline bool wait_readable(SPGconn *c, int timeout_ms = 10000) {
    pollfd p{SPQsocket(c), POLLIN, 0};
    return poll(&p, 1, timeout_ms) == 1;
}
inline bool is_readable(SPGconn *c) { return wait_readable(c, 0); }

}  // namespace spqtest
```

**(measured)** A weak reference to a function the library does not export links and
evaluates to null; once the library exports it, the same binary source calls it.

### 9.2 Files and cases

Each line is one `TEST_CASE`; the text after the colon is what it must assert.
"notice capture" means a receiver installed with `SPQsetNoticeReceiver` that appends
`SPQresultErrorMessage(res)` to a `std::vector<std::string>` passed as `arg`.

| File (part) | Test cases |
|---|---|
| `test_build.cpp` (BUILD-01) | `build: versions`: `SPQlibVersion() == 180006`, `SPQisthreadsafe() == 1`, `SPQXversion()` equals `"0.1.0"`. |
| `syntax/build_c11.c` (BUILD-01) | includes `libspq-fe.h` and `libspq-ext.h`; one function that calls `SPQlibVersion()`. |
| `coexist/pqstub.c`, `coexist/coexist.c` (BUILD-01) | the stub library defines `PQlibVersion` (returns 42), `PQexec` and a function named `fatalf`; the program calls `SPQlibVersion()`, `PQlibVersion()` and `fatalf()` and exits 0 only if they return 180006, 42 and the stub's value. |
| `test_headers.cpp` (HEADER-01) | `static_assert` for every enumerator value of 2.2 and every macro value of 2.3; `offsetof` order and member types of `SPGnotify` and `SPQconninfoOption`; `sizeof(SPQOid) == 4`; `headers: constants`: a runtime `CHECK` of `SPQInvalidOid == 0` and `SPQ_QUERY_PARAM_MAX_LIMIT == 65535`. |
| `syntax/headers_c11.c`, `syntax/compat_c11.c` (HEADER-01) | the first includes `libspq-fe.h` and `libspq-ext.h` and takes the address of every one of the 104 + 5 functions into a `void (*)(void)` array (proves each is declared); the second includes only `libspq-compat.h` and compiles a function written with libpq names (`PGconn`, `PQexec`, `PQresultStatus`, `PGRES_TUPLES_OK`, `CONNECTION_OK`, `InvalidOid`, `PQclear`, `LIBPQ_HAS_CHUNK_MODE`). |
| `test_connect.cpp` (CABI-01) | `connect: ok`: status OK, `SPQerrorMessage` is `""`, `SPQdb`/`SPQuser`/`SPQhost`/`SPQport` echo the conninfo, `SPQpass` is `""`, `SPQsocket >= 0`, `SPQbackendPID >= 2`, `SPQprotocolVersion == 3`, `SPQserverVersion == 180000`, `SPQtransactionStatus == SPQTRANS_IDLE`. `connect: parameter status`: `server_version` and `client_encoding` (`UTF8`) present, unknown name NULL, NULL name NULL. `connect: bad conninfo`: `"nonsense"` gives a non-NULL object with status BAD, message `missing "=" after "nonsense" in connection info string\n`, `SPQsocket == -1`, and `SPQfinish` works. `connect: rejected by the engine`: `options=-c no_such_setting=1` gives BAD and a message starting `connection to server at "` and containing `failed: FATAL:`. `connect: null connection`: every function of 6.2 with NULL returns the listed value. `connect: strings stay valid`: the pointer from `SPQdb` still reads the same text after `SPQuser` and `SPQhost` were called. `connect: two clusters`: two hosts give different objects with the same backend PID numbering starting at 2. `connect: finish null`: `SPQfinish(NULL)` returns. |
| `test_fork.cpp` (CABI-01) | `fork: child is refused`: the parent connects; the child (which must end with `_exit`, with `alarm(10)` as a hang guard) checks that a new connection is BAD with `spq_child_message`'s text, that `SPQstatus` of the inherited connection is BAD, that `SPQfinish` of both returns; exit code 0. The parent then checks the child's exit status and that its own connection still reports OK. Later parts append cases here: (02A) results from the parent are readable and clearable in the child and `SPQexec` returns NULL there; (08) `SPQcancel` in the child returns 1 and the parent's running statement is not cancelled. |
| `test_result.cpp` (CABI-02A) | `result: values`: `SELECT 'a', NULL::text, ''` gives lengths 1, 0, 0, `SPQgetisnull` 0, 1, 0, values `"a"`, `""`, `""`, and the NULL value's pointer is not the empty value's pointer. `result: several rows`: `generate_series(1,1000)` values and terminators. `result: survives finish`: values readable after `SPQfinish`. `result: clear null`. `result: error`: a failing statement gives `SPGRES_FATAL_ERROR` and a message starting `ERROR:  `. `result: command status`: `CREATE TABLE` gives `"CREATE TABLE"`, a select of three rows `"SELECT 3"`. `result: null result`: `SPQresultStatus(NULL) == SPGRES_FATAL_ERROR`, `SPQntuples(NULL) == 0`, `SPQgetvalue(NULL,0,0) == NULL`, `SPQresultErrorMessage(NULL)` is `""`, `SPQcmdStatus(NULL) == NULL`. |
| `test_query.cpp` (CABI-02A) | `query: one result per statement`: `SELECT 1; SELECT 2` gives two `TUPLES_OK` then NULL, and a second `SPQgetResult` gives NULL again. `query: empty`: `""` gives `SPGRES_EMPTY_QUERY`. `query: error stops the batch`. `query: busy`: a second `SPQsendQuery` before collecting returns 0 with `another command is already in progress\n`. `query: null query`: 0 with `command string is a null pointer\n`. `query: large`: 100,000 rows arrive complete (exercises the wait loop and back-pressure). `query: startup notice is delivered`: `application_name` of 70 characters gives one captured notice (needs notice capture: use the default hooks and only assert the connection is OK until part 05 adds the receiver; part 05 appends the capturing form). |
| `test_accessors.cpp` (CABI-02) | `accessors: fields`: for `SELECT id, name AS "Mixed" FROM t`: `SPQfname`, `SPQfnumber("id")`, `SPQfnumber("ID")` (folded), `SPQfnumber("\"Mixed\"")`, `SPQfnumber("mixed") == -1`, `SPQftable != 0` and `SPQftablecol` 1 and 2, `SPQftype` 23 and 25, `SPQfsize` 4 and -1, `SPQfformat` 0, `SPQbinaryTuples` 0; for an expression column `SPQftable == 0`. `accessors: command tuples`: `INSERT` of two rows gives `"2"`, `UPDATE`, `DELETE`, `SELECT`, and `CREATE TABLE` gives `""`; `SPQoidValue == 0`. `accessors: res status`: all 13 names and the invalid text. `accessors: memory size`: positive, larger for more rows, 0 for NULL. `accessors: null result`: every function of 6.4. `accessors: freemem null`. |
| `test_async.cpp` (CABI-02) | `async: descriptor`: not readable while idle, always writable (`poll` with `POLLOUT`), readable after `SPQsendQuery` once the result is there; after collecting everything not readable again. `async: consume and busy`: `pg_sleep(0.2)`: `SPQisBusy` 1 right after sending; after `wait_readable` and `SPQconsumeInput`, `SPQisBusy` 0 and `SPQgetResult` does not wait. `async: flags`: `SPQflush == 0`, `SPQsetnonblocking(1) == 0`, `SPQisnonblocking == 1`. `async: null connection`: values of 6.3. |
| `test_exec.cpp` (CABI-03) | `exec: last result`: `SELECT 1; SELECT 2` returns the second. `exec: error text accumulates`. `exec: discards pending`: after an uncollected `SPQsendQuery`, `SPQexec` returns its own result. `exec: transaction status`: `BEGIN` gives INTRANS, a failing statement INERROR, `ROLLBACK` IDLE. `exec: two connections share data`. `exec: threads`: four `std::thread`s, each with its own connection to one cluster, 500 statements each. `exec: connection loss`: `pg_terminate_backend` from a second connection; the victim's next `SPQexec` gives a FATAL result, then status BAD and `no connection to the server\n`. `exec: null`: `SPQexec(NULL, ...) == NULL`. |
| `test_extended.cpp` (CABI-04) | `extended: text parameters`. `extended: binary parameter and result`: int4 as four network-order bytes with `paramFormats` 1 and `resultFormat` 1; `SPQbinaryTuples == 1`, length 4. `extended: null parameter`. `extended: parameter types`. `extended: missing binary length`: 0/NULL with `length must be given for binary parameter\n`. `extended: parameter count`: -1 and 65536 give `number of parameters must be between 0 and 65535\n`. `extended: prepare and execute twice`. `extended: describe prepared`: `SPGRES_COMMAND_OK` with `SPQnparams`, `SPQparamtype`, `SPQnfields`; `SPQparamtype` out of range is `SPQInvalidOid`. `extended: describe portal` of a cursor inside a transaction. `extended: close prepared`: executing it afterwards fails. `extended: several statements rejected`. `extended: send forms`: each send function followed by a `SPQgetResult` loop. `extended: error leaves the connection usable`. |
| `test_errors.cpp` (CABI-05) | `errors: fields`: SQLSTATE `42601`, severity `ERROR`, primary message, statement position; unknown code NULL; NULL result NULL. `errors: verbose message`: the terse, default, verbose and sqlstate forms of one error (CLIENT 8.3 layout), freed with `SPQfreemem`; a non-error result gives `PGresult is not an error result\n`. `errors: verbosity setters`: return the old value; the next error message changes form. `errors: receiver`: called once per `RAISE NOTICE`, with status `SPGRES_NONFATAL_ERROR`, on the calling thread (`std::this_thread::get_id()`), before `SPQexec` returns; its `arg` is passed through. `errors: processor`: with the default receiver a custom processor gets `NOTICE:  hi\n`. `errors: setters return the previous hook` and ignore a NULL function. `errors: internal notice`: `SPQgetvalue(res, 5, 0)` on three rows captures `row number 5 is out of range 0..2\n`; a column and a parameter out of range likewise. `errors: results keep their hooks`. `errors: null connection`. |
| `test_escape.cpp` (CABI-06) | `escape: literal`: `O'Reilly` gives `'O''Reilly'`; `a\b` gives ` E'a\\b'`. `escape: identifier`. `escape: string conn`: length and `*error == 0`; invalid UTF-8 gives `*error == 1` and a message. `escape: string static`. `escape: bytea conn`: `\x00ff…`, `*to_length` counts the NUL. `escape: bytea static`. `escape: unescape`: hex and escape forms, NULL input. `escape: input stops at NUL`: a length larger than the string. `escape: null connection`. |
| `test_conninfo.cpp` (CABI-07) | `conninfo: parse`: values found by keyword; 50 entries then a NULL keyword. `conninfo: parse error`: NULL result and `*errmsg` equal to the message plus newline, freed with `SPQfreemem`; a NULL `errmsg` argument is accepted. `conninfo: defaults`: `port` is `5432`, `user` is `postgres`. `conninfo: of a connection`: `host`, `port`, `dbname`, `user` as resolved. `conninfo: free null`. |
| `test_cancel.cpp` (CABI-08) | `cancel: running statement`: a second thread calls `SPQcancel` while `SELECT pg_sleep(30)` runs; the result has SQLSTATE `57014`; the connection then runs `SELECT 1`. `cancel: idle has no effect`. `cancel: null object`: 0 and the text `PQcancel() -- no cancel object supplied`; a buffer of 8 bytes gets the first 7 characters. `cancel: after finish`: returns 1, no signal. `cancel: from a signal handler`: `SIGALRM` handler calls `SPQcancel`. `cancel: free null`. |
| `test_copy.cpp` (CABI-09) | `copy: in`: status `SPGRES_COPY_IN`, `SPQnfields` and `SPQbinaryTuples`, data in three pieces, `SPQputCopyEnd(conn, NULL)`, final `COPY 2`. `copy: in aborted`: an error text gives SQLSTATE `57014`. `copy: in bad data`. `copy: out`: rows with lengths and NUL terminators, -1, final result. `copy: out async`: 0 before any row is ready. `copy: wrong state`: -1 and -2 with `no COPY in progress\n`. `copy: exec during copy` recovers. `copy: null`. |
| `test_notify.cpp` (CABI-10) | `notify: delivery`: A listens, B notifies; A's descriptor becomes readable while idle; after `SPQconsumeInput`, `SPQnotifies` gives the channel, payload and B's `SPQbackendPID`; a second call NULL; freed with `SPQfreemem` and with `SPQfreeNotify`. `notify: socket poll`: both flags 0 gives 0; writable gives a positive value; a 50 ms end time on an idle descriptor gives 0 after at least 40 ms; end time -1 with a pending notification returns positive. `notify: current time` within one second of `time(NULL)`. `notify: null`. |
| `test_rowmodes.cpp` (CABI-11) | `rowmodes: single row`: three `SPGRES_SINGLE_TUPLE` results then `TUPLES_OK` with no rows. `rowmodes: chunked`: five rows, chunk 2: sizes 2, 2, 1, then `TUPLES_OK`. `rowmodes: only right after send`: 0 when idle and 0 after the first result. `rowmodes: null`. |
| `test_connect_async.cpp` (CABI-12) | `connect async: params`: keyword/value arrays; `expand_dbname` with a conninfo in `dbname`. `connect async: start and poll`: status OK at once, `SPQconnectPoll == SPGRES_POLLING_OK`; a bad conninfo polls `SPGRES_POLLING_FAILED`; NULL polls FAILED. `connect async: reset`: new backend PID, same `SPQsocket` number, a temporary table is gone, a regular table kept. `connect async: ping`: OK for a well-formed string (and no cluster is created: a later drop reports success), `SPQPING_NO_ATTEMPT` for a malformed one; the params form. |
| `test_encoding.cpp` (CABI-13) | `encoding: default` is 6. `encoding: set`: `LATIN1` gives 0, then 8, and `SELECT 'é'` (sent as LATIN1 byte 0xE9) returns one byte 0xE9; an unknown name gives -1; NULL gives -1. `encoding: names`: 6 is `UTF8`, 8 is `LATIN1`, -1 and 64 are `""`; `utf8` gives 6, unknown -1; the same pointer is returned on a second call. |
| `test_control.cpp` (CABI-14) | `control: drop refused while connected`: 0 and `cluster "<name>" has 1 open connections`. `control: drop`: 1 after finish; a new connection finds no table. `control: drop unknown` is 1. `control: small buffer`: the message is truncated and terminated. `control: reset`: tables gone, connection still OK. `control: reset in a transaction`: 0 with the CLIENT 10 message. `control: null`. |
| `test_dump.cpp` (CABI-15) | `dump: round trip`: dump contains `CREATE TABLE`; restoring it into another cluster reproduces the rows; `*length` equals `strlen(buffer)`. `dump: invalid restore`: 0, message, content unchanged. `dump: null arguments`. |
## 10. Steps

One subsection per follow-up part. "Owns" lists the files the part creates; (stub)
marks a file created with the minimal content given and replaced by the part named.
Unless a row says otherwise a part edits nothing it does not own. Every part runs
`make libspq-check` and, before merge, `make quickcheck` (rule L5). Sizes are code plus
tests.

| Part | Owns | Public functions | Prerequisites | Size |
|---|---|---|---|---|
| `LIBSPQ-BUILD-01-SKELETON` | `Makefile` (edit), `.gitignore` (edit), `bindings/libspq/README.md`, `exports.map`, `main.go`, `version.c`, minimal `include/libspq-fe.h` and `include/libspq-ext.h`, `tests/third_party/*`, `tests/main.cpp`, `tests/test_build.cpp`, `tests/syntax/build_c11.c`, `tests/coexist/*`, `tests/check_symbols.sh` | `SPQlibVersion`, `SPQisthreadsafe`, `SPQXversion` | LIBSPQ-DESIGN-CABI | 200 + 80 |
| `LIBSPQ-HEADER-01-PUBLIC` | `include/libspq-fe.h` and `include/libspq-ext.h` (replace), `include/libspq-compat.h`, `tests/test_headers.cpp`, `tests/syntax/headers_c11.c`, `tests/syntax/compat_c11.c` | declarations only | BUILD-01 | 450 + 200 |
| `LIBSPQ-CABI-01-CONNECT` | `spq_int.h`, `guard.c`, `conn.c`, `conn.go`, `notices.c` (stub), `notices.go` (stub), `tests/spqtest.h`, `tests/test_connect.cpp`, `tests/test_fork.cpp` | `SPQconnectdb`, `SPQfinish`, `SPQstatus`, `SPQtransactionStatus`, `SPQerrorMessage`, `SPQdb`, `SPQuser`, `SPQpass`, `SPQhost`, `SPQport`, `SPQoptions`, `SPQparameterStatus`, `SPQprotocolVersion`, `SPQserverVersion`, `SPQbackendPID`, `SPQsocket` | HEADER-01, LIBSPQ-CLIENT-08-SIMPLE | 380 + 250 |
| `LIBSPQ-CABI-02A-BLOCK` | `result.c`, `result.go`, `notices.c` and `notices.go` (replace the stubs), `query.c`, `query.go`, `tests/test_result.cpp`, `tests/test_query.cpp`; appends to `tests/test_fork.cpp` | `SPQsendQuery`, `SPQgetResult`, `SPQclear`, `SPQresultStatus`, `SPQntuples`, `SPQnfields`, `SPQgetvalue`, `SPQgetlength`, `SPQgetisnull`, `SPQcmdStatus`, `SPQresultErrorMessage` | CABI-01, LIBSPQ-CLIENT-10-NOTICES | 420 + 300 |
| `LIBSPQ-CABI-02-RESULT` | `accessors.c`, `async.c`, `async.go`, `tests/test_accessors.cpp`, `tests/test_async.cpp` | `SPQresStatus`, `SPQbinaryTuples`, `SPQfname`, `SPQfnumber`, `SPQftable`, `SPQftablecol`, `SPQfformat`, `SPQftype`, `SPQfsize`, `SPQfmod`, `SPQnparams`, `SPQparamtype`, `SPQcmdTuples`, `SPQoidValue`, `SPQresultMemorySize`, `SPQfreemem`, `SPQconsumeInput`, `SPQisBusy`, `SPQflush`, `SPQsetnonblocking`, `SPQisnonblocking` | CABI-02A | 280 + 300 |
| `LIBSPQ-CABI-03-BLOCKING` | `exec.c`, `exec.go`, `tests/test_exec.cpp` | `SPQexec` | CABI-02 | 110 + 250 |
| `LIBSPQ-CABI-04-PREPARED` | `extended.c`, `extended.go`, `tests/test_extended.cpp` | the eight `SPQsend…` functions of the extended protocol and the seven blocking forms | CABI-03, LIBSPQ-CLIENT-09-EXTENDED | 280 + 350 |
| `LIBSPQ-CABI-05-ERRORS` | `errors.c`, `errors.go`, `tests/test_errors.cpp`; appends one case to `tests/test_query.cpp` | `SPQresultErrorField`, `SPQresultVerboseErrorMessage`, `SPQsetErrorVerbosity`, `SPQsetErrorContextVisibility`, `SPQsetNoticeReceiver`, `SPQsetNoticeProcessor` | CABI-03, LIBSPQ-CLIENT-10-NOTICES | 160 + 300 |
| `LIBSPQ-CABI-06-ESCAPE` | `escape.c`, `escape.go`, `tests/test_escape.cpp` | the seven escaping functions | CABI-03, LIBSPQ-CLIENT-04-ESCAPE | 200 + 200 |
| `LIBSPQ-CABI-07-CONNINFO` | `conninfo.c`, `conninfo.go`, `tests/test_conninfo.cpp` | `SPQconninfoParse`, `SPQconndefaults`, `SPQconninfo`, `SPQconninfoFree` | CABI-03, LIBSPQ-CLIENT-01-CONNINFO | 150 + 150 |
| `LIBSPQ-CABI-08-CANCEL` | `cancel.c`, `tests/test_cancel.cpp`; appends to `tests/test_fork.cpp` | `SPQgetCancel`, `SPQcancel`, `SPQfreeCancel` | CABI-03, LIBSPQ-CLIENT-13-CANCEL | 70 + 200 |
| `LIBSPQ-CABI-09-COPY` | `copy.c`, `copy.go`, `tests/test_copy.cpp` | `SPQputCopyData`, `SPQputCopyEnd`, `SPQgetCopyData` | CABI-04, LIBSPQ-CLIENT-11-COPY | 130 + 280 |
| `LIBSPQ-CABI-10-NOTIFY` | `notify.c`, `notify.go`, `tests/test_notify.cpp` | `SPQnotifies`, `SPQsocketPoll`, `SPQgetCurrentTimeUSec` | CABI-05 | 110 + 180 |
| `LIBSPQ-CABI-11-ROWMODES` | `rowmodes.c`, `rowmodes.go`, `tests/test_rowmodes.cpp` | `SPQsetSingleRowMode`, `SPQsetChunkedRowsMode` | CABI-04, LIBSPQ-CLIENT-12-ROWMODES | 50 + 160 |
| `LIBSPQ-CABI-12-CONNECT-ASYNC` | `connect_async.c`, `connect_async.go`, `tests/test_connect_async.cpp` | `SPQconnectdbParams`, `SPQconnectStart`, `SPQconnectStartParams`, `SPQconnectPoll`, `SPQreset`, `SPQping`, `SPQpingParams` | CABI-03, LIBSPQ-CLIENT-14-CONNECT | 170 + 220 |
| `LIBSPQ-CABI-13-ENCODING` | `encoding.c`, `encoding.go`, `tests/test_encoding.cpp` | `SPQclientEncoding`, `SPQsetClientEncoding`, `spg_encoding_to_char`, `spg_char_to_encoding` | CABI-03, LIBSPQ-CLIENT-15-SETENCODING | 110 + 130 |
| `LIBSPQ-CABI-14-CONTROL` | `control.c`, `control.go`, `tests/test_control.cpp` | `SPQXclusterDrop`, `SPQXreset` | CABI-03, LIBSPQ-CLIENT-16-CONTROL | 90 + 180 |
| `LIBSPQ-CABI-15-DUMP` | `dump.c`, `dump.go`, `tests/test_dump.cpp` | `SPQXdump`, `SPQXrestore` | CABI-14, LIBSPQ-CLIENT-17-DUMP | 90 + 130 |
| `LIBSPQ-DOCS-01-FINAL` | edits `AGENTS.md`, `docs/DEVELOPMENT.md`, `docs/TESTING.md`, `docs/LIBSPQ.md`, `bindings/libspq/README.md`, one sentence of `internal/engine/README.md` | none | CABI-01 … CABI-15 | 80 |
| `LIBSPQ-EXT-01-PORTAL` (P3) | `spq/portal.go`, `spq/portal_linux_test.go`, `portal.c`, `portal.go`, `tests/test_portal.cpp`; edits `spq/conn.go`, `spq/state.go`, `spq/result.go`, `result.go`, `include/libspq-ext.h`, `docs/LIBSPQ.md` | the five functions of 3.2 | CABI-11 | 300 + 300 |
| `LIBSPQ-EXT-02-NOTICE-QUEUE` (P3) | `noticequeue.c`, `tests/test_noticequeue.cpp`; edits `include/libspq-ext.h`, `docs/LIBSPQ.md` | `SPQXsetNoticeQueue`, `SPQXnextNotice` | CABI-05 | 60 + 120 |

LIBSPQ-CABI-02-RESULT was split: `LIBSPQ-CABI-02A-BLOCK` carries the result block and
the first query path (it is the first part that can show a result at all), and
`LIBSPQ-CABI-02-RESULT` the remaining accessors and the nonblocking controls. The chain
is BUILD-01 → HEADER-01 → CABI-01 → CABI-02A → CABI-02 → CABI-03; parts 04 to 14 need
only CABI-03 (or a later part named in the table) and their CLIENT part, own disjoint
files, and can run in parallel.

For LIBSPQ-BUILD-01-SKELETON every file was built and tested as written (section 11,
"Validation slice"); a copy is in `.cswd/tmp/LIBSPQ/cabi/skeleton/`. That part may copy
those files; if the directory is missing, sections 8 and 9.2 contain everything needed
to write them.

### 10.1 Stubs

| File | Created by | Stub content | Replaced by |
|---|---|---|---|
| `notices.c` | CABI-01 | `spq_default_hooks` that zeroes the struct; `spq_deliver_notices` and `spq_discard_notices` with empty bodies | CABI-02A (4.3 in full, including `spq_conn_queue_notice` and `spq_internal_notice`) |
| `notices.go` | CABI-01 | `func queueNotices(conn *C.SPGconn, c *spq.Conn) {}` | CABI-02A (5.2) |

CABI-01 is the only part before results exist; with the stubs it compiles and links
without `SPQclear` and `TakeNotices`. A startup notice is silently dropped until
CABI-02A.

### 10.2 What each part must take from this document

| Part | Sections |
|---|---|
| BUILD-01 | 1.2, 8 (all), 9 rules, 9.2 rows `test_build.cpp`, `syntax/build_c11.c`, `coexist`; 6.2 rows `SPQlibVersion`, `SPQisthreadsafe`, `SPQXversion` |
| HEADER-01 | 2, 3 (without the optional extensions), 9.2 rows `test_headers.cpp`, `syntax/headers_c11.c`, `syntax/compat_c11.c` |
| CABI-01 | 4.1, 4.2, 5.1, 5.2, 5.3 (`conn.go`), 6.2 (the `conn.c` functions), 7.1, 9.1, 10.1 |
| CABI-02A | 4.3, 4.4, 4.5, 5.2 (`newCResult`, `queueNotices`), 5.3 (`query.go`), 6.1, 6.3 (`SPQsendQuery`, `SPQgetResult`), 6.4 (its functions) |
| CABI-02 | 6.3 (`async.c` functions), 6.4 (the rest), 5.3 (`async.go`) |
| CABI-03 | 6.1, 6.5, 5.3 (`exec.go`) |
| CABI-04 | 5.2 (`goParams`, `goOids`), 5.3 (`extended.go`), 6.3, 6.5 |
| CABI-05 | 4.3, 5.3 (`errors.go`), 6.6, 7.3 |
| CABI-06 | 5.3 (`escape.go`), 6.7 |
| CABI-07 | 4.6, 5.2 (`cOptions`), 5.3 (`conninfo.go`), 6.8 |
| CABI-08 | 4.6, 6.9, 7.1 |
| CABI-09 | 5.3 (`copy.go`), 6.10 |
| CABI-10 | 4.6, 5.3 (`notify.go`), 6.11 |
| CABI-11 | 5.3 (`rowmodes.go`), 6.3 |
| CABI-12 | 5.2 (`goPairs`), 5.3 (`connect_async.go`), 6.2 |
| CABI-13 | 5.3 (`encoding.go`), 6.12 |
| CABI-14, CABI-15 | 3.2, 5.3 (`control.go`, `dump.go`), 6.13 |
| EXT-01, EXT-02 | 3.2 "Optional extensions", 4.3 |
## 11. Experiments behind this design

Run in a detached scratch worktree of commit `c662029a11` (removed afterwards) with a
cgo package at `bindings/libspq` whose Go side was a stub (one `cgo.Handle` per
connection, a socketpair read by a goroutine for cancel, results built through C
helpers from DataRow bytes). The scratch sources, the Makefile patch and the command
log are kept in `.cswd/tmp/LIBSPQ/cabi/` (`scratch-sources.tgz`, `makefile.patch`,
`log.txt`); the generated headers and the generator are in `hdr/` there.

| # | Question | Command (abridged) | Observation | Consequence |
|---|---|---|---|---|
| 1 | Is a package whose Go files are all behind the tag skipped? | `go list ./...`, `go build ./bindings/...`, `go vet ./bindings/...`, `go test ./bindings/...`, each with `CGO_ENABLED` 0 and 1 | Not listed; "matched no packages"; exit 0 for build (vet and test report "no packages" only because nothing else matched the narrow pattern). Naming the package explicitly fails with "build constraints exclude all Go files". C files without a constraint change nothing. With a pure-Go subpackage present, `./bindings/...` lists only that subpackage. | F9; C files carry no constraint. |
| 2 | Do `c-shared` and `c-archive` build such a package? | `go build -tags libspq -buildmode=c-shared -o libspq.so ./bindings/libspq`; the same with `c-archive` | Both build; `-Wall -Wextra -Werror` in `#cgo CFLAGS` passes including generated files; a C program linked against the archive (`gcc t.c libspq.a -lpthread`) runs the same tests. | 8.1, 8.3. |
| 3 | Does a version script hide the Go exports? | `-ldflags='-extldflags=-Wl,--version-script=<abs>/exports.map,-soname,libspq.so'`, then `nm -D --defined-only` and `readelf -d` | Without the script about fifty cgo runtime symbols are exported besides the public ones (`spqgo_*` are already hidden by `-fvisibility=hidden`); with it only `SPQ*`. An anonymous script leaves symbols unversioned; `SONAME` is as given; flags show `NODELETE`. | F1, 8.2. |
| 4 | Two threads through the `.so`? | C program, four threads, each its own connection, 20,000 calls each | Passes. | 7.2. |
| 5 | Does a forked child stay out of Go? | parent connects and holds a result and a cancel object; child runs every guarded call with `alarm(10)` | Child exits 0: new connection BAD with the message, calls on the inherited connection fail, the parent's result is readable and clearable, `SPQcancel` sends nothing; the parent keeps working and saw no cancel. Same with the static archive. | F6, 7.1. The guard is the `pthread_atfork` flag alone: the pid comparison the part record mentioned adds a system call per call and catches nothing `fork()` does not already report. |
| 6 | Can C call the receiver after the Go call returned? | Go queues a notice result on the connection; C delivers after `spqgo_exec` returns; the receiver executes a statement on another connection | Called once, on the calling thread, text intact; the nested call works. | F7, 4.3. |
| 7 | What does a call into Go cost? | 2,000,000 iterations each | Call into Go 25 to 35 ns; C accessor 1.1 to 1.5 ns; a call that builds a 3 × 3 result with two Go-to-C calls about 300 to 470 ns; a cached string accessor 60 to 85 ns. | F2: accessors stay in C; connection-level calls may enter Go. |
| 8 | Any export that clashes with libpq? | export list compared with `exports.txt` of `REL_18_6`; a program linking a stub `libpqstub.so` (defining `PQlibVersion`, `PQexec`, `fatalf`) together with `libspq.so`, in both link orders | No common symbol; no libpq export starts with `SPQ` or `spg_`; both libraries answer their own calls, and the stub's `fatalf` is not disturbed by the cgo runtime's hidden one. | F1; `coexist` test. In the static archive the cgo runtime symbols (`fatalf`, `threadentry`, `crosscall2`, `_cgo_*`) are visible to the linker: a program that defines the same names, or links a second Go archive, must use the shared library. |

Additional checks: a goroutine blocked in `Read` on an `os.NewFile` wrapping a
nonblocking socketpair end returns when the file is closed (the scratch `SPQfinish`
joins it), which CLIENT section 13 listed as not measured; `SPQcancel` from a `SIGALRM`
handler and after `SPQfinish`; the doctest binary compiles in about one second per file
and runs; a weak reference to a missing library function is null (9.1);
`go vet -tags libspq` and `gofmt` are clean; a recovered Go panic comes back as an
error result and the next call works.

**Validation slice.** After the document was written, its own code was built: the
skeleton of LIBSPQ-BUILD-01 exactly as section 8 and 9.2 describe it (kept as files in
`.cswd/tmp/LIBSPQ/cabi/skeleton/`, with the root `Makefile` and `.gitignore` changes
as `root.patch`), and then the full generated headers, `spq_int.h` of 4.1, and the code
blocks of sections 4.3, 4.5, 5.2, 6.1, 6.2, 6.9, 7.1 and 9.1 extracted verbatim from
this document, completed with straightforward builders and accessors and a stand-in
`spq` package whose `GetResult` reports would-block until a goroutine signals the
descriptor. `make libspq-check` passed (symbol check, C11 syntax checks, coexistence
program, 5 doctest cases with 38 assertions): the wait loop, result building and the
in-place row index, notice delivery through the default hooks, the internal notice of
an out-of-range row, the compile-time constant checks, the weak-reference helper and
the fork guard all behave as specified. The sources are `slice-sources.tgz` in the
same directory. They are a check of this document, not a starting point: the parts
write their files from the sections listed in 10.2.

Not measured: anything that needs the real `spq` package (it does not exist yet); the
C-side loops of 6.1 against a live engine; behavior on arm64 and on toolchains other
than go 1.27.1 with gcc 13.3. If a later compiler warns in cgo-generated code, drop
`-Werror` from the `#cgo CFLAGS` line and report it; nothing else depends on it.
