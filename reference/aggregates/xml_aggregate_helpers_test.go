package reference_test

import (
	"database/sql"
	"testing"
	"stuntgres/reference/fixture"
)

func xmlAggScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer c.Close()
			body(t, p, b, c)
		})
	}
}
func xmlAggBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	s.Prepared = true
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}
func xmlAggSelect(q, typ string, values ...any) fixture.Step {
	rows := make([][]any, len(values))
	for i, v := range values { rows[i] = []any{v} }
	return fixture.Select(q, []string{"v"}, []string{typ}, rows)
}
func xmlAggDiagnostic(code, message, hint string, pos int) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic(code, message)
	d.Hint, d.Position = hint, fixture.Position(pos)
	return d
}
func xmlAggFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, d fixture.Diagnostic, analysis bool, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, d, args...)
		s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
		if prepared && analysis { s.ErrorStage = "prepare" }
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}
