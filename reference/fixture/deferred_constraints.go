package fixture

import (
	"database/sql"
	"testing"
)

// DeferredConstraintScenario owns a pinned transaction session for timing witnesses.
func DeferredConstraintScenario(t *testing.T, body func(*testing.T, *Pair, Backend, *sql.Conn, func(Step))) {
	t.Helper()
	p := NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := ConcurrentIndexConn(t, p, backend)
			run := func(s Step) { RunStep(t, p.Ctx, c, backend.Recorder, s) }
			body(t, p, backend, c, run)
		})
	}
}

func DeferredUniqueConflict(table, name, column, value string) Diagnostic {
	return ConcurrentIndexUnique(table, name, "Key ("+column+")=("+value+") already exists.", false)
}

func DeferredConstraintFlags(query string, rows ...[]any) Step {
	return Select(query, []string{"conname", "condeferrable", "condeferred"}, []string{"NAME", "BOOL", "BOOL"}, rows)
}
