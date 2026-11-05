package logical

// initialize snapshots terminal status before replay, like catalog/CLOG checks
// of a fresh decoding context. It reads only the captured immutable frontier.
func (d *decoder) initialize() error {
	d.pending = make(map[uint32]*buffer)
	d.aborted = make(map[uint32]bool)
	d.parents = make(map[uint32]uint32)
	d.warmed = make(map[uint32]bool)
	for _, r := range d.req.Records {
		if err := d.out.Context().Err(); err != nil {
			return err
		}
		if r.LSN >= d.req.Frontier {
			break
		}
		if r.Txn == nil {
			continue
		}
		if r.Child != 0 && r.Child != r.Txn.XID {
			d.parents[r.Child] = r.Parent
		}
		if r.Kind == Abort {
			d.aborted[r.Txn.XID] = true
		}
		if r.Kind == SubAbort {
			d.aborted[r.Child] = true
		}
	}
	return nil
}

func (d *decoder) descendant(child, parent uint32) bool {
	for child != 0 {
		if child == parent {
			return true
		}
		next := d.parents[child]
		if next == child {
			break
		}
		child = next
	}
	return false
}

func (d *decoder) isAborted(child uint32) bool {
	for child != 0 {
		if d.aborted[child] {
			return true
		}
		next := d.parents[child]
		if next == child {
			break
		}
		child = next
	}
	return false
}

func (d *decoder) buffered(r Record) *buffer {
	b := d.pending[r.Txn.XID]
	if b == nil {
		b = &buffer{txn: r.Txn, origin: r.Origin}
		d.pending[r.Txn.XID] = b
	}
	return b
}

func eventSize(r Record) int64 {
	if r.Change != nil {
		return r.Change.DecodingSize()
	}
	return 80
}

func (d *decoder) event(r Record) (bool, error) {
	top := r.Txn.XID
	switch r.Kind {
	case RowEvent, CatalogEvent:
		// Immediate bypasses the buffered terminal precheck, not catalog
		// liveness. A completed top abort cannot open a fresh catalog context;
		// an aborted child can use a relation opened by its parent prefix.
		if d.aborted[top] {
			return true, nil
		}
		child := r.Child
		if child == 0 {
			child = top
		}
		if d.isAborted(child) {
			if !d.req.StreamImmediate || r.Change == nil || r.Change.Relation == nil || !d.relationAvailable(top, r.Change.Relation.OID) {
				return true, nil
			}
		}
		b := d.buffered(r)
		// Like ReorderBufferQueueChange, only real queued row/truncate
		// evidence makes the top transaction streamable. Keep this flag
		// through drains/child aborts; rewrite visibility is decided later.
		// Catalog records still count toward memory and accompany real work.
		if r.Kind == RowEvent && r.Change != nil {
			b.hasStreamable = true
		}
		b.records = append(b.records, r)
		size := eventSize(r)
		b.bytes += size
		d.memory += size
		if r.LSN < d.req.Confirmed {
			return true, nil
		}
		return true, d.schedule(r)
	case StatementEnd, StatementAbort, SubCommit:
		return true, nil
	case SubAbort:
		b := d.pending[top]
		if b == nil {
			return true, nil
		}
		emitted := false
		for child := range b.children {
			if d.descendant(child, r.Child) {
				emitted = true
				delete(b.children, child)
			}
		}
		kept := b.records[:0]
		for _, event := range b.records {
			if d.descendant(event.Child, r.Child) {
				size := eventSize(event)
				b.bytes -= size
				d.memory -= size
			} else {
				kept = append(kept, event)
			}
		}
		clear(b.records[len(kept):])
		b.records = kept
		if emitted && r.LSN >= d.req.Confirmed {
			txn := d.descriptor(r)
			txn.XID = r.Child
			d.at(r.LSN, txn)
			return true, d.stream.StreamAbort(d.out, txn)
		}
		return true, nil
	case Abort:
		return true, d.abortStream(r)
	case Prepare:
		if !d.twoPhase {
			return true, nil
		}
		return d.finishStream(r, true)
	case Commit:
		return d.finishStream(r, false)
	case CommitPrepared:
		if !d.twoPhase {
			return d.finishStream(r, false)
		}
	case RollbackPrepared:
		if !d.twoPhase {
			return true, d.abortStream(r)
		}
	}
	return false, nil
}

func (d *decoder) abortStream(r Record) error {
	b := d.pending[r.Txn.XID]
	if b == nil {
		return nil
	}
	delete(d.pending, r.Txn.XID)
	d.memory -= b.bytes
	if b.streamed && r.LSN >= d.req.Confirmed {
		txn := d.descriptor(r)
		d.at(r.LSN, txn)
		return d.stream.StreamAbort(d.out, txn)
	}
	return nil
}

func (d *decoder) finishStream(r Record, prepare bool) (bool, error) {
	b := d.pending[r.Txn.XID]
	if b == nil {
		return false, nil
	}
	if b.streamed && r.LSN >= d.req.Confirmed {
		if err := d.flush(b, r); err != nil {
			return true, err
		}
		txn := d.descriptor(r)
		d.at(r.End(), txn)
		var err error
		if prepare {
			err = d.stream.StreamPrepare(d.out, txn)
		} else {
			err = d.stream.StreamCommit(d.out, txn)
		}
		delete(d.pending, r.Txn.XID)
		return true, err
	}
	d.memory -= b.bytes
	delete(d.pending, r.Txn.XID)
	return false, nil
}

func (d *decoder) schedule(trigger Record) error {
	for d.memory > 0 && (d.req.StreamImmediate || d.req.StreamLimit > 0 && d.memory >= d.req.StreamLimit) {
		var largest *buffer
		for _, b := range d.pending {
			if !b.hasStreamable || b.bytes == 0 || d.aborted[b.txn.XID] {
				continue
			}
			if largest == nil || b.bytes > largest.bytes || b.bytes == largest.bytes && b.txn.FirstLSN < largest.txn.FirstLSN {
				largest = b
			}
		}
		if largest == nil {
			return nil
		}
		if err := d.flush(largest, trigger); err != nil {
			return err
		}
	}
	return nil
}

func (d *decoder) flush(b *buffer, trigger Record) error {
	if len(b.records) == 0 {
		return nil
	}
	txn := &Txn{XID: b.txn.XID, TopXID: b.txn.XID, FirstLSN: b.txn.FirstLSN, FinalLSN: trigger.LSN, EndLSN: trigger.End(), Origin: b.origin, Transaction: b.txn}
	d.at(b.records[0].LSN, txn)
	if err := d.stream.StreamStart(d.out, txn); err != nil {
		return err
	}
	if b.children == nil {
		b.children = make(map[uint32]bool)
	}
	for _, event := range b.records {
		if err := d.out.Context().Err(); err != nil {
			return err
		}
		d.at(event.LSN, txn)
		if d.visible(event.Change) {
			var err error
			if event.Change.Kind == Truncate {
				err = d.stream.StreamTruncate(d.out, txn, event.Change)
			} else {
				err = d.stream.StreamChange(d.out, txn, event.Change)
			}
			if err != nil {
				return err
			}
			if event.Change.Relation != nil {
				d.warmed[event.Change.Relation.OID] = true
			}
		}
		child := event.Child
		if child == 0 {
			child = txn.XID
		}
		b.children[child] = true
	}
	d.at(b.records[len(b.records)-1].LSN, txn)
	if err := d.stream.StreamStop(d.out, txn); err != nil {
		return err
	}
	b.streamed = true
	d.memory -= b.bytes
	b.bytes = 0
	clear(b.records)
	b.records = b.records[:0]
	return nil
}

func (d *decoder) relationAvailable(top, oid uint32) bool {
	if d.warmed[oid] {
		return true
	}
	if b := d.pending[top]; b != nil {
		for _, r := range b.records {
			if r.Change != nil && r.Change.Relation != nil && r.Change.Relation.OID == oid && !d.isAborted(r.Child) {
				return true
			}
		}
	}
	return false
}
