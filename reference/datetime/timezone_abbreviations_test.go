package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// Expectations are independently derived from PostgreSQL 18's stock sets.
// Each backend is checked against constants, never used to generate expectations.
func timezoneAbbreviationsScenario(t *testing.T, body func(*fixture.Pair, fixture.Executor, *fixture.Recorder, bool)) {
	t.Helper()
	p := fixture.TimezoneReferencePair(t)
	for _, backend := range p.Backends() {
		body(p, backend.DB, backend.Recorder, backend.PG)
	}
}

func timezoneAbbreviationsShow(want string) fixture.Step {
	return fixture.Select("SHOW timezone_abbreviations", []string{"timezone_abbreviations"}, []string{"TEXT"}, [][]any{{want}})
}

func TestReferenceTimezoneAbbreviationsStockInput(t *testing.T) {
	timezoneAbbreviationsScenario(t, func(p *fixture.Pair, x fixture.Executor, record *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, record, s) }
		run(timezoneAbbreviationsShow("Default"))
		for _, tc := range []struct{ set, ist, est, cst, sat string }{
			{"Default", "10:00:00", "17:00:00", "18:00:00", "12:00:00"},
			{"India", "06:30:00", "17:00:00", "18:00:00", "12:00:00"},
			{"Australia", "10:00:00", "02:00:00", "02:30:00", "02:30:00"},
		} {
			run(fixture.Exec("SET TIMEZONE_ABBREVIATIONS='"+tc.set+"'", 0))
			run(timezoneAbbreviationsShow(tc.set))
			for _, v := range []struct{ abbreviation, clock string }{{"IST", tc.ist}, {"est", tc.est}, {"CST", tc.cst}, {"SAT", tc.sat}, {"UTC", "12:00:00"}, {"EDT", "16:00:00"}} {
				query := "SELECT (($1::TIMESTAMPTZ) AT TIME ZONE 'UTC')::TEXT AS v FROM tz_witness"
				s := fixture.Select(query, []string{"v"}, []string{"TEXT"}, [][]any{{"2004-07-01 " + v.clock}}, "2004-07-01 12:00 "+v.abbreviation)
				run(s)
				run(fixture.Prepared(s))
				run(timezoneTextStep("TIMESTAMPTZ '2004-07-01 12:00 "+v.abbreviation+"' AT TIME ZONE 'UTC'", "2004-07-01 "+v.clock))
			}
			for _, v := range []struct{ expression, want string }{
				{"DATE '2004-07-01 12:00 IST'", "2004-07-01"},
				{"TIMESTAMP '2004-07-01 12:00 IST'", "2004-07-01 12:00:00"},
				{"TIME '12:00 IST'", "12:00:00"},
				{"TIMESTAMPTZ '2004-07-01 12:00 Europe/Paris' AT TIME ZONE 'UTC'", "2004-07-01 10:00:00"},
				{"TIMESTAMPTZ '2004-07-01 12:00+05:30' AT TIME ZONE 'UTC'", "2004-07-01 06:30:00"},
			} {
				run(timezoneTextStep(v.expression, v.want))
			}
		}
		for _, tc := range []struct{ set, token, offset string }{{"India", "IST", "+05:30"}, {"Australia", "EST", "+10"}, {"Australia", "CST", "+09:30"}, {"Australia", "SAT", "+09:30"}} {
			run(fixture.Exec("SET timezone_abbreviations='"+tc.set+"'", 0))
			run(timezoneTextStep("TIMETZ '12:00 "+tc.token+"'", "12:00:00"+tc.offset))
			run(timezoneTextStep("TIME '12:00 "+tc.token+"'", "12:00:00"))
		}
	})
}

func TestReferenceTimezoneAbbreviationsPriorityConsumers(t *testing.T) {
	timezoneAbbreviationsScenario(t, func(p *fixture.Pair, x fixture.Executor, record *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, record, s) }
		for _, tc := range []struct{ set, zone, token, want string }{{"Default", "UTC", "IST", "10:00:00"}, {"India", "UTC", "IST", "06:30:00"}, {"Australia", "UTC", "EST", "02:00:00"}, {"Australia", "America/New_York", "EST", "17:00:00"}} {
			run(fixture.Exec("SET timezone_abbreviations='"+tc.set+"'", 0))
			run(fixture.Exec("SET TIME ZONE '"+tc.zone+"'", 0))
			for _, expression := range []string{
				"TIMESTAMPTZ '2004-07-01 12:00 " + tc.token + "'",
				"TIMESTAMP '2004-07-01 12:00' AT TIME ZONE '" + tc.token + "'",
				"make_timestamptz(2004,7,1,12,0,0,'" + tc.token + "')",
				"to_timestamp('2004-07-01 12:00 " + tc.token + "','YYYY-MM-DD HH24:MI TZ')",
				"to_timestamp('2004-07-01 12:00 " + tc.token + "','YYYY-MM-DD HH24:MI tz')",
			} {
				s := timezoneTextStep("("+expression+") AT TIME ZONE 'UTC'", "2004-07-01 "+tc.want)
				run(s)
				run(fixture.Prepared(s))
			}
			run(timezoneTextStep("to_date('2004-07-01 "+tc.token+"','YYYY-MM-DD TZ')", "2004-07-01"))
		}
		run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
		run(fixture.Exec("SET timezone_abbreviations='Default'", 0))
		for _, typ := range []string{"TIME", "TIMETZ"} {
			name := "time"
			if typ == "TIMETZ" {
				name = "time with time zone"
			}
			query := "SELECT " + typ + " '12:00 SAT' FROM tz_witness"
			s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22007", Message: "invalid input syntax for type " + name + `: "12:00 SAT"`, Position: fmt.Sprint(9 + len(typ))})
			s.SelectRows, s.ErrorStage = true, "execute"
			run(s)
			s.ErrorStage = "prepare"
			run(fixture.Prepared(s))
		}
	})
}

func TestReferenceTimezoneAbbreviationsDiagnostics(t *testing.T) {
	timezoneAbbreviationsScenario(t, func(p *fixture.Pair, x fixture.Executor, record *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, record, s) }
		run(fixture.Exec("SET timezone_abbreviations='India'", 0))
		for _, value := range []string{"NoSuchSet", "default", "Australia.txt", "../Default", "Default1", "Indiä", "CustomLocal"} {
			want := fixture.Diagnostic{Code: "22023", Message: `invalid value for parameter "timezone_abbreviations": "` + value + `"`}
			query := "SET timezone_abbreviations='" + value + "'"
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(query, want)
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				run(timezoneAbbreviationsShow("India"))
				run(timezoneTextStep("TIMESTAMPTZ '2004-07-01 12:00 IST' AT TIME ZONE 'UTC'", "2004-07-01 06:30:00"))
			}
			s := fixture.ImplicitError("SELECT set_config('timezone_abbreviations',$1,false)", want, value)
			s.SelectRows, s.ErrorStage = true, "execute"
			run(fixture.Prepared(s))
		}
	})
}
