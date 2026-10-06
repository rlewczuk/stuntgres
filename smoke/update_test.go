package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres"
	"testing"
	"time"
)

func TestUpdateCoreSmoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := stuntgres.NewDriver().OpenConnector("update-core-smoke")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	defer db.Close()
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	defer a.ExecContext(context.Background(), "ROLLBACK")
	exec := func(x transactionExecutor, q string, prepared bool, n int64, args ...any) {
		t.Helper()
		if err := transactionAffected(ctx, x, q, prepared, n, args...); err != nil {
			t.Fatal(err)
		}
	}
	check := func(x transactionExecutor, want [][]int64) {
		t.Helper()
		rows, err := x.QueryContext(ctx, "SELECT id,a,b,n FROM t ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		got := [][]int64{}
		for rows.Next() {
			var id, a, b, n int64
			if err := rows.Scan(&id, &a, &b, &n); err != nil {
				t.Fatal(err)
			}
			got = append(got, []int64{id, a, b, n})
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	exec(b, "CREATE TABLE t(id INT,a INT,b INT,n SMALLINT NOT NULL)", false, 0)
	exec(b, "INSERT INTO t VALUES(1,10,20,1),(2,30,40,1),(3,30,40,1)", false, 3)
	saved, err := b.QueryContext(ctx, "SELECT a FROM t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer saved.Close()
	exec(b, "UPDATE t SET a=b,b=a", false, 3)
	check(b, [][]int64{{1, 20, 10, 1}, {2, 40, 30, 1}, {3, 40, 30, 1}})
	exec(b, "UPDATE t SET a=a", false, 3)
	exec(b, "UPDATE t SET a=9 WHERE id=99", true, 0)
	exec(b, "UPDATE t SET a=$1,b=a WHERE id=$2", true, 1, int64(40000), int64(3))
	want := [][]int64{{1, 20, 10, 1}, {2, 40, 30, 1}, {3, 40000, 40, 1}}
	_, err = b.ExecContext(ctx, "UPDATE t SET n=a")
	if err := typeCoreDiagnostic(err, stuntgres.Error{Code: "22003", Message: "smallint out of range"}); err != nil {
		t.Fatal(err)
	}
	check(b, want)
	_, err = b.ExecContext(ctx, "UPDATE t SET n=NULL WHERE id=3")
	if err := typeCoreDiagnostic(err, stuntgres.Error{Code: "23502", Message: `null value in column "n" of relation "t" violates not-null constraint`, Detail: "Failing row contains (3, 40000, 40, null).", Schema: "public", Table: "t", Column: "n"}); err != nil {
		t.Fatal(err)
	}
	check(b, want)
	exec(a, "BEGIN", false, 0)
	exec(a, "UPDATE t SET a=7 WHERE id=1", true, 1)
	check(a, [][]int64{{1, 7, 10, 1}, {2, 40, 30, 1}, {3, 40000, 40, 1}})
	check(b, want)
	exec(a, "COMMIT", false, 0)
	want[0][1] = 7
	check(b, want)
	exec(a, "BEGIN", false, 0)
	exec(a, "UPDATE t SET a=8 WHERE id=1", false, 1)
	waitCtx, stop := context.WithTimeout(ctx, 25*time.Millisecond)
	_, err = b.ExecContext(waitCtx, "UPDATE t SET b=a WHERE id=1")
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("held-row cancellation: %v", err)
	}
	exec(a, "ROLLBACK", false, 0)
	check(b, want)
	exec(b, "UPDATE t SET b=a WHERE id=1", false, 1)
	want[0][2] = 7
	check(b, want)
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
	check(b, want)
	old := []int64{}
	for saved.Next() {
		var v int64
		if err := saved.Scan(&v); err != nil {
			t.Fatal(err)
		}
		old = append(old, v)
	}
	if err := saved.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(old, []int64{10, 30, 30}) {
		t.Fatal("retained pre-update result", old)
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &again); err != nil {
		t.Fatal(err)
	}
	if dump.String() != again.String() {
		t.Fatal("current-row dump changed after round trip")
	}
}
