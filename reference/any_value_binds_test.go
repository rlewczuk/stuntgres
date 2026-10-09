package reference_test

import (
	"errors"
	"testing"
)

func TestReferenceAnyValueBindBoundaries(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("CREATE TABLE t(v INT)", 0))
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("INSERT INTO t VALUES(1)", 1))
			const q = "SELECT any_value($1::integer) FROM t"
			for _, prepared := range []bool{false, true} {
				if backend.pg {
					// pq serializes this Go slice as an array. Its integer
					// diagnostic is not the native driver's Go-type boundary.
					s := referenceImplicitError(q, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "{9}"`, Context: "unnamed portal parameter $1 = '...'"}, []int{9})
					s.selectRows, s.prepared, s.errorStage = true, prepared, "execute"
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
					continue
				}
				var executor referenceExecutor = backend.db
				if prepared {
					stmt, err := backend.db.PrepareContext(p.ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					executor = referenceHeldStatement{backend.db, stmt, q}
				}
				rows, err := executor.QueryContext(p.ctx, q, []int{9})
				if rows != nil {
					rows.Close()
					t.Fatal("invalid Go argument exposed rows")
				}
				var state interface{ SQLState() string }
				if err == nil || errors.As(err, &state) {
					t.Fatalf("adapter error must be ordinary, got %T: %v", err, err)
				}
			}
			anyValueRoutes(t, p, backend, referenceSelect("SELECT any_value($1::date)::text AS v FROM t", []string{"v"}, []string{"TEXT"}, [][]any{{"2000-02-29"}}, "2000-02-29"), referenceSelect("SELECT any_value($1::time)::text AS v FROM t", []string{"v"}, []string{"TEXT"}, [][]any{{"12:34:56.123"}}, "12:34:56.123"), referenceSelect("SELECT any_value($1::timestamp)::text AS v FROM t", []string{"v"}, []string{"TEXT"}, [][]any{{"2000-02-29 12:34:56.123"}}, "2000-02-29 12:34:56.123"))
		})
	}
}
