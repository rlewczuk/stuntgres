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
	// INSERT's query source must consume the prior own UPDATE head, not its
	// predecessor, while peer visibility and rollback remain transaction-owned.
	exec(b, "CREATE TABLE consumed(id INT,a INT,b INT,n SMALLINT)", false, 0)
	exec(a, "BEGIN", false, 0)
	exec(a, "UPDATE t SET a=12 WHERE id=1", false, 1)
	exec(a, "INSERT INTO consumed SELECT id,a,b,n FROM t WHERE id=1", true, 1)
	var consumed int64
	if err := a.QueryRowContext(ctx, "SELECT a FROM consumed").Scan(&consumed); err != nil || consumed != 12 {
		t.Fatal(consumed, err)
	}
	if err := b.QueryRowContext(ctx, "SELECT a FROM consumed").Scan(&consumed); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("peer saw private INSERT query result", err)
	}
	exec(a, "ROLLBACK", false, 0)
	if err := b.QueryRowContext(ctx, "SELECT a FROM consumed").Scan(&consumed); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("rollback retained INSERT query result", err)
	}
	// Retained public composition of the integrated DATE/BYTEA and baseline
	// cast/null/truth/distinct surfaces, not a throwaway interoperability probe.
	exec(b, "CREATE TABLE composed(id INT,d DATE,b BYTEA,n INT NOT NULL,f BOOL)", false, 0)
	exec(b, `INSERT INTO composed VALUES(1,'2000-01-01','\x01',1,TRUE)`, false, 1)
	composed, err := b.PrepareContext(ctx, "UPDATE composed SET d=$1,b=$2,n=CAST(n AS BIGINT)::INTEGER,f=(b IS NULL) IS FALSE WHERE id=1 AND d IS DISTINCT FROM $1")
	if err != nil {
		t.Fatal(err)
	}
	defer composed.Close()
	for _, args := range [][]any{{"2024-02-29", []byte{0, 92, 255}}, {nil, nil}, {"2030-01-02", []byte{65}}} {
		r, err := composed.ExecContext(ctx, args...)
		if err != nil {
			t.Fatal(err)
		}
		n, err := r.RowsAffected()
		if err != nil || n != 1 {
			t.Fatal(n, err)
		}
	}
	var date string
	var blob []byte
	var number int64
	var flag bool
	if err = b.QueryRowContext(ctx, "SELECT d,b,n,f FROM composed").Scan(&date, &blob, &number, &flag); err != nil || date != "2030-01-02" || !bytes.Equal(blob, []byte{65}) || number != 1 || flag {
		t.Fatal(date, blob, number, flag, err)
	}
	exec(b, "UPDATE composed SET d=d,b=b,n=CAST(n AS BIGINT)::INTEGER WHERE f IS FALSE", false, 1)
	failed, err := b.PrepareContext(ctx, "UPDATE composed SET b=$1,n=NULL WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	defer failed.Close()
	for _, output := range []struct{ name, detail string }{{"hex", `Failing row contains (1, 2030-01-02, \x005cff, null, f).`}, {"escape", `Failing row contains (1, 2030-01-02, \000\\\377, null, f).`}} {
		exec(b, "SET bytea_output="+output.name, false, 0)
		for _, prepared := range []bool{false, true} {
			if prepared {
				_, err = failed.ExecContext(ctx, []byte{0, 92, 255})
			} else {
				_, err = b.ExecContext(ctx, "UPDATE composed SET b=$1,n=NULL WHERE id=1", []byte{0, 92, 255})
			}
			if err := typeCoreDiagnostic(err, stuntgres.Error{Code: "23502", Message: `null value in column "n" of relation "composed" violates not-null constraint`, Detail: output.detail, Schema: "public", Table: "composed", Column: "n"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	var compositionDump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &compositionDump); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(compositionDump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if err = b.QueryRowContext(ctx, "SELECT d,b,n,f FROM composed").Scan(&date, &blob, &number, &flag); err != nil || date != "2030-01-02" || !bytes.Equal(blob, []byte{65}) || number != 1 || flag {
		t.Fatal("composition roundtrip", date, blob, number, flag, err)
	}
	// One canonical own-head/live-source path through UPDATE, self INSERT SELECT
	// and DELETE, with independent peer visibility and no resurrection.
	exec(b, "CREATE TABLE mixed(id INT,v INT)", false, 0)
	exec(b, "INSERT INTO mixed VALUES(1,10),(2,20)", false, 2)
	mixedOld, err := b.QueryContext(ctx, "SELECT id,v FROM mixed ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer mixedOld.Close()
	mixedRows := func(x transactionExecutor, want [][]int64) {
		t.Helper()
		rows, err := x.QueryContext(ctx, "SELECT id,v FROM mixed ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		got := [][]int64{}
		for rows.Next() {
			var id, v int64
			if err := rows.Scan(&id, &v); err != nil {
				t.Fatal(err)
			}
			got = append(got, []int64{id, v})
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal(got, want)
		}
	}
	exec(a, "BEGIN", false, 0)
	exec(a, "UPDATE mixed SET v=30 WHERE id=1", false, 1)
	exec(a, "INSERT INTO mixed SELECT id,v FROM mixed WHERE id=1", true, 1)
	exec(a, "DELETE FROM mixed WHERE id=1", true, 2)
	mixedRows(a, [][]int64{{2, 20}})
	mixedRows(b, [][]int64{{1, 10}, {2, 20}})
	exec(a, "UPDATE mixed SET v=40 WHERE id=1", true, 0)
	exec(a, "ROLLBACK", false, 0)
	mixedRows(b, [][]int64{{1, 10}, {2, 20}})
	exec(a, "BEGIN", false, 0)
	exec(a, "UPDATE mixed SET v=30 WHERE id=1", false, 1)
	exec(a, "DELETE FROM mixed WHERE id=1", true, 1)
	exec(a, "COMMIT", false, 0)
	mixedRows(b, [][]int64{{2, 20}})
	var mixedDump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &mixedDump); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(mixedDump.Bytes())); err != nil {
		t.Fatal(err)
	}
	mixedRows(b, [][]int64{{2, 20}})
	oldMixed := [][]int64{}
	for mixedOld.Next() {
		var id, v int64
		if err := mixedOld.Scan(&id, &v); err != nil {
			t.Fatal(err)
		}
		oldMixed = append(oldMixed, []int64{id, v})
	}
	if err = mixedOld.Err(); err != nil || !reflect.DeepEqual(oldMixed, [][]int64{{1, 10}, {2, 20}}) {
		t.Fatal("retained mixed source", oldMixed, err)
	}
}
