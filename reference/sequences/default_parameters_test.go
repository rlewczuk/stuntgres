package reference_test

import (
	"errors"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDefaultParameterUtilityArgumentBoundary(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					q := `CREATE TABLE bad (i integer DEFAULT $1)`
					var supplied, semantic error
					if prepared {
						stmt, err := backend.DB.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal("utility Prepare", err)
						}
						defer stmt.Close()
						_, supplied = stmt.ExecContext(p.Ctx, 7)
						_, semantic = stmt.ExecContext(p.Ctx)
					} else {
						_, supplied = backend.DB.ExecContext(p.Ctx, q, 7)
						_, semantic = backend.DB.ExecContext(p.Ctx, q)
					}
					var state interface{ SQLState() string }
					if supplied == nil || errors.As(supplied, &state) {
						t.Fatal("ordinary adapter count boundary changed", supplied)
					}
					if backend.PG {
						want := "pq: got 1 parameters but the statement requires 0"
						if prepared {
							want = "sql: expected 0 arguments, got 1"
						}
						if supplied.Error() != want {
							t.Fatalf("argument error=%q want=%q", supplied.Error(), want)
						}
					}
					fixture.AssertDiagnostic(t, semantic, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P02", Message: "there is no parameter $1", Position: "37"})
				})
			}
		})
	}
}
