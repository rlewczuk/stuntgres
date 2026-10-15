# cmd/stuntgres

## Testing

Follow the [shared testing guide](../../docs/TESTING.md). Public SQL, wire-client
and server lifecycle scenarios live in [reference/sessions](../../reference/sessions/README.md#phase-1-server-selected-proof);
this package retains command, filesystem, process and framing mechanics.

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
