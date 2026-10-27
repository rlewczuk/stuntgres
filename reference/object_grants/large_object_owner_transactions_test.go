package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceLargeObjectOwnerTransactions(t *testing.T) {
	largeObjectOwnerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer largeObjectCleanup(t, p, c, 910261)
		largeObjectOwnerCreate(run, 910261)
		fixture.RoleGrantActor(run, "lo_old", func() { run(fixture.Exec("GRANT SELECT ON LARGE OBJECT 910261 TO lo_reader", 0)) })
		old, next := largeObjectOwnerOID(t, p, c, "lo_old"), largeObjectOwnerOID(t, p, c, "lo_new")
		oldACL, newACL := "{lo_old=rw/lo_old,lo_reader=r/lo_old}", "{lo_new=rw/lo_new,lo_reader=r/lo_new}"
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer func() {
			peer.ExecContext(p.Ctx, "ROLLBACK")
			peer.ExecContext(p.Ctx, "RESET ROLE")
			peer.Close()
		}()
		peerState := func(owner int64, acl string) { fixture.RunStep(t, p.Ctx, peer, b.Recorder, largeObjectBackendStep(largeObjectMetadataStep(910261, [][]any{{int64(910261), owner, acl}}), b)) }
		peerRun := func(s fixture.Step) {
			t.Helper()
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, largeObjectBackendStep(s, b))
		}
		// Metadata alone cannot distinguish a private owner from a permission
		// consumer accidentally using committed authority. Read as actual roles
		// on both physical connections; the independent ACL grantee still reads.
		authority := func(step func(fixture.Step), inTransaction bool, owner, denied string) {
			t.Helper()
			if !inTransaction { step(fixture.Exec("BEGIN", 0)) }
			for _, reader := range []string{owner, "lo_reader"} {
				largeObjectOwnerActor(step, reader, func() {
					step(largeObjectBytes(910261, "00017f80ff00"))
				})
			}
			// The savepoint precedes SET ROLE so recovery also restores the
			// administrative actor needed by subsequent ownership assertions.
			step(fixture.Exec("SAVEPOINT owner_read_denied", 0))
			step(fixture.Exec("SET ROLE "+denied, 0))
			step(largeObjectError("SELECT lo_get(910261::oid)", "42501", "permission denied for large object 910261"))
			step(fixture.Exec("ROLLBACK TO owner_read_denied", 0))
			step(fixture.Exec("RESET ROLE", 0))
			step(fixture.Exec("RELEASE SAVEPOINT owner_read_denied", 0))
			if !inTransaction { step(fixture.Exec("COMMIT", 0)) }
		}
		authority(run, false, "lo_old", "lo_new")
		authority(peerRun, false, "lo_old", "lo_new")
		q := largeObjectOwnerAlter(910261, "lo_new")
		held := largeObjectOwnerHeld(t, p, b, c, q)
		run(fixture.Exec("BEGIN", 0))
		held(fixture.Exec(q, 0))
		largeObjectOwnerMetadata(run, 910261, next, newACL)
		peerState(old, oldACL)
		authority(run, true, "lo_new", "lo_old")
		authority(peerRun, false, "lo_old", "lo_new")
		run(fixture.Exec("SAVEPOINT owner_save", 0))
		run(fixture.Exec(largeObjectOwnerAlter(910261, "lo_old"), 0))
		largeObjectOwnerMetadata(run, 910261, old, oldACL)
		authority(run, true, "lo_old", "lo_new")
		run(fixture.Exec("ROLLBACK TO owner_save", 0))
		largeObjectOwnerMetadata(run, 910261, next, newACL)
		authority(run, true, "lo_new", "lo_old")
		run(fixture.Exec("SAVEPOINT owner_error", 0))
		run(largeObjectOwnerError(largeObjectOwnerAlter(910261, "lo_missing"), "42704", `role "lo_missing" does not exist`))
		run(largeObjectOwnerError("SELECT 1", "25P02", "current transaction is aborted, commands ignored until end of transaction block"))
		run(fixture.Exec("ROLLBACK TO owner_error", 0))
		largeObjectOwnerMetadata(run, 910261, next, newACL)
		peerState(old, oldACL)
		authority(run, true, "lo_new", "lo_old")
		authority(peerRun, false, "lo_old", "lo_new")
		run(fixture.Exec("ROLLBACK", 0))
		largeObjectOwnerMetadata(run, 910261, old, oldACL)
		peerState(old, oldACL)
		authority(run, false, "lo_old", "lo_new")
		authority(peerRun, false, "lo_old", "lo_new")
		run(largeObjectOwnerDropError("lo_old", "owner of large object 910261"))
		// Role commands themselves may not be transaction-admitted natively;
		// prove the dependency transition after the ownership commit instead.
		run(fixture.Exec("BEGIN", 0))
		held(fixture.Exec(q, 0))
		peerState(old, oldACL)
		authority(run, true, "lo_new", "lo_old")
		authority(peerRun, false, "lo_old", "lo_new")
		run(fixture.Exec("COMMIT", 0))
		peerState(next, newACL)
		authority(run, false, "lo_new", "lo_old")
		authority(peerRun, false, "lo_new", "lo_old")
		run(largeObjectOwnerDropError("lo_new", "owner of large object 910261"))
		// Go BeginTx is a separate admission path; keep a real Tx-held handle.
		tx, err := c.BeginTx(p.Ctx, nil)
		if err != nil { t.Fatal(err) }
		defer tx.Rollback()
		back := largeObjectOwnerAlter(910261, "lo_old")
		stmt, err := tx.PrepareContext(p.Ctx, back)
		if err != nil { t.Fatal(err) }
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: tx, Statement: stmt, SQL: back}, b.Recorder, fixture.Exec(back, 0))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, largeObjectBackendStep(largeObjectMetadataStep(910261, [][]any{{int64(910261), old, oldACL}}), b))
		peerState(next, newACL)
		txRun := func(s fixture.Step) {
			t.Helper()
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, largeObjectBackendStep(s, b))
		}
		authority(txRun, true, "lo_old", "lo_new")
		authority(peerRun, false, "lo_new", "lo_old")
		if err := stmt.Close(); err != nil { t.Fatal(err) }
		if err := tx.Rollback(); err != nil { t.Fatal(err) }
		largeObjectOwnerMetadata(run, 910261, next, newACL)
		authority(run, false, "lo_new", "lo_old")
		authority(peerRun, false, "lo_new", "lo_old")
		for _, query := range []string{q, largeObjectOwnerAlter(910269, "lo_missing")} {
			run(fixture.Exec("BEGIN READ ONLY", 0))
			run(largeObjectOwnerError(query, "25006", "cannot execute ALTER LARGE OBJECT in a read-only transaction"))
			run(largeObjectOwnerError("SELECT 1", "25P02", "current transaction is aborted, commands ignored until end of transaction block"))
			run(fixture.Exec("ROLLBACK", 0))
		}
		largeObjectOwnerActor(run, "lo_actor", func() {
			run(fixture.Exec("BEGIN READ ONLY", 0))
			run(largeObjectOwnerError(q, "25006", "cannot execute ALTER LARGE OBJECT in a read-only transaction"))
			run(fixture.Exec("ROLLBACK", 0))
		})
		largeObjectOwnerMetadata(run, 910261, next, newACL)
		run(largeObjectBytes(910261, "00017f80ff00"))
		run(fixture.Exec("DROP ROLE lo_old", 0))
	})
}

func TestReferenceLargeObjectOwnerAtomicity(t *testing.T) {
	largeObjectOwnerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer largeObjectCleanup(t, p, c, 910271)
		largeObjectOwnerCreate(run, 910271)
		old := largeObjectOwnerOID(t, p, c, "lo_old")
		q := largeObjectOwnerAlter(910271, "lo_new")
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		verify := func() {
			largeObjectOwnerMetadata(run, 910271, old, nil)
			run(largeObjectBytes(910271, "00017f80ff00"))
			run(largeObjectOwnerDropError("lo_old", "owner of large object 910271"))
		}
		fixture.RoleGrantActor(run, "lo_actor", func() { run(largeObjectOwnerError(q, "42501", "must be owner of large object 910271")) })
		verify()
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec(q, 0))
		run(fixture.Exec("ROLLBACK", 0))
		verify()
		if !b.PG {
			for _, expired := range []bool{false, true} {
				var ctx context.Context
				var cancel context.CancelFunc
				want := error(context.Canceled)
				if expired {
					ctx, cancel = context.WithDeadline(p.Ctx, time.Unix(1, 0))
					want = context.DeadlineExceeded
				} else { ctx, cancel = context.WithCancel(p.Ctx); cancel() }
				for _, held := range []bool{false, true} {
					var result sql.Result
					var err error
					if held { result, err = stmt.ExecContext(ctx) } else { result, err = c.ExecContext(ctx, q) }
					if result != nil || !errors.Is(err, want) { t.Fatalf("held=%t result=%v error=%v want=%v", held, result, err, want) }
					verify()
				}
				cancel()
			}
		}
		run(fixture.Exec("DROP ROLE lo_new", 0))
		verify()
	})
}
