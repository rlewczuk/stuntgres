package reference_test

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

func TestReferenceAlterDropColumnWaitCancellationAndCommittedInsert(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx, stop := context.WithTimeout(p.ctx, 10*time.Second)
			defer stop()
			db, record := backend.db, backend.recorder
			referenceRunStep(t, ctx, db, record, referenceExec("CREATE TABLE t(a INT,b TEXT)", 0))
			holder, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Close()
			worker, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer worker.Close()
			observer, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer observer.Close()
			holderPID, workerPID := 0, 0
			var oid int64
			if backend.pg {
				holderPID = referenceLockPID(t, ctx, holder)
				workerPID = referenceLockPID(t, ctx, worker)
				oid = referenceLockOID(t, ctx, "t")
			}
			tx, err := holder.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceRunStep(t, ctx, tx, record, referenceExec("INSERT INTO t VALUES(1,'committed')", 1))
			// ACCESS SHARE is compatible with the holder's ROW EXCLUSIVE baseline.
			if !backend.pg {
				probe, e := observer.BeginTx(ctx, nil)
				if e != nil {
					t.Fatal(e)
				}
				referenceRunStep(t, ctx, probe, record, referenceExec("LOCK TABLE t IN ACCESS SHARE MODE NOWAIT", 0))
				if e = probe.Rollback(); e != nil {
					t.Fatal(e)
				}
			}
			for _, cancelPhase := range []bool{true, false} {
				func() {
					waitCtx, cancel := context.WithCancel(ctx)
					type outcome struct {
						err   error
						count int64
					}
					done := make(chan outcome, 1)
					go func() {
						r, e := worker.ExecContext(waitCtx, "ALTER TABLE t DROP b")
						out := outcome{err: e}
						if e == nil {
							out.count, out.err = r.RowsAffected()
						}
						done <- out
					}()
					joined := false
					defer func() {
						cancel()
						if !joined {
							select {
							case <-done:
							case <-ctx.Done():
								t.Error("drop worker did not join", ctx.Err())
							}
						}
					}()
					if backend.pg {
						referenceLockWaitBehind(t, ctx, workerPID, holderPID, oid, "AccessExclusiveLock")
					} else {
						for {
							select {
							case out := <-done:
								joined = true
								t.Fatal("AX completed before holder ended", out.err)
							case <-ctx.Done():
								t.Fatal("native AX queue was not witnessed", ctx.Err())
							default:
							}
							probe, e := observer.BeginTx(ctx, nil)
							if e != nil {
								t.Fatal(e)
							}
							_, e = probe.ExecContext(ctx, "LOCK TABLE t IN ACCESS SHARE MODE NOWAIT")
							rollback := probe.Rollback()
							if rollback != nil {
								t.Fatal(rollback)
							}
							if e != nil {
								referenceAssertDiagnostic(t, e, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55P03", Message: `could not obtain lock on relation "t"`})
								break
							}
							runtime.Gosched()
						}
					}
					if cancelPhase {
						if backend.pg {
							var canceled bool
							if err = referenceAdmin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", workerPID).Scan(&canceled); err != nil || !canceled {
								t.Fatal("server cancellation", canceled, err)
							}
						} else {
							cancel()
						}
					} else if err = tx.Commit(); err != nil {
						t.Fatal(err)
					}
					select {
					case out := <-done:
						joined = true
						if cancelPhase {
							if backend.pg {
								referenceAssertDiagnostic(t, out.err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
							} else if !errors.Is(out.err, context.Canceled) {
								t.Fatal(out.err)
							}
						} else if out.err != nil || out.count != 0 {
							t.Fatal("committed drop outcome", out.count, out.err)
						}
					case <-ctx.Done():
						t.Fatal("queued drop did not finish", ctx.Err())
					}
				}()
			}
			referenceRunStep(t, ctx, db, record, referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
			referenceRunStep(t, ctx, db, record, referenceExec("INSERT INTO t VALUES(2)", 1))
			before := ""
			if !backend.pg {
				before = referenceDDLNativeDump(t, p)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			_, err = db.ExecContext(canceled, "ALTER TABLE t DROP a")
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if !backend.pg && referenceDDLNativeDump(t, p) != before {
				t.Fatal("pre-canceled drop changed canonical bytes")
			}
			referenceRunStep(t, ctx, db, record, referenceSelect("SELECT * FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		})
	}
}
