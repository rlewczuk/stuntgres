package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceAdvisoryXactDeadlocks(t *testing.T) {
	advisoryXactScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, peer, observer := advisoryXactConn(t, p, b), advisoryXactConn(t, p, b), advisoryXactConn(t, p, b)
		advisorySessionExec(t, p.Ctx, a, "CREATE TABLE advisory_xact_deadlock_data(v integer)", 0)
		conns := [2]*sql.Conn{a, peer}
		pids := [2]int{advisoryXactPID(t, p, b, a), advisoryXactPID(t, p, b, peer)}
		var dboid int64
		if err := observer.QueryRowContext(p.Ctx, "SELECT oid FROM pg_database WHERE datname=current_database()").Scan(&dboid); err != nil {
			t.Fatal(err)
		}
		for _, pair := range []bool{false, true} {
			for _, mixed := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprint(pair, mixed, prepared), func(t *testing.T) {
						keys := [2]advisorySessionKey{{"6101::bigint", 0, 6101, 1}, {"6102::bigint", 0, 6102, 1}}
						if pair {
							keys = [2]advisorySessionKey{{"61::integer,1::integer", 61, 1, 2}, {"61::integer,2::integer", 61, 2, 2}}
						}
						var executors [2]fixture.Executor
						for i, c := range conns {
							executors[i] = c
							if prepared {
								executors[i] = advisoryXactHeld(t, p, c, keys[1-i].call("pg_advisory_xact_lock"))
							}
							advisorySessionExec(t, p.Ctx, c, "BEGIN", 0)
							advisorySessionExec(t, p.Ctx, c, fmt.Sprintf("INSERT INTO advisory_xact_deadlock_data VALUES(%d)", i), 1)
							name := "pg_advisory_xact_lock"
							if mixed && i == 0 {
								name = "pg_advisory_lock"
							}
							advisorySessionValue(t, p.Ctx, c, keys[i].call(name), "VOID", "")
						}
						ctx, cancel := context.WithTimeout(p.Ctx, 10*time.Second)
						defer cancel()
						type outcome struct {
							index int
							err   error
						}
						done := make(chan outcome, 2)
						started, joined := 0, 0
						t.Cleanup(func() {
							cancel()
							timer := time.NewTimer(12 * time.Second)
							defer timer.Stop()
							for joined < started {
								select {
								case <-done:
									joined++
								case <-timer.C:
									t.Error("deadlock workers failed to join")
									return
								}
							}
						})
						start := func(i int) {
							started++
							go func() {
								rows, err := executors[i].QueryContext(ctx, keys[1-i].call("pg_advisory_xact_lock"))
								if rows != nil {
									var raw any
									if rows.Next() {
										if e := rows.Scan(&raw); err == nil {
											err = e
										}
										if err == nil && fixture.Normalize(raw) != "" {
											err = fmt.Errorf("advisory result %#v", raw)
										}
									}
									for rows.Next() {
									}
									if err == nil {
										err = rows.Err()
									}
									rows.Close()
								}
								done <- outcome{i, err}
							}()
						}
						start(0)
						advisoryXactWait(t, p, observer, pids[0], pids[1], keys[1], false)
						start(1)
						victim, successes := -1, 0
						for joined < 2 {
							select {
							case out := <-done:
								joined++
								if out.err == nil {
									successes++
									continue
								}
								if victim != -1 {
									t.Fatal("multiple deadlock errors", out.err)
								}
								victim = out.index
								advisorySessionDeadlockDetail(t, out.err, b.PG, false, pids, dboid, 0, keys)
								advisorySessionError(t, p.Ctx, conns[victim], "SELECT true", advisorySessionTimeout("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
								advisorySessionExec(t, p.Ctx, conns[victim], "ROLLBACK", 0)
								if mixed && victim == 0 {
									advisoryXactProbe(t, p, observer, keys[0], false, false)
									advisorySessionClear(t, p.Ctx, conns[0])
								}
							case <-ctx.Done():
								t.Fatal("advisory cycle did not resolve", ctx.Err())
							}
						}
						if victim < 0 || successes != 1 {
							t.Fatal("expected one victim and survivor", victim, successes)
						}
						survivor := 1 - victim
						fixture.PreparedReferenceRows(t, p, b, conns[survivor], "SELECT v FROM advisory_xact_deadlock_data", int64(survivor))
						for _, k := range keys {
							advisoryXactProbe(t, p, observer, k, false, false)
						}
						advisorySessionExec(t, p.Ctx, conns[survivor], "COMMIT", 0)
						fixture.PreparedReferenceRows(t, p, b, observer, "SELECT v FROM advisory_xact_deadlock_data", int64(survivor))
						for _, c := range conns {
							advisorySessionClear(t, p.Ctx, c)
						}
						for _, k := range keys {
							advisoryXactProbe(t, p, observer, k, false, true)
						}
						advisorySessionExec(t, p.Ctx, observer, "DELETE FROM advisory_xact_deadlock_data", 1)
					})
				}
			}
		}
	})
}
