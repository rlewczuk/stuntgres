package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDatetimeMakeZones(t *testing.T) {
	datetimeMakeTextCases(t, []datetimeMakeCase{
		{"explicitNYsummer", "make_timestamptz(2013,7,15,12,0,0,'America/New_York')", "2013-07-15 16:00:00+00"},
		{"explicitNYwinter", "make_timestamptz(2013,1,15,12,0,0,'America/New_York')", "2013-01-15 17:00:00+00"},
		{"numericISO", "make_timestamptz(2013,7,15,12,0,0,'+2')", "2013-07-15 10:00:00+00"},
		{"POSIX", "make_timestamptz(2013,7,15,12,0,0,'UTC+2')", "2013-07-15 14:00:00+00"},
		{"legacyColon", "make_timestamptz(2013,7,15,12,0,0,'+1:')", "2013-07-15 11:00:00+00"},
		{"legacyTrailing", "make_timestamptz(2013,7,15,12,0,0,'+10:00:')", "2013-07-15 02:00:00+00"},
		{"legacy100", "make_timestamptz(2013,7,15,12,0,0,'+100')", "2013-07-15 11:00:00+00"},
		{"legacy1000", "make_timestamptz(2013,7,15,12,0,0,'+1000')", "2013-07-15 02:00:00+00"},
		{"historicalSeconds", "make_timestamptz(1800,1,1,12,0,0,'Europe/Amsterdam')", "1800-01-01 11:40:28+00"},
		{"fixedEST", "make_timestamptz(2013,7,15,12,0,0,'EST')", "2013-07-15 17:00:00+00"},
		{"gap", "make_timestamptz(2018,3,11,2,30,0,'America/New_York')", "2018-03-11 07:30:00+00"},
		{"fold", "make_timestamptz(2018,11,4,1,30,0,'America/New_York')", "2018-11-04 06:30:00+00"},
		{"roundedTransition", "make_timestamptz(2018,3,11,1,59,59.9999995,'America/New_York')", "2018-03-11 07:00:00+00"},
		{"dayCarry", "make_timestamptz(2018,3,10,24,0,0,'America/New_York')", "2018-03-11 05:00:00+00"},
	})
}

func datetimeMakeHeldZones(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	q := "SELECT make_timestamptz(2013,7,15,12,0,0)::text AS session,make_timestamptz(2013,7,15,12,0,0,'UTC')::text AS explicit"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"session", "explicit"}, []string{"TEXT", "TEXT"}, [][]any{{"2013-07-15 12:00:00+00", "2013-07-15 12:00:00+00"}}))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE 'America/New_York'", 0))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"session", "explicit"}, []string{"TEXT", "TEXT"}, [][]any{{"2013-07-15 12:00:00-04", "2013-07-15 08:00:00-04"}}))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE 'UTC'", 0))
}

func TestReferenceDatetimeMakeLifecycle(t *testing.T) {
	datetimeMakeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		datetimeMakeHeldZones(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE dt_make_atomic(id integer,d date)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO dt_make_atomic VALUES(1,make_date(2000,1,1))", 1))
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("INSERT INTO dt_make_atomic VALUES(2,make_date(2000,2,29)),(3,make_date(2013,2,29))", datetimeMakeError("22008", "date field value out of range: 2013-02-29"))
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			s = fixture.ErrorDiagnostic("UPDATE dt_make_atomic SET d=make_date(2013,2,29)", datetimeMakeError("22008", "date field value out of range: 2013-02-29"))
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if _, err := b.DB.ExecContext(canceled, "INSERT INTO dt_make_atomic VALUES(9,make_date(2000,1,1))"); !errors.Is(err, context.Canceled) {
			t.Fatalf("precanceled request: %v", err)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id,d::text AS d FROM dt_make_atomic ORDER BY id", []string{"id", "d"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "2000-01-01"}}))
		styles := []struct{ name, want string }{{"postgres", "1 year 2 mons 3 days 04:05:06"}, {"postgres_verbose", "@ 1 year 2 mons 3 days 4 hours 5 mins 6 secs"}, {"sql_standard", "+1-2 +3 +4:05:06"}, {"iso_8601", "P1Y2M3DT4H5M6S"}}
		q := "SELECT make_interval(1,2,0,3,4,5,6) AS result"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, style := range styles {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET IntervalStyle TO '"+style.name+"'", 0))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"result"}, []string{"INTERVAL"}, [][]any{{style.want}}))
		}
	})
}
