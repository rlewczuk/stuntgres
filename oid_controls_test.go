package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
)

func TestOIDControls(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	db := openLocal(t, d, t.Name())
	execSQL(t, db, `CREATE TABLE "MixedOID"(id INT,v OID DEFAULT '4294967295'::oid)`)
	execSQL(t, db, `INSERT INTO "MixedOID" VALUES(1,'0'),(2,'2147483648'),(3,'4294967295'),(4,NULL)`)
	execSQL(t, db, "CREATE TABLE empty(v OID)")
	execSQL(t, db, "CREATE TABLE defaults(v OID DEFAULT NULL::oid,b BOOL DEFAULT ('4294967295'::oid > '2147483648'::oid))")
	want := [][]any{{int64(1), int64(0)}, {int64(2), int64(2147483648)}, {int64(3), int64(4294967295)}, {int64(4), nil}}
	raw, err := d.Open(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	held, err := raw.(driver.QueryerContext).QueryContext(ctx, `SELECT * FROM "MixedOID" ORDER BY id`, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), "OID") || !strings.Contains(dump.String(), "'4294967295'") {
		t.Fatal(dump.String())
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
	wantTuples(t, db, `SELECT * FROM "MixedOID" ORDER BY id`, want)
	wantTuples(t, db, "SELECT v FROM empty", [][]any{})
	execSQL(t, db, "INSERT INTO defaults DEFAULT VALUES")
	wantTuples(t, db, "SELECT * FROM defaults", [][]any{{nil, true}})
	if held.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(1) != "OID" || held.(driver.RowsColumnTypeScanType).ColumnTypeScanType(1) != reflect.TypeFor[int64]() {
		t.Fatal("retained metadata")
	}
	values := make([]driver.Value, 2)
	for _, row := range want {
		if err = held.Next(values); err != nil || !reflect.DeepEqual([]any{values[0], values[1]}, row) {
			t.Fatal(values, row, err)
		}
	}
	if err = held.Next(values); err != io.EOF {
		t.Fatal(err)
	}
	execSQL(t, db, `INSERT INTO "MixedOID"(id) VALUES(5)`)
	wantTuples(t, db, `SELECT v FROM "MixedOID" WHERE id=5`, [][]any{{int64(4294967295)}})
	for _, script := range []string{"CREATE TABLE replacement(v OID); INSERT INTO replacement VALUES('1'); INSERT INTO replacement VALUES('4294967296')", "CREATE TABLE replacement(v OID); INSERT INTO replacement VALUES('1'); INSERT INTO replacement VALUES((-1)::int8::oid)"} {
		if err = stuntgres.Restore(ctx, db, strings.NewReader(script)); err == nil {
			t.Fatal("bad restore accepted")
		}
		wantTuples(t, db, `SELECT v FROM "MixedOID" WHERE id=5`, [][]any{{int64(4294967295)}})
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err = stuntgres.Restore(cancelled, db, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	wantTuples(t, db, `SELECT v FROM "MixedOID" WHERE id=5`, [][]any{{int64(4294967295)}})
}
