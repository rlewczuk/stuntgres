package reference_test

import "testing"

// Supported subset enforcement retains the former boundary's state and routes.
func TestReferencePartialIndexUniqueEnforcement(t *testing.T) {
	partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE u(id INT,k INT,active BOOL)", 0))
		run(referenceExec("INSERT INTO u VALUES(1,1,FALSE),(2,1,NULL)", 2))
		for _, prefix := range []string{"", "/*é*/ "} {
			for _, plain := range []bool{false, true} {
				q := prefix + "CREATE UNIQUE INDEX pu ON u(k) WHERE active"
				s := referenceExec(q, 0)
				s.nonContext = plain
				run(s)
				run(referenceExec("DROP INDEX pu", 0))
				s = referencePrepared(referenceExec(q, 0))
				s.nonContext = plain
				run(s)
				run(referenceExec("DROP INDEX pu", 0))
			}
		}
		run(referenceSelect("SELECT relname FROM pg_class WHERE relname='pu'", []string{"relname"}, []string{"NAME"}, nil))
		run(partialIndexIDs("SELECT id FROM u ORDER BY id", 1, 2))
		run(referencePrepared(referenceExec("CREATE UNIQUE INDEX pu ON u(k) WHERE active", 0)))
		run(referenceExec("INSERT INTO u VALUES(3,1,TRUE)", 1))
		duplicate := uniqueReferenceDuplicate("u", "pu", "Key (k)=(1) already exists.")
		for _, q := range []string{"INSERT INTO u VALUES(4,2,TRUE),(5,1,TRUE)", "UPDATE u SET active=TRUE WHERE id=1 OR id=2"} {
			s := referenceErrorDiagnostic(q, duplicate)
			s.errorStage = "execute"
			run(s)
			run(referencePrepared(s))
			run(partialIndexIDs("SELECT id FROM u ORDER BY id", 1, 2, 3))
			run(partialIndexIDs("SELECT id FROM u WHERE active ORDER BY id", 3))
		}
		run(referenceExec("UPDATE u SET active=FALSE WHERE id=3", 1))
		run(referenceExec("UPDATE u SET active=TRUE WHERE id=1", 1))
		run(partialIndexIDs("SELECT id FROM u WHERE active ORDER BY id", 1))
		run(referenceExec("INSERT INTO u VALUES(4,1,FALSE),(5,1,NULL)", 2))
		run(referenceExec("DROP INDEX pu", 0))
		run(referenceExec("UPDATE u SET active=TRUE WHERE id=2", 1))
		build := controlsReferenceDiagnostic("23505", `could not create unique index "pu"`)
		build.Detail = "Key (k)=(1) is duplicated."
		build.Schema = "public"
		build.Table = "u"
		build.Constraint = "pu"
		before := ""
		if !b.pg {
			before = partialIndexDump(t, p)
		}
		run(referenceErrorDiagnostic("CREATE UNIQUE INDEX pu ON u(k) WHERE active", build))
		if !b.pg && partialIndexDump(t, p) != before {
			t.Fatal("failed unique build changed Dump")
		}
		run(referencePrepared(referenceErrorDiagnostic("CREATE UNIQUE INDEX pu ON u(k) WHERE active", build)))
		run(partialIndexIDs("SELECT id FROM u ORDER BY id", 1, 2, 3, 4, 5))
		run(referenceSelect("SELECT relname FROM pg_class WHERE relname='pu'", []string{"relname"}, []string{"NAME"}, nil))
		run(partialIndexIDs("SELECT id FROM u WHERE active ORDER BY id", 1, 2))
		run(referenceExec("UPDATE u SET active=NULL WHERE id=2", 1))
		run(referenceExec("CREATE UNIQUE INDEX pu ON u(k) WHERE active", 0))
		run(referenceErrorDiagnostic("UPDATE u SET active=TRUE WHERE id=5", duplicate))
		run(partialIndexIDs("SELECT id FROM u WHERE active ORDER BY id", 1))
	})
}
