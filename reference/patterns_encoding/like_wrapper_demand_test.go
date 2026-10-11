package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceLikeWrapperDemand(t *testing.T) {
	p := fixture.NewScenario(t)
	run := func(t *testing.T, s fixture.Step) {
		s.CheckMetadata = likeReferenceMetadata
		p.Step(t, s)
	}
	run(t, fixture.Exec("CREATE TABLE t(c TEXT COLLATE \"C\",p TEXT COLLATE \"POSIX\")", 0))
	run(t, fixture.Exec("INSERT INTO t VALUES('a','z')", 1))
	for state := range 3 {
		if state == 1 {
			run(t, fixture.Exec("UPDATE t SET c=NULL", 1))
		}
		if state == 2 {
			run(t, fixture.Exec("UPDATE t SET p=NULL", 1))
		}
		for _, function := range []string{"NULLIF", "GREATEST", "LEAST"} {
			q := "SELECT 'a' LIKE " + function + "(c,p) AS matched FROM t"
			t.Run(q, func(t *testing.T) {
				s := fixture.Select(q, []string{"matched"}, []string{"BOOL"}, [][]any{{nil}})
				if state == 0 || state == 1 && function != "NULLIF" {
					message := "could not determine which collation to use for string comparison"
					if state == 1 {
						message = "could not determine which collation to use for LIKE"
					}
					s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P22", Message: message, Hint: "Use the COLLATE clause to set the collation explicitly."})
					s.SelectRows = true
					s.ErrorStage = "execute"
				}
				run(t, s)
				run(t, fixture.Prepared(s))
			})
		}
	}
	for _, expr := range []string{"COALESCE(pg_catalog.like_escape(c,p),'a')", "NULLIF(c,p)", "GREATEST(c,p)", "LEAST(c,p)"} {
		q := "CREATE VIEW escaped_pattern AS SELECT " + expr + " AS pattern FROM t"
		t.Run(q, func(t *testing.T) {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P22", Message: "could not determine which collation to use for view column \"pattern\"", Hint: "Use the COLLATE clause to set the collation explicitly."})
			s.ErrorStage = "execute"
			run(t, s)
			run(t, fixture.Prepared(s))
		})
	}
	run(t, fixture.Exec("INSERT INTO t VALUES('a','a')", 1))
	run(t, fixture.Exec("CREATE VIEW escaped_pattern AS SELECT CAST(p AS TEXT) AS pattern FROM t", 0))
	s := fixture.Select("SELECT 'a' LIKE pattern AS matched FROM escaped_pattern ORDER BY pattern", []string{"matched"}, []string{"BOOL"}, [][]any{{true}, {nil}})
	run(t, s)
	run(t, fixture.Prepared(s))
	conflict := fixture.ImplicitError("SELECT a.c LIKE b.pattern FROM t a JOIN escaped_pattern b ON a.c IS NOT NULL AND b.pattern IS NOT NULL", fixture.Diagnostic{Code: "42P22", Message: "could not determine which collation to use for LIKE", Hint: "Use the COLLATE clause to set the collation explicitly."})
	conflict.SelectRows = true
	conflict.ErrorStage = "execute"
	run(t, conflict)
	run(t, fixture.Prepared(conflict))
}
