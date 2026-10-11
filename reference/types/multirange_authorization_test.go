package reference_test

import (
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMultirangeAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"multirange_reader"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		d := p.NativeConnector.Driver().(*stuntgres.Driver)
		dsn := "reference/" + t.Name()
		for _, q := range []string{
			"CREATE USER multirange_reader",
			"CREATE TABLE r(a int4multirange,b int8multirange,c nummultirange)",
			"CREATE SCHEMA multirange_private",
			"CREATE TABLE multirange_private.secret(a nummultirange)",
		} {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec(q, 0))
		}
		if backend.PG {
			if _, err := backend.DB.ExecContext(p.Ctx, "ALTER ROLE multirange_reader PASSWORD 'role-core-fixture'"); err != nil {
				t.Fatal(err)
			}
			// The freshly recreated oracle schema has no PUBLIC USAGE.
			// Native public already grants it; only the setup differs.
			if _, err := backend.DB.ExecContext(p.Ctx, "GRANT USAGE ON SCHEMA public TO multirange_reader"); err != nil {
				t.Fatal(err)
			}
		}
		t.Cleanup(func() {
			if _, err := backend.DB.ExecContext(p.Ctx, "DROP SCHEMA multirange_private CASCADE"); err != nil {
				t.Error(err)
			}
		})
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO r VALUES ('{}','{(,)}','{(4,5)}')", 1))
		actor := fixture.RoleScenarioLoginPool(t, d, backend.PG, dsn, "multirange_reader")
		if err := actor.PingContext(p.Ctx); err != nil {
			t.Fatal(err)
		}
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				q, message, position, stage string
				query                       bool
			}{
				{"SELECT a,b,c FROM public.r", "permission denied for table r", "", "execute", true},
				{"INSERT INTO public.r VALUES ('{}','{}','{}')", "permission denied for table r", "", "execute", false},
				{"CREATE TABLE multirange_private.denied(a int4multirange)", "permission denied for schema multirange_private", "14", "execute", false},
				{"SELECT a FROM multirange_private.secret", "permission denied for schema multirange_private", "15", "execute", true},
			} {
				want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: tc.message, Position: tc.position}
				s := fixture.ErrorDiagnostic(tc.q, want)
				s.Prepared, s.SelectRows, s.ErrorStage = prepared, tc.query, tc.stage
				if prepared && strings.Contains(tc.q, "SELECT a FROM multirange_private") {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, actor, backend.Recorder, s)
			}
		}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, rangeSelect(backend, "SELECT a,b,c FROM r", []string{"a", "b", "c"}, []string{"INT4MULTIRANGE", "INT8MULTIRANGE", "NUMMULTIRANGE"}, [][]any{{"{}", "{(,)}", "{(4,5)}"}}, []bool{true, true, true}))
	}
}
