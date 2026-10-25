package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferencePartitionAttachLocks(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, cancelWorker := range []bool{false, true} {
			name := "direct/complete"
			if prepared {
				name = "prepared/complete"
			}
			if cancelWorker {
				name = name[:len(name)-8] + "cancel"
			}
			t.Run(name, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						ctx, cancel := context.WithTimeout(p.Ctx, 10*time.Second)
						defer cancel()
						run := func(s fixture.Step) { fixture.RunStep(t, ctx, b.DB, b.Recorder, s) }
						run(fixture.Exec("CREATE TABLE pa_lock_parent(k INT) PARTITION BY RANGE(k)", 0))
						run(fixture.Exec("CREATE TABLE pa_lock_sibling PARTITION OF pa_lock_parent FOR VALUES FROM(20) TO(30)", 0))
						run(fixture.Exec("CREATE TABLE pa_lock_candidate(k INT)", 0))
						run(fixture.Exec("INSERT INTO pa_lock_candidate VALUES(1)", 1))
						run(fixture.Exec("CREATE TABLE pa_lock_second(k INT)", 0))
						holder, err := b.DB.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer holder.Rollback()
						fixture.RunStep(t, ctx, holder, b.Recorder, referenceRangeRows("SELECT k FROM pa_lock_candidate", 1))
						worker, err := b.DB.Conn(ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer worker.Close()
						probe, err := b.DB.Conn(ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer probe.Close()
						fixture.RunStep(t, ctx, probe, b.Recorder, fixture.Exec("SET lock_timeout='50ms'", 0))
						q := "ALTER TABLE pa_lock_parent ATTACH PARTITION pa_lock_candidate FOR VALUES FROM(1) TO(3)"
						var stmt *sql.Stmt
						if prepared {
							stmt, err = worker.PrepareContext(ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
						}
						pid := 0
						if b.PG {
							pid = fixture.LockPID(t, ctx, worker)
						}
						workerCtx, stop := context.WithCancel(ctx)
						defer stop()
						type outcome struct {
							result sql.Result
							err    error
						}
						done := make(chan outcome, 1)
						joined := false
						defer func() {
							stop()
							holder.Rollback()
							if !joined {
								select {
								case <-done:
								case <-time.After(5 * time.Second):
									t.Error("ATTACH worker did not join")
								}
							}
						}()
						go func() {
							var r sql.Result
							var e error
							if stmt != nil {
								r, e = stmt.ExecContext(workerCtx)
							} else {
								r, e = worker.ExecContext(workerCtx, q)
							}
							done <- outcome{r, e}
						}()
						if b.PG {
							var holderPID int
							if err := holder.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&holderPID); err != nil {
								t.Fatal(err)
							}
							fixture.LockWaitBehind(t, ctx, pid, holderPID, fixture.LockOID(t, ctx, "pa_lock_candidate"), "AccessExclusiveLock")
						} else {
							// A compatible AccessShare probe cannot time out behind the
							// original reader; only a genuinely queued candidate AX can
							// exclude it through the real native manager's fairness rule.
							probeCtx, stopProbe := context.WithTimeout(ctx, 3*time.Second)
							defer stopProbe()
							for {
								rows, e := probe.QueryContext(probeCtx, "SELECT k FROM pa_lock_candidate")
								if rows != nil {
									for rows.Next() {
									}
									if err := rows.Close(); err != nil {
										t.Fatal(err)
									}
								}
								if e != nil {
									want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55P03", Message: "canceling statement due to lock timeout", Position: referenceRangePosition("SELECT k FROM pa_lock_candidate", "pa_lock_candidate")}
									fixture.AssertDiagnostic(t, e, &want)
									break
								}
								if probeCtx.Err() != nil {
									t.Fatal("ATTACH never excluded fairness probe", probeCtx.Err())
								}
							}
						}
						// Parent SUE is compatible with a root-only reader and a
						// writer on an unrelated existing explicit physical branch.
						fixture.RunStep(t, ctx, probe, b.Recorder, fixture.Select("SELECT k FROM ONLY pa_lock_parent", []string{"k"}, []string{"INT4"}, nil))
						fixture.RunStep(t, ctx, probe, b.Recorder, fixture.Exec("INSERT INTO pa_lock_sibling VALUES(21)", 1))
						fixture.RunStep(t, ctx, probe, b.Recorder, fixture.ImplicitError("ALTER TABLE pa_lock_parent ATTACH PARTITION pa_lock_second FOR VALUES FROM(3) TO(5)", fixture.Diagnostic{Code: "55P03", Message: "canceling statement due to lock timeout"}))
						if cancelWorker {
							if b.PG {
								var canceled bool
								if err := fixture.Admin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
									t.Fatal("server cancellation", canceled, err)
								}
							} else {
								stop()
							}
						} else {
							if err := holder.Rollback(); err != nil {
								t.Fatal(err)
							}
						}
						select {
						case got := <-done:
							joined = true
							if cancelWorker {
								if got.result != nil {
									t.Fatal("canceled ATTACH published result")
								}
								if b.PG {
									fixture.AssertDiagnostic(t, got.err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
								} else if got.err != context.Canceled {
									t.Fatalf("native cancellation %v", got.err)
								}
							} else {
								if got.err != nil {
									t.Fatal(got.err)
								}
								if got.result == nil {
									t.Fatal("successful attach lost result")
								}
								n, e := got.result.RowsAffected()
								if e != nil || n != 0 {
									t.Fatalf("attach affected %d: %v", n, e)
								}
							}
						case <-ctx.Done():
							t.Fatal(ctx.Err())
						}
						if cancelWorker {
							if err := holder.Rollback(); err != nil {
								t.Fatal(err)
							}
							fixture.RunStep(t, ctx, worker, b.Recorder, fixture.Select("SELECT relispartition FROM pg_class WHERE relname='pa_lock_candidate'", []string{"relispartition"}, []string{"BOOL"}, [][]any{{false}}))
							fixture.RunStep(t, ctx, worker, b.Recorder, fixture.Exec(q, 0))
						}
						fixture.RunStep(t, ctx, worker, b.Recorder, referenceRangeRows("SELECT k FROM pa_lock_parent ORDER BY k", 1, 21))
						run(fixture.Exec("ALTER TABLE pa_lock_parent ATTACH PARTITION pa_lock_second FOR VALUES FROM(3) TO(5)", 0))
					})
				}
			})
		}
	}
	partitionAttachReaderGrowth(t)
}
