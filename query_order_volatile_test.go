package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestQueryOrderVolatileOccurrences(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE v(id INT)")
	execSQL(t, db, "INSERT INTO v VALUES(1),(2),(3)")
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			q    string
			rows [][]any
			next int64
		}{
			{"SELECT nextval('s') AS n,nextval('s') AS m FROM v ORDER BY n DESC", [][]any{{int64(3), int64(3)}, {int64(2), int64(2)}, {int64(1), int64(1)}}, 4},
			{"SELECT nextval('s') AS n,nextval('s') AS m FROM v ORDER BY id DESC", [][]any{{int64(1), int64(2)}, {int64(3), int64(4)}, {int64(5), int64(6)}}, 7},
		} {
			t.Run(tc.q+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				execSQL(t, db, "CREATE SEQUENCE s START WITH 1")
				if prepared {
					stmt, err := db.Prepare(tc.q)
					if err != nil {
						t.Fatal(err)
					}
					rows, err := stmt.Query()
					queryCoreRows(t, rows, err, []string{"n", "m"}, []string{"INT8", "INT8"}, tc.rows, false)
					stmt.Close()
				} else {
					rows, err := db.Query(tc.q)
					queryCoreRows(t, rows, err, []string{"n", "m"}, []string{"INT8", "INT8"}, tc.rows, false)
				}
				var next int64
				if err := db.QueryRow("SELECT nextval('s')").Scan(&next); err != nil || next != tc.next {
					t.Fatal(next, tc.next, err)
				}
				execSQL(t, db, "DROP SEQUENCE s")
			})
		}
	}
}
