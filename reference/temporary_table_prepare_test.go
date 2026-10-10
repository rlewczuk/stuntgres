package reference_test

import "testing"

func TestReferenceTemporaryTablePrepare(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		s.exec(s.c, "BEGIN", 0)
		absentDrop := s.held(s.c, "DROP TABLE pg_temp.prepared_absent")
		if err := absentDrop.statement.Close(); err != nil {
			s.t.Fatal(err)
		}
		q := "CREATE TEMP TABLE prepared_quiet(v INT) ON COMMIT DROP"
		held := s.held(s.c, q)
		defer held.statement.Close()
		referenceRunStep(s.t, s.p.ctx, s.c, s.record, referenceSelect("SELECT relname FROM pg_class WHERE relname='prepared_quiet'", []string{"relname"}, []string{"NAME"}, nil))
		referenceRunStep(s.t, s.p.ctx, held, s.record, referenceExec(q, 0))
		s.exec(s.c, "INSERT INTO prepared_quiet VALUES(11)", 1)
		temporaryTableRows(s, s.c, "prepared_quiet", 11)
		s.exec(s.c, "COMMIT", 0)
		s.missing(s.c, "prepared_quiet", "v", "15")
	})
}
