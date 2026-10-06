package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
)

// TestArithmeticSmoke exercises the consumer-facing integer expression path.
func TestArithmeticSmoke(t *testing.T) {
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector("arithmetic-smoke")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	for _, q := range []string{"CREATE TABLE t(id INT,i INT,b BIGINT)", "INSERT INTO t VALUES(1,7,7),(2,-7,-7),(3,NULL,NULL)"} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	q := "SELECT i/2,i%2,b/2,b%2,(i+1)*2,+i,-i FROM t ORDER BY id"
	want := [][]any{{int64(3), int64(1), int64(3), int64(1), int64(16), int64(7), int64(-7)}, {int64(-3), int64(-1), int64(-3), int64(-1), int64(-12), int64(-7), int64(7)}, {nil, nil, nil, nil, nil, nil, nil}}
	check := func(rows *sql.Rows, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, typ := range []string{"INT4", "INT4", "INT8", "INT8", "INT4", "INT4", "INT4"} {
			if types[i].Name() != "?column?" || types[i].DatabaseTypeName() != typ || types[i].ScanType() != reflect.TypeOf(int64(0)) {
				t.Fatalf("column %d: %v", i, types[i])
			}
			if _, known := types[i].Nullable(); known {
				t.Fatal("computed nullability became physical")
			}
		}
		got := make([][]any, 0)
		for rows.Next() {
			values := make([]any, len(types))
			targets := make([]any, len(types))
			for i := range values {
				targets[i] = &values[i]
			}
			if err := rows.Scan(targets...); err != nil {
				t.Fatal(err)
			}
			got = append(got, values)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("rows = %#v, want %#v", got, want)
		}
	}
	rows, err := db.QueryContext(ctx, q)
	check(rows, err)
	stmt, err := db.PrepareContext(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	rows, err = stmt.QueryContext(ctx)
	check(rows, err)
	want = [][]any{}
	rows, err = db.QueryContext(ctx, "SELECT i/2,i%2,b/2,b%2,(i+1)*2,+i,-i FROM t WHERE FALSE")
	check(rows, err)
	held, err := db.PrepareContext(ctx, "SELECT i+$1 FROM t WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, arg := range []any{2, nil, 3} {
		var got any
		if err := held.QueryRowContext(ctx, arg).Scan(&got); err != nil {
			t.Fatal(err)
		}
		var expected any
		if arg != nil {
			expected = int64(arg.(int) + 7)
		}
		if got != expected {
			t.Fatal(got, expected)
		}
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE minimum(i INT,b BIGINT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO minimum VALUES(-2147483648,-9223372036854775808)"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ q, code, primary string }{{"SELECT i/0 FROM t WHERE id=1", "22012", "division by zero"}, {"SELECT b/0 FROM t WHERE id=1", "22012", "division by zero"}, {"SELECT 2147483647+1 FROM t WHERE id=1", "22003", "integer out of range"}, {"SELECT 9223372036854775807+1 FROM t WHERE id=1", "22003", "bigint out of range"}, {"SELECT -i FROM minimum", "22003", "integer out of range"}, {"SELECT -b FROM minimum", "22003", "bigint out of range"}} {
		rows, err := db.QueryContext(ctx, tc.q)
		if rows != nil {
			rows.Close()
			t.Fatal("arithmetic error exposed cursor")
		}
		if err := typeCoreDiagnostic(err, stuntgres.Error{Code: tc.code, Message: tc.primary}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE atomic(i INT,b BIGINT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO atomic VALUES(99,99)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO atomic VALUES(7/2,-7%2),(7/0,1)"); err == nil {
		t.Fatal("later tuple division published")
	}
	var sentinel int64
	if err := db.QueryRowContext(ctx, "SELECT i FROM atomic").Scan(&sentinel); err != nil || sentinel != 99 {
		t.Fatal(sentinel, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := held.QueryContext(canceled, 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(canceled, "INSERT INTO atomic VALUES(1+2,3)"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO atomic VALUES(7/2,-7%2)"); err != nil {
		t.Fatal(err)
	}
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
	var a, b int64
	if err := db.QueryRowContext(ctx, "SELECT i,b FROM atomic WHERE i=3").Scan(&a, &b); err != nil || a != 3 || b != -1 {
		t.Fatal(a, b, err)
	}
	if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE replacement(i INT); INSERT INTO replacement VALUES((7+1)*2)")); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT i FROM replacement").Scan(&a); err != nil || a != 16 {
		t.Fatal(a, err)
	}
	if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE failed(i INT); INSERT INTO failed VALUES(1); INSERT INTO failed VALUES(7/0)")); err == nil {
		t.Fatal("failed arithmetic Restore replaced state")
	}
	if err := db.QueryRowContext(ctx, "SELECT i FROM replacement").Scan(&a); err != nil || a != 16 {
		t.Fatal(a, err)
	}
}
