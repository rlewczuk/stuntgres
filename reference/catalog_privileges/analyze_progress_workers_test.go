package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceAnalyzeProgressWorkers(t *testing.T) {
	var databases []string
	fixture.DatabaseReferenceCleanup(t, &databases)
	analyzeProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		admin := vacuumConn(t, p, b)
		databases = append(databases, "analyze_progress_peer")
		copyProgressExec(t, p.Ctx, admin, "CREATE DATABASE analyze_progress_peer")
		pool, _, _ := fixture.DatabaseScenarioPool(t, p, b.PG, "analyze_progress_peer", "")
		peer := b
		peer.DB = pool
		peerAdmin := copyProgressConn(t, p, pool)
		analyzeProgressSetup(t, p, admin, "analyze_workers")
		analyzeProgressSetup(t, p, peerAdmin, "analyze_workers")
		workers := []*sql.Conn{fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, peer)}
		a, ha, ra := analyzeProgressHold(t, p, b, workers[0], "analyze_workers", false)
		bb, hb, rb := analyzeProgressHold(t, p, peer, workers[1], "analyze_workers", true)
		if a.pid == bb.pid || a.datid == bb.datid || a.database == bb.database {
			t.Fatalf("cross-database workers not distinct: %+v %+v", a, bb)
		}
		held := vacuumHeld(t, p, admin, "SELECT * FROM pg_catalog.pg_stat_progress_analyze WHERE pid=$1 OR pid=$2")
		assertBag := func(x fixture.Executor) {
			ra.live(t)
			rb.live(t)
			rows, err := x.QueryContext(p.Ctx, held.SQL, a.pid, bb.pid)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			if names, err := rows.Columns(); err != nil || !reflect.DeepEqual(names, analyzeProgressNames) {
				t.Fatalf("live columns %v: %v", names, err)
			}
			seen := map[int64]int{}
			for {
				s, ok := analyzeProgressScan(t, rows)
				if !ok {
					break
				}
				seen[s.pid]++
				switch s.pid {
				case a.pid:
					analyzeProgressVisible(t, s, a, b.PG)
				case bb.pid:
					analyzeProgressVisible(t, s, bb, b.PG)
				default:
					t.Fatalf("unrequested worker %+v", s)
				}
			}
			if !reflect.DeepEqual(seen, map[int64]int{a.pid: 1, bb.pid: 1}) {
				t.Fatalf("worker bag multiplicities %v", seen)
			}
			ra.live(t)
			rb.live(t)
		}
		assertBag(admin)
		assertBag(held)
		first := analyzeProgressObserve(t, p, admin, bb, rb)
		analyzeProgressCancel(t, p, b, admin, a, ra)
		copyProgressExec(t, p.Ctx, ha, "ROLLBACK")
		analyzeProgressVisible(t, analyzeProgressObserve(t, p, admin, bb, rb), bb, b.PG)
		// Execute an already held statement after A's removal: no cached membership.
		rows, err := held.QueryContext(p.Ctx, held.SQL, a.pid, bb.pid)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer rows.Close()
			s, ok := analyzeProgressScan(t, rows)
			if !ok {
				t.Fatal("held bag lost surviving worker")
			}
			analyzeProgressVisible(t, s, bb, b.PG)
			if rows.Next() {
				t.Fatal("held bag retained removed worker or duplicate")
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
		}()
		rb.live(t)
		one := vacuumHeld(t, p, admin, analyzeProgressQuery)
		analyzeProgressIdle(t, p, one, a.pid)
		analyzeProgressVisible(t, analyzeProgressObserve(t, p, one, bb, rb), bb, b.PG)
		copyProgressExec(t, p.Ctx, hb, "ROLLBACK")
		if err := rb.result(t); err != nil {
			t.Fatal(err)
		}
		analyzeProgressIdle(t, p, one, bb.pid)
		analyzeProgressVisible(t, first, bb, b.PG)
	})
}

func analyzeProgressReset(t *testing.T, p *fixture.Pair, rows *sql.Rows, id copyProgressIdentity) {
	t.Helper()
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if names, err := rows.Columns(); err != nil || !reflect.DeepEqual(names, analyzeProgressNames) {
		t.Fatalf("retained columns %v: %v", names, err)
	}
	s, ok := analyzeProgressScan(t, rows)
	if !ok {
		t.Fatal("native replacement erased detached active result")
	}
	analyzeProgressVisible(t, s, id, false)
	if rows.Next() {
		t.Fatal("duplicate retained active result")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
