package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCopyProgressCluster(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"copy_cluster_other"})
	databases := []string{"copy_progress_second"}
	fixture.DatabaseReferenceCleanup(t, &databases)
	copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		admin := vacuumConn(t, p, b)
		copyProgressExec(t, p.Ctx, admin, "CREATE ROLE copy_cluster_other LOGIN PASSWORD 'role-core-fixture'")
		copyProgressExec(t, p.Ctx, admin, "CREATE DATABASE copy_progress_second")
		first := copyProgressConn(t, p, fixture.CopyScenarioPool(t, p, b.PG, "", ""))
		second := copyProgressConn(t, p, fixture.CopyScenarioPool(t, p, b.PG, "copy_progress_second", ""))
		other := copyProgressConn(t, p, fixture.CopyScenarioPool(t, p, b.PG, "copy_progress_second", "copy_cluster_other"))
		secondObserver := copyProgressConn(t, p, fixture.CopyScenarioPool(t, p, b.PG, "copy_progress_second", ""))
		for _, c := range []*sql.Conn{first, second} {
			copyProgressExec(t, p.Ctx, c, "CREATE TABLE copy_progress_cluster(id INT)")
		}
		a := copyProgressIdentify(t, p, first, "copy_progress_cluster")
		bb := copyProgressIdentify(t, p, second, "copy_progress_cluster")
		if a.pid == bb.pid || a.datid == bb.datid || a.database == bb.database {
			t.Fatalf("workers not in distinct sessions/databases: %+v %+v", a, bb)
		}
		arun := copyProgressStart(t, p, b, first, "COPY copy_progress_cluster FROM STDIN")
		brun := copyProgressStart(t, p, b, second, "COPY copy_progress_cluster FROM STDIN")
		for _, observer := range []*sql.Conn{admin, secondObserver} {
			for _, id := range []copyProgressIdentity{a, bb} {
				s := copyProgressAwait(t, p, observer, id.pid, func(s copyProgressSnapshot, ok bool) bool { return ok })
				copyProgressVisible(t, s, id, "COPY FROM")
			}
			// Set semantics: do not compare unspecified source order or dynamic OIDs
			// between servers, and do not accidentally accept duplicate/missing PIDs.
			rows, err := observer.QueryContext(p.Ctx, "SELECT pid FROM pg_stat_progress_copy WHERE pid=$1 OR pid=$2", a.pid, bb.pid)
			if err != nil {
				t.Fatal(err)
			}
			seen := map[int64]bool{}
			for rows.Next() {
				var pid int64
				if err := rows.Scan(&pid); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				if seen[pid] {
					rows.Close()
					t.Fatal("duplicate PID", pid)
				}
				seen[pid] = true
			}
			err = rows.Err()
			rows.Close()
			if err != nil || len(seen) != 2 || !seen[a.pid] || !seen[bb.pid] {
				t.Fatalf("cluster PID set=%v err=%v", seen, err)
			}
		}
		masked, ok := copyProgressRead(t, p, other, a.pid)
		if !ok {
			t.Fatal("cross-database unrelated observer hid worker")
		}
		copyProgressMasked(t, masked, a)
		arun.finish(t, 0)
		copyProgressIdle(t, p, admin, a.pid)
		s, ok := copyProgressRead(t, p, admin, bb.pid)
		if !ok {
			t.Fatal("ending first database COPY removed second")
		}
		copyProgressVisible(t, s, bb, "COPY FROM")
		brun.finish(t, 0)
		for _, observer := range []*sql.Conn{admin, secondObserver} {
			copyProgressIdle(t, p, observer, a.pid)
			copyProgressIdle(t, p, observer, bb.pid)
		}
	})
}
