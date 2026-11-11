# CPYTHON-FEATURES: COPY, named cursors, two-phase commit, cluster control, the wheel

Design of the last layer of the Python DB-API binding `bindings/cpython` (package
`stuntgres`): COPY through `cursor.copy()`, server-side (named) cursors, the PEP 249
two-phase commit extension, the libspq cluster-control API with an isolation helper for
consumers, the self-contained wheel with the make targets `cpython` and `cpython-check`,
the repository documentation, and the parts that verify every test family against libspq.
Produced by catalog part `CPYTHON-DESIGN-FEATURES`. It builds on
`docs/design/CPYTHON-PQ.md` (**PQ** below; "PQ 7.5" is its section 7.5),
`docs/design/CPYTHON-ADAPT.md` (**ADAPT**) and `docs/design/CPYTHON-DBAPI.md` (**DBAPI**).
The contract for consumers is `docs/CPYTHON.md`, which this design completes.

Words used below: **(measured)** marks a value observed in the experiments of section 11;
**oracle mode** is real libpq 18.6 against the pinned PostgreSQL 18.6 image; **server
mode** is the same libpq against the stuntgres wire server (`cmd/stuntgres`); **native
mode** is libspq, which does not exist yet: nothing of this design ran in native mode.
Every literal that depends on PostgreSQL or libpq was observed in oracle mode; literals
that follow from the binding's own algorithms were produced by the designed code. That
code, and every test file printed in section 10, ran as scratch code in unit, oracle and
server mode on top of the scratch package of the three earlier designs (section 11).

## 1. Purpose, fixed points, layout

```
stuntgres/_features.py           composition: copies the members below into the six feature classes  (1.3)
   _cur_copy.py, _cur_copy_async.py        Cursor.copy(), AsyncCursor.copy()                      (2.5, 2.6)
      copy.py, copy_async.py     BaseCopy, Copy, AsyncCopy                                        (2.2-2.6)
      _copygen.py                COPY generators (sans-IO)                                             (2.1)
      _copyfmt.py                COPY text format                                                      (2.3)
   _conn_servercur.py, _conn_servercur_async.py   server_cursor_factory                           (3.4, 3.5)
      server_cursor.py, server_cursor_async.py    ServerCursor, AsyncServerCursor                 (3.4, 3.5)
      _servercur_base.py         ServerCursorBase: statements and generators                      (3.1-3.3)
   _conn_tpc.py                  xid(), tpc_begin ... tpc_recover (sync and asyncio)                   (4.2-4.4)
      _xid.py                    Xid                                                                   (4.1)
   _conn_control.py              drop_cluster, libspq_version, reset_database, dump, restore      (5.1, 5.2)
stuntgres/testing.py             temp_cluster()                                                        (5.3)
tools/elfneeds.py                what a shared object needs; the platform tag                          (6.3)
tools/build_wheel.py             the wheel builder and PEP 517 backend                            (6.1-6.5)
tools/runinstalled.py            the test runner against an unpacked wheel                             (7.3)
tools/wheel_smoke.py             smoke test of an unpacked wheel                                       (9.4)
Makefile                         targets cpython and cpython-check                                       (7)
```

Everything above the generators stays sans-IO (plan A3, A7): every operation that talks to
the server is a generator method of a base class (`BaseCopy`, `ServerCursorBase`, the
`TpcBaseMixin` members of `BaseConnection`); the public synchronous and asyncio classes
take the connection's lock and hand the generator to the connection's waiter, exactly as
DBAPI 9.1 prescribes. The only blocking calls are the three SPQX functions of cluster
control (5.2), which libspq offers in no other form.

### 1.1 Fixed points

| # | Point | Evidence |
|---|---|---|
| F1 | `cursor.copy(statement, params=None)` returns a `Copy` object (asyncio: `AsyncCopy`), which is the context manager. Entering the block sends the statement and holds the connection's lock until the block is left. `write(buffer)` and `write_row(row)` serve `COPY ... FROM STDIN`; `read()`, iteration, `read_row()` and `rows()` serve `COPY ... TO STDOUT`. | 2.2; **(measured)** experiment 1 and the tests of 10.3 in oracle and server mode |
| F2 | The COPY statement is always sent with the extended protocol (`send_query_params` with no parameters), so it is exactly one statement: a string with several statements is refused by the server with `42601`. | **(measured)** experiment 1, case 9 |
| F3 | PostgreSQL binds no parameters in a COPY statement: with a parameter of unknown type it answers `08P01` (`bind message supplies 1 parameters, but prepared statement "" requires 0`), with a typed one `42P02` (`there is no parameter $1`). The binding therefore refuses a non-empty `params` itself with `NotSupportedError`; an empty sequence or mapping only switches the placeholder rules on (`%%` becomes `%`). Client-side binding is outside this wave (plan U10). | **(measured)** experiment 1, case 10; 2.4 |
| F4 | Leaving the block normally ends the COPY and gives the cursor the result of the command: `rowcount` is the count of the `COPY n` tag. For `COPY TO` the data not yet read is read and discarded first. Leaving it with an exception aborts `COPY FROM` with `put_copy_end(message)` (the server answers `57014`) and cancels `COPY TO` and drains it; the exception of the block propagates, the outcome of the abort is discarded. | 2.4; **(measured)** experiment 1, cases 6 and 7 |
| F5 | The generators are right for a library that asks to wait and for one that never does: `put_copy_data` and `put_copy_end` returning 0 mean "wait until the socket is writable, reading what arrives meanwhile, and try again"; after each block the output is flushed with `generators.send`. With libpq 18.6 over a local TCP connection `put_copy_data` never returned 0 while `flush` did return 1; libspq returns neither (`docs/LIBSPQ.md` 5.4). | **(measured)** experiment 1, case 13; 2.1 and its unit test with a fake connection |
| F6 | Text format only for `write_row`, `read_row` and `rows`: the dumpers and loaders of ADAPT with the escaping rules of the COPY text format. Without `set_types()` every value is returned as `str`. `read()` and `write()` pass blocks through unchanged, in any format the statement names (CSV and binary included). | 2.3; **(measured)** experiment 1, case 14 |
| F7 | `conn.cursor(name, *, row_factory=None, scrollable=None, withhold=False)` returns a `ServerCursor` (`AsyncServerCursor`). It is a class of its own on `CurCore`, not a subclass of `Cursor`: it has no client-side result to scroll in, no `executemany` and no `copy`. | 3.1 |
| F8 | Statements of a named cursor, literally: `DECLARE "name" [SCROLL \| NO SCROLL] CURSOR [WITH HOLD] FOR <query>` (extended protocol, parameters bound on the server), a Describe of the portal, `FETCH FORWARD <n> FROM "name"`, `FETCH FORWARD ALL FROM "name"`, `MOVE ABSOLUTE <n> FROM "name"`, `SELECT 1 FROM pg_catalog.pg_cursors WHERE name = $1`, `CLOSE "name"`. The name is quoted as an identifier (`"` doubled). | 3.2; **(measured)** experiment 2 |
| F9 | Every move is `MOVE ABSOLUTE <target>`, where target is the index of the next row to fetch: the cursor keeps that index itself (`rownumber`). The tag `MOVE 0` for a target above 0 means the target is beyond the last row: `IndexError`, and the position is unknown until the next absolute move. | **(measured)** experiment 2: `MOVE ABSOLUTE 7` of 6 rows gives `MOVE 0`; 3.3 |
| F10 | `close()` first asks `pg_cursors` whether the cursor still exists and sends `CLOSE` only then: a `CLOSE` of a cursor that is gone (its transaction ended, a savepoint was rolled back) would fail with `34000` and abort the transaction that is open now. Outside a transaction a cursor without `WITH HOLD` is gone by definition and nothing is sent. | **(measured)** experiment 2: `CLOSE "c1"` twice gives `34000` and `INERROR`; 3.3 |
| F11 | The implicit `BEGIN` of DBAPI D8 applies before `DECLARE`, `FETCH` and `MOVE`. With `autocommit=True` only a `withhold=True` cursor can be declared; PostgreSQL answers `25P01` otherwise. | **(measured)** experiment 2, case 1; `tests/test_server_cursor.py` |
| F12 | Two-phase commit is the PEP 249 extension: `xid()`, `tpc_begin()`, `tpc_prepare()`, `tpc_commit()`, `tpc_rollback()`, `tpc_recover()`. `ConnCore._tpc` is `None` or the pair `(xid, prepared)`; while it is set, `commit()`, `rollback()` and `transaction()` are refused (DBAPI 4.6, 5.4). | 4.3 |
| F13 | An `Xid` made by `xid(format_id, gtrid, bqual)` is known to PostgreSQL as `<format_id>_<base64(gtrid)>_<base64(bqual)>` (standard base64 with padding, UTF-8); `tpc_recover()` parses that form back, and any other identifier becomes `Xid(None, <text>, None)`. With parts of at most 64 bytes the identifier has at most 188 characters; PostgreSQL accepts 199. | 4.1; **(measured)** experiment 3: 199 characters accepted, 200 refused with `22023` |
| F14 | `PREPARE TRANSACTION` of a transaction that has failed, or with no transaction open, is no error in PostgreSQL: it rolls back and answers with the tag `ROLLBACK`. `tpc_prepare()` therefore checks the tag and raises `OperationalError` when it is not `PREPARE TRANSACTION`. | **(measured)** experiment 3; 4.3 |
| F15 | PostgreSQL needs `max_prepared_transactions` above 0 for `PREPARE TRANSACTION`: the oracle container is started with 100 (PQ 9.4). The stuntgres server needs no setting: it reports `max_prepared_transactions` as `0` and accepts `PREPARE TRANSACTION`. | **(measured)** experiment 3 |
| F16 | Cluster control exists only with libspq: `stuntgres.drop_cluster(host="localhost", port=5432)`, `stuntgres.libspq_version()`, `Connection.reset_database()`, `Connection.dump() -> bytes`, `Connection.restore(data)`, with asyncio twins of the three methods. With the test seam's libpq each raises `NotSupportedError` (the stub of PQ 3.2). The three methods need an idle session. | 5; **(measured)** the refusals, in oracle and server mode |
| F17 | `stuntgres.testing.temp_cluster(dbname="test", user="test")` is a context manager that yields the connection string of a cluster with a name of its own (`host=temp-<32 hex digits>`) and drops the cluster when the block ends. | 5.3 |
| F18 | The wheel is built by `tools/build_wheel.py` with the standard library only and is `build/cpython/dist/stuntgres-<version>-py3-none-<platform>.whl`. It holds the package, `stuntgres/_lib/libspq.so` and the three metadata files. Identical input gives an identical file. | 6; **(measured)** experiment 4: accepted by `pip install --no-index --no-deps --target`, and unpacked with `zipfile` |
| F19 | The platform tag is `manylinux_<major>_<minor>_<arch>`, read from the library itself: `<arch>` from the ELF machine, the glibc version from the highest `GLIBC_<major>.<minor>` symbol version the library needs, at least 2.17, and 2.36 when it needs `GLIBC_ABI_DT_RELR`. A library that needs anything but glibc's own libraries, or `GLIBC_PRIVATE`, is refused. | 6.3; **(measured)** experiment 5: a cgo shared library that links the whole engine needs only `libc.so.6` and at most `GLIBC_2.34` on a host with glibc 2.39 |
| F20 | The version has one source, `stuntgres/_version.py`. `pyproject.toml` repeats it; the builder refuses to build when the two differ. | 6.4 |
| F21 | `pyproject.toml` gets a `[build-system]` table that names `tools/build_wheel.py` as an in-tree PEP 517 backend with no requirements. `pip install bindings/cpython` then builds the same wheel, offline; without the table pip tries to download setuptools and, with a network, would install the package without its library. `make cpython` needs neither pip nor a network. | **(measured)** experiment 4 |
| F22 | `make cpython` depends on `libspq` and builds the wheel. `make cpython-check` builds it, unpacks it into `build/cpython/site`, runs the test suite in unit and native mode against the unpacked package, then the wheel smoke test; `REFERENCE=1` adds oracle mode and `SERVER=1` adds server mode. Neither target is part of `quickcheck`. | 7; **(measured)** experiment 6 |
| F23 | The suite runs against the unpacked wheel through `tools/runinstalled.py`, which imports the package from the unpacked directory and then calls the unchanged runner of PQ 9.1. | 7.3; **(measured)** 251 unit tests and the oracle tests of three files against an unpacked wheel |
| F24 | In native mode the binder of PQ 3.2 needs every required function at once, so nothing runs before the LIBSPQ parts `CABI-01` to `CABI-12` are done. Every native part is gated on that set through `CPYTHON-TEST-03-NATIVE`, and names the LIBSPQ parts of its own family besides. | 9.1; experiment 7 |
| F25 | `import stuntgres` still maps no native library, starts no thread and imports neither `asyncio` nor the type adapters with the seven new names in place. | **(measured)** 22 to 27 ms; `/proc/self/maps` shows no library |

Decisions the part record had left open, and what settled them:

- **`copy()` returns the context manager itself**, as `transaction()` does (DBAPI 5.4),
  instead of a generator-based context manager: the object can then refuse to be entered
  twice and has a `repr` that tells its state.
- **Parameters of `copy()` are refused, not bound** (F3).
- **A `COPY TO` block that is left early reads the rest** (F4). The alternative, leaving
  the session in the middle of a COPY, would make the next statement fail.
- **Relative moves are sent as absolute moves** (F9). PostgreSQL's relative `MOVE` counts
  from a position that has two states at the end of the result (on the last row, after the
  last row) which a client cannot tell apart; an absolute target has one meaning.
- **`close()` asks before it closes** (F10).
- **`tpc_prepare()` checks the command tag** (F14). A coordinator that took a rolled-back
  branch for a prepared one would commit the other branches.
- **The two-phase tests are this design's own.** The repository of the vendored suite
  (`https://github.com/baztian/dbapi-compliance`, PQ 9.8) has no two-phase file (its file
  list was read: `dbapi20.py`, `setup.py`, two driver test files, a README). The only
  file of that kind at hand, `../src-ref/psycopg/tests/dbapi20_tpc.py`, carries no
  public-domain statement, is distributed under psycopg's LGPL and was edited by psycopg
  (type annotations, pytest assertions, connection handling). So nothing is vendored
  (rule C3) and `tests/test_tpc.py` holds cases written for this design (10.3).
- **The glibc version comes from the library's symbol versions, not from the build host**
  (F19). Consequence: a wheel built on a host with glibc 2.39 is installable by pip
  wherever glibc is at least 2.34 (Debian 12, Ubuntu 22.04, RHEL 9), which is where the
  library can be loaded; tagging by the host would lock it to 2.39 and newer. The tag
  claims only what the symbol versions and the list of needed libraries show; the builder
  does not run `auditwheel` and the wheel is not meant for a package index (plan U10).
- **A `[build-system]` table is added** (F21).
- **The runner is not edited** (F23): PQ 1.2 says no later part edits it, and a wrapper
  is enough.
- **Parts were split** at 500 lines (code plus tests): COPY-03 and SERVERCUR-01 each
  hand a second test file to a tests-only part, and the ELF reader is a part of its own
  (10.1).

One fixed point of the part record is met differently, on evidence of the merged design:
the record expects that in a forked child a connection attempt "fails with libspq's fork
message as an OperationalError". The loader of PQ 2.3 refuses before libspq is called and
raises `InterfaceError` with that same text (PQ 2.4, measured there and again here: the
smoke test of 9.4 ran through the test seam in oracle and server mode). The smoke test
asserts `InterfaceError`; PQ is not changed.

Server-mode differences found on statements the binding sends or a designed test needs
**(measured)**, each filed under rule D8 or covered by an existing part:

| Difference | PostgreSQL 18.6 | stuntgres server | Part |
|---|---|---|---|
| Describe of a portal made by SQL `DECLARE` | row description | `34000 portal "c" does not exist` | `CPYTHON-ENGINE-01-DESCRIBE-SQL-CURSOR` (existing) |
| `DECLARE "c" CURSOR FOR ...` without `WITH HOLD`, outside a transaction block, sent with the extended protocol | `25P01 DECLARE CURSOR can only be used in transaction blocks` | `DECLARE CURSOR` succeeds (the simple protocol answers `25P01` correctly) | `CPYTHON-ENGINE-06-DECLARE-EXTENDED-OUTSIDE-BLOCK` |
| `PREPARE TRANSACTION` in a failed transaction, or with none open | tag `ROLLBACK` | tag `PREPARE TRANSACTION` (nothing is prepared in either system) | `CPYTHON-ENGINE-07-PREPARE-TRANSACTION-TAG` |
| `MOVE ABSOLUTE 0` after a fetch on a cursor declared with neither `SCROLL` nor `NO SCROLL` over `select ... order by <primary key>` | `MOVE 0` (the plan can scan backward; `pg_cursors.is_scrollable` is `t`) | `55000 cursor can only scan forward` (`is_scrollable` is `f`) | `CPYTHON-ENGINE-08-DEFAULT-CURSOR-SCROLL` (P2; no designed test depends on it: PostgreSQL documents the default as plan-dependent, so the tests scroll backward only with `scrollable=True`) |

Everything else the binding sends, and every other statement of the designed tests,
answered identically in both modes. Differences seen only in probes are listed in
section 11.

### 1.2 Files

Everything is under `bindings/cpython/` except the root `Makefile` and the documents of
section 8. "Owner" is the one part that writes the final file. `CPYTHON-` is omitted from
the part names.

| File | Responsibility | Owner |
|---|---|---|
| `stuntgres/_features.py` | The composition (1.3). Replaces the stub of CONN-01D-BASE. | FEATURES-01-STUBS |
| `stuntgres/_copygen.py` | `copy_write`, `copy_end`, `copy_read` (2.1). | COPY-01-GENERATORS |
| `stuntgres/_copyfmt.py` | `format_row`, `parse_row` (2.3). | COPY-02-FORMAT |
| `stuntgres/copy.py` | `BaseCopy`, `Copy`, `FLUSH_SIZE` (2.2, 2.4, 2.5). | COPY-03-SYNC |
| `stuntgres/_cur_copy.py` | `CopyCursorMixin`: `Cursor.copy()`; exports `Copy`. | replaced by COPY-03-SYNC (stub by FEATURES-01-STUBS) |
| `stuntgres/copy_async.py` | `AsyncCopy` (2.6). | COPY-04-ASYNC |
| `stuntgres/_cur_copy_async.py` | `AsyncCopyCursorMixin`: `AsyncCursor.copy()`; exports `AsyncCopy`. | replaced by COPY-04-ASYNC (stub by FEATURES-01-STUBS) |
| `stuntgres/_servercur_base.py` | `ServerCursorBase` (3.1 to 3.3). | SERVERCUR-01-BASE |
| `stuntgres/server_cursor.py` | `ServerCursor` (3.4). | SERVERCUR-02-SYNC |
| `stuntgres/_conn_servercur.py` | `ServerCursorMixin`: `server_cursor_factory`; exports `ServerCursor`. | replaced by SERVERCUR-02-SYNC (stub by FEATURES-01-STUBS) |
| `stuntgres/server_cursor_async.py` | `AsyncServerCursor` (3.5). | SERVERCUR-03-ASYNC |
| `stuntgres/_conn_servercur_async.py` | `AsyncServerCursorMixin`; exports `AsyncServerCursor`. | replaced by SERVERCUR-03-ASYNC (stub by FEATURES-01-STUBS) |
| `stuntgres/_xid.py` | `Xid`, `need_xid` (4.1). | TPC-01-XID |
| `stuntgres/_conn_tpc.py` | `TpcBaseMixin`, `TpcMixin`, `AsyncTpcMixin` (4.2 to 4.4); exports `Xid`. | replaced by TPC-02-CONNECTION (stub by FEATURES-01-STUBS) |
| `stuntgres/_conn_control.py` | `drop_cluster`, `libspq_version`, `ControlMixin`, `AsyncControlMixin` (5.1, 5.2). | replaced by CONTROL-01-API (stub by FEATURES-01-STUBS) |
| `stuntgres/testing.py` | `temp_cluster` (5.3). | CONTROL-02-ISOLATION |
| `tools/elfneeds.py` | `read_needs`, `glibc_version`, `platform_tag` (6.3). | MAKE-01A-ELF |
| `tests/elf_util.py` | `make_elf`: a synthetic shared object for the tests. | MAKE-01A-ELF |
| `tools/build_wheel.py` | The builder and the PEP 517 hooks (6). | MAKE-01-WHEEL |
| `pyproject.toml` | Created by BUILD-01-SKELETON; MAKE-01-WHEEL appends the `[build-system]` table (6.5). | BUILD-01-SKELETON, then MAKE-01-WHEEL |
| `tools/runinstalled.py` | The runner against an unpacked wheel (7.3). | MAKE-02-TARGETS |
| `tools/wheel_smoke.py` | The wheel smoke test (9.4). | MAKE-02-TARGETS |
| `Makefile` (repository root) | The block of 7.1 and two words on the `.PHONY` line. The only CPYTHON part that edits it. | MAKE-02-TARGETS (after LIBSPQ-BUILD-01-SKELETON) |
| `README.md`, `docs/CPYTHON.md`, `AGENTS.md`, `docs/DEVELOPMENT.md`, `docs/TESTING.md`, `docs/LIBSPQ.md` | Section 8. | DOCS-01-FINAL |
| `tests/test_<name>.py` | One or two test files per part (10.1 names each). | the part |

Four files of the DBAPI design are edited, each in one test and each by one part that
depends on the file's owner: `tests/test_connection_base.py` and `tests/test_module.py`
(FEATURES-01-STUBS), `tests/test_connection.py` (SERVERCUR-02-SYNC) and
`tests/test_async_connection.py` (SERVERCUR-03-ASYNC). The edits are printed in 10.3. No
other file of PQ, ADAPT or DBAPI is touched: `stuntgres/__init__.py` copies the new names
through `_features.__all__` (DBAPI 2), `Connection.cursor(name)` finds
`server_cursor_factory` (DBAPI 9.2), and `tools/runtests.py` stays as PQ 9.1 prints it.

### 1.3 The composition (`stuntgres/_features.py`)

DBAPI 1.5 hands this design one file with six empty classes, each the first base of a
connection or cursor class, and an `__all__` that the package module copies. Four
families add members to those classes. So that each family owns its own files and no two
parts without an ordering edge touch the same file, the pattern of DBAPI 1.3 is applied
once more:

- CPYTHON-FEATURES-01-STUBS replaces `_features.py` by the composition printed in 10.3
  and creates six **family modules** as stubs. Each family module defines
  `EXPORTS: tuple[str, ...]` (the public names it gives the package; empty in a stub)
  and one class per feature class it extends (empty in a stub).
- Each later part replaces the whole family module it owns and nothing else.

| Family module | Classes it defines | Members go into | `EXPORTS` when its owner has landed | Owner |
|---|---|---|---|---|
| `_cur_copy.py` | `CopyCursorMixin` | `CursorFeatures` | `("Copy",)` | COPY-03-SYNC |
| `_cur_copy_async.py` | `AsyncCopyCursorMixin` | `AsyncCursorFeatures` | `("AsyncCopy",)` | COPY-04-ASYNC |
| `_conn_servercur.py` | `ServerCursorMixin` | `ConnectionFeatures` | `("ServerCursor",)` | SERVERCUR-02-SYNC |
| `_conn_servercur_async.py` | `AsyncServerCursorMixin` | `AsyncConnectionFeatures` | `("AsyncServerCursor",)` | SERVERCUR-03-ASYNC |
| `_conn_tpc.py` | `TpcBaseMixin`, `TpcMixin`, `AsyncTpcMixin` | `BaseConnectionFeatures`, `ConnectionFeatures`, `AsyncConnectionFeatures` | `("Xid",)` | TPC-02-CONNECTION |
| `_conn_control.py` | `ControlMixin`, `AsyncControlMixin` | `ConnectionFeatures`, `AsyncConnectionFeatures` | `("drop_cluster", "libspq_version")` | CONTROL-01-API |

`_features.py` does two things when it is imported:

1. For every family module, in the order of the table, and for every name in its
   `EXPORTS`: bind the name in `_features` and append it to `_features.__all__`. With
   every family in place `__all__` is `["Copy", "AsyncCopy", "ServerCursor",
   "AsyncServerCursor", "Xid", "drop_cluster", "libspq_version"]`, and
   `stuntgres.__all__` is the 42 names of DBAPI 2 followed by these seven.
2. `_merge(target, *sources)`: copy every member of the family classes whose name does
   not start with two underscores into the feature class (`setattr`). The six feature
   classes keep `object` as their only base, so the method resolution order that the
   DBAPI tests pin (`Connection`, `ConnectionFeatures`, `BaseConnection`,
   `BaseConnectionFeatures`, ...) is unchanged. `BaseCursorFeatures` gets no member in
   this design.

Rules for a family module: it must not import `connection.py`, `connection_async.py`,
`cursor.py`, `cursor_async.py`, `_connection_base.py` or `_cursor_base.py` (they import
`_features`; the import would be circular), and it must not import `asyncio` at module
level (F25). The methods of the family classes are written against the class they end up
in: `self` is annotated `Any` where a method uses members of `BaseConnection`.

### 1.4 Rules for every module of this design

The rules of DBAPI 1.4 hold unchanged (private names and message texts are part of the
design; a generator method never takes the lock; no reliance on the GIL; one logger,
`stuntgres`; the package never prints). In addition:

- **`__module__`.** `Copy`, `AsyncCopy`, `ServerCursor`, `AsyncServerCursor` and `Xid`
  set `__module__ = "stuntgres"`.
- **Shared state.** This design adds none: a copy object and a server cursor belong to
  one thread or task at a time, like a cursor; an `Xid` is not changed after it is made;
  `_conn_control` and `testing` keep no state.
- **The lock and COPY.** A copy block holds the connection's lock from its first to its
  last statement. Another thread or task that uses the connection waits for the end of
  the block; code *inside* the block must not use the connection for anything but the
  copy object (it would wait for itself). `Connection.cancel()` takes no lock and may be
  called at any time.
- **Never run Python with `bindings/cpython/stuntgres` as the current or script
  directory** (ADAPT 1.3) **(measured once more: a script started there failed in
  `import re`)**.

## 2. COPY

```python
with cur.copy("COPY item (id, name) FROM STDIN") as copy:
    copy.write_row((1, "pen"))
    copy.write_row((2, None))
print(cur.rowcount)                                   # 2

with cur.copy("COPY item TO STDOUT") as copy:
    copy.set_types([23, 25])                          # int4, text
    for row in copy.rows():
        print(row)                                    # (1, 'pen') then (2, None)
```

### 2.1 The generators (`stuntgres/_copygen.py`)

Three generators of the protocol of PQ 7.1 (`PQGen`); they call only the nonblocking
members of `PGconn` (PQ 5.12, 5.7) and the generators of PQ 7.

```python
def copy_write(pgconn: PGconn, data: bytes) -> PQGen[None]: ...
def copy_end(pgconn: PGconn, error: bytes | None = None) -> PQGen[list[PGresult]]: ...
def copy_read(pgconn: PGconn) -> PQGen[bytes | list[PGresult]]: ...
```

`_wait_writable(pgconn) -> PQGen[None]` (private):

1. `ready = yield Wait.RW`; while `ready` is `Ready.NONE` (the waiter's interval
   elapsed), yield `Wait.RW` again.
2. If `ready & Ready.R`: `pgconn.consume_input()`. (While the client waits to write,
   the server may be writing a notice or an error; reading it keeps both sides moving.)

`copy_write(pgconn, data)`, one block of `COPY FROM STDIN` data:

1. While `pgconn.put_copy_data(data) == 0`: `yield from _wait_writable(pgconn)`.
2. `yield from generators.send(pgconn)` (PQ 7.3: flush until `flush()` returns 0).

`copy_end(pgconn, error=None)`, the end of `COPY FROM STDIN`:

1. While `pgconn.put_copy_end(error) == 0`: `yield from _wait_writable(pgconn)`.
   `error` not `None` makes the server abort the COPY and report that text.
2. `yield from generators.send(pgconn)`.
3. `return (yield from generators.fetch_many(pgconn))`: the results that follow, as
   PQ 7.5 collects them (it stops again at a COPY status).

`copy_read(pgconn)`, the next row of `COPY TO STDOUT`:

1. Loop: `count, data = pgconn.get_copy_data(1)`. If `count != 0` leave the loop.
   Otherwise `ready = yield Wait.R`; if `ready`: `pgconn.consume_input()`.
2. If `count > 0`: `return bytes(data)` (one row; libpq and libspq both deliver a COPY
   OUT stream row by row).
3. Otherwise (`count == -1`, the COPY is over): `return (yield from
   generators.fetch_many(pgconn))`.

A caller tells the two results of `copy_read` apart with `isinstance(item, bytes)`.
`get_copy_data` raises `OperationalError` for `-2` itself (PQ 5.12).

Why both shapes of library are served (F5): libspq's `SPQputCopyData` never returns 0
and `SPQflush` always returns 0 (`docs/LIBSPQ.md` 5.4), so `copy_write` yields nothing
there; `SPQgetCopyData` in async mode returns 0 when no row is ready, which step 1 of
`copy_read` handles. libpq may return 0 from `PQputCopyData` in nonblocking mode and 1
from `PQflush` **(measured: sending 40 blocks of 1.1 MB, `put_copy_data` never returned
0 and `flush` returned 1)**. The path for 0 is covered by a unit test with a fake
connection (10.3).

What the session does **(measured, experiment 1; identical in oracle and server mode)**:

| Step | Result status | Transaction status afterwards |
|---|---|---|
| `copy t from stdin` sent (simple or extended protocol) | `COPY_IN`, `nfields` 2, `binary_tuples` 0 | `ACTIVE` |
| `copy_end()` after valid rows | `COMMAND_OK`, tag `COPY 3` | `IDLE` (`INTRANS` in a transaction block) |
| `copy_end()` after a row the table rejects | `FATAL_ERROR 22P02` | `IDLE` (`INERROR` in a block) |
| `copy_end(b"...")` | `FATAL_ERROR 57014`, message `COPY from stdin failed: ...` | `IDLE` (`INERROR`) |
| `copy t to stdout` sent | `COPY_OUT` | `ACTIVE` |
| `copy_read()` until the end | each row as `bytes` ending in `\n`, then `COMMAND_OK`, tag `COPY 3` | `IDLE` |
| a query error in the middle of `COPY TO` | the rows before it, then `FATAL_ERROR 22012` | `IDLE` |
| a cancel request during `COPY TO` | some rows, then `FATAL_ERROR 57014` | `IDLE` |
| `select 1; copy t to stdout; select 2` (simple protocol) | `TUPLES_OK`, `COPY_OUT`; after the data `COMMAND_OK`, `TUPLES_OK` | `IDLE` |
| `copy t to stdout; select 2` (extended protocol) | `FATAL_ERROR 42601 cannot insert multiple commands into a prepared statement` | `IDLE` |
| `copy (select $1::int4) to stdout` with one parameter of unknown type | `FATAL_ERROR 08P01` | `IDLE` |

### 2.2 The copy object: state (`stuntgres/copy.py`)

`BaseCopy` holds the state and the generators; `Copy` (2.5) and `AsyncCopy` (2.6) add
the lock and the waiting.

```python
Buffer = Union[bytes, bytearray, memoryview]
FLUSH_SIZE = 32 * 1024

class BaseCopy:
    cursor: Any                                  # the cursor copy() was called on
    def __init__(self, cursor: Any, statement: Query, params: Params | None = None) -> None: ...
    @property
    def connection(self) -> Any: ...
    def set_types(self, types: Sequence[int]) -> None: ...
```

| Attribute | Meaning |
|---|---|
| `cursor`, `_conn` | The cursor and its connection. |
| `_statement`, `_params` | What `copy()` was given. |
| `_state` | `"new"` (not entered), `"in"` (COPY FROM STDIN), `"out"` (COPY TO STDOUT), `"done"`. |
| `_pending` | `bytearray`: data of `write` and `write_row` not yet sent. |
| `_tx` | The `Transformer` of this COPY (ADAPT 3.5); `None` before the block is entered. |
| `_nfields`, `_binary` | Column count and `bool(binary_tuples)` of the result that started the COPY. |

`repr(copy)` is `<stuntgres.Copy [out] at 0x...>` with the state in brackets.

The state table (the method named in the first column is refused with
`ProgrammingError` in the states marked "no"):

| | `new` | `in` | `out` | `done` after `out` | `done` after `in` |
|---|---|---|---|---|---|
| enter the block | starts the COPY | no: `a copy block cannot be entered twice` | no | no | no |
| `write`, `write_row` | no: `write() needs a copy block: use 'with'` | queue data | no: `write() can be used only during COPY ... FROM STDIN` | no | no |
| `read`, `read_row`, iteration, `rows` | no: `read() needs a copy block: use 'with'` | no: `read() can be used only during COPY ... TO STDOUT` | next block or row | `b""` / `None` | no |
| `set_types` | no | no | chooses the loaders | chooses the loaders | no |
| leave the block | nothing | 2.4 | 2.4 | nothing | nothing |

(The message names the method that was called: `write_row() needs ...`,
`read_row() can be used only ...`.)

### 2.3 The text format (`stuntgres/_copyfmt.py`)

```python
def format_row(values: Sequence[bytes | None]) -> bytes: ...
def parse_row(data: bytes) -> list[bytes | None]: ...
```

The COPY text format (`https://www.postgresql.org/docs/18/sql-copy.html`, "Text
Format"): one line per row, fields separated by a tab, `\N` for NULL, a backslash before
the characters that would otherwise end a field or a line.

`format_row(values)`: for each value, `None` becomes `\N`; any other value has each of
these seven bytes replaced, and nothing else:

| Byte | Written as |
|---|---|
| backslash (`0x5c`) | `\\` |
| backspace (`0x08`) | `\b` |
| tab (`0x09`) | `\t` |
| newline (`0x0a`) | `\n` |
| vertical tab (`0x0b`) | `\v` |
| form feed (`0x0c`) | `\f` |
| carriage return (`0x0d`) | `\r` |

The fields are joined with a tab and a newline is appended. A row without values is
`b"\n"`. The values are what the dumpers of ADAPT produce (`Transformer.dump_sequence`):
the text of a parameter is the text of a COPY field before escaping, for every type.

`parse_row(data)`: remove one trailing newline; split at every tab; a field that is
exactly `\N` becomes `None`; in any other field a backslash followed by `b`, `f`, `n`,
`r`, `t` or `v` becomes that control character and a backslash followed by any other
byte becomes that byte. This is what `COPY TO` writes; octal and hexadecimal escapes,
which `COPY FROM` accepts, are never written by the server and are not parsed.

**(measured, experiment 1, case 14)** what PostgreSQL 18.6 writes for
`(text, bytea, int4[], text)` rows:

| Values | Line |
|---|---|
| `tab<TAB>here<LF>nl\back<CR>cr<BS>bs<FF>ff<VT>vt`, `\x00ff5c`, `{1,NULL,3}`, NULL | `b'tab\\there\\nnl\\\\back\\rcr\\bbs\\fff\\vvt\t\\\\x00ff5c\t{1,NULL,3}\t\\N\n'` |
| empty string, empty bytea, `{}`, the two characters `\N` | `b'\t\\\\x\t{}\t\\\\N\n'` |
| the two characters `\N`, NULL, NULL, `café €` | `b'\\\\N\t\\N\t\\N\tcaf\xc3\xa9 \xe2\x82\xac\n'` |

The text is in the session's client encoding, like every other result.

### 2.4 The copy object: generators and algorithms

All generators assume that the caller holds the connection's lock.

`_start_gen() -> PQGen[None]` (entering the block):

1. If `_state != "new"` raise `ProgrammingError("a copy block cannot be entered twice")`.
2. `cursor._check_usable()`; `cursor._reset()` (DBAPI 6.4: the cursor forgets its
   previous result now).
3. `tx = Transformer(cursor)`; `sql, dumped, _ = cursor._convert(tx, statement, params)`
   (ADAPT 2: with `params` not `None` the placeholders are converted).
4. If `dumped` is not empty raise `NotSupportedError("copy() cannot bind parameters:
   PostgreSQL accepts none in a COPY statement")` (F3). Nothing was sent.
5. `yield from conn._start_query()` (the implicit `BEGIN` of DBAPI 4.6).
6. `pgconn.send_query_params(sql, [], None)`; `results = yield from
   generators.execute(pgconn)`.
7. Look at the last result (`_begin`):
   - no result at all: `InternalError("the statement returned no result")`;
   - an error status: the exception of `error_from_result` (DBAPI 3.5);
   - `COPY_BOTH`: `NotSupportedError("COPY BOTH is not supported")` (and the session is
     first abandoned with `_cur_results.end_copy`, DBAPI 6.3);
   - any other status but `COPY_IN` and `COPY_OUT`: `ProgrammingError("copy() needs a
     COPY ... FROM STDIN or COPY ... TO STDOUT statement")`. The statement has run;
   - `COPY_IN` or `COPY_OUT`: remember `nfields` and `binary_tuples`, keep `tx`, call
     `tx.set_loader_types([0] * nfields)` (every column gets the fallback loader, ADAPT
     3.5 and 6.3: `str`), set `_state` to `"in"` or `"out"`.
8. Release every result. In the four failing cases raise the exception; the state stays
   `"new"`.

`_add(buffer) -> bool` (`write`): check the state (2.2); a `str` is encoded with
`tx.encoding`, `bytes`, `bytearray` and `memoryview` are taken as they are, anything
else is `TypeError("cannot write int: use bytes or str")` (with the name of the type);
append to `_pending`; return `len(_pending) >= FLUSH_SIZE`.

`_add_row(row) -> bool` (`write_row`): check the state; if `_binary` raise
`NotSupportedError("write_row() supports only the text format")`; `values, _ =
tx.dump_sequence(row)`; append `format_row(values)` to `_pending`; return
`len(_pending) >= FLUSH_SIZE`.

`_flush_gen() -> PQGen[None]`: if `_pending` is not empty, take its content as `bytes`,
empty it, and `yield from copy_write(pgconn, data)`. The public `write` and `write_row`
run this generator only when `_add` / `_add_row` returned `True`, so most calls do no
I/O **(measured: 10000 rows of 26 bytes are sent as 8 blocks, 7 of them between 32768
and 32797 bytes)**.

`_read_gen() -> PQGen[bytes]`: check the state; in state `"done"` return `b""`; `item =
yield from copy_read(pgconn)`; a `bytes` item is returned; otherwise `yield from
_end_gen(item)` and return `b""`.

`_read_row_gen() -> PQGen[tuple | None]`: check the state; if `_binary` raise
`NotSupportedError("read_row() supports only the text format")`; `data = yield from
_read_gen()`; empty: return `None`; else return
`tx.load_sequence(parse_row(data))` (ADAPT 3.5; a line with another number of fields
than the COPY has columns raises its `InterfaceError`).

`set_types(types)`: check the state (`"out"`, or `"done"` after `"out"`); if
`len(types) != _nfields` raise `ProgrammingError("set_types() got 1 types for 7
columns")` (with the two numbers); `tx.set_loader_types(list(types))`. The items are
type OIDs (the `type_code` of a `Column`, or a constant of `stuntgres._oids`).

`_end_gen(results) -> PQGen[None]` (the COPY is over): set `_state = "done"`; `yield
from _cur_results.check_results(pgconn, results)` (DBAPI 6.3: an error result raises
and releases everything); `cursor._set_results(results, tx)`. The cursor then shows the
command: `rowcount` 3, `statusmessage` `"COPY 3"`, `description` `None`.

`_finish_gen(exc) -> PQGen[None]` (leaving the block; `exc` is the exception of the
block or `None`):

| State | `exc` | Steps |
|---|---|---|
| `new`, `done` | any | Nothing. |
| `in` | `None` | `_flush_gen()`; `results = yield from copy_end(pgconn)`; `_end_gen(results)`. An error of the server (a row it rejects) is raised here. |
| `out` | `None` | `copy_read` until it returns the results, discarding the rows; `_end_gen(results)`. |
| `in` | set | `_state = "done"`; empty `_pending`; if the connection is closed or lost, stop. `results = yield from copy_end(pgconn, message)` with `message = f"aborted by the client: {type(exc).__name__}: {exc}"`, NUL characters replaced by spaces, encoded in the client encoding with `errors="replace"`. Release the results without looking at them. |
| `out` | set | `_state = "done"`; if the connection is closed or lost, stop. If `pgconn.transaction_status` is `ACTIVE`: `conn._cancel_handle.cancel()` (DBAPI 8.3). `copy_read` until it returns the results. Release them without looking. |

After an abort the session is `INERROR` inside a transaction and `IDLE` in autocommit
mode **(measured)**; the cursor has no result. The public classes log an exception that
the abort itself raises (`logger.warning("error ignored while aborting COPY on %r: %s",
conn, error)`) and let the exception of the block propagate.

Interruption needs no code of its own: Ctrl-C or task cancellation inside a wait goes
through `Connection.wait` (DBAPI 8.4), whose cancel generator stops at the COPY status;
the exception then leaves the block and `_finish_gen(exc)` ends the COPY **(measured:
`tests/test_copy_async.py`, a cancelled task inside a `COPY TO` block leaves the
connection usable)**.

### 2.5 `Copy` and `Cursor.copy()`

```python
class Copy(BaseCopy):
    __module__ = "stuntgres"
    def __enter__(self) -> Copy: ...
    def __exit__(self, exc_type: type[BaseException] | None, exc_val: BaseException | None,
                 exc_tb: TracebackType | None) -> None: ...
    def write(self, buffer: Buffer | str) -> None: ...
    def write_row(self, row: Sequence[Any]) -> None: ...
    def read(self) -> bytes: ...
    def __iter__(self) -> Iterator[bytes]: ...
    def read_row(self) -> tuple[Any, ...] | None: ...
    def rows(self) -> Iterator[tuple[Any, ...]]: ...

# stuntgres/_cur_copy.py, merged into Cursor (1.3)
class CopyCursorMixin:
    def copy(self, statement: Query, params: Params | None = None) -> Copy: ...
```

| Method | Algorithm |
|---|---|
| `Cursor.copy(statement, params=None)` | `return Copy(self, statement, params)`. Nothing is sent before the block is entered. |
| `__enter__` | `conn.lock.acquire()`; `conn.wait(self._start_gen())`; if that raises (any `BaseException`), release the lock and re-raise; return `self`. |
| `__exit__` | In a `try` whose `finally` releases the lock: with no exception `conn.wait(self._finish_gen(None))`; with one, `conn.wait(self._finish_gen(exc_val))` inside `try ... except Exception as error:` that logs the warning of 2.4. Returns `None`: the exception of the block propagates. |
| `write(buffer)` | `if self._add(buffer): conn.wait(self._flush_gen())`. |
| `write_row(row)` | `if self._add_row(row): conn.wait(self._flush_gen())`. |
| `read()` | `return conn.wait(self._read_gen())`. |
| `__iter__()` | A generator: `read()` until it returns `b""`, yielding each block. |
| `read_row()` | `return conn.wait(self._read_row_gen())`. |
| `rows()` | A generator: `read_row()` until it returns `None`, yielding each row. |

`Cursor.execute("copy ...")` is still refused (DBAPI D19).

### 2.6 `AsyncCopy` and `AsyncCursor.copy()`

```python
class AsyncCopy(BaseCopy):
    __module__ = "stuntgres"
    async def __aenter__(self) -> AsyncCopy: ...
    async def __aexit__(self, exc_type: type[BaseException] | None,
                        exc_val: BaseException | None, exc_tb: TracebackType | None) -> None: ...
    async def write(self, buffer: Buffer | str) -> None: ...
    async def write_row(self, row: Sequence[Any]) -> None: ...
    async def read(self) -> bytes: ...
    def __aiter__(self) -> AsyncIterator[bytes]: ...       # an async generator
    async def read_row(self) -> tuple[Any, ...] | None: ...
    def rows(self) -> AsyncIterator[tuple[Any, ...]]: ...  # an async generator

# stuntgres/_cur_copy_async.py, merged into AsyncCursor (1.3)
class AsyncCopyCursorMixin:
    def copy(self, statement: Query, params: Params | None = None) -> AsyncCopy: ...
```

The twin of 2.5 line by line: `await conn.lock.acquire()` / `conn.lock.release()` for the
lock (an `asyncio.Lock`), `await conn.wait(...)` for every wait. `copy()` itself is a
plain method; `set_types()` is plain too. `copy_async.py` does not import `asyncio`.

## 3. Named cursors

```python
with conn.cursor("big", scrollable=True) as cur:
    cur.execute("select id, name from item where id > %s order by id", (100,))
    first = cur.fetchmany(50)            # FETCH FORWARD 50 FROM "big"
    for row in cur:                      # FETCH FORWARD 100 at a time (itersize)
        ...
    cur.scroll(0, "absolute")            # MOVE ABSOLUTE 0 FROM "big"
# leaving the block sends CLOSE "big"
```

### 3.1 Classes and state (`stuntgres/_servercur_base.py`)

```python
class ServerCursorBase(OptionalMixin):
    itersize: int                      # rows per round trip of an iteration; 100
    arraysize: int                     # default of fetchmany(); 1 (CurCore)
    def __init__(self, connection: Any, name: str, *, row_factory: RowFactory | None = None,
                 scrollable: bool | None = None, withhold: bool = False) -> None: ...
    @property
    def name(self) -> str: ...
    @property
    def scrollable(self) -> bool | None: ...
    @property
    def withhold(self) -> bool: ...
    @property
    def description(self) -> list[Column] | None: ...
    @property
    def rowcount(self) -> int: ...            # always -1
    @property
    def rownumber(self) -> int | None: ...
```

`ServerCursorBase` derives from `OptionalMixin` (DBAPI 6.9), that is from `CurCore`
(DBAPI 6.1) plus `setinputsizes`, `setoutputsize` and `lastrowid`. It inherits
`connection`, `adapters`, `closed`, `row_factory`, `statusmessage`, `pgresult`,
`_check_usable`, `_convert`, `_select_result` and `repr`. It does not derive from
`FetchMixin`, `ManyMixin` or `NextsetMixin`: those work on a result held by the client.
`ServerCursor` and `AsyncServerCursor` (3.4, 3.5) derive from it and are not subclasses
of `Cursor` / `AsyncCursor` (F7).

| Attribute | Meaning |
|---|---|
| `_name`, `_scrollable`, `_withhold` | The arguments. An empty `name` raises `ProgrammingError("a server-side cursor needs a name")`. |
| `_declared` | `True` from a successful `DECLARE` until the cursor is closed or declared again. |
| `_described` | The result of the Describe of the portal; the source of `description`. Released by `_reset()`. |
| `_columns` | The `Column` list made from `_described`, built on first use. |
| `_spot` | The index of the next row to fetch, `None` when unknown (before `execute`, after a move outside the result). This is `rownumber`. |
| `pgresult`, `_results` | The result of the last `FETCH` (CurCore); `statusmessage` is its tag, for example `FETCH 3`. |

`description` is `None` before `execute()` and from then on the columns of the query,
also before the first fetch and when the query returns no row. `rowcount` is always -1:
the server does not say how many rows a cursor has (PEP 249 allows -1 for "cannot be
determined").

`_reset()` extends `CurCore._reset()`: it also releases `_described` and sets
`_columns` and `_spot` to `None`. (`CurCore.__init__` calls `_reset()` before the
attributes of this class exist, so `__init__` sets `_described = None` first.)

### 3.2 Statements

Every statement the cursor sends, with `"name"` standing for
`transaction.quote_identifier(name)` (DBAPI 5.4: the name between double quotes, each
`"` in it doubled; a NUL character raises `ProgrammingError`) encoded in the client
encoding:

| When | Statement | Sent with |
|---|---|---|
| `execute(query, params)` | `DECLARE "name" CURSOR FOR <query>`; with `scrollable=True`: `DECLARE "name" SCROLL CURSOR FOR <query>`; with `scrollable=False`: `DECLARE "name" NO SCROLL CURSOR FOR <query>`; with `withhold=True` the words ` WITH HOLD` follow `CURSOR` | `send_query_params` (always; the parameters are bound on the server) |
| after the `DECLARE` | Describe of the portal `name` (the unquoted name as bytes) | `send_describe_portal` (PQ 5.8) |
| `fetchone()` | `FETCH FORWARD 1 FROM "name"` | `_exec_command` (DBAPI 4.6) |
| `fetchmany(n)`, an iteration step | `FETCH FORWARD <n> FROM "name"` | `_exec_command` |
| `fetchall()` | `FETCH FORWARD ALL FROM "name"` | `_exec_command` |
| `scroll(...)` | `MOVE ABSOLUTE <target> FROM "name"` | `_exec_command` |
| `close()`, a second `execute()` | `SELECT 1 FROM pg_catalog.pg_cursors WHERE name = $1` with the name as the parameter, then, if it returned a row, `CLOSE "name"` | `send_query_params`, then `_exec_command` |

`<query>` is the caller's statement after the placeholder conversion of ADAPT 2
(`%s` becomes `$1`; without `params` it is sent as written). Because the `DECLARE` goes
through the extended protocol it holds one statement: `select 1; select 2` is refused
with `42601`.

**(measured, experiment 2, oracle mode)** on a table with ids 1 to 7 and the cursor
`select ... where id > $1 order by id` bound to 1 (six rows):

| Statement | Answer |
|---|---|
| `DECLARE "c1" CURSOR FOR ...` inside a transaction | `COMMAND_OK`, tag `DECLARE CURSOR` |
| the same outside a transaction block | `25P01 DECLARE CURSOR can only be used in transaction blocks` |
| `DECLARE "w" CURSOR WITH HOLD FOR ...` outside a transaction block | `COMMAND_OK`; the session stays `IDLE` |
| Describe of portal `c1` | `COMMAND_OK` with 3 fields: `id` 23, `name` 25, `price` 1700 with modifier 524294 |
| `FETCH FORWARD 2 FROM "c1"` | `TUPLES_OK`, tag `FETCH 2` |
| `FETCH FORWARD 1 FROM "c1"` at the end | `TUPLES_OK`, tag `FETCH 0`, no rows |
| `MOVE ABSOLUTE 0 FROM "c1"` | `MOVE 0`; the next fetch returns the first row |
| `MOVE ABSOLUTE 4 FROM "c1"` | `MOVE 1`; the next fetch returns the fifth row |
| `MOVE ABSOLUTE 6 FROM "c1"` (the last row) | `MOVE 1`; the next fetch returns nothing |
| `MOVE ABSOLUTE 7 FROM "c1"`, `MOVE ABSOLUTE 100 FROM "c1"` | `MOVE 0` |
| `MOVE ABSOLUTE 5 FROM "n"` on a `NO SCROLL` cursor at position 3 | `MOVE 1` (forward) |
| `MOVE ABSOLUTE 0 FROM "n"` on that cursor after a fetch | `55000 cursor can only scan forward` |
| `MOVE ABSOLUTE 0 FROM "n"` on a `NO SCROLL` cursor that has not moved | `MOVE 0` |
| `CLOSE "c1"` | tag `CLOSE CURSOR`; a second `CLOSE "c1"`: `34000 cursor "c1" does not exist` |
| a `WITH HOLD` cursor after `COMMIT` | still fetches; a cursor without it: `34000` |
| `DECLARE "My ""Cur""" ...`, Describe of portal `My "Cur"` | both succeed |
| `DECLARE` of a name that exists | `42P03 cursor "d" already exists` |

### 3.3 Generators and algorithms

All generators assume that the caller holds the connection's lock.

`_check_declared()`: `_check_usable()` (closed cursor: `InterfaceError("the cursor is
closed")`; closed connection: `OperationalError`); if not `_declared` raise
`ProgrammingError("no result available: execute a statement first")`.

`_declare_gen(query, params=None) -> PQGen[None]`:

1. `_check_usable()`.
2. If `_declared`: `yield from _close_portal_gen()` (the previous cursor of this name
   is closed first).
3. `_reset()`; `tx = Transformer(self)`; `sql, dumped, oids = self._convert(tx, query,
   params)`.
4. `yield from conn._start_query()` (implicit `BEGIN`, F11).
5. Build the statement of 3.2 and `pgconn.send_query_params(statement, dumped or [],
   oids)`; `results = yield from generators.execute(pgconn)`; `yield from
   check_results(pgconn, results)` (an error raises; nothing of the cursor changed
   except that it was reset); release the results; `_declared = True`.
6. `pgconn.send_describe_portal(name)`; `results = yield from
   generators.execute(pgconn)`; `yield from check_results(pgconn, results)`;
   `_described = results[-1]`; `_tx = tx`; `_spot = 0`.

`_fetch_gen(count) -> PQGen[list[Any]]` (`count` is a positive `int`, or `None` for
`ALL`):

1. `_check_declared()`; `yield from conn._start_query()`.
2. `result = yield from conn._exec_command(statement)` (an error of the server raises;
   the cursor keeps its previous state).
3. Release the previous fetch result, make `result` the current one with
   `_select_result(0)` (the loaders and the row maker are chosen from it, DBAPI 6.4).
4. `rows = tx.load_rows(0, result.ntuples, self._make_row)`; if `_spot` is known add
   `len(rows)` to it; return `rows`.

`_scroll_gen(value, mode="relative") -> PQGen[None]`:

1. `_check_declared()`.
2. `mode == "relative"`: if `_spot` is `None` raise `ProgrammingError("the position of
   the cursor is not known: use scroll(value, 'absolute')")`; if `value == 0` return;
   `target = _spot + value`. `mode == "absolute"`: `target = value`. Any other mode:
   `ValueError("bad mode 'sideways': use 'relative' or 'absolute'")` (DBAPI 6.5 wording).
3. If `target < 0` raise `IndexError("the position is outside the result")`; nothing is
   sent.
4. `yield from conn._start_query()`; `result = yield from
   conn._exec_command(b"MOVE ABSOLUTE <target> FROM " + ident)`; `moved =
   result.command_tuples`; release the result.
5. If `target > 0` and `moved` is 0: `_spot = None`; raise `IndexError("the position is
   outside the result")`. The cursor is then after the last row: fetches return nothing,
   relative moves are refused, an absolute move sets the position again.
6. `_spot = target`.

A target equal to the number of rows is inside the result (the cursor stands on the last
row and the next fetch returns nothing). Backward moves need a scrollable cursor: on a
`NO SCROLL` cursor PostgreSQL answers `55000`, which is raised as
`errors.ObjectNotInPrerequisiteState` and aborts the transaction; on a cursor declared
with `scrollable=None` PostgreSQL allows them for some plans only, so use
`scrollable=True` whenever `scroll()` may move backward.

`_close_portal_gen() -> PQGen[None]`:

1. `_declared = False`. If `conn.closed`: return.
2. `status = pgconn.transaction_status`. If it is `IDLE` and the cursor is not
   `withhold`: return (the cursor ended with its transaction). If it is neither `IDLE`
   nor `INTRANS` (a failed transaction, an unknown state): return.
3. `pgconn.send_query_params(b"SELECT 1 FROM pg_catalog.pg_cursors WHERE name = $1",
   [name], None)`; collect and check the results; `exists = ntuples > 0`.
4. If `exists`: `conn._exec_command(b'CLOSE ' + ident)`.

No `BEGIN` is sent for the two statements: outside a transaction each runs in its own.

`_close_gen() -> PQGen[None]`: if the cursor is closed return; `try:` if `_declared`,
`yield from _close_portal_gen()`; `finally: self._close()` (CurCore: closed, results
released).

### 3.4 `ServerCursor` and `Connection.cursor(name)`

```python
class ServerCursor(ServerCursorBase):
    __module__ = "stuntgres"
    def __enter__(self) -> ServerCursor: ...
    def __exit__(self, exc_type: type[BaseException] | None, exc_val: BaseException | None,
                 exc_tb: TracebackType | None) -> None: ...
    def close(self) -> None: ...
    def execute(self, query: Query, params: Params | None = None) -> ServerCursor: ...
    def executemany(self, query: Query, params_seq: Iterable[Params], *,
                    returning: bool = False) -> None: ...
    def fetchone(self) -> Any: ...
    def fetchmany(self, size: int | None = None) -> list[Any]: ...
    def fetchall(self) -> list[Any]: ...
    def scroll(self, value: int, mode: str = "relative") -> None: ...
    def nextset(self) -> None: ...
    def __iter__(self) -> Iterator[Any]: ...

# stuntgres/_conn_servercur.py, merged into Connection (1.3)
class ServerCursorMixin:
    server_cursor_factory: Any = ServerCursor
```

Every method that sends something is `with conn.lock: conn.wait(<generator>)`.

| Method | Algorithm |
|---|---|
| `execute(query, params=None)` | `_declare_gen(query, params)`; return `self`. |
| `fetchone()` | `rows = _fetch_gen(1)`; the row, or `None`. |
| `fetchmany(size=None)` | `size` defaults to `arraysize`. If `size <= 0`: `_check_declared()` and return `[]` without sending (`FETCH FORWARD 0` would re-read the current row). Else `_fetch_gen(size)`. |
| `fetchall()` | `_fetch_gen(None)`. |
| `scroll(value, mode="relative")` | `_scroll_gen(value, mode)`. |
| `__iter__()` | A generator: loop { `size = self.itersize`; `rows = _fetch_gen(size)`; yield each row; stop when `len(rows) < size` }. |
| `close()` | If already closed return; `_close_gen()`. Closing twice is allowed; closing after the connection was closed sends nothing. |
| `__exit__` | `close()`. |
| `executemany(...)` | Raises `NotSupportedError("executemany() is not supported on a server-side cursor")`. |
| `nextset()` | Returns `None`. |

`Connection.cursor(name, ...)` (DBAPI 9.2) calls `self.server_cursor_factory(self, name,
row_factory=..., scrollable=..., withhold=...)`. The attribute is a class attribute of
`Connection` holding the class `ServerCursor`; assigning another callable to a
connection changes what its `cursor(name)` returns.

### 3.5 `AsyncServerCursor` and `AsyncConnection.cursor(name)`

```python
class AsyncServerCursor(ServerCursorBase):
    __module__ = "stuntgres"
    async def __aenter__(self) -> AsyncServerCursor: ...
    async def __aexit__(self, exc_type: type[BaseException] | None,
                        exc_val: BaseException | None, exc_tb: TracebackType | None) -> None: ...
    async def close(self) -> None: ...
    async def execute(self, query: Query, params: Params | None = None) -> AsyncServerCursor: ...
    async def executemany(self, query: Query, params_seq: Iterable[Params], *,
                          returning: bool = False) -> None: ...
    async def fetchone(self) -> Any: ...
    async def fetchmany(self, size: int | None = None) -> list[Any]: ...
    async def fetchall(self) -> list[Any]: ...
    async def scroll(self, value: int, mode: str = "relative") -> None: ...
    def nextset(self) -> None: ...
    def __aiter__(self) -> AsyncIterator[Any]: ...        # an async generator

# stuntgres/_conn_servercur_async.py, merged into AsyncConnection (1.3)
class AsyncServerCursorMixin:
    server_cursor_factory: Any = AsyncServerCursor
```

The twin of 3.4: `async with conn.lock: await conn.wait(<generator>)`. `cursor(name)`
and `nextset()` stay plain methods, as for `AsyncCursor` (DBAPI 9.5).
`server_cursor_async.py` does not import `asyncio`.

## 4. Two-phase commit

```python
xid = conn.xid(42, "order-7781", "inventory")
conn.tpc_begin(xid)
cur.execute("update stock set n = n - 1 where item = %s", (17,))
conn.tpc_prepare()                 # PREPARE TRANSACTION '42_b3JkZXItNzc4MQ==_aW52ZW50b3J5'
conn.tpc_commit()                  # COMMIT PREPARED '...'

for pending in conn.tpc_recover():     # after a crash of the coordinator
    conn.tpc_rollback(pending)
```

### 4.1 `Xid` (`stuntgres/_xid.py`)

```python
MAX_FORMAT_ID = 0x7FFFFFFF
MAX_PART_BYTES = 64

class Xid:
    __module__ = "stuntgres"
    format_id: int | None
    gtrid: str
    bqual: str | None
    prepared: datetime.datetime | None
    owner: str | None
    database: str | None
    def __init__(self, format_id: int | None, gtrid: str, bqual: str | None,
                 prepared: datetime.datetime | None = None, owner: str | None = None,
                 database: str | None = None) -> None: ...
    @classmethod
    def from_parts(cls, format_id: int, gtrid: str, bqual: str) -> Xid: ...
    @classmethod
    def from_string(cls, text: str) -> Xid: ...
    def __str__(self) -> str: ...
    def __repr__(self) -> str: ...
    def __len__(self) -> int: ...                 # 3
    def __getitem__(self, index: int) -> Any: ...  # (format_id, gtrid, bqual)[index]
    def __eq__(self, other: object) -> bool: ...
    def __hash__(self) -> int: ...

def need_xid(value: Any) -> Xid: ...
```

An `Xid` is the transaction ID object of PEP 249: a sequence of three items
(`format_id`, `gtrid`, `bqual`). `str(xid)` is the identifier PostgreSQL knows the
transaction by. Equality and hash use the three items only. `prepared` (an aware
`datetime`), `owner` and `database` are filled by `tpc_recover()` and are `None`
otherwise.

**The encoding (F13).** For an `Xid` whose `format_id` and `bqual` are not `None`:

```
str(xid) = "<format_id>_<B(gtrid)>_<B(bqual)>"
B(text)  = base64.b64encode(text.encode("utf-8")).decode("ascii")    # standard alphabet, with padding
```

The underscore is not in the base64 alphabet, so the three parts cannot run into each
other. This is the form pgjdbc and psycopg use, so identifiers made by this driver can be
recovered by them and the other way round. Examples **(produced by the designed code)**:

| `xid(...)` | `str(xid)` |
|---|---|
| `(42, "exp3-a", "branch")` | `42_ZXhwMy1h_YnJhbmNo` |
| `(0, "", "")` | `0__` |
| `(1, "a", "b")` | `1_YQ==_Yg==` |
| `(2147483647, "ab", "abc")` | `2147483647_YWI=_YWJj` |
| `(7, "café €", "x_y")` | `7_Y2Fmw6kg4oKs_eF95` |
| `(5, "?>?", "~~~")` | `5_Pz4/_fn5+` |

For any other `Xid` (`format_id` is `None`) `str(xid)` is `gtrid`.

`Xid.from_parts(format_id, gtrid, bqual)` (what `conn.xid()` calls) checks, in this
order:

1. `format_id` is an `int` and not a `bool`, else `TypeError("format_id must be an
   int")`; `0 <= format_id <= 2147483647`, else `ValueError("format_id must be a
   non-negative 32-bit integer")`.
2. For `gtrid`, then `bqual`: a `str`, else `TypeError("gtrid must be a str")`; at most
   64 bytes in UTF-8, else `ValueError("gtrid must be at most 64 bytes")`.

With these limits the identifier has at most 10 + 1 + 88 + 1 + 88 = 188 characters;
PostgreSQL accepts identifiers below 200 bytes **(measured: 199 accepted, 200 refused
with `22023 transaction identifier "..." is too long`)**.

`Xid.from_string(text)`: `text` must be a `str` (`TypeError("a transaction identifier
must be a str")`). If it matches
`^(\d{1,10})_([A-Za-z0-9+/]*={0,2})_([A-Za-z0-9+/]*={0,2})$`, both encoded parts decode
with `base64.b64decode(part, validate=True)` to valid UTF-8, and the number is at most
2147483647, the result is `Xid(number, gtrid, bqual)`. In every other case it is
`Xid(None, text, None)`: an identifier that was not made by `xid()`, kept whole.

`need_xid(value)`: an `Xid` is returned as it is, a `str` goes through `from_string`,
anything else raises `ProgrammingError("a transaction identifier must be a Xid or a
str, got int")` (with the name of the type).

### 4.2 Statements

| Method | Statement | Notes |
|---|---|---|
| `tpc_begin` | the `BEGIN` statement of the connection (`_begin_statement()`, DBAPI 5.3: `BEGIN` with the options of `isolation_level`, `read_only`, `deferrable`) | |
| `tpc_prepare` | `PREPARE TRANSACTION <literal>` | |
| `tpc_commit()` after `tpc_prepare` | `COMMIT PREPARED <literal>` | |
| `tpc_commit()` without `tpc_prepare` | `COMMIT` | one-phase commit |
| `tpc_rollback()` after `tpc_prepare` | `ROLLBACK PREPARED <literal>` | |
| `tpc_rollback()` without `tpc_prepare` | `ROLLBACK` | |
| `tpc_commit(xid)`, `tpc_rollback(xid)` | `COMMIT PREPARED <literal>`, `ROLLBACK PREPARED <literal>` | recovery |
| `tpc_recover` | `SELECT gid, prepared, owner, database FROM pg_catalog.pg_prepared_xacts` | the columns are `text`, `timestamptz`, `name`, `name` **(measured: type OIDs 25, 1184, 19, 19)** |

`<literal>` is `pq.Escaping(conn.pgconn).escape_literal(str(xid).encode(codec))` (PQ
5.14), so an identifier with quotes or backslashes is sent correctly **(measured: the
identifier `it's \ odd` was prepared, listed and rolled back)**. All statements go
through `conn._exec_command` (DBAPI 4.6); none is preceded by an implicit `BEGIN`.

**(measured, experiment 3, oracle mode)**:

| Statement | Answer |
|---|---|
| `PREPARE TRANSACTION '42_ZXhwMy1h_YnJhbmNo'` in a transaction | tag `PREPARE TRANSACTION`; the session is `IDLE`; the changes are not visible |
| `COMMIT PREPARED '...'`, `ROLLBACK PREPARED '...'` | tags `COMMIT PREPARED`, `ROLLBACK PREPARED` |
| the same for an identifier that is not prepared | `42704 prepared transaction with identifier "missing" does not exist` |
| `PREPARE TRANSACTION` of an identifier in use | `42710 transaction identifier "..." is already in use`; the transaction is rolled back (`IDLE`) |
| `COMMIT PREPARED` inside a transaction block | `25001 COMMIT PREPARED cannot run inside a transaction block` |
| `PREPARE TRANSACTION` after an error in the transaction | no error: tag `ROLLBACK`, session `IDLE`, nothing prepared |
| `PREPARE TRANSACTION` with no transaction open | tag `ROLLBACK` and `WARNING 25P01 there is no transaction in progress` |
| a second session | sees the prepared transaction in `pg_prepared_xacts` and can finish it |

### 4.3 State rules

`ConnCore._tpc` (DBAPI 4.3) is `None`, or `(xid, False)` after `tpc_begin`, or `(xid,
True)` after `tpc_prepare`. "Idle" means `pgconn.transaction_status == IDLE`.

| Call | `_tpc is None` | `(xid, False)` | `(xid, True)` |
|---|---|---|---|
| `tpc_begin(xid)` | Needs an idle session and `autocommit` off; sends `BEGIN`; then `_tpc = (xid, False)` | refused | refused |
| `tpc_prepare()` | refused | `PREPARE TRANSACTION`; tag `PREPARE TRANSACTION`: `_tpc = (xid, True)`; another tag: `_tpc = None` and `OperationalError` | refused |
| `tpc_commit()`, `tpc_rollback()` | refused | `COMMIT` / `ROLLBACK` unless the session is idle; `_tpc = None` | `COMMIT PREPARED` / `ROLLBACK PREPARED`; `_tpc = None` |
| `tpc_commit(xid)`, `tpc_rollback(xid)` | Needs an idle session; `COMMIT PREPARED` / `ROLLBACK PREPARED` of that identifier | refused | refused |
| `tpc_recover()` | allowed | allowed | allowed |
| `commit()`, `rollback()`, `transaction()` | as DBAPI 5 | refused (DBAPI 4.6, 5.4) | refused |
| `execute` and everything else | as usual | as usual; the statements belong to the two-phase transaction | as usual: a statement starts a new, ordinary implicit transaction, which `tpc_commit()` cannot finish (PostgreSQL answers `25001`) |

Texts of the refusals (`ProgrammingError`; the method name is the one called):

| Condition | Text |
|---|---|
| `tpc_begin` while `_tpc` is set | `tpc_begin() cannot be used during a two-phase transaction` |
| `tpc_begin` with `autocommit` on | `tpc_begin() cannot be used in autocommit mode` |
| `tpc_begin` when the session is not idle | `tpc_begin() cannot be used inside a transaction (transaction status INTRANS)` |
| `tpc_prepare` without `tpc_begin` | `tpc_prepare() must follow tpc_begin()` |
| `tpc_prepare` twice | `tpc_prepare() cannot be used twice in a two-phase transaction` |
| `tpc_commit()` / `tpc_rollback()` without `tpc_begin` | `tpc_commit() without an xid must follow tpc_begin()` |
| `tpc_commit(xid)` / `tpc_rollback(xid)` while `_tpc` is set | `tpc_commit() with an xid cannot be used during a two-phase transaction` |
| `tpc_commit(xid)` / `tpc_rollback(xid)` when the session is not idle | `tpc_commit() with an xid cannot be used inside a transaction (transaction status INTRANS)` |
| an identifier that is neither `Xid` nor `str` | `a transaction identifier must be a Xid or a str, got int` |

`tpc_prepare()` whose answer is not the tag `PREPARE TRANSACTION` raises
`OperationalError("the transaction was rolled back instead of prepared: it had failed
or was not open")` (F14; `sqlstate` is `None`, the server reported no error).

Errors of the server pass through as the exception of their SQLSTATE. `_tpc` after an
error:

- `tpc_prepare()` failing with an error (for example `42710`): `_tpc` stays `(xid,
  False)`; PostgreSQL has rolled the transaction back; `tpc_rollback()` then sends
  nothing and clears `_tpc`.
- `tpc_commit()` / `tpc_rollback()` without xid: `_tpc` is cleared in a `finally`,
  whatever the statement answered. A prepared transaction whose `COMMIT PREPARED` failed
  is reached again through `tpc_recover()` and `tpc_commit(xid)`.

### 4.4 Generators and methods (`stuntgres/_conn_tpc.py`)

```python
class TpcBaseMixin:                 # merged into BaseConnection
    def xid(self, format_id: int, gtrid: str, bqual: str) -> Xid: ...
    def _tpc_begin_gen(self, xid: Xid | str) -> PQGen[None]: ...
    def _tpc_prepare_gen(self) -> PQGen[None]: ...
    def _tpc_finish_gen(self, action: str, xid: Xid | str | None) -> PQGen[None]: ...
    def _tpc_recover_gen(self) -> PQGen[list[Xid]]: ...

class TpcMixin:                     # merged into Connection
    def tpc_begin(self, xid: Xid | str) -> None: ...
    def tpc_prepare(self) -> None: ...
    def tpc_commit(self, xid: Xid | str | None = None) -> None: ...
    def tpc_rollback(self, xid: Xid | str | None = None) -> None: ...
    def tpc_recover(self) -> list[Xid]: ...

class AsyncTpcMixin:                # merged into AsyncConnection
    async def tpc_begin(self, xid: Xid | str) -> None: ...
    async def tpc_prepare(self) -> None: ...
    async def tpc_commit(self, xid: Xid | str | None = None) -> None: ...
    async def tpc_rollback(self, xid: Xid | str | None = None) -> None: ...
    async def tpc_recover(self) -> list[Xid]: ...
```

`xid()` is `Xid.from_parts(...)`; it sends nothing and is a plain method on both
connection classes. Each `tpc_` method is `with self.lock: self.wait(<generator>)`
(`async with` / `await`).

`_tpc_begin_gen(xid)`: `_check_open()`; `xid = need_xid(xid)`; the three checks of 4.3
in the order of its text table; `result = yield from
self._exec_command(self._begin_statement())`; release it; `_tpc = (xid, False)`.

`_tpc_prepare_gen()`: `_check_open()`; the two checks; `result = yield from
self._exec_command(b"PREPARE TRANSACTION " + literal)`; `tag = result.command_status`;
release it; if `tag != b"PREPARE TRANSACTION"`: `_tpc = None` and raise the
`OperationalError` of 4.3; else `_tpc = (xid, True)`.

`_tpc_finish_gen(action, xid)` with `action` `"COMMIT"` or `"ROLLBACK"`:

1. `_check_open()`.
2. `xid is None`: if `_tpc is None` raise. In a `try` whose `finally` sets `_tpc =
   None`: if prepared, `_exec_command(action + b" PREPARED " + literal)`; else, unless
   the session is idle, `_exec_command(action)`.
3. `xid` given: if `_tpc` is set raise; `xid = need_xid(xid)`; the idle check;
   `_exec_command(action + b" PREPARED " + literal)`.

`_tpc_recover_gen()`: `_check_open()`; `result = yield from
self._exec_command(<the SELECT of 4.2>)`; `tx = Transformer(self)`;
`tx.set_pgresult(result)`; `rows = tx.load_rows(0, result.ntuples)`; release the
result; for each row `(gid, prepared, owner, database)` return
`Xid(x.format_id, x.gtrid, x.bqual, prepared, owner, database)` with `x =
Xid.from_string(gid)`. The list covers every database of the cluster; filter on
`database` where that matters. No transaction is begun: on an idle session the session
is idle afterwards.

## 5. Cluster control and the isolation helper

These names exist only with libspq (`docs/LIBSPQ.md` section 6). They are thin layers
over `stuntgres.pq.ext` (PQ 5.15, CPYTHON-PQ-19-EXT). With another library loaded (the
test seam of PQ 2.2) every one of them raises the `NotSupportedError` of the binder's
stub: `PQXreset is not available in the loaded library` **(measured in oracle and
server mode for all five functions)**. Nothing of this section could run against
libspq; the native expectations in 10.3 come from `docs/LIBSPQ.md` sections 5.1 and 6.

### 5.1 Module functions (`stuntgres/_conn_control.py`)

```python
def libspq_version() -> str: ...
def drop_cluster(host: str = "localhost", port: int | str = 5432) -> None: ...
```

| Function | Algorithm | Raises |
|---|---|---|
| `libspq_version()` | `ext.version().decode("ascii")`: the version of libspq itself, for example `"0.1.0"`. Loads the library if it is not loaded yet. | `NotSupportedError` without libspq |
| `drop_cluster(host="localhost", port=5432)` | `ext.cluster_drop(str(host).encode("utf-8"), str(port).encode("ascii"))`. Forgets the cluster that host and port name (`docs/LIBSPQ.md` 5.1: a connection string names a cluster by host and port), with every database and role in it. The next connection to that name gets a new, empty cluster. Dropping a cluster that does not exist does nothing. | `OperationalError("cluster_drop failed: ...")` while a connection to the cluster is open; `NotSupportedError` without libspq |

Both are exported by the package: `stuntgres.drop_cluster`, `stuntgres.libspq_version`.
`from .pq import ext` is done inside the functions, so importing the package does not
import `ctypes` code paths it does not need.

### 5.2 Connection methods

```python
class ControlMixin:                 # merged into Connection
    def reset_database(self) -> None: ...
    def dump(self) -> bytes: ...
    def restore(self, data: Buffer) -> None: ...

class AsyncControlMixin:            # merged into AsyncConnection
    async def reset_database(self) -> None: ...
    async def dump(self) -> bytes: ...
    async def restore(self, data: Buffer) -> None: ...
```

| Method | libspq function | Effect (`docs/LIBSPQ.md` 6) |
|---|---|---|
| `reset_database()` | `SPQXreset` | Removes every persistent object and all data of the connected database. The cluster, its roles, its other databases and the connection stay. |
| `dump()` | `SPQXdump` | Returns the database as `bytes` (the engine's native dump). |
| `restore(data)` | `SPQXrestore` | Replaces the content of the connected database with a dump. `data` is `bytes`, `bytearray` or `memoryview`. |

Algorithm of each synchronous method, with the connection's lock held:

1. `_check_idle(conn, "<method>")`: `conn._check_open()`; if
   `pgconn.transaction_status` is not `IDLE` raise `ProgrammingError("dump() cannot be
   used inside a transaction (transaction status INTRANS)")` (with the method's name and
   the status). No `BEGIN` is sent: the three functions are not SQL.
2. `_call(conn, "<ext function>", *args)`: `getattr(ext, name)(conn.pgconn, *args)`,
   and in a `finally` `conn._note_status()` (DBAPI 4.3). A failure is the
   `OperationalError` of PQ 5.15 (`reset failed: ...`, `dump failed: ...`, `restore
   failed: ...`).

The three SPQX functions block until they are done and have no nonblocking form; this
is the one place above `stuntgres.pq` that calls a blocking function (rule C9 names the
nonblocking functions for everything that can wait on the server; these calls do not
wait on a statement). The asyncio twins therefore run step 2 in the default executor,
so the event loop is not blocked: `await
asyncio.get_running_loop().run_in_executor(None, _call, self, "<ext function>",
*args)`, with the `asyncio.Lock` held and `import asyncio` inside the method (F25).
libspq allows a connection to be used from another thread between calls
(`docs/LIBSPQ.md` section 7, rule 5).

### 5.3 The isolation helper (`stuntgres/testing.py`)

```python
@contextmanager
def temp_cluster(dbname: str = "test", user: str = "test") -> Iterator[str]: ...
```

```python
from stuntgres.testing import temp_cluster

def test_orders():
    with temp_cluster() as conninfo:              # "host=temp-3f0c... dbname=test user=test"
        with stuntgres.connect(conninfo) as conn:
            ...
    # the cluster and everything in it is gone
```

Algorithm:

1. `host = "temp-" + uuid.uuid4().hex` (no state, so it is safe in threads and in
   forked or spawned workers).
2. `conninfo = make_conninfo(host=host, dbname=dbname, user=user)` (DBAPI 4.1).
3. Yield `conninfo`.
4. When the block ends normally: `drop_cluster(host)`. An open connection makes this
   raise `OperationalError`: a test that leaks a connection fails.
5. When the block raises: `drop_cluster(host)` too, but an `Error` it raises is logged
   (`logger.warning("cluster %s was not dropped: %s", host, exc)`) and the exception of
   the block propagates.

`stuntgres.testing` is a public submodule (like `stuntgres.conninfo`); it is not
imported by `import stuntgres` and its name is not in `stuntgres.__all__`.

The check that DBAPI left for native mode (two connections with the same string share
committed data, different clusters do not) is `ControlTest.test_clusters_do_not_see_
each_other` in `tests/test_control.py` (10.3).

## 6. Wheel

`make cpython` produces one file, for example
`build/cpython/dist/stuntgres-0.1.0-py3-none-manylinux_2_34_x86_64.whl`. It is a wheel
as the binary distribution format specifies
(`https://packaging.python.org/en/latest/specifications/binary-distribution-format/`),
written by `bindings/cpython/tools/build_wheel.py` with the standard library only
(`zipfile`, `hashlib`, `base64`, `csv`): no pip, no `build`, no setuptools, no network.

### 6.1 File contents

The members of the archive, in this order:

| Member | Content |
|---|---|
| `stuntgres/...` | Every file below `bindings/cpython/stuntgres/`, as sorted paths, except directories named `__pycache__` or `_lib` and files ending in `.pyc` or `.pyo`. This includes `stuntgres/py.typed`. |
| `stuntgres/_lib/libspq.so` | The file given with `--libspq` (`build/libspq/lib/libspq.so`). This is the path the loader looks at (`BUNDLED`, PQ 2.1). |
| `stuntgres-<version>.dist-info/METADATA` | Below. |
| `stuntgres-<version>.dist-info/WHEEL` | Below. |
| `stuntgres-<version>.dist-info/RECORD` | One CSV line per member: `path,sha256=<digest>,<size>`, the digest being the URL-safe base64 of the SHA-256 without `=` padding; its own line is `stuntgres-<version>.dist-info/RECORD,,`. Lines end with `\n`. |

`METADATA` (the values come from `pyproject.toml`):

```
Metadata-Version: 2.1
Name: stuntgres
Version: 0.1.0
Summary: PEP 249 driver for the embedded stuntgres engine
Requires-Python: >=3.10
```

`WHEEL`:

```
Wheel-Version: 1.0
Generator: stuntgres build_wheel
Root-Is-Purelib: false
Tag: py3-none-manylinux_2_34_x86_64
```

The tag is `py3-none-<platform>`: any Python 3 (the package is pure Python and
`Requires-Python` states the lower bound), no ABI (ctypes, no extension module), one
platform (the library). Every member is stored with the time stamp 2020-01-01 00:00:00,
deflate compression and the mode `0644` (`0755` for the library), so identical input
gives a byte-identical wheel **(measured)**. Tests, tools and `pyproject.toml` are not
in the wheel.

**(measured, experiment 4)** with a stand-in for `libspq.so` (a cgo shared library that
links the whole engine, 83 MB): the wheel is 34 MB and is built in 2.5 s; `python3 -m pip
install --no-index --no-deps --target <dir> <wheel>` (pip 24.0) installs it; `python3 -m
zipfile -e <wheel> <dir>` gives the same tree; every member matches `RECORD`; `import
stuntgres` from that directory works, maps no library, and
`stuntgres.pq._loader.BUNDLED` is the unpacked `stuntgres/_lib/libspq.so`.

### 6.2 The builder (`tools/build_wheel.py`)

```sh
python3 bindings/cpython/tools/build_wheel.py [--libspq PATH] [--out DIR]
```

`--libspq` defaults to `build/libspq/lib/libspq.so` and `--out` to `build/cpython/dist`,
both at the repository root. On success it prints the path of the wheel on standard
output and exits 0; on failure it prints `build_wheel: <reason>` on standard error and
exits 1.

```python
class BuildError(Exception): ...
def read_metadata(root: str) -> dict[str, str]: ...
def package_files(root: str) -> list[str]: ...
def metadata_text(meta: dict[str, str]) -> str: ...
def wheel_text(tag: str) -> str: ...
def build(libspq: str, out_dir: str, root: str = ROOT) -> str: ...
def verify(path: str) -> list[str]: ...
def main(argv: Sequence[str] | None = None) -> int: ...
```

`build(libspq, out_dir, root)`:

1. If `libspq` is not a file raise `BuildError(f"{libspq} not found: run 'make libspq'
   first")`.
2. `meta = read_metadata(root)` (6.4).
3. `tag = elfneeds.platform_tag(libspq)` (6.3); an `ElfError` becomes a `BuildError`
   with the same text.
4. Create `out_dir`; delete every `stuntgres-*.whl` in it (so the directory holds
   exactly one wheel and a shell pattern finds it).
5. Write `<out_dir>/stuntgres-<version>-py3-none-<tag>.whl.part`: the members of 6.1 in
   order, collecting `(name, digest, size)` for `RECORD`; then `os.replace` it to the
   final name. A failure removes the partial file.
6. Return the path.

`verify(path)` re-reads a wheel: exactly one `RECORD`; its lines name the members in
archive order; every digest and size matches; the line of `RECORD` itself has empty
digest and size. It returns the member names and raises `BuildError` otherwise. `main`
calls `build` and then `verify`.

### 6.3 The platform tag (`tools/elfneeds.py`)

```python
class ElfError(Exception): ...
class Needs(NamedTuple):
    machine: str                    # "x86_64" or "aarch64"
    libraries: tuple[str, ...]      # DT_NEEDED entries, in file order
    versions: tuple[str, ...]       # symbol versions it needs, sorted, unique
def read_needs(path: str) -> Needs: ...
def glibc_version(needs: Needs) -> tuple[int, int]: ...
def platform_tag(path: str) -> str: ...
```

The rule (F19): **the tag says what the library itself needs**, read from the ELF file,
not from the host that builds it.

`read_needs(path)` reads a 64-bit little-endian ELF shared object:

1. The file starts with `\x7fELF`, class 2, data 1; `e_type` is 3 (`ET_DYN`);
   `e_machine` is 62 (`x86_64`) or 183 (`aarch64`). Otherwise `ElfError` (texts in the
   file).
2. From the section headers: every `DT_NEEDED` entry (tag 1) of the section of type
   `SHT_DYNAMIC` (6), and every version name of the section of type `SHT_GNU_verneed`
   (`0x6ffffffe`); both use the string table their `sh_link` names.

`glibc_version(needs)`: start from `(2, 17)`, the manylinux2014 baseline and the oldest
glibc this project targets; for every version name: `GLIBC_PRIVATE` raises `ElfError`;
`GLIBC_ABI_DT_RELR` raises the result to at least `(2, 36)` (the first glibc that loads
such a library); a name `GLIBC_<a>.<b>` or `GLIBC_<a>.<b>.<c>` raises it to at least
`(a, b)`; other names (`OPENSSL_3.0.0`, ...) are ignored here.

`platform_tag(path)`: `needs = read_needs(path)`; if a needed library is not one of
glibc's own (`libc.so.6`, `libpthread.so.0`, `libdl.so.2`, `libm.so.6`, `librt.so.1`,
`libresolv.so.2`, `libutil.so.1`, `ld-linux-x86-64.so.2`, `ld-linux-aarch64.so.1`) raise
`ElfError(f"{path}: needs libssl.so.3; a wheel may rely only on glibc")`; return
`f"manylinux_{major}_{minor}_{machine}"`.

**(measured, experiment 5, host with glibc 2.39)**:

| Library | Needed libraries | Highest versions | Result |
|---|---|---|---|
| the stand-in for `libspq.so` (cgo, `-buildmode=c-shared`, the whole engine linked in) | `libc.so.6` | `GLIBC_2.32`, `GLIBC_2.34` | `manylinux_2_34_x86_64` |
| `libpq.so.5.18` of the pinned image | `libssl.so.3`, `libcrypto.so.3`, `libgssapi_krb5.so.2`, `libm.so.6`, `libldap-2.5.so.0`, `libc.so.6` | | refused: `needs libssl.so.3, libcrypto.so.3, libgssapi_krb5.so.2, libldap-2.5.so.0` |
| `/lib/x86_64-linux-gnu/libm.so.6` | `libc.so.6`, `ld-linux-x86-64.so.2` | `GLIBC_2.36`, `GLIBC_ABI_DT_RELR`, `GLIBC_PRIVATE` | refused: private glibc symbols |
| `_ctypes_test` of CPython 3.12 | `libm.so.6`, `libc.so.6` | `GLIBC_2.2.5`, `GLIBC_2.3.4` | `manylinux_2_17_x86_64` |

The same values are printed by `readelf -d` and `readelf -V`.

Consequence of the rule: the wheel built on this host is accepted by pip on every
x86_64 system with glibc 2.34 or newer (pip 24.0 on glibc 2.39 lists
`py3-none-manylinux_2_34_x86_64` among its compatible tags **(measured)**), which is
exactly where the dynamic loader can resolve the library's symbols. Tagging by the build
host's glibc (2.39) would be simpler and never wrong, but would refuse installation on
the systems in between. What the rule does not prove: that nothing else in the library
depends on a newer system (kernel features, for instance). `docs/LIBSPQ.md` section 7,
rule 1 already limits libspq to Linux with glibc; musl is excluded by the tag.

### 6.4 The version

One source: `bindings/cpython/stuntgres/_version.py` (`__version__ = "0.1.0"`, PQ 1.2).
`pyproject.toml` repeats it in `version = "0.1.0"`, as CPYTHON-BUILD-01-SKELETON wrote
it and its test asserts. `read_metadata(root)` reads `name`, `version`, `description`
and `requires-python` from `pyproject.toml` with one regular expression per key (a line
`key = "value"`; no TOML library, because `tomllib` is not in Python 3.10), reads
`__version__` the same way, and raises `BuildError("version mismatch:
stuntgres/_version.py says 0.2.0, pyproject.toml says 0.1.0")` when they differ. A
release changes both files in one commit; the wheel cannot be built from a tree where
they disagree.

### 6.5 `pyproject.toml` and pip

CPYTHON-MAKE-01-WHEEL appends to `bindings/cpython/pyproject.toml`:

```toml

[build-system]
requires = []
build-backend = "build_wheel"
backend-path = ["tools"]
```

`tools/build_wheel.py` is thereby also the PEP 517 build backend of the directory:

```python
def get_requires_for_build_wheel(config_settings: Any = None) -> list[str]: ...   # []
def build_wheel(wheel_directory: str, config_settings: Any = None,
                metadata_directory: Any = None) -> str: ...
def build_sdist(sdist_directory: str, config_settings: Any = None) -> str: ...     # refuses
```

`build_wheel` calls `build(<library>, wheel_directory, os.getcwd())` and returns the
file name; the library is `config_settings["libspq"]` if given, else the environment
variable `STUNTGRES_LIBSPQ` if set, else `build/libspq/lib/libspq.so` of the repository.
A `BuildError` becomes `SystemExit("build_wheel: <reason>")`. `build_sdist` always
exits with `build_wheel: no source distribution: the wheel carries a native library
built by 'make libspq'`.

What pip does with the directory **(measured, pip 24.0, `--no-index`)**:

| Tree | `pip install --no-index --no-deps --target <dir> ./bindings/cpython` |
|---|---|
| with the table, library built | builds the wheel of 6.1 through the backend and installs it; nothing is downloaded and nothing is written into the source tree |
| with the table, no library | fails with `build_wheel: <path>/build/libspq/lib/libspq.so not found: run 'make libspq' first` |
| without the table | pip falls back to setuptools and fails with `Could not find a version that satisfies the requirement setuptools>=40.8.0`; with a network it would build and install a pure-Python package without `libspq.so`, which then fails at the first `connect` |

`make cpython` does not use pip: it runs the builder directly.

## 7. Makefile

### 7.1 The text

CPYTHON-MAKE-02-TARGETS makes two changes to the root `Makefile`, after
LIBSPQ-BUILD-01-SKELETON has added its block (`docs/design/LIBSPQ-CABI.md` 8.3):

1. Append ` cpython cpython-check` to the end of the existing `.PHONY` line, whatever
   that line contains by then.
2. Append this block at the end of the file, after one empty line **(measured,
   experiment 6: `make -n cpython` and `make -n cpython-check` on a copy of the Makefile
   with the libspq block print the commands of 7.2)**:

```make
# cpython: the Python DB-API binding (docs/CPYTHON.md). These targets need python3
# (3.10 or newer) and what libspq needs. They are not part of quickcheck.
# cpython-check also takes REFERENCE=1 (adds oracle mode; needs Docker) and SERVER=1
# (adds server mode; builds cmd/stuntgres).
PYTHON ?= python3
SERVER ?= 0
CPYTHON_SRC = bindings/cpython
CPYTHON_OUT = build/cpython
CPYTHON_SITE = $(CPYTHON_OUT)/site
CPYTHON_RUN = $(PYTHON) -W error $(CPYTHON_SRC)/tools/runinstalled.py $(CPYTHON_SITE)

# Build the wheel that carries libspq.so.
cpython: libspq
	$(PYTHON) $(CPYTHON_SRC)/tools/build_wheel.py --libspq $(LIBSPQ_OUT)/lib/libspq.so --out $(CPYTHON_OUT)/dist

# Build the wheel, unpack it into a scratch directory and test the unpacked package.
cpython-check: cpython
	rm -rf $(CPYTHON_SITE)
	$(PYTHON) -m zipfile -e $(CPYTHON_OUT)/dist/stuntgres-*.whl $(CPYTHON_SITE)
	$(CPYTHON_RUN) --mode unit
	$(CPYTHON_RUN) --mode native
	$(PYTHON) -I -W error $(CPYTHON_SRC)/tools/wheel_smoke.py $(CPYTHON_SITE)
	$(if $(filter 1,$(REFERENCE)),$(CPYTHON_RUN) --mode oracle)
	$(if $(filter 1,$(SERVER)),$(CPYTHON_RUN) --mode server)
```

Recipe lines start with a tab. The block uses `LIBSPQ_OUT` of the libspq block and
`REFERENCE` of the top of the file (`REFERENCE ?= 0`); it defines `PYTHON` and
`SERVER`. Neither target is added to `quickcheck`, `finalcheck` or `deps`: the binding is
not Go code. `build/` is git-ignored by LIBSPQ-BUILD-01-SKELETON; until then
`build/cpython/.gitignore` of the runner covers it (PQ 1.2).

| Variable | Default | Meaning |
|---|---|---|
| `PYTHON` | `python3` | The interpreter for the builder, the runner and the smoke test (CPython 3.10 or newer). |
| `REFERENCE` | `0` | `1`: `cpython-check` also runs the suite in oracle mode (Docker, the pinned image). |
| `SERVER` | `0` | `1`: `cpython-check` also runs the suite in server mode (builds `cmd/stuntgres`; needs Docker once for libpq, PQ 9.3). |

### 7.2 What each target runs

`make cpython`:

```
(the recipe of libspq)
python3 bindings/cpython/tools/build_wheel.py --libspq build/libspq/lib/libspq.so --out build/cpython/dist
```

`make cpython-check REFERENCE=1 SERVER=1`, after the lines of `make cpython`:

```
rm -rf build/cpython/site
python3 -m zipfile -e build/cpython/dist/stuntgres-*.whl build/cpython/site
python3 -W error bindings/cpython/tools/runinstalled.py build/cpython/site --mode unit
python3 -W error bindings/cpython/tools/runinstalled.py build/cpython/site --mode native
python3 -I -W error bindings/cpython/tools/wheel_smoke.py build/cpython/site
python3 -W error bindings/cpython/tools/runinstalled.py build/cpython/site --mode oracle
python3 -W error bindings/cpython/tools/runinstalled.py build/cpython/site --mode server
```

Without the two variables the last two lines are absent. The "scratch install" is the
unpacked wheel in `build/cpython/site`: unpacking with `zipfile` is all that installing
a wheel without scripts means, and needs no pip. Agents set an external deadline of 1800
s for `cpython-check` (900 s more for each of the two optional modes).

### 7.3 The suite against the unpacked wheel (`tools/runinstalled.py`)

```sh
python3 bindings/cpython/tools/runinstalled.py SITE --mode <mode> [selector ...]
```

The runner of PQ 9.1 puts `bindings/cpython` first on `sys.path` and is not to be
edited. `runinstalled.py` runs it against another copy of the package without changing
it:

1. No argument, or a first argument that starts with `-`: print `usage: runinstalled.py
   SITE --mode <mode> [selector ...]` on standard error, return 2.
2. `site = os.path.abspath(SITE)`. If `site/stuntgres/__init__.py` is not a file: print
   `runinstalled: no package stuntgres in <site>`, return 3.
3. `sys.path.insert(0, site)`; `import stuntgres`; if its directory is not
   `site/stuntgres`: print `runinstalled: stuntgres was imported from <dir>, not from
   <site>/stuntgres`, return 3.
4. `import runtests` (the script's directory, `tools/`, is on `sys.path`) and return
   `runtests.main(<the remaining arguments>)`.

The runner then inserts the source tree before `site`, but `stuntgres` is already in
`sys.modules`, and its submodules are found through the package's own path: the tests
(`tests/`, from the source tree) exercise the unpacked package **(measured: a copy of
the package with another version string made `test_skeleton.SkeletonTest.test_version`
fail; 251 unit tests and the oracle-mode tests of three files passed against the
unpacked wheel)**. Tests that start a fresh interpreter (`tests/test_module.py`,
`tests/test_skeleton.py`) set `PYTHONPATH` to the source tree themselves and keep
testing that tree.

In native mode the backend of PQ 9.7 sets `STUNTGRES_LIBSPQ` to
`build/libspq/lib/libspq.so`, which wins over the bundled copy (PQ 2.3): the suite then
loads the build tree's library, which is the file the wheel carries. That the bundled
copy itself loads is what the smoke test checks (9.4).

## 8. Documentation

CPYTHON-DOCS-01-FINAL writes what follows, after every other CPYTHON part. It edits no
code. Each text is final; where a file was changed meanwhile by another part
(LIBSPQ-DOCS-01-FINAL, the JDBC binding's documentation part, a cleanup part), the text
is inserted at the place named and the other part's lines are left as they are. Follow
`AGENTS.md` "Documentation rules": present tense, contracts only, no history.

### 8.1 `bindings/cpython/README.md` (replaces the file of CPYTHON-BUILD-01-SKELETON)

````markdown
# stuntgres for Python

`stuntgres` is a PEP 249 (DB-API 2.0) driver for the embedded stuntgres engine: the
database runs inside the Python process, so the tests of an application written for
PostgreSQL need no server. It is pure Python over `ctypes` and the native library
`libspq`, uses the standard library only, and needs CPython 3.10 or newer on Linux with
glibc (amd64, arm64).

```python
import stuntgres

with stuntgres.connect("host=my-test dbname=app") as conn:
    conn.execute("create table item (id int primary key, name text)")
    conn.execute("insert into item values (%s, %s)", (1, "pen"))
    print(conn.execute("select * from item").fetchall())      # [(1, 'pen')]
```

The consumer contract is [docs/CPYTHON.md](../../docs/CPYTHON.md): connecting, the API,
types, transactions, COPY, server-side cursors, two-phase commit, asyncio, test
isolation, threads and fork. The C library underneath is described in
[docs/LIBSPQ.md](../../docs/LIBSPQ.md).

## Build

```sh
make cpython          # build/cpython/dist/stuntgres-<version>-py3-none-manylinux_<glibc>_<arch>.whl
python3 -m pip install build/cpython/dist/stuntgres-*.whl
```

The wheel carries `libspq.so`; nothing else is needed at run time. `make cpython` needs
Go with cgo, a C compiler and `python3`; it uses neither pip nor a network.

## Layout

| Path | Content |
|---|---|
| `stuntgres/` | The package. `stuntgres/pq/` is the ctypes layer over libspq. |
| `tests/` | The test suite (`unittest`); `tests/support/` starts the backends; `tests/third_party/dbapi20.py` is the vendored DB-API compliance suite and is never edited. |
| `tools/runtests.py` | The test runner; the only way the tests are run. |
| `tools/build_wheel.py`, `tools/elfneeds.py` | The wheel builder (also the PEP 517 backend named in `pyproject.toml`). |
| `tools/runinstalled.py`, `tools/wheel_smoke.py` | The suite and a smoke test against an unpacked wheel (`make cpython-check`). |
| `tools/gen_sqlstates.py`, `tools/sqlstates.txt` | Generator and table of `stuntgres/_sqlstates.py`. |

## Tests

```sh
python3 bindings/cpython/tools/runtests.py --mode unit      # no native library
python3 bindings/cpython/tools/runtests.py --mode oracle    # libpq 18 -> pinned PostgreSQL 18.6 (Docker)
python3 bindings/cpython/tools/runtests.py --mode server    # libpq 18 -> cmd/stuntgres
python3 bindings/cpython/tools/runtests.py --mode native    # libspq (after make libspq)
make cpython-check                                          # wheel, unit and native mode, smoke test
```

A selector after the mode runs one module, class or method
(`--mode oracle test_copy.CopyTest.test_write_row`). Expected values in the tests were
recorded from PostgreSQL 18.6 in oracle mode; a test that passes there and fails in
server or native mode shows a difference of the engine or of libspq, not a reason to
change the test.

Invariants: only `stuntgres/pq/_loader.py` loads the native library, and never at
import; above `stuntgres/pq` only the nonblocking functions are used and the waiting is
done in Python; never run Python with `stuntgres/` as the current directory (its
`types` package would hide the standard library's). The design is in
`docs/design/CPYTHON-PQ.md`, `CPYTHON-ADAPT.md`, `CPYTHON-DBAPI.md` and
`CPYTHON-FEATURES.md`.
````

### 8.2 `docs/CPYTHON.md`

This design completes the document (sections 2, 4.5, 4.6, 10 and 11; small additions in
4.1, 4.2 and 8). CPYTHON-DOCS-01-FINAL replaces only its **Status** paragraph by:

```markdown
**Status.** Implemented as specified here. The package is built by `make cpython` and
verified by `make cpython-check` (section 11). Internal design:
`docs/design/CPYTHON-PQ.md`, `CPYTHON-ADAPT.md`, `CPYTHON-DBAPI.md` and
`CPYTHON-FEATURES.md`.
```

If CPYTHON-NATIVE-99-FINAL recorded a behavior that differs from the document, the part
corrects that sentence of the document and lists the correction in its commit body.

### 8.3 `AGENTS.md`

"Where to look": after the line for `docs/LIBSPQ.md` (added by LIBSPQ-DOCS-01-FINAL;
if it is absent, after the `docs/DEVELOPMENT.md` line) add:

```markdown
- [docs/CPYTHON.md](docs/CPYTHON.md): the Python DB-API driver (`bindings/cpython`).
```

"Package map": after the bullet for `bindings/libspq` (if it is absent, after the
`dual` bullet) add:

```markdown
- `bindings/cpython`: the Python DB-API driver, package `stuntgres`: pure Python over
  `ctypes` and libspq, standard library only. Not Go code and not part of
  `make quickcheck`; built by `make cpython`, tested by `make cpython-check` and by
  `python3 bindings/cpython/tools/runtests.py --mode <unit|oracle|server|native>`. Read
  its [README](bindings/cpython/README.md) before editing it.
```

"Commands", in the `sh` block, after the libspq lines (if absent, after `make deps`):

```sh
make cpython                      # Python wheel with libspq.so (needs python3 >= 3.10)
make cpython-check                # wheel + Python suite: unit, native; REFERENCE=1, SERVER=1 add modes
```

### 8.4 `docs/DEVELOPMENT.md`

"Make targets", two rows after the libspq rows (if absent, after `make deps`):

```markdown
| `make cpython` | Build the Python wheel `build/cpython/dist/stuntgres-<version>-py3-none-manylinux_<glibc>_<arch>.whl`: the package `bindings/cpython/stuntgres` plus `libspq.so`. Depends on `make libspq`; needs `python3` (CPython 3.10 or newer), no pip and no network. `PYTHON=` chooses the interpreter. |
| `make cpython-check` | Build the wheel, unpack it into `build/cpython/site` and run the Python suite against it in unit and native mode, then the wheel smoke test. `REFERENCE=1` adds oracle mode (Docker), `SERVER=1` adds server mode (builds `cmd/stuntgres`). Not part of `quickcheck`. Deadline 1800 s, plus 900 s for each added mode. |
```

"Dependencies", one bullet at the end of the list:

```markdown
- `bindings/cpython` is Python and has no dependency beyond the CPython standard
  library (3.10 or newer), at run time, in its tests and in its build tools; it is not
  covered by `make deps`. Its one third-party file is the vendored public-domain
  compliance suite `bindings/cpython/tests/third_party/dbapi20.py`, which is test-only
  and never edited. Oracle and server mode of its suite load libpq 18 out of the pinned
  PostgreSQL image; nothing of that is installed or committed.
```

### 8.5 `docs/TESTING.md`

"Placement policy", one bullet after the bullet about the C++ suite of
`bindings/libspq/tests` (if it is absent, after the first bullet):

```markdown
- The Python suite in `bindings/cpython/tests` is a sanctioned binding suite that
  issues SQL through a public API outside `reference/`, by owner decision. It tests the
  Python driver (parameter conversion, type adaptation, the DB-API surface, COPY,
  cursors, two-phase commit, asyncio, threads and fork) and uses SQL to produce the
  results the driver must handle; SQL behavior belongs in `reference/`. It runs in three
  database modes with one set of expectations, recorded from PostgreSQL 18.6: **oracle**
  (libpq 18 against the pinned PostgreSQL image), **server** (libpq 18 against
  `cmd/stuntgres`) and **native** (libspq), plus a **unit** mode without a native
  library. A test that passes in oracle mode and fails in server or native mode shows an
  engine or libspq difference: add a dual-mode reference test for it and fix the engine;
  never change the Python expectation. Run it with
  `python3 bindings/cpython/tools/runtests.py --mode <mode>` or `make cpython-check`.
```

### 8.6 `docs/LIBSPQ.md`, section 8

The paragraph that starts with **Python.** is replaced by (the other paragraphs of the
section, and section 6, which LIBSPQ-EXT parts may edit, are not touched):

```markdown
**Python.** `bindings/cpython` is a self-contained PEP 249 driver, package `stuntgres`
(`docs/CPYTHON.md`): `ctypes` declarations over libspq, a `pq` layer with the names of
psycopg 3's `pq` abstraction (`psycopg/pq/abc.py`) for every function libspq has,
generators above the nonblocking functions, and waiting on the descriptor in Python,
synchronously and with asyncio. It loads the library at the first connection with all
signals blocked, refuses to be used in a forked child, and calls `SPQcancel` on Ctrl-C
and on task cancellation. psycopg itself cannot be pointed at a third-party `pq`
implementation, so a psycopg adapter (and with it SQLAlchemy and Django) is separate,
later work that can reuse the `pq` layer. `ctypes` releases the GIL around calls and
re-acquires it for callbacks. Fork-based runners (`multiprocessing` fork start method,
`pytest-forked`, preloading servers) need rule 3; `pytest-xdist` workers are separate
processes and each gets its own engine.
```

## 9. Native verification

Plan U4: the implementation parts carry no LIBSPQ prerequisite and are proved in unit,
oracle and server mode. Once libspq exists, the `CPYTHON-NATIVE-` parts run each test
family against it. Nothing in this section could be run: `build/libspq` does not exist
(every LIBSPQ implementation part is pending).

### 9.1 What native mode needs before anything runs

The binder of PQ 3.2 looks up every row of the declaration table when the library is
first used and raises `InterfaceError("... lacks the required function SPQ...")` when a
required one is missing. So native mode has no partial state: until every required
function exists, not even a connection can be made. **(experiment 7: the table of PQ
3.4 against the catalog records)** 94 functions are bound, 82 of them required; each is
provided by exactly one LIBSPQ part:

| LIBSPQ part | Required functions it provides |
|---|---|
| `LIBSPQ-BUILD-01-SKELETON` | `SPQlibVersion` |
| `LIBSPQ-CABI-01-CONNECT` | 16: `SPQconnectdb`, `SPQfinish`, `SPQstatus`, `SPQtransactionStatus`, `SPQerrorMessage`, the session accessors, `SPQsocket` |
| `LIBSPQ-CABI-02A-BLOCK` | 11: `SPQsendQuery`, `SPQgetResult`, `SPQclear`, the first result accessors |
| `LIBSPQ-CABI-02-RESULT` | 18: the remaining accessors, `SPQfreemem`, `SPQconsumeInput`, `SPQisBusy`, `SPQflush`, `SPQsetnonblocking`, `SPQisnonblocking` |
| `LIBSPQ-CABI-03-BLOCKING` | `SPQexec` |
| `LIBSPQ-CABI-04-PREPARED` | 10: `SPQsendQueryParams`, `SPQexecParams`, prepare, describe |
| `LIBSPQ-CABI-05-ERRORS` | `SPQresultErrorField`, `SPQsetNoticeReceiver` |
| `LIBSPQ-CABI-06-ESCAPE` | the 7 escaping functions |
| `LIBSPQ-CABI-07-CONNINFO` | `SPQconninfoParse`, `SPQconndefaults`, `SPQconninfo`, `SPQconninfoFree` |
| `LIBSPQ-CABI-08-CANCEL` | `SPQgetCancel`, `SPQcancel`, `SPQfreeCancel` |
| `LIBSPQ-CABI-09-COPY` | `SPQputCopyData`, `SPQputCopyEnd`, `SPQgetCopyData` |
| `LIBSPQ-CABI-10-NOTIFY` | `SPQnotifies` |
| `LIBSPQ-CABI-11-ROWMODES` | `SPQsetSingleRowMode` |
| `LIBSPQ-CABI-12-CONNECT-ASYNC` | `SPQconnectStart`, `SPQconnectPoll`, `SPQreset`, `SPQping` |

The 12 optional functions come from `LIBSPQ-BUILD-01-SKELETON` (`SPQXversion`),
`LIBSPQ-CABI-04-PREPARED` (the four close functions), `-10-NOTIFY` (`SPQsocketPoll`,
`SPQgetCurrentTimeUSec`), `-11-ROWMODES` (`SPQsetChunkedRowsMode`), `-14-CONTROL`
(`SPQXclusterDrop`, `SPQXreset`) and `-15-DUMP` (`SPQXdump`, `SPQXrestore`).
`LIBSPQ-CABI-13-ENCODING` provides nothing the binding binds (PQ 3.5).

The native backend (PQ 9.7) also drops a cluster per test, which needs
`LIBSPQ-CABI-14-CONTROL`. Therefore every part that runs anything in native mode needs
`LIBSPQ-CABI-01` to `-12` and `-14`. CPYTHON-TEST-03-NATIVE is the part all of them
depend on; its record listed only `LIBSPQ-CABI-03-BLOCKING` and
`LIBSPQ-CABI-14-CONTROL`, which reach `-01`, `-02A` and `-02` but none of `-04` to
`-12`. The final audit of this design added those nine to its prerequisites (section
11); CPYTHON-PQ-19-EXT and every native part inherit them through it.

### 9.2 The family-to-LIBSPQ mapping

One native part per test family. "Family" is a set of implementation parts whose tests
the native part runs. The LIBSPQ column lists the parts that provide the functions the
family's tests reach beyond the first five (`BUILD-01`, `CABI-01`, `-02A`, `-02`,
`-03`); it was derived from the pq members each family calls (PQ 5.16) and the
functions each LIBSPQ record names, and is printed by `exp7/mapping.py`. Every DB-API
connection reaches `CABI-04` (parameters), `-05` (notice receiver, error fields), `-07`
(the connection string check), `-08` (the cancel handle made at connect time), `-10`
(the notification check of every fetch) and `-12` (the connect generator), so those six
appear in every family above the pq layer.

| Part | Runs the tests of (CPYTHON-) | LIBSPQ parts of the family (LIBSPQ-) |
|---|---|---|
| CPYTHON-NATIVE-01-PQ-CORE | PQ-01-ERRORS-BASE, PQ-02-ENUMS, PQ-03-LOADER, PQ-04-FFI, PQ-05-CONNINFO, PQ-06A-PGCONN-BASE, PQ-06-PGCONN-CONNECT, PQ-07-PGRESULT, PQ-08-EXEC, TEST-01A-SUPPORT, TEST-01S-STARTER, TEST-03-NATIVE | CABI-04-PREPARED, CABI-07-CONNINFO, CABI-12-CONNECT-ASYNC |
| CPYTHON-NATIVE-02-PQ-EXTRAS | PQ-09-PREPARED, PQ-10-NOTICES, PQ-11-NOTIFY, PQ-12-CANCEL, PQ-13-COPY, PQ-14-ROWMODES, PQ-15-ESCAPING | CABI-04-PREPARED, CABI-05-ERRORS, CABI-06-ESCAPE, CABI-08-CANCEL, CABI-09-COPY, CABI-10-NOTIFY, CABI-11-ROWMODES |
| CPYTHON-NATIVE-03-GENERATORS | PQ-16-GENERATORS, PQ-17-WAIT-SYNC, PQ-18-WAIT-ASYNC | CABI-05-ERRORS, CABI-08-CANCEL, CABI-10-NOTIFY, CABI-12-CONNECT-ASYNC |
| CPYTHON-NATIVE-04-ADAPT | QUERY-01-CONVERT, ADAPT-01A-MAP, ADAPT-01B-TRANSFORMER, ADAPT-02-OIDS, ADAPT-02B-OIDS-CATALOG, ADAPT-03-NUMERIC, ADAPT-04-STRING, ADAPT-05-DATE-TIME, ADAPT-06A-TIMESTAMP, ADAPT-06B-INTERVAL, ADAPT-07-UUID-JSON, ADAPT-08-ARRAY-LOAD, ADAPT-09-ARRAY-DUMP, ADAPT-10-DBAPI20-TYPES, ADAPT-11-DEFAULTS, ADAPT-12A-ROUNDTRIP-BASIC, ADAPT-12B-ROUNDTRIP-DATETIME, ADAPT-12C-ROUNDTRIP-COMPOSITE | CABI-04-PREPARED, CABI-05-ERRORS, CABI-07-CONNINFO, CABI-08-CANCEL, CABI-10-NOTIFY, CABI-12-CONNECT-ASYNC |
| CPYTHON-NATIVE-05-CONNECTION | ERRORS-01-DIAGNOSTIC, ERRORS-02-SQLSTATE, ROWS-01-FACTORIES, CONN-01A-CONNINFO, CONN-01B-INFO, CONN-01C-CORE, CONN-01D-BASE, CONN-01E-SYNC, CONN-02-EXEC, CONN-03-TXN-OPTIONS, CONN-04-TRANSACTION, CONN-05-NOTICES, CONN-06-NOTIFY, CONN-07-CANCEL | CABI-04-PREPARED, CABI-05-ERRORS, CABI-07-CONNINFO, CABI-08-CANCEL, CABI-10-NOTIFY, CABI-12-CONNECT-ASYNC |
| CPYTHON-NATIVE-06-CURSOR | CUR-01A-COLUMN, CUR-01B-RESULTS, CUR-01C-CORE, CUR-01D-EXECUTE-TESTS, CUR-02-FETCH, CUR-03-EXECUTEMANY, CUR-04-NEXTSET, CUR-05-OPTIONAL | CABI-04-PREPARED, CABI-05-ERRORS, CABI-07-CONNINFO, CABI-08-CANCEL, CABI-09-COPY, CABI-10-NOTIFY, CABI-12-CONNECT-ASYNC |
| CPYTHON-NATIVE-07-ASYNC | ASYNC-01-CONNECTION, ASYNC-02-CURSOR, ASYNC-03-TRANSACTION | CABI-04-PREPARED, CABI-05-ERRORS, CABI-07-CONNINFO, CABI-08-CANCEL, CABI-10-NOTIFY, CABI-12-CONNECT-ASYNC |
| CPYTHON-NATIVE-08-MODULE | MODULE-01-EXPORTS, TEST-02-DBAPI20-VENDOR, TEST-04-DBAPI20, FEATURES-01-STUBS | CABI-04-PREPARED, CABI-05-ERRORS, CABI-07-CONNINFO, CABI-08-CANCEL, CABI-10-NOTIFY, CABI-12-CONNECT-ASYNC |
| CPYTHON-NATIVE-09-COPY | COPY-01-GENERATORS, COPY-02-FORMAT, COPY-03-SYNC, COPY-03B-EDGES, COPY-04-ASYNC | CABI-04-PREPARED, CABI-05-ERRORS, CABI-07-CONNINFO, CABI-08-CANCEL, CABI-09-COPY, CABI-10-NOTIFY, CABI-12-CONNECT-ASYNC |
| CPYTHON-NATIVE-10-SERVERCUR | SERVERCUR-01-BASE, SERVERCUR-01B-MOVE, SERVERCUR-02-SYNC, SERVERCUR-03-ASYNC | CABI-04-PREPARED, CABI-05-ERRORS, CABI-07-CONNINFO, CABI-08-CANCEL, CABI-10-NOTIFY, CABI-12-CONNECT-ASYNC |
| CPYTHON-NATIVE-11-TPC | TPC-01-XID, TPC-02-CONNECTION | CABI-04-PREPARED, CABI-05-ERRORS, CABI-06-ESCAPE, CABI-07-CONNINFO, CABI-08-CANCEL, CABI-10-NOTIFY, CABI-12-CONNECT-ASYNC |
| CPYTHON-NATIVE-12-CONTROL | PQ-19-EXT, CONTROL-01-API, CONTROL-02-ISOLATION | CABI-04-PREPARED, CABI-05-ERRORS, CABI-07-CONNINFO, CABI-08-CANCEL, CABI-10-NOTIFY, CABI-12-CONNECT-ASYNC, CABI-14-CONTROL, CABI-15-DUMP |

Each native part lists as prerequisites: every part of its family,
CPYTHON-TEST-03-NATIVE, and the LIBSPQ parts of its row. CPYTHON-NATIVE-99-FINAL lists
the twelve family parts, CPYTHON-MAKE-02-TARGETS and LIBSPQ-DOCS-01-FINAL (the last
LIBSPQ part, after all of `CABI-01` to `-15`).

### 9.3 The procedure of a native part

These parts are marked "a stronger model is advisable": they have to tell a fault of the
binding from a fault of libspq, and nothing they will meet was observed when they were
written.

1. `make libspq`; if `build/libspq/lib/libspq.so` is missing afterwards, stop (C8).
2. Run the family's test files in native mode, one runner call with the selectors the
   part lists: `python3 bindings/cpython/tools/runtests.py --mode native test_a test_b
   ...`. Tests whose class is restricted to other modes (`modes = ("oracle",
   "server")`) or gated with `@support.only_modes` are dropped or skipped by the runner;
   that is intended.
3. For every failure decide, with evidence, which of two cases it is:
   - **A fault of the binding** that native mode exposes (an assumption that held for
     libpq over TCP and is not part of libpq's contract). Repair it in the package files
     of the family (the part lists them), and prove the repair in unit, oracle and
     server mode too. No test file is changed.
   - **Anything else.** libspq or the engine answers differently from PostgreSQL 18.6
     through libpq, or a test asserts behavior of libpq over TCP that libspq documents
     differently (`docs/LIBSPQ.md` section 5) and lacks its mode gate. Stop and report
     under rule C8 with the smallest call sequence, what oracle mode returned and what
     native mode returned. The coordinator files a LIBSPQ or engine part, or corrects the
     design and the test; this part resumes when that is done. Do not change the test, do
     not work around the difference in the binding.
4. Acceptance: the family passes in native mode; unit, oracle and server mode still
   pass; `git diff --stat` lists no file under `tests/`; the commit body lists every
   failure met, its case and its evidence.

Known in advance, to be checked by the first native part: `host`, `dbname` and `user` of
the native backend's connection strings are `t<NNNNN>`, `test` and `test` (PQ 9.7), so
tests that compare them use `support.conninfo_dict(self.conninfo)`; `server_version` is
that of the engine (`docs/LIBSPQ.md` 5.2); a forked child cannot use libspq, and from
Python 3.12 `os.fork()` may warn in a process with several threads, which the Go runtime
of libspq creates **(measured on CPython 3.12.3 with the stand-in library loaded and 17
operating-system threads: `os.fork()` gave no warning under `-W error`)**.

### 9.4 The wheel smoke test (`tools/wheel_smoke.py`)

```sh
python3 -I -W error bindings/cpython/tools/wheel_smoke.py SITE [--conninfo STRING]
```

`SITE` is the directory the wheel was unpacked into. `-I` (isolated mode) keeps the
script's directory, the current directory, `PYTHONPATH` and the user's site directory
off `sys.path`: the script inserts `SITE` itself, so the package can come from nowhere
else. It prints `wheel smoke test passed: stuntgres <version>` and exits 0; a failed
check raises `AssertionError` with the reason. The checks, in order:

1. `import stuntgres` comes from `SITE/stuntgres` and maps no native library:
   `/proc/self/maps` names no path containing `libspq` or `libpq`.
2. `stuntgres.connect("host=smoke-<pid> dbname=smoke user=smoke")` works; the loaded
   library is `SITE/stuntgres/_lib/libspq.so` (`_loader.load().flavor == "libspq"`, its
   `path` is that file, and it is the one library mapped); a table is created, two rows
   inserted with parameters and read back as `[(1, "pen"), (2, None)]`; `commit()`.
3. `os.fork()` (with `DeprecationWarning` ignored around the call): in the child
   `stuntgres.connect(...)` must raise `stuntgres.InterfaceError` whose text is `libspq
   cannot be used in a process forked after the library was loaded` (PQ 2.4); the child
   leaves with `os._exit(0)` in that case, with 1, 2 or 3 otherwise. The parent waits
   for it, requires exit status 0 and then reads the table again on its connection.
4. Two threads each open a connection, run `select %s::int4 * 2` with 10 and 20 and
   close; the results are `(20,)` and `(40,)`.
5. The table is dropped, the connection closed and the cluster dropped with
   `stuntgres.drop_cluster("smoke-<pid>")`.

`--conninfo` replaces the connection string; when the environment variable
`_STUNTGRES_TEST_LIBPQ` is set (the test seam), the library checks of step 2 and the
cluster drop of step 5 are left out. That combination exists so that the script itself
could be validated before libspq exists **(measured: against an unpacked wheel, with the
seam, the script passed in oracle and in server mode; without the seam the loader found
the bundled stand-in library and the binder refused it with `lacks the required function
SPQconnectdb`, which shows the bundled path is the one used)**. `make cpython-check`
runs it without either.

## 10. Steps

Each step is one catalog part. A part edits only its own files (rule C2), writes its
tests first (C7) and must pass `--mode unit`, `--mode oracle` and `--mode server` of the
runner; parts marked native also run `--mode native` as their step says. All paths are
relative to `bindings/cpython/` unless they name the root `Makefile` or a document.

### 10.1 Parts

Every part also has `CPYTHON-DESIGN-FEATURES` as a prerequisite. Sizes are lines of
code + lines of tests, measured on the files that ran. `COPY-01` abbreviates
`CPYTHON-COPY-01-GENERATORS`, and so on; LIBSPQ parts are written in full.

| Part | Files | Public names | Prerequisites | Size |
|---|---|---|---|---|
| CPYTHON-FEATURES-01-STUBS | `stuntgres/_features.py` (replaces the stub of CPYTHON-CONN-01D-BASE), `stuntgres/_cur_copy.py` (stub), `stuntgres/_cur_copy_async.py` (stub), `stuntgres/_conn_servercur.py` (stub), `stuntgres/_conn_servercur_async.py` (stub), `stuntgres/_conn_tpc.py` (stub), `stuntgres/_conn_control.py` (stub), `tests/test_features.py`, `tests/test_connection_base.py` (edit: one test), `tests/test_module.py` (edit: one test) | _features._FAMILIES, _features._merge, the six feature classes; EXPORTS of every family module | CONN-01D-BASE, MODULE-01-EXPORTS | 135 + 95 |
| CPYTHON-COPY-01-GENERATORS | `stuntgres/_copygen.py`, `tests/test_copygen.py` | _copygen.copy_write, copy_end, copy_read | PQ-13-COPY, PQ-16-GENERATORS, PQ-12-CANCEL, TEST-01B-ORACLE, TEST-01C-SERVER | 50 + 200 |
| CPYTHON-COPY-02-FORMAT | `stuntgres/_copyfmt.py`, `tests/test_copyfmt.py` | _copyfmt.format_row, parse_row | BUILD-01-SKELETON | 60 + 75 |
| CPYTHON-COPY-03-SYNC | `stuntgres/copy.py`, `stuntgres/_cur_copy.py` (replaces the stub), `tests/test_copy.py` | stuntgres.Copy, copy.BaseCopy, copy.FLUSH_SIZE, Cursor.copy | FEATURES-01-STUBS, COPY-01-GENERATORS, COPY-02-FORMAT, CUR-02-FETCH, CUR-03-EXECUTEMANY, CONN-07-CANCEL, ADAPT-11-DEFAULTS | 320 + 150 |
| CPYTHON-COPY-03B-EDGES | `tests/test_copy_edges.py` | none (tests only) | COPY-03-SYNC | 0 + 295 |
| CPYTHON-COPY-04-ASYNC | `stuntgres/copy_async.py`, `stuntgres/_cur_copy_async.py` (replaces the stub), `tests/test_copy_async.py` | stuntgres.AsyncCopy, AsyncCursor.copy | COPY-03-SYNC, ASYNC-02-CURSOR | 100 + 160 |
| CPYTHON-SERVERCUR-01-BASE | `stuntgres/_servercur_base.py`, `tests/test_servercur_base.py` | _servercur_base.ServerCursorBase | CUR-05-OPTIONAL, CUR-02-FETCH, CONN-04-TRANSACTION, PQ-09-PREPARED, ENGINE-01-DESCRIBE-SQL-CURSOR, ADAPT-11-DEFAULTS | 220 + 220 |
| CPYTHON-SERVERCUR-01B-MOVE | `tests/test_servercur_move.py` | none (tests only) | SERVERCUR-01-BASE | 0 + 165 |
| CPYTHON-SERVERCUR-02-SYNC | `stuntgres/server_cursor.py`, `stuntgres/_conn_servercur.py` (replaces the stub), `tests/test_server_cursor.py`, `tests/test_connection.py` (edit: one test) | stuntgres.ServerCursor, Connection.server_cursor_factory | SERVERCUR-01-BASE, FEATURES-01-STUBS, CONN-01E-SYNC, ENGINE-06-DECLARE-EXTENDED-OUTSIDE-BLOCK | 105 + 200 |
| CPYTHON-SERVERCUR-03-ASYNC | `stuntgres/server_cursor_async.py`, `stuntgres/_conn_servercur_async.py` (replaces the stub), `tests/test_server_cursor_async.py`, `tests/test_async_connection.py` (edit: one test) | stuntgres.AsyncServerCursor, AsyncConnection.server_cursor_factory | SERVERCUR-02-SYNC, ASYNC-01-CONNECTION | 105 + 105 |
| CPYTHON-TPC-01-XID | `stuntgres/_xid.py`, `tests/test_xid.py` | stuntgres.Xid (class in _xid.py; exported by CPYTHON-TPC-02-CONNECTION), _xid.need_xid | PQ-01-ERRORS-BASE | 120 + 110 |
| CPYTHON-TPC-02-CONNECTION | `stuntgres/_conn_tpc.py` (replaces the stub), `tests/test_tpc.py` | Connection.xid, tpc_begin, tpc_prepare, tpc_commit, tpc_rollback, tpc_recover and their AsyncConnection twins; exports stuntgres.Xid | TPC-01-XID, FEATURES-01-STUBS, CONN-04-TRANSACTION, CONN-03-TXN-OPTIONS, ASYNC-01-CONNECTION, PQ-15-ESCAPING, ADAPT-11-DEFAULTS, ENGINE-07-PREPARE-TRANSACTION-TAG | 185 + 305 |
| CPYTHON-CONTROL-01-API | `stuntgres/_conn_control.py` (replaces the stub), `tests/test_control.py` | stuntgres.drop_cluster, stuntgres.libspq_version, Connection.reset_database, dump, restore and their AsyncConnection twins | FEATURES-01-STUBS, PQ-19-EXT, CONN-02-EXEC, ASYNC-01-CONNECTION, LIBSPQ-CABI-14-CONTROL, LIBSPQ-CABI-15-DUMP | 120 + 195 |
| CPYTHON-CONTROL-02-ISOLATION | `stuntgres/testing.py`, `tests/test_testing.py` | stuntgres.testing.temp_cluster | CONTROL-01-API, CONN-01A-CONNINFO, LIBSPQ-CABI-14-CONTROL | 35 + 85 |
| CPYTHON-MAKE-01A-ELF | `tools/elfneeds.py`, `tests/elf_util.py`, `tests/test_elfneeds.py` | elfneeds.read_needs, glibc_version, platform_tag, Needs, ElfError; tests.elf_util.make_elf | BUILD-01-SKELETON | 120 + 185 |
| CPYTHON-MAKE-01-WHEEL | `tools/build_wheel.py`, `pyproject.toml` (edit: append the build-system table), `tests/test_build_wheel.py` | build_wheel.build, verify, read_metadata, package_files, main and the PEP 517 hooks | MAKE-01A-ELF | 235 + 245 |
| CPYTHON-MAKE-02-TARGETS | `Makefile` (edit: the .PHONY line and one appended block), `tools/runinstalled.py`, `tools/wheel_smoke.py`, `tests/test_runinstalled.py` | make cpython, make cpython-check; tools/runinstalled.py; tools/wheel_smoke.py | MAKE-01-WHEEL, TEST-03-NATIVE, CONTROL-01-API, CUR-02-FETCH, LIBSPQ-BUILD-01-SKELETON, LIBSPQ-CABI-03-BLOCKING, LIBSPQ-CABI-04-PREPARED, LIBSPQ-CABI-05-ERRORS, LIBSPQ-CABI-06-ESCAPE, LIBSPQ-CABI-07-CONNINFO, LIBSPQ-CABI-08-CANCEL, LIBSPQ-CABI-09-COPY, LIBSPQ-CABI-10-NOTIFY, LIBSPQ-CABI-11-ROWMODES, LIBSPQ-CABI-12-CONNECT-ASYNC, LIBSPQ-CABI-14-CONTROL | 200 + 65 |
| CPYTHON-NATIVE-99-FINAL | `none of its own; a fault of the binding is repaired in the file that holds it and recorded` | none | NATIVE-01-PQ-CORE, NATIVE-02-PQ-EXTRAS, NATIVE-03-GENERATORS, NATIVE-04-ADAPT, NATIVE-05-CONNECTION, NATIVE-06-CURSOR, NATIVE-07-ASYNC, NATIVE-08-MODULE, NATIVE-09-COPY, NATIVE-10-SERVERCUR, NATIVE-11-TPC, NATIVE-12-CONTROL, MAKE-02-TARGETS, LIBSPQ-DOCS-01-FINAL | 20 + 0 |
| CPYTHON-DOCS-01-FINAL | `bindings/cpython/README.md` (rewrite), `docs/CPYTHON.md` (edit: the status paragraph), `AGENTS.md` (edit), `docs/DEVELOPMENT.md` (edit), `docs/TESTING.md` (edit), `docs/LIBSPQ.md` (edit: the Python paragraph of section 8) | none | NATIVE-99-FINAL, COPY-03B-EDGES, COPY-04-ASYNC, SERVERCUR-01B-MOVE, SERVERCUR-03-ASYNC, TPC-02-CONNECTION, CONTROL-02-ISOLATION, MAKE-02-TARGETS | 150 + 0 |
| CPYTHON-ENGINE-06-DECLARE-EXTENDED-OUTSIDE-BLOCK (P1) | engine and reference test files, named by the part | - | - | 25 + 70 |
| CPYTHON-ENGINE-07-PREPARE-TRANSACTION-TAG (P1) | engine and reference test files, named by the part | - | - | 15 + 60 |
| CPYTHON-ENGINE-08-DEFAULT-CURSOR-SCROLL (P2) | engine and reference test files, named by the part | - | - | 15 + 80 |

The twelve family parts `CPYTHON-NATIVE-01-PQ-CORE` to `CPYTHON-NATIVE-12-CONTROL` are
listed in 9.2; each is 20 + 0 lines by estimate (they repair what they find).

Changes against the list in the part record, with the reason:

- `CPYTHON-FEATURES-01-STUBS` is new: it owns `_features.py`, the one file four families
  extend, and creates their stubs (1.3), so that the families can be built side by side.
- `CPYTHON-COPY-03-SYNC` hands its second test file to `CPYTHON-COPY-03B-EDGES` and
  `CPYTHON-SERVERCUR-01-BASE` hands one to `CPYTHON-SERVERCUR-01B-MOVE`: together each pair
  would exceed 500 lines.
- `CPYTHON-MAKE-01-WHEEL` is split: `CPYTHON-MAKE-01A-ELF` owns the ELF reader.
- `CPYTHON-MAKE-02-TARGETS` also needs `CPYTHON-CONTROL-01-API` (the smoke test drops its
  cluster) and, through `CPYTHON-TEST-03-NATIVE`, the LIBSPQ parts of 9.1.
- Three engine parts are added under rule D8 (1.1); `CPYTHON-ENGINE-06` and `-07` are
  prerequisites of the parts whose tests need them, `CPYTHON-ENGINE-08` (P2) of none.

Parts that may run side by side never share a file: after `FEATURES-01` the families
COPY, SERVERCUR, TPC and CONTROL each replace their own stubs; `COPY-01`, `COPY-02`,
`TPC-01`, `MAKE-01A` and `SERVERCUR-01` need none of the stubs and can start earlier.

### 10.2 What "the test fails first" means here

Rule C7 asks that the tests are written first and seen to fail. For a part that creates a
module the failure is the `ImportError` named under "Before the code exists"; for a part
that replaces a stub it is the `AttributeError` named there; a tests-only part has
nothing to see fail and says so. The test files printed below are complete and ran as
printed: write them as printed.

### 10.3 What each part does

#### CPYTHON-FEATURES-01-STUBS

Files: `stuntgres/_features.py` (replaces the stub of CPYTHON-CONN-01D-BASE), `stuntgres/_cur_copy.py` (stub), `stuntgres/_cur_copy_async.py` (stub), `stuntgres/_conn_servercur.py` (stub), `stuntgres/_conn_servercur_async.py` (stub), `stuntgres/_conn_tpc.py` (stub), `stuntgres/_conn_control.py` (stub), `tests/test_features.py`, `tests/test_connection_base.py` (edit: one test), `tests/test_module.py` (edit: one test). Design sections: 1.3, 1.4.

Step.
1. Create the six family modules as stubs, exactly as printed below (each is replaced
   as a whole by the part named in its docstring).
2. Replace `stuntgres/_features.py` (the stub of CPYTHON-CONN-01D-BASE) by the file
   printed below.
3. In `tests/test_connection_base.py` replace the first two lines of the method
   `test_features_stub`

   ```python
       def test_features_stub(self) -> None:
           self.assertEqual(_features.__all__, [])
   ```

   by the single line

   ```python
       def test_feature_classes(self) -> None:
   ```

   and leave the rest of the method (the loop that checks `cls.__bases__ == (object,)`
   for the six classes) unchanged. `_features.__all__` is no longer empty once a family
   lands; its content is asserted in `tests/test_features.py`.
4. In `tests/test_module.py` add the import line `from stuntgres import _features`
   directly before `from stuntgres import errors as e`, and in `test_all` replace

   ```python
           self.assertEqual(stuntgres.__all__, EXPECTED)
   ```

   by

   ```python
           # The names of COPY, named cursors, two-phase commit and cluster control
           # follow the 42 names of this module (tests/test_features.py).
           self.assertEqual(stuntgres.__all__, EXPECTED + _features.__all__)
   ```

No behavior changes with this part: every stub is empty, so `_features.__all__` is `[]`
and the six classes have no members.

The stub `stuntgres/_cur_copy.py`:

```python
"""Stub: replaced by CPYTHON-COPY-03-SYNC."""
from __future__ import annotations

EXPORTS: tuple[str, ...] = ()


class CopyCursorMixin:
    """Members of Cursor are added by CPYTHON-COPY-03-SYNC."""
```

The stub `stuntgres/_cur_copy_async.py`:

```python
"""Stub: replaced by CPYTHON-COPY-04-ASYNC."""
from __future__ import annotations

EXPORTS: tuple[str, ...] = ()


class AsyncCopyCursorMixin:
    """Members of AsyncCursor are added by CPYTHON-COPY-04-ASYNC."""
```

The stub `stuntgres/_conn_servercur.py`:

```python
"""Stub: replaced by CPYTHON-SERVERCUR-02-SYNC."""
from __future__ import annotations

EXPORTS: tuple[str, ...] = ()


class ServerCursorMixin:
    """Members of Connection are added by CPYTHON-SERVERCUR-02-SYNC."""
```

The stub `stuntgres/_conn_servercur_async.py`:

```python
"""Stub: replaced by CPYTHON-SERVERCUR-03-ASYNC."""
from __future__ import annotations

EXPORTS: tuple[str, ...] = ()


class AsyncServerCursorMixin:
    """Members of AsyncConnection are added by CPYTHON-SERVERCUR-03-ASYNC."""
```

The stub `stuntgres/_conn_tpc.py`:

```python
"""Stub: replaced by CPYTHON-TPC-02-CONNECTION."""
from __future__ import annotations

EXPORTS: tuple[str, ...] = ()


class TpcBaseMixin:
    """Members of BaseConnection are added by CPYTHON-TPC-02-CONNECTION."""


class TpcMixin:
    """Members of Connection are added by CPYTHON-TPC-02-CONNECTION."""


class AsyncTpcMixin:
    """Members of AsyncConnection are added by CPYTHON-TPC-02-CONNECTION."""
```

The stub `stuntgres/_conn_control.py`:

```python
"""Stub: replaced by CPYTHON-CONTROL-01-API."""
from __future__ import annotations

EXPORTS: tuple[str, ...] = ()


class ControlMixin:
    """Members of Connection are added by CPYTHON-CONTROL-01-API."""


class AsyncControlMixin:
    """Members of AsyncConnection are added by CPYTHON-CONTROL-01-API."""
```

The complete file `stuntgres/_features.py`:

```python
"""COPY, named cursors, two-phase commit and cluster control: the composition.

Every family lives in modules of its own. This module copies the members of
their classes into the six feature classes, which are the first base of the
connection and cursor classes, and collects the names the families export;
the package module copies those names (stuntgres/__init__.py).
"""
from __future__ import annotations

from . import _conn_control, _conn_servercur, _conn_servercur_async, _conn_tpc
from . import _cur_copy, _cur_copy_async

_FAMILIES = (
    _cur_copy, _cur_copy_async, _conn_servercur, _conn_servercur_async,
    _conn_tpc, _conn_control,
)

__all__: list[str] = []

for _module in _FAMILIES:
    for _name in _module.EXPORTS:
        globals()[_name] = getattr(_module, _name)
        __all__.append(_name)


def _merge(target: type, *sources: type) -> None:
    """Copy the members of the family classes into a feature class."""
    for source in sources:
        for name, value in vars(source).items():
            if not name.startswith("__"):
                setattr(target, name, value)


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


_merge(BaseConnectionFeatures, _conn_tpc.TpcBaseMixin)
_merge(
    ConnectionFeatures,
    _conn_tpc.TpcMixin, _conn_control.ControlMixin, _conn_servercur.ServerCursorMixin,
)
_merge(
    AsyncConnectionFeatures,
    _conn_tpc.AsyncTpcMixin,
    _conn_control.AsyncControlMixin,
    _conn_servercur_async.AsyncServerCursorMixin,
)
_merge(CursorFeatures, _cur_copy.CopyCursorMixin)
_merge(AsyncCursorFeatures, _cur_copy_async.AsyncCopyCursorMixin)
```

Tests to write first. Before the code exists: `tests/test_features.py` fails with `AttributeError: module 'stuntgres._features' has no attribute '_FAMILIES'`.

The complete file `tests/test_features.py` (5 tests):

```python
"""Tests of the composition of the feature classes."""
from __future__ import annotations

import unittest

import stuntgres
from stuntgres import _features

FAMILIES = (
    "_cur_copy", "_cur_copy_async", "_conn_servercur", "_conn_servercur_async",
    "_conn_tpc", "_conn_control",
)
CLASSES = (
    "BaseConnectionFeatures", "ConnectionFeatures", "AsyncConnectionFeatures",
    "BaseCursorFeatures", "CursorFeatures", "AsyncCursorFeatures",
)


class FeaturesTest(unittest.TestCase):
    def test_families(self) -> None:
        self.assertEqual(
            tuple(m.__name__.rsplit(".", 1)[1] for m in _features._FAMILIES), FAMILIES
        )
        for module in _features._FAMILIES:
            self.assertIsInstance(module.EXPORTS, tuple, module.__name__)

    def test_classes_keep_their_shape(self) -> None:
        for name in CLASSES:
            cls = getattr(_features, name)
            self.assertEqual(cls.__bases__, (object,), name)
            self.assertEqual(cls.__module__, "stuntgres._features", name)

    def test_exports_reach_the_package(self) -> None:
        expected = [n for m in _features._FAMILIES for n in m.EXPORTS]
        self.assertEqual(_features.__all__, expected)
        for name in expected:
            self.assertIs(getattr(stuntgres, name), getattr(_features, name), name)
            self.assertIn(name, stuntgres.__all__)

    def test_merge_copies_public_members_only(self) -> None:
        class Source:
            """Doc of the source."""

            flag = 7

            def hello(self) -> str:
                return "hello"

            @property
            def twice(self) -> int:
                return 2 * self.flag

        class Other:
            flag = 9

        class Target:
            """Doc of the target."""

        _features._merge(Target, Source, Other)
        target = Target()
        self.assertEqual(target.hello(), "hello")  # type: ignore[attr-defined]
        self.assertEqual(target.flag, 9)  # type: ignore[attr-defined]
        self.assertEqual(target.twice, 18)  # type: ignore[attr-defined]
        self.assertEqual(Target.__doc__, "Doc of the target.")
        self.assertEqual(Target.__bases__, (object,))

    def test_family_members_are_merged(self) -> None:
        pairs = (
            ("BaseConnectionFeatures", "_conn_tpc", "TpcBaseMixin"),
            ("ConnectionFeatures", "_conn_tpc", "TpcMixin"),
            ("ConnectionFeatures", "_conn_control", "ControlMixin"),
            ("ConnectionFeatures", "_conn_servercur", "ServerCursorMixin"),
            ("AsyncConnectionFeatures", "_conn_tpc", "AsyncTpcMixin"),
            ("AsyncConnectionFeatures", "_conn_control", "AsyncControlMixin"),
            ("AsyncConnectionFeatures", "_conn_servercur_async",
             "AsyncServerCursorMixin"),
            ("CursorFeatures", "_cur_copy", "CopyCursorMixin"),
            ("AsyncCursorFeatures", "_cur_copy_async", "AsyncCopyCursorMixin"),
        )
        modules = {m.__name__.rsplit(".", 1)[1]: m for m in _features._FAMILIES}
        for target_name, module_name, source_name in pairs:
            target = getattr(_features, target_name)
            source = getattr(modules[module_name], source_name)
            for name, value in vars(source).items():
                if not name.startswith("__"):
                    self.assertIs(vars(target)[name], value, (target_name, name))
```

Acceptance: `python3 -c 'import sys; sys.path.insert(0, "bindings/cpython"); from stuntgres import _features; print(_features.__all__, [m.__name__ for m in _features._FAMILIES])'` prints `[]` and the six module names `stuntgres._cur_copy`, `stuntgres._cur_copy_async`, `stuntgres._conn_servercur`, `stuntgres._conn_servercur_async`, `stuntgres._conn_tpc`, `stuntgres._conn_control`. `git diff` of `tests/test_connection_base.py` and `tests/test_module.py` shows only the edits of steps 3 and 4.

#### CPYTHON-COPY-01-GENERATORS

Files: `stuntgres/_copygen.py`, `tests/test_copygen.py`. Design sections: 2.1.

Step. Write `stuntgres/_copygen.py` as printed: the three generators of 2.1 and the private
`_wait_writable`.

The complete file `stuntgres/_copygen.py`:

```python
"""Sans-IO generators of the COPY data transfer."""
from __future__ import annotations

from typing import TYPE_CHECKING

from . import generators
from ._gentypes import PQGen, Ready, Wait

if TYPE_CHECKING:
    from .pq import PGconn, PGresult


def copy_write(pgconn: PGconn, data: bytes) -> PQGen[None]:
    """Queue one block of COPY FROM STDIN data and flush it to the server."""
    while pgconn.put_copy_data(data) == 0:
        yield from _wait_writable(pgconn)
    yield from generators.send(pgconn)


def copy_end(pgconn: PGconn, error: bytes | None = None) -> PQGen["list[PGresult]"]:
    """End COPY FROM STDIN and return the results that follow.

    With `error` the server aborts the COPY and reports that text.
    """
    while pgconn.put_copy_end(error) == 0:
        yield from _wait_writable(pgconn)
    yield from generators.send(pgconn)
    return (yield from generators.fetch_many(pgconn))


def copy_read(pgconn: PGconn) -> PQGen["bytes | list[PGresult]"]:
    """Return the next row of COPY TO STDOUT, or the results at its end."""
    while True:
        count, data = pgconn.get_copy_data(1)
        if count != 0:
            break
        ready = yield Wait.R
        if ready:
            pgconn.consume_input()
    if count > 0:
        return bytes(data)
    return (yield from generators.fetch_many(pgconn))


def _wait_writable(pgconn: PGconn) -> PQGen[None]:
    """Wait until the socket takes more; read what arrives meanwhile."""
    ready = yield Wait.RW
    while not ready:
        ready = yield Wait.RW
    if ready & Ready.R:
        pgconn.consume_input()
```

Tests to write first. Before the code exists: `tests/test_copygen.py` fails to import: `cannot import name '_copygen' from 'stuntgres'`.

The complete file `tests/test_copygen.py` (9 tests):

```python
"""Tests of the COPY generators."""
from __future__ import annotations

from typing import Any

from stuntgres import _copygen, generators, pq
from tests import support

ES = pq.ExecStatus
TS = pq.TransactionStatus


class CopyGenTest(support.DatabaseTestCase):
    def setUp(self) -> None:
        super().setUp()
        self.conn = pq.PGconn.connect(self.conninfo.encode())
        self.addCleanup(self.conn.finish)
        self.conn.nonblocking = 1
        self.command("create table t (a int4, b text)")

    def run_gen(self, gen: Any) -> Any:
        return support.run_gen(gen, self.conn.socket, 30.0)

    def command(self, sql: str) -> list[Any]:
        self.conn.send_query(sql.encode())
        return self.statuses(self.run_gen(generators.execute(self.conn)))

    def start(self, sql: str) -> int:
        self.conn.send_query_params(sql.encode(), [], None)
        results = self.run_gen(generators.execute(self.conn))
        self.assertEqual(len(results), 1)
        return int(results[0].status)

    def statuses(self, results: list[Any]) -> list[Any]:
        return [
            (
                ES(r.status).name,
                r.error_field(pq.DiagnosticField.SQLSTATE) or r.command_status,
            )
            for r in results
        ]

    def read_all(self) -> tuple[list[bytes], list[Any]]:
        rows: list[bytes] = []
        while True:
            item = self.run_gen(_copygen.copy_read(self.conn))
            if not isinstance(item, bytes):
                return rows, self.statuses(item)
            rows.append(item)

    def count(self) -> bytes:
        self.conn.send_query(b"select count(*) from t")
        results = self.run_gen(generators.execute(self.conn))
        value: bytes = results[0].get_value(0, 0)
        return value

    def test_copy_in(self) -> None:
        conn = self.conn
        self.assertEqual(self.start("copy t from stdin"), ES.COPY_IN)
        self.assertEqual(conn.transaction_status, TS.ACTIVE)
        self.assertIsNone(self.run_gen(_copygen.copy_write(conn, b"1\tone\n2\ttw")))
        self.run_gen(_copygen.copy_write(conn, b"o\n3\t\\N\n"))
        results = self.run_gen(_copygen.copy_end(conn))
        self.assertEqual(self.statuses(results), [("COMMAND_OK", b"COPY 3")])
        self.assertEqual(results[0].command_tuples, 3)
        self.assertEqual(conn.transaction_status, TS.IDLE)
        self.assertEqual(self.count(), b"3")

    def test_copy_out(self) -> None:
        self.command("insert into t values (1, 'one'), (2, 'two'), (3, NULL)")
        self.assertEqual(self.start("copy t to stdout"), ES.COPY_OUT)
        rows, end = self.read_all()
        self.assertEqual(rows, [b"1\tone\n", b"2\ttwo\n", b"3\t\\N\n"])
        self.assertEqual(end, [("COMMAND_OK", b"COPY 3")])
        self.assertEqual(self.conn.transaction_status, TS.IDLE)

    def test_server_error_during_copy_in(self) -> None:
        conn = self.conn
        self.start("copy t from stdin")
        self.run_gen(_copygen.copy_write(conn, b"5\tfive\nx\tbad\n"))
        self.run_gen(_copygen.copy_write(conn, b"6\tsix\n"))
        results = self.run_gen(_copygen.copy_end(conn))
        self.assertEqual(self.statuses(results), [("FATAL_ERROR", b"22P02")])
        self.assertEqual(conn.transaction_status, TS.IDLE)
        self.assertEqual(self.count(), b"0")

    def test_server_error_during_copy_out(self) -> None:
        self.start("copy (select 10/(3-g) from generate_series(1,5) g) to stdout")
        rows, end = self.read_all()
        self.assertEqual(rows, [b"5\n", b"10\n"])
        self.assertEqual(end, [("FATAL_ERROR", b"22012")])

    def test_abort_by_the_client(self) -> None:
        conn = self.conn
        self.start("copy t from stdin")
        self.run_gen(_copygen.copy_write(conn, b"7\tseven\n"))
        results = self.run_gen(_copygen.copy_end(conn, b"stop it"))
        self.assertEqual(self.statuses(results), [("FATAL_ERROR", b"57014")])
        self.assertEqual(
            results[0].error_field(pq.DiagnosticField.MESSAGE_PRIMARY),
            b"COPY from stdin failed: stop it",
        )
        self.assertEqual(self.count(), b"0")

    def test_cancel_during_copy_out(self) -> None:
        conn = self.conn
        self.start("copy (select g from generate_series(1,5000000) g) to stdout")
        first = [self.run_gen(_copygen.copy_read(conn)) for _ in range(3)]
        self.assertEqual(first, [b"1\n", b"2\n", b"3\n"])
        cancel = conn.get_cancel()
        cancel.cancel()
        cancel.free()
        rows, end = self.read_all()
        self.assertLess(len(rows), 4999997)
        self.assertEqual(end, [("FATAL_ERROR", b"57014")])
        self.assertEqual(self.command("select 1"), [("TUPLES_OK", b"SELECT 1")])

    def test_large_copy_in(self) -> None:
        conn = self.conn
        self.start("copy t from stdin")
        block = b"".join(b"%d\t%s\n" % (i, b"x" * 200) for i in range(5000))
        for _ in range(20):
            self.run_gen(_copygen.copy_write(conn, block))
        results = self.run_gen(_copygen.copy_end(conn))
        self.assertEqual(self.statuses(results), [("COMMAND_OK", b"COPY 100000")])

    def test_copy_in_a_multi_statement_string(self) -> None:
        conn = self.conn
        self.command("insert into t values (1, 'one')")
        conn.send_query(b"select 1; copy t to stdout; copy t from stdin; select 2")
        results = self.run_gen(generators.execute(conn))
        self.assertEqual(
            self.statuses(results), [("TUPLES_OK", b"SELECT 1"), ("COPY_OUT", b"")]
        )
        rows, end = self.read_all()
        self.assertEqual(rows, [b"1\tone\n"])
        self.assertEqual(end, [("COMMAND_OK", b"COPY 1"), ("COPY_IN", b"")])
        results = self.run_gen(_copygen.copy_end(conn))
        self.assertEqual(
            self.statuses(results),
            [("COMMAND_OK", b"COPY 0"), ("TUPLES_OK", b"SELECT 1")],
        )

    def test_a_fake_connection_that_asks_to_wait(self) -> None:
        events: list[Any] = []

        class Fake:
            def __init__(self) -> None:
                self.puts = [0, 0, 1]
                self.ends = [0, 1]
                self.flushes = [1, 0, 0]

            def put_copy_data(self, data: bytes) -> int:
                events.append(("put", data))
                return self.puts.pop(0)

            def put_copy_end(self, error: Any = None) -> int:
                events.append(("end", error))
                return self.ends.pop(0)

            def flush(self) -> int:
                events.append("flush")
                return self.flushes.pop(0)

            def consume_input(self) -> None:
                events.append("consume")

            def is_busy(self) -> int:
                return 0

            def notifies(self) -> None:
                return None

            def get_result(self) -> None:
                return None

        fake: Any = Fake()
        gen = _copygen.copy_write(fake, b"data")
        self.assertEqual(next(gen), 3)          # put returned 0: wait RW
        self.assertEqual(gen.send(0), 3)        # nothing ready: wait again
        self.assertEqual(gen.send(2), 3)        # writable: put again, 0 again
        self.assertEqual(gen.send(3), 3)        # readable too: consume, put ok, flush 1
        with self.assertRaises(StopIteration):
            gen.send(2)                         # writable: flush returns 0
        self.assertEqual(
            events,
            [("put", b"data"), ("put", b"data"), "consume", ("put", b"data"),
             "flush", "flush"],
        )
        del events[:]
        gen = _copygen.copy_end(fake, b"why")
        self.assertEqual(next(gen), 3)
        with self.assertRaises(StopIteration) as stop:
            gen.send(2)
        self.assertEqual(stop.exception.value, [])
        self.assertEqual(events, [("end", b"why"), ("end", b"why"), "flush"])
```

Acceptance: `--mode oracle test_copygen` and `--mode server test_copygen` run 9 tests each and pass (`test_cancel_during_copy_out` cancels a COPY of 5,000,000 rows after three).

#### CPYTHON-COPY-02-FORMAT

Files: `stuntgres/_copyfmt.py`, `tests/test_copyfmt.py`. Design sections: 2.3.

Step. Write `stuntgres/_copyfmt.py` as printed: `format_row` and `parse_row` of 2.3. Pure
Python: the tests run in unit mode.

The complete file `stuntgres/_copyfmt.py`:

```python
"""Rows of the COPY text format: formatting and parsing."""
from __future__ import annotations

import re
from typing import Sequence

_ESCAPES = {
    b"\\": b"\\\\",
    b"\b": b"\\b",
    b"\f": b"\\f",
    b"\n": b"\\n",
    b"\r": b"\\r",
    b"\t": b"\\t",
    b"\v": b"\\v",
}
_UNESCAPES = {
    b"b": b"\b",
    b"f": b"\f",
    b"n": b"\n",
    b"r": b"\r",
    b"t": b"\t",
    b"v": b"\v",
}
_SPECIAL = re.compile(rb"[\\\x08\x09\x0a\x0b\x0c\x0d]")
_BACKSLASHED = re.compile(rb"\\(.)", re.DOTALL)
_NULL = b"\\N"


def _escape(match: "re.Match[bytes]") -> bytes:
    return _ESCAPES[match.group(0)]


def _unescape(match: "re.Match[bytes]") -> bytes:
    char = match.group(1)
    return _UNESCAPES.get(char, char)


def format_row(values: Sequence["bytes | None"]) -> bytes:
    """Return one line of COPY text: the dumped values of a row, None as NULL."""
    fields = [
        _NULL if value is None else _SPECIAL.sub(_escape, value) for value in values
    ]
    return b"\t".join(fields) + b"\n"


def parse_row(data: bytes) -> "list[bytes | None]":
    """Split one line of COPY text into its values; NULL becomes None."""
    if data.endswith(b"\n"):
        data = data[:-1]
    fields: "list[bytes | None]" = []
    for field in data.split(b"\t"):
        if field == _NULL:
            fields.append(None)
        elif b"\\" in field:
            fields.append(_BACKSLASHED.sub(_unescape, field))
        else:
            fields.append(field)
    return fields
```

Tests to write first. Before the code exists: `tests/test_copyfmt.py` fails to import: `No module named 'stuntgres._copyfmt'`.

The complete file `tests/test_copyfmt.py` (12 tests):

```python
"""Tests of the COPY text format."""
from __future__ import annotations

import unittest

from stuntgres._copyfmt import format_row, parse_row


class FormatRowTest(unittest.TestCase):
    def test_plain_values(self) -> None:
        self.assertEqual(format_row([b"1", b"one"]), b"1\tone\n")
        self.assertEqual(format_row([b"x"]), b"x\n")
        self.assertEqual(format_row([]), b"\n")

    def test_null_and_empty(self) -> None:
        self.assertEqual(format_row([None, b"", None]), b"\\N\t\t\\N\n")

    def test_the_text_backslash_n_is_no_null(self) -> None:
        self.assertEqual(format_row([b"\\N"]), b"\\\\N\n")

    def test_special_characters(self) -> None:
        value = b"tab\there\nnl\\back\rcr\x08bs\x0cff\x0bvt"
        self.assertEqual(
            format_row([value]),
            b"tab\\there\\nnl\\\\back\\rcr\\bbs\\fff\\vvt\n",
        )

    def test_other_bytes_pass(self) -> None:
        value = bytes(range(1, 8)) + bytes(range(14, 32)) + b"caf\xc3\xa9 \xe2\x82\xac"
        self.assertEqual(format_row([value]), value + b"\n")

    def test_bytea_hex(self) -> None:
        self.assertEqual(format_row([b"\\x00ff5c"]), b"\\\\x00ff5c\n")


class ParseRowTest(unittest.TestCase):
    def test_plain_values(self) -> None:
        self.assertEqual(parse_row(b"1\tone\n"), [b"1", b"one"])
        self.assertEqual(parse_row(b"1\tone"), [b"1", b"one"])
        self.assertEqual(parse_row(b"x\n"), [b"x"])

    def test_null_and_empty(self) -> None:
        self.assertEqual(parse_row(b"\\N\t\t\\N\n"), [None, b"", None])
        self.assertEqual(parse_row(b"\n"), [b""])

    def test_the_text_backslash_n_is_no_null(self) -> None:
        self.assertEqual(parse_row(b"\\\\N\n"), [b"\\N"])

    def test_rows_as_postgresql_writes_them(self) -> None:
        # Observed from COPY TO STDOUT of PostgreSQL 18.6 (FEATURES 2.3).
        self.assertEqual(
            parse_row(
                b"tab\\there\\nnl\\\\back\\rcr\\bbs\\fff\\vvt"
                b"\t\\\\x00ff5c\t{1,NULL,3}\t\\N\n"
            ),
            [
                b"tab\there\nnl\\back\rcr\x08bs\x0cff\x0bvt",
                b"\\x00ff5c", b"{1,NULL,3}", None,
            ],
        )
        self.assertEqual(
            parse_row(b"\t\\\\x\t{}\t\\\\N\n"), [b"", b"\\x", b"{}", b"\\N"]
        )
        self.assertEqual(
            parse_row(b"\\\\N\t\\N\t\\N\tcaf\xc3\xa9 \xe2\x82\xac\n"),
            [b"\\N", None, None, b"caf\xc3\xa9 \xe2\x82\xac"],
        )

    def test_another_backslashed_character_is_itself(self) -> None:
        self.assertEqual(parse_row(b"a\\qb\tc\\.\n"), [b"aqb", b"c."])

    def test_round_trip(self) -> None:
        row = [b"a\tb", None, b"", b"\\", b"\n\r", b"\\N", b"x\\ty"]
        self.assertEqual(parse_row(format_row(row)), row)
```

Acceptance: `--mode unit test_copyfmt` runs 12 tests and passes.

#### CPYTHON-COPY-03-SYNC

Files: `stuntgres/copy.py`, `stuntgres/_cur_copy.py` (replaces the stub), `tests/test_copy.py`. Design sections: 2.2, 2.4, 2.5.

Step.
1. Write `stuntgres/copy.py` as printed: `BaseCopy` (2.2, 2.4) and `Copy` (2.5).
2. Replace the stub `stuntgres/_cur_copy.py` by the file printed: `CopyCursorMixin`
   with `copy()`, and `EXPORTS = ("Copy",)`. From now on `stuntgres.Copy` exists and
   `stuntgres.__all__` ends with `"Copy"` (or contains it, when other families landed
   first).

The complete file `stuntgres/copy.py`:

```python
"""COPY ... FROM STDIN and COPY ... TO STDOUT: the copy object."""
from __future__ import annotations

from types import TracebackType
from typing import TYPE_CHECKING, Any, Iterator, Sequence, Union

from . import _copyfmt, _copygen
from . import errors as e
from . import generators
from ._conn_core import logger
from ._cur_results import check_results, end_copy
from ._diagnostic import error_from_result
from ._gentypes import PQGen
from ._queries import Params, Query
from ._transformer import Transformer
from .pq import ExecStatus, TransactionStatus

if TYPE_CHECKING:
    from .pq import PGresult

Buffer = Union[bytes, bytearray, memoryview]

# Rows and blocks are collected up to this many bytes before they are sent.
FLUSH_SIZE = 32 * 1024

_NEW, _IN, _OUT, _DONE = "new", "in", "out", "done"
_FAILED = (
    ExecStatus.FATAL_ERROR, ExecStatus.NONFATAL_ERROR, ExecStatus.BAD_RESPONSE,
)


class BaseCopy:
    """State and generators of one COPY; no waiting, no lock."""

    def __init__(
        self, cursor: Any, statement: Query, params: Params | None = None
    ) -> None:
        self.cursor = cursor
        self._conn = cursor._conn
        self._statement = statement
        self._params = params
        self._state = _NEW
        self._pending = bytearray()
        self._tx: Transformer | None = None
        self._nfields = 0
        self._binary = False

    def __repr__(self) -> str:
        cls = type(self)
        name = f"{cls.__module__}.{cls.__qualname__}"
        return f"<{name} [{self._state}] at 0x{id(self):x}>"

    @property
    def connection(self) -> Any:
        """The connection the COPY runs on."""
        return self._conn

    def set_types(self, types: Sequence[int]) -> None:
        """Choose the loaders of read_row() and rows() by type OID, per column.

        COPY carries no type information: without this call every value is
        returned as str.
        """
        self._check(_OUT, "set_types")
        if len(types) != self._nfields:
            raise e.ProgrammingError(
                f"set_types() got {len(types)} types for {self._nfields} columns"
            )
        assert self._tx is not None
        self._tx.set_loader_types(list(types))

    def _check(self, state: str, method: str) -> None:
        if self._state == state:
            return
        if self._state == _NEW:
            raise e.ProgrammingError(f"{method}() needs a copy block: use 'with'")
        if state == _OUT and self._state == _DONE and self._tx is not None:
            return
        wanted = "COPY ... FROM STDIN" if state == _IN else "COPY ... TO STDOUT"
        raise e.ProgrammingError(f"{method}() can be used only during {wanted}")

    def _start_gen(self) -> PQGen[None]:
        """Send the statement and take the answer that starts the COPY."""
        if self._state != _NEW:
            raise e.ProgrammingError("a copy block cannot be entered twice")
        cursor = self.cursor
        cursor._check_usable()
        cursor._reset()
        tx = Transformer(cursor)
        sql, dumped, _oids = cursor._convert(tx, self._statement, self._params)
        if dumped:
            raise e.NotSupportedError(
                "copy() cannot bind parameters: PostgreSQL accepts none in a"
                " COPY statement"
            )
        yield from self._conn._start_query()
        pgconn = self._conn.pgconn
        # Always the extended protocol: it takes one statement only.
        pgconn.send_query_params(sql, [], None)
        results = yield from generators.execute(pgconn)
        error = self._begin(results, tx)
        if error is None:
            return
        if results and results[-1].status == ExecStatus.COPY_BOTH:
            yield from end_copy(pgconn, ExecStatus.COPY_BOTH)
        for result in results:
            result.clear()
        raise error

    def _begin(self, results: list[PGresult], tx: Transformer) -> Exception | None:
        """Enter the COPY state the results announce, or say why not."""
        if not results:
            return e.InternalError("the statement returned no result")
        result = results[-1]
        status = result.status
        if status in _FAILED:
            return error_from_result(result, self._conn.pgconn._encoding)
        if status == ExecStatus.COPY_BOTH:
            return e.NotSupportedError("COPY BOTH is not supported")
        if status not in (ExecStatus.COPY_IN, ExecStatus.COPY_OUT):
            return e.ProgrammingError(
                "copy() needs a COPY ... FROM STDIN or COPY ... TO STDOUT statement"
            )
        self._nfields = result.nfields
        self._binary = bool(result.binary_tuples)
        self._tx = tx
        tx.set_loader_types([0] * self._nfields)
        self._state = _IN if status == ExecStatus.COPY_IN else _OUT
        for result in results:
            result.clear()
        return None

    def _to_bytes(self, buffer: Buffer | str) -> bytes:
        if isinstance(buffer, str):
            assert self._tx is not None
            return buffer.encode(self._tx.encoding)
        if isinstance(buffer, (bytes, bytearray, memoryview)):
            return bytes(buffer)
        raise TypeError(
            f"cannot write {type(buffer).__name__}: use bytes or str"
        )

    def _add(self, buffer: Buffer | str) -> bool:
        """Queue a block of data; True when the queue should be sent."""
        self._check(_IN, "write")
        self._pending += self._to_bytes(buffer)
        return len(self._pending) >= FLUSH_SIZE

    def _add_row(self, row: Sequence[Any]) -> bool:
        """Queue one row in the text format; True when the queue should be sent."""
        self._check(_IN, "write_row")
        if self._binary:
            raise e.NotSupportedError("write_row() supports only the text format")
        assert self._tx is not None
        values, _oids = self._tx.dump_sequence(row)
        self._pending += _copyfmt.format_row(values)
        return len(self._pending) >= FLUSH_SIZE

    def _flush_gen(self) -> PQGen[None]:
        """Send what is queued."""
        if self._pending:
            data = bytes(self._pending)
            del self._pending[:]
            yield from _copygen.copy_write(self._conn.pgconn, data)

    def _read_gen(self) -> PQGen[bytes]:
        """Return the next block of COPY TO data; b"" when there is no more."""
        self._check(_OUT, "read")
        if self._state == _DONE:
            return b""
        item = yield from _copygen.copy_read(self._conn.pgconn)
        if isinstance(item, bytes):
            return item
        yield from self._end_gen(item)
        return b""

    def _read_row_gen(self) -> PQGen["tuple[Any, ...] | None"]:
        """Return the next row of COPY TO as a tuple; None when there is no more."""
        self._check(_OUT, "read_row")
        if self._binary:
            raise e.NotSupportedError("read_row() supports only the text format")
        data = yield from self._read_gen()
        if not data:
            return None
        assert self._tx is not None
        return self._tx.load_sequence(_copyfmt.parse_row(data))

    def _end_gen(self, results: list[PGresult]) -> PQGen[None]:
        """The COPY is over: raise its error or hand its result to the cursor."""
        self._state = _DONE
        yield from check_results(self._conn.pgconn, results)
        self.cursor._set_results(results, self._tx)

    def _finish_gen(self, exc: BaseException | None) -> PQGen[None]:
        """Leave the block: end the COPY, or abort it when `exc` is given."""
        state = self._state
        if state not in (_IN, _OUT):
            return
        conn = self._conn
        pgconn = conn.pgconn
        if exc is None:
            if state == _IN:
                yield from self._flush_gen()
                results = yield from _copygen.copy_end(pgconn)
            else:
                while True:
                    item = yield from _copygen.copy_read(pgconn)
                    if not isinstance(item, bytes):
                        results = item
                        break
            yield from self._end_gen(results)
            return
        self._state = _DONE
        del self._pending[:]
        if conn._closed or conn._broken:
            return
        if state == _IN:
            text = f"aborted by the client: {type(exc).__name__}: {exc}"
            message = text.replace("\x00", " ").encode(pgconn._encoding, "replace")
            results = yield from _copygen.copy_end(pgconn, message)
        else:
            if pgconn.transaction_status == TransactionStatus.ACTIVE:
                conn._cancel_handle.cancel()
            while True:
                item = yield from _copygen.copy_read(pgconn)
                if not isinstance(item, bytes):
                    results = item
                    break
        for result in results:
            result.clear()


class Copy(BaseCopy):
    """The context manager cursor.copy() returns (blocking)."""

    __module__ = "stuntgres"

    def __enter__(self) -> Copy:
        self._conn.lock.acquire()
        try:
            self._conn.wait(self._start_gen())
        except BaseException:
            self._conn.lock.release()
            raise
        return self

    def __exit__(
        self,
        exc_type: type[BaseException] | None,
        exc_val: BaseException | None,
        exc_tb: TracebackType | None,
    ) -> None:
        try:
            if exc_val is None:
                self._conn.wait(self._finish_gen(None))
            else:
                try:
                    self._conn.wait(self._finish_gen(exc_val))
                except Exception as error:
                    logger.warning(
                        "error ignored while aborting COPY on %r: %s",
                        self._conn, error,
                    )
        finally:
            self._conn.lock.release()

    def write(self, buffer: Buffer | str) -> None:
        """Send a block of COPY FROM STDIN data; str is encoded first."""
        if self._add(buffer):
            self._conn.wait(self._flush_gen())

    def write_row(self, row: Sequence[Any]) -> None:
        """Send one row; its values are dumped as parameters are."""
        if self._add_row(row):
            self._conn.wait(self._flush_gen())

    def read(self) -> bytes:
        """Return the next block of COPY TO STDOUT data; b"" at the end."""
        data: bytes = self._conn.wait(self._read_gen())
        return data

    def __iter__(self) -> Iterator[bytes]:
        while True:
            data = self.read()
            if not data:
                break
            yield data

    def read_row(self) -> tuple[Any, ...] | None:
        """Return the next row of COPY TO STDOUT; None at the end."""
        row: tuple[Any, ...] | None = self._conn.wait(self._read_row_gen())
        return row

    def rows(self) -> Iterator[tuple[Any, ...]]:
        """Iterate over the rows of COPY TO STDOUT."""
        while True:
            row = self.read_row()
            if row is None:
                break
            yield row
```

The complete file `stuntgres/_cur_copy.py`:

```python
"""Cursor.copy()."""
from __future__ import annotations

from ._queries import Params, Query
from .copy import Copy

EXPORTS: tuple[str, ...] = ("Copy",)


class CopyCursorMixin:
    """Members of Cursor: COPY."""

    def copy(self, statement: Query, params: Params | None = None) -> Copy:
        """Return the context manager of a COPY FROM STDIN or COPY TO STDOUT.

        Use it in a 'with' statement; the connection can do nothing else
        until the block is left.
        """
        return Copy(self, statement, params)
```

Tests to write first. Before the code exists: `tests/test_copy.py` fails to import: `No module named 'stuntgres.copy'`.

The complete file `tests/test_copy.py` (8 tests):

```python
"""Tests of Cursor.copy() and the Copy object."""
from __future__ import annotations

import datetime
import decimal
from typing import Any
from unittest import mock

import stuntgres
from stuntgres import _copygen
from stuntgres import errors as e
from stuntgres.copy import FLUSH_SIZE, BaseCopy, Copy
from tests.dbapi_util import ConnTestCase, status

ROWS = [(1, "one"), (2, "two"), (3, None)]


class CopyTest(ConnTestCase):
    def setUp(self) -> None:
        super().setUp()
        self.conn = self.connect()
        self.cur = self.conn.cursor()
        self.cur.execute("create table t (a int4, b text)")
        self.conn.commit()

    def all_rows(self) -> list[Any]:
        return self.conn.execute("select a, b from t order by a").fetchall()

    def fill(self) -> None:
        self.cur.executemany("insert into t values (%s, %s)", ROWS)
        self.conn.commit()

    def test_names(self) -> None:
        self.assertIs(stuntgres.Copy, Copy)
        self.assertEqual(Copy.__module__, "stuntgres")
        self.assertTrue(issubclass(Copy, BaseCopy))
        self.assertEqual(FLUSH_SIZE, 32768)

    def test_write_blocks(self) -> None:
        cur = self.cur
        with cur.copy("copy t from stdin") as copy:
            self.assertIs(type(copy), Copy)
            self.assertIs(copy.cursor, cur)
            self.assertIs(copy.connection, self.conn)
            self.assertEqual(status(self.conn), "ACTIVE")
            self.assertTrue(self.conn.lock.locked())
            self.assertIsNone(cur.pgresult)
            self.assertIsNone(copy.write(b"1\tone\n2\ttw"))
            copy.write("o\n")
            copy.write(bytearray(b"3\t"))
            copy.write(memoryview(b"\\N\n"))
        self.assertFalse(self.conn.lock.locked())
        self.assertEqual(status(self.conn), "INTRANS")
        self.assertEqual(cur.rowcount, 3)
        self.assertEqual(cur.statusmessage, "COPY 3")
        self.assertIsNone(cur.description)
        self.assertEqual(self.all_rows(), ROWS)

    def test_write_row(self) -> None:
        cur = self.cur
        cur.execute(
            "create table w (i int8, n numeric, s text, b bytea, d date,"
            " f bool, a int4[], j jsonb)"
        )
        rows = [
            (1, decimal.Decimal("1.50"), "tab\there\nnl\\back", b"\x00\xff\\",
             datetime.date(2024, 2, 29), True, [1, None, 3], None),
            (None, None, "", b"", None, False, [], None),
            (-5, decimal.Decimal("0"), "\\N", None, None, None, None, None),
        ]
        with cur.copy("copy w from stdin") as copy:
            for row in rows:
                self.assertIsNone(copy.write_row(row))
            copy.write_row([4, None, "caf\u00e9 \u20ac", None, None, None, None, None])
        self.assertEqual(cur.rowcount, 4)
        cur.execute("select i, n, s, b, d, f, a, j from w order by i nulls first")
        self.assertEqual(
            cur.fetchall(),
            [
                rows[1], rows[2], rows[0],
                (4, None, "caf\u00e9 \u20ac", None, None, None, None, None),
            ],
        )

    def test_many_rows_are_sent_in_blocks(self) -> None:
        cur = self.cur
        sent: list[int] = []
        real = _copygen.copy_write

        def counting(pgconn: Any, data: bytes) -> Any:
            sent.append(len(data))
            return real(pgconn, data)

        with mock.patch.object(_copygen, "copy_write", counting):
            with cur.copy("copy t from stdin") as copy:
                for number in range(10000):
                    copy.write_row((number, "x" * 20))
                self.assertEqual(len(sent), 7)
                self.assertTrue(all(FLUSH_SIZE <= n < FLUSH_SIZE + 30 for n in sent))
        self.assertEqual(len(sent), 8)
        self.assertEqual(sum(sent), 258890)
        self.assertEqual(cur.rowcount, 10000)
        self.assertEqual(
            self.conn.execute("select count(*), sum(a) from t").fetchone(),
            (10000, 49995000),
        )

    def test_read_blocks(self) -> None:
        self.fill()
        cur = self.cur
        with cur.copy("copy t to stdout") as copy:
            self.assertEqual(status(self.conn), "ACTIVE")
            self.assertEqual(copy.read(), b"1\tone\n")
            self.assertEqual(list(copy), [b"2\ttwo\n", b"3\t\\N\n"])
            self.assertEqual(copy.read(), b"")
            self.assertEqual(copy.read(), b"")
            self.assertEqual(cur.rowcount, 3)
        self.assertEqual(cur.rowcount, 3)
        self.assertEqual(cur.statusmessage, "COPY 3")
        self.assertEqual(status(self.conn), "INTRANS")

    def test_read_rows(self) -> None:
        self.fill()
        cur = self.cur
        with cur.copy("copy t to stdout") as copy:
            self.assertEqual(copy.read_row(), ("1", "one"))
            self.assertEqual(list(copy.rows()), [("2", "two"), ("3", None)])
            self.assertIsNone(copy.read_row())
        with cur.copy("copy t to stdout") as copy:
            self.assertIsNone(copy.set_types([23, 25]))
            self.assertEqual(list(copy.rows()), ROWS)

    def test_leaving_early_reads_the_rest(self) -> None:
        cur = self.cur
        with cur.copy("copy (select generate_series(1, 20000)) to stdout") as copy:
            self.assertEqual(copy.read(), b"1\n")
        self.assertEqual(cur.rowcount, 20000)
        self.assertEqual(status(self.conn), "INTRANS")
        self.assertEqual(self.conn.execute("select 1").fetchone(), (1,))

    def test_repr(self) -> None:
        copy = self.cur.copy("copy t to stdout")
        self.assertRegex(repr(copy), r"^<stuntgres\.Copy \[new\] at 0x[0-9a-f]+>$")
        with copy:
            self.assertRegex(repr(copy), r"^<stuntgres\.Copy \[out\] at 0x[0-9a-f]+>$")
        self.assertRegex(repr(copy), r"^<stuntgres\.Copy \[done\] at 0x[0-9a-f]+>$")
```

Acceptance: `--mode oracle test_copy` and `--mode server test_copy` run 8 tests each and pass.

#### CPYTHON-COPY-03B-EDGES

Files: `tests/test_copy_edges.py`. Design sections: 2.2, 2.4, 2.5.

Step. Tests only: write `tests/test_copy_edges.py` as printed. It covers leaving a block with
an exception, the abort message, cancellation, errors of the server, statements that
start no COPY, parameters, CSV and binary blocks, a second thread waiting for the
block, `set_types()` and every wrong use of the copy object. If a test fails, the fault is in `stuntgres/copy.py` or below: stop and report
(C8); this part owns no package file.

Tests to write first. Before the code exists: Nothing to see fail: the code exists (CPYTHON-COPY-03-SYNC). Run the file before committing; every test must pass in oracle and server mode.

The complete file `tests/test_copy_edges.py` (14 tests):

```python
"""Tests of the Copy object: errors, aborts, formats and other edges."""
from __future__ import annotations

import datetime
import decimal
import threading
from typing import Any
from unittest import mock

from stuntgres import _copygen
from stuntgres import errors as e
from tests.dbapi_util import ConnTestCase, status

ROWS = [(1, "one"), (2, "two"), (3, None)]
MANY = "copy (select generate_series(1, 5000000)) to stdout"


class CopyEdgesTest(ConnTestCase):
    def setUp(self) -> None:
        super().setUp()
        self.conn = self.connect()
        self.cur = self.conn.cursor()
        self.cur.execute("create table t (a int4, b text)")
        self.conn.commit()

    def all_rows(self) -> list[Any]:
        return self.conn.execute("select a, b from t order by a").fetchall()

    def fill(self) -> None:
        self.cur.executemany("insert into t values (%s, %s)", ROWS)
        self.conn.commit()

    def test_exception_aborts_copy_in(self) -> None:
        cur = self.cur
        with self.assertRaises(ValueError) as caught:
            with cur.copy("copy t from stdin") as copy:
                copy.write_row((1, "one"))
                raise ValueError("boom")
        self.assertEqual(str(caught.exception), "boom")
        self.assertFalse(self.conn.lock.locked())
        self.assertEqual(status(self.conn), "INERROR")
        self.assertEqual(cur.rowcount, -1)
        self.assertIsNone(cur.pgresult)
        self.conn.rollback()
        self.assertEqual(self.all_rows(), [])

    def test_the_abort_message(self) -> None:
        self.conn.rollback()
        self.conn.autocommit = True
        seen: list[Any] = []
        real = _copygen.copy_end

        def spying(pgconn: Any, error: Any = None) -> Any:
            seen.append(error)
            return real(pgconn, error)

        with mock.patch.object(_copygen, "copy_end", spying):
            with self.assertRaises(KeyError):
                with self.cur.copy("copy t from stdin"):
                    raise KeyError("k\x00ey")
        self.assertEqual(seen, [b"aborted by the client: KeyError: 'k\\x00ey'"])
        self.assertEqual(status(self.conn), "IDLE")

    def test_exception_cancels_copy_out(self) -> None:
        cur = self.cur
        with self.assertRaises(ValueError):
            with cur.copy(MANY) as copy:
                self.assertEqual(copy.read(), b"1\n")
                raise ValueError("boom")
        self.assertEqual(status(self.conn), "INERROR")
        self.assertIsNone(cur.pgresult)
        self.conn.rollback()
        self.assertEqual(self.conn.execute("select 1").fetchone(), (1,))

    def test_exception_after_the_end_of_copy_out(self) -> None:
        self.fill()
        cur = self.cur
        with self.assertRaises(ValueError):
            with cur.copy("copy t to stdout") as copy:
                self.assertEqual(len(list(copy)), 3)
                raise ValueError("boom")
        self.assertEqual(status(self.conn), "INTRANS")
        self.assertEqual(cur.rowcount, 3)

    def test_server_error_at_the_end_of_copy_in(self) -> None:
        cur = self.cur
        with self.assertRaises(e.DataError) as caught:
            with cur.copy("copy t from stdin") as copy:
                copy.write(b"1\tone\nx\tbad\n")
        self.assertEqual(caught.exception.sqlstate, "22P02")
        self.assertEqual(
            str(caught.exception).splitlines()[0],
            'invalid input syntax for type integer: "x"',
        )
        self.assertEqual(caught.exception.diag.context, 'COPY t, line 2, column a: "x"')
        self.assertFalse(self.conn.lock.locked())
        self.assertEqual(status(self.conn), "INERROR")
        self.assertIsNone(cur.pgresult)

    def test_server_error_during_copy_out(self) -> None:
        cur = self.cur
        got: list[bytes] = []
        with self.assertRaises(e.DataError) as caught:
            with cur.copy(
                "copy (select 10/(3-g) from generate_series(1,5) g) to stdout"
            ) as copy:
                for block in copy:
                    got.append(block)
        self.assertEqual(got, [b"5\n", b"10\n"])
        self.assertEqual(caught.exception.sqlstate, "22012")
        self.assertEqual(status(self.conn), "INERROR")

    def test_cancel_during_copy_out(self) -> None:
        cur = self.cur
        with self.assertRaises(e.OperationalError) as caught:
            with cur.copy(MANY) as copy:
                self.assertEqual(copy.read(), b"1\n")
                self.conn.cancel()
                for _block in copy:
                    pass
        self.assertEqual(caught.exception.sqlstate, "57014")
        self.assertEqual(type(caught.exception).__name__, "QueryCanceled")

    def test_statements_that_start_no_copy(self) -> None:
        cur = self.cur
        with self.assertRaises(e.ProgrammingError) as caught:
            with cur.copy("select 1"):
                self.fail("the block must not run")
        self.assertEqual(
            str(caught.exception),
            "copy() needs a COPY ... FROM STDIN or COPY ... TO STDOUT statement",
        )
        self.assertFalse(self.conn.lock.locked())
        self.assertEqual(status(self.conn), "INTRANS")
        with self.assertRaises(e.ProgrammingError) as caught:
            with cur.copy("copy nosuch from stdin"):
                self.fail("the block must not run")
        self.assertEqual(caught.exception.sqlstate, "42P01")
        self.assertFalse(self.conn.lock.locked())
        self.conn.rollback()
        with self.assertRaises(e.ProgrammingError) as caught:
            with cur.copy("copy t to stdout; select 1"):
                self.fail("the block must not run")
        self.assertEqual(caught.exception.sqlstate, "42601")
        self.assertEqual(
            str(caught.exception).splitlines()[0],
            "cannot insert multiple commands into a prepared statement",
        )

    def test_parameters(self) -> None:
        self.conn.rollback()
        self.conn.autocommit = True
        cur = self.cur
        for params in ((42,), {"n": 42}):
            ph = "%s" if isinstance(params, tuple) else "%(n)s"
            with self.assertRaises(e.NotSupportedError) as caught:
                with cur.copy(f"copy (select {ph}::int4) to stdout", params):
                    self.fail("the block must not run")
            self.assertEqual(
                str(caught.exception),
                "copy() cannot bind parameters: PostgreSQL accepts none in a COPY"
                " statement",
            )
            self.assertFalse(self.conn.lock.locked())
        self.assertEqual(status(self.conn), "IDLE")
        with cur.copy("copy (select 'a%%b') to stdout", ()) as copy:
            self.assertEqual(copy.read(), b"a%b\n")
        with cur.copy("copy (select 'a%%b') to stdout", {}) as copy:
            self.assertEqual(copy.read(), b"a%b\n")
        with cur.copy("copy (select 'a%%b') to stdout") as copy:
            self.assertEqual(copy.read(), b"a%%b\n")

    def test_other_formats(self) -> None:
        cur = self.cur
        with cur.copy("copy t from stdin (format csv)") as copy:
            copy.write('8,"eight, with comma"\n')
        with cur.copy("copy t to stdout (format csv, header)") as copy:
            self.assertEqual(list(copy), [b"a,b\n", b'8,"eight, with comma"\n'])
        with cur.copy("copy t to stdout (format binary)") as copy:
            data = b"".join(copy)
            with self.assertRaises(e.NotSupportedError) as caught:
                copy.read_row()
            self.assertEqual(
                str(caught.exception), "read_row() supports only the text format"
            )
        self.assertEqual(data[:11], b"PGCOPY\n\xff\r\n\x00")
        self.assertEqual(len(data), 52)
        with cur.copy("copy t from stdin (format binary)") as copy:
            with self.assertRaises(e.NotSupportedError) as caught:
                copy.write_row((1, "x"))
            self.assertEqual(
                str(caught.exception), "write_row() supports only the text format"
            )
            copy.write(data)
        self.assertEqual(cur.rowcount, 1)
        self.assertEqual(self.all_rows(), [(8, "eight, with comma")] * 2)

    def test_execute_still_refuses_copy(self) -> None:
        with self.assertRaises(e.ProgrammingError) as caught:
            self.cur.execute("copy t to stdout")
        self.assertEqual(
            str(caught.exception), "COPY cannot be run with execute() or executemany()"
        )

    def test_another_thread_waits_for_the_block(self) -> None:
        self.fill()
        cur = self.cur
        other = self.conn.cursor()
        done: list[Any] = []

        def work() -> None:
            done.append(other.execute("select 7").fetchone())

        thread = threading.Thread(target=work)
        with cur.copy("copy t to stdout") as copy:
            thread.start()
            self.addCleanup(thread.join)
            self.assertEqual(copy.read(), b"1\tone\n")
            thread.join(0.2)
            self.assertEqual(done, [])
        thread.join(10)
        self.assertEqual(done, [(7,)])

    def test_set_types(self) -> None:
        cur = self.cur
        query = (
            "copy (select 1.50::numeric, date '2024-02-29', '{1,NULL,3}'::int4[],"
            " '\\x00ff5c'::bytea, E'tab\\there\\\\N', NULL::int4, true) to stdout"
        )
        with cur.copy(query) as copy:
            self.assertEqual(
                copy.read_row(),
                ("1.50", "2024-02-29", "{1,NULL,3}", "\\x00ff5c", "tab\there\\N",
                 None, "t"),
            )
        with cur.copy(query) as copy:
            copy.set_types([1700, 1082, 1007, 17, 25, 23, 16])
            self.assertEqual(
                copy.read_row(),
                (decimal.Decimal("1.50"), datetime.date(2024, 2, 29), [1, None, 3],
                 b"\x00\xff\\", "tab\there\\N", None, True),
            )
        with cur.copy(query) as copy:
            with self.assertRaises(e.ProgrammingError) as caught:
                copy.set_types([23])
            self.assertEqual(
                str(caught.exception), "set_types() got 1 types for 7 columns"
            )

    def test_wrong_use(self) -> None:
        self.fill()
        cur = self.cur
        copy = cur.copy("copy t to stdout")
        with self.assertRaises(e.ProgrammingError) as caught:
            copy.read()
        self.assertEqual(str(caught.exception), "read() needs a copy block: use 'with'")
        with copy:
            with self.assertRaises(e.ProgrammingError) as caught:
                copy.write(b"x")
            self.assertEqual(
                str(caught.exception),
                "write() can be used only during COPY ... FROM STDIN",
            )
            with self.assertRaises(e.ProgrammingError) as caught:
                copy.write_row((1,))
            self.assertEqual(
                str(caught.exception),
                "write_row() can be used only during COPY ... FROM STDIN",
            )
        with self.assertRaises(e.ProgrammingError) as caught:
            with copy:
                self.fail("the block must not run")
        self.assertEqual(str(caught.exception), "a copy block cannot be entered twice")
        self.assertFalse(self.conn.lock.locked())
        with cur.copy("copy t from stdin") as copy:
            for method in (copy.read, copy.read_row):
                with self.assertRaises(e.ProgrammingError) as caught:
                    method()
                self.assertEqual(
                    str(caught.exception),
                    f"{method.__name__}() can be used only during COPY ... TO STDOUT",
                )
            with self.assertRaises(TypeError) as caught_type:
                copy.write(5)  # type: ignore[arg-type]
            self.assertEqual(
                str(caught_type.exception), "cannot write int: use bytes or str"
            )
        cur.close()
        with self.assertRaises(e.InterfaceError) as caught_closed:
            with cur.copy("copy t to stdout"):
                self.fail("the block must not run")
        self.assertEqual(str(caught_closed.exception), "the cursor is closed")
        self.assertFalse(self.conn.lock.locked())
```

Acceptance: `--mode oracle test_copy_edges` and `--mode server test_copy_edges` run 14 tests each and pass.

#### CPYTHON-COPY-04-ASYNC

Files: `stuntgres/copy_async.py`, `stuntgres/_cur_copy_async.py` (replaces the stub), `tests/test_copy_async.py`. Design sections: 2.2, 2.4, 2.6.

Step.
1. Write `stuntgres/copy_async.py` as printed: `AsyncCopy` (2.6). It must not import
   `asyncio`.
2. Replace the stub `stuntgres/_cur_copy_async.py` by the file printed
   (`EXPORTS = ("AsyncCopy",)`).

The complete file `stuntgres/copy_async.py`:

```python
"""COPY for asyncio: the twin of stuntgres.copy.Copy."""
from __future__ import annotations

from types import TracebackType
from typing import Any, AsyncIterator, Sequence

from ._conn_core import logger
from .copy import BaseCopy, Buffer


class AsyncCopy(BaseCopy):
    """The async context manager AsyncCursor.copy() returns."""

    __module__ = "stuntgres"

    async def __aenter__(self) -> AsyncCopy:
        await self._conn.lock.acquire()
        try:
            await self._conn.wait(self._start_gen())
        except BaseException:
            self._conn.lock.release()
            raise
        return self

    async def __aexit__(
        self,
        exc_type: type[BaseException] | None,
        exc_val: BaseException | None,
        exc_tb: TracebackType | None,
    ) -> None:
        try:
            if exc_val is None:
                await self._conn.wait(self._finish_gen(None))
            else:
                try:
                    await self._conn.wait(self._finish_gen(exc_val))
                except Exception as error:
                    logger.warning(
                        "error ignored while aborting COPY on %r: %s",
                        self._conn, error,
                    )
        finally:
            self._conn.lock.release()

    async def write(self, buffer: Buffer | str) -> None:
        """Send a block of COPY FROM STDIN data; str is encoded first."""
        if self._add(buffer):
            await self._conn.wait(self._flush_gen())

    async def write_row(self, row: Sequence[Any]) -> None:
        """Send one row; its values are dumped as parameters are."""
        if self._add_row(row):
            await self._conn.wait(self._flush_gen())

    async def read(self) -> bytes:
        """Return the next block of COPY TO STDOUT data; b"" at the end."""
        data: bytes = await self._conn.wait(self._read_gen())
        return data

    async def __aiter__(self) -> AsyncIterator[bytes]:
        while True:
            data = await self.read()
            if not data:
                break
            yield data

    async def read_row(self) -> tuple[Any, ...] | None:
        """Return the next row of COPY TO STDOUT; None at the end."""
        row: tuple[Any, ...] | None = await self._conn.wait(self._read_row_gen())
        return row

    async def rows(self) -> AsyncIterator[tuple[Any, ...]]:
        """Iterate over the rows of COPY TO STDOUT."""
        while True:
            row = await self.read_row()
            if row is None:
                break
            yield row
```

The complete file `stuntgres/_cur_copy_async.py`:

```python
"""AsyncCursor.copy()."""
from __future__ import annotations

from ._queries import Params, Query
from .copy_async import AsyncCopy

EXPORTS: tuple[str, ...] = ("AsyncCopy",)


class AsyncCopyCursorMixin:
    """Members of AsyncCursor: COPY."""

    def copy(self, statement: Query, params: Params | None = None) -> AsyncCopy:
        """Return the async context manager of a COPY FROM STDIN or TO STDOUT.

        Use it in an 'async with' statement; the connection can do nothing
        else until the block is left.
        """
        return AsyncCopy(self, statement, params)
```

Tests to write first. Before the code exists: `tests/test_copy_async.py` fails to import: `No module named 'stuntgres.copy_async'`.

The complete file `tests/test_copy_async.py` (10 tests):

```python
"""Tests of AsyncCursor.copy() and the AsyncCopy object."""
from __future__ import annotations

import asyncio
from typing import Any

import stuntgres
from stuntgres import errors as e
from stuntgres.copy import BaseCopy
from stuntgres.copy_async import AsyncCopy
from tests.dbapi_util import status
from tests.dbapi_util_async import AsyncConnTestCase

ROWS = [(1, "one"), (2, "two"), (3, None)]


class AsyncCopyTest(AsyncConnTestCase):
    async def asyncSetUp(self) -> None:
        self.conn = await self.connect()
        self.cur = self.conn.cursor()
        await self.cur.execute("create table t (a int4, b text)")
        await self.conn.commit()

    async def all_rows(self) -> list[Any]:
        cur = await self.conn.execute("select a, b from t order by a")
        return await cur.fetchall()

    async def fill(self) -> None:
        await self.cur.executemany("insert into t values (%s, %s)", ROWS)
        await self.conn.commit()

    async def test_names(self) -> None:
        self.assertIs(stuntgres.AsyncCopy, AsyncCopy)
        self.assertEqual(AsyncCopy.__module__, "stuntgres")
        self.assertTrue(issubclass(AsyncCopy, BaseCopy))

    async def test_write(self) -> None:
        cur = self.cur
        async with cur.copy("copy t from stdin") as copy:
            self.assertIs(type(copy), AsyncCopy)
            self.assertEqual(status(self.conn), "ACTIVE")
            self.assertTrue(self.conn.lock.locked())
            self.assertIsNone(await copy.write(b"1\tone\n"))
            self.assertIsNone(await copy.write_row((2, "two")))
            await copy.write("3\t\\N\n")
        self.assertFalse(self.conn.lock.locked())
        self.assertEqual(status(self.conn), "INTRANS")
        self.assertEqual(cur.rowcount, 3)
        self.assertEqual(cur.statusmessage, "COPY 3")
        self.assertEqual(await self.all_rows(), ROWS)

    async def test_many_rows(self) -> None:
        cur = self.cur
        async with cur.copy("copy t from stdin") as copy:
            for number in range(10000):
                await copy.write_row((number, "x" * 20))
        self.assertEqual(cur.rowcount, 10000)

    async def test_read(self) -> None:
        await self.fill()
        cur = self.cur
        async with cur.copy("copy t to stdout") as copy:
            self.assertEqual(await copy.read(), b"1\tone\n")
            self.assertEqual([b async for b in copy], [b"2\ttwo\n", b"3\t\\N\n"])
            self.assertEqual(await copy.read(), b"")
        self.assertEqual(cur.rowcount, 3)
        async with cur.copy("copy t to stdout") as copy:
            self.assertEqual(await copy.read_row(), ("1", "one"))
            copy.set_types([23, 25])
            self.assertEqual([r async for r in copy.rows()], [(2, "two"), (3, None)])
            self.assertIsNone(await copy.read_row())
        self.assertEqual(status(self.conn), "INTRANS")

    async def test_leaving_early_reads_the_rest(self) -> None:
        cur = self.cur
        async with cur.copy("copy (select generate_series(1, 20000)) to stdout") as c:
            self.assertEqual(await c.read(), b"1\n")
        self.assertEqual(cur.rowcount, 20000)

    async def test_exception_aborts_copy_in(self) -> None:
        cur = self.cur
        with self.assertRaises(ValueError):
            async with cur.copy("copy t from stdin") as copy:
                await copy.write_row((1, "one"))
                raise ValueError("boom")
        self.assertFalse(self.conn.lock.locked())
        self.assertEqual(status(self.conn), "INERROR")
        await self.conn.rollback()
        self.assertEqual(await self.all_rows(), [])

    async def test_exception_cancels_copy_out(self) -> None:
        cur = self.cur
        with self.assertRaises(ValueError):
            async with cur.copy(
                "copy (select generate_series(1, 5000000)) to stdout"
            ) as copy:
                self.assertEqual(await copy.read(), b"1\n")
                raise ValueError("boom")
        self.assertEqual(status(self.conn), "INERROR")
        await self.conn.rollback()

    async def test_server_error(self) -> None:
        cur = self.cur
        with self.assertRaises(e.DataError) as caught:
            async with cur.copy("copy t from stdin") as copy:
                await copy.write(b"x\tbad\n")
        self.assertEqual(caught.exception.sqlstate, "22P02")
        self.assertFalse(self.conn.lock.locked())
        await self.conn.rollback()
        with self.assertRaises(e.ProgrammingError) as caught_start:
            async with cur.copy("select 1"):
                self.fail("the block must not run")
        self.assertEqual(
            str(caught_start.exception),
            "copy() needs a COPY ... FROM STDIN or COPY ... TO STDOUT statement",
        )
        self.assertFalse(self.conn.lock.locked())

    async def test_task_cancellation_inside_the_block(self) -> None:
        cur = self.cur
        started = asyncio.Event()

        async def work() -> None:
            async with cur.copy(
                "copy (select generate_series(1, 5000000)) to stdout"
            ) as copy:
                await copy.read()
                started.set()
                await asyncio.Event().wait()

        task = asyncio.ensure_future(work())
        await asyncio.wait_for(started.wait(), 10)
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await task
        self.assertFalse(self.conn.lock.locked())
        self.assertEqual(status(self.conn), "INERROR")
        await self.conn.rollback()
        cur2 = await self.conn.execute("select 1")
        self.assertEqual(await cur2.fetchone(), (1,))

    async def test_another_task_waits_for_the_block(self) -> None:
        await self.fill()
        cur = self.cur
        other = self.conn.cursor()

        async def work() -> Any:
            await other.execute("select 7")
            return await other.fetchone()

        async with cur.copy("copy t to stdout") as copy:
            task = asyncio.ensure_future(work())
            await asyncio.sleep(0)
            self.assertEqual(await copy.read(), b"1\tone\n")
            self.assertFalse(task.done())
        self.assertEqual(await asyncio.wait_for(task, 10), (7,))
```

Acceptance: `--mode oracle test_copy_async` and `--mode server test_copy_async` run 10 tests each and pass; `python3 -c 'import sys; sys.path.insert(0, "bindings/cpython"); import stuntgres; print("asyncio" in sys.modules)'` prints `False`.

#### CPYTHON-SERVERCUR-01-BASE

Files: `stuntgres/_servercur_base.py`, `tests/test_servercur_base.py`. Design sections: 3.1, 3.2, 3.3.

Step. Write `stuntgres/_servercur_base.py` as printed: `ServerCursorBase` with the state of
3.1, the statements of 3.2 and the generators of 3.3. The tests drive the generators
directly (`with conn.lock: conn.wait(gen)`) and record every statement that is sent by
wrapping `send_query`, `send_query_params` and `send_describe_portal` of the `PGconn`
class.

Server mode needs CPYTHON-ENGINE-01-DESCRIBE-SQL-CURSOR (a prerequisite): without it the
Describe of the portal fails with `34000`.

The complete file `stuntgres/_servercur_base.py`:

```python
"""Server-side (named) cursors: state, statements and generators."""
from __future__ import annotations

from typing import TYPE_CHECKING, Any

from . import errors as e
from . import generators
from ._column import Column
from ._cur_optional import OptionalMixin
from ._cur_results import check_results
from ._gentypes import PQGen
from ._queries import Params, Query
from ._transformer import Transformer
from .pq import TransactionStatus
from .rows import RowFactory
from .transaction import quote_identifier

if TYPE_CHECKING:
    from .pq import PGresult

_NO_RESULT = "no result available: execute a statement first"
_OUTSIDE = "the position is outside the result"
_EXISTS = b"SELECT 1 FROM pg_catalog.pg_cursors WHERE name = $1"


class ServerCursorBase(OptionalMixin):
    """A cursor whose rows stay on the server until they are fetched."""

    def __init__(
        self,
        connection: Any,
        name: str,
        *,
        row_factory: RowFactory | None = None,
        scrollable: bool | None = None,
        withhold: bool = False,
    ) -> None:
        if not name:
            raise e.ProgrammingError("a server-side cursor needs a name")
        self._described: PGresult | None = None
        super().__init__(connection, row_factory=row_factory)
        self._name = name
        self._scrollable = scrollable
        self._withhold = bool(withhold)
        self._declared = False
        self._columns: list[Column] | None = None
        self._spot: int | None = None
        self.itersize = 100

    def _reset(self) -> None:
        super()._reset()
        if self._described is not None:
            self._described.clear()
        self._described = None
        self._columns = None
        self._spot = None

    @property
    def name(self) -> str:
        """The name of the cursor on the server."""
        return self._name

    @property
    def scrollable(self) -> bool | None:
        """True: SCROLL; False: NO SCROLL; None: left to the server."""
        return self._scrollable

    @property
    def withhold(self) -> bool:
        """True when the cursor outlives the transaction (WITH HOLD)."""
        return self._withhold

    @property
    def description(self) -> list[Column] | None:
        """The columns of the cursor's query; None before execute()."""
        described = self._described
        if described is None:
            return None
        if self._columns is None:
            encoding = self._tx.encoding if self._tx is not None else "utf-8"
            self._columns = [
                Column(described, index, encoding)
                for index in range(described.nfields)
            ]
        return self._columns

    @property
    def rowcount(self) -> int:
        """Always -1: the server does not tell how many rows the query has."""
        return -1

    @property
    def rownumber(self) -> int | None:
        """Index of the next row to fetch; None when it is not known."""
        return self._spot

    def _ident(self) -> bytes:
        codec: str = self._conn._codec
        return quote_identifier(self._name).encode(codec)

    def _check_declared(self) -> None:
        self._check_usable()
        if not self._declared:
            raise e.ProgrammingError(_NO_RESULT)

    def _declare_gen(self, query: Query, params: Params | None = None) -> PQGen[None]:
        """DECLARE the cursor, then describe its portal."""
        self._check_usable()
        conn = self._conn
        pgconn = conn.pgconn
        if self._declared:
            yield from self._close_portal_gen()
        self._reset()
        tx = Transformer(self)  # type: ignore[arg-type]
        sql, dumped, oids = self._convert(tx, query, params)
        yield from conn._start_query()
        parts = [b"DECLARE ", self._ident()]
        if self._scrollable is not None:
            parts.append(b" SCROLL" if self._scrollable else b" NO SCROLL")
        parts.append(b" CURSOR")
        if self._withhold:
            parts.append(b" WITH HOLD")
        parts.append(b" FOR ")
        parts.append(sql)
        # Always the extended protocol: it takes one statement only.
        pgconn.send_query_params(b"".join(parts), dumped or [], oids)
        results = yield from generators.execute(pgconn)
        yield from check_results(pgconn, results)
        for result in results:
            result.clear()
        self._declared = True
        pgconn.send_describe_portal(self._name.encode(conn._codec))
        results = yield from generators.execute(pgconn)
        yield from check_results(pgconn, results)
        self._described = results[-1]
        self._tx = tx
        self._spot = 0

    def _fetch_gen(self, count: int | None) -> PQGen["list[Any]"]:
        """FETCH FORWARD `count` rows (None: ALL) and convert them."""
        self._check_declared()
        conn = self._conn
        yield from conn._start_query()
        howmany = b"ALL" if count is None else b"%d" % count
        command = b"FETCH FORWARD " + howmany + b" FROM " + self._ident()
        result = yield from conn._exec_command(command)
        for old in self._results:
            old.clear()
        self._results = [result]
        self._select_result(0)
        assert self._tx is not None
        rows: list[Any] = self._tx.load_rows(0, result.ntuples, self._make_row)
        if self._spot is not None:
            self._spot += len(rows)
        return rows

    def _scroll_gen(self, value: int, mode: str = "relative") -> PQGen[None]:
        """MOVE the cursor so that the next fetch starts at the target index."""
        self._check_declared()
        if mode == "relative":
            if self._spot is None:
                raise e.ProgrammingError(
                    "the position of the cursor is not known:"
                    " use scroll(value, 'absolute')"
                )
            if value == 0:
                return
            target = self._spot + value
        elif mode == "absolute":
            target = value
        else:
            raise ValueError(f"bad mode {mode!r}: use 'relative' or 'absolute'")
        if target < 0:
            raise IndexError(_OUTSIDE)
        conn = self._conn
        yield from conn._start_query()
        command = b"MOVE ABSOLUTE %d FROM " % target + self._ident()
        result = yield from conn._exec_command(command)
        moved = result.command_tuples
        result.clear()
        if target > 0 and not moved:
            self._spot = None
            raise IndexError(_OUTSIDE)
        self._spot = target

    def _close_portal_gen(self) -> PQGen[None]:
        """CLOSE the cursor on the server when it is still there."""
        self._declared = False
        conn = self._conn
        if conn.closed:
            return
        pgconn = conn.pgconn
        status = pgconn.transaction_status
        if status == TransactionStatus.IDLE:
            # Without WITH HOLD the cursor ended with its transaction.
            if not self._withhold:
                return
        elif status != TransactionStatus.INTRANS:
            return
        # A CLOSE of a cursor that is gone would abort the transaction.
        pgconn.send_query_params(_EXISTS, [self._name.encode(conn._codec)], None)
        results = yield from generators.execute(pgconn)
        yield from check_results(pgconn, results)
        exists = results[-1].ntuples > 0
        for result in results:
            result.clear()
        if exists:
            result = yield from conn._exec_command(b"CLOSE " + self._ident())
            result.clear()

    def _close_gen(self) -> PQGen[None]:
        """Close the cursor: on the server when possible, then locally."""
        if self._closed:
            return
        try:
            if self._declared:
                yield from self._close_portal_gen()
        finally:
            self._close()
```

Tests to write first. Before the code exists: `tests/test_servercur_base.py` fails to import: `No module named 'stuntgres._servercur_base'`.

The complete file `tests/test_servercur_base.py` (7 tests):

```python
"""Tests of the base of the server-side cursors: statements and generators."""
from __future__ import annotations

from typing import Any
from unittest import mock

from stuntgres import errors as e
from stuntgres import rows
from stuntgres._cur_core import CurCore
from stuntgres._servercur_base import ServerCursorBase
from tests.dbapi_util import ConnTestCase, run, status

SEVEN = "select g as id, 'n' || g as name from generate_series(1, 7) g"
NO_RESULT = "no result available: execute a statement first"


class ServerCursorBaseTest(ConnTestCase):
    def setUp(self) -> None:
        super().setUp()
        self.conn = self.connect()
        self.sent: list[bytes] = []
        pgconn_type = type(self.conn.pgconn)
        for name in ("send_query", "send_query_params", "send_describe_portal"):
            real = getattr(pgconn_type, name)

            def spy(pgconn: Any, command: bytes, *args: Any, _real: Any = real) -> Any:
                self.sent.append(command)
                return _real(pgconn, command, *args)

            patcher = mock.patch.object(pgconn_type, name, spy)
            patcher.start()
            self.addCleanup(patcher.stop)

    def drive(self, gen: Any) -> Any:
        with self.conn.lock:
            return self.conn.wait(gen)

    def cursor(self, name: str = "c", **kwargs: Any) -> ServerCursorBase:
        return ServerCursorBase(self.conn, name, **kwargs)

    def test_new_cursor(self) -> None:
        cur = self.cursor("big")
        self.assertIsInstance(cur, CurCore)
        self.assertEqual(
            (cur.name, cur.scrollable, cur.withhold, cur.itersize, cur.arraysize),
            ("big", None, False, 100, 1),
        )
        self.assertIs(cur.connection, self.conn)
        self.assertIs(cur.row_factory, rows.tuple_row)
        self.assertIsNone(cur.description)
        self.assertIsNone(cur.rownumber)
        self.assertEqual(cur.rowcount, -1)
        self.assertIsNone(cur.lastrowid)
        self.assertIsNone(cur.setinputsizes([]))
        self.assertIsNone(cur.setoutputsize(1))
        self.assertEqual(self.sent, [])
        for gen in (cur._fetch_gen(1), cur._scroll_gen(1)):
            with self.assertRaises(e.ProgrammingError) as caught:
                self.drive(gen)
            self.assertEqual(str(caught.exception), NO_RESULT)
        with self.assertRaises(e.ProgrammingError) as caught:
            self.cursor("")
        self.assertEqual(str(caught.exception), "a server-side cursor needs a name")

    def test_declare_describe_fetch_close(self) -> None:
        cur = self.cursor()
        self.assertIsNone(self.drive(cur._declare_gen(SEVEN)))
        self.assertEqual(
            self.sent, [b"BEGIN", b'DECLARE "c" CURSOR FOR ' + SEVEN.encode(), b"c"]
        )
        self.assertEqual(status(self.conn), "INTRANS")
        description = cur.description
        assert description is not None
        self.assertEqual(
            [tuple(column) for column in description],
            [
                ("id", 23, None, 4, None, None, None),
                ("name", 25, None, None, None, None, None),
            ],
        )
        self.assertIs(cur.description, description)
        self.assertEqual((cur.rownumber, cur.rowcount), (0, -1))
        self.assertIsNone(cur.pgresult)
        self.assertIsNone(cur.statusmessage)
        del self.sent[:]
        self.assertEqual(self.drive(cur._fetch_gen(1)), [(1, "n1")])
        self.assertEqual(
            self.drive(cur._fetch_gen(3)), [(2, "n2"), (3, "n3"), (4, "n4")]
        )
        self.assertEqual((cur.rownumber, cur.rowcount), (4, -1))
        self.assertEqual(cur.statusmessage, "FETCH 3")
        self.assertEqual(
            self.drive(cur._fetch_gen(None)), [(5, "n5"), (6, "n6"), (7, "n7")]
        )
        self.assertEqual(self.drive(cur._fetch_gen(2)), [])
        self.assertEqual(cur.rownumber, 7)
        self.assertEqual(
            self.sent,
            [
                b'FETCH FORWARD 1 FROM "c"', b'FETCH FORWARD 3 FROM "c"',
                b'FETCH FORWARD ALL FROM "c"', b'FETCH FORWARD 2 FROM "c"',
            ],
        )
        del self.sent[:]
        self.assertIsNone(self.drive(cur._close_gen()))
        self.assertEqual(
            self.sent,
            [b"SELECT 1 FROM pg_catalog.pg_cursors WHERE name = $1", b'CLOSE "c"'],
        )
        self.assertIs(cur.closed, True)
        self.assertIsNone(cur.description)
        self.assertEqual(status(self.conn), "INTRANS")
        self.assertEqual(run(self.conn, "select count(*) from pg_cursors"), [(b"0",)])
        del self.sent[:]
        self.drive(cur._close_gen())
        self.assertEqual(self.sent, [])
        with self.assertRaises(e.InterfaceError) as caught:
            self.drive(cur._fetch_gen(1))
        self.assertEqual(str(caught.exception), "the cursor is closed")

    def test_the_declare_statement(self) -> None:
        cases = [
            ({}, b'DECLARE "c" CURSOR FOR select 1'),
            ({"scrollable": True}, b'DECLARE "c" SCROLL CURSOR FOR select 1'),
            ({"scrollable": False}, b'DECLARE "c" NO SCROLL CURSOR FOR select 1'),
            ({"withhold": True}, b'DECLARE "c" CURSOR WITH HOLD FOR select 1'),
            (
                {"scrollable": True, "withhold": True},
                b'DECLARE "c" SCROLL CURSOR WITH HOLD FOR select 1',
            ),
        ]
        for kwargs, statement in cases:
            cur = self.cursor(**kwargs)
            self.drive(cur._declare_gen("select 1"))
            self.assertEqual(self.sent[-2], statement)
            self.drive(cur._close_gen())

    def test_the_name_is_quoted(self) -> None:
        cur = self.cursor('My "Cur"')
        self.drive(cur._declare_gen("select 1 as one where false"))
        self.assertEqual(
            self.sent[1:],
            [b'DECLARE "My ""Cur""" CURSOR FOR select 1 as one where false',
             b'My "Cur"'],
        )
        description = cur.description
        assert description is not None
        self.assertEqual([column.name for column in description], ["one"])
        self.assertEqual(self.drive(cur._fetch_gen(None)), [])
        self.assertEqual(self.sent[-1], b'FETCH FORWARD ALL FROM "My ""Cur"""')
        self.drive(cur._close_gen())
        self.assertEqual(self.sent[-1], b'CLOSE "My ""Cur"""')

    def test_parameters_and_row_factory(self) -> None:
        cur = self.cursor(row_factory=rows.dict_row)
        with self.assertRaises(e.ProgrammingError) as caught:
            self.drive(cur._declare_gen(SEVEN + " where g > %s", {"a": 1}))
        self.assertEqual(
            str(caught.exception),
            "the query has positional placeholders (%s) but the parameters are a"
            " mapping",
        )
        self.assertEqual(self.sent, [])
        self.drive(cur._declare_gen(SEVEN + " where g > %s and 'n%%' <> %s", (5, "x")))
        self.assertEqual(
            self.sent[-2],
            b'DECLARE "c" CURSOR FOR ' + SEVEN.encode()
            + b" where g > $1 and 'n%' <> $2",
        )
        self.assertEqual(
            self.drive(cur._fetch_gen(None)),
            [{"id": 6, "name": "n6"}, {"id": 7, "name": "n7"}],
        )
        cur.row_factory = rows.tuple_row
        self.assertEqual(self.drive(cur._fetch_gen(None)), [])

    def test_withhold(self) -> None:
        cur = self.cursor("h", withhold=True)
        self.drive(cur._declare_gen(SEVEN))
        self.assertEqual(self.drive(cur._fetch_gen(2)), [(1, "n1"), (2, "n2")])
        self.conn.commit()
        self.assertEqual(status(self.conn), "IDLE")
        self.assertEqual(self.drive(cur._fetch_gen(1)), [(3, "n3")])
        self.assertEqual(status(self.conn), "INTRANS")
        self.conn.commit()
        del self.sent[:]
        self.drive(cur._close_gen())
        self.assertEqual(
            self.sent,
            [b"SELECT 1 FROM pg_catalog.pg_cursors WHERE name = $1", b'CLOSE "h"'],
        )
        self.assertEqual(status(self.conn), "IDLE")
        self.assertEqual(run(self.conn, "select count(*) from pg_cursors"), [(b"0",)])

    def test_errors(self) -> None:
        cur = self.cursor()
        with self.assertRaises(e.ProgrammingError) as caught:
            self.drive(cur._declare_gen("select * from nosuch"))
        self.assertEqual(caught.exception.sqlstate, "42P01")
        self.assertIsNone(cur.description)
        with self.assertRaises(e.ProgrammingError) as caught:
            self.drive(cur._fetch_gen(1))
        self.assertEqual(str(caught.exception), NO_RESULT)
        self.conn.rollback()
        with self.assertRaises(e.ProgrammingError) as caught:
            self.drive(cur._declare_gen("select 1; select 2"))
        self.assertEqual(caught.exception.sqlstate, "42601")
        self.conn.rollback()
        self.drive(cur._declare_gen(SEVEN))
        self.conn.rollback()
        with self.assertRaises(e.InvalidCursorName) as caught_gone:
            self.drive(cur._fetch_gen(1))
        self.assertEqual(caught_gone.exception.sqlstate, "34000")
        self.assertEqual(
            str(caught_gone.exception).splitlines()[0], 'cursor "c" does not exist'
        )
```

Acceptance: `--mode oracle test_servercur_base` and `--mode server test_servercur_base` run 7 tests each and pass.

#### CPYTHON-SERVERCUR-01B-MOVE

Files: `tests/test_servercur_move.py`. Design sections: 3.2, 3.3.

Step. Tests only: write `tests/test_servercur_move.py` as printed. It covers `MOVE` to every
kind of target, moves outside the result, a `NO SCROLL` cursor, a second `execute` on
the same cursor and `close()` when the cursor is gone. If a test fails, the fault is in
`stuntgres/_servercur_base.py` or below: stop and report (C8).

Tests to write first. Before the code exists: Nothing to see fail: the code exists (CPYTHON-SERVERCUR-01-BASE). Every test must pass in oracle and server mode.

The complete file `tests/test_servercur_move.py` (5 tests):

```python
"""Tests of the base of the server-side cursors: MOVE, re-execution, CLOSE."""
from __future__ import annotations

from typing import Any
from unittest import mock

from stuntgres import errors as e
from stuntgres._servercur_base import ServerCursorBase
from tests.dbapi_util import ConnTestCase, run, status

SEVEN = "select g as id, 'n' || g as name from generate_series(1, 7) g"
OUTSIDE = "the position is outside the result"


class ServerCursorMoveTest(ConnTestCase):
    def setUp(self) -> None:
        super().setUp()
        self.conn = self.connect()
        self.sent: list[bytes] = []
        pgconn_type = type(self.conn.pgconn)
        for name in ("send_query", "send_query_params", "send_describe_portal"):
            real = getattr(pgconn_type, name)

            def spy(pgconn: Any, command: bytes, *args: Any, _real: Any = real) -> Any:
                self.sent.append(command)
                return _real(pgconn, command, *args)

            patcher = mock.patch.object(pgconn_type, name, spy)
            patcher.start()
            self.addCleanup(patcher.stop)

    def drive(self, gen: Any) -> Any:
        with self.conn.lock:
            return self.conn.wait(gen)

    def cursor(self, name: str = "c", **kwargs: Any) -> ServerCursorBase:
        return ServerCursorBase(self.conn, name, **kwargs)

    def test_scroll(self) -> None:
        cur = self.cursor(scrollable=True)
        self.drive(cur._declare_gen(SEVEN))
        del self.sent[:]
        self.assertIsNone(self.drive(cur._scroll_gen(2)))
        self.assertEqual(cur.rownumber, 2)
        self.assertEqual(self.drive(cur._fetch_gen(1)), [(3, "n3")])
        self.drive(cur._scroll_gen(-3))
        self.assertEqual(self.drive(cur._fetch_gen(1)), [(1, "n1")])
        self.drive(cur._scroll_gen(5, "absolute"))
        self.assertEqual(self.drive(cur._fetch_gen(1)), [(6, "n6")])
        self.drive(cur._scroll_gen(0, "relative"))
        self.drive(cur._scroll_gen(7, "absolute"))
        self.assertEqual(cur.rownumber, 7)
        self.assertEqual(self.drive(cur._fetch_gen(1)), [])
        self.drive(cur._scroll_gen(-1))
        self.assertEqual(self.drive(cur._fetch_gen(None)), [(7, "n7")])
        self.drive(cur._scroll_gen(0, "absolute"))
        self.assertEqual(self.drive(cur._fetch_gen(1)), [(1, "n1")])
        self.assertEqual(
            [s for s in self.sent if s.startswith(b"MOVE")],
            [
                b'MOVE ABSOLUTE 2 FROM "c"', b'MOVE ABSOLUTE 0 FROM "c"',
                b'MOVE ABSOLUTE 5 FROM "c"', b'MOVE ABSOLUTE 7 FROM "c"',
                b'MOVE ABSOLUTE 6 FROM "c"', b'MOVE ABSOLUTE 0 FROM "c"',
            ],
        )

    def test_scroll_outside_the_result(self) -> None:
        cur = self.cursor(scrollable=True)
        self.drive(cur._declare_gen(SEVEN))
        del self.sent[:]
        for gen in (cur._scroll_gen(-1), cur._scroll_gen(-1, "absolute")):
            with self.assertRaises(IndexError) as caught:
                self.drive(gen)
            self.assertEqual(str(caught.exception), OUTSIDE)
        self.assertEqual(self.sent, [])
        self.assertEqual(cur.rownumber, 0)
        with self.assertRaises(ValueError) as caught_mode:
            self.drive(cur._scroll_gen(1, "sideways"))
        self.assertEqual(
            str(caught_mode.exception),
            "bad mode 'sideways': use 'relative' or 'absolute'",
        )
        with self.assertRaises(IndexError) as caught:
            self.drive(cur._scroll_gen(8))
        self.assertEqual(str(caught.exception), OUTSIDE)
        self.assertEqual(self.sent, [b'MOVE ABSOLUTE 8 FROM "c"'])
        self.assertIsNone(cur.rownumber)
        self.assertEqual(self.drive(cur._fetch_gen(1)), [])
        self.assertIsNone(cur.rownumber)
        with self.assertRaises(e.ProgrammingError) as caught_pos:
            self.drive(cur._scroll_gen(-1))
        self.assertEqual(
            str(caught_pos.exception),
            "the position of the cursor is not known: use scroll(value, 'absolute')",
        )
        self.drive(cur._scroll_gen(6, "absolute"))
        self.assertEqual(cur.rownumber, 6)
        self.assertEqual(self.drive(cur._fetch_gen(None)), [(7, "n7")])

    def test_no_scroll_cursor_moves_forward_only(self) -> None:
        cur = self.cursor(scrollable=False)
        self.drive(cur._declare_gen(SEVEN))
        self.drive(cur._scroll_gen(0, "absolute"))
        self.assertEqual(self.drive(cur._fetch_gen(2)), [(1, "n1"), (2, "n2")])
        self.drive(cur._scroll_gen(3))
        self.assertEqual(self.drive(cur._fetch_gen(1)), [(6, "n6")])
        with self.assertRaises(e.OperationalError) as caught:
            self.drive(cur._scroll_gen(-2))
        self.assertEqual(caught.exception.sqlstate, "55000")
        self.assertEqual(
            str(caught.exception).splitlines()[0], "cursor can only scan forward"
        )
        self.assertEqual(status(self.conn), "INERROR")

    def test_executing_again_closes_the_first_cursor(self) -> None:
        cur = self.cursor()
        self.drive(cur._declare_gen(SEVEN))
        self.drive(cur._fetch_gen(2))
        del self.sent[:]
        self.drive(cur._declare_gen("select 'again' as word"))
        self.assertEqual(
            self.sent,
            [
                b"SELECT 1 FROM pg_catalog.pg_cursors WHERE name = $1", b'CLOSE "c"',
                b"DECLARE \"c\" CURSOR FOR select 'again' as word", b"c",
            ],
        )
        self.assertEqual(cur.rownumber, 0)
        description = cur.description
        assert description is not None
        self.assertEqual([column.name for column in description], ["word"])
        self.assertEqual(self.drive(cur._fetch_gen(None)), [("again",)])

    def test_close_when_the_cursor_is_gone(self) -> None:
        cur = self.cursor()
        self.drive(cur._declare_gen(SEVEN))
        self.conn.rollback()
        del self.sent[:]
        self.drive(cur._close_gen())
        self.assertEqual(self.sent, [])
        self.assertIs(cur.closed, True)
        cur = self.cursor()
        self.drive(cur._declare_gen(SEVEN))
        self.conn.rollback()
        run(self.conn, "select 1")
        del self.sent[:]
        self.drive(cur._close_gen())
        self.assertEqual(
            self.sent, [b"SELECT 1 FROM pg_catalog.pg_cursors WHERE name = $1"]
        )
        self.assertEqual(status(self.conn), "INTRANS")
        cur = self.cursor()
        self.drive(cur._declare_gen(SEVEN))
        with self.assertRaises(e.DataError):
            run(self.conn, "select 1/0")
        del self.sent[:]
        self.drive(cur._close_gen())
        self.assertEqual(self.sent, [])
        self.assertEqual(status(self.conn), "INERROR")
```

Acceptance: `--mode oracle test_servercur_move` and `--mode server test_servercur_move` run 5 tests each and pass.

#### CPYTHON-SERVERCUR-02-SYNC

Files: `stuntgres/server_cursor.py`, `stuntgres/_conn_servercur.py` (replaces the stub), `tests/test_server_cursor.py`, `tests/test_connection.py` (edit: one test). Design sections: 3.4.

Step.
1. Write `stuntgres/server_cursor.py` as printed: `ServerCursor` (3.4).
2. Replace the stub `stuntgres/_conn_servercur.py` by the file printed
   (`server_cursor_factory = ServerCursor`, `EXPORTS = ("ServerCursor",)`).
3. In `tests/test_connection.py` replace the whole method
   `test_named_cursor_is_not_available` (five lines; a named cursor is available now) by

   ```python
       def test_named_cursor_comes_from_the_factory_attribute(self) -> None:
           conn = self.connect()
           factory = type(conn).server_cursor_factory  # type: ignore[attr-defined]
           made = conn.cursor("big")
           self.assertIs(type(made), factory)
           self.assertEqual(made.name, "big")  # type: ignore[attr-defined]
   ```

   Leave `test_named_cursor_goes_to_the_feature_hook` and everything else unchanged.

`test_autocommit` needs CPYTHON-ENGINE-06-DECLARE-EXTENDED-OUTSIDE-BLOCK in server mode
(a prerequisite).

The complete file `stuntgres/server_cursor.py`:

```python
"""The synchronous server-side cursor."""
from __future__ import annotations

from types import TracebackType
from typing import Any, Iterable, Iterator

from . import errors as e
from ._queries import Params, Query
from ._servercur_base import ServerCursorBase


class ServerCursor(ServerCursorBase):
    """A named cursor: DECLARE, FETCH, MOVE and CLOSE on the server (blocking)."""

    __module__ = "stuntgres"

    def __enter__(self) -> ServerCursor:
        return self

    def __exit__(
        self,
        exc_type: type[BaseException] | None,
        exc_val: BaseException | None,
        exc_tb: TracebackType | None,
    ) -> None:
        self.close()

    def close(self) -> None:
        """Close the cursor on the server, when it still exists, and locally."""
        if self._closed:
            return
        with self._conn.lock:
            self._conn.wait(self._close_gen())

    def execute(self, query: Query, params: Params | None = None) -> ServerCursor:
        """Declare the cursor for one query and return the cursor itself."""
        with self._conn.lock:
            self._conn.wait(self._declare_gen(query, params))
        return self

    def executemany(
        self, query: Query, params_seq: Iterable[Params], *, returning: bool = False
    ) -> None:
        """Not available: a server-side cursor runs one query."""
        raise e.NotSupportedError(
            "executemany() is not supported on a server-side cursor"
        )

    def _fetch(self, count: int | None) -> list[Any]:
        with self._conn.lock:
            rows: list[Any] = self._conn.wait(self._fetch_gen(count))
        return rows

    def fetchone(self) -> Any:
        """Fetch the next row from the server; None when there is none left."""
        rows = self._fetch(1)
        return rows[0] if rows else None

    def fetchmany(self, size: int | None = None) -> list[Any]:
        """Fetch the next `size` rows (default: arraysize); fewer at the end."""
        if size is None:
            size = self.arraysize
        if size <= 0:
            self._check_declared()
            return []
        return self._fetch(size)

    def fetchall(self) -> list[Any]:
        """Fetch all the rows that are left."""
        return self._fetch(None)

    def scroll(self, value: int, mode: str = "relative") -> None:
        """Move the cursor; IndexError when the target is outside the result."""
        with self._conn.lock:
            self._conn.wait(self._scroll_gen(value, mode))

    def nextset(self) -> None:
        """Return None: a server-side cursor has one result."""
        return None

    def __iter__(self) -> Iterator[Any]:
        while True:
            size = self.itersize
            rows = self._fetch(size)
            yield from rows
            if len(rows) < size:
                break
```

The complete file `stuntgres/_conn_servercur.py`:

```python
"""Connection.cursor(name): the class of named cursors."""
from __future__ import annotations

from typing import Any

from .server_cursor import ServerCursor

EXPORTS: tuple[str, ...] = ("ServerCursor",)


class ServerCursorMixin:
    """Members of Connection: named cursors."""

    # What cursor(name) calls; assign another callable to change it.
    server_cursor_factory: Any = ServerCursor
```

Tests to write first. Before the code exists: `tests/test_server_cursor.py` fails to import: `No module named 'stuntgres.server_cursor'`.

The complete file `tests/test_server_cursor.py` (12 tests):

```python
"""Tests of ServerCursor and Connection.cursor(name)."""
from __future__ import annotations

from typing import Any

import stuntgres
from stuntgres import errors as e
from stuntgres import rows
from stuntgres._servercur_base import ServerCursorBase
from stuntgres.cursor import Cursor
from stuntgres.server_cursor import ServerCursor
from tests.dbapi_util import ConnTestCase, run, status

SEVEN = "select g as id, 'n' || g as name from generate_series(1, 7) g"


class ServerCursorTest(ConnTestCase):
    def setUp(self) -> None:
        super().setUp()
        self.conn = self.connect()

    def test_names(self) -> None:
        self.assertIs(stuntgres.ServerCursor, ServerCursor)
        self.assertEqual(ServerCursor.__module__, "stuntgres")
        self.assertTrue(issubclass(ServerCursor, ServerCursorBase))
        self.assertFalse(issubclass(ServerCursor, Cursor))
        self.assertIs(type(self.conn).server_cursor_factory, ServerCursor)

    def test_cursor_with_a_name(self) -> None:
        conn = self.conn
        cur = conn.cursor("big")
        self.assertIs(type(cur), ServerCursor)
        self.assertEqual((cur.name, cur.scrollable, cur.withhold), ("big", None, False))
        self.assertIs(cur.connection, conn)
        self.assertEqual(status(conn), "IDLE")
        cur = conn.cursor(
            "s", row_factory=rows.dict_row, scrollable=True, withhold=True
        )
        self.assertEqual((cur.name, cur.scrollable, cur.withhold), ("s", True, True))
        self.assertIs(cur.row_factory, rows.dict_row)
        self.assertIs(type(conn.cursor()), Cursor)

    def test_execute_and_fetch(self) -> None:
        cur = self.conn.cursor("c")
        self.assertIs(cur.execute(SEVEN + " where g <> %s", (4,)), cur)
        self.assertEqual(status(self.conn), "INTRANS")
        description = cur.description
        assert description is not None
        self.assertEqual([column.name for column in description], ["id", "name"])
        self.assertEqual((cur.rownumber, cur.rowcount), (0, -1))
        self.assertEqual(cur.fetchone(), (1, "n1"))
        self.assertEqual(cur.fetchmany(), [(2, "n2")])
        self.assertEqual(cur.fetchmany(2), [(3, "n3"), (5, "n5")])
        self.assertEqual(cur.fetchmany(0), [])
        self.assertEqual(cur.rownumber, 4)
        cur.arraysize = 5
        self.assertEqual(cur.fetchmany(), [(6, "n6"), (7, "n7")])
        self.assertEqual(cur.fetchall(), [])
        self.assertIsNone(cur.fetchone())
        self.assertEqual(cur.rownumber, 6)
        self.assertIsNone(cur.nextset())
        cur.close()
        self.assertIs(cur.closed, True)
        cur.close()
        self.assertEqual(run(self.conn, "select count(*) from pg_cursors"), [(b"0",)])
        with self.assertRaises(e.InterfaceError) as caught:
            cur.fetchone()
        self.assertEqual(str(caught.exception), "the cursor is closed")

    def test_iteration_fetches_itersize_rows_at_a_time(self) -> None:
        cur = self.conn.cursor("c")
        cur.itersize = 3
        cur.execute(SEVEN)
        seen: list[Any] = []
        for row in cur:
            seen.append((row[0], cur.rownumber))
        self.assertEqual(
            seen, [(1, 3), (2, 3), (3, 3), (4, 6), (5, 6), (6, 6), (7, 7)]
        )
        cur.execute("select generate_series(1, 6)")
        self.assertEqual([row[0] for row in cur], [1, 2, 3, 4, 5, 6])
        self.assertEqual(cur.rownumber, 6)

    def test_scroll(self) -> None:
        cur = self.conn.cursor("c", scrollable=True)
        cur.execute(SEVEN)
        self.assertIsNone(cur.scroll(2))
        self.assertEqual(cur.fetchone(), (3, "n3"))
        cur.scroll(-2)
        self.assertEqual(cur.fetchone(), (2, "n2"))
        cur.scroll(6, "absolute")
        self.assertEqual(cur.fetchall(), [(7, "n7")])
        cur.scroll(0, mode="absolute")
        self.assertEqual(cur.rownumber, 0)
        with self.assertRaises(IndexError) as caught:
            cur.scroll(8)
        self.assertEqual(str(caught.exception), "the position is outside the result")
        self.assertIsNone(cur.rownumber)

    def test_with_block_closes(self) -> None:
        with self.conn.cursor("c") as cur:
            cur.execute(SEVEN)
            self.assertEqual(run(self.conn, "select name from pg_cursors"), [(b"c",)])
        self.assertIs(cur.closed, True)
        self.assertEqual(run(self.conn, "select count(*) from pg_cursors"), [(b"0",)])

    def test_withhold_outlives_the_transaction(self) -> None:
        conn = self.conn
        cur = conn.cursor("h", withhold=True)
        cur.execute(SEVEN)
        self.assertEqual(cur.fetchone(), (1, "n1"))
        conn.commit()
        self.assertEqual(cur.fetchone(), (2, "n2"))
        conn.commit()
        cur.close()
        self.assertEqual(status(conn), "IDLE")

    def test_autocommit(self) -> None:
        conn = self.conn
        conn.autocommit = True
        cur = conn.cursor("h", withhold=True)
        cur.execute(SEVEN)
        self.assertEqual(status(conn), "IDLE")
        self.assertEqual(cur.fetchmany(2), [(1, "n1"), (2, "n2")])
        cur.close()
        self.assertEqual(run(conn, "select count(*) from pg_cursors"), [(b"0",)])
        cur = conn.cursor("c")
        with self.assertRaises(e.InternalError) as caught:
            cur.execute(SEVEN)
        self.assertEqual(caught.exception.sqlstate, "25P01")
        self.assertEqual(
            str(caught.exception),
            "DECLARE CURSOR can only be used in transaction blocks",
        )
        self.assertIsNone(cur.description)
        cur.close()

    def test_transaction_block(self) -> None:
        conn = self.conn
        with conn.transaction():
            with conn.cursor("c") as cur:
                cur.execute(SEVEN)
                self.assertEqual(cur.fetchone(), (1, "n1"))
        self.assertEqual(status(conn), "IDLE")

    def test_what_a_server_cursor_does_not_do(self) -> None:
        cur = self.conn.cursor("c")
        with self.assertRaises(e.NotSupportedError) as caught:
            cur.executemany("select %s", [(1,), (2,)])
        self.assertEqual(
            str(caught.exception),
            "executemany() is not supported on a server-side cursor",
        )
        self.assertFalse(hasattr(cur, "copy"))
        self.assertFalse(hasattr(cur, "callproc"))
        with self.assertRaises(e.ProgrammingError) as caught_fetch:
            cur.fetchmany(0)
        self.assertEqual(
            str(caught_fetch.exception),
            "no result available: execute a statement first",
        )

    def test_closed_connection(self) -> None:
        cur = self.conn.cursor("c")
        cur.execute(SEVEN)
        self.conn.close()
        with self.assertRaises(e.OperationalError) as caught:
            cur.fetchone()
        self.assertEqual(str(caught.exception), "the connection is closed")
        cur.close()
        self.assertIs(cur.closed, True)
        with self.assertRaises(e.OperationalError):
            self.conn.cursor("again")

    def test_repr(self) -> None:
        cur = self.conn.cursor("c")
        self.assertRegex(
            repr(cur), r"^<stuntgres\.ServerCursor \[no result\] at 0x[0-9a-f]+>$"
        )
        cur.execute(SEVEN)
        cur.fetchone()
        self.assertRegex(
            repr(cur), r"^<stuntgres\.ServerCursor \[TUPLES_OK\] at 0x[0-9a-f]+>$"
        )
        cur.close()
        self.assertRegex(
            repr(cur), r"^<stuntgres\.ServerCursor \[closed\] at 0x[0-9a-f]+>$"
        )
```

Acceptance: `--mode oracle test_server_cursor test_connection` and `--mode server test_server_cursor test_connection` pass (`test_server_cursor` has 12 tests).

#### CPYTHON-SERVERCUR-03-ASYNC

Files: `stuntgres/server_cursor_async.py`, `stuntgres/_conn_servercur_async.py` (replaces the stub), `tests/test_server_cursor_async.py`, `tests/test_async_connection.py` (edit: one test). Design sections: 3.5.

Step.
1. Write `stuntgres/server_cursor_async.py` as printed: `AsyncServerCursor` (3.5). It
   must not import `asyncio`.
2. Replace the stub `stuntgres/_conn_servercur_async.py` by the file printed.
3. In `tests/test_async_connection.py` replace the whole method
   `test_named_cursor_is_not_available` (the last five lines of the file) by

   ```python
       async def test_named_cursor_comes_from_the_factory_attribute(self) -> None:
           conn = await self.connect()
           factory = type(conn).server_cursor_factory  # type: ignore[attr-defined]
           made = conn.cursor("big")
           self.assertIs(type(made), factory)
           self.assertEqual(made.name, "big")  # type: ignore[attr-defined]
   ```

The complete file `stuntgres/server_cursor_async.py`:

```python
"""The asyncio server-side cursor."""
from __future__ import annotations

from types import TracebackType
from typing import Any, AsyncIterator, Iterable

from . import errors as e
from ._queries import Params, Query
from ._servercur_base import ServerCursorBase


class AsyncServerCursor(ServerCursorBase):
    """A named cursor for asyncio: the twin of ServerCursor."""

    __module__ = "stuntgres"

    async def __aenter__(self) -> AsyncServerCursor:
        return self

    async def __aexit__(
        self,
        exc_type: type[BaseException] | None,
        exc_val: BaseException | None,
        exc_tb: TracebackType | None,
    ) -> None:
        await self.close()

    async def close(self) -> None:
        """Close the cursor on the server, when it still exists, and locally."""
        if self._closed:
            return
        async with self._conn.lock:
            await self._conn.wait(self._close_gen())

    async def execute(
        self, query: Query, params: Params | None = None
    ) -> AsyncServerCursor:
        """Declare the cursor for one query and return the cursor itself."""
        async with self._conn.lock:
            await self._conn.wait(self._declare_gen(query, params))
        return self

    async def executemany(
        self, query: Query, params_seq: Iterable[Params], *, returning: bool = False
    ) -> None:
        """Not available: a server-side cursor runs one query."""
        raise e.NotSupportedError(
            "executemany() is not supported on a server-side cursor"
        )

    async def _fetch(self, count: int | None) -> list[Any]:
        async with self._conn.lock:
            rows: list[Any] = await self._conn.wait(self._fetch_gen(count))
        return rows

    async def fetchone(self) -> Any:
        """Fetch the next row from the server; None when there is none left."""
        rows = await self._fetch(1)
        return rows[0] if rows else None

    async def fetchmany(self, size: int | None = None) -> list[Any]:
        """Fetch the next `size` rows (default: arraysize); fewer at the end."""
        if size is None:
            size = self.arraysize
        if size <= 0:
            self._check_declared()
            return []
        return await self._fetch(size)

    async def fetchall(self) -> list[Any]:
        """Fetch all the rows that are left."""
        return await self._fetch(None)

    async def scroll(self, value: int, mode: str = "relative") -> None:
        """Move the cursor; IndexError when the target is outside the result."""
        async with self._conn.lock:
            await self._conn.wait(self._scroll_gen(value, mode))

    def nextset(self) -> None:
        """Return None: a server-side cursor has one result."""
        return None

    async def __aiter__(self) -> AsyncIterator[Any]:
        while True:
            size = self.itersize
            rows = await self._fetch(size)
            for row in rows:
                yield row
            if len(rows) < size:
                break
```

The complete file `stuntgres/_conn_servercur_async.py`:

```python
"""AsyncConnection.cursor(name): the class of named cursors."""
from __future__ import annotations

from typing import Any

from .server_cursor_async import AsyncServerCursor

EXPORTS: tuple[str, ...] = ("AsyncServerCursor",)


class AsyncServerCursorMixin:
    """Members of AsyncConnection: named cursors."""

    # What cursor(name) calls; assign another callable to change it.
    server_cursor_factory: Any = AsyncServerCursor
```

Tests to write first. Before the code exists: `tests/test_server_cursor_async.py` fails to import: `No module named 'stuntgres.server_cursor_async'`.

The complete file `tests/test_server_cursor_async.py` (5 tests):

```python
"""Tests of AsyncServerCursor and AsyncConnection.cursor(name)."""
from __future__ import annotations

from typing import Any

import stuntgres
from stuntgres import errors as e
from stuntgres._servercur_base import ServerCursorBase
from stuntgres.cursor_async import AsyncCursor
from stuntgres.server_cursor_async import AsyncServerCursor
from tests.dbapi_util import status
from tests.dbapi_util_async import AsyncConnTestCase, arun

SEVEN = "select g as id, 'n' || g as name from generate_series(1, 7) g"


class AsyncServerCursorTest(AsyncConnTestCase):
    async def test_names(self) -> None:
        conn = await self.connect()
        self.assertIs(stuntgres.AsyncServerCursor, AsyncServerCursor)
        self.assertEqual(AsyncServerCursor.__module__, "stuntgres")
        self.assertTrue(issubclass(AsyncServerCursor, ServerCursorBase))
        self.assertIs(type(conn).server_cursor_factory, AsyncServerCursor)
        cur = conn.cursor("big", scrollable=False, withhold=True)
        self.assertIs(type(cur), AsyncServerCursor)
        self.assertEqual((cur.name, cur.scrollable, cur.withhold), ("big", False, True))
        self.assertIs(type(conn.cursor()), AsyncCursor)

    async def test_execute_and_fetch(self) -> None:
        conn = await self.connect()
        cur = conn.cursor("c")
        self.assertIs(await cur.execute(SEVEN + " where g <> %s", (4,)), cur)
        self.assertEqual(status(conn), "INTRANS")
        description = cur.description
        assert description is not None
        self.assertEqual([column.name for column in description], ["id", "name"])
        self.assertEqual(await cur.fetchone(), (1, "n1"))
        self.assertEqual(await cur.fetchmany(), [(2, "n2")])
        self.assertEqual(await cur.fetchmany(2), [(3, "n3"), (5, "n5")])
        self.assertEqual(await cur.fetchmany(0), [])
        self.assertEqual(await cur.fetchall(), [(6, "n6"), (7, "n7")])
        self.assertIsNone(await cur.fetchone())
        self.assertEqual((cur.rownumber, cur.rowcount), (6, -1))
        self.assertIsNone(cur.nextset())
        await cur.close()
        await cur.close()
        self.assertIs(cur.closed, True)
        self.assertEqual(await arun(conn, "select count(*) from pg_cursors"), [(b"0",)])

    async def test_iteration_and_scroll(self) -> None:
        conn = await self.connect()
        async with conn.cursor("c", scrollable=True) as cur:
            cur.itersize = 3
            await cur.execute(SEVEN)
            seen: list[Any] = []
            async for row in cur:
                seen.append((row[0], cur.rownumber))
            self.assertEqual(
                seen, [(1, 3), (2, 3), (3, 3), (4, 6), (5, 6), (6, 6), (7, 7)]
            )
            self.assertIsNone(await cur.scroll(-2))
            self.assertEqual(await cur.fetchone(), (6, "n6"))
            await cur.scroll(0, "absolute")
            self.assertEqual(await cur.fetchone(), (1, "n1"))
            with self.assertRaises(IndexError):
                await cur.scroll(100, "absolute")
            self.assertIsNone(cur.rownumber)
        self.assertIs(cur.closed, True)
        self.assertEqual(await arun(conn, "select count(*) from pg_cursors"), [(b"0",)])

    async def test_withhold_and_autocommit(self) -> None:
        conn = await self.connect(autocommit=True)
        cur = conn.cursor("h", withhold=True)
        await cur.execute(SEVEN)
        self.assertEqual(status(conn), "IDLE")
        self.assertEqual(await cur.fetchmany(2), [(1, "n1"), (2, "n2")])
        await cur.close()
        cur = conn.cursor("c")
        with self.assertRaises(e.InternalError) as caught:
            await cur.execute(SEVEN)
        self.assertEqual(caught.exception.sqlstate, "25P01")
        await cur.close()

    async def test_what_a_server_cursor_does_not_do(self) -> None:
        conn = await self.connect()
        cur = conn.cursor("c")
        with self.assertRaises(e.NotSupportedError) as caught:
            await cur.executemany("select %s", [(1,), (2,)])
        self.assertEqual(
            str(caught.exception),
            "executemany() is not supported on a server-side cursor",
        )
        with self.assertRaises(e.ProgrammingError) as caught_fetch:
            await cur.fetchone()
        self.assertEqual(
            str(caught_fetch.exception),
            "no result available: execute a statement first",
        )
```

Acceptance: `--mode oracle test_server_cursor_async test_async_connection` and the same in server mode pass (`test_server_cursor_async` has 5 tests).

#### CPYTHON-TPC-01-XID

Files: `stuntgres/_xid.py`, `tests/test_xid.py`. Design sections: 4.1.

Step. Write `stuntgres/_xid.py` as printed: `Xid` and `need_xid` of 4.1. Pure Python: the
tests run in unit mode. The class becomes `stuntgres.Xid` with CPYTHON-TPC-02-CONNECTION,
which exports it; until then it is imported from `stuntgres._xid`.

The complete file `stuntgres/_xid.py`:

```python
"""Xid: the identifier of a two-phase transaction."""
from __future__ import annotations

import base64
import binascii
import datetime
import re
from typing import Any

from . import errors as e

MAX_FORMAT_ID = 0x7FFFFFFF
MAX_PART_BYTES = 64

_ENCODED = re.compile(r"^(\d{1,10})_([A-Za-z0-9+/]*={0,2})_([A-Za-z0-9+/]*={0,2})$")


def _b64(text: str) -> str:
    return base64.b64encode(text.encode("utf-8")).decode("ascii")


def _unb64(text: str) -> str:
    return base64.b64decode(text, validate=True).decode("utf-8")


class Xid:
    """A two-phase transaction identifier (PEP 249 TPC extension).

    A sequence of three items: format_id, gtrid, bqual. str(xid) is the
    identifier PostgreSQL knows the transaction by. An identifier that was
    not made by xid() has format_id None, bqual None and the whole text as
    gtrid. tpc_recover() also fills prepared, owner and database.
    """

    __module__ = "stuntgres"
    __slots__ = ("format_id", "gtrid", "bqual", "prepared", "owner", "database")

    def __init__(
        self,
        format_id: int | None,
        gtrid: str,
        bqual: str | None,
        prepared: datetime.datetime | None = None,
        owner: str | None = None,
        database: str | None = None,
    ) -> None:
        self.format_id = format_id
        self.gtrid = gtrid
        self.bqual = bqual
        self.prepared = prepared
        self.owner = owner
        self.database = database

    @classmethod
    def from_parts(cls, format_id: int, gtrid: str, bqual: str) -> Xid:
        """Make an identifier from the three parts of the XA specification."""
        if isinstance(format_id, bool) or not isinstance(format_id, int):
            raise TypeError("format_id must be an int")
        if not 0 <= format_id <= MAX_FORMAT_ID:
            raise ValueError("format_id must be a non-negative 32-bit integer")
        for label, part in (("gtrid", gtrid), ("bqual", bqual)):
            if not isinstance(part, str):
                raise TypeError(f"{label} must be a str")
            if len(part.encode("utf-8")) > MAX_PART_BYTES:
                raise ValueError(f"{label} must be at most {MAX_PART_BYTES} bytes")
        return cls(format_id, gtrid, bqual)

    @classmethod
    def from_string(cls, text: str) -> Xid:
        """Make an identifier from the text PostgreSQL stores."""
        if not isinstance(text, str):
            raise TypeError("a transaction identifier must be a str")
        match = _ENCODED.match(text)
        if match is not None:
            try:
                format_id = int(match.group(1))
                gtrid = _unb64(match.group(2))
                bqual = _unb64(match.group(3))
            except (binascii.Error, UnicodeDecodeError):
                pass
            else:
                if format_id <= MAX_FORMAT_ID:
                    return cls(format_id, gtrid, bqual)
        return cls(None, text, None)

    def __str__(self) -> str:
        if self.format_id is None or self.bqual is None:
            return self.gtrid
        return f"{self.format_id}_{_b64(self.gtrid)}_{_b64(self.bqual)}"

    def __repr__(self) -> str:
        return f"Xid({self.format_id!r}, {self.gtrid!r}, {self.bqual!r})"

    def _parts(self) -> tuple[Any, ...]:
        return (self.format_id, self.gtrid, self.bqual)

    def __len__(self) -> int:
        return 3

    def __getitem__(self, index: int) -> Any:
        return self._parts()[index]

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, Xid):
            return NotImplemented
        return self._parts() == other._parts()

    def __hash__(self) -> int:
        return hash(self._parts())


def need_xid(value: Any) -> Xid:
    """Return a Xid for what a tpc method was given: a Xid or a str."""
    if isinstance(value, Xid):
        return value
    if isinstance(value, str):
        return Xid.from_string(value)
    raise e.ProgrammingError(
        f"a transaction identifier must be a Xid or a str, got {type(value).__name__}"
    )
```

Tests to write first. Before the code exists: `tests/test_xid.py` fails to import: `No module named 'stuntgres._xid'`.

The complete file `tests/test_xid.py` (7 tests):

```python
"""Tests of Xid."""
from __future__ import annotations

import datetime
import unittest

from stuntgres import errors as e
from stuntgres._xid import MAX_FORMAT_ID, MAX_PART_BYTES, Xid, need_xid


class XidTest(unittest.TestCase):
    def test_from_parts(self) -> None:
        xid = Xid.from_parts(42, "exp3-a", "branch")
        self.assertEqual(
            (xid.format_id, xid.gtrid, xid.bqual), (42, "exp3-a", "branch")
        )
        self.assertEqual((xid.prepared, xid.owner, xid.database), (None, None, None))
        self.assertEqual(str(xid), "42_ZXhwMy1h_YnJhbmNo")
        self.assertEqual(repr(xid), "Xid(42, 'exp3-a', 'branch')")
        self.assertEqual(Xid.__module__, "stuntgres")
        self.assertEqual((MAX_FORMAT_ID, MAX_PART_BYTES), (2147483647, 64))

    def test_encoding(self) -> None:
        cases = [
            ((0, "", ""), "0__"),
            ((1, "a", "b"), "1_YQ==_Yg=="),
            ((2147483647, "ab", "abc"), "2147483647_YWI=_YWJj"),
            ((7, "caf\u00e9 \u20ac", "x_y"), "7_Y2Fmw6kg4oKs_eF95"),
            ((5, "?>?", "~~~"), "5_Pz4/_fn5+"),
        ]
        for parts, text in cases:
            xid = Xid.from_parts(*parts)
            self.assertEqual(str(xid), text)
            back = Xid.from_string(text)
            self.assertEqual(tuple(back), parts)
            self.assertEqual(back, xid)
        longest = Xid.from_parts(2147483647, "g" * 64, "b" * 64)
        self.assertEqual(len(str(longest)), 188)

    def test_sequence(self) -> None:
        xid = Xid.from_parts(42, "g", "b")
        self.assertEqual(len(xid), 3)
        self.assertEqual((xid[0], xid[1], xid[2], xid[-1]), (42, "g", "b", "b"))
        self.assertEqual(list(xid), [42, "g", "b"])
        with self.assertRaises(IndexError):
            xid[3]
        format_id, gtrid, bqual = xid
        self.assertEqual((format_id, gtrid, bqual), (42, "g", "b"))

    def test_equality(self) -> None:
        one = Xid.from_parts(42, "g", "b")
        two = Xid(42, "g", "b", datetime.datetime(2024, 1, 1), "alice", "app")
        self.assertEqual(one, two)
        self.assertEqual(hash(one), hash(two))
        self.assertNotEqual(one, Xid.from_parts(42, "g", "c"))
        self.assertNotEqual(one, (42, "g", "b"))
        self.assertEqual(len({one, two}), 1)

    def test_from_parts_checks(self) -> None:
        for args, error, text in [
            ((-1, "g", "b"), ValueError,
             "format_id must be a non-negative 32-bit integer"),
            ((2147483648, "g", "b"), ValueError,
             "format_id must be a non-negative 32-bit integer"),
            (("1", "g", "b"), TypeError, "format_id must be an int"),
            ((True, "g", "b"), TypeError, "format_id must be an int"),
            ((1, b"g", "b"), TypeError, "gtrid must be a str"),
            ((1, "g", None), TypeError, "bqual must be a str"),
            ((1, "g" * 65, "b"), ValueError, "gtrid must be at most 64 bytes"),
            ((1, "g", "\u20ac" * 22), ValueError, "bqual must be at most 64 bytes"),
        ]:
            with self.assertRaises(error) as caught:
                Xid.from_parts(*args)  # type: ignore[arg-type]
            self.assertEqual(str(caught.exception), text)
        self.assertEqual(Xid.from_parts(1, "g", "\u20ac" * 21).bqual, "\u20ac" * 21)

    def test_from_string_of_a_foreign_identifier(self) -> None:
        for text in [
            "my-transaction", "", "42_ZXhwMy1h", "42_ZXhwMy1h_YnJhbmNo_x",
            "x_ZXhwMy1h_YnJhbmNo", "42_ZXhw*y1h_YnJhbmNo", "42_ZXhwMy1_YnJhbmNo",
            "42_/w==_YnJhbmNo", "2147483648_YQ==_Yg==", "12345678901_YQ==_Yg==",
            "-1_YQ==_Yg==",
        ]:
            xid = Xid.from_string(text)
            self.assertEqual(tuple(xid), (None, text, None), text)
            self.assertEqual(str(xid), text)
        self.assertEqual(repr(Xid.from_string("it's")), "Xid(None, \"it's\", None)")
        with self.assertRaises(TypeError) as caught:
            Xid.from_string(b"x")  # type: ignore[arg-type]
        self.assertEqual(
            str(caught.exception), "a transaction identifier must be a str"
        )

    def test_need_xid(self) -> None:
        xid = Xid.from_parts(1, "a", "b")
        self.assertIs(need_xid(xid), xid)
        self.assertEqual(tuple(need_xid("1_YQ==_Yg==")), (1, "a", "b"))
        self.assertEqual(tuple(need_xid("plain")), (None, "plain", None))
        with self.assertRaises(e.ProgrammingError) as caught:
            need_xid(5)
        self.assertEqual(
            str(caught.exception),
            "a transaction identifier must be a Xid or a str, got int",
        )
```

Acceptance: `--mode unit test_xid` runs 7 tests and passes.

#### CPYTHON-TPC-02-CONNECTION

Files: `stuntgres/_conn_tpc.py` (replaces the stub), `tests/test_tpc.py`. Design sections: 4.2, 4.3, 4.4.

Step. Replace the stub `stuntgres/_conn_tpc.py` by the file printed: `TpcBaseMixin` (the
generators of 4.4), `TpcMixin`, `AsyncTpcMixin`, and `EXPORTS = ("Xid",)`.

The tests use identifiers that no other test uses (`support.unique_name("tpc")` as the
gtrid) and filter `tpc_recover()` by it, because prepared transactions are visible
across the databases of a cluster. Every test finishes the transactions it prepares.

`test_prepare_of_a_failed_transaction` needs CPYTHON-ENGINE-07-PREPARE-TRANSACTION-TAG
in server mode (a prerequisite). Oracle mode needs `max_prepared_transactions` above 0,
which the oracle backend sets (PQ 9.4).

The complete file `stuntgres/_conn_tpc.py`:

```python
"""Two-phase commit: the PEP 249 TPC extension of a connection."""
from __future__ import annotations

from typing import Any

from . import errors as e
from ._gentypes import PQGen
from ._transformer import Transformer
from ._xid import Xid, need_xid
from .pq import TransactionStatus

EXPORTS: tuple[str, ...] = ("Xid",)

_RECOVER = b"SELECT gid, prepared, owner, database FROM pg_catalog.pg_prepared_xacts"


def _literal(conn: Any, xid: Xid) -> bytes:
    """The identifier as an SQL string literal."""
    from .pq import Escaping

    return Escaping(conn.pgconn).escape_literal(str(xid).encode(conn._codec))


def _need_idle(conn: Any, method: str) -> None:
    status = conn.pgconn.transaction_status
    if status != TransactionStatus.IDLE:
        name = TransactionStatus(status).name
        raise e.ProgrammingError(
            f"{method} cannot be used inside a transaction"
            f" (transaction status {name})"
        )


class TpcBaseMixin:
    """Members of BaseConnection: the generators of two-phase commit."""

    def xid(self: Any, format_id: int, gtrid: str, bqual: str) -> Xid:
        """Return a transaction identifier for tpc_begin()."""
        return Xid.from_parts(format_id, gtrid, bqual)

    def _tpc_begin_gen(self: Any, xid: Xid | str) -> PQGen[None]:
        self._check_open()
        xid = need_xid(xid)
        if self._tpc is not None:
            raise e.ProgrammingError(
                "tpc_begin() cannot be used during a two-phase transaction"
            )
        if self._autocommit:
            raise e.ProgrammingError("tpc_begin() cannot be used in autocommit mode")
        _need_idle(self, "tpc_begin()")
        result = yield from self._exec_command(self._begin_statement())
        result.clear()
        self._tpc = (xid, False)

    def _tpc_prepare_gen(self: Any) -> PQGen[None]:
        self._check_open()
        if self._tpc is None:
            raise e.ProgrammingError("tpc_prepare() must follow tpc_begin()")
        xid, prepared = self._tpc
        if prepared:
            raise e.ProgrammingError(
                "tpc_prepare() cannot be used twice in a two-phase transaction"
            )
        command = b"PREPARE TRANSACTION " + _literal(self, xid)
        result = yield from self._exec_command(command)
        tag = result.command_status
        result.clear()
        if tag != b"PREPARE TRANSACTION":
            self._tpc = None
            raise e.OperationalError(
                "the transaction was rolled back instead of prepared:"
                " it had failed or was not open"
            )
        self._tpc = (xid, True)

    def _tpc_finish_gen(self: Any, action: str, xid: Xid | str | None) -> PQGen[None]:
        """tpc_commit() and tpc_rollback(); `action` is COMMIT or ROLLBACK."""
        self._check_open()
        method = f"tpc_{action.lower()}()"
        word = action.encode("ascii")
        if xid is None:
            if self._tpc is None:
                raise e.ProgrammingError(
                    f"{method} without an xid must follow tpc_begin()"
                )
            current, prepared = self._tpc
            try:
                if prepared:
                    command = word + b" PREPARED " + _literal(self, current)
                    result = yield from self._exec_command(command)
                    result.clear()
                elif self.pgconn.transaction_status != TransactionStatus.IDLE:
                    result = yield from self._exec_command(word)
                    result.clear()
            finally:
                self._tpc = None
            return
        if self._tpc is not None:
            raise e.ProgrammingError(
                f"{method} with an xid cannot be used during a two-phase transaction"
            )
        xid = need_xid(xid)
        _need_idle(self, f"{method} with an xid")
        command = word + b" PREPARED " + _literal(self, xid)
        result = yield from self._exec_command(command)
        result.clear()

    def _tpc_recover_gen(self: Any) -> PQGen["list[Xid]"]:
        self._check_open()
        result = yield from self._exec_command(_RECOVER)
        tx = Transformer(self)
        tx.set_pgresult(result)
        try:
            rows = tx.load_rows(0, result.ntuples)
        finally:
            tx.set_pgresult(None)
            result.clear()
        found = []
        for gid, prepared, owner, database in rows:
            xid = Xid.from_string(gid)
            found.append(
                Xid(xid.format_id, xid.gtrid, xid.bqual, prepared, owner, database)
            )
        return found


class TpcMixin:
    """Members of Connection: the tpc_ methods (blocking)."""

    def tpc_begin(self: Any, xid: Xid | str) -> None:
        """Begin a two-phase transaction with the given identifier."""
        with self.lock:
            self.wait(self._tpc_begin_gen(xid))

    def tpc_prepare(self: Any) -> None:
        """First phase: prepare the transaction begun with tpc_begin()."""
        with self.lock:
            self.wait(self._tpc_prepare_gen())

    def tpc_commit(self: Any, xid: Xid | str | None = None) -> None:
        """Commit the two-phase transaction, or a recovered one given by xid."""
        with self.lock:
            self.wait(self._tpc_finish_gen("COMMIT", xid))

    def tpc_rollback(self: Any, xid: Xid | str | None = None) -> None:
        """Roll back the two-phase transaction, or a recovered one given by xid."""
        with self.lock:
            self.wait(self._tpc_finish_gen("ROLLBACK", xid))

    def tpc_recover(self: Any) -> list[Xid]:
        """Return the prepared transactions the server holds, in every database."""
        with self.lock:
            found: list[Xid] = self.wait(self._tpc_recover_gen())
        return found


class AsyncTpcMixin:
    """Members of AsyncConnection: the tpc_ methods (coroutines)."""

    async def tpc_begin(self: Any, xid: Xid | str) -> None:
        """Begin a two-phase transaction with the given identifier."""
        async with self.lock:
            await self.wait(self._tpc_begin_gen(xid))

    async def tpc_prepare(self: Any) -> None:
        """First phase: prepare the transaction begun with tpc_begin()."""
        async with self.lock:
            await self.wait(self._tpc_prepare_gen())

    async def tpc_commit(self: Any, xid: Xid | str | None = None) -> None:
        """Commit the two-phase transaction, or a recovered one given by xid."""
        async with self.lock:
            await self.wait(self._tpc_finish_gen("COMMIT", xid))

    async def tpc_rollback(self: Any, xid: Xid | str | None = None) -> None:
        """Roll back the two-phase transaction, or a recovered one given by xid."""
        async with self.lock:
            await self.wait(self._tpc_finish_gen("ROLLBACK", xid))

    async def tpc_recover(self: Any) -> list[Xid]:
        """Return the prepared transactions the server holds, in every database."""
        async with self.lock:
            found: list[Xid] = await self.wait(self._tpc_recover_gen())
        return found
```

Tests to write first. Before the code exists: `tests/test_tpc.py` fails: `AttributeError: 'Connection' object has no attribute 'xid'` (and `module 'stuntgres' has no attribute 'Xid'`).

The complete file `tests/test_tpc.py` (13 tests):

```python
"""Tests of the two-phase commit methods of Connection and AsyncConnection."""
from __future__ import annotations

import datetime
from typing import Any

import stuntgres
from stuntgres import errors as e
from stuntgres._xid import Xid
from tests import support
from tests.dbapi_util import ConnTestCase, run, status
from tests.dbapi_util_async import AsyncConnTestCase, arun


class TpcTest(ConnTestCase):
    def setUp(self) -> None:
        super().setUp()
        self.conn = self.connect()
        run(self.conn, "create table t (a int4)")
        self.conn.commit()
        self.dbname = support.conninfo_dict(self.conninfo)["dbname"]
        self.gtrid = support.unique_name("tpc")

    def xid(self, bqual: str = "b") -> Xid:
        return self.conn.xid(42, self.gtrid, bqual)

    def mine(self, conn: Any = None) -> list[Xid]:
        found = (conn or self.conn).tpc_recover()
        return [x for x in found if x.gtrid == self.gtrid or self.gtrid in x.gtrid]

    def count(self) -> bytes | None:
        other = self.connect(autocommit=True)
        try:
            return run(other, "select count(*) from t")[0][0]
        finally:
            other.close()

    def test_names(self) -> None:
        self.assertIs(stuntgres.Xid, Xid)
        xid = self.conn.xid(42, "g", "b")
        self.assertIs(type(xid), Xid)
        self.assertEqual(tuple(xid), (42, "g", "b"))
        with self.assertRaises(ValueError):
            self.conn.xid(-1, "g", "b")

    def test_prepare_and_commit(self) -> None:
        conn = self.conn
        xid = self.xid()
        self.assertIsNone(conn.tpc_begin(xid))
        self.assertEqual(status(conn), "INTRANS")
        self.assertEqual(conn._tpc, (xid, False))
        run(conn, "insert into t values (1)")
        self.assertIsNone(conn.tpc_prepare())
        self.assertEqual(status(conn), "IDLE")
        self.assertEqual(conn._tpc, (xid, True))
        self.assertEqual(self.count(), b"0")
        found = self.mine()
        self.assertEqual(found, [xid])
        self.assertEqual(tuple(found[0]), (42, self.gtrid, "b"))
        self.assertIsInstance(found[0].prepared, datetime.datetime)
        assert found[0].prepared is not None
        self.assertIsNotNone(found[0].prepared.tzinfo)
        self.assertEqual(found[0].owner, support.conninfo_dict(self.conninfo)["user"])
        self.assertEqual(found[0].database, self.dbname)
        self.assertEqual(status(conn), "IDLE")
        self.assertIsNone(conn.tpc_commit())
        self.assertIsNone(conn._tpc)
        self.assertEqual(status(conn), "IDLE")
        self.assertEqual(self.count(), b"1")
        self.assertEqual(self.mine(), [])

    def test_prepare_and_rollback(self) -> None:
        conn = self.conn
        conn.tpc_begin(self.xid())
        run(conn, "insert into t values (1)")
        conn.tpc_prepare()
        self.assertIsNone(conn.tpc_rollback())
        self.assertIsNone(conn._tpc)
        self.assertEqual(self.count(), b"0")
        self.assertEqual(self.mine(), [])

    def test_one_phase(self) -> None:
        conn = self.conn
        conn.tpc_begin(self.xid())
        run(conn, "insert into t values (1)")
        conn.tpc_commit()
        self.assertEqual((status(conn), conn._tpc), ("IDLE", None))
        self.assertEqual(self.count(), b"1")
        conn.tpc_begin(self.xid())
        run(conn, "insert into t values (2)")
        conn.tpc_rollback()
        self.assertEqual((status(conn), conn._tpc), ("IDLE", None))
        self.assertEqual(self.count(), b"1")
        self.assertEqual(self.mine(), [])

    def test_recover_and_finish_from_another_connection(self) -> None:
        conn = self.conn
        for bqual in ("one", "two"):
            conn.tpc_begin(self.xid(bqual))
            run(conn, "insert into t values (1)")
            conn.tpc_prepare()
            conn._tpc = None  # as if the connection had been lost after the prepare
        other = self.connect()
        found = sorted(self.mine(other), key=lambda x: str(x.bqual))
        self.assertEqual([x.bqual for x in found], ["one", "two"])
        self.assertEqual(status(other), "IDLE")
        self.assertIsNone(other.tpc_commit(found[0]))
        self.assertIsNone(other.tpc_rollback(str(found[1])))
        self.assertEqual(status(other), "IDLE")
        self.assertEqual(self.count(), b"1")
        self.assertEqual(self.mine(other), [])

    def test_an_identifier_that_is_plain_text(self) -> None:
        conn = self.conn
        name = self.gtrid + " it's \\ odd"
        conn.tpc_begin(name)
        self.assertEqual(tuple(conn._tpc[0]), (None, name, None))
        conn.tpc_prepare()
        found = self.mine()
        self.assertEqual([tuple(x) for x in found], [(None, name, None)])
        conn.tpc_rollback()
        self.assertEqual(self.mine(), [])

    def test_state_rules(self) -> None:
        conn = self.conn
        xid = self.xid()
        for call, text in [
            (conn.tpc_prepare, "tpc_prepare() must follow tpc_begin()"),
            (conn.tpc_commit, "tpc_commit() without an xid must follow tpc_begin()"),
            (conn.tpc_rollback,
             "tpc_rollback() without an xid must follow tpc_begin()"),
        ]:
            with self.assertRaises(e.ProgrammingError) as caught:
                call()
            self.assertEqual(str(caught.exception), text)
        run(conn, "select 1")
        with self.assertRaises(e.ProgrammingError) as caught:
            conn.tpc_begin(xid)
        self.assertEqual(
            str(caught.exception),
            "tpc_begin() cannot be used inside a transaction"
            " (transaction status INTRANS)",
        )
        with self.assertRaises(e.ProgrammingError) as caught:
            conn.tpc_commit(xid)
        self.assertEqual(
            str(caught.exception),
            "tpc_commit() with an xid cannot be used inside a transaction"
            " (transaction status INTRANS)",
        )
        conn.rollback()
        conn.autocommit = True
        with self.assertRaises(e.ProgrammingError) as caught:
            conn.tpc_begin(xid)
        self.assertEqual(
            str(caught.exception), "tpc_begin() cannot be used in autocommit mode"
        )
        conn.autocommit = False
        with self.assertRaises(e.ProgrammingError) as caught:
            conn.tpc_begin(5)
        self.assertEqual(
            str(caught.exception),
            "a transaction identifier must be a Xid or a str, got int",
        )
        conn.tpc_begin(xid)
        for call, text in [
            (lambda: conn.tpc_begin(xid),
             "tpc_begin() cannot be used during a two-phase transaction"),
            (conn.commit, "commit() cannot be used during a two-phase transaction"),
            (conn.rollback, "rollback() cannot be used during a two-phase transaction"),
            (lambda: conn.transaction().__enter__(),
             "transaction() cannot be used during a two-phase transaction"),
            (lambda: conn.tpc_commit(xid),
             "tpc_commit() with an xid cannot be used during a two-phase transaction"),
            (lambda: conn.tpc_rollback("x"),
             "tpc_rollback() with an xid cannot be used during a two-phase"
             " transaction"),
        ]:
            with self.assertRaises(e.ProgrammingError) as caught:
                call()
            self.assertEqual(str(caught.exception), text)
        conn.tpc_prepare()
        with self.assertRaises(e.ProgrammingError) as caught:
            conn.tpc_prepare()
        self.assertEqual(
            str(caught.exception),
            "tpc_prepare() cannot be used twice in a two-phase transaction",
        )
        conn.tpc_rollback()
        conn.commit()
        conn.close()
        with self.assertRaises(e.OperationalError) as caught_closed:
            conn.tpc_recover()
        self.assertEqual(str(caught_closed.exception), "the connection is closed")

    def test_prepare_of_a_failed_transaction(self) -> None:
        conn = self.conn
        conn.tpc_begin(self.xid())
        with self.assertRaises(e.DataError):
            run(conn, "select 1/0")
        self.assertEqual(status(conn), "INERROR")
        with self.assertRaises(e.OperationalError) as caught:
            conn.tpc_prepare()
        self.assertEqual(
            str(caught.exception),
            "the transaction was rolled back instead of prepared: it had failed or"
            " was not open",
        )
        self.assertIsNone(caught.exception.sqlstate)
        self.assertEqual((status(conn), conn._tpc), ("IDLE", None))
        self.assertEqual(self.mine(), [])
        conn.commit()

    def test_rollback_of_a_failed_transaction(self) -> None:
        conn = self.conn
        conn.tpc_begin(self.xid())
        with self.assertRaises(e.DataError):
            run(conn, "select 1/0")
        conn.tpc_rollback()
        self.assertEqual((status(conn), conn._tpc), ("IDLE", None))

    def test_server_errors(self) -> None:
        conn = self.conn
        xid = self.xid()
        conn.tpc_begin(xid)
        conn.tpc_prepare()
        other = self.connect()
        other.tpc_begin(xid)
        with self.assertRaises(e.ProgrammingError) as caught:
            other.tpc_prepare()
        self.assertEqual(caught.exception.sqlstate, "42710")
        self.assertEqual(
            str(caught.exception),
            f'transaction identifier "{xid}" is already in use',
        )
        self.assertEqual(status(other), "IDLE")
        self.assertEqual(other._tpc, (xid, False))
        other.tpc_rollback()
        self.assertIsNone(other._tpc)
        other.tpc_rollback(xid)
        with self.assertRaises(e.ProgrammingError) as caught:
            conn.tpc_commit()
        self.assertEqual(caught.exception.sqlstate, "42704")
        self.assertEqual(
            str(caught.exception),
            f'prepared transaction with identifier "{xid}" does not exist',
        )
        self.assertIsNone(conn._tpc)
        with self.assertRaises(e.ProgrammingError) as caught:
            conn.tpc_rollback("no such transaction")
        self.assertEqual(caught.exception.sqlstate, "42704")
        self.assertEqual(status(conn), "IDLE")

    def test_transaction_characteristics_apply(self) -> None:
        conn = self.conn
        conn.read_only = True
        conn.tpc_begin(self.xid())
        with self.assertRaises(e.InternalError) as caught:
            run(conn, "insert into t values (1)")
        self.assertEqual(caught.exception.sqlstate, "25006")
        conn.tpc_rollback()


class AsyncTpcTest(AsyncConnTestCase):
    async def test_two_phases(self) -> None:
        conn = await self.connect()
        await arun(conn, "create table t (a int4)")
        await conn.commit()
        gtrid = support.unique_name("tpc")
        xid = conn.xid(7, gtrid, "async")
        self.assertIsNone(await conn.tpc_begin(xid))
        await arun(conn, "insert into t values (1)")
        self.assertIsNone(await conn.tpc_prepare())
        self.assertEqual(status(conn), "IDLE")
        found = [x for x in await conn.tpc_recover() if x.gtrid == gtrid]
        self.assertEqual(found, [xid])
        self.assertIsNone(await conn.tpc_commit())
        self.assertEqual(await arun(conn, "select count(*) from t"), [(b"1",)])
        await conn.rollback()
        await conn.tpc_begin(xid)
        with self.assertRaises(e.ProgrammingError) as caught:
            await conn.commit()
        self.assertEqual(
            str(caught.exception),
            "commit() cannot be used during a two-phase transaction",
        )
        await conn.tpc_prepare()
        self.assertIsNone(await conn.tpc_rollback())
        self.assertEqual(
            [x for x in await conn.tpc_recover() if x.gtrid == gtrid], []
        )

    async def test_recovered_transaction(self) -> None:
        conn = await self.connect()
        gtrid = support.unique_name("tpc")
        await conn.tpc_begin(conn.xid(7, gtrid, "lost"))
        await conn.tpc_prepare()
        conn._tpc = None
        other = await self.connect()
        found = [x for x in await other.tpc_recover() if x.gtrid == gtrid]
        self.assertEqual([x.bqual for x in found], ["lost"])
        await other.tpc_rollback(found[0])
        with self.assertRaises(e.ProgrammingError) as caught:
            await other.tpc_commit(found[0])
        self.assertEqual(caught.exception.sqlstate, "42704")
```

Acceptance: `--mode oracle test_tpc` and `--mode server test_tpc` run 13 tests each and pass; afterwards `select count(*) from pg_prepared_xacts` on the oracle container is 0.

#### CPYTHON-CONTROL-01-API

Files: `stuntgres/_conn_control.py` (replaces the stub), `tests/test_control.py`. Design sections: 5.1, 5.2.

Step. Replace the stub `stuntgres/_conn_control.py` by the file printed: the two module
functions of 5.1, `ControlMixin` and `AsyncControlMixin` of 5.2, and
`EXPORTS = ("drop_cluster", "libspq_version")`.

The test file has classes for three situations: `ControlNamesTest` runs in every
database mode; `ControlWithoutLibspqTest` and `AsyncControlWithoutLibspqTest` run in
oracle and server mode, where every function must raise `NotSupportedError`;
`ControlTest` and `AsyncControlTest` run in native mode only. Nothing of the native
classes could be executed when this part was designed: their expectations come from
`docs/LIBSPQ.md` sections 5.1 and 6. If libspq answers differently, stop and report
(C8).

The complete file `stuntgres/_conn_control.py`:

```python
"""Cluster control: what libspq offers beyond PostgreSQL's client library.

Every function here needs libspq. With another library loaded (the test seam
of the loader) each raises NotSupportedError.
"""
from __future__ import annotations

from typing import Any, Union

from . import errors as e
from .pq import TransactionStatus

Buffer = Union[bytes, bytearray, memoryview]

EXPORTS: tuple[str, ...] = ("drop_cluster", "libspq_version")


def libspq_version() -> str:
    """Return the version of the libspq library in use, for example '0.1.0'."""
    from .pq import ext

    return ext.version().decode("ascii")


def drop_cluster(host: str = "localhost", port: int | str = 5432) -> None:
    """Forget the cluster the host and port name, with everything in it.

    Fails with OperationalError while a connection to the cluster is open.
    The next connection to the same name gets a new, empty cluster. Dropping
    a cluster that does not exist does nothing.
    """
    from .pq import ext

    ext.cluster_drop(str(host).encode("utf-8"), str(port).encode("ascii"))


def _check_idle(conn: Any, method: str) -> None:
    conn._check_open()
    status = conn.pgconn.transaction_status
    if status != TransactionStatus.IDLE:
        name = TransactionStatus(status).name
        raise e.ProgrammingError(
            f"{method}() cannot be used inside a transaction"
            f" (transaction status {name})"
        )


def _call(conn: Any, name: str, *args: Any) -> Any:
    """Run one extension function on the connection; the lock is held."""
    from .pq import ext

    try:
        return getattr(ext, name)(conn.pgconn, *args)
    finally:
        conn._note_status()


class ControlMixin:
    """Members of Connection: reset, dump and restore of its database."""

    def reset_database(self: Any) -> None:
        """Remove every persistent object and all data of the database.

        The cluster, its roles, its other databases and this connection stay.
        """
        with self.lock:
            _check_idle(self, "reset_database")
            _call(self, "reset")

    def dump(self: Any) -> bytes:
        """Return the content of the database as bytes for restore()."""
        with self.lock:
            _check_idle(self, "dump")
            data: bytes = _call(self, "dump")
        return data

    def restore(self: Any, data: Buffer) -> None:
        """Replace the content of the database with a dump."""
        with self.lock:
            _check_idle(self, "restore")
            _call(self, "restore", bytes(data))


class AsyncControlMixin:
    """Members of AsyncConnection: reset, dump and restore of its database."""

    async def reset_database(self: Any) -> None:
        """Remove every persistent object and all data of the database.

        The cluster, its roles, its other databases and this connection stay.
        """
        import asyncio

        async with self.lock:
            _check_idle(self, "reset_database")
            loop = asyncio.get_running_loop()
            await loop.run_in_executor(None, _call, self, "reset")

    async def dump(self: Any) -> bytes:
        """Return the content of the database as bytes for restore()."""
        import asyncio

        async with self.lock:
            _check_idle(self, "dump")
            loop = asyncio.get_running_loop()
            data: bytes = await loop.run_in_executor(None, _call, self, "dump")
        return data

    async def restore(self: Any, data: Buffer) -> None:
        """Replace the content of the database with a dump."""
        import asyncio

        async with self.lock:
            _check_idle(self, "restore")
            loop = asyncio.get_running_loop()
            await loop.run_in_executor(None, _call, self, "restore", bytes(data))
```

Tests to write first. Before the code exists: `tests/test_control.py` fails: `module 'stuntgres' has no attribute 'drop_cluster'`.

The complete file `tests/test_control.py` (11 tests):

```python
"""Tests of cluster control: drop_cluster, reset_database, dump, restore."""
from __future__ import annotations

import inspect
import re
from typing import Any

import stuntgres
from stuntgres import _conn_control
from stuntgres import errors as e
from tests import support
from tests.dbapi_util import ConnTestCase, run, status
from tests.dbapi_util_async import AsyncConnTestCase, arun

IN_TRANSACTION = "{}() cannot be used inside a transaction (transaction status INTRANS)"


class ControlNamesTest(ConnTestCase):
    def test_names(self) -> None:
        self.assertIs(stuntgres.drop_cluster, _conn_control.drop_cluster)
        self.assertIs(stuntgres.libspq_version, _conn_control.libspq_version)
        self.assertEqual(
            str(inspect.signature(stuntgres.drop_cluster)),
            "(host: 'str' = 'localhost', port: 'int | str' = 5432) -> 'None'",
        )
        conn = self.connect()
        for name in ("reset_database", "dump", "restore"):
            self.assertTrue(callable(getattr(conn, name)), name)

    def test_not_inside_a_transaction(self) -> None:
        conn = self.connect()
        run(conn, "select 1")
        for name, args in (("reset_database", ()), ("dump", ()), ("restore", (b"",))):
            with self.assertRaises(e.ProgrammingError) as caught:
                getattr(conn, name)(*args)
            self.assertEqual(str(caught.exception), IN_TRANSACTION.format(name))
        self.assertEqual(status(conn), "INTRANS")
        conn.close()
        with self.assertRaises(e.OperationalError) as caught_closed:
            conn.dump()
        self.assertEqual(str(caught_closed.exception), "the connection is closed")


class ControlWithoutLibspqTest(ConnTestCase):
    """With the test seam's libpq loaded the extensions do not exist."""

    modes = ("oracle", "server")

    def refused(self, symbol: str, call: Any, *args: Any) -> None:
        with self.assertRaises(e.NotSupportedError) as caught:
            call(*args)
        self.assertEqual(
            str(caught.exception), f"{symbol} is not available in the loaded library"
        )

    def test_module_functions(self) -> None:
        self.refused("PQXversion", stuntgres.libspq_version)
        self.refused("PQXclusterDrop", stuntgres.drop_cluster)
        self.refused("PQXclusterDrop", stuntgres.drop_cluster, "elsewhere", 5433)

    def test_connection_methods(self) -> None:
        conn = self.connect(autocommit=True)
        self.refused("PQXreset", conn.reset_database)
        self.refused("PQXdump", conn.dump)
        self.refused("PQXrestore", conn.restore, b"data")
        self.assertEqual(run(conn, "select 1"), [(b"1",)])
        self.assertFalse(conn.lock.locked())


class AsyncControlWithoutLibspqTest(AsyncConnTestCase):
    modes = ("oracle", "server")

    async def test_connection_methods(self) -> None:
        conn = await self.connect(autocommit=True)
        for symbol, call, args in (
            ("PQXreset", conn.reset_database, ()),
            ("PQXdump", conn.dump, ()),
            ("PQXrestore", conn.restore, (b"data",)),
        ):
            with self.assertRaises(e.NotSupportedError) as caught:
                await call(*args)
            self.assertEqual(
                str(caught.exception),
                f"{symbol} is not available in the loaded library",
            )
        self.assertEqual(await arun(conn, "select 1"), [(b"1",)])
        self.assertFalse(conn.lock.locked())
        await arun(conn, "begin")
        with self.assertRaises(e.ProgrammingError) as caught_txn:
            await conn.dump()
        self.assertEqual(str(caught_txn.exception), IN_TRANSACTION.format("dump"))


class ControlTest(ConnTestCase):
    """Against libspq: expectations from docs/LIBSPQ.md sections 5.1 and 6."""

    modes = ("native",)

    def table_count(self, conn: Any) -> bytes | None:
        return run(conn, "select count(*) from pg_class where relname = 'kept'")[0][0]

    def test_version(self) -> None:
        self.assertRegex(stuntgres.libspq_version(), r"^\d+\.\d+\.\d+$")

    def test_reset_database(self) -> None:
        conn = self.connect(autocommit=True)
        run(conn, "create table kept (a int4)")
        run(conn, "insert into kept values (1)")
        self.assertIsNone(conn.reset_database())
        self.assertEqual(self.table_count(conn), b"0")
        self.assertEqual(run(conn, "select current_user"), [(b"test",)])
        self.assertEqual(status(conn), "IDLE")

    def test_dump_and_restore(self) -> None:
        conn = self.connect(autocommit=True)
        run(conn, "create table kept (a int4, b text)")
        run(conn, "insert into kept values (1, 'one'), (2, NULL)")
        data = conn.dump()
        self.assertIsInstance(data, bytes)
        self.assertGreater(len(data), 0)
        run(conn, "drop table kept")
        self.assertIsNone(conn.restore(data))
        self.assertEqual(
            run(conn, "select a, b from kept order by a"),
            [(b"1", b"one"), (b"2", None)],
        )
        conn.reset_database()
        conn.restore(bytearray(data))
        self.assertEqual(self.table_count(conn), b"1")
        with self.assertRaises(e.OperationalError) as caught:
            conn.restore(b"this is no dump")
        self.assertTrue(str(caught.exception).startswith("restore failed: "))

    def test_drop_cluster(self) -> None:
        host = "drop-" + support.unique_name()
        conninfo = f"host={host} dbname=test user=test"
        conn = stuntgres.connect(conninfo, autocommit=True)
        run(conn, "create table kept (a int4)")
        with self.assertRaises(e.OperationalError) as caught:
            stuntgres.drop_cluster(host)
        self.assertTrue(str(caught.exception).startswith("cluster_drop failed: "))
        conn.close()
        self.assertIsNone(stuntgres.drop_cluster(host))
        self.assertIsNone(stuntgres.drop_cluster(host, 5432))
        again = stuntgres.connect(conninfo, autocommit=True)
        self.addCleanup(stuntgres.drop_cluster, host)
        self.addCleanup(again.close)
        self.assertEqual(self.table_count(again), b"0")

    def test_clusters_do_not_see_each_other(self) -> None:
        # The check the DB-API design left for native mode (prelim-takeaways 1).
        names = ["iso-" + support.unique_name() for _ in range(2)]
        conns = []
        for name in names:
            conn = stuntgres.connect(f"host={name} dbname=test user=test")
            self.addCleanup(stuntgres.drop_cluster, name)
            self.addCleanup(conn.close)
            conns.append(conn)
        same = stuntgres.connect(f"host={names[0]} dbname=test user=test")
        self.addCleanup(same.close)
        run(conns[0], "create table kept (a int4)")
        self.assertEqual(self.table_count(same), b"0")
        conns[0].commit()
        same.rollback()
        self.assertEqual(self.table_count(same), b"1")
        self.assertEqual(self.table_count(conns[1]), b"0")
        other_port = stuntgres.connect(
            f"host={names[0]} port=5433 dbname=test user=test"
        )
        self.addCleanup(stuntgres.drop_cluster, names[0], 5433)
        self.addCleanup(other_port.close)
        self.assertEqual(self.table_count(other_port), b"0")


class AsyncControlTest(AsyncConnTestCase):
    modes = ("native",)

    async def test_reset_dump_restore(self) -> None:
        conn = await self.connect(autocommit=True)
        await arun(conn, "create table kept (a int4)")
        await arun(conn, "insert into kept values (1)")
        data = await conn.dump()
        self.assertIsInstance(data, bytes)
        self.assertIsNone(await conn.reset_database())
        count = "select count(*) from pg_class where relname = 'kept'"
        self.assertEqual(await arun(conn, count), [(b"0",)])
        self.assertIsNone(await conn.restore(data))
        self.assertEqual(await arun(conn, "select a from kept"), [(b"1",)])
        self.assertFalse(conn.lock.locked())
        self.assertTrue(re.match(r"^\d+\.\d+\.\d+$", stuntgres.libspq_version()))
```

Acceptance: `--mode oracle test_control` and `--mode server test_control` run 5 tests each and pass; after `make libspq`, `--mode native test_control` runs 8 tests and passes.

#### CPYTHON-CONTROL-02-ISOLATION

Files: `stuntgres/testing.py`, `tests/test_testing.py`. Design sections: 5.3.

Step. Write `stuntgres/testing.py` as printed: `temp_cluster` of 5.3. The module is not
imported by `import stuntgres`.

`TempClusterWithoutLibspqTest` runs in oracle and server mode (the helper yields its
connection string and the drop is refused with `NotSupportedError`); `TempClusterTest`
runs in native mode only and could not be executed when this part was designed.

The complete file `stuntgres/testing.py`:

```python
"""Helpers for the tests of applications that use stuntgres."""
from __future__ import annotations

import uuid
from contextlib import contextmanager
from typing import Iterator

from . import errors as e
from ._conn_control import drop_cluster
from ._conn_core import logger
from .conninfo import make_conninfo

__all__ = ["temp_cluster"]


@contextmanager
def temp_cluster(dbname: str = "test", user: str = "test") -> Iterator[str]:
    """Yield the connection string of a new cluster; drop the cluster afterwards.

    The cluster has a name no other cluster has, so nothing else sees its
    data. Close every connection to it before the block ends: the drop fails
    with OperationalError while one is open.
    """
    host = "temp-" + uuid.uuid4().hex
    conninfo = make_conninfo(host=host, dbname=dbname, user=user)
    try:
        yield conninfo
    except BaseException:
        try:
            drop_cluster(host)
        except e.Error as exc:
            logger.warning("cluster %s was not dropped: %s", host, exc)
        raise
    else:
        drop_cluster(host)
```

Tests to write first. Before the code exists: `tests/test_testing.py` fails to import: `cannot import name 'testing' from 'stuntgres'`.

The complete file `tests/test_testing.py` (5 tests):

```python
"""Tests of stuntgres.testing.temp_cluster."""
from __future__ import annotations

import logging
import re

import stuntgres
from stuntgres import errors as e
from stuntgres import testing
from stuntgres.conninfo import conninfo_to_dict
from tests import support
from tests.dbapi_util import run

HOST = re.compile(r"^temp-[0-9a-f]{32}$")


class TempClusterWithoutLibspqTest(support.LibraryTestCase):
    modes = ("oracle", "server")

    def test_exports(self) -> None:
        self.assertEqual(testing.__all__, ["temp_cluster"])

    def test_conninfo_then_refusal(self) -> None:
        seen: list[str] = []
        with self.assertRaises(e.NotSupportedError) as caught:
            with testing.temp_cluster() as conninfo:
                seen.append(conninfo)
        self.assertEqual(
            str(caught.exception),
            "PQXclusterDrop is not available in the loaded library",
        )
        options = conninfo_to_dict(seen[0])
        self.assertRegex(options.pop("host"), HOST)
        self.assertEqual(options, {"dbname": "test", "user": "test"})

    def test_the_error_of_the_block_wins(self) -> None:
        with self.assertLogs("stuntgres", logging.WARNING) as logs:
            with self.assertRaises(ValueError) as caught:
                with testing.temp_cluster(dbname="app", user="alice") as conninfo:
                    raise ValueError(conninfo)
        options = conninfo_to_dict(str(caught.exception))
        host = options.pop("host")
        self.assertEqual(options, {"dbname": "app", "user": "alice"})
        self.assertEqual(
            [record.getMessage() for record in logs.records],
            [f"cluster {host} was not dropped: PQXclusterDrop is not available in the"
             " loaded library"],
        )


class TempClusterTest(support.LibraryTestCase):
    """Against libspq: expectations from docs/LIBSPQ.md sections 5.1 and 6."""

    modes = ("native",)

    def test_fresh_cluster_each_time(self) -> None:
        hosts = []
        for _ in range(2):
            with testing.temp_cluster() as conninfo:
                hosts.append(conninfo_to_dict(conninfo)["host"])
                conn = stuntgres.connect(conninfo, autocommit=True)
                try:
                    count = "select count(*) from pg_class where relname = 'kept'"
                    self.assertEqual(run(conn, count), [(b"0",)])
                    run(conn, "create table kept (a int4)")
                    self.assertEqual(
                        run(conn, "select current_database(), current_user"),
                        [(b"test", b"test")],
                    )
                finally:
                    conn.close()
        self.assertNotEqual(hosts[0], hosts[1])
        for host in hosts:
            self.assertRegex(host, HOST)

    def test_an_open_connection_fails_the_drop(self) -> None:
        with self.assertRaises(e.OperationalError) as caught:
            with testing.temp_cluster() as conninfo:
                conn = stuntgres.connect(conninfo)
                host = conninfo_to_dict(conninfo)["host"]
                self.addCleanup(stuntgres.drop_cluster, host)
                self.addCleanup(conn.close)
        self.assertTrue(str(caught.exception).startswith("cluster_drop failed: "))
```

Acceptance: `--mode oracle test_testing` and `--mode server test_testing` run 3 tests each and pass; after `make libspq`, `--mode native test_testing` runs 2 tests and passes.

#### CPYTHON-MAKE-01A-ELF

Files: `tools/elfneeds.py`, `tests/elf_util.py`, `tests/test_elfneeds.py`. Design sections: 6.3.

Step.
1. Write `tools/elfneeds.py` as printed: `read_needs`, `glibc_version`, `platform_tag`
   of 6.3.
2. Write `tests/elf_util.py` as printed: `make_elf` builds the bytes of a minimal
   64-bit ELF shared object with chosen machine, needed libraries and version needs, so
   that the tests have literal expectations on every host.

The tools directory is not a package: the tests put it on `sys.path` (as printed) and
import `elfneeds`. Pure Python: the tests run in unit mode. `test_a_real_library` reads
`_ctypes_test`, a small extension module that CPython ships; if the interpreter lacks
it, stop and report (C8).

The complete file `tools/elfneeds.py`:

```python
"""What a shared object needs from the system: machine, libraries, glibc version.

Reads the ELF file itself (64-bit, little-endian), so the answer does not
depend on the host that runs the build.
"""
from __future__ import annotations

import re
import struct
from typing import NamedTuple

ARCHITECTURES = {62: "x86_64", 183: "aarch64"}
# The libraries glibc itself consists of; a wheel may rely on nothing else.
SYSTEM_LIBRARIES = frozenset({
    "libc.so.6", "libpthread.so.0", "libdl.so.2", "libm.so.6", "librt.so.1",
    "libresolv.so.2", "libutil.so.1", "ld-linux-x86-64.so.2",
    "ld-linux-aarch64.so.1",
})
# The oldest glibc a wheel is tagged for (the manylinux2014 baseline).
GLIBC_FLOOR = (2, 17)
# glibc version that understands DT_RELR (the marker GLIBC_ABI_DT_RELR).
GLIBC_RELR = (2, 36)

_SHT_DYNAMIC = 6
_SHT_GNU_VERNEED = 0x6FFFFFFE
_DT_NEEDED = 1
_GLIBC = re.compile(r"^GLIBC_(\d+)\.(\d+)(?:\.\d+)?$")


class ElfError(Exception):
    """The file is not a shared object this tool can describe."""


class Needs(NamedTuple):
    """The requirements of one shared object."""

    machine: str                    # "x86_64" or "aarch64"
    libraries: tuple[str, ...]      # DT_NEEDED entries, in file order
    versions: tuple[str, ...]       # symbol versions it needs, sorted, unique


def _cstring(data: bytes, start: int) -> str:
    end = data.index(b"\x00", start)
    return data[start:end].decode("ascii")


def read_needs(path: str) -> Needs:
    """Read machine, needed libraries and needed symbol versions of an ELF file."""
    with open(path, "rb") as fh:
        data = fh.read()
    if data[:4] != b"\x7fELF":
        raise ElfError(f"{path}: not an ELF file")
    if data[4:6] != b"\x02\x01":
        raise ElfError(f"{path}: not a 64-bit little-endian ELF file")
    e_type, e_machine = struct.unpack_from("<HH", data, 16)
    if e_type != 3:
        raise ElfError(f"{path}: not a shared object (ELF type {e_type})")
    machine = ARCHITECTURES.get(e_machine)
    if machine is None:
        raise ElfError(f"{path}: unsupported machine {e_machine}")
    (shoff,) = struct.unpack_from("<Q", data, 40)
    shentsize, shnum = struct.unpack_from("<HH", data, 58)
    sections = []
    for index in range(shnum):
        base = shoff + index * shentsize
        sh_type = struct.unpack_from("<I", data, base + 4)[0]
        offset, size, link, info = struct.unpack_from("<QQII", data, base + 24)
        sections.append((sh_type, offset, size, link, info))
    libraries: list[str] = []
    versions: set[str] = set()
    for sh_type, offset, size, link, info in sections:
        if sh_type == _SHT_DYNAMIC:
            strings = sections[link][1]
            for entry in range(offset, offset + size, 16):
                tag, value = struct.unpack_from("<qQ", data, entry)
                if tag == _DT_NEEDED:
                    libraries.append(_cstring(data, strings + value))
        elif sh_type == _SHT_GNU_VERNEED:
            strings = sections[link][1]
            need = offset
            for _ in range(info):
                _ver, count, _file, aux, next_need = struct.unpack_from(
                    "<HHIII", data, need
                )
                item = need + aux
                for _ in range(count):
                    _hash, _flags, _other, name, next_item = struct.unpack_from(
                        "<IHHII", data, item
                    )
                    versions.add(_cstring(data, strings + name))
                    item += next_item
                need += next_need
    return Needs(machine, tuple(libraries), tuple(sorted(versions)))


def glibc_version(needs: Needs) -> tuple[int, int]:
    """The oldest glibc that can load the object, by its symbol versions."""
    found = GLIBC_FLOOR
    for name in needs.versions:
        if name == "GLIBC_PRIVATE":
            raise ElfError("the library uses private glibc symbols (GLIBC_PRIVATE)")
        if name == "GLIBC_ABI_DT_RELR":
            found = max(found, GLIBC_RELR)
            continue
        match = _GLIBC.match(name)
        if match is not None:
            found = max(found, (int(match.group(1)), int(match.group(2))))
    return found


def platform_tag(path: str) -> str:
    """The wheel platform tag of a shared object: manylinux_<major>_<minor>_<arch>."""
    needs = read_needs(path)
    foreign = [name for name in needs.libraries if name not in SYSTEM_LIBRARIES]
    if foreign:
        raise ElfError(
            f"{path}: needs {', '.join(foreign)}; a wheel may rely only on glibc"
        )
    major, minor = glibc_version(needs)
    return f"manylinux_{major}_{minor}_{needs.machine}"
```

The complete file `tests/elf_util.py`:

```python
"""A minimal ELF shared object for the tests of the wheel tools."""
from __future__ import annotations

import struct
from typing import Mapping, Sequence


def make_elf(
    machine: int = 62,
    needed: Sequence[str] = ("libc.so.6",),
    versions: Mapping[str, Sequence[str]] | None = None,
    elf_type: int = 3,
) -> bytes:
    """Return the bytes of a 64-bit little-endian ELF file.

    It has a string table, a dynamic section with one DT_NEEDED entry per
    name in `needed`, and a version-needs section built from `versions`
    (library name -> version names). Nothing in it can be executed.
    """
    if versions is None:
        versions = {"libc.so.6": ("GLIBC_2.2.5", "GLIBC_2.34")}
    strings = bytearray(b"\x00")
    offsets: dict[str, int] = {}

    def name_offset(name: str) -> int:
        if name not in offsets:
            offsets[name] = len(strings)
            strings.extend(name.encode("ascii") + b"\x00")
        return offsets[name]

    dynamic = b"".join(struct.pack("<qQ", 1, name_offset(n)) for n in needed)
    dynamic += struct.pack("<qQ", 0, 0)
    verneed = b""
    files = list(versions.items())
    for index, (library, names) in enumerate(files):
        size = 16 + 16 * len(names)
        verneed += struct.pack(
            "<HHIII", 1, len(names), name_offset(library), 16,
            0 if index == len(files) - 1 else size,
        )
        for position, version in enumerate(names):
            verneed += struct.pack(
                "<IHHII", 0, 0, position + 2, name_offset(version),
                0 if position == len(names) - 1 else 16,
            )
    body = bytes(strings)
    strtab_at = 64
    dynamic_at = strtab_at + len(body)
    verneed_at = dynamic_at + len(dynamic)
    shoff = verneed_at + len(verneed)

    def section(kind: int, offset: int, size: int, link: int, info: int) -> bytes:
        return struct.pack("<IIQQQQIIQQ", 0, kind, 0, 0, offset, size, link, info, 1, 0)

    header = b"\x7fELF\x02\x01\x01" + b"\x00" * 9
    header += struct.pack(
        "<HHIQQQIHHHHHH", elf_type, machine, 1, 0, 0, shoff, 0, 64, 0, 0, 64, 4, 0
    )
    table = (
        section(0, 0, 0, 0, 0)
        + section(3, strtab_at, len(body), 0, 0)
        + section(6, dynamic_at, len(dynamic), 1, 0)
        + section(0x6FFFFFFE, verneed_at, len(verneed), 1, len(files))
    )
    return header + body + dynamic + verneed + table
```

Tests to write first. Before the code exists: `tests/test_elfneeds.py` fails to import: `No module named 'tests.elf_util'`, then `No module named 'elfneeds'`.

The complete file `tests/test_elfneeds.py` (5 tests):

```python
"""Tests of tools/elfneeds.py: what a shared object needs."""
from __future__ import annotations

import os
import sys
import tempfile
import unittest
from typing import Any

from tests.elf_util import make_elf

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, os.path.join(ROOT, "tools"))

import elfneeds  # noqa: E402


class ElfNeedsTest(unittest.TestCase):
    def write(self, data: bytes) -> str:
        handle, path = tempfile.mkstemp(suffix=".so")
        self.addCleanup(os.remove, path)
        with os.fdopen(handle, "wb") as fh:
            fh.write(data)
        return path

    def tag(self, **kwargs: Any) -> str:
        return elfneeds.platform_tag(self.write(make_elf(**kwargs)))

    def refused(self, data: bytes, text: str) -> None:
        path = self.write(data)
        with self.assertRaises(elfneeds.ElfError) as caught:
            elfneeds.platform_tag(path)
        self.assertEqual(str(caught.exception), text.format(path=path))

    def test_read_needs(self) -> None:
        path = self.write(make_elf(
            needed=("libm.so.6", "libc.so.6"),
            versions={
                "libm.so.6": ("GLIBC_2.29",),
                "libc.so.6": ("GLIBC_2.34", "GLIBC_2.2.5", "GLIBC_2.34"),
            },
        ))
        self.assertEqual(
            elfneeds.read_needs(path),
            elfneeds.Needs(
                "x86_64", ("libm.so.6", "libc.so.6"),
                ("GLIBC_2.2.5", "GLIBC_2.29", "GLIBC_2.34"),
            ),
        )

    def test_constants(self) -> None:
        self.assertEqual(elfneeds.ARCHITECTURES, {62: "x86_64", 183: "aarch64"})
        self.assertEqual(elfneeds.GLIBC_FLOOR, (2, 17))
        self.assertEqual(elfneeds.GLIBC_RELR, (2, 36))
        self.assertEqual(
            sorted(elfneeds.SYSTEM_LIBRARIES),
            [
                "ld-linux-aarch64.so.1", "ld-linux-x86-64.so.2", "libc.so.6",
                "libdl.so.2", "libm.so.6", "libpthread.so.0", "libresolv.so.2",
                "librt.so.1", "libutil.so.1",
            ],
        )

    def test_platform_tag(self) -> None:
        self.assertEqual(self.tag(), "manylinux_2_34_x86_64")
        self.assertEqual(self.tag(machine=183), "manylinux_2_34_aarch64")
        cases = [
            (("GLIBC_2.2.5", "GLIBC_2.3.4"), "manylinux_2_17_x86_64"),
            (("GLIBC_2.17",), "manylinux_2_17_x86_64"),
            (("GLIBC_2.18",), "manylinux_2_18_x86_64"),
            (("GLIBC_2.4", "GLIBC_2.32", "GLIBC_2.3.2", "GLIBC_2.34"),
             "manylinux_2_34_x86_64"),
            (("GLIBC_2.9", "GLIBC_2.10"), "manylinux_2_17_x86_64"),
            (("GLIBC_2.28", "GLIBC_ABI_DT_RELR"), "manylinux_2_36_x86_64"),
            (("GLIBC_2.38", "GLIBC_ABI_DT_RELR"), "manylinux_2_38_x86_64"),
            (("GLIBC_3.1",), "manylinux_3_1_x86_64"),
            ((), "manylinux_2_17_x86_64"),
        ]
        for names, tag in cases:
            self.assertEqual(self.tag(versions={"libc.so.6": names}), tag, names)
        self.assertEqual(
            self.tag(
                needed=("libpthread.so.0", "libc.so.6"),
                versions={
                    "libpthread.so.0": ("GLIBC_2.30",), "libc.so.6": ("GLIBC_2.25",),
                },
            ),
            "manylinux_2_30_x86_64",
        )
        self.assertEqual(self.tag(needed=(), versions={}), "manylinux_2_17_x86_64")

    def test_refusals(self) -> None:
        self.refused(b"not an elf file", "{path}: not an ELF file")
        self.refused(
            b"\x7fELF\x01\x01" + b"\x00" * 60,
            "{path}: not a 64-bit little-endian ELF file",
        )
        self.refused(
            make_elf(elf_type=2), "{path}: not a shared object (ELF type 2)"
        )
        self.refused(make_elf(machine=3), "{path}: unsupported machine 3")
        self.refused(
            make_elf(needed=("libssl.so.3", "libc.so.6", "libz.so.1")),
            "{path}: needs libssl.so.3, libz.so.1; a wheel may rely only on glibc",
        )
        self.refused(
            make_elf(versions={"libc.so.6": ("GLIBC_2.34", "GLIBC_PRIVATE")}),
            "the library uses private glibc symbols (GLIBC_PRIVATE)",
        )

    def test_a_real_library(self) -> None:
        import _ctypes_test  # a small extension module of the standard library

        needs = elfneeds.read_needs(_ctypes_test.__file__)
        self.assertIn(needs.machine, ("x86_64", "aarch64"))
        self.assertIn("libc.so.6", needs.libraries)
        self.assertTrue(any(v.startswith("GLIBC_2.") for v in needs.versions))
        major, minor = elfneeds.glibc_version(needs)
        host = os.confstr("CS_GNU_LIBC_VERSION").split()[1].split(".")
        self.assertLessEqual((major, minor), (int(host[0]), int(host[1])))
```

Acceptance: `--mode unit test_elfneeds` runs 5 tests and passes.

#### CPYTHON-MAKE-01-WHEEL

Files: `tools/build_wheel.py`, `pyproject.toml` (edit: append the build-system table), `tests/test_build_wheel.py`. Design sections: 6.1, 6.2, 6.4, 6.5.

Step.
1. Write `tools/build_wheel.py` as printed (6.1, 6.2, 6.4, 6.5) and make it executable
   (`chmod +x`).
2. Append to `pyproject.toml` (created by CPYTHON-BUILD-01-SKELETON; keep its five
   lines) one empty line and the four lines of 6.5:

   ```toml

   [build-system]
   requires = []
   build-backend = "build_wheel"
   backend-path = ["tools"]
   ```

The tests build wheels from a synthetic library (`tests.elf_util.make_elf`) into
temporary directories; they never write into the repository. Pure Python: unit mode.
`test_wheel_of_a_small_tree` pins the SHA-256 lines of three small files; they are the
digests of the texts the test itself writes.

The complete file `tools/build_wheel.py`:

```python
#!/usr/bin/env python3
"""Build the stuntgres wheel: the package plus libspq.so, standard library only.

    python3 bindings/cpython/tools/build_wheel.py --libspq PATH --out DIR

writes DIR/stuntgres-<version>-py3-none-<platform>.whl, removes older stuntgres
wheels from DIR and prints the path of the new one. The module is also the
PEP 517 build backend of bindings/cpython (pyproject.toml), so that
'pip install bindings/cpython' builds the same wheel.
"""
from __future__ import annotations

import argparse
import base64
import csv
import glob
import hashlib
import io
import os
import re
import sys
import zipfile
from typing import Any, Sequence

HERE = os.path.dirname(os.path.abspath(__file__))
if HERE not in sys.path:
    sys.path.insert(0, HERE)

import elfneeds  # noqa: E402

ROOT = os.path.dirname(HERE)
REPO = os.path.dirname(os.path.dirname(ROOT))
DEFAULT_LIBSPQ = os.path.join(REPO, "build", "libspq", "lib", "libspq.so")
PACKAGE = "stuntgres"
LIBRARY = "stuntgres/_lib/libspq.so"
# Fixed time stamp of every member: the same input gives the same wheel.
STAMP = (2020, 1, 1, 0, 0, 0)
GENERATOR = "stuntgres build_wheel"
_FIELD = r'^{}\s*=\s*"([^"]*)"\s*$'


class BuildError(Exception):
    """The wheel cannot be built; the text says why."""


def read_metadata(root: str) -> dict[str, str]:
    """Name, version, description and requires-python of pyproject.toml.

    The version must equal stuntgres/_version.py, which is its one source.
    """
    with open(os.path.join(root, "pyproject.toml"), encoding="utf-8") as fh:
        text = fh.read()
    found = {}
    for key in ("name", "version", "description", "requires-python"):
        match = re.search(_FIELD.format(re.escape(key)), text, re.M)
        if match is None:
            raise BuildError(f"pyproject.toml has no {key}")
        found[key] = match.group(1)
    with open(os.path.join(root, PACKAGE, "_version.py"), encoding="utf-8") as fh:
        match = re.search(_FIELD.format("__version__"), fh.read(), re.M)
    if match is None:
        raise BuildError("stuntgres/_version.py has no __version__")
    if match.group(1) != found["version"]:
        raise BuildError(
            f"version mismatch: stuntgres/_version.py says {match.group(1)},"
            f" pyproject.toml says {found['version']}"
        )
    return found


def package_files(root: str) -> list[str]:
    """The files of the package, as sorted paths relative to `root`."""
    found = []
    top = os.path.join(root, PACKAGE)
    for directory, names, files in os.walk(top):
        names[:] = sorted(n for n in names if n not in ("__pycache__", "_lib"))
        for name in sorted(files):
            if name.endswith((".pyc", ".pyo")):
                continue
            path = os.path.relpath(os.path.join(directory, name), root)
            found.append(path.replace(os.sep, "/"))
    return sorted(found)


def metadata_text(meta: dict[str, str]) -> str:
    return (
        "Metadata-Version: 2.1\n"
        f"Name: {meta['name']}\n"
        f"Version: {meta['version']}\n"
        f"Summary: {meta['description']}\n"
        f"Requires-Python: {meta['requires-python']}\n"
    )


def wheel_text(tag: str) -> str:
    return (
        "Wheel-Version: 1.0\n"
        f"Generator: {GENERATOR}\n"
        "Root-Is-Purelib: false\n"
        f"Tag: py3-none-{tag}\n"
    )


def _digest(data: bytes) -> str:
    raw = hashlib.sha256(data).digest()
    return "sha256=" + base64.urlsafe_b64encode(raw).rstrip(b"=").decode("ascii")


def _add(
    archive: zipfile.ZipFile, name: str, data: bytes, record: list[Any], mode: int
) -> None:
    info = zipfile.ZipInfo(name, STAMP)
    info.compress_type = zipfile.ZIP_DEFLATED
    info.external_attr = (0o100000 | mode) << 16
    archive.writestr(info, data)
    record.append((name, _digest(data), str(len(data))))


def build(libspq: str, out_dir: str, root: str = ROOT) -> str:
    """Write the wheel into `out_dir` and return its path."""
    if not os.path.isfile(libspq):
        raise BuildError(f"{libspq} not found: run 'make libspq' first")
    meta = read_metadata(root)
    try:
        tag = elfneeds.platform_tag(libspq)
    except elfneeds.ElfError as exc:
        raise BuildError(str(exc)) from None
    stem = f"{meta['name']}-{meta['version']}"
    info_dir = f"{stem}.dist-info"
    os.makedirs(out_dir, exist_ok=True)
    for old in glob.glob(os.path.join(out_dir, f"{meta['name']}-*.whl")):
        os.remove(old)
    path = os.path.join(out_dir, f"{stem}-py3-none-{tag}.whl")
    partial = path + ".part"
    record: list[Any] = []
    try:
        with zipfile.ZipFile(partial, "w", zipfile.ZIP_DEFLATED) as archive:
            for name in package_files(root):
                with open(os.path.join(root, name), "rb") as fh:
                    _add(archive, name, fh.read(), record, 0o644)
            with open(libspq, "rb") as fh:
                _add(archive, LIBRARY, fh.read(), record, 0o755)
            _add(archive, f"{info_dir}/METADATA", metadata_text(meta).encode(),
                 record, 0o644)
            _add(archive, f"{info_dir}/WHEEL", wheel_text(tag).encode(), record, 0o644)
            record.append((f"{info_dir}/RECORD", "", ""))
            text = io.StringIO()
            csv.writer(text, lineterminator="\n").writerows(record)
            info = zipfile.ZipInfo(f"{info_dir}/RECORD", STAMP)
            info.compress_type = zipfile.ZIP_DEFLATED
            info.external_attr = (0o100000 | 0o644) << 16
            archive.writestr(info, text.getvalue().encode("utf-8"))
        os.replace(partial, path)
    finally:
        if os.path.exists(partial):
            os.remove(partial)
    return path


def verify(path: str) -> list[str]:
    """Check every member of a wheel against its RECORD; return the names."""
    with zipfile.ZipFile(path) as archive:
        names = archive.namelist()
        record_name = [n for n in names if n.endswith(".dist-info/RECORD")]
        if len(record_name) != 1:
            raise BuildError(f"{path}: expected one RECORD, found {len(record_name)}")
        rows = list(csv.reader(io.StringIO(archive.read(record_name[0]).decode())))
        if [row[0] for row in rows] != names:
            raise BuildError(f"{path}: RECORD does not list the members in order")
        for name, digest, size in rows:
            if name == record_name[0]:
                if digest or size:
                    raise BuildError(f"{path}: RECORD must not hash itself")
                continue
            data = archive.read(name)
            if digest != _digest(data) or size != str(len(data)):
                raise BuildError(f"{path}: {name} does not match RECORD")
    return names


# PEP 517 hooks: pip calls them with bindings/cpython as the current directory.

def _libspq_setting(config_settings: Any) -> str:
    chosen = (config_settings or {}).get("libspq") or os.environ.get("STUNTGRES_LIBSPQ")
    return os.path.abspath(chosen) if chosen else DEFAULT_LIBSPQ


def get_requires_for_build_wheel(config_settings: Any = None) -> list[str]:
    return []


def build_wheel(
    wheel_directory: str, config_settings: Any = None, metadata_directory: Any = None
) -> str:
    try:
        path = build(_libspq_setting(config_settings), wheel_directory, os.getcwd())
    except BuildError as exc:
        raise SystemExit(f"build_wheel: {exc}") from None
    return os.path.basename(path)


def build_sdist(sdist_directory: str, config_settings: Any = None) -> str:
    raise SystemExit(
        "build_wheel: no source distribution: the wheel carries a native library"
        " built by 'make libspq'"
    )


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="build_wheel.py", description="Build the stuntgres wheel."
    )
    parser.add_argument("--libspq", default=DEFAULT_LIBSPQ)
    parser.add_argument("--out", default=os.path.join(REPO, "build", "cpython", "dist"))
    args = parser.parse_args(argv)
    try:
        path = build(args.libspq, args.out)
        verify(path)
    except BuildError as exc:
        print(f"build_wheel: {exc}", file=sys.stderr)
        return 1
    print(path)
    return 0


if __name__ == "__main__":
    sys.exit(main())
```

Tests to write first. Before the code exists: `tests/test_build_wheel.py` fails to import: `No module named 'build_wheel'`.

The complete file `tests/test_build_wheel.py` (8 tests):

```python
"""Tests of tools/build_wheel.py: the wheel builder."""
from __future__ import annotations

import io
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
import zipfile
from contextlib import redirect_stderr, redirect_stdout

from tests.elf_util import make_elf

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, os.path.join(ROOT, "tools"))

import build_wheel  # noqa: E402

WHEEL = "stuntgres-0.1.0-py3-none-manylinux_2_34_x86_64.whl"
INFO = "stuntgres-0.1.0.dist-info"
METADATA = (
    "Metadata-Version: 2.1\n"
    "Name: stuntgres\n"
    "Version: 0.1.0\n"
    "Summary: PEP 249 driver for the embedded stuntgres engine\n"
    "Requires-Python: >=3.10\n"
)
WHEEL_FILE = (
    "Wheel-Version: 1.0\n"
    "Generator: stuntgres build_wheel\n"
    "Root-Is-Purelib: false\n"
    "Tag: py3-none-manylinux_2_34_x86_64\n"
)


class BuildWheelTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = tempfile.mkdtemp(prefix="stuntgres-wheel-")
        self.addCleanup(shutil.rmtree, self.tmp)
        self.lib = os.path.join(self.tmp, "libspq.so")
        with open(self.lib, "wb") as fh:
            fh.write(make_elf())
        self.out = os.path.join(self.tmp, "dist")

    def fake_root(self, version: str = "0.1.0", project: str = "0.1.0") -> str:
        root = os.path.join(self.tmp, "root")
        os.makedirs(os.path.join(root, "stuntgres", "_lib"))
        os.makedirs(os.path.join(root, "stuntgres", "sub", "__pycache__"))
        files = {
            "pyproject.toml": (
                '[project]\nname = "stuntgres"\n'
                f'version = "{project}"\n'
                'description = "PEP 249 driver for the embedded stuntgres engine"\n'
                'requires-python = ">=3.10"\n'
            ),
            "stuntgres/__init__.py": "from ._version import __version__\n",
            "stuntgres/_version.py": f'__version__ = "{version}"\n',
            "stuntgres/py.typed": "",
            "stuntgres/sub/b.py": "B = 2\n",
            "stuntgres/sub/a.py": "A = 1\n",
            "stuntgres/sub/__pycache__/a.cpython-312.pyc": "x",
            "stuntgres/stale.pyc": "x",
            "stuntgres/_lib/libspq.so": "an old copy that must not be packed",
        }
        for name, text in files.items():
            with open(os.path.join(root, name), "w", encoding="utf-8") as fh:
                fh.write(text)
        return root

    def test_metadata_of_the_source_tree(self) -> None:
        self.assertEqual(
            build_wheel.read_metadata(ROOT),
            {
                "name": "stuntgres", "version": "0.1.0",
                "description": "PEP 249 driver for the embedded stuntgres engine",
                "requires-python": ">=3.10",
            },
        )
        self.assertEqual(build_wheel.DEFAULT_LIBSPQ, os.path.join(
            os.path.dirname(os.path.dirname(ROOT)), "build", "libspq", "lib",
            "libspq.so",
        ))

    def test_wheel_of_a_small_tree(self) -> None:
        root = self.fake_root()
        path = build_wheel.build(self.lib, self.out, root)
        self.assertEqual(path, os.path.join(self.out, WHEEL))
        with zipfile.ZipFile(path) as archive:
            self.assertEqual(
                archive.namelist(),
                [
                    "stuntgres/__init__.py", "stuntgres/_version.py",
                    "stuntgres/py.typed", "stuntgres/sub/a.py", "stuntgres/sub/b.py",
                    "stuntgres/_lib/libspq.so", f"{INFO}/METADATA", f"{INFO}/WHEEL",
                    f"{INFO}/RECORD",
                ],
            )
            self.assertEqual(archive.read(f"{INFO}/METADATA").decode(), METADATA)
            self.assertEqual(archive.read(f"{INFO}/WHEEL").decode(), WHEEL_FILE)
            self.assertEqual(archive.read("stuntgres/_lib/libspq.so"), make_elf())
            record = archive.read(f"{INFO}/RECORD").decode().splitlines()
            self.assertEqual(
                record[3:5],
                [
                    "stuntgres/sub/a.py,"
                    "sha256=Bu289DNhZaJx5lJAJaZDnDyqMkb9B5bxFncieXB6UyU,6",
                    "stuntgres/sub/b.py,"
                    "sha256=N7i6c7XGaYZnKYj6qocK1ffcPqeEEigSn8gxLHxe9n8,6",
                ],
            )
            self.assertEqual(record[2], "stuntgres/py.typed,"
                             "sha256=47DEQpj8HBSa-_TImW-5JCeuQeRkm5NMpJWZG3hSuFU,0")
            self.assertEqual(record[-1], f"{INFO}/RECORD,,")
            modes = {i.filename: i.external_attr >> 16 for i in archive.infolist()}
            self.assertEqual(modes["stuntgres/_lib/libspq.so"], 0o100755)
            self.assertEqual(modes["stuntgres/sub/a.py"], 0o100644)
            self.assertEqual(
                {i.date_time for i in archive.infolist()}, {(2020, 1, 1, 0, 0, 0)}
            )
        self.assertEqual(len(build_wheel.verify(path)), 9)
        with open(path, "rb") as fh:
            first = fh.read()
        with open(os.path.join(self.out, "stuntgres-0.0.9-py3-none-any.whl"), "w"):
            pass
        build_wheel.build(self.lib, self.out, root)
        self.assertEqual(os.listdir(self.out), [WHEEL])
        with open(path, "rb") as fh:
            self.assertEqual(fh.read(), first)

    def test_wheel_of_the_source_tree_imports(self) -> None:
        path = build_wheel.build(self.lib, self.out)
        names = build_wheel.verify(path)
        self.assertEqual(names[-4:], [
            "stuntgres/_lib/libspq.so", f"{INFO}/METADATA", f"{INFO}/WHEEL",
            f"{INFO}/RECORD",
        ])
        self.assertIn("stuntgres/__init__.py", names)
        self.assertIn("stuntgres/py.typed", names)
        self.assertEqual([n for n in names if "__pycache__" in n or "tests/" in n], [])
        site = os.path.join(self.tmp, "site")
        with zipfile.ZipFile(path) as archive:
            archive.extractall(site)
        code = (
            "import sys; sys.path.insert(0, sys.argv[1]); import stuntgres, os;"
            " print(stuntgres.__version__,"
            " os.path.dirname(stuntgres.__file__) == os.path.join(sys.argv[1],"
            " 'stuntgres'), os.path.isfile(os.path.join(sys.argv[1], 'stuntgres',"
            " '_lib', 'libspq.so')))"
        )
        done = subprocess.run(
            [sys.executable, "-I", "-W", "error", "-c", code, site],
            capture_output=True, text=True, timeout=60, cwd=self.tmp,
        )
        self.assertEqual((done.returncode, done.stderr), (0, ""))
        self.assertEqual(done.stdout.strip(), "0.1.0 True True")

    def test_failures(self) -> None:
        missing = os.path.join(self.tmp, "nosuch.so")
        with self.assertRaises(build_wheel.BuildError) as caught:
            build_wheel.build(missing, self.out)
        self.assertEqual(
            str(caught.exception), f"{missing} not found: run 'make libspq' first"
        )
        with self.assertRaises(build_wheel.BuildError) as caught:
            build_wheel.build(self.lib, self.out, self.fake_root("0.2.0"))
        self.assertEqual(
            str(caught.exception),
            "version mismatch: stuntgres/_version.py says 0.2.0, pyproject.toml says"
            " 0.1.0",
        )
        with open(self.lib, "wb") as fh:
            fh.write(make_elf(needed=("libssl.so.3",)))
        with self.assertRaises(build_wheel.BuildError) as caught:
            build_wheel.build(self.lib, self.out)
        self.assertEqual(
            str(caught.exception),
            f"{self.lib}: needs libssl.so.3; a wheel may rely only on glibc",
        )
        self.assertFalse(os.path.exists(self.out) and os.listdir(self.out))

    def test_verify_detects_a_changed_member(self) -> None:
        path = build_wheel.build(self.lib, self.out, self.fake_root())
        changed = os.path.join(self.tmp, "changed.whl")
        with zipfile.ZipFile(path) as source, zipfile.ZipFile(changed, "w") as target:
            for info in source.infolist():
                data = source.read(info.filename)
                if info.filename == "stuntgres/sub/a.py":
                    data = b"A = 3\n"
                target.writestr(info, data)
        with self.assertRaises(build_wheel.BuildError) as caught:
            build_wheel.verify(changed)
        self.assertEqual(
            str(caught.exception),
            f"{changed}: stuntgres/sub/a.py does not match RECORD",
        )

    def test_command_line(self) -> None:
        out, err = io.StringIO(), io.StringIO()
        with redirect_stdout(out), redirect_stderr(err):
            code = build_wheel.main(["--libspq", self.lib, "--out", self.out])
        self.assertEqual((code, err.getvalue()), (0, ""))
        self.assertEqual(out.getvalue(), os.path.join(self.out, WHEEL) + "\n")
        out, err = io.StringIO(), io.StringIO()
        with redirect_stdout(out), redirect_stderr(err):
            code = build_wheel.main(["--libspq", self.lib + ".no", "--out", self.out])
        self.assertEqual((code, out.getvalue()), (1, ""))
        self.assertEqual(
            err.getvalue(),
            f"build_wheel: {self.lib}.no not found: run 'make libspq' first\n",
        )

    def test_build_backend_hooks(self) -> None:
        self.assertEqual(build_wheel.get_requires_for_build_wheel(), [])
        root = self.fake_root()
        here = os.getcwd()
        os.chdir(root)
        try:
            name = build_wheel.build_wheel(self.out, {"libspq": self.lib})
            self.assertEqual(name, WHEEL)
            self.assertTrue(os.path.isfile(os.path.join(self.out, WHEEL)))
            with self.assertRaises(SystemExit) as caught:
                build_wheel.build_wheel(self.out, {"libspq": self.lib + ".no"})
            self.assertEqual(
                str(caught.exception),
                f"build_wheel: {self.lib}.no not found: run 'make libspq' first",
            )
            with self.assertRaises(SystemExit) as caught:
                build_wheel.build_sdist(self.out)
            self.assertEqual(
                str(caught.exception),
                "build_wheel: no source distribution: the wheel carries a native"
                " library built by 'make libspq'",
            )
        finally:
            os.chdir(here)

    def test_pyproject_names_the_backend(self) -> None:
        with open(os.path.join(ROOT, "pyproject.toml"), encoding="utf-8") as fh:
            text = fh.read()
        self.assertIn(
            '\n[build-system]\nrequires = []\nbuild-backend = "build_wheel"\n'
            'backend-path = ["tools"]\n',
            text,
        )
```

Acceptance: `--mode unit test_build_wheel test_skeleton` passes (`test_build_wheel` has 8 tests). `python3 bindings/cpython/tools/build_wheel.py --libspq /nonexistent --out /tmp/x` exits 1 and prints `build_wheel: /nonexistent not found: run 'make libspq' first`. If pip is installed, `python3 -m pip install --no-index --no-deps --target <empty dir> bindings/cpython` fails with the line `build_wheel: <repository>/build/libspq/lib/libspq.so not found: run 'make libspq' first` while libspq is not built (and writes nothing into the repository).

#### CPYTHON-MAKE-02-TARGETS

Files: `Makefile` (edit: the .PHONY line and one appended block), `tools/runinstalled.py`, `tools/wheel_smoke.py`, `tests/test_runinstalled.py`. Design sections: 7, 9.4.

Step.
1. Root `Makefile` (this is the only CPYTHON part that edits it; LIBSPQ-BUILD-01-SKELETON
   has added its block before): append ` cpython cpython-check` to the end of the
   existing `.PHONY` line, and append the block of 7.1 at the end of the file after one
   empty line, exactly as printed there (recipe lines start with a tab). Change nothing
   else: not `quickcheck`, not `finalcheck`, not `deps`. If the `.PHONY` line or the end
   of the file was changed by another part meanwhile (CLEANUP-GOFMT-PRODUCTION adds a
   target; the JDBC binding adds a block of its own), keep their text and add yours
   after it; when a rebase conflicts in the `.PHONY` line, the resolution is the other
   side's line with the two words appended.
2. Write `tools/runinstalled.py` (7.3) and `tools/wheel_smoke.py` (9.4) as printed and
   make both executable.

Checks of this part:

- `make -n cpython` and `make -n cpython-check REFERENCE=1 SERVER=1` print the commands
  of 7.2 (after those of libspq).
- `make cpython` builds `build/cpython/dist/stuntgres-0.1.0-py3-none-manylinux_*.whl`.
- `python3 -m zipfile -e build/cpython/dist/stuntgres-*.whl build/cpython/site` and then
  `python3 -I -W error bindings/cpython/tools/wheel_smoke.py build/cpython/site` prints
  `wheel smoke test passed: stuntgres 0.1.0`. This needs libspq (the LIBSPQ
  prerequisites) and could not be run when the part was designed, except through the
  test seam; if libspq answers differently, stop and report (C8).
- `python3 -W error bindings/cpython/tools/runinstalled.py build/cpython/site --mode
  unit` passes, and `... --mode native test_backend_native` passes.

`make cpython-check` as a whole passes only when every test family passes in native
mode; that is the acceptance of CPYTHON-NATIVE-99-FINAL, not of this part. Run it once
and record in the commit body which families still fail.

The complete file `tools/runinstalled.py`:

```python
#!/usr/bin/env python3
"""Run the tests of bindings/cpython against an installed copy of the package.

    python3 bindings/cpython/tools/runinstalled.py SITE --mode <mode> [selector ...]

SITE is a directory that holds the unpacked wheel. The package stuntgres is
imported from SITE; the tests and the runner are those of the source tree.
"""
from __future__ import annotations

import os
import sys
from typing import Sequence

EXIT_USAGE, EXIT_NO_START = 2, 3


def main(argv: Sequence[str] | None = None) -> int:
    args = list(sys.argv[1:] if argv is None else argv)
    if not args or args[0].startswith("-"):
        print(
            "usage: runinstalled.py SITE --mode <mode> [selector ...]", file=sys.stderr
        )
        return EXIT_USAGE
    site = os.path.abspath(args[0])
    wanted = os.path.join(site, "stuntgres")
    if not os.path.isfile(os.path.join(wanted, "__init__.py")):
        print(f"runinstalled: no package stuntgres in {site}", file=sys.stderr)
        return EXIT_NO_START
    sys.path.insert(0, site)
    import stuntgres

    found = os.path.dirname(os.path.abspath(stuntgres.__file__))
    if found != wanted:
        print(
            f"runinstalled: stuntgres was imported from {found}, not from {wanted}",
            file=sys.stderr,
        )
        return EXIT_NO_START
    # The runner puts the source tree first on sys.path; the package is already
    # imported, so the tests get the installed one.
    import runtests

    return runtests.main(args[1:])


if __name__ == "__main__":
    sys.exit(main())
```

The complete file `tools/wheel_smoke.py`:

```python
#!/usr/bin/env python3
"""Smoke test of an installed stuntgres wheel.

    python3 -I bindings/cpython/tools/wheel_smoke.py SITE [--conninfo STRING]

SITE is the directory the wheel was unpacked into. Run it with -I, so that
nothing but SITE and the standard library is importable. Prints one line and
exits 0 when every check passes; a failed check raises.
"""
from __future__ import annotations

import argparse
import os
import sys
import threading
import warnings
from typing import Any, Sequence

FORK_MESSAGE = "libspq cannot be used in a process forked after the library was loaded"


def mapped() -> list[str]:
    """Paths of the libspq and libpq libraries mapped into this process."""
    paths = set()
    with open("/proc/self/maps", encoding="ascii", errors="replace") as fh:
        for line in fh:
            fields = line.split(None, 5)
            if len(fields) == 6:
                paths.add(fields[5].strip())
    return sorted(p for p in paths if "libspq" in p or "libpq" in p)


def check(condition: Any, what: str) -> None:
    if not condition:
        raise AssertionError(what)


def one_query(stuntgres: Any, conninfo: str, number: int, out: list[Any]) -> None:
    conn = stuntgres.connect(conninfo)
    try:
        out.append(conn.execute("select %s::int4 * 2", (number,)).fetchone())
    finally:
        conn.close()


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="wheel_smoke.py")
    parser.add_argument("site")
    parser.add_argument("--conninfo", default=None)
    args = parser.parse_args(argv)
    site = os.path.abspath(args.site)
    seam = bool(os.environ.get("_STUNTGRES_TEST_LIBPQ"))
    conninfo = args.conninfo or f"host=smoke-{os.getpid()} dbname=smoke user=smoke"
    sys.path.insert(0, site)

    # 1. Importing the package maps no native library.
    import stuntgres

    package = os.path.join(site, "stuntgres")
    check(
        os.path.dirname(os.path.abspath(stuntgres.__file__)) == package,
        f"stuntgres was imported from {stuntgres.__file__}, not from {package}",
    )
    check(mapped() == [], f"import stuntgres mapped {mapped()}")

    # 2. Connect, query, commit, close; the library is the one the wheel carries.
    conn = stuntgres.connect(conninfo)
    bundled = os.path.join(package, "_lib", "libspq.so")
    if not seam:
        from stuntgres.pq import _loader

        loaded = _loader.load()
        check(loaded.flavor == "libspq", f"loaded a {loaded.flavor}")
        check(loaded.path == bundled, f"loaded {loaded.path}, not {bundled}")
        check(mapped() == [os.path.realpath(bundled)], f"mapped: {mapped()}")
    cur = conn.cursor()
    cur.execute("create table smoke (id int4 primary key, name text)")
    cur.execute("insert into smoke values (%s, %s), (%s, %s)", (1, "pen", 2, None))
    cur.execute("select id, name from smoke order by id")
    check(cur.fetchall() == [(1, "pen"), (2, None)], "wrong rows")
    conn.commit()

    # 3. A forked child cannot connect; the parent goes on.
    with warnings.catch_warnings():
        warnings.simplefilter("ignore", DeprecationWarning)
        pid = os.fork()
    if pid == 0:
        code = 1
        try:
            stuntgres.connect(conninfo)
        except stuntgres.InterfaceError as exc:
            code = 0 if str(exc) == FORK_MESSAGE else 2
        except BaseException:
            code = 3
        finally:
            os._exit(code)
    _, status = os.waitpid(pid, 0)
    check(os.waitstatus_to_exitcode(status) == 0, f"forked child: status {status}")
    check(conn.execute("select count(*) from smoke").fetchone() == (2,), "parent")
    conn.rollback()

    # 4. Two threads use two connections.
    results: list[Any] = []
    threads = [
        threading.Thread(target=one_query, args=(stuntgres, conninfo, n, results))
        for n in (10, 20)
    ]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join(60)
    check(sorted(results) == [(20,), (40,)], f"threads: {results}")

    conn.execute("drop table smoke")
    conn.commit()
    conn.close()
    if not seam:
        stuntgres.drop_cluster(f"smoke-{os.getpid()}")
    print(f"wheel smoke test passed: stuntgres {stuntgres.__version__}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
```

The block appended to the root `Makefile` (7.1):

```make
# cpython: the Python DB-API binding (docs/CPYTHON.md). These targets need python3
# (3.10 or newer) and what libspq needs. They are not part of quickcheck.
# cpython-check also takes REFERENCE=1 (adds oracle mode; needs Docker) and SERVER=1
# (adds server mode; builds cmd/stuntgres).
PYTHON ?= python3
SERVER ?= 0
CPYTHON_SRC = bindings/cpython
CPYTHON_OUT = build/cpython
CPYTHON_SITE = $(CPYTHON_OUT)/site
CPYTHON_RUN = $(PYTHON) -W error $(CPYTHON_SRC)/tools/runinstalled.py $(CPYTHON_SITE)

# Build the wheel that carries libspq.so.
cpython: libspq
	$(PYTHON) $(CPYTHON_SRC)/tools/build_wheel.py --libspq $(LIBSPQ_OUT)/lib/libspq.so --out $(CPYTHON_OUT)/dist

# Build the wheel, unpack it into a scratch directory and test the unpacked package.
cpython-check: cpython
	rm -rf $(CPYTHON_SITE)
	$(PYTHON) -m zipfile -e $(CPYTHON_OUT)/dist/stuntgres-*.whl $(CPYTHON_SITE)
	$(CPYTHON_RUN) --mode unit
	$(CPYTHON_RUN) --mode native
	$(PYTHON) -I -W error $(CPYTHON_SRC)/tools/wheel_smoke.py $(CPYTHON_SITE)
	$(if $(filter 1,$(REFERENCE)),$(CPYTHON_RUN) --mode oracle)
	$(if $(filter 1,$(SERVER)),$(CPYTHON_RUN) --mode server)
```

Tests to write first. Before the code exists: `tests/test_runinstalled.py` fails: the tool does not exist (`can't open file ... runinstalled.py`, exit status 2 instead of the expected text).

The complete file `tests/test_runinstalled.py` (3 tests):

```python
"""Tests of tools/runinstalled.py: the runner against an unpacked wheel."""
from __future__ import annotations

import os
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
TOOL = os.path.join(ROOT, "tools", "runinstalled.py")


class RunInstalledTest(unittest.TestCase):
    def run_tool(self, *args: str) -> "subprocess.CompletedProcess[str]":
        return subprocess.run(
            [sys.executable, "-W", "error", TOOL, *args],
            capture_output=True, text=True, timeout=120,
        )

    def test_usage(self) -> None:
        for args in ((), ("--mode", "unit")):
            done = self.run_tool(*args)
            self.assertEqual(done.returncode, 2)
            self.assertEqual(
                done.stderr,
                "usage: runinstalled.py SITE --mode <mode> [selector ...]\n",
            )

    def test_no_package(self) -> None:
        empty = tempfile.mkdtemp(prefix="stuntgres-site-")
        self.addCleanup(shutil.rmtree, empty)
        done = self.run_tool(empty, "--mode", "unit")
        self.assertEqual(done.returncode, 3)
        self.assertEqual(
            done.stderr, f"runinstalled: no package stuntgres in {empty}\n"
        )

    def site(self, version: str) -> str:
        """A copy of the package, as an unpacked wheel, saying `version`."""
        site = tempfile.mkdtemp(prefix="stuntgres-site-")
        self.addCleanup(shutil.rmtree, site)
        shutil.copytree(
            os.path.join(ROOT, "stuntgres"), os.path.join(site, "stuntgres"),
            ignore=shutil.ignore_patterns("__pycache__", "_lib"),
        )
        with open(
            os.path.join(site, "stuntgres", "_version.py"), "w", encoding="utf-8"
        ) as fh:
            fh.write("from __future__ import annotations\n\n")
            fh.write(f'__version__ = "{version}"\n')
        return site

    def test_the_tests_see_the_installed_package(self) -> None:
        selector = "test_skeleton.SkeletonTest.test_version"
        done = self.run_tool(self.site("9.9.9"), "--mode", "unit", selector)
        self.assertEqual(done.returncode, 1)
        self.assertIn("AssertionError: '9.9.9' != '0.1.0'", done.stderr)
        done = self.run_tool(self.site("0.1.0"), "--mode", "unit", selector)
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertRegex(done.stderr, r"Ran 1 test in [0-9.]+s\n\nOK\n$")
```

Acceptance: The five checks of the step. `make -n quickcheck` prints no line containing `cpython`. `git diff --stat` lists `Makefile`, the two tools and the test file only.

#### CPYTHON-DOCS-01-FINAL

Files: `bindings/cpython/README.md` (rewrite), `docs/CPYTHON.md` (edit: the status paragraph), `AGENTS.md` (edit), `docs/DEVELOPMENT.md` (edit), `docs/TESTING.md` (edit), `docs/LIBSPQ.md` (edit: the Python paragraph of section 8). Design sections: 8.

Step. Documentation only: no code, test, tool or Makefile change. Write the texts of section
8, each at the place it names:

1. `bindings/cpython/README.md`: replace the file by the text of 8.1.
2. `docs/CPYTHON.md`: replace the **Status** paragraph by the text of 8.2.
3. `AGENTS.md`: the three additions of 8.3. Load and follow the `writing-for-agents`
   skill before editing this file.
4. `docs/DEVELOPMENT.md`: the two table rows and the bullet of 8.4.
5. `docs/TESTING.md`: the bullet of 8.5.
6. `docs/LIBSPQ.md`: the paragraph of 8.6.

Other parts edit four of these files too (LIBSPQ-DOCS-01-FINAL, which is done before
this part through CPYTHON-NATIVE-99-FINAL; LIBSPQ-PGSESSION-19-DOCS, the CLEANUP parts
that maintain the package map, GRIND-INFRA-DOCKER-IMAGE, LIBSPQ-EXT-01 and -02 in
section 6 of `docs/LIBSPQ.md`, and the JDBC binding's documentation part). Never rewrite
a line this part does not own: insert the texts at the anchors of section 8, or at the
fallback each anchor names when the line it refers to is absent. On a rebase conflict
keep the other side's lines and insert yours again.

Verify every statement you write against the tree (file names, target names, the wheel
name `make cpython` produced), and correct `docs/CPYTHON.md` where
CPYTHON-NATIVE-99-FINAL recorded a difference.

Acceptance: `git diff --stat` lists exactly the six files and no file with a code extension. `grep -n 'bindings/cpython' AGENTS.md docs/DEVELOPMENT.md docs/TESTING.md docs/LIBSPQ.md` finds the new text in each; `grep -n 'cpython-check' AGENTS.md docs/DEVELOPMENT.md bindings/cpython/README.md` finds the target in each; `grep -c 'To be specified' docs/CPYTHON.md` prints 0. Every relative link in the six files resolves to a file. `make build` passes.

#### CPYTHON-ENGINE-06-DECLARE-EXTENDED-OUTSIDE-BLOCK

An engine gap found in server mode (rule D8). Smallest reproduction: outside a
transaction block, send `DECLARE "c1" CURSOR FOR select 1` (no `WITH HOLD`) with the
extended protocol (Parse, Bind, Execute, Sync, as libpq's `PQsendQueryParams` does), then
`FETCH FORWARD 1 FROM "c1"`.

| | PostgreSQL 18.6 | stuntgres server (a07ba892fb) |
|---|---|---|
| `DECLARE "c1" CURSOR FOR select 1`, extended protocol, no transaction block | `ERROR 25P01 DECLARE CURSOR can only be used in transaction blocks`; session `IDLE` | `COMMAND_OK`, tag `DECLARE CURSOR`; session `IDLE` |
| `FETCH FORWARD 1 FROM "c1"` afterwards | `ERROR 34000 cursor "c1" does not exist` | `ERROR 34000 cursor "c1" does not exist` (the cursor ended with the implicit transaction) |
| the same `DECLARE` with the simple protocol | `ERROR 25P01` | `ERROR 25P01` (already correct) |
| `DECLARE "w" CURSOR WITH HOLD FOR select 1`, either protocol, no block | `DECLARE CURSOR` | `DECLARE CURSOR` (already correct) |
| the same `DECLARE` inside `BEGIN` | `DECLARE CURSOR` | `DECLARE CURSOR` (already correct) |

Where it is decided (read on the tree; line numbers of a07ba892fb):
`internal/engine/core/execute.go`, `(*Engine).executeStatement`, lines 95 to 101: `if
!scope.Owner.InBlock() && !declaration.Hold { return Result{}, SQLError("25P01", "DECLARE
CURSOR can only be used in transaction blocks") }`. `InBlock`
(`internal/engine/session/owner.go`, lines 143 to 145) returns `t.State == TxActive ||
t.State == TxImplicit && t.RetainsTransaction()`, and `RetainsTransaction`
(`internal/engine/session/protocol_cycle.go`, lines 21 to 23) is true while an
extended-protocol cycle is active (`StartProtocolCommand`,
`internal/engine/core/protocol_command.go`, sets `cycle.Active = true` for every
extended message and begins an implicit transaction). So through the extended protocol
the implicit transaction that is kept until Sync counts as a transaction block. In
PostgreSQL it does not: a transaction block is what `BEGIN` opens, or the implicit block
of a multi-statement simple query.

Needed by `ServerCursorTest.test_autocommit` (CPYTHON-SERVERCUR-02-SYNC) and
`AsyncServerCursorTest.test_withhold_and_autocommit` (CPYTHON-SERVERCUR-03-ASYNC): with
`autocommit=True` a cursor without `withhold` must fail with `25P01`.

The same `InBlock()` test guards `LOCK TABLE` (`internal/engine/core/lock_table.go`),
`SET LOCAL`, `SET TRANSACTION` (`transaction_isolation.go`) and `SET CONSTRAINTS`
(`set_constraints.go`); whether they differ from PostgreSQL through the extended protocol
was not observed. The part probes them in its reference test and reports what it finds;
it fixes only what the same predicate fixes.

Raw evidence: `.cswd/tmp/CPYTHON-DESIGN-FEATURES/exp2/oracle.txt`, `server.txt`
(section 1), `oracle-b.txt`, `server-b.txt` (section 8: the simple protocol).

#### CPYTHON-ENGINE-07-PREPARE-TRANSACTION-TAG

An engine gap found in server mode (rule D8). Smallest reproduction: `BEGIN`, `select
1/0` (the transaction fails), `PREPARE TRANSACTION 'failed'`.

| | PostgreSQL 18.6 | stuntgres server (a07ba892fb) |
|---|---|---|
| `PREPARE TRANSACTION 'failed'` in a failed transaction | `COMMAND_OK`, tag `ROLLBACK`; session `IDLE`; nothing prepared | `COMMAND_OK`, tag `PREPARE TRANSACTION`; session `IDLE`; nothing prepared |
| `PREPARE TRANSACTION 'outside'` with no transaction open | tag `ROLLBACK`, `WARNING 25P01 there is no transaction in progress` | tag `PREPARE TRANSACTION`, the same warning |
| `COMMIT PREPARED 'failed'` afterwards | `ERROR 42704 prepared transaction with identifier "failed" does not exist` | the same |
| `PREPARE TRANSACTION 'x'` in a healthy transaction | tag `PREPARE TRANSACTION` | tag `PREPARE TRANSACTION` (already correct) |

Where it is decided: `internal/engine/session/command_tag.go`, `ClassifyCommandTag`,
`case parser.KindPrepareTransaction: base = "PREPARE TRANSACTION"` (lines 359 to 360),
chosen before the statement runs from the state `before`. Directly above it (lines 351
to 356) `COMMIT` already becomes `ROLLBACK` when `before == TxAborted`: the same
treatment is missing for `PREPARE TRANSACTION` when `before` is `TxAborted` or `TxIdle`.
The execution itself (`internal/engine/core/transaction_prepared.go`,
`executePrepared`) already warns and rolls back correctly; only the tag is wrong.

Needed by `TpcTest.test_prepare_of_a_failed_transaction` (CPYTHON-TPC-02-CONNECTION):
`tpc_prepare()` reads the tag to learn that nothing was prepared (4.3).

`command_tag.go` is also edited by CPYTHON-ENGINE-04-START-TRANSACTION-TAG (the same
clause; a prerequisite of this part, so the two are ordered) and named by three pending
CLEANUP parts; this part's record repeats the coordination rule of CPYTHON-ENGINE-04.

Raw evidence: `.cswd/tmp/CPYTHON-DESIGN-FEATURES/exp3/oracle.txt`, `server.txt` and
`exp2/oracle-b.txt`, `server-b.txt` (section 9, with the notices).

#### CPYTHON-ENGINE-08-DEFAULT-CURSOR-SCROLL (P2)

An engine difference found in server mode (rule D8); no designed test depends on it.
Smallest reproduction, on `create table t (id int4 primary key, name text)` with seven
rows: `BEGIN; DECLARE c CURSOR FOR select id from t where id > 1 order by id; FETCH
FORWARD 3 FROM c; MOVE ABSOLUTE 0 FROM c;`.

| | PostgreSQL 18.6 | stuntgres server (a07ba892fb) |
|---|---|---|
| `MOVE ABSOLUTE 0 FROM c` after the fetch | `MOVE 0`; the next fetch returns the first row | `ERROR 55000 cursor can only scan forward` |
| `select is_scrollable from pg_cursors where name = 'c'` | `t` | `f` |
| the same with `DECLARE c SCROLL CURSOR` | `MOVE 0` | `MOVE 0` (already correct) |
| the same with `DECLARE c NO SCROLL CURSOR` | `ERROR 55000 cursor can only scan forward` | the same (already correct) |

PostgreSQL makes a cursor declared with neither `SCROLL` nor `NO SCROLL` scrollable
when its plan can scan backward ("The default is to allow scrolling in some cases",
`sql-declare.html`). The engine treats the default of an SQL `DECLARE` as `NO SCROLL`:
`internal/engine/core/sql_cursor.go`, `installSQLCursor`, passes `declaration.Scroll ==
parser.CursorScrollEnabled`. The PL/pgSQL `OPEN` path already applies PostgreSQL's rule
(`internal/engine/core/execute_prepared_bound.go`: `scroll =
exec.SupportsBackwardCursor(queryRoot, source)` for `parser.CursorScrollDefault`;
`internal/engine/exec/cursor_backward.go`).

The difference is pinned on purpose by an existing reference test,
`reference/cursors/pg_cursors_metadata_test.go` (`TestReferencePGCursorsMetadata`:
"native omitted SCROLL intentionally remains forward-only, unlike explicit SCROLL"). A
fix reverses that expectation; the part therefore asks for the owner's confirmation
before it changes anything. The Python binding's tests scroll backward only on cursors
made with `scrollable=True`.

Raw evidence: `.cswd/tmp/CPYTHON-DESIGN-FEATURES/exp2/oracle-b.txt`, `server-b.txt`
(section 2 and the `pg_cursors` lines).

#### CPYTHON-NATIVE-01-PQ-CORE to CPYTHON-NATIVE-12-CONTROL

One part per row of 9.2, all with the procedure of 9.3. The selectors of each part
(the test files of its family):

| Part | Runner call: `python3 bindings/cpython/tools/runtests.py --mode native` followed by |
|---|---|
| CPYTHON-NATIVE-01-PQ-CORE | `test_errors test_enums test_loader test_ffi test_pq_conninfo test_pq_base test_pq_connect test_pq_result test_pq_exec test_support test_backends test_backend_native` |
| CPYTHON-NATIVE-02-PQ-EXTRAS | `test_pq_prepared test_pq_notices test_pq_notify test_pq_cancel test_pq_copy test_pq_rowmodes test_pq_escaping` |
| CPYTHON-NATIVE-03-GENERATORS | `test_generators test_waiting test_waiting_async` |
| CPYTHON-NATIVE-04-ADAPT | `test_queries test_adapt_map test_transformer test_oids test_oids_catalog test_types_numeric test_types_string test_types_datetime test_types_timestamp test_types_interval test_types_uuid test_types_json test_types_array test_types_list test_dbapi20_types test_defaults test_roundtrip_basic test_roundtrip_datetime test_roundtrip_composite` |
| CPYTHON-NATIVE-05-CONNECTION | `test_diagnostic test_sqlstates test_rows test_conninfo test_connection_info test_conn_core test_connection_base test_connection test_conn_exec test_conn_txnopts test_transaction test_conn_notices test_conn_notify test_conn_cancel` |
| CPYTHON-NATIVE-06-CURSOR | `test_column test_cur_results test_cursor_core test_cursor_execute test_cursor_fetch test_cursor_executemany test_cursor_nextset test_cursor_optional` |
| CPYTHON-NATIVE-07-ASYNC | `test_async_connection test_async_cursor test_async_transaction` |
| CPYTHON-NATIVE-08-MODULE | `test_module test_dbapi20_vendor test_dbapi20_compliance test_features` |
| CPYTHON-NATIVE-09-COPY | `test_copygen test_copyfmt test_copy test_copy_edges test_copy_async` |
| CPYTHON-NATIVE-10-SERVERCUR | `test_servercur_base test_servercur_move test_server_cursor test_server_cursor_async` |
| CPYTHON-NATIVE-11-TPC | `test_xid test_tpc` |
| CPYTHON-NATIVE-12-CONTROL | `test_pq_ext test_control test_testing` |

#### CPYTHON-NATIVE-99-FINAL

Files: none of its own. Design sections: 9.1, 9.3, 9.4, 7.

Step. After the twelve family parts and CPYTHON-MAKE-02-TARGETS:

1. `make cpython-check` (builds libspq and the wheel, unpacks it, runs the suite in unit
   and native mode against the unpacked package, then the wheel smoke test). It must
   pass as a whole.
2. `python3 bindings/cpython/tools/runtests.py --mode native test_dbapi20_compliance`
   passes: the vendored compliance suite against libspq (DBAPI 10).
3. `make cpython-check REFERENCE=1 SERVER=1` passes: the same package in all four modes.
4. A failure is handled as 9.3 step 3 prescribes; a repair of the binding is made in the
   file that holds the fault and listed in the commit body. If a behavior that
   `docs/CPYTHON.md` states turned out different under libspq and the difference was
   accepted by the coordinator, record it in the commit body for CPYTHON-DOCS-01-FINAL.

Acceptance: the three commands exit 0; `make cpython-check` prints `wheel smoke test
passed: stuntgres 0.1.0`; `git diff --stat` lists no file under `tests/`.

## 11. Experiments behind this design

Everything is under `.cswd/tmp/CPYTHON-DESIGN-FEATURES/`: `log.txt` holds the commands
and their output (written by `run_all.sh`), `scratch/` the designed code and tests on
top of the scratch package of the three earlier designs, `exp1/` to `exp7/` the
experiment scripts with their `oracle.txt` and `server.txt`, `audit/` the final audit.
Oracle mode: libpq 18.6 extracted from the pinned image against a container of that
image (`postgres:18.6-bookworm@sha256:afc7e2d4...`, started with
`max_prepared_transactions=100` as PQ 9.4 prescribes). Server mode: the same libpq
against `cmd/stuntgres` built from the worktree with the tag `stuntgres_reduced_kdf`.
Host: Linux x86_64, glibc 2.39, CPython 3.12.3, pip 24.0. Nothing ran in native mode.

| # | Question | Outcome | Used in |
|---|---|---|---|
| 1 | The COPY state machine through the pq layer and the designed generators: copy in, copy out, a server error in the middle of each, an abort by the client, a cancel during copy out, COPY inside a multi-statement string, parameters, CSV and binary, a large copy in, the text of special values. | 14 cases, **identical in oracle and server mode**. The statuses and tags of 2.1; `put_copy_data` never returned 0 and `flush` returned 1 with libpq over TCP; the extended protocol refuses a second statement (`42601`) and parameters (`08P01`); the lines of 2.3. | 2; F1 to F6 |
| 2 | The statements of a named cursor: `DECLARE` in and outside a transaction, with a bound parameter, `SCROLL`, `NO SCROLL`, `WITH HOLD`; the Describe of the portal; `FETCH FORWARD`; `MOVE ABSOLUTE` to every kind of target; `CLOSE` once and twice; `pg_cursors`; quoting; errors. | The table of 3.2. Server mode differs in: the Describe (`34000`, CPYTHON-ENGINE-01); `DECLARE` without `HOLD` outside a block through the extended protocol succeeds (CPYTHON-ENGINE-06); a cursor with neither `SCROLL` nor `NO SCROLL` never scrolls backward and shows `is_scrollable` `f` (CPYTHON-ENGINE-08). With the Describe replaced by `FETCH FORWARD 0` (a stand-in used only in this experiment) the 29 designed server-cursor tests pass in oracle mode and all but the two autocommit cases of ENGINE-06 pass in server mode. | 3; F7 to F11 |
| 3 | The two-phase statements and `pg_prepared_xacts`: prepare, recover, commit and roll back prepared, from the same and from another session; every failure; identifier length and quoting; the server setting. | The table of 4.2. Server mode differs in: `show max_prepared_transactions` is `0` (PostgreSQL: the configured 100) while `PREPARE TRANSACTION` works; `PREPARE TRANSACTION` in a failed transaction or without one answers with the tag `PREPARE TRANSACTION` (PostgreSQL: `ROLLBACK`), CPYTHON-ENGINE-07. | 4; F12 to F15 |
| 4 | The wheel builder on a stand-in for `libspq.so` (a cgo `c-shared` library that links the whole engine, built from a scratch module): is the wheel accepted by pip and by `zipfile`, do the `RECORD` hashes verify, does the package import from the scratch directory; what does pip do with the source directory with and without a `[build-system]` table. | 83 MB library, 34 MB wheel, 2.5 s. `pip install --no-index --no-deps --target` and `python3 -m zipfile -e` give the same tree; 91 members verified; the import maps no library and `BUNDLED` is the unpacked file. With the in-tree backend pip builds and installs the same wheel offline; without the table pip fails looking for setuptools. | 6; F18, F20, F21 |
| 5 | The platform-tag rule on real shared objects of this host. | The table of 6.3: the stand-in needs `libc.so.6` only and at most `GLIBC_2.34` on glibc 2.39: `manylinux_2_34_x86_64`; libpq is refused for its other libraries; `libm` for `GLIBC_PRIVATE`. The values equal `readelf -d` and `readelf -V`. | 6.3; F19 |
| 6 | The Makefile block appended to a copy of the Makefile that already has the libspq block of `docs/design/LIBSPQ-CABI.md` 8.3. | `make -n cpython`, `make -n cpython-check`, with `REFERENCE=1 SERVER=1` and with `PYTHON=python3.12`, print the commands of 7.2; `make -n quickcheck` contains none of them. The runner wrapper ran the unit suite and oracle-mode tests against an unpacked wheel; the smoke test passed through the test seam in oracle and server mode. | 7, 9.4; F22, F23 |
| 7 | The mapping from the functions the binding binds to the LIBSPQ parts that provide them, and from test families to those parts, against the catalog. | 94 bound functions, 82 required, each provided by exactly one part; the binder needs `BUILD-01` and `CABI-01` to `-12`; the table of 9.2; CPYTHON-TEST-03-NATIVE lacked nine of them. | 9.1, 9.2; F24 |
| 8 | The final audit of every CPYTHON part in the catalog. | Below. | 10 |

The designed tests **(measured; `log.txt`)**: in unit mode 251 tests pass; in oracle
mode all 664 tests of the scratch tree pass (the three earlier designs' and this
one's); in server mode 636 of 663 pass, and the failures are exactly: the
23 server-cursor tests that need the Describe (CPYTHON-ENGINE-01),
`TpcTest.test_prepare_of_a_failed_transaction` (CPYTHON-ENGINE-07), and three tests of
the earlier designs with parts of their own (CPYTHON-ENGINE-03, -04, -05). The native
classes of `tests/test_control.py` and `tests/test_testing.py` were only compiled.

Differences seen in probes that no designed test needs and the binding does not depend
on (no part filed):

- A cursor over `select 10 / (3 - id) from t order by id` (ids 1 to 7): PostgreSQL
  returns the first two rows and fails with `22012` at the third; the stuntgres server
  fails at the first `FETCH`. PostgreSQL reads the primary-key index in order and
  evaluates row by row; the engine evaluates the output expressions of all rows below
  its sort when the cursor is opened (`internal/engine/analyze/query_producer_bind.go`,
  `ScheduleQueryProjection`). When an error surfaces depends on the plan in PostgreSQL
  too.
- `show max_prepared_transactions` is `0` on the server although two-phase commit works.
- `pg_cursors.is_scrollable` (part of CPYTHON-ENGINE-08).

Checks that could not be made, and what stands in for them: no Python 3.10 or 3.11
interpreter was available (the designed files were parsed with `ast.parse(...,
feature_version=(3, 10))` and ran on 3.12.3); no arm64 host (the ELF reader's `aarch64`
path is covered by a synthetic file in its unit test); libspq does not exist (sections 5
and 9 rest on `docs/LIBSPQ.md` and `docs/design/LIBSPQ-CABI.md`); whether the repository
of the vendored suite has a two-phase file was read from its file list on the web, not
cloned.

**Scratch code.** `.cswd/tmp/CPYTHON-DESIGN-FEATURES/scratch/` holds the package, tools
and tests as they ran (`featstubs/` the six stubs). It is a non-authoritative starting
point that implementers may copy from; it is not polished and not part of the
repository. Where it differs from this document, the document wins.

Not verified in this run: anything in native mode; Python 3.10 and 3.11; arm64; the
native test classes of sections 5 and 9.4; the reference tests and fixes the three
engine parts describe.

### 11.1 The final audit

`audit/audit.py` reads every CPYTHON record through the catalog utility (`list`, then
`select`) and checks the points below. Its output is `audit/report.txt`.

1. Prerequisite graph: 115 CPYTHON parts and 62 other parts they reach; acyclic
2. Every implementation part lists a CPYTHON design part as a direct prerequisite: yes (ADAPT: 19, DBAPI: 29, FEATURES: 34, PQ: 29)
3. Of 111 implementation parts: 111 start with the rules block, 111 state an estimated size, 111 name their files
4. Files: 221 distinct files named; 96 are named by more than one part (a stub and its owner, an edited test, a file a native part may repair); pairs without an ordering path: 0
5. libspq: 18 binding parts need it at run time (TEST-03-NATIVE, PQ-19-EXT, CONTROL-01-API, CONTROL-02-ISOLATION, MAKE-02-TARGETS, NATIVE-01-PQ-CORE, NATIVE-02-PQ-EXTRAS, NATIVE-03-GENERATORS, NATIVE-04-ADAPT, NATIVE-05-CONNECTION, NATIVE-06-CURSOR, NATIVE-07-ASYNC, NATIVE-08-MODULE, NATIVE-09-COPY, NATIVE-10-SERVERCUR, NATIVE-11-TPC, NATIVE-12-CONTROL, NATIVE-99-FINAL); each lists LIBSPQ prerequisites and waits for the binder set: yes; the other 85 binding parts list none and reach none (CPYTHON-DOCS-01-FINAL, the last part, excepted)
6. Parts with an estimate of 500 lines or more: none
- (between 400 and 499, acceptable by owner decision: PQ-02-ENUMS (400), TEST-01A-SUPPORT (400), ADAPT-01B-TRANSFORMER (415), ASYNC-01-CONNECTION (420), QUERY-01-CONVERT (420), ADAPT-01A-MAP (435), CONN-04-TRANSACTION (435), ERRORS-01-DIAGNOSTIC (435), SERVERCUR-01-BASE (440), CONN-01C-CORE (445), CONN-01E-SYNC (445), CUR-01C-CORE (445), COPY-03-SYNC (470), MAKE-01-WHEEL (480), TPC-02-CONNECTION (490))
7. Public class and module-level names read from the 'Public names' of the binding parts: 145; named by more than one part: BINARY (ADAPT-01A-MAP, ADAPT-10-DBAPI20-TYPES); RowMaker (ADAPT-01B-TRANSFORMER, ROWS-01-FACTORIES) (each pair is two names in two different modules, adapt.BINARY and dbapi20.BINARY, _transformer.RowMaker and rows.RowMaker: no name of the package is created twice)

Numbers:

- parts per family: ADAPT 17, ASYNC 3, BUILD 1, CONN 11, CONTROL 2, COPY 5, CUR 8, DESIGN 4, DOCS 1, ENGINE 8, ERRORS 2, FEATURES 1, MAKE 3, MODULE 1, NATIVE 13, PQ 20, QUERY 1, ROWS 1, SERVERCUR 4, TEST 7, TPC 2; total 115 (4 design, 111 implementation)
- status: done 3, in_progress 1, pending 111
- estimated size of the 111 implementation parts: 9865 + 15005 = 24870 lines (code + tests)
- longest prerequisite chain inside the CPYTHON parts: 33 parts: DESIGN-PQ -> BUILD-01-SKELETON -> PQ-01-ERRORS-BASE -> PQ-02-ENUMS -> PQ-03-LOADER -> PQ-04-FFI -> TEST-01A-SUPPORT -> TEST-01S-STARTER -> TEST-01B-ORACLE -> PQ-05-CONNINFO -> PQ-06-PGCONN-CONNECT -> PQ-07-PGRESULT -> PQ-08-EXEC -> PQ-10-NOTICES -> PQ-16-GENERATORS -> PQ-17-WAIT-SYNC -> CONN-01C-CORE -> CONN-01D-BASE -> CONN-01E-SYNC -> CONN-02-EXEC -> CUR-01C-CORE -> CUR-02-FETCH -> CUR-04-NEXTSET -> CUR-03-EXECUTEMANY -> ASYNC-02-CURSOR -> ASYNC-03-TRANSACTION -> MODULE-01-EXPORTS -> FEATURES-01-STUBS -> COPY-03-SYNC -> COPY-03B-EDGES -> NATIVE-09-COPY -> NATIVE-99-FINAL -> DOCS-01-FINAL
- parts that can start once the design parts are done (every prerequisite is a design part or done): 8: ENGINE-01-DESCRIBE-SQL-CURSOR, ENGINE-02-CANCEL-PLPGSQL-CONTEXT, BUILD-01-SKELETON, ENGINE-03-SUBSCRIPT-UNKNOWN, ENGINE-04-START-TRANSACTION-TAG, ENGINE-05-ARRAY-CAST-COLUMN-NAME, ENGINE-06-DECLARE-EXTENDED-OUTSIDE-BLOCK, ENGINE-08-DEFAULT-CURSOR-SCROLL

Findings and repairs. The audit ran on the 81 parts that existed before this design
added its own, again after the 34 new records were inserted, and once more after the
repair:

| Finding | Cause | Repair |
|---|---|---|
| CPYTHON-TEST-03-NATIVE and CPYTHON-PQ-19-EXT (before the insertion), and with them the 14 new parts that run in native mode, did not wait for `LIBSPQ-CABI-04` to `-12` | The record of CPYTHON-TEST-03-NATIVE listed `LIBSPQ-CABI-03-BLOCKING` and `LIBSPQ-CABI-14-CONTROL` only, but its first test calls the binder, which needs all 82 required functions (9.1) | `update CPYTHON-TEST-03-NATIVE --set prerequisites=[...]`: the nine parts `LIBSPQ-CABI-04-PREPARED` to `LIBSPQ-CABI-12-CONNECT-ASYNC` added. Every other native part reaches them through it. The row of that part in `docs/design/CPYTHON-PQ.md` 10.1 was corrected to match (the one correction this design makes to an earlier document). |

After the repair the audit reports no finding. What it does not cover, and how it is
handled instead:

- **Files shared with other families.** The root `Makefile` is also edited by
  LIBSPQ-BUILD-01-SKELETON (a prerequisite of CPYTHON-MAKE-02-TARGETS) and by
  CLEANUP-GOFMT-PRODUCTION (no ordering edge: CPYTHON-MAKE-02-TARGETS only appends two
  words to the `.PHONY` line and a block at the end, and its record says how to resolve
  a conflict). `AGENTS.md`, `docs/DEVELOPMENT.md`, `docs/TESTING.md` and `docs/LIBSPQ.md`
  are edited by LIBSPQ-DOCS-01-FINAL, which CPYTHON-DOCS-01-FINAL follows through
  CPYTHON-NATIVE-99-FINAL; other parts that touch `AGENTS.md` (LIBSPQ-PGSESSION-19-DOCS,
  GRIND-INFRA-DOCKER-IMAGE, CLEANUP-DOC-STALE-CONTRACTS and four more CLEANUP parts that
  maintain the package map) or section 6 of `docs/LIBSPQ.md` (LIBSPQ-EXT-01, -02) have no
  edge: section 8 gives every text an anchor and a fallback, and the record forbids
  rewriting other lines. The JDBC binding's make and documentation parts do not exist
  yet; its design part is instructed to order them against these.
- **Engine files.** The three engine parts name files that pending CLEANUP parts also
  name; each record carries a coordination rule (stop while one of them is in progress).
  CPYTHON-ENGINE-07 follows CPYTHON-ENGINE-04, which edits the same switch.
- **The import closure.** `docgen/closure_check.py` checked that every module a new
  part's files import is owned by a part in that part's prerequisite closure (the six
  stubs that CPYTHON-FEATURES-01-STUBS creates itself excepted).
