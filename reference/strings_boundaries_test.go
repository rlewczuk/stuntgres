package reference_test

import (
	"context"
	"errors"
	"testing"
)

func TestReferenceStringLiteralDollarParametersAndBoundaries(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE dollar_params(id INT,label TEXT)", 0))
			for i, prepared := range []bool{false, true} {
				s := referenceExec(`INSERT INTO dollar_params VALUES ($1,$tag$$2;$tag$)`, 1, 7+i)
				s.prepared = prepared
				run(s)
			}
			s := referenceSelect("SELECT * FROM dollar_params ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "$2;"}, {int64(8), "$2;"}})
			run(s)
			rows, err := backend.db.QueryContext(p.ctx, s.query)
			if err != nil {
				t.Fatal(err)
			}
			lexicalRows(t, backend.pg, rows, s, nil)
			q := `SELECT id FROM dollar_params WHERE label=$$ $1 $$ ORDER BY id`
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
			referenceRunStep(t, p.ctx, held, backend.recorder, referenceSelect(q, []string{"id"}, []string{"INT4"}, nil))
			rows, err = stmt.QueryContext(p.ctx, 1)
			if rows != nil {
				rows.Close()
				t.Fatal("extra argument exposed rows")
			}
			var state interface{ SQLState() string }
			if err == nil || errors.As(err, &state) {
				t.Fatalf("extra argument must be ordinary: %v", err)
			}
			stmt.Close()
			run(referenceExec("CREATE TABLE separated(x$tag$x$tag$ TEXT)", 0))
			for _, prepared := range []bool{false, true} {
				s := referenceExec(`INSERT INTO separated(x$tag$x$tag$) VALUES ($$v$$)`, 1)
				s.prepared = prepared
				run(s)
			}
			s = referenceSelect("SELECT x$tag$x$tag$ FROM separated ORDER BY x$tag$x$tag$", []string{"x$tag$x$tag$"}, []string{"TEXT"}, [][]any{{"v"}, {"v"}})
			run(s)
			rows, err = backend.db.QueryContext(p.ctx, s.query)
			if err != nil {
				t.Fatal(err)
			}
			lexicalRows(t, backend.pg, rows, s, nil)
			run(referenceExec("CREATE TABLE t(label TEXT)", 0))
			run(referenceExec("INSERT INTO t VALUES ('seed')", 1))
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			_, err = backend.db.ExecContext(canceled, `INSERT INTO t VALUES ($$canceled$$)`)
			lexicalOrdinary(t, err, context.Canceled)
			stmt, err = backend.db.PrepareContext(p.ctx, `INSERT INTO t VALUES ($$canceled$$)`)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			_, err = stmt.ExecContext(canceled)
			lexicalOrdinary(t, err, context.Canceled)
			run(referenceSelect("SELECT * FROM t", []string{"label"}, []string{"TEXT"}, [][]any{{"seed"}}))
		})
	}
}

func TestReferenceStringLiteralDollarDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) {
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			}
			run(referenceExec("CREATE TABLE t(label TEXT)", 0))
			run(referenceExec("INSERT INTO t VALUES ('seed')", 1))
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
				if backend.pg && tc.postgresMessage != "" {
					message = tc.postgresMessage
				}
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: message, Position: tc.position})
					s.prepared = prepared
					s.errorStage = "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					run(s)
					run(referenceSelect("SELECT * FROM t", []string{"label"}, []string{"TEXT"}, [][]any{{"seed"}}))
				}
			}
			for _, name := range []string{"fresh", "phantom"} {
				s := referenceError("SELECT * FROM "+name, "42P01")
				s.selectRows = true
				run(s)
			}
		})
	}
}
