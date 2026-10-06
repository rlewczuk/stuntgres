//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"regexp"
	"runtime"
	"testing"
)

func referenceUpdateWait(t *testing.T, ctx context.Context, pid int) {
	t.Helper()
	for {
		var waiting bool
		if err := referenceAdmin.QueryRowContext(ctx, `SELECT cardinality(pg_blocking_pids($1)) > 0 AND EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND NOT granted)`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		runtime.Gosched()
	}
}

func TestReferenceUpdateCoreRowRecheckOracle(t *testing.T) {
	for _, mode := range []string{"commit", "rollback", "predicate", "cancel"} {
		for _, prepared := range []bool{false, true} {
			name := mode + map[bool]string{false: "/direct", true: "/prepared"}[prepared]
			t.Run(name, func(t *testing.T) {
				p := newReferencePair(t)
				ctx := p.ctx
				a, b := referenceLockConnections(t, ctx, p.postgres)
				c, err := p.postgres.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				referenceLockExec(t, ctx, c, "CREATE TABLE t(id INT,a INT,b INT)", 0)
				referenceLockExec(t, ctx, c, "INSERT INTO t VALUES(1,10,0),(2,20,0)", 2)
				var pid int
				if err := b.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
					t.Fatal(err)
				}
				q := "UPDATE t SET b=a WHERE id=1"
				var stmt *sql.Stmt
				if prepared {
					stmt, err = b.PrepareContext(ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
				}
				tx, err := a.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				referenceLockExec(t, ctx, tx, "UPDATE t SET a=11 WHERE id=1", 1)
				if mode == "predicate" {
					referenceLockExec(t, ctx, tx, "UPDATE t SET id=3 WHERE id=1", 1)
					referenceLockExec(t, ctx, tx, "UPDATE t SET id=1 WHERE id=2", 1)
				}
				done := make(chan relationOutcome, 1)
				go func() {
					var o relationOutcome
					if prepared {
						o.result, o.err = stmt.ExecContext(ctx)
					} else {
						o.result, o.err = b.ExecContext(ctx, q)
					}
					done <- o
				}()
				referenceUpdateWait(t, ctx, pid)
				// These complete before releasing row1: readers do not take tuple locks
				// and RX on the table does not serialize a distinct logical row.
				var old int64
				if err := c.QueryRowContext(ctx, "SELECT a FROM t WHERE id=1").Scan(&old); err != nil || old != 10 {
					t.Fatal(old, err)
				}
				if mode != "predicate" {
					referenceLockExec(t, ctx, c, "UPDATE t SET b=20 WHERE id=2", 1)
				}
				referenceLockExec(t, ctx, c, "INSERT INTO t VALUES(1,99,0)", 1)
				if mode == "cancel" {
					var canceled bool
					if err := referenceAdmin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
						t.Fatal(canceled, err)
					}
					o := referenceLockResult(t, done)
					diagnostic := referenceExtractDiagnostic(t, o.err)
					if !regexp.MustCompile(`^while updating tuple \([0-9]+,[1-9][0-9]*\) in relation "t"$`).MatchString(diagnostic.Context) {
						t.Fatalf("tuple wait cancellation context: %#v", diagnostic)
					}
					want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request", Context: diagnostic.Context}
					referenceAssertDiagnostic(t, o.err, &want)
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					referenceLockExec(t, ctx, b, "UPDATE t SET b=a WHERE a=10", 1)
				} else {
					if mode == "rollback" {
						err = tx.Rollback()
					} else {
						err = tx.Commit()
					}
					if err != nil {
						t.Fatal(err)
					}
					o := referenceLockResult(t, done)
					if o.err != nil {
						t.Fatal(o.err)
					}
					count := int64(1)
					if mode == "predicate" {
						count = 0
					}
					referenceLockAffected(t, o.result, count)
				}
				expected := int64(11)
				if mode == "rollback" || mode == "cancel" {
					expected = 10
				}
				if mode == "predicate" {
					expected = 0
				}
				var got int64
				if mode == "commit" || mode == "predicate" {
					if err := c.QueryRowContext(ctx, "SELECT b FROM t WHERE a=11").Scan(&got); err != nil || got != expected {
						t.Fatal(got, err, expected)
					}
				}
				if mode == "rollback" || mode == "cancel" {
					if err := c.QueryRowContext(ctx, "SELECT b FROM t WHERE a=10").Scan(&got); err != nil || got != expected {
						t.Fatal(got, err)
					}
				}
				if err := c.QueryRowContext(ctx, "SELECT b FROM t WHERE a=99").Scan(&got); err != nil || got != 0 {
					t.Fatal("concurrent insert discovered", got, err)
				}
				if mode == "predicate" {
					if err := c.QueryRowContext(ctx, "SELECT b FROM t WHERE a=20").Scan(&got); err != nil || got != 0 {
						t.Fatal("initially rejected row discovered", got, err)
					}
				}
			})
		}
	}
}

func TestReferenceUpdateCoreVisibility(t *testing.T) {
	p := newReferencePair(t)
	for i, db := range []*sql.DB{p.memory, p.postgres} {
		t.Run([]string{"memory", "postgres"}[i], func(t *testing.T) {
			a, b := referenceLockConnections(t, p.ctx, db)
			referenceLockExec(t, p.ctx, b, "CREATE TABLE t(v INT)", 0)
			tx, err := a.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceLockExec(t, p.ctx, tx, "INSERT INTO t VALUES(1)", 1)
			referenceLockExec(t, p.ctx, tx, "UPDATE t SET v=2", 1)
			referenceLockExec(t, p.ctx, tx, "UPDATE t SET v=3", 1)
			var own int64
			if err := tx.QueryRowContext(p.ctx, "SELECT v FROM t").Scan(&own); err != nil || own != 3 {
				t.Fatal(own, err)
			}
			var peer int64
			if err := b.QueryRowContext(p.ctx, "SELECT v FROM t").Scan(&peer); err != sql.ErrNoRows {
				t.Fatal("private row leaked", peer, err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := b.QueryRowContext(p.ctx, "SELECT v FROM t").Scan(&peer); err != nil || peer != 3 {
				t.Fatal(peer, err)
			}
			tx, err = a.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceLockExec(t, p.ctx, tx, "UPDATE t SET v=4", 1)
			if err := b.QueryRowContext(p.ctx, "SELECT v FROM t").Scan(&peer); err != nil || peer != 3 {
				t.Fatal(peer, err)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err := b.QueryRowContext(p.ctx, "SELECT v FROM t").Scan(&peer); err != nil || peer != 3 {
				t.Fatal(peer, err)
			}
		})
	}
}
