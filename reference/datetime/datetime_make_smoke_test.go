package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceDatetimeMakeFeatureSmoke(t *testing.T) {
	datetimeMakeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		date := any("2013-07-15")
		clock := any("08:15:23.5")
		if b.PG {
			date = time.Date(2013, 7, 15, 0, 0, 0, 0, time.UTC)
			clock = time.Date(0, 1, 1, 8, 15, 23, 500000000, time.UTC)
		}
		ts := time.Date(2013, 7, 15, 8, 15, 23, 500000000, time.UTC)
		run(fixture.Select("SELECT make_date(2013,7,15),make_time(8,15,23.5),make_timestamp(2013,7,15,8,15,23.5),make_timestamptz(2013,7,15,8,15,23.5,'UTC'),make_interval(1,2,3,4,5,6,7)", []string{"make_date", "make_time", "make_timestamp", "make_timestamptz", "make_interval"}, []string{"DATE", "TIME", "TIMESTAMP", "TIMESTAMPTZ", "INTERVAL"}, [][]any{{date, clock, ts, ts, "1 year 2 mons 25 days 05:06:07"}}))
		run(fixture.Select("SELECT make_date(-44,3,15)::text AS bc,make_time(24,0,0)::text AS endpoint,make_timestamp(1999,12,31,24,0,0)::text AS carry,make_interval(days=>2,secs:=1.5) AS sparse,make_interval() AS zero,make_interval(secs=>NULL) AS null", []string{"bc", "endpoint", "carry", "sparse", "zero", "null"}, []string{"TEXT", "TEXT", "TEXT", "INTERVAL", "INTERVAL", "INTERVAL"}, [][]any{{"0044-03-15 BC", "24:00:00", "2000-01-01 00:00:00", "2 days 00:00:01.5", "00:00:00", nil}}))
		datetimeMakeHeldZones(t, p, b, c)
		for _, failure := range []struct {
			q string
			d fixture.Diagnostic
		}{
			{"SELECT make_date(2013,2,29)", datetimeMakeError("22008", "date field value out of range: 2013-02-29")},
			{"SELECT make_time(0,0,$1::float8)", datetimeMakeError("22008", "time field value out of range: 0:00:61")},
			{"SELECT make_timestamptz(2000,1,1,0,0,0,'Nehwon/Lankhmar')", datetimeMakeError("22023", `time zone "Nehwon/Lankhmar" not recognized`)},
			{"SELECT make_date(2013,7)", func() fixture.Diagnostic {
				d := datetimeMakeError("42883", "function make_date(integer, integer) does not exist")
				d.Position = "8"
				d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				return d
			}()},
		} {
			s := fixture.ErrorDiagnostic(failure.q, failure.d)
			s.SelectRows = true
			if failure.q == "SELECT make_time(0,0,$1::float8)" {
				s.Args = []any{float64(61)}
				s.Prepared = true
				s.ErrorStage = "execute"
			}
			run(s)
		}
		run(fixture.Exec("CREATE TABLE dt_make_smoke_atomic(id integer,d date)", 0))
		run(fixture.Exec("INSERT INTO dt_make_smoke_atomic VALUES(1,make_date(2000,1,1))", 1))
		run(fixture.ErrorDiagnostic("INSERT INTO dt_make_smoke_atomic VALUES(2,make_date(2000,2,29)),(3,make_date(2013,2,29))", datetimeMakeError("22008", "date field value out of range: 2013-02-29")))
		run(fixture.Select("SELECT id,d::text AS date FROM dt_make_smoke_atomic ORDER BY id", []string{"id", "date"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "2000-01-01"}}))
		datetimeMakeStored(t, p, b, c)
	})
}
