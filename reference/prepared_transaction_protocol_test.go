package reference_test

import (
	"strings"
	"testing"
)

func TestReferencePreparedTransactionUtilityProtocolStages(t *testing.T) {
	preparedReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, peer := referenceLockConnections(t, p.ctx, b.db)
		referenceLockExec(t, p.ctx, peer, "CREATE TABLE prepared_protocol(v INT NOT NULL)", 0)
		// An unknown completion is valid metadata; only Execute consults GID.
		for _, word := range []string{"COMMIT", "ROLLBACK"} {
			stmt, err := a.PrepareContext(p.ctx, word+" PREPARED 'protocol_absent'")
			if err != nil {
				t.Fatal("unknown GID rejected during metadata Prepare", err)
			}
			_, err = stmt.ExecContext(p.ctx)
			want := controlsReferenceDiagnostic("42704", "prepared transaction with identifier \"protocol_absent\" does not exist")
			referenceAssertDiagnostic(t, err, &want)
			stmt.Close()
		}
		// Length belongs to execution, not semantic preparation/reservation.
		gid := strings.Repeat("x", 200)
		preparedReferenceCleanup(t, b, gid)
		referenceLockExec(t, p.ctx, a, "BEGIN", 0)
		stmt, err := a.PrepareContext(p.ctx, "PREPARE TRANSACTION "+preparedReferenceLiteral(gid))
		if err != nil {
			t.Fatal("GID length rejected during metadata Prepare", err)
		}
		referenceLockExec(t, p.ctx, a, "INSERT INTO prepared_protocol VALUES(1)", 1)
		_, err = stmt.ExecContext(p.ctx)
		want := controlsReferenceDiagnostic("22023", "transaction identifier \""+gid+"\" is too long")
		referenceAssertDiagnostic(t, err, &want)
		stmt.Close()
		preparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_protocol")
		referenceLockExec(t, p.ctx, a, "INSERT INTO prepared_protocol VALUES(2)", 1)
		preparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_protocol", 2)
		// Recovery PREPARE may itself be prepared in an already-aborted block,
		// without recovering that block until the held statement is executed.
		referenceLockExec(t, p.ctx, a, "BEGIN", 0)
		wantNull := referenceTransactionNull()
		wantNull.Message = `null value in column "v" of relation "prepared_protocol" violates not-null constraint`
		wantNull.Table = "prepared_protocol"
		referenceAssertDiagnostic(t, transactionConstraintExec(t, p.ctx, a, "INSERT INTO prepared_protocol VALUES(NULL)", false), &wantNull)
		preparedReferenceCleanup(t, b, "aborted_protocol")
		recovery, err := a.PrepareContext(p.ctx, "PREPARE TRANSACTION 'aborted_protocol'")
		if err != nil {
			t.Fatal("recovery metadata Prepare", err)
		}
		defer recovery.Close()
		preparedReferenceError(t, p.ctx, a, "SELECT v FROM prepared_protocol", "25P02", "current transaction is aborted, commands ignored until end of transaction block")
		if r, err := recovery.ExecContext(p.ctx); err != nil {
			t.Fatal(err)
		} else {
			referenceLockAffected(t, r, 0)
		}
		preparedReferenceAbsent(t, p, peer, "aborted_protocol")
		referenceLockExec(t, p.ctx, a, "INSERT INTO prepared_protocol VALUES(3)", 1)
		preparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_protocol ORDER BY v", 2, 3)
		// Preparing a forbidden active-block completion is metadata-only, even
		// though its execution fails before looking up the unknown identifier.
		referenceLockExec(t, p.ctx, a, "BEGIN", 0)
		completion, err := a.PrepareContext(p.ctx, "COMMIT PREPARED 'protocol_absent'")
		if err != nil {
			t.Fatal("inside-block metadata Prepare", err)
		}
		defer completion.Close()
		_, err = completion.ExecContext(p.ctx)
		want = controlsReferenceDiagnostic("25001", "COMMIT PREPARED cannot run inside a transaction block")
		referenceAssertDiagnostic(t, err, &want)
		referenceLockExec(t, p.ctx, a, "ROLLBACK", 0)
	})
}

func TestReferencePreparedTransactionTemporarySemanticPrepare(t *testing.T) {
	preparedReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, peer := referenceLockConnections(t, p.ctx, b.db)
		referenceLockExec(t, p.ctx, a, "CREATE TEMP SEQUENCE prepared_semantic", 0)
		referenceLockExec(t, p.ctx, a, "BEGIN", 0)
		stmt, err := a.PrepareContext(p.ctx, "SELECT last_value FROM pg_temp.prepared_semantic")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		// Resolving a temporary namespace in semantic Prepare is itself access,
		// although the SELECT has never executed or advanced its sequence.
		preparedReferenceCleanup(t, b, "semantic_temp")
		preparedReferenceError(t, p.ctx, a, "PREPARE TRANSACTION 'semantic_temp'", "0A000", "cannot PREPARE a transaction that has operated on temporary objects")
		preparedReferenceAbsent(t, p, peer, "semantic_temp")
		sequenceProbeValue(t, p.ctx, a, "SELECT nextval('pg_temp.prepared_semantic')", 1)
	})
}
