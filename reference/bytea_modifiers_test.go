package reference_test

import "testing"

func TestReferenceByteaModifierPrecedence(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		for _, declaration := range []string{"BYTEA(1.5)", "BYTEA(2147483648)", "BYTEA(+1)", `"bytea"(1.5)`, `"bytea"(2147483648)`, `"bytea"(+1)`} {
			t.Run(b.name+"/"+declaration, func(t *testing.T) {
				q := "CREATE TABLE bad(v " + declaration + ")"
				stmt, err := b.db.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal("metadata-only DDL Prepare", err)
				}
				defer func() {
					if err := stmt.Close(); err != nil {
						t.Error(err)
					}
				}()
				absent := referenceImplicitError("SELECT v FROM bad", referenceDiagnostic{Code: "42P01", Message: `relation "bad" does not exist`, Position: "15"})
				absent.selectRows = true
				absent.errorStage = "execute"
				bad := referenceImplicitError(q, referenceDiagnostic{Code: "42601", Message: `type modifier is not allowed for type "bytea"`, Position: "20"})
				bad.errorStage = "execute"
				referenceRunStep(t, p.ctx, b.db, b.recorder, absent)
				referenceRunStep(t, p.ctx, referenceHeldStatement{b.db, stmt, q}, b.recorder, bad)
				referenceRunStep(t, p.ctx, b.db, b.recorder, absent)
				referenceRunStep(t, p.ctx, b.db, b.recorder, bad)
				referenceRunStep(t, p.ctx, b.db, b.recorder, absent)
			})
		}
	}
}
