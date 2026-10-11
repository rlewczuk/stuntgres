//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"runtime"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// This PostgreSQL barrier independently witnesses the queued identity behavior.
// Native covers provider mutation and transactional commit/rollback AX barriers.
func TestReferenceAlterDropColumnQueuedSameIdentity(t *testing.T) {
	for _, q := range []string{"SELECT * FROM t ORDER BY a", "INSERT INTO t(a,c) VALUES(2,20)", "ALTER TABLE t DROP c", "ALTER TABLE t DROP IF EXISTS b"} {
		t.Run(q, func(t *testing.T) {
			p := fixture.NewPair(t)
			var notices []fixture.Diagnostic
			_, queued, record := fixture.NoticeReferencePool(t, p, true, &notices)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, queued, record, s) }
			run(fixture.Exec("CREATE TABLE t(a INT,b TEXT,c BIGINT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,'gone',9)", 1))
			notices = nil
			var pid int
			if err := queued.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
			tx, err := p.Postgres.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err = tx.ExecContext(p.Ctx, "ALTER TABLE t DROP b"); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
			defer cancel()
			type outcome struct {
				err           error
				count         int64
				labels, types []string
				values        [][]any
			}
			done := make(chan outcome, 1)
			go func() {
				got := outcome{}
				if q == "SELECT * FROM t ORDER BY a" {
					rows, err := queued.QueryContext(ctx, q)
					got.err = err
					if err == nil {
						got.labels, got.err = rows.Columns()
						metadata, e := rows.ColumnTypes()
						if got.err == nil {
							got.err = e
						}
						for _, c := range metadata {
							got.types = append(got.types, c.DatabaseTypeName())
						}
						for rows.Next() {
							var a, c int64
							if e := rows.Scan(&a, &c); e != nil {
								got.err = e
								break
							}
							got.values = append(got.values, []any{a, c})
						}
						if got.err == nil {
							got.err = rows.Err()
						}
						rows.Close()
					}
				} else {
					r, err := queued.ExecContext(ctx, q)
					got.err = err
					if err == nil {
						got.count, got.err = r.RowsAffected()
					}
				}
				done <- got
			}()
			deadline := time.Now().Add(5 * time.Second)
			for {
				var pending int
				if err = fixture.Admin.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_locks WHERE pid=$1 AND relation='t'::regclass AND NOT granted", pid).Scan(&pending); err != nil {
					t.Fatal(err)
				}
				if pending == 1 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("oracle operation never queued for relation lock")
				}
				runtime.Gosched()
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			got := <-done
			if got.err != nil {
				t.Fatal(got.err)
			}
			switch q {
			case "SELECT * FROM t ORDER BY a":
				if !reflect.DeepEqual(got.labels, []string{"a", "c"}) || !reflect.DeepEqual(got.types, []string{"INT4", "INT8"}) || !reflect.DeepEqual(got.values, [][]any{{int64(1), int64(9)}}) {
					t.Fatal(got)
				}
			case "INSERT INTO t(a,c) VALUES(2,20)":
				if got.count != 1 {
					t.Fatal(got.count)
				}
				run(fixture.Select("SELECT * FROM t ORDER BY a", []string{"a", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(9)}, {int64(2), int64(20)}}))
			case "ALTER TABLE t DROP c":
				if got.count != 0 {
					t.Fatal(got.count)
				}
				run(fixture.Select("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
			case "ALTER TABLE t DROP IF EXISTS b":
				want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `column "b" of relation "t" does not exist, skipping`}}
				if got.count != 0 || !reflect.DeepEqual(notices, want) {
					t.Fatal(fmt.Sprint(got), notices, want)
				}
			}
		})
	}
}

// Unlike native's bounded exact-resource fence, PostgreSQL retries RangeVar
// resolution and must obtain the shadow's AX before performing this utility.
func TestReferenceAlterDropColumnLockedBindingShadowCreation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		phase := "direct"
		if prepared {
			phase = "retained-prepared"
		}
		t.Run(phase, func(t *testing.T) {
			p := fixture.NewPair(t)
			ctx, cancel := context.WithTimeout(p.Ctx, 10*time.Second)
			defer cancel()
			a, waiter := fixture.LockConnections(t, ctx, p.Postgres)
			shadow, err := p.Postgres.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer shadow.Close()
			defer func() {
				cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				if _, err := p.Postgres.ExecContext(cleanup, "DROP SCHEMA IF EXISTS early, late CASCADE"); err != nil {
					t.Error("schema cleanup", err)
				}
			}()
			fixture.LockExec(t, ctx, a, "CREATE SCHEMA early", 0)
			fixture.LockExec(t, ctx, a, "CREATE SCHEMA late CREATE TABLE t(a INT,b TEXT,c BIGINT)", 0)
			fixture.LockExec(t, ctx, a, "INSERT INTO late.t VALUES(1,'late',9)", 1)
			fixture.LockExec(t, ctx, waiter, "SET search_path TO early, late", 0)
			var held *sql.Stmt
			if prepared {
				held, err = waiter.PrepareContext(ctx, "ALTER TABLE t DROP b")
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
			}
			var pid int
			if err = waiter.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
			lateTx, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer lateTx.Rollback()
			fixture.LockExec(t, ctx, lateTx, "LOCK TABLE late.t IN ACCESS EXCLUSIVE MODE", 0)
			done := make(chan fixture.RelationOutcome, 1)
			go func() {
				out := fixture.RelationOutcome{}
				if held != nil {
					out.Result, out.Err = held.ExecContext(ctx)
				} else {
					out.Result, out.Err = waiter.ExecContext(ctx, "ALTER TABLE t DROP b")
				}
				done <- out
			}()
			joined := false
			defer func() {
				cancel()
				if !joined {
					fixture.LockResult(t, done)
				}
			}()
			fixture.SchemaShadowWait(t, ctx, pid, "late.t")
			fixture.LockExec(t, ctx, shadow, "CREATE TABLE early.t(a INT,b TEXT,c BIGINT)", 0)
			fixture.LockExec(t, ctx, shadow, "INSERT INTO early.t VALUES(2,'early',20)", 1)
			earlyTx, err := shadow.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer earlyTx.Rollback()
			fixture.LockExec(t, ctx, earlyTx, "LOCK TABLE early.t IN ACCESS EXCLUSIVE MODE", 0)
			if err = lateTx.Rollback(); err != nil {
				t.Fatal(err)
			}
			fixture.SchemaShadowWait(t, ctx, pid, "early.t")
			// Catalog reads do not acquire a lock on early.t. Its original shape must
			// remain visible while the independently observed early AX wait is pending.
			for _, table := range []string{"early.t", "late.t"} {
				var columns int
				if err = fixture.Admin.QueryRowContext(ctx, "SELECT count(*) FROM pg_attribute WHERE attrelid=$1::regclass AND attnum>0 AND NOT attisdropped", table).Scan(&columns); err != nil || columns != 3 {
					t.Fatalf("blocked %s shape: columns=%d err=%v", table, columns, err)
				}
			}
			if err = earlyTx.Rollback(); err != nil {
				t.Fatal(err)
			}
			out := fixture.LockResult(t, done)
			joined = true
			if out.Err != nil {
				t.Fatal(out.Err)
			}
			fixture.LockAffected(t, out.Result, 0)
			record := &fixture.Recorder{}
			fixture.RunStep(t, ctx, waiter, record, fixture.Select("SELECT * FROM early.t", []string{"a", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(2), int64(20)}}))
			fixture.RunStep(t, ctx, waiter, record, fixture.Select("SELECT * FROM late.t", []string{"a", "b", "c"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(1), "late", int64(9)}}))
			t.Log("pinned PostgreSQL utility reacquired shadow early.t AX, then dropped only early.b")
		})
	}
}
