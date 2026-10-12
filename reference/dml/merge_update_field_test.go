package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeUpdateFields(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TYPE merge_pair AS (a INT,b INT)", 0))
		run(fixture.Exec("CREATE TABLE t(id INT,v merge_pair)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,'(10,20)')", 1))
		q := "MERGE INTO t USING (VALUES(1,7)) s(id,x) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v.a=s.x"
		run(fixture.Exec(q, 1))
		run(fixture.Select("SELECT v::text AS payload FROM t", []string{"payload"}, []string{"TEXT"}, [][]any{{"(7,20)"}}))
	})
}
