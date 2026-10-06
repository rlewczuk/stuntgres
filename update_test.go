package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestUpdateCoreRegression(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "update-core-regression")
	execSQL(t, db, "CREATE TABLE t(id INT NOT NULL,a INT,b INT,n SMALLINT NOT NULL)")
	execSQL(t, db, "INSERT INTO t VALUES(1,10,20,1),(2,30,40,1),(2,30,40,1)")
	affected := func(q string, prepared bool, want int64, args ...any) {
		t.Helper()
		if prepared {
			stmt, err := db.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			r, err := stmt.Exec(args...)
			if err != nil {
				t.Fatal(err)
			}
			n, err := r.RowsAffected()
			if err != nil || n != want {
				t.Fatalf("%s: count %d, error %v; want %d", q, n, err, want)
			}
			if _, err := r.LastInsertId(); err == nil {
				t.Fatal("UPDATE invented LastInsertId")
			}
		} else {
			r, err := db.Exec(q, args...)
			if err != nil {
				t.Fatal(err)
			}
			n, err := r.RowsAffected()
			if err != nil || n != want {
				t.Fatalf("%s: count %d, error %v; want %d", q, n, err, want)
			}
		}
	}
	check := func(want [][]any) {
		t.Helper()
		rows, err := db.Query("SELECT id,a,b,n FROM t")
		queryCoreRows(t, rows, err, []string{"id", "a", "b", "n"}, []string{"INT4", "INT4", "INT4", "INT2"}, want, true)
	}
	affected("UPDATE t SET a=b,b=a", false, 3)
	check([][]any{{int64(1), int64(20), int64(10), int64(1)}, {int64(2), int64(40), int64(30), int64(1)}, {int64(2), int64(40), int64(30), int64(1)}})
	affected("UPDATE t SET a=a", false, 3)
	affected("UPDATE t SET a=7 WHERE id=99", false, 0)
	affected("UPDATE t SET a=$1,b=a WHERE id=$2", true, 1, int64(7), int64(1))
	affected("UPDATE t SET a=$1,b=a WHERE id=$2", true, 2, int64(40000), int64(2))
	want := [][]any{{int64(1), int64(7), int64(20), int64(1)}, {int64(2), int64(40000), int64(40), int64(1)}, {int64(2), int64(40000), int64(40), int64(1)}}
	check(want)
	_, err := db.Exec("UPDATE t SET n=a")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22003", Message: "smallint out of range"})
	check(want)
	affected("UPDATE t SET n=a WHERE FALSE", false, 0)
	_, err = db.Exec("UPDATE t SET n=NULL WHERE id=2")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `null value in column "n" of relation "t" violates not-null constraint`, Detail: "Failing row contains (2, 40000, 40, null).", Schema: "public", Table: "t", Column: "n"})
	check(want)
}
