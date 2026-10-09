package reference_test

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReferenceTimeDiagnosticsAndAtomicity(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) {
				referenceTemporalRunStep(t, p.ctx, backend.db, backend.recorder, s)
				if s.state != "" {
					referenceTemporalRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT v FROM t", []string{"v"}, []string{"TIME"}, referenceTimeRows(t, backend.pg, []string{"TIME"}, [][]any{{"00:00:00"}})))
				}
			}
			run(referenceExec("CREATE TABLE t(v TIME)", 0))
			run(referenceExec("INSERT INTO t VALUES('00:00')", 1))
			paramQuery := "INSERT INTO t VALUES($1)"
			param, err := backend.db.PrepareContext(p.ctx, paramQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := param.Close(); err != nil {
					t.Error(err)
				}
			}()
			held := referenceHeldStatement{referenceExecutor: backend.db, statement: param, query: paramQuery}
			for _, tc := range []struct{ input, code, primary, hint string }{
				{"25:00", "22008", "date/time field value out of range", ""}, {"12:60", "22008", "date/time field value out of range", ""}, {"12:00:61", "22008", "date/time field value out of range", ""}, {"24:01", "22008", "date/time field value out of range", ""}, {"24:00:00.1", "22008", "date/time field value out of range", ""}, {"23:59:60.01", "22008", "date/time field value out of range", ""},
				{"250000", "22008", "date/time field value out of range", ""}, {"049900", "22008", "date/time field value out of range", ""}, {"040599", "22008", "date/time field value out of range", ""}, {"13:00 PM", "22008", "date/time field value out of range", ""},
				{"bad", "22007", "invalid input syntax for type time", ""}, {"04:05:06+16", "22009", "time zone displacement out of range", ""}, {"15:36:39 America/New_York", "22007", "invalid input syntax for type time", ""}, {"04:05 Asia/Kolkata", "22007", "invalid input syntax for type time", ""}, {"04:05 X0Y", "22007", "invalid input syntax for type time", ""},
				{"04:05 AM PM", "22007", "invalid input syntax for type time", ""}, {"04:05 UTC UTC", "22007", "invalid input syntax for type time", ""}, {"04:05 EDT DST", "22007", "invalid input syntax for type time", ""}, {"04:05 MSK DST", "22007", "invalid input syntax for type time", ""}, {"04:05 Etc/UTC DST", "22007", "invalid input syntax for type time", ""}, {"allballs UTC", "22007", "invalid input syntax for type time", ""},
				{"epoch", "22007", "invalid input syntax for type time", ""}, {"today", "22007", "invalid input syntax for type time", ""}, {"infinity", "22007", "invalid input syntax for type time", ""}, {"2003-02-29 04:05", "22008", "date/time field value out of range", ""}, {"2003-13-07 04:05", "22008", "date/time field value out of range", "Perhaps you need a different \"DateStyle\" setting."},
				{"04:05 on Monday", "22007", "invalid input syntax for type time", ""}, {"Monday 04:05", "22007", "invalid input syntax for type time", ""},
				{"2003-07-07 04:05 Monday", "22007", "invalid input syntax for type time", ""},
			} {
				t.Run(tc.input, func(t *testing.T) {
					want := referenceDiagnostic{Code: tc.code, Message: tc.primary + ": \"" + tc.input + "\"", Hint: tc.hint, Position: "22"}
					q := "INSERT INTO t VALUES('" + tc.input + "')"
					for _, prepared := range []bool{false, true} {
						s := referenceImplicitError(q, want)
						s.prepared = prepared
						s.errorStage = "execute"
						if prepared {
							s.errorStage = "prepare"
						}
						run(s)
					}
					want.Position, want.Context = "", "unnamed portal parameter $1 = '...'"
					s := referenceImplicitError(paramQuery, want, tc.input)
					s.errorStage = "execute"
					referenceTemporalRunStep(t, p.ctx, held, backend.recorder, s)
					run(referenceSelect("SELECT v FROM t", []string{"v"}, []string{"TIME"}, referenceTimeRows(t, backend.pg, []string{"TIME"}, [][]any{{"00:00:00"}})))
					for _, prepared := range []bool{false, true} {
						s := referenceImplicitError("INSERT INTO t VALUES($1)", want, tc.input)
						s.prepared = prepared
						s.errorStage = "execute"
						pg := *s.diagnostic
						pg.Context = "unnamed portal parameter $1 = '...'"
						s.diagnostic = &pg
						run(s)
					}
				})
			}
			for _, input := range []string{"15:36:39 America/Bogus", "15:36:39 AMERICA/BOGUS"} {
				want := referenceDiagnostic{Code: "22023", Message: "time zone \"america/bogus\" not recognized", Position: "22"}
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError("INSERT INTO t VALUES('"+input+"')", want)
					s.prepared = prepared
					s.errorStage = "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					run(s)
				}
				want.Position, want.Context = "", "unnamed portal parameter $1 = '...'"
				s := referenceImplicitError(paramQuery, want, input)
				s.errorStage = "execute"
				referenceTemporalRunStep(t, p.ctx, held, backend.recorder, s)
				run(referenceSelect("SELECT v FROM t", []string{"v"}, []string{"TIME"}, referenceTimeRows(t, backend.pg, []string{"TIME"}, [][]any{{"00:00:00"}})))
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError("INSERT INTO t VALUES($1)", want, input)
					s.prepared = prepared
					s.errorStage = "execute"
					pg := *s.diagnostic
					pg.Context = "unnamed portal parameter $1 = '...'"
					s.diagnostic = &pg
					run(s)
				}
			}
		})
	}
}

func TestReferenceTimeDiagnosticBoundariesAndTransaction(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceTemporalRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(v TIME)", 0))
			run(referenceExec("INSERT INTO t VALUES('00:00')", 1))
			for _, tc := range []struct{ q, code, message, position, detail, hint string }{
				{"SELECT TIME 'bad' FROM t", "22007", "invalid input syntax for type time: \"bad\"", "13", "", ""},
				{"SELECT TIME(0) '24:00:00.1' FROM t", "22008", "date/time field value out of range: \"24:00:00.1\"", "16", "", ""},
				{"SELECT $1 FROM t WHERE v=$1", "42P08", "inconsistent types deduced for parameter $1", "8", "time without time zone versus text", ""},
				{"SELECT v FROM t WHERE v=TIMESTAMP '2003-07-07'", "42883", "operator does not exist: time without time zone = timestamp without time zone", "24", "", "No operator matches the given name and argument types. You might need to add explicit type casts."},
			} {
				s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: tc.position, Detail: tc.detail, Hint: tc.hint})
				s.prepared = true
				s.selectRows = true
				s.errorStage = "prepare"
				run(s)
				if tc.code == "42883" {
					s.prepared, s.selectRows, s.errorStage = false, false, "execute"
					run(s)
				}
			}
			q := "/*é*/ INSERT INTO t VALUES('bad')"
			run(referenceImplicitError(q, referenceDiagnostic{Code: "22007", Message: "invalid input syntax for type time: \"bad\"", Position: strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "'bad'")]) + 1)}))
			run(referenceImplicitError("INSERT INTO t VALUES('24:00'),('bad')", referenceDiagnostic{Code: "22007", Message: "invalid input syntax for type time: \"bad\"", Position: "32"}))
			parameter := referenceImplicitError("INSERT INTO t VALUES($1),($2)", referenceDiagnostic{Code: "22007", Message: "invalid input syntax for type time: \"bad\""}, "24:00", "bad")
			pg := *parameter.diagnostic
			pg.Context = "unnamed portal parameter $2 = '...'"
			parameter.diagnostic = &pg
			run(parameter)
			run(referenceSelect("SELECT v FROM t", []string{"v"}, []string{"TIME"}, referenceTimeRows(t, backend.pg, []string{"TIME"}, [][]any{{"00:00:00"}})))
			run(referenceExec("CREATE TABLE nn(v TIME NOT NULL)", 0))
			run(referenceImplicitError("INSERT INTO nn VALUES(NULL)", referenceDiagnostic{Code: "23502", Message: "null value in column \"v\" of relation \"nn\" violates not-null constraint", Detail: "Failing row contains (null).", Schema: "public", Table: "nn", Column: "v"}))
			run(referenceImplicitError(`CREATE TABLE later(a TIME,v "time"(-1))`, referenceDiagnostic{Code: "22023", Message: "TIME(-1) precision must not be negative", Position: "29"}))
			run(referenceExec("CREATE TABLE later(a TIME,v TIME)", 0))
			tx, err := backend.db.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceTemporalRunStep(t, p.ctx, tx, backend.recorder, referenceImplicitError("INSERT INTO t VALUES('bad')", referenceDiagnostic{Code: "22007", Message: "invalid input syntax for type time: \"bad\"", Position: "22"}))
			referenceTemporalRunStep(t, p.ctx, tx, backend.recorder, referenceImplicitError("INSERT INTO t VALUES('24:00')", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
