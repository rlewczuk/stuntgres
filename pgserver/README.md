# pgserver

An importable, standard-library-only PostgreSQL wire server over independent
embedded engines. The transport adds no SQL, catalog, type or cluster capability.
For command-line flags, configuration precedence, password administration,
PID/daemon/signals and on-disk log records see [cmd/stuntgres](../cmd/stuntgres/README.md).

## API

```go
srv, err := pgserver.Start(ctx, pgserver.Config{
    Accounts: []pgserver.Account{{
        Username: "app", Password: "secret", Databases: []string{"app"},
    }},
})
if err != nil { return err }
defer srv.Close()
// Use srv.DialFunc() with pgx/pgconn, or choose Listen: "127.0.0.1:0".
// Shutdown's context bounds this caller's wait, not finalization ownership.
return srv.Shutdown(cleanupCtx)
```

`Start(ctx, Config) (*Server, error)` is exactly `Open` -> `Prepare` -> method
`Start`. `Open(ctx, Config)` opens logging only, with copied account/grant/reference
slices. `Prepare()` loads/hashes accounts, selects latest dumps, provisions engines,
attaches references and restores, without listening or starting goroutines.
`(*Server).Start()` creates its dummy verifier/core, optionally binds TCP, writes
warnings/`server started`, then admits connections. The CLI registers signals
between Open and Prepare and acquires PID ownership between Prepare and Start.
`Log(ctx, slog.Level, message, args...) error` preserves required operational-write
errors for that process boundary and for embedded callers.

Startup stages are sequential and execute once. Invalid/repeated transitions
return ordinary Go errors. Close every returned staged handle. Startup failures
close partial transports/files, retain their error and never publish a final dump
or `server stopped`; no goroutine starts before successful method Start.

### Configuration and ownership

- `Listen`: empty means **no TCP socket**, only DialFunc admission. Nonempty is
  passed to `net.Listen("tcp", ...)`; `127.0.0.1:0` obtains an ephemeral port.
- `UserFile` and non-nil `Accounts` are mutually exclusive. Nil or empty accounts
  are valid and admit no network identities. `Account` has `Username`, plaintext
  `Password`, and database grant slice `Databases`. Prepare validates and hashes
  once through the shared PBKDF2 owner, then drops its retained plaintext copy.
  Userfile validation and unknown-user verification belong to the shared
  `internal/serverauth` owner.
- Every served database owns a fresh engine and independent catalog/roles. An
  admitted account is a database-local `SUPERUSER LOGIN`; SQL-created roles do not
  become network accounts. No existing Driver/cluster injection is provided.
- `DumpDir` selects native, atomic timestamped SQL publications; `Restore` requires
  it and selects each database's newest dump, never falling back on corrupt data
  or missing owners. Dumps are 0600; directories are created 0700.
- `LogWriter` receives operational/SQL JSONL; nil is quiet `io.Discard`. `LogDir`
  opens append-only 0600 `server.log`/`error.log` in a 0700 directory, optionally
  mirrored to LogWriter. No default stdout, global logger or process state changes.
- `Verbose` admits DEBUG records. `QueryLog` enables exact command-cycle
  capture; it requires LogDir or `QueryLogWriter`. QueryLogWriter without QueryLog,
  or combined with LogDir, is an error. Directory `query.log` never mirrors to
  LogWriter. Caller writers are never closed; only server-opened files are owned.
  Shared non-thread-safe writers across separate Servers need caller synchronization.
- `References` uses repeatable `DB=postgres[ql]://...` declarations, requires
  QueryLog, and must name served databases. The shared parser preserves duplicate
  rejection/redaction and libpq passfile precedence. There is no eager reference
  connection. `ReferenceTimeout` zero means 30s; negative is rejected (the CLI
  separately rejects explicit `0s`). Reference records use the CLI query-log
  contract, comparisons and failure behavior.
- Startup authentication is cleartext, without TLS. Use loopback or a protected
  network; non-loopback TCP logs a warning.

### Connections and cancellation

`Addr() net.Addr` returns the actual TCP address (nil without TCP), immutable after
Start and readable after shutdown. `DSN(user, password, database) (string, error)`
constructs an escaped PostgreSQL URL with `sslmode=disable`; it errors without a
bound TCP address. Passwords for DSN construction are explicit, never retained.

`DialFunc() func(context.Context, string, string) (net.Conn, error)` creates an
owned in-memory pipe and uses exactly the TCP admission/dispatcher path. It ignores
**all** supplied network/address strings, including pgconn's second CancelRequest
dial using `pipe,pipe`. The returned connection retains its real pipe addresses;
its lifetime after success is not tied to the dialing context. A canceled dial
or closed admission cannot leave an untracked endpoint/client.

For pgx/pgconn use ParseConfig with a numeric `127.0.0.1` host and
`sslmode=disable`, set `DialFunc = srv.DialFunc()`, then ConnectConfig. pgxpool uses
`poolCfg.ConnConfig.DialFunc` and NewWithConfig. DNS resolution precedes DialFunc;
a fictional host is not a DNS bypass. Alternatively start ephemeral TCP and pass
srv.DSN to pgx.Connect or lib/pq. See [embedded recipes](../docs/GRIND.md#embedded-mode).

Each Server has its own accounts, engines, log owner, IDs and backend registry.
Independent Servers may both allocate PID2; cancellation PID+secret cannot cross
instances. Cancellation is the real protocol request on a second connection,
not context/SQL emulation. Owned pipe-close notifications observe disconnect even
when the two-buffer reader is backpressured, without consuming protocol bytes.

### Persistence and termination

- `Dump(ctx) error` synchronously reports its serialized cycle's result. Without
  DumpDir it is a no-op while running. Cancellation bounds queued admission,
  waiting and active engine work; buffered results cannot strand abandoned callers.
  A publication failure is logged, but only required-log/core failure is fatal.
- `Shutdown(ctx) error` closes admission immediately, cancels every current command
  before any session rollback, joins transports/sessions, makes the final dump,
  emits `server stopped`, and closes owned logs. This is fast cancellation/rollback,
  not PostgreSQL smart draining. A timed-out caller can wait again; required cleanup
  uses an independent context. Parent cancellation and core/accept/log failures
  trigger the same owned finalization path.
- `Close() error` is immediate abort: cancel commands/sockets and active persistence,
  skip an unstarted final dump, join all ownership and close logs. It may supersede
  incomplete Shutdown; already atomically published files are not undone. Abort
  emits no new stopped record. Close after completed Shutdown is harmless.
  The graceful stopped record is selected atomically after persistence; Close
  arriving after that selection joins the already-committed graceful finalization.
- `Done() <-chan struct{}` closes exactly once after finalization and owned closure.
  `Err() error` is nil until completion, then the stable joined failure result.
  Healthy Shutdown/Close and normal parent cancellation are not error causes.
  Actual I/O/cleanup failures remain visible; calls after log closure return
  `os.ErrClosed`. Caller I/O callbacks must eventually return: Go cannot terminate
  an arbitrary blocked writer.

## Ownership and lock order

The public lifecycle mutex owns stages, terminal selection, the dump request queue,
active dump cancellation and stable completion publication. One lifecycle worker
serializes explicit dumps and terminal finalization; it does not hold that mutex
over engine work, hashing, file/network/log I/O or user callbacks.

Private `server.mu` owns physical clients, backend registrations, identity allocation,
admission and command-cancel slots. Closing admission uses the same lock as
`workers.Add`; accept is stopped/joined before workers.Wait, so no zero-count
Wait races later Add. Lifecycle may acquire core.mu only to fence admission; core
never acquires lifecycle.mu. Context cancellation fences exact command slots under
core.mu, without engine/socket I/O; release it before reference cancel socket work.
Shutdown cancels all command descendants before allowing any session to end.

The log mutex serializes complete records across operational/SQL/query sinks and
owned file closure. It is a leaf relative to lifecycle/core/reference state locks:
no state lock is held when invoking a writer. The reference-forwarder mutex is a
leaf with no I/O/logging/other lock under it. Client finish joins reader, passive
watcher, end watcher, reference reader and native session cleanup; the public owner
also joins accept, parent/core watcher and lifecycle completion before Done.

## Protocol and execution boundaries

- Protocol 3.0, 3.1 and 3.2 share one session authority; newer 3.x minors
  negotiate 3.2. Cancel keys are opaque and instance-local; cancellation targets
  an active command, never a later one.
- One dispatcher owns serialized output and normal/extended/COPY/recovery state.
  The reader has bounded reusable buffering; announced message lengths never
  cause body-sized allocations.
- Engine operations are fresh per extended command and are not retained over
  idle frontend waits. Prepared names, portals, codecs and transaction
  publication are engine-owned.
- COPY streams through unlocked engine callbacks. Input publication waits for
  the terminal protocol outcome; notification delivery waits for an idle
  transaction boundary.
- CancelRequest and SQL cancellation share the engine's cancellation cause.
  Physical termination independently wakes frontend/COPY waits before lease
  cleanup. Only the serial writer emits terminal frames; partial/failed writes
  remain terminal and are never retried or followed by fabricated output.
- Client encodings use captured engine conversion authority and shared typed
  codecs, not a transport-only registry. Decode before name lookup or mutation;
  encode textual output before publishing lengths. Binary framing and opaque
  payloads remain bytes. Startup diagnostics precede session admission and do
  not borrow later runtime authority.
- The admitted operation retains original frontend SQL and typed parameter
  context for diagnostics/logging. Nested execution does not substitute a
  different frontend source.
- Dual-backend forwarding has one dispatcher-owned write side and one reference
  reader per connection. Failures close the reference so late responses cannot
  be attributed to a later cycle.
- Monitoring records an accepted disconnect cause before generic cleanup.
  Decoded normal termination and protocol decisions take precedence over
  provisional transport observations; output failure cannot overwrite an
  already accepted fatal/administrative cause.

Each managed database has an independent engine, catalog and roles. Monitoring
must use an observer attached to the database being observed.

## Testing

Follow [docs/TESTING.md](../docs/TESTING.md). Private byte/codec/log/persistence,
startup/transport and lifecycle ownership units live here. Public native-client
SQL/lifecycle observations are dual-mode tests in
[reference/sessions](../reference/sessions/README.md).

Reusable selectors: `RUN='^TestServer'` for wire/adapter units and
`RUN='^TestEmbeddedServer'` for embedded configuration/lifecycle units. Use
`RACE=1` for lifecycle and pipe ownership. Public embedded-client scenarios use
the dual-mode `RUN='^TestReferenceEmbeddedServer'` family.
