package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func formatReferenceScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			body(t, p, backend, conn)
		})
	}
}

func formatReferenceBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			step := s
			if prepared {
				step = fixture.Prepared(step)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		})
	}
}

func formatReferenceText(call string, want any) fixture.Step {
	return fixture.Select("SELECT "+call, []string{"format"}, []string{"TEXT"}, [][]any{{want}})
}

func formatReferenceFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, d fixture.Diagnostic, analysis bool) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			s := fixture.ErrorDiagnostic(q, d)
			s.SelectRows, s.ErrorStage = true, "execute"
			if prepared {
				s = fixture.Prepared(s)
				if analysis {
					s.ErrorStage = "prepare"
				}
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		})
	}
}
