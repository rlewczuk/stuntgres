package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestUpdateReturningOwnPredecessor(t *testing.T) {
	for _, commit := range []bool{false, true} {
		db := openLocal(t, stuntgres.NewDriver(), t.Name())
		execSQL(t, db, "CREATE TABLE t(id INT,a INT)")
		execSQL(t, db, "INSERT INTO t VALUES(1,10)")
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		for _, q := range []string{"INSERT INTO t VALUES(3,30)", "UPDATE t SET a=11 WHERE id=1"} {
			if _, err = tx.Exec(q); err != nil {
				t.Fatal(err)
			}
		}
		for _, tc := range []struct {
			q    string
			want [][]any
		}{
			{"UPDATE t SET a=a+1 WHERE id=3 RETURNING old.a,new.a", [][]any{{int64(30), int64(31)}}},
			{"UPDATE t SET a=a+1 WHERE id=1 RETURNING old.a,new.a", [][]any{{int64(11), int64(12)}}},
		} {
			rows, err := tx.Query(tc.q)
			queryCoreRows(t, rows, err, []string{"a", "a"}, []string{"INT4", "INT4"}, tc.want, false)
		}
		rows, err := tx.Query("DELETE FROM t WHERE id=3 RETURNING a")
		queryCoreRows(t, rows, err, []string{"a"}, []string{"INT4"}, [][]any{{int64(31)}}, false)
		rows, err = db.Query("SELECT id,a FROM t")
		queryCoreRows(t, rows, err, []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}, false)
		if commit {
			err = tx.Commit()
		} else {
			err = tx.Rollback()
		}
		if err != nil {
			t.Fatal(err)
		}
		a := int64(10)
		if commit {
			a = 12
		}
		rows, err = db.Query("SELECT id,a FROM t")
		queryCoreRows(t, rows, err, []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), a}}, false)
	}
}
