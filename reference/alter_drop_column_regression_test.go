//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestReferenceAlterDropColumnQuotedExpressionDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, definition, drop, projection, filter, message string }{
		{"quote", `"q""u"`, `ALTER TABLE quoted DROP COLUMN "q""u"`, `/*é*/ SELECT "q""u" FROM quoted`, `/*é*/ SELECT a FROM quoted WHERE "q""u"=1`, `column "q"u" does not exist`},
		{"backslash", `"q\u"`, `ALTER TABLE quoted DROP COLUMN "q\u"`, `/*é*/ SELECT "q\u" FROM quoted`, `/*é*/ SELECT a FROM quoted WHERE "q\u"=1`, `column "q\u" does not exist`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferencePair(t)
			for _, db := range []*sql.DB{p.memory, p.postgres} {
				record := &referenceRecorder{}
				ctx := context.WithValue(p.ctx, referenceRecorderKey{}, record)
				referenceRunStep(t, ctx, db, record, referenceExec("CREATE TABLE quoted("+tc.definition+" INT,a INT)", 0))
				referenceRunStep(t, ctx, db, record, referenceExec("INSERT INTO quoted VALUES(1,7)", 1))
				queries := []string{tc.projection, tc.filter, strings.TrimPrefix(tc.projection, "/*é*/ "), strings.TrimPrefix(tc.filter, "/*é*/ ")}
				retained := make([]*sql.Stmt, len(queries))
				for i, q := range queries {
					s, err := db.PrepareContext(ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer s.Close()
					retained[i] = s
				}
				referenceRunStep(t, ctx, db, record, referenceExec(tc.drop, 0))
				for i, q := range queries {
					position := []string{"14", "34", "8", "28"}[i]
					want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: tc.message, Position: position}
					rows, err := retained[i].QueryContext(ctx)
					if rows != nil {
						rows.Close()
						t.Fatal("dropped expression returned rows")
					}
					referenceAssertDiagnostic(t, err, &want)
					failure := referenceImplicitError(q, want)
					failure.selectRows = true
					failure.errorStage = "execute"
					referenceRunStep(t, ctx, db, record, failure)
					failure.prepared = true
					failure.errorStage = "prepare"
					referenceRunStep(t, ctx, db, record, failure)
				}
				referenceRunStep(t, ctx, db, record, referenceSelect("SELECT * FROM quoted", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
		})
	}
}

// This PostgreSQL barrier independently witnesses the queued identity behavior.
// Native covers provider mutation and transactional commit/rollback AX barriers.
func TestReferenceAlterDropColumnQueuedSameIdentity(t *testing.T) {
	for _, q := range []string{"SELECT * FROM t ORDER BY a", "INSERT INTO t(a,c) VALUES(2,20)", "ALTER TABLE t DROP c", "ALTER TABLE t DROP IF EXISTS b"} {
		t.Run(q, func(t *testing.T) {
			p := newReferencePair(t)
			var notices []referenceDiagnostic
			_, queued, record := noticeReferencePool(t, p, true, &notices)
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, queued, record, s) }
			run(referenceExec("CREATE TABLE t(a INT,b TEXT,c BIGINT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1,'gone',9)", 1))
			notices = nil
			var pid int
			if err := queued.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
			tx, err := p.postgres.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err = tx.ExecContext(p.ctx, "ALTER TABLE t DROP b"); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
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
				if err = referenceAdmin.QueryRowContext(p.ctx, "SELECT count(*) FROM pg_locks WHERE pid=$1 AND relation='t'::regclass AND NOT granted", pid).Scan(&pending); err != nil {
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
				run(referenceSelect("SELECT * FROM t ORDER BY a", []string{"a", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(9)}, {int64(2), int64(20)}}))
			case "ALTER TABLE t DROP c":
				if got.count != 0 {
					t.Fatal(got.count)
				}
				run(referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
			case "ALTER TABLE t DROP IF EXISTS b":
				want := []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `column "b" of relation "t" does not exist, skipping`}}
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
			p := newReferencePair(t)
			ctx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
			defer cancel()
			a, waiter := referenceLockConnections(t, ctx, p.postgres)
			shadow, err := p.postgres.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer shadow.Close()
			defer func() {
				cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				if _, err := p.postgres.ExecContext(cleanup, "DROP SCHEMA IF EXISTS early, late CASCADE"); err != nil {
					t.Error("schema cleanup", err)
				}
			}()
			referenceLockExec(t, ctx, a, "CREATE SCHEMA early", 0)
			referenceLockExec(t, ctx, a, "CREATE SCHEMA late CREATE TABLE t(a INT,b TEXT,c BIGINT)", 0)
			referenceLockExec(t, ctx, a, "INSERT INTO late.t VALUES(1,'late',9)", 1)
			referenceLockExec(t, ctx, waiter, "SET search_path TO early, late", 0)
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
			referenceLockExec(t, ctx, lateTx, "LOCK TABLE late.t IN ACCESS EXCLUSIVE MODE", 0)
			done := make(chan relationOutcome, 1)
			go func() {
				out := relationOutcome{}
				if held != nil {
					out.result, out.err = held.ExecContext(ctx)
				} else {
					out.result, out.err = waiter.ExecContext(ctx, "ALTER TABLE t DROP b")
				}
				done <- out
			}()
			joined := false
			defer func() {
				cancel()
				if !joined {
					referenceLockResult(t, done)
				}
			}()
			schemaShadowWait(t, ctx, pid, "late.t")
			referenceLockExec(t, ctx, shadow, "CREATE TABLE early.t(a INT,b TEXT,c BIGINT)", 0)
			referenceLockExec(t, ctx, shadow, "INSERT INTO early.t VALUES(2,'early',20)", 1)
			earlyTx, err := shadow.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer earlyTx.Rollback()
			referenceLockExec(t, ctx, earlyTx, "LOCK TABLE early.t IN ACCESS EXCLUSIVE MODE", 0)
			if err = lateTx.Rollback(); err != nil {
				t.Fatal(err)
			}
			schemaShadowWait(t, ctx, pid, "early.t")
			// Catalog reads do not acquire a lock on early.t. Its original shape must
			// remain visible while the independently observed early AX wait is pending.
			for _, table := range []string{"early.t", "late.t"} {
				var columns int
				if err = referenceAdmin.QueryRowContext(ctx, "SELECT count(*) FROM pg_attribute WHERE attrelid=$1::regclass AND attnum>0 AND NOT attisdropped", table).Scan(&columns); err != nil || columns != 3 {
					t.Fatalf("blocked %s shape: columns=%d err=%v", table, columns, err)
				}
			}
			if err = earlyTx.Rollback(); err != nil {
				t.Fatal(err)
			}
			out := referenceLockResult(t, done)
			joined = true
			if out.err != nil {
				t.Fatal(out.err)
			}
			referenceLockAffected(t, out.result, 0)
			record := &referenceRecorder{}
			referenceRunStep(t, ctx, waiter, record, referenceSelect("SELECT * FROM early.t", []string{"a", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(2), int64(20)}}))
			referenceRunStep(t, ctx, waiter, record, referenceSelect("SELECT * FROM late.t", []string{"a", "b", "c"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(1), "late", int64(9)}}))
			t.Log("pinned PostgreSQL utility reacquired shadow early.t AX, then dropped only early.b")
		})
	}
}
