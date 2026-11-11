# CPYTHON-PQ: foundation of the Python binding

Design of the lowest layers of the Python DB-API binding `bindings/cpython` (package
`stuntgres`): directory layout, test runner and three-mode harness, the lazy loader, the
ctypes declarations, the low-level package `stuntgres.pq`, the base exceptions, the sans-IO
generators and the waiting functions. Produced by catalog part `CPYTHON-DESIGN-PQ`. The
later designs build on it: `CPYTHON-DESIGN-ADAPT`, `CPYTHON-DESIGN-DBAPI`,
`CPYTHON-DESIGN-FEATURES`.

Words used below: **(measured)** marks a value observed in the experiments of section 11;
**oracle mode** is real libpq 18.6 against the pinned PostgreSQL 18.6 image; **server
mode** is the same libpq against the stuntgres wire server; **native mode** is libspq.
Every literal test expectation that depends on PostgreSQL or libpq was observed in oracle
mode; expectations that follow from the binding's own algorithms alone (the unit tests
with fakes, object life cycle) were derived from the designed code.

Nothing under `bindings/` exists yet and libspq is not built
(`ls bindings` fails; every `LIBSPQ-` implementation part is pending). The contract of
libspq is `docs/LIBSPQ.md`; its prototypes are `docs/design/LIBSPQ-CABI.md` section 3.

## 1. Purpose, layering, layout

```
stuntgres                      PEP 249 surface                (CPYTHON-DESIGN-DBAPI)
   adaptation                  %s -> $n, dumpers, loaders     (CPYTHON-DESIGN-ADAPT)
   stuntgres.generators        sans-IO: connect, send, fetch, notifies, cancel   (7)
   stuntgres.waiting           select.poll driver                                (8.1)
   stuntgres.waiting_async     asyncio driver                                    (8.2)
   stuntgres.pq                PGconn, PGresult, PGcancel, Conninfo, Escaping    (5)
      _ffi                     declaration table, binder                         (3)
      _loader                  lazy, signals blocked, absolute path, pid         (2)
         libspq.so             (tests in oracle and server mode: a real libpq)
stuntgres.errors               PEP 249 base classes                              (6)
```

A layer imports only layers below it. `stuntgres.errors` imports nothing of the package
at run time.

### 1.1 Fixed points

| # | Point | Evidence |
|---|---|---|
| P1 | Standard library only; CPython 3.10 or newer; Linux. Every module starts with `from __future__ import annotations`. | plan A1, U9 |
| P2 | Importing `stuntgres`, `stuntgres.pq`, `stuntgres.generators`, `stuntgres.waiting` or `stuntgres.waiting_async` maps no native library, starts no thread and reads no environment variable. | **(measured)** experiment 1: `/proc/self/maps` shows no libpq before the first call |
| P3 | The loader runs on the first call that needs the library, once per process, under a lock, with every signal blocked on the calling thread around `ctypes.CDLL`, from any thread. It records `os.getpid()`. | **(measured)** experiment 1: 60 of 60 blockable signals blocked inside `dlopen`, loaded from a worker thread, 8 racing threads got one object |
| P4 | In a process other than the loading one the loader raises; finalizers release nothing there. | **(measured)** experiment 1: the forked child is refused, the parent's connection keeps working |
| P5 | One private environment variable, `_STUNTGRES_TEST_LIBPQ`, makes the loader open a real libpq and bind the same table under libpq's names. Nothing else in the package knows which library is loaded; the only question it may ask is `stuntgres.pq.has_function(name)`. | section 2 |
| P6 | ctypes declarations are data (section 3). A missing required symbol is a load error; a missing optional symbol becomes a callable that raises `NotSupportedError`. `SPQlibVersion` never decides anything. | section 3.3 |
| P7 | The result accessors that never block and never enter the Go runtime are bound from `ctypes.PyDLL` (GIL held); every other function from `ctypes.CDLL` (GIL released). | **(measured)** experiment 9: 10000 cells in 6 to 9 ms against 10 ms alone, 13 ms against 2 to 3 s beside one CPU-bound Python thread |
| P8 | Reading cells in bulk goes through one function, `stuntgres.pq._rows.read_rows`; a compiled replacement of that module needs no change in its callers (plan U2). | section 5.6 |
| P9 | Notices: one module-level ctypes callback; each connection is known to it by an integer key in a registry of weak references. A handler's exception is logged and never enters C. | **(measured)** experiment 5 |
| P10 | `stuntgres.pq` follows `psycopg/pq/abc.py` for every member whose libpq function libspq has (section 5.16) and raises only `stuntgres.errors` classes, `TypeError`, `ValueError` and `MemoryError`. | section 5 |
| P11 | Generators are sans-IO and correct for a real libpq over TCP. | **(measured)** experiment 8: for an 8 MB parameter `flush()` returned 1 three to six times before 0 in server mode and at most once in oracle mode, and the loop finished |
| P12 | Waiters leave the descriptor unregistered and the generator closed on every exit, including `KeyboardInterrupt` and task cancellation. | **(measured)** experiments 6 and 7 |
| P13 | Tests run only through `python3 bindings/cpython/tools/runtests.py --mode <unit|oracle|server|native> [selector ...]` (rules C5, unchanged). | section 9 |
| P14 | Every database test gets state no other test sees: a database per test in oracle mode, a pre-declared database of a rolling pool in server mode, a cluster per test in native mode. | **(measured)** experiments 3 and 4 |
| P15 | One module and one test file per part; a file has one owner part (section 10.1). | plan A11 |

Experiments changed two things the part record had left open or suggested, and nothing an
owner decision fixed:

- libpq 18 from the image does not load by itself on the host (`libldap-2.5.so.0` is
  missing); it loads once two more libraries copied from the same image are opened first.
  The test seam variable therefore holds a list of paths (section 2.2).
- The stuntgres server cannot give a test a new database on request: a database created
  with `CREATE DATABASE` is not reachable by a later connection **(measured)**. That is the
  server's documented model (`pgserver/README.md`: its first paragraph, "The transport
  adds no SQL, catalog, type or cluster capability", and "Configuration and ownership",
  "Every served database owns a fresh engine"), so server mode uses the databases declared
  in the user file (section 9.5).

### 1.2 Files

Everything is under `bindings/cpython/`. "Owner" is the one part that creates the file;
"replaced by" names the part that replaces a stub (section 10.1).

| File | Responsibility | Owner |
|---|---|---|
| `README.md` | What the directory is, how to run the tests (text in section 10.3). | CPYTHON-BUILD-01-SKELETON |
| `pyproject.toml` | Project metadata only (`name`, `version` source, `requires-python`). No build backend: the wheel builder belongs to CPYTHON-DESIGN-FEATURES. | CPYTHON-BUILD-01-SKELETON |
| `.gitignore` | `__pycache__/`, `*.pyc`, `/stuntgres/_lib/`, `/dist/`, `/build/`. | CPYTHON-BUILD-01-SKELETON |
| `stuntgres/__init__.py` | Version only for now. CPYTHON-DESIGN-DBAPI hands it to the part that builds the module surface. | CPYTHON-BUILD-01-SKELETON |
| `stuntgres/_version.py` | `__version__ = "0.1.0"`. | CPYTHON-BUILD-01-SKELETON |
| `stuntgres/py.typed` | Empty marker (PEP 561). | CPYTHON-BUILD-01-SKELETON |
| `stuntgres/errors.py` | Base exceptions (6). | CPYTHON-PQ-01-ERRORS-BASE |
| `stuntgres/_gentypes.py` | `Wait`, `Ready`, `PQGen`, `PQGenConn`, `RV` (7.1). | CPYTHON-PQ-02-ENUMS |
| `stuntgres/_encodings.py` | PostgreSQL client encoding to Python codec (4.3). | CPYTHON-PQ-02-ENUMS |
| `stuntgres/pq/__init__.py` | Exports; classes are resolved lazily (5.1). Never edited again. | CPYTHON-PQ-02-ENUMS |
| `stuntgres/pq/_enums.py` | Enumerations (4.1). | CPYTHON-PQ-02-ENUMS |
| `stuntgres/pq/misc.py` | `PGnotify`, `ConninfoOption`, `need_bytes`, `error_message` (4.2). | CPYTHON-PQ-02-ENUMS |
| `stuntgres/pq/_loader.py` | Loader (2). | CPYTHON-PQ-03-LOADER |
| `stuntgres/pq/_ffi.py` | Declaration table, structures, binder (3). | CPYTHON-PQ-04-FFI |
| `stuntgres/pq/_conninfo.py` | `Conninfo` (5.2). | CPYTHON-PQ-05-CONNINFO |
| `stuntgres/pq/_pgconn_base.py` | `PGconnBase` (5.3). | CPYTHON-PQ-06A-PGCONN-BASE |
| `stuntgres/pq/_pgconn.py` | `PGconn`: life cycle, status, composition of the mixins (5.4). | CPYTHON-PQ-06-PGCONN-CONNECT |
| `stuntgres/pq/_pgresult.py` | `PGresult` (5.5). | CPYTHON-PQ-07-PGRESULT |
| `stuntgres/pq/_rows.py` | `read_rows` (5.6). | CPYTHON-PQ-07-PGRESULT |
| `stuntgres/pq/_params.py` | Parameter arrays (5.7). | CPYTHON-PQ-08-EXEC |
| `stuntgres/pq/_pgconn_exec.py` | `ExecMixin` (5.7). Stub by PQ-06A. | replaced by CPYTHON-PQ-08-EXEC |
| `stuntgres/pq/_pgconn_prepared.py` | `PreparedMixin` (5.8). Stub by PQ-06A. | replaced by CPYTHON-PQ-09-PREPARED |
| `stuntgres/pq/_notices.py` | Notice receiver (5.9). Stub by PQ-06A. | replaced by CPYTHON-PQ-10-NOTICES |
| `stuntgres/pq/_pgconn_notify.py` | `NotifyMixin` (5.10). Stub by PQ-06A. | replaced by CPYTHON-PQ-11-NOTIFY |
| `stuntgres/pq/_pgcancel.py` | `PGcancel` (5.11). | CPYTHON-PQ-12-CANCEL |
| `stuntgres/pq/_pgconn_cancel.py` | `CancelMixin` (5.11). Stub by PQ-06A. | replaced by CPYTHON-PQ-12-CANCEL |
| `stuntgres/pq/_pgconn_copy.py` | `CopyMixin` (5.12). Stub by PQ-06A. | replaced by CPYTHON-PQ-13-COPY |
| `stuntgres/pq/_pgconn_rowmodes.py` | `RowModesMixin` (5.13). Stub by PQ-06A. | replaced by CPYTHON-PQ-14-ROWMODES |
| `stuntgres/pq/_escaping.py` | `Escaping` (5.14). | CPYTHON-PQ-15-ESCAPING |
| `stuntgres/pq/ext.py` | `SPQX` wrappers (5.15). | CPYTHON-PQ-19-EXT |
| `stuntgres/generators.py` | Generators (7). | CPYTHON-PQ-16-GENERATORS |
| `stuntgres/waiting.py` | Synchronous waiter (8.1). | CPYTHON-PQ-17-WAIT-SYNC |
| `stuntgres/waiting_async.py` | asyncio waiter (8.2). | CPYTHON-PQ-18-WAIT-ASYNC |
| `tools/runtests.py` | The runner (9.1). Never edited again. | CPYTHON-BUILD-01-SKELETON |
| `tests/__init__.py` | Empty. | CPYTHON-BUILD-01-SKELETON |
| `tests/support/__init__.py` | Test cases, gates, bounded waits (9.6). | CPYTHON-TEST-01A-SUPPORT |
| `tests/support/raw.py` | `RawConn`: a connection through the bare declarations (9.6). | CPYTHON-TEST-01A-SUPPORT |
| `tests/support/libpq.py` | libpq 18 out of the pinned image (9.3). | CPYTHON-TEST-01S-STARTER |
| `tests/support/backends.py` | `start(mode, build_dir, repo_root)` (9.2). | CPYTHON-TEST-01S-STARTER |
| `tests/support/oracle.py` | Oracle container backend (9.4). | CPYTHON-TEST-01B-ORACLE |
| `tests/support/server.py` | stuntgres server backend (9.5). | CPYTHON-TEST-01C-SERVER |
| `tests/support/native.py` | libspq backend (9.7). | CPYTHON-TEST-03-NATIVE |
| `tests/third_party/dbapi20.py`, `tests/third_party/PROVENANCE.md`, `tests/third_party/__init__.py` | Vendored suite (9.8). | CPYTHON-TEST-02-DBAPI20-VENDOR |
| `tests/test_<name>.py` | One test file per part (section 10.1 names each). | the part |

Outside `bindings/cpython` the runner writes only below `build/cpython/` at the repository
root: `build/cpython/.gitignore` (the single line `*`, so the directory ignores itself
until `LIBSPQ-BUILD-01-SKELETON` adds `/build/` to the root `.gitignore`),
`build/cpython/libpq18/`, `build/cpython/stuntgres`, `build/cpython/server-<pid>/`,
`build/cpython/pgpass`.

## 2. Loader (`stuntgres/pq/_loader.py`)

Rules 2, 3 and 4 of `docs/LIBSPQ.md` section 7 ("One copy per process", "Never use libspq
in a forked child", "Load with signals blocked").

### 2.1 Public names

```python
ENV_LIBSPQ = "STUNTGRES_LIBSPQ"
ENV_TEST_LIBPQ = "_STUNTGRES_TEST_LIBPQ"
BUNDLED: str            # <directory of the package stuntgres>/_lib/libspq.so
FORK_MESSAGE = "libspq cannot be used in a process forked after the library was loaded"

class Loaded(NamedTuple):
    cdll: ctypes.CDLL     # functions called with the GIL released
    pydll: ctypes.PyDLL   # the same handle; functions called with the GIL held
    path: str             # absolute path of the library
    flavor: str           # "libspq" or "libpq"
    pid: int              # os.getpid() of the loading process

def load() -> Loaded: ...
def loaded_pid() -> int | None: ...
```

`BUNDLED` is computed without touching the file system:
`os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "_lib", "libspq.so")`.
Module state: `_lock = threading.Lock()`, `_loaded: Loaded | None = None`. The module
imports `ctypes`, `os`, `signal`, `threading`, `typing.NamedTuple` and
`from .. import errors as e`; it must not import `_ffi`.

### 2.2 Environment variables

| Variable | Read by | Meaning |
|---|---|---|
| `STUNTGRES_LIBSPQ` | `load()`, first call only | Absolute path of `libspq.so`. Part of the consumer contract. |
| `_STUNTGRES_TEST_LIBPQ` | `load()`, first call only | Test seam, not part of the consumer contract. One or more absolute paths joined with `os.pathsep`. Every path but the last is a dependency that is opened first, in order; the last is a real libpq, bound under libpq's names. When set and non-empty it wins over `STUNTGRES_LIBSPQ`. |

No variable is read while a module is imported.

### 2.3 Algorithm

`load()`:

1. `state = _loaded`. If it is `None`: take `_lock`; read `_loaded` again; if still `None`,
   `state = _loaded = _load_locked()`; release the lock (use `with _lock:`).
2. If `state.pid != os.getpid()`: raise `e.InterfaceError(FORK_MESSAGE)`.
3. Return `state`.

`_resolve() -> tuple[list[str], str, str]` (dependencies, library path, flavor):

1. `test = os.environ.get(ENV_TEST_LIBPQ)`. If it is a non-empty string:
   `paths = test.split(os.pathsep)`; return `(paths[:-1], paths[-1], "libpq")`.
2. `path = os.environ.get(ENV_LIBSPQ)`. If it is a non-empty string:
   if `not os.path.isabs(path)` raise
   `e.InterfaceError(f"{ENV_LIBSPQ} must be an absolute path, got {path!r}")`;
   if `not os.path.isfile(path)` raise
   `e.InterfaceError(f"{ENV_LIBSPQ} names a missing file: {path!r}")`;
   return `([], path, "libspq")`.
3. If `os.path.isfile(BUNDLED)`: return `([], BUNDLED, "libspq")`.
4. Raise `e.InterfaceError(f"libspq not found: set {ENV_LIBSPQ} to the absolute path of libspq.so or install it as {BUNDLED}")`.

`_load_locked() -> Loaded` (called with `_lock` held):

1. `deps, path, flavor = _resolve()`.
2. `old = signal.pthread_sigmask(signal.SIG_BLOCK, signal.valid_signals())`.
3. In a `try`: for each `dep` in `deps` call `ctypes.CDLL(dep)`; then
   `cdll = ctypes.CDLL(path)`. An `OSError` from any of these calls becomes
   `e.InterfaceError(f"cannot load {path!r}: {exc}")`, raised `from None`.
4. In the `finally` of that `try`: `signal.pthread_sigmask(signal.SIG_SETMASK, old)`.
5. `pydll = ctypes.PyDLL(path, handle=cdll._handle)` (no second `dlopen`; the handle is
   shared).
6. Return `Loaded(cdll, pydll, path, flavor, os.getpid())`.

A failed load leaves `_loaded` as `None`, so a later call tries again. Refer to
`ctypes.CDLL` and `ctypes.PyDLL` through the module `ctypes` (not `from ctypes import
CDLL`), so that the unit tests can replace them.

`loaded_pid()` returns `None` while `_loaded` is `None`, else `_loaded.pid`. It never
loads.

### 2.4 Fork rule

- After `os.fork()` the child inherits `_loaded` with the parent's pid, so every `load()`
  there raises `InterfaceError(FORK_MESSAGE)`: no new connection, no `Conninfo` call, no
  `Escaping()` without a connection. The text is libspq's own
  (`docs/design/LIBSPQ-CABI.md` 7.1) without the newline. The rule is the same when the
  test seam loaded a libpq: the binding behaves alike in every mode.
- Every finalizer (`__del__` of `PGconn`, `PGresult`, `PGcancel`) releases its native
  object only when `os.getpid()` equals the `pid` attribute of the bound library
  (section 3.2), which is `Loaded.pid`.
- Explicit calls on an object inherited from the parent are passed to the library, which
  answers as `docs/design/LIBSPQ-CABI.md` 7.1 lists.
- A child that never inherited a loaded library (fork before the first call) loads its
  own copy.

**(measured, experiment 1)** in the forked child `PGconn.connect` and `Conninfo.parse`
raised `InterfaceError: libspq cannot be used in a process forked after the library was
loaded`, `PGconn.__del__` left the pointer alone, and the parent's connection answered
`select 42` after the child had exited.

## 3. ctypes declarations (`stuntgres/pq/_ffi.py`)

### 3.1 Types and structures

The module imports `CFUNCTYPE, POINTER, Structure, c_char_p, c_int, c_int64, c_size_t,
c_uint, c_void_p` from `ctypes`, `threading`, `typing` names, `from .. import errors as e`
and `from . import _loader`. Importing it loads nothing.

Abbreviations used in the table (module-level names of `_ffi.py`):

| Name | ctypes type | Used for |
|---|---|---|
| `P` | `c_void_p` | An opaque object (`SPGconn *`, `SPGresult *`, `SPGcancel *`), a `void *`, or a buffer that Python must not copy on return. As an argument it accepts an `int`, `None` or a ctypes object; as a result it is an `int`, or `None` for NULL. |
| `S` | `c_char_p` | A NUL-terminated string. As a result ctypes copies it into `bytes` (`None` for NULL): right only where the C side keeps ownership. As an argument it accepts `bytes`, `None` or a `ctypes.create_string_buffer` array. |
| `I` | `c_int` | `int` and every enum. |
| `U` | `c_uint` | `SPQOid`. |
| `Z` | `c_size_t` | `size_t`. |
| `OPTS` | `POINTER(PQconninfoOption_struct)` | Option arrays. |

```python
class PGnotify_struct(Structure):
    _fields_ = [("relname", c_char_p), ("be_pid", c_int), ("extra", c_char_p),
                ("next", c_void_p)]

class PQconninfoOption_struct(Structure):
    _fields_ = [("keyword", c_char_p), ("envvar", c_char_p), ("compiled", c_char_p),
                ("val", c_char_p), ("label", c_char_p), ("dispchar", c_char_p),
                ("dispsize", c_int)]

NoticeReceiver = CFUNCTYPE(None, c_void_p, c_void_p)   # (void *arg, const SPGresult *res)

REQUIRED, OPTIONAL = True, False
RELEASED, HELD = "released", "held"

class Decl(NamedTuple):
    name: str                    # libpq spelling
    restype: Any
    argtypes: tuple[Any, ...]
    required: bool = REQUIRED
    gil: str = RELEASED

DECLARATIONS: tuple[Decl, ...]   # the 94 rows of section 3.4, in that order
```

Rules that follow from memory ownership (`docs/LIBSPQ.md` 5.8):

- A result that the caller must free (`SPQfreemem`, `SPQconninfoFree`) or that may hold
  NUL bytes is declared `P` (or a structure pointer), never `S`: `SPQgetvalue`,
  `SPQescapeLiteral`, `SPQescapeIdentifier`, `SPQescapeByteaConn`, `SPQescapeBytea`,
  `SPQunescapeBytea`; the out-parameters of `SPQconninfoParse` (`errmsg`),
  `SPQgetCopyData` (`buffer`) and `SPQXdump` (`buffer`) are `POINTER(P)`.
- Such a buffer is read with `ctypes.string_at(address, length)` (or
  `string_at(address)` for a NUL-terminated string) and freed in a `finally` block.

### 3.2 The binder

```python
def symbol_name(name: str, flavor: str) -> str: ...

class Library:
    path: str
    flavor: str
    pid: int
    def __init__(self, loaded: _loader.Loaded) -> None: ...
    def has(self, name: str) -> bool: ...
    # plus one attribute per row of DECLARATIONS, named Decl.name

def get() -> Library: ...
```

`symbol_name(name, flavor)`: for `flavor == "libpq"` return `name`; otherwise return
`"S" + name` when `name[0]` is upper case and `"s" + name` when it is lower case
(`PQexec` -> `SPQexec`, `PQXreset` -> `SPQXreset`; no lower-case name is bound today, the
rule is the one of `docs/LIBSPQ.md` section 2).

`Library.__init__(loaded)`:

1. Copy `loaded.path`, `loaded.flavor`, `loaded.pid` to the attributes; `self._present:
   set[str] = set()`.
2. For each `decl` in `DECLARATIONS`, in order:
   1. `dll = loaded.pydll if decl.gil == HELD else loaded.cdll`.
   2. `symbol = symbol_name(decl.name, loaded.flavor)`.
   3. `func = getattr(dll, symbol)`. On `AttributeError`: if `decl.required`, raise
      `e.InterfaceError(f"{loaded.path!r} lacks the required function {symbol}")` (`from
      None`); otherwise `setattr(self, decl.name, _missing(symbol))` and continue with the
      next row.
   4. `func.restype = decl.restype`; `func.argtypes = list(decl.argtypes)`;
      `setattr(self, decl.name, func)`; add `decl.name` to `self._present`.

`_missing(symbol)` returns a function `missing(*args)` that raises
`e.NotSupportedError(f"{symbol} is not available in the loaded library")`.

`Library.has(name)` returns `name in self._present`.

`get()`; module state `_lock = threading.Lock()`, `_library: Library | None = None`:

1. `lib = _library`. If it is not `None`: call `_loader.load()` (this is the fork check of
   2.3 step 2) and return `lib`.
2. Otherwise, under `_lock`: read `_library` again; if still `None`,
   `_library = Library(_loader.load())`. Return it.

Callers: code that creates an object from nothing calls `_ffi.get()` (`PGconn.connect`,
`PGconn.connect_start`, `PGconn.ping`, `Conninfo`, `Escaping` without a connection,
`pq.version`, `pq.has_function`, `pq.ext`). Every object stores the `Library` it was
created with in `_lib` and calls through it, so the hot paths make no global lookup.

### 3.3 The optional-symbol rule

Optional are the functions libpq 16 lacks (`PQsetChunkedRowsMode`, `PQclosePrepared`,
`PQclosePortal`, `PQsendClosePrepared`, `PQsendClosePortal`, `PQsocketPoll`,
`PQgetCurrentTimeUSec`) and every `PQX` row. Everything else is required. Whether a
function exists is decided by the symbol lookup alone; `PQlibVersion` is bound only to be
reported by `pq.version()`. Code above `_ffi` asks `stuntgres.pq.has_function(name)`
(section 5.1) and nothing else. Core paths of the binding call only required functions
(plan A4).

### 3.4 The table

`P`, `S`, `I`, `U`, `Z`, `OPTS` as in 3.1; `—` means no arguments (`argtypes == ()`).
"GIL held" rows are bound from `Loaded.pydll`, the others from `Loaded.cdll`.

| # | Name | libspq symbol | restype | argtypes | Required | GIL |
|---|---|---|---|---|---|---|
| 1 | `PQconnectdb` | `SPQconnectdb` | `P` | `S` | required | released |
| 2 | `PQconnectStart` | `SPQconnectStart` | `P` | `S` | required | released |
| 3 | `PQconnectPoll` | `SPQconnectPoll` | `I` | `P` | required | released |
| 4 | `PQfinish` | `SPQfinish` | `None` | `P` | required | released |
| 5 | `PQreset` | `SPQreset` | `None` | `P` | required | released |
| 6 | `PQping` | `SPQping` | `I` | `S` | required | released |
| 7 | `PQconninfoParse` | `SPQconninfoParse` | `OPTS` | `S, POINTER(P)` | required | released |
| 8 | `PQconndefaults` | `SPQconndefaults` | `OPTS` | `—` | required | released |
| 9 | `PQconninfo` | `SPQconninfo` | `OPTS` | `P` | required | released |
| 10 | `PQconninfoFree` | `SPQconninfoFree` | `None` | `OPTS` | required | released |
| 11 | `PQstatus` | `SPQstatus` | `I` | `P` | required | released |
| 12 | `PQtransactionStatus` | `SPQtransactionStatus` | `I` | `P` | required | released |
| 13 | `PQerrorMessage` | `SPQerrorMessage` | `S` | `P` | required | released |
| 14 | `PQdb` | `SPQdb` | `S` | `P` | required | released |
| 15 | `PQuser` | `SPQuser` | `S` | `P` | required | released |
| 16 | `PQpass` | `SPQpass` | `S` | `P` | required | released |
| 17 | `PQhost` | `SPQhost` | `S` | `P` | required | released |
| 18 | `PQport` | `SPQport` | `S` | `P` | required | released |
| 19 | `PQoptions` | `SPQoptions` | `S` | `P` | required | released |
| 20 | `PQparameterStatus` | `SPQparameterStatus` | `S` | `P, S` | required | released |
| 21 | `PQprotocolVersion` | `SPQprotocolVersion` | `I` | `P` | required | released |
| 22 | `PQserverVersion` | `SPQserverVersion` | `I` | `P` | required | released |
| 23 | `PQbackendPID` | `SPQbackendPID` | `I` | `P` | required | released |
| 24 | `PQsocket` | `SPQsocket` | `I` | `P` | required | released |
| 25 | `PQlibVersion` | `SPQlibVersion` | `I` | `—` | required | released |
| 26 | `PQsendQuery` | `SPQsendQuery` | `I` | `P, S` | required | released |
| 27 | `PQsendQueryParams` | `SPQsendQueryParams` | `I` | `P, S, I, POINTER(U), POINTER(S), POINTER(I), POINTER(I), I` | required | released |
| 28 | `PQsendPrepare` | `SPQsendPrepare` | `I` | `P, S, S, I, POINTER(U)` | required | released |
| 29 | `PQsendQueryPrepared` | `SPQsendQueryPrepared` | `I` | `P, S, I, POINTER(S), POINTER(I), POINTER(I), I` | required | released |
| 30 | `PQsendDescribePrepared` | `SPQsendDescribePrepared` | `I` | `P, S` | required | released |
| 31 | `PQsendDescribePortal` | `SPQsendDescribePortal` | `I` | `P, S` | required | released |
| 32 | `PQsendClosePrepared` | `SPQsendClosePrepared` | `I` | `P, S` | optional | released |
| 33 | `PQsendClosePortal` | `SPQsendClosePortal` | `I` | `P, S` | optional | released |
| 34 | `PQgetResult` | `SPQgetResult` | `P` | `P` | required | released |
| 35 | `PQconsumeInput` | `SPQconsumeInput` | `I` | `P` | required | released |
| 36 | `PQisBusy` | `SPQisBusy` | `I` | `P` | required | released |
| 37 | `PQflush` | `SPQflush` | `I` | `P` | required | released |
| 38 | `PQsetnonblocking` | `SPQsetnonblocking` | `I` | `P, I` | required | released |
| 39 | `PQisnonblocking` | `SPQisnonblocking` | `I` | `P` | required | released |
| 40 | `PQsocketPoll` | `SPQsocketPoll` | `I` | `I, I, I, c_int64` | optional | released |
| 41 | `PQgetCurrentTimeUSec` | `SPQgetCurrentTimeUSec` | `c_int64` | `—` | optional | released |
| 42 | `PQsetSingleRowMode` | `SPQsetSingleRowMode` | `I` | `P` | required | released |
| 43 | `PQsetChunkedRowsMode` | `SPQsetChunkedRowsMode` | `I` | `P, I` | optional | released |
| 44 | `PQexec` | `SPQexec` | `P` | `P, S` | required | released |
| 45 | `PQexecParams` | `SPQexecParams` | `P` | `P, S, I, POINTER(U), POINTER(S), POINTER(I), POINTER(I), I` | required | released |
| 46 | `PQprepare` | `SPQprepare` | `P` | `P, S, S, I, POINTER(U)` | required | released |
| 47 | `PQexecPrepared` | `SPQexecPrepared` | `P` | `P, S, I, POINTER(S), POINTER(I), POINTER(I), I` | required | released |
| 48 | `PQdescribePrepared` | `SPQdescribePrepared` | `P` | `P, S` | required | released |
| 49 | `PQdescribePortal` | `SPQdescribePortal` | `P` | `P, S` | required | released |
| 50 | `PQclosePrepared` | `SPQclosePrepared` | `P` | `P, S` | optional | released |
| 51 | `PQclosePortal` | `SPQclosePortal` | `P` | `P, S` | optional | released |
| 52 | `PQresultStatus` | `SPQresultStatus` | `I` | `P` | required | held |
| 53 | `PQntuples` | `SPQntuples` | `I` | `P` | required | held |
| 54 | `PQnfields` | `SPQnfields` | `I` | `P` | required | held |
| 55 | `PQbinaryTuples` | `SPQbinaryTuples` | `I` | `P` | required | held |
| 56 | `PQfname` | `SPQfname` | `S` | `P, I` | required | held |
| 57 | `PQftable` | `SPQftable` | `U` | `P, I` | required | held |
| 58 | `PQftablecol` | `SPQftablecol` | `I` | `P, I` | required | held |
| 59 | `PQfformat` | `SPQfformat` | `I` | `P, I` | required | held |
| 60 | `PQftype` | `SPQftype` | `U` | `P, I` | required | held |
| 61 | `PQfsize` | `SPQfsize` | `I` | `P, I` | required | held |
| 62 | `PQfmod` | `SPQfmod` | `I` | `P, I` | required | held |
| 63 | `PQgetvalue` | `SPQgetvalue` | `P` | `P, I, I` | required | held |
| 64 | `PQgetlength` | `SPQgetlength` | `I` | `P, I, I` | required | held |
| 65 | `PQgetisnull` | `SPQgetisnull` | `I` | `P, I, I` | required | held |
| 66 | `PQnparams` | `SPQnparams` | `I` | `P` | required | held |
| 67 | `PQparamtype` | `SPQparamtype` | `U` | `P, I` | required | held |
| 68 | `PQcmdStatus` | `SPQcmdStatus` | `S` | `P` | required | held |
| 69 | `PQcmdTuples` | `SPQcmdTuples` | `S` | `P` | required | held |
| 70 | `PQoidValue` | `SPQoidValue` | `U` | `P` | required | held |
| 71 | `PQclear` | `SPQclear` | `None` | `P` | required | released |
| 72 | `PQfreemem` | `SPQfreemem` | `None` | `P` | required | released |
| 73 | `PQresultErrorMessage` | `SPQresultErrorMessage` | `S` | `P` | required | held |
| 74 | `PQresultErrorField` | `SPQresultErrorField` | `S` | `P, I` | required | held |
| 75 | `PQsetNoticeReceiver` | `SPQsetNoticeReceiver` | `P` | `P, NoticeReceiver, P` | required | released |
| 76 | `PQescapeLiteral` | `SPQescapeLiteral` | `P` | `P, S, Z` | required | released |
| 77 | `PQescapeIdentifier` | `SPQescapeIdentifier` | `P` | `P, S, Z` | required | released |
| 78 | `PQescapeStringConn` | `SPQescapeStringConn` | `Z` | `P, S, S, Z, POINTER(I)` | required | released |
| 79 | `PQescapeString` | `SPQescapeString` | `Z` | `S, S, Z` | required | released |
| 80 | `PQescapeByteaConn` | `SPQescapeByteaConn` | `P` | `P, S, Z, POINTER(Z)` | required | released |
| 81 | `PQescapeBytea` | `SPQescapeBytea` | `P` | `S, Z, POINTER(Z)` | required | released |
| 82 | `PQunescapeBytea` | `SPQunescapeBytea` | `P` | `S, POINTER(Z)` | required | released |
| 83 | `PQgetCancel` | `SPQgetCancel` | `P` | `P` | required | released |
| 84 | `PQcancel` | `SPQcancel` | `I` | `P, S, I` | required | released |
| 85 | `PQfreeCancel` | `SPQfreeCancel` | `None` | `P` | required | released |
| 86 | `PQputCopyData` | `SPQputCopyData` | `I` | `P, S, I` | required | released |
| 87 | `PQputCopyEnd` | `SPQputCopyEnd` | `I` | `P, S` | required | released |
| 88 | `PQgetCopyData` | `SPQgetCopyData` | `I` | `P, POINTER(P), I` | required | released |
| 89 | `PQnotifies` | `SPQnotifies` | `POINTER(PGnotify_struct)` | `P` | required | released |
| 90 | `PQXversion` | `SPQXversion` | `S` | `—` | optional | released |
| 91 | `PQXclusterDrop` | `SPQXclusterDrop` | `I` | `S, S, S, I` | optional | released |
| 92 | `PQXreset` | `SPQXreset` | `I` | `P` | optional | released |
| 93 | `PQXdump` | `SPQXdump` | `I` | `P, POINTER(P), POINTER(Z)` | optional | released |
| 94 | `PQXrestore` | `SPQXrestore` | `I` | `P, S, Z` | optional | released |

Why 21 rows hold the GIL **(measured, experiment 9; four runs, the last one is in
`log.txt`)**: `ctypes.CDLL` releases and retakes the GIL around every call. Reading 10000
cells takes 20000 calls. Alone that costs about 10 ms against 6 to 9 ms with the GIL
held. Beside one CPU-bound Python thread every retake can wait a whole switch interval
(5 ms): 1.8 to 3.1 s against 12 to 13 ms. Beside three such threads: between 100 s and
730 s (extrapolated from 200 cells) against 17 to 73 ms. The held rows are exactly the accessors of an immutable result:
pure C in libspq (`docs/design/LIBSPQ-CABI.md` 1.1, fixed point F2: "Result accessors,
`SPQclear` and `SPQfreemem` never enter Go"), no I/O in libpq, so holding the GIL cannot block another thread for longer
than a memory read. They can still call back: libpq reports an out-of-range row or column
through the result's notice hooks. A ctypes callback entered with the GIL already held
works **(measured: `PQgetvalue(res, 5, 0)` from the PyDLL binding delivered `row number 5
is out of range 0..0` to the handler)**; the wrappers of section 5.5 check ranges first
and never make such a call. Everything else stays on `ctypes.CDLL`: functions that take a
connection enter the Go runtime in libspq and do I/O in libpq, and `PQclear`/`PQfreemem`
may free large blocks.

### 3.5 Functions of `docs/LIBSPQ.md` section 3 that are not bound

`docs/LIBSPQ.md` section 3 names 109 functions (104 in `libspq-fe.h`, 5 in
`libspq-ext.h`); 94 are bound. The other 15, and why:

| Function | Reason |
|---|---|
| `SPQconnectdbParams`, `SPQconnectStartParams`, `SPQpingParams` | psycopg's abc has no member for them; a conninfo string says the same. |
| `SPQclientEncoding`, `spg_encoding_to_char`, `spg_char_to_encoding` | The encoding name comes from `parameter_status(b"client_encoding")` (`PGconn._encoding`, 5.4), which needs no id-to-name table. |
| `SPQsetClientEncoding` | Blocking. The upper layers send `SET client_encoding` through the nonblocking path. |
| `SPQisthreadsafe` | Always 1; nothing depends on it. |
| `SPQresStatus` | `ExecStatus(value).name` gives the name. |
| `SPQfnumber` | No abc member; the DB-API layer maps names itself. |
| `SPQresultMemorySize` | No abc member, no caller. |
| `SPQresultVerboseErrorMessage`, `SPQsetErrorVerbosity`, `SPQsetErrorContextVisibility` | Errors are built from `error_field` values; no first-wave feature changes the verbosity. |
| `SPQsetNoticeProcessor` | The receiver (5.9) gets the whole result; a processor would get less. |

`PQsocketPoll` and `PQgetCurrentTimeUSec` are bound (optional) but no class calls them:
waiting is done in Python (section 8). They are in the table so that a later part needs
no change here to use them. A later design that needs one of the 15 adds a row to
`DECLARATIONS` in a part that lists `_ffi.py` and `tests/test_ffi.py` (whose
`test_table_shape` pins the counts and the name sets) and depends on the last part that
edited them.

## 4. Enums and plain data types

### 4.1 Enumerations (`stuntgres/pq/_enums.py`)

Each is an `enum.IntEnum` with a one-line docstring. Member names are psycopg's
(`psycopg/pq/_enums.py`); values are libpq's (`docs/design/LIBSPQ-CABI.md` 2.2 and 2.3).
Members are written in the order shown.

| Enum | Members |
|---|---|
| `ConnStatus` | `OK` = 0, `BAD` = 1, `STARTED` = 2, `MADE` = 3, `AWAITING_RESPONSE` = 4, `AUTH_OK` = 5, `SETENV` = 6, `SSL_STARTUP` = 7, `NEEDED` = 8, `CHECK_WRITABLE` = 9, `CONSUME` = 10, `GSS_STARTUP` = 11, `CHECK_TARGET` = 12, `CHECK_STANDBY` = 13, `ALLOCATED` = 14, `AUTHENTICATING` = 15 |
| `PollingStatus` | `FAILED` = 0, `READING` = 1, `WRITING` = 2, `OK` = 3, `ACTIVE` = 4 |
| `ExecStatus` | `EMPTY_QUERY` = 0, `COMMAND_OK` = 1, `TUPLES_OK` = 2, `COPY_OUT` = 3, `COPY_IN` = 4, `BAD_RESPONSE` = 5, `NONFATAL_ERROR` = 6, `FATAL_ERROR` = 7, `COPY_BOTH` = 8, `SINGLE_TUPLE` = 9, `PIPELINE_SYNC` = 10, `PIPELINE_ABORTED` = 11, `TUPLES_CHUNK` = 12 |
| `TransactionStatus` | `IDLE` = 0, `ACTIVE` = 1, `INTRANS` = 2, `INERROR` = 3, `UNKNOWN` = 4 |
| `Ping` | `OK` = 0, `REJECT` = 1, `NO_RESPONSE` = 2, `NO_ATTEMPT` = 3 |
| `Format` | `TEXT` = 0, `BINARY` = 1 |
| `Wait` | `R` = 1, `W` = 2, `RW` = 3 |
| `Ready` | `NONE` = 0, `R` = 1, `W` = 2, `RW` = 3 |
| `DiagnosticField` | `SEVERITY` = `ord("S")` = 83, `SEVERITY_NONLOCALIZED` = `ord("V")` = 86, `SQLSTATE` = `ord("C")` = 67, `MESSAGE_PRIMARY` = `ord("M")` = 77, `MESSAGE_DETAIL` = `ord("D")` = 68, `MESSAGE_HINT` = `ord("H")` = 72, `STATEMENT_POSITION` = `ord("P")` = 80, `INTERNAL_POSITION` = `ord("p")` = 112, `INTERNAL_QUERY` = `ord("q")` = 113, `CONTEXT` = `ord("W")` = 87, `SCHEMA_NAME` = `ord("s")` = 115, `TABLE_NAME` = `ord("t")` = 116, `COLUMN_NAME` = `ord("c")` = 99, `DATATYPE_NAME` = `ord("d")` = 100, `CONSTRAINT_NAME` = `ord("n")` = 110, `SOURCE_FILE` = `ord("F")` = 70, `SOURCE_LINE` = `ord("L")` = 76, `SOURCE_FUNCTION` = `ord("R")` = 82 |

`DiagnosticField` members are written as `SEVERITY = ord("S")` and so on. `Wait` and
`Ready` are defined in `stuntgres/_gentypes.py` (7.1) and listed here for their values.
Not defined, because libspq has no function that uses them: `PipelineStatus`, `Trace`.

Methods of `stuntgres.pq` return plain `int` values of these enums (`IntEnum` members
compare equal to them).

### 4.2 Plain data (`stuntgres/pq/misc.py`)

```python
class PGnotify(NamedTuple):
    relname: bytes
    be_pid: int
    extra: bytes

class ConninfoOption(NamedTuple):
    keyword: bytes
    envvar: bytes | None
    compiled: bytes | None
    val: bytes | None
    label: bytes
    dispchar: bytes
    dispsize: int

def need_bytes(value: Any) -> bytes: ...
def error_message(obj: Any, encoding: str = "utf-8") -> str: ...
```

`need_bytes(value)`: if `not isinstance(value, bytes)` raise
`TypeError(f"bytes expected, got {type(value).__name__}")`; return `value`. Every method
of section 5 that takes a command, a name or a conninfo checks it with this function, so a
`str` fails with a clear message before ctypes sees it.

`error_message(obj, encoding)`: `raw = obj.error_message` (a `PGconn` or `PGresult`
property returning `bytes`); if `raw` is empty return `"no error details available"`;
otherwise return `raw.decode(encoding, "replace").rstrip("\n")`.

Not defined: `PGresAttDesc` (result construction is absent from libspq),
`find_libpq_full_path`, `version_pretty`, `connection_summary` (belong to psycopg's
loader and `repr`).

### 4.3 Encodings (`stuntgres/_encodings.py`)

```python
def python_codec(name: bytes) -> str: ...
```

`python_codec(name)` returns the Python codec for a PostgreSQL client-encoding name as the
server reports it in the `client_encoding` parameter. Algorithm: `key =
name.decode("ascii", "replace").upper().replace("-", "").replace("_", "")`; look `key` up
in the table below (whose keys are written without `-` and `_`); a miss raises
`e.NotSupportedError(f"client encoding {name.decode('ascii', 'replace')} has no Python codec")`.

| PostgreSQL name (table key) | Python codec |
|---|---|
| `BIG5` | `big5` |
| `EUC_CN` (`EUCCN`) | `gb2312` |
| `EUC_JP` (`EUCJP`) | `euc_jp` |
| `EUC_JIS_2004` (`EUCJIS2004`) | `euc_jis_2004` |
| `EUC_KR` (`EUCKR`) | `euc_kr` |
| `GB18030` | `gb18030` |
| `GBK` | `gbk` |
| `ISO_8859_5` … `ISO_8859_8` (`ISO88595` … `ISO88598`) | `iso8859-5` … `iso8859-8` |
| `JOHAB` | `johab` |
| `KOI8R`, `KOI8U` | `koi8-r`, `koi8-u` |
| `LATIN1`, `LATIN2`, `LATIN3`, `LATIN4` | `iso8859-1`, `iso8859-2`, `iso8859-3`, `iso8859-4` |
| `LATIN5`, `LATIN6`, `LATIN7`, `LATIN8`, `LATIN9`, `LATIN10` | `iso8859-9`, `iso8859-10`, `iso8859-13`, `iso8859-14`, `iso8859-15`, `iso8859-16` |
| `SJIS` | `shift_jis` |
| `SHIFT_JIS_2004` (`SHIFTJIS2004`) | `shift_jis_2004` |
| `SQL_ASCII` (`SQLASCII`) | `ascii` |
| `UHC` | `cp949` |
| `UTF8` | `utf-8` |
| `WIN866`, `WIN874` | `cp866`, `cp874` |
| `WIN1250` … `WIN1258` | `cp1250` … `cp1258` |

That is 40 keys. `EUC_TW` and `MULE_INTERNAL` have no Python codec and are absent on
purpose. Source of the left column: PostgreSQL 18 manual, "Character Set Support", table
"PostgreSQL Character Sets"; the right column names codecs of the standard library
(`codecs.lookup` succeeds for each on CPython 3.12.3 **(measured)**; the unit test of
CPYTHON-PQ-02-ENUMS repeats the check on the interpreter it runs under).

## 5. `stuntgres.pq`

Conventions of this section:

- `lib` is the `Library` of section 3.2; an object keeps it in `self._lib`. `lib.PQexec`
  means the bound function of the row `PQexec` (`SPQexec` in libspq).
- "closed" means the object's pointer attribute `_ptr` is `None`.
- `need_bytes(value)` (4.2) raises `TypeError(f"bytes expected, got {type(value).__name__}")`
  unless `value` is `bytes`, and returns it.
- `error_message(x)` is the function of 4.2 with its default encoding.
- A failure of a libpq call is reported as
  `e.OperationalError(f"{method} failed: {error_message(self)}")`, where `method` is the
  name of the Python method. The tables say "fails as `method`" for this.
- `Buffer = Union[bytes, bytearray, memoryview]` (defined in `_params.py`; `_escaping.py`
  defines the same alias locally).
- One thread at a time may use a connection (`docs/LIBSPQ.md` section 7 rule 5); the
  classes take no lock. Results are immutable and may be read from any thread.
- Private names (leading underscore) are fixed by this document because parts in
  different files use them; they are not consumer API.

### 5.1 Package module (`stuntgres/pq/__init__.py`)

The complete file:

```python
"""Low-level interface to the native library, shaped like psycopg 3's pq."""
from __future__ import annotations

import importlib
from typing import TYPE_CHECKING, Any

from ._enums import ConnStatus, DiagnosticField, ExecStatus, Format, Ping
from ._enums import PollingStatus, TransactionStatus
from .misc import ConninfoOption, PGnotify, error_message

if TYPE_CHECKING:
    from ._conninfo import Conninfo
    from ._escaping import Escaping
    from ._pgcancel import PGcancel
    from ._pgconn import PGconn
    from ._pgresult import PGresult

__impl__ = "ctypes"

_LAZY = {
    "Conninfo": "_conninfo",
    "Escaping": "_escaping",
    "PGcancel": "_pgcancel",
    "PGconn": "_pgconn",
    "PGresult": "_pgresult",
}

__all__ = [
    "ConnStatus", "Conninfo", "ConninfoOption", "DiagnosticField", "Escaping",
    "ExecStatus", "Format", "PGcancel", "PGconn", "PGnotify", "PGresult", "Ping",
    "PollingStatus", "TransactionStatus", "error_message", "has_function", "version",
]


def __getattr__(name: str) -> Any:
    module = _LAZY.get(name)
    if module is None:
        raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
    value = getattr(importlib.import_module("." + module, __name__), name)
    globals()[name] = value
    return value


def version() -> int:
    """Version number of the loaded library, as PQlibVersion reports it."""
    from . import _ffi

    return int(_ffi.get().PQlibVersion())


def has_function(name: str) -> bool:
    """Tell whether the loaded library exports a function (libpq or libspq name)."""
    from . import _ffi

    if name.startswith(("SPQ", "spg_")):
        name = name[1:]
    return _ffi.get().has(name)
```

The five classes are resolved on first access (PEP 562), so this file is complete before
their modules exist and no later part edits it: `from stuntgres.pq import PGconn` works
as soon as `_pgconn.py` is there, and raises `ImportError` before. `version()` and
`has_function()` load the library; importing the package does not. `has_function` takes
the name of a row of 3.4 in either spelling (`"PQclosePrepared"`, `"SPQclosePrepared"`,
`"SPQXreset"`); an unknown name gives `False`.

### 5.2 `Conninfo` (`stuntgres/pq/_conninfo.py`)

```python
class Conninfo:
    @classmethod
    def get_defaults(cls) -> list[ConninfoOption]: ...
    @classmethod
    def parse(cls, conninfo: bytes) -> list[ConninfoOption]: ...
    @classmethod
    def _options_from_array(cls, opts: Any) -> list[ConninfoOption]: ...
```

| Member | Algorithm | Raises |
|---|---|---|
| `get_defaults()` | `lib = _ffi.get()`; `opts = lib.PQconndefaults()`; if `not opts` raise; `try: return cls._options_from_array(opts)` `finally: lib.PQconninfoFree(opts)`. | `MemoryError("PQconndefaults failed")` |
| `parse(conninfo)` | `need_bytes(conninfo)`; `lib = _ffi.get()`; `errmsg = c_void_p()`; `opts = lib.PQconninfoParse(conninfo, byref(errmsg))`. If `not opts`: if `not errmsg.value` raise `MemoryError("PQconninfoParse failed")`; else `try: text = string_at(errmsg.value).decode("utf-8", "replace")` `finally: lib.PQfreemem(errmsg)`; raise `e.OperationalError(text.rstrip("\n"))`. Otherwise `try: return cls._options_from_array(opts)` `finally: lib.PQconninfoFree(opts)`. | `TypeError`, `MemoryError`, `OperationalError` with libpq's text |
| `_options_from_array(opts)` | `opts` is an `OPTS` pointer. For `index = 0, 1, …`: `opt = opts[index]`; stop when `not opt.keyword`; append `ConninfoOption(opt.keyword, opt.envvar, opt.compiled, opt.val, opt.label, opt.dispchar, opt.dispsize)`. Does not free. | — |

Memory: the array belongs to the caller and is freed with `PQconninfoFree`; the message
of a failed parse is freed with `PQfreemem`. The structure fields are `c_char_p`, so each
access already copied the text into `bytes`.

### 5.3 `PGconnBase` (`stuntgres/pq/_pgconn_base.py`)

```python
class PGconnBase:
    __slots__ = ("_lib", "_ptr", "_notice_key", "notice_handler", "notify_handler",
                 "__weakref__")

    notice_handler: Callable[[PGresult], None] | None
    notify_handler: Callable[[PGnotify], None] | None

    def __init__(self, lib: Any, ptr: int) -> None: ...
    def __del__(self) -> None: ...
    def __repr__(self) -> str: ...
    def _ensure(self) -> int: ...
    def finish(self) -> None: ...
    @property
    def pgconn_ptr(self) -> int | None: ...
```

The module imports `os`, `typing` names, `from .. import errors as e`, `from . import
_notices` and `from .misc import PGnotify`; `PGresult` is named only in annotations
(under `TYPE_CHECKING`).

| Member | Algorithm |
|---|---|
| `__init__(lib, ptr)` | Private: objects are made by the classmethods of 5.4. `self._lib = lib`; `self._ptr = ptr`; `self.notice_handler = None`; `self.notify_handler = None`; `self._notice_key = _notices.register(self)` (last, the pointer must be set). |
| `__del__()` | `if self._ptr is not None and os.getpid() == self._lib.pid: self.finish()`. |
| `__repr__()` | `f"<stuntgres.pq.PGconn at 0x{id(self):x}>"`. |
| `_ensure()` | If `self._ptr is None` raise `e.OperationalError("the connection is closed")`; return `self._ptr`. |
| `finish()` | `ptr, self._ptr = self._ptr, None`; if `ptr is not None`: `_notices.unregister(self._notice_key)`; `self._lib.PQfinish(ptr)`. Idempotent. Closes the descriptor: a caller that registered it in an event loop removes it first (`docs/LIBSPQ.md` 5.3). |
| `pgconn_ptr` | `self._ptr`. |

Each mixin of 5.7 to 5.13 is `class <Name>Mixin(PGconnBase)` with `__slots__ = ()`, in a
module of its own that imports `PGconnBase` from `._pgconn_base`. Because only
`PGconnBase` has slots, the composed class has one instance layout.

### 5.4 `PGconn` (`stuntgres/pq/_pgconn.py`)

```python
class PGconn(PreparedMixin, RowModesMixin, CancelMixin, NotifyMixin, CopyMixin,
             ExecMixin):
    __slots__ = ()

    @classmethod
    def connect(cls, conninfo: bytes) -> PGconn: ...
    @classmethod
    def connect_start(cls, conninfo: bytes) -> PGconn: ...
    def connect_poll(self) -> int: ...
    def reset(self) -> None: ...
    @classmethod
    def ping(cls, conninfo: bytes) -> int: ...
    @property
    def info(self) -> list[ConninfoOption]: ...
    @property
    def db(self) -> bytes: ...
    @property
    def user(self) -> bytes: ...
    @property
    def password(self) -> bytes: ...
    @property
    def host(self) -> bytes: ...
    @property
    def port(self) -> bytes: ...
    @property
    def options(self) -> bytes: ...
    @property
    def status(self) -> int: ...
    @property
    def transaction_status(self) -> int: ...
    def parameter_status(self, name: bytes) -> bytes | None: ...
    @property
    def error_message(self) -> bytes: ...
    def get_error_message(self, encoding: str = "utf-8") -> str: ...
    @property
    def _encoding(self) -> str: ...
    @property
    def protocol_version(self) -> int: ...
    @property
    def server_version(self) -> int: ...
    @property
    def socket(self) -> int: ...
    @property
    def backend_pid(self) -> int: ...
```

The bases are written in exactly this order. The module imports the six mixins from their
modules, `Conninfo` from `._conninfo`, `ConnStatus` and `TransactionStatus` from
`._enums`, `ConninfoOption`, `error_message` and `need_bytes` from `.misc`, `_ffi`, `python_codec` from `.._encodings` and `errors as e`.

| Member | Algorithm | Raises |
|---|---|---|
| `connect(conninfo)` | `need_bytes(conninfo)`; `lib = _ffi.get()`; `ptr = lib.PQconnectdb(conninfo)`; if `not ptr` raise; return `cls(lib, ptr)`. Blocking. A failed attempt still returns an object (status `BAD`, message in `error_message`); the caller checks `status`. | `TypeError`; `MemoryError("PQconnectdb failed")`; `InterfaceError` from the loader |
| `connect_start(conninfo)` | The same with `lib.PQconnectStart`; `MemoryError("PQconnectStart failed")`. Nonblocking; continue with `connect_poll` (generator 7.2). | as above |
| `connect_poll()` | `return self._lib.PQconnectPoll(self._ensure())` (a `PollingStatus` value). | closed |
| `reset()` | `self._lib.PQreset(self._ensure())`. Blocking; the caller checks `status`. | closed |
| `ping(conninfo)` | `need_bytes(conninfo)`; `return _ffi.get().PQping(conninfo)` (a `Ping` value). | `TypeError` |
| `info` | `opts = self._lib.PQconninfo(self._ensure())`; if `not opts` raise `MemoryError("PQconninfo failed")`; `try: return Conninfo._options_from_array(opts)` `finally: self._lib.PQconninfoFree(opts)`. | closed, `MemoryError` |
| `db`, `user`, `password`, `host`, `port`, `options` | `value = self._lib.<F>(self._ensure())` with `<F>` = `PQdb`, `PQuser`, `PQpass`, `PQhost`, `PQport`, `PQoptions`; `return value or b""` (NULL becomes `b""`). Write one private helper `_string(self, name: str) -> bytes` that does `getattr(self._lib, name)(self._ensure()) or b""`. | closed |
| `status` | If closed return `ConnStatus.BAD`; else `self._lib.PQstatus(self._ptr)`. Never raises. | — |
| `transaction_status` | If closed return `TransactionStatus.UNKNOWN`; else `self._lib.PQtransactionStatus(self._ptr)`. Never raises. | — |
| `parameter_status(name)` | `return self._lib.PQparameterStatus(self._ensure(), need_bytes(name))` (`None` when the server did not report it). | closed, `TypeError` |
| `error_message` | If closed return `b""`; else `self._lib.PQerrorMessage(self._ptr) or b""`. Never raises. | — |
| `get_error_message(encoding)` | `return error_message(self, encoding)`. | — |
| `_encoding` | `name = None`; if `self.status == ConnStatus.OK`: `name = self.parameter_status(b"client_encoding")`; return `python_codec(name)` if `name` else `"utf-8"`. | `NotSupportedError` from `python_codec` |
| `protocol_version`, `server_version`, `backend_pid` | `self._lib.PQprotocolVersion`, `PQserverVersion`, `PQbackendPID` of `self._ensure()`. | closed |
| `socket` | `rv = self._lib.PQsocket(self._ensure())`; if `rv == -1` raise `e.OperationalError("the connection is lost")`; return `rv`. | closed, lost |

`finish`, `pgconn_ptr`, `notice_handler` and `notify_handler` come from `PGconnBase`.

Memory: the object owns the `SPGconn` and frees it in `finish`. Strings returned by the
status functions belong to the connection; `S` results are copied at once, so nothing
dangles.

### 5.5 `PGresult` (`stuntgres/pq/_pgresult.py`)

```python
class PGresult:
    __slots__ = ("_lib", "_ptr", "_owned", "__weakref__")

    def __init__(self, lib: Any, ptr: int, owned: bool = True) -> None: ...
    def __del__(self) -> None: ...
    def __repr__(self) -> str: ...
    @property
    def pgresult_ptr(self) -> int | None: ...
    def clear(self) -> None: ...
    @property
    def status(self) -> int: ...
    @property
    def error_message(self) -> bytes: ...
    def get_error_message(self, encoding: str = "utf-8") -> str: ...
    def error_field(self, fieldcode: int) -> bytes | None: ...
    @property
    def ntuples(self) -> int: ...
    @property
    def nfields(self) -> int: ...
    @property
    def binary_tuples(self) -> int: ...
    @property
    def nparams(self) -> int: ...
    @property
    def command_status(self) -> bytes | None: ...
    @property
    def command_tuples(self) -> int | None: ...
    @property
    def oid_value(self) -> int: ...
    def fname(self, column_number: int) -> bytes | None: ...
    def ftable(self, column_number: int) -> int: ...
    def ftablecol(self, column_number: int) -> int: ...
    def fformat(self, column_number: int) -> int: ...
    def ftype(self, column_number: int) -> int: ...
    def fmod(self, column_number: int) -> int: ...
    def fsize(self, column_number: int) -> int: ...
    def param_type(self, param_number: int) -> int: ...
    def get_value(self, row_number: int, column_number: int) -> bytes | None: ...
    def get_rows(self, first: int, last: int) -> list[tuple[bytes | None, ...]]: ...
```

`_ptr` may be `None` (cleared). The library functions accept a NULL result and answer
with the value libpq documents for it, so members without a guard below pass `self._ptr`
as it is: `status` of a cleared result is `ExecStatus.FATAL_ERROR` (7), `ntuples`,
`nfields`, `binary_tuples`, `nparams` and `oid_value` are 0.

| Member | Algorithm |
|---|---|
| `__init__(lib, ptr, owned=True)` | Private. Store the three values. `owned=False` marks a result that libpq frees itself (the notice receiver, 5.9). |
| `__del__()` | `if self._owned and self._ptr is not None and os.getpid() == self._lib.pid: self.clear()`. |
| `__repr__()` | `f"<stuntgres.pq.PGresult [{name}] at 0x{id(self):x}>"`, `name` = `ExecStatus(self.status).name`, or `f"status {self.status}"` when the value is no member. |
| `pgresult_ptr` | `self._ptr`. |
| `clear()` | `ptr, self._ptr = self._ptr, None`; `if ptr is not None and self._owned: self._lib.PQclear(ptr)`. Idempotent. |
| `status` | `self._lib.PQresultStatus(self._ptr)`. |
| `error_message` | If cleared `b""`; else `self._lib.PQresultErrorMessage(self._ptr) or b""`. |
| `get_error_message(encoding)` | `error_message(self, encoding)`. |
| `error_field(fieldcode)` | If cleared `None`; else `self._lib.PQresultErrorField(self._ptr, fieldcode)`. |
| `ntuples`, `nfields`, `binary_tuples`, `nparams`, `oid_value` | `PQntuples`, `PQnfields`, `PQbinaryTuples`, `PQnparams`, `PQoidValue` of `self._ptr`. |
| `command_status` | If cleared `None`; else `self._lib.PQcmdStatus(self._ptr)`. |
| `command_tuples` | If cleared `None`; `text = self._lib.PQcmdTuples(self._ptr)`; `int(text) if text else None`. |
| `_column(column_number) -> bool` | Private helper: `self._ptr is not None and 0 <= column_number < self.nfields`. |
| `fname(n)` | `None` unless `self._column(n)`; else `self._lib.PQfname(self._ptr, n)`. |
| `ftable(n)`, `ftablecol(n)`, `fformat(n)`, `ftype(n)`, `fmod(n)`, `fsize(n)` | `0` unless `self._column(n)`; else `PQftable`, `PQftablecol`, `PQfformat`, `PQftype`, `PQfmod`, `PQfsize` of `(self._ptr, n)`. |
| `param_type(n)` | `0` when cleared or not `0 <= n < self.nparams`; else `self._lib.PQparamtype(self._ptr, n)`. |
| `get_value(row, col)` | `None` unless `self._column(col)` and `0 <= row < self.ntuples`. `length = lib.PQgetlength(ptr, row, col)`; if `length`: `return string_at(lib.PQgetvalue(ptr, row, col), length)`; if `lib.PQgetisnull(ptr, row, col)`: `return None`; `return b""`. |
| `get_rows(first, last)` | If cleared `[]`. `first = max(first, 0)`; `last = min(last, self.ntuples)`; `return _rows.read_rows(self._lib, self._ptr, first, last, self.nfields)`. |

The range checks are done in Python on purpose: libpq answers an out-of-range request by
calling the notice receiver, and the accessors hold the GIL (3.4).

Memory: an owned result is freed by `clear()` or the finalizer; it stays valid after
`finish()` of its connection (`docs/LIBSPQ.md` 5.8). Values are copied out with
`string_at`, so `bytes` objects never point into the result.

### 5.6 Bulk read (`stuntgres/pq/_rows.py`)

```python
def read_rows(
    lib: Any, res: int, first: int, last: int, nfields: int
) -> list[tuple[bytes | None, ...]]: ...
```

Rows `first` to `last - 1` of the result at address `res`, each a tuple of `nfields`
cells: `bytes` for a value (text or binary, exactly `PQgetlength` bytes), `None` for SQL
NULL. The caller guarantees `0 <= first`, `last <= ntuples` and a live result; the
function checks nothing.

1. Bind to locals: `getvalue = lib.PQgetvalue`, `getlength = lib.PQgetlength`,
   `getisnull = lib.PQgetisnull`, `columns = range(nfields)`; `rows = []`.
2. For `row` in `range(first, last)`: `cells = []`; for `col` in `columns`:
   `length = getlength(res, row, col)`; if `length`: append
   `string_at(getvalue(res, row, col), length)`; elif `getisnull(res, row, col)`: append
   `None`; else append `b""`. Then `rows.append(tuple(cells))`.
3. Return `rows`.

Two calls for a non-empty cell, two for an empty or NULL one **(measured: 20000 calls for
10000 cells, 0.6 to 0.9 µs per cell)**. This module is the seam of plan U2: a compiled reader
replaces the body of `read_rows` (or the module) and keeps the signature; `res` is the
address of the `SPGresult`, which a C function can use directly. Nothing but
`PGresult.get_rows` may call it, and upper layers read cells only through
`PGresult.get_rows` or `get_value`.

### 5.7 Execution (`stuntgres/pq/_params.py`, `stuntgres/pq/_pgconn_exec.py`)

`_params.py`:

```python
Buffer = Union[bytes, bytearray, memoryview]

def values_arrays(
    param_values: Sequence[Buffer | None] | None, param_formats: Sequence[int] | None
) -> tuple[int, Any, Any, Any]: ...
def types_array(param_types: Sequence[int] | None, count: int | None) -> Any: ...
def query_params(
    command: bytes,
    param_values: Sequence[Buffer | None] | None,
    param_types: Sequence[int] | None,
    param_formats: Sequence[int] | None,
    result_format: int,
) -> tuple[Any, ...]: ...
def prepare_params(
    name: bytes, command: bytes, param_types: Sequence[int] | None
) -> tuple[Any, ...]: ...
def prepared_params(
    name: bytes,
    param_values: Sequence[Buffer | None] | None,
    param_formats: Sequence[int] | None,
    result_format: int,
) -> tuple[Any, ...]: ...
```

| Function | Algorithm |
|---|---|
| `values_arrays(param_values, param_formats)` | `n = len(param_values) if param_values else 0`. If `param_formats is not None and len(param_formats) != n` raise `ValueError(f"got {n} param_values but {len(param_formats)} param_formats")`. If `n == 0` return `(0, None, None, None)`. `raw = [v if v is None or isinstance(v, bytes) else bytes(v) for v in param_values]`; `avalues = (c_char_p * n)(*raw)`; `alengths = (c_int * n)(*[0 if v is None else len(v) for v in raw])`; `aformats = (c_int * n)(*param_formats) if param_formats is not None else None`; return `(n, avalues, alengths, aformats)`. A `None` value is SQL NULL. Lengths are always passed (libpq needs them for binary values and ignores them for text). |
| `types_array(param_types, count)` | `None` -> `None`. If `count is not None and len(param_types) != count` raise `ValueError(f"got {count} param_values but {len(param_types)} param_types")`. Empty -> `None`. Else `(c_uint * len(param_types))(*param_types)`. |
| `query_params(...)` | `need_bytes(command)`; `n, avalues, alengths, aformats = values_arrays(param_values, param_formats)`; `atypes = types_array(param_types, n)`; return `(command, n, atypes, avalues, alengths, aformats, result_format)`: the arguments of `PQexecParams` and `PQsendQueryParams` after the connection. |
| `prepare_params(name, command, param_types)` | `need_bytes` both; `atypes = types_array(param_types, None)`; return `(name, command, len(param_types) if param_types else 0, atypes)`. |
| `prepared_params(...)` | `need_bytes(name)`; `n, avalues, alengths, aformats = values_arrays(param_values, param_formats)`; return `(name, n, avalues, alengths, aformats, result_format)`. |

The ctypes arrays keep the `bytes` objects alive for the duration of the call; libpq
copies what it needs before returning.

`_pgconn_exec.py`:

```python
def make_result(conn: PGconnBase, method: str, ptr: int | None) -> PGresult: ...
def check_sent(conn: PGconnBase, method: str, rv: int) -> None: ...

class ExecMixin(PGconnBase):
    __slots__ = ()

    def exec_(self, command: bytes) -> PGresult: ...
    def send_query(self, command: bytes) -> None: ...
    def exec_params(
        self,
        command: bytes,
        param_values: Sequence[Buffer | None] | None,
        param_types: Sequence[int] | None = None,
        param_formats: Sequence[int] | None = None,
        result_format: int = Format.TEXT,
    ) -> PGresult: ...
    def send_query_params(
        self,
        command: bytes,
        param_values: Sequence[Buffer | None] | None,
        param_types: Sequence[int] | None = None,
        param_formats: Sequence[int] | None = None,
        result_format: int = Format.TEXT,
    ) -> None: ...
    def get_result(self) -> PGresult | None: ...
    def consume_input(self) -> None: ...
    def is_busy(self) -> int: ...
    @property
    def nonblocking(self) -> int: ...
    @nonblocking.setter
    def nonblocking(self, arg: int) -> None: ...
    def flush(self) -> int: ...
```

| Member | Algorithm | Raises |
|---|---|---|
| `make_result(conn, method, ptr)` | If `not ptr` raise `e.OperationalError(f"{method} failed: {error_message(conn)}")`; return `PGresult(conn._lib, ptr)`. | |
| `check_sent(conn, method, rv)` | If `not rv` raise the same error. | |
| `exec_(command)` | `make_result(self, "exec_", self._lib.PQexec(self._ensure(), need_bytes(command)))`. Blocking; returns the last result of a multi-statement string. | closed, `TypeError`, fails as `exec_` |
| `send_query(command)` | `check_sent(self, "send_query", self._lib.PQsendQuery(self._ensure(), need_bytes(command)))`. | closed, `TypeError`, fails as `send_query` |
| `exec_params(...)` | `args = query_params(command, param_values, param_types, param_formats, result_format)`; `make_result(self, "exec_params", self._lib.PQexecParams(self._ensure(), *args))`. | plus `ValueError` |
| `send_query_params(...)` | Same `args`; `check_sent(self, "send_query_params", self._lib.PQsendQueryParams(self._ensure(), *args))`. | plus `ValueError` |
| `get_result()` | `ptr = self._lib.PQgetResult(self._ensure())`; `PGresult(self._lib, ptr) if ptr else None`. Blocks while `is_busy()` is non-zero. | closed |
| `consume_input()` | If `self._lib.PQconsumeInput(self._ensure()) != 1` raise, failing as `consume_input`. | closed, fails |
| `is_busy()` | `self._lib.PQisBusy(self._ensure())`. | closed |
| `nonblocking` (get) | `self._lib.PQisnonblocking(self._ensure())`. | closed |
| `nonblocking` (set) | If `self._lib.PQsetnonblocking(self._ensure(), arg) < 0` raise, failing as `nonblocking`. | closed, fails |
| `flush()` | `rv = self._lib.PQflush(self._ensure())`; if `rv < 0` raise, failing as `flush`; return `rv` (0 done, 1 data still queued). | closed, fails |

An error reported by the server is not an exception at this level: it is a result with
status `FATAL_ERROR`. Exceptions mean the call itself could not be made.

### 5.8 Prepared statements (`stuntgres/pq/_pgconn_prepared.py`)

```python
class PreparedMixin(PGconnBase):
    __slots__ = ()

    def prepare(
        self, name: bytes, command: bytes, param_types: Sequence[int] | None = None
    ) -> PGresult: ...
    def send_prepare(
        self, name: bytes, command: bytes, param_types: Sequence[int] | None = None
    ) -> None: ...
    def exec_prepared(
        self,
        name: bytes,
        param_values: Sequence[Buffer | None] | None,
        param_formats: Sequence[int] | None = None,
        result_format: int = Format.TEXT,
    ) -> PGresult: ...
    def send_query_prepared(
        self,
        name: bytes,
        param_values: Sequence[Buffer | None] | None,
        param_formats: Sequence[int] | None = None,
        result_format: int = Format.TEXT,
    ) -> None: ...
    def describe_prepared(self, name: bytes) -> PGresult: ...
    def send_describe_prepared(self, name: bytes) -> None: ...
    def describe_portal(self, name: bytes) -> PGresult: ...
    def send_describe_portal(self, name: bytes) -> None: ...
    def close_prepared(self, name: bytes) -> PGresult: ...
    def send_close_prepared(self, name: bytes) -> None: ...
    def close_portal(self, name: bytes) -> PGresult: ...
    def send_close_portal(self, name: bytes) -> None: ...
```

It imports `make_result` and `check_sent` from `._pgconn_exec` and the helpers of
`._params`. Every member follows one of two shapes, with `<method>` the member's own name:

- blocking: `make_result(self, "<method>", self._lib.<F>(self._ensure(), *args))`;
- send: `check_sent(self, "<method>", self._lib.<F>(self._ensure(), *args))`.

| Member | `<F>` | `args` |
|---|---|---|
| `prepare` | `PQprepare` | `prepare_params(name, command, param_types)` |
| `send_prepare` | `PQsendPrepare` | the same |
| `exec_prepared` | `PQexecPrepared` | `prepared_params(name, param_values, param_formats, result_format)` |
| `send_query_prepared` | `PQsendQueryPrepared` | the same |
| `describe_prepared` / `send_describe_prepared` | `PQdescribePrepared` / `PQsendDescribePrepared` | `(need_bytes(name),)` |
| `describe_portal` / `send_describe_portal` | `PQdescribePortal` / `PQsendDescribePortal` | `(need_bytes(name),)` |
| `close_prepared` / `send_close_prepared` | `PQclosePrepared` / `PQsendClosePrepared` (optional rows) | `(need_bytes(name),)` |
| `close_portal` / `send_close_portal` | `PQclosePortal` / `PQsendClosePortal` (optional rows) | `(need_bytes(name),)` |

With a library that lacks the four close functions the call raises
`NotSupportedError` from the stub of 3.2 (after `_ensure` and `need_bytes` have run).

### 5.9 Notices (`stuntgres/pq/_notices.py`)

```python
logger = logging.getLogger("stuntgres")
receiver: Any                      # the one NoticeReceiver instance
def register(conn: Any) -> int: ...
def unregister(key: int) -> None: ...
```

Module state: `_keys = itertools.count(1)`, `_connections: dict[int, weakref.ref[Any]] =
{}`, `receiver = _ffi.NoticeReceiver(_receive)`. Creating the callback object needs no
native library.

`register(conn)`: `key = next(_keys)`; `_connections[key] = weakref.ref(conn)`;
`conn._lib.PQsetNoticeReceiver(conn._ptr, receiver, key)`; return `key`. The key is passed
as the `void *arg` of the receiver: an integer, not an address, so a late call can never
follow a dangling pointer.

`unregister(key)`: `_connections.pop(key, None)`.

`_receive(arg: int | None, result_ptr: int | None) -> None`, the whole body inside
`try:` / `except BaseException as exc:`:

1. `ref = _connections.get(arg or 0)`; `conn = ref() if ref is not None else None`; if
   `conn is None or result_ptr is None`: return.
2. `handler = conn.notice_handler`; if `handler is None`: return.
3. `res = PGresult(conn._lib, result_ptr, owned=False)`.
4. `try: handler(res)` `finally: res._ptr = None` (libpq frees the result when the receiver
   returns; a handler that keeps `res` finds it cleared).
5. In the `except BaseException` branch:
   `logger.error("error in notice handler: %s", exc, exc_info=exc)`, itself wrapped in
   `try`/`except BaseException: pass`. Nothing is re-raised: no exception leaves the
   callback (`docs/LIBSPQ.md` section 7 rule 7).

Consequences **(all measured, experiment 5)**:

- A handler that raises `ValueError("boom")` produces one log record (logger `stuntgres`,
  level `ERROR`, message `error in notice handler: boom`, `exc_info` set) and the call
  that delivered the notice returns its normal result.
- The callback does not keep a connection alive: the registry holds a weak reference and
  `finish()` removes the entry; a connection that is dropped without `finish()` is
  collected, finished by `__del__`, and its key disappears.
- A result that outlives its connection still carries the receiver and the key (libpq
  copies the hooks into every result). A notice raised through it afterwards finds no
  entry and is dropped.
- The handler runs on the thread that is inside the library call, with the GIL held; it
  may use other connections, not its own (`docs/LIBSPQ.md` 5.6).
- A `KeyboardInterrupt` or `SystemExit` raised inside a handler is logged and dropped
  like any other exception; it cannot be carried through C. CPYTHON-DESIGN-DBAPI decides
  whether its own handler defers user code to after the library call.
- A connection without a handler drops notices silently (libpq's default would write
  them to `stderr`; the receiver replaces that default from the moment the object exists).
  Notices raised while `connect` runs inside libspq go to `stderr`
  (`docs/design/LIBSPQ-CABI.md` 7.3); with libpq a receiver installed after
  `connect_start` sees them.

### 5.10 Notifications (`stuntgres/pq/_pgconn_notify.py`)

```python
class NotifyMixin(PGconnBase):
    __slots__ = ()

    def notifies(self) -> PGnotify | None: ...
```

`notifies()`: `ptr = self._lib.PQnotifies(self._ensure())`; if `not ptr` return `None`;
`try: c = ptr.contents; return PGnotify(c.relname, c.be_pid, c.extra)` `finally:
self._lib.PQfreemem(ptr)`. `ptr` is a `POINTER(PGnotify_struct)`; reading the `c_char_p`
fields copies them before the block is freed. It does not read from the connection: call
`consume_input()` first (or any blocking call). `notify_handler` is not called here; the
generators call it (7.7).

### 5.11 Cancel (`stuntgres/pq/_pgcancel.py`, `stuntgres/pq/_pgconn_cancel.py`)

```python
class PGcancel:
    __slots__ = ("_lib", "_ptr", "__weakref__")

    def __init__(self, lib: Any, ptr: int) -> None: ...
    def __del__(self) -> None: ...
    def free(self) -> None: ...
    def cancel(self) -> None: ...

class CancelMixin(PGconnBase):
    __slots__ = ()

    def get_cancel(self) -> PGcancel: ...
```

| Member | Algorithm | Raises |
|---|---|---|
| `PGcancel.__init__(lib, ptr)` | Private; store both. | |
| `PGcancel.__del__()` | `if self._ptr is not None and os.getpid() == self._lib.pid: self.free()`. | |
| `PGcancel.free()` | `ptr, self._ptr = self._ptr, None`; `if ptr is not None: self._lib.PQfreeCancel(ptr)`. Idempotent. | |
| `PGcancel.cancel()` | If freed raise `e.OperationalError("the cancel object is freed")`. `buf = create_string_buffer(256)`; if `not self._lib.PQcancel(self._ptr, buf, len(buf))`: raise `e.OperationalError(f"cancel failed: {text}")` with `text = buf.value.decode("utf-8", "replace").rstrip("\n")`. | `OperationalError` |
| `CancelMixin.get_cancel()` | `ptr = self._lib.PQgetCancel(self._ensure())`; if `not ptr` raise `e.OperationalError("get_cancel failed")`; return `PGcancel(self._lib, ptr)`. | closed, fails |

`cancel()` may be called from any thread while another thread uses the connection
(`docs/LIBSPQ.md` section 7 rule 5). With libspq it never blocks; with a real libpq it
opens a second connection to the server and returns when the server has closed it
**(measured: 1 to 3 ms against a local server)**. A cancel that arrives when nothing runs
has no effect. The object stays usable after `finish()` of its connection.
`cancel_conn()` of psycopg's abc is not defined (5.16).

### 5.12 COPY (`stuntgres/pq/_pgconn_copy.py`)

```python
class CopyMixin(PGconnBase):
    __slots__ = ()

    def put_copy_data(self, buffer: Buffer) -> int: ...
    def put_copy_end(self, error: bytes | None = None) -> int: ...
    def get_copy_data(self, async_: int) -> tuple[int, memoryview]: ...
```

| Member | Algorithm | Raises |
|---|---|---|
| `put_copy_data(buffer)` | `raw = buffer if isinstance(buffer, bytes) else bytes(buffer)`; `rv = self._lib.PQputCopyData(self._ensure(), raw, len(raw))`; if `rv < 0` raise, failing as `put_copy_data`; return `rv` (1 queued; 0 would block, libpq in nonblocking mode only). | closed, fails |
| `put_copy_end(error=None)` | `rv = self._lib.PQputCopyEnd(self._ensure(), error)`; if `rv < 0` raise, failing as `put_copy_end`; return `rv`. `error` not `None` makes the server abort the COPY with that text. | closed, fails |
| `get_copy_data(async_)` | `buf = c_void_p()`; `n = self._lib.PQgetCopyData(self._ensure(), byref(buf), async_)`; if `n == -2` raise, failing as `get_copy_data`; if `not buf.value` return `(n, memoryview(b""))`; else `try: return n, memoryview(string_at(buf.value, n))` `finally: self._lib.PQfreemem(buf)`. `n > 0`: one row of `n` bytes; `0`: nothing ready yet (`async_` non-zero only); `-1`: the COPY is over, take the result with `get_result()`. | closed, fails |

The COPY state machine above these three calls (sync and async) belongs to
CPYTHON-DESIGN-FEATURES.

### 5.13 Row modes (`stuntgres/pq/_pgconn_rowmodes.py`)

```python
class RowModesMixin(PGconnBase):
    __slots__ = ()

    def set_single_row_mode(self) -> None: ...
    def set_chunked_rows_mode(self, size: int) -> None: ...
```

`set_single_row_mode()`: if `not self._lib.PQsetSingleRowMode(self._ensure())` raise
`e.OperationalError("set_single_row_mode failed")`.
`set_chunked_rows_mode(size)`: if `not self._lib.PQsetChunkedRowsMode(self._ensure(),
size)` raise `e.OperationalError("set_chunked_rows_mode failed")`; the row is optional, so
an older libpq raises `NotSupportedError`. Both are valid only right after a `send_…`
call and before the first `get_result()`. libpq leaves no message for this failure,
hence the fixed texts.

### 5.14 `Escaping` (`stuntgres/pq/_escaping.py`)

```python
Buffer = Union[bytes, bytearray, memoryview]

class Escaping:
    def __init__(self, conn: PGconn | None = None) -> None: ...
    def escape_literal(self, data: Buffer) -> bytes: ...
    def escape_identifier(self, data: Buffer) -> bytes: ...
    def escape_string(self, data: Buffer) -> bytes: ...
    def escape_bytea(self, data: Buffer) -> bytes: ...
    def unescape_bytea(self, data: Buffer) -> bytes: ...
```

`__init__` stores `self.conn = conn` and loads nothing. In every method `raw =
bytes(data)`; "the connection pointer" is `self.conn._ensure()` (a closed connection
raises `OperationalError("the connection is closed")`).

| Member | Algorithm | Raises |
|---|---|---|
| `escape_literal(data)` | If `self.conn is None` raise `e.OperationalError("escape_literal failed: no connection provided")`. `lib = self.conn._lib`; `out = lib.PQescapeLiteral(ptr, raw, len(raw))`; if `not out` raise `e.OperationalError(f"escape_literal failed: {error_message(self.conn)}")`; `try: return string_at(out)` `finally: lib.PQfreemem(out)`. | as stated |
| `escape_identifier(data)` | The same with `PQescapeIdentifier` and the word `escape_identifier`. Write both through one private helper `_escape_conn(self, func: str, method: str, data: Buffer) -> bytes`. | as stated |
| `escape_string(data)` | `out = create_string_buffer(len(raw) * 2 + 1)`. With a connection: `error = c_int()`; `n = lib.PQescapeStringConn(ptr, out, raw, len(raw), byref(error))`; if `error.value` raise `e.OperationalError(f"escape_string failed: {error_message(self.conn)}")`. Without: `n = _ffi.get().PQescapeString(out, raw, len(raw))`. Return `out.raw[:n]`. | as stated |
| `escape_bytea(data)` | `size = c_size_t()`. With a connection: `lib = self.conn._lib`; `out = lib.PQescapeByteaConn(ptr, raw, len(raw), byref(size))`. Without: `lib = _ffi.get()`; `out = lib.PQescapeBytea(raw, len(raw), byref(size))`. If `not out` raise `MemoryError("escape_bytea failed")`. `try: return string_at(out, size.value - 1)` `finally: lib.PQfreemem(out)` (`size` counts the terminating NUL). | `MemoryError` |
| `unescape_bytea(data)` | `lib = self.conn._lib if self.conn is not None else _ffi.get()`; `size = c_size_t()`; `out = lib.PQunescapeBytea(raw, byref(size))`; if `not out` raise `MemoryError("unescape_bytea failed")`; `try: return string_at(out, size.value)` `finally: lib.PQfreemem(out)`. | `MemoryError` |

### 5.15 Extensions (`stuntgres/pq/ext.py`, native mode)

```python
def version() -> bytes: ...
def cluster_drop(host: bytes | None = None, port: bytes | None = None) -> None: ...
def reset(pgconn: PGconn) -> None: ...
def dump(pgconn: PGconn) -> bytes: ...
def restore(pgconn: PGconn, data: Buffer) -> None: ...
```

Wrappers of the five functions of `docs/design/LIBSPQ-CABI.md` 3.2. Their rows are
optional: with a real libpq loaded each call raises `NotSupportedError` from the stub of
3.2. The module is imported as `from stuntgres.pq import ext`; it is not listed in
`pq/__init__.py`. The cluster-control API that applications see belongs to
CPYTHON-DESIGN-FEATURES and is built on these functions.

| Function | Algorithm | Raises |
|---|---|---|
| `version()` | `return _ffi.get().PQXversion()` (a static string, for example `b"0.1.0"`). | `NotSupportedError` |
| `cluster_drop(host, port)` | `lib = _ffi.get()`; `buf = create_string_buffer(256)`; if `not lib.PQXclusterDrop(host, port, buf, len(buf))` raise `e.OperationalError(f"cluster_drop failed: {text}")`, `text = buf.value.decode("utf-8", "replace")`. `None` means `localhost` and `5432`. Dropping a cluster that does not exist succeeds. | `OperationalError` (for example while the cluster has open connections) |
| `reset(pgconn)` | If `not pgconn._lib.PQXreset(pgconn._ensure())` raise `e.OperationalError(f"reset failed: {error_message(pgconn)}")`. | closed, `OperationalError` |
| `dump(pgconn)` | `buf = c_void_p()`; `size = c_size_t()`; if `not lib.PQXdump(ptr, byref(buf), byref(size))` raise `e.OperationalError(f"dump failed: {error_message(pgconn)}")`; `try: return string_at(buf.value, size.value)` `finally: lib.PQfreemem(buf)`. | closed, `OperationalError` |
| `restore(pgconn, data)` | `raw = bytes(data)`; if `not lib.PQXrestore(ptr, raw, len(raw))` raise `e.OperationalError(f"restore failed: {error_message(pgconn)}")`. | closed, `OperationalError` |

In `reset`, `dump` and `restore`, `lib = pgconn._lib` and `ptr = pgconn._ensure()`.

### 5.16 Correspondence with `psycopg/pq/abc.py`

"Same" means: same name, argument order, defaults and return type as the Protocol member.
Differences are spelled out. The last block lists what `stuntgres.pq` adds.

`PGconn`:

| abc member | libspq function | Defined | Note |
|---|---|---|---|
| `notice_handler`, `notify_handler` | `SPQsetNoticeReceiver`, `SPQnotifies` | yes | 5.3, 5.9, 7.7 |
| `connect`, `connect_start`, `connect_poll` | `SPQconnectdb`, `SPQconnectStart`, `SPQconnectPoll` | yes | same |
| `finish` | `SPQfinish` | yes | same |
| `info` | `SPQconninfo` | yes | same |
| `reset` | `SPQreset` | yes | same |
| `reset_start`, `reset_poll` | — | no | `PQresetStart` / `PQresetPoll` are absent from libspq |
| `ping` | `SPQping` | yes | same (a classmethod) |
| `db`, `user`, `password`, `host`, `port`, `options` | `SPQdb`, `SPQuser`, `SPQpass`, `SPQhost`, `SPQport`, `SPQoptions` | yes | same |
| `hostaddr`, `tty` | — | no | `PQhostaddr`, `PQtty` absent |
| `status`, `transaction_status` | `SPQstatus`, `SPQtransactionStatus` | yes | same; never raise |
| `parameter_status` | `SPQparameterStatus` | yes | same |
| `error_message`, `get_error_message` | `SPQerrorMessage` | yes | `get_error_message(encoding="utf-8")` |
| `_encoding` | `SPQparameterStatus` | yes | same meaning: the Python codec of the client encoding |
| `protocol_version`, `server_version`, `socket`, `backend_pid` | `SPQprotocolVersion`, `SPQserverVersion`, `SPQsocket`, `SPQbackendPID` | yes | same |
| `full_protocol_version` | — | no | `PQfullProtocolVersion` absent |
| `needs_password`, `used_password` | — | no | password functions absent (`docs/LIBSPQ.md`, "Absent on purpose") |
| `used_gssapi`, `ssl_in_use` | — | no | no TLS, no GSS |
| `exec_`, `send_query`, `exec_params`, `send_query_params` | `SPQexec`, `SPQsendQuery`, `SPQexecParams`, `SPQsendQueryParams` | yes | same |
| `prepare`, `send_prepare`, `exec_prepared`, `send_query_prepared` | `SPQprepare`, `SPQsendPrepare`, `SPQexecPrepared`, `SPQsendQueryPrepared` | yes | same; `exec_prepared` accepts `None` values and defaults `result_format` to `Format.TEXT` (0) |
| `describe_prepared`, `send_describe_prepared`, `describe_portal`, `send_describe_portal` | `SPQdescribePrepared`, `SPQsendDescribePrepared`, `SPQdescribePortal`, `SPQsendDescribePortal` | yes | same |
| `close_prepared`, `send_close_prepared`, `close_portal`, `send_close_portal` | `SPQclosePrepared`, `SPQsendClosePrepared`, `SPQclosePortal`, `SPQsendClosePortal` | yes | same; optional symbols |
| `get_result`, `consume_input`, `is_busy`, `nonblocking`, `flush` | `SPQgetResult`, `SPQconsumeInput`, `SPQisBusy`, `SPQisnonblocking` / `SPQsetnonblocking`, `SPQflush` | yes | same |
| `set_single_row_mode`, `set_chunked_rows_mode` | `SPQsetSingleRowMode`, `SPQsetChunkedRowsMode` | yes | same; the second is an optional symbol |
| `cancel_conn` | — | no | the `PGcancelConn` family is absent |
| `get_cancel` | `SPQgetCancel` | yes | same |
| `notifies` | `SPQnotifies` | yes | same |
| `put_copy_data`, `put_copy_end`, `get_copy_data` | `SPQputCopyData`, `SPQputCopyEnd`, `SPQgetCopyData` | yes | same |
| `trace`, `set_trace_flags`, `untrace` | — | no | tracing absent |
| `encrypt_password`, `change_password` | — | no | password functions absent |
| `make_empty_result` | — | no | result construction absent |
| `pipeline_status`, `enter_pipeline_mode`, `exit_pipeline_mode`, `pipeline_sync`, `send_flush_request` | — | no | pipeline mode absent (`docs/LIBSPQ.md` section 8: it must be reported as unsupported; the DB-API layer never calls it) |

`PGresult`:

| abc member | libspq function | Defined | Note |
|---|---|---|---|
| `clear` | `SPQclear` | yes | same |
| `status`, `error_message`, `get_error_message`, `error_field` | `SPQresultStatus`, `SPQresultErrorMessage`, `SPQresultErrorField` | yes | same |
| `ntuples`, `nfields`, `binary_tuples`, `nparams`, `oid_value` | `SPQntuples`, `SPQnfields`, `SPQbinaryTuples`, `SPQnparams`, `SPQoidValue` | yes | same |
| `fname`, `ftable`, `ftablecol`, `fformat`, `ftype`, `fmod`, `fsize` | `SPQfname`, `SPQftable`, `SPQftablecol`, `SPQfformat`, `SPQftype`, `SPQfmod`, `SPQfsize` | yes | same; out of range gives `None` / 0 without calling the library |
| `get_value` | `SPQgetvalue`, `SPQgetlength`, `SPQgetisnull` | yes | same |
| `param_type` | `SPQparamtype` | yes | same |
| `command_status`, `command_tuples` | `SPQcmdStatus`, `SPQcmdTuples` | yes | same |
| `set_attributes` | — | no | result construction absent |

Other Protocols:

| abc member | libspq function | Defined | Note |
|---|---|---|---|
| `PGcancelConn` (all nine members) | — | no | absent from libspq; `SPQgetCancel` + `SPQcancel` instead |
| `PGcancel.free`, `PGcancel.cancel` | `SPQfreeCancel`, `SPQcancel` | yes | same |
| `Conninfo.get_defaults`, `Conninfo.parse`, `Conninfo._options_from_array` | `SPQconndefaults`, `SPQconninfoParse`, `SPQconninfoFree` | yes | same |
| `Escaping.__init__`, `escape_literal`, `escape_identifier`, `escape_string`, `escape_bytea`, `unescape_bytea` | `SPQescapeLiteral`, `SPQescapeIdentifier`, `SPQescapeStringConn` / `SPQescapeString`, `SPQescapeByteaConn` / `SPQescapeBytea`, `SPQunescapeBytea` | yes | same |

Added beyond the abc:

| Name | Why |
|---|---|
| `PGconn.pgconn_ptr`, `PGresult.pgresult_ptr` | The addresses, as psycopg's ctypes classes expose them; the compiled reader of plan U2 and the test support need them. |
| `PGresult.get_rows(first, last)` | The bulk read behind the seam of 5.6. |
| `pq.version()`, `pq.has_function(name)`, `pq.__impl__` | Version report and the one permitted feature question. |
| `pq.ext` | The `SPQX` extensions. |

Deliberately not added: client-encoding accessors (`_encoding` covers the need), error
verbosity (3.5), a row-count cache (the accessors are cheap with the GIL held).

## 6. Base exceptions (`stuntgres/errors.py`)

```python
class Warning(Exception): ...
class Error(Exception):
    sqlstate: str | None = None
    def __init__(self, *args: Any, pgconn: PGconn | None = None,
                 pgresult: PGresult | None = None) -> None: ...
    @property
    def diag(self) -> Any: ...
class InterfaceError(Error): ...
class DatabaseError(Error): ...
class DataError(DatabaseError): ...
class OperationalError(DatabaseError): ...
class IntegrityError(DatabaseError): ...
class InternalError(DatabaseError): ...
class ProgrammingError(DatabaseError): ...
class NotSupportedError(DatabaseError): ...
class ConnectionTimeout(OperationalError): ...
class CancellationTimeout(OperationalError): ...
class WaitTimeout(OperationalError): ...

_diag_factory: Callable[[Error], Any] | None = None
```

Rules:

- The module imports only `typing` names at run time; `PGconn` and `PGresult` are imported
  under `if TYPE_CHECKING:` from `.pq`.
- Every class has a one-line docstring and the class attribute `__module__ = "stuntgres"`
  (so a traceback shows `stuntgres.OperationalError`).
- `Error.__init__`: `super().__init__(*args)`; `self.pgconn = pgconn`;
  `self.pgresult = pgresult`.
- `Error.sqlstate` is a class attribute, `None` here.
- `Error.diag`: `factory = _diag_factory`; return `None` if `factory is None`, else
  `factory(self)`.
- `ConnectionTimeout` and `CancellationTimeout` are psycopg's public names; nothing in
  this foundation raises them. `WaitTimeout` is what the waiters of section 8 raise when
  their `timeout` runs out; the DB-API layer translates it.
- The `Warning` class shadows the built-in of that name inside the module: write
  `class Warning(Exception):  # noqa: A001`.

What `stuntgres.pq`, the generators and the waiters raise: `InterfaceError` (loader),
`OperationalError` (a libpq call failed, a closed object was used), `NotSupportedError`
(missing optional function, unknown encoding), `InternalError` (unexpected polling
status), `WaitTimeout`, plus the built-ins `TypeError`, `ValueError`, `MemoryError`.

Extension contract for CPYTHON-DESIGN-DBAPI (no later part edits this file):

1. Classes for individual SQLSTATEs and the class lookup live in a file of its own
   (for example `stuntgres/_sqlstates.py`) and subclass the classes above; a subclass sets
   `sqlstate` as a class attribute, the factory that builds an exception from a result
   may set it on the instance.
2. The diagnostics object is installed by assigning `stuntgres.errors._diag_factory` once,
   from the module that defines it; `Error.diag` then returns `factory(error)`, which
   reads `error.pgresult`.
3. The module surface (`stuntgres/__init__.py`) re-exports the names; that file is handed
   over by CPYTHON-BUILD-01-SKELETON to the part that builds the surface.
4. Pickling, `__str__` customisation and the mapping of `pgconn` to a finished-connection
   snapshot are decided there and implemented in subclasses or helpers, not here.

## 7. Generators (`stuntgres/generators.py`)

### 7.1 Protocol (`stuntgres/_gentypes.py`)

```python
class Wait(enum.IntEnum):      # what a generator waits for
    R = 1
    W = 2
    RW = 3

class Ready(enum.IntEnum):     # what became ready
    NONE = 0
    R = 1
    W = 2
    RW = 3

RV = TypeVar("RV")
PQGen = Generator[Wait, Union[Ready, int], RV]
PQGenConn = Generator[Tuple[int, Wait], Union[Ready, int], RV]
```

- A generator never touches a descriptor. It **yields** what it waits for and is **sent**
  what became ready; it **returns** its result (the driver reads `StopIteration.value`).
- A `PQGen` works on an established connection, whose descriptor the driver is given once.
  A `PQGenConn` yields `(fileno, wait)` because the descriptor may change while a
  connection is being made.
- The driver may send `Ready.NONE` (0): "the interval elapsed, nothing is ready". A
  generator then yields the same request again, except `notifies` (7.7), which returns.
- The driver starts a generator with `next(gen)` and always calls `gen.close()` when it
  stops driving it, finished or not.
- Generators call only nonblocking members of `PGconn`, with one exception: `cancel`
  (7.8) calls `PGcancel.cancel()`, which blocks for a round trip with a real libpq.
- Errors are raised with the classes of section 6; a server error is not raised, it is a
  result with status `FATAL_ERROR` in the returned list.

The module imports `errors as e`, the four names of `._gentypes`, and from `.pq`:
`ConnStatus`, `ExecStatus`, `PGconn`, `PGnotify`, `PGresult`, `PollingStatus`,
`TransactionStatus`, `error_message`. Module constant: `_COPY_STATES =
(ExecStatus.COPY_IN, ExecStatus.COPY_OUT, ExecStatus.COPY_BOTH)`.

```python
def connect(conninfo: bytes) -> PQGenConn[PGconn]: ...
def send(pgconn: PGconn) -> PQGen[None]: ...
def fetch(pgconn: PGconn) -> PQGen[PGresult | None]: ...
def fetch_many(pgconn: PGconn) -> PQGen[list[PGresult]]: ...
def execute(pgconn: PGconn) -> PQGen[list[PGresult]]: ...
def notifies(pgconn: PGconn) -> PQGen[list[PGnotify]]: ...
def cancel(pgconn: PGconn) -> PQGen[list[PGresult]]: ...
def _deliver_notifies(pgconn: PGconn) -> None: ...
```

COPY generators belong to CPYTHON-DESIGN-FEATURES; `fetch_many` stops at the COPY states
for them (7.5 step 4).

### 7.2 `connect(conninfo)`

1. `conn = PGconn.connect_start(conninfo)`; `done = False`. Everything below is inside
   `try:` with `finally: if not done: conn.finish()`.
2. Loop:
   1. If `conn.status == ConnStatus.BAD`: raise
      `e.OperationalError(f"connection failed: {error_message(conn)}", pgconn=conn)`.
   2. `status = conn.connect_poll()`.
   3. `PollingStatus.OK`: leave the loop.
   4. `PollingStatus.READING`: `wanted = Wait.R`. `PollingStatus.WRITING`: `wanted = Wait.W`.
   5. `PollingStatus.FAILED`: raise the same `OperationalError` as in 2.1.
   6. Any other value: raise
      `e.InternalError(f"unexpected connection poll status: {status}", pgconn=conn)`.
   7. `while not (yield conn.socket, wanted): pass` (yield again on `Ready.NONE`;
      `connect_poll` must not be called before the descriptor is ready).
3. `conn.nonblocking = 1`; `done = True`; `return conn`.

Timeouts are the driver's business (8). When the driver closes the generator early, or an
error is raised, the `finally` finishes the half-made connection; the exception's
`pgconn` is that finished object (its `status` is `BAD`, its `error_message` `b""`; the
text is in the exception). With libspq the first poll already answers `OK`
(`docs/LIBSPQ.md` 5.1), so the generator returns without yielding.

### 7.3 `send(pgconn)`

For use right after a `send_…` call.

1. `rv = pgconn.flush()`. If `rv == 0`: return `None`.
2. `ready = yield Wait.RW`; while `ready` is 0: `ready = yield Wait.RW`.
3. If `ready & Ready.R`: `pgconn.consume_input()` (the server may be blocked writing to
   us; reading lets it take our data).
4. Go to step 1.

With libspq `flush()` is always 0. With libpq over TCP it is 1 while data is queued
**(measured for an 8 MB parameter: three to six times in server mode; once or not at
all in oracle mode, where the run kept in `log.txt` had none)**.

### 7.4 `fetch(pgconn)`

Returns the next result, or `None` when the command is complete.

1. If `pgconn.is_busy()` is non-zero, loop:
   1. `ready = yield Wait.R`; if `ready` is 0, repeat this step.
   2. `pgconn.consume_input()`.
   3. `_deliver_notifies(pgconn)`.
   4. If `pgconn.is_busy()` is 0, leave the loop.
2. `_deliver_notifies(pgconn)`.
3. `return pgconn.get_result()` (does not block: the connection is not busy).

### 7.5 `fetch_many(pgconn)`

1. `results = []`.
2. `res = yield from fetch(pgconn)`.
3. If `res is None`: return `results`.
4. `results.append(res)`. If `res.status in _COPY_STATES`: return `results` (the caller
   has to move COPY data before the next result exists).
5. Go to step 2.

### 7.6 `execute(pgconn)`

`yield from send(pgconn)`; `return (yield from fetch_many(pgconn))`. The caller has
called one `send_…` method just before.

### 7.7 `notifies(pgconn)` and `_deliver_notifies(pgconn)`

`notifies` waits once for input and returns the notifications that are then available;
with `Ready.NONE` it returns what is already queued, usually `[]`, so a caller with a
deadline regains control every interval.

1. `ready = yield Wait.R`.
2. If `ready` is non-zero: `pgconn.consume_input()`.
3. `found = []`; loop: `n = pgconn.notifies()`; stop at `None`; `found.append(n)`; if
   `pgconn.notify_handler is not None`: `pgconn.notify_handler(n)`.
4. `return found`.

`_deliver_notifies(pgconn)`: the loop of step 3 without the list: take every queued
notification and pass each to `pgconn.notify_handler` when one is set (without a handler
they are discarded: psycopg does the same, and the DB-API layer always installs one). An
exception raised by the handler propagates to whoever drives the generator.

### 7.8 `cancel(pgconn)`

Asks the server to cancel the running command and collects what the command still
returns (`docs/LIBSPQ.md` section 7 rule 6: "on interrupt call `SPQcancel` and keep
collecting results").

1. If `pgconn.transaction_status != TransactionStatus.ACTIVE`: return `[]` (nothing is
   running; a cancel would have no effect).
2. `c = pgconn.get_cancel()`; `try: c.cancel()` `finally: c.free()`.
3. `return (yield from fetch_many(pgconn))`.

The usual outcome is one result with status `FATAL_ERROR`, SQLSTATE `57014` and the
primary message `canceling statement due to user request`; afterwards
`transaction_status` is `IDLE`, or `INERROR` inside a transaction block **(measured in
both modes)**. If the command finished before the request arrived, the list holds its
normal results. A COPY in progress stops the collection as in 7.5 step 4.

## 8. Waiting

Two drivers of the generators of section 7 with the same four parameters. Neither reads,
writes or closes the descriptor (`docs/LIBSPQ.md` 5.3); they only ask whether it is
ready.

Common rules:

- `timeout` (seconds, `None` = no limit) bounds the whole call. When it runs out the
  driver raises `e.WaitTimeout("timeout expired")`.
- `interval` (seconds, `None` = never) bounds one wait. When it elapses without
  readiness the driver sends `Ready.NONE` into the generator (7.1) and goes on.
- On every way out (result, exception from the generator, `WaitTimeout`,
  `KeyboardInterrupt`, task cancellation) the generator is closed with `gen.close()` and
  the descriptor is registered nowhere.
- What a connection does after an interruption (cancel, finish, re-raise) is decided by
  CPYTHON-DESIGN-DBAPI; the pieces are `generators.cancel` (7.8) and these guarantees.

Both modules define the same private helper:

```python
def _slice(deadline: float | None, interval: float | None) -> float | None:
```

1. If `deadline is None`: return `interval`.
2. `left = deadline - time.monotonic()`; if `left <= 0`: raise
   `e.WaitTimeout("timeout expired")`.
3. Return `left` if `interval is None`, else `min(left, interval)`.

### 8.1 Synchronous driver (`stuntgres/waiting.py`)

```python
def wait(gen: PQGen[RV], fileno: int, *, timeout: float | None = None,
         interval: float | None = None) -> RV: ...
def wait_conn(gen: PQGenConn[RV], *, timeout: float | None = None,
              interval: float | None = None) -> RV: ...
```

Module constants: `_MASKS = {Wait.R: select.POLLIN, Wait.W: select.POLLOUT, Wait.RW:
select.POLLIN | select.POLLOUT}`; `_ERRORS = select.POLLERR | select.POLLHUP |
select.POLLNVAL`.

`_poll_once(fileno: int, wait: int, seconds: float | None) -> int`:

1. `poller = select.poll()`; `poller.register(fileno, _MASKS[Wait(wait)])`.
2. `events = poller.poll(None if seconds is None else max(0.0, seconds) * 1000.0)`.
3. No events: return `Ready.NONE`.
4. `ev = events[0][1]`. If `ev & _ERRORS`: return `int(wait)` (report what was asked
   for; the next library call then sees and reports the failure).
5. `ready = 0`; add `Ready.R` if `ev & select.POLLIN`; add `Ready.W` if
   `ev & select.POLLOUT`; return `ready`.

The poll object is local to the call, so nothing stays registered. Python retries
`poll()` after a signal whose handler returns (PEP 475) and lets an exception raised by
the handler, `KeyboardInterrupt` for Ctrl-C, propagate.

`wait(gen, fileno, *, timeout, interval)`:

1. `deadline = None if timeout is None else time.monotonic() + timeout`.
2. `try:` `wanted = next(gen)`; loop: `ready = _poll_once(fileno, wanted,
   _slice(deadline, interval))`; `wanted = gen.send(ready)`.
3. `except StopIteration as stop: return stop.value`.
4. `finally: gen.close()`.

`wait_conn(gen, *, timeout, interval)`: the same with `fileno, wanted = next(gen)` and
`fileno, wanted = gen.send(ready)`.

**(measured, experiment 6, both modes)** a `SIGINT` delivered to the main thread while it
waited inside `wait()` for a statement in `pg_sleep(30)` raised `KeyboardInterrupt` out of
`wait()` within 1 ms; the generator was closed (`gen.gi_frame is None`), the connection
was still `OK` with transaction status `ACTIVE`; `wait(generators.cancel(pgconn), …)`
then returned `[FATAL_ERROR 57014]` within 3 ms and the connection ran `select 1`. With
`interval=0.05` a statement of 0.3 s delivered `Ready.NONE` 5 times before its result.

A process started in the background (`nohup`, `&` in a non-interactive shell) inherits
`SIGINT` ignored and Python then installs no handler: Ctrl-C does nothing there, in any
program. Tests that send `SIGINT` install `signal.default_int_handler` themselves
(section 10.3, CPYTHON-PQ-17-WAIT-SYNC) **(measured: the first run of experiment 6 from
a background job was not interrupted)**.

### 8.2 asyncio driver (`stuntgres/waiting_async.py`)

```python
async def wait_async(gen: PQGen[RV], fileno: int, *, timeout: float | None = None,
                     interval: float | None = None) -> RV: ...
async def wait_conn_async(gen: PQGenConn[RV], *, timeout: float | None = None,
                          interval: float | None = None) -> RV: ...
```

`async def _ready_once(fileno: int, wait: int, seconds: float | None) -> int`:

1. `loop = asyncio.get_running_loop()`; `fut = loop.create_future()`.
2. Local function `wake(flag: int) -> None`: `if not fut.done(): fut.set_result(flag)`.
3. `reading = bool(wait & Wait.R)`; `writing = bool(wait & Wait.W)`; `timer = None`.
4. `try:` if `reading`: `loop.add_reader(fileno, wake, Ready.R)`; if `writing`:
   `loop.add_writer(fileno, wake, Ready.W)`; if `seconds is not None`:
   `timer = loop.call_later(max(0.0, seconds), wake, Ready.NONE)`; then
   `return await fut`.
5. `finally:` if `timer is not None`: `timer.cancel()`; if `reading`:
   `loop.remove_reader(fileno)`; if `writing`: `loop.remove_writer(fileno)`.

The descriptor is registered only while the coroutine is suspended in step 4 and is
removed before `_ready_once` returns or raises, cancellation included. When both
directions are ready at once the first callback wins and the generator sees the other one
on its next request. The interval is a timer on the same future, not `asyncio.wait_for`:
on Python 3.10 and 3.11 `wait_for` can swallow a cancellation that arrives in the loop
iteration in which the awaited future completes; a bare `await fut` cannot.

`wait_async` and `wait_conn_async` are the functions of 8.1 with
`ready = await _ready_once(fileno, wanted, _slice(deadline, interval))` in place of
`_poll_once`. They need a selector event loop (the default on Linux).

**(measured, experiment 7, both modes)** after every call `loop.remove_reader(fd)` and
`loop.remove_writer(fd)` returned `False` (nothing was left registered); another task
ran at least 10 times during a 0.3 s statement; cancelling the task that awaited
`wait_async` for `pg_sleep(30)` raised `CancelledError` in it, closed the generator, left
the connection `ACTIVE`, and `await wait_async(generators.cancel(pgconn), fd, timeout=10)`
returned `[FATAL_ERROR 57014]`; `timeout=0.2` raised `WaitTimeout` after 0.20 s; an 8 MB
parameter went through the writer path.

`generators.cancel` blocks the event loop for one local round trip with a real libpq
(8.1 measured 1 to 3 ms) and not at all with libspq. That is accepted for the test modes.

## 9. Test harness

The suite is the standard library's `unittest`. It issues SQL through a public API in a
directory other than `reference/`; `docs/TESTING.md` ("Placement policy") does not allow
that today, exactly as for the libspq C++ tests. Recording both as sanctioned binding
suites is the job of the documentation parts (`LIBSPQ-DOCS-01-FINAL`, and the docs part
CPYTHON-DESIGN-FEATURES creates, plan A10). The tests check the behavior of the binding;
SQL features stay with the reference tests.

### 9.1 The runner (`tools/runtests.py`)

```sh
python3 bindings/cpython/tools/runtests.py --mode <unit|oracle|server|native> [selector ...]
```

This is the command line of rule C5 of `.cswd/tmp/CPYTHON/rules.md`; no experiment
required a change. It may be run from any directory.

| Mode | Needs | Runs |
|---|---|---|
| `unit` | nothing | Tests whose class needs no native library. |
| `oracle` | Docker, the pinned image | Every test: real libpq 18.6 against PostgreSQL 18.6 in a container (9.4). |
| `server` | Go, and Docker once (9.3) | Every test: real libpq 18.6 against `cmd/stuntgres` (9.5). |
| `native` | `build/libspq/lib/libspq.so` | Every test: libspq (9.7). Exists after CPYTHON-TEST-03-NATIVE. |

A **selector** is a dotted name below the package `tests`: a module (`test_pq_exec`), a
class (`test_pq_exec.ExecTest`) or a method (`test_pq_exec.ExecTest.test_exec`); a
leading `tests.` is accepted. Without selectors every `tests/test_*.py` is loaded.

Selection by class attribute (plain data, so the runner needs no support code):

| Attribute | Values | Effect |
|---|---|---|
| `needs` | absent or `"nothing"`, `"library"`, `"database"` | In `unit` mode only classes that need nothing are kept. |
| `modes` | absent or a tuple of mode names | The class is kept only in those modes. |

Classes that are dropped are not reported. Gates inside a class are decorators (9.6) and
show up as skips.

| Exit code | Meaning |
|---|---|
| 0 | Every selected test passed (skips allowed). |
| 1 | A test failed or raised, a module failed to import, or an exception was reported through `sys.unraisablehook` (for example from a finalizer) during the run. |
| 2 | Wrong command line (`argparse`). |
| 3 | The mode could not start (no Docker, image missing, build failed, server not ready, backend module absent). One line on `stderr`: `runtests: cannot start <mode> mode: <reason>`. |
| 4 | No test was selected. `stderr`: `runtests: no tests selected`. |
| 130 | Interrupted by `SIGINT` or `SIGTERM`. `stderr`: `runtests: interrupted`. |

Behavior:

- `bindings/cpython` is put first on `sys.path`: the source tree is tested, nothing is
  installed.
- Before anything else every environment variable whose name starts with `PG`, and
  `STUNTGRES_LIBSPQ` and `_STUNTGRES_TEST_LIBPQ`, are removed from the process, so a real
  libpq reads nothing from the caller's environment **(measured: a `~/.pgpass` with loose
  permissions made libpq print a warning on every connection until `PGPASSFILE` pointed
  at an empty 0600 file; the backends set that)**.
- Warnings are errors (`warnings.simplefilter("error")` and
  `TextTestRunner(warnings="error")`). Output is `unittest`'s summary at verbosity 0, on
  `stderr`.
- The backend is stopped in a `finally` block: after a pass, a failure, `Ctrl-C` and
  `SIGTERM` (whose handler raises `KeyboardInterrupt`; `unittest` would swallow
  `SystemExit` as a test error). **(measured in unit, oracle and server mode: a runner
  signalled with `SIGTERM` or `SIGINT` while a test blocked exited with 130, printed
  `runtests: interrupted` and left no container, server or work directory.)** A runner that is killed outright leaves its backend
  behind; the next runner removes it (9.4, 9.5).
- In a database mode the runner creates `build/cpython/` at the repository root and, when
  missing, `build/cpython/.gitignore` with the single line `*`.

The complete file (CPYTHON-BUILD-01-SKELETON writes it; no later part edits it):

```python
#!/usr/bin/env python3
"""Test runner of bindings/cpython: the only way its tests are run."""
from __future__ import annotations

import argparse
import os
import signal
import sys
import unittest
import warnings
from typing import Any, Iterator, Sequence

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
REPO = os.path.dirname(os.path.dirname(ROOT))
BUILD = os.path.join(REPO, "build", "cpython")
MODES = ("unit", "oracle", "server", "native")
EXIT_OK, EXIT_FAILED, EXIT_USAGE, EXIT_NO_START, EXIT_NO_TESTS = 0, 1, 2, 3, 4
EXIT_INTERRUPTED = 130


def parse(argv: Sequence[str] | None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        prog="runtests.py", description="Run the tests of bindings/cpython."
    )
    parser.add_argument("--mode", required=True, choices=MODES)
    parser.add_argument("selectors", nargs="*", metavar="selector")
    return parser.parse_args(argv)


def clean_environment() -> None:
    """Drop the variables a libpq or the loader would read from the caller."""
    for key in list(os.environ):
        if key.startswith("PG") or key in ("STUNTGRES_LIBSPQ", "_STUNTGRES_TEST_LIBPQ"):
            del os.environ[key]


def prepare_build_dir() -> None:
    os.makedirs(BUILD, exist_ok=True)
    ignore = os.path.join(BUILD, ".gitignore")
    if not os.path.exists(ignore):
        with open(ignore, "w", encoding="ascii") as fh:
            fh.write("*\n")


def load(selectors: Sequence[str]) -> unittest.TestSuite:
    loader = unittest.TestLoader()
    if not selectors:
        return loader.discover(
            os.path.join(ROOT, "tests"), pattern="test_*.py", top_level_dir=ROOT
        )
    names = [s if s.startswith("tests.") else "tests." + s for s in selectors]
    return loader.loadTestsFromNames(names)


def flatten(suite: unittest.TestSuite) -> Iterator[unittest.TestCase]:
    for item in suite:
        if isinstance(item, unittest.TestSuite):
            yield from flatten(item)
        else:
            yield item


def wanted(test: unittest.TestCase, mode: str) -> bool:
    cls = type(test)
    if mode == "unit" and getattr(cls, "needs", "nothing") != "nothing":
        return False
    modes = getattr(cls, "modes", None)
    return modes is None or mode in modes


def select(suite: unittest.TestSuite, mode: str) -> unittest.TestSuite:
    return unittest.TestSuite(t for t in flatten(suite) if wanted(t, mode))


def _terminate(signum: int, frame: Any) -> None:
    raise KeyboardInterrupt


def main(argv: Sequence[str] | None = None) -> int:
    args = parse(argv)
    sys.path.insert(0, ROOT)
    clean_environment()
    unraisable: list[str] = []

    def record(info: Any) -> None:
        what = info.err_msg or "Exception ignored in"
        unraisable.append(
            f"{what} {info.object!r}: {info.exc_type.__name__}: {info.exc_value}"
        )

    sys.unraisablehook = record
    signal.signal(signal.SIGTERM, _terminate)
    warnings.simplefilter("error")
    backend: Any = None
    try:
        if args.mode != "unit":
            try:
                prepare_build_dir()
                from tests.support import backends

                backend = backends.start(args.mode, BUILD, REPO)
            except Exception as exc:
                print(
                    f"runtests: cannot start {args.mode} mode: {exc}", file=sys.stderr
                )
                return EXIT_NO_START
        suite = select(load(args.selectors), args.mode)
        if suite.countTestCases() == 0:
            print("runtests: no tests selected", file=sys.stderr)
            return EXIT_NO_TESTS
        runner = unittest.TextTestRunner(
            stream=sys.stderr, verbosity=0, warnings="error"
        )
        result = runner.run(suite)
    except KeyboardInterrupt:
        print("runtests: interrupted", file=sys.stderr)
        return EXIT_INTERRUPTED
    finally:
        if backend is not None:
            backend.stop()
    for line in unraisable:
        print(f"runtests: {line}", file=sys.stderr)
    if unraisable or not result.wasSuccessful():
        return EXIT_FAILED
    return EXIT_OK


if __name__ == "__main__":
    sys.exit(main())
```

### 9.2 Backends (`tests/support/backends.py`)

A backend is an object with four methods; the runner knows only `stop`.

| Method | Contract |
|---|---|
| `start() -> None` | Make the database service ready or raise; on failure release everything it had started. |
| `acquire() -> str` | A conninfo string for database state no other test sees. |
| `release(conninfo: str) -> None` | Called by the test's cleanup with the string `acquire` returned. |
| `stop() -> None` | Remove everything. Never raises; may be called twice. |

`backends.start(mode, build_dir, repo_root)` prepares the environment of the mode,
imports `tests.support.<mode>`, builds its `Backend(build_dir, repo_root)`, starts it,
publishes it as `tests.support.backend` with `tests.support.mode`, and returns it. For
`oracle` and `server` it first creates the empty file `build/cpython/pgpass` (mode 0600),
sets `PGPASSFILE` to it and sets `_STUNTGRES_TEST_LIBPQ` to the value of 9.3. Setting
`os.environ` before the first library call is enough: the loader reads it on first use.

The complete file:

```python
"""Start the database backend of a runner mode."""
from __future__ import annotations

import importlib
import os
from typing import Any

from tests import support

from . import libpq

MODULES = {"oracle": "oracle", "server": "server", "native": "native"}


def start(mode: str, build_dir: str, repo_root: str) -> Any:
    """Start the backend of `mode`, publish it in tests.support and return it."""
    if mode not in MODULES:
        raise ValueError(f"unknown mode {mode!r}")
    if mode in ("oracle", "server"):
        pgpass = os.path.join(build_dir, "pgpass")
        with open(os.open(pgpass, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), "w"):
            pass
        os.environ["PGPASSFILE"] = pgpass
        os.environ["_STUNTGRES_TEST_LIBPQ"] = libpq.ensure(build_dir)
    module = importlib.import_module("tests.support." + MODULES[mode])
    backend = module.Backend(build_dir, repo_root)
    backend.start()
    support.mode = mode
    support.backend = backend
    return backend
```

### 9.3 libpq 18 for oracle and server mode (`tests/support/libpq.py`)

Decision: both modes use the libpq of the pinned image, version 18.6, so the functions
libpq 16 lacks (chunked rows, close prepared, `PQsocketPoll`) are tested in both. The
host's `/lib/x86_64-linux-gnu/libpq.so.5` (16.15 here) is not used.

**(measured, experiment 2)** `libpq.so.5.18` copied out of the image does not load by
itself on Ubuntu 24.04: `libldap-2.5.so.0: cannot open shared object file`. Its other
dependencies (`libssl.so.3`, `libcrypto.so.3`, `libgssapi_krb5.so.2`, `libm`, `libc`)
exist on the host. `libldap-2.5.so.0` needs `liblber-2.5.so.0`, `libsasl2.so.2`,
`libgnutls.so.30` and `libc`; only `liblber-2.5.so.0` is missing on the host. Opening
`liblber-2.5.so.0`, then `libldap-2.5.so.0`, then `libpq.so.5.18` by absolute path works:
the dynamic linker satisfies a `DT_NEEDED` entry from an already loaded library with the
same soname. Nothing is installed and no search path is changed. `PQlibVersion()` is
180006 and all seven optional libpq functions are present.

`ensure(build_dir)` returns the three paths under `build/cpython/libpq18/` joined with
`os.pathsep`, in that order: the value of `_STUNTGRES_TEST_LIBPQ` (2.2). It copies the
files only when the stamp file `image.txt` does not hold the image reference:

```sh
docker create <IMAGE>                    # prints the container id
docker cp -L <id>:/usr/lib/<triplet>/<file> build/cpython/libpq18/<file>     # three times
docker rm -f <id>
```

`<triplet>` is `x86_64-linux-gnu` or `aarch64-linux-gnu` by `platform.machine()`
(`x86_64` was run; the `aarch64` path is the Debian multiarch name and is not measured).
**(measured)** 0.06 s for a fresh copy; nothing is run when the stamp matches.

`IMAGE` is a copy of the Go constant `referenceImage` of `reference/fixture/fixture.go`;
a unit test of CPYTHON-TEST-01S-STARTER reads that Go file and fails when the two strings
differ.

The complete file:

```python
"""libpq 18 for oracle and server mode, copied out of the pinned image."""
from __future__ import annotations

import os
import platform
import subprocess

IMAGE = (
    "postgres:18.6-bookworm@sha256:"
    "afc7e2d441324c0388fa80c3d24f733b4194a4eb7f47dd8ee2b08eb1a24a647c"
)
FILES = ("liblber-2.5.so.0", "libldap-2.5.so.0", "libpq.so.5.18")
_TRIPLETS = {"x86_64": "x86_64-linux-gnu", "aarch64": "aarch64-linux-gnu"}


def run(*argv: str, timeout: float = 120.0, cwd: str | None = None) -> str:
    """Run a command quietly; return its stdout; raise RuntimeError when it fails."""
    try:
        proc = subprocess.run(
            argv, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout,
            text=True, cwd=cwd, stdin=subprocess.DEVNULL,
        )
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise RuntimeError(f"{argv[0]}: {exc}") from None
    if proc.returncode != 0:
        raise RuntimeError(
            f"{' '.join(argv[:3])} failed ({proc.returncode}): {proc.stderr.strip()}"
        )
    return proc.stdout.strip()


def ensure(build_dir: str) -> str:
    """Return the loader's test-seam value, extracting the libraries when needed."""
    dest = os.path.join(build_dir, "libpq18")
    stamp = os.path.join(dest, "image.txt")
    paths = [os.path.join(dest, name) for name in FILES]
    fresh = False
    if os.path.isfile(stamp) and all(os.path.isfile(p) for p in paths):
        with open(stamp, encoding="ascii") as fh:
            fresh = fh.read() == IMAGE
    if not fresh:
        triplet = _TRIPLETS[platform.machine()]
        os.makedirs(dest, exist_ok=True)
        container = run("docker", "create", IMAGE)
        try:
            for name, path in zip(FILES, paths):
                source = f"{container}:/usr/lib/{triplet}/{name}"
                run("docker", "cp", "-L", source, path)
        finally:
            run("docker", "rm", "-f", container)
        with open(stamp, "w", encoding="ascii") as fh:
            fh.write(IMAGE)
    return os.pathsep.join(paths)
```

### 9.4 Oracle mode (`tests/support/oracle.py`)

Container settings are those of the Go fixture (`reference/fixture/lifecycle.go`,
function `referenceStart`'s option list: database `stuntgres_reference`, user and
password `stuntgres`, `POSTGRES_INITDB_ARGS=--locale=C --encoding=UTF8`, and the server
arguments `max_prepared_transactions=100`, `log_line_prefix=timeout-oracle|%p|%c|%e|`,
`track_commit_timestamp=off`). The optional settings of that fixture (`wal_level`,
preload libraries, the OpenSSL configuration) are not used.

```sh
docker run -d --rm --name stuntgres-cpython-<pid> --label stuntgres.cpython.pid=<pid> \
  -e POSTGRES_DB=stuntgres_reference -e POSTGRES_USER=stuntgres \
  -e POSTGRES_PASSWORD=stuntgres -e 'POSTGRES_INITDB_ARGS=--locale=C --encoding=UTF8' \
  -p 127.0.0.1::5432 <IMAGE> \
  -c max_prepared_transactions=100 -c 'log_line_prefix=timeout-oracle|%p|%c|%e|' \
  -c track_commit_timestamp=off
```

Life cycle:

1. **Sweep.** List containers that carry the label; remove (`docker rm -f -v`) each whose
   label value is the pid of a process that no longer exists.
2. **Start** with the command above (`<pid>` is the runner's pid).
3. **Readiness**, a bounded poll: `docker exec <name> pg_isready -q -h 127.0.0.1 -U
   stuntgres -d stuntgres_reference` every 0.1 s until it exits 0, at most 120 s. The
   image's first-run server listens on a Unix socket only, so TCP readiness means the
   final server.
4. **Verify**: `docker exec <name> psql -X -At -h 127.0.0.1 -U stuntgres -d
   stuntgres_reference -c "<FACTS_SQL>"` must print `180006|UTF8|C|C|100`.
5. **Port**: `docker port <name> 5432/tcp`, first line, the number after the last colon.
6. **Admin connection**: one `RawConn` (9.6) to `stuntgres_reference`, kept open.
7. **Stop**: close the admin connection, `docker rm -f -v <name>`.

**(measured, experiment 3)** start to verified: 0.9 s; removal 0.2 s; 150 database tests
through the runner: 5.0 to 5.9 s (6.3 to 7.3 s with start and removal); the container
was gone after a passing run, a failing run, `SIGINT` and `SIGTERM`; after `SIGKILL` of
the runner it stayed and the next runner's sweep removed it.

Isolation: **a database per test.** `acquire()` runs `CREATE DATABASE "t<NNNNN>"` on the
admin connection (a counter, five digits) and returns
`host=127.0.0.1 port=<port> dbname=t<NNNNN> user=stuntgres password=stuntgres sslmode=disable`;
`release()` runs `DROP DATABASE "t<NNNNN>" WITH (FORCE)`, which also ends connections a
failed test left open.

| Mechanism **(measured, n = 30 to 200)** | Cost per test |
|---|---|
| `CREATE DATABASE` + connect + one table + finish + `DROP DATABASE … WITH (FORCE)` | 28 ms median (create 11 to 12 ms, drop 7 ms) |
| the same with `STRATEGY file_copy` | 63 ms |
| one database, `DROP SCHEMA public CASCADE; CREATE SCHEMA public` between tests | 1 ms, rejected: leaves roles, settings, other schemas, prepared transactions and anything a failed test leaks |

Roles, tablespaces and other cluster-wide objects are shared by all tests of a run in
oracle mode: a test that creates one names it with `support.unique_name()`.

The complete file:

```python
"""Oracle mode: the pinned PostgreSQL image in a container, a database per test."""
from __future__ import annotations

import itertools
import os
import subprocess
import time

from .libpq import IMAGE, run
from .raw import RawConn

DATABASE = "stuntgres_reference"
USER = "stuntgres"
PASSWORD = "stuntgres"
SERVER_ARGS = (
    "-c", "max_prepared_transactions=100",
    "-c", "log_line_prefix=timeout-oracle|%p|%c|%e|",
    "-c", "track_commit_timestamp=off",
)
LABEL = "stuntgres.cpython.pid"
START_SECONDS = 120.0
FACTS = "180006|UTF8|C|C|100"
FACTS_SQL = (
    "SELECT current_setting('server_version_num'), current_setting('server_encoding'),"
    " datcollate, datctype, current_setting('max_prepared_transactions')"
    " FROM pg_database WHERE datname = current_database()"
)


def sweep() -> None:
    """Remove containers left by runners that no longer exist."""
    listing = run(
        "docker", "ps", "-a", "--filter", f"label={LABEL}",
        "--format", '{{.Names}} {{.Label "' + LABEL + '"}}',
    )
    for line in listing.splitlines():
        name, pid = line.split()
        try:
            os.kill(int(pid), 0)
        except ProcessLookupError:
            run("docker", "rm", "-f", "-v", name)
        except PermissionError:
            pass


class Backend:
    def __init__(self, build_dir: str, repo_root: str) -> None:
        self.name = f"stuntgres-cpython-{os.getpid()}"
        self.port = 0
        self._admin: RawConn | None = None
        self._numbers = itertools.count(1)

    def start(self) -> None:
        sweep()
        try:
            run(
                "docker", "run", "-d", "--rm", "--name", self.name,
                "--label", f"{LABEL}={os.getpid()}",
                "-e", f"POSTGRES_DB={DATABASE}",
                "-e", f"POSTGRES_USER={USER}",
                "-e", f"POSTGRES_PASSWORD={PASSWORD}",
                "-e", "POSTGRES_INITDB_ARGS=--locale=C --encoding=UTF8",
                "-p", "127.0.0.1::5432", IMAGE, *SERVER_ARGS,
            )
            deadline = time.monotonic() + START_SECONDS
            probe = ["docker", "exec", self.name, "pg_isready", "-q",
                     "-h", "127.0.0.1", "-U", USER, "-d", DATABASE]
            while subprocess.run(probe, stdout=subprocess.DEVNULL,
                                 stderr=subprocess.DEVNULL,
                                 stdin=subprocess.DEVNULL).returncode != 0:
                if time.monotonic() > deadline:
                    raise RuntimeError(
                        f"the oracle container was not ready in {START_SECONDS:.0f} s"
                    )
                time.sleep(0.1)
            facts = run("docker", "exec", self.name, "psql", "-X", "-At",
                        "-h", "127.0.0.1", "-U", USER, "-d", DATABASE, "-c", FACTS_SQL)
            if facts != FACTS:
                raise RuntimeError(f"oracle configuration is {facts}, want {FACTS}")
            mapping = run("docker", "port", self.name, "5432/tcp")
            self.port = int(mapping.splitlines()[0].rsplit(":", 1)[1])
            self._admin = RawConn(self._conninfo(DATABASE))
        except BaseException:
            self.stop()
            raise

    def _conninfo(self, dbname: str) -> str:
        return (f"host=127.0.0.1 port={self.port} dbname={dbname} user={USER}"
                f" password={PASSWORD} sslmode=disable")

    def acquire(self) -> str:
        assert self._admin is not None
        name = f"t{next(self._numbers):05d}"
        self._admin.execute(f'CREATE DATABASE "{name}"')
        return self._conninfo(name)

    def release(self, conninfo: str) -> None:
        assert self._admin is not None
        name = conninfo.split(" dbname=", 1)[1].split(" ", 1)[0]
        self._admin.execute(f'DROP DATABASE "{name}" WITH (FORCE)')

    def stop(self) -> None:
        admin, self._admin = self._admin, None
        if admin is not None:
            admin.close()
        subprocess.run(["docker", "rm", "-f", "-v", self.name],
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                       stdin=subprocess.DEVNULL)
```

### 9.5 Server mode (`tests/support/server.py`)

Facts of the wire server that shape this backend (`cmd/stuntgres/README.md` "Operation",
`pgserver/README.md` "Configuration and ownership", `internal/serverauth/users.go`
function `Load`, `internal/serverauth/password_cost_reduced.go`):

- Accounts come from a user file; a record is
  `name:$pbkdf2-sha256$i=<iterations>$<salt>$<key>:<db1>,<db2>,…` with a 16-byte salt and
  a 32-byte PBKDF2-HMAC-SHA256 key in unpadded standard base64; the file must be mode
  0600. `stuntgres passwd` prompts on a terminal, so the backend writes the record itself
  with `hashlib.pbkdf2_hmac`.
- Every login verifies the password with that PBKDF2 cost. The build tag
  `stuntgres_reduced_kdf` lowers it from 600000 to 4096 iterations for test servers; the
  backend builds with it and writes `i=4096`.
- The served databases are exactly those the user file grants; each has an engine of its
  own with its own roles. **(measured)** `CREATE DATABASE dbx` succeeds but a connection
  to `dbx` is refused with `FATAL:  database "dbx" does not exist`; tables of one served
  database are invisible in another.
- `serve -l 127.0.0.1:0` binds an ephemeral port and prints one JSON line on standard
  output when ready: `{"tstamp":…,"severity":"INFO","message":"server started","address":"127.0.0.1:<port>","databases":[…]}`.
  SQL errors are logged to standard output too, so it goes to a file, never to a pipe
  nobody reads.

```sh
go build -tags stuntgres_reduced_kdf -o build/cpython/stuntgres ./cmd/stuntgres    # cwd: repository root
build/cpython/stuntgres serve -l 127.0.0.1:0 -u build/cpython/server-<pid>/users \
    -p build/cpython/server-<pid>/pid  > build/cpython/server-<pid>/out.log 2>&1
```

Isolation: **a rolling pool of pre-declared databases.** The user file grants `t000` to
`t063`. `acquire()` hands out the next unused name; when all 64 are used it kills the
server and starts a new one (same user file, new port), which gives 64 fresh engines.
`release()` does nothing. A test must therefore close its connections before it ends
(the test cases' cleanups do).

| Mechanism **(measured)** | Cost |
|---|---|
| start of a server with 1 / 100 / 1000 databases | 46 ms / 242 ms / 1.5 s; resident memory 59 MB / 347 MB / 3.0 GB |
| rolling pool of 16 / 64 / 256, 200 tests (connect, one table, finish) | 8.8 / 6.0 / 5.1 ms per test; 109 / 254 / 897 MB |
| a server process per test | 38 ms per test |

64 is the pool size: 6 ms per test and a quarter of a gigabyte. Build: 31 s cold, 0.1 s
when nothing changed. 150 database tests through the runner: 0.7 s (1.1 s with start
and stop). Start-up waits for the `server started` line for at most 60 s.

Life cycle: sweep (for every `build/cpython/server-<pid>` whose pid is dead: kill the
server named in its `pid` file if `/proc/<n>/cmdline` holds that directory's `users`
path, then remove the directory); build; write the user file; spawn. Any failure or
interruption during `start` stops the backend before it propagates. Stop: kill the
process, wait for it, remove the work directory. **(measured)** with the runner: gone after a pass, a failure, `SIGINT` and `SIGTERM`
(above); after `SIGKILL` of the runner the server and its work directory stayed and the
next runner's sweep removed both.

The complete file:

```python
"""Server mode: the stuntgres wire server, a pre-declared database per test."""
from __future__ import annotations

import base64
import glob
import hashlib
import json
import os
import shutil
import subprocess
import time

from .libpq import run

USER = "stuntgres"
PASSWORD = "stuntgres"
POOL = 64
ITERATIONS = 4096          # the PBKDF2 cost of the build tag stuntgres_reduced_kdf
START_SECONDS = 60.0
NAMES = tuple(f"t{i:03d}" for i in range(POOL))


def _b64(data: bytes) -> str:
    return base64.b64encode(data).decode("ascii").rstrip("=")


def user_record(salt: bytes) -> str:
    """The user-file line that grants USER every database of the pool."""
    key = hashlib.pbkdf2_hmac("sha256", PASSWORD.encode(), salt, ITERATIONS, 32)
    return (f"{USER}:$pbkdf2-sha256$i={ITERATIONS}${_b64(salt)}${_b64(key)}:"
            f"{','.join(NAMES)}\n")


def sweep(build_dir: str) -> None:
    """Kill servers and remove work directories left by runners that are gone."""
    for workdir in glob.glob(os.path.join(build_dir, "server-*")):
        try:
            os.kill(int(workdir.rsplit("-", 1)[1]), 0)
            continue
        except ProcessLookupError:
            pass
        except (PermissionError, ValueError):
            continue
        try:
            with open(os.path.join(workdir, "pid"), encoding="utf-8") as fh:
                pid = int(json.load(fh)["pid"])
            with open(f"/proc/{pid}/cmdline", "rb") as fh:
                argv = fh.read().split(b"\0")
            if os.path.join(workdir, "users").encode() in argv:
                os.kill(pid, 9)
        except (OSError, ValueError, KeyError):
            pass
        shutil.rmtree(workdir, ignore_errors=True)


class Backend:
    def __init__(self, build_dir: str, repo_root: str) -> None:
        self.repo_root = repo_root
        self.build_dir = build_dir
        self.binary = os.path.join(build_dir, "stuntgres")
        self.workdir = os.path.join(build_dir, f"server-{os.getpid()}")
        self.port = 0
        self._proc: subprocess.Popen[bytes] | None = None
        self._used = 0

    def start(self) -> None:
        try:
            sweep(self.build_dir)
            run("go", "build", "-tags", "stuntgres_reduced_kdf", "-o", self.binary,
                "./cmd/stuntgres", timeout=900.0, cwd=self.repo_root)
            os.makedirs(self.workdir, exist_ok=True)
            userfile = os.path.join(self.workdir, "users")
            flags = os.O_WRONLY | os.O_CREAT | os.O_TRUNC
            with open(os.open(userfile, flags, 0o600), "w", encoding="utf-8") as fh:
                fh.write(user_record(os.urandom(16)))
            self._spawn()
        except BaseException:
            self.stop()
            raise

    def _spawn(self) -> None:
        self._kill()
        out_path = os.path.join(self.workdir, "out.log")
        pid_path = os.path.join(self.workdir, "pid")
        with open(out_path, "wb") as out:
            self._proc = subprocess.Popen(
                [self.binary, "serve", "-l", "127.0.0.1:0",
                 "-u", os.path.join(self.workdir, "users"), "-p", pid_path],
                stdout=out, stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL,
            )
        deadline = time.monotonic() + START_SECONDS
        while True:
            with open(out_path, encoding="utf-8", errors="replace") as fh:
                for line in fh:
                    if line.endswith("\n") and '"server started"' in line:
                        self.port = int(json.loads(line)["address"].rsplit(":", 1)[1])
                        self._used = 0
                        return
            if self._proc.poll() is not None or time.monotonic() > deadline:
                with open(out_path, encoding="utf-8", errors="replace") as fh:
                    text = fh.read().strip()
                self._kill()
                raise RuntimeError(f"the stuntgres server did not start: {text}")
            time.sleep(0.005)

    def acquire(self) -> str:
        if self._used == POOL:
            self._spawn()
        name = NAMES[self._used]
        self._used += 1
        return (f"host=127.0.0.1 port={self.port} dbname={name} user={USER}"
                f" password={PASSWORD} sslmode=disable")

    def release(self, conninfo: str) -> None:
        """Nothing to do: a database is never handed out twice."""

    def _kill(self) -> None:
        proc, self._proc = self._proc, None
        if proc is not None:
            proc.kill()
            proc.wait()

    def stop(self) -> None:
        self._kill()
        shutil.rmtree(self.workdir, ignore_errors=True)
```

### 9.6 Support API (`tests/support/__init__.py`, `tests/support/raw.py`)

| Name | Purpose |
|---|---|
| `support.mode` | `"unit"`, `"oracle"`, `"server"` or `"native"`. |
| `support.backend` | The backend of 9.2, `None` in unit mode. |
| `support.LibraryTestCase` | `unittest.TestCase` with `needs = "library"`: uses the loaded library, no database. |
| `support.DatabaseTestCase` | `needs = "database"`. `setUp` sets `self.conninfo` (a `str`) to a fresh database and registers the release. A subclass that defines `setUp` calls `super().setUp()` first. |
| `support.AsyncDatabaseTestCase` | The same on `unittest.IsolatedAsyncioTestCase`. |
| `@support.only_modes("oracle", "server")` | Decorator for a test **method** (plain or `async def`), never for a class: skip unless the runner's mode is listed. |
| `@support.requires_function("PQsetChunkedRowsMode")` | Decorator for a test method (plain or `async def`): skip unless `pq.has_function(name)`. Evaluated when the test runs, never at import. |
| `support.unique_name(prefix="n")` | `f"{prefix}_{pid}_{counter}"`, for objects that are not private to a database. |
| `support.conninfo_dict(conninfo)` | The `keyword=value` pairs of a backend's conninfo string as a `dict` (the backends never quote values). Tests compare `PGconn.db`, `user`, `host`, `port` with it instead of with mode-specific literals. |
| `support.is_readable(fileno, timeout)` | Wait at most `timeout` seconds; `True` when readable. For loops that repeat an action until the descriptor answers. |
| `support.wait_readable(fileno, timeout=10.0)` | Returns when readable; raises `AssertionError(f"descriptor {fileno} not readable within {timeout} s")`. |
| `support.run_gen(gen, fileno, timeout=10.0)`, `support.run_gen_conn(gen, timeout=10.0)` | Drive a generator of section 7 with `select.select`, for tests that must not depend on `stuntgres.waiting`; `AssertionError("generator not finished within its time limit")`. |
| `raw.RawConn(conninfo)` | `execute(sql) -> list[tuple[bytes | None, ...]]`, `close()`; raises `RuntimeError` with the server's message. Built on the bare declarations of section 3 so that the harness and the tests of parts before `PGconn` exists can run SQL. |

Rules for test authors (they restate C7): expectations are literals; nothing sleeps: wait
with `wait_readable`, a generator driver, or a blocking call; every wait has a bound; a
test never relies on what another test created; no test prints; a test that starts a
thread joins it before it returns (from Python 3.12 `os.fork()` in a process with other
threads raises a `DeprecationWarning`, which is an error here, and one test forks); a
whole class is restricted to modes with the class attribute `modes`, not with a
decorator.

The complete files:

```python
"""Support for the tests of bindings/cpython: cases, gates and bounded waits."""
from __future__ import annotations

import functools
import inspect
import itertools
import os
import select
import time
import unittest
from typing import Any, Callable, Generator

mode: str = "unit"
backend: Any = None

_names = itertools.count(1)


class LibraryTestCase(unittest.TestCase):
    """A test that needs the native library but no database."""

    needs = "library"


class DatabaseTestCase(LibraryTestCase):
    """A test with a database of its own; `self.conninfo` connects to it."""

    needs = "database"
    conninfo: str

    def setUp(self) -> None:
        super().setUp()
        self.conninfo = backend.acquire()
        self.addCleanup(backend.release, self.conninfo)


class AsyncDatabaseTestCase(unittest.IsolatedAsyncioTestCase):
    """The asyncio twin of DatabaseTestCase."""

    needs = "database"
    conninfo: str

    def setUp(self) -> None:
        super().setUp()
        self.conninfo = backend.acquire()
        self.addCleanup(backend.release, self.conninfo)


def _gate(
    reason: Callable[[], "str | None"],
) -> Callable[[Callable[..., Any]], Callable[..., Any]]:
    """Make a decorator that skips a test method when `reason()` returns a text."""

    def decorate(func: Callable[..., Any]) -> Callable[..., Any]:
        if inspect.iscoroutinefunction(func):
            @functools.wraps(func)
            async def async_wrapper(
                self: unittest.TestCase, *args: Any, **kwargs: Any
            ) -> Any:
                why = reason()
                if why is not None:
                    self.skipTest(why)
                return await func(self, *args, **kwargs)

            return async_wrapper

        @functools.wraps(func)
        def wrapper(self: unittest.TestCase, *args: Any, **kwargs: Any) -> Any:
            why = reason()
            if why is not None:
                self.skipTest(why)
            return func(self, *args, **kwargs)

        return wrapper

    return decorate


def only_modes(*modes: str) -> Callable[[Callable[..., Any]], Callable[..., Any]]:
    """Skip the decorated test method unless the runner's mode is one of `modes`."""

    def reason() -> "str | None":
        if mode in modes:
            return None
        return f"runs only in mode {', '.join(modes)}"

    return _gate(reason)


def requires_function(name: str) -> Callable[[Callable[..., Any]], Callable[..., Any]]:
    """Skip the decorated test method unless the loaded library has `name`."""

    def reason() -> "str | None":
        from stuntgres import pq

        if pq.has_function(name):
            return None
        return f"the loaded library has no {name}"

    return _gate(reason)


def unique_name(prefix: str = "n") -> str:
    """A name no other test of this run gets (for roles and other shared objects)."""
    return f"{prefix}_{os.getpid()}_{next(_names)}"


def conninfo_dict(conninfo: str) -> dict[str, str]:
    """The keyword=value pairs of a conninfo string that a backend made."""
    return dict(item.split("=", 1) for item in conninfo.split())


def is_readable(fileno: int, timeout: float) -> bool:
    """Wait at most `timeout` seconds; tell whether `fileno` became readable."""
    return bool(select.select([fileno], [], [], timeout)[0])


def wait_readable(fileno: int, timeout: float = 10.0) -> None:
    """Return when `fileno` is readable; fail the test after `timeout` seconds."""
    if not is_readable(fileno, timeout):
        raise AssertionError(f"descriptor {fileno} not readable within {timeout} s")


def _ready(fileno: int, wait: int, deadline: float) -> int:
    left = deadline - time.monotonic()
    if left <= 0:
        raise AssertionError("generator not finished within its time limit")
    r, w, _ = select.select(
        [fileno] if wait & 1 else [], [fileno] if wait & 2 else [], [], left
    )
    return (1 if r else 0) | (2 if w else 0)


def run_gen(gen: Generator[Any, Any, Any], fileno: int, timeout: float = 10.0) -> Any:
    """Drive a PQGen to its end without the waiting module; bounded by `timeout`."""
    deadline = time.monotonic() + timeout
    try:
        wait = next(gen)
        while True:
            wait = gen.send(_ready(fileno, wait, deadline))
    except StopIteration as stop:
        return stop.value
    finally:
        gen.close()


def run_gen_conn(gen: Generator[Any, Any, Any], timeout: float = 10.0) -> Any:
    """Drive a PQGenConn to its end without the waiting module."""
    deadline = time.monotonic() + timeout
    try:
        fileno, wait = next(gen)
        while True:
            fileno, wait = gen.send(_ready(fileno, wait, deadline))
    except StopIteration as stop:
        return stop.value
    finally:
        gen.close()
```

```python
"""A connection through the bare declarations, for the harness and early tests."""
from __future__ import annotations

from ctypes import string_at
from typing import Any

from stuntgres.pq import _ffi


class RawConn:
    """Blocking connection that raises RuntimeError on any failure."""

    def __init__(self, conninfo: str) -> None:
        self._lib: Any = _ffi.get()
        self._ptr: int | None = self._lib.PQconnectdb(conninfo.encode())
        if self._lib.PQstatus(self._ptr) != 0:
            raw = self._lib.PQerrorMessage(self._ptr) or b""
            text = raw.decode("utf-8", "replace")
            self.close()
            raise RuntimeError(f"cannot connect: {text.strip()}")

    def execute(self, sql: str) -> list[tuple[bytes | None, ...]]:
        """Run one command string; return the rows of its last result."""
        lib = self._lib
        res = lib.PQexec(self._ptr, sql.encode())
        try:
            if lib.PQresultStatus(res) not in (1, 2):
                text = lib.PQresultErrorMessage(res) or lib.PQerrorMessage(self._ptr)
                raise RuntimeError((text or b"").decode("utf-8", "replace").strip())
            rows = []
            for row in range(lib.PQntuples(res)):
                cells = []
                for col in range(lib.PQnfields(res)):
                    if lib.PQgetisnull(res, row, col):
                        cells.append(None)
                    else:
                        length = lib.PQgetlength(res, row, col)
                        cells.append(string_at(lib.PQgetvalue(res, row, col), length))
                rows.append(tuple(cells))
            return rows
        finally:
            lib.PQclear(res)

    def close(self) -> None:
        ptr, self._ptr = self._ptr, None
        if ptr is not None:
            self._lib.PQfinish(ptr)
```

### 9.7 Native mode (`tests/support/native.py`, CPYTHON-TEST-03-NATIVE)

Nothing here could run during this design: libspq does not exist yet. The backend is
specified from `docs/LIBSPQ.md` (sections 1, 5.1 and 6) and is small.

- `Backend.start()`: `path = os.path.join(repo_root, "build", "libspq", "lib",
  "libspq.so")`; if it is not a file raise
  `RuntimeError(f"{path} not found: run 'make libspq' first")`; set
  `os.environ["STUNTGRES_LIBSPQ"] = path`. No process is started.
- Isolation: **a cluster per test.** `acquire()` returns
  `host=t<NNNNN> port=5432 dbname=test user=test` (a counter); the cluster, database and
  role are created by the first connection. `release(conninfo)` calls
  `stuntgres.pq._ffi.get().PQXclusterDrop(host, b"5432", buf, len(buf))` through the bare
  declaration with a 256-byte `create_string_buffer`; a zero result raises
  `RuntimeError(f"cluster {host} not dropped: {text}")`, which fails the test that left a
  connection open.
- `stop()`: nothing.
- Tests that assert libpq-over-TCP behavior (refused connections, wrong password,
  `flush()` returning 1) carry `@support.only_modes("oracle", "server")`; section 10.3
  marks them.

### 9.8 Vendored suite (`tests/third_party/`)

| | |
|---|---|
| File | `bindings/cpython/tests/third_party/dbapi20.py` |
| Origin | `https://github.com/baztian/dbapi-compliance`, file `dbapi20.py` |
| Version | `__version__ = '1.15.0'`; commit `ea7cb1b48894a2858e79cc6709105f9f1b3948d1` (2022-10-24, head of `master`; git blob `151a1455bef39b26cdffc4827109f690e0fcf04f`) |
| Size | 31973 bytes, 850 lines |
| SHA-256 | `9d58f464d96d93e9b2b1442034711160203b0d0ca51459c2804f0cee48ca8ce9` |
| Licence statement | In the module docstring: "This software is Public Domain and may be used without restrictions." The repository has no licence file. |
| Pinned bytes | `.cswd/tmp/CPYTHON/third_party/dbapi20.py` (the implementing part copies them and proves it with `cmp`) |
| Changes | None. |

It is Stuart Bishop's DB-API 2.0 compliance suite in the copy maintained at that
repository; the commit is the released 1.15.0 plus one fix for leaked connections. The
copy in `../src-ref/psycopg/tests/dbapi20.py` was rejected: psycopg edited it (typing,
relative imports, changed DDL and parameter tests), so it is not pristine.

What is established, and how **(experiment 10)**:

- Read in full: the file imports `unittest`, `time`, `sys`; it opens no file, starts no
  process and reads no environment; every database call goes through `self.driver`.
- Compiled, not executed, under `-W error` on CPython 3.12.3, and parsed with
  `ast.parse(feature_version=(3, 10))`: no warning, no syntax newer than 3.10.
- The names that do not exist on Python 3 (`exceptions`, `StandardError`,
  `TestCase.failUnless`) occur only in the `else` branches of `sys.version[0] >= '3'`
  (line 20) and `sys.version[0] == '3'` (line 152). Every other `unittest` method it uses
  (`assertEqual`, `assertRaises`, `assertTrue`, `fail`) exists in 3.10 and 3.12.
- **Not established by execution.** The part record asks that the file be imported and
  run on Python 3.10 and 3.12 with warnings as errors. In this run the session's
  permission system refused to execute a file downloaded in the session and to pull a
  Python 3.10 image, and the host has no Python 3.10. The first test of
  CPYTHON-TEST-02-DBAPI20-VENDOR therefore imports the vendored file with warnings as
  errors, and that part stops (rule C8) if the import fails; the first run against a
  driver is the `dbapi20` subclass that CPYTHON-DESIGN-DBAPI designs. Should the file
  need a change after all, the smallest deviation is a recorded patch listed in
  `PROVENANCE.md`; that is the owner's decision, not the implementer's.

`DatabaseAPI20Test` defines 36 distinct test methods (`test_nextset` twice; the second
definition raises `NotImplementedError` and is meant to be overridden). Which of them the
driver overrides is decided in CPYTHON-DESIGN-DBAPI.

### 9.9 Layout of `tests/`

```
tests/__init__.py                 empty
tests/support/                    9.2 to 9.7
tests/third_party/                9.8 (never edited)
tests/test_<name>.py              one file per part; section 10.1 gives the name
```

Test classes are named `<Topic>Test`; a test file may hold several classes when they need
different base classes (a unit class and a database class). No test file is longer than
750 lines (rule C6).

## 10. Steps

Each step is one catalog part. A part edits only its own files (rule C2), writes its
tests first (C7) and must pass `--mode unit`, `--mode oracle` and `--mode server` of the
runner; before CPYTHON-TEST-01B-ORACLE and CPYTHON-TEST-01C-SERVER exist the two database
modes exit 3, which the earlier parts record instead. All paths are relative to `bindings/cpython/`.

### 10.1 Parts

Every part also has `CPYTHON-DESIGN-PQ` as a prerequisite. Sizes are lines of code +
lines of tests. `PQ-nn` abbreviates `CPYTHON-PQ-nn-…`, likewise `TEST-…`, `BUILD-…`.

| Part | Files | Public names | Prerequisites | Size |
|---|---|---|---|---|
| CPYTHON-BUILD-01-SKELETON | `README.md`, `pyproject.toml`, `.gitignore`, `stuntgres/__init__.py`, `stuntgres/_version.py`, `stuntgres/py.typed`, `tests/__init__.py`, `tools/runtests.py`, `tests/test_skeleton.py` | `stuntgres.__version__`; the runner | — | 190 + 90 |
| CPYTHON-PQ-01-ERRORS-BASE | `stuntgres/errors.py`, `tests/test_errors.py` | the 13 classes of section 6 | BUILD-01 | 110 + 90 |
| CPYTHON-PQ-02-ENUMS | `stuntgres/pq/__init__.py`, `stuntgres/pq/_enums.py`, `stuntgres/pq/misc.py`, `stuntgres/_gentypes.py`, `stuntgres/_encodings.py`, `tests/test_enums.py` | enums, `PGnotify`, `ConninfoOption`, `need_bytes`, `error_message`, `Wait`, `Ready`, `PQGen`, `PQGenConn`, `python_codec`, `pq.version`, `pq.has_function` | BUILD-01, PQ-01 | 260 + 140 |
| CPYTHON-PQ-03-LOADER | `stuntgres/pq/_loader.py`, `tests/test_loader.py` | `load`, `loaded_pid`, `Loaded` | PQ-02 | 90 + 170 |
| CPYTHON-PQ-04-FFI | `stuntgres/pq/_ffi.py`, `tests/test_ffi.py` | `DECLARATIONS`, `Library`, `get`, `symbol_name` | PQ-03 | 230 + 160 |
| CPYTHON-TEST-01A-SUPPORT | `tests/support/__init__.py`, `tests/support/raw.py`, `tests/test_support.py` | section 9.6 | PQ-04 | 210 + 190 |
| CPYTHON-TEST-01S-STARTER | `tests/support/libpq.py`, `tests/support/backends.py`, `tests/test_backends.py` | `backends.start`, `libpq.ensure`, `libpq.run`, `libpq.IMAGE` | TEST-01A | 85 + 110 |
| CPYTHON-TEST-01B-ORACLE | `tests/support/oracle.py`, `tests/test_backend_oracle.py` | `oracle.Backend` | TEST-01S | 110 + 70 |
| CPYTHON-TEST-01C-SERVER | `tests/support/server.py`, `tests/test_backend_server.py` | `server.Backend`, `server.user_record` | TEST-01S | 130 + 80 |
| CPYTHON-TEST-02-DBAPI20-VENDOR | `tests/third_party/__init__.py`, `tests/third_party/dbapi20.py`, `tests/third_party/PROVENANCE.md`, `tests/test_dbapi20_vendor.py` | — | BUILD-01 | 25 + 50 (plus 850 vendored lines) |
| CPYTHON-PQ-05-CONNINFO | `stuntgres/pq/_conninfo.py`, `tests/test_pq_conninfo.py` | `Conninfo` | TEST-01B, TEST-01C | 60 + 90 |
| CPYTHON-PQ-06A-PGCONN-BASE | `stuntgres/pq/_pgconn_base.py`, the seven stubs of 10.2, `tests/test_pq_base.py` | `PGconnBase` (5.3) | PQ-02 | 140 + 110 |
| CPYTHON-PQ-06-PGCONN-CONNECT | `stuntgres/pq/_pgconn.py`, `tests/test_pq_connect.py` | `PGconn` (5.4) | PQ-05, PQ-06A | 180 + 200 |
| CPYTHON-PQ-07-PGRESULT | `stuntgres/pq/_pgresult.py`, `stuntgres/pq/_rows.py`, `tests/test_pq_result.py` | `PGresult`, `read_rows` | PQ-06 | 190 + 200 |
| CPYTHON-PQ-08-EXEC | `stuntgres/pq/_params.py`, `stuntgres/pq/_pgconn_exec.py`, `tests/test_pq_exec.py` | `ExecMixin`, `make_result`, `check_sent`, the helpers of `_params` | PQ-07 | 150 + 220 |
| CPYTHON-PQ-09-PREPARED | `stuntgres/pq/_pgconn_prepared.py`, `tests/test_pq_prepared.py` | `PreparedMixin` | PQ-08, CPYTHON-ENGINE-01-DESCRIBE-SQL-CURSOR | 90 + 180 |
| CPYTHON-PQ-10-NOTICES | `stuntgres/pq/_notices.py`, `tests/test_pq_notices.py` | `register`, `unregister`, `receiver` | PQ-08 | 60 + 150 |
| CPYTHON-PQ-11-NOTIFY | `stuntgres/pq/_pgconn_notify.py`, `tests/test_pq_notify.py` | `NotifyMixin` | PQ-08 | 25 + 80 |
| CPYTHON-PQ-12-CANCEL | `stuntgres/pq/_pgcancel.py`, `stuntgres/pq/_pgconn_cancel.py`, `tests/test_pq_cancel.py` | `PGcancel`, `CancelMixin` | PQ-08 | 55 + 90 |
| CPYTHON-PQ-13-COPY | `stuntgres/pq/_pgconn_copy.py`, `tests/test_pq_copy.py` | `CopyMixin` | PQ-08 | 45 + 140 |
| CPYTHON-PQ-14-ROWMODES | `stuntgres/pq/_pgconn_rowmodes.py`, `tests/test_pq_rowmodes.py` | `RowModesMixin` | PQ-08 | 20 + 110 |
| CPYTHON-PQ-15-ESCAPING | `stuntgres/pq/_escaping.py`, `tests/test_pq_escaping.py` | `Escaping` | PQ-06 | 90 + 110 |
| CPYTHON-PQ-16-GENERATORS | `stuntgres/generators.py`, `tests/test_generators.py` | the seven generators of section 7 | PQ-08, PQ-10, PQ-11, PQ-12 | 120 + 220 |
| CPYTHON-PQ-17-WAIT-SYNC | `stuntgres/waiting.py`, `tests/test_waiting.py` | `wait`, `wait_conn` | PQ-16 | 80 + 180 |
| CPYTHON-PQ-18-WAIT-ASYNC | `stuntgres/waiting_async.py`, `tests/test_waiting_async.py` | `wait_async`, `wait_conn_async` | PQ-16 | 85 + 170 |
| CPYTHON-TEST-03-NATIVE | `tests/support/native.py`, `tests/test_backend_native.py` | `native.Backend` | TEST-01S, LIBSPQ-CABI-03-BLOCKING, LIBSPQ-CABI-14-CONTROL | 60 + 60 |
| CPYTHON-PQ-19-EXT | `stuntgres/pq/ext.py`, `tests/test_pq_ext.py` | `ext.version`, `cluster_drop`, `reset`, `dump`, `restore` | PQ-08, TEST-03, LIBSPQ-CABI-15-DUMP | 70 + 110 |
| CPYTHON-ENGINE-01-DESCRIBE-SQL-CURSOR | engine and reference test files, named by the part | — | — | 60 + 90 |
| CPYTHON-ENGINE-02-CANCEL-PLPGSQL-CONTEXT (P2) | engine and reference test files, named by the part | — | — | 30 + 60 |

Changes against the list in the part record, with the reason:

- `CPYTHON-TEST-01-HARNESS` is split into `-01A-SUPPORT` (test cases, gates, waits,
  `RawConn`), `-01S-STARTER` (libpq extraction and the backend starter), `-01B-ORACLE` and
  `-01C-SERVER` (separate files with separate owners; together they would exceed 400
  lines). Parts that
  listed TEST-01 depend on 01B and 01C.
- PQ-02 depends on PQ-01 (`_encodings.py` raises `NotSupportedError`); PQ-03 depends on
  PQ-02 rather than PQ-01 directly (the package directory `stuntgres/pq/` and its
  `__init__.py` are created by PQ-02).
- PQ-06 is split: `-06A-PGCONN-BASE` owns `_pgconn_base.py` and the stubs (unit tests
  with a fake library, so it needs only PQ-02 and can run beside the loader and harness
  parts); `-06-PGCONN-CONNECT` owns `_pgconn.py` and depends on 06A and on PQ-05 (`info`
  uses `Conninfo._options_from_array`).
- PQ-09 depends on CPYTHON-ENGINE-01 (its `describe_portal` test fails in server mode
  until the engine is fixed, section 11).
- PQ-16 also depends on PQ-10 and PQ-12 (the `cancel` generator uses `get_cancel`; its
  test synchronises on a notice). PQ-17 and PQ-18 reach PQ-10 through PQ-16.

Parts that may run side by side never share a file: after PQ-08 the parts 09 to 14 each
own one mixin module and one test file; 15 owns `_escaping.py`; 17 and 18 own one waiter
each.

### 10.2 Stubs

CPYTHON-PQ-06A-PGCONN-BASE creates these seven files so that `PGconn` can be composed
at once. Each later part replaces the whole file it owns and nothing else.

Six mixin stubs, identical but for the names
(`_pgconn_exec.py` / `ExecMixin`, `_pgconn_prepared.py` / `PreparedMixin`,
`_pgconn_notify.py` / `NotifyMixin`, `_pgconn_cancel.py` / `CancelMixin`,
`_pgconn_copy.py` / `CopyMixin`, `_pgconn_rowmodes.py` / `RowModesMixin`):

```python
"""Stub: replaced by <owner part>."""
from __future__ import annotations

from ._pgconn_base import PGconnBase


class ExecMixin(PGconnBase):
    """Methods are added by <owner part>."""

    __slots__ = ()
```

`_notices.py` stub (replaced by CPYTHON-PQ-10-NOTICES):

```python
"""Stub: replaced by CPYTHON-PQ-10-NOTICES."""
from __future__ import annotations

from typing import Any


def register(conn: Any) -> int:
    """Install nothing yet; return a key."""
    return 0


def unregister(key: int) -> None:
    """Forget a key."""
```

Until PQ-10 lands no receiver is installed, so a notice is printed to `stderr` by libpq;
no test of the parts before PQ-10 provokes one.

### 10.3 What each part does

For every part: the files, the sections to implement exactly, the step, and the tests to
write first. "Subprocess check" means: run `sys.executable -c <code>` with
`subprocess.run(..., capture_output=True, text=True, timeout=60)`, the directory
`bindings/cpython` first in `PYTHONPATH`, and assert on its output; the loader's state is
per process, so anything that must observe a fresh process is tested this way. Unless a
test says otherwise it is a method of a class with the stated base.

#### CPYTHON-BUILD-01-SKELETON

Files: `README.md`, `pyproject.toml`, `.gitignore`, `stuntgres/__init__.py`,
`stuntgres/_version.py`, `stuntgres/py.typed`, `tests/__init__.py`, `tools/runtests.py`,
`tests/test_skeleton.py`.

Design sections: 1.2, 9.1.

Step.

1. `stuntgres/_version.py`: the future import and `__version__ = "0.1.0"`.
2. `stuntgres/__init__.py`: docstring `"""stuntgres: PEP 249 driver for the embedded
   stuntgres engine."""`, the future import, `from ._version import __version__`,
   `__all__ = ["__version__"]`. Nothing else: importing it must not import `ctypes`.
3. `stuntgres/py.typed` and `tests/__init__.py`: empty files.
4. `pyproject.toml`, exactly:
   ```toml
   [project]
   name = "stuntgres"
   version = "0.1.0"
   description = "PEP 249 driver for the embedded stuntgres engine"
   requires-python = ">=3.10"
   ```
5. `.gitignore`, exactly five lines: `__pycache__/`, `*.pyc`, `/stuntgres/_lib/`,
   `/dist/`, `/build/`.
6. `README.md`: a title `# stuntgres for Python`, one paragraph saying that this
   directory holds the PEP 249 driver of the embedded engine, built on libspq
   (`docs/LIBSPQ.md`) with `ctypes`, standard library only, CPython 3.10 or newer, and
   that it is under construction; a section `## Testing` with the command line of 9.1 and
   the table of modes of 9.1. No feature list.
7. `tools/runtests.py`: the complete file of section 9.1, unchanged.

Tests to write first (`tests/test_skeleton.py`, class `SkeletonTest(unittest.TestCase)`;
`ROOT` is `bindings/cpython`, computed from `__file__`; `RUNNER` is
`ROOT/tools/runtests.py`):

- `test_version`: `stuntgres.__version__ == "0.1.0"`.
- `test_pyproject`: the text of `pyproject.toml` contains the lines `name = "stuntgres"`,
  `version = "0.1.0"` and `requires-python = ">=3.10"`.
- `test_import_is_free_of_side_effects`: subprocess check; the code imports `stuntgres`,
  reads `/proc/self/maps` (with a `with` block) and prints
  `threading.active_count()` and whether the text contains `libpq` or `libspq`: the
  output is `1 False`. (Do not assert on `sys.modules`: a later part makes this package
  import its upper layers.)
- `test_runner_selector`: `[sys.executable, RUNNER, "--mode", "unit",
  "test_skeleton.SkeletonTest.test_version"]` exits 0 and its `stderr` contains
  `Ran 1 test`.
- `test_runner_usage`: `--mode bogus` exits 2.
- `test_runner_unknown_module`: `--mode unit nosuch_module` exits 1.
- `test_runner_nothing_selected`: `--mode unit test_skeleton.NeedsLibraryProbe` exits 4 and
  `stderr` is `runtests: no tests selected\n`. The file also defines
  `class NeedsLibraryProbe(unittest.TestCase)` with `needs = "library"` and one method
  `test_dropped_in_unit_mode` whose body is `self.assertIn("needs", vars(type(self)))`.
- `test_runner_interrupted`: write a module `tests/_block_<pid>.py` into `ROOT/tests`
  (removed with `addCleanup`) whose single test prints `BLOCKING` (flushed) to standard
  output and then calls `signal.pause()`; start the runner on it with
  `subprocess.Popen(..., stdout=PIPE, stderr=PIPE, text=True)`; read the line `BLOCKING`
  from its standard output; `proc.send_signal(signal.SIGTERM)`; `proc.communicate(timeout=
  60)`: the exit code is 130 and `stderr` contains `runtests: interrupted`.
- `test_runner_unraisable`: write a module `tests/_scratch_<pid>.py` into `ROOT/tests`
  (remove it with `addCleanup`) whose single test creates and drops an object whose
  `__del__` raises `ValueError("in finalizer")`; the runner with that selector exits 1 and
  `stderr` contains `ValueError: in finalizer`.

Acceptance: `python3 bindings/cpython/tools/runtests.py --mode unit` prints `OK` and
exits 0; `--mode native` exits 3 with `runtests: cannot start native mode: No module
named 'tests.support'`.

#### CPYTHON-PQ-01-ERRORS-BASE

Files: `stuntgres/errors.py`, `tests/test_errors.py`. Design sections: 6.

Step. Write the module exactly as section 6 specifies: 13 classes in the order shown,
each with a docstring and `__module__ = "stuntgres"`, `Error.__init__`, `Error.diag`,
`_diag_factory = None`.

Tests to write first (`ErrorsTest(unittest.TestCase)`):

- `test_hierarchy`: `Warning` and `Error` are subclasses of `Exception` and not of each
  other; `InterfaceError` and `DatabaseError` of `Error`; `DataError`, `OperationalError`,
  `IntegrityError`, `InternalError`, `ProgrammingError`, `NotSupportedError` of
  `DatabaseError`; `InterfaceError` is not a `DatabaseError`; `ConnectionTimeout`,
  `CancellationTimeout`, `WaitTimeout` of `OperationalError`.
- `test_module_name`: `repr(errors.OperationalError) == "<class 'stuntgres.OperationalError'>"`.
- `test_attributes`: `err = errors.OperationalError("boom")`; `str(err) == "boom"`;
  `err.args == ("boom",)`; `err.pgconn is None`; `err.pgresult is None`;
  `err.sqlstate is None`; `err.diag is None`.
- `test_keyword_arguments`: with two `object()` values, `errors.Error("x", pgconn=a,
  pgresult=b)` has `pgconn is a` and `pgresult is b`.
- `test_diag_factory`: set `errors._diag_factory = lambda err: ("diag", err)` (restore
  `None` in a `finally`); `err.diag == ("diag", err)`.
- `test_sqlstate_on_subclass`: a local `class Custom(errors.DataError): sqlstate = "22012"`
  gives `Custom("x").sqlstate == "22012"`.
- `test_runtime_imports`: the source text of `stuntgres/errors.py` contains no `import
  ctypes`, and its only import of the package is the line `from .pq import PGconn,
  PGresult` indented under `if TYPE_CHECKING:`.

#### CPYTHON-PQ-02-ENUMS

Files: `stuntgres/pq/__init__.py`, `stuntgres/pq/_enums.py`, `stuntgres/pq/misc.py`,
`stuntgres/_gentypes.py`, `stuntgres/_encodings.py`, `tests/test_enums.py`. Design
sections: 4.1, 4.2, 4.3, 5.1, 7.1.

Step.

1. `_enums.py`: the seven enums of 4.1 (`ConnStatus`, `PollingStatus`, `ExecStatus`,
   `TransactionStatus`, `Ping`, `Format`, `DiagnosticField`) with every member and value
   of the table.
2. `misc.py`: section 4.2.
3. `stuntgres/_gentypes.py`: section 7.1 (the code block).
4. `stuntgres/_encodings.py`: section 4.3, the 40 keys written out one per line.
5. `pq/__init__.py`: the complete file of section 5.1.

Tests to write first (`EnumsTest(unittest.TestCase)`):

- `test_values`: for each enum, `{m.name: m.value for m in Enum}` equals the literal
  dictionary written out from the table of 4.1 (for example `ExecStatus`:
  `EMPTY_QUERY` 0, `COMMAND_OK` 1, `TUPLES_OK` 2, `COPY_OUT` 3, `COPY_IN` 4,
  `BAD_RESPONSE` 5, `NONFATAL_ERROR` 6, `FATAL_ERROR` 7, `COPY_BOTH` 8, `SINGLE_TUPLE` 9,
  `PIPELINE_SYNC` 10, `PIPELINE_ABORTED` 11, `TUPLES_CHUNK` 12; `DiagnosticField.SQLSTATE`
  67, `MESSAGE_PRIMARY` 77, `SEVERITY` 83, `SEVERITY_NONLOCALIZED` 86).
- `test_gentypes`: `Wait.R, Wait.W, Wait.RW == 1, 2, 3`; `Ready.NONE, Ready.R, Ready.W,
  Ready.RW == 0, 1, 2, 3`; `Wait.RW & Ready.R == 1`.
- `test_plain_data`: `PGnotify(b"ch", 7, b"x")` has `relname`, `be_pid`, `extra` in that
  order; `ConninfoOption._fields == ("keyword", "envvar", "compiled", "val", "label",
  "dispchar", "dispsize")`.
- `test_need_bytes`: `misc.need_bytes(b"x") == b"x"`; `need_bytes("x")` raises `TypeError`
  with the text `bytes expected, got str`; `need_bytes(bytearray(b"x"))` raises `bytes
  expected, got bytearray`; `need_bytes(None)` raises `bytes expected, got NoneType`.
- `test_error_message`: with `types.SimpleNamespace(error_message=…)`:
  `b"ERROR:  x\n"` gives `"ERROR:  x"`; `b""` gives `"no error details available"`;
  `b"\xff\n"` gives `"�"`; `b"\xe9"` with encoding `"latin1"` gives `"\xe9"`.
- `test_python_codec`: `b"UTF8"` -> `"utf-8"`, `b"LATIN1"` -> `"iso8859-1"`, `b"LATIN9"` ->
  `"iso8859-15"`, `b"WIN1252"` -> `"cp1252"`, `b"SJIS"` -> `"shift_jis"`, `b"EUC_JP"` ->
  `"euc_jp"`, `b"SQL_ASCII"` -> `"ascii"`, `b"latin1"` -> `"iso8859-1"`, `b"ISO_8859_5"`
  -> `"iso8859-5"`; `b"EUC_TW"` raises `NotSupportedError` with the text
  `client encoding EUC_TW has no Python codec`.
- `test_codec_table`: `len(_encodings._CODECS) == 40` and `codecs.lookup(value)` succeeds
  for every value.
- `test_package`: `pq.__impl__ == "ctypes"`; `"PGconn" in pq.__all__`; `pq.ExecStatus is
  _enums.ExecStatus`; `getattr(pq, "nosuch")` raises `AttributeError` with the text
  `module 'stuntgres.pq' has no attribute 'nosuch'`.
- `test_import_is_free_of_side_effects`: subprocess check; the code imports
  `stuntgres.pq`, `stuntgres._gentypes` and `stuntgres._encodings`, reads
  `/proc/self/maps` and prints `threading.active_count()` and whether the text contains
  `libpq` or `libspq`: `1 False`.

#### CPYTHON-PQ-03-LOADER

Files: `stuntgres/pq/_loader.py`, `tests/test_loader.py`. Design sections: 2.

Step. Write the module exactly as section 2 specifies.

Tests to write first (`LoaderTest(unittest.TestCase)`; all in unit mode with fakes, no
native library). `setUp` saves `_loader._loaded`, sets it to `None` and restores it in a
cleanup; the environment is changed with `unittest.mock.patch.dict(os.environ, …,
clear=False)` after removing both variables; `ctypes.CDLL` and `ctypes.PyDLL` are replaced
with `mock.patch.object(_loader.ctypes, "CDLL", fake_cdll)` and likewise `PyDLL`. The fake
`CDLL(path)` appends `path` to a list and returns an object with `_handle = 7`; the fake
`PyDLL(path, handle=None)` records its arguments.

- `test_not_found`: no variable, `_loader.BUNDLED` patched to
  `"/nonexistent/_lib/libspq.so"`: `InterfaceError` with the text `libspq not found: set
  STUNTGRES_LIBSPQ to the absolute path of libspq.so or install it as
  /nonexistent/_lib/libspq.so`.
- `test_relative_path`: `STUNTGRES_LIBSPQ=relative/libspq.so`: `STUNTGRES_LIBSPQ must be
  an absolute path, got 'relative/libspq.so'`.
- `test_missing_file`: `STUNTGRES_LIBSPQ=/nonexistent/libspq.so`: `STUNTGRES_LIBSPQ names
  a missing file: '/nonexistent/libspq.so'`.
- `test_loads_libspq`: the variable names a file made with `tempfile.NamedTemporaryFile`;
  `load()` returns `Loaded` with that `path`, `flavor == "libspq"`, `pid == os.getpid()`;
  the `CDLL` list is `[path]`; `PyDLL` got `(path, handle=7)`; `loaded_pid() ==
  os.getpid()`.
- `test_bundled`: no variable, `BUNDLED` patched to an existing temporary file: loaded
  from it, flavor `"libspq"`.
- `test_seam`: `_STUNTGRES_TEST_LIBPQ="/a/dep1" + os.pathsep + "/a/dep2" + os.pathsep +
  "/a/libpq.so.5"` and `STUNTGRES_LIBSPQ=/nonexistent/x`: the `CDLL` list is `["/a/dep1",
  "/a/dep2", "/a/libpq.so.5"]`, `flavor == "libpq"`, `path == "/a/libpq.so.5"`.
- `test_signals_blocked_and_restored`: the fake `CDLL` records
  `signal.pthread_sigmask(signal.SIG_BLOCK, [])`; the recorded set equals
  `set(signal.valid_signals()) - {signal.SIGKILL, signal.SIGSTOP}`; after `load()` the
  mask equals the mask before.
- `test_load_error`: with `_STUNTGRES_TEST_LIBPQ=/a/libpq.so.5` and a fake `CDLL` that
  raises `OSError("boom")`: `InterfaceError` with the text `cannot load '/a/libpq.so.5':
  boom`; the mask is restored; `_loader._loaded is None`; a
  second `load()` with a working fake succeeds.
- `test_idempotent`: two calls return the same object; `CDLL` was called once.
- `test_threads`: 8 threads call `load()`; one object, one `CDLL` call.
- `test_worker_thread`: `load()` called from a `threading.Thread` succeeds.
- `test_fork_rule`: `_loader._loaded = Loaded(None, None, "/x", "libspq", os.getpid() + 1)`;
  `load()` raises `InterfaceError` with the text `libspq cannot be used in a process
  forked after the library was loaded`.
- `test_loaded_pid_before`: `loaded_pid() is None`.
- `test_bundled_path`: `_loader.BUNDLED.endswith("/stuntgres/_lib/libspq.so")` and
  `os.path.isabs(_loader.BUNDLED)`.

#### CPYTHON-PQ-04-FFI

Files: `stuntgres/pq/_ffi.py`, `tests/test_ffi.py`. Design sections: 3.

Step. Write the module as sections 3.1 and 3.2 specify, with `DECLARATIONS` holding the
94 rows of 3.4 in order, grouped by the families of `docs/LIBSPQ.md` section 3 with a
comment line per family.

Tests to write first (`FfiTest(unittest.TestCase)`; unit mode with fakes). A fake DLL is
an object whose `__getattr__(name)` returns a new `types.SimpleNamespace(symbol=name,
dll=<tag>)` when `name` is in its set of symbols and raises `AttributeError` otherwise. A
fake `Loaded` is `_loader.Loaded(fake_cdll, fake_pydll, "/fake/lib.so", flavor, os.getpid())`.

- `test_table_shape`: `len(DECLARATIONS) == 94`; names are unique;
  `sum(d.required for d in DECLARATIONS) == 82`; the set of optional names equals
  `{"PQsendClosePrepared", "PQsendClosePortal", "PQsocketPoll", "PQgetCurrentTimeUSec",
  "PQsetChunkedRowsMode", "PQclosePrepared", "PQclosePortal", "PQXversion",
  "PQXclusterDrop", "PQXreset", "PQXdump", "PQXrestore"}`; the set of names with
  `gil == "held"` equals `{"PQresultStatus", "PQntuples", "PQnfields", "PQbinaryTuples",
  "PQfname", "PQftable", "PQftablecol", "PQfformat", "PQftype", "PQfsize", "PQfmod",
  "PQgetvalue", "PQgetlength", "PQgetisnull", "PQnparams", "PQparamtype", "PQcmdStatus",
  "PQcmdTuples", "PQoidValue", "PQresultErrorMessage", "PQresultErrorField"}`.
- `test_rows`: the row `PQgetvalue` has `restype is c_void_p` and `argtypes ==
  (c_void_p, c_int, c_int)`; `PQexec`: `c_void_p`, `(c_void_p, c_char_p)`;
  `PQsendQueryParams`: `c_int`, `(c_void_p, c_char_p, c_int, POINTER(c_uint),
  POINTER(c_char_p), POINTER(c_int), POINTER(c_int), c_int)`; `PQescapeLiteral`:
  `c_void_p`, `(c_void_p, c_char_p, c_size_t)`; `PQfreemem`: `None`, `(c_void_p,)`;
  `PQnotifies`: `POINTER(PGnotify_struct)`; `PQsetNoticeReceiver`: `c_void_p`,
  `(c_void_p, NoticeReceiver, c_void_p)`; `PQXdump`: `c_int`, `(c_void_p,
  POINTER(c_void_p), POINTER(c_size_t))`.
- `test_symbol_name`: `("PQexec", "libspq")` -> `"SPQexec"`; `("PQXreset", "libspq")` ->
  `"SPQXreset"`; `("pg_encoding_to_char", "libspq")` -> `"spg_encoding_to_char"`;
  `("PQexec", "libpq")` -> `"PQexec"`.
- `test_bind_libspq`: both fake DLLs know `symbol_name(d.name, "libspq")` of every row:
  `lib.PQexec.symbol == "SPQexec"` and `lib.PQexec.dll == "cdll"`; `lib.PQntuples.dll ==
  "pydll"`; `lib.PQexec.restype is c_void_p`; `lib.PQexec.argtypes == [c_void_p,
  c_char_p]`; `lib.has("PQXreset")`; `lib.flavor == "libspq"`; `lib.pid == os.getpid()`;
  `lib.path == "/fake/lib.so"`.
- `test_bind_libpq_optional_missing`: flavor `"libpq"`, the fakes know every row's name
  except the five `PQX` rows and `PQsetChunkedRowsMode`: binding succeeds;
  `lib.has("PQXversion") is False`; `lib.has("PQsetChunkedRowsMode") is False`;
  `lib.has("PQexec") is True`; `lib.PQXversion()` raises `NotSupportedError` with the text
  `PQXversion is not available in the loaded library`; `lib.PQsetChunkedRowsMode(1, 2)`
  raises the same class.
- `test_required_missing`: flavor `"libspq"`, the fake lacks `SPQconnectdb`:
  `InterfaceError` with the text `'/fake/lib.so' lacks the required function
  SPQconnectdb`.
- `test_get`: `_ffi._library` saved, set to `None` and restored; `_loader.load` patched to
  return a fake `Loaded` and count calls: two `get()` calls return the same object and
  `load` was called twice (once to bind, once as the fork check).
- `test_structures`: `[f[0] for f in PGnotify_struct._fields_] == ["relname", "be_pid",
  "extra", "next"]`; `ctypes.sizeof(PGnotify_struct) == 32`;
  `ctypes.sizeof(PQconninfoOption_struct) == 56`.
- `test_import_loads_nothing`: subprocess check, code
  `import stuntgres.pq._ffi, stuntgres.pq._loader as l; print(l.loaded_pid())` prints
  `None`.

#### CPYTHON-TEST-01A-SUPPORT

Files: `tests/support/__init__.py`, `tests/support/raw.py`, `tests/test_support.py`.
Design sections: 9.6.

Step. Write the two support files as the complete files of 9.6. The classes
`SupportDatabaseTest` and `SupportAsyncDatabaseTest` below need a database mode; they
first run when CPYTHON-TEST-01B-ORACLE or CPYTHON-TEST-01C-SERVER exists. This part's own
acceptance is unit mode.

Tests to write first (`tests/test_support.py`).

`SupportUnitTest(unittest.TestCase)`:

- `test_wait_readable`: on a `socket.socketpair()`: `support.is_readable(a.fileno(), 0.01)
  is False`; `support.wait_readable(a.fileno(), 0.01)` raises `AssertionError` with the
  text `descriptor <fd> not readable within 0.01 s`; after `b.send(b"x")`:
  `is_readable(…, 10.0) is True` and `wait_readable` returns `None`.
- `test_unique_name`: two calls differ; `unique_name("r")` matches `^r_\d+_\d+$`.
- `test_run_gen`: a local generator that yields `1` (wait for read) until it is sent a
  non-zero value and then returns `("done", <that value>)`: on the readable end of a
  socket pair `support.run_gen(gen, fd)` returns `("done", 1)`; with nothing to read and
  `timeout=0.01` it raises `AssertionError` with the text `generator not finished within
  its time limit` and the generator is closed (`gen.gi_frame is None`).
- `test_run_gen_conn`: a generator that yields `(fd, 2)` (wait for write) on a socket and
  returns what it is sent: `support.run_gen_conn(gen) == 2`.
- `test_only_modes`: with `support.mode` patched to `"unit"`
  (`mock.patch.object(support, "mode", "unit")`), a function decorated with
  `@support.only_modes("oracle")` and called with a `unittest.TestCase` instance raises
  `unittest.SkipTest` with the text `runs only in mode oracle`; patched to `"oracle"` it
  returns its value.
- `test_gates_keep_coroutine_functions`: an `async def` decorated with
  `@support.only_modes("oracle")` and one decorated with
  `@support.requires_function("PQexec")` still satisfy `inspect.iscoroutinefunction`; with
  the mode patched to `"oracle"`, `asyncio.run(body(self)) == "ran"`; patched to `"unit"`,
  `asyncio.run(body(self))` raises `unittest.SkipTest`.
- `test_conninfo_dict`: `support.conninfo_dict("host=127.0.0.1 port=5 dbname=t00001
  user=u") == {"host": "127.0.0.1", "port": "5", "dbname": "t00001", "user": "u"}`.
- `test_case_markers`: `support.LibraryTestCase.needs == "library"`;
  `support.DatabaseTestCase.needs == "database"`; `support.AsyncDatabaseTestCase.needs ==
  "database"`.

`SupportDatabaseTest(support.DatabaseTestCase)` (`setUp` calls `super().setUp()`, then
opens `self.conn = RawConn(self.conninfo)` and registers `close` as a cleanup):

- `test_isolated_a` and `test_isolated_b`, identical bodies: `conn.execute("select
  count(*) from pg_class where relname = 'isolated'") == [(b"0",)]`; `conn.execute("create
  table isolated (a int4)") == []`; `conn.execute("select count(*) from isolated") ==
  [(b"0",)]`.
- `test_raw_rows`: `conn.execute("select 1, null::text, ''") == [(b"1", None, b"")]`.
- `test_raw_error`: `conn.execute("select 1 from nosuch")` raises `RuntimeError` whose
  text starts with `ERROR:  relation "nosuch" does not exist`.
- `test_raw_cannot_connect`, `@support.only_modes("oracle", "server")`:
  `RawConn("host=127.0.0.1 port=1 sslmode=disable")` raises `RuntimeError` whose text
  starts with `cannot connect: connection to server at "127.0.0.1", port 1 failed:
  Connection refused`.

`SupportAsyncDatabaseTest(support.AsyncDatabaseTestCase)`:

- `async def test_database`: a `RawConn(self.conninfo)` answers `select 1` with
  `[(b"1",)]`.
- `async def test_gated_coroutine_runs`, `@support.only_modes("oracle", "server",
  "native")`: `await asyncio.sleep(0)`, then `select 2` gives `[(b"2",)]` (proves the
  decorated coroutine is awaited).
- `async def test_gated_coroutine_skips`, `@support.only_modes("nosuchmode")`: its body is
  `self.fail("must be skipped")`; the run reports one skip for it.

#### CPYTHON-TEST-01S-STARTER

Files: `tests/support/libpq.py`, `tests/support/backends.py`, `tests/test_backends.py`.
Design sections: 9.2, 9.3.

Step. Write the two files as the complete files of 9.3 and 9.2. Until
CPYTHON-TEST-01B-ORACLE and CPYTHON-TEST-01C-SERVER land, `--mode oracle` and `--mode
server` exit 3 (`No module named 'tests.support.oracle'`); that is expected for this part.

Tests to write first (`tests/test_backends.py`).

`StarterUnitTest(unittest.TestCase)`:

- `test_reference_image`: read `reference/fixture/fixture.go` of the repository (the
  repository root is four `os.path.dirname` calls above the absolute path of the test
  file: `tests`, `cpython`, `bindings`, root); `re.findall(r'^const referenceImage =
  "([^"]+)"$', text, re.M)` equals `[libpq.IMAGE]`, and `libpq.IMAGE` equals
  `postgres:18.6-bookworm@sha256:afc7e2d441324c0388fa80c3d24f733b4194a4eb7f47dd8ee2b08eb1a24a647c`.
- `test_unknown_mode`: `backends.start("bogus", "/tmp", "/tmp")` raises `ValueError` with
  the text `unknown mode 'bogus'`.

`LoadedLibraryTest(support.LibraryTestCase)`, every method decorated
`@support.only_modes("oracle", "server")`:

- `test_loaded_library`: `pq.version() == 180006`; `pq.has_function("PQsetChunkedRowsMode")`;
  `pq.has_function("SPQclosePrepared")`; `not pq.has_function("PQXversion")`; `not
  pq.has_function("nosuch")`; `_ffi.get().flavor == "libpq"`.
- `test_lazy_load_in_a_fresh_process`: subprocess check (the child inherits
  `_STUNTGRES_TEST_LIBPQ`); the code prints, before and after `pq.version()`, whether
  `/proc/self/maps` contains `libpq.so.5.18`, and `threading.active_count()`: the output
  is `False 1\nTrue 1\n`.
- `test_forked_child_is_refused`: after `pq.version()` in the test process, `os.fork()`;
  the child calls `_ffi.get()`, writes `str(exc)` of the `InterfaceError` to a pipe and
  leaves with `os._exit(0)` (in a `finally`); the parent reads `libspq cannot be used in a
  process forked after the library was loaded` and `os.waitpid` reports exit status 0.

#### CPYTHON-TEST-01B-ORACLE

Files: `tests/support/oracle.py`, `tests/test_backend_oracle.py`. Design sections: 9.4.

Step. Write `tests/support/oracle.py` as the complete file of 9.4.

Tests to write first (`OracleBackendTest(support.DatabaseTestCase)` with the class
attribute `modes = ("oracle",)`, so the class runs in oracle mode only):

- `test_conninfo`: `self.conninfo` matches
  `^host=127\.0\.0\.1 port=\d+ dbname=t\d{5} user=stuntgres password=stuntgres sslmode=disable$`.
- `test_server_facts`: with a `RawConn`: `show server_version_num` -> `[(b"180006",)]`;
  `show server_encoding` -> `[(b"UTF8",)]`; `show max_prepared_transactions` ->
  `[(b"100",)]`; `select datcollate, datctype from pg_database where datname =
  current_database()` -> `[(b"C", b"C")]`; `select current_database()` matches
  `^t\d{5}$`.
- `test_release_drops_the_database`: `extra = support.backend.acquire()`; connect, create a
  table, close; `support.backend.release(extra)`; a new `RawConn(extra)` raises
  `RuntimeError` whose text ends with `does not exist`.
- `test_release_with_an_open_connection`: `extra = support.backend.acquire()`; a `RawConn`
  to it stays open; `support.backend.release(extra)` returns (the server ends the
  session); close the stale object in a cleanup.
- `test_container_label`: `libpq.run("docker", "ps", "--filter",
  "label=stuntgres.cpython.pid=" + str(os.getpid()), "--format", "{{.Names}}") ==
  "stuntgres-cpython-" + str(os.getpid())`.

Acceptance: `--mode oracle` runs every test of the suite and exits 0; afterwards
`docker ps -a --filter label=stuntgres.cpython.pid --format '{{.Names}}'` prints nothing.

#### CPYTHON-TEST-01C-SERVER

Files: `tests/support/server.py`, `tests/test_backend_server.py`. Design sections: 9.5.

Step. Write `tests/support/server.py` as the complete file of 9.5.

Tests to write first.

`ServerRecordTest(unittest.TestCase)` (unit):

- `test_user_record`: `record = server.user_record(bytes(16))`; it starts with
  `stuntgres:$pbkdf2-sha256$i=4096$AAAAAAAAAAAAAAAAAAAAAA$2sbLJQcWpdJ/6HewQncqEPkQgOwXem1CS4M3aeGY9d8:t000,t001,`;
  it ends with `,t062,t063\n`; `len(record) == 419`.
- `test_pool`: `server.POOL == 64`; `server.NAMES[0] == "t000"`; `server.NAMES[-1] ==
  "t063"`.

`ServerBackendTest(support.DatabaseTestCase)` with `modes = ("server",)`:

- `test_conninfo`: matches
  `^host=127\.0\.0\.1 port=\d+ dbname=t\d{3} user=stuntgres password=stuntgres sslmode=disable$`.
- `test_connects`: `RawConn(self.conninfo).execute("select current_database()")` gives
  one row whose value is the `dbname` of `self.conninfo`.
- `test_pool_rolls_over`: acquire 70 conninfo strings in a loop (no release is needed);
  the last three connect and `create table once (a int4 primary key)` succeeds on each;
  the set of ports in the 70 strings has at least 2 members; `dbname=t000` occurs at
  least once.
- `test_wrong_password`: `RawConn(self.conninfo.replace("password=stuntgres",
  "password=wrong"))` raises `RuntimeError` whose text ends with `FATAL:  password
  authentication failed for user "stuntgres"`.

Acceptance: `--mode server` runs every test of the suite and exits 0; afterwards no
process `build/cpython/stuntgres serve` of this runner is left and
`build/cpython/server-<pid>` is gone.

#### CPYTHON-TEST-02-DBAPI20-VENDOR

Files: `tests/third_party/__init__.py` (empty), `tests/third_party/dbapi20.py`,
`tests/third_party/PROVENANCE.md`, `tests/test_dbapi20_vendor.py`. Design sections: 9.8.

Step.

1. Write `tests/test_dbapi20_vendor.py` first and see it fail.
2. `cp .cswd/tmp/CPYTHON/third_party/dbapi20.py
   bindings/cpython/tests/third_party/dbapi20.py` (from the integration checkout's root;
   in a worktree `.cswd` is a link). Then `cmp` of the two files must print nothing. If
   the pinned file is missing, fetch
   `https://raw.githubusercontent.com/baztian/dbapi-compliance/ea7cb1b48894a2858e79cc6709105f9f1b3948d1/dbapi20.py`
   and check the SHA-256 below. Never edit the file, not even its whitespace.
3. `PROVENANCE.md`: the table of section 9.8 (file, origin, version and commit, size,
   SHA-256, licence statement, "Changes: none") and one sentence: the file is vendored
   unchanged and must not be edited; an update replaces the file and this record together.
4. If any test below fails because of the content of the vendored file (an import error
   or a warning raised as an error), stop and report (rule C8); do not patch the file.

Tests to write first (`VendoredSuiteTest(unittest.TestCase)`; `PATH` is the vendored
file):

- `test_bytes`: `hashlib.sha256(<file bytes>).hexdigest() ==
  "9d58f464d96d93e9b2b1442034711160203b0d0ca51459c2804f0cee48ca8ce9"` and the length is
  31973.
- `test_imports_with_warnings_as_errors`: inside `warnings.catch_warnings()` with
  `simplefilter("error")`: `importlib.import_module("tests.third_party.dbapi20")`
  succeeds; `module.__version__ == "1.15.0"`; `issubclass(module.DatabaseAPI20Test,
  unittest.TestCase)`. Import the module inside the test and never bind the class at the
  top level of the test file (the loader would collect it).
- `test_method_count`: 36 names of `vars(module.DatabaseAPI20Test)` start with `test_`.
- `test_provenance`: `PROVENANCE.md` contains the SHA-256, the commit
  `ea7cb1b48894a2858e79cc6709105f9f1b3948d1` and the sentence `This software is Public
  Domain and may be used without restrictions.`

Acceptance: `cmp bindings/cpython/tests/third_party/dbapi20.py
.cswd/tmp/CPYTHON/third_party/dbapi20.py` prints nothing; `sha256sum` prints the value
above; when a `python3.10` is on `PATH`, `python3.10 bindings/cpython/tools/runtests.py
--mode unit test_dbapi20_vendor` passes too (record in the commit body whether 3.10 was
available).

#### CPYTHON-PQ-05-CONNINFO

Files: `stuntgres/pq/_conninfo.py`, `tests/test_pq_conninfo.py`. Design sections: 5.2.

Step. Write `Conninfo` as section 5.2 specifies.

Tests to write first (`ConninfoTest(support.LibraryTestCase)`; `Conninfo` is imported as
`from stuntgres.pq import Conninfo`):

- `test_defaults`: `opts = Conninfo.get_defaults()`; every item is a `ConninfoOption`;
  `len(opts) >= 40`; the keywords include `b"host"`, `b"port"`, `b"dbname"`, `b"user"`,
  `b"password"`, `b"options"`, `b"application_name"`, `b"client_encoding"`, `b"sslmode"`,
  `b"connect_timeout"`.
- `test_default_port_option`, `@support.only_modes("oracle", "server")`: the option with
  keyword `b"port"` equals `ConninfoOption(keyword=b"port", envvar=b"PGPORT",
  compiled=b"5432", val=b"5432", label=b"Database-Port", dispchar=b"", dispsize=6)`.
- `test_parse`: `{o.keyword: o.val for o in Conninfo.parse(b"host=h1 port=5433
  dbname='my db' user=u")}` has `b"host"` -> `b"h1"`, `b"port"` -> `b"5433"`, `b"dbname"`
  -> `b"my db"`, `b"user"` -> `b"u"`, `b"password"` -> `None`.
- `test_parse_uri`: `b"postgresql://u:pw@h2:7/d2?application_name=app"` gives `b"h2"`,
  `b"7"`, `b"d2"`, `b"u"`, `b"pw"` and `b"application_name"` -> `b"app"`.
- `test_parse_empty`: `all(o.val is None for o in Conninfo.parse(b""))`.
- `test_parse_errors`: `OperationalError` with exactly these texts: `b"nonsense"` ->
  `missing "=" after "nonsense" in connection info string`; `b"nosuchkw=1"` -> `invalid
  connection option "nosuchkw"`; `b"host='abc"` -> `unterminated quoted string in
  connection info string`.
- `test_parse_type`: `Conninfo.parse("host=x")` raises `TypeError` with the text `bytes
  expected, got str`.

#### CPYTHON-PQ-06A-PGCONN-BASE

Files: `stuntgres/pq/_pgconn_base.py`, the seven stubs `stuntgres/pq/_pgconn_exec.py`,
`_pgconn_prepared.py`, `_pgconn_notify.py`, `_pgconn_cancel.py`, `_pgconn_copy.py`,
`_pgconn_rowmodes.py`, `_notices.py`, and `tests/test_pq_base.py`. Design sections: 5.3,
10.2.

Step.

1. The seven stubs, exactly as 10.2.
2. `_pgconn_base.py` as 5.3.

Tests to write first (`BaseTest(unittest.TestCase)`; unit mode, no native library). The
fake library is `types.SimpleNamespace(pid=os.getpid(), PQfinish=finished.append,
PQsetNoticeReceiver=lambda *args: None)` with a list `finished` (the last attribute is
there because the real `_notices.register` of CPYTHON-PQ-10-NOTICES calls it; the stub
does not); `conn = PGconnBase(lib, 123)`.

- `test_init`: `conn.pgconn_ptr == 123`; `conn.notice_handler is None`;
  `conn.notify_handler is None`; `isinstance(conn._notice_key, int)`; `conn._lib is lib`;
  `not hasattr(conn, "__dict__")`.
- `test_ensure`: `conn._ensure() == 123`; after `conn.finish()` it raises
  `OperationalError` with the text `the connection is closed`.
- `test_finish_is_idempotent`: `finish()` twice: `finished == [123]`; `pgconn_ptr is
  None`.
- `test_finalizer`: `conn.__del__()` finishes it (`finished == [123]`); a second
  `__del__()` does nothing.
- `test_finalizer_in_another_process`: a fake library with `pid=os.getpid() + 1`:
  `__del__()` leaves `finished == []` and `pgconn_ptr == 123`; an explicit `finish()` still
  calls `PQfinish`.
- `test_repr`: `repr(conn).startswith("<stuntgres.pq.PGconn at 0x")`.
- `test_stubs`: for each pair (`_pgconn_exec`, `ExecMixin`), (`_pgconn_prepared`,
  `PreparedMixin`), (`_pgconn_notify`, `NotifyMixin`), (`_pgconn_cancel`, `CancelMixin`),
  (`_pgconn_copy`, `CopyMixin`), (`_pgconn_rowmodes`, `RowModesMixin`): the module imports,
  the class is a subclass of `PGconnBase` and its `__slots__ == ()`. Assert only this
  (later parts replace the stubs and add methods).
- `test_composition`: a local `class Composed(PreparedMixin, RowModesMixin, CancelMixin,
  NotifyMixin, CopyMixin, ExecMixin): __slots__ = ()` can be defined and instantiated with
  the fake library; `Composed.__mro__[-2] is PGconnBase`.

#### CPYTHON-PQ-06-PGCONN-CONNECT

Files: `stuntgres/pq/_pgconn.py`, `tests/test_pq_connect.py`. Design sections: 5.4.

Step. Write `_pgconn.py` as 5.4, with the bases in the stated order.

There is no `exec_` yet: the tests below use only members of this part, and the bare
declaration `conn._lib.PQexec` where a statement is unavoidable (they clear the result
with `conn._lib.PQclear`).

Tests to write first (`ConnectTest(support.DatabaseTestCase)`; `DSN =
self.conninfo.encode()`; a helper `connect()` returns `PGconn.connect(DSN)`, asserts
`status == ConnStatus.OK` and registers `finish` as a cleanup):

- `test_connected`: `status == 0`; `transaction_status == 0`; `error_message == b""`;
  with `want = support.conninfo_dict(self.conninfo)`: `db == want["dbname"].encode()`,
  `user == want["user"].encode()`, `host == want["host"].encode()`, `port ==
  want["port"].encode()`, `password == want.get("password", "").encode()` (in oracle and
  server mode these are the database of the test, `b"stuntgres"`, `b"127.0.0.1"`, the
  backend's port and `b"stuntgres"`); `options == b""`; `protocol_version == 3`;
  `server_version // 10000 == 18`; `backend_pid > 0`; `socket >= 0`;
  `isinstance(conn.pgconn_ptr, int)`. `nonblocking` is tested by PQ-08.
- `test_parameter_status`: `client_encoding` -> `b"UTF8"`; `server_encoding` ->
  `b"UTF8"`; `integer_datetimes` -> `b"on"`; `standard_conforming_strings` -> `b"on"`;
  `DateStyle` -> `b"ISO, MDY"`; `IntervalStyle` -> `b"postgres"`; `is_superuser` ->
  `b"on"`; `b"nosuch"` -> `None`; `parameter_status("x")` raises `TypeError` (`bytes
  expected, got str`).
- `test_encoding`: `conn._encoding == "utf-8"`; a connection made with `DSN + b"
  client_encoding=LATIN1"` reports `parameter_status(b"client_encoding") == b"LATIN1"`
  and `_encoding == "iso8859-1"`; with `client_encoding=EUC_TW` the property raises
  `NotSupportedError` (`client encoding EUC_TW has no Python codec`).
- `test_info`: `got = {o.keyword: o.val for o in conn.info}`; for every key and value of
  `support.conninfo_dict(self.conninfo)`: `got[key.encode()] == value.encode()` (in oracle
  and server mode that covers `host`, `port`, `dbname`, `user`, `password` and `sslmode`
  -> `b"disable"`).
- `test_finish`: `finish()` twice; then `status == ConnStatus.BAD`; `transaction_status
  == TransactionStatus.UNKNOWN`; `error_message == b""`; `pgconn_ptr is None`; each of
  `db`, `socket`, `backend_pid`, `server_version`, `info`, `connect_poll()`, `reset()`,
  `parameter_status(b"x")` raises `OperationalError` with the text `the connection is
  closed`.
- `test_bad_conninfo`: `PGconn.connect(b"nonsense")`: `status == 1`; `error_message ==
  b'missing "=" after "nonsense" in connection info string\n'`; `get_error_message() ==
  'missing "=" after "nonsense" in connection info string'`; `socket` raises
  `OperationalError` (`the connection is lost`); `finish()` works.
- `test_connect_type`: `PGconn.connect("dbname=x")` raises `TypeError`.
- `test_refused`, `@support.only_modes("oracle", "server")`: `PGconn.connect(b"host=127.0.0.1
  port=1 dbname=x user=y connect_timeout=2 sslmode=disable")`: `status == 1`;
  `error_message == b'connection to server at "127.0.0.1", port 1 failed: Connection
  refused\n\tIs the server running on that host and accepting TCP/IP connections?\n'`.
- `test_no_such_database`, `@support.only_modes("oracle", "server")`: the conninfo with
  `dbname=nosuchdb`: `status == 1`; `error_message` ends with `failed: FATAL:  database
  "nosuchdb" does not exist\n`.
- `test_wrong_password`, `@support.only_modes("oracle", "server")`: `password=wrong`:
  `status == 1`; `error_message` ends with `failed: FATAL:  password authentication failed
  for user "stuntgres"\n`.
- `test_ping`: `PGconn.ping(DSN) == Ping.OK`; `PGconn.ping(b"nonsense") ==
  Ping.NO_ATTEMPT`; and, `@support.only_modes("oracle", "server")` in a method of its own,
  `PGconn.ping(b"host=127.0.0.1 port=1 connect_timeout=2") == Ping.NO_RESPONSE`.
- `test_connect_start`: `conn = PGconn.connect_start(DSN)`; loop: `s =
  conn.connect_poll()`; on `READING` `support.wait_readable(conn.socket)`; on `WRITING`
  wait with `select.select([], [conn.socket], [], 10)`; stop at `OK` or `FAILED`; the last
  value is `PollingStatus.OK` and `conn.status == 0`. With
  `@support.only_modes("oracle", "server")` in a second method: the first value is
  `PollingStatus.WRITING` and the set of values seen is `{WRITING, READING, OK}`.
- `test_reset`: `pid = conn.backend_pid`; `conn.reset()`; `status == 0`; `backend_pid !=
  pid`.
- `test_finalizer`: `conn = connect()` (without the cleanup); `ref = weakref.ref(conn)`;
  `del conn`; `gc.collect()`; `ref() is None`; no exception is reported (the runner fails
  the run on unraisable exceptions).
- `test_finalizer_in_another_process`: with `mock.patch("os.getpid", return_value=…)`
  giving a different pid, `conn.__del__()` leaves `conn.pgconn_ptr` set; afterwards (patch
  removed) `conn.finish()` works.
- `test_mro`: `[c.__name__ for c in PGconn.__mro__] == ["PGconn", "PreparedMixin",
  "RowModesMixin", "CancelMixin", "NotifyMixin", "CopyMixin", "ExecMixin", "PGconnBase",
  "object"]`; `PGconn.__slots__ == ()`; an instance has no `__dict__`.
- `test_not_defined`: none of `needs_password`, `used_password`, `ssl_in_use`,
  `used_gssapi`, `hostaddr`, `tty`, `full_protocol_version`, `pipeline_status`,
  `cancel_conn`, `trace`, `reset_start`, `encrypt_password`, `make_empty_result` is an
  attribute of `PGconn`.

#### CPYTHON-PQ-07-PGRESULT

Files: `stuntgres/pq/_pgresult.py`, `stuntgres/pq/_rows.py`, `tests/test_pq_result.py`.
Design sections: 5.5, 5.6.

Step. Write both modules as specified.

Tests to write first (`ResultTest(support.DatabaseTestCase)`). There is no `exec_` yet;
the test class has the helper `run(self, sql: bytes) -> PGresult` that returns
`PGresult(self.conn._lib, self.conn._lib.PQexec(self.conn.pgconn_ptr, sql))`. `setUp`
connects and runs
`create table rt (id int4 primary key, name varchar(10), amount numeric(10,2), note text, flag bool, big int8, data bytea)`
and
`insert into rt values (1, 'one', 1.50, null, true, 10000000000, '\x00ff'), (2, '', 2.25, 'n', false, -1, '')`
(in the Python source the bytea literal is written `'\\x00ff'`).

- `test_command_results`: the `create table` result: `status == ExecStatus.COMMAND_OK`,
  `command_status == b"CREATE TABLE"`, `command_tuples is None`, `ntuples == 0`,
  `nfields == 0`, `oid_value == 0`; the insert: `command_status == b"INSERT 0 2"`,
  `command_tuples == 2`; `update rt set note = 'x' where id > 5`: `b"UPDATE 0"`, `0`;
  `delete from rt where id = 2`: `b"DELETE 1"`, `1`; the empty string `b""`: status
  `EMPTY_QUERY`, `command_status == b""`, `command_tuples is None`.
- `test_select_shape`: `r = run(b"select id, name, amount, note, flag, big, data, 1+1 as
  expr from rt order by id")`: `status == TUPLES_OK`; `ntuples == 2`; `nfields == 8`;
  `binary_tuples == 0`; `command_status == b"SELECT 2"`; `command_tuples == 2`; `nparams
  == 0`; `isinstance(r.pgresult_ptr, int)`.
- `test_columns` (same result): `[r.fname(i) for i in range(8)] == [b"id", b"name",
  b"amount", b"note", b"flag", b"big", b"data", b"expr"]`; `r.fname(8) is None`;
  `r.fname(-1) is None`; `ftype`: `[23, 1043, 1700, 25, 16, 20, 17, 23]` and `r.ftype(8)
  == 0`; `fmod`: `[-1, 14, 655366, -1, -1, -1, -1, -1]`; `fsize`: `[4, -1, -1, -1, 1, 8,
  -1, 4]`; `fformat`: eight zeros; `ftablecol`: `[1, 2, 3, 4, 5, 6, 7, 0]`; `ftable(i) !=
  0` for `i` in 0 to 6, all seven equal, and `ftable(7) == 0`.
- `test_rows` (same result): `r.get_rows(0, 2) == [(b"1", b"one", b"1.50", None, b"t",
  b"10000000000", b"\\x00ff", b"2"), (b"2", b"", b"2.25", b"n", b"f", b"-1", b"\\x",
  b"2")]`; `get_rows(1, 99) == get_rows(1, 2)`; `get_rows(-5, 1) == get_rows(0, 1)`;
  `get_rows(2, 2) == []`; `get_rows(1, 0) == []`.
- `test_get_value` (same result): `(0, 1)` -> `b"one"`; `(0, 3)` -> `None`; `(1, 1)` ->
  `b""`; `(1, 6)` -> `b"\\x"`; `(2, 0)`, `(0, 8)`, `(-1, 0)` -> `None`.
- `test_no_rows`: `select * from rt where false`: `TUPLES_OK`, `ntuples == 0`, `nfields
  == 7`, `command_status == b"SELECT 0"`, `command_tuples == 0`, `get_rows(0, 5) == []`.
- `test_error_result`: `r = run(b"select 1 from nosuch_table")`: `status ==
  FATAL_ERROR`; `error_message == b'ERROR:  relation "nosuch_table" does not exist\nLINE
  1: select 1 from nosuch_table\n                      ^\n'`; `get_error_message()` is
  that text decoded without the final newline; `error_field` of `SEVERITY`,
  `SEVERITY_NONLOCALIZED`, `SQLSTATE`, `MESSAGE_PRIMARY`, `STATEMENT_POSITION` ->
  `b"ERROR"`, `b"ERROR"`, `b"42P01"`, `b'relation "nosuch_table" does not exist'`,
  `b"15"`; `MESSAGE_DETAIL`, `MESSAGE_HINT`, `CONTEXT`, `SCHEMA_NAME`, `TABLE_NAME`,
  `CONSTRAINT_NAME` -> `None`; `ntuples == 0`; `nfields == 0`; `command_status == b""`;
  `command_tuples is None`. Do not assert `SOURCE_FILE`, `SOURCE_LINE`,
  `SOURCE_FUNCTION`: they name the source code of whichever server answers.
- `test_constraint_error`: `insert into rt values (1, 'dup', 0, null, null, null, null)`:
  `SQLSTATE` `b"23505"`; `MESSAGE_PRIMARY` `b'duplicate key value violates unique
  constraint "rt_pkey"'`; `MESSAGE_DETAIL` `b"Key (id)=(1) already exists."`;
  `SCHEMA_NAME` `b"public"`; `TABLE_NAME` `b"rt"`; `CONSTRAINT_NAME` `b"rt_pkey"`;
  `error_message == b'ERROR:  duplicate key value violates unique constraint
  "rt_pkey"\nDETAIL:  Key (id)=(1) already exists.\n'`.
- `test_ok_result_has_no_error`: on a `select 1` result: `error_message == b""`;
  `error_field(DiagnosticField.SQLSTATE) is None`; `get_error_message() == "no error
  details available"`.
- `test_clear`: `r.clear()` twice; then `pgresult_ptr is None`; `error_message == b""`;
  `error_field(67) is None`; `command_status is None`; `command_tuples is None`;
  `fname(0) is None`; `ftype(0) == 0`; `get_value(0, 0) is None`; `get_rows(0, 1) == []`;
  `status == ExecStatus.FATAL_ERROR`; `ntuples == 0`.
- `test_repr`: `repr(run(b"select 1")).startswith("<stuntgres.pq.PGresult [TUPLES_OK] at
  0x")`.
- `test_outlives_connection`: take a result, `conn.finish()`, then `get_rows(0, 1) ==
  [(b"1",)]`.
- `test_borrowed_result_is_not_freed`: `raw = lib.PQexec(ptr, b"select 1")`; `b =
  PGresult(lib, raw, owned=False)`; `b.clear()`; an owned `PGresult(lib, raw)` still
  reads `get_value(0, 0) == b"1"`.
- `test_read_rows_calls`: `_rows.read_rows` called with a counting stand-in for `lib`
  (an object with the three accessors that counts calls and forwards to the real ones)
  on the two-row, eight-column result makes exactly 32 calls and returns the rows of
  `test_rows`.

#### CPYTHON-PQ-08-EXEC

Files: `stuntgres/pq/_params.py`, `stuntgres/pq/_pgconn_exec.py`,
`tests/test_pq_exec.py`. Design sections: 5.7.

Step. Write `_params.py`, then replace the stub `_pgconn_exec.py` with the full module.

Tests to write first (`ExecTest(support.DatabaseTestCase)`; `st(r)` below means
`ExecStatus(r.status).name`):

- `test_exec`: `r = conn.exec_(b"select 1 as a, null::text as b, '' as c")`: `TUPLES_OK`,
  `r.get_rows(0, 1) == [(b"1", None, b"")]`.
- `test_exec_multi_returns_last`: `conn.exec_(b"select 1; select 2, 3").get_rows(0, 9) ==
  [(b"2", b"3")]`.
- `test_exec_multi_stops_at_error`: `b"select 1; select 1/0; select 3"`: `FATAL_ERROR`,
  SQLSTATE `b"22012"`, primary message `b"division by zero"`; `transaction_status == 0`.
- `test_transaction_status`: `begin` -> `COMMAND_OK`, `command_status == b"BEGIN"`,
  `transaction_status == 2`; `select 1/0` -> `transaction_status == 3`; `select 1` ->
  `FATAL_ERROR`, `b"25P02"`, `b"current transaction is aborted, commands ignored until
  end of transaction block"`; `rollback` -> `b"ROLLBACK"`, `transaction_status == 0`.
- `test_exec_params`: `conn.exec_params(b"select $1::int4 + 1, $2::text, $3", [b"41",
  None, b"x"])`: rows `[(b"42", None, b"x")]`, `ftype` `[23, 25, 25]`.
- `test_exec_params_types`: `(b"select $1, $2", [b"5", b"abc"], [23, 25])`: rows `[(b"5",
  b"abc")]`, `ftype` `[23, 25]`.
- `test_exec_params_binary`: `(b"select $1::int4, $2::bytea", [b"\x00\x00\x01\x00",
  b"\x00\xff"], None, [1, 1], 1)`: `binary_tuples == 1`; rows `[(b"\x00\x00\x01\x00",
  b"\x00\xff")]`; `fformat` `[1, 1]`.
- `test_exec_params_buffers`: `(b"select $1::bytea", [bytearray(b"ab")], None, [1])` ->
  `[(b"\\x6162",)]`; `(b"select $1::text", [memoryview(b"mv")])` -> `[(b"mv",)]`.
- `test_exec_params_no_values`: `(b"select 7", None)` and `(b"select 7", [])` ->
  `[(b"7",)]`.
- `test_exec_params_errors`: `(b"select 1; select 2", [])` -> `FATAL_ERROR` `b"42601"`
  `b"cannot insert multiple commands into a prepared statement"`; `(b"select $1::int4",
  [b"zz"])` -> `b"22P02"` `b'invalid input syntax for type integer: "zz"'`; `(b"select
  $1::int4, $2::int4", [b"1"])` -> `b"08P01"` `b'bind message supplies 1 parameters, but
  prepared statement "" requires 2'`.
- `test_argument_errors`: `conn.exec_params(b"select $1", [b"1"], [23, 23])` raises
  `ValueError` (`got 1 param_values but 2 param_types`); with `None, [0, 0]` raises
  `ValueError` (`got 1 param_values but 2 param_formats`); `conn.exec_("select 1")` raises
  `TypeError` (`bytes expected, got str`).
- `test_send_query_all_results`: `conn.send_query(b"select 1 as a; select 'x' as b, 2 as
  c; select 1/0; select 4")`; collecting `get_result()` until `None` gives three results:
  `TUPLES_OK` `[(b"1",)]`; `TUPLES_OK` `[(b"x", b"2")]`; `FATAL_ERROR` `b"22012"`.
  Afterwards `is_busy() == 0`, `get_result() is None`, `transaction_status == 0`.
- `test_busy_and_consume`: `conn.send_query(b"select pg_sleep(0.2), 5")`; `flush() == 0`;
  `is_busy() == 1`; `transaction_status == 1`; a second `conn.send_query(b"select 2")`
  raises `OperationalError` (`send_query failed: another command is already in
  progress`); `support.wait_readable(conn.socket)`; `conn.consume_input()`; `is_busy() ==
  0`; `get_result().get_value(0, 1) == b"5"`; `get_result() is None`.
- `test_nonblocking`: `conn.nonblocking == 0`; set to 1 -> 1; set to 0 -> 0.
- `test_send_query_params`: `conn.send_query_params(b"select $1::int4 * 2", [b"21"])`;
  `get_result().get_rows(0, 1) == [(b"42",)]`; `get_result() is None`.
- `test_empty_query`: `conn.send_query(b"")`; one result with status `EMPTY_QUERY`, then
  `None`.
- `test_idle_calls`: `conn.consume_input()` returns `None`; `conn.is_busy() == 0`;
  `conn.flush() == 0`.
- `test_bad_connection`: on `PGconn.connect(b"nonsense")`: `exec_(b"select 1")` raises
  `OperationalError` (`exec_ failed: no connection to the server`); `send_query` raises
  `send_query failed: no connection to the server`.
- `test_closed`: after `finish()`: `exec_`, `send_query`, `get_result`, `consume_input`,
  `is_busy`, `flush` and reading `nonblocking` raise `OperationalError` (`the connection
  is closed`).
- `test_terminated_session`, `@support.only_modes("oracle", "server")`: a second
  connection runs `select pg_terminate_backend(<pid of the first>)` -> `b"t"`; then on the
  first `exec_(b"select 1")` returns a `FATAL_ERROR` result whose `error_message` starts
  with `b"server closed the connection unexpectedly"`; `status == ConnStatus.BAD`;
  `socket` raises `OperationalError` (`the connection is lost`).

#### CPYTHON-PQ-09-PREPARED

Files: `stuntgres/pq/_pgconn_prepared.py`, `tests/test_pq_prepared.py`. Design
sections: 5.8.

Step. Replace the stub with the full mixin.

Tests to write first (`PreparedTest(support.DatabaseTestCase)`):

- `test_prepare_and_execute`: `conn.prepare(b"st1", b"select $1::int4 + $2::int4 as s,
  $3::text as t")`: `COMMAND_OK`, `command_status == b""`, `ntuples == 0`;
  `conn.exec_prepared(b"st1", [b"1", b"2", None]).get_rows(0, 1) == [(b"3", None)]`;
  `conn.exec_prepared(b"st1", [b"\x00\x00\x00\x05", b"\x00\x00\x00\x06", b"t"], [1, 1, 0],
  1)`: `binary_tuples == 1`, rows `[(b"\x00\x00\x00\x0b", b"t")]`.
- `test_describe_prepared`: after that prepare, `r = conn.describe_prepared(b"st1")`:
  `COMMAND_OK`; `nparams == 3`; `[r.param_type(i) for i in range(3)] == [23, 23, 25]`;
  `param_type(3) == 0`; `nfields == 2`; `fname` `[b"s", b"t"]`; `ftype` `[23, 25]`;
  `ntuples == 0`.
- `test_prepare_with_types`: `conn.prepare(b"st2", b"select $1, $2", [23, 25])`;
  `describe_prepared(b"st2")`: `nparams == 2`, param types `[23, 25]`, `ftype` `[23, 25]`.
- `test_describe_non_select`: `create table pt (id int4)`; `prepare(b"st3", b"insert into
  pt (id) values ($1)")`; `describe_prepared(b"st3")`: `COMMAND_OK`, `nparams == 1`,
  `nfields == 0`.
- `test_unnamed`: `conn.prepare(b"", b"select $1::int4 as u")` -> `COMMAND_OK`;
  `conn.exec_prepared(b"", [b"9"]).get_rows(0, 1) == [(b"9",)]`.
- `test_errors_are_results`: `exec_prepared(b"nosuch", [])` -> `FATAL_ERROR` `b"26000"`
  `b'prepared statement "nosuch" does not exist'`; `describe_prepared(b"nosuch")` -> the
  same; a second `prepare(b"st1", b"select 1")` -> `b"42P05"` `b'prepared statement
  "st1" already exists'`.
- `test_describe_portal`: `begin`; `declare cur1 cursor for select 1 as one, 'x'::text as
  two` -> `command_status == b"DECLARE CURSOR"`; `r = conn.describe_portal(b"cur1")`:
  `COMMAND_OK`, `nfields == 2`, `fname` `[b"one", b"two"]`, `ftype` `[23, 25]`, `nparams
  == 0`; `transaction_status == 2`; `fetch all from cur1` -> `[(b"1", b"x")]`.
  (Server mode needs CPYTHON-ENGINE-01-DESCRIBE-SQL-CURSOR.)
- `test_describe_portal_unknown`: inside a transaction, `describe_portal(b"nosuch")` ->
  `FATAL_ERROR` `b"34000"` `b'portal "nosuch" does not exist'`.
- `test_close_portal`, `@support.requires_function("PQclosePortal")`: `begin`; `declare
  cur1 cursor for select 1`; `conn.close_portal(b"cur1")` -> `COMMAND_OK`,
  `command_status == b""`; `fetch 1 from cur1` -> `FATAL_ERROR` `b"34000"` `b'cursor
  "cur1" does not exist'`.
- `test_close_prepared`, `@support.requires_function("PQclosePrepared")`: prepare
  `st2`; `conn.close_prepared(b"st2")` -> `COMMAND_OK`, `b""`; `exec_prepared(b"st2",
  [b"1", b"2"])` -> `b"26000"`; `close_prepared(b"nosuch")` -> `COMMAND_OK`.
- `test_send_variants`: `send_prepare(b"sp1", b"select $1::int4 as v")`, result
  `COMMAND_OK`, then `None`; `send_query_prepared(b"sp1", [b"3"])` -> `[(b"3",)]`, `None`;
  `send_describe_prepared(b"sp1")` -> `COMMAND_OK`, `nparams == 1`, `nfields == 1`,
  `None`; inside a transaction with `declare cur2 cursor for select 1 as one`:
  `send_describe_portal(b"cur2")` -> `fname(0) == b"one"`, `None`.
- `test_send_close_variants`, `@support.requires_function("PQsendClosePrepared")`: in a
  transaction with a cursor `cur2`: `send_close_portal(b"cur2")` -> `COMMAND_OK`, `None`;
  with a prepared `sp1`: `send_close_prepared(b"sp1")` -> `COMMAND_OK`, `None`.
- `test_type_errors`: `conn.prepare("x", b"select 1")` raises `TypeError` (`bytes
  expected, got str`); `conn.exec_prepared(b"st1", [b"1"], [0, 0])` raises `ValueError`
  (`got 1 param_values but 2 param_formats`).

#### CPYTHON-PQ-10-NOTICES

Files: `stuntgres/pq/_notices.py`, `tests/test_pq_notices.py`. Design sections: 5.9.

Step. Replace the stub with the module of 5.9.

Tests to write first (`NoticesTest(support.DatabaseTestCase)`; `NOTICE = b"drop table if
exists nosuch_table"`; a handler that records `(ExecStatus(res.status).name,
res.error_field(SEVERITY), res.error_field(SEVERITY_NONLOCALIZED),
res.error_field(SQLSTATE), res.error_field(MESSAGE_PRIMARY), res.error_message)`):

- `test_notice`: `conn.exec_(NOTICE)` -> `COMMAND_OK`, `b"DROP TABLE"`; the handler got
  one record: `("NONFATAL_ERROR", b"NOTICE", b"NOTICE", b"00000", b'table "nosuch_table"
  does not exist, skipping', b'NOTICE:  table "nosuch_table" does not exist,
  skipping\n')`.
- `test_notice_and_warning`: `do $$ begin raise notice 'hello %', 42; raise warning
  'careful' using detail = 'dt', hint = 'hn'; end $$` -> `command_status == b"DO"`; two
  records: `("NONFATAL_ERROR", b"NOTICE", b"NOTICE", b"00000", b"hello 42", b"NOTICE:
  hello 42\n")` and `("NONFATAL_ERROR", b"WARNING", b"WARNING", b"01000", b"careful",
  b"WARNING:  careful\nDETAIL:  dt\nHINT:  hn\n")` (two spaces after each colon).
- `test_result_is_cleared_after_the_handler`: a handler that keeps `res`; afterwards
  `kept.pgresult_ptr is None`, `kept.error_message == b""`,
  `kept.error_field(DiagnosticField.SQLSTATE) is None`.
- `test_no_handler`: `conn.notice_handler = None`; `exec_(NOTICE)` -> `COMMAND_OK`;
  nothing is written to file descriptor 2 (redirect it to a temporary file with `os.dup2`
  around the call and assert the file is empty).
- `test_handler_raises`: a handler that raises `ValueError("boom")`; inside
  `self.assertLogs("stuntgres", level="ERROR")`: `exec_(NOTICE)` returns `COMMAND_OK`;
  one record with `getMessage() == "error in notice handler: boom"` and
  `exc_info[0] is ValueError`.
- `test_handler_raises_keyboard_interrupt`: a handler that raises `KeyboardInterrupt()`:
  `exec_(NOTICE)` returns `COMMAND_OK`; one log record with `exc_info[0] is
  KeyboardInterrupt`; `exec_(b"select 1")` works afterwards.
- `test_handlers_are_per_connection`: two connections; a handler on the first only; a
  notice on the second leaves the first handler's list empty.
- `test_handler_may_use_another_connection`: the handler runs `other.exec_(b"select
  7").get_value(0, 0)` and records it with `threading.current_thread() is
  threading.main_thread()`: `[(True, b"7")]`.
- `test_connection_is_not_kept_alive`: `key = conn._notice_key`; `ref =
  weakref.ref(conn)`; `kept = conn.exec_(b"select 1 as x")`; drop every reference to the
  connection and `gc.collect()`: `ref() is None` and `key not in
  _notices._connections`.
- `test_late_notice_through_a_surviving_result`: repeat the steps of
  `test_connection_is_not_kept_alive` inside this method (a handler that would record, a
  kept result, the connection dropped and collected); then: `kept._lib.PQgetvalue(kept.pgresult_ptr, 99, 0) is None` (libpq
  raises a notice through the result's hooks); the handler list stays empty and nothing
  is logged; `kept.get_value(99, 0) is None`.
- `test_finish_unregisters`: `key = conn._notice_key`; `key in _notices._connections`;
  `conn.finish()`; `key not in _notices._connections`.
- `test_worker_thread`: a thread connects, sets a handler that records
  `threading.current_thread().name`, runs `NOTICE` and finishes: `["worker-1"]` for a
  thread named `worker-1`.

#### CPYTHON-PQ-11-NOTIFY

Files: `stuntgres/pq/_pgconn_notify.py`, `tests/test_pq_notify.py`. Design sections: 5.10.

Step. Replace the stub with the mixin.

Tests to write first (`NotifyTest(support.DatabaseTestCase)`):

- `test_none`: `conn.notifies() is None`.
- `test_self_notify`: `listen ch1` -> `b"LISTEN"`; `notify ch1, 'payload one'` ->
  `b"NOTIFY"`; `n = conn.notifies()`: `n == PGnotify(b"ch1", conn.backend_pid, b"payload
  one")`; `conn.notifies() is None`.
- `test_no_payload`: `notify ch1` gives `extra == b""`.
- `test_from_another_session`: a second connection runs `notify ch1, 'from two'`; on the
  first: `support.wait_readable(conn.socket)`; `conn.consume_input()`; `n =
  conn.notifies()`: `relname == b"ch1"`, `extra == b"from two"`, `be_pid ==
  other.backend_pid`.
- `test_order`: the second connection runs `select pg_notify('ch1', 'a'), pg_notify('ch1',
  'b')`; on the first, until two notifications are collected (bounded by a 10 s
  deadline): `support.wait_readable(conn.socket)`, `conn.consume_input()`, then
  `notifies()` until `None`; the `extra` values are `[b"a", b"b"]`.
- `test_unlisten`: `unlisten ch1` -> `b"UNLISTEN"`; a later `notify ch1` leaves
  `notifies()` at `None` after `exec_(b"select 1")`.
- `test_closed`: after `finish()`, `notifies()` raises `OperationalError` (`the connection
  is closed`).

#### CPYTHON-PQ-12-CANCEL

Files: `stuntgres/pq/_pgcancel.py`, `stuntgres/pq/_pgconn_cancel.py`,
`tests/test_pq_cancel.py`. Design sections: 5.11.

Step. Write `_pgcancel.py`; replace the stub `_pgconn_cancel.py`.

Tests to write first (`CancelTest(support.DatabaseTestCase)`):

- `test_cancel_running`: `k = conn.get_cancel()`; `conn.send_query(b"select
  pg_sleep(30)")`; then, bounded by a 10 s deadline: `k.cancel()`; leave the loop when
  `support.is_readable(conn.socket, 0.05)`; otherwise cancel again (a request that arrives
  before the statement started has no effect, so it is repeated; this is a bounded wait on
  the descriptor, not a sleep). `r = conn.get_result()`: `FATAL_ERROR`, SQLSTATE
  `b"57014"`, primary message `b"canceling statement due to user request"`;
  `conn.get_result() is None`; `transaction_status == 0`.
- `test_cancel_when_idle`: `k.cancel()` returns `None`; `conn.exec_(b"select 1")` ->
  `TUPLES_OK`.
- `test_free`: `k.free()` twice; `k.cancel()` raises `OperationalError` (`the cancel
  object is freed`).
- `test_cancel_from_another_thread`: `done = threading.Event()`; a thread runs `while
  not done.wait(0.05): k.cancel()` (bounded by a 10 s deadline); the main thread calls
  `conn.send_query(b"select pg_sleep(30)")`, starts the thread and blocks in
  `conn.get_result()`: the result is the `57014` error; then `done.set()`, join the thread
  with a timeout of 10 s and assert it ended; `conn.get_result() is None`.
- `test_survives_finish`: `k = conn.get_cancel()`; `conn.finish()`; `k.cancel()` returns
  `None`; `k.free()` works.
- `test_closed`: after `finish()`, `conn.get_cancel()` raises `OperationalError` (`the
  connection is closed`).
- `test_finalizer`: a `PGcancel` dropped without `free()` is collected without an
  unraisable exception.

#### CPYTHON-PQ-13-COPY

Files: `stuntgres/pq/_pgconn_copy.py`, `tests/test_pq_copy.py`. Design sections: 5.12.

Step. Replace the stub with the mixin.

Tests to write first (`CopyTest(support.DatabaseTestCase)`; `setUp` runs `create table ct
(a int4, b text)` and `insert into ct values (1, 'x'), (2, null)`):

- `test_copy_out`: `r = conn.exec_(b"copy ct to stdout")`: `COPY_OUT`, `nfields == 2`,
  `binary_tuples == 0`; repeated `conn.get_copy_data(0)` gives `(4, b"1\tx\n")`, `(5,
  b"2\t\\N\n")`, `(-1, b"")` (compare `bytes(view)`; the second item is a `memoryview`);
  then `get_result()`: `COMMAND_OK`, `b"COPY 2"`; `get_result() is None`.
- `test_copy_in`: `conn.exec_(b"copy ct from stdin")`: `COPY_IN`, `nfields == 2`;
  `put_copy_data(b"3\ty\n") == 1`; `put_copy_data(bytearray(b"4\t\\N\n")) == 1`;
  `put_copy_data(memoryview(b"5\tz\n")) == 1`; `put_copy_end() == 1`; `get_result()`:
  `COMMAND_OK`, `b"COPY 3"`, `command_tuples == 3`; then `None`; `select count(*) from
  ct` -> `b"5"`.
- `test_copy_in_aborted`: `copy ct from stdin`; `put_copy_data(b"6\tw\n")`;
  `put_copy_end(b"stop here") == 1`; `get_result()`: `FATAL_ERROR`, `b"57014"`, `b"COPY
  from stdin failed: stop here"`; then `None`; the table still has 2 rows.
- `test_copy_in_bad_data`: `put_copy_data(b"bad line without tab\n")`; `put_copy_end()`;
  result `FATAL_ERROR`, `b"22P02"`, `b'invalid input syntax for type integer: "bad line
  without tab"'`, `CONTEXT` `b'COPY ct, line 1, column a: "bad line without tab"'`.
- `test_async_copy_out`: `conn.send_query(b"copy (select g from generate_series(1, 3) g)
  to stdout")`; `get_result()` -> `COPY_OUT`; loop on `get_copy_data(1)`: on `0` do
  `support.wait_readable(conn.socket)` and `conn.consume_input()`; collected: `(2,
  b"1\n")`, `(2, b"2\n")`, `(2, b"3\n")`, `(-1, b"")`; `get_result()`: `COMMAND_OK`,
  `b"COPY 3"`.
- `test_outside_copy`: each of the three calls is made right after a successful
  `conn.exec_(b"select 1")` (libpq appends to its error text until the next command
  starts): `put_copy_data(b"x")` raises `OperationalError` (`put_copy_data failed: no COPY
  in progress`); `put_copy_end()` raises `put_copy_end failed: no COPY in progress`;
  `get_copy_data(0)` raises `get_copy_data failed: no COPY in progress`.
- `test_closed`: after `finish()` the three methods raise `OperationalError` (`the
  connection is closed`).

#### CPYTHON-PQ-14-ROWMODES

Files: `stuntgres/pq/_pgconn_rowmodes.py`, `tests/test_pq_rowmodes.py`. Design
sections: 5.13.

Step. Replace the stub with the mixin.

Tests to write first (`RowModesTest(support.DatabaseTestCase)`; each collects
`(ExecStatus(r.status).name, r.ntuples, r.get_rows(0, 9), r.command_status)` until
`get_result()` is `None`):

- `test_single_row`: `send_query(b"select g from generate_series(1, 3) g")`;
  `set_single_row_mode()`: `("SINGLE_TUPLE", 1, [(b"1",)], b"")`, `("SINGLE_TUPLE", 1,
  [(b"2",)], b"")`, `("SINGLE_TUPLE", 1, [(b"3",)], b"")`, `("TUPLES_OK", 0, [], b"SELECT
  3")`.
- `test_single_row_no_rows`: `select 1 where false`: one result `TUPLES_OK`, `ntuples ==
  0`, `nfields == 1`.
- `test_single_row_then_error`: `select g from generate_series(1, 2) g; select 1/0`:
  statuses `SINGLE_TUPLE`, `SINGLE_TUPLE`, `TUPLES_OK`, `FATAL_ERROR` (SQLSTATE
  `b"22012"`).
- `test_single_row_when_idle`: `set_single_row_mode()` raises `OperationalError`
  (`set_single_row_mode failed`).
- `test_chunked_partial_last`, `@support.requires_function("PQsetChunkedRowsMode")`:
  `select g from generate_series(1, 5) g`; `set_chunked_rows_mode(2)`: `("TUPLES_CHUNK",
  2, [(b"1",), (b"2",)], b"")`, `("TUPLES_CHUNK", 2, [(b"3",), (b"4",)], b"")`,
  `("TUPLES_CHUNK", 1, [(b"5",)], b"SELECT 5")`, `("TUPLES_OK", 0, [], b"")`.
- `test_chunked_exact`, same gate: 4 rows in chunks of 2: `TUPLES_CHUNK` 2, `TUPLES_CHUNK`
  2, then `("TUPLES_OK", 0, [], b"SELECT 4")`.
- `test_chunked_when_idle`, same gate: raises `OperationalError`
  (`set_chunked_rows_mode failed`).
- `test_closed`: after `finish()` both raise `OperationalError` (`the connection is
  closed`).

#### CPYTHON-PQ-15-ESCAPING

Files: `stuntgres/pq/_escaping.py`, `tests/test_pq_escaping.py`. Design sections: 5.14.

Step. Write `Escaping` as specified.

Tests to write first (`EscapingTest(support.DatabaseTestCase)`, `esc = Escaping(conn)`;
Python literals below are written as they appear in source):

- `test_literal`: `b"it's"` -> `b"'it''s'"`; `b"a\\b"` -> `b" E'a\\\\b'"` (a leading
  space); `b""` -> `b"''"`; `bytearray(b"x")` -> `b"'x'"`.
- `test_identifier`: `b'a"b'` -> `b'"a""b"'`; `b"Tab le"` -> `b'"Tab le"'`; `b""` ->
  `b'""'`.
- `test_string`: `b"it's"` -> `b"it''s"`; `b"a\\b"` -> `b"a\\b"`; `b""` -> `b""`.
- `test_bytea`: `b"\x00\x01a'\\"` -> `b"\\x000161275c"`; `b""` -> `b"\\x"`.
- `test_unescape`: `b"\\x000161"` -> `b"\x00\x01a"`; `b"\\000\\001a"` -> `b"\x00\x01a"`;
  `b"abc"` -> `b"abc"`; `b""` -> `b""`.
- `test_invalid_encoding`: `escape_literal(b"\xff\xfe")` raises `OperationalError`
  (`escape_literal failed: invalid multibyte character`); `escape_identifier(b"\xff")`
  raises `escape_identifier failed: invalid multibyte character`; `escape_string(b"\xff'")`
  raises `escape_string failed: invalid multibyte character`.
- `test_without_connection` (`noc = Escaping()`): `escape_string(b"it's")` -> `b"it''s"`;
  `escape_string(b"a\\b")` -> `b"a\\b"`; `escape_bytea(b"\x00\x01a'\\")` ->
  `b"\\000\\001a''\\\\"`; `unescape_bytea(b"\\x6162")` -> `b"ab"`; `escape_literal(b"x")`
  raises `OperationalError` (`escape_literal failed: no connection provided`);
  `escape_identifier(b"x")` raises `escape_identifier failed: no connection provided`.
- `test_standard_conforming_strings_off`: a connection made with `DSN + b"
  options='-c standard_conforming_strings=off'"`: `parameter_status(
  b"standard_conforming_strings") == b"off"`; `escape_string(b"a\\b")` -> `b"a\\\\b"`;
  `escape_bytea(b"\x00")` -> `b"\\\\x00"`; `escape_literal(b"a\\b")` -> `b" E'a\\\\b'"`.
- `test_closed_connection`: `Escaping(conn)` after `conn.finish()`: `escape_literal(b"x")`
  raises `OperationalError` (`the connection is closed`).

#### CPYTHON-PQ-16-GENERATORS

Files: `stuntgres/generators.py`, `tests/test_generators.py`. Design sections: 7.

Step. Write the module as section 7 specifies.

Tests to write first. The waiting module does not exist yet: generators are driven with
`support.run_gen(gen, conn.socket)` and `support.run_gen_conn(gen)`.

`GeneratorsUnitTest(unittest.TestCase)` with a fake connection object (plain Python class
that records calls):

- `test_send_done_at_once`: `flush()` returns 0: `next(send(fake))` raises `StopIteration`
  at once.
- `test_send_loops`: `flush()` returns 1, 1, 0: the generator yields `Wait.RW`; sent
  `Ready.NONE` it yields `Wait.RW` again without calling `flush` or `consume_input`; sent
  `Ready.W` it calls `flush` (1) and yields; sent `Ready.RW` it calls `consume_input` once,
  then `flush` (0) and stops.
- `test_fetch_ignores_ready_none`: `is_busy()` returns 1, 1, 0: yields `Wait.R`; sent
  `Ready.NONE` yields `Wait.R` again with `consume_input` not called; sent `Ready.R` twice
  it calls `consume_input` twice and returns the fake's `get_result()` value.
- `test_fetch_delivers_notifications`: the fake's `notifies()` returns two objects then
  `None`; `notify_handler` gets both before `get_result` is called.
- `test_fetch_many_stops_at_copy`: `get_result` returns objects with `status` 2 then 3
  (`COPY_OUT`): the list has both and `get_result` was called twice.
- `test_cancel_when_idle`: `transaction_status == 0`: returns `[]` and `get_cancel` was not
  called.

`GeneratorsTest(support.DatabaseTestCase)` (`conn = support.run_gen_conn(
generators.connect(DSN))`, finished by a cleanup):

- `test_connect`: the result is a `PGconn`; `status == 0`; `nonblocking == 1`.
- `test_connect_errors`: `support.run_gen_conn(generators.connect(b"nonsense"))` raises
  `OperationalError` with the text `connection failed: missing "=" after "nonsense" in
  connection info string`; its `pgconn.pgconn_ptr is None`. With
  `@support.only_modes("oracle", "server")` in methods of their own: `b"host=127.0.0.1
  port=1 sslmode=disable"` raises `OperationalError` whose text starts with `connection
  failed: connection to server at "127.0.0.1", port 1 failed: Connection refused`; the
  conninfo with `dbname=nosuchdb` raises one whose text ends with `failed: FATAL:
  database "nosuchdb" does not exist` (two spaces after `FATAL:`).
- `test_connect_closed_early`, `@support.only_modes("oracle", "server")`: `gen =
  generators.connect(DSN)`; `fileno, wanted = next(gen)`; `wanted == Wait.W`;
  `gen.close()`; `gen.gi_frame is None`; nothing unraisable (the half-made connection is
  finished).
- `test_execute_multi`: `conn.send_query(b"select 1 as a; select 2 as b")`; `rs =
  support.run_gen(generators.execute(conn), conn.socket)`: two `TUPLES_OK` results with
  rows `[(b"1",)]` and `[(b"2",)]`; `transaction_status == 0`.
- `test_execute_error_in_the_middle`: `select 1; select 1/0; select 3`: statuses
  `TUPLES_OK`, `FATAL_ERROR` (`b"22012"`).
- `test_large_parameter`: `conn.send_query_params(b"select length($1::text)", [b"x" *
  8_000_000])`; the result is `[(b"8000000",)]` (over TCP `flush()` returns 1 on the way).
- `test_large_result`: `select repeat('y', 2000000)`: `len(get_value(0, 0)) == 2000000`.
- `test_stops_at_copy_out`: `send_query(b"copy (select 1) to stdout")`: the list is one
  result with status `COPY_OUT`.
- `test_notifications_during_execute`: `conn.notify_handler = got.append`;
  `send_query(b"listen gch; notify gch, 'in-band'")`: two `COMMAND_OK` results and `got
  == [PGnotify(b"gch", conn.backend_pid, b"in-band")]`.
- `test_notifies`: after `listen gch`, another connection runs `notify gch,
  'out-of-band'`; `support.run_gen(generators.notifies(conn), conn.socket)` returns
  `[PGnotify(b"gch", other.backend_pid, b"out-of-band")]` and the handler got the same
  object.
- `test_notifies_ready_none`: `gen = generators.notifies(conn)`; `next(gen) == Wait.R`;
  `gen.send(Ready.NONE)` raises `StopIteration` with value `[]`.
- `test_cancel`: `started = []`; `conn.notice_handler = started.append`;
  `conn.send_query(b"do $$ begin raise notice 'started'; perform pg_sleep(30); end $$")`;
  until `started`: `support.wait_readable(conn.socket)`; `conn.consume_input()`. Then `rs =
  support.run_gen(generators.cancel(conn), conn.socket)`: one result, `FATAL_ERROR`,
  `b"57014"`, `b"canceling statement due to user request"`; `transaction_status == 0`;
  a following `select 8` through `execute` gives `b"8"`.
- `test_cancel_in_transaction`: the same after `begin`: afterwards `transaction_status ==
  3`; `rollback` gives `b"ROLLBACK"` and status 0.
- `test_cancel_when_idle`: `support.run_gen(generators.cancel(conn), conn.socket) == []`.

#### CPYTHON-PQ-17-WAIT-SYNC

Files: `stuntgres/waiting.py`, `tests/test_waiting.py`. Design sections: 8 (intro), 8.1.

Step. Write the module as 8.1 specifies.

Tests to write first.

`WaitUnitTest(unittest.TestCase)` (socket pairs, hand-written generators):

- `test_returns_value`: a generator that yields `Wait.R` on the readable end of a socket
  pair and returns `("done", ready)`: `wait(gen, fd) == ("done", Ready.R)`.
- `test_write_ready`: yields `Wait.RW` on a fresh socket: the value sent has `Ready.W`
  set.
- `test_timeout`: nothing to read, `timeout=0.05`: raises `WaitTimeout` with the text
  `timeout expired`; `isinstance(exc, OperationalError)`; the generator is closed
  (`gen.gi_frame is None`).
- `test_interval`: nothing to read, `interval=0.01`, a generator that counts what it is
  sent and returns after three `Ready.NONE`: returns; all three were `0`.
- `test_generator_error_propagates`: a generator that raises `ValueError("x")` after its
  first yield: `wait` raises it and the generator is closed.
- `test_hangup_wakes_the_generator`: yields `Wait.R` on one end of a pair whose other end
  was closed: the value sent is non-zero.
- `test_wait_conn`: a generator that yields `(fd, Wait.R)` then `(fd2, Wait.W)`: returns
  its value; with `timeout=0.05` and nothing ready raises `WaitTimeout`.

`WaitTest(support.DatabaseTestCase)`:

- `test_connect_and_execute`: `conn = waiting.wait_conn(generators.connect(DSN),
  timeout=10)`; `conn.send_query(b"select 1 as a; select 2 as b")`;
  `waiting.wait(generators.execute(conn), conn.socket)` gives rows `[(b"1",)]` and
  `[(b"2",)]`.
- `test_statement_timeout_and_cancel`: `seen = []`; `conn.notice_handler = seen.append`;
  `conn.send_query(b"do $$ begin raise notice 'started'; perform pg_sleep(30); end $$")`;
  `wait(generators.execute(conn), conn.socket, timeout=0.2)` raises `WaitTimeout`
  (`timeout expired`); `transaction_status == 1`; if `seen` is still empty: until it is
  not, `support.wait_readable(conn.socket)` and `conn.consume_input()` (the notice proves
  the statement runs); then `rs = wait(generators.cancel(conn), conn.socket, timeout=10)`:
  one result, the `57014` error; `transaction_status == 0`.
- `test_interval_reaches_the_generator`: `send_query(b"select pg_sleep(0.3)")`; a wrapping
  generator that forwards to `generators.fetch_many(conn)` and records every value it is
  sent; `wait(wrapper, conn.socket, interval=0.05)`: at least 3 of the recorded values
  are `0`, the last is `Ready.R`, the result is `TUPLES_OK`.
- `test_keyboard_interrupt`: install `signal.default_int_handler` for `SIGINT` and restore
  the previous handler in a cleanup (a test run started in the background inherits
  `SIGINT` ignored). `seen = []`; `conn.notice_handler = seen.append`; `started =
  threading.Event()`. A local generator `announcing(inner)` forwards to `inner` (`wanted =
  next(inner)`; loop: `if seen: started.set()`; `wanted = inner.send((yield wanted))`;
  return the `StopIteration` value; `finally: inner.close()`): it releases the helper from
  generator code, never from inside the notice callback, where a `KeyboardInterrupt` would
  be logged and dropped (5.9). A helper thread calls
  `signal.pthread_kill(threading.main_thread().ident, signal.SIGINT)` only if
  `started.wait(10)` returned `True`; it is joined in a cleanup. `conn.send_query(b"do $$ begin raise notice 'started'; perform pg_sleep(30); end
  $$")`; `gen = announcing(generators.execute(conn))`; `with
  self.assertRaises(KeyboardInterrupt): waiting.wait(gen, conn.socket)`; then
  `gen.gi_frame is None`; `conn.status == 0`; `transaction_status == 1`; `rs =
  waiting.wait(generators.cancel(conn), conn.socket, timeout=10)`: one result,
  `FATAL_ERROR` `b"57014"`; `transaction_status == 0`; `select 1` works. The method calls
  `self.skipTest` when `threading.current_thread() is not threading.main_thread()`.
- `test_connect_timeout`, `@support.only_modes("oracle", "server")`: `srv =
  socket.socket()`; `srv.bind(("127.0.0.1", 0))`; `srv.listen(1)` (it never answers);
  `gen = generators.connect(b"host=127.0.0.1 port=<its port> dbname=x user=y
  sslmode=disable gssencmode=disable")`; `waiting.wait_conn(gen, timeout=0.2)` raises
  `WaitTimeout` (`timeout expired`); `gen.gi_frame is None`.
- `test_connect_refused`, `@support.only_modes("oracle", "server")`:
  `waiting.wait_conn(generators.connect(b"host=127.0.0.1 port=1 sslmode=disable"),
  timeout=10)` raises `OperationalError` whose text starts with `connection failed:
  connection to server at "127.0.0.1", port 1 failed: Connection refused`.

#### CPYTHON-PQ-18-WAIT-ASYNC

Files: `stuntgres/waiting_async.py`, `tests/test_waiting_async.py`. Design sections: 8
(intro), 8.2.

Step. Write the module as 8.2 specifies.

Tests to write first. "Nothing registered" means `loop.remove_reader(fd) is False and
loop.remove_writer(fd) is False` for the running loop.

`WaitAsyncUnitTest(unittest.IsolatedAsyncioTestCase)` (socket pairs set non-blocking):

- `test_returns_value`, `test_write_ready`, `test_timeout` (`WaitTimeout("timeout
  expired")` after `timeout=0.05`), `test_interval` (three `Ready.NONE` with
  `interval=0.01`), `test_generator_error_propagates`: as in CPYTHON-PQ-17 with `await
  wait_async(...)`; after each, nothing is registered and the generator is closed.
- `test_cancelled_task`: `task = asyncio.create_task(wait_async(gen, fd))` on a socket
  with nothing to read; `await asyncio.sleep(0)` once so the task starts; `task.cancel()`;
  awaiting it raises `asyncio.CancelledError`; the generator is closed; nothing is
  registered.
- `test_wait_conn_async`: a generator yielding `(fd, Wait.W)`: returns its value.

`WaitAsyncTest(support.AsyncDatabaseTestCase)`:

- `test_connect_and_execute`: `conn = await wait_conn_async(generators.connect(DSN),
  timeout=10)`; `status == 0`; `nonblocking == 1`; nothing registered; `send_query(b"select
  1 as a; select 2 as b")`; `await wait_async(generators.execute(conn), conn.socket)` gives
  `[(b"1",)]` and `[(b"2",)]`; nothing registered.
- `test_loop_stays_responsive`: a ticker task increments a counter every
  `asyncio.sleep(0.01)` while `select pg_sleep(0.3)` is awaited: the counter is at least
  10.
- `test_cancel_awaiting_task`: `started = asyncio.Event()`; `conn.notice_handler = lambda
  res: started.set()`; `send_query(b"do $$ begin raise notice 'started'; perform
  pg_sleep(30); end $$")`; `gen = generators.execute(conn)`; `task =
  asyncio.create_task(wait_async(gen, fd))`; `await asyncio.wait_for(started.wait(), 10)`;
  `task.cancel()`; awaiting it raises `CancelledError`; `gen.gi_frame is None`; nothing
  registered; `transaction_status == 1`; `rs = await wait_async(generators.cancel(conn),
  fd, timeout=10)`: `[FATAL_ERROR b"57014"]`; `transaction_status == 0`; `select 5` ->
  `b"5"`.
- `test_timeout`: as CPYTHON-PQ-17's `test_statement_timeout_and_cancel` with `await
  wait_async(...)`: the `do` block with the notice, `timeout=0.2` raises `WaitTimeout`;
  the generator is closed; nothing registered; wait for the notice if it has not been
  seen (`support.wait_readable`, `consume_input`); `await
  wait_async(generators.cancel(conn), fd, timeout=10)` returns the `57014` error.
- `test_notifies_interval`: `await wait_async(generators.notifies(conn), fd,
  interval=0.05) == []`.
- `test_large_parameter`: an 8 MB parameter as in CPYTHON-PQ-16 gives `[(b"8000000",)]`;
  nothing registered.
- `test_connect_refused`, `@support.only_modes("oracle", "server")`: `OperationalError`
  whose text starts with `connection failed: connection to server at "127.0.0.1", port 1
  failed: Connection refused`.

#### CPYTHON-TEST-03-NATIVE

Files: `tests/support/native.py`, `tests/test_backend_native.py`. Design sections: 9.7.

A stronger model is advisable: nothing of this part could be run when it was designed
(libspq did not exist). Its expectations come from `docs/LIBSPQ.md` sections 1, 5.1, 5.2
and 6. If libspq answers differently, that is a libspq gap or a documentation error: stop
and report (rule C8), do not adapt the expectation.

Step. Write `tests/support/native.py` with `class Backend` as 9.7 specifies:
`__init__(self, build_dir: str, repo_root: str)`, `start()`, `acquire() -> str`,
`release(conninfo: str) -> None`, `stop()`. `release` takes the host from the conninfo
(`conninfo.split("host=", 1)[1].split(" ", 1)[0]`).

Tests to write first (`NativeBackendTest(support.DatabaseTestCase)` with `modes =
("native",)`):

- `test_conninfo`: matches `^host=t\d{5} port=5432 dbname=test user=test$`.
- `test_library`: `_ffi.get().flavor == "libspq"`; `pq.has_function("SPQXclusterDrop")`;
  `pq.version() == 180006`.
- `test_fresh_cluster_per_test_a` and `_b`, identical: with a `RawConn`: `select count(*)
  from pg_class where relname = 'isolated'` -> `[(b"0",)]`; `create table isolated (a
  int4)`.
- `test_session_facts`: `select current_database(), current_user` -> `[(b"test",
  b"test")]`; `show server_version_num` -> `[(b"180000",)]`.
- `test_release_drops_the_cluster`: `extra = support.backend.acquire()`; create a table
  through a `RawConn` and close it; `support.backend.release(extra)`; a new connection to
  `extra` finds no such table (`count(*)` of `pg_class` for it is `b"0"`); release it
  again in a cleanup.
- `test_release_with_an_open_connection_fails`: `extra = support.backend.acquire()`; keep
  a `RawConn` open; `support.backend.release(extra)` raises `RuntimeError` whose text
  starts with `cluster ` and contains `not dropped`; close the connection and release
  again without error.

Acceptance: `make libspq` was run; `python3 bindings/cpython/tools/runtests.py --mode
native test_backend_native test_support` passes. The whole suite in native mode is not
part of this acceptance: each family is verified against libspq by the `CPYTHON-NATIVE-`
parts that CPYTHON-DESIGN-FEATURES creates.

#### CPYTHON-PQ-19-EXT

Files: `stuntgres/pq/ext.py`, `tests/test_pq_ext.py`. Design sections: 5.15.

A stronger model is advisable, for the reason given under CPYTHON-TEST-03-NATIVE; the
expectations come from `docs/LIBSPQ.md` section 6 and `docs/design/LIBSPQ-CABI.md` 3.2.

Step. Write the module as 5.15 specifies.

Tests to write first.

`ExtGateTest(support.LibraryTestCase)`:

- `test_absent_with_libpq`, the method decorated `@support.only_modes("oracle",
  "server")`: `ext.version()` raises `NotSupportedError` with the text
  `PQXversion is not available in the loaded library`; `ext.cluster_drop(b"x")` raises
  `NotSupportedError`.

`ExtTest(support.DatabaseTestCase)` with `modes = ("native",)`:

- `test_version`: `ext.version() == b"0.1.0"`.
- `test_reset`: create a table and a row; `ext.reset(conn)` returns `None`; the table is
  gone (`select count(*) from pg_class where relname = 'et'` -> `b"0"`); the connection
  still works.
- `test_dump_restore`: create `et (a int4)` with one row `1`; `data = ext.dump(conn)` is
  non-empty `bytes`; `ext.reset(conn)`; `ext.restore(conn, data)`; `select a from et` ->
  `[(b"1",)]`; `ext.restore(conn, bytearray(data))` works too.
- `test_reset_inside_transaction_fails`: after `begin`, `ext.reset(conn)` raises
  `OperationalError` with the text `reset failed: cannot reset or restore the database
  inside a transaction block`.
- `test_cluster_drop`: `ext.cluster_drop(b"never-used-host", b"5432")` returns `None` (a
  cluster that does not exist); with an open connection to a host `h`,
  `ext.cluster_drop(h, b"5432")` raises `OperationalError` whose text starts with
  `cluster_drop failed: cluster ` and ends with `open connections`; after `finish()` it
  succeeds.
- `test_closed`: `ext.reset`, `ext.dump`, `ext.restore` on a finished connection raise
  `OperationalError` (`the connection is closed`).

Acceptance: `python3 bindings/cpython/tools/runtests.py --mode native test_pq_ext` passes,
in addition to the three other modes for the whole suite.

#### CPYTHON-ENGINE-01-DESCRIBE-SQL-CURSOR

An engine gap found in server mode (rule D8). Smallest reproduction, over the wire
protocol: `Query("BEGIN")`, `Query("DECLARE cur1 CURSOR FOR SELECT 1 AS one, 'x'::text AS
two")`, then `Describe(portal "cur1")` + `Sync`. libpq form: `PQexec` twice, then
`PQdescribePortal(conn, "cur1")`.

| | PostgreSQL 18.6 | stuntgres server (d4393e0c07) |
|---|---|---|
| Describe of `cur1` | `PGRES_COMMAND_OK`, 2 fields `one` (23), `two` (25); transaction stays open; `FETCH ALL FROM cur1` returns `(1, x)` | `PGRES_FATAL_ERROR`, SQLSTATE `34000`, `portal "cur1" does not exist`; the transaction is aborted |
| the same for a `WITH HOLD` cursor after `COMMIT` | `PGRES_COMMAND_OK`, 1 field | `34000`, `portal "cur2" does not exist` |
| `Close(portal "cur1")` | closes the cursor | closes the cursor (already correct) |

In PostgreSQL a cursor declared with SQL and a protocol-level portal are the same object.
The server's Describe path is `pgserver/protocol_extended.go` (the call
`scope.DescribeProtocolPortal(name)`), which does not find SQL cursors. Needed by
`PGconn.describe_portal` (the only way to describe a cursor through libpq) and by the
named cursors of CPYTHON-DESIGN-FEATURES.

#### CPYTHON-ENGINE-02-CANCEL-PLPGSQL-CONTEXT

An engine difference found in server mode, priority P2, no CPYTHON part depends on it.
Reproduction: run `DO $$ BEGIN PERFORM pg_sleep(30); END $$`, cancel it while it runs.

| | PostgreSQL 18.6 | stuntgres server (d4393e0c07) |
|---|---|---|
| SQLSTATE, primary message | `57014`, `canceling statement due to user request` | the same |
| `CONTEXT` field | `SQL statement "SELECT pg_sleep(30)"` / `PL/pgSQL function inline_code_block line 1 at PERFORM` | absent |

Other errors inside PL/pgSQL carry their context on the server (`PERFORM 1/0` and `RAISE
EXCEPTION` were checked and match); only the cancellation error lacks it.

## 11. Experiments behind this design

Run on 2026-11-11 on Linux amd64 (Ubuntu 24.04, CPython 3.12.3, Go 1.27.1, Docker
29.1.3) against the integration tip `d4393e0c07`. Scripts, commands and output are in
`.cswd/tmp/CPYTHON-DESIGN-PQ/`: `log.txt` is written by `run_all.sh` from the directories
`exp1` … `exp10`; `scratch/` holds the code that ran: the package `scratch/stuntgres`
(every module of section 1.2 except `pq/ext.py`, split as designed), the runner
`scratch/tools/runtests.py`, the support package and the harness self-tests under
`scratch/tests/`. Timings of the database and container measurements vary by a few percent between runs;
the GIL measurements of experiment 9 vary widely and are given as ranges.

| # | Question | Result | Decides |
|---|---|---|---|
| 1 | Lazy load with all signals blocked, from a thread other than the main thread; the pid check after `fork`; does importing map a libpq? | Importing `stuntgres`, `stuntgres.pq`, `generators`, `waiting`, `waiting_async` mapped no libpq and started no thread. First use from 8 racing worker threads: one `Library`, each file opened once, on a worker thread. A probe library opened as the first dependency recorded 60 blocked signals inside `dlopen` (all that can be blocked: 1 to 64 without `SIGKILL`, `SIGSTOP`, 32, 33); the mask afterwards equalled the mask before. A forked child got `InterfaceError` from `PGconn.connect` and `Conninfo.parse`; its `PGconn.__del__` left the pointer alone; the parent's connection still answered. The five loader error texts of 2.3 were observed. | Sections 2, 3.2; P2 to P4. |
| 2 | Does libpq 18 from the pinned image load on the host? | Not alone (`libldap-2.5.so.0` missing). It loads after `liblber-2.5.so.0` and `libldap-2.5.so.0` from the same image are opened first; `PQlibVersion()` 180006; all seven libpq 17/18 functions present. The host's libpq is 16.15 and lacks three of them. | 9.3; the seam variable is a list (2.2). |
| 3 | Oracle container driven with `subprocess` only: start, readiness, isolation cost, removal. | Ready and verified in 0.9 s; removed in 0.2 s; removed after a pass, a failure, `SIGINT`, `SIGTERM`; left by `SIGKILL` and swept by the next start. A database per test: 28 ms. The designed runner ran 150 database tests in 5.0 to 5.9 s and, signalled while a test blocked, exited 130 with nothing left behind. | 9.4; P14. |
| 4 | `cmd/stuntgres` built, started and connected to with libpq; isolation; differences. | Build 31 s cold, 0.1 s warm; one JSON line announces the port; a database created with SQL cannot be connected to; start-up costs 1.4 ms and 3 MB per served database; rolling pool of 64: 6 ms per test. 150 database tests ran in 0.7 s. Two differences from PostgreSQL became engine parts (below). | 9.5; the two CPYTHON-ENGINE parts. |
| 5 | Notice receiver: a handler that raises; a connection collected while the callback is registered. | A raising handler was logged (`stuntgres`, `ERROR`, `error in notice handler: boom`) and the call returned normally; so was `KeyboardInterrupt`. The connection was collected and its registry key removed; a notice raised afterwards through a surviving result (`PQgetvalue` out of range) reached no handler and logged nothing. Handlers ran on the calling thread, also on a worker thread, and could use another connection. Identical in both modes. A first attempt to re-post a `KeyboardInterrupt` with `_thread.interrupt_main()` from inside the callback failed (it is raised again inside the callback) and was dropped. | 5.9; P9. |
| 6 | `SIGINT` in the synchronous waiter during `pg_sleep`, then cancel and collect. | `KeyboardInterrupt` left `wait()` within 1 ms with the generator closed and the connection `ACTIVE`; `generators.cancel` returned the `57014` result within 3 ms; the connection was reusable; inside a transaction block the status became `INERROR` and `ROLLBACK` recovered. Identical in both modes except the `CONTEXT` field. Started from a background job the process inherits `SIGINT` ignored and is not interrupted. | 7.8, 8.1; the test of CPYTHON-PQ-17 installs the handler. |
| 7 | asyncio waiter on the libpq descriptor, with cancellation. | Nothing stayed registered after any call; other tasks ran during a statement; cancelling the awaiting task closed the generator and removed the reader; timeout and interval behaved as designed; an 8 MB parameter used the writer path. Identical in both modes. | 8.2; P12. |
| 8 | Validation slice: one statement and a multi-statement string, blocking and through the generators, in oracle and server mode. | Identical output in both modes (`exp8/slice.py`). The scenario probes (`exp8/probe.py`, `probe2.py`, `probe3.py`, about 250 lines of observed values per mode) are the source of every literal in section 10.3; in server mode `probe.py` differs from the oracle in three lines (the two `describe_portal` lines below and one about fields no test asserts), the other two in none. | Sections 5, 7, 10.3. |
| 9 | Cost of 10000 cells through ctypes, `CDLL` against `PyDLL`. | Alone about 10 ms against 6 to 9 ms; beside one CPU-bound thread 1.8 to 3.1 s against 12 to 13 ms; beside three, 100 s and more against 17 to 73 ms. A callback from a `PyDLL` call works. | 3.4; P7. |
| 10 | A pristine public-domain `dbapi20.py` that runs unchanged on Python 3.10 and 3.12. | Origin, version and hash as in 9.8; static checks pass. Executing the file was refused by the session's permission system and no Python 3.10 exists on the host, so "runs" is not established; the first test of CPYTHON-TEST-02 establishes the import. | 9.8. |

Differences between the stuntgres server (`d4393e0c07`) and PostgreSQL 18.6 that these
experiments turned into engine parts:

| Difference | Treatment |
|---|---|
| `Describe` of a portal that was created with SQL `DECLARE` answers `34000 portal "…" does not exist` (PostgreSQL returns the row description). | CPYTHON-ENGINE-01-DESCRIBE-SQL-CURSOR; prerequisite of CPYTHON-PQ-09-PREPARED. |
| The error of a statement cancelled inside PL/pgSQL has no `CONTEXT` field. | CPYTHON-ENGINE-02-CANCEL-PLPGSQL-CONTEXT (P2); no test of this foundation asserts the field. |

Everything else the probes exercise was identical in server mode: connection failures and
their texts, every result accessor, error fields and messages, parameters in text and
binary, prepared statements, notices, notifications, cancellation, COPY in both
directions, single-row and chunked modes, escaping, client encodings.

Not validated, and marked so in section 10.3: native mode (`tests/support/native.py`,
`pq/ext.py`), because libspq does not exist yet; the `aarch64` library path of 9.3; the
execution of the vendored suite (experiment 10).
