package smoke_test

import (
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

func TestQueryCoreSmoke(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	connector, err := d.OpenConnector("query-core-smoke")
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
			t.Fatal(q, got, err)
		}
	}
	for _, tc := range []struct{ query, position string }{
		{"SELECT id FROM t", "16"},
		{"SELECT /*é*/ id FROM t", "22"},
	} {
		want := stuntgres.Error{Code: "42P01", Message: `relation "t" does not exist`, Position: tc.position}
		rows, err := db.QueryContext(ctx, tc.query)
		if rows != nil {
			rows.Close()
			t.Fatal("missing relation query exposed cursor")
		}
		if err := typeCoreDiagnostic(err, want); err != nil {
			t.Fatal(err)
		}
		stmt, err := db.PrepareContext(ctx, tc.query)
		if stmt != nil {
			stmt.Close()
			t.Fatal("missing relation prepare exposed handle")
		}
		if err := typeCoreDiagnostic(err, want); err != nil {
			t.Fatal(err)
		}
	}
	exec("CREATE TABLE t(id INT NOT NULL,v INT,s TEXT,f BOOL)", 0)
	exec("INSERT INTO t VALUES(1,1,'x',TRUE),(2,NULL,'x',NULL),(3,2,'y',FALSE),(4,1,'x',TRUE)", 4)
	exec("CREATE TABLE empty(id INT NOT NULL,v INT,s TEXT,f BOOL)", 0)
	check := func(rows *sql.Rows, err error, names, types []string, want [][]any, bag bool) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		gotNames, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(gotNames, names) {
			t.Fatal(gotNames, err)
		}
		metadata, err := rows.ColumnTypes()
		if err != nil || len(metadata) != len(types) {
			t.Fatal(metadata, err)
		}
		for i, typ := range types {
			if metadata[i].DatabaseTypeName() != typ {
				t.Fatal(metadata[i])
			}
		}
		got := [][]any{}
		for rows.Next() {
			values := make([]any, len(names))
			dst := make([]any, len(names))
			for i := range values {
				dst[i] = &values[i]
			}
			if err := rows.Scan(dst...); err != nil {
				t.Fatal(err)
			}
			got = append(got, values)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if bag {
			if len(got) != len(want) {
				t.Fatal(got, want)
			}
			used := make([]bool, len(want))
			for _, row := range got {
				found := false
				for i, expected := range want {
					if !used[i] && reflect.DeepEqual(row, expected) {
						used[i] = true
						found = true
						break
					}
				}
				if !found {
					t.Fatal("bag occurrence", got, want)
				}
			}
		} else if !reflect.DeepEqual(got, want) {
			t.Fatal(got, want)
		}
	}
	for _, tc := range []struct {
		q            string
		names, types []string
		want         [][]any
		args         []any
		bag          bool
	}{
		{"SELECT id,7,NULL,f FROM t ORDER BY id", []string{"id", "?column?", "?column?", "f"}, []string{"INT4", "INT4", "TEXT", "BOOL"}, [][]any{{int64(1), int64(7), nil, true}, {int64(2), int64(7), nil, nil}, {int64(3), int64(7), nil, false}, {int64(4), int64(7), nil, true}}, nil, false},
		{"SELECT id,7,NULL,f FROM empty", []string{"id", "?column?", "?column?", "f"}, []string{"INT4", "INT4", "TEXT", "BOOL"}, [][]any{}, nil, true},
		{"SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}, nil, true},
		{"SELECT 7 FROM t WHERE v=1", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}}, nil, true},
		{"SELECT v FROM t WHERE v=NULL", []string{"v"}, []string{"INT4"}, [][]any{}, nil, true},
		{"SELECT id FROM t WHERE FALSE", []string{"id"}, []string{"INT4"}, [][]any{}, nil, true},
		{"SELECT id FROM t WHERE NULL", []string{"id"}, []string{"INT4"}, [][]any{}, nil, true},
		{"SELECT id FROM t WHERE f", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(4)}}, nil, true},
		{"SELECT $1 FROM t WHERE s=$1", []string{"?column?"}, []string{"TEXT"}, [][]any{{"x"}, {"x"}, {"x"}}, []any{"x"}, true},
		{"SELECT v FROM t ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}, {nil}}, nil, false},
		{"SELECT v FROM t ORDER BY v DESC", []string{"v"}, []string{"INT4"}, [][]any{{nil}, {int64(2)}, {int64(1)}, {int64(1)}}, nil, false},
	} {
		rows, err := db.QueryContext(ctx, tc.q, tc.args...)
		check(rows, err, tc.names, tc.types, tc.want, tc.bag)
		stmt, err := db.PrepareContext(ctx, tc.q)
		if err != nil {
			t.Fatal(err)
		}
		rows, err = stmt.QueryContext(ctx, tc.args...)
		check(rows, err, tc.names, tc.types, tc.want, tc.bag)
		stmt.Close()
	}
	held, err := db.PrepareContext(ctx, "SELECT id FROM t WHERE v=$1 ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for i, arg := range []any{1, 2, nil} {
		want := [][]any{{int64(1)}, {int64(4)}}
		if i == 1 {
			want = [][]any{{int64(3)}}
		}
		if i == 2 {
			want = [][]any{}
		}
		rows, err := held.QueryContext(ctx, arg)
		check(rows, err, []string{"id"}, []string{"INT4"}, want, false)
	}
	for _, tc := range []struct{ q, position string }{
		{"SELECT id FROM t ORDER BY missing", "27"},
		{"SELECT /*é*/ id FROM t ORDER BY missing", "33"},
	} {
		want := stuntgres.Error{Code: "42703", Message: `column "missing" does not exist`, Position: tc.position}
		rows, err := db.QueryContext(ctx, tc.q)
		if rows != nil {
			rows.Close()
			t.Fatal("failed query exposed cursor")
		}
		if err := typeCoreDiagnostic(err, want); err != nil {
			t.Fatal(err)
		}
		stmt, err := db.PrepareContext(ctx, tc.q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("failed prepare exposed handle")
		}
		if err := typeCoreDiagnostic(err, want); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("malformed shared syntax", func(t *testing.T) {
		query := "SELECT id FROM t WHERE"
		want := stuntgres.Error{Code: "42601", Message: "syntax error at end of input", Position: "23"}
		rows, err := db.QueryContext(ctx, query)
		if rows != nil {
			rows.Close()
			t.Fatal("malformed query exposed cursor")
		}
		if err := typeCoreDiagnostic(err, want); err != nil {
			t.Fatal(err)
		}
		stmt, err := db.PrepareContext(ctx, query)
		if stmt != nil {
			stmt.Close()
			t.Fatal("malformed prepare exposed handle")
		}
		if err := typeCoreDiagnostic(err, want); err != nil {
			t.Fatal(err)
		}
	})
	// Raw driver iteration makes EOF/error cleanup and sticky errors observable.
	raw, err := d.Open("query-core-smoke")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	query := raw.(driver.QueryerContext)
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cursor, err := query.QueryContext(cancelCtx, "SELECT id,7 FROM t ORDER BY id", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	dst := make([]driver.Value, 2)
	if err := cursor.Next(dst); err != nil {
		t.Fatal(err)
	}
	saved := append([]driver.Value(nil), dst...)
	cancel()
	for range 2 {
		if err := cursor.Next(dst); !errors.Is(err, context.Canceled) {
			t.Fatal("nonsticky cancellation", err)
		}
	}
	if !reflect.DeepEqual(saved, []driver.Value{int64(1), int64(7)}) || !reflect.DeepEqual(cursor.Columns(), []string{"id", "?column?"}) {
		t.Fatal(saved, cursor.Columns())
	}
	cursor.Close()
	cursor.Close()
	if err := cursor.Next(dst); err != io.EOF {
		t.Fatal(err)
	}
	direct, err := query.QueryContext(ctx, "SELECT id FROM t ORDER BY id", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer direct.Close()
	owned := []byte("owned")
	computed, err := query.QueryContext(ctx, "SELECT id,$1 FROM t ORDER BY id", []driver.NamedValue{{Ordinal: 1, Value: owned}})
	if err != nil {
		t.Fatal(err)
	}
	defer computed.Close()
	owned[0] = 'X'
	exec("INSERT INTO t VALUES(5,5,'later',TRUE)", 1)
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE t(replacement TEXT); INSERT INTO t VALUES('99')")); err != nil {
		t.Fatal(err)
	}
	for _, rows := range []driver.Rows{direct, computed} {
		if rows.Columns()[0] != "id" || rows.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0) != "INT4" {
			t.Fatal("replacement metadata")
		}
		dst := make([]driver.Value, len(rows.Columns()))
		for i := range 4 {
			if err := rows.Next(dst); err != nil || dst[0] != int64(i+1) {
				t.Fatal(dst, err)
			}
			if len(dst) == 2 && dst[1] != "owned" {
				t.Fatal(dst)
			}
		}
		if err := rows.Next(dst); err != io.EOF {
			t.Fatal(err)
		}
		if rows.Columns()[0] != "id" {
			t.Fatal("EOF erased schema")
		}
	}
	rows, err := db.QueryContext(ctx, "SELECT replacement FROM t")
	check(rows, err, []string{"replacement"}, []string{"TEXT"}, [][]any{{"99"}}, false)
	exec("CREATE TABLE zero(v INT)", 0)
	exec("INSERT INTO zero VALUES(1),(2)", 2)
	exec("ALTER TABLE zero DROP COLUMN v", 0)
	rows, err = db.QueryContext(ctx, "SELECT * FROM zero")
	check(rows, err, []string{}, []string{}, [][]any{{}, {}}, true)
	exec("CREATE TABLE times(v TIMESTAMP)", 0)
	exec("INSERT INTO times VALUES('infinity'),('infinity')", 2)
	bytesRows, err := query.QueryContext(ctx, "SELECT v,v FROM times", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bytesRows.Close()
	bytesDst := make([]driver.Value, 2)
	if err := bytesRows.Next(bytesDst); err != nil {
		t.Fatal(err)
	}
	first, second := bytesDst[0].([]byte), bytesDst[1].([]byte)
	first[0] = 'X'
	if string(second) != "infinity" {
		t.Fatal("mutable exports alias")
	}
	if err := bytesRows.Next(bytesDst); err != nil || string(bytesDst[0].([]byte)) != "infinity" {
		t.Fatal(bytesDst, err)
	}
	bytesRows.Close()
	if string(second) != "infinity" {
		t.Fatal("Close invalidated export")
	}
}
