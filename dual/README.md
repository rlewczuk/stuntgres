# dual

A `database/sql` connector for root-causing driver-mode differences. Every
driver call runs on a native connector (normally stuntgres), then on a paired
connection of a reference connector the caller supplies (normally a real
PostgreSQL through lib/pq or pgx). The caller always gets the native outcome,
unchanged. One JSON record per call, written to `Options.Log`, shows both
outcomes and, where they differ, readable `response_diff` lines. The package
imports only the standard library.

```go
func NewConnector(native, reference driver.Connector, opts Options) driver.Connector

type Options struct {
	Log                 io.Writer                                       // JSONL records; nil discards
	ReferenceTimeout    time.Duration                                   // <= 0: 30s
	Order               OrderPolicy                                     // OrderByClause (default), OrderAlways, OrderNever
	Normalize           func(Value) (text string, ok bool)              // non-NULL values; ok=false keeps the default
	ErrorFields         func(err error) (code, message string, ok bool) // ok=false keeps the default
	CompareErrorDetails bool                                            // also Detail, Hint, Position
	Uncompared          []Call                                          // mirrored and logged, never diffed
}
```

`NewConnector` panics on a nil native connector. A nil reference behaves like a
reference whose every Connect fails. `Driver()` returns the native driver; its
`Open` bypasses mirroring. `Close` closes the native connector when it is an
`io.Closer`. The reference connector belongs to the caller and is never closed.

## Usage

```go
func openDual(key, dsn, logPath string) (*sql.DB, error) {
	native, err := stuntgres.NewDriver().OpenConnector(key)
	if err != nil {
		return nil, err
	}
	reference, err := pq.NewConnector(dsn) // pgx: stdlib.GetConnector(*config)
	if err != nil {
		return nil, err
	}
	out, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(dual.NewConnector(native, reference, dual.Options{Log: out})), nil
}
```

Configure the native connector (notice handler, logical database, role) before
wrapping it, and give the reference DSN the matching database and role. Give
each connector its own log file: `connection_id` and `cycle` restart with every
connector, so records of two connectors in one file collide.

## Calls

- **Order.** Each call runs natively first, with the caller's context and
  arguments. Then the caller's goroutine waits, at most `ReferenceTimeout`,
  for the reference part, which runs in a goroutine of its own with a context
  that drops the caller's cancellation and deadline. Reference
  rows are drained completely, set by set, inside that bound. So the reference
  session gets the calls in native order and is idle when the next call arrives.
- **Pairing.** Each native connection owns one reference connection, opened in
  Connect. Statements and transactions pair with their counterparts on it.
  `BeginTx` passes the same `driver.TxOptions` to both sides.
- **Failure.** A reference Connect that fails or times out marks the pair
  **failed** (`08001 reference connection failed: <err>`). When a later call
  times out, its context is canceled and the pair is **lost**
  (`08006 reference did not respond within <T>; connection closed`); the
  abandoned goroutine closes the connection when its call returns. The pair is
  also lost after a reference panic (`reference connection lost: panic: <v>`).
  A reference error that is `driver.ErrBadConn`, or leaves the connection
  invalid (`driver.Validator`), loses the pair too, as do a failed reference
  `ResetSession` or an invalid `IsValid`
  (`08006 reference connection lost: <err>`). Failed and lost are final for that
  connection: a new reference session would lack the native session's state.
  Later records carry the synthesized code and are not mirrored.
- **Skips.** The reference part is skipped when the pair is failed or lost,
  when the native call returned `driver.ErrBadConn`, when it returned a context
  error with the caller's context done (running it would drift the reference
  state), and when the paired statement or transaction does not exist. A
  native `driver.ErrSkip` is returned to database/sql without a reference call
  or record.
- **Pool decisions.** `ResetSession` and `IsValid` always return the native
  result. A bad reference never turns into `driver.ErrBadConn` on the native
  side.

## Interfaces

database/sql picks code paths by interface presence, so the wrappers keep the
native driver's behavior:

| Interface | Wrapper |
|---|---|
| `ConnPrepareContext`, `ExecerContext`, `QueryerContext`, `ConnBeginTx`, `Pinger`; `StmtExecContext`, `StmtQueryContext` | Always. Natives without them get database/sql's own fallbacks (legacy methods, `ErrSkip`, `Ping` nil, `BeginTx` option rejections). |
| `SessionResetter`, `Validator`, `NamedValueChecker` (conn); `NamedValueChecker`, `ColumnConverter` (stmt) | Exactly when the native object has them; they forward. |
| Rows `ColumnType*` (all five) and `RowsNextResultSet` | Always, with database/sql's neutral defaults when native lacks them. |
| Legacy `Execer`, `Queryer` | Never. database/sql uses them only without the context forms. |
| `io.Closer` (connector) | Always. |

Native-only stuntgres APIs (`CopyFrom`/`CopyTo`, `WaitForNotification`,
Reset/Dump/Restore, `InspectIndex`) reject a wrapped connection. Run them on
`sql.OpenDB(native)` over the same connector, which shares the engine. Their
effects are not mirrored, so the reference drifts from that point.

## Comparison

Values are canonicalized to PostgreSQL-style text, keyed on the native column
type name (else the reference's). NULL is `null` and never reaches the hook.
`Options.Normalize` runs first. Then: `BYTEA` bytes become `\x` hex, other
bytes become text; `BOOL` becomes `t`/`f`; `FLOAT4`/`FLOAT8` text or floats are
rendered with Go's shortest form (`FLOAT4` at 32-bit precision; `NaN`,
`Infinity`, `-Infinity`); integers are decimal. `time.Time` values render by
type: `TIMESTAMPTZ` as a UTC instant (`… +00`), `TIMESTAMP` as wall clock,
`DATE`, `TIME` (lib/pq's 24:00:00 included) and `TIMETZ` with `±HH[:MM[:SS]]`.
Years ≤ 0 get a ` BC` suffix. Strings compare as they are.

Errors: SQLSTATE is read through `interface{ SQLState() string }`. The message
comes from an exported `Message` string field, else from `Error()` without the
`stuntgres: `, `pq: … (CODE)` or `SEVERITY: … (SQLSTATE CODE)` framing.
`ErrorFields` overrides this. SQL errors compare code and message; with
`CompareErrorDetails` they also compare Detail, Hint and Position. Other errors
compare by presence only, and context errors are equal to each other.

Result sets: native rows are compared when the caller closes them. Only the
consumed prefix of a set left early is compared, against the reference's
complete set. A set that matches only as a multiset is flagged `order_only`.
It adds a line when `Order` counts it: `OrderByClause` counts it iff the query
text has a top-level ORDER BY in any of its statements. Sets native never
reached are summarized, not compared. Likewise a reference error that lies
past the rows native consumed is logged in `error_ref` but not compared.

Lines (all capped together: 50, then `… N more differences`; values are JSON
strings or `null`, cut at 120 bytes with `…`):

| Situation | Line |
|---|---|
| reference failure on this call | the failure message alone |
| errors | `error: stuntgres <E>, reference <E>` (`none`, `CODE message`, `error <text>`, `canceled`) |
| details (opt-in) | `error detail: stuntgres <v>, reference <v>` (also `hint`, `position`) |
| Exec | `rows_affected: stuntgres 2, reference 3`; `last_insert_id: stuntgres unavailable, reference 7` |
| Prepare | `num_input: stuntgres 1, reference 2` |
| set count (native complete, no error) | `sets: stuntgres 1, reference 2` |
| columns / types | `#S columns: stuntgres […], reference […]`; `#S types[K]: stuntgres "TEXT", reference "VARCHAR"` |
| row count | `#S rows: stuntgres 2, reference 3`; partial: `#S rows: stuntgres at least 3 (partial), reference 2` |
| values | `#S row I[ (reference J)] COL: stuntgres <v>, reference <v>` (COL is `[K]` when the name is empty or repeated) |
| unpaired rows | `#S stuntgres only row I […]`, `#S reference only row J […]` |
| order only | `#S rows: same multiset, different order (first at row I)` |

Unmatched rows are paired in order after removing equal rows as multisets.
Reference rows past a partial native prefix are never reported as one-sided.

## Records

One object per line, keys in this order. Names follow the cmd/stuntgres
`query.log` where the meaning matches.

| Field | Meaning |
|---|---|
| `tstamp` | call start, RFC 3339 UTC |
| `connection_id` | dual connection number, 1-based in Connect order |
| `cycle` | per-connection call counter, 1-based |
| `call` | `connect`, `ping`, `prepare`, `exec`, `query`, `stmt_exec`, `stmt_query`, `stmt_close`, `begin`, `commit`, `rollback`, `reset_session`, `is_valid`, `close` |
| `statement` | cycle of the `prepare` record (prepare and `stmt_*`; omitted otherwise) |
| `query` | SQL text; statement calls carry the prepared text |
| `params` | `[]` or `{"index","name"?,"type"?,"format":"text","null","encoding":"text"\|"base64","value"}`. `type` is the Go type of the argument (`int64`, `[]byte`, `time.Time`, …), not query.log's SQL type; a nil argument, including a nil `[]byte`, is `"null":true` without `type` |
| `tx_options` | begin only: `{"isolation":"Repeatable Read","read_only":true}` |
| `response`, `response_ref` | summaries: exec `{"rows_affected","last_insert_id"}` (null: unavailable), rows `{"sets":[{"columns","types","rows","partial"}],"complete"}`, prepare `{"num_input"}`, is_valid `{"valid"}`, else `{}` |
| `error`, `error_ref` | SQLSTATE or `""`; `error_ref` carries `08001`/`08006` for a failed or lost pair |
| `errmsg`, `errmsg_ref` | messages (omitted when empty) |
| `unmirrored` | why the reference did not run: `reference connection failed`, `reference connection lost`, `canceled`, `native bad connection`, `native connect failed`, `no reference statement`, `no reference transaction` |
| `uncompared` | both ran but were not compared: `option`, `canceled` |
| `order_only` | some set matched only as a multiset |
| `response_diff` | the lines above, only when non-empty |
| `duration_us`, `duration_ref_us` | native time (queries: until rows Close); reference task time |

Records of one connection are written in cycle order. A record waits only while
an earlier cycle's rows are still open.

## Concurrency

database/sql serializes every call on one driver connection and its
statements, transactions and rows. The per-connection state is owned by that
serialization and has no lock. Abandoned reference goroutines touch only their
own reference connection. Two leaf mutexes exist: the per-call handoff between
a reference goroutine and its waiter, and the log mutex held for the single
`Write` of one complete record. No driver call, hook or encoding runs under
either. Hooks run in the calling goroutine, but different connections may run
them at once, so they must be goroutine-safe.

## Limitations

- Reference rows (and the native rows consumed) are buffered in memory per
  query.
- pgx stdlib runs every Query through the extended protocol, so multi-statement
  Query strings fail on it. Use lib/pq for projects that send them.
- Go 1.27's `driver.RowsColumnScanner` is not forwarded; natives that implement
  it are read through `Next`.
- The ORDER BY check is lexical: a multi-statement text counts when any of its
  statements has a top-level ORDER BY.
- Positions and details of stuntgres errors decorated with an extra diagnostic
  are not reflected (only code and message).

## Testing

- Units (no SQL, test-local fakes): `make check RUN='^TestDual'`.
- Reference family (`reference/sessions`, quick mode mirrors onto a second
  native engine, `REFERENCE=1` onto the pinned PostgreSQL through lib/pq):
  `RUN='^TestReferenceDualConnector'`.
