package reference_test

import (
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePreparedTransactionAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"prepared_owner", "prepared_other"})
	fixture.SchemaReferenceCleanup(t, "prepared_auth")
	fixture.PreparedReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		admin, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		for _, name := range []string{"prepared_owner", "prepared_other"} {
			fixture.LockExec(t, p.Ctx, admin, "CREATE USER "+name, 0)
			if b.PG {
				fixture.LockExec(t, p.Ctx, admin, "ALTER ROLE "+name+" PASSWORD 'role-core-fixture'", 0)
			}
		}
		// Privileged schema creation gives the real login table-creation authority
		// through the existing ownership API; no native GRANT rollout is needed.
		fixture.LockExec(t, p.Ctx, admin, "CREATE SCHEMA prepared_auth AUTHORIZATION prepared_owner", 0)
		if b.PG {
			fixture.LockExec(t, p.Ctx, admin, "GRANT USAGE ON SCHEMA public TO prepared_owner,prepared_other", 0)
		}
		driver := p.NativeConnector.Driver().(*stuntgres.Driver)
		dsn := "reference/" + t.Name()[:strings.LastIndex(t.Name(), "/")]
		// The fixture's exact opaque native key, not a new database per login.
		ownerPool := fixture.RoleScenarioLoginPool(t, driver, b.PG, dsn, "prepared_owner")
		otherPool := fixture.RoleScenarioLoginPool(t, driver, b.PG, dsn, "prepared_other")
		owner, ownerPeer := fixture.LockConnections(t, p.Ctx, ownerPool)
		other := fixture.SequenceCoreConn(t, p.Ctx, otherPool)
		for _, x := range []fixture.Executor{owner, ownerPeer, other} {
			fixture.LockExec(t, p.Ctx, x, "SET search_path=public", 0)
		}
		fixture.LockExec(t, p.Ctx, owner, "CREATE TABLE prepared_auth.prepared_authority(v INT)", 0)
		for _, word := range []string{"COMMIT", "ROLLBACK"} {
			fixture.LockExec(t, p.Ctx, owner, "BEGIN", 0)
			fixture.LockExec(t, p.Ctx, owner, "INSERT INTO prepared_auth.prepared_authority VALUES(7)", 1)
			fixture.PreparedReferencePrepare(t, p, b, owner, "authority")
			_, err := other.ExecContext(p.Ctx, word+" PREPARED 'authority'")
			want := fixture.ControlsReferenceDiagnostic("42501", "permission denied to finish prepared transaction")
			want.Hint = "Must be superuser or the user that prepared the transaction."
			fixture.AssertDiagnostic(t, err, &want)
			entry := fixture.PreparedReferenceOne(t, p, peer, "authority")
			if !entry.Owner.Valid || entry.Owner.String != "prepared_owner" {
				t.Fatal(entry)
			}
			fixture.PreparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_auth.prepared_authority")
			fixture.LockExec(t, p.Ctx, ownerPeer, "ROLLBACK PREPARED 'authority'", 0)
		}
		// SAME held statement must consult the actor at execution, not Prepare.
		fixture.LockExec(t, p.Ctx, owner, "BEGIN", 0)
		fixture.PreparedReferencePrepare(t, p, b, owner, "held_authority")
		stmt, err := admin.PrepareContext(p.Ctx, "COMMIT PREPARED 'held_authority'")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		fixture.LockExec(t, p.Ctx, admin, "SET ROLE prepared_other", 0)
		_, err = stmt.ExecContext(p.Ctx)
		want := fixture.ControlsReferenceDiagnostic("42501", "permission denied to finish prepared transaction")
		want.Hint = "Must be superuser or the user that prepared the transaction."
		fixture.AssertDiagnostic(t, err, &want)
		fixture.PreparedReferenceOne(t, p, peer, "held_authority")
		fixture.LockExec(t, p.Ctx, admin, "RESET ROLE", 0)
		if r, err := stmt.ExecContext(p.Ctx); err != nil {
			t.Fatal(err)
		} else {
			fixture.LockAffected(t, r, 0)
		}
		fixture.PreparedReferenceAbsent(t, p, peer, "held_authority")
	})
}

func TestReferencePreparedTransactionDroppedOwner(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"prepared_dropped"})
	fixture.PreparedReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		admin, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		fixture.LockExec(t, p.Ctx, admin, "CREATE USER prepared_dropped", 0)
		fixture.LockExec(t, p.Ctx, admin, "SET ROLE prepared_dropped", 0)
		fixture.LockExec(t, p.Ctx, admin, "BEGIN", 0)
		fixture.PreparedReferencePrepare(t, p, b, admin, "dropped_owner")
		fixture.LockExec(t, p.Ctx, admin, "RESET ROLE", 0)
		fixture.LockExec(t, p.Ctx, admin, "DROP ROLE prepared_dropped", 0)
		if entry := fixture.PreparedReferenceOne(t, p, peer, "dropped_owner"); entry.Owner.Valid {
			t.Fatal("dropped role still projected", entry)
		}
		fixture.LockExec(t, p.Ctx, admin, "CREATE USER prepared_dropped", 0)
		fixture.LockExec(t, p.Ctx, admin, "SET ROLE prepared_dropped", 0)
		_, err := admin.ExecContext(p.Ctx, "ROLLBACK PREPARED 'dropped_owner'")
		want := fixture.ControlsReferenceDiagnostic("42501", "permission denied to finish prepared transaction")
		want.Hint = "Must be superuser or the user that prepared the transaction."
		fixture.AssertDiagnostic(t, err, &want)
		fixture.LockExec(t, p.Ctx, admin, "RESET ROLE", 0)
		fixture.LockExec(t, p.Ctx, admin, "ROLLBACK PREPARED 'dropped_owner'", 0)
	})
}
