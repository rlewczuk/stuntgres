package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

type partitionDetachOutcome struct {
	result sql.Result
	err    error
}

func partitionDetachWait(t *testing.T, ctx context.Context, p *fixture.Pair, b fixture.Backend, holder *sql.Tx, pid int, probe *sql.Conn, relation string) {
	t.Helper()
	if b.PG {
		var holderPID int
		if err := holder.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&holderPID); err != nil {
			t.Fatal(err)
		}
		fixture.LockWaitBehind(t, ctx, pid, holderPID, fixture.LockOID(t, ctx, relation), "AccessExclusiveLock")
		return
	}
	fixture.RunStep(t, ctx, probe, b.Recorder, fixture.Exec("SET lock_timeout='50ms'", 0))
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	q := "SELECT k FROM ONLY " + relation
	for {
		rows, err := probe.QueryContext(probeCtx, q)
		if rows != nil {
			for rows.Next() {
			}
			if e := rows.Close(); e != nil {
				t.Fatal(e)
			}
		}
		if err != nil {
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55P03", Message: "canceling statement due to lock timeout", Position: referenceRangePosition(q, relation)})
			return
		}
		if probeCtx.Err() != nil {
			t.Fatal("DETACH never excluded compatible AS fairness probe", probeCtx.Err())
		}
	}
}

func TestReferencePartitionDetachLocks(t *testing.T) {
	for _, mode := range []string{"complete_commit", "complete_rollback", "cancel", "cancel_transaction", "timeout", "reset", "restore"} {
		for _, prepared := range []bool{false, true} {
			name := mode + "/direct"
			if prepared {
				name = mode + "/held"
			}
			t.Run(name, func(t *testing.T) {
				partitionDetachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					ctx, cancel := context.WithTimeout(p.Ctx, 12*time.Second)
					defer cancel()
					run := func(s fixture.Step) { fixture.RunStep(t, ctx, b.DB, b.Recorder, s) }
					partitionDetachSetup(run, "pd_parent")
					if b.PG && (mode == "reset" || mode == "restore") {
						return
					} // No server API counterpart.
					graphQ := "SELECT classid::BIGINT,objid::BIGINT,objsubid,refclassid::BIGINT,refobjid::BIGINT,refobjsubid,deptype FROM pg_depend ORDER BY classid,objid,objsubid,refclassid,refobjid,refobjsubid,deptype"
					beforeGraph := partitionDetachCapture(t, p, b.DB, graphQ)
					var stream bytes.Buffer
					if mode == "restore" {
						if err := stuntgres.Dump(ctx, p.Native, &stream); err != nil {
							t.Fatal(err)
						}
					}
					var retained *sql.Rows
					if mode == "reset" || mode == "restore" {
						var err error
						retained, err = b.DB.QueryContext(ctx, "SELECT k FROM pd_parent ORDER BY k")
						if err != nil {
							t.Fatal(err)
						}
						defer retained.Close()
						referenceRangeMetadata(false, []string{"k"}, []string{"INT4"}, []bool{false})(t, retained)
					}
					holder, err := b.DB.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Rollback()
					fixture.RunStep(t, ctx, holder, b.Recorder, referenceRangeRows("SELECT k FROM pd_parent ORDER BY k", 1, 2, 3))
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
					q := "ALTER TABLE pd_parent DETACH PARTITION pd_child"
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
					if mode == "timeout" {
						fixture.RunStep(t, ctx, worker, b.Recorder, fixture.Exec("SET lock_timeout='1s'", 0))
					}
					if mode == "cancel_transaction" {
						fixture.RunStep(t, ctx, worker, b.Recorder, fixture.Exec("BEGIN", 0))
						defer worker.ExecContext(ctx, "ROLLBACK")
					}
					workerCtx, stop := context.WithCancel(ctx)
					defer stop()
					done := make(chan partitionDetachOutcome, 1)
					joined := false
					defer func() {
						stop()
						holder.Rollback()
						if !joined {
							select {
							case <-done:
							case <-time.After(5 * time.Second):
								t.Error("DETACH worker failed to join")
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
						done <- partitionDetachOutcome{r, e}
					}()
					partitionDetachWait(t, ctx, p, b, holder, pid, probe, "pd_parent")
					switch mode {
					case "complete_commit":
						if err := holder.Commit(); err != nil {
							t.Fatal(err)
						}
					case "complete_rollback":
						if err := holder.Rollback(); err != nil {
							t.Fatal(err)
						}
					case "reset":
						if err := stuntgres.Reset(ctx, p.Native); err != nil {
							t.Fatal(err)
						}
						if _, err := b.DB.ExecContext(ctx, "CREATE TABLE pd_parent(k INT); CREATE TABLE pd_child(k INT); INSERT INTO pd_child VALUES(99)"); err != nil {
							t.Fatal(err)
						}
					case "restore":
						if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(stream.Bytes())); err != nil {
							t.Fatal(err)
						}
					case "timeout": // Keep the holder until the admitted request times out.
					default:
						if b.PG {
							var canceled bool
							if err := fixture.Admin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
								t.Fatal("server cancellation", canceled, err)
							}
						} else {
							stop()
						}
					}
					select {
					case got := <-done:
						joined = true
						if mode == "complete_commit" || mode == "complete_rollback" {
							if got.err != nil || got.result == nil {
								t.Fatal("completion", got.err)
							}
							n, err := got.result.RowsAffected()
							if err != nil || n != 0 {
								t.Fatalf("affected %d: %v", n, err)
							}
						} else {
							if got.result != nil {
								t.Fatal("failed DETACH published result")
							}
							if mode == "timeout" {
								want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55P03", Message: "canceling statement due to lock timeout"}
								fixture.AssertDiagnostic(t, got.err, &want)
							} else if mode == "reset" || mode == "restore" {
								fixture.TransactionOrdinary(t, got.err, "stuntgres: catalog replaced before publication")
							} else if b.PG {
								fixture.AssertDiagnostic(t, got.err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
							} else if got.err != context.Canceled {
								t.Fatalf("native cancellation identity: %v", got.err)
							}
						}
					case <-ctx.Done():
						t.Fatal("worker did not finish", ctx.Err())
					}
					if mode == "reset" || mode == "restore" {
						var values []int64
						for retained.Next() {
							var k int64
							if err := retained.Scan(&k); err != nil {
								t.Fatal(err)
							}
							values = append(values, k)
						}
						if retained.Err() != nil || !reflect.DeepEqual(values, []int64{1, 2, 3}) {
							t.Fatal("replacement changed retained rows", values, retained.Err())
						}
						if mode == "reset" {
							run(referenceRangeRows("SELECT k FROM pd_child", 99))
							run(fixture.Select("SELECT k FROM pd_parent", []string{"k"}, []string{"INT4"}, nil))
							return
						}
					}
					if mode == "cancel_transaction" {
						fixture.RunStep(t, ctx, worker, b.Recorder, fixture.ImplicitError("SELECT k FROM pd_child", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
						fixture.RunStep(t, ctx, worker, b.Recorder, fixture.Exec("ROLLBACK", 0))
					}
					if mode != "complete_commit" && mode != "complete_rollback" {
						holder.Rollback()
						if mode != "restore" {
							if got := partitionDetachCapture(t, p, worker, graphQ); !reflect.DeepEqual(got, beforeGraph) {
								t.Fatalf("cancellation/timeout changed graph: %#v", got)
							}
						}
						fixture.RunStep(t, ctx, worker, b.Recorder, referenceRangeRows("SELECT k FROM pd_parent ORDER BY k", 1, 2, 3))
						fixture.RunStep(t, ctx, worker, b.Recorder, partitionConstraintEdge("pd_child_key", "pd_key", "pd_child"))
						fixture.RunStep(t, ctx, worker, b.Recorder, partitionDetachClass("pd_child", true))
						fixture.RunStep(t, ctx, worker, b.Recorder, fixture.Exec(q, 0))
					}
					fixture.RunStep(t, ctx, worker, b.Recorder, referenceRangeRows("SELECT k FROM pd_parent", 3))
					fixture.RunStep(t, ctx, worker, b.Recorder, referenceRangeRows("SELECT k FROM pd_child ORDER BY k", 1, 2))
					fixture.RunStep(t, ctx, worker, b.Recorder, partitionDetachTopEdges("pd_child_key", false))
				})
			})
		}
	}
	partitionDetachLateLocks(t)
}

// PostgreSQL-only lock introspection is an observer phase, not an alternate
// native scenario. Native late-stage requests are covered by private units once
// production exists; SQL child reads first acquire ancestor AS there.
func partitionDetachLateLocks(t *testing.T) {
	for _, target := range []string{"pd_default", "pd_child", "pd_descendant"} {
		t.Run("late/"+target, func(t *testing.T) {
			partitionDetachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				if !b.PG {
					return
				}
				ctx, cancel := context.WithTimeout(p.Ctx, 10*time.Second)
				defer cancel()
				run := func(s fixture.Step) { fixture.RunStep(t, ctx, b.DB, b.Recorder, s) }
				partitionDetachSetup(run, "pd_parent")
				candidate := "pd_child"
				if target == "pd_descendant" {
					run(fixture.Exec("CREATE TABLE pd_branch PARTITION OF pd_parent FOR VALUES FROM(5) TO(7) PARTITION BY RANGE(k)", 0))
					run(fixture.Exec("CREATE TABLE pd_descendant PARTITION OF pd_branch DEFAULT", 0))
					candidate = "pd_branch"
				}
				holder, err := b.DB.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Rollback()
				fixture.RunStep(t, ctx, holder, b.Recorder, fixture.Exec("LOCK TABLE ONLY "+target+" IN ACCESS SHARE MODE", 0))
				worker, err := b.DB.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer worker.Close()
				pid := fixture.LockPID(t, ctx, worker)
				done := make(chan partitionDetachOutcome, 1)
				joined := false
				defer func() {
					cancel()
					holder.Rollback()
					if !joined {
						select {
						case <-done:
						case <-time.After(5 * time.Second):
							t.Error("late DETACH did not join")
						}
					}
				}()
				go func() {
					r, e := worker.ExecContext(ctx, "ALTER TABLE pd_parent DETACH PARTITION "+candidate)
					done <- partitionDetachOutcome{r, e}
				}()
				partitionDetachWait(t, ctx, p, b, holder, pid, nil, target)
				var parentAX bool
				if err := fixture.Admin.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND relation=$2 AND mode='AccessExclusiveLock' AND granted)", pid, fixture.LockOID(t, ctx, "pd_parent")).Scan(&parentAX); err != nil || !parentAX {
					t.Fatal("late waiter must retain parent AX", parentAX, err)
				}
				if target == "pd_default" {
					var openedChild bool
					if err := fixture.Admin.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND relation=$2 AND mode='AccessExclusiveLock')", pid, fixture.LockOID(t, ctx, "pd_child")).Scan(&openedChild); err != nil || openedChild {
						t.Fatal("DEFAULT must be admitted before candidate AX", openedChild, err)
					}
				} else {
					var defaultAX bool
					if err := fixture.Admin.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND relation=$2 AND mode='AccessExclusiveLock' AND granted)", pid, fixture.LockOID(t, ctx, "pd_default")).Scan(&defaultAX); err != nil || !defaultAX {
						t.Fatal("candidate/descendant wait must retain default AX", defaultAX, err)
					}
				}
				probe, err := b.DB.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer probe.Close()
				fixture.RunStep(t, ctx, probe, b.Recorder, fixture.Exec("SET lock_timeout='50ms'", 0))
				fixture.RunStep(t, ctx, probe, b.Recorder, fixture.ImplicitError("SELECT k FROM ONLY pd_parent", fixture.Diagnostic{Code: "55P03", Message: "canceling statement due to lock timeout", Position: "20"}))
				fixture.RunStep(t, ctx, probe, b.Recorder, fixture.ImplicitError("INSERT INTO pd_parent VALUES(4,'blocked')", fixture.Diagnostic{Code: "55P03", Message: "canceling statement due to lock timeout", Position: "13"}))
				if err := holder.Rollback(); err != nil {
					t.Fatal(err)
				}
				select {
				case got := <-done:
					joined = true
					if got.err != nil || got.result == nil {
						t.Fatal(got.err)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				run(partitionDetachClass(candidate, false))
			})
		})
	}
}
