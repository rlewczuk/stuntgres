package stuntgres_test

import (
	"bytes"
	"context"
	"stuntgres"
	"testing"
)

func TestCountExpressionCallers(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, q := range []string{"CREATE TABLE t(k TEXT,v INT)", "INSERT INTO t VALUES('a',1),('a',NULL),(NULL,3)", "CREATE TABLE empty(k TEXT,v INT)", "CREATE TABLE out(k TEXT,n BIGINT)", "CREATE TABLE narrow(n SMALLINT)", "CREATE SEQUENCE s", "CREATE VIEW counted(n,z,r) AS SELECT count(v),count(NULLIF(v,1)),count(CAST('s' AS regclass)) FROM t"} {
		execSQL(t, db, q)
	}
	for _, tc := range []struct {
		q    string
		want int64
	}{
		{`SELECT pg_catalog.count(t.v) FROM t`, 2},
		{`SELECT "count"(v) FROM t`, 2},
		{`SELECT count(l.v) FROM t AS l JOIN t AS r ON l.k=r.k`, 2},
		{`SELECT count(v) FROM t LIMIT 1 OFFSET 0`, 2},
	} {
		for _, prepared := range []bool{false, true} {
			if prepared {
				stmt, err := db.Prepare(tc.q)
				if err != nil {
					t.Fatal(err)
				}
				rows, err := stmt.Query()
				queryCoreRows(t, rows, err, []string{"count"}, []string{"INT8"}, [][]any{{tc.want}}, true)
				stmt.Close()
			} else {
				rows, err := db.Query(tc.q)
				queryCoreRows(t, rows, err, []string{"count"}, []string{"INT8"}, [][]any{{tc.want}}, true)
			}
		}
	}
	for _, tc := range []struct {
		q    string
		want int64
	}{
		{"INSERT INTO out SELECT k,count(v) FROM t GROUP BY k", 2},
		{"INSERT INTO out(n) SELECT count(v) FROM empty", 1},
		{"INSERT INTO out(n) SELECT count(v) FROM empty GROUP BY k", 0},
		{"INSERT INTO narrow SELECT count(v) FROM t", 1},
	} {
		r, err := db.Exec(tc.q)
		if err != nil {
			t.Fatal(err)
		}
		n, err := r.RowsAffected()
		if err != nil || n != tc.want {
			t.Fatal(n, err)
		}
	}
	rows, err := db.Query("SELECT k,n FROM out")
	queryCoreRows(t, rows, err, []string{"k", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(1)}, {nil, int64(1)}, {nil, int64(0)}}, true)
	_, err = db.Exec("INSERT INTO narrow SELECT count(1/(v-3)) FROM t")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22012", Message: "division by zero"})
	rows, err = db.Query("SELECT n FROM narrow")
	queryCoreRows(t, rows, err, []string{"n"}, []string{"INT2"}, [][]any{{int64(2)}}, true)
	rows, err = db.Query("SELECT count(nextval('s')),count(nextval('s')) FROM t")
	queryCoreRows(t, rows, err, []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(3), int64(3)}}, true)
	rows, err = db.Query("SELECT currval('s')")
	queryCoreRows(t, rows, err, []string{"currval"}, []string{"INT8"}, [][]any{{int64(6)}}, true)
	check := func() {
		rows, err := db.Query("SELECT n,z,r FROM counted")
		queryCoreRows(t, rows, err, []string{"n", "z", "r"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(2), int64(1), int64(3)}}, true)
	}
	check()
	execSQL(t, db, "ALTER TABLE t RENAME COLUMN v TO renamed")
	execSQL(t, db, "ALTER TABLE t RENAME TO renamed_table")
	check()
	var dump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check()
	stmt, err := db.Prepare("SELECT count(renamed) FROM renamed_table")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held, err := stmt.Query()
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "INSERT INTO renamed_table VALUES('b',NULL)")
	rows, err = stmt.Query()
	queryCoreRows(t, rows, err, []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}, true)
	execSQL(t, db, "INSERT INTO renamed_table VALUES('b',4)")
	rows, err = stmt.Query()
	queryCoreRows(t, rows, err, []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}, true)
	if err := stuntgres.Reset(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	queryCoreRows(t, held, nil, []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}, true)
	if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check()
}
