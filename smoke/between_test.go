package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres"
)

func TestBetweenSmoke(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	connector, err := d.OpenConnector("between-smoke")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	exec := func(q string, n int64) {
		t.Helper()
		r, err := db.ExecContext(ctx, q)
		if err != nil {
			t.Fatal(q, err)
		}
		got, err := r.RowsAffected()
		if err != nil || got != n {
			t.Fatal(q, got, n, err)
		}
	}
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
			v, dst := make([]any, len(types)), make([]any, len(types))
			for i := range v {
				dst[i] = &v[i]
			}
			if err := rows.Scan(dst...); err != nil {
				t.Fatal(err)
			}
			got = append(got, v)
		}
		if err := rows.Err(); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal(got, want, err)
		}
	}
	exec("CREATE TABLE t(id INTEGER,v INTEGER)", 0)
	exec("INSERT INTO t VALUES(1,0),(2,1),(3,2),(4,3),(5,4),(6,NULL)", 6)
	rows, err := db.QueryContext(ctx, "SELECT v BETWEEN 1 AND 3,v NOT BETWEEN 1 AND 3,v BETWEEN SYMMETRIC 3 AND 1,v NOT BETWEEN SYMMETRIC 3 AND 1 FROM t ORDER BY id")
	check(rows, err, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, false, true}, {true, false, true, false}, {true, false, true, false}, {true, false, true, false}, {false, true, false, true}, {nil, nil, nil, nil}})
	for _, tc := range []struct {
		expr string
		want [][]any
	}{
		{"v BETWEEN 3 AND 1", [][]any{{false}, {false}, {false}, {false}, {false}, {nil}}},
		{"v BETWEEN 2 AND 2", [][]any{{false}, {false}, {true}, {false}, {false}, {nil}}},
	} {
		rows, err := db.QueryContext(ctx, "SELECT "+tc.expr+" FROM t ORDER BY id")
		check(rows, err, []string{"BOOL"}, tc.want)
	}
	rows, err = db.QueryContext(ctx, "SELECT 5 BETWEEN NULL AND 3,5 NOT BETWEEN NULL AND 3,5 BETWEEN SYMMETRIC NULL AND 3,TRUE BETWEEN (1 BETWEEN 0 AND 2) AND TRUE FROM t WHERE id=1")
	check(rows, err, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, nil, true}})
	rows, err = db.QueryContext(ctx, "SELECT TRUE BETWEEN (1<2) AND (2<3),TRUE BETWEEN 1<2 AND (2<3),2 BETWEEN 1 AND 3 = TRUE FROM t WHERE id=1")
	check(rows, err, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}})
	rows, err = db.QueryContext(ctx, "SELECT id FROM t WHERE v BETWEEN (1+0) AND (2+1) AND TRUE BETWEEN (FALSE OR TRUE) AND TRUE ORDER BY id")
	check(rows, err, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}, {int64(4)}})
	stmt, err := db.PrepareContext(ctx, "SELECT $1 BETWEEN 1 AND 3 FROM t WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	for _, arg := range []any{2, nil, 2} {
		var want any = true
		if arg == nil {
			want = nil
		}
		rows, err := stmt.QueryContext(ctx, arg)
		check(rows, err, []string{"BOOL"}, [][]any{{want}})
	}
	stmt.Close()
	_, err = db.QueryContext(ctx, "SELECT v NOT BETWEEN 1 AND TRUE FROM t")
	if err := typeCoreDiagnostic(err, stuntgres.Error{Code: "42883", Message: "operator does not exist: integer > boolean", Position: "10", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}); err != nil {
		t.Fatal(err)
	}
	_, err = db.QueryContext(ctx, "SELECT TRUE BETWEEN 1<2 AND 2<3 FROM t WHERE id=1")
	if err := typeCoreDiagnostic(err, stuntgres.Error{Code: "42883", Message: "operator does not exist: boolean <= integer", Position: "13", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}); err != nil {
		t.Fatal(err)
	}
	exec("CREATE TABLE temporal_bounds(d DATE,ts TIMESTAMP)", 0)
	exec("INSERT INTO temporal_bounds VALUES('2000-01-01','2000-01-03')", 1)
	for _, form := range []string{"BETWEEN", "NOT BETWEEN", "BETWEEN SYMMETRIC", "NOT BETWEEN SYMMETRIC"} {
		query := "SELECT '2000-01-02' " + form + " d AND ts FROM temporal_bounds"
		want := [][]any{{form == "BETWEEN" || form == "BETWEEN SYMMETRIC"}}
		rows, err := db.QueryContext(ctx, query)
		check(rows, err, []string{"BOOL"}, want)
		held, err := db.PrepareContext(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			rows, err := held.QueryContext(ctx)
			check(rows, err, []string{"BOOL"}, want)
		}
		held.Close()
	}
	exec("CREATE TABLE defaults(b BOOL DEFAULT (2 BETWEEN SYMMETRIC 3 AND 1),c BOOL DEFAULT (TRUE BETWEEN (1<2) AND (2<3)),s BOOL DEFAULT (TRUE BETWEEN TRUE AND (2 BETWEEN 1 AND 3)),n BOOL DEFAULT ((TRUE OR FALSE) NOT BETWEEN SYMMETRIC FALSE AND TRUE))", 0)
	exec("INSERT INTO defaults DEFAULT VALUES", 1)
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
	exec("INSERT INTO defaults DEFAULT VALUES", 1)
	rows, err = db.QueryContext(ctx, "SELECT b BETWEEN FALSE AND TRUE,c BETWEEN FALSE AND TRUE,s BETWEEN FALSE AND TRUE,n BETWEEN TRUE AND TRUE FROM defaults")
	check(rows, err, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, false}, {true, true, true, false}})
}
