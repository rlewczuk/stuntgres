//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceIntervalDiagnosticsAtomicityAndSyntax(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		name   string
		db     *sql.DB
		record *referenceRecorder
	}{{"native", p.memory, p.memoryRecord}, {"postgres", p.postgres, p.postgresRecord}} {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.record, s) }
			run(referenceExec("CREATE TABLE iv(v INTERVAL)", 0))
			run(referenceExec("INSERT INTO iv VALUES('1 day')", 1))
			for _, tc := range []struct{ input, code, primary string }{{"invalid", "22007", "invalid input syntax for type interval"}, {"1 second 2 seconds", "22007", "invalid input syntax for type interval"}, {"5.5 seconds 3 milliseconds", "22007", "invalid input syntax for type interval"}, {"@ 30 eons ago", "22007", "invalid input syntax for type interval"}, {"P", "22007", "invalid input syntax for type interval"}, {"PT1Y", "22007", "invalid input syntax for type interval"}, {"2147483648 days", "22015", "interval field value out of range"}, {"2147483648 months", "22015", "interval field value out of range"}, {"9223372036854775808 microseconds", "22015", "interval field value out of range"}, {"2147483647 years", "22008", "interval out of range"}} {
				message := tc.primary
				if tc.code != "22008" {
					message += ": \"" + tc.input + "\""
				}
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError("INSERT INTO iv VALUES('"+tc.input+"')", referenceDiagnostic{Code: tc.code, Message: message, Position: "23"})
					s.prepared = prepared
					s.errorStage = "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					run(s)
					s = referenceImplicitError("INSERT INTO iv VALUES($1),($2)", referenceDiagnostic{Code: tc.code, Message: message}, "1 mon", tc.input)
					s.prepared = prepared
					s.errorStage = "execute"
					pg := *s.diagnostic
					pg.Context = "unnamed portal parameter $2 = '...'"
					s.postgresDiagnostic = &pg
					run(s)
				}
				run(referenceSelect("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{"1 day"}}))
			}
			for _, tc := range []struct{ q, code, message, position string }{{"SELECT INTERVAL 'invalid' FROM iv", "22007", "invalid input syntax for type interval: \"invalid\"", "17"}, {"SELECT /*é*/ INTERVAL 'invalid' FROM iv", "22007", "invalid input syntax for type interval: \"invalid\"", "23"}, {"CREATE TABLE ivbad(v INTERVAL MONTH TO DAY)", "42601", "syntax error at or near \"TO\"", "37"}, {"CREATE TABLE ivbad(v INTERVAL YEAR(2))", "42601", "syntax error at or near \"(\"", "35"}, {"CREATE TABLE ivbad(v INTERVAL(-1))", "42601", "syntax error at or near \"-\"", "31"}, {`CREATE TABLE ivbad(v "interval"(0))`, "22023", "invalid INTERVAL type modifier", "22"}, {`CREATE TABLE ivbad(v "interval"(32767,-1))`, "22023", "INTERVAL(-1) precision must not be negative", "22"}} {
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
					s.prepared = prepared
					s.errorStage = "execute"
					if prepared && tc.code != "22023" {
						s.errorStage = "prepare"
					}
					run(s)
				}
			}
			run(referenceExec("CREATE TABLE ivbad(v INTERVAL)", 0))
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError("SET IntervalStyle=invalid", referenceDiagnostic{Code: "22023", Message: "invalid value for parameter \"IntervalStyle\": \"invalid\"", Hint: "Available values: postgres, postgres_verbose, sql_standard, iso_8601."})
				s.prepared = prepared
				s.errorStage = "execute"
				run(s)
			}
			run(referenceExec("CREATE TABLE nn(v INTERVAL NOT NULL)", 0))
			run(referenceImplicitError("INSERT INTO nn VALUES('1 day'),(NULL)", referenceDiagnostic{Code: "23502", Message: "null value in column \"v\" of relation \"nn\" violates not-null constraint", Detail: "Failing row contains (null).", Schema: "public", Table: "nn", Column: "v"}))
			run(referenceSelect("SELECT v FROM nn", []string{"v"}, []string{"INTERVAL"}, nil))
			tx, e := backend.db.BeginTx(p.ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			referenceRunStep(t, p.ctx, tx, backend.record, referenceExec("SET LOCAL IntervalStyle=iso_8601", 0))
			referenceRunStep(t, p.ctx, tx, backend.record, referenceSelect("SHOW IntervalStyle", []string{"IntervalStyle"}, []string{"TEXT"}, [][]any{{"iso_8601"}}))
			referenceRunStep(t, p.ctx, tx, backend.record, referenceImplicitError("INSERT INTO iv VALUES('invalid')", referenceDiagnostic{Code: "22007", Message: "invalid input syntax for type interval: \"invalid\"", Position: "23"}))
			referenceRunStep(t, p.ctx, tx, backend.record, referenceImplicitError("INSERT INTO iv VALUES('1 day')", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
			if e = tx.Rollback(); e != nil {
				t.Fatal(e)
			}
			run(referenceSelect("SHOW IntervalStyle", []string{"IntervalStyle"}, []string{"TEXT"}, [][]any{{"postgres"}}))
		})
	}
}

func TestReferenceIntervalMalformedYearMonth(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		db     *sql.DB
		record *referenceRecorder
	}{{p.memory, p.memoryRecord}, {p.postgres, p.postgresRecord}} {
		referenceRunStep(t, p.ctx, backend.db, backend.record, referenceExec("CREATE TABLE iv(v INTERVAL)", 0))
		for _, prepared := range []bool{false, true} {
			step := referenceImplicitError("SELECT INTERVAL '1-foo' FROM iv", referenceDiagnostic{Code: "22007", Message: "invalid input syntax for type interval: \"1-foo\"", Position: "17"})
			step.prepared = prepared
			step.errorStage = "execute"
			if prepared {
				step.errorStage = "prepare"
			}
			referenceRunStep(t, p.ctx, backend.db, backend.record, step)
		}
	}
}
