package reference_test

import "testing"

func TestReferenceRegclassConstructor(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, backend.recorder, s) }
			run(referenceExec("CREATE TABLE rr_constructor_target(v INT)", 0))
			for _, tc := range []struct{ expression, want string }{{"ARRAY['rr_constructor_target'::regclass,NULL::regclass]", "{rr_constructor_target,NULL}"}, {"ARRAY[]::regclass[]", "{}"}, {"ARRAY[NULL]::regclass[]", "{NULL}"}} {
				s := referenceSelect("SELECT "+tc.expression+" AS a", []string{"a"}, []string{"_REGCLASS"}, [][]any{{tc.want}})
				run(s)
				run(referencePrepared(s))
			}
			q := "SELECT ARRAY[$1::regclass,$2::regclass] AS a"
			stmt, err := c.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
			for _, args := range [][]any{{"rr_constructor_target", nil}, {nil, "rr_constructor_target"}, {nil, nil}} {
				want := "{rr_constructor_target,NULL}"
				if args[0] == nil {
					want = "{NULL,rr_constructor_target}"
				}
				if args[0] == nil && args[1] == nil {
					want = "{NULL,NULL}"
				}
				s := referenceSelect(q, []string{"a"}, []string{"_REGCLASS"}, [][]any{{want}}, args...)
				run(s)
				referenceRunStep(t, p.ctx, held, backend.recorder, s)
			}
			want := controlsReferenceDiagnostic("42P18", "cannot determine type of empty array")
			want.Position = "8"
			want.Hint = "Explicitly cast to the desired type, for example ARRAY[]::integer[]."
			s := referenceErrorDiagnostic("SELECT ARRAY[]", want)
			s.selectRows = true
			run(s)
			s = referencePrepared(s)
			s.errorStage = "prepare"
			run(s)
		})
	}
}
