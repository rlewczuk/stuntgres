package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestNameControls(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	db := openLocal(t, d, t.Name())
	execSQL(t, db, `CREATE TABLE "MixedObject"(id INT,n NAME)`)
	execSQL(t, db, "CREATE TABLE empty(n NAME)")
	execSQL(t, db, "CREATE TABLE nulls(n NAME)")
	execSQL(t, db, "INSERT INTO nulls VALUES(NULL)")
	want := [][]any{{int64(1), "MiXeD"}, {int64(2), "O'Brien\\tail\n🙂"}, {int64(3), strings.Repeat("a", 61) + "é"}, {int64(4), " x "}, {int64(5), ""}, {int64(6), nil}}
	for _, row := range want {
		input := row[1]
		if row[0] == int64(3) {
			input = strings.Repeat("a", 61) + "éZ"
		}
		execSQL(t, db, `INSERT INTO "MixedObject" VALUES($1,$2)`, row[0], input)
	}
	raw, err := d.Open(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	held, err := raw.(driver.QueryerContext).QueryContext(ctx, `SELECT * FROM "MixedObject" ORDER BY id`, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var redump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal(redump.String(), err)
	}
	wantTuples(t, db, `SELECT * FROM "MixedObject" ORDER BY id`, want)
	wantTuples(t, db, "SELECT n FROM nulls", [][]any{{nil}})
	wantTuples(t, db, "SELECT n FROM empty", [][]any{})
	if held.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(1) != "NAME" || held.(driver.RowsColumnTypeScanType).ColumnTypeScanType(1) != reflect.TypeFor[string]() {
		t.Fatal("retained NAME metadata")
	}
	values := make([]driver.Value, 2)
	for _, row := range want {
		if err = held.Next(values); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual([]any{values[0], values[1]}, row) {
			t.Fatal(values, row)
		}
	}
	if err = held.Next(values); err != io.EOF {
		t.Fatal(err)
	}
	for _, script := range []string{"CREATE TABLE replacement(n NAME); INSERT INTO replacement VALUES('ok'); nonsense", "CREATE TABLE replacement(n NAME); INSERT INTO replacement VALUES('ok'),('" + strings.Repeat("a", 63) + "\xff')"} {
		if err = stuntgres.Restore(ctx, db, strings.NewReader(script)); err == nil {
			t.Fatal("bad staged restore accepted")
		}
		wantTuples(t, db, `SELECT * FROM "MixedObject" ORDER BY id`, want)
	}
}
