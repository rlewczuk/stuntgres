# cmd/stuntgres

The executable exposes the embedded engine through PostgreSQL simple/extended
queries, prepared statements, resumable portals, text/binary values, streaming
COPY, executable-OID FunctionCall and idle transactional notifications. Protocol
versions3.0/3.1/3.2 share the same session authority; higher3.x minors negotiate
3.2. This does not expand the engine's SQL/catalog/provider capabilities.
See [commands, account grants and security](../../README.md#phase-2-server--change-note)
and [native limits](../../docs/DIALECT.md#server-transport). Cleartext password
authentication is the only method; TLS and GSS encryption are unavailable.

One dispatcher owns the serialized writer and normal/extended/COPY/recovery
states. The existing reader has two reusable buffers and one queued message;
announced lengths never cause eager body-sized allocations. Engine operations
are fresh per extended command, not held across idle frontend waits. Streaming
COPY reads and writes synchronously through unlocked engine callbacks, with no
extra output worker or stream queue. Input publication waits for the terminal
protocol outcome; cancellation remains registered through output.

Prepared names, portal execution, codecs and transaction publication remain
engine-owned. Bind and FunctionCall use a bounded primitive receiver so lookup,
permission, receive and conversion errors keep their released precedence.
Notifications use a nonblocking engine poll/wake and only drain at TxIdle;
there is no idle activity lease or notification receiver worker.
Bootstrap ID1 is reserved and network session IDs equal global wire PIDs2+.
Cancellation compares the exact opaque4-byte or32-byte registered key in
constant time and never stores a pending cancel for the next command.
The registered user-cancel cause is an immutable SQL diagnostic; COPY enriches
an owned clone, never that shared cause. Input finishes actual physical frames
and shared conversion before surfacing a pending SQL cancel. The existing end
watcher observes canonical terminal request before lease drain; physical shutdown
requests the same authority, while final cleanup still waits and reports errors.

Operational and SQL JSONL retain separate sinks. Prepared SQL errors preserve
immutable original-source and typed/format-aware parameter context; simple SQL
retains `params:[]`. Raw undecoded binary/invalid UTF8 values are tagged base64,
NULL and empty values are distinct, and passwords/cancel keys are not parameters.
Normal-protocol C-string text, including exact case-sensitive statement/portal
names, is validated by the shared UTF8 input owner at each staged field read,
before later framing fields, lookup or unnamed-definition mutation. Invalid text
raises recoverable22021; binary values/COPY payloads/cancel keys remain opaque.
Startup and password text retain their separately owned policies.


## Testing

Follow the [shared testing guide](../../docs/TESTING.md). Public SQL, wire-client
and server lifecycle scenarios live in [reference/sessions](../../reference/sessions/README.md#phase-2-server-selected-proof);
this package retains command, filesystem, process, framing and log mechanics.

`make finalcheck` alone enables `noservertests` for its race suite. The Linux
`process_socket_test.go` file uses `linux && !noservertests`: it contains
`TestServerPIDSocketLifecycle/DumpAndStopActualChild` and the subprocess entry
`TestServerSocketProcessHelper`. The scenario still opens a real TCP listener,
checks readiness by dialing it, observes a dump signal/output, stops and joins the
child, and verifies PID retirement. Ordinary `make check`, `make quickcheck` and
`make racecheck` include it by default; production server builds are unchanged.

Socket-free checks remain included in finalcheck: PID ownership/stale recovery,
startup failure/readiness, CLI parsing, users/password files and permissions,
PTY signal handling, byte-backed framing, logging, persistence files and startup
parameter parsing. The original `TestServerProcessHelper` is now only the
socket-free PID-conflict subprocess; it must fail if acquisition unexpectedly
succeeds. Process launches retain their10-second context and5-second readiness
bound with owned cancellation/kill/join cleanup.

`TestServerProtocolFraming` exercises per-tag bounds, tiny fulfilled prefixes
under large length announcements, unknown tags before body parsing and bounded
receive primitives. `TestServerProtocolCancellation` checks full-key matching,
wrong/stale keys and current-command fencing for both key sizes.
It also pins primary diagnostic/context preservation, immutable cancellation
ownership and physical-end wake before an already-canceled operation's lease drains.
`TestServerPreparedLogValues` checks structured JSON values, canonical text,
base64 fallback, NULL/empty distinctions and detached log data.
`TestServerStartupParameters` checks exact/last-wins pairs, option order and
backslash parsing. These byte/function-level units supplement, rather than
replace, the independent public wire and SQL witnesses.

```sh
make check RUN='^TestServer(ProtocolFraming|ProtocolCancellation|PreparedLogValues)$'
make check RUN='^TestServer(Framing|Logging|StartupParameters|CLI)$'
make check RUN='^TestServerProtocolCancellation$' RACE=1
```


Run selected mechanics and the real TCP lifecycle from repository root, using an
external900-second deadline for each command:

```sh
make check RUN='^TestServer(PID|StartupFailure|PIDSocketLifecycle)$'
make check RUN='^TestServer(PID|StartupFailure|PIDSocketLifecycle)$' RACE=1
```

These are native process-unit checks, not dual-backend SQL definitions. Their
helper test entry points normally do nothing without the subprocess environment;
the enclosing scenarios exercise them. Selected reference server definitions
still require both native and real pinned PostgreSQL modes separately. A passing
socket-excluding finalcheck does not replace that proof or the TCP lifecycle run.
