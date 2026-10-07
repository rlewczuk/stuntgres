package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestQueryAggregateBehavior(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(k TEXT,v INT)")
	execSQL(t, db, "INSERT INTO t VALUES('a',1),('a',NULL),(NULL,2)")
	execSQL(t, db, "CREATE TABLE empty(k TEXT,v INT)")
	for _, tc := range []struct {
		q            string
		names, types []string
		want         [][]any
		args         []any
	}{
		{"SELECT k FROM t GROUP BY k", []string{"k"}, []string{"TEXT"}, [][]any{{"a"}, {nil}}, nil},
		{"SELECT count(*),count(*) FROM t", []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(3), int64(3)}}, nil},
		{"SELECT count(*) FROM t WHERE FALSE", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, nil},
		{"SELECT count(*) FROM t WHERE NULL", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, nil},
		{"SELECT count(*) FROM t WHERE v=NULL", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, nil},
		{"SELECT k,count(*) FROM t GROUP BY k,k", []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {nil, int64(1)}}, nil},
		{"SELECT 7 FROM t GROUP BY k", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}}, nil},
		{"SELECT count(*)+1 FROM empty", []string{"?column?"}, []string{"INT8"}, [][]any{{int64(1)}}, nil},
		{"SELECT count(*) FROM empty HAVING count(*)=0", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, nil},
		{"SELECT count(*) FROM empty HAVING count(*)>0", []string{"count"}, []string{"INT8"}, [][]any{}, nil},
		{"SELECT k,count(*) FROM t GROUP BY k HAVING k IS NULL", []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{{nil, int64(1)}}, nil},
		{"SELECT k FROM t GROUP BY k HAVING FALSE", []string{"k"}, []string{"TEXT"}, [][]any{}, nil},
		{"SELECT k FROM t GROUP BY k HAVING NULL", []string{"k"}, []string{"TEXT"}, [][]any{}, nil},
		{"SELECT count(*) FROM t HAVING $1", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}, []any{true}},
		{"SELECT count(*) FROM t HAVING $1", []string{"count"}, []string{"INT8"}, [][]any{}, []any{nil}},
		{"SELECT v+1,count(*) FROM t GROUP BY v", []string{"?column?", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(2), int64(1)}, {nil, int64(1)}, {int64(3), int64(1)}}, nil},
		{"SELECT k,count(*) FROM t GROUP BY 1", []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {nil, int64(1)}}, nil},
		{"SELECT count(*) FROM t GROUP BY +1", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}, nil},
		{"SELECT count(*) FROM empty GROUP BY +1.5", []string{"count"}, []string{"INT8"}, [][]any{}, nil},
		{"SELECT k,count(*) FROM t GROUP BY (1)", []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {nil, int64(1)}}, nil},
	} {
		t.Run(tc.q, func(t *testing.T) {
			rows, err := db.Query(tc.q, tc.args...)
			queryCoreRows(t, rows, err, tc.names, tc.types, tc.want, true)
			stmt, err := db.Prepare(tc.q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			rows, err = stmt.Query(tc.args...)
			queryCoreRows(t, rows, err, tc.names, tc.types, tc.want, true)
		})
	}
	execSQL(t, db, "CREATE TABLE pairs(a TEXT,b TEXT)")
	execSQL(t, db, "INSERT INTO pairs VALUES('a',NULL),('a',NULL),(NULL,'a'),(NULL,'a'),(NULL,NULL),('ab','c'),('a','bc')")
	rows, err := db.Query("SELECT a,b,count(*) FROM pairs GROUP BY a,b")
	queryCoreRows(t, rows, err, []string{"a", "b", "count"}, []string{"TEXT", "TEXT", "INT8"}, [][]any{{"a", nil, int64(2)}, {nil, "a", int64(2)}, {nil, nil, int64(1)}, {"ab", "c", int64(1)}, {"a", "bc", int64(1)}}, true)
	execSQL(t, db, "CREATE TABLE zero(v INT)")
	execSQL(t, db, "INSERT INTO zero VALUES(NULL),(NULL)")
	execSQL(t, db, "ALTER TABLE zero DROP COLUMN v")
	rows, err = db.Query("SELECT count(*) FROM zero")
	queryCoreRows(t, rows, err, []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}, true)
}

func TestQueryAggregateInsertAndPreparedFreshness(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(k TEXT,v INT)")
	execSQL(t, db, "INSERT INTO t VALUES('a',1),('a',NULL),(NULL,2)")
	execSQL(t, db, "CREATE TABLE out(k TEXT,n BIGINT)")
	stmt, err := db.Prepare("SELECT count(*) FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held, err := stmt.Query()
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "INSERT INTO t VALUES('b',3)")
	queryCoreRows(t, held, nil, []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}, true)
	rows, err := stmt.Query()
	queryCoreRows(t, rows, err, []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}, true)
	for _, prepared := range []bool{false, true} {
		q := "INSERT INTO out SELECT k,count(*) FROM t GROUP BY k HAVING count(*)>1"
		if prepared {
			s, err := db.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			r, err := s.Exec()
			s.Close()
			if err != nil {
				t.Fatal(err)
			}
			n, err := r.RowsAffected()
			if err != nil || n != 1 {
				t.Fatal(n, err)
			}
		} else {
			r, err := db.Exec(q)
			if err != nil {
				t.Fatal(err)
			}
			n, err := r.RowsAffected()
			if err != nil || n != 1 {
				t.Fatal(n, err)
			}
		}
	}
	rows, err = db.Query("SELECT k,n FROM out")
	queryCoreRows(t, rows, err, []string{"k", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {"a", int64(2)}}, true)
}
