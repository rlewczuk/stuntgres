package reference_test

import (
	"database/sql"
	"testing"

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
		a := vacuumProgressLaunch(t, p, b, workers[0], "vacuum_workers_a")
		c := vacuumProgressLaunch(t, p, b, workers[1], "vacuum_workers_b")
		vacuumProgressVisible(t, vacuumProgressObserve(t, p, held[0], identities[0], a), identities[0], b.PG)
		vacuumProgressVisible(t, vacuumProgressObserve(t, p, held[1], identities[1], c), identities[1], b.PG)
		// A single SQL result is a bag containing both actual worker identities.
		rows, err := observer.QueryContext(p.Ctx, "SELECT pid,relid FROM pg_stat_progress_vacuum WHERE pid IN ($1,$2)", identities[0].pid, identities[1].pid)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[int64]int64{}
		for rows.Next() {
			var pid, relid int64
			if err := rows.Scan(&pid, &relid); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if _, exists := seen[pid]; exists {
				rows.Close()
				t.Fatal("duplicate worker", pid)
			}
			seen[pid] = relid
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(seen) != 2 {
			t.Fatal("did not observe both active workers in one SQL snapshot", seen)
		}
		for _, id := range identities {
			if seen[id.pid] != id.relid {
				t.Fatal("cross-worker identity", seen, id)
			}
		}
		vacuumProgressCancel(t, p, b, observer, identities[0].pid, "vacuum_workers_a", a.cancel, func() error { err := <-a.done; a.joined = true; return err })
		vacuumProgressVisible(t, vacuumProgressObserve(t, p, held[1], identities[1], c), identities[1], b.PG)
		vacuumProgressCancel(t, p, b, observer, identities[1].pid, "vacuum_workers_b", c.cancel, func() error { err := <-c.done; c.joined = true; return err })
		vacuumProgressEmpty(t, p, b, observer)
	})
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
		}
		vacuumProgressCancel(t, p, b, observer, a.pid, "vacuum_targets_b", r.cancel, func() error { err := <-r.done; r.joined = true; return err })
		vacuumCount(t, p.Ctx, observer, "vacuum_targets_a", 1)
		vacuumCount(t, p.Ctx, observer, "vacuum_targets_b", 0)
		vacuumProgressEmpty(t, p, b, observer)
		vacuumProgressVisible(t, first, a, b.PG)
	})
}
