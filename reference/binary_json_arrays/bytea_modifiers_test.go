package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceByteaModifierPrecedence(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		for _, declaration := range []string{"BYTEA(1.5)", "BYTEA(2147483648)", "BYTEA(+1)", `"bytea"(1.5)`, `"bytea"(2147483648)`, `"bytea"(+1)`} {
			t.Run(b.Name+"/"+declaration, func(t *testing.T) {
				q := "CREATE TABLE bad(v " + declaration + ")"
				stmt, err := b.DB.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal("metadata-only DDL Prepare", err)
				}
				defer func() {
					if err := stmt.Close(); err != nil {
						t.Error(err)
					}
				}()
				absent := fixture.ImplicitError("SELECT v FROM bad", fixture.Diagnostic{Code: "42P01", Message: `relation "bad" does not exist`, Position: "15"})
				absent.SelectRows = true
				absent.ErrorStage = "execute"
				bad := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: `type modifier is not allowed for type "bytea"`, Position: "20"})
				bad.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, absent)
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{b.DB, stmt, q}, b.Recorder, bad)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, absent)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, bad)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, absent)
			})
		}
	}
}
