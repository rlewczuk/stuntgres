package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestUpdateReturningCommentSnapshot(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		db := openLocal(t, stuntgres.NewDriver(), t.Name())
		execSQL(t, db, "CREATE TABLE t(id INT)")
		execSQL(t, db, "INSERT INTO t VALUES(1)")
		execSQL(t, db, "COMMENT ON TABLE t IS 'committed table'")
		execSQL(t, db, "COMMENT ON COLUMN t.id IS 'committed column'")
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		query := "UPDATE t SET id=id RETURNING id,col_description('t'::regclass,1) AS column_comment,obj_description('t'::regclass,'pg_class') AS table_comment"
		if prepared {
			stmt, err := tx.Prepare(query)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := stmt.Query()
			queryCoreRows(t, rows, err, []string{"id", "column_comment", "table_comment"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "committed column", "committed table"}}, false)
			stmt.Close()
		} else {
			rows, err := tx.Query(query)
			queryCoreRows(t, rows, err, []string{"id", "column_comment", "table_comment"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "committed column", "committed table"}}, false)
		}
		if err = tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		rows, err := db.Query("SELECT id,col_description('t'::regclass,1) AS column_comment,obj_description('t'::regclass,'pg_class') AS table_comment FROM t")
		queryCoreRows(t, rows, err, []string{"id", "column_comment", "table_comment"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "committed column", "committed table"}}, false)
		db.Close()
	}
}
