package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestQueryOrderVoidRegression(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE order_void_t(id INT)")
	execSQL(t, db, "CREATE SEQUENCE s START WITH 1")
	for _, population := range []string{"empty", "one", "many"} {
		if population == "one" {
			execSQL(t, db, "INSERT INTO order_void_t VALUES(1)")
		}
		if population == "many" {
			execSQL(t, db, "INSERT INTO order_void_t VALUES(2),(3)")
		}
		for _, tc := range []struct{ q, position string }{
			{"SELECT id FROM order_void_t ORDER BY setseed(0)", "38"},
			{"SELECT setseed(0) AS v FROM order_void_t ORDER BY v", "51"},
			{"SELECT setseed(0) FROM order_void_t ORDER BY 1", "46"},
			{"SELECT nextval('s') FROM order_void_t ORDER BY setseed(0)", "48"},
		} {
			t.Run(population+"/"+tc.q, func(t *testing.T) {
				execSQL(t, db, "ALTER SEQUENCE s RESTART WITH 1")
				want := stuntgres.Error{Code: "42883", Message: "could not identify an ordering operator for type void", Hint: "Use an explicit ordering operator or modify the query.", Position: tc.position}
				rows, err := db.Query(tc.q)
				if rows != nil {
					rows.Close()
					t.Fatal("analysis failure exposed cursor")
				}
				typeCoreDiagnostic(t, err, want)
				stmt, err := db.Prepare(tc.q)
				if stmt != nil {
					stmt.Close()
					t.Fatal("analysis failure exposed statement")
				}
				typeCoreDiagnostic(t, err, want)
				var next int64
				if err := db.QueryRow("SELECT nextval('s')").Scan(&next); err != nil || next != 1 {
					t.Fatal("failed analysis evaluated visible output", next, err)
				}
			})
		}
	}
}
