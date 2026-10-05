//go:build reference

package reference_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestReferenceTransactionControlsLockLifecycle(t *testing.T) {
	pair := newReferencePair(t)
	for _, postgres := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "postgres"}[postgres], func(t *testing.T) {
			f := newControlsReferenceFixture(t, pair, postgres)
			ctx := pair.ctx
			for _, q := range []string{"COMMIT AND CHAIN", "ROLLBACK AND CHAIN", "ABORT AND CHAIN"} {
				f.exec(t, f.b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
				pid := 0
				if postgres {
					if err := f.b.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
						t.Fatal(err)
					}
				}
				f.exec(t, f.a, "BEGIN", false, 0)
				f.exec(t, f.a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", false, 0)
				f.exec(t, f.a, "INSERT INTO t VALUES(1)", false, 1)
				end, err := f.a.PrepareContext(ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				f.noticesWant(t, "", "")
				if !postgres {
					blocked, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
					_, err = f.b.QueryContext(blocked, "SELECT v FROM t")
					cancel()
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Fatal("Describe released native hold", err)
					}
				}
				done := make(chan relationOutcome, 1)
				go func() {
					rows, err := f.b.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
					done <- relationOutcome{rows: rows, err: err}
				}()
				if postgres {
					referenceLockWait(t, ctx, pid)
				}
				r, err := end.ExecContext(ctx)
				end.Close()
				if err != nil {
					t.Fatal(err)
				}
				referenceLockAffected(t, r, 0)
				out := referenceLockResult(t, done)
				if out.err != nil {
					t.Fatal(out.err)
				}
				want := []int64{}
				if strings.HasPrefix(q, "COMMIT") {
					want = []int64{1}
				}
				referenceTransactionValues(t, out.rows, want...)
				f.exec(t, f.a, "INSERT INTO t VALUES(2)", false, 1)
				f.rows(t, f.b, want...)
				f.exec(t, f.a, "ROLLBACK", false, 0)
				f.exec(t, f.a, "BEGIN", false, 0)
				f.exec(t, f.a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", false, 0)
				wantNull := referenceTransactionNull()
				referenceAssertDiagnostic(t, transactionConstraintExec(t, ctx, f.a, "INSERT INTO t VALUES(NULL)", true), &wantNull)
				f.rows(t, f.b, want...)
				f.exec(t, f.a, "COMMIT AND CHAIN", true, 0)
				f.exec(t, f.a, "ROLLBACK", false, 0)
				f.exec(t, f.b, "DROP TABLE t", false, 0)
			}
		})
	}
}
func TestReferenceTransactionControlsCancellation(t *testing.T) {
	pair := newReferencePair(t)
	for _, postgres := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "postgres"}[postgres], func(t *testing.T) {
			f := newControlsReferenceFixture(t, pair, postgres)
			ctx := pair.ctx
			f.exec(t, f.b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
			f.exec(t, f.b, "INSERT INTO t VALUES(77)", false, 1)
			f.exec(t, f.a, "SET statement_timeout TO '20ms'", false, 0)
			f.exec(t, f.b, "BEGIN", false, 0)
			f.exec(t, f.b, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", false, 0)
			f.exec(t, f.a, "BEGIN", false, 0)
			_, err := f.a.ExecContext(ctx, "INSERT INTO t VALUES(2)")
			want := controlsReferenceDiagnostic("57014", "canceling statement due to statement timeout")
			referenceAssertDiagnostic(t, err, &want)
			_, err = f.a.ExecContext(ctx, "SELECT v FROM t")
			want = referenceTransactionAborted()
			referenceAssertDiagnostic(t, err, &want)
			f.exec(t, f.a, "ROLLBACK AND CHAIN", true, 0)
			f.exec(t, f.a, "ROLLBACK", false, 0)
			f.exec(t, f.a, "SET statement_timeout TO 0", false, 0)
			if postgres {
				pid := 0
				if err = f.a.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
					t.Fatal(err)
				}
				f.exec(t, f.a, "BEGIN", false, 0)
				done := make(chan relationOutcome, 1)
				go func() {
					r, err := f.a.ExecContext(ctx, "INSERT INTO t VALUES(3)")
					done <- relationOutcome{result: r, err: err}
				}()
				referenceLockWait(t, ctx, pid)
				var canceled bool
				if err = referenceAdmin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
					t.Fatal(canceled, err)
				}
				out := referenceLockResult(t, done)
				want = controlsReferenceDiagnostic("57014", "canceling statement due to user request")
				referenceAssertDiagnostic(t, out.err, &want)
				f.exec(t, f.a, "COMMIT", false, 0)
			} else {
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				_, err = f.a.ExecContext(canceled, "BEGIN")
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				f.exec(t, f.a, "BEGIN", false, 0)
				bounded, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
				_, err = f.a.ExecContext(bounded, "INSERT INTO t VALUES(3)")
				cancel()
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal(err)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					t.Fatal("ordinary native caller context fabricated SQLSTATE", err)
				}
				f.exec(t, f.a, "COMMIT", false, 0)
			}
			f.exec(t, f.b, "ROLLBACK", false, 0)
			f.rows(t, f.b, 77)
			f.exec(t, f.a, "INSERT INTO t VALUES(9)", true, 1)
			f.rows(t, f.b, 9, 77)
		})
	}
}
