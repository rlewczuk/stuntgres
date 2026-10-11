package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeUpdateFieldAssignmentBoundary(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TYPE merge_pair AS (a INT,b INT)", 0))
		run(fixture.Exec("CREATE TABLE t(id INT,v merge_pair)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,'(10,20)')", 1))
		q := "MERGE INTO t USING (VALUES(1,7)) s(id,x) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v.a=s.x"
		want := "(10,20)"
		if b.PG {
			run(fixture.Exec(q, 1))
			want = "(7,20)"
		} else {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "field assignments are unsupported", Position: fixture.Position(strings.Index(q, "v.a") + 1)})
			s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
			run(s)
		}
		run(fixture.Select("SELECT v::text AS payload FROM t", []string{"payload"}, []string{"TEXT"}, [][]any{{want}}))
	})
}
