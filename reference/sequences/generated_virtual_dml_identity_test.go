package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGeneratedVirtualCorrelatedUpdateFrom(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				generatedVirtualIdentitySeed(run)
				s := fixture.Exec(`UPDATE t SET v=(SELECT g.c) FROM l LEFT JOIN g ON l.x=1 WHERE t.id=l.x`, 3)
				s.Prepared = prepared
				run(s)
				run(generatedVirtualIdentityRows([][]any{{int64(1), int64(1)}, {int64(2), nil}, {int64(3), nil}}))
				// The only virtual demand is inside a correlated scalar query.
				// A demanded real zero divides; none of the staged writes publish.
				failure := fixture.ImplicitError(`UPDATE t SET v=(SELECT g.z) FROM l LEFT JOIN g ON l.x=3 WHERE t.id=l.x`, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
				failure.Prepared, failure.ErrorStage = prepared, "execute"
				run(failure)
				run(generatedVirtualIdentityRows([][]any{{int64(1), int64(1)}, {int64(2), nil}, {int64(3), nil}}))
			})
		})
	}
}

func TestReferenceGeneratedVirtualCorrelatedDeleteUsing(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				generatedVirtualIdentitySeed(run)
				failure := fixture.ImplicitError(`DELETE FROM t USING l LEFT JOIN g ON l.x=3 WHERE t.id=l.x RETURNING t.id,(SELECT g.z)`, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
				failure.Prepared, failure.ErrorStage, failure.SelectRows = prepared, "execute", true
				run(failure)
				run(generatedVirtualIdentityRows([][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(30)}}))
				s := fixture.Select(`DELETE FROM t USING l LEFT JOIN g ON l.x=1 WHERE t.id=l.x AND (SELECT g.c) IS NULL RETURNING t.id,(SELECT g.c) AS c`, []string{"id", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(2), nil}, {int64(3), nil}})
				s.Prepared, s.Bag = prepared, true
				run(s)
				run(generatedVirtualIdentityRows([][]any{{int64(1), int64(10)}}))
			})
		})
	}
}

func TestReferenceGeneratedVirtualJoinedMerge(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				generatedVirtualIdentitySeed(run)
				merge := func(query string, affected int64) {
					s := fixture.Exec(query, affected)
					if b.PG {
						// pq exports zero for MERGE; exact survivors prove effects.
						s.Affected = 0
					}
					s.Prepared = prepared
					run(s)
				}
				merge(`MERGE INTO t USING l LEFT JOIN g ON l.x=1 ON t.id=l.x AND g.c IS NULL WHEN MATCHED THEN UPDATE SET v=99`, 2)
				run(generatedVirtualIdentityRows([][]any{{int64(1), int64(10)}, {int64(2), int64(99)}, {int64(3), int64(99)}}))
				merge(`MERGE INTO t USING l LEFT JOIN g ON l.x=1 ON t.id=l.x WHEN MATCHED AND g.c IS NULL THEN UPDATE SET v=9 WHEN MATCHED THEN UPDATE SET v=g.c`, 3)
				run(generatedVirtualIdentityRows([][]any{{int64(1), int64(1)}, {int64(2), int64(9)}, {int64(3), int64(9)}}))
				merge(`MERGE INTO t USING l LEFT JOIN g ON l.x=1 ON t.id=l.x WHEN MATCHED THEN UPDATE SET v=g.c`, 3)
				want := [][]any{{int64(1), int64(1)}, {int64(2), nil}, {int64(3), nil}}
				run(generatedVirtualIdentityRows(want))
				failure := fixture.ImplicitError(`MERGE INTO t USING l LEFT JOIN g ON l.x=3 ON t.id=l.x WHEN MATCHED THEN UPDATE SET v=g.z`, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
				failure.Prepared, failure.ErrorStage = prepared, "execute"
				run(failure)
				run(generatedVirtualIdentityRows(want))
			})
		})
	}
}

func generatedVirtualIdentitySeed(run func(fixture.Step)) {
	run(fixture.Exec(`CREATE TABLE t(id INT,v INT)`, 0))
	run(fixture.Exec(`INSERT INTO t VALUES(1,10),(2,20),(3,30)`, 3))
	run(fixture.Exec(`CREATE TABLE l(x INT)`, 0))
	run(fixture.Exec(`INSERT INTO l VALUES(1),(2),(3)`, 3))
	run(fixture.Exec(`CREATE TABLE g(a INT,c INT GENERATED ALWAYS AS(1) VIRTUAL,z INT GENERATED ALWAYS AS(1/a) VIRTUAL)`, 0))
	run(fixture.Exec(`INSERT INTO g(a) VALUES(0)`, 1))
}

func generatedVirtualIdentityRows(rows [][]any) fixture.Step {
	return fixture.Select(`SELECT id,v FROM t ORDER BY id`, []string{"id", "v"}, []string{"INT4", "INT4"}, rows)
}
