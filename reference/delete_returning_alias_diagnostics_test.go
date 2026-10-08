package reference_test

import "testing"

func TestReferenceDeleteReturningCrossKindDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db, r := backend.db, backend.recorder
			referenceLockExec(t, p.ctx, db, "CREATE TABLE t(id INT NOT NULL,v TEXT NOT NULL)", 0)
			referenceLockExec(t, p.ctx, db, "INSERT INTO t VALUES(2,'payload'),(2,'payload')", 2)
			for _, tc := range []struct {
				query string
				want  referenceDiagnostic
			}{
				{"DELETE FROM t RETURNING WITH (OLD AS new) old.id", referenceDiagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "old"`, Position: "43"}},
				{"DELETE FROM t RETURNING WITH (NEW AS old) new.id", referenceDiagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "new"`, Position: "43"}},
				{"DELETE FROM t RETURNING WITH (OLD AS new, NEW AS new) id", referenceDiagnostic{Code: "42712", Message: `table name "new" specified more than once`, Position: "43"}},
			} {
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError(tc.query, tc.want)
					s.prepared = prepared
					s.selectRows = true
					s.errorStage = "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					referenceRunStep(t, p.ctx, db, r, s)
					s = referenceSelect("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "payload"}, {int64(2), "payload"}})
					s.bag = true
					referenceRunStep(t, p.ctx, db, r, s)
				}
			}
		})
	}
}
