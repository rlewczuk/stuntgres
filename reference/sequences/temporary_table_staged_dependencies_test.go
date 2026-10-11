package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTemporaryTableNamedIdentity(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		s.Utility("CREATE TEMP TABLE named_identity(v INT GENERATED ALWAYS AS IDENTITY (SEQUENCE NAME pg_temp.explicit_identity))", nil)
		s.Exec(s.C, "INSERT INTO named_identity DEFAULT VALUES", 1)
		fixture.TemporaryTableRows(s, s.C, "named_identity", 1)
		s.Utility("DROP TABLE named_identity", nil)
		s.Missing(s.C, "explicit_identity", "last_value", "24")
		s.Utility("CREATE TEMP TABLE named_altered(v INT NOT NULL)", nil)
		s.Utility("ALTER TABLE named_altered ALTER v ADD GENERATED ALWAYS AS IDENTITY(SEQUENCE NAME pg_temp.explicit_altered)", nil)
		s.Exec(s.C, "INSERT INTO named_altered DEFAULT VALUES", 1)
		fixture.TemporaryTableRows(s, s.C, "named_altered", 1)
		s.Utility("DROP TABLE named_altered", nil)
		s.Missing(s.C, "explicit_altered", "last_value", "24")
	})
}

func TestReferenceTemporaryTableSameStageDefault(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		s.Utility("CREATE TEMP TABLE same_stage(v SERIAL,other INT DEFAULT nextval('pg_temp.same_stage_v_seq'))", nil)
		s.Exec(s.C, "INSERT INTO same_stage DEFAULT VALUES", 1)
		s.Exec(s.C, "INSERT INTO same_stage DEFAULT VALUES", 1)
		step := fixture.Select("SELECT v,other FROM same_stage ORDER BY v", []string{"v", "other"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(3), int64(4)}})
		step.Prepared = s.Prepared
		fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, step)
		s.Utility("DROP TABLE same_stage", nil)
		s.Missing(s.C, "same_stage_v_seq", "last_value", "24")
	})
}
