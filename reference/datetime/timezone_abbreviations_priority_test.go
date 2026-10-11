package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTimezoneAbbreviationsPrefixAndStages(t *testing.T) {
	timezoneAbbreviationsScenario(t, func(p *fixture.Pair, x fixture.Executor, record *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, record, s) }
		query := "SET timezone_abbreviations='India'"
		held := timezoneHeld(t, p, x, query)
		run(timezoneAbbreviationsShow("Default"))
		fixture.RunStep(t, p.Ctx, held, record, fixture.Exec(query, 0))
		run(timezoneAbbreviationsShow("India"))
		run(fixture.Exec("SET TIME ZONE 'ABC5DEF'", 0))
		for _, expression := range []string{
			`to_timestamp('2004-07-01 12:00 DEFtail','YYYY-MM-DD HH24:MI TZ"tail"')`,
			`to_timestamp('2004-07-01 12:00 deftail','YYYY-MM-DD HH24:MI tz"tail"')`,
			`TIMESTAMP '2004-07-01 12:00' AT LOCAL`,
		} {
			run(timezoneTextStep("("+expression+") AT TIME ZONE 'UTC'", "2004-07-01 16:00:00"))
		}
		run(timezoneTextStep("to_timestamp('2004-07-01 12:00 +05:30','YYYY-MM-DD HH24:MI TZ') AT TIME ZONE 'UTC'", "2004-07-01 06:30:00"))
		run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SET timezone_abbreviations='Australia'", 0))
		s := fixture.ImplicitError("SET timezone_abbreviations='NoSuchSet'", fixture.Diagnostic{Code: "22023", Message: `invalid value for parameter "timezone_abbreviations": "NoSuchSet"`})
		s.ErrorStage = "execute"
		run(s)
		run(fixture.Exec("ROLLBACK", 0))
		run(timezoneAbbreviationsShow("India"))
		run(timezoneTextStep("TIMESTAMPTZ '2004-07-01 12:00 IST' AT TIME ZONE 'UTC'", "2004-07-01 06:30:00"))
	})
}
