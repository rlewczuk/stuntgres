package stuntgres_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"sync"
	"testing"
)

func TestExpressionCoreProjectionMetadataAndPreparedFrames(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "expression-core")
	execSQL(t, db, "CREATE TABLE t(id INT,v INT,b BIGINT,s TEXT,f BOOL)")
	execSQL(t, db, "INSERT INTO t VALUES(1,1,2147483648,'x',TRUE),(2,NULL,2,'x',NULL),(3,2,3,'y',FALSE),(4,1,4,'x',TRUE)")
	wantTuples(t, db, "SELECT 7 FROM t WHERE v=1", [][]any{{int64(7)}, {int64(7)}})
	wantTuples(t, db, "SELECT id FROM t WHERE v=2147483648", [][]any{})
	wantTuples(t, db, "SELECT v FROM t WHERE v=$1", [][]any{}, nil)
	q := "SELECT id,1,2147483648,'x',TRUE,FALSE,NULL FROM t ORDER BY id"
	rows, err := db.Query(q)
	if err != nil {
		t.Fatal(err)
	}
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, []string{"id", "?column?", "?column?", "?column?", "?column?", "?column?", "?column?"}) {
		t.Fatal(names, err)
	}
	types, err := rows.ColumnTypes()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	scans := []reflect.Type{reflect.TypeFor[int64](), reflect.TypeFor[int64](), reflect.TypeFor[int64](), reflect.TypeFor[string](), reflect.TypeFor[bool](), reflect.TypeFor[bool](), reflect.TypeFor[string]()}
	for i, name := range []string{"INT4", "INT4", "INT8", "TEXT", "BOOL", "BOOL", "TEXT"} {
		if types[i].DatabaseTypeName() != name || types[i].ScanType() != scans[i] {
			t.Fatal(types[i])
		}
		_, known := types[i].Nullable()
		if i > 0 && known {
			t.Fatal("scalar nullability invented", types[i])
		}
	}
	want := [][]any{}
	for i := range 4 {
		want = append(want, []any{int64(i + 1), int64(1), int64(2147483648), "x", true, false, nil})
	}
	wantTuples(t, db, q, want)
	stmt, err := db.Prepare("SELECT id FROM t WHERE v=$1 ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for i, arg := range []any{1, 2, nil} {
		want := [][]any{{int64(1)}, {int64(4)}}
		if i == 1 {
			want = [][]any{{int64(3)}}
		}
		if i == 2 {
			want = [][]any{}
		}
		rows, err := stmt.Query(arg)
		if err != nil {
			t.Fatal(err)
		}
		types, err := rows.ColumnTypes()
		if err != nil || types[0].DatabaseTypeName() != "INT4" {
			t.Fatal(types, err)
		}
		got := [][]any{}
		for rows.Next() {
			var v any
			if err := rows.Scan(&v); err != nil {
				t.Fatal(err)
			}
			got = append(got, []any{v})
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if !reflect.DeepEqual(got, want) {
			t.Fatal(got, want)
		}
	}
	var wg sync.WaitGroup
	for n := range 16 {
		wg.Go(func() {
			arg := 1
			if n%2 != 0 {
				arg = 2
			}
			rows, err := stmt.Query(arg)
			if err != nil {
				t.Error(err)
				return
			}
			defer rows.Close()
			got := []int64{}
			for rows.Next() {
				var v int64
				if err := rows.Scan(&v); err != nil {
					t.Error(err)
				}
				got = append(got, v)
			}
			want := []int64{1, 4}
			if arg == 2 {
				want = []int64{3}
			}
			if rows.Err() != nil || !reflect.DeepEqual(got, want) {
				t.Error(got, rows.Err())
			}
		})
	}
	wg.Wait()
	wantTuples(t, db, "SELECT $1 FROM t WHERE s=$1 ORDER BY id", [][]any{{"x"}, {"x"}, {"x"}}, "x")
	wantTuples(t, db, "SELECT id FROM t WHERE $1 ORDER BY id", [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}, true)
	wantTuples(t, db, "SELECT $1,$1 FROM t ORDER BY id", [][]any{{nil, nil}, {nil, nil}, {nil, nil}, {nil, nil}}, nil)
	for _, tc := range []struct {
		q, detail string
		arg       any
	}{
		{"SELECT $1 FROM t WHERE v=$1 ORDER BY id", "integer versus text", 1},
		{"SELECT $1 FROM t WHERE f=$1 ORDER BY id", "boolean versus text", true},
	} {
		want := stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: tc.detail, Position: "8"}
		rows, err := db.Query(tc.q, tc.arg)
		if rows != nil {
			rows.Close()
			t.Fatal("failed query exposed cursor")
		}
		typeCoreDiagnostic(t, err, want)
		stmt, err := db.Prepare(tc.q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("failed prepare exposed handle")
		}
		typeCoreDiagnostic(t, err, want)
	}
}

func TestExpressionCoreDiagnosticsAndCancellation(t *testing.T) {
	d := stuntgres.NewDriver()
	db := openLocal(t, d, "expression-errors")
	execSQL(t, db, "CREATE TABLE t(id INT,s TEXT,f BOOL)")
	for _, tc := range []struct {
		q    string
		want stuntgres.Error
	}{
		{"SELECT missing FROM t", stuntgres.Error{Code: "42703", Message: `column "missing" does not exist`, Position: "8"}},
		{"SELECT /*é*/ missing FROM t", stuntgres.Error{Code: "42703", Message: `column "missing" does not exist`, Position: "14"}},
		{"SELECT id FROM t WHERE missing=1", stuntgres.Error{Code: "42703", Message: `column "missing" does not exist`, Position: "24"}},
		{"SELECT id FROM t WHERE 1", stuntgres.Error{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "24"}},
		{"SELECT id FROM t WHERE s", stuntgres.Error{Code: "42804", Message: "argument of WHERE must be type boolean, not type text", Position: "24"}},
		{"SELECT id FROM t WHERE id=TRUE", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer = boolean", Position: "26", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
		{"SELECT id FROM t WHERE 'truth'", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`, Position: "24"}},
		{"SELECT $0 FROM t", stuntgres.Error{Code: "42P02", Message: "there is no parameter $0", Position: "8"}},
		{"SELECT $2 FROM t", stuntgres.Error{Code: "42P18", Message: "could not determine data type of parameter $1"}},
		{"SELECT $1 FROM t WHERE id=$1 ORDER BY id", stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "8"}},
		{"SELECT $1 FROM t WHERE f=$1 ORDER BY id", stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "boolean versus text", Position: "8"}},
		{"SELECT $1 FROM t WHERE $1", stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "boolean versus text", Position: "8"}},
	} {
		rows, err := db.Query(tc.q)
		if rows != nil {
			rows.Close()
			t.Fatal("failed query exposed cursor")
		}
		typeCoreDiagnostic(t, err, tc.want)
		stmt, err := db.Prepare(tc.q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("failed prepare exposed handle")
		}
		typeCoreDiagnostic(t, err, tc.want)
	}
	stmt, err := db.Prepare("SELECT id FROM t WHERE $1")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	rows, err := stmt.Query("truth")
	if rows != nil {
		rows.Close()
		t.Fatal("parameter failed late")
	}
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`, Context: "unnamed portal parameter $1 = '...'"})
	if _, err := stmt.Query(); err == nil {
		t.Fatal("missing actual args accepted")
	} else {
		var d *stuntgres.Error
		if errors.As(err, &d) {
			t.Fatal("native count acquired SQLSTATE", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := db.QueryContext(ctx, "SELECT id FROM t"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	execSQL(t, db, "INSERT INTO t VALUES(1,'x',TRUE)")
	raw, err := d.Open("expression-errors")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	ctx, cancel = context.WithCancel(context.Background())
	cursor, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT 7 FROM t", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	cancel()
	if err := cursor.Next(make([]driver.Value, 1)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestExpressionCoreCapturedProjectionAndRestoreAtomicity(t *testing.T) {
	d := stuntgres.NewDriver()
	db := openLocal(t, d, "expression-capture")
	execSQL(t, db, "CREATE TABLE t(id INT NOT NULL)")
	execSQL(t, db, "INSERT INTO t VALUES(1),(2)")
	raw, err := d.Open("expression-capture")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	owned := []byte("before")
	cursor, err := raw.(driver.QueryerContext).QueryContext(context.Background(), "SELECT id,$1,NULL FROM t ORDER BY id", []driver.NamedValue{{Ordinal: 1, Value: owned}})
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	owned[0] = 'X'
	execSQL(t, db, "INSERT INTO t VALUES(3)")
	if err := stuntgres.Reset(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(context.Background(), db, strings.NewReader("CREATE TABLE t(replacement TEXT); INSERT INTO t VALUES('after')")); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cursor.Columns(), []string{"id", "?column?", "?column?"}) {
		t.Fatal(cursor.Columns())
	}
	meta := cursor.(driver.RowsColumnTypeDatabaseTypeName)
	for i, name := range []string{"INT4", "TEXT", "TEXT"} {
		if meta.ColumnTypeDatabaseTypeName(i) != name {
			t.Fatal("metadata replaced")
		}
	}
	dst := make([]driver.Value, 3)
	for i := range 2 {
		if err := cursor.Next(dst); err != nil || !reflect.DeepEqual(dst, []driver.Value{int64(i + 1), "before", nil}) {
			t.Fatal(dst, err)
		}
	}
	if err := cursor.Next(dst); err != io.EOF {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT replacement FROM t", [][]any{{"after"}})
	err = stuntgres.Restore(context.Background(), db, strings.NewReader("CREATE TABLE replacement(i INT); INSERT INTO replacement VALUES($1)"))
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P02", Message: "parameters are not allowed in restore", Position: "65"})
	wantTuples(t, db, "SELECT replacement FROM t", [][]any{{"after"}})
}
