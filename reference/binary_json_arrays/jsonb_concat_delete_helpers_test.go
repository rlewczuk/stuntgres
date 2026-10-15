package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonbConcatDeleteScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := c.Close(); err != nil {
					t.Error(err)
				}
			})
			body(t, p, backend, c)
		})
	}
}

func jsonbConcatDeleteSelect(q string, want any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"v"}, []string{"JSONB"}, [][]any{{want}}, args...)
}

func jsonbConcatDeleteError(q string, d fixture.Diagnostic, args ...any) fixture.Step {
	s := fixture.ErrorDiagnostic(q, d, args...)
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}
