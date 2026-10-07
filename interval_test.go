package stuntgres_test

import (
	"context"
	"database/sql"
	"reflect"
	"stuntgres"
	"testing"
)

func intervalRows(t *testing.T, rows *sql.Rows, want [][]any) {
	t.Helper()
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var got [][]any
	for rows.Next() {
		row, dest := make([]any, len(names)), make([]any, len(names))
		for i := range row {
			dest[i] = &row[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %#v; want %#v", got, want)
	}
}

func TestIntervalComponentsAndComparison(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE iv(id INTEGER,v INTERVAL)")
	result, err := db.Exec("INSERT INTO iv VALUES(1,'1 mon'),(2,'30 days'),(3,'720 hours'),(4,'1 day'),(5,'24 hours'),(6,NULL),(7,'P2Y10M15DT10H30M20S')")
	if err != nil {
		t.Fatal(err)
	}
	if n, e := result.RowsAffected(); e != nil || n != 7 {
		t.Fatal(n, e)
	}
	rows, err := db.Query("SELECT id,v FROM iv ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if meta[1].DatabaseTypeName() != "INTERVAL" || meta[1].ScanType() != reflect.TypeFor[string]() {
		t.Fatalf("interval metadata = %#v", meta[1])
	}
	if _, _, ok := meta[1].DecimalSize(); ok {
		t.Fatal("interval exposes numeric decimal size")
	}
	intervalRows(t, rows, [][]any{{int64(1), "1 mon"}, {int64(2), "30 days"}, {int64(3), "720:00:00"}, {int64(4), "1 day"}, {int64(5), "24:00:00"}, {int64(6), nil}, {int64(7), "2 years 10 mons 15 days 10:30:20"}})
	rows, err = db.Query("SELECT id FROM iv WHERE v=INTERVAL '1 month' ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	intervalRows(t, rows, [][]any{{int64(1)}, {int64(2)}, {int64(3)}})
	rows, err = db.Query("SELECT COUNT(*) FROM iv GROUP BY v")
	if err != nil {
		t.Fatal(err)
	}
	intervalGroupCounts(t, rows, map[int64]int{1: 2, 2: 1, 3: 1})
}

func TestIntervalLiteralParameterTypmod(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE ivtm(id INTEGER,y INTERVAL YEAR,m INTERVAL MINUTE TO SECOND(2))")
	execSQL(t, db, "INSERT INTO ivtm VALUES(1,'1','1:02.345')")
	stmt, err := db.Prepare("INSERT INTO ivtm VALUES($1,$2,$3)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	result, err := stmt.Exec(2, "1", []byte("1:02.345"))
	if err != nil {
		t.Fatal(err)
	}
	if n, e := result.RowsAffected(); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	result, err = stmt.Exec(3, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n, e := result.RowsAffected(); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	rows, err := db.Query("SELECT id,y,m FROM ivtm ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	intervalRows(t, rows, [][]any{{int64(1), "1 year", "00:01:02.35"}, {int64(2), "00:00:00", "00:01:02.35"}, {int64(3), nil, nil}})
}

func TestIntervalPreparedStyleCapture(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	a, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, err = a.ExecContext(context.Background(), "CREATE TABLE ivstyle(id INTEGER,v INTERVAL)"); err != nil {
		t.Fatal(err)
	}
	literal, err := a.PrepareContext(context.Background(), "INSERT INTO ivstyle VALUES(1,INTERVAL '-1 2:03:04')")
	if err != nil {
		t.Fatal(err)
	}
	defer literal.Close()
	param, err := a.PrepareContext(context.Background(), "INSERT INTO ivstyle VALUES(2,$1)")
	if err != nil {
		t.Fatal(err)
	}
	defer param.Close()
	if _, err = a.ExecContext(context.Background(), "SET IntervalStyle=sql_standard"); err != nil {
		t.Fatal(err)
	}
	if _, err = literal.ExecContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = param.ExecContext(context.Background(), "-1 2:03:04"); err != nil {
		t.Fatal(err)
	}
	rows, err := a.QueryContext(context.Background(), "SELECT id,v FROM ivstyle ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	intervalRows(t, rows, [][]any{{int64(1), "+0-0 -1 +2:03:04"}, {int64(2), "-1 2:03:04"}})
	rows, err = b.QueryContext(context.Background(), "SELECT id,v FROM ivstyle ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	intervalRows(t, rows, [][]any{{int64(1), "-1 days +02:03:04"}, {int64(2), "-1 days -02:03:04"}})
}
