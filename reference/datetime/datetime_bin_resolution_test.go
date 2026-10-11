package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func datetimeBinResolution(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, tc := range []struct{ source, origin, typ string }{
		{"TIMESTAMP '2000-01-02'", "TIMESTAMP '2000-01-01'", "TIMESTAMP"},
		{"TIMESTAMPTZ '2000-01-02+00'", "TIMESTAMPTZ '2000-01-01+00'", "TIMESTAMPTZ"},
		{"TIMESTAMP '2000-01-02'", "TIMESTAMPTZ '2000-01-01+00'", "TIMESTAMPTZ"},
		{"TIMESTAMPTZ '2000-01-02+00'", "TIMESTAMP '2000-01-01'", "TIMESTAMPTZ"},
		{"DATE '2000-01-02'", "TIMESTAMP '2000-01-01'", "TIMESTAMP"},
		{"TIMESTAMP '2000-01-02'", "DATE '2000-01-01'", "TIMESTAMP"},
		{"DATE '2000-01-02'", "TIMESTAMPTZ '2000-01-01+00'", "TIMESTAMPTZ"},
		{"TIMESTAMPTZ '2000-01-02+00'", "DATE '2000-01-01'", "TIMESTAMPTZ"},
		{"DATE '2000-01-02'", "DATE '2000-01-01'", "TIMESTAMPTZ"},
		{"'2000-01-02'", "'2000-01-01'", "TIMESTAMPTZ"},
		{"TIMESTAMP '2000-01-02'", "'2000-01-01'", "TIMESTAMP"},
		{"'2000-01-02'", "TIMESTAMP '2000-01-01'", "TIMESTAMP"},
		{"TIMESTAMPTZ '2000-01-02+00'", "'2000-01-01'", "TIMESTAMPTZ"},
		{"'2000-01-02'", "TIMESTAMPTZ '2000-01-01+00'", "TIMESTAMPTZ"},
	} {
		datetimeTruncValue(t, p, b, c, "date_bin('1 day',"+tc.source+","+tc.origin+")", tc.typ, "2000-01-02T00:00:00Z")
	}
	for _, name := range []string{"date_bin", "pg_catalog.date_bin", `"date_bin"`, `pg_catalog."date_bin"`} {
		datetimeTruncValue(t, p, b, c, name+"('15 minutes',TIMESTAMP '2000-01-02 00:07',TIMESTAMP '2000-01-01 00:02:30')", "TIMESTAMP", "2000-01-02T00:02:30Z")
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN db_stamp AS timestamp", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN db_instant AS timestamptz", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN db_stride AS interval", 0))
	datetimeTruncValue(t, p, b, c, "date_bin('1 day'::db_stride,'2000-01-02'::db_stamp,'2000-01-01'::db_stamp)", "TIMESTAMP", "2000-01-02T00:00:00Z")
	datetimeTruncValue(t, p, b, c, "date_bin('1 day'::db_stride,'2000-01-02+00'::db_instant,'2000-01-01'::db_stamp)", "TIMESTAMPTZ", "2000-01-02T00:00:00Z")
	// Prepared literal timestamp meaning is captured by the existing Prepare
	// contract; parameters and implicit coercions use each execution's zone.
	for _, tc := range []struct {
		q       string
		args    []any
		utc, ny string
	}{
		{"SELECT date_bin($1,$2,$3) AS result", []any{"15 minutes", "2000-01-02 00:07", "2000-01-01 00:02:30"}, "2000-01-02T00:02:30Z", "2000-01-02T05:02:30Z"},
		{"SELECT date_bin('1 day',$1::date,$2::date) AS result", []any{"2000-01-02", "2000-01-01"}, "2000-01-02T00:00:00Z", "2000-01-02T05:00:00Z"},
		{"SELECT date_bin('15 minutes',$1::timestamp,$2::timestamptz) AS result", []any{"2000-01-02 00:07", "2000-01-01 00:02:30+00"}, "2000-01-02T00:02:30Z", "2000-01-02T05:02:30Z"},
		{"SELECT date_bin('15 minutes',$1::timestamptz,$2::timestamp) AS result", []any{"2000-01-02 00:07+00", "2000-01-01 00:02:30"}, "2000-01-02T00:02:30Z", "2000-01-02T00:02:30Z"},
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE 'UTC'", 0))
		stmt, err := c.PrepareContext(p.Ctx, tc.q)
		if err != nil {
			t.Fatal(err)
		}
		x := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.q}
		for _, z := range []struct{ name, want string }{{"UTC", tc.utc}, {"America/New_York", tc.ny}, {"UTC", tc.utc}} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE '"+z.name+"'", 0))
			fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(tc.q, []string{"result"}, []string{"TIMESTAMPTZ"}, [][]any{{datetimeTruncInstant(z.want)}}, tc.args...))
		}
		stmt.Close()
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE 'America/New_York'", 0))
	datetimeTruncValue(t, p, b, c, "date_bin('1 day',DATE '2000-01-02',DATE '2000-01-01')", "TIMESTAMPTZ", "2000-01-02T05:00:00Z")
	datetimeTruncValue(t, p, b, c, "date_bin('1 day',DATE '2000-01-02',TIMESTAMP '2000-01-01')", "TIMESTAMP", "2000-01-02T00:00:00Z")
}

func TestReferenceDatetimeBinResolution(t *testing.T) {
	datetimeBinScenario(t, datetimeBinResolution)
}
