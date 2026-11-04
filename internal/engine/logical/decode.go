package logical

import (
	"context"
	"fmt"

	"stuntgres/internal/sqlstate"
)

// Request is one get/peek decoding pass over an immutable history snapshot.
// Frontier is the end of the stream when the call started. UptoLSN zero and
// UptoChanges zero mean no limit, as NULL arguments do.
type Request struct {
	Records     []Record
	Restart     uint64
	Confirmed   uint64
	Frontier    uint64
	UptoLSN     uint64
	UptoChanges int32
	TwoPhase    bool
	TwoPhaseAt  uint64 // Where the slot's two-phase decoding starts.
	Plugin      Plugin
	PluginName  string
	Options     Options
	// Streaming boundary: logical_decoding_work_mem in bytes, and
	// debug_logical_replication_streaming=immediate.
	StreamLimit     int64
	StreamImmediate bool
}

// Result holds the produced rows and End, the reader position after the last
// processed record: get confirms it.
type Result struct {
	Rows []Row
	End  uint64
}

// Decode mirrors pg_logical_slot_get_changes_guts: records are read from the
// restart position up to the frontier and the limits are checked after each
// record, so a transaction's rows are never split. A negative change limit
// stops after the first record.
func Decode(ctx context.Context, req Request) (Result, error) {
	if req.Options.ReceiveRewrites {
		return Result{}, sqlError("0A000", "logical decoding output plugin \"%s\" requested table rewrite changes, which are not supported", req.PluginName)
	}
	d := decoder{req: req, out: &Output{}}
	_, d.twoPhase = req.Plugin.(TwoPhasePlugin)
	d.twoPhase = d.twoPhase && req.TwoPhase
	start := req.Restart
	if start >= req.Frontier {
		return Result{End: req.Frontier}, nil
	}
	var stop uint64 // A record end that ends reading whatever was produced.
	if req.UptoLSN != 0 {
		stop = start + Span
		if req.UptoLSN > stop {
			stop = start + (req.UptoLSN-start+Span-1)/Span*Span
		}
	}
	if req.UptoChanges < 0 && (stop == 0 || start+Span < stop) {
		stop = start + Span
	}
	for _, r := range req.Records {
		if r.LSN < start {
			continue
		}
		if r.LSN >= req.Frontier || stop != 0 && r.End() > stop {
			break
		}
		if err := d.record(r); err != nil {
			return Result{}, err
		}
		// CHECK_FOR_INTERRUPTS follows every record.
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if req.UptoChanges > 0 && int(req.UptoChanges) <= len(d.out.rows) || stop == r.End() {
			return Result{Rows: d.out.rows, End: r.End()}, nil
		}
	}
	end := req.Frontier
	if stop != 0 && stop < end {
		end = stop
	}
	return Result{Rows: d.out.rows, End: end}, nil
}

type decoder struct {
	req      Request
	out      *Output
	twoPhase bool
}

func (d *decoder) record(r Record) error {
	if r.LSN < d.req.Confirmed || r.Txn == nil {
		return nil
	}
	if filter, ok := d.req.Plugin.(OriginFilter); ok && r.Origin != 0 && filter.FilterByOrigin(r.Origin) {
		return nil
	}
	txn := &Txn{XID: r.Txn.XID, FirstLSN: r.Txn.FirstLSN, FinalLSN: r.LSN, EndLSN: r.End(), Time: r.Time, Origin: r.Origin, GID: r.Txn.GID, PrepareTime: r.Txn.PrepareTime, Transaction: r.Txn}
	switch r.Kind {
	case Commit:
		return d.replay(txn, false)
	case Prepare:
		if d.twoPhase {
			return d.replay(txn, true)
		}
	case CommitPrepared:
		if !d.twoPhase {
			return d.replay(txn, false)
		}
		// ReorderBufferFinishPrepared: a PREPARE before two_phase_at was never
		// sent, so the transaction is replayed with its prepare first.
		if r.Txn.PrepareLSN < d.req.TwoPhaseAt {
			prepared := *txn
			prepared.FinalLSN, prepared.EndLSN, prepared.Time = r.Txn.PrepareLSN, r.Txn.PrepareLSN+Span, r.Txn.PrepareTime
			if err := d.replay(&prepared, true); err != nil {
				return err
			}
		}
		d.at(r.End(), txn)
		return d.req.Plugin.(TwoPhasePlugin).CommitPrepared(d.out, txn)
	case RollbackPrepared:
		if d.twoPhase {
			d.at(r.End(), txn)
			return d.req.Plugin.(TwoPhasePlugin).RollbackPrepared(d.out, txn)
		}
	}
	return nil
}

func (d *decoder) at(lsn uint64, txn *Txn) { d.out.lsn, d.out.xid = lsn, txn.XID }

// replay sends one whole transaction: begin, its changes in order, then the
// commit (or, for two-phase decoding, the prepare).
func (d *decoder) replay(txn *Txn, prepare bool) error {
	if err := d.streamBoundary(txn.Transaction); err != nil {
		return err
	}
	p := d.req.Plugin
	d.at(txn.FirstLSN, txn)
	var err error
	if prepare {
		err = p.(TwoPhasePlugin).BeginPrepare(d.out, txn)
	} else {
		err = p.Begin(d.out, txn)
	}
	if err != nil {
		return err
	}
	for i := range txn.Transaction.Changes {
		change := &txn.Transaction.Changes[i]
		d.at(change.LSN, txn)
		if change.Kind == Truncate {
			if t, ok := p.(TruncatePlugin); ok {
				err = t.Truncate(d.out, txn, change)
			}
		} else {
			err = p.Change(d.out, txn, change)
		}
		if err != nil {
			return err
		}
	}
	d.at(txn.EndLSN, txn)
	if prepare {
		return p.(TwoPhasePlugin).Prepare(d.out, txn)
	}
	return p.Commit(d.out, txn)
}

// streamBoundary rejects a pass where PostgreSQL would stream an in-progress
// transaction: streaming is not implemented, and buffering such a transaction
// instead would silently change the plugin's output.
func (d *decoder) streamBoundary(t *Transaction) error {
	if !d.req.Options.Streaming || len(t.Changes) == 0 {
		return nil
	}
	exceeds := d.req.StreamImmediate
	if !exceeds && d.req.StreamLimit > 0 {
		var size int64
		for i := range t.Changes {
			size += t.Changes[i].approximateSize()
		}
		exceeds = size > d.req.StreamLimit
	}
	if exceeds {
		return sqlError("0A000", "logical decoding output plugin \"%s\" requires streaming of in-progress transactions, which is not supported", d.req.PluginName)
	}
	return nil
}

func sqlError(code, format string, args ...any) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: fmt.Sprintf(format, args...)}
}

// FormatLSN renders a position as pg_lsn text.
func FormatLSN(lsn uint64) string { return fmt.Sprintf("%X/%X", lsn>>32, uint32(lsn)) }
