package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceTimeDiagnosticsAndAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) {
				fixture.TemporalRunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				if s.State != "" {
					fixture.TemporalRunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT v FROM t", []string{"v"}, []string{"TIME"}, fixture.TimeRows(t, backend.PG, []string{"TIME"}, [][]any{{"00:00:00"}})))
				}
			}
			run(fixture.Exec("CREATE TABLE t(v TIME)", 0))
			run(fixture.Exec("INSERT INTO t VALUES('00:00')", 1))
			paramQuery := "INSERT INTO t VALUES($1)"
			param, err := backend.DB.PrepareContext(p.Ctx, paramQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := param.Close(); err != nil {
					t.Error(err)
				}
			}()
			held := fixture.HeldStatement{Executor: backend.DB, Statement: param, SQL: paramQuery}
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
					want := fixture.Diagnostic{Code: tc.code, Message: tc.primary + ": \"" + tc.input + "\"", Hint: tc.hint, Position: "22"}
					q := "INSERT INTO t VALUES('" + tc.input + "')"
					for _, prepared := range []bool{false, true} {
						s := fixture.ImplicitError(q, want)
						s.Prepared = prepared
						s.ErrorStage = "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						run(s)
					}
					want.Position, want.Context = "", "unnamed portal parameter $1 = '...'"
					s := fixture.ImplicitError(paramQuery, want, tc.input)
					s.ErrorStage = "execute"
					fixture.TemporalRunStep(t, p.Ctx, held, backend.Recorder, s)
					run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"TIME"}, fixture.TimeRows(t, backend.PG, []string{"TIME"}, [][]any{{"00:00:00"}})))
					for _, prepared := range []bool{false, true} {
						s := fixture.ImplicitError("INSERT INTO t VALUES($1)", want, tc.input)
						s.Prepared = prepared
						s.ErrorStage = "execute"
						pg := *s.Diagnostic
						pg.Context = "unnamed portal parameter $1 = '...'"
						s.Diagnostic = &pg
						run(s)
					}
				})
			}
			for _, input := range []string{"15:36:39 America/Bogus", "15:36:39 AMERICA/BOGUS"} {
				want := fixture.Diagnostic{Code: "22023", Message: "time zone \"america/bogus\" not recognized", Position: "22"}
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError("INSERT INTO t VALUES('"+input+"')", want)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
				}
				want.Position, want.Context = "", "unnamed portal parameter $1 = '...'"
				s := fixture.ImplicitError(paramQuery, want, input)
				s.ErrorStage = "execute"
				fixture.TemporalRunStep(t, p.Ctx, held, backend.Recorder, s)
				run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"TIME"}, fixture.TimeRows(t, backend.PG, []string{"TIME"}, [][]any{{"00:00:00"}})))
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError("INSERT INTO t VALUES($1)", want, input)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					pg := *s.Diagnostic
					pg.Context = "unnamed portal parameter $1 = '...'"
					s.Diagnostic = &pg
					run(s)
				}
			}
		})
	}
}

func TestReferenceTimeDiagnosticBoundariesAndTransaction(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.TemporalRunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(v TIME)", 0))
			run(fixture.Exec("INSERT INTO t VALUES('00:00')", 1))
			for _, tc := range []struct{ q, code, message, position, detail, hint string }{
				{"SELECT TIME 'bad' FROM t", "22007", "invalid input syntax for type time: \"bad\"", "13", "", ""},
				{"SELECT TIME(0) '24:00:00.1' FROM t", "22008", "date/time field value out of range: \"24:00:00.1\"", "16", "", ""},
				{"SELECT $1 FROM t WHERE v=$1", "42P08", "inconsistent types deduced for parameter $1", "8", "time without time zone versus text", ""},
				{"SELECT v FROM t WHERE v=TIMESTAMP '2003-07-07'", "42883", "operator does not exist: time without time zone = timestamp without time zone", "24", "", "No operator matches the given name and argument types. You might need to add explicit type casts."},
			} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position, Detail: tc.detail, Hint: tc.hint})
				s.Prepared = true
				s.SelectRows = true
				s.ErrorStage = "prepare"
				run(s)
				if tc.code == "42883" {
					s.Prepared, s.SelectRows, s.ErrorStage = false, false, "execute"
					run(s)
				}
			}
			q := "/*é*/ INSERT INTO t VALUES('bad')"
			run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "22007", Message: "invalid input syntax for type time: \"bad\"", Position: strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "'bad'")]) + 1)}))
			run(fixture.ImplicitError("INSERT INTO t VALUES('24:00'),('bad')", fixture.Diagnostic{Code: "22007", Message: "invalid input syntax for type time: \"bad\"", Position: "32"}))
			parameter := fixture.ImplicitError("INSERT INTO t VALUES($1),($2)", fixture.Diagnostic{Code: "22007", Message: "invalid input syntax for type time: \"bad\""}, "24:00", "bad")
			pg := *parameter.Diagnostic
			pg.Context = "unnamed portal parameter $2 = '...'"
			parameter.Diagnostic = &pg
			run(parameter)
			run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"TIME"}, fixture.TimeRows(t, backend.PG, []string{"TIME"}, [][]any{{"00:00:00"}})))
			run(fixture.Exec("CREATE TABLE nn(v TIME NOT NULL)", 0))
			run(fixture.ImplicitError("INSERT INTO nn VALUES(NULL)", fixture.Diagnostic{Code: "23502", Message: "null value in column \"v\" of relation \"nn\" violates not-null constraint", Detail: "Failing row contains (null).", Schema: "public", Table: "nn", Column: "v"}))
			run(fixture.ImplicitError(`CREATE TABLE later(a TIME,v "time"(-1))`, fixture.Diagnostic{Code: "22023", Message: "TIME(-1) precision must not be negative", Position: "29"}))
			run(fixture.Exec("CREATE TABLE later(a TIME,v TIME)", 0))
			tx, err := backend.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.TemporalRunStep(t, p.Ctx, tx, backend.Recorder, fixture.ImplicitError("INSERT INTO t VALUES('bad')", fixture.Diagnostic{Code: "22007", Message: "invalid input syntax for type time: \"bad\"", Position: "22"}))
			fixture.TemporalRunStep(t, p.Ctx, tx, backend.Recorder, fixture.ImplicitError("INSERT INTO t VALUES('24:00')", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
