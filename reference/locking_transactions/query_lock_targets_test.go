package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryLockTargets(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			q, free, held string
			want          int64
		}{
			{"SELECT b.v FROM t a JOIN u b USING(id) WHERE a.id=1 FOR UPDATE OF a", "UPDATE u SET v=v WHERE id=1", "UPDATE t SET v=v WHERE id=1", 10},
			{"SELECT a.v FROM t a JOIN u b ON a.id=b.id WHERE a.id=1 FOR UPDATE OF b", "UPDATE t SET v=v WHERE id=1", "UPDATE u SET v=v WHERE id=1", 10},
			{"SELECT a.v FROM t a JOIN t b ON a.id=1 AND b.id=2 FOR UPDATE OF b", "UPDATE t SET v=v WHERE id=1", "UPDATE t SET v=v WHERE id=2", 10},
			{`SELECT "A".v FROM t "A" JOIN u b USING(id) WHERE "A".id=1 FOR SHARE OF "A","A"`, "UPDATE u SET v=v WHERE id=1", "UPDATE t SET v=v WHERE id=1", 10},
			{"SELECT j.id AS v FROM (t JOIN u USING(id)) j WHERE j.id=1 FOR UPDATE OF t", "UPDATE u SET v=v WHERE id=1", "UPDATE t SET v=v WHERE id=1", 1},
			{"SELECT a.v FROM t a LEFT JOIN u b ON a.id=b.id AND b.id=9 WHERE a.id=1 FOR UPDATE OF a", "UPDATE u SET v=v WHERE id=1", "UPDATE t SET v=v WHERE id=1", 10},
			{"SELECT b.v FROM u a RIGHT JOIN t b ON a.id=b.id AND a.id=9 WHERE b.id=1 FOR UPDATE OF b", "UPDATE u SET v=v WHERE id=1", "UPDATE t SET v=v WHERE id=1", 10},
		} {
			t.Run(fmt.Sprintf("%s/%v", tc.q, prepared), func(t *testing.T) {
				fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
					holder := fixture.QueryLockBegin(t, p.Ctx, a)
					fixture.QueryLockRows(t, p, b, holder, tc.q, prepared, "v", tc.want)
					fixture.RowAffected(t, p.Ctx, peer, tc.free, prepared, 1)
					queryLockMigratedBlocked(t, p.Ctx, peer, b.PG, tc.held, prepared)
					if err := holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					fixture.RowAffected(t, p.Ctx, peer, tc.held, prepared, 1)
				})
			})
		}
	}
	// Whole candidates, not individual joined leaves, are removed by SKIP.
	for _, target := range []string{"a", "b", "a,b"} {
		t.Run("joined skip/"+target, func(t *testing.T) {
			fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
				holder := fixture.QueryLockBegin(t, p.Ctx, a)
				fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=1 FOR UPDATE", false, "id", 1)
				fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM u WHERE id=2 FOR UPDATE", false, "id", 2)
				want := []int64{3, 4}
				if target == "a" {
					want = []int64{2, 3, 4}
				}
				if target == "b" {
					want = []int64{1, 3, 4}
				}
				fixture.QueryLockRows(t, p, b, peer, "SELECT a.id FROM t a JOIN u b USING(id) ORDER BY a.id FOR UPDATE OF "+target+" SKIP LOCKED", true, "id", want...)
			})
		})
	}
}

func TestReferenceQueryLockRepeatedClauses(t *testing.T) {
	for _, clauses := range []string{
		"FOR UPDATE OF a SKIP LOCKED FOR SHARE OF a NOWAIT",
		"FOR SHARE OF a NOWAIT FOR UPDATE OF a SKIP LOCKED",
		"FOR KEY SHARE OF a NOWAIT FOR UPDATE SKIP LOCKED",
		"FOR UPDATE SKIP LOCKED FOR KEY SHARE OF a NOWAIT",
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%v", clauses, prepared), func(t *testing.T) {
				fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
					holder := fixture.QueryLockBegin(t, p.Ctx, a)
					fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=1 FOR NO KEY UPDATE", false, "id", 1)
					fixture.QueryLockNowait(t, p, b, peer, "SELECT a.id FROM t a JOIN u b USING(id) WHERE a.id=1 "+clauses, prepared, "t")
				})
			})
		}
	}
	for _, clauses := range []string{
		"FOR SHARE OF a FOR KEY SHARE OF a SKIP LOCKED",
		"FOR KEY SHARE OF a SKIP LOCKED FOR SHARE OF a",
		"FOR UPDATE OF a NOWAIT FOR SHARE OF b SKIP LOCKED",
	} {
		t.Run(clauses, func(t *testing.T) {
			fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
				holder := fixture.QueryLockBegin(t, p.Ctx, a)
				table := "t"
				if clauses == "FOR UPDATE OF a NOWAIT FOR SHARE OF b SKIP LOCKED" {
					table = "u"
				}
				fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM "+table+" WHERE id=1 FOR UPDATE", false, "id", 1)
				fixture.QueryLockRows(t, p, b, peer, "SELECT a.id FROM t a JOIN u b USING(id) ORDER BY a.id "+clauses, true, "id", 2, 3, 4)
			})
		})
	}
}

func TestReferenceQueryLockPlacement(t *testing.T) {
	for _, tail := range []string{
		"FOR UPDATE OF t NOWAIT FOR KEY SHARE OF t SKIP LOCKED LIMIT 1 OFFSET 1",
		"OFFSET 1 LIMIT 1 FOR UPDATE OF t NOWAIT FOR KEY SHARE OF t SKIP LOCKED",
		"FOR SHARE OF t SKIP LOCKED OFFSET 1 FETCH FIRST 1 ROW ONLY",
		"OFFSET 1 FETCH NEXT 1 ROW ONLY FOR SHARE OF t SKIP LOCKED",
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%v", tail, prepared), func(t *testing.T) {
				fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
					q := "SELECT id FROM t ORDER BY id " + tail
					holder := fixture.QueryLockBegin(t, p.Ctx, a)
					fixture.QueryLockRows(t, p, b, holder, q, prepared, "id", 2)
					if err := holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					fixture.LockExec(t, p.Ctx, a, "CREATE TABLE dst(id INT)", 0)
					fixture.RowAffected(t, p.Ctx, a, "INSERT INTO dst "+q, prepared, 1)
					fixture.QueryLockRows(t, p, b, a, "SELECT id FROM dst", false, "id", 2)
					fixture.RowAffected(t, p.Ctx, a, "CREATE VIEW placed AS "+q, prepared, 0)
					fixture.QueryLockRows(t, p, b, a, "SELECT id FROM placed", prepared, "id", 2)
				})
			})
		}
	}
}
