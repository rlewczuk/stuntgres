package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompositeExprJoinTargetPaths(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.CompositeReferenceSetup(t, p, b, c,
			`CREATE TYPE join_array_pair AS (a integer[],t text)`,
			`CREATE TYPE join_array_outer AS (p join_array_pair,n integer)`,
			`CREATE TABLE target(k integer PRIMARY KEY,w join_array_outer)`,
			`CREATE TABLE a(id integer)`, `CREATE TABLE b(id integer)`,
			`CREATE TABLE sentinel(k integer)`)
		for _, q := range []string{
			`INSERT INTO target VALUES(1,ROW(ROW(ARRAY[10,20],'kept'),7))`,
			`INSERT INTO a VALUES(1),(2)`, `INSERT INTO b VALUES(1),(2)`,
			`INSERT INTO sentinel VALUES(99)`,
		} {
			count := int64(1)
			if q == `INSERT INTO a VALUES(1),(2)` || q == `INSERT INTO b VALUES(1),(2)` {
				count = 2
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, count))
		}
		check := func(want string) {
			t.Helper()
			compositeExprBoth(t, p, b, c,
				`SELECT k,((w).p).a::text AS a,((w).p).t AS t,(w).n FROM target ORDER BY k`,
				[]string{"k", "a", "t", "n"}, []string{"INT4", "TEXT", "TEXT", "INT4"},
				[][]any{{int64(1), want, "kept", int64(7)}})
			compositeExprBoth(t, p, b, c, `SELECT k FROM sentinel`, []string{"k"}, []string{"INT4"}, [][]any{{int64(99)}})
		}
		check("{10,20}")
		for _, tc := range []struct {
			name, query, message string
		}{
			{"selected-bound-before-row-rhs", `UPDATE target SET w.p.a[(SELECT a.id FROM a FULL JOIN b ON a.id>b.id)]=1/(k-k)`, "FULL JOIN is only supported with merge-joinable or hash-joinable join conditions"},
			{"zero-row-update", `UPDATE target SET w.p.a[(SELECT a.id FROM a FULL JOIN b ON a.id>b.id)]=90 WHERE FALSE`, "FULL JOIN is only supported with merge-joinable or hash-joinable join conditions"},
			{"zero-row-insert", `INSERT INTO target(k,w.p.a[(SELECT a.id FROM a FULL JOIN b ON a.id>b.id)]) SELECT 2,90 WHERE FALSE`, "FULL JOIN is only supported with merge-joinable or hash-joinable join conditions"},
			{"conflict-zero-row-update", `INSERT INTO target VALUES(1,NULL) ON CONFLICT(k) DO UPDATE SET w.p.a[(SELECT a.id FROM a FULL JOIN b ON a.id>b.id)]=90 WHERE FALSE`, "FULL JOIN is only supported with merge-joinable or hash-joinable join conditions"},
			{"merge-unused-update", `MERGE INTO target h USING (VALUES(2)) s(k) ON h.k=s.k WHEN MATCHED THEN UPDATE SET w.p.a[(SELECT a.id FROM a FULL JOIN b ON a.id>b.id)]=90`, "FULL JOIN is only supported with merge-joinable or hash-joinable join conditions"},
			{"nested-local-rowmark", `UPDATE target SET w.p.a[(SELECT a.id FROM a LEFT JOIN b ON a.id=b.id FOR UPDATE)]=90 WHERE FALSE`, "FOR UPDATE cannot be applied to the nullable side of an outer join"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				fixture.CompositeReferenceError(t, p, c, tc.query, "execute", fixture.CompositeReferenceDiagnostic("0A000", tc.message))
				check("{10,20}")
			})
		}
		q := `UPDATE target SET w.p.a[(SELECT a.id FROM a LEFT JOIN b ON a.id=b.id WHERE a.id=$1)]=$2 WHERE k=1`
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for _, route := range []struct {
			name string
			x    fixture.Executor
		}{{"direct", c}, {"held", fixture.HeldStatement{Executor: c, Statement: held, SQL: q}}} {
			t.Run(route.name, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`UPDATE target SET w.p.a=ARRAY[10,20]`, 1))
				for _, tc := range []struct {
					index, value int
					want         string
				}{{1, 71, "{71,20}"}, {2, 82, "{71,82}"}} {
					fixture.RunStep(t, p.Ctx, route.x, b.Recorder, fixture.Exec(q, 1, tc.index, tc.value))
					check(tc.want)
				}
				fixture.RunStep(t, p.Ctx, route.x, b.Recorder, fixture.Select(q, nil, nil, nil, 1, 93))
				check("{93,82}")
			})
		}
	})
}
