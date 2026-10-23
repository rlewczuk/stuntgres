package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func routineAlterError(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q, code, message string) {
	t.Helper()
	routineDropError(t, p, b, c, q, fixture.ControlsReferenceDiagnostic(code, message))
}

func routineAlterOID(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) int64 {
	t.Helper()
	var oid int64
	if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_proc WHERE proname=$1", name).Scan(&oid); err != nil {
		t.Fatal(err)
	}
	return oid
}

func routineAlterSameOID(t *testing.T, p *fixture.Pair, c *sql.Conn, name string, want int64) {
	t.Helper()
	if got := routineAlterOID(t, p, c, name); got != want {
		t.Fatalf("%s identity changed: got %d want %d", name, got, want)
	}
}

func routineAlterRows(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, names, types []string, rows [][]any) {
	t.Helper()
	s := fixture.Select(q, names, types, rows)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}
