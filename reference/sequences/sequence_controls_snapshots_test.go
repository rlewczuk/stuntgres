package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequenceControlsOptionsStateSnapshots(t *testing.T) {
	p := fixture.NewScenario(t)
	ctx := p.Ctx
	db := p.Native
	a := fixture.SequenceCoreConn(t, ctx, p.Memory)
	b := fixture.SequenceCoreConn(t, ctx, p.Memory)
	for _, q := range []string{"CREATE SCHEMA other", "CREATE TABLE other.t(a INT,b INT,c INT)", "CREATE SEQUENCE other.s AS SMALLINT INCREMENT -2 MINVALUE -10 MAXVALUE -2 START -4 CACHE 3 CYCLE OWNED BY other.t.c", "CREATE UNLOGGED SEQUENCE uncalled START 7", "COMMENT ON SEQUENCE other.s IS 'generator metadata'"} {
		fixture.SequenceProbeExec(t, ctx, a, q)
	}
	fixture.SequenceProbeValue(t, ctx, a, "SELECT nextval('other.s')", -4)
	query := "SELECT * FROM other.s"
	held, err := a.QueryContext(context.WithValue(ctx, fixture.RecorderKey{}, p.MemoryRecord), query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	types, err := held.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 3 || types[0].DatabaseTypeName() != "INT8" || types[1].DatabaseTypeName() != "INT8" || types[2].DatabaseTypeName() != "BOOL" {
		t.Fatal(types)
	}
	var first bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &first); err != nil {
		t.Fatal(err)
	}
	fixture.SequenceProbeExec(t, ctx, a, "CREATE TEMP SEQUENCE temporary START 50 CACHE 3")
	fixture.SequenceProbeValue(t, ctx, a, "SELECT nextval('temporary')", 50)
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	fixture.SequenceProbeValue(t, ctx, a, "SELECT nextval('temporary')", 51)
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(first.Bytes())); err != nil {
		t.Fatal(err)
	}
	var second bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatalf("dump mismatch\n%s\n%s", first.Bytes(), second.Bytes())
	}
	frozen := fixture.SequenceFrozenRows{Executor: a, Rows: held, Query: query}
	step := fixture.Step{Query: query, SelectRows: true, Columns: []string{"last_value", "log_cnt", "is_called"}, Types: []string{"INT8", "INT8", "BOOL"}, Rows: [][]any{{int64(-8), int64(0), true}}}
	step.CheckScan = func(t *testing.T, rows *sql.Rows, rowIndex int) {
		var last, log int64
		var called bool
		if err := rows.Scan(&last, &log, &called); err != nil || rowIndex != 0 || last != -8 || log != 0 || !called {
			t.Fatal("frozen state", rowIndex, last, log, called, err)
		}
	}
	fixture.RunStep(t, ctx, frozen, p.MemoryRecord, step)
	fixture.SequenceProbeValue(t, ctx, b, "SELECT nextval('other.s')", -10)
	fixture.SequenceProbeValue(t, ctx, b, "SELECT nextval('uncalled')", 7)
	fixture.SequenceProbeValue(t, ctx, a, "SELECT nextval('temporary')", 52)
	_, err = a.ExecContext(ctx, "SELECT currval('other.s')")
	fixture.SequenceNativeDiagnostic(t, err, stuntgres.Error{Code: "55000", Message: `currval of sequence "s" is not yet defined in this session`})
	// Invalid late content must not replace persistent objects or invalidate local memories.
	for _, stream := range []string{"CREATE SEQUENCE replacement; CREATE SEQUENCE invalid CACHE 0;", "CREATE SEQUENCE replacement; SELECT setval('replacement',0,true);", "CREATE SEQUENCE replacement; SELECT setval('replacement',1);", "CREATE SEQUENCE replacement; SELECT nextval('replacement');"} {
		if err = stuntgres.Restore(ctx, db, bytes.NewBufferString(stream)); err == nil {
			t.Fatal("bad restore accepted", stream)
		}
		fixture.SequenceProbeValue(t, ctx, b, "SELECT currval('uncalled')", 7)
	}
	fixture.SequenceProbeExec(t, ctx, b, "DROP TABLE other.t RESTRICT")
	_, err = b.ExecContext(ctx, "SELECT nextval('other.s')")
	fixture.SequenceNativeDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "other.s" does not exist`, Position: "16"})
}
