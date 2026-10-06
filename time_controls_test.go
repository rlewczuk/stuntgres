package stuntgres_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func TestTimeControlsRoundTripAndRetention(t *testing.T) {
	ctx := context.Background()
	driver := stuntgres.NewDriver()
	db := openLocal(t, driver, t.Name())
	dest := openLocal(t, driver, t.Name()+"/restore")
	execSQL(t, db, "CREATE TABLE clocks(id INTEGER,v TIME(0),fine TIME(6),plain TIME)")
	execSQL(t, db, "INSERT INTO clocks VALUES(1,'00:00','00:00','allballs'),(2,'24:00','24:00','24:00'),(3,'04:05 PM','04:05:06.12',NULL)")
	execSQL(t, db, "CREATE TABLE copied(LIKE clocks)")
	want := [][]any{{int64(1), "00:00:00", "00:00:00", "00:00:00"}, {int64(2), "24:00:00", "24:00:00", "24:00:00"}, {int64(3), "16:05:00", "04:05:06.12", nil}}
	names, types := []string{"id", "v", "fine", "plain"}, []string{"INT4", "TIME", "TIME", "TIME"}
	old, err := db.Query("SELECT id,v,fine,plain FROM clocks ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"TIME(0)", "TIME(6)", "TIME", "'24:00:00'", "'04:05:06.12'", "NULL"} {
		if !strings.Contains(dump.String(), needle) {
			t.Fatal("missing dump witness", needle, dump.String())
		}
	}
	if err = stuntgres.Restore(ctx, dest, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	timeQuery(t, dest, "SELECT id,v,fine,plain FROM clocks ORDER BY id", names, types, want)
	var again bytes.Buffer
	if err = stuntgres.Dump(ctx, dest, &again); err != nil || again.String() != dump.String() {
		t.Fatal("redump differs", err, dump.String(), again.String())
	}
	// A later staged codec failure must not replace any target object or row.
	failed := dump.String() + "CREATE TABLE broken(v TIME); INSERT INTO broken VALUES('00:00'),('bad');"
	if err = stuntgres.Restore(ctx, dest, strings.NewReader(failed)); err == nil {
		t.Fatal("malformed restore succeeded")
	}
	timeQuery(t, dest, "SELECT id,v,fine,plain FROM clocks ORDER BY id", names, types, want)
	if err = stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE replacement(v TIME); INSERT INTO replacement VALUES('12:00');")); err != nil {
		t.Fatal(err)
	}
	timeCheckRows(t, old, names, types, want)
	held, err := dest.Query("SELECT id,v,fine,plain FROM clocks ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Reset(ctx, dest); err != nil {
		t.Fatal(err)
	}
	timeCheckRows(t, held, names, types, want)
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if err = stuntgres.Restore(ctx, db, strings.NewReader(dump.String())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, "INSERT INTO replacement VALUES('24:00')"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	timeQuery(t, db, "SELECT v FROM replacement", []string{"v"}, []string{"TIME"}, [][]any{{"12:00:00"}})
	iteration, cancelIteration := context.WithCancel(context.Background())
	rows, err := db.QueryContext(iteration, "SELECT v FROM replacement")
	if err != nil {
		t.Fatal(err)
	}
	cancelIteration()
	for rows.Next() {
	}
	if !errors.Is(rows.Err(), context.Canceled) {
		t.Fatal(rows.Err())
	}
	rows.Close()
}

func TestTimePreparedClocksAndCancellation(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE anchor(v TIME)")
	execSQL(t, db, "INSERT INTO anchor VALUES('00:00')")
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	before := time.Now().UTC()
	held, err := conn.PrepareContext(ctx, "SELECT TIME 'now',TIMESTAMP 'now' FROM anchor")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	after := time.Now().UTC()
	var frozen string
	var stamp time.Time
	if err = held.QueryRowContext(ctx).Scan(&frozen, &stamp); err != nil {
		t.Fatal(err)
	}
	if stamp.Before(before.Add(-time.Microsecond)) || stamp.After(after.Add(time.Microsecond)) || frozen != stamp.Format("15:04:05.999999") {
		t.Fatal(frozen, stamp, before, after)
	}
	contextual, err := conn.PrepareContext(ctx, "INSERT INTO anchor VALUES('now')")
	if err != nil {
		t.Fatal(err)
	}
	defer contextual.Close()
	for range 2 {
		if _, err = contextual.ExecContext(ctx); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := conn.QueryContext(ctx, "SELECT v FROM anchor ORDER BY v")
	if err != nil {
		t.Fatal(err)
	}
	var clocks []string
	for rows.Next() {
		var s string
		if err = rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		clocks = append(clocks, s)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if len(clocks) != 3 || clocks[1] != clocks[2] {
		t.Fatal("contextual now not frozen", clocks)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for range 2 {
		if _, err = tx.ExecContext(ctx, "INSERT INTO anchor VALUES($1)", "now"); err != nil {
			t.Fatal(err)
		}
	}
	var current string
	var txStamp time.Time
	if err = tx.QueryRowContext(ctx, "SELECT TIME 'now',TIMESTAMP 'now' FROM anchor WHERE v='00:00'").Scan(&current, &txStamp); err != nil {
		t.Fatal(err)
	}
	if current != txStamp.Format("15:04:05.999999") {
		t.Fatal(current, txStamp)
	}
	rows, err = tx.QueryContext(ctx, "SELECT v FROM anchor WHERE v=$1", current)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for rows.Next() {
		count++
	}
	if err = rows.Err(); err != nil || count < 2 {
		t.Fatal(count, err)
	}
	rows.Close()
	var again string
	var againStamp time.Time
	if err = held.QueryRowContext(ctx).Scan(&again, &againStamp); err != nil || again != frozen || !againStamp.Equal(stamp) {
		t.Fatal(again, againStamp, err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err = contextual.ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = contextual.ExecContext(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	param, err := conn.PrepareContext(ctx, "SELECT v FROM anchor WHERE v=$1")
	if err != nil {
		t.Fatal(err)
	}
	defer param.Close()
	if _, err = param.QueryContext(ctx); err == nil {
		t.Fatal("missing argument succeeded")
	}
}
