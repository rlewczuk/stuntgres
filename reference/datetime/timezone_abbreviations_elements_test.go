package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTimezoneAbbreviationsElementsAndHistory(t *testing.T) {
	timezoneAbbreviationsScenario(t, func(p *fixture.Pair, x fixture.Executor, record *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, record, s) }
		run(fixture.Exec("CREATE DOMAIN abbreviation_instant AS TIMESTAMPTZ", 0))
		query := "SELECT (($1::abbreviation_instant)::TIMESTAMPTZ AT TIME ZONE 'UTC')::TEXT AS v FROM tz_witness"
		held := timezoneHeld(t, p, x, query)
		arrayQuery := "SELECT ($1::TIMESTAMPTZ[])::TEXT AS v FROM tz_witness"
		array := timezoneHeld(t, p, x, arrayQuery)
		for _, tc := range []struct{ set, clock string }{{"Default", "10:00:00"}, {"India", "06:30:00"}, {"Australia", "10:00:00"}} {
			run(fixture.Exec("SET timezone_abbreviations='"+tc.set+"'", 0))
			fixture.RunStep(t, p.Ctx, held, record, fixture.Select(query, []string{"v"}, []string{"TEXT"}, [][]any{{"2004-07-01 " + tc.clock}}, "2004-07-01 12:00 IST"))
			fixture.RunStep(t, p.Ctx, array, record, fixture.Select(arrayQuery, []string{"v"}, []string{"TEXT"}, [][]any{{`{"2004-07-01 ` + tc.clock + `+00",NULL}`}}, `{"2004-07-01 12:00 IST",NULL}`))
			for _, history := range []struct{ year, clock string }{{"2010", "09:00:00"}, {"2012", "08:00:00"}, {"2015", "09:00:00"}} {
				run(timezoneTextStep("TIMESTAMPTZ '"+history.year+"-07-01 12:00 MSK' AT TIME ZONE 'UTC'", history.year+"-07-01 "+history.clock))
			}
		}
	})
}
