package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestByteaSmoke(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	c, err := d.OpenConnector("bytea-smoke")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	exec := func(q string, n int64, args ...any) {
		t.Helper()
		r, err := conn.ExecContext(ctx, q, args...)
		if err != nil {
			t.Fatal(q, err)
		}
		got, err := r.RowsAffected()
		if err != nil || got != n {
			t.Fatal(q, got, err)
		}
	}
	exec("CREATE TABLE t(id INTEGER,v BYTEA)", 0)
	exec(`INSERT INTO t VALUES(1,''),(2,'\x00aAfF'),(3,'\000\177\200\377'),(4,'\\'),(5,'é'),(6,NULL)`, 6)
	stmt, err := conn.PrepareContext(ctx, "INSERT INTO t VALUES($1,$2)")
	if err != nil {
		t.Fatal(err)
	}
	for i, arg := range []any{[]byte{0, 128, 255}, `\x41`, []byte(nil), []byte{}} {
		r, err := stmt.ExecContext(ctx, 7+i, arg)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := r.RowsAffected(); err != nil || n != 1 {
			t.Fatal(n, err)
		}
	}
	stmt.Close()
	want := []any{[]byte{}, []byte{0, 170, 255}, []byte{0, 127, 128, 255}, []byte{92}, []byte{195, 169}, nil, []byte{0, 128, 255}, []byte{92, 120, 52, 49}, nil, []byte{}}
	check := func() {
		t.Helper()
		rows, err := conn.QueryContext(ctx, "SELECT v FROM t ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if types[0].Name() != "v" || types[0].DatabaseTypeName() != "BYTEA" || types[0].ScanType() != reflect.TypeFor[[]byte]() {
			t.Fatal(types[0])
		}
		if n, k := types[0].Nullable(); !n || !k {
			t.Fatal(n, k)
		}
		if _, k := types[0].Length(); k {
			t.Fatal("native length")
		}
		if _, _, k := types[0].DecimalSize(); k {
			t.Fatal("decimal size")
		}
		i := 0
		for rows.Next() {
			var raw any
			if err := rows.Scan(&raw); err != nil {
				t.Fatal(err)
			}
			if i >= len(want) || !reflect.DeepEqual(raw, want[i]) {
				t.Fatalf("row %d got %#v want %#v", i, raw, want)
			}
			if b, ok := raw.([]byte); ok && len(b) > 0 {
				b[0] ^= 255
			}
			i++
		}
		if err := rows.Err(); err != nil || i != len(want) {
			t.Fatal(i, err)
		}
		rows.Close()
		if types[0].DatabaseTypeName() != "BYTEA" {
			t.Fatal("lost metadata")
		}
	}
	check()
	exec("CREATE TABLE nn(v BYTEA,n INTEGER NOT NULL)", 0)
	for _, mode := range []struct{ name, detail string }{
		{"hex", `Failing row contains (\x0020275c7e7f80ff, null).`},
		{"escape", "Failing row contains (\\000 '\\\\~\\177\\200\\377, null)."},
	} {
		exec("SET bytea_output="+mode.name, 0)
		var shown string
		if err := conn.QueryRowContext(ctx, "SHOW bytea_output").Scan(&shown); err != nil || shown != mode.name {
			t.Fatal(shown, err)
		}
		check()
		_, err := conn.ExecContext(ctx, `INSERT INTO nn VALUES('\x0020275c7e7f80ff',NULL)`)
		var se *stuntgres.Error
		if !errors.As(err, &se) || se.Code != "23502" || se.Detail != mode.detail || se.Table != "nn" || se.Column != "n" {
			t.Fatal(err, se)
		}
	}
	_, err = conn.ExecContext(ctx, `INSERT INTO t VALUES(11,'\x41'),(12,'\q')`)
	if e := typeCoreDiagnostic(err, stuntgres.Error{Code: "22P02", Message: "invalid input syntax for type bytea", Position: "37"}); e != nil {
		t.Fatal(e)
	}
	check()
	exec("CREATE TABLE binary_schema(id INTEGER,v BYTEA,r BYTEA NOT NULL)", 0)
	exec(`INSERT INTO binary_schema VALUES(1,'',''),(2,'\x0080ff','\x0080ff'),(3,NULL,'\xff')`, 3)
	checkBinarySchema := func() {
		t.Helper()
		rows, err := conn.QueryContext(ctx, "SELECT v,r FROM binary_schema ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 2 {
			t.Fatal(types, err)
		}
		for i, name := range []string{"v", "r"} {
			column := types[i]
			if column.Name() != name || column.DatabaseTypeName() != "BYTEA" || column.ScanType() != reflect.TypeFor[[]byte]() {
				t.Fatal("restored BYTEA schema", column)
			}
			if nullable, known := column.Nullable(); !known || nullable != (i == 0) {
				t.Fatal("restored declaration nullability", name, nullable, known)
			}
		}
		expected := [][]any{{[]byte{}, []byte{}}, {[]byte{0, 128, 255}, []byte{0, 128, 255}}, {nil, []byte{255}}}
		for i := range expected {
			if !rows.Next() {
				t.Fatal("missing restored row", i, rows.Err())
			}
			var nullable, required any
			if err := rows.Scan(&nullable, &required); err != nil || !reflect.DeepEqual([]any{nullable, required}, expected[i]) {
				t.Fatal("restored bytes", i, nullable, required, err)
			}
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("extra restored rows", rows.Err())
		}
	}
	checkBinarySchema()
	// Native retained driver snapshots survive catalog replacement and exported mutations.
	raw, err := d.Open("bytea-smoke")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	held, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT v FROM t ORDER BY id", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	for _, piece := range []string{`"v" BYTEA, "r" BYTEA CONSTRAINT "binary_schema_r_not_null" NOT NULL`, `'\x00aaff'`, `'\x0080ff'`, `'\xff'`, `'\x'`} {
		if !strings.Contains(dump.String(), piece) {
			t.Fatal(piece, dump.String())
		}
	}
	exec("SET bytea_output=hex", 0)
	var hexDump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &hexDump); err != nil || !bytes.Equal(dump.Bytes(), hexDump.Bytes()) {
		t.Fatal(hexDump.String(), err)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check()
	checkBinarySchema()
	_, err = conn.ExecContext(ctx, "INSERT INTO binary_schema VALUES(4,NULL,$1),(5,NULL,NULL)", []byte{})
	if e := typeCoreDiagnostic(err, stuntgres.Error{
		Code: "23502", Message: `null value in column "r" of relation "binary_schema" violates not-null constraint`,
		Detail: "Failing row contains (5, null, null).", Schema: "public", Table: "binary_schema", Column: "r",
	}); e != nil {
		t.Fatal(e)
	}
	checkBinarySchema()
	dst := make([]driver.Value, 1)
	for i, w := range want {
		if err := held.Next(dst); err != nil || !reflect.DeepEqual(dst[0], w) {
			t.Fatal(i, dst, err)
		}
	}
	if err := held.Next(dst); err != io.EOF {
		t.Fatal(err)
	}
	if held.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0) != "BYTEA" {
		t.Fatal("snapshot metadata")
	}
}
