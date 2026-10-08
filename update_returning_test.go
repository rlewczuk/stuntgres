package stuntgres_test

import (
	"database/sql"
	"testing"

	"stuntgres"
)

func TestUpdateReturningVersions(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE t(id INT NOT NULL,a INT,b INT,v TEXT)")
			execSQL(t, db, "INSERT INTO t VALUES(1,10,20,'one'),(2,30,40,NULL),(2,30,40,NULL)")
			q := "UPDATE t SET a=b,b=a WHERE id=$1 RETURNING id,a,b,old.a,new.a,new.a-old.a AS delta,v"
			var stmt *sql.Stmt
			if prepared {
				var err error
				stmt, err = db.Prepare(q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
			}
			for _, id := range []int{2, 99} {
				var rows *sql.Rows
				var err error
				if stmt != nil {
					rows, err = stmt.Query(id)
				} else {
					rows, err = db.Query(q, id)
				}
				want := [][]any{}
				if id == 2 {
					want = [][]any{{int64(2), int64(40), int64(30), int64(30), int64(40), int64(10), nil}, {int64(2), int64(40), int64(30), int64(30), int64(40), int64(10), nil}}
				}
				queryCoreRows(t, rows, err, []string{"id", "a", "b", "a", "a", "delta", "v"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "TEXT"}, want, true)
			}
			rows, err := db.Query("UPDATE t SET a=a+1 WHERE id=1 RETURNING WITH (OLD AS o,NEW AS n) o.a,n.a,t.a")
			queryCoreRows(t, rows, err, []string{"a", "a", "a"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(10), int64(11), int64(11)}}, false)
			result, err := db.Exec("UPDATE t SET a=a RETURNING old.a,new.a")
			if err != nil {
				t.Fatal(err)
			}
			if count, err := result.RowsAffected(); err != nil || count != 3 {
				t.Fatalf("count=%d err=%v, want 3", count, err)
			}
			rows, err = db.Query("SELECT id,a,b,v FROM t")
			queryCoreRows(t, rows, err, []string{"id", "a", "b", "v"}, []string{"INT4", "INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(11), int64(20), "one"}, {int64(2), int64(40), int64(30), nil}, {int64(2), int64(40), int64(30), nil}}, true)
		})
	}
}
