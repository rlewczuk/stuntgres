package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteReturningCrossKindDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db, r := backend.DB, backend.Recorder
			fixture.LockExec(t, p.Ctx, db, "CREATE TABLE t(id INT NOT NULL,v TEXT NOT NULL)", 0)
			fixture.LockExec(t, p.Ctx, db, "INSERT INTO t VALUES(2,'payload'),(2,'payload')", 2)
			for _, tc := range []struct {
				query string
				want  fixture.Diagnostic
			}{
				{"DELETE FROM t RETURNING WITH (OLD AS new) old.id", fixture.Diagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "old"`, Position: "43"}},
				{"DELETE FROM t RETURNING WITH (NEW AS old) new.id", fixture.Diagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "new"`, Position: "43"}},
				{"DELETE FROM t RETURNING WITH (OLD AS new, NEW AS new) id", fixture.Diagnostic{Code: "42712", Message: `table name "new" specified more than once`, Position: "43"}},
			} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.query, tc.want)
					s.Prepared = prepared
					s.SelectRows = true
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, db, r, s)
					s = fixture.Select("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "payload"}, {int64(2), "payload"}})
					s.Bag = true
					fixture.RunStep(t, p.Ctx, db, r, s)
				}
			}
		})
	}
}
