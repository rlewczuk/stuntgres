package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

// Expectations were established by the pinned PostgreSQL 18.0 oracle.
// Backend selection changes availability, not the shared SQL scenario.
func aggregateModifiersScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, func(fixture.Step))) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) {
				if s.NonContext {
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				} else {
					fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
				}
			}
			body(t, p, backend, conn, run)
		})
	}
}

func aggregateModifiersBoth(run func(fixture.Step), s fixture.Step) {
	for _, plain := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			step := s
			step.NonContext, step.Prepared = plain, prepared
			run(step)
		}
	}
}

func aggregateModifiersError(q, code, message, token string) fixture.Step {
	position := ""
	if token != "" {
		i := strings.Index(q, token)
		if i < 0 {
			panic("diagnostic token not present: " + token)
		}
		position = strconv.Itoa(utf8.RuneCountInString(q[:i]) + 1)
	}
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message, Position: position})
	s.SelectRows = true
	return s
}
