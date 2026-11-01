package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceVacuumProgressActiveWorkers(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		route := "direct"
		if prepared {
			route = "prepared"
		}
		t.Run(route, func(t *testing.T) {
			vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				setup, observer := vacuumConn(t, p, b), vacuumConn(t, p, b)
				workers := []*sql.Conn{vacuumConn(t, p, b), vacuumConn(t, p, b)}
				names := []string{"vacuum_workers_a", "vacuum_workers_b"}
				var identities []copyProgressIdentity
				var controls []vacuumProgressLaunchControl
				var blockers []*vacuumProgressIndexBlocker
				for i, name := range names {
					vacuumProgressWorkload(t, p, b, setup, name)
					identities = append(identities, copyProgressIdentify(t, p, workers[i], name))
					control := vacuumProgressLaunchControl{}
					if prepared {
						control.statement = vacuumHeld(t, p, workers[i], "VACUUM (PROCESS_TOAST false) "+name).Statement
					}
					if !b.PG {
						control.pause = stuntgres.NewExecutionPause("vacuum.inspect")
					}
					controls = append(controls, control)
				}
				held := vacuumHeld(t, p, observer, vacuumProgressQuery).Statement
				bag := vacuumHeld(t, p, observer, "SELECT * FROM pg_catalog.pg_stat_progress_vacuum WHERE pid IN ($1,$2)").Statement
				vacuumProgressEmpty(t, p, b, observer)
				if b.PG {
					for _, name := range names {
						blockers = append(blockers, vacuumProgressBlockIndexes(t, p, b, observer, name))
					}
					if blockers[0].pid == blockers[1].pid {
						t.Fatal("blockers must be independently owned")
					}
					for _, blocker := range blockers {
						blocker.acquire(t, p, b)
					}
				}
				a := vacuumProgressLaunch(t, p, b, workers[0], names[0], controls[0])
				c := vacuumProgressLaunch(t, p, b, workers[1], names[1], controls[1])
				running := []*vacuumProgressRunning{a, c}
				for i, r := range running {
					if b.PG {
						blockers[i].admitted(t, p, observer, identities[i], r)
					} else {
						vacuumProgressHeld(t, p, controls[i].pause, r)
					}
				}
				retained := vacuumProgressOverlap(t, p, b, bag, identities, a, c)
				original := [2]vacuumProgressSnapshot{retained[identities[0].pid], retained[identities[1].pid]}
				for range 3 {
					vacuumProgressFreshWorker(t, p, b, observer, held, identities[1], c)
				}
				join := func(r *vacuumProgressRunning) func() error {
					return func() error { err := <-r.done; r.joined = true; return err }
				}
				vacuumProgressCancel(t, p, b, observer, identities[0].pid, names[0], a.cancel, join(a), b.PG)
				vacuumProgressNotCompleted(t, p, b, observer, names[0])
				vacuumProgressFreshWorker(t, p, b, observer, held, identities[1], c)
				if b.PG {
					blockers[1].admitted(t, p, observer, identities[1], c)
				}
				vacuumProgressRetained(t, retained, identities, original)
				vacuumProgressCancel(t, p, b, observer, identities[1].pid, names[1], c.cancel, join(c), b.PG)
				vacuumProgressEmpty(t, p, b, observer)
				for i, name := range names {
					if b.PG {
						blockers[i].unchanged(t, p)
						blockers[i].release(t)
						blockers[i].unchangedAfterRelease(t, p, observer)
					}
					vacuumProgressNotCompleted(t, p, b, observer, name)
					vacuumProgressSurvivors(t, p, b, observer, name)
				}
				vacuumProgressRetained(t, retained, identities, original)
			})
		})
	}
}

// Each condition attempt owns one complete SQL bag and one complete native
// capture. Neither SQL rows nor storage admission accumulate across attempts.
func vacuumProgressOverlap(t *testing.T, p *fixture.Pair, b fixture.Backend, stmt *sql.Stmt, ids []copyProgressIdentity, a, c *vacuumProgressRunning) map[int64]vacuumProgressSnapshot {
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
			vacuumProgressTypes(t, rows)
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
				if b.PG {
					vacuumProgressIndexOpen(t, s)
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
			return seen
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

func vacuumProgressRetained(t *testing.T, retained map[int64]vacuumProgressSnapshot, ids []copyProgressIdentity, original [2]vacuumProgressSnapshot) {
	t.Helper()
	for i, id := range ids {
		if !reflect.DeepEqual(retained[id.pid], original[i]) {
			t.Fatalf("retained progress changed: got %+v want %+v", retained[id.pid], original[i])
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
		t.Cleanup(func() { stmt.Close() })
		r := vacuumProgressLaunch(t, p, b, worker, "vacuum_targets_a,vacuum_targets_b")
		first := vacuumProgressObserve(t, p, stmt, a, r)
		vacuumProgressVisible(t, first, a, b.PG)
		vacuumProgressObserveTarget(t, p, stmt, a, second, r)
		vacuumProgressCancel(t, p, b, observer, a.pid, "vacuum_targets_b", r.cancel, func() error { err := <-r.done; r.joined = true; return err })
		vacuumCount(t, p.Ctx, observer, "vacuum_targets_a", 1)
		vacuumCount(t, p.Ctx, observer, "vacuum_targets_b", 0)
		vacuumProgressEmpty(t, p, b, observer)
		vacuumProgressVisible(t, first, a, b.PG)
	})
}

// The first target already has a SQL/storage witness. Validate its intervening
// rows without repeatedly capturing every goroutine while awaiting the second.
func vacuumProgressObserveTarget(t *testing.T, p *fixture.Pair, stmt *sql.Stmt, first, second copyProgressIdentity, r *vacuumProgressRunning) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 30*time.Second)
	defer cancel()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		vacuumProgressUnfinished(t, r)
		s, ok := vacuumProgressScan(t, stmt.QueryRowContext(ctx, second.pid))
		vacuumProgressUnfinished(t, r)
		if ok {
			switch {
			case s.relid.Valid && s.relid.Int64 == first.relid:
				vacuumProgressVisible(t, s, first, r.pg)
			case s.relid.Valid && s.relid.Int64 == second.relid:
				vacuumProgressVisible(t, s, second, r.pg)
				admitted := r.pg || vacuumProgressNativeStack(r.workerStack)
				vacuumProgressUnfinished(t, r)
				if admitted {
					return
				}
			default:
				t.Fatal("not either actual target", s)
			}
		}
		select {
		case <-r.finished:
			t.Fatal("VACUUM completed without an observed second-target progress row")
		case <-ctx.Done():
			t.Fatal("no active second-target SQL progress witness", ctx.Err())
		case <-tick.C:
		}
	}
}
