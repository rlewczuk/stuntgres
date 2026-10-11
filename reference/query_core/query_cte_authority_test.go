package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func cteAuthorityFreshness(t *testing.T) {
	cteReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE SCHEMA cte_path_a", 0), fixture.Exec("CREATE SCHEMA cte_path_b", 0),
			fixture.Exec("CREATE TABLE cte_path_a.source(id INT)", 0), fixture.Exec("CREATE TABLE cte_path_b.source(id INT)", 0),
			fixture.Exec("INSERT INTO cte_path_a.source VALUES(1)", 1), fixture.Exec("INSERT INTO cte_path_b.source VALUES(2)", 1),
			fixture.Exec("SET search_path TO cte_path_a,public", 0),
		} {
			run(s)
		}
		q := "WITH c AS MATERIALIZED (SELECT id FROM source) SELECT id FROM c"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, cteBag(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(fixture.Exec("SET search_path TO cte_path_b,public", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, cteBag(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
		run(fixture.Exec("ALTER TABLE cte_path_b.source ADD COLUMN ignored INT", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, cteBag(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
		run(fixture.Exec("SET search_path TO public", 0))
		run(fixture.Exec("CREATE TABLE parameter_source(id INT,v TEXT)", 0))
		q = "WITH a AS (SELECT id FROM parameter_source WHERE id=$1), b AS (SELECT v FROM parameter_source WHERE v=$1) SELECT * FROM a"
		want := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: text = integer")
		want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
		want.Position = "102"
		// Direct lib/pq execution needs a bound argument to use the extended
		// protocol; otherwise $1 is an undefined parameter, not a type conflict.
		for _, prepared := range []bool{false, true} {
			for _, queryMode := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, want, int64(1))
				s.Prepared, s.SelectRows = prepared, queryMode
				if prepared {
					s.ErrorStage = "prepare"
				} else {
					s.ErrorStage = "execute"
				}
				run(s)
			}
		}
		// Explicitly qualified real leaves do not follow a virtual spelling.
		subqueryBoth(run, cteBag("WITH source AS (SELECT 9 AS id) SELECT a.id,b.id FROM source a CROSS JOIN cte_path_a.source b", []string{"id", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(9), int64(1)}}))
		run(fixture.Exec("DROP SCHEMA cte_path_a CASCADE", 0))
		run(fixture.Exec("DROP SCHEMA cte_path_b CASCADE", 0))
	})
}
