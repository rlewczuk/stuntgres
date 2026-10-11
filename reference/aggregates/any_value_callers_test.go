package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAnyValueCallers(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(k TEXT,v INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES('a',1),('b',0)", 2))
			run(fixture.Exec("CREATE TABLE out(v INT)", 0))
			for _, prepared := range []bool{false, true} {
				for _, q := range []string{"SELECT any_value($1::integer/v) FROM t", "SELECT k,any_value($1::integer/v) FROM t GROUP BY k", "INSERT INTO out SELECT any_value($1::integer/v) FROM t", "INSERT INTO out SELECT any_value($1::integer/v) FROM t GROUP BY k"} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"}, 10)
					s.Prepared, s.SelectRows, s.ErrorStage = prepared, q[:6] == "SELECT", "execute"
					run(s)
				}
			}
			q := "INSERT INTO out VALUES(99); INSERT INTO out SELECT any_value(10/v) FROM t"
			run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
			run(fixture.Select("SELECT v FROM out", []string{"v"}, []string{"INT4"}, nil))
			batch := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: "cannot insert multiple commands into a prepared statement"})
			batch.ErrorStage = "prepare"
			run(fixture.Prepared(batch))
			run(fixture.Select("SELECT v FROM out", []string{"v"}, []string{"INT4"}, nil))
			batch.Args = []any{99}
			batch.ErrorStage = "execute"
			run(batch)
			run(fixture.Select("SELECT v FROM out", []string{"v"}, []string{"INT4"}, nil))
			for _, prepared := range []bool{false, true} {
				for _, q := range []string{"SELECT any_value(v) FROM t", "INSERT INTO out SELECT any_value(v) FROM t"} {
					var executor fixture.Executor = backend.DB
					if prepared {
						stmt, err := backend.DB.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						executor = fixture.HeldStatement{backend.DB, stmt, q}
					}
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					if q[:6] == "SELECT" {
						rows, err := executor.QueryContext(ctx, q)
						if rows != nil {
							rows.Close()
							t.Fatal("canceled query exposed rows")
						}
						if !errors.Is(err, context.Canceled) {
							t.Fatalf("Query canceled: %v", err)
						}
					} else {
						result, err := executor.ExecContext(ctx, q)
						if result != nil || !errors.Is(err, context.Canceled) {
							t.Fatalf("Exec canceled: %v/%v", result, err)
						}
					}
				}
			}
			run(fixture.Select("SELECT v FROM out", []string{"v"}, []string{"INT4"}, nil))
		})
	}
}
