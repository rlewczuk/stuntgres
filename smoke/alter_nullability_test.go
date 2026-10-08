package smoke_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func TestAlterNullabilitySmoke(t *testing.T) {
	var notices []stuntgres.Error
	db := alterDropSmokePool(t, &notices)
	db.SetMaxOpenConns(2)
	notNullSmokeExec(t, db, false, "CREATE TABLE t(a INT,b TEXT)", 0)
	notNullSmokeExec(t, db, false, "INSERT INTO t VALUES(NULL,'old')", 1)
	_, err := db.Exec("ALTER TABLE t ALTER a SET NOT NULL")
	notNullSmokeDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"})
	notNullSmokeExec(t, db, true, "UPDATE t SET a=7", 1)
	insert, err := db.Prepare("INSERT INTO t VALUES($1,$2)")
	if err != nil {
		t.Fatal(err)
	}
	defer insert.Close()
	notNullSmokeExec(t, db, true, "ALTER TABLE t ADD CONSTRAINT nn NOT NULL a", 0)
	_, err = insert.Exec(nil, "bad")
	notNullSmokeDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "t", Column: "a"})
	notNullSmokeExec(t, db, true, "ALTER TABLE t DROP CONSTRAINT nn", 0)
	result, err := insert.Exec(nil, "after")
	if err != nil {
		t.Fatal(err)
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	_, err = db.Exec("ALTER TABLE t DROP COLUMN b, ALTER missing SET NOT NULL")
	notNullSmokeDiagnostic(t, err, stuntgres.Error{Code: "42703", Message: `column "missing" of relation "t" does not exist`})
	notNullSmokeQuery(t, db, "SELECT * FROM t ORDER BY b", []string{"a", "b"}, []string{"INT4", "TEXT"}, []bool{true, true}, [][]any{{nil, "after"}, {int64(7), "old"}})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	peer, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	run := func(q string, count int64) {
		t.Helper()
		r, e := tx.Exec(q)
		if e != nil {
			t.Fatal(q, e)
		}
		n, e := r.RowsAffected()
		if e != nil || n != count {
			t.Fatal(q, n, e)
		}
	}
	run("DELETE FROM t WHERE b='after'", 1)
	run("SAVEPOINT s", 0)
	run("ALTER TABLE t DROP COLUMN b, ALTER a SET NOT NULL", 0)
	run("INSERT INTO t VALUES(8)", 1)
	run("ROLLBACK TO s", 0)
	run("ALTER TABLE t DROP COLUMN b, ALTER a SET NOT NULL", 0)
	run("INSERT INTO t VALUES(9)", 1)
	run("UPDATE t SET a=10 WHERE a=9", 1)
	blocked, stop := context.WithTimeout(ctx, 50*time.Millisecond)
	rows, waitErr := peer.QueryContext(blocked, "SELECT * FROM t")
	stop()
	if rows != nil {
		rows.Close()
	}
	if !errors.Is(waitErr, context.DeadlineExceeded) {
		t.Fatal("peer was not blocked by private ALTER", waitErr)
	}
	// The deadline probe proves native blocking, not enqueue of a later call.
	// Exact queued transitions are observed by the engine and pinned-PG fixtures.
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rows, err = peer.QueryContext(ctx, "SELECT * FROM t ORDER BY a")
	if err != nil {
		t.Fatal(err)
	}
	notNullSmokeRows(t, rows, []string{"a"}, []string{"INT4"}, []bool{false}, [][]any{{int64(7)}, {int64(10)}})
	notNullSmokeQuery(t, db, "SELECT * FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, []bool{false}, [][]any{{int64(7)}, {int64(10)}})
}

func TestAlterNullabilityControlsSmoke(t *testing.T) {
	var notices []stuntgres.Error
	db := alterDropSmokePool(t, &notices)
	db.SetMaxOpenConns(3)
	ctx := context.Background()
	notNullSmokeExec(t, db, false, "CREATE TABLE t(a INT,b TEXT DEFAULT 'keep')", 0)
	notNullSmokeExec(t, db, false, "INSERT INTO t VALUES(7,'seed')", 1)
	dump := func() string {
		t.Helper()
		var b bytes.Buffer
		if e := stuntgres.Dump(ctx, db, &b); e != nil {
			t.Fatal(e)
		}
		return b.String()
	}
	before := dump()
	held, err := db.Query("SELECT * FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`ALTER TABLE t ADD CONSTRAINT "n""é" NOT NULL a, ALTER b SET NOT NULL`); err != nil {
		t.Fatal(err)
	}
	if got := dump(); got != before {
		t.Fatal("uncommitted descriptor leaked")
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := dump(); got != before {
		t.Fatal("rollback changed dump")
	}
	notNullSmokeExec(t, db, true, `ALTER TABLE t ADD CONSTRAINT "n""é" NOT NULL a, ALTER b SET NOT NULL`, 0)
	final := dump()
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, strings.NewReader(final)); err != nil {
		t.Fatal(err)
	}
	if got := dump(); got != final {
		t.Fatal("roundtrip changed logical state")
	}
	if !held.Next() {
		t.Fatal(held.Err())
	}
	var a int64
	var b string
	if err = held.Scan(&a, &b); err != nil || a != 7 || b != "seed" {
		t.Fatal(a, b, err)
	}
	held.Close()
	notNullSmokeQuery(t, db, "SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, []bool{false, false}, [][]any{{int64(7), "seed"}})
	err = stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE replacement(a INT NOT NULL);INSERT INTO replacement VALUES(NULL)"))
	notNullSmokeDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `null value in column "a" of relation "replacement" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "replacement", Column: "a"})
	if got := dump(); got != final {
		t.Fatal("failed restore lost sentinel")
	}
	notNullSmokeExec(t, db, true, `ALTER TABLE t DROP CONSTRAINT "n""é", DROP CONSTRAINT t_b_not_null`, 0)
	notNullSmokeExec(t, db, false, "INSERT INTO t VALUES(NULL,NULL)", 1)
}
