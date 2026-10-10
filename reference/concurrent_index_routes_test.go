package reference_test

import "testing"

func TestReferenceBtreeIndexOrdinaryShareBuild(t *testing.T) {
	TestReferenceConcurrentIndexOrdinaryBoundary(t)
}

func TestReferenceConcurrentIndexPublicRoutes(t *testing.T) {
	for _, plain := range []bool{false, true} {
		t.Run(map[bool]string{false: "context", true: "plain"}[plain], func(t *testing.T) {
			concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				run := func(s referenceStep) { s.nonContext = plain; referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
				run(referenceExec("CREATE TABLE t(n INT,c INT,s TEXT)", 0))
				run(referenceExec("INSERT INTO t VALUES(2,1,'one')", 1))
				for _, prepared := range []bool{false, true} {
					s := referenceExec("CREATE INDEX CONCURRENTLY i ON t(n)", 0)
					if prepared {
						s = referencePrepared(s)
					}
					run(s)
					run(referenceSelect("SELECT relname FROM pg_class WHERE relname='i'", []string{"relname"}, []string{"NAME"}, [][]any{{"i"}}))
					run(referenceSelect("SELECT relname,relkind FROM pg_class WHERE relname='i'", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, [][]any{{"i", "i"}}))
					run(referenceExec("DROP INDEX i", 0))
				}
				run(referenceSelect("SELECT relname FROM pg_class WHERE relname='i' OR relname='ix'", []string{"relname"}, []string{"NAME"}, nil))
				run(referenceExec("CREATE INDEX i ON t(n)", 0))
				run(referencePrepared(referenceExec("INSERT INTO t VALUES(3,2,'two')", 1)))
				run(referenceSelect("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}))
				run(referenceExec("DELETE FROM t WHERE n=3", 1))
				run(referenceSelect("SELECT c,s FROM t", []string{"c", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
				run(referenceExec("ALTER TABLE t ALTER c TYPE TEXT USING c::text", 0))
				run(referenceSelect("SELECT c FROM t", []string{"c"}, []string{"TEXT"}, [][]any{{"1"}}))
			})
		})
	}
}
