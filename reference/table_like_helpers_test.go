package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func tableLikeScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn, bool, func(referenceStep))) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					c, err := b.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					run := func(s referenceStep) {
						s.prepared = prepared
						// Explicit generated/ALWAYS-identity writes are rejected during analysis,
						// as in the existing generated and identity diagnostic scenarios.
						if prepared && s.state == "428C9" {
							s.errorStage = "prepare"
						}
						referenceRunStep(t, p.ctx, c, b.recorder, s)
					}
					body(t, p, b, c, prepared, run)
				})
			}
		})
	}
}

func tableLikeError(q string, d referenceDiagnostic) referenceStep {
	s := referenceImplicitError(q, d)
	s.errorStage = "execute"
	return s
}

func tableLikeAbsent(run func(referenceStep), name string, prepared bool) {
	s := referenceImplicitError("SELECT * FROM "+name, referenceDiagnostic{Code: "42P01", Message: `relation "` + name + `" does not exist`, Position: "15"})
	s.selectRows = true
	s.errorStage = "execute"
	if prepared {
		s.errorStage = "prepare"
	}
	run(s)
}
