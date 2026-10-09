package reference_test

import "testing"

func TestReferenceCommentInquiryImplicitSource(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { t.Helper(); commentInquiryStep(t, p, backend, backend.db, s) }
			run(referenceExec("CREATE TABLE t(a INT,b TEXT,c BIGINT)", 0))
			run(referenceExec("COMMENT ON TABLE t IS 'whole'", 0))
			run(referenceExec("INSERT INTO t(a) VALUES(1),(2)", 2))
			s := referenceSelect("SELECT CASE WHEN a=1 THEN obj_description('t'::regclass,'pg_class') ELSE col_description(NULL,1) END AS description FROM t ORDER BY a", []string{"description"}, []string{"TEXT"}, [][]any{{"whole"}, {nil}})
			run(s)
			run(referencePrepared(s))
		})
	}
}
