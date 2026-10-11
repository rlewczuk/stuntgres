package fixture

import (
	"context"
	"github.com/lib/pq"
	"testing"
	"time"
)

func SchemaReferenceCleanup(t *testing.T, names ...string) {
	t.Helper()
	t.Cleanup(func() {
		if !Started {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, name := range names {
			if _, err := Admin.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+pq.QuoteIdentifier(name)+" CASCADE"); err != nil {
				t.Errorf("schema cleanup %s: %v", name, err)
			}
		}
	})
}
func SchemaReferenceError(query, code, message, detail string, prepared bool, phase, position string) Step {
	s := RoleReferenceError(query, code, message, detail, prepared, phase)
	s.Diagnostic.Position = position
	return s
}

// Creation destination diagnostics also preserve the AST target position on
// both backends without a backend-specific position override.
func SchemaReferenceCreationError(query, code, message string, prepared bool, position string) Step {
	s := RoleReferenceError(query, code, message, "", prepared, "execute")
	s.Diagnostic.Position = position
	return s
}
