package reference_test

import "testing"

func TestReferenceTimezoneAbbreviationsPrefixAndStages(t *testing.T) {
	timezoneAbbreviationsScenario(t, func(p *referencePair, x referenceExecutor, record *referenceRecorder, pg bool) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, x, record, s) }
		query := "SET timezone_abbreviations='India'"
		held := timezoneHeld(t, p, x, query)
		run(timezoneAbbreviationsShow("Default"))
		referenceRunStep(t, p.ctx, held, record, referenceExec(query, 0))
		run(timezoneAbbreviationsShow("India"))
		run(referenceExec("SET TIME ZONE 'ABC5DEF'", 0))
		for _, expression := range []string{
			`to_timestamp('2004-07-01 12:00 DEFtail','YYYY-MM-DD HH24:MI TZ"tail"')`,
			`to_timestamp('2004-07-01 12:00 deftail','YYYY-MM-DD HH24:MI tz"tail"')`,
			`TIMESTAMP '2004-07-01 12:00' AT LOCAL`,
		} {
			run(timezoneTextStep("("+expression+") AT TIME ZONE 'UTC'", "2004-07-01 16:00:00"))
		}
		run(timezoneTextStep("to_timestamp('2004-07-01 12:00 +05:30','YYYY-MM-DD HH24:MI TZ') AT TIME ZONE 'UTC'", "2004-07-01 06:30:00"))
		run(referenceExec("SET TIME ZONE 'UTC'", 0))
		run(referenceExec("BEGIN", 0))
		run(referenceExec("SET timezone_abbreviations='Australia'", 0))
		s := referenceImplicitError("SET timezone_abbreviations='NoSuchSet'", referenceDiagnostic{Code: "22023", Message: `invalid value for parameter "timezone_abbreviations": "NoSuchSet"`})
		s.errorStage = "execute"
		run(s)
		run(referenceExec("ROLLBACK", 0))
		run(timezoneAbbreviationsShow("India"))
		run(timezoneTextStep("TIMESTAMPTZ '2004-07-01 12:00 IST' AT TIME ZONE 'UTC'", "2004-07-01 06:30:00"))
	})
}
