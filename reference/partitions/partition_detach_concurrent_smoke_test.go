package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionDetachConcurrentFeatureSmoke(t *testing.T) {
	detachConcurrentScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		detachConcurrentSetup(run)
		c := detachConcurrentConn(t, p, b)
		pid := detachConcurrentPID(t, p, b, c)
		tx, err := c.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { tx.Rollback() })
		both := fixture.Select("SELECT k,payload FROM dc_parent ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "keep"}, {int64(2), "leave"}})
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, both)
		w := detachConcurrentStart(t, p, b, "ALTER TABLE dc_parent DETACH PARTITION dc_leave CONCURRENTLY", true)
		fixture.PartitionDetachPending(t, p, b, "dc_parent", "dc_leave", w.pid, pid, w.finished)
		run(fixture.Select("SELECT k,payload FROM dc_parent ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "keep"}}))
		run(fixture.Select("SELECT k,payload FROM dc_leave", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "leave"}}))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, both)
		detachConcurrentState(run, true, true)
		detachConcurrentCancel(t, p, b, w)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		run(fixture.ImplicitError("ALTER TABLE dc_parent DETACH PARTITION dc_keep FINALIZE", fixture.Diagnostic{Code: "55000", Message: `cannot complete detaching partition "dc_keep"`, Detail: "There's no pending concurrent detach."}))
		detachConcurrentState(run, true, true)
		final, err := b.DB.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { final.Rollback() })
		detachConcurrentExecute(t, p, b, final, "ALTER TABLE dc_parent DETACH PARTITION dc_leave FINALIZE", true)
		fixture.RunStep(t, p.Ctx, final, b.Recorder, detachConcurrentGraph("dc_parent", "dc_leave", false, false))
		run(detachConcurrentGraph("dc_parent", "dc_leave", true, true))
		if err := final.Rollback(); err != nil {
			t.Fatal(err)
		}
		detachConcurrentState(run, true, true)
		detachConcurrentExecute(t, p, b, b.DB, "ALTER TABLE dc_parent DETACH PARTITION dc_leave FINALIZE", true)
		detachConcurrentState(run, false, false)
		run(fixture.ImplicitError("INSERT INTO dc_leave VALUES(9,'outside')", fixture.Diagnostic{Code: "23514", Message: `new row for relation "dc_leave" violates check constraint "dc_leave_k_check"`, Detail: "Failing row contains (9, outside).", Schema: "public", Table: "dc_leave", Constraint: "dc_leave_k_check"}))
		if !b.PG {
			var stream bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &stream); err != nil {
				t.Fatal(err)
			}
			fresh := fixture.NewFixture(t, false, true)
			if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(stream.Bytes())); err != nil {
				t.Fatal(err)
			}
			restored := func(s fixture.Step) { fixture.RunStep(t, fresh.Ctx, fresh.Memory, fresh.MemoryRecord, s) }
			detachConcurrentState(restored, false, false)
			restored(detachConcurrentRows("dc_parent", 1))
			restored(fixture.ImplicitError("INSERT INTO dc_leave VALUES(9,'outside')", fixture.Diagnostic{Code: "23514", Message: `new row for relation "dc_leave" violates check constraint "dc_leave_k_check"`, Detail: "Failing row contains (9, outside).", Schema: "public", Table: "dc_leave", Constraint: "dc_leave_k_check"}))
		}
		run(fixture.Exec("DROP TABLE dc_parent", 0))
		run(detachConcurrentRows("dc_leave", 2))
	})
}
