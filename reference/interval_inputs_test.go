package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceIntervalFractionAliasesAndComparisonBounds(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE iv(id INTEGER,v INTERVAL)", 0))
			fixtures := []struct{ s, out string }{{"0.125 years", "2 mons"}, {"0.375 years", "4 mons"}, {"-1.75 months", "-1 mons -22 days -12:00:00"}, {"-1.5 weeks", "-10 days -12:00:00"}, {"@ 1 day ago", "-1 days"}, {"1day", "1 day"}, {"- 1 day", "-1 days"}, {"00:00:60", "00:01:00"}, {"PT1e2S", "00:01:40"}, {"PT10", "10:00:00"}, {"P2-10-15", "2 years 10 mons 15 days"}, {"1 c", "100 years"}, {"1 decs", "10 years"}, {"1 mils", "1000 years"}, {"1 mseconds", "00:00:00.001"}, {"1 useconds", "00:00:00.000001"}, {"4:00 1.75 months", "1 mon 22 days 04:00:00"}, {"1.75 months 4:00", "1 mon 22 days 16:00:00"}}
			var want [][]any
			for i, tc := range fixtures {
				run(referencePrepared(referenceExec("INSERT INTO iv VALUES($1,$2)", 1, i+1, tc.s)))
				want = append(want, []any{int64(i + 1), tc.out})
			}
			run(referenceSelect("SELECT id,v FROM iv ORDER BY id", []string{"id", "v"}, []string{"INT4", "INTERVAL"}, want))
			run(referenceExec("CREATE TABLE bounds(a INTERVAL,b INTERVAL)", 0))
			run(referenceExec("INSERT INTO bounds VALUES('2147483647 months -2147483648 days','2147483646 months -2147483618 days')", 1))
			for _, op := range []string{"=", "<>", "<", "<=", ">", ">="} {
				s := referenceSelect(fmt.Sprintf("SELECT a%s b FROM bounds", op), []string{"?column?"}, []string{"BOOL"}, [][]any{{op == "=" || op == "<=" || op == ">="}})
				run(s)
				run(referencePrepared(s))
			}
			run(referenceExec("CREATE TABLE infin(v INTERVAL)", 0))
			run(referenceExec("INSERT INTO infin VALUES('infinity'),('1 day'),('-infinity'),(NULL)", 4))
			run(referenceSelect("SELECT v FROM infin ORDER BY v", []string{"v"}, []string{"INTERVAL"}, [][]any{{"-infinity"}, {"1 day"}, {"infinity"}, {nil}}))
			run(referenceSelect("SELECT GREATEST(v,INTERVAL '1 mon'),LEAST(v,INTERVAL '1 mon') FROM infin WHERE v=INTERVAL '1 day'", []string{"greatest", "least"}, []string{"INTERVAL", "INTERVAL"}, [][]any{{"1 mon", "1 day"}}))

		})
	}
}
