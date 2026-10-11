package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTimeRetainedCursorsAndCancellation(t *testing.T) {
	p := fixture.NewScenario(t)
	run := func(s fixture.Step) { fixture.TemporalRunStep(t, p.Ctx, p.Memory, p.MemoryRecord, s) }
	run(fixture.Exec("CREATE TABLE clocks(id INTEGER,v TIME(0),fine TIME(6),plain TIME)", 0))
	run(fixture.Exec("INSERT INTO clocks VALUES(1,'00:00','00:00','allballs'),(2,'24:00','24:00','24:00'),(3,'04:05 PM','04:05:06.12',NULL)", 3))
	run(fixture.Exec("CREATE TABLE copied(LIKE clocks)", 0))
	want := [][]any{{int64(1), "00:00:00", "00:00:00", "00:00:00"}, {int64(2), "24:00:00", "24:00:00", "24:00:00"}, {int64(3), "16:05:00", "04:05:06.12", nil}}
	s := fixture.Select("SELECT id,v,fine,plain FROM clocks ORDER BY id", []string{"id", "v", "fine", "plain"}, []string{"INT4", "TIME", "TIME", "TIME"}, want)
	record := &fixture.Recorder{RawBackend: fixture.RawNative}
	ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, record)
	old, err := p.Memory.QueryContext(ctx, s.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE replacement(v TIME); INSERT INTO replacement VALUES('12:00');")); err != nil {
		t.Fatal(err)
	}
	referenceTimeConsume(t, old, false, s)
	fixture.TemporalRecorded(t, record, s)
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	held, err := p.Memory.QueryContext(ctx, s.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	referenceTimeConsume(t, held, false, s)
	fixture.TemporalRecorded(t, record, s)
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE replacement(v TIME); INSERT INTO replacement VALUES('12:00');")); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := p.Memory.ExecContext(canceled, "INSERT INTO replacement VALUES('24:00')"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	run(fixture.Select("SELECT v FROM replacement", []string{"v"}, []string{"TIME"}, [][]any{{"12:00:00"}}))
	iteration, stop := context.WithCancel(ctx)
	rows, err := p.Memory.QueryContext(iteration, "SELECT v FROM replacement")
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
