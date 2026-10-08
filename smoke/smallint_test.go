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
	"testing"

	"stuntgres"
)

func TestSmallintSmoke(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	connector, err := d.OpenConnector("smallint-smoke")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	exec := func(q string, count int64, args ...any) {
		t.Helper()
		r, err := db.ExecContext(ctx, q, args...)
		if err != nil {
			t.Fatal(q, err)
		}
		if n, err := r.RowsAffected(); err != nil || n != count {
			t.Fatal(q, n, err)
		}
	}
	check := func(q string, types []string, want [][]any) {
		t.Helper()
		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		metadata, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if len(metadata) != len(types) {
			t.Fatal(metadata)
		}
		for i, column := range metadata {
			if column.DatabaseTypeName() != types[i] || column.ScanType() != reflect.TypeFor[int64]() {
				t.Fatal(column)
			}
		}
		got := make([][]any, 0)
		for rows.Next() {
			row := make([]any, len(types))
			dst := make([]any, len(types))
			for i := range row {
				dst[i] = &row[i]
			}
			if err := rows.Scan(dst...); err != nil {
				t.Fatal(err)
			}
			got = append(got, row)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s got %#v want %#v", q, got, want)
		}
	}
	diagnostic := func(err error, want stuntgres.Error) {
		t.Helper()
		if err := typeCoreDiagnostic(err, want); err != nil {
			t.Fatal(err)
		}
	}
	exec("CREATE TABLE typed(s SMALLINT NOT NULL,i INTEGER,b BIGINT,n int2)", 0)
	exec("CREATE TABLE empty(s SMALLINT)", 0)
	alias, err := db.PrepareContext(ctx, `CREATE TABLE alias(s "int2",n SMALLINT)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = alias.ExecContext(ctx)
	alias.Close()
	if err != nil {
		t.Fatal(err)
	}
	endpoints, err := db.PrepareContext(ctx, "INSERT INTO alias VALUES($1,$2)")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int64{-32768, 32767} {
		r, err := endpoints.ExecContext(ctx, n, nil)
		if err != nil {
			t.Fatal(err)
		}
		if count, err := r.RowsAffected(); err != nil || count != 1 {
			t.Fatal(count, err)
		}
	}
	endpoints.Close()
	check("SELECT * FROM alias ORDER BY s", []string{"INT2", "INT2"}, [][]any{{int64(-32768), nil}, {int64(32767), nil}})
	exec("INSERT INTO typed VALUES(-32768,-2147483648,-9223372036854775808,NULL),(32767,2147483647,9223372036854775807,NULL)", 2)
	stmt, err := db.PrepareContext(ctx, "INSERT INTO typed VALUES($1,$2,$3,$4)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, args := range [][]any{{[]byte("0x_FF"), int64(7), int64(8), nil}, {int64(1000), nil, nil, nil}} {
		r, err := stmt.ExecContext(ctx, args...)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := r.RowsAffected(); err != nil || n != 1 {
			t.Fatal(n, err)
		}
	}
	want := [][]any{{int64(-32768), int64(-2147483648), int64(-9223372036854775808), nil}, {int64(255), int64(7), int64(8), nil}, {int64(1000), nil, nil, nil}, {int64(32767), int64(2147483647), int64(9223372036854775807), nil}}
	check("SELECT * FROM typed ORDER BY s", []string{"INT2", "INT4", "INT8", "INT2"}, want)
	check("SELECT * FROM empty", []string{"INT2"}, [][]any{})
	exec("CREATE TABLE failures(i SMALLINT NOT NULL)", 0)
	exec("INSERT INTO failures VALUES(99)", 1)
	bound, err := db.PrepareContext(ctx, "INSERT INTO failures VALUES(1),($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer bound.Close()
	for _, tc := range []struct {
		raw           any
		code, message string
	}{
		{"oops", "22P02", `invalid input syntax for type smallint: "oops"`},
		{int64(32768), "22003", `value "32768" is out of range for type smallint`},
		{int64(-32769), "22003", `value "-32769" is out of range for type smallint`},
	} {
		_, err := bound.ExecContext(ctx, tc.raw)
		diagnostic(err, stuntgres.Error{Code: tc.code, Message: tc.message, Context: "unnamed portal parameter $1 = '...'"})
		_, err = db.ExecContext(ctx, "INSERT INTO failures VALUES(1),($1)", tc.raw)
		diagnostic(err, stuntgres.Error{Code: tc.code, Message: tc.message, Context: "unnamed portal parameter $1 = '...'"})
		check("SELECT * FROM failures", []string{"INT2"}, [][]any{{int64(99)}})
	}
	for _, q := range []string{"INSERT INTO failures VALUES(1),(32768)", "INSERT INTO failures VALUES(1),(-32769)"} {
		prepared, err := db.PrepareContext(ctx, q)
		if err != nil {
			t.Fatal("typed narrowing failed at prepare", err)
		}
		_, err = prepared.ExecContext(ctx)
		prepared.Close()
		diagnostic(err, stuntgres.Error{Code: "22003", Message: "smallint out of range"})
		_, err = db.ExecContext(ctx, q)
		diagnostic(err, stuntgres.Error{Code: "22003", Message: "smallint out of range"})
		check("SELECT * FROM failures", []string{"INT2"}, [][]any{{int64(99)}})
	}
	_, err = db.ExecContext(ctx, "INSERT INTO failures(i) VALUES('oops')")
	diagnostic(err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type smallint: "oops"`, Position: "32"})
	_, err = db.ExecContext(ctx, "INSERT INTO failures VALUES(1),('oops')")
	diagnostic(err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type smallint: "oops"`, Position: "33"})
	_, err = db.ExecContext(ctx, "INSERT INTO failures VALUES()")
	diagnostic(err, stuntgres.Error{Code: "42601", Message: `syntax error at or near ")"`, Position: "29"})
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = db.ExecContext(canceled, "INSERT INTO failures VALUES(1),(2)")
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	check("SELECT * FROM failures", []string{"INT2"}, [][]any{{int64(99)}})

	raw, err := d.Open("smallint-smoke")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	held, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT * FROM typed ORDER BY s", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"SMALLINT", "INTEGER", "BIGINT"} {
		if !strings.Contains(dump.String(), name) {
			t.Fatal(dump.String())
		}
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
	check("SELECT * FROM typed ORDER BY s", []string{"INT2", "INT4", "INT8", "INT2"}, want)
	for i, name := range []string{"INT2", "INT4", "INT8", "INT2"} {
		if held.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(i) != name {
			t.Fatal("retained metadata changed")
		}
	}
	for i := range 4 {
		nullable, known := held.(driver.RowsColumnTypeNullable).ColumnTypeNullable(i)
		if !known || nullable != (i != 0) || held.(driver.RowsColumnTypeScanType).ColumnTypeScanType(i) != reflect.TypeFor[int64]() {
			t.Fatal("retained scan/nullability changed")
		}
	}
	dst := make([]driver.Value, 4)
	for _, row := range want {
		if err = held.Next(dst); err != nil {
			t.Fatal(err)
		}
		for i, v := range row {
			if dst[i] != v {
				t.Fatal(dst, row)
			}
		}
	}
	if err = held.Next(dst); err != io.EOF {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE replacement(s SMALLINT); INSERT INTO replacement VALUES(1),(32768)")); err == nil {
		t.Fatal("bad restore accepted")
	}
	check("SELECT * FROM typed ORDER BY s", []string{"INT2", "INT4", "INT8", "INT2"}, want)
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, q := range []string{"CREATE ROLE reader", "CREATE TABLE secret(i SMALLINT)", "INSERT INTO secret VALUES(7)", "SET SESSION AUTHORIZATION reader"} {
		if _, err = conn.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	defer conn.ExecContext(ctx, "RESET SESSION AUTHORIZATION")
	_, err = conn.ExecContext(ctx, "INSERT INTO secret VALUES(9)")
	diagnostic(err, stuntgres.Error{Code: "42501", Message: "permission denied for table secret"})
	denied, err := conn.PrepareContext(ctx, "INSERT INTO secret VALUES($1)")
	if err != nil {
		t.Fatal(err)
	}
	_, err = denied.ExecContext(ctx, int64(9))
	denied.Close()
	diagnostic(err, stuntgres.Error{Code: "42501", Message: "permission denied for table secret"})
	if _, err = conn.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
		t.Fatal(err)
	}
	var sentinel int64
	if err = conn.QueryRowContext(ctx, "SELECT i FROM secret").Scan(&sentinel); err != nil || sentinel != 7 {
		t.Fatal(sentinel, err)
	}
}
