package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// Expectations were established by the pinned PostgreSQL 18.0 oracle.
// Backend selection changes availability, not the shared SQL scenario.
func aggregateModifiersScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn, func(referenceStep))) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s referenceStep) {
				if s.nonContext {
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				} else {
					referenceRunStep(t, p.ctx, conn, backend.recorder, s)
				}
			}
			body(t, p, backend, conn, run)
		})
	}
}

func aggregateModifiersBoth(run func(referenceStep), s referenceStep) {
	for _, plain := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			step := s
			step.nonContext, step.prepared = plain, prepared
			run(step)
		}
	}
}

func aggregateModifiersError(q, code, message, token string) referenceStep {
	position := ""
	if token != "" {
		i := strings.Index(q, token)
		if i < 0 {
			panic("diagnostic token not present: " + token)
		}
		position = strconv.Itoa(utf8.RuneCountInString(q[:i]) + 1)
	}
	s := referenceImplicitError(q, referenceDiagnostic{Code: code, Message: message, Position: position})
	s.selectRows = true
	return s
}
