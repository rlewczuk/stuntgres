package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Supported subset enforcement retains the former boundary's state and routes.
func TestReferencePartialIndexUniqueEnforcement(t *testing.T) {
	partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE u(id INT,k INT,active BOOL)", 0))
		run(fixture.Exec("INSERT INTO u VALUES(1,1,FALSE),(2,1,NULL)", 2))
		for _, prefix := range []string{"", "/*é*/ "} {
			for _, plain := range []bool{false, true} {
				q := prefix + "CREATE UNIQUE INDEX pu ON u(k) WHERE active"
				s := fixture.Exec(q, 0)
				s.NonContext = plain
				run(s)
				run(fixture.Exec("DROP INDEX pu", 0))
				s = fixture.Prepared(fixture.Exec(q, 0))
				s.NonContext = plain
				run(s)
				run(fixture.Exec("DROP INDEX pu", 0))
			}
		}
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='pu'", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.PartialIndexIDs("SELECT id FROM u ORDER BY id", 1, 2))
		run(fixture.Prepared(fixture.Exec("CREATE UNIQUE INDEX pu ON u(k) WHERE active", 0)))
		run(fixture.Exec("INSERT INTO u VALUES(3,1,TRUE)", 1))
		duplicate := fixture.UniqueReferenceDuplicate("u", "pu", "Key (k)=(1) already exists.")
		for _, q := range []string{"INSERT INTO u VALUES(4,2,TRUE),(5,1,TRUE)", "UPDATE u SET active=TRUE WHERE id=1 OR id=2"} {
			s := fixture.ErrorDiagnostic(q, duplicate)
			s.ErrorStage = "execute"
			run(s)
			run(fixture.Prepared(s))
			run(fixture.PartialIndexIDs("SELECT id FROM u ORDER BY id", 1, 2, 3))
			run(fixture.PartialIndexIDs("SELECT id FROM u WHERE active ORDER BY id", 3))
		}
		run(fixture.Exec("UPDATE u SET active=FALSE WHERE id=3", 1))
		run(fixture.Exec("UPDATE u SET active=TRUE WHERE id=1", 1))
		run(fixture.PartialIndexIDs("SELECT id FROM u WHERE active ORDER BY id", 1))
		run(fixture.Exec("INSERT INTO u VALUES(4,1,FALSE),(5,1,NULL)", 2))
		run(fixture.Exec("DROP INDEX pu", 0))
		run(fixture.Exec("UPDATE u SET active=TRUE WHERE id=2", 1))
		build := fixture.ControlsReferenceDiagnostic("23505", `could not create unique index "pu"`)
		build.Detail = "Key (k)=(1) is duplicated."
		build.Schema = "public"
		build.Table = "u"
		build.Constraint = "pu"
		before := ""
		if !b.PG {
			before = partialIndexDump(t, p)
		}
		run(fixture.ErrorDiagnostic("CREATE UNIQUE INDEX pu ON u(k) WHERE active", build))
		if !b.PG && partialIndexDump(t, p) != before {
			t.Fatal("failed unique build changed Dump")
		}
		run(fixture.Prepared(fixture.ErrorDiagnostic("CREATE UNIQUE INDEX pu ON u(k) WHERE active", build)))
		run(fixture.PartialIndexIDs("SELECT id FROM u ORDER BY id", 1, 2, 3, 4, 5))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='pu'", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.PartialIndexIDs("SELECT id FROM u WHERE active ORDER BY id", 1, 2))
		run(fixture.Exec("UPDATE u SET active=NULL WHERE id=2", 1))
		run(fixture.Exec("CREATE UNIQUE INDEX pu ON u(k) WHERE active", 0))
		run(fixture.ErrorDiagnostic("UPDATE u SET active=TRUE WHERE id=5", duplicate))
		run(fixture.PartialIndexIDs("SELECT id FROM u WHERE active ORDER BY id", 1))
	})
}
