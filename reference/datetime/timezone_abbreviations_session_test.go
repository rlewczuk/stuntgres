package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTimezoneAbbreviationsCapture(t *testing.T) {
	timezoneAbbreviationsScenario(t, func(p *fixture.Pair, x fixture.Executor, record *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, record, s) }
		run(fixture.Exec("SET timezone_abbreviations='Default'", 0))
		literalQuery := "SELECT (TIMESTAMPTZ '2004-07-01 12:00 IST' AT TIME ZONE 'UTC')::TEXT AS v FROM tz_witness"
		parameterQuery := "SELECT ($1::TIMESTAMPTZ AT TIME ZONE 'UTC')::TEXT AS v FROM tz_witness"
		literal := timezoneHeld(t, p, x, literalQuery)
		parameter := timezoneHeld(t, p, x, parameterQuery)
		consumers := []struct {
			query string
			args  []any
		}{
			{"SELECT ((TIMESTAMP '2004-07-01 12:00' AT TIME ZONE $1) AT TIME ZONE 'UTC')::TEXT AS v FROM tz_witness", []any{"IST"}},
			{"SELECT (make_timestamptz(2004,7,1,12,0,0,$1) AT TIME ZONE 'UTC')::TEXT AS v FROM tz_witness", []any{"IST"}},
			{"SELECT (to_timestamp($1,'YYYY-MM-DD HH24:MI TZ') AT TIME ZONE 'UTC')::TEXT AS v FROM tz_witness", []any{"2004-07-01 12:00 IST"}},
		}
		held := make([]fixture.HeldStatement, len(consumers))
		for i, consumer := range consumers {
			held[i] = timezoneHeld(t, p, x, consumer.query)
		}
		run(fixture.Exec("SET timezone_abbreviations='India'", 0))
		fixture.RunStep(t, p.Ctx, literal, record, fixture.Select(literalQuery, []string{"v"}, []string{"TEXT"}, [][]any{{"2004-07-01 10:00:00"}}))
		fixture.RunStep(t, p.Ctx, parameter, record, fixture.Select(parameterQuery, []string{"v"}, []string{"TEXT"}, [][]any{{"2004-07-01 06:30:00"}}, "2004-07-01 12:00 IST"))
		for i, consumer := range consumers {
			fixture.RunStep(t, p.Ctx, held[i], record, fixture.Select(consumer.query, []string{"v"}, []string{"TEXT"}, [][]any{{"2004-07-01 06:30:00"}}, consumer.args...))
		}
		run(fixture.Exec("UPDATE tz_witness SET z='2004-07-01 12:00+00',q='12:00 IST',c='2004-07-01 12:00 IST',d='2004-07-01' WHERE id=1", 1))
		for _, set := range []string{"Default", "Australia", "India"} {
			run(fixture.Exec("SET timezone_abbreviations='"+set+"'", 0))
			run(fixture.Select("SELECT z::TEXT AS z,q::TEXT AS q,c::TEXT AS c,d::TEXT AS d FROM tz_witness", []string{"z", "q", "c", "d"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"2004-07-01 12:00:00+00", "12:00:00+05:30", "2004-07-01 12:00:00", "2004-07-01"}}))
			run(fixture.Exec("SET TIME ZONE 'America/New_York'", 0))
			for _, style := range []struct{ setting, want string }{{"SQL,MDY", "07/01/2004 08:00:00 EDT"}, {"Postgres,MDY", "Thu Jul 01 08:00:00 2004 EDT"}, {"German", "01.07.2004 08:00:00 EDT"}} {
				run(fixture.Exec("SET DateStyle='"+style.setting+"'", 0))
				run(timezoneTextStep("z", style.want))
			}
			run(fixture.Exec("SET DateStyle='ISO,MDY'", 0))
			run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
		}
	})
}

func TestReferenceTimezoneAbbreviationsLifecycle(t *testing.T) {
	timezoneAbbreviationsScenario(t, func(p *fixture.Pair, x fixture.Executor, record *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, record, s) }
		check := func(set, clock string) {
			run(timezoneAbbreviationsShow(set))
			run(timezoneTextStep("TIMESTAMPTZ '2004-07-01 12:00 IST' AT TIME ZONE 'UTC'", "2004-07-01 "+clock))
		}
		for _, prepared := range []bool{false, true} {
			utility := func(q string) { s := fixture.Exec(q, 0); s.Prepared = prepared; run(s) }
			utility("SET timezone_abbreviations='Default'")
			for _, q := range []string{"BEGIN", "SET LOCAL timezone_abbreviations='India'", "SAVEPOINT mark", "SET timezone_abbreviations='Australia'", "ROLLBACK TO mark"} {
				utility(q)
			}
			check("India", "06:30:00")
			utility("COMMIT")
			check("Default", "10:00:00")
			for _, q := range []string{"BEGIN", "SET timezone_abbreviations='India'", "SET LOCAL timezone_abbreviations='Australia'"} {
				utility(q)
			}
			check("Australia", "10:00:00")
			utility("COMMIT")
			check("India", "06:30:00")
			for _, q := range []string{"BEGIN", "SET timezone_abbreviations='Australia'", "ROLLBACK"} {
				utility(q)
			}
			check("India", "06:30:00")
			for _, reset := range []string{"RESET timezone_abbreviations", "SET timezone_abbreviations=DEFAULT", "RESET ALL"} {
				utility("SET timezone_abbreviations='India'")
				utility(reset)
				check("Default", "10:00:00")
			}
		}
		run(fixture.Select("SELECT set_config('timezone_abbreviations','India',false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"India"}}))
		check("India", "06:30:00")
		run(fixture.Select("SELECT set_config('timezone_abbreviations',NULL,false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"Default"}}))
		check("Default", "10:00:00")
		run(fixture.Select("SELECT current_setting('TIMEZONE_ABBREVIATIONS')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{"Default"}}))
		run(fixture.Select("SELECT current_setting(NULL)", []string{"current_setting"}, []string{"TEXT"}, [][]any{{nil}}))
	})
}
