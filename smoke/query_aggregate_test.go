package smoke_test

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func aggregateSmokeRows(t *testing.T, rows *sql.Rows, err error, names, types []string, want [][]any) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, names) {
		t.Fatal(columns, err, names)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil || len(metadata) != len(types) {
		t.Fatal(metadata, err)
	}
	for i, typ := range types {
		if metadata[i].DatabaseTypeName() != typ {
			t.Fatal(metadata[i], typ)
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
			t.Fatal("missing bag occurrence", row, got, want)
		}
	}
}
func TestQueryAggregateSmoke(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	connector, err := d.OpenConnector(t.Name())
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
		n, err := r.RowsAffected()
		if err != nil || n != want {
			t.Fatal(q, n, err, want)
		}
	}
	exec("CREATE TABLE t(k TEXT,v INT)", 0)
	exec("INSERT INTO t VALUES('a',1),('a',NULL),(NULL,2)", 3)
	exec("CREATE TABLE empty(k TEXT,v INT)", 0)
	for _, tc := range []struct {
		q            string
		names, types []string
		want         [][]any
	}{
		{"SELECT k,count(*) FROM t GROUP BY k", []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {nil, int64(1)}}},
		{"SELECT count(*) FROM empty", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}},
		{"SELECT k,count(*) FROM empty GROUP BY k", []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{}},
		{"SELECT k FROM t GROUP BY k HAVING count(*)>1", []string{"k"}, []string{"TEXT"}, [][]any{{"a"}}},
		{"SELECT 7 FROM empty HAVING TRUE", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(7)}}},
		{"SELECT 7 FROM t GROUP BY k", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}}},
		{"SELECT k,v,count(*) FROM t GROUP BY k,v", []string{"k", "v", "count"}, []string{"TEXT", "INT4", "INT8"}, [][]any{{"a", int64(1), int64(1)}, {"a", nil, int64(1)}, {nil, int64(2), int64(1)}}},
		{"SELECT (v+1)*2,count(*) FROM t GROUP BY v+1", []string{"?column?", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(4), int64(1)}, {nil, int64(1)}, {int64(6), int64(1)}}},
		{"SELECT k,count(*) FROM t GROUP BY 1", []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {nil, int64(1)}}},
		{"SELECT count(*) FROM t GROUP BY +1", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}},
	} {
		rows, err := db.QueryContext(ctx, tc.q)
		aggregateSmokeRows(t, rows, err, tc.names, tc.types, tc.want)
		stmt, err := db.PrepareContext(ctx, tc.q)
		if err != nil {
			t.Fatal(err)
		}
		rows, err = stmt.QueryContext(ctx)
		aggregateSmokeRows(t, rows, err, tc.names, tc.types, tc.want)
		stmt.Close()
	}
	bad := "SELECT /*é*/ v,count(*) FROM t"
	want := stuntgres.Error{Code: "42803", Message: `column "t.v" must appear in the GROUP BY clause or be used in an aggregate function`, Position: "14"}
	rows, err := db.QueryContext(ctx, bad)
	if rows != nil {
		rows.Close()
		t.Fatal("failed query exposed rows")
	}
	if err := typeCoreDiagnostic(err, want); err != nil {
		t.Fatal(err)
	}
	stmt, err := db.PrepareContext(ctx, bad)
	if stmt != nil {
		stmt.Close()
		t.Fatal("failed prepare exposed handle")
	}
	if err := typeCoreDiagnostic(err, want); err != nil {
		t.Fatal(err)
	}
	stmt, err = db.PrepareContext(ctx, "SELECT count(*) FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held, err := stmt.QueryContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	exec("INSERT INTO t VALUES('b',3)", 1)
	aggregateSmokeRows(t, held, nil, []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}})
	rows, err = stmt.QueryContext(ctx)
	aggregateSmokeRows(t, rows, err, []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}})
	exec("CREATE TABLE out(k TEXT,n BIGINT)", 0)
	exec("INSERT INTO out SELECT k,count(*) FROM t GROUP BY k", 3)
	rows, err = db.QueryContext(ctx, "SELECT k,n FROM out")
	aggregateSmokeRows(t, rows, err, []string{"k", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {nil, int64(1)}, {"b", int64(1)}})
	for _, replacement := range []string{"reset", "restore"} {
		held, err := db.QueryContext(ctx, "SELECT k,n FROM out GROUP BY k,n")
		if err != nil {
			t.Fatal(err)
		}
		if replacement == "reset" {
			err = stuntgres.Reset(ctx, db)
		} else {
			err = stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE replacement(v INT)"))
		}
		if err != nil {
			t.Fatal(err)
		}
		aggregateSmokeRows(t, held, nil, []string{"k", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {nil, int64(1)}, {"b", int64(1)}})
		if replacement == "reset" {
			exec("CREATE TABLE out(k TEXT,n BIGINT)", 0)
			exec("INSERT INTO out VALUES('a',2),(NULL,1),('b',1)", 3)
		}
	}
}
