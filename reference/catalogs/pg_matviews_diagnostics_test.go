package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGMatviewsDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, c := range []struct{ query, column, hint string }{
		{"SELECT oid FROM pg_catalog.pg_matviews", "oid", ""},
		{"SELECT relispopulated FROM pg_catalog.pg_matviews WHERE FALSE", "relispopulated", `Perhaps you meant to reference the column "pg_matviews.ispopulated".`},
		{"SELECT hasrules FROM pg_catalog.pg_matviews LIMIT 0", "hasrules", ""},
	} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(c.query, fixture.Diagnostic{Code: "42703", Message: `column "` + c.column + `" does not exist`, Hint: c.hint, Position: "8"})
			s.SelectRows, s.Prepared = true, prepared
			if prepared {
				s.ErrorStage = "prepare"
			}
			p.Step(t, s)
		}
	}
}
