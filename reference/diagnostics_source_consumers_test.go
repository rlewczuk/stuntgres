package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceErrorFieldsSyntaxSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			query := "INSERT INTO t VALUES ('é', )"
			want := controlsReferenceDiagnostic("42601", `syntax error at or near ")"`)
			want.Position = "28"
			for _, route := range []string{"exec", "query", "prepare"} {
				t.Run(route, func(t *testing.T) {
					var err error
					switch route {
					case "exec":
						result, e := db.Exec(query)
						err = e
						if result != nil {
							t.Fatal("failed Exec exposed result")
						}
					case "query":
						rows, e := db.Query(query)
						err = e
						if rows != nil {
							rows.Close()
							t.Fatal("failed Query exposed rows")
						}
					case "prepare":
						stmt, e := db.Prepare(query)
						err = e
						if stmt != nil {
							stmt.Close()
							t.Fatal("failed Prepare exposed statement")
						}
					}
					referenceAssertDiagnostic(t, err, &want)
				})
			}
			result, err := db.Exec("CREATE TABLE syntax_prefix (x INT); )")
			if result != nil {
				t.Fatal("failed prefix Exec exposed result")
			}
			driverSQLState(t, err, "42601")
			rows, err := db.Query("SELECT * FROM syntax_prefix")
			if rows != nil {
				rows.Close()
				t.Fatal("failed prefix Query exposed rows")
			}
			driverSQLState(t, err, "42P01")
		})
	}
}

func TestReferenceErrorFieldsEngineSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			driverExecSQL(t, db, "CREATE TABLE diagnostic_rows (id INT)")
			for _, tc := range []struct{ query, code, message, position string }{
				{"SELECT * FROM missing", "42P01", `relation "missing" does not exist`, "15"},
				{"SELECT missing FROM diagnostic_rows", "42703", `column "missing" does not exist`, "8"},
				{"SELECT * FROM diagnostic_rows WHERE missing=1", "42703", `column "missing" does not exist`, "37"},
				{"SELECT * FROM diagnostic_rows ORDER BY missing", "42703", `column "missing" does not exist`, "40"},
				{"CREATE TABLE absent.t (id INT)", "3F000", `schema "absent" does not exist`, "14"},
				{"CREATE TABLE pg_catalog.t (id INT)", "0A000", "system schema creation is unsupported", "14"},
				{"CREATE TABLE diagnostic_rows (id INT)", "42P07", `relation "diagnostic_rows" already exists`, ""},
			} {
				want := controlsReferenceDiagnostic(tc.code, tc.message)
				want.Position = tc.position
				if backend.pg && tc.code == "0A000" {
					want = controlsReferenceDiagnostic("42501", `permission denied to create "pg_catalog.t"`)
					want.Detail = "System catalog modifications are currently disallowed."
				}
				for _, route := range []string{"exec", "query", "prepare"} {
					t.Run(route+"/"+tc.query, func(t *testing.T) {
						var err error
						switch route {
						case "exec":
							result, e := db.Exec(tc.query)
							err = e
							if result != nil {
								t.Fatal("failed Exec exposed result")
							}
						case "query":
							rows, e := db.Query(tc.query)
							err = e
							if rows != nil {
								rows.Close()
								t.Fatal("failed Query exposed rows")
							}
						case "prepare":
							stmt, e := db.Prepare(tc.query)
							err = e
							if strings.HasPrefix(tc.query, "CREATE ") {
								if err != nil {
									t.Fatalf("effect-free utility Prepare: %v", err)
								}
								defer stmt.Close()
								result, e := stmt.Exec()
								err = e
								if result != nil {
									t.Fatal("failed held Exec exposed result")
								}
								if err := stmt.Close(); err != nil {
									t.Fatal(err)
								}
							} else if stmt != nil {
								stmt.Close()
								t.Fatal("failed Prepare exposed statement")
							}
						}
						referenceAssertDiagnostic(t, err, &want)
					})
				}
			}
		})
	}
}
