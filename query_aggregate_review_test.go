package stuntgres_test

import (
	"testing"

	"stuntgres"
)

func TestQueryAggregateReviewRegression(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(v NUMERIC)")
	execSQL(t, db, "INSERT INTO t VALUES(1),(2)")
	t.Run("constant representation", func(t *testing.T) {
		query := "SELECT v=1.0,count(*) FROM t GROUP BY v=1.00"
		want := stuntgres.Error{Code: "42803", Message: `column "t.v" must appear in the GROUP BY clause or be used in an aggregate function`, Position: "8"}
		rows, err := db.Query(query)
		if rows != nil {
			rows.Close()
			t.Fatal("invalid grouping exposed rows")
		}
		typeCoreDiagnostic(t, err, want)
		stmt, err := db.Prepare(query)
		if stmt != nil {
			stmt.Close()
			t.Fatal("invalid grouping exposed a statement")
		}
		typeCoreDiagnostic(t, err, want)
	})
	t.Run("identical literal control", func(t *testing.T) {
		query := "SELECT v=1.0,count(*) FROM t GROUP BY v=1.0"
		rows, err := db.Query(query)
		queryCoreRows(t, rows, err, []string{"?column?", "count"}, []string{"BOOL", "INT8"}, [][]any{{true, int64(1)}, {false, int64(1)}}, true)
		stmt, err := db.Prepare(query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		rows, err = stmt.Query()
		queryCoreRows(t, rows, err, []string{"?column?", "count"}, []string{"BOOL", "INT8"}, [][]any{{true, int64(1)}, {false, int64(1)}}, true)
	})
	t.Run("no FROM sibling admission", func(t *testing.T) {
		query := "SELECT count(*),CAST('0' AS regclass)"
		want := stuntgres.Error{Code: "0A000", Message: "SELECT without FROM is unsupported", Position: "1"}
		rows, err := db.Query(query)
		if rows != nil {
			rows.Close()
			t.Fatal("excluded singleton exposed rows")
		}
		typeCoreDiagnostic(t, err, want)
		stmt, err := db.Prepare(query)
		if stmt != nil {
			stmt.Close()
			t.Fatal("excluded singleton exposed a statement")
		}
		typeCoreDiagnostic(t, err, want)
		_, err = db.Exec("INSERT INTO t VALUES(3);" + query)
		if err == nil {
			t.Fatal("excluded singleton permitted prefix effects")
		}
		stmt, err = db.Prepare("INSERT INTO t VALUES(3);" + query)
		if stmt != nil {
			stmt.Close()
			t.Fatal("excluded prefix exposed a statement")
		}
		if err == nil {
			t.Fatal("excluded prefix Prepare succeeded")
		}
		rows, err = db.Query("SELECT count(*) FROM t")
		queryCoreRows(t, rows, err, []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}, true)
	})
}
