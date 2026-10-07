package stuntgres_test

import (
	"context"
	"errors"
	"stuntgres"
	"testing"
)

func TestIntervalSessionReuseLocalAndReplacement(t *testing.T) {
	ctx := context.Background()
	drv := stuntgres.NewDriver()
	db := openLocal(t, drv, t.Name())
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	peer := openLocal(t, drv, t.Name())
	execSQL(t, db, "CREATE TABLE iv(v INTERVAL)")
	execSQL(t, db, "INSERT INTO iv VALUES('1 day')")
	execSQL(t, db, "SET IntervalStyle=iso_8601")
	var value string
	if err := peer.QueryRow("SELECT v FROM iv").Scan(&value); err != nil || value != "1 day" {
		t.Fatal(value, err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"SET LOCAL IntervalStyle=postgres_verbose", "SAVEPOINT mark", "SET LOCAL IntervalStyle=sql_standard", "ROLLBACK TO mark"} {
		if _, err = tx.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	if err = tx.QueryRow("SELECT v FROM iv").Scan(&value); err != nil || value != "@ 1 day" {
		t.Fatal(value, err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT v FROM iv").Scan(&value); err != nil || value != "P1D" {
		t.Fatal("LOCAL leaked", value, err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SHOW IntervalStyle").Scan(&value); err != nil || value != "iso_8601" {
		t.Fatal("reuse reset", value, err)
	}
	db.SetMaxIdleConns(0)
	db.SetMaxIdleConns(1)
	if err = db.QueryRow("SHOW IntervalStyle").Scan(&value); err != nil || value != "postgres" {
		t.Fatal("replacement default", value, err)
	}
	if err = db.QueryRow("SELECT v FROM iv").Scan(&value); err != nil || value != "1 day" {
		t.Fatal("replacement lost data", value, err)
	}
}
func TestIntervalCancellationBoundaries(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE iv(v INTERVAL)")
	execSQL(t, db, "INSERT INTO iv VALUES('1 day')")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := db.PrepareContext(canceled, "INSERT INTO iv VALUES('1 mon')"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := db.QueryContext(canceled, "SELECT v FROM iv"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(canceled, db); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	queryCtx, stop := context.WithCancel(ctx)
	rows, err := db.QueryContext(queryCtx, "SELECT v FROM iv")
	if err != nil {
		t.Fatal(err)
	}
	stop()
	for rows.Next() {
	}
	if !errors.Is(rows.Err(), context.Canceled) {
		t.Fatal(rows.Err())
	}
	rows.Close()
	var value string
	if err = db.QueryRow("SELECT v FROM iv").Scan(&value); err != nil || value != "1 day" {
		t.Fatal(value, err)
	}
	stmt, err := db.Prepare("INSERT INTO iv VALUES($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	buffer := []byte("1 mon")
	if _, err = stmt.Exec(buffer); err != nil {
		t.Fatal(err)
	}
	buffer[0] = '9'
	rows, err = db.Query("SELECT v FROM iv ORDER BY v")
	if err != nil {
		t.Fatal(err)
	}
	intervalRows(t, rows, [][]any{{"1 day"}, {"1 mon"}})
}
