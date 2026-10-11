package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSimilarReviewGroupIdentity(t *testing.T) {
	p := fixture.NewScenario(t)
	forms := []string{`substring(v SIMILAR '%' ESCAPE '#')`, `substring(v FROM '%' FOR '#')`, `substring(v,'%','#')`, `pg_catalog.substring(v,'%','#')`}
	for _, b := range p.Backends() {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`CREATE TABLE grouped_source(v TEXT)`, 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`INSERT INTO grouped_source VALUES('a'),('a'),('b')`, 3))
		for _, projection := range forms {
			for _, grouping := range forms {
				q := fmt.Sprintf(`SELECT %s AS piece,count(*) AS n FROM grouped_source GROUP BY %s ORDER BY piece`, projection, grouping)
				similarReferenceRoutes(t, p, b, fixture.Select(q, []string{"piece", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {"b", int64(1)}}))
			}
		}
	}
}
