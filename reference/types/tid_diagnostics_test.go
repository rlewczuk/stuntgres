package reference_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func tidDiagnostic(input string) fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type tid: "`+input+`"`)
}

func TestReferenceTIDDiagnostics(t *testing.T) {
	tidScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE tid_errors(id integer,v tid)", 0))
		run(fixture.Exec("INSERT INTO tid_errors VALUES(1,'(1,2)')", 1))
		for _, input := range []string{"", "bad", "(1)", "(1,2", "(1,2,3)", "(0x1,2)", "(4294967296,1)", "(-2147483649,1)", "(1,65536)", "(1,-1)", "(18446744073709551616,1)", "(1 ,2)", "(1,2 )", "( ,1)", "(1, )", "bad\"\\\n"} {
			for _, prefix := range []string{"", "/*é*/ "} {
				q := prefix + "SELECT " + tidQuote(input) + "::tid"
				d := tidDiagnostic(input)
				d.Position = fixture.Position(8 + utf8.RuneCountInString(prefix))
				for _, query := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(q, d)
					s.SelectRows = query
					s.ErrorStage = "execute"
					run(s)
					s.Prepared = true
					s.ErrorStage = "prepare"
					run(s)
				}
			}
			for _, arg := range []any{input, []byte(input)} {
				d := tidDiagnostic(input)
				d.Context = "unnamed portal parameter $1 = '...'"
				if input == "" {
					d.Context = "unnamed portal parameter $1 = ''"
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("SELECT $1::tid", d, arg)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					s.SelectRows = true
					run(s)
				}
			}
		}
		for _, tc := range []struct{ q, pos string }{{"SELECT TID 'bad'", "12"}, {"SELECT 'bad'::tid FROM tid_errors WHERE FALSE", "8"}} {
			d := tidDiagnostic("bad")
			d.Position = tc.pos
			s := fixture.ErrorDiagnostic(tc.q, d)
			s.SelectRows = true
			run(s)
			s.Prepared = true
			s.ErrorStage = "prepare"
			run(s)
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT 'bad'::text::tid", tidDiagnostic("bad"))
			s.Prepared = prepared
			s.SelectRows = true
			s.ErrorStage = "execute"
			run(s)
		}
		run(fixture.Exec("CREATE TABLE tid_empty(v tid)", 0))
		d := tidDiagnostic("bad")
		d.Position = "8"
		eager := fixture.ErrorDiagnostic("SELECT 'bad'::tid FROM tid_empty", d)
		eager.SelectRows = true
		run(eager)
		eager.Prepared = true
		eager.ErrorStage = "prepare"
		run(eager)
		q := "INSERT INTO tid_errors VALUES(2,'(3,1)'),(3,'bad')"
		d = tidDiagnostic("bad")
		d.Position = fixture.Position(strings.Index(q, "'bad'") + 1)
		run(fixture.ErrorDiagnostic(q, d))
		d = tidDiagnostic("bad")
		d.Context = "unnamed portal parameter $2 = '...'"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("INSERT INTO tid_errors VALUES(2,$1),(3,$2)", d, "(3,1)", "bad")
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
			run(fixture.Select("SELECT id,v FROM tid_errors ORDER BY id", []string{"id", "v"}, []string{"INT4", "TID"}, [][]any{{int64(1), "(1,2)"}}))
		}
		for _, tc := range []struct{ q, code, message, hint, pos string }{
			{"SELECT 1::tid", "42846", "cannot cast type integer to tid", "", "9"},
			{"SELECT '(1,2)'::tid = 1", "42883", "operator does not exist: tid = integer", "No operator matches the given name and argument types. You might need to add explicit type casts.", "21"},
			{"SELECT '(1,2)'::tid = '(1,2)'::text", "42883", "operator does not exist: tid = text", "No operator matches the given name and argument types. You might need to add explicit type casts.", "21"},
			{"SELECT '(1,2)'::tid(2)", "42601", `type modifier is not allowed for type "tid"`, "", "17"},
			{"SELECT '(1,2)'::tid COLLATE \"C\"", "42804", "collations are not supported by type tid", "", "21"},
		} {
			d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			d.Hint = tc.hint
			d.Position = tc.pos
			s := fixture.ErrorDiagnostic(tc.q, d)
			s.SelectRows = true
			run(s)
			s.Prepared = true
			s.ErrorStage = "prepare"
			run(s)
		}
	})
}

func TestReferenceTIDAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"tid_reader"})
	tidScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		owner, reader := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, owner, b.Recorder, s) }
		run(fixture.Exec("CREATE ROLE tid_reader", 0))
		run(fixture.Exec("CREATE TABLE tid_denied(id integer,v tid)", 0))
		run(fixture.Exec("INSERT INTO tid_denied VALUES(1,'(1,2)')", 1))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO tid_reader", 0))
		}
		fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Exec("SET ROLE tid_reader", 0))
		defer reader.ExecContext(p.Ctx, "RESET ROLE")
		q := "SELECT id,v FROM tid_denied ORDER BY id"
		d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table tid_denied")
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(q, d)
			s.SelectRows = true
			s.Prepared = prepared
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, reader, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.ErrorDiagnostic("INSERT INTO tid_denied VALUES(2,'(2,3)')", d))
		run(fixture.Exec("GRANT SELECT ON tid_denied TO tid_reader", 0))
		stmt, err := reader.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: reader, Statement: stmt, SQL: q}
		want := fixture.Select(q, []string{"id", "v"}, []string{"INT4", "TID"}, [][]any{{int64(1), "(1,2)"}})
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		run(fixture.Exec("REVOKE SELECT ON tid_denied FROM tid_reader", 0))
		s := fixture.ErrorDiagnostic(q, d)
		s.SelectRows = true
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		run(fixture.Exec("GRANT SELECT ON tid_denied TO tid_reader", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
	})
}
