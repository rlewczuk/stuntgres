package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGReplicationOriginStatusRowsProgress(t *testing.T) {
	databases := []string{"status_progress_peer"}
	fixture.DatabaseReferenceCleanup(t, &databases)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			name, second := "status UTF8 é'quote", "status_second"
			originStateCleanup(t, b.DB, name, second)
			c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
			originStateRun(t, p, b, c, fixture.Exec("CREATE TABLE status_progress_sentinel(n integer)", 0))
			originStateRun(t, p, b, c, fixture.Exec("INSERT INTO status_progress_sentinel VALUES(41)", 1))
			a := originStateCreate(t, p, b, c, name)
			z := originStateCreate(t, p, b, c, second)
			originStatusRows(t, p, b, c, originStatusSelect, nil)
			stmt, err := c.PrepareContext(p.Ctx, originStatusSelect+" ORDER BY external_id")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: originStatusSelect + " ORDER BY external_id"}
			for _, lsn := range []string{"80000000/0", "FFFFFFFF/FFFFFFFF", "0/1", "0/0"} {
				originStatusAdvance(t, p, b, c, name, lsn)
				want := [][]any{{a, name, lsn, "0/0"}}
				originStatusRows(t, p, b, c, held.SQL, want)
				fixture.OidReferenceRows(t, p.Ctx, b, held, fixture.Select(held.SQL, originStatusColumns, originStatusTypes, want))
			}
			originStateProgress(t, p, b, c, name, nil)
			originStatusAdvance(t, p, b, c, second, "0/22")
			want := [][]any{{a, name, "0/0", "0/0"}, {z, second, "0/22", "0/0"}}
			originStatusRows(t, p, b, c, held.SQL, want)
			originSessionSetup(t, p, b, c, name)
			originStateRun(t, p, b, c, fixture.Exec("SELECT pg_replication_origin_session_reset()", 1))
			originStatusRows(t, p, b, c, held.SQL, want)
			originStateRun(t, p, b, c, fixture.Exec("CREATE DATABASE status_progress_peer", 0))
			peer, _, recorder := fixture.DatabaseScenarioPool(t, p, b.PG, "status_progress_peer", "")
			pb := b
			pb.DB, pb.Recorder = peer, recorder
			originStatusRows(t, p, pb, peer, held.SQL, want)
			originStateRun(t, p, b, c, originStateScalar("SELECT n AS v FROM status_progress_sentinel", "INT4", int64(41)))
			if !b.PG {
				connector, err := stuntgres.NewDriver().OpenConnector("status-isolated")
				if err != nil {
					t.Fatal(err)
				}
				isolated := sql.OpenDB(connector)
				defer isolated.Close()
				var count int
				if err := isolated.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_replication_origin_status").Scan(&count); err != nil || count != 0 {
					t.Fatal(count, err)
				}
			}
		})
	}
}

func TestReferencePGReplicationOriginStatusTransactions(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			originStateCleanup(t, b.DB, "status_tx", "status_private", "status_reused")
			actor, observer := fixture.LockConnections(t, p.Ctx, b.DB)
			reader := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
			var private int64
			originStatusOrphanCleanup(t, actor, observer, &private)
			id := originStateCreate(t, p, b, observer, "status_tx")
			originStatusAdvance(t, p, b, observer, "status_tx", "0/1")
			originStateRun(t, p, b, actor, fixture.Exec("BEGIN", 0))
			originStatusAdvance(t, p, b, actor, "status_tx", "0/3")
			originStatusRows(t, p, b, observer, originStatusSelect, [][]any{{id, "status_tx", "0/3", "0/0"}})
			originStateRun(t, p, b, actor, fixture.Exec("ROLLBACK", 0))
			originStatusRows(t, p, b, observer, originStatusSelect, [][]any{{id, "status_tx", "0/3", "0/0"}})
			originStateRun(t, p, b, actor, fixture.Exec("BEGIN", 0))
			originStateRun(t, p, b, actor, fixture.Exec("SELECT pg_replication_origin_drop('status_tx')", 1))
			originStatusRows(t, p, b, observer, originStatusSelect, nil)
			originStateRun(t, p, b, actor, fixture.Exec("ROLLBACK", 0))
			if originStateID(t, p, observer, "status_tx") != id {
				t.Fatal("rollback lost registration")
			}
			originStatusRows(t, p, b, observer, originStatusSelect, nil)
			originStateRun(t, p, b, actor, fixture.Exec("BEGIN", 0))
			private = originStateCreate(t, p, b, actor, "status_private")
			originStatusAdvance(t, p, b, actor, "status_private", "80000000/4")
			q := originStatusSelect + " WHERE local_id=$1::oid"
			originStatusRows(t, p, b, actor, q, [][]any{{private, "status_private", "80000000/4", "0/0"}}, private)
			originStatusRows(t, p, b, observer, q, [][]any{{private, nil, "80000000/4", "0/0"}}, private)
			originStateRun(t, p, b, actor, fixture.Exec("ROLLBACK", 0))
			originStatusRows(t, p, b, observer, q, [][]any{{private, nil, "80000000/4", "0/0"}}, private)
			old, err := reader.QueryContext(p.Ctx, q, private)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			originStatusMetadata(t, old, b.PG)
			// The existing first-free allocator releases rolled-back registrations.
			reused := originStateCreate(t, p, b, observer, "status_reused")
			if reused != private {
				t.Fatal("first-free identity was not reused", reused, private)
			}
			originStatusRows(t, p, b, observer, q, [][]any{{reused, "status_reused", "80000000/4", "0/0"}}, reused)
			if !old.Next() {
				t.Fatal(old.Err())
			}
			var oldID int64
			var oldName sql.NullString
			var remote, local string
			if err := old.Scan(&oldID, &oldName, &remote, &local); err != nil || oldID != private || oldName.Valid || remote != "80000000/4" || local != "0/0" {
				t.Fatal(oldID, oldName, remote, local, err)
			}
			if old.Next() || old.Err() != nil {
				t.Fatal(old.Err())
			}
			old.Close()
		})
	}
}

func TestReferencePGReplicationOriginStatusSessionProgress(t *testing.T) {
	databases := []string{"status_session_peer"}
	fixture.DatabaseReferenceCleanup(t, &databases)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			originStateCleanup(t, b.DB, "status_session")
			id := originStateCreate(t, p, b, b.DB, "status_session")
			originStateRun(t, p, b, b.DB, fixture.Exec("CREATE DATABASE status_session_peer", 0))
			pool, _, recorder := fixture.DatabaseScenarioPool(t, p, b.PG, "status_session_peer", "")
			pb := b
			pb.DB, pb.Recorder = pool, recorder
			pool.SetMaxOpenConns(1)
			pool.SetMaxIdleConns(1)
			c, err := pool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
				defer cancel()
				c.ExecContext(ctx, "SELECT pg_replication_origin_session_reset()")
			}()
			originStateRun(t, p, pb, c, fixture.Exec("CREATE TABLE status_session_write(n integer)", 0))
			originSessionSetup(t, p, pb, c, "status_session")
			originStatusRows(t, p, b, b.DB, originStatusSelect, [][]any{{id, "status_session", "0/0", "0/0"}})
			originSessionXact(t, p, pb, c, "0/90")
			originStatusRows(t, p, b, b.DB, originStatusSelect, [][]any{{id, "status_session", "0/0", "0/0"}})
			var walBefore string
			if b.PG {
				if err := b.DB.QueryRowContext(p.Ctx, "SELECT pg_current_wal_insert_lsn()::text").Scan(&walBefore); err != nil {
					t.Fatal(err)
				}
			}
			originStateRun(t, p, pb, c, fixture.Exec("INSERT INTO status_session_write VALUES(1)", 1))
			// PostgreSQL has a genuine physical commit position. Native has no
			// local WAL assignment: neither backend's expected value comes from
			// the other backend, an XID, or the remote address.
			q := "SELECT remote_lsn='0/90'::pg_lsn AS remote,local_lsn> '0/0'::pg_lsn AS assigned,local_lsn<>remote_lsn AS distinct FROM pg_replication_origin_status WHERE external_id='status_session'"
			assigned, distinct := b.PG, true
			originStateRun(t, p, b, b.DB, fixture.Select(q, []string{"remote", "assigned", "distinct"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, assigned, distinct}}))
			originStateRun(t, p, b, b.DB, fixture.Prepared(fixture.Select(q, []string{"remote", "assigned", "distinct"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, assigned, distinct}})))
			if b.PG {
				bounds := originStateScalar("SELECT local_lsn>=$1::pg_lsn AND local_lsn<=pg_current_wal_insert_lsn() AS v FROM pg_replication_origin_status WHERE external_id='status_session'", "BOOL", true, walBefore)
				originStateRun(t, p, b, b.DB, bounds)
				originStateRun(t, p, b, b.DB, fixture.Prepared(bounds))
			}
			originSessionXact(t, p, pb, c, "0/10")
			originStateRun(t, p, pb, c, fixture.Exec("INSERT INTO status_session_write VALUES(2)", 1))
			originStateRun(t, p, b, b.DB, originStateScalar("SELECT remote_lsn AS v FROM pg_replication_origin_status WHERE external_id='status_session'", "PG_LSN", "0/90"))
			originStateRun(t, p, pb, c, fixture.Exec("SELECT pg_replication_origin_session_reset()", 1))
			originStatusAdvance(t, p, b, b.DB, "status_session", "0/2")
			originStateRun(t, p, b, b.DB, originStateScalar("SELECT remote_lsn AS v FROM pg_replication_origin_status WHERE external_id='status_session'", "PG_LSN", "0/2"))
			originSessionSetup(t, p, pb, c, "status_session")
			// Returning a borrower does not close the physical owner. Closing its
			// pool does; neither operation removes the allocated progress slot.
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			if err := pool.Close(); err != nil {
				t.Fatal(err)
			}
			originStateRun(t, p, b, b.DB, originStateScalar("SELECT remote_lsn AS v FROM pg_replication_origin_status WHERE external_id='status_session'", "PG_LSN", "0/2"))
			observer := originSessionConn(t, p, b.DB)
			originSessionSetup(t, p, b, observer, "status_session")
			originStateRun(t, p, b, observer, fixture.Exec("SELECT pg_replication_origin_session_reset()", 1))
		})
	}
}

// A rolled-back registration can leave a real numeric progress slot. Cleanup
// must reclaim that exact ID through ordinary first-free registration, even if
// an assertion fails before the scenario's normal recreate/drop path.
func originStatusOrphanCleanup(t *testing.T, actor, admin *sql.Conn, id *int64) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
		defer cancel()
		if _, err := actor.ExecContext(ctx, "ROLLBACK"); err != nil {
			t.Error(err)
			return
		}
		if *id == 0 {
			return
		}
		var allocated []string
		defer func() {
			for _, name := range allocated {
				if _, err := admin.ExecContext(ctx, "SELECT pg_replication_origin_drop($1::text)", name); err != nil {
					t.Error(err)
				}
			}
		}()
		var name string
		err := admin.QueryRowContext(ctx, "SELECT roname FROM pg_replication_origin WHERE roident=$1::oid", *id).Scan(&name)
		if err == nil {
			if name != "status_private" && name != "status_reused" {
				t.Errorf("orphan cleanup ID %d belongs to unrelated origin %q", *id, name)
				return
			}
			allocated = append(allocated, name)
			return
		}
		if err != sql.ErrNoRows {
			t.Error(err)
			return
		}
		for i := range *id {
			name := fmt.Sprintf("status_orphan_cleanup_%d", i)
			var created int64
			if err := admin.QueryRowContext(ctx, "SELECT pg_replication_origin_create($1::text)", name).Scan(&created); err != nil {
				t.Error(err)
				return
			}
			allocated = append(allocated, name)
			if created == *id {
				return
			}
			if created > *id {
				t.Errorf("first-free allocation passed orphan ID %d: got %d", *id, created)
				return
			}
		}
		t.Errorf("could not reclaim orphan progress ID %d", *id)
	})
}
