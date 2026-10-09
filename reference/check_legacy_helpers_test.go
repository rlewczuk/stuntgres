//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

// Legacy DEPARSE callers deliberately retain the always-PostgreSQL fixture.
func referenceCheckBackends(t *testing.T, scenario func(*testing.T, func(referenceStep))) {
	p := newReferencePair(t)
	for _, b := range []struct {
		name string
		db   *sql.DB
		rec  *referenceRecorder
	}{{"postgres", p.postgres, p.postgresRecord}, {"memory", p.memory, p.memoryRecord}} {
		t.Run(b.name, func(t *testing.T) { scenario(t, func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.rec, s) }) })
	}
}
