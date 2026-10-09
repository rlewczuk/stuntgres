package reference_test

import "testing"

func TestReferenceDeparseConstraintsNeverEvaluatesStoredTree(t *testing.T) {
	referenceCheckBackends(t, func(t *testing.T, run func(referenceStep)) {
		run(referenceExec("CREATE SEQUENCE s START 1", 0))
		run(referenceExec("CREATE TABLE t(a INT,CONSTRAINT c CHECK(nextval('s')>0))", 0))
		// These regclass presentation goldens are source-derived until the
		// root exercises this independent PostgreSQL stream.
		for _, prepared := range []bool{false, true} {
			for _, mode := range []struct {
				pretty bool
				want   string
			}{{false, "(nextval('s'::regclass) > 0)"}, {true, "nextval('s'::regclass) > 0"}} {
				s := referenceSelect("SELECT pg_get_expr(conbin,conrelid,$1) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", []string{"d"}, []string{"TEXT"}, [][]any{{mode.want}}, mode.pretty)
				s.prepared = prepared
				run(s)
			}
		}
		run(referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(referenceExec("ALTER SEQUENCE s RENAME TO renamed_sequence", 0))
		run(referencePrepared(referenceSelect("SELECT pg_get_expr(conbin,conrelid,true) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", []string{"d"}, []string{"TEXT"}, [][]any{{"nextval('renamed_sequence'::regclass) > 0"}})))
		run(referenceSelect("SELECT nextval('renamed_sequence')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(2)}}))
	})
}
