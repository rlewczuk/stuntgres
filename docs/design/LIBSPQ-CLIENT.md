# LIBSPQ-CLIENT: the pure-Go libspq client package `bindings/libspq/spq`

Design for catalog part LIBSPQ-DESIGN-CLIENT. It specifies package
`stuntgres/bindings/libspq/spq`: the Go implementation of everything
[docs/LIBSPQ.md](../LIBSPQ.md) promises, below the C ABI. The C shim
(`docs/design/LIBSPQ-CABI.md`) only marshals arguments, waits on descriptors and
invokes callbacks. The package sits on `internal/pgsession`
(`docs/design/LIBSPQ-PGSESSION.md`).

Behavior of libpq described here was learned from the PostgreSQL 18 documentation and
by reading libpq at tag `REL_18_6`; nothing is copied from it. Message texts that
applications match on are quoted exactly because they are part of the observable
behavior.

Facts about the engine marked **(measured)** come from scratch tests run on the tree at
commit `c698641e5f` (see section 13).

## 1. Purpose, layering, rules

```
C shim (cgo, bindings/libspq)        marshals, waits in C, calls callbacks
        │ only the exported API of section 2
package spq (this document)          libpq client behavior, pure Go
        │ pgsession.Start/New/Dispatch/..., Transport
internal/pgsession                   protocol orchestration over one engine session
        │
internal/engine                      unchanged
```

Rules that every file of the package obeys:

- **R1 No cgo, no C types, no `os/signal`, no environment variables, no file access.**
  Imports: the standard library, `stuntgres/internal/pgsession`,
  `stuntgres/internal/engine` (constructors, Reset/Dump/Restore),
  `stuntgres/internal/engine/core`, `.../session`, `.../ids`,
  `stuntgres/internal/sqlstate`, `stuntgres/internal/sqltype` and
  `stuntgres/internal/sqltype/pgencoding18`. Nothing else from the engine tree.
- **R2 Every exported method is nonblocking**, with one exception: `PutCopyData` may
  wait for queue space (section 6.3). Blocking libpq functions are loops in the C shim:
  call, and when the call reports "would block", `poll()` the readiness descriptor and
  call again. The package has a blocking helper for its own tests only (section 11).
- **R3 One worker goroutine per connection** owns the `pgsession.Session` while the
  connection is open and is the only goroutine that calls `Dispatch`. Session start
  runs on the connecting goroutine before the worker exists; that is a sequential
  hand-over, not concurrent use.
- **R4 One thread at a time per connection** on the application side, as in libpq.
  Application-side state is not locked. The only state shared with other goroutines is
  the inbox (mutex `link.mu`), the current `*link` (atomic pointer) and the command
  slot (its own mutex).
- **R5 No panic leaves the package.** Every exported function and every goroutine
  recovers; a panic turns into a broken connection with SQLSTATE `XX000`
  (section 6.6).
- **R6 Reuse.** Encoding names and ids come from `sqltype.EncodingID` /
  `sqltype.EncodingName`; multibyte length and validity from
  `pgencoding18.Length` / `pgencoding18.Verify`; error-to-diagnostic conversion from
  `pgsession.Diagnostic`. The package adds no second table for any of these.
- **R7 One file per feature**, each owned by one part (section 12). A part never
  edits a file owned by another part. Where an early part needs a function that a
  later part owns, the early part creates the later part's file with a stub and the
  later part replaces that file.

## 2. Exported API

This is the complete surface the C shim may use. Unexported helpers are named in the
sections that define them.

### 2.1 Constants and enums (`doc.go`, `types.go`)

```go
// Version is the version of libspq itself (SPQXversion).
const Version = "0.1.0"

// LibVersion is the libpq release whose documented behavior the package follows
// (SPQlibVersion).
const LibVersion = 180006

// ConnStatus has libpq's ConnStatusType values. Only ConnectionOK and ConnectionBad
// are ever reported.
type ConnStatus int

const (
	ConnectionOK ConnStatus = iota
	ConnectionBad
	ConnectionStarted
	ConnectionMade
	ConnectionAwaitingResponse
	ConnectionAuthOK
	ConnectionSetEnv
	ConnectionSSLStartup
	ConnectionNeeded
	ConnectionCheckWritable
	ConnectionConsume
	ConnectionGSSStartup
	ConnectionCheckTarget
	ConnectionCheckStandby
	ConnectionAllocated
	ConnectionAuthenticating
)

// PollingStatus has libpq's PostgresPollingStatusType values.
type PollingStatus int

const (
	PollingFailed PollingStatus = iota
	PollingReading
	PollingWriting
	PollingOK
	PollingActive
)

// ExecStatus has libpq's ExecStatusType values.
type ExecStatus int

const (
	EmptyQuery ExecStatus = iota
	CommandOK
	TuplesOK
	CopyOut
	CopyIn
	BadResponse
	NonfatalError
	FatalError
	CopyBoth
	SingleTuple
	PipelineSync
	PipelineAborted
	TuplesChunk
)

// TransactionStatus has libpq's PGTransactionStatusType values.
type TransactionStatus int

const (
	TransIdle TransactionStatus = iota
	TransActive
	TransInTrans
	TransInError
	TransUnknown
)

// Verbosity has libpq's PGVerbosity values.
type Verbosity int

const (
	ErrorsTerse Verbosity = iota
	ErrorsDefault
	ErrorsVerbose
	ErrorsSQLState
)

// ContextVisibility has libpq's PGContextVisibility values.
type ContextVisibility int

const (
	ShowContextNever ContextVisibility = iota
	ShowContextErrors
	ShowContextAlways
)

// Ping has libpq's PGPing values.
type Ping int

const (
	PingOK Ping = iota
	PingReject
	PingNoResponse
	PingNoAttempt
)

// Error and notice field codes (libpq's PG_DIAG_* values).
const (
	DiagSeverity             byte = 'S'
	DiagSeverityNonlocalized byte = 'V'
	DiagSQLState             byte = 'C'
	DiagMessagePrimary       byte = 'M'
	DiagMessageDetail        byte = 'D'
	DiagMessageHint          byte = 'H'
	DiagStatementPosition    byte = 'P'
	DiagInternalPosition     byte = 'p'
	DiagInternalQuery        byte = 'q'
	DiagContext              byte = 'W'
	DiagSchemaName           byte = 's'
	DiagTableName            byte = 't'
	DiagColumnName           byte = 'c'
	DiagDatatypeName         byte = 'd'
	DiagConstraintName       byte = 'n'
	DiagSourceFile           byte = 'F'
	DiagSourceLine           byte = 'L'
	DiagSourceFunction       byte = 'R'
)

// MaxParams is the largest number of parameters of one command.
const MaxParams = 65535
```

### 2.2 Data types (`types.go`, `conninfo.go`, `result.go`)

```go
// FieldDesc describes one result column (libpq's PGresAttDesc).
type FieldDesc struct {
	Name     string
	TableOID uint32 // 0 when the column is not a table column
	ColumnID int    // attribute number, 0 when unknown
	Format   int    // 0 text, 1 binary
	TypeOID  uint32
	TypeSize int // pg_type.typlen; negative for variable width
	TypeMod  int
}

// ErrorField is one field of an ErrorResponse or NoticeResponse, in arrival order.
type ErrorField struct {
	Code  byte
	Value string
}

// Notification is one NotificationResponse (libpq's PGnotify).
type Notification struct {
	Channel    string // PGnotify.relname
	BackendPID int    // PGnotify.be_pid
	Payload    string // PGnotify.extra
}

// Param is one parameter of a parameterized command.
type Param struct {
	Value  []byte // ignored when Null
	Null   bool
	Binary bool // format code 1
	// LengthMissing reports that the caller passed a non-NULL binary value without a
	// length (paramLengths was NULL). The send then fails as libpq does.
	LengthMissing bool
}

// Option is one connection option (libpq's PQconninfoOption). A nil pointer is a C
// NULL.
type Option struct {
	Keyword  string
	EnvVar   *string // the variable name libpq reports; libspq never reads it
	Compiled *string // built-in default
	Val      *string // current value
	Label    string
	DispChar string
	DispSize int
}
```

`Result` is opaque; its methods are in section 2.6.

### 2.3 Connection strings (`conninfo.go`)

```go
// DefaultOptions returns every option with Val set to its default (SPQconndefaults).
func DefaultOptions() []Option

// ParseConninfo parses a keyword/value string or a URI without adding defaults
// (SPQconninfoParse). The error text is the libpq message without a trailing newline.
func ParseConninfo(conninfo string) ([]Option, error)

// ParseParams merges keyword/value arrays (SPQconnectdbParams semantics). Entries with
// an empty value are ignored. With expandDbname, the first "dbname" value that is
// itself a connection string is expanded.
func ParseParams(keywords, values []string, expandDbname bool) ([]Option, error)

// StartupSettings returns the session defaults a connection asks for: the settings in
// "options", then application_name (or fallback_application_name) and
// client_encoding. Names are lower-cased.
func StartupSettings(options []Option) (map[string]string, error)
```

### 2.4 Connection lifecycle and status

```go
// Conn is one libspq connection (SPGconn). The zero value is not usable; connections
// come from Connect and its variants, which never return nil.
type Conn struct{ /* unexported, section 6.1 */ }

func Connect(conninfo string) *Conn                                              // conn.go
func ConnectParams(keywords, values []string, expandDbname bool) *Conn           // connect.go
func ConnectStart(conninfo string) *Conn                                         // connect.go
func ConnectStartParams(keywords, values []string, expandDbname bool) *Conn      // connect.go
func (c *Conn) ConnectPoll() PollingStatus                                       // connect.go
func Ping(conninfo string) Ping                                                  // connect.go
func PingParams(keywords, values []string, expandDbname bool) Ping               // connect.go

// Close ends the session and releases the descriptors (SPQfinish). It cancels a
// running statement and does not wait for it to finish by itself. Idempotent.
func (c *Conn) Close()                                                           // conn.go
// Reset ends the session and opens a new one with the same options (SPQreset). The
// descriptors keep their numbers.
func (c *Conn) Reset()                                                           // connect.go

func (c *Conn) Status() ConnStatus                       // conn.go
func (c *Conn) TransactionStatus() TransactionStatus     // simple.go
func (c *Conn) ErrorMessage() string                     // conn.go; ends with "\n" unless empty
func (c *Conn) DB() string                               // conn.go
func (c *Conn) User() string                             // conn.go
func (c *Conn) Password() string                         // conn.go
func (c *Conn) Host() string                             // conn.go
func (c *Conn) Port() string                             // conn.go
func (c *Conn) Options() string                          // conn.go
func (c *Conn) Conninfo() []Option                       // conn.go (SPQconninfo)
func (c *Conn) ParameterStatus(name string) (string, bool) // simple.go
func (c *Conn) ProtocolVersion() int                     // conn.go: 3, or 0 when Bad
func (c *Conn) ServerVersion() int                       // simple.go: 0 when Bad
func (c *Conn) BackendPID() int                          // conn.go: 0 unless OK
func (c *Conn) Socket() int                              // conn.go: readiness descriptor or -1
func (c *Conn) CancelFD() int                            // cancel.go: descriptor C writes to, or -1
func (c *Conn) ClientEncoding() int                      // clientenc.go: -1 unless OK
func (c *Conn) SetNonblocking(on bool) int               // simple.go: always 0
func (c *Conn) IsNonblocking() bool                      // simple.go
func (c *Conn) Flush() int                               // simple.go: 0, or -1 when Bad
func (c *Conn) SetErrorVerbosity(v Verbosity) Verbosity  // simple.go: returns the old value
func (c *Conn) SetErrorContextVisibility(v ContextVisibility) ContextVisibility // simple.go
```

### 2.5 Commands, results, COPY, notices, cancel

```go
// Send functions queue one command and return at once (SPQsend...). A nil *string is a
// C NULL argument. They return false after appending a message to ErrorMessage.
func (c *Conn) SendQuery(query *string) bool                                                   // simple.go
func (c *Conn) SendQueryParams(command *string, nParams int, paramTypes []uint32,
	params []Param, resultFormat int) bool                                                     // extended.go
func (c *Conn) SendPrepare(stmtName, query *string, nParams int, paramTypes []uint32) bool    // extended.go
func (c *Conn) SendQueryPrepared(stmtName *string, nParams int, params []Param,
	resultFormat int) bool                                                                     // extended.go
func (c *Conn) SendDescribePrepared(stmt string) bool                                          // extended.go
func (c *Conn) SendDescribePortal(portal string) bool                                          // extended.go
func (c *Conn) SendClosePrepared(stmt string) bool                                             // extended.go
func (c *Conn) SendClosePortal(portal string) bool                                             // extended.go

// ConsumeInput moves everything that has arrived into the connection (SPQconsumeInput).
// It returns false when the connection is broken.
func (c *Conn) ConsumeInput() bool                                                             // simple.go
// IsBusy reports whether GetResult would report wouldBlock (SPQisBusy).
func (c *Conn) IsBusy() bool                                                                   // simple.go
// GetResult returns the next result, (nil, false) when the command is complete, or
// (nil, true) when the caller must wait for the readiness descriptor and call again.
func (c *Conn) GetResult() (res *Result, wouldBlock bool)                                      // simple.go

// ExecStart and ExecFinish are the two halves of libpq's blocking execution
// (SPQexec and its siblings): ExecStart, one Send function, ExecFinish. Each is called
// repeatedly while it reports wouldBlock, waiting for the descriptor in between.
func (c *Conn) ExecStart() (ready, wouldBlock bool)                                            // simple.go
func (c *Conn) ExecFinish() (res *Result, wouldBlock bool)                                     // simple.go

func (c *Conn) SetSingleRowMode() bool                                                         // rowmodes.go
func (c *Conn) SetChunkedRowsMode(chunkSize int) bool                                          // rowmodes.go

// PutCopyData returns 1 or -1. PutCopyEnd returns 1 or -1; a nil errormsg ends the
// COPY successfully. GetCopyData returns (row, len(row)) for a data row, (nil, 0) when
// no row is ready yet, (nil, -1) at the end of the data and (nil, -2) on failure.
func (c *Conn) PutCopyData(data []byte) int                                                    // copy.go
func (c *Conn) PutCopyEnd(errormsg *string) int                                                // copy.go
func (c *Conn) GetCopyData() (row []byte, rc int)                                              // copy.go

// TakeNotices returns the notices that calls on this connection consumed since the
// last TakeNotices, oldest first, as NonfatalError results, and forgets them. The C
// shim calls it after every call that can consume input and passes each to the notice
// receiver.
func (c *Conn) TakeNotices() []*Result                                                         // notices.go
// Notifies returns the next pending notification or nil (SPQnotifies).
func (c *Conn) Notifies() *Notification                                                        // notices.go

// RequestCancel asks for cancellation of the running command exactly as a byte written
// to CancelFD does. It may be called from any goroutine.
func (c *Conn) RequestCancel() bool                                                            // cancel.go
```

### 2.6 Results (`result.go`)

```go
type Result struct{ /* unexported */ }

func (r *Result) Status() ExecStatus
func (r *Result) ErrorMessage() string                    // "" for a non-error result
func (r *Result) ErrorField(code byte) (string, bool)
func (r *Result) ErrorFields() []ErrorField
func (r *Result) ErrorQuery() string                      // query text kept for LINE display
func (r *Result) VerboseErrorMessage(v Verbosity, cv ContextVisibility) string
func (r *Result) NTuples() int
func (r *Result) NFields() int
func (r *Result) Binary() bool
func (r *Result) Fields() []FieldDesc
func (r *Result) FieldNumber(name string) int             // -1 when absent
func (r *Result) ParamTypes() []uint32
func (r *Result) CmdStatus() string
func (r *Result) CmdTuples() (count string, ok bool)      // ok false: tag could not be interpreted
func (r *Result) OidValue() uint32
func (r *Result) ClientEncoding() int
// RowData returns the rows as the concatenation of their DataRow message bodies
// (section 5.3), NTuples of them. The C shim indexes it once.
func (r *Result) RowData() []byte
// Value returns one value. It is for Go tests; C reads RowData.
func (r *Result) Value(row, col int) (value []byte, null bool)

// ResStatus returns libpq's name for a status (SPQresStatus).
func ResStatus(status ExecStatus) string
```

### 2.7 Escaping and encoding (`escape.go`, `conn.go`, `encoding.go`, `clientenc.go`)

```go
// Pure functions (escape.go). encoding is a libpq encoding id.
func EscapeString(encoding int, stdStrings bool, from []byte) (to []byte, err error)
func EscapeLiteral(encoding int, s []byte) ([]byte, error)
func EscapeIdentifier(encoding int, s []byte) ([]byte, error)
func EscapeBytea(stdStrings, hex bool, from []byte) []byte
func UnescapeBytea(text []byte) []byte

// Connection-bound forms (conn.go). They set ErrorMessage on failure as libpq does.
func (c *Conn) EscapeStringConn(from []byte) (to []byte, ok bool)   // SPQescapeStringConn
func (c *Conn) EscapeLiteral(s []byte) ([]byte, bool)               // SPQescapeLiteral
func (c *Conn) EscapeIdentifier(s []byte) ([]byte, bool)            // SPQescapeIdentifier
func (c *Conn) EscapeByteaConn(from []byte) []byte                  // SPQescapeByteaConn

// Forms without a connection (conn.go). They use the settings of the connection that
// most recently reported them, as libpq does.
func EscapeStringStatic(from []byte) []byte                         // SPQescapeString
func EscapeByteaStatic(from []byte) []byte                          // SPQescapeBytea

func EncodingToChar(id int) string            // encoding.go; "" for an invalid id
func CharToEncoding(name string) int          // encoding.go; -1 when unknown
// ClientEncodingStatement returns the statement SPQsetClientEncoding executes, or
// false when the name is unusable.
func ClientEncodingStatement(encoding string) (string, bool)        // clientenc.go
```

### 2.8 Control (`control.go`, `dump.go`)

```go
// DropCluster forgets the cluster named by host and port (SPQXclusterDrop).
func DropCluster(host, port string) error
// ResetDatabase wipes the connected database (SPQXreset). False sets ErrorMessage.
func (c *Conn) ResetDatabase() bool
// Dump serializes the connected database (SPQXdump).
func (c *Conn) Dump() ([]byte, bool)
// Restore replaces the connected database with a dump (SPQXrestore).
func (c *Conn) Restore(dump []byte) bool
```

### 2.9 C function to spq call

`wait` means: poll the descriptor `Socket()` for readability (retry on `EINTR`), then
repeat the call. After every call marked ◆ the shim calls `TakeNotices` and delivers
them.

| C function (docs/LIBSPQ.md 3 and 6) | spq call(s) |
|---|---|
| `SPQconnectdb` | `Connect` ◆ |
| `SPQconnectdbParams` | `ConnectParams` ◆ |
| `SPQconnectStart`, `SPQconnectStartParams` | `ConnectStart`, `ConnectStartParams` ◆ |
| `SPQconnectPoll` | `ConnectPoll` |
| `SPQfinish` | `Close` |
| `SPQreset` | `Reset` ◆ |
| `SPQping`, `SPQpingParams` | `Ping`, `PingParams` |
| `SPQconninfoParse` | `ParseConninfo` |
| `SPQconndefaults` | `DefaultOptions` |
| `SPQconninfo` | `Conn.Conninfo` |
| `SPQconninfoFree` | C only |
| `SPQstatus`, `SPQtransactionStatus`, `SPQerrorMessage` | `Status`, `TransactionStatus`, `ErrorMessage` |
| `SPQdb`, `SPQuser`, `SPQpass`, `SPQhost`, `SPQport`, `SPQoptions` | `DB`, `User`, `Password`, `Host`, `Port`, `Options` |
| `SPQparameterStatus` | `ParameterStatus` |
| `SPQprotocolVersion`, `SPQserverVersion`, `SPQbackendPID` | `ProtocolVersion`, `ServerVersion`, `BackendPID` |
| `SPQsocket` | `Socket` (the shim may cache the number; it never changes) |
| `SPQclientEncoding` | `ClientEncoding` |
| `SPQsetClientEncoding` | `ClientEncodingStatement`, then the `SPQexec` sequence; 0 when the result is `CommandOK`, else -1 |
| `SPQlibVersion`, `SPQisthreadsafe` | `LibVersion`; constant 1 |
| `spg_encoding_to_char`, `spg_char_to_encoding` | `EncodingToChar`, `CharToEncoding` |
| `SPQsendQuery` … `SPQsendClosePortal` | the `Send…` method of the same name |
| `SPQgetResult` | loop: `GetResult` ◆; on wouldBlock wait |
| `SPQconsumeInput`, `SPQisBusy` | `ConsumeInput` ◆, `IsBusy` ◆ |
| `SPQflush`, `SPQsetnonblocking`, `SPQisnonblocking` | `Flush`, `SetNonblocking`, `IsNonblocking` |
| `SPQsetSingleRowMode`, `SPQsetChunkedRowsMode` | `SetSingleRowMode`, `SetChunkedRowsMode` |
| `SPQsocketPoll`, `SPQgetCurrentTimeUSec` | C only |
| `SPQexec` | loop `ExecStart` ◆ (wait on wouldBlock); `SendQuery`; loop `ExecFinish` ◆ (wait on wouldBlock) |
| `SPQexecParams`, `SPQprepare`, `SPQexecPrepared`, `SPQdescribePrepared`, `SPQdescribePortal`, `SPQclosePrepared`, `SPQclosePortal` | as `SPQexec` with the matching `Send…` |
| result accessors, `SPQclear`, `SPQfreemem`, `SPQresultMemorySize` | C only, on a block built once from `Result` (`Status`, `Fields`, `ParamTypes`, `CmdStatus`, `CmdTuples`, `OidValue`, `Binary`, `ErrorFields`, `ErrorMessage`, `ErrorQuery`, `ClientEncoding`, `RowData`) |
| `SPQfnumber` | C, by the rule of section 7.6, or `Result.FieldNumber` |
| `SPQresStatus` | `ResStatus` (or a C table with the same strings) |
| `SPQresultErrorMessage`, `SPQresultErrorField` | C only (stored in the block) |
| `SPQresultVerboseErrorMessage` | `FormatError` on the stored fields (section 8), or `Result.VerboseErrorMessage` while the Go result is at hand |
| `SPQsetErrorVerbosity`, `SPQsetErrorContextVisibility` | `SetErrorVerbosity`, `SetErrorContextVisibility` |
| `SPQsetNoticeReceiver`, `SPQsetNoticeProcessor` | C only (callbacks stored in C; fed by `TakeNotices`) |
| `SPQescapeLiteral`, `SPQescapeIdentifier`, `SPQescapeStringConn`, `SPQescapeByteaConn` | `Conn.EscapeLiteral`, `Conn.EscapeIdentifier`, `Conn.EscapeStringConn`, `Conn.EscapeByteaConn` |
| `SPQescapeString`, `SPQescapeBytea`, `SPQunescapeBytea` | `EscapeStringStatic`, `EscapeByteaStatic`, `UnescapeBytea` |
| `SPQgetCancel`, `SPQcancel`, `SPQfreeCancel` | C only, on a duplicate of `CancelFD()` |
| `SPQputCopyData`, `SPQputCopyEnd` | `PutCopyData` ◆, `PutCopyEnd` |
| `SPQgetCopyData` | `GetCopyData` ◆; rc 0 and `async` 0: wait and repeat |
| `SPQnotifies` | `Notifies` ◆ |
| `SPQXversion` | `Version` |
| `SPQXclusterDrop` | `DropCluster` |
| `SPQXreset`, `SPQXdump`, `SPQXrestore` | `ResetDatabase`, `Dump`, `Restore` |

`FormatError` is the exported formatter of section 8:

```go
// FormatError renders error or notice fields as libpq does. query is the text kept for
// LINE display ("" when none); isError selects the CONTEXT rule.
func FormatError(fields []ErrorField, query string, encoding int, isError bool,
	v Verbosity, cv ContextVisibility) string                                    // errtext.go
```

## 3. Connection strings

### 3.1 Grammar of keyword/value strings

A string that starts with `postgresql://` or `postgres://` is a URI (3.2). Anything
else is a sequence of `keyword = value` items:

1. Skip white space (the bytes C `isspace` accepts: space, `\t`, `\n`, `\v`, `\f`,
   `\r`).
2. The keyword runs to the next `=` or white space. White space may follow before the
   `=`. If the next non-space byte is not `=`, fail with
   `missing "=" after "<keyword>" in connection info string`.
3. Skip white space after `=`.
4. An unquoted value runs to the next white space; inside it a backslash makes the
   following byte literal. An empty unquoted value is allowed (`a= b=1` sets `a` to the
   empty string).
5. A value that starts with `'` runs to the next unescaped `'`; inside it a backslash
   makes the following byte literal (so `\'` and `\\`). A missing closing quote fails
   with `unterminated quoted string in connection info string`.
6. A keyword that is not in the option table (3.4) fails with
   `invalid connection option "<keyword>"`. `requiressl` is accepted as an alias: it
   sets `sslmode` to `require` when the value starts with `1`, else to `prefer`.
7. A later item for the same keyword replaces the earlier value.

### 3.2 Grammar of URIs

`postgresql://[user[:password]@][hostspec[,hostspec...]][/dbname][?param=value[&...]]`

1. After the prefix, if an `@` occurs before the first `/`, the text before it is the
   user information: up to the first `:` the user, after it the password. Empty parts
   are not stored.
2. Host specifications follow, separated by commas. A host is either `[...]` (an IPv6
   address; the brackets are dropped) or runs to `:`, `/`, `?` or `,`. After a host a
   `:` introduces its port, which runs to `/`, `?` or `,`. All hosts joined by `,`
   become the value of `host`, all ports joined by `,` the value of `port`; each is
   stored only when non-empty.
   - A `[` without `]` fails with
     `end of string reached when looking for matching "]" in IPv6 host address in URI: "<uri>"`.
   - `[]` fails with `IPv6 host address may not be empty in URI: "<uri>"`.
   - A byte other than `:`, `/`, `?`, `,` or the end after `]` fails with
     `unexpected character "<c>" at position <n> in URI (expected ":" or "/"): "<uri>"`
     where `<n>` is the 1-based byte position in the URI.
3. After `/` the database name runs to `?`; stored when non-empty.
4. After `?` come `key=value` pairs separated by `&`. A pair with a second `=` fails
   with `extra key/value separator "=" in URI query parameter: "<key>"`; a pair without
   `=` fails with `missing key/value separator "=" in URI query parameter: "<key>"`.
   `ssl=true` means `sslmode=require`. An unknown key fails with
   `invalid URI query parameter: "<key>"`.
5. User, password, host, port, database name, and both sides of a query pair are
   percent-decoded. A `%` not followed by two hexadecimal digits fails with
   `invalid percent-encoded token: "<text>"`; `%00` fails with
   `forbidden value %00 in percent-encoded value: "<text>"`. Leading and trailing
   spaces of a component are dropped; a space inside one fails with
   `unexpected spaces found in "<text>", use percent-encoded spaces (%20) instead`.

### 3.3 Arrays (`ParseParams`)

- Entries whose value is empty are skipped. An unknown keyword fails as in 3.1 item 6.
- With `expandDbname`, the value of the first `dbname` entry is examined before
  anything is stored: if it starts with a URI prefix or contains `=`, it is parsed by
  3.1/3.2 without defaults. When the loop reaches that entry, every option set by that
  parse replaces the corresponding option; entries after it override again, entries
  before it are overridden. Later `dbname` entries are plain names.

### 3.4 Option table

`DefaultOptions`, `ParseConninfo` and `Conn.Conninfo` return all 50 options in this
order. `EnvVar` is reported for compatibility with tools that print it; libspq never
reads the environment. "Compiled" is the built-in default (`-` means nil); it is what
`DefaultOptions` puts in `Val`, except for `user` and `dbname` (3.5). Only the options
marked ● change what libspq does.

| # | Keyword | EnvVar | Compiled | Label | DispChar | DispSize | |
|---|---|---|---|---|---|---|---|
| 1 | `service` | `PGSERVICE` | - | `Database-Service` | `` | 20 | |
| 2 | `user` | `PGUSER` | - | `Database-User` | `` | 20 | ● |
| 3 | `password` | `PGPASSWORD` | - | `Database-Password` | `*` | 20 | ● |
| 4 | `passfile` | `PGPASSFILE` | - | `Database-Password-File` | `` | 64 | |
| 5 | `channel_binding` | `PGCHANNELBINDING` | `disable` | `Channel-Binding` | `` | 8 | |
| 6 | `connect_timeout` | `PGCONNECT_TIMEOUT` | - | `Connect-timeout` | `` | 10 | |
| 7 | `dbname` | `PGDATABASE` | - | `Database-Name` | `` | 20 | ● |
| 8 | `host` | `PGHOST` | - | `Database-Host` | `` | 40 | ● |
| 9 | `hostaddr` | `PGHOSTADDR` | - | `Database-Host-IP-Address` | `` | 45 | ● |
| 10 | `port` | `PGPORT` | `5432` | `Database-Port` | `` | 6 | ● |
| 11 | `client_encoding` | `PGCLIENTENCODING` | - | `Client-Encoding` | `` | 10 | ● |
| 12 | `options` | `PGOPTIONS` | `` (empty) | `Backend-Options` | `` | 40 | ● |
| 13 | `application_name` | `PGAPPNAME` | - | `Application-Name` | `` | 64 | ● |
| 14 | `fallback_application_name` | - | - | `Fallback-Application-Name` | `` | 64 | ● |
| 15 | `keepalives` | - | - | `TCP-Keepalives` | `` | 1 | |
| 16 | `keepalives_idle` | - | - | `TCP-Keepalives-Idle` | `` | 10 | |
| 17 | `keepalives_interval` | - | - | `TCP-Keepalives-Interval` | `` | 10 | |
| 18 | `keepalives_count` | - | - | `TCP-Keepalives-Count` | `` | 10 | |
| 19 | `tcp_user_timeout` | - | - | `TCP-User-Timeout` | `` | 10 | |
| 20 | `sslmode` | `PGSSLMODE` | `disable` | `SSL-Mode` | `` | 12 | |
| 21 | `sslnegotiation` | `PGSSLNEGOTIATION` | `postgres` | `SSL-Negotiation` | `` | 9 | |
| 22 | `sslcompression` | `PGSSLCOMPRESSION` | `0` | `SSL-Compression` | `` | 1 | |
| 23 | `sslcert` | `PGSSLCERT` | - | `SSL-Client-Cert` | `` | 64 | |
| 24 | `sslkey` | `PGSSLKEY` | - | `SSL-Client-Key` | `` | 64 | |
| 25 | `sslcertmode` | `PGSSLCERTMODE` | - | `SSL-Client-Cert-Mode` | `` | 8 | |
| 26 | `sslpassword` | - | - | `SSL-Client-Key-Password` | `*` | 20 | |
| 27 | `sslrootcert` | `PGSSLROOTCERT` | - | `SSL-Root-Certificate` | `` | 64 | |
| 28 | `sslcrl` | `PGSSLCRL` | - | `SSL-Revocation-List` | `` | 64 | |
| 29 | `sslcrldir` | `PGSSLCRLDIR` | - | `SSL-Revocation-List-Dir` | `` | 64 | |
| 30 | `sslsni` | `PGSSLSNI` | `1` | `SSL-SNI` | `` | 1 | |
| 31 | `requirepeer` | `PGREQUIREPEER` | - | `Require-Peer` | `` | 10 | |
| 32 | `require_auth` | `PGREQUIREAUTH` | - | `Require-Auth` | `` | 14 | |
| 33 | `min_protocol_version` | `PGMINPROTOCOLVERSION` | - | `Min-Protocol-Version` | `` | 6 | |
| 34 | `max_protocol_version` | `PGMAXPROTOCOLVERSION` | - | `Max-Protocol-Version` | `` | 6 | |
| 35 | `ssl_min_protocol_version` | `PGSSLMINPROTOCOLVERSION` | `TLSv1.2` | `SSL-Minimum-Protocol-Version` | `` | 8 | |
| 36 | `ssl_max_protocol_version` | `PGSSLMAXPROTOCOLVERSION` | - | `SSL-Maximum-Protocol-Version` | `` | 8 | |
| 37 | `gssencmode` | `PGGSSENCMODE` | `disable` | `GSSENC-Mode` | `` | 8 | |
| 38 | `krbsrvname` | `PGKRBSRVNAME` | `postgres` | `Kerberos-service-name` | `` | 20 | |
| 39 | `gsslib` | `PGGSSLIB` | - | `GSS-library` | `` | 7 | |
| 40 | `gssdelegation` | `PGGSSDELEGATION` | `0` | `GSS-delegation` | `` | 1 | |
| 41 | `replication` | - | - | `Replication` | `D` | 5 | |
| 42 | `target_session_attrs` | `PGTARGETSESSIONATTRS` | `any` | `Target-Session-Attrs` | `` | 15 | |
| 43 | `load_balance_hosts` | `PGLOADBALANCEHOSTS` | `disable` | `Load-Balance-Hosts` | `` | 8 | |
| 44 | `scram_client_key` | - | - | `SCRAM-Client-Key` | `D` | 64 | |
| 45 | `scram_server_key` | - | - | `SCRAM-Server-Key` | `D` | 64 | |
| 46 | `oauth_issuer` | - | - | `OAuth-Issuer` | `` | 40 | |
| 47 | `oauth_client_id` | - | - | `OAuth-Client-ID` | `` | 40 | |
| 48 | `oauth_client_secret` | - | - | `OAuth-Client-Secret` | `*` | 40 | |
| 49 | `oauth_scope` | - | - | `OAuth-Scope` | `` | 15 | |
| 50 | `sslkeylogfile` | - | - | `SSL-Key-Log-File` | `D` | 64 | |

The table is libpq 18's (`PQconninfoOptions` in `fe-connect.c`), with the compiled
defaults of a build without TLS and GSS, which is what libspq is.

### 3.5 Resolution of the effective options

Applied by the connect functions after parsing (unexported
`func resolve(options []Option) (target, error)` in `conninfo.go`):

| Item | Rule |
|---|---|
| `user` | given value, else `postgres`. Clipped to at most 63 bytes without splitting a UTF-8 sequence. |
| `dbname` | given value, else the user. Clipped the same way. |
| cluster host | `host` when non-empty, else `hostaddr` when non-empty, else `localhost`; of a comma-separated list only the text before the first comma. |
| `port` | given value (text before the first comma), else `5432`. It must be a decimal number from 1 to 65535, else the connection fails with `invalid port number: "<value>"`. |
| cluster name | `<host>:<port>`, compared byte for byte. |
| `password` | stored for `Password()`; unused. |
| `options` | kept as text for `Options()`; parsed by 3.6 when the session starts. |
| `application_name` | given value, else `fallback_application_name`. |
| `client_encoding` | given value; `auto` means `UTF8`. |

```go
// target is the result of resolve (conninfo.go's sibling resolve.go).
type target struct {
	user, database, host, port, password, optionsText string
	cluster string // "<host>:<port>"
}

func resolve(options []Option) (target, error)
```

`DefaultOptions` sets `Val` to the compiled default where there is one, `user` to
`postgres` and `dbname` to `postgres`. `Conn.Conninfo` sets `Val` to the value in
effect for options given or resolved above (`host` and `port` as resolved) and to the
compiled default otherwise.

Every other option is stored and reported back but neither validated nor used.

### 3.6 `options` and `StartupSettings`

The `options` value is split into tokens at white space (space, `\t`, `\n`, `\r`,
`\v`, `\f`); a backslash makes the next byte part of the token. Accepted token forms
are `-c name=value` (two tokens), `-cname=value` and `--name=value`. Each sets the
session default `name` (ASCII letters lower-cased) to `value`. `StartupSettings`
returns the errors below as `*sqlstate.Error` with SQLSTATE `08P01`; the connect
functions report them as a connection failure in the first format of 4.5 (they are
what a server would answer, not conninfo syntax errors):

| Condition | Message |
|---|---|
| backslash at the end | `unterminated escape in startup options` |
| `-c` as the last token | `missing setting in startup options` |
| any other token | `unsupported startup option` |
| no `=` or an empty name | `invalid setting in startup options` |

These are pgserver's rules and texts (`pgserver/startup.go`, `startupOptionTokens` and
`startupOptions`), which stay in pgserver; spq implements the same rules in
`conninfo.go`.

`StartupSettings` returns the map of the settings from `options`, then, overriding
them, `application_name` (3.5) and `client_encoding` (3.5) when set.

## 4. Clusters and automatic provisioning (`cluster.go`)

### 4.1 Data structures

```go
// registry holds the clusters of the process.
var registry struct {
	mu       sync.Mutex
	clusters map[string]*cluster // key: cluster name "<host>:<port>"
}

type cluster struct {
	name   string
	engine *core.Engine        // engine.NewWithSettings(nil, "postgres", false)
	known  map[string]struct{} // setting definition names, immutable after creation

	mu     sync.Mutex // guards the fields below; never held across engine calls
	nextID uint64     // last session id handed out; starts at 1
	open   int        // connections attached

	provision sync.Mutex // serializes bootstrap sessions (id 1)
}

func clusterFor(name string) (*cluster, error)      // get or create, attaches nothing
func (cl *cluster) attach()                         // open++
func (cl *cluster) detach()                         // open--
func (cl *cluster) newSessionID() (ids.SessionID, error)
func (cl *cluster) admit(ctx context.Context, user, database string,
	defaults map[string]string) (*session.Session, ids.SessionID, *sqlstate.Error, error)
func (cl *cluster) bootstrap(ctx context.Context, statement string) error
```

Lock order: `registry.mu` before `cluster.mu`; `cluster.provision` is taken with
neither held. No lock is held while the engine runs, except `cluster.provision`
around a bootstrap session.

### 4.2 Creating a cluster

`clusterFor` creates the engine under `registry.mu` (creation is in-memory and takes
under a millisecond). It then collects the known setting names with one bootstrap
session, exactly as `pgserver/database.go` `provision` does:

```go
known := pgsession.SettingNames{}
principal, err := e.ResolveLogin("postgres")
admission, err := e.AdmitDatabase(ctx, principal, 1, core.DatabaseSelector{})
boot, err := admission.Target().StartSession(ctx, admission,
	session.SessionStart{ID: 1, Defaults: known})
admission.Release()
boot.RequestEnd(session.CleanClose)
err = boot.WaitEnd(context.Background())
```

**(measured)** A fresh engine has exactly one database, `postgres`, and the provider
sees 397 definition names, among them `application_name`, `client_encoding` and
`search_path`.

### 4.3 Session ids

`newSessionID` returns `++nextID` under `cluster.mu`; the first connection of a cluster
gets 2. A value above `math.MaxInt32` fails with `backend identity exhausted`. The id
is the session id given to `pgsession.Start`, the value of `pg_backend_pid()`, the
`be_pid` of notifications and `Conn.BackendPID()`.

**(measured)** The engine accepts the same id in two databases of one cluster and
rejects it within one database (`stuntgres: duplicate advisory session`), and an id is
reusable after its session ended. A cluster-wide counter therefore gives unique ids,
and serializing bootstrap sessions makes id 1 safe to reuse.

### 4.4 `admit`: connect with automatic provisioning

```
1. if _, ok := cl.engine.Roles().Lookup(user); !ok:
       cl.bootstrap(ctx, `CREATE ROLE "<user>" LOGIN SUPERUSER`)   // 42710 is success
2. id := cl.newSessionID()
3. es, notice, err := pgsession.Start(ctx, pgsession.StartConfig{
       Engine: cl.engine, ID: id, Role: user,
       Database: core.DatabaseSelector{Name: database, Explicit: true},
       Defaults: defaults, Known: cl.known})
4. if err has SQLSTATE 3D000 and this is the first attempt:
       cl.bootstrap(ctx, `CREATE DATABASE "<database>"`)           // 42P04 is success
       go to 2 (once)
5. return es, id, notice, err
```

`"<name>"` is the name with every `"` doubled, inside double quotes. A role that
exists is never altered: when it lacks `LOGIN`, `Start` fails and so does the
connection, as in PostgreSQL.

**(measured)** `ResolveLogin` reports a missing role with a plain error that has no
SQLSTATE (`stuntgres: authenticated role "x" does not exist`), so the role is looked up
first instead of interpreting that error. `AdmitDatabase` reports a missing database as
`*sqlstate.Error` FATAL `3D000` `database "x" does not exist`. `CREATE DATABASE` works
while other sessions are connected to `postgres`. A second `CREATE ROLE` fails with
`42710`, a second `CREATE DATABASE` with `42P04`; `bootstrap` treats both as success,
which settles the race between two connections that provision the same name.

`bootstrap` takes `cl.provision`, opens a session of role `postgres` with id 1 in the
default database (the three engine calls of 4.2 with a nil `Defaults`), wraps it in
`pgsession.New` with a transport that records the backend messages, dispatches one
`Q` message with the statement, ends the session (`RequestEnd(CleanClose)`,
`WaitEnd`), and returns nil unless the recording contains an `E` message; then it
returns the decoded fields as `*sqlstate.Error` (code from field `C`, message from
`M`). The session core of the selected database is `core.EngineOf(es)`; it differs
from `cl.engine` for every database other than `postgres` **(measured)**.

### 4.5 Connection failures

Every failure between option resolution and the first ReadyForQuery makes the
connection `ConnectionBad` with

```
connection to server at "<host>", port <port> failed: <text>
```

where `<text>` is `FormatError` (default verbosity) of `pgsession.Diagnostic(err)` with
both severity fields forced to `FATAL`, which ends in a newline. Examples: a role
without `LOGIN`; a bad session default such as `client_encoding=NOPE` **(measured:
SQLSTATE `22023`, `invalid value for parameter "client_encoding": "NOPE"`)**; an
unknown setting in `options` (`42704`, from `pgsession.Start`).

A failure of option parsing or resolution (3.1 to 3.5)
makes the connection `ConnectionBad` with the bare message and a newline, and no
descriptors (`Socket()` is -1).

### 4.6 Dropping a cluster

`DropCluster(host, port)` resolves the name by 3.5 (empty host means `localhost`,
empty port `5432`), and under `registry.mu`:

| Condition | Result |
|---|---|
| no such cluster | nil (nothing to drop) |
| `open > 0` | error `cluster "<name>" has <n> open connections` |
| otherwise | the entry is deleted; nil |

**(measured)** An engine whose sessions ended holds no goroutine and is garbage
collected once unreferenced (`runtime.AddCleanup` on the engine fires after
`runtime.GC()`; goroutine count returns to the baseline). Dropping the registry entry
is therefore all that is needed; the next connection to the name gets a new engine.

## 5. Message codec (`messages.go`)

Bodies are PostgreSQL protocol version 3 message bodies without the type byte and the
length word; see
[message formats](https://www.postgresql.org/docs/18/protocol-message-formats.html).
Integers are big-endian. A "string" is its bytes followed by one zero byte.

### 5.1 Frontend messages

| Tag | Message | Body | Builder |
|---|---|---|---|
| `Q` | Query | string query | `AppendQuery(dst []byte, query string) []byte` |
| `P` | Parse | string statement, string query, int16 n, n × int32 type OID | `AppendParse(dst []byte, stmt, query string, paramTypes []uint32) []byte` |
| `B` | Bind | string portal, string statement, int16 f, f × int16 format, int16 n, n × (int32 length or -1, bytes), int16 r, r × int16 result format | `AppendBind(dst []byte, portal, stmt string, params []Param, resultFormat int) []byte` |
| `D` | Describe | byte `S` or `P`, string name | `AppendDescribe(dst []byte, kind byte, name string) []byte` |
| `E` | Execute | string portal, int32 maximum rows | `AppendExecute(dst []byte, portal string, maxRows int32) []byte` |
| `C` | Close | byte `S` or `P`, string name | `AppendClose(dst []byte, kind byte, name string) []byte` |
| `S` | Sync | empty | none |
| `H` | Flush | empty | none (never sent) |
| `d` | CopyData | the data bytes | none |
| `c` | CopyDone | empty | none |
| `f` | CopyFail | string message | `AppendCopyFail(dst []byte, message string) []byte` |

`AppendBind` writes `f = 0` when no parameter is binary and `f = len(params)` format
codes otherwise; it always writes `r = 1` and the single result format. A string
argument must not contain a zero byte: the builders cut it at the first one (a C
string cannot contain one).

### 5.2 Backend messages

| Tag | Message | Body | Decoder |
|---|---|---|---|
| `T` | RowDescription | int16 n, n × (string name, int32 table OID, int16 column, int32 type OID, int16 type size, int32 type modifier, int16 format) | `DecodeRowDescription(body []byte) ([]FieldDesc, error)` |
| `D` | DataRow | int16 n, n × (int32 length or -1, bytes) | `CheckDataRow(body []byte, nfields int) error` |
| `C` | CommandComplete | string tag | `DecodeString(body []byte) (string, error)` |
| `I` | EmptyQueryResponse | empty | none |
| `E`, `N` | ErrorResponse, NoticeResponse | repeated (byte code, string value), then a zero byte | `DecodeErrorFields(body []byte) ([]ErrorField, error)` |
| `S` | ParameterStatus | string name, string value | `DecodeParameterStatus(body []byte) (name, value string, err error)` |
| `Z` | ReadyForQuery | byte `I`, `T` or `E` | `DecodeReadyForQuery(body []byte) (TransactionStatus, error)` |
| `A` | NotificationResponse | int32 pid, string channel, string payload | `DecodeNotification(body []byte) (Notification, error)` |
| `1`, `2`, `3`, `n`, `s` | ParseComplete, BindComplete, CloseComplete, NoData, PortalSuspended | empty | none |
| `t` | ParameterDescription | int16 n, n × int32 type OID | `DecodeParameterDescription(body []byte) ([]uint32, error)` |
| `G`, `H` | CopyInResponse, CopyOutResponse | byte overall format, int16 n, n × int16 column format | `DecodeCopyResponse(body []byte) (binary bool, formats []int16, err error)` |
| `d` | CopyData | the data bytes | none |
| `c` | CopyDone | empty | none |

Signed 16-bit fields (column number, type size, format) are sign-extended.
`DecodeReadyForQuery` maps `I`, `T`, `E` to `TransIdle`, `TransInTrans`,
`TransInError` and anything else to `TransUnknown`. A decoder returns an error when
the body is too short or has bytes left over; the caller reports it as a protocol
violation (7.4). `pgsession` never sends `K` (BackendKeyData) or `V`; if one arrives it
is an unexpected message (7.4).

### 5.3 Row data

A result stores its rows as the concatenation of the DataRow bodies in arrival order
(`Result.RowData`), after `CheckDataRow` validated each: the field count equals the
result's and every length fits the body. `Result.Value` walks this layout. The C shim
makes one pass to count value bytes and a second to build its cell index.

### 5.4 Limits

A command has at most 65535 parameters (`MaxParams`). `pgsession` rejects a backend
message body of `pgsession.MaxMessageLength` bytes or more; spq adds no limit of its
own.

## 6. Transport (`conn.go`, `transport.go`, `wake_linux.go`, `wake_other.go`, `cancel.go`)

### 6.1 Structures

```go
type asyncStatus uint8

const (
	asyncIdle asyncStatus = iota
	asyncBusy
	asyncReady
	asyncReadyMore
	asyncCopyIn
	asyncCopyOut
)

type cmdClass uint8

const (
	cmdNone cmdClass = iota
	cmdStartup  // waiting for the first ReadyForQuery of a session
	cmdSimple   // SendQuery
	cmdExtended // SendQueryParams, SendQueryPrepared
	cmdPrepare  // SendPrepare
	cmdDescribe // SendDescribePrepared, SendDescribePortal
	cmdClose    // SendClosePrepared, SendClosePortal
)

type message struct {
	tag  byte
	body []byte // owned
}

// Conn: every field is application-side (R4) unless marked otherwise.
type Conn struct {
	options []Option // as parsed
	target  target   // resolved by 3.5
	cl      *cluster // attached cluster, nil when none

	in         inbox                // shared with the worker, section 6.2
	link       atomic.Pointer[link] // current session; nil when none
	cancelW    int                  // descriptor C writes cancel bytes to; -1 when absent
	cancelR    *os.File             // read side, owned by cancelLoop
	cancelDone chan struct{}

	status      ConnStatus
	async       asyncStatus
	cmd         cmdClass
	cmdActive   bool    // a command was sent and its ReadyForQuery was not parsed yet
	cmdQuery    string  // text of the active command, for LINE display
	result      *Result // result under construction
	saved       *Result // base result while a partial result is being filled (row modes)
	errorResult bool    // a client-side error result is pending
	partial     bool
	singleRow   bool
	maxChunk    int
	xact        TransactionStatus
	rbuf        []message // consumed from the inbox, not yet parsed
	lost        bool      // the end of the session was noticed
	errMsg      []byte    // ErrorMessage text
	errReported int       // prefix of errMsg already handed out in results
	params      map[string]string
	sversion    int
	clientEnc   int
	stdStrings  bool
	backendPID  int
	nonblocking bool
	verbosity   Verbosity         // initial ErrorsDefault
	showContext ContextVisibility // initial ShowContextErrors
	notices     []*Result
	notifies    []*Notification
	execStarted bool
	execLast    *Result
	closed      bool
}

// link is one session of a Conn. Reset replaces it.
type link struct {
	c          *Conn
	es         *session.Session
	sess       *pgsession.Session
	slot       pgsession.CommandSlot
	ctx        context.Context    // physical lifetime of this session
	stop       context.CancelFunc
	outbox     chan pgsession.Message // capacity outboxCap
	ended      <-chan error           // pgsession.WatchEnd
	watchDone  <-chan struct{}
	workerDone chan struct{}
	closing    atomic.Bool
}

const (
	outboxCap  = 64      // frontend messages
	inboxLimit = 1 << 20 // bytes of backend message bodies
)
```

The `Conn` struct is declared once, completely, by the part that creates `conn.go`;
later parts add methods in their own files and never add fields.

### 6.2 Inbox and readiness descriptor

```go
type inbox struct {
	mu      sync.Mutex
	space   *sync.Cond // signalled when the inbox was emptied or closing was set
	q       []message
	size    int  // sum of len(body) in q
	wakeSet bool // one byte is in the descriptor
	dead    bool // the worker ended; nothing more will arrive
	closing bool // Close or Reset is tearing the session down
	appFD   int  // what Socket() returns
	workFD  int  // the worker writes the wake byte here
}

func (in *inbox) push(tag byte, body []byte) error // worker side; copies body
func (in *inbox) pushPanic(r any)                  // 6.6; never waits
func (in *inbox) take() (moved []message, dead bool) // application side
func (in *inbox) markDead()
func (in *inbox) setClosing(on bool)
func (in *inbox) reset() // after a session was torn down, before the next one starts
```

The descriptors are one `AF_UNIX` `SOCK_STREAM` socketpair created with
`SOCK_CLOEXEC|SOCK_NONBLOCK` (`syscall.Socketpair`; `wake_other.go`, built on
non-Linux systems, returns the error `libspq is supported on Linux only`). `appFD` is
never written by anyone and is therefore always writable.

Algorithms, all under `in.mu`:

| Operation | Steps |
|---|---|
| `push` | while `size >= inboxLimit && len(q) > 0 && !closing`: `space.Wait()`. If `closing`: return `errClosing`. Append a copy; `size += len(body)`. If `!wakeSet`: write one byte to `workFD`, `wakeSet = true`. |
| `take` | `moved = q`; `q = nil`; `size = 0`. If `wakeSet && !dead`: read one byte from `appFD`, `wakeSet = false`. `space.Broadcast()`. Return `moved, dead`. |
| `markDead` | `dead = true`. If `!wakeSet`: write one byte, `wakeSet = true`. |
| `setClosing` | set the flag; `space.Broadcast()`. |
| `reset` | `q = nil`; `size = 0`; `dead = false`; `closing = false`; if `wakeSet`: read one byte, `wakeSet = false`. |

**Invariant.** Whenever `in.mu` is free: `wakeSet == (len(q) > 0 || dead)`, and the
descriptor holds exactly one unread byte if `wakeSet` and none otherwise.

Proof, by showing each operation keeps it. `push` leaves `len(q) > 0` and ends with
`wakeSet` true, writing a byte only if none was there. `take` leaves `len(q) == 0`; if
`dead` it keeps the byte and `wakeSet` (true, as required); otherwise it removes the
one byte and clears `wakeSet`. `markDead` makes the right side true and ensures the
byte. `reset` makes the right side false and removes the byte if present. No other code
touches the descriptor or these fields. Because the byte is written inside the same
critical section that makes the inbox non-empty, an application that polls the
descriptor after `take` returned cannot miss a later push: either the push completed
before `take` (its message is in `moved`) or after (its byte is in the descriptor).
The write cannot fail with `EAGAIN` because at most one byte is ever unread.

So the descriptor is readable exactly while the inbox is non-empty or the session has
ended, which is the contract of docs/LIBSPQ.md 5.3. Once `dead`, it stays readable.

Back-pressure: a worker that produces faster than the application consumes blocks in
`push` once a megabyte is waiting. This is what stalls the engine in row-by-row modes
and during `COPY TO STDOUT`.

### 6.3 Outbox

`link.outbox` is the channel `Transport.Messages()` returns: the worker loop reads
commands from it, and `pgsession` reads COPY data from it while `Dispatch` is inside a
`COPY FROM STDIN`.

- A command is at most five messages and is only sent when the connection is idle,
  which means the worker has consumed everything sent before. Sends of command
  messages therefore never block; they use a plain channel send.
- `PutCopyData` and `PutCopyEnd` send with
  `select { case l.outbox <- m: case <-l.workerDone: }`. With 64 messages queued the
  call waits until the engine has taken one. This is the one place where an
  application thread may wait inside Go (R2, docs/LIBSPQ.md 5.4). If the COPY already
  failed, the worker loop discards the remaining `d`, `c` and `f` messages
  (`pgsession` drops them outside COPY), so the wait ends.

### 6.4 Session start

`startSession` (`transport.go`), called by the connect functions and by `Reset` on the
application goroutine:

```
0. settings, err := StartupSettings(c.options)                  // 3.6
1. cl, err := clusterFor(c.target.cluster)                      // 4.2
2. es, id, notice, err := cl.admit(ctx, c.target.user, c.target.database, settings)   // 4.4
3. cl.attach(); c.cl = cl; c.backendPID = int(id)
4. l := &link{c: c, es: es, outbox: make(chan pgsession.Message, outboxCap), workerDone: make(chan struct{})}
   l.ctx, l.stop = context.WithCancel(context.Background())
   l.sess = pgsession.New(pgsession.Config{Session: es, Context: l.ctx, Transport: transport{l}})
   l.ended, l.watchDone = pgsession.WatchEnd(l.ctx, es, pgsession.EndHooks{
       Terminate: func(fatal *sqlstate.Error) { l.slot.Cancel(fatal) }})
5. c.async, c.cmd, c.cmdActive = asyncBusy, cmdStartup, true
   if notice != nil: append a NonfatalError result built from it to c.notices
6. err = l.sess.ReportSettings(); if err == nil { err = l.sess.SendReady() }   // pushes S..., Z
7. c.link.Store(l); go l.run()
8. c.pull(); c.parse()                                           // section 7
9. c.async must now be asyncIdle; then c.status = ConnectionOK and errMsg is cleared
```

Any error in steps 0, 1, 2, 6 or 9 is a connection failure (first format of 4.5);
after step 3 it also runs `stopSession`. `ctx` in steps 1 and 2 is `context.Background()`.

The transport handed to `pgsession`:

```go
type transport struct{ l *link }

func (t transport) Send(tag byte, body []byte) error      { return t.l.c.in.push(tag, body) }
func (t transport) Flush() error                          { return nil }
func (t transport) BeginFatal() error                     { return nil }
func (t transport) Messages() <-chan pgsession.Message    { return t.l.outbox }
func (t transport) Release(pgsession.Message)             {}
func (t transport) Ended() <-chan error                   { return t.l.ended }
```

No `Observer` is installed.

### 6.5 Worker loop

```go
func (l *link) run() {
	defer close(l.workerDone)
	defer func() {
		if r := recover(); r != nil {
			l.c.in.pushPanic(r) // 6.6
		}
		reason := session.Terminated
		if l.closing.Load() {
			reason = session.CleanClose
		}
		l.es.RequestEnd(reason)
		l.c.in.markDead()
	}()
	for {
		wake, err := l.sess.DrainNotifications()
		if err != nil {
			l.fatal(err)
			return
		}
		select {
		case <-l.ctx.Done():
			return
		case terminal := <-l.ended:
			l.fatal(terminal)
			return
		case <-wake:
		case m := <-l.outbox:
			ctx, done := l.slot.Begin(l.ctx)
			err := l.sess.Dispatch(ctx, m)
			done()
			if err != nil {
				l.fatal(err)
				return
			}
		}
	}
}

// fatal reports a connection-ending error unless the application is closing or
// pgsession already sent it. Errors that are not FATAL diagnostics are transport
// failures and produce no output.
func (l *link) fatal(err error) {
	if l.closing.Load() || l.ctx.Err() != nil {
		return
	}
	if pgsession.Diagnostic(err).Severity == "FATAL" && !l.sess.FatalSent() {
		_ = l.sess.WriteSQLError(l.ctx, "", err)
	}
}
```

This is the loop of `pgserver/dispatch.go` `run` without socket, capture and reference
forwarding. Notifications reach the application because `DrainNotifications` runs
before every wait and when the engine's wake channel fires.

### 6.6 Panics

`inbox.pushPanic(r)` appends, without waiting for space, one `E` message built with
`pgsession.DiagnosticBody` from
`sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "XX000",
Message: fmt.Sprintf("libspq: internal error: %v", r)}`. The deferred function then
marks the inbox dead, so the application sees a FATAL error result and a broken
connection.

Every exported function of the package that can run engine or parsing code starts with
`defer c.recoverCall()` (for functions returning a value, with a named result set to
the failure value). `recoverCall` recovers, sets `status = ConnectionBad`, `lost =
true`, and appends `libspq: internal error: <panic>` to `ErrorMessage`.

### 6.7 Close and Reset

```go
func (c *Conn) stopSession() {
	l := c.link.Load()
	if l == nil {
		return
	}
	l.closing.Store(true)
	c.in.setClosing(true)                   // unblocks a worker waiting in push
	l.es.RequestEnd(session.CleanClose)     // cancels a running statement
	l.stop()
	<-l.workerDone
	<-l.watchDone
	_ = l.es.WaitEnd(context.Background())
	c.link.Store(nil)
	c.cl.detach()
}
```

**(measured)** `RequestEnd(CleanClose)` during `SELECT pg_sleep(30)` makes the
statement return after 0.3 ms and `WaitEnd` return right after, so `Close` does not
wait for a statement to finish by itself.

`Close`: if `closed` return; `stopSession`; close the cancel descriptors and wait for
`cancelDone`; close both wake descriptors; `status = ConnectionBad`; `closed = true`.
`Socket()` and `CancelFD()` return -1 afterwards.

`Reset`: `stopSession`; `c.in.reset()`; reset the application-side state to its
initial values (everything from `status` to `execLast` in 6.1 except `nonblocking`,
`verbosity`, `showContext`; `notifies` is cleared; `errMsg` is cleared);
`startSession`. The descriptors are untouched. A `Conn` whose option resolution failed
stays as it is.

Engine-initiated end (`pg_terminate_backend`, a session timeout): `WatchEnd` cancels
the running command with the FATAL as cause and yields it on `ended`; the worker
writes the FATAL through `WriteSQLError` unless `Dispatch` already did, then exits and
marks the inbox dead. **(measured)** the victim statement returns `57P01`
`terminating connection due to administrator command`.

### 6.8 Cancel descriptor (`cancel.go`)

A second socketpair, created with the wake pair. `cancelW` is handed to C
(`CancelFD()`); `cancelR` is wrapped with `os.NewFile` (the descriptor is nonblocking,
so reads park the goroutine without holding a thread).

```go
func (c *Conn) cancelLoop() {
	defer close(c.cancelDone)
	var b [1]byte
	for {
		if _, err := c.cancelR.Read(b[:]); err != nil {
			return
		}
		if l := c.link.Load(); l != nil {
			l.slot.Cancel(session.UserCancelCause)
		}
	}
}

func (c *Conn) CancelFD() int        // cancelW, or -1 when absent or closed
func (c *Conn) RequestCancel() bool  // writes one byte to cancelW; false when absent
```

`CommandSlot.Cancel` does nothing when no command is being dispatched, so a cancel
that arrives while the connection is idle has no effect. A byte that arrives after its
command finished cancels whatever runs when it is read; libpq has the same race.
During `COPY FROM STDIN` a cancel takes effect when the engine next reads a record
(pgsession's rule): the application must keep sending data or end the COPY.

`Close` closes `cancelW` and `cancelR`; a duplicate of `cancelW` held by C stays valid
and writing to it fails harmlessly.

## 7. Connection state machine (`state.go`, `simple.go`, `extended.go`, `copy.go`, `rowmodes.go`, `notices.go`)

It follows libpq's nonblocking machine: input moves in two steps (arrive in the inbox,
then get parsed), and parsing stops as soon as a result is ready for collection.

### 7.1 Helpers (`state.go`)

| Helper | Definition |
|---|---|
| `clearError()` | if `!cmdActive`: `errMsg = errMsg[:0]`, `errReported = 0`. |
| `appendError(text)` | append `text` and `"\n"` to `errMsg`. |
| `pending()` | `result != nil || errorResult`. |
| `dropResult()` | `result = nil`, `saved = nil`, `errorResult = false`. |
| `saveErrorResult()` | `dropResult()`, `errorResult = true`. |
| `prepareResult()` | If `result != nil`: `res = result`; if its status is `FatalError`, `errReported = len(errMsg)`. Otherwise: if `!errorResult`, `appendError("no error text available")`; `res` = a `FatalError` result without fields whose message is `errMsg[errReported:]` (from 0 if `errReported` is out of range); `errReported = len(errMsg)`. Then `result = saved`, `saved = nil`, `errorResult = false`. Return `res`. |
| `pull()` | `moved, dead := in.take()`; append `moved` to `rbuf`; return `len(moved), dead`. |
| `connectionLost()` | once: `status = ConnectionBad`, `lost = true`, `appendError("server closed the connection unexpectedly\n\tThis probably means the server terminated abnormally\n\tbefore or while processing the request.")`. |
| `parse()` | 7.4. |

### 7.2 Calls: preconditions and effects

"start" is the common beginning of every `Send…` function: `clearError()`; if
`status != ConnectionOK`: `appendError("no connection to the server")`, return false;
if `async != asyncIdle`: `appendError("another command is already in progress")`,
return false. Checks listed after "start" run in the order given. A successful send
does: `dropResult()`; `partial, singleRow, maxChunk = false, false, 0`; set `cmd`,
`cmdQuery`, `cmdActive = true`, `async = asyncBusy`; put the messages on the outbox.

| Call | Checks after start (message → false) | Messages sent | `cmd` |
|---|---|---|---|
| `SendQuery(q)` | `q == nil`: `command string is a null pointer` | `Q` | `cmdSimple` |
| `SendQueryParams(q, n, types, params, rf)` | `q == nil`: as above; `n < 0 || n > 65535`: `number of parameters must be between 0 and 65535`; a param with `LengthMissing`: `length must be given for binary parameter` | `P` (unnamed, `types` or none), `B`, `D` kind `P` name empty, `E` portal empty rows 0, `S` | `cmdExtended` |
| `SendPrepare(name, q, n, types)` | `name == nil`: `statement name is a null pointer`; `q == nil`: `command string is a null pointer`; `n` out of range: as above | `P`, `S` | `cmdPrepare` |
| `SendQueryPrepared(name, n, params, rf)` | `name == nil`: `statement name is a null pointer`; `n` out of range; `LengthMissing` | `B`, `D` kind `P`, `E`, `S` | `cmdExtended` |
| `SendDescribePrepared(s)`, `SendDescribePortal(p)` | none | `D` kind `S` or `P`, `S` | `cmdDescribe` |
| `SendClosePrepared(s)`, `SendClosePortal(p)` | none | `C` kind `S` or `P`, `S` | `cmdClose` |

`cmdQuery` is the query text for `SendQuery`, `SendQueryParams` and `SendPrepare` and
empty otherwise. The `LengthMissing` failure happens while building `B`; nothing is
sent and the state is unchanged.

| Call | Behavior |
|---|---|
| `ConsumeInput()` | `n, dead := pull()`. If `n == 0 && dead`: `connectionLost()`. Return `status == ConnectionOK`. It does not parse. |
| `IsBusy()` | `parse()`; return `async == asyncBusy && status != ConnectionBad`. |
| `GetResult()` | `parse()`. While `async == asyncBusy`: `n, dead := pull()`; if `n > 0`: `parse()` and continue; if `dead`: `connectionLost()`, `saveErrorResult()`, `async = asyncIdle`, return `prepareResult(), false`; otherwise return `nil, true`. Then by state: `asyncIdle`: `nil, false`. `asyncReady`: `res := prepareResult()`; if `result == nil`, `async = asyncBusy`; return `res, false`. `asyncReadyMore`: `res := prepareResult()`; `async = asyncBusy`; return `res, false`. `asyncCopyIn` / `asyncCopyOut`: if `status != ConnectionOK`: `saveErrorResult()`, `async = asyncIdle`, return `prepareResult(), false`; if `result` has that COPY status return `prepareResult(), false`; else return a new empty result with that COPY status. |
| `ExecStart()` | First call of a cycle: `clearError()`, `execStarted = true`. Loop: `res, wb := GetResult()`; `wb`: return `false, true`. `res == nil`: `execStarted = false`, return `true, false`. Status `CopyIn`: `PutCopyEnd` with the text `COPY terminated by new PQexec`; on -1: `execStarted = false`, return `false, false`. Status `CopyOut`: `async = asyncBusy` (the rest of the data is skipped by 7.4). Then if `status == ConnectionBad`: `execStarted = false`, return `false, false`. |
| `ExecFinish()` | Loop: `res, wb := GetResult()`; `wb`: return `nil, true`. `res == nil`: return `execLast` (clearing it), `false`. Otherwise `execLast = res`; if its status is `CopyIn`, `CopyOut` or `CopyBoth`, or `status == ConnectionBad`: return `execLast` (clearing it), `false`. |
| `TransactionStatus()` | `status != ConnectionOK`: `TransUnknown`; `async != asyncIdle`: `TransActive`; else `xact`. |
| `SetSingleRowMode()` | Allowed iff `async == asyncBusy && cmdActive && (cmd == cmdSimple || cmd == cmdExtended) && !pending()`. Then `partial, singleRow, maxChunk = true, true, 1`. No parsing. |
| `SetChunkedRowsMode(n)` | Same condition and `n > 0`. Then `partial, singleRow, maxChunk = true, false, n`. |
| `Notifies()` | `parse()`; pop the oldest notification or return nil. |
| `TakeNotices()` | return `notices` and set it to nil. No parsing. |
| `PutCopyData(d)` | `async != asyncCopyIn`: `appendError("no COPY in progress")`, -1. `parse()`. If `len(d) > 0`: send `d` (6.3); if the worker is gone, `connectionLost()` and -1. Return 1. |
| `PutCopyEnd(msg)` | `async != asyncCopyIn`: as above, -1. Send `f` with the text when `msg != nil`, else `c`. If `cmd != cmdSimple`: also send `S`. `async = asyncBusy`. Return 1 (-1 with `connectionLost()` if the worker is gone). |
| `GetCopyData()` | `async != asyncCopyOut`: `appendError("no COPY in progress")`, `nil, -2`. Then 7.5. |

`ExecFinish` returns the last result of the command: libpq's "PQexec returns the last
result" rule. Error messages of all failed results of the cycle accumulate in
`ErrorMessage`, because `errMsg` is only cleared at the start of the next cycle; each
error result carries only its own message.

The connection-loss sequence seen by an application, as in libpq: the FATAL result the
engine sent (for example `57P01`), then on the next `GetResult` a second `FatalError`
result without fields whose message is the `server closed the connection
unexpectedly…` text, with `Status()` now `ConnectionBad`, then `nil`. `ExecFinish`
therefore returns the second one, and `ErrorMessage` holds both texts.

### 7.3 Parameter status side effects

On `S name value`: `params[name] = value`. Additionally:

| Name | Effect |
|---|---|
| `client_encoding` | `clientEnc = CharToEncoding(value)`, or 0 (SQL_ASCII) when unknown; the process-wide static encoding (9.3) gets the same value. |
| `standard_conforming_strings` | `stdStrings = (value == "on")`; the static flag likewise. |
| `server_version` | `sversion` from the leading integers `a.b.c`: three numbers: `(100a+b)*100+c`; two: `10000a+b` when `a >= 10`, else `(100a+b)*100`; one: `10000a`; none: 0. `18.0` gives 180000. |

`ParameterStatus(name)` reads `params`. `ServerVersion()` is `sversion`, or 0 when
`status == ConnectionBad`.

### 7.4 `parse()`: backend messages by state

`parse` handles the messages in `rbuf` in order and removes each one it handles. It
stops when `rbuf` is empty or a row says "stop". For each message, the first matching
row applies.

| # | Condition | Message | Action |
|---|---|---|---|
| 1 | any state | `A` | decode; append to `notifies`. |
| 2 | any state | `N` | decode fields; build a `NonfatalError` result (8.4); append to `notices`. |
| 3 | `async` is `asyncReady`, `asyncReadyMore`, `asyncCopyIn` or `asyncCopyOut` | anything else | stop (leave the message). |
| 4 | `asyncIdle` | `E` | treat as a notice: as row 2. |
| 5 | `asyncIdle` | `S` | 7.3. |
| 6 | `asyncIdle` | anything else | append an internal notice (8.4) `message type 0x<two hex digits> arrived from server while idle`; drop the message. |
| 7 | `asyncBusy` | `C` | tag = string. If `!pending()`: `result` = new `CommandOK` result. If `result != nil`: its command status = tag (first 63 bytes). `async = asyncReady`. |
| 8 | `asyncBusy` | `E` | `dropResult()`; `result` = `FatalError` result with the fields, `ErrorQuery` = `cmdQuery` when the fields contain `P` and `cmdQuery` is not empty, message = `FormatError(...)` with the connection's verbosity and visibility; append that message to `errMsg`. `async = asyncReady`. |
| 9 | `asyncBusy` | `Z` | `xact` = decoded status; `cmdActive = false`; `cmd = cmdNone`; `cmdQuery = ""`; `async = asyncIdle`. |
| 10 | `asyncBusy` | `I` | if `!pending()`: `result` = new `EmptyQuery` result. `async = asyncReady`. |
| 11 | `asyncBusy` | `1` | if `cmd == cmdPrepare`: if `!pending()`, `result` = new `CommandOK` result; `async = asyncReady`. Otherwise ignore. |
| 12 | `asyncBusy` | `2` | ignore. |
| 13 | `asyncBusy` | `3` | if `cmd == cmdClose`: as row 11. Otherwise ignore. |
| 14 | `asyncBusy` | `S` | 7.3. |
| 15 | `asyncBusy` | `T` | If `errorResult` or `result` is a `FatalError` result: ignore. Else if `result == nil` or `cmd == cmdDescribe`: decode the fields; if `cmd == cmdDescribe`: put them into `result` (created as `CommandOK` if nil) and `async = asyncReady`; otherwise `result` = new `TuplesOK` result with the fields. Else (a result is pending): `async = asyncReady` and stop, leaving the message. |
| 16 | `asyncBusy` | `n` | if `cmd == cmdDescribe`: if `!pending()`, `result` = new `CommandOK` result; `async = asyncReady`. Otherwise ignore. |
| 17 | `asyncBusy` | `t` | `result` = new `CommandOK` result with the parameter types. |
| 18 | `asyncBusy` | `D` | If `result` has status `TuplesOK` or `TuplesChunk`: `addRow` (7.7). Else if `errorResult` or `result` is `FatalError`: ignore. Else: `appendError("server sent data (\"D\" message) without prior row description (\"T\" message)")`, `saveErrorResult()`. |
| 19 | `asyncBusy` | `G` | `result` = new `CopyIn` result (binary flag and one field per column carrying only its format); `async = asyncCopyIn`. |
| 20 | `asyncBusy` | `H` | the same with `CopyOut`; `async = asyncCopyOut`. |
| 21 | `asyncBusy` | `d`, `c` | ignore (COPY OUT data being skipped, row `ExecStart` of 7.2). |
| 22 | `asyncBusy` | anything else | `appendError("unexpected response from server; first received character was \"<tag>\"")`, `saveErrorResult()`, `async = asyncReady`. |

A decoder error in any row is handled as:
`appendError("message contents do not agree with length in message type \"<tag>\"")`,
`saveErrorResult()`, `async = asyncReady`, message dropped.

A new result takes `ClientEncoding` from `clientEnc`. The binary flag of a result with
fields is true iff it has at least one field and every field has format 1; for COPY
results it is the overall format byte.

During session start (`cmd == cmdStartup`) the same table applies: `S` messages fill
`params`, `Z` makes the connection idle.

### 7.5 `GetCopyData`

Loop over `rbuf`; when it is empty, `n, dead := pull()`; if `n == 0`: when `dead`,
`connectionLost()` and return `nil, -2`; otherwise return `nil, 0`. For the next
message:

| Message | Action |
|---|---|
| `A`, `N`, `S` | as rows 1, 2 and 7.3; continue. |
| `d` | remove it; if the body is empty continue; return `body, len(body)`. |
| `c` | remove it; `async = asyncBusy`; return `nil, -1`. |
| anything else | leave it; `async = asyncBusy`; return `nil, -1`. |

After -1 the application calls `GetResult` for the final result of the command.

A COPY started through the extended protocol (`SendQueryParams`) needs the extra `S`
that `PutCopyEnd` sends, because the engine ignores the command's own Sync while it
reads COPY data. If such a COPY fails before the engine consumed that first Sync, two
ReadyForQuery messages arrive; the second is reported by row 6 of 7.4 as the internal
notice `message type 0x5a arrived from server while idle`. libpq behaves the same way;
it is not an error.

### 7.6 Results (`result.go`)

`Result` holds: status; fields; parameter types; command status; binary flag; client
encoding; row data and row count (5.3); error fields; message; error query.

- `ErrorMessage()` is the stored message, `""` for results that are not errors or
  notices. `ErrorField` returns the first field with that code.
- `VerboseErrorMessage(v, cv)`: for a result whose status is neither `FatalError` nor
  `NonfatalError`: `PGresult is not an error result\n`. Otherwise `FormatError` of the
  stored fields; a result without fields returns its stored message, or
  `no error message available\n` when that is empty.
- `CmdTuples()`: the row count as text, from the command status. After `INSERT ` skip
  the next token (the OID) and its space, then take the rest; after `SELECT `,
  `DELETE `, `UPDATE ` (7 bytes), `FETCH `, `MERGE ` (6), `MOVE `, `COPY ` (5) take the
  rest. Any other status returns `"", true`. If the rest is empty or contains a
  non-digit, or the `INSERT` tag has no second token, return `"", false`; the C shim
  then emits the internal notice `could not interpret result from server: <status>`.
- `OidValue()`: for a status starting with `INSERT ` followed by digits that end at a
  space or at the end, that number; otherwise 0.
- `FieldNumber(name)`: empty name: -1. If the name contains no `"` and no upper-case
  ASCII letter, compare it as is. Otherwise fold it: outside double quotes ASCII
  letters are lower-cased; quotes are removed; inside quotes `""` stands for one `"`.
  Return the index of the first field whose name equals the folded name, or -1.
- `ResStatus` returns libpq's strings unchanged, because programs compare them:
  `PGRES_EMPTY_QUERY`, `PGRES_COMMAND_OK`, `PGRES_TUPLES_OK`, `PGRES_COPY_OUT`,
  `PGRES_COPY_IN`, `PGRES_BAD_RESPONSE`, `PGRES_NONFATAL_ERROR`, `PGRES_FATAL_ERROR`,
  `PGRES_COPY_BOTH`, `PGRES_SINGLE_TUPLE`, `PGRES_PIPELINE_SYNC`,
  `PGRES_PIPELINE_ABORTED`, `PGRES_TUPLES_CHUNK`; any other value:
  `invalid ExecStatusType code`.

Unexported constructors used by the state machine:

```go
func newResult(status ExecStatus, encoding int) *Result
func (r *Result) setFields(fields []FieldDesc)
func (r *Result) setParamTypes(types []uint32)
func (r *Result) setCmdStatus(tag string)            // keeps at most 63 bytes
func (r *Result) setError(fields []ErrorField, message, query string)
func (r *Result) appendRow(body []byte)              // caller ran CheckDataRow
func (r *Result) cloneMeta(status ExecStatus) *Result // fields, encoding, binary; no rows
```

### 7.7 Rows and row modes (`state.go` `addRow`, `rowmodes.go`)

`addRow(body)`:

1. `CheckDataRow(body, result.NFields())`; on error:
   `appendError("insufficient data in \"D\" message")`, `saveErrorResult()`, return.
2. If `partial && saved == nil`: `saved = result`;
   `result = saved.cloneMeta(SingleTuple or TuplesChunk)` (`SingleTuple` when
   `singleRow`).
3. `result.appendRow(body)`.
4. If `partial && result.NTuples() >= maxChunk`: `async = asyncReadyMore`.

Consequences, which are libpq's observable behavior and must be reproduced exactly:

- Single-row mode: each row is returned as its own `SingleTuple` result; after the
  last row a `TuplesOK` result with no rows and the command status follows.
- Chunked mode with chunk size `n`: full chunks are returned as `TuplesChunk` results
  with `n` rows. When the command completes while a chunk is partly filled, that chunk
  is returned first (row 7 of 7.4 wrote the command status into it, and `GetResult`
  keeps the state `asyncReady` because `result` is non-nil after `prepareResult`),
  and then the `TuplesOK` result with no rows, whose command status is empty in this
  case. When the row count is a multiple of `n`, the final `TuplesOK` result carries
  the command status.
- An error in the middle discards the partly filled result; the `FatalError` result
  follows the rows already returned.

### 7.8 Connection death in every state

| State when the worker ends | What the application observes |
|---|---|
| idle, FATAL was sent | descriptor readable; `ConsumeInput` returns true and moves the `E`; the next parsing call turns it into a notice (row 4); the following `ConsumeInput` returns false with `ConnectionBad` and the `server closed…` text; `Send…` then fails with `no connection to the server`. |
| busy | the FATAL arrives as an error result (row 8) if one was sent; the next `GetResult` gives the connection-loss result (7.2), then nil. |
| ready, ready-more | the pending result is returned normally; afterwards as busy. |
| copy-in | `PutCopyData` / `PutCopyEnd` return -1 once the worker is gone; `GetResult` returns a `FatalError` result because `status != ConnectionOK`. Until the application calls one of them it sees nothing; the descriptor is readable. |
| copy-out | `GetCopyData` returns the remaining rows, then -2. |
| closing (`Close`) | nothing is reported; the object is unusable afterwards. |

## 8. Error and notice text (`errtext.go`)

### 8.1 Layout rules of `FormatError`

Input: the fields in arrival order (the first field with a code counts), the query text
kept for position display, the client encoding, whether the message is an error, the
verbosity and the context visibility. Output, built in this order:

1. If field `S` exists: `<S>:` followed by two spaces.
2. Verbosity `ErrorsSQLState`: if field `C` exists, append it and a newline and stop.
   Without `C`, continue as `ErrorsTerse`.
3. Verbosity `ErrorsVerbose`: if `C` exists, append `<C>: ` (one space).
4. Append field `M` if it exists.
5. Position. If `P` exists: when the verbosity is not terse and the query text is not
   empty, remember (query, `P`) for step 7; otherwise append ` at character <P>`.
   Else if `p` exists: when the verbosity is not terse and field `q` exists, remember
   (`q`, `p`); otherwise append ` at character <p>`.
6. Append a newline.
7. Unless terse, in this order: the position display of 8.2 when something was
   remembered and its number is greater than 0; `DETAIL:  <D>`; `HINT:  <H>`;
   `QUERY:  <q>`; `CONTEXT:  <W>`, the last only when the visibility is
   `ShowContextAlways`, or `ShowContextErrors` and the message is an error. Each label
   is followed by two spaces, each line ends with a newline, each appears only when its
   field exists.
8. Verbose only: `SCHEMA NAME:  <s>`, `TABLE NAME:  <t>`, `COLUMN NAME:  <c>`,
   `DATATYPE NAME:  <d>`, `CONSTRAINT NAME:  <n>`, each on its own line when present.
   Then, if any of `R`, `F`, `L` exists: `LOCATION:  `, then `<R>, ` when `R` exists,
   then `<F>:<L>` when both exist, then a newline.

A position is read as its leading decimal digits; text without leading digits is 0.

### 8.2 Position display

Given a text and a 1-based character position `pos`:

1. `loc = pos - 1`. Split the text into characters: in UTF8, runes (an invalid byte is
   one character); in a single-byte encoding, bytes; in any other encoding,
   `pgencoding18.Length` bytes per character, at least 1 and at most the rest.
2. Each character has a display width: 0 for Unicode combining marks (categories Mn
   and Me) in UTF8, 2 for characters of the Han, Hangul, Hiragana and Katakana scripts
   and for U+FF00 to U+FF60 and U+FFE0 to U+FFE6 in UTF8, otherwise 1. This is an
   approximation of PostgreSQL's width table; exact parity outside these classes is
   not required.
3. Find the line that contains `loc`. Lines end at `\n` or `\r`; `\r\n` counts as one
   line end. The line number is 1 plus the number of line ends before `loc`. If `loc`
   is greater than the number of characters, display nothing.
4. In the line, replace each tab by one space.
5. If the line is wider than 60 columns, shorten it. Let `w(i)` be the column of
   character `i` within the text, `b` and `e` the first character and the end of the
   line. If `w(b) + 60 >= w(loc) + 10`: move `e` left until `w(e) - w(b) <= 60` and
   mark the end as cut. Otherwise: move `e` left while `w(loc) + 10 < w(e)`, marking
   the end as cut if it moved; then move `b` right until `w(e) - w(b) <= 60`, marking
   the start as cut.
6. Emit `LINE <n>: `, then `...` if the start was cut, the line from `b` to `e`, `...`
   if the end was cut, a newline. Then a line of spaces as wide as the prefix
   (`LINE <n>: ` plus the leading `...`) plus `w(loc) - w(b)`, a `^`, and a newline.

### 8.3 Worked examples

Query `SELECT * FORM t`; fields `S=ERROR`, `V=ERROR`, `C=42601`,
`M=syntax error at or near "FORM"`, `P=10`, `F=scan.l`, `L=1244`,
`R=scanner_yyerror`.

Default verbosity:

```
ERROR:  syntax error at or near "FORM"
LINE 1: SELECT * FORM t
                 ^
```

(17 spaces before `^`: 8 for `LINE 1: ` and 9 for the characters before `F`.)

Terse:

```
ERROR:  syntax error at or near "FORM" at character 10
```

Verbose:

```
ERROR:  42601: syntax error at or near "FORM"
LINE 1: SELECT * FORM t
                 ^
LOCATION:  scanner_yyerror, scan.l:1244
```

SQLSTATE only:

```
ERROR:  42601
```

Second line of a query. Query `SELECT 1,\n  nope\nFROM t`, fields `S=ERROR`,
`C=42703`, `M=column "nope" does not exist`, `P=13`, default verbosity:

```
ERROR:  column "nope" does not exist
LINE 2:   nope
          ^
```

Detail and names. Fields `S=ERROR`, `C=23505`,
`M=duplicate key value violates unique constraint "t_pkey"`,
`D=Key (id)=(1) already exists.`, `s=public`, `t=t`, `n=t_pkey`. Default:

```
ERROR:  duplicate key value violates unique constraint "t_pkey"
DETAIL:  Key (id)=(1) already exists.
```

Verbose:

```
ERROR:  23505: duplicate key value violates unique constraint "t_pkey"
DETAIL:  Key (id)=(1) already exists.
SCHEMA NAME:  public
TABLE NAME:  t
CONSTRAINT NAME:  t_pkey
```

Context. Fields `S=NOTICE`, `C=00000`, `M=hello`,
`W=PL/pgSQL function f() line 3 at RAISE`, as a notice. With `ShowContextErrors` (the
default) or `ShowContextNever`:

```
NOTICE:  hello
```

With `ShowContextAlways`:

```
NOTICE:  hello
CONTEXT:  PL/pgSQL function f() line 3 at RAISE
```

The same fields with `S=ERROR` as an error show the `CONTEXT` line under
`ShowContextErrors` and hide it under `ShowContextNever`.

Internal query. Fields `S=ERROR`, `C=42703`, `M=column "nope" does not exist`, `p=8`,
`q=SELECT nope`, `W=PL/pgSQL function f() line 3 at SQL statement`, as an error,
default settings:

```
ERROR:  column "nope" does not exist
LINE 1: SELECT nope
               ^
QUERY:  SELECT nope
CONTEXT:  PL/pgSQL function f() line 3 at SQL statement
```

Long lines. The text is 100 times the letter `a`; `<60a>` stands for 60 of them.

| `P` | Line | Spaces before `^` |
|---|---|---|
| 5 | `LINE 1: <60a>...` | 12 |
| 52 | `LINE 1: ...<60a>...` | 61 |
| 95 | `LINE 1: ...<60a>` | 65 |

### 8.4 Notices

- A `N` message (and an `E` message that arrives while idle) becomes a `NonfatalError`
  result with the fields, the error query set as for errors (row 8 of 7.4), and the
  message `FormatError(..., isError=false, ...)` with the connection's settings.
- An internal notice is a `NonfatalError` result with the fields `M=<text>`,
  `S=NOTICE`, `V=NOTICE` and the message `<text>\n` (not formatted by 8.1).
- The session-start notice returned by `pgsession.Start` is converted with
  `pgsession.DiagnosticBody(notice, "NOTICE")`, decoded, and treated as a `N` message.

### 8.5 Client-side messages

Texts appended to `ErrorMessage` get a newline after them. All are libpq's texts unless
marked libspq.

| Text | When |
|---|---|
| `no connection to the server` | a `Send…` call while `Status()` is not `ConnectionOK` |
| `another command is already in progress` | a `Send…` call while not idle |
| `command string is a null pointer` | nil query (7.2) |
| `statement name is a null pointer` | nil statement name (7.2) |
| `number of parameters must be between 0 and 65535` | parameter count out of range |
| `length must be given for binary parameter` | `Param.LengthMissing` |
| `no COPY in progress` | COPY calls outside the matching COPY state |
| `COPY terminated by new PQexec` | not an error text: the CopyFail message `ExecStart` sends |
| `server closed the connection unexpectedly\n\tThis probably means the server terminated abnormally\n\tbefore or while processing the request.` | the session ended (7.1 `connectionLost`) |
| `server sent data ("D" message) without prior row description ("T" message)` | row 18 of 7.4 |
| `unexpected response from server; first received character was "<c>"` | row 22 of 7.4 |
| `message contents do not agree with length in message type "<c>"` | a decoder failed (7.4) |
| `insufficient data in "D" message` | a DataRow does not match the result (7.7) |
| `no error text available` | `prepareResult` without any error text |
| `incomplete multibyte character`, `invalid multibyte character` | escaping (9.1) |
| `missing "=" after "<k>" in connection info string` and the other texts of 3.1 to 3.3 | option parsing |
| `invalid port number: "<v>"` | 3.5 |
| `connection to server at "<host>", port <port> failed: <text>` | 4.5 (libpq's wording without the address part) |
| `PGresult is not an error result\n`, `no error message available\n` | `VerboseErrorMessage` (7.6) |
| `invalid ExecStatusType code` | `ResStatus` |
| `message type 0x<hh> arrived from server while idle` | internal notice, row 6 of 7.4 |
| `could not interpret result from server: <status>` | internal notice emitted by the C shim (7.6) |
| `libspq: internal error: <panic>` | libspq, 6.6 |
| `libspq is supported on Linux only` | libspq, 6.2 |
| `cluster "<name>" has <n> open connections` | libspq, 4.6 |
| `backend identity exhausted` | libspq, 4.3 |
| `cannot reset, dump or restore the database while a command is in progress` | libspq, 10 |
| `cannot reset or restore the database inside a transaction block` | libspq, 10 |

Messages the C shim produces without entering Go (for a NULL connection or result, row
or column numbers out of range) are specified in `LIBSPQ-CABI.md`.

## 9. Escaping and encoding

### 9.1 String escaping (`escape.go`)

Input ends at the first zero byte or at the given length, whichever comes first.
"Multibyte check" for a byte with the high bit set at offset `i`:
`n := pgencoding18.Length(encoding, rest)`; if `n` exceeds the bytes left the character
is *incomplete*; else if `pgencoding18.Verify(encoding, rest[:n]) == -1` it is
*invalid*; else it is a valid character of `n` bytes. (In single-byte encodings `n` is
1 and every byte is valid.)

| Function | Rule |
|---|---|
| `EscapeString(encoding, stdStrings, from)` | Bytes below 0x80: `'` is doubled; `\` is doubled only when `!stdStrings`; others are copied. Valid multibyte characters are copied whole. For an incomplete or invalid character: write the two-byte marker (0xC0 0x20 in UTF8, 0x8D 0x20 otherwise), skip one input byte, and remember the first such problem; the function still returns the whole output, plus the error `incomplete multibyte character` or `invalid multibyte character`. |
| `EscapeLiteral(encoding, s)` | First validate: the first incomplete character fails with `incomplete multibyte character`; any invalid character fails with `invalid multibyte character`; on failure nothing is returned. Output: if `s` contains a backslash, a space and `E`; then `'`; the bytes with each `'` and each `\` doubled; then `'`. |
| `EscapeIdentifier(encoding, s)` | Same validation. Output: `"`, the bytes with each `"` doubled (backslashes are not special), `"`. |
| `EscapeBytea(stdStrings, hex, from)` | `hex`: `\x` (or `\\x` when `!stdStrings`) followed by two lower-case hexadecimal digits per byte. Otherwise per byte: below 0x20 or above 0x7E: `\ooo` three octal digits (`\\ooo` when `!stdStrings`); `'`: `''`; `\`: `\\` (`\\\\` when `!stdStrings`); others copied. |
| `UnescapeBytea(text)` | nil input: nil. Text starting with `\x`: read pairs of hexadecimal digits after it; a character that is not a hexadecimal digit in the first position of a pair is skipped; a pair whose second character is not a hexadecimal digit produces nothing. Otherwise: `\\` gives one backslash; `\` followed by three octal digits (the first 0 to 3) gives that byte; a backslash followed by anything else is dropped and the following bytes are read normally; every other byte is copied. |

### 9.2 Connection-bound forms (`conn.go`)

Each first calls `clearError()`.

| Method | Uses | On error |
|---|---|---|
| `EscapeStringConn(from)` | `EscapeString(clientEnc, stdStrings, from)` | `appendError(text)`; returns the output and `false` |
| `EscapeLiteral(s)`, `EscapeIdentifier(s)` | `EscapeLiteral/EscapeIdentifier(clientEnc, s)` | `appendError(text)`; returns `nil, false` |
| `EscapeByteaConn(from)` | `EscapeBytea(stdStrings, true, from)` | none |

### 9.3 Forms without a connection (`conn.go`)

Two process-wide atomics, `staticEncoding` (initially 0, SQL_ASCII) and
`staticStdStrings` (initially false), are overwritten by every connection that
processes a `client_encoding` or `standard_conforming_strings` report (7.3).
`EscapeStringStatic(from)` is `EscapeString(staticEncoding, staticStdStrings, from)`
with the error ignored; `EscapeByteaStatic(from)` is
`EscapeBytea(staticStdStrings, false, from)`.

### 9.4 Encodings (`encoding.go`, `clientenc.go`)

`EncodingToChar(id)` is `sqltype.EncodingName(id)`; `CharToEncoding(name)` is
`sqltype.EncodingID(name)`. **(measured)** They implement libpq's table: 42 ids from 0
(`SQL_ASCII`) to 41 (`SHIFT_JIS_2004`) with `UTF8` = 6 and `LATIN1` = 8; aliases
(`utf-8`, `Unicode`, `iso_8859-1`, `win1252`) resolve; unknown names give -1 and
invalid ids the empty string.

`Conn.ClientEncoding()` is `clientEnc`, or -1 when `Status()` is not `ConnectionOK`.

`ClientEncodingStatement(encoding)`: `auto` is replaced by `UTF8`; if the name is 101
bytes or longer return `"", false`; otherwise return
`set client_encoding to '<encoding>'` (the name is inserted as given). The C shim runs
it with the `SPQexec` sequence; the new encoding becomes visible through the
`client_encoding` report (7.3).

## 10. Control functions (`control.go`, `dump.go`)

`ResetDatabase`, `Dump` and `Restore` run on the calling goroutine against
`core.EngineOf(l.es)`, the engine of the connected database, while the worker waits
for commands. Each starts with `clearError()` and requires `Status() ==
ConnectionOK` (else `no connection to the server`), `async == asyncIdle` (else
`cannot reset, dump or restore the database while a command is in progress`) and,
for `ResetDatabase` and `Restore`, `xact == TransIdle` (else
`cannot reset or restore the database inside a transaction block`). An engine error is
reported by appending `FormatError` of `pgsession.Diagnostic(err)` (default
verbosity; the text already ends with a newline) to `ErrorMessage`; the function then
returns false.

| Function | Engine call |
|---|---|
| `ResetDatabase()` | `engine.Reset(context.Background(), core.EngineOf(l.es))` |
| `Dump()` | `engine.Dump(context.Background(), core.EngineOf(l.es), &buf)`; returns the bytes |
| `Restore(dump)` | `engine.Restore(context.Background(), core.EngineOf(l.es), bytes.NewReader(dump))` |
| `DropCluster(host, port)` | 4.6 |

**(measured)** What `engine.Reset` does to a database with other sessions open:

- It returns at once, also when another session is inside a transaction.
- Tables and other persistent objects of that database are gone for every session.
- Roles (they belong to the cluster), other databases, and the temporary tables and
  prepared statements of open sessions remain.
- A session that was inside a transaction fails its next statements with
  `stuntgres: catalog replaced before publication` (no SQLSTATE of its own, so
  `XX000`) until it rolls back; after `ROLLBACK` it works normally.
- `core.EngineOf` of an open session is the same engine before and after.

**(measured)** `engine.Dump` produces a script (`SET SESSION AUTHORIZATION …;
CREATE TABLE …; INSERT …`); `engine.Restore` replaces the database content with it and
other open sessions see the restored content; restoring invalid input fails with the
parser's error (`42601`) and leaves the content as it was.

## 11. Test strategy

- Test names start with `TestSPQ`, so `make check RUN='^TestSPQ'` selects the package.
- **Pure parts** (conninfo, messages, error text, escaping, encoding, result): table
  driven tests whose expected values are written out literally from this document's
  rules and examples, never computed by the code under test.
- **State machine without an engine**: tests build a `Conn` literal, append backend
  messages to `rbuf` with the test encoders of `backend_test.go`
  (`tRowDescription`, `tDataRow`, `tCommandComplete`, `tError`, `tNotice`,
  `tParameterStatus`, `tReady`, `tNotification`, `tParameterDescription`,
  `tCopyResponse`), and call `parse`/`GetResult`. Every row of table 7.4 has at least
  one case.
- **Engine-backed tests** (transport onward) use `helpers_linux_test.go`:
  `testHost(t)` returns a cluster host name unique to the test and registers a
  cleanup that closes its connections and drops the cluster; `openTest(t)` connects
  with `host=<testHost> dbname=app user=alice` and fails the test unless the status is
  `ConnectionOK`; `waitReadable(t, c)` waits up to ten seconds for the descriptor with
  `syscall.Select`; `collect(t, c)` gathers results with `GetResult`, waiting on
  wouldBlock; `exec(t, c, sql)` runs the `ExecStart` / `SendQuery` / `ExecFinish`
  sequence. No test sleeps to synchronize; waiting is always on the descriptor.
- SQL in these tests exists only to produce protocol shapes: `SELECT 1`,
  `SELECT generate_series(1,n)`, `SELECT pg_sleep(30)`, `CREATE TABLE`, `INSERT`,
  `COPY … FROM STDIN` / `TO STDOUT`, `LISTEN` / `NOTIFY`, `SET`, `BEGIN` /
  `ROLLBACK`, `DO $$ BEGIN RAISE NOTICE … END $$`, `pg_terminate_backend`. Assertions
  are about the client: statuses, ordering, counts, error fields, state. SQL behavior
  is the business of the reference tests.
- **Race detector**: parts 07A, 07, 08, 11, 13 and 16 run
  `make check RACE=1 RUN='<their selector>'`.
- **Leaks**: `TestSPQTransportClose` and `TestSPQClusterDrop` compare
  `runtime.NumGoroutine()` before and after (after joining what `Close` joins), and
  the cluster test checks collection with `runtime.AddCleanup` and `runtime.GC()` in a
  bounded loop.

## 12. Steps

Each step is one catalog part. "Owns" lists the files the part creates; a file marked
(stub) is created with the minimal body given and is replaced by the part that owns it
later. No part edits a file it does not own.

| Part | Owns | Implements | Prerequisites | Size (code + tests) |
|---|---|---|---|---|
| `LIBSPQ-CLIENT-01A-OPTIONS` | `doc.go`, `conninfo.go`, `conninfo_test.go`; `uri.go` (stub) | 2.1 `Version`, `LibVersion`; `Option`; 3.1, 3.3, 3.4; `DefaultOptions`, `ParseConninfo`, `ParseParams` | design | 300 + 250 |
| `LIBSPQ-CLIENT-01-CONNINFO` | `uri.go`, `resolve.go`, `uri_test.go`, `resolve_test.go` | 3.2, 3.5, 3.6; `target`, `resolve`, `StartupSettings` | 01A | 300 + 300 |
| `LIBSPQ-CLIENT-02-MESSAGES` | `types.go`, `messages.go`, `messages_test.go`, `backend_test.go` | 2.1 enums and field codes, 2.2 types except `Option`, section 5 | design | 350 + 250 |
| `LIBSPQ-CLIENT-03-ERRTEXT` | `errtext.go`, `errtext_test.go` | section 8.1 to 8.3, `FormatError` | 02 | 250 + 250 |
| `LIBSPQ-CLIENT-04-ESCAPE` | `escape.go`, `escape_test.go` | 9.1 | design | 220 + 220 |
| `LIBSPQ-CLIENT-05-ENCODING` | `encoding.go`, `encoding_test.go` | `EncodingToChar`, `CharToEncoding` | design | 20 + 60 |
| `LIBSPQ-CLIENT-07A-INBOX` | `inbox.go`, `wake_linux.go`, `wake_other.go`, `inbox_linux_test.go` | 6.2, `message` | design | 180 + 200 |
| `LIBSPQ-CLIENT-08A-RESULT` | `result.go`, `result_test.go` | 2.6, 5.3, 7.6 | 02, 03 | 300 + 250 |
| `LIBSPQ-CLIENT-06-CLUSTER` | `cluster.go`, `cluster_test.go` | section 4 (`dropCluster` unexported) | LIBSPQ-PGSESSION-19-DOCS, 01, 02 | 300 + 250 |
| `LIBSPQ-CLIENT-07-TRANSPORT` | `conn.go`, `transport.go`, `transport_linux_test.go`, `helpers_linux_test.go`; `state.go`, `notices.go`, `cancel.go` (stubs) | 6.1, 6.3 to 6.7, `Connect`, `Close`, the accessors and escape methods assigned to `conn.go` in section 2, `clearError`, `appendError`, `recoverCall`, 9.2, 9.3 | 06, 07A, 08A, 04 | 450 + 300 |
| `LIBSPQ-CLIENT-08B-STATE` | `state.go`, `state_test.go` | 7.1, 7.3, 7.4, 7.7 `addRow` | 07, 03, 05 | 350 + 400 |
| `LIBSPQ-CLIENT-08-SIMPLE` | `simple.go`, `simple_linux_test.go`; `copy.go` (stub) | the functions assigned to `simple.go` in section 2; 7.2 for them | 08B | 250 + 350 |
| `LIBSPQ-CLIENT-09-EXTENDED` | `extended.go`, `extended_linux_test.go` | the eight `Send…` functions of `extended.go`; 7.2 | 08 | 200 + 300 |
| `LIBSPQ-CLIENT-10-NOTICES` | `notices.go`, `notices_linux_test.go` | 8.4, `TakeNotices`, `Notifies` | 08 | 120 + 220 |
| `LIBSPQ-CLIENT-11-COPY` | `copy.go`, `copy_linux_test.go` | `PutCopyData`, `PutCopyEnd`, `GetCopyData`; 6.3, 7.2, 7.5 | 09 | 150 + 300 |
| `LIBSPQ-CLIENT-12-ROWMODES` | `rowmodes.go`, `rowmodes_linux_test.go` | `SetSingleRowMode`, `SetChunkedRowsMode`; 7.7 | 11 | 40 + 250 |
| `LIBSPQ-CLIENT-13-CANCEL` | `cancel.go`, `cancel_linux_test.go` | 6.8 | 08 | 60 + 180 |
| `LIBSPQ-CLIENT-14-CONNECT` | `connect.go`, `connect_linux_test.go` | `ConnectParams`, `ConnectStart`, `ConnectStartParams`, `ConnectPoll`, `Ping`, `PingParams`, `Reset` | 08 | 150 + 220 |
| `LIBSPQ-CLIENT-15-SETENCODING` | `clientenc.go`, `clientenc_linux_test.go` | `ClientEncoding`, `ClientEncodingStatement`; 9.4 | 08, 05 | 40 + 120 |
| `LIBSPQ-CLIENT-16-CONTROL` | `control.go`, `control_linux_test.go` | `DropCluster`, `ResetDatabase`; section 10 | 08 | 80 + 180 |
| `LIBSPQ-CLIENT-17-DUMP` | `dump.go`, `dump_linux_test.go` | `Dump`, `Restore`; section 10 | 16 | 60 + 140 |

The parts with a letter (01A, 07A, 08A, 08B) split steps that would have been too
large. The identifiers named by other part families (`…-01-CONNINFO`, `…-07-TRANSPORT`,
`…-08-SIMPLE`) are the last piece of their chain, so a prerequisite on them still means
"the whole step is done".

`LIBSPQ-CLIENT-08B-STATE` is the largest part (the 22-row table of 7.4 must be written
as one function); a stronger model is advisable for it. Everything it needs is in 7.1,
7.3, 7.4 and 7.7.

### 12.1 Stubs

| File | Created by | Stub content | Replaced by |
|---|---|---|---|
| `uri.go` | 01A | `func parseURI(uri string) ([]Option, error)` returning the error `URI connection strings are not supported yet` | 01 |
| `state.go` | 07 | `func (c *Conn) parse()`: for each message in `rbuf`: `S`: `params[name] = value`; `Z`: `xact` from the body, `cmdActive = false`, `cmd = cmdNone`, `async = asyncIdle`; anything else: dropped. Also `func (c *Conn) pull() (int, bool)` as in 7.1. | 08B |
| `notices.go` | 07 | `func (c *Conn) noticeArrived(fields []ErrorField) {}`, `func (c *Conn) notificationArrived(n Notification) {}`, `func (c *Conn) internalNotice(text string) {}` | 10 |
| `cancel.go` | 07 | `cancelLoop` of 6.8 without the `slot.Cancel` call; `CancelFD` complete | 13 |
| `copy.go` | 08 | `PutCopyData` and `PutCopyEnd` that `appendError("no COPY in progress")` and return -1; `GetCopyData` that does the same and returns `nil, -2` | 11 |

Rows 1, 2, 4 and 6 of table 7.4 call `notificationArrived`, `noticeArrived` and
`internalNotice`; `startSession` step 5 calls `noticeArrived` with the decoded startup
notice. Part 10 implements them (append to `c.notifies` / `c.notices`).

### 12.2 Tests per part

| Part | Tests (exact names) and what each must contain |
|---|---|
| 01A | `TestSPQConninfoKeywordValue`: every rule of 3.1 with literal expectations, both error texts, `requiressl`, later-wins. `TestSPQConninfoParams`: empty values skipped, unknown keyword, `expandDbname` with a keyword string and precedence in both directions, a plain `dbname` untouched. `TestSPQConninfoOptionTable`: 50 options in the order of 3.4; every row's label, display character and size; `DefaultOptions` values per 3.5; `EnvVar` nil exactly where the table has `-`. |
| 01 | `TestSPQConninfoURI`: user and password, several hosts and ports, IPv6, database, query pairs, `ssl=true`, percent decoding, and every error text of 3.2. `TestSPQConninfoResolve`: each row of 3.5 including clipping at a UTF-8 boundary and the port error. `TestSPQStartupSettings`: the three token forms, escapes, override order, the four errors of 3.6. |
| 02 | `TestSPQMessagesFrontend`: each builder against literal byte strings, parameters null/text/binary, format-count rule. `TestSPQMessagesBackend`: each decoder on valid bodies, short bodies and trailing bytes; sign extension. `TestSPQEnumValues`: every constant of 2.1 against its number. |
| 03 | `TestSPQErrTextExamples`: every example of 8.3 literally, all four verbosities and three visibilities. `TestSPQErrTextPosition`: 8.2 rules: `\r\n`, tab, position beyond the text, position 0, the three long-line rows, one combining mark, one Han character, a LATIN1 text. |
| 04 | `TestSPQEscapeString`, `TestSPQEscapeLiteral`, `TestSPQEscapeIdentifier`, `TestSPQEscapeBytea`, `TestSPQUnescapeBytea`: each rule of 9.1 with literal expectations, including zero-byte termination, both `stdStrings` values, invalid and incomplete UTF8, the marker bytes, hex and escape bytea round trips. |
| 05 | `TestSPQEncodingNames`: ids 0, 6, 8, 41; aliases; unknown name; invalid ids -1 and 42. |
| 07A | `TestSPQInboxInvariant`: after each of push, take, markDead, reset the descriptor's readability (checked with `syscall.Select`, zero timeout) equals `len(q) > 0 || dead`. `TestSPQInboxBackpressure`: a pusher goroutine blocks at the limit and resumes after take; `setClosing` releases it with an error. `TestSPQInboxConcurrent` (RACE=1): one pusher, one taker, 10,000 messages, none lost, order kept. |
| 08A | `TestSPQResultAccessors`, `TestSPQResultRows` (`RowData` layout, `Value`, nulls, empty values), `TestSPQResultCmdTuples` (every prefix of 7.6, the uninterpretable cases), `TestSPQResultOidValue`, `TestSPQResultFieldNumber` (quotes, case folding, doubled quote), `TestSPQResStatus`, `TestSPQResultVerboseErrorMessage`. |
| 06 | `TestSPQClusterCreate` (known names contain `application_name`, `client_encoding`, `search_path`), `TestSPQClusterAdmit` (missing role and database are created; names with `"` and spaces; existing ones reused; a role without LOGIN fails), `TestSPQClusterAdmitRace` (16 goroutines admit the same new user and database; all succeed), `TestSPQClusterSessionIDs`, `TestSPQClusterDrop` (refused while attached; collected afterwards; goroutine count restored). |
| 07 | `TestSPQTransportConnect` (status, accessors, parameter status map filled, backend pid ≥ 2, `Socket()` ≥ 0 and not readable when idle), `TestSPQTransportConnectFailure` (bad conninfo, bad option value, role without LOGIN: status, message format of 4.5, `Socket()`), `TestSPQTransportClose` (idempotent; while `pg_sleep(30)` runs it returns within the ten-second test limit; goroutines restored; descriptors closed), `TestSPQTransportRawQuery` (put a `Q` message on the outbox, read `T D C Z` from `rbuf` after waiting on the descriptor), `TestSPQConnEscape` (9.2, 9.3). |
| 08B | `TestSPQStateTable`: one subtest per row of 7.4 plus decoder-error handling. `TestSPQStateResults`: `prepareResult` and error accumulation. `TestSPQStateRows`: 7.7 including the three chunk outcomes. `TestSPQStateParameterStatus`: 7.3 including version forms `18.0`, `9.6.3`, `17`. |
| 08 | `TestSPQSimpleQuery` (one result per statement, empty query, error stops the batch, `GetResult` nil exactly once), `TestSPQSimpleSendChecks` (every message of 7.2 for `SendQuery`), `TestSPQSimpleExec` (`ExecStart`/`ExecFinish` last-result rule; discards pending results; accumulated `ErrorMessage`), `TestSPQSimpleStatus` (`TransactionStatus` in every state, `ParameterStatus` after `SET`, `ServerVersion`), `TestSPQSimpleConnectionLoss` (7.2 sequence after `pg_terminate_backend` from a second connection, busy and idle), `TestSPQSimpleBackpressure` (a large result is not fully in memory before the application consumes: the worker is blocked in `push`). |
| 09 | `TestSPQExtendedParams` (text and binary parameters and results, NULL, type OIDs given and inferred), `TestSPQExtendedPrepared` (prepare, execute twice, describe: parameter types and fields with status `CommandOK`, close), `TestSPQExtendedPortal` (describe portal of a cursor), `TestSPQExtendedSendChecks` (every message of 7.2), `TestSPQExtendedError` (an error skips to Sync and the connection is idle and usable afterwards; several statements in one command are rejected by the engine). |
| 10 | `TestSPQNotices` (a `RAISE NOTICE` is handed out by `TakeNotices` as `NonfatalError` with fields and message; order with results; taken once), `TestSPQNotifications` (LISTEN/NOTIFY across two connections: descriptor becomes readable while idle, `Notifies` returns channel, payload and the sender's pid; delivery waits for the end of a transaction), `TestSPQInternalNotice`. |
| 11 | `TestSPQCopyIn` (status `CopyIn` with column formats; data in several pieces; end; final `CommandOK` with `COPY n`), `TestSPQCopyInFail` (`PutCopyEnd` with a message: error result `57014`), `TestSPQCopyOut` (rows, `-1`, final result; `0` when nothing is ready), `TestSPQCopyExtended` (COPY through `SendQueryParams` needs the extra Sync), `TestSPQCopyExecStart` (a new exec during COPY IN and COPY OUT recovers), `TestSPQCopyWrongState`, `TestSPQCopyBackpressure` (RACE=1; more than 64 pieces). |
| 12 | `TestSPQSingleRowMode`, `TestSPQChunkedRowsMode` (the three outcomes of 7.7 with literal row counts and command statuses), `TestSPQRowModeConditions`, `TestSPQRowModeError` (error after some rows). |
| 13 | `TestSPQCancelRunning` (`57014`, connection usable), `TestSPQCancelIdle` (no effect on the next command), `TestSPQCancelFD` (a byte written with `syscall.Write` to `CancelFD()` cancels), `TestSPQCancelAfterClose`. |
| 14 | `TestSPQConnectStartPoll`, `TestSPQConnectParams`, `TestSPQPing` (well-formed: `PingOK` and no cluster created; malformed: `PingNoAttempt`), `TestSPQReset` (new backend pid, same descriptors, session state gone, data kept, pending results dropped). |
| 15 | `TestSPQClientEncoding` (`SET client_encoding` changes `ClientEncoding()`; LATIN1 bytes arrive), `TestSPQClientEncodingStatement` (`auto`, too long). |
| 16 | `TestSPQDropCluster` (4.6 table; a new connection gets an empty cluster), `TestSPQResetDatabase` (the measured effects of section 10; the three precondition errors). |
| 17 | `TestSPQDumpRestore` (round trip into the same and into another cluster; invalid dump: `false`, message, content unchanged; preconditions). |

## 13. Experiments behind this design

Scratch package in a detached worktree of commit `c698641e5f` (removed afterwards), run
with `systemd-run --user --scope -q -p MemoryMax=24G -p MemorySwapMax=0 go test
-count=1 -v -run '<name>' ./bindings/spqscratch/`. The package opened sessions with
`ResolveLogin`, `AdmitDatabase`, `StartSession` and ran statements through
`ExecuteProtocolSimple`. All tests passed; the observations are the statements marked
**(measured)** above:

| Test | Observation |
|---|---|
| missing role, missing database | plain error without SQLSTATE; FATAL `3D000` |
| provisioning | one database `postgres`; 397 setting names; `CREATE ROLE … LOGIN SUPERUSER` and `CREATE DATABASE` through a bootstrap session; duplicates `42710` / `42P04`; connect with `DatabaseSelector{Name, Explicit: true}`; `core.EngineOf(session)` is the database's engine, not the cluster's first engine; session id rules |
| session defaults | `application_name`, `client_encoding`, `search_path`, `datestyle` applied; invalid value `22023`; the settings `default_transaction_read_only`, `in_hot_standby`, `scram_iterations` exist |
| reset, dump, restore | section 10 |
| end while running | `RequestEnd` ends a sleeping statement in 0.3 ms; `pg_terminate_backend` gives `57P01`; a cancel cause gives `57014` and leaves the session live |
| garbage collection | engine collected after its sessions ended; no goroutine left, with or without live sessions |
| encodings | `sqltype.EncodingID` / `EncodingName` values of 9.4 |

Not measured, to be confirmed by the part that first needs it: that `os.NewFile` on the
nonblocking cancel descriptor gives a pollable file whose blocked `Read` returns when
the file is closed (part 07 `TestSPQTransportClose` proves it), and everything about
`internal/pgsession`, which did not exist when this was written (the API of
`LIBSPQ-PGSESSION.md` section 2 is assumed).
