package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQueryLockSelectedKinds(t *testing.T) {
	queryLockScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, a, peer *sql.Conn) {
		referenceLockExec(t, p.ctx, a, "CREATE SEQUENCE s", 0)
		for _, prepared := range []bool{false, true} {
			// PostgreSQL's nonlocking reference rowmark still validates sequence kind.
			// This is Execute preflight, not a physical row grant or UPDATE requirement.
			queryLockFailure(t, p, b, a, "SELECT a.id FROM t a CROSS JOIN s WHERE a.id=1 FOR UPDATE OF a NOWAIT", prepared, referenceDiagnostic{Code: "42809", Message: `cannot lock rows in sequence "s"`}, "execute")
			queryLockRows(t, p, b, a, "SELECT a.id FROM t a CROSS JOIN pg_catalog.pg_class c WHERE FALSE FOR SHARE OF a SKIP LOCKED", prepared, "id")
			queryLockFailure(t, p, b, a, "SELECT a.id FROM t a CROSS JOIN s WHERE FALSE FOR UPDATE OF s", prepared, referenceDiagnostic{Code: "42809", Message: `cannot lock rows in sequence "s"`}, "execute")
			if !b.pg {
				queryLockFailure(t, p, b, a, "SELECT a.id FROM t a CROSS JOIN pg_catalog.pg_class c WHERE FALSE FOR UPDATE OF c", prepared, referenceDiagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"}, "execute")
			}
			reader, err := peer.BeginTx(p.ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			queryLockRollbackCleanup(t, reader)
			queryLockFailure(t, p, b, reader, "SELECT id FROM t FOR SHARE OF t SKIP LOCKED", prepared, referenceDiagnostic{Code: "25006", Message: "cannot execute SELECT FOR SHARE in a read-only transaction"}, "execute")
			if err := reader.Rollback(); err != nil {
				t.Fatal(err)
			}
			referenceRowAffected(t, p.ctx, peer, "UPDATE t SET v=v", prepared, 4)
		}
	})
}
