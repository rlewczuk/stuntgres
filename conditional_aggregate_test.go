package stuntgres_test

import (
	"testing"

	"stuntgres"
)

func TestConditionalAggregateComposition(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(v INTEGER)")
	execSQL(t, db, "INSERT INTO t VALUES(1),(1),(2)")
	execSQL(t, db, "CREATE TABLE empty(v INTEGER)")
	execSQL(t, db, "CREATE TABLE out(v BIGINT)")
	for _, tc := range []struct {
		q            string
		names, types []string
		want         [][]any
	}{
		{"SELECT CASE count(*) WHEN 3 THEN count(*) ELSE 0 END,COALESCE(count(*),0),NULLIF(count(*),0),GREATEST(count(*),2),LEAST(count(*),2) FROM t", []string{"case", "coalesce", "nullif", "greatest", "least"}, []string{"INT8", "INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(3), int64(3), int64(3), int64(3), int64(2)}}},
		{"SELECT CASE WHEN count(*)=3 THEN 7 ELSE 0 END FROM t HAVING COALESCE(count(*),0)>0", []string{"case"}, []string{"INT4"}, [][]any{{int64(7)}}},
		{"SELECT CASE WHEN v=1 THEN 10 ELSE 20 END,count(*) FROM t GROUP BY CASE WHEN v=1 THEN 10 ELSE 20 END", []string{"case", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(10), int64(2)}, {int64(20), int64(1)}}},
		{"SELECT COALESCE(v,0),count(*) FROM t GROUP BY COALESCE(v,0)", []string{"coalesce", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(1)}}},
		{"SELECT CASE count(*) WHEN 0 THEN count(*) ELSE 9 END,COALESCE(count(*),9),NULLIF(count(*),0),GREATEST(count(*),2),LEAST(count(*),2) FROM empty", []string{"case", "coalesce", "nullif", "greatest", "least"}, []string{"INT8", "INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(0), int64(0), nil, int64(2), int64(0)}}},
		{"SELECT COALESCE(v,0),count(*) FROM empty GROUP BY COALESCE(v,0)", []string{"coalesce", "count"}, []string{"INT4", "INT8"}, [][]any{}},
	} {
		rows, err := db.Query(tc.q)
		queryCoreRows(t, rows, err, tc.names, tc.types, tc.want, true)
		stmt, err := db.Prepare(tc.q)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			rows, err = stmt.Query()
			queryCoreRows(t, rows, err, tc.names, tc.types, tc.want, true)
		}
		stmt.Close()
	}
	for _, q := range []string{
		"INSERT INTO out SELECT CASE count(*) WHEN 3 THEN count(*) ELSE 0 END FROM t",
		"INSERT INTO out SELECT COALESCE(count(*),9) FROM empty",
	} {
		result, err := db.Exec(q)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			t.Fatal(n, err)
		}
		stmt, err := db.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		result, err = stmt.Exec()
		stmt.Close()
		if err != nil {
			t.Fatal(err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			t.Fatal(n, err)
		}
	}
	rows, err := db.Query("SELECT v FROM out ORDER BY v")
	queryCoreRows(t, rows, err, []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}, {int64(0)}, {int64(3)}, {int64(3)}}, false)
	for _, tc := range []struct {
		q    string
		want stuntgres.Error
	}{
		{"SELECT CASE WHEN TRUE THEN count(*) ELSE v END FROM t", stuntgres.Error{Code: "42803", Message: `column "t.v" must appear in the GROUP BY clause or be used in an aggregate function`, Position: "42"}},
		{"SELECT COALESCE(count(*),0) FROM t GROUP BY 1", stuntgres.Error{Code: "42803", Message: "aggregate functions are not allowed in GROUP BY", Position: "17"}},
		{"SELECT count(*) FROM t WHERE COALESCE(count(*),0)>0", stuntgres.Error{Code: "42803", Message: "aggregate functions are not allowed in WHERE", Position: "39"}},
		{"SELECT COALESCE(count(*),nextval('s'))", stuntgres.Error{Code: "0A000", Message: "SELECT without FROM is unsupported", Position: "1"}},
	} {
		rows, err := db.Query(tc.q)
		if rows != nil {
			rows.Close()
			t.Fatal("invalid aggregate exposed rows")
		}
		typeCoreDiagnostic(t, err, tc.want)
		stmt, err := db.Prepare(tc.q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("invalid aggregate exposed statement")
		}
		typeCoreDiagnostic(t, err, tc.want)
	}
}
