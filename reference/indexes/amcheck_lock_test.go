package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func amcheckReferenceLockStrength(t *testing.T, p *fixture.Pair, backend fixture.Backend) {
	t.Helper()
	a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
	defer a.Close()
	defer b.Close()
	holder, waiter := 0, 0
	var relation int64
	if backend.PG {
		holder = fixture.LockPID(t, p.Ctx, a)
		waiter = fixture.LockPID(t, p.Ctx, b)
		relation = fixture.LockOID(t, p.Ctx, "am_heap")
	}
	tx, err := a.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	fixture.LockExec(t, p.Ctx, tx, "LOCK TABLE am_heap IN ROW EXCLUSIVE MODE", 0)
	fixture.RunStep(t, p.Ctx, b, backend.Recorder, amcheckSelect("SELECT bt_index_check('am_idx',true,true) AS ok", false))
	query := "SELECT bt_index_parent_check('am_idx',true,true,true)"
	finished := make(chan struct{})
	join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
		defer close(finished)
		result, err := b.ExecContext(ctx, query)
		return fixture.RelationOutcome{Result: result, Err: err}
	})
	fixture.RelationLockPending(t, p, backend, waiter, holder, relation, "ShareLock", finished)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	result := join()
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	fixture.LockAffected(t, result.Result, 1)
}

func TestReferenceAmcheckLocks(t *testing.T) {
	p := fixture.NewScenario(t)
	amcheckSetup(t, p)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			amcheckReferenceLockStrength(t, p, backend)
			a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
			defer a.Close()
			defer b.Close()
			// Each successful function contribution ends before the surrounding
			// explicit transaction. Another owner can take AX immediately.
			for _, query := range []string{"SELECT bt_index_check('am_idx',true,true) AS ok", "SELECT bt_index_parent_check('am_idx',true,true,true) AS ok"} {
				tx, err := b.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, tx, backend.Recorder, amcheckSelect(query, false))
				other, err := a.BeginTx(p.Ctx, nil)
				if err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
				fixture.LockExec(t, p.Ctx, other, "LOCK TABLE am_heap IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
				if err := other.Rollback(); err != nil {
					t.Fatal(err)
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
			// An earlier table SELECT contribution survives amcheck's return.
			tx, err := b.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Select("SELECT n FROM am_heap WHERE n=1", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
			fixture.RunStep(t, p.Ctx, tx, backend.Recorder, amcheckSelect("SELECT bt_index_check('am_idx') AS ok", false))
			other, err := a.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer other.Rollback()
			fixture.RunStep(t, p.Ctx, other, backend.Recorder, fixture.ImplicitError("LOCK TABLE am_heap IN ACCESS EXCLUSIVE MODE NOWAIT", fixture.Diagnostic{Code: "55P03", Message: `could not obtain lock on relation "am_heap"`}))
			if err := other.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			priorShare, err := b.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer priorShare.Rollback()
			fixture.LockExec(t, p.Ctx, priorShare, "LOCK TABLE am_heap IN SHARE MODE", 0)
			fixture.RunStep(t, p.Ctx, priorShare, backend.Recorder, amcheckSelect("SELECT bt_index_parent_check('am_idx',true,true,true) AS ok", false))
			writer, err := a.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Rollback()
			fixture.RunStep(t, p.Ctx, writer, backend.Recorder, fixture.ImplicitError("LOCK TABLE am_heap IN ROW EXCLUSIVE MODE NOWAIT", fixture.Diagnostic{Code: "55P03", Message: `could not obtain lock on relation "am_heap"`}))
			if err := writer.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err := priorShare.Rollback(); err != nil {
				t.Fatal(err)
			}
			// AccessExclusive blocks both documented families, independently
			// of the stronger Share-vs-writer witness above.
			for _, function := range []string{"bt_index_check", "bt_index_parent_check"} {
				holder, waiter := 0, 0
				var relation int64
				if backend.PG {
					holder = fixture.LockPID(t, p.Ctx, a)
					waiter = fixture.LockPID(t, p.Ctx, b)
					relation = fixture.LockOID(t, p.Ctx, "am_heap")
				}
				tx, err := a.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.LockExec(t, p.Ctx, tx, "LOCK TABLE am_heap IN ACCESS EXCLUSIVE MODE", 0)
				finished := make(chan struct{})
				join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					defer close(finished)
					result, err := b.ExecContext(ctx, "SELECT "+function+"('am_idx')")
					return fixture.RelationOutcome{Result: result, Err: err}
				})
				mode := "AccessShareLock"
				if function == "bt_index_parent_check" {
					mode = "ShareLock"
				}
				fixture.RelationLockPending(t, p, backend, waiter, holder, relation, mode, finished)
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				outcome := join()
				if outcome.Err != nil {
					t.Fatal(outcome.Err)
				}
				fixture.LockAffected(t, outcome.Result, 1)
			}
		})
	}
}

func TestReferenceAmcheckCancellation(t *testing.T) {
	p := fixture.NewScenario(t)
	amcheckSetup(t, p)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
			defer a.Close()
			defer b.Close()
			tx, err := a.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.LockExec(t, p.Ctx, tx, "LOCK TABLE am_heap IN ROW EXCLUSIVE MODE", 0)
			query := "SELECT bt_index_parent_check('am_idx',true,true,true)"
			statement, err := b.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer statement.Close()
			fixture.RelationLockActiveCancel(t, p, backend, b, query, func(ctx context.Context) fixture.RelationOutcome {
				result, err := statement.ExecContext(ctx)
				return fixture.RelationOutcome{Result: result, Err: err}
			})
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, a, backend.Recorder, amcheckSelect("SELECT bt_index_parent_check('am_idx',true,true,true) AS ok", false))
			holder, err := a.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback()
			fixture.LockExec(t, p.Ctx, holder, "LOCK TABLE am_heap IN ROW EXCLUSIVE MODE", 0)
			for _, tc := range []struct{ name, timer, code, message string }{
				{"lock_timeout", "10ms", "55P03", "canceling statement due to lock timeout"},
				{"statement_timeout", "100ms", "57014", "canceling statement due to statement timeout"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					// Each timeout owns a fresh physical borrower. Nothing is
					// prepared or reset after arming; disposal retires its timer.
					armed, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := armed.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
							t.Error("discard armed owner", err)
						}
						if err := armed.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
							t.Error("close armed owner", err)
						}
					}()
					prepared, err := armed.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := prepared.Close(); err != nil {
							t.Error("close timeout statement", err)
						}
					}()
					fixture.LockExec(t, p.Ctx, armed, "SET "+tc.name+"='"+tc.timer+"'", 0)
					held := fixture.HeldStatement{Executor: armed, Statement: prepared, SQL: query}
					failure := fixture.ImplicitError(query, fixture.Diagnostic{Code: tc.code, Message: tc.message})
					failure.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, held, backend.Recorder, failure)
				})
			}
			if err := holder.Rollback(); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, a, backend.Recorder, amcheckSelect("SELECT bt_index_parent_check('am_idx',true,true,true) AS ok", false))
		})
	}
}
