package stuntgres_test

import (
	"bytes"
	"context"
	"stuntgres"
	"testing"
)

func TestBooleanAggregateCallers(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, q := range []string{"CREATE TABLE t(k TEXT,b BOOL)", "INSERT INTO t VALUES('T',TRUE),('F',FALSE),('M',TRUE),('M',FALSE),('N',NULL),(NULL,TRUE)", "CREATE TABLE empty(k TEXT,b BOOL)", "CREATE TABLE d(v INT)", "INSERT INTO d VALUES(1),(0)", "CREATE TABLE out(b BOOL)", "INSERT INTO out VALUES(TRUE)", "CREATE VIEW aggregated(a,o,e,u) AS SELECT bool_and(b),bool_or(b),every(b),every('true') FROM t"} {
		execSQL(t, db, q)
	}
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			q    string
			want int64
		}{{"INSERT INTO out SELECT bool_and(b) FROM t", 1}, {"INSERT INTO out SELECT bool_and(b) FROM t GROUP BY k", 5}, {"INSERT INTO out SELECT every(b) FROM empty", 1}, {"INSERT INTO out SELECT bool_or(b) FROM empty GROUP BY k", 0}} {
			var n int64
			if prepared {
				stmt, err := db.Prepare(tc.q)
				if err != nil {
					t.Fatal(err)
				}
				r, err := stmt.Exec()
				stmt.Close()
				if err != nil {
					t.Fatal(err)
				}
				n, err = r.RowsAffected()
				if err != nil {
					t.Fatal(err)
				}
			} else {
				r, err := db.Exec(tc.q)
				if err != nil {
					t.Fatal(err)
				}
				n, err = r.RowsAffected()
				if err != nil {
					t.Fatal(err)
				}
			}
			if n != tc.want {
				t.Fatal(tc.q, n)
			}
		}
		for _, q := range []string{"SELECT bool_and(10/v=0) FROM d", "SELECT bool_or(10/v>0) FROM d", "INSERT INTO out SELECT bool_or(10/v>0) FROM d"} {
			var err error
			if prepared {
				stmt, e := db.Prepare(q)
				if e != nil {
					t.Fatal(e)
				}
				_, err = stmt.Exec()
				stmt.Close()
			} else {
				_, err = db.Exec(q)
			}
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22012", Message: "division by zero"})
		}
	}
	rows, err := db.Query("SELECT b FROM out")
	queryCoreRows(t, rows, err, []string{"b"}, []string{"BOOL"}, [][]any{{true}, {false}, {true}, {false}, {false}, {nil}, {true}, {nil}, {false}, {true}, {false}, {false}, {nil}, {true}, {nil}}, true)
	stmt, err := db.Prepare("SELECT bool_and(b),bool_or(b) FROM t WHERE k='T'")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held, err := stmt.Query()
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "INSERT INTO t VALUES('T',FALSE)")
	rows, err = stmt.Query()
	queryCoreRows(t, rows, err, []string{"bool_and", "bool_or"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}, true)
	check := func() {
		rows, err := db.Query("SELECT a,o,e,u FROM aggregated")
		queryCoreRows(t, rows, err, []string{"a", "o", "e", "u"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, false, true}}, true)
	}
	check()
	var dump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	queryCoreRows(t, held, nil, []string{"bool_and", "bool_or"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, true)
	check()
	execSQL(t, db, "CREATE SEQUENCE s")
	rows, err = db.Query("SELECT bool_and(nextval('s')>0),every(nextval('s')>0),bool_or(nextval('s')>0) FROM t")
	queryCoreRows(t, rows, err, []string{"bool_and", "every", "bool_or"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}, true)
	rows, err = db.Query("SELECT currval('s')")
	queryCoreRows(t, rows, err, []string{"currval"}, []string{"INT8"}, [][]any{{int64(21)}}, true)
}
