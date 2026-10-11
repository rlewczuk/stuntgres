package reference_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterAddColumnCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			alterAddReferenceScenario(t, func(p *fixture.Pair, backend fixture.Backend) {
				db, record := backend.DB, backend.Recorder
				fixture.RunStep(t, p.Ctx, db, record, fixture.Exec("CREATE TABLE t(id INT)", 0))
				holder, err := db.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Close()
				worker, err := db.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer worker.Close()
				observer, err := db.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer observer.Close()
				workerPID, holderPID := 0, 0
				var oid int64
				if backend.PG {
					workerPID, holderPID = fixture.LockPID(t, p.Ctx, worker), fixture.LockPID(t, p.Ctx, holder)
					oid = fixture.LockOID(t, p.Ctx, "t")
				}
				query := "ALTER TABLE t ADD b INT DEFAULT 7 NOT NULL"
				executor := fixture.Executor(worker)
				if prepared {
					stmt, err := worker.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					executor = fixture.HeldStatement{worker, stmt, query}
				}
				tx, err := holder.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.RunStep(t, p.Ctx, tx, record, fixture.Exec("INSERT INTO t VALUES(1)", 1))
				waitCtx, cancel := context.WithCancel(p.Ctx)
				done := make(chan error, 1)
				go func() { _, err := executor.ExecContext(waitCtx, query); done <- err }()
				joined := false
				defer func() {
					cancel()
					if !joined {
						select {
						case <-done:
						case <-p.Ctx.Done():
							t.Error("ADD worker did not join", p.Ctx.Err())
						}
					}
				}()
				if backend.PG {
					fixture.LockWaitBehind(t, p.Ctx, workerPID, holderPID, oid, "AccessExclusiveLock")
				} else {
					for {
						select {
						case err := <-done:
							joined = true
							t.Fatal("ADD completed before AX release", err)
						case <-p.Ctx.Done():
							t.Fatal(p.Ctx.Err())
						default:
						}
						probe, err := observer.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						_, err = probe.ExecContext(p.Ctx, "LOCK TABLE t IN ACCESS SHARE MODE NOWAIT")
						if rollback := probe.Rollback(); rollback != nil {
							t.Fatal(rollback)
						}
						if err != nil {
							fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55P03", Message: `could not obtain lock on relation "t"`})
							break
						}
						runtime.Gosched()
					}
				}
				if backend.PG {
					var canceled bool
					if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", workerPID).Scan(&canceled); err != nil || !canceled {
						t.Fatal(canceled, err)
					}
				} else {
					cancel()
				}
				select {
				case err := <-done:
					joined = true
					if backend.PG {
						fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
					} else if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				case <-p.Ctx.Done():
					t.Fatal("queued ADD did not finish", p.Ctx.Err())
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, db, record, fixture.Select("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				fixture.RunStep(t, p.Ctx, executor, record, fixture.Exec(query, 0))
				fixture.RunStep(t, p.Ctx, db, record, fixture.Select("SELECT * FROM t", []string{"id", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}}))
			})
		})
	}
}
