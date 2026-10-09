package reference_test

import (
	"errors"
	"fmt"
	"testing"
)

func TestReferenceDefaultParameterUtilityArgumentBoundary(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					q := `CREATE TABLE bad (i integer DEFAULT $1)`
					var supplied, semantic error
					if prepared {
						stmt, err := backend.db.PrepareContext(p.ctx, q)
						if err != nil {
							t.Fatal("utility Prepare", err)
						}
						defer stmt.Close()
						_, supplied = stmt.ExecContext(p.ctx, 7)
						_, semantic = stmt.ExecContext(p.ctx)
					} else {
						_, supplied = backend.db.ExecContext(p.ctx, q, 7)
						_, semantic = backend.db.ExecContext(p.ctx, q)
					}
					var state interface{ SQLState() string }
					if supplied == nil || errors.As(supplied, &state) {
						t.Fatal("ordinary adapter count boundary changed", supplied)
					}
					if backend.pg {
						want := "pq: got 1 parameters but the statement requires 0"
						if prepared {
							want = "sql: expected 0 arguments, got 1"
						}
						if supplied.Error() != want {
							t.Fatalf("argument error=%q want=%q", supplied.Error(), want)
						}
					}
					referenceAssertDiagnostic(t, semantic, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P02", Message: "there is no parameter $1", Position: "37"})
				})
			}
		})
	}
}
