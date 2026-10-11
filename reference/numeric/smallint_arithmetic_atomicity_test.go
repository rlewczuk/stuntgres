package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSmallintArithmeticAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE dest(s SMALLINT)", 0))
			run(fixture.Exec("INSERT INTO dest VALUES(99)", 1))
			unchanged := fixture.Select("SELECT s FROM dest", []string{"s"}, []string{"INT2"}, [][]any{{int64(99)}})
			run(fixture.Exec("CREATE TABLE src(id INT,s SMALLINT,h SMALLINT)", 0))
			run(fixture.Exec("INSERT INTO src VALUES(1,7,2),(2,32767,1)", 2))
			for _, tc := range []struct{ q, code, message string }{
				{"INSERT INTO dest VALUES(1),(32767::smallint+1::smallint)", "22003", "smallint out of range"},
				{"INSERT INTO dest VALUES(1),(1::smallint/0::smallint)", "22012", "division by zero"},
				{"INSERT INTO dest SELECT s+h FROM src ORDER BY id", "22003", "smallint out of range"},
			} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
					s.ErrorStage = "execute"
					s.Prepared = prepared
					run(s)
					run(unchanged)
					tx, err := backend.DB.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					fixture.RunStep(t, p.Ctx, tx, backend.Recorder, s)
					fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.ImplicitError("SELECT s FROM dest", fixture.TransactionAborted()))
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					run(unchanged)
				}
			}
			run(fixture.Exec("CREATE TABLE wide(b BIGINT)", 0))
			s := fixture.ImplicitError("INSERT INTO wide VALUES(32767::smallint+1::smallint)", fixture.Diagnostic{Code: "22003", Message: "smallint out of range"})
			s.ErrorStage = "execute"
			run(s)
			run(fixture.Prepared(s))
			run(fixture.Select("SELECT b FROM wide", []string{"b"}, []string{"INT8"}, nil))
			s = fixture.ImplicitError("SELECT s+h FROM src ORDER BY id", fixture.Diagnostic{Code: "22003", Message: "smallint out of range"})
			s.SelectRows = true
			s.ErrorStage = "execute"
			run(s)
			run(fixture.Prepared(s))
			run(fixture.Select("SELECT s+h FROM src WHERE id=1", []string{"?column?"}, []string{"INT2"}, [][]any{{int64(9)}}))
			run(fixture.Exec("UPDATE src SET h=0 WHERE id=2", 1))
			for _, q := range []string{"SELECT s/h FROM src ORDER BY id", "SELECT s%h FROM src ORDER BY id", "INSERT INTO dest SELECT s/h FROM src ORDER BY id"} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
				s.SelectRows = q[:6] == "SELECT"
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Prepared(s))
				run(unchanged)
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			if r, err := backend.DB.ExecContext(ctx, "INSERT INTO dest VALUES(1)"); r != nil || !errors.Is(err, context.Canceled) {
				t.Fatal(r, err)
			}
			if rows, err := backend.DB.QueryContext(ctx, "SELECT s+s FROM dest"); rows != nil || !errors.Is(err, context.Canceled) {
				t.Fatal(rows, err)
			}
			stmt, err := backend.DB.PrepareContext(p.Ctx, "INSERT INTO dest VALUES($1::smallint+1::smallint)")
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
