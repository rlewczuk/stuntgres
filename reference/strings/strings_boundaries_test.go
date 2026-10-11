package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringLiteralDollarParametersAndBoundaries(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE dollar_params(id INT,label TEXT)", 0))
			for i, prepared := range []bool{false, true} {
				s := fixture.Exec(`INSERT INTO dollar_params VALUES ($1,$tag$$2;$tag$)`, 1, 7+i)
				s.Prepared = prepared
				run(s)
			}
			s := fixture.Select("SELECT * FROM dollar_params ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "$2;"}, {int64(8), "$2;"}})
			run(s)
			rows, err := backend.DB.QueryContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(err)
			}
			fixture.LexicalRows(t, backend.PG, rows, s, nil)
			q := `SELECT id FROM dollar_params WHERE label=$$ $1 $$ ORDER BY id`
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Select(q, []string{"id"}, []string{"INT4"}, nil))
			rows, err = stmt.QueryContext(p.Ctx, 1)
			if rows != nil {
				rows.Close()
				t.Fatal("extra argument exposed rows")
			}
			var state interface{ SQLState() string }
			if err == nil || errors.As(err, &state) {
				t.Fatalf("extra argument must be ordinary: %v", err)
			}
			stmt.Close()
			run(fixture.Exec("CREATE TABLE separated(x$tag$x$tag$ TEXT)", 0))
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec(`INSERT INTO separated(x$tag$x$tag$) VALUES ($$v$$)`, 1)
				s.Prepared = prepared
				run(s)
			}
			s = fixture.Select("SELECT x$tag$x$tag$ FROM separated ORDER BY x$tag$x$tag$", []string{"x$tag$x$tag$"}, []string{"TEXT"}, [][]any{{"v"}, {"v"}})
			run(s)
			rows, err = backend.DB.QueryContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(err)
			}
			fixture.LexicalRows(t, backend.PG, rows, s, nil)
			run(fixture.Exec("CREATE TABLE t(label TEXT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES ('seed')", 1))
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			_, err = backend.DB.ExecContext(canceled, `INSERT INTO t VALUES ($$canceled$$)`)
			fixture.LexicalOrdinary(t, err, context.Canceled)
			stmt, err = backend.DB.PrepareContext(p.Ctx, `INSERT INTO t VALUES ($$canceled$$)`)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			_, err = stmt.ExecContext(canceled)
			fixture.LexicalOrdinary(t, err, context.Canceled)
			run(fixture.Select("SELECT * FROM t", []string{"label"}, []string{"TEXT"}, [][]any{{"seed"}}))
		})
	}
}

func TestReferenceStringLiteralDollarDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) {
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			}
			run(fixture.Exec("CREATE TABLE t(label TEXT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES ('seed')", 1))
			for _, tc := range []struct{ q, code, message, position, postgresMessage string }{
				{"INSERT INTO t VALUES$$v$$", "42601", `syntax error at or near "VALUES$$v$$"`, "15", ""},
				{"INSERT INTO t VALUES ($0)", "42P02", "there is no parameter $0", "23", ""},
				{"INSERT INTO t VALUES ($000)", "42P02", "there is no parameter $0", "23", ""},
				{"INSERT INTO t VALUES ($1junk)", "42601", "invalid parameter", "23", `trailing junk after parameter at or near "$1junk"`},
				{"INSERT INTO t VALUES ($9tag$)", "42601", "invalid parameter", "23", `trailing junk after parameter at or near "$9tag$"`},
				{"INSERT INTO t VALUES ($2147483648)", "42601", "parameter number too large", "23", `parameter number too large at or near "$2147483648"`},
				{"INSERT INTO t VALUES ('valid'),($Tag$a$tag$)", "42601", `unterminated dollar-quoted string at or near "$Tag$a$tag$)"`, "33", ""},
				{"CREATE TABLE fresh(x TEXT); INSERT INTO fresh VALUES ($$a)", "42601", `unterminated dollar-quoted string at or near "$$a)"`, "55", ""},
			} {
				// These three PostgreSQL messages are independently observed
				// full records in tests-1.md. Keep native diagnostics unchanged;
				// message differences are not parity-normalized.
				message := tc.message
				if backend.PG && tc.postgresMessage != "" {
					message = tc.postgresMessage
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: message, Position: tc.position})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
					run(fixture.Select("SELECT * FROM t", []string{"label"}, []string{"TEXT"}, [][]any{{"seed"}}))
				}
			}
			for _, name := range []string{"fresh", "phantom"} {
				s := fixture.Error("SELECT * FROM "+name, "42P01")
				s.SelectRows = true
				run(s)
			}
		})
	}
}
