package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQueryLockFromOccurrences(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		queryLockScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, a, peer *sql.Conn) {
			queryLockRows(t, p, b, a, "SELECT x.id FROM (VALUES (1)) x(id) FOR SHARE OF x", prepared, "id", 1)
			valuesOnly := queryLockBegin(t, p.ctx, a)
			queryLockRows(t, p, b, valuesOnly, "SELECT t.id FROM t CROSS JOIN (VALUES (1)) x(n) WHERE t.id=1 FOR UPDATE OF x NOWAIT", prepared, "id", 1)
			referenceRowAffected(t, p.ctx, peer, "UPDATE t SET v=v WHERE id=1", prepared, 1)
			if err := valuesOnly.Rollback(); err != nil {
				t.Fatal(err)
			}
			holder := queryLockBegin(t, p.ctx, a)
			queryLockRows(t, p, b, holder, "SELECT t.id FROM t CROSS JOIN (VALUES (1)) x(n) WHERE t.id=1 FOR UPDATE NOWAIT", prepared, "id", 1)
			queryLockMigratedBlocked(t, p.ctx, peer, b.pg, "UPDATE t SET v=v WHERE id=1", prepared)
			if err := holder.Rollback(); err != nil {
				t.Fatal(err)
			}
			q := "SELECT t.id FROM t JOIN u USING(id) AS j FOR SHARE OF j"
			stage := "execute"
			if prepared {
				stage = "prepare"
			}
			queryLockFailure(t, p, b, a, q, prepared, referenceDiagnostic{Code: "0A000", Message: "FOR SHARE cannot be applied to a join", Position: queryLockPosition(q, "j")}, stage)
			// Nonoverlapping self-join occurrences carry different policies even when
			// their physical relation identity is equal.
			holder = queryLockBegin(t, p.ctx, a)
			queryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=2 FOR UPDATE", false, "id", 2)
			queryLockRows(t, p, b, peer, "SELECT a.id FROM t a JOIN t b ON a.id=1 AND b.id=2 FOR UPDATE OF a NOWAIT FOR UPDATE OF b SKIP LOCKED", prepared, "id")
			queryLockNowait(t, p, b, peer, "SELECT a.id FROM t a JOIN t b ON a.id=1 AND b.id=2 FOR UPDATE OF a SKIP LOCKED FOR UPDATE OF b NOWAIT", prepared, "t")
			// Both implicit failed/skipped calls have ended; their acquired a prefix
			// must not remain owned by the peer.
			referenceRowAffected(t, p.ctx, holder, "UPDATE t SET v=v WHERE id=1", prepared, 1)
		})
	}
}
