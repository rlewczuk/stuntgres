package stuntgres_test

import (
	"testing"

	"stuntgres"
)

func TestCountExpressionRegression(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "count-expression-regression")
	execSQL(t, db, "CREATE TABLE t(k TEXT,v INT)")
	execSQL(t, db, "INSERT INTO t VALUES('a',1),('a',NULL),(NULL,3)")
	execSQL(t, db, "CREATE TABLE empty(k TEXT,v INT)")
	execSQL(t, db, "CREATE TABLE allnull(k TEXT,v INT)")
	execSQL(t, db, "INSERT INTO allnull VALUES('a',NULL),(NULL,NULL)")
	for _, tc := range []struct {
		query        string
		args         []any
		names, types []string
		want         [][]any
	}{
		{"SELECT count(v) FROM t", nil, []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}},
		{"SELECT count(*),count(v),count(k),count(NULL) FROM t", nil, []string{"count", "count", "count", "count"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(3), int64(2), int64(2), int64(0)}}},
		{"SELECT count(v),count(v),count(1),count(FALSE),count(''),count(0) FROM t", nil, []string{"count", "count", "count", "count", "count", "count"}, []string{"INT8", "INT8", "INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(2), int64(2), int64(3), int64(3), int64(3), int64(3)}}},
		{"SELECT count(v) FROM empty", nil, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}},
		{"SELECT count(v),count(1) FROM allnull", nil, []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(0), int64(2)}}},
		{"SELECT count(v) FROM t WHERE FALSE", nil, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}},
		{"SELECT count(v) FROM t WHERE NULL", nil, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}},
		{"SELECT k,count(v) FROM t GROUP BY k", nil, []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(1)}, {nil, int64(1)}}},
		{"SELECT k,count(v) FROM empty GROUP BY k", nil, []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{}},
		{"SELECT k FROM t GROUP BY k HAVING count(v)>0", nil, []string{"k"}, []string{"TEXT"}, [][]any{{"a"}, {nil}}},
		{"SELECT count(v+1),count(NULLIF(v,1)) FROM t", nil, []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(2), int64(1)}}},
		{"SELECT count(v IS NULL),count(v>1),count((v>1) OR v IS NULL) FROM t", nil, []string{"count", "count", "count"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(3), int64(2), int64(3)}}},
		{"SELECT count(abs(v)) FROM t", nil, []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}},
		{"SELECT count(v)+1,COALESCE(count(v),0) FROM t", nil, []string{"?column?", "coalesce"}, []string{"INT8", "INT8"}, [][]any{{int64(3), int64(2)}}},
		{"SELECT count(CAST($1 AS INT)) FROM t", []any{7}, []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}},
		{"SELECT count(CAST($1 AS INT)) FROM t", []any{nil}, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}},
		{"SELECT count(CAST($1 AS INT)),count($1) FROM t", []any{7}, []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(3), int64(3)}}},
		{"SELECT count(CAST($1 AS INT)),count($1) FROM t", []any{nil}, []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(0), int64(0)}}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			t.Run("direct", func(t *testing.T) {
				rows, err := db.Query(tc.query, tc.args...)
				queryCoreRows(t, rows, err, tc.names, tc.types, tc.want, true)
			})
			t.Run("prepared", func(t *testing.T) {
				stmt, err := db.Prepare(tc.query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				rows, err := stmt.Query(tc.args...)
				queryCoreRows(t, rows, err, tc.names, tc.types, tc.want, true)
			})
		})
	}
	// Exact records validated by the root's pinned PostgreSQL 18.0 checkpoint.
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, tc := range []struct {
		query string
		args  []any
		want  stuntgres.Error
	}{
		{"SELECT count() FROM t", nil, stuntgres.Error{Code: "42809", Message: "count(*) must be used to call a parameterless aggregate function", Position: "8"}},
		{"SELECT /*é*/ count() FROM t", nil, stuntgres.Error{Code: "42809", Message: "count(*) must be used to call a parameterless aggregate function", Position: "14"}},
		{"SELECT count() FROM empty WHERE FALSE", nil, stuntgres.Error{Code: "42809", Message: "count(*) must be used to call a parameterless aggregate function", Position: "8"}},
		{"SELECT count(1,2) FROM t", nil, stuntgres.Error{Code: "42883", Message: "function count(integer, integer) does not exist", Hint: hint, Position: "8"}},
		{"SELECT count(CAST(NULL AS INT),CAST(NULL AS TEXT)) FROM t", nil, stuntgres.Error{Code: "42883", Message: "function count(integer, text) does not exist", Hint: hint, Position: "8"}},
		{"SELECT count(NULL,'x') FROM t", nil, stuntgres.Error{Code: "42883", Message: "function count(unknown, unknown) does not exist", Hint: hint, Position: "8"}},
		{"SELECT pg_catalog.count(1,2) FROM t", nil, stuntgres.Error{Code: "42883", Message: "function pg_catalog.count(integer, integer) does not exist", Hint: hint, Position: "8"}},
		{"SELECT count(missing,1) FROM t", nil, stuntgres.Error{Code: "42703", Message: `column "missing" does not exist`, Position: "14"}},
		{"SELECT count(missing) FROM t", nil, stuntgres.Error{Code: "42703", Message: `column "missing" does not exist`, Position: "14"}},
		{"SELECT /*é*/ count(missing) FROM empty WHERE FALSE", nil, stuntgres.Error{Code: "42703", Message: `column "missing" does not exist`, Position: "20"}},
		{"SELECT count(count(*)) FROM t", nil, stuntgres.Error{Code: "42803", Message: "aggregate function calls cannot be nested", Position: "14"}},
		{"SELECT count(count(v)) FROM empty WHERE FALSE", nil, stuntgres.Error{Code: "42803", Message: "aggregate function calls cannot be nested", Position: "14"}},
		{"SELECT count(CASE WHEN FALSE THEN count(v) ELSE 1 END) FROM t", nil, stuntgres.Error{Code: "42803", Message: "aggregate function calls cannot be nested", Position: "35"}},
		{"SELECT count(v) FROM t WHERE count(v)>0", nil, stuntgres.Error{Code: "42803", Message: "aggregate functions are not allowed in WHERE", Position: "30"}},
		{"SELECT k FROM t GROUP BY count(v)", nil, stuntgres.Error{Code: "42803", Message: "aggregate functions are not allowed in GROUP BY", Position: "26"}},
		{"SELECT count(1/0) FROM t", nil, stuntgres.Error{Code: "22012", Message: "division by zero"}},
		{"SELECT v FROM t LIMIT count(v)", nil, stuntgres.Error{Code: "42803", Message: "aggregate functions are not allowed in LIMIT", Position: "23"}},
		{"SELECT v FROM t OFFSET count(v)", nil, stuntgres.Error{Code: "42803", Message: "aggregate functions are not allowed in OFFSET", Position: "24"}},
		{"SELECT v FROM t FETCH FIRST count(v) ROWS ONLY", nil, stuntgres.Error{Code: "42803", Message: "aggregate functions are not allowed in LIMIT", Position: "29"}},
		{"SELECT count($1) FROM t", []any{7}, stuntgres.Error{Code: "42P18", Message: "could not determine data type of parameter $1"}},
		{"SELECT count($1) FROM t", []any{nil}, stuntgres.Error{Code: "42P18", Message: "could not determine data type of parameter $1"}},
		{"SELECT count($1),count(CAST($1 AS INT)) FROM t", []any{7}, stuntgres.Error{Code: "42P08", Message: "could not determine data type of parameter $1", Position: "14"}},
		{"SELECT count($1),count(CAST($1 AS INT)) FROM t", []any{nil}, stuntgres.Error{Code: "42P08", Message: "could not determine data type of parameter $1", Position: "14"}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			t.Run("direct", func(t *testing.T) {
				rows, err := db.Query(tc.query, tc.args...)
				if rows != nil {
					rows.Close()
					t.Fatal("failed Query exposed rows")
				}
				typeCoreDiagnostic(t, err, tc.want)
			})
			t.Run("prepared", func(t *testing.T) {
				stmt, err := db.Prepare(tc.query)
				if err == nil {
					defer stmt.Close()
					rows, queryErr := stmt.Query(tc.args...)
					if rows != nil {
						rows.Close()
						t.Fatal("failed statement Query exposed rows")
					}
					err = queryErr
				} else if stmt != nil {
					stmt.Close()
					t.Fatal("failed Prepare exposed a statement")
				}
				typeCoreDiagnostic(t, err, tc.want)
			})
		})
	}
}
