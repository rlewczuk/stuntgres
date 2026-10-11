package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryLockFromOccurrences(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
			fixture.QueryLockRows(t, p, b, a, "SELECT x.id FROM (VALUES (1)) x(id) FOR SHARE OF x", prepared, "id", 1)
			valuesOnly := fixture.QueryLockBegin(t, p.Ctx, a)
			fixture.QueryLockRows(t, p, b, valuesOnly, "SELECT t.id FROM t CROSS JOIN (VALUES (1)) x(n) WHERE t.id=1 FOR UPDATE OF x NOWAIT", prepared, "id", 1)
			fixture.RowAffected(t, p.Ctx, peer, "UPDATE t SET v=v WHERE id=1", prepared, 1)
			if err := valuesOnly.Rollback(); err != nil {
				t.Fatal(err)
			}
			holder := fixture.QueryLockBegin(t, p.Ctx, a)
			fixture.QueryLockRows(t, p, b, holder, "SELECT t.id FROM t CROSS JOIN (VALUES (1)) x(n) WHERE t.id=1 FOR UPDATE NOWAIT", prepared, "id", 1)
			queryLockMigratedBlocked(t, p.Ctx, peer, b.PG, "UPDATE t SET v=v WHERE id=1", prepared)
			if err := holder.Rollback(); err != nil {
				t.Fatal(err)
			}
			q := "SELECT t.id FROM t JOIN u USING(id) AS j FOR SHARE OF j"
			stage := "execute"
			if prepared {
				stage = "prepare"
			}
			fixture.QueryLockFailure(t, p, b, a, q, prepared, fixture.Diagnostic{Code: "0A000", Message: "FOR SHARE cannot be applied to a join", Position: queryLockPosition(q, "j")}, stage)
			// Nonoverlapping self-join occurrences carry different policies even when
			// their physical relation identity is equal.
			holder = fixture.QueryLockBegin(t, p.Ctx, a)
			fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=2 FOR UPDATE", false, "id", 2)
			fixture.QueryLockRows(t, p, b, peer, "SELECT a.id FROM t a JOIN t b ON a.id=1 AND b.id=2 FOR UPDATE OF a NOWAIT FOR UPDATE OF b SKIP LOCKED", prepared, "id")
			fixture.QueryLockNowait(t, p, b, peer, "SELECT a.id FROM t a JOIN t b ON a.id=1 AND b.id=2 FOR UPDATE OF a SKIP LOCKED FOR UPDATE OF b NOWAIT", prepared, "t")
			// Both implicit failed/skipped calls have ended; their acquired a prefix
			// must not remain owned by the peer.
			fixture.RowAffected(t, p.Ctx, holder, "UPDATE t SET v=v WHERE id=1", prepared, 1)
		})
	}
}
