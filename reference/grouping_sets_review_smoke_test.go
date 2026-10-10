package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceGroupingSetsReviewFeatureSmoke(t *testing.T) {
	groupingSetsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
		groupingSetsFixture(run)
		valid := groupingSetsBag("SELECT GROUPING(a,b,v),count(*) FROM gs GROUP BY ROLLUP(a,(b,v))", []string{"grouping", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(0), int64(1)}, {int64(0), int64(1)}, {int64(0), int64(1)}, {int64(3), int64(2)}, {int64(3), int64(1)}, {int64(7), int64(3)}})
		groupingSetsBoth(run, valid)
		q := "SELECT $1::INT AS k,GROUPING($1::INT),count(*) FROM gs GROUP BY GROUPING SETS(($1::INT),($1::INT),())"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		for _, value := range []any{int64(7), nil, int64(7)} {
			s := groupingSetsBag(q, []string{"k", "grouping", "count"}, []string{"INT4", "INT4", "INT8"}, [][]any{{value, int64(0), int64(3)}, {value, int64(0), int64(3)}, {nil, int64(1), int64(3)}})
			s.args = []any{value}
			s.checkMetadata = groupingSetsReviewParameterMetadata(b)
			referenceRunStep(t, p.ctx, held, b.recorder, s)
		}
		q = "SELECT count(*) FROM gs GROUP BY ROLLUP(())"
		groupingSetsAnalysisError(run, groupingSetsError(q, "42601", "syntax error at or near \")\"", "))"))
		q = "INSERT INTO gs VALUES(9,9,9); SELECT count(*) FROM gs GROUP BY CUBE(GROUPING SETS((a),()))"
		groupingSetsAnalysisError(run, groupingSetsError(q, "42601", "syntax error at or near \"SETS\"", "SETS"))
		run(referenceSelect("SELECT count(*) FROM gs", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
		run(referenceExec("CREATE TABLE gs_review_smoke(k INT,g INT,n BIGINT)", 0))
		q = "INSERT INTO gs_review_smoke SELECT $1::INT,GROUPING($1::INT),count(*) FROM gs GROUP BY GROUPING SETS(($1::INT),($1::INT),())"
		run(referencePrepared(referenceExec(q, 3, int64(7))))
		run(groupingSetsBag("SELECT k,g,n FROM gs_review_smoke", []string{"k", "g", "n"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(7), int64(0), int64(3)}, {int64(7), int64(0), int64(3)}, {nil, int64(1), int64(3)}}))
	})
}
