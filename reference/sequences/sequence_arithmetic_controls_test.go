package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequenceArithmeticCompositionNativeControls(t *testing.T) {
	p := fixture.NewScenario(t)
	ctx, db := p.Ctx, p.Native
	c := fixture.SequenceCoreConn(t, ctx, p.Memory)
	fixture.SequenceProbeExec(t, ctx, c, "CREATE TABLE control_src(id INT)")
	result, err := c.ExecContext(ctx, "INSERT INTO control_src VALUES(1)")
	if err != nil {
		t.Fatal(err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	fixture.SequenceProbeExec(t, ctx, c, "CREATE SEQUENCE control_s START 10")
	var n int64
	if err := c.QueryRowContext(ctx, "SELECT CAST(0+nextval('control_s') AS integer) FROM control_src").Scan(&n); err != nil || n != 10 {
		t.Fatal(n, err)
	}
	held, err := c.QueryContext(context.WithValue(ctx, fixture.RecorderKey{}, p.MemoryRecord), "SELECT 'control_s'::regclass,0+currval('control_s') FROM control_src")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	q := "SELECT 'control_s'::regclass,0+currval('control_s') FROM control_src"
	step := fixture.Select(q, []string{"regclass", "?column?"}, []string{"REGCLASS", "INT8"}, [][]any{{"control_s", int64(10)}})
	step.CheckScan = func(t *testing.T, rows *sql.Rows, rowIndex int) {
		var label string
		var n int64
		if err := rows.Scan(&label, &n); err != nil {
			t.Fatal(err)
		}
		if rowIndex != 0 || label != "control_s" || n != 10 {
			t.Fatalf("control typed row %d = (%q,%d), want (control_s,10)", rowIndex, label, n)
		}
	}
	fixture.RunStep(t, ctx, fixture.SequenceFrozenRows{Executor: c, Rows: held, Query: q}, p.MemoryRecord, step)
	var restored bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &restored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored.Bytes(), dump.Bytes()) {
		t.Fatalf("roundtrip changed dump\n%s\n%s", dump.String(), restored.String())
	}
	if err := c.QueryRowContext(ctx, "SELECT CAST(0+nextval('control_s') AS integer) FROM control_src").Scan(&n); err != nil || n != 11 {
		t.Fatal(n, err)
	}
}
