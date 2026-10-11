package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePreparedTransactionUtilityProtocolStages(t *testing.T) {
	fixture.PreparedReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		fixture.LockExec(t, p.Ctx, peer, "CREATE TABLE prepared_protocol(v INT NOT NULL)", 0)
		// An unknown completion is valid metadata; only Execute consults GID.
		for _, word := range []string{"COMMIT", "ROLLBACK"} {
			stmt, err := a.PrepareContext(p.Ctx, word+" PREPARED 'protocol_absent'")
			if err != nil {
				t.Fatal("unknown GID rejected during metadata Prepare", err)
			}
			_, err = stmt.ExecContext(p.Ctx)
			want := fixture.ControlsReferenceDiagnostic("42704", "prepared transaction with identifier \"protocol_absent\" does not exist")
			fixture.AssertDiagnostic(t, err, &want)
			stmt.Close()
		}
		// Length belongs to execution, not semantic preparation/reservation.
		gid := strings.Repeat("x", 200)
		fixture.PreparedReferenceCleanup(t, b, gid)
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		stmt, err := a.PrepareContext(p.Ctx, "PREPARE TRANSACTION "+fixture.PreparedReferenceLiteral(gid))
		if err != nil {
			t.Fatal("GID length rejected during metadata Prepare", err)
		}
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO prepared_protocol VALUES(1)", 1)
		_, err = stmt.ExecContext(p.Ctx)
		want := fixture.ControlsReferenceDiagnostic("22023", "transaction identifier \""+gid+"\" is too long")
		fixture.AssertDiagnostic(t, err, &want)
		stmt.Close()
		fixture.PreparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_protocol")
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO prepared_protocol VALUES(2)", 1)
		fixture.PreparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_protocol", 2)
		// Recovery PREPARE may itself be prepared in an already-aborted block,
		// without recovering that block until the held statement is executed.
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		wantNull := fixture.TransactionNull()
		wantNull.Message = `null value in column "v" of relation "prepared_protocol" violates not-null constraint`
		wantNull.Table = "prepared_protocol"
		fixture.AssertDiagnostic(t, fixture.TransactionConstraintExec(t, p.Ctx, a, "INSERT INTO prepared_protocol VALUES(NULL)", false), &wantNull)
		fixture.PreparedReferenceCleanup(t, b, "aborted_protocol")
		recovery, err := a.PrepareContext(p.Ctx, "PREPARE TRANSACTION 'aborted_protocol'")
		if err != nil {
			t.Fatal("recovery metadata Prepare", err)
		}
		defer recovery.Close()
		fixture.PreparedReferenceError(t, p.Ctx, a, "SELECT v FROM prepared_protocol", "25P02", "current transaction is aborted, commands ignored until end of transaction block")
		if r, err := recovery.ExecContext(p.Ctx); err != nil {
			t.Fatal(err)
		} else {
			fixture.LockAffected(t, r, 0)
		}
		fixture.PreparedReferenceAbsent(t, p, peer, "aborted_protocol")
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO prepared_protocol VALUES(3)", 1)
		fixture.PreparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_protocol ORDER BY v", 2, 3)
		// Preparing a forbidden active-block completion is metadata-only, even
		// though its execution fails before looking up the unknown identifier.
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		completion, err := a.PrepareContext(p.Ctx, "COMMIT PREPARED 'protocol_absent'")
		if err != nil {
			t.Fatal("inside-block metadata Prepare", err)
		}
		defer completion.Close()
		_, err = completion.ExecContext(p.Ctx)
		want = fixture.ControlsReferenceDiagnostic("25001", "COMMIT PREPARED cannot run inside a transaction block")
		fixture.AssertDiagnostic(t, err, &want)
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
	})
}

func TestReferencePreparedTransactionTemporarySemanticPrepare(t *testing.T) {
	fixture.PreparedReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		fixture.LockExec(t, p.Ctx, a, "CREATE TEMP SEQUENCE prepared_semantic", 0)
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		stmt, err := a.PrepareContext(p.Ctx, "SELECT last_value FROM pg_temp.prepared_semantic")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		// Resolving a temporary namespace in semantic Prepare is itself access,
		// although the SELECT has never executed or advanced its sequence.
		fixture.PreparedReferenceCleanup(t, b, "semantic_temp")
		fixture.PreparedReferenceError(t, p.Ctx, a, "PREPARE TRANSACTION 'semantic_temp'", "0A000", "cannot PREPARE a transaction that has operated on temporary objects")
		fixture.PreparedReferenceAbsent(t, p, peer, "semantic_temp")
		fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('pg_temp.prepared_semantic')", 1)
	})
}
