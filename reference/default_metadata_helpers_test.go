package reference_test

import (
	"database/sql"
	"testing"
)

// Defaults consumer projections have independently known nullable columns.
// The logical runner still owns raw cells, recording, exhaustion and closure.
func defaultReferenceRunStep(t *testing.T, p *referencePair, db *sql.DB, r *referenceRecorder, s referenceStep) {
	t.Helper()
	if s.selectRows && s.state == "" {
		nullable := make([]bool, len(s.columns))
		for i := range nullable {
			nullable[i] = true
		}
		referenceTableDDLMetadata(t, p.ctx, db, db == p.postgres, s.prepared, s.query, s.columns, s.types, nullable, len(s.rows))
	}
	referenceRunStep(t, p.ctx, db, r, s)
}

func defaultReferenceStep(t *testing.T, p *referencePair, s referenceStep) {
	t.Helper()
	if s.selectRows && s.state == "" {
		nullable := make([]bool, len(s.columns))
		for i := range nullable {
			nullable[i] = true
		}
		for _, backend := range p.backends() {
			referenceTableDDLMetadata(t, p.ctx, backend.db, backend.pg, s.prepared, s.query, s.columns, s.types, nullable, len(s.rows))
		}
	}
	p.step(t, s)
}
