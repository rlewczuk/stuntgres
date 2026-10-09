package reference_test

import (
	"context"
	"errors"
	"testing"
)

func TestReferenceAnyValueCallers(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(k TEXT,v INT)", 0))
			run(referenceExec("INSERT INTO t VALUES('a',1),('b',0)", 2))
			run(referenceExec("CREATE TABLE out(v INT)", 0))
			for _, prepared := range []bool{false, true} {
				for _, q := range []string{"SELECT any_value($1::integer/v) FROM t", "SELECT k,any_value($1::integer/v) FROM t GROUP BY k", "INSERT INTO out SELECT any_value($1::integer/v) FROM t", "INSERT INTO out SELECT any_value($1::integer/v) FROM t GROUP BY k"} {
					s := referenceImplicitError(q, referenceDiagnostic{Code: "22012", Message: "division by zero"}, 10)
					s.prepared, s.selectRows, s.errorStage = prepared, q[:6] == "SELECT", "execute"
					run(s)
				}
			}
			q := "INSERT INTO out VALUES(99); INSERT INTO out SELECT any_value(10/v) FROM t"
			batch := referenceImplicitError(q, referenceDiagnostic{Code: "22012", Message: "division by zero"})
			if !backend.pg {
				batch = referenceError(q, "0A000")
			}
			run(batch)
			run(referenceSelect("SELECT v FROM out", []string{"v"}, []string{"INT4"}, nil))
			for _, prepared := range []bool{false, true} {
				for _, q := range []string{"SELECT any_value(v) FROM t", "INSERT INTO out SELECT any_value(v) FROM t"} {
					var executor referenceExecutor = backend.db
					if prepared {
						stmt, err := backend.db.PrepareContext(p.ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						executor = referenceHeldStatement{backend.db, stmt, q}
					}
					ctx, cancel := context.WithCancel(p.ctx)
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
			run(referenceSelect("SELECT v FROM out", []string{"v"}, []string{"INT4"}, nil))
		})
	}
}
