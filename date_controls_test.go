package stuntgres_test

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
	"unicode/utf8"
)

func TestDateControlsRoundTripSnapshotsAndCancellation(t *testing.T) {
	ctx := context.Background()
	driver := stuntgres.NewDriver()
	db := openLocal(t, driver, t.Name())
	dest := openLocal(t, driver, t.Name()+"/restore")
	execSQL(t, db, `CREATE TABLE d(v "date")`)
	execSQL(t, db, "INSERT INTO d VALUES('-infinity'),('4714-11-24 BC'),('0001-02-29 BC'),('epoch'),('epoch'),('5874897-12-31'),('infinity'),(NULL)")
	want := []any{"-infinity", "4714-11-24 BC", "0001-02-29 BC", "1970-01-01", "1970-01-01", "5874897-12-31", "infinity", nil}
	old, err := db.Query("SELECT v FROM d ORDER BY v")
	if err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"DATE", "'4714-11-24 BC'", "'0001-02-29 BC'", "'1970-01-01'", "'5874897-12-31'", "'-infinity'", "'infinity'", "NULL"} {
		if !strings.Contains(dump.String(), needle) {
			t.Fatal("missing dump witness", needle, dump.String())
		}
	}
	if err := stuntgres.Restore(ctx, dest, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	rows, err := dest.Query("SELECT v FROM d ORDER BY v")
	if err != nil {
		t.Fatal(err)
	}
	dateCheckRows(t, rows, want)
	var again bytes.Buffer
	if err := stuntgres.Dump(ctx, dest, &again); err != nil || again.String() != dump.String() {
		t.Fatal("redump differs", err, dump.String(), again.String())
	}
	if err := stuntgres.Restore(ctx, dest, strings.NewReader("CREATE TABLE broken(v DATE); INSERT INTO broken VALUES('epoch'),('bad');")); err == nil {
		t.Fatal("bad staged Restore succeeded")
	} else {
		timeDiagnostic(t, err, stuntgres.Error{Code: "22007", Message: `invalid input syntax for type date: "bad"`, Position: strconv.Itoa(utf8.RuneCountInString("CREATE TABLE broken(v DATE); INSERT INTO broken VALUES('epoch'),(") + 1)})
	}
	rows, err = dest.Query("SELECT v FROM d ORDER BY v")
	if err != nil {
		t.Fatal(err)
	}
	dateCheckRows(t, rows, want)
	execSQL(t, db, "INSERT INTO d VALUES('2000-01-01')")
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	dateCheckRows(t, old, want)
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	held, err := db.Query("SELECT v FROM d ORDER BY v")
	if err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE replacement(v DATE); INSERT INTO replacement VALUES('2000-01-01');")); err != nil {
		t.Fatal(err)
	}
	dateCheckRows(t, held, want)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := stuntgres.Restore(canceled, db, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(canceled, "INSERT INTO replacement VALUES('epoch')"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	rows, err = db.Query("SELECT v FROM replacement")
	if err != nil {
		t.Fatal(err)
	}
	dateCheckRows(t, rows, []any{"2000-01-01"})
	iteration, stop := context.WithCancel(ctx)
	rows, err = db.QueryContext(iteration, "SELECT v FROM replacement")
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
}

func TestDateNotNullAndTransactionAtomicity(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE nn(v DATE NOT NULL)")
	_, err := db.Exec("INSERT INTO nn VALUES('epoch'),(NULL)")
	timeDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `null value in column "v" of relation "nn" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "nn", Column: "v"})
	rows, err := db.Query("SELECT v FROM nn")
	if err != nil {
		t.Fatal(err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if n, known := meta[0].Nullable(); !known || n {
		t.Fatal("NOT NULL metadata", meta[0])
	}
	dateCheckRows(t, rows, nil)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec("INSERT INTO nn VALUES('bad')")
	timeDiagnostic(t, err, stuntgres.Error{Code: "22007", Message: `invalid input syntax for type date: "bad"`, Position: "23"})
	_, err = tx.Exec("INSERT INTO nn VALUES('epoch')")
	timeDiagnostic(t, err, stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}
