# cmd/stuntgres

The executable serves the embedded engine over the PostgreSQL wire protocol:
simple and extended queries, prepared statements, resumable portals, text/binary
values, streaming COPY, FunctionCall by routine OID and LISTEN/NOTIFY delivery to
idle listeners. Transport adds no SQL, catalog or type capability of its own.

## Operation

Commands (`./stuntgres --help` lists every option and long alias):

```sh
stuntgres serve|start [-l ADDR] [-d DUMPDIR] [-L LOGDIR] -u USERFILE [-p PIDFILE] [-r] [-v] [-b] [-q]
                      [--reference DB=URL]... [--reference-timeout DURATION]
stuntgres dump|stop [-p PIDFILE]
stuntgres passwd -u USERFILE [--databases DB1,DB2] USER
```

- **Configuration.** Each option resolves from its flag, then the
  `STUNTGRES_<OPTION>` environment variable (`STUNTGRES_LISTEN`,
  `STUNTGRES_USERFILE`, `STUNTGRES_RESTORE=true`, ...), then `--config FILE`, then
  its default. The config file is YAML, TOML or JSON (by extension) keyed by long
  option names (`listen`, `dumpdir`, `logdir`, `userfile`, `pidfile`, `restore`,
  `verbose`, `background`, `querylog`, `reference` (a list),
  `reference-timeout`; `STUNTGRES_REFERENCE` is whitespace-separated and
  `STUNTGRES_REFERENCE_TIMEOUT` uses an underscore); unknown keys and malformed booleans are
  errors, and one file can serve every command (each uses only its own keys).
  `--databases` is flag-only. `start` hands the daemon its resolved options as
  arguments, never through the environment or config file.

- **Accounts.** `passwd` prompts twice without echo. New accounts require
  `--databases`; omitting it keeps an existing account's grants. Changes apply
  after restart. The userfile stores PBKDF2-SHA256 verifiers (600000 iterations,
  16-byte salt, 32-byte key). On Unix it must belong to the serving user with no
  group/other access (normally 0600); on Windows its DACL may grant only the
  serving user, SYSTEM and local Administrators. Each permitted account is a
  database-local `SUPERUSER LOGIN`; the userfile is the cross-database gate.
  SQL-created roles do not become network accounts.
- **Security.** Only cleartext password authentication and UTF8 client encoding
  are implemented; SSL/GSS requests are refused. Listen defaults to
  `127.0.0.1:5434`, a bare port binds loopback, and non-loopback binding logs a
  warning. Keep passwords in a 0600 PGPASSFILE, not argv.
- **Lifecycle.** `serve` runs in the foreground; `start` (or `serve -b`)
  re-executes as a Linux daemon and waits for readiness. `-p` selects the PID file
  (default `./stuntgres.pid`). `dump` (or SIGUSR1) requests an asynchronous dump;
  `stop` waits for connections to close, open transactions to roll back and a
  final dump. PID, daemon and signal controls are Linux-specific; foreground
  serving is portable.
- **Dumps.** Each database is written atomically as a 0600
  `<database>--<UTC YYYYMMDDhhmmss>.sql` native SQL dump; earlier files are kept.
  `-r` (requires `-d`) restores the newest file for every database before
  listening. A corrupt newest file or a missing named owner fails startup; there is
  no fallback to older files.
- **Logs.** Operational `server.log` and SQL `error.log` are JSONL, mirrored to
  stdout (stdout only without `-L`). SQL records carry the query, SQLSTATE,
  message, a `SYNTAX`/`UNSUPPORTED`/`SQL` class and Bind parameters (canonical
  text, or tagged base64 for undecoded bytes); simple queries log `params:[]`.
  Passwords and cancel keys are never logged.
- **Query log.** `-q` (requires `-L`) appends one JSONL record per command
  cycle to `query.log` (0600, never mirrored to stdout): a simple Query, a
  FunctionCall, or extended messages through Sync, ending at ReadyForQuery.
  Fields: `connection_id`, `database`, `user`, `cycle` (per connection),
  `frontend` (message tags, e.g. `PBDES`, COPY input included), `query` (first
  SQL source), `params` (first Bind/Execute, same shape as `error.log`),
  `steps` (each frontend message with its statement/portal, SQL, params and
  base64 COPY input), `response`, `error` (first SQLSTATE), `complete` and
  `duration_us`. Each `response` entry is one backend message: `type`, `name`,
  `data` (exact body, base64; the authority) and `decoded` (readable form;
  binary or unprintable values as `{"hex":...}`, `null` if malformed). Output
  sent outside a cycle (idle notifications, a terminal error) is its own record
  with empty `frontend`; a cycle cut off by disconnect has `complete:false`.
  Startup and authentication are never captured. Each cycle is buffered whole
  until it ends.
- **Dual-backend mode.** `--reference DB=URL` (repeatable, one per database,
  requires `-q`; DB must be a served database) compares DB with a real
  PostgreSQL server. URL is
  `postgres[ql]://user[:password]@host[:port][/dbname][?sslmode=disable|require|verify-full]`
  (port 5432, dbname DB and sslmode `disable` by default; `require` does not
  verify the certificate). Without a URL password the libpq password file
  (`PGPASSFILE`, else `~/.pgpass`, at most 0600) supplies it; prefer that,
  because `start` passes the URL to the daemon in argv. Each connection to DB
  also logs in to the reference (trust, cleartext, MD5 or SCRAM-SHA-256) with
  the client's startup settings and forwards every frontend message in order;
  clients always receive stuntgres's responses, but a cycle's ReadyForQuery is
  held until the reference finished it too. Records of DB add `response_ref`
  (the reference messages, same shape as `response`), `error_ref` (its first
  SQLSTATE) and, only when they differ, `response_diff`: readable lines such as
  `#1 DataRow values[0]: stuntgres "1", reference "2"` or
  `reference only #0 ErrorResponse 42P01 ...`, at most 50 plus a count.
  Diagnostic `file`/`line`/`routine` and notification `pid` are not compared.
  A reference left in COPY FROM STDIN that the client will not feed gets a
  CopyFail. After `--reference-timeout` (default `30s`) the reference gets a
  cancel request; one more timeout closes it. A failed, lost or closed
  reference never blocks clients: `server.log` gets a warning and every later
  record of that connection ends `response_ref` with a synthesized FATAL
  ErrorResponse (`08001` login, `08006` lost or timed out). Reference output
  received while idle is written with the next idle record, or as its own
  record before the next cycle. Other databases work as
  before.

## Contract

- Command parsing (cobra) and option resolution (viper) build fresh instances per
  invocation; `parseCLI` only resolves and validates, `runCLI` acts.

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
- The registered user-cancel cause is an immutable SQL diagnostic; COPY enriches
  an owned clone.
- Normal-protocol C-string fields (including case-sensitive statement/portal
  names) are validated by the shared UTF8 input owner at each field read, before
  later framing, lookup or mutation; invalid text raises recoverable 22021.
  Binary values, COPY payloads and cancel keys stay opaque.
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

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). Public SQL, wire-client and
server-lifecycle scenarios live in [reference/sessions](../../reference/sessions/README.md);
this package keeps command, filesystem, process, framing and log mechanics.

Session activity source transport and identity-capacity mechanics use
`RUN='^TestSessionActivity'`.

- `process_socket_test.go` (`linux && !noservertests`) holds the real TCP
  lifecycle (`TestServerPIDSocketLifecycle`) and its subprocess entry
  `TestServerSocketProcessHelper`; the `noservertests` tag excludes it only from
  the `race` target. Process launches use a 10-second context and 5-second
  readiness bound with owned kill/join cleanup.
- Build tag `stuntgres_reduced_kdf` (`password_cost_reduced.go`) lowers PBKDF2 to
  4096 iterations so the reference server fixture logs in cheaply. It is
  test-only; production builds and this package's tests use 600000
  (`password_cost.go`).
- Byte- and function-level units (framing, cancellation, prepared log values,
  startup parameters) supplement the public wire witnesses in `reference/sessions`.

```sh
make check RUN='^TestServer(ProtocolFraming|ProtocolCancellation|PreparedLogValues|StartupParameters)$'
make check RUN='^TestServer(Framing|Logging|QueryLogDecode|CLI.*|PID|StartupFailure|PIDSocketLifecycle)$'
make check RUN='^TestServer(Reference.*|QueryLogDiff)$'
```
