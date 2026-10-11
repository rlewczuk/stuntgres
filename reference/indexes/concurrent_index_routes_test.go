package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBtreeIndexOrdinaryShareBuild(t *testing.T) {
	TestReferenceConcurrentIndexOrdinaryBoundary(t)
}

func TestReferenceConcurrentIndexPublicRoutes(t *testing.T) {
	for _, plain := range []bool{false, true} {
		t.Run(map[bool]string{false: "context", true: "plain"}[plain], func(t *testing.T) {
			fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { s.NonContext = plain; fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE t(n INT,c INT,s TEXT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(2,1,'one')", 1))
				for _, prepared := range []bool{false, true} {
					s := fixture.Exec("CREATE INDEX CONCURRENTLY i ON t(n)", 0)
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
					run(fixture.Select("SELECT relname FROM pg_class WHERE relname='i'", []string{"relname"}, []string{"NAME"}, [][]any{{"i"}}))
					run(fixture.Select("SELECT relname,relkind FROM pg_class WHERE relname='i'", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, [][]any{{"i", "i"}}))
					run(fixture.Exec("DROP INDEX i", 0))
				}
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='i' OR relname='ix'", []string{"relname"}, []string{"NAME"}, nil))
				run(fixture.Exec("CREATE INDEX i ON t(n)", 0))
				run(fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(3,2,'two')", 1)))
				run(fixture.Select("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}))
				run(fixture.Exec("DELETE FROM t WHERE n=3", 1))
				run(fixture.Select("SELECT c,s FROM t", []string{"c", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
				run(fixture.Exec("ALTER TABLE t ALTER c TYPE TEXT USING c::text", 0))
				run(fixture.Select("SELECT c FROM t", []string{"c"}, []string{"TEXT"}, [][]any{{"1"}}))
			})
		})
	}
}
