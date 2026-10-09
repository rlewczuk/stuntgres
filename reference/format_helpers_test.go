package reference_test

import (
	"database/sql"
	"testing"
)

func formatReferenceScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
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

func formatReferenceBoth(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, s referenceStep) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			step := s
			if prepared {
				step = referencePrepared(step)
			}
			referenceRunStep(t, p.ctx, c, b.recorder, step)
		})
	}
}

func formatReferenceText(call string, want any) referenceStep {
	return referenceSelect("SELECT "+call, []string{"format"}, []string{"TEXT"}, [][]any{{want}})
}

func formatReferenceFailure(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, q string, d referenceDiagnostic, analysis bool) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			s := referenceErrorDiagnostic(q, d)
			s.selectRows, s.errorStage = true, "execute"
			if prepared {
				s = referencePrepared(s)
				if analysis {
					s.errorStage = "prepare"
				}
			}
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		})
	}
}
