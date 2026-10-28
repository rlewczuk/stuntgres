package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// All routes retain exactly the same scenario and independent expectations.
func truncateRestartScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, fixture.ControlsReferenceFixture, bool)) {
	t.Helper()
	for _, held := range []bool{false, true} {
		name := "Direct"
		if held {
			name = "Held"
		}
		t.Run(name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					body(t, p, b, fixture.NewControlsReferenceFixture(t, p, b.PG), held)
				})
			}
		})
	}
}

func truncateRestartRoute(t *testing.T, p *fixture.Pair, x fixture.Executor, q string, held bool) fixture.Executor {
	t.Helper()
	if !held {
		return x
	}
	s, err := x.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return fixture.HeldStatement{Executor: x, Statement: s, SQL: q}
}

func truncateRestartValue(t *testing.T, f fixture.ControlsReferenceFixture, x fixture.Executor, q, label string, want int64) {
	t.Helper()
	truncateRun(t, f, x, fixture.Select(q, []string{label}, []string{"INT8"}, [][]any{{want}}))
}

func truncateRestartState(t *testing.T, f fixture.ControlsReferenceFixture, x fixture.Executor, sequence string, last int64, called bool) {
	t.Helper()
	truncateRun(t, f, x, fixture.Select("SELECT last_value,is_called FROM "+sequence, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{last, called}}))
}

func truncateRestartSeed(t *testing.T, f fixture.ControlsReferenceFixture) {
	t.Helper()
	truncateExec(t, f, f.A, "CREATE TABLE t(id SERIAL PRIMARY KEY)", 0)
	truncateExec(t, f, f.A, "INSERT INTO t DEFAULT VALUES", 1)
	truncateExec(t, f, f.A, "INSERT INTO t DEFAULT VALUES", 1)
}

func truncateRestartError(t *testing.T, f fixture.ControlsReferenceFixture, x fixture.Executor, q string, d fixture.Diagnostic) {
	t.Helper()
	s := fixture.ErrorDiagnostic(q, d)
	s.ErrorStage = "execute"
	truncateRun(t, f, x, s)
}

// Reuse the native public physical-session witness used by the live-lock and
// blocking-PID scenarios, independently of the SQL backend inquiry.
func truncateRestartPID(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) int {
	t.Helper()
	if b.PG {
		return fixture.LockPID(t, p.Ctx, c)
	}
	if _, err := c.ExecContext(p.Ctx, "CREATE TEMP SEQUENCE truncate_restart_pid_identity"); err != nil {
		t.Fatal(err)
	}
	var oid int64
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_my_temp_schema()").Scan(&oid); err != nil {
		t.Fatal(err)
	}
	var namespace string
	if err := c.QueryRowContext(p.Ctx, "SELECT nspname FROM pg_namespace WHERE oid=$1", oid).Scan(&namespace); err != nil {
		t.Fatal(err)
	}
	suffix, ok := strings.CutPrefix(namespace, "pg_temp_")
	id, err := strconv.ParseUint(suffix, 10, 31)
	if !ok || err != nil || id == 0 {
		t.Fatalf("native physical identity %q: %v", namespace, err)
	}
	return int(id)
}
