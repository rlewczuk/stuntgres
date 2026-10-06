//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceByteaModifierPrecedence(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		name string
		db   *sql.DB
	}{{"native", p.memory}, {"postgres", p.postgres}} {
		for _, declaration := range []string{"BYTEA(1.5)", "BYTEA(2147483648)", "BYTEA(+1)", `"bytea"(1.5)`, `"bytea"(2147483648)`, `"bytea"(+1)`} {
			t.Run(backend.name+"/"+declaration, func(t *testing.T) {
				q := "CREATE TABLE bad(v " + declaration + ")"
				want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `type modifier is not allowed for type "bytea"`, Position: "20"}
				assertAbsent := func() {
					t.Helper()
					rows, err := backend.db.QueryContext(p.ctx, "SELECT v FROM bad")
					if rows != nil {
						rows.Close()
					}
					referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "bad" does not exist`, Position: "15"})
				}
				stmt, err := backend.db.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal("metadata-only DDL Prepare:", err)
				}
				defer stmt.Close()
				assertAbsent()
				_, err = stmt.ExecContext(p.ctx)
				referenceAssertDiagnostic(t, err, &want)
				assertAbsent()
				_, err = backend.db.ExecContext(p.ctx, q)
				referenceAssertDiagnostic(t, err, &want)
				assertAbsent()
			})
		}
	}
}
