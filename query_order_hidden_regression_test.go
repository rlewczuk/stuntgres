package stuntgres_test

import (
	"database/sql"
	"stuntgres"
	"testing"
)

func TestQueryOrderRepeatedHiddenVolatileRegression(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE v(id INT)")
	execSQL(t, db, "INSERT INTO v VALUES(1),(2),(3)")
	for _, prepared := range []bool{false, true} {
		for _, where := range []string{"", "WHERE id>1"} {
			execSQL(t, db, "CREATE SEQUENCE s START WITH 1")
			q := "SELECT id FROM v " + where + " ORDER BY nextval('s'),nextval('s') DESC"
			want := [][]any{{int64(1)}, {int64(2)}, {int64(3)}}
			nextWant := int64(4)
			if where != "" {
				want = [][]any{{int64(2)}, {int64(3)}}
				nextWant = 3
			}
			var held *sql.Stmt
			if prepared {
				stmt, err := db.Prepare(q)
				if err != nil {
					t.Fatal(err)
				}
				rows, err := stmt.Query()
				queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, want, true)
				held = stmt
			} else {
				rows, err := db.Query(q)
				queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, want, true)
			}
			var next int64
			if err := db.QueryRow("SELECT nextval('s')").Scan(&next); err != nil || next != nextWant {
				t.Fatalf("prepared=%v %s: subsequent nextval=%d, want%d; err=%v", prepared, where, next, nextWant, err)
			}
			if held != nil {
				rows, err := held.Query()
				queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, want, true)
				if err := db.QueryRow("SELECT nextval('s')").Scan(&next); err != nil || next != 2*nextWant {
					t.Fatal("prepared second execution", next, 2*nextWant, err)
				}
				held.Close()
			}
			execSQL(t, db, "DROP SEQUENCE s")
		}
	}
}
