package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"testing"

	"stuntgres"
)

func TestCompareOpsSmoke(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	connector, err := d.OpenConnector("compare-smoke")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	exec := func(q string, want int64) {
		t.Helper()
		result, err := db.ExecContext(ctx, q)
		if err != nil {
			t.Fatal(q, err)
		}
		got, err := result.RowsAffected()
		if err != nil || got != want {
			t.Fatal(q, got, err)
		}
	}
	exec("CREATE TABLE t(id INTEGER,i INTEGER,j BIGINT,s TEXT,b BOOLEAN)", 0)
	exec("INSERT INTO t VALUES(1,2147483647,2147483648,'a',FALSE),(2,-2147483648,-2147483649,'z',TRUE),(3,NULL,NULL,NULL,NULL),(4,7,7,'a',FALSE)", 4)
	check := func(rows *sql.Rows, err error, types []string, want [][]any) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		meta, err := rows.ColumnTypes()
		if err != nil || len(meta) != len(types) {
			t.Fatal(meta, err)
		}
		for i, typ := range types {
			if meta[i].DatabaseTypeName() != typ || typ == "BOOL" && (meta[i].Name() != "?column?" || meta[i].ScanType() != reflect.TypeFor[bool]()) {
				t.Fatal(meta[i])
			}
		}
		got := [][]any{}
		for rows.Next() {
			values, dst := make([]any, len(types)), make([]any, len(types))
			for i := range values {
				dst[i] = &values[i]
			}
			if err := rows.Scan(dst...); err != nil {
				t.Fatal(err)
			}
			got = append(got, values)
		}
		if err := rows.Err(); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal(got, want, err)
		}
	}
	for _, tc := range []struct {
		op     string
		values []any
		ids    []int64
	}{
		{"=", []any{false, false, nil, true}, []int64{4}}, {"<>", []any{true, true, nil, false}, []int64{1, 2}}, {"!=", []any{true, true, nil, false}, []int64{1, 2}},
		{"<", []any{true, false, nil, false}, []int64{1}}, {"<=", []any{true, false, nil, true}, []int64{1, 4}}, {">", []any{false, true, nil, false}, []int64{2}}, {">=", []any{false, true, nil, true}, []int64{2, 4}},
	} {
		want := [][]any{}
		for _, v := range tc.values {
			want = append(want, []any{v})
		}
		rows, err := db.QueryContext(ctx, "SELECT i"+tc.op+"j FROM t ORDER BY id")
		check(rows, err, []string{"BOOL"}, want)
		want = [][]any{}
		for _, id := range tc.ids {
			want = append(want, []any{id})
		}
		rows, err = db.QueryContext(ctx, "SELECT id FROM t WHERE i"+tc.op+"j ORDER BY id")
		check(rows, err, []string{"INT4"}, want)
		rows, err = db.QueryContext(ctx, "SELECT NULL"+tc.op+"NULL FROM t")
		check(rows, err, []string{"BOOL"}, [][]any{{nil}, {nil}, {nil}, {nil}})
	}
	for _, tc := range []struct {
		q     string
		types []string
		want  [][]any
		args  []any
	}{
		{"SELECT -9223372036854775808<-2147483648,9223372036854775807>2147483647,9007199254740993>9007199254740992 FROM t", []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}, {true, true, true}, {true, true, true}, {true, true, true}}, nil},
		{"SELECT 'a'<'z',FALSE<TRUE,NULL<'x' FROM t", []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, nil}, {true, true, nil}, {true, true, nil}, {true, true, nil}}, nil},
		{"SELECT i<j FROM t WHERE NULL<>NULL", []string{"BOOL"}, [][]any{}, nil},
		{"SELECT $1<$2 FROM t", []string{"BOOL"}, [][]any{{true}, {true}, {true}, {true}}, []any{"10", "2"}},
		{"SELECT i=$1,j=$1 FROM t ORDER BY id", []string{"BOOL", "BOOL"}, [][]any{{false, false}, {false, false}, {nil, nil}, {true, true}}, []any{7}},
	} {
		rows, err := db.QueryContext(ctx, tc.q, tc.args...)
		check(rows, err, tc.types, tc.want)
		stmt, err := db.PrepareContext(ctx, tc.q)
		if err != nil {
			t.Fatal(err)
		}
		rows, err = stmt.QueryContext(ctx, tc.args...)
		check(rows, err, tc.types, tc.want)
		stmt.Close()
	}
	held, err := db.PrepareContext(ctx, "SELECT $1<i FROM t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, arg := range []any{7, nil, 7} {
		want := [][]any{{true}, {false}, {nil}, {false}}
		if arg == nil {
			want = [][]any{{nil}, {nil}, {nil}, {nil}}
		}
		rows, err := held.QueryContext(ctx, arg)
		check(rows, err, []string{"BOOL"}, want)
	}
	const hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
	for _, tc := range []struct {
		q    string
		want stuntgres.Error
		args []any
	}{
		{"SELECT i<TRUE FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer < boolean", Position: "9", Hint: hint}, nil},
		{"SELECT s!=b FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: text <> boolean", Position: "9", Hint: hint}, nil},
		{"SELECT i<'oops' FROM t", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "10"}, nil},
		{"SELECT $1,i=$1 FROM t", stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Position: "8", Detail: "integer versus text"}, []any{7}},
		{"SELECT i=$1,b=$1 FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: boolean = integer", Position: "14", Hint: hint}, []any{7}},
	} {
		rows, err := db.QueryContext(ctx, tc.q, tc.args...)
		if rows != nil {
			rows.Close()
			t.Fatal("error exposed cursor")
		}
		if err := typeCoreDiagnostic(err, tc.want); err != nil {
			t.Fatal(err)
		}
		stmt, err := db.PrepareContext(ctx, tc.q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("error exposed statement")
		}
		if err := typeCoreDiagnostic(err, tc.want); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := held.QueryContext(ctx, "oops")
	if rows != nil {
		rows.Close()
		t.Fatal("bad parameter exposed cursor")
	}
	if err := typeCoreDiagnostic(err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}); err != nil {
		t.Fatal(err)
	}
	// Capture before fetching, then replace storage through public native controls.
	raw, err := d.Open("compare-smoke")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	cursor, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT i<j FROM t ORDER BY id", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	exec("INSERT INTO t VALUES(5,9,10,'later',TRUE)", 1)
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
	if !reflect.DeepEqual(cursor.Columns(), []string{"?column?"}) || cursor.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0) != "BOOL" {
		t.Fatal("captured metadata changed")
	}
	dst := make([]driver.Value, 1)
	for _, want := range []driver.Value{true, false, nil, false} {
		if err := cursor.Next(dst); err != nil || !reflect.DeepEqual(dst, []driver.Value{want}) {
			t.Fatal(dst, want, err)
		}
	}
	if err := cursor.Next(dst); err != io.EOF {
		t.Fatal(err)
	}
	rows, err = db.QueryContext(ctx, "SELECT id FROM t WHERE i<j ORDER BY id")
	check(rows, err, []string{"INT4"}, [][]any{{int64(1)}, {int64(5)}})
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := db.QueryContext(canceled, "SELECT i<j FROM t"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
