package testdecoding

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine/logical"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func types() map[sqltype.ID]*sqltype.Descriptor {
	out := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() {
		out[d.ID()] = d
	}
	return out
}
func TestTestDecodingOptions(t *testing.T) {
	for _, flag := range []string{"include-xids", "include-timestamp", "skip-empty-xacts", "only-local", "include-rewrites", "stream-changes", "force-binary"} {
		for _, value := range []string{"bad", "o", " true", "true ", ""} {
			p := New()
			_, err := p.Startup(logical.Startup{Options: []logical.Option{{Name: flag, Value: value}}})
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "22023" || diagnostic.Message != "could not parse value \""+value+"\" for parameter \""+flag+"\"" {
				t.Fatal(flag, value, err)
			}
		}
	}
	for _, value := range []string{"1", "t", "TRUE", "y", "YeS", "on"} {
		v, ok := boolean(value)
		if !ok || !v {
			t.Fatal(value)
		}
	}
	for _, value := range []string{"0", "f", "FALSE", "n", "No", "of", "OFF"} {
		v, ok := boolean(value)
		if !ok || v {
			t.Fatal(value)
		}
	}
	p := New()
	opts, err := p.Startup(logical.Startup{Options: []logical.Option{{"force-binary", "1"}, {"force-binary", "0"}, {"stream-changes", "1"}, {"include-rewrites", "1"}}})
	if err != nil || !opts.Binary || !opts.Streaming || !opts.ReceiveRewrites {
		t.Fatal(opts, err)
	}
	fresh := New()
	opts, err = fresh.Startup(logical.Startup{})
	if err != nil || opts != (logical.Options{}) {
		t.Fatal("fresh context", opts, err)
	}
}
func TestTestDecodingFormatting(t *testing.T) {
	typ := types()
	r := &logical.Relation{Schema: "public", Name: "select"}
	var values []sqltype.Value
	for _, c := range []struct {
		name        string
		id          sqltype.ID
		text, label string
	}{{"Mixed", sqltype.Int4, "7", "integer"}, {"a\"b", sqltype.Text, "O'Brien\\x", "text"}, {"truth", sqltype.Bool, "t", "boolean"}, {"n", sqltype.Numeric, "12.30", "numeric"}, {"bits", sqltype.Bit, "101", "bit"}} {
		v, err := typ[c.id].Input(context.Background(), c.text, -1)
		if err != nil {
			t.Fatal(err)
		}
		r.Columns = append(r.Columns, logical.Column{Name: c.name, Number: int32(len(r.Columns) + 1), Usage: typ[c.id].Usage(), TypeName: c.label})
		values = append(values, v)
	}
	c := logical.Change{Kind: logical.Insert, Relation: r, New: values}
	text, err := formatChange(context.Background(), sqltype.OutputEnvironment{}, &c)
	want := `table public."select": INSERT: "Mixed"[integer]:7 "a""b"[text]:'O''Brien\x' truth[boolean]:true n[numeric]:12.30 bits[bit]:B'101'`
	if err != nil || text != want {
		t.Fatal(text, err, "want", want)
	}
	c.Kind = logical.Update
	c.OldTuple = logical.OldFull
	c.Old = slices.Clone(values)
	c.Old[1] = sqltype.Null(typ[sqltype.Text])
	text, err = formatChange(context.Background(), sqltype.OutputEnvironment{}, &c)
	if err != nil || text != `table public."select": UPDATE: old-key: "Mixed"[integer]:7 truth[boolean]:true n[numeric]:12.30 bits[bit]:B'101' new-tuple:`+want[len(`table public."select": INSERT:`):] {
		t.Fatal(text, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = formatChange(ctx, sqltype.OutputEnvironment{}, &c); !errors.Is(err, context.Canceled) {
		t.Fatal("format cancellation", err)
	}
}
func TestTestDecodingCallbacks(t *testing.T) {
	p := New()
	opts, err := p.Startup(logical.Startup{Options: []logical.Option{{"include-xids", "0"}, {"skip-empty-xacts", "1"}, {"stream-changes", "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	var h logical.History
	var a logical.Allocator
	first := a.Allocate()
	txn := h.Anchor(11, first)
	c := &logical.Change{LSN: first, Kind: logical.Insert, Relation: &logical.Relation{OID: 1}}
	h.Append(logical.Record{Kind: logical.RowEvent, LSN: first, Txn: txn, Child: 11, Change: c})
	h.Append(logical.Record{Kind: logical.Commit, LSN: a.Allocate(), Txn: txn})
	got, err := logical.Decode(context.Background(), logical.Request{Records: h.Snapshot(), Restart: logical.Base, Confirmed: logical.Base, Frontier: a.End(), Plugin: p, Options: opts, StreamImmediate: true})
	var text []string
	for _, row := range got.Rows {
		text = append(text, string(row.Data))
		if row.XID != 11 {
			t.Fatal(row)
		}
	}
	want := []string{"opening a streamed block for transaction", "streaming change for transaction", "closing a streamed block for transaction", "committing streamed transaction"}
	if err != nil || !slices.Equal(text, want) {
		t.Fatal(text, err)
	}
	p.Shutdown()
}

func TestTestDecodingTimestamp(t *testing.T) {
	p := New().(*plugin)
	p.timestamp = true
	if got := p.stamp(&logical.Txn{Time: 1234567}); got != " (at 2000-01-01 00:00:01.234567+00)" {
		t.Fatal(got)
	}
	zone, err := sqltype.ResolveZone("Asia/Kathmandu")
	if err != nil {
		t.Fatal(err)
	}
	p.output = sqltype.OutputEnvironment{Zone: zone, DateStyle: sqltype.DateStyle{Style: sqltype.DateGerman}}
	if got := p.stamp(&logical.Txn{}); got != " (at 2000-01-01 05:45:00+05:45)" {
		t.Fatal("callback must use ISO and actual zone", got)
	}
	if got := p.stamp(&logical.Txn{Time: sqltype.TimestampPositiveInfinity}); got != " (at infinity)" {
		t.Fatal(got)
	}
	if got := p.stamp(&logical.Txn{Time: sqltype.TimestampNegativeInfinity}); got != " (at -infinity)" {
		t.Fatal(got)
	}
}
