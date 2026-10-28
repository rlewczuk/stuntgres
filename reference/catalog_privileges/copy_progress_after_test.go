package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCopyProgressAfterTriggers(t *testing.T) {
	copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		observer := vacuumConn(t, p, b)
		holder := vacuumConn(t, p, b)
		worker := copyProgressConn(t, p, fixture.CopyScenarioPool(t, p, b.PG, "", ""))
		for _, q := range []string{
			"CREATE TABLE copy_progress_after(id INT PRIMARY KEY)",
			"INSERT INTO copy_progress_after VALUES(99)",
			"CREATE TABLE copy_progress_after_gate(id INT PRIMARY KEY,n INT)",
			"INSERT INTO copy_progress_after_gate VALUES(1,0)",
			"CREATE TABLE copy_progress_after_log(id INT)",
			"CREATE FUNCTION copy_progress_after_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN UPDATE copy_progress_after_gate SET n=n+1 WHERE id=1; INSERT INTO copy_progress_after_log VALUES(NEW.id); RETURN NULL; END$$",
			"CREATE TRIGGER copy_progress_after_trigger AFTER INSERT ON copy_progress_after FOR EACH ROW EXECUTE FUNCTION copy_progress_after_fn()",
		} {
			copyProgressExec(t, p.Ctx, observer, q)
		}
		id := copyProgressIdentify(t, p, worker, "copy_progress_after")
		copyProgressExec(t, p.Ctx, holder, "BEGIN")
		defer holder.ExecContext(p.Ctx, "ROLLBACK")
		copyProgressExec(t, p.Ctx, holder, "SELECT id FROM copy_progress_after_gate WHERE id=1 FOR UPDATE")
		holderPID := fixture.LockPID(t, p.Ctx, holder)
		run := copyProgressStart(t, p, b, worker, "COPY copy_progress_after FROM STDIN")
		copyProgressAwait(t, p, observer, id.pid, func(_ copyProgressSnapshot, present bool) bool { return present })
		run.input.send(t, p, []byte("1\n"))
		run.input.close()
		// EOF has been delivered. The blocked row UPDATE is real queued AFTER
		// execution, not a stream-reader gate or a timing approximation.
		if b.PG {
			fixture.TransactionLockPending(t, p, int(id.pid), holderPID, run.finished)
		} else {
			fixture.NativeCopyUpdateRowWait(t, p.Ctx, run.finished)
		}
		active, present := copyProgressRead(t, p, observer, id.pid)
		if !present {
			t.Fatal("COPY disappeared during queued AFTER execution")
		}
		copyProgressVisible(t, active, id, "COPY FROM")
		if active.processed.Int64 != 1 || active.excluded.Int64 != 0 || active.skipped.Int64 != 0 || active.bytes.Int64 <= 0 || active.bytes.Int64 > 2 {
			t.Fatalf("queued AFTER progress=%+v", active)
		}
		vacuumRun(t, p, b, observer, fixture.Select("SELECT id FROM copy_progress_after ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}))
		vacuumRun(t, p, b, observer, fixture.Select("SELECT n FROM copy_progress_after_gate WHERE id=1", []string{"n"}, []string{"INT4"}, [][]any{{int64(0)}}))
		vacuumRun(t, p, b, observer, fixture.Select("SELECT id FROM copy_progress_after_log", []string{"id"}, []string{"INT4"}, nil))
		copyProgressExec(t, p.Ctx, holder, "ROLLBACK")
		run.finish(t, 1)
		copyProgressIdle(t, p, observer, id.pid)
		vacuumRun(t, p, b, observer, fixture.Select("SELECT id FROM copy_progress_after ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(99)}}))
		vacuumRun(t, p, b, observer, fixture.Select("SELECT n FROM copy_progress_after_gate WHERE id=1", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
		vacuumRun(t, p, b, observer, fixture.Select("SELECT id FROM copy_progress_after_log", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
}
