package reference_test

import "testing"

func TestReferenceTemporaryTableDropDependents(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		s.exec(s.c, "BEGIN", 0)
		s.utility("CREATE TEMP TABLE drop_owner(id SERIAL) ON COMMIT DROP", nil)
		s.utility("CREATE TEMP TABLE drop_survivor(v INT DEFAULT nextval('drop_owner_id_seq'),CONSTRAINT survivor_guard CHECK(v>0 AND nextval('drop_owner_id_seq')>0))", nil)
		s.exec(s.c, "INSERT INTO drop_survivor DEFAULT VALUES", 1)
		temporaryTableRows(s, s.c, "drop_survivor", 1)
		s.exec(s.c, "SET search_path=pg_temp,public", 0)
		namespace := tempReferenceNamespace(s.t, s, false)
		s.exec(s.c, "SET search_path=public", 0)
		s.exec(s.c, "SAVEPOINT before_bad", 0)
		want := tempReferenceDiagnostic("23514", `new row for relation "drop_survivor" violates check constraint "survivor_guard"`, "", "")
		want.Detail, want.Schema, want.Table, want.Constraint = "Failing row contains (-1).", namespace, "drop_survivor", "survivor_guard"
		failure := referenceImplicitError("INSERT INTO drop_survivor(v) VALUES(-1)", *want)
		failure.prepared, failure.errorStage = s.prepared, "execute"
		referenceRunStep(s.t, s.p.ctx, s.c, s.record, failure)
		s.exec(s.c, "ROLLBACK TO before_bad", 0)
		temporaryTableRows(s, s.c, "drop_survivor", 1)
		s.exec(s.c, "COMMIT", 0)
		s.missing(s.c, "drop_owner", "id", "16")
		s.missing(s.c, "drop_owner_id_seq", "last_value", "24")
		temporaryTableRows(s, s.c, "drop_survivor", 1)
		// The dependent CHECK was enforced before commit; its removal now permits -1.
		s.exec(s.c, "INSERT INTO drop_survivor(v) VALUES(-1)", 1)
		// The dependent closed DEFAULT was removed, not rebound to a missing name.
		s.exec(s.c, "INSERT INTO drop_survivor DEFAULT VALUES", 1)
		rows := referenceSelect("SELECT v FROM drop_survivor ORDER BY v NULLS LAST", []string{"v"}, []string{"INT4"}, [][]any{{int64(-1)}, {int64(1)}, {nil}})
		rows.prepared = s.prepared
		referenceRunStep(s.t, s.p.ctx, s.c, s.record, rows)
	})
}
