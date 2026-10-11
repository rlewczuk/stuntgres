package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGeneratedVirtualStoredViewGraft(t *testing.T) {
	// Existing updatable-view fixtures admit computed scalar-subquery outputs
	// alongside a single physical target. Reading that output in SET transplants
	// its stored query graph; the view itself is not a writable joined view.
	for _, tc := range []struct {
		name  string
		query string
		rows  [][]any
	}{
		{"arithmetic", `SELECT p.c+1 FROM l LEFT JOIN g p ON l.x=1 LEFT JOIN g q ON l.x=2 WHERE l.x=t.id`, [][]any{{int64(1), int64(2)}, {int64(2), nil}}},
		{"case_aliases", `SELECT CASE WHEN q.c IS NULL THEN p.c+1 ELSE q.c+10 END FROM l LEFT JOIN g p ON l.x=1 LEFT JOIN g q ON l.x=2 WHERE l.x=t.id`, [][]any{{int64(1), int64(2)}, {int64(2), int64(11)}}},
		{"derived_aliases", `SELECT CASE WHEN d.q IS NULL THEN d.p ELSE d.q+9 END FROM (SELECT l.x,p.c+1 AS p,q.c+1 AS q FROM l LEFT JOIN g p ON l.x=1 LEFT JOIN g q ON l.x=2) d WHERE d.x=t.id`, [][]any{{int64(1), int64(2)}, {int64(2), int64(11)}}},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", tc.name, prepared), func(t *testing.T) {
				generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
					run(fixture.Exec(`CREATE TABLE t(id INT,v INT)`, 0))
					run(fixture.Exec(`INSERT INTO t VALUES(1,10),(2,20)`, 2))
					run(fixture.Exec(`CREATE TABLE l(x INT)`, 0))
					run(fixture.Exec(`INSERT INTO l VALUES(1),(2)`, 2))
					run(fixture.Exec(`CREATE TABLE g(a INT,c INT GENERATED ALWAYS AS(1) VIRTUAL)`, 0))
					run(fixture.Exec(`INSERT INTO g(a) VALUES(NULL)`, 1))
					run(fixture.Exec(`CREATE VIEW graft_v AS SELECT id,v,(`+tc.query+`) AS answer FROM t`, 0))
					// Read independently before grafting; two aliases of the same
					// relation alternate presence without using NULL-cell heuristics.
					run(fixture.Select(`SELECT id,answer FROM graft_v ORDER BY id`, []string{"id", "answer"}, []string{"INT4", "INT4"}, tc.rows))
					s := fixture.Exec(`UPDATE graft_v SET v=answer`, 2)
					s.Prepared = prepared
					run(s)
					run(generatedVirtualIdentityRows(tc.rows))
					run(fixture.Exec(`CREATE VIEW graft_fail AS SELECT id,v,(SELECT CASE WHEN p.c IS NULL THEN 10/(l.x-2) ELSE p.c+1 END FROM l LEFT JOIN g p ON l.x=1 WHERE l.x=t.id) AS unsafe FROM t`, 0))
					failure := fixture.ImplicitError(`UPDATE graft_fail SET v=unsafe`, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
					failure.Prepared, failure.ErrorStage = prepared, "execute"
					run(failure)
					run(generatedVirtualIdentityRows(tc.rows))
				})
			})
		}
	}
}
