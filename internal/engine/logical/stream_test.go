package logical

import (
	"context"
	"errors"
	"slices"
	"testing"
)

type streamingCounter struct {
	counter
	blocks  [][]uint32
	active  []uint32
	aborted []uint32
	commits []uint32
	fail    bool
}

func (p *streamingCounter) StreamStart(o *Output, t *Txn) error {
	p.active = nil
	o.Write([]byte("open"))
	return nil
}
func (p *streamingCounter) StreamChange(o *Output, t *Txn, c *Change) error {
	p.active = append(p.active, t.XID)
	o.Write([]byte("change"))
	if p.fail {
		return errors.New("staged callback failed")
	}
	return nil
}
func (p *streamingCounter) StreamTruncate(o *Output, t *Txn, c *Change) error {
	return p.StreamChange(o, t, c)
}
func (p *streamingCounter) StreamStop(o *Output, t *Txn) error {
	p.blocks = append(p.blocks, slices.Clone(p.active))
	o.Write([]byte("close"))
	return nil
}
func (p *streamingCounter) StreamAbort(o *Output, t *Txn) error {
	p.aborted = append(p.aborted, t.XID)
	return nil
}
func (p *streamingCounter) StreamCommit(o *Output, t *Txn) error {
	p.commits = append(p.commits, t.XID)
	o.Write([]byte("commit"))
	return nil
}
func (p *streamingCounter) StreamPrepare(o *Output, t *Txn) error {
	o.Write([]byte("prepare"))
	return nil
}
func appendEvent(h *History, a *Allocator, xid, child, parent uint32, size int64, rewrite bool) *Change {
	lsn := a.Allocate()
	txn := h.Anchor(xid, lsn)
	c := &Change{LSN: lsn, Kind: Insert, Relation: &Relation{OID: 1}, DecodedBytes: size, Rewrite: rewrite}
	h.Append(Record{Kind: RowEvent, LSN: lsn, Txn: txn, Child: child, Parent: parent, Change: c})
	return c
}
func TestLogicalHistoryEventSnapshots(t *testing.T) {
	var h History
	var a Allocator
	c := appendEvent(&h, &a, 10, 10, 10, 80, false)
	held := h.Snapshot()
	anchor := held[0].Txn
	appendEvent(&h, &a, 10, 11, 10, 90, false)
	h.Append(Record{Kind: SubAbort, LSN: a.Allocate(), Txn: anchor, Child: 11, Parent: 10})
	h.Append(Record{Kind: Abort, LSN: a.Allocate(), Txn: anchor})
	h.Trim(a.End())
	h.Reset()
	if len(held) != 1 || held[0].Change != c || held[0].Txn != anchor || anchor.XID != 10 || anchor.FirstLSN != Base || held[0].Kind != RowEvent {
		t.Fatal("published snapshot mutated", held)
	}
	if len(h.Snapshot()) != 0 || h.anchors != nil {
		t.Fatal("reset retained owners")
	}
}
func TestLogicalHistoryStreamingRetention(t *testing.T) {
	var h History
	var a Allocator
	appendEvent(&h, &a, 10, 10, 10, 80, false)
	first := h.Snapshot()[0].Txn
	confirmed := a.End()
	if got := RestartLSN(h.Snapshot(), confirmed); got != Base {
		t.Fatal("open anchor lost", got)
	}
	h.Append(Record{Kind: Prepare, LSN: a.Allocate(), Txn: first})
	if got := RestartLSN(h.Snapshot(), a.End()); got != Base {
		t.Fatal("prepared anchor lost", got)
	}
	held := h.Snapshot()
	h.Append(Record{Kind: CommitPrepared, LSN: a.Allocate(), Txn: first})
	if got := RestartLSN(h.Snapshot(), confirmed); got != Base {
		t.Fatal("lagging consumer lost prefix", got)
	}
	restart := RestartLSN(h.Snapshot(), a.End())
	if restart != a.End() {
		t.Fatal("finished owner retained", restart)
	}
	h.Trim(restart)
	if h.Len() != 0 || len(h.anchors) != 0 {
		t.Fatal("finished payloads retained")
	}
	if held[0].Txn != first || held[0].Kind != RowEvent {
		t.Fatal("trim changed held backing")
	}
}
func TestLogicalDecodeStreamingEvents(t *testing.T) {
	var h History
	var a Allocator
	appendEvent(&h, &a, 10, 10, 10, 80, false)
	appendEvent(&h, &a, 10, 11, 10, 80, false)
	txn := h.Snapshot()[0].Txn
	h.Append(Record{Kind: SubAbort, LSN: a.Allocate(), Txn: txn, Child: 11, Parent: 10})
	appendEvent(&h, &a, 10, 12, 10, 80, false)
	h.Append(Record{Kind: Commit, LSN: a.Allocate(), Txn: txn})
	p := &streamingCounter{}
	req := Request{Records: h.Snapshot(), Restart: Base, Confirmed: Base, Frontier: a.End(), Plugin: p, Options: Options{Streaming: true}, StreamImmediate: true, UptoChanges: 1}
	first, err := Decode(context.Background(), req)
	if err != nil || len(first.Rows) != 3 || first.End != Base+Span {
		t.Fatal("block limit", first, err)
	}
	p = &streamingCounter{}
	req.Plugin = p
	req.Confirmed = first.End
	req.UptoChanges = 0
	_, err = Decode(context.Background(), req)
	if err != nil || len(p.blocks) != 2 || len(p.blocks[0]) != 2 || !slices.Equal(p.aborted, []uint32{11}) || !slices.Equal(p.commits, []uint32{10}) {
		t.Fatal("fresh rebuffer/abort", p, err)
	}
	// Equality at the total logical budget triggers, and selects the largest top.
	var threshold History
	var positions Allocator
	appendEvent(&threshold, &positions, 20, 20, 20, 60, false)
	appendEvent(&threshold, &positions, 21, 21, 21, 40, false)
	p = &streamingCounter{}
	req = Request{Records: threshold.Snapshot(), Restart: Base, Confirmed: Base, Frontier: positions.End(), Plugin: p, Options: Options{Streaming: true}, StreamLimit: 100}
	_, err = Decode(context.Background(), req)
	if err != nil || len(p.blocks) != 1 || !slices.Equal(p.blocks[0], []uint32{20}) {
		t.Fatal("inclusive largest budget", p.blocks, err)
	}
	p = &streamingCounter{fail: true}
	req.Plugin = p
	req.StreamImmediate = true
	if result, err := Decode(context.Background(), req); err == nil || result.End != 0 || len(result.Rows) != 0 {
		t.Fatal("callback failure published result", result, err)
	}
}
func TestLogicalDecodeRewriteFiltering(t *testing.T) {
	var h History
	var a Allocator
	c := appendEvent(&h, &a, 10, 10, 10, 80, true)
	txn := &Transaction{XID: 10, FirstLSN: Base, Changes: []Change{*c}}
	h.Append(Record{Kind: Commit, LSN: a.Allocate(), Txn: txn})
	for _, rewrites := range []bool{false, true} {
		p := &counter{}
		result, err := Decode(context.Background(), Request{Records: h.Snapshot(), Restart: Base, Confirmed: Base, Frontier: a.End(), Plugin: p, Options: Options{ReceiveRewrites: rewrites}})
		want := 2
		if rewrites {
			want = 3
		}
		if err != nil || len(result.Rows) != want {
			t.Fatal(rewrites, result, err)
		}
	}
}

func TestLogicalDecodeCatalogEligibility(t *testing.T) {
	for _, immediate := range []bool{false, true} {
		name := "buffered"
		if immediate {
			name = "immediate"
		}
		t.Run(name, func(t *testing.T) {
			var h History
			var a Allocator
			catalog := a.Allocate()
			txn := h.Anchor(10, catalog)
			h.Append(Record{Kind: CatalogEvent, LSN: catalog, Txn: txn})
			h.Append(Record{Kind: Commit, LSN: a.Allocate(), Txn: txn})
			p := &streamingCounter{}
			req := Request{Records: h.Snapshot(), Restart: Base, Confirmed: Base, Frontier: a.End(), Plugin: p, Options: Options{Streaming: true}, StreamImmediate: immediate, StreamLimit: 1}
			result, err := Decode(context.Background(), req)
			if err != nil || len(result.Rows) != 2 || !slices.Equal(p.seen, []uint32{10}) || len(p.blocks) != 0 || len(p.commits) != 0 {
				t.Fatal("catalog-only work must use ordinary callbacks", result, p, err)
			}
			if result.End != a.End() {
				t.Fatal("catalog-only work did not advance the input frontier", result)
			}
		})
	}
	for _, tc := range []struct {
		name string
		kind ChangeKind
	}{
		{"insert", Insert}, {"update", Update}, {"delete", Delete}, {"truncate", Truncate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var h History
			var a Allocator
			catalog := a.Allocate()
			txn := h.Anchor(10, catalog)
			h.Append(Record{Kind: CatalogEvent, LSN: catalog, Txn: txn})
			row := a.Allocate()
			h.Append(Record{Kind: RowEvent, LSN: row, Txn: txn, Child: 11, Parent: 10, Change: &Change{Kind: tc.kind, LSN: row}})
			h.Append(Record{Kind: CatalogEvent, LSN: a.Allocate(), Txn: txn})
			h.Append(Record{Kind: Commit, LSN: a.Allocate(), Txn: txn})
			p := &streamingCounter{}
			req := Request{Records: h.Snapshot(), Restart: Base, Confirmed: Base, Frontier: a.End(), Plugin: p, Options: Options{Streaming: true}, StreamImmediate: true, UptoLSN: row}
			result, err := Decode(context.Background(), req)
			if err != nil || len(result.Rows) != 0 || result.End != row || len(p.blocks) != 0 {
				t.Fatal("future row must not make the catalog prefix streamable", result, p, err)
			}
			p = &streamingCounter{}
			req.Plugin, req.UptoLSN = p, 0
			result, err = Decode(context.Background(), req)
			// Catalog prefix accompanies the row in the first block. After
			// that drain, genuine catalog work still forms an empty block.
			if err != nil || len(result.Rows) != 6 || len(p.blocks) != 2 || !slices.Equal(p.blocks[0], []uint32{10}) || len(p.blocks[1]) != 0 || !slices.Equal(p.commits, []uint32{10}) || len(p.seen) != 0 {
				t.Fatal("real work must retain subsequent empty catalog blocks", result, p, err)
			}
		})
	}
	t.Run("largest eligible transaction", func(t *testing.T) {
		var h History
		var a Allocator
		catalog := a.Allocate()
		txn := h.Anchor(10, catalog)
		h.Append(Record{Kind: CatalogEvent, LSN: catalog, Txn: txn}) // 80 logical bytes
		appendEvent(&h, &a, 20, 20, 20, 20, false)
		p := &streamingCounter{}
		result, err := Decode(context.Background(), Request{Records: h.Snapshot(), Restart: Base, Confirmed: Base, Frontier: a.End(), Plugin: p, Options: Options{Streaming: true}, StreamLimit: 100})
		// Catalog memory contributes to the inclusive threshold, but its
		// larger catalog-only transaction is not a streaming candidate.
		if err != nil || len(result.Rows) != 3 || len(p.blocks) != 1 || !slices.Equal(p.blocks[0], []uint32{20}) {
			t.Fatal("largest selection ignored streamable evidence", result, p, err)
		}
	})
	t.Run("filtered rewrite still supplies streamable evidence", func(t *testing.T) {
		var h History
		var a Allocator
		appendEvent(&h, &a, 10, 10, 10, 80, true)
		txn := h.Snapshot()[0].Txn
		h.Append(Record{Kind: CatalogEvent, LSN: a.Allocate(), Txn: txn})
		h.Append(Record{Kind: Commit, LSN: a.Allocate(), Txn: txn})
		p := &streamingCounter{}
		result, err := Decode(context.Background(), Request{Records: h.Snapshot(), Restart: Base, Confirmed: Base, Frontier: a.End(), Plugin: p, Options: Options{Streaming: true}, StreamImmediate: true})
		// Rewrite filtering is downstream of queue eligibility. Both blocks
		// reach the plugin even though neither has a visible row callback.
		if err != nil || len(result.Rows) != 5 || len(p.blocks) != 2 || len(p.blocks[0]) != 0 || len(p.blocks[1]) != 0 || !slices.Equal(p.commits, []uint32{10}) {
			t.Fatal("visibility was confused with queue eligibility", result, p, err)
		}
	})
	t.Run("child abort retains top eligibility", func(t *testing.T) {
		var h History
		var a Allocator
		// A committed relation prefix permits immediate catalog access to
		// the later aborted child, just as in the existing abort proof.
		appendEvent(&h, &a, 20, 20, 20, 80, false)
		h.Append(Record{Kind: Commit, LSN: a.Allocate(), Txn: h.Snapshot()[0].Txn})
		appendEvent(&h, &a, 10, 11, 10, 80, false)
		txn := h.Snapshot()[2].Txn
		h.Append(Record{Kind: SubAbort, LSN: a.Allocate(), Txn: txn, Child: 11, Parent: 10})
		h.Append(Record{Kind: CatalogEvent, LSN: a.Allocate(), Txn: txn})
		h.Append(Record{Kind: Commit, LSN: a.Allocate(), Txn: txn})
		p := &streamingCounter{}
		result, err := Decode(context.Background(), Request{Records: h.Snapshot(), Restart: Base, Confirmed: Base, Frontier: a.End(), Plugin: p, Options: Options{Streaming: true}, StreamImmediate: true})
		if err != nil || len(result.Rows) != 10 || len(p.blocks) != 3 || !slices.Equal(p.blocks[0], []uint32{20}) || !slices.Equal(p.blocks[1], []uint32{10}) || len(p.blocks[2]) != 0 || !slices.Equal(p.aborted, []uint32{11}) || !slices.Equal(p.commits, []uint32{20, 10}) {
			t.Fatal("child rollback discarded top streamable provenance", result, p, err)
		}
	})
	t.Run("origin filtering precedes eligibility", func(t *testing.T) {
		var a Allocator
		txn := &Transaction{XID: 10, FirstLSN: a.End()}
		records := []Record{
			{Kind: RowEvent, LSN: a.Allocate(), Txn: txn, Origin: 7, Change: &Change{Kind: Insert}},
			{Kind: CatalogEvent, LSN: a.Allocate(), Txn: txn, Origin: 7},
			{Kind: Commit, LSN: a.Allocate(), Txn: txn, Origin: 7},
		}
		local := &Transaction{XID: 20, FirstLSN: a.End()}
		records = append(records, Record{Kind: CatalogEvent, LSN: a.Allocate(), Txn: local})
		records = append(records, Record{Kind: Commit, LSN: a.Allocate(), Txn: local})
		p := &localStreamingCounter{}
		result, err := Decode(context.Background(), Request{Records: records, Restart: Base, Confirmed: Base, Frontier: a.End(), Plugin: p, Options: Options{Streaming: true}, StreamImmediate: true})
		if err != nil || len(result.Rows) != 2 || !slices.Equal(p.seen, []uint32{20}) || len(p.blocks) != 0 || len(p.commits) != 0 {
			t.Fatal("filtered origin supplied streaming eligibility", result, p, err)
		}
	})
}

type localStreamingCounter struct{ streamingCounter }

func (*localStreamingCounter) FilterByOrigin(origin uint32) bool { return origin != 0 }
