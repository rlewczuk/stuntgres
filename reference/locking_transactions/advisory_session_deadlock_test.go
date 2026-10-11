package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func advisorySessionDeadlockDetail(t *testing.T, err error, pg, mixed bool, pids [2]int, database, relation int64, keys [2]advisorySessionKey) {
	t.Helper()
	d := fixture.ExtractDiagnostic(t, err)
	want := advisorySessionTimeout("40P01", "deadlock detected")
	want.Hint = "See server log for query details."
	want.Detail = d.Detail
	// PG logs query texts separately; client Detail is exactly the two edges.
	// The mixed ordinary command is LOCK TABLE AX, an unpositioned utility
	// wait in both backends, not SELECT/DML semantic relation analysis.
	fixture.AssertDiagnostic(t, err, &want)
	lines := strings.Split(d.Detail, "\n")
	if len(lines) != 2 {
		t.Fatal("expected two actual cycle edges", d.Detail)
	}
	if pg {
		advisory := regexp.MustCompile(`^Process ([1-9][0-9]*) waits for ExclusiveLock on advisory lock \[([1-9][0-9]*),([0-9]+),([0-9]+),([12])\]; blocked by process ([1-9][0-9]*)\.$`)
		rel := regexp.MustCompile(`^Process ([1-9][0-9]*) waits for AccessExclusiveLock on relation ([1-9][0-9]*) of database ([1-9][0-9]*); blocked by process ([1-9][0-9]*)\.$`)
		seen := map[int]int{}
		for _, line := range lines {
			if m := advisory.FindStringSubmatch(line); m != nil {
				waiter, _ := strconv.Atoi(m[1])
				blocker, _ := strconv.Atoi(m[6])
				if waiter == blocker || !(waiter == pids[0] && blocker == pids[1] || waiter == pids[1] && blocker == pids[0]) {
					t.Fatal("non-cycle process edge", line, pids)
				}
				idx := 0
				if waiter == pids[0] {
					idx = 1
				}
				k := keys[idx]
				if m[2] != fmt.Sprint(database) || m[3] != fmt.Sprint(k.high) || m[4] != fmt.Sprint(k.low) || m[5] != fmt.Sprint(k.space) {
					t.Fatal("incorrect advisory identity", line, k, database)
				}
				seen[waiter]++
			} else if m := rel.FindStringSubmatch(line); mixed && m != nil {
				if m[1] != fmt.Sprint(pids[0]) || m[4] != fmt.Sprint(pids[1]) || m[2] != fmt.Sprint(relation) || m[3] != fmt.Sprint(database) {
					t.Fatal("incorrect mixed relation edge", line)
				}
				seen[pids[0]]++
			} else {
				t.Fatal("unexpected PG deadlock detail", line)
			}
		}
		if seen[pids[0]] != 1 || seen[pids[1]] != 1 {
			t.Fatal("missing closed two-session cycle", d.Detail)
		}
		return
	}
	advisory := regexp.MustCompile(`^Session ([1-9][0-9]*) waits for ExclusiveLock on advisory lock \[([0-9]+),([0-9]+),([12])\]; blocked by session ([1-9][0-9]*)\.$`)
	rel := regexp.MustCompile(`^Transaction ([1-9][0-9]*) \(session ([1-9][0-9]*), epoch ([1-9][0-9]*)\) waits for AccessExclusiveLock on relation ([1-9][0-9]*) \(class 1259, sub-ID 0, epoch ([1-9][0-9]*)\); blocked by transaction ([1-9][0-9]*) \(session ([1-9][0-9]*), epoch ([1-9][0-9]*)\)\.$`)
	var waiters, blockers []string
	resources := map[string]bool{}
	for _, line := range lines {
		if m := advisory.FindStringSubmatch(line); m != nil {
			if m[1] == m[5] {
				t.Fatal("self edge", line)
			}
			waiters = append(waiters, m[1])
			blockers = append(blockers, m[5])
			resource := strings.Join(m[2:5], ",")
			valid := false
			for i, k := range keys {
				if mixed && i == 1 {
					continue
				}
				if resource == fmt.Sprintf("%d,%d,%d", k.high, k.low, k.space) {
					valid = true
				}
			}
			if !valid || resources[resource] {
				t.Fatal("incorrect/repeated advisory resource", line)
			}
			resources[resource] = true
		} else if m := rel.FindStringSubmatch(line); mixed && m != nil {
			if m[2] == m[7] || m[3] != m[5] || m[3] != m[8] {
				t.Fatal("mixed relation identity", line)
			}
			waiters = append(waiters, m[2])
			blockers = append(blockers, m[7])
		} else {
			t.Fatal("unexpected native detail", line)
		}
	}
	if waiters[0] != blockers[1] || waiters[1] != blockers[0] {
		t.Fatal("physical session graph is not closed", d.Detail)
	}
}

func TestReferenceAdvisorySessionDeadlock(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		for _, pair := range []bool{false, true} {
			t.Run(fmt.Sprintf("mixed%v/pair%v", mixed, pair), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						a, peer, observer := advisorySessionConn(t, p, b), advisorySessionConn(t, p, b), advisorySessionConn(t, p, b)
						conns := [2]*sql.Conn{a, peer}
						keys := [2]advisorySessionKey{{"101::bigint", 0, 101, 1}, {"102::bigint", 0, 102, 1}}
						if pair {
							keys = [2]advisorySessionKey{{"-1::integer,101::integer", 4294967295, 101, 2}, {"-1::integer,102::integer", 4294967295, 102, 2}}
						}
						var pids [2]int
						var dboid, oid int64
						if b.PG {
							pids = [2]int{fixture.LockPID(t, p.Ctx, a), fixture.LockPID(t, p.Ctx, peer)}
							if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT oid FROM pg_database WHERE datname=current_database()").Scan(&dboid); err != nil {
								t.Fatal(err)
							}
						}
						if mixed {
							advisorySessionExec(t, p.Ctx, a, "CREATE TABLE advisory_deadlock_relation(id integer)", 0)
							if b.PG {
								oid = fixture.LockOID(t, p.Ctx, "advisory_deadlock_relation")
							}
						}
						for i, c := range conns {
							advisorySessionExec(t, p.Ctx, c, "BEGIN", 0)
							t.Cleanup(func() { deadlockPublicRollback(c) })
							advisorySessionValue(t, p.Ctx, c, keys[i].call("pg_advisory_lock"), "VOID", "")
						}
						if mixed {
							advisorySessionExec(t, p.Ctx, peer, "LOCK TABLE advisory_deadlock_relation IN ACCESS EXCLUSIVE MODE", 0)
						}
						ctx, cancel := context.WithTimeout(p.Ctx, 10*time.Second)
						defer cancel()
						type outcome struct {
							index int
							err   error
						}
						done := make(chan outcome, 2)
						joined, started := 0, 0
						t.Cleanup(func() {
							cancel()
							deadline := time.NewTimer(12 * time.Second)
							defer deadline.Stop()
							for joined < started {
								select {
								case <-done:
									joined++
								case <-deadline.C:
									t.Error("deadlock cleanup did not join")
									return
								}
							}
						})
						start := func(i int) {
							started++
							go func() {
								var err error
								if mixed && i == 0 {
									_, err = conns[i].ExecContext(ctx, "LOCK TABLE advisory_deadlock_relation IN ACCESS EXCLUSIVE MODE")
								} else {
									var v any
									err = conns[i].QueryRowContext(ctx, keys[1-i].call("pg_advisory_lock")).Scan(&v)
									if err == nil && fixture.Normalize(v) != "" {
										err = fmt.Errorf("acquire result %#v", v)
									}
								}
								done <- outcome{i, err}
							}()
						}
						start(0)
						if b.PG {
							if mixed {
								fixture.LockWaitBehind(t, p.Ctx, pids[0], pids[1], oid, "AccessExclusiveLock")
							} else {
								advisorySessionWait(t, p.Ctx, pids[0], pids[1], keys[1], false)
							}
						}
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
									t.Fatal("multiple victims", out.err)
								}
								victim = out.index
								advisorySessionDeadlockDetail(t, out.err, b.PG, mixed, pids, dboid, oid, keys)
								advisorySessionError(t, p.Ctx, conns[victim], "SELECT true", advisorySessionTimeout("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
								advisorySessionExec(t, p.Ctx, conns[victim], "ROLLBACK", 0)
								// Transaction death must not release previously held
								// session ownership; explicit cleanup unlocks survivor.
								advisorySessionValue(t, p.Ctx, observer, keys[victim].call("pg_try_advisory_lock"), "BOOL", false)
								advisorySessionClear(t, p.Ctx, conns[victim])
							case <-ctx.Done():
								t.Fatal("cycle did not resolve", ctx.Err())
							}
						}
						if victim == -1 || successes != 1 {
							t.Fatal("expected exactly one error victim and one survivor", victim, successes)
						}
						for _, c := range conns {
							advisorySessionExec(t, p.Ctx, c, "ROLLBACK", 0)
							advisorySessionClear(t, p.Ctx, c)
						}
						for _, k := range keys {
							advisorySessionValue(t, p.Ctx, observer, k.call("pg_try_advisory_lock"), "BOOL", true)
						}
						advisorySessionClear(t, p.Ctx, observer)
					})
				}
			})
		}
	}
}
