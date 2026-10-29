package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceVacuumProgressActiveWorkers(t *testing.T) {
	vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		setup, observer := vacuumConn(t, p, b), vacuumConn(t, p, b)
		workers := []*sql.Conn{vacuumConn(t, p, b), vacuumConn(t, p, b)}
		var identities []copyProgressIdentity
		var held []*sql.Stmt
		for i, name := range []string{"vacuum_workers_a", "vacuum_workers_b"} {
			vacuumProgressWorkload(t, p, b, setup, name)
			identities = append(identities, copyProgressIdentify(t, p, workers[i], name))
			stmt, err := observer.PrepareContext(p.Ctx, vacuumProgressQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held = append(held, stmt)
		}
		bag, err := observer.PrepareContext(p.Ctx, "SELECT * FROM pg_catalog.pg_stat_progress_vacuum WHERE pid IN ($1,$2)")
		if err != nil {
			t.Fatal(err)
		}
		defer bag.Close()
		a := vacuumProgressLaunch(t, p, b, workers[0], "vacuum_workers_a")
		c := vacuumProgressLaunch(t, p, b, workers[1], "vacuum_workers_b")
		vacuumProgressOverlap(t, p, b, bag, identities, a, c)
		vacuumProgressCancel(t, p, b, observer, identities[0].pid, "vacuum_workers_a", a.cancel, func() error { err := <-a.done; a.joined = true; return err })
		vacuumProgressVisible(t, vacuumProgressObserve(t, p, held[1], identities[1], c), identities[1], b.PG)
		vacuumProgressCancel(t, p, b, observer, identities[1].pid, "vacuum_workers_b", c.cancel, func() error { err := <-c.done; c.joined = true; return err })
		vacuumProgressEmpty(t, p, b, observer)
	})
}

// Each condition attempt owns one complete SQL bag and one complete native
// capture. Neither SQL rows nor storage admission accumulate across attempts.
func vacuumProgressOverlap(t *testing.T, p *fixture.Pair, b fixture.Backend, stmt *sql.Stmt, ids []copyProgressIdentity, a, c *vacuumProgressRunning) {
	t.Helper()
	if ids[0].pid == ids[1].pid || a.workerStack == c.workerStack {
		t.Fatal("workers must have distinct backend and goroutine identities")
	}
	ctx, cancel := context.WithTimeout(p.Ctx, 30*time.Second)
	defer cancel()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		vacuumProgressUnfinished(t, a, c)
		seen := func() map[int64]vacuumProgressSnapshot {
			rows, err := stmt.QueryContext(ctx, ids[0].pid, ids[1].pid)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			bag := make(map[int64]vacuumProgressSnapshot, 2)
			for rows.Next() {
				s, _ := vacuumProgressScan(t, rows)
				if _, exists := bag[s.pid]; exists {
					t.Fatal("duplicate worker", s.pid)
				}
				switch s.pid {
				case ids[0].pid:
					vacuumProgressVisible(t, s, ids[0], b.PG)
				case ids[1].pid:
					vacuumProgressVisible(t, s, ids[1], b.PG)
				default:
					t.Fatal("unexpected worker", s)
				}
				bag[s.pid] = s
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			return bag
		}()
		vacuumProgressUnfinished(t, a, c)
		admitted := len(seen) == 2 && (b.PG || vacuumProgressNativeStack(a.workerStack, c.workerStack))
		vacuumProgressUnfinished(t, a, c)
		if admitted {
			return
		}
		select {
		case <-a.finished:
			t.Fatal("first VACUUM completed before simultaneous admission")
		case <-c.finished:
			t.Fatal("second VACUUM completed before simultaneous admission")
		case <-ctx.Done():
			t.Fatal("no simultaneous full-row progress witness", ctx.Err(), seen)
		case <-tick.C:
		}
	}
}

func TestReferenceVacuumProgressActiveTargets(t *testing.T) {
	vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		setup, worker, observer := vacuumConn(t, p, b), vacuumConn(t, p, b), vacuumConn(t, p, b)
		for _, name := range []string{"vacuum_targets_a", "vacuum_targets_b"} {
			vacuumProgressWorkload(t, p, b, setup, name)
		}
		a := copyProgressIdentify(t, p, worker, "vacuum_targets_a")
		second := copyProgressIdentify(t, p, worker, "vacuum_targets_b")
		stmt, err := observer.PrepareContext(p.Ctx, vacuumProgressQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		r := vacuumProgressLaunch(t, p, b, worker, "vacuum_targets_a,vacuum_targets_b")
		first := vacuumProgressObserve(t, p, stmt, a, r)
		vacuumProgressVisible(t, first, a, b.PG)
		for {
			s := vacuumProgressObserve(t, p, stmt, second, r)
			if s.relid.Valid && s.relid.Int64 == second.relid {
				vacuumProgressVisible(t, s, second, b.PG)
				break
			}
			if !s.relid.Valid || s.relid.Int64 != a.relid {
				t.Fatal("not either actual target", s)
			}
			vacuumProgressVisible(t, s, a, b.PG)
		}
		vacuumProgressCancel(t, p, b, observer, a.pid, "vacuum_targets_b", r.cancel, func() error { err := <-r.done; r.joined = true; return err })
		vacuumCount(t, p.Ctx, observer, "vacuum_targets_a", 1)
		vacuumCount(t, p.Ctx, observer, "vacuum_targets_b", 0)
		vacuumProgressEmpty(t, p, b, observer)
		vacuumProgressVisible(t, first, a, b.PG)
	})
}
