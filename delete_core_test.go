package stuntgres_test

import (
	"database/sql"
	"testing"

	"stuntgres"
)

func TestDeleteCoreRegression(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		prepared    bool
	}{
		{"direct", "DELETE FROM t WHERE v=10", false},
		{"as_alias", "DELETE FROM t AS d WHERE d.v=10", false},
		{"bare_alias", "DELETE FROM t d WHERE d.v=10", false},
		{"prepared", "DELETE FROM t AS d WHERE d.v=$1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE t(id INT NOT NULL,v INT,f BOOL)")
			execSQL(t, db, "INSERT INTO t VALUES(1,10,TRUE),(2,NULL,NULL),(3,20,FALSE),(4,10,TRUE),(4,10,TRUE)")
			var stmt *sql.Stmt
			if tc.prepared {
				var err error
				stmt, err = db.Prepare(tc.query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
			}
			deleteRows := func(want int64, args ...any) {
				t.Helper()
				var result sql.Result
				var err error
				if stmt != nil {
					result, err = stmt.Exec(args...)
				} else {
					result, err = db.Exec(tc.query, args...)
				}
				if err != nil {
					t.Fatalf("DELETE must execute: %v", err)
				}
				if got, err := result.RowsAffected(); err != nil || got != want {
					t.Fatalf("RowsAffected got %d, %v; want %d", got, err, want)
				}
				if _, err := result.LastInsertId(); err == nil {
					t.Fatal("DELETE must not expose LastInsertId")
				}
			}
			if tc.prepared {
				deleteRows(3, 10)
				deleteRows(0, 10)
				deleteRows(0, nil)
			} else {
				deleteRows(3)
				deleteRows(0)
			}
			rows, err := db.Query("SELECT id,v,f FROM t ORDER BY id")
			queryCoreRows(t, rows, err, []string{"id", "v", "f"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(2), nil, nil}, {int64(3), int64(20), false}}, false)
			for _, query := range []string{"DELETE FROM t WHERE FALSE", "DELETE FROM t WHERE NULL"} {
				if got, err := execSQL(t, db, query).RowsAffected(); err != nil || got != 0 {
					t.Fatalf("%s: RowsAffected %d, %v; want 0", query, got, err)
				}
			}
			if got, err := execSQL(t, db, "DELETE FROM t").RowsAffected(); err != nil || got != 2 {
				t.Fatalf("delete all: RowsAffected %d, %v; want 2", got, err)
			}
			rows, err = db.Query("SELECT id,v,f FROM t ORDER BY id")
			queryCoreRows(t, rows, err, []string{"id", "v", "f"}, []string{"INT4", "INT4", "BOOL"}, [][]any{}, false)
			if got, err := execSQL(t, db, "DELETE FROM t").RowsAffected(); err != nil || got != 0 {
				t.Fatalf("delete empty: RowsAffected %d, %v; want 0", got, err)
			}
		})
	}
}
