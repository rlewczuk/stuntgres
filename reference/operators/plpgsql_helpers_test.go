package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func plpgsqlScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func plpgsqlValue(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q, typ string, value any) {
	t.Helper()
	s := fixture.Select(q, []string{"v"}, []string{typ}, [][]any{{value}})
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

// Full diagnostics are recorded from pinned18.6 tests-1; every optional field,
// including absence, remains part of the independent contract.
func plpgsqlError(t *testing.T, p *fixture.Pair, c *sql.Conn, q string, query bool, want fixture.Diagnostic) {
	t.Helper()
	stage := "execute"
	fixture.EnumReferenceErrorChecked(t, p, c, q, query, stage, func(t *testing.T, got fixture.Diagnostic) {
		if got != want {
			t.Errorf("PL diagnostic query=%q got=%#v want=%#v", q, got, want)
		}
	})
}

func plpgsqlRuntimeDiagnostic(code, message, function, action string) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic(code, message)
	d.Context = "PL/pgSQL function " + function + " line 1 at " + action
	if action == "statement block local variable initialization" {
		d.Context = "PL/pgSQL function " + function + " line 1 during " + action
	}
	return d
}
