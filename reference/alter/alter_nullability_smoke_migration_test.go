package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceAlterNullabilitySmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { migrationAlterNullabilitySmoke(t, p, backend) })
	}
}
func TestReferenceAlterNullabilityControlsSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { migrationAlterNullabilityControlsSmoke(t, p, backend) })
	}
}

func migrationAlterNullabilitySmoke(t *testing.T, p *fixture.Pair, backend fixture.Backend) {
	m, db, ctx, _ := fixture.MigrationAlterFixture(t, p, backend)
	db.SetMaxOpenConns(2)
	m.NullExec(db, false, "CREATE TABLE t(a INT,b TEXT)", 0)
	m.NullExec(db, false, "INSERT INTO t VALUES(NULL,'old')", 1)
	_, err := db.Exec("ALTER TABLE t ALTER a SET NOT NULL")
	m.NullDiagnostic(err, stuntgres.Error{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"})
	m.NullExec(db, true, "UPDATE t SET a=7", 1)
	insert, err := db.Prepare("INSERT INTO t VALUES($1,$2)")
	if err != nil {
		t.Fatal(err)
	}
	defer insert.Close()
	m.NullExec(db, true, "ALTER TABLE t ADD CONSTRAINT nn NOT NULL a", 0)
	_, err = insert.Exec(nil, "bad")
	m.NullDiagnostic(err, stuntgres.Error{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "t", Column: "a"})
	m.NullExec(db, true, "ALTER TABLE t DROP CONSTRAINT nn", 0)
	result, err := insert.Exec(nil, "after")
	if err != nil {
		t.Fatal(err)
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	_, err = db.Exec("ALTER TABLE t DROP COLUMN b, ALTER missing SET NOT NULL")
	m.NullDiagnostic(err, stuntgres.Error{Code: "42703", Message: `column "missing" of relation "t" does not exist`})
	m.NullQuery(db, "SELECT * FROM t ORDER BY b", []string{"a", "b"}, []string{"INT4", "TEXT"}, []bool{true, true}, [][]any{{nil, "after"}, {int64(7), "old"}})
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
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
	if backend.PG {
		m.NullDiagnostic(waitErr, stuntgres.Error{Code: "57014", Message: "canceling statement due to user request"})
	} else if !errors.Is(waitErr, context.DeadlineExceeded) {
		t.Fatal("peer was not blocked by private ALTER", waitErr)
	}
	// The deadline probe proves native blocking, not enqueue of a later call.
	// Exact queued transitions are observed by the engine and pinned-PG fixtures.
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if backend.PG {
		// lib/pq discards the physical connection after request cancellation.
		// Native deliberately retains the same borrower for this recovery probe.
		if err = peer.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
			t.Fatal(err)
		}
		peer, err = db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
	}
	rows, err = peer.QueryContext(ctx, "SELECT * FROM t ORDER BY a")
	if err != nil {
		t.Fatal(err)
	}
	m.NullRows(rows, []string{"a"}, []string{"INT4"}, []bool{false}, [][]any{{int64(7)}, {int64(10)}})
	m.NullQuery(db, "SELECT * FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, []bool{false}, [][]any{{int64(7)}, {int64(10)}})
}

func migrationAlterNullabilityControlsSmoke(t *testing.T, p *fixture.Pair, backend fixture.Backend) {
	m, db, ctx, _ := fixture.MigrationAlterFixture(t, p, backend)
	db.SetMaxOpenConns(3)
	m.NullExec(db, false, "CREATE TABLE t(a INT,b TEXT DEFAULT 'keep')", 0)
	m.NullExec(db, false, "INSERT INTO t VALUES(7,'seed')", 1)
	dump := func() string {
		t.Helper()
		if backend.PG {
			return ""
		}
		var b bytes.Buffer
		if e := stuntgres.Dump(ctx, p.Native, &b); e != nil {
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
	m.NullExec(db, true, `ALTER TABLE t ADD CONSTRAINT "n""é" NOT NULL a, ALTER b SET NOT NULL`, 0)
	final := dump()
	if !backend.PG {
		if err = stuntgres.Reset(ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err = stuntgres.Restore(ctx, p.Native, strings.NewReader(final)); err != nil {
			t.Fatal(err)
		}
		if got := dump(); got != final {
			t.Fatal("roundtrip changed logical state")
		}
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
	m.NullQuery(db, "SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, []bool{false, false}, [][]any{{int64(7), "seed"}})
	if !backend.PG {
		err = stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE TABLE replacement(a INT NOT NULL);INSERT INTO replacement VALUES(NULL)"))
		m.NullDiagnostic(err, stuntgres.Error{Code: "23502", Message: `null value in column "a" of relation "replacement" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "replacement", Column: "a"})
		if got := dump(); got != final {
			t.Fatal("failed restore lost sentinel")
		}
	}
	m.NullExec(db, true, `ALTER TABLE t DROP CONSTRAINT "n""é", DROP CONSTRAINT t_b_not_null`, 0)
	m.NullExec(db, false, "INSERT INTO t VALUES(NULL,NULL)", 1)
}
