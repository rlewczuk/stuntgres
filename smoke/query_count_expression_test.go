package smoke_test

import (
	"database/sql"
	"strings"
	"stuntgres"
	"testing"
)

func TestCountExpressionSmoke(t *testing.T) {
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	exec := func(q string, want int64) {
		t.Helper()
		r, err := db.Exec(q)
		if err != nil {
			t.Fatal(q, err)
		}
		n, err := r.RowsAffected()
		if err != nil || n != want {
			t.Fatal(q, n, err)
		}
	}
	exec("CREATE TABLE t(k TEXT,v INT)", 0)
	exec("INSERT INTO t VALUES('a',1),('a',NULL),(NULL,3)", 3)
	exec("CREATE TABLE empty(k TEXT,v INT)", 0)
	exec("CREATE TABLE allnull(v INT)", 0)
	exec("INSERT INTO allnull VALUES(NULL),(NULL)", 2)
	for _, tc := range []struct {
		q            string
		args         []any
		names, types []string
		want         [][]any
	}{
		{"SELECT count(v) FROM t", nil, []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}},
		{"SELECT count(*),count(v),count(k),count(NULL) FROM t", nil, []string{"count", "count", "count", "count"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(3), int64(2), int64(2), int64(0)}}},
		{"SELECT count(v) FROM empty", nil, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}},
		{"SELECT count(v) FROM allnull", nil, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}},
		{"SELECT k,count(v) FROM t GROUP BY k", nil, []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(1)}, {nil, int64(1)}}},
		{"SELECT k,count(v) FROM empty GROUP BY k", nil, []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{}},
		{"SELECT k FROM t GROUP BY k HAVING count(v)>0", nil, []string{"k"}, []string{"TEXT"}, [][]any{{"a"}, {nil}}},
		{"SELECT count(v+1),count(NULLIF(v,1)) FROM t", nil, []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(2), int64(1)}}},
		{"SELECT count(CAST($1 AS INT)) FROM t", []any{7}, []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}},
		{"SELECT count(CAST($1 AS INT)) FROM t", []any{nil}, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}},
	} {
		rows, err := db.Query(tc.q, tc.args...)
		aggregateSmokeRows(t, rows, err, tc.names, tc.types, tc.want)
		stmt, err := db.Prepare(tc.q)
		if err != nil {
			t.Fatal(err)
		}
		rows, err = stmt.Query(tc.args...)
		aggregateSmokeRows(t, rows, err, tc.names, tc.types, tc.want)
		stmt.Close()
	}
	stmt, err := db.Prepare("SELECT count(v) FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	exec("INSERT INTO t VALUES('b',NULL)", 1)
	rows, err := stmt.Query()
	aggregateSmokeRows(t, rows, err, []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}})
	exec("INSERT INTO t VALUES('b',4)", 1)
	rows, err = stmt.Query()
	aggregateSmokeRows(t, rows, err, []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}})
	exec("CREATE TABLE out(k TEXT,n BIGINT)", 0)
	exec("INSERT INTO out SELECT k,count(v) FROM t GROUP BY k", 3)
	exec("INSERT INTO out(n) SELECT count(v) FROM empty", 1)
	exec("INSERT INTO out(n) SELECT count(v) FROM empty GROUP BY k", 0)
	exec("INSERT INTO out(n) SELECT count(v) FROM t", 1)
	rows, err = db.Query("SELECT k,n FROM out")
	aggregateSmokeRows(t, rows, err, []string{"k", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(1)}, {nil, int64(1)}, {"b", int64(1)}, {nil, int64(0)}, {nil, int64(3)}})
	for _, tc := range []struct {
		q    string
		want stuntgres.Error
	}{
		{"SELECT count() FROM t", stuntgres.Error{Code: "42809", Message: "count(*) must be used to call a parameterless aggregate function", Position: "8"}},
		{"SELECT count(1,2) FROM t", stuntgres.Error{Code: "42883", Message: "function count(integer, integer) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}},
		{"SELECT pg_catalog.count() FROM t", stuntgres.Error{Code: "42809", Message: "pg_catalog.count(*) must be used to call a parameterless aggregate function", Position: "8"}},
		{"SELECT count(count(*),missing) FROM t", stuntgres.Error{Code: "42703", Message: `column "missing" does not exist`, Position: "23"}},
		{"SELECT count(count(*),1) FROM t", stuntgres.Error{Code: "42883", Message: "function count(bigint, integer) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}},
		{"SELECT count(count(*)) FROM t", stuntgres.Error{Code: "42803", Message: "aggregate function calls cannot be nested", Position: "14"}},
		{"SELECT count(" + strings.Repeat("1,", 100) + "1) FROM t", stuntgres.Error{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}},
	} {
		rows, err := db.Query(tc.q)
		if rows != nil {
			rows.Close()
			t.Fatal("error exposed rows")
		}
		if diagnostic := typeCoreDiagnostic(err, tc.want); diagnostic != nil {
			t.Fatal(diagnostic)
		}
		stmt, err := db.Prepare(tc.q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("error exposed statement")
		}
		if diagnostic := typeCoreDiagnostic(err, tc.want); diagnostic != nil {
			t.Fatal(diagnostic)
		}
	}
}
