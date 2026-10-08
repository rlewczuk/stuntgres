package stuntgres_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"testing"
)

func TestDeleteReturningOutputAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, exec := range []bool{false, true} {
			name := "query"
			if exec {
				name = "exec"
			}
			if prepared {
				name += "/prepared"
			}
			t.Run(name, func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				execSQL(t, db, "CREATE TABLE t(id INT)")
				execSQL(t, db, "INSERT INTO t VALUES(1),(0),(2)")
				query := "DELETE FROM t RETURNING 10/id"
				var stmt *sql.Stmt
				var err error
				if prepared {
					stmt, err = db.Prepare(query)
					if err != nil {
						t.Fatal("Prepare evaluated output", err)
					}
					defer stmt.Close()
				}
				if exec {
					var result sql.Result
					if prepared {
						result, err = stmt.Exec()
					} else {
						result, err = db.Exec(query)
					}
					if result != nil {
						t.Fatal("failure exposed count", result)
					}
				} else {
					var rows *sql.Rows
					if prepared {
						rows, err = stmt.Query()
					} else {
						rows, err = db.Query(query)
					}
					if rows != nil {
						rows.Close()
						t.Fatal("failure exposed cursor")
					}
				}
				var got *stuntgres.Error
				want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"}
				if !errors.As(err, &got) || *got != want {
					t.Fatal(err, got, want)
				}
				rows, err := db.Query("SELECT id FROM t ORDER BY id")
				queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}, {int64(2)}}, false)
			})
		}
	}
}

func TestDeleteReturningCancellation(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT)")
	execSQL(t, db, "INSERT INTO t VALUES(1),(2)")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rows, err := db.QueryContext(ctx, "DELETE FROM t RETURNING *")
	if rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(rows, err)
	}
	rows, err = db.Query("SELECT id FROM t ORDER BY id")
	queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}, false)
}

func TestDeleteReturningSequenceDemand(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT)")
	execSQL(t, db, "INSERT INTO t VALUES(1),(2),(3)")
	execSQL(t, db, "CREATE SEQUENCE s START 101 CACHE 1")
	stmt, err := db.Prepare("DELETE FROM t RETURNING nextval('s')")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	var called bool
	if err = db.QueryRow("SELECT is_called FROM s").Scan(&called); err != nil || called {
		t.Fatal("Prepare drew", called, err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	rows, err := tx.Query("DELETE FROM t RETURNING nextval('s')")
	queryCoreRows(t, rows, err, []string{"nextval"}, []string{"INT8"}, [][]any{{int64(101)}, {int64(102)}, {int64(103)}}, true)
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var next int64
	if err = db.QueryRow("SELECT nextval('s')").Scan(&next); err != nil || next != 104 {
		t.Fatal(next, err)
	}
	rows, err = db.Query("SELECT id FROM t ORDER BY id")
	queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}, false)
	rows, err = db.Query("DELETE FROM t WHERE FALSE RETURNING nextval('s')")
	queryCoreRows(t, rows, err, []string{"nextval"}, []string{"INT8"}, [][]any{}, false)
	if err = db.QueryRow("SELECT currval('s')").Scan(&next); err != nil || next != 104 {
		t.Fatal("no-match drew", next, err)
	}
}
