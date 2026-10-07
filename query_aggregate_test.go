package stuntgres_test

import (
	"testing"

	"stuntgres"
)

func TestQueryAggregateRegression(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "query-aggregate-regression")
	execSQL(t, db, "CREATE TABLE t(k TEXT,v INT)")
	execSQL(t, db, "INSERT INTO t VALUES('a',1),('a',NULL),(NULL,2)")
	execSQL(t, db, "CREATE TABLE empty(k TEXT,v INT)")
	for _, tc := range []struct {
		query        string
		names, types []string
		want         [][]any
	}{
		{"SELECT k,count(*) FROM t GROUP BY k", []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {nil, int64(1)}}},
		{"SELECT count(*) FROM empty", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}},
		{"SELECT k,count(*) FROM empty GROUP BY k", []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{}},
		{"SELECT k,count(*) FROM t GROUP BY k HAVING count(*)>1", []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}}},
		{"SELECT k FROM t GROUP BY k HAVING count(*)>1", []string{"k"}, []string{"TEXT"}, [][]any{{"a"}}},
		{"SELECT 7 FROM empty HAVING TRUE", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(7)}}},
		{"SELECT (v+1)*2,count(*) FROM t GROUP BY v+1", []string{"?column?", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(4), int64(1)}, {nil, int64(1)}, {int64(6), int64(1)}}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			t.Run("direct", func(t *testing.T) {
				rows, err := db.Query(tc.query)
				queryCoreRows(t, rows, err, tc.names, tc.types, tc.want, true)
			})
			t.Run("prepared", func(t *testing.T) {
				stmt, err := db.Prepare(tc.query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				rows, err := stmt.Query()
				queryCoreRows(t, rows, err, tc.names, tc.types, tc.want, true)
			})
		})
	}
	for _, tc := range []struct{ query, relation, position string }{
		{"SELECT v,count(*) FROM t", "t", "8"},
		{"SELECT /*é*/ v,count(*) FROM t", "t", "14"},
		{"SELECT v,count(*) FROM empty HAVING FALSE", "empty", "8"},
		{"SELECT v,count(*) FROM t GROUP BY v+1", "t", "8"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			want := stuntgres.Error{Code: "42803", Message: `column "` + tc.relation + `.v" must appear in the GROUP BY clause or be used in an aggregate function`, Position: tc.position}
			t.Run("direct", func(t *testing.T) {
				rows, err := db.Query(tc.query)
				if rows != nil {
					rows.Close()
					t.Fatal("failed Query exposed rows")
				}
				typeCoreDiagnostic(t, err, want)
			})
			t.Run("prepared", func(t *testing.T) {
				stmt, err := db.Prepare(tc.query)
				if stmt != nil {
					stmt.Close()
					t.Fatal("failed Prepare exposed a statement")
				}
				typeCoreDiagnostic(t, err, want)
			})
		})
	}
}
