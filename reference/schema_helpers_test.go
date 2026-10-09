package reference_test

import (
	"context"
	"github.com/lib/pq"
	"testing"
	"time"
)

func schemaReferenceCleanup(t *testing.T, names ...string) {
	t.Helper()
	t.Cleanup(func() {
		if !referenceStarted {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, name := range names {
			if _, err := referenceAdmin.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+pq.QuoteIdentifier(name)+" CASCADE"); err != nil {
				t.Errorf("schema cleanup %s: %v", name, err)
			}
		}
	})
}
func schemaReferenceError(query, code, message, detail string, prepared bool, phase, position string) referenceStep {
	s := roleReferenceError(query, code, message, detail, prepared, phase)
	s.diagnostic.Position = position
	return s
}

// Creation destination diagnostics also preserve the AST target position on
// both backends without a backend-specific position override.
func schemaReferenceCreationError(query, code, message string, prepared bool, position string) referenceStep {
	s := roleReferenceError(query, code, message, "", prepared, "execute")
	s.diagnostic.Position = position
	return s
}
