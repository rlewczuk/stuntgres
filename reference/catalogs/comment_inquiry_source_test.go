package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCommentInquiryImplicitSource(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { t.Helper(); commentInquiryStep(t, p, backend, backend.DB, s) }
			run(fixture.Exec("CREATE TABLE t(a INT,b TEXT,c BIGINT)", 0))
			run(fixture.Exec("COMMENT ON TABLE t IS 'whole'", 0))
			run(fixture.Exec("INSERT INTO t(a) VALUES(1),(2)", 2))
			s := fixture.Select("SELECT CASE WHEN a=1 THEN obj_description('t'::regclass,'pg_class') ELSE col_description(NULL,1) END AS description FROM t ORDER BY a", []string{"description"}, []string{"TEXT"}, [][]any{{"whole"}, {nil}})
			run(s)
			run(fixture.Prepared(s))
		})
	}
}
