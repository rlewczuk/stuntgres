package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWRowLockPersistence(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fdwRowLockModeName(prepared), func(t *testing.T) {
			fdwRowLockPersistence(t, prepared)
		})
	}
}

func fdwRowLockPersistence(t *testing.T, prepared bool) {
	fdwRowLockConfiguredScenario(t, false, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, locker *fdwSQLRowLocker, run func(fixture.Step)) {
		run(fixture.Exec("CREATE VIEW fdw_persist_mark AS SELECT id,label,flag FROM fdw_data WHERE id=1 FOR UPDATE", 0))
		run(fixture.Exec("BEGIN", 0))
		defer fdwRowLockRollback(t, c)
		fdwRowLockClose(t, fdwRowLockRead(t, p.Ctx, c, "SELECT id,label,flag FROM fdw_persist_mark", prepared, [][]any{{int64(1), "one", true}}))
		var dump bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
		}
		// Dump is not transaction completion. The original participant
		// retains its actual source lock until its owner rolls back.
		fdwRowLockDeleteWitness(t, p, b, c, source, run)
		if b.PG {
			return
		} // Dump/Restore are intentionally native public controls.
		fixture.LockExec(t, p.Ctx, source, "INSERT INTO fdw_source.data VALUES(1,777,1,1,'one',true)", 1)
		d := stuntgres.NewDriver()
		if err := locker.fdwSQLReader.configure(d); err != nil {
			t.Fatal(err)
		}
		connector, err := d.OpenConnector("fdw-row-lock-restored")
		if err != nil {
			t.Fatal(err)
		}
		control := sql.OpenDB(connector)
		defer control.Close()
		if err := stuntgres.Restore(p.Ctx, control, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		db := fdwRecordedPool(t, connector)
		defer db.Close()
		dest, err := db.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer dest.Close()
		defer fdwRowLockRollback(t, dest)
		recorder := &fixture.Recorder{RawBackend: fixture.RawNative}
		destRun := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, dest, recorder, step) }
		destRun(fixture.Select("SELECT option_value FROM information_schema.foreign_table_options WHERE foreign_table_name='fdw_data' ORDER BY option_name", []string{"option_value"}, []string{"VARCHAR"}, [][]any{{"fdw_source"}, {"data"}}))
		destRun(fixture.Exec("BEGIN", 0))
		// The source generation differs from the dumped execution. Only
		// fresh identities from a new real transaction can lock this row.
		fdwRowLockClose(t, fdwRowLockRead(t, p.Ctx, dest, "SELECT id,label,flag FROM fdw_persist_mark", prepared, [][]any{{int64(1), "one", true}}))
		fdwRowLockDeleteWitness(t, p, b, dest, source, destRun)
	})
}

func fdwRowLockDeleteWitness(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, run func(fixture.Step)) {
	t.Helper()
	peer, _ := fixture.LockConnections(t, p.Ctx, source)
	waiter := 0
	if b.PG {
		waiter = fixture.LockPID(t, p.Ctx, peer)
	}
	finished := make(chan struct{})
	join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
		defer close(finished)
		return fixture.RowExecute(ctx, peer, "DELETE FROM fdw_source.data WHERE rid=1", false)
	})
	if b.PG {
		fdwRowLockRemoteWait(t, p.Ctx, 0, waiter, 0)
	} else {
		fixture.NativeDeleteRowWait(t, p.Ctx, finished)
	}
	run(fixture.Exec("ROLLBACK", 0))
	o := join()
	if o.Err != nil {
		t.Fatal(o.Err)
	}
	fixture.LockAffected(t, o.Result, 1)
}
