package smoke_test

import (
	"database/sql"
	"testing"

	"stuntgres"
)

func TestCastExplicitReviewSmoke(t *testing.T) {
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE t(id INT4,i INT4,s TEXT)"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		q        string
		want     stuntgres.Error
		analysis bool
	}{
		{"SELECT id FROM t WHERE i=int8(TRUE)", stuntgres.Error{Code: "42883", Message: "function int8(boolean) does not exist", Position: "26", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT id FROM t WHERE i=TRUE::bool", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer = boolean", Position: "25", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT 2147483648::int4 FROM t", stuntgres.Error{Code: "22003", Message: "integer out of range"}, false},
		{"SELECT CAST('oops'::text AS int4) FROM t", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}, false},
		{"SELECT i FROM t WHERE i=2147483648::int4", stuntgres.Error{Code: "22003", Message: "integer out of range"}, false},
	} {
		t.Run(tc.q, func(t *testing.T) {
			t.Run("direct", func(t *testing.T) {
				rows, err := db.Query(tc.q)
				if rows != nil {
					rows.Close()
					t.Fatal("invalid cast exposed cursor")
				}
				if err := typeCoreDiagnostic(err, tc.want); err != nil {
					t.Fatal(err)
				}
			})
			t.Run("prepared", func(t *testing.T) {
				stmt, err := db.Prepare(tc.q)
				if tc.analysis {
					if stmt != nil {
						stmt.Close()
						t.Fatal("invalid analysis exposed statement")
					}
					if err := typeCoreDiagnostic(err, tc.want); err != nil {
						t.Fatal(err)
					}
					return
				}
				if err != nil {
					t.Fatalf("expected successful Prepare: %v", err)
				}
				defer stmt.Close()
				rows, err := stmt.Query()
				if rows != nil {
					rows.Close()
					t.Fatal("immutable cast failure exposed cursor")
				}
				if err := typeCoreDiagnostic(err, tc.want); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
	for _, q := range []string{"SELECT s::int4 FROM t", "SELECT i FROM t WHERE s::bool"} {
		stmt, err := db.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := stmt.Query()
		if err != nil {
			stmt.Close()
			t.Fatal(err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Error("row-dependent conversion evaluated without rows", rows.Err())
		}
		rows.Close()
		stmt.Close()
	}
}
