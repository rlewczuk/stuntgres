# LIBSPQ-PGSESSION: extracting the protocol session from pgserver

Design for catalog part `LIBSPQ-DESIGN-PGSESSION`. It defines the package
`internal/pgsession` and the ordered steps that move the protocol orchestration out
of `pgserver` into it. Each step is one catalog part (`LIBSPQ-PGSESSION-01` .. `-20`).

Code facts were read at commit `c698641e5f` (master). Earlier steps shift line numbers,
so this document names files, functions, tests and subtests, never lines. Before
editing, locate every symbol with `git grep -n '<name>' -- pgserver internal/pgsession`.

## 1. Purpose and scope

libspq ([docs/LIBSPQ.md](../LIBSPQ.md)) drives the engine without sockets. The engine
has no protocol-session object: the orchestration over `core`'s protocol entry points
lives in pgserver's private `client` type. `internal/pgsession` becomes its one owner,
used by pgserver and by libspq.

**The session is message-shaped.** It consumes one frontend message `(tag, body)` per
call and emits backend messages `(tag, body)` through a `Transport`. Bodies are the
ones pgserver builds today, already converted to the client encoding. Reasons:

- Two statement errors depend on the accumulated body length (the DataRow limit in
  `writeFields`, the text limit in `appendText`); only the body builder can raise them.
- Query-log capture and the reference diff consume `(tag, body)`.
- Moved functions keep their shape, so each step is a substitution table and wire
  output is identical by construction: bodies still pass through `wireCodec.send`.

**Moves to pgsession:** the per-message dispatch switch and everything below it
(simple query, extended commands, Sync, Flush, fastpath, COPY stream, row, description,
notice and error output, ready boundary, ParameterStatus diff, notification draining,
skip-until-Sync state), client-encoding text conversion, engine session admission
(`Start`), and two helpers (`WatchEnd`, `CommandSlot`).

**Stays in pgserver:** `server`, `client`, `newClient`, `register`, `cancelCommand`,
`beginCommand`, `startReader`, `releaseMessage`, `interruptTerminalWrite`, `finish`,
`serve`, the outer select loop of `run`, `knownFrontendTag`, `startup` and startup
parsing, `startReference`, `wireCodec` framing (`readStartup`, `readMessage`, `send`,
`flush`, `errorResponse`, `notice`), `framingError`, `newDatabases`, `provision`,
`bootstrapExecute`, logging, query log and query diff, reference forwarding,
persistence, lifecycle, pipe and peer files. Logging, query-log capture and reference
forwarding reach moved code only through the optional `Observer`.

**Parts that must not run concurrently with steps 01–19:** `GRIND-INFRA-SERVER-CLUSTER`
and `GRIND-INFRA-POSTGRES-COMMAND` (both pending) also edit `pgserver/startup.go` and
`pgserver/database.go`. Finish or postpone them before starting the chain.

## 2. Target API (final state after step 18)

```go
// Package pgsession is the transport-neutral PostgreSQL protocol session over
// one engine session: it executes frontend messages and emits backend message
// bodies. It owns no socket, no framing and no authentication.
package pgsession

// Message is one frontend message without its length word. Body is borrowed
// from the transport until the session calls Transport.Release for it.
type Message struct {
	Tag  byte
	Body []byte
}

// Transport is the consumer's side of one connection. All methods are called
// on the goroutine that calls the Session, never concurrently. No engine mutex
// is held during a call, but the command's operation lease usually is, and
// Send may run inside the engine's lock-releasing statement I/O (rows, COPY,
// notices, ProtocolOutput). A Transport method must not call back into the
// Session or the engine. A blocked Send or Flush keeps the statement open, so
// a transport that can block must become interruptible when the session ends.
type Transport interface {
	// Send delivers one backend message. body is valid only during the call.
	// Any error ends the connection. Return *sqlstate.Error only for a
	// message-size violation: in description, notice and COPY-notice paths
	// that type is treated as a statement error, every other error as a
	// transport failure.
	Send(tag byte, body []byte) error
	Flush() error
	// BeginFatal runs before a FATAL ErrorResponse is sent, so the transport
	// can bound the terminal write. An error ends the connection.
	BeginFatal() error
	// Messages yields frontend messages while COPY FROM STDIN is active
	// (CopyData, CopyDone, CopyFail; Flush and Sync are ignored). A closed
	// channel is an unexpected end of input.
	Messages() <-chan Message
	// Release returns a message body. Each dispatched or received message is
	// released exactly once.
	Release(m Message)
	// Ended yields the canonical FATAL once the engine ended the session.
	// A nil channel never fires.
	Ended() <-chan error
}

// Observer receives the hooks pgserver needs for error.log, query.log and
// reference forwarding. A nil Observer disables all of them.
type Observer interface {
	// Step reports the decoded statement or portal name, Describe/Close
	// target and SQL context of the message being dispatched. It may run
	// while the statement admission is held: it must not block or enter the
	// engine.
	Step(tag, target byte, name string, context session.ProtocolContext)
	// CopyInput reports each frontend message consumed by COPY FROM STDIN,
	// before it is interpreted. m.Body is borrowed for the call.
	CopyInput(m Message)
	// SQLError runs before each ErrorResponse. A non-nil result is returned
	// to the caller of the Session as a connection-ending error.
	SQLError(ctx context.Context, context session.ProtocolContext, err error) error
	// BeforeReady runs at each ready boundary before notifications,
	// ParameterStatus and ReadyForQuery are produced.
	BeforeReady(ctx context.Context)
}

type Config struct {
	Session   *session.Session // required; the engine is core.EngineOf(Session)
	Context   context.Context  // physical connection lifetime
	Transport Transport        // required
	Observer  Observer         // optional
}

// Session orchestrates the protocol for one engine session. It is confined to
// one goroutine. It does not end the engine session; its owner does
// (session.RequestEnd, then WaitEnd).
type Session struct{ /* unexported */ }

func New(cfg Config) *Session

// Dispatch executes one frontend message (Q P B D E C S H F; d c f outside
// COPY are discarded). ctx is the per-command context: cancel it with
// session.UserCancelCause for a query cancel, or with a FATAL *sqlstate.Error
// when the session is being terminated. While skipping to Sync after an
// extended-protocol error, non-Sync messages are released and ignored.
// A nil result means the connection continues. A FATAL *sqlstate.Error means
// the connection must end: if FatalSent reports false the caller reports it
// with WriteSQLError, otherwise it was already sent. ErrUnknownMessage means
// the tag is not a frontend message. Any other error is a transport failure:
// stop without further output.
func (s *Session) Dispatch(ctx context.Context, m Message) error

var ErrUnknownMessage = errors.New("pgsession: unknown frontend message type")

// Recovering reports whether the session is skipping messages until Sync.
func (s *Session) Recovering() bool

// WriteSQLError sends an ErrorResponse, calling Observer.SQLError first. For a
// FATAL it flushes, marks FatalSent and returns the diagnostic.
func (s *Session) WriteSQLError(ctx context.Context, source string, err error) error

// Fatal sends err as FATAL without an Observer record and flushes, once.
func (s *Session) Fatal(err error)
func (s *Session) FatalSent() bool

// ReportSettings sends ParameterStatus for every reported setting whose value
// differs from the last report. SendReady sends ReadyForQuery with the
// transaction status, or returns the session's FATAL.
func (s *Session) ReportSettings() error
func (s *Session) SendReady() error

// DrainNotifications sends and flushes every pending NotificationResponse if
// the session is idle, and returns the engine's wake channel for the next one.
func (s *Session) DrainNotifications() (<-chan struct{}, error)

// Codec is the session's client-encoding text codec, for a transport that
// must encode its own diagnostics identically.
func (s *Session) Codec() *TextCodec

// TextCodec converts protocol text to the client encoding. Before the session
// sets an encoding it passes text through unchanged.
type TextCodec struct{ /* encoding policy, ready flag */ }

// ErrorBody builds an ErrorResponse body; the error result is non-nil only
// for a nil err. NoticeBody builds a NoticeResponse body; a conversion
// failure is returned as *sqlstate.Error.
func (c *TextCodec) ErrorBody(err error, fatal bool) ([]byte, error)
func (c *TextCodec) NoticeBody(d sqlstate.Error) ([]byte, error)

const MaxMessageLength = (1 << 30) - 2

// Input is a bounded reader over one message body. It implements
// session.ProtocolInput; String decodes through Encoding.
type Input struct {
	Body     []byte
	Encoding sqltype.ClientEncodingPolicy
	Ctx      context.Context
}

func (p *Input) Uint16() (uint16, error)
func (p *Input) Int32() (int32, error)
func (p *Input) Bytes(n int) ([]byte, error)
func (p *Input) Byte() (byte, error)
func (p *Input) String() (string, error)
func (p *Input) End() error

func ConsumeString(body *[]byte) (string, error)
func ProtocolError(message string) error // *sqlstate.Error, code 08P01
func Diagnostic(err error) *sqlstate.Error
func DiagnosticBody(d sqlstate.Error, defaultSeverity string) []byte

// SettingNames is a session default provider that supplies no default and
// records the name of every setting definition it is asked about.
type SettingNames map[string]struct{}

type StartConfig struct {
	Engine   *core.Engine
	ID       ids.SessionID         // allocated by the caller, non-zero, unique per engine
	Role     string                // login role; no authentication here
	Database core.DatabaseSelector // zero selects the engine's default database
	Defaults map[string]string     // startup settings, names already lower-cased
	Known    map[string]struct{}   // definition names; an unknown default is FATAL 42704
}

// Start admits and starts one engine session without protocol output. The
// notice (application_name truncation) may be non-nil even when err is non-nil.
func Start(ctx context.Context, cfg StartConfig) (*session.Session, *sqlstate.Error, error)

// EndHooks are called by the watcher goroutine of WatchEnd.
type EndHooks struct {
	Abandon   func()                      // ctx ended first; runs before RequestEnd(InvalidConnection)
	Terminate func(fatal *sqlstate.Error) // engine ended the session; interrupt writes, cancel the command
}

// WatchEnd starts one goroutine. When the engine requests the session's end
// it calls Terminate and then delivers the same FATAL on ended. When ctx ends
// first it calls Abandon and requests the session's end. done closes when the
// goroutine exits.
func WatchEnd(ctx context.Context, s *session.Session, hooks EndHooks) (ended <-chan error, done <-chan struct{})

// CommandSlot holds the cancel function of the command in flight. Its mutex
// is a leaf: nothing is called while it is held except the cancel function.
type CommandSlot struct{ /* mutex, cancel func */ }

func (c *CommandSlot) Begin(parent context.Context) (context.Context, func())
func (c *CommandSlot) Cancel(cause error) bool // false when no command is in flight
```

Unexported state of `Session` after step 14: `engine *core.Engine`, `session
*session.Session`, `cycle *session.ProtocolCycle`, `ctx context.Context`, `transport
Transport`, `observer Observer`, `codec TextCodec`, `fatalSent bool`, `lastSettings
map[string]string`, `state protocolState`, `copyStream protocolCopy`, `copyHooks
core.CopyProtocolHooks`, `copyDrain [8192]byte`.

## 3. Invariants every step keeps

Each is visible in the current code; a step that would change one is wrong.

1. **Logical versus transport errors.** Three conventions coexist:
   - the named result `transportErr`, set inside the `describe`, `row` and `sink`
     closures of `executeSimple` and `extendedCommand`;
   - classification by type: a `*sqlstate.Error` from `writeDescription`,
     `writeNotices` or `protocolCopy.notices` is a statement error, any other error
     is a transport failure;
   - `protocolCopy.transportErr`, checked after `Finish`.
   A transport error returns straight out of the command with no further output.
   `wireCodec.send` returns a FATAL `*sqlstate.Error` for an oversized body, which is
   why `Transport.Send` documents the type convention.
2. **Output placement.** Rows, descriptions and streamed notices are written inside
   the engine's lock-releasing statement I/O. `writeActiveNotices` runs inside
   `scope.ProtocolNotices`. Detached notices, CopyDone, CommandComplete and
   ErrorResponse are written after `Finish`. In a multi-statement simple query the
   per-statement output runs inside `scope.ProtocolOutput`. In the single-statement
   case an output attempt that finds the operation canceled calls `rejectCommand`.
   All output of a command happens before its deferred `op.Done()`.
3. **FATAL order** in `writeContextError`: replace a bare `context.Canceled` by the
   context cause (only `session.UserCancelCause` or a FATAL); `ReserveDisconnectCause`;
   bound the terminal write; error-log record; ErrorResponse; flush; `fatalSent`.
4. **COPY.** The engine blocks in `protocolCopy.Read` on the dispatcher goroutine and
   pulls frames itself. A user cancel does not interrupt the frame wait (the engine
   consumes it at its record boundary); a FATAL from the end watcher and the physical
   context do. The borrowed frame is released on every exit. `extendedCommand`
   releases its own Execute message before running, so both reader buffers are free.
5. **Encoding refresh points do not move.** They decide which encoding a `SET
   client_encoding` statement's own tag and notices use: the top of each dispatched
   message, `readyBoundary`, `drainNotifications` (per notification), after
   `ExecuteProtocolSimple`, after each extended step and fastpath call, in
   `writeNotices`, `descriptionBody`, `protocolCopy.notices`, and once after session
   start.
6. **Notifications** are drained only while `TransactionState() == session.TxIdle`,
   at the top of every loop iteration and inside the ready boundary.
7. **Cancel slot.** Until step 18 `client.commandCancel` is read and written only
   under `server.mu`.
8. **`protocolState`** is only ever compared with `protocolRecovery` (the check in
   `run`); the other four values are never distinguished.

## 4. A consumer without sockets

```go
type conn struct {
	ctx   context.Context
	sess  *pgsession.Session
	slot  pgsession.CommandSlot
	in    chan pgsession.Message // COPY input from the application
	ended <-chan error
	mu    sync.Mutex
	out   []backend // decoded later into results, notifications, parameters
}
type backend struct {
	tag  byte
	body []byte
}

func (c *conn) Send(tag byte, body []byte) error {
	c.mu.Lock()
	c.out = append(c.out, backend{tag, bytes.Clone(body)})
	c.mu.Unlock()
	return nil
}
func (c *conn) Flush() error                       { return nil }
func (c *conn) BeginFatal() error                  { return nil }
func (c *conn) Messages() <-chan pgsession.Message { return c.in }
func (c *conn) Release(pgsession.Message)          {}
func (c *conn) Ended() <-chan error                { return c.ended }

func open(cluster *core.Engine, id ids.SessionID, role, db string, known map[string]struct{}) (*conn, error) {
	ctx := context.Background()
	es, _, err := pgsession.Start(ctx, pgsession.StartConfig{Engine: cluster, ID: id, Role: role,
		Database: core.DatabaseSelector{Name: db, Explicit: db != ""}, Known: known})
	if err != nil {
		return nil, err
	}
	c := &conn{ctx: ctx, in: make(chan pgsession.Message, 1)}
	c.sess = pgsession.New(pgsession.Config{Session: es, Context: ctx, Transport: c})
	c.ended, _ = pgsession.WatchEnd(ctx, es, pgsession.EndHooks{
		Terminate: func(f *sqlstate.Error) { c.slot.Cancel(f) }})
	if err = c.sess.ReportSettings(); err == nil {
		err = c.sess.SendReady() // out: S... Z
	}
	return c, err
}

func (c *conn) step(tag byte, body []byte) error {
	ctx, done := c.slot.Begin(c.ctx)
	defer done()
	return c.sess.Dispatch(ctx, pgsession.Message{Tag: tag, Body: body})
}
```

- Simple query: `step('Q', sql+"\x00")` emits `T D... C Z`.
- Extended: `P` emits `1`, `B` emits `2`, `D` emits `t`/`T`/`n`, `E` emits `D... C`,
  `S` emits `Z`. After an error only `S` is executed.
- COPY OUT emits `H d... c C Z`. COPY IN: `step('Q', ...)` blocks inside the engine,
  so it runs on its own goroutine; after `G` the application feeds `d`, then `c` or
  `f`, into `in`.
- Cancel from any goroutine: `c.slot.Cancel(session.UserCancelCause)`.
- Notifications while idle: `wake, _ := c.sess.DrainNotifications()` emits `A` for
  each pending one and returns a channel that fires when another arrives.
- End: `es.RequestEnd(session.CleanClose)`, then `es.WaitEnd(ctx)`.
- Known setting names: run one bootstrap session per engine with
  `session.SessionStart{ID: 1, Defaults: pgsession.SettingNames(known)}`, as
  `provision` in `pgserver/database.go` does, and keep the collected map.
- Logical database: `core.DatabaseSelector{Name: db, Explicit: true}`. The session's
  engine is then the admission target, which is why `New` uses `core.EngineOf`.

## 5. Steps

### Rules for all steps

- Files outside `pgserver/`, `internal/pgsession/`, and (step 19) `AGENTS.md` are not
  touched. Nothing under `internal/engine` changes.
- A whole-file move uses `git mv`, then edits.
- Substitutions apply to the moved code. "Call sites" are found with `git grep` on the
  name; the lists below were complete at commit `c698641e5f`.
- After every edit remove imports that became unused and add
  `"stuntgres/internal/pgsession"` where a file starts using the package; run
  `gofmt -w` on changed files.
- A moved test keeps every assertion. In package `pgsession` tests are in-package
  (`package pgsession`) and named `TestPGSession...`.
- Per-commit gate: `go build ./... && go test -count=1 -run '^$' ./...`
- **Unit set** (run after every step):
  ```
  make check RUN='^TestServer'
  make check RUN='^TestEmbeddedServer'
  make check RUN='^(TestClientEncoding|TestSessionActivityWire|TestSessionSignalTerminalWriter|TestXMLServerActiveOutput|TestPGSession)'
  make check RUN='^TestReferenceEmbeddedServer'
  ```
  A selector that matches no test after its tests moved is not a failure.
- Reference server tests build `cmd/stuntgres` and need `psql` on `PATH`.
- Frame-name check (rule L9) was run for every function these steps rename or move
  (`diagnostic`, `protocolInput`, `executeSimple`, `extendedCommand`,
  `functionCommand`, `rejectCommand`, `rejectProtocolCommand`, `readyBoundary`,
  `drainNotifications`, `writeFields`, `writeDescription`, `descriptionBody`,
  `writeNotices`, `writeActiveNotices`, `writeSQLError`, `writeContextError`,
  `settingsSnapshot`, `sendSettings`, `sendReady`, `startSession`, `protocolCopy`,
  `watchSessionEnd`, `beginCommand`, `refreshEncoding`): no test passes any of them
  as a frame or provider name. Repeat the check before renaming.

Steps 01–05 were applied to a scratch copy of the tree; the gate, all tests of
`./pgserver/` and `./internal/pgsession/`, and the reference selectors
`TestReferenceEmbeddedServer`, `TestReferenceServerProtocol`, `...Queries`,
`...Negotiation`, `...FrontendEncoding`, `...Logs`, `...ExtendedMalformed`,
`...FunctionCall` passed. The resulting diff is kept (uncommitted) as
`.cswd/tmp/LIBSPQ-DESIGN-PGSESSION/steps-01-05.patch`; it is an illustration, not a
substitute for doing each step. Steps 06–18 were derived by reading and not executed.

### Step 01: package and `Diagnostic` (about 90 lines)

Files: new `internal/pgsession/diagnostic.go`, `diagnostic_test.go`, `README.md`;
`pgserver/logging.go`, `wire.go`, `dispatch.go`, `query_output.go`, `server.go`,
`startup.go`, `logging_test.go`, `protocol_test.go`, `reference_test.go`,
`wire_test.go`.

1. Create `diagnostic.go` with the package comment of section 2 and
   `func Diagnostic(err error) *sqlstate.Error`: the body of `diagnostic` in
   `pgserver/logging.go`, unchanged, with its comment.
2. Delete `diagnostic` from `logging.go` (its `sqlstate` import becomes unused).
3. Replace every call `diagnostic(` by `pgsession.Diagnostic(` in the pgserver files
   above. Do **not** touch: the method `(*diffResponse).diagnostic` in
   `query_diff_test.go`; local variables named `diagnostic` (`client_encoding.go`,
   `protocol_copy.go`, `startup_test.go`, `xml_output_test.go`).
4. Move the subtest `"diagnostic preservation"` of `TestServerLogging`
   (`logging_test.go`) to `diagnostic_test.go` as `TestPGSessionDiagnostic`; copy the
   test type `loggingStateError` it uses (keep the original where other subtests use
   it).
5. `README.md`: title, one paragraph (transport-neutral protocol session over one
   engine session; used by pgserver and libspq; design in this document), and
   `## Testing` with `make check RUN='^TestPGSession'`.

Verify: gate; `make check RUN='^(TestServerLogging|TestServerProtocolFraming|TestServerFraming|TestServerReferenceCore|TestPGSession)'`.

### Step 02: `Input`, `ConsumeString`, `ProtocolError` (about 170 lines)

Files: `git mv pgserver/protocol_input.go internal/pgsession/input.go`; new
`internal/pgsession/input_test.go`; `pgserver/wire.go`, `dispatch.go`,
`protocol_extended.go`, `protocol_copy.go`, `startup.go`, `protocol_test.go`,
`wire_test.go`, `session_activity_test.go`, `client_encoding_notice_test.go`.

| In `input.go` | Becomes |
|---|---|
| `package pgserver` | `package pgsession` |
| type `protocolInput` | `Input` |
| fields `body`, `encoding`, `ctx` | `Body`, `Encoding`, `Ctx` |
| `protocolError` | `ProtocolError` |
| `consumeString` | `ConsumeString` (moved here from `wire.go` with its comment) |

Also move `var _ session.ProtocolInput = (*protocolInput)(nil)` and its comment from
the end of `protocol_extended.go` into `input.go` as `(*Input)(nil)`. `wire.go` loses
its `bytes` import.

Call sites in pgserver: composite literals `protocolInput{body: X, encoding: Y, ctx: Z}`
become `pgsession.Input{Body: X, Encoding: Y, Ctx: Z}` (`dispatch.go` four,
`protocol_extended.go` one, `protocol_copy.go` one, the three tests); the parameter
type `*protocolInput` of `functionCommand` becomes `*pgsession.Input`; `input.body`
in `extendedCommand` becomes `input.Body`; `protocolError(` becomes
`pgsession.ProtocolError(` (`protocol_extended.go` two, `protocol_copy.go` one);
`consumeString(` becomes `pgsession.ConsumeString(` (`startup.go` three,
`wire_test.go` in `framingFields`).

Tests: subtest `"bounded primitives and full consumption"` of
`TestServerProtocolFraming` moves to `input_test.go` as `TestPGSessionInput`; subtest
`"cstring exact consumption"` of `TestServerFraming` moves as
`TestPGSessionConsumeString`.

Verify: gate; unit set; `make check RUN='^(TestReferenceServerExtendedMalformed|TestReferenceServerFunctionCall|TestReferenceServerProtocol)$'`.

### Step 03: `TextCodec` and diagnostic bodies (about 330 lines)

Files: new `internal/pgsession/codec.go`, `codec_test.go`; `pgserver/wire.go`,
`client_encoding.go`, `dispatch.go`, `query.go`, `query_output.go`,
`protocol_extended.go`, `startup.go`, `reference_forward.go`,
`client_encoding_test.go`, `client_encoding_notice_test.go`, `xml_output_test.go`,
`wire_test.go`, `reference_test.go`, `query_diff_test.go`.

`codec.go` contains:

```go
const MaxMessageLength = (1 << 30) - 2

type TextCodec struct {
	encoding sqltype.ClientEncodingPolicy
	ready    bool
}

// Temporary exports, unexported in step 16.
func (c *TextCodec) Set(policy sqltype.ClientEncodingPolicy) { c.encoding, c.ready = policy, true }
func (c *TextCodec) Encoding() sqltype.ClientEncodingPolicy    { return c.encoding }
func (c *TextCodec) AppendText(ctx context.Context, body []byte, value string) ([]byte, error)

func (c *TextCodec) ErrorBody(err error, fatal bool) ([]byte, error)
func (c *TextCodec) NoticeBody(d sqlstate.Error) ([]byte, error)
func (c *TextCodec) encodedDiagnostic(d sqlstate.Error, severity string) ([]byte, error)
func replacementDiagnostic(err error, previous sqlstate.Error) sqlstate.Error
func asciiDiagnostic(d sqlstate.Error, severity string) []byte
type diagnosticField struct{ tag byte; value string }
func diagnosticFields(d sqlstate.Error, defaultSeverity string) [18]diagnosticField
func DiagnosticBody(d sqlstate.Error, defaultSeverity string) []byte
func appendInt16(body []byte, value int16) []byte
func appendInt32(body []byte, value uint32) []byte
func appendString(body []byte, value string) []byte
```

- `AppendText` is `(*wireCodec).appendText` from `client_encoding.go`;
  `encodedDiagnostic` and `asciiDiagnostic` come from the same file;
  `replacementDiagnostic`, `diagnosticField`, `diagnosticFields` and `diagnosticBody`
  (renamed `DiagnosticBody`) come from `wire.go`. In all of them `w.encodingReady`
  becomes `c.ready`, `w.encoding` becomes `c.encoding`, `maxMessageLength` becomes
  `MaxMessageLength`, `diagnostic(` is already `Diagnostic(`.
- The three `append*` helpers are copied (pgserver keeps its own; about 110 call
  sites there are not worth rewriting).
- `ErrorBody` is the body of `(*wireCodec).errorResponse` with each
  `return w.send('E', X)` replaced by `return X, nil`, and the nil-diagnostic case
  returning `nil, errors.New("cannot send a nil SQL error")`. Keep its comments.
- `NoticeBody` is `return c.encodedDiagnostic(d, "NOTICE")`.

pgserver after the step:

```go
// wire.go: wireCodec loses encoding and encodingReady and gains
	text *pgsession.TextCodec // set by newWire; replaced by the session's codec after start
// newWire: &wireCodec{reader: ..., writer: ..., text: new(pgsession.TextCodec)}
// const maxMessageLength = pgsession.MaxMessageLength

func (w *wireCodec) errorResponse(err error, fatal bool) error {
	body, e := w.text.ErrorBody(err, fatal)
	if e != nil {
		return e
	}
	return w.send('E', body)
}

func (w *wireCodec) notice(d sqlstate.Error) error {
	body, err := w.text.NoticeBody(d)
	if err != nil {
		return err
	}
	return w.send('N', body)
}

// client_encoding.go
func (c *client) refreshEncoding() {
	if c.session != nil {
		c.wire.text.Set(c.session.Settings.ClientEncoding())
	}
}
```

Call sites: `c.wire.appendText(` becomes `c.wire.text.AppendText(` (`dispatch.go` two
in `drainNotifications`, `query_output.go` one, `query.go` one,
`protocol_extended.go` one, `startup.go` two in `sendSettings`); `c.wire.encoding`
as a value becomes `c.wire.text.Encoding()` (`dispatch.go` two,
`protocol_extended.go` one, `client_encoding_notice_test.go` one); `diagnosticBody(`
becomes `pgsession.DiagnosticBody(` (`reference_forward.go`, `wire_test.go`,
`reference_test.go`, `query_diff_test.go`); in `xml_output_test.go`
`client.wire.encoding, client.wire.encodingReady = policy, true` becomes
`client.wire.text.Set(policy)`.

Test: `TestClientEncodingStream` moves from `client_encoding_test.go` to
`codec_test.go` as `TestPGSessionCodec`. `conn := framingInput(nil); w := newWire(conn)`
becomes `var w TextCodec`; `w.encoding, w.encodingReady = policy, true` becomes
`w.Set(policy)`; in the final loop `w.errorResponse(&d, fatal)`, the flush and the
frame check become `raw, err := w.ErrorBody(&d, fatal)` (fail on error) and the field
map is read from `raw` instead of `raw[5:]`. Copy the helper `framingFields` from
`wire_test.go` into `codec_test.go` under the name `diagnosticFieldMap`.

Do not touch `wireCodec.send`, `readMessage`, or pgserver's `append*` helpers.

Verify: gate; unit set; `make check RUN='^(TestReferenceServerFrontendEncoding|TestReferenceServerLogs|TestReferenceServerNegotiation|TestReferenceServerAuthentication)$'`.

### Step 04: `Start` (about 130 lines)

Files: new `internal/pgsession/start.go`, `start_test.go`; `pgserver/database.go`,
`startup.go`; delete `pgserver/client_encoding_test.go`.

Move from `database.go` to `start.go`: the methods of `startupDefaults`, the type
`startupDefaultValues` with its method, `settingNames` (renamed `SettingNames`, with
the doc comment of section 2), and `(*database).startSession`, which becomes:

```go
func Start(ctx context.Context, cfg StartConfig) (*session.Session, *sqlstate.Error, error) {
	defaults := startupDefaults(cfg.Defaults)
	for name := range defaults {
		if _, ok := cfg.Known[name]; !ok {
			return nil, nil, &sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "42704", Message: fmt.Sprintf(`unrecognized configuration parameter "%s"`, name)}
		}
	}
	principal, err := cfg.Engine.ResolveLogin(cfg.Role)
	if err != nil {
		return nil, nil, err
	}
	admission, err := cfg.Engine.AdmitDatabase(ctx, principal, cfg.ID, cfg.Database)
	if err != nil {
		return nil, nil, err
	}
	defer admission.Release()
	values := startupDefaultValues{startupDefaults: defaults}
	var notice *sqlstate.Error
	if raw, present := defaults["application_name"]; present {
		values.applicationName, notice, err = settings.StartupApplicationName(raw)
		if err != nil {
			return nil, notice, err
		}
		values.applicationSet = true
	}
	s, err := admission.Target().StartSession(ctx, admission, session.SessionStart{ID: cfg.ID, Defaults: values})
	return s, notice, err
}
```

`start.go` declares its own `type startupDefaults map[string]string` (unexported) and
`StartConfig` as in section 2.

pgserver after: `database.go` keeps `type startupDefaults map[string]string` without
methods (used by `startupOptions` and `startup_test.go`), and in `provision`
`Defaults: settingNames(d.settings)` becomes `Defaults: pgsession.SettingNames(d.settings)`.
In `startup`, the call `db.startSession(c.ctx, c.id, c.user, defaults)` becomes
`pgsession.Start(c.ctx, pgsession.StartConfig{Engine: db.engine, ID: ids.SessionID(c.id), Role: c.user, Defaults: defaults, Known: db.settings})`
(`startup.go` gains the `ids` import; `database.go` loses `ids`, `settings`,
`sqlstate`).

Test: `TestClientEncodingStartupNameCapture` moves to `start_test.go` as
`TestPGSessionStartNameCapture`; the two lines building `database{...}` and calling
`d.startSession` become
`s, notice, err := Start(context.Background(), StartConfig{Engine: e, ID: 1, Role: "postgres", Defaults: map[string]string{"application_name": raw, "client_encoding": "LATIN1"}, Known: map[string]struct{}{"application_name": {}, "client_encoding": {}}})`.
`client_encoding_test.go` is then empty: delete it.

Do not touch `provision` beyond the one expression, `bootstrapExecute`, `newDatabases`.

Verify: gate; unit set; `make check RUN='^(TestReferenceServerNegotiation|TestReferenceServerAuthentication|TestReferenceServerClients|TestReferenceServerFrontendEncoding|TestReferenceEmbeddedServerClients)$'`.

### Step 05: `Session` skeleton and wiring (about 140 lines)

Files: new `internal/pgsession/session.go`; `pgserver/server.go`, `startup.go`,
`client_encoding.go`, `client_encoding_notice_test.go`,
`client_encoding_notice_stream_test.go`, `session_activity_test.go`,
`session_signal_writer_test.go`.

`session.go`:

```go
type Message struct {
	Tag  byte
	Body []byte
}

type Transport interface {
	Send(tag byte, body []byte) error
	Flush() error
}

type Observer interface{}

type Config struct {
	Session   *session.Session
	Context   context.Context
	Transport Transport
	Observer  Observer
}

type Session struct {
	engine    *core.Engine
	session   *session.Session
	cycle     *session.ProtocolCycle
	ctx       context.Context
	transport Transport
	observer  Observer
	codec     TextCodec
}

func New(cfg Config) *Session {
	s := &Session{engine: core.EngineOf(cfg.Session), session: cfg.Session, cycle: cfg.Session.NewProtocolCycle(),
		ctx: cfg.Context, transport: cfg.Transport, observer: cfg.Observer}
	s.RefreshEncoding()
	return s
}

func (s *Session) Codec() *TextCodec { return &s.codec }

// RefreshEncoding is a temporary export, unexported in step 16.
func (s *Session) RefreshEncoding() {
	if s.session != nil {
		s.codec.Set(s.session.Settings.ClientEncoding())
	}
}
```

Use the doc comments of section 2 for the final types; `Transport` and `Observer`
grow in later steps. Keep the `s.session != nil` guard: moved tests build a `Session`
without an engine session.

pgserver:

- `client` gains the field `ps *pgsession.Session`.
- Add to `server.go`:
  ```go
  func (c *client) Send(tag byte, body []byte) error { return c.wire.send(tag, body) }
  func (c *client) Flush() error                     { return c.wire.flush() }
  ```
- In `startup`, the call `c.refreshEncoding()` that follows session start becomes
  ```go
  c.ps = pgsession.New(pgsession.Config{Session: c.session, Context: c.ctx, Transport: c, Observer: c})
  c.wire.text = c.ps.Codec()
  ```
  (the application-name notice written just before it still uses the unready codec,
  so it stays unconverted as today).
- `refreshEncoding` becomes
  ```go
  func (c *client) refreshEncoding() {
  	if c.ps != nil {
  		c.ps.RefreshEncoding()
  		c.wire.text = c.ps.Codec()
  	}
  }
  ```
- Tests: add
  `c.ps = pgsession.New(pgsession.Config{Session: c.session, Context: c.ctx, Transport: c, Observer: c})`
  directly after the `client` literal in `TestClientEncodingNoticeOwnership`,
  `TestClientEncodingNoticeStreaming` and `signalWriterClient`, and in
  `TestSessionActivityWireTransport` directly after `c.session` is assigned.

Why it is safe: `(*session.Session).NewProtocolCycle` returns the existing cycle when
one exists, so `c.cycle` (still created in `run`) and the session's cycle are the same
object; `core.EngineOf(c.session)` is `c.db.engine` (`StartProtocolCommand` already
rejects any other engine); no command runs between the two creation points.

Verify: gate; unit set.

### Step 06: row, description and notice output (about 300 lines)

Files: new `internal/pgsession/output.go`, `transport_test.go`, `output_test.go`;
`pgserver/query_output.go`, `client_encoding.go`, `query.go`,
`protocol_extended.go`, `protocol_test.go`; `git mv pgserver/xml_output_test.go
internal/pgsession/xml_output_test.go`.

Move to `output.go` as methods of `*Session` (temporary exports, unexported in step
16):

| pgserver | pgsession |
|---|---|
| `(*client).descriptionBody` | `DescriptionBody(schema []catalog.Column, formats []int16) ([]byte, error)` |
| `(*client).writeDescription` | `WriteDescription(schema []catalog.Column, formats []int16) error` |
| `(*client).writeFields` | `WriteFields(fields []session.ProtocolField, buffer *[]byte) (logicalErr, transportErr error)` |
| `(*client).writeNotices` | `WriteNotices(op *session.Operation) (logical, transport error)` |
| `(*client).writeActiveNotices` | `WriteActiveNotices(scope *core.StatementScope) (logical, transport error)` |

Add the unexported helper:

```go
func (s *Session) notice(d sqlstate.Error) error {
	body, err := s.codec.NoticeBody(d)
	if err != nil {
		return err
	}
	return s.transport.Send('N', body)
}
```

| In moved code | Becomes |
|---|---|
| `c.refreshEncoding()` | `s.RefreshEncoding()` |
| `c.wire.text.AppendText(c.ctx,` | `s.codec.AppendText(s.ctx,` |
| `c.wire.send(` | `s.transport.Send(` |
| `c.wire.notice(` | `s.notice(` |
| `maxMessageLength` | `MaxMessageLength` |
| receiver calls `c.descriptionBody`, `c.writeNotices` | `s.DescriptionBody`, `s.WriteNotices` |

Call sites in pgserver: `c.writeNotices(` → `c.ps.WriteNotices(` (`query.go` two,
`protocol_extended.go` one); `c.writeDescription(` → `c.ps.WriteDescription(`
(`query.go`); `c.writeFields(` → `c.ps.WriteFields(` (`query.go`,
`protocol_extended.go`); `c.writeActiveNotices(` → `c.ps.WriteActiveNotices(`
(`query.go` one, `protocol_extended.go` two); `c.descriptionBody(` →
`c.ps.DescriptionBody(` (`protocol_extended.go` two). `writeSQLError` and
`writeContextError` stay in `query_output.go`. Direct `c.wire.notice(` calls in
`query.go`, `protocol_extended.go`, `protocol_copy.go` and `startup.go` stay.

`transport_test.go` (used by all later moved tests):

```go
type sentMessage struct {
	tag  byte
	body []byte
}

// testTransport records backend messages. A non-nil conn makes Send write the
// framed message to it instead (step 15).
type testTransport struct {
	sent     []sentMessage
	sendErr  error
	flushes  int
	fatals   int
	messages chan Message
	released chan []byte
	ended    chan error
}

func (t *testTransport) Send(tag byte, body []byte) error {
	if t.sendErr != nil {
		return t.sendErr
	}
	t.sent = append(t.sent, sentMessage{tag, bytes.Clone(body)})
	return nil
}
func (t *testTransport) Flush() error             { t.flushes++; return nil }
func (t *testTransport) BeginFatal() error        { t.fatals++; return nil }
func (t *testTransport) Messages() <-chan Message { return t.messages }
func (t *testTransport) Ended() <-chan error      { return t.ended }
func (t *testTransport) Release(m Message) {
	if t.released != nil {
		t.released <- m.Body
	}
}
```

Tests:

- `xml_output_test.go` (`TestXMLServerActiveOutput`, renamed
  `TestPGSessionXMLActiveOutput`): `client := &client{wire: newWire(conn)}` becomes
  `tr := &testTransport{}; s := &Session{transport: tr}`;
  `client.wire.text.Set(policy)` becomes `s.codec.Set(policy)`; `client.writeNotices`
  and `client.writeFields` become `s.WriteNotices` and `s.WriteFields`; where the test
  parsed frames from the connection's output it reads `tr.sent` in order (tag and
  body; the body is the frame without the 5-byte header). Copy any `wire_test.go`
  helper it needs.
- Subtest `"encoded fields preserve null empty and buffer ownership"` of
  `TestServerProtocolFraming` moves to `output_test.go` as `TestPGSessionWriteFields`:
  the client literal becomes `s := &Session{transport: tr}`; the framed packet
  comparison becomes `tr.sent[0].tag == 'D'` and `bytes.Equal(tr.sent[0].body, wantBody)`
  where `wantBody` is the expected packet without its 5-byte header; a write error
  injected with `conn.writeErr` becomes `tr.sendErr`.

Verify: gate; unit set; `make check RUN='^(TestReferenceServerQueries|TestReferenceServerExtendedMetadata|TestReferenceServerExtendedCodecs|TestReferenceServerDomainMetadata|TestReferenceServerPortalPresentation)$'`.

### Step 07: `WriteSQLError` and `Fatal` (about 260 lines)

Files: new `internal/pgsession/sqlerror.go`, `sqlerror_test.go`;
`internal/pgsession/session.go`; `pgserver/query_output.go` (deleted when empty),
`dispatch.go`, `query.go`, `protocol_extended.go`, `server.go`, `protocol_test.go`,
`session_signal_writer_test.go`.

Interfaces grow: `Transport` gains `BeginFatal() error`; `Observer` gains
`SQLError(ctx context.Context, context session.ProtocolContext, err error) error`.
`Session` gains `fatalSent bool`.

`sqlerror.go` (complete):

```go
func (s *Session) WriteSQLError(ctx context.Context, source string, err error) error {
	return s.WriteContextError(ctx, session.ProtocolContext{Source: source}, err)
}

// WriteContextError is a temporary export, unexported in step 16.
func (s *Session) WriteContextError(ctx context.Context, source session.ProtocolContext, err error) error {
	// An execution owner may already have enriched the typed diagnostic (COPY
	// line context, for example). Never replace that primary error with a
	// context cause just because cleanup also returned context.Canceled.
	var primary *sqlstate.Error
	if !errors.As(err, &primary) && errors.Is(err, context.Canceled) {
		if cause := context.Cause(ctx); cause == session.UserCancelCause {
			err = cause
		} else if cause != nil && Diagnostic(cause).Severity == "FATAL" {
			err = cause
		}
	}
	if Diagnostic(err).Severity == "FATAL" && s.session != nil {
		s.session.ReserveDisconnectCause(session.DisconnectFatal)
	}
	if Diagnostic(err).Severity == "FATAL" {
		if e := s.transport.BeginFatal(); e != nil {
			return e
		}
	}
	if s.observer != nil {
		if e := s.observer.SQLError(ctx, source, err); e != nil {
			return e
		}
	}
	body, e := s.codec.ErrorBody(err, false)
	if e != nil {
		return e
	}
	if e := s.transport.Send('E', body); e != nil {
		return e
	}
	if fatal := Diagnostic(err); fatal.Severity == "FATAL" {
		if e := s.transport.Flush(); e != nil {
			return e
		}
		s.fatalSent = true
		return fatal
	}
	return nil
}

func (s *Session) Fatal(err error) {
	if s.session != nil {
		s.session.ReserveDisconnectCause(session.DisconnectFatal)
	}
	if !s.fatalSent {
		if body, e := s.codec.ErrorBody(err, true); e == nil {
			_ = s.transport.Send('E', body)
		}
		_ = s.transport.Flush()
		s.fatalSent = true
	}
}

func (s *Session) FatalSent() bool { return s.fatalSent }
```

pgserver adds to `server.go`:

```go
func (c *client) BeginFatal() error {
	if c.socket == nil {
		return nil
	}
	c.interruptTerminalWrite()
	return c.socket.SetWriteDeadline(time.Now().Add(time.Second))
}

func (c *client) SQLError(ctx context.Context, source session.ProtocolContext, err error) error {
	if logErr := c.server.logs.sqlError(ctx, source.Source, err, logParameters(source.Parameters), "connection_id", c.id, "database", c.database, "user", c.user); logErr != nil {
		c.server.fail(logErr)
		return logErr
	}
	return nil
}
```

Delete `(*client).writeSQLError`, `(*client).writeContextError`, `(*client).fatal`
and the field `fatalSent`.

Call sites: `c.writeSQLError(` → `c.ps.WriteSQLError(` (`dispatch.go` six, `query.go`
three, `protocol_extended.go` four); `c.writeContextError(` →
`c.ps.WriteContextError(` (`protocol_extended.go` one, `session_signal_writer_test.go`
two: there use `c.ps.WriteSQLError(ctx, "", fatal)` when the context argument is the
empty `session.ProtocolContext{}`); `c.fatal(` → `c.ps.Fatal(` (`dispatch.go` four);
`c.fatalSent` → `c.ps.FatalSent()` (`dispatch.go` three reads).

One caller needs care: in `run`, the startup-failure path and the code before
`startup` succeeded may run while `c.ps` is nil. `c.fatal` is called there only after
`startup` returned `admitted == true` (peer-watch failure, read failure, unknown tag),
so `c.ps` is set; the startup failure itself uses `c.wire.errorResponse` directly and
does not change.

Tests:

- Subtest `"typed cancellation diagnostics preserve owned context"` of
  `TestServerProtocolCancellation` moves to `sqlerror_test.go` as
  `TestPGSessionSQLErrorCancellation`. Per case: `tr := &testTransport{}`, a
  recording observer `obs` (a test type with all `Observer` methods that stores the
  arguments of `SQLError`), `s := &Session{transport: tr, observer: obs}`,
  `err := s.WriteContextError(ctx, session.ProtocolContext{Source: "exact source"}, tc.input)`.
  Assertions: the same disposition checks with `s.fatalSent`; `tr.sent[0].tag == 'E'`
  and the field map of `tr.sent[0].body` (code, message, context, both severities) as
  before; `tr.fatals == 1` exactly for the fatal cases; instead of the JSON log
  record: `obs.context.Source == "exact source"`, `Diagnostic(obs.err).Code == tc.code`,
  `Diagnostic(obs.err).Message == tc.message`; the shared-cause mutation check stays.
- So the log record stays covered, add to `TestServerProtocolCancellation` in
  pgserver a subtest `"sql error observer writes the error log record"`: build
  `c := &client{server: &server{logs: logs}}` with `logs.sql` writing to a buffer,
  call `c.SQLError(context.Background(), session.ProtocolContext{Source: "exact source"}, &sqlstate.Error{Code: "57014", Message: "m"})`,
  expect nil, and assert the JSON record has `error == "57014"`, `errmsg == "m"`,
  `query == "exact source"`.

Verify: gate; unit set; `make check RACE=1 RUN='^(TestSessionSignalTerminalWriter|TestPGSession)'`;
`make check RUN='^(TestReferenceServerFatalSession|TestReferenceServerSignalBoundary|TestReferenceServerSignalDiagnostics|TestReferenceServerSignalSmoke|TestReferenceServerLogs|TestReferenceServerPreparedLogs)$'`.

### Step 08: ready boundary and notifications (about 240 lines)

Files: new `internal/pgsession/ready.go`; `internal/pgsession/session.go`;
`pgserver/startup.go`, `dispatch.go`, `query.go`, `protocol_extended.go`,
`server.go`, `client_encoding_notice_test.go`, `client_encoding_notice_stream_test.go`.

`Observer` gains `BeforeReady(ctx context.Context)`. `Session` gains
`lastSettings map[string]string`; `New` sets `lastSettings: make(map[string]string)`.

Move to `ready.go` as methods of `*Session`: the type `parameterValue` and
`settingsSnapshot`, `sendSettings` (both stay unexported), `sendReady` (renamed
`SendReady`), from `startup.go`; `readyBoundary` (renamed `ReadyBoundary`, temporary
export) and `drainNotifications` (renamed `DrainNotifications`) from `dispatch.go`.
Add:

```go
func (s *Session) ReportSettings() error {
	values, err := s.settingsSnapshot()
	if err != nil {
		return err
	}
	return s.sendSettings(values)
}
```

| In moved code | Becomes |
|---|---|
| `c.session` | `s.session` |
| `c.db.engine` | `s.engine` |
| `c.ctx` | `s.ctx` |
| `c.lastSettings` | `s.lastSettings` |
| `c.wire.text.AppendText(` | `s.codec.AppendText(` |
| `c.wire.send(` / `c.wire.flush()` | `s.transport.Send(` / `s.transport.Flush()` |
| `c.refreshEncoding()` | `s.RefreshEncoding()` |
| `c.ps.Fatal(` | `s.Fatal(` |
| `if c.reference != nil { c.reference.await(c.ctx) }` | `if s.observer != nil { s.observer.BeforeReady(s.ctx) }` |
| `pgsession.Diagnostic(` | `Diagnostic(` |
| `appendInt32(` | unchanged (pgsession has its own copy) |

pgserver: add `func (c *client) BeforeReady(ctx context.Context) { if c.reference != nil { c.reference.await(ctx) } }`.
In `startup`, the `settingsSnapshot`/`sendSettings` pair becomes
`if err := c.ps.ReportSettings(); err != nil { return false, err }` and
`c.sendReady()` becomes `c.ps.SendReady()`. `c.readyBoundary()` →
`c.ps.ReadyBoundary()` (`dispatch.go` two, `query.go` three, `protocol_extended.go`
two); `c.drainNotifications()` in `run` → `c.ps.DrainNotifications()`. Remove the
field `lastSettings`, its initialisation in `newClient`, and the
`lastSettings: make(map[string]string)` element of the two notice-test literals.

Verify: gate; unit set; `make check RUN='^(TestReferenceServerParameterStatusBoundaries|TestReferenceServerNotifications|TestReferenceServerNotificationsLifetime|TestReferenceServerNotificationsOrdering|TestReferenceServerTransactions|TestReferenceServerDualQueryLog|TestReferenceEmbeddedServerNotifications)$'`.

### Step 09: command rejection and fastpath (about 120 lines)

Files: new `internal/pgsession/function.go`; `pgserver/dispatch.go`, `query.go`,
`protocol_extended.go`, `client_encoding_notice_test.go`.

Move as methods of `*Session` (temporary exports): `(*client).rejectCommand` →
`RejectCommand(err error) error`; `(*client).rejectProtocolCommand` →
`RejectProtocolCommand(ctx context.Context, err error) error`;
`(*client).functionCommand` → `FunctionCall(ctx context.Context, input *Input) error`.

Substitutions: `c.session` → `s.session`; `c.db.engine` → `s.engine`; `c.cycle` →
`s.cycle`; `c.ctx` → `s.ctx` (`RejectCommand` keeps acquiring on the physical context,
as today); `c.ps.X(` → `s.X(`; `c.refreshEncoding()` → `s.RefreshEncoding()`;
`c.wire.notice(` → `s.notice(`; `c.wire.send(` → `s.transport.Send(`;
`*pgsession.Input` → `*Input`.

Call sites: `c.rejectCommand(` → `c.ps.RejectCommand(` (`dispatch.go` in the `'Q'`
case, `query.go`); `c.rejectProtocolCommand(` → `c.ps.RejectProtocolCommand(`
(`dispatch.go`, `'H'` case); `c.functionCommand(` → `c.ps.FunctionCall(`
(`dispatch.go` `'F'` case, `client_encoding_notice_test.go`).

Verify: gate; unit set; `make check RUN='^(TestReferenceServerFunctionCall|TestReferenceServerFunctionCallReceivePrecedence|TestReferenceServerExtendedMalformed)$'`.

### Step 10: `frontendMessage` becomes `pgsession.Message` (about 90 lines)

Files: `pgserver/wire.go`, `server.go`, `dispatch.go`, `protocol_extended.go`,
`protocol_copy.go`, `startup.go`, `query_log.go`, `reference_forward.go`, and the
pgserver tests that build or read frontend messages.

In `wire.go` replace the struct by `type frontendMessage = pgsession.Message`. Then
rename the fields on frontend-message values only: `.tag` → `.Tag`, `.body` → `.Body`,
and literals `frontendMessage{tag: X, body: Y}` → `frontendMessage{Tag: X, Body: Y}`.
The compiler is the check in both directions: a missed rename and a rename on another
struct (`queryStep`, capture and reference records keep their lower-case fields) both
fail to compile. No behavior changes.

Verify: gate; unit set.

### Step 11: COPY stream (about 210 production and 330 test lines; needs a stronger model)

The step is mechanical but large, and production code and its tests must move in one
commit because the tests build `protocolCopy` literals.

Files: `git mv pgserver/protocol_copy.go internal/pgsession/copy.go`; new
`internal/pgsession/copy_test.go`; `internal/pgsession/session.go`;
`pgserver/server.go`, `query.go`, `protocol_extended.go`, `protocol_test.go`,
`client_encoding_notice_test.go`, `client_encoding_notice_stream_test.go`.

Interfaces grow: `Transport` gains `Messages() <-chan Message`, `Release(m Message)`,
`Ended() <-chan error`; `Observer` gains `CopyInput(m Message)`.

`Session` gains `state protocolState`, `copyStream ProtocolCopy`,
`copyHooks core.CopyProtocolHooks`, `copyDrain [8192]byte`. Copy the type
`protocolState` and its five constants from `dispatch.go` into `session.go`
(pgserver keeps its own until step 13). `New` binds the hooks:

```go
s.copyStream.session = s
s.copyHooks = core.CopyProtocolHooks{Start: s.copyStream.start, FinishInput: s.copyStream.finishInput, Notices: s.copyStream.notices}
```

| In `copy.go` | Becomes |
|---|---|
| `package pgserver` | `package pgsession` |
| type `protocolCopy` | `ProtocolCopy` (temporary export) |
| field `client *client` | `session *Session` |
| field `current frontendMessage` | `current Message` |
| `p.client.releaseMessage(p.current)` | `p.session.transport.Release(p.current)` |
| `p.client.refreshEncoding()` | `p.session.RefreshEncoding()` |
| `p.client.wire.notice(` | `p.session.notice(` |
| `p.client.wire.send(` / `p.client.wire.flush()` | `p.session.transport.Send(` / `p.session.transport.Flush()` |
| `p.client.protocolState = X` | `p.session.state = X` |
| `p.client.ctx` | `p.session.ctx` |
| `p.client.sessionEnded` | `p.session.transport.Ended()` |
| `p.client.messages` | `p.session.transport.Messages()` |
| `p.client.capture.copyInput(m)` and the `reference.forward(m)` block after it | `if o := p.session.observer; o != nil { o.CopyInput(m) }` |
| `p.client.session.Settings` | `p.session.session.Settings` |
| `p.client.copyDrain` | `p.session.copyDrain` |
| `var m frontendMessage` | `var m Message` |
| `pgsession.Input`, `pgsession.ProtocolError` | `Input`, `ProtocolError` |

Temporary exports used by pgserver until steps 12 and 13 (add to `copy.go`):

```go
func (s *Session) Copy() *ProtocolCopy                 { return &s.copyStream }
func (s *Session) CopyHooks() core.CopyProtocolHooks   { return s.copyHooks }
func (p *ProtocolCopy) Begin(ctx context.Context)      { p.ctx = ctx }
func (p *ProtocolCopy) CloseCommand()                  { p.closeCommand() }
func (p *ProtocolCopy) ReleaseFrame()                  { p.release() }
func (p *ProtocolCopy) TransportErr() error            { return p.transportErr }
func (p *ProtocolCopy) Complete() error                { return p.complete() }
```

pgserver adds to `server.go`:

```go
func (c *client) Messages() <-chan pgsession.Message { return c.messages }
func (c *client) Release(m pgsession.Message)        { c.releaseMessage(m) }
func (c *client) Ended() <-chan error                { return c.sessionEnded }
func (c *client) CopyInput(m pgsession.Message) {
	c.capture.copyInput(m)
	if c.reference != nil {
		c.reference.forward(m)
	}
}
```

Call sites in `executeSimple` (`query.go`) and `extendedCommand`
(`protocol_extended.go`): `stream := &c.copyStream` → `stream := c.ps.Copy()`;
`stream = &c.copyStream` likewise; `stream.ctx = op.Context()` →
`stream.Begin(op.Context())`; `stream.closeCommand()` → `stream.CloseCommand()`;
`stream.release()` → `stream.ReleaseFrame()`; `stream.transportErr` →
`stream.TransportErr()`; `stream.complete()` → `stream.Complete()`; `c.copyHooks` →
`c.ps.CopyHooks()`; the variable type `*protocolCopy` → `*pgsession.ProtocolCopy`.
Remove the fields `copyStream`, `copyHooks`, `copyDrain` and their two initialising
lines in `newClient`, and the two `c.copyStream.client = c` / `c.copyHooks = ...`
lines in each notice test.

Why the state write is safe: `protocolCopyIn` and `protocolCopyOut` are never read
(invariant 8), and pgserver's own `protocolState` stays authoritative for recovery
until step 13.

Tests moving from `TestServerProtocolFraming` and `TestServerProtocolCancellation`
(`protocol_test.go`) to `copy_test.go`, each as its own `TestPGSessionCopy...`
function: `"connection COPY hooks reuse actual stream state"`,
`"copy frame bytes defer only registered user cancel"`,
`"physical close interrupts borrowed copy data"`,
`"copy logical cancel wakes then awaits physical frame"`,
`"later fatal interrupts copy wait after user cancel"`. Conversion:

- `c := &client{ctx: X, messages: M, sessionEnded: T, freeBuffers: F}` and
  `protocolCopy{client: c, ctx: ctx}` become
  `tr := &testTransport{messages: M, ended: T, released: F}`,
  `s := &Session{ctx: X, transport: tr}` and `ProtocolCopy{session: s, ctx: ctx}`;
- where the first subtest used a `client` built by `newClient` with its bound hooks,
  build `s` the same way and set `s.copyStream.session = s` and `s.copyHooks` as
  `New` does;
- `<-c.freeBuffers` becomes `<-tr.released`; channel element type
  `frontendMessage` becomes `Message`;
- framed-packet comparisons on the connection output become `(tag, body)` comparisons
  on `tr.sent`;
- copy the constant `testLiveness` and any helper the subtests use.

Known interim effect: the `simple` and `extended` routes of
`TestClientEncodingNoticeOwnership` used hooks without `Notices`; from this step
until step 15 they run with the session's hooks (the streamed-notice path). They are
expected to pass because the assertions hold on both paths. If one fails, that is a
real difference: stop and report (L8), do not adapt the test.

Verify: gate; unit set; `make check RACE=1 RUN='^TestPGSession'`;
`make check RUN='^TestReferenceServerCopy'`;
`make check RUN='^(TestReferenceServerPreparedCopy|TestReferenceServerPreparedCopyQuery|TestReferenceEmbeddedServerCopy|TestReferenceServerQueryLog)$'`.

### Step 12: simple query (about 240 lines)

Files: `git mv pgserver/query.go internal/pgsession/simple.go`;
`pgserver/dispatch.go`, `client_encoding_notice_test.go`,
`client_encoding_notice_stream_test.go`, `session_activity_test.go`.

`(*client).executeSimple` becomes
`func (s *Session) SimpleQuery(ctx context.Context, source string) (transportErr error)`
(temporary export).

| In moved code | Becomes |
|---|---|
| `c.session` | `s.session` |
| `c.db.engine` | `s.engine` |
| `c.ctx` | `s.ctx` |
| `c.ps.X(` (any method) | `s.X(` |
| `c.ps.Copy()` | `&s.copyStream` |
| `stream.Begin(X)`, `CloseCommand()`, `ReleaseFrame()`, `TransportErr()`, `Complete()` | `stream.ctx = X`, `closeCommand()`, `release()`, `transportErr`, `complete()` |
| `c.ps.CopyHooks()` | `s.copyHooks` |
| `c.refreshEncoding()` | `s.RefreshEncoding()` |
| `c.wire.notice(` | `s.notice(` |
| `c.wire.text.AppendText(` | `s.codec.AppendText(` |
| `c.wire.send(` | `s.transport.Send(` |
| `c.protocolState = protocolNormal` | `s.state = protocolNormal` |

The state write is safe: a Query never runs while pgserver's flag is
`protocolRecovery`, and only that value is read.

Call sites: `c.executeSimple(` → `c.ps.SimpleQuery(` (`dispatch.go` one, the three
tests).

Verify: gate; unit set; `make check RUN='^(TestReferenceServerQueries|TestReferenceServerSimplePlanning|TestReferenceServerSimpleExecuteTiming|TestReferenceServerMixedCycles|TestReferenceServerTransactions|TestReferenceServerOutputCancellation|TestReferenceServerCancellation|TestReferenceServerCopyRoundTrip)$'`.

### Step 13: extended commands and recovery state (about 310 lines)

Files: `git mv pgserver/protocol_extended.go internal/pgsession/extended.go` (after
step 09 the file holds only `extendedCommand`); `internal/pgsession/session.go`;
`pgserver/server.go`, `dispatch.go`, the two notice tests, `session_activity_test.go`.

`Observer` gains `Step(tag, target byte, name string, context session.ProtocolContext)`.
`(*client).extendedCommand` becomes
`func (s *Session) Extended(ctx context.Context, message Message) (transportErr error)`
(temporary export). Substitutions as in step 12, plus:

| In moved code | Becomes |
|---|---|
| `c.releaseMessage(message)` | `s.transport.Release(message)` |
| `c.capture.detail(message.Tag, stepTarget, stepName, provenance)` | `if s.observer != nil { s.observer.Step(message.Tag, stepTarget, stepName, provenance) }` |
| `c.protocolState = X` | `s.state = X` |
| `c.cycle` | `s.cycle` |
| `c.ps.WriteContextError(` | `s.WriteContextError(` |
| `pgsession.Input{...}`, `pgsession.ProtocolError(` | `Input{...}`, `ProtocolError(` |
| `core.ProtocolStatement` etc. | unchanged |

Add to `session.go`:

```go
func (s *Session) Recovering() bool { return s.state == protocolRecovery }

// SetRecovery is a temporary export, removed in step 16.
func (s *Session) SetRecovery(on bool) {
	if on {
		s.state = protocolRecovery
	} else {
		s.state = protocolNormal
	}
}
```

pgserver:

- add `func (c *client) Step(tag, target byte, name string, context session.ProtocolContext) { c.capture.detail(tag, target, name, context) }`;
- in `run`: the check `c.protocolState == protocolRecovery && m.Tag != 'S'` becomes
  `c.ps.Recovering() && m.Tag != 'S'`; in the `'P','B','D','E','C'` case
  `c.protocolState = protocolExtended` becomes `c.ps.SetRecovery(false)` and
  `c.extendedCommand(ctx, m)` becomes `c.ps.Extended(ctx, m)`; in the `'S'` case
  `c.protocolState = protocolNormal` becomes `c.ps.SetRecovery(false)`; in the `'H'`
  case `c.protocolState = protocolRecovery` becomes `c.ps.SetRecovery(true)`;
- delete the type `protocolState`, its constants and the field `protocolState`;
- **keep** the field `cycle` and its assignment in `run`: the `'S'` case still uses
  `c.cycle` until step 14;
- tests: `c.extendedCommand(` → `c.ps.Extended(`.

Verify: gate; unit set; `make check RUN='^TestReferenceServerExtended'`;
`make check RUN='^TestReferenceServerPortal'`;
`make check RUN='^(TestReferenceServerPipelineLifetime|TestReferenceServerFailedPortalIdentity|TestReferenceServerFailedUnnamedParse|TestReferenceServerAbortedDescribe|TestReferenceServerParsePrecedence|TestReferenceServerReceivePrecedence|TestReferenceServerUnnamedCreationFastPaths|TestReferenceServerUnnamedPortalQueryLifecycle|TestReferenceServerNestedPreparedExecute|TestReferenceServerPreparedLogs|TestReferenceServerQueryLog)$'`.

### Step 14: `Dispatch` (about 200 lines)

Files: new `internal/pgsession/dispatch.go`; `pgserver/dispatch.go`, `server.go`,
the two notice tests, `session_activity_test.go`.

`dispatch.go` in pgsession (complete):

```go
var ErrUnknownMessage = errors.New("pgsession: unknown frontend message type")

func (s *Session) Dispatch(ctx context.Context, m Message) (err error) {
	if s.state == protocolRecovery && m.Tag != 'S' {
		s.transport.Release(m)
		return nil
	}
	s.RefreshEncoding()
	switch m.Tag {
	case 'Q':
		input := Input{Body: m.Body, Encoding: s.codec.Encoding(), Ctx: ctx}
		source, e := input.String()
		if e == nil {
			e = input.End()
		}
		if s.observer != nil {
			s.observer.Step(m.Tag, 0, "", session.ProtocolContext{Source: source})
		}
		s.transport.Release(m)
		if e != nil {
			e = s.RejectCommand(e)
			err = s.WriteSQLError(ctx, source, e)
			if err == nil {
				err = s.ReadyBoundary()
			}
		} else {
			err = s.SimpleQuery(ctx, source)
		}
	case 'P', 'B', 'D', 'E', 'C':
		s.state = protocolExtended
		err = s.Extended(ctx, m)
	case 'S':
		s.state = protocolNormal
		input := Input{Body: m.Body}
		e := input.End()
		op, _, admitErr := s.session.AcquireCommand(ctx)
		if admitErr != nil {
			e = admitErr
		} else {
			if e != nil {
				scope, se := s.engine.StartProtocolCommand(op, s.cycle)
				if se == nil {
					e = scope.Finish(e)
				} else {
					e = se
				}
			}
			syncErr := s.engine.SyncProtocolCycle(op, s.cycle)
			e = op.CommandError(errors.Join(e, syncErr))
			op.Done()
		}
		s.transport.Release(m)
		if e != nil {
			err = s.WriteSQLError(ctx, "", e)
		}
		if err == nil {
			err = s.ReadyBoundary()
		}
	case 'H':
		input := Input{Body: m.Body}
		e := input.End()
		s.transport.Release(m)
		if e != nil {
			e = s.RejectProtocolCommand(ctx, e)
			err = s.WriteSQLError(ctx, "", e)
			s.state = protocolRecovery
		}
		if err == nil {
			err = s.transport.Flush()
		}
	case 'F':
		err = s.FunctionCall(ctx, &Input{Body: m.Body, Encoding: s.codec.Encoding(), Ctx: ctx})
		s.transport.Release(m)
	case 'd', 'c', 'f':
		s.transport.Release(m)
	default:
		s.transport.Release(m)
		return ErrUnknownMessage
	}
	return err
}
```

Before writing it, compare case by case with the switch in `run`: the body above must
equal the current switch after the substitutions of the earlier steps. If the switch
differs (another part changed it), port the difference and report it.

pgserver `run`: the block from the recovery check through the end of the switch
(ending with the `done()` call that follows the switch) becomes

```go
		if c.ps.Recovering() && m.Tag != 'S' {
			c.releaseMessage(m)
			continue
		}
		ctx, done := c.beginCommand()
		err = c.ps.Dispatch(ctx, m)
		if errors.Is(err, pgsession.ErrUnknownMessage) {
			c.ps.Fatal(framingError("08P01", "invalid frontend message type"))
			done()
			return
		}
		done()
```

Everything before it in the loop (notification drain, select, the `c.ctx.Err()`
check, the `'X'` case, reference forwarding and `c.capture.begin`) and everything
after it (`emitQuery`, the FATAL handling) stays. The pre-check keeps a skipped
message from registering a cancel slot, as today. The unknown-tag branch is
unreachable (`readMessage` rejects such tags with `knownFrontendTag`) but keeps its
order of effects.

Also: delete the field `cycle` of `client`, its assignment in `run`, the
`cycle: s.NewProtocolCycle()` element of the two notice-test literals and the line
`c.cycle = c.session.NewProtocolCycle()` in `TestSessionActivityWireTransport`.
`(*client).refreshEncoding` stays (the tests still call it) until step 15.

Verify: gate; unit set; `make check RUN='^(TestReferenceServerProtocol|TestReferenceServerMixedCycles|TestReferenceServerExtendedPipeline|TestReferenceServerCancellation|TestReferenceServerVersionCancellation)$'`;
`make check RUN='^TestReferenceEmbeddedServer'`.

### Step 15: move the orchestration tests (about 420 lines; needs a stronger model)

Files: `git mv` of `pgserver/client_encoding_notice_test.go` and
`pgserver/client_encoding_notice_stream_test.go` to `internal/pgsession/`; new
`internal/pgsession/activity_test.go`; `internal/pgsession/transport_test.go`;
`pgserver/session_activity_test.go`, `pgserver/client_encoding.go`.

Move `TestClientEncodingNoticeOwnership`, `TestClientEncodingNoticeStreaming` and
`TestSessionActivityWireTransport`, renamed `TestPGSessionNoticeOwnership`,
`TestPGSessionNoticeStreaming` and `TestPGSessionActivityTransport`.
`TestSessionActivityWireIdentityCapacity` stays in pgserver. Then delete
`(*client).refreshEncoding` and `client_encoding.go` if it is empty.

The judgement call: the two notice tests observe a statement while its output write
is blocked on a real `net.Pipe`. Extend `testTransport` with

```go
	conn    net.Conn      // when set, Send writes tag, length word and body to it
	entered chan struct{} // closed by the first Send that reaches conn
	once    sync.Once
```

so that `Send` with a non-nil `conn` closes `entered` once and then writes the
5-byte header and the body with `conn.Write` (returning its error), and records
nothing. Each test builds its session with
`New(Config{Session: s, Context: ctx, Transport: tr})` where the old test built a
`client`; for the ownership test restore the original hooks after `New` with
`c.copyHooks = core.CopyProtocolHooks{Start: c.copyStream.start, FinishInput: c.copyStream.finishInput}`.
Calls map as: `c.executeSimple`/`c.ps.SimpleQuery` → `s.SimpleQuery`,
`c.ps.Extended` → `s.Extended`, `c.ps.FunctionCall` → `s.FunctionCall`,
`c.refreshEncoding()` → `s.RefreshEncoding()`. The activity test reads rows from the
recorded `'D'` bodies and compares the backend PID with the engine session's ID.

Every assertion must survive: engine locks free while the write is blocked,
`io.ErrClosedPipe` returned as the transport error, no operation lease left, rows not
published, the LATIN1 bytes of converted notices. If an assertion cannot be expressed
through the transport, stop and report it.

Verify: gate; `make check RUN='^TestPGSession'`; `make check RACE=1 RUN='^TestPGSession'`;
unit set.

### Step 16: unexport the temporary surface (about 120 lines)

Files: `internal/pgsession/*.go` only. pgserver must compile unchanged; if it does
not, a call site was missed in step 14 or 15: stop and report.

Rename (declarations and all uses inside the package): `RefreshEncoding` →
`refreshEncoding`; `DescriptionBody`, `WriteDescription`, `WriteFields`,
`WriteNotices`, `WriteActiveNotices`, `WriteContextError`, `ReadyBoundary`,
`RejectCommand`, `RejectProtocolCommand` → the same names with a lower-case first
letter; `FunctionCall` → `functionCommand`; `SimpleQuery` → `executeSimple`;
`Extended` → `extendedCommand`; type `ProtocolCopy` → `protocolCopy`;
`TextCodec.Set`, `Encoding`, `AppendText` → `set`, `policy`, `appendText`.
Delete: `Session.Copy`, `Session.CopyHooks`, `Session.SetRecovery`,
`ProtocolCopy.Begin`, `CloseCommand`, `ReleaseFrame`, `TransportErr`, `Complete`.

After the step `go doc ./internal/pgsession` lists exactly the identifiers of
section 2 (`WatchEnd`, `EndHooks` and `CommandSlot` arrive in steps 17 and 18).

Verify: gate; `make check RUN='^TestPGSession'`; unit set.

### Step 17: `WatchEnd` (about 90 lines)

Files: new `internal/pgsession/end.go`; `pgserver/server.go`.

```go
type EndHooks struct {
	Abandon   func()
	Terminate func(fatal *sqlstate.Error)
}

func WatchEnd(ctx context.Context, s *session.Session, hooks EndHooks) (<-chan error, <-chan struct{}) {
	events := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-s.EndRequested():
		case <-ctx.Done():
			if hooks.Abandon != nil {
				hooks.Abandon()
			}
			s.RequestEnd(session.InvalidConnection)
			return
		}
		if ctx.Err() != nil {
			return
		}
		fatal := *Diagnostic(s.Check(ctx))
		fatal.Severity, fatal.SeverityUnlocalized = "FATAL", "FATAL"
		if hooks.Terminate != nil {
			hooks.Terminate(&fatal)
		}
		select {
		case events <- &fatal:
		case <-ctx.Done():
		}
	}()
	return events, done
}
```

Move the comments of `watchSessionEnd` to the matching lines. pgserver:

```go
func (c *client) watchSessionEnd() <-chan error {
	events, done := pgsession.WatchEnd(c.ctx, c.session, pgsession.EndHooks{
		Abandon: func() {
			c.server.awaitCancellation()
			if c.readDone != nil {
				<-c.readDone
			}
		},
		Terminate: func(fatal *sqlstate.Error) {
			c.interruptTerminalWrite()
			c.server.mu.Lock()
			if c.commandCancel != nil {
				c.commandCancel(fatal)
			}
			c.server.mu.Unlock()
		},
	})
	c.sessionDone = done
	return events
}
```

The field `sessionDone` changes type to `<-chan struct{}`. pgserver tests stay
unchanged (`TestServerProtocolCancellation` subtest
`"physical watcher requests end before held lease drains"`,
`TestSessionSignalTerminalWriter`, `TestEmbeddedServerPipe`).

Verify: gate; `make check RACE=1 RUN='^(TestServerProtocolCancellation|TestSessionSignalTerminalWriter|TestEmbeddedServerPipe|TestEmbeddedServerLifecycle)$'`;
`make check RUN='^(TestReferenceServerFatalSession|TestReferenceServerSignalSmoke|TestReferenceServerCopyCancelTerminal|TestReferenceEmbeddedServerShutdown)$'`.

### Step 18: `CommandSlot` (about 90 lines)

Files: new `internal/pgsession/command.go`, `command_test.go`; `pgserver/server.go`,
`protocol_test.go`.

```go
type CommandSlot struct {
	mu     sync.Mutex
	cancel context.CancelCauseFunc
}

func (c *CommandSlot) Begin(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()
	return ctx, func() {
		c.mu.Lock()
		c.cancel = nil
		c.mu.Unlock()
		cancel(nil)
	}
}

func (c *CommandSlot) Cancel(cause error) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel == nil {
		return false
	}
	c.cancel(cause)
	return true
}
```

`TestPGSessionCommandSlot`: `Cancel` with no command returns false; after `Begin`,
`Cancel(session.UserCancelCause)` returns true and `context.Cause(ctx)` is that
cause; after the done function, `Cancel` returns false and the context is done; a
second `Begin` after done is cancellable.

pgserver: the field `commandCancel context.CancelCauseFunc` becomes
`commands pgsession.CommandSlot`; `beginCommand` becomes
`return c.commands.Begin(c.ctx)`; in `cancelCommand` (still under `server.mu`, after
the unchanged key check) the condition `c.commandCancel != nil` and the call become
one condition `c.commands.Cancel(session.UserCancelCause)`, and `reference =
c.reference` is assigned only when it is true; the `Terminate` hook of step 17
becomes `c.interruptTerminalWrite(); c.commands.Cancel(fatal)`; delete
`c.commandCancel = nil` in `finish`. Lock order: `server.mu` before the slot's mutex;
the slot's mutex is a leaf.

Equivalence: before, a CancelRequest hit whichever command was registered when
`server.mu` was taken; after, whichever is registered when the slot's mutex is taken
inside the same `server.mu` section.

Test edits in `TestServerProtocolCancellation` (the part before its first subtest):
each `X, cancel := context.WithCancelCause(context.Background())` followed by
`c.commandCancel = cancel` becomes `X, doneX := c.commands.Begin(context.Background())`;
each `c.commandCancel = nil` becomes a call of the matching done function. Call every
done function exactly once and before the next `Begin`: a late call would clear the
newer command's slot.

Verify: gate; `make check RACE=1 RUN='^(TestServerProtocolCancellation|TestEmbeddedServerPipe|TestPGSession)'`;
`make check RUN='^(TestReferenceServerCancellation|TestReferenceServerVersionCancellation|TestReferenceServerCopyCancelInput|TestReferenceServerCopyCancellation|TestReferenceServerComparisonCancellation|TestReferenceEmbeddedServerCancellation)$'`.

### Step 19: documentation (about 60 lines)

Files: `internal/pgsession/README.md`, `pgserver/README.md`, `AGENTS.md`.

- Complete the pgsession README: responsibility and layering (imports engine
  packages, never pgserver or the root package); the single-goroutine rule; what the
  owner keeps (ending the session, the command context, the transport); borrowed
  buffers (`Send` bodies, `Message` bodies and `Release`); the error convention of
  `Dispatch` and `Transport.Send`; lock rules for `Transport` and `Observer` methods;
  `## Testing` with `make check RUN='^TestPGSession'` and the note to use `RACE=1`
  for COPY and end-watcher tests.
- pgserver README, "Protocol and execution boundaries": keep the bullets that are
  about the transport (reader buffers, serial writer, cancel keys, reference
  forwarding, monitoring); replace the bullets that now describe pgsession (engine
  operations per extended command, COPY streaming and notification timing, encoding
  conversion authority, retained SQL context) by one bullet linking to the pgsession
  README. In "Ownership and lock order" state that the per-command cancel slot has its
  own leaf mutex taken under `server.mu`.
- `AGENTS.md` package map: one line for `internal/pgsession`.

Verify: no code change; `make quickcheck`.

## 6. Step 20: ParameterStatus settings PostgreSQL 18 reports and pgserver omits

This is the only step that changes behavior, for pgserver and for every pgsession
consumer.

Observed with PostgreSQL 18.6 (`postgres:18.6-bookworm`, a raw protocol-3.0 startup
as user `postgres`): the server sends 15 ParameterStatus messages before
ReadyForQuery: `in_hot_standby`, `integer_datetimes`, `TimeZone`, `IntervalStyle`,
`search_path`, `is_superuser`, `application_name`, `default_transaction_read_only`,
`scram_iterations`, `DateStyle`, `standard_conforming_strings`,
`session_authorization`, `client_encoding`, `server_version`, `server_encoding`.
Changes are reported once per ReadyForQuery with the final value:
`SET default_transaction_read_only=on` reports `on`; `SET scram_iterations=5000`
reports `5000`; `RESET ALL` reports both back; `BEGIN; SET LOCAL
default_transaction_read_only=off; COMMIT` (value `on` before) reports nothing.

pgserver's `settingsSnapshot` reports 12 of them; missing are
`default_transaction_read_only`, `in_hot_standby` and `scram_iterations`. The engine
defines all three (`internal/engine/settings/builtins.go`, `preset.go`).

Change: in `settingsSnapshot` (`internal/pgsession/ready.go`) append the three names
to the list of names read with `s.session.Setting`, after `search_path`. The existing
diff in `sendSettings` gives the change reports. Order of ParameterStatus messages is
not significant to clients and differs from PostgreSQL already.

Test first, in `reference/sessions` (dual mode, per docs/TESTING.md): add to
`server_parameter_status_boundaries_test.go` (or a new file beside it if that file
would exceed 750 lines) a test `TestReferenceServerParameterStatusReported` that uses
the raw-wire helper of that file (`serverParameterBoundaryStart`, `serverWire`,
`serverFrame`) to assert: (1) the set of ParameterStatus names received before the
first ReadyForQuery contains the three names with values `off`, `off`, `4096`;
(2) `SET default_transaction_read_only = on` yields exactly one ParameterStatus for
that name with `on` before ReadyForQuery; (3) `SET scram_iterations = 5000` likewise;
(4) the `BEGIN; SET LOCAL ...; COMMIT` batch above yields none for that name. Record
PostgreSQL 18.6 results as expectations with `make check REFERENCE=1 RUN='^TestReferenceServerParameterStatusReported$'`,
watch quick mode fail, then make the change.

Other tests compare complete startup or boundary frames between stuntgres and the
reference (`TestReferenceServerNegotiation`, `TestReferenceServerComparisonWireMatrix`,
`TestReferenceServerDualQueryLog`, `TestReferenceServerQueryLog`). Run
`make check RUN='^TestReferenceServer'` and `make check RUN='^TestReferenceEmbeddedServer'`
after the change; a test that pinned the old set of 12 is corrected to the PostgreSQL
set, never loosened.

## 7. Risks

| Risk | Mitigation |
|---|---|
| Other agents rename engine entry points the moved files call (about 30 of them) | Moves are `git mv` with minimal edits; rebase before merge and rerun the gate and the step's selectors. |
| Interim coverage shift of `TestClientEncodingNoticeOwnership` between steps 11 and 15 | Expected to pass on both notice paths; a failure is reported, not patched. Step 15 restores the original hooks. |
| A temporary export leaks into the final API | Step 16 lists every one; its `go doc` check is the gate. |
| Steps 11 and 15 exceed what a small model does reliably | Flagged in their parts as needing a stronger model; step 11 is volume, step 15 needs the pipe-backed transport. |
| Reference server tests need `psql` and build a subprocess | Confirm `psql` on the implementer's host before starting. |
| Goroutine boundaries change in steps 07, 11, 15, 17, 18 | Their selectors are run with `RACE=1` as listed. |
| Steps 06–18 were not executed while writing this document | Each step's gate and selectors are the proof; a step whose substitution table does not compile is stopped and reported (L8), not improvised. |

Fallback if the temporary exports of steps 06–14 prove error-prone: re-receiver the
same methods onto one unexported struct inside pgserver first, then move that struct
in one commit. It gives up compiler enforcement of the boundary until the end, which
is why it is not the plan.
