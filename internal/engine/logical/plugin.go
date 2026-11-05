package logical

import (
	"context"
	"stuntgres/internal/sqltype"
)

// Option is one name/value pair of the decoding call's options array.
type Option struct{ Name, Value string }

// Startup describes one decoding context: slot creation (Init) or one
// get/peek call. TwoPhase is the slot's two_phase property.
type Startup struct {
	Slot     string
	Options  []Option
	Output   sqltype.OutputEnvironment // Ephemeral decoding-session datum output.
	Init     bool
	TwoPhase bool
}

// Options are the plugin's startup answers. Streaming asks for in-progress
// transactions; ReceiveRewrites asks for table rewrite changes.
type Options struct {
	Binary          bool
	Streaming       bool
	ReceiveRewrites bool
}

// Txn is the transaction a callback reports. FinalLSN is the start of the
// commit, prepare or finish record; EndLSN its end. Time is the matching
// commit, prepare or finish timestamp.
type Txn struct {
	XID         uint32
	FirstLSN    uint64
	FinalLSN    uint64
	EndLSN      uint64
	Time        int64
	Origin      uint32
	GID         string
	PrepareTime int64
	// TopXID identifies the parent of a subtransaction callback.
	TopXID      uint32
	Transaction *Transaction
}

// Row is one output row of a decoding call.
type Row struct {
	LSN  uint64
	XID  uint32
	Data []byte
}

// Output collects rows; the decoder sets the position and transaction of each
// callback, as PostgreSQL's write_location and write_xid.
type Output struct {
	rows []Row
	lsn  uint64
	xid  uint32
	ctx  context.Context
}

// Write emits one row with a private copy of data.
func (o *Output) Write(data []byte) {
	o.rows = append(o.rows, Row{LSN: o.lsn, XID: o.xid, Data: append([]byte(nil), data...)})
}

// Context is the operation's cancellation context, including value formatting.
func (o *Output) Context() context.Context {
	if o.ctx == nil {
		return context.Background()
	}
	return o.ctx
}

// Plugin is one decoding context's output plugin instance. Callbacks run
// without engine locks and may return SQL errors.
type Plugin interface {
	Startup(Startup) (Options, error)
	Begin(*Output, *Txn) error
	// Change receives Insert, Update and Delete changes.
	Change(*Output, *Txn, *Change) error
	Commit(*Output, *Txn) error
	Shutdown()
}

// TruncatePlugin receives TRUNCATE changes; plugins without it skip them, as
// PostgreSQL does without a truncate callback.
type TruncatePlugin interface {
	Truncate(*Output, *Txn, *Change) error
}

// TwoPhasePlugin decodes prepared transactions at PREPARE on slots with
// two_phase enabled.
type TwoPhasePlugin interface {
	BeginPrepare(*Output, *Txn) error
	Prepare(*Output, *Txn) error
	CommitPrepared(*Output, *Txn) error
	RollbackPrepared(*Output, *Txn) error
}

// StreamingPlugin receives complete blocks of genuine staged events. The
// decoder, not the plugin or slot, owns buffering for this one decoding pass.
type StreamingPlugin interface {
	StreamStart(*Output, *Txn) error
	StreamChange(*Output, *Txn, *Change) error
	StreamTruncate(*Output, *Txn, *Change) error
	StreamStop(*Output, *Txn) error
	StreamAbort(*Output, *Txn) error
	StreamCommit(*Output, *Txn) error
	StreamPrepare(*Output, *Txn) error
}

// OriginFilter skips whole transactions by replication origin.
type OriginFilter interface {
	FilterByOrigin(origin uint32) bool
}

// Factory creates a fresh plugin instance for one decoding context.
type Factory func() Plugin

// WriteString emits immutable formatter text without an intermediate byte copy.
func (o *Output) WriteString(data string) {
	o.rows = append(o.rows, Row{LSN: o.lsn, XID: o.xid, Data: []byte(data)})
}
