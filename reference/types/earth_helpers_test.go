package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func earthScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func earthInstall(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	cubeInstall(t, p, b, c)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION earthdistance", 0))
}

func earthCheckDiagnostic(schema, constraint string, visible bool) fixture.Diagnostic {
	name := schema + ".earth"
	if visible {
		name = "earth"
	}
	want := fixture.ControlsReferenceDiagnostic("23514", `value for domain `+name+` violates check constraint "`+constraint+`"`)
	want.Schema, want.DataType, want.Constraint = schema, "earth", constraint
	return want
}

// Domain wire results flatten to the installed cube base, confirmed by the
// root's pinned PostgreSQL18.6 oracle run.
func earthSelect(b fixture.Backend, q string, columns []string, rows [][]any, args ...any) fixture.Step {
	return cubeSelect(b, q, columns, rows, args...)
}

func earthAbsent(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT extname FROM pg_extension WHERE extname='earthdistance'", []string{"extname"}, []string{"NAME"}, [][]any{}))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('earth','_earth') ORDER BY typname", []string{"typname"}, []string{"NAME"}, [][]any{}))
}

func earthHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, q string) fixture.HeldStatement {
	t.Helper()
	s, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	return fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
}
