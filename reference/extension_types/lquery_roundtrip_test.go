package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLqueryRoundTrips(t *testing.T) {
	for i, tc := range lqueryPositiveFixtures {
		t.Run(fmt.Sprintf("%02d", i), func(t *testing.T) {
			lqueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				lqueryInstall(t, p, b, c)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE lq_roundtrip(id integer NOT NULL,v lquery)", 0))
				literal := queryIntLiteral(tc.input)
				for _, q := range []string{"SELECT " + literal + "::lquery AS v", "SELECT CAST(" + literal + " AS lquery) AS v", "SELECT $1::lquery AS v"} {
					var args []any
					if q == "SELECT $1::lquery AS v" { args = []any{tc.input} }
					s := lquerySelect(b, q, []string{"v"}, [][]any{{tc.output}}, args...)
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { lqueryMetadata(t, b.PG, rows, []string{"v"}, nil) }
					run(s)
					run(fixture.Prepared(s))
				}
				run(fixture.Exec("INSERT INTO lq_roundtrip VALUES (1,"+literal+")", 1))
				run(fixture.Prepared(fixture.Exec("INSERT INTO lq_roundtrip VALUES (2,"+literal+")", 1)))
				run(fixture.Exec("INSERT INTO lq_roundtrip VALUES (3,$1)", 1, tc.input))
				run(fixture.Prepared(fixture.Exec("INSERT INTO lq_roundtrip VALUES (4,$1)", 1, []byte(tc.input))))
				run(fixture.Exec("INSERT INTO lq_roundtrip VALUES (5,NULL),(6,NULL)", 2))
				s := lquerySelect(b, "SELECT v FROM lq_roundtrip", []string{"v"}, [][]any{{tc.output}, {tc.output}, {tc.output}, {tc.output}, {nil}, {nil}})
				s.Bag = true
				run(s)
				run(fixture.Prepared(s))
			})
		})
	}
}
