package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTransactionControlsLockLifecycle(t *testing.T) {
	pair := fixture.NewScenario(t)
	for _, backend := range pair.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			postgres := backend.PG
			f := fixture.NewControlsReferenceFixture(t, pair, postgres)
			ctx := pair.Ctx
			for _, q := range []string{"COMMIT AND CHAIN", "ROLLBACK AND CHAIN", "ABORT AND CHAIN"} {
				f.Exec(t, f.B, "CREATE TABLE t(v INT NOT NULL)", false, 0)
				pid, holderPID := 0, 0
				var relationOID int64
				if postgres {
					pid = fixture.LockPID(t, ctx, f.B)
					holderPID = fixture.LockPID(t, ctx, f.A)
					relationOID = fixture.LockOID(t, ctx, "public.t")
				}
				f.Exec(t, f.A, "BEGIN", false, 0)
				f.Exec(t, f.A, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", false, 0)
				f.Exec(t, f.A, "INSERT INTO t VALUES(1)", false, 1)
				end, err := f.A.PrepareContext(ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				f.NoticesWant(t, "", "")
				if !postgres {
					blocked, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
					_, err = f.B.QueryContext(blocked, "SELECT v FROM t")
					cancel()
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Fatal("Describe released native hold", err)
					}
				}
				await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
					rows, err := f.B.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				if postgres {
					fixture.LockWaitBehind(t, ctx, pid, holderPID, relationOID, "AccessShareLock")
				}
				r, err := end.ExecContext(ctx)
				end.Close()
				if err != nil {
					t.Fatal(err)
				}
				fixture.LockAffected(t, r, 0)
				out := await()
				if out.Err != nil {
					t.Fatal(out.Err)
				}
				want := []int64{}
				if strings.HasPrefix(q, "COMMIT") {
					want = []int64{1}
				}
				fixture.LimitLockValues(t, out, postgres, want...)
				f.Exec(t, f.A, "INSERT INTO t VALUES(2)", false, 1)
				f.Rows(t, f.B, want...)
				f.Exec(t, f.A, "ROLLBACK", false, 0)
				f.Exec(t, f.A, "BEGIN", false, 0)
				f.Exec(t, f.A, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", false, 0)
				wantNull := fixture.TransactionNull()
				fixture.AssertDiagnostic(t, fixture.TransactionConstraintExec(t, ctx, f.A, "INSERT INTO t VALUES(NULL)", true), &wantNull)
				f.Rows(t, f.B, want...)
				f.Exec(t, f.A, "COMMIT AND CHAIN", true, 0)
				f.Exec(t, f.A, "ROLLBACK", false, 0)
				f.Exec(t, f.B, "DROP TABLE t", false, 0)
			}
		})
	}
}

// pq's canceled-context watcher can finish normally or retire the connection.
// Each exact outcome must discard private work before borrowing the next owner.
func referenceTransactionPreCanceledRetirement(t *testing.T, f *fixture.ControlsReferenceFixture, query, next string, prior ...int64) {
	t.Helper()
	ctx := f.Pair.Ctx
	oldPID := fixture.LockPID(t, ctx, f.A)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	r, err := f.A.ExecContext(canceled, query)
	if err == nil {
		if r == nil {
			t.Fatal("successful pre-canceled control returned no result")
		}
		fixture.LockAffected(t, r, 0)
		r, err = f.A.ExecContext(ctx, next)
		if err == nil {
			if r == nil {
				t.Fatal("successful subsequent control returned no result")
			}
			fixture.LockAffected(t, r, 0)
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
		if err := f.A.Close(); !errors.Is(err, sql.ErrConnDone) {
			t.Fatal("retired borrower Close", err)
		}
	} else {
		// Successful ROLLBACK already ended BEGIN; ROLLBACK TO leaves the
		// SAVEPOINT cohort active and needs its actual outer rollback.
		if next != "ROLLBACK" {
			f.Exec(t, f.A, "ROLLBACK", false, 0)
		}
		if err := f.A.Close(); err != nil {
			t.Fatal("healthy pre-canceled borrower Close", err)
		}
	}
	f.Rows(t, f.B, prior...)
	f.A, err = f.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner := f.A
	t.Cleanup(func() {
		owner.ExecContext(ctx, "ROLLBACK")
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	})
	if pid := fixture.LockPID(t, ctx, owner); pid <= 0 || (retired && pid == oldPID) {
		t.Fatal("invalid reopened owner PID", oldPID, pid, "retired", retired)
	}
	f.Rows(t, owner, prior...)
}

func TestReferenceTransactionControlsCancellation(t *testing.T) {
	for _, committedOne := range []bool{false, true} {
		t.Run(map[bool]string{false: "reference_77", true: "original_1_77"}[committedOne], func(t *testing.T) {
			pair := fixture.NewScenario(t)
			for _, backend := range pair.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					postgres := backend.PG
					f := fixture.NewControlsReferenceFixture(t, pair, postgres)
					ctx := pair.Ctx
					f.Exec(t, f.B, "CREATE TABLE t(v INT NOT NULL)", false, 0)
					f.Exec(t, f.B, "INSERT INTO t VALUES(77)", false, 1)
					if committedOne {
						if postgres {
							referenceTransactionPreCanceledRetirement(t, &f, "BEGIN", "ROLLBACK", 77)
						} else {
							canceled, cancel := context.WithCancel(ctx)
							cancel()
							r, err := f.A.ExecContext(canceled, "BEGIN")
							if r != nil || !errors.Is(err, context.Canceled) {
								t.Fatal(r, err)
							}
							var state interface{ SQLState() string }
							if errors.As(err, &state) {
								t.Fatal("pre-canceled BEGIN fabricated SQLSTATE", err)
							}
						}
						f.Exec(t, f.A, "INSERT INTO t VALUES(1)", false, 1)
						f.Rows(t, f.B, 1, 77)
					}
					f.Exec(t, f.A, "SET statement_timeout TO '20ms'", false, 0)
					f.Exec(t, f.B, "BEGIN", false, 0)
					f.Exec(t, f.B, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", false, 0)
					f.Exec(t, f.A, "BEGIN", false, 0)
					_, err := f.A.ExecContext(ctx, "INSERT INTO t VALUES(2)")
					want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to statement timeout")
					fixture.AssertDiagnostic(t, err, &want)
					_, err = f.A.ExecContext(ctx, "SELECT v FROM t")
					want = fixture.TransactionAborted()
					fixture.AssertDiagnostic(t, err, &want)
					f.Exec(t, f.A, "ROLLBACK AND CHAIN", !committedOne, 0)
					f.Exec(t, f.A, "ROLLBACK", false, 0)
					f.Exec(t, f.A, "SET statement_timeout TO 0", false, 0)
					if postgres {
						pid := 0
						if err = f.A.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
							t.Fatal(err)
						}
						f.Exec(t, f.A, "BEGIN", false, 0)
						await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
							r, err := f.A.ExecContext(ctx, "INSERT INTO t VALUES(3)")
							return fixture.RelationOutcome{Result: r, Err: err}
						})
						holderPID := 0
						if err := f.B.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&holderPID); err != nil {
							t.Fatal(err)
						}
						fixture.LockWaitBehind(t, ctx, pid, holderPID, fixture.LockOID(t, ctx, "t"), "RowExclusiveLock")
						var canceled bool
						if err = fixture.Admin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
							t.Fatal(canceled, err)
						}
						out := await()
						want = fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
						fixture.AssertDiagnostic(t, out.Err, &want)
						f.Exec(t, f.A, "COMMIT", false, 0)
					} else {
						canceled, cancel := context.WithCancel(ctx)
						cancel()
						_, err = f.A.ExecContext(canceled, "BEGIN")
						if !errors.Is(err, context.Canceled) {
							t.Fatal(err)
						}
						f.Exec(t, f.A, "BEGIN", false, 0)
						bounded, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
						_, err = f.A.ExecContext(bounded, "INSERT INTO t VALUES(3)")
						cancel()
						if !errors.Is(err, context.DeadlineExceeded) {
							t.Fatal(err)
						}
						var state interface{ SQLState() string }
						if errors.As(err, &state) {
							t.Fatal("ordinary native caller context fabricated SQLSTATE", err)
						}
						f.Exec(t, f.A, "COMMIT", false, 0)
					}
					f.Exec(t, f.B, "ROLLBACK", false, 0)
					if committedOne {
						f.Rows(t, f.B, 1, 77)
					} else {
						f.Rows(t, f.B, 77)
					}
					f.Exec(t, f.A, "INSERT INTO t VALUES(9)", true, 1)
					if committedOne {
						f.Rows(t, f.B, 1, 9, 77)
					} else {
						f.Rows(t, f.B, 9, 77)
					}
				})
			}
		})
	}
}
