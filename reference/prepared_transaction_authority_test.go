package reference_test

import (
	"strings"
	"stuntgres"
	"testing"
)

func TestReferencePreparedTransactionAuthority(t *testing.T) {
	roleReferenceCleanup(t, []string{"prepared_owner", "prepared_other"})
	schemaReferenceCleanup(t, "prepared_auth")
	preparedReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		admin, peer := referenceLockConnections(t, p.ctx, b.db)
		for _, name := range []string{"prepared_owner", "prepared_other"} {
			referenceLockExec(t, p.ctx, admin, "CREATE USER "+name, 0)
			if b.pg {
				referenceLockExec(t, p.ctx, admin, "ALTER ROLE "+name+" PASSWORD 'role-core-fixture'", 0)
			}
		}
		// Privileged schema creation gives the real login table-creation authority
		// through the existing ownership API; no native GRANT rollout is needed.
		referenceLockExec(t, p.ctx, admin, "CREATE SCHEMA prepared_auth AUTHORIZATION prepared_owner", 0)
		if b.pg {
			referenceLockExec(t, p.ctx, admin, "GRANT USAGE ON SCHEMA public TO prepared_owner,prepared_other", 0)
		}
		driver := p.nativeConnector.Driver().(*stuntgres.Driver)
		dsn := "reference/" + t.Name()[:strings.LastIndex(t.Name(), "/")]
		// The fixture's exact opaque native key, not a new database per login.
		ownerPool := roleScenarioLoginPool(t, driver, b.pg, dsn, "prepared_owner")
		otherPool := roleScenarioLoginPool(t, driver, b.pg, dsn, "prepared_other")
		owner, ownerPeer := referenceLockConnections(t, p.ctx, ownerPool)
		other := sequenceCoreConn(t, p.ctx, otherPool)
		for _, x := range []referenceExecutor{owner, ownerPeer, other} {
			referenceLockExec(t, p.ctx, x, "SET search_path=public", 0)
		}
		referenceLockExec(t, p.ctx, owner, "CREATE TABLE prepared_auth.prepared_authority(v INT)", 0)
		for _, word := range []string{"COMMIT", "ROLLBACK"} {
			referenceLockExec(t, p.ctx, owner, "BEGIN", 0)
			referenceLockExec(t, p.ctx, owner, "INSERT INTO prepared_auth.prepared_authority VALUES(7)", 1)
			preparedReferencePrepare(t, p, b, owner, "authority")
			_, err := other.ExecContext(p.ctx, word+" PREPARED 'authority'")
			want := controlsReferenceDiagnostic("42501", "permission denied to finish prepared transaction")
			want.Hint = "Must be superuser or the user that prepared the transaction."
			referenceAssertDiagnostic(t, err, &want)
			entry := preparedReferenceOne(t, p, peer, "authority")
			if !entry.owner.Valid || entry.owner.String != "prepared_owner" {
				t.Fatal(entry)
			}
			preparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_auth.prepared_authority")
			referenceLockExec(t, p.ctx, ownerPeer, "ROLLBACK PREPARED 'authority'", 0)
		}
		// SAME held statement must consult the actor at execution, not Prepare.
		referenceLockExec(t, p.ctx, owner, "BEGIN", 0)
		preparedReferencePrepare(t, p, b, owner, "held_authority")
		stmt, err := admin.PrepareContext(p.ctx, "COMMIT PREPARED 'held_authority'")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		referenceLockExec(t, p.ctx, admin, "SET ROLE prepared_other", 0)
		_, err = stmt.ExecContext(p.ctx)
		want := controlsReferenceDiagnostic("42501", "permission denied to finish prepared transaction")
		want.Hint = "Must be superuser or the user that prepared the transaction."
		referenceAssertDiagnostic(t, err, &want)
		preparedReferenceOne(t, p, peer, "held_authority")
		referenceLockExec(t, p.ctx, admin, "RESET ROLE", 0)
		if r, err := stmt.ExecContext(p.ctx); err != nil {
			t.Fatal(err)
		} else {
			referenceLockAffected(t, r, 0)
		}
		preparedReferenceAbsent(t, p, peer, "held_authority")
	})
}

func TestReferencePreparedTransactionDroppedOwner(t *testing.T) {
	roleReferenceCleanup(t, []string{"prepared_dropped"})
	preparedReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		admin, peer := referenceLockConnections(t, p.ctx, b.db)
		referenceLockExec(t, p.ctx, admin, "CREATE USER prepared_dropped", 0)
		referenceLockExec(t, p.ctx, admin, "SET ROLE prepared_dropped", 0)
		referenceLockExec(t, p.ctx, admin, "BEGIN", 0)
		preparedReferencePrepare(t, p, b, admin, "dropped_owner")
		referenceLockExec(t, p.ctx, admin, "RESET ROLE", 0)
		referenceLockExec(t, p.ctx, admin, "DROP ROLE prepared_dropped", 0)
		if entry := preparedReferenceOne(t, p, peer, "dropped_owner"); entry.owner.Valid {
			t.Fatal("dropped role still projected", entry)
		}
		referenceLockExec(t, p.ctx, admin, "CREATE USER prepared_dropped", 0)
		referenceLockExec(t, p.ctx, admin, "SET ROLE prepared_dropped", 0)
		_, err := admin.ExecContext(p.ctx, "ROLLBACK PREPARED 'dropped_owner'")
		want := controlsReferenceDiagnostic("42501", "permission denied to finish prepared transaction")
		want.Hint = "Must be superuser or the user that prepared the transaction."
		referenceAssertDiagnostic(t, err, &want)
		referenceLockExec(t, p.ctx, admin, "RESET ROLE", 0)
		referenceLockExec(t, p.ctx, admin, "ROLLBACK PREPARED 'dropped_owner'", 0)
	})
}
