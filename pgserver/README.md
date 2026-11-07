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
  The existing UTF-8/name/password/grant constraints, duplicate-user rejection,
  secure userfile checks and randomized constant-time unknown-user verifier apply.
  Production PBKDF2-SHA256 cost is 600000 (16-byte salt, 32-byte key).
- Every served database owns a fresh engine and independent catalog/roles. An
  admitted account is a database-local `SUPERUSER LOGIN`; SQL-created roles do not
  become network accounts. No existing Driver/cluster injection is provided.
- `DumpDir` selects native, atomic timestamped SQL publications; `Restore` requires
  it and selects each database's newest dump, never falling back on corrupt data
  or missing owners. Dumps are 0600; directories are created 0700.
- `LogWriter` receives operational/SQL JSONL; nil is quiet `io.Discard`. `LogDir`
  opens append-only 0600 `server.log`/`error.log` in a 0700 directory, optionally
  mirrored to LogWriter. No default stdout, global logger or process state changes.
- `Verbose` admits the existing DEBUG records. `QueryLog` enables exact command-cycle
  capture; it requires LogDir or `QueryLogWriter`. QueryLogWriter without QueryLog,
  or combined with LogDir, is an error. Directory `query.log` never mirrors to
  LogWriter. Caller writers are never closed; only server-opened files are owned.
  Shared non-thread-safe writers across separate Servers need caller synchronization.
- `References` is the existing repeatable `DB=postgres[ql]://...` syntax, requires
  QueryLog, and must name served databases. The shared parser preserves duplicate
  rejection/redaction and libpq passfile precedence. There is no eager reference
  connection. `ReferenceTimeout` zero means 30s; negative is rejected (the CLI
  separately rejects explicit `0s`). Reference records retain the CLI query-log
  contract, comparisons and failure behavior.
- Startup authentication remains cleartext, without TLS. Use loopback or a protected
  network; non-loopback TCP logs the unchanged warning. No new authentication,
  startup-GUC or Unix-socket semantics are added.

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

## Contract

- Protocol 3.0, 3.1 and 3.2 share one session authority; newer 3.x minors
  negotiate 3.2. Cancel keys are 4 bytes (3.0/3.1) or 32 opaque bytes (3.2),
  compared in constant time; a cancel never stays pending for a later command.
- One dispatcher owns the serialized writer and the normal/extended/COPY/recovery
  states. The reader keeps two reusable buffers and one queued message; announced
  lengths never trigger body-sized allocations.
- Engine operations are fresh per extended command, never held across idle
  frontend waits. Prepared names, portals, codecs and transaction publication are
  engine-owned; wire names share SQL PREPARE/EXECUTE/DEALLOCATE authority.
- COPY streams synchronously through unlocked engine callbacks, with no extra
  worker or queue; input publication waits for the terminal protocol outcome.
- Notifications use a nonblocking engine poll/wake and drain only at TxIdle.
- Session ID 1 is reserved for bootstrap; network session IDs equal wire PIDs
  and SQL `pg_backend_pid()` (2 through signed-int4 capacity). Exhaustion rejects
  admission without wrapping or registering a replacement identity.
- CancelRequest and SQL cancellation share session.UserCancelCause; COPY enriches
  an owned clone. Physical termination is independent of the first context cause
  and wakes pending frontend/COPY input before waiting for engine lease cleanup.
  The end watcher only arms a one-shot socket write deadline, never writes bytes.
  The serial writer joins that arm before installing a bounded one-second FATAL
  budget. Failed/partial writes remain terminal; bufio errors are never reset,
  retried or followed by a fabricated fatal frame.
- Normal-protocol C-string fields (including case-sensitive statement/portal
  names) decode through the admitted `client_encoding` pair before later
  framing, lookup or mutation. Invalid input raises recoverable 22021.
  Textual output fields convert before message lengths are published.
  Bind diagnostics add a text-value suffix only after client decoding has
  succeeded and canonical type input begins; conversion failures retain the
  parameter location without pretending that type input received a value.
  Startup/authentication diagnostics remain raw until session admission;
  startup application names undergo original NAME clipping with the UTF8 database
  length rule, then sanitize each non-ASCII/control byte as `\xNN`. Their typed
  captured defaults are never clipped again after ASCII expansion.
- Client encodings use actual visible bidirectional default conversion
  providers, not a transport-only whitelist. UTF8 and SQL_ASCII need no
  provider; MULE_INTERNAL remains an unsupported SET/startup conversion.
  Native Go APIs remain Unicode. Binary framing, numeric/bytea/internal-CHAR
  payloads and cancel keys stay opaque; textual binary leaves convert.
- COPY text/CSV decodes incrementally before record framing and encodes each
  output attribute before multibyte-aware escaping. Explicit `ENCODING`
  selects the file encoding independently of the session; COPY TO selecting the
  active encoding reuses its admitted outgoing capability, while COPY FROM and
  different output encodings resolve current directional defaults.
  Binary COPY keeps its framing and uses the session for textual leaves.
  Failed notifications produce a fatal diagnostic rather than a partial A
  frame. Idle delivery precedes ParameterStatus and ReadyForQuery.
  End-of-command notices remain fallible before statement Finish; actual writes
  use the active unlocked owner reservation, retaining conversion rollback and
  terminal transport-failure cleanup for simple, extended and FunctionCall
  commands. A top-level DO block's RAISE notices are sent and flushed as each
  RAISE completes (`CopyProtocolHooks.Notices`); a socket failure aborts the
  block. Diagnostics retain the failing command's encoding before unwind;
  FunctionCall reports recoverable errors at its own ReadyForQuery boundary.
- Dual-backend forwarding (`reference_forward.go`): the dispatcher owns the
  reference write side, waiting and record assembly, and forwards each
  message where the capture records it; one reader goroutine per connection
  owns decoding. The forwarder mutex is a leaf lock (no I/O, logging or other
  lock under it). Cancel forwarding reads only the immutable session key
  after releasing `server.mu`. A failure closes the reference so a late
  response is never attributed to a later cycle.
- Prepared SQL errors keep the immutable original source and typed,
  format-aware parameter context for logging.
- Simple commands attach their complete frontend SQL to the admitted operation.
  Extended Bind and every Execute/resume attach the original prepared source;
  nested SQL EXECUTE and FETCH keep the outer frontend command's source.
- XML text/binary values use the shared genuine codecs. Actual typed output
  attaches an ephemeral warning sink while the command is active; all simple
  result rows are consumed before Finish. Logging/metadata are quiet, and
  committed warnings precede a later conversion error. Wire text converts once
  through admitted client authority; XML binary send owns its declaration.

## Monitoring

Database monitoring preserves the real client disconnect cause before generic
cancellation/cleanup. A fully decoded frontend Terminate reserves normal close
and stops the bounded reader after publishing that message. The passive FIN
watcher is a provisional transport observation, not an accepted statistics cause;
generic InvalidConnection teardown joins the reader so decoded normal/protocol
decisions win before fallback abandonment. If closure prevents decoding a full
Terminate, the actual server decision is transport retirement. Recognizable
protocol FATAL reserves fatal before output; native timeout/admin requests already
own fatal/killed. Failed FATAL output and subsequent EOF cannot overwrite an
accepted cause. Existing diagnostics, bounded buffers and terminal-write budgets
are unchanged. Public real-wire proof:
`make check RUN='^TestReferenceStatDatabaseWire$'`, also with `REFERENCE=1`.
Each managed database owns its own engine, catalog and roles. Monitoring a
secondary wire database therefore requires an observer attached to that same
engine; its catalog is not part of an unrelated managed database's inventory.
The wire proof checks actual connection/catalog/view identities and accounts for
the genuine provisioning and observer sessions before exact client deltas.

## Testing

Follow [docs/TESTING.md](../docs/TESTING.md). Private byte/codec/log/persistence,
startup/transport and lifecycle ownership units live here. Public native-client
SQL/lifecycle observations are dual-mode tests in
[reference/sessions](../reference/sessions/README.md); existing subprocess fixtures
and all their server tests retain their original transport.

```sh
make check RUN='^TestServer(ProtocolFraming|ProtocolCancellation|PreparedLogValues|StartupParameters)$'
make check RUN='^TestServer(Framing|Logging|QueryLogDecode|QueryLogDiff|PersistenceFiles|ReferenceCore|ReferencePassfile|ReferenceSCRAM)$'
make check RUN='^(TestClientEncoding(Stream|StartupNameCapture|NoticeOwnership|NoticeStreaming)|TestXMLServerActiveOutput)$'
make check RUN='^(TestSessionActivityWire(Transport|IdentityCapacity)|TestSessionSignalTerminalWriter)$'
make check RUN='^TestEmbeddedServer(Config|Startup|Logs)$'
make check RACE=1 RUN='^TestEmbeddedServer(Lifecycle|Pipe)$'
make check RUN='^TestServerUsers(Ownership)?$'
make check RUN='^TestServerReferenceParser$'
```

Run every new embedded public selector with `REFERENCE=1` (PostgreSQL18.6) and
without it; `TestReferenceEmbeddedServer(Clients|Copy|Notifications|Cancellation|Isolation|Shutdown|Logging)`
is the separate in-process family. Production has no pgx/pq/cobra/viper imports.
