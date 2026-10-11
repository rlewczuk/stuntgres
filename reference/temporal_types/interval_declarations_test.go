package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIntervalDeclarationsAndRounding(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		query := func(q string, names, types []string, rows [][]any) {
			s := fixture.Select(q, names, types, rows)
			run(s)
			run(fixture.Prepared(s))
		}
		for _, prepared := range []bool{false, true} {
			for i, tc := range []struct{ decl, out string }{{"INTERVAL", "1 year 1 mon 2 days 03:04:05.6789"}, {"INTERVAL SECOND(2)", "1 year 1 mon 2 days 03:04:05.68"}, {"INTERVAL YEAR", "1 year"}, {"INTERVAL MONTH", "1 year 1 mon"}, {"INTERVAL DAY", "1 year 1 mon 2 days"}, {"INTERVAL HOUR", "1 year 1 mon 2 days 03:00:00"}, {"INTERVAL MINUTE", "1 year 1 mon 2 days 03:04:00"}, {"INTERVAL SECOND", "1 year 1 mon 2 days 03:04:05.6789"}, {"INTERVAL YEAR TO MONTH", "1 year 1 mon"}, {"INTERVAL DAY TO HOUR", "1 year 1 mon 2 days 03:00:00"}, {"INTERVAL DAY TO MINUTE", "1 year 1 mon 2 days 03:04:00"}, {"INTERVAL DAY TO SECOND(2)", "1 year 1 mon 2 days 03:04:05.68"}, {"INTERVAL HOUR TO MINUTE", "1 year 1 mon 2 days 03:04:00"}, {"INTERVAL HOUR TO SECOND(2)", "1 year 1 mon 2 days 03:04:05.68"}, {"INTERVAL MINUTE TO SECOND(2)", "1 year 1 mon 2 days 03:04:05.68"}} {
				table := fmt.Sprintf("iv%d_%t", i, prepared)
				s := fixture.Exec("CREATE TABLE "+table+"(v "+tc.decl+")", 0)
				s.Prepared = prepared
				run(s)
				s = fixture.Exec("INSERT INTO "+table+" VALUES('13 months 2 days 3:04:05.6789')", 1)
				s.Prepared = prepared
				run(s)
				query("SELECT v FROM "+table, []string{"v"}, []string{"INTERVAL"}, [][]any{{tc.out}})
			}
			for p, out := range []string{"00:00:01", "00:00:01.2", "00:00:01.23", "00:00:01.235", "00:00:01.2346", "00:00:01.23457", "00:00:01.234567"} {
				table := fmt.Sprintf("prec%d_%t", p, prepared)
				run(fixture.Exec(fmt.Sprintf("CREATE TABLE %s(v INTERVAL(%d))", table, p), 0))
				run(fixture.Exec("INSERT INTO "+table+" VALUES('1.234567 seconds')", 1))
				query("SELECT v FROM "+table, []string{"v"}, []string{"INTERVAL"}, [][]any{{out}})
			}
		}
	}
}

func TestReferenceIntervalRepeatedParametersAndInsertSelect(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		run(fixture.Exec("CREATE TABLE repeated(v INTERVAL,d INTERVAL DAY)", 0))
		stmt, err := backend.DB.PrepareContext(p.Ctx, "INSERT INTO repeated VALUES($1,$1)")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{backend.DB, stmt, "INSERT INTO repeated VALUES($1,$1)"}, backend.Recorder, fixture.Exec("INSERT INTO repeated VALUES($1,$1)", 1, "13 months 2 days 3:04:05.6789"))
		run(fixture.Select("SELECT v,d FROM repeated", []string{"v", "d"}, []string{"INTERVAL", "INTERVAL"}, [][]any{{"1 year 1 mon 2 days 03:04:05.6789", "1 year 1 mon 2 days"}}))
		run(fixture.Exec("CREATE TABLE dst(v INTERVAL YEAR)", 0))
		run(fixture.Exec("INSERT INTO dst SELECT v FROM repeated", 1))
		run(fixture.Select("SELECT v FROM dst", []string{"v"}, []string{"INTERVAL"}, [][]any{{"1 year"}}))
	}
}
