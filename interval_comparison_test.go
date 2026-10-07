package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestIntervalComparisonGroupingAndConditionals(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE iv(id INTEGER,v INTERVAL)")
	execSQL(t, db, "INSERT INTO iv VALUES(1,'-infinity'),(2,'-1 day'),(3,'0'),(4,'1 day'),(5,'24 hours'),(6,'1 mon'),(7,'30 days'),(8,'infinity'),(9,NULL)")
	for _, tc := range []struct {
		op  string
		ids []int64
	}{{"=", []int64{4, 5}}, {"<>", []int64{1, 2, 3, 6, 7, 8}}, {"<", []int64{1, 2, 3}}, {"<=", []int64{1, 2, 3, 4, 5}}, {">", []int64{6, 7, 8}}, {">=", []int64{4, 5, 6, 7, 8}}} {
		rows, e := db.Query("SELECT id FROM iv WHERE v" + tc.op + "INTERVAL '1 day' ORDER BY id")
		if e != nil {
			t.Fatal(e)
		}
		want := make([][]any, len(tc.ids))
		for i, id := range tc.ids {
			want[i] = []any{id}
		}
		intervalRows(t, rows, want)
	}
	rows, e := db.Query("SELECT v FROM iv ORDER BY v")
	if e != nil {
		t.Fatal(e)
	}
	// Equal-span order is deliberately not asserted: compare only class ranks.
	var previous int
	var count int
	for rows.Next() {
		var v any
		if e = rows.Scan(&v); e != nil {
			t.Fatal(e)
		}
		rank := map[any]int{"-infinity": 0, "-1 days": 1, "00:00:00": 2, "1 day": 3, "24:00:00": 3, "1 mon": 4, "30 days": 4, "infinity": 5, nil: 6}[v]
		if rank < previous {
			t.Fatal(v, rank, previous)
		}
		previous = rank
		count++
	}
	if rows.Err() != nil || count != 9 {
		t.Fatal(count, rows.Err())
	}
	rows.Close()
	rows, e = db.Query("SELECT COALESCE(NULL,v),GREATEST(v,INTERVAL '1 day'),LEAST(v,INTERVAL '1 day') FROM iv WHERE id=6")
	if e != nil {
		t.Fatal(e)
	}
	intervalRows(t, rows, [][]any{{"1 mon", "1 mon", "1 day"}})
	rows, e = db.Query("SELECT COUNT(*) FROM iv GROUP BY v")
	if e != nil {
		t.Fatal(e)
	}
	intervalGroupCounts(t, rows, map[int64]int{1: 5, 2: 2})
	execSQL(t, db, "CREATE TABLE large(a INTERVAL,b INTERVAL)")
	execSQL(t, db, "INSERT INTO large VALUES('2147483647 months -2147483648 days','2147483646 months -2147483618 days')")
	rows, e = db.Query("SELECT a=b,a>b,a<b FROM large")
	if e != nil {
		t.Fatal(e)
	}
	intervalRows(t, rows, [][]any{{true, false, false}})
}

func TestIntervalRepeatedParametersAndInsertSelect(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE src(v INTERVAL,d INTERVAL DAY)")
	stmt, e := db.Prepare("INSERT INTO src VALUES($1,$1)")
	if e != nil {
		t.Fatal(e)
	}
	defer stmt.Close()
	result, e := stmt.Exec("13 months 2 days 3:04:05.6789")
	if e != nil {
		t.Fatal(e)
	}
	if n, e := result.RowsAffected(); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	execSQL(t, db, "CREATE TABLE dst(v INTERVAL YEAR)")
	execSQL(t, db, "INSERT INTO dst SELECT v FROM src")
	rows, e := db.Query("SELECT v,d FROM src")
	if e != nil {
		t.Fatal(e)
	}
	intervalRows(t, rows, [][]any{{"1 year 1 mon 2 days 03:04:05.6789", "1 year 1 mon 2 days"}})
	rows, e = db.Query("SELECT v FROM dst")
	if e != nil {
		t.Fatal(e)
	}
	intervalRows(t, rows, [][]any{{"1 year"}})
}
