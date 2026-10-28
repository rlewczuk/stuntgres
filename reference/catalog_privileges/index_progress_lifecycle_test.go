package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceCreateIndexProgressLongBuild(t *testing.T)    { indexProgressLifetime(t, false) }
func TestReferenceCreateIndexProgressFeatureSmoke(t *testing.T) { indexProgressLifetime(t, true) }

func indexProgressLifetime(t *testing.T, smoke bool) {
	t.Helper()
	roles := []string{"index_progress_stranger"}
	fixture.RoleReferenceCleanup(t, roles)
	copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		observer, worker := vacuumConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
		holders := []*sql.Conn{fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)}
		copyProgressExec(t, p.Ctx, observer, "CREATE TABLE index_progress_work(n INT)")
		copyProgressExec(t, p.Ctx, observer, "INSERT INTO index_progress_work VALUES(3),(1),(1),(NULL)")
		id := copyProgressIdentify(t, p, worker, "index_progress_work")
		var holderIDs [2]int64
		for i, c := range holders {
			holderIDs[i] = copyProgressIdentify(t, p, c, "").pid
			copyProgressExec(t, p.Ctx, c, "BEGIN")
			copyProgressExec(t, p.Ctx, c, "LOCK TABLE index_progress_work IN ROW EXCLUSIVE MODE")
		}
		run := fixture.ConcurrentIndexStart(t, p, worker, "CREATE INDEX CONCURRENTLY index_progress_cancel ON index_progress_work(n)", false)
		first := indexProgressAwait(t, p, observer, id.pid, "waiting for writers before build", 0)
		indexProgressVisible(t, first, id, "CREATE INDEX CONCURRENTLY")
		select {
		case err := <-run.Done:
			t.Fatalf("worker ended before active observation: %v", err)
		default:
		}
		if first.counters[0].Int64 != 2 {
			t.Fatalf("frozen writer total: %+v", first)
		}
		for _, n := range first.counters[3:] {
			if n.Int64 != 0 {
				t.Fatalf("prebuild unknown/inapplicable must be authorized zero: %+v", first)
			}
		}
		var indexOID int64
		if err := observer.QueryRowContext(p.Ctx, "SELECT 'index_progress_cancel'::regclass::oid::bigint").Scan(&indexOID); err != nil {
			t.Fatal(err)
		}
		if first.index.Int64 != indexOID {
			t.Fatalf("published concurrent index identity: %+v oid=%d", first, indexOID)
		}
		if smoke {
			copyProgressExec(t, p.Ctx, observer, "CREATE ROLE index_progress_stranger LOGIN PASSWORD 'role-core-fixture'")
			stranger := copyProgressConn(t, p, fixture.CopyScenarioPool(t, p, b.PG, "", "index_progress_stranger"))
			s, ok := indexProgressRead(t, p, stranger, id.pid)
			if !ok {
				t.Fatal("restricted observer lost worker")
			}
			indexProgressMasked(t, s, id)
		}
		current := -1
		for i, pid := range holderIDs {
			if pid == first.counters[2].Int64 {
				current = i
			}
		}
		if current < 0 {
			t.Fatalf("current locker not an actual holder: %+v holders=%v", first, holderIDs)
		}
		copyProgressExec(t, p.Ctx, holders[current], "COMMIT")
		second := indexProgressAwait(t, p, observer, id.pid, "waiting for writers before build", 1, holderIDs[1-current])
		indexProgressVisible(t, second, id, "CREATE INDEX CONCURRENTLY")
		if second.counters[0].Int64 != 2 || second.counters[2].Int64 != holderIDs[1-current] {
			t.Fatalf("completed actual wait accounting: first=%+v second=%+v", first, second)
		}
		select {
		case err := <-run.Done:
			t.Fatalf("worker ended before cancellation: %v", err)
		default:
		}
		indexProgressCancel(t, p, b, observer, id.pid, run.Cancel, func() error { return run.Join(t, p) })
		state, found := fixture.ConcurrentIndexState(t, p, b, "index_progress_cancel")
		if !found || state.Ready || state.Valid || !state.Live {
			t.Fatalf("admitted concurrent cancellation must retain invalid index: %+v found=%v", state, found)
		}
		copyProgressExec(t, p.Ctx, holders[1-current], "ROLLBACK")
		copyProgressExec(t, p.Ctx, observer, "DROP INDEX index_progress_cancel")
		copyProgressExec(t, p.Ctx, holders[0], "BEGIN")
		copyProgressExec(t, p.Ctx, holders[0], "LOCK TABLE index_progress_work IN ROW EXCLUSIVE MODE")
		success := fixture.ConcurrentIndexStart(t, p, worker, "CREATE INDEX CONCURRENTLY index_progress_success ON index_progress_work(n)", true)
		s := indexProgressAwait(t, p, observer, id.pid, "waiting for writers before build", 0)
		indexProgressVisible(t, s, id, "CREATE INDEX CONCURRENTLY")
		copyProgressExec(t, p.Ctx, holders[0], "COMMIT")
		if err := success.Join(t, p); err != nil {
			t.Fatal(err)
		}
		indexProgressIdle(t, p, observer, id.pid)
		fixture.ConcurrentIndexWait(t, p, b, "index_progress_success", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
		vacuumRun(t, p, b, observer, fixture.Select("SELECT n FROM index_progress_work ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(3)}, {nil}}))
		copyProgressExec(t, p.Ctx, worker, "CREATE INDEX index_progress_ordinary ON index_progress_work(n)")
		indexProgressIdle(t, p, observer, id.pid)
	})
}

func TestReferenceCreateIndexProgressAdmission(t *testing.T) {
	copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		observer, holder, worker := vacuumConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
		copyProgressExec(t, p.Ctx, observer, "CREATE TABLE index_progress_admission(n INT)")
		copyProgressExec(t, p.Ctx, observer, "INSERT INTO index_progress_admission VALUES(1)")
		copyProgressExec(t, p.Ctx, holder, "BEGIN")
		copyProgressExec(t, p.Ctx, holder, "LOCK TABLE index_progress_admission IN ROW EXCLUSIVE MODE")
		id := copyProgressIdentify(t, p, worker, "index_progress_admission")
		holderID := copyProgressIdentify(t, p, holder, "")
		run := fixture.ConcurrentIndexStart(t, p, worker, "CREATE INDEX index_progress_admission_ix ON index_progress_admission(n)", true)
		if b.PG {
			fixture.LockWaitBehind(t, p.Ctx, int(id.pid), int(holderID.pid), fixture.LockOID(t, p.Ctx, "index_progress_admission"), "ShareLock")
		}
		indexProgressIdle(t, p, observer, id.pid)
		indexProgressCancel(t, p, b, observer, id.pid, run.Cancel, func() error { return run.Join(t, p) })
		if _, found := fixture.ConcurrentIndexState(t, p, b, "index_progress_admission_ix"); found {
			t.Fatal("initial admission cancellation published index")
		}
		copyProgressExec(t, p.Ctx, holder, "ROLLBACK")
		copyProgressExec(t, p.Ctx, worker, "CREATE INDEX index_progress_admission_ix ON index_progress_admission(n)")
		indexProgressIdle(t, p, observer, id.pid)
	})
}

func TestReferenceCreateIndexProgressConcurrentPhases(t *testing.T) {
	copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		observer, a, d, worker := vacuumConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
		copyProgressExec(t, p.Ctx, observer, "CREATE TABLE index_progress_phases(n INT)")
		copyProgressExec(t, p.Ctx, observer, "INSERT INTO index_progress_phases VALUES(1),(2)")
		id := copyProgressIdentify(t, p, worker, "index_progress_phases")
		copyProgressExec(t, p.Ctx, a, "BEGIN")
		copyProgressExec(t, p.Ctx, a, "LOCK TABLE index_progress_phases IN ROW EXCLUSIVE MODE")
		run := fixture.ConcurrentIndexStart(t, p, worker, "CREATE INDEX CONCURRENTLY index_progress_phases_ix ON index_progress_phases(n)", true)
		indexProgressAwait(t, p, observer, id.pid, "waiting for writers before build", 0)
		copyProgressExec(t, p.Ctx, d, "BEGIN")
		copyProgressExec(t, p.Ctx, d, "LOCK TABLE index_progress_phases IN ROW EXCLUSIVE MODE")
		did := copyProgressIdentify(t, p, d, "")
		copyProgressExec(t, p.Ctx, a, "COMMIT")
		s := indexProgressAwait(t, p, observer, id.pid, "waiting for writers before validation", 0)
		indexProgressVisible(t, s, id, "CREATE INDEX CONCURRENTLY")
		if s.counters[0].Int64 != 1 || s.counters[2].Int64 != did.pid || s.counters[5].Int64 != 2 {
			t.Fatalf("actual second writer cohort / exact build tuple total: %+v", s)
		}
		copyProgressExec(t, p.Ctx, d, "COMMIT")
		if err := run.Join(t, p); err != nil {
			t.Fatal(err)
		}
		indexProgressIdle(t, p, observer, id.pid)
	})
}
