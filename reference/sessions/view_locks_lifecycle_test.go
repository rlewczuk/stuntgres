package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceViewLocksPrepared(t *testing.T) {
	for _, finish := range []string{"COMMIT", "ROLLBACK"} {
		t.Run(finish, func(t *testing.T) {
			viewLocksScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, aid := blockingConn(t, p, b)
				o, _ := blockingConn(t, p, b)
				namespaceExec(t, p, b, a, "CREATE TABLE view_locks_prepared(v INT)", "BEGIN")
				fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("INSERT INTO view_locks_prepared VALUES(1)", 1))
				oid := viewLocksOID(t, p, o, "SELECT 'view_locks_prepared'::regclass::oid")
				owner := viewLocksOwner(t, p, b, o, aid)
				xid := viewLocksXID(t, p, b, o, aid)
				gid := "view_locks_prepared_" + strings.ToLower(finish)
				fixture.PreparedReferencePrepare(t, p, b, a, gid)
				var preparedXID string
				if err := o.QueryRowContext(p.Ctx, "SELECT transaction FROM pg_prepared_xacts WHERE gid=$1", gid).Scan(&preparedXID); err != nil {
					t.Fatal(err)
				}
				if preparedXID != xid.XID.String {
					t.Fatal("prepared XID witness", preparedXID, xid)
				}
				check := func() {
					t.Helper()
					rows := viewLocksRead(t, p.Ctx, b, o, "virtualtransaction=$1", owner.Owner.String)
					if len(rows) != 2 {
						t.Fatal("prepared relation/XID inventory", rows)
					}
					for _, r := range rows {
						if r.PID.Valid || !r.Granted || r.Owner != owner.Owner {
							t.Fatal("prepared origin association", r)
						}
						switch r.Kind {
						case "transactionid":
							viewLocksTarget(t, r, "transactionid", 0, 0)
							if r.XID.String != preparedXID || r.Mode != "ExclusiveLock" {
								t.Fatal(r)
							}
						case "relation":
							viewLocksTarget(t, r, "relation", viewLocksDatabase(t, p, o), oid)
							if r.Mode != "RowExclusiveLock" {
								t.Fatal(r)
							}
						default:
							t.Fatal("prepared fabricated lifetime", r)
						}
					}
				}
				check()
				if err := a.Raw(func(any) error { return driver.ErrBadConn }); err != driver.ErrBadConn {
					t.Fatal(err)
				}
				check()
				namespaceExec(t, p, b, o, finish+" PREPARED '"+gid+"'")
				viewLocksEmpty(t, p, b, o, "virtualtransaction=$1", owner.Owner.String)
			})
		})
	}
}

func TestReferenceViewLocksPredicates(t *testing.T) {
	viewLocksScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, aid := blockingConn(t, p, b)
		overlap, _ := blockingConn(t, p, b)
		aborted, abortID := blockingConn(t, p, b)
		o, _ := blockingConn(t, p, b)
		namespaceExec(t, p, b, a, "CREATE TABLE view_locks_predicate(v INT)")
		fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("INSERT INTO view_locks_predicate VALUES(1)", 1))
		oid := viewLocksOID(t, p, o, "SELECT 'view_locks_predicate'::regclass::oid")
		db := viewLocksDatabase(t, p, o)
		read := func(c *sql.Conn) {
			namespaceExec(t, p, b, c, "BEGIN ISOLATION LEVEL SERIALIZABLE")
			fixture.PreparedReferenceRows(t, p, b, c, "SELECT v FROM view_locks_predicate", 1)
		}
		read(overlap)
		read(a)
		owner := viewLocksOwner(t, p, b, o, aid)
		where := "relation=$1 AND mode='SIReadLock' AND pid=$2"
		check := func() {
			t.Helper()
			r := viewLocksOne(t, viewLocksRead(t, p.Ctx, b, o, where, oid, aid))
			viewLocksTarget(t, r, "relation", db, oid)
			if !r.Granted || r.Fast || r.Wait.Valid || r.Owner != owner.Owner {
				t.Fatal("SSI association/lifetime", r, owner)
			}
		}
		check()
		namespaceExec(t, p, b, a, "COMMIT")
		check()
		read(aborted)
		viewLocksOne(t, viewLocksRead(t, p.Ctx, b, o, where, oid, abortID))
		namespaceExec(t, p, b, aborted, "ROLLBACK")
		viewLocksEmpty(t, p, b, o, where, oid, abortID)
		viewLocksEmpty(t, p, b, o, "mode='SIReadLock' AND NOT granted")
		namespaceExec(t, p, b, overlap, "ROLLBACK")
		viewLocksEmpty(t, p, b, o, "relation=$1 AND mode='SIReadLock'", oid)
	})
}

func viewLocksCancel(t *testing.T, p *fixture.Pair, b fixture.Backend, pid int, cancel context.CancelFunc, await func() fixture.RelationOutcome) {
	t.Helper()
	if b.PG {
		var ok bool
		if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&ok); err != nil || !ok {
			t.Fatal("server cancel", ok, err)
		}
	} else {
		cancel()
	}
	o := await()
	if o.Result != nil || o.Rows != nil || o.Stmt != nil {
		t.Fatal("canceled acquisition published output", o)
	}
	if b.PG {
		fixture.WaitsDiagnostic(t, o.Err, "57014", "canceling statement due to user request")
	} else {
		if !errors.Is(o.Err, context.Canceled) {
			t.Fatal("ordinary native caller cancellation", o.Err)
		}
		var state interface{ SQLState() string }
		if errors.As(o.Err, &state) {
			t.Fatal("fabricated cancellation SQLSTATE", o.Err)
		}
	}
}

func TestReferenceViewLocksCancellation(t *testing.T) {
	viewLocksScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, aid := blockingConn(t, p, b)
		middle, mid := blockingConn(t, p, b)
		tail, tid := blockingConn(t, p, b)
		o, _ := blockingConn(t, p, b)
		namespaceExec(t, p, b, a, "CREATE TABLE view_locks_cancel(v INT)")
		oid := viewLocksOID(t, p, o, "SELECT 'view_locks_cancel'::regclass::oid")
		namespaceExec(t, p, b, a, "BEGIN", "LOCK TABLE view_locks_cancel IN ACCESS SHARE MODE")
		namespaceExec(t, p, b, middle, "BEGIN")
		ctx, cancel := context.WithCancel(p.Ctx)
		defer cancel()
		before := time.Now()
		awaitMiddle := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
			r, err := middle.ExecContext(ctx, "LOCK TABLE view_locks_cancel IN ACCESS EXCLUSIVE MODE")
			return fixture.RelationOutcome{Result: r, Err: err}
		})
		rows := viewLocksPoll(t, p, b, o, "relation=$1", func(r []viewLockRow) bool { return viewLocksHasPending(r, mid, "AccessExclusiveLock") }, oid)
		viewLocksPending(t, rows, mid, "AccessExclusiveLock", before)
		namespaceExec(t, p, b, tail, "BEGIN")
		awaitTail := blockingLock(t, p, tail, "LOCK TABLE view_locks_cancel IN ACCESS SHARE MODE", false)
		viewLocksPoll(t, p, b, o, "relation=$1", func(r []viewLockRow) bool {
			return len(r) == 3 && viewLocksHasPending(r, tid, "AccessShareLock") && viewLocksHasPending(r, mid, "AccessExclusiveLock")
		}, oid)
		viewLocksCancel(t, p, b, mid, cancel, awaitMiddle)
		blockingLockDone(t, awaitTail)
		viewLocksEmpty(t, p, b, o, "pid=$1 AND NOT granted", mid)
		rows = viewLocksRead(t, p.Ctx, b, o, "relation=$1", oid)
		if len(rows) != 2 {
			t.Fatal("queue wake after cancel", rows)
		}
		seen := map[int64]bool{}
		for _, r := range rows {
			if !r.Granted || r.Mode != "AccessShareLock" {
				t.Fatal("queue grant", r)
			}
			seen[r.PID.Int64] = true
		}
		if !seen[int64(aid)] || !seen[int64(tid)] {
			t.Fatal("holder released as cancellation shortcut", rows)
		}
		namespaceExec(t, p, b, middle, "ROLLBACK")
		namespaceExec(t, p, b, tail, "ROLLBACK")
		namespaceExec(t, p, b, a, "ROLLBACK")
	})
}

func TestReferenceViewLocksDeadlock(t *testing.T) {
	viewLocksScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, aid := blockingConn(t, p, b)
		peer, bid := blockingConn(t, p, b)
		o, _ := blockingConn(t, p, b)
		namespaceExec(t, p, b, a, "CREATE TABLE view_locks_dead_left(v INT)", "CREATE TABLE view_locks_dead_right(v INT)")
		oids := [2]int64{viewLocksOID(t, p, o, "SELECT 'view_locks_dead_left'::regclass::oid"), viewLocksOID(t, p, o, "SELECT 'view_locks_dead_right'::regclass::oid")}
		db := viewLocksDatabase(t, p, o)
		namespaceExec(t, p, b, a, "BEGIN", "LOCK TABLE view_locks_dead_left IN ACCESS EXCLUSIVE MODE")
		namespaceExec(t, p, b, peer, "BEGIN", "LOCK TABLE view_locks_dead_right IN ACCESS EXCLUSIVE MODE")
		conns := [2]*sql.Conn{a, peer}
		pids := [2]int{aid, bid}
		queries := [2]string{"LOCK TABLE view_locks_dead_right IN ACCESS EXCLUSIVE MODE", "LOCK TABLE view_locks_dead_left IN ACCESS EXCLUSIVE MODE"}
		type outcome struct {
			index         int
			result        sql.Result
			err, rollback error
		}
		ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
		defer cancel()
		done := make(chan outcome, 2)
		joined, started := 0, 0
		t.Cleanup(func() {
			cancel()
			deadline := time.NewTimer(5 * time.Second)
			defer deadline.Stop()
			for joined < started {
				select {
				case <-done:
					joined++
				case <-deadline.C:
					t.Error("deadlock workers did not join")
					return
				}
			}
		})
		start := func(i int) {
			started++
			go func() {
				r, err := conns[i].ExecContext(ctx, queries[i])
				recovery, stop := context.WithTimeout(context.Background(), 2*time.Second)
				defer stop()
				_, rb := conns[i].ExecContext(recovery, "ROLLBACK")
				done <- outcome{i, r, err, rb}
			}()
		}
		start(0)
		// A real first edge is observed before creating the opposite edge.
		viewLocksPoll(t, p, b, o, "relation=$1 AND pid=$2", func(rows []viewLockRow) bool { return viewLocksHasPending(rows, aid, "AccessExclusiveLock") }, oids[1], aid)
		start(1)
		var outcomes [2]outcome
		for joined < 2 {
			select {
			case v := <-done:
				outcomes[v.index] = v
				joined++
			case <-ctx.Done():
				t.Fatal("deadlock join", ctx.Err())
			}
		}
		victim := -1
		for i, v := range outcomes {
			if v.rollback != nil {
				t.Fatal("deadlock rollback", v.rollback)
			}
			if v.err != nil {
				if victim != -1 {
					t.Fatal("two deadlock victims", outcomes)
				}
				victim = i
			} else {
				viewLocksAffected(t, fixture.RelationOutcome{Result: v.result}, 0)
			}
		}
		if victim < 0 || outcomes[victim].result != nil {
			t.Fatal("deadlock victim/output", outcomes)
		}
		d := fixture.ExtractDiagnostic(t, outcomes[victim].err)
		want := fixture.ControlsReferenceDiagnostic("40P01", "deadlock detected")
		want.Hint = "See server log for query details."
		want.Detail = d.Detail
		fixture.AssertDiagnostic(t, outcomes[victim].err, &want)
		viewLocksDeadlockEdges(t, b.PG, d.Detail, pids, oids, db)
		viewLocksEmpty(t, p, b, o, "relation IN ($1,$2)", oids[0], oids[1])
	})
}

func viewLocksDeadlockEdges(t *testing.T, pg bool, detail string, pids [2]int, oids [2]int64, db int64) {
	t.Helper()
	lines := strings.Split(detail, "\n")
	if len(lines) != 2 {
		t.Fatal("actual two-edge detail", detail)
	}
	pattern := regexp.MustCompile(`^Process ([1-9][0-9]*) waits for AccessExclusiveLock on relation ([1-9][0-9]*) of database ([1-9][0-9]*); blocked by process ([1-9][0-9]*)\.$`)
	native := regexp.MustCompile(`^Transaction ([1-9][0-9]*) \(session ([1-9][0-9]*), epoch ([1-9][0-9]*)\) waits for AccessExclusiveLock on relation ([1-9][0-9]*) \(class 1259, sub-ID 0, epoch ([1-9][0-9]*)\); blocked by transaction ([1-9][0-9]*) \(session ([1-9][0-9]*), epoch ([1-9][0-9]*)\)\.$`)
	seen := map[int]bool{}
	for _, line := range lines {
		var waiter, blocker, relation string
		if pg {
			m := pattern.FindStringSubmatch(line)
			if m == nil || m[3] != fmt.Sprint(db) {
				t.Fatal("PG cycle edge", line)
			}
			waiter, relation, blocker = m[1], m[2], m[4]
		} else {
			m := native.FindStringSubmatch(line)
			if m == nil || m[3] != m[5] || m[3] != m[8] || m[1] == m[6] {
				t.Fatal("native cycle edge", line)
			}
			waiter, relation, blocker = m[2], m[4], m[7]
		}
		pid, err := strconv.Atoi(waiter)
		if err != nil {
			t.Fatal(err)
		}
		i := -1
		for n, pidWant := range pids {
			if pid == pidWant {
				i = n
			}
		}
		if i < 0 || seen[pid] || blocker != fmt.Sprint(pids[1-i]) || relation != fmt.Sprint(oids[1-i]) {
			t.Fatal("untruthful cycle identity", line, pids, oids)
		}
		seen[pid] = true
	}
}

func TestReferenceViewLocksControls(t *testing.T) {
	for _, restore := range []bool{false, true} {
		t.Run(map[bool]string{false: "reset", true: "restore"}[restore], func(t *testing.T) {
			viewLocksScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, aid := blockingConn(t, p, b)
				w, wid := blockingConn(t, p, b)
				o, _ := blockingConn(t, p, b)
				namespaceExec(t, p, b, a, "CREATE TABLE view_locks_control(v INT)")
				oid := viewLocksOID(t, p, o, "SELECT 'view_locks_control'::regclass::oid")
				q := viewLocksQuery("pid=$1 AND locktype='relation'")
				s, err := o.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				x := fixture.HeldStatement{Executor: o, Statement: s, SQL: q}
				namespaceExec(t, p, b, a, "BEGIN", "LOCK TABLE view_locks_control IN ACCESS EXCLUSIVE MODE")
				rows := viewLocksRead(t, p.Ctx, b, x, "pid=$1 AND locktype='relation'", aid)
				viewLocksOne(t, rows)
				saved := append([]viewLockRow(nil), rows...)
				namespaceExec(t, p, b, a, "ROLLBACK")
				viewLocksEmpty(t, p, b, x, "pid=$1 AND locktype='relation'", aid)
				if !reflect.DeepEqual(saved, rows) {
					t.Fatal("retained snapshot mutation")
				}
				if b.PG {
					return
				} // PostgreSQL has no Reset/Dump/Restore API.
				viewLocksCall(t, p, a, "SELECT pg_advisory_lock(-71391::bigint)", nil)
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(dump.String(), "pg_advisory") || strings.Contains(dump.String(), "pg_locks") || strings.Contains(dump.String(), "SIReadLock") {
					t.Fatal("dump serialized live locks", dump.String())
				}
				predicate, predID := blockingConn(t, p, b)
				namespaceExec(t, p, b, predicate, "BEGIN ISOLATION LEVEL SERIALIZABLE")
				fixture.RunStep(t, p.Ctx, predicate, b.Recorder, fixture.Select("SELECT v FROM view_locks_control", []string{"v"}, []string{"INT4"}, nil))
				viewLocksOne(t, viewLocksRead(t, p.Ctx, b, o, "relation=$1 AND pid=$2 AND mode='SIReadLock'", oid, predID))
				namespaceExec(t, p, b, a, "BEGIN", "LOCK TABLE view_locks_control IN ACCESS SHARE MODE")
				namespaceExec(t, p, b, w, "BEGIN")
				await := blockingLock(t, p, w, "LOCK TABLE view_locks_control IN ACCESS EXCLUSIVE MODE", false)
				viewLocksPoll(t, p, b, o, "relation=$1", func(r []viewLockRow) bool { return viewLocksHasPending(r, wid, "AccessExclusiveLock") }, oid)
				if restore {
					err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes()))
				} else {
					err = stuntgres.Reset(p.Ctx, p.Native)
				}
				if err != nil {
					t.Fatal(err)
				}
				out := await()
				if out.Result != nil {
					t.Fatal("fenced wait published result")
				}
				fixture.TransactionOrdinary(t, out.Err, "stuntgres: catalog replaced before publication")
				namespaceExec(t, p, b, a, "ROLLBACK")
				namespaceExec(t, p, b, w, "ROLLBACK")
				namespaceExec(t, p, b, predicate, "ROLLBACK")
				viewLocksEmpty(t, p, b, o, "relation=$1", oid)
				viewLocksEmpty(t, p, b, x, "pid=$1 AND locktype='relation'", aid)
				advisory := viewLocksOne(t, viewLocksRead(t, p.Ctx, b, o, "pid=$1 AND locktype='advisory'", aid))
				viewLocksTarget(t, advisory, "advisory", viewLocksDatabase(t, p, o), 0)
				viewLocksCall(t, p, a, "SELECT pg_advisory_unlock(-71391::bigint)", true)
				// Replaying the dump after unlock cannot resurrect session ownership.
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				viewLocksEmpty(t, p, b, o, "pid=$1 AND locktype='advisory'", aid)
				viewLocksEmpty(t, p, b, o, "mode='SIReadLock' OR NOT granted")
			})
		})
	}
}
