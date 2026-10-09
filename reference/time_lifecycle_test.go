package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceTimeRetainedCursorsAndCancellation(t *testing.T) {
	p := newReferenceScenario(t)
	run := func(s referenceStep) { referenceTemporalRunStep(t, p.ctx, p.memory, p.memoryRecord, s) }
	run(referenceExec("CREATE TABLE clocks(id INTEGER,v TIME(0),fine TIME(6),plain TIME)", 0))
	run(referenceExec("INSERT INTO clocks VALUES(1,'00:00','00:00','allballs'),(2,'24:00','24:00','24:00'),(3,'04:05 PM','04:05:06.12',NULL)", 3))
	run(referenceExec("CREATE TABLE copied(LIKE clocks)", 0))
	want := [][]any{{int64(1), "00:00:00", "00:00:00", "00:00:00"}, {int64(2), "24:00:00", "24:00:00", "24:00:00"}, {int64(3), "16:05:00", "04:05:06.12", nil}}
	s := referenceSelect("SELECT id,v,fine,plain FROM clocks ORDER BY id", []string{"id", "v", "fine", "plain"}, []string{"INT4", "TIME", "TIME", "TIME"}, want)
	record := &referenceRecorder{rawBackend: referenceRawNative}
	ctx := context.WithValue(p.ctx, referenceRecorderKey{}, record)
	old, err := p.memory.QueryContext(ctx, s.query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE replacement(v TIME); INSERT INTO replacement VALUES('12:00');")); err != nil {
		t.Fatal(err)
	}
	referenceTimeConsume(t, old, false, s)
	referenceTemporalRecorded(t, record, s)
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	held, err := p.memory.QueryContext(ctx, s.query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	referenceTimeConsume(t, held, false, s)
	referenceTemporalRecorded(t, record, s)
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE replacement(v TIME); INSERT INTO replacement VALUES('12:00');")); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(p.ctx)
	cancel()
	if err := stuntgres.Restore(canceled, p.native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := p.memory.ExecContext(canceled, "INSERT INTO replacement VALUES('24:00')"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	run(referenceSelect("SELECT v FROM replacement", []string{"v"}, []string{"TIME"}, [][]any{{"12:00:00"}}))
	iteration, stop := context.WithCancel(ctx)
	rows, err := p.memory.QueryContext(iteration, "SELECT v FROM replacement")
	if err != nil {
		stop()
		t.Fatal(err)
	}
	defer rows.Close()
	stop()
	for rows.Next() {
	}
	if !errors.Is(rows.Err(), context.Canceled) {
		t.Fatal(rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}
