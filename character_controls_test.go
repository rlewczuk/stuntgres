package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"io"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestCharacterControlsRoundTripAndSnapshots(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	db := openLocal(t, d, t.Name())
	execSQL(t, db, "CREATE TABLE c(id INT,c CHAR(4),v VARCHAR(20),u BPCHAR,w VARCHAR,t TEXT)")
	execSQL(t, db, "CREATE TABLE empty(c CHAR(2),v VARCHAR(3),u BPCHAR)")
	escaped := "O'Brien\n\\tail  "
	execSQL(t, db, "INSERT INTO c VALUES(1,'é🙂','ok  ',$1,$2,$3),(2,'','','','',''),(3,NULL,NULL,NULL,NULL,NULL)", escaped, escaped, escaped)
	want := [][]any{{int64(1), "é🙂  ", "ok  ", escaped, escaped, escaped}, {int64(2), "    ", "", "", "", ""}, {int64(3), nil, nil, nil, nil, nil}}
	raw, err := d.Open(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	held, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT * FROM c ORDER BY id", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	names := held.(driver.RowsColumnTypeDatabaseTypeName)
	lengths, ok := held.(driver.RowsColumnTypeLength)
	if !ok {
		t.Fatal("missing string length metadata")
	}
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), "BPCHAR") {
		t.Fatal("unlimited bpchar declaration lost", dump.String())
	}
	execSQL(t, db, "INSERT INTO c VALUES(4,'new','new','new','new','new')")
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
	wantTuples(t, db, "SELECT * FROM c ORDER BY id", want)
	characterMetadata(t, db, "SELECT * FROM empty", []string{"BPCHAR", "VARCHAR", "BPCHAR"}, []int64{2, 3, -1})
	for _, phase := range []string{"before", "after"} {
		if names.ColumnTypeDatabaseTypeName(1) != "BPCHAR" || names.ColumnTypeDatabaseTypeName(2) != "VARCHAR" {
			t.Fatal(phase, "old types changed")
		}
		for i, n := range map[int]int64{1: 4, 2: 20} {
			got, known := lengths.ColumnTypeLength(i)
			if !known || got != n {
				t.Fatal(phase, i, got, known)
			}
		}
		if phase == "before" {
			dst := make([]driver.Value, 6)
			for _, row := range want {
				if err = held.Next(dst); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual([]any{dst[0], dst[1], dst[2], dst[3], dst[4], dst[5]}, row) {
					t.Fatal(dst, row)
				}
			}
			if err = held.Next(dst); err != io.EOF {
				t.Fatal(err)
			}
			held.Close()
		}
	}
	for _, script := range []string{"CREATE TABLE staged(c CHAR(2)); INSERT INTO staged VALUES('ok'),('bad')", "CREATE TABLE staged(v VARCHAR(2)); INSERT INTO staged VALUES('ok'),('é🙂x')", "CREATE TABLE staged(c CHAR(2)); INSERT INTO staged VALUES(CAST($1 AS char(2)))"} {
		if err = stuntgres.Restore(ctx, db, strings.NewReader(script)); err == nil {
			t.Fatal("invalid restore accepted", script)
		}
		wantTuples(t, db, "SELECT * FROM c ORDER BY id", want)
		var unchanged bytes.Buffer
		if err = stuntgres.Dump(ctx, db, &unchanged); err != nil || unchanged.String() != dump.String() {
			t.Fatal("failed restore changed catalog/data", unchanged.String(), err)
		}
	}
}

func TestCharacterRestoreNestedParameterDiagnostic(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE sentinel(v CHAR(2))")
	execSQL(t, db, "INSERT INTO sentinel VALUES('a')")
	var before bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &before); err != nil {
		t.Fatal(err)
	}
	script := "CREATE TABLE staged(v CHAR(2)); INSERT INTO staged VALUES(CAST(CAST($1 AS varchar(3)) AS char(2)))"
	err := stuntgres.Restore(ctx, db, strings.NewReader(script))
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P02", Message: "parameters are not allowed in restore", Position: strconv.Itoa(strings.Index(script, "$1") + 1)})
	var after bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &after); err != nil || after.String() != before.String() {
		t.Fatal(after.String(), err)
	}
	wantTuples(t, db, "SELECT v FROM sentinel", [][]any{{"a "}})
}
