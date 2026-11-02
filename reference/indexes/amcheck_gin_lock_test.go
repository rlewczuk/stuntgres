package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func ginAmcheckSetup(t *testing.T, p *fixture.Pair) {
	t.Helper()
	for _, b := range p.Backends() {
		admin, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if _, err := admin.ExecContext(ctx, "DROP EXTENSION IF EXISTS amcheck CASCADE"); err != nil {
				t.Error(err)
			}
			if err := admin.Close(); err != nil {
				t.Error(err)
			}
		})
		fixture.LockExec(t, p.Ctx, admin, "DROP EXTENSION IF EXISTS amcheck CASCADE", 0)
	}
	for _, q := range []string{"CREATE EXTENSION amcheck", "CREATE TABLE am_heap(n integer,a integer[],v tsvector)", "CREATE INDEX am_idx ON am_heap USING gin(a,v)", "CREATE INDEX am_bt ON am_heap(n)"} {
		p.Step(t, fixture.Exec(q, 0))
	}
	p.Step(t, fixture.Exec("INSERT INTO am_heap VALUES(1,'{1,1,2}','cat dog'),(2,'{}',''),(3,NULL,NULL)", 3))
}
func ginAmcheckLockRound(t *testing.T, p *fixture.Pair, b fixture.Backend, commit bool) {
	t.Helper()
	a, c := fixture.LockConnections(t, p.Ctx, b.DB)
	defer a.Close()
	defer c.Close()
	const q = "SELECT gin_index_check('am_idx') AS ok"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	tx, err := a.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	fixture.LockExec(t, p.Ctx, tx, "LOCK TABLE am_heap IN ROW EXCLUSIVE MODE", 0)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, amcheckSelect(q, false))
	fixture.LockExec(t, p.Ctx, tx, "LOCK TABLE am_heap IN ACCESS EXCLUSIVE MODE", 0)
	holder, waiter := 0, 0
	var relation int64
	if b.PG {
		holder = fixture.LockPID(t, p.Ctx, a)
		waiter = fixture.LockPID(t, p.Ctx, c)
		relation = fixture.LockOID(t, p.Ctx, "am_heap")
	}
	finished := make(chan struct{})
	join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
		defer close(finished)
		r, err := stmt.ExecContext(ctx)
		return fixture.RelationOutcome{Result: r, Err: err}
	})
	fixture.RelationLockPending(t, p, b, waiter, holder, relation, "AccessShareLock", finished)
	if commit {
		err = tx.Commit()
	} else {
		err = tx.Rollback()
	}
	if err != nil {
		t.Fatal(err)
	}
	result := join()
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	fixture.LockAffected(t, result.Result, 1)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, amcheckSelect(q, false))
}
func TestReferenceAmcheckGINLocks(t *testing.T) {
	p := fixture.NewScenario(t)
	ginAmcheckSetup(t, p)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			ginAmcheckLockRound(t, p, b, true)
			ginAmcheckLockRound(t, p, b, false)
			a, c := fixture.LockConnections(t, p.Ctx, b.DB)
			defer a.Close()
			defer c.Close()
			for _, prior := range []bool{false, true} {
				tx, err := c.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if prior {
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT n FROM am_heap WHERE n=1", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
				}
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, amcheckSelect("SELECT gin_index_check('am_idx') AS ok", false))
				other, err := a.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer other.Rollback()
				if prior {
					fixture.RunStep(t, p.Ctx, other, b.Recorder, heapError("LOCK TABLE am_heap IN ACCESS EXCLUSIVE MODE NOWAIT", heapDiagnostic("55P03", `could not obtain lock on relation "am_heap"`)))
				} else {
					fixture.LockExec(t, p.Ctx, other, "LOCK TABLE am_heap IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
				}
				if err := other.Rollback(); err != nil {
					t.Fatal(err)
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
			// Numeric REGCLASS keeps binder lookup outside the lock-before-error proof.
			for _, target := range []string{"am_bt", "am_heap"} {
				var oid int64
				if err := c.QueryRowContext(p.Ctx, "SELECT '"+target+"'::regclass::oid::bigint").Scan(&oid); err != nil {
					t.Fatal(err)
				}
				q := fmt.Sprintf("SELECT gin_index_check(%d::regclass)", oid)
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				tx, err := a.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.LockExec(t, p.Ctx, tx, "LOCK TABLE am_heap IN ACCESS EXCLUSIVE MODE", 0)
				holder, waiter := 0, 0
				var relation int64
				if b.PG {
					holder = fixture.LockPID(t, p.Ctx, a)
					waiter = fixture.LockPID(t, p.Ctx, c)
					relation = fixture.LockOID(t, p.Ctx, "am_heap")
				}
				finished := make(chan struct{})
				join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					defer close(finished)
					r, err := stmt.ExecContext(ctx)
					return fixture.RelationOutcome{Result: r, Err: err}
				})
				fixture.RelationLockPending(t, p, b, waiter, holder, relation, "AccessShareLock", finished)
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				want := heapDiagnostic("42809", `"am_heap" is not an index`)
				if target == "am_bt" {
					want = heapDiagnostic("0A000", `expected "gin" index as targets for verification`)
					want.Detail = `Relation "am_bt" is a btree index.`
				}
				result := join()
				fixture.AssertDiagnostic(t, result.Err, &want)
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestReferenceAmcheckGINCancellation(t *testing.T) {
	p := fixture.NewScenario(t)
	ginAmcheckSetup(t, p)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, c := fixture.LockConnections(t, p.Ctx, b.DB)
			defer a.Close()
			defer c.Close()
			const q = "SELECT gin_index_check('am_idx')"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			tx, err := a.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.LockExec(t, p.Ctx, tx, "LOCK TABLE am_heap IN ACCESS EXCLUSIVE MODE", 0)
			fixture.RelationLockActiveCancel(t, p, b, c, q, func(ctx context.Context) fixture.RelationOutcome {
				r, err := stmt.ExecContext(ctx)
				return fixture.RelationOutcome{Result: r, Err: err}
			})
			for _, tc := range []struct{ name, timer, code, message string }{{"lock_timeout", "10ms", "55P03", "canceling statement due to lock timeout"}, {"statement_timeout", "100ms", "57014", "canceling statement due to statement timeout"}} {
				t.Run(tc.name, func(t *testing.T) {
					armed, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := armed.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
							t.Error(err)
						}
						if err := armed.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
							t.Error(err)
						}
					}()
					prepared, err := armed.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer prepared.Close()
					fixture.LockExec(t, p.Ctx, armed, "SET "+tc.name+"='"+tc.timer+"'", 0)
					failure := heapError(q, heapDiagnostic(tc.code, tc.message))
					failure.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: armed, Statement: prepared, SQL: q}, b.Recorder, failure)
				})
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			fresh, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			fixture.RunStep(t, p.Ctx, fresh, b.Recorder, amcheckSelect("SELECT gin_index_check('am_idx') AS ok", false))
			clean, err := fresh.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer clean.Rollback()
			fixture.LockExec(t, p.Ctx, clean, "LOCK TABLE am_heap IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
			if err := clean.Rollback(); err != nil {
				t.Fatal(err)
			}
			// Index DDL independently requires the index lock, not just the heap lock.
			fixture.RunStep(t, p.Ctx, fresh, b.Recorder, fixture.Exec("DROP INDEX am_idx", 0))
		})
	}
}
