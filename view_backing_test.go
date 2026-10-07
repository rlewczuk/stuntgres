package stuntgres_test

import (
	"fmt"
	"stuntgres"
	"testing"
)

func TestViewProjectedBackingSortGroupAndConcurrency(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(8)
	for _, query := range []string{
		"CREATE TABLE t(a INT,b INT)", "INSERT INTO t VALUES(10,3),(20,1),(30,2),(40,3)",
		"CREATE VIEW p(q,r,s) AS SELECT b,a,b FROM t",
		"CREATE VIEW direct AS SELECT s,r,q FROM p",
		"CREATE VIEW computed(s,r,q) AS SELECT s,COALESCE(r,0),q FROM direct",
		"CREATE VIEW nested AS SELECT q,r,s FROM computed",
	} {
		execSQL(t, db, query)
	}
	for _, test := range []struct {
		query        string
		names, types []string
		values       [][]any
	}{
		{"SELECT q,r,s FROM nested ORDER BY r ASC", []string{"q", "r", "s"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(3), int64(10), int64(3)}, {int64(1), int64(20), int64(1)}, {int64(2), int64(30), int64(2)}, {int64(3), int64(40), int64(3)}}},
		{"SELECT q,r,s FROM p ORDER BY r DESC", []string{"q", "r", "s"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(3), int64(40), int64(3)}, {int64(2), int64(30), int64(2)}, {int64(1), int64(20), int64(1)}, {int64(3), int64(10), int64(3)}}},
		{"SELECT q,COUNT(*) FROM p GROUP BY q HAVING COUNT(*)>=2 ORDER BY q", []string{"q", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(3), int64(2)}}},
	} {
		rows, err := db.Query(test.query)
		queryCoreRows(t, rows, err, test.names, test.types, test.values, false)
	}
	rows, err := db.Query("SELECT q,r,s FROM p ORDER BY r")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal(rows.Err())
	}
	var heldQ, heldR, heldS any
	if err := rows.Scan(&heldQ, &heldR, &heldS); err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "INSERT INTO t VALUES(50,4)")
	for rows.Next() {
		var q, r, s any
		if err := rows.Scan(&q, &r, &s); err != nil {
			t.Fatal(err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if heldQ != int64(3) || heldR != int64(10) || heldS != int64(3) {
		t.Fatal("exported row changed", heldQ, heldR, heldS)
	}
	results := make(chan error, 8)
	for range 8 {
		go func() {
			rows, err := db.Query("SELECT q,r,s FROM p ORDER BY r DESC")
			if err != nil {
				results <- err
				return
			}
			defer rows.Close()
			for _, expected := range [][3]int64{{4, 50, 4}, {3, 40, 3}, {2, 30, 2}, {1, 20, 1}, {3, 10, 3}} {
				if !rows.Next() {
					results <- fmt.Errorf("missing occurrence: %v", rows.Err())
					return
				}
				var got [3]int64
				if err := rows.Scan(&got[0], &got[1], &got[2]); err != nil {
					results <- err
					return
				}
				if got != expected {
					results <- fmt.Errorf("projected scratch: got %v want %v", got, expected)
					return
				}
			}
			if rows.Next() {
				results <- fmt.Errorf("extra occurrence")
				return
			}
			results <- rows.Err()
		}()
	}
	for range 8 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
}
