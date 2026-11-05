package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBackendStateControls(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		peer := statActivityConn(t, p, b)
		activityExec(t, p, peer, "SET application_name='bs_control_source_peer'")
		mapped := stateMapped(t, p, b, o, peer)
		id, pid := mapped[0], mapped[1].(int64)
		activityExec(t, p, peer, "/* pool retained source */ SELECT 37")
		before := stateRead(t, p, b, o, id)
		if err := peer.Close(); err != nil {
			t.Fatal(err)
		}
		peer = statActivityConn(t, p, b)
		if statActivityPID(t, p, peer) != pid {
			t.Fatal("pool return retired attachment")
		}
		activityExec(t, p, o, "CREATE DATABASE bs_cross_database")
		defer o.ExecContext(context.Background(), "DROP DATABASE IF EXISTS bs_cross_database")
		pool, _, _ := fixture.DatabaseScenarioPool(t, p, b.PG, "bs_cross_database", "")
		defer pool.Close()
		cross, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer cross.Close()
		crossID := stateMapped(t, p, b, o, cross)[0]
		activityExec(t, p, cross, "/* genuine cross database */ SELECT 38")
		statActivityClear(t, p, o)
		if stateRead(t, p, b, o, crossID)[0] != "/* genuine cross database */ SELECT 38" {
			t.Fatal("cluster activity scope")
		}
		activityExec(t, p, o, "CREATE TABLE bs_control(v integer)")
		activityExec(t, p, o, "INSERT INTO bs_control VALUES(37)")
		if backendCount(t, p, o, "SELECT count(*) FROM pg_stat_activity WHERE application_name='bs_control_source_peer' AND pid=$1", pid) != 1 {
			t.Fatal("known source peer marker missing")
		}
		if b.PG {
			return
		} // Native controls have no SQL PostgreSQL equivalent.
		held := statePrepare(t, p, o)
		activityExec(t, p, peer, "BEGIN")
		defer peer.ExecContext(context.Background(), "ROLLBACK")
		activityExec(t, p, peer, "SAVEPOINT private_child")
		activityExec(t, p, peer, "INSERT INTO bs_control VALUES(99)")
		stateSub(t, p, b, o, id, 1, false, true)
		activityExec(t, p, o, "BEGIN")
		defer o.ExecContext(context.Background(), "ROLLBACK")
		private := stateRead(t, p, b, held, id)
		retained, err := held.QueryContext(p.Ctx, stateSQL, id)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString("not a dump")); err == nil {
			t.Fatal("bad restore accepted")
		}
		got := stateRows(t, retained, false, stateNames, stateTypes)
		if len(got) != 1 {
			t.Fatal(got)
		}
		stateEqual(t, got[0], private)
		stateEqual(t, stateRead(t, p, b, held, id), private)
		activityExec(t, p, o, "ROLLBACK")
		identityEqual(t, identityMapped(t, p, b, o, peer), mapped)
		// Successful replacement clears real old child authority but preserves
		// physical slot/birth; detached Rows still own their pre-replacement data.
		for _, restore := range []bool{false, true} {
			if restore {
				activityExec(t, p, o, "CREATE TABLE bs_control(v integer)")
				activityExec(t, p, peer, "BEGIN")
				activityExec(t, p, peer, "SAVEPOINT restore_child")
				activityExec(t, p, peer, "INSERT INTO bs_control VALUES(99)")
				stateSub(t, p, b, o, id, 1, false, true)
			}
			old := stateRead(t, p, b, held, id)
			r, err := held.QueryContext(p.Ctx, stateSQL, id)
			if err != nil {
				t.Fatal(err)
			}
			if restore {
				err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes()))
			} else {
				err = stuntgres.Reset(p.Ctx, p.Native)
			}
			if err != nil {
				r.Close()
				t.Fatal(err)
			}
			rows := stateRows(t, r, false, stateNames, stateTypes)
			if len(rows) != 1 {
				t.Fatal(rows)
			}
			stateEqual(t, rows[0], old)
			stateSub(t, p, b, o, id, 0, false, true)
			activityExec(t, p, peer, "ROLLBACK")
			stateSub(t, p, b, o, id, 0, false, true)
			identityEqual(t, identityMapped(t, p, b, o, peer), mapped)
			if !restore && backendCount(t, p, o, "SELECT count(*) FROM pg_class WHERE relname='bs_control'") != 0 {
				t.Fatal("Reset retained catalog")
			}
			if restore && backendCount(t, p, o, "SELECT count(*) FROM bs_control WHERE v=37") != 1 {
				t.Fatal("committed dump lost")
			}
			if restore && backendCount(t, p, o, "SELECT count(*) FROM bs_control WHERE v=99") != 0 {
				t.Fatal("private write serialized")
			}
		}
		// A stale native Go completion cannot end a genuine successor or erase
		// its child cache. This uses the established public stale-ref boundary.
		tx, err := peer.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(p.Ctx, "COMMIT"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(p.Ctx, "BEGIN"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(p.Ctx, "SAVEPOINT successor"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(p.Ctx, "INSERT INTO bs_control VALUES(88)"); err != nil {
			t.Fatal(err)
		}
		stateSub(t, p, b, o, id, 1, false, true)
		fixture.TransactionOrdinary(t, tx.Rollback(), "stuntgres: stale transaction reference")
		stateSub(t, p, b, o, id, 1, false, true)
		activityExec(t, p, peer, "ROLLBACK")
		stateSub(t, p, b, o, id, 0, false, true)
		// Destination has genuine independent attachment and no imported peer.
		d := stuntgres.NewDriver()
		connector, err := d.OpenConnector("backend-state-independent")
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(connector)
		defer destination.Close()
		c, err := destination.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		dest := stateMapped(t, p, b, c, c)
		if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		identityEqual(t, identityMapped(t, p, b, c, c), dest)
		if backendCount(t, p, c, "SELECT count(*) FROM bs_control") != 1 {
			t.Fatal("independent committed image")
		}
		// Restore borrows a genuine pool connection while c is held; its
		// returned physical attachment may remain live in the destination.
		// Source membership, not total destination cardinality, proves isolation.
		if backendCount(t, p, c, "SELECT count(*) FROM pg_stat_activity WHERE application_name='bs_control_source_peer'") != 0 {
			t.Fatal("destination imported known source peer")
		}
		statActivityClear(t, p, o)
		if stateRead(t, p, b, held, id)[5] != "(0,f)" {
			t.Fatal("independent control touched source")
		}
		identityEqual(t, identityMapped(t, p, b, o, peer), mapped)
		if backendCount(t, p, o, "SELECT count(*) FROM pg_stat_activity WHERE application_name='bs_control_source_peer' AND pid=$1", pid) != 1 {
			t.Fatal("independent control changed known source membership")
		}
		if before[0] != "/* pool retained source */ SELECT 37" {
			t.Fatal("original source witness", before)
		}
	})
}
