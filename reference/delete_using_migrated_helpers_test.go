package reference_test

import (
	"database/sql"
	"testing"
)

// Legacy smoke called DB/Stmt methods without Context. Preserve those public
// routes while using the common fixture's independent row/metadata assertions.
func deleteUsingNonContextRun(t *testing.T, p *referencePair, b referenceBackend, db *sql.DB) func(referenceStep) {
	t.Helper()
	return func(step referenceStep) { step.nonContext = true; referenceRunStep(t, p.ctx, db, b.recorder, step) }
}

type deleteUsingNonContextHeld struct {
	referenceHeldStatement
	direct referenceNonContextExecutor
}

func (s deleteUsingNonContextHeld) Prepare(query string) (*sql.Stmt, error) {
	return s.direct.Prepare(query)
}
func (s deleteUsingNonContextHeld) Exec(query string, args ...any) (sql.Result, error) {
	if query != s.query {
		return s.direct.Exec(query, args...)
	}
	return s.statement.Exec(args...)
}
func (s deleteUsingNonContextHeld) Query(query string, args ...any) (*sql.Rows, error) {
	if query != s.query {
		return s.direct.Query(query, args...)
	}
	return s.statement.Query(args...)
}
