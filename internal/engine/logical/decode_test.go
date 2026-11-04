package logical

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// counter emits one row per callback and records which transactions it saw.
type counter struct{ seen []uint32 }

func (c *counter) Startup(Startup) (Options, error) { return Options{}, nil }
func (c *counter) Begin(o *Output, t *Txn) error {
	c.seen = append(c.seen, t.XID)
	o.Write(nil)
	return nil
}
func (c *counter) Change(o *Output, t *Txn, _ *Change) error { o.Write(nil); return nil }
func (c *counter) Commit(o *Output, t *Txn) error            { o.Write(nil); return nil }
func (c *counter) Shutdown()                                 {}

// stream builds n one-change committed transactions: change at Base+2iS,
// commit at Base+(2i+1)S.
func stream(n int) ([]Record, *Allocator) {
	a := &Allocator{}
	var records []Record
	for i := range n {
		change := Change{LSN: a.Allocate(), Kind: Insert}
		txn := &Transaction{XID: uint32(i + 10), FirstLSN: change.LSN, Changes: []Change{change}}
		records = append(records, Record{Kind: Commit, LSN: a.Allocate(), Txn: txn})
	}
	return records, a
}

func TestLogicalDecodeLimits(t *testing.T) {
	records, a := stream(3)
	decode := func(confirmed, uptoLSN uint64, uptoChanges int32) ([]uint32, uint64) {
		t.Helper()
		c := &counter{}
		r, err := Decode(context.Background(), Request{Records: records, Restart: confirmed, Confirmed: confirmed, Frontier: a.End(), UptoLSN: uptoLSN, UptoChanges: uptoChanges, Plugin: c})
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Rows) != 3*len(c.seen) {
			t.Fatal("rows of whole transactions only", len(r.Rows), c.seen)
		}
		return c.seen, r.End
	}
	for _, tc := range []struct {
		name        string
		confirmed   uint64
		uptoLSN     uint64
		uptoChanges int32
		seen        []uint32
		end         uint64
	}{
		{"unlimited", Base, 0, 0, []uint32{10, 11, 12}, a.End()},
		{"one change finishes its transaction", Base, 0, 1, []uint32{10}, records[0].End()},
		{"limit counts rows", Base, 0, 4, []uint32{10, 11}, records[1].End()},
		{"negative stops after the first record", Base, 0, -1, nil, Base + Span},
		{"commit end includes it", Base, records[1].End(), 0, []uint32{10, 11}, records[1].End()},
		{"inside a commit includes it", Base, records[1].LSN + 1, 0, []uint32{10, 11}, records[1].End()},
		{"commit start excludes it", Base, records[1].LSN, 0, []uint32{10}, records[1].LSN},
		{"before the restart", records[1].End(), Base, 0, nil, records[1].End() + Span},
		{"confirmed skips earlier commits", records[0].End(), 0, 0, []uint32{11, 12}, a.End()},
		{"beyond the stream", Base, a.End() + 10*Span, 0, []uint32{10, 11, 12}, a.End()},
	} {
		seen, end := decode(tc.confirmed, tc.uptoLSN, tc.uptoChanges)
		if !slices.Equal(seen, tc.seen) || end != tc.end {
			t.Errorf("%s: seen %v end %X, want %v %X", tc.name, seen, end, tc.seen, tc.end)
		}
	}
}

func TestLogicalDecodeCancellation(t *testing.T) {
	records, a := stream(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Decode(ctx, Request{Records: records, Restart: Base, Confirmed: Base, Frontier: a.End(), Plugin: &counter{}}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled decode", err)
	}
}

func TestLogicalHistoryRetention(t *testing.T) {
	records, _ := stream(3)
	var h History
	for _, r := range records {
		h.Append(r)
	}
	held := h.Snapshot()
	if got := RestartLSN(held, records[1].LSN); got != records[1].Txn.FirstLSN {
		t.Fatal("restart holds the first record of an unreceived transaction", got)
	}
	if got := RestartLSN(held, records[2].End()); got != records[2].End() {
		t.Fatal("restart with nothing pending", got)
	}
	h.Trim(records[1].LSN)
	if h.Len() != 2 || h.Snapshot()[0].LSN != records[1].LSN {
		t.Fatal("trim keeps records some slot still needs", h.Snapshot())
	}
	if len(held) != 3 || held[0].LSN != records[0].LSN {
		t.Fatal("trim changed a reader's snapshot")
	}
	h.Append(Record{Kind: Commit, LSN: records[2].End(), Txn: &Transaction{}})
	if len(held) != 3 {
		t.Fatal("append changed a reader's snapshot")
	}
	h.Trim(^uint64(0))
	if h.Len() != 0 {
		t.Fatal("trim releases history no slot needs", h.Len())
	}
}
