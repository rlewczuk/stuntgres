package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestByteaModifierPrecedence(t *testing.T) {
	for _, declaration := range []string{"BYTEA(1.5)", "BYTEA(2147483648)", "BYTEA(+1)", `"bytea"(1.5)`, `"bytea"(2147483648)`, `"bytea"(+1)`} {
		t.Run(declaration, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			q := "CREATE TABLE bad(v " + declaration + ")"
			want := stuntgres.Error{Code: "42601", Message: `type modifier is not allowed for type "bytea"`, Position: "20"}
			assertAbsent := func() {
				t.Helper()
				rows, err := db.Query("SELECT v FROM bad")
				if rows != nil {
					rows.Close()
				}
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "bad" does not exist`, Position: "15"})
			}
			stmt, err := db.Prepare(q)
			if err != nil {
				t.Fatal("metadata-only DDL Prepare:", err)
			}
			defer stmt.Close()
			assertAbsent()
			_, err = stmt.Exec()
			typeCoreDiagnostic(t, err, want)
			assertAbsent()
			_, err = db.Exec(q)
			typeCoreDiagnostic(t, err, want)
			assertAbsent()
		})
	}
}
