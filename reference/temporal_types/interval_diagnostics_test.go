package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIntervalDiagnosticsAtomicityAndSyntax(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE iv(v INTERVAL)", 0))
			run(fixture.Exec("INSERT INTO iv VALUES('1 day')", 1))
			for _, tc := range []struct{ input, code, primary string }{{"invalid", "22007", "invalid input syntax for type interval"}, {"1 second 2 seconds", "22007", "invalid input syntax for type interval"}, {"5.5 seconds 3 milliseconds", "22007", "invalid input syntax for type interval"}, {"@ 30 eons ago", "22007", "invalid input syntax for type interval"}, {"P", "22007", "invalid input syntax for type interval"}, {"PT1Y", "22007", "invalid input syntax for type interval"}, {"2147483648 days", "22015", "interval field value out of range"}, {"2147483648 months", "22015", "interval field value out of range"}, {"9223372036854775808 microseconds", "22015", "interval field value out of range"}, {"2147483647 years", "22008", "interval out of range"}} {
				message := tc.primary
				if tc.code != "22008" {
					message += ": \"" + tc.input + "\""
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError("INSERT INTO iv VALUES('"+tc.input+"')", fixture.Diagnostic{Code: tc.code, Message: message, Position: "23"})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
					run(fixture.Select("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{"1 day"}}))
					late := fixture.ImplicitError("INSERT INTO iv VALUES('1 mon'),('"+tc.input+"')", fixture.Diagnostic{Code: tc.code, Message: message, Position: "33"})
					late.Prepared, late.ErrorStage = prepared, "execute"
					if prepared {
						late.ErrorStage = "prepare"
					}
					run(late)
					run(fixture.Select("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{"1 day"}}))
					s = fixture.ImplicitError("INSERT INTO iv VALUES($1),($2)", fixture.Diagnostic{Code: tc.code, Message: message}, "1 mon", tc.input)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					pg := *s.Diagnostic
					pg.Context = "unnamed portal parameter $2 = '...'"
					s.Diagnostic = &pg
					run(s)
					run(fixture.Select("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{"1 day"}}))
				}
				run(fixture.Select("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{"1 day"}}))
			}
			for _, tc := range []struct{ q, code, message, position string }{{"SELECT INTERVAL 'invalid' FROM iv", "22007", "invalid input syntax for type interval: \"invalid\"", "17"}, {"SELECT /*é*/ INTERVAL 'invalid' FROM iv", "22007", "invalid input syntax for type interval: \"invalid\"", "23"}, {"CREATE TABLE ivbad(v INTERVAL MONTH TO DAY)", "42601", "syntax error at or near \"TO\"", "37"}, {"CREATE TABLE ivbad(v INTERVAL YEAR(2))", "42601", "syntax error at or near \"(\"", "35"}, {"CREATE TABLE ivbad(v INTERVAL(-1))", "42601", "syntax error at or near \"-\"", "31"}, {`CREATE TABLE ivbad(v "interval"(0))`, "22023", "invalid INTERVAL type modifier", "22"}, {`CREATE TABLE ivbad(v "interval"(32767,-1))`, "22023", "INTERVAL(-1) precision must not be negative", "22"}} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared && tc.code != "22023" {
						s.ErrorStage = "prepare"
					}
					run(s)
					run(fixture.Select("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{"1 day"}}))
				}
			}
			run(fixture.Exec("CREATE TABLE ivbad(v INTERVAL)", 0))
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("SET IntervalStyle=invalid", fixture.Diagnostic{Code: "22023", Message: "invalid value for parameter \"IntervalStyle\": \"invalid\"", Hint: "Available values: postgres, postgres_verbose, sql_standard, iso_8601."})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Select("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{"1 day"}}))
			}
			run(fixture.Exec("CREATE TABLE nn(v INTERVAL NOT NULL)", 0))
			run(fixture.ImplicitError("INSERT INTO nn VALUES('1 day'),(NULL)", fixture.Diagnostic{Code: "23502", Message: "null value in column \"v\" of relation \"nn\" violates not-null constraint", Detail: "Failing row contains (null).", Schema: "public", Table: "nn", Column: "v"}))
			run(fixture.Select("SELECT v FROM nn", []string{"v"}, []string{"INTERVAL"}, nil))
			tx, e := backend.DB.BeginTx(p.Ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Exec("SET LOCAL IntervalStyle=iso_8601", 0))
			fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Select("SHOW IntervalStyle", []string{"IntervalStyle"}, []string{"TEXT"}, [][]any{{"iso_8601"}}))
			fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.ImplicitError("INSERT INTO iv VALUES('invalid')", fixture.Diagnostic{Code: "22007", Message: "invalid input syntax for type interval: \"invalid\"", Position: "23"}))
			fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.ImplicitError("INSERT INTO iv VALUES('1 day')", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
			if e = tx.Rollback(); e != nil {
				t.Fatal(e)
			}
			run(fixture.Select("SHOW IntervalStyle", []string{"IntervalStyle"}, []string{"TEXT"}, [][]any{{"postgres"}}))
		})
	}
}

func TestReferenceIntervalMalformedYearMonth(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE iv(v INTERVAL)", 0))
		for _, prepared := range []bool{false, true} {
			step := fixture.ImplicitError("SELECT INTERVAL '1-foo' FROM iv", fixture.Diagnostic{Code: "22007", Message: "invalid input syntax for type interval: \"1-foo\"", Position: "17"})
			step.Prepared = prepared
			step.ErrorStage = "execute"
			if prepared {
				step.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
		}
	}
}
