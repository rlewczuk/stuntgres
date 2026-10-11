package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// Legacy smoke called DB/Stmt methods without Context. Preserve those public
// routes while using the common fixture's independent row/metadata assertions.
func deleteUsingNonContextRun(t *testing.T, p *fixture.Pair, b fixture.Backend, db *sql.DB) func(fixture.Step) {
	t.Helper()
	return func(step fixture.Step) { step.NonContext = true; fixture.RunStep(t, p.Ctx, db, b.Recorder, step) }
}

type deleteUsingNonContextHeld struct {
	fixture.HeldStatement
	direct fixture.NonContextExecutor
}

func (s deleteUsingNonContextHeld) Prepare(query string) (*sql.Stmt, error) {
	return s.direct.Prepare(query)
}
func (s deleteUsingNonContextHeld) Exec(query string, args ...any) (sql.Result, error) {
	if query != s.SQL {
		return s.direct.Exec(query, args...)
	}
	return s.Statement.Exec(args...)
}
func (s deleteUsingNonContextHeld) Query(query string, args ...any) (*sql.Rows, error) {
	if query != s.SQL {
		return s.direct.Query(query, args...)
	}
	return s.Statement.Query(args...)
}
