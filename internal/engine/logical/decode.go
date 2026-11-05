package logical

import (
	"context"
	"fmt"
)

// Request is one fresh reorder pass over a coherent immutable history/status
// frontier. Zero limits mean unlimited, as do NULL SQL operands.
type Request struct {
	Records                      []Record
	Restart, Confirmed, Frontier uint64
	UptoLSN                      uint64
	UptoChanges                  int32
	TwoPhase                     bool
	TwoPhaseAt                   uint64
	Plugin                       Plugin
	Options                      Options
	StreamLimit                  int64
	StreamImmediate              bool
}

type Result struct {
	Rows []Row
	End  uint64
}

// Decode checks limits after complete input records. Ordinary commit replay is
// atomic; a streaming record can end the pass after one complete stream block.
func Decode(ctx context.Context, req Request) (Result, error) {
	d := decoder{req: req, out: &Output{ctx: ctx}}
	_, d.twoPhase = req.Plugin.(TwoPhasePlugin)
	d.twoPhase = d.twoPhase && req.TwoPhase
	d.stream, _ = req.Plugin.(StreamingPlugin)
	if !req.Options.Streaming {
		d.stream = nil
	}
	if d.stream != nil {
		if err := d.initialize(); err != nil {
			return Result{}, err
		}
	}
	start := req.Restart
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if start >= req.Frontier {
		return Result{End: req.Frontier}, nil
	}
	var stop uint64
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
	stream   StreamingPlugin
	pending  map[uint32]*buffer
	aborted  map[uint32]bool
	parents  map[uint32]uint32
	warmed   map[uint32]bool
	memory   int64
}

type buffer struct {
	txn           *Transaction
	records       []Record
	bytes         int64
	hasStreamable bool
	streamed      bool
	children      map[uint32]bool
	origin        uint32
}

func (d *decoder) descriptor(r Record) *Txn {
	return &Txn{XID: r.Txn.XID, TopXID: r.Txn.XID, FirstLSN: r.Txn.FirstLSN, FinalLSN: r.LSN, EndLSN: r.End(), Time: r.Time, Origin: r.Origin, GID: r.Txn.GID, PrepareTime: r.Txn.PrepareTime, Transaction: r.Txn}
}

func (d *decoder) record(r Record) error {
	if r.Txn == nil {
		return nil
	}
	if filter, ok := d.req.Plugin.(OriginFilter); ok && r.Origin != 0 && filter.FilterByOrigin(r.Origin) {
		return nil
	}
	if d.stream != nil {
		handled, err := d.event(r)
		if handled || err != nil {
			return err
		}
	}
	// Metadata and staged events have no ordinary output descriptor to build.
	switch r.Kind {
	case RowEvent, CatalogEvent, Abort, SubAbort, SubCommit, StatementEnd, StatementAbort:
		return nil
	}
	if r.LSN < d.req.Confirmed {
		return nil
	}
	txn := d.descriptor(r)
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

func (d *decoder) visible(c *Change) bool {
	return c != nil && (!c.Rewrite || d.req.Options.ReceiveRewrites)
}

func (d *decoder) replay(txn *Txn, prepare bool) error {
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
		if err := d.out.Context().Err(); err != nil {
			return err
		}
		change := &txn.Transaction.Changes[i]
		if !d.visible(change) {
			continue
		}
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

func FormatLSN(lsn uint64) string { return fmt.Sprintf("%X/%X", lsn>>32, uint32(lsn)) }
