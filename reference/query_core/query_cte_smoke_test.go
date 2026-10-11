package reference_test

import (
	"bytes"
	"database/sql"
	"strconv"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryCTEFeatureSmoke(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			cteReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				raw := subqueryRun(t, p, b, c)
				run := func(s fixture.Step) { s.Prepared = prepared; raw(s) }
				cteFixture(raw)
				var before bytes.Buffer
				if !b.PG {
					if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
						t.Fatal(err)
					}
				}
				run(cteBag("WITH a(k,v) AS (SELECT id,v FROM t), b AS (SELECT k,v FROM a WHERE k=2) SELECT * FROM b", []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), nil}, {int64(2), nil}}))
				run(cteBag("WITH t AS (SELECT 7 AS id) SELECT a.k,b.id FROM t a(k) CROSS JOIN public.t b", []string{"k", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(1)}, {int64(7), int64(2)}, {int64(7), int64(2)}}))
				run(cteBag("WITH c AS (SELECT id FROM t) SELECT a.id,b.id FROM c a JOIN c b ON a.id=b.id", []string{"id", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(2), int64(2)}, {int64(2), int64(2)}, {int64(2), int64(2)}}))
				for _, hint := range []string{"", "NOT MATERIALIZED"} {
					run(cteBag("WITH c AS "+hint+" (SELECT id,10/(id-1) AS v FROM t) SELECT v FROM c WHERE id<>1", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}, {int64(10)}}))
				}
				q := "WITH c AS MATERIALIZED (SELECT id,10/(id-1) AS v FROM t) SELECT v FROM c WHERE id<>1"
				var x fixture.Executor = c
				if prepared {
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					x = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				}
				subqueryQueryFailure(t, p, b, x, q, fixture.ControlsReferenceDiagnostic("22012", "division by zero"))
				run(fixture.Exec("CREATE SEQUENCE smoke_s", 0))
				run(cteBag("WITH c AS NOT MATERIALIZED (SELECT nextval('smoke_s') AS n) SELECT a.n,b.n FROM c a CROSS JOIN c b", []string{"n", "n"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(1)}}))
				run(cteBag("WITH c AS MATERIALIZED (SELECT nextval('smoke_s')) SELECT 7 AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}}))
				run(cteBag("WITH c AS MATERIALIZED (SELECT nextval('smoke_s') AS n FROM t) SELECT n FROM c LIMIT 1", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
				run(cteBag("SELECT nextval('smoke_s') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
				lockSQL := "WITH c AS (SELECT 1 AS n) SELECT n FROM c AS z FOR UPDATE OF z"
				lockDiagnostic := fixture.ControlsReferenceDiagnostic("0A000", "FOR UPDATE cannot be applied to a WITH query")
				lockDiagnostic.Position = strconv.Itoa(len(lockSQL))
				subqueryAnalysisFailure(raw, lockSQL, lockDiagnostic)
				run(cteBag("WITH c AS (SELECT 1 AS n) SELECT n FROM c FOR UPDATE", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
				for _, predicate := range []string{"FALSE", "NULL::boolean"} {
					run(cteBag("WITH c AS MATERIALIZED (SELECT nextval('smoke_s') AS n) SELECT n FROM c WHERE "+predicate, []string{"n"}, []string{"INT8"}, nil))
					run(cteBag("WITH c AS NOT MATERIALIZED (SELECT 10/(id-1) AS v FROM t) SELECT v FROM c WHERE "+predicate, []string{"v"}, []string{"INT4"}, nil))
				}
				run(cteBag("SELECT nextval('smoke_s') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
				run(fixture.Select("SELECT o.id,(WITH c AS MATERIALIZED (SELECT o.id AS n) SELECT n FROM c) AS n FROM t o ORDER BY o.id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(2), int64(2)}}))
				want := fixture.ControlsReferenceDiagnostic("42712", `WITH query name "c" specified more than once`)
				want.Position = "23"
				s := fixture.ErrorDiagnostic("WITH c AS (SELECT 1), c AS (SELECT 2) SELECT * FROM c", want)
				s.SelectRows = true
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
				run(cteBag("SELECT relname FROM pg_catalog.pg_class WHERE relname='cte_smoke_transient'", []string{"relname"}, []string{"NAME"}, nil))
				run(cteBag("WITH cte_smoke_transient AS (SELECT 42 AS n) SELECT n FROM cte_smoke_transient", []string{"n"}, []string{"INT4"}, [][]any{{int64(42)}}))
				want = fixture.ControlsReferenceDiagnostic("42P01", `relation "cte_smoke_transient" does not exist`)
				want.Position = "15"
				s = fixture.ErrorDiagnostic("SELECT * FROM cte_smoke_transient", want)
				s.SelectRows = true
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
				run(fixture.Exec("DROP SEQUENCE smoke_s", 0))
				if !b.PG {
					var after bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(before.Bytes(), after.Bytes()) {
						t.Fatal("read-only smoke persisted CTE state")
					}
				}
			})
		})
	}
}
