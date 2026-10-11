package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryLockSelectedKinds(t *testing.T) {
	fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		fixture.LockExec(t, p.Ctx, a, "CREATE SEQUENCE s", 0)
		for _, prepared := range []bool{false, true} {
			// PostgreSQL's nonlocking reference rowmark still validates sequence kind.
			// This is Execute preflight, not a physical row grant or UPDATE requirement.
			fixture.QueryLockFailure(t, p, b, a, "SELECT a.id FROM t a CROSS JOIN s WHERE a.id=1 FOR UPDATE OF a NOWAIT", prepared, fixture.Diagnostic{Code: "42809", Message: `cannot lock rows in sequence "s"`}, "execute")
			fixture.QueryLockRows(t, p, b, a, "SELECT a.id FROM t a CROSS JOIN pg_catalog.pg_class c WHERE FALSE FOR SHARE OF a SKIP LOCKED", prepared, "id")
			fixture.QueryLockFailure(t, p, b, a, "SELECT a.id FROM t a CROSS JOIN s WHERE FALSE FOR UPDATE OF s", prepared, fixture.Diagnostic{Code: "42809", Message: `cannot lock rows in sequence "s"`}, "execute")
			if !b.PG {
				fixture.QueryLockFailure(t, p, b, a, "SELECT a.id FROM t a CROSS JOIN pg_catalog.pg_class c WHERE FALSE FOR UPDATE OF c", prepared, fixture.Diagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"}, "execute")
			}
			reader, err := peer.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			fixture.QueryLockRollbackCleanup(t, reader)
			fixture.QueryLockFailure(t, p, b, reader, "SELECT id FROM t FOR SHARE OF t SKIP LOCKED", prepared, fixture.Diagnostic{Code: "25006", Message: "cannot execute SELECT FOR SHARE in a read-only transaction"}, "execute")
			if err := reader.Rollback(); err != nil {
				t.Fatal(err)
			}
			fixture.RowAffected(t, p.Ctx, peer, "UPDATE t SET v=v", prepared, 4)
		}
	})
}
