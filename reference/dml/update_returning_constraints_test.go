package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningConstraintDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT NOT NULL CONSTRAINT positive CHECK(id>0),v TEXT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,'a'),(2,'b')", 2))
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			q string
			d fixture.Diagnostic
		}{
			{"UPDATE t SET id=NULL WHERE id=1 RETURNING old.id,new.id", fixture.Diagnostic{Code: "23502", Message: `null value in column "id" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, a).", Schema: "public", Table: "t", Column: "id"}},
			{"UPDATE t SET id=0 WHERE id=1 RETURNING old.id,new.id", fixture.Diagnostic{Code: "23514", Message: `new row for relation "t" violates check constraint "positive"`, Detail: "Failing row contains (0, a).", Schema: "public", Table: "t", Constraint: "positive"}},
			{"UPDATE t SET id=id+1 RETURNING old.id+2147483647", fixture.Diagnostic{Code: "22003", Message: "integer out of range"}},
		} {
			s := fixture.ImplicitError(tc.q, tc.d)
			s.Prepared = prepared
			s.SelectRows = true
			s.ErrorStage = "execute"
			p.Step(t, s)
		}
	}
	p.Step(t, fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "b"}}))
}
