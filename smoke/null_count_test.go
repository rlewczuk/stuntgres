package smoke_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres"
)

func TestNullCountSmoke(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	connector, err := d.OpenConnector("null-count-smoke")
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
		if n, err := result.RowsAffected(); err != nil || n != want {
			t.Fatal(q, n, err)
		}
	}
	check := func(rows *sql.Rows, err error, want [][]any, arity int64) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		names, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(names, []string{"num_nulls", "num_nonnulls"}) {
			t.Fatal(names, err)
		}
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 2 {
			t.Fatal(types, err)
		}
		for _, typ := range types {
			if typ.DatabaseTypeName() != "INT4" || typ.ScanType() != reflect.TypeFor[int64]() {
				t.Fatal(typ)
			}
		}
		got := make([][]any, 0)
		for rows.Next() {
			var a, b any
			if err := rows.Scan(&a, &b); err != nil {
				t.Fatal(err)
			}
			x, xok := a.(int64)
			y, yok := b.(int64)
			if !xok || !yok {
				t.Fatal("nonnull int64 counts required", a, b)
			}
			if arity >= 0 && x+y != arity {
				t.Fatal("argument-count sum", x, y, arity)
			}
			got = append(got, []any{a, b})
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("independent counts", got, want)
		}
	}
	bad := func(q string, want stuntgres.Error, args ...any) {
		t.Helper()
		rows, err := db.QueryContext(ctx, q, args...)
		if rows != nil {
			rows.Close()
			t.Fatal("failed query exposed cursor", q)
		}
		if err := typeCoreDiagnostic(err, want); err != nil {
			t.Fatal(q, err)
		}
		stmt, err := db.PrepareContext(ctx, q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("failed prepare exposed handle", q)
		}
		if err := typeCoreDiagnostic(err, want); err != nil {
			t.Fatal(q, err)
		}
	}
	exec("CREATE TABLE t(id INT,v INT,b BIGINT,s TEXT,f BOOL)", 0)
	exec("INSERT INTO t VALUES(1,1,2147483648,'x',TRUE),(2,NULL,2,'',NULL),(3,NULL,NULL,NULL,NULL)", 3)
	// Valid table-backed call comes first, independent of FROM-less support.
	rows, err := db.QueryContext(ctx, "SELECT num_nulls(v,b,s,f),num_nonnulls(v,b,s,f) FROM t ORDER BY id")
	check(rows, err, [][]any{{int64(0), int64(4)}, {int64(2), int64(2)}, {int64(4), int64(0)}}, 4)
	for _, tc := range []struct {
		args        string
		a, b, arity int64
	}{
		{"NULL", 1, 0, 1}, {"1,NULL,2", 1, 2, 3}, {"NULL,NULL,NULL", 3, 0, 3},
		{"1,'',FALSE,0,2147483648", 0, 5, 5}, {"NULL,1,NULL,'x',NULL", 3, 2, 5},
	} {
		q := "SELECT num_nulls(" + tc.args + "),num_nonnulls(" + tc.args + ")"
		rows, err := db.QueryContext(ctx, q)
		check(rows, err, [][]any{{tc.a, tc.b}}, tc.arity)
		stmt, err := db.PrepareContext(ctx, q)
		if err != nil {
			t.Fatal(q, err)
		}
		rows, err = stmt.QueryContext(ctx)
		check(rows, err, [][]any{{tc.a, tc.b}}, tc.arity)
		stmt.Close()
	}
	rows, err = db.QueryContext(ctx, "SELECT num_nulls(num_nulls(NULL),NULL),num_nonnulls(num_nonnulls(NULL),NULL)")
	check(rows, err, [][]any{{int64(1), int64(1)}}, 2)
	held, err := db.PrepareContext(ctx, "SELECT num_nulls(v,b,s,f),num_nonnulls(v,b,s,f) FROM t WHERE id=$1")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, id := range []int{1, 2, 3, 1, 99} {
		want := [][]any{{int64(0), int64(4)}}
		if id == 2 {
			want = [][]any{{int64(2), int64(2)}}
		}
		if id == 3 {
			want = [][]any{{int64(4), int64(0)}}
		}
		if id == 99 {
			want = [][]any{}
		}
		rows, err := held.QueryContext(ctx, id)
		check(rows, err, want, 4)
	}
	exec("SELECT num_nulls(NULL),num_nonnulls(1,NULL,'x')", 1)
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	bad("SELECT num_nulls()", stuntgres.Error{Code: "42883", Message: "function num_nulls() does not exist", Position: "8", Hint: hint})
	bad("SELECT num_nonnulls()", stuntgres.Error{Code: "42883", Message: "function num_nonnulls() does not exist", Position: "8", Hint: hint})
	bad("SELECT num_nulls(VARIADIC 1)", stuntgres.Error{Code: "42804", Message: "VARIADIC argument must be an array", Position: "27"})
	bad("SELECT num_nonnulls(VARIADIC 1)", stuntgres.Error{Code: "42804", Message: "VARIADIC argument must be an array", Position: "30"})
	for _, name := range []string{"num_nulls", "num_nonnulls"} {
		for _, arg := range []any{nil, 1, "x"} {
			bad("SELECT "+name+"($1)", stuntgres.Error{Code: "42P18", Message: "could not determine data type of parameter $1"}, arg)
		}
	}
	bad("SELECT num_nulls($1) FROM t WHERE v=$1", stuntgres.Error{Code: "42P08", Message: "could not determine data type of parameter $1", Position: "18"}, 1)
	for _, q := range []string{"SELECT 1", "SELECT num_nulls(NULL),1", "SELECT num_nulls(VARIADIC ARRAY[1,NULL])", "SELECT num_nulls(NULL) AS n FROM t", "SELECT id FROM t WHERE num_nulls(v)", "INSERT INTO t VALUES(4,num_nulls(NULL),1,'x',TRUE)"} {
		rows, err := db.QueryContext(ctx, q)
		if rows != nil {
			rows.Close()
			t.Fatal("excluded query exposed cursor", q)
		}
		if err == nil {
			t.Fatal("excluded syntax accepted", q)
		}
		stmt, err := db.PrepareContext(ctx, q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("excluded prepare exposed handle", q)
		}
		if err == nil {
			t.Fatal("excluded prepare accepted", q)
		}
	}
}
