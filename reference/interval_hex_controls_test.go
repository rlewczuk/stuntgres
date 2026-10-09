package reference_test

import (
	"bytes"
	"context"
	"stuntgres"
	"testing"
)

func TestReferenceIntervalISOHexadecimalNativeRoundTrip(t *testing.T) {
	p := newReferenceScenario(t)
	ctx := p.ctx
	source := p.native
	p.step(t, referenceExec("CREATE TABLE iv(id INTEGER,v INTERVAL DEFAULT INTERVAL 'PT0x.8p0S')", 0))
	p.step(t, referenceExec("INSERT INTO iv VALUES(1,'PT0x1.8S'),(2,'P1DT-0x1.8p1S')", 2))
	p.step(t, referenceExec("INSERT INTO iv(id) VALUES(3)", 1))
	p.step(t, referenceExec("CREATE VIEW ivview AS SELECT INTERVAL 'PT0X1.8P+1S' AS v FROM iv WHERE id=1", 0))
	p.step(t, referenceSelect("SELECT id,v FROM iv ORDER BY id", []string{"id", "v"}, []string{"INT4", "INTERVAL"}, [][]any{{int64(1), "00:00:01.5"}, {int64(2), "1 day -00:00:03"}, {int64(3), "00:00:00.5"}}))
	p.step(t, referenceSelect("SELECT v FROM ivview", []string{"v"}, []string{"INTERVAL"}, [][]any{{"00:00:03"}}))
	var canonical bytes.Buffer
	if err := stuntgres.Dump(ctx, source, &canonical); err != nil {
		t.Fatal(err)
	}
	for _, style := range []string{"postgres", "postgres_verbose", "sql_standard", "iso_8601"} {
		intervalNativeExec(t, source, "SET IntervalStyle="+style)
		var dump bytes.Buffer
		if err := stuntgres.Dump(ctx, source, &dump); err != nil || dump.String() != canonical.String() {
			t.Fatal(style, err, "style-dependent hexadecimal-origin dump")
		}
		destNative, dest, destRecord := intervalReferenceDestination(t, p, t.Name()+"/"+style)
		queryCtx := context.WithValue(ctx, referenceRecorderKey{}, destRecord)
		intervalNativeExec(t, dest, "SET IntervalStyle="+style)
		if err := stuntgres.Restore(ctx, destNative, bytes.NewReader(canonical.Bytes())); err != nil {
			t.Fatal(style, err)
		}
		var redump bytes.Buffer
		if err := stuntgres.Dump(ctx, destNative, &redump); err != nil || redump.String() != canonical.String() {
			t.Fatal(style, err, "noncanonical hexadecimal-origin roundtrip")
		}
		intervalNativeExec(t, dest, "SET IntervalStyle=postgres")
		rows, err := dest.QueryContext(queryCtx, "SELECT id,v FROM iv ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		intervalRawRows(t, rows, [][]any{{int64(1), "00:00:01.5"}, {int64(2), "1 day -00:00:03"}, {int64(3), "00:00:00.5"}}, destRecord)
		rows, err = dest.QueryContext(queryCtx, "SELECT v FROM ivview")
		if err != nil {
			t.Fatal(err)
		}
		intervalRawRows(t, rows, [][]any{{"00:00:03"}}, destRecord)
		intervalNativeExec(t, dest, "INSERT INTO iv(id) VALUES(4)")
		rows, err = dest.QueryContext(queryCtx, "SELECT v FROM iv WHERE id=4")
		if err != nil {
			t.Fatal(err)
		}
		intervalRawRows(t, rows, [][]any{{"00:00:00.5"}}, destRecord)
	}
}
