// Public null-count acceptance witnesses; no private implementation types are required.
package stuntgres_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
)

// Counts and metadata are independently specified, not inferred from another backend.
func nullCountRows(t *testing.T, rows *sql.Rows, err error, want [][]any, arity int64) {
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
			t.Fatalf("nonnull int64 counts required: %T %T", a, b)
		}
		if arity >= 0 && x+y != arity {
			t.Fatalf("sum %d + %d != %d", x, y, arity)
		}
		got = append(got, []any{a, b})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("counts got %v want %v", got, want)
	}
}

func TestNullCountLiterals(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "null-count-literals")
	execSQL(t, db, "CREATE TABLE t(id INT)")
	execSQL(t, db, "INSERT INTO t VALUES(1)")
	// First valid call is table-backed: failing-before is not just no-FROM rejection.
	rows, err := db.Query("SELECT num_nulls(1,NULL,2),num_nonnulls(1,NULL,2) FROM t")
	nullCountRows(t, rows, err, [][]any{{int64(1), int64(2)}}, 3)
	for _, tc := range []struct {
		args        string
		a, b, arity int64
	}{
		{"NULL", 1, 0, 1}, {"1,NULL,2", 1, 2, 3}, {"NULL,NULL,NULL", 3, 0, 3},
		{"1,'',FALSE,0,2147483648", 0, 5, 5}, {"NULL,1,NULL,'x',NULL", 3, 2, 5},
		{strings.TrimSuffix(strings.Repeat("NULL,", 100), ","), 100, 0, 100},
		{strings.TrimSuffix(strings.Repeat("1,", 100), ","), 0, 100, 100},
	} {
		q := "SELECT num_nulls(" + tc.args + "),num_nonnulls(" + tc.args + ")"
		t.Run(tc.args, func(t *testing.T) {
			want := [][]any{{tc.a, tc.b}}
			rows, err := db.Query(q)
			nullCountRows(t, rows, err, want, tc.arity)
			stmt, err := db.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			rows, err = stmt.Query()
			nullCountRows(t, rows, err, want, tc.arity)
		})
	}
	q := "SELECT num_nulls(num_nulls(NULL),NULL),num_nonnulls(num_nonnulls(NULL),NULL)"
	rows, err = db.Query(q)
	nullCountRows(t, rows, err, [][]any{{int64(1), int64(1)}}, 2)
	stmt, err := db.Prepare(q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	rows, err = stmt.Query()
	nullCountRows(t, rows, err, [][]any{{int64(1), int64(1)}}, 2)
	result, err := db.Exec("SELECT num_nulls(NULL),num_nonnulls(1,NULL,'x')")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		t.Fatal(n, err)
	}
}

func TestNullCountColumns(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "null-count-columns")
	execSQL(t, db, "CREATE TABLE t(id INT,v INT,b BIGINT,s TEXT,f BOOL)")
	execSQL(t, db, "INSERT INTO t VALUES(1,1,2147483648,'x',TRUE),(2,NULL,2,'',NULL),(3,NULL,NULL,NULL,NULL),(4,1,2147483648,'x',TRUE)")
	q := "SELECT num_nulls(v,b,s,f),num_nonnulls(v,b,s,f) FROM t"
	want := [][]any{{int64(0), int64(4)}, {int64(2), int64(2)}, {int64(4), int64(0)}, {int64(0), int64(4)}}
	rows, err := db.Query(q + " ORDER BY id")
	nullCountRows(t, rows, err, want, 4)
	stmt, err := db.Prepare(q + " WHERE id=$1")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, id := range []int{1, 2, 3, 1, 99} {
		w := [][]any{}
		if id != 99 {
			w = [][]any{want[id-1]}
		}
		rows, err := stmt.Query(id)
		nullCountRows(t, rows, err, w, 4)
	}
	rows, err = db.Query("SELECT num_nulls(NULL),num_nonnulls(NULL) FROM t ORDER BY id")
	nullCountRows(t, rows, err, [][]any{{int64(1), int64(0)}, {int64(1), int64(0)}, {int64(1), int64(0)}, {int64(1), int64(0)}}, 1)
	execSQL(t, db, "CREATE TABLE empty(v INT)")
	rows, err = db.Query("SELECT num_nulls(v),num_nonnulls(v) FROM empty")
	nullCountRows(t, rows, err, [][]any{}, 1)
	execSQL(t, db, "CREATE TABLE baseline(id INT,n NUMERIC,r REAL,d DOUBLE PRECISION,tm TIME,ts TIMESTAMP,u UUID)")
	execSQL(t, db, "INSERT INTO baseline VALUES(1,1.5,1.5,2.5,'12:34:56','2000-01-02 03:04:05','00000000-0000-0000-0000-000000000001'),(2,NULL,NULL,NULL,NULL,NULL,NULL)")
	q = "SELECT num_nulls(n,r,d,tm,ts,u),num_nonnulls(n,r,d,tm,ts,u) FROM baseline ORDER BY id"
	w := [][]any{{int64(0), int64(6)}, {int64(6), int64(0)}}
	rows, err = db.Query(q)
	nullCountRows(t, rows, err, w, 6)
	other, err := db.Prepare(q)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	rows, err = other.Query()
	nullCountRows(t, rows, err, w, 6)
}

func nullCountError(t *testing.T, db *sql.DB, q string, want stuntgres.Error, args ...any) {
	t.Helper()
	rows, err := db.Query(q, args...)
	if rows != nil {
		rows.Close()
		t.Fatal("failed analysis exposed cursor", q)
	}
	typeCoreDiagnostic(t, err, want)
	stmt, err := db.Prepare(q)
	if stmt != nil {
		stmt.Close()
		t.Fatal("failed analysis exposed handle", q)
	}
	typeCoreDiagnostic(t, err, want)
}

func TestNullCountDiagnostics(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "null-count-diagnostics")
	execSQL(t, db, "CREATE TABLE t(v INT)")
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, tc := range []struct {
		q    string
		want stuntgres.Error
	}{
		{"SELECT num_nulls()", stuntgres.Error{Code: "42883", Message: "function num_nulls() does not exist", Position: "8", Hint: hint}},
		{"SELECT num_nonnulls()", stuntgres.Error{Code: "42883", Message: "function num_nonnulls() does not exist", Position: "8", Hint: hint}},
		{"SELECT /*é*/ num_nulls()", stuntgres.Error{Code: "42883", Message: "function num_nulls() does not exist", Position: "14", Hint: hint}},
		{"SELECT num_nulls() FROM t", stuntgres.Error{Code: "42883", Message: "function num_nulls() does not exist", Position: "8", Hint: hint}},
		{"SELECT num_nulls(VARIADIC 1)", stuntgres.Error{Code: "42804", Message: "VARIADIC argument must be an array", Position: "27"}},
		{"SELECT num_nonnulls(VARIADIC 1)", stuntgres.Error{Code: "42804", Message: "VARIADIC argument must be an array", Position: "30"}},
		{"SELECT num_nulls(VARIADIC NULL)", stuntgres.Error{Code: "42804", Message: "VARIADIC argument must be an array", Position: "27"}},
		{"SELECT num_nulls(VARIADIC 'x')", stuntgres.Error{Code: "42804", Message: "VARIADIC argument must be an array", Position: "27"}},
		{"SELECT num_nulls(1,VARIADIC 2)", stuntgres.Error{Code: "42883", Message: "function num_nulls(integer, integer) does not exist", Position: "8", Hint: hint}},
		{"SELECT num_nulls($0)", stuntgres.Error{Code: "42P02", Message: "there is no parameter $0", Position: "18"}},
		{"SELECT num_nulls(1,)", stuntgres.Error{Code: "42601", Message: `syntax error at or near ")"`, Position: "20"}},
		{"SELECT num_nulls(", stuntgres.Error{Code: "42601", Message: "syntax error at end of input", Position: "18"}},
		{"SELECT num_nulls(" + strings.TrimSuffix(strings.Repeat("NULL,", 101), ",") + ")", stuntgres.Error{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}},
		{"SELECT num_nulls(missing) FROM t", stuntgres.Error{Code: "42703", Message: `column "missing" does not exist`, Position: "18"}},
	} {
		t.Run(tc.q, func(t *testing.T) { nullCountError(t, db, tc.q, tc.want) })
	}
}

func TestNullCountArgumentErrorPrecedence(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "null-count-argument-precedence")
	execSQL(t, db, "CREATE TABLE t(v INT)")
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	tail := strings.TrimSuffix(strings.Repeat("NULL,", 100), ",")
	for _, name := range []string{"num_nulls", "num_nonnulls"} {
		position, lastPosition := "18", "518"
		if name == "num_nonnulls" {
			position, lastPosition = "21", "521"
		}
		for _, tc := range []struct {
			label, operands string
			want            stuntgres.Error
			args            []any
		}{
			{"parameter", "$0," + tail, stuntgres.Error{Code: "42P02", Message: "there is no parameter $0", Position: position}, nil},
			{"last-parameter", strings.Repeat("NULL,", 100) + "$0", stuntgres.Error{Code: "42P02", Message: "there is no parameter $0", Position: lastPosition}, nil},
			{"column", "missing," + tail, stuntgres.Error{Code: "42703", Message: `column "missing" does not exist`, Position: position}, nil},
			{"nested-call", "num_nulls()," + tail, stuntgres.Error{Code: "42883", Message: "function num_nulls() does not exist", Position: position, Hint: hint}, nil},
			// Unknown ANY parameters are transformed successfully; the outer
			// arity error precedes later whole-statement slot finalization.
			{"unresolved-parameter", "$1," + tail, stuntgres.Error{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}, []any{1}},
		} {
			t.Run(name+"/"+tc.label, func(t *testing.T) {
				q := "SELECT " + name + "(" + tc.operands + ") FROM t"
				nullCountError(t, db, q, tc.want, tc.args...)
			})
		}
	}
}

func TestNullCountParameters(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "null-count-parameters")
	execSQL(t, db, "CREATE TABLE t(v INT,s TEXT)")
	for _, name := range []string{"num_nulls", "num_nonnulls"} {
		q := "SELECT " + name + "($1)"
		want := stuntgres.Error{Code: "42P18", Message: "could not determine data type of parameter $1"}
		for _, arg := range []any{nil, 1, "x"} {
			nullCountError(t, db, q, want, arg)
		}
		nullCountError(t, db, q, want)
	}
	for _, tc := range []struct{ q, pos string }{
		{"SELECT num_nulls($1),$1 FROM t", "18"},
		{"SELECT num_nulls($1) FROM t WHERE v=$1", "18"},
		{"SELECT $1,num_nulls($1) FROM t", "21"},
	} {
		nullCountError(t, db, tc.q, stuntgres.Error{Code: "42P08", Message: "could not determine data type of parameter $1", Position: tc.pos}, 1)
	}
	// Existing occurrence-sensitive target behavior must survive the new ANY path.
	nullCountError(t, db, "SELECT $1 FROM t WHERE v=$1", stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "8"}, 1)
}

func TestNullCountCapabilityBoundaries(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "null-count-boundaries")
	execSQL(t, db, "CREATE TABLE t(num_nulls INT,num_nonnulls INT)")
	execSQL(t, db, "INSERT INTO t VALUES(1,2)")
	wantTuples(t, db, "SELECT num_nulls,num_nonnulls FROM t", [][]any{{int64(1), int64(2)}})
	// Valid PostgreSQL features excluded by the bounded native surface are not parity claims.
	for _, q := range []string{
		"SELECT 1", "SELECT num_nulls(NULL),1", "SELECT $1", "SELECT *",
		"SELECT num_nulls(NULL) WHERE TRUE", "SELECT num_nulls(NULL) ORDER BY 1",
		"INSERT INTO t VALUES(num_nulls(NULL),1)", "SELECT num_nulls FROM t WHERE num_nulls(NULL)",
		"SELECT abs(1) FROM t", "SELECT num_nulls(NULL)::int FROM t", "SELECT num_nulls(ARRAY[1,NULL])",
		"SELECT num_nulls(VARIADIC ARRAY[1,NULL])", "SELECT num_nulls(NULL) AS n FROM t",
		"SELECT pg_catalog.num_nulls(NULL) FROM t", "SELECT num_nulls(DISTINCT 1) FROM t",
		"SELECT num_nulls(1 ORDER BY 1) FROM t", "SELECT num_nulls(*) FROM t",
		"SELECT num_nulls(1) FILTER (WHERE TRUE) FROM t", "SELECT num_nulls(1) OVER () FROM t",
		"SELECT num_nulls(a => 1) FROM t", "SELECT current_setting(num_nulls(NULL))",
	} {
		rows, err := db.Query(q)
		if rows != nil {
			rows.Close()
			t.Fatal("excluded form exposed cursor", q)
		}
		if err == nil {
			t.Fatal("excluded form accepted", q)
		}
		stmt, err := db.Prepare(q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("excluded form exposed handle", q)
		}
		if err == nil {
			t.Fatal("excluded prepare accepted", q)
		}
	}
}

func TestNullCountCapturedResults(t *testing.T) {
	d := stuntgres.NewDriver()
	db := openLocal(t, d, "null-count-capture")
	execSQL(t, db, "CREATE TABLE t(id INT,v INT)")
	execSQL(t, db, "INSERT INTO t VALUES(1,NULL),(2,1)")
	raw, err := d.Open("null-count-capture")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	cursor, err := raw.(driver.QueryerContext).QueryContext(context.Background(), "SELECT num_nulls(v),num_nonnulls(v) FROM t ORDER BY id", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	execSQL(t, db, "INSERT INTO t VALUES(3,NULL)")
	if err := stuntgres.Reset(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cursor.Columns(), []string{"num_nulls", "num_nonnulls"}) {
		t.Fatal(cursor.Columns())
	}
	meta := cursor.(driver.RowsColumnTypeDatabaseTypeName)
	if meta.ColumnTypeDatabaseTypeName(0) != "INT4" || meta.ColumnTypeDatabaseTypeName(1) != "INT4" {
		t.Fatal("captured metadata changed")
	}
	dst := make([]driver.Value, 2)
	for _, want := range [][]driver.Value{{int64(1), int64(0)}, {int64(0), int64(1)}} {
		if err := cursor.Next(dst); err != nil || !reflect.DeepEqual(dst, want) {
			t.Fatal(dst, want, err)
		}
	}
	if err := cursor.Next(dst); err != io.EOF {
		t.Fatal(err)
	}
}
