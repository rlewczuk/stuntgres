package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTemporaryTableDropDependents(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		s.Exec(s.C, "BEGIN", 0)
		s.Utility("CREATE TEMP TABLE drop_owner(id SERIAL) ON COMMIT DROP", nil)
		s.Utility("CREATE TEMP TABLE drop_survivor(v INT DEFAULT nextval('drop_owner_id_seq'),CONSTRAINT survivor_guard CHECK(v>0 AND nextval('drop_owner_id_seq')>0))", nil)
		s.Exec(s.C, "INSERT INTO drop_survivor DEFAULT VALUES", 1)
		fixture.TemporaryTableRows(s, s.C, "drop_survivor", 1)
		s.Exec(s.C, "SET search_path=pg_temp,public", 0)
		namespace := fixture.TempReferenceNamespace(s.T, s, false)
		s.Exec(s.C, "SET search_path=public", 0)
		s.Exec(s.C, "SAVEPOINT before_bad", 0)
		want := fixture.TempReferenceDiagnostic("23514", `new row for relation "drop_survivor" violates check constraint "survivor_guard"`, "", "")
		want.Detail, want.Schema, want.Table, want.Constraint = "Failing row contains (-1).", namespace, "drop_survivor", "survivor_guard"
		failure := fixture.ImplicitError("INSERT INTO drop_survivor(v) VALUES(-1)", *want)
		failure.Prepared, failure.ErrorStage = s.Prepared, "execute"
		fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, failure)
		s.Exec(s.C, "ROLLBACK TO before_bad", 0)
		fixture.TemporaryTableRows(s, s.C, "drop_survivor", 1)
		s.Exec(s.C, "COMMIT", 0)
		s.Missing(s.C, "drop_owner", "id", "16")
		s.Missing(s.C, "drop_owner_id_seq", "last_value", "24")
		fixture.TemporaryTableRows(s, s.C, "drop_survivor", 1)
		// The dependent CHECK was enforced before commit; its removal now permits -1.
		s.Exec(s.C, "INSERT INTO drop_survivor(v) VALUES(-1)", 1)
		// The dependent closed DEFAULT was removed, not rebound to a missing name.
		s.Exec(s.C, "INSERT INTO drop_survivor DEFAULT VALUES", 1)
		rows := fixture.Select("SELECT v FROM drop_survivor ORDER BY v NULLS LAST", []string{"v"}, []string{"INT4"}, [][]any{{int64(-1)}, {int64(1)}, {nil}})
		rows.Prepared = s.Prepared
		fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, rows)
	})
}
