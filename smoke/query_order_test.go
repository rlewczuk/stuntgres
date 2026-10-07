package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"stuntgres"
)

func orderSmokeRows(t *testing.T, rows *sql.Rows, err error, names, types []string, want [][]any) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, names) {
		t.Fatal(columns, names, err)
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
		v := make([]any, len(names))
		dst := make([]any, len(names))
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

func TestQueryOrderSmoke(t *testing.T) {
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	exec := func(q string, want int64) {
		t.Helper()
		r, err := db.Exec(q)
		if err != nil {
			t.Fatal(q, err)
		}
		count, err := r.RowsAffected()
		if err != nil || count != want {
			t.Fatal(q, count, want, err)
		}
	}
	exec("CREATE TABLE t(id INT NOT NULL,a INT,b INT)", 0)
	exec("INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)", 4)
	for _, tc := range []struct {
		q            string
		names, types []string
		want         [][]any
	}{
		{"SELECT id,a+b AS k FROM t ORDER BY k DESC NULLS LAST,id", []string{"id", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(6)}, {int64(1), int64(3)}, {int64(4), int64(2)}, {int64(3), nil}}},
		{"SELECT id FROM t ORDER BY a ASC NULLS FIRST,b DESC,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(2)}, {int64(1)}, {int64(4)}}},
		{"SELECT id,a FROM t ORDER BY 2 DESC NULLS LAST,1", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(4), int64(2)}, {int64(2), int64(1)}, {int64(3), nil}}},
	} {
		rows, err := db.Query(tc.q)
		orderSmokeRows(t, rows, err, tc.names, tc.types, tc.want)
		stmt, err := db.Prepare(tc.q)
		if err != nil {
			t.Fatal(err)
		}
		rows, err = stmt.Query()
		orderSmokeRows(t, rows, err, tc.names, tc.types, tc.want)
		stmt.Close()
	}
	stmt, err := db.Prepare("SELECT id FROM t ORDER BY a*$1 NULLS LAST,id")
	if err != nil {
		t.Fatal(err)
	}
	for i, arg := range []any{1, -1, nil, 1} {
		ids := []int64{2, 1, 4, 3}
		if i == 1 {
			ids = []int64{1, 4, 2, 3}
		}
		if i == 2 {
			ids = []int64{1, 2, 3, 4}
		}
		want := make([][]any, len(ids))
		for j, id := range ids {
			want[j] = []any{id}
		}
		rows, err := stmt.Query(arg)
		orderSmokeRows(t, rows, err, []string{"id"}, []string{"INT4"}, want)
	}
	stmt.Close()
	for _, tc := range []struct{ q, code, message, position string }{
		{"SELECT id FROM t ORDER BY 0", "42P10", "ORDER BY position 0 is not in select list", "27"},
		{"SELECT a AS x,b AS x FROM t ORDER BY x", "42702", `ORDER BY "x" is ambiguous`, "38"},
	} {
		for _, prepared := range []bool{false, true} {
			var err error
			if prepared {
				s, e := db.Prepare(tc.q)
				err = e
				if s != nil {
					s.Close()
					t.Fatal("failure exposed statement")
				}
			} else {
				r, e := db.Query(tc.q)
				err = e
				if r != nil {
					r.Close()
					t.Fatal("failure exposed rows")
				}
			}
			want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position}
			var diagnostic *stuntgres.Error
			if !errors.As(err, &diagnostic) || *diagnostic != want {
				t.Fatal(diagnostic, err, want)
			}
		}
	}
	exec("CREATE TABLE order_void_t(id INT)", 0)
	exec("CREATE SEQUENCE s START WITH 1", 0)
	voidSession, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seed := func() {
		var result any
		if err := voidSession.QueryRowContext(ctx, "SELECT setseed(.25)").Scan(&result); err != nil {
			t.Fatal(err)
		}
	}
	draw := func() float64 {
		var value float64
		if err := voidSession.QueryRowContext(ctx, "SELECT random()").Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	seed()
	draw()
	wantContinuation := draw()
	for _, populated := range []bool{false, true} {
		if populated {
			if _, err := voidSession.ExecContext(ctx, "INSERT INTO order_void_t VALUES(1),(2),(3)"); err != nil {
				t.Fatal(err)
			}
		}
		for _, tc := range []struct{ q, position string }{
			{"SELECT id FROM order_void_t ORDER BY setseed(0)", "38"},
			{"SELECT setseed(0) AS v FROM order_void_t ORDER BY v", "51"},
			{"SELECT setseed(0) FROM order_void_t ORDER BY 1", "46"},
			{"SELECT nextval('s') FROM order_void_t ORDER BY setseed(0)", "48"},
		} {
			for _, prepared := range []bool{false, true} {
				if _, err := voidSession.ExecContext(ctx, "ALTER SEQUENCE s RESTART WITH 1"); err != nil {
					t.Fatal(err)
				}
				seed()
				draw()
				var err error
				if prepared {
					stmt, failure := voidSession.PrepareContext(ctx, tc.q)
					err = failure
					if stmt != nil {
						stmt.Close()
						t.Fatal("VOID failure exposed statement")
					}
				} else {
					rows, failure := voidSession.QueryContext(ctx, tc.q)
					err = failure
					if rows != nil {
						rows.Close()
						t.Fatal("VOID failure exposed cursor")
					}
				}
				want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an ordering operator for type void", Hint: "Use an explicit ordering operator or modify the query.", Position: tc.position}
				var diagnostic *stuntgres.Error
				if !errors.As(err, &diagnostic) || *diagnostic != want {
					t.Fatal(tc.q, diagnostic, err, want)
				}
				if got := draw(); got != wantContinuation {
					t.Fatal("VOID analysis changed random continuation", got, wantContinuation)
				}
				var next int64
				if err := voidSession.QueryRowContext(ctx, "SELECT nextval('s')").Scan(&next); err != nil || next != 1 {
					t.Fatal("VOID analysis evaluated sequence output", next, err)
				}
			}
		}
	}
	if err := voidSession.Close(); err != nil {
		t.Fatal(err)
	}
	exec("DROP SEQUENCE s", 0)
	exec("CREATE TABLE v(id INT)", 0)
	exec("INSERT INTO v VALUES(1),(2),(3)", 3)
	for _, key := range []string{"n", "1", "nextval('s')"} {
		exec("CREATE SEQUENCE s START WITH 1", 0)
		rows, err := db.Query("SELECT nextval('s') AS n FROM v ORDER BY " + key + " DESC")
		orderSmokeRows(t, rows, err, []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}, {int64(2)}, {int64(1)}})
		var next int64
		if err := db.QueryRow("SELECT nextval('s')").Scan(&next); err != nil || next != 4 {
			t.Fatal(next, err)
		}
		exec("DROP SEQUENCE s", 0)
	}
	exec("CREATE SEQUENCE s START WITH 1", 0)
	rows, err := db.Query("SELECT id,nextval('s') AS n FROM v ORDER BY id DESC")
	orderSmokeRows(t, rows, err, []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(3), int64(1)}, {int64(2), int64(2)}, {int64(1), int64(3)}})
	var next int64
	if err := db.QueryRow("SELECT nextval('s')").Scan(&next); err != nil || next != 4 {
		t.Fatal(next, err)
	}
	exec("INSERT INTO v VALUES(4),(5)", 2)
	exec("CREATE SEQUENCE repeated_s START WITH 1", 0)
	exec("CREATE VIEW repeated_order AS SELECT id FROM v WHERE id>2 ORDER BY nextval('repeated_s'),nextval('repeated_s') DESC", 0)
	rows, err = db.Query("SELECT id FROM repeated_order ORDER BY id")
	orderSmokeRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(4)}, {int64(5)}})
	if err := db.QueryRow("SELECT nextval('repeated_s')").Scan(&next); err != nil || next != 4 {
		t.Fatal("repeated hidden filtered keys", next, err)
	}
	repeated, err := db.Prepare("SELECT id FROM repeated_order ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	rows, err = repeated.Query()
	orderSmokeRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(4)}, {int64(5)}})
	repeated.Close()
	if err := db.QueryRow("SELECT nextval('repeated_s')").Scan(&next); err != nil || next != 8 {
		t.Fatal("fresh repeated hidden keys", next, err)
	}
	held, err := db.Query("SELECT id,a+b AS k FROM t ORDER BY k DESC NULLS LAST,id")
	if err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	exec("INSERT INTO t VALUES(5,9,9)", 1)
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	want := [][]any{{int64(2), int64(6)}, {int64(1), int64(3)}, {int64(4), int64(2)}, {int64(3), nil}}
	orderSmokeRows(t, held, nil, []string{"id", "k"}, []string{"INT4", "INT4"}, want)
	rows, err = db.Query("SELECT id,a+b AS k FROM t ORDER BY k DESC NULLS LAST,id")
	orderSmokeRows(t, rows, err, []string{"id", "k"}, []string{"INT4", "INT4"}, want)
	exec("CREATE TABLE d(id INT)", 0)
	exec("INSERT INTO d VALUES(9)", 1)
	_, err = db.Exec("INSERT INTO d SELECT id FROM t ORDER BY a,1/(id-4)")
	var diagnostic *stuntgres.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" || diagnostic.Message != "division by zero" {
		t.Fatal(err)
	}
	rows, err = db.Query("SELECT id FROM d ORDER BY id")
	orderSmokeRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}})
}
