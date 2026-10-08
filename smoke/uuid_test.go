package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestUUIDSmoke(t *testing.T) {
	d := stuntgres.NewDriver()
	c, err := d.OpenConnector("uuid-smoke")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	defer db.Close()
	for _, step := range []struct {
		q string
		n int64
	}{
		{"CREATE TABLE u(v UUID)", 0},
		{"INSERT INTO u(v) VALUES(NULL),('ffffffff-ffff-ffff-ffff-ffffffffffff'),('{A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11}'),('00000000000000000000000000000000'),('a0eebc999c0b4ef8bb6d6bb9bd380a11')", 5},
	} {
		r, err := db.Exec(step.q)
		if err != nil {
			t.Fatal(err)
		}
		n, err := r.RowsAffected()
		if err != nil || n != step.n {
			t.Fatal(n, err)
		}
	}
	rows, err := db.Query("SELECT v FROM u ORDER BY v")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if types[0].DatabaseTypeName() != "UUID" || types[0].ScanType() != reflect.TypeFor[string]() {
		t.Fatal(types[0])
	}
	want := []any{"00000000-0000-0000-0000-000000000000", "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", "ffffffff-ffff-ffff-ffff-ffffffffffff", nil}
	var got []any
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	rows.Close()
	insert, err := db.Prepare("INSERT INTO u VALUES($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer insert.Close()
	for _, arg := range []any{[]byte("{A0EEBC999C0B4EF8BB6D6BB9BD380A11}"), nil} {
		r, err := insert.Exec(arg)
		if err != nil {
			t.Fatal(err)
		}
		n, err := r.RowsAffected()
		if err != nil || n != 1 {
			t.Fatal(n, err)
		}
	}
	query, err := db.Prepare("SELECT v FROM u WHERE v=$1")
	if err != nil {
		t.Fatal(err)
	}
	defer query.Close()
	for _, arg := range []any{uuidSmokeA, nil} {
		rows, err := query.Query(arg)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil || v != uuidSmokeA {
				t.Fatal(v, err)
			}
			count++
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		want := 3
		if arg == nil {
			want = 0
		}
		if count != want {
			t.Fatal(count, want)
		}
	}
	for _, tc := range []struct{ q, pos string }{{"INSERT INTO u(v) VALUES('bad')", "25"}, {"/*é*/ SELECT v FROM u WHERE v='bad'", "31"}} {
		_, err := db.Exec(tc.q)
		if err := typeCoreDiagnostic(err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type uuid: "bad"`, Position: tc.pos}); err != nil {
			t.Fatal(err)
		}
	}
	_, err = insert.Exec("bad")
	if err := typeCoreDiagnostic(err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type uuid: "bad"`,Context:"unnamed portal parameter $1 = '...'"}); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("INSERT INTO u VALUES('00000000000000000000000000000000'),(NULL),('bad')")
	if err == nil {
		t.Fatal("later malformed tuple accepted")
	}
	raw, err := d.Open("uuid-smoke")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	held, err := raw.(driver.QueryerContext).QueryContext(context.Background(), "SELECT v FROM u ORDER BY v", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), "'"+uuidSmokeA+"'") || strings.Contains(dump.String(), "{A0EE") {
		t.Fatal(dump.String())
	}
	if err := stuntgres.Reset(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal(redump.String(), err)
	}
	if held.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0) != "UUID" || held.(driver.RowsColumnTypeScanType).ColumnTypeScanType(0) != reflect.TypeFor[string]() {
		t.Fatal("retained metadata")
	}
	dst := make([]driver.Value, 1)
	for _, want := range []driver.Value{"00000000-0000-0000-0000-000000000000", uuidSmokeA, uuidSmokeA, uuidSmokeA, "ffffffff-ffff-ffff-ffff-ffffffffffff", nil, nil} {
		if err := held.Next(dst); err != nil || dst[0] != want {
			t.Fatal(dst, want, err)
		}
	}
	if err := held.Next(dst); err != io.EOF {
		t.Fatal("atomicity/snapshot count", err)
	}
}

const uuidSmokeA = "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"
