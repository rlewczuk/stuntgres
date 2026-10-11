package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTimezoneAbbreviationsFeatureSmoke(t *testing.T) {
	timezoneAbbreviationsScenario(t, func(p *fixture.Pair, x fixture.Executor, record *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, record, s) }
		run(timezoneAbbreviationsShow("Default"))
		query := "SELECT (TIMESTAMPTZ '2004-07-01 12:00 IST' AT TIME ZONE 'UTC')::TEXT AS v FROM tz_witness"
		literal := timezoneHeld(t, p, x, query)
		run(fixture.Exec("SET timezone_abbreviations='India'", 0))
		run(timezoneAbbreviationsShow("India"))
		fixture.RunStep(t, p.Ctx, literal, record, fixture.Select(query, []string{"v"}, []string{"TEXT"}, [][]any{{"2004-07-01 10:00:00"}}))
		run(fixture.Prepared(fixture.Select("SELECT ($1::TIMESTAMPTZ AT TIME ZONE 'UTC')::TEXT AS v FROM tz_witness", []string{"v"}, []string{"TEXT"}, [][]any{{"2004-07-01 06:30:00"}}, "2004-07-01 12:00 IST")))
		for _, expression := range []string{"make_timestamptz(2004,7,1,12,0,0,'IST')", "TIMESTAMP '2004-07-01 12:00' AT TIME ZONE 'IST'"} {
			run(timezoneTextStep("("+expression+") AT TIME ZONE 'UTC'", "2004-07-01 06:30:00"))
		}
		run(fixture.Exec("UPDATE tz_witness SET z='2004-07-01 12:00 IST',q='12:00 IST',c='2004-07-01 12:00 IST' WHERE id=1", 1))
		run(fixture.Exec("SET timezone_abbreviations='Australia'", 0))
		run(timezoneAbbreviationsShow("Australia"))
		run(timezoneTextStep("TIMESTAMPTZ '2004-07-01 12:00 SAT' AT TIME ZONE 'UTC'", "2004-07-01 02:30:00"))
		run(timezoneTextStep("q", "12:00:00+05:30"))
		run(timezoneTextStep("c", "2004-07-01 12:00:00"))
		run(fixture.Exec("SET TIME ZONE 'America/New_York'", 0))
		for _, expression := range []string{"TIMESTAMPTZ '2004-07-01 12:00 EST'", "to_timestamp('2004-07-01 12:00 EST','YYYY-MM-DD HH24:MI TZ')"} {
			run(timezoneTextStep("("+expression+") AT TIME ZONE 'UTC'", "2004-07-01 17:00:00"))
		}
		run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
		run(fixture.Exec("SET DateStyle='SQL,DMY'", 0))
		run(timezoneTextStep("DATE '01/02/03'", "01/02/2003"))
		run(fixture.Exec("SET DateStyle='ISO,MDY'", 0))
		run(fixture.Exec("SET IntervalStyle='sql_standard'", 0))
		run(timezoneTextStep("INTERVAL '-1 2:03:04'", "-1 2:03:04"))
		nulls := fixture.Select("SELECT NULL::DATE AS d,NULL::TIME AS t,NULL::TIMESTAMP AS c,NULL::TIMESTAMPTZ AS z,NULL::INTERVAL AS i,NULL::TIMETZ AS q FROM tz_witness", []string{"d", "t", "c", "z", "i", "q"}, []string{"DATE", "TIME", "TIMESTAMP", "TIMESTAMPTZ", "INTERVAL", "TIMETZ"}, [][]any{{nil, nil, nil, nil, nil, nil}})
		nulls.CheckMetadata = func(t *testing.T, rows *sql.Rows) { timezoneAbbreviationsMetadata(t, rows, pg) }
		run(nulls)
		s := fixture.ImplicitError("SET timezone_abbreviations='default'", fixture.Diagnostic{Code: "22023", Message: `invalid value for parameter "timezone_abbreviations": "default"`})
		s.ErrorStage = "execute"
		run(s)
		run(timezoneAbbreviationsShow("Australia"))
		run(timezoneTextStep("TIMESTAMPTZ '2004-07-01 12:00 SAT' AT TIME ZONE 'UTC'", "2004-07-01 02:30:00"))
		query = "INSERT INTO tz_witness(z) VALUES($1::TEXT::TIMESTAMPTZ),($2::TEXT::TIMESTAMPTZ)"
		s = fixture.ImplicitError(query, fixture.Diagnostic{Code: "22007", Message: `invalid input syntax for type timestamp with time zone: "nonsense"`}, "2005-01-01 12:00 EST", "nonsense")
		s.ErrorStage = "execute"
		run(fixture.Prepared(s))
		run(fixture.Select("SELECT COUNT(*) AS n FROM tz_witness", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(timezoneTextStep("z", "2004-07-01 06:30:00+00"))
		run(fixture.Exec("RESET timezone_abbreviations", 0))
		run(timezoneAbbreviationsShow("Default"))
	})
}
