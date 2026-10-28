package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

const arraySearchInput = "[-2:2]={7,NULL,7,8,7}"

func arraySearchScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, backend, c)
		})
	}
}

func arraySearchBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func arraySearchSelect(b fixture.Backend, q string, names, types []string, rows [][]any, args ...any) fixture.Step {
	return fixture.ArrayReferenceSelect(q, names, types, types, rows, b.PG, args...)
}

func arraySearchRuntimeError(q, code, message string, args ...any) fixture.Step {
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message}, args...)
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}

// Analysis diagnostics surface on direct Query and on actual Prepare, whereas
// demanded provider/rank/start diagnostics leave Prepare successful.
func arraySearchDiagnosticBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, d fixture.Diagnostic, analysis bool, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError(q, d, args...)
		s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
		if prepared && analysis {
			s.ErrorStage = "prepare"
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}
