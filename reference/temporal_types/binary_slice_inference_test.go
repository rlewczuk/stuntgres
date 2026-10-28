package reference_test

import (
	"context"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBinarySliceInference(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := fixture.NewScenario(t)
			apply := func(t *testing.T, s fixture.Step) { s.NonContext = nonContext; p.Step(t, s) }
			run := func(t *testing.T, ctx context.Context, x fixture.Executor, r *fixture.Recorder, s fixture.Step) {
				s.NonContext = nonContext
				fixture.RunStep(t, ctx, x, r, s)
			}
			apply(t, fixture.Exec("CREATE TABLE t(v BYTEA,s BYTEA)", 0))
			hint := "No function matches the given name and argument types. You might need to add explicit type casts."
			for _, tc := range []struct {
				q    string
				want fixture.Diagnostic
			}{
				{"SELECT overlay(v,$1,$1) FROM t", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "bytea versus integer", Position: "21"}},
				{"SELECT substr(v) FROM t", fixture.Diagnostic{Code: "42883", Message: "function substr(bytea) does not exist", Hint: hint, Position: "8"}},
				{"SELECT overlay(v,s,1,2,3) FROM t", fixture.Diagnostic{Code: "42883", Message: "function overlay(bytea, bytea, integer, integer, integer) does not exist", Hint: hint, Position: "8"}},
				{"SELECT missing.substr(v,1) FROM t", fixture.Diagnostic{Code: "3F000", Message: `schema "missing" does not exist`, Position: "8"}},
				{"SELECT substr(v," + strings.Repeat("1,", 99) + "1) FROM t", fixture.Diagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}},
				{`SELECT substring(NULL::bytea FROM 'bad') FROM t`, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "35"}},
			} {
				step := fixture.ImplicitError(tc.q, tc.want)
				step.SelectRows = true
				step.ErrorStage = "prepare"
				apply(t, fixture.Prepared(step))
			}
			for _, backend := range p.Backends() {
				for _, tc := range []struct{ name, q, want string }{{"sub", "CREATE TABLE d_sub(v BYTEA DEFAULT substr('abc'::bytea,1))", "abc"}, {"overlay", "CREATE TABLE d_overlay(v BYTEA DEFAULT overlay('abc'::bytea,'x'::bytea,1))", "xbc"}} {
					if backend.PG {
						run(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec(tc.q, 0))
						run(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO d_"+tc.name+" DEFAULT VALUES", 1))
						run(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT v FROM d_"+tc.name, []string{"v"}, []string{"BYTEA"}, [][]any{{tc.want}}))
					} else {
						q := strings.Replace(tc.q, "d_"+tc.name, "d", 1)
						run(t, p.Ctx, backend.DB, backend.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "functions are unsupported", Position: "32"}))
					}
				}
			}
			apply(t, fixture.Exec("INSERT INTO t VALUES('abcdef','XY')", 1))
			success := fixture.Select("SELECT substr(v,VARIADIC 1) AS v FROM t", []string{"v"}, []string{"BYTEA"}, [][]any{{"abcdef"}})
			apply(t, success)
			apply(t, fixture.Prepared(success))
			if nonContext {
				for _, backend := range p.Backends() {
					stmt, err := backend.DB.Prepare("SELECT substr(v,VARIADIC 1) FROM t")
					if err != nil {
						t.Fatal(err)
					}
					var raw []byte
					err = stmt.QueryRow().Scan(&raw)
					if closeErr := stmt.Close(); closeErr != nil {
						t.Fatal(closeErr)
					}
					if err != nil || raw == nil || string(raw) != "abcdef" {
						t.Fatal(raw, err)
					}
				}
			}
		})
	}
}
