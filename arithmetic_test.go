package stuntgres_test

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

func arithmeticDB(t *testing.T) (*stuntgres.Driver, *sql.DB) {
	t.Helper()
	d := stuntgres.NewDriver()
	c, err := d.OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	t.Cleanup(func() { db.Close() })
	return d, db
}
func arithmeticRows(t *testing.T, db *sql.DB, q string, types []string, want [][]any, args ...any) {
	t.Helper()
	check := func(rows *sql.Rows, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(q, err)
		}
		defer rows.Close()
		meta, err := rows.ColumnTypes()
		if err != nil || len(meta) != len(types) {
			t.Fatal(meta, err)
		}
		for i, typ := range types {
			if meta[i].DatabaseTypeName() != typ {
				t.Fatal(q, meta[i])
			}
		}
		got := make([][]any, 0)
		for rows.Next() {
			v := make([]any, len(types))
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
			t.Fatal(q, got, want, err)
		}
	}
	rows, err := db.Query(q, args...)
	check(rows, err)
	stmt, err := db.Prepare(q)
	if err != nil {
		t.Fatal(q, err)
	}
	defer stmt.Close()
	rows, err = stmt.Query(args...)
	check(rows, err)
}
func arithmeticExec(t *testing.T, db *sql.DB, q string, want int64) {
	t.Helper()
	r, err := db.Exec(q)
	if err != nil {
		t.Fatal(q, err)
	}
	n, err := r.RowsAffected()
	if err != nil || n != want {
		t.Fatal(n, err)
	}
}
func arithmeticError(t *testing.T, err error, want stuntgres.Error) {
	t.Helper()
	typeCoreDiagnostic(t, err, want)
}

func TestArithmeticMatrix(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE t(id INT,i INT,b BIGINT)", 0)
	arithmeticExec(t, db, "INSERT INTO t VALUES(1,7,7),(2,-7,-7),(3,NULL,NULL)", 3)
	for _, tc := range []struct {
		op                 string
		positive, negative int64
	}{{"+", 9, -5}, {"-", 5, -9}, {"*", 14, -14}, {"/", 3, -3}, {"%", 1, -1}} {
		for _, expr := range []struct{ text, typ string }{{"i" + tc.op + "2", "INT4"}, {"b" + tc.op + "2", "INT8"}, {"i" + tc.op + "(b-b+2)", "INT8"}, {"b" + tc.op + "(i-i+2)", "INT8"}} {
			arithmeticRows(t, db, "SELECT "+expr.text+" FROM t ORDER BY id", []string{expr.typ}, [][]any{{tc.positive}, {tc.negative}, {nil}})
		}
	}
	for _, tc := range []struct {
		expr, typ string
		value     any
	}{{"2+3*4", "INT4", int64(14)}, {"(2+3)*4", "INT4", int64(20)}, {"20/3*2", "INT4", int64(12)}, {"20-3-2", "INT4", int64(15)}, {"- -i", "INT4", int64(7)}, {"-2147483648", "INT4", int64(-2147483648)}, {"-9223372036854775808", "INT8", int64(-9223372036854775808)}, {"-(-2147483648)", "INT8", int64(2147483648)}, {"9007199254740993-9007199254740992", "INT8", int64(1)}, {"2147483647+2147483648", "INT8", int64(4294967295)}} {
		arithmeticRows(t, db, "SELECT "+tc.expr+" FROM t WHERE id=1", []string{tc.typ}, [][]any{{tc.value}})
	}
	arithmeticRows(t, db, "SELECT id FROM t WHERE i+1=8", []string{"INT4"}, [][]any{{int64(1)}})
	arithmeticRows(t, db, "SELECT i+1,b+1 FROM t WHERE FALSE", []string{"INT4", "INT8"}, [][]any{})
	for _, arg := range []any{1, nil, 2} {
		want := [][]any{{nil, nil}}
		if arg != nil {
			n := int64(arg.(int))
			want = [][]any{{n + 7, n + 2147483648}}
		}
		arithmeticRows(t, db, "SELECT $1+7,$1+2147483648 FROM t WHERE id=1", []string{"INT4", "INT8"}, want, arg)
	}
	arithmeticRows(t, db, "SELECT $1+2147483648,$1+7 FROM t WHERE id=1", []string{"INT8", "INT8"}, [][]any{{int64(2147483649), int64(8)}}, 1)
}

func TestArithmeticDiagnosticsAtomicity(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE t(id INT,i INT,b BIGINT,s TEXT)", 0)
	arithmeticExec(t, db, "INSERT INTO t VALUES(1,2147483647,9223372036854775807,'x'),(2,-2147483648,-9223372036854775808,'x')", 2)
	for _, tc := range []struct{ expr, where, code, primary string }{{"i+1", "id=1", "22003", "integer out of range"}, {"b+1", "id=1", "22003", "bigint out of range"}, {"i-1", "id=2", "22003", "integer out of range"}, {"b-1", "id=2", "22003", "bigint out of range"}, {"i*2", "id=1", "22003", "integer out of range"}, {"b*2", "id=1", "22003", "bigint out of range"}, {"-i", "id=2", "22003", "integer out of range"}, {"-b", "id=2", "22003", "bigint out of range"}, {"i/-1", "id=2", "22003", "integer out of range"}, {"b/-1", "id=2", "22003", "bigint out of range"}, {"i/0", "id=1", "22012", "division by zero"}, {"b%0", "id=1", "22012", "division by zero"}} {
		q := "SELECT " + tc.expr + " FROM t WHERE " + tc.where
		stmt, err := db.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := stmt.Query()
		if rows != nil {
			rows.Close()
			t.Fatal("failed SELECT exposed cursor")
		}
		arithmeticError(t, err, stuntgres.Error{Code: tc.code, Message: tc.primary})
		stmt.Close()
	}
	arithmeticRows(t, db, "SELECT i% -1,b% -1 FROM t WHERE id=2", []string{"INT4", "INT8"}, [][]any{{int64(0), int64(0)}})
	for _, tc := range []struct {
		q string
		d stuntgres.Error
	}{{"SELECT /*é*/ i+TRUE FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer + boolean", Position: "15", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}}, {"SELECT -s FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: - text", Position: "8", Hint: "No operator matches the given name and argument type. You might need to add an explicit type cast."}}, {"SELECT i+'oops' FROM t", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "10"}}, {"SELECT $1,i+$1 FROM t", stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "8"}}} {
		_, err := db.Prepare(tc.q)
		arithmeticError(t, err, tc.d)
	}
	arithmeticExec(t, db, "CREATE TABLE dest(i INT,b BIGINT)", 0)
	arithmeticExec(t, db, "INSERT INTO dest VALUES(99,99)", 1)
	for _, q := range []string{"INSERT INTO dest VALUES(1,1),(2,7/0)", "INSERT INTO dest VALUES(1,1),(2,2147483647+1)"} {
		_, err := db.Exec(q)
		if err == nil {
			t.Fatal(q)
		}
		arithmeticRows(t, db, "SELECT i,b FROM dest", []string{"INT4", "INT8"}, [][]any{{int64(99), int64(99)}})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := db.ExecContext(ctx, "INSERT INTO dest VALUES(1+2,3)"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestArithmeticNativeControls(t *testing.T) {
	d, db := arithmeticDB(t)
	ctx := context.Background()
	arithmeticExec(t, db, "CREATE TABLE t(i INT,b BIGINT)", 0)
	arithmeticExec(t, db, "INSERT INTO t VALUES(7/2,-7%2),(NULL,NULL)", 2)
	raw, err := d.Open(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	cursor, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT i+1,b*2 FROM t ORDER BY i", nil)
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
	var again bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &again); err != nil || dump.String() != again.String() {
		t.Fatal(again.String(), err)
	}
	for _, script := range []string{"CREATE TABLE replacement(i INT); INSERT INTO replacement VALUES(1); INSERT INTO replacement VALUES(7/0)", "CREATE TABLE replacement(i INT); INSERT INTO replacement VALUES((7+$1)*2)"} {
		if err := stuntgres.Restore(ctx, db, strings.NewReader(script)); err == nil {
			t.Fatal("bad restore accepted")
		}
		arithmeticRows(t, db, "SELECT i,b FROM t WHERE i=3", []string{"INT4", "INT8"}, [][]any{{int64(3), int64(-1)}})
	}
	if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE replacement(i INT); INSERT INTO replacement VALUES((7+1)*2)")); err != nil {
		t.Fatal(err)
	}
	arithmeticRows(t, db, "SELECT i FROM replacement", []string{"INT4"}, [][]any{{int64(16)}})
	meta := cursor.(driver.RowsColumnTypeDatabaseTypeName)
	if meta.ColumnTypeDatabaseTypeName(0) != "INT4" || meta.ColumnTypeDatabaseTypeName(1) != "INT8" {
		t.Fatal("held metadata changed")
	}
	dst := make([]driver.Value, 2)
	for _, want := range [][]driver.Value{{int64(4), int64(-2)}, {nil, nil}} {
		if err := cursor.Next(dst); err != nil || !reflect.DeepEqual(dst, want) {
			t.Fatal(dst, err)
		}
	}
	if err := cursor.Next(dst); err != io.EOF {
		t.Fatal(err)
	}
}
