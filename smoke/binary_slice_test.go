package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres"
	"testing"
)

func TestBinarySliceSmoke(t *testing.T) {
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	exec := func(q string, n int64) {
		t.Helper()
		result, err := db.Exec(q)
		if err != nil {
			t.Fatal(q, err)
		}
		got, err := result.RowsAffected()
		if err != nil || got != n {
			t.Fatal(q, got, err)
		}
	}
	check := func(rows *sql.Rows, want []any) {
		t.Helper()
		defer rows.Close()
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != len(want) {
			t.Fatal(types, err)
		}
		for i, typ := range types {
			name, scan := "BYTEA", reflect.TypeFor[[]byte]()
			if _, ok := want[i].(int64); ok {
				name, scan = "INT4", reflect.TypeFor[int64]()
			}
			if typ.DatabaseTypeName() != name || typ.ScanType() != scan {
				t.Fatal(i, typ)
			}
		}
		if !rows.Next() {
			t.Fatal("missing row", rows.Err())
		}
		got := make([]any, len(want))
		dest := make([]any, len(want))
		for i := range got {
			dest[i] = &got[i]
		}
		if err := rows.Scan(dest...); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal(got, want, err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal(rows.Err())
		}
	}
	query := func(q string, want []any, args ...any) {
		t.Helper()
		rows, err := db.Query(q, args...)
		if err != nil {
			t.Fatal(q, err)
		}
		check(rows, want)
	}
	exec("CREATE TABLE t(id INTEGER,v BYTEA,s BYTEA,i INTEGER,n INTEGER)", 0)
	exec(`INSERT INTO t VALUES(1,'\x00c3a9ff00414200','\xa9ff',2,1)`, 1)
	q := "SELECT substring(v FROM i FOR n),substr(v,3,1),position(s IN v),pg_catalog.position(v,''),pg_catalog.position(v,'zz') FROM t"
	want := []any{[]byte{195}, []byte{169}, int64(3), int64(1), int64(0)}
	query(q, want)
	stmt, err := db.Prepare(q)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := stmt.Query()
	if err != nil {
		t.Fatal(err)
	}
	check(rows, want)
	stmt.Close()
	query("SELECT substr('abcdef'::bytea,0,3),substring('abcdef'::bytea FROM -2 FOR 4),substring('abcdef'::bytea FOR 3),overlay('abcdef'::bytea,'XY'::bytea,3,-2),overlay('abcdef'::bytea PLACING '\\xc3a9'::bytea FROM 3)", []any{[]byte("ab"), []byte("a"), []byte("abc"), []byte("abXYabcdef"), []byte{'a', 'b', 195, 169, 'e', 'f'}})
	query(`SELECT trim('\xff8000'::bytea FROM '\x00ff8041008042ff00'::bytea),trim(LEADING '\xff8000'::bytea FROM '\x00ff8041008042ff00'::bytea),trim(TRAILING '\x00ff8041008042ff00'::bytea,'\xff8000'::bytea)`, []any{[]byte{65, 0, 128, 66}, []byte{65, 0, 128, 66, 255, 0}, []byte{0, 255, 128, 65, 0, 128, 66}})
	stmt, err = db.Prepare("SELECT overlay($1::bytea,$2::bytea,$3,$4),substr($1::bytea,$3,$4)")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		a, b any
		s, n int
		want []any
	}{{[]byte("abcdef"), []byte("XY"), 3, 2, []any{[]byte("abXYef"), []byte("cd")}}, {[]byte{}, []byte{}, 1, 0, []any{[]byte{}, []byte{}}}, {[]byte(nil), []byte("x"), 1, 1, []any{nil, nil}}, {`\x41`, []byte("x"), 2, 1, []any{[]byte(`\x41`), []byte("x")}}} {
		rows, err := stmt.Query(tc.a, tc.b, tc.s, tc.n)
		if err != nil {
			t.Fatal(err)
		}
		check(rows, tc.want)
	}
	stmt.Close()
	exec("CREATE TABLE src(id INTEGER,v BYTEA,n INTEGER)", 0)
	exec("INSERT INTO src VALUES(1,'abcdef',2),(2,'abcdef',-1)", 2)
	exec("CREATE TABLE dst(v BYTEA)", 0)
	exec("INSERT INTO dst VALUES('sentinel')", 1)
	rows, err = db.Query("INSERT INTO dst SELECT substr(v,1,n) FROM src ORDER BY id")
	if rows != nil {
		rows.Close()
		t.Fatal("failed write exposed cursor")
	}
	var diagnostic *stuntgres.Error
	expected := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22011", Message: "negative substring length not allowed"}
	if !errors.As(err, &diagnostic) || *diagnostic != expected {
		t.Fatal(err, diagnostic)
	}
	query("SELECT v FROM dst", []any{[]byte("sentinel")})
	exec("CREATE TABLE checked(v BYTEA,s BYTEA,i INTEGER,n INTEGER,e BYTEA,CHECK(overlay(v PLACING s FROM i FOR n)=e))", 0)
	exec("INSERT INTO checked VALUES('abcdef','XY',3,2,'abXYef')", 1)
	exec(`CREATE VIEW slices AS SELECT overlay(v PLACING '\x0080ff' FROM i FOR n) AS o,substring(v FROM i FOR n) AS b,position(s IN v) AS p,trim('\x00' FROM v) AS t FROM checked`, 0)
	restored := []any{[]byte{'a', 'b', 0, 128, 255, 'e', 'f'}, []byte("cd"), int64(0), []byte("abcdef")}
	held, err := db.Query("SELECT * FROM slices")
	if err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	query("SELECT * FROM slices", restored)
	check(held, restored)
	_, err = db.Exec("INSERT INTO checked VALUES('abcdef','XY',3,2,'wrong')")
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23514" {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	rows, err = db.QueryContext(canceled, "SELECT * FROM slices")
	if rows != nil {
		rows.Close()
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	query("SELECT * FROM slices", restored)
}
