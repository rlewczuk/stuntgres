package reference_test

import (
	"context"
	"errors"
	"testing"
)

func TestReferenceSmallintArithmeticAtomicity(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE dest(s SMALLINT)", 0))
			run(referenceExec("INSERT INTO dest VALUES(99)", 1))
			unchanged := referenceSelect("SELECT s FROM dest", []string{"s"}, []string{"INT2"}, [][]any{{int64(99)}})
			run(referenceExec("CREATE TABLE src(id INT,s SMALLINT,h SMALLINT)", 0))
			run(referenceExec("INSERT INTO src VALUES(1,7,2),(2,32767,1)", 2))
			for _, tc := range []struct{ q, code, message string }{
				{"INSERT INTO dest VALUES(1),(32767::smallint+1::smallint)", "22003", "smallint out of range"},
				{"INSERT INTO dest VALUES(1),(1::smallint/0::smallint)", "22012", "division by zero"},
				{"INSERT INTO dest SELECT s+h FROM src ORDER BY id", "22003", "smallint out of range"},
			} {
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message})
					s.errorStage = "execute"
					s.prepared = prepared
					run(s)
					run(unchanged)
					tx, err := backend.db.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					referenceRunStep(t, p.ctx, tx, backend.recorder, s)
					referenceRunStep(t, p.ctx, tx, backend.recorder, referenceImplicitError("SELECT s FROM dest", referenceTransactionAborted()))
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					run(unchanged)
				}
			}
			run(referenceExec("CREATE TABLE wide(b BIGINT)", 0))
			s := referenceImplicitError("INSERT INTO wide VALUES(32767::smallint+1::smallint)", referenceDiagnostic{Code: "22003", Message: "smallint out of range"})
			s.errorStage = "execute"
			run(s)
			run(referencePrepared(s))
			run(referenceSelect("SELECT b FROM wide", []string{"b"}, []string{"INT8"}, nil))
			s = referenceImplicitError("SELECT s+h FROM src ORDER BY id", referenceDiagnostic{Code: "22003", Message: "smallint out of range"})
			s.selectRows = true
			s.errorStage = "execute"
			run(s)
			run(referencePrepared(s))
			run(referenceSelect("SELECT s+h FROM src WHERE id=1", []string{"?column?"}, []string{"INT2"}, [][]any{{int64(9)}}))
			run(referenceExec("UPDATE src SET h=0 WHERE id=2", 1))
			for _, q := range []string{"SELECT s/h FROM src ORDER BY id", "SELECT s%h FROM src ORDER BY id", "INSERT INTO dest SELECT s/h FROM src ORDER BY id"} {
				s := referenceImplicitError(q, referenceDiagnostic{Code: "22012", Message: "division by zero"})
				s.selectRows = q[:6] == "SELECT"
				s.errorStage = "execute"
				run(s)
				run(referencePrepared(s))
				run(unchanged)
			}
			ctx, cancel := context.WithCancel(p.ctx)
			cancel()
			if r, err := backend.db.ExecContext(ctx, "INSERT INTO dest VALUES(1)"); r != nil || !errors.Is(err, context.Canceled) {
				t.Fatal(r, err)
			}
			if rows, err := backend.db.QueryContext(ctx, "SELECT s+s FROM dest"); rows != nil || !errors.Is(err, context.Canceled) {
				t.Fatal(rows, err)
			}
			stmt, err := backend.db.PrepareContext(p.ctx, "INSERT INTO dest VALUES($1::smallint+1::smallint)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			if r, err := stmt.ExecContext(ctx, 1); r != nil || !errors.Is(err, context.Canceled) {
				t.Fatal(r, err)
			}
			run(unchanged)
		})
	}
}
