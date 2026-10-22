package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"runtime"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func vacuumPhaseWarning(code, message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: code, Message: message}
}
func vacuumPhaseNotices(t *testing.T, got, want []fixture.Diagnostic) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("complete phase-ordered diagnostics got %#v want %#v", got, want)
	}
}
func vacuumPhaseExecute(t *testing.T, p *fixture.Pair, c *sql.Conn, rec *fixture.Recorder, held bool, s fixture.Step) {
	t.Helper()
	if held {
		fixture.RunStep(t, p.Ctx, vacuumHeld(t, p, c, s.Query), rec, s)
	} else {
		fixture.RunStep(t, p.Ctx, c, rec, s)
	}
}

// This observes the real backend's exact parent SUE request and actual SHARE
// holder/blocking edge. It never queries PostgreSQL Admin for native work.
func vacuumPhaseWait(t *testing.T, p *fixture.Pair, observer *sql.Conn, table string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	for {
		var waiter, holder int
		err := observer.QueryRowContext(ctx, "SELECT pid FROM pg_locks WHERE locktype='relation' AND relation=$1::regclass AND mode='ShareUpdateExclusiveLock' AND NOT granted", table).Scan(&waiter)
		if errors.Is(err, sql.ErrNoRows) {
			if ctx.Err() != nil {
				t.Fatal("exact parent work wait not observed", ctx.Err())
			}
			runtime.Gosched()
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := observer.QueryRowContext(ctx, "SELECT pid FROM pg_locks WHERE locktype='relation' AND relation=$1::regclass AND mode='ShareLock' AND granted", table).Scan(&holder); err != nil {
			t.Fatal(err)
		}
		var blocked bool
		if err := observer.QueryRowContext(ctx, "SELECT $1=ANY(pg_blocking_pids($2))", holder, waiter).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if waiter <= 0 || holder <= 0 || waiter == holder || !blocked {
			t.Fatal("not the actual holder/waiter edge", waiter, holder, blocked)
		}
		return
	}
}
func vacuumPhaseStart(t *testing.T, p *fixture.Pair, c *sql.Conn, q string, held bool) func() {
	t.Helper()
	var call func(context.Context) fixture.RelationOutcome
	if held {
		h := vacuumHeld(t, p, c, q)
		call = func(ctx context.Context) fixture.RelationOutcome {
			r, e := h.Statement.ExecContext(ctx)
			return fixture.RelationOutcome{Result: r, Err: e}
		}
	} else {
		call = func(ctx context.Context) fixture.RelationOutcome {
			r, e := c.ExecContext(ctx, q)
			return fixture.RelationOutcome{Result: r, Err: e}
		}
	}
	await := fixture.LockOperation(t, p.Ctx, call)
	return func() {
		t.Helper()
		out := await()
		if out.Err != nil || out.Result == nil || out.Rows != nil {
			t.Fatal("actual waiting VACUUM failed", out.Err)
		}
		n, err := out.Result.RowsAffected()
		if err != nil || n != 0 {
			t.Fatal("VACUUM command count", n, err)
		}
	}
}

func TestReferenceVacuumF6ExecutionPhases(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			for _, kind := range []string{"view", "sequence", "index"} {
				for _, main := range []bool{true, false} {
					for _, nonheapFirst := range []bool{true, false} {
						t.Run(kind+map[bool]string{true: "/main", false: "/no_main"}[main]+map[bool]string{true: "/nonheap_first", false: "/locked_first"}[nonheapFirst], func(t *testing.T) {
							vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
								var notices []fixture.Diagnostic
								_, worker, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
								holder := vacuumConn(t, p, b)
								run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, worker, rec, s) }
								run(fixture.Exec("CREATE TABLE phase_locked(id INT)", 0))
								switch kind {
								case "view":
									run(fixture.Exec("CREATE VIEW phase_nonheap AS SELECT 1 AS id", 0))
								case "sequence":
									run(fixture.Exec("CREATE SEQUENCE phase_nonheap", 0))
								case "index":
									run(fixture.Exec("CREATE INDEX phase_nonheap ON phase_locked(id)", 0))
								}
								q := "VACUUM (SKIP_LOCKED,PROCESS_MAIN " + map[bool]string{true: "true", false: "false"}[main] + ") "
								if nonheapFirst {
									q += "phase_nonheap,phase_locked"
								} else {
									q += "phase_locked,phase_nonheap"
								}
								h := vacuumHeld(t, p, worker, q)
								vacuumRun(t, p, b, holder, fixture.Exec("BEGIN", 0))
								defer holder.ExecContext(p.Ctx, "ROLLBACK")
								vacuumRun(t, p, b, holder, fixture.Exec("LOCK TABLE ONLY phase_locked IN ACCESS EXCLUSIVE MODE", 0))
								notices = nil
								if held {
									fixture.RunStep(t, p.Ctx, h, rec, fixture.Exec(q, 0))
								} else {
									run(fixture.Exec(q, 0))
								}
								vacuumPhaseNotices(t, notices, []fixture.Diagnostic{vacuumPhaseWarning("55P03", `skipping vacuum of "phase_locked" --- lock not available`), vacuumPhaseWarning("01000", `skipping "phase_nonheap" --- cannot vacuum non-tables or special system tables`)})
								vacuumCount(t, p.Ctx, worker, "phase_locked", 0)
								vacuumProgressEmpty(t, p, b, worker)
							})
						})
					}
				}
			}
			for _, prefix := range []string{"nonheap", "denied_nonheap", "heap"} {
				t.Run("later_missing/"+prefix, func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"phase_actor"})
					vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
						owner := vacuumConn(t, p, b)
						var notices []fixture.Diagnostic
						_, worker, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
						for _, q := range []string{"CREATE ROLE phase_actor", "GRANT USAGE,CREATE ON SCHEMA public TO phase_actor", "CREATE TABLE phase_denied(id INT)", "CREATE TABLE phase_heap(id INT)", "GRANT MAINTAIN ON phase_heap TO phase_actor"} {
							vacuumRun(t, p, b, owner, fixture.Exec(q, 0))
						}
						fixture.RunStep(t, p.Ctx, worker, rec, fixture.Exec("SET ROLE phase_actor", 0))
						fixture.RunStep(t, p.Ctx, worker, rec, fixture.Exec("CREATE VIEW phase_nonheap AS SELECT 1 AS id", 0))
						list := map[string]string{"nonheap": "phase_nonheap", "denied_nonheap": "phase_denied,phase_nonheap", "heap": "phase_heap"}[prefix]
						q := "VACUUM " + list + ",phase_missing"
						notices = nil
						vacuumPhaseExecute(t, p, worker, rec, held, fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P01", Message: `relation "phase_missing" does not exist`}))
						var want []fixture.Diagnostic
						if prefix == "denied_nonheap" {
							want = []fixture.Diagnostic{vacuumPhaseWarning("01000", `permission denied to vacuum "phase_denied", skipping it`)}
						}
						vacuumPhaseNotices(t, notices, want)
						vacuumCount(t, p.Ctx, owner, "phase_heap", 0)
						vacuumProgressEmpty(t, p, b, owner)
					})
				})
			}
			for _, main := range []bool{true, false} {
				t.Run(map[bool]string{true: "view_work_lock", false: "view_work_lock_no_main"}[main], func(t *testing.T) {
					vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
						var notices []fixture.Diagnostic
						_, worker, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
						holder := vacuumConn(t, p, b)
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, worker, rec, s) }
						run(fixture.Exec("CREATE VIEW phase_nonheap AS SELECT 1 AS id", 0))
						vacuumRun(t, p, b, holder, fixture.Exec("BEGIN", 0))
						defer holder.ExecContext(p.Ctx, "ROLLBACK")
						vacuumRun(t, p, b, holder, fixture.Exec("LOCK TABLE ONLY phase_nonheap IN SHARE MODE", 0))
						flag := map[bool]string{true: "true", false: "false"}[main]
						notices = nil
						vacuumPhaseExecute(t, p, worker, rec, held, fixture.Exec("VACUUM (SKIP_LOCKED,PROCESS_MAIN "+flag+") phase_nonheap", 0))
						vacuumPhaseNotices(t, notices, []fixture.Diagnostic{vacuumPhaseWarning("55P03", `skipping vacuum of "phase_nonheap" --- lock not available`)})
						run(fixture.Exec("SET lock_timeout='100ms'", 0))
						notices = nil
						vacuumPhaseExecute(t, p, worker, rec, held, fixture.ImplicitError("VACUUM (PROCESS_MAIN "+flag+") phase_nonheap", fixture.Diagnostic{Code: "55P03", Message: "canceling statement due to lock timeout"}))
						vacuumPhaseNotices(t, notices, nil)
						vacuumProgressEmpty(t, p, b, worker)
						vacuumRun(t, p, b, holder, fixture.Exec("ROLLBACK", 0))
						run(fixture.Exec("RESET lock_timeout", 0))
						notices = nil
						vacuumPhaseExecute(t, p, worker, rec, held, fixture.Exec("VACUUM (PROCESS_MAIN "+flag+") phase_nonheap", 0))
						vacuumPhaseNotices(t, notices, []fixture.Diagnostic{vacuumPhaseWarning("01000", `skipping "phase_nonheap" --- cannot vacuum non-tables or special system tables`)})
					})
				})
			}
		})
	}
}
