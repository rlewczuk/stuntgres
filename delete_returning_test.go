package stuntgres_test

import (
	"database/sql"
	"testing"

	"stuntgres"
)

func TestDeleteReturningRegression(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE t(id INT NOT NULL,v TEXT)")
			execSQL(t, db, "INSERT INTO t VALUES(1,'a'),(2,NULL),(2,NULL),(3,'c')")
			query := "DELETE FROM t WHERE id=$1 RETURNING *"
			var stmt *sql.Stmt
			if prepared {
				var err error
				stmt, err = db.Prepare(query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
			}
			for _, want := range [][][]any{{{int64(2), nil}, {int64(2), nil}}, {}} {
				var rows *sql.Rows
				var err error
				if stmt != nil {
					rows, err = stmt.Query(2)
				} else {
					rows, err = db.Query(query, 2)
				}
				queryCoreRows(t, rows, err, []string{"id", "v"}, []string{"INT4", "TEXT"}, want, true)
			}
			rows, err := db.Query("SELECT id,v FROM t ORDER BY id")
			queryCoreRows(t, rows, err, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(3), "c"}}, false)
			execSQL(t, db, "INSERT INTO t VALUES(2,NULL),(2,NULL)")
			for _, want := range []int64{2, 0} {
				var result sql.Result
				if stmt != nil {
					result, err = stmt.Exec(2)
				} else {
					result, err = db.Exec(query, 2)
				}
				if err != nil {
					t.Fatal(err)
				}
				if got, err := result.RowsAffected(); err != nil || got != want {
					t.Fatalf("RowsAffected=%d,%v want %d", got, err, want)
				}
				if _, err := result.LastInsertId(); err == nil {
					t.Fatal("DELETE invented LastInsertId")
				}
			}
			rows, err = db.Query("SELECT id,v FROM t ORDER BY id")
			queryCoreRows(t, rows, err, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(3), "c"}}, false)
			rows, err = db.Query("DELETE FROM t WHERE FALSE")
			queryCoreRows(t, rows, err, []string{}, []string{}, [][]any{}, false)
		})
	}
}
