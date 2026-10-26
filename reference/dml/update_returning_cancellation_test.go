package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceUpdateReturningCancellation(t *testing.T) {
	for _, kind := range []string{"admin", "lock_timeout", "statement_timeout"} {
		p := fixture.NewScenario(t)
		for _, backend := range p.Backends() {
			t.Run(backend.Name+"/"+kind, func(t *testing.T) {
				a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
				fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT)", 0)
				fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1),(2)", 2)
				var physical string
				var ap, bp int
				if backend.PG {
					var tuple string
					if err := b.QueryRowContext(p.Ctx, "SELECT ctid::text FROM t WHERE id=2").Scan(&tuple); err != nil {
						t.Fatal(err)
					}
					physical = "while updating tuple " + tuple + ` in relation "t"`
					ap, bp = fixture.LockPID(t, p.Ctx, a), fixture.LockPID(t, p.Ctx, b)
				}
				holder, err := a.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Rollback()
				fixture.LockExec(t, p.Ctx, holder, "DELETE FROM t WHERE id=2", 1)
				if !backend.PG {
					ctx, cancel := context.WithTimeout(p.Ctx, 25*time.Millisecond)
					defer cancel()
					rows, err := b.QueryContext(ctx, "UPDATE t SET id=id+1 RETURNING old.id,new.id")
					if rows != nil {
						rows.Close()
						t.Fatal("canceled eager output exposed rows")
					}
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Fatal(err)
					}
					if err = holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					fixture.DeleteReferenceRows(t, p, b, backend.Recorder, 1, 2)
					return
				}
				// A lock timer counts only the lock wait; a statement timer also
				// counts parse and plan time.
				if kind != "admin" {
					timer := map[string]string{"lock_timeout": "10ms", "statement_timeout": "100ms"}[kind]
					fixture.LockExec(t, p.Ctx, b, "SET "+kind+"='"+timer+"'", 0)
				}
				await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					rows, err := b.QueryContext(ctx, "UPDATE t SET id=id+1 RETURNING old.id,new.id")
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				// A timer's exact outcome proves the wait; never race an observer
				// against the short timer. The admin cancel observes the parked waiter.
				if kind == "admin" {
					updateFromPGWait(t, p.Ctx, bp)
					var exactBlocker bool
					if err = fixture.Admin.QueryRowContext(p.Ctx, "SELECT $2=ANY(pg_blocking_pids($1))", bp, ap).Scan(&exactBlocker); err != nil || !exactBlocker {
						t.Fatal("unexpected tuple blocker", exactBlocker, err)
					}
					var earlierDeletion bool
					if err = fixture.Admin.QueryRowContext(p.Ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='transactionid' AND granted AND transactionid=(SELECT xmax FROM t WHERE id=1))", bp).Scan(&earlierDeletion); err != nil || !earlierDeletion {
						t.Fatal("wait lacked independently observed earlier tuple deletion", earlierDeletion, err)
					}
					var canceled bool
					if err = fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&canceled); err != nil || !canceled {
						t.Fatal(canceled, err)
					}
				}
				o := await()
				if o.Rows != nil {
					o.Rows.Close()
					t.Fatal("failed RETURNING exposed cursor")
				}
				want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request", Context: physical}
				if kind == "lock_timeout" {
					want.Code, want.Message = "55P03", "canceling statement due to lock timeout"
				}
				if kind == "statement_timeout" {
					want.Message = "canceling statement due to statement timeout"
				}
				fixture.AssertDiagnostic(t, o.Err, &want)
				if err = holder.Rollback(); err != nil {
					t.Fatal(err)
				}
				// Resetting a lock timer takes no lock. Once a statement timer is
				// armed even SET can time out, so that owner is disposed instead.
				survivor := b
				if kind == "statement_timeout" {
					if err = b.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
						t.Fatal("disposing timer-armed owner", err)
					}
					survivor = a
				} else {
					fixture.LockExec(t, p.Ctx, b, "SET lock_timeout=0", 0)
				}
				fixture.DeleteReferenceRows(t, p, survivor, backend.Recorder, 1, 2)
			})
		}
	}
}
