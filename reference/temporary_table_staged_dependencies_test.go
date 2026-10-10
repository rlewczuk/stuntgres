package reference_test

import "testing"

func TestReferenceTemporaryTableNamedIdentity(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		s.utility("CREATE TEMP TABLE named_identity(v INT GENERATED ALWAYS AS IDENTITY (SEQUENCE NAME pg_temp.explicit_identity))", nil)
		s.exec(s.c, "INSERT INTO named_identity DEFAULT VALUES", 1)
		temporaryTableRows(s, s.c, "named_identity", 1)
		s.utility("DROP TABLE named_identity", nil)
		s.missing(s.c, "explicit_identity", "last_value", "24")
		s.utility("CREATE TEMP TABLE named_altered(v INT NOT NULL)", nil)
		s.utility("ALTER TABLE named_altered ALTER v ADD GENERATED ALWAYS AS IDENTITY(SEQUENCE NAME pg_temp.explicit_altered)", nil)
		s.exec(s.c, "INSERT INTO named_altered DEFAULT VALUES", 1)
		temporaryTableRows(s, s.c, "named_altered", 1)
		s.utility("DROP TABLE named_altered", nil)
		s.missing(s.c, "explicit_altered", "last_value", "24")
	})
}

func TestReferenceTemporaryTableSameStageDefault(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		s.utility("CREATE TEMP TABLE same_stage(v SERIAL,other INT DEFAULT nextval('pg_temp.same_stage_v_seq'))", nil)
		s.exec(s.c, "INSERT INTO same_stage DEFAULT VALUES", 1)
		s.exec(s.c, "INSERT INTO same_stage DEFAULT VALUES", 1)
		step := referenceSelect("SELECT v,other FROM same_stage ORDER BY v", []string{"v", "other"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(3), int64(4)}})
		step.prepared = s.prepared
		referenceRunStep(s.t, s.p.ctx, s.c, s.record, step)
		s.utility("DROP TABLE same_stage", nil)
		s.missing(s.c, "same_stage_v_seq", "last_value", "24")
	})
}
