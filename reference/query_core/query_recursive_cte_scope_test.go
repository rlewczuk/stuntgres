package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceQueryRecursiveCTEScope(t *testing.T) {
	recursiveCTEReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE r(n INT)", 0))
		run(fixture.Exec("INSERT INTO r VALUES(9)", 1))
		run(fixture.Exec("CREATE TABLE recursive_one(n INT)", 0))
		run(fixture.Exec("INSERT INTO recursive_one VALUES(1)", 1))
		for _, tc := range []struct {
			q    string
			rows [][]any
		}{
			{"WITH RECURSIVE r AS (SELECT 7 AS n) SELECT n FROM r", recursiveCTEInts(7)},
			{"WITH RECURSIVE a AS (SELECT n FROM b), b AS (SELECT 7 AS n) SELECT n FROM a", recursiveCTEInts(7)},
			{"WITH RECURSIVE a AS (SELECT n FROM b), b(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM b WHERE n<3) SELECT n FROM a", recursiveCTEInts(1, 2, 3)},
			{"WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT d.n+1 FROM (SELECT n FROM r) d WHERE d.n<3) SELECT n FROM r", recursiveCTEInts(1, 2, 3)},
			{"WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT r.n+1 FROM r LEFT JOIN recursive_one o ON TRUE WHERE r.n<3) SELECT n FROM r", recursiveCTEInts(1, 2, 3)},
			{"WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT r.n+1 FROM recursive_one o RIGHT JOIN r ON TRUE WHERE r.n<3) SELECT n FROM r", recursiveCTEInts(1, 2, 3)},
			{"WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT r.n+1 FROM r INNER JOIN recursive_one o ON TRUE WHERE r.n<3) SELECT n FROM r", recursiveCTEInts(1, 2, 3)},
			{"WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT r.n+1 FROM r CROSS JOIN recursive_one o WHERE r.n<3) SELECT n FROM r", recursiveCTEInts(1, 2, 3)},
			{"WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL (SELECT n+1 FROM r WHERE n<3 INTERSECT SELECT n FROM (VALUES(2),(3)) s(n))) SELECT n FROM r", recursiveCTEInts(1, 2, 3)},
			{"WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL (SELECT n+1 FROM r WHERE n<3 EXCEPT SELECT 9)) SELECT n FROM r", recursiveCTEInts(1, 2, 3)},
			{"WITH RECURSIVE r(n) AS (SELECT 1 UNION (SELECT n+1 FROM r WHERE n<3 UNION SELECT 3)) SELECT n FROM r", recursiveCTEInts(1, 2, 3)},
			{"WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT DISTINCT n+1 FROM r WHERE n<3) SELECT n FROM r", recursiveCTEInts(1, 2, 3)},
			{"WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM r WHERE n<3 GROUP BY n) SELECT n FROM r", recursiveCTEInts(1, 2, 3)},
			{"WITH RECURSIVE r(n) AS ((SELECT 1 ORDER BY 1 LIMIT 1) UNION ALL SELECT n+1 FROM r WHERE n<3) SELECT n FROM r", recursiveCTEInts(1, 2, 3)},
			{"WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL (SELECT n+1 FROM r WHERE n<3 LIMIT 1)) SELECT n FROM r", recursiveCTEInts(1, 2, 3)},
			{"WITH RECURSIVE \"é\"(n) AS (SELECT 1 UNION ALL SELECT x.n+1 FROM \"é\" x WHERE x.n<3) SELECT n FROM \"é\"", recursiveCTEInts(1, 2, 3)},
			{"WITH RECURSIVE recursive(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM recursive WHERE n<3) SELECT n FROM recursive", recursiveCTEInts(1, 2, 3)},
			{"WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM r WHERE n<3) SELECT p.n FROM public.r p CROSS JOIN r WHERE r.n=1", recursiveCTEInts(9)},
			{"WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM r WHERE n<3) SELECT n FROM (WITH r AS (SELECT 7 AS n) SELECT n FROM r) d", recursiveCTEInts(7)},
			{"WITH r AS (SELECT 1 AS n) SELECT n FROM (WITH a AS (SELECT n FROM r),r AS (SELECT 2 AS n) SELECT n FROM a) d", recursiveCTEInts(1)},
			{"WITH r AS (SELECT 1 AS n) SELECT n FROM (WITH RECURSIVE a AS (SELECT n FROM r),r AS (SELECT 2 AS n) SELECT n FROM a) d", recursiveCTEInts(2)},
		} {
			recursiveCTERoutes(t, p, b, c, cteBag(tc.q, []string{"n"}, []string{"INT4"}, tc.rows))
		}
		recursiveCTERoutes(t, p, b, c, cteBag("WITH RECURSIVE r(k) AS (SELECT 1 AS n,10 AS v UNION ALL SELECT k+1,v FROM r WHERE k<3) SELECT * FROM r", []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(10)}, {int64(3), int64(10)}}))
		recursiveCTERoutes(t, p, b, c, cteBag("WITH RECURSIVE r(x,x) AS (SELECT 1,2 UNION SELECT 1,2 FROM r) SELECT * FROM r", []string{"x", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
		recursiveCTERoutes(t, p, b, c, cteBag("WITH RECURSIVE r(n,k) AS (SELECT 1,1::bigint UNION ALL SELECT n+1,row_number() OVER () FROM r WHERE n<3) SELECT n,k FROM r", []string{"n", "k"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}, {int64(3), int64(1)}}))
		recursiveCTERoutes(t, p, b, c, cteBag("SELECT o.n,(WITH RECURSIVE r(n) AS (SELECT o.n UNION ALL SELECT n+1 FROM r WHERE n<3) SELECT sum(n) FROM r) AS total FROM (VALUES(1),(2)) o(n)", []string{"n", "total"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(6)}, {int64(2), int64(5)}}))
		// INSERT/UPDATE/DELETE accept recursive WITH input; MERGE rejects
		// a directly attached recursive modifier before analyzing the body.
		run(fixture.Exec("CREATE TABLE recursive_target(n INT)", 0))
		subqueryBoth(run, fixture.Exec("WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM r WHERE n<3) INSERT INTO recursive_target SELECT n FROM r", 3))
		run(fixture.Exec("DELETE FROM recursive_target", 6))
		run(fixture.Exec("INSERT INTO recursive_target VALUES(1),(2),(3)", 3))
		run(fixture.Exec("WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM r WHERE n<3) UPDATE recursive_target SET n=n+10 WHERE n IN (SELECT n FROM r)", 3))
		run(fixture.Exec("WITH RECURSIVE r(n) AS (SELECT 11 UNION ALL SELECT n+1 FROM r WHERE n<13) DELETE FROM recursive_target WHERE n IN (SELECT n FROM r)", 3))
		merge := "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM r WHERE n<3) MERGE INTO recursive_target t USING r ON t.n=r.n WHEN NOT MATCHED THEN INSERT VALUES(r.n)"
		want := fixture.ControlsReferenceDiagnostic("42601", "WITH RECURSIVE is not supported for MERGE statement")
		for _, prefix := range []string{"", "/*é*/ "} {
			subqueryAnalysisFailure(run, prefix+merge, want)
		}
		run(cteBag("SELECT n FROM recursive_target", []string{"n"}, []string{"INT4"}, nil))
		for _, prepared := range []bool{false, true} {
			route := map[bool]string{false: "direct", true: "prepared"}[prepared]
			t.Run("derived_merge_"+route, func(t *testing.T) {
				run := subqueryRun(t, p, b, c)
				name := "recursive_merge_" + route
				run(fixture.Exec("CREATE TABLE "+name+"(n INT)", 0))
				t.Cleanup(func() {
					cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
					defer stop()
					if _, err := c.ExecContext(cleanup, "DROP TABLE IF EXISTS "+name); err != nil {
						t.Error("MERGE target cleanup", err)
					}
				})
				run(fixture.Exec("INSERT INTO "+name+" VALUES(99)", 1))
				run(cteBag("SELECT n FROM "+name, []string{"n"}, []string{"INT4"}, recursiveCTEInts(99)))
				subqueryBoth(run, cteBag(recursiveCTEFinite, []string{"n"}, []string{"INT4"}, recursiveCTEInts(1, 2, 3)))
				// Follow fixture.MergeRun's established codec contract while
				// retaining the scenario's actual pinned physical connection.
				// pq does not decode MERGE tags; exact rows prove SQL effects.
				mergeRun := func(q string) {
					s := fixture.Exec(q, 3)
					s.Prepared = prepared
					if b.PG {
						s.Affected = 0
					}
					fixture.InsertSelectRun(t, p.Ctx, c, b.Recorder, s)
				}
				mergeRun("MERGE INTO " + name + " t USING (" + recursiveCTEFinite + ") src ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(src.n)")
				run(cteBag("SELECT n FROM "+name, []string{"n"}, []string{"INT4"}, recursiveCTEInts(1, 2, 3, 99)))
				mergeRun("MERGE INTO " + name + " t USING (" + recursiveCTEFinite + ") src ON t.n=src.n WHEN MATCHED THEN UPDATE SET n=src.n+10")
				run(cteBag("SELECT n FROM "+name, []string{"n"}, []string{"INT4"}, recursiveCTEInts(11, 12, 13, 99)))
			})
		}
	})
}
