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

func TestCastExplicitSmoke(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	connector, err := d.OpenConnector("cast-explicit-smoke")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	exec := func(q string, count int64) {
		t.Helper()
		result, err := db.ExecContext(ctx, q)
		if err != nil {
			t.Fatal(q, err)
		}
		got, err := result.RowsAffected()
		if err != nil || got != count {
			t.Fatal(q, got, count, err)
		}
	}
	check := func(rows *sql.Rows, err error, names, types []string, want [][]any) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		gotNames, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(gotNames, names) {
			t.Fatal(gotNames, names, err)
		}
		meta, err := rows.ColumnTypes()
		if err != nil || len(meta) != len(types) {
			t.Fatal(meta, err)
		}
		for i, typ := range types {
			if meta[i].DatabaseTypeName() != typ {
				t.Fatal(meta[i], typ)
			}
		}
		got := make([][]any, 0)
		for rows.Next() {
			v := make([]any, len(names))
			dst := make([]any, len(v))
			for i := range v {
				dst[i] = &v[i]
			}
			if err := rows.Scan(dst...); err != nil {
				t.Fatal(err)
			}
			got = append(got, v)
		}
		if err := rows.Err(); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal(got, want, err)
		}
	}
	exec("CREATE TABLE t(id INT4,i INT4,n INT8,s TEXT,b BOOL)", 0)
	exec("INSERT INTO t VALUES(1,2,2,'42',TRUE),(2,0,0,'0',FALSE),(3,NULL,NULL,NULL,NULL)", 3)
	rows, err := db.QueryContext(ctx, "SELECT CAST(i AS bool),int4(b),b::text,CAST(s AS int8),NULL::int4 FROM t ORDER BY id")
	check(rows, err, []string{"i", "int4", "b", "s", "int4"}, []string{"BOOL", "INT4", "TEXT", "INT8", "INT4"}, [][]any{{true, int64(1), "true", int64(42), nil}, {false, int64(0), "false", int64(0), nil}, {nil, nil, nil, nil, nil}})
	for _, predicate := range []string{"i=CAST('2' AS int4)", "i='2'::int4", "i=int4('2')", "CAST(i AS bool)"} {
		rows, err = db.QueryContext(ctx, "SELECT id FROM t WHERE "+predicate+" ORDER BY id")
		check(rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}})
	}
	exec("CREATE TABLE saved(id INT4,i INT4,n INT8,s TEXT,b BOOL)", 0)
	exec("INSERT INTO saved VALUES(1,TRUE::int4,int8('9223372036854775807'),CAST(FALSE AS text),(-2)::bool),(2,NULL::int4,NULL::int8,NULL::text,NULL::bool)", 2)
	rows, err = db.QueryContext(ctx, "SELECT i,n,s,b FROM saved ORDER BY id")
	check(rows, err, []string{"i", "n", "s", "b"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(1), int64(9223372036854775807), "false", true}, {nil, nil, nil, nil}})
	stmt, err := db.PrepareContext(ctx, "SELECT $1::int4,$1::text FROM t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, arg := range []any{2, nil, 0, 2} {
		row := []any{nil, nil}
		if arg != nil {
			row = []any{int64(arg.(int)), "2"}
			if arg == 0 {
				row[1] = "0"
			}
		}
		rows, err = stmt.QueryContext(ctx, arg)
		check(rows, err, []string{"int4", "text"}, []string{"INT4", "TEXT"}, [][]any{row, row, row})
	}
	for _, tc := range []struct {
		q    string
		args []any
		want stuntgres.Error
	}{
		{"SELECT CAST(n AS bool) FROM t", nil, stuntgres.Error{Code: "42846", Message: "cannot cast type bigint to boolean", Position: "8"}},
		{"SELECT /*é*/ 'oops'::int4 FROM t", nil, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "14"}},
		{"SELECT CAST('oops'::text AS int4) FROM t", nil, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
		{"SELECT int4($1) FROM t", []any{"42"}, stuntgres.Error{Code: "42725", Message: "function int4(unknown) is not unique", Position: "8", Hint: "Could not choose a best candidate function. You might need to add explicit type casts."}},
		{"SELECT $1,$1::int4 FROM t", []any{2}, stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "8"}},
	} {
		rows, err := db.QueryContext(ctx, tc.q, tc.args...)
		if rows != nil {
			rows.Close()
			t.Fatal("failed cast exposed cursor")
		}
		if err := typeCoreDiagnostic(err, tc.want); err != nil {
			t.Fatal(err)
		}
	}
	exec("CREATE TABLE atom(i INT4)", 0)
	exec("INSERT INTO atom VALUES(7)", 1)
	for _, tc := range []struct {
		q    string
		want stuntgres.Error
	}{
		{"INSERT INTO atom VALUES(CAST('8' AS int4)),(CAST('oops' AS int4))", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "50"}},
		{"INSERT INTO atom VALUES(8),(2147483648::int4)", stuntgres.Error{Code: "22003", Message: "integer out of range"}},
	} {
		_, err := db.ExecContext(ctx, tc.q)
		if err := typeCoreDiagnostic(err, tc.want); err != nil {
			t.Fatal(err)
		}
		rows, err = db.QueryContext(ctx, "SELECT i FROM atom")
		check(rows, err, []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}})
	}
	raw, err := d.Open("cast-explicit-smoke")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	cursor, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT i::int8,text(b) FROM saved ORDER BY id", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
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
	rows, err = db.QueryContext(ctx, "SELECT i,n,s,b FROM saved ORDER BY id")
	check(rows, err, []string{"i", "n", "s", "b"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(1), int64(9223372036854775807), "false", true}, {nil, nil, nil, nil}})
	var redump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatal("roundtrip not deterministic", err)
	}
	script := "CREATE TABLE replacement(i INT4); INSERT INTO replacement VALUES(CAST($1::text AS int4))"
	err = stuntgres.Restore(ctx, db, strings.NewReader(script))
	if err := typeCoreDiagnostic(err, stuntgres.Error{Code: "42P02", Message: "parameters are not allowed in restore", Position: "71"}); err != nil {
		t.Fatal(err)
	}
	var after bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &after); err != nil || !bytes.Equal(dump.Bytes(), after.Bytes()) {
		t.Fatal("failed restore published", err)
	}
	if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE replacement(i INT4); INSERT INTO replacement VALUES(int4(TRUE))")); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cursor.Columns(), []string{"i", "text"}) {
		t.Fatal(cursor.Columns())
	}
	meta := cursor.(driver.RowsColumnTypeDatabaseTypeName)
	if meta.ColumnTypeDatabaseTypeName(0) != "INT8" || meta.ColumnTypeDatabaseTypeName(1) != "TEXT" {
		t.Fatal("retained metadata changed")
	}
	dst := make([]driver.Value, 2)
	for _, want := range [][]driver.Value{{int64(1), "true"}, {nil, nil}} {
		if err := cursor.Next(dst); err != nil || !reflect.DeepEqual(dst, want) {
			t.Fatal(dst, want, err)
		}
	}
	if err := cursor.Next(dst); err != io.EOF {
		t.Fatal(err)
	}
	rows, err = db.QueryContext(ctx, "SELECT i FROM replacement")
	check(rows, err, []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}})
}
