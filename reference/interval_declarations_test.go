package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceIntervalDeclarationsAndRounding(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
		query := func(q string, names, types []string, rows [][]any) {
			s := referenceSelect(q, names, types, rows)
			run(s)
			run(referencePrepared(s))
		}
		for _, prepared := range []bool{false, true} {
			for i, tc := range []struct{ decl, out string }{{"INTERVAL", "1 year 1 mon 2 days 03:04:05.6789"}, {"INTERVAL SECOND(2)", "1 year 1 mon 2 days 03:04:05.68"}, {"INTERVAL YEAR", "1 year"}, {"INTERVAL MONTH", "1 year 1 mon"}, {"INTERVAL DAY", "1 year 1 mon 2 days"}, {"INTERVAL HOUR", "1 year 1 mon 2 days 03:00:00"}, {"INTERVAL MINUTE", "1 year 1 mon 2 days 03:04:00"}, {"INTERVAL SECOND", "1 year 1 mon 2 days 03:04:05.6789"}, {"INTERVAL YEAR TO MONTH", "1 year 1 mon"}, {"INTERVAL DAY TO HOUR", "1 year 1 mon 2 days 03:00:00"}, {"INTERVAL DAY TO MINUTE", "1 year 1 mon 2 days 03:04:00"}, {"INTERVAL DAY TO SECOND(2)", "1 year 1 mon 2 days 03:04:05.68"}, {"INTERVAL HOUR TO MINUTE", "1 year 1 mon 2 days 03:04:00"}, {"INTERVAL HOUR TO SECOND(2)", "1 year 1 mon 2 days 03:04:05.68"}, {"INTERVAL MINUTE TO SECOND(2)", "1 year 1 mon 2 days 03:04:05.68"}} {
				table := fmt.Sprintf("iv%d_%t", i, prepared)
				s := referenceExec("CREATE TABLE "+table+"(v "+tc.decl+")", 0)
				s.prepared = prepared
				run(s)
				s = referenceExec("INSERT INTO "+table+" VALUES('13 months 2 days 3:04:05.6789')", 1)
				s.prepared = prepared
				run(s)
				query("SELECT v FROM "+table, []string{"v"}, []string{"INTERVAL"}, [][]any{{tc.out}})
			}
			for p, out := range []string{"00:00:01", "00:00:01.2", "00:00:01.23", "00:00:01.235", "00:00:01.2346", "00:00:01.23457", "00:00:01.234567"} {
				table := fmt.Sprintf("prec%d_%t", p, prepared)
				run(referenceExec(fmt.Sprintf("CREATE TABLE %s(v INTERVAL(%d))", table, p), 0))
				run(referenceExec("INSERT INTO "+table+" VALUES('1.234567 seconds')", 1))
				query("SELECT v FROM "+table, []string{"v"}, []string{"INTERVAL"}, [][]any{{out}})
			}
		}
	}
}

func TestReferenceIntervalRepeatedParametersAndInsertSelect(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
		run(referenceExec("CREATE TABLE repeated(v INTERVAL,d INTERVAL DAY)", 0))
		stmt, err := backend.db.PrepareContext(p.ctx, "INSERT INTO repeated VALUES($1,$1)")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		referenceRunStep(t, p.ctx, referenceHeldStatement{backend.db, stmt, "INSERT INTO repeated VALUES($1,$1)"}, backend.recorder, referenceExec("INSERT INTO repeated VALUES($1,$1)", 1, "13 months 2 days 3:04:05.6789"))
		run(referenceSelect("SELECT v,d FROM repeated", []string{"v", "d"}, []string{"INTERVAL", "INTERVAL"}, [][]any{{"1 year 1 mon 2 days 03:04:05.6789", "1 year 1 mon 2 days"}}))
		run(referenceExec("CREATE TABLE dst(v INTERVAL YEAR)", 0))
		run(referenceExec("INSERT INTO dst SELECT v FROM repeated", 1))
		run(referenceSelect("SELECT v FROM dst", []string{"v"}, []string{"INTERVAL"}, [][]any{{"1 year"}}))
	}
}
