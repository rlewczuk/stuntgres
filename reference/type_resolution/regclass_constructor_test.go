package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRegclassConstructor(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE rr_constructor_target(v INT)", 0))
			for _, tc := range []struct{ expression, want string }{{"ARRAY['rr_constructor_target'::regclass,NULL::regclass]", "{rr_constructor_target,NULL}"}, {"ARRAY[]::regclass[]", "{}"}, {"ARRAY[NULL]::regclass[]", "{NULL}"}} {
				s := fixture.Select("SELECT "+tc.expression+" AS a", []string{"a"}, []string{"_REGCLASS"}, [][]any{{tc.want}})
				run(s)
				run(fixture.Prepared(s))
			}
			q := "SELECT ARRAY[$1::regclass,$2::regclass] AS a"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, args := range [][]any{{"rr_constructor_target", nil}, {nil, "rr_constructor_target"}, {nil, nil}} {
				want := "{rr_constructor_target,NULL}"
				if args[0] == nil {
					want = "{NULL,rr_constructor_target}"
				}
				if args[0] == nil && args[1] == nil {
					want = "{NULL,NULL}"
				}
				s := fixture.Select(q, []string{"a"}, []string{"_REGCLASS"}, [][]any{{want}}, args...)
				run(s)
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
			}
			want := fixture.ControlsReferenceDiagnostic("42P18", "cannot determine type of empty array")
			want.Position = "8"
			want.Hint = "Explicitly cast to the desired type, for example ARRAY[]::integer[]."
			s := fixture.ErrorDiagnostic("SELECT ARRAY[]", want)
			s.SelectRows = true
			run(s)
			s = fixture.Prepared(s)
			s.ErrorStage = "prepare"
			run(s)
		})
	}
}
