package reference_test

import (
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAnyValueBindBoundaries(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE t(v INT)", 0))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO t VALUES(1)", 1))
			const q = "SELECT any_value($1::integer) FROM t"
			for _, prepared := range []bool{false, true} {
				if backend.PG {
					// pq serializes this Go slice as an array. Its integer
					// diagnostic is not the native driver's Go-type boundary.
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "{9}"`, Context: "unnamed portal parameter $1 = '...'"}, []int{9})
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					continue
				}
				var executor fixture.Executor = backend.DB
				if prepared {
					stmt, err := backend.DB.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					executor = fixture.HeldStatement{backend.DB, stmt, q}
				}
				rows, err := executor.QueryContext(p.Ctx, q, []int{9})
				if rows != nil {
					rows.Close()
					t.Fatal("invalid Go argument exposed rows")
				}
				var state interface{ SQLState() string }
				if err == nil || errors.As(err, &state) {
					t.Fatalf("adapter error must be ordinary, got %T: %v", err, err)
				}
			}
			fixture.AnyValueRoutes(t, p, backend, fixture.Select("SELECT any_value($1::date)::text AS v FROM t", []string{"v"}, []string{"TEXT"}, [][]any{{"2000-02-29"}}, "2000-02-29"), fixture.Select("SELECT any_value($1::time)::text AS v FROM t", []string{"v"}, []string{"TEXT"}, [][]any{{"12:34:56.123"}}, "12:34:56.123"), fixture.Select("SELECT any_value($1::timestamp)::text AS v FROM t", []string{"v"}, []string{"TEXT"}, [][]any{{"2000-02-29 12:34:56.123"}}, "2000-02-29 12:34:56.123"))
		})
	}
}
