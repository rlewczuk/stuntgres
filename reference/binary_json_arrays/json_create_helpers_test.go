package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonCreateScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func jsonCreateSelect(q, typ string, want any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"v"}, []string{typ}, [][]any{{want}}, args...)
}

func jsonCreateError(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q string, d fixture.Diagnostic, analysis bool, args ...any) {
	t.Helper()
	s := fixture.ErrorDiagnostic(q, d, args...)
	s.SelectRows = true
	s.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	s.Prepared = true
	if analysis {
		s.ErrorStage = "prepare"
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func jsonCreateCases(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, cases []struct {
	expr, typ string
	want      any
}) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			jsonObjectBoth(t, p, b, c, jsonCreateSelect("SELECT "+tc.expr+" AS v", tc.typ, tc.want))
		})
	}
}
