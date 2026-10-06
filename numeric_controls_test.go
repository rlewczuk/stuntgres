package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestNumericControlsSnapshotsAndOwnership(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	db := openLocal(t, d, "numeric-controls")
	execSQL(t, db, "CREATE TABLE n(id INT,v NUMERIC,a NUMERIC(2,-3),b NUMERIC(3,5))")
	exact := "12345678901234567890.1234567890123456789000"
	execSQL(t, db, "INSERT INTO n VALUES(1,$1,1500,.009994),(2,'NaN',NULL,NULL),(3,'Infinity',NULL,NULL),(4,'-Infinity',NULL,NULL),(5,NULL,NULL,NULL)", []byte(exact))
	raw, err := d.Open("numeric-controls")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	owned := []byte(exact)
	held, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT id,v,a,b FROM n WHERE v=$1", []driver.NamedValue{{Ordinal: 1, Value: owned}})
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	owned[0] = 'X'
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), "NUMERIC(2,-3)") || !strings.Contains(dump.String(), "'"+exact+"'") {
		t.Fatal(dump.String())
	}
	execSQL(t, db, "INSERT INTO n VALUES(6,0,NULL,NULL)")
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	want := [][]any{{int64(1), exact, "2000", "0.00999"}, {int64(2), "NaN", nil, nil}, {int64(3), "Infinity", nil, nil}, {int64(4), "-Infinity", nil, nil}, {int64(5), nil, nil, nil}}
	wantTuples(t, db, "SELECT * FROM n ORDER BY id", want)
	var redump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal(redump.String(), err)
	}
	metadata := held.(driver.RowsColumnTypeDatabaseTypeName)
	sizing := held.(driver.RowsColumnTypePrecisionScale)
	if metadata.ColumnTypeDatabaseTypeName(1) != "NUMERIC" {
		t.Fatal("old descriptor changed")
	}
	p, s, ok := sizing.ColumnTypePrecisionScale(2)
	if !ok || p != 2 || s != -3 {
		t.Fatal(p, s, ok)
	}
	dst := make([]driver.Value, 4)
	if err = held.Next(dst); err != nil || !reflect.DeepEqual(dst, []driver.Value{int64(1), exact, "2000", "0.00999"}) {
		t.Fatal(dst, err)
	}
	if err = held.Next(dst); err != io.EOF {
		t.Fatal(err)
	}
	for _, script := range []string{"CREATE TABLE staged(v NUMERIC(3,1)); INSERT INTO staged VALUES(1.2),(99.95)", "CREATE TABLE staged(v NUMERIC); INSERT INTO staged VALUES(1),('oops')", "CREATE TABLE staged(v NUMERIC); INSERT INTO staged VALUES(1); SELECT 1.2__3 FROM staged"} {
		if err = stuntgres.Restore(ctx, db, strings.NewReader(script)); err == nil {
			t.Fatal("invalid restore succeeded")
		}
		wantTuples(t, db, "SELECT * FROM n ORDER BY id", want)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err = stuntgres.Restore(canceled, db, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT * FROM n ORDER BY id", want)
	iterCtx, stop := context.WithCancel(ctx)
	cursor, err := raw.(driver.QueryerContext).QueryContext(iterCtx, "SELECT v FROM n", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	stop()
	if err = cursor.Next(make([]driver.Value, 1)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
