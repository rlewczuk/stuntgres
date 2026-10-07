package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres"
)

// TestConditionalSmoke exercises the public SQL and native control surfaces.
// Expectations are independent of the implementation and PostgreSQL comparison.
func TestConditionalSmoke(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	connector, err := d.OpenConnector("conditional-smoke")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	exec := func(q string, want int64) {
		t.Helper()
		r, err := db.ExecContext(ctx, q)
		if err != nil {
			t.Fatal(q, err)
		}
		got, err := r.RowsAffected()
		if err != nil || got != want {
			t.Fatal(q, got, want, err)
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
		metadata, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, typ := range types {
			if metadata[i].DatabaseTypeName() != typ {
				t.Fatalf("column %d type %s, want %s", i, metadata[i].DatabaseTypeName(), typ)
			}
		}
		got := make([][]any, 0)
		for rows.Next() {
			values := make([]any, len(names))
			dest := make([]any, len(values))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			got = append(got, values)
		}
		if err := rows.Err(); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal(got, want, err)
		}
	}
	exec("CREATE TABLE t(id INTEGER,v INTEGER,b BOOLEAN,s TEXT)", 0)
	exec("INSERT INTO t VALUES(1,0,TRUE,'x'),(2,2,FALSE,NULL),(3,NULL,NULL,'y')", 3)
	cases := []struct {
		q            string
		names, types []string
		want         [][]any
		args         []any
	}{
		{"SELECT id,CASE WHEN b THEN 10 WHEN v=2 THEN 20 ELSE 30 END,CASE v WHEN 0 THEN 'zero' WHEN 2 THEN 'two' ELSE 'other' END,CASE WHEN b THEN 1 END FROM t ORDER BY id", []string{"id", "case", "case", "case"}, []string{"INT4", "INT4", "TEXT", "INT4"}, [][]any{{int64(1), int64(10), "zero", int64(1)}, {int64(2), int64(20), "two", nil}, {int64(3), int64(30), "other", nil}}, nil},
		{"SELECT COALESCE(s,'fallback'),COALESCE(NULL,NULL),GREATEST(v,NULL,2),LEAST(v,NULL,2),NULLIF(v,0) FROM t ORDER BY id", []string{"coalesce", "coalesce", "greatest", "least", "nullif"}, []string{"TEXT", "TEXT", "INT4", "INT4", "INT4"}, [][]any{{"x", nil, int64(2), int64(0), nil}, {"fallback", nil, int64(2), int64(2), int64(2)}, {"y", nil, int64(2), int64(2), nil}}, nil},
		{"SELECT NULLIF(1,2147483648),NULLIF(1,2.2),CASE WHEN b THEN 1 ELSE 2147483648 END FROM t WHERE id=1", []string{"nullif", "nullif", "case"}, []string{"INT4", "NUMERIC", "INT8"}, [][]any{{int64(1), "1", int64(1)}}, nil},
		{"SELECT CASE WHEN v=0 THEN 0 ELSE 10/v END,COALESCE(v,10/v) FROM t ORDER BY id", []string{"case", "coalesce"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(0)}, {int64(5), int64(2)}, {nil, nil}}, nil},
		{"SELECT CASE WHEN b THEN 1 ELSE CAST(CAST($1 AS TEXT) AS INTEGER) END,COALESCE(v,CAST(CAST($1 AS TEXT) AS INTEGER)) FROM t WHERE id=1", []string{"case", "coalesce"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(0)}}, []any{"bad"}},
		{"SELECT CASE WHEN TRUE THEN 1 ELSE 1/0 END,CASE WHEN FALSE THEN 1/0 ELSE 1 END,COALESCE(1,1/0) FROM t WHERE id=1", []string{"case", "case", "coalesce"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(1)}}, nil},
	}
	for _, tc := range cases {
		rows, err := db.QueryContext(ctx, tc.q, tc.args...)
		check(rows, err, tc.names, tc.types, tc.want)
		stmt, err := db.PrepareContext(ctx, tc.q)
		if err != nil {
			t.Fatal(tc.q, err)
		}
		rows, err = stmt.QueryContext(ctx, tc.args...)
		check(rows, err, tc.names, tc.types, tc.want)
		stmt.Close()
	}
	exec("CREATE TABLE mixed(b INTEGER,e REAL)", 0)
	exec("INSERT INTO mixed VALUES(1,2)", 1)
	mixedSQL := "SELECT NULLIF(b,e),NULLIF(e,b) FROM mixed"
	rows, err := db.QueryContext(ctx, mixedSQL)
	check(rows, err, []string{"nullif", "nullif"}, []string{"FLOAT8", "FLOAT4"}, [][]any{{float64(1), float64(2)}})
	mixed, err := db.PrepareContext(ctx, mixedSQL)
	if err != nil {
		t.Fatal(err)
	}
	rows, err = mixed.QueryContext(ctx)
	check(rows, err, []string{"nullif", "nullif"}, []string{"FLOAT8", "FLOAT4"}, [][]any{{float64(1), float64(2)}})
	mixed.Close()
	drainFailure := func(rows *sql.Rows, err error) error {
		t.Helper()
		if rows != nil {
			if err == nil {
				for rows.Next() {
				}
				err = rows.Err()
			}
			rows.Close()
		}
		return err
	}
	for _, tc := range []struct {
		q, name           string
		selected, skipped int
		skippedValue      int64
	}{
		{"SELECT CASE WHEN b THEN 1 ELSE CAST(CAST($1 AS TEXT) AS INTEGER) END FROM t WHERE id=$2", "case", 2, 1, 1},
		{"SELECT COALESCE(v,CAST(CAST($1 AS TEXT) AS INTEGER)) FROM t WHERE id=$2", "coalesce", 3, 1, 0},
	} {
		rows, err := db.QueryContext(ctx, tc.q, "bad", tc.selected)
		if err := typeCoreDiagnostic(drainFailure(rows, err), stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`}); err != nil {
			t.Fatal(tc.q, err)
		}
		rows, err = db.QueryContext(ctx, tc.q, "7", tc.selected)
		check(rows, err, []string{tc.name}, []string{"INT4"}, [][]any{{int64(7)}})
		stmt, err := db.PrepareContext(ctx, tc.q)
		if err != nil {
			t.Fatal(err)
		}
		rows, err = stmt.QueryContext(ctx, "bad", tc.selected)
		if err := typeCoreDiagnostic(drainFailure(rows, err), stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`}); err != nil {
			t.Fatal(tc.q, err)
		}
		rows, err = stmt.QueryContext(ctx, "7", tc.selected)
		check(rows, err, []string{tc.name}, []string{"INT4"}, [][]any{{int64(7)}})
		rows, err = stmt.QueryContext(ctx, "bad", tc.skipped)
		check(rows, err, []string{tc.name}, []string{"INT4"}, [][]any{{tc.skippedValue}})
		stmt.Close()
	}
	for _, tc := range []struct {
		q    string
		want stuntgres.Error
	}{
		{"SELECT CASE WHEN 1 THEN 2 END FROM t", stuntgres.Error{Code: "42804", Message: "argument of CASE/WHEN must be type boolean, not type integer", Position: "18"}},
		{"SELECT COALESCE(1,TRUE) FROM t", stuntgres.Error{Code: "42804", Message: "COALESCE types integer and boolean cannot be matched", Position: "19"}},
		{"SELECT CASE WHEN TRUE THEN 1 ELSE FALSE END FROM t", stuntgres.Error{Code: "42804", Message: "CASE types boolean and integer cannot be matched", Position: "28"}},
		{"SELECT NULLIF(1,TRUE) FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer = boolean", Position: "8", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
		{"SELECT COALESCE(1,'bad') FROM t", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "19"}},
	} {
		rows, err := db.QueryContext(ctx, tc.q)
		if rows != nil {
			rows.Close()
			t.Fatal("analysis error returned rows")
		}
		if err := typeCoreDiagnostic(err, tc.want); err != nil {
			t.Fatal(tc.q, err)
		}
		stmt, err := db.PrepareContext(ctx, tc.q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("analysis error returned statement")
		}
		if err := typeCoreDiagnostic(err, tc.want); err != nil {
			t.Fatal(tc.q, err)
		}
	}
	plan, err := db.PrepareContext(ctx, "SELECT CASE WHEN v=0 THEN 0 ELSE 1/0 END FROM t")
	if err != nil {
		t.Fatal("Prepare folded execution-planning expression", err)
	}
	rows, err = plan.QueryContext(ctx)
	if err == nil && rows != nil {
		for rows.Next() {
		}
		err = rows.Err()
		rows.Close()
	}
	plan.Close()
	if err := typeCoreDiagnostic(err, stuntgres.Error{Code: "22012", Message: "division by zero"}); err != nil {
		t.Fatal(err)
	}
	exec("CREATE TABLE d(a INTEGER DEFAULT CASE WHEN TRUE THEN COALESCE(NULL,7) ELSE 1/0 END,b INTEGER DEFAULT NULLIF(2,3),c INTEGER DEFAULT GREATEST(NULL,4),e INTEGER DEFAULT LEAST(NULL,5))", 0)
	exec("INSERT INTO d VALUES(DEFAULT,DEFAULT,DEFAULT,DEFAULT)", 1)
	exec("CREATE TABLE bd(a BOOL DEFAULT CASE WHEN TRUE THEN FALSE AND CAST(CAST('oops' AS TEXT) AS INTEGER)=1 ELSE FALSE END,b BOOL DEFAULT CASE WHEN TRUE THEN TRUE OR CAST(CAST('oops' AS TEXT) AS INTEGER)=1 ELSE FALSE END,c BOOL DEFAULT COALESCE(FALSE AND CAST(CAST('oops' AS TEXT) AS INTEGER)=1,FALSE),e BOOL DEFAULT COALESCE(TRUE OR CAST(CAST('oops' AS TEXT) AS INTEGER)=1,FALSE))", 0)
	exec("INSERT INTO bd VALUES(DEFAULT,DEFAULT,DEFAULT,DEFAULT)", 1)
	rows, err = db.QueryContext(ctx, "SELECT a,b,c,e FROM bd")
	check(rows, err, []string{"a", "b", "c", "e"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, false, true}})
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
	var redump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &redump); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatal("conditional default dump is not stable")
	}
	exec("INSERT INTO d VALUES(DEFAULT,DEFAULT,DEFAULT,DEFAULT)", 1)
	exec("INSERT INTO bd VALUES(DEFAULT,DEFAULT,DEFAULT,DEFAULT)", 1)
	rows, err = db.QueryContext(ctx, "SELECT a,b,c,e FROM bd")
	check(rows, err, []string{"a", "b", "c", "e"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, false, true}, {false, true, false, true}})
	rows, err = db.QueryContext(ctx, "SELECT a,b,c,e FROM d")
	check(rows, err, []string{"a", "b", "c", "e"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(7), int64(2), int64(4), int64(5)}, {int64(7), int64(2), int64(4), int64(5)}})
	_, err = db.ExecContext(ctx, "UPDATE t SET v=CASE WHEN id=1 THEN 9 ELSE 10/(id-2) END")
	if err := typeCoreDiagnostic(err, stuntgres.Error{Code: "22012", Message: "division by zero"}); err != nil {
		t.Fatal(err)
	}
	rows, err = db.QueryContext(ctx, "SELECT v FROM t ORDER BY id")
	check(rows, err, []string{"v"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(2)}, {nil}})
}
