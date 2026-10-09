package reference_test

import (
	"context"
	"errors"
	"testing"
)

func TestReferenceArithmeticWidthAssignments(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT,i INT,b BIGINT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,2147483647,9223372036854775807),(2,-2147483648,-9223372036854775808)", 2))
	p.step(t, referenceExec("CREATE TABLE dest(i INT,b BIGINT)", 0))
	const query = "INSERT INTO dest VALUES($1+7,$2+2147483648)"
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			stmt, err := backend.db.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := stmt.Close(); err != nil {
					t.Error(err)
				}
			}()
			held := referenceHeldStatement{backend.db, stmt, query}
			referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(query, 1, 1, 1))
			remaining := referenceSelect("SELECT i,b FROM dest", []string{"i", "b"}, []string{"INT4", "INT8"}, [][]any{{int64(8), int64(2147483649)}})
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, remaining)
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(remaining))
			for _, q := range []string{"INSERT INTO dest VALUES(1,2147483647+1)", "INSERT INTO dest VALUES(2147483647+2147483648,1)"} {
				step := referenceImplicitError(q, referenceDiagnostic{Code: "22003", Message: "integer out of range"})
				step.errorStage = "execute"
				referenceRunStep(t, p.ctx, referenceArithmeticExecutor{backend.db, t}, backend.recorder, step)
			}
			bad := referenceImplicitError(query, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Context: "unnamed portal parameter $1 = '...'"}, "oops", 1)
			bad.errorStage = "execute"
			referenceRunStep(t, p.ctx, referenceArithmeticExecutor{held, t}, backend.recorder, bad)
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, remaining)
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(remaining))
			ctx, cancel := context.WithCancel(p.ctx)
			cancel()
			rows, err := backend.db.QueryContext(ctx, "SELECT i+1 FROM t")
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
