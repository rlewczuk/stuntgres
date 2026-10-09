package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestReferenceTransactionControlsLockLifecycle(t *testing.T) {
	pair := newReferenceScenario(t)
	for _, backend := range pair.backends() {
		t.Run(backend.name, func(t *testing.T) {
			postgres := backend.pg
			f := newControlsReferenceFixture(t, pair, postgres)
			ctx := pair.ctx
			for _, q := range []string{"COMMIT AND CHAIN", "ROLLBACK AND CHAIN", "ABORT AND CHAIN"} {
				f.exec(t, f.b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
				pid, holderPID := 0, 0
				var relationOID int64
				if postgres {
					pid = referenceLockPID(t, ctx, f.b)
					holderPID = referenceLockPID(t, ctx, f.a)
					relationOID = referenceLockOID(t, ctx, "public.t")
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
				await := referenceLockOperation(t, ctx, func(ctx context.Context) relationOutcome {
					rows, err := f.b.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
					return relationOutcome{rows: rows, err: err}
				})
				if postgres {
					referenceLockWaitBehind(t, ctx, pid, holderPID, relationOID, "AccessShareLock")
				}
				r, err := end.ExecContext(ctx)
				end.Close()
				if err != nil {
					t.Fatal(err)
				}
				referenceLockAffected(t, r, 0)
				out := await()
				if out.err != nil {
					t.Fatal(out.err)
				}
				want := []int64{}
				if strings.HasPrefix(q, "COMMIT") {
					want = []int64{1}
				}
				referenceLimitLockValues(t, out, postgres, want...)
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

// pq's canceled-context watcher can finish normally or retire the connection.
// Each exact outcome must discard private work before borrowing the next owner.
func referenceTransactionPreCanceledRetirement(t *testing.T, f *controlsReferenceFixture, query, next string, prior ...int64) {
	t.Helper()
	ctx := f.pair.ctx
	oldPID := referenceLockPID(t, ctx, f.a)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	r, err := f.a.ExecContext(canceled, query)
	if err == nil {
		if r == nil {
			t.Fatal("successful pre-canceled control returned no result")
		}
		referenceLockAffected(t, r, 0)
		r, err = f.a.ExecContext(ctx, next)
		if err == nil {
			if r == nil {
				t.Fatal("successful subsequent control returned no result")
			}
			referenceLockAffected(t, r, 0)
		}
	}
	retired := errors.Is(err, driver.ErrBadConn)
	if err != nil {
		if !retired || r != nil {
			t.Fatal("unexpected pq pre-canceled control outcome", r, err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("physical retirement fabricated SQLSTATE", err)
		}
	}
	if retired {
		if err := f.a.Close(); !errors.Is(err, sql.ErrConnDone) {
			t.Fatal("retired borrower Close", err)
		}
	} else {
		// Successful ROLLBACK already ended BEGIN; ROLLBACK TO leaves the
		// SAVEPOINT cohort active and needs its actual outer rollback.
		if next != "ROLLBACK" {
			f.exec(t, f.a, "ROLLBACK", false, 0)
		}
		if err := f.a.Close(); err != nil {
			t.Fatal("healthy pre-canceled borrower Close", err)
		}
	}
	f.rows(t, f.b, prior...)
	f.a, err = f.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner := f.a
	t.Cleanup(func() {
		owner.ExecContext(ctx, "ROLLBACK")
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	})
	if pid := referenceLockPID(t, ctx, owner); pid <= 0 || (retired && pid == oldPID) {
		t.Fatal("invalid reopened owner PID", oldPID, pid, "retired", retired)
	}
	f.rows(t, owner, prior...)
}

func TestReferenceTransactionControlsCancellation(t *testing.T) {
	for _, committedOne := range []bool{false, true} {
		t.Run(map[bool]string{false: "reference_77", true: "original_1_77"}[committedOne], func(t *testing.T) {
			pair := newReferenceScenario(t)
			for _, backend := range pair.backends() {
				t.Run(backend.name, func(t *testing.T) {
					postgres := backend.pg
					f := newControlsReferenceFixture(t, pair, postgres)
					ctx := pair.ctx
					f.exec(t, f.b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
					f.exec(t, f.b, "INSERT INTO t VALUES(77)", false, 1)
					if committedOne {
						if postgres {
							referenceTransactionPreCanceledRetirement(t, &f, "BEGIN", "ROLLBACK", 77)
						} else {
							canceled, cancel := context.WithCancel(ctx)
							cancel()
							r, err := f.a.ExecContext(canceled, "BEGIN")
							if r != nil || !errors.Is(err, context.Canceled) {
								t.Fatal(r, err)
							}
							var state interface{ SQLState() string }
							if errors.As(err, &state) {
								t.Fatal("pre-canceled BEGIN fabricated SQLSTATE", err)
							}
						}
						f.exec(t, f.a, "INSERT INTO t VALUES(1)", false, 1)
						f.rows(t, f.b, 1, 77)
					}
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
					f.exec(t, f.a, "ROLLBACK AND CHAIN", !committedOne, 0)
					f.exec(t, f.a, "ROLLBACK", false, 0)
					f.exec(t, f.a, "SET statement_timeout TO 0", false, 0)
					if postgres {
						pid := 0
						if err = f.a.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
							t.Fatal(err)
						}
						f.exec(t, f.a, "BEGIN", false, 0)
						await := referenceLockOperation(t, ctx, func(ctx context.Context) relationOutcome {
							r, err := f.a.ExecContext(ctx, "INSERT INTO t VALUES(3)")
							return relationOutcome{result: r, err: err}
						})
						holderPID := 0
						if err := f.b.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&holderPID); err != nil {
							t.Fatal(err)
						}
						referenceLockWaitBehind(t, ctx, pid, holderPID, referenceLockOID(t, ctx, "t"), "RowExclusiveLock")
						var canceled bool
						if err = referenceAdmin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
							t.Fatal(canceled, err)
						}
						out := await()
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
					if committedOne {
						f.rows(t, f.b, 1, 77)
					} else {
						f.rows(t, f.b, 77)
					}
					f.exec(t, f.a, "INSERT INTO t VALUES(9)", true, 1)
					if committedOne {
						f.rows(t, f.b, 1, 9, 77)
					} else {
						f.rows(t, f.b, 9, 77)
					}
				})
			}
		})
	}
}
