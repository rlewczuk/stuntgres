package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceArithmeticWidthAssignments(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT,i INT,b BIGINT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,2147483647,9223372036854775807),(2,-2147483648,-9223372036854775808)", 2))
	p.Step(t, fixture.Exec("CREATE TABLE dest(i INT,b BIGINT)", 0))
	const query = "INSERT INTO dest VALUES($1+7,$2+2147483648)"
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			stmt, err := backend.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := stmt.Close(); err != nil {
					t.Error(err)
				}
			}()
			held := fixture.HeldStatement{backend.DB, stmt, query}
			fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(query, 1, 1, 1))
			remaining := fixture.Select("SELECT i,b FROM dest", []string{"i", "b"}, []string{"INT4", "INT8"}, [][]any{{int64(8), int64(2147483649)}})
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, remaining)
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(remaining))
			for _, q := range []string{"INSERT INTO dest VALUES(1,2147483647+1)", "INSERT INTO dest VALUES(2147483647+2147483648,1)"} {
				step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22003", Message: "integer out of range"})
				step.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, referenceArithmeticExecutor{backend.DB, t}, backend.Recorder, step)
			}
			bad := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Context: "unnamed portal parameter $1 = '...'"}, "oops", 1)
			bad.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, referenceArithmeticExecutor{held, t}, backend.Recorder, bad)
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, remaining)
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(remaining))
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := backend.DB.QueryContext(ctx, "SELECT i+1 FROM t")
			if rows != nil {
				rows.Close()
				t.Fatal("canceled query exposed cursor")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled query: %v", err)
			}
		})
	}
}
