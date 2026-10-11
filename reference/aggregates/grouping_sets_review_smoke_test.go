package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGroupingSetsReviewFeatureSmoke(t *testing.T) {
	fixture.GroupingSetsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		fixture.GroupingSetsFixture(run)
		valid := fixture.GroupingSetsBag("SELECT GROUPING(a,b,v),count(*) FROM gs GROUP BY ROLLUP(a,(b,v))", []string{"grouping", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(0), int64(1)}, {int64(0), int64(1)}, {int64(0), int64(1)}, {int64(3), int64(2)}, {int64(3), int64(1)}, {int64(7), int64(3)}})
		fixture.GroupingSetsBoth(run, valid)
		q := "SELECT $1::INT AS k,GROUPING($1::INT),count(*) FROM gs GROUP BY GROUPING SETS(($1::INT),($1::INT),())"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, value := range []any{int64(7), nil, int64(7)} {
			s := fixture.GroupingSetsBag(q, []string{"k", "grouping", "count"}, []string{"INT4", "INT4", "INT8"}, [][]any{{value, int64(0), int64(3)}, {value, int64(0), int64(3)}, {nil, int64(1), int64(3)}})
			s.Args = []any{value}
			s.CheckMetadata = groupingSetsReviewParameterMetadata(b)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		q = "SELECT count(*) FROM gs GROUP BY ROLLUP(())"
		fixture.GroupingSetsAnalysisError(run, fixture.GroupingSetsError(q, "42601", "syntax error at or near \")\"", "))"))
		q = "INSERT INTO gs VALUES(9,9,9); SELECT count(*) FROM gs GROUP BY CUBE(GROUPING SETS((a),()))"
		fixture.GroupingSetsAnalysisError(run, fixture.GroupingSetsError(q, "42601", "syntax error at or near \"SETS\"", "SETS"))
		run(fixture.Select("SELECT count(*) FROM gs", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
		run(fixture.Exec("CREATE TABLE gs_review_smoke(k INT,g INT,n BIGINT)", 0))
		q = "INSERT INTO gs_review_smoke SELECT $1::INT,GROUPING($1::INT),count(*) FROM gs GROUP BY GROUPING SETS(($1::INT),($1::INT),())"
		run(fixture.Prepared(fixture.Exec(q, 3, int64(7))))
		run(fixture.GroupingSetsBag("SELECT k,g,n FROM gs_review_smoke", []string{"k", "g", "n"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(7), int64(0), int64(3)}, {int64(7), int64(0), int64(3)}, {nil, int64(1), int64(3)}}))
	})
}
