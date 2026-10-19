package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func routineSmokeRoundtrip(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	if b.PG {
		return
	}
	routineExec(t, p, b, c,
		"CREATE TABLE routine_smoke_cycle(x integer)",
		"CREATE FUNCTION routine_smoke_cycle_read() RETURNS integer LANGUAGE SQL AS $$SELECT max(x) FROM routine_smoke_cycle$$",
		"ALTER TABLE routine_smoke_cycle ALTER COLUMN x SET DEFAULT routine_smoke_cycle_read()")
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO routine_smoke_cycle VALUES (7)", 1))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	routineValue(t, p, b, c, "SELECT add1(4)", "add1", "INT4", int64(5))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO routine_smoke_cycle DEFAULT VALUES", 1))
	routineValue(t, p, b, c, "SELECT sum(x)::integer AS v FROM routine_smoke_cycle", "v", "INT4", int64(14))
}
