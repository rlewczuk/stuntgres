package reference_test

import (
	"testing"
	"time"
)

func TestReferenceTimezonePreparedFreezeAndDynamicParse(t *testing.T) {
	p := timezoneReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE tz_freeze(id INTEGER,z TIMESTAMP(3) WITH TIME ZONE,q TIMETZ(3))", 0))
	for _, backend := range p.backends() {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
		run(referenceExec("SET TIME ZONE 'America/New_York'", 0))
		frozenQuery := "SELECT TIMESTAMPTZ '2004-07-01 12:00'::TEXT AS z,TIMETZ '05:34:17-05' AS q FROM tz_freeze WHERE id=1"
		frozen := timezoneHeld(t, p, backend.db, frozenQuery)
		insertQuery := "INSERT INTO tz_freeze VALUES($1,$2,$3)"
		insert := timezoneHeld(t, p, backend.db, insertQuery)
		for _, args := range [][]any{{1, "2004-07-01 12:00", "05:34:17-05"}, {2, time.Date(2004, 7, 1, 18, 0, 0, 123500000, time.FixedZone("east", 7200)), nil}, {3, nil, nil}} {
			referenceRunStep(t, p.ctx, insert, backend.recorder, referenceExec(insertQuery, 1, args...))
		}
		run(referenceExec("SET TIME ZONE 'UTC'", 0))
		var q any = "05:34:17-05"
		if backend.pg {
			q = time.Date(0, 1, 1, 10, 34, 17, 0, time.UTC)
		}
		referenceRunStep(t, p.ctx, frozen, backend.recorder, referenceSelect(frozenQuery, []string{"z", "q"}, []string{"TEXT", "TIMETZ"}, [][]any{{"2004-07-01 16:00:00+00", q}}))
		run(referenceSelect("SELECT z::TEXT AS z,q FROM tz_freeze ORDER BY id", []string{"z", "q"}, []string{"TEXT", "TIMETZ"}, [][]any{{"2004-07-01 16:00:00+00", q}, {"2004-07-01 16:00:00.124+00", nil}, {nil, nil}}))
		parseQuery := "SELECT $1::TIMESTAMPTZ::TEXT AS z FROM tz_freeze WHERE id=1"
		parse := timezoneHeld(t, p, backend.db, parseQuery)
		for _, tc := range []struct{ zone, want string }{{"UTC", "2004-07-01 12:00:00+00"}, {"America/New_York", "2004-07-01 12:00:00-04"}} {
			run(referenceExec("SET TIME ZONE '"+tc.zone+"'", 0))
			referenceRunStep(t, p.ctx, parse, backend.recorder, referenceSelect(parseQuery, []string{"z"}, []string{"TEXT"}, [][]any{{tc.want}}, "2004-07-01 12:00"))
		}
	}
}
