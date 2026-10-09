package reference_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"testing"
)

func TestReferenceAlterAddColumnCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			alterAddReferenceScenario(t, func(p *referencePair, backend referenceBackend) {
				db, record := backend.db, backend.recorder
				referenceRunStep(t, p.ctx, db, record, referenceExec("CREATE TABLE t(id INT)", 0))
				holder, err := db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Close()
				worker, err := db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer worker.Close()
				observer, err := db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer observer.Close()
				workerPID, holderPID := 0, 0
				var oid int64
				if backend.pg {
					workerPID, holderPID = referenceLockPID(t, p.ctx, worker), referenceLockPID(t, p.ctx, holder)
					oid = referenceLockOID(t, p.ctx, "t")
				}
				query := "ALTER TABLE t ADD b INT DEFAULT 7 NOT NULL"
				executor := referenceExecutor(worker)
				if prepared {
					stmt, err := worker.PrepareContext(p.ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					executor = referenceHeldStatement{worker, stmt, query}
				}
				tx, err := holder.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				referenceRunStep(t, p.ctx, tx, record, referenceExec("INSERT INTO t VALUES(1)", 1))
				waitCtx, cancel := context.WithCancel(p.ctx)
				done := make(chan error, 1)
				go func() { _, err := executor.ExecContext(waitCtx, query); done <- err }()
				joined := false
				defer func() {
					cancel()
					if !joined {
						select {
						case <-done:
						case <-p.ctx.Done():
							t.Error("ADD worker did not join", p.ctx.Err())
						}
					}
				}()
				if backend.pg {
					referenceLockWaitBehind(t, p.ctx, workerPID, holderPID, oid, "AccessExclusiveLock")
				} else {
					for {
						select {
						case err := <-done:
							joined = true
							t.Fatal("ADD completed before AX release", err)
						case <-p.ctx.Done():
							t.Fatal(p.ctx.Err())
						default:
						}
						probe, err := observer.BeginTx(p.ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						_, err = probe.ExecContext(p.ctx, "LOCK TABLE t IN ACCESS SHARE MODE NOWAIT")
						if rollback := probe.Rollback(); rollback != nil {
							t.Fatal(rollback)
						}
						if err != nil {
							referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55P03", Message: `could not obtain lock on relation "t"`})
							break
						}
						runtime.Gosched()
					}
				}
				if backend.pg {
					var canceled bool
					if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT pg_cancel_backend($1)", workerPID).Scan(&canceled); err != nil || !canceled {
						t.Fatal(canceled, err)
					}
				} else {
					cancel()
				}
				select {
				case err := <-done:
					joined = true
					if backend.pg {
						referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
					} else if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				case <-p.ctx.Done():
					t.Fatal("queued ADD did not finish", p.ctx.Err())
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				referenceRunStep(t, p.ctx, db, record, referenceSelect("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				referenceRunStep(t, p.ctx, executor, record, referenceExec(query, 0))
				referenceRunStep(t, p.ctx, db, record, referenceSelect("SELECT * FROM t", []string{"id", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}}))
			})
		})
	}
}
