package fixture

import (
	"database/sql"
	"testing"
)

// Defaults consumer projections have independently known nullable columns.
// The logical runner still owns raw cells, recording, exhaustion and closure.
func DefaultReferenceRunStep(t *testing.T, p *Pair, db *sql.DB, r *Recorder, s Step) {
	t.Helper()
	if s.SelectRows && s.State == "" {
		nullable := make([]bool, len(s.Columns))
		for i := range nullable {
			nullable[i] = true
		}
		TableDDLMetadata(t, p.Ctx, db, db == p.Postgres, s.Prepared, s.Query, s.Columns, s.Types, nullable, len(s.Rows))
	}
	RunStep(t, p.Ctx, db, r, s)
}

func DefaultReferenceStep(t *testing.T, p *Pair, s Step) {
	t.Helper()
	if s.SelectRows && s.State == "" {
		nullable := make([]bool, len(s.Columns))
		for i := range nullable {
			nullable[i] = true
		}
		for _, backend := range p.Backends() {
			TableDDLMetadata(t, p.Ctx, backend.DB, backend.PG, s.Prepared, s.Query, s.Columns, s.Types, nullable, len(s.Rows))
		}
	}
	p.Step(t, s)
}
