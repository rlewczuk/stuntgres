//go:build reference

package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceCommentInquiryComposition(t *testing.T) {
	for _, backend := range []string{"Oracle", "Native"} {
		t.Run(backend, func(t *testing.T) {
			p := newReferencePair(t)
			db, rec := p.postgres, p.postgresRecord
			if backend == "Native" {
				db, rec = p.memory, p.memoryRecord
			}
			run := func(s referenceStep) { t.Helper(); referenceRunStep(t, p.ctx, db, rec, s) }
			run(referenceExec("CREATE TABLE t(a INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1),(1),(2)", 3))
			run(referenceExec("COMMENT ON TABLE t IS 'whole'", 0))
			grouped := referenceSelect("SELECT obj_description('t'::regclass,'pg_class') AS description,count(*) AS n FROM t GROUP BY obj_description('t'::regclass,'pg_class')", []string{"description", "n"}, []string{"TEXT", "INT8"}, [][]any{{"whole", int64(3)}})
			run(grouped)
			run(referencePrepared(grouped))
			for _, tc := range []struct{ name, expression string }{
				{"null_column", "col_description(NULL,1)"},
				{"null_local", "obj_description(NULL,'pg_class')"},
				{"null_legacy", "obj_description(NULL)"},
				{"null_shared", "shobj_description(NULL,'pg_authid')"},
				{"null_case", "CASE WHEN a=1 THEN obj_description(NULL,'pg_class') ELSE col_description(NULL,1) END"},
			} {
				run(referenceExec("CREATE VIEW "+tc.name+" AS SELECT "+tc.expression+" AS description FROM t", 0))
				s := referenceSelect(fmt.Sprintf("SELECT description FROM %s", tc.name), []string{"description"}, []string{"TEXT"}, [][]any{{nil}, {nil}, {nil}})
				run(s)
				run(referencePrepared(s))
			}
		})
	}
}
