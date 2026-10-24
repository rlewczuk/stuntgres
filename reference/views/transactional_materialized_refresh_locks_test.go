package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalMaterializedRefreshLocks(t *testing.T) {
	for _, end := range []string{"commit", "rollback", "rollback_to"} {
		t.Run(end, func(t *testing.T) {
			txRefreshRoutes(t, []string{"sql", "go"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held, concurrent bool) {
				base := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				other := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, peer, b.Recorder, s) }
				txRefreshSeed(base)
				base(fixture.Exec("CREATE TABLE txr_unrelated(i INT)", 0))
				q := txRefreshSQL(concurrent)
				route := txRefreshHeld(t, p, c, held, q)
				pid, hpid := 0, 0
				var oid int64
				if b.PG {
					pid = fixture.LockPID(t, p.Ctx, peer)
					hpid = fixture.LockPID(t, p.Ctx, c)
					oid = fixture.LockOID(t, p.Ctx, "txr_mv")
				}
				e, finish := transactionalViewsBegin(t, p, b, c, mode)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
				run(txRefreshRows("txr_mv", 1)) // Premark AS must survive rollback of the upgrade.
				run(fixture.Exec("SAVEPOINT locks", 0))
				run(fixture.Exec("INSERT INTO txr_source VALUES(2)", 1))
				fixture.RunStep(t, p.Ctx, route(e), b.Recorder, fixture.Exec(q, 0))
				run(txRefreshRows("txr_mv", 1, 2))
				other(fixture.Exec("INSERT INTO txr_unrelated VALUES(9)", 1))
				if concurrent {
					other(txRefreshRows("txr_mv", 1))
				}
				finished := make(chan struct{})
				waitSQL := "SELECT i FROM txr_mv ORDER BY i"
				lockMode := "AccessShareLock"
				if concurrent {
					waitSQL = q
					lockMode = "ExclusiveLock"
				}
				join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					defer close(finished)
					if concurrent {
						r, err := peer.ExecContext(ctx, waitSQL)
						return fixture.RelationOutcome{Result: r, Err: err}
					}
					rows, err := peer.QueryContext(ctx, waitSQL)
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				fixture.RelationLockPending(t, p, b, pid, hpid, oid, lockMode, finished)
				switch end {
				case "commit":
					run(fixture.Exec("RELEASE locks", 0))
					fixture.RelationLockPending(t, p, b, pid, hpid, oid, lockMode, finished)
					finish(true)
				case "rollback":
					finish(false)
				case "rollback_to":
					run(fixture.Exec("ROLLBACK TO locks", 0))
				}
				out := join()
				if out.Err != nil {
					t.Fatal(out.Err)
				}
				if concurrent {
					n, err := out.Result.RowsAffected()
					if n != 0 || err != nil {
						t.Fatal(n, err)
					}
				} else {
					defer out.Rows.Close()
					var got []int64
					for out.Rows.Next() {
						var i int64
						if err := out.Rows.Scan(&i); err != nil {
							t.Fatal(err)
						}
						got = append(got, i)
					}
					want := []int64{1}
					if end == "commit" {
						want = append(want, 2)
					}
					if len(got) != len(want) {
						t.Fatal(got, want)
					}
					for i := range want {
						if got[i] != want[i] {
							t.Fatal(got, want)
						}
					}
					if err := out.Rows.Err(); err != nil {
						t.Fatal(err)
					}
					out.Rows.Close()
				}
				if end == "rollback_to" {
					// AS acquired before the mark still blocks a genuine ordinary refresh.
					ptx, err := peer.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer ptx.Rollback()
					done := make(chan struct{})
					lock := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
						defer close(done)
						r, err := ptx.ExecContext(ctx, "REFRESH MATERIALIZED VIEW txr_mv")
						return fixture.RelationOutcome{Result: r, Err: err}
					})
					fixture.RelationLockPending(t, p, b, pid, hpid, oid, "AccessExclusiveLock", done)
					finish(false)
					if o := lock(); o.Err != nil {
						t.Fatal(o.Err)
					}
					if err := ptx.Rollback(); err != nil {
						t.Fatal(err)
					}
				}
				want := []int64{1}
				if end == "commit" {
					want = append(want, 2)
				}
				// A queued refresh keeps its prewait source snapshot: after a
				// concurrent holder commits source2, the follower still produces1.
				mvWant := want
				if concurrent && end == "commit" {
					mvWant = []int64{1}
				}
				base(txRefreshRows("txr_mv", mvWant...))
				base(txRefreshRows("txr_source", want...))
				base(fixture.Exec(q, 0))
				base(txRefreshRows("txr_mv", want...))
				base(fixture.Select("SELECT i FROM txr_unrelated", []string{"i"}, []string{"INT4"}, [][]any{{int64(9)}}))
			})
		})
	}
	t.Run("wait_failures", txRefreshWaitFailures)
}

func txRefreshWaitFailures(t *testing.T) {
	for _, source := range []bool{false, true} {
		for _, timeout := range []bool{false, true} {
			t.Run(map[bool]string{false: "target", true: "source"}[source]+"/"+map[bool]string{false: "cancel", true: "timeout"}[timeout], func(t *testing.T) {
				txRefreshRoutes(t, []string{"sql", "go"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held, concurrent bool) {
					base := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					txRefreshSeed(base)
					q := txRefreshSQL(concurrent)
					route := txRefreshHeld(t, p, c, held, q)
					e, finish := transactionalViewsBegin(t, p, b, c, mode)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
					run(fixture.Exec("INSERT INTO txr_source VALUES(2)", 1))
					fixture.RunStep(t, p.Ctx, route(e), b.Recorder, fixture.Exec(q, 0))
					// Release the first refresh lock before arranging an independent blocker.
					finish(false)
					blocker, err := peer.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer blocker.Rollback()
					blockSQL := "REFRESH MATERIALIZED VIEW txr_mv"
					if source {
						blockSQL = "LOCK TABLE txr_source IN ACCESS EXCLUSIVE MODE"
					}
					fixture.RunStep(t, p.Ctx, blocker, b.Recorder, fixture.Exec(blockSQL, 0))
					e, finish = transactionalViewsBegin(t, p, b, c, mode)
					if timeout {
						run(fixture.Exec("SET LOCAL lock_timeout='100ms'", 0))
						d := fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
						fixture.RunStep(t, p.Ctx, route(e), b.Recorder, fixture.ErrorDiagnostic(q, d))
					} else {
						fixture.RelationLockActiveCancel(t, p, b, c, q, func(ctx context.Context) fixture.RelationOutcome {
							r, err := route(e).ExecContext(ctx, q)
							return fixture.RelationOutcome{Result: r, Err: err}
						})
					}
					run(fixture.ErrorDiagnostic("SELECT 1", fixture.TransactionAborted()))
					finish(false)
					if err := blocker.Rollback(); err != nil {
						t.Fatal(err)
					}
					base(txRefreshRows("txr_source", 1))
					base(txRefreshRows("txr_mv", 1))
					base(fixture.Select("SELECT 42 AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(42)}}))
				})
			})
		}
	}
}
