package stuntgres_test

import (
	"database/sql"
	"reflect"
	"stuntgres"
	"testing"
)

func intervalGroupCounts(t *testing.T, rows *sql.Rows, want map[int64]int) {
	t.Helper()
	defer rows.Close()
	got := make(map[int64]int)
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		got[n]++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal(got, want)
	}
}

func TestIntervalGroupComparisonClasses(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE iv(v INTERVAL)")
	execSQL(t, db, "INSERT INTO iv VALUES('1 mon'),('30 days'),('720 hours'),('1 day'),('24 hours'),(NULL)")
	rows, err := db.Query("SELECT v FROM iv GROUP BY v")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	classes := make(map[int]bool)
	for rows.Next() {
		var value any
		if err = rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		rank := -1
		switch value {
		case "1 mon", "30 days", "720:00:00":
			rank = 0
		case "1 day", "24:00:00":
			rank = 1
		case nil:
			rank = 2
		}
		if rank < 0 || classes[rank] {
			t.Fatal(value, classes)
		}
		classes[rank] = true
	}
	if rows.Err() != nil || len(classes) != 3 {
		t.Fatal(classes, rows.Err())
	}
}
