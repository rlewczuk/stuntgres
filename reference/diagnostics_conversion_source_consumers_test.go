package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceErrorFieldsConversionSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			driverExecSQL(t, db, "CREATE TABLE diagnostics (id INT, flag BOOLEAN)")
			for _, tc := range []struct{ query, value, code, message string }{
				{"INSERT INTO diagnostics (flag,id) VALUES ($1,$2)", "ambiguous", "22P02", `invalid input syntax for type boolean: "ambiguous"`},
				{"SELECT id FROM diagnostics WHERE id=$1", "not an integer", "22P02", `invalid input syntax for type integer: "not an integer"`},
				{"SELECT id FROM diagnostics WHERE id=$1", "2147483648", "22003", `value "2147483648" is out of range for type integer`},
			} {
				args := []any{tc.value}
				queryRows := strings.HasPrefix(tc.query, "SELECT ")
				if !queryRows {
					args = append(args, 1)
				}
				want := controlsReferenceDiagnostic(tc.code, tc.message)
				want.Context = "unnamed portal parameter $1 = '...'"
				for _, prepared := range []bool{false, true} {
					var err error
					if prepared {
						stmt, e := db.Prepare(tc.query)
						if e != nil {
							t.Fatalf("Prepare conversion: %v", e)
						}
						defer stmt.Close()
						if queryRows {
							rows, e := stmt.Query(args...)
							err = e
							if rows != nil {
								rows.Close()
								t.Fatal("failed held Query exposed rows")
							}
						} else {
							result, e := stmt.Exec(args...)
							err = e
							if result != nil {
								t.Fatal("failed held Exec exposed result")
							}
						}
						if e := stmt.Close(); e != nil {
							t.Fatal(e)
						}
					} else if queryRows {
						rows, e := db.Query(tc.query, args...)
						err = e
						if rows != nil {
							rows.Close()
							t.Fatal("failed Query exposed rows")
						}
					} else {
						result, e := db.Exec(tc.query, args...)
						err = e
						if result != nil {
							t.Fatal("failed Exec exposed result")
						}
					}
					referenceAssertDiagnostic(t, err, &want)
				}
			}
			driverWantTuples(t, db, "SELECT * FROM diagnostics", [][]any{})
		})
	}
}

func TestReferenceErrorFieldsNotNullSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			driverExecSQL(t, db, "CREATE TABLE diagnostic_rows (id INT NOT NULL,note TEXT)")
			want := controlsReferenceDiagnostic("23502", `null value in column "id" of relation "diagnostic_rows" violates not-null constraint`)
			want.Detail, want.Schema, want.Table, want.Column = "Failing row contains (null, bad).", "public", "diagnostic_rows", "id"
			for _, query := range []string{"INSERT INTO diagnostic_rows VALUES (NULL,'bad')", "INSERT INTO diagnostic_rows VALUES (1,'good'),(NULL,'bad')"} {
				result, err := db.Exec(query)
				if result != nil {
					t.Fatal("failed Exec exposed result")
				}
				referenceAssertDiagnostic(t, err, &want)
				driverWantTuples(t, db, "SELECT id,note FROM diagnostic_rows ORDER BY id", [][]any{})
			}
			stmt, err := db.Prepare("INSERT INTO diagnostic_rows VALUES ($1,$2)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			result, err := stmt.Exec(nil, "bad")
			if result != nil {
				t.Fatal("failed held Exec exposed result")
			}
			if closeErr := stmt.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			referenceAssertDiagnostic(t, err, &want)
			driverWantTuples(t, db, "SELECT id,note FROM diagnostic_rows ORDER BY id", [][]any{})
			rows, err := db.Query("SELECT id,note FROM diagnostic_rows")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			driverBasicMetadata(t, rows, backend.pg, []string{"INT4", "TEXT"}, []bool{false, true})
			if rows.Next() || rows.Err() != nil {
				t.Fatalf("empty NN cursor: %v", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
